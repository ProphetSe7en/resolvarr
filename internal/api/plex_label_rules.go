package api

// plex_label_rules.go — CRUD for the user-managed PlexLabelRule list
// (the Plex-label-sync mappings). Architectural twin of
// webhook_rules.go's WebhookRule CRUD — same shape, same conventions
// (server-assigned ID, normalised inputs, history preserved on PUT,
// validator owns all error wording).
//
// Endpoints:
//   GET    /api/plex-label-rules           list
//   POST   /api/plex-label-rules           create
//   GET    /api/plex-label-rules/{id}      get one
//   PUT    /api/plex-label-rules/{id}      update (history preserved)
//   DELETE /api/plex-label-rules/{id}      delete
//   POST   /api/plex-label-rules/{id}/run  manual one-off run (Phase D-1)

import (
	"encoding/json"
	"net/http"
	"strings"

	"resolvarr/internal/core"
	"resolvarr/internal/plex"
)

// plexRunTimeout lives in plex_sync_run.go (survives the standalone-rule
// CRUD removal). Referenced here while the legacy handlers remain.

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
	if err := core.ValidatePlexLabelRule(&req, cfg.Instances, cfg.PlexInstances, cfg.PlexLabelRules, ""); err != nil {
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
	if err := core.ValidatePlexLabelRule(&req, cfg.Instances, cfg.PlexInstances, cfg.PlexLabelRules, id); err != nil {
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

// handleRunPlexLabelRule fires the engine for a single saved rule on
// demand. The caller (one-off wizard / "Run now" button in the UI)
// can override the rule's stored RunMode via a {runMode: "preview" |
// "apply"} body — empty body keeps the rule's stored mode.
//
// The handler resolves the Arr + Plex instances from the rule, builds
// clients, calls runPlexLabelSync, appends the result to the rule's
// History (capped at PlexLabelHistoryCap), and returns the result. A
// disabled rule rejects with 400; engine errors surface via the run's
// Status field (response is still 200 — engine status is the
// authoritative outcome).
func (s *Server) handleRunPlexLabelRule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, 400, "id required")
		return
	}

	// Optional body — only field is the run-mode override.
	var req struct {
		RunMode string `json:"runMode,omitempty"`
	}
	if r.ContentLength > 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req); err != nil {
			writeError(w, 400, "invalid body: "+err.Error())
			return
		}
	}
	if req.RunMode != "" && req.RunMode != "apply" && req.RunMode != "preview" {
		writeError(w, 400, `runMode must be "apply" or "preview"`)
		return
	}

	cfg := s.App.Config.Get()

	// Find the rule. We need a snapshot — not a pointer into the
	// store — so concurrent config edits don't mutate the rule
	// mid-fire.
	var ruleSnap core.PlexLabelRule
	found := false
	for _, r := range cfg.PlexLabelRules {
		if r.ID == id {
			ruleSnap = r
			found = true
			break
		}
	}
	if !found {
		writeError(w, 404, "Plex label rule not found")
		return
	}
	if !ruleSnap.Enabled {
		writeError(w, 400, "rule is disabled — enable it before running")
		return
	}

	// Resolve linked Arr instance.
	var arrInst *core.Instance
	for i := range cfg.Instances {
		if cfg.Instances[i].ID == ruleSnap.InstanceID {
			arrInst = &cfg.Instances[i]
			break
		}
	}
	if arrInst == nil {
		writeError(w, 404, "linked Arr instance not found")
		return
	}

	// Resolve linked Plex instance from the rule's first (and only)
	// target. Multi-target rules are out of scope today; engine
	// would need to fan out across targets.
	if len(ruleSnap.Targets) == 0 {
		writeError(w, 400, "rule has no targets")
		return
	}
	var plexInst core.PlexInstance
	plexFound := false
	for _, p := range cfg.PlexInstances {
		if p.ID == ruleSnap.Targets[0].PlexInstanceID {
			plexInst = p
			plexFound = true
			break
		}
	}
	if !plexFound {
		writeError(w, 404, "linked Plex instance not found")
		return
	}

	// Build clients. Plex timeout is generous — a full library
	// walk + label-write pass on a 10k-item library can take a
	// minute or two on busy servers.
	arrClient := s.arrClientFor(arrInst)
	plexClient, err := plex.New(plex.Config{
		URL:          plexInst.URL,
		Token:        plexInst.Token,
		TrustedCerts: plexInst.TrustedCerts,
		Timeout:      plexRunTimeout,
	})
	if err != nil {
		writeError(w, 500, "build Plex client: "+err.Error())
		return
	}

	// Execute the engine. Returns the PlexLabelRuleRun — caller
	// decides what to do with it (persist + respond).
	run := s.runPlexLabelSync(r.Context(), ruleSnap, arrClient, plexClient, plexInst, "manual", req.RunMode)

	// Append to rule history. Cap at PlexLabelHistoryCap. Persist
	// failure is non-fatal — log, return the run anyway so the user
	// sees what happened. The history miss is a soft loss; the run
	// already executed against Plex (or was a no-op preview).
	if err := s.App.Config.Update(func(c *core.Config) {
		for i := range c.PlexLabelRules {
			if c.PlexLabelRules[i].ID == id {
				hist := append(c.PlexLabelRules[i].History, run)
				if len(hist) > core.PlexLabelHistoryCap {
					hist = hist[len(hist)-core.PlexLabelHistoryCap:]
				}
				c.PlexLabelRules[i].History = hist
				return
			}
		}
	}); err != nil {
		// History persistence failed — the run still happened, the
		// user already sees the result in the response, so this is a
		// soft loss. Audited so it surfaces in runs.log for the user
		// to investigate if it recurs.
		if s.App != nil && s.App.RunLog != nil {
			s.App.RunLog.Audit("plex-label-sync", "history append failed", "ruleId="+id, "err="+err.Error())
		}
	}

	writeJSON(w, run)
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
