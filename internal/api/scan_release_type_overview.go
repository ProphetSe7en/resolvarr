package api

import (
	"context"
	"net/http"
	"sort"
	"strings"

	"resolvarr/internal/core"
	"resolvarr/internal/core/engine"
)

// scan_release_type_overview.go — Sonarr-only, read-only "Release Type
// Overview" scan. Walks every series, fetches its episode files, and
// aggregates the stored releaseType field per season + per series, plus
// an instance-wide tally. Surfaces what Sonarr's UI hides: which
// episodes are Single Episode / Multi-Episode / Season Pack / Unknown.
//
// Read-only by design: no writes to Sonarr, no apply mode. The field
// itself is write-only via ManualImport (see release-type recovery
// design doc); this scan only reports the current state.

func (s *Server) handleScanReleaseTypeOverview(w http.ResponseWriter, r *http.Request, inst *core.Instance, appType string, req scanRunRequest) {
	ctx, cancel := context.WithTimeout(r.Context(), scanTimeout)
	defer cancel()
	resp, apiErr := s.runReleaseTypeOverview(ctx, inst, req)
	if apiErr != nil {
		s.auditScan(req.auditSource(), "release-type-overview", inst, req, nil, apiErr.Message)
		writeAPIError(w, apiErr)
		return
	}
	s.auditScan(req.auditSource(), "release-type-overview", inst, req, resp, "")
	s.dumpScanJSON("release-type-overview", resp)
	writeJSON(w, resp)
}

func (s *Server) runReleaseTypeOverview(ctx context.Context, inst *core.Instance, req scanRunRequest) (*scanResponse, *apiError) {
	client := s.arrClientFor(inst)
	series, err := client.ListItems(ctx, "sonarr")
	if err != nil {
		return nil, newAPIError(502, "arr list series: "+err.Error())
	}

	resp := &scanResponse{
		Mode:   "preview",
		Action: "release-type-overview",
		Instance: scanInstanceInfo{
			ID:   inst.ID,
			Name: inst.Name,
			Type: inst.Type,
		},
		Totals: scanTotals{Items: len(series)},
	}

	for _, ser := range series {
		epfiles, lerr := client.ListEpisodefiles(ctx, ser.ID)
		if lerr != nil {
			// Per-series fetch failure: skip this series and keep going so
			// one unreachable series doesn't sink the whole overview. The
			// scan is read-only, so there is nothing to roll back.
			continue
		}
		if len(epfiles) == 0 {
			continue // no files on disk yet — nothing to classify
		}

		br := engine.ClassifyReleaseTypes(epfiles)
		item := scanReleaseTypeItem{
			SeriesID:      ser.ID,
			SeriesTitle:   ser.Title,
			Year:          ser.Year,
			TvdbID:        ser.TvdbID,
			Total:         br.Summary.Total,
			SingleEpisode: br.Summary.SingleEpisode,
			MultiEpisode:  br.Summary.MultiEpisode,
			SeasonPack:    br.Summary.SeasonPack,
			Unknown:       br.Summary.Unknown,
		}
		for _, sn := range br.Seasons {
			item.Seasons = append(item.Seasons, scanReleaseTypeSeason{
				SeasonNumber:  sn.SeasonNumber,
				Total:         sn.Tally.Total,
				SingleEpisode: sn.Tally.SingleEpisode,
				MultiEpisode:  sn.Tally.MultiEpisode,
				SeasonPack:    sn.Tally.SeasonPack,
				Unknown:       sn.Tally.Unknown,
			})
		}
		resp.ReleaseTypeOverview = append(resp.ReleaseTypeOverview, item)

		// Instance-wide tally.
		resp.Totals.ReleaseTypeTotal += br.Summary.Total
		resp.Totals.ReleaseTypeSingle += br.Summary.SingleEpisode
		resp.Totals.ReleaseTypeMulti += br.Summary.MultiEpisode
		resp.Totals.ReleaseTypeSeasonPack += br.Summary.SeasonPack
		resp.Totals.ReleaseTypeUnknown += br.Summary.Unknown
	}

	// Sort by series title (case-insensitive) for a stable, readable list.
	sort.Slice(resp.ReleaseTypeOverview, func(i, j int) bool {
		return strings.ToLower(resp.ReleaseTypeOverview[i].SeriesTitle) <
			strings.ToLower(resp.ReleaseTypeOverview[j].SeriesTitle)
	})

	return resp, nil
}
