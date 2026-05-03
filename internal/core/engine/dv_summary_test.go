package engine

import "testing"

func TestParseDvSummary_Profile7FEL(t *testing.T) {
	// Real-world dovi_tool 2.1.2 output for a Profile 7 FEL encode.
	in := `Summary:
Profile: 7.6
FEL (Enhancement Layer)
DM version: 1
RPU mastering display: P3, D65 (1000 nit)
CM v2.9
`
	got := ParseDvSummary(in)
	if got.Profile != 7 {
		t.Errorf("Profile = %d, want 7", got.Profile)
	}
	if got.Layer != "fel" {
		t.Errorf("Layer = %q, want fel", got.Layer)
	}
	if got.CMVersion != 2 {
		t.Errorf("CMVersion = %d, want 2", got.CMVersion)
	}
	tags := got.Tags()
	if len(tags) != 2 || tags[0] != "fel" || tags[1] != "cm2" {
		t.Errorf("Tags() = %v, want [fel cm2]", tags)
	}
}

func TestParseDvSummary_Profile7MEL(t *testing.T) {
	// MEL = absence of FEL marker on a Profile 7 stream. Bash logic
	// at dv-hdr_tagarr.sh:288 — Profile 7 without the FEL keyword
	// defaults to MEL.
	in := `Profile: 7.6
MEL
DM version: 1
CM v2.9
`
	got := ParseDvSummary(in)
	if got.Profile != 7 {
		t.Errorf("Profile = %d, want 7", got.Profile)
	}
	if got.Layer != "mel" {
		t.Errorf("Layer = %q, want mel", got.Layer)
	}
	if got.CMVersion != 2 {
		t.Errorf("CMVersion = %d, want 2", got.CMVersion)
	}
}

func TestParseDvSummary_Profile8CM4(t *testing.T) {
	in := `Profile: 8.1
DM version: 2
CM v4.0
`
	got := ParseDvSummary(in)
	if got.Profile != 8 {
		t.Errorf("Profile = %d, want 8", got.Profile)
	}
	if got.Layer != "" {
		t.Errorf("Layer = %q, want empty (Profile 8 has no layer)", got.Layer)
	}
	if got.CMVersion != 4 {
		t.Errorf("CMVersion = %d, want 4", got.CMVersion)
	}
	tags := got.Tags()
	if len(tags) != 2 || tags[0] != "dvprofile8" || tags[1] != "cm4" {
		t.Errorf("Tags() = %v, want [dvprofile8 cm4]", tags)
	}
}

func TestParseDvSummary_UnknownProfile(t *testing.T) {
	// Profile 5 (or any unrecognised profile) — no profile/layer tag
	// per bash behaviour. The base `dv` tag still gets added by the
	// caller; this layer only contributes detail.
	in := `Profile: 5.0
DM version: 1
CM v2.9
`
	got := ParseDvSummary(in)
	if got.Profile != 0 {
		t.Errorf("Profile = %d, want 0 (unknown)", got.Profile)
	}
	if got.Layer != "" {
		t.Errorf("Layer = %q, want empty", got.Layer)
	}
	if got.CMVersion != 2 {
		t.Errorf("CMVersion = %d, want 2", got.CMVersion)
	}
	tags := got.Tags()
	// Only cm2; no profile tag.
	if len(tags) != 1 || tags[0] != "cm2" {
		t.Errorf("Tags() = %v, want [cm2]", tags)
	}
}

func TestParseDvSummary_EmptyInput(t *testing.T) {
	got := ParseDvSummary("")
	if got.Profile != 0 || got.Layer != "" || got.CMVersion != 0 {
		t.Errorf("empty input produced detail: %+v, want zero", got)
	}
	if tags := got.Tags(); len(tags) != 0 {
		t.Errorf("Tags() = %v, want empty", tags)
	}
}

func TestParseDvSummary_CMVariants(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"v4.0", "CM v4.0", 4},
		{"v4 no-dot", "CM v4", 4},
		{"v4.0.1", "CM v4.0.1", 4}, // sub-version after major still counts as v4
		{"v2.9", "CM v2.9", 2},
		{"v3.x falls to v2", "CM v3.5", 2}, // bash semantics: any non-v4 = cm2
		{"no version", "CM ?", 0},
		{"missing CM", "Profile: 7", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseDvSummary(tc.in)
			if got.CMVersion != tc.want {
				t.Errorf("CMVersion = %d, want %d", got.CMVersion, tc.want)
			}
		})
	}
}

func TestParseDvSummary_FelDetectionViaEnhancementLayer(t *testing.T) {
	// dovi_tool sometimes prints "FEL" alone, sometimes longer like
	// "Enhancement Layer". We accept both since the bash script
	// matches case-insensitive substring.
	in := `Profile: 7.6
Enhancement Layer present
CM v2.9
`
	got := ParseDvSummary(in)
	if got.Layer != "fel" {
		t.Errorf("Layer = %q, want fel (enhancement-layer phrase)", got.Layer)
	}
}

func TestDvDetailVocabulary_AllValuesRadarrCompatible(t *testing.T) {
	// Pin the vocab against Radarr's `^[a-z0-9-]+$` tag-label rule.
	// Same defence as TestEngineEmitValuesAreRadarrCompatible in
	// extra_tags_test.go — adding a new vocab value that breaks the
	// rule trips this test before it ever reaches Radarr's API.
	for _, v := range DvDetailVocabulary() {
		for _, c := range v {
			if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
				t.Errorf("vocab %q contains char %q — fails Radarr ^[a-z0-9-]+$", v, c)
			}
		}
		if v == "" {
			t.Error("vocab contains empty string")
		}
	}
}

func TestDvDetailVocabulary_ReturnsCopy(t *testing.T) {
	v := DvDetailVocabulary()
	v[0] = "MUTATED"
	v2 := DvDetailVocabulary()
	if v2[0] == "MUTATED" {
		t.Error("DvDetailVocabulary returned a shared backing array")
	}
}

func TestEmitDvDetailTags_DisabledReturnsNil(t *testing.T) {
	cfg := DvDetailConfig{Enabled: false}
	got := EmitDvDetailTags(DvDetail{Profile: 7, Layer: "fel", CMVersion: 2}, cfg)
	if got != nil {
		t.Errorf("got %v, want nil (disabled)", got)
	}
}

func TestEmitDvDetailTags_EmptyDetailReturnsNil(t *testing.T) {
	// DvDetail{} produces no bare values → no tags. Matches the
	// "API said DV but parser found nothing" / "Profile 5 with no
	// CM info" case. Cleaner than emitting an empty prefix string.
	cfg := DvDetailConfig{Enabled: true, Prefix: "dv-"}
	got := EmitDvDetailTags(DvDetail{}, cfg)
	if got != nil {
		t.Errorf("got %v, want nil (empty detail)", got)
	}
}

func TestEmitDvDetailTags_AllValuesAllowed(t *testing.T) {
	cfg := DvDetailConfig{Enabled: true} // nil AllowedValues = all
	got := EmitDvDetailTags(DvDetail{Profile: 7, Layer: "fel", CMVersion: 4}, cfg)
	want := []string{"fel", "cm4"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestEmitDvDetailTags_AllowedValuesFiltersOut(t *testing.T) {
	// User wants only the profile/layer values, not CM. AllowedValues
	// must drop "cm2" without affecting "fel".
	cfg := DvDetailConfig{
		Enabled:       true,
		AllowedValues: []string{"mel", "fel", "dvprofile8"},
	}
	got := EmitDvDetailTags(DvDetail{Profile: 7, Layer: "fel", CMVersion: 2}, cfg)
	if len(got) != 1 || got[0] != "fel" {
		t.Errorf("got %v, want [fel]", got)
	}
}

func TestEmitDvDetailTags_PrefixApplied(t *testing.T) {
	cfg := DvDetailConfig{Enabled: true, Prefix: "dv-"}
	got := EmitDvDetailTags(DvDetail{Profile: 8, CMVersion: 4}, cfg)
	want := []string{"dv-dvprofile8", "dv-cm4"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestEmitDvDetailTags_AllValuesFilteredOutReturnsEmpty(t *testing.T) {
	// Enabled + valid detail + every emit value rejected by
	// AllowedValues. This is the only path that returns a non-nil
	// empty slice (vs the disabled / empty-detail paths which
	// return nil). Pinning the asymmetry — callers must treat
	// both as len==0.
	cfg := DvDetailConfig{
		Enabled:       true,
		AllowedValues: []string{"cm4"}, // detail emits "fel" + "cm2", neither matches
	}
	got := EmitDvDetailTags(DvDetail{Profile: 7, Layer: "fel", CMVersion: 2}, cfg)
	if got == nil {
		t.Error("got nil, want non-nil empty slice (all-filtered-out path)")
	}
	if len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}

func TestEmitDvDetailTags_NonVocabAllowedValuesIgnored(t *testing.T) {
	// AllowedValues containing a value that isn't in the canonical
	// vocab is silently tolerated at emit-time — the engine doesn't
	// crash, it just doesn't match anything for that bogus entry.
	// Validation belongs at the API layer; the engine stays
	// permissive so a hand-edited stale config doesn't break a
	// running scan.
	cfg := DvDetailConfig{
		Enabled:       true,
		AllowedValues: []string{"mel", "fel", "bogus-not-in-vocab"},
	}
	got := EmitDvDetailTags(DvDetail{Profile: 7, Layer: "fel", CMVersion: 2}, cfg)
	// "fel" survives the allow-list, "cm2" is filtered out, "bogus"
	// is harmlessly ignored. Result: just ["fel"].
	if len(got) != 1 || got[0] != "fel" {
		t.Errorf("got %v, want [fel] (bogus value silently ignored)", got)
	}
}

func TestAllPossibleDvDetailTags_IgnoresEnabledAndAllowedValues(t *testing.T) {
	// Cleanup safety-bound: every vocab value MUST appear in the
	// map regardless of Enabled/AllowedValues, otherwise disabling
	// the feature would orphan tags users already have. Same
	// invariant pinned in extra_tags_test.go for ExtraTags.
	cfg := DvDetailConfig{
		Enabled:       false,
		AllowedValues: []string{"mel"}, // most filtered config possible
	}
	got := AllPossibleDvDetailTags(cfg)
	if len(got) != len(vocabDvDetail) {
		t.Errorf("got %d tags, want %d (all vocab)", len(got), len(vocabDvDetail))
	}
	for _, v := range vocabDvDetail {
		if got[v] != "dvdetail" {
			t.Errorf("missing %q in safety-bound", v)
		}
	}
}

func TestAllPossibleDvDetailTags_PrefixApplied(t *testing.T) {
	cfg := DvDetailConfig{Prefix: "dv-"}
	got := AllPossibleDvDetailTags(cfg)
	if got["dv-fel"] != "dvdetail" {
		t.Errorf("missing prefixed key dv-fel: %v", got)
	}
	if _, exists := got["fel"]; exists {
		t.Errorf("bare key 'fel' should not exist with prefix 'dv-'")
	}
}

func TestEmittableDvDetailTags_DisabledIsEmpty(t *testing.T) {
	got := EmittableDvDetailTags(DvDetailConfig{Enabled: false})
	if len(got) != 0 {
		t.Errorf("got %d entries, want 0 when disabled", len(got))
	}
}

func TestEmittableDvDetailTags_AllowedValuesNarrows(t *testing.T) {
	cfg := DvDetailConfig{
		Enabled:       true,
		AllowedValues: []string{"mel", "fel"},
	}
	got := EmittableDvDetailTags(cfg)
	if len(got) != 2 {
		t.Errorf("got %d entries, want 2", len(got))
	}
	if got["mel"] != "dvdetail" || got["fel"] != "dvdetail" {
		t.Errorf("missing expected entry: %v", got)
	}
	if _, exists := got["cm4"]; exists {
		t.Error("filtered-out value should not appear")
	}
}

func TestHdrTypeIndicatesDv(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		// Real Radarr mediaInfo strings.
		{"", false},
		{"HDR10", false},
		{"HDR10Plus", false},
		{"PQ", false},
		{"HLG", false},
		{"DV", true},
		{"DV HDR10", true},
		{"DV HDR10Plus", true},
		{"DV HLG", true},
		{"DV SDR", true},
		{"Dolby Vision", true},
		// Edge: lowercase. Radarr is consistently capitalised but
		// API-mocking tests sometimes lowercase. Bash is case-
		// insensitive so we are too.
		{"dv", true},
		{"dolby vision", true},
		// False positives we DON'T want — substring "dv" inside
		// other words. \b word-boundary in regex prevents this.
		{"Advanced", false},
		{"Saved", false},
		{"DVD", false}, // word-boundary anchored regex — "DVD" is one word so `\bDV\b` doesn't match.
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := HdrTypeIndicatesDv(tc.in)
			if got != tc.want {
				t.Errorf("HdrTypeIndicatesDv(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestEmitNoDvTag_DisabledReturnsNil(t *testing.T) {
	if got := EmitNoDvTag(DvDetailConfig{Enabled: false}); got != nil {
		t.Errorf("disabled cfg = %v, want nil", got)
	}
}

func TestEmitNoDvTag_EnabledNoFilter(t *testing.T) {
	got := EmitNoDvTag(DvDetailConfig{Enabled: true})
	if len(got) != 1 || got[0] != "no-dv" {
		t.Errorf("got %v, want [no-dv]", got)
	}
}

func TestEmitNoDvTag_PrefixApplied(t *testing.T) {
	got := EmitNoDvTag(DvDetailConfig{Enabled: true, Prefix: "media-"})
	if len(got) != 1 || got[0] != "media-no-dv" {
		t.Errorf("got %v, want [media-no-dv]", got)
	}
}

func TestEmitNoDvTag_AllowedValuesFiltersOut(t *testing.T) {
	// User only wants positive DV-detail tags (mel/fel/dvprofile8/cm2/cm4),
	// not no-dv. AllowedValues without "no-dv" should suppress emission.
	cfg := DvDetailConfig{
		Enabled:       true,
		AllowedValues: []string{"mel", "fel", "dvprofile8", "cm2", "cm4"},
	}
	if got := EmitNoDvTag(cfg); got != nil {
		t.Errorf("filtered-out cfg = %v, want nil", got)
	}
}

func TestEmitNoDvTag_AllowedValuesIncludesNoDv(t *testing.T) {
	// AllowedValues explicitly listing no-dv allows it through even
	// when other values are restricted.
	cfg := DvDetailConfig{
		Enabled:       true,
		AllowedValues: []string{"no-dv"},
	}
	got := EmitNoDvTag(cfg)
	if len(got) != 1 || got[0] != "no-dv" {
		t.Errorf("got %v, want [no-dv] when explicitly listed", got)
	}
}

func TestEmitNoDvTag_LegacyEmptyAllowsEverything(t *testing.T) {
	// SelectMode != "select" with empty AllowedValues = legacy
	// "all-allowed" mode. no-dv should emit.
	cfg := DvDetailConfig{Enabled: true, AllowedValues: nil, SelectMode: ""}
	got := EmitNoDvTag(cfg)
	if len(got) != 1 || got[0] != "no-dv" {
		t.Errorf("got %v, want [no-dv] in legacy empty-allow mode", got)
	}
}

func TestEmitNoDvTag_SelectModeEmptyTagsNothing(t *testing.T) {
	// SelectMode == "select" + empty AllowedValues = explicit
	// "tag nothing". no-dv should NOT emit even though it's in vocab.
	cfg := DvDetailConfig{Enabled: true, AllowedValues: nil, SelectMode: "select"}
	if got := EmitNoDvTag(cfg); got != nil {
		t.Errorf("select-mode empty cfg = %v, want nil", got)
	}
}

func TestVocabIncludesNoDv(t *testing.T) {
	// Drift sentinel — adding/removing no-dv from vocab without
	// updating the no-dv emit path or tests would fail this. Pin
	// the explicit value so a refactor surfaces the change.
	vocab := DvDetailVocabulary()
	found := false
	for _, v := range vocab {
		if v == "no-dv" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("vocab missing no-dv: %v", vocab)
	}
}

func TestAllPossibleDvDetailTags_IncludesNoDv(t *testing.T) {
	// no-dv must be in the cleanup safety-bound (the universe of
	// labels resolvarr could ever emit). Otherwise an existing
	// no-dv tag from a previous scan with different config wouldn't
	// be cleaned up by RemoveOrphanedTags.
	got := AllPossibleDvDetailTags(DvDetailConfig{Prefix: "p-"})
	if _, ok := got["p-no-dv"]; !ok {
		t.Errorf("AllPossibleDvDetailTags missing p-no-dv: keys=%v", keysOf(got))
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
