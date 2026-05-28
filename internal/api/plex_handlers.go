package api

// plex_handlers.go — CRUD + Test Connection + Library refresh for the
// user-managed Plex instance list. Mirrors qbit_handlers.go's shape
// because the lifecycle (list / create / update / delete / test) is
// identical: instances exist independently of any specific rule so a
// single Plex can serve multiple PlexLabelRules without duplicating
// credentials.
//
// Endpoints:
//   GET    /api/plex-instances                       list (tokens masked)
//   POST   /api/plex-instances                       create
//   PUT    /api/plex-instances/{id}                  update (preserves masked token)
//   DELETE /api/plex-instances/{id}                  delete + clean PlexLabelRule refs
//   POST   /api/plex-instances/{id}/test             Test Connection (stored creds)
//   POST   /api/plex-instances/test                  Test Connection inline (body has full creds)
//   POST   /api/plex-instances/{id}/fetch-libraries  refresh Libraries cache from Plex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"resolvarr/internal/core"
	"resolvarr/internal/plex"
)

// plexTestTimeout caps the test-connection probe + the fetch-libraries
// probe so a misconfigured URL doesn't stall the request goroutine for
// the full client default. Plex's /identity + /library/sections are
// both fast on a healthy server.
const plexTestTimeout = 10 * time.Second

// validatePlexInstanceBody rejects malformed creates / updates. URL
// shape is delegated to plex.New (same pattern as qbit.New); here we
// just enforce the user-facing constraints (name + uniqueness).
func validatePlexInstanceBody(req core.PlexInstance, all []core.PlexInstance, ignoreID string) error {
	req.Name = strings.TrimSpace(req.Name)
	req.URL = strings.TrimSpace(req.URL)
	if req.Name == "" {
		return errors.New("name is required")
	}
	if req.URL == "" {
		return errors.New("URL is required")
	}
	// URL shape via the client constructor — same trick qbit_handlers
	// uses to avoid duplicating the parser logic.
	if _, err := plex.New(plex.Config{URL: req.URL, Token: "validate-only-non-empty-stub"}); err != nil {
		return err
	}
	lower := strings.ToLower(req.Name)
	for _, existing := range all {
		if existing.ID == ignoreID {
			continue
		}
		if strings.ToLower(existing.Name) == lower {
			return fmt.Errorf("name %q is already used by another Plex instance", req.Name)
		}
	}
	return nil
}

// handleListPlexInstances returns every configured Plex instance with
// the Token masked. Libraries cache stays unmasked (it's read-only
// metadata).
func (s *Server) handleListPlexInstances(w http.ResponseWriter, r *http.Request) {
	cfg := s.App.Config.Get()
	out := make([]core.PlexInstance, 0, len(cfg.PlexInstances))
	for _, pi := range cfg.PlexInstances {
		out = append(out, maskPlexInstance(pi))
	}
	writeJSON(w, out)
}

// handleCreatePlexInstance adds a new entry. ID auto-generated; the
// user-supplied Token is stored as-is.
func (s *Server) handleCreatePlexInstance(w http.ResponseWriter, r *http.Request) {
	var req core.PlexInstance
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&req); err != nil {
		writeError(w, 400, "invalid body: "+err.Error())
		return
	}
	if req.Token == maskSentinel {
		writeError(w, 400, "token cannot be the masked placeholder — paste the real X-Plex-Token")
		return
	}
	if strings.TrimSpace(req.Token) == "" {
		writeError(w, 400, "token is required")
		return
	}
	cfg := s.App.Config.Get()
	if err := validatePlexInstanceBody(req, cfg.PlexInstances, ""); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	created := core.PlexInstance{
		ID:           genID(),
		Name:         strings.TrimSpace(req.Name),
		URL:          strings.TrimSpace(req.URL),
		Token:        strings.TrimSpace(req.Token),
		TrustedCerts: req.TrustedCerts,
		// Libraries deliberately empty on create — user clicks
		// "Fetch libraries" from the modal after save to populate
		// the cache. Keeps the create path side-effect-free.
	}
	if err := s.App.Config.Update(func(c *core.Config) {
		c.PlexInstances = append(c.PlexInstances, created)
	}); err != nil {
		writeError(w, 500, "save: "+err.Error())
		return
	}
	writeJSON(w, maskPlexInstance(created))
}

// handleUpdatePlexInstance edits an existing entry. Empty / masked
// token preserves the stored value (Edit modal lets the user touch
// name/URL/TrustedCerts without re-typing the token).
func (s *Server) handleUpdatePlexInstance(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, 400, "id required")
		return
	}
	var req core.PlexInstance
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&req); err != nil {
		writeError(w, 400, "invalid body: "+err.Error())
		return
	}
	cfg := s.App.Config.Get()
	var existing *core.PlexInstance
	for i := range cfg.PlexInstances {
		if cfg.PlexInstances[i].ID == id {
			existing = &cfg.PlexInstances[i]
			break
		}
	}
	if existing == nil {
		writeError(w, 404, "Plex instance not found")
		return
	}
	if err := validatePlexInstanceBody(req, cfg.PlexInstances, id); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	// Token preservation — masked sentinel OR empty string keeps the
	// stored value. To explicitly clear the token the user deletes +
	// re-creates the instance (matches the qbit handler convention).
	token := strings.TrimSpace(req.Token)
	if token == "" || req.Token == maskSentinel {
		token = existing.Token
	}
	updated := core.PlexInstance{
		ID:           id,
		Name:         strings.TrimSpace(req.Name),
		URL:          strings.TrimSpace(req.URL),
		Token:        token,
		TrustedCerts: req.TrustedCerts,
		Libraries:    existing.Libraries, // preserved — refresh via /fetch-libraries
	}
	if err := s.App.Config.Update(func(c *core.Config) {
		for i := range c.PlexInstances {
			if c.PlexInstances[i].ID == id {
				c.PlexInstances[i] = updated
				return
			}
		}
	}); err != nil {
		writeError(w, 500, "save: "+err.Error())
		return
	}
	writeJSON(w, maskPlexInstance(updated))
}

// handleDeletePlexInstance removes a Plex instance entry. Inline
// Plex-sync configs that referenced it (a schedule's PlexSync or a
// webhook rule's PlexLabelSync) are left as-is; the engine resolves
// the Plex instance at run-time and returns a "Plex instance not
// found" error rather than writing anywhere, so a dangling reference
// fails safe instead of needing an eager cascade.
func (s *Server) handleDeletePlexInstance(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, 400, "id required")
		return
	}
	if err := s.App.Config.Update(func(c *core.Config) {
		outInst := make([]core.PlexInstance, 0, len(c.PlexInstances))
		for _, pi := range c.PlexInstances {
			if pi.ID != id {
				outInst = append(outInst, pi)
			}
		}
		c.PlexInstances = outInst
	}); err != nil {
		writeError(w, 500, "save: "+err.Error())
		return
	}
	writeJSON(w, map[string]any{"status": "ok"})
}

// handleTestPlexInstance probes the saved credentials for an
// existing instance ID. Used by the per-row Test Connection button.
func (s *Server) handleTestPlexInstance(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, 400, "id required")
		return
	}
	cfg := s.App.Config.Get()
	var pi *core.PlexInstance
	for i := range cfg.PlexInstances {
		if cfg.PlexInstances[i].ID == id {
			pi = &cfg.PlexInstances[i]
			break
		}
	}
	if pi == nil {
		writeError(w, 404, "Plex instance not found")
		return
	}
	s.runPlexTest(w, r, plex.Config{
		URL:          pi.URL,
		Token:        pi.Token,
		TrustedCerts: pi.TrustedCerts,
	})
}

// handleTestPlexInline probes credentials supplied in the request body
// — used by the Add modal before save. Masked-token + ID combo resolves
// to the stored token (Edit modal can test without re-typing).
func (s *Server) handleTestPlexInline(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID           string `json:"id,omitempty"`
		URL          string `json:"url"`
		Token        string `json:"token"`
		TrustedCerts bool   `json:"trustedCerts"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&req); err != nil {
		writeError(w, 400, "invalid body: "+err.Error())
		return
	}
	if strings.TrimSpace(req.URL) == "" {
		writeError(w, 400, "URL is required")
		return
	}
	token := req.Token
	tokenMasked := token == maskSentinel
	if (tokenMasked || token == "") && req.ID != "" {
		cfg := s.App.Config.Get()
		for _, pi := range cfg.PlexInstances {
			if pi.ID == req.ID {
				token = pi.Token
				break
			}
		}
	} else if tokenMasked {
		writeError(w, 400, "token is masked — supply the real X-Plex-Token or include the instance id")
		return
	}
	if strings.TrimSpace(token) == "" {
		writeError(w, 400, "token is required")
		return
	}
	s.runPlexTest(w, r, plex.Config{
		URL:          strings.TrimSpace(req.URL),
		Token:        token,
		TrustedCerts: req.TrustedCerts,
	})
}

// handleFetchPlexLibraries refreshes the Libraries cache on the given
// instance by hitting Plex's /library/sections. Returns the new
// library list (unmasked — library metadata isn't a secret).
func (s *Server) handleFetchPlexLibraries(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, 400, "id required")
		return
	}
	cfg := s.App.Config.Get()
	var pi *core.PlexInstance
	for i := range cfg.PlexInstances {
		if cfg.PlexInstances[i].ID == id {
			pi = &cfg.PlexInstances[i]
			break
		}
	}
	if pi == nil {
		writeError(w, 404, "Plex instance not found")
		return
	}
	client, err := plex.New(plex.Config{
		URL:          pi.URL,
		Token:        pi.Token,
		TrustedCerts: pi.TrustedCerts,
		Timeout:      plexTestTimeout,
	})
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), plexTestTimeout)
	defer cancel()
	libs, err := client.GetLibraries(ctx)
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	cached := make([]core.PlexLibrary, 0, len(libs))
	for _, l := range libs {
		cached = append(cached, core.PlexLibrary{
			Key:   l.Key,
			Title: l.Title,
			Type:  l.Type,
		})
	}
	if err := s.App.Config.Update(func(c *core.Config) {
		for i := range c.PlexInstances {
			if c.PlexInstances[i].ID == id {
				c.PlexInstances[i].Libraries = cached
				return
			}
		}
	}); err != nil {
		writeError(w, 500, "save: "+err.Error())
		return
	}
	writeJSON(w, map[string]any{
		"ok":        true,
		"libraries": cached,
	})
}

// handleInspectPlexLibrary is a diagnostic helper for verifying what
// Plex actually returns for a library. Used when label-sync results
// look wrong — lets the user pick a known-labelled movie title and
// see the raw GUIDs + Labels Plex sends back. Query params:
//
//	libraryKey  — required, Plex section ID
//	title       — optional substring filter (case-insensitive); empty
//	              returns the first 10 items as a sample
//	limit       — optional cap on results, default 10, max 50
//
// Returns: { ok, items: [{title, year, ratingKey, type, guids[], labels[]}] }
func (s *Server) handleInspectPlexLibrary(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, 400, "id required")
		return
	}
	libraryKey := r.URL.Query().Get("libraryKey")
	if libraryKey == "" {
		writeError(w, 400, "libraryKey query param required")
		return
	}
	titleFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("title")))
	limit := 10
	if v := r.URL.Query().Get("limit"); v != "" {
		// Best-effort parse; ignore bad values.
		var parsed int
		if _, err := fmt.Sscanf(v, "%d", &parsed); err == nil && parsed > 0 {
			if parsed > 50 {
				parsed = 50
			}
			limit = parsed
		}
	}

	cfg := s.App.Config.Get()
	var pi *core.PlexInstance
	for i := range cfg.PlexInstances {
		if cfg.PlexInstances[i].ID == id {
			pi = &cfg.PlexInstances[i]
			break
		}
	}
	if pi == nil {
		writeError(w, 404, "Plex instance not found")
		return
	}
	client, err := plex.New(plex.Config{
		URL:          pi.URL,
		Token:        pi.Token,
		TrustedCerts: pi.TrustedCerts,
		Timeout:      plexTestTimeout,
	})
	if err != nil {
		writeError(w, 500, "build client: "+err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), plexTestTimeout)
	defer cancel()
	items, err := client.GetItems(ctx, libraryKey)
	if err != nil {
		writeError(w, 502, "fetch items: "+err.Error())
		return
	}

	type inspectItem struct {
		Title       string   `json:"title"`
		Year        int      `json:"year,omitempty"`
		RatingKey   string   `json:"ratingKey"`
		Type        string   `json:"type"`
		GUIDs       []string `json:"guids"`
		Labels      []string `json:"labels"`
		Collections []string `json:"collections"`
	}
	// Per-item metadata fetch — the bulk /all endpoint omits labels
	// on many Plex Server versions, so for diagnostic inspection we
	// hit /library/metadata/{ratingKey} for each matched item to get
	// the authoritative label list. Same path the engine uses now.
	out := make([]inspectItem, 0, limit)
	var rawSample string
	for _, it := range items {
		if titleFilter != "" && !strings.Contains(strings.ToLower(it.Title), titleFilter) {
			continue
		}
		labels := it.Labels
		collections := it.Collections
		guids := it.GUIDs
		if full, err := client.GetItemMetadata(ctx, it.RatingKey); err == nil {
			labels = full.Labels
			collections = full.Collections
			guids = full.GUIDs
		}
		// Capture the raw Plex JSON for the FIRST matched item so the
		// caller can see exactly what shape Plex is returning. If our
		// typed decoder is missing fields (e.g. they're under a
		// different JSON key than expected), this surfaces it.
		if rawSample == "" {
			if raw, err := client.FetchRawItemMetadata(ctx, it.RatingKey); err == nil {
				rawSample = string(raw)
			}
		}
		out = append(out, inspectItem{
			Title:       it.Title,
			Year:        it.Year,
			RatingKey:   it.RatingKey,
			Type:        it.Type,
			GUIDs:       guids,
			Labels:      labels,
			Collections: collections,
		})
		if len(out) >= limit {
			break
		}
	}
	writeJSON(w, map[string]any{
		"ok":             true,
		"total":          len(items),
		"shown":          len(out),
		"items":          out,
		"rawFirstSample": rawSample,
	})
}

// runPlexTest is the shared probe path: build a Client, hit /identity,
// surface the friendly server name on success. Capped via
// plexTestTimeout to avoid stalls on misconfigured URLs.
func (s *Server) runPlexTest(w http.ResponseWriter, r *http.Request, cfg plex.Config) {
	if cfg.Timeout == 0 {
		cfg.Timeout = plexTestTimeout
	}
	client, err := plex.New(cfg)
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), plexTestTimeout)
	defer cancel()
	friendly, err := client.Ping(ctx)
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	msg := "Connected — Plex accepted the X-Plex-Token."
	if friendly != "" {
		msg = fmt.Sprintf("Connected — server reports %q.", friendly)
	}
	writeJSON(w, map[string]any{
		"ok":      true,
		"message": msg,
		"server":  friendly,
	})
}

// maskPlexInstance returns a shallow copy of pi with Token masked.
// Same clone-before-mutate pattern as maskWebhookRuleCreds — the
// PlexInstance value itself is safe to copy (string/bool fields +
// Libraries slice already deep-copied by ConfigStore.Get).
func maskPlexInstance(pi core.PlexInstance) core.PlexInstance {
	out := pi
	if out.Token != "" {
		out.Token = maskSentinel
	}
	return out
}
