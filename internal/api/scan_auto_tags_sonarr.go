package api

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"resolvarr/internal/arr"
	"resolvarr/internal/core"
	"resolvarr/internal/core/engine"
)

// scan_auto_tags_sonarr.go — Sonarr equivalent of scan_auto_tags.go's
// runAudioTags / runVideoTags. The Sonarr flow differs from Radarr in
// three load-bearing ways:
//
//   1. Identity: items in the response represent SERIES, not movies.
//      One series with 200 episodes shows up as ONE row whose drill-
//      in (Episodes []) lists the per-file mediaInfo + the per-file
//      tag contribution that fed the series-level aggregate.
//
//   2. Fan-out: Sonarr's tag model is series-level only — there is no
//      season.tags or episode.tags. We aggregate per-bucket using
//      AggregateForSeries (strategy chosen per-bucket via
//      SonarrAggregation: all-occurring / strict / highest), then
//      diff against series.Tags via /api/v3/series/editor add+remove.
//
//   3. N+1 API shape: ListItems returns the whole series catalogue in
//      one call but mediaInfo lives on per-series episodefiles. We
//      worker-pool the /api/v3/episodefile?seriesId=N calls at
//      concurrency=5 with a 30 s sub-context per call so a single slow
//      series can't stall the whole scan. The semaphore acquire is
//      ctx-aware so user-fired Cancel from the QFA chain unwinds
//      promptly instead of waiting for in-flight goroutines to drain.
//
// Empty series (Statistics.EpisodeFileCount == 0) are skipped before
// firing the per-series fetch — same pattern onedr0p home-ops uses in
// tag-resolution.sh, saves N requests against an empty library.
//
// Engine layer is shared with Radarr — AudioTagsForFile / VideoTagsForFile
// + AggregateAudioForSeries / AggregateVideoForSeries are the same
// helpers for both Arr types. The handler boundary is where the
// per-Arr fan-out logic lives.

// sonarrEpisodeWorkerCount caps concurrent ListEpisodefiles calls.
// 5 is a fresh choice for this handler — Sonarr-Recover today is
// serial (see scan_recover_sonarr.go's per-series for-loop) and could
// be worker-pooled in a follow-up. Bump cautiously: Sonarr's API is
// single-threaded internally and >10 concurrent reads on a slow disk-
// backed library can stall.
const sonarrEpisodeWorkerCount = 5

// sonarrEpisodefilesTimeout is the per-series fetch sub-context.
// ListEpisodefiles is a single GET against Sonarr's local DB; even a
// very large series (1000+ episodes) returns in < 5s on a healthy
// Sonarr. 30 s is generous — anything past it indicates a real
// problem (Sonarr unresponsive, disk wedged) and we'd rather record
// a per-series error than hang the whole scan.
const sonarrEpisodefilesTimeout = 30 * time.Second

// runAudioTagsSonarr is the Sonarr audio pipeline.
func (s *Server) runAudioTagsSonarr(ctx context.Context, cfg core.Config, inst *core.Instance, req scanRunRequest) (*scanResponse, *apiError) {
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
	aggregateFn := func(eps []engine.EpisodeInput) []string {
		return engine.AggregateAudioForSeries(eps, engineCfg)
	}
	perEpisodeFn := func(mi engine.MediaInfo, _ int) []string {
		return engine.AudioTagsForFile(mi, engineCfg)
	}
	missingFn := func(mi engine.MediaInfo, _ int) bool {
		// Audio: missing means audioCodec + audioChannels both empty.
		// qualityResolution doesn't apply (video-only fallback).
		return mi.AudioCodec == "" && mi.AudioChannels == 0
	}
	return s.runAutoTagsSonarr(ctx, inst, req, "audiotags", managed, aggregateFn, perEpisodeFn, missingFn)
}

// runVideoTagsSonarr is the Sonarr video pipeline.
func (s *Server) runVideoTagsSonarr(ctx context.Context, cfg core.Config, inst *core.Instance, req scanRunRequest) (*scanResponse, *apiError) {
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
	aggregateFn := func(eps []engine.EpisodeInput) []string {
		return engine.AggregateVideoForSeries(eps, engineCfg)
	}
	perEpisodeFn := func(mi engine.MediaInfo, qr int) []string {
		return engine.VideoTagsForFile(mi, qr, engineCfg)
	}
	missingFn := func(mi engine.MediaInfo, qr int) bool {
		// Video: missing if mediaInfo.Height + qualityResolution both
		// 0 AND no other video field is populated. Same predicate
		// Radarr's video handler uses (scan_auto_tags.go:75-81).
		return mi.Height == 0 && qr == 0 && mi.VideoCodec == "" && mi.VideoDynamicRangeType == ""
	}
	return s.runAutoTagsSonarr(ctx, inst, req, "videotags", managed, aggregateFn, perEpisodeFn, missingFn)
}

// runAutoTagsSonarr is the shared Sonarr-side diff loop. Both audio
// and video handlers funnel through here with their bucket-managed
// label set + aggregator + per-episode emitter. The shape mirrors
// runAutoTags but iterates series → episodefiles instead of items →
// movieFile and applies tags at series level.
func (s *Server) runAutoTagsSonarr(
	ctx context.Context, inst *core.Instance, req scanRunRequest, action string,
	managedTags map[string]string,
	aggregateFn func(eps []engine.EpisodeInput) []string,
	perEpisodeFn func(mi engine.MediaInfo, qualityRes int) []string,
	missingMediaInfoFn func(mi engine.MediaInfo, qualityRes int) bool,
) (*scanResponse, *apiError) {
	client := s.arrClientFor(inst)
	series, err := client.ListItems(ctx, "sonarr")
	if err != nil {
		return nil, newAPIError(502, "arr list series: "+err.Error())
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

	resp := &scanResponse{
		Mode:   req.Mode,
		Action: action,
		Instance: scanInstanceInfo{
			ID:   inst.ID,
			Name: inst.Name,
			Type: inst.Type,
		},
		Totals: scanTotals{Items: len(series)},
	}

	// Skip empty series (no episodefiles → nothing to tag). Statistics
	// is populated by Sonarr on every /series response; nil-guard for
	// pathological mocks. Onedr0p tag-resolution.sh uses the same
	// fast-path skip.
	tasks := make([]arr.Item, 0, len(series))
	for _, ser := range series {
		if ser.Statistics != nil && ser.Statistics.EpisodeFileCount == 0 {
			continue
		}
		tasks = append(tasks, ser)
	}

	type fetchResult struct {
		epfiles []arr.EpisodeFile
		err     error
	}
	results := make([]fetchResult, len(tasks))

	// Worker pool: bounded concurrency over the per-series episodefile
	// fetches. Channel-as-semaphore keeps the goroutine bookkeeping
	// trivial (no external dep). Per-call sub-context guards against
	// a single hung fetch holding the whole scan.
	sem := make(chan struct{}, sonarrEpisodeWorkerCount)
	var wg sync.WaitGroup
	for i := range tasks {
		// Ctx-aware semaphore acquire — when the user fires Cancel mid-
		// scan, we stop dispatching new fetches instead of blocking on
		// 5 in-flight goroutines to drain (worst case 30 s per the sub-
		// context). Goroutines already started keep running; their
		// per-call sub-context will catch the parent cancel and abort
		// each in turn.
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			results[i] = fetchResult{err: ctx.Err()}
			continue
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			subCtx, cancel := context.WithTimeout(ctx, sonarrEpisodefilesTimeout)
			defer cancel()
			ef, err := client.ListEpisodefiles(subCtx, tasks[i].ID)
			results[i] = fetchResult{epfiles: ef, err: err}
		}(i)
	}
	wg.Wait()

	// Aggregate counters mirror Radarr's runAutoTags so flattenAutoTagRollup
	// + the UI rollup table read identically across Arr types.
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

	for i, ser := range tasks {
		fr := results[i]
		if fr.err != nil {
			// Per-series fetch failed — surface as an error row so the
			// user sees the series wasn't checked. Other series in the
			// scan still run.
			resp.Items = append(resp.Items, scanItem{
				ID:          ser.ID,
				Title:       ser.Title,
				Year:        ser.Year,
				TvdbID:      ser.TvdbID,
				CurrentTags: ser.Tags,
				SeriesID:    ser.ID,
				SeriesTitle: ser.Title,
				Error:       "fetch episodefiles: " + fr.err.Error(),
			})
			continue
		}
		epfiles := fr.epfiles
		if len(epfiles) == 0 {
			// Statistics said >0 but the array came back empty — race
			// against Sonarr ingestion or stale stats. Skip silently;
			// nothing to aggregate.
			continue
		}

		// Build EpisodeInput[] for aggregation + scanSeriesEpisode[]
		// for the drill-in view. One pass over epfiles populates both
		// AND increments resp.Totals.MissingMediaInfo per-episode for
		// any file whose mediaInfo is missing the audio/video fields
		// the active scan needs (matches the Radarr predicate's
		// per-item semantics — Radarr movies are 1 file, Sonarr series
		// are N files, so the Sonarr count grows per affected episode
		// rather than per series).
		eps := make([]engine.EpisodeInput, 0, len(epfiles))
		episodeViews := make([]scanSeriesEpisode, 0, len(epfiles))
		for _, ef := range epfiles {
			var mi engine.MediaInfo
			if ef.MediaInfo != nil {
				mi = engine.MediaInfo{
					Height:                  ef.MediaInfo.Height,
					VideoCodec:              ef.MediaInfo.VideoCodec,
					VideoBitDepth:           ef.MediaInfo.VideoBitDepth,
					VideoDynamicRangeType:   ef.MediaInfo.VideoDynamicRangeType,
					AudioCodec:              ef.MediaInfo.AudioCodec,
					AudioChannels:           ef.MediaInfo.AudioChannels,
					AudioAdditionalFeatures: ef.MediaInfo.AudioAdditionalFeatures,
				}
			}
			// Filename context fuels hasAtmos's fallback path on legacy
			// imports + EAC3-Atmos streams (audioAdditionalFeatures
			// often blank for those).
			mi.RelativePath = ef.RelativePath
			mi.SceneName = ef.SceneName
			qr := 0
			if ef.Quality != nil {
				qr = ef.Quality.Quality.Resolution
			}
			if missingMediaInfoFn(mi, qr) {
				resp.Totals.MissingMediaInfo++
			}
			eps = append(eps, engine.EpisodeInput{Info: mi, QualityResolution: qr})

			ev := scanSeriesEpisode{
				EpisodeFileID:   ef.ID,
				SeasonNumber:    ef.SeasonNumber,
				RelativePath:    ef.RelativePath,
				SceneName:       ef.SceneName,
				ContributedTags: perEpisodeFn(mi, qr),
			}
			fillEpisodeMediaSummary(&ev, mi, qr)
			episodeViews = append(episodeViews, ev)
		}

		desired := aggregateFn(eps)

		// Diff series.Tags against (managed-only ∩ current). Anything
		// not in the managed set is left alone (could be a manual tag,
		// a CF tag, etc — we never touch it).
		desiredSet := make(map[string]struct{}, len(desired))
		for _, t := range desired {
			desiredSet[t] = struct{}{}
		}
		currentManaged := make(map[string]struct{})
		for _, tid := range ser.Tags {
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
			ensureSet(addByTag, tag)[ser.ID] = struct{}{}
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
			ensureSet(removeByTag, label)[ser.ID] = struct{}{}
			itemDecisions = append(itemDecisions, scanAutoTagDecision{
				Bucket: bucket, Tag: label, Action: "remove",
			})
			resp.Totals.ToRemove++
			bumpRollup("remove", bucket, label)
		}

		resp.Items = append(resp.Items, scanItem{
			ID:               ser.ID, // series ID = unique row identity
			TvdbID:           ser.TvdbID,
			Title:            ser.Title,
			Year:             ser.Year,
			CurrentTags:      ser.Tags,
			SeriesID:         ser.ID,
			SeriesTitle:      ser.Title,
			EpisodeFileCount: len(epfiles),
			AutoDecisions:    itemDecisions,
			Episodes:         episodeViews,
		})
	}

	resp.Totals.AutoTagRollups = flattenAutoTagRollup(rollupCount)

	if req.Mode == "preview" {
		return resp, nil
	}

	// Apply path: create missing tags, then add/remove via series-editor.
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
		if err := client.EditorApplyTags(ctx, "sonarr", idList, []int{labelToID[label]}, "add"); err != nil {
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
		if err := client.EditorApplyTags(ctx, "sonarr", idList, []int{tid}, "remove"); err != nil {
			return nil, newAPIError(502, fmt.Sprintf("apply remove %q: %v", label, err))
		}
		applied.ItemsRemoved += len(idList)
	}
	resp.Applied = &applied
	return resp, nil
}

// fillEpisodeMediaSummary populates the compact mediaInfo strings on
// scanSeriesEpisode (used by the drill-in card) from the engine
// MediaInfo + qualityResolution fallback. Routed through
// engine.SummariseMediaInfo so the drill-in copy matches what the
// emit-side actually computes.
func fillEpisodeMediaSummary(ev *scanSeriesEpisode, mi engine.MediaInfo, qualityResolution int) {
	sum := engine.SummariseMediaInfo(mi, qualityResolution)
	ev.Resolution = sum.Resolution
	ev.VideoCodec = sum.VideoCodec
	ev.HDR = sum.HDR
	ev.VideoBitDepth = sum.VideoBitDepth
	ev.AudioCodec = sum.AudioCodec
	ev.AudioChannels = sum.AudioChannels
	ev.HasAtmos = sum.HasAtmos
}
