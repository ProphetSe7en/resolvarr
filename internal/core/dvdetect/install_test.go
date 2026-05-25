package dvdetect

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStatus_NoBinaries verifies the empty-state path: when neither
// the legacy /config/tools/ location nor $PATH yields a runnable
// binary, Status returns Installed=false and empty versions/paths.
// The UI branches on `Installed`, not on the path-string, so the
// empty-path-when-missing behaviour is intentional.
//
// $PATH is replaced with an empty tempdir so a dovi_tool/ffmpeg
// accidentally installed on the test runner doesn't trip the
// assertion by resolving via exec.LookPath.
func TestStatus_NoBinaries(t *testing.T) {
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

// TestDefaultTools_Path locks the path-derivation surface — the
// API handler + scan_dv_detail + webhook_adapters all assume the
// /config/tools/{dovi_tool,ffmpeg} shape.
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

// TestStatus_HappyPathViaVersionFn verifies Status's "given a resolved
// path, call VersionFn, capture first line of stdout" contract via a
// stub VersionFn so we don't need real binaries on disk. Empty files
// at the legacy paths are enough for os.Stat to succeed and Status to
// reach the VersionFn branch.
func TestStatus_HappyPathViaVersionFn(t *testing.T) {
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
