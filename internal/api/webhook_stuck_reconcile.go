package api

// webhook_stuck_reconcile.go — stuck-downloads adapter for the
// M-Webhook dispatcher. Event-driven twin of the Library scan's
// "Stuck downloads" action (scan_reconcile.go), which until now was
// manual-only: the user had to open the page and press Run.
//
// Why an IMPORT event is the right trigger: a download gets stuck
// precisely because a BETTER release of the same thing imported first.
// Sonarr/Radarr grab two releases seconds apart, the higher-scoring one
// wins the import race, and the loser sits in the queue forever in
// importPending / importBlocked on the pre-import category — the
// Your Friends & Neighbors S02E08 case in scan_reconcile.go's header.
// The Download event IS the winning import, so firing the sweep right
// after it is the narrowest possible moment to catch the loser.
//
// Scope: always the movie / series the event names — the losers of
// THIS race. Architectural rule 2 (webhook_adapters.go) forbids an
// adapter fanning out past the item its event identifies, and a
// whole-queue sweep on every import is exactly the scaling trap that
// rule calls out. Sweeping the entire queue stays the Library scan
// action's job, or a `reconcile` schedule's.
//
// Coalescing: a 24-episode season pack emits 24 Download events, each
// naming the same series. Scheduling 24 identical sweeps would re-read
// the queue, history and manual-import evaluation 24 times for one
// answer, so a sweep already pending for the same (rule, item) is not
// scheduled again.
//
// Fan-out: the rule carries a LIST of (qBit instance, target category)
// targets, so one import event reconciles every download client the Arr
// feeds from. Each stuck hash is resolved to the client that actually
// holds it (see scan_reconcile_targets.go) — names aren't matched,
// hashes are.
//
// Deferred, always: the sweep reads the Arr queue + per-target history +
// manualimport evaluations, which is far too much work to do inline on
// a Connect webhook response that Sonarr/Radarr block on (that blocking
// is what serialised per-file imports on a season pack for the qBit
// category-fix adapter). It also needs the Arr to finish settling the
// losing rows into their stuck state first. So the dispatcher schedules
// it and returns immediately.
//
// Architectural rule, same as the Library scan path: the adapter NEVER
// decides redundancy itself. engine.ClassifyStuckDownload, reached via
// Server.runReconcile, is the single decision point. This file only
// decides WHEN to run it and WHAT to narrow it to.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"resolvarr/internal/core"
)

// stuckReconcileDeferDelay resolves how long after the import event the
// deferred sweep runs. 0 / unset → 60s default; clamped to 600s.
//
// The default is longer than the category-fix's 30s on purpose: that
// one only re-checks a single torrent's category, while this one needs
// the Arr to have finished processing the winning import AND settled
// the losing queue rows into importPending / importBlocked. Sweeping
// too early finds nothing and the rule looks broken.
func stuckReconcileDeferDelay(rule *core.WebhookRule) time.Duration {
	secs := 60
	if rule != nil && rule.StuckReconcile != nil && rule.StuckReconcile.DeferSeconds > 0 {
		secs = rule.StuckReconcile.DeferSeconds
	}
	if secs > maxStuckReconcileDeferSeconds {
		secs = maxStuckReconcileDeferSeconds
	}
	return time.Duration(secs) * time.Second
}

// stuckReconcileScope resolves which movie / series an import event is
// about. Returns ok=false when the payload carries no usable item, which
// is a clean skip rather than a failure (a manual import with no movie
// block, say).
//
// Shared by the scheduler (to build the coalescing key) and the
// dispatcher (to narrow the sweep) so the two can never disagree about
// what "this item" means.
func stuckReconcileScope(appType string, body []byte) (movieID, seriesID int, label string, ok bool) {
	var payload downloadEventPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return 0, 0, "", false
	}
	switch appType {
	case "radarr":
		if payload.Movie == nil || payload.Movie.ID <= 0 {
			return 0, 0, "", false
		}
		label = strings.TrimSpace(payload.Movie.Title)
		if label == "" {
			label = "this movie"
		}
		return payload.Movie.ID, 0, label, true
	case "sonarr":
		if payload.Series == nil || payload.Series.ID <= 0 {
			return 0, 0, "", false
		}
		label = strings.TrimSpace(payload.Series.Title)
		if label == "" {
			label = "this series"
		}
		return 0, payload.Series.ID, label, true
	}
	return 0, 0, "", false
}

// stuckSweepKey identifies one pending sweep: a rule acting on one item.
// Two events for the same series under the same rule share a key and
// therefore share a single sweep.
func stuckSweepKey(ruleID, appType string, movieID, seriesID int) string {
	return fmt.Sprintf("%s:%s:%d:%d", ruleID, appType, movieID, seriesID)
}

// claimStuckSweep reserves the key for a pending sweep. Returns false
// when one is already pending, meaning the caller should not schedule
// another. releaseStuckSweep frees it as the sweep begins, so a later
// import for the same item schedules a fresh one.
func (s *Server) claimStuckSweep(key string) bool {
	s.stuckSweepMu.Lock()
	defer s.stuckSweepMu.Unlock()
	if s.stuckSweepPending == nil {
		s.stuckSweepPending = map[string]bool{}
	}
	if s.stuckSweepPending[key] {
		return false
	}
	s.stuckSweepPending[key] = true
	return true
}

func (s *Server) releaseStuckSweep(key string) {
	s.stuckSweepMu.Lock()
	defer s.stuckSweepMu.Unlock()
	delete(s.stuckSweepPending, key)
}

// scheduleDeferredStuckReconcile runs the stuck-downloads sweep in the
// background `delay` after the import event instead of inline in the
// Connect dispatch, and records its own single-function history entry +
// notification when it completes.
//
// Inputs are snapshotted (the request body + structs must survive past
// the HTTP response; config is re-read fresh at fire-time). Pending
// timers are lost on container restart — acceptable: the next import
// event fires another sweep, and the Library scan page is still there
// for a manual run.
//
// Mirrors scheduleDeferredCategoryFix exactly; if you change the
// snapshot / panic-guard / history-append shape in one, change both.
// Returns false when an identical sweep is already pending, so the
// dispatcher can say so in the History row instead of implying a second
// one was queued.
func (s *Server) scheduleDeferredStuckReconcile(rule *core.WebhookRule, inst *core.Instance, env *connectEventEnvelope, body []byte) bool {
	if rule == nil || inst == nil || env == nil {
		return false
	}
	// Coalesce per (rule, item): one sweep answers for every event in a
	// season-pack burst, since they all name the same series.
	movieID, seriesID, _, ok := stuckReconcileScope(rule.AppType, body)
	if !ok {
		// No usable item on the payload. Let the dispatcher run and
		// report the skip with its own wording rather than guessing here.
		movieID, seriesID = 0, 0
	}
	key := stuckSweepKey(rule.ID, rule.AppType, movieID, seriesID)
	if !s.claimStuckSweep(key) {
		return false
	}
	delay := stuckReconcileDeferDelay(rule)
	ruleCopy := *rule
	instCopy := *inst
	envCopy := *env
	bodyCopy := append([]byte(nil), body...)
	started := time.Now().UTC()
	time.AfterFunc(delay, func() {
		// Freed first: from here on the sweep is running, so a later
		// import for the same item must be able to queue a fresh one.
		s.releaseStuckSweep(key)
		defer func() {
			if rec := recover(); rec != nil {
				fmt.Fprintf(os.Stderr, "resolvarr: deferred stuck-downloads sweep panicked for rule %s: %v\n", ruleCopy.ID, rec)
			}
		}()
		// The sweep reads the queue once, then per-target history +
		// manual-import evaluations for the one item it is scoped to,
		// then one category change per redundant download.
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		cfg := s.App.Config.Get()
		res := s.dispatchStuckReconcile(ctx, &ruleCopy, cfg, &envCopy, bodyCopy)
		run := buildWebhookRuleRun(&envCopy, bodyCopy, started, []functionResult{res})
		s.fireWebhookNotifications(&ruleCopy, &instCopy, &envCopy, bodyCopy, []functionResult{res}, run)
		s.appendWebhookRuleRunsBatch([]pendingRuleRun{{ruleID: ruleCopy.ID, run: run}})
	})
	return true
}

// dispatchStuckReconcile runs the stuck-downloads sweep for one rule on
// one import event. Returns OK=true for every clean path including
// "nothing was stuck"; OK=false only on real failures (missing config,
// Arr unreachable, qBit client init).
//
// Changed is true only when at least one download actually moved — the
// notification layer consumes Changed, and a rule firing on every single
// import must not emit a "swept, found nothing" line each time.
func (s *Server) dispatchStuckReconcile(
	ctx context.Context,
	rule *core.WebhookRule,
	cfg core.Config,
	env *connectEventEnvelope,
	body []byte,
) functionResult {
	const fn = core.WebhookFnStuckReconcile

	if env.EventType != string(core.WebhookEventDownload) {
		return functionResult{Function: fn, OK: true, Summary: "skipped (not a Download event)"}
	}
	if rule.StuckReconcile == nil || len(rule.StuckReconcile.Targets) == 0 {
		return functionResult{
			Function: fn, OK: false,
			Summary: "rule has the stuck-downloads function but no download clients configured",
		}
	}

	inst := findInstanceByID(cfg, rule.InstanceID)
	if inst == nil {
		return functionResult{
			Function: fn, OK: false,
			Summary: "instance vanished between event receive and dispatch",
		}
	}

	req := scanRunRequest{
		InstanceID: rule.InstanceID,
		Action:     "reconcile",
		Mode:       "apply",
	}
	for _, t := range rule.StuckReconcile.Targets {
		req.ReconcileTargets = append(req.ReconcileTargets, scanReconcileTarget{
			QbitInstanceID: t.QbitInstanceID,
			PostCategory:   t.PostCategory,
		})
	}

	// Always narrowed to the item the event names — Architectural rule 2.
	movieID, seriesID, scopeLabel, ok := stuckReconcileScope(rule.AppType, body)
	if !ok {
		return functionResult{
			Function: fn, OK: true,
			Summary: "skipped (no movie/series on the event to scope the sweep to)",
		}
	}
	req.ReconcileOnlyMovieID = movieID
	req.ReconcileOnlySeriesID = seriesID

	resp, apiErr := s.runReconcile(ctx, cfg, inst, rule.AppType, req)
	s.auditScan("webhook:"+rule.ID, "reconcile", inst, req, resp, errMsgOf(apiErr))
	if apiErr != nil {
		return functionResult{Function: fn, OK: false, Summary: "stuck-downloads sweep", Err: apiErr}
	}

	t := resp.Totals
	detail := StuckReconcileDetail{
		Scope:          scopeLabel,
		Stuck:          t.ReconcileStuck,
		Moved:          t.ReconcileRecategorised,
		NeedsAttention: t.ReconcileNeedsAttention,
		Failed:         t.ReconcileFailed,
		Moves:          collectStuckReconcileMoves(resp),
	}

	if t.ReconcileStuck == 0 {
		return functionResult{
			Function: fn, OK: true,
			Summary: fmt.Sprintf("nothing stuck (%s)", scopeLabel),
			Detail:  detail,
		}
	}

	parts := []string{fmt.Sprintf("%d stuck", t.ReconcileStuck)}
	if t.ReconcileRecategorised > 0 {
		parts = append(parts, fmt.Sprintf("%d moved", t.ReconcileRecategorised))
	}
	if t.ReconcileNeedsAttention > 0 {
		parts = append(parts, fmt.Sprintf("%d need attention", t.ReconcileNeedsAttention))
	}
	if t.ReconcileFailed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", t.ReconcileFailed))
	}
	return functionResult{
		Function: fn,
		// A failed row is a per-item problem, not a failed run — the
		// sweep itself worked and the row carries the reason. Same
		// convention the scheduler's reconcile phase uses.
		OK:      true,
		Changed: t.ReconcileRecategorised > 0,
		Summary: fmt.Sprintf("%s (%s)", strings.Join(parts, "; "), scopeLabel),
		Detail:  detail,
	}
}

// collectStuckReconcileMoves flattens the per-row results into one line
// per download that actually moved, deduped by hash (a Sonarr season
// pack is N queue rows sharing one torrent).
func collectStuckReconcileMoves(resp *scanResponse) []StuckReconcileMove {
	if resp == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []StuckReconcileMove
	for _, row := range resp.Reconcile {
		if row.Status != "recategorised" || row.QbitCategory == "" {
			continue
		}
		if seen[row.DownloadID] {
			continue
		}
		seen[row.DownloadID] = true
		out = append(out, StuckReconcileMove{
			Title:        row.Title,
			QbitInstance: row.QbitInstanceName,
			Category:     row.QbitCategory,
		})
		if len(out) >= stuckReconcileMaxMovesInNotification {
			break
		}
	}
	return out
}

// stuckReconcileMaxMovesInNotification bounds how many individual moves
// a notification lists before falling back to the count in the summary.
// A season pack whose every episode lost its race can move dozens at
// once; an embed listing all of them is unreadable and may blow
// Discord's field limit.
const stuckReconcileMaxMovesInNotification = 10
