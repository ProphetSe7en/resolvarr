package api

import (
	"context"
	"fmt"
	"net/http"

	"resolvarr/internal/core"
)

// scan_cleanup.go — standalone cleanup pipeline (M3-tag-cleanup).
// Bash parity for `CLEANUP_UNUSED_TAGS=true` minus the tag-mode tail-pass:
// this is the user-driven entry point that runs without a tag-mode pass.
// (The tag-mode chain that runs cleanup as a tail is in scan_tag.go.)
//
// Two entry points share one body:
//
//   runCleanup     — headless, takes ctx + parsed args, returns response
//                    + typed apiError. Called by the HTTP wrapper and by
//                    the M3d scheduler when a cleanup-mode schedule fires.
//   handleScanCleanup — HTTP wrapper. Translates ctx + r.Body into the
//                       runCleanup arg shape, encodes the result.
//
// Preview returns the candidate list (managed labels with 0 movies); apply
// deletes them. Apply with CleanupLabels narrows to a subset (used by the
// "Delete selected" UI flow); empty CleanupLabels deletes every candidate.
//
// SAFETY INVARIANT — the load-bearing constraint of this entire feature:
// cleanup ONLY iterates labels resolved from cfg.ReleaseGroups. The
// computeCleanupCandidates helper enforces this via its managedLabels
// argument; this handler is the source of that argument. Quality-profile
// tags, custom-format tags, and manually-created Radarr tags are out of
// scope and must stay untouched. Bash enforces the same via
// `for cfg in "${RELEASE_GROUPS[@]}"`.

// runCleanup is the headless cleanup pipeline.
func (s *Server) runCleanup(ctx context.Context, cfg core.Config, inst *core.Instance, appType string, req scanRunRequest) (*scanResponse, *apiError) {
	managedLabels := managedLabelsForType(cfg, appType)
	if len(managedLabels) == 0 {
		return nil, newAPIError(400, "no release groups configured for this instance type")
	}

	client := s.arrClientFor(inst)
	items, err := client.ListItems(ctx, appType)
	if err != nil {
		return nil, newAPIError(502, "arr list items: "+err.Error())
	}
	tagDetails, err := client.ListTagDetails(ctx)
	if err != nil {
		return nil, newAPIError(502, "arr list tags: "+err.Error())
	}
	labelToID := make(map[string]int, len(tagDetails))
	for _, t := range tagDetails {
		labelToID[t.Label] = t.ID
	}

	candidates := computeCleanupCandidates(items, labelToID, managedLabels, nil, nil)

	resp := &scanResponse{
		Mode:   req.Mode,
		Action: "cleanup",
		Instance: scanInstanceInfo{
			ID:   inst.ID,
			Name: inst.Name,
			Type: inst.Type,
		},
		Totals: scanTotals{
			Items:        len(items),
			TagsToDelete: candidates,
		},
	}

	if req.Mode == "preview" {
		return resp, nil
	}

	// Apply mode — optional CleanupLabels narrows the delete set. Treated as
	// an intersection with candidates: a label not in candidates is silently
	// dropped (caller might be referencing a stale list, no need to error).
	targetSet := make(map[string]bool, len(req.CleanupLabels))
	for _, l := range req.CleanupLabels {
		targetSet[l] = true
	}

	applied := scanApplied{}
	for _, c := range candidates {
		if len(targetSet) > 0 && !targetSet[c.Label] {
			continue
		}
		if err := client.DeleteTag(ctx, c.TagID); err != nil {
			return nil, newAPIError(502, fmt.Sprintf("delete tag %q: %v", c.Label, err))
		}
		applied.TagsDeleted = append(applied.TagsDeleted, c.Label)
	}
	resp.Applied = &applied
	return resp, nil
}

// handleScanCleanup is the HTTP wrapper around runCleanup. Builds the
// scanTimeout-bounded context from r.Context() and encodes the response.
func (s *Server) handleScanCleanup(w http.ResponseWriter, r *http.Request, cfg core.Config, inst *core.Instance, appType string, req scanRunRequest) {
	ctx, cancel := context.WithTimeout(r.Context(), scanTimeout)
	defer cancel()
	resp, apiErr := s.runCleanup(ctx, cfg, inst, appType, req)
	if apiErr != nil {
		s.auditScan(req.auditSource(), "cleanup", inst, req, nil, apiErr.Message)
		writeAPIError(w, apiErr)
		return
	}
	s.auditScan(req.auditSource(), "cleanup", inst, req, resp, "")
	s.dumpScanJSON("cleanup", resp)
	writeJSON(w, resp)
}
