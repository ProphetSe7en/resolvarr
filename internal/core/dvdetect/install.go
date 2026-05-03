package dvdetect

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Tool versions + URLs are pinned here. Bump alongside CHANGELOG
// when upgrading. dovi_tool publishes a per-asset .sha256 sidecar
// on GitHub releases — values below are mirrored from the
// 2.1.2 release page.
//
// ffmpeg ships from BtbN/FFmpeg-Builds. They tag a rolling "latest"
// + dated builds; we pin the rolling tag for now and verify by
// running --version after extraction. SHA pinning for ffmpeg is a
// known gap — fix before v0.4 GHCR push (track via CHANGELOG).
const (
	DovitoolVersion = "2.1.2"

	// dovi_tool 2.1.2 SHA-256 from per-asset .sha256 sidecar files.
	dovitoolSHAx86  = "c200a08daefce49bb7de59a6daf852539cfc73c9de183f3ca6597fdc4de7ef80"
	dovitoolSHAarm  = "c15fe4367f0024b7b256711c2501e2b11f236d9e68f9b87c9feba6915ee8baf3"
	dovitoolURLBase = "https://github.com/quietvoid/dovi_tool/releases/download/"

	// ffmpeg from BtbN. Pin SHA before public release.
	ffmpegURL_x86 = "https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/ffmpeg-master-latest-linux64-gpl.tar.xz"
	ffmpegURL_arm = "https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/ffmpeg-master-latest-linuxarm64-gpl.tar.xz"
)

// Tools resolves the on-disk paths for ffmpeg + dovi_tool. Default
// install location is /config/tools — survives container restart +
// image upgrade since /config is the user's appdata mount.
//
// HTTPClient and VersionFn are testability hooks. Production code
// leaves them nil and gets the default behaviour (real network +
// real exec). Tests inject httptest.Server + canned version strings
// to cover SHA-mismatch + status-happy-path cases.
type Tools struct {
	Dir        string                                                                    // base directory; binaries go here directly
	HTTPClient *http.Client                                                               // optional; nil → 5min-timeout default
	VersionFn  func(ctx context.Context, bin, flag string) (string, error)               // optional; nil → real exec via runVersion
}

// DefaultTools returns a Tools rooted at /config/tools (or the
// supplied configDir + "/tools" — server passes the actual config
// dir from cfg so test/dev runs can use a different path).
func DefaultTools(configDir string) Tools {
	return Tools{Dir: filepath.Join(configDir, "tools")}
}

func (t Tools) DvBinPath() string { return filepath.Join(t.Dir, "dovi_tool") }
func (t Tools) FfBinPath() string { return filepath.Join(t.Dir, "ffmpeg") }

// ResolveDvBin / ResolveFfBin pick the runtime binary location.
// With the ENABLE_DV_TOOLS=true entrypoint flow, dovi_tool lands in
// /usr/local/bin and ffmpeg comes from apk into /usr/bin — both via
// $PATH. The legacy /config/tools/ location (from the now-removed
// Settings → Tools install button) is checked first as a fallback so
// existing test fixtures + any pre-existing manual installs still
// resolve. Returns "" when neither location yields a runnable binary.
func (t Tools) ResolveDvBin() string { return resolveBin(t.DvBinPath(), "dovi_tool") }
func (t Tools) ResolveFfBin() string { return resolveBin(t.FfBinPath(), "ffmpeg") }

func resolveBin(legacyPath, name string) string {
	if legacyPath != "" {
		if info, err := os.Stat(legacyPath); err == nil && !info.IsDir() {
			return legacyPath
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	return ""
}

// State captures what's currently installed at the tools directory.
// Used by /api/health/detailed + the UI install banner.
type State struct {
	Installed     bool   `json:"installed"`     // true when BOTH dovi_tool and ffmpeg are present and runnable
	DvBinPath     string `json:"dvBinPath"`
	DvVersion     string `json:"dvVersion,omitempty"`
	FfBinPath     string `json:"ffBinPath"`
	FfVersion     string `json:"ffVersion,omitempty"`
	LastError     string `json:"lastError,omitempty"`
}

// Status resolves both binaries (legacy /config/tools/ → $PATH) and
// invokes --version on each to confirm they're runnable. Quick
// (sub-100ms) so the UI can poll on a banner refresh.
func (t Tools) Status(ctx context.Context) State {
	dv := t.ResolveDvBin()
	ff := t.ResolveFfBin()
	s := State{DvBinPath: dv, FfBinPath: ff}
	versionFn := t.VersionFn
	if versionFn == nil {
		versionFn = runVersion
	}
	if dv != "" {
		if v, err := versionFn(ctx, dv, "--version"); err == nil {
			s.DvVersion = v
		}
	}
	if ff != "" {
		if v, err := versionFn(ctx, ff, "-version"); err == nil {
			s.FfVersion = v
		}
	}
	s.Installed = s.DvVersion != "" && s.FfVersion != ""
	return s
}

// Install downloads dovi_tool + ffmpeg into t.Dir, verifies SHA
// (where pinned), extracts, and chmod-pluses the binaries. Existing
// binaries are overwritten. Idempotent: re-running on an already-
// installed setup just re-downloads + replaces.
//
// progress, when non-nil, is called with a one-line status update
// after each step ("downloading dovi_tool…", "extracting ffmpeg…",
// "verifying…", "done"). Used by the API handler to stream progress
// back to the UI.
//
// Concurrent calls to Install on the same Tools instance produce
// undefined behaviour at the disk level (both writing to the same
// dovi_tool/ffmpeg path with O_TRUNC). The /api handler that wires
// this up MUST serialise via a mutex — there's no install-in-flight
// flag here.
func (t Tools) Install(ctx context.Context, progress func(string)) error {
	if progress == nil {
		progress = func(string) {}
	}
	if err := os.MkdirAll(t.Dir, 0o755); err != nil {
		return fmt.Errorf("create tools dir: %w", err)
	}

	progress("downloading dovi_tool " + DovitoolVersion)
	if err := t.installDoviTool(ctx); err != nil {
		return fmt.Errorf("dovi_tool install: %w", err)
	}
	progress("downloading ffmpeg")
	if err := t.installFfmpeg(ctx); err != nil {
		return fmt.Errorf("ffmpeg install: %w", err)
	}

	progress("verifying binaries")
	state := t.Status(ctx)
	if !state.Installed {
		return fmt.Errorf("verification failed (dvVer=%q ffVer=%q)", state.DvVersion, state.FfVersion)
	}
	progress("done")
	return nil
}

// Uninstall removes the binaries from t.Dir. Leaves the directory
// itself in place so repeated install/uninstall cycles don't churn
// permissions.
func (t Tools) Uninstall() error {
	for _, p := range []string{t.DvBinPath(), t.FfBinPath()} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", p, err)
		}
	}
	return nil
}

// installDoviTool downloads + verifies + extracts the dovi_tool
// musl static binary for the current architecture.
func (t Tools) installDoviTool(ctx context.Context) error {
	arch, archSlug, sha, err := doviAssetForArch()
	if err != nil {
		return err
	}
	url := dovitoolURLBase + DovitoolVersion + "/dovi_tool-" + DovitoolVersion + "-" + archSlug + ".tar.gz"
	body, err := t.fetchWithSHA(ctx, url, sha)
	if err != nil {
		return fmt.Errorf("fetch dovi_tool (%s): %w", arch, err)
	}
	defer body.Close()
	return extractTarGz(body, t.Dir, "dovi_tool", 0o755)
}

// installFfmpeg downloads + extracts the BtbN/FFmpeg-Builds static
// archive. SHA pin is a TODO — for now we trust the archive +
// verify by running --version after extraction.
func (t Tools) installFfmpeg(ctx context.Context) error {
	var url string
	switch runtime.GOARCH {
	case "amd64":
		url = ffmpegURL_x86
	case "arm64":
		url = ffmpegURL_arm
	default:
		return fmt.Errorf("ffmpeg install: unsupported arch %q", runtime.GOARCH)
	}
	body, err := t.fetchWithSHA(ctx, url, skipSHAVerification) // TODO before v0.4 GHCR push: pin a dated BtbN release + its SHA
	if err != nil {
		return fmt.Errorf("fetch ffmpeg: %w", err)
	}
	defer body.Close()
	// BtbN tarball is `tar.xz` and contains ffmpeg + ffprobe in a
	// versioned subdir. Extract only ffmpeg — the runner shells out
	// to ffmpeg only (matches bash). ffprobe can be added later if
	// any feature needs it (e.g. raw mediaInfo pull).
	return extractTarXz(body, t.Dir, []string{"ffmpeg"}, 0o755)
}

// doviAssetForArch returns the architecture slug + pinned SHA for
// the dovi_tool asset matching the current Go build.
func doviAssetForArch() (arch, slug, sha string, err error) {
	switch runtime.GOARCH {
	case "amd64":
		return "amd64", "x86_64-unknown-linux-musl", dovitoolSHAx86, nil
	case "arm64":
		return "arm64", "aarch64-unknown-linux-musl", dovitoolSHAarm, nil
	default:
		return "", "", "", fmt.Errorf("dovi_tool install: unsupported arch %q", runtime.GOARCH)
	}
}

// skipSHAVerification is the explicit sentinel for the SHA argument
// of fetchWithSHA when no pin exists yet. Using a named constant
// rather than the empty string forces caller intent at the call site
// — passing "" by accident no longer silently bypasses verification.
const skipSHAVerification = "<skip-sha-verification>"

// maxFetchBytes caps a single download to 200 MiB. Both dovi_tool
// (~12 MB) and ffmpeg-static (~50 MB) are well under this — the cap
// is purely to bound a server-side response that grows unexpectedly
// (compromised mirror, malicious redirect, etc).
const maxFetchBytes = 200 * 1024 * 1024

// fetchWithSHA downloads url into a temporary file and verifies the
// SHA-256 against the pinned value. Pass skipSHAVerification (not "")
// to deliberately bypass — the explicit sentinel makes the intent
// loud at the call site. Caps the response at 200 MiB so a runaway
// server can't fill the disk.
//
// Method on Tools so tests can inject a httptest.Server-backed
// http.Client via Tools.HTTPClient. Production callers leave the
// field nil and get the default 5-minute-timeout client.
func (t Tools) fetchWithSHA(ctx context.Context, url, shaHex string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	client := t.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Minute}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		resp.Body.Close()
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
	tmp, err := os.CreateTemp("", "tagarr-tools-*.bin")
	if err != nil {
		resp.Body.Close()
		return nil, err
	}
	hasher := sha256.New()
	w := io.MultiWriter(tmp, hasher)
	// LimitReader bounds the download size — a malicious mirror
	// returning gigabytes of garbage gets cut off at maxFetchBytes.
	limited := io.LimitReader(resp.Body, maxFetchBytes+1)
	n, err := io.Copy(w, limited)
	resp.Body.Close()
	if err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return nil, err
	}
	if n > maxFetchBytes {
		tmp.Close()
		os.Remove(tmp.Name())
		return nil, fmt.Errorf("response exceeded %d-byte cap from %s", maxFetchBytes, url)
	}
	switch shaHex {
	case "":
		// Empty shaHex was the old foot-gun; fail closed.
		tmp.Close()
		os.Remove(tmp.Name())
		return nil, fmt.Errorf("internal: empty SHA pin for %s — pass skipSHAVerification to opt out explicitly", url)
	case skipSHAVerification:
		// Caller chose to skip — log so the gap is visible. (No
		// log import here; relies on stderr flushing from the
		// containing process.)
	default:
		got := hex.EncodeToString(hasher.Sum(nil))
		if got != shaHex {
			tmp.Close()
			os.Remove(tmp.Name())
			return nil, fmt.Errorf("SHA-256 mismatch: got %s, want %s", got, shaHex)
		}
	}
	if _, err := tmp.Seek(0, 0); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return nil, err
	}
	// Wrap so Close also removes the tempfile.
	return &tempFileCloser{File: tmp}, nil
}

type tempFileCloser struct{ *os.File }

func (t *tempFileCloser) Close() error {
	name := t.File.Name()
	err := t.File.Close()
	os.Remove(name)
	return err
}

// maxEntryBytes caps a single tar entry's decompressed size at
// 256 MiB. ffmpeg static is ~80 MB, dovi_tool ~12 MB. The cap
// defends against decompression bombs (gzip/xz/tar all share this
// limit since extractFromTar is the final write boundary).
const maxEntryBytes = 256 * 1024 * 1024

// extractTarGz writes a single named entry from a gzipped tar
// archive into dstDir. Used for the dovi_tool tarball whose only
// payload is a single binary.
func extractTarGz(r io.Reader, dstDir, wantName string, mode os.FileMode) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()
	return extractFromTar(tar.NewReader(gz), dstDir, []string{wantName}, mode)
}

// extractTarXz writes named entries from an xz-compressed tar
// archive. xz support isn't in stdlib so we shell out to `xz -d`.
// Streams output through a pipe instead of buffering with .Output()
// — a multi-GB xz bomb could OOM the container otherwise. Per-entry
// size cap in extractFromTar is the final defence.
func extractTarXz(r io.Reader, dstDir string, wantNames []string, mode os.FileMode) error {
	tmp, err := os.CreateTemp("", "tagarr-tarxz-*.tar.xz")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	// LimitReader caps the compressed input at the same cap the
	// downloader uses — fetchWithSHA already enforces this, but a
	// belt-and-braces cap here covers the hypothetical "caller
	// passed a custom Reader" path.
	limited := io.LimitReader(r, maxFetchBytes+1)
	n, err := io.Copy(tmp, limited)
	tmp.Close()
	if err != nil {
		return err
	}
	if n > maxFetchBytes {
		return fmt.Errorf("xz input exceeded %d-byte cap", maxFetchBytes)
	}

	cmd := exec.Command("xz", "-dc", tmpPath)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("xz -dc start: %w", err)
	}
	extractErr := extractFromTar(tar.NewReader(stdout), dstDir, wantNames, mode)
	// Drain remaining stdout so xz doesn't block on a full pipe
	// after extractFromTar returned early.
	_, _ = io.Copy(io.Discard, stdout)
	if waitErr := cmd.Wait(); waitErr != nil && extractErr == nil {
		return fmt.Errorf("xz -dc: %w", waitErr)
	}
	return extractErr
}

// extractFromTar walks a tar archive and writes any entry whose
// basename matches one of wantNames into dstDir. BtbN's ffmpeg
// archive nests binaries under a versioned `bin/` subdir, so we
// match by basename rather than full path.
//
// Per-entry size cap (maxEntryBytes) defends against tar/gzip/xz
// bombs that decompress to gigabytes from a small input.
//
// filepath.Base on the entry name strips path components — even if
// a malicious archive contained `../../../bin/dovi_tool`, the result
// is `dovi_tool` and we filepath.Join with dstDir, so traversal is
// neutralised. Symlinks are skipped via the Typeflag != TypeReg
// check so an archive can't redirect us to /etc/something via a
// "dovi_tool → /etc/passwd" link.
//
// Loop breaks early once all wanted entries have been extracted —
// avoids reading 999 MB of trailing junk when binaries appear early
// in the archive.
func extractFromTar(tr *tar.Reader, dstDir string, wantNames []string, mode os.FileMode) error {
	want := make(map[string]bool, len(wantNames))
	for _, n := range wantNames {
		want[n] = true
	}
	found := make(map[string]bool, len(wantNames))
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		name := filepath.Base(hdr.Name)
		if !want[name] {
			continue
		}
		out := filepath.Join(dstDir, name)
		f, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
		if err != nil {
			return err
		}
		// Cap per-entry write at maxEntryBytes — limit-reader is
		// the bomb defence. Reading +1 byte tells us if the source
		// was at-cap vs over-cap.
		written, copyErr := io.Copy(f, io.LimitReader(tr, maxEntryBytes+1))
		f.Close()
		if copyErr != nil {
			os.Remove(out)
			return copyErr
		}
		if written > maxEntryBytes {
			os.Remove(out)
			return fmt.Errorf("archive entry %q exceeded %d-byte cap (decompression bomb?)", name, maxEntryBytes)
		}
		// chmod separately because OpenFile honors umask.
		if err := os.Chmod(out, mode); err != nil {
			return err
		}
		found[name] = true
		// Stop once everything we wanted is here — don't waste
		// time reading the rest of a (possibly huge) archive.
		if len(found) == len(want) {
			break
		}
	}
	for _, n := range wantNames {
		if !found[n] {
			return fmt.Errorf("expected entry %q not found in archive", n)
		}
	}
	return nil
}

// runVersion invokes a binary with the given version-flag argument
// and returns the first line of stdout. Captures with a 5-second
// timeout so a hung binary doesn't lock the status check.
func runVersion(ctx context.Context, bin, flag string) (string, error) {
	if _, err := os.Stat(bin); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, flag).Output()
	if err != nil {
		return "", err
	}
	first := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
	return first, nil
}
