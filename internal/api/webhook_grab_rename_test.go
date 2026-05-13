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
