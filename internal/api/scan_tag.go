package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"resolvarr/internal/arr"
	"resolvarr/internal/core"
	"resolvarr/internal/core/engine"
)

// scan_tag.go — tag-mode handler. The user's library walk that decides
// per-(movie, group) whether to ADD / REMOVE / KEEP / SKIP a tag, and
// in apply-mode commits those decisions via batched editor calls.
//
// Strict contract: every per-(movie, group) decision is delegated to
// engine.DecideTag(). The handler composes (decision.ShouldHave, hasTag)
// into an action label and routes the result. No matching / filter /
// should-have logic lives here.
//
// Optional features layered on top of the core decision pass:
//   - M3e secondary-sync: mirror primary's decisions to a second
//     instance via TmdbID matching, plus an orphan-cleanup pass on the
//     secondary library
//   - M3-tag-cleanup chain: after apply, optionally delete managed tags
//     that ended up with 0 movies

// runTag is the headless tag-mode pipeline. Called by the HTTP wrapper
// (handleScanTag) and by the M3d scheduler when a tag-mode schedule fires.
//
// See scan_cleanup.go for the runX/handleX wrapper-pattern rationale —
// HTTP layer concerns (ctx setup from r.Context, response encoding) live
// in the wrapper; the actual decision + apply logic is here.
func (s *Server) runTag(ctx context.Context, cfg core.Config, inst *core.Instance, appType string, filterCfg engine.FilterConfig, req scanRunRequest) (*scanResponse, *apiError) {
	// Filter release-groups: by type, and (optionally) by the runGroups
	// subset the caller passed. Empty subset = all groups of this type.
	runSubset := make(map[string]bool, len(req.RunGroups))
	for _, id := range req.RunGroups {
		runSubset[id] = true
	}
	var groups []core.ReleaseGroup
	for _, g := range cfg.ReleaseGroups {
		if g.Type != appType {
			continue
		}
		// A disabled group is the UI equivalent of commenting out the bash
		// array entry — the user still wants to keep the search / tag /
		// display settings, just pause the rule. Skip it on every scan mode.
		if !g.Enabled {
			continue
		}
		if len(runSubset) > 0 && !runSubset[g.ID] {
			continue
		}
		// Defensive: a group with no Tag label is unusable (can't apply
		// anywhere in Arr). Skip and flag in logs — but for now we just
		// skip silently; the Groups config UI validates on save.
		if g.Tag == "" || g.Search == "" {
			continue
		}
		groups = append(groups, g)
	}
	if len(groups) == 0 {
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

	// Build label→tagID map from Arr's current tag list. Missing labels
	// are lazy-created in apply mode (bash parity). In preview mode a
	// missing label just means "add would also create this tag" —
	// surfaced via scanApplied.TagsCreated when apply runs.
	labelToID := make(map[string]int, len(tagDetails))
	for _, t := range tagDetails {
		labelToID[t.Label] = t.ID
	}

	// Decision pass. Engine-only logic for ShouldHave; handler only
	// composes (decision.ShouldHave, hasTag-on-item) into an action label.
	resp := scanResponse{
		Mode:   req.Mode,
		Action: "tag",
		Instance: scanInstanceInfo{
			ID:   inst.ID,
			Name: inst.Name,
			Type: inst.Type,
		},
		Totals: scanTotals{
			Items:  len(items),
			Groups: len(groups),
		},
	}

	// Batched action accumulators. Deduped by item ID in case a single
	// movie decides "add tag X" for two groups that share the same Tag
	// label — we only want it in the add batch once.
	addByTag := make(map[string]map[int]struct{})
	removeByTag := make(map[string]map[int]struct{})
	ensureSet := func(m map[string]map[int]struct{}, k string) map[int]struct{} {
		if m[k] == nil {
			m[k] = make(map[int]struct{})
		}
		return m[k]
	}

	// ===== Secondary-sync setup (M3e) =====
	//
	// Mirrors bash tagarr.sh ENABLE_SYNC_TO_SECONDARY=true. When enabled,
	// after the primary decision pass we mirror each (movie, tag) decision
	// to the secondary instance via TmdbID matching. Plus an orphan pass
	// that cleans up tags on secondary movies the primary either doesn't
	// have or no longer qualifies for.
	var (
		syncEnabled       bool
		secondary         *core.Instance
		secondaryClient   *arr.Client
		secondaryItems    []arr.Item
		secondaryByTmdb   map[int]arr.Item
		secondaryTagToID  map[string]int
		secAddByTag       map[string]map[int]struct{}
		secRemoveByTag    map[string]map[int]struct{}
		primaryTagStatus  map[string]string // "tmdbId:tag" → "true"|"false"
		secOrphansRemoved int
	)
	if req.SyncToInstanceID != "" {
		// Resolve secondary instance from config.
		for i := range cfg.Instances {
			if cfg.Instances[i].ID == req.SyncToInstanceID {
				secondary = &cfg.Instances[i]
				break
			}
		}
		if secondary == nil {
			return nil, newAPIError(404, "syncToInstanceId not found")
		}
		if secondary.Type != appType {
			return nil, newAPIError(400, "syncToInstanceId must be the same type as instanceId")
		}
		if secondary.ID == inst.ID {
			return nil, newAPIError(400, "syncToInstanceId cannot be the same as instanceId")
		}

		// Fetch secondary library + tag list. Hard-fail on errors —
		// surfacing the issue to the user is clearer than silently
		// dropping sync for an interactive run. Schedules can use a
		// future soft-fail mode if needed (M3d).
		secondaryClient = s.arrClientFor(secondary)
		var secErr error
		secondaryItems, secErr = secondaryClient.ListItems(ctx, appType)
		if secErr != nil {
			return nil, newAPIError(502, "secondary list items: "+secErr.Error())
		}
		secTagDetails, secErr := secondaryClient.ListTagDetails(ctx)
		if secErr != nil {
			return nil, newAPIError(502, "secondary list tags: "+secErr.Error())
		}

		// Build the lookup table — TmdbID is the only matching key (bash
		// parity, no ImdbID fallback). Items with TmdbID==0 are silently
		// skipped from sync (matches bash's tmdb_id null/empty guard).
		secondaryByTmdb = make(map[int]arr.Item, len(secondaryItems))
		for _, it := range secondaryItems {
			if it.TmdbID > 0 {
				secondaryByTmdb[it.TmdbID] = it
			}
		}
		secondaryTagToID = make(map[string]int, len(secTagDetails))
		for _, t := range secTagDetails {
			secondaryTagToID[t.Label] = t.ID
		}
		secAddByTag = make(map[string]map[int]struct{})
		secRemoveByTag = make(map[string]map[int]struct{})
		primaryTagStatus = make(map[string]string)
		syncEnabled = true
	}

	for _, item := range items {
		// No-file movies (file deleted from disk in this instance) get
		// processed with an empty MovieFile. Engine's MatchReleaseGroup
		// returns false on empty inputs → ShouldHave=false →
		// composeAction(false, hasTag) = "remove" if the tag is
		// currently on the movie, "skip" otherwise. The earlier
		// behaviour (continue + no decisions) left stale tags on
		// no-file movies on PRIMARY while the orphan-walk on SECONDARY
		// removed them — asymmetric. Treating no-file as "doesn't
		// qualify, so remove" makes both sides consistent.
		//
		// Note: this only affects movies that were ALREADY tagged. A
		// no-file movie that never had the tag stays tag-free
		// (composeAction(false, false) = "skip", no-op).
		var mf engine.MovieFile
		hasFile := item.MovieFile != nil
		if hasFile {
			mf = engine.MovieFile{
				RelativePath: item.MovieFile.RelativePath,
				SceneName:    item.MovieFile.SceneName,
				ReleaseGroup: item.MovieFile.ReleaseGroup,
			}
		} else {
			resp.Totals.NoFile++
		}
		// Which tag IDs does Arr currently have on this item?
		hasTagID := make(map[int]struct{}, len(item.Tags))
		for _, tid := range item.Tags {
			hasTagID[tid] = struct{}{}
		}

		// Per-movie missing-in-secondary check (M3e). Counted once per
		// primary movie, NOT per (movie, group) decision — a movie is
		// either in secondary or it isn't, group choice doesn't change
		// that. The user expects this count to equal the size of the
		// set difference primary minus secondary.
		if syncEnabled && item.TmdbID > 0 {
			if _, found := secondaryByTmdb[item.TmdbID]; !found {
				resp.Totals.SecondaryMissing++
			}
		}

		var itemDecisions []scanDecision
		for _, g := range groups {
			// THE single authoritative decision call. Handler does no
			// tag logic of its own beyond routing the result.
			d := engine.DecideTag(mf, engine.GroupConfig{
				Search:  g.Search,
				Tag:     g.Tag,
				Display: g.Display,
				Mode:    g.Mode,
			}, filterCfg)

			// Compose (ShouldHave, hasTag) → action. hasTag is a pure
			// Arr-state lookup — not a decision, just a comparison.
			currentID, existsInArr := labelToID[g.Tag]
			hasTagOnItem := existsInArr
			if hasTagOnItem {
				_, hasTagOnItem = hasTagID[currentID]
			}
			action := composeAction(d.ShouldHave, hasTagOnItem)

			switch action {
			case "add":
				ensureSet(addByTag, g.Tag)[item.ID] = struct{}{}
				resp.Totals.ToAdd++
			case "remove":
				ensureSet(removeByTag, g.Tag)[item.ID] = struct{}{}
				resp.Totals.ToRemove++
			case "keep":
				resp.Totals.ToKeep++
				// "skip" has no totals column — it's the common no-op case
				// (rejected match + tag not present).
			}

			// Secondary mirror (M3e). Compute regardless of mode — same
			// data is needed for both preview rendering and apply queues.
			// "" = sync not requested for this run.
			// "missing" = sync requested but secondary has no movie matching this TmdbID.
			// "add"/"remove"/"keep"/"skip" = mirror semantics on secondary.
			var secondaryAction string
			var secondaryHasTag bool
			if syncEnabled && item.TmdbID > 0 {
				// Track primary's verdict for the orphan-cleanup pass. The
				// key is "tmdbId:tag" so the orphan pass can ask "does
				// primary think this secondary movie should have this tag?"
				primaryStatusKey := fmt.Sprintf("%d:%s", item.TmdbID, g.Tag)
				if d.ShouldHave {
					primaryTagStatus[primaryStatusKey] = "true"
				} else {
					primaryTagStatus[primaryStatusKey] = "false"
				}
				secMovie, secMovieFound := secondaryByTmdb[item.TmdbID]
				if !secMovieFound {
					secondaryAction = "missing"
					// Note: SecondaryMissing is incremented at the per-movie
					// level (outside the per-group loop) since "missing" is a
					// movie-level property — the movie is either in secondary
					// or not, the group choice doesn't change that. Counting
					// here would inflate the total by the group count.
				} else {
					secTagID, secTagExists := secondaryTagToID[g.Tag]
					if secTagExists {
						for _, tid := range secMovie.Tags {
							if tid == secTagID {
								secondaryHasTag = true
								break
							}
						}
					}
					secondaryAction = composeAction(d.ShouldHave, secondaryHasTag)
					switch secondaryAction {
					case "add":
						ensureSet(secAddByTag, g.Tag)[secMovie.ID] = struct{}{}
						resp.Totals.SecondaryToAdd++
					case "remove":
						ensureSet(secRemoveByTag, g.Tag)[secMovie.ID] = struct{}{}
						resp.Totals.SecondaryToRemove++
					case "keep":
						resp.Totals.SecondaryToKeep++
					}
				}
			}

			// Always emit the per-decision detail. Apply mode used to skip
			// this for response-size; the per-movie list now renders in
			// both modes so callers (Tag library Apply, QFA) can drill in.
			itemDecisions = append(itemDecisions, scanDecision{
				GroupID:         g.ID,
				GroupTag:        g.Tag,
				GroupDisplay:    g.Display,
				ShouldHave:      d.ShouldHave,
				HasTag:          hasTagOnItem,
				Action:          action,
				Matched:         d.Matched,
				MatchLocation:   d.MatchLocation,
				Quality:         d.QualityResult,
				QualityDetail:   d.QualityDetail,
				Audio:           d.AudioResult,
				AudioDetail:     d.AudioDetail,
				Reason:          d.Reason,
				SecondaryAction: secondaryAction,
				SecondaryHasTag: secondaryHasTag,
			})
		}

		// Always emit decisions — both modes render the per-movie list now.
		// Apply mode used to skip this to keep the response small, but the
		// user wanted the same drill-in detail after QFA / direct apply.
		// MovieFile fields are nil-safe — empty when no-file (those
		// rows still emit a Decisions array now via the engine's
		// natural ShouldHave=false path).
		row := scanItem{
			ID:          item.ID,
			TmdbID:      item.TmdbID,
			Title:       item.Title,
			Year:        item.Year,
			CurrentTags: item.Tags,
			Decisions:   itemDecisions,
		}
		if hasFile {
			row.ReleaseGroup = item.MovieFile.ReleaseGroup
			row.SceneName = item.MovieFile.SceneName
			row.RelativePath = item.MovieFile.RelativePath
		}
		resp.Items = append(resp.Items, row)
	}

	// Orphan-cleanup pass (M3e). Bash tagarr.sh:1199-1260. Walks the
	// SECONDARY library and queues tag-removals for any (sec_movie, tag)
	// pair where primary either said "false" or didn't say anything at
	// all (movie isn't in primary's library). The "or unknown" branch is
	// the load-bearing one — it's how the user's "primary deletes →
	// secondary tag goes too" workflow actually works.
	//
	// Dedup against the per-movie pass: if a (sec_movie, tag) pair was
	// already queued for removal during decision-walk (primary explicitly
	// said "remove" and secondary had the tag), don't double-queue it.
	//
	// Orphan-cleanup is an inseparable part of sync — bash parity.
	// tagarr.conf.sample documents it as automatic when sync is on
	// ("Orphaned tags in secondary ... are cleaned up automatically").
	// Mirror semantics ARE: primary's view of every (movie, tag) pair
	// is the truth source; secondary aligns. There is no toggle to
	// skip this — adding one would diverge from bash behaviour and
	// leave secondary in an inconsistent state.
	if syncEnabled {
		for _, secItem := range secondaryItems {
			if secItem.TmdbID == 0 {
				continue
			}
			for _, g := range groups {
				secTagID, secTagExists := secondaryTagToID[g.Tag]
				if !secTagExists {
					continue
				}
				hasTag := false
				for _, tid := range secItem.Tags {
					if tid == secTagID {
						hasTag = true
						break
					}
				}
				if !hasTag {
					continue
				}
				primaryStatus := primaryTagStatus[fmt.Sprintf("%d:%s", secItem.TmdbID, g.Tag)]
				if primaryStatus == "true" {
					continue
				}
				existing := secRemoveByTag[g.Tag]
				if existing == nil {
					existing = make(map[int]struct{})
					secRemoveByTag[g.Tag] = existing
				}
				if _, already := existing[secItem.ID]; !already {
					existing[secItem.ID] = struct{}{}
					secOrphansRemoved++
					resp.Totals.SecondaryToRemove++
					resp.Totals.SecondaryOrphans++
				}
			}
		}
	}

	if req.Mode == "preview" {
		return &resp, nil
	}

	// Apply phase — lazy-create missing tags, then batch per (label, action).
	// Labels only need creation for the add side: removing a non-existent
	// tag is a no-op in Arr. (In theory the label-to-ID map for remove
	// is already populated because we only hit "remove" action when the
	// tag exists on the item, which requires the tag exists at all.)
	applied := scanApplied{}
	for label := range addByTag {
		if _, ok := labelToID[label]; ok {
			continue
		}
		created, err := client.CreateTag(ctx, label)
		if err != nil {
			return nil, newAPIError(502, fmt.Sprintf("create tag %q: %v", label, err))
		}
		labelToID[label] = created.ID
		applied.TagsCreated = append(applied.TagsCreated, label)
	}

	// Apply add batches.
	for label, ids := range addByTag {
		if len(ids) == 0 {
			continue
		}
		idList := setToSlice(ids)
		if err := client.EditorApplyTags(ctx, appType, idList, []int{labelToID[label]}, "add"); err != nil {
			return nil, newAPIError(502, fmt.Sprintf("apply add %q: %v", label, err))
		}
		applied.ItemsAdded += len(idList)
	}
	// Apply remove batches.
	for label, ids := range removeByTag {
		tid, ok := labelToID[label]
		if !ok || len(ids) == 0 {
			continue
		}
		idList := setToSlice(ids)
		if err := client.EditorApplyTags(ctx, appType, idList, []int{tid}, "remove"); err != nil {
			return nil, newAPIError(502, fmt.Sprintf("apply remove %q: %v", label, err))
		}
		applied.ItemsRemoved += len(idList)
	}

	// Secondary apply (M3e). Same lazy-create + add + remove pattern as
	// primary, scoped to the secondary instance's tag inventory and
	// targeting secondary movie IDs (not primary IDs — the per-movie
	// sync pass queued sec_movie.ID, not item.ID).
	if syncEnabled && (len(secAddByTag) > 0 || len(secRemoveByTag) > 0) {
		secApplied := &scanSecondaryApplied{
			InstanceID:     secondary.ID,
			InstanceName:   secondary.Name,
			OrphansRemoved: secOrphansRemoved,
		}
		for label := range secAddByTag {
			if _, ok := secondaryTagToID[label]; ok {
				continue
			}
			created, err := secondaryClient.CreateTag(ctx, label)
			if err != nil {
				return nil, newAPIError(502, fmt.Sprintf("secondary create tag %q: %v", label, err))
			}
			secondaryTagToID[label] = created.ID
			secApplied.TagsCreated = append(secApplied.TagsCreated, label)
		}
		for label, ids := range secAddByTag {
			if len(ids) == 0 {
				continue
			}
			idList := setToSlice(ids)
			if err := secondaryClient.EditorApplyTags(ctx, appType, idList, []int{secondaryTagToID[label]}, "add"); err != nil {
				return nil, newAPIError(502, fmt.Sprintf("secondary apply add %q: %v", label, err))
			}
			secApplied.ItemsAdded += len(idList)
		}
		for label, ids := range secRemoveByTag {
			tid, ok := secondaryTagToID[label]
			if !ok || len(ids) == 0 {
				continue
			}
			idList := setToSlice(ids)
			if err := secondaryClient.EditorApplyTags(ctx, appType, idList, []int{tid}, "remove"); err != nil {
				return nil, newAPIError(502, fmt.Sprintf("secondary apply remove %q: %v", label, err))
			}
			secApplied.ItemsRemoved += len(idList)
		}
		applied.Secondary = secApplied
	}

	// Tag-mode cleanup chain (M3-tag-cleanup). Bash CLEANUP_UNUSED_TAGS=true
	// parity. Runs as a tail-pass after the per-movie tag apply: counts
	// usage of every managed label (factoring in the just-applied deltas),
	// queues 0-count labels for deletion. Preview reports candidates without
	// touching Arr; apply does the deletes.
	//
	// SAFETY: same invariant as handleScanCleanup — managedLabels is the
	// upper bound on what cleanup can ever touch. Quality-profile,
	// custom-format, and manually-created tags in Radarr are never
	// iterated here.
	if req.CleanupUnusedTags {
		managedLabels := managedLabelsForType(cfg, appType)
		candidates := computeCleanupCandidates(items, labelToID, managedLabels, addByTag, removeByTag)
		resp.Totals.TagsToDelete = candidates

		if syncEnabled {
			secCandidates := computeCleanupCandidates(secondaryItems, secondaryTagToID, managedLabels, secAddByTag, secRemoveByTag)
			resp.Totals.SecondaryTagsToDelete = secCandidates
		}

		if req.Mode == "apply" {
			for _, c := range candidates {
				if err := client.DeleteTag(ctx, c.TagID); err != nil {
					return nil, newAPIError(502, fmt.Sprintf("cleanup delete tag %q: %v", c.Label, err))
				}
				applied.TagsDeleted = append(applied.TagsDeleted, c.Label)
			}
			if syncEnabled && applied.Secondary != nil {
				for _, c := range resp.Totals.SecondaryTagsToDelete {
					if err := secondaryClient.DeleteTag(ctx, c.TagID); err != nil {
						return nil, newAPIError(502, fmt.Sprintf("secondary cleanup delete tag %q: %v", c.Label, err))
					}
					applied.Secondary.TagsDeleted = append(applied.Secondary.TagsDeleted, c.Label)
				}
			} else if syncEnabled && len(resp.Totals.SecondaryTagsToDelete) > 0 {
				// applied.Secondary was nil because no add/remove batches ran
				// for the secondary, but cleanup-only deletions are still
				// meaningful. Allocate the report struct so the user sees what
				// happened on the secondary side.
				secApplied := &scanSecondaryApplied{
					InstanceID:   secondary.ID,
					InstanceName: secondary.Name,
				}
				for _, c := range resp.Totals.SecondaryTagsToDelete {
					if err := secondaryClient.DeleteTag(ctx, c.TagID); err != nil {
						return nil, newAPIError(502, fmt.Sprintf("secondary cleanup delete tag %q: %v", c.Label, err))
					}
					secApplied.TagsDeleted = append(secApplied.TagsDeleted, c.Label)
				}
				applied.Secondary = secApplied
			}
		}
	}

	resp.Applied = &applied
	return &resp, nil
}

// handleScanTag is the HTTP wrapper around runTag / runTagFilterOnly.
// Routes by req.TagSource: "filter-only" → runTagFilterOnly, anything
// else (empty / "active" / "discover") → the per-group runTag path.
// Validation of TagSource happened upstream in handleScanRun.
func (s *Server) handleScanTag(w http.ResponseWriter, r *http.Request, cfg core.Config, inst *core.Instance, appType string, filterCfg engine.FilterConfig, req scanRunRequest) {
	ctx, cancel := context.WithTimeout(r.Context(), scanTimeout)
	defer cancel()
	var (
		resp   *scanResponse
		apiErr *apiError
	)
	if req.TagSource == "filter-only" {
		resp, apiErr = s.runTagFilterOnly(ctx, cfg, inst, appType, filterCfg, req)
	} else {
		resp, apiErr = s.runTag(ctx, cfg, inst, appType, filterCfg, req)
	}
	if apiErr != nil {
		s.auditScan(req.auditSource(), "tag", inst, req, nil, apiErr.Message)
		writeAPIError(w, apiErr)
		return
	}
	s.auditScan(req.auditSource(), "tag", inst, req, resp, "")
	s.dumpScanJSON("tag", resp)
	writeJSON(w, resp)
}

// runTagFilterOnly is the filter-only tag-mode pipeline. Tags every
// movie passing the active quality + audio filter with one user-named
// tag — release group is ignored entirely. Replaces the broken
// "shared tag across multiple groups" pattern that flapped on every
// alternating run (different group's ShouldHave=false would queue a
// remove for the same shared tag the next run).
//
// Architecturally simpler than runTag: one tag, one decision per
// movie, no per-group iteration. Sync/orphan/apply machinery mirrors
// runTag's so secondary mirroring works identically — only the
// decision pass differs.
func (s *Server) runTagFilterOnly(ctx context.Context, cfg core.Config, inst *core.Instance, appType string, filterCfg engine.FilterConfig, req scanRunRequest) (*scanResponse, *apiError) {
	// Defense-in-depth tag-name validation. handleScanRun validates
	// FilterOnlyTag for live HTTP calls, but the schedule path calls
	// this function directly via scheduler_runner.go without going
	// through the dispatcher. Without this guard, a hand-edited config
	// surviving Config.Load could reach Arr's CreateTag with a malformed
	// label and surface as a confusing 502. Also normalises whitespace.
	tag := strings.TrimSpace(req.FilterOnlyTag)
	if tag == "" {
		return nil, newAPIError(400, "filterOnlyTag is required for tagSource=filter-only")
	}
	if !reTagName.MatchString(tag) {
		return nil, newAPIError(400, "filterOnlyTag must be lowercase letters, digits, underscores, or dashes")
	}
	// Conflict check: filter-only's tag must not collide with an
	// existing per-group rule's Tag (regardless of group's enabled
	// state, since disable→enable would re-introduce the conflict).
	// Symmetric with the API-level uniqueness check on /api/groups.
	for _, g := range cfg.ReleaseGroups {
		if g.Type == appType && strings.EqualFold(g.Tag, tag) {
			return nil, newAPIError(409, fmt.Sprintf("filterOnlyTag %q collides with an Active group rule (group: %q). Pick a different name or remove the conflicting group.", tag, g.Display))
		}
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

	resp := scanResponse{
		Mode:   req.Mode,
		Action: "tag",
		Instance: scanInstanceInfo{
			ID:   inst.ID,
			Name: inst.Name,
			Type: inst.Type,
		},
		Totals: scanTotals{
			Items:  len(items),
			Groups: 0, // filter-only has no group concept
		},
	}

	// Single-tag accumulators. Same shape as runTag's per-tag maps so
	// the apply phase can reuse the same lazy-create + batch-PUT path.
	addByTag := make(map[string]map[int]struct{})
	removeByTag := make(map[string]map[int]struct{})
	ensureSet := func(m map[string]map[int]struct{}, k string) map[int]struct{} {
		if m[k] == nil {
			m[k] = make(map[int]struct{})
		}
		return m[k]
	}

	// Secondary-sync setup — identical to runTag's. Mirroring works the
	// same way: filter passes/fails per movie on the secondary side
	// (since it walks its own files), and orphan-cleanup uses
	// primaryTagStatus[tmdbId:tag] keyed by the single filter-only tag.
	var (
		syncEnabled       bool
		secondary         *core.Instance
		secondaryClient   *arr.Client
		secondaryItems    []arr.Item
		secondaryByTmdb   map[int]arr.Item
		secondaryTagToID  map[string]int
		secAddByTag       map[string]map[int]struct{}
		secRemoveByTag    map[string]map[int]struct{}
		primaryTagStatus  map[string]string
		secOrphansRemoved int
	)
	if req.SyncToInstanceID != "" {
		for i := range cfg.Instances {
			if cfg.Instances[i].ID == req.SyncToInstanceID {
				secondary = &cfg.Instances[i]
				break
			}
		}
		if secondary == nil {
			return nil, newAPIError(404, "syncToInstanceId not found")
		}
		if secondary.Type != appType {
			return nil, newAPIError(400, "syncToInstanceId must be the same type as instanceId")
		}
		if secondary.ID == inst.ID {
			return nil, newAPIError(400, "syncToInstanceId cannot be the same as instanceId")
		}
		secondaryClient = s.arrClientFor(secondary)
		var secErr error
		secondaryItems, secErr = secondaryClient.ListItems(ctx, appType)
		if secErr != nil {
			return nil, newAPIError(502, "secondary list items: "+secErr.Error())
		}
		secTagDetails, secErr := secondaryClient.ListTagDetails(ctx)
		if secErr != nil {
			return nil, newAPIError(502, "secondary list tags: "+secErr.Error())
		}
		secondaryByTmdb = make(map[int]arr.Item, len(secondaryItems))
		for _, it := range secondaryItems {
			if it.TmdbID > 0 {
				secondaryByTmdb[it.TmdbID] = it
			}
		}
		secondaryTagToID = make(map[string]int, len(secTagDetails))
		for _, t := range secTagDetails {
			secondaryTagToID[t.Label] = t.ID
		}
		secAddByTag = make(map[string]map[int]struct{})
		secRemoveByTag = make(map[string]map[int]struct{})
		primaryTagStatus = make(map[string]string)
		syncEnabled = true
	}

	// Per-movie decision pass.
	for _, item := range items {
		hasFile := item.MovieFile != nil
		var combined string
		if hasFile {
			// Same combined-string construction as engine.DecideTag —
			// space-joined to prevent two fields' tokens from bleeding
			// into a false match (e.g. "MA" + "WEB" → "MAWEB").
			rel := strings.ToLower(item.MovieFile.RelativePath)
			scene := strings.ToLower(item.MovieFile.SceneName)
			rg := strings.ToLower(item.MovieFile.ReleaseGroup)
			combined = rel + " " + scene + " " + rg
		} else {
			resp.Totals.NoFile++
		}
		hasTagID := make(map[int]struct{}, len(item.Tags))
		for _, tid := range item.Tags {
			hasTagID[tid] = struct{}{}
		}

		// Per-movie missing-in-secondary count. Same semantics as runTag.
		if syncEnabled && item.TmdbID > 0 {
			if _, found := secondaryByTmdb[item.TmdbID]; !found {
				resp.Totals.SecondaryMissing++
			}
		}

		// Filter pass. Empty combined (no-file movie) → both filters
		// return false → ShouldHave=false → "remove" if tag present,
		// "skip" otherwise. Symmetric with runTag's no-file handling.
		shouldHave := false
		var qualityResult, qualityDetail, audioResult, audioDetail string
		if hasFile {
			qOK := engine.CheckQuality(filterCfg, combined)
			aOK := engine.CheckAudio(filterCfg, combined)
			if qOK {
				qualityResult = engine.ResultPass
				qualityDetail = engine.QualityDetailPass(combined)
			} else {
				qualityResult = engine.ResultFail
				qualityDetail = engine.QualityDetailFail(combined)
			}
			if aOK {
				audioResult = engine.ResultPass
				audioDetail = engine.AudioDetailPass(combined)
			} else {
				audioResult = engine.ResultFail
				audioDetail = engine.AudioDetailFail(combined)
			}
			shouldHave = qOK && aOK
		} else {
			qualityResult = engine.ResultNA
			audioResult = engine.ResultNA
		}

		currentID, existsInArr := labelToID[tag]
		hasTagOnItem := existsInArr
		if hasTagOnItem {
			_, hasTagOnItem = hasTagID[currentID]
		}
		action := composeAction(shouldHave, hasTagOnItem)

		switch action {
		case "add":
			ensureSet(addByTag, tag)[item.ID] = struct{}{}
			resp.Totals.ToAdd++
		case "remove":
			ensureSet(removeByTag, tag)[item.ID] = struct{}{}
			resp.Totals.ToRemove++
		case "keep":
			resp.Totals.ToKeep++
		}

		// Secondary mirror — same composeAction(shouldHave, secondaryHasTag).
		var secondaryAction string
		var secondaryHasTag bool
		if syncEnabled && item.TmdbID > 0 {
			primaryStatusKey := fmt.Sprintf("%d:%s", item.TmdbID, tag)
			if shouldHave {
				primaryTagStatus[primaryStatusKey] = "true"
			} else {
				primaryTagStatus[primaryStatusKey] = "false"
			}
			secMovie, secMovieFound := secondaryByTmdb[item.TmdbID]
			if !secMovieFound {
				secondaryAction = "missing"
			} else {
				secTagID, secTagExists := secondaryTagToID[tag]
				if secTagExists {
					for _, tid := range secMovie.Tags {
						if tid == secTagID {
							secondaryHasTag = true
							break
						}
					}
				}
				secondaryAction = composeAction(shouldHave, secondaryHasTag)
				switch secondaryAction {
				case "add":
					ensureSet(secAddByTag, tag)[secMovie.ID] = struct{}{}
					resp.Totals.SecondaryToAdd++
				case "remove":
					ensureSet(secRemoveByTag, tag)[secMovie.ID] = struct{}{}
					resp.Totals.SecondaryToRemove++
				case "keep":
					resp.Totals.SecondaryToKeep++
				}
			}
		}

		// Per-decision detail row. Filter-only emits ONE decision per
		// movie (no group fan-out). Reason follows the per-group format
		// so the existing UI drill-down renders without changes.
		var reason string
		if !shouldHave && hasFile {
			qOK := qualityResult == engine.ResultPass
			aOK := audioResult == engine.ResultPass
			switch {
			case !qOK && !aOK:
				reason = "Failed quality & audio"
			case !qOK:
				reason = "Failed quality"
			case !aOK:
				reason = "Failed audio"
			}
		}
		row := scanItem{
			ID:          item.ID,
			TmdbID:      item.TmdbID,
			Title:       item.Title,
			Year:        item.Year,
			CurrentTags: item.Tags,
			Decisions: []scanDecision{{
				GroupID:         "", // filter-only — no group identity
				GroupTag:        tag,
				GroupDisplay:    tag,
				ShouldHave:      shouldHave,
				HasTag:          hasTagOnItem,
				Action:          action,
				Matched:         shouldHave, // filter pass acts as the "match" verdict
				MatchLocation:   "",
				Quality:         qualityResult,
				QualityDetail:   qualityDetail,
				Audio:           audioResult,
				AudioDetail:     audioDetail,
				Reason:          reason,
				SecondaryAction: secondaryAction,
				SecondaryHasTag: secondaryHasTag,
			}},
		}
		if hasFile {
			row.ReleaseGroup = item.MovieFile.ReleaseGroup
			row.SceneName = item.MovieFile.SceneName
			row.RelativePath = item.MovieFile.RelativePath
		}
		resp.Items = append(resp.Items, row)
	}

	// Orphan-cleanup pass — identical mechanics to runTag, scoped to
	// the single filter-only tag. Walks secondary; removes the tag
	// from any sec-movie whose primary counterpart said "false" or
	// whose primary doesn't exist at all (movie deleted from primary).
	if syncEnabled {
		for _, secItem := range secondaryItems {
			if secItem.TmdbID == 0 {
				continue
			}
			secTagID, secTagExists := secondaryTagToID[tag]
			if !secTagExists {
				continue
			}
			hasTag := false
			for _, tid := range secItem.Tags {
				if tid == secTagID {
					hasTag = true
					break
				}
			}
			if !hasTag {
				continue
			}
			primaryStatus := primaryTagStatus[fmt.Sprintf("%d:%s", secItem.TmdbID, tag)]
			if primaryStatus == "true" {
				continue
			}
			existing := secRemoveByTag[tag]
			if existing == nil {
				existing = make(map[int]struct{})
				secRemoveByTag[tag] = existing
			}
			if _, already := existing[secItem.ID]; !already {
				existing[secItem.ID] = struct{}{}
				secOrphansRemoved++
				resp.Totals.SecondaryToRemove++
				resp.Totals.SecondaryOrphans++
			}
		}
	}

	if req.Mode == "preview" {
		return &resp, nil
	}

	// Apply phase — lazy-create + batch PUT. Same machinery runTag uses.
	applied := scanApplied{}
	if _, ok := labelToID[tag]; !ok && len(addByTag[tag]) > 0 {
		created, err := client.CreateTag(ctx, tag)
		if err != nil {
			return nil, newAPIError(502, fmt.Sprintf("create tag %q: %v", tag, err))
		}
		labelToID[tag] = created.ID
		applied.TagsCreated = append(applied.TagsCreated, tag)
	}
	if ids := addByTag[tag]; len(ids) > 0 {
		idList := setToSlice(ids)
		if err := client.EditorApplyTags(ctx, appType, idList, []int{labelToID[tag]}, "add"); err != nil {
			return nil, newAPIError(502, fmt.Sprintf("apply add %q: %v", tag, err))
		}
		applied.ItemsAdded += len(idList)
	}
	if ids := removeByTag[tag]; len(ids) > 0 {
		if tid, ok := labelToID[tag]; ok {
			idList := setToSlice(ids)
			if err := client.EditorApplyTags(ctx, appType, idList, []int{tid}, "remove"); err != nil {
				return nil, newAPIError(502, fmt.Sprintf("apply remove %q: %v", tag, err))
			}
			applied.ItemsRemoved += len(idList)
		}
	}

	// Secondary apply — same lazy-create + batch pattern, scoped to
	// the secondary instance's tag inventory and secondary movie IDs.
	if syncEnabled && (len(secAddByTag) > 0 || len(secRemoveByTag) > 0) {
		secApplied := &scanSecondaryApplied{
			InstanceID:     secondary.ID,
			InstanceName:   secondary.Name,
			OrphansRemoved: secOrphansRemoved,
		}
		if _, ok := secondaryTagToID[tag]; !ok && len(secAddByTag[tag]) > 0 {
			created, err := secondaryClient.CreateTag(ctx, tag)
			if err != nil {
				return nil, newAPIError(502, fmt.Sprintf("secondary create tag %q: %v", tag, err))
			}
			secondaryTagToID[tag] = created.ID
			secApplied.TagsCreated = append(secApplied.TagsCreated, tag)
		}
		if ids := secAddByTag[tag]; len(ids) > 0 {
			idList := setToSlice(ids)
			if err := secondaryClient.EditorApplyTags(ctx, appType, idList, []int{secondaryTagToID[tag]}, "add"); err != nil {
				return nil, newAPIError(502, fmt.Sprintf("secondary apply add %q: %v", tag, err))
			}
			secApplied.ItemsAdded += len(idList)
		}
		if ids := secRemoveByTag[tag]; len(ids) > 0 {
			if tid, ok := secondaryTagToID[tag]; ok {
				idList := setToSlice(ids)
				if err := secondaryClient.EditorApplyTags(ctx, appType, idList, []int{tid}, "remove"); err != nil {
					return nil, newAPIError(502, fmt.Sprintf("secondary apply remove %q: %v", tag, err))
				}
				secApplied.ItemsRemoved += len(idList)
			}
		}
		applied.Secondary = secApplied
	}

	// CleanupUnusedTags is a no-op in filter-only mode by design —
	// the filter-only tag is governed by exactly one rule. Either it's
	// in use (rule fires) or the user disabled the rule (tag lifecycle
	// is their choice via Settings → Tag inventory). No tail-pass.

	resp.Applied = &applied
	return &resp, nil
}
