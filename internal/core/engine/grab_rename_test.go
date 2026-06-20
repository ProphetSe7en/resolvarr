package engine

import (
	"reflect"
	"regexp"
	"testing"
)

// grab_rename_test.go — coverage for the built-in vocabularies + diff
// helpers + scene-detection. Pure functions; full coverage cheap.

func TestDiffMissingMovieVersions(t *testing.T) {
	cases := []struct {
		name    string
		current string
		grab    string
		want    []string
	}{
		{"theatrical missing",
			"Movie 2024 1080p WEB-DL-FLUX",
			"Movie 2024 Theatrical 1080p WEB-DL-FLUX",
			[]string{"Theatrical"}},
		{"director's cut + IMAX missing",
			"Movie 2024 1080p WEB-DL-FLUX",
			"Movie 2024 Director's Cut IMAX 1080p WEB-DL-FLUX",
			[]string{"Director's Cut", "IMAX"}},
		{"already present — no diff",
			"Movie 2024 IMAX 1080p WEB-DL-FLUX",
			"Movie 2024 IMAX 1080p WEB-DL-FLUX",
			nil},
		{"empty grab → no diff",
			"Movie 2024 1080p-FLUX", "", nil},
		{"hybrid token",
			"Movie 2024 1080p-FLUX",
			"Movie 2024 Hybrid 1080p-FLUX",
			[]string{"Hybrid"}},
		{"open-matte token",
			"Movie 2024 1080p-FLUX",
			"Movie 2024 Open Matte 1080p-FLUX",
			[]string{"Open Matte"}},
		{"IMAX Enhanced lost but plain IMAX kept",
			"Movie 2024 IMAX 1080p-FLUX",
			"Movie 2024 IMAX Enhanced 1080p-FLUX",
			[]string{"IMAX Enhanced"}},
		{"4K Remaster distinct from generic Remaster",
			"Movie 2024 Remaster 1080p-FLUX",
			"Movie 2024 4K Remaster 1080p-FLUX",
			[]string{"4K Remaster"}},
		{"special edition token",
			"Movie 2024 1080p-FLUX",
			"Movie 2024 Special Edition 1080p-FLUX",
			[]string{"Special Edition"}},
		{"uncensored token",
			"Movie 2024 1080p-FLUX",
			"Movie 2024 Uncensored 1080p-FLUX",
			[]string{"Uncensored"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := DiffMissingMovieVersions(c.current, c.grab)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("DiffMissingMovieVersions(%q, %q) = %v, want %v",
					c.current, c.grab, got, c.want)
			}
		})
	}
}

func TestDiffMissingMovieVersions_NonImaxExclusion(t *testing.T) {
	// Regression: bare \bimax\b would false-match NON-IMAX titles.
	// TRaSH NON-IMAX CF intentionally flags releases as not IMAX;
	// firing the IMAX trigger on those would rename grabs that
	// explicitly identify themselves as the NON-IMAX cut.
	//
	// Go RE2 has no lookbehind, so the Exclude pattern catches
	// "NON-IMAX" / "NON IMAX" / "NON.IMAX" / "NON_IMAX" forms and
	// drops the match. Edge case where a single title contains both
	// "NON-IMAX" AND a separate plain "IMAX" returns no match — rare
	// enough to accept the false-negative.
	cases := []struct {
		name    string
		current string
		grab    string
		want    []string
	}{
		{"non-imax in grab does not trigger imax",
			"Movie 2024 1080p-FLUX",
			"Movie 2024 NON-IMAX 1080p-FLUX",
			nil},
		{"non.imax with dot separator does not trigger",
			"Movie 2024 1080p-FLUX",
			"Movie.2024.NON.IMAX.1080p-FLUX",
			nil},
		{"non_imax with underscore does not trigger",
			"Movie 2024 1080p-FLUX",
			"Movie.2024.NON_IMAX.WEB-DL-FLUX",
			nil},
		{"non imax with space does not trigger",
			"Movie 2024 1080p-FLUX",
			"Movie 2024 NON IMAX 1080p-FLUX",
			nil},
		{"plain imax still triggers",
			"Movie 2024 1080p-FLUX",
			"Movie 2024 IMAX 1080p-FLUX",
			[]string{"IMAX"}},
		{"asymmetric upgrade — current is NON-IMAX, grab is plain IMAX → MUST diff",
			// User has the NON-IMAX cut, grab is the IMAX cut: rename should
			// fire so the qBit name reflects the upgraded version.
			"Movie 2024 NON-IMAX 1080p-FLUX",
			"Movie 2024 IMAX 1080p-FLUX",
			[]string{"IMAX"}},
		{"non-imax in current AND grab → no diff",
			"Movie 2024 NON-IMAX 1080p-FLUX",
			"Movie 2024 NON-IMAX 1080p-FLUX",
			nil},
		{"NONIMAX without separator → boundary safety, no false-match",
			// \bimax\b doesn't match inside NONIMAX (no word boundary
			// between N and I), so the substring "IMAX" is invisible to
			// the matcher. Test pins the behaviour so a future regex
			// tweak (e.g. dropping word-boundaries) doesn't silently
			// flag this case.
			"Movie 2024 1080p-FLUX",
			"Movie.2024.NONIMAX.1080p-FLUX",
			nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := DiffMissingMovieVersions(c.current, c.grab)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("DiffMissingMovieVersions(%q, %q) = %v, want %v",
					c.current, c.grab, got, c.want)
			}
		})
	}
}

func TestDiffMissingSources(t *testing.T) {
	cases := []struct {
		name    string
		current string
		grab    string
		want    []string
	}{
		{"AMZN missing",
			"Movie 2024 1080p WEB-DL-FLUX",
			"Movie 2024 1080p AMZN WEB-DL-FLUX",
			[]string{"AMZN"}},
		{"MA WEB-DL missing",
			"Movie 2024 1080p WEB-DL-FLUX",
			"Movie 2024 1080p MA WEB-DL-FLUX",
			[]string{"MA WEB-DL"}},
		{"already present",
			"Movie 2024 NF 1080p WEB-DL-FLUX",
			"Movie 2024 NF 1080p WEB-DL-FLUX",
			nil},
		{"iT missing from torrent (real grab name, space form)",
			"Movie 2025 2160p WEB-DL DDP5.1 Atmos DV HDR H.265-BYNDR",
			"Movie 2025 2160p iT WEB-DL DDP5.1 Atmos DV HDR H.265-BYNDR",
			[]string{"iT"}},
		{"iT missing from torrent (dot form)",
			"Movie.2011.2160p.WEB-DL.DTS-HD.MA.5.1.DV.HDR.H.265-FLUX",
			"Movie.2011.2160p.iT.WEB-DL.DTS-HD.MA.5.1.DV.HDR.H.265-FLUX",
			[]string{"iT"}},
		{"iT in grab vs iTunes in torrent (same source, no diff)",
			"[x].Movie.1969.1080p.iTunes.WEB-DL.H264.DD5.1-UBWEB",
			"Movie 1969 1080p iT WEB-DL DD 5.1 H.264-UBWEB",
			nil},
		{"title word It is not the iTunes source",
			"When It Rains 2017 1080p WEBRip-RG",
			"When It Rains 2017 1080p WEB-DL-RG",
			[]string{"WEB-DL"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := DiffMissingSources(c.current, c.grab)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("DiffMissingSources(%q, %q) = %v, want %v",
					c.current, c.grab, got, c.want)
			}
		})
	}
}

func TestDiffMissingAudio(t *testing.T) {
	cases := []struct {
		name    string
		current string
		grab    string
		want    []string
	}{
		{"TrueHD Atmos missing",
			"Movie 2024 1080p WEB-DL-FLUX",
			"Movie 2024 1080p WEB-DL TrueHD Atmos 7.1-FLUX",
			// TrueHD Atmos pattern catches the combined; Atmos+TrueHD as
			// individual labels also match. Order from set definition.
			[]string{"TrueHD Atmos", "Atmos", "TrueHD"}},
		{"DTS-X missing",
			"Movie 2024 1080p WEB-DL-FLUX",
			"Movie 2024 1080p WEB-DL DTS-X-FLUX",
			[]string{"DTS-X"}},
		{"DTS-HD MA missing",
			"Movie 2024 1080p WEB-DL-FLUX",
			"Movie 2024 1080p WEB-DL DTS-HD MA 5.1-FLUX",
			[]string{"DTS-HD MA"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := DiffMissingAudio(c.current, c.grab)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("DiffMissingAudio(%q, %q) = %v, want %v",
					c.current, c.grab, got, c.want)
			}
		})
	}
}

func TestDiffMissingHdr(t *testing.T) {
	cases := []struct {
		name    string
		current string
		grab    string
		want    []string
	}{
		{"HDR10+ lost to HDR",
			"Movie 2024 2160p HDR x265-FLUX",
			"Movie 2024 2160p HDR10+ x265-FLUX",
			[]string{"HDR10+"}},
		{"HDR10Plus lost to HDR10",
			"Movie 2024 2160p HDR10 x265-FLUX",
			"Movie 2024 2160p HDR10Plus x265-FLUX",
			[]string{"HDR10+"}},
		{"Dolby Vision missing",
			"Movie 2024 2160p HDR x265-FLUX",
			"Movie 2024 2160p DV HDR x265-FLUX",
			[]string{"Dolby Vision"}},
		{"HLG missing",
			"Movie 2024 1080p x264-FLUX",
			"Movie 2024 1080p HLG x264-FLUX",
			[]string{"HLG"}},
		{"already present — no diff",
			"Movie 2024 2160p HDR10+ x265-FLUX",
			"Movie 2024 2160p HDR10+ x265-FLUX",
			nil},
		{"generic HDR in both — no diff (only granular tokens count)",
			"Movie 2024 2160p HDR x265-FLUX",
			"Movie 2024 2160p HDR x265-FLUX",
			nil},
		{"DVDRip must not false-match Dolby Vision",
			"Movie 2024 DVDRip x264-FLUX",
			"Movie 2024 DVDRip DV x264-FLUX",
			[]string{"Dolby Vision"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := DiffMissingHdr(c.current, c.grab)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("DiffMissingHdr(%q, %q) = %v, want %v",
					c.current, c.grab, got, c.want)
			}
		})
	}
}

func TestIsKnownSceneGroup(t *testing.T) {
	cases := map[string]bool{
		"":               false,
		"CAKES":          true,
		"cakes":          true, // case-insensitive
		"GLHF":           true,
		"FLUX":           false, // P2P group
		"NTb":            false,
		"  GGEZ  ":       true,  // trim whitespace
		"SumVision":      false, // would-be-renamed group
		"SuccessfulCrab": true,
	}
	for in, want := range cases {
		if got := IsKnownSceneGroup(in); got != want {
			t.Errorf("IsKnownSceneGroup(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestIsSceneNamingPattern(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{"WEB without DL — scene-stripped",
			"Movie 2024 1080p WEB-FLUX", true},
		{"WEB.DL — real WEB-DL release",
			"Movie 2024 1080p WEB-DL-FLUX", false},
		{"WEB-DL — real WEB-DL release",
			"Movie 2024 1080p WEB-DL-FLUX", false},
		{"Bluray — not a WEB release at all",
			"Movie 2024 1080p Bluray-FLUX", false},
		{"720p with WEB",
			"Movie 2024 720p WEB-FLUX", true},
		{"resolution missing",
			"Movie 2024 WEB-FLUX", false},
		{"empty input",
			"", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := IsSceneNamingPattern(c.input)
			if got != c.want {
				t.Errorf("IsSceneNamingPattern(%q) = %v, want %v",
					c.input, got, c.want)
			}
		})
	}
}

func TestMatchCustomTokens(t *testing.T) {
	tokens := []CompiledCustomToken{
		{Label: "NORDIC", Pattern: regexp.MustCompile(`(?i)\bnordic\b`)},
		{Label: "MULTi", Pattern: regexp.MustCompile(`(?i)\bmulti\b`)},
	}
	cases := []struct {
		name    string
		current string
		grab    string
		want    []string
	}{
		{"NORDIC missing",
			"Movie 2024 1080p WEB-DL-FLUX",
			"Movie 2024 1080p NORDIC WEB-DL-FLUX",
			[]string{"NORDIC"}},
		{"both missing",
			"Movie 2024 1080p WEB-DL-FLUX",
			"Movie 2024 1080p MULTi NORDIC WEB-DL-FLUX",
			[]string{"NORDIC", "MULTi"}},
		{"already present",
			"Movie 2024 NORDIC 1080p WEB-DL-FLUX",
			"Movie 2024 NORDIC 1080p WEB-DL-FLUX",
			nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := MatchCustomTokens(c.current, c.grab, tokens)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("MatchCustomTokens(%q, %q) = %v, want %v",
					c.current, c.grab, got, c.want)
			}
		})
	}
}

func TestVocabularyExports(t *testing.T) {
	// Smoke test that the exported label-list helpers stay populated
	// (regression guard if someone refactors the slice and forgets
	// to update the export wrappers).
	if got := MovieVersionTokens(); len(got) < 10 {
		t.Errorf("MovieVersionTokens len = %d, want ≥10 (12 in TRaSH list)", len(got))
	}
	if got := SourceTokens(); len(got) < 8 {
		t.Errorf("SourceTokens len = %d, want ≥8", len(got))
	}
	if got := AudioTokens(); len(got) < 4 {
		t.Errorf("AudioTokens len = %d, want ≥4", len(got))
	}
}

func TestHasLeadingForeignBracket(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"chinese bracket prefix", "[测试名].Movie.1969.1080p.iTunes.WEB-DL.H264.DD5.1-UBWEB", true},
		{"chinese bracket fires regardless of grab (clean-in-place)", "[测试名].Movie.2020-RG", true},
		{"ascii bracket prefix left alone", "[RlsGrp].Movie.2020.1080p.WEB-DL-RG", false},
		{"no leading bracket", "Movie.1969.1080p.WEB-DL-UBWEB", false},
		{"empty bracket", "[].Movie.2020-RG", false},
		{"unterminated bracket", "[测试名 Movie 2020 RG", false},
		{"cyrillic bracket prefix", "[Перевод].Movie.2021.1080p.WEB-DL-RG", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := HasLeadingForeignBracket(c.in); got != c.want {
				t.Errorf("HasLeadingForeignBracket(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestStripLeadingForeignBracket(t *testing.T) {
	cases := []struct{ in, want string }{
		{"[测试名].Movie.1969.1080p.iTunes.WEB-DL.H264.DD5.1-UBWEB", "Movie.1969.1080p.iTunes.WEB-DL.H264.DD5.1-UBWEB"},
		{"[Перевод].Movie.2021-RG", "Movie.2021-RG"},
		// Accented-Latin leading bracket is also stripped — deliberate:
		// Radarr mis-parses any non-ASCII leading bracket as the group,
		// so it's the same hazard as CJK/Cyrillic.
		{"[Amélie].Movie.2001-RG", "Movie.2001-RG"},
		{"[RlsGrp].Movie.2020-RG", "[RlsGrp].Movie.2020-RG"},
		{"Movie.2020-RG", "Movie.2020-RG"},
	}
	for _, c := range cases {
		if got := StripLeadingForeignBracket(c.in); got != c.want {
			t.Errorf("StripLeadingForeignBracket(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCleanReleaseName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"[测试名].Movie.1969.1080p.iTunes.WEB-DL.H264.DD5.1-UBWEB", "Movie.1969.1080p.iTunes.WEB-DL.H264.DD5.1-UBWEB"},
		{"Movie.The.Secret.Service.2015.2160p.MA.WEB-DL-FLUX.mkv", "Movie.The.Secret.Service.2015.2160p.MA.WEB-DL-FLUX"},
		{"Movie.2020.2020.1080p-RG", "Movie.2020.1080p-RG"},
		{"[测试名].Movie.2020.2020.1080p-RG.mkv", "Movie.2020.1080p-RG"},
		{"Movie.2024.1080p.WEB-DL-FLUX", "Movie.2024.1080p.WEB-DL-FLUX"},
		{"[SubGroup] Show 2024 1080p WEB-DL-RG.mkv", "[SubGroup] Show 2024 1080p WEB-DL-RG"},
	}
	for _, c := range cases {
		if got := CleanReleaseName(c.in); got != c.want {
			t.Errorf("CleanReleaseName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestResolveReleaseGroup(t *testing.T) {
	cases := []struct {
		name     string
		radarrRG string
		input    string
		want     string
	}{
		{"normal trust Radarr", "FLUX", "Movie.2024.1080p.WEB-DL-FLUX", "FLUX"},
		{"empty Radarr parse name", "", "Movie.2024.1080p.WEB-DL-FLUX", "FLUX"},
		{"Radarr took the bracket use name", "测试名", "[测试名].Movie.1969.1080p.WEB-DL-UBWEB", "UBWEB"},
		{"Radarr non-ASCII garbage use name", "某组", "Movie.2024.1080p.WEB-DL-UBWEB", "UBWEB"},
		{"normal ASCII differs still trust Radarr", "NTb", "Movie.2024.1080p.WEB-DL-FLUX", "NTb"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ResolveReleaseGroup(c.radarrRG, c.input); got != c.want {
				t.Errorf("ResolveReleaseGroup(%q, %q) = %q, want %q", c.radarrRG, c.input, got, c.want)
			}
		})
	}
}

func TestDuplicateYear(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantHas bool
		wantOut string
	}{
		{"same year twice dot", "Movie.2026.2026.1080p.AMZN.WEB-DL.DDP5.1.H.264-KyoGo", true, "Movie.2026.1080p.AMZN.WEB-DL.DDP5.1.H.264-KyoGo"},
		{"same year twice dot (alt)", "Film.2016.2016.2160p.ATVP.WEB-DL.DD.5.1.DV.HDR.H.265", true, "Film.2016.2160p.ATVP.WEB-DL.DD.5.1.DV.HDR.H.265"},
		{"same year twice space", "Movie 2026 2026 1080p WEB-DL-KyoGo", true, "Movie 2026 1080p WEB-DL-KyoGo"},
		{"different years left alone (title-year + release-year)", "Movie.2049.2017.2160p.WEB-DL-FLUX", false, "Movie.2049.2017.2160p.WEB-DL-FLUX"},
		{"different years title-plus-release", "Movie.2018.2019.2160p.WEB-DL-TheFarm", false, "Movie.2018.2019.2160p.WEB-DL-TheFarm"},
		{"single year no-op", "Movie.2024.1080p.WEB-DL-RG", false, "Movie.2024.1080p.WEB-DL-RG"},
		{"resolution not a year", "Movie.2024.2160p.WEB-DL-RG", false, "Movie.2024.2160p.WEB-DL-RG"},
		{"triple same year collapses fully", "Movie.2016.2016.2016.1080p-RG", true, "Movie.2016.1080p-RG"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := HasDuplicateYear(c.in); got != c.wantHas {
				t.Errorf("HasDuplicateYear(%q) = %v, want %v", c.in, got, c.wantHas)
			}
			if got := CollapseDuplicateYear(c.in); got != c.wantOut {
				t.Errorf("CollapseDuplicateYear(%q) = %q, want %q", c.in, got, c.wantOut)
			}
		})
	}
}
