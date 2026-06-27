package engine

import "testing"

func TestClassifyReleaseTypeFromTitle(t *testing.T) {
	cases := map[string]string{
		"Altered.Carbon.S01.1080p.NF.WEB-DL.DDP5.1.x264-NTb":  ReleaseTypeSeasonPack,
		"Altered.Carbon.S02.COMPLETE.1080p.NF.WEB-DL-NTG":     ReleaseTypeSeasonPack,
		"Broad City S01 1080p AMZN WEB-DL DD+ 2.0 H.264-WADU": ReleaseTypeSeasonPack,
		"Altered.Carbon.S02E08.1080p.WEB.X264-METCON":         ReleaseTypeSingleEpisode,
		"Show.S03E04E05.1080p-GRP":                            ReleaseTypeMultiEpisode,
		"Show.S03E04-E05.1080p-GRP":                           ReleaseTypeMultiEpisode,
		"Some.Movie.2019.1080p.BluRay-GRP":                    "", // no S/E marker
	}
	for in, want := range cases {
		if got := ClassifyReleaseTypeFromTitle(in); got != want {
			t.Errorf("ClassifyReleaseTypeFromTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormaliseReleaseType(t *testing.T) {
	cases := map[string]string{
		"seasonPack": ReleaseTypeSeasonPack, "SeasonPack": ReleaseTypeSeasonPack,
		"singleEpisode": ReleaseTypeSingleEpisode, "multiEpisode": ReleaseTypeMultiEpisode,
		"unknown": "", "": "", "Unknown": "",
	}
	for in, want := range cases {
		if got := NormaliseReleaseType(in); got != want {
			t.Errorf("NormaliseReleaseType(%q) = %q, want %q", in, got, want)
		}
	}
}

// The real Altered Carbon case: file group NTb, current "unknown", grabs =
// NTb season packs (empty field) + METCON singles. Group match keeps only
// the NTb packs -> Season Pack, high confidence, candidate.
func TestDecide_AlteredCarbon_PreV4_SeasonPack(t *testing.T) {
	in := ReleaseTypeRecoverInput{
		CurrentType:  "unknown",
		ReleaseGroup: "NTb",
		Grabs: []ReleaseTypeGrab{
			{SourceTitle: "Altered.Carbon.S01.1080p.NF.WEB-DL.DDP5.1.x264-NTb", ReleaseGroup: "NTb", FieldType: ""},
			{SourceTitle: "Altered.Carbon.S01E03.1080p.WEB.X264-METCON", ReleaseGroup: "METCON", FieldType: ""},
		},
	}
	v := DecideReleaseTypeRecovery(in)
	if v.RecoveredType != ReleaseTypeSeasonPack {
		t.Fatalf("RecoveredType = %q, want seasonPack", v.RecoveredType)
	}
	if !v.IsCandidate || v.Confidence != ReleaseTypeConfHigh || v.Source != "title" || !v.GroupMatched {
		t.Errorf("verdict = %+v, want candidate/high/title/groupMatched", v)
	}
}

// Broad City "lost" case: grab carries the stored field -> Tier 1, high.
func TestDecide_BroadCity_FieldTier(t *testing.T) {
	in := ReleaseTypeRecoverInput{
		CurrentType:  "singleEpisode",
		ReleaseGroup: "WADU",
		Grabs: []ReleaseTypeGrab{
			{SourceTitle: "Broad City S01 1080p AMZN WEB-DL-WADU", ReleaseGroup: "WADU", FieldType: "SeasonPack"},
		},
	}
	v := DecideReleaseTypeRecovery(in)
	if v.RecoveredType != ReleaseTypeSeasonPack || v.Source != "field" || v.Confidence != ReleaseTypeConfHigh || !v.IsCandidate {
		t.Errorf("verdict = %+v, want seasonPack/field/high/candidate", v)
	}
}

// Conflicting grabs that both match the group -> ambiguous, no candidate.
func TestDecide_Conflicting_LeftAlone(t *testing.T) {
	in := ReleaseTypeRecoverInput{
		CurrentType:  "unknown",
		ReleaseGroup: "GRP",
		Grabs: []ReleaseTypeGrab{
			{SourceTitle: "Show.S01.1080p-GRP", ReleaseGroup: "GRP"},
			{SourceTitle: "Show.S01E04.1080p-GRP", ReleaseGroup: "GRP"},
		},
	}
	v := DecideReleaseTypeRecovery(in)
	if v.IsCandidate || v.RecoveredType != "" {
		t.Errorf("verdict = %+v, want no candidate (ambiguous)", v)
	}
}

// Already correct -> determinable but NOT a candidate (no change needed).
func TestDecide_AlreadyCorrect_NotCandidate(t *testing.T) {
	in := ReleaseTypeRecoverInput{
		CurrentType:  "seasonPack",
		ReleaseGroup: "NTb",
		Grabs:        []ReleaseTypeGrab{{SourceTitle: "Show.S01.1080p-NTb", ReleaseGroup: "NTb"}},
	}
	v := DecideReleaseTypeRecovery(in)
	if v.RecoveredType != ReleaseTypeSeasonPack || v.IsCandidate {
		t.Errorf("verdict = %+v, want seasonPack but not a candidate", v)
	}
}

// Whiskey Cavalier case: file group NTb, the matching grab is an NTb SINGLE
// (sourceTitle only, no stored field). A season pack would look identical on
// disk, so this must be Unconfirmed, never High.
func TestDecide_SourceTitleSingle_Unconfirmed(t *testing.T) {
	in := ReleaseTypeRecoverInput{
		CurrentType:  "unknown",
		ReleaseGroup: "NTb",
		Grabs: []ReleaseTypeGrab{
			{SourceTitle: "Whiskey.Cavalier.S01E13.Czech.Mate.1080p.AMZN.WEB-DL-NTb", ReleaseGroup: "NTb", FieldType: ""},
		},
	}
	v := DecideReleaseTypeRecovery(in)
	if v.RecoveredType != ReleaseTypeSingleEpisode {
		t.Fatalf("RecoveredType = %q, want singleEpisode", v.RecoveredType)
	}
	if v.Confidence != ReleaseTypeConfUnconfirmed {
		t.Errorf("Confidence = %q, want unconfirmed (a season pack looks identical on disk)", v.Confidence)
	}
}

// A single episode with Sonarr's STORED field is trustworthy -> high.
func TestDecide_FieldSingle_High(t *testing.T) {
	in := ReleaseTypeRecoverInput{
		CurrentType:  "unknown",
		ReleaseGroup: "NTb",
		Grabs:        []ReleaseTypeGrab{{SourceTitle: "Show.S01E05.1080p-NTb", ReleaseGroup: "NTb", FieldType: "singleEpisode"}},
	}
	v := DecideReleaseTypeRecovery(in)
	if v.RecoveredType != ReleaseTypeSingleEpisode || v.Confidence != ReleaseTypeConfHigh {
		t.Errorf("verdict = %+v, want singleEpisode/high (stored field)", v)
	}
}

// No group on the file -> falls back to all grabs, capped at medium.
func TestDecide_NoGroup_Medium(t *testing.T) {
	in := ReleaseTypeRecoverInput{
		CurrentType:  "unknown",
		ReleaseGroup: "",
		Grabs:        []ReleaseTypeGrab{{SourceTitle: "Show.S02.1080p-GRP", ReleaseGroup: "GRP"}},
	}
	v := DecideReleaseTypeRecovery(in)
	if v.RecoveredType != ReleaseTypeSeasonPack || v.Confidence != ReleaseTypeConfMedium || v.GroupMatched {
		t.Errorf("verdict = %+v, want seasonPack/medium/!groupMatched", v)
	}
}
