package api

import (
	"context"
	"net/http"
	"sort"
	"strings"

	"resolvarr/internal/core"
	"resolvarr/internal/core/engine"
)

// scan_discover.go — discover-mode handler. Walks the library and surfaces
// release-groups that pass the configured quality + audio filters but
// aren't already in the user's release-group config. Mirrors
// `tagarr.sh --discover` (and, with req.IncludeKnown=true,
// `tagarr.sh --discover-clean` which scans the library as if no groups
// were configured — useful for an audit-style report).
//
// Discover writes nothing. The user picks candidates from the response and
// adds them through the normal /api/groups endpoint, which keeps validation
// and the Add-Group safety net in one place.
//
// Two entry points share one body — see scan_cleanup.go for the
// runX/handleX wrapper-pattern rationale.
//
// Strict contract: this handler does NO tag logic. It evaluates the same
// quality + audio filters that engine.DecideTag uses for the filtered
// path, but never considers tag state, release-group matching, or
// should-have. The output is "would this group's first sample have passed
// engine's filter check?" — nothing more.

// runDiscover is the headless discover pipeline.
func (s *Server) runDiscover(ctx context.Context, cfg core.Config, inst *core.Instance, appType string, filterCfg engine.FilterConfig, req scanRunRequest) (*scanResponse, *apiError) {
	// Build the "known" set: all configured groups of this Arr-type, by
	// search term (lowercased). Mirrors bash's `known_release_groups` —
	// includes both Enabled and Disabled groups, since a disabled entry is
	// the UI equivalent of "commented out in bash config" and should still
	// suppress re-discovery (the user already knows about it). IncludeKnown
	// mode ignores this set entirely (clean-slate report).
	knownSet := make(map[string]bool, len(cfg.ReleaseGroups))
	for _, g := range cfg.ReleaseGroups {
		if g.Type != appType {
			continue
		}
		if g.Search == "" {
			continue
		}
		knownSet[strings.ToLower(g.Search)] = true
	}

	client := s.arrClientFor(inst)
	items, err := client.ListItems(ctx, appType)
	if err != nil {
		return nil, newAPIError(502, "arr list items: "+err.Error())
	}

	// Aggregate by lowercase release-group key. The first movie that
	// triggers discovery for a given group becomes the sample (preserving
	// original case in Search and the quality/audio detail strings derived
	// from that movie's combined-fields text). Subsequent movies with the
	// same group only bump the count.
	discovered := make(map[string]*scanDiscoveredGroup)

	for _, item := range items {
		if item.MovieFile == nil {
			continue
		}
		rg := item.MovieFile.ReleaseGroup
		if rg == "" {
			continue
		}
		rgLower := strings.ToLower(rg)

		// Skip if already known and not in include-known mode. IncludeKnown
		// re-scans every group as if config were empty — the user wants to
		// see everything in their library that passes filters, including
		// already-configured groups.
		if !req.IncludeKnown && knownSet[rgLower] {
			continue
		}

		// Build the same combined-fields text engine.DecideTag uses, so
		// the filter evaluation here matches what tag-mode would compute
		// for a configured group searching this same string. Joining with
		// space prevents two-field tokens from merging into a false match.
		combined := strings.ToLower(item.MovieFile.RelativePath) + " " +
			strings.ToLower(item.MovieFile.SceneName) + " " +
			rgLower

		if !engine.CheckQuality(filterCfg, combined) {
			continue
		}
		if !engine.CheckAudio(filterCfg, combined) {
			continue
		}

		// Per-movie evaluation strings are reused below for both the
		// group-level "first sample" fields and per-movie sample entries.
		qDetail := engine.QualityDetailPass(combined)
		aDetail := engine.AudioDetailPass(combined)

		if existing, ok := discovered[rgLower]; ok {
			existing.Count++
			// Append sample only while under the cap. Older movies stay
			// in the list — first-N order mirrors bash's first-seen-wins
			// for the registered display name.
			if len(existing.Samples) < discoveredMaxSamples {
				existing.Samples = append(existing.Samples, scanDiscoveredSample{
					MovieID:       item.ID,
					TmdbID:        item.TmdbID,
					Title:         item.Title,
					Year:          item.Year,
					ReleaseGroup:  rg,
					SceneName:     item.MovieFile.SceneName,
					RelativePath:  item.MovieFile.RelativePath,
					QualityDetail: qDetail,
					AudioDetail:   aDetail,
				})
			}
			continue
		}
		discovered[rgLower] = &scanDiscoveredGroup{
			Search:           rg,
			Count:            1,
			SampleMovieID:    item.ID,
			SampleMovieTitle: item.Title,
			QualityDetail:    qDetail,
			AudioDetail:      aDetail,
			Samples: []scanDiscoveredSample{{
				MovieID:       item.ID,
				TmdbID:        item.TmdbID,
				Title:         item.Title,
				Year:          item.Year,
				ReleaseGroup:  rg,
				SceneName:     item.MovieFile.SceneName,
				RelativePath:  item.MovieFile.RelativePath,
				QualityDetail: qDetail,
				AudioDetail:   aDetail,
			}},
		}
	}

	// Flatten + sort alphabetically (case-insensitive). UI gets a stable
	// order regardless of map iteration randomness. Per-group samples are
	// also sorted by title so the drill-in cards line up alphabetically
	// when the user expands a group — matches Tag library bucket ordering.
	out := make([]scanDiscoveredGroup, 0, len(discovered))
	for _, d := range discovered {
		sort.Slice(d.Samples, func(i, j int) bool {
			return strings.ToLower(d.Samples[i].Title) < strings.ToLower(d.Samples[j].Title)
		})
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Search) < strings.ToLower(out[j].Search)
	})

	resp := &scanResponse{
		Mode:   "preview",
		Action: "discover",
		Instance: scanInstanceInfo{
			ID:   inst.ID,
			Name: inst.Name,
			Type: inst.Type,
		},
		Totals: scanTotals{
			Items:      len(items),
			Discovered: len(out),
		},
		Discovered: out,
	}

	// Discover write-back: when DiscoverWriteBack=true AND we're in
	// apply-mode, persist new groups to cfg.ReleaseGroups so subsequent
	// runs (and the frontend chain's Tag phase) pick them up.
	// AutoActivateDiscovered flags the new entries' Enabled bit.
	// Persistence failure is non-fatal — the preview is still useful
	// and the user can retry.
	//
	// Mode gate is defence-in-depth — every current caller already
	// gates on apply-mode at the call site (frontend chain at
	// app.js:6921, scheduler_runner.go:559 / :166), so this branch
	// rarely sees `req.Mode == "preview"`. The gate exists so a
	// future caller can't silently bypass: a "preview" run must NEVER
	// write to the user's release-groups list.
	//
	// Standalone Discover-fane requests don't set DiscoverWriteBack so
	// this branch is a no-op for the manual "Find new groups" UI; it
	// only fires when the frontend chain (Quick fix-all) or the M3d
	// schedule-runner sends DiscoverWriteBack=true with apply-mode.
	if req.DiscoverWriteBack && req.Mode == "apply" && len(out) > 0 {
		added, err := s.applyDiscoverWriteBack(out, inst.Type, req.AutoActivateDiscovered)
		if err == nil && len(added) > 0 {
			resp.Applied = &scanApplied{
				DiscoverAdded: added,
			}
		}
	}
	return resp, nil
}

// applyDiscoverWriteBack persists newly-discovered release-groups
// to cfg.ReleaseGroups, skipping any whose Tag (case-insensitive)
// or Search-text already exists. Returns the slice of added rows
// (in scanDiscoverAdded shape — frontend uses these to extend its
// overlay for subsequent phases).
//
// Shared by runDiscover (action="discover" via /api/scan/run) and
// the schedule-runner's persistDiscoveredGroups (kept thin around
// this for now so the schedule path's local-cfg-inject keeps its
// existing []core.ReleaseGroup return type).
func (s *Server) applyDiscoverWriteBack(discovered []scanDiscoveredGroup, instType string, enable bool) ([]scanDiscoverAdded, error) {
	if len(discovered) == 0 {
		return nil, nil
	}
	var added []scanDiscoverAdded
	err := s.App.Config.Update(func(c *core.Config) {
		seenTag := make(map[string]bool, len(c.ReleaseGroups))
		seenSearch := make(map[string]bool, len(c.ReleaseGroups))
		for _, g := range c.ReleaseGroups {
			seenTag[strings.ToLower(g.Tag)] = true
			seenSearch[strings.ToLower(g.Search)] = true
		}
		for _, d := range discovered {
			search := strings.TrimSpace(d.Search)
			if search == "" {
				continue
			}
			tag := strings.ToLower(search)
			if seenTag[tag] || seenSearch[strings.ToLower(search)] {
				continue
			}
			id := genID()
			c.ReleaseGroups = append(c.ReleaseGroups, core.ReleaseGroup{
				ID:      id,
				Search:  search,
				Tag:     tag,
				Display: search,
				Mode:    "filtered",
				Type:    instType,
				Enabled: enable,
			})
			added = append(added, scanDiscoverAdded{
				ID: id, Search: search, Tag: tag, Enabled: enable,
			})
			seenTag[tag] = true
			seenSearch[strings.ToLower(search)] = true
		}
	})
	if err != nil {
		return nil, err
	}
	return added, nil
}

// handleScanDiscover is the HTTP wrapper around runDiscover.
func (s *Server) handleScanDiscover(w http.ResponseWriter, r *http.Request, cfg core.Config, inst *core.Instance, appType string, filterCfg engine.FilterConfig, req scanRunRequest) {
	ctx, cancel := context.WithTimeout(r.Context(), scanTimeout)
	defer cancel()
	resp, apiErr := s.runDiscover(ctx, cfg, inst, appType, filterCfg, req)
	if apiErr != nil {
		s.auditScan(req.auditSource(), "discover", inst, req, nil, apiErr.Message)
		writeAPIError(w, apiErr)
		return
	}
	s.auditScan(req.auditSource(), "discover", inst, req, resp, "")
	s.dumpScanJSON("discover", resp)
	writeJSON(w, resp)
}
