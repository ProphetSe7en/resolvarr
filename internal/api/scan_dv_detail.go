package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"resolvarr/internal/core"
	"resolvarr/internal/core/dvdetect"
	"resolvarr/internal/core/engine"
)

// scan_dv_detail.go — dvdetail-mode handler (M4b). Walks the library,
// for every Radarr-says-DV file checks the dv-cache.json, runs ffmpeg
// + dovi_tool on cache miss, parses the RPU summary into engine.DvDetail,
// and emits the configured DV-detail tag set (mel/fel/dvprofile8/cm2/cm4)
// via engine.EmitDvDetailTags.
//
// Distinct from scan_extra_tags.go because:
//
//   - extra-tags reads pre-computed mediaInfo (microseconds per movie).
//     dv-detail shells out to ffmpeg + dovi_tool. Per-file cost is
//     fast on remux sources (tens of ms once the RPU SEI is found in
//     the first GOP), but the fork+exec + I/O overhead adds up across
//     hundreds of files. The cache + opt-in tools-install pattern
//     only matters here.
//   - The base "dv" / "no-dv" / "hdr10" / etc. labels belong to the
//     HDR bucket of extra-tags. dv-detail layers ONLY the additional
//     profile/layer/cm tags on top — never re-emits the base.
//   - DV detail can fail in ways extra-tags can't (ffmpeg error, RPU-
//     less file, missing tools). Per-row Status surfaces each outcome
//     so the UI badge can render "cached" / "extracted" / "no-rpu" /
//     "failed" / "skipped" / "tools-missing" without re-deriving from
//     counters.
//
// Strict contract: the engine owns every emit decision. The handler
// builds inputs (file path → RPU summary → engine.DvDetail) and routes
// outputs (desired vs current → add/remove/keep). No regex, no
// mediaInfo parsing live in this file.
//
// SAFETY invariant (mirrors scan_extra_tags.go): cleanup is bounded by
// engine.AllPossibleDvDetailTags(cfg) — labels outside that set are by
// definition not ours and stay untouched. Manual user tags, release-
// group tags, quality-profile tags are all invisible to this handler.

// runDvDetail is the headless dvdetail pipeline. Called by the HTTP
// wrapper (handleScanDvDetail), the M3d scheduler runner (single
// dvdetail and combined chains), and any future caller.
//
// Owns the global single-in-flight gate (dvScanMu + DvScanState) so
// every caller serializes the same way: cron-fired and adhoc-fired
// scans can never run concurrently against the same Radarr (would
// duplicate dovi_tool subprocess load + race tag writes). Returns
// 429 apiError when the slot is held; HTTP wrapper translates to a
// real 429, scheduler treats it as a phaseErr.
//
// Owns the per-scan context too — DvDetailScanTimeout wraps parentCtx
// so a stuck scan can't outlive its budget, and cancel() is registered
// on the slot so POST /api/scan/dvdetail/cancel propagates through to
// the runner's exec.CommandContext (kills ffmpeg/dovi_tool inside a
// second). All counter writes use the captured `st` local + dvScanMu
// — the bare s.DvScanState pointer is no longer read after the slot
// is acquired (avoids race-detector violations on the wrapper's clear).
func (s *Server) runDvDetail(parentCtx context.Context, cfg core.Config, inst *core.Instance, appType string, req scanRunRequest) (*scanResponse, *apiError) {
	engineCfg := core.DvDetailToEngine(cfg.DvDetail)
	if !engineCfg.Enabled {
		return nil, newAPIError(400, "DV detail is not enabled — turn it on in Library scan → DV detail")
	}

	// Tools-availability gate. Surface the install banner's actionable
	// guidance directly in the error so the UI can route the user to
	// the right place. dvToolsMu deliberately NOT taken — a Status
	// read is just a Stat + version invocation; we don't need to block
	// concurrent install/uninstall here. If tools disappear during the
	// scan, individual files surface as "tools-missing" rows.
	if s.DvTools.Dir == "" {
		return nil, newAPIError(400, "DV detail tools not configured — re-deploy with /config writeable")
	}
	toolsCtx, toolsCancel := context.WithTimeout(parentCtx, 5*time.Second)
	state := s.DvTools.Status(toolsCtx)
	toolsCancel()
	if !state.Installed {
		return nil, newAPIError(400, "DV detail tools not installed — set ENABLE_DV_TOOLS=true on the container and restart")
	}

	// Reserve the global single-in-flight slot. Wrap parentCtx with the
	// scan-wide timeout so cancel() propagates everywhere (Detect,
	// arr API calls, batch apply). cancel is registered on the slot
	// so handleDvScanCancel can flip the context from any goroutine.
	ctx, cancel := context.WithTimeout(parentCtx, DvDetailScanTimeout)
	st := &DvScanState{StartedAt: time.Now(), cancel: cancel}
	s.dvScanMu.Lock()
	if s.DvScanState != nil {
		s.dvScanMu.Unlock()
		cancel()
		return nil, newAPIError(429, "another DV detail scan is already running — cancel it first or wait for it to finish")
	}
	s.DvScanState = st
	s.dvScanMu.Unlock()
	defer func() {
		// Clear slot first, then cancel — order matters so a poll
		// landing right after clear sees no scan instead of a
		// zombie cancel.
		s.dvScanMu.Lock()
		s.DvScanState = nil
		s.dvScanMu.Unlock()
		cancel()
	}()

	// Safety bound: AllPossible (broad) when the user has opted into
	// orphan removal; Emittable (narrow) by default. Same pattern as
	// scan_extra_tags.go. Disabling the feature shouldn't strip tags
	// users already have unless they explicitly opt in.
	var managedTags map[string]string
	if cfg.DvDetail.RemoveOrphanedTags {
		managedTags = engine.AllPossibleDvDetailTags(engineCfg)
	} else {
		managedTags = engine.EmittableDvDetailTags(engineCfg)
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
	idToLabel := make(map[int]string, len(tagDetails))
	for _, t := range tagDetails {
		labelToID[t.Label] = t.ID
		idToLabel[t.ID] = t.Label
	}

	resp := scanResponse{
		Mode:   req.Mode,
		Action: "dvdetail",
		Instance: scanInstanceInfo{
			ID:   inst.ID,
			Name: inst.Name,
			Type: inst.Type,
		},
		Totals: scanTotals{Items: len(items)},
	}

	// Diff accumulators per label. Same pattern as scan_extra_tags.go —
	// dedupe by item ID, batch-apply at end.
	addByTag := make(map[string]map[int]struct{})
	removeByTag := make(map[string]map[int]struct{})
	ensureSet := func(m map[string]map[int]struct{}, k string) map[int]struct{} {
		if m[k] == nil {
			m[k] = make(map[int]struct{})
		}
		return m[k]
	}

	// Per-(action, tag) rollup count for the response summary.
	rollupCount := make(map[string]int)
	bumpRollup := func(action, tag string) { rollupCount[action+"|"+tag]++ }

	// Build the runner once — Resolve* picks the runtime location
	// (legacy /config/tools/ → $PATH). Empty string when neither
	// resolves; the runner itself surfaces "tools missing" per row
	// in that case.
	runner := dvdetect.Runner{
		DvBin: s.DvTools.ResolveDvBin(),
		FfBin: s.DvTools.ResolveFfBin(),
	}

	// Pre-pass: count DV candidates so the progress endpoint can report
	// a meaningful Total before any heavy work starts. The actual loop
	// re-walks; this is two passes over a 200-movie list, microseconds.
	total := 0
	for _, item := range items {
		if item.MovieFile == nil {
			continue
		}
		var t string
		if item.MovieFile.MediaInfo != nil {
			t = item.MovieFile.MediaInfo.VideoDynamicRangeType
		}
		if engine.HdrTypeIndicatesDv(t) {
			total++
		}
	}
	s.dvScanMu.Lock()
	st.Total = total
	s.dvScanMu.Unlock()

	for _, item := range items {
		// Cancel check — if the user clicked Cancel, the dvScanMu-stored
		// cancel func has been called and ctx.Err() returns non-nil.
		// Returns the partial response with the items processed so far;
		// scanResponse Mode stays as the request mode but Totals reflect
		// the actual progress.
		if err := ctx.Err(); err != nil {
			break
		}
		baseRow := scanItem{
			ID:          item.ID,
			TmdbID:      item.TmdbID,
			Title:       item.Title,
			Year:        item.Year,
			CurrentTags: item.Tags,
		}

		if item.MovieFile == nil {
			resp.Totals.NoFile++
			baseRow.DvStatus = "skipped"
			resp.Items = append(resp.Items, baseRow)
			continue
		}
		baseRow.ReleaseGroup = item.MovieFile.ReleaseGroup
		baseRow.SceneName = item.MovieFile.SceneName
		baseRow.RelativePath = item.MovieFile.RelativePath

		// Fast-path skip: not a DV candidate. The base "dv" / "no-dv"
		// label is the extra-tags HDR bucket's responsibility; we just
		// don't act on this movie.
		var hdrType string
		if item.MovieFile.MediaInfo != nil {
			hdrType = item.MovieFile.MediaInfo.VideoDynamicRangeType
		}
		if !engine.HdrTypeIndicatesDv(hdrType) {
			resp.Totals.DvNonCandidates++
			baseRow.DvStatus = "skipped"
			resp.Items = append(resp.Items, baseRow)
			continue
		}
		resp.Totals.DvCandidates++

		// Update progress before processing this candidate so the UI
		// can show "currently extracting Movie Title (132/487)" while
		// the ffmpeg+dovi_tool pipeline runs. Locked write —
		// the progress endpoint reads the same struct under-lock.
		s.dvScanMu.Lock()
		st.CurrentTitle = item.Title
		s.dvScanMu.Unlock()

		// Cache lookup: (movieFileId, size). nil cache → always miss.
		// req.BypassDvCache (per-scan checkbox in Run controls, or
		// per-rule JobOptions.BypassDvCache for saved rules) skips
		// both Get and Put — every file is re-extracted, nothing is
		// memoised. For users who don't trust (movieFileId, size) as
		// a sufficient cache key (e.g. re-encodes that produce
		// coincidentally-identical sizes, or after a dovi_tool upgrade
		// where they want a fresh extraction without using the Clear
		// cache button).
		var detail engine.DvDetail
		var foundRPU bool
		var status string
		var reason string

		movieFileID := item.MovieFile.ID
		size := item.MovieFile.Size
		var hit bool
		var entry dvdetect.Entry
		if !req.BypassDvCache {
			entry, hit = s.DvCache.Get(movieFileID, size)
		}
		if hit {
			detail = entry.Detail
			foundRPU = entry.Found
			status = "cached"
			resp.Totals.DvCacheHits++
			s.dvScanMu.Lock()
			st.CacheHits++
			st.Processed++
			s.dvScanMu.Unlock()
		} else {
			// Cache miss → translate path + run the extraction pipeline.
			containerPath := inst.TranslatePath(item.MovieFile.Path)
			if containerPath == "" {
				status = "failed"
				reason = "Radarr returned no path for this movie file"
				resp.Totals.DvExtractFailed++
			} else if _, statErr := os.Stat(containerPath); statErr != nil {
				status = "failed"
				reason = "media file unreachable: " + containerPath + " — check path mappings"
				resp.Totals.DvFileUnreachable++
			} else {
				d, ok, runErr := runner.Detect(ctx, containerPath)
				switch {
				case errors.Is(runErr, dvdetect.ErrToolsMissing):
					status = "tools-missing"
					reason = runErr.Error()
					resp.Totals.DvToolsMissing++
				case runErr != nil:
					status = "failed"
					reason = runErr.Error()
					resp.Totals.DvExtractFailed++
				case !ok:
					// Extraction succeeded; file had no RPU.
					detail = d
					foundRPU = false
					status = "no-rpu"
					resp.Totals.DvExtracted++
					resp.Totals.DvExtractedNoRpu++
					// Cache the negative result so we don't re-run
					// the extraction every scan for the same file.
					if !req.BypassDvCache {
						s.DvCache.Put(movieFileID, size, d, false)
					}
				default:
					detail = d
					foundRPU = true
					status = "extracted"
					resp.Totals.DvExtracted++
					if !req.BypassDvCache {
						s.DvCache.Put(movieFileID, size, d, true)
					}
				}
			}
			// Slow-path candidate finished — bump processed regardless
			// of outcome (extract OK, no RPU, failed, tools missing).
			// Extracted counter only bumps when we actually ran tools
			// (status != "failed" && != "tools-missing"); failed counter
			// catches the rest so totals reconcile.
			s.dvScanMu.Lock()
			st.Processed++
			if status == "extracted" || status == "no-rpu" {
				st.Extracted++
			}
			if status == "failed" || status == "tools-missing" {
				st.Failed++
			}
			s.dvScanMu.Unlock()
		}

		baseRow.DvStatus = status

		// Skip diff phase for non-emit statuses. The row still goes into
		// the response so the UI can render it; just no add/remove/keep.
		// One synthetic decision carries the failure reason for the
		// drill-down — empty Tag/Action signal "this is a status-only
		// row" to the renderer. DvDetail stays nil on failure paths so
		// the UI doesn't render an all-zero "facts" block; only set it
		// for cached / extracted / no-rpu where the parsed RPU result
		// (even zero-valued) is meaningful.
		if status == "failed" || status == "tools-missing" {
			baseRow.DvDecisions = []scanDvDetailDecision{{
				Status: status,
				Reason: reason,
			}}
			resp.Items = append(resp.Items, baseRow)
			continue
		}
		baseRow.DvDetail = &scanDvDetailFacts{
			Profile:   detail.Profile,
			Layer:     detail.Layer,
			CMVersion: detail.CMVersion,
		}

		// Build the desired set. foundRPU=false produces no detail tags
		// — the extra-tags HDR bucket emits "no-dv" for that case.
		var desired []string
		if foundRPU {
			desired = engine.EmitDvDetailTags(detail, engineCfg)
		}
		desiredSet := make(map[string]struct{}, len(desired))
		for _, tag := range desired {
			desiredSet[tag] = struct{}{}
		}

		// Current managed-DV-detail labels currently on the movie:
		// labels in our managedTags map AND on item.Tags.
		currentManaged := make(map[string]struct{})
		for _, tid := range item.Tags {
			label, ok := idToLabel[tid]
			if !ok {
				continue
			}
			if _, isManaged := managedTags[label]; !isManaged {
				continue
			}
			currentManaged[label] = struct{}{}
		}

		var rowDecisions []scanDvDetailDecision
		// ADD / KEEP — walk desired in engine emit order for stable display.
		for _, tag := range desired {
			if _, alreadyOn := currentManaged[tag]; alreadyOn {
				rowDecisions = append(rowDecisions, scanDvDetailDecision{
					Tag: tag, Action: "keep", Status: status,
				})
				resp.Totals.ToKeep++
				bumpRollup("keep", tag)
				continue
			}
			ensureSet(addByTag, tag)[item.ID] = struct{}{}
			rowDecisions = append(rowDecisions, scanDvDetailDecision{
				Tag: tag, Action: "add", Status: status,
			})
			resp.Totals.ToAdd++
			bumpRollup("add", tag)
		}
		// REMOVE — sort the leftover currentManaged so consecutive runs
		// produce identical orderings (helps schedule history diffs).
		removeList := make([]string, 0, len(currentManaged))
		for label := range currentManaged {
			if _, stillDesired := desiredSet[label]; stillDesired {
				continue
			}
			removeList = append(removeList, label)
		}
		sort.Strings(removeList)
		for _, label := range removeList {
			ensureSet(removeByTag, label)[item.ID] = struct{}{}
			rowDecisions = append(rowDecisions, scanDvDetailDecision{
				Tag: label, Action: "remove", Status: status,
			})
			resp.Totals.ToRemove++
			bumpRollup("remove", label)
		}

		baseRow.DvDecisions = rowDecisions
		resp.Items = append(resp.Items, baseRow)

		// Per-row audit line so a tail of runs.log shows each candidate
		// the engine processed, with the same fields users grep for in
		// the bash dv_hdr_manual_tagging.sh log (status, profile, layer,
		// CM, tag decisions). Lets the cross-comparison test pin
		// individual differences without having to open the JSON dump.
		if s.App != nil && s.App.RunLog != nil {
			adds := dvTagListByAction(rowDecisions, "add")
			rems := dvTagListByAction(rowDecisions, "remove")
			keeps := dvTagListByAction(rowDecisions, "keep")
			fields := []string{
				"id=" + itoa(item.ID),
				"title=" + kvEscape(item.Title),
				"file=" + kvEscape(baseRow.RelativePath),
				"status=" + status,
			}
			if baseRow.DvDetail != nil {
				if baseRow.DvDetail.Profile != 0 {
					fields = append(fields, "profile="+itoa(baseRow.DvDetail.Profile))
				}
				if baseRow.DvDetail.Layer != "" {
					fields = append(fields, "layer="+baseRow.DvDetail.Layer)
				}
				if baseRow.DvDetail.CMVersion != 0 {
					fields = append(fields, "cm="+itoa(baseRow.DvDetail.CMVersion))
				}
			}
			fields = append(fields,
				"add="+kvEscape(strings.Join(adds, ",")),
				"remove="+kvEscape(strings.Join(rems, ",")),
				"keep="+kvEscape(strings.Join(keeps, ",")),
			)
			s.App.RunLog.Audit("scan-dvdetail-row", "", fields...)
		}
	}


	// Flatten rollup counts. Sort key is (action ASC, tag ASC) so the
	// "add" rows appear before "keep" / "remove" — matches the
	// scan_extra_tags.go convention so the UI table is consistent.
	resp.Totals.DvDetailRollups = flattenDvDetailRollup(rollupCount)

	// Persist cache best-effort. Failure to write the cache shouldn't
	// abort the scan — extraction work is already done in memory; the
	// next run pays the re-extraction cost. Same logging-only fallback
	// as the migration save.
	if s.DvCache != nil {
		if err := s.DvCache.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "tagarr: dv cache save failed: %v\n", err)
		}
	}

	if req.Mode == "preview" {
		return &resp, nil
	}

	// Apply phase. Same partial-failure semantics as scan_extra_tags.go:
	// first error stops the apply and returns 502. Re-run reconciles
	// since both legs are idempotent per (movie, tag).
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
	resp.Applied = &applied
	return &resp, nil
}

// handleScanDvDetail is the HTTP wrapper around runDvDetail.
// Slot reservation + cancel-registration + per-scan timeout all
// live inside runDvDetail itself so adhoc, cron-fired, and
// chain-fired DV scans share the same single-in-flight gate. A
// 429 from runDvDetail surfaces here as a 429 + Retry-After.
func (s *Server) handleScanDvDetail(w http.ResponseWriter, r *http.Request, cfg core.Config, inst *core.Instance, appType string, req scanRunRequest) {
	resp, apiErr := s.runDvDetail(r.Context(), cfg, inst, appType, req)
	if apiErr != nil {
		if apiErr.Status == 429 {
			w.Header().Set("Retry-After", "60")
		}
		s.auditScan(req.auditSource(), "dvdetail", inst, req, nil, apiErr.Message)
		writeAPIError(w, apiErr)
		return
	}
	// Persist the full scanResponse for adhoc inspection. See
	// scan_audit.go for the generic dumpScanJSON helper used by
	// every scan handler.
	if path := s.dumpScanJSON("dvdetail", resp); path != "" {
		if s.App != nil && s.App.RunLog != nil {
			s.App.RunLog.Audit("dvtools", "scan dump", "file="+path, "items="+itoa(len(resp.Items)))
		}
	}
	s.auditScan(req.auditSource(), "dvdetail", inst, req, resp, "")
	writeJSON(w, resp)
}

// handleDvScanProgress returns the in-flight scan state, or {running:false}
// when no scan is active. Polled by the UI ~every second to drive the
// progress bar + current-file label.
func (s *Server) handleDvScanProgress(w http.ResponseWriter, r *http.Request) {
	s.dvScanMu.Lock()
	st := s.DvScanState
	s.dvScanMu.Unlock()
	if st == nil {
		writeJSON(w, map[string]any{"running": false})
		return
	}
	// Snapshot fields under-lock just before we read — fields are
	// updated by the loop under dvScanMu so reading without the lock
	// would race. We took the lock above; release after copying.
	s.dvScanMu.Lock()
	out := map[string]any{
		"running":      true,
		"startedAt":    st.StartedAt,
		"total":        st.Total,
		"processed":    st.Processed,
		"extracted":    st.Extracted,
		"cacheHits":    st.CacheHits,
		"failed":       st.Failed,
		"currentTitle": st.CurrentTitle,
	}
	s.dvScanMu.Unlock()
	writeJSON(w, out)
}

// handleDvScanCancel flips the active scan's context cancel — the
// loop checks ctx.Err() each iteration and exits cleanly with the
// items processed so far. No-op (200) when nothing is running so the
// UI can fire the cancel without first checking state.
func (s *Server) handleDvScanCancel(w http.ResponseWriter, r *http.Request) {
	s.dvScanMu.Lock()
	st := s.DvScanState
	s.dvScanMu.Unlock()
	if st != nil && st.cancel != nil {
		st.cancel()
		if s.App != nil && s.App.RunLog != nil {
			s.App.RunLog.Audit("dvtools", "scan cancel", "processed="+itoa(st.Processed), "total="+itoa(st.Total))
		}
	}
	w.WriteHeader(http.StatusOK)
}

// dvTagListByAction collects tag names from the per-row decision list
// that match the given action. Used by the per-row audit-log line so
// the runs.log entry mirrors the comma-separated "add=mel,cm4" shape
// users would otherwise have to derive by parsing the response JSON.
func dvTagListByAction(decisions []scanDvDetailDecision, action string) []string {
	var out []string
	for _, d := range decisions {
		if d.Action == action {
			out = append(out, d.Tag)
		}
	}
	return out
}

// flattenDvDetailRollup turns the action|tag → count map into a
// sorted slice. Sort key is (action ASC, tag ASC) so "add" rows
// come before "keep" / "remove" — matches scan_extra_tags.go's
// flattenBucketCount convention so the UI doesn't branch.
func flattenDvDetailRollup(m map[string]int) []scanDvDetailRollup {
	out := make([]scanDvDetailRollup, 0, len(m))
	for k, v := range m {
		// Tag values never contain | per engine vocab (^[a-z0-9-]+$
		// enforced) so SplitN(_, 2) is safe — first | always splits
		// action from tag.
		parts := strings.SplitN(k, "|", 2)
		if len(parts) != 2 {
			continue
		}
		out = append(out, scanDvDetailRollup{Action: parts[0], Tag: parts[1], Count: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Action != out[j].Action {
			return out[i].Action < out[j].Action
		}
		return out[i].Tag < out[j].Tag
	})
	return out
}

// DvDetailScanTimeout is wider than scanTimeout because dvdetail can
// be genuinely slow on a fresh cache. Reference math:
//
//   - 1000-movie library, 30% DV candidates, 2s per extraction
//     = 600s = 10 min.
//   - 2000-movie library, 50% DV, 3s per extraction
//     = 3000s = 50 min — exceeds this 30-min ceiling.
//
// Subsequent runs hit the cache and complete in seconds. The first
// run on a large all-DV library WILL hit the timeout and need to
// be re-kicked (cache picks up where it left off, since each Put
// persists when Save() runs at end). Worker-pool parallelism is a
// follow-up task tracked in CLAUDE.md if real-world feedback shows
// the serial ceiling biting users.
//
// Exported because the scheduler invokes runDvDetail directly and
// reuses this constant rather than picking its own divergent
// timeout.
const DvDetailScanTimeout = 30 * time.Minute
