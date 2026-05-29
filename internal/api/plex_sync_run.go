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
	"os"
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

	// Persist the run to /config/logs so it shows up in the History tab
	// alongside the other one-off scans. Best-effort; a dump failure
	// doesn't block the response.
	s.dumpPlexSyncJSON(run, arrInst)

	writeJSON(w, run)
}

// plexSyncDumpFile is the on-disk shape for a one-off Plex sync run.
// The top-level mode / instance / totals.items fields mirror
// scanResponse so the generic readScanPreview (scan_history.go) can
// build a History-row preview without special-casing plexsync. The
// full run hangs off .run for the result viewer to render.
type plexSyncDumpFile struct {
	Mode     string `json:"mode"`
	Instance struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"instance"`
	Totals struct {
		Items int `json:"items"`
	} `json:"totals"`
	Run core.PlexLabelRuleRun `json:"run"`
}

// dumpPlexSyncJSON writes a one-off Plex sync run to
// /config/logs/scan-plexsync-{ts}.json (atomic .tmp+rename, same as
// dumpScanJSON). Returns the path or "" on failure. The History tab
// lists it via the scan-plexsync- filename prefix and opens it through
// the Plex run modal.
func (s *Server) dumpPlexSyncJSON(run core.PlexLabelRuleRun, inst *core.Instance) string {
	if inst == nil {
		return ""
	}
	var dump plexSyncDumpFile
	dump.Mode = run.RunMode
	dump.Instance.ID = inst.ID
	dump.Instance.Name = inst.Name
	dump.Instance.Type = inst.Type
	dump.Totals.Items = run.ItemsTotal
	dump.Run = run

	dir := "/config/logs"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "resolvarr: plexsync dump mkdir: %v\n", err)
		return ""
	}
	path := fmt.Sprintf("%s/scan-plexsync-%s.json", dir, time.Now().Format("20060102-150405"))
	data, err := json.MarshalIndent(dump, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolvarr: plexsync dump marshal: %v\n", err)
		return ""
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "resolvarr: plexsync dump write: %v\n", err)
		return ""
	}
	if err := os.Rename(tmp, path); err != nil {
		fmt.Fprintf(os.Stderr, "resolvarr: plexsync dump rename: %v\n", err)
		_ = os.Remove(tmp)
		return ""
	}
	return path
}
