package api

// plex_label_rules.go — CRUD for the user-managed PlexLabelRule list
// (the Plex-label-sync mappings). Architectural twin of
// webhook_rules.go's WebhookRule CRUD — same shape, same conventions
// (server-assigned ID, normalised inputs, history preserved on PUT,
// validator owns all error wording).
//
// Endpoints:
//   GET    /api/plex-label-rules         list
//   POST   /api/plex-label-rules         create
//   GET    /api/plex-label-rules/{id}    get one
//   PUT    /api/plex-label-rules/{id}    update (history preserved)
//   DELETE /api/plex-label-rules/{id}    delete

import (
	"encoding/json"
	"net/http"
	"strings"

	"resolvarr/internal/core"
)

// handleListPlexLabelRules returns every configured rule. No credential
// fields on the rule (the Arr API key + Plex token live on their
// referenced instance), so no masking step here.
func (s *Server) handleListPlexLabelRules(w http.ResponseWriter, r *http.Request) {
	cfg := s.App.Config.Get()
	// Force non-nil JSON-array for empty state — frontend uses
	// `.length` checks and treats `null` as a config-corruption
	// signal. Same pattern as handleListWebhookRules.
	if cfg.PlexLabelRules == nil {
		writeJSON(w, []core.PlexLabelRule{})
		return
	}
	writeJSON(w, cfg.PlexLabelRules)
}

// handleGetPlexLabelRule returns one rule by ID.
func (s *Server) handleGetPlexLabelRule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, 400, "id required")
		return
	}
	cfg := s.App.Config.Get()
	for _, rule := range cfg.PlexLabelRules {
		if rule.ID == id {
			writeJSON(w, rule)
			return
		}
	}
	writeError(w, 404, "Plex label rule not found")
}

// handleCreatePlexLabelRule adds a new rule. ID + AppType auto-derived;
// AppType is denormalised from the linked instance so dispatch / UI
// filters don't have to chase the instance reference on every read.
func (s *Server) handleCreatePlexLabelRule(w http.ResponseWriter, r *http.Request) {
	var req core.PlexLabelRule
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&req); err != nil {
		writeError(w, 400, "invalid body: "+err.Error())
		return
	}
	cfg := s.App.Config.Get()
	// Derive AppType from the linked instance before validating.
	// Client can pass it for explicitness, but the linked-instance's
	// Type is authoritative.
	for i := range cfg.Instances {
		if cfg.Instances[i].ID == strings.TrimSpace(req.InstanceID) {
			req.AppType = cfg.Instances[i].Type
			break
		}
	}
	if err := core.ValidatePlexLabelRule(req, cfg.Instances, cfg.PlexInstances, cfg.PlexLabelRules, ""); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	// Normalise + default before save.
	req.ID = genID()
	req.Name = strings.TrimSpace(req.Name)
	req.InstanceID = strings.TrimSpace(req.InstanceID)
	if req.RunMode == "" {
		req.RunMode = "apply"
	}
	for i, l := range req.Labels {
		req.Labels[i] = strings.TrimSpace(l)
	}
	for i := range req.Targets {
		req.Targets[i].PlexInstanceID = strings.TrimSpace(req.Targets[i].PlexInstanceID)
	}
	// Drop any client-supplied history — server owns the history
	// window. Defensive; validator doesn't reject populated history
	// today but if it ever did, this would already be cleaned.
	req.History = nil
	if err := s.App.Config.Update(func(c *core.Config) {
		c.PlexLabelRules = append(c.PlexLabelRules, req)
	}); err != nil {
		writeError(w, 500, "save: "+err.Error())
		return
	}
	writeJSON(w, req)
}

// handleUpdatePlexLabelRule edits an existing rule. History is
// preserved (server-owned). Run-mode + label set + targets can all
// change; AppType is re-derived from the (possibly changed) linked
// instance.
func (s *Server) handleUpdatePlexLabelRule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, 400, "id required")
		return
	}
	var req core.PlexLabelRule
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&req); err != nil {
		writeError(w, 400, "invalid body: "+err.Error())
		return
	}
	cfg := s.App.Config.Get()
	var existing *core.PlexLabelRule
	for i := range cfg.PlexLabelRules {
		if cfg.PlexLabelRules[i].ID == id {
			existing = &cfg.PlexLabelRules[i]
			break
		}
	}
	if existing == nil {
		writeError(w, 404, "Plex label rule not found")
		return
	}
	// Re-derive AppType from the (possibly updated) instance.
	req.InstanceID = strings.TrimSpace(req.InstanceID)
	for i := range cfg.Instances {
		if cfg.Instances[i].ID == req.InstanceID {
			req.AppType = cfg.Instances[i].Type
			break
		}
	}
	if err := core.ValidatePlexLabelRule(req, cfg.Instances, cfg.PlexInstances, cfg.PlexLabelRules, id); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if req.RunMode == "" {
		req.RunMode = "apply"
	}
	req.Name = strings.TrimSpace(req.Name)
	for i, l := range req.Labels {
		req.Labels[i] = strings.TrimSpace(l)
	}
	for i := range req.Targets {
		req.Targets[i].PlexInstanceID = strings.TrimSpace(req.Targets[i].PlexInstanceID)
	}
	// Preserve server-owned state.
	req.ID = id
	req.History = existing.History
	if err := s.App.Config.Update(func(c *core.Config) {
		for i := range c.PlexLabelRules {
			if c.PlexLabelRules[i].ID == id {
				c.PlexLabelRules[i] = req
				return
			}
		}
	}); err != nil {
		writeError(w, 500, "save: "+err.Error())
		return
	}
	writeJSON(w, req)
}

// handleDeletePlexLabelRule removes a rule by ID.
func (s *Server) handleDeletePlexLabelRule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, 400, "id required")
		return
	}
	if err := s.App.Config.Update(func(c *core.Config) {
		out := make([]core.PlexLabelRule, 0, len(c.PlexLabelRules))
		for _, rule := range c.PlexLabelRules {
			if rule.ID != id {
				out = append(out, rule)
			}
		}
		c.PlexLabelRules = out
	}); err != nil {
		writeError(w, 500, "save: "+err.Error())
		return
	}
	writeJSON(w, map[string]any{"status": "ok"})
}
