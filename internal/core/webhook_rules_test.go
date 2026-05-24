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
	// Per-Arr-type scope:
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

func TestWebhookRule_FiresAutoStripOnDelete(t *testing.T) {
	// Radarr rule with Tag-RG fires the auto-strip on MovieFileDelete
	// and MovieFileDeleteForUpgrade — no user toggle required beyond
	// Tag-RG itself.
	r := WebhookRule{
		AppType:   "radarr",
		Functions: []WebhookFunction{WebhookFnTagReleaseGroups},
	}
	for _, ev := range []WebhookConnectEvent{
		WebhookEventMovieFileDelete,
		WebhookEventMovieFileDeleteForUpgrade,
	} {
		if !r.FiresAutoStripOnDelete(ev) {
			t.Errorf("FiresAutoStripOnDelete(%s) = false, want true", ev)
		}
	}
	// Non-delete event must not trigger auto-strip.
	if r.FiresAutoStripOnDelete(WebhookEventDownload) {
		t.Error("FiresAutoStripOnDelete(Download) = true, want false — only delete events drive auto-strip")
	}
	// Sonarr rule even with Tag-RG (validator would reject the combo,
	// but defence-in-depth) must not auto-strip.
	sonarr := WebhookRule{
		AppType:   "sonarr",
		Functions: []WebhookFunction{WebhookFnTagReleaseGroups},
	}
	if sonarr.FiresAutoStripOnDelete(WebhookEventMovieFileDelete) {
		t.Error("Sonarr rule must not fire auto-strip — Tag-RG is Radarr-only")
	}
	// Radarr rule without Tag-RG must not auto-strip.
	noTagRg := WebhookRule{
		AppType:   "radarr",
		Functions: []WebhookFunction{WebhookFnTagAudio},
	}
	if noTagRg.FiresAutoStripOnDelete(WebhookEventMovieFileDelete) {
		t.Error("rule without Tag-RG must not fire auto-strip")
	}
	// Nil-rule safe.
	var nilRule *WebhookRule
	if nilRule.FiresAutoStripOnDelete(WebhookEventMovieFileDelete) {
		t.Error("nil rule must return false")
	}
}

func TestWebhookRule_ConnectEventsNeeded_IncludesAutoStripDeleteEvents(t *testing.T) {
	// A Radarr Tag-RG rule that doesn't tick FileDeleteClean still needs
	// the user to enable Movie File Delete notifications in Radarr so
	// the auto-strip flow can fire. ConnectEventsNeeded must surface
	// those events even when no user-toggleable function dispatches on
	// them — otherwise the wizard's "enable these triggers" summary
	// would silently miss them.
	r := WebhookRule{
		AppType:   "radarr",
		Functions: []WebhookFunction{WebhookFnTagReleaseGroups},
	}
	got := r.ConnectEventsNeeded()
	want := map[WebhookConnectEvent]bool{
		WebhookEventDownload:                  true,
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

func TestWebhookRule_ConnectEventsNeeded_NoAutoStripForSonarrOrNonTagRg(t *testing.T) {
	// Sonarr rule with Tag-Audio — no Tag-RG, no auto-strip events.
	sonarr := WebhookRule{
		AppType:   "sonarr",
		Functions: []WebhookFunction{WebhookFnTagAudio},
	}
	got := sonarr.ConnectEventsNeeded()
	for _, e := range got {
		if e == WebhookEventMovieFileDelete || e == WebhookEventMovieFileDeleteForUpgrade {
			t.Errorf("Sonarr rule got Radarr-only auto-strip event %s", e)
		}
	}
	// Radarr rule WITHOUT Tag-RG must also skip the auto-strip events.
	radarrNoTagRg := WebhookRule{
		AppType:   "radarr",
		Functions: []WebhookFunction{WebhookFnTagAudio},
	}
	got = radarrNoTagRg.ConnectEventsNeeded()
	for _, e := range got {
		if e == WebhookEventMovieFileDelete || e == WebhookEventMovieFileDeleteForUpgrade {
			t.Errorf("rule without Tag-RG got auto-strip event %s", e)
		}
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

func TestWebhookRule_FiresPerBucketStripOnDelete_Positive(t *testing.T) {
	// Radarr rule with Audio snapshot opting into strip-on-delete fires
	// for both Movie-file-delete event variants. No legacy function in
	// Functions — gate must work purely on the bucket flag.
	r := WebhookRule{
		AppType:   "radarr",
		AudioTags: &AudioTagsConfig{StripOnFileDelete: true},
	}
	for _, ev := range []WebhookConnectEvent{
		WebhookEventMovieFileDelete,
		WebhookEventMovieFileDeleteForUpgrade,
	} {
		if !r.FiresPerBucketStripOnDelete(ev) {
			t.Errorf("FiresPerBucketStripOnDelete(%s) = false, want true (Audio bucket flagged)", ev)
		}
	}
}

func TestWebhookRule_FiresPerBucketStripOnDelete_VideoAndDvBuckets(t *testing.T) {
	// Video snapshot alone (no Audio) is enough to fire — covers
	// granular per-bucket opt-in.
	video := WebhookRule{
		AppType:   "radarr",
		VideoTags: &VideoTagsConfig{StripOnFileDelete: true},
	}
	if !video.FiresPerBucketStripOnDelete(WebhookEventMovieFileDelete) {
		t.Error("Video-only flagged → MovieFileDelete must fire")
	}
	// DV-detail snapshot alone — Radarr-only feature, fires on Radarr.
	dv := WebhookRule{
		AppType:  "radarr",
		DvDetail: &DvDetailConfig{StripOnFileDelete: true},
	}
	if !dv.FiresPerBucketStripOnDelete(WebhookEventMovieFileDeleteForUpgrade) {
		t.Error("DV-only flagged → MovieFileDeleteForUpgrade must fire")
	}
}

func TestWebhookRule_FiresPerBucketStripOnDelete_SonarrEpisodeEvents(t *testing.T) {
	// Sonarr rule fires on EpisodeFileDelete events, not Movie variants.
	r := WebhookRule{
		AppType:   "sonarr",
		AudioTags: &AudioTagsConfig{StripOnFileDelete: true},
	}
	for _, ev := range []WebhookConnectEvent{
		WebhookEventEpisodeFileDelete,
		WebhookEventEpisodeFileDeleteForUpgrade,
	} {
		if !r.FiresPerBucketStripOnDelete(ev) {
			t.Errorf("Sonarr FiresPerBucketStripOnDelete(%s) = false, want true", ev)
		}
	}
	// Wrong Arr's events must not fire.
	if r.FiresPerBucketStripOnDelete(WebhookEventMovieFileDelete) {
		t.Error("Sonarr rule must not fire on MovieFileDelete (Radarr-only event)")
	}
}

func TestWebhookRule_FiresPerBucketStripOnDelete_SonarrIgnoresDvBucket(t *testing.T) {
	// Sonarr rule with DV snapshot flagged — DV-detail is Radarr-only
	// (Sonarr mediaInfo doesn't expose DV fields). Gate must not fire
	// on DV alone for a Sonarr rule.
	r := WebhookRule{
		AppType:  "sonarr",
		DvDetail: &DvDetailConfig{StripOnFileDelete: true},
	}
	if r.FiresPerBucketStripOnDelete(WebhookEventEpisodeFileDelete) {
		t.Error("Sonarr rule must not fire on DV-only flag (DV is Radarr-only)")
	}
}

func TestWebhookRule_FiresPerBucketStripOnDelete_DefenceInDepth(t *testing.T) {
	// Wrong event type for any rule.
	r := WebhookRule{
		AppType:   "radarr",
		AudioTags: &AudioTagsConfig{StripOnFileDelete: true},
	}
	if r.FiresPerBucketStripOnDelete(WebhookEventDownload) {
		t.Error("non-delete event must not fire")
	}
	// Snapshot nil → gate does not peek into globals.
	noSnapshot := WebhookRule{AppType: "radarr"}
	if noSnapshot.FiresPerBucketStripOnDelete(WebhookEventMovieFileDelete) {
		t.Error("nil snapshots must not fire (gate is snapshot-only by design)")
	}
	// Snapshot present but flag false.
	flagOff := WebhookRule{
		AppType:   "radarr",
		AudioTags: &AudioTagsConfig{StripOnFileDelete: false},
	}
	if flagOff.FiresPerBucketStripOnDelete(WebhookEventMovieFileDelete) {
		t.Error("snapshot with StripOnFileDelete=false must not fire")
	}
	// Empty / unknown AppType.
	bogus := WebhookRule{
		AppType:   "",
		AudioTags: &AudioTagsConfig{StripOnFileDelete: true},
	}
	if bogus.FiresPerBucketStripOnDelete(WebhookEventMovieFileDelete) {
		t.Error("rule with empty AppType must not fire")
	}
	// Nil-rule safe.
	var nilRule *WebhookRule
	if nilRule.FiresPerBucketStripOnDelete(WebhookEventMovieFileDelete) {
		t.Error("nil rule must return false")
	}
}

func TestWebhookRule_ConnectEventsNeeded_SurfacesPerBucketStripDeleteEvents(t *testing.T) {
	// Rule with no functions but Audio bucket flagged — wizard's
	// "enable these triggers" summary must surface the file-delete
	// events so the user enables them in Radarr/Sonarr. Otherwise the
	// per-bucket strip silently never fires because Connect never
	// delivers the event.
	radarr := WebhookRule{
		AppType:   "radarr",
		AudioTags: &AudioTagsConfig{StripOnFileDelete: true},
	}
	got := radarr.ConnectEventsNeeded()
	wantSet := map[WebhookConnectEvent]bool{
		WebhookEventMovieFileDelete:           false,
		WebhookEventMovieFileDeleteForUpgrade: false,
	}
	for _, e := range got {
		if _, ok := wantSet[e]; ok {
			wantSet[e] = true
		}
	}
	for ev, found := range wantSet {
		if !found {
			t.Errorf("Radarr per-bucket-flagged rule: %s missing from ConnectEventsNeeded (got %v)", ev, got)
		}
	}

	// Sonarr equivalent: Episode-side events surface.
	sonarr := WebhookRule{
		AppType:   "sonarr",
		VideoTags: &VideoTagsConfig{StripOnFileDelete: true},
	}
	gotSon := sonarr.ConnectEventsNeeded()
	wantSon := map[WebhookConnectEvent]bool{
		WebhookEventEpisodeFileDelete:           false,
		WebhookEventEpisodeFileDeleteForUpgrade: false,
	}
	for _, e := range gotSon {
		if _, ok := wantSon[e]; ok {
			wantSon[e] = true
		}
	}
	for ev, found := range wantSon {
		if !found {
			t.Errorf("Sonarr per-bucket-flagged rule: %s missing from ConnectEventsNeeded (got %v)", ev, gotSon)
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

func TestMigrateLegacyQbitSeFlags_LegacyTagSeasonOnly(t *testing.T) {
	// Legacy rule with TagSeason=true, TagEpisode=false. Migration
	// should set SeasonEnabled=true, EpisodeEnabled=false, and turn
	// the new Unmatched bucket ON by default (preserves Python-script
	// "always have a fallback" posture). All three tag names get the
	// documented defaults backfilled.
	r := &QbitSeRules{TagSeason: true, TagEpisode: false}
	r.MigrateLegacyQbitSeFlags()
	if !r.SeasonEnabled {
		t.Error("expected SeasonEnabled=true (legacy TagSeason=true)")
	}
	if r.EpisodeEnabled {
		t.Error("expected EpisodeEnabled=false (legacy TagEpisode=false)")
	}
	if !r.UnmatchedEnabled {
		t.Error("expected UnmatchedEnabled=true (new default for migrated rules)")
	}
	if r.EpisodeTag != "Episode" || r.SeasonTag != "Season" || r.UnmatchedTag != "Unmatched" {
		t.Errorf("default tag names not backfilled: ep=%q se=%q un=%q",
			r.EpisodeTag, r.SeasonTag, r.UnmatchedTag)
	}
}

func TestMigrateLegacyQbitSeFlags_LegacyBothBoolsOn(t *testing.T) {
	r := &QbitSeRules{TagSeason: true, TagEpisode: true}
	r.MigrateLegacyQbitSeFlags()
	if !r.SeasonEnabled || !r.EpisodeEnabled || !r.UnmatchedEnabled {
		t.Errorf("expected all three Enabled flags=true after migration; got ep=%v se=%v un=%v",
			r.EpisodeEnabled, r.SeasonEnabled, r.UnmatchedEnabled)
	}
}

func TestMigrateLegacyQbitSeFlags_LegacyAllOff(t *testing.T) {
	// Pristine zero-value struct (no legacy bools, no new flags).
	// Detection treats this as "legacy" because all new fields are
	// blank — backfill turns Unmatched ON (so a config with an empty
	// QbitSe block ends up with the catch-all firing) plus default
	// tag names. EpisodeEnabled / SeasonEnabled stay false because
	// the legacy bools were false too.
	r := &QbitSeRules{}
	r.MigrateLegacyQbitSeFlags()
	if r.EpisodeEnabled {
		t.Error("expected EpisodeEnabled=false (no legacy TagEpisode)")
	}
	if r.SeasonEnabled {
		t.Error("expected SeasonEnabled=false (no legacy TagSeason)")
	}
	if !r.UnmatchedEnabled {
		t.Error("expected UnmatchedEnabled=true (new default for empty/legacy rules)")
	}
}

func TestMigrateLegacyQbitSeFlags_AlreadyMigratedNoOp(t *testing.T) {
	// Rule already on the new shape — at least one Enabled flag set.
	// Migration should NOT clobber the user's choice.
	r := &QbitSeRules{
		EpisodeEnabled: true, EpisodeTag: "ep",
		SeasonEnabled: false, SeasonTag: "",
		UnmatchedEnabled: false, UnmatchedTag: "",
		// Legacy bools left at zero — would falsely activate
		// migration if the detection were broken.
	}
	r.MigrateLegacyQbitSeFlags()
	if !r.EpisodeEnabled || r.EpisodeTag != "ep" {
		t.Errorf("user's EpisodeEnabled+EpisodeTag clobbered: %v / %q", r.EpisodeEnabled, r.EpisodeTag)
	}
	if r.SeasonEnabled {
		t.Error("user's SeasonEnabled=false flipped by migration")
	}
	if r.UnmatchedEnabled {
		t.Error("user's UnmatchedEnabled=false flipped by migration")
	}
	// Tag-name defaults DO get backfilled even on already-migrated
	// rules (cheap idempotent backfill so blanks always have a name).
	if r.SeasonTag != "Season" || r.UnmatchedTag != "Unmatched" {
		t.Errorf("blank tag names not backfilled: se=%q un=%q", r.SeasonTag, r.UnmatchedTag)
	}
}

func TestMigrateLegacyQbitSeFlags_Idempotent(t *testing.T) {
	// Run migration twice — second pass must be a no-op.
	r := &QbitSeRules{TagSeason: true, TagEpisode: false}
	r.MigrateLegacyQbitSeFlags()
	pre := *r
	r.MigrateLegacyQbitSeFlags()
	if !reflect.DeepEqual(*r, pre) {
		t.Errorf("MigrateLegacyQbitSeFlags not idempotent:\n  before second: %+v\n  after  second: %+v", pre, *r)
	}
}

func TestMigrateLegacyQbitSeFlags_NilReceiverNoPanic(t *testing.T) {
	var r *QbitSeRules
	r.MigrateLegacyQbitSeFlags() // must not panic
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
