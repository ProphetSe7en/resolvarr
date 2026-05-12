// Package engine — missing_episodes.go
//
// Sonarr-only "missing episodes" scanner. Walks the (already-fetched)
// episode list for one series and flags seasons that are:
//
//   1. fully aired (every monitored episode's airDateUtc + bufferHours
//      is in the past — gives indexers time to register the latest
//      release)
//   2. at or above the coverage threshold (haveFile / aired_monitored ≥
//      threshold) so brand-new series the user just added but hasn't
//      started filling out don't get flagged
//   3. has at least one missing episode (aired + monitored + !hasFile)
//
// For continuing series the LATEST monitored season is special-cased:
// it's only flagged once it's also fully aired. For ended series every
// monitored season is gated on "fully aired" too (which is automatic
// for an ended series whose finale aired more than 24h ago).
//
// The function is PURE — no I/O, no globals. The caller fetches series
// + episodes (typically via arr.ListSeries + arr.ListEpisodesForSeries
// with a worker pool) and feeds them in.
package engine

import (
	"sort"
	"time"
)

// MissingEpisodeRow is one episode the scan flagged as missing.
type MissingEpisodeRow struct {
	EpisodeID     int       `json:"episodeID"`
	SeasonNumber  int       `json:"seasonNumber"`
	EpisodeNumber int       `json:"episodeNumber"`
	Title         string    `json:"title"`
	AirDateUtc    time.Time `json:"airDateUtc"`
}

// MissingEpisodeSeason groups missing episodes per season for one
// series. CoverageFraction is haveFile / airedMonitored as a 0.0-1.0
// fraction; the season is only kept in the output when both gates pass
// (finished + at or above threshold) AND there's at least one missing
// episode.
type MissingEpisodeSeason struct {
	SeasonNumber     int                 `json:"seasonNumber"`
	AiredMonitored   int                 `json:"airedMonitored"`
	HaveFile         int                 `json:"haveFile"`
	CoverageFraction float64             `json:"coverageFraction"`
	FinishedAiring   bool                `json:"finishedAiring"`
	MissingEpisodes  []MissingEpisodeRow `json:"missingEpisodes"`
}

// MissingEpisodeSeries — output per series. Seasons holds only the
// seasons that qualified (finished + at or above threshold + gaps>0).
// SeasonsScanned counts every monitored season we looked at; the
// difference SeasonsScanned-SeasonsWithGaps tells the UI how many
// seasons were clean.
type MissingEpisodeSeries struct {
	SeriesID        int                    `json:"seriesID"`
	SeriesTitle     string                 `json:"seriesTitle"`
	SeriesStatus    string                 `json:"seriesStatus"`
	SeasonsScanned  int                    `json:"seasonsScanned"`
	SeasonsWithGaps int                    `json:"seasonsWithGaps"`
	Seasons         []MissingEpisodeSeason `json:"seasons"`
}

// ArrSeriesSummary is the subset of Sonarr's series object the engine
// reads. Kept as a separate type from arr.Item so the engine stays
// I/O-free and the test fixtures don't have to construct a full
// arr.Item.
type ArrSeriesSummary struct {
	ID        int    `json:"id"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	Monitored bool   `json:"monitored"`
}

// ArrEpisodeSummary is the subset of Sonarr's episode object the
// engine reads. SeasonNumber 0 is the "specials" season — callers
// typically don't want to flag those, but the engine doesn't filter
// them automatically: if Sonarr says the special is monitored + aired
// + missing, it counts. The handler layer decides whether to skip
// season 0 by pre-filtering episodes; the API handler skips them by
// default and honours an explicit includeSpecials opt-in.
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

// DetectMissingEpisodes runs the per-series scan logic.
//
// threshold is 0.0-1.0 (0.70 for "70% of monitored aired episodes must
// be on disk"). bufferHours is the "finished airing" buffer in hours
// (24 is the default and matches the help-panel copy).
//
// Returns a populated MissingEpisodeSeries with SeasonsScanned set
// even when no seasons qualify, so the UI can show "5 series scanned ·
// 3 with gaps · 2 clean". Seasons in the output are sorted by
// SeasonNumber ascending.
func DetectMissingEpisodes(
	series ArrSeriesSummary,
	episodes []ArrEpisodeSummary,
	threshold float64,
	bufferHours int,
	now time.Time,
) MissingEpisodeSeries {
	out := MissingEpisodeSeries{
		SeriesID:     series.ID,
		SeriesTitle:  series.Title,
		SeriesStatus: series.Status,
	}

	// Bucket monitored episodes by season. Unmonitored episodes are
	// dropped entirely — they don't count toward aired_monitored and
	// they can't be "missing" (the user has explicitly told Sonarr to
	// not download them).
	bySeason := make(map[int][]ArrEpisodeSummary)
	for _, ep := range episodes {
		if !ep.Monitored {
			continue
		}
		bySeason[ep.SeasonNumber] = append(bySeason[ep.SeasonNumber], ep)
	}
	if len(bySeason) == 0 {
		return out
	}

	// Identify the latest monitored season — used for the
	// "continuing series: only flag latest when fully aired" gate.
	latestSeason := -1
	for sn := range bySeason {
		if sn > latestSeason {
			latestSeason = sn
		}
	}

	cutoff := now.Add(-time.Duration(bufferHours) * time.Hour)
	bufferDur := time.Duration(bufferHours) * time.Hour

	seasonNumbers := make([]int, 0, len(bySeason))
	for sn := range bySeason {
		seasonNumbers = append(seasonNumbers, sn)
	}
	sort.Ints(seasonNumbers)

	for _, sn := range seasonNumbers {
		eps := bySeason[sn]

		// First pass: count aired monitored, count haveFile within
		// aired monitored, collect missing rows. We compare each
		// episode's airDateUtc to (now - bufferHours) — same as
		// requiring airDateUtc + bufferHours < now.
		airedMonitored := 0
		have := 0
		var missing []MissingEpisodeRow
		allAired := true
		hasAnyMonitored := false
		for _, ep := range eps {
			hasAnyMonitored = true
			aired := !ep.AirDateUtc.IsZero() && ep.AirDateUtc.Before(cutoff)
			if !aired {
				// Any monitored episode that hasn't aired (yet, or
				// has zero airDate which we treat as "not yet
				// announced") means the season is still in flight.
				allAired = false
				continue
			}
			airedMonitored++
			if ep.HasFile {
				have++
				continue
			}
			missing = append(missing, MissingEpisodeRow{
				EpisodeID:     ep.ID,
				SeasonNumber:  ep.SeasonNumber,
				EpisodeNumber: ep.EpisodeNumber,
				Title:         ep.Title,
				AirDateUtc:    ep.AirDateUtc,
			})
		}
		if !hasAnyMonitored {
			continue
		}

		// "Finished airing" — every monitored episode in this season
		// has airDateUtc + bufferHours in the past. If any episode
		// has a zero airDateUtc OR an airDate in the future relative
		// to the buffer, the season is not finished.
		//
		// allAired is the per-episode bool we accumulated above; it's
		// already false if any monitored episode's airDateUtc was
		// after the cutoff OR zero. We materialise the bool on the
		// season object for the UI.
		finished := allAired
		_ = bufferDur // bufferDur retained for clarity; cutoff already encodes it

		// Continuing-series gate: for the LATEST monitored season,
		// only consider it qualifying when fully aired. Older
		// seasons are always candidates regardless of series status
		// (handles the "S01-S03 had gaps but S04 still airing"
		// pattern correctly).
		//
		// Seasons that are still airing (continuing-latest-not-
		// finished, or any series with future-airing episodes) are
		// NOT counted in SeasonsScanned — they aren't evaluation
		// candidates yet. SeasonsScanned reflects "seasons we made a
		// finished-or-not-finished judgement against" so the UI's
		// clean-count is SeasonsScanned - SeasonsWithGaps.
		isLatest := sn == latestSeason
		if isContinuing(series.Status) && isLatest && !finished {
			continue
		}

		if !finished {
			// For ended / upcoming series with a non-finished season:
			// can only happen if airDateUtc data is missing / future.
			// Treat as "not yet ready" — same skip, not counted.
			continue
		}

		out.SeasonsScanned++

		if airedMonitored == 0 {
			// Edge case: every monitored episode in this season has
			// a zero airDateUtc and we've still hit "finished"
			// (impossible given allAired logic, but defensive).
			continue
		}

		coverage := float64(have) / float64(airedMonitored)
		if coverage < threshold {
			continue
		}
		if len(missing) == 0 {
			// Season is complete — no gaps to flag.
			continue
		}

		// Sort missing episodes by episode number for stable UI ordering.
		sort.Slice(missing, func(i, j int) bool {
			return missing[i].EpisodeNumber < missing[j].EpisodeNumber
		})

		out.Seasons = append(out.Seasons, MissingEpisodeSeason{
			SeasonNumber:     sn,
			AiredMonitored:   airedMonitored,
			HaveFile:         have,
			CoverageFraction: coverage,
			FinishedAiring:   finished,
			MissingEpisodes:  missing,
		})
		out.SeasonsWithGaps++
	}

	return out
}

// isContinuing returns true when the Sonarr series status indicates
// the show may still be releasing episodes. "continuing" is Sonarr's
// canonical value; we also accept the rarer "upcoming" (pre-pilot)
// defensively. Anything else (ended, deleted, "") is treated as a
// finished show whose seasons can be flagged whenever they qualify.
func isContinuing(status string) bool {
	switch status {
	case "continuing", "upcoming":
		return true
	}
	return false
}
