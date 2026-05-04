package api

import (
	"context"
	"encoding/json"
	"net/http"

	"resolvarr/internal/core"
)

// recover_exclusions.go — HTTP handlers for the per-instance Recover
// exclusion lists. The frontend maintains the list via three calls:
//
//   GET    /api/recover/exclusions/{instanceId}   → current list
//   POST   /api/recover/exclusions/{instanceId}   → add items
//   DELETE /api/recover/exclusions/{instanceId}   → remove items
//
// Body shape for POST + DELETE (same envelope):
//
//   {
//     "movies": [123, 456],            // Radarr movie IDs
//     "series": [42],                  // Sonarr series IDs (whole series)
//     "seasons": [{"seriesId": 7, "seasonNumber": 3}, ...]
//   }
//
// Whichever fields the body carries are processed; missing fields are
// no-ops. Validation is intentionally lenient: we accept arbitrary IDs
// even when they don't exist in the current scan, so the user can
// pre-exclude items they know about (e.g. a series whose source has
// permanently dried up).
//
// All three handlers reject non-matching app-type body fields:
// posting movies for a Sonarr instance returns 400, posting series
// for a Radarr instance returns 400.

type recoverExclusionRequestSeason struct {
	SeriesID     int `json:"seriesId"`
	SeasonNumber int `json:"seasonNumber"`
}

type recoverExclusionRequestBody struct {
	Movies  []int                           `json:"movies,omitempty"`
	Series  []int                           `json:"series,omitempty"`
	Seasons []recoverExclusionRequestSeason `json:"seasons,omitempty"`
}

// recoverExclusionMovieEntry / SeriesEntry / SeasonEntry are the
// title-enriched shapes the GET handler returns. Title comes from a
// best-effort Arr ListItems lookup so the Excluded filter chip in
// the result panel can render full series-cards (matching the
// visual of Would-fix / Flagged / etc.) instead of bare-ID rows.
//
// Title is empty when the lookup failed (Arr unreachable) or the
// item was deleted from Arr after exclusion. Frontend falls back
// to "Movie #ID" / "Series #ID" in that case.
type recoverExclusionMovieEntry struct {
	ID    int    `json:"id"`
	Title string `json:"title,omitempty"`
	Year  int    `json:"year,omitempty"`
}

type recoverExclusionSeriesEntry struct {
	ID     int    `json:"id"`
	Title  string `json:"title,omitempty"`
	Year   int    `json:"year,omitempty"`
	TvdbID int    `json:"tvdbId,omitempty"`
}

type recoverExclusionSeasonEntry struct {
	SeriesID     int    `json:"seriesId"`
	SeasonNumber int    `json:"seasonNumber"`
	SeriesTitle  string `json:"seriesTitle,omitempty"`
	Year         int    `json:"year,omitempty"`
}

// recoverExclusionResponse is the GET return shape — title-enriched
// arrays + the instance ID echo so the frontend can confirm it asked
// for the right one.
type recoverExclusionResponse struct {
	InstanceID string                        `json:"instanceId"`
	Movies     []recoverExclusionMovieEntry  `json:"movies"`
	Series     []recoverExclusionSeriesEntry `json:"series"`
	Seasons    []recoverExclusionSeasonEntry `json:"seasons"`
}

// handleListRecoverExclusions returns the current exclusion list for
// the given instance, enriched with titles fetched best-effort from
// the Arr instance's ListItems. Empty struct (200 OK) when the
// instance has nothing excluded yet — distinguishes "no exclusions"
// from "instance not found" (404). When Arr is unreachable, IDs are
// returned with empty titles; frontend falls back to "Movie #ID" /
// "Series #ID".
func (s *Server) handleListRecoverExclusions(w http.ResponseWriter, r *http.Request) {
	instanceID := r.PathValue("instanceId")
	cfg := s.App.Config.Get()
	inst := findInstance(cfg, instanceID)
	if inst == nil {
		writeError(w, 404, "instance not found")
		return
	}

	excl := cfg.RecoverExclusions[instanceID]
	resp := recoverExclusionResponse{
		InstanceID: instanceID,
		Movies:     []recoverExclusionMovieEntry{},
		Series:     []recoverExclusionSeriesEntry{},
		Seasons:    []recoverExclusionSeasonEntry{},
	}

	// Best-effort title enrichment. Build an ID-keyed lookup from one
	// /api/v3/movie or /api/v3/series call; absorb failure silently
	// (ListItems errors → empty maps → empty Title fields → frontend
	// falls back to "Movie #ID"). Skipped entirely when there's
	// nothing to enrich (no exclusions).
	type titleInfo struct {
		title  string
		year   int
		tvdbID int
	}
	titleByID := make(map[int]titleInfo)
	if len(excl.Movies) > 0 || len(excl.Series) > 0 {
		ctx, cancel := context.WithTimeout(r.Context(), scanTimeout)
		defer cancel()
		client := s.arrClientFor(inst)
		if items, err := client.ListItems(ctx, inst.Type); err == nil {
			for _, it := range items {
				titleByID[it.ID] = titleInfo{title: it.Title, year: it.Year, tvdbID: it.TvdbID}
			}
		}
	}

	for _, id := range excl.Movies {
		ti := titleByID[id]
		resp.Movies = append(resp.Movies, recoverExclusionMovieEntry{
			ID: id, Title: ti.title, Year: ti.year,
		})
	}
	for seriesID, seasons := range excl.Series {
		ti := titleByID[seriesID]
		if len(seasons) == 0 {
			resp.Series = append(resp.Series, recoverExclusionSeriesEntry{
				ID: seriesID, Title: ti.title, Year: ti.year, TvdbID: ti.tvdbID,
			})
			continue
		}
		for _, sn := range seasons {
			resp.Seasons = append(resp.Seasons, recoverExclusionSeasonEntry{
				SeriesID: seriesID, SeasonNumber: sn,
				SeriesTitle: ti.title, Year: ti.year,
			})
		}
	}
	writeJSON(w, resp)
}

// handleAddRecoverExclusions adds the items in the body to the
// per-instance exclusion list. Idempotent — adding an already-excluded
// item is a no-op.
func (s *Server) handleAddRecoverExclusions(w http.ResponseWriter, r *http.Request) {
	instanceID := r.PathValue("instanceId")
	cfg := s.App.Config.Get()
	inst := findInstance(cfg, instanceID)
	if inst == nil {
		writeError(w, 404, "instance not found")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, recoverExclusionMaxBodyBytes)
	var body recoverExclusionRequestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "invalid body: "+err.Error())
		return
	}
	if apiErr := validateExclusionBody(inst.Type, body); apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}

	if err := s.App.Config.Update(func(c *core.Config) {
		if c.RecoverExclusions == nil {
			c.RecoverExclusions = make(map[string]core.RecoverExclusion)
		}
		excl := c.RecoverExclusions[instanceID]
		for _, id := range body.Movies {
			excl.AddMovie(id)
		}
		for _, sid := range body.Series {
			excl.AddSeries(sid)
		}
		for _, ss := range body.Seasons {
			excl.AddSeason(ss.SeriesID, ss.SeasonNumber)
		}
		c.RecoverExclusions[instanceID] = excl
	}); err != nil {
		writeError(w, 500, "save: "+err.Error())
		return
	}
	s.handleListRecoverExclusions(w, r)
}

// handleRemoveRecoverExclusions removes the items in the body. Idempotent
// — removing something that wasn't excluded is a no-op. The "Include
// again" button on the Show-excluded panel routes here.
func (s *Server) handleRemoveRecoverExclusions(w http.ResponseWriter, r *http.Request) {
	instanceID := r.PathValue("instanceId")
	cfg := s.App.Config.Get()
	inst := findInstance(cfg, instanceID)
	if inst == nil {
		writeError(w, 404, "instance not found")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, recoverExclusionMaxBodyBytes)
	var body recoverExclusionRequestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "invalid body: "+err.Error())
		return
	}
	if apiErr := validateExclusionBody(inst.Type, body); apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}

	if err := s.App.Config.Update(func(c *core.Config) {
		if c.RecoverExclusions == nil {
			return
		}
		excl, ok := c.RecoverExclusions[instanceID]
		if !ok {
			return
		}
		for _, id := range body.Movies {
			excl.RemoveMovie(id)
		}
		for _, sid := range body.Series {
			excl.RemoveSeries(sid)
		}
		for _, ss := range body.Seasons {
			excl.RemoveSeason(ss.SeriesID, ss.SeasonNumber)
		}
		// Drop the per-instance entry entirely when nothing's left
		// so the JSON stays compact.
		if len(excl.Movies) == 0 && len(excl.Series) == 0 {
			delete(c.RecoverExclusions, instanceID)
		} else {
			c.RecoverExclusions[instanceID] = excl
		}
	}); err != nil {
		writeError(w, 500, "save: "+err.Error())
		return
	}
	s.handleListRecoverExclusions(w, r)
}

// validateExclusionBody rejects bodies that mix Arr-types, carry
// non-positive IDs, or have nothing to act on. Defends the per-
// instance exclusion semantics + closes the storage-bloat path where
// an authenticated client could pile {-1, 0, MAX_INT} entries into
// the JSON forever (writes go through, scan loop ignores them, but
// disk + memory bloat).
func validateExclusionBody(instType string, body recoverExclusionRequestBody) *apiError {
	if len(body.Movies) == 0 && len(body.Series) == 0 && len(body.Seasons) == 0 {
		return newAPIError(400, "body has no items — set at least one of movies / series / seasons")
	}
	switch instType {
	case "radarr":
		if len(body.Series) > 0 || len(body.Seasons) > 0 {
			return newAPIError(400, "series + seasons exclusions only apply to Sonarr instances")
		}
	case "sonarr":
		if len(body.Movies) > 0 {
			return newAPIError(400, "movies exclusions only apply to Radarr instances")
		}
	default:
		return newAPIError(400, "unknown instance type: "+instType)
	}
	for _, id := range body.Movies {
		if id <= 0 {
			return newAPIError(400, "movie IDs must be positive integers")
		}
	}
	for _, id := range body.Series {
		if id <= 0 {
			return newAPIError(400, "series IDs must be positive integers")
		}
	}
	for _, ss := range body.Seasons {
		if ss.SeriesID <= 0 || ss.SeasonNumber < 0 {
			return newAPIError(400, "seriesId must be positive and seasonNumber non-negative (0 = Specials)")
		}
	}
	return nil
}

// recoverExclusionMaxBodyBytes caps POST/DELETE body size. Bodies
// are tiny (a list of int IDs); anything past 64 KB is either
// confused or hostile.
const recoverExclusionMaxBodyBytes = 64 * 1024

// findInstance returns a pointer to the instance config or nil. Same
// helper pattern other handlers use; kept local to avoid an import-
// cycle hop into core for a one-line lookup.
func findInstance(cfg core.Config, id string) *core.Instance {
	for i := range cfg.Instances {
		if cfg.Instances[i].ID == id {
			return &cfg.Instances[i]
		}
	}
	return nil
}
