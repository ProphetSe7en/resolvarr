package engine

import (
	"reflect"
	"testing"
)

func TestParseSeasonEpisodeFromTitle(t *testing.T) {
	cases := []struct {
		name       string
		title      string
		wantSeason int
		wantEps    []int
		wantOk     bool
	}{
		{"single episode S01E05",
			"Show.Name.S01E05.1080p.WEB-DL-FLUX",
			1, []int{5}, true},
		{"multi-episode S01E05E06",
			"Show.Name.S01E05E06.1080p.WEB-DL-FLUX",
			1, []int{5, 6}, true},
		{"three-episode S01E05E06E07",
			"Show.Name.S01E05E06E07.1080p.WEB-DL-FLUX",
			1, []int{5, 6, 7}, true},
		{"split episodes S01E05.E06",
			"Show.Name.S01E05.E06.1080p.WEB-DL-FLUX",
			1, []int{5, 6}, true},
		{"S01 season pack",
			"Show.Name.S01.Complete.1080p.WEB-DL-FLUX",
			1, nil, true},
		{"Season 1 worded",
			"Show.Name.Season.1.Complete.WEB-DL-FLUX",
			1, nil, true},
		{"S12 double-digit",
			"Show.Name.S12E03.1080p-FLUX",
			12, []int{3}, true},
		{"no S/E token",
			"Show.Name.2024.1080p.WEB-DL-FLUX",
			0, nil, false},
		{"empty title",
			"", 0, nil, false},
		{"S00 — invalid season",
			"Show.S00E01-FLUX",
			0, nil, false},
		{"unsorted multi-ep regex parser still emits sorted",
			"Show.S01E10E05-FLUX",
			1, []int{5, 10}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotSeason, gotEps, gotOk := ParseSeasonEpisodeFromTitle(c.title)
			if gotSeason != c.wantSeason || gotOk != c.wantOk {
				t.Errorf("ParseSeasonEpisodeFromTitle(%q) = (%d, %v, %v), want (%d, %v, %v)",
					c.title, gotSeason, gotEps, gotOk, c.wantSeason, c.wantEps, c.wantOk)
				return
			}
			if !reflect.DeepEqual(gotEps, c.wantEps) {
				t.Errorf("episodes = %v, want %v", gotEps, c.wantEps)
			}
		})
	}
}

// TestDetermineQbitTag covers the three-rule first-match-wins model.
// Mirrors the Python qbittorrent_auto_tagger.py reference behaviour
// — Episode wins over Season, Unmatched is the catch-all, disabling
// a rule short-circuits without falling through to the next class.
func TestDetermineQbitTag(t *testing.T) {
	allOn := QbitSeRulesView{
		EpisodeEnabled: true, EpisodeTag: "Episode",
		SeasonEnabled: true, SeasonTag: "Season",
		UnmatchedEnabled: true, UnmatchedTag: "Unmatched",
	}
	episodeOnly := QbitSeRulesView{
		EpisodeEnabled: true, EpisodeTag: "Episode",
	}
	seasonOnly := QbitSeRulesView{
		SeasonEnabled: true, SeasonTag: "Season",
	}
	unmatchedOnly := QbitSeRulesView{
		UnmatchedEnabled: true, UnmatchedTag: "Unmatched",
	}
	custom := QbitSeRulesView{
		EpisodeEnabled: true, EpisodeTag: "ep",
		SeasonEnabled: true, SeasonTag: "sn",
		UnmatchedEnabled: true, UnmatchedTag: "un",
	}
	allOff := QbitSeRulesView{}

	cases := []struct {
		name string
		in   string
		cfg  QbitSeRulesView
		want string
	}{
		// Episode wins
		{"S01E05 single episode", "Show.S01E05.WEB-DL-FLUX", allOn, "Episode"},
		{"S01E05E06 multi-episode", "Show.S01E05E06.WEB-DL-FLUX", allOn, "Episode"},
		{"S12E03 double-digit season", "Show.S12E03-FLUX", allOn, "Episode"},
		{"daily-show 2024.10.15", "Show.2024.10.15.1080p.WEB-DL-FLUX", allOn, "Episode"},
		{"daily-show 2024-10-15 hyphenated", "Show.2024-10-15.WEB-DL-FLUX", allOn, "Episode"},
		{"daily-show with spaces", "Show 2024 10 15 WEB-DL FLUX", allOn, "Episode"},
		// Season — bare S01 / Season 1, no episode token
		{"bare S01 season pack", "Show.S01.Complete.WEB-DL-FLUX", allOn, "Season"},
		{"Season.1 worded", "Show.Season.1.Complete.WEB-DL-FLUX", allOn, "Season"},
		{"Season 1 spaced", "Show Season 1 Complete WEB-DL FLUX", allOn, "Season"},
		// Unmatched — neither pattern matched
		{"movie no S/E token", "Movie.2024.1080p.WEB-DL-FLUX", allOn, "Unmatched"},
		{"music release", "Album.Name.2024.FLAC", allOn, "Unmatched"},
		{"software ISO", "ubuntu-24.04-desktop-amd64.iso", allOn, "Unmatched"},
		// Episode-only mode
		{"episode-only on episode → tag", "Show.S01E05-FLUX", episodeOnly, "Episode"},
		{"episode-only on season pack → empty", "Show.S01.Complete-FLUX", episodeOnly, ""},
		{"episode-only on movie → empty", "Movie.2024-FLUX", episodeOnly, ""},
		// Season-only mode
		{"season-only on episode → empty (epMatched short-circuits)", "Show.S01E05-FLUX", seasonOnly, ""},
		{"season-only on season pack → tag", "Show.S01.Complete-FLUX", seasonOnly, "Season"},
		{"season-only on movie → empty", "Movie.2024-FLUX", seasonOnly, ""},
		// Unmatched-only mode
		{"unmatched-only on episode → empty (epMatched short-circuits)", "Show.S01E05-FLUX", unmatchedOnly, ""},
		{"unmatched-only on season → empty (seasonMatched short-circuits)", "Show.S01.Complete-FLUX", unmatchedOnly, ""},
		{"unmatched-only on movie → tag", "Movie.2024-FLUX", unmatchedOnly, "Unmatched"},
		// Custom tag names
		{"custom episode name", "Show.S01E05-FLUX", custom, "ep"},
		{"custom season name", "Show.S01.Complete-FLUX", custom, "sn"},
		{"custom unmatched name", "Movie.2024-FLUX", custom, "un"},
		// All off
		{"all-off on episode", "Show.S01E05-FLUX", allOff, ""},
		{"all-off on movie", "Movie.2024-FLUX", allOff, ""},
		// Empty input
		{"empty torrent name", "", allOn, ""},
		// Empty tag string falls back to default
		{"empty episode tag → default Episode", "Show.S01E05-FLUX",
			QbitSeRulesView{EpisodeEnabled: true}, "Episode"},
		{"empty season tag → default Season", "Show.S01.Complete-FLUX",
			QbitSeRulesView{SeasonEnabled: true}, "Season"},
		{"empty unmatched tag → default Unmatched", "Movie.2024-FLUX",
			QbitSeRulesView{UnmatchedEnabled: true}, "Unmatched"},
		// Whitespace-only tag string falls back too
		{"whitespace tag → default", "Show.S01E05-FLUX",
			QbitSeRulesView{EpisodeEnabled: true, EpisodeTag: "   "}, "Episode"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := DetermineQbitTag(c.in, c.cfg)
			if got != c.want {
				t.Errorf("DetermineQbitTag(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
