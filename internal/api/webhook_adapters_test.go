package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"resolvarr/internal/arr"
	"resolvarr/internal/core"
	"resolvarr/internal/core/engine"
)

// webhook_adapters_test.go — unit coverage for the pure helpers in
// webhook_adapters.go. The full http-path adapter (dispatchTagAudio)
// requires an arr.Client mock; that lands during soak / integration
// once we have real Connect-event fixtures captured in
// dev/analysis/webhook-samples/. These tests lock the pieces that
// matter for correctness regardless of the http boundary:
//
//   - extractDownload: payload-shape mapping per Arr type
//   - pickAudioTagsConfig: per-rule-snapshot vs global fallback

func TestExtractMediaInfoForAudio_RadarrHappyPath(t *testing.T) {
	body := []byte(`{
		"isUpgrade": false,
		"movie":     {"id": 42, "title": "Dune", "year": 2021, "tmdbId": 438631},
		"movieFile": {
			"id": 100, "relativePath": "Dune (2021)/dune.mkv", "sceneName": "Dune.2021",
			"mediaInfo": {
				"audioCodec": "TrueHD", "audioChannels": 7.1,
				"audioAdditionalFeatures": "Atmos"
			}
		}
	}`)
	var p downloadEventPayload
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	ed := extractDownload("radarr", p); id, mi, ok := ed.ItemID, ed.MediaInfo, ed.OK
	if !ok {
		t.Fatal("expected ok=true")
	}
	if id != 42 {
		t.Errorf("itemID = %d, want 42 (radarr movie.id)", id)
	}
	if mi.AudioCodec != "TrueHD" || mi.AudioChannels != 7.1 || mi.AudioAdditionalFeatures != "Atmos" {
		t.Errorf("audio fields = %+v, want TrueHD/7.1/Atmos", mi)
	}
	if mi.RelativePath != "Dune (2021)/dune.mkv" {
		t.Errorf("RelativePath = %q, want carried through from MovieFile", mi.RelativePath)
	}
	if mi.SceneName != "Dune.2021" {
		t.Errorf("SceneName = %q, want carried through", mi.SceneName)
	}
}

func TestExtractMediaInfoForAudio_SonarrUsesSeriesIDNotEpisodeID(t *testing.T) {
	// Sonarr applies tags at the SERIES level (Library scan model);
	// episode metadata only determines what to tag. The adapter must
	// return series.id, NOT any episode.id, even though episodes[]
	// is populated.
	body := []byte(`{
		"series":   {"id": 7, "title": "Andor", "tvdbId": 393311},
		"episodes": [{"id": 999, "episodeNumber": 1, "seasonNumber": 1}],
		"episodeFile": {
			"id": 200, "relativePath": "Andor/S01E01.mkv",
			"mediaInfo": {"audioCodec": "EAC3", "audioChannels": 5.1}
		}
	}`)
	var p downloadEventPayload
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	ed := extractDownload("sonarr", p); id, mi, ok := ed.ItemID, ed.MediaInfo, ed.OK
	if !ok {
		t.Fatal("expected ok=true")
	}
	if id != 7 {
		t.Errorf("itemID = %d, want 7 (sonarr series.id, NOT episode.id 999)", id)
	}
	if mi.AudioCodec != "EAC3" {
		t.Errorf("AudioCodec = %q, want EAC3", mi.AudioCodec)
	}
}

func TestExtractMediaInfoForAudio_MissingFile(t *testing.T) {
	// Test event / older Arr version that doesn't emit mediaInfo on
	// the event — adapter must report skip cleanly, not crash.
	cases := []struct {
		name    string
		appType string
		body    string
	}{
		{"radarr no movie", "radarr", `{"movieFile": {"id": 1}}`},
		{"radarr no movieFile", "radarr", `{"movie": {"id": 42, "title": "X"}}`},
		{"sonarr no series", "sonarr", `{"episodeFile": {"id": 1}}`},
		{"sonarr no episodeFile", "sonarr", `{"series": {"id": 7, "title": "Y"}}`},
		{"unknown apptype", "unknown", `{"movie": {"id": 42}}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var p downloadEventPayload
			if err := json.Unmarshal([]byte(c.body), &p); err != nil {
				t.Fatalf("decode: %v", err)
			}
			ed := extractDownload(c.appType, p); ok := ed.OK
			if ok {
				t.Error("expected ok=false for missing file")
			}
		})
	}
}

func TestExtractMediaInfoForAudio_NilMediaInfoStillReturnsItem(t *testing.T) {
	// MovieFile present but mediaInfo missing — adapter returns ok=true
	// with a zero-valued MediaInfo. Engine.AudioTagsForFile then emits
	// an empty desired set (no audio fields → no tags). This is the
	// "Connect event arrived before mediaInfo populated" race.
	body := []byte(`{
		"movie":     {"id": 42, "title": "Dune"},
		"movieFile": {"id": 100, "relativePath": "x.mkv"}
	}`)
	var p downloadEventPayload
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	ed := extractDownload("radarr", p); id, mi, ok := ed.ItemID, ed.MediaInfo, ed.OK
	if !ok {
		t.Fatal("expected ok=true even with nil MediaInfo")
	}
	if id != 42 {
		t.Errorf("itemID = %d, want 42", id)
	}
	if mi.AudioCodec != "" || mi.AudioChannels != 0 {
		t.Errorf("expected zero-valued MediaInfo, got %+v", mi)
	}
	if mi.RelativePath != "x.mkv" {
		t.Errorf("RelativePath = %q, want x.mkv (carries through from MovieFile)", mi.RelativePath)
	}
	// Quick downstream check: engine returns empty for empty MediaInfo +
	// enabled bucket. Locks the contract that nil-mediaInfo == no-op,
	// not crash.
	emptyAudioCfg := engine.AudioTagsConfig{Audio: engine.BucketConfig{Enabled: true}}
	if got := engine.AudioTagsForFile(mi, emptyAudioCfg); len(got) != 0 {
		t.Errorf("engine returned %v for zero-mediaInfo, want empty", got)
	}
}

func TestPickAudioTagsConfig_RuleSnapshotWins(t *testing.T) {
	// Per-rule snapshot wins over global — locks the architectural
	// rule that schedules + webhook-rules read their own config at
	// fire-time, not the user's current global Library-scan settings.
	global := core.AudioTagsConfig{
		Audio: core.TagBucket{Enabled: true, Prefix: "global-"},
	}
	ruleSnap := &core.AudioTagsConfig{
		Audio: core.TagBucket{Enabled: true, Prefix: "rule-"},
	}
	rule := &core.WebhookRule{AudioTags: ruleSnap}
	got := pickAudioTagsConfig(rule, core.Config{AudioTags: global})
	if got.Audio.Prefix != "rule-" {
		t.Errorf("Prefix = %q, want rule- (snapshot must win over global)", got.Audio.Prefix)
	}
}

func TestPickAudioTagsConfig_GlobalFallback(t *testing.T) {
	// Pre-snapshot rule (AudioTags == nil) reads the global. Back-compat
	// path for rules saved before the snapshot field landed.
	global := core.AudioTagsConfig{
		Audio: core.TagBucket{Enabled: true, Prefix: "global-"},
	}
	rule := &core.WebhookRule{AudioTags: nil}
	got := pickAudioTagsConfig(rule, core.Config{AudioTags: global})
	if got.Audio.Prefix != "global-" {
		t.Errorf("Prefix = %q, want global- (nil snapshot must fall back)", got.Audio.Prefix)
	}
}

func TestDispatchDiscover_SkipPaths(t *testing.T) {
	// Pure skip-condition coverage. Each case asserts adapter returns
	// OK=true with a descriptive "skipped (...)" reason without
	// touching Arr or config. No real ConfigStore needed for skips
	// before the persist-layer call.
	dir := t.TempDir()
	store := core.NewConfigStore(dir)
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Seed cfg with an existing group so the "already known" branch
	// fires when we test it.
	if err := store.Update(func(c *core.Config) {
		c.ReleaseGroups = []core.ReleaseGroup{
			{ID: "g1", Search: "FLUX", Tag: "rg-flux", Type: "radarr", Enabled: true},
		}
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	s := &Server{App: &core.App{Config: store}}
	rule := &core.WebhookRule{ID: "r1", AppType: "radarr"}

	cases := []struct {
		name      string
		eventType string
		body      string
		want      string
	}{
		{"non-Download event", "Grab", `{}`, "skipped (not a Download event)"},
		{"no movieFile", "Download", `{"movie": {"id": 42}}`, "skipped (no movieFile on event payload)"},
		{"empty rg", "Download", `{"movie": {"id": 42}, "movieFile": {"id": 100, "releaseGroup": ""}}`, "skipped (no releaseGroup on file)"},
		{"Unknown rg", "Download", `{"movie": {"id": 42}, "movieFile": {"id": 100, "releaseGroup": "Unknown"}}`, "skipped (no releaseGroup on file)"},
		{"already known (case-insensitive)", "Download", `{"movie": {"id": 42}, "movieFile": {"id": 100, "releaseGroup": "flux"}}`, "skipped (group already known: FLUX)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := &connectEventEnvelope{EventType: c.eventType}
			res := s.dispatchDiscover(context.Background(), rule, store.Get(), env, []byte(c.body))
			if !res.OK {
				t.Fatalf("expected OK=true, got %+v", res)
			}
			if res.Summary != c.want {
				t.Errorf("Summary = %q, want %q", res.Summary, c.want)
			}
		})
	}
}

func TestDispatchDiscover_AddsNewGroup(t *testing.T) {
	dir := t.TempDir()
	store := core.NewConfigStore(dir)
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	s := &Server{App: &core.App{Config: store}}
	rule := &core.WebhookRule{
		ID:                 "r1",
		AppType:            "radarr",
		DiscoverAutoEnable: false, // commented entry (bash AUTO_TAG_DISCOVERED=false default)
		Filters:            &engine.FilterConfig{Quality: false, Audio: false}, // disable filters → always pass
	}
	body := []byte(`{
		"movie": {"id": 42, "title": "Dune"},
		"movieFile": {
			"id": 100,
			"relativePath": "Dune.2021.2160p.NEWGROUP.mkv",
			"releaseGroup": "NEWGROUP"
		}
	}`)
	env := &connectEventEnvelope{EventType: string(core.WebhookEventDownload)}
	res := s.dispatchDiscover(context.Background(), rule, store.Get(), env, body)
	if !res.OK {
		t.Fatalf("expected OK=true, got %+v", res)
	}
	if !strings.Contains(res.Summary, "discovered NEWGROUP") {
		t.Errorf("Summary = %q, want 'discovered NEWGROUP …'", res.Summary)
	}
	if !strings.Contains(res.Summary, "commented") {
		t.Errorf("Summary = %q, want 'commented' (DiscoverAutoEnable=false → bash AUTO_TAG_DISCOVERED=false → commented entry)", res.Summary)
	}
	// M-Webhook notifications contract: Changed=true + Detail
	// populated for the embed builder.
	if !res.Changed {
		t.Error("Changed = false, want true (discovery actually added a group)")
	}
	if d, ok := res.Detail.(DiscoverDetail); !ok {
		t.Errorf("Detail type = %T, want DiscoverDetail", res.Detail)
	} else {
		if d.NewGroup != "NEWGROUP" {
			t.Errorf("Detail.NewGroup = %q, want NEWGROUP", d.NewGroup)
		}
		if d.AutoEnabled {
			t.Error("Detail.AutoEnabled = true, want false (DiscoverAutoEnable=false on this rule)")
		}
	}
	cfgAfter := store.Get()
	if len(cfgAfter.ReleaseGroups) != 1 {
		t.Fatalf("ReleaseGroups len = %d, want 1", len(cfgAfter.ReleaseGroups))
	}
	g := cfgAfter.ReleaseGroups[0]
	if g.Search != "NEWGROUP" || g.Tag != "newgroup" || g.Type != "radarr" {
		t.Errorf("group = %+v, want Search=NEWGROUP Tag=newgroup Type=radarr", g)
	}
	if g.Enabled {
		t.Error("group must be Enabled=false (DiscoverAutoEnable=false)")
	}
	if g.Mode != "filtered" {
		t.Errorf("Mode = %q, want filtered", g.Mode)
	}
}

func TestDispatchDiscover_AutoEnableTrue(t *testing.T) {
	dir := t.TempDir()
	store := core.NewConfigStore(dir)
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	s := &Server{App: &core.App{Config: store}}
	rule := &core.WebhookRule{
		ID:                 "r1",
		AppType:            "radarr",
		DiscoverAutoEnable: true,
		Filters:            &engine.FilterConfig{Quality: false, Audio: false},
	}
	body := []byte(`{
		"movie": {"id": 42},
		"movieFile": {"id": 100, "releaseGroup": "AUTOGROUP"}
	}`)
	env := &connectEventEnvelope{EventType: string(core.WebhookEventDownload)}
	res := s.dispatchDiscover(context.Background(), rule, store.Get(), env, body)
	if !res.OK {
		t.Fatalf("expected OK=true, got %+v", res)
	}
	if !strings.Contains(res.Summary, "active") {
		t.Errorf("Summary = %q, want 'active' (DiscoverAutoEnable=true → bash AUTO_TAG_DISCOVERED=true)", res.Summary)
	}
	cfgAfter := store.Get()
	if len(cfgAfter.ReleaseGroups) != 1 || !cfgAfter.ReleaseGroups[0].Enabled {
		t.Errorf("expected one Enabled=true group, got %+v", cfgAfter.ReleaseGroups)
	}
}

func TestDispatchDiscover_DisabledKnownGroupBlocksReDiscovery(t *testing.T) {
	// "Disabled" in container = "commented" in bash. Both must suppress
	// re-discovery (user already knows about the group).
	dir := t.TempDir()
	store := core.NewConfigStore(dir)
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := store.Update(func(c *core.Config) {
		c.ReleaseGroups = []core.ReleaseGroup{
			{ID: "g1", Search: "DISABLED-GROUP", Tag: "rg-disabled", Type: "radarr", Enabled: false},
		}
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	s := &Server{App: &core.App{Config: store}}
	rule := &core.WebhookRule{ID: "r1", AppType: "radarr"}
	body := []byte(`{
		"movie": {"id": 42},
		"movieFile": {"id": 100, "releaseGroup": "DISABLED-GROUP"}
	}`)
	env := &connectEventEnvelope{EventType: string(core.WebhookEventDownload)}
	res := s.dispatchDiscover(context.Background(), rule, store.Get(), env, body)
	if !res.OK {
		t.Fatalf("expected OK=true, got %+v", res)
	}
	if !strings.Contains(res.Summary, "already known") {
		t.Errorf("Summary = %q, want 'already known' (disabled group still suppresses re-discovery)", res.Summary)
	}
	if len(store.Get().ReleaseGroups) != 1 {
		t.Error("must not append duplicate when known group exists (even disabled)")
	}
}

func TestDispatchDiscover_TagCollisionRejected(t *testing.T) {
	// Locks review concern #1: a manual group with Search != incoming
	// rg but Tag == lowercased(rg) must block discovery (Tag uniqueness
	// invariant). Library scan's applyDiscoverWriteBack (scan_discover
	// .go:222-223) does the same dual-check; webhook adapter must too.
	dir := t.TempDir()
	store := core.NewConfigStore(dir)
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := store.Update(func(c *core.Config) {
		// Manual group: Search="oldname" but Tag="newgroup".
		// Incoming Connect-event rg="NEWGROUP" → rgLower="newgroup".
		// Pure Search-dedup would miss the collision; Tag-dedup catches it.
		c.ReleaseGroups = []core.ReleaseGroup{
			{ID: "g1", Search: "oldname", Tag: "newgroup", Type: "radarr", Enabled: true},
		}
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	s := &Server{App: &core.App{Config: store}}
	rule := &core.WebhookRule{ID: "r1", AppType: "radarr"}
	body := []byte(`{
		"movie": {"id": 42},
		"movieFile": {"id": 100, "releaseGroup": "NEWGROUP"}
	}`)
	env := &connectEventEnvelope{EventType: string(core.WebhookEventDownload)}
	res := s.dispatchDiscover(context.Background(), rule, store.Get(), env, body)
	if !res.OK {
		t.Fatalf("expected OK=true (clean skip), got %+v", res)
	}
	if !strings.Contains(res.Summary, "already known") {
		t.Errorf("Summary = %q, want 'already known' (Tag-collision must block, not append duplicate)", res.Summary)
	}
	cfgAfter := store.Get()
	if len(cfgAfter.ReleaseGroups) != 1 {
		t.Errorf("ReleaseGroups len = %d, want 1 (no append on Tag-collision)", len(cfgAfter.ReleaseGroups))
	}
}

func TestDispatchDiscover_AutoEnableTriggersAutoApply(t *testing.T) {
	// Locks B3 from the bash-parity review: when DiscoverAutoEnable=true,
	// the discovered tag MUST be applied to the triggering movie in the
	// same fire — bash tagarr_import.sh:1169-1184 promises this and the
	// conf-sample explicitly states "the import script can tag immediately
	// because it handles a single movie at a time" (line 252-256).
	//
	// We verify the auto-apply branch is reached without ever calling
	// arr.Client by intentionally NOT seeding an instance: the auto-
	// apply branch's first step is findInstanceByID, which returns nil,
	// triggering the early-return "(auto-apply skipped: instance
	// vanished)" branch. That summary text proves we entered the
	// auto-apply path; without DiscoverAutoEnable=true we'd return
	// "added as commented" without the auto-apply phrase.
	dir := t.TempDir()
	store := core.NewConfigStore(dir)
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	s := &Server{App: &core.App{Config: store}}
	rule := &core.WebhookRule{
		ID:                 "r1",
		InstanceID:         "primary", // not seeded → triggers instance-vanished branch inside auto-apply
		AppType:            "radarr",
		DiscoverAutoEnable: true,
		Filters:            &engine.FilterConfig{Quality: false, Audio: false},
	}
	body := []byte(`{
		"movie": {"id": 42, "tmdbId": 999},
		"movieFile": {"id": 100, "releaseGroup": "AUTOAPPLY"}
	}`)
	env := &connectEventEnvelope{EventType: string(core.WebhookEventDownload)}
	res := s.dispatchDiscover(context.Background(), rule, store.Get(), env, body)
	if !res.OK {
		t.Fatalf("expected OK=true (clean instance-vanished skip inside auto-apply branch), got %+v", res)
	}
	if !strings.Contains(res.Summary, "auto-apply") {
		t.Errorf("Summary = %q, want 'auto-apply' phrase — proves DiscoverAutoEnable=true entered the auto-apply branch", res.Summary)
	}
}

func TestDispatchFileDeleteCleanup_ForUpgradeVariants(t *testing.T) {
	// Locks B2 from the bash-parity review: MovieFileDeleteForUpgrade +
	// EpisodeFileDeleteForUpgrade are distinct event types Radarr/Sonarr
	// emit during upgrade flows. Bash tagarr_import.sh:574 defends
	// against both; container must too — without it, file-delete-rules
	// don't fire on upgrade and stale managed tags survive.
	dir := t.TempDir()
	store := core.NewConfigStore(dir)
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	s := &Server{App: &core.App{Config: store}}
	body := []byte(`{"movie": {"id": 42}, "movieFile": {"id": 100}}`)
	bodySonarr := []byte(`{"series": {"id": 7}, "episodeFile": {"id": 200}}`)

	cases := []struct {
		name      string
		appType   string
		event     core.WebhookConnectEvent
		body      []byte
		mustEnter bool // true → adapter passed gate; false → clean skip
	}{
		{"radarr MovieFileDelete", "radarr", core.WebhookEventMovieFileDelete, body, true},
		{"radarr MovieFileDeleteForUpgrade", "radarr", core.WebhookEventMovieFileDeleteForUpgrade, body, true},
		{"radarr ignores Sonarr-typed events", "radarr", core.WebhookEventEpisodeFileDelete, body, false},
		{"sonarr EpisodeFileDelete", "sonarr", core.WebhookEventEpisodeFileDelete, bodySonarr, true},
		{"sonarr EpisodeFileDeleteForUpgrade", "sonarr", core.WebhookEventEpisodeFileDeleteForUpgrade, bodySonarr, true},
		{"sonarr ignores Radarr-typed events", "sonarr", core.WebhookEventMovieFileDelete, bodySonarr, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rule := &core.WebhookRule{ID: "r1", InstanceID: "primary", AppType: c.appType}
			env := &connectEventEnvelope{EventType: string(c.event)}
			res := s.dispatchFileDeleteCleanup(context.Background(), rule, store.Get(), nil, env, c.body)
			if c.mustEnter {
				// Past the event-type gate. Inner branches will fail (no
				// instance set up), but the gate-skip summary must NOT match.
				if strings.Contains(res.Summary, "skipped (not a") {
					t.Errorf("event %s/%s should have passed event-type gate; got skip summary %q", c.appType, c.event, res.Summary)
				}
			} else {
				if !res.OK || !strings.Contains(res.Summary, "skipped (not a") {
					t.Errorf("event %s/%s should have skipped at event-type gate; got %+v", c.appType, c.event, res)
				}
			}
		})
	}
}

func TestEventsForFunction_FileDeleteIncludesForUpgradeVariants(t *testing.T) {
	radarrEvents := core.EventsForFunction(core.WebhookFnFileDeleteClean, "radarr")
	if len(radarrEvents) != 2 {
		t.Fatalf("Radarr FileDeleteClean events = %v, want 2 (MovieFileDelete + MovieFileDeleteForUpgrade)", radarrEvents)
	}
	sonarrEvents := core.EventsForFunction(core.WebhookFnFileDeleteClean, "sonarr")
	if len(sonarrEvents) != 2 {
		t.Fatalf("Sonarr FileDeleteClean events = %v, want 2 (EpisodeFileDelete + EpisodeFileDeleteForUpgrade)", sonarrEvents)
	}
}

func TestDispatchSyncToSecondary_RequiresTagRG(t *testing.T) {
	// Locks review concern C1: rule with SyncToSecondary but WITHOUT
	// Tag-RG must skip cleanly. Without this gate the recompute-on-
	// secondary model would silently strip secondary's managed-RG
	// tags on every fire — bash-divergent foot-gun.
	dir := t.TempDir()
	store := core.NewConfigStore(dir)
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	s := &Server{App: &core.App{Config: store}}
	rule := &core.WebhookRule{
		ID:        "r1",
		AppType:   "radarr",
		Functions: []core.WebhookFunction{core.WebhookFnSyncToSecondary}, // Tag-RG NOT enabled
	}
	env := &connectEventEnvelope{EventType: string(core.WebhookEventDownload)}
	body := []byte(`{"movie": {"id": 42, "tmdbId": 438631}, "movieFile": {"id": 100}}`)
	res := s.dispatchSyncToSecondary(context.Background(), rule, store.Get(), env, body)
	if !res.OK {
		t.Fatalf("expected OK=true (clean skip), got %+v", res)
	}
	if !strings.Contains(res.Summary, "Tag Release Groups") {
		t.Errorf("Summary = %q, want hint pointing user to enable Tag-RG", res.Summary)
	}
}

func TestPickSyncTarget(t *testing.T) {
	cfg := core.Config{
		Instances: []core.Instance{
			{ID: "primary", Type: "radarr", Name: "Primary"},
			{ID: "secondary", Type: "radarr", Name: "Secondary 4K"},
			{ID: "tertiary", Type: "radarr", Name: "Tertiary"},
			{ID: "sonarr1", Type: "sonarr", Name: "Sonarr"},
		},
	}
	t.Run("explicit SyncToInstanceID wins", func(t *testing.T) {
		rule := &core.WebhookRule{InstanceID: "primary", AppType: "radarr", SyncToInstanceID: "tertiary"}
		got := pickSyncTarget(rule, cfg)
		if got == nil || got.ID != "tertiary" {
			t.Errorf("got %+v, want tertiary", got)
		}
	})
	t.Run("explicit non-existent target returns nil", func(t *testing.T) {
		rule := &core.WebhookRule{InstanceID: "primary", AppType: "radarr", SyncToInstanceID: "ghost"}
		if got := pickSyncTarget(rule, cfg); got != nil {
			t.Errorf("got %+v, want nil (ghost id doesn't exist)", got)
		}
	})
	t.Run("empty SyncToInstanceID picks first other of matching type", func(t *testing.T) {
		rule := &core.WebhookRule{InstanceID: "primary", AppType: "radarr", SyncToInstanceID: ""}
		got := pickSyncTarget(rule, cfg)
		if got == nil || got.ID != "secondary" {
			t.Errorf("got %+v, want secondary (first non-primary radarr)", got)
		}
	})
	t.Run("empty SyncToInstanceID skips wrong AppType + primary itself", func(t *testing.T) {
		// Primary IS sonarr1; rule is sonarr; only sonarr1 exists; no other
		// sonarr → nil
		rule := &core.WebhookRule{InstanceID: "sonarr1", AppType: "sonarr", SyncToInstanceID: ""}
		if got := pickSyncTarget(rule, cfg); got != nil {
			t.Errorf("got %+v, want nil (no other sonarr instance)", got)
		}
	})
	t.Run("empty + only primary exists returns nil", func(t *testing.T) {
		smallCfg := core.Config{
			Instances: []core.Instance{{ID: "only", Type: "radarr", Name: "Only"}},
		}
		rule := &core.WebhookRule{InstanceID: "only", AppType: "radarr"}
		if got := pickSyncTarget(rule, smallCfg); got != nil {
			t.Errorf("got %+v, want nil (only one radarr instance)", got)
		}
	})
}

func TestExtractDownload_TmdbIDCarriesThrough(t *testing.T) {
	body := []byte(`{
		"movie":     {"id": 42, "tmdbId": 438631},
		"movieFile": {"id": 100}
	}`)
	var p downloadEventPayload
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	ed := extractDownload("radarr", p)
	if ed.TmdbID != 438631 {
		t.Errorf("TmdbID = %d, want 438631", ed.TmdbID)
	}
}

func TestNeedsRecovery(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", true},
		{"  ", true},
		{"Unknown", true},
		{"unknown", true},
		{"UNKNOWN", true},
		{"null", true},
		{"NULL", true},
		{"FLUX", false},
		{"NTb", false},
		{"SiC", false}, // false-positive guard: SiC contains 'c' but isn't unknown
	}
	for _, c := range cases {
		if got := needsRecovery(c.in); got != c.want {
			t.Errorf("needsRecovery(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestFindRecoveryGroupByDownloadID(t *testing.T) {
	mkRec := func(eventType, downloadID, rgLower, rgTitle string, dateOffset int) arr.HistoryRecord {
		r := arr.HistoryRecord{
			EventType:  eventType,
			DownloadID: downloadID,
			Date:       time.Date(2024, 1, 1, 0, dateOffset, 0, 0, time.UTC),
		}
		r.Data.ReleaseGroupLower = rgLower
		r.Data.ReleaseGroupTitle = rgTitle
		return r
	}
	t.Run("happy path — matches grabbed event", func(t *testing.T) {
		history := []arr.HistoryRecord{
			mkRec("grabbed", "ABCDEF", "FLUX", "", 0),
			mkRec("downloadFolderImported", "ABCDEF", "", "", 1),
		}
		rg, ok := findRecoveryGroupByDownloadID(history, "ABCDEF")
		if !ok || rg != "FLUX" {
			t.Errorf("got (%q, %v), want (FLUX, true)", rg, ok)
		}
	})
	t.Run("case-insensitive downloadId match", func(t *testing.T) {
		// qBit + Arr disagree on hash casing — locks the EqualFold path.
		history := []arr.HistoryRecord{mkRec("grabbed", "ABCdef0123", "NTb", "", 0)}
		rg, ok := findRecoveryGroupByDownloadID(history, "abcDEF0123")
		if !ok || rg != "NTb" {
			t.Errorf("got (%q, %v), want (NTb, true)", rg, ok)
		}
	})
	t.Run("no matching grab event → false", func(t *testing.T) {
		history := []arr.HistoryRecord{
			mkRec("grabbed", "OTHER", "FLUX", "", 0),
			mkRec("downloadFolderImported", "ABCDEF", "", "", 1),
		}
		_, ok := findRecoveryGroupByDownloadID(history, "ABCDEF")
		if ok {
			t.Error("expected ok=false (no grabbed event with matching downloadId)")
		}
	})
	t.Run("matched grab event with empty rg → ('', true)", func(t *testing.T) {
		// Both Arr's pre-parsed rg AND sourceTitle are empty → tolerant
		// fallback has nothing to extract → returns ("", true).
		history := []arr.HistoryRecord{mkRec("grabbed", "ABCDEF", "", "", 0)}
		rg, ok := findRecoveryGroupByDownloadID(history, "ABCDEF")
		if !ok || rg != "" {
			t.Errorf("got (%q, %v), want ('', true) — matched but empty rg", rg, ok)
		}
	})
	t.Run("Rango fallback — Arr's rg empty, sourceTitle salvages it", func(t *testing.T) {
		// Locks the Rango/Matilda fix. Radarr's grab-time parser bombed
		// on " - SumVision" multi-token reject → data.releaseGroup empty.
		// Tolerant fallback parses sourceTitle directly → extracts
		// "SumVision" via " - <RG>" pattern handler.
		rec := mkRec("grabbed", "ABCDEF", "", "", 0)
		rec.SourceTitle = "Rango 2011 Hybrid Theatrical 2160p WEB-DL HEVC DTS-HD MA 5.1 DoVi - SumVision"
		history := []arr.HistoryRecord{rec}
		rg, ok := findRecoveryGroupByDownloadID(history, "ABCDEF")
		if !ok || rg != "SumVision" {
			t.Errorf("got (%q, %v), want (SumVision, true) — tolerant fallback should salvage from sourceTitle", rg, ok)
		}
	})
	t.Run("Matilda fallback — same pattern, different title", func(t *testing.T) {
		rec := mkRec("grabbed", "ABCDEF", "", "", 0)
		rec.SourceTitle = "Roald Dahls Matilda the Musical 2022 Hybrid 2160p WEB-DL HEVC TrueHD Atmos 7.1 DoVi - SumVision"
		history := []arr.HistoryRecord{rec}
		rg, ok := findRecoveryGroupByDownloadID(history, "ABCDEF")
		if !ok || rg != "SumVision" {
			t.Errorf("got (%q, %v), want (SumVision, true)", rg, ok)
		}
	})
	t.Run("Arr's rg present takes priority over tolerant fallback", func(t *testing.T) {
		// When data.releaseGroup is populated, use it directly without
		// re-parsing sourceTitle (cheaper + Arr's parser is the
		// authoritative source when it succeeded).
		rec := mkRec("grabbed", "ABCDEF", "FLUX", "", 0)
		rec.SourceTitle = "Movie 2024 ... DoVi - SumVision" // would extract SumVision if we parsed
		history := []arr.HistoryRecord{rec}
		rg, ok := findRecoveryGroupByDownloadID(history, "ABCDEF")
		if !ok || rg != "FLUX" {
			t.Errorf("got (%q, %v), want (FLUX, true) — primary path takes priority", rg, ok)
		}
	})
	t.Run("ReleaseGroup() coalesces lowercase + Title casings", func(t *testing.T) {
		// Bash equivalent: `(.data.releaseGroup // .data.ReleaseGroup // "")`.
		// arr.HistoryRecord.ReleaseGroup() prefers lowercase; falls back
		// to Title when missing. Lock the contract here too.
		history := []arr.HistoryRecord{mkRec("grabbed", "ABCDEF", "", "FLUX", 0)}
		rg, ok := findRecoveryGroupByDownloadID(history, "ABCDEF")
		if !ok || rg != "FLUX" {
			t.Errorf("got (%q, %v), want (FLUX, true) — Title fallback", rg, ok)
		}
	})
	t.Run("multiple grabbed events with same downloadId — picks newest by date", func(t *testing.T) {
		history := []arr.HistoryRecord{
			mkRec("grabbed", "ABCDEF", "OLD", "", 0),
			mkRec("grabbed", "ABCDEF", "NEW", "", 5),
			mkRec("grabbed", "ABCDEF", "MID", "", 2),
		}
		rg, ok := findRecoveryGroupByDownloadID(history, "ABCDEF")
		if !ok || rg != "NEW" {
			t.Errorf("got (%q, %v), want (NEW, true) — most recent grab wins", rg, ok)
		}
	})
	t.Run("empty downloadId returns false without iterating", func(t *testing.T) {
		_, ok := findRecoveryGroupByDownloadID([]arr.HistoryRecord{}, "")
		if ok {
			t.Error("empty downloadId must return false")
		}
	})
}

func TestExtractDownload_DownloadIDCarriesThrough(t *testing.T) {
	body := []byte(`{
		"downloadId": "ABCDEF0123",
		"movie":      {"id": 42},
		"movieFile":  {"id": 100, "relativePath": "x.mkv"}
	}`)
	var p downloadEventPayload
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	ed := extractDownload("radarr", p)
	if ed.DownloadID != "ABCDEF0123" {
		t.Errorf("DownloadID = %q, want ABCDEF0123", ed.DownloadID)
	}
}

func TestResolveRuleReleaseGroups(t *testing.T) {
	cfg := core.Config{
		ReleaseGroups: []core.ReleaseGroup{
			{ID: "g1", Search: "FLUX", Tag: "rg-flux", Type: "radarr", Enabled: true},
			{ID: "g2", Search: "NTb", Tag: "rg-ntb", Type: "radarr", Enabled: true},
			{ID: "g3", Search: "XEBEC", Tag: "rg-xebec", Type: "radarr", Enabled: false}, // disabled
			{ID: "g4", Search: "NORDIC", Tag: "rg-nordic", Type: "sonarr", Enabled: true},
		},
	}
	t.Run("nil ReleaseGroupIDs returns all enabled of matching type", func(t *testing.T) {
		rule := &core.WebhookRule{AppType: "radarr", ReleaseGroupIDs: nil}
		got := resolveRuleReleaseGroups(rule, cfg)
		if len(got) != 2 {
			t.Fatalf("got %d groups, want 2 (g1+g2; g3 disabled, g4 sonarr)", len(got))
		}
		ids := []string{got[0].ID, got[1].ID}
		if ids[0] != "g1" || ids[1] != "g2" {
			t.Errorf("got IDs %v, want [g1 g2]", ids)
		}
	})
	t.Run("populated subset narrows + still filters disabled", func(t *testing.T) {
		// User picked g1 + g3 (g3 is disabled → must be excluded).
		rule := &core.WebhookRule{AppType: "radarr", ReleaseGroupIDs: []string{"g1", "g3"}}
		got := resolveRuleReleaseGroups(rule, cfg)
		if len(got) != 1 || got[0].ID != "g1" {
			t.Errorf("got %+v, want [g1] only (g3 disabled)", got)
		}
	})
	t.Run("empty subset returns no groups", func(t *testing.T) {
		rule := &core.WebhookRule{AppType: "radarr", ReleaseGroupIDs: []string{}}
		got := resolveRuleReleaseGroups(rule, cfg)
		if len(got) != 0 {
			t.Errorf("got %d groups, want 0 (explicitly empty subset)", len(got))
		}
	})
	t.Run("empty Tag or Search excludes group (defence-in-depth)", func(t *testing.T) {
		cfgBad := core.Config{
			ReleaseGroups: []core.ReleaseGroup{
				{ID: "ok", Search: "FLUX", Tag: "rg-flux", Type: "radarr", Enabled: true},
				{ID: "no-tag", Search: "NTb", Tag: "", Type: "radarr", Enabled: true},
				{ID: "no-search", Search: "", Tag: "rg-bad", Type: "radarr", Enabled: true},
			},
		}
		rule := &core.WebhookRule{AppType: "radarr", ReleaseGroupIDs: nil}
		got := resolveRuleReleaseGroups(rule, cfgBad)
		if len(got) != 1 || got[0].ID != "ok" {
			t.Errorf("got %+v, want [ok] (no-tag + no-search must be excluded)", got)
		}
	})
	t.Run("Sonarr rule uses Sonarr-typed groups only", func(t *testing.T) {
		rule := &core.WebhookRule{AppType: "sonarr", ReleaseGroupIDs: nil}
		got := resolveRuleReleaseGroups(rule, cfg)
		if len(got) != 1 || got[0].ID != "g4" {
			t.Errorf("got %+v, want [g4] only", got)
		}
	})
}

func TestPickFiltersConfig_RuleSnapshotWins(t *testing.T) {
	// rule.Filters wins over cfg.Filters.{Radarr|Sonarr}. Use a
	// distinguishing flag (DTSX) so we can tell which path returned.
	radarrGlobal := engine.FilterConfig{Quality: true, DTSX: false}
	ruleSnap := &engine.FilterConfig{Quality: true, DTSX: true}
	rule := &core.WebhookRule{AppType: "radarr", Filters: ruleSnap}
	cfg := core.Config{Filters: core.FilterSet{Radarr: radarrGlobal}}
	got := pickFiltersConfig(rule, cfg)
	if !got.DTSX {
		t.Errorf("got DTSX=%v, want true (rule snapshot wins over global false)", got.DTSX)
	}
}

func TestPickFiltersConfig_GlobalFallback(t *testing.T) {
	radarrGlobal := engine.FilterConfig{Quality: true, MAWebDL: true}
	sonarrGlobal := engine.FilterConfig{Quality: true, PlayWebDL: true}
	cfg := core.Config{Filters: core.FilterSet{Radarr: radarrGlobal, Sonarr: sonarrGlobal}}
	t.Run("radarr → uses Filters.Radarr", func(t *testing.T) {
		rule := &core.WebhookRule{AppType: "radarr", Filters: nil}
		got := pickFiltersConfig(rule, cfg)
		if !got.MAWebDL || got.PlayWebDL {
			t.Errorf("got MAWebDL=%v PlayWebDL=%v, want true/false (Radarr config)", got.MAWebDL, got.PlayWebDL)
		}
	})
	t.Run("sonarr → uses Filters.Sonarr", func(t *testing.T) {
		rule := &core.WebhookRule{AppType: "sonarr", Filters: nil}
		got := pickFiltersConfig(rule, cfg)
		if got.MAWebDL || !got.PlayWebDL {
			t.Errorf("got MAWebDL=%v PlayWebDL=%v, want false/true (Sonarr config)", got.MAWebDL, got.PlayWebDL)
		}
	})
}

func TestPickItemIDForDelete(t *testing.T) {
	cases := []struct {
		name    string
		appType string
		body    string
		want    int
	}{
		{"radarr movie present", "radarr", `{"movie": {"id": 42}}`, 42},
		{"radarr movie absent", "radarr", `{"movieFile": {"id": 100}}`, 0},
		{"sonarr series present", "sonarr", `{"series": {"id": 7}, "episodes": [{"id": 999}]}`, 7},
		{"sonarr ignores episodes", "sonarr", `{"series": {"id": 7}, "episodes": [{"id": 999}]}`, 7},
		{"sonarr series absent", "sonarr", `{"episodeFile": {"id": 200}}`, 0},
		{"unknown apptype", "unknown", `{"movie": {"id": 42}}`, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var p downloadEventPayload
			if err := json.Unmarshal([]byte(c.body), &p); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got := pickItemIDForDelete(c.appType, p); got != c.want {
				t.Errorf("got %d, want %d", got, c.want)
			}
		})
	}
}

func TestDispatchFileDeleteCleanup_EventTypeGate(t *testing.T) {
	// Defence-in-depth: dispatcher's FiresOn already gates rules by
	// event-type, but adapter's own gate must not fire on the wrong
	// event-type either. A Radarr rule must reject EpisodeFileDelete
	// (and vice versa) without contacting Arr.
	s := &Server{} // no Config / arr.Client needed — we never reach them
	rule := &core.WebhookRule{AppType: "radarr"}
	env := &connectEventEnvelope{EventType: string(core.WebhookEventEpisodeFileDelete)}
	res := s.dispatchFileDeleteCleanup(context.Background(), rule, core.Config{}, nil, env, []byte(`{}`))
	if !res.OK {
		t.Errorf("expected OK=true (skip), got %+v", res)
	}
	if !strings.Contains(res.Summary, "skipped") {
		t.Errorf("expected skipped-summary, got %q", res.Summary)
	}
}

func TestBuildFileDeleteManagedSet_IgnoresRemoveOrphanedTags(t *testing.T) {
	// File Delete uses AllPossible*Tags regardless of the user's
	// RemoveOrphanedTags toggle — locks design decision against a
	// future refactor that "respects" the flag and breaks cleanup.
	cfg := core.Config{
		AudioTags: core.AudioTagsConfig{
			Audio:              core.TagBucket{Enabled: true, Prefix: "audio-"},
			RemoveOrphanedTags: false, // user opted out of orphan removal in Library scan
			StripOnFileDelete:  true,  // opt-in for file-delete strip
		},
	}
	rule := &core.WebhookRule{AppType: "radarr"}
	managed := buildFileDeleteManagedSet(rule, cfg)
	if _, ok := managed["audio-truehd"]; !ok {
		t.Error("RemoveOrphanedTags=false on the user's config must NOT shrink File Delete's managed set when StripOnFileDelete=true — the file is gone, all derived tags follow")
	}
}

func TestBuildFileDeleteManagedSet_UnionAcrossBuckets(t *testing.T) {
	// Rule with snapshots covering Audio + Video + DV, each with
	// StripOnFileDelete=true — File Delete must strip tags from ALL
	// three buckets in one pass.
	cfg := core.Config{
		AudioTags: core.AudioTagsConfig{
			Audio:             core.TagBucket{Enabled: true, Prefix: "audio-"},
			StripOnFileDelete: true,
		},
		VideoTags: core.VideoTagsConfig{
			Resolution:        core.TagBucket{Enabled: true, Prefix: "res-"},
			Codec:             core.TagBucket{Enabled: true, Prefix: "codec-"},
			HDR:               core.TagBucket{Enabled: true, Prefix: "hdr-"},
			StripOnFileDelete: true,
		},
		DvDetail: core.DvDetailConfig{
			Enabled:           true,
			Prefix:            "dv-",
			StripOnFileDelete: true,
		},
	}
	rule := &core.WebhookRule{AppType: "radarr"}
	managed := buildFileDeleteManagedSet(rule, cfg)
	// Spot-check that representative tags from each bucket are present.
	checks := map[string]string{
		"audio-truehd":  "audio bucket",
		"res-2160p":     "video resolution bucket",
		"codec-h265":    "video codec bucket",
		"hdr-hdr10":     "video HDR bucket",
		"dv-dvprofile8": "DV bucket",
	}
	for tag, label := range checks {
		if _, ok := managed[tag]; !ok {
			t.Errorf("managed set missing %q (%s) — got: %v", tag, label, managed)
		}
	}
}

func TestBuildFileDeleteManagedSet_SonarrSkipsDv(t *testing.T) {
	// Sonarr rule must NOT include DV tags (mediaInfo lacks DV fields,
	// validator already gates this — defence in depth in the cleanup set).
	cfg := core.Config{
		AudioTags: core.AudioTagsConfig{
			Audio:             core.TagBucket{Enabled: true, Prefix: "audio-"},
			StripOnFileDelete: true,
		},
		DvDetail: core.DvDetailConfig{
			Enabled:           true,
			Prefix:            "dv-",
			StripOnFileDelete: true, // opted in but Sonarr rule must still skip
		},
	}
	rule := &core.WebhookRule{AppType: "sonarr"}
	managed := buildFileDeleteManagedSet(rule, cfg)
	if _, ok := managed["dv-dvprofile8"]; ok {
		t.Error("Sonarr rule must not include DV-detail tags in managed set even with StripOnFileDelete=true — mediaInfo doesn't expose DV fields")
	}
	if _, ok := managed["audio-truehd"]; !ok {
		t.Error("Sonarr rule should still include Audio bucket when opted in")
	}
}

func TestBuildFileDeleteManagedSet_PerBucketOptInRequired(t *testing.T) {
	// StripOnFileDelete defaults to false. A bucket with the flag
	// unset MUST NOT be in the managed set even when enabled +
	// populated — opt-in semantics.
	cfg := core.Config{
		AudioTags: core.AudioTagsConfig{
			Audio: core.TagBucket{Enabled: true, Prefix: "audio-"},
			// StripOnFileDelete not set — defaults false
		},
		VideoTags: core.VideoTagsConfig{
			Resolution:        core.TagBucket{Enabled: true, Prefix: "res-"},
			StripOnFileDelete: true, // only video opted in
		},
		DvDetail: core.DvDetailConfig{
			Enabled: true,
			Prefix:  "dv-",
			// StripOnFileDelete not set — defaults false
		},
	}
	rule := &core.WebhookRule{AppType: "radarr"}
	managed := buildFileDeleteManagedSet(rule, cfg)
	if _, ok := managed["audio-truehd"]; ok {
		t.Error("audio bucket missing StripOnFileDelete=true — must not appear in managed set")
	}
	if _, ok := managed["res-2160p"]; !ok {
		t.Error("video bucket has StripOnFileDelete=true — must appear in managed set")
	}
	if _, ok := managed["dv-dvprofile8"]; ok {
		t.Error("DV bucket missing StripOnFileDelete=true — must not appear in managed set")
	}
}

// The C2 legacy bridge (rule with WebhookFnFileDeleteClean → all three
// buckets stripped regardless of per-bucket flags) was retired in C8
// of the M-webhook delete-semantics refactor. The C5 migration
// converts every pre-existing rule on first Load, so no rule reaches
// this dispatcher with the legacy function in Functions[]. The bridge
// test that lived here was removed alongside the bridge itself.

func TestBuildFileDeleteManagedSet_NoOptIn(t *testing.T) {
	// Without per-bucket opt-in, the managed set is empty — no surprise
	// stripping. Replaces the old "NoLegacyBridgeWithoutFunction" test.
	cfg := core.Config{
		AudioTags: core.AudioTagsConfig{Audio: core.TagBucket{Enabled: true, Prefix: "audio-"}},
		VideoTags: core.VideoTagsConfig{Resolution: core.TagBucket{Enabled: true, Prefix: "res-"}},
		DvDetail:  core.DvDetailConfig{Enabled: true, Prefix: "dv-"},
	}
	rule := &core.WebhookRule{AppType: "radarr"} // no functions, no opt-in
	managed := buildFileDeleteManagedSet(rule, cfg)
	if len(managed) != 0 {
		t.Errorf("expected empty managed set, got %d entries: %v", len(managed), managed)
	}
}

func TestExtractDownload_HasMediaInfoFlag(t *testing.T) {
	// Locks B2 from the Tag DV Details review: HasMediaInfo
	// distinguishes "Arr definitely probed this file" from "Arr emitted
	// the file row before mediaInfo was populated". Tag DV Details
	// MUST NOT emit no-dv when HasMediaInfo=false — would strip real
	// DV tags from a freshly imported DV file mid-probe.
	t.Run("populated mediaInfo → HasMediaInfo true", func(t *testing.T) {
		body := []byte(`{
			"movie":     {"id": 42, "title": "Dune"},
			"movieFile": {
				"id": 100,
				"mediaInfo": {"audioCodec": "EAC3"}
			}
		}`)
		var p downloadEventPayload
		if err := json.Unmarshal(body, &p); err != nil {
			t.Fatalf("decode: %v", err)
		}
		ed := extractDownload("radarr", p)
		if !ed.HasMediaInfo {
			t.Error("expected HasMediaInfo=true when mediaInfo present")
		}
	})
	t.Run("missing mediaInfo → HasMediaInfo false", func(t *testing.T) {
		body := []byte(`{
			"movie":     {"id": 42, "title": "Dune"},
			"movieFile": {"id": 100, "relativePath": "x.mkv"}
		}`)
		var p downloadEventPayload
		if err := json.Unmarshal(body, &p); err != nil {
			t.Fatalf("decode: %v", err)
		}
		ed := extractDownload("radarr", p)
		if !ed.OK {
			t.Fatal("expected ok=true (file exists, mediaInfo just empty)")
		}
		if ed.HasMediaInfo {
			t.Error("expected HasMediaInfo=false when mediaInfo absent")
		}
	})
	t.Run("populated mediaInfo with zero-valued fields → HasMediaInfo true", func(t *testing.T) {
		// Edge: Arr probed the file, all fields came back empty (rare —
		// truly broken file). HasMediaInfo must still be true so the
		// adapter knows the values are authoritative, not pre-probe.
		body := []byte(`{
			"movie":     {"id": 42, "title": "Dune"},
			"movieFile": {"id": 100, "mediaInfo": {}}
		}`)
		var p downloadEventPayload
		if err := json.Unmarshal(body, &p); err != nil {
			t.Fatalf("decode: %v", err)
		}
		ed := extractDownload("radarr", p)
		if !ed.HasMediaInfo {
			t.Error("expected HasMediaInfo=true when mediaInfo present even if fields are zero")
		}
	})
}

func TestPickDvDetailConfig_RuleSnapshotWins(t *testing.T) {
	global := core.DvDetailConfig{Enabled: true, Prefix: "global-dv-"}
	ruleSnap := &core.DvDetailConfig{Enabled: true, Prefix: "rule-dv-"}
	rule := &core.WebhookRule{DvDetail: ruleSnap}
	got := pickDvDetailConfig(rule, core.Config{DvDetail: global})
	if got.Prefix != "rule-dv-" {
		t.Errorf("Prefix = %q, want rule-dv-", got.Prefix)
	}
}

func TestPickDvDetailConfig_GlobalFallback(t *testing.T) {
	global := core.DvDetailConfig{Enabled: true, Prefix: "global-dv-"}
	rule := &core.WebhookRule{DvDetail: nil}
	got := pickDvDetailConfig(rule, core.Config{DvDetail: global})
	if got.Prefix != "global-dv-" {
		t.Errorf("Prefix = %q, want global-dv- (nil snapshot must fall back)", got.Prefix)
	}
}

// TestExtractDownload_FilePathAndFileID locks the file-aware fields on
// the extract struct — Tag DV Details needs FilePath for dovi_tool;
// Recover needs FileID for the moviefile PUT. Both are read directly
// from arr.MovieFile / Sonarr's episodeFile (same shape).
func TestExtractDownload_FilePathAndFileID(t *testing.T) {
	body := []byte(`{
		"movie":     {"id": 42, "title": "Dune"},
		"movieFile": {
			"id": 100, "relativePath": "Dune (2021)/dune.mkv",
			"path": "/movies/Dune (2021)/dune.mkv",
			"releaseGroup": "FLUX",
			"mediaInfo": {"videoDynamicRangeType": "DV HDR10"}
		}
	}`)
	var p downloadEventPayload
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	ed := extractDownload("radarr", p)
	if !ed.OK {
		t.Fatal("expected ok=true")
	}
	if ed.FileID != 100 {
		t.Errorf("FileID = %d, want 100", ed.FileID)
	}
	if ed.FilePath != "/movies/Dune (2021)/dune.mkv" {
		t.Errorf("FilePath = %q, want carried through from movieFile.path", ed.FilePath)
	}
	if ed.ReleaseGroup != "FLUX" {
		t.Errorf("ReleaseGroup = %q, want FLUX", ed.ReleaseGroup)
	}
}

func TestPickVideoTagsConfig_RuleSnapshotWins(t *testing.T) {
	global := core.VideoTagsConfig{
		Resolution: core.TagBucket{Enabled: true, Prefix: "global-res-"},
	}
	ruleSnap := &core.VideoTagsConfig{
		Resolution: core.TagBucket{Enabled: true, Prefix: "rule-res-"},
	}
	rule := &core.WebhookRule{VideoTags: ruleSnap}
	got := pickVideoTagsConfig(rule, core.Config{VideoTags: global})
	if got.Resolution.Prefix != "rule-res-" {
		t.Errorf("Resolution.Prefix = %q, want rule-res-", got.Resolution.Prefix)
	}
}

func TestPickVideoTagsConfig_GlobalFallback(t *testing.T) {
	global := core.VideoTagsConfig{
		Resolution: core.TagBucket{Enabled: true, Prefix: "global-res-"},
	}
	rule := &core.WebhookRule{VideoTags: nil}
	got := pickVideoTagsConfig(rule, core.Config{VideoTags: global})
	if got.Resolution.Prefix != "global-res-" {
		t.Errorf("Resolution.Prefix = %q, want global-res- (nil snapshot must fall back)", got.Resolution.Prefix)
	}
}

func TestExtractMediaInfoFromDownload_QualityResolutionFallback(t *testing.T) {
	// Library scan reads Quality.Quality.Resolution as a fallback when
	// mediaInfo.Height is missing — webhook adapter must do the same.
	body := []byte(`{
		"movie":     {"id": 42, "title": "Dune"},
		"movieFile": {
			"id": 100, "relativePath": "x.mkv",
			"mediaInfo": {"audioCodec": "EAC3"},
			"quality":   {"quality": {"resolution": 2160}}
		}
	}`)
	var p downloadEventPayload
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	ed := extractDownload("radarr", p); id, mi, qualityRes, ok := ed.ItemID, ed.MediaInfo, ed.QualityResolution, ed.OK
	if !ok {
		t.Fatal("expected ok=true")
	}
	if id != 42 {
		t.Errorf("id = %d, want 42", id)
	}
	if mi.Height != 0 {
		t.Errorf("Height = %d, want 0 (mediaInfo lacks height — quality fallback only)", mi.Height)
	}
	if qualityRes != 2160 {
		t.Errorf("qualityRes = %d, want 2160", qualityRes)
	}
}

func TestExtractDownload_SonarrQualityResolutionFallback(t *testing.T) {
	// Sonarr's episodeFile.quality.quality.resolution is read identically
	// to Radarr's movieFile.quality.quality.resolution. Lock the path
	// explicitly so a future arr-type-asymmetric refactor can't drop it.
	body := []byte(`{
		"series":   {"id": 7, "title": "Andor"},
		"episodes": [{"id": 999, "episodeNumber": 1, "seasonNumber": 1}],
		"episodeFile": {
			"id": 200, "relativePath": "Andor/S01E01.mkv",
			"quality": {"quality": {"resolution": 1080}}
		}
	}`)
	var p downloadEventPayload
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	ed := extractDownload("sonarr", p)
	if !ed.OK {
		t.Fatal("expected ok=true")
	}
	if ed.QualityResolution != 1080 {
		t.Errorf("QualityResolution = %d, want 1080", ed.QualityResolution)
	}
	if ed.ItemID != 7 {
		t.Errorf("ItemID = %d, want 7 (series.id)", ed.ItemID)
	}
}

// TestAdapterEngineCallMatchesLibraryScan_Video locks the Video parity
// invariant. Three buckets + qualityRes int means more places for
// divergence than Audio's single-bucket case — engine helper signature
// changes here would silently break the webhook path without this test.
func TestAdapterEngineCallMatchesLibraryScan_Video(t *testing.T) {
	mi := engine.MediaInfo{
		Height:                2160,
		VideoCodec:            "x265",
		VideoBitDepth:         10,
		VideoDynamicRangeType: "HDR10",
	}
	qualityRes := 2160
	cfg := core.VideoTagsConfig{
		Resolution: core.TagBucket{Enabled: true, Prefix: "res-"},
		Codec:      core.TagBucket{Enabled: true, Prefix: "codec-"},
		HDR:        core.TagBucket{Enabled: true, Prefix: "hdr-"},
	}

	libDesired := engine.VideoTagsForFile(mi, qualityRes, core.VideoTagsToEngine(cfg))

	rule := &core.WebhookRule{VideoTags: nil}
	adapterCfg := pickVideoTagsConfig(rule, core.Config{VideoTags: cfg})
	adapterDesired := engine.VideoTagsForFile(mi, qualityRes, core.VideoTagsToEngine(adapterCfg))
	if !reflect.DeepEqual(libDesired, adapterDesired) {
		t.Fatalf("global-fallback divergence: lib=%v adapter=%v", libDesired, adapterDesired)
	}

	ruleSnap := &core.WebhookRule{VideoTags: &cfg}
	snapCfg := pickVideoTagsConfig(ruleSnap, core.Config{VideoTags: core.VideoTagsConfig{}})
	snapDesired := engine.VideoTagsForFile(mi, qualityRes, core.VideoTagsToEngine(snapCfg))
	if !reflect.DeepEqual(libDesired, snapDesired) {
		t.Fatalf("snapshot path divergence: lib=%v snap=%v", libDesired, snapDesired)
	}
}

// TestAdapterEngineCallMatchesLibraryScan locks the parity invariant:
// for the same (mediaInfo, AudioTagsConfig) pair, the webhook adapter's
// engine call must emit the same desired tag-set Library scan emits.
// Future regression: someone refactors AudioTagsToEngine or pickAudio
// and accidentally breaks one path. Without this test the divergence
// would only show up in user reports.
func TestAdapterEngineCallMatchesLibraryScan(t *testing.T) {
	mi := engine.MediaInfo{
		AudioCodec:              "TrueHD",
		AudioChannels:           7.1,
		AudioAdditionalFeatures: "Atmos",
	}
	cfg := core.AudioTagsConfig{Audio: core.TagBucket{Enabled: true, Prefix: "audio-"}}

	// Library scan path: cfg → AudioTagsToEngine → AudioTagsForFile
	libDesired := engine.AudioTagsForFile(mi, core.AudioTagsToEngine(cfg))

	// Adapter path (rule with nil snapshot, falls back to global):
	// pickAudioTagsConfig → AudioTagsToEngine → AudioTagsForFile
	rule := &core.WebhookRule{AudioTags: nil}
	adapterCfg := pickAudioTagsConfig(rule, core.Config{AudioTags: cfg})
	adapterDesired := engine.AudioTagsForFile(mi, core.AudioTagsToEngine(adapterCfg))

	if !reflect.DeepEqual(libDesired, adapterDesired) {
		t.Fatalf("library-scan vs adapter divergence: lib=%v adapter=%v", libDesired, adapterDesired)
	}

	// Also verify the snapshot path (rule.AudioTags populated) emits
	// the same thing when the snapshot equals the global.
	ruleWithSnap := &core.WebhookRule{AudioTags: &cfg}
	snapCfg := pickAudioTagsConfig(ruleWithSnap, core.Config{AudioTags: core.AudioTagsConfig{}}) // global empty
	snapDesired := engine.AudioTagsForFile(mi, core.AudioTagsToEngine(snapCfg))
	if !reflect.DeepEqual(libDesired, snapDesired) {
		t.Fatalf("snapshot path divergence: lib=%v snap=%v", libDesired, snapDesired)
	}
}

func TestFormatAutoTagSummary(t *testing.T) {
	cases := []struct {
		name     string
		toAdd    []string
		toRemove []string
		kept     int
		want     string
	}{
		{"adds only", []string{"audio:truehd", "audio:7-1"}, nil, 0, "+2 (audio:truehd, audio:7-1)"},
		{"removes only", nil, []string{"audio:dts"}, 0, "-1 (audio:dts)"},
		{"adds + removes", []string{"audio:eac3"}, []string{"audio:dts"}, 0, "+1 (audio:eac3) -1 (audio:dts)"},
		{"adds + kept", []string{"audio:atmos"}, nil, 2, "+1 (audio:atmos) =2"},
		{"all three", []string{"audio:truehd"}, []string{"audio:dts"}, 1, "+1 (audio:truehd) -1 (audio:dts) =1"},
		{"kept only suppressed (caller short-circuits)", nil, nil, 5, "=5"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := formatAutoTagSummary(c.toAdd, c.toRemove, c.kept); got != c.want {
				t.Errorf("formatAutoTagSummary = %q, want %q", got, c.want)
			}
		})
	}
}

// TestDownloadEventPayload_DecodeStable is the wire-shape lock —
// json field names match Sonarr/Radarr Connect actuals. Drift here
// means real events silently miss the adapter's decode path.
func TestDownloadEventPayload_DecodeStable(t *testing.T) {
	// Realistic Radarr Download event excerpt (synthetic; structure
	// matches captured samples).
	radarrBody := []byte(`{
		"eventType": "Download", "isUpgrade": true,
		"movie": {"id": 42, "title": "Dune", "year": 2021, "tmdbId": 438631},
		"movieFile": {
			"id": 100, "relativePath": "Dune.mkv",
			"mediaInfo": {"audioCodec": "TrueHD", "audioChannels": 7.1, "audioAdditionalFeatures": "Atmos"}
		}
	}`)
	var p downloadEventPayload
	if err := json.Unmarshal(radarrBody, &p); err != nil {
		t.Fatalf("decode radarr: %v", err)
	}
	if !p.IsUpgrade {
		t.Error("isUpgrade dropped on decode")
	}
	if p.Movie == nil || p.Movie.TmdbID != 438631 {
		t.Errorf("movie tmdbId not decoded: %+v", p.Movie)
	}
	if p.MovieFile == nil || p.MovieFile.MediaInfo == nil {
		t.Fatalf("movieFile.mediaInfo dropped: %+v", p.MovieFile)
	}
	want := &arr.MediaInfo{AudioCodec: "TrueHD", AudioChannels: 7.1, AudioAdditionalFeatures: "Atmos"}
	if !reflect.DeepEqual(p.MovieFile.MediaInfo, want) {
		t.Errorf("MediaInfo = %+v, want %+v", p.MovieFile.MediaInfo, want)
	}
}

// =====  Filter-only tag-mode (M-Webhook Slice D) =====
//
// These tests exercise the rule.TagSource == "filter-only" branches
// added to dispatchTagReleaseGroups + dispatchSyncToSecondary +
// buildFileDeleteManagedSet. The Library-scan twin (runTagFilterOnly)
// already has 409-collision + reTagName tests; these lock the per-
// event single-item parity for the webhook adapters.
//
// Note: full applyAutoTagDiff path requires an arr.Client mock, so
// these tests cover the early-out branches (missing tag → 400;
// File-Delete managed-set inclusion; "filterOnlyTag required"
// errors). Apply-side behavior is locked by integration soak.

func TestDispatchTagReleaseGroups_FilterOnlyMissingTagReturnsError(t *testing.T) {
	// Filter-only mode with empty FilterOnlyTag must return OK=false
	// with a clear "required" message. The validator catches this at
	// save-time — the adapter guard is defence-in-depth for
	// hand-edited / migrated configs.
	dir := t.TempDir()
	store := core.NewConfigStore(dir)
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	s := &Server{App: &core.App{Config: store}}
	rule := &core.WebhookRule{
		ID:            "r1",
		InstanceID:    "primary",
		AppType:       "radarr",
		Functions:     []core.WebhookFunction{core.WebhookFnTagReleaseGroups},
		TagSource:     "filter-only",
		FilterOnlyTag: "", // missing
	}
	// Need an instance in cfg so findInstanceByID succeeds (pre-validation
	// branch happens after instance resolve in the new adapter ordering).
	if err := store.Update(func(c *core.Config) {
		c.Instances = []core.Instance{{ID: "primary", Type: "radarr", Name: "Primary", URL: "http://localhost:7878"}}
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	body := []byte(`{"movie": {"id": 42}, "movieFile": {"id": 100, "relativePath": "x.mkv"}}`)
	env := &connectEventEnvelope{EventType: string(core.WebhookEventDownload)}
	res := s.dispatchTagReleaseGroups(context.Background(), rule, store.Get(), nil, env, body)
	if res.OK {
		t.Fatalf("expected OK=false (filterOnlyTag required), got %+v", res)
	}
	if !strings.Contains(res.Summary, "filterOnlyTag required") {
		t.Errorf("Summary = %q, want hint about missing filterOnlyTag", res.Summary)
	}
}

// (The sync-side filter-only path requires a httptest-fronted
// arr.Client to reach the FilterOnlyTag-required guard, since the
// adapter does GetMovieByTmdbID on secondary BEFORE the filter-only
// branch. The validator's save-time gate at TestWebhookRuleRequest_
// ValidateFilterOnly is the load-bearing guarantee that empty-tag
// filter-only rules can never reach the dispatcher in the first place.)

func TestWebhookRuleRequest_ValidateFilterOnly(t *testing.T) {
	// Locks the validator's filter-only contract at save-time:
	//   - tagSource="filter-only" + Tag-RG enabled requires filterOnlyTag
	//   - filterOnlyTag must match the tag-name regex
	//   - filterOnlyTag must NOT collide with an existing per-group rule's Tag
	//   - filter-only without Tag-RG does NOT require the tag (it's ignored)
	//   - tagSource other than "" / "active" / "filter-only" rejected
	cfg := core.Config{
		Instances: []core.Instance{
			{ID: "primary", Type: "radarr", Name: "Primary", URL: "http://localhost:7878"},
			{ID: "sonarr1", Type: "sonarr", Name: "Sonarr", URL: "http://localhost:8989"},
		},
		ReleaseGroups: []core.ReleaseGroup{
			{ID: "g1", Type: "radarr", Search: "FLUX", Tag: "rg-flux", Display: "FLUX", Enabled: true},
		},
	}
	base := webhookRuleRequest{
		Name:       "test",
		InstanceID: "primary",
		AppType:    "radarr",
		Functions:  []core.WebhookFunction{core.WebhookFnTagReleaseGroups},
	}

	t.Run("filter-only with Tag-RG and missing tag rejected", func(t *testing.T) {
		req := base
		req.TagSource = "filter-only"
		req.FilterOnlyTag = ""
		apiErr := req.validate(cfg)
		if apiErr == nil {
			t.Fatal("expected validation error, got nil")
		}
		if !strings.Contains(apiErr.Message, "filterOnlyTag is required") {
			t.Errorf("message = %q, want 'filterOnlyTag is required'", apiErr.Message)
		}
	})

	t.Run("filter-only with malformed tag rejected", func(t *testing.T) {
		req := base
		req.TagSource = "filter-only"
		req.FilterOnlyTag = "Lossless Web" // uppercase + space
		apiErr := req.validate(cfg)
		if apiErr == nil {
			t.Fatal("expected validation error, got nil")
		}
		if !strings.Contains(apiErr.Message, "filterOnlyTag must be lowercase") {
			t.Errorf("message = %q, want regex-rejection hint", apiErr.Message)
		}
	})

	t.Run("filter-only with colliding tag rejected (409)", func(t *testing.T) {
		req := base
		req.TagSource = "filter-only"
		req.FilterOnlyTag = "rg-flux" // collides with seeded group
		apiErr := req.validate(cfg)
		if apiErr == nil {
			t.Fatal("expected collision error, got nil")
		}
		if apiErr.Status != 409 {
			t.Errorf("status = %d, want 409", apiErr.Status)
		}
		if !strings.Contains(apiErr.Message, "FLUX") {
			t.Errorf("message = %q, want collision hint naming FLUX", apiErr.Message)
		}
	})

	t.Run("filter-only happy path passes", func(t *testing.T) {
		req := base
		req.TagSource = "filter-only"
		req.FilterOnlyTag = "lossless-web"
		if apiErr := req.validate(cfg); apiErr != nil {
			t.Errorf("expected pass, got %+v", apiErr)
		}
	})

	t.Run("active mode (default) does not require tag", func(t *testing.T) {
		req := base
		req.TagSource = "" // legacy default
		req.FilterOnlyTag = ""
		if apiErr := req.validate(cfg); apiErr != nil {
			t.Errorf("expected pass for active mode, got %+v", apiErr)
		}
	})

	t.Run("filter-only without Tag-RG does NOT require tag", func(t *testing.T) {
		// Rule is in filter-only mode but TagReleaseGroups is NOT in
		// Functions — backend treats fields as ignored, validator doesn't
		// require filterOnlyTag. Frontend should never send this shape,
		// but defence-in-depth.
		req := base
		req.Functions = []core.WebhookFunction{core.WebhookFnTagAudio}
		req.TagSource = "filter-only"
		req.FilterOnlyTag = ""
		if apiErr := req.validate(cfg); apiErr != nil {
			t.Errorf("expected pass (no Tag-RG → tag is ignored), got %+v", apiErr)
		}
	})

	t.Run("unknown tagSource value rejected", func(t *testing.T) {
		req := base
		req.TagSource = "discover" // valid for schedules but not webhook rules
		apiErr := req.validate(cfg)
		if apiErr == nil {
			t.Fatal("expected error for unknown tagSource, got nil")
		}
		if !strings.Contains(apiErr.Message, "tagSource") {
			t.Errorf("message = %q, want hint about tagSource", apiErr.Message)
		}
	})

	t.Run("filter-only with Sync (no Tag-RG) requires tag", func(t *testing.T) {
		// Sync function's filter-only branch reads FilterOnlyTag at fire-
		// time; tag-required gate must extend to Sync-without-Tag-RG too.
		req := base
		req.Functions = []core.WebhookFunction{core.WebhookFnSyncToSecondary}
		req.TagSource = "filter-only"
		req.FilterOnlyTag = ""
		apiErr := req.validate(cfg)
		if apiErr == nil {
			t.Fatal("expected validation error, got nil")
		}
		if !strings.Contains(apiErr.Message, "filterOnlyTag is required") {
			t.Errorf("message = %q, want 'filterOnlyTag is required'", apiErr.Message)
		}
	})

	// "filter-only with FileDeleteClean (no Tag-RG) requires tag" subtest
	// retired in C8 — WebhookFnFileDeleteClean is no longer in
	// allWebhookFunctions, so a rule listing it now fails the
	// "unknown function" validation before reaching the filter-only-tag
	// gate. The Tag-RG and Sync-to-secondary tag-required subtests
	// above still cover the surviving consumers of FilterOnlyTag.

	t.Run("Sonarr appType + filter-only rejected", func(t *testing.T) {
		// Filter-only is a Radarr-only feature today (Library scan
		// runTagFilterOnly is in the Radarr scan path). Reject Sonarr-
		// rules with TagSource=filter-only at save-time so the
		// dispatcher's filter-only branches (Tag-RG, Sync, File-Delete)
		// can stay AppType-agnostic without risking inconsistent fires.
		// Functions list uses Tag-Audio (valid on both Arrs) so the
		// Sonarr+filter-only gate is the one that fires, not the
		// upstream "Tag-RG only applies to Radarr" gate.
		req := base
		req.InstanceID = "sonarr1"
		req.AppType = "sonarr"
		req.Functions = []core.WebhookFunction{core.WebhookFnTagAudio}
		req.TagSource = "filter-only"
		req.FilterOnlyTag = "lossless-web"
		apiErr := req.validate(cfg)
		if apiErr == nil {
			t.Fatal("expected validation error for Sonarr + filter-only, got nil")
		}
		if apiErr.Status != 400 {
			t.Errorf("status = %d, want 400", apiErr.Status)
		}
		if !strings.Contains(apiErr.Message, "Radarr only") {
			t.Errorf("message = %q, want hint that filter-only is Radarr-only", apiErr.Message)
		}
	})
}

// Filter-only / RG tag inclusion in buildFileDeleteManagedSet was
// dropped in the M-webhook delete-semantics refactor (C2). Those tags
// now flow through the auto-strip-on-delete dispatcher (C3) instead.
// Three test helpers covering the old branches were removed; the
// replacement test surface for the new auto-strip path lives alongside
// that dispatcher's tests.

// Shared httptest infrastructure for the file-delete + auto-strip
// dispatcher integration tests below. Both fakeArr instances stand in
// for primary + secondary Radarr/Sonarr; tests record editor PUT calls
// and assert which sides got hit.

type fakeArr struct {
	server   *httptest.Server
	apiKey   string
	tmdbID   int
	itemID   int
	itemTags []int            // current tags on the looked-up item
	tags     []arr.TagDetail  // tag/detail listing
	editorCalls []editorCall  // recorded editor requests
}

type editorCall struct {
	Path      string
	MovieIDs  []int  `json:"movieIds,omitempty"`
	SeriesIDs []int  `json:"seriesIds,omitempty"`
	Tags      []int  `json:"tags"`
	ApplyTags string `json:"applyTags"`
}

// editorRequestBody matches the shape arr.Client.EditorApplyTags posts.
type editorRequestBody struct {
	MovieIDs  []int  `json:"movieIds,omitempty"`
	SeriesIDs []int  `json:"seriesIds,omitempty"`
	Tags      []int  `json:"tags"`
	ApplyTags string `json:"applyTags"`
}

func newFakeArr(t *testing.T, tmdbID, itemID int, itemTags []int, tags []arr.TagDetail) *fakeArr {
	t.Helper()
	f := &fakeArr{
		apiKey:   "test-key",
		tmdbID:   tmdbID,
		itemID:   itemID,
		itemTags: append([]int(nil), itemTags...),
		tags:     tags,
	}
	mux := http.NewServeMux()
	// /api/v3/tag/detail
	mux.HandleFunc("/api/v3/tag/detail", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(f.tags)
	})
	// /api/v3/movie?tmdbId=N — Radarr lookup
	mux.HandleFunc("/api/v3/movie", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		q := r.URL.Query().Get("tmdbId")
		if q != "" && q == strconv.Itoa(f.tmdbID) {
			_ = json.NewEncoder(w).Encode([]arr.Item{
				{ID: f.itemID, TmdbID: f.tmdbID, Tags: f.itemTags},
			})
			return
		}
		_ = json.NewEncoder(w).Encode([]arr.Item{})
	})
	// /api/v3/movie/{id} — single-item fetch (used by GetItemTags)
	mux.HandleFunc("/api/v3/movie/", func(w http.ResponseWriter, r *http.Request) {
		// Reject the editor sub-path here; it's handled by its own
		// handler below.
		if strings.HasSuffix(r.URL.Path, "/editor") {
			f.handleEditor(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			ID   int   `json:"id"`
			Tags []int `json:"tags"`
		}{ID: f.itemID, Tags: f.itemTags})
	})
	// /api/v3/series/{id} (Sonarr equivalent — unused in mirror tests
	// but provided for completeness so primary-Radarr tests don't
	// accidentally hit a 404 on a stray Sonarr code path).
	mux.HandleFunc("/api/v3/series/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/editor") {
			f.handleEditor(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			ID   int   `json:"id"`
			Tags []int `json:"tags"`
		}{ID: f.itemID, Tags: f.itemTags})
	})
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeArr) handleEditor(w http.ResponseWriter, r *http.Request) {
	var body editorRequestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	f.editorCalls = append(f.editorCalls, editorCall{
		Path:      r.URL.Path,
		MovieIDs:  body.MovieIDs,
		SeriesIDs: body.SeriesIDs,
		Tags:      body.Tags,
		ApplyTags: body.ApplyTags,
	})
	w.WriteHeader(http.StatusAccepted)
}

// TestDispatchFileDeleteCleanup_MirrorsFilterOnlyTagToSecondary +
// TestDispatchFileDeleteCleanup_FilterOnlyMirrorTolerantOfSecondaryGone
// were removed in the C2 commit of the M-webhook delete-semantics
// refactor. The filter-only mirror call was lifted out of this
// dispatcher: filter-only secondary strip now lives in the auto-strip-
// on-delete dispatcher (C3) alongside per-group RG strip, which gives
// both modes uniform mirror behavior. The new dispatcher's tests cover
// that behavior in a single place.

func TestDispatchFileDeleteCleanup_NoSecondaryMirrorWithoutSyncFn(t *testing.T) {
	// Per-bucket Audio strip-on-delete alone (no Sync function) →
	// secondary editor must NOT be called. dispatchFileDeleteCleanup is
	// always primary-only by design — secondary holds its own file with
	// its own mediaInfo and its own derived tags. Locks the property so
	// no future refactor silently fans out file-property cleanup.
	primary := newFakeArr(t, 100, 42, []int{1}, []arr.TagDetail{{ID: 1, Label: "audio-truehd"}})
	secondary := newFakeArr(t, 100, 4242, []int{1}, []arr.TagDetail{{ID: 1, Label: "audio-truehd"}})

	cfg := core.Config{
		Instances: []core.Instance{
			{ID: "primary", Type: "radarr", Name: "Primary", URL: primary.server.URL, APIKey: "test-key"},
			{ID: "secondary", Type: "radarr", Name: "Secondary 4K", URL: secondary.server.URL, APIKey: "test-key"},
		},
	}
	rule := &core.WebhookRule{
		ID:         "r1",
		InstanceID: "primary",
		AppType:    "radarr",
		AudioTags: &core.AudioTagsConfig{
			Audio:             core.TagBucket{Enabled: true, Prefix: "audio-"},
			StripOnFileDelete: true,
		},
	}
	app := &core.App{HTTPClient: &http.Client{Timeout: 5 * time.Second}}
	s := &Server{App: app}
	env := &connectEventEnvelope{EventType: string(core.WebhookEventMovieFileDelete)}
	body := []byte(`{"movie": {"id": 42, "tmdbId": 100}, "movieFile": {"id": 100}}`)

	res := s.dispatchFileDeleteCleanup(context.Background(), rule, cfg, primary.tags, env, body)
	if !res.OK {
		t.Fatalf("expected OK=true, got %+v", res)
	}
	if len(secondary.editorCalls) != 0 {
		t.Errorf("secondary editor must not be called when Sync function is absent; got %d call(s): %+v",
			len(secondary.editorCalls), secondary.editorCalls)
	}
}

func TestDispatchFileDeleteCleanup_PrimaryOnlyEvenWithSyncFunction(t *testing.T) {
	// Rule with Sync function on AND per-bucket Audio strip → secondary
	// editor must still NOT be called by dispatchFileDeleteCleanup.
	// File-property tags are per-instance file-derived — the secondary
	// holds its own file and derives its own tags. Sync function
	// applies to Tag-RG / filter-only via the auto-strip dispatcher
	// (C3), not to file-property cleanup.
	primary := newFakeArr(t, 100, 42, []int{1}, []arr.TagDetail{{ID: 1, Label: "audio-truehd"}})
	secondary := newFakeArr(t, 100, 4242, []int{1}, []arr.TagDetail{{ID: 1, Label: "audio-truehd"}})

	cfg := core.Config{
		Instances: []core.Instance{
			{ID: "primary", Type: "radarr", Name: "Primary", URL: primary.server.URL, APIKey: "test-key"},
			{ID: "secondary", Type: "radarr", Name: "Secondary 4K", URL: secondary.server.URL, APIKey: "test-key"},
		},
	}
	rule := &core.WebhookRule{
		ID:               "r1",
		InstanceID:       "primary",
		AppType:          "radarr",
		SyncToInstanceID: "secondary",
		Functions:        []core.WebhookFunction{core.WebhookFnSyncToSecondary},
		AudioTags: &core.AudioTagsConfig{
			Audio:             core.TagBucket{Enabled: true, Prefix: "audio-"},
			StripOnFileDelete: true,
		},
	}
	app := &core.App{HTTPClient: &http.Client{Timeout: 5 * time.Second}}
	s := &Server{App: app}
	env := &connectEventEnvelope{EventType: string(core.WebhookEventMovieFileDelete)}
	body := []byte(`{"movie": {"id": 42, "tmdbId": 100}, "movieFile": {"id": 100}}`)

	res := s.dispatchFileDeleteCleanup(context.Background(), rule, cfg, primary.tags, env, body)
	if !res.OK {
		t.Fatalf("expected OK=true, got %+v", res)
	}
	if len(secondary.editorCalls) != 0 {
		t.Errorf("file-property cleanup must not mirror to secondary even with Sync function on; got %d call(s): %+v",
			len(secondary.editorCalls), secondary.editorCalls)
	}
}

// containsInt was a helper for the removed filter-only-mirror test —
// the auto-strip-on-delete dispatcher (C3) will reintroduce a similar
// helper if/when needed for the new mirror tests.

// =====  Auto-strip Tag-RG on file-delete (C3) =====
//
// Tests for the dispatcher that enforces the Tag-RG invariant on file-
// delete events. Symmetric for per-group and filter-only modes; mirrors
// to secondary when fnSyncToSecondary is on the rule. Bash-parity with
// tagarr_import.sh:574+ (auto-strip RG tags + ENABLE_SYNC_TO_SECONDARY
// mirror).

func TestDispatchAutoStripTagRgOnDelete_PerGroup_StripsPrimaryAndSecondary(t *testing.T) {
	// Bash-parity: primary file delete → RG tag falls off primary AND
	// secondary in one fire. Tests the per-group branch end-to-end.
	primary := newFakeArr(t, 100, 42, []int{1}, []arr.TagDetail{{ID: 1, Label: "rg-flux"}})
	secondary := newFakeArr(t, 100, 4242, []int{1}, []arr.TagDetail{{ID: 1, Label: "rg-flux"}})

	cfg := core.Config{
		Instances: []core.Instance{
			{ID: "primary", Type: "radarr", Name: "Primary", URL: primary.server.URL, APIKey: "test-key"},
			{ID: "secondary", Type: "radarr", Name: "Secondary 4K", URL: secondary.server.URL, APIKey: "test-key"},
		},
		ReleaseGroups: []core.ReleaseGroup{
			{ID: "g1", Type: "radarr", Search: "FLUX", Tag: "rg-flux", Display: "FLUX", Enabled: true},
		},
	}
	rule := &core.WebhookRule{
		ID:               "r1",
		InstanceID:       "primary",
		AppType:          "radarr",
		SyncToInstanceID: "secondary",
		Functions: []core.WebhookFunction{
			core.WebhookFnTagReleaseGroups,
			core.WebhookFnSyncToSecondary,
		},
	}
	app := &core.App{HTTPClient: &http.Client{Timeout: 5 * time.Second}}
	s := &Server{App: app}
	env := &connectEventEnvelope{EventType: string(core.WebhookEventMovieFileDelete)}
	body := []byte(`{"movie": {"id": 42, "tmdbId": 100}, "movieFile": {"id": 100}}`)

	res := s.dispatchAutoStripTagRgOnDelete(context.Background(), rule, cfg, primary.tags, env, body)
	if !res.OK {
		t.Fatalf("expected OK=true, got %+v", res)
	}
	if len(primary.editorCalls) == 0 {
		t.Fatal("primary editor was never called — RG strip did not fire on primary")
	}
	if !containsInt(primary.editorCalls[len(primary.editorCalls)-1].Tags, 1) {
		t.Errorf("primary editor tags = %v, want include id 1 (rg-flux)", primary.editorCalls[len(primary.editorCalls)-1].Tags)
	}
	if len(secondary.editorCalls) == 0 {
		t.Fatal("secondary editor was never called — RG strip did not mirror to secondary")
	}
	if !containsInt(secondary.editorCalls[len(secondary.editorCalls)-1].Tags, 1) {
		t.Errorf("secondary editor tags = %v, want include id 1 (rg-flux mirrored)", secondary.editorCalls[len(secondary.editorCalls)-1].Tags)
	}
	if !strings.Contains(res.Summary, "Secondary 4K") {
		t.Errorf("res.Summary = %q, want mention of secondary instance", res.Summary)
	}
}

func TestDispatchAutoStripTagRgOnDelete_FilterOnly_StripsPrimaryAndSecondary(t *testing.T) {
	// Filter-only branch: single FilterOnlyTag stripped from both.
	primary := newFakeArr(t, 100, 42, []int{1}, []arr.TagDetail{{ID: 1, Label: "lossless-web"}})
	secondary := newFakeArr(t, 100, 4242, []int{1}, []arr.TagDetail{{ID: 1, Label: "lossless-web"}})

	cfg := core.Config{
		Instances: []core.Instance{
			{ID: "primary", Type: "radarr", Name: "Primary", URL: primary.server.URL, APIKey: "test-key"},
			{ID: "secondary", Type: "radarr", Name: "Secondary 4K", URL: secondary.server.URL, APIKey: "test-key"},
		},
	}
	rule := &core.WebhookRule{
		ID:               "r1",
		InstanceID:       "primary",
		AppType:          "radarr",
		TagSource:        "filter-only",
		FilterOnlyTag:    "lossless-web",
		SyncToInstanceID: "secondary",
		Functions: []core.WebhookFunction{
			core.WebhookFnTagReleaseGroups,
			core.WebhookFnSyncToSecondary,
		},
	}
	app := &core.App{HTTPClient: &http.Client{Timeout: 5 * time.Second}}
	s := &Server{App: app}
	env := &connectEventEnvelope{EventType: string(core.WebhookEventMovieFileDelete)}
	body := []byte(`{"movie": {"id": 42, "tmdbId": 100}, "movieFile": {"id": 100}}`)

	res := s.dispatchAutoStripTagRgOnDelete(context.Background(), rule, cfg, primary.tags, env, body)
	if !res.OK {
		t.Fatalf("expected OK=true, got %+v", res)
	}
	if len(primary.editorCalls) == 0 {
		t.Fatal("primary editor was never called")
	}
	if len(secondary.editorCalls) == 0 {
		t.Fatal("secondary editor was never called — filter-only mirror did not fire")
	}
	// Secondary call must include exactly the filter-only tag id.
	if !reflect.DeepEqual(secondary.editorCalls[len(secondary.editorCalls)-1].Tags, []int{1}) {
		t.Errorf("secondary editor tags = %v, want [1] (filter-only tag id)", secondary.editorCalls[len(secondary.editorCalls)-1].Tags)
	}
}

func TestDispatchAutoStripTagRgOnDelete_NoSync_PrimaryOnly(t *testing.T) {
	// Without fnSyncToSecondary on the rule, secondary editor must
	// not be called. User can reconcile secondary via Library scan
	// M3e Sync.
	primary := newFakeArr(t, 100, 42, []int{1}, []arr.TagDetail{{ID: 1, Label: "rg-flux"}})
	secondary := newFakeArr(t, 100, 4242, []int{1}, []arr.TagDetail{{ID: 1, Label: "rg-flux"}})

	cfg := core.Config{
		Instances: []core.Instance{
			{ID: "primary", Type: "radarr", Name: "Primary", URL: primary.server.URL, APIKey: "test-key"},
			{ID: "secondary", Type: "radarr", Name: "Secondary 4K", URL: secondary.server.URL, APIKey: "test-key"},
		},
		ReleaseGroups: []core.ReleaseGroup{
			{ID: "g1", Type: "radarr", Search: "FLUX", Tag: "rg-flux", Enabled: true},
		},
	}
	rule := &core.WebhookRule{
		ID:         "r1",
		InstanceID: "primary",
		AppType:    "radarr",
		Functions:  []core.WebhookFunction{core.WebhookFnTagReleaseGroups},
		// no fnSyncToSecondary
	}
	app := &core.App{HTTPClient: &http.Client{Timeout: 5 * time.Second}}
	s := &Server{App: app}
	env := &connectEventEnvelope{EventType: string(core.WebhookEventMovieFileDelete)}
	body := []byte(`{"movie": {"id": 42, "tmdbId": 100}, "movieFile": {"id": 100}}`)

	res := s.dispatchAutoStripTagRgOnDelete(context.Background(), rule, cfg, primary.tags, env, body)
	if !res.OK {
		t.Fatalf("expected OK=true, got %+v", res)
	}
	if len(primary.editorCalls) == 0 {
		t.Error("primary editor must still be called — primary strip is always on for the dispatcher")
	}
	if len(secondary.editorCalls) != 0 {
		t.Errorf("secondary editor must not be called without fnSyncToSecondary; got %d call(s)", len(secondary.editorCalls))
	}
}

func TestDispatchAutoStripTagRgOnDelete_DefenceInDepth(t *testing.T) {
	app := &core.App{HTTPClient: &http.Client{Timeout: 5 * time.Second}}
	s := &Server{App: app}
	env := &connectEventEnvelope{EventType: string(core.WebhookEventMovieFileDelete)}
	bodyMoviePresent := []byte(`{"movie": {"id": 42, "tmdbId": 100}}`)

	t.Run("Sonarr rule skipped", func(t *testing.T) {
		rule := &core.WebhookRule{AppType: "sonarr", Functions: []core.WebhookFunction{core.WebhookFnTagReleaseGroups}}
		res := s.dispatchAutoStripTagRgOnDelete(context.Background(), rule, core.Config{}, nil, env, bodyMoviePresent)
		if !res.OK || !strings.Contains(res.Summary, "Radarr-only") {
			t.Errorf("Sonarr rule expected to skip with Radarr-only note, got %+v", res)
		}
	})

	t.Run("no Tag-RG on rule skipped", func(t *testing.T) {
		rule := &core.WebhookRule{AppType: "radarr", Functions: []core.WebhookFunction{core.WebhookFnTagAudio}}
		res := s.dispatchAutoStripTagRgOnDelete(context.Background(), rule, core.Config{}, nil, env, bodyMoviePresent)
		if !res.OK || !strings.Contains(res.Summary, "no Tag-RG") {
			t.Errorf("no-Tag-RG rule expected to skip with note, got %+v", res)
		}
	})

	t.Run("no movie id on payload skipped", func(t *testing.T) {
		rule := &core.WebhookRule{AppType: "radarr", Functions: []core.WebhookFunction{core.WebhookFnTagReleaseGroups}}
		res := s.dispatchAutoStripTagRgOnDelete(context.Background(), rule, core.Config{}, nil, env, []byte(`{}`))
		if !res.OK || !strings.Contains(res.Summary, "no movie id") {
			t.Errorf("empty-payload rule expected to skip with note, got %+v", res)
		}
	})

	t.Run("filter-only with empty tag skipped", func(t *testing.T) {
		rule := &core.WebhookRule{
			AppType:       "radarr",
			TagSource:     "filter-only",
			FilterOnlyTag: "",
			Functions:     []core.WebhookFunction{core.WebhookFnTagReleaseGroups},
		}
		res := s.dispatchAutoStripTagRgOnDelete(context.Background(), rule, core.Config{}, nil, env, bodyMoviePresent)
		if !res.OK || !strings.Contains(res.Summary, "filter-only mode with no FilterOnlyTag") {
			t.Errorf("empty-FilterOnlyTag rule expected to skip with note, got %+v", res)
		}
	})

	t.Run("per-group mode with no managed RGs skipped", func(t *testing.T) {
		rule := &core.WebhookRule{
			AppType:    "radarr",
			Functions:  []core.WebhookFunction{core.WebhookFnTagReleaseGroups},
		}
		res := s.dispatchAutoStripTagRgOnDelete(context.Background(), rule, core.Config{}, nil, env, bodyMoviePresent)
		if !res.OK || !strings.Contains(res.Summary, "no managed release groups") {
			t.Errorf("no-RG rule expected to skip with note, got %+v", res)
		}
	})
}

func TestDispatchAutoStripTagRgOnDelete_SyncWithoutTmdbID(t *testing.T) {
	// Payload without tmdbId → primary strip succeeds, mirror skip
	// noted in summary. Library-scan M3e Sync reconciles later.
	primary := newFakeArr(t, 100, 42, []int{1}, []arr.TagDetail{{ID: 1, Label: "lossless-web"}})

	cfg := core.Config{
		Instances: []core.Instance{
			{ID: "primary", Type: "radarr", Name: "Primary", URL: primary.server.URL, APIKey: "test-key"},
		},
	}
	rule := &core.WebhookRule{
		ID:            "r1",
		InstanceID:    "primary",
		AppType:       "radarr",
		TagSource:     "filter-only",
		FilterOnlyTag: "lossless-web",
		Functions: []core.WebhookFunction{
			core.WebhookFnTagReleaseGroups,
			core.WebhookFnSyncToSecondary,
		},
	}
	app := &core.App{HTTPClient: &http.Client{Timeout: 5 * time.Second}}
	s := &Server{App: app}
	env := &connectEventEnvelope{EventType: string(core.WebhookEventMovieFileDelete)}
	body := []byte(`{"movie": {"id": 42}}`) // no tmdbId

	res := s.dispatchAutoStripTagRgOnDelete(context.Background(), rule, cfg, primary.tags, env, body)
	if !res.OK {
		t.Fatalf("primary strip should still succeed, got %+v", res)
	}
	if !strings.Contains(res.Summary, "secondary mirror skipped (no tmdbId on event)") {
		t.Errorf("summary missing tmdbId skip note: %q", res.Summary)
	}
}

func containsInt(haystack []int, needle int) bool {
	for _, v := range haystack {
		if v == needle {
			return true
		}
	}
	return false
}
