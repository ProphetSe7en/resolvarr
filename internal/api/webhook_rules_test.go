package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"resolvarr/internal/core"
)

// TestWebhookRuleNotifyOnFireValidate is the slimmed-down validator
// test after the power-to-the-agent pivot. NotifyAgents +
// NotifyOnEveryEvent are retired — only NotifyOnFire remains as
// the per-rule master kill-switch. Which agents see what is purely
// agents.Agent.Functions territory now.
func TestWebhookRuleNotifyOnFireValidate(t *testing.T) {
	dir := t.TempDir()
	store := core.NewConfigStore(dir)
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := store.Update(func(c *core.Config) {
		c.Instances = []core.Instance{{ID: "arr1", Name: "Radarr", Type: "radarr", URL: "http://x", APIKey: "k"}}
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cfg := store.Get()

	mk := func(notify bool) *webhookRuleRequest {
		return &webhookRuleRequest{
			Name: "test", Enabled: true, InstanceID: "arr1", AppType: "radarr",
			Functions:    []core.WebhookFunction{core.WebhookFnTagAudio},
			NotifyOnFire: notify,
		}
	}

	t.Run("NotifyOnFire=false validates", func(t *testing.T) {
		if err := mk(false).validate(cfg); err != nil {
			t.Errorf("NotifyOnFire=false rule rejected: %v", err)
		}
	})

	t.Run("NotifyOnFire=true validates", func(t *testing.T) {
		if err := mk(true).validate(cfg); err != nil {
			t.Errorf("NotifyOnFire=true rule rejected: %v", err)
		}
	})
}

// TestWebhookRuleApplyToNotifyOnFire locks the field's persistence
// through the request → rule transformation.
func TestWebhookRuleApplyToNotifyOnFire(t *testing.T) {
	for _, on := range []bool{false, true} {
		t.Run(map[bool]string{true: "true persists", false: "false persists"}[on], func(t *testing.T) {
			req := &webhookRuleRequest{
				Name:         "x",
				Enabled:      true,
				InstanceID:   "arr1",
				AppType:      "radarr",
				Functions:    []core.WebhookFunction{core.WebhookFnTagAudio},
				NotifyOnFire: on,
			}
			rule := core.WebhookRule{}
			req.applyTo(&rule, false)
			if rule.NotifyOnFire != on {
				t.Errorf("NotifyOnFire = %v after applyTo, want %v", rule.NotifyOnFire, on)
			}
		})
	}
}

// TestWebhookRuleNotifyJSONOmitemptyContract pins the on-disk shape:
// NotifyOnFire is omitempty so legacy rules (no key) deserialise to
// false (silent, no surprise notifications on upgrade); zero-value
// re-marshals without the key. Critical for backward compat.
//
// Also pins that NotifyAgents + NotifyOnEveryEvent are NOT in the
// WebhookRule struct anymore — legacy on-disk rules that had them
// from 7.4a-7.4c silently lose the keys on re-save. That's
// intentional: option A retired both fields.
func TestWebhookRuleNotifyJSONOmitemptyContract(t *testing.T) {
	// (a) Empty JSON object → zero-value fields.
	var legacy core.WebhookRule
	if err := json.Unmarshal([]byte(`{}`), &legacy); err != nil {
		t.Fatalf("unmarshal empty: %v", err)
	}
	if legacy.NotifyOnFire {
		t.Errorf("legacy rule NotifyOnFire = true, want false (no surprise notifications on upgrade)")
	}

	// (b) Legacy on-disk rules with the retired fields decode cleanly
	// — unknown fields are ignored by encoding/json, no error. The
	// rule lands with NotifyOnFire only; the retired fields are gone.
	legacyWithRetired := []byte(`{
		"id":"r1","name":"old","enabled":true,
		"notifyOnFire":true,
		"notifyAgents":["agent-1","agent-2"],
		"notifyOnEveryEvent":true
	}`)
	var migrated core.WebhookRule
	if err := json.Unmarshal(legacyWithRetired, &migrated); err != nil {
		t.Fatalf("unmarshal legacy: %v", err)
	}
	if !migrated.NotifyOnFire {
		t.Errorf("NotifyOnFire failed to roundtrip from legacy JSON")
	}

	// (c) Zero-value struct re-marshals with no notification keys.
	// Retired field names must NEVER appear in fresh writes — they're
	// gone from the struct so the marshaller can't emit them.
	rule := core.WebhookRule{ID: "abc", Name: "n", Enabled: true}
	out, err := json.Marshal(rule)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(out)
	for _, key := range []string{`"notifyOnFire"`, `"notifyOnEveryEvent"`, `"notifyAgents"`} {
		if strings.Contains(body, key) {
			t.Errorf("zero-value rule serialised retired/empty key %s; body: %s", key, body)
		}
	}
}

// TestWebhookRule_PerRuleWebhookCreds_MaskedInListAndGet verifies that
// the broad rule-listing + single-rule-fetch endpoints mask the
// per-rule Webhook.Token + Webhook.Secret bearer credentials. The
// plain values stay reachable via the dedicated /webhook endpoint
// (handleGetPerRuleWebhook) where the admin copies them into
// Sonarr/Radarr — but they must NEVER leak through the broader rule
// surface the wizard polls for the rule grid.
//
// Locks the security-audit finding: pre-fix v0.6.8-dev shipped with
// handleListWebhookRules + handleGetWebhookRule returning the rule
// struct verbatim, including any populated Webhook substruct.
func TestWebhookRule_PerRuleWebhookCreds_MaskedInListAndGet(t *testing.T) {
	dir := t.TempDir()
	store := core.NewConfigStore(dir)
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	app := &core.App{Config: store}
	s := &Server{App: app}

	// Seed two rules: one without per-rule webhook (Webhook nil), one
	// with per-rule webhook fully populated. Both should round-trip
	// without leaking the Token + Secret.
	const realToken = "0123456789abcdef0123456789abcdef"
	const realSecret = "secret-must-not-leak-deadbeef-0123"
	if err := store.Update(func(c *core.Config) {
		c.WebhookRules = []core.WebhookRule{
			{ID: "rule-no-webhook", Name: "no per-rule webhook", Enabled: true, InstanceID: "i1", AppType: "radarr"},
			{ID: "rule-with-webhook", Name: "per-rule webhook", Enabled: true, InstanceID: "i1", AppType: "radarr",
				Webhook: &core.WebhookConfig{Token: realToken, Secret: realSecret, RequireSignature: true}},
		}
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// 1. handleListWebhookRules — the per-rule Token + Secret must be
	// masked. The other rule's missing Webhook stays nil.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/webhook-rules", nil)
	s.handleListWebhookRules(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("List status %d, body %s", rr.Code, rr.Body.String())
	}
	listed := rr.Body.String()
	for _, leak := range []string{realToken, realSecret} {
		if strings.Contains(listed, leak) {
			t.Errorf("List response leaked %q in: %s", leak, listed)
		}
	}
	if !strings.Contains(listed, maskSentinel) {
		t.Errorf("List response missing masked sentinel for the seeded rule; body: %s", listed)
	}

	// 2. handleGetWebhookRule for the rule with per-rule webhook — same
	// masking story.
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/webhook-rules/rule-with-webhook", nil)
	req.SetPathValue("id", "rule-with-webhook")
	s.handleGetWebhookRule(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("Get status %d, body %s", rr.Code, rr.Body.String())
	}
	got := rr.Body.String()
	for _, leak := range []string{realToken, realSecret} {
		if strings.Contains(got, leak) {
			t.Errorf("Get response leaked %q in: %s", leak, got)
		}
	}
	if !strings.Contains(got, maskSentinel) {
		t.Errorf("Get response missing masked sentinel; body: %s", got)
	}

	// 3. On-disk state must still hold the plaintext — masking is for
	// the response only, never the store. Critical invariant: mask
	// without ever dropping the real value.
	stored := store.Get().WebhookRules
	var found *core.WebhookRule
	for i := range stored {
		if stored[i].ID == "rule-with-webhook" {
			found = &stored[i]
			break
		}
	}
	if found == nil || found.Webhook == nil {
		t.Fatalf("on-disk rule lost its Webhook substruct: %+v", stored)
	}
	if found.Webhook.Token != realToken {
		t.Errorf("on-disk Token = %q, want %q (masking must NOT mutate the store)", found.Webhook.Token, realToken)
	}
	if found.Webhook.Secret != realSecret {
		t.Errorf("on-disk Secret = %q, want %q", found.Webhook.Secret, realSecret)
	}
	if !found.Webhook.RequireSignature {
		t.Errorf("on-disk RequireSignature flipped during round-trip")
	}

	// 4. RequireSignature (non-credential) must still surface to the
	// caller — masking is targeted at bearer creds only.
	var rules []core.WebhookRule
	if err := json.Unmarshal([]byte(listed), &rules); err != nil {
		t.Fatalf("re-decode list: %v", err)
	}
	for _, r := range rules {
		if r.ID == "rule-with-webhook" {
			if r.Webhook == nil {
				t.Fatal("Webhook substruct dropped from listed rule")
			}
			if !r.Webhook.RequireSignature {
				t.Errorf("RequireSignature was masked too aggressively; non-credential metadata must survive")
			}
			if r.Webhook.Token != maskSentinel || r.Webhook.Secret != maskSentinel {
				t.Errorf("Token/Secret should be sentinel, got Token=%q Secret=%q", r.Webhook.Token, r.Webhook.Secret)
			}
		}
	}
}
