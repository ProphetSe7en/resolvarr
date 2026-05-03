// Package core — RunLogger writes audit + debug lines to /config/logs/runs.log.
//
// Audit lines (one per scan-run summary, schedule fire, recover, cleanup,
// discover) are always written. Debug lines (per Arr HTTP call, sanitized)
// are gated on a config flag that takes effect immediately when toggled
// from the Settings UI — no restart needed.
//
// File rotation: lines append to runs.log for the current calendar day.
// At first call after midnight, the previous day's content is renamed
// to runs-YYYYMMDD.log and runs.log starts empty. Files older than
// KeepDays (default 14) are pruned at rotation time.
//
// Format is fixed-prefix structured: `2026-05-02 18:23:45.123 LEVEL  source: message key=val key=val`.
// LEVEL is AUDIT or DEBUG. Sanitization is the caller's responsibility
// for now — utils.SanitizeLogField helps for user-controlled strings.
//
// Concurrency: every Write goes through a mutex. The file handle is
// opened lazily and re-opened on day-rollover.

package core

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// RunLogger is the audit + debug logger. Construct with NewRunLogger.
// Safe for concurrent use. Reads the latest config snapshot to decide
// whether debug lines are written and which retention to enforce, so
// runtime toggling from Settings is picked up without restart.
type RunLogger struct {
	dir         string
	cfgSnapshot func() LoggingConfig

	mu       sync.Mutex
	file     *os.File
	openedOn string // YYYYMMDD of the open file — drives rotation
}

// NewRunLogger wires the logger to a directory and a snapshot accessor.
// dir defaults to /config/logs when empty; the directory is created on
// first write if missing. cfgSnapshot is called on every Debug() and
// every rotation to pick up live config changes.
//
// IMPORTANT: cfgSnapshot must NOT acquire the same lock as ConfigStore.Update.
// The default wiring in app.go uses cfg.Get() which takes RLock — Update()
// holds the write lock while running its mutator, so any code path that
// (a) is called from inside an Update mutator AND (b) calls Audit/Debug
// will deadlock when the rotate path reads cfgSnapshot. Today no mutator
// logs anywhere; if that changes, capture a snapshot before Update and
// pass a closure that returns the captured value.
func NewRunLogger(dir string, cfgSnapshot func() LoggingConfig) *RunLogger {
	if dir == "" {
		dir = "/config/logs"
	}
	if cfgSnapshot == nil {
		cfgSnapshot = func() LoggingConfig { return LoggingConfig{KeepDays: 14} }
	}
	return &RunLogger{dir: dir, cfgSnapshot: cfgSnapshot}
}

// Audit writes a guaranteed line. source is a short tag like "scan-tag"
// or "schedule:abc123"; msg is the human-readable summary; fields are
// optional key=val pairs appended after the message.
func (l *RunLogger) Audit(source, msg string, fields ...string) {
	l.write("AUDIT", source, msg, fields)
}

// Debug writes only when LoggingConfig.Debug is true. Cheap when off:
// reads the snapshot func and returns immediately.
func (l *RunLogger) Debug(source, msg string, fields ...string) {
	if !l.cfgSnapshot().Debug {
		return
	}
	l.write("DEBUG", source, msg, fields)
}

// DebugEnabled exposes the live toggle so call sites that want to skip
// expensive log-line construction can short-circuit.
func (l *RunLogger) DebugEnabled() bool {
	return l.cfgSnapshot().Debug
}

// LogPath returns the absolute path of the current-day log file. Used
// by the Settings UI to display where logs live.
func (l *RunLogger) LogPath() string {
	return filepath.Join(l.dir, "runs.log")
}

func (l *RunLogger) write(level, source, msg string, fields []string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	today := now.Format("20060102")

	// Lazy-open + day-rollover.
	if l.file == nil || l.openedOn != today {
		if err := l.rotate(today); err != nil {
			// Rotation failure is non-fatal for the caller — best-effort
			// logging shouldn't block the request. Drop the line silently
			// rather than panic; a stderr line gives operators a hint.
			fmt.Fprintf(os.Stderr, "runlog: rotate failed: %v\n", err)
			return
		}
	}

	var b strings.Builder
	b.WriteString(now.Format("2006-01-02 15:04:05.000"))
	b.WriteString(" ")
	b.WriteString(level)
	b.WriteString("  ") // two-space padding; AUDIT/DEBUG are same length, no level-specific branch needed
	b.WriteString(source)
	if msg != "" {
		b.WriteString(": ")
		b.WriteString(msg)
	}
	for _, f := range fields {
		b.WriteString(" ")
		b.WriteString(f)
	}
	b.WriteString("\n")

	if _, err := l.file.WriteString(b.String()); err != nil {
		fmt.Fprintf(os.Stderr, "runlog: write failed: %v\n", err)
	}
}

// rotate handles three things: directory creation, day-rollover (close
// current file, rename previous-day content to dated archive, prune old
// archives), and opening today's file in append mode.
func (l *RunLogger) rotate(today string) error {
	if err := os.MkdirAll(l.dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", l.dir, err)
	}

	current := filepath.Join(l.dir, "runs.log")

	// If we previously had a file open from a different day, close it.
	// Then check if runs.log on disk is from a previous day (e.g. the
	// process restarted across midnight): rename to dated archive.
	if l.file != nil {
		_ = l.file.Close()
		l.file = nil
	}
	if info, err := os.Stat(current); err == nil {
		fileDay := info.ModTime().Format("20060102")
		if fileDay != today {
			archive := filepath.Join(l.dir, "runs-"+fileDay+".log")
			// Best-effort rename. If a same-day archive already exists
			// (rare — only if the rename didn't run last time), append
			// today's file to it instead of overwriting.
			if _, err := os.Stat(archive); err == nil {
				// Append-and-truncate fallback. Read current, append to
				// archive, truncate current. Cheaper than a real merge.
				if data, rerr := os.ReadFile(current); rerr == nil && len(data) > 0 {
					if af, oerr := os.OpenFile(archive, os.O_APPEND|os.O_WRONLY, 0o644); oerr == nil {
						_, _ = af.Write(data)
						_ = af.Close()
					}
				}
				_ = os.Remove(current)
			} else {
				_ = os.Rename(current, archive)
			}
		}
	}

	// Prune dated archives older than KeepDays.
	keep := l.cfgSnapshot().KeepDays
	if keep < 1 {
		keep = 14
	}
	l.pruneOldArchives(keep)

	// Open / create today's file.
	f, err := os.OpenFile(current, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", current, err)
	}
	l.file = f
	l.openedOn = today
	return nil
}

// pruneOldArchives removes runs-YYYYMMDD.log files older than keep days.
// Best-effort: errors are logged to stderr but don't abort.
func (l *RunLogger) pruneOldArchives(keep int) {
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		return
	}
	// cutoff is the oldest day we still keep. With KeepDays=14 and
	// today=2026-05-02, cutoff=2026-04-19 — files dated 2026-04-19
	// through 2026-05-01 (13 archives) plus today's runs.log =
	// 14 days total, matching what the user typed.
	cutoff := time.Now().AddDate(0, 0, -(keep - 1)).Format("20060102")
	type dated struct {
		path string
		day  string
	}
	var dates []dated
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		// Two file patterns share this directory + retention setting:
		//   runs-YYYYMMDD.log          rotated audit log
		//   scan-{action}-YYYYMMDD-HHMMSS.json   adhoc-scan dumps
		// Pull the YYYYMMDD prefix from each so the same cutoff
		// comparison applies. Files that don't match either pattern
		// are left alone (might be schedule-runner per-run logs which
		// the scheduler itself handles, or user-dropped files).
		var day string
		switch {
		case strings.HasPrefix(name, "runs-") && strings.HasSuffix(name, ".log"):
			day = strings.TrimSuffix(strings.TrimPrefix(name, "runs-"), ".log")
			if len(day) != 8 {
				continue
			}
		case strings.HasPrefix(name, "scan-") && strings.HasSuffix(name, ".json"):
			// scan-<action>-YYYYMMDD-HHMMSS.json — split on '-' and
			// pull the second-to-last token (YYYYMMDD).
			parts := strings.Split(strings.TrimSuffix(name, ".json"), "-")
			if len(parts) < 3 {
				continue
			}
			day = parts[len(parts)-2]
			if len(day) != 8 {
				continue
			}
		default:
			continue
		}
		dates = append(dates, dated{path: filepath.Join(l.dir, name), day: day})
	}
	sort.Slice(dates, func(i, j int) bool { return dates[i].day < dates[j].day })
	for _, d := range dates {
		if d.day < cutoff {
			_ = os.Remove(d.path)
		}
	}
}

// Close releases the file handle. Safe to call multiple times.
func (l *RunLogger) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		_ = l.file.Close()
		l.file = nil
	}
}
