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

func TestIsKnownSceneGroup(t *testing.T) {
	cases := map[string]bool{
		"":               false,
		"CAKES":          true,
		"cakes":          true, // case-insensitive
		"GLHF":           true,
		"FLUX":           false, // P2P group
		"NTb":            false,
		"  GGEZ  ":       true, // trim whitespace
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
