package core

import (
	"reflect"
	"testing"
)

// webhook_rules_test.go — coverage for the M-Webhook foundation pure
// helpers. Locks the per-Arr-type asymmetry (review concern #7) so a
// future refactor that drops one branch from WebhookFunctionAppliesTo
// can't silently let Sonarr rules pick Tag Release Groups.
//
// Out of scope: the dispatcher loop (depends on Server / ConfigStore;
// covered separately), the CRUD validator (in api/), and the function
// adapters (stubs today, real coverage lands per task #5/#7/#8/#9).

func TestWebhookFunctionAppliesTo_PerInstanceTypeMatrix(t *testing.T) {
	// Per dev/analysis/M-webhook.md § "Per-Arr-type scope":
	cases := []struct {
		fn         WebhookFunction
		radarr     bool
		sonarr     bool
	}{
		{WebhookFnTagReleaseGroups, true, false},
		{WebhookFnDiscover, true, false},
		{WebhookFnTagDvDetail, true, false},
		{WebhookFnSyncToSecondary, true, false},
		{WebhookFnQbitSeTag, false, true},
		{WebhookFnTagAudio, true, true},
		{WebhookFnTagVideo, true, true},
		{WebhookFnRecover, true, true},
		{WebhookFnFileDeleteClean, true, true},
		{WebhookFnGrabRename, true, true},
	}
	for _, c := range cases {
		if got := WebhookFunctionAppliesTo(c.fn, "radarr"); got != c.radarr {
			t.Errorf("AppliesTo(%s, radarr) = %v, want %v", c.fn, got, c.radarr)
		}
		if got := WebhookFunctionAppliesTo(c.fn, "sonarr"); got != c.sonarr {
			t.Errorf("AppliesTo(%s, sonarr) = %v, want %v", c.fn, got, c.sonarr)
		}
		// Empty / unknown AppType must reject every function — defence
		// against tampered configs that landed via direct file edit.
		if WebhookFunctionAppliesTo(c.fn, "") {
			t.Errorf("AppliesTo(%s, '') = true, want false (empty AppType must reject)", c.fn)
		}
		if WebhookFunctionAppliesTo(c.fn, "unknownType") {
			t.Errorf("AppliesTo(%s, unknownType) = true, want false", c.fn)
		}
	}
}

func TestEventsForFunction_MatchesAppliesTo(t *testing.T) {
	// Contract documented in EventsForFunction: returns nil when fn
	// doesn't apply to appType. Inconsistency with WebhookFunctionAppliesTo
	// would be a maintenance trap; this test keeps them honest.
	for _, fn := range allWebhookFunctions {
		for _, appType := range []string{"radarr", "sonarr"} {
			events := EventsForFunction(fn, appType)
			applies := WebhookFunctionAppliesTo(fn, appType)
			if applies && len(events) == 0 {
				t.Errorf("EventsForFunction(%s, %s) returned empty but AppliesTo says yes", fn, appType)
			}
			if !applies && len(events) > 0 {
				t.Errorf("EventsForFunction(%s, %s) returned %v but AppliesTo says no", fn, appType, events)
			}
		}
	}
	// Sonarr S/E tagging fires on Grab.
	if got := EventsForFunction(WebhookFnQbitSeTag, "sonarr"); !reflect.DeepEqual(got, []WebhookConnectEvent{WebhookEventGrab}) {
		t.Errorf("QbitSeTag/sonarr events = %v, want [Grab]", got)
	}
	// Radarr file-delete maps to MovieFileDelete + MovieFileDeleteForUpgrade
	// (bash defends against both at tagarr_import.sh:574). Sonarr same shape.
	wantRadarr := []WebhookConnectEvent{WebhookEventMovieFileDelete, WebhookEventMovieFileDeleteForUpgrade}
	if got := EventsForFunction(WebhookFnFileDeleteClean, "radarr"); !reflect.DeepEqual(got, wantRadarr) {
		t.Errorf("FileDeleteClean/radarr events = %v, want %v", got, wantRadarr)
	}
	wantSonarr := []WebhookConnectEvent{WebhookEventEpisodeFileDelete, WebhookEventEpisodeFileDeleteForUpgrade}
	if got := EventsForFunction(WebhookFnFileDeleteClean, "sonarr"); !reflect.DeepEqual(got, wantSonarr) {
		t.Errorf("FileDeleteClean/sonarr events = %v, want %v", got, wantSonarr)
	}
}

func TestWebhookRule_FiresOn(t *testing.T) {
	// Rule with TagAudio + GrabRename on Sonarr fires on Download AND Grab,
	// not on file-delete events.
	r := WebhookRule{
		AppType:   "sonarr",
		Functions: []WebhookFunction{WebhookFnTagAudio, WebhookFnGrabRename},
	}
	if !r.FiresOn(WebhookEventDownload) {
		t.Error("expected FiresOn(Download) for TagAudio rule")
	}
	if !r.FiresOn(WebhookEventGrab) {
		t.Error("expected FiresOn(Grab) for GrabRename rule")
	}
	if r.FiresOn(WebhookEventEpisodeFileDelete) {
		t.Error("rule without FileDeleteClean must not fire on EpisodeFileDelete")
	}
	if r.FiresOn(WebhookEventMovieFileDelete) {
		t.Error("Sonarr rule must not fire on MovieFileDelete (Radarr-only event)")
	}
}

func TestWebhookRule_ConnectEventsNeeded_Dedup(t *testing.T) {
	// Three functions all dispatching on Download must collapse to one
	// Download entry — drives the wizard's "enable these triggers in
	// Sonarr/Radarr" summary list.
	r := WebhookRule{
		AppType:   "radarr",
		Functions: []WebhookFunction{WebhookFnTagAudio, WebhookFnTagVideo, WebhookFnRecover},
	}
	got := r.ConnectEventsNeeded()
	if len(got) != 1 || got[0] != WebhookEventDownload {
		t.Errorf("ConnectEventsNeeded = %v, want [Download]", got)
	}
}

func TestWebhookRule_ConnectEventsNeeded_MixedEvents(t *testing.T) {
	r := WebhookRule{
		AppType:   "radarr",
		Functions: []WebhookFunction{WebhookFnTagAudio, WebhookFnGrabRename, WebhookFnFileDeleteClean},
	}
	got := r.ConnectEventsNeeded()
	// FileDeleteClean adds 2 events (Delete + DeleteForUpgrade).
	want := map[WebhookConnectEvent]bool{
		WebhookEventDownload:                  true,
		WebhookEventGrab:                      true,
		WebhookEventMovieFileDelete:           true,
		WebhookEventMovieFileDeleteForUpgrade: true,
	}
	if len(got) != len(want) {
		t.Fatalf("ConnectEventsNeeded = %v, want %d distinct events", got, len(want))
	}
	for _, e := range got {
		if !want[e] {
			t.Errorf("unexpected event %s in result", e)
		}
	}
}

func TestNormalizeWebhookRule_Idempotent(t *testing.T) {
	r := WebhookRule{
		Name:    "  My Rule  ",
		AppType: " RADARR ",
		Functions: []WebhookFunction{
			"  tagAudio ", // whitespace-padded — must trim before dedup
			"tagAudio",    // duplicate after trim
			"",            // empty after trim — must drop
		},
	}
	NormalizeWebhookRule(&r)
	if r.Name != "My Rule" {
		t.Errorf("Name = %q, want %q", r.Name, "My Rule")
	}
	if r.AppType != "radarr" {
		t.Errorf("AppType = %q, want %q", r.AppType, "radarr")
	}
	if len(r.Functions) != 1 || r.Functions[0] != "tagAudio" {
		t.Errorf("Functions = %v, want [tagAudio]", r.Functions)
	}
	// Second pass must not change anything (idempotent contract).
	pre := r
	NormalizeWebhookRule(&r)
	if !reflect.DeepEqual(r, pre) {
		t.Errorf("NormalizeWebhookRule not idempotent: %+v != %+v", r, pre)
	}
}

func TestValidGrabRenameTarget(t *testing.T) {
	cases := map[string]bool{
		"":        true, // empty = adapter default ("torrent")
		"torrent": true,
		"file":    true,
		"both":    true,
		"unknown": false,
		"FILE":    false, // case-sensitive
		"display": false,
	}
	for in, want := range cases {
		if got := ValidGrabRenameTarget(in); got != want {
			t.Errorf("ValidGrabRenameTarget(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestMigrateLegacyTriggerFlags_AppendReleaseGroupDefault(t *testing.T) {
	// Legacy rule with no AppendReleaseGroup set (nil → defaulted true).
	// Migration should set TriggerOnMissingReleaseGroup=true.
	c := &GrabRenameCriteria{} // pristine — all flags false, no legacy fields
	c.MigrateLegacyTriggerFlags()
	if !c.TriggerOnMissingReleaseGroup {
		t.Error("expected TriggerOnMissingReleaseGroup=true (legacy AppendReleaseGroup defaulted true)")
	}
}

func TestMigrateLegacyTriggerFlags_AppendReleaseGroupExplicitFalse(t *testing.T) {
	fal := false
	c := &GrabRenameCriteria{AppendReleaseGroup: &fal}
	c.MigrateLegacyTriggerFlags()
	if c.TriggerOnMissingReleaseGroup {
		t.Error("expected TriggerOnMissingReleaseGroup=false (legacy AppendReleaseGroup=false)")
	}
}

func TestMigrateLegacyTriggerFlags_MovieVersionTokensActivate(t *testing.T) {
	c := &GrabRenameCriteria{MovieVersionTokens: []string{"Director's Cut", "IMAX"}}
	c.MigrateLegacyTriggerFlags()
	if !c.TriggerOnMovieVersionMismatch {
		t.Error("expected TriggerOnMovieVersionMismatch=true (legacy populated MovieVersionTokens)")
	}
}

func TestMigrateLegacyTriggerFlags_SourceTokensActivate(t *testing.T) {
	c := &GrabRenameCriteria{SourceTokens: []string{"MA", "Play"}}
	c.MigrateLegacyTriggerFlags()
	if !c.TriggerOnSourceMismatch {
		t.Error("expected TriggerOnSourceMismatch=true (legacy populated SourceTokens)")
	}
}

func TestMigrateLegacyTriggerFlags_ExcludeSceneReleasesActivates(t *testing.T) {
	// Locks the new branch added per Y7 review fix: legacy
	// ExcludeSceneReleases=true should migrate to TriggerOnSceneMismatch=true
	// (semantic shift — see helper's doc-comment) so user's intent to
	// "do something special with scene-related releases" is preserved.
	c := &GrabRenameCriteria{ExcludeSceneReleases: true}
	c.MigrateLegacyTriggerFlags()
	if !c.TriggerOnSceneMismatch {
		t.Error("expected TriggerOnSceneMismatch=true (legacy ExcludeSceneReleases=true)")
	}
}

func TestMigrateLegacyTriggerFlags_AlreadyMigratedNoOp(t *testing.T) {
	// Rule already has a new-style flag set. Migration should leave it alone.
	c := &GrabRenameCriteria{
		TriggerOnSceneMismatch: true, // any new-style flag triggers no-op
		MovieVersionTokens:     []string{"IMAX"},
	}
	c.MigrateLegacyTriggerFlags()
	if c.TriggerOnMissingReleaseGroup {
		t.Error("migration must not run when any TriggerOn* is already set")
	}
	if c.TriggerOnMovieVersionMismatch {
		t.Error("MovieVersionTokens shouldn't activate flag if rule is already migrated")
	}
}

func TestGrabRenameCriteria_AppendReleaseGroupOrDefault(t *testing.T) {
	// nil pointer → default true (matches the field doc-comment).
	var nilCriteria *GrabRenameCriteria
	if !nilCriteria.AppendReleaseGroupOrDefault() {
		t.Error("nil receiver must default to true")
	}
	c := &GrabRenameCriteria{}
	if !c.AppendReleaseGroupOrDefault() {
		t.Error("nil AppendReleaseGroup must default to true")
	}
	tru := true
	c.AppendReleaseGroup = &tru
	if !c.AppendReleaseGroupOrDefault() {
		t.Error("explicit true must return true")
	}
	fal := false
	c.AppendReleaseGroup = &fal
	if c.AppendReleaseGroupOrDefault() {
		t.Error("explicit false must return false")
	}
}

func TestWebhookRule_HasFunction(t *testing.T) {
	r := WebhookRule{Functions: []WebhookFunction{WebhookFnTagAudio, WebhookFnGrabRename}}
	if !r.HasFunction(WebhookFnTagAudio) {
		t.Error("expected HasFunction(tagAudio) = true")
	}
	if r.HasFunction(WebhookFnTagVideo) {
		t.Error("expected HasFunction(tagVideo) = false")
	}
}

func TestWebhookRule_FunctionsApplyToType(t *testing.T) {
	// Sonarr rule with a Radarr-only function must report it.
	r := WebhookRule{
		AppType:   "sonarr",
		Functions: []WebhookFunction{WebhookFnTagAudio, WebhookFnTagReleaseGroups},
	}
	if got := r.FunctionsApplyToType(); got != WebhookFnTagReleaseGroups {
		t.Errorf("FunctionsApplyToType = %s, want tagReleaseGroups", got)
	}
	// All functions match → empty.
	r2 := WebhookRule{
		AppType:   "radarr",
		Functions: []WebhookFunction{WebhookFnTagAudio, WebhookFnTagReleaseGroups},
	}
	if got := r2.FunctionsApplyToType(); got != "" {
		t.Errorf("FunctionsApplyToType (clean) = %q, want empty", got)
	}
}

func TestValidWebhookFunction(t *testing.T) {
	for _, fn := range allWebhookFunctions {
		if !ValidWebhookFunction(fn) {
			t.Errorf("ValidWebhookFunction(%s) = false, want true", fn)
		}
	}
	if ValidWebhookFunction("") {
		t.Error("empty must be invalid")
	}
	if ValidWebhookFunction("tagAudio ") {
		t.Error("trailing-space must be invalid (NormalizeWebhookRule trims first)")
	}
	if ValidWebhookFunction("tagaudio") {
		t.Error("lowercase variant must be invalid (constants are camelCase)")
	}
}
