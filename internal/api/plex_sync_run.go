package api

// plex_sync_run.go — the shared run path for Plex label sync, used by
// every trigger context that fires a bulk sync from inline config:
//
//   - POST /api/plex-sync/run   one-off run (Tag Library / Plex label sync tab)
//   - scheduler runner          JobModePlexSync standalone + combined phase
//
// There is no standalone persisted "Plex label rule" — the config is
// always supplied inline (PlexLabelSyncConfig) and the engine input is
// synthesized per-call via AsPlexLabelRule.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"resolvarr/internal/core"
	"resolvarr/internal/plex"
)

// plexRunTimeout bounds a full library walk + label-write pass. Generous
// — a 10k-item Plex library on a busy server can take a minute or two.
const plexRunTimeout = 5 * time.Minute

// runPlexSyncFromConfig resolves the Arr + Plex instances, builds their
// clients, and fires the bulk engine against an inline
// PlexLabelSyncConfig. Returned error covers only resolve/build
// failures (instance vanished, bad Plex URL); engine-level problems
// come back inside the PlexLabelRuleRun (run.Status == "error").
//
// trigger is "manual" | "scheduled" — recorded on the run for the
// Activity row. runMode "" falls back to "apply".
func (s *Server) runPlexSyncFromConfig(
	ctx context.Context,
	cfg core.Config,
	syncCfg *core.PlexLabelSyncConfig,
	arrInstanceID, trigger, runMode string,
) (core.PlexLabelRuleRun, error) {
	if syncCfg == nil {
		return core.PlexLabelRuleRun{}, fmt.Errorf("Plex sync config is required")
	}

	arrInst := findInstanceByID(cfg, arrInstanceID)
	if arrInst == nil {
		return core.PlexLabelRuleRun{}, fmt.Errorf("Arr instance %q not found", arrInstanceID)
	}

	// Synthesize the engine input. AsPlexLabelRule pins the Arr
	// instance + appType from the surrounding context (the config
	// itself is instance-agnostic).
	rule := syncCfg.AsPlexLabelRule(arrInst.ID, arrInst.Type)

	var plexInst core.PlexInstance
	plexFound := false
	for _, p := range cfg.PlexInstances {
		if p.ID == syncCfg.PlexInstanceID {
			plexInst = p
			plexFound = true
			break
		}
	}
	if !plexFound {
		return core.PlexLabelRuleRun{}, fmt.Errorf("Plex instance %q not found", syncCfg.PlexInstanceID)
	}

	arrClient := s.arrClientFor(arrInst)
	plexClient, err := plex.New(plex.Config{
		URL:          plexInst.URL,
		Token:        plexInst.Token,
		TrustedCerts: plexInst.TrustedCerts,
		Timeout:      plexRunTimeout,
	})
	if err != nil {
		return core.PlexLabelRuleRun{}, fmt.Errorf("build Plex client: %w", err)
	}

	return s.runPlexLabelSync(ctx, rule, arrClient, plexClient, plexInst, trigger, runMode), nil
}

// handleRunPlexSync — POST /api/plex-sync/run. One-off run from inline
// config; nothing is persisted (mirrors how every other Tag Library
// run works). The caller surfaces the returned PlexLabelRuleRun in the
// result panel + (Phase 2) the scan-history JSON dump.
func (s *Server) handleRunPlexSync(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ArrInstanceID string                     `json:"arrInstanceId"`
		RunMode       string                     `json:"runMode,omitempty"`
		PlexLabelSync *core.PlexLabelSyncConfig  `json:"plexLabelSync"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&req); err != nil {
		writeError(w, 400, "invalid body: "+err.Error())
		return
	}
	if req.RunMode != "" && req.RunMode != "apply" && req.RunMode != "preview" {
		writeError(w, 400, `runMode must be "apply" or "preview"`)
		return
	}
	if req.PlexLabelSync == nil {
		writeError(w, 400, "plexLabelSync config is required")
		return
	}

	cfg := s.App.Config.Get()

	arrInst := findInstanceByID(cfg, req.ArrInstanceID)
	if arrInst == nil {
		writeError(w, 404, "Arr instance not found")
		return
	}

	// Validate the inline config (label/library dedupe, type filter,
	// cached-key existence, target-types). Same validator the webhook
	// + schedule paths use.
	if err := core.ValidatePlexLabelSyncConfig(req.PlexLabelSync, cfg.PlexInstances, arrInst.Type); err != nil {
		writeError(w, 400, err.Error())
		return
	}

	run, err := s.runPlexSyncFromConfig(r.Context(), cfg, req.PlexLabelSync, arrInst.ID, "manual", req.RunMode)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}

	writeJSON(w, run)
}
