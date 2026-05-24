package agents

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestDiscordValidate verifies the Discord provider's Validate logic:
// missing webhook, invalid URL prefix, invalid updates webhook, and valid config.
func TestDiscordValidate(t *testing.T) {
	tests := []struct {
		name    string
		agent   Agent
		wantErr string
	}{
		{
			name:    "missing webhook",
			agent:   Agent{Name: "Discord", Type: "discord"},
			wantErr: "discord webhook is required",
		},
		{
			name: "invalid webhook",
			agent: Agent{Name: "Discord", Type: "discord", Config: Config{
				DiscordWebhook: "http://example.com/webhook",
			}},
			wantErr: "discord webhook must start with https://discord.com/api/webhooks/ or https://discordapp.com/api/webhooks/",
		},
		{
			name: "invalid updates webhook",
			agent: Agent{Name: "Discord", Type: "discord", Config: Config{
				DiscordWebhook:        "https://discord.com/api/webhooks/111/aaa",
				DiscordWebhookUpdates: "http://example.com/webhook",
			}},
			wantErr: "discord updates webhook must start with https://discord.com/api/webhooks/ or https://discordapp.com/api/webhooks/",
		},
		{
			name: "valid",
			agent: Agent{Name: "Discord", Type: "discord", Config: Config{
				DiscordWebhook:        "https://discord.com/api/webhooks/111/aaa",
				DiscordWebhookUpdates: "https://discord.com/api/webhooks/222/bbb",
			}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateAgent(tc.agent)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateAgent() unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ValidateAgent() expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("ValidateAgent() error = %q, want contains %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestDiscordMaskAndPreserve verifies the credential mask/preserve round-trip:
// MaskConfigByType replaces webhook URLs with placeholders, and PreserveConfigByType
// restores the originals when those placeholders are submitted back.
func TestDiscordMaskAndPreserve(t *testing.T) {
	cfg := Config{
		DiscordWebhook:        "https://discord.com/api/webhooks/111/aaa",
		DiscordWebhookUpdates: "https://discord.com/api/webhooks/222/bbb",
	}

	masked := MaskConfigByType("discord", cfg)
	if masked.DiscordWebhook != maskedDiscordWebhook {
		t.Fatalf("discord webhook not masked")
	}
	if masked.DiscordWebhookUpdates != maskedDiscordWebhook {
		t.Fatalf("discord updates webhook not masked")
	}

	restored := PreserveConfigByType("discord", masked, cfg)
	if restored.DiscordWebhook != cfg.DiscordWebhook {
		t.Fatalf("discord webhook not preserved")
	}
	if restored.DiscordWebhookUpdates != cfg.DiscordWebhookUpdates {
		t.Fatalf("discord updates webhook not preserved")
	}
}

// TestDiscordResolveWebhook verifies route-based webhook selection:
// RouteDefault → main webhook, RouteUpdates → updates webhook (with fallback
// to main when updates webhook is empty).
func TestDiscordResolveWebhook(t *testing.T) {
	p := discordProvider{}
	agent := Agent{Config: Config{
		DiscordWebhook:        "https://discord.com/api/webhooks/main/token",
		DiscordWebhookUpdates: "https://discord.com/api/webhooks/updates/token",
	}}

	if got := p.resolveWebhook(agent, RouteDefault); got != agent.Config.DiscordWebhook {
		t.Fatalf("default route webhook = %q", got)
	}
	if got := p.resolveWebhook(agent, RouteUpdates); got != agent.Config.DiscordWebhookUpdates {
		t.Fatalf("updates route webhook = %q", got)
	}

	agent.Config.DiscordWebhookUpdates = ""
	if got := p.resolveWebhook(agent, RouteUpdates); got != agent.Config.DiscordWebhook {
		t.Fatalf("updates fallback webhook = %q", got)
	}
}

// TestDiscordTestHappyPath verifies that Test sends to both webhooks and
// returns OK results when the HTTP client returns 200.
func TestDiscordTestHappyPath(t *testing.T) {
	mock := newOKPoster()
	rt := testRuntime(nil, mock)
	agent := Agent{Name: "Discord", Type: "discord", Config: Config{
		DiscordWebhook:        "https://discord.com/api/webhooks/111/aaa",
		DiscordWebhookUpdates: "https://discord.com/api/webhooks/222/bbb",
	}}

	results, err := discordProvider{}.Test(context.Background(), rt, agent)
	if err != nil {
		t.Fatalf("Test() error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results (main + updates), got %d", len(results))
	}
	for _, r := range results {
		if r.Status != statusOK {
			t.Errorf("result %q: status=%q, error=%q", r.Label, r.Status, r.Error)
		}
	}
}

// TestDiscordTestHTTPError verifies that Test captures per-channel errors
// when the HTTP client returns an error.
func TestDiscordTestHTTPError(t *testing.T) {
	mock := newErrorPoster("connection refused")
	rt := testRuntime(nil, mock)
	agent := Agent{Name: "Discord", Type: "discord", Config: Config{
		DiscordWebhook: "https://discord.com/api/webhooks/111/aaa",
	}}

	results, err := discordProvider{}.Test(context.Background(), rt, agent)
	if err != nil {
		t.Fatalf("Test() should not return top-level error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != statusError {
		t.Fatalf("expected error status, got %q", results[0].Status)
	}
	if !strings.Contains(results[0].Error, "connection refused") {
		t.Fatalf("error should contain 'connection refused', got %q", results[0].Error)
	}
}

// TestDiscordTestNoWebhook verifies that Test returns an error when no
// webhook URL is configured.
func TestDiscordTestNoWebhook(t *testing.T) {
	rt := testRuntime(nil, newOKPoster())
	agent := Agent{Name: "Discord", Type: "discord", Config: Config{}}

	_, err := discordProvider{}.Test(context.Background(), rt, agent)
	if err == nil {
		t.Fatal("expected error when no webhook configured")
	}
}

// TestDiscordNotifyHappyPath verifies that Notify sends to the correct
// webhook and returns nil on success.
func TestDiscordNotifyHappyPath(t *testing.T) {
	mock := newOKPoster()
	rt := testRuntime(nil, mock)
	agent := Agent{Name: "Discord", Type: "discord", Enabled: true, Config: Config{
		DiscordWebhook: "https://discord.com/api/webhooks/111/aaa",
	}}
	payload := Payload{
		Title:    "Test Title",
		Message:  "Test body",
		Color:    0x00ff00,
		Severity: SeverityInfo,
		Route:    RouteDefault,
	}

	err := discordProvider{}.Notify(context.Background(), rt, agent, payload)
	if err != nil {
		t.Fatalf("Notify() error: %v", err)
	}
	if !strings.Contains(mock.lastURL, "discord.com/api/webhooks/") {
		t.Fatalf("expected Discord webhook URL, got %q", mock.lastURL)
	}
}

// TestDiscordNotifyHTTPError verifies that Notify returns errors from the
// HTTP client.
func TestDiscordNotifyHTTPError(t *testing.T) {
	mock := newStatusPoster(429)
	rt := testRuntime(nil, mock)
	agent := Agent{Name: "Discord", Type: "discord", Enabled: true, Config: Config{
		DiscordWebhook: "https://discord.com/api/webhooks/111/aaa",
	}}

	err := discordProvider{}.Notify(context.Background(), rt, agent, Payload{Route: RouteDefault})
	if err == nil {
		t.Fatal("expected error on 429")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Fatalf("error should mention status code: %v", err)
	}
}

// TestDiscordNotifyNilClient verifies that Notify returns an error when
// the SafeClient is not configured.
func TestDiscordNotifyNilClient(t *testing.T) {
	rt := testRuntime(nil, nil)
	agent := Agent{Name: "Discord", Type: "discord", Enabled: true, Config: Config{
		DiscordWebhook: "https://discord.com/api/webhooks/111/aaa",
	}}

	err := discordProvider{}.Notify(context.Background(), rt, agent, Payload{Route: RouteDefault})
	if err == nil {
		t.Fatal("expected error when SafeClient is nil")
	}
}

// TestDiscordNotifyEmptyWebhook verifies that Notify silently skips when
// the resolved webhook is empty.
func TestDiscordNotifyEmptyWebhook(t *testing.T) {
	rt := testRuntime(nil, newOKPoster())
	agent := Agent{Name: "Discord", Type: "discord", Enabled: true, Config: Config{}}

	err := discordProvider{}.Notify(context.Background(), rt, agent, Payload{Route: RouteDefault})
	if err != nil {
		t.Fatalf("expected nil for empty webhook, got: %v", err)
	}
}

// TestDiscordNotifyThumbnail verifies that a non-empty ThumbnailURL on
// the payload renders into the embed's `thumbnail.url` field. Webhook-
// fire notifications use this for the movie/series poster sourced from
// the Connect payload (`.movie.images[].remoteUrl`).
func TestDiscordNotifyThumbnail(t *testing.T) {
	mock := newOKPoster()
	rt := testRuntime(nil, mock)
	agent := Agent{Name: "Discord", Type: "discord", Enabled: true, Config: Config{
		DiscordWebhook: "https://discord.com/api/webhooks/111/aaa",
	}}
	payload := Payload{
		Title:        "Tagged - The Matrix (1999)",
		Color:        0xFFA500,
		Route:        RouteDefault,
		ThumbnailURL: "https://image.tmdb.org/t/p/original/poster.jpg",
	}

	if err := (discordProvider{}).Notify(context.Background(), rt, agent, payload); err != nil {
		t.Fatalf("Notify() error: %v", err)
	}

	var got struct {
		Embeds []struct {
			Thumbnail struct {
				URL string `json:"url"`
			} `json:"thumbnail"`
		} `json:"embeds"`
	}
	if err := json.Unmarshal(mock.lastBody, &got); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if len(got.Embeds) != 1 {
		t.Fatalf("expected 1 embed, got %d", len(got.Embeds))
	}
	if got.Embeds[0].Thumbnail.URL != "https://image.tmdb.org/t/p/original/poster.jpg" {
		t.Fatalf("thumbnail.url = %q, want poster URL", got.Embeds[0].Thumbnail.URL)
	}
}

// TestDiscordNotifyTimestamp verifies that a non-zero Payload.Timestamp
// is emitted as the embed's `timestamp` field in RFC3339 / UTC. Discord
// renders this as the locale-aware "Today at 14:32" / "Yesterday at …"
// label in the lower-right of the embed.
func TestDiscordNotifyTimestamp(t *testing.T) {
	mock := newOKPoster()
	rt := testRuntime(nil, mock)
	agent := Agent{Name: "Discord", Type: "discord", Enabled: true, Config: Config{
		DiscordWebhook: "https://discord.com/api/webhooks/111/aaa",
	}}
	// Pick a fixed non-UTC instant so we can verify both that the field
	// lands AND that the wire-form is UTC-normalised RFC3339.
	loc, _ := time.LoadLocation("Europe/Oslo")
	ts := time.Date(2026, 5, 24, 16, 32, 7, 0, loc) // 16:32:07 Oslo = 14:32:07 UTC
	payload := Payload{
		Title:     "Tagged - Movie (2024)",
		Route:     RouteDefault,
		Timestamp: ts,
	}

	if err := (discordProvider{}).Notify(context.Background(), rt, agent, payload); err != nil {
		t.Fatalf("Notify() error: %v", err)
	}

	var got struct {
		Embeds []struct {
			Timestamp string `json:"timestamp"`
		} `json:"embeds"`
	}
	if err := json.Unmarshal(mock.lastBody, &got); err != nil {
		t.Fatalf("unmarshal body: %v\nbody: %s", err, mock.lastBody)
	}
	if len(got.Embeds) != 1 {
		t.Fatalf("expected 1 embed, got %d", len(got.Embeds))
	}
	// RFC3339-formatted UTC: "2026-05-24T14:32:07Z" — original Oslo
	// offset (+02:00) is normalised to Z so wire payload is locale-
	// stable. Discord clients re-render in viewer's timezone.
	const wantWire = "2026-05-24T14:32:07Z"
	if got.Embeds[0].Timestamp != wantWire {
		t.Errorf("embed.timestamp = %q, want %q (UTC RFC3339)", got.Embeds[0].Timestamp, wantWire)
	}
}

// TestDiscordNotifyTimestampZeroValue verifies that the zero-value
// time.Time omits the `timestamp` key entirely. Non-webhook callers
// (Test path, ad-hoc notifications) leave Timestamp unset; they
// should not have the embed pinned to 0001-01-01.
func TestDiscordNotifyTimestampZeroValue(t *testing.T) {
	mock := newOKPoster()
	rt := testRuntime(nil, mock)
	agent := Agent{Name: "Discord", Type: "discord", Enabled: true, Config: Config{
		DiscordWebhook: "https://discord.com/api/webhooks/111/aaa",
	}}
	payload := Payload{Title: "no timestamp", Route: RouteDefault} // Timestamp left zero

	if err := (discordProvider{}).Notify(context.Background(), rt, agent, payload); err != nil {
		t.Fatalf("Notify() error: %v", err)
	}
	if strings.Contains(string(mock.lastBody), `"timestamp"`) {
		t.Errorf("zero Timestamp should omit the timestamp key, got body: %s", mock.lastBody)
	}
}

// TestDiscordNotifyNoThumbnail verifies that an empty ThumbnailURL
// omits the `thumbnail` key from the embed entirely (Discord renders
// nothing rather than a broken-image icon).
func TestDiscordNotifyNoThumbnail(t *testing.T) {
	mock := newOKPoster()
	rt := testRuntime(nil, mock)
	agent := Agent{Name: "Discord", Type: "discord", Enabled: true, Config: Config{
		DiscordWebhook: "https://discord.com/api/webhooks/111/aaa",
	}}
	payload := Payload{Title: "no poster", Route: RouteDefault}

	if err := (discordProvider{}).Notify(context.Background(), rt, agent, payload); err != nil {
		t.Fatalf("Notify() error: %v", err)
	}
	if strings.Contains(string(mock.lastBody), `"thumbnail"`) {
		t.Fatalf("empty ThumbnailURL should omit thumbnail key, got body: %s", mock.lastBody)
	}
}

// TestDiscordNotifyFooterSuffix verifies that a non-empty FooterSuffix
// is appended to the default "Resolvarr {version} by ProphetSe7en"
// footer with " · " as separator. Webhook-fire notifications use this
// to add the rule name without forcing callers to know the version
// string.
func TestDiscordNotifyFooterSuffix(t *testing.T) {
	mock := newOKPoster()
	rt := Runtime{Version: "v9.9.9", SafeClient: mock}
	agent := Agent{Name: "Discord", Type: "discord", Enabled: true, Config: Config{
		DiscordWebhook: "https://discord.com/api/webhooks/111/aaa",
	}}
	payload := Payload{
		Title:        "fire",
		Route:        RouteDefault,
		FooterSuffix: "rule: Tag 4K imports",
	}

	if err := (discordProvider{}).Notify(context.Background(), rt, agent, payload); err != nil {
		t.Fatalf("Notify() error: %v", err)
	}
	var got struct {
		Embeds []struct {
			Footer struct {
				Text string `json:"text"`
			} `json:"footer"`
		} `json:"embeds"`
	}
	if err := json.Unmarshal(mock.lastBody, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := "Resolvarr v9.9.9 by ProphetSe7en · rule: Tag 4K imports"
	if got.Embeds[0].Footer.Text != want {
		t.Fatalf("footer = %q, want %q", got.Embeds[0].Footer.Text, want)
	}
}

// TestDiscordNotifyFooterSuffixWhitespaceOnly verifies that a
// whitespace-only FooterSuffix is treated as empty (no separator,
// no trailing junk in the footer). Defends against UI quirks where
// a focused-then-cleared input field sends "  ".
func TestDiscordNotifyFooterSuffixWhitespaceOnly(t *testing.T) {
	mock := newOKPoster()
	rt := Runtime{Version: "v9.9.9", SafeClient: mock}
	agent := Agent{Name: "Discord", Type: "discord", Enabled: true, Config: Config{
		DiscordWebhook: "https://discord.com/api/webhooks/111/aaa",
	}}

	if err := (discordProvider{}).Notify(context.Background(), rt, agent, Payload{Title: "x", Route: RouteDefault, FooterSuffix: "   "}); err != nil {
		t.Fatalf("Notify() error: %v", err)
	}
	if !strings.Contains(string(mock.lastBody), `"Resolvarr v9.9.9 by ProphetSe7en"`) {
		t.Fatalf("whitespace-only suffix should leave plain default footer; body: %s", mock.lastBody)
	}
	if strings.Contains(string(mock.lastBody), " · ") {
		t.Fatalf("whitespace-only suffix should not introduce ' · ' separator; body: %s", mock.lastBody)
	}
}

// TestDiscordNotifyFooterDefault verifies that an empty FooterSuffix
// keeps the standard "Resolvarr {version} by ProphetSe7en" footer
// untouched.
func TestDiscordNotifyFooterDefault(t *testing.T) {
	mock := newOKPoster()
	rt := Runtime{Version: "v9.9.9", SafeClient: mock}
	agent := Agent{Name: "Discord", Type: "discord", Enabled: true, Config: Config{
		DiscordWebhook: "https://discord.com/api/webhooks/111/aaa",
	}}

	if err := (discordProvider{}).Notify(context.Background(), rt, agent, Payload{Title: "x", Route: RouteDefault}); err != nil {
		t.Fatalf("Notify() error: %v", err)
	}
	if !strings.Contains(string(mock.lastBody), "Resolvarr v9.9.9 by ProphetSe7en") {
		t.Fatalf("default footer missing from body: %s", mock.lastBody)
	}
	if strings.Contains(string(mock.lastBody), " · ") {
		t.Fatalf("empty suffix should not introduce ' · ' separator; body: %s", mock.lastBody)
	}
}

// TestDiscordNotifyThumbnailAndFooterSuffix verifies that thumbnail
// + footer suffix render correctly together — the most common
// webhook-fire payload shape (poster + "rule: X" suffix).
func TestDiscordNotifyThumbnailAndFooterSuffix(t *testing.T) {
	mock := newOKPoster()
	rt := Runtime{Version: "v0.6.4-dev", SafeClient: mock}
	agent := Agent{Name: "Discord", Type: "discord", Enabled: true, Config: Config{
		DiscordWebhook: "https://discord.com/api/webhooks/111/aaa",
	}}
	payload := Payload{
		Title:        "Tagged - The Matrix (1999)",
		Color:        0xFFA500,
		Route:        RouteDefault,
		ThumbnailURL: "https://image.tmdb.org/t/p/original/poster.jpg",
		FooterSuffix: "rule: Tag 4K imports",
	}

	if err := (discordProvider{}).Notify(context.Background(), rt, agent, payload); err != nil {
		t.Fatalf("Notify() error: %v", err)
	}
	var got struct {
		Embeds []struct {
			Footer struct {
				Text string `json:"text"`
			} `json:"footer"`
			Thumbnail struct {
				URL string `json:"url"`
			} `json:"thumbnail"`
		} `json:"embeds"`
	}
	if err := json.Unmarshal(mock.lastBody, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Embeds[0].Footer.Text != "Resolvarr v0.6.4-dev by ProphetSe7en · rule: Tag 4K imports" {
		t.Errorf("footer = %q", got.Embeds[0].Footer.Text)
	}
	if got.Embeds[0].Thumbnail.URL != "https://image.tmdb.org/t/p/original/poster.jpg" {
		t.Errorf("thumbnail = %q", got.Embeds[0].Thumbnail.URL)
	}
}

// TestDiscordNotifyThumbnailWhitespaceOnly verifies that a
// whitespace-only ThumbnailURL is treated as empty — the embed must
// not get a "thumbnail" key with whitespace value (Discord renders
// that as a broken-image icon).
func TestDiscordNotifyThumbnailWhitespaceOnly(t *testing.T) {
	mock := newOKPoster()
	rt := testRuntime(nil, mock)
	agent := Agent{Name: "Discord", Type: "discord", Enabled: true, Config: Config{
		DiscordWebhook: "https://discord.com/api/webhooks/111/aaa",
	}}

	if err := (discordProvider{}).Notify(context.Background(), rt, agent, Payload{Title: "x", Route: RouteDefault, ThumbnailURL: "   "}); err != nil {
		t.Fatalf("Notify() error: %v", err)
	}
	if strings.Contains(string(mock.lastBody), `"thumbnail"`) {
		t.Fatalf("whitespace-only thumbnail should omit key, got: %s", mock.lastBody)
	}
}

// TestDiscordNotifyRouteUpdates verifies that Notify uses the updates
// webhook when Route is RouteUpdates.
func TestDiscordNotifyRouteUpdates(t *testing.T) {
	mock := newOKPoster()
	rt := testRuntime(nil, mock)
	agent := Agent{Name: "Discord", Type: "discord", Enabled: true, Config: Config{
		DiscordWebhook:        "https://discord.com/api/webhooks/main/token",
		DiscordWebhookUpdates: "https://discord.com/api/webhooks/updates/token",
	}}

	err := discordProvider{}.Notify(context.Background(), rt, agent, Payload{Route: RouteUpdates})
	if err != nil {
		t.Fatalf("Notify() error: %v", err)
	}
	if !strings.Contains(mock.lastURL, "/updates/") {
		t.Fatalf("expected updates webhook URL, got %q", mock.lastURL)
	}
}
