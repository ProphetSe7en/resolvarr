package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"resolvarr/internal/core"
)

// Test fixtures — all synthetic. The Arr + Plex instances are seeded
// directly into the store; no network traffic.

func seedPlexLabelRuleFixture(t *testing.T, store *core.ConfigStore) {
	t.Helper()
	if err := store.Update(func(c *core.Config) {
		c.Instances = []core.Instance{
			{ID: "arr-r", Name: "Radarr Main", URL: "http://radarr.lan:7878", APIKey: "synth-arr-api-key", Type: "radarr"},
			{ID: "arr-s", Name: "Sonarr Main", URL: "http://sonarr.lan:8989", APIKey: "synth-arr-api-key", Type: "sonarr"},
		}
		c.PlexInstances = []core.PlexInstance{
			{
				ID: "plex-1", Name: "Main Plex", URL: "http://plex.lan:32400", Token: syntheticPlexToken,
				Libraries: []core.PlexLibrary{
					{Key: "1", Title: "Movies", Type: "movie"},
					{Key: "2", Title: "Movies 4K", Type: "movie"},
					{Key: "3", Title: "TV Shows", Type: "show"},
				},
			},
		}
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

// TestPlexLabelRule_CreateHappyPath_RadarrMovieLibrary verifies the
// minimum-viable create flow: Radarr instance + movie library + one
// label whitelist + apply mode. AppType is denormalised server-side.
func TestPlexLabelRule_CreateHappyPath_RadarrMovieLibrary(t *testing.T) {
	s, store := newTestServerWithPlex(t)
	seedPlexLabelRuleFixture(t, store)

	req := core.PlexLabelRule{
		Name:       "Sync 4K to Plex",
		Enabled:    true,
		InstanceID: "arr-r",
		Labels:     []string{"4k", "hdr"},
		Targets: []core.PlexLabelTarget{
			{PlexInstanceID: "plex-1", LibraryKeys: []string{"1", "2"}},
		},
		RunMode: "apply",
	}
	body, _ := json.Marshal(req)
	rr := httptest.NewRecorder()
	s.handleCreatePlexLabelRule(rr, httptest.NewRequest(http.MethodPost, "/api/plex-label-rules", bytes.NewReader(body)))
	if rr.Code != http.StatusOK {
		t.Fatalf("Create status %d, body %s", rr.Code, rr.Body.String())
	}
	var got core.PlexLabelRule
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID == "" {
		t.Errorf("server should assign an ID")
	}
	if got.AppType != "radarr" {
		t.Errorf("AppType should be denormalised from instance, got %q", got.AppType)
	}
	if len(got.Labels) != 2 {
		t.Errorf("labels lost: %+v", got.Labels)
	}
	if got.RunMode != "apply" {
		t.Errorf("RunMode lost: %q", got.RunMode)
	}

	// Verify persistence.
	stored := store.Get().PlexLabelRules
	if len(stored) != 1 || stored[0].ID != got.ID {
		t.Errorf("rule not persisted: %+v", stored)
	}
}

// TestPlexLabelRule_RejectsRadarrPointingAtShowLibrary locks the
// server-side type filter — Radarr rule pointing at a "show" Plex
// library is rejected at save time so the user can't shoot their feet
// from a custom UI / curl / Postman call.
func TestPlexLabelRule_RejectsRadarrPointingAtShowLibrary(t *testing.T) {
	s, store := newTestServerWithPlex(t)
	seedPlexLabelRuleFixture(t, store)
	req := core.PlexLabelRule{
		Name:       "Bad rule",
		Enabled:    true,
		InstanceID: "arr-r", // Radarr
		Labels:     []string{"4k"},
		Targets: []core.PlexLabelTarget{
			{PlexInstanceID: "plex-1", LibraryKeys: []string{"3"}}, // TV Shows library — wrong type
		},
	}
	body, _ := json.Marshal(req)
	rr := httptest.NewRecorder()
	s.handleCreatePlexLabelRule(rr, httptest.NewRequest(http.MethodPost, "/api/plex-label-rules", bytes.NewReader(body)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for Radarr→show-library; got %d body %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "movie") {
		t.Errorf("error message should explain type-mismatch: %s", rr.Body.String())
	}
}

// TestPlexLabelRule_RequiresAtLeastOneLabel locks the validator's "no
// empty whitelist" rule. Empty whitelist = no-op rule = config bug,
// not a valid state.
func TestPlexLabelRule_RequiresAtLeastOneLabel(t *testing.T) {
	s, store := newTestServerWithPlex(t)
	seedPlexLabelRuleFixture(t, store)
	req := core.PlexLabelRule{
		Name:       "No labels",
		Enabled:    true,
		InstanceID: "arr-r",
		Labels:     []string{},
		Targets: []core.PlexLabelTarget{
			{PlexInstanceID: "plex-1", LibraryKeys: []string{"1"}},
		},
	}
	body, _ := json.Marshal(req)
	rr := httptest.NewRecorder()
	s.handleCreatePlexLabelRule(rr, httptest.NewRequest(http.MethodPost, "/api/plex-label-rules", bytes.NewReader(body)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty labels; got %d", rr.Code)
	}
}

// TestPlexLabelRule_RejectsUncachedLibraryKey locks that the library
// picker can only reference libraries the user has fetched from Plex.
// Catches client-side tampering + stale UI state.
func TestPlexLabelRule_RejectsUncachedLibraryKey(t *testing.T) {
	s, store := newTestServerWithPlex(t)
	seedPlexLabelRuleFixture(t, store)
	req := core.PlexLabelRule{
		Name:       "Unknown lib",
		Enabled:    true,
		InstanceID: "arr-r",
		Labels:     []string{"4k"},
		Targets: []core.PlexLabelTarget{
			{PlexInstanceID: "plex-1", LibraryKeys: []string{"99999"}}, // not in cache
		},
	}
	body, _ := json.Marshal(req)
	rr := httptest.NewRecorder()
	s.handleCreatePlexLabelRule(rr, httptest.NewRequest(http.MethodPost, "/api/plex-label-rules", bytes.NewReader(body)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for uncached library; got %d body %s", rr.Code, rr.Body.String())
	}
}

// TestPlexLabelRule_UpdatePreservesHistory locks the server-owned
// history rule. A PUT replaces labels/targets/runMode but History
// must survive — the client never owns the history slice.
func TestPlexLabelRule_UpdatePreservesHistory(t *testing.T) {
	s, store := newTestServerWithPlex(t)
	seedPlexLabelRuleFixture(t, store)
	if err := store.Update(func(c *core.Config) {
		c.PlexLabelRules = []core.PlexLabelRule{
			{
				ID: "rule-1", Name: "r", Enabled: true, InstanceID: "arr-r", AppType: "radarr",
				Labels:  []string{"4k"},
				Targets: []core.PlexLabelTarget{{PlexInstanceID: "plex-1", LibraryKeys: []string{"1"}}},
				RunMode: "apply",
				History: []core.PlexLabelRuleRun{
					{Trigger: "scheduled", Status: "ok", Summary: "synced 12"},
				},
			},
		}
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	updated := core.PlexLabelRule{
		ID: "rule-1", Name: "renamed", Enabled: true, InstanceID: "arr-r",
		Labels:  []string{"4k", "hdr"},
		Targets: []core.PlexLabelTarget{{PlexInstanceID: "plex-1", LibraryKeys: []string{"1", "2"}}},
		RunMode: "apply",
		// Client passes empty history — server must ignore + preserve.
		History: nil,
	}
	body, _ := json.Marshal(updated)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/plex-label-rules/rule-1", bytes.NewReader(body))
	req.SetPathValue("id", "rule-1")
	s.handleUpdatePlexLabelRule(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("Update status %d, body %s", rr.Code, rr.Body.String())
	}
	storedAfter := store.Get().PlexLabelRules
	if len(storedAfter) != 1 || len(storedAfter[0].History) != 1 {
		t.Errorf("history dropped on Update: %+v", storedAfter)
	}
	if storedAfter[0].Name != "renamed" {
		t.Errorf("name update didn't apply: %q", storedAfter[0].Name)
	}
	if len(storedAfter[0].Labels) != 2 {
		t.Errorf("label update didn't apply: %+v", storedAfter[0].Labels)
	}
}

// TestPlexLabelRule_ListEmpty returns a JSON array, not null, so the
// front-end's `.length` checks don't crash on first run.
func TestPlexLabelRule_ListEmpty(t *testing.T) {
	s, _ := newTestServerWithPlex(t)
	rr := httptest.NewRecorder()
	s.handleListPlexLabelRules(rr, httptest.NewRequest(http.MethodGet, "/api/plex-label-rules", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("List status %d", rr.Code)
	}
	body := strings.TrimSpace(rr.Body.String())
	if body != "[]" {
		t.Errorf("empty list should return `[]`, got %q", body)
	}
}

// TestPlexLabelRule_RejectsMultipleTargets locks the schema invariant
// the slice-of-Targets modelling depends on: exactly one target per
// rule today. Future multi-Plex relaxation would change the validator;
// the test catches accidental schema drift in either direction.
func TestPlexLabelRule_RejectsMultipleTargets(t *testing.T) {
	s, store := newTestServerWithPlex(t)
	seedPlexLabelRuleFixture(t, store)
	req := core.PlexLabelRule{
		Name:       "Two targets",
		Enabled:    true,
		InstanceID: "arr-r",
		Labels:     []string{"4k"},
		Targets: []core.PlexLabelTarget{
			{PlexInstanceID: "plex-1", LibraryKeys: []string{"1"}},
			{PlexInstanceID: "plex-1", LibraryKeys: []string{"2"}},
		},
	}
	body, _ := json.Marshal(req)
	rr := httptest.NewRecorder()
	s.handleCreatePlexLabelRule(rr, httptest.NewRequest(http.MethodPost, "/api/plex-label-rules", bytes.NewReader(body)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for multi-target rule; got %d body %s", rr.Code, rr.Body.String())
	}
}

// TestPlexLabelRule_RejectsDuplicateLabels locks the dedupe rule —
// duplicate label names (case-insensitive) cause the engine to iterate
// the same label N times per item. Validator surfaces this as a 400
// with a clear error so the UI can highlight the bad row.
func TestPlexLabelRule_RejectsDuplicateLabels(t *testing.T) {
	s, store := newTestServerWithPlex(t)
	seedPlexLabelRuleFixture(t, store)
	req := core.PlexLabelRule{
		Name:       "Dup labels",
		Enabled:    true,
		InstanceID: "arr-r",
		Labels:     []string{"4k", "hdr", "4K"}, // case-insensitive duplicate
		Targets: []core.PlexLabelTarget{
			{PlexInstanceID: "plex-1", LibraryKeys: []string{"1"}},
		},
	}
	body, _ := json.Marshal(req)
	rr := httptest.NewRecorder()
	s.handleCreatePlexLabelRule(rr, httptest.NewRequest(http.MethodPost, "/api/plex-label-rules", bytes.NewReader(body)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for duplicate label; got %d body %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "more than once") {
		t.Errorf("error message should explain the duplicate: %s", rr.Body.String())
	}
}

// TestPlexLabelRule_RejectsDuplicateLibraryKeys locks the same dedupe
// rule for library keys — same engine-waste argument.
func TestPlexLabelRule_RejectsDuplicateLibraryKeys(t *testing.T) {
	s, store := newTestServerWithPlex(t)
	seedPlexLabelRuleFixture(t, store)
	req := core.PlexLabelRule{
		Name:       "Dup libs",
		Enabled:    true,
		InstanceID: "arr-r",
		Labels:     []string{"4k"},
		Targets: []core.PlexLabelTarget{
			{PlexInstanceID: "plex-1", LibraryKeys: []string{"1", "2", "1"}},
		},
	}
	body, _ := json.Marshal(req)
	rr := httptest.NewRecorder()
	s.handleCreatePlexLabelRule(rr, httptest.NewRequest(http.MethodPost, "/api/plex-label-rules", bytes.NewReader(body)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for duplicate library key; got %d body %s", rr.Code, rr.Body.String())
	}
}

// TestPlexLabelRule_Delete removes a rule and leaves siblings alone.
func TestPlexLabelRule_Delete(t *testing.T) {
	s, store := newTestServerWithPlex(t)
	seedPlexLabelRuleFixture(t, store)
	if err := store.Update(func(c *core.Config) {
		c.PlexLabelRules = []core.PlexLabelRule{
			{ID: "rule-a", Name: "a", Enabled: true, InstanceID: "arr-r", AppType: "radarr",
				Labels:  []string{"4k"},
				Targets: []core.PlexLabelTarget{{PlexInstanceID: "plex-1", LibraryKeys: []string{"1"}}}},
			{ID: "rule-b", Name: "b", Enabled: true, InstanceID: "arr-r", AppType: "radarr",
				Labels:  []string{"hdr"},
				Targets: []core.PlexLabelTarget{{PlexInstanceID: "plex-1", LibraryKeys: []string{"1"}}}},
		}
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/plex-label-rules/rule-a", nil)
	req.SetPathValue("id", "rule-a")
	s.handleDeletePlexLabelRule(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("Delete status %d, body %s", rr.Code, rr.Body.String())
	}
	stored := store.Get().PlexLabelRules
	if len(stored) != 1 || stored[0].ID != "rule-b" {
		t.Errorf("Delete affected wrong rules: %+v", stored)
	}
}
