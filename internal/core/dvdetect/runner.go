// Package dvdetect runs Dolby Vision RPU extraction against a video
// file by shelling out to ffmpeg + dovi_tool. Lives outside the
// engine package because the engine has the project-rule "no I/O"
// constraint; this package is pure I/O wrapper that feeds the
// resulting text into engine.ParseDvSummary.
//
// Two-process pipeline mirrors the upstream bash script
// (TRaSH/Starr-taggers/Radarr-DV-HDR-Tagarr/dv-hdr_tagarr.sh:180-197):
//
//	ffmpeg -loglevel error -i $FILE -c:v copy -vbsf hevc_mp4toannexb -f hevc -frames:v 100 - \
//	  | dovi_tool extract-rpu - -o $TMP_RPU
//	dovi_tool info -i $TMP_RPU --summary
//
// First 100 video frames are enough to reach the first DV RPU NAL —
// extracting the whole stream would be wasteful and slow. The RPU is
// written to a temp file so dovi_tool info can read it back; deleted
// after parsing.
package dvdetect

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"resolvarr/internal/core/engine"
)

// Runner shells out to the locally-installed ffmpeg + dovi_tool
// binaries and returns the parsed DvDetail. Configurable for testing
// (Stub field) and for users with non-standard binary locations.
//
// Defaults:
//   - DvBin / FfBin from Tools.DefaultPaths() (resolved at runner
//     creation; pinned thereafter so a later install doesn't shift
//     the binary mid-scan).
//   - Timeout 30s — bash uses 30s for extraction, 10s for analysis;
//     we collapse both into one wall-clock budget.
type Runner struct {
	DvBin   string
	FfBin   string
	Timeout time.Duration
	// Stub, when non-nil, replaces the actual exec.Command pipeline
	// with a canned summary string. Used by tests so we don't need
	// real ffmpeg/dovi_tool binaries on the test runner.
	Stub func(path string) (string, bool, error)
}

// ErrToolsMissing means one or both binaries aren't present at the
// configured path. Caller decides whether to surface this as a
// per-movie skip (preview mode) or a hard failure.
var ErrToolsMissing = errors.New("dv tools not installed")

// Detect runs the extraction pipeline against path and returns the
// parsed DvDetail. The bool return distinguishes three outcomes:
//
//   - (detail, true,  nil)  — RPU extraction produced a non-empty
//                              summary. detail may have empty fields
//                              if the summary text didn't match our
//                              parser regexes — caller still emits
//                              the base `dv` tag, just no detail tags.
//                              detail.Tags() returns empty slice in
//                              that case.
//   - (zero,   false, nil)  — extraction succeeded but the file had
//                              no DV RPU (legitimate "API said DV but
//                              the stream actually has none" case —
//                              caller emits no-dv tag)
//   - (zero,   false, err)  — extraction failed (ffmpeg error,
//                              missing binary, timeout, etc.) —
//                              caller decides retry vs skip vs fail
func (r Runner) Detect(ctx context.Context, path string) (engine.DvDetail, bool, error) {
	if r.Stub != nil {
		summary, ok, err := r.Stub(path)
		if err != nil || !ok {
			return engine.DvDetail{}, ok, err
		}
		return engine.ParseDvSummary(summary), true, nil
	}

	if r.DvBin == "" || r.FfBin == "" {
		return engine.DvDetail{}, false, ErrToolsMissing
	}
	if _, err := os.Stat(r.DvBin); err != nil {
		return engine.DvDetail{}, false, fmt.Errorf("%w: %s", ErrToolsMissing, r.DvBin)
	}
	if _, err := os.Stat(r.FfBin); err != nil {
		return engine.DvDetail{}, false, fmt.Errorf("%w: %s", ErrToolsMissing, r.FfBin)
	}
	if _, err := os.Stat(path); err != nil {
		return engine.DvDetail{}, false, fmt.Errorf("media file not readable: %w", err)
	}

	timeout := r.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	rpuTmp, err := os.CreateTemp("", "resolvarr-dv-rpu-*.bin")
	if err != nil {
		return engine.DvDetail{}, false, fmt.Errorf("create rpu tempfile: %w", err)
	}
	rpuPath := rpuTmp.Name()
	rpuTmp.Close()
	defer os.Remove(rpuPath)

	if err := r.extractRPU(ctx, path, rpuPath); err != nil {
		return engine.DvDetail{}, false, err
	}

	// Empty output = no DV RPU in the stream.
	st, err := os.Stat(rpuPath)
	if err != nil || st.Size() == 0 {
		return engine.DvDetail{}, false, nil
	}

	summary, err := r.summary(ctx, rpuPath)
	if err != nil {
		return engine.DvDetail{}, false, err
	}
	if strings.TrimSpace(summary) == "" {
		return engine.DvDetail{}, false, nil
	}
	return engine.ParseDvSummary(summary), true, nil
}

// extractRPU pipes ffmpeg's HEVC bitstream into dovi_tool extract-rpu.
// Both processes run concurrently under the supplied context.
//
// Pipe orchestration — both Wait calls run in goroutines so an early
// exit from EITHER side closes the pipe and unblocks the other:
//
//   - If dovi_tool exits first (parse error / RPU complete after first
//     few frames), `pw.CloseWithError` interrupts ffmpeg's stdin write
//     immediately. Without this ffmpeg blocks until the 30s timeout.
//   - If ffmpeg exits first (input file invalid / stream corrupt),
//     pw.Close() flushes EOF to dovi_tool's stdin so it finishes
//     processing what it has and exits cleanly.
//
// Stderr is captured into capped buffers (32 KiB each) so a misbehaving
// binary spewing logs can't OOM the container.
func (r Runner) extractRPU(ctx context.Context, mediaPath, outRPU string) error {
	ff := exec.CommandContext(ctx, r.FfBin,
		"-loglevel", "error",
		"-i", mediaPath,
		"-c:v", "copy",
		"-bsf:v", "hevc_mp4toannexb",
		"-f", "hevc",
		"-frames:v", "100",
		"-",
	)
	dv := exec.CommandContext(ctx, r.DvBin,
		"extract-rpu",
		"-",
		"-o", outRPU,
	)

	pr, pw := io.Pipe()
	ff.Stdout = pw
	dv.Stdin = pr

	const stderrCap = 32 * 1024
	ffErrBuf := newCappedBuffer(stderrCap)
	dvErrBuf := newCappedBuffer(stderrCap)
	ff.Stderr = ffErrBuf
	dv.Stderr = dvErrBuf

	if err := ff.Start(); err != nil {
		pw.Close()
		return fmt.Errorf("start ffmpeg: %w", err)
	}
	if err := dv.Start(); err != nil {
		_ = ff.Process.Kill()
		_ = ff.Wait()
		pw.Close()
		return fmt.Errorf("start dovi_tool extract-rpu: %w", err)
	}

	// Wait on both in parallel. dvDone fires first signals "close the
	// pipe so ffmpeg's pending writes unblock"; ffDone signals "EOF
	// to dovi_tool". Whichever happens first releases the other.
	type waitResult struct{ err error }
	dvCh := make(chan waitResult, 1)
	ffCh := make(chan waitResult, 1)
	go func() { dvCh <- waitResult{err: dv.Wait()} }()
	go func() {
		err := ff.Wait()
		// Always close the writer — flushes EOF to dovi_tool when ffmpeg
		// finished naturally; force-aborts the pipe if ffmpeg errored.
		if err != nil {
			pw.CloseWithError(err)
		} else {
			pw.Close()
		}
		ffCh <- waitResult{err: err}
	}()

	// Drain the channels — order doesn't matter, just collect both.
	var ffErr, dvErr error
	for got := 0; got < 2; got++ {
		select {
		case r := <-dvCh:
			dvErr = r.err
			// dovi_tool exited — if it errored AND ffmpeg is still going,
			// force the pipe closed with that error so ffmpeg unblocks.
			if r.err != nil {
				pw.CloseWithError(r.err)
			}
		case r := <-ffCh:
			ffErr = r.err
		}
	}

	if ffErr != nil {
		return fmt.Errorf("ffmpeg: %w (stderr: %s)", ffErr, strings.TrimSpace(ffErrBuf.String()))
	}
	if dvErr != nil {
		return fmt.Errorf("dovi_tool extract-rpu: %w (stderr: %s)", dvErr, strings.TrimSpace(dvErrBuf.String()))
	}
	return nil
}

// cappedBuffer wraps bytes.Buffer with an upper bound. Writes past
// the cap are silently truncated — sufficient for stderr capture
// where we only want the first few KiB for error-message context.
type cappedBuffer struct {
	buf bytes.Buffer
	cap int
}

func newCappedBuffer(cap int) *cappedBuffer { return &cappedBuffer{cap: cap} }

func (c *cappedBuffer) Write(p []byte) (int, error) {
	remaining := c.cap - c.buf.Len()
	if remaining <= 0 {
		return len(p), nil // pretend we accepted; truncate silently
	}
	if len(p) > remaining {
		c.buf.Write(p[:remaining])
		return len(p), nil
	}
	return c.buf.Write(p)
}

func (c *cappedBuffer) String() string { return c.buf.String() }

// summary runs dovi_tool info --summary and returns its stdout.
func (r Runner) summary(ctx context.Context, rpuPath string) (string, error) {
	cmd := exec.CommandContext(ctx, r.DvBin, "info", "-i", rpuPath, "--summary")
	out, err := cmd.Output()
	if err != nil {
		var stderr string
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			stderr = strings.TrimSpace(string(exitErr.Stderr))
		}
		return "", fmt.Errorf("dovi_tool info: %w (stderr: %s)", err, stderr)
	}
	return string(out), nil
}
