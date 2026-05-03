package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestPruneOldArchives_BothShapes checks the dual-pattern prune walks
// runs-YYYYMMDD.log AND scan-{action}-YYYYMMDD-HHMMSS.json side by
// side using the same cutoff. Drops a mix of fresh + stale + non-
// matching files in a tempdir, runs prune with keep=14, and asserts
// stale files of either pattern are removed while fresh files + the
// non-matching ones survive.
func TestPruneOldArchives_BothShapes(t *testing.T) {
	dir := t.TempDir()

	// Day stamps relative to today so the test doesn't drift over time.
	today := time.Now()
	fresh := today.AddDate(0, 0, -3).Format("20060102")  // within keep=14
	stale := today.AddDate(0, 0, -30).Format("20060102") // well past
	rightAtCutoff := today.AddDate(0, 0, -13).Format("20060102")
	pastCutoff := today.AddDate(0, 0, -14).Format("20060102")

	files := map[string]bool{
		// runs-*.log
		"runs-" + fresh + ".log":         true,  // keep
		"runs-" + stale + ".log":         false, // drop
		"runs-" + rightAtCutoff + ".log": true,  // keep (boundary inclusive)
		"runs-" + pastCutoff + ".log":    false, // drop
		// scan-*.json (action with no internal hyphens)
		"scan-tag-" + fresh + "-120000.json":      true,
		"scan-recover-" + stale + "-090000.json":  false,
		"scan-dvdetail-" + fresh + "-235959.json": true,
		"scan-cleanup-" + stale + "-000000.json":  false,
		// Files that don't match either pattern — must survive.
		"random.txt":             true,
		"runs-bad.log":            true, // wrong day-shape (3 chars), parser must skip not crash
		"scan-foo.json":           true, // < 3 parts after split
		"scan-foo-notdate.json":   true,
	}

	for name := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("setup: write %s: %v", name, err)
		}
	}

	l := &RunLogger{
		dir:         dir,
		cfgSnapshot: func() LoggingConfig { return LoggingConfig{KeepDays: 14} },
	}
	l.pruneOldArchives(14)

	for name, shouldExist := range files {
		_, err := os.Stat(filepath.Join(dir, name))
		exists := err == nil
		if exists != shouldExist {
			if shouldExist {
				t.Errorf("expected %q to survive prune, but it was removed", name)
			} else {
				t.Errorf("expected %q to be pruned, but it survived", name)
			}
		}
	}
}

// TestPruneOldArchives_HyphenatedAction confirms the parser correctly
// pulls the YYYYMMDD token even when an action name contains hyphens
// (no current action does, but tolerance keeps the parser robust if
// new ones are added).
func TestPruneOldArchives_HyphenatedAction(t *testing.T) {
	dir := t.TempDir()
	stale := time.Now().AddDate(0, 0, -30).Format("20060102")
	name := "scan-some-future-action-" + stale + "-120000.json"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	l := &RunLogger{
		dir:         dir,
		cfgSnapshot: func() LoggingConfig { return LoggingConfig{KeepDays: 14} },
	}
	l.pruneOldArchives(14)
	if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
		t.Errorf("hyphenated-action stale dump should have been pruned")
	}
}

// TestPruneOldArchives_BadInputs asserts the parser doesn't panic on
// malformed filenames it shouldn't have been given (defensive — the
// dumpScanJSON path always produces well-formed names, but /config is
// user-writable).
func TestPruneOldArchives_BadInputs(t *testing.T) {
	dir := t.TempDir()
	bad := []string{
		"scan-.json",
		"scan--.json",
		"scan-tag-not8chars-120000.json",
		"runs-.log",
		"runs-too-many-parts.log",
		strings.Repeat("a", 200) + ".log",
	}
	for _, name := range bad {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	l := &RunLogger{
		dir:         dir,
		cfgSnapshot: func() LoggingConfig { return LoggingConfig{KeepDays: 14} },
	}
	// Must not panic.
	l.pruneOldArchives(14)
	// All bad-shaped files must survive (parser skips non-matching).
	for _, name := range bad {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("malformed file %q was unexpectedly removed", name)
		}
	}
}
