package api

import (
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"resolvarr/internal/core"
	"resolvarr/internal/core/agents"
)

// TestBuildNotificationPayload covers the orchestration helper that
// stitches together the framework helpers (composeTitle / pickColor /
// composeFields / extractPosterURL / composeFooterSuffix) into one
// agents.Payload. Locks the skip-on-empty contract (no Changed=true
// results → false return) and the NotifyOnEveryEvent debug-mode
// fallback.
func TestBuildNotificationPayload(t *testing.T) {
	radarrInstance := &core.Instance{ID: "arr1", Name: "Radarr Main", Type: "radarr"}
	radarrBody := []byte(`{"movie":{"images":[{"coverType":"poster","remoteUrl":"https://cdn.tmdb.example/poster.jpg"}]}}`)

	t.Run("happy path: Tagged + Auto-tagged combo with poster", func(t *testing.T) {
		rule := &core.WebhookRule{Name: "Tag 4K imports", NotifyOnFire: true}
		results := []functionResult{
			{Function: core.WebhookFnTagReleaseGroups, OK: true, Changed: true,
				Detail: TagDetail{Added: []string{"FLUX"}, Primary: "Radarr Main"}},
			{Function: core.WebhookFnTagAudio, OK: true, Changed: true,
				Detail: AudioDetail{PlainSummary: "TrueHD Atmos 7.1"}},
		}
		run := core.WebhookRuleRun{ItemTitle: "Dune: Part Two", ItemContext: "2024"}
		payload, fire := buildNotificationPayload(rule, radarrInstance, core.WebhookEventDownload, results, radarrBody, run, nil)
		if !fire {
			t.Fatalf("expected fire=true on Changed=true results")
		}
		if payload.Title != "Tagged + Auto-tagged - Dune: Part Two (2024)" {
			t.Errorf("title = %q, want 'Tagged + Auto-tagged - Dune: Part Two (2024)'", payload.Title)
		}
		if payload.Color != embedColorTagged {
			t.Errorf("color = %#x, want orange (%#x)", payload.Color, embedColorTagged)
		}
		if payload.ThumbnailURL != "https://cdn.tmdb.example/poster.jpg" {
			t.Errorf("thumbnail = %q, want poster URL", payload.ThumbnailURL)
		}
		if payload.FooterSuffix != "" {
			t.Errorf("footer suffix = %q, want empty (rule name now lives in a 'Rule' field, not the footer)", payload.FooterSuffix)
		}
		if payload.Timestamp.IsZero() {
			t.Errorf("expected payload.Timestamp set for webhook-fire notifications")
		}
		if len(payload.Fields) == 0 {
			t.Errorf("expected fields to render for the bundle, got 0")
		}
		// Verify the new Rule field landed.
		var sawRule bool
		for _, f := range payload.Fields {
			if f.Name == "Rule" && f.Value == "Tag 4K imports" {
				sawRule = true
				break
			}
		}
		if !sawRule {
			t.Errorf("expected a Rule field with rule name; got %+v", payload.Fields)
		}
	})

	t.Run("skip path: no Changed=true results + NotifyOnEveryEvent=false", func(t *testing.T) {
		rule := &core.WebhookRule{Name: "Quiet rule", NotifyOnFire: true}
		results := []functionResult{
			{Function: core.WebhookFnTagAudio, OK: true, Changed: false}, // ran clean, no change
		}
		run := core.WebhookRuleRun{ItemTitle: "Some Movie", ItemContext: "2024"}
		_, fire := buildNotificationPayload(rule, radarrInstance, core.WebhookEventDownload, results, nil, run, nil)
		if fire {
			t.Errorf("expected fire=false on Changed=false-only results (only-actual-changes rule)")
		}
	})

	// (NotifyOnEveryEvent debug-mode subtests retired in 7.4d along
	// with the field itself. "no Changed → fire=false" is now the
	// only path; the agent-filter-eliminates-everything subtest below
	// covers the filtered equivalent.)

	t.Run("delete event with strip results → 'Cleaned up tags' title", func(t *testing.T) {
		rule := &core.WebhookRule{Name: "Cleanup", NotifyOnFire: true}
		results := []functionResult{
			{Function: core.WebhookFnTagReleaseGroups, OK: true, Changed: true,
				Detail: FileDeleteDetail{
					PerBucket:    map[string][]string{"audio": {"x"}},
					TagRgRemoved: "FLUX",
					Primary:      "Radarr Main",
				}},
		}
		run := core.WebhookRuleRun{ItemTitle: "Old Movie", ItemContext: "2019"}
		payload, fire := buildNotificationPayload(rule, radarrInstance, core.WebhookEventMovieFileDelete, results, nil, run, nil)
		if !fire {
			t.Fatalf("expected fire=true on delete with Changed results")
		}
		if payload.Title != "Cleaned up tags - Old Movie (2019)" {
			t.Errorf("title = %q", payload.Title)
		}
		if payload.Color != embedColorDelete {
			t.Errorf("color = %#x, want red (%#x)", payload.Color, embedColorDelete)
		}
	})

	t.Run("delete event with no strip → no embed (user rule)", func(t *testing.T) {
		rule := &core.WebhookRule{Name: "Cleanup", NotifyOnFire: true}
		run := core.WebhookRuleRun{ItemTitle: "Old Movie", ItemContext: "2019"}
		_, fire := buildNotificationPayload(rule, radarrInstance, core.WebhookEventMovieFileDelete, nil, nil, run, nil)
		if fire {
			t.Errorf("expected fire=false on delete with no Changed results (option A)")
		}
	})

	t.Run("nil instance → empty appType passed to extractPosterURL (no thumbnail)", func(t *testing.T) {
		rule := &core.WebhookRule{Name: "x", NotifyOnFire: true}
		results := []functionResult{
			{Function: core.WebhookFnTagReleaseGroups, OK: true, Changed: true,
				Detail: TagDetail{Added: []string{"FLUX"}}},
		}
		run := core.WebhookRuleRun{ItemTitle: "Movie", ItemContext: "2024"}
		payload, fire := buildNotificationPayload(rule, nil, core.WebhookEventDownload, results, radarrBody, run, nil)
		if !fire {
			t.Fatalf("expected fire=true on Changed=true results")
		}
		if payload.ThumbnailURL != "" {
			t.Errorf("nil instance should produce no thumbnail, got %q", payload.ThumbnailURL)
		}
	})

	// Per-agent function filter (7.4c): each agent receives its own
	// tailored payload. Filter eliminating everything for THAT agent
	// → fire=false → caller skips dispatch for that agent.
	t.Run("agent filter eliminates everything → fire=false (silent skip)", func(t *testing.T) {
		rule := &core.WebhookRule{Name: "x", NotifyOnFire: true}
		results := []functionResult{
			{Function: core.WebhookFnTagAudio, OK: true, Changed: true,
				Detail: AudioDetail{PlainSummary: "TrueHD"}},
		}
		run := core.WebhookRuleRun{ItemTitle: "Movie", ItemContext: "2024"}
		// Agent subscribes to grabRename only — but the fire is Tag-Audio.
		_, fire := buildNotificationPayload(rule, radarrInstance, core.WebhookEventDownload, results, radarrBody, run, []string{"grabRename"})
		if fire {
			t.Errorf("expected fire=false when filter excludes all fired functions")
		}
	})

	t.Run("agent filter narrows title + color + fields", func(t *testing.T) {
		rule := &core.WebhookRule{Name: "x", NotifyOnFire: true}
		results := []functionResult{
			{Function: core.WebhookFnTagReleaseGroups, OK: true, Changed: true,
				Detail: TagDetail{Added: []string{"FLUX"}, Primary: "Radarr Main"}},
			{Function: core.WebhookFnDiscover, OK: true, Changed: true,
				Detail: DiscoverDetail{NewGroup: "SiC"}},
		}
		run := core.WebhookRuleRun{ItemTitle: "Movie", ItemContext: "2024"}
		// Agent subscribed only to Discover → title is "Discovered"
		// (NOT "Tagged + Discovered"), color is gold (Discover-only),
		// fields contain only the New-group line.
		payload, fire := buildNotificationPayload(rule, radarrInstance, core.WebhookEventDownload, results, nil, run, []string{"discover"})
		if !fire {
			t.Fatalf("expected fire=true")
		}
		if payload.Title != "Discovered - Movie (2024)" {
			t.Errorf("title = %q, want 'Discovered - Movie (2024)'", payload.Title)
		}
		if payload.Color != embedColorDiscover {
			t.Errorf("color = %#x, want gold %#x (filtered to Discover-only)", payload.Color, embedColorDiscover)
		}
		// Post-2026-05-24: payload includes universal Tagged-in lead +
		// Rule + Event suffix in addition to per-section fields. The
		// agent-filter check here verifies that the Discover-only
		// filtered fire surfaces ONLY "New group" as its detail field
		// (no Quality tag from the filtered-out Tag-RG).
		var sawNewGroup, sawTaggedIn, sawRule, sawEvent bool
		var detailCount int
		for _, f := range payload.Fields {
			switch f.Name {
			case "New group":
				sawNewGroup = true
				detailCount++
			case "Tagged in":
				sawTaggedIn = true
			case "Rule":
				sawRule = true
			case "Event":
				sawEvent = true
			default:
				detailCount++
			}
		}
		if !sawNewGroup {
			t.Errorf("expected a 'New group' field; got %+v", payload.Fields)
		}
		if detailCount != 1 {
			t.Errorf("expected exactly one detail field (New group); got %d in %+v", detailCount, payload.Fields)
		}
		if !sawTaggedIn || !sawRule || !sawEvent {
			t.Errorf("expected universal Tagged-in + Rule + Event scaffolding; got %+v", payload.Fields)
		}
	})
}

// TestResolveNotificationAgents locks the three-layer gating-precedence
// rule from the M-Webhook framework's foundation decisions.
func TestResolveNotificationAgents(t *testing.T) {
	mkAgent := func(id, name string, enabled bool, events core.AgentEvents) core.NotificationAgent {
		return core.NotificationAgent{ID: id, Name: name, Type: "discord", Enabled: enabled, Events: events}
	}
	allAgents := []core.NotificationAgent{
		mkAgent("a-on-import", "Import-only", true, core.AgentEvents{OnImport: true}),
		mkAgent("a-on-grab", "Grab-only", true, core.AgentEvents{OnGrab: true}),
		mkAgent("a-on-all", "All-events", true, core.AgentEvents{OnImport: true, OnGrab: true, OnFileDelete: true}),
		mkAgent("a-disabled", "Disabled", false, core.AgentEvents{OnImport: true}),
		mkAgent("a-no-events", "No events", true, core.AgentEvents{}),
	}

	idsOf := func(agents []core.NotificationAgent) []string {
		ids := make([]string, len(agents))
		for i, a := range agents {
			ids[i] = a.ID
		}
		return ids
	}

	t.Run("NotifyOnFire=false → no agents (master kill-switch)", func(t *testing.T) {
		rule := &core.WebhookRule{NotifyOnFire: false}
		got := resolveNotificationAgents(rule, allAgents, core.WebhookEventDownload)
		if len(got) != 0 {
			t.Errorf("expected 0 agents, got %v", idsOf(got))
		}
	})

	t.Run("nil rule → no agents", func(t *testing.T) {
		got := resolveNotificationAgents(nil, allAgents, core.WebhookEventDownload)
		if len(got) != 0 {
			t.Errorf("expected 0 agents, got %v", idsOf(got))
		}
	})

	t.Run("Events.OnImport gates Download events", func(t *testing.T) {
		rule := &core.WebhookRule{NotifyOnFire: true}
		got := resolveNotificationAgents(rule, allAgents, core.WebhookEventDownload)
		// a-on-import + a-on-all subscribe to import; a-on-grab,
		// a-disabled, a-no-events do not.
		gotIDs := idsOf(got)
		if len(gotIDs) != 2 || !slices.Contains(gotIDs, "a-on-import") || !slices.Contains(gotIDs, "a-on-all") {
			t.Errorf("expected [a-on-import, a-on-all], got %v", gotIDs)
		}
	})

	t.Run("Events.OnGrab gates Grab events", func(t *testing.T) {
		rule := &core.WebhookRule{NotifyOnFire: true}
		got := resolveNotificationAgents(rule, allAgents, core.WebhookEventGrab)
		gotIDs := idsOf(got)
		if len(gotIDs) != 2 || !slices.Contains(gotIDs, "a-on-grab") || !slices.Contains(gotIDs, "a-on-all") {
			t.Errorf("expected [a-on-grab, a-on-all], got %v", gotIDs)
		}
	})

	t.Run("Events.OnFileDelete gates MovieFileDelete events", func(t *testing.T) {
		rule := &core.WebhookRule{NotifyOnFire: true}
		got := resolveNotificationAgents(rule, allAgents, core.WebhookEventMovieFileDelete)
		gotIDs := idsOf(got)
		if len(gotIDs) != 1 || gotIDs[0] != "a-on-all" {
			t.Errorf("expected [a-on-all], got %v", gotIDs)
		}
	})

	t.Run("empty agent list → no agents", func(t *testing.T) {
		rule := &core.WebhookRule{NotifyOnFire: true}
		got := resolveNotificationAgents(rule, nil, core.WebhookEventDownload)
		if len(got) != 0 {
			t.Errorf("expected 0 agents, got %v", idsOf(got))
		}
	})

	t.Run("disabled agent skipped even when Events match", func(t *testing.T) {
		rule := &core.WebhookRule{NotifyOnFire: true}
		got := resolveNotificationAgents(rule, allAgents, core.WebhookEventDownload)
		for _, a := range got {
			if a.ID == "a-disabled" {
				t.Errorf("disabled agent should not surface, got %v", idsOf(got))
			}
		}
	})
}

// TestAgentSubscribesToEvent locks the WebhookConnectEvent → Events.OnX
// mapping. Covers the four delete-event variants + Grab + Download +
// unknown event types.
func TestAgentSubscribesToEvent(t *testing.T) {
	cases := []struct {
		name   string
		events core.AgentEvents
		event  core.WebhookConnectEvent
		want   bool
	}{
		{"OnGrab → Grab matches", core.AgentEvents{OnGrab: true}, core.WebhookEventGrab, true},
		{"OnGrab → Download does NOT match", core.AgentEvents{OnGrab: true}, core.WebhookEventDownload, false},
		{"OnImport → Download matches", core.AgentEvents{OnImport: true}, core.WebhookEventDownload, true},
		{"OnImport → Grab does NOT match", core.AgentEvents{OnImport: true}, core.WebhookEventGrab, false},
		{"OnFileDelete → MovieFileDelete matches", core.AgentEvents{OnFileDelete: true}, core.WebhookEventMovieFileDelete, true},
		{"OnFileDelete → MovieFileDeleteForUpgrade matches", core.AgentEvents{OnFileDelete: true}, core.WebhookEventMovieFileDeleteForUpgrade, true},
		{"OnFileDelete → EpisodeFileDelete matches", core.AgentEvents{OnFileDelete: true}, core.WebhookEventEpisodeFileDelete, true},
		{"OnFileDelete → EpisodeFileDeleteForUpgrade matches", core.AgentEvents{OnFileDelete: true}, core.WebhookEventEpisodeFileDeleteForUpgrade, true},
		{"empty flags → no match", core.AgentEvents{}, core.WebhookEventDownload, false},
		{"unknown event → no match even with all flags", core.AgentEvents{OnImport: true, OnGrab: true, OnFileDelete: true}, "ApplicationUpdate", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := core.NotificationAgent{Events: tc.events}
			got := agentSubscribesToEvent(a, tc.event)
			if got != tc.want {
				t.Errorf("agentSubscribesToEvent(%q) = %v, want %v", tc.event, got, tc.want)
			}
		})
	}
}

// captureTransport is a test-side http.RoundTripper that captures
// the request URL + body and returns a fixed 204 response. Installed
// on App.SafeClient so the Discord provider's webhook-URL validator
// (`https://discord.com/api/webhooks/...`) passes while the actual
// HTTP exchange is captured locally — no network, no real Discord.
//
// Mutex-protected because Discord's Async()=true puts RoundTrip on a
// SafeGo goroutine while the test goroutine polls the same fields.
// Without the lock `go test -race` flags every E2E run.
type captureTransport struct {
	mu       sync.Mutex
	lastURL  string
	lastBody []byte
	calls    int
}

func (c *captureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	c.lastURL = req.URL.String()
	if req.Body != nil {
		c.lastBody, _ = io.ReadAll(req.Body)
	}
	return &http.Response{
		StatusCode: 204,
		Body:       io.NopCloser(strings.NewReader("")),
		Header:     http.Header{},
	}, nil
}

// snapshot returns a consistent view of the captured request state
// for the test goroutine. Body is copied so the test can't observe
// mid-write tearing if a later request lands between snapshots.
func (c *captureTransport) snapshot() (calls int, url string, body []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls, c.lastURL, append([]byte(nil), c.lastBody...)
}

// TestFireWebhookNotificationsEndToEnd locks the full dispatch
// hot path with a real Discord-shaped JSON payload. Exercises:
//   - Rule has NotifyOnFire=true → recipient resolution finds the
//     Discord agent (Events.OnImport=true)
//   - Agent's Functions whitelist filters the embed (tagAudio +
//     tagReleaseGroups only; Discover result is filtered out)
//   - buildNotificationPayload assembles agents.Payload with title +
//     color + fields + poster + footer
//   - app.DispatchNotificationAgent → Discord provider → captureTransport
//     records the actual wire JSON
//
// The wire-format assertion locks the contract task #8 (UI) will
// build against: changing the embed shape silently would fail this
// test rather than silently break in production.
func TestFireWebhookNotificationsEndToEnd(t *testing.T) {
	capture := &captureTransport{}
	store := core.NewConfigStore(t.TempDir())
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := store.Update(func(c *core.Config) {
		c.Instances = []core.Instance{{ID: "arr1", Name: "Radarr Main", Type: "radarr", URL: "http://radarr.example", APIKey: "k"}}
		c.NotificationAgents = []core.NotificationAgent{
			{
				ID:      "discord-tags",
				Name:    "Discord — tags",
				Type:    "discord",
				Enabled: true,
				Events:  agents.Events{OnImport: true},
				// Agent subscribed to Tag-Q-R + tagAudio only — the
				// Discover result in the fire should be FILTERED OUT.
				Functions: []string{"tagReleaseGroups", "tagAudio"},
				Config:    agents.Config{DiscordWebhook: "https://discord.com/api/webhooks/123/abc"},
			},
		}
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	app := &core.App{
		Config:       store,
		Version:      "test",
		NotifyClient: &http.Client{Transport: capture, Timeout: 5 * time.Second},
		SafeClient:   &http.Client{Transport: capture, Timeout: 5 * time.Second},
	}
	s := &Server{App: app}

	rule := &core.WebhookRule{
		ID:           "r1",
		Name:         "Tag 4K imports",
		Enabled:      true,
		InstanceID:   "arr1",
		AppType:      "radarr",
		NotifyOnFire: true,
	}
	env := &connectEventEnvelope{EventType: string(core.WebhookEventDownload)}
	body := []byte(`{"movie":{"id":42,"images":[{"coverType":"poster","remoteUrl":"https://cdn.tmdb.example/poster.jpg"}]}}`)
	results := []functionResult{
		{Function: core.WebhookFnTagReleaseGroups, OK: true, Changed: true,
			Detail: TagDetail{Added: []string{"FLUX"}, Primary: "Radarr Main"}},
		{Function: core.WebhookFnTagAudio, OK: true, Changed: true,
			Detail: AudioDetail{PlainSummary: "TrueHD Atmos 7.1"}},
		// Filtered out by the agent's Functions whitelist.
		{Function: core.WebhookFnDiscover, OK: true, Changed: true,
			Detail: DiscoverDetail{NewGroup: "SiC"}},
	}
	run := core.WebhookRuleRun{ItemTitle: "Dune: Part Two", ItemContext: "2024"}

	s.fireWebhookNotifications(rule, &core.Instance{ID: "arr1", Name: "Radarr Main", Type: "radarr"}, env, body, results, run)

	// Discord provider's Notify is Async() → wraps in utils.SafeGo →
	// runs in a goroutine. Give it a moment to land. 5s deadline gives
	// race-detector-slowed CI runs ample headroom.
	deadline := time.Now().Add(5 * time.Second)
	var calls int
	var url string
	var gotBody []byte
	for {
		calls, url, gotBody = capture.snapshot()
		if calls > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if calls == 0 {
		t.Fatalf("expected at least one HTTP call to the Discord webhook, got none")
	}

	// Verify URL hit the configured webhook (no rewrite, no SSRF
	// surprises — the captureTransport sees the raw URL).
	if !strings.HasPrefix(url, "https://discord.com/api/webhooks/") {
		t.Errorf("captured URL = %q, want https://discord.com/api/webhooks/...", url)
	}

	// Parse the captured body as Discord's embed shape + assert the
	// per-agent-filter took effect: Discover should be missing.
	var got struct {
		Embeds []struct {
			Title  string `json:"title"`
			Color  int    `json:"color"`
			Fields []struct {
				Name   string `json:"name"`
				Value  string `json:"value"`
				Inline bool   `json:"inline"`
			} `json:"fields"`
			Footer struct {
				Text string `json:"text"`
			} `json:"footer"`
			Thumbnail struct {
				URL string `json:"url"`
			} `json:"thumbnail"`
		} `json:"embeds"`
	}
	if err := json.Unmarshal(gotBody, &got); err != nil {
		t.Fatalf("unmarshal captured body: %v\nbody: %s", err, gotBody)
	}
	if len(got.Embeds) != 1 {
		t.Fatalf("expected 1 embed, got %d", len(got.Embeds))
	}
	e := got.Embeds[0]

	// Title: filtered to Tag-Q-R + Auto-tagged (Audio bucket); no
	// Discover label since the agent didn't subscribe.
	if e.Title != "Tagged + Auto-tagged - Dune: Part Two (2024)" {
		t.Errorf("title = %q, want 'Tagged + Auto-tagged - Dune: Part Two (2024)'", e.Title)
	}
	// Color: orange (tag-family wins over Discover via priority chain,
	// but Discover is filtered out entirely anyway).
	if e.Color != embedColorTagged {
		t.Errorf("color = %#x, want orange %#x", e.Color, embedColorTagged)
	}
	// Fields (post-2026-05-24 layout): Tagged in + Quality tag + Audio
	// from the detail sections, then Rule + Event from the universal
	// suffix. NO "New group" since Discover was filtered.
	wantFields := []string{"Tagged in", "Quality tag", "Audio", "Rule", "Event"}
	if len(e.Fields) != len(wantFields) {
		t.Fatalf("field count = %d (%+v), want %d (%v)", len(e.Fields), e.Fields, len(wantFields), wantFields)
	}
	for i, want := range wantFields {
		if e.Fields[i].Name != want {
			t.Errorf("fields[%d].Name = %q, want %q", i, e.Fields[i].Name, want)
		}
	}
	// Per-field spot-check
	if e.Fields[0].Value != "Radarr Main" {
		t.Errorf("Tagged in = %q, want 'Radarr Main'", e.Fields[0].Value)
	}
	if e.Fields[1].Value != "FLUX" {
		t.Errorf("Quality tag = %q, want 'FLUX'", e.Fields[1].Value)
	}
	if e.Fields[2].Value != "TrueHD Atmos 7.1" {
		t.Errorf("Audio = %q, want 'TrueHD Atmos 7.1'", e.Fields[2].Value)
	}
	if e.Fields[3].Value != "Tag 4K imports" {
		t.Errorf("Rule = %q, want 'Tag 4K imports'", e.Fields[3].Value)
	}
	if e.Fields[4].Value != "Import" {
		t.Errorf("Event = %q, want 'Import'", e.Fields[4].Value)
	}
	// Footer: version + by-line only — rule name is no longer crammed
	// into the footer (it has its own Rule field now).
	if !strings.Contains(e.Footer.Text, "Resolvarr test by ProphetSe7en") {
		t.Errorf("footer missing default text: %q", e.Footer.Text)
	}
	if strings.Contains(e.Footer.Text, "rule:") {
		t.Errorf("footer should not contain 'rule:' suffix anymore: %q", e.Footer.Text)
	}
	// Thumbnail: poster URL flows through the http(s)-filter.
	if e.Thumbnail.URL != "https://cdn.tmdb.example/poster.jpg" {
		t.Errorf("thumbnail = %q, want poster URL", e.Thumbnail.URL)
	}
}

// TestFireWebhookNotificationsAgentSkipsFilteredFire verifies the
// silent-skip path: when an agent's Functions filter eliminates
// every changed result, the dispatcher must NOT make an HTTP call
// for that agent. Each agent gets its own per-agent decision.
func TestFireWebhookNotificationsAgentSkipsFilteredFire(t *testing.T) {
	capture := &captureTransport{}
	store := core.NewConfigStore(t.TempDir())
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := store.Update(func(c *core.Config) {
		c.Instances = []core.Instance{{ID: "arr1", Name: "Radarr Main", Type: "radarr"}}
		c.NotificationAgents = []core.NotificationAgent{{
			ID:        "discord-grab-only",
			Name:      "Discord — Grab only",
			Type:      "discord",
			Enabled:   true,
			Events:    agents.Events{OnImport: true},
			Functions: []string{"grabRename"}, // subscribes to Grab Rename only
			Config:    agents.Config{DiscordWebhook: "https://discord.com/api/webhooks/123/abc"},
		}}
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	app := &core.App{
		Config:       store,
		Version:      "test",
		NotifyClient: &http.Client{Transport: capture, Timeout: 5 * time.Second},
		SafeClient:   &http.Client{Transport: capture, Timeout: 5 * time.Second},
	}
	s := &Server{App: app}
	rule := &core.WebhookRule{ID: "r1", Name: "Tag 4K", InstanceID: "arr1", AppType: "radarr", NotifyOnFire: true}
	env := &connectEventEnvelope{EventType: string(core.WebhookEventDownload)}
	results := []functionResult{
		// Tag-Audio fires but agent isn't subscribed → filter empties
		// the embed → silent skip.
		{Function: core.WebhookFnTagAudio, OK: true, Changed: true,
			Detail: AudioDetail{PlainSummary: "TrueHD"}},
	}
	run := core.WebhookRuleRun{ItemTitle: "Movie", ItemContext: "2024"}

	s.fireWebhookNotifications(rule, &core.Instance{ID: "arr1", Type: "radarr"}, env, nil, results, run)

	// Give async dispatch time to land (none should fire).
	time.Sleep(100 * time.Millisecond)
	calls, url, _ := capture.snapshot()
	if calls != 0 {
		t.Errorf("expected 0 HTTP calls (agent filtered out all results), got %d (URL=%s)", calls, url)
	}
}

