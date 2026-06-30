package api

// qbit_event.go — receiver for qBit's "Run external program on torrent
// added" hook. qBit curls POST /api/qbit/torrent-added/{instanceId}
// for every newly-added torrent (cross-seed, manual, Sonarr-Connect-
// grabbed all flow through here). Per-rule debounce buffer aggregates
// burst-events into ONE history entry per window.
//
// This file: HTTP handler (auth + parse + enqueue) + flush callback
// wiring (classify + tag-apply + history). The buffer itself lives in
// qbit_event_buffer.go.

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"resolvarr/internal/core"
	"resolvarr/internal/core/engine"
	"resolvarr/internal/qbit"
)

// QbitEventBuffer returns the shared per-server qBit event buffer,
// constructing it on first call. Lazy-allocated like ArrDLCache so
// test factories that build Server{} directly don't need explicit
// wiring.
//
// The buffer's flush callback is bound to s.flushQbitAggregated —
// runs classification + tagging + writes a single WebhookRuleRun
// history entry per drained window.
func (s *Server) QbitEventBuffer() *qbitEventBuffer {
	s.qbitBufferMu.Lock()
	defer s.qbitBufferMu.Unlock()
	if s.qbitBuffer == nil {
		s.qbitBuffer = newQbitEventBuffer(s.flushQbitAggregated)
	}
	return s.qbitBuffer
}

// handleQbitTorrentAdded is the receiver for qBit's "Run external
// program on torrent added" hook.
//
//	URL:     POST /api/qbit/torrent-added/{instanceId}
//	Headers: X-API-Key: <QbitInstance.WebhookSecret>
//	Body:    form-encoded — infoHash + name + category (category optional)
//
// Response shape:
//   - 202 + {"queued":N} on enqueue (work happens async via flush)
//   - 401 on auth failure (missing or wrong X-API-Key)
//   - 400 on missing infoHash or name
//
// Auth-failure responses don't reveal whether the instance exists —
// we look up the instance first to find the secret to compare against,
// but on a key mismatch we emit a generic "unauthorized" so a probe
// can't enumerate valid instance IDs by watching status codes.
func (s *Server) handleQbitTorrentAdded(w http.ResponseWriter, r *http.Request) {
	instanceID := strings.TrimSpace(r.PathValue("instanceId"))
	if instanceID == "" {
		fmt.Fprintf(os.Stderr, "resolvarr: qbit-add 400 — missing instanceId in URL path\n")
		writeError(w, 400, "missing instanceId in URL path")
		return
	}

	cfg := s.App.Config.Get()
	qbitInst := findQbitInstanceByID(cfg, instanceID)

	// Auth gate — constant-time-compare the supplied X-API-Key against
	// the per-instance WebhookSecret. We deliberately do the lookup +
	// compare BEFORE returning a 404 for unknown instances so the
	// timing of "unknown instance" vs "wrong key on known instance"
	// is indistinguishable to a network probe.
	suppliedKey := strings.TrimSpace(r.Header.Get("X-API-Key"))
	var storedKey string
	if qbitInst != nil {
		storedKey = qbitInst.WebhookSecret
	}
	if storedKey == "" || suppliedKey == "" ||
		subtle.ConstantTimeCompare([]byte(suppliedKey), []byte(storedKey)) != 1 {
		// Reason breakdown helps diagnose silently-failing setups
		// without leaking which-of-three actually failed to a probe
		// (the response is still a generic 401).
		reason := "wrong key"
		switch {
		case qbitInst == nil:
			reason = "unknown instance id"
		case storedKey == "":
			reason = "instance has no stored secret"
		case suppliedKey == "":
			reason = "missing X-API-Key header"
		}
		fmt.Fprintf(os.Stderr, "resolvarr: qbit-add 401 unauthorized for instance %q — %s\n", instanceID, reason)
		writeError(w, 401, "unauthorized")
		return
	}

	// Body parse — qBit sends form-encoded body via curl --data-urlencode.
	if err := r.ParseForm(); err != nil {
		fmt.Fprintf(os.Stderr, "resolvarr: qbit-add 400 for instance %q — invalid form body: %v\n", instanceID, err)
		writeError(w, 400, "invalid form body")
		return
	}
	infoHash := strings.ToLower(strings.TrimSpace(r.Form.Get("infoHash")))
	name := strings.TrimSpace(r.Form.Get("name"))
	category := strings.TrimSpace(r.Form.Get("category"))
	if infoHash == "" {
		fmt.Fprintf(os.Stderr, "resolvarr: qbit-add 400 for instance %q — missing infoHash\n", instanceID)
		writeError(w, 400, "missing infoHash")
		return
	}

	// Webhook round-trip probe: if this is the synthetic test torrent for a
	// pending probe on this instance, signal it and stop. The secret was
	// already verified above, so a successful probe proves the full chain
	// (qBit autorun -> network -> resolvarr receiver -> secret). The test
	// torrent must NOT be classified, tagged, notified, or logged as a real
	// add.
	if s.signalQbitProbe(instanceID, infoHash) {
		writeJSON(w, map[string]string{"status": "probe-received"})
		return
	}
	if name == "" {
		fmt.Fprintf(os.Stderr, "resolvarr: qbit-add 400 for instance %q — missing name (hash=%s)\n", instanceID, shortInfoHash(infoHash))
		writeError(w, 400, "missing name")
		return
	}

	// Find rules: enabled + has WebhookFnQbitSeTag in Functions +
	// QbitSe.QbitInstanceID matches our path.
	matchingRules := matchQbitSeRulesForInstance(cfg, instanceID)

	// Apply tags EAGERLY — qBit gets the tag within the request, not
	// after the aggregation window. The buffer is now purely for
	// history-row + notification batching; tag-latency is independent
	// of the configured window. AddTags is idempotent so multiple
	// concurrent fires for the same hash converge to the same final
	// state. Errors are recorded on the event and surfaced in the
	// summary at flush; we never fail the receive response because
	// qBit doesn't reattempt on autorun-curl failure anyway.
	receivedAt := time.Now().UTC()

	// Arr import-category map (cached) for the movie/series booster — built
	// once per add, only when a rule will actually use it. Bounded so a
	// slow Arr can't hold the autorun-curl response open; on timeout/error
	// the sets are nil and classification uses the name+files floor.
	var movieCats, seriesCats map[string]bool
	if len(matchingRules) > 0 {
		catCtx, catCancel := context.WithTimeout(r.Context(), 5*time.Second)
		movieCats, seriesCats = s.buildArrCategorySets(catCtx, cfg)
		catCancel()
	}

	buf := s.QbitEventBuffer()
	enqueued := 0
	matchedAny := false
	perRuleEvents := make([]qbitAddEvent, 0, len(matchingRules))
	for _, rule := range matchingRules {
		// Per-rule event so eager-apply results (Matched / AppliedTag /
		// ApplyErrMsg) stay scoped to the rule that produced them.
		// Otherwise two rules sharing an event would clobber each
		// other's results at the aggregation summary.
		ev := qbitAddEvent{
			InfoHash: infoHash,
			Name:     name,
			Category: category,
			Received: receivedAt,
			RuleID:   rule.ID,
			RuleName: rule.Name,
		}
		s.eagerApplyQbitSeTag(r.Context(), qbitInst, &rule, &ev, movieCats, seriesCats)
		if ev.Matched {
			matchedAny = true
		}
		windowSec := 0
		if rule.QbitSe != nil {
			windowSec = rule.QbitSe.AggregationWindowSeconds
		}
		buf.Enqueue(rule.ID, ev, windowSec)
		perRuleEvents = append(perRuleEvents, ev)
		enqueued++
	}

	// Log one activity entry per torrent into the qBit-webhook view
	// (webhook log keyed by the qBit instance ID, a separate key
	// namespace from Arr-Connect events). This is what makes "did the
	// qBit webhook fire, and what did it do / why" visible in Recent
	// Activity. Logged even when no rule matched, so a delivered-but-
	// ignored add is still visible (the common "wrong instance / no
	// qBit S/E rule" confusion).
	s.logQbitAddActivity(instanceID, genID(), receivedAt, infoHash, name, category, perRuleEvents, false)

	// One-line receipt log per torrent. Truncated name + short hash
	// keeps the line scannable; rule-count surfaces the most-common
	// "no rule matched" silent-skip case without needing the History
	// modal. category is included since cross-seed setups use it as
	// the routing signal. matched=true when at least one rule's
	// classifier produced a tag (whether AddTags succeeded or not —
	// per-event ApplyErrMsg captures the failure side).
	fmt.Fprintf(os.Stderr,
		"resolvarr: qbit-add 202 instance=%q hash=%s category=%q name=%q queued=%d matched=%v\n",
		instanceID, shortInfoHash(infoHash), category, truncateForLog(name, 80), enqueued, matchedAny)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusAccepted)
	fmt.Fprintf(w, `{"queued":%d}`, enqueued)
}

// eagerApplyQbitSeTag classifies the event against ONE rule's QbitSe
// view + calls AddTags on qBit synchronously, recording the result on
// the event. Called inline from handleQbitTorrentAdded so tags land
// in qBit before the request returns — independent of the aggregation
// window, which now only controls history/notification batching.
//
// On classifier no-match: leaves Matched=false, AppliedTag="" — the
// summary at flush will skip this event. On AddTags failure: records
// ApplyErrMsg + still marks Matched=true so the summary surfaces the
// attempt + error. Errors don't propagate to the receive response —
// qBit autorun-curl doesn't retry on failure, so blocking the receive
// would just lose the chance to write a history entry.
func (s *Server) eagerApplyQbitSeTag(
	ctx context.Context,
	qbitInst *core.QbitInstance,
	rule *core.WebhookRule,
	ev *qbitAddEvent,
	movieCats, seriesCats map[string]bool,
) {
	if rule == nil || rule.QbitSe == nil || qbitInst == nil {
		return
	}
	view := engine.QbitSeRulesView{
		EpisodeEnabled:   rule.QbitSe.EpisodeEnabled,
		EpisodeTag:       rule.QbitSe.EpisodeTag,
		SeasonEnabled:    rule.QbitSe.SeasonEnabled,
		SeasonTag:        rule.QbitSe.SeasonTag,
		UnmatchedEnabled: rule.QbitSe.UnmatchedEnabled,
		UnmatchedTag:     rule.QbitSe.UnmatchedTag,
	}

	client, initErr := qbit.New(qbit.Config{
		URL:          qbitInst.URL,
		Username:     qbitInst.Username,
		Password:     qbitInst.Password,
		TrustedCerts: qbitInst.TrustedCerts,
	})

	// Cap the qBit roundtrips (file-list fetch + AddTags) so a slow
	// tracker-side qBit can't hold the autorun-curl response open. 10s is
	// generous — local qBit calls are usually <100ms.
	opCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// Content-aware classification: reconcile the name with the torrent's
	// file list + the Arr-category hint, same as the backlog scan. The file
	// list can be empty at add time (e.g. a magnet whose metadata hasn't
	// resolved yet), or unavailable if the client failed to init — either
	// way ClassifyTorrentTypeWithHint falls back to name-only.
	var fileViews []engine.TorrentFileView
	if initErr == nil {
		if files, ferr := client.ListTorrentFiles(opCtx, ev.InfoHash); ferr == nil {
			fileViews = make([]engine.TorrentFileView, 0, len(files))
			for _, f := range files {
				fileViews = append(fileViews, engine.TorrentFileView{Name: f.Name, Size: f.Size})
			}
		}
	}
	hint := categoryHint(ev.Category, movieCats, seriesCats)
	res := engine.ClassifyTorrentTypeWithHint(ev.Name, fileViews, hint)
	// Capture the reason regardless of whether a tag applies, so the
	// qBit-webhook activity view can explain a skip ("why nothing happened")
	// as well as a tag.
	ev.Reason = res.Reason
	tag := engine.DetermineQbitTagFromClass(res.Class, view)
	if tag == "" {
		// No tag for this torrent — leave Matched=false and ApplyErrMsg
		// empty (invariant: an unmatched event never carries an error),
		// even when the client failed to init above.
		return
	}
	ev.AppliedTag = tag
	ev.Matched = true

	// Only now, with a tag to apply, surface a client-init failure.
	if initErr != nil {
		ev.ApplyErrMsg = "qbit client init: " + initErr.Error()
		return
	}
	// Skip when the torrent already carries this tag (re-add, cross-seed,
	// a double-fire of the hook, or the Connect qbitSe path tagged it first).
	// AddTags is idempotent on qBit's side, but counting it as a change would
	// fire a notification with nothing actually changed. Mirrors the Connect
	// path's qbitHasTag guard. Fail-open: if we can't read the tags, fall
	// through to AddTags and behave as before.
	if t, found, gerr := client.GetTorrent(opCtx, ev.InfoHash); gerr == nil && found && qbitHasTag(t.Tags, tag) {
		ev.AlreadyTagged = true
		return
	}
	if err := client.AddTags(opCtx, []string{ev.InfoHash}, []string{tag}); err != nil {
		ev.ApplyErrMsg = err.Error()
	}
}

// qbitEventItemTitle builds the History-modal display name for a
// flushed batch. See the call site for the design rationale.
func qbitEventItemTitle(events []qbitAddEvent) string {
	if len(events) == 0 {
		return "(empty window)"
	}
	if len(events) == 1 {
		return events[0].Name + " · " + shortInfoHash(events[0].InfoHash)
	}
	// Multi-event window — detect "all same torrent" so cross-seed
	// duplicate-burst becomes obvious. Same-name + same-hash → one
	// torrent, fired N times. Anything else → mixed window.
	first := events[0]
	allSame := true
	for _, e := range events[1:] {
		if e.Name != first.Name || e.InfoHash != first.InfoHash {
			allSame = false
			break
		}
	}
	if allSame {
		return fmt.Sprintf("%s · %s (×%d)", first.Name, shortInfoHash(first.InfoHash), len(events))
	}
	return fmt.Sprintf("%d torrent(s) in window: %s + others", len(events), first.Name)
}

// shortInfoHash returns the first 8 chars of an infoHash — enough to
// correlate with qBit's logs without bloating every line with the
// full 40-char hash.
func shortInfoHash(h string) string {
	if len(h) <= 8 {
		return h
	}
	return h[:8]
}

// truncateForLog clips a string to maxLen runes + a trailing "…" so
// long torrent names don't overflow log lines. ASCII-only inputs in
// practice (torrent names rarely have multibyte chars), so byte-slice
// is safe + cheap.
func truncateForLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
}

// matchQbitSeRulesForInstance returns enabled webhook rules that have
// qbitSeTag function AND target the given qBit instance ID. Pure
// function — no I/O, no side effects. Tested in isolation.
func matchQbitSeRulesForInstance(cfg core.Config, qbitInstanceID string) []core.WebhookRule {
	out := make([]core.WebhookRule, 0, 4)
	for _, r := range cfg.WebhookRules {
		if !r.Enabled {
			continue
		}
		if !r.HasFunction(core.WebhookFnQbitSeTag) {
			continue
		}
		if r.QbitSe == nil || r.QbitSe.QbitInstanceID != qbitInstanceID {
			continue
		}
		out = append(out, r)
	}
	return out
}

// qbitTagBatch groups events by their classified tag so AddTags can
// be called once per (tag, batch) instead of once per event.
type qbitTagBatch struct {
	tag    string
	hashes []string
	names  []string
}

// qbitTagResult is the per-tag outcome of a flushed window. Captured
// for the rolled-up history summary.
type qbitTagResult struct {
	tag           string
	applied       int
	alreadyTagged int // torrents that already had the tag (no change, no notify)
	failed        bool
	errMsg        string
	examples      []string // first few names for the history detail
}

// flushQbitAggregated is the buffer's drain callback. Runs in a
// goroutine spawned by time.AfterFunc when a rule's window expires.
//
// After the eager-apply refactor: tags are already in qBit before the
// window closes. This function ONLY summarises the per-event results
// already recorded on each qbitAddEvent and writes ONE WebhookRuleRun
// history row. No classification or AddTags happens here.
//
// Per-event states:
//   - Matched=false      → classifier produced no tag; skipped silently
//   - Matched=true, no ApplyErrMsg → tag was applied successfully
//   - Matched=true, ApplyErrMsg    → apply attempt failed at receive
//
// The buffer is now purely a UX aggregator: it batches per-event noise
// into one history row + (when wired) one notification. Tag latency is
// independent — already applied within the receive request.
func (s *Server) flushQbitAggregated(ctx context.Context, ruleID string, events []qbitAddEvent) {
	_ = ctx // no longer used for qBit roundtrip; retained for callback signature
	if len(events) == 0 {
		return
	}
	startedAt := time.Now().UTC()

	// Roll up per-tag counts from the eager-apply results.
	type aggRow struct {
		tag           string
		applied       int
		alreadyTagged int
		failed        int
		firstErr      string
		examples      []string
	}
	byTag := map[string]*aggRow{}
	tagOrder := make([]string, 0, 3)
	matchedCount := 0
	for _, ev := range events {
		if !ev.Matched {
			continue
		}
		matchedCount++
		row, ok := byTag[ev.AppliedTag]
		if !ok {
			row = &aggRow{tag: ev.AppliedTag}
			byTag[ev.AppliedTag] = row
			tagOrder = append(tagOrder, ev.AppliedTag)
		}
		switch {
		case ev.ApplyErrMsg != "":
			row.failed++
			if row.firstErr == "" {
				row.firstErr = ev.ApplyErrMsg
			}
			if len(row.examples) < 5 {
				row.examples = append(row.examples, ev.Name)
			}
		case ev.AlreadyTagged:
			// Already had the tag → no change. Counted for the history view,
			// but not as an "applied" so it never triggers a notification.
			row.alreadyTagged++
		default:
			row.applied++
			if len(row.examples) < 5 {
				row.examples = append(row.examples, ev.Name)
			}
		}
	}

	if matchedCount == 0 {
		// No event in the window matched any rule branch. Still emit a
		// history entry so the user sees the rule fired but produced
		// no tags (helps distinguish "rule never triggered" from
		// "rule triggered but classifier rejected everything").
		s.appendQbitAggregatedHistory(ruleID, startedAt, events, nil,
			fmt.Sprintf("no rule matched on %d torrent(s)", len(events)), "ok")
		return
	}

	results := make([]qbitTagResult, 0, len(byTag))
	hadError := false
	hadSuccess := false
	for _, tag := range tagOrder {
		row := byTag[tag]
		r := qbitTagResult{tag: tag, examples: row.examples, alreadyTagged: row.alreadyTagged}
		if row.failed > 0 {
			hadError = true
			r.failed = true
			r.errMsg = row.firstErr
		}
		if row.applied > 0 {
			hadSuccess = true
			r.applied = row.applied
		}
		results = append(results, r)
	}

	status := "ok"
	switch {
	case hadError && !hadSuccess:
		status = "error"
	case hadError:
		status = "partial"
	}

	s.appendQbitAggregatedHistory(ruleID, startedAt, events, results, "", status)
	// Notify on the qBit-add path itself; the tag was already applied
	// here, so the Connect qbitSeTag stays silent (already-tagged) and
	// this is the single notifying surface. Gated on OnGrab.
	s.notifyQbitAddResult(ruleID, results, status)
}

// appendQbitAggregatedHistory writes ONE WebhookRuleRun summarising
// the flushed window. Summary follows the dispatcher's "fn: result"
// format so the History modal's parser (parseRuleRunSummary in app.js)
// renders it using the same per-function row layout — qbitSeTag is
// the only "function" represented since this path is single-purpose.
//
// Status semantics match buildWebhookRuleRun: "ok" / "partial" / "error".
//
// On a fatal pre-classify failure (qBit instance gone), pass results=nil
// + a non-empty fatalReason; the function emits an error-status entry
// with the reason as the qbitSeTag summary text.
func (s *Server) appendQbitAggregatedHistory(
	ruleID string,
	startedAt time.Time,
	events []qbitAddEvent,
	results []qbitTagResult,
	fatalReason string,
	status string,
) {
	durationMs := time.Since(startedAt).Milliseconds()

	// Build the "qbitSeTag: ..." summary string. Format mirrors what
	// the dispatcher emits per function so the frontend parser splits
	// on "; " then "fn: result" gets a familiar shape.
	var summary string
	switch {
	case fatalReason != "":
		summary = "qbitSeTag: error: " + fatalReason
	case len(results) == 0:
		summary = "qbitSeTag: no change (no rule matched)"
	default:
		totalApplied := 0
		totalFailed := 0
		totalAlready := 0
		var firstErr string
		parts := make([]string, 0, len(results))
		for _, r := range results {
			totalAlready += r.alreadyTagged
			if r.failed {
				totalFailed++
				if firstErr == "" {
					firstErr = r.errMsg
				}
				parts = append(parts, fmt.Sprintf("%s: failed (%s)", r.tag, r.errMsg))
				continue
			}
			totalApplied += r.applied
			switch {
			case r.applied > 0 && r.alreadyTagged > 0:
				parts = append(parts, fmt.Sprintf("%s: %d (%d already tagged)", r.tag, r.applied, r.alreadyTagged))
			case r.applied > 0:
				parts = append(parts, fmt.Sprintf("%s: %d", r.tag, r.applied))
			case r.alreadyTagged > 0:
				parts = append(parts, fmt.Sprintf("%s: already tagged %d", r.tag, r.alreadyTagged))
			}
		}
		breakdown := strings.Join(parts, ", ")
		switch {
		case totalApplied > 0 && totalFailed == 0:
			summary = fmt.Sprintf("qbitSeTag: tagged %d (%s)", totalApplied, breakdown)
		case totalApplied > 0 && totalFailed > 0:
			// Surface "partial" in the chip text so the History modal's
			// status icon and the result-prefix tell the same story —
			// otherwise the green ▲ chip says "tagged 5" while the
			// underlying status is "partial" + breakdown shows failures.
			summary = fmt.Sprintf("qbitSeTag: tagged %d with %d error(s) (%s)", totalApplied, totalFailed, breakdown)
		case totalFailed > 0:
			summary = fmt.Sprintf("qbitSeTag: error: tagging failed (%s)", breakdown)
		default:
			// No new tags and no failures: every matched torrent already had
			// the tag. A real no-op, recorded but never notified.
			summary = fmt.Sprintf("qbitSeTag: no change (already tagged %d)", totalAlready)
		}
	}

	// itemTitle renders in the History modal as the row's display
	// name. Two refinements over plain torrent name:
	//
	//  - Single event: append the short infoHash so multiple history
	//    entries for the SAME torrent (cross-seed duplicate fires,
	//    pause/resume re-adds) stay visually distinct. Same-name +
	//    same-hash rows were indistinguishable before.
	//
	//  - Multi-event window: if all events share the same name + hash
	//    (the common cross-seed-burst case), collapse to
	//    "<name> · <hash> (×N)" so the duplicate-count is obvious.
	//    Mixed names/hashes keep the per-count summary + first name.
	itemTitle := qbitEventItemTitle(events)

	// qBit-add Changed semantics: window had at least one successful
	// AddTags call. Mirrors the rule-fire path so the "Made changes"
	// filter on Recent Activity / Rule History treats qBit-add fires
	// consistently. Fatal-reason and "no rule matched on N torrents"
	// keep Changed=false (no state mutation happened).
	changed := false
	for _, r := range results {
		if r.applied > 0 {
			changed = true
			break
		}
	}
	run := core.WebhookRuleRun{
		StartedAt:   startedAt,
		DurationMs:  durationMs,
		Status:      status,
		EventType:   "qbit:torrentAdded",
		ItemTitle:   itemTitle,
		ItemContext: fmt.Sprintf("aggregated %d", len(events)),
		Summary:     summary,
		Changed:     changed,
	}
	s.appendWebhookRuleRunsBatch([]pendingRuleRun{{ruleID: ruleID, run: run}})
	// M-Webhook notifications are NOT wired here even when the rule
	// has NotifyOnFire=true. Reasons:
	//   - EventType "qbit:torrentAdded" isn't a core.WebhookConnectEvent
	//     constant — agentSubscribesToEvent unconditionally returns
	//     false for non-Connect events, so the fallback path fires no
	//     agents at all. Whitelist mode would technically work but
	//     leaves only opt-in users with notifications — inconsistent
	//     UX vs Sonarr/Radarr Connect events.
	//   - There's no Connect-shaped body here (qBit POSTs a different
	//     payload), so extractPosterURL produces no thumbnail.
	//   - The "instance" context is a QbitInstance, not an Arr Instance,
	//     so the buildInstanceList "primary Arr" framing is wrong.
	// If demand surfaces (user explicitly asks for cross-seed catch-up
	// notifications), extend the framework with a qBit-event class +
	// agent flag (e.g. OnQbitAdd) + qBit-specific Detail payloads.
	// Until then: History records the fire so the user can audit; the
	// notification surface stays Connect-only.
}
