package api

import (
	"context"
	"strings"
	"testing"

	"resolvarr/internal/core"
)

// webhook_qbit_se_test.go — skip-path coverage for dispatchQbitSeTag.
// qBit network calls deferred to soak.

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

	cases := []struct {
		name    string
		event   core.WebhookConnectEvent
		rule    *core.WebhookRule
		body    string
		wantSub string // expected substring in summary
	}{
		{"non-Grab event",
			core.WebhookEventDownload,
			&core.WebhookRule{AppType: "sonarr", QbitSe: &core.QbitSeRules{TagSeason: true, QbitInstanceID: "q1"}},
			`{}`,
			"not a Grab event"},
		{"QbitSe criteria nil",
			core.WebhookEventGrab,
			&core.WebhookRule{AppType: "sonarr"},
			`{"downloadId":"abc","episodes":[{"seasonNumber":1,"episodeNumber":5}]}`,
			"no QbitSe criteria"},
		{"empty downloadId",
			core.WebhookEventGrab,
			&core.WebhookRule{AppType: "sonarr", QbitSe: &core.QbitSeRules{TagSeason: true, QbitInstanceID: "q1"}},
			`{"downloadId":"","episodes":[{"seasonNumber":1,"episodeNumber":5}]}`,
			"no downloadId"},
		{"empty episodes",
			core.WebhookEventGrab,
			&core.WebhookRule{AppType: "sonarr", QbitSe: &core.QbitSeRules{TagSeason: true, QbitInstanceID: "q1"}},
			`{"downloadId":"abc","episodes":[]}`,
			"no episodes"},
		{"multi-season episodes (anomalous)",
			core.WebhookEventGrab,
			&core.WebhookRule{AppType: "sonarr", QbitSe: &core.QbitSeRules{TagSeason: true, QbitInstanceID: "q1"}},
			`{"downloadId":"abc","episodes":[{"seasonNumber":1,"episodeNumber":5},{"seasonNumber":2,"episodeNumber":1}]}`,
			"multiple seasons"},
		{"both formats off",
			core.WebhookEventGrab,
			&core.WebhookRule{AppType: "sonarr", QbitSe: &core.QbitSeRules{QbitInstanceID: "q1"}},
			`{"downloadId":"abc","episodes":[{"seasonNumber":1,"episodeNumber":5}]}`,
			"no tag formats enabled"},
		{"qbit instance not found",
			core.WebhookEventGrab,
			&core.WebhookRule{AppType: "sonarr", QbitSe: &core.QbitSeRules{TagSeason: true, QbitInstanceID: "ghost"}},
			`{"downloadId":"abc","episodes":[{"seasonNumber":1,"episodeNumber":5}]}`,
			"qbit instance"},
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
