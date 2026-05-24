package api

import (
	"testing"

	"resolvarr/internal/core"
	"resolvarr/internal/core/agents"
)

// TestComposeTitle covers every combo shape bash tagarr_import.sh
// emits plus the resolvarr-specific extensions (qBit S/E, qBit
// Category Fix, file-delete cleanup). Validates the human-scannable
// "Verb [+ Verb] - Title (Context)" shape end-to-end.
func TestComposeTitle(t *testing.T) {
	cases := []struct {
		name        string
		event       core.WebhookConnectEvent
		results     []functionResult
		itemTitle   string
		itemContext string
		want        string
	}{
		// Bash-parity Import-event combos (tagarr_import.sh:1356-1370)
		{
			name:        "Tag only — Radarr Download",
			event:       core.WebhookEventDownload,
			results:     []functionResult{{Function: core.WebhookFnTagReleaseGroups, OK: true, Changed: true}},
			itemTitle:   "Dune: Part Two",
			itemContext: "2024",
			want:        "Tagged - Dune: Part Two (2024)",
		},
		{
			name:  "Tag + Discover — bash 'Tagged + Discovered' combo",
			event: core.WebhookEventDownload,
			results: []functionResult{
				{Function: core.WebhookFnTagReleaseGroups, OK: true, Changed: true},
				{Function: core.WebhookFnDiscover, OK: true, Changed: true},
			},
			itemTitle:   "The Matrix",
			itemContext: "1999",
			want:        "Tagged + Discovered - The Matrix (1999)",
		},
		{
			name:  "Tag + Recover — bash 'Tagged + Fixed' combo",
			event: core.WebhookEventDownload,
			results: []functionResult{
				{Function: core.WebhookFnTagReleaseGroups, OK: true, Changed: true},
				{Function: core.WebhookFnRecover, OK: true, Changed: true},
			},
			itemTitle:   "Inception",
			itemContext: "2010",
			want:        "Tagged + Fixed release group - Inception (2010)",
		},
		{
			name:        "Discover only — bash gold-color case",
			event:       core.WebhookEventDownload,
			results:     []functionResult{{Function: core.WebhookFnDiscover, OK: true, Changed: true}},
			itemTitle:   "Movie",
			itemContext: "2024",
			want:        "Discovered - Movie (2024)",
		},
		{
			name:        "Recover only — bash green-color case",
			event:       core.WebhookEventDownload,
			results:     []functionResult{{Function: core.WebhookFnRecover, OK: true, Changed: true}},
			itemTitle:   "Movie",
			itemContext: "2024",
			want:        "Fixed release group - Movie (2024)",
		},

		// Auto-tag bundling: three buckets → one label
		{
			name:  "Audio + Video + DV → single Auto-tagged label",
			event: core.WebhookEventDownload,
			results: []functionResult{
				{Function: core.WebhookFnTagAudio, OK: true, Changed: true},
				{Function: core.WebhookFnTagVideo, OK: true, Changed: true},
				{Function: core.WebhookFnTagDvDetail, OK: true, Changed: true},
			},
			itemTitle:   "Dune: Part Two",
			itemContext: "2024",
			want:        "Auto-tagged - Dune: Part Two (2024)",
		},
		{
			name:  "Tag + Auto-tag combo — both labels surface",
			event: core.WebhookEventDownload,
			results: []functionResult{
				{Function: core.WebhookFnTagReleaseGroups, OK: true, Changed: true},
				{Function: core.WebhookFnTagAudio, OK: true, Changed: true},
			},
			itemTitle:   "Dune: Part Two",
			itemContext: "2024",
			want:        "Tagged + Auto-tagged - Dune: Part Two (2024)",
		},

		// SyncToSecondary never produces a standalone label
		{
			name:  "Tag + Sync → Sync folded into Tagged label",
			event: core.WebhookEventDownload,
			results: []functionResult{
				{Function: core.WebhookFnTagReleaseGroups, OK: true, Changed: true},
				{Function: core.WebhookFnSyncToSecondary, OK: true, Changed: true},
			},
			itemTitle:   "Movie",
			itemContext: "2024",
			want:        "Tagged - Movie (2024)",
		},

		// Grab Rename has its own embed entirely (own title)
		{
			name:        "GrabRename — own title",
			event:       core.WebhookEventGrab,
			results:     []functionResult{{Function: core.WebhookFnGrabRename, OK: true, Changed: true}},
			itemTitle:   "Dune: Part Two",
			itemContext: "2024",
			want:        "Renamed in qBit - Dune: Part Two (2024)",
		},

		// qBit S/E + qBit Category Fix — own titles
		{
			name:        "qBit S/E on a Sonarr Grab",
			event:       core.WebhookEventGrab,
			results:     []functionResult{{Function: core.WebhookFnQbitSeTag, OK: true, Changed: true}},
			itemTitle:   "The Bear",
			itemContext: "S03E07",
			want:        "Episode tagged - The Bear (S03E07)",
		},
		{
			name:        "qBit Category Fix on Import",
			event:       core.WebhookEventDownload,
			results:     []functionResult{{Function: core.WebhookFnQbitCategoryFix, OK: true, Changed: true}},
			itemTitle:   "Movie",
			itemContext: "2024",
			want:        "Category fixed - Movie (2024)",
		},

		// Rule: notifications must contain only actual
		// changes. A delete event whose strip-on-delete dispatchers
		// found no managed tags to strip → no embed. History still
		// records the fire so the user can audit; Discord stays
		// quiet.
		{
			name:        "MovieFileDelete with nothing changed → empty (no embed)",
			event:       core.WebhookEventMovieFileDelete,
			results:     nil,
			itemTitle:   "Old Movie",
			itemContext: "2019",
			want:        "",
		},
		{
			name:        "EpisodeFileDeleteForUpgrade with nothing changed → empty",
			event:       core.WebhookEventEpisodeFileDeleteForUpgrade,
			results:     nil,
			itemTitle:   "Series",
			itemContext: "S01E05",
			want:        "",
		},
		// Delete event WITH actual strip results → "Cleaned up tags"
		// label (NOT "Tagged" — the action was REMOVE, not ADD).
		{
			name:  "MovieFileDelete with Tag-RG strip → 'Cleaned up tags' label",
			event: core.WebhookEventMovieFileDelete,
			results: []functionResult{
				{Function: core.WebhookFnTagReleaseGroups, OK: true, Changed: true},
			},
			itemTitle:   "Old Movie",
			itemContext: "2019",
			want:        "Cleaned up tags - Old Movie (2019)",
		},
		{
			name:  "MovieFileDelete with multi-bucket strip → single 'Cleaned up tags' label",
			event: core.WebhookEventMovieFileDelete,
			results: []functionResult{
				{Function: core.WebhookFnTagReleaseGroups, OK: true, Changed: true},
				{Function: core.WebhookFnTagAudio, OK: true, Changed: true},
				{Function: core.WebhookFnTagVideo, OK: true, Changed: true},
			},
			itemTitle:   "Old Movie",
			itemContext: "2019",
			want:        "Cleaned up tags - Old Movie (2019)", // all four collapse to one label
		},
		// Delete event with Changed=false results → empty (user rule
		// also applies on the delete side).
		{
			name:  "MovieFileDelete with strip results but Changed=false → empty",
			event: core.WebhookEventMovieFileDelete,
			results: []functionResult{
				{Function: core.WebhookFnTagReleaseGroups, OK: true, Changed: false},
				{Function: core.WebhookFnTagAudio, OK: true, Changed: false},
			},
			itemTitle: "Old Movie",
			want:      "",
		},

		// Rule: "notifications must contain only actual
		// changes" — successful no-op results (OK=true, Changed=false)
		// are excluded from the title even though the function ran
		// cleanly. Bash tagarr_import.sh's "Nothing to report" gate
		// (line 1457) implemented per-function.
		{
			name:  "OK=true but Changed=false → skipped from title",
			event: core.WebhookEventDownload,
			results: []functionResult{
				{Function: core.WebhookFnTagReleaseGroups, OK: true, Changed: false},
				{Function: core.WebhookFnTagAudio, OK: true, Changed: false},
			},
			itemTitle: "Movie",
			want:      "",
		},
		{
			name:  "Errored results (OK=false) → also excluded",
			event: core.WebhookEventDownload,
			results: []functionResult{
				{Function: core.WebhookFnTagReleaseGroups, OK: false},
				{Function: core.WebhookFnTagAudio, OK: false},
			},
			itemTitle: "Movie",
			want:      "",
		},
		{
			name:      "Empty results on non-delete event → empty",
			event:     core.WebhookEventDownload,
			results:   nil,
			itemTitle: "Movie",
			want:      "",
		},
		// Mixed result: one Changed=true + one Changed=false →
		// only the changed function contributes a label.
		{
			name:  "Mixed Changed flags → only changed function appears",
			event: core.WebhookEventDownload,
			results: []functionResult{
				{Function: core.WebhookFnTagReleaseGroups, OK: true, Changed: true},
				{Function: core.WebhookFnTagAudio, OK: true, Changed: false},
			},
			itemTitle:   "Movie",
			itemContext: "2024",
			want:        "Tagged - Movie (2024)",
		},

		// Edge: empty itemTitle gets a placeholder rather than a
		// truncated " - " (which would look broken in Discord).
		{
			name:      "Missing itemTitle → placeholder",
			event:     core.WebhookEventDownload,
			results:   []functionResult{{Function: core.WebhookFnTagReleaseGroups, OK: true, Changed: true}},
			itemTitle: "",
			want:      "Tagged - (unknown title)",
		},
		{
			name:        "Missing itemContext → no trailing parens",
			event:       core.WebhookEventDownload,
			results:     []functionResult{{Function: core.WebhookFnTagReleaseGroups, OK: true, Changed: true}},
			itemTitle:   "Movie",
			itemContext: "",
			want:        "Tagged - Movie",
		},

		// Title display-order: even though canonicalFunctionOrder
		// executes Recover BEFORE Discover BEFORE Tag-RG (so results
		// arrive in that slice order), the title puts the headline
		// action FIRST. Tag-Q-R → Auto-tag → Discover → Recover.
		// Mirrors bash tagarr_import.sh's "Tagged + Discovered +
		// Fixed" output, not the execution order.
		{
			name:  "Display order: results in canonical exec order → title in display order",
			event: core.WebhookEventDownload,
			results: []functionResult{
				// Canonical execution order: Recover first, then
				// Discover, then Tag-RG, then Auto-tag buckets.
				{Function: core.WebhookFnRecover, OK: true, Changed: true},
				{Function: core.WebhookFnDiscover, OK: true, Changed: true},
				{Function: core.WebhookFnTagReleaseGroups, OK: true, Changed: true},
				{Function: core.WebhookFnTagAudio, OK: true, Changed: true},
				{Function: core.WebhookFnTagVideo, OK: true, Changed: true},
			},
			itemTitle:   "Movie",
			itemContext: "2024",
			// Tag-Q-R (10) → Auto-tagged (20, bucket-collapsed) →
			// Discover (30) → Recover (40).
			want: "Tagged + Auto-tagged + Discovered + Fixed release group - Movie (2024)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := composeTitle(tc.event, tc.results, tc.itemTitle, tc.itemContext, nil)
			if got != tc.want {
				t.Errorf("composeTitle() = %q, want %q", got, tc.want)
			}
		})
	}

	// Per-agent function filter (7.4b): when allowedFunctions is
	// non-empty, only results whose Function is in the list contribute
	// to the combo title. Title stays aligned with what the agent
	// will actually see in the body.
	t.Run("filter: Tag+AutoTags results, agent subscribed only to tagAudio → 'Auto-tagged' only", func(t *testing.T) {
		results := []functionResult{
			{Function: core.WebhookFnTagReleaseGroups, OK: true, Changed: true},
			{Function: core.WebhookFnTagAudio, OK: true, Changed: true},
		}
		got := composeTitle(core.WebhookEventDownload, results, "Movie", "2024", []string{"tagAudio"})
		want := "Auto-tagged - Movie (2024)"
		if got != want {
			t.Errorf("composeTitle filtered = %q, want %q", got, want)
		}
	})

	t.Run("filter: no result matches the agent's whitelist → empty title", func(t *testing.T) {
		results := []functionResult{
			{Function: core.WebhookFnTagAudio, OK: true, Changed: true},
		}
		got := composeTitle(core.WebhookEventDownload, results, "Movie", "2024", []string{"grabRename"})
		if got != "" {
			t.Errorf("expected empty title when filter excludes everything, got %q", got)
		}
	})

	t.Run("filter: empty list = no filter (backward compat)", func(t *testing.T) {
		results := []functionResult{
			{Function: core.WebhookFnTagReleaseGroups, OK: true, Changed: true},
		}
		gotNil := composeTitle(core.WebhookEventDownload, results, "Movie", "2024", nil)
		gotEmpty := composeTitle(core.WebhookEventDownload, results, "Movie", "2024", []string{})
		if gotNil != gotEmpty || gotNil != "Tagged - Movie (2024)" {
			t.Errorf("nil and empty-slice filters should be equivalent + match all-functions title; got nil=%q, empty=%q", gotNil, gotEmpty)
		}
	})
}

// TestPickColor exercises the 5-color palette + priority rules.
// File-delete events always get red regardless of co-occurring
// outcomes; other events fall through the priority chain
// (tag > qBit > Discover > Recover).
func TestPickColor(t *testing.T) {
	cases := []struct {
		name    string
		event   core.WebhookConnectEvent
		results []functionResult
		want    int
	}{
		{
			name:    "Tag only → orange",
			event:   core.WebhookEventDownload,
			results: []functionResult{{Function: core.WebhookFnTagReleaseGroups, OK: true, Changed: true}},
			want:    embedColorTagged,
		},
		{
			name:    "Auto-tag only → orange (tag family)",
			event:   core.WebhookEventDownload,
			results: []functionResult{{Function: core.WebhookFnTagAudio, OK: true, Changed: true}},
			want:    embedColorTagged,
		},
		{
			name:    "Discover only → gold",
			event:   core.WebhookEventDownload,
			results: []functionResult{{Function: core.WebhookFnDiscover, OK: true, Changed: true}},
			want:    embedColorDiscover,
		},
		{
			name:    "Recover only → green",
			event:   core.WebhookEventDownload,
			results: []functionResult{{Function: core.WebhookFnRecover, OK: true, Changed: true}},
			want:    embedColorRecover,
		},
		{
			name:    "GrabRename only → blue (qBit-side)",
			event:   core.WebhookEventGrab,
			results: []functionResult{{Function: core.WebhookFnGrabRename, OK: true, Changed: true}},
			want:    embedColorQbitSide,
		},
		{
			name:    "qBit S/E only → blue",
			event:   core.WebhookEventGrab,
			results: []functionResult{{Function: core.WebhookFnQbitSeTag, OK: true, Changed: true}},
			want:    embedColorQbitSide,
		},
		{
			name:    "qBit Category Fix only → blue",
			event:   core.WebhookEventDownload,
			results: []functionResult{{Function: core.WebhookFnQbitCategoryFix, OK: true, Changed: true}},
			want:    embedColorQbitSide,
		},

		// Priority: Tag wins over Discover when both fire.
		{
			name:  "Tag + Discover → orange (tag wins)",
			event: core.WebhookEventDownload,
			results: []functionResult{
				{Function: core.WebhookFnTagReleaseGroups, OK: true, Changed: true},
				{Function: core.WebhookFnDiscover, OK: true, Changed: true},
			},
			want: embedColorTagged,
		},
		// Priority: Tag wins over qBit-side when both fire.
		{
			name:  "Tag + GrabRename → orange (tag wins)",
			event: core.WebhookEventDownload,
			results: []functionResult{
				{Function: core.WebhookFnTagReleaseGroups, OK: true, Changed: true},
				{Function: core.WebhookFnGrabRename, OK: true, Changed: true},
			},
			want: embedColorTagged,
		},
		// Priority: qBit-side wins over Discover when no tag fires.
		{
			name:  "GrabRename + Discover → blue (qBit wins)",
			event: core.WebhookEventGrab,
			results: []functionResult{
				{Function: core.WebhookFnGrabRename, OK: true, Changed: true},
				{Function: core.WebhookFnDiscover, OK: true, Changed: true},
			},
			want: embedColorQbitSide,
		},

		// Delete events override everything → red.
		{
			name:    "MovieFileDelete → red (regardless of results)",
			event:   core.WebhookEventMovieFileDelete,
			results: nil,
			want:    embedColorDelete,
		},
		{
			name:    "EpisodeFileDeleteForUpgrade → red",
			event:   core.WebhookEventEpisodeFileDeleteForUpgrade,
			results: nil,
			want:    embedColorDelete,
		},
		{
			name:  "Delete event with stale Tag result → still red",
			event: core.WebhookEventMovieFileDelete,
			results: []functionResult{
				// Hypothetical: the strip-on-delete adapter reports
				// itself as a Tag-RG result. Delete-event override
				// still wins — the color tells the user "this was a
				// destructive cleanup", which is more important than
				// "tag changed".
				{Function: core.WebhookFnTagReleaseGroups, OK: true, Changed: true},
			},
			want: embedColorDelete,
		},

		// Failed/skipped results don't contribute color.
		{
			name:  "OK=true but Changed=false → safe default (orange)",
			event: core.WebhookEventDownload,
			results: []functionResult{
				{Function: core.WebhookFnTagReleaseGroups, OK: true, Changed: false},
			},
			want: embedColorTagged,
		},
		{
			name:  "Errored results (OK=false) → safe default (orange)",
			event: core.WebhookEventDownload,
			results: []functionResult{
				{Function: core.WebhookFnTagReleaseGroups, OK: false},
			},
			want: embedColorTagged,
		},
		{
			name:    "Empty results, non-delete → safe default (orange)",
			event:   core.WebhookEventDownload,
			results: nil,
			want:    embedColorTagged,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := pickColor(tc.event, tc.results, nil)
			if got != tc.want {
				t.Errorf("pickColor() = %#x, want %#x", got, tc.want)
			}
		})
	}

	// Per-agent function filter (7.4b): color reflects what the
	// agent will actually see, not what the rule fired.
	t.Run("filter: Tag+Discover fires, agent subscribed only to Discover → gold (Discover-only after filter)", func(t *testing.T) {
		results := []functionResult{
			{Function: core.WebhookFnTagReleaseGroups, OK: true, Changed: true},
			{Function: core.WebhookFnDiscover, OK: true, Changed: true},
		}
		// Without filter: Tag > Discover → orange.
		// With Discover-only filter: only Discover remains → gold.
		got := pickColor(core.WebhookEventDownload, results, []string{"discover"})
		if got != embedColorDiscover {
			t.Errorf("filtered to Discover only = %#x, want gold %#x", got, embedColorDiscover)
		}
	})

	t.Run("filter: empty filter = no filter (backward compat)", func(t *testing.T) {
		results := []functionResult{
			{Function: core.WebhookFnTagReleaseGroups, OK: true, Changed: true},
		}
		gotNil := pickColor(core.WebhookEventDownload, results, nil)
		gotEmpty := pickColor(core.WebhookEventDownload, results, []string{})
		if gotNil != gotEmpty || gotNil != embedColorTagged {
			t.Errorf("nil and empty filters should match; nil=%#x, empty=%#x", gotNil, gotEmpty)
		}
	})

	t.Run("filter: delete event still red regardless of function filter", func(t *testing.T) {
		// Delete-event red override happens BEFORE the function
		// filter — the destructive nature is the headline, not the
		// agent's subscription.
		results := []functionResult{
			{Function: core.WebhookFnTagReleaseGroups, OK: true, Changed: true},
		}
		got := pickColor(core.WebhookEventMovieFileDelete, results, []string{"grabRename"})
		if got != embedColorDelete {
			t.Errorf("delete-event color = %#x, want red %#x", got, embedColorDelete)
		}
	})
}

// TestAppendRuleSection verifies the Rule field — replaces the
// pre-2026-05-24 FooterSuffix that put rule name on the footer
// alongside the version string. Rule now has its own embed body
// field; the footer is reserved for "Resolvarr {version} by
// ProphetSe7en" + the locale-aware embed timestamp.
func TestAppendRuleSection(t *testing.T) {
	cases := []struct {
		name string
		rule string
		want []agents.PayloadField
	}{
		{"empty rule → no field", "", nil},
		{"whitespace-only rule → no field", "   ", nil},
		{"normal rule name", "Tag 4K imports", []agents.PayloadField{
			{Name: "Rule", Value: "Tag 4K imports", Inline: false},
		}},
		{"rule name needs trimming", "  Tag 4K imports  ", []agents.PayloadField{
			{Name: "Rule", Value: "Tag 4K imports", Inline: false},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := appendRuleSection(nil, tc.rule)
			fieldsEqual(t, got, tc.want)
		})
	}
}

// TestExtractPosterURL covers the Connect-payload poster lookup —
// both Arr types, the remoteUrl-over-url preference, and the
// empty-body / malformed-JSON / missing-images fail-soft paths. URLs
// are synthetic per the project rule on test fixtures (never copy
// real secrets from user-shared payloads).
func TestExtractPosterURL(t *testing.T) {
	radarrFull := `{
		"eventType": "Download",
		"movie": {
			"id": 1,
			"title": "Test Movie",
			"images": [
				{"coverType": "banner", "remoteUrl": "https://cdn.tmdb.example/banner.jpg", "url": "/MediaCover/1/banner.jpg"},
				{"coverType": "poster", "remoteUrl": "https://cdn.tmdb.example/poster.jpg", "url": "/MediaCover/1/poster.jpg"},
				{"coverType": "fanart", "remoteUrl": "https://cdn.tmdb.example/fanart.jpg", "url": "/MediaCover/1/fanart.jpg"}
			]
		}
	}`
	radarrRemoteOnly := `{
		"movie": {
			"images": [
				{"coverType": "poster", "remoteUrl": "https://cdn.tmdb.example/poster.jpg"}
			]
		}
	}`
	radarrURLOnly := `{
		"movie": {
			"images": [
				{"coverType": "poster", "url": "/MediaCover/1/poster.jpg"}
			]
		}
	}`
	radarrNoPoster := `{
		"movie": {
			"images": [
				{"coverType": "banner", "remoteUrl": "https://cdn.tmdb.example/banner.jpg"}
			]
		}
	}`
	sonarrFull := `{
		"eventType": "Download",
		"series": {
			"id": 5,
			"title": "Test Series",
			"images": [
				{"coverType": "poster", "remoteUrl": "https://cdn.thetvdb.example/poster.jpg", "url": "/MediaCover/5/poster.jpg"}
			]
		}
	}`
	mixedPayloadRadarrLookup := `{
		"movie":  {"images": [{"coverType": "poster", "remoteUrl": "https://cdn.tmdb.example/movie.jpg"}]},
		"series": {"images": [{"coverType": "poster", "remoteUrl": "https://cdn.tmdb.example/series.jpg"}]}
	}`

	cases := []struct {
		name    string
		body    string
		appType string
		want    string
	}{
		{"Radarr — remoteUrl preferred over url", radarrFull, "radarr", "https://cdn.tmdb.example/poster.jpg"},
		{"Radarr — remoteUrl only", radarrRemoteOnly, "radarr", "https://cdn.tmdb.example/poster.jpg"},
		{"Radarr — relative url path rejected by http(s) filter → empty", radarrURLOnly, "radarr", ""},
		{"Radarr — no poster coverType → empty", radarrNoPoster, "radarr", ""},
		{"Sonarr — series images path", sonarrFull, "sonarr", "https://cdn.thetvdb.example/poster.jpg"},
		{"Mixed Radarr + Sonarr payload — radarr appType picks movie", mixedPayloadRadarrLookup, "radarr", "https://cdn.tmdb.example/movie.jpg"},
		{"Mixed Radarr + Sonarr payload — sonarr appType picks series", mixedPayloadRadarrLookup, "sonarr", "https://cdn.tmdb.example/series.jpg"},
		{"appType case-insensitive", radarrFull, "RADARR", "https://cdn.tmdb.example/poster.jpg"},
		{"appType with whitespace", radarrFull, "  radarr  ", "https://cdn.tmdb.example/poster.jpg"},
		{"Sonarr appType against Radarr payload → empty (no series)", radarrFull, "sonarr", ""},
		{"Unknown appType → empty", radarrFull, "lidarr", ""},
		{"Empty appType → empty", radarrFull, "", ""},
		{"Empty body → empty", "", "radarr", ""},
		{"Malformed JSON → empty", `{not json`, "radarr", ""},
		{"Missing images array → empty", `{"movie": {"id": 1}}`, "radarr", ""},
		{"Empty images array → empty", `{"movie": {"images": []}}`, "radarr", ""},

		// Security/correctness filter: only http:// and https:// URLs
		// reach the embed. javascript:/data:/file:/relative paths all
		// return "" so the embed thumbnail is omitted cleanly.
		{"javascript: scheme rejected → empty", `{"movie": {"images": [{"coverType": "poster", "remoteUrl": "javascript:alert(1)"}]}}`, "radarr", ""},
		{"data: scheme rejected → empty", `{"movie": {"images": [{"coverType": "poster", "remoteUrl": "data:image/png;base64,iVBORw0KG"}]}}`, "radarr", ""},
		{"file: scheme rejected → empty", `{"movie": {"images": [{"coverType": "poster", "remoteUrl": "file:///tmp/poster.jpg"}]}}`, "radarr", ""},
		{"http:// (no s) accepted — LAN Arr fallback", `{"movie": {"images": [{"coverType": "poster", "url": "http://10.0.0.5/MediaCover/1/poster.jpg"}]}}`, "radarr", "http://10.0.0.5/MediaCover/1/poster.jpg"},

		// Coverage gaps flagged in review of f2008540:
		{"case-insensitive coverType: 'Poster' (capitalised) accepted", `{"movie": {"images": [{"coverType": "Poster", "remoteUrl": "https://cdn.tmdb.example/poster.jpg"}]}}`, "radarr", "https://cdn.tmdb.example/poster.jpg"},
		{"first poster wins — returns first match, ignores later entries", `{"movie": {"images": [
			{"coverType": "poster", "remoteUrl": "https://cdn.tmdb.example/first.jpg"},
			{"coverType": "poster", "remoteUrl": "https://cdn.tmdb.example/second.jpg"}
		]}}`, "radarr", "https://cdn.tmdb.example/first.jpg"},
		{"whitespace-only remoteUrl falls back to url", `{"movie": {"images": [{"coverType": "poster", "remoteUrl": "   ", "url": "https://cdn.tmdb.example/fallback.jpg"}]}}`, "radarr", "https://cdn.tmdb.example/fallback.jpg"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractPosterURL([]byte(tc.body), tc.appType)
			if got != tc.want {
				t.Errorf("extractPosterURL() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestIsDeleteEvent locks the four delete-event variants. If a future
// Connect-event enum adds (e.g.) MovieFileDeleteSeasonPack, this test
// will surface that we need to update isDeleteEvent + the title
// synthesiser + pickColor + section builders.
func TestIsDeleteEvent(t *testing.T) {
	deleteEvents := []core.WebhookConnectEvent{
		core.WebhookEventMovieFileDelete,
		core.WebhookEventMovieFileDeleteForUpgrade,
		core.WebhookEventEpisodeFileDelete,
		core.WebhookEventEpisodeFileDeleteForUpgrade,
	}
	for _, ev := range deleteEvents {
		if !isDeleteEvent(ev) {
			t.Errorf("isDeleteEvent(%q) = false, want true", ev)
		}
	}
	nonDelete := []core.WebhookConnectEvent{
		core.WebhookEventGrab,
		core.WebhookEventDownload,
		"Test",         // Radarr/Sonarr test event — not a real fire
		"SomethingNew", // future event we don't know about yet
	}
	for _, ev := range nonDelete {
		if isDeleteEvent(ev) {
			t.Errorf("isDeleteEvent(%q) = true, want false", ev)
		}
	}
}

// TestFunctionResultDetailNilSafe verifies the contract that section
// builders (task #5) can rely on: nil Detail is allowed for legacy /
// unwired call paths. The field's any type makes nil-vs-untyped-nil
// a real concern; this test pins that an unset Detail compares equal
// to nil and a type-assertion fails cleanly rather than panicking.
func TestFunctionResultDetailNilSafe(t *testing.T) {
	r := functionResult{Function: core.WebhookFnTagAudio, OK: true, Changed: true}

	if r.Detail != nil {
		t.Errorf("zero-value Detail = %v, want nil", r.Detail)
	}

	// Type-assert to a concrete struct — must fail cleanly, not panic.
	if _, ok := r.Detail.(AudioDetail); ok {
		t.Errorf("nil Detail should not type-assert to AudioDetail")
	}

	// Populated Detail round-trips.
	r.Detail = AudioDetail{Added: []string{"audio-truehd-71"}, PlainSummary: "TrueHD 7.1"}
	d, ok := r.Detail.(AudioDetail)
	if !ok {
		t.Fatalf("populated Detail should type-assert to AudioDetail")
	}
	if d.PlainSummary != "TrueHD 7.1" {
		t.Errorf("AudioDetail.PlainSummary = %q, want TrueHD 7.1", d.PlainSummary)
	}

	// Mismatched type-assert must fail cleanly (ok=false), not panic.
	// Builders rely on this to skip rendering a section when an
	// adapter populated the wrong Detail type for the Function — a
	// programming error we want to surface as "no embed section"
	// rather than a runtime crash on every fire.
	if _, ok := r.Detail.(TagDetail); ok {
		t.Errorf("AudioDetail Detail should NOT type-assert to TagDetail")
	}
	if _, ok := r.Detail.(GrabRenameDetail); ok {
		t.Errorf("AudioDetail Detail should NOT type-assert to GrabRenameDetail")
	}
}
