package api

// webhook_per_rule_config.go — UI-side CRUD endpoints for managing a
// rule's per-rule webhook URL config (M-per-rule-webhook Slice 4).
//
// Endpoints (all under /api/webhook-rules/{id}/webhook):
//
//	GET    /                       — return current per-rule webhook
//	                                  config + computed URL (empty when
//	                                  not configured)
//	POST   /generate                — generate fresh Token + Secret. Idempotent
//	                                  (overwrites if already configured).
//	POST   /rotate-secret           — rotate Secret only, keep Token + URL
//	PUT    /require-signature       — flip strict mode
//	DELETE /                       — disable per-rule URL, drop back to
//	                                  instance routing
//
// Auth: standard UI session-cookie. These manage the rule's webhook
// credentials; the rule's actual receive path is at /api/webhooks/rule/
// {token} and uses its own X-API-Key-style Basic-auth (Slice 2).

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"resolvarr/internal/core"
)

// perRuleWebhookGetResponse is the shape returned by GET. Empty Token
// means the rule has no per-rule URL yet — UI shows "Generate URL" CTA.
type perRuleWebhookGetResponse struct {
	Token            string `json:"token"`
	Secret           string `json:"secret"`
	RequireSignature bool   `json:"requireSignature"`
	URL              string `json:"url"`
}

// handleGetPerRuleWebhook returns the rule's per-rule webhook config
// + the computed full URL. The wizard / rule-card modal reads this
// to render the curl + secret. Secret is unmasked here (same as the
// instance-level GET) because the user needs it to paste into
// Sonarr/Radarr.
func (s *Server) handleGetPerRuleWebhook(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, 400, "id required")
		return
	}
	cfg := s.App.Config.Get()
	rule := findWebhookRuleByID(cfg, id)
	if rule == nil {
		writeError(w, 404, "rule not found")
		return
	}
	resp := perRuleWebhookGetResponse{}
	if rule.Webhook != nil {
		resp.Token = rule.Webhook.Token
		resp.Secret = rule.Webhook.Secret
		resp.RequireSignature = rule.Webhook.RequireSignature
		if rule.Webhook.Token != "" {
			resp.URL = buildPerRuleWebhookURL(r, rule.Webhook.Token)
		}
	}
	writeJSON(w, resp)
}

// handleGeneratePerRuleWebhook stamps a fresh Token + Secret on the
// rule's Webhook config (creating it if nil). Idempotent — calling
// twice rotates the URL + Secret (the user clicks "Generate URL"
// again to refresh after a suspected leak). After this call:
//
//   - The rule's instance-URL dispatcher entry is excluded (Slice 3)
//   - Sonarr/Radarr Connect-webhook must be updated with the new URL
//     OR the user can re-rotate to keep the original URL stable + only
//     change the Secret via /rotate-secret
//
// RequireSignature stays at its previous value when re-rotating; if
// the rule was already strict-mode, it remains strict after rotation
// — same instance-rotate semantics in webhooks.go:633.
func (s *Server) handleGeneratePerRuleWebhook(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, 400, "id required")
		return
	}
	cfg := s.App.Config.Get()
	if findWebhookRuleByID(cfg, id) == nil {
		writeError(w, 404, "rule not found")
		return
	}
	token, err := generateWebhookToken()
	if err != nil {
		writeError(w, 500, "generate token: "+err.Error())
		return
	}
	secret, err := generateWebhookSecret()
	if err != nil {
		writeError(w, 500, "generate secret: "+err.Error())
		return
	}
	// Token-collision defence — vanishingly unlikely with 32-byte
	// crypto/rand (~ 256 bits of entropy) but cheap to check.
	// Re-roll on collision; second collision in a row would indicate
	// rand.Read isn't seeding properly, surface as 500.
	for attempt := 0; attempt < 3; attempt++ {
		if r2, _ := core.FindRuleByWebhookToken(cfg.WebhookRules, token); r2 == nil || r2.ID == id {
			break
		}
		token, err = generateWebhookToken()
		if err != nil {
			writeError(w, 500, "generate token (retry): "+err.Error())
			return
		}
	}

	if err := s.App.Config.Update(func(c *core.Config) {
		for i := range c.WebhookRules {
			if c.WebhookRules[i].ID == id {
				if c.WebhookRules[i].Webhook == nil {
					c.WebhookRules[i].Webhook = &core.WebhookConfig{}
				}
				c.WebhookRules[i].Webhook.Token = token
				c.WebhookRules[i].Webhook.Secret = secret
				return
			}
		}
	}); err != nil {
		writeError(w, 500, "save: "+err.Error())
		return
	}
	// Re-read so the response reflects the persisted RequireSignature
	// (preserved across rotation).
	cfg = s.App.Config.Get()
	rule := findWebhookRuleByID(cfg, id)
	resp := perRuleWebhookGetResponse{
		Token:  token,
		Secret: secret,
		URL:    buildPerRuleWebhookURL(r, token),
	}
	if rule != nil && rule.Webhook != nil {
		resp.RequireSignature = rule.Webhook.RequireSignature
	}
	writeJSON(w, resp)
}

// handleRotatePerRuleWebhookSecret rotates the Secret only — Token
// stays the same so the user doesn't have to re-paste the URL into
// Sonarr/Radarr. They DO have to re-paste the Secret as the new
// Webhook password in Sonarr/Radarr's Connect config. RequireSignature
// preserved.
func (s *Server) handleRotatePerRuleWebhookSecret(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, 400, "id required")
		return
	}
	cfg := s.App.Config.Get()
	rule := findWebhookRuleByID(cfg, id)
	if rule == nil {
		writeError(w, 404, "rule not found")
		return
	}
	if rule.Webhook == nil || rule.Webhook.Token == "" {
		writeError(w, 409, "per-rule webhook not configured — call /generate first")
		return
	}
	secret, err := generateWebhookSecret()
	if err != nil {
		writeError(w, 500, "generate secret: "+err.Error())
		return
	}
	if err := s.App.Config.Update(func(c *core.Config) {
		for i := range c.WebhookRules {
			if c.WebhookRules[i].ID == id && c.WebhookRules[i].Webhook != nil {
				c.WebhookRules[i].Webhook.Secret = secret
				return
			}
		}
	}); err != nil {
		writeError(w, 500, "save: "+err.Error())
		return
	}
	writeJSON(w, map[string]any{
		"secret": secret,
		"url":    buildPerRuleWebhookURL(r, rule.Webhook.Token),
	})
}

// handleSetPerRuleWebhookRequireSignature flips the strict-mode flag.
// Body: `{"enabled": bool}`. Same validator-rule as the instance-level
// equivalent: enabling strict mode requires a non-empty Secret —
// otherwise the receiver would fail-close on every event with
// "rule has no Secret configured".
func (s *Server) handleSetPerRuleWebhookRequireSignature(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, 400, "id required")
		return
	}
	cfg := s.App.Config.Get()
	rule := findWebhookRuleByID(cfg, id)
	if rule == nil {
		writeError(w, 404, "rule not found")
		return
	}
	if rule.Webhook == nil || rule.Webhook.Token == "" {
		writeError(w, 409, "per-rule webhook not configured — call /generate first")
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
		writeError(w, 400, "invalid body: "+err.Error())
		return
	}
	if req.Enabled && rule.Webhook.Secret == "" {
		writeError(w, 400, "cannot enable strict mode — generate a Secret first via /rotate-secret")
		return
	}
	if err := s.App.Config.Update(func(c *core.Config) {
		for i := range c.WebhookRules {
			if c.WebhookRules[i].ID == id && c.WebhookRules[i].Webhook != nil {
				c.WebhookRules[i].Webhook.RequireSignature = req.Enabled
				return
			}
		}
	}); err != nil {
		writeError(w, 500, "save: "+err.Error())
		return
	}
	writeJSON(w, map[string]any{"requireSignature": req.Enabled})
}

// handleDeletePerRuleWebhook clears the rule's per-rule webhook
// config. After this:
//   - The dedicated URL stops working (returns 404 to any incoming
//     event from Sonarr/Radarr)
//   - The rule reverts to firing via the instance URL alongside
//     sibling rules — the instance-dispatcher exclusion guard in
//     Slice 3 no longer applies because rule.HasOwnWebhookURL() is
//     false
//
// Idempotent — 200 whether or not the rule had a webhook config.
// User responsibility: remove the now-dead webhook from Sonarr/
// Radarr's Connect settings, otherwise that side keeps trying to
// deliver to a 404'd URL until it gives up.
func (s *Server) handleDeletePerRuleWebhook(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, 400, "id required")
		return
	}
	cfg := s.App.Config.Get()
	if findWebhookRuleByID(cfg, id) == nil {
		writeError(w, 404, "rule not found")
		return
	}
	if err := s.App.Config.Update(func(c *core.Config) {
		for i := range c.WebhookRules {
			if c.WebhookRules[i].ID == id {
				c.WebhookRules[i].Webhook = nil
				return
			}
		}
	}); err != nil {
		writeError(w, 500, "save: "+err.Error())
		return
	}
	writeJSON(w, map[string]any{"status": "ok"})
}

// buildPerRuleWebhookURL renders the full URL Sonarr/Radarr will POST
// to. Uses the request's Host header — same pattern as the instance-
// level URL builder at webhooks.go:692.
func buildPerRuleWebhookURL(r *http.Request, token string) string {
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s/api/webhooks/rule/%s", scheme, r.Host, token)
}
