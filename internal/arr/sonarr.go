package arr

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// sonarr.go — Sonarr-only helpers. Today the only callers are the
// Missing Episodes scanner (Tag Library → Sonarr → Missing episodes);
// the existing Sonarr Recover + Audio/Video paths still live in arr.go
// since they share infrastructure with the Radarr helpers there. As
// the Sonarr surface grows, more Sonarr-specific helpers can migrate
// here.

// ArrSeriesSummary is the subset of Sonarr's /api/v3/series object
// the engine consumes. Mirrors engine.ArrSeriesSummary on purpose —
// callers fetch via this type and pass straight through to
// engine.DetectMissingEpisodes.
type ArrSeriesSummary struct {
	ID        int    `json:"id"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	Monitored bool   `json:"monitored"`
}

// ArrEpisodeSummary is the subset of Sonarr's /api/v3/episode object
// the engine consumes for the missing-episodes scan. We only read the
// fields needed for the "is this episode aired + monitored + missing"
// gate plus identity bits (episodeID + S/E numbers + title) for the
// UI drill-in and Sonarr's EpisodeSearch command body.
type ArrEpisodeSummary struct {
	ID            int       `json:"id"`
	SeriesID      int       `json:"seriesId"`
	SeasonNumber  int       `json:"seasonNumber"`
	EpisodeNumber int       `json:"episodeNumber"`
	Title         string    `json:"title"`
	AirDateUtc    time.Time `json:"airDateUtc"`
	Monitored     bool      `json:"monitored"`
	HasFile       bool      `json:"hasFile"`
}

// ListSeries fetches every series on the Sonarr instance and projects
// the response down to ArrSeriesSummary. Sonarr's /api/v3/series
// returns a flat array (no pagination). The full series object
// includes statistics + seasons + alternativeTitles which we don't
// need for the missing-episodes scan — projecting at decode time
// keeps the engine input shape small + obvious.
func (c *Client) ListSeries(ctx context.Context) ([]ArrSeriesSummary, error) {
	resp, err := c.do(ctx, "GET", "/api/v3/series", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var out []ArrSeriesSummary
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("parse series: %w", err)
	}
	return out, nil
}

// ListEpisodesForSeries fetches every episode for one series. Sonarr's
// /api/v3/episode?seriesId=X returns a flat array regardless of
// season/episode count. Series with hundreds of episodes return in <
// 5s on a healthy Sonarr, so we don't bother paginating.
//
// Episodes that haven't been announced yet may have a zero
// airDateUtc — DetectMissingEpisodes treats those as "not aired"
// without needing a tristate.
func (c *Client) ListEpisodesForSeries(ctx context.Context, seriesID int) ([]ArrEpisodeSummary, error) {
	path := fmt.Sprintf("/api/v3/episode?seriesId=%d", seriesID)
	resp, err := c.do(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var out []ArrEpisodeSummary
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("parse episodes: %w", err)
	}
	return out, nil
}

// SearchEpisodes triggers Sonarr's normal indexer search for the given
// episodes. POST /api/v3/command with body
// {"name":"EpisodeSearch","episodeIds":[...]} — Sonarr queues the
// search internally + handles throttling against the configured
// indexers. The command returns 201 with a command ID; we discard the
// ID since the UI doesn't poll for completion (Sonarr drives the
// search queue itself, the user can watch progress in Sonarr's
// Activity tab if they want to).
//
// Returns nil immediately on an empty slice — Sonarr would 400 on an
// empty episodeIds array.
func (c *Client) SearchEpisodes(ctx context.Context, episodeIDs []int) error {
	if len(episodeIDs) == 0 {
		return nil
	}
	body := map[string]any{
		"name":       "EpisodeSearch",
		"episodeIds": episodeIDs,
	}
	resp, err := c.do(ctx, "POST", "/api/v3/command", body)
	if err != nil {
		return err
	}
	return unwrap(resp)
}
