package dvdetect

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Tools resolves the on-disk paths for ffmpeg + dovi_tool. As of
// v0.3.5 both binaries ship baked into the image (Dockerfile dv-tools
// stage): dovi_tool at /usr/local/bin/dovi_tool, ffmpeg at /usr/bin/
// ffmpeg — both reachable via $PATH. Status() resolves and verifies
// them; the runtime install path (the old ENABLE_DV_TOOLS download
// flow) is gone.
//
// VersionFn is a testability hook. Production code leaves it nil and
// gets the default behaviour (real exec via runVersion). Tests inject
// canned version strings to cover happy/sad paths without touching
// the real binaries.
type Tools struct {
	Dir       string                                                      // legacy /config/tools base dir; checked first as a fallback for users with leftover pre-v0.3.5 installs
	VersionFn func(ctx context.Context, bin, flag string) (string, error) // optional; nil → real exec via runVersion
}

// DefaultTools returns a Tools rooted at <configDir>/tools — the
// legacy install location. Status() falls back to $PATH (which is
// where the baked-in image puts the binaries) when nothing is found
// in that legacy directory.
func DefaultTools(configDir string) Tools {
	return Tools{Dir: filepath.Join(configDir, "tools")}
}

func (t Tools) DvBinPath() string { return filepath.Join(t.Dir, "dovi_tool") }
func (t Tools) FfBinPath() string { return filepath.Join(t.Dir, "ffmpeg") }

// ResolveDvBin / ResolveFfBin pick the runtime binary location. The
// legacy /config/tools/ location is checked first as a fallback so
// any pre-existing manual installs still resolve. Returns "" when
// neither location yields a runnable binary (in baked-in deployments
// this should be unreachable).
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

// State captures what's currently runnable. Used by /api/health/detailed
// + the UI status banner.
type State struct {
	Installed bool   `json:"installed"` // true when BOTH dovi_tool and ffmpeg are present and runnable
	DvBinPath string `json:"dvBinPath"`
	DvVersion string `json:"dvVersion,omitempty"`
	FfBinPath string `json:"ffBinPath"`
	FfVersion string `json:"ffVersion,omitempty"`
	LastError string `json:"lastError,omitempty"`
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
