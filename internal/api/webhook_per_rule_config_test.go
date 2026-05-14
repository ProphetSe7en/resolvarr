package api

// webhook_per_rule_config_test.go — coverage for the per-rule webhook
// config CRUD endpoints added in M-per-rule-webhook Slice 4. The five
// endpoints under /api/webhook-rules/{id}/webhook (GET, generate,
// rotate-secret, require-signature, DELETE) manage the rule's own
// webhook URL config.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"resolvarr/internal/core"
)

// newPerRuleConfigTestServer wires a Server with a single rule (no
// Webhook config yet — the test will add one via /generate).
func newPerRuleConfigTestServer(t *testing.T) (*Server, *core.ConfigStore, string) {
	t.Helper()
	dir := t.TempDir()
	store := core.NewConfigStore(dir)
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	const ruleID = "rule-1"
	if err := store.Update(func(c *core.Config) {
		c.Instances = []core.Instance{{ID: "inst-1", Name: "Radarr", Type: "radarr"}}
		c.WebhookRules = []core.WebhookRule{{
			ID:         ruleID,
			Name:       "Test rule",
			Enabled:    true,
			InstanceID: "inst-1",
			AppType:    "radarr",
			Functions:  []core.WebhookFunction{core.WebhookFnTagAudio},
		}}
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return &Server{App: &core.App{Config: store}}, store, ruleID
}

func setRulePath(req *http.Request, id string) *http.Request {
	req.SetPathValue("id", id)
	return req
}

// TestGetPerRuleWebhook_EmptyWhenNotConfigured — fresh rule returns
// empty fields, signalling "Generate URL" CTA in the UI.
func TestGetPerRuleWebhook_EmptyWhenNotConfigured(t *testing.T) {
	s, _, id := newPerRuleConfigTestServer(t)
	req := setRulePath(httptest.NewRequest(http.MethodGet, "/api/webhook-rules/"+id+"/webhook", nil), id)
	rr := httptest.NewRecorder()
	s.handleGetPerRuleWebhook(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var resp perRuleWebhookGetResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Token != "" || resp.URL != "" {
		t.Errorf("unconfigured rule returned non-empty token/URL: %+v", resp)
	}
}

// TestGeneratePerRuleWebhook_FirstCall — stamps fresh Token + Secret
// + computed URL on a rule that had none. RequireSignature defaults
// to false.
func TestGeneratePerRuleWebhook_FirstCall(t *testing.T) {
	s, store, id := newPerRuleConfigTestServer(t)
	req := setRulePath(httptest.NewRequest(http.MethodPost, "/api/webhook-rules/"+id+"/webhook/generate", nil), id)
	req.Host = "resolvarr.test:6075"
	rr := httptest.NewRecorder()
	s.handleGeneratePerRuleWebhook(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var resp perRuleWebhookGetResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Token == "" {
		t.Error("Token empty after generate")
	}
	if resp.Secret == "" {
		t.Error("Secret empty after generate")
	}
	if !strings.Contains(resp.URL, "/api/webhooks/rule/") || !strings.Contains(resp.URL, resp.Token) {
		t.Errorf("URL = %q, expected /api/webhooks/rule/<token>", resp.URL)
	}
	// Storage verification
	rule := findWebhookRuleByID(store.Get(), id)
	if rule.Webhook == nil || rule.Webhook.Token != resp.Token || rule.Webhook.Secret != resp.Secret {
		t.Errorf("stored Webhook mismatch: %+v vs resp %+v", rule.Webhook, resp)
	}
}

// TestGeneratePerRuleWebhook_RotatesPreservesRequireSignature — calling
// generate on an already-configured rule rotates Token + Secret but
// keeps RequireSignature unchanged.
func TestGeneratePerRuleWebhook_RotatesPreservesRequireSignature(t *testing.T) {
	s, store, id := newPerRuleConfigTestServer(t)
	// Seed an existing Webhook config with RequireSignature=true.
	if err := store.Update(func(c *core.Config) {
		c.WebhookRules[0].Webhook = &core.WebhookConfig{
			Token:            "old-token",
			Secret:           "old-secret",
			RequireSignature: true,
		}
	}); err != nil {
		t.Fatalf("seed webhook: %v", err)
	}

	req := setRulePath(httptest.NewRequest(http.MethodPost, "/api/webhook-rules/"+id+"/webhook/generate", nil), id)
	rr := httptest.NewRecorder()
	s.handleGeneratePerRuleWebhook(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}

	var resp perRuleWebhookGetResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Token == "old-token" {
		t.Errorf("Token not rotated: still %q", resp.Token)
	}
	if !resp.RequireSignature {
		t.Errorf("RequireSignature flipped off on rotation — should preserve previous value")
	}
}

// TestRotatePerRuleSecret_KeepsToken — rotate-secret rotates Secret
// but keeps Token + URL stable. User doesn't have to re-paste the URL
// in Sonarr/Radarr.
func TestRotatePerRuleSecret_KeepsToken(t *testing.T) {
	s, store, id := newPerRuleConfigTestServer(t)
	if err := store.Update(func(c *core.Config) {
		c.WebhookRules[0].Webhook = &core.WebhookConfig{
			Token:  "stable-token",
			Secret: "old-secret",
		}
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	req := setRulePath(httptest.NewRequest(http.MethodPost, "/api/webhook-rules/"+id+"/webhook/rotate-secret", nil), id)
	rr := httptest.NewRecorder()
	s.handleRotatePerRuleWebhookSecret(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	rule := findWebhookRuleByID(store.Get(), id)
	if rule.Webhook.Token != "stable-token" {
		t.Errorf("Token mutated by rotate-secret: %q", rule.Webhook.Token)
	}
	if rule.Webhook.Secret == "old-secret" {
		t.Errorf("Secret not rotated")
	}
}

// TestRotatePerRuleSecret_RejectsWhenUnconfigured — calling rotate-
// secret on a rule that has no Webhook config returns 409.
func TestRotatePerRuleSecret_RejectsWhenUnconfigured(t *testing.T) {
	s, _, id := newPerRuleConfigTestServer(t)
	req := setRulePath(httptest.NewRequest(http.MethodPost, "/api/webhook-rules/"+id+"/webhook/rotate-secret", nil), id)
	rr := httptest.NewRecorder()
	s.handleRotatePerRuleWebhookSecret(rr, req)
	if rr.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409 (no webhook configured)", rr.Code)
	}
}

// TestSetRequireSignature_RejectsWithoutSecret — enabling strict
// mode requires a Secret. The instance equivalent at webhooks.go:803
// enforces the same rule.
func TestSetRequireSignature_RejectsWithoutSecret(t *testing.T) {
	s, store, id := newPerRuleConfigTestServer(t)
	if err := store.Update(func(c *core.Config) {
		c.WebhookRules[0].Webhook = &core.WebhookConfig{
			Token:  "tok",
			Secret: "", // intentionally empty
		}
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	body := strings.NewReader(`{"enabled":true}`)
	req := setRulePath(httptest.NewRequest(http.MethodPut, "/api/webhook-rules/"+id+"/webhook/require-signature", body), id)
	rr := httptest.NewRecorder()
	s.handleSetPerRuleWebhookRequireSignature(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (cannot enable strict mode without Secret)", rr.Code)
	}
}

// TestSetRequireSignature_WithSecret_Succeeds — happy path.
func TestSetRequireSignature_WithSecret_Succeeds(t *testing.T) {
	s, store, id := newPerRuleConfigTestServer(t)
	if err := store.Update(func(c *core.Config) {
		c.WebhookRules[0].Webhook = &core.WebhookConfig{Token: "t", Secret: "s"}
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	body := strings.NewReader(`{"enabled":true}`)
	req := setRulePath(httptest.NewRequest(http.MethodPut, "/api/webhook-rules/"+id+"/webhook/require-signature", body), id)
	rr := httptest.NewRecorder()
	s.handleSetPerRuleWebhookRequireSignature(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	rule := findWebhookRuleByID(store.Get(), id)
	if !rule.Webhook.RequireSignature {
		t.Errorf("RequireSignature not persisted")
	}
}

// TestDeletePerRuleWebhook_ClearsConfig — drops the rule back to
// instance-URL routing. After DELETE, HasOwnWebhookURL() returns
// false → the instance dispatcher includes it again.
func TestDeletePerRuleWebhook_ClearsConfig(t *testing.T) {
	s, store, id := newPerRuleConfigTestServer(t)
	if err := store.Update(func(c *core.Config) {
		c.WebhookRules[0].Webhook = &core.WebhookConfig{
			Token:            "tok",
			Secret:           "sec",
			RequireSignature: true,
		}
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	req := setRulePath(httptest.NewRequest(http.MethodDelete, "/api/webhook-rules/"+id+"/webhook", nil), id)
	rr := httptest.NewRecorder()
	s.handleDeletePerRuleWebhook(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	rule := findWebhookRuleByID(store.Get(), id)
	if rule.Webhook != nil {
		t.Errorf("Webhook not cleared: %+v", rule.Webhook)
	}
	if rule.HasOwnWebhookURL() {
		t.Error("HasOwnWebhookURL still true after delete")
	}
}

// TestPerRuleConfig_UnknownRuleID_404 — all five endpoints should
// return 404 on a non-existent rule.
func TestPerRuleConfig_UnknownRuleID_404(t *testing.T) {
	s, _, _ := newPerRuleConfigTestServer(t)
	mkReq := func(method, path string) *http.Request {
		req := httptest.NewRequest(method, path, nil)
		req.SetPathValue("id", "nonexistent")
		return req
	}
	cases := []struct {
		name    string
		handler func(http.ResponseWriter, *http.Request)
		req     *http.Request
	}{
		{"get", s.handleGetPerRuleWebhook, mkReq(http.MethodGet, "/api/webhook-rules/nonexistent/webhook")},
		{"generate", s.handleGeneratePerRuleWebhook, mkReq(http.MethodPost, "/api/webhook-rules/nonexistent/webhook/generate")},
		{"rotate-secret", s.handleRotatePerRuleWebhookSecret, mkReq(http.MethodPost, "/api/webhook-rules/nonexistent/webhook/rotate-secret")},
		{"require-signature", s.handleSetPerRuleWebhookRequireSignature, mkReq(http.MethodPut, "/api/webhook-rules/nonexistent/webhook/require-signature")},
		{"delete", s.handleDeletePerRuleWebhook, mkReq(http.MethodDelete, "/api/webhook-rules/nonexistent/webhook")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			c.handler(rr, c.req)
			if rr.Code != http.StatusNotFound {
				t.Errorf("%s on missing rule = %d, want 404", c.name, rr.Code)
			}
		})
	}
}
