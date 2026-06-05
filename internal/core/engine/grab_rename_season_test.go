package engine

import "testing"

func TestParseSeasonEpisodeToken(t *testing.T) {
	cases := []struct {
		name       string
		in         string
		wantToken  string
		wantSeason int
		wantOK     bool
	}{
		{"scene file", "the.last.kingdom.s03e01.proper.1080p.web.x264-strife.mkv", "S03E01", 3, true},
		{"upper", "The.Last.Kingdom.S03E10.1080p.NF.WEB-DL.x264-NTb.mkv", "S03E10", 3, true},
		{"multi-ep E-form", "show.s01e05e06.1080p.web-dl-grp.mkv", "S01E05E06", 1, true},
		{"multi-ep dash", "show.s01e05-e06.1080p.web-dl-grp.mkv", "S01E05-E06", 1, true},
		{"three-digit season", "show.s123e04.mkv", "S123E04", 123, true},
		{"no episode token", "the.last.kingdom.s03.1080p.web-dl-grp.mkv", "", 0, false},
		{"season-only word", "The Last Kingdom Season 3 1080p", "", 0, false},
		{"no match", "random.movie.2024.1080p.bluray-x264.mkv", "", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tok, s, ok := ParseSeasonEpisodeToken(tc.in)
			if ok != tc.wantOK || tok != tc.wantToken || s != tc.wantSeason {
				t.Fatalf("ParseSeasonEpisodeToken(%q) = (%q,%d,%v), want (%q,%d,%v)",
					tc.in, tok, s, ok, tc.wantToken, tc.wantSeason, tc.wantOK)
			}
		})
	}
}

func TestBuildSeasonPackEpisodeTitle(t *testing.T) {
	cases := []struct {
		name     string
		grab     string
		token    string
		season   int
		want     string
		wantOK   bool
	}{
		{
			name:   "the last kingdom S03 → S03E01",
			grab:   "The Last Kingdom S03 1080p Proper NF WEB-DL DD+ 5.1 x264-STRiFE",
			token:  "S03E01", season: 3,
			want:   "The Last Kingdom S03E01 1080p Proper NF WEB-DL DD+ 5.1 x264-STRiFE",
			wantOK: true,
		},
		{
			name:   "short Sx form",
			grab:   "Some Show S3 1080p WEB-DL-GRP",
			token:  "S03E05", season: 3,
			want:   "Some Show S03E05 1080p WEB-DL-GRP",
			wantOK: true,
		},
		{
			name:   "Season word form",
			grab:   "Some Show Season 03 1080p WEB-DL-GRP",
			token:  "S03E05", season: 3,
			want:   "Some Show S03E05 1080p WEB-DL-GRP",
			wantOK: true,
		},
		{
			name:   "multi-episode token",
			grab:   "Some Show S01 1080p WEB-DL-GRP",
			token:  "S01E05E06", season: 1,
			want:   "Some Show S01E05E06 1080p WEB-DL-GRP",
			wantOK: true,
		},
		{
			name:   "lowercase token normalised",
			grab:   "Some Show S02 1080p WEB-DL-GRP",
			token:  "s02e03", season: 2,
			want:   "Some Show S02E03 1080p WEB-DL-GRP",
			wantOK: true,
		},
		{
			name:   "grab already per-episode → no season-only token",
			grab:   "Some Show S03E01 1080p WEB-DL-GRP",
			token:  "S03E01", season: 3,
			want:   "", wantOK: false,
		},
		{
			name:   "no matching season token",
			grab:   "Some Show 1080p WEB-DL-GRP",
			token:  "S03E01", season: 3,
			want:   "", wantOK: false,
		},
		{
			name:   "wrong season number not matched",
			grab:   "Some Show S05 1080p WEB-DL-GRP",
			token:  "S03E01", season: 3,
			want:   "", wantOK: false,
		},
		{
			name:   "malformed token",
			grab:   "Some Show S03 1080p WEB-DL-GRP",
			token:  "", season: 0,
			want:   "", wantOK: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := BuildSeasonPackEpisodeTitle(tc.grab, tc.token, tc.season)
			if ok != tc.wantOK || got != tc.want {
				t.Fatalf("BuildSeasonPackEpisodeTitle(%q,%q,%d) = (%q,%v), want (%q,%v)",
					tc.grab, tc.token, tc.season, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}
