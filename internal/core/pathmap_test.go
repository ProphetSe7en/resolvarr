package core

import "testing"

func TestTranslatePath_NoMappingsPassthrough(t *testing.T) {
	// Default Unraid TRaSH-aligned setup: zero mappings, paths
	// pass through unchanged. The "no work needed" happy path.
	got := TranslatePathWithMappings("/data/media/movies/Foo (2024)/foo.mkv", nil)
	want := "/data/media/movies/Foo (2024)/foo.mkv"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestTranslatePath_EmptyApiPathPassthrough(t *testing.T) {
	got := TranslatePathWithMappings("", []PathMapping{{From: "/movies", To: "/data/movies"}})
	if got != "" {
		t.Errorf("empty input mutated to %q", got)
	}
}

func TestTranslatePath_ExactPrefixReplace(t *testing.T) {
	mappings := []PathMapping{{From: "/movies", To: "/data/movies"}}
	got := TranslatePathWithMappings("/movies/Foo (2024)/foo.mkv", mappings)
	want := "/data/movies/Foo (2024)/foo.mkv"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestTranslatePath_ExactPathOnlyMatch(t *testing.T) {
	// Edge case: API returns the bare folder root, no trailing
	// segment. apiPath == From should still translate.
	mappings := []PathMapping{{From: "/movies", To: "/data/movies"}}
	got := TranslatePathWithMappings("/movies", mappings)
	if got != "/data/movies" {
		t.Errorf("got %q, want /data/movies", got)
	}
}

func TestTranslatePath_NoFalseSubstringMatch(t *testing.T) {
	// The Bazarr-style bug: mapping "/movies" must NOT match
	// "/moviesextra/...". Word-boundary via "/" anchor protects us.
	mappings := []PathMapping{{From: "/movies", To: "/data/movies"}}
	got := TranslatePathWithMappings("/moviesextra/Foo/foo.mkv", mappings)
	want := "/moviesextra/Foo/foo.mkv"
	if got != want {
		t.Errorf("got %q, want %q (false-prefix bug)", got, want)
	}
}

func TestTranslatePath_LongestPrefixFirst(t *testing.T) {
	// Nested mappings: "/movies/4k" must win over "/movies" for
	// paths under the 4k subtree. Order in the slice intentionally
	// puts the shorter one first to verify sort-by-length works.
	mappings := []PathMapping{
		{From: "/movies", To: "/data/movies"},
		{From: "/movies/4k", To: "/data/4k-movies"},
	}
	got := TranslatePathWithMappings("/movies/4k/Foo (2024)/foo.mkv", mappings)
	want := "/data/4k-movies/Foo (2024)/foo.mkv"
	if got != want {
		t.Errorf("longest-prefix-first failed: got %q, want %q", got, want)
	}
	// And the shorter mapping still works for non-nested paths.
	got2 := TranslatePathWithMappings("/movies/Bar (2023)/bar.mkv", mappings)
	want2 := "/data/movies/Bar (2023)/bar.mkv"
	if got2 != want2 {
		t.Errorf("shorter mapping broke: got %q, want %q", got2, want2)
	}
}

func TestTranslatePath_TrailingSlashOnFromNormalised(t *testing.T) {
	// User typed "/movies/" — should behave identically to "/movies".
	mappings := []PathMapping{{From: "/movies/", To: "/data/movies"}}
	got := TranslatePathWithMappings("/movies/Foo/foo.mkv", mappings)
	want := "/data/movies/Foo/foo.mkv"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestTranslatePath_TrailingSlashOnToPreserved(t *testing.T) {
	// We don't second-guess the To side. If the user typed a
	// trailing slash, the resulting path will have a double slash
	// at the join — harmless on Linux, user's choice.
	mappings := []PathMapping{{From: "/movies", To: "/data/movies/"}}
	got := TranslatePathWithMappings("/movies/foo.mkv", mappings)
	if got != "/data/movies//foo.mkv" {
		t.Errorf("got %q, want /data/movies//foo.mkv (preserve user's To)", got)
	}
}

func TestTranslatePath_NoMatchPassthrough(t *testing.T) {
	mappings := []PathMapping{{From: "/movies", To: "/data/movies"}}
	got := TranslatePathWithMappings("/tv/Foo/S01E01.mkv", mappings)
	want := "/tv/Foo/S01E01.mkv"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestTranslatePath_EmptySidesIgnored(t *testing.T) {
	// Mappings with empty From or empty To are silently skipped —
	// likely a half-edited row. Validation belongs in the API
	// layer; the translator must never panic on bad data.
	mappings := []PathMapping{
		{From: "", To: "/data/movies"},
		{From: "/movies", To: ""},
		{From: "/tv", To: "/data/tv"},
	}
	got := TranslatePathWithMappings("/movies/foo.mkv", mappings)
	if got != "/movies/foo.mkv" {
		t.Errorf("empty-To mapping should not have applied: got %q", got)
	}
	got2 := TranslatePathWithMappings("/tv/foo.mkv", mappings)
	if got2 != "/data/tv/foo.mkv" {
		t.Errorf("valid mapping should still work: got %q", got2)
	}
}

func TestTranslatePath_FirstMatchWinsAtSameLength(t *testing.T) {
	// Two same-length mappings (e.g. /movies vs /tv, both length 4):
	// stable sort keeps user declaration order so the earlier one
	// wins. Pinning the behaviour so a future switch to an unstable
	// sort can't silently re-order matches.
	mappings := []PathMapping{
		{From: "/aaa", To: "/first"},
		{From: "/bbb", To: "/never"},
	}
	got := TranslatePathWithMappings("/aaa/foo.mkv", mappings)
	if got != "/first/foo.mkv" {
		t.Errorf("got %q, want /first/foo.mkv", got)
	}
}

func TestTranslatePath_RelativeApiPathPassthrough(t *testing.T) {
	// Relative input paths can never match an absolute mapping
	// prefix; they fall through. Pinning the documented behaviour.
	mappings := []PathMapping{{From: "/movies", To: "/data/movies"}}
	got := TranslatePathWithMappings("foo.mkv", mappings)
	if got != "foo.mkv" {
		t.Errorf("relative input mutated to %q", got)
	}
	got2 := TranslatePathWithMappings("subdir/foo.mkv", mappings)
	if got2 != "subdir/foo.mkv" {
		t.Errorf("relative input mutated to %q", got2)
	}
}

func TestTranslatePath_RootOnlyFromIsSkipped(t *testing.T) {
	// From="/" trims to "" after the trailing-slash normalisation
	// and gets skipped. A "translate everything under root" mapping
	// is silently rejected — the API validator should reject this
	// at save-time with a clear error, but the translator must not
	// panic or produce surprising matches.
	mappings := []PathMapping{
		{From: "/", To: "/data"},
		{From: "/movies", To: "/data/movies"},
	}
	// "/" mapping is dropped → /movies still works.
	got := TranslatePathWithMappings("/movies/foo.mkv", mappings)
	if got != "/data/movies/foo.mkv" {
		t.Errorf("got %q, want /data/movies/foo.mkv", got)
	}
	// Path that only the "/" mapping could have matched stays
	// unchanged because the mapping was dropped.
	got2 := TranslatePathWithMappings("/tv/foo.mkv", mappings)
	if got2 != "/tv/foo.mkv" {
		t.Errorf("got %q, want /tv/foo.mkv (root-mapping was rightly dropped)", got2)
	}
}

func TestConfigStore_GetDeepCopiesPathMappings(t *testing.T) {
	// Regression-pin the same class of header-aliasing bug as
	// NotificationAgents.AppriseURLs and ExtraTags.AllowedValues:
	// ConfigStore.Get must return a slice that doesn't share its
	// backing array with the store, so a caller mutating the
	// returned slice can't corrupt the store.
	s := &ConfigStore{}
	s.cfg.Instances = []Instance{
		{
			ID:   "test",
			Name: "Radarr",
			Type: "radarr",
			URL:  "http://example",
			PathMappings: []PathMapping{
				{From: "/movies", To: "/data/movies"},
			},
		},
	}
	got := s.Get()
	if len(got.Instances) != 1 || len(got.Instances[0].PathMappings) != 1 {
		t.Fatalf("unexpected Get result: %+v", got.Instances)
	}
	// Mutate the returned slice — should not affect the store.
	got.Instances[0].PathMappings[0].To = "/HACKED"
	if s.cfg.Instances[0].PathMappings[0].To != "/data/movies" {
		t.Errorf("store was mutated via returned slice: %q", s.cfg.Instances[0].PathMappings[0].To)
	}
}

func TestTranslatePath_InstanceMethod(t *testing.T) {
	// Sanity-check the receiver method delegates to the pure func.
	inst := &Instance{
		PathMappings: []PathMapping{{From: "/movies", To: "/data/movies"}},
	}
	got := inst.TranslatePath("/movies/foo.mkv")
	if got != "/data/movies/foo.mkv" {
		t.Errorf("got %q, want /data/movies/foo.mkv", got)
	}
}

func TestTranslatePath_NilInstance(t *testing.T) {
	// Nil-receiver no-op so callers handling optional Instance
	// pointers don't need pre-flight checks.
	var inst *Instance
	got := inst.TranslatePath("/movies/foo.mkv")
	if got != "/movies/foo.mkv" {
		t.Errorf("got %q, want pass-through", got)
	}
}

func TestTranslatePath_OriginalSliceUnmodified(t *testing.T) {
	// Internal sort works on a copy. Catches a class of bug where
	// the shared []PathMapping in Instance gets reordered as a
	// side effect of translation, which would surface days later
	// as confusing UI ordering.
	mappings := []PathMapping{
		{From: "/movies", To: "/data/movies"},
		{From: "/movies/4k", To: "/data/4k"},
	}
	_ = TranslatePathWithMappings("/movies/4k/foo.mkv", mappings)
	if mappings[0].From != "/movies" || mappings[1].From != "/movies/4k" {
		t.Errorf("input slice was reordered: %+v", mappings)
	}
}
