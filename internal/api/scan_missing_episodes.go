package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"resolvarr/internal/arr"
	"resolvarr/internal/core"
	"resolvarr/internal/core/engine"
)

// scan_missing_episodes.go — Tag Library → Sonarr → Missing episodes.
// Finds finished-airing seasons that have gaps (mostly-imported but
// missing 1-2 episodes mid-season), lets the user trigger
// per-episode search in Sonarr + optionally tag the parent series.
// Sonarr-only — all three handlers reject non-Sonarr instances.
//
// Architecture mirrors scan_auto_tags_sonarr.go: ListSeries → bounded
// worker-pool fan-out over ListEpisodesForSeries → engine helper per
// series. The engine helper is pure; this file is the I/O + HTTP
// glue.

// missingEpisodesWorkerCount caps concurrent ListEpisodesForSeries
// calls. Same reasoning as sonarrEpisodeWorkerCount in
// scan_auto_tags_sonarr.go — Sonarr's API is single-threaded
// internally and >5 concurrent reads on a slow library can stall.
const missingEpisodesWorkerCount = 5

// missingEpisodesPerSeriesTimeout bounds each ListEpisodesForSeries
// fetch. Episode listing for one series is a single GET against
// Sonarr's local DB; 30 s is generous and matches the audio/video
// pipeline's choice.
const missingEpisodesPerSeriesTimeout = 30 * time.Second

// missingEpisodesPreviewRequest is the POST body for /api/scan/missing-episodes/preview.
//
// BufferHours uses *int so we can tell "not supplied" (use default 24)
// apart from an explicit 0 (= flag any aired episode immediately). The
// pointer is the only sentinel the JSON layer gives us — a plain int
// zero-values to 0 even when the field was absent.
type missingEpisodesPreviewRequest struct {
	InstanceID        string  `json:"instanceId"`
	Threshold         float64 `json:"threshold"`         // 0.0-1.0, default 0.7
	BufferHours       *int    `json:"bufferHours"`       // nil = use default 24; 0 = explicit "no buffer"
	IncludeContinuing bool    `json:"includeContinuing"` // default true (treat as opt-in)
	IncludeEnded      bool    `json:"includeEnded"`      // default true
	IncludeSpecials   bool    `json:"includeSpecials"`   // default false — season-0 specials skipped unless explicitly opted in
}

type missingEpisodesPreviewResponse struct {
	InstanceID           string                          `json:"instanceId"`
	InstanceName         string                          `json:"instanceName"`
	Threshold            float64                         `json:"threshold"`
	BufferHours          int                             `json:"bufferHours"`
	SeriesScanned        int                             `json:"seriesScanned"`
	SeriesWithGaps       int                             `json:"seriesWithGaps"`
	TotalMissingEpisodes int                             `json:"totalMissingEpisodes"`
	Series               []engine.MissingEpisodeSeries   `json:"series"` // only series with at least one qualifying season
	Errors               []missingEpisodesSeriesError    `json:"errors,omitempty"`
}

type missingEpisodesSeriesError struct {
	SeriesID    int    `json:"seriesId"`
	SeriesTitle string `json:"seriesTitle"`
	Error       string `json:"error"`
}

// missingEpisodesSearchRequest triggers Sonarr's EpisodeSearch command
// for the given episode IDs. Per-row and bulk both use this endpoint.
type missingEpisodesSearchRequest struct {
	InstanceID string `json:"instanceId"`
	EpisodeIDs []int  `json:"episodeIds"`
}

type missingEpisodesSearchResponse struct {
	Triggered int `json:"triggered"`
}

// missingEpisodesTagRequest applies (or auto-cleans) the configured
// tag against the supplied series IDs. RemoveFromOthers=true also
// clears the tag from any series currently carrying it but not in
// SeriesIDs — this is the auto-cleanup hook a follow-up scan uses to
// drop the tag from series that have become complete since the last
// run.
type missingEpisodesTagRequest struct {
	InstanceID       string `json:"instanceId"`
	TagName          string `json:"tagName"`
	SeriesIDs        []int  `json:"seriesIds"`
	RemoveFromOthers bool   `json:"removeFromOthers"`
}

type missingEpisodesTagResponse struct {
	Applied int    `json:"applied"`
	Removed int    `json:"removed"`
	TagName string `json:"tagName"`
	TagID   int    `json:"tagId"`
}

// validateMissingEpisodesInstance shared guard — looks up the
// instance + checks it's Sonarr. Returns the resolved instance or an
// already-written HTTP error (in which case the bool is false and the
// caller returns).
func (s *Server) validateMissingEpisodesInstance(w http.ResponseWriter, instanceID string) (*core.Instance, bool) {
	if instanceID == "" {
		writeError(w, 400, "instanceId is required")
		return nil, false
	}
	cfg := s.App.Config.Get()
	var inst *core.Instance
	for i := range cfg.Instances {
		if cfg.Instances[i].ID == instanceID {
			inst = &cfg.Instances[i]
			break
		}
	}
	if inst == nil {
		writeError(w, 404, "instance not found")
		return nil, false
	}
	if inst.Type != "sonarr" {
		writeError(w, 400, "missing-episodes scan is Sonarr-only — pick a Sonarr instance")
		return nil, false
	}
	return inst, true
}

// handleMissingEpisodesPreview runs the scan + returns the result.
// Worker-pooled fetch over ListEpisodesForSeries, engine.DetectMissingEpisodes
// per series, aggregate into the response.
func (s *Server) handleMissingEpisodesPreview(w http.ResponseWriter, r *http.Request) {
	var req missingEpisodesPreviewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "invalid body")
		return
	}
	inst, ok := s.validateMissingEpisodesInstance(w, req.InstanceID)
	if !ok {
		return
	}
	if req.Threshold == 0 {
		req.Threshold = 0.7
	}
	if req.Threshold < 0 || req.Threshold > 1 {
		writeError(w, 400, "threshold must be between 0 and 1")
		return
	}
	// BufferHours: nil → default 24; explicit value honoured (including 0).
	// Bounds: 0 to 672 (4 weeks). 0 means "flag any aired episode" — the
	// user knows what they're asking for; we don't silently coerce.
	bufferHours := 24
	if req.BufferHours != nil {
		bufferHours = *req.BufferHours
		if bufferHours < 0 || bufferHours > 168*4 {
			// 168 hours = 1 week; we cap at 4 weeks to keep the input
			// sensible (Sonarr re-checks for new releases more often than
			// that anyway).
			writeError(w, 400, "bufferHours must be between 0 and 672")
			return
		}
	}
	// C1: at least one series-status filter must be enabled, otherwise
	// the scan walks zero series and returns a misleading "all complete"
	// success. The UI also disables the Run button in that state, but the
	// backend validates defensively in case a stale client / direct API
	// caller skips the UI gate.
	if !req.IncludeContinuing && !req.IncludeEnded {
		writeError(w, 400, "at least one of includeContinuing / includeEnded must be true")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()

	client := s.arrClientFor(inst)
	series, err := client.ListSeries(ctx)
	if err != nil {
		writeError(w, 502, "list series: "+err.Error())
		return
	}

	// Filter series before we fan out the per-series fetch. Saves N
	// fetches when the user has unchecked "continuing" or "ended".
	// Series with monitored=false are always skipped — they're the
	// "I'm hoarding metadata but not syncing files" case, no point
	// flagging anything.
	type task struct {
		series arr.ArrSeriesSummary
	}
	tasks := make([]task, 0, len(series))
	for _, ser := range series {
		if !ser.Monitored {
			continue
		}
		if isContinuingStatus(ser.Status) {
			if !req.IncludeContinuing {
				continue
			}
		} else {
			if !req.IncludeEnded {
				continue
			}
		}
		tasks = append(tasks, task{series: ser})
	}

	// Per-series episodes + per-series error captured in parallel.
	type result struct {
		episodes []arr.ArrEpisodeSummary
		err      error
	}
	results := make([]result, len(tasks))
	sem := make(chan struct{}, missingEpisodesWorkerCount)
	var wg sync.WaitGroup
	for i := range tasks {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			results[i] = result{err: ctx.Err()}
			continue
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			subCtx, c := context.WithTimeout(ctx, missingEpisodesPerSeriesTimeout)
			defer c()
			eps, err := client.ListEpisodesForSeries(subCtx, tasks[i].series.ID)
			results[i] = result{episodes: eps, err: err}
		}(i)
	}
	wg.Wait()

	now := time.Now().UTC()
	resp := missingEpisodesPreviewResponse{
		InstanceID:   inst.ID,
		InstanceName: inst.Name,
		Threshold:    req.Threshold,
		BufferHours:  bufferHours,
	}

	for i, ts := range tasks {
		ser := ts.series
		if results[i].err != nil {
			resp.Errors = append(resp.Errors, missingEpisodesSeriesError{
				SeriesID:    ser.ID,
				SeriesTitle: ser.Title,
				Error:       results[i].err.Error(),
			})
			continue
		}
		// Convert arr.ArrEpisodeSummary → engine.ArrEpisodeSummary at
		// the boundary. Same-shape types but engine keeps its types
		// independent of the arr package to preserve the no-I/O
		// contract on engine.
		//
		// B2: season 0 = specials. Most libraries have ad-hoc, sparse
		// specials that the user doesn't actively curate via Sonarr
		// search — including them by default would pollute the result
		// list and trigger inappropriate searches on bulk Search. Skip
		// unless the caller opts in explicitly.
		eps := make([]engine.ArrEpisodeSummary, 0, len(results[i].episodes))
		for _, ep := range results[i].episodes {
			if !req.IncludeSpecials && ep.SeasonNumber == 0 {
				continue
			}
			eps = append(eps, engine.ArrEpisodeSummary{
				ID:            ep.ID,
				SeriesID:      ep.SeriesID,
				SeasonNumber:  ep.SeasonNumber,
				EpisodeNumber: ep.EpisodeNumber,
				Title:         ep.Title,
				AirDateUtc:    ep.AirDateUtc,
				Monitored:     ep.Monitored,
				HasFile:       ep.HasFile,
			})
		}
		det := engine.DetectMissingEpisodes(
			engine.ArrSeriesSummary{
				ID:        ser.ID,
				Title:     ser.Title,
				Status:    ser.Status,
				Monitored: ser.Monitored,
			},
			eps,
			req.Threshold,
			bufferHours,
			now,
		)
		resp.SeriesScanned++
		if det.SeasonsWithGaps == 0 || len(det.Seasons) == 0 {
			continue
		}
		resp.SeriesWithGaps++
		for _, season := range det.Seasons {
			resp.TotalMissingEpisodes += len(season.MissingEpisodes)
		}
		resp.Series = append(resp.Series, det)
	}

	// Sort flagged series alphabetically by title — stable UI order.
	sort.Slice(resp.Series, func(i, j int) bool {
		return resp.Series[i].SeriesTitle < resp.Series[j].SeriesTitle
	})

	writeJSON(w, resp)
}

// handleMissingEpisodesSearch triggers Sonarr's EpisodeSearch command
// for the given episode IDs. Per-row and bulk both POST here.
func (s *Server) handleMissingEpisodesSearch(w http.ResponseWriter, r *http.Request) {
	var req missingEpisodesSearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "invalid body")
		return
	}
	inst, ok := s.validateMissingEpisodesInstance(w, req.InstanceID)
	if !ok {
		return
	}
	if len(req.EpisodeIDs) == 0 {
		writeError(w, 400, "episodeIds is required")
		return
	}
	// Defensive cap so a runaway selection can't post a body that
	// times out Sonarr's command queue. 500 episodes per call is
	// already a large search burst (one TV-spanning multi-season
	// blast).
	if len(req.EpisodeIDs) > 500 {
		writeError(w, 400, "episodeIds capped at 500 per request")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	client := s.arrClientFor(inst)
	if err := client.SearchEpisodes(ctx, req.EpisodeIDs); err != nil {
		writeError(w, 502, "episode search: "+err.Error())
		return
	}
	writeJSON(w, missingEpisodesSearchResponse{Triggered: len(req.EpisodeIDs)})
}

// handleMissingEpisodesTag applies the configured tag (default
// "missing-episodes") to the supplied series and, when
// RemoveFromOthers is true, strips it from any series currently
// carrying it that isn't in SeriesIDs. The latter is the auto-cleanup
// path a follow-up scan uses to retire the tag from series that
// became complete since the previous run.
//
// Tag is created in Sonarr if it doesn't already exist. Validation
// follows the same reTagName convention used elsewhere — Sonarr's
// label regex would reject anything outside `^[a-z0-9][a-z0-9_-]*$`
// with a cryptic 400.
func (s *Server) handleMissingEpisodesTag(w http.ResponseWriter, r *http.Request) {
	var req missingEpisodesTagRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "invalid body")
		return
	}
	inst, ok := s.validateMissingEpisodesInstance(w, req.InstanceID)
	if !ok {
		return
	}
	tagName := req.TagName
	if tagName == "" {
		tagName = "missing-episodes"
	}
	if !reTagName.MatchString(tagName) {
		writeError(w, 400, "tagName must be lowercase letters, digits, underscores, or dashes")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()

	client := s.arrClientFor(inst)

	// Resolve / create the tag.
	tagDetails, err := client.ListTagDetails(ctx)
	if err != nil {
		writeError(w, 502, "list tags: "+err.Error())
		return
	}
	var tagID int
	var carriers []int // series IDs that currently carry this tag
	for _, td := range tagDetails {
		if td.Label == tagName {
			tagID = td.ID
			carriers = append(carriers, td.SeriesIDs...)
			break
		}
	}
	if tagID == 0 {
		// Tag doesn't exist yet — create it (only when there's
		// actually something to apply; pure-cleanup with nothing to
		// apply against a non-existent tag is a no-op).
		if len(req.SeriesIDs) == 0 {
			writeJSON(w, missingEpisodesTagResponse{Applied: 0, Removed: 0, TagName: tagName, TagID: 0})
			return
		}
		t, err := client.CreateTag(ctx, tagName)
		if err != nil {
			// B3: race-aware retry. Two concurrent callers (cron + user
			// click, two browser tabs, parallel webhook + manual run)
			// can both see the tag missing in their respective
			// ListTagDetails snapshots → both POST /api/v3/tag → second
			// gets HTTP 409. Re-fetch and continue if the tag now exists.
			// Only the second (loser) caller falls into this branch;
			// the winner's CreateTag succeeded normally above.
			details, rerr := client.ListTagDetails(ctx)
			if rerr == nil {
				for _, td := range details {
					if td.Label == tagName {
						tagID = td.ID
						carriers = td.SeriesIDs
						break
					}
				}
			}
			if tagID == 0 {
				writeError(w, 502, fmt.Sprintf("create tag %q: %v", tagName, err))
				return
			}
		} else {
			tagID = t.ID
		}
	}

	// Set semantics for the desired series.
	desired := make(map[int]struct{}, len(req.SeriesIDs))
	for _, id := range req.SeriesIDs {
		desired[id] = struct{}{}
	}
	// Apply pass — add the tag to every series in desired that
	// doesn't already carry it.
	carrierSet := make(map[int]struct{}, len(carriers))
	for _, id := range carriers {
		carrierSet[id] = struct{}{}
	}
	var toAdd []int
	for id := range desired {
		if _, on := carrierSet[id]; !on {
			toAdd = append(toAdd, id)
		}
	}
	if len(toAdd) > 0 {
		if err := client.EditorApplyTags(ctx, "sonarr", toAdd, []int{tagID}, "add"); err != nil {
			writeError(w, 502, "apply add: "+err.Error())
			return
		}
	}

	// Optional cleanup pass.
	var toRemove []int
	if req.RemoveFromOthers {
		for _, id := range carriers {
			if _, want := desired[id]; want {
				continue
			}
			toRemove = append(toRemove, id)
		}
		if len(toRemove) > 0 {
			if err := client.EditorApplyTags(ctx, "sonarr", toRemove, []int{tagID}, "remove"); err != nil {
				writeError(w, 502, "apply remove: "+err.Error())
				return
			}
		}
	}

	writeJSON(w, missingEpisodesTagResponse{
		Applied: len(toAdd),
		Removed: len(toRemove),
		TagName: tagName,
		TagID:   tagID,
	})
}

// isContinuingStatus mirrors engine.isContinuing but lives in the api
// package so the API-side filter doesn't have to import the engine
// helper twice (engine.isContinuing is unexported). Same logic.
func isContinuingStatus(status string) bool {
	switch status {
	case "continuing", "upcoming":
		return true
	}
	return false
}
