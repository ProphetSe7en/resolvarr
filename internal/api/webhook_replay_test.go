package api

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"resolvarr/internal/core"
)

// newReplayTestServer builds a Server with one Radarr instance, one
// enabled grab-rename rule on it, and a webhook log seeded with a Grab
// event for that instance. Returns the server + the seeded event ID.
func newReplayTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	store := core.NewConfigStore(dir)
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := store.Update(func(c *core.Config) {
		c.Instances = []core.Instance{{ID: "i1", Name: "Radarr HD", Type: "radarr"}}
		c.WebhookRules = []core.WebhookRule{{
			ID: "r1", Name: "Rename rule", InstanceID: "i1", AppType: "radarr", Enabled: true,
			Functions:  []core.WebhookFunction{core.WebhookFnGrabRename},
			GrabRename: &core.GrabRenameCriteria{QbitInstanceID: "q1", TriggerAlways: true},
		}}
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	s := &Server{App: &core.App{Config: store}, WebhookLog: newWebhookLog(filepath.Join(dir, "events.json"))}

	id := "ev-1"
	s.WebhookLog.append(WebhookEvent{
		ID:         id,
		InstanceID: "i1",
		ReceivedAt: time.Date(2026, 6, 20, 1, 30, 0, 0, time.UTC),
		EventType:  "Grab",
		Title:      "Some Movie",
		Raw:        json.RawMessage(`{"eventType":"Grab","movie":{"id":1,"title":"Some Movie","year":2020}}`),
		Outcomes:   []WebhookEventOutcome{{RuleID: "r1", RuleName: "Rename rule", Status: "error", Summary: "qbit rename: HTTP 401"}},
	})
	return s, id
}

func TestMatchingReplayRules_GrabFiresRenameRule(t *testing.T) {
	s, _ := newReplayTestServer(t)
	cfg := s.App.Config.Get()
	var inst *core.Instance
	for i := range cfg.Instances {
		inst = &cfg.Instances[i]
	}
	rules := matchingReplayRules(cfg, inst, core.WebhookEventGrab)
	if len(rules) != 1 {
		t.Fatalf("matching rules = %d, want 1", len(rules))
	}
	if rules[0].RuleID != "r1" || len(rules[0].Functions) != 1 || rules[0].Functions[0] != string(core.WebhookFnGrabRename) {
		t.Errorf("unexpected rule preview: %+v", rules[0])
	}
}

func TestMatchingReplayRules_ExcludesPerRuleURLAndDisabled(t *testing.T) {
	s, _ := newReplayTestServer(t)
	// Disable the rule → no match.
	if err := s.App.Config.Update(func(c *core.Config) { c.WebhookRules[0].Enabled = false }); err != nil {
		t.Fatalf("update: %v", err)
	}
	cfg := s.App.Config.Get()
	inst := &cfg.Instances[0]
	if got := matchingReplayRules(cfg, inst, core.WebhookEventGrab); len(got) != 0 {
		t.Errorf("disabled rule should not match, got %d", len(got))
	}
}

func TestResolveReplay_NonReplayablePayload(t *testing.T) {
	s, _ := newReplayTestServer(t)
	// Synthetic null-payload entry (e.g. an auth-rejected notice).
	s.WebhookLog.append(WebhookEvent{ID: "ev-null", InstanceID: "i1", EventType: "(rejected)", Raw: json.RawMessage(`null`)})
	_, _, _, _, apiErr := s.resolveReplay("ev-null")
	if apiErr == nil || apiErr.Status != 400 {
		t.Fatalf("expected 400 for null payload, got %v", apiErr)
	}
}

func TestResolveReplay_UnknownID(t *testing.T) {
	s, _ := newReplayTestServer(t)
	_, _, _, _, apiErr := s.resolveReplay("does-not-exist")
	if apiErr == nil || apiErr.Status != 404 {
		t.Fatalf("expected 404 for unknown id, got %v", apiErr)
	}
}

func TestResolveReplay_HappyPath(t *testing.T) {
	s, id := newReplayTestServer(t)
	ev, inst, env, _, apiErr := s.resolveReplay(id)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if ev.ID != id || inst.ID != "i1" || env.EventType != "Grab" {
		t.Errorf("resolveReplay returned wrong data: ev=%s inst=%s evt=%s", ev.ID, inst.ID, env.EventType)
	}
}
