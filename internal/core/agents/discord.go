package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

// discordProvider implements Provider for Discord webhook notifications.
// It supports two independent webhook channels (main and updates) selected
// via payload Route. Messages are delivered as Discord embeds with a colored
// sidebar and a footer containing the Resolvarr version.
//
// Security: Discord webhooks are user-supplied URLs, so all HTTP calls go
// through Runtime.SafeClient (SSRF-protected). The URL is also validated
// against known Discord API prefixes before every send.
type discordProvider struct{}

// Compile-time check: discordProvider satisfies the Provider interface.
var _ Provider = discordProvider{}

func init() {
	registerProvider(discordProvider{})
}

// Type returns the provider registration key used in Agent.Type.
func (discordProvider) Type() string {
	return "discord"
}

// Async returns true because Discord webhook sends are dispatched in background
// workers. This prevents the sync goroutine from blocking when Discord rate-limits
// (429 + Retry-After) or experiences high latency.
func (discordProvider) Async() bool {
	return true
}

// MaskConfig hides Discord webhook credentials for API responses.
func (discordProvider) MaskConfig(cfg Config) Config {
	cfg.DiscordWebhook = maskSecret(cfg.DiscordWebhook, maskedDiscordWebhook)
	cfg.DiscordWebhookUpdates = maskSecret(cfg.DiscordWebhookUpdates, maskedDiscordWebhook)
	return cfg
}

// PreserveConfig keeps existing credentials when masked placeholders are posted back.
func (discordProvider) PreserveConfig(incoming, existing Config) Config {
	incoming.DiscordWebhook = preserveIfMasked(strings.TrimSpace(incoming.DiscordWebhook), existing.DiscordWebhook, maskedDiscordWebhook)
	incoming.DiscordWebhookUpdates = preserveIfMasked(strings.TrimSpace(incoming.DiscordWebhookUpdates), existing.DiscordWebhookUpdates, maskedDiscordWebhook)
	return incoming
}

// Validate checks that the main webhook URL is present and well-formed,
// and optionally validates the updates webhook URL if provided.
func (discordProvider) Validate(agent Agent) error {
	if strings.TrimSpace(agent.Config.DiscordWebhook) == "" {
		return fmt.Errorf("discord webhook is required")
	}
	webhook := strings.TrimSpace(agent.Config.DiscordWebhook)
	if isLiteralPlaceholder(webhook) {
		return fmt.Errorf("discord webhook is the masked placeholder — re-enter the real webhook URL (Server Settings → Integrations → Webhooks in Discord)")
	}
	if !isDiscordWebhookURL(webhook) {
		return fmt.Errorf("discord webhook must start with https://discord.com/api/webhooks/ or https://discordapp.com/api/webhooks/")
	}
	if u := strings.TrimSpace(agent.Config.DiscordWebhookUpdates); u != "" {
		if isLiteralPlaceholder(u) {
			return fmt.Errorf("discord updates webhook is the masked placeholder — re-enter the real webhook URL")
		}
		if !isDiscordWebhookURL(u) {
			return fmt.Errorf("discord updates webhook must start with https://discord.com/api/webhooks/ or https://discordapp.com/api/webhooks/")
		}
	}
	return nil
}

// Test sends one test embed per configured webhook channel (main and, if
// different, updates). Returns one TestResult per channel so the UI can
// show per-channel pass/fail feedback.
func (d discordProvider) Test(ctx context.Context, runtime Runtime, agent Agent) ([]TestResult, error) {
	cfg := agent.Config
	mainWebhook := strings.TrimSpace(cfg.DiscordWebhook)
	updatesWebhook := strings.TrimSpace(cfg.DiscordWebhookUpdates)
	if mainWebhook == "" {
		return nil, fmt.Errorf("discord webhook is required")
	}

	results := make([]TestResult, 0, 2)

	res := TestResult{Label: "Sync webhook", Status: statusOK}
	if err := d.sendWebhook(ctx, runtime, mainWebhook, testTitle, testMessage("Discord"), testColor, nil, "", "", time.Time{}); err != nil {
		res.Status = statusError
		res.Error = err.Error()
	}
	results = append(results, res)

	if updatesWebhook != "" && updatesWebhook != mainWebhook {
		res := TestResult{Label: "Updates webhook", Status: statusOK}
		if err := d.sendWebhook(ctx, runtime, updatesWebhook, testTitle, testMessage("Discord"), testColor, nil, "", "", time.Time{}); err != nil {
			res.Status = statusError
			res.Error = err.Error()
		}
		results = append(results, res)
	}

	return results, nil
}

// Notify sends one outbound notification to the route-resolved webhook.
// When Payload.Detail is non-empty, sends a follow-up content-only
// message after the embed (matches bash tagarr.sh's two-step pattern:
// summary embed + detail listing).
func (d discordProvider) Notify(ctx context.Context, runtime Runtime, agent Agent, payload Payload) error {
	webhook := d.resolveWebhook(agent, payload.Route)
	if webhook == "" {
		return nil
	}
	// Prefer Fields when the caller populated them — gives a proper
	// Discord embed-fields-grid (Primary/Secondary as inline columns).
	// Falls back to Message-as-description for callers that haven't
	// adopted Fields yet (Gotify/NTFY/etc keep using Message).
	if err := d.sendWebhook(ctx, runtime, webhook, payload.Title, payload.messageFor("discord"), payload.Color, payload.Fields, payload.ThumbnailURL, payload.FooterSuffix, payload.Timestamp); err != nil {
		return err
	}
	if strings.TrimSpace(payload.Detail) != "" {
		if err := d.sendDetailContent(ctx, runtime, webhook, payload.Detail); err != nil {
			// Detail-send failure shouldn't fail the whole notification —
			// the user already got the embed with the summary.
			log.Printf("discord detail follow-up failed: %v", err)
		}
	}
	return nil
}

// sendDetailContent posts one or more content-only messages to the
// webhook with the Detail body. Discord caps content at 2000 chars per
// message; we chunk at 1800 to leave headroom for markdown overhead.
// Splits on newlines so code-block fences aren't broken mid-content.
func (discordProvider) sendDetailContent(ctx context.Context, runtime Runtime, webhook, detail string) error {
	if runtime.SafeClient == nil {
		return fmt.Errorf("discord client not configured")
	}
	const chunkLimit = 1800
	chunks := chunkDetail(detail, chunkLimit)
	for _, chunk := range chunks {
		body, err := json.Marshal(map[string]string{"content": chunk})
		if err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, "POST", webhook, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("discord detail request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := runtime.SafeClient.Do(req)
		if err != nil {
			return err
		}
		drainAndClose(resp)
		if resp.StatusCode >= 400 {
			return fmt.Errorf("discord detail HTTP %d", resp.StatusCode)
		}
	}
	return nil
}

// chunkDetail splits detail content into Discord-friendly chunks no
// larger than limit chars each. Splits on newline boundaries to keep
// markdown-block content intact. Each chunk that contains a "```"
// opener gets a balancing "```" appended if the closer landed in the
// next chunk; the next chunk gets its own opener prepended.
func chunkDetail(detail string, limit int) []string {
	if len(detail) <= limit {
		return []string{detail}
	}
	var chunks []string
	lines := strings.Split(detail, "\n")
	var cur strings.Builder
	for _, line := range lines {
		// +1 for the newline we'll add. If adding this line would push
		// us over, flush the current chunk first.
		if cur.Len() > 0 && cur.Len()+len(line)+1 > limit {
			chunks = append(chunks, cur.String())
			cur.Reset()
		}
		if cur.Len() > 0 {
			cur.WriteByte('\n')
		}
		cur.WriteString(line)
	}
	if cur.Len() > 0 {
		chunks = append(chunks, cur.String())
	}
	// Balance code-block fences: if any chunk has an unclosed ```, close
	// it; if the next chunk starts inside an unclosed block, prepend ```.
	for i := range chunks {
		opens := strings.Count(chunks[i], "```")
		if opens%2 == 1 {
			chunks[i] += "\n```"
			if i+1 < len(chunks) {
				chunks[i+1] = "```\n" + chunks[i+1]
			}
		}
	}
	return chunks
}

// resolveWebhook chooses the updates webhook for RouteUpdates, falling back to main.
func (discordProvider) resolveWebhook(agent Agent, route Route) string {
	if route == RouteUpdates {
		if webhook := strings.TrimSpace(agent.Config.DiscordWebhookUpdates); webhook != "" {
			return webhook
		}
	}
	return strings.TrimSpace(agent.Config.DiscordWebhook)
}

// sendWebhook posts one Discord embed to the given webhook URL.
// The embed includes a title, optional description, optional fields-grid
// (when caller populated Payload.Fields), colored sidebar, optional
// thumbnail (top-right poster image), and a footer. The footer always
// starts with "Resolvarr {version} by ProphetSe7en"; when footerSuffix
// is non-empty it is appended with " · " as separator (used by
// webhook-fire notifications to add the rule name without forcing
// callers to know the version string). Returns an error if the HTTP
// client is missing, the URL fails validation, or Discord responds
// with a 4xx/5xx status.
func (discordProvider) sendWebhook(ctx context.Context, runtime Runtime, webhook, title, description string, color int, fields []PayloadField, thumbnailURL, footerSuffix string, ts time.Time) error {
	if runtime.SafeClient == nil {
		return fmt.Errorf("discord client not configured")
	}

	webhook = strings.TrimSpace(webhook)
	if isLiteralPlaceholder(webhook) {
		return fmt.Errorf("discord webhook is the masked placeholder — open Settings → Notifications → Edit and re-enter the real webhook URL")
	}
	if !isDiscordWebhookURL(webhook) {
		return fmt.Errorf("discord webhook must start with https://discord.com/api/webhooks/ or https://discordapp.com/api/webhooks/")
	}

	footerText := "Resolvarr " + runtime.Version + " by ProphetSe7en"
	if suffix := strings.TrimSpace(footerSuffix); suffix != "" {
		footerText = footerText + " · " + suffix
	}
	embed := map[string]any{
		"title":  title,
		"color":  color,
		"footer": map[string]string{"text": footerText},
	}
	if !ts.IsZero() {
		// Discord expects RFC3339 (ISO8601 with timezone). UTC keeps
		// the wire payload locale-stable; clients render in viewer's
		// timezone via embed.timestamp's automatic locale handling.
		embed["timestamp"] = ts.UTC().Format(time.RFC3339)
	}
	if thumb := strings.TrimSpace(thumbnailURL); thumb != "" {
		embed["thumbnail"] = map[string]string{"url": thumb}
	}
	// Fields takes precedence over description: when the caller built a
	// rich fields-grid, the description (which is just the same data
	// flattened to plain text for Gotify/NTFY/etc) would render BELOW
	// the fields and duplicate the information visually. So Discord
	// gets fields-only when fields are present; description-only when
	// Fields is empty (fallback for callers who haven't adopted Fields).
	if len(fields) > 0 {
		out := make([]map[string]any, 0, len(fields))
		for _, f := range fields {
			out = append(out, map[string]any{
				"name":   f.Name,
				"value":  f.Value,
				"inline": f.Inline,
			})
		}
		embed["fields"] = out
	} else if description != "" {
		embed["description"] = description
	}
	body, err := json.Marshal(map[string]any{"embeds": []any{embed}})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", webhook, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("discord request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := runtime.SafeClient.Do(req)
	if err != nil {
		return err
	}
	defer drainAndClose(resp)

	if resp.StatusCode >= 400 {
		return httpError("discord", resp)
	}
	return nil
}

// isDiscordWebhookURL returns true when raw starts with an accepted Discord
// webhook API prefix. Both discord.com and the legacy discordapp.com domains
// are accepted.
func isDiscordWebhookURL(raw string) bool {
	return strings.HasPrefix(raw, "https://discord.com/api/webhooks/") ||
		strings.HasPrefix(raw, "https://discordapp.com/api/webhooks/")
}
