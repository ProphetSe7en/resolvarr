package api

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"

	"resolvarr/internal/arr"
	"resolvarr/internal/core"
	"resolvarr/internal/core/engine"
)

// scan_recover.go — M3c Recover pipeline. Bash parity for
// tagarr_recover.sh: walk movies with empty/unknown releaseGroup, look up
// grab history per movie, run the safety chain (filename check + import-
// verified grab lookup), classify into one of six buckets, optionally
// patch movieFile.releaseGroup and trigger a rename.
//
// Two entry points share one body — see scan_cleanup.go for the
// runX/handleX wrapper-pattern rationale.
//
// STRICT CONTRACT: this handler delegates ALL recovery decisions to
// engine.ParseReleaseGroupFromFilename and engine.FindImportedGrabGroup.
// No filename parsing, no history walking, no verification logic lives
// in this file. If a future contributor finds themselves writing those
// here, they're writing them in the wrong place — extend internal/core/engine
// (and add a test there) instead.
//
// Sonarr support lives in scan_recover_sonarr.go (per-series →
// per-episodefile). runRecover dispatches by appType — both flows
// share scanResponse + scanRecoverItem so the frontend renders one
// panel for either.

// runRecover is the headless recover pipeline. Dispatches to the
// per-Arr-type implementation. Both share the scanResponse shape +
// scanRecoverItem rows so the frontend renders one panel for either.
func (s *Server) runRecover(ctx context.Context, inst *core.Instance, appType string, req scanRunRequest) (*scanResponse, *apiError) {
	switch appType {
	case "radarr":
		return s.runRecoverRadarr(ctx, inst, req)
	case "sonarr":
		return s.runRecoverSonarr(ctx, inst, req)
	default:
		return nil, newAPIError(400, "unknown instance type: "+appType)
	}
}

// runRecoverRadarr is the original Radarr per-movie recovery flow.
// Walks movies whose movieFile lacks a release-group, fetches grab/
// import history, runs engine.FindImportedGrabGroup against it, and
// optionally PUTs a patched movieFile back. Bash parity:
// tagarr_recover.sh _process_radarr.
func (s *Server) runRecoverRadarr(ctx context.Context, inst *core.Instance, req scanRunRequest) (*scanResponse, *apiError) {
	client := s.arrClientFor(inst)
	items, err := client.ListItems(ctx, "radarr")
	if err != nil {
		return nil, newAPIError(502, "arr list items: "+err.Error())
	}

	// Per-instance exclusion list — user-flagged movies that shouldn't
	// take up scan time or panel space. Fetched fresh from config; the
	// list mutates via /api/recover/exclusions endpoints between scans.
	cfg := s.App.Config.Get()
	excl := cfg.RecoverExclusions[inst.ID]

	// Build the recover-scope set. Bash filters in jq:
	//   .hasFile == true && (releaseGroup == null | "" | "Unknown")
	// hasFile maps to MovieFile != nil for the Go shape. We additionally
	// honor RecoverItems (--movie ID parity) — empty = all affected.
	itemFilter := make(map[int]bool, len(req.RecoverItems))
	for _, id := range req.RecoverItems {
		itemFilter[id] = true
	}
	var affected []arr.Item
	for _, it := range items {
		if it.MovieFile == nil {
			continue
		}
		rg := strings.TrimSpace(it.MovieFile.ReleaseGroup)
		if rg != "" && rg != "Unknown" {
			continue
		}
		if len(itemFilter) > 0 && !itemFilter[it.ID] {
			continue
		}
		// Excluded movies skip the scan entirely — user marked them as
		// faulty / unfixable. Surfaces in the "Show excluded" panel
		// instead so the user can include-again if circumstances change.
		if excl.IsMovieExcluded(it.ID) {
			continue
		}
		affected = append(affected, it)
	}

	// applyFilter: when ApplyMode + RecoverApplyItems is non-empty, only
	// apply for those movie IDs (per-row UI exclude). Empty = apply for
	// every would-fix.
	applyFilter := make(map[int]bool, len(req.RecoverApplyItems))
	for _, id := range req.RecoverApplyItems {
		applyFilter[id] = true
	}

	resp := &scanResponse{
		Mode:   req.Mode,
		Action: "recover",
		Instance: scanInstanceInfo{
			ID:   inst.ID,
			Name: inst.Name,
			Type: inst.Type,
		},
		Totals: scanTotals{
			Items:           len(items),
			RecoverAffected: len(affected),
		},
	}
	var applied scanApplied
	hasApply := false

	for _, it := range affected {
		row := scanRecoverItem{
			ID:           it.ID,
			Title:        it.Title,
			Year:         it.Year,
			TmdbID:       it.TmdbID,
			RelativePath: it.MovieFile.RelativePath,
			SceneName:    it.MovieFile.SceneName,
			MovieFileID:  it.MovieFile.ID,
			CurrentGroup: it.MovieFile.ReleaseGroup,
		}

		// Safety check 1: filename already carries a group? Flag for
		// manual verification — never auto-fix when on-disk evidence
		// exists, even if the app field is empty (user might want the
		// filename truth, not the indexer's truth).
		filenameGroup, hasFilenameGroup, rejectReason := engine.ParseReleaseGroupFromFilename(it.MovieFile.RelativePath)
		if hasFilenameGroup {
			row.Status = "flagged"
			row.FilenameGroup = filenameGroup
			resp.Totals.RecoverFlagged++
			resp.Recover = append(resp.Recover, row)
			continue
		}
		// Surface rejection reason on no-history / failed-verify rows so
		// the UI can show "filtered: looks like resolution token" — helps
		// the user understand why an obvious-looking name was skipped.
		if rejectReason != "" {
			row.FilenameReject = string(rejectReason)
		}

		// Safety check 2: history fetch.
		history, hErr := client.ListHistoryForMovie(ctx, it.ID)
		if hErr != nil {
			row.Status = "fix-failed"
			row.Error = "fetch history: " + hErr.Error()
			resp.Totals.RecoverFixFailed++
			resp.Recover = append(resp.Recover, row)
			continue
		}
		if len(history) == 0 {
			row.Status = "no-history"
			resp.Totals.RecoverNoHistory++
			resp.Recover = append(resp.Recover, row)
			continue
		}

		// Safety chain 3-5: engine walks the history.
		engHistory := make([]engine.HistoryRecord, 0, len(history))
		for _, h := range history {
			engHistory = append(engHistory, engine.HistoryRecord{
				EventType:    engine.HistoryEventType(h.EventType),
				Date:         h.Date,
				SourceTitle:  h.SourceTitle,
				DownloadID:   h.DownloadID,
				ReleaseGroup: h.ReleaseGroup(),
			})
		}
		recoveredGroup, status := engine.FindImportedGrabGroup(engHistory, it.Title, it.Year)
		switch status {
		case engine.RecoverNoVerified:
			row.Status = "failed-verify"
			resp.Totals.RecoverFailedVerify++
			resp.Recover = append(resp.Recover, row)
			continue
		case engine.RecoverVerifiedEmpty:
			row.Status = "no-rls-group"
			resp.Totals.RecoverNoGroup++
			resp.Recover = append(resp.Recover, row)
			continue
		}

		// status == RecoverFound — populate import-event metadata for
		// would-fix rows so the UI drill-down can show the user which
		// import event the recovered group came from.
		row.RecoveredGroup = recoveredGroup
		if importEv := newestImportEvent(engHistory); importEv != nil {
			row.ImportSourceTitle = importEv.SourceTitle
			if !importEv.Date.IsZero() {
				row.ImportDate = importEv.Date.UTC().Format(time.RFC3339)
			}
		}

		if req.Mode == "preview" {
			row.Status = "would-fix"
			resp.Totals.RecoverWouldFix++
			resp.Recover = append(resp.Recover, row)
			continue
		}

		// Apply mode. Skip if user excluded this movie via RecoverApplyItems.
		if len(applyFilter) > 0 && !applyFilter[it.ID] {
			row.Status = "would-fix"
			resp.Totals.RecoverWouldFix++
			resp.Recover = append(resp.Recover, row)
			continue
		}

		hasApply = true
		// Patch movieFile: GET full → set releaseGroup → PUT.
		raw, getErr := client.GetMovieFile(ctx, it.MovieFile.ID)
		if getErr != nil {
			row.Status = "fix-failed"
			row.Error = "fetch movieFile: " + getErr.Error()
			resp.Totals.RecoverFixFailed++
			resp.Recover = append(resp.Recover, row)
			continue
		}
		if putErr := client.UpdateMovieFileReleaseGroup(ctx, it.MovieFile.ID, raw, recoveredGroup); putErr != nil {
			row.Status = "fix-failed"
			row.Error = "PUT movieFile: " + putErr.Error()
			resp.Totals.RecoverFixFailed++
			resp.Recover = append(resp.Recover, row)
			continue
		}
		row.Status = "fixed"
		resp.Totals.RecoverFixed++

		// Optional rename. Bash RENAME=true default — container honors
		// the user's request flag. A rename failure does NOT degrade the
		// row to fix-failed (the metadata patch succeeded); it surfaces
		// in a separate counter and the row's RenameTriggered stays false.
		if req.RecoverRename {
			if renErr := client.TriggerRadarrRenameFiles(ctx, it.ID, []int{it.MovieFile.ID}); renErr != nil {
				resp.Totals.RecoverRenameFailed++
				// Log via Error field so user can see why rename didn't fire.
				if row.Error == "" {
					row.Error = "rename: " + renErr.Error()
				}
			} else {
				row.RenameTriggered = true
			}
		}
		resp.Recover = append(resp.Recover, row)
	}

	// Sort the result list alphabetically by title (case-insensitive) so
	// the UI cards line up predictably regardless of the order Arr
	// returned movies. Recover doesn't group by release-group like Tag
	// library does — it's a flat per-movie list — so this is the only
	// sort needed.
	sort.Slice(resp.Recover, func(i, j int) bool {
		return strings.ToLower(resp.Recover[i].Title) < strings.ToLower(resp.Recover[j].Title)
	})

	if hasApply {
		resp.Applied = &applied
	}
	return resp, nil
}

// handleScanRecover is the HTTP wrapper around runRecover.
func (s *Server) handleScanRecover(w http.ResponseWriter, r *http.Request, inst *core.Instance, appType string, req scanRunRequest) {
	ctx, cancel := context.WithTimeout(r.Context(), scanTimeout)
	defer cancel()
	resp, apiErr := s.runRecover(ctx, inst, appType, req)
	if apiErr != nil {
		s.auditScan(req.auditSource(), "recover", inst, req, nil, apiErr.Message)
		writeAPIError(w, apiErr)
		return
	}
	s.auditScan(req.auditSource(), "recover", inst, req, resp, "")
	s.dumpScanJSON("recover", resp)
	writeJSON(w, resp)
}
