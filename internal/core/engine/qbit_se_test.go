package engine

import (
	"reflect"
	"testing"
)

func TestParseSeasonEpisodeFromTitle(t *testing.T) {
	cases := []struct {
		name        string
		title       string
		wantSeason  int
		wantEps     []int
		wantOk      bool
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

func TestQbitSeasonEpisodeTags(t *testing.T) {
	bothOn := QbitSeRulesView{TagSeason: true, TagEpisode: true}
	seasonOnly := QbitSeRulesView{TagSeason: true, TagEpisode: false}
	episodeOnly := QbitSeRulesView{TagSeason: false, TagEpisode: true}
	bothOff := QbitSeRulesView{}

	cases := []struct {
		name           string
		season         int
		episodes       []int
		totalEpsKnown  int
		cfg            QbitSeRulesView
		want           []string
	}{
		{"single episode, both on",
			1, []int{5}, 0, bothOn,
			[]string{"S01", "S01E05"}},
		{"single episode, season only",
			1, []int{5}, 0, seasonOnly,
			[]string{"S01"}},
		{"single episode, episode only",
			1, []int{5}, 0, episodeOnly,
			[]string{"S01E05"}},
		{"multi-episode (S01E05E06), both on",
			1, []int{5, 6}, 0, bothOn,
			[]string{"S01", "S01E05E06"}},
		{"multi-episode unsorted input — sorts ascending",
			1, []int{6, 5}, 0, episodeOnly,
			[]string{"S01E05E06"}},
		{"three-episode multi-ep (S01E05E06E07)",
			1, []int{5, 6, 7}, 0, episodeOnly,
			[]string{"S01E05E06E07"}},
		{"empty episodes — season pack",
			1, nil, 0, bothOn,
			[]string{"S01"}},
		{"explicit season-pack via totalEps match",
			1, []int{1, 2, 3, 4, 5}, 5, bothOn,
			[]string{"S01"}},
		{"≥10 episodes fallback heuristic",
			1, []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, 0, bothOn,
			[]string{"S01"}},
		{"both formats off — empty",
			1, []int{5}, 0, bothOff,
			nil},
		{"invalid season",
			0, []int{5}, 0, bothOn,
			nil},
		{"season 12 — double-digit padding",
			12, []int{3}, 0, bothOn,
			[]string{"S12", "S12E03"}},
		{"duplicate episode IDs deduped",
			1, []int{5, 5, 6, 5}, 0, episodeOnly,
			[]string{"S01E05E06"}},
		{"zero/negative episode numbers filtered",
			1, []int{0, -1, 5}, 0, episodeOnly,
			[]string{"S01E05"}},
		{"season-only with empty episodes",
			3, nil, 0, seasonOnly,
			[]string{"S03"}},
		{"episode-only mode + season-pack input → empty (no episode tag for season pack)",
			1, nil, 0, episodeOnly,
			nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := QbitSeasonEpisodeTags(c.season, c.episodes, c.totalEpsKnown, c.cfg)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}
