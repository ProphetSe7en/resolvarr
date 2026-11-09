package api

// webhook_stuck_reconcile.go — webhook version of the Library scan's
// "Stuck downloads" action (scan_reconcile.go).
//
// A download gets stuck when a better release of the same item imports
// first; the Download event is that import, so it triggers a sweep of
// the same movie/series (never the whole queue — adapter rule 2 in
// webhook_adapters.go). The sweep is deferred: Sonarr/Radarr wait on the
// webhook reply, and the losing row needs time to settle into
// importPending/importBlocked. Events for the same (rule, item) share one
// sweep. Redundancy is decided only by engine.ClassifyStuckDownload via
// runReconcile.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"resolvarr/internal/core"
)

const (
	// maxConcurrentStuckSweeps caps sweeps running at once across all
	// items, so an import burst doesn't fan out into parallel queue reads.
	maxConcurrentStuckSweeps = 2
	// stuckReconcileMaxMovesInNotification caps the moves listed in one
	// notification; the summary still carries the full count.
	stuckReconcileMaxMovesInNotification = 10
)

// stuckReconcileDeferDelay returns the delay before the sweep runs:
// DeferSeconds, or 60s when unset, capped at 600s.
func stuckReconcileDeferDelay(rule *core.WebhookRule) time.Duration {
	secs := 60
	if rule != nil && rule.StuckReconcile != nil && rule.StuckReconcile.DeferSeconds > 0 {
		secs = rule.StuckReconcile.DeferSeconds
	}
	return time.Duration(min(secs, maxStuckReconcileDeferSeconds)) * time.Second
}

// stuckReconcileScope returns the movie or series an import event is
// about. ok=false means the payload names no item (a clean skip).
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
		return payload.Movie.ID, 0, orDefault(payload.Movie.Title, "this movie"), true
	case "sonarr":
		if payload.Series == nil || payload.Series.ID <= 0 {
			return 0, 0, "", false
		}
		return 0, payload.Series.ID, orDefault(payload.Series.Title, "this series"), true
	}
	return 0, 0, "", false
}

func orDefault(s, def string) string {
	if s = strings.TrimSpace(s); s != "" {
		return s
	}
	return def
}

// stuckSweepKey identifies one pending sweep: one rule acting on one item.
func stuckSweepKey(ruleID, appType string, movieID, seriesID int) string {
	return fmt.Sprintf("%s:%s:%d:%d", ruleID, appType, movieID, seriesID)
}

// claimStuckSweep reserves key for a sweep due at deadline. If one is
// already pending it returns false and pushes that sweep's deadline out,
// so every coalesced import still gets its full settling time.
func (s *Server) claimStuckSweep(key string, deadline time.Time) bool {
	s.stuckSweepMu.Lock()
	defer s.stuckSweepMu.Unlock()
	if s.stuckSweepPending == nil {
		s.stuckSweepPending = map[string]time.Time{}
	}
	if cur, pending := s.stuckSweepPending[key]; pending {
		if deadline.After(cur) {
			s.stuckSweepPending[key] = deadline
		}
		return false
	}
	s.stuckSweepPending[key] = deadline
	return true
}

// stuckSweepDue is called when a sweep's timer fires. If the deadline was
// pushed out it returns the remaining wait; otherwise it frees the key
// (later imports may queue a new sweep) and reports the sweep due.
func (s *Server) stuckSweepDue(key string, now time.Time) (wait time.Duration, due bool) {
	s.stuckSweepMu.Lock()
	defer s.stuckSweepMu.Unlock()
	if deadline, pending := s.stuckSweepPending[key]; pending && now.Before(deadline) {
		return deadline.Sub(now), false
	}
	delete(s.stuckSweepPending, key)
	return 0, true
}

// acquireStuckSweepSlot blocks until a sweep may run; call the returned
// func to free the slot.
func (s *Server) acquireStuckSweepSlot() func() {
	s.stuckSweepMu.Lock()
	if s.stuckSweepSlots == nil {
		s.stuckSweepSlots = make(chan struct{}, maxConcurrentStuckSweeps)
	}
	slots := s.stuckSweepSlots
	s.stuckSweepMu.Unlock()
	slots <- struct{}{}
	return func() { <-slots }
}

// scheduleDeferredStuckReconcile runs the sweep in the background after
// the rule's delay and records its own History entry and notification.
// The rule is re-read when the sweep runs, so a rule paused, edited or
// deleted meanwhile is respected. Pending sweeps are lost on restart; the
// next import schedules another. Returns false when a sweep for the same
// (rule, item) was already pending.
func (s *Server) scheduleDeferredStuckReconcile(rule *core.WebhookRule, inst *core.Instance, env *connectEventEnvelope, body []byte) bool {
	if rule == nil || inst == nil || env == nil {
		return false
	}
	// A payload with no item still gets a sweep; it reports the skip.
	movieID, seriesID, _, _ := stuckReconcileScope(rule.AppType, body)
	key := stuckSweepKey(rule.ID, rule.AppType, movieID, seriesID)
	delay := stuckReconcileDeferDelay(rule)
	if !s.claimStuckSweep(key, time.Now().Add(delay)) {
		return false
	}
	ruleID := rule.ID
	instCopy := *inst
	envCopy := *env
	bodyCopy := append([]byte(nil), body...)
	started := time.Now().UTC()
	var fire func()
	fire = func() {
		if wait, due := s.stuckSweepDue(key, time.Now()); !due {
			time.AfterFunc(wait, fire)
			return
		}
		defer func() {
			if rec := recover(); rec != nil {
				fmt.Fprintf(os.Stderr, "resolvarr: deferred stuck-downloads sweep panicked for rule %s: %v\n", ruleID, rec)
			}
		}()
		release := s.acquireStuckSweepSlot()
		defer release()
		cfg := s.App.Config.Get()
		current := findWebhookRuleByID(cfg, ruleID)
		if !stuckReconcileStillWanted(current) {
			return
		}
		ruleCopy := *current
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		res := s.dispatchStuckReconcile(ctx, &ruleCopy, cfg, &envCopy, bodyCopy)
		run := buildWebhookRuleRun(&envCopy, bodyCopy, started, []functionResult{res})
		s.fireWebhookNotifications(&ruleCopy, &instCopy, &envCopy, bodyCopy, []functionResult{res}, run)
		s.appendWebhookRuleRunsBatch([]pendingRuleRun{{ruleID: ruleCopy.ID, run: run}})
	}
	time.AfterFunc(delay, fire)
	return true
}

// stuckReconcileStillWanted reports whether the rule, as it is now in
// config, still exists, is enabled and has the function ticked.
func stuckReconcileStillWanted(rule *core.WebhookRule) bool {
	if rule == nil || !rule.Enabled {
		return false
	}
	for _, fn := range rule.Functions {
		if fn == core.WebhookFnStuckReconcile {
			return true
		}
	}
	return false
}

// dispatchStuckReconcile runs one sweep for the item the event names.
// OK=false only when the sweep itself fails; failed moves are per-row.
// Changed (which drives notifications) is set when something moved or
// failed, so a sweep that found nothing stays quiet.
func (s *Server) dispatchStuckReconcile(ctx context.Context, rule *core.WebhookRule, cfg core.Config, env *connectEventEnvelope, body []byte) functionResult {
	const fn = core.WebhookFnStuckReconcile

	if env.EventType != string(core.WebhookEventDownload) {
		return functionResult{Function: fn, OK: true, Summary: "skipped (not a Download event)"}
	}
	if rule.StuckReconcile == nil || len(rule.StuckReconcile.Targets) == 0 {
		return functionResult{Function: fn, OK: false, Summary: "rule has the stuck-downloads function but no download clients configured"}
	}
	inst := findInstanceByID(cfg, rule.InstanceID)
	if inst == nil {
		return functionResult{Function: fn, OK: false, Summary: "instance vanished between event receive and dispatch"}
	}
	movieID, seriesID, scopeLabel, ok := stuckReconcileScope(rule.AppType, body)
	if !ok {
		return functionResult{Function: fn, OK: true, Summary: "skipped (no movie/series on the event to scope the sweep to)"}
	}

	req := scanRunRequest{
		InstanceID:            rule.InstanceID,
		Action:                "reconcile",
		Mode:                  "apply",
		ReconcileOnlyMovieID:  movieID,
		ReconcileOnlySeriesID: seriesID,
	}
	for _, t := range rule.StuckReconcile.Targets {
		req.ReconcileTargets = append(req.ReconcileTargets, scanReconcileTarget{QbitInstanceID: t.QbitInstanceID, PostCategory: t.PostCategory})
	}

	resp, apiErr := s.runReconcile(ctx, cfg, inst, rule.AppType, req)
	s.auditScan("webhook:"+rule.ID, "reconcile", inst, req, resp, errMsgOf(apiErr))
	if apiErr != nil {
		// Runs after the webhook reply, so notify: History is the only
		// other place this would show.
		return functionResult{
			Function: fn, OK: false, Changed: true,
			Summary: "stuck-downloads sweep", Err: apiErr,
			Detail: StuckReconcileDetail{Scope: scopeLabel, SweepError: apiErr.Message},
		}
	}

	detail := summariseStuckReconcile(resp)
	detail.Scope = scopeLabel
	if detail.Stuck == 0 {
		return functionResult{Function: fn, OK: true, Summary: fmt.Sprintf("nothing stuck (%s)", scopeLabel), Detail: detail}
	}

	parts := []string{fmt.Sprintf("%d stuck", detail.Stuck)}
	for _, p := range []struct {
		n    int
		text string
	}{
		{detail.Moved, "moved"},
		{detail.AlreadyMoved, "already moved"},
		{detail.NeedsAttention, "need attention"},
		{detail.Failed, "failed: " + detail.FailReason},
	} {
		if p.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", p.n, p.text))
		}
	}
	return functionResult{
		Function: fn,
		OK:       true,
		Changed:  detail.Moved > 0 || detail.Failed > 0,
		Summary:  fmt.Sprintf("%s (%s)", strings.Join(parts, "; "), scopeLabel),
		Detail:   detail,
	}
}

// summariseStuckReconcile counts outcomes per download, not per queue
// row: a season pack is one row per episode but a single torrent. A
// download takes its strongest row outcome (failed > moved > already
// moved > needs attention > redundant).
func summariseStuckReconcile(resp *scanResponse) StuckReconcileDetail {
	var d StuckReconcileDetail
	if resp == nil {
		return d
	}
	const (
		outRedundant = iota
		outNeedsAttention
		outAlreadyMoved
		outMoved
		outFailed
	)
	outcome := map[string]int{}
	moves := map[string]StuckReconcileMove{}
	var order []string
	for _, row := range resp.Reconcile {
		key := row.DownloadID
		if key == "" {
			key = "row:" + row.Title
		}
		o := outRedundant
		switch {
		case row.Status == "failed":
			o = outFailed
			if d.FailReason == "" {
				d.FailReason = row.Error
			}
		case row.Status == "recategorised" && row.AlreadyInCategory:
			o = outAlreadyMoved
		case row.Status == "recategorised":
			o = outMoved
			if _, ok := moves[key]; !ok && row.QbitCategory != "" {
				moves[key] = StuckReconcileMove{Title: row.Title, QbitInstance: row.QbitInstanceName, Category: row.QbitCategory}
			}
		case row.Status != "redundant":
			o = outNeedsAttention
		}
		if prev, seen := outcome[key]; !seen {
			order = append(order, key)
			outcome[key] = o
		} else if o > prev {
			outcome[key] = o
		}
	}
	d.Stuck = len(order)
	for _, key := range order {
		switch outcome[key] {
		case outMoved:
			d.Moved++
			if m, ok := moves[key]; ok && len(d.Moves) < stuckReconcileMaxMovesInNotification {
				d.Moves = append(d.Moves, m)
			}
		case outAlreadyMoved:
			d.AlreadyMoved++
		case outFailed:
			d.Failed++
		case outNeedsAttention:
			d.NeedsAttention++
		}
	}
	return d
}
