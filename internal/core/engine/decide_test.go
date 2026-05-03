package engine

import (
	"regexp"
	"testing"
)

// allFiltersOn is the config shipped in tagarr.conf.sample — every
// toggle on. All TAG-TEST-LIST cases assume this config; divergence
// would change expected outcomes.
var allFiltersOn = FilterConfig{
	Quality:     true,
	MAWebDL:     true,
	PlayWebDL:   true,
	Audio:       true,
	TrueHD:      true,
	TrueHDAtmos: true,
	DTSX:        true,
	DTSHDMA:     true,
}

// TestMatchReleaseGroup_priority verifies the three-field priority
// order (releaseGroup → sceneName → relativePath). Each case provides
// values that WOULD match in more than one field, and the test
// asserts the winner is the highest-priority field. Also covers
// miss-only cases and the word-boundary guard.
func TestMatchReleaseGroup_priority(t *testing.T) {
	cases := []struct {
		name                                    string
		releaseGroup, sceneName, relativePath   string
		search                                  string
		wantMatched                             bool
		wantLocation                            string
	}{
		{"rg_wins_over_others", "flux", "flux.release", "movie.flux.mkv", "flux", true, MatchLocationReleaseGroup},
		{"scene_when_rg_missing", "", "release.thefarm.scene", "movie.thefarm.mkv", "thefarm", true, MatchLocationSceneName},
		{"path_when_others_missing", "", "", "some.movie.flux.mkv", "flux", true, MatchLocationRelativePath},
		{"scene_wins_when_rg_empty_and_path_also_matches", "", "movie.flux.1080p", "movie.flux.1080p.mkv", "flux", true, MatchLocationSceneName},
		{"nothing_matches", "amzn", "some.scene.amzn", "movie.amzn.mkv", "flux", false, ""},
		{"word_boundary_blocks_substring", "influx", "", "", "flux", false, ""},
		{"word_boundary_blocks_jurassic_trick", "", "", "jurassic.park.movie.sic.something.mkv", "sic", true, MatchLocationRelativePath}, // real "sic" as its own token
		{"empty_search_never_matches", "flux", "", "", "", false, ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			matched, loc := MatchReleaseGroup(tc.releaseGroup, tc.sceneName, tc.relativePath, tc.search)
			if matched != tc.wantMatched || loc != tc.wantLocation {
				t.Errorf("got (%v, %q), want (%v, %q)", matched, loc, tc.wantMatched, tc.wantLocation)
			}
		})
	}
}

// TestMatchReleaseGroup_wordBoundaryHardCases verifies the \b guard
// against known false-positive patterns. Each of these previously
// bit tagarr users on real libraries; they are the reason the bash
// script switched from `grep -i "$search"` to `grep -Ei "\b$search\b"`.
func TestMatchReleaseGroup_wordBoundaryHardCases(t *testing.T) {
	// search=MA must NOT match AMZN, IMAX, MAGIC, anagram-like words
	for _, field := range []string{"movie.amzn.webdl.mkv", "movie.imax.h265.mkv", "movie.magical.release.mkv"} {
		matched, _ := MatchReleaseGroup("", "", field, "ma")
		if matched {
			t.Errorf("search=ma matched in %q — word boundary leak", field)
		}
	}
	// search=sic must NOT match inside jurassic
	matched, _ := MatchReleaseGroup("", "", "jurassic.park.mkv", "sic")
	if matched {
		t.Error("search=sic matched inside jurassic — word boundary leak")
	}
	// search=play must NOT match inside gameplay
	matched, _ = MatchReleaseGroup("", "", "gameplay.movie.mkv", "play")
	if matched {
		t.Error("search=play matched inside gameplay — word boundary leak")
	}
}

// TestDecideTag_noMatch: a movie whose releaseGroup does NOT match the
// configured search string always fails, regardless of filter state.
// Reason must be "Wrong release group" so removal notifications show
// the right explanation.
func TestDecideTag_noMatch(t *testing.T) {
	mf := MovieFile{RelativePath: "some.movie.amzn.webdl.h265-otherguy.mkv"}
	group := GroupConfig{Search: "flux", Mode: "filtered"}
	d := DecideTag(mf, group, allFiltersOn)
	if d.ShouldHave {
		t.Fatal("ShouldHave=true on no-match — fundamental regression")
	}
	if d.Matched {
		t.Error("Matched=true on no-match")
	}
	if d.Reason != "Wrong release group" {
		t.Errorf("Reason=%q, want Wrong release group", d.Reason)
	}
	if d.QualityResult != ResultNA || d.AudioResult != ResultNA {
		t.Errorf("on no-match expected both results N/A, got quality=%q audio=%q", d.QualityResult, d.AudioResult)
	}
}

// TestDecideTag_simpleMode: mode=simple skips the filter entirely.
// A matched release gets the tag regardless of EAC3/AMZN/etc.
func TestDecideTag_simpleMode(t *testing.T) {
	// The release's codec and source are both "bad" by the filters'
	// standards, but mode=simple means we tag anyway.
	mf := MovieFile{RelativePath: "movie.amzn.webdl.eac3.atmos-flux.mkv"}
	group := GroupConfig{Search: "flux", Mode: "simple"}
	d := DecideTag(mf, group, allFiltersOn)
	if !d.ShouldHave {
		t.Fatal("ShouldHave=false in simple mode on a matched release")
	}
	if d.QualityResult != ResultNASimpleMode || d.AudioResult != ResultNASimpleMode {
		t.Errorf("simple mode must skip filters; got quality=%q audio=%q", d.QualityResult, d.AudioResult)
	}
}

// TestDecideTag_filteredModePassAndFailReasons: comprehensive coverage
// of the four filter outcomes and their reason strings.
func TestDecideTag_filteredModePassAndFailReasons(t *testing.T) {
	cases := []struct {
		name              string
		filename          string
		wantShould        bool
		wantReason        string
		wantQResult       string
		wantAResult       string
	}{
		{
			"both_pass_tags",
			"Sample.B.1995.MA.WEBDL-2160p.TrueHD.Atmos.7.1.DV.HDR10.h265-FLUX.mkv",
			true, "", ResultPass, ResultPass,
		},
		{
			"quality_fail_amzn",
			"Sample.G.2024.AMZN.WEBDL-2160p.DTS-HD.MA.5.1.h265-FLUX.mkv",
			false, "Failed quality", ResultFail, ResultPass,
		},
		{
			"audio_fail_eac3",
			"Sample.A.2023.MA.WEBDL-2160p.EAC3.Atmos.5.1.DV.HDR10.h265-FLUX.mkv",
			false, "Failed audio", ResultPass, ResultFail,
		},
		{
			"both_fail_amzn_eac3",
			"Sample.X.2024.AMZN.WEBDL-2160p.EAC3.5.1-FLUX.mkv",
			false, "Failed quality & audio", ResultFail, ResultFail,
		},
	}
	group := GroupConfig{Search: "flux", Mode: "filtered"}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			d := DecideTag(MovieFile{RelativePath: tc.filename}, group, allFiltersOn)
			if d.ShouldHave != tc.wantShould {
				t.Errorf("ShouldHave=%v, want %v (file=%q)", d.ShouldHave, tc.wantShould, tc.filename)
			}
			if d.Reason != tc.wantReason {
				t.Errorf("Reason=%q, want %q", d.Reason, tc.wantReason)
			}
			if d.QualityResult != tc.wantQResult {
				t.Errorf("QualityResult=%q, want %q", d.QualityResult, tc.wantQResult)
			}
			if d.AudioResult != tc.wantAResult {
				t.Errorf("AudioResult=%q, want %q", d.AudioResult, tc.wantAResult)
			}
		})
	}
}

// TestDecideTag_combinedFieldsForFilter: the filter runs against
// relativePath + sceneName + releaseGroup combined. A token present in
// sceneName but absent from relativePath must still count. Matches
// tagarr.sh:816.
func TestDecideTag_combinedFieldsForFilter(t *testing.T) {
	// relativePath has no quality or audio token, but sceneName does.
	// Without the combined-field logic, quality+audio would both fail.
	mf := MovieFile{
		RelativePath: "movie.2024.1080p.h265-flux.mkv",
		SceneName:    "Movie.2024.MA.WEBDL-1080p.TrueHD.Atmos-FLUX",
		ReleaseGroup: "FLUX",
	}
	group := GroupConfig{Search: "flux", Mode: "filtered"}
	d := DecideTag(mf, group, allFiltersOn)
	if !d.ShouldHave {
		t.Fatalf("combined-fields filter didn't kick in — ShouldHave=false; decision=%+v", d)
	}
}

// TestDecideTag_detailStrings: the human-readable detail strings for
// each pass/fail path. Regression guard on tagarr.sh:876-923 port.
func TestDecideTag_detailStrings(t *testing.T) {
	cases := []struct {
		name     string
		filename string
		wantQ    string
		wantA    string
	}{
		{"ma_webdl_truehd_atmos", "x.MA.WEBDL-1080p.TrueHD.Atmos-FLUX.mkv", "MA WEB-DL", "TrueHD Atmos"},
		{"ma_webdl_dtsx",         "x.MA.WEBDL-1080p.DTS-X-FLUX.mkv",         "MA WEB-DL", "DTS-X"},
		{"ma_webdl_truehd",       "x.MA.WEBDL-1080p.TrueHD-FLUX.mkv",        "MA WEB-DL", "TrueHD"},
		{"ma_webdl_dtshdma",      "x.MA.WEBDL-1080p.DTS-HD.MA-FLUX.mkv",     "MA WEB-DL", "DTS-HD.MA"},
		{"play_webdl_truehd",     "x.Play.WEBDL-1080p.TrueHD-FLUX.mkv",      "Play WEB-DL", "TrueHD"},
		{"amzn_dtshdma",          "x.AMZN.WEBDL-1080p.DTS-HD.MA-FLUX.mkv",   "AMZN (not MA/Play)", "DTS-HD.MA"},
		{"nf_aac",                "x.NF.WEBDL-1080p.AAC.5.1-FLUX.mkv",       "Netflix (not MA/Play)", "AAC (lossy)"},
		{"plain_web_eac3",        "x.WEB-1080p.EAC3-FLUX.mkv",               "Plain WEB-DL (no MA/Play prefix)", "EAC3/DD+ (lossy)"},
		{"bluray_no_webdl_ac3",   "x.BluRay-1080p.AC3.5.1-FLUX.mkv",         "No WEB-DL source", "AC3 (lossy)"},
	}
	group := GroupConfig{Search: "flux", Mode: "filtered"}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			d := DecideTag(MovieFile{RelativePath: tc.filename}, group, allFiltersOn)
			if d.QualityDetail != tc.wantQ {
				t.Errorf("QualityDetail=%q, want %q (file=%q)", d.QualityDetail, tc.wantQ, tc.filename)
			}
			if d.AudioDetail != tc.wantA {
				t.Errorf("AudioDetail=%q, want %q (file=%q)", d.AudioDetail, tc.wantA, tc.filename)
			}
		})
	}
}

// tagTestCase is one TAG-TEST-LIST row ported from DESIGN_NOTES.md.
// Every case assumes all filters are on and the group is in
// "filtered" mode. wantTag==true means the filename should PASS the
// full pipeline (match + quality + audio); false means one of the
// three stages rejected it.
type tagTestCase struct {
	group      string // which configured group this targets
	filename   string
	wantTag    bool
}

// TestDecideTag_goldStandard runs the DESIGN_NOTES.md:331-382
// TAG-TEST-LIST byte-for-byte against the Go engine. If the Go port
// ever produces a different decision from the TAG-TEST-LIST's + / -
// expectation, this test fails and the offending case prints both
// the filename and the decision breakdown so the drift is obvious.
//
// The list asserts end-to-end pipeline behaviour (match + filter +
// result). It's intentionally broader than the unit tests in
// filter_test.go — those cover CheckQuality / CheckAudio in
// isolation; this covers DecideTag as a whole.
func TestDecideTag_goldStandard(t *testing.T) {
	cases := []tagTestCase{
		// TheFarm — MA
		{"thefarm", "MOVIE.2023.MA.WEBDL-2160p.TrueHD.Atmos.7.1.DV.HDR10.HEVC-TheFarm.mkv", true},
		{"thefarm", "MOVIE.2023.MA.WEBDL-2160p.DTS-X.7.1.DV.HDR10.HEVC-TheFarm.mkv", true},
		{"thefarm", "MOVIE.2023.MA.WEBDL-2160p.TrueHD.7.1.DV.HDR10.HEVC-TheFarm.mkv", true},
		{"thefarm", "MOVIE.2023.MA.WEBDL-2160p.DTS-HD.MA.5.1.DV.HDR10.HEVC-TheFarm.mkv", true},
		{"thefarm", "MOVIE.2023.MA.WEBDL-2160p.EAC3.Atmos.5.1.DV.HDR10.HEVC-TheFarm.mkv", false},
		// TheFarm — Play
		{"thefarm", "MOVIE.2023.Play.WEBDL-2160p.TrueHD.Atmos.7.1.DV.HDR10.HEVC-TheFarm.mkv", true},
		{"thefarm", "MOVIE.2023.Play.WEBDL-2160p.DTS-X.7.1.DV.HDR10.HEVC-TheFarm.mkv", true},
		{"thefarm", "MOVIE.2023.Play.WEBDL-2160p.TrueHD.7.1.DV.HDR10.HEVC-TheFarm.mkv", true},
		{"thefarm", "MOVIE.2023.Play.WEBDL-2160p.DTS-HD.MA.5.1.DV.HDR10.HEVC-TheFarm.mkv", true},
		{"thefarm", "MOVIE.2023.Play.WEBDL-2160p.EAC3.Atmos.5.1.DV.HDR10.HEVC-TheFarm.mkv", false},
		// TheFarm — plain WEBDL (no MA/Play prefix)
		{"thefarm", "MOVIE.2023.WEBDL-2160p.TrueHD.Atmos.7.1.DV.HDR10.HEVC-TheFarm.mkv", false},
		{"thefarm", "MOVIE.2023.WEBDL-2160p.DTS-X.7.1.DV.HDR10.HEVC-TheFarm.mkv", false},
		{"thefarm", "MOVIE.2023.WEBDL-2160p.TrueHD.7.1.DV.HDR10.HEVC-TheFarm.mkv", false},
		{"thefarm", "MOVIE.2023.WEBDL-2160p.DTS-HD.MA.5.1.DV.HDR10.HEVC-TheFarm.mkv", false},
		{"thefarm", "MOVIE.2023.WEBDL-2160p.EAC3.Atmos.5.1.DV.HDR10.HEVC-TheFarm.mkv", false},

		// FLUX — MA
		{"flux", "MOVIE.2023.MA.WEBDL-2160p.TrueHD.Atmos.7.1.DV.HDR10.HEVC-FLUX.mkv", true},
		{"flux", "MOVIE.2023.MA.WEBDL-2160p.DTS-X.7.1.DV.HDR10.HEVC-FLUX.mkv", true},
		{"flux", "MOVIE.2023.MA.WEBDL-2160p.TrueHD.7.1.DV.HDR10.HEVC-FLUX.mkv", true},
		{"flux", "MOVIE.2023.MA.WEBDL-2160p.DTS-HD.MA.5.1.DV.HDR10.HEVC-FLUX.mkv", true},
		{"flux", "MOVIE.2023.MA.WEBDL-2160p.EAC3.Atmos.5.1.DV.HDR10.HEVC-FLUX.mkv", false},
		// FLUX — Play
		{"flux", "MOVIE.2023.Play.WEBDL-2160p.TrueHD.Atmos.7.1.DV.HDR10.HEVC-FLUX.mkv", true},
		{"flux", "MOVIE.2023.Play.WEBDL-2160p.DTS-X.7.1.DV.HDR10.HEVC-FLUX.mkv", true},
		{"flux", "MOVIE.2023.Play.WEBDL-2160p.TrueHD.7.1.DV.HDR10.HEVC-FLUX.mkv", true},
		{"flux", "MOVIE.2023.Play.WEBDL-2160p.DTS-HD.MA.5.1.DV.HDR10.HEVC-FLUX.mkv", true},
		{"flux", "MOVIE.2023.Play.WEBDL-2160p.EAC3.Atmos.5.1.DV.HDR10.HEVC-FLUX.mkv", false},
		// FLUX — plain WEBDL
		{"flux", "MOVIE.2023.WEBDL-2160p.TrueHD.Atmos.7.1.DV.HDR10.HEVC-FLUX.mkv", false},
		{"flux", "MOVIE.2023.WEBDL-2160p.DTS-X.7.1.DV.HDR10.HEVC-FLUX.mkv", false},
		{"flux", "MOVIE.2023.WEBDL-2160p.TrueHD.7.1.DV.HDR10.HEVC-FLUX.mkv", false},
		{"flux", "MOVIE.2023.WEBDL-2160p.DTS-HD.MA.5.1.DV.HDR10.HEVC-FLUX.mkv", false},
		{"flux", "MOVIE.2023.WEBDL-2160p.EAC3.Atmos.5.1.DV.HDR10.HEVC-FLUX.mkv", false},

		// 126811 — numeric groups (tests \b with digits)
		{"126811", "MOVIE.2023.MA.WEBDL-2160p.TrueHD.Atmos.7.1.DV.HDR10.HEVC-126811.mkv", true},
		{"126811", "MOVIE.2023.MA.WEBDL-2160p.DTS-X.7.1.DV.HDR10.HEVC-126811.mkv", true},
		{"126811", "MOVIE.2023.MA.WEBDL-2160p.EAC3.Atmos.5.1.DV.HDR10.HEVC-126811.mkv", false},
		{"126811", "MOVIE.2023.Play.WEBDL-2160p.TrueHD.Atmos.7.1.DV.HDR10.HEVC-126811.mkv", true},
		{"126811", "MOVIE.2023.Play.WEBDL-2160p.DTS-X.7.1.DV.HDR10.HEVC-126811.mkv", true},
		{"126811", "MOVIE.2023.Play.WEBDL-2160p.TrueHD.7.1.DV.HDR10.HEVC-126811.mkv", true},
		{"126811", "MOVIE.2023.Play.WEBDL-2160p.DTS-HD.MA.5.1.DV.HDR10.HEVC-126811.mkv", true},
		{"126811", "MOVIE.2023.Play.WEBDL-2160p.EAC3.Atmos.5.1.DV.HDR10.HEVC-126811.mkv", false},
		{"126811", "MOVIE.2023.WEBDL-2160p.TrueHD.Atmos.7.1.DV.HDR10.HEVC-126811.mkv", false},
		{"126811", "MOVIE.2023.WEBDL-2160p.DTS-X.7.1.DV.HDR10.HEVC-126811.mkv", false},
		{"126811", "MOVIE.2023.WEBDL-2160p.TrueHD.7.1.DV.HDR10.HEVC-126811.mkv", false},
		{"126811", "MOVIE.2023.WEBDL-2160p.DTS-HD.MA.5.1.DV.HDR10.HEVC-126811.mkv", false},
		{"126811", "MOVIE.2023.WEBDL-2160p.EAC3.Atmos.5.1.DV.HDR10.HEVC-126811.mkv", false},

		// Real-file samples from tagarr test bench (lines 331-337)
		{"flux", "Sample.A.2023.MA.WEBDL-2160p.EAC3.Atmos.5.1.DV.HDR10.h265-FLUX.mkv", false},
		{"flux", "Sample.B.1995.MA.WEBDL-2160p.TrueHD.Atmos.7.1.DV.HDR10.h265-FLUX.mkv", true},
		{"flux", "Sample.C.1997.MA.WEBDL-2160p.TrueHD.Atmos.7.1.DV.HDR10.h265-FLUX.mkv", true},
		{"flux", "Sample.D.1989.MA.WEBDL-2160p.TrueHD.Atmos.7.1.DV.HDR10.h265-FLUX.mkv", true},
		{"flux", "Sample.E.1999.MA.WEBDL-2160p.DTS-X.7.1.HDR10.h265-FLUX.mkv", true},
		{"flux", "Sample.F.2019.MA.WEBDL-2160p.DTS-HD.MA.7.1.HDR10.h265-FLUX.mkv", true},
		{"126811", "Sample.G.2024.AMZN.WEBDL-2160p.DTS-HD.MA.5.1.h265-126811.mkv", false},

		// rlsgrp_7 / rlsgrp_1 (DESIGN_NOTES.md:370-371). In TAG-TEST-LIST
		// these appear as "+" because the test assumes the group exists
		// in RELEASE_GROUPS. Here we do the same — configure the search
		// as the literal group name; filter pass/fail then decides.
		{"rlsgrp_7", "MOVIE.2023.MA.WEBDL-2160p.TrueHD.7.1.DV.HDR10.HEVC-rlsgrp_7.mkv", true},
		{"rlsgrp_1", "MOVIE.2023.MA.WEBDL-2160p.DTS-HD.MA.5.1.DV.HDR10.HEVC-rlsgrp_1.mkv", true},
	}

	group := func(name string) GroupConfig {
		return GroupConfig{Search: name, Tag: name, Display: name, Mode: "filtered"}
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.group+"/"+tc.filename, func(t *testing.T) {
			d := DecideTag(MovieFile{RelativePath: tc.filename}, group(tc.group), allFiltersOn)
			if d.ShouldHave != tc.wantTag {
				t.Errorf("ShouldHave=%v, want %v\n  filename = %s\n  decision = %+v",
					d.ShouldHave, tc.wantTag, tc.filename, d)
			}
		})
	}
}

// reGroupFromFilename captures the trailing -GROUP.mkv release-group
// suffix. Used to derive per-case Search strings for the end-to-end
// gold-standard test below — that lets us reuse the exact testCase
// slices from filter_test.go / filter_production_test.go without
// adding a manual group column to every row.
var reGroupFromFilename = regexp.MustCompile(`-([A-Za-z0-9_]+)\.mkv$`)

// TestDecideTag_filterSuitesEndToEnd runs every standardTests /
// bracketTests / falsePosTests / productionTests case (from
// filter_test.go + filter_production_test.go, last refreshed
// 2026-04-19 in commit 887df49) through the full DecideTag pipeline.
// The filter-level tests in those files verify CheckQuality /
// CheckAudio in isolation; this test verifies that the same decisions
// propagate through DecideTag's match + filter + decision wrapper.
//
// Expected "+" means DecideTag should yield ShouldHave=true; "-"
// means ShouldHave=false. All cases use the filename's trailing
// release group as the Search, filtered mode, all filters on.
func TestDecideTag_filterSuitesEndToEnd(t *testing.T) {
	suites := []struct {
		name  string
		cases []testCase
	}{
		{"standard", standardTests},
		{"bracket", bracketTests},
		{"falsePos", falsePosTests},
		{"production", productionTests},
	}
	for _, suite := range suites {
		suite := suite
		t.Run(suite.name, func(t *testing.T) {
			for _, tc := range suite.cases {
				tc := tc
				m := reGroupFromFilename.FindStringSubmatch(tc.filename)
				if m == nil {
					// Fatal, not continue — a silent skip would shrink
					// coverage on any future fixture that doesn't end
					// in -GROUP.mkv. If a filename without the suffix
					// belongs in the fixture, the test needs updating,
					// not silent tolerance.
					t.Fatalf("can't derive release-group from %q — filename must end in -GROUP.mkv for this test to exercise a real match", tc.filename)
				}
				groupName := m[1]
				cfg := GroupConfig{Search: groupName, Tag: groupName, Display: groupName, Mode: "filtered"}
				d := DecideTag(MovieFile{RelativePath: tc.filename}, cfg, allFiltersOn)
				want := tc.expected == "+"
				if d.ShouldHave != want {
					t.Errorf("ShouldHave=%v, want %v\n  filename = %s\n  group    = %s\n  decision = %+v",
						d.ShouldHave, want, tc.filename, groupName, d)
				}
			}
		})
	}
}
