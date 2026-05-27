package api

import (
	"reflect"
	"sort"
	"testing"

	"resolvarr/internal/arr"
	"resolvarr/internal/core"
	"resolvarr/internal/plex"
)

// ---------- 4-tier match strategy ----------

func TestParsePlexGUIDs_Standard(t *testing.T) {
	tmdb, tvdb, imdb := parsePlexGUIDs([]string{
		"tmdb://933260",
		"imdb://tt17526714",
	})
	if tmdb != 933260 {
		t.Errorf("tmdb = %d, want 933260", tmdb)
	}
	if tvdb != 0 {
		t.Errorf("tvdb should be 0, got %d", tvdb)
	}
	if imdb != "tt17526714" {
		t.Errorf("imdb = %q, want tt17526714", imdb)
	}
}

func TestParsePlexGUIDs_IgnoresPlexNativeAndLocal(t *testing.T) {
	tmdb, tvdb, imdb := parsePlexGUIDs([]string{
		"plex://movie/abc123",
		"local://12345",
		"tmdb://42",
	})
	if tmdb != 42 || tvdb != 0 || imdb != "" {
		t.Errorf("native/local GUIDs leaked through: tmdb=%d tvdb=%d imdb=%q", tmdb, tvdb, imdb)
	}
}

func TestParsePlexGUIDs_MalformedNumericFallsToZero(t *testing.T) {
	tmdb, tvdb, _ := parsePlexGUIDs([]string{
		"tmdb://not-a-number",
		"tvdb://abc",
	})
	if tmdb != 0 || tvdb != 0 {
		t.Errorf("malformed numerics should parse to 0, got tmdb=%d tvdb=%d", tmdb, tvdb)
	}
}

func TestNormalisePlexTitle(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Star Wars: Episode IV - A New Hope", "star wars episode iv a new hope"},
		{"  Heat   ", "heat"},
		{"WALL·E", "wall e"},
		{"!!!", ""},
		{"", ""},
	}
	for _, tc := range cases {
		if got := normalisePlexTitle(tc.in); got != tc.want {
			t.Errorf("normalisePlexTitle(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestMatcher_Tier1_TmdbImdbCompound locks the strongest tier.
// Both IDs must match the same Arr item.
func TestMatcher_Tier1_TmdbImdbCompound(t *testing.T) {
	items := []arr.Item{
		{ID: 1, Title: "The Substance", Year: 2024, TmdbID: 933260, ImdbID: "tt17526714"},
		// Decoy with same TMDB but different IMDb — should NOT match
		// when Plex carries the OTHER IMDb.
		{ID: 2, Title: "Wrong Movie", Year: 2024, TmdbID: 933260, ImdbID: "tt00000001"},
	}
	idx := buildPlexMatchIndex(items)
	plexItem := plex.Item{
		Title: "The Substance",
		Year:  2024,
		Type:  "movie",
		GUIDs: []string{"tmdb://933260", "imdb://tt17526714"},
	}
	matched, tier := matchPlexItemToArrItem(plexItem, idx)
	if matched == nil {
		t.Fatal("expected a match")
	}
	if matched.ID != 1 {
		t.Errorf("matched wrong item: ID=%d, want 1", matched.ID)
	}
	if tier != "tmdb+imdb" {
		t.Errorf("expected tier tmdb+imdb, got %q", tier)
	}
}

// TestMatcher_FallbackOrder locks the priority of single-ID tiers.
// When TMDB+IMDB doesn't compound-match, but TMDB alone matches one
// Arr item, we use that tier — NOT a title-year fallback that might
// pick a remake.
func TestMatcher_FallbackOrder(t *testing.T) {
	items := []arr.Item{
		{ID: 10, Title: "Heat", Year: 1995, TmdbID: 949, ImdbID: "tt0113277"},
		{ID: 11, Title: "Heat", Year: 2024, TmdbID: 999999}, // remake — same title, different year
	}
	idx := buildPlexMatchIndex(items)
	// Plex item has TMDB but NO IMDb — tier 1 + tier 2 (compound)
	// can't fire; tier 3 (single TMDB) should pick the right item.
	plexItem := plex.Item{Title: "Heat", Year: 1995, GUIDs: []string{"tmdb://949"}}
	matched, tier := matchPlexItemToArrItem(plexItem, idx)
	if matched == nil || matched.ID != 10 {
		t.Fatalf("matcher picked the wrong item: %+v (tier=%q)", matched, tier)
	}
	if tier != "tmdb" {
		t.Errorf("expected single-TMDB tier, got %q", tier)
	}
}

// TestMatcher_TitleYearLastResort locks the final fallback. When all
// IDs are missing, normalised title+year disambiguates remakes.
func TestMatcher_TitleYearLastResort(t *testing.T) {
	items := []arr.Item{
		{ID: 20, Title: "Solaris", Year: 1972},
		{ID: 21, Title: "Solaris", Year: 2002},
	}
	idx := buildPlexMatchIndex(items)
	plexItem := plex.Item{Title: "Solaris", Year: 2002}
	matched, tier := matchPlexItemToArrItem(plexItem, idx)
	if matched == nil || matched.ID != 21 {
		t.Fatalf("title+year fallback picked the wrong item: %+v", matched)
	}
	if tier != "title+year" {
		t.Errorf("expected tier title+year, got %q", tier)
	}
}

// TestMatcher_NoMatchOnTitleAloneWithoutYear locks that title alone
// (no year, no IDs) returns no match — defends against picking the
// wrong remake when Plex's metadata is broken.
func TestMatcher_NoMatchOnTitleAloneWithoutYear(t *testing.T) {
	items := []arr.Item{
		{ID: 30, Title: "Solaris", Year: 2002},
	}
	idx := buildPlexMatchIndex(items)
	plexItem := plex.Item{Title: "Solaris" /* no Year, no GUIDs */}
	matched, _ := matchPlexItemToArrItem(plexItem, idx)
	if matched != nil {
		t.Errorf("expected no match for title-only without year; got %+v", matched)
	}
}

// TestMatcher_TitleNormalisationHandlesPunctuation locks title-+-year
// matching across "Star Wars: Episode IV - A New Hope" punctuation
// variants — Plex and Arr each render the title slightly differently.
func TestMatcher_TitleNormalisationHandlesPunctuation(t *testing.T) {
	items := []arr.Item{
		{ID: 40, Title: "Star Wars: Episode IV - A New Hope", Year: 1977},
	}
	idx := buildPlexMatchIndex(items)
	plexItem := plex.Item{
		Title: "Star Wars   Episode IV   A  New Hope",
		Year:  1977,
	}
	matched, _ := matchPlexItemToArrItem(plexItem, idx)
	if matched == nil || matched.ID != 40 {
		t.Errorf("title normalisation should match across punctuation variants; got %+v", matched)
	}
}

// ---------- label diff (the core add/remove decision) ----------

func sortedCopy(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}

// TestComputeDiff_NoChanges — Arr + Plex already agree. No adds, no
// removes. Idempotent re-runs land here for every already-synced item.
func TestComputeDiff_NoChanges(t *testing.T) {
	whitelistedTagIDs := map[int]string{1: "4k", 2: "hdr"}
	whitelistByLower := map[string]string{"4k": "4k", "hdr": "hdr"}
	arrTagIDs := []int{1, 2}
	plexLabels := []string{"4k", "hdr"}

	diff := computePlexLabelDiff(arrTagIDs, plexLabels, whitelistedTagIDs, whitelistByLower)
	if len(diff.add) != 0 || len(diff.remove) != 0 {
		t.Errorf("expected no changes; got add=%v remove=%v", diff.add, diff.remove)
	}
}

// TestComputeDiff_AddLabel — Arr has the tag, Plex doesn't.
func TestComputeDiff_AddLabel(t *testing.T) {
	whitelistedTagIDs := map[int]string{1: "4k"}
	whitelistByLower := map[string]string{"4k": "4k"}
	diff := computePlexLabelDiff([]int{1}, []string{}, whitelistedTagIDs, whitelistByLower)
	if !reflect.DeepEqual(diff.add, []string{"4k"}) {
		t.Errorf("expected add [4k]; got %v", diff.add)
	}
	if len(diff.remove) != 0 {
		t.Errorf("expected no removes; got %v", diff.remove)
	}
}

// TestComputeDiff_RemoveLabel — Plex has the label, Arr doesn't.
func TestComputeDiff_RemoveLabel(t *testing.T) {
	whitelistedTagIDs := map[int]string{1: "4k"}
	whitelistByLower := map[string]string{"4k": "4k"}
	diff := computePlexLabelDiff([]int{}, []string{"4k"}, whitelistedTagIDs, whitelistByLower)
	if !reflect.DeepEqual(diff.remove, []string{"4k"}) {
		t.Errorf("expected remove [4k]; got %v", diff.remove)
	}
}

// TestComputeDiff_WhitelistScope_PreservesManualLabels — Plex has
// "favorite" outside the whitelist. Engine must NOT touch it. This
// is the core invariant from the analysis doc: manual Plex labels
// outside the whitelist are sacrosanct.
func TestComputeDiff_WhitelistScope_PreservesManualLabels(t *testing.T) {
	whitelistedTagIDs := map[int]string{1: "4k"}
	whitelistByLower := map[string]string{"4k": "4k"}
	// Plex has "favorite" (not in whitelist) + "4k" (in whitelist).
	// Arr has "4k". → No remove, no add (already synced; favorite
	// stays untouched because it's outside whitelist).
	diff := computePlexLabelDiff([]int{1}, []string{"favorite", "4k"}, whitelistedTagIDs, whitelistByLower)
	if len(diff.add) != 0 {
		t.Errorf("expected no adds; got %v", diff.add)
	}
	if len(diff.remove) != 0 {
		t.Errorf("favorite label should be untouched (outside whitelist); got remove=%v", diff.remove)
	}
}

// TestComputeDiff_WhitelistScope_RemovesOnlyWhitelistLabels — Plex
// has "favorite" (not in whitelist) + "hdr" (in whitelist). Arr
// lacks the corresponding tag for hdr. → Remove hdr, leave favorite.
func TestComputeDiff_WhitelistScope_RemovesOnlyWhitelistLabels(t *testing.T) {
	whitelistedTagIDs := map[int]string{1: "4k", 2: "hdr"}
	whitelistByLower := map[string]string{"4k": "4k", "hdr": "hdr"}
	diff := computePlexLabelDiff([]int{1}, []string{"favorite", "hdr", "4k"}, whitelistedTagIDs, whitelistByLower)
	if len(diff.add) != 0 {
		t.Errorf("expected no adds; got %v", diff.add)
	}
	if !reflect.DeepEqual(diff.remove, []string{"hdr"}) {
		t.Errorf("expected remove [hdr]; favorite must stay; got %v", diff.remove)
	}
}

// TestComputeDiff_CaseInsensitive — Plex stores "4K" (user-typed
// caps), Arr stores "4k". They must be treated as the same label.
func TestComputeDiff_CaseInsensitive(t *testing.T) {
	whitelistedTagIDs := map[int]string{1: "4k"}
	whitelistByLower := map[string]string{"4k": "4k"}
	diff := computePlexLabelDiff([]int{1}, []string{"4K"}, whitelistedTagIDs, whitelistByLower)
	if len(diff.add) != 0 || len(diff.remove) != 0 {
		t.Errorf("case difference should not produce changes; got add=%v remove=%v", diff.add, diff.remove)
	}
}

// TestComputeDiff_AddAndRemoveInSameRun — Arr has new tag X, Plex
// has stale tag Y. Both in whitelist. → Add X, remove Y.
func TestComputeDiff_AddAndRemoveInSameRun(t *testing.T) {
	whitelistedTagIDs := map[int]string{1: "4k", 2: "hdr"}
	whitelistByLower := map[string]string{"4k": "4k", "hdr": "hdr"}
	// Arr has 4k; Plex has hdr.
	diff := computePlexLabelDiff([]int{1}, []string{"hdr"}, whitelistedTagIDs, whitelistByLower)
	if !reflect.DeepEqual(sortedCopy(diff.add), []string{"4k"}) {
		t.Errorf("expected add [4k]; got %v", diff.add)
	}
	if !reflect.DeepEqual(diff.remove, []string{"hdr"}) {
		t.Errorf("expected remove [hdr]; got %v", diff.remove)
	}
}

// TestDisplayLabel_OverrideAndFallback locks the per-rule "Display
// as" override (PlexLabelRule.LabelDisplay) behaviour. Lets a Radarr
// tag "atmos" render as "Atmos" on Plex even though Radarr forces
// lowercase-kebab on the tag side.
func TestDisplayLabel_OverrideAndFallback(t *testing.T) {
	rule := core.PlexLabelRule{
		Labels: []string{"atmos", "hdr", "fel"},
		LabelDisplay: map[string]string{
			"atmos": "Atmos",
			"fel":   "FEL",
			// "hdr" has no override — falls back to verbatim
		},
	}
	cases := map[string]string{
		"atmos":   "Atmos",   // overridden
		"fel":     "FEL",     // overridden
		"hdr":     "hdr",     // no override → fallback
		"missing": "missing", // tag not in map at all → fallback
	}
	for in, want := range cases {
		if got := rule.DisplayLabel(in); got != want {
			t.Errorf("DisplayLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestDisplayLabel_EmptyOverrideFallsBack — empty / whitespace
// override is treated as "no override" (same as missing key). Defends
// against the UI saving an empty string when the user clears the input.
func TestDisplayLabel_EmptyOverrideFallsBack(t *testing.T) {
	rule := core.PlexLabelRule{
		Labels: []string{"atmos"},
		LabelDisplay: map[string]string{
			"atmos": "   ", // user cleared the override
		},
	}
	if got := rule.DisplayLabel("atmos"); got != "atmos" {
		t.Errorf("whitespace override should fall back to verbatim; got %q", got)
	}
}

// TestComputeDiff_InSyncRecorded locks the InSync field — whitelist
// labels that are already correctly applied on both sides should be
// recorded so the result UI can show "FEL: 33 in sync" alongside the
// adds + removes.
func TestComputeDiff_InSyncRecorded(t *testing.T) {
	whitelistedTagIDs := map[int]string{1: "4k", 2: "hdr", 3: "atmos"}
	whitelistByLower := map[string]string{"4k": "4k", "hdr": "hdr", "atmos": "atmos"}
	// Arr has 4k + hdr; Plex has 4k + atmos.
	//   4k → in sync (both have)
	//   hdr → add (Arr has, Plex doesn't)
	//   atmos → remove (Plex has, Arr doesn't)
	diff := computePlexLabelDiff([]int{1, 2}, []string{"4k", "atmos"}, whitelistedTagIDs, whitelistByLower)
	if !reflect.DeepEqual(diff.add, []string{"hdr"}) {
		t.Errorf("expected add [hdr]; got %v", diff.add)
	}
	if !reflect.DeepEqual(diff.remove, []string{"atmos"}) {
		t.Errorf("expected remove [atmos]; got %v", diff.remove)
	}
	if !reflect.DeepEqual(diff.inSync, []string{"4k"}) {
		t.Errorf("expected inSync [4k]; got %v", diff.inSync)
	}
}

// TestComputeDiff_InSyncOnly_NoActionItem — when every whitelist
// label is already correct (both sides agree on every label), the
// item produces no actions but the inSync list still captures the
// labels for per-label accounting.
func TestComputeDiff_InSyncOnly_NoActionItem(t *testing.T) {
	whitelistedTagIDs := map[int]string{1: "4k", 2: "hdr"}
	whitelistByLower := map[string]string{"4k": "4k", "hdr": "hdr"}
	diff := computePlexLabelDiff([]int{1, 2}, []string{"4k", "hdr"}, whitelistedTagIDs, whitelistByLower)
	if len(diff.add) != 0 || len(diff.remove) != 0 {
		t.Errorf("expected no actions; got add=%v remove=%v", diff.add, diff.remove)
	}
	if !reflect.DeepEqual(diff.inSync, []string{"4k", "hdr"}) {
		t.Errorf("expected inSync [4k hdr]; got %v", diff.inSync)
	}
}

// TestComputeDiff_AddRemoveAreSliceSafeOnEmptyInputs locks the
// defensive behaviour when whitelistedTagIDs is empty (e.g. every
// whitelist label is preempt-config and doesn't resolve in Arr).
// add slice stays empty; remove still fires for Plex labels that
// match the whitelistByLower scope.
func TestComputeDiff_AddRemoveAreSliceSafeOnEmptyInputs(t *testing.T) {
	// Empty tagIDByLabel resolution — whitelist still scopes Plex
	// removes via whitelistByLower.
	whitelistedTagIDs := map[int]string{}
	whitelistByLower := map[string]string{"4k": "4k"}
	diff := computePlexLabelDiff([]int{1, 2}, []string{"4k", "manual"}, whitelistedTagIDs, whitelistByLower)
	if len(diff.add) != 0 {
		t.Errorf("expected no adds when whitelistedTagIDs empty; got %v", diff.add)
	}
	if !reflect.DeepEqual(diff.remove, []string{"4k"}) {
		t.Errorf("expected remove [4k]; got %v", diff.remove)
	}
}

// TestComputeDiff_WhitelistMissingFromArr — whitelist has labels
// that don't resolve to Arr tag-IDs (because the user wrote the tag-
// name before creating the tag in Arr). Those labels effectively
// "Arr doesn't have it" → engine REMOVES them if Plex has them, but
// only since the whitelistByLower map still scopes them in. Edge
// case the analysis doc calls out as "preempt-config".
func TestComputeDiff_WhitelistMissingFromArr_RemovesIfPlexHas(t *testing.T) {
	// Whitelist includes "kids" + "4k". Arr-tag lookup only resolved
	// "4k" — "kids" isn't a tag in Arr yet. Plex has both labels on
	// this item.
	whitelistedTagIDs := map[int]string{1: "4k"} // only 4k resolved
	whitelistByLower := map[string]string{"4k": "4k", "kids": "kids"}
	// Arr item has the 4k tag. Plex has both labels.
	diff := computePlexLabelDiff([]int{1}, []string{"4k", "kids"}, whitelistedTagIDs, whitelistByLower)
	// 4k is already in sync (no add, no remove).
	// kids is in whitelist but Arr-side resolution failed (kids tag
	// doesn't exist in Arr), so engine sees "Arr lacks it" and Plex
	// has it → remove kids.
	if !reflect.DeepEqual(diff.remove, []string{"kids"}) {
		t.Errorf("preempt-config whitelist label should remove if Plex has it; got remove=%v", diff.remove)
	}
}
