package api

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"resolvarr/internal/core"
	"resolvarr/internal/core/engine"
)

// scan_auto_tags.go — audiotags + videotags handlers (M4 split).
// Both scans share most of their structure: walk the library, read
// each movieFile's mediaInfo, call the appropriate engine.* helper
// to derive the desired tag set, diff against the movie's currently-
// applied managed labels, batch the add/remove deltas through Arr's
// editor endpoint.
//
// Distinct handlers (action="audiotags" vs "videotags") because:
//
//   - User-facing UX splits them into two sub-tabs by stream type.
//   - Their results land on different UI sub-tabs / result cards.
//   - Per-section RemoveOrphanedTags toggles work cleanly when the
//     handlers are separate.
//
// Strict contract: the engine owns every emit decision. Handlers
// build inputs (arr.MediaInfo → engine.MediaInfo) and route outputs
// (desired vs current → add/remove/keep).
//
// SAFETY invariant (mirrors scan_dv_detail.go): cleanup is bounded
// by engine.AllPossible*Tags(cfg). Labels outside that set are by
// definition not ours and stay untouched.

// runAudioTags is the headless audiotags pipeline.
func (s *Server) runAudioTags(ctx context.Context, cfg core.Config, inst *core.Instance, appType string, req scanRunRequest) (*scanResponse, *apiError) {
	if appType == "sonarr" {
		return s.runAudioTagsSonarr(ctx, cfg, inst, req)
	}
	engineCfg := core.AudioTagsToEngine(cfg.AudioTags)
	if !engineCfg.Audio.Enabled {
		return nil, newAPIError(400, "Audio tags is disabled — enable the Audio bucket in Library scan → Audio tags")
	}
	var managed map[string]string
	if cfg.AudioTags.RemoveOrphanedTags {
		managed = engine.AllPossibleAudioTags(engineCfg)
	} else {
		managed = engine.EmittableAudioTags(engineCfg)
	}
	desiredFn := func(mi engine.MediaInfo, _ int) []string {
		return engine.AudioTagsForFile(mi, engineCfg)
	}
	missingMediaInfoFn := func(mi engine.MediaInfo, qualityRes int) bool {
		// Audio: missing means audioCodec + audioChannels both empty
		// (no audio fields populated in mediaInfo). qualityResolution
		// fallback is irrelevant here (video-only).
		return mi.AudioCodec == "" && mi.AudioChannels == 0
	}
	return s.runAutoTags(ctx, cfg, inst, appType, req, "audiotags", managed, desiredFn, missingMediaInfoFn)
}

// runVideoTags is the headless videotags pipeline.
func (s *Server) runVideoTags(ctx context.Context, cfg core.Config, inst *core.Instance, appType string, req scanRunRequest) (*scanResponse, *apiError) {
	if appType == "sonarr" {
		return s.runVideoTagsSonarr(ctx, cfg, inst, req)
	}
	engineCfg := core.VideoTagsToEngine(cfg.VideoTags)
	if !engineCfg.Resolution.Enabled && !engineCfg.Codec.Enabled && !engineCfg.HDR.Enabled {
		return nil, newAPIError(400, "no video buckets enabled — enable Resolution, Codec, or HDR in Library scan → Video tags")
	}
	var managed map[string]string
	if cfg.VideoTags.RemoveOrphanedTags {
		managed = engine.AllPossibleVideoTags(engineCfg)
	} else {
		managed = engine.EmittableVideoTags(engineCfg)
	}
	desiredFn := func(mi engine.MediaInfo, qualityRes int) []string {
		return engine.VideoTagsForFile(mi, qualityRes, engineCfg)
	}
	missingMediaInfoFn := func(mi engine.MediaInfo, qualityRes int) bool {
		// Video: missing if mediaInfo Height + qualityResolution both
		// 0 (no fallback) AND no other video field populated. Same
		// "legacy import surfaced for re-probe" surface as the old
		// extra-tags handler had.
		return mi.Height == 0 && qualityRes == 0 && mi.VideoCodec == "" && mi.VideoDynamicRangeType == ""
	}
	return s.runAutoTags(ctx, cfg, inst, appType, req, "videotags", managed, desiredFn, missingMediaInfoFn)
}

// runAutoTags is the shared diff loop. Both audio and video handlers
// call this with their own engine + managed-label set + per-file
// desired-tag function + missing-mediaInfo predicate.
func (s *Server) runAutoTags(
	ctx context.Context, cfg core.Config, inst *core.Instance, appType string,
	req scanRunRequest, action string,
	managedTags map[string]string,
	desiredFn func(mi engine.MediaInfo, qualityRes int) []string,
	missingMediaInfoFn func(mi engine.MediaInfo, qualityRes int) bool,
) (*scanResponse, *apiError) {
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
		Action: action,
		Instance: scanInstanceInfo{
			ID:   inst.ID,
			Name: inst.Name,
			Type: inst.Type,
		},
		Totals: scanTotals{Items: len(items)},
	}

	addByTag := make(map[string]map[int]struct{})
	removeByTag := make(map[string]map[int]struct{})
	ensureSet := func(m map[string]map[int]struct{}, k string) map[int]struct{} {
		if m[k] == nil {
			m[k] = make(map[int]struct{})
		}
		return m[k]
	}
	rollupCount := make(map[string]int)
	bumpRollup := func(action, bucket, tag string) {
		rollupCount[action+"|"+bucket+"|"+tag]++
	}

	for _, item := range items {
		if item.MovieFile == nil {
			resp.Totals.NoFile++
			resp.Items = append(resp.Items, scanItem{
				ID:          item.ID,
				TmdbID:      item.TmdbID,
				Title:       item.Title,
				Year:        item.Year,
				CurrentTags: item.Tags,
			})
			continue
		}

		var mi engine.MediaInfo
		if item.MovieFile.MediaInfo != nil {
			mi = engine.MediaInfo{
				Width:                   item.MovieFile.MediaInfo.Width,
				VideoResolution:         item.MovieFile.MediaInfo.VideoResolution,
				Height:                  item.MovieFile.MediaInfo.Height,
				VideoCodec:              item.MovieFile.MediaInfo.VideoCodec,
				VideoBitDepth:           item.MovieFile.MediaInfo.VideoBitDepth,
				VideoDynamicRangeType:   item.MovieFile.MediaInfo.VideoDynamicRangeType,
				AudioCodec:              item.MovieFile.MediaInfo.AudioCodec,
				AudioChannels:           item.MovieFile.MediaInfo.AudioChannels,
				AudioAdditionalFeatures: item.MovieFile.MediaInfo.AudioAdditionalFeatures,
			}
		}
		// Always carry filename context — hasAtmos uses it to fall back
		// when audioAdditionalFeatures is blank (older Radarr imports +
		// Atmos-in-EAC3 streams sometimes leave the field empty).
		mi.RelativePath = item.MovieFile.RelativePath
		mi.SceneName = item.MovieFile.SceneName
		var qualityRes int
		if item.MovieFile.Quality != nil {
			qualityRes = item.MovieFile.Quality.Quality.Resolution
		}

		desired := desiredFn(mi, qualityRes)

		if missingMediaInfoFn(mi, qualityRes) {
			resp.Totals.MissingMediaInfo++
		}

		desiredSet := make(map[string]struct{}, len(desired))
		for _, tag := range desired {
			desiredSet[tag] = struct{}{}
		}

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

		var itemDecisions []scanAutoTagDecision

		for _, tag := range desired {
			bucket := managedTags[tag]
			if _, alreadyOn := currentManaged[tag]; alreadyOn {
				itemDecisions = append(itemDecisions, scanAutoTagDecision{
					Bucket: bucket, Tag: tag, Action: "keep",
				})
				resp.Totals.ToKeep++
				bumpRollup("keep", bucket, tag)
				continue
			}
			ensureSet(addByTag, tag)[item.ID] = struct{}{}
			itemDecisions = append(itemDecisions, scanAutoTagDecision{
				Bucket: bucket, Tag: tag, Action: "add",
			})
			resp.Totals.ToAdd++
			bumpRollup("add", bucket, tag)
		}

		removeList := make([]string, 0, len(currentManaged))
		for label := range currentManaged {
			if _, stillDesired := desiredSet[label]; stillDesired {
				continue
			}
			removeList = append(removeList, label)
		}
		sort.Strings(removeList)
		for _, label := range removeList {
			bucket := managedTags[label]
			ensureSet(removeByTag, label)[item.ID] = struct{}{}
			itemDecisions = append(itemDecisions, scanAutoTagDecision{
				Bucket: bucket, Tag: label, Action: "remove",
			})
			resp.Totals.ToRemove++
			bumpRollup("remove", bucket, label)
		}

		resp.Items = append(resp.Items, scanItem{
			ID:            item.ID,
			TmdbID:        item.TmdbID,
			Title:         item.Title,
			Year:          item.Year,
			CurrentTags:   item.Tags,
			ReleaseGroup:  item.MovieFile.ReleaseGroup,
			SceneName:     item.MovieFile.SceneName,
			RelativePath:  item.MovieFile.RelativePath,
			AutoDecisions: itemDecisions,
		})
	}

	resp.Totals.AutoTagRollups = flattenAutoTagRollup(rollupCount)

	if req.Mode == "preview" {
		return &resp, nil
	}

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

// handleScanAudioTags is the HTTP wrapper around runAudioTags.
func (s *Server) handleScanAudioTags(w http.ResponseWriter, r *http.Request, cfg core.Config, inst *core.Instance, appType string, req scanRunRequest) {
	ctx, cancel := context.WithTimeout(r.Context(), scanTimeout)
	defer cancel()
	resp, apiErr := s.runAudioTags(ctx, cfg, inst, appType, req)
	if apiErr != nil {
		s.auditScan(req.auditSource(), "audiotags", inst, req, nil, apiErr.Message)
		writeAPIError(w, apiErr)
		return
	}
	s.auditScan(req.auditSource(), "audiotags", inst, req, resp, "")
	s.dumpScanJSON("audiotags", resp)
	writeJSON(w, resp)
}

// handleScanVideoTags is the HTTP wrapper around runVideoTags.
func (s *Server) handleScanVideoTags(w http.ResponseWriter, r *http.Request, cfg core.Config, inst *core.Instance, appType string, req scanRunRequest) {
	ctx, cancel := context.WithTimeout(r.Context(), scanTimeout)
	defer cancel()
	resp, apiErr := s.runVideoTags(ctx, cfg, inst, appType, req)
	if apiErr != nil {
		s.auditScan(req.auditSource(), "videotags", inst, req, nil, apiErr.Message)
		writeAPIError(w, apiErr)
		return
	}
	s.auditScan(req.auditSource(), "videotags", inst, req, resp, "")
	s.dumpScanJSON("videotags", resp)
	writeJSON(w, resp)
}

// flattenAutoTagRollup turns the action|bucket|tag → count map into a
// sorted slice. Sort key (action ASC, bucket ASC, tag ASC) so "add"
// rows appear before "keep" / "remove" — matches scan_dv_detail.go's
// flatten convention so the UI doesn't branch.
func flattenAutoTagRollup(m map[string]int) []scanAutoTagRollup {
	out := make([]scanAutoTagRollup, 0, len(m))
	for k, v := range m {
		parts := strings.SplitN(k, "|", 3)
		if len(parts) != 3 {
			continue
		}
		out = append(out, scanAutoTagRollup{
			Action: parts[0],
			Bucket: parts[1],
			Tag:    parts[2],
			Count:  v,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Action != out[j].Action {
			return out[i].Action < out[j].Action
		}
		if out[i].Bucket != out[j].Bucket {
			return out[i].Bucket < out[j].Bucket
		}
		return out[i].Tag < out[j].Tag
	})
	return out
}
