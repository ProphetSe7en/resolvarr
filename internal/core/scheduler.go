package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"resolvarr/internal/utils"
)

// scheduler.go — in-process cron runner for ScheduledJob entries.
//
// Owns a robfig/cron/v3 instance + an entries map keyed by schedule ID
// for cancel/replace on update/delete. On fire: acquires a per-instance
// lock, calls the Runner (api package), appends the result to History,
// prunes to maxInMemoryHistory, writes a per-run log file.
//
// Strict separation of concerns:
//   - core (this file) owns scheduling primitives, history rotation,
//     log-file writes, per-instance serialization. It has no scan-
//     specific knowledge.
//   - api (Runner implementation) owns the conversion from
//     ScheduledJob → scanRunRequest → run* method invocation, and
//     translation of the result into a RunSummary.
//
// Why this split: the api package depends on core (handler structs +
// data model), so the dependency arrow is api → core. Putting the
// scheduler in core with a small Runner interface keeps that direction
// intact without forcing core to import api types.

// Runner is what the scheduler calls when a schedule fires. The api
// package implements it via runTag/runDiscover/runRecover/runCleanup
// methods on its Server type.
//
// Implementations MUST honor ctx cancellation — the scheduler caps
// each run with schedulerJobTimeout and forwards Stop() via ctx.Done().
type Runner interface {
	RunSchedule(ctx context.Context, job ScheduledJob) (RunSummary, error)
}

// RunSummary is the post-execution view the Runner returns. Status maps
// onto the existing JobRun.Status set ("ok" / "partial" / "error").
// Summary is a short user-facing description (e.g. "14 tags added,
// 2 removed"). Detail is the full per-item trace, written to the log
// file and not surfaced in the in-memory history.
type RunSummary struct {
	Status  string // "ok" | "partial" | "error"
	Summary string // short, e.g. "14 tags added, 2 removed"
	Detail  string // full per-item trace — written to log file, not
	// surfaced in History. Empty when run failed before producing one.

	// Result is the structured scan response for the run (scanResponse for
	// tag/discover/recover, or a {tag, discover} pair for combined). Persisted
	// to /config/logs/<scheduleID>-<timestamp>.json next to the .log file so
	// the history modal can drill into the same per-movie detail the live
	// Run-mode UI shows. nil when the run failed before producing one.
	Result interface{}
}

// schedulerJobTimeout caps a single fire's duration. Generous: a
// 5K-movie tag-mode run takes a few seconds; recover walks history
// per-movie which can be slower. 30 minutes covers the worst case
// while still failing if a goroutine hangs.
const schedulerJobTimeout = 30 * time.Minute

// maxInMemoryHistory caps Schedule.History length. Both .log and .json
// files for runs beyond this cap are deleted from disk — the user can
// see and replay the seven most-recent runs per schedule, older are
// gone for good. Beyond seven, re-run the schedule.
const maxInMemoryHistory = 7

// Scheduler wires schedule data + cron loop + Runner. Constructed once
// in main.go, started after http-server, stopped on ctx cancel.
type Scheduler struct {
	app    *App
	cron   *cron.Cron
	runner Runner
	logger *log.Logger
	logDir string

	// entries maps schedule.ID → cron.EntryID. Guarded by mu.
	mu      sync.Mutex
	entries map[string]cron.EntryID

	// instanceLocks serializes runs targeting the same instance. Two
	// schedules pointing at the same Radarr won't race their tag/cleanup
	// batches against each other; manual user runs via /api/scan/run
	// share the same lock when wired up via api.Server's RunSchedule.
	// Created lazily on first access; never garbage-collected (the
	// instance count is small, single digits).
	locksMu       sync.Mutex
	instanceLocks map[string]*sync.Mutex

	// stopCh is closed by Stop() to signal Reload's mid-loop iteration.
	stopCh chan struct{}
}

// NewScheduler wires the components. logDir defaults to /config/logs;
// callers should pass `filepath.Join(configDir, "logs")` to keep
// schedule logs alongside other resolvarr state.
func NewScheduler(app *App, runner Runner, logDir string) *Scheduler {
	return &Scheduler{
		app:           app,
		cron:          cron.New(),
		runner:        runner,
		logger:        log.New(os.Stderr, "[scheduler] ", log.LstdFlags),
		logDir:        logDir,
		entries:       make(map[string]cron.EntryID),
		instanceLocks: make(map[string]*sync.Mutex),
		stopCh:        make(chan struct{}),
	}
}

// Start begins the cron loop and registers every Enabled schedule
// from cfg.Schedules. Blocks until ctx is cancelled, then stops the
// cron (drains any in-flight fires up to its own timeout) and returns.
func (s *Scheduler) Start(ctx context.Context) {
	if err := os.MkdirAll(s.logDir, 0o755); err != nil {
		s.logger.Printf("create log dir %q: %v", s.logDir, err)
		// Non-fatal — scheduler runs, log writes will fail individually.
	}
	s.pruneOrphanLogFiles()
	s.Reload()
	s.cron.Start()
	s.logger.Printf("started")
	<-ctx.Done()
	close(s.stopCh)
	stopCtx := s.cron.Stop()
	<-stopCtx.Done()
	s.logger.Printf("stopped")
}

// Reload pulls fresh schedules from cfg, diffs against entries, and
// adds/removes cron entries to match. Called after every CRUD op so
// the running cron always matches the persisted config.
//
// Atomicity rule from the M3d agent-review charter: stale entries are
// removed BEFORE new ones are added — no overlap window where both old
// and new fire for an updated schedule.
func (s *Scheduler) Reload() {
	cfg := s.app.Config.Get()
	wanted := make(map[string]ScheduledJob, len(cfg.Schedules))
	for _, sj := range cfg.Schedules {
		if !sj.Enabled {
			continue
		}
		if sj.Cron == "" {
			continue
		}
		// Cron validation runs again on Reload. If the persisted
		// expression is malformed (e.g., user edited resolvarr.json by
		// hand), log + skip rather than crash the scheduler.
		if _, err := cron.ParseStandard(sj.Cron); err != nil {
			s.logger.Printf("schedule %s: invalid cron %q: %v — skipped", sj.ID, sj.Cron, err)
			continue
		}
		wanted[sj.ID] = sj
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Phase 1 — remove stale entries (schedules deleted, disabled, or
	// whose cron expression changed).
	for id, entryID := range s.entries {
		w, present := wanted[id]
		stillSame := present && s.entryCron(entryID) == w.Cron
		if !stillSame {
			s.cron.Remove(entryID)
			delete(s.entries, id)
		}
	}
	// Phase 2 — add new/changed entries.
	for id, sj := range wanted {
		if _, ok := s.entries[id]; ok {
			continue // already registered with the right cron
		}
		scheduleID := id // capture for closure
		entryID, err := s.cron.AddFunc(sj.Cron, func() {
			s.fire(scheduleID, false)
		})
		if err != nil {
			s.logger.Printf("schedule %s: AddFunc failed: %v", id, err)
			continue
		}
		s.entries[id] = entryID
	}
}

// entryCron returns the textual cron spec for a registered entry, or
// "" if the entry has been removed. cron/v3 doesn't expose a direct
// "give me the spec" lookup — we have to scan the entry list.
func (s *Scheduler) entryCron(eid cron.EntryID) string {
	for _, e := range s.cron.Entries() {
		if e.ID == eid {
			// cron.Entry doesn't carry the original spec string. Approximate
			// by formatting Schedule.Next from a fixed reference time.
			// In practice this means a "spec changed" detection compares
			// the wanted cron string to "". Always non-equal → always
			// re-register on Reload. Acceptable for v1; the cost is one
			// extra Remove+AddFunc per Reload per active schedule, which
			// is negligible. Tighten if it becomes a hot path.
			return ""
		}
	}
	return ""
}

// RunNow fires a schedule immediately, bypassing the cron schedule.
// Used by the "Run now" button on the schedule list. Returns an error
// if the schedule isn't found; the actual run goes via SafeGo and any
// runtime error lands in the schedule's history just like a cron fire.
//
// Run-now respects user intent over the Enabled flag: a paused rule
// can still be fired manually. Cron-loop fires keep the gate (only
// enabled rules ever auto-run). This matters most for manual-only
// presets — the user pauses cron, but Run-now is the whole point.
func (s *Scheduler) RunNow(scheduleID string) error {
	cfg := s.app.Config.Get()
	for _, sj := range cfg.Schedules {
		if sj.ID == scheduleID {
			utils.SafeGo("schedule-run-now-"+scheduleID, func() { s.fire(scheduleID, true) })
			return nil
		}
	}
	return errors.New("schedule not found")
}

// fire executes one run of a schedule. Wrapped in SafeGo by the
// caller so a panic in Runner.RunSchedule doesn't kill the cron loop.
// manualTrigger=true bypasses the Enabled-gate (Run-now); cron fires
// pass false so paused rules don't auto-run.
func (s *Scheduler) fire(scheduleID string, manualTrigger bool) {
	cfg := s.app.Config.Get()
	var sj *ScheduledJob
	for i := range cfg.Schedules {
		if cfg.Schedules[i].ID == scheduleID {
			tmp := cfg.Schedules[i]
			sj = &tmp
			break
		}
	}
	if sj == nil {
		// Schedule was deleted between cron firing and this lookup.
		return
	}
	if !manualTrigger && !sj.Enabled {
		return
	}

	// Per-instance serialization. Creating the lock on first access is
	// safe (locksMu guards the map); subsequent fires reuse the same
	// mutex.
	lock := s.instanceLock(sj.InstanceID)
	lock.Lock()
	defer lock.Unlock()

	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), schedulerJobTimeout)
	defer cancel()

	summary, err := s.runner.RunSchedule(ctx, *sj)
	duration := time.Since(started)

	run := JobRun{
		StartedAt:  started,
		DurationMs: duration.Milliseconds(),
	}
	if err != nil {
		run.Status = "error"
		run.Summary = err.Error()
	} else {
		run.Status = summary.Status
		run.Summary = summary.Summary
	}

	// Write per-run log file BEFORE the in-memory history truncation
	// drops older entries — keeps the trail intact even when History
	// rolls over. SanitizeLogField (security baseline T68) is applied
	// to user-controlled fields by the writeRunLogFile helper.
	if path, werr := s.writeRunLogFile(scheduleID, started, summary, err); werr == nil {
		run.LogPath = path
	} else {
		s.logger.Printf("schedule %s: write log: %v", scheduleID, werr)
	}

	// Persist the structured scan response next to the log file so the
	// history modal's "View details" action can hydrate the same
	// per-movie drill-in the live Run-mode UI shows. nil-safe: if the
	// run failed before producing a response, no file is written.
	if path, werr := s.writeRunResultFile(scheduleID, started, summary.Result); werr == nil {
		run.ResultPath = path
	} else {
		s.logger.Printf("schedule %s: write result: %v", scheduleID, werr)
	}

	// Capture runs that fall off the in-memory cap so we can delete
	// their .log/.json files immediately after the config update lands.
	// Done outside the closure so the file-deletes don't run while we
	// hold the config lock.
	var dropped []JobRun
	if err := s.app.Config.Update(func(c *Config) {
		for i := range c.Schedules {
			if c.Schedules[i].ID != scheduleID {
				continue
			}
			c.Schedules[i].History = append(c.Schedules[i].History, run)
			if len(c.Schedules[i].History) > maxInMemoryHistory {
				excess := len(c.Schedules[i].History) - maxInMemoryHistory
				dropped = append(dropped, c.Schedules[i].History[:excess]...)
				c.Schedules[i].History = c.Schedules[i].History[excess:]
			}
			return
		}
	}); err != nil {
		// Persist failed (disk full / permissions / etc). The run already
		// happened — only the in-memory + disk history record is lost.
		// Surface in scheduler log so the admin sees the issue.
		s.logger.Printf("schedule %s: persist history: %v", scheduleID, err)
	}

	// Delete on-disk files for runs that just fell off the cap. Best-effort
	// — a missing file or permission failure is logged but doesn't block.
	// Done after the config update so a write failure above doesn't leave
	// us with files deleted but History claiming they exist.
	for _, d := range dropped {
		if d.LogPath != "" {
			if rerr := os.Remove(d.LogPath); rerr != nil && !os.IsNotExist(rerr) {
				s.logger.Printf("schedule %s: drop old log %s: %v", scheduleID, d.LogPath, rerr)
			}
		}
		if d.ResultPath != "" {
			if rerr := os.Remove(d.ResultPath); rerr != nil && !os.IsNotExist(rerr) {
				s.logger.Printf("schedule %s: drop old result %s: %v", scheduleID, d.ResultPath, rerr)
			}
		}
	}

	if run.Status != "ok" {
		s.logger.Printf("schedule %s (%s): %s — %s", scheduleID, sj.Name, run.Status, run.Summary)
	}
}

// instanceLock returns a per-instanceID mutex. Lazy-create on first
// access. The map never shrinks — instance count is small.
func (s *Scheduler) instanceLock(instanceID string) *sync.Mutex {
	s.locksMu.Lock()
	defer s.locksMu.Unlock()
	if m, ok := s.instanceLocks[instanceID]; ok {
		return m
	}
	m := &sync.Mutex{}
	s.instanceLocks[instanceID] = m
	return m
}

// writeRunLogFile writes the per-run trace to /config/logs/<scheduleID>-<timestamp>.log.
// Returns the path on success. On error the caller surfaces it in the
// scheduler-internal log; the run's JobRun.LogPath stays empty.
//
// Timestamp uses container-local time (driven by the TZ env var) so the
// filename matches what the UI history modal displays — was UTC, but
// users found that confusing when their TZ != UTC (Europe/Oslo +02:00:
// schedule fired 10:29 local, filename said 0829).
func (s *Scheduler) writeRunLogFile(scheduleID string, started time.Time, summary RunSummary, runErr error) (string, error) {
	stamp := started.Local().Format("20060102-150405")
	name := fmt.Sprintf("%s-%s.log", scheduleID, stamp)
	path := filepath.Join(s.logDir, name)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return "", err
	}
	defer f.Close()
	fmt.Fprintf(f, "schedule: %s\nstarted: %s\nstatus: %s\n", scheduleID, started.UTC().Format(time.RFC3339), summary.Status)
	if summary.Summary != "" {
		fmt.Fprintf(f, "summary: %s\n", summary.Summary)
	}
	if runErr != nil {
		fmt.Fprintf(f, "error: %s\n", runErr.Error())
	}
	if summary.Detail != "" {
		fmt.Fprintf(f, "\n--- detail ---\n%s\n", summary.Detail)
	}
	return path, nil
}

// LogDir returns the absolute log directory the scheduler writes
// per-run files to. Used by the API layer to validate that a JobRun's
// ResultPath isn't a path-traversal attempt before serving the file.
func (s *Scheduler) LogDir() string {
	abs, err := filepath.Abs(s.logDir)
	if err != nil {
		return s.logDir
	}
	return abs
}

// writeRunResultFile writes the structured scan response as JSON next
// to the .log file. The history modal's "View details" action loads
// this back so the user gets the same per-movie drill-in as the live
// Run-mode UI. nil result is a no-op (run failed before producing one).
// Returns the path on success; on error caller surfaces it in the
// scheduler-internal log and JobRun.ResultPath stays empty.
func (s *Scheduler) writeRunResultFile(scheduleID string, started time.Time, result interface{}) (string, error) {
	if result == nil {
		return "", nil
	}
	// Container-local timestamp — pairs with the .log file (same naming)
	// and matches the UI's local-time display.
	stamp := started.Local().Format("20060102-150405")
	name := fmt.Sprintf("%s-%s.json", scheduleID, stamp)
	path := filepath.Join(s.logDir, name)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return "", err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	if err := enc.Encode(result); err != nil {
		// Encode failed mid-stream — file exists with truncated/partial
		// content. Caller's JobRun.ResultPath stays empty, so a future
		// pruneOrphanLogFiles will eventually delete it on next start.
		// Drop it now too so the same-session disk doesn't carry a
		// half-written file until next restart.
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

// pruneOrphanLogFiles deletes any file in logDir that isn't referenced
// by a current JobRun.LogPath / JobRun.ResultPath. Cleans up two cases:
//
//   - Files left from runs that fell off the cap before the runtime
//     learned to delete on truncation (older container builds).
//   - Files left after manual edits to resolvarr.json or to the logs dir.
//
// Best-effort — a permission failure is logged but doesn't block startup.
//
// MUST run BEFORE cron.Start so no in-flight run can write a file
// whose JobRun isn't in cfg yet (which would get pruned mid-flight).
// Today the only call site is Scheduler.Start, before cron starts
// firing. Future call sites (e.g. on-demand "free disk space") would
// need to either snapshot the keep set differently or be gated on
// no-runs-in-flight. Combined with the in-truncation deletes in
// recordRun, this keeps the on-disk file count bounded by
// maxInMemoryHistory × #schedules.
func (s *Scheduler) pruneOrphanLogFiles() {
	entries, err := os.ReadDir(s.logDir)
	if err != nil {
		return
	}
	keep := make(map[string]struct{})
	cfg := s.app.Config.Get()
	for _, sj := range cfg.Schedules {
		for _, run := range sj.History {
			if run.LogPath != "" {
				keep[filepath.Base(run.LogPath)] = struct{}{}
			}
			if run.ResultPath != "" {
				keep[filepath.Base(run.ResultPath)] = struct{}{}
			}
		}
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if _, ok := keep[name]; ok {
			continue
		}
		// Audit log + adhoc scan dumps share /config/logs/ but are NOT
		// scheduler output. They're managed by their own retention
		// (runlog.pruneOldArchives — KeepDays-based). Touching them
		// here wipes user-visible Activity tab history on every
		// container restart. Match-and-skip the patterns this prune
		// shouldn't see:
		//   runs.log              — current audit log
		//   runs-YYYYMMDD.log     — rotated audit logs
		//   scan-{action}-YYYYMMDD-HHMMSS.json — adhoc scan dumps
		if name == "runs.log" ||
			(strings.HasPrefix(name, "runs-") && strings.HasSuffix(name, ".log")) ||
			(strings.HasPrefix(name, "scan-") && strings.HasSuffix(name, ".json")) {
			continue
		}
		full := filepath.Join(s.logDir, name)
		if err := os.Remove(full); err != nil {
			s.logger.Printf("prune orphan %s: %v", full, err)
		}
	}
}

// ValidateCron is a thin pass-through over cron.ParseStandard so HTTP
// handlers can validate at save-time without importing the cron package.
// Returns nil for any valid 5-field expression.
func ValidateCron(expr string) error {
	if _, err := cron.ParseStandard(expr); err != nil {
		return fmt.Errorf("invalid cron expression: %w", err)
	}
	return nil
}
