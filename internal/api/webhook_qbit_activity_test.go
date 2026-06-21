package api

import (
	"path/filepath"
	"testing"
	"time"

	"resolvarr/internal/core"
)

func newQbitActivityServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	store := core.NewConfigStore(dir)
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	return &Server{App: &core.App{Config: store}, WebhookLog: newWebhookLog(filepath.Join(dir, "events.json"))}
}

// TestLogQbitAddActivity_Outcomes covers the three per-rule outcome shapes
// (tagged / skipped-no-tag / error) and that the entry is keyed by the qBit
// instance ID so the qBit-webhook view can read it back.
func TestLogQbitAddActivity_Outcomes(t *testing.T) {
	s := newQbitActivityServer(t)
	now := time.Now().UTC()
	evs := []qbitAddEvent{
		{RuleID: "r1", RuleName: "tag rule", Matched: true, AppliedTag: "Episode", Reason: "single video file, so a single episode"},
		{RuleID: "r2", RuleName: "skip rule", Matched: false, Reason: "category matches a Radarr movie category"},
		{RuleID: "r3", RuleName: "err rule", ApplyErrMsg: "renameTorrent HTTP 401"},
	}
	s.logQbitAddActivity("qbit-1", "ev-1", now, "abc123", "Some.Show.S01E01", "tv", evs, false)

	got := s.WebhookLog.list("qbit-1")
	if len(got) != 1 {
		t.Fatalf("events for qbit-1 = %d, want 1", len(got))
	}
	e := got[0]
	if e.EventType != "qBit add" || e.Title != "Some.Show.S01E01" || e.Subtitle != "tv" {
		t.Errorf("event header wrong: type=%q title=%q sub=%q", e.EventType, e.Title, e.Subtitle)
	}
	if len(e.Outcomes) != 3 {
		t.Fatalf("outcomes = %d, want 3", len(e.Outcomes))
	}
	if e.Outcomes[0].Status != "ok" || !e.Outcomes[0].Changed {
		t.Errorf("tagged outcome wrong: %+v", e.Outcomes[0])
	}
	if e.Outcomes[1].Status != "ok" || e.Outcomes[1].Changed {
		t.Errorf("skip outcome should be ok+unchanged: %+v", e.Outcomes[1])
	}
	if e.Outcomes[2].Status != "error" || e.Outcomes[2].Summary != "renameTorrent HTTP 401" {
		t.Errorf("error outcome wrong: %+v", e.Outcomes[2])
	}
}

// TestLogQbitAddActivity_NoRuleMatched logs an entry with empty outcomes so
// a delivered-but-ignored add is still visible.
func TestLogQbitAddActivity_NoRuleMatched(t *testing.T) {
	s := newQbitActivityServer(t)
	s.logQbitAddActivity("qbit-1", "ev-2", time.Now().UTC(), "h", "Movie.2021", "", nil, false)
	got := s.WebhookLog.list("qbit-1")
	if len(got) != 1 || len(got[0].Outcomes) != 0 {
		t.Fatalf("want 1 event with 0 outcomes, got %d events", len(got))
	}
	if got[0].Subtitle != "(no category)" {
		t.Errorf("empty category should render as (no category), got %q", got[0].Subtitle)
	}
	// Raw must carry the payload for re-run.
	if got[0].Raw == nil {
		t.Error("Raw payload missing (needed for re-run)")
	}
}
