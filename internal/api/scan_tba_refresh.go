package api

// scan_tba_refresh.go — Sonarr "TBA refresh": find episode files that
// were imported with a placeholder "TBA" title and rename them now that
// Sonarr knows the real episode title.
//
// Detection rides on Sonarr's own rename preview (GET /api/v3/rename):
// Sonarr lists ONLY files whose on-disk name differs from what the
// series' naming pattern would now produce. We filter that list to
// files whose CURRENT name still carries a "TBA" token — those are the
// ones that imported as TBA and have since gained a real title. Apply
// fires Sonarr's RenameFiles command (async, per series).
//
// Architecture mirrors scan_missing_episodes.go: worker-pooled walk over
// monitored series, reusable runner methods used by both the HTTP
// handlers and the scheduler runner's combined/standalone phase.

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"resolvarr/internal/arr"
	"resolvarr/internal/core"
)

const (
	tbaRefreshWorkerCount      = 6
	tbaRefreshPerSeriesTimeout = 30 * time.Second
	// Aggregate ceilings so a huge library / a stuck Sonarr can't hang
	// the request indefinitely (mirrors the missing-episodes handler's
	// WithTimeout wrap). Per-series workers still bound individually.
	tbaRefreshPreviewTimeout = 5 * time.Minute
	tbaRefreshApplyTimeout   = 2 * time.Minute
)

// reTbaToken matches a standalone "TBA" token (case-insensitive) bounded
// by non-alphanumerics or string edges, so "S03E07 - TBA.mkv" matches
// but "Mortbay" / "TBAR" do not. Same word-boundary discipline as the
// release-group matcher.
var reTbaToken = regexp.MustCompile(`(?i)(^|[^a-z0-9])tba([^a-z0-9]|$)`)

type tbaRefreshPreviewRequest struct {
	InstanceID        string `json:"instanceId"`
	IncludeContinuing bool   `json:"includeContinuing"`
	IncludeEnded      bool   `json:"includeEnded"`
	IncludeSpecials   bool   `json:"includeSpecials"`
}

// tbaRefreshFile is one renameable file. ExistingName/NewName are the
// basenames (the relative path's last segment) for compact display.
type tbaRefreshFile struct {
	EpisodeFileID  int    `json:"episodeFileId"`
	SeasonNumber   int    `json:"seasonNumber"`
	EpisodeNumbers []int  `json:"episodeNumbers"`
	ExistingName   string `json:"existingName"`
	NewName        string `json:"newName"`
}

type tbaRefreshSeries struct {
	SeriesID    int              `json:"seriesId"`
	SeriesTitle string           `json:"seriesTitle"`
	Files       []tbaRefreshFile `json:"files"`
}

type tbaRefreshSeriesError struct {
	SeriesID    int    `json:"seriesId"`
	SeriesTitle string `json:"seriesTitle"`
	Error       string `json:"error"`
}

type tbaRefreshPreviewResponse struct {
	InstanceID    string                  `json:"instanceId"`
	InstanceName  string                  `json:"instanceName"`
	SeriesScanned int                     `json:"seriesScanned"`
	SeriesWithTba int                     `json:"seriesWithTba"`
	TotalFiles    int                     `json:"totalFiles"`
	Series        []tbaRefreshSeries      `json:"series"`
	Errors        []tbaRefreshSeriesError `json:"errors,omitempty"`
}

// tbaRefreshApplyGroup pairs a series with the file IDs to rename in it.
// Sonarr's RenameFiles command is per-series, so the apply request
// carries the grouping the preview already produced.
type tbaRefreshApplyGroup struct {
	SeriesID int   `json:"seriesId"`
	FileIDs  []int `json:"fileIds"`
}

type tbaRefreshApplyRequest struct {
	InstanceID string                 `json:"instanceId"`
	Groups     []tbaRefreshApplyGroup `json:"groups"`
}

type tbaRefreshApplyResponse struct {
	Queued      int                     `json:"queued"`
	SeriesCount int                     `json:"seriesCount"`
	Errors      []tbaRefreshSeriesError `json:"errors,omitempty"`
}

// pathHasTbaToken reports whether the basename of a relative path
// carries a standalone TBA token.
func pathHasTbaToken(relPath string) bool {
	return reTbaToken.MatchString(baseNameOf(relPath))
}

// baseNameOf returns the last path segment, splitting on both / and \
// (Sonarr reports relative paths with either separator depending on
// host OS).
func baseNameOf(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}

// validateTbaRefreshInstance resolves the instance + enforces Sonarr.
func (s *Server) validateTbaRefreshInstance(w http.ResponseWriter, instanceID string) (*core.Instance, bool) {
	if instanceID == "" {
		writeError(w, 400, "instanceId is required")
		return nil, false
	}
	inst := findInstanceByID(s.App.Config.Get(), instanceID)
	if inst == nil {
		writeError(w, 404, "instance not found")
		return nil, false
	}
	if inst.Type != "sonarr" {
		writeError(w, 400, "TBA refresh is Sonarr-only — pick a Sonarr instance")
		return nil, false
	}
	return inst, true
}

func (s *Server) handleTbaRefreshPreview(w http.ResponseWriter, r *http.Request) {
	var req tbaRefreshPreviewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "invalid body")
		return
	}
	inst, ok := s.validateTbaRefreshInstance(w, req.InstanceID)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), tbaRefreshPreviewTimeout)
	defer cancel()
	resp, apiErr := s.runTbaRefreshPreview(ctx, inst, req.IncludeContinuing, req.IncludeEnded, req.IncludeSpecials)
	if apiErr != nil {
		writeError(w, apiErr.Status, apiErr.Message)
		return
	}
	writeJSON(w, resp)
}

func (s *Server) handleTbaRefreshApply(w http.ResponseWriter, r *http.Request) {
	var req tbaRefreshApplyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "invalid body")
		return
	}
	inst, ok := s.validateTbaRefreshInstance(w, req.InstanceID)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), tbaRefreshApplyTimeout)
	defer cancel()
	resp, apiErr := s.runTbaRefreshApply(ctx, inst, req.Groups)
	if apiErr != nil {
		writeError(w, apiErr.Status, apiErr.Message)
		return
	}
	writeJSON(w, resp)
}

// runTbaRefreshPreview walks monitored series, pulls each one's rename
// preview, and keeps the records whose current name still has a TBA
// token. Reusable by the HTTP handler + the scheduler runner.
func (s *Server) runTbaRefreshPreview(
	ctx context.Context,
	inst *core.Instance,
	includeContinuing bool,
	includeEnded bool,
	includeSpecials bool,
) (*tbaRefreshPreviewResponse, *apiError) {
	client := s.arrClientFor(inst)
	series, err := client.ListSeries(ctx)
	if err != nil {
		return nil, newAPIError(502, "list series: "+err.Error())
	}

	type task struct{ series arr.ArrSeriesSummary }
	tasks := make([]task, 0, len(series))
	for _, ser := range series {
		if !ser.Monitored {
			continue
		}
		if isContinuingStatus(ser.Status) {
			if !includeContinuing {
				continue
			}
		} else if !includeEnded {
			continue
		}
		tasks = append(tasks, task{series: ser})
	}

	type result struct {
		records []arr.SonarrRenameRecord
		err     error
	}
	results := make([]result, len(tasks))
	sem := make(chan struct{}, tbaRefreshWorkerCount)
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
			subCtx, c := context.WithTimeout(ctx, tbaRefreshPerSeriesTimeout)
			defer c()
			recs, err := client.GetSonarrRenamePreview(subCtx, tasks[i].series.ID)
			results[i] = result{records: recs, err: err}
		}(i)
	}
	wg.Wait()

	resp := &tbaRefreshPreviewResponse{InstanceID: inst.ID, InstanceName: inst.Name}
	for i, ts := range tasks {
		ser := ts.series
		resp.SeriesScanned++
		if results[i].err != nil {
			resp.Errors = append(resp.Errors, tbaRefreshSeriesError{
				SeriesID:    ser.ID,
				SeriesTitle: ser.Title,
				Error:       results[i].err.Error(),
			})
			continue
		}
		var files []tbaRefreshFile
		for _, rec := range results[i].records {
			if !includeSpecials && rec.SeasonNumber == 0 {
				continue
			}
			if !pathHasTbaToken(rec.ExistingPath) {
				continue
			}
			files = append(files, tbaRefreshFile{
				EpisodeFileID:  rec.EpisodeFileID,
				SeasonNumber:   rec.SeasonNumber,
				EpisodeNumbers: rec.EpisodeNumbers,
				ExistingName:   baseNameOf(rec.ExistingPath),
				NewName:        baseNameOf(rec.NewPath),
			})
		}
		if len(files) == 0 {
			continue
		}
		sort.Slice(files, func(a, b int) bool {
			if files[a].SeasonNumber != files[b].SeasonNumber {
				return files[a].SeasonNumber < files[b].SeasonNumber
			}
			return files[a].ExistingName < files[b].ExistingName
		})
		resp.SeriesWithTba++
		resp.TotalFiles += len(files)
		resp.Series = append(resp.Series, tbaRefreshSeries{
			SeriesID:    ser.ID,
			SeriesTitle: ser.Title,
			Files:       files,
		})
	}
	sort.Slice(resp.Series, func(a, b int) bool {
		return resp.Series[a].SeriesTitle < resp.Series[b].SeriesTitle
	})
	return resp, nil
}

// runTbaRefreshApply fires Sonarr's RenameFiles command per series.
// Fire-and-forget: Sonarr queues the renames asynchronously, so we
// report the count we triggered rather than confirmed completions.
//
// One series failing does NOT abort the rest — the user may have picked
// files across many series, and silently dropping the remainder after a
// mid-batch failure would be worse than a partial success with the
// failures reported. A request returns 502 only when EVERY group failed.
func (s *Server) runTbaRefreshApply(
	ctx context.Context,
	inst *core.Instance,
	groups []tbaRefreshApplyGroup,
) (*tbaRefreshApplyResponse, *apiError) {
	client := s.arrClientFor(inst)
	resp := &tbaRefreshApplyResponse{}
	attempted := 0
	for _, g := range groups {
		if g.SeriesID == 0 || len(g.FileIDs) == 0 {
			continue
		}
		attempted++
		if err := client.TriggerSonarrRenameFiles(ctx, g.SeriesID, g.FileIDs); err != nil {
			resp.Errors = append(resp.Errors, tbaRefreshSeriesError{
				SeriesID: g.SeriesID,
				Error:    err.Error(),
			})
			continue
		}
		resp.Queued += len(g.FileIDs)
		resp.SeriesCount++
	}
	if attempted > 0 && resp.SeriesCount == 0 {
		return nil, newAPIError(502, "all rename commands failed: "+resp.Errors[0].Error)
	}
	return resp, nil
}
