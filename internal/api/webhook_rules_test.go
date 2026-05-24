package api

import (
	"encoding/json"
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
