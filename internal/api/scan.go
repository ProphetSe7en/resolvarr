package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"resolvarr/internal/core"
	"resolvarr/internal/core/engine"
)

// scan.go — dispatcher for /api/scan/run. Owns request decoding +
// validation + instance/filter resolution, then routes to the per-action
// handler in scan_tag.go / scan_discover.go / scan_cleanup.go /
// scan_recover.go. Type definitions live in scan_types.go; small shared
// helpers in scan_helpers.go.
//
// Strict contract on this whole package: orchestration only — every
// per-movie, per-group tag decision is delegated to engine.DecideTag().
// The handlers never implement match/filter/should-have logic. If a
// future contributor finds themselves writing a word-boundary regex or a
// quality predicate inside scan_*.go, they're writing it in the wrong
// place — extend internal/core/engine and add a test there instead.

func (s *Server) handleScanRun(w http.ResponseWriter, r *http.Request) {
	var req scanRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "invalid body")
		return
	}
	if req.InstanceID == "" {
		writeError(w, 400, "instanceId is required")
		return
	}

	// Action defaults to "tag" for back-compat with v1 callers.
	if req.Action == "" {
		req.Action = "tag"
	}

	switch req.Action {
	case "tag":
		// Tag mode validates Mode strictly (preview / apply have side-effect difference).
		if req.Mode != "preview" && req.Mode != "apply" {
			writeError(w, 400, "mode must be preview or apply")
			return
		}
	case "discover":
		// Discover is preview-only — there's no "apply" semantic until the
		// user POSTs selected candidates to /api/groups themselves. Force
		// Mode to preview internally for the response shape.
		req.Mode = "preview"
	case "cleanup":
		// Cleanup has both modes: preview lists managed tags with 0 movies;
		// apply deletes them. Apply with CleanupLabels narrows to that subset.
		if req.Mode != "preview" && req.Mode != "apply" {
			writeError(w, 400, "mode must be preview or apply")
			return
		}
	case "recover":
		// Recover has both modes: preview lists per-movie verdicts (would-fix /
		// flagged / no-history / no-rls-group / failed-verify); apply patches
		// movieFile.releaseGroup and (if RecoverRename) triggers RenameFiles.
		// RecoverItems narrows the scope to specific movie IDs (--movie parity).
		if req.Mode != "preview" && req.Mode != "apply" {
			writeError(w, 400, "mode must be preview or apply")
			return
		}
	case "audiotags":
		// Audio tags (M4): emits informative auto-tags from the
		// audio stream of mediaInfo. Config lives in cfg.AudioTags.
		if req.Mode != "preview" && req.Mode != "apply" {
			writeError(w, 400, "mode must be preview or apply")
			return
		}
	case "videotags":
		// Video tags (M4): resolution + codec + HDR from the video
		// stream of mediaInfo. Config lives in cfg.VideoTags.
		if req.Mode != "preview" && req.Mode != "apply" {
			writeError(w, 400, "mode must be preview or apply")
			return
		}
	case "dvdetail":
		// Dolby Vision detail (M4b): preview shows the desired DV-detail
		// labels (mel/fel/dvprofile8/cm2/cm4) vs the movie's current
		// managed set; apply commits the diff. Distinct from extratags
		// because the underlying flow is slow (ffmpeg+dovi_tool RPU
		// extraction, cached to disk) and gated on opt-in tools install.
		// Configuration lives in cfg.DvDetail (not the request body).
		if req.Mode != "preview" && req.Mode != "apply" {
			writeError(w, 400, "mode must be preview or apply")
			return
		}
	case "combined":
		writeError(w, 501, fmt.Sprintf("action %q is not implemented yet", req.Action))
		return
	default:
		writeError(w, 400, "action must be tag, discover, cleanup, recover, audiotags, videotags, or dvdetail")
		return
	}

	cfg := s.App.Config.Get()

	// Resolve instance.
	var inst *core.Instance
	for i := range cfg.Instances {
		if cfg.Instances[i].ID == req.InstanceID {
			inst = &cfg.Instances[i]
			break
		}
	}
	if inst == nil {
		writeError(w, 404, "instance not found")
		return
	}

	// Per-request overlay (quickfix wizard support). Identical
	// semantics to applyScheduleOverlay — when the caller posts
	// rule-style snapshots, this run uses them instead of globals.
	// nil fields fall through to globals so existing callers (Library
	// scan single-action runs, scheduler) keep working unchanged.
	//
	// Auto-tags overlays must pass the same prefix + vocab validation
	// that the global PUT handlers apply — without this a quickfix
	// payload with bad prefix would slip past and surface as a
	// confusing 502 from Radarr's tag-label validator.
	if req.OverlayAudioTags != nil {
		if err := validateAudioTagsConfig(*req.OverlayAudioTags); err != nil {
			writeError(w, 400, "overlayAudioTags: "+err.Error())
			return
		}
	}
	if req.OverlayVideoTags != nil {
		if err := validateVideoTagsConfig(*req.OverlayVideoTags); err != nil {
			writeError(w, 400, "overlayVideoTags: "+err.Error())
			return
		}
	}
	if req.OverlayDvDetail != nil {
		if err := validateDvDetailConfig(*req.OverlayDvDetail); err != nil {
			writeError(w, 400, "overlayDvDetail: "+err.Error())
			return
		}
	}
	cfg = applyRuleOverlay(cfg, req.OverlayFilters, req.OverlayAudioTags, req.OverlayVideoTags, req.OverlayDvDetail, req.OverlayReleaseGroupIDs, inst.Type, req.OverlayInjectGroups)

	// Per-action Sonarr support is rolling in incrementally — Recover is
	// the first one (per-series → per-episodefile), the rest still
	// short-circuit here with 501 until each is ported. Done this way
	// (per-action gate) so the dispatcher can keep dispatching to all
	// actions without each handler needing its own boilerplate "501
	// Sonarr not implemented" branch.
	appType := inst.Type
	switch appType {
	case "radarr":
		// every action supported
	case "sonarr":
		switch req.Action {
		case "recover", "audiotags", "videotags":
			// supported — recover (M3c) + audio/video tags (M-Sonarr).
			// Audio/video walk series → episodefiles, aggregate per-bucket
			// using SonarrAggregation, apply at series level.
		default:
			writeError(w, 501, "Sonarr is supported for: recover, audiotags, videotags. Other actions are coming as separate milestones.")
			return
		}
	default:
		writeError(w, 400, "unknown instance type: "+appType)
		return
	}

	// Pick per-Arr-type FilterConfig.
	var filterCfg engine.FilterConfig
	switch appType {
	case "radarr":
		filterCfg = cfg.Filters.Radarr
	case "sonarr":
		filterCfg = cfg.Filters.Sonarr
	}

	// Per-action dispatch. Each handler owns its own pipeline beyond this
	// point — the dispatcher's job ends here.
	switch req.Action {
	case "discover":
		s.handleScanDiscover(w, r, cfg, inst, appType, filterCfg, req)
	case "cleanup":
		s.handleScanCleanup(w, r, cfg, inst, appType, req)
	case "recover":
		s.handleScanRecover(w, r, inst, appType, req)
	case "tag":
		s.handleScanTag(w, r, cfg, inst, appType, filterCfg, req)
	case "audiotags":
		s.handleScanAudioTags(w, r, cfg, inst, appType, req)
	case "videotags":
		s.handleScanVideoTags(w, r, cfg, inst, appType, req)
	case "dvdetail":
		s.handleScanDvDetail(w, r, cfg, inst, appType, req)
	}
}
