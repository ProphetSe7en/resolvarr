package api

import (
	"testing"

	"resolvarr/internal/core"
	"resolvarr/internal/core/agents"
)

// fieldsEqual is a small helper for field-by-field slice comparison
// — the standard library reflect.DeepEqual would work too, but
// failure messages are easier to read with explicit per-field
// comparisons.
func fieldsEqual(t *testing.T, got, want []agents.PayloadField) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("field count = %d, want %d\n  got:  %+v\n  want: %+v", len(got), len(want), got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("field[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestAppendTagSection covers Tag-RG / filter-only outcomes.
func TestAppendTagSection(t *testing.T) {
	cases := []struct {
		name string
		d    *TagDetail
		want []agents.PayloadField
	}{
		{"nil → no fields", nil, nil},
		{"empty Added → no fields (Tagged-in moved to universal lead)", &TagDetail{Tag: "FLUX"}, nil},
		{"added tag only → just Quality tag", &TagDetail{Added: []string{"FLUX"}}, []agents.PayloadField{
			{Name: "Quality tag", Value: "FLUX", Inline: true},
		}},
		{"mirrored/secondary fields are no-op here (Tagged-in lives in composeFields)", &TagDetail{Added: []string{"FLUX"}, Mirrored: true, SecondaryName: "Radarr 4K"}, []agents.PayloadField{
			{Name: "Quality tag", Value: "FLUX", Inline: true},
		}},
		{"multiple added tags joined with separator", &TagDetail{Added: []string{"FLUX", "SiC"}}, []agents.PayloadField{
			{Name: "Quality tag", Value: "FLUX · SiC", Inline: true},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := appendTagSection(nil, tc.d)
			fieldsEqual(t, got, tc.want)
		})
	}
}

// TestAppendAutoTagsSection covers the bundled Audio + Video + DV
// section: each bucket renders independently, all three skip on
// empty PlainSummary.
func TestAppendAutoTagsSection(t *testing.T) {
	cases := []struct {
		name        string
		a           *AudioDetail
		v           *VideoDetail
		dv          *DvDetail
		want        []agents.PayloadField
	}{
		{"all nil → no fields", nil, nil, nil, nil},
		{"audio only", &AudioDetail{PlainSummary: "TrueHD Atmos 7.1"}, nil, nil, []agents.PayloadField{
			{Name: "Audio", Value: "TrueHD Atmos 7.1", Inline: true},
		}},
		{"video only", nil, &VideoDetail{PlainSummary: "4K · HDR"}, nil, []agents.PayloadField{
			{Name: "Video", Value: "4K · HDR", Inline: true},
		}},
		{"dv only", nil, nil, &DvDetail{PlainSummary: "Profile 7 · Layer 7.1"}, []agents.PayloadField{
			{Name: "Dolby Vision", Value: "Profile 7 · Layer 7.1", Inline: true},
		}},
		{"all three bundled in order", &AudioDetail{PlainSummary: "TrueHD Atmos 7.1"}, &VideoDetail{PlainSummary: "4K · HDR"}, &DvDetail{PlainSummary: "Profile 7"}, []agents.PayloadField{
			{Name: "Audio", Value: "TrueHD Atmos 7.1", Inline: true},
			{Name: "Video", Value: "4K · HDR", Inline: true},
			{Name: "Dolby Vision", Value: "Profile 7", Inline: true},
		}},
		{"empty PlainSummary skipped per-bucket", &AudioDetail{PlainSummary: ""}, &VideoDetail{PlainSummary: "4K"}, &DvDetail{PlainSummary: "  "}, []agents.PayloadField{
			{Name: "Video", Value: "4K", Inline: true},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := appendAutoTagsSection(nil, tc.a, tc.v, tc.dv)
			fieldsEqual(t, got, tc.want)
		})
	}
}

// TestAppendDiscoverSection covers Discover outcomes.
func TestAppendDiscoverSection(t *testing.T) {
	cases := []struct {
		name string
		d    *DiscoverDetail
		want []agents.PayloadField
	}{
		{"nil → no fields", nil, nil},
		{"empty group name → no fields", &DiscoverDetail{NewGroup: ""}, nil},
		{"manual-review default (AutoEnabled=false) → group only", &DiscoverDetail{NewGroup: "FLUX"}, []agents.PayloadField{
			{Name: "New group", Value: "FLUX", Inline: true},
		}},
		// AutoEnabled is preserved on the Detail for History
		// debugging but no longer surfaces in the embed — dropped
		// per the "only actual changes" rule (a freshly-active
		// group will surface naturally on its next tag-fire).
		{"AutoEnabled=true does NOT add a field", &DiscoverDetail{NewGroup: "FLUX", AutoEnabled: true}, []agents.PayloadField{
			{Name: "New group", Value: "FLUX", Inline: true},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := appendDiscoverSection(nil, tc.d)
			fieldsEqual(t, got, tc.want)
		})
	}
}

// TestAppendRecoverSection covers Recover outcomes.
func TestAppendRecoverSection(t *testing.T) {
	cases := []struct {
		name string
		d    *RecoverDetail
		want []agents.PayloadField
	}{
		{"nil → no fields", nil, nil},
		{"empty group → no fields", &RecoverDetail{RecoveredGroup: ""}, nil},
		{"group only — source absent", &RecoverDetail{RecoveredGroup: "FLUX"}, []agents.PayloadField{
			{Name: "Recovered", Value: "FLUX", Inline: true},
		}},
		{"group + source", &RecoverDetail{RecoveredGroup: "FLUX", Source: "grab history"}, []agents.PayloadField{
			{Name: "Recovered", Value: "FLUX", Inline: true},
			{Name: "Source", Value: "grab history", Inline: true},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := appendRecoverSection(nil, tc.d)
			fieldsEqual(t, got, tc.want)
		})
	}
}

// TestAppendGrabRenameSection covers the qBit torrent-rename
// section. Mirrors bash tagarr_import.sh:452-489's "Torrent Name /
// Restored to Release Name" field shape (post-2026-05-24 rewrite —
// dropped the earlier compact "Was / Now" naming).
func TestAppendGrabRenameSection(t *testing.T) {
	cases := []struct {
		name string
		d    *GrabRenameDetail
		want []agents.PayloadField
	}{
		{"nil → no fields", nil, nil},
		{"both From and To empty → no fields", &GrabRenameDetail{Triggers: []string{"x"}}, nil},
		{"full rename with everything populated", &GrabRenameDetail{
			From:            "Dune.Part.Two.2024.2160p.WEB-DL.DV.HDR",
			To:              "Dune.Part.Two.2024.2160p.WEB-DL.DV.HDR-FLUX",
			Triggers:        []string{"missing-release-group …", "scene-stripped …"},
			GroupRecovered:  "FLUX",
			TokensRecovered: []string{"Director's Cut", "IMAX"},
			QbitInstance:    "qBit Movies",
			SceneCFChanged:  true,
		}, []agents.PayloadField{
			{Name: "Renamed in", Value: "qBit Movies", Inline: false},
			{Name: "Release Group Recovered", Value: "FLUX", Inline: true},
			{Name: "Tokens Recovered", Value: "Director's Cut · IMAX", Inline: true},
			{Name: "⚠ Scene CF", Value: "No longer matches after rename", Inline: false},
			{Name: "Torrent Name", Value: "Dune.Part.Two.2024.2160p.WEB-DL.DV.HDR", Inline: false},
			{Name: "Restored to Release Name", Value: "Dune.Part.Two.2024.2160p.WEB-DL.DV.HDR-FLUX", Inline: false},
		}},
		{"minimal — only To set", &GrabRenameDetail{
			To: "movie.2024.web-dl.x264-XEBEC",
		}, []agents.PayloadField{
			{Name: "Restored to Release Name", Value: "movie.2024.web-dl.x264-XEBEC", Inline: false},
		}},
		{"scene CF warning omitted when flag false", &GrabRenameDetail{
			From: "a", To: "b", QbitInstance: "qBit", SceneCFChanged: false,
		}, []agents.PayloadField{
			{Name: "Renamed in", Value: "qBit", Inline: false},
			{Name: "Torrent Name", Value: "a", Inline: false},
			{Name: "Restored to Release Name", Value: "b", Inline: false},
		}},
		{"only release-group recovered, no other tokens", &GrabRenameDetail{
			From: "a", To: "b", QbitInstance: "qBit", GroupRecovered: "FLUX",
		}, []agents.PayloadField{
			{Name: "Renamed in", Value: "qBit", Inline: false},
			{Name: "Release Group Recovered", Value: "FLUX", Inline: true},
			{Name: "Torrent Name", Value: "a", Inline: false},
			{Name: "Restored to Release Name", Value: "b", Inline: false},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := appendGrabRenameSection(nil, tc.d)
			fieldsEqual(t, got, tc.want)
		})
	}
}

// TestAppendQbitSeSection covers the qBit S/E classification.
func TestAppendQbitSeSection(t *testing.T) {
	cases := []struct {
		name string
		d    *QbitSeDetail
		want []agents.PayloadField
	}{
		{"nil → no fields", nil, nil},
		{"empty tag → no fields", &QbitSeDetail{Tag: "", Classification: "Episode"}, nil},
		{"tag only", &QbitSeDetail{Tag: "Episode"}, []agents.PayloadField{
			{Name: "Tag", Value: "Episode", Inline: true},
		}},
		{"tag + matching classification → Type field elided", &QbitSeDetail{Tag: "Episode", Classification: "Episode"}, []agents.PayloadField{
			{Name: "Tag", Value: "Episode", Inline: true},
		}},
		{"tag + diverging classification → both surface", &QbitSeDetail{Tag: "Season", Classification: "Season pack", QbitInstance: "qBit TV"}, []agents.PayloadField{
			{Name: "Tag", Value: "Season", Inline: true},
			{Name: "Type", Value: "Season pack", Inline: true},
			{Name: "Client", Value: "qBit TV", Inline: true},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := appendQbitSeSection(nil, tc.d)
			fieldsEqual(t, got, tc.want)
		})
	}
}

// TestAppendQbitCategoryFixSection covers the category-swap section.
func TestAppendQbitCategoryFixSection(t *testing.T) {
	cases := []struct {
		name string
		d    *QbitCategoryFixDetail
		want []agents.PayloadField
	}{
		{"nil → no fields", nil, nil},
		{"both cats empty → no fields", &QbitCategoryFixDetail{QbitInstance: "qBit"}, nil},
		{"full swap", &QbitCategoryFixDetail{PreCat: "movies", PostCat: "movies-imp", QbitInstance: "qBit Movies"}, []agents.PayloadField{
			{Name: "Was in", Value: "movies", Inline: true},
			{Name: "Moved to", Value: "movies-imp", Inline: true},
			{Name: "Client", Value: "qBit Movies", Inline: true},
		}},
		{"SkipReason ignored (no-op detail never reaches builder per orchestrator gate)", &QbitCategoryFixDetail{
			PreCat: "movies", PostCat: "movies-imp", SkipReason: "Arr did its job",
		}, []agents.PayloadField{
			{Name: "Was in", Value: "movies", Inline: true},
			{Name: "Moved to", Value: "movies-imp", Inline: true},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := appendQbitCategoryFixSection(nil, tc.d)
			fieldsEqual(t, got, tc.want)
		})
	}
}

// TestAppendFileDeleteSection covers the bundled per-bucket strip +
// Tag-RG strip section. Order is locked at "Audio · Video · DV ·
// Quality tag" regardless of input map order (maps are unordered).
func TestAppendFileDeleteSection(t *testing.T) {
	cases := []struct {
		name string
		d    *FileDeleteDetail
		want []agents.PayloadField
	}{
		{"nil → no fields", nil, nil},
		{"nothing to strip → no fields", &FileDeleteDetail{}, nil},
		{"single bucket — audio, no instance name → fallback placeholder", &FileDeleteDetail{
			PerBucket: map[string][]string{"audio": {"audio-truehd-71"}},
		}, []agents.PayloadField{
			{Name: "Cleaned in", Value: "primary Arr", Inline: false},
			{Name: "Removed", Value: "Audio", Inline: false},
		}},
		{"multi-bucket + Tag-RG → stable order, real primary name", &FileDeleteDetail{
			// Map order is randomised by Go's iteration but the
			// builder enforces "Audio · Video · DV · Quality tag".
			PerBucket: map[string][]string{
				"video": {"video-4k"},
				"audio": {"audio-71"},
				"dv":    {"dv-p7"},
			},
			TagRgRemoved: "FLUX",
			Primary:      "Radarr Main",
		}, []agents.PayloadField{
			{Name: "Cleaned in", Value: "Radarr Main", Inline: false},
			{Name: "Removed", Value: "Audio · Video · DV · Quality tag", Inline: false},
		}},
		{"mirrored to secondary folds into 'Cleaned in' instance list", &FileDeleteDetail{
			PerBucket:         map[string][]string{"audio": {"x"}},
			Primary:           "Radarr Main",
			MirroredSecondary: true,
			SecondaryName:     "Radarr 4K",
		}, []agents.PayloadField{
			{Name: "Cleaned in", Value: "Radarr Main · Radarr 4K", Inline: false},
			{Name: "Removed", Value: "Audio", Inline: false},
		}},
		{"empty bucket arrays (no actual strip) → that bucket skipped", &FileDeleteDetail{
			PerBucket: map[string][]string{
				"audio": {}, // bucket present but empty
				"video": {"video-4k"},
			},
		}, []agents.PayloadField{
			{Name: "Cleaned in", Value: "primary Arr", Inline: false},
			{Name: "Removed", Value: "Video", Inline: false},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := appendFileDeleteSection(nil, tc.d)
			fieldsEqual(t, got, tc.want)
		})
	}
}

// TestComposeFields covers the orchestrator end-to-end on
// representative event-type fires. Verifies (1) only Changed=true
// results contribute, (2) Detail type-asserts dispatch to the right
// section, (3) sections appear in user-scan order, (4) delete events
// take the dedicated File-Delete path and skip every other section.
func TestComposeFields(t *testing.T) {
	t.Run("Download — Tag + Auto-tags bundle with real instance names", func(t *testing.T) {
		results := []functionResult{
			{Function: core.WebhookFnTagReleaseGroups, OK: true, Changed: true,
				Detail: TagDetail{Added: []string{"FLUX"}, Primary: "Radarr Main", Mirrored: true, SecondaryName: "Radarr 4K"}},
			{Function: core.WebhookFnTagAudio, OK: true, Changed: true,
				Detail: AudioDetail{PlainSummary: "TrueHD Atmos 7.1"}},
			{Function: core.WebhookFnTagVideo, OK: true, Changed: true,
				Detail: VideoDetail{PlainSummary: "4K · HDR"}},
		}
		want := []agents.PayloadField{
			{Name: "Tagged in", Value: "Radarr Main · Radarr 4K", Inline: false},
			{Name: "Quality tag", Value: "FLUX", Inline: true},
			{Name: "Audio", Value: "TrueHD Atmos 7.1", Inline: true},
			{Name: "Video", Value: "4K · HDR", Inline: true},
			{Name: "Event", Value: "Import", Inline: true},
		}
		got := composeFields(core.WebhookEventDownload, results, nil, "", "", "")
		fieldsEqual(t, got, want)
	})

	t.Run("Download — Changed=false results contribute nothing", func(t *testing.T) {
		results := []functionResult{
			{Function: core.WebhookFnTagReleaseGroups, OK: true, Changed: false,
				Detail: TagDetail{Added: []string{"FLUX"}}}, // populated but Changed=false
			{Function: core.WebhookFnTagAudio, OK: true, Changed: true,
				Detail: AudioDetail{PlainSummary: "TrueHD Atmos 7.1"}},
		}
		want := []agents.PayloadField{
			{Name: "Audio", Value: "TrueHD Atmos 7.1", Inline: true},
			{Name: "Event", Value: "Import", Inline: true},
		}
		got := composeFields(core.WebhookEventDownload, results, nil, "", "", "")
		fieldsEqual(t, got, want)
	})

	t.Run("Grab — GrabRename + qBit S/E (multi-section)", func(t *testing.T) {
		results := []functionResult{
			{Function: core.WebhookFnGrabRename, OK: true, Changed: true,
				Detail: GrabRenameDetail{From: "old.name", To: "new.name-FLUX", QbitInstance: "qBit"}},
			{Function: core.WebhookFnQbitSeTag, OK: true, Changed: true,
				Detail: QbitSeDetail{Tag: "Episode", QbitInstance: "qBit"}},
		}
		want := []agents.PayloadField{
			{Name: "Renamed in", Value: "qBit", Inline: false},
			{Name: "Torrent Name", Value: "old.name", Inline: false},
			{Name: "Restored to Release Name", Value: "new.name-FLUX", Inline: false},
			{Name: "Tag", Value: "Episode", Inline: true},
			{Name: "Client", Value: "qBit", Inline: true},
			{Name: "Event", Value: "Grab", Inline: true},
		}
		got := composeFields(core.WebhookEventGrab, results, nil, "", "", "")
		fieldsEqual(t, got, want)
	})

	// Mirror-only edge case (7.5a fix): primary had nothing to strip
	// but secondary mirror still had a stale managed tag. The
	// dispatchAutoStripTagRgOnDelete adapter promotes mirror.Changed
	// to res.Changed and populates FileDeleteDetail with TagRgRemoved
	// from the mirror's strip. composeFields then renders a complete
	// "Cleaned in: primary · secondary, Removed: Quality tag" embed
	// — bash tagarr_import.sh would have silently dropped this fire.
	t.Run("MovieFileDelete — mirror-only strip (primary already clean) renders", func(t *testing.T) {
		results := []functionResult{
			// dispatchAutoStripTagRgOnDelete: primary Changed=false
			// would normally produce no result; the 7.5a fix uses
			// the mirror's strip data to flip Changed=true and
			// populate Detail. This is what composeFields sees:
			{Function: webhookFnAutoStripTagRgOnDelete, OK: true, Changed: true,
				Detail: FileDeleteDetail{
					TagRgRemoved:      "FLUX",
					Primary:           "Radarr Main",
					MirroredSecondary: true,
					SecondaryName:     "Radarr 4K",
				}},
		}
		want := []agents.PayloadField{
			{Name: "Cleaned in", Value: "Radarr Main · Radarr 4K", Inline: false},
			{Name: "Removed", Value: "Quality tag", Inline: false},
			{Name: "Event", Value: "File deleted", Inline: true},
		}
		got := composeFields(core.WebhookEventMovieFileDelete, results, nil, "", "", "")
		fieldsEqual(t, got, want)
	})

	// composeFields merges multiple FileDeleteDetail attachments on
	// the same event — the per-bucket strip dispatcher
	// (dispatchFileDeleteCleanup) and the auto-strip-Tag-RG dispatcher
	// each emit their own partial Detail covering their scope.
	// Section reads as one consolidated "Cleaned in / Removed" pair.
	t.Run("MovieFileDelete — two FileDeleteDetail attachments merge into one section", func(t *testing.T) {
		results := []functionResult{
			// dispatchFileDeleteCleanup contributes per-bucket data.
			{Function: core.WebhookFnFileDeleteClean, OK: true, Changed: true,
				Detail: FileDeleteDetail{
					PerBucket: map[string][]string{"audio": {"audio-71"}, "video": {"video-4k"}},
					Primary:   "Radarr Main",
				}},
			// dispatchAutoStripTagRgOnDelete contributes Tag-RG +
			// secondary mirror data.
			{Function: webhookFnAutoStripTagRgOnDelete, OK: true, Changed: true,
				Detail: FileDeleteDetail{
					TagRgRemoved:      "FLUX",
					Primary:           "Radarr Main",
					MirroredSecondary: true,
					SecondaryName:     "Radarr 4K",
				}},
		}
		want := []agents.PayloadField{
			{Name: "Cleaned in", Value: "Radarr Main · Radarr 4K", Inline: false},
			{Name: "Removed", Value: "Audio · Video · Quality tag", Inline: false},
			{Name: "Event", Value: "File deleted", Inline: true},
		}
		got := composeFields(core.WebhookEventMovieFileDelete, results, nil, "", "", "")
		fieldsEqual(t, got, want)
	})

	t.Run("MovieFileDelete — only File-Delete section surfaces", func(t *testing.T) {
		// A delete event firing with a FileDeleteDetail (the dispatcher
		// will attach this via any strip-on-delete result). Section
		// builders for non-delete families are skipped entirely.
		results := []functionResult{
			{Function: core.WebhookFnTagReleaseGroups, OK: true, Changed: true,
				Detail: FileDeleteDetail{
					PerBucket:    map[string][]string{"audio": {"x"}, "video": {"y"}},
					TagRgRemoved: "FLUX",
					Primary:      "Radarr Main",
				}},
		}
		want := []agents.PayloadField{
			{Name: "Cleaned in", Value: "Radarr Main", Inline: false},
			{Name: "Removed", Value: "Audio · Video · Quality tag", Inline: false},
			{Name: "Event", Value: "File deleted", Inline: true},
		}
		got := composeFields(core.WebhookEventMovieFileDelete, results, nil, "", "", "")
		fieldsEqual(t, got, want)
	})

	t.Run("MovieFileDelete — nothing stripped → empty", func(t *testing.T) {
		results := []functionResult{}
		got := composeFields(core.WebhookEventMovieFileDelete, results, nil, "", "", "")
		if len(got) != 0 {
			t.Errorf("expected empty fields slice, got %+v", got)
		}
	})

	t.Run("Download — mismatched Detail type silently degrades", func(t *testing.T) {
		// Adapter populated wrong Detail for the Function — the
		// type-assert in composeFields fails cleanly + the section
		// is omitted rather than panicking. This is the
		// "fail-soft on programming error" path locked by
		// TestFunctionResultDetailNilSafe in the framework tests.
		results := []functionResult{
			{Function: core.WebhookFnTagAudio, OK: true, Changed: true,
				Detail: TagDetail{Added: []string{"FLUX"}}}, // wrong type for the function
			{Function: core.WebhookFnTagVideo, OK: true, Changed: true,
				Detail: VideoDetail{PlainSummary: "1080p"}},
		}
		want := []agents.PayloadField{
			{Name: "Video", Value: "1080p", Inline: true},
			{Name: "Event", Value: "Import", Inline: true},
		}
		got := composeFields(core.WebhookEventDownload, results, nil, "", "", "")
		fieldsEqual(t, got, want)
	})

	t.Run("Empty results → empty fields", func(t *testing.T) {
		got := composeFields(core.WebhookEventDownload, nil, nil, "", "", "")
		if len(got) != 0 {
			t.Errorf("expected empty fields, got %+v", got)
		}
	})

	// Full-bundle test: every non-delete function fires on the same
	// Download event. Locks (1) section-order against future re-
	// orderings and (2) field-count budget (this maxes out at the
	// realistic upper bound — 4 sections × 2-5 fields each).
	t.Run("Download — full bundle (every non-delete function fires)", func(t *testing.T) {
		results := []functionResult{
			{Function: core.WebhookFnTagReleaseGroups, OK: true, Changed: true,
				Detail: TagDetail{Added: []string{"FLUX"}, Primary: "Radarr Main", Mirrored: true, SecondaryName: "Radarr 4K"}},
			{Function: core.WebhookFnTagAudio, OK: true, Changed: true,
				Detail: AudioDetail{PlainSummary: "TrueHD Atmos 7.1"}},
			{Function: core.WebhookFnTagVideo, OK: true, Changed: true,
				Detail: VideoDetail{PlainSummary: "4K · HDR"}},
			{Function: core.WebhookFnTagDvDetail, OK: true, Changed: true,
				Detail: DvDetail{PlainSummary: "Profile 7"}},
			{Function: core.WebhookFnDiscover, OK: true, Changed: true,
				Detail: DiscoverDetail{NewGroup: "SiC"}},
			{Function: core.WebhookFnRecover, OK: true, Changed: true,
				Detail: RecoverDetail{RecoveredGroup: "FLUX", Source: "grab history"}},
			{Function: core.WebhookFnQbitCategoryFix, OK: true, Changed: true,
				Detail: QbitCategoryFixDetail{PreCat: "movies", PostCat: "movies-imp", QbitInstance: "qBit Movies"}},
		}
		want := []agents.PayloadField{
			// Tag section
			{Name: "Tagged in", Value: "Radarr Main · Radarr 4K", Inline: false},
			{Name: "Quality tag", Value: "FLUX", Inline: true},
			// Auto-tags bundle (Sound · Picture · Dolby Vision)
			{Name: "Audio", Value: "TrueHD Atmos 7.1", Inline: true},
			{Name: "Video", Value: "4K · HDR", Inline: true},
			{Name: "Dolby Vision", Value: "Profile 7", Inline: true},
			// Discover
			{Name: "New group", Value: "SiC", Inline: true},
			// Recover
			{Name: "Recovered", Value: "FLUX", Inline: true},
			{Name: "Source", Value: "grab history", Inline: true},
			// qBit Category Fix (no GrabRename / qBit S/E on Import)
			{Name: "Was in", Value: "movies", Inline: true},
			{Name: "Moved to", Value: "movies-imp", Inline: true},
			{Name: "Client", Value: "qBit Movies", Inline: true},
			{Name: "Event", Value: "Import", Inline: true},
		}
		got := composeFields(core.WebhookEventDownload, results, nil, "", "", "")
		fieldsEqual(t, got, want)
	})

	// Sync → Tag folding: when both Tag-RG and SyncToSecondary fire
	// with Changed=true, composeFields post-processes the typed
	// pointers and folds SyncDetail.SecondaryName into TagDetail.
	// Mirrored + SecondaryName so the Tag section reads "Tagged in:
	// primary · secondary" as one line (no separate Sync field).
	t.Run("Download — Sync result folds SecondaryName into TagDetail", func(t *testing.T) {
		results := []functionResult{
			{Function: core.WebhookFnTagReleaseGroups, OK: true, Changed: true,
				Detail: TagDetail{Added: []string{"FLUX"}, Primary: "Radarr Main"}},
			{Function: core.WebhookFnSyncToSecondary, OK: true, Changed: true,
				Detail: SyncDetail{SecondaryName: "Radarr 4K"}},
		}
		want := []agents.PayloadField{
			{Name: "Tagged in", Value: "Radarr Main · Radarr 4K", Inline: false},
			{Name: "Quality tag", Value: "FLUX", Inline: true},
			{Name: "Event", Value: "Import", Inline: true},
		}
		got := composeFields(core.WebhookEventDownload, results, nil, "", "", "")
		fieldsEqual(t, got, want)
	})

	t.Run("Download — Sync fires alone (no Tag) → no fold + no Tag section", func(t *testing.T) {
		// Sync with Changed=true but no Tag-RG sibling. Sync has no
		// embed section of its own; composeFields produces no fields.
		// (Real-world this can't happen — Sync validator requires
		// Tag-RG on the rule — but defence-in-depth: orchestrator
		// must not panic on a lone Sync result.)
		results := []functionResult{
			{Function: core.WebhookFnSyncToSecondary, OK: true, Changed: true,
				Detail: SyncDetail{SecondaryName: "Radarr 4K"}},
		}
		got := composeFields(core.WebhookEventDownload, results, nil, "", "", "")
		if len(got) != 0 {
			t.Errorf("expected empty fields for orphan Sync, got %+v", got)
		}
	})

	t.Run("Download — Sync Changed=true but mismatched Detail type → no fold (defensive)", func(t *testing.T) {
		// Defence-in-depth: if a future adapter mistakenly attaches
		// the wrong Detail to a Sync result, composeFields must NOT
		// produce Mirrored=true with empty SecondaryName (which would
		// render the fallback "primary · secondary" string). Polish
		// from e66a1c31 moved the Mirrored=true assignment INSIDE
		// the type-assert branch — this test locks that ordering.
		results := []functionResult{
			{Function: core.WebhookFnTagReleaseGroups, OK: true, Changed: true,
				Detail: TagDetail{Added: []string{"FLUX"}, Primary: "Radarr Main"}},
			{Function: core.WebhookFnSyncToSecondary, OK: true, Changed: true,
				Detail: TagDetail{}}, // wrong type for the function
		}
		want := []agents.PayloadField{
			{Name: "Tagged in", Value: "Radarr Main", Inline: false},
			{Name: "Quality tag", Value: "FLUX", Inline: true},
			{Name: "Event", Value: "Import", Inline: true},
		}
		got := composeFields(core.WebhookEventDownload, results, nil, "", "", "")
		fieldsEqual(t, got, want)
	})

	t.Run("Download — Sync fired but Changed=false → no fold", func(t *testing.T) {
		// Sync ran but didn't actually mirror anything (secondary
		// already in desired state). TagDetail's Mirrored stays
		// false → section reads "Tagged in: Radarr Main" only.
		results := []functionResult{
			{Function: core.WebhookFnTagReleaseGroups, OK: true, Changed: true,
				Detail: TagDetail{Added: []string{"FLUX"}, Primary: "Radarr Main"}},
			{Function: core.WebhookFnSyncToSecondary, OK: true, Changed: false,
				Detail: SyncDetail{SecondaryName: "Radarr 4K"}},
		}
		want := []agents.PayloadField{
			{Name: "Tagged in", Value: "Radarr Main", Inline: false},
			{Name: "Quality tag", Value: "FLUX", Inline: true},
			{Name: "Event", Value: "Import", Inline: true},
		}
		got := composeFields(core.WebhookEventDownload, results, nil, "", "", "")
		fieldsEqual(t, got, want)
	})

	// Nil-Detail (not wrong type — actually nil): the orchestrator's
	// type-assert short-circuits at `Detail.(TagDetail)` because nil
	// satisfies no concrete type assertion. Function family stays
	// nil → section omitted. Different code path from wrong-type
	// assert (which the "mismatched Detail" subtest covers).
	t.Run("Download — nil Detail value (not wrong type) → section omitted", func(t *testing.T) {
		results := []functionResult{
			{Function: core.WebhookFnTagReleaseGroups, OK: true, Changed: true, Detail: nil},
			{Function: core.WebhookFnTagAudio, OK: true, Changed: true,
				Detail: AudioDetail{PlainSummary: "TrueHD 7.1"}},
		}
		want := []agents.PayloadField{
			{Name: "Audio", Value: "TrueHD 7.1", Inline: true},
			{Name: "Event", Value: "Import", Inline: true},
		}
		got := composeFields(core.WebhookEventDownload, results, nil, "", "", "")
		fieldsEqual(t, got, want)
	})

	// Per-agent function filter (7.4b) — when allowedFunctions is
	// non-empty, only results whose Function is in the list contribute
	// to the embed. Each agent gets a tailored payload.
	t.Run("Download — agent subscribed to tagAudio only → other sections filtered out", func(t *testing.T) {
		results := []functionResult{
			{Function: core.WebhookFnTagReleaseGroups, OK: true, Changed: true,
				Detail: TagDetail{Added: []string{"FLUX"}, Primary: "Radarr Main"}},
			{Function: core.WebhookFnTagAudio, OK: true, Changed: true,
				Detail: AudioDetail{PlainSummary: "TrueHD Atmos 7.1"}},
			{Function: core.WebhookFnTagVideo, OK: true, Changed: true,
				Detail: VideoDetail{PlainSummary: "4K · HDR"}},
		}
		// Agent subscribed ONLY to tagAudio. Tag-RG + tagVideo filtered
		// out → Tag section and Picture line both vanish; only Sound
		// renders.
		want := []agents.PayloadField{
			{Name: "Audio", Value: "TrueHD Atmos 7.1", Inline: true},
			{Name: "Event", Value: "Import", Inline: true},
		}
		got := composeFields(core.WebhookEventDownload, results, []string{"tagAudio"}, "", "", "")
		fieldsEqual(t, got, want)
	})

	t.Run("Download — agent subscribed to all relevant functions → full bundle", func(t *testing.T) {
		results := []functionResult{
			{Function: core.WebhookFnTagReleaseGroups, OK: true, Changed: true,
				Detail: TagDetail{Added: []string{"FLUX"}, Primary: "Radarr Main"}},
			{Function: core.WebhookFnTagAudio, OK: true, Changed: true,
				Detail: AudioDetail{PlainSummary: "TrueHD Atmos 7.1"}},
		}
		// Explicit filter covering both fired functions = same as nil.
		want := []agents.PayloadField{
			{Name: "Tagged in", Value: "Radarr Main", Inline: false},
			{Name: "Quality tag", Value: "FLUX", Inline: true},
			{Name: "Audio", Value: "TrueHD Atmos 7.1", Inline: true},
			{Name: "Event", Value: "Import", Inline: true},
		}
		got := composeFields(core.WebhookEventDownload, results, []string{"tagReleaseGroups", "tagAudio"}, "", "", "")
		fieldsEqual(t, got, want)
	})

	t.Run("Download — agent subscribed to nothing matching → empty embed", func(t *testing.T) {
		results := []functionResult{
			{Function: core.WebhookFnTagAudio, OK: true, Changed: true,
				Detail: AudioDetail{PlainSummary: "TrueHD"}},
		}
		// Agent subscribed to grabRename only — but the fire is a
		// Download event with tagAudio. Filter eliminates everything,
		// composeFields returns no fields → caller skips dispatch
		// (preserves the "only actual changes" rule combined with
		// "agent only sees what it subscribed to").
		got := composeFields(core.WebhookEventDownload, results, []string{"grabRename"}, "", "", "")
		if len(got) != 0 {
			t.Errorf("expected empty fields when filter excludes everything, got %+v", got)
		}
	})

	t.Run("Download — Sync filtered out but Tag included → Tag section without Mirrored", func(t *testing.T) {
		results := []functionResult{
			{Function: core.WebhookFnTagReleaseGroups, OK: true, Changed: true,
				Detail: TagDetail{Added: []string{"FLUX"}, Primary: "Radarr Main"}},
			{Function: core.WebhookFnSyncToSecondary, OK: true, Changed: true,
				Detail: SyncDetail{SecondaryName: "Radarr 4K"}},
		}
		// Agent subscribed to tagReleaseGroups only — Sync filtered
		// out. TagDetail.Mirrored stays false → "Tagged in: Radarr Main"
		// without secondary. Reviewer's forward-look from 7.4a.
		want := []agents.PayloadField{
			{Name: "Tagged in", Value: "Radarr Main", Inline: false},
			{Name: "Quality tag", Value: "FLUX", Inline: true},
			{Name: "Event", Value: "Import", Inline: true},
		}
		got := composeFields(core.WebhookEventDownload, results, []string{"tagReleaseGroups"}, "", "", "")
		fieldsEqual(t, got, want)
	})
}

// TestDisplayBucketForEngine locks the engine-bucket → display-bucket
// remap that dispatchFileDeleteCleanup uses when populating
// FileDeleteDetail.PerBucket. The user thinks in three buckets
// (audio/video/dv); the engine emits five sub-buckets internally.
func TestDisplayBucketForEngine(t *testing.T) {
	cases := []struct {
		engine, want string
	}{
		{"audio", "audio"},
		{"resolution", "video"},
		{"codec", "video"},
		{"hdr", "video"},
		{"dvdetail", "dv"},
		{"", ""},
		// Unknown bucket passes through unchanged — surfaces a future
		// bucket addition rather than swallowing it.
		{"future-bucket", "future-bucket"},
	}
	for _, tc := range cases {
		t.Run(tc.engine, func(t *testing.T) {
			got := displayBucketForEngine(tc.engine)
			if got != tc.want {
				t.Errorf("displayBucketForEngine(%q) = %q, want %q", tc.engine, got, tc.want)
			}
		})
	}
}

// TestBuildInstanceList locks the helper's four branches: primary-only,
// primary+secondary, fallback when primary empty, fallback when
// secondary empty under mirror.
func TestBuildInstanceList(t *testing.T) {
	cases := []struct {
		name      string
		primary   string
		mirrored  bool
		secondary string
		want      string
	}{
		{"primary name only (not mirrored)", "Radarr Main", false, "", "Radarr Main"},
		{"primary empty + not mirrored → placeholder", "", false, "", "primary Arr"},
		{"primary empty + mirrored → placeholders", "", true, "", "primary Arr · secondary"},
		{"both names populated under mirror", "Radarr Main", true, "Radarr 4K", "Radarr Main · Radarr 4K"},
		{"primary populated, secondary empty under mirror", "Radarr Main", true, "", "Radarr Main · secondary"},
		{"whitespace-only primary → placeholder", "   ", false, "", "primary Arr"},
		{"whitespace-only secondary under mirror → placeholder", "Radarr Main", true, "   ", "Radarr Main · secondary"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildInstanceList(tc.primary, tc.mirrored, tc.secondary)
			if got != tc.want {
				t.Errorf("buildInstanceList(%q, %v, %q) = %q, want %q", tc.primary, tc.mirrored, tc.secondary, got, tc.want)
			}
		})
	}
}

// TestJoinNonEmpty locks the small string-list helper.
func TestJoinNonEmpty(t *testing.T) {
	cases := []struct {
		name  string
		items []string
		sep   string
		want  string
	}{
		{"empty slice", nil, " · ", ""},
		{"all empty strings", []string{"", "  ", "\t"}, " · ", ""},
		{"single value", []string{"a"}, " · ", "a"},
		{"trimming applied", []string{"  a  ", "b"}, " · ", "a · b"},
		{"empty entries dropped", []string{"a", "", "b", "  "}, " · ", "a · b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := joinNonEmpty(tc.items, tc.sep)
			if got != tc.want {
				t.Errorf("joinNonEmpty(%v) = %q, want %q", tc.items, got, tc.want)
			}
		})
	}
}

// TestFormatDvDetailPlainSummary locks the DV-detail value humanizer
// so notification embeds render "Profile 8 · MEL · CM v4.0" instead
// of the raw engine vocabulary "dvprofile8 · mel · cm4". The
// constraint behind the raw vocab is Arr's tag-name regex
// (`^[a-z0-9-]+$`) which the notification embed isn't bound by.
func TestFormatDvDetailPlainSummary(t *testing.T) {
	cases := []struct {
		name   string
		labels []string
		prefix string
		want   string
	}{
		{"empty", nil, "dv-", ""},
		{"profile 8 alone", []string{"dvprofile8"}, "dv-", "Profile 8"},
		{"profile 7 + MEL", []string{"dvprofile7", "mel"}, "dv-", "Profile 7 · MEL"},
		{"profile 7 + FEL + CM v4.0", []string{"dvprofile7", "fel", "cm4"}, "dv-", "Profile 7 · FEL · CM v4.0"},
		{"profile 8 + CM v2.9", []string{"dvprofile8", "cm2"}, "dv-", "Profile 8 · CM v2.9"},
		{"with bucket prefix stripped", []string{"dv-dvprofile8", "dv-mel"}, "dv-", "Profile 8 · MEL"},
		{"unknown token passes through", []string{"dvprofile8", "future-tag"}, "dv-", "Profile 8 · future-tag"},
		{"empty entries dropped", []string{"dvprofile8", "", "  ", "mel"}, "dv-", "Profile 8 · MEL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatDvDetailPlainSummary(tc.labels, tc.prefix)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
