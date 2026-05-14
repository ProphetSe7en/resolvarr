package core

import (
	"encoding/json"
	"testing"
)

// TestWebhookRule_HasOwnWebhookURL covers the gate that the
// instance-URL dispatcher uses to skip per-rule-URL rules.
func TestWebhookRule_HasOwnWebhookURL(t *testing.T) {
	cases := []struct {
		name string
		rule WebhookRule
		want bool
	}{
		{"nil webhook", WebhookRule{}, false},
		{"empty token", WebhookRule{Webhook: &WebhookConfig{}}, false},
		{"token set", WebhookRule{Webhook: &WebhookConfig{Token: "abc123"}}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.rule.HasOwnWebhookURL(); got != c.want {
				t.Errorf("HasOwnWebhookURL = %v, want %v", got, c.want)
			}
		})
	}
}

// TestFindRuleByWebhookToken covers the lookup helper the per-rule
// receive endpoint uses to route POST /api/webhooks/rule/{token}.
func TestFindRuleByWebhookToken(t *testing.T) {
	rules := []WebhookRule{
		{ID: "r1", Webhook: &WebhookConfig{Token: "token-1"}},
		{ID: "r2"}, // no per-rule webhook
		{ID: "r3", Webhook: &WebhookConfig{Token: "token-3"}},
	}

	r, idx := FindRuleByWebhookToken(rules, "token-1")
	if r == nil || r.ID != "r1" || idx != 0 {
		t.Errorf("token-1 lookup: got %+v idx=%d, want r1 at idx 0", r, idx)
	}

	r, idx = FindRuleByWebhookToken(rules, "token-3")
	if r == nil || r.ID != "r3" || idx != 2 {
		t.Errorf("token-3 lookup: got %+v idx=%d, want r3 at idx 2", r, idx)
	}

	r, idx = FindRuleByWebhookToken(rules, "nonexistent")
	if r != nil || idx != -1 {
		t.Errorf("nonexistent lookup: got %+v idx=%d, want nil + -1", r, idx)
	}

	// Empty token must NEVER match — defence against a misconfigured
	// rule that somehow ended up with Webhook != nil + Token == "".
	r, idx = FindRuleByWebhookToken(rules, "")
	if r != nil || idx != -1 {
		t.Errorf("empty-token lookup: got %+v idx=%d, want nil + -1", r, idx)
	}
}

// TestWebhookRule_JSONRoundTrip_LegacyOmitsWebhookField — verifies
// that a rule without per-rule webhook config marshals WITHOUT the
// new field (omitempty), so on-disk JSON for legacy rules stays
// byte-compatible across the upgrade.
func TestWebhookRule_JSONRoundTrip_LegacyOmitsWebhookField(t *testing.T) {
	legacy := WebhookRule{
		ID:         "r1",
		Name:       "test",
		Enabled:    true,
		InstanceID: "inst-1",
		AppType:    "radarr",
		Functions:  []WebhookFunction{WebhookFnTagAudio},
	}
	b, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)
	if containsField(s, "\"webhook\"") {
		t.Errorf("legacy rule emitted webhook field — should be omitted: %s", s)
	}
}

// TestWebhookRule_JSONRoundTrip_WithWebhookSurvives — opt-in rule's
// Webhook struct must survive marshal/unmarshal intact.
func TestWebhookRule_JSONRoundTrip_WithWebhookSurvives(t *testing.T) {
	original := WebhookRule{
		ID:         "r1",
		Name:       "test",
		Enabled:    true,
		InstanceID: "inst-1",
		AppType:    "radarr",
		Functions:  []WebhookFunction{WebhookFnTagAudio},
		Webhook: &WebhookConfig{
			Token:            "abc123token",
			Secret:           "supersecret",
			RequireSignature: true,
		},
	}
	b, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded WebhookRule
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Webhook == nil {
		t.Fatal("decoded.Webhook == nil — round-trip lost the field")
	}
	if decoded.Webhook.Token != "abc123token" {
		t.Errorf("Token = %q, want abc123token", decoded.Webhook.Token)
	}
	if decoded.Webhook.Secret != "supersecret" {
		t.Errorf("Secret = %q, want supersecret", decoded.Webhook.Secret)
	}
	if !decoded.Webhook.RequireSignature {
		t.Errorf("RequireSignature = false, want true")
	}
}

// containsField is a tiny helper that lets the legacy-omitempty test
// avoid pulling in strings.Contains just for one check. Reads as
// "does the marshaled JSON include this exact field key".
func containsField(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
