package api

// qbit_event.go — receiver for qBit's "Run external program on torrent
// added" hook. qBit curls POST /api/qbit/torrent-added/{instanceId}
// for every newly-added torrent (cross-seed, manual, Sonarr-Connect-
// grabbed all flow through here). Per-rule debounce buffer aggregates
// burst-events into ONE history entry per window. See M-qBit-add
// design doc at dev/analysis/M-qbit-add.md.
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
		writeError(w, 401, "unauthorized")
		return
	}

	// Body parse — qBit sends form-encoded body via curl --data-urlencode.
	if err := r.ParseForm(); err != nil {
		writeError(w, 400, "invalid form body")
		return
	}
	infoHash := strings.ToLower(strings.TrimSpace(r.Form.Get("infoHash")))
	name := strings.TrimSpace(r.Form.Get("name"))
	category := strings.TrimSpace(r.Form.Get("category"))
	if infoHash == "" {
		writeError(w, 400, "missing infoHash")
		return
	}
	if name == "" {
		writeError(w, 400, "missing name")
		return
	}

	// Find rules: enabled + has WebhookFnQbitSeTag in Functions +
	// QbitSe.QbitInstanceID matches our path.
	matchingRules := matchQbitSeRulesForInstance(cfg, instanceID)

	ev := qbitAddEvent{
		InfoHash: infoHash,
		Name:     name,
		Category: category,
		Received: time.Now().UTC(),
	}

	buf := s.QbitEventBuffer()
	enqueued := 0
	for _, rule := range matchingRules {
		windowSec := 0
		if rule.QbitSe != nil {
			windowSec = rule.QbitSe.AggregationWindowSeconds
		}
		buf.Enqueue(rule.ID, ev, windowSec)
		enqueued++
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusAccepted)
	fmt.Fprintf(w, `{"queued":%d}`, enqueued)
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
	tag      string
	applied  int
	failed   bool
	errMsg   string
	examples []string // first few names for the history detail
}

// flushQbitAggregated is the buffer's drain callback. Runs in a
// goroutine spawned by time.AfterFunc when a rule's window expires.
//
// For each event in the batch:
//  1. Classify torrent name via engine.DetermineQbitTag
//  2. Group by classified tag → one AddTags call per (tag, batch)
//  3. Apply tags to qBit (idempotent — qBit no-ops on already-set tags)
//  4. Build ONE WebhookRuleRun history entry summarising counts
//  5. Append via appendWebhookRuleRunsBatch
//
// Errors from qBit are tolerated per-batch — a failed AddTags for one
// tag doesn't abort the others. The rolled-up summary surfaces the
// failure count + first error message; user can drill in via the
// History modal to debug.
//
// Re-reads config snapshot at flush time (not capture from enqueue) —
// rule may have been edited or disabled between enqueue and fire;
// always act on current state.
//
// Mid-window edit semantics: if the user changes
// AggregationWindowSeconds while a window is open, the change applies
// only to the NEXT window. The currently-open window keeps its
// original duration (the AfterFunc timer was scheduled at Enqueue
// time). Other rule edits (tag names, enabled flags, target qBit
// instance) DO take effect at flush time because we re-read config
// here. This split is intentional — re-arming the timer on every
// edit would risk premature flush AND defeats the user's expectation
// that "the running window completes as scheduled".
func (s *Server) flushQbitAggregated(ctx context.Context, ruleID string, events []qbitAddEvent) {
	if len(events) == 0 {
		return
	}
	startedAt := time.Now().UTC()

	cfg := s.App.Config.Get()

	// Re-resolve the rule + qBit instance — both may have changed
	// between enqueue and now. Defensive skip-with-empty-summary if
	// either is gone (rare but possible during config edits).
	var rule *core.WebhookRule
	for i := range cfg.WebhookRules {
		if cfg.WebhookRules[i].ID == ruleID {
			rule = &cfg.WebhookRules[i]
			break
		}
	}
	if rule == nil || !rule.Enabled || !rule.HasFunction(core.WebhookFnQbitSeTag) || rule.QbitSe == nil {
		// Rule deleted/disabled/restructured mid-window. Drop events
		// silently; a history entry would be confusing ("8 events for
		// a rule that doesn't exist anymore"). Logged for post-mortem.
		fmt.Fprintf(os.Stderr, "resolvarr: qbit-add window for rule %q dropped — rule no longer matches at flush time (events=%d)\n", ruleID, len(events))
		return
	}
	qbitInst := findQbitInstanceByID(cfg, rule.QbitSe.QbitInstanceID)
	if qbitInst == nil {
		s.appendQbitAggregatedHistory(ruleID, startedAt, events, nil,
			fmt.Sprintf("qbit instance %q not found in config", rule.QbitSe.QbitInstanceID), "error")
		return
	}

	// Classify each event. Group by tag for AddTags batching.
	view := engine.QbitSeRulesView{
		EpisodeEnabled:   rule.QbitSe.EpisodeEnabled,
		EpisodeTag:       rule.QbitSe.EpisodeTag,
		SeasonEnabled:    rule.QbitSe.SeasonEnabled,
		SeasonTag:        rule.QbitSe.SeasonTag,
		UnmatchedEnabled: rule.QbitSe.UnmatchedEnabled,
		UnmatchedTag:     rule.QbitSe.UnmatchedTag,
	}
	byTag := map[string]*qbitTagBatch{}
	tagOrder := make([]string, 0, 3)
	for _, ev := range events {
		tag := engine.DetermineQbitTag(ev.Name, view)
		if tag == "" {
			continue
		}
		batch, ok := byTag[tag]
		if !ok {
			batch = &qbitTagBatch{tag: tag}
			byTag[tag] = batch
			tagOrder = append(tagOrder, tag)
		}
		batch.hashes = append(batch.hashes, ev.InfoHash)
		batch.names = append(batch.names, ev.Name)
	}

	if len(byTag) == 0 {
		// Every event landed on a disabled rule branch (Episode/Season/
		// Unmatched all off, or no patterns matched). Still emit a
		// history entry so the user sees the rule fired but produced
		// no tags.
		s.appendQbitAggregatedHistory(ruleID, startedAt, events, nil,
			fmt.Sprintf("no rule matched on %d torrent(s)", len(events)), "ok")
		return
	}

	client, err := qbit.New(qbit.Config{
		URL:          qbitInst.URL,
		Username:     qbitInst.Username,
		Password:     qbitInst.Password,
		TrustedCerts: qbitInst.TrustedCerts,
	})
	if err != nil {
		s.appendQbitAggregatedHistory(ruleID, startedAt, events, nil,
			fmt.Sprintf("qbit client init: %v", err), "error")
		return
	}

	results := make([]qbitTagResult, 0, len(byTag))
	hadError := false
	hadSuccess := false
	for _, tag := range tagOrder {
		batch := byTag[tag]
		// Cap example list to keep history detail readable (full list
		// would be ugly for 50-torrent cross-seed bursts).
		examples := batch.names
		if len(examples) > 5 {
			examples = examples[:5]
		}
		err := client.AddTags(ctx, batch.hashes, []string{tag})
		if err != nil {
			hadError = true
			results = append(results, qbitTagResult{tag: tag, failed: true, errMsg: err.Error(), examples: examples})
			continue
		}
		hadSuccess = true
		results = append(results, qbitTagResult{tag: tag, applied: len(batch.hashes), examples: examples})
	}

	status := "ok"
	switch {
	case hadError && !hadSuccess:
		status = "error"
	case hadError:
		status = "partial"
	}

	s.appendQbitAggregatedHistory(ruleID, startedAt, events, results, "", status)
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
		var firstErr string
		parts := make([]string, 0, len(results))
		for _, r := range results {
			if r.failed {
				totalFailed++
				if firstErr == "" {
					firstErr = r.errMsg
				}
				parts = append(parts, fmt.Sprintf("%s: failed (%s)", r.tag, r.errMsg))
				continue
			}
			totalApplied += r.applied
			parts = append(parts, fmt.Sprintf("%s: %d", r.tag, r.applied))
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
		default:
			summary = fmt.Sprintf("qbitSeTag: error: tagging failed (%s)", breakdown)
		}
	}

	itemTitle := fmt.Sprintf("%d torrent(s) in window", len(events))
	if len(events) == 1 {
		itemTitle = events[0].Name
	}

	run := core.WebhookRuleRun{
		StartedAt:   startedAt,
		DurationMs:  durationMs,
		Status:      status,
		EventType:   "qbit:torrentAdded",
		ItemTitle:   itemTitle,
		ItemContext: fmt.Sprintf("aggregated %d", len(events)),
		Summary:     summary,
	}
	s.appendWebhookRuleRunsBatch([]pendingRuleRun{{ruleID: ruleID, run: run}})
}
