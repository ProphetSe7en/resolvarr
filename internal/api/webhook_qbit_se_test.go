package api

import (
	"context"
	"strings"
	"testing"

	"resolvarr/internal/core"
)

// webhook_qbit_se_test.go — skip-path coverage for dispatchQbitSeTag.
// Three-rule first-match-wins model — exercises every clean skip
// branch + the new "no rule matched" path. qBit network calls
// deferred to soak.

func TestDispatchQbitSeTag_SkipPaths(t *testing.T) {
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

	// Ruleshapes used across cases — three-rule new model.
	episodeRule := &core.QbitSeRules{
		QbitInstanceID: "q1",
		EpisodeEnabled: true, EpisodeTag: "Episode",
	}
	allOnRule := &core.QbitSeRules{
		QbitInstanceID: "q1",
		EpisodeEnabled: true, EpisodeTag: "Episode",
		SeasonEnabled: true, SeasonTag: "Season",
		UnmatchedEnabled: true, UnmatchedTag: "Unmatched",
	}
	allOffRule := &core.QbitSeRules{QbitInstanceID: "q1"}
	ghostQbitRule := &core.QbitSeRules{
		QbitInstanceID: "ghost",
		EpisodeEnabled: true, EpisodeTag: "Episode",
	}

	cases := []struct {
		name    string
		event   core.WebhookConnectEvent
		rule    *core.WebhookRule
		body    string
		wantSub string // expected substring in summary
	}{
		{"non-Grab event",
			core.WebhookEventDownload,
			&core.WebhookRule{AppType: "sonarr", QbitSe: episodeRule},
			`{}`,
			"not a Grab event"},
		{"QbitSe criteria nil",
			core.WebhookEventGrab,
			&core.WebhookRule{AppType: "sonarr"},
			`{"downloadId":"abc","release":{"releaseTitle":"Show.S01E05-FLUX"}}`,
			"no QbitSe criteria"},
		{"empty downloadId",
			core.WebhookEventGrab,
			&core.WebhookRule{AppType: "sonarr", QbitSe: episodeRule},
			`{"downloadId":"","release":{"releaseTitle":"Show.S01E05-FLUX"}}`,
			"no downloadId"},
		{"empty release title",
			core.WebhookEventGrab,
			&core.WebhookRule{AppType: "sonarr", QbitSe: episodeRule},
			`{"downloadId":"abc","release":{"releaseTitle":""}}`,
			"no release.releaseTitle"},
		{"all rules off → no tag",
			core.WebhookEventGrab,
			&core.WebhookRule{AppType: "sonarr", QbitSe: allOffRule},
			`{"downloadId":"abc","release":{"releaseTitle":"Show.S01E05-FLUX"}}`,
			"no rule matched"},
		{"episode-only rule on movie name (no match) → no tag",
			core.WebhookEventGrab,
			&core.WebhookRule{AppType: "sonarr", QbitSe: episodeRule},
			`{"downloadId":"abc","release":{"releaseTitle":"Movie.2024.1080p.WEB-DL-FLUX"}}`,
			"no rule matched"},
		{"qbit instance not found",
			core.WebhookEventGrab,
			&core.WebhookRule{AppType: "sonarr", QbitSe: ghostQbitRule},
			`{"downloadId":"abc","release":{"releaseTitle":"Show.S01E05-FLUX"}}`,
			"qbit instance"},
		// All-rules-on with a movie name lands on Unmatched and tries
		// to call qBit (127.0.0.1:1 fails with connection-refused).
		// Asserts that classification proceeded past the "no rule
		// matched" guard; the connection error is the expected outcome
		// for this skip-path test.
		{"all-on movie → unmatched → qbit reachable",
			core.WebhookEventGrab,
			&core.WebhookRule{AppType: "sonarr", QbitSe: allOnRule},
			`{"downloadId":"abc","release":{"releaseTitle":"Movie.2024.1080p.WEB-DL-FLUX"}}`,
			"qbit"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := &connectEventEnvelope{EventType: string(c.event)}
			res := s.dispatchQbitSeTag(context.Background(), c.rule, store.Get(), env, []byte(c.body))
			if !strings.Contains(res.Summary, c.wantSub) {
				t.Errorf("Summary = %q, want substring %q", res.Summary, c.wantSub)
			}
		})
	}
}
