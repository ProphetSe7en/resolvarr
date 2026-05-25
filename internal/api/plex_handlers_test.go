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

// All credential values in this file are SYNTHETIC. They never reach
// any real Plex / Arr / qBit instance. Generated entropy only.

const (
	syntheticPlexToken    = "0123456789abcdef0123456789abcdef0123"
	syntheticPlexTokenAlt = "fedcba9876543210fedcba9876543210fedc"
)

func newTestServerWithPlex(t *testing.T) (*Server, *core.ConfigStore) {
	t.Helper()
	dir := t.TempDir()
	store := core.NewConfigStore(dir)
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	app := &core.App{Config: store}
	return &Server{App: app}, store
}

// TestPlexInstance_ListMasksToken locks the masking pattern:
// 1. List response masks Token
// 2. Store still holds the plaintext Token after the List call
// 3. Non-credential metadata (Name, URL, TrustedCerts, Libraries) survives
//
// Same 3-invariant shape as the WebhookRule + QbitInstance masking
// tests. Catches a regression where the handler-side mask helper
// mutates the store-shared pointer (which is the bug the Phase B
// security work caught on the webhook_rules.go path).
func TestPlexInstance_ListMasksToken(t *testing.T) {
	s, store := newTestServerWithPlex(t)

	if err := store.Update(func(c *core.Config) {
		c.PlexInstances = []core.PlexInstance{
			{
				ID:           "plex-1",
				Name:         "Main Plex",
				URL:          "http://plex.lan:32400",
				Token:        syntheticPlexToken,
				TrustedCerts: false,
				Libraries: []core.PlexLibrary{
					{Key: "1", Title: "Movies", Type: "movie"},
					{Key: "2", Title: "TV Shows", Type: "show"},
				},
			},
		}
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// 1. List response masks Token.
	rr := httptest.NewRecorder()
	s.handleListPlexInstances(rr, httptest.NewRequest(http.MethodGet, "/api/plex-instances", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("List status %d, body %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if strings.Contains(body, syntheticPlexToken) {
		t.Errorf("List response leaked Token in: %s", body)
	}
	if !strings.Contains(body, maskSentinel) {
		t.Errorf("List response missing masked sentinel; body: %s", body)
	}

	// 2. Store still holds the plaintext Token. Critical: masking
	// must NEVER mutate the underlying store, only the response copy.
	storedAfter := store.Get().PlexInstances
	if len(storedAfter) != 1 || storedAfter[0].Token != syntheticPlexToken {
		t.Errorf("store lost plaintext Token after List call; got %+v", storedAfter)
	}

	// 3. Non-credential metadata (Name, URL, Libraries, TrustedCerts)
	// survives the round-trip.
	var listed []core.PlexInstance
	if err := json.Unmarshal([]byte(body), &listed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("expected 1 instance, got %d", len(listed))
	}
	got := listed[0]
	if got.Name != "Main Plex" {
		t.Errorf("Name corrupted: %q", got.Name)
	}
	if got.URL != "http://plex.lan:32400" {
		t.Errorf("URL corrupted: %q", got.URL)
	}
	if got.Token != maskSentinel {
		t.Errorf("Token should be sentinel, got %q", got.Token)
	}
	if len(got.Libraries) != 2 {
		t.Errorf("Libraries dropped: %+v", got.Libraries)
	}
}

// TestPlexInstance_GetConfigMasksToken locks defense-in-depth masking
// on the broader handleGetConfig endpoint. Bearer credentials must
// never appear in plaintext even on the catch-all config dump.
func TestPlexInstance_GetConfigMasksToken(t *testing.T) {
	s, store := newTestServerWithPlex(t)
	if err := store.Update(func(c *core.Config) {
		c.PlexInstances = []core.PlexInstance{
			{ID: "plex-1", Name: "Main", URL: "http://plex.lan:32400", Token: syntheticPlexToken},
		}
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rr := httptest.NewRecorder()
	s.handleGetConfig(rr, httptest.NewRequest(http.MethodGet, "/api/config", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("GetConfig status %d", rr.Code)
	}
	body := rr.Body.String()
	if strings.Contains(body, syntheticPlexToken) {
		t.Errorf("/api/config leaked Plex Token: %s", body)
	}
}

// TestPlexInstance_UpdatePreservesMaskedToken locks the round-trip
// safety: when the UI sends back the masked sentinel (because the user
// edited Name/URL without re-typing the token), the handler must
// preserve the stored plaintext rather than overwriting with the
// placeholder string.
func TestPlexInstance_UpdatePreservesMaskedToken(t *testing.T) {
	s, store := newTestServerWithPlex(t)
	if err := store.Update(func(c *core.Config) {
		c.PlexInstances = []core.PlexInstance{
			{ID: "plex-1", Name: "Main", URL: "http://plex.lan:32400", Token: syntheticPlexToken},
		}
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// UI re-sends the masked sentinel — the user just changed the Name.
	updated := core.PlexInstance{
		ID:    "plex-1",
		Name:  "Main Plex Renamed",
		URL:   "http://plex.lan:32400",
		Token: maskSentinel,
	}
	bodyBytes, _ := json.Marshal(updated)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/plex-instances/plex-1", bytes.NewReader(bodyBytes))
	req.SetPathValue("id", "plex-1")
	s.handleUpdatePlexInstance(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("Update status %d, body %s", rr.Code, rr.Body.String())
	}

	// Store must still hold the original plaintext Token.
	storedAfter := store.Get().PlexInstances
	if len(storedAfter) != 1 {
		t.Fatalf("instance lost on Update: %+v", storedAfter)
	}
	if storedAfter[0].Token != syntheticPlexToken {
		t.Errorf("Update overwrote stored Token with placeholder; got %q want %q",
			storedAfter[0].Token, syntheticPlexToken)
	}
	if storedAfter[0].Name != "Main Plex Renamed" {
		t.Errorf("Update didn't apply Name change: %q", storedAfter[0].Name)
	}
}

// TestPlexInstance_UpdateAcceptsNewToken locks that a non-masked
// non-empty Token on update DOES replace the stored value (so the user
// can rotate the X-Plex-Token without delete+recreate).
func TestPlexInstance_UpdateAcceptsNewToken(t *testing.T) {
	s, store := newTestServerWithPlex(t)
	if err := store.Update(func(c *core.Config) {
		c.PlexInstances = []core.PlexInstance{
			{ID: "plex-1", Name: "Main", URL: "http://plex.lan:32400", Token: syntheticPlexToken},
		}
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	updated := core.PlexInstance{
		ID:    "plex-1",
		Name:  "Main",
		URL:   "http://plex.lan:32400",
		Token: syntheticPlexTokenAlt,
	}
	bodyBytes, _ := json.Marshal(updated)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/plex-instances/plex-1", bytes.NewReader(bodyBytes))
	req.SetPathValue("id", "plex-1")
	s.handleUpdatePlexInstance(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("Update status %d, body %s", rr.Code, rr.Body.String())
	}
	if got := store.Get().PlexInstances[0].Token; got != syntheticPlexTokenAlt {
		t.Errorf("Token rotation failed; stored = %q want %q", got, syntheticPlexTokenAlt)
	}
}

// TestPlexInstance_CreateRejectsMaskedToken catches a copy-paste trap
// where a user (or buggy UI) sends the masked sentinel string as the
// brand-new Token. There's no stored value to fall back on, so we
// fail loud instead of saving "********" as the token.
func TestPlexInstance_CreateRejectsMaskedToken(t *testing.T) {
	s, _ := newTestServerWithPlex(t)
	body := `{"name":"Main","url":"http://plex.lan:32400","token":"********"}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/plex-instances", strings.NewReader(body))
	s.handleCreatePlexInstance(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("Create with masked token: status %d, want 400; body %s", rr.Code, rr.Body.String())
	}
}

// TestPlexInstance_CreateRequiresToken locks token-required at the
// handler boundary. plex.New also enforces this at construction, but
// the handler short-circuits with a clearer error.
func TestPlexInstance_CreateRequiresToken(t *testing.T) {
	s, _ := newTestServerWithPlex(t)
	body := `{"name":"Main","url":"http://plex.lan:32400"}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/plex-instances", strings.NewReader(body))
	s.handleCreatePlexInstance(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("Create without token: status %d, want 400", rr.Code)
	}
}

// TestPlexInstance_DeleteCascadesToPlexLabelRules locks the cleanup
// invariant: deleting a Plex instance drops any PlexLabelRule whose
// single target was that Plex. Half-broken rules are worse than no
// rules — the engine has nothing to write to once the target is gone.
func TestPlexInstance_DeleteCascadesToPlexLabelRules(t *testing.T) {
	s, store := newTestServerWithPlex(t)
	if err := store.Update(func(c *core.Config) {
		c.PlexInstances = []core.PlexInstance{
			{ID: "plex-1", Name: "Main", URL: "http://plex.lan:32400", Token: syntheticPlexToken,
				Libraries: []core.PlexLibrary{{Key: "1", Title: "Movies", Type: "movie"}}},
			{ID: "plex-2", Name: "Other", URL: "http://plex2.lan:32400", Token: syntheticPlexTokenAlt,
				Libraries: []core.PlexLibrary{{Key: "1", Title: "Movies", Type: "movie"}}},
		}
		c.PlexLabelRules = []core.PlexLabelRule{
			{ID: "rule-targets-plex-1", Name: "r1", Enabled: true, InstanceID: "i1", AppType: "radarr",
				Labels:  []string{"4k"},
				Targets: []core.PlexLabelTarget{{PlexInstanceID: "plex-1", LibraryKeys: []string{"1"}}}},
			{ID: "rule-targets-plex-2", Name: "r2", Enabled: true, InstanceID: "i1", AppType: "radarr",
				Labels:  []string{"4k"},
				Targets: []core.PlexLabelTarget{{PlexInstanceID: "plex-2", LibraryKeys: []string{"1"}}}},
		}
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/plex-instances/plex-1", nil)
	req.SetPathValue("id", "plex-1")
	s.handleDeletePlexInstance(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("Delete status %d, body %s", rr.Code, rr.Body.String())
	}

	cfg := store.Get()
	if len(cfg.PlexInstances) != 1 || cfg.PlexInstances[0].ID != "plex-2" {
		t.Errorf("PlexInstance not deleted cleanly: %+v", cfg.PlexInstances)
	}
	if len(cfg.PlexLabelRules) != 1 || cfg.PlexLabelRules[0].ID != "rule-targets-plex-2" {
		t.Errorf("PlexLabelRule cleanup didn't run: %+v", cfg.PlexLabelRules)
	}
}
