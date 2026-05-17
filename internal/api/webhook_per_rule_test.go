package api

// webhook_per_rule_test.go — coverage for the per-rule webhook receive
// endpoint added in M-per-rule-webhook Slice 2. The endpoint routes
// POST /api/webhooks/rule/{token} directly to one rule via
// FindRuleByWebhookToken, runs the same execution pipeline as the
// instance dispatcher, but only fires that one rule.

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"resolvarr/internal/core"
)

// newPerRuleReceiveTestServer wires a Server with a single rule whose
// Webhook.Token is set + Webhook.Secret + RequireSignature configurable
// per test. Returns the server, store, and the token/secret strings so
// tests don't have to re-derive them.
func newPerRuleReceiveTestServer(t *testing.T, requireSig bool) (*Server, *core.ConfigStore, string, string) {
	t.Helper()
	dir := t.TempDir()
	store := core.NewConfigStore(dir)
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	const (
		ruleToken  = "rule-token-abc"
		ruleSecret = "rule-secret-xyz"
	)
	if err := store.Update(func(c *core.Config) {
		c.Instances = []core.Instance{{
			ID:     "inst-1",
			Name:   "Radarr",
			Type:   "radarr",
			URL:    "http://radarr.test:7878",
			APIKey: "key",
			Webhook: core.WebhookConfig{
				Token:          "inst-tok",
				LoggingEnabled: true,
			},
		}}
		c.WebhookRules = []core.WebhookRule{{
			ID:         "rule-1",
			Name:       "Test rule",
			Enabled:    true,
			InstanceID: "inst-1",
			AppType:    "radarr",
			Functions:  []core.WebhookFunction{core.WebhookFnTagAudio},
			Webhook: &core.WebhookConfig{
				Token:            ruleToken,
				Secret:           ruleSecret,
				RequireSignature: requireSig,
			},
		}}
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	s := &Server{App: &core.App{Config: store}}
	s.WebhookLog = newWebhookLog(dir + "/events.json")
	s.authLogLimiter = &authLogRateLimiter{}
	return s, store, ruleToken, ruleSecret
}

// postPerRule fires a POST against the per-rule receiver via the
// project's mux so SetPathValue gets handled correctly.
func postPerRule(t *testing.T, s *Server, token, body string, authVal string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/webhooks/rule/{ruleToken}", s.handleWebhookReceivePerRule)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/rule/"+token, strings.NewReader(body))
	if authVal != "" {
		req.Header.Set("Authorization", authVal)
	}
	mux.ServeHTTP(rr, req)
	return rr
}

func basicAuth(user, pass string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
}

// TestPerRuleReceive_UnknownToken_404 — bad/unknown token leaks less
// information than 403 (probe can't distinguish "rule deleted" from
// "wrong URL"). Matches the instance-receiver pattern.
func TestPerRuleReceive_UnknownToken_404(t *testing.T) {
	s, _, _, _ := newPerRuleReceiveTestServer(t, false)
	rr := postPerRule(t, s, "nonexistent-token", `{"eventType":"Test"}`, "")
	if rr.Code != http.StatusNotFound {
		t.Errorf("unknown token = %d, want 404", rr.Code)
	}
}

// TestPerRuleReceive_GraceMode_NoAuth_Succeeds — RequireSignature=false
// + no Authorization header passes. Same legacy/grace semantics as the
// instance receiver. Logs an (unsigned) nudge in the ring-buffer.
func TestPerRuleReceive_GraceMode_NoAuth_Succeeds(t *testing.T) {
	s, _, token, _ := newPerRuleReceiveTestServer(t, false)
	rr := postPerRule(t, s, token, `{"eventType":"Test"}`, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("grace + no auth = %d body=%s, want 200", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"status":"ok"`) {
		t.Errorf("body = %q, missing status:ok", rr.Body.String())
	}
	// Ring-buffer should have an (unsigned) warning entry.
	evs := s.WebhookLog.list("inst-1")
	foundUnsigned := false
	for _, e := range evs {
		if e.EventType == "(unsigned)" {
			foundUnsigned = true
			break
		}
	}
	if !foundUnsigned {
		t.Errorf("expected (unsigned) warning in ring-buffer, got events: %+v", evs)
	}
}

// TestPerRuleReceive_StrictMode_NoAuth_401 — RequireSignature=true +
// missing Authorization header rejects. Rejection logged.
func TestPerRuleReceive_StrictMode_NoAuth_401(t *testing.T) {
	s, _, token, _ := newPerRuleReceiveTestServer(t, true)
	rr := postPerRule(t, s, token, `{"eventType":"Test"}`, "")
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("strict + no auth = %d, want 401", rr.Code)
	}
	evs := s.WebhookLog.list("inst-1")
	foundRejected := false
	for _, e := range evs {
		if e.EventType == "(rejected)" && strings.Contains(e.Title, "Test rule") {
			foundRejected = true
			break
		}
	}
	if !foundRejected {
		t.Errorf("expected (rejected) ring-buffer entry for rule, got: %+v", evs)
	}
}

// TestPerRuleReceive_StrictMode_RightSecret_Succeeds — matching Basic
// auth in strict mode succeeds. Rule fires + a non-error response
// comes back.
func TestPerRuleReceive_StrictMode_RightSecret_Succeeds(t *testing.T) {
	s, _, token, secret := newPerRuleReceiveTestServer(t, true)
	rr := postPerRule(t, s, token, `{"eventType":"Test"}`, basicAuth("resolvarr", secret))
	if rr.Code != http.StatusOK {
		t.Fatalf("strict + right secret = %d body=%s, want 200", rr.Code, rr.Body.String())
	}
}

// TestPerRuleReceive_WrongSecret_401 — Basic auth with the wrong
// secret rejects regardless of strict-mode.
func TestPerRuleReceive_WrongSecret_401(t *testing.T) {
	s, _, token, _ := newPerRuleReceiveTestServer(t, true)
	rr := postPerRule(t, s, token, `{"eventType":"Test"}`, basicAuth("resolvarr", "WRONG"))
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("wrong secret = %d, want 401", rr.Code)
	}
}

// TestPerRuleReceive_RuleInstanceGone_404 — rule references a deleted
// instance. Receiver returns 404 so Sonarr/Radarr stops retrying.
func TestPerRuleReceive_RuleInstanceGone_404(t *testing.T) {
	s, store, token, _ := newPerRuleReceiveTestServer(t, false)
	if err := store.Update(func(c *core.Config) {
		c.Instances = nil
	}); err != nil {
		t.Fatalf("clear instances: %v", err)
	}
	rr := postPerRule(t, s, token, `{"eventType":"Test"}`, "")
	if rr.Code != http.StatusNotFound {
		t.Errorf("rule with deleted instance = %d, want 404", rr.Code)
	}
}

// TestDispatchWebhookRules_SkipsPerRuleURLRules — Slice 3 invariant:
// when a rule has its own per-rule webhook URL (HasOwnWebhookURL()),
// the instance dispatcher MUST skip it. Without this guard, both
// URLs (instance + per-rule) would fire the same rule on every event
// → double history entries + double tag-applications.
//
// Setup: two rules on the same instance, both with TagAudio enabled
// so a Download event qualifies both:
//   - Rule A: no per-rule URL → fires via instance dispatcher
//   - Rule B: per-rule URL set → MUST be skipped here
//
// We use Functions:[TagAudio] with a NIL AudioTags snapshot so the
// adapter takes its no-config early-return path without touching a
// real Arr. The rule's "qualified" gate still trips so it would
// produce a history entry IF executeWebhookRule ran — meaning
// presence/absence of a history entry is a reliable proxy for
// "did the dispatcher reach this rule".
func TestDispatchWebhookRules_SkipsPerRuleURLRules(t *testing.T) {
	dir := t.TempDir()
	store := core.NewConfigStore(dir)
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := store.Update(func(c *core.Config) {
		c.Instances = []core.Instance{{
			ID:     "inst-1",
			Name:   "Radarr",
			Type:   "radarr",
			URL:    "http://127.0.0.1:1", // reserved port — any arr call connection-refuses
			APIKey: "key",
		}}
		c.WebhookRules = []core.WebhookRule{
			{
				ID:         "rule-A-instance-url",
				Name:       "Instance-URL rule",
				Enabled:    true,
				InstanceID: "inst-1",
				AppType:    "radarr",
				Functions:  []core.WebhookFunction{core.WebhookFnTagAudio},
				// no Webhook → fires via instance dispatcher
			},
			{
				ID:         "rule-B-per-rule-url",
				Name:       "Per-rule-URL rule",
				Enabled:    true,
				InstanceID: "inst-1",
				AppType:    "radarr",
				Functions:  []core.WebhookFunction{core.WebhookFnTagAudio},
				Webhook: &core.WebhookConfig{
					Token:  "rule-b-token",
					Secret: "rule-b-secret",
				},
			},
		}
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// arrClientFor needs s.App.HTTPClient — without it the Arr probe
	// panics on nil-deref. Real HTTP client with short timeout so
	// connection-refuses to 127.0.0.1:1 fail immediately.
	s := &Server{App: &core.App{
		Config:     store,
		HTTPClient: &http.Client{Timeout: 2 * time.Second},
	}}

	env := &connectEventEnvelope{EventType: "Download"}
	body := []byte(`{"eventType":"Download"}`)
	inst := &store.Get().Instances[0]
	fired := s.dispatchWebhookRules(t.Context(), inst, env, body, "")

	// Exactly ONE rule should have fired — rule A. Rule B was skipped
	// because it has its own per-rule URL.
	if fired != 1 {
		t.Errorf("rules fired = %d, want exactly 1 (rule A; rule B has per-rule URL)", fired)
	}

	// Verify by history entry too: rule A has one entry, rule B has none.
	cfg := store.Get()
	for _, r := range cfg.WebhookRules {
		switch r.ID {
		case "rule-A-instance-url":
			if len(r.History) != 1 {
				t.Errorf("rule A history = %d, want 1 entry from instance dispatcher", len(r.History))
			}
		case "rule-B-per-rule-url":
			if len(r.History) != 0 {
				t.Errorf("rule B history = %d, want 0 — should have been excluded from instance dispatcher", len(r.History))
			}
		}
	}
}

// TestPerRuleReceive_DisabledRule_OkWithZeroFires — disabled rules
// return 200 + rulesFired:0 (NOT 404). Rationale: Sonarr/Radarr
// retry on 5xx but abandon on 4xx — returning 404 would make Sonarr
// give up entirely on a rule the user may re-enable later. Returning
// 200 + rulesFired:0 keeps the URL viable for re-enabling without
// having to reconfigure Sonarr Connect. The user sees the disabled-
// rule state in resolvarr's UI; Sonarr just gets a "delivered, did
// nothing" ack.
func TestPerRuleReceive_DisabledRule_OkWithZeroFires(t *testing.T) {
	s, store, token, _ := newPerRuleReceiveTestServer(t, false)
	if err := store.Update(func(c *core.Config) {
		c.WebhookRules[0].Enabled = false
	}); err != nil {
		t.Fatalf("disable rule: %v", err)
	}
	rr := postPerRule(t, s, token, `{"eventType":"Test"}`, "")
	// Disabled rule isn't handled by the receiver — dispatchSingleWebhookRule
	// returns 0 (no fire), but the receiver still returned 200 to ack.
	// Verify the response says rulesFired=0 rather than 404.
	if rr.Code != http.StatusOK {
		t.Fatalf("disabled rule receive = %d body=%s, want 200", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"rulesFired":0`) {
		t.Errorf("disabled rule body = %q, want rulesFired:0", rr.Body.String())
	}
}
