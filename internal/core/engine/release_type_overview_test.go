package engine

import (
	"testing"

	"resolvarr/internal/arr"
)

func ef(season int, releaseType string) arr.EpisodeFile {
	return arr.EpisodeFile{SeasonNumber: season, ReleaseType: releaseType}
}

func TestClassifyReleaseTypes_PerSeasonAndSummary(t *testing.T) {
	files := []arr.EpisodeFile{
		ef(1, ReleaseTypeSeasonPack),
		ef(1, ReleaseTypeSeasonPack),
		ef(2, ReleaseTypeSingleEpisode),
		ef(2, ReleaseTypeMultiEpisode),
		ef(2, ""),        // empty -> Unknown
		ef(2, "garbage"), // unrecognised -> Unknown
	}
	got := ClassifyReleaseTypes(files)

	if got.Summary.Total != 6 {
		t.Fatalf("summary Total = %d, want 6", got.Summary.Total)
	}
	if got.Summary.SeasonPack != 2 || got.Summary.SingleEpisode != 1 ||
		got.Summary.MultiEpisode != 1 || got.Summary.Unknown != 2 {
		t.Errorf("summary buckets wrong: %+v", got.Summary)
	}

	// Buckets must always sum to Total.
	sum := got.Summary.SingleEpisode + got.Summary.MultiEpisode +
		got.Summary.SeasonPack + got.Summary.Unknown
	if sum != got.Summary.Total {
		t.Errorf("buckets sum %d != Total %d", sum, got.Summary.Total)
	}

	// Seasons ascending: season 1 then season 2.
	if len(got.Seasons) != 2 {
		t.Fatalf("got %d seasons, want 2", len(got.Seasons))
	}
	if got.Seasons[0].SeasonNumber != 1 || got.Seasons[1].SeasonNumber != 2 {
		t.Errorf("seasons not ascending: %d, %d", got.Seasons[0].SeasonNumber, got.Seasons[1].SeasonNumber)
	}
	if got.Seasons[0].Tally.SeasonPack != 2 || got.Seasons[0].Tally.Total != 2 {
		t.Errorf("season 1 tally wrong: %+v", got.Seasons[0].Tally)
	}
	if got.Seasons[1].Tally.Unknown != 2 || got.Seasons[1].Tally.Total != 4 {
		t.Errorf("season 2 tally wrong: %+v", got.Seasons[1].Tally)
	}
}

func TestClassifyReleaseTypes_Empty(t *testing.T) {
	got := ClassifyReleaseTypes(nil)
	if got.Summary.Total != 0 || len(got.Seasons) != 0 {
		t.Errorf("empty input should yield zero tally and no seasons, got %+v", got)
	}
}
