package dvdetect

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var osReadFile = os.ReadFile

func TestDoviAssetForArch(t *testing.T) {
	arch, slug, sha, err := doviAssetForArch()
	switch runtime.GOARCH {
	case "amd64":
		if err != nil {
			t.Fatalf("expected amd64 to succeed, got err: %v", err)
		}
		if slug != "x86_64-unknown-linux-musl" {
			t.Errorf("slug = %q, want x86_64-unknown-linux-musl", slug)
		}
		if sha != dovitoolSHAx86 {
			t.Errorf("sha mismatch")
		}
		_ = arch
	case "arm64":
		if err != nil {
			t.Fatalf("expected arm64 to succeed, got err: %v", err)
		}
		if slug != "aarch64-unknown-linux-musl" {
			t.Errorf("slug = %q, want aarch64-unknown-linux-musl", slug)
		}
	default:
		if err == nil {
			t.Errorf("expected unsupported-arch error on %q", runtime.GOARCH)
		}
	}
}

func TestExtractTarGz_BasicEntry(t *testing.T) {
	// Build a minimal tar.gz containing a single file named "dovi_tool"
	// with known contents. Verifies the extraction code writes the
	// payload + sets exec mode.
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := []byte("#!/bin/sh\necho fake-dovi-tool\n")
	if err := tw.WriteHeader(&tar.Header{
		Name:     "dovi_tool",
		Mode:     0o755,
		Size:     int64(len(body)),
		Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()

	dst := t.TempDir()
	if err := extractTarGz(&buf, dst, "dovi_tool", 0o755); err != nil {
		t.Fatalf("extractTarGz: %v", err)
	}
	out := filepath.Join(dst, "dovi_tool")
	got, err := readFile(out)
	if err != nil {
		t.Fatalf("read extracted: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("payload mismatch — got %q, want %q", got, body)
	}
}

func TestExtractFromTar_NestedSubdirByBasename(t *testing.T) {
	// BtbN's ffmpeg archive nests binaries under
	// `ffmpeg-master-latest-linux64-gpl/bin/ffmpeg`. Verify we
	// match by basename across nested paths.
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	body := []byte("ffmpeg-binary-bytes")
	if err := tw.WriteHeader(&tar.Header{
		Name:     "ffmpeg-master-latest-linux64-gpl/bin/ffmpeg",
		Mode:     0o755,
		Size:     int64(len(body)),
		Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.WriteHeader(&tar.Header{
		Name:     "ffmpeg-master-latest-linux64-gpl/bin/ffprobe",
		Mode:     0o755,
		Size:     int64(len("probe")),
		Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("probe")); err != nil {
		t.Fatal(err)
	}
	// Add a noise entry the extractor should skip.
	if err := tw.WriteHeader(&tar.Header{
		Name:     "ffmpeg-master-latest-linux64-gpl/README.md",
		Mode:     0o644,
		Size:     int64(len("readme")),
		Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("readme")); err != nil {
		t.Fatal(err)
	}
	tw.Close()

	dst := t.TempDir()
	if err := extractFromTar(tar.NewReader(&buf), dst, []string{"ffmpeg", "ffprobe"}, 0o755); err != nil {
		t.Fatalf("extractFromTar: %v", err)
	}
	for _, name := range []string{"ffmpeg", "ffprobe"} {
		got, err := readFile(filepath.Join(dst, name))
		if err != nil {
			t.Errorf("read %s: %v", name, err)
			continue
		}
		if len(got) == 0 {
			t.Errorf("%s extracted empty", name)
		}
	}
	// README should not have been extracted.
	if _, err := readFile(filepath.Join(dst, "README.md")); err == nil {
		t.Error("README.md was extracted (should have been ignored)")
	}
}

func TestExtractFromTar_OversizedEntryRejected(t *testing.T) {
	// Build a tar entry whose declared Size exceeds maxEntryBytes —
	// extractFromTar must abort to defend against decompression
	// bombs. We don't actually write the bomb-sized payload (that
	// would be 256+ MB); the LimitReader catches it at copy time
	// after we write a few bytes past the cap.
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	huge := maxEntryBytes + 1024
	if err := tw.WriteHeader(&tar.Header{
		Name: "dovi_tool", Size: int64(huge), Mode: 0o755, Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	// Write just enough to trigger the LimitReader's read-past-cap
	// detection. tar.Writer doesn't enforce Size matching; reading
	// it back through LimitReader will halt at maxEntryBytes+1.
	body := bytes.Repeat([]byte("A"), maxEntryBytes+10)
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	tw.Close()

	dst := t.TempDir()
	err := extractFromTar(tar.NewReader(&buf), dst, []string{"dovi_tool"}, 0o755)
	if err == nil {
		t.Fatal("expected error on oversized entry, got nil")
	}
	if !strings.Contains(err.Error(), "decompression bomb") {
		t.Errorf("error should mention decompression bomb, got: %v", err)
	}
	// File must NOT be left on disk after a bomb-rejection.
	if _, statErr := os.Stat(filepath.Join(dst, "dovi_tool")); !os.IsNotExist(statErr) {
		t.Errorf("oversized entry left a file on disk")
	}
}

func TestExtractFromTar_PathTraversalNeutralised(t *testing.T) {
	// Malicious entry tries to write outside dstDir via path traversal.
	// filepath.Base strips path components so this lands as "passwd"
	// inside dstDir — but that's still rejected because "passwd" isn't
	// in wantNames. End result: no file written outside dstDir.
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	body := []byte("malicious")
	if err := tw.WriteHeader(&tar.Header{
		Name: "../../../etc/passwd", Size: int64(len(body)), Mode: 0o644, Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	tw.Write(body)
	// Add the legit entry the caller wants so the function doesn't error
	// on "expected entry not found".
	body2 := []byte("real")
	tw.WriteHeader(&tar.Header{
		Name: "dovi_tool", Size: int64(len(body2)), Mode: 0o755, Typeflag: tar.TypeReg,
	})
	tw.Write(body2)
	tw.Close()

	dst := t.TempDir()
	if err := extractFromTar(tar.NewReader(&buf), dst, []string{"dovi_tool"}, 0o755); err != nil {
		t.Fatalf("extractFromTar: %v", err)
	}
	// /etc/passwd was definitely not touched.
	// The would-be passwd file is also not in dstDir (we filtered by wantNames).
	if _, err := os.Stat(filepath.Join(dst, "passwd")); !os.IsNotExist(err) {
		t.Error("malicious entry was accidentally extracted")
	}
}

func TestExtractFromTar_MissingExpectedEntry(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	tw.WriteHeader(&tar.Header{
		Name: "wrong_binary", Mode: 0o755, Size: 1, Typeflag: tar.TypeReg,
	})
	tw.Write([]byte("x"))
	tw.Close()

	dst := t.TempDir()
	err := extractFromTar(tar.NewReader(&buf), dst, []string{"dovi_tool"}, 0o755)
	if err == nil {
		t.Fatal("expected error when expected entry not in archive")
	}
	if !strings.Contains(err.Error(), "dovi_tool") {
		t.Errorf("error should name the missing entry, got: %v", err)
	}
}

func TestStatus_NoBinaries(t *testing.T) {
	// Status today returns "" for *BinPath when neither the legacy
	// /config/tools/ location nor $PATH yields a runnable binary.
	// Earlier versions of Status filled DvBinPath/FfBinPath with the
	// legacy path even when the file didn't exist; that pre-dated
	// resolveBin and the move to ENABLE_DV_TOOLS at entrypoint. The
	// UI now branches on `Installed`, not on the path-string, so
	// the empty-path-when-missing behaviour is intentional.
	//
	// To make the test self-contained we point Dir at a tempdir AND
	// give it an isolated $PATH so a dovi_tool/ffmpeg accidentally
	// installed on the test runner doesn't FAIL this test (real
	// binaries on PATH would resolve, set Installed=true, and trip
	// the "Installed = true when no binaries present" assertion).
	t.Setenv("PATH", t.TempDir())
	tt := Tools{Dir: t.TempDir()}
	got := tt.Status(context.Background())
	if got.Installed {
		t.Error("Installed = true when no binaries present")
	}
	if got.DvVersion != "" || got.FfVersion != "" {
		t.Errorf("expected empty versions, got dv=%q ff=%q", got.DvVersion, got.FfVersion)
	}
	if got.DvBinPath != "" || got.FfBinPath != "" {
		t.Errorf("expected empty paths when not installed, got dv=%q ff=%q", got.DvBinPath, got.FfBinPath)
	}
}

func TestUninstall_Idempotent(t *testing.T) {
	// Removing nothing should not error.
	tt := Tools{Dir: t.TempDir()}
	if err := tt.Uninstall(); err != nil {
		t.Errorf("Uninstall on empty dir errored: %v", err)
	}
	// Second call still safe.
	if err := tt.Uninstall(); err != nil {
		t.Errorf("Uninstall second call errored: %v", err)
	}
}

func TestDefaultTools_Path(t *testing.T) {
	tt := DefaultTools("/config")
	if tt.Dir != "/config/tools" {
		t.Errorf("Dir = %q, want /config/tools", tt.Dir)
	}
	if tt.DvBinPath() != "/config/tools/dovi_tool" {
		t.Errorf("DvBinPath = %q", tt.DvBinPath())
	}
	if tt.FfBinPath() != "/config/tools/ffmpeg" {
		t.Errorf("FfBinPath = %q", tt.FfBinPath())
	}
}

func TestStatus_HappyPathViaVersionFn(t *testing.T) {
	// Inject a stub VersionFn so we don't need real binaries on disk.
	// Tools.Status's only job is "resolve a binary path, shell out
	// to it, capture first line of stdout" — we test that it does so.
	//
	// Path-resolution today uses resolveBin: legacy /config/tools/
	// → exec.LookPath. We need at least one of those to return a
	// non-empty path so Status will call VersionFn. Cheapest: drop
	// empty files at the legacy paths so os.Stat succeeds. That's
	// enough to exercise Status's "given a resolved path, call
	// VersionFn" branch without depending on $PATH state.
	dir := t.TempDir()
	for _, name := range []string{"dovi_tool", "ffmpeg"} {
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("create stub %s: %v", name, err)
		}
		f.Close()
	}
	tt := Tools{
		Dir: dir,
		VersionFn: func(ctx context.Context, bin, flag string) (string, error) {
			if strings.HasSuffix(bin, "/dovi_tool") {
				return "dovi_tool 2.1.2", nil
			}
			if strings.HasSuffix(bin, "/ffmpeg") {
				return "ffmpeg version 6.1", nil
			}
			return "", os.ErrNotExist
		},
	}
	got := tt.Status(context.Background())
	if !got.Installed {
		t.Error("Installed = false, want true")
	}
	if got.DvVersion != "dovi_tool 2.1.2" {
		t.Errorf("DvVersion = %q", got.DvVersion)
	}
	if got.FfVersion != "ffmpeg version 6.1" {
		t.Errorf("FfVersion = %q", got.FfVersion)
	}
}

func TestFetchWithSHA_Mismatch(t *testing.T) {
	// Build a httptest server that serves a known body, then call
	// fetchWithSHA with a deliberately-wrong SHA. Must return error
	// AND clean up the tempfile (no leftover detritus on /tmp).
	body := []byte("known-test-payload")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write(body)
	}))
	defer srv.Close()

	tt := Tools{
		Dir:        t.TempDir(),
		HTTPClient: srv.Client(),
	}
	_, err := tt.fetchWithSHA(context.Background(), srv.URL+"/anything", "deadbeef00deadbeef00deadbeef00deadbeef00deadbeef00deadbeef000000")
	if err == nil {
		t.Fatal("expected SHA mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "SHA-256 mismatch") {
		t.Errorf("error should mention SHA mismatch, got: %v", err)
	}
}

func TestFetchWithSHA_EmptyShaIsRejected(t *testing.T) {
	// The pre-fix foot-gun: passing "" silently skipped verification.
	// Now it errors so a future regression is caught at the call site.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("anything"))
	}))
	defer srv.Close()

	tt := Tools{HTTPClient: srv.Client()}
	_, err := tt.fetchWithSHA(context.Background(), srv.URL, "")
	if err == nil {
		t.Fatal("expected error on empty shaHex, got nil")
	}
	if !strings.Contains(err.Error(), "empty SHA pin") {
		t.Errorf("error should mention empty SHA pin, got: %v", err)
	}
}

func TestFetchWithSHA_ExplicitSkip(t *testing.T) {
	// skipSHAVerification sentinel allows the deliberate-skip path.
	body := []byte("ffmpeg-style-payload")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	defer srv.Close()

	tt := Tools{HTTPClient: srv.Client()}
	rc, err := tt.fetchWithSHA(context.Background(), srv.URL, skipSHAVerification)
	if err != nil {
		t.Fatalf("fetchWithSHA: %v", err)
	}
	defer rc.Close()
	got := make([]byte, len(body))
	rc.Read(got)
	if !bytes.Equal(got, body) {
		t.Errorf("body mismatch — got %q, want %q", got, body)
	}
}

func TestUninstall_RemovesTools(t *testing.T) {
	dir := t.TempDir()
	tt := Tools{Dir: dir}
	// Drop dummy binaries in place to verify Uninstall actually
	// removes them. Use placeholder content so we don't need real
	// binaries; Uninstall only cares about file existence.
	for _, p := range []string{tt.DvBinPath(), tt.FfBinPath()} {
		if err := os.WriteFile(p, []byte("stub"), 0o755); err != nil {
			t.Fatalf("seed %s: %v", p, err)
		}
	}
	if err := tt.Uninstall(); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	for _, p := range []string{tt.DvBinPath(), tt.FfBinPath()} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("expected %s to be removed", p)
		}
	}
}

func readFile(path string) ([]byte, error) {
	return osReadFile(path)
}
