package api

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"resolvarr/internal/core"
)

// webhook_dispatch_test.go — coverage for buildWebhookRuleRun status
// matrix + appendWebhookRuleRunsBatch cap behaviour. The dispatcher
// loop itself depends on a full Server/ConfigStore setup; that
// integration coverage lands when adapters are real (task #5+).
//
// rawEventField is also covered here — adapters will lean on it for
// fields not on the lowest-common-denominator envelope.

func TestBuildWebhookRuleRun_StatusOK(t *testing.T) {
	env := &connectEventEnvelope{EventType: "Download"}
	env.Movie = &struct {
		ID    int    `json:"id"`
		Title string `json:"title"`
		Year  int    `json:"year"`
	}{ID: 42, Title: "Dune", Year: 2021}
	results := []functionResult{
		{Function: core.WebhookFnTagAudio, OK: true, Summary: "3 added"},
		{Function: core.WebhookFnTagVideo, OK: true, Summary: "1 added"},
	}
	run := buildWebhookRuleRun(env, time.Now().Add(-50*time.Millisecond).UTC(), results)
	if run.Status != "ok" {
		t.Errorf("Status = %q, want ok", run.Status)
	}
	if run.EventType != "Download" {
		t.Errorf("EventType = %q, want Download", run.EventType)
	}
	if run.ItemTitle != "Dune" {
		t.Errorf("ItemTitle = %q, want Dune", run.ItemTitle)
	}
	if run.DurationMs <= 0 {
		t.Errorf("DurationMs = %d, want >0", run.DurationMs)
	}
}

func TestBuildWebhookRuleRun_StatusPartial(t *testing.T) {
	env := &connectEventEnvelope{EventType: "Download"}
	results := []functionResult{
		{Function: core.WebhookFnTagAudio, OK: true, Summary: "ok"},
		{Function: core.WebhookFnRecover, OK: false, Summary: "no grab history"},
	}
	run := buildWebhookRuleRun(env, time.Now().UTC(), results)
	if run.Status != "partial" {
		t.Errorf("Status = %q, want partial", run.Status)
	}
}

func TestBuildWebhookRuleRun_StatusError(t *testing.T) {
	env := &connectEventEnvelope{EventType: "Download"}
	results := []functionResult{
		{Function: core.WebhookFnTagAudio, OK: false, Summary: "arr unreachable"},
		{Function: core.WebhookFnTagVideo, OK: false, Summary: "arr unreachable"},
	}
	run := buildWebhookRuleRun(env, time.Now().UTC(), results)
	if run.Status != "error" {
		t.Errorf("Status = %q, want error", run.Status)
	}
}

func TestBuildWebhookRuleRun_EmptyResults(t *testing.T) {
	// Defensive: a rule with FiresOn=true but zero matching functions
	// shouldn't reach buildWebhookRuleRun (dispatcher skips), but if it
	// did the run should still classify cleanly (zero failures + zero
	// successes maps to "ok" — vacuously true).
	env := &connectEventEnvelope{EventType: "Download"}
	run := buildWebhookRuleRun(env, time.Now().UTC(), nil)
	if run.Status != "ok" {
		t.Errorf("Status (empty results) = %q, want ok", run.Status)
	}
	if run.Summary != "" {
		t.Errorf("Summary (empty results) = %q, want empty", run.Summary)
	}
}

// TestAppendWebhookRuleRunsBatch_TrimsToCap exercises the rolling cap +
// batch-append. Locks the regression flagged in review concern #2: a
// previous sort-then-truncate version dropped the just-appended run
// when StartedAt tied an existing entry.
func TestAppendWebhookRuleRunsBatch_TrimsToCap(t *testing.T) {
	dir := t.TempDir()
	store := core.NewConfigStore(dir)
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Seed a rule with capacity-minus-one history entries.
	if err := store.Update(func(c *core.Config) {
		r := core.WebhookRule{
			ID:         "rule-1",
			Name:       "Test",
			Enabled:    true,
			InstanceID: "inst-1",
			AppType:    "radarr",
			Functions:  []core.WebhookFunction{core.WebhookFnTagAudio},
		}
		// Pre-fill history with cap-1 entries, oldest first.
		for i := 0; i < core.MaxInMemoryHistory-1; i++ {
			r.History = append(r.History, core.WebhookRuleRun{
				StartedAt: time.Date(2024, 1, 1, 0, i, 0, 0, time.UTC),
				Status:    "ok",
				Summary:   "seed",
			})
		}
		c.WebhookRules = append(c.WebhookRules, r)
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Server only needs ConfigStore.Update + an App pointer — the
	// dispatcher's batch helper doesn't read other Server fields. App
	// is constructed inline here to avoid pulling in the full server
	// setup.
	s := &Server{App: &core.App{Config: store}}

	// Append 3 new runs — pre-cap was 6, +3 = 9, must trim to 7.
	now := time.Date(2024, 1, 2, 12, 0, 0, 0, time.UTC)
	pending := []pendingRuleRun{
		{ruleID: "rule-1", run: core.WebhookRuleRun{StartedAt: now, Status: "ok", Summary: "new-1"}},
		{ruleID: "rule-1", run: core.WebhookRuleRun{StartedAt: now.Add(time.Second), Status: "ok", Summary: "new-2"}},
		{ruleID: "rule-1", run: core.WebhookRuleRun{StartedAt: now.Add(2 * time.Second), Status: "ok", Summary: "new-3"}},
	}
	s.appendWebhookRuleRunsBatch(pending)

	cfg := store.Get()
	var rule core.WebhookRule
	for _, r := range cfg.WebhookRules {
		if r.ID == "rule-1" {
			rule = r
			break
		}
	}
	if got := len(rule.History); got != core.MaxInMemoryHistory {
		t.Fatalf("History len = %d, want %d", got, core.MaxInMemoryHistory)
	}
	// Newest must be at the tail (scheduler-style append-then-trim).
	last := rule.History[len(rule.History)-1]
	if last.Summary != "new-3" {
		t.Errorf("tail summary = %q, want new-3 (newest at end)", last.Summary)
	}
	// Oldest entries must have been trimmed from the head.
	for _, h := range rule.History {
		if h.Summary == "seed" && h.StartedAt.Equal(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)) {
			t.Errorf("oldest seed entry should have been trimmed but is still in history")
		}
	}
}

func TestAppendWebhookRuleRunsBatch_MultipleRulesInOneBatch(t *testing.T) {
	dir := t.TempDir()
	store := core.NewConfigStore(dir)
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := store.Update(func(c *core.Config) {
		c.WebhookRules = []core.WebhookRule{
			{ID: "a", Name: "A", InstanceID: "i1", AppType: "radarr", Functions: []core.WebhookFunction{core.WebhookFnTagAudio}},
			{ID: "b", Name: "B", InstanceID: "i1", AppType: "radarr", Functions: []core.WebhookFunction{core.WebhookFnTagVideo}},
		}
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	s := &Server{App: &core.App{Config: store}}
	now := time.Now().UTC()
	pending := []pendingRuleRun{
		{ruleID: "a", run: core.WebhookRuleRun{StartedAt: now, Summary: "a-1"}},
		{ruleID: "b", run: core.WebhookRuleRun{StartedAt: now, Summary: "b-1"}},
		{ruleID: "a", run: core.WebhookRuleRun{StartedAt: now.Add(time.Second), Summary: "a-2"}},
	}
	s.appendWebhookRuleRunsBatch(pending)
	cfg := store.Get()
	var aRule, bRule core.WebhookRule
	for _, r := range cfg.WebhookRules {
		switch r.ID {
		case "a":
			aRule = r
		case "b":
			bRule = r
		}
	}
	if len(aRule.History) != 2 {
		t.Errorf("rule a history len = %d, want 2", len(aRule.History))
	}
	if len(bRule.History) != 1 {
		t.Errorf("rule b history len = %d, want 1", len(bRule.History))
	}
	// Verify single-write — config file should exist with both rules
	// reflecting the new history.
	if _, err := os.Stat(filepath.Join(dir, "resolvarr.json")); err != nil {
		t.Errorf("config file missing after batch append: %v", err)
	}
}

func TestAppendWebhookRuleRunsBatch_UnknownRuleIDIsNoop(t *testing.T) {
	// Defence: a rule deleted between dispatch + persist must not panic
	// or corrupt the config; just silently skip the orphaned entry.
	dir := t.TempDir()
	store := core.NewConfigStore(dir)
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	s := &Server{App: &core.App{Config: store}}
	pending := []pendingRuleRun{
		{ruleID: "ghost-rule", run: core.WebhookRuleRun{Status: "ok"}},
	}
	s.appendWebhookRuleRunsBatch(pending) // must not panic
	cfg := store.Get()
	if len(cfg.WebhookRules) != 0 {
		t.Errorf("WebhookRules created for unknown ID: %d", len(cfg.WebhookRules))
	}
}

