package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"resolvarr/internal/core"
)

// newQbitEventTestServer wires a Server backed by a real ConfigStore +
// a single qBit instance (id=qbit-1, secret=test-secret). Returns the
// server, store, and the instance ID + secret so tests don't have to
// re-derive them.
func newQbitEventTestServer(t *testing.T) (*Server, *core.ConfigStore, string, string) {
	t.Helper()
	dir := t.TempDir()
	store := core.NewConfigStore(dir)
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	const (
		instID = "qbit-1"
		secret = "test-secret-do-not-use-in-prod"
	)
	if err := store.Update(func(c *core.Config) {
		c.QbitInstances = append(c.QbitInstances, core.QbitInstance{
			ID:            instID,
			Name:          "test-qbit",
			URL:           "http://qbit:8080",
			WebhookSecret: secret,
		})
	}); err != nil {
		t.Fatalf("seed instance: %v", err)
	}
	return &Server{App: &core.App{Config: store}}, store, instID, secret
}

// TestHandleQbitTorrentAdded_AuthRequired — missing X-API-Key gets
// 401, not 400.
func TestHandleQbitTorrentAdded_AuthRequired(t *testing.T) {
	s, _, instID, _ := newQbitEventTestServer(t)
	body := strings.NewReader("infoHash=abc&name=test")
	req := httptest.NewRequest(http.MethodPost, "/api/qbit/torrent-added/"+instID, body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("instanceId", instID)
	rr := httptest.NewRecorder()
	s.handleQbitTorrentAdded(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

// TestHandleQbitTorrentAdded_AuthMismatch — wrong key gets 401.
func TestHandleQbitTorrentAdded_AuthMismatch(t *testing.T) {
	s, _, instID, _ := newQbitEventTestServer(t)
	body := strings.NewReader("infoHash=abc&name=test")
	req := httptest.NewRequest(http.MethodPost, "/api/qbit/torrent-added/"+instID, body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-API-Key", "wrong-key")
	req.SetPathValue("instanceId", instID)
	rr := httptest.NewRecorder()
	s.handleQbitTorrentAdded(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

// TestHandleQbitTorrentAdded_UnknownInstanceLooksLikeAuthFailure —
// timing-side-channel guard. Unknown instance returns 401 (same as
// wrong key) so a probe can't enumerate valid instance IDs.
func TestHandleQbitTorrentAdded_UnknownInstanceLooksLikeAuthFailure(t *testing.T) {
	s, _, _, secret := newQbitEventTestServer(t)
	body := strings.NewReader("infoHash=abc&name=test")
	req := httptest.NewRequest(http.MethodPost, "/api/qbit/torrent-added/does-not-exist", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-API-Key", secret)
	req.SetPathValue("instanceId", "does-not-exist")
	rr := httptest.NewRecorder()
	s.handleQbitTorrentAdded(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 (unknown instance must look like auth failure)", rr.Code)
	}
}

// TestHandleQbitTorrentAdded_MissingInfoHash — required field check.
func TestHandleQbitTorrentAdded_MissingInfoHash(t *testing.T) {
	s, _, instID, secret := newQbitEventTestServer(t)
	body := strings.NewReader("name=test")
	req := httptest.NewRequest(http.MethodPost, "/api/qbit/torrent-added/"+instID, body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-API-Key", secret)
	req.SetPathValue("instanceId", instID)
	rr := httptest.NewRecorder()
	s.handleQbitTorrentAdded(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "infoHash") {
		t.Errorf("error body = %q, should mention infoHash", rr.Body.String())
	}
}

// TestHandleQbitTorrentAdded_MissingName — required field check.
func TestHandleQbitTorrentAdded_MissingName(t *testing.T) {
	s, _, instID, secret := newQbitEventTestServer(t)
	body := strings.NewReader("infoHash=abc")
	req := httptest.NewRequest(http.MethodPost, "/api/qbit/torrent-added/"+instID, body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-API-Key", secret)
	req.SetPathValue("instanceId", instID)
	rr := httptest.NewRecorder()
	s.handleQbitTorrentAdded(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

// TestHandleQbitTorrentAdded_NoMatchingRules_Still202 — instance has
// no rules with qbitSeTag pointing at it. Handler returns 202 with
// queued=0 — qBit can't act on an error response, and a valid setup
// where rules haven't been wired yet shouldn't surface as a hook
// failure on every fire.
func TestHandleQbitTorrentAdded_NoMatchingRules_Still202(t *testing.T) {
	s, _, instID, secret := newQbitEventTestServer(t)
	body := strings.NewReader("infoHash=abc&name=Show.S01E01.WEB-DL")
	req := httptest.NewRequest(http.MethodPost, "/api/qbit/torrent-added/"+instID, body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-API-Key", secret)
	req.SetPathValue("instanceId", instID)
	rr := httptest.NewRecorder()
	s.handleQbitTorrentAdded(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Errorf("status = %d, want 202", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"queued":0`) {
		t.Errorf("body = %q, want queued:0", rr.Body.String())
	}
}

// TestHandleQbitTorrentAdded_EnqueuesPerMatchingRule — two qbitSeTag
// rules pointing at the same qBit instance both get the event.
func TestHandleQbitTorrentAdded_EnqueuesPerMatchingRule(t *testing.T) {
	s, store, instID, secret := newQbitEventTestServer(t)

	// Seed two rules pointing at instID + a third pointing elsewhere.
	if err := store.Update(func(c *core.Config) {
		c.WebhookRules = append(c.WebhookRules,
			core.WebhookRule{
				ID: "rule-1", Name: "r1", Enabled: true,
				Functions: []core.WebhookFunction{core.WebhookFnQbitSeTag},
				QbitSe:    &core.QbitSeRules{QbitInstanceID: instID, EpisodeEnabled: true, EpisodeTag: "Episode", AggregationWindowSeconds: 60},
			},
			core.WebhookRule{
				ID: "rule-2", Name: "r2", Enabled: true,
				Functions: []core.WebhookFunction{core.WebhookFnQbitSeTag},
				QbitSe:    &core.QbitSeRules{QbitInstanceID: instID, EpisodeEnabled: true, EpisodeTag: "Ep", AggregationWindowSeconds: 60},
			},
			core.WebhookRule{
				ID: "rule-other", Name: "other-instance", Enabled: true,
				Functions: []core.WebhookFunction{core.WebhookFnQbitSeTag},
				QbitSe:    &core.QbitSeRules{QbitInstanceID: "different-instance", EpisodeEnabled: true},
			},
		)
	}); err != nil {
		t.Fatalf("seed rules: %v", err)
	}

	body := strings.NewReader("infoHash=abc&name=Show.S01E01.WEB-DL")
	req := httptest.NewRequest(http.MethodPost, "/api/qbit/torrent-added/"+instID, body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-API-Key", secret)
	req.SetPathValue("instanceId", instID)
	rr := httptest.NewRecorder()
	s.handleQbitTorrentAdded(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"queued":2`) {
		t.Errorf("body = %q, want queued:2 (rule-1 + rule-2 only, rule-other points elsewhere)", rr.Body.String())
	}
	if pc := s.QbitEventBuffer().PendingCount(); pc != 2 {
		t.Errorf("PendingCount = %d, want 2", pc)
	}
}

// TestHandleQbitTorrentAdded_DisabledRuleSkipped — disabled rules
// don't get enqueued even if they match instance + function.
func TestHandleQbitTorrentAdded_DisabledRuleSkipped(t *testing.T) {
	s, store, instID, secret := newQbitEventTestServer(t)
	if err := store.Update(func(c *core.Config) {
		c.WebhookRules = append(c.WebhookRules, core.WebhookRule{
			ID: "rule-disabled", Name: "off", Enabled: false,
			Functions: []core.WebhookFunction{core.WebhookFnQbitSeTag},
			QbitSe:    &core.QbitSeRules{QbitInstanceID: instID, EpisodeEnabled: true},
		})
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	body := strings.NewReader("infoHash=abc&name=Show.S01E01")
	req := httptest.NewRequest(http.MethodPost, "/api/qbit/torrent-added/"+instID, body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-API-Key", secret)
	req.SetPathValue("instanceId", instID)
	rr := httptest.NewRecorder()
	s.handleQbitTorrentAdded(rr, req)

	if !strings.Contains(rr.Body.String(), `"queued":0`) {
		t.Errorf("body = %q, want queued:0 (disabled rule must be skipped)", rr.Body.String())
	}
}

// TestMatchQbitSeRulesForInstance_FunctionFlagRequired — rule pointing
// at the instance but WITHOUT WebhookFnQbitSeTag in Functions doesn't
// match.
func TestMatchQbitSeRulesForInstance_FunctionFlagRequired(t *testing.T) {
	cfg := core.Config{
		WebhookRules: []core.WebhookRule{
			{
				ID: "rule-no-fn", Enabled: true,
				Functions: []core.WebhookFunction{core.WebhookFnTagAudio}, // wrong function
				QbitSe:    &core.QbitSeRules{QbitInstanceID: "i1"},
			},
		},
	}
	if got := matchQbitSeRulesForInstance(cfg, "i1"); len(got) != 0 {
		t.Errorf("matched %d rules, want 0 (function flag missing)", len(got))
	}
}

// TestFlushQbitAggregated_RuleGoneSkipsHistory — config edit between
// enqueue and flush deletes the rule. Flush must NOT write a history
// entry (would attach to a non-existent rule).
func TestFlushQbitAggregated_RuleGoneSkipsHistory(t *testing.T) {
	s, _, _, _ := newQbitEventTestServer(t)
	// No rules in config → flushQbitAggregated should silently drop.
	s.flushQbitAggregated(context.Background(), "deleted-rule-id", []qbitAddEvent{
		{InfoHash: "h", Name: "Show.S01E01"},
	})
	// No assertions on output beyond "didn't panic" — the rule isn't
	// in config so there's no history to inspect. The skip-with-stderr-
	// log is the documented behaviour.
}

// TestFlushQbitAggregated_NoMatchEmitsHistoryEntry — events that
// classify to no tag (every Episode/Season/Unmatched off, or no
// pattern hits) still produce a history entry so the user sees the
// rule fired but did nothing.
func TestFlushQbitAggregated_NoMatchEmitsHistoryEntry(t *testing.T) {
	s, store, instID, _ := newQbitEventTestServer(t)
	if err := store.Update(func(c *core.Config) {
		c.WebhookRules = append(c.WebhookRules, core.WebhookRule{
			ID: "rule-1", Name: "r1", Enabled: true,
			Functions: []core.WebhookFunction{core.WebhookFnQbitSeTag},
			// All three rule-branches disabled → DetermineQbitTag returns ""
			QbitSe: &core.QbitSeRules{
				QbitInstanceID: instID,
			},
		})
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	s.flushQbitAggregated(context.Background(), "rule-1", []qbitAddEvent{
		{InfoHash: "h1", Name: "Show.S01E01"},
		{InfoHash: "h2", Name: "Show.S02"},
	})

	cfg := store.Get()
	if len(cfg.WebhookRules[0].History) != 1 {
		t.Fatalf("history len = %d, want 1", len(cfg.WebhookRules[0].History))
	}
	entry := cfg.WebhookRules[0].History[0]
	if entry.EventType != "qbit:torrentAdded" {
		t.Errorf("EventType = %q, want qbit:torrentAdded", entry.EventType)
	}
	if entry.Status != "ok" {
		t.Errorf("Status = %q, want ok (no-match is not an error)", entry.Status)
	}
	if !strings.Contains(entry.Summary, "no rule matched") {
		t.Errorf("Summary = %q, should mention no rule matched", entry.Summary)
	}
}

// TestEndToEnd_HookFiresHistoryAppears — full path from HTTP POST
// through buffer (window=1s) to flush callback to history append.
// Uses a real qBit-instance config but the flush will fail on the
// AddTags HTTP call (no real qBit running). Verifies the failure
// gets surfaced as an error-status history entry.
func TestEndToEnd_HookFiresHistoryAppears(t *testing.T) {
	s, store, instID, secret := newQbitEventTestServer(t)
	// Point qBit URL at a nonexistent server so AddTags fails fast.
	if err := store.Update(func(c *core.Config) {
		// 127.0.0.1:1 is reserved — connection-refused immediately,
		// avoids waiting on full qBit timeout.
		c.QbitInstances[0].URL = "http://127.0.0.1:1"
		c.WebhookRules = append(c.WebhookRules, core.WebhookRule{
			ID: "rule-e2e", Name: "e2e", Enabled: true,
			Functions: []core.WebhookFunction{core.WebhookFnQbitSeTag},
			QbitSe: &core.QbitSeRules{
				QbitInstanceID: instID, EpisodeEnabled: true, EpisodeTag: "Episode",
				AggregationWindowSeconds: 1,
			},
		})
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	body := strings.NewReader("infoHash=hashE2E&name=Show.S01E05.WEB-DL")
	req := httptest.NewRequest(http.MethodPost, "/api/qbit/torrent-added/"+instID, body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-API-Key", secret)
	req.SetPathValue("instanceId", instID)
	rr := httptest.NewRecorder()
	s.handleQbitTorrentAdded(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("handler status = %d, want 202", rr.Code)
	}

	// Wait for the 1s window + flush.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(store.Get().WebhookRules[0].History) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	hist := store.Get().WebhookRules[0].History
	if len(hist) != 1 {
		t.Fatalf("history len = %d, want 1 after buffer flush", len(hist))
	}
	entry := hist[0]
	if entry.EventType != "qbit:torrentAdded" {
		t.Errorf("EventType = %q, want qbit:torrentAdded", entry.EventType)
	}
	// Status should be "error" because AddTags failed on the unreachable qBit.
	if entry.Status != "error" {
		t.Errorf("Status = %q, want error (qBit unreachable)", entry.Status)
	}
	if !strings.Contains(entry.Summary, "qbitSeTag") {
		t.Errorf("Summary = %q, should be prefixed qbitSeTag", entry.Summary)
	}
}

// TestEndToEnd_BurstAggregation — three rapid HTTP POSTs end up as
// ONE history entry with aggregated count=3. Validates the buffer's
// burst-coalescing in the full pipeline.
func TestEndToEnd_BurstAggregation(t *testing.T) {
	s, store, instID, secret := newQbitEventTestServer(t)
	if err := store.Update(func(c *core.Config) {
		c.QbitInstances[0].URL = "http://127.0.0.1:1"
		c.WebhookRules = append(c.WebhookRules, core.WebhookRule{
			ID: "rule-burst", Enabled: true,
			Functions: []core.WebhookFunction{core.WebhookFnQbitSeTag},
			QbitSe: &core.QbitSeRules{
				QbitInstanceID: instID, EpisodeEnabled: true, EpisodeTag: "Episode",
				AggregationWindowSeconds: 1,
			},
		})
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	for i := 0; i < 3; i++ {
		body := strings.NewReader("infoHash=h" + string(rune('0'+i)) + "&name=Show.S01E0" + string(rune('1'+i)))
		req := httptest.NewRequest(http.MethodPost, "/api/qbit/torrent-added/"+instID, body)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-API-Key", secret)
		req.SetPathValue("instanceId", instID)
		rr := httptest.NewRecorder()
		s.handleQbitTorrentAdded(rr, req)
		if rr.Code != http.StatusAccepted {
			t.Fatalf("post %d status = %d, want 202", i, rr.Code)
		}
	}

	// Wait for the single window to flush.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(store.Get().WebhookRules[0].History) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	hist := store.Get().WebhookRules[0].History
	if len(hist) != 1 {
		t.Fatalf("history len = %d, want 1 (three events should aggregate)", len(hist))
	}
	if !strings.Contains(hist[0].ItemContext, "aggregated 3") {
		t.Errorf("ItemContext = %q, should mention aggregated 3", hist[0].ItemContext)
	}
}

// TestEagerApplyQbitSeTag_Classifies — eager-apply helper sets
// Matched + AppliedTag when the classifier produces a tag, leaves
// them zero when the rule's branches don't match the name. qBit
// unreachable populates ApplyErrMsg but still marks Matched.
func TestEagerApplyQbitSeTag_Classifies(t *testing.T) {
	s, _, instID, _ := newQbitEventTestServer(t)
	cfg := s.App.Config.Get()
	qi := findQbitInstanceByID(cfg, instID)
	if qi == nil {
		t.Fatal("instance not found in seeded config")
	}
	// Unreachable URL so AddTags fails — verifies the error-recording
	// path. Classification still works (it's a pure function).
	qi.URL = "http://127.0.0.1:1"

	rule := &core.WebhookRule{
		ID: "r", Enabled: true,
		Functions: []core.WebhookFunction{core.WebhookFnQbitSeTag},
		QbitSe: &core.QbitSeRules{
			QbitInstanceID: instID,
			SeasonEnabled:  true, SeasonTag: "Season",
		},
	}

	ev := qbitAddEvent{InfoHash: "abc", Name: "Charmed.S07.WEB-DL"}
	s.eagerApplyQbitSeTag(context.Background(), qi, rule, &ev)
	if !ev.Matched {
		t.Errorf("Matched = false, want true for S07 with SeasonEnabled")
	}
	if ev.AppliedTag != "Season" {
		t.Errorf("AppliedTag = %q, want Season", ev.AppliedTag)
	}
	if ev.ApplyErrMsg == "" {
		t.Errorf("ApplyErrMsg should be populated when qBit unreachable, got empty")
	}
}

// TestEagerApplyQbitSeTag_NoBranchMatch — when DetermineQbitTag
// returns "" (no Episode/Season/Unmatched branch enabled or no
// pattern hit), the event stays Matched=false and AppliedTag empty,
// AND we don't attempt AddTags (no ApplyErrMsg even if qBit
// unreachable).
func TestEagerApplyQbitSeTag_NoBranchMatch(t *testing.T) {
	s, _, instID, _ := newQbitEventTestServer(t)
	cfg := s.App.Config.Get()
	qi := findQbitInstanceByID(cfg, instID)
	qi.URL = "http://127.0.0.1:1" // would fail if we tried

	rule := &core.WebhookRule{
		ID: "r", Enabled: true,
		Functions: []core.WebhookFunction{core.WebhookFnQbitSeTag},
		QbitSe: &core.QbitSeRules{
			QbitInstanceID: instID,
			// All branches disabled — classifier returns ""
		},
	}

	ev := qbitAddEvent{InfoHash: "abc", Name: "Charmed.S07.WEB-DL"}
	s.eagerApplyQbitSeTag(context.Background(), qi, rule, &ev)
	if ev.Matched {
		t.Errorf("Matched = true, want false when no branch enabled")
	}
	if ev.AppliedTag != "" {
		t.Errorf("AppliedTag = %q, want empty", ev.AppliedTag)
	}
	if ev.ApplyErrMsg != "" {
		t.Errorf("ApplyErrMsg = %q, want empty when no apply attempt", ev.ApplyErrMsg)
	}
}

// TestFlushQbitAggregated_PreAppliedSuccess — events with successful
// eager-apply (no ApplyErrMsg) produce a clean "tagged N" summary
// without re-classifying or making qBit calls. The qBit instance URL
// is unreachable to prove flush doesn't reach qBit anymore.
func TestFlushQbitAggregated_PreAppliedSuccess(t *testing.T) {
	s, store, instID, _ := newQbitEventTestServer(t)
	if err := store.Update(func(c *core.Config) {
		c.QbitInstances[0].URL = "http://127.0.0.1:1" // unreachable; flush should not care
		c.WebhookRules = append(c.WebhookRules, core.WebhookRule{
			ID: "rule-pre", Enabled: true,
			Functions: []core.WebhookFunction{core.WebhookFnQbitSeTag},
			QbitSe: &core.QbitSeRules{
				QbitInstanceID: instID,
				SeasonEnabled:  true, SeasonTag: "Season",
			},
		})
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	s.flushQbitAggregated(context.Background(), "rule-pre", []qbitAddEvent{
		{InfoHash: "h1", Name: "Show.S01", Matched: true, AppliedTag: "Season"},
		{InfoHash: "h2", Name: "Show.S02", Matched: true, AppliedTag: "Season"},
	})

	hist := store.Get().WebhookRules[0].History
	if len(hist) != 1 {
		t.Fatalf("history len = %d, want 1", len(hist))
	}
	if hist[0].Status != "ok" {
		t.Errorf("Status = %q, want ok", hist[0].Status)
	}
	if !strings.Contains(hist[0].Summary, "Season: 2") {
		t.Errorf("Summary = %q, should mention 'Season: 2'", hist[0].Summary)
	}
}

// TestFlushQbitAggregated_MultipleTagsBatchedSeparately — Episode +
// Season events in the same window produce ONE history entry whose
// breakdown shows both buckets. Post-eager-apply refactor: the events
// carry pre-applied results (Matched + AppliedTag + optionally
// ApplyErrMsg) and the flush rolls them up into per-tag aggregate
// rows. This test validates the byTag iteration that 20+ other tests
// don't reach.
func TestFlushQbitAggregated_MultipleTagsBatchedSeparately(t *testing.T) {
	s, store, instID, _ := newQbitEventTestServer(t)
	if err := store.Update(func(c *core.Config) {
		c.WebhookRules = append(c.WebhookRules, core.WebhookRule{
			ID: "rule-multi", Enabled: true,
			Functions: []core.WebhookFunction{core.WebhookFnQbitSeTag},
			QbitSe: &core.QbitSeRules{
				QbitInstanceID: instID,
				EpisodeEnabled: true, EpisodeTag: "Episode",
				SeasonEnabled: true, SeasonTag: "Season",
				UnmatchedEnabled: true, UnmatchedTag: "Unmatched",
			},
		})
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Two episodes + one season in the same window — eager-apply
	// already populated AppliedTag. Simulating an unreachable qBit
	// at receive time by setting ApplyErrMsg on every event; the
	// flush should aggregate per-tag failure rows + emit error
	// status because no event succeeded.
	s.flushQbitAggregated(context.Background(), "rule-multi", []qbitAddEvent{
		{InfoHash: "h1", Name: "Show.S01E05.WEB-DL", Matched: true, AppliedTag: "Episode", ApplyErrMsg: "qbit unreachable"},
		{InfoHash: "h2", Name: "Show.S01E06.WEB-DL", Matched: true, AppliedTag: "Episode", ApplyErrMsg: "qbit unreachable"},
		{InfoHash: "h3", Name: "Show.S03.Complete", Matched: true, AppliedTag: "Season", ApplyErrMsg: "qbit unreachable"},
	})

	hist := store.Get().WebhookRules[0].History
	if len(hist) != 1 {
		t.Fatalf("history len = %d, want 1", len(hist))
	}
	summary := hist[0].Summary
	if !strings.Contains(summary, "Episode") || !strings.Contains(summary, "Season") {
		t.Errorf("Summary = %q, should mention both Episode + Season groups", summary)
	}
	if hist[0].Status != "error" {
		t.Errorf("Status = %q, want error (all attempts failed)", hist[0].Status)
	}
}
