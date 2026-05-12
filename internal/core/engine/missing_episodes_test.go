package engine

import (
	"testing"
	"time"
)

// Test fixtures use a fixed "now" so cutoff math is deterministic.
//
// now = 2026-05-01T00:00:00Z
// bufferHours = 24 → cutoff = 2026-04-30T00:00:00Z
//
// Episodes aired before cutoff = aired; after = not yet.
var (
	tNow       = time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	tCutoff    = tNow.Add(-24 * time.Hour)
	tLongAgo   = tNow.Add(-365 * 24 * time.Hour) // a year ago
	tWeekAgo   = tNow.Add(-7 * 24 * time.Hour)
	tDayBefore = tCutoff.Add(-1 * time.Hour) // safely aired
	tDayAfter  = tCutoff.Add(1 * time.Hour)  // hasn't cleared the buffer yet
)

func mkEp(id, season, ep int, aired time.Time, monitored, hasFile bool) ArrEpisodeSummary {
	return ArrEpisodeSummary{
		ID:            id,
		SeasonNumber:  season,
		EpisodeNumber: ep,
		Title:         "ep" + itoaTest(id),
		AirDateUtc:    aired,
		Monitored:     monitored,
		HasFile:       hasFile,
	}
}

func itoaTest(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

func TestDetectMissingEpisodes(t *testing.T) {
	threshold := 0.7
	buffer := 24

	tests := []struct {
		name          string
		series        ArrSeriesSummary
		episodes      []ArrEpisodeSummary
		wantScanned   int
		wantWithGaps  int
		wantSeasonsLen int
		// Per-season expectations (only checked when set)
		wantMissingTotal int
	}{
		{
			name: "empty input",
			series: ArrSeriesSummary{ID: 1, Title: "Empty", Status: "ended"},
			episodes: nil,
			wantScanned: 0, wantWithGaps: 0, wantSeasonsLen: 0,
		},
		{
			name: "all unmonitored",
			series: ArrSeriesSummary{ID: 1, Title: "Unmon", Status: "ended"},
			episodes: []ArrEpisodeSummary{
				mkEp(1, 1, 1, tLongAgo, false, false),
				mkEp(2, 1, 2, tLongAgo, false, false),
			},
			wantScanned: 0, wantWithGaps: 0, wantSeasonsLen: 0,
		},
		{
			name: "single season fully complete — no flag",
			series: ArrSeriesSummary{ID: 1, Title: "Complete", Status: "ended"},
			episodes: []ArrEpisodeSummary{
				mkEp(1, 1, 1, tLongAgo, true, true),
				mkEp(2, 1, 2, tLongAgo, true, true),
				mkEp(3, 1, 3, tLongAgo, true, true),
			},
			wantScanned: 1, wantWithGaps: 0, wantSeasonsLen: 0,
		},
		{
			name: "single season below threshold — skip",
			series: ArrSeriesSummary{ID: 1, Title: "Low", Status: "ended"},
			episodes: []ArrEpisodeSummary{
				mkEp(1, 1, 1, tLongAgo, true, true),
				mkEp(2, 1, 2, tLongAgo, true, false),
				mkEp(3, 1, 3, tLongAgo, true, false),
				mkEp(4, 1, 4, tLongAgo, true, false),
				mkEp(5, 1, 5, tLongAgo, true, false),
			},
			// 1/5 = 20% < 70% → skipped
			wantScanned: 1, wantWithGaps: 0, wantSeasonsLen: 0,
		},
		{
			name: "single gap above threshold — flagged",
			series: ArrSeriesSummary{ID: 1, Title: "Gap", Status: "ended"},
			episodes: []ArrEpisodeSummary{
				mkEp(1, 1, 1, tLongAgo, true, true),
				mkEp(2, 1, 2, tLongAgo, true, true),
				mkEp(3, 1, 3, tLongAgo, true, false), // missing
				mkEp(4, 1, 4, tLongAgo, true, true),
				mkEp(5, 1, 5, tLongAgo, true, true),
			},
			// 4/5 = 80% ≥ 70% → flagged with one missing
			wantScanned: 1, wantWithGaps: 1, wantSeasonsLen: 1,
			wantMissingTotal: 1,
		},
		{
			name: "multi-season mixed: S1 complete, S2 has gap",
			series: ArrSeriesSummary{ID: 1, Title: "Mixed", Status: "ended"},
			episodes: []ArrEpisodeSummary{
				mkEp(1, 1, 1, tLongAgo, true, true),
				mkEp(2, 1, 2, tLongAgo, true, true),
				// S2 has 5 episodes; 4 have file (80%), one missing.
				mkEp(3, 2, 1, tLongAgo, true, true),
				mkEp(4, 2, 2, tLongAgo, true, false), // missing
				mkEp(5, 2, 3, tLongAgo, true, true),
				mkEp(6, 2, 4, tLongAgo, true, true),
				mkEp(7, 2, 5, tLongAgo, true, true),
			},
			wantScanned: 2, wantWithGaps: 1, wantSeasonsLen: 1,
			wantMissingTotal: 1,
		},
		{
			name: "continuing — last season still airing — skip last; older seasons flagged",
			series: ArrSeriesSummary{ID: 1, Title: "Cont", Status: "continuing"},
			episodes: []ArrEpisodeSummary{
				// S1 fully aired with a gap
				mkEp(1, 1, 1, tLongAgo, true, true),
				mkEp(2, 1, 2, tLongAgo, true, false), // missing
				mkEp(3, 1, 3, tLongAgo, true, true),
				mkEp(4, 1, 4, tLongAgo, true, true),
				mkEp(5, 1, 5, tLongAgo, true, true),
				// S2 partially aired
				mkEp(10, 2, 1, tLongAgo, true, true),
				mkEp(11, 2, 2, tLongAgo, true, true),
				mkEp(12, 2, 3, tWeekAgo, true, true),
				mkEp(13, 2, 4, tDayAfter, true, false), // not yet aired
				mkEp(14, 2, 5, tDayAfter, true, false), // not yet aired
			},
			wantScanned: 1, wantWithGaps: 1, wantSeasonsLen: 1,
			wantMissingTotal: 1,
		},
		{
			name: "continuing — last season fully aired with gap — flagged",
			series: ArrSeriesSummary{ID: 1, Title: "ContDone", Status: "continuing"},
			episodes: []ArrEpisodeSummary{
				// S1 perfect
				mkEp(1, 1, 1, tLongAgo, true, true),
				mkEp(2, 1, 2, tLongAgo, true, true),
				// S2 fully aired with one gap
				mkEp(10, 2, 1, tLongAgo, true, true),
				mkEp(11, 2, 2, tLongAgo, true, false), // missing
				mkEp(12, 2, 3, tLongAgo, true, true),
				mkEp(13, 2, 4, tLongAgo, true, true),
			},
			wantScanned: 2, wantWithGaps: 1, wantSeasonsLen: 1,
			wantMissingTotal: 1,
		},
		{
			name: "ended series with gaps in middle seasons",
			series: ArrSeriesSummary{ID: 1, Title: "EndMid", Status: "ended"},
			episodes: []ArrEpisodeSummary{
				// S1 perfect
				mkEp(1, 1, 1, tLongAgo, true, true),
				mkEp(2, 1, 2, tLongAgo, true, true),
				// S2 gap (4/5 = 80%)
				mkEp(10, 2, 1, tLongAgo, true, true),
				mkEp(11, 2, 2, tLongAgo, true, true),
				mkEp(12, 2, 3, tLongAgo, true, false), // missing
				mkEp(13, 2, 4, tLongAgo, true, true),
				mkEp(14, 2, 5, tLongAgo, true, true),
				// S3 perfect
				mkEp(20, 3, 1, tLongAgo, true, true),
				mkEp(21, 3, 2, tLongAgo, true, true),
			},
			wantScanned: 3, wantWithGaps: 1, wantSeasonsLen: 1,
			wantMissingTotal: 1,
		},
		{
			name: "season with one episode not past buffer — not finished — skip + not counted",
			series: ArrSeriesSummary{ID: 1, Title: "Buffer", Status: "ended"},
			episodes: []ArrEpisodeSummary{
				mkEp(1, 1, 1, tLongAgo, true, true),
				mkEp(2, 1, 2, tLongAgo, true, true),
				mkEp(3, 1, 3, tDayAfter, true, false), // aired but inside 24h buffer
			},
			// SeasonsScanned excludes still-airing seasons (engine
			// reserves the counter for seasons we made a finished/not
			// judgement against — the UI's clean-count math depends
			// on that).
			wantScanned: 0, wantWithGaps: 0, wantSeasonsLen: 0,
		},
		{
			name: "season just past buffer — finished — flagged",
			series: ArrSeriesSummary{ID: 1, Title: "JustPast", Status: "ended"},
			episodes: []ArrEpisodeSummary{
				mkEp(1, 1, 1, tLongAgo, true, true),
				mkEp(2, 1, 2, tLongAgo, true, true),
				mkEp(3, 1, 3, tLongAgo, true, true),
				mkEp(4, 1, 4, tLongAgo, true, false), // missing
				mkEp(5, 1, 5, tDayBefore, true, true), // aired just past buffer
			},
			wantScanned: 1, wantWithGaps: 1, wantSeasonsLen: 1,
			wantMissingTotal: 1,
		},
		{
			name: "unmonitored episodes don't count toward aired_monitored",
			series: ArrSeriesSummary{ID: 1, Title: "UnmonSkip", Status: "ended"},
			episodes: []ArrEpisodeSummary{
				mkEp(1, 1, 1, tLongAgo, true, true),
				mkEp(2, 1, 2, tLongAgo, true, true),
				mkEp(3, 1, 3, tLongAgo, true, false), // missing
				mkEp(4, 1, 4, tLongAgo, true, true),
				mkEp(5, 1, 5, tLongAgo, true, true),
				// Two unmonitored episodes that would skew the math
				// if they were included.
				mkEp(6, 1, 6, tLongAgo, false, false),
				mkEp(7, 1, 7, tLongAgo, false, false),
			},
			// 4/5 monitored aired, 1 missing → 80% coverage, flagged
			wantScanned: 1, wantWithGaps: 1, wantSeasonsLen: 1,
			wantMissingTotal: 1,
		},
		{
			name: "continuing — only one season exists and it's still airing — not counted",
			series: ArrSeriesSummary{ID: 1, Title: "Brand", Status: "continuing"},
			episodes: []ArrEpisodeSummary{
				mkEp(1, 1, 1, tLongAgo, true, true),
				mkEp(2, 1, 2, tDayAfter, true, false), // not yet aired
			},
			wantScanned: 0, wantWithGaps: 0, wantSeasonsLen: 0,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := DetectMissingEpisodes(tc.series, tc.episodes, threshold, buffer, tNow)
			if got.SeasonsScanned != tc.wantScanned {
				t.Errorf("SeasonsScanned: got %d want %d", got.SeasonsScanned, tc.wantScanned)
			}
			if got.SeasonsWithGaps != tc.wantWithGaps {
				t.Errorf("SeasonsWithGaps: got %d want %d", got.SeasonsWithGaps, tc.wantWithGaps)
			}
			if len(got.Seasons) != tc.wantSeasonsLen {
				t.Errorf("len(Seasons): got %d want %d", len(got.Seasons), tc.wantSeasonsLen)
			}
			if tc.wantMissingTotal > 0 {
				totalMissing := 0
				for _, s := range got.Seasons {
					totalMissing += len(s.MissingEpisodes)
				}
				if totalMissing != tc.wantMissingTotal {
					t.Errorf("total missing episodes: got %d want %d", totalMissing, tc.wantMissingTotal)
				}
			}
		})
	}
}

// TestDetectMissingEpisodes_PreservesSeriesMeta checks that we always
// populate seriesID + title + status on the output, even when no
// seasons qualify. This is what lets the UI display "5 series scanned
// · 3 with gaps · 2 clean" — the API handler aggregates clean series
// counts from the scanned-but-no-gaps responses.
func TestDetectMissingEpisodes_PreservesSeriesMeta(t *testing.T) {
	got := DetectMissingEpisodes(
		ArrSeriesSummary{ID: 42, Title: "Mortimer's Vortex", Status: "ended"},
		nil, 0.7, 24, tNow,
	)
	if got.SeriesID != 42 {
		t.Errorf("SeriesID: got %d want 42", got.SeriesID)
	}
	if got.SeriesTitle != "Mortimer's Vortex" {
		t.Errorf("SeriesTitle: got %q want %q", got.SeriesTitle, "Mortimer's Vortex")
	}
	if got.SeriesStatus != "ended" {
		t.Errorf("SeriesStatus: got %q want %q", got.SeriesStatus, "ended")
	}
}

// TestDetectMissingEpisodes_MissingRowOrder verifies that missing rows
// are sorted by episode number so the UI renders them in natural
// order regardless of the input slice order.
func TestDetectMissingEpisodes_MissingRowOrder(t *testing.T) {
	series := ArrSeriesSummary{ID: 1, Title: "OrderTest", Status: "ended"}
	episodes := []ArrEpisodeSummary{
		mkEp(1, 1, 1, tLongAgo, true, true),
		mkEp(5, 1, 5, tLongAgo, true, false), // missing — comes first in input
		mkEp(2, 1, 2, tLongAgo, true, false), // missing
		mkEp(3, 1, 3, tLongAgo, true, true),
		mkEp(4, 1, 4, tLongAgo, true, true),
		mkEp(6, 1, 6, tLongAgo, true, true),
		mkEp(7, 1, 7, tLongAgo, true, true),
		mkEp(8, 1, 8, tLongAgo, true, true),
		mkEp(9, 1, 9, tLongAgo, true, true),
		mkEp(10, 1, 10, tLongAgo, true, true),
	}
	got := DetectMissingEpisodes(series, episodes, 0.7, 24, tNow)
	if len(got.Seasons) != 1 {
		t.Fatalf("expected 1 season, got %d", len(got.Seasons))
	}
	missing := got.Seasons[0].MissingEpisodes
	if len(missing) != 2 {
		t.Fatalf("expected 2 missing, got %d", len(missing))
	}
	if missing[0].EpisodeNumber != 2 || missing[1].EpisodeNumber != 5 {
		t.Errorf("missing not sorted: got [%d, %d] want [2, 5]",
			missing[0].EpisodeNumber, missing[1].EpisodeNumber)
	}
}
