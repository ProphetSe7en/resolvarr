package engine

import (
	"sort"

	"resolvarr/internal/arr"
)

// release_type_overview.go — pure aggregation for the Sonarr-only
// "Release Type Overview" scan. Sonarr v4 stores how each episode file
// was grabbed in the file's releaseType field, but its UI exposes no
// easy per-file view of it. This classifies a series' episode files by
// that field, per season and overall, so the API handler can surface a
// library-wide picture (and spot the "everything became Single Episode
// after a re-import" loss). Read-only: nothing here writes to Sonarr.

// Sonarr's releaseType values. Anything empty or unrecognised collapses
// into Unknown so the buckets always sum to the file count.
const (
	ReleaseTypeSingleEpisode = "singleEpisode"
	ReleaseTypeMultiEpisode  = "multiEpisode"
	ReleaseTypeSeasonPack    = "seasonPack"
	ReleaseTypeUnknown       = "unknown"
)

// ReleaseTypeTally counts episode files by their stored releaseType.
// The four buckets always sum to Total.
type ReleaseTypeTally struct {
	Total         int
	SingleEpisode int
	MultiEpisode  int
	SeasonPack    int
	Unknown       int
}

// add files one episode file's releaseType into the tally. An empty or
// unrecognised value counts as Unknown (Sonarr reports "unknown" for
// files it never classified, e.g. legacy imports).
func (t *ReleaseTypeTally) add(releaseType string) {
	t.Total++
	switch releaseType {
	case ReleaseTypeSingleEpisode:
		t.SingleEpisode++
	case ReleaseTypeMultiEpisode:
		t.MultiEpisode++
	case ReleaseTypeSeasonPack:
		t.SeasonPack++
	default:
		t.Unknown++
	}
}

// ReleaseTypeSeason is one season's breakdown within a series.
type ReleaseTypeSeason struct {
	SeasonNumber int
	Tally        ReleaseTypeTally
}

// ReleaseTypeSeries is one series' release-type breakdown: per season
// (ascending) plus a series-level summary.
type ReleaseTypeSeries struct {
	Seasons []ReleaseTypeSeason
	Summary ReleaseTypeTally
}

// ClassifyReleaseTypes aggregates a series' episode files by releaseType,
// per season and overall. Pure and deterministic: seasons come back in
// ascending order so the same input always renders the same way.
func ClassifyReleaseTypes(epfiles []arr.EpisodeFile) ReleaseTypeSeries {
	seasonIdx := make(map[int]*ReleaseTypeTally, 4)
	var summary ReleaseTypeTally
	for _, ef := range epfiles {
		summary.add(ef.ReleaseType)
		t := seasonIdx[ef.SeasonNumber]
		if t == nil {
			t = &ReleaseTypeTally{}
			seasonIdx[ef.SeasonNumber] = t
		}
		t.add(ef.ReleaseType)
	}

	nums := make([]int, 0, len(seasonIdx))
	for n := range seasonIdx {
		nums = append(nums, n)
	}
	sort.Ints(nums)

	seasons := make([]ReleaseTypeSeason, 0, len(nums))
	for _, n := range nums {
		seasons = append(seasons, ReleaseTypeSeason{SeasonNumber: n, Tally: *seasonIdx[n]})
	}
	return ReleaseTypeSeries{Seasons: seasons, Summary: summary}
}
