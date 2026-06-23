package api

import (
	"context"
	"strings"
	"testing"

	"resolvarr/internal/core"
)

// webhook_grab_rename_test.go — coverage for evaluateGrabRenameTriggers
// + dispatchGrabRename's skip-paths. Pure-helper coverage; the qBit
// network calls are deferred to soak with real fixtures.

func TestEvaluateGrabRenameTriggers_MissingReleaseGroup(t *testing.T) {
	c := &core.GrabRenameCriteria{TriggerOnMissingReleaseGroup: true}
	// Current name: " - SumVision" pattern → strict parser bombs →
	// trigger fires (even though rg is "visually present"). This is
	// the Rango/Matilda failure mode.
	current := "Rango 2011 1080p WEB-DL DTS-HD MA 5.1 DoVi - SumVision"
	grab := "Rango 2011 1080p WEB-DL DTS-HD MA 5.1 DoVi - SumVision"
	got := evaluateGrabRenameTriggers(current, grab, "SumVision", c)
	if len(got) != 1 || !strings.HasPrefix(got[0], "missing-release-group") {
		t.Errorf("got %v, want [missing-release-group (...)] (Rango space-dash-space pattern)", got)
	}
}

func TestEvaluateGrabRenameTriggers_MissingReleaseGroup_NoTriggerWhenAlreadyParserFriendly(t *testing.T) {
	c := &core.GrabRenameCriteria{TriggerOnMissingReleaseGroup: true}
	// Current name already parser-friendly — strict parser succeeds.
	current := "Movie 2024 1080p WEB-DL-FLUX"
	grab := "Movie 2024 1080p WEB-DL-FLUX"
	got := evaluateGrabRenameTriggers(current, grab, "FLUX", c)
	if len(got) != 0 {
		t.Errorf("got %v, want [] (rg already parser-friendly, no trigger)", got)
	}
}

func TestEvaluateGrabRenameTriggers_MovieVersionMismatch(t *testing.T) {
	c := &core.GrabRenameCriteria{TriggerOnMovieVersionMismatch: true}
	current := "Movie 2024 1080p WEB-DL-FLUX"
	grab := "Movie 2024 Director's Cut 1080p WEB-DL-FLUX"
	got := evaluateGrabRenameTriggers(current, grab, "FLUX", c)
	if len(got) != 1 || !strings.HasPrefix(got[0], "movie-version:") {
		t.Errorf("got %v, want movie-version trigger", got)
	}
	if !strings.Contains(got[0], "Director's Cut") {
		t.Errorf("trigger should mention 'Director's Cut', got %q", got[0])
	}
}

func TestEvaluateGrabRenameTriggers_AudioMismatch(t *testing.T) {
	c := &core.GrabRenameCriteria{TriggerOnAudioMismatch: true}
	current := "Movie 2024 1080p WEB-DL-FLUX"
	grab := "Movie 2024 1080p WEB-DL TrueHD Atmos 7.1-FLUX"
	got := evaluateGrabRenameTriggers(current, grab, "FLUX", c)
	if len(got) != 1 || !strings.HasPrefix(got[0], "audio:") {
		t.Errorf("got %v, want audio trigger", got)
	}
}

func TestEvaluateGrabRenameTriggers_SceneMismatch(t *testing.T) {
	c := &core.GrabRenameCriteria{TriggerOnSceneMismatch: true}
	t.Run("scene-stripped + non-scene rg → fire", func(t *testing.T) {
		current := "Movie 2024 1080p WEB-FLUX" // scene-stripped pattern
		grab := "Movie 2024 1080p WEB-DL-FLUX"
		got := evaluateGrabRenameTriggers(current, grab, "FLUX", c)
		if len(got) != 1 || !strings.Contains(got[0], "scene-stripped") {
			t.Errorf("got %v, want scene-stripped trigger (FLUX is P2P, not scene)", got)
		}
	})
	t.Run("scene-stripped + scene rg → no fire (legit scene release)", func(t *testing.T) {
		current := "Movie 2024 1080p WEB-CAKES" // scene-stripped pattern
		got := evaluateGrabRenameTriggers(current, "Movie 2024 1080p WEB-CAKES", "CAKES", c)
		if len(got) != 0 {
			t.Errorf("got %v, want [] (CAKES is a known scene group; preserve)", got)
		}
	})
	t.Run("non-scene-stripped pattern → no fire", func(t *testing.T) {
		current := "Movie 2024 1080p WEB-DL-FLUX" // real WEB-DL
		got := evaluateGrabRenameTriggers(current, "Movie 2024 1080p WEB-DL-FLUX", "FLUX", c)
		if len(got) != 0 {
			t.Errorf("got %v, want [] (WEB-DL is real, not scene-stripped)", got)
		}
	})
}

func TestEvaluateGrabRenameTriggers_ForeignBracketPrefix(t *testing.T) {
	c := &core.GrabRenameCriteria{TriggerOnBadNaming: true}
	t.Run("non-Latin leading bracket grab lacks → fire", func(t *testing.T) {
		current := "[测试名].Movie.1969.1080p.iTunes.WEB-DL.H264.DD5.1-UBWEB"
		grab := "Movie 1969 1080p iT WEB-DL DD 5.1 H.264-UBWEB"
		got := evaluateGrabRenameTriggers(current, grab, "UBWEB", c)
		if len(got) != 1 || !strings.Contains(got[0], "foreign-bracket-prefix") {
			t.Errorf("got %v, want foreign-bracket-prefix trigger", got)
		}
	})
	t.Run("ascii leading bracket → no fire", func(t *testing.T) {
		current := "[RlsGrp].Movie.2020.1080p.WEB-DL-RG"
		got := evaluateGrabRenameTriggers(current, "Movie 2020 1080p WEB-DL-RG", "RG", c)
		if len(got) != 0 {
			t.Errorf("got %v, want [] (plain ASCII bracket is left alone)", got)
		}
	})
	t.Run("disabled flag → no fire even on foreign bracket", func(t *testing.T) {
		off := &core.GrabRenameCriteria{}
		got := evaluateGrabRenameTriggers("[测试名].Movie.2020-RG", "Movie 2020-RG", "RG", off)
		if len(got) != 0 {
			t.Errorf("got %v, want [] (trigger disabled)", got)
		}
	})
}

func TestEvaluateGrabRenameTriggers_DuplicateYear(t *testing.T) {
	c := &core.GrabRenameCriteria{TriggerOnBadNaming: true}
	t.Run("same year twice → fire", func(t *testing.T) {
		got := evaluateGrabRenameTriggers("Movie.2026.2026.1080p.AMZN.WEB-DL-KyoGo", "Movie.2026.2026.1080p.AMZN.WEB-DL-KyoGo", "KyoGo", c)
		if len(got) != 1 || !strings.Contains(got[0], "duplicate-year") {
			t.Errorf("got %v, want duplicate-year trigger", got)
		}
	})
	t.Run("different years (title-year + release-year) → no fire", func(t *testing.T) {
		got := evaluateGrabRenameTriggers("Movie.2049.2017.2160p.WEB-DL-FLUX", "Movie.2049.2017.2160p.WEB-DL-FLUX", "FLUX", c)
		if len(got) != 0 {
			t.Errorf("got %v, want [] (2049+2017 is legit, not a duplicate)", got)
		}
	})
	t.Run("disabled flag → no fire", func(t *testing.T) {
		off := &core.GrabRenameCriteria{}
		got := evaluateGrabRenameTriggers("Movie.2026.2026.1080p-RG", "Movie.2026.2026.1080p-RG", "RG", off)
		if len(got) != 0 {
			t.Errorf("got %v, want [] (trigger disabled)", got)
		}
	})
}

func TestEvaluateGrabRenameTriggers_TriggerAlwaysHandledByCaller(t *testing.T) {
	// TriggerAlways is appended by dispatchGrabRename when reasons are
	// otherwise empty — evaluateGrabRenameTriggers itself doesn't see it.
	// Verify the helper returns empty when no individual trigger matches.
	c := &core.GrabRenameCriteria{TriggerAlways: true}
	got := evaluateGrabRenameTriggers("X", "X", "FLUX", c)
	if len(got) != 0 {
		t.Errorf("got %v, want [] (TriggerAlways handled by dispatcher, not helper)", got)
	}
}

func TestEvaluateGrabRenameTriggers_MultipleTriggersOR(t *testing.T) {
	c := &core.GrabRenameCriteria{
		TriggerOnMissingReleaseGroup:  true,
		TriggerOnMovieVersionMismatch: true,
	}
	current := "Movie 2024 1080p WEB-DL - SumVision" // both triggers should fire
	grab := "Movie 2024 IMAX 1080p WEB-DL - SumVision"
	got := evaluateGrabRenameTriggers(current, grab, "SumVision", c)
	if len(got) != 2 {
		t.Errorf("got %v, want 2 triggers (missing-rg + movie-version)", got)
	}
}

func TestDispatchGrabRename_SkipPaths(t *testing.T) {
	// Cover every clean-skip path without touching qBit.
	dir := t.TempDir()
	store := core.NewConfigStore(dir)
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	s := &Server{App: &core.App{Config: store}}
	cases := []struct {
		name    string
		event   core.WebhookConnectEvent
		rule    *core.WebhookRule
		body    string
		wantSub string // expected substring in summary
	}{
		{"non-Grab event", core.WebhookEventDownload,
			&core.WebhookRule{AppType: "radarr", GrabRename: &core.GrabRenameCriteria{}},
			`{}`,
			"not a Grab event"},
		{"GrabRename criteria nil", core.WebhookEventGrab,
			&core.WebhookRule{AppType: "radarr"},
			`{"release":{"releaseTitle":"X"}, "downloadId":"H"}`,
			"no criteria struct"},
		{"empty release title", core.WebhookEventGrab,
			&core.WebhookRule{AppType: "radarr", GrabRename: &core.GrabRenameCriteria{}},
			`{"release":{"releaseTitle":""}, "downloadId":"H"}`,
			"no release.releaseTitle"},
		{"empty downloadId", core.WebhookEventGrab,
			&core.WebhookRule{AppType: "radarr", GrabRename: &core.GrabRenameCriteria{}},
			`{"release":{"releaseTitle":"X"}, "downloadId":""}`,
			"no downloadId"},
		{"qbit instance not found", core.WebhookEventGrab,
			&core.WebhookRule{AppType: "radarr", GrabRename: &core.GrabRenameCriteria{QbitInstanceID: "ghost"}},
			`{"release":{"releaseTitle":"Movie 2024 1080p-FLUX","releaseGroup":"FLUX"}, "downloadId":"abc"}`,
			"qbit instance"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := &connectEventEnvelope{EventType: string(c.event)}
			res := s.dispatchGrabRename(context.Background(), c.rule, store.Get(), env, []byte(c.body))
			if !strings.Contains(res.Summary, c.wantSub) {
				t.Errorf("Summary = %q, want substring %q", res.Summary, c.wantSub)
			}
		})
	}
}

func TestDispatchGrabRename_GroupBlocklist(t *testing.T) {
	// Rule has FLUX in GroupBlocklist — even though qBit is reachable,
	// adapter should skip BEFORE qBit lookup. We use unreachable qBit
	// URL (127.0.0.1:1) and still expect a clean OK=true skip.
	dir := t.TempDir()
	store := core.NewConfigStore(dir)
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := store.Update(func(c *core.Config) {
		c.QbitInstances = []core.QbitInstance{
			{ID: "q1", URL: "http://127.0.0.1:1", Username: "x", Password: "y"},
		}
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	s := &Server{App: &core.App{Config: store}}
	rule := &core.WebhookRule{
		AppType: "radarr",
		GrabRename: &core.GrabRenameCriteria{
			QbitInstanceID:               "q1",
			TriggerOnMissingReleaseGroup: true,
			GroupBlocklist:               []string{"FLUX"},
		},
	}
	body := []byte(`{"release":{"releaseTitle":"Movie 2024 1080p-FLUX","releaseGroup":"FLUX"}, "downloadId":"abc"}`)
	env := &connectEventEnvelope{EventType: string(core.WebhookEventGrab)}
	res := s.dispatchGrabRename(context.Background(), rule, store.Get(), env, body)
	if !res.OK {
		t.Fatalf("expected OK=true (clean blocklist skip), got %+v", res)
	}
	if !strings.Contains(res.Summary, "blocklist") {
		t.Errorf("Summary = %q, want 'blocklist' phrase", res.Summary)
	}
}

// TestSummariseGrabRenameRecovery exercises the raw-trigger-label →
// user-friendly recovery-summary mapping. Each prefix branch from
// evaluateGrabRenameTriggers is covered explicitly so a future
// rename of the diagnostic prefix (e.g. "audio:" → "audio-mismatch:")
// breaks here loudly rather than silently dropping tokens from the
// "Tokens Recovered" embed field.
func TestSummariseGrabRenameRecovery(t *testing.T) {
	cases := []struct {
		name       string
		reasons    []string
		rg         string
		wantGroup  string
		wantTokens []string
	}{
		{"empty reasons → no recovery", nil, "FLUX", "", nil},
		{"missing-release-group (parser rejected variant)",
			[]string{`missing-release-group (parser rejected: "Rango 2011 - SumVision" — strict parser bombs on space-dash-space)`},
			"SumVision",
			"SumVision", nil,
		},
		{"missing-release-group (parsed/expected mismatch variant)",
			[]string{`missing-release-group (parsed="FLUX" expected="FLUX-RG")`},
			"FLUX-RG",
			"FLUX-RG", nil,
		},
		{"movie-version single token",
			[]string{"movie-version: Director's Cut"},
			"FLUX",
			"", []string{"Director's Cut"},
		},
		{"movie-version multiple tokens split on slash",
			[]string{"movie-version: Director's Cut/IMAX/Remaster"},
			"FLUX",
			"", []string{"Director's Cut", "IMAX", "Remaster"},
		},
		{"source mismatch",
			[]string{"source: WEB-DL"},
			"FLUX",
			"", []string{"WEB-DL"},
		},
		{"audio mismatch — multi token",
			[]string{"audio: TrueHD/Atmos"},
			"FLUX",
			"", []string{"TrueHD", "Atmos"},
		},
		{"scene-stripped → 'scene' token",
			[]string{"scene-stripped (rg not a known scene group)"},
			"FLUX",
			"", []string{"scene"},
		},
		{"custom tokens",
			[]string{"custom: IMAX/UHD"},
			"FLUX",
			"", []string{"IMAX", "UHD"},
		},
		{"always-rename has no recovery semantics",
			[]string{"always-rename"},
			"FLUX",
			"", nil,
		},
		{"combo — RG + audio + custom",
			[]string{
				"missing-release-group (parser rejected: bare name)",
				"audio: TrueHD/Atmos",
				"custom: IMAX",
			},
			"FLUX",
			"FLUX", []string{"TrueHD", "Atmos", "IMAX"},
		},
		{"dedup — same token from movie-version + custom",
			[]string{
				"movie-version: IMAX",
				"custom: IMAX",
			},
			"FLUX",
			"", []string{"IMAX"},
		},
		{"empty rg + missing-release-group → GroupRecovered stays empty (defence)",
			[]string{"missing-release-group (parser rejected: x)"},
			"",
			"", nil,
		},
		{"unknown prefix passes through without contribution",
			[]string{"future-trigger-shape: who-knows"},
			"FLUX",
			"", nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotGroup, gotTokens, _ := summariseGrabRenameRecovery(tc.reasons, tc.rg)
			if gotGroup != tc.wantGroup {
				t.Errorf("GroupRecovered = %q, want %q", gotGroup, tc.wantGroup)
			}
			if len(gotTokens) != len(tc.wantTokens) {
				t.Fatalf("TokensRecovered len = %d (%v), want %d (%v)", len(gotTokens), gotTokens, len(tc.wantTokens), tc.wantTokens)
			}
			for i, want := range tc.wantTokens {
				if gotTokens[i] != want {
					t.Errorf("TokensRecovered[%d] = %q, want %q", i, gotTokens[i], want)
				}
			}
		})
	}
}

func TestFindQbitInstanceByID(t *testing.T) {
	cfg := core.Config{
		QbitInstances: []core.QbitInstance{
			{ID: "q1", Name: "Main"},
			{ID: "q2", Name: "Backup"},
		},
	}
	if got := findQbitInstanceByID(cfg, "q2"); got == nil || got.Name != "Backup" {
		t.Errorf("got %+v, want q2/Backup", got)
	}
	if got := findQbitInstanceByID(cfg, "ghost"); got != nil {
		t.Errorf("got %+v, want nil", got)
	}
	if got := findQbitInstanceByID(cfg, ""); got != nil {
		t.Errorf("got %+v, want nil (empty id)", got)
	}
}

func TestSummariseGrabRenameRecovery_NameCleanup(t *testing.T) {
	cases := []struct {
		name        string
		reasons     []string
		rg          string
		wantGroup   string
		wantCleanup []string
	}{
		{"foreign bracket → cleanup, no group recovered",
			[]string{"foreign-bracket-prefix (Radarr would misparse the leading bracket as the release group)"},
			"UBWEB", "", []string{"foreign bracket prefix"}},
		{"duplicate year → cleanup",
			[]string{"duplicate-year (same year twice; collapsed to one)"},
			"KyoGo", "", []string{"duplicate year"}},
		{"both bad-naming defects → both cleanups",
			[]string{
				"foreign-bracket-prefix (x)",
				"duplicate-year (y)",
			},
			"RG", "", []string{"foreign bracket prefix", "duplicate year"}},
		{"missing-rg still recovers group, no cleanup",
			[]string{"missing-release-group (parser rejected: x)"},
			"FLUX", "FLUX", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotGroup, _, gotCleanup := summariseGrabRenameRecovery(tc.reasons, tc.rg)
			if gotGroup != tc.wantGroup {
				t.Errorf("GroupRecovered = %q, want %q", gotGroup, tc.wantGroup)
			}
			if len(gotCleanup) != len(tc.wantCleanup) {
				t.Fatalf("NameCleanup = %v, want %v", gotCleanup, tc.wantCleanup)
			}
			for i, want := range tc.wantCleanup {
				if gotCleanup[i] != want {
					t.Errorf("NameCleanup[%d] = %q, want %q", i, gotCleanup[i], want)
				}
			}
		})
	}
}

func TestReasonsNeedGrabBase(t *testing.T) {
	cases := []struct {
		name    string
		reasons []string
		want    bool
	}{
		{"token-preservation needs grab", []string{"missing-release-group (x)"}, true},
		{"source needs grab", []string{"source: WEB-DL"}, true},
		{"always-rename needs grab", []string{"always-rename"}, true},
		{"bad-naming only → clean in place", []string{"foreign-bracket-prefix (x)", "duplicate-year (y)", "file-extension (z)"}, false},
		{"mixed bad-naming + token → grab", []string{"foreign-bracket-prefix (x)", "audio: TrueHD"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := reasonsNeedGrabBase(c.reasons); got != c.want {
				t.Errorf("reasonsNeedGrabBase(%v) = %v, want %v", c.reasons, got, c.want)
			}
		})
	}
}

func TestEvaluateGrabRenameTriggers_FileExtension(t *testing.T) {
	c := &core.GrabRenameCriteria{TriggerOnFileExtension: true}
	t.Run("trailing .mkv in display → fire", func(t *testing.T) {
		got := evaluateGrabRenameTriggers("Movie.2024.2160p.MA.WEB-DL-FLUX.mkv", "Movie.2024.2160p.MA.WEB-DL-FLUX", "FLUX", c)
		if len(got) != 1 || !strings.Contains(got[0], "file-extension") {
			t.Errorf("got %v, want file-extension trigger", got)
		}
	})
	t.Run("no extension → no fire", func(t *testing.T) {
		got := evaluateGrabRenameTriggers("Movie.2024.2160p.MA.WEB-DL-FLUX", "Movie.2024.2160p.MA.WEB-DL-FLUX", "FLUX", c)
		if len(got) != 0 {
			t.Errorf("got %v, want []", got)
		}
	})
}

func TestEvaluateGrabRenameTriggers_BadNamingIgnoresExtension(t *testing.T) {
	// Bad naming no longer fires on a trailing extension; only the
	// dedicated file-extension trigger does. A name whose ONLY defect is a
	// ".mkv" must not fire when only Bad naming is enabled.
	c := &core.GrabRenameCriteria{TriggerOnBadNaming: true}
	got := evaluateGrabRenameTriggers("Movie.2024.2160p.MA.WEB-DL-FLUX.mkv", "Movie.2024.2160p.MA.WEB-DL-FLUX", "FLUX", c)
	if len(got) != 0 {
		t.Errorf("got %v, want [] (Bad naming must ignore a trailing extension)", got)
	}
}
