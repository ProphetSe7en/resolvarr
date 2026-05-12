package api

// webhooks_auth_test.go — coverage for the shared-secret-as-Basic-auth
// validation introduced for M-Webhook Phase 2 Slices A + B. The
// receiver path's auth gate must satisfy every state in the
// (RequireSignature, header-present, header-matches) truth table:
//
//   - RequireSignature=false + no header        → pass (legacy/grace)
//   - RequireSignature=false + matching header  → pass
//   - RequireSignature=false + wrong header     → fail (user tried)
//   - RequireSignature=true  + no header        → fail
//   - RequireSignature=true  + matching header  → pass
//   - RequireSignature=true  + wrong header     → fail
//   - Malformed Basic credentials               → fail
//   - Empty stored Secret + strict mode         → fail (defense in depth)

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"resolvarr/internal/core"
)

// authHeader builds a properly-formatted Authorization: Basic value
// for the (user, pass) pair. Matches what Sonarr/Radarr emit when the
// user fills in Webhook → username + password.
func authHeader(user, pass string) string {
	encoded := base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
	return "Basic " + encoded
}

func TestValidateWebhookAuth_GraceMode_NoHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/tok", nil)
	ok, reason := validateWebhookAuth(req, "shhh-very-secret", false)
	if !ok {
		t.Errorf("grace mode with no header should pass, got fail (reason=%q)", reason)
	}
}

func TestValidateWebhookAuth_GraceMode_WrongHeader(t *testing.T) {
	// User clearly tried to authenticate (header present) but supplied
	// a wrong password. We reject — silently accepting a wrong-secret
	// request would mask a real misconfiguration.
	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/tok", nil)
	req.Header.Set("Authorization", authHeader("resolvarr", "WRONG-SECRET"))
	ok, reason := validateWebhookAuth(req, "shhh-very-secret", false)
	if ok {
		t.Errorf("grace mode with wrong header should fail (mismatched explicit auth attempt)")
	}
	if !strings.Contains(reason, "does not match") {
		t.Errorf("reason should mention mismatch, got %q", reason)
	}
}

func TestValidateWebhookAuth_GraceMode_RightHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/tok", nil)
	req.Header.Set("Authorization", authHeader("resolvarr", "shhh-very-secret"))
	ok, reason := validateWebhookAuth(req, "shhh-very-secret", false)
	if !ok {
		t.Errorf("grace mode with matching header should pass, got fail (reason=%q)", reason)
	}
}

func TestValidateWebhookAuth_StrictMode_NoHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/tok", nil)
	ok, reason := validateWebhookAuth(req, "shhh-very-secret", true)
	if ok {
		t.Errorf("strict mode with no header should fail")
	}
	if !strings.Contains(reason, "missing") {
		t.Errorf("reason should mention missing header, got %q", reason)
	}
}

func TestValidateWebhookAuth_StrictMode_WrongHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/tok", nil)
	req.Header.Set("Authorization", authHeader("resolvarr", "WRONG-SECRET"))
	ok, reason := validateWebhookAuth(req, "shhh-very-secret", true)
	if ok {
		t.Errorf("strict mode with wrong header should fail")
	}
	if !strings.Contains(reason, "does not match") {
		t.Errorf("reason should mention mismatch, got %q", reason)
	}
}

func TestValidateWebhookAuth_StrictMode_RightHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/tok", nil)
	req.Header.Set("Authorization", authHeader("any-username-works", "shhh-very-secret"))
	ok, reason := validateWebhookAuth(req, "shhh-very-secret", true)
	if !ok {
		t.Errorf("strict mode with matching header should pass, got fail (reason=%q)", reason)
	}
}

func TestValidateWebhookAuth_MalformedBase64(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/tok", nil)
	req.Header.Set("Authorization", "Basic this-is-not-base64!!!")
	ok, reason := validateWebhookAuth(req, "shhh-very-secret", true)
	if ok {
		t.Errorf("malformed base64 should fail")
	}
	if !strings.Contains(reason, "base64") {
		t.Errorf("reason should mention base64, got %q", reason)
	}
}

func TestValidateWebhookAuth_BasicWithoutColon(t *testing.T) {
	// "noColonHere" base64-encoded — decodes cleanly but doesn't split
	// on ":" so we can't extract user/pass.
	encoded := base64.StdEncoding.EncodeToString([]byte("noColonHere"))
	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/tok", nil)
	req.Header.Set("Authorization", "Basic "+encoded)
	ok, reason := validateWebhookAuth(req, "shhh-very-secret", true)
	if ok {
		t.Errorf("Basic without colon should fail")
	}
	if !strings.Contains(reason, "format") {
		t.Errorf("reason should mention format, got %q", reason)
	}
}

func TestValidateWebhookAuth_NotBasicScheme(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/tok", nil)
	req.Header.Set("Authorization", "Bearer some-token")
	ok, reason := validateWebhookAuth(req, "shhh-very-secret", true)
	if ok {
		t.Errorf("Bearer scheme should fail")
	}
	if !strings.Contains(reason, "Basic") {
		t.Errorf("reason should mention Basic, got %q", reason)
	}
}

// TestValidateWebhookAuth_BasicLowercase locks RFC 7235 §2.1 case-
// insensitive scheme matching: a "basic <b64>" header with valid
// credentials must pass. Sonarr/Radarr emit canonical "Basic " today
// but a future client-library change could ship a lowercase variant
// and we must not break.
func TestValidateWebhookAuth_BasicLowercase(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/tok", nil)
	encoded := base64.StdEncoding.EncodeToString([]byte("resolvarr:shhh-very-secret"))
	req.Header.Set("Authorization", "basic "+encoded)
	ok, reason := validateWebhookAuth(req, "shhh-very-secret", true)
	if !ok {
		t.Errorf("lowercase 'basic' scheme should pass per RFC 7235 §2.1, got fail (reason=%q)", reason)
	}
}

// TestValidateWebhookAuth_BasicMixedCase covers the goofy-but-spec-
// compliant middle ground: a "bAsIc <b64>" header is still a valid
// Basic auth and must pass.
func TestValidateWebhookAuth_BasicMixedCase(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/tok", nil)
	encoded := base64.StdEncoding.EncodeToString([]byte("resolvarr:shhh-very-secret"))
	req.Header.Set("Authorization", "bAsIc "+encoded)
	ok, reason := validateWebhookAuth(req, "shhh-very-secret", true)
	if !ok {
		t.Errorf("mixed-case 'bAsIc' scheme should pass per RFC 7235 §2.1, got fail (reason=%q)", reason)
	}
}

// TestValidateWebhookAuth_BasicWithDoubleSpace pins behaviour for
// the malformed "Basic  <b64>" form (two spaces). RFC 7235 specifies
// a single SP separator, and our SplitN(" ", 2) on the first space
// leaves the second space as the leading byte of the value — which
// breaks base64 decode. We reject with a base64-error reason; this
// test pins that behaviour so a future "tolerate whitespace" refactor
// has to update the contract intentionally.
func TestValidateWebhookAuth_BasicWithDoubleSpace(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/tok", nil)
	encoded := base64.StdEncoding.EncodeToString([]byte("resolvarr:shhh-very-secret"))
	req.Header.Set("Authorization", "Basic  "+encoded)
	ok, reason := validateWebhookAuth(req, "shhh-very-secret", true)
	if ok {
		t.Errorf("double-space between scheme and value should fail (RFC 7235 specifies single SP)")
	}
	if !strings.Contains(reason, "base64") {
		t.Errorf("reason should mention base64 (leading space breaks decode), got %q", reason)
	}
}

func TestValidateWebhookAuth_EmptyConfiguredSecret_StrictMode(t *testing.T) {
	// Defense in depth: the require-signature endpoint refuses to save
	// strict=true with empty Secret, but if a hand-edited config.json
	// or a future bug lands us in that state at fire-time, fail closed.
	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/tok", nil)
	req.Header.Set("Authorization", authHeader("resolvarr", "anything"))
	ok, reason := validateWebhookAuth(req, "", true)
	if ok {
		t.Errorf("empty stored Secret with auth header should fail closed")
	}
	if !strings.Contains(reason, "no Secret") {
		t.Errorf("reason should mention no Secret configured, got %q", reason)
	}
}

func TestValidateWebhookAuth_EmptyConfiguredSecret_GraceModeNoHeader(t *testing.T) {
	// Edge case: zero stored Secret + grace mode + no header. This is
	// the legacy webhook URL state — user hasn't generated a secret
	// yet, must still accept events. Matches the "existing webhook
	// URLs continue working without any user action" contract.
	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/tok", nil)
	ok, _ := validateWebhookAuth(req, "", false)
	if !ok {
		t.Errorf("legacy state (empty Secret, grace mode, no header) must pass for backwards compat")
	}
}

// TestWebhookConfig_LegacyMigration locks the on-disk migration: an
// older config.json with only Token populated must decode cleanly into
// the new WebhookConfig shape with Secret="" and RequireSignature=false.
// This is what every existing user's config will look like on their
// first start after the upgrade.
func TestWebhookConfig_LegacyMigration(t *testing.T) {
	// Simulate a pre-Slice-A WebhookConfig blob — only Token + Logging.
	legacyJSON := []byte(`{
		"token": "legacy-tok-xxxxx",
		"loggingEnabled": true
	}`)
	var got core.WebhookConfig
	if err := json.Unmarshal(legacyJSON, &got); err != nil {
		t.Fatalf("legacy JSON should decode cleanly: %v", err)
	}
	if got.Token != "legacy-tok-xxxxx" {
		t.Errorf("Token = %q, want legacy-tok-xxxxx", got.Token)
	}
	if got.Secret != "" {
		t.Errorf("Secret on legacy migration = %q, want empty", got.Secret)
	}
	if got.RequireSignature {
		t.Errorf("RequireSignature on legacy migration should be false (grace mode)")
	}
	if !got.LoggingEnabled {
		t.Errorf("LoggingEnabled should preserve true")
	}
}

// TestHandleWebhookSetRequireSignature_RejectsStrictWithEmptySecret
// proves Slice B.2: the validator on the require-signature endpoint
// must reject {enabled:true} when no Secret is stored. The handler
// would otherwise put the receiver in an un-satisfiable state where
// every incoming event 401s with "instance has no Secret configured".
func TestHandleWebhookSetRequireSignature_RejectsStrictWithEmptySecret(t *testing.T) {
	dir := t.TempDir()
	store := core.NewConfigStore(dir)
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Seed an instance with a Token but no Secret — the state a
	// legacy-migrated user lands in until they click Configure
	// webhook to rotate (which generates Secret alongside Token).
	if err := store.Update(func(c *core.Config) {
		c.Instances = []core.Instance{{
			ID:     "inst-1",
			Name:   "Radarr",
			Type:   "radarr",
			URL:    "http://radarr.test:7878",
			APIKey: "abc",
			Webhook: core.WebhookConfig{
				Token:  "legacy-token",
				Secret: "", // intentionally empty — legacy state
			},
		}}
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	s := &Server{App: &core.App{Config: store}}

	// Build request with path-value baked in via mux pattern.
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/instances/{id}/webhook/require-signature", s.handleWebhookSetRequireSignature)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut,
		"/api/instances/inst-1/webhook/require-signature",
		strings.NewReader(`{"enabled":true}`))
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("strict+empty should 400, got %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Generate a secret first") {
		t.Errorf("error body should guide the user to Configure webhook first, got %s", rr.Body.String())
	}

	// Confirm the flag stayed false on disk.
	cfg := store.Get()
	if cfg.Instances[0].Webhook.RequireSignature {
		t.Errorf("RequireSignature should NOT be set after rejected request")
	}
}

// TestHandleWebhookSetRequireSignature_AcceptsStrictWithSecret proves
// the happy path: with a Secret stored, the user can flip strict mode
// on, the persisted state reflects the flip, and the response echoes
// the new value.
func TestHandleWebhookSetRequireSignature_AcceptsStrictWithSecret(t *testing.T) {
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
			URL:    "http://radarr.test:7878",
			APIKey: "abc",
			Webhook: core.WebhookConfig{
				Token:  "some-token",
				Secret: "some-secret",
			},
		}}
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	s := &Server{App: &core.App{Config: store}}
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/instances/{id}/webhook/require-signature", s.handleWebhookSetRequireSignature)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut,
		"/api/instances/inst-1/webhook/require-signature",
		strings.NewReader(`{"enabled":true}`))
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("happy path should 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	cfg := store.Get()
	if !cfg.Instances[0].Webhook.RequireSignature {
		t.Errorf("RequireSignature should be true after accepted flip")
	}

	// Disabling strict mode is always allowed (no Secret check on the
	// off-path because downgrading to grace mode never needs the
	// secret to validate anything).
	rr2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPut,
		"/api/instances/inst-1/webhook/require-signature",
		strings.NewReader(`{"enabled":false}`))
	mux.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("disabling strict should always 200, got %d", rr2.Code)
	}
	cfg = store.Get()
	if cfg.Instances[0].Webhook.RequireSignature {
		t.Errorf("RequireSignature should be false after disable")
	}
}

// TestHandleWebhookSetRequireSignature_Rejects409WhenNotConfigured
// mirrors handleWebhookSetLogging's 409 gate: can't tune flags on a
// webhook that doesn't exist yet.
func TestHandleWebhookSetRequireSignature_Rejects409WhenNotConfigured(t *testing.T) {
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
			URL:    "http://radarr.test:7878",
			APIKey: "abc",
			// Webhook intentionally zero-valued.
		}}
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	s := &Server{App: &core.App{Config: store}}
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/instances/{id}/webhook/require-signature", s.handleWebhookSetRequireSignature)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut,
		"/api/instances/inst-1/webhook/require-signature",
		strings.NewReader(`{"enabled":true}`))
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409 on no-webhook-configured, got %d body=%s", rr.Code, rr.Body.String())
	}
}

// TestHandleWebhookRotateToken_GeneratesSecret_RetainsRequireSignature
// proves Slice A.2: rotating the token also rotates the Secret, and
// the existing RequireSignature flag is preserved across rotation so
// users who already opted into strict mode aren't silently downgraded.
//
// NOTE: the user does need to re-paste the new Secret into Sonarr/
// Radarr's Connect config — until they do, strict mode will reject
// every event. The UI surfaces this in the rotation confirmation toast.
func TestHandleWebhookRotateToken_GeneratesSecret_RetainsRequireSignature(t *testing.T) {
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
			URL:    "http://radarr.test:7878",
			APIKey: "abc",
			Webhook: core.WebhookConfig{
				Token:            "old-token",
				Secret:           "old-secret",
				RequireSignature: true,
			},
		}}
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	s := &Server{App: &core.App{Config: store}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/instances/{id}/webhook/rotate", s.handleWebhookRotateToken)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/api/instances/inst-1/webhook/rotate",
		strings.NewReader(`{}`))
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("rotate should 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	newToken, _ := resp["token"].(string)
	newSecret, _ := resp["secret"].(string)
	requireSig, _ := resp["requireSignature"].(bool)
	if newToken == "" || newToken == "old-token" {
		t.Errorf("Token should rotate, got %q", newToken)
	}
	if newSecret == "" || newSecret == "old-secret" {
		t.Errorf("Secret should rotate, got %q", newSecret)
	}
	if !requireSig {
		t.Errorf("RequireSignature should be preserved across rotation, got %v", requireSig)
	}

	// Confirm on-disk state matches the response.
	cfg := store.Get()
	if cfg.Instances[0].Webhook.Token != newToken {
		t.Errorf("on-disk Token mismatch: got %q want %q", cfg.Instances[0].Webhook.Token, newToken)
	}
	if cfg.Instances[0].Webhook.Secret != newSecret {
		t.Errorf("on-disk Secret mismatch: got %q want %q", cfg.Instances[0].Webhook.Secret, newSecret)
	}
	if !cfg.Instances[0].Webhook.RequireSignature {
		t.Errorf("on-disk RequireSignature should stay true")
	}
}

// TestHandleWebhookReceive_StrictMode_RejectsUnsigned drives the
// full HTTP path through handleWebhookReceive: a strict-mode instance
// must reject an unsigned POST with 401 and log the rejection to the
// ring-buffer so the user can see it in Recent activity.
func TestHandleWebhookReceive_StrictMode_RejectsUnsigned(t *testing.T) {
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
			URL:    "http://radarr.test:7878",
			APIKey: "abc",
			Webhook: core.WebhookConfig{
				Token:            "rcv-token",
				Secret:           "rcv-secret",
				RequireSignature: true,
				LoggingEnabled:   true,
			},
		}}
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	s := &Server{App: &core.App{Config: store}}
	s.WebhookLog = newWebhookLog(dir + "/events.json")

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/webhooks/{token}", s.handleWebhookReceive)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/api/webhooks/rcv-token",
		strings.NewReader(`{"eventType":"Test"}`))
	// Deliberately no Authorization header.
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("strict mode unsigned should 401, got %d body=%s", rr.Code, rr.Body.String())
	}
	// Verify the rejection landed in the ring-buffer with the
	// "(rejected)" event type.
	evs := s.WebhookLog.list("inst-1")
	if len(evs) != 1 {
		t.Fatalf("expected 1 rejection event in log, got %d", len(evs))
	}
	if evs[0].EventType != "(rejected)" {
		t.Errorf("rejection event type = %q, want (rejected)", evs[0].EventType)
	}
}

// TestHandleWebhookReceive_StrictMode_AcceptsValidAuth proves the
// happy path: matching Authorization: Basic header with strict mode
// on succeeds, the event lands in the log as a normal event.
func TestHandleWebhookReceive_StrictMode_AcceptsValidAuth(t *testing.T) {
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
			URL:    "http://radarr.test:7878",
			APIKey: "abc",
			Webhook: core.WebhookConfig{
				Token:            "rcv-token",
				Secret:           "rcv-secret",
				RequireSignature: true,
				LoggingEnabled:   true,
			},
		}}
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	s := &Server{App: &core.App{Config: store}}
	s.WebhookLog = newWebhookLog(dir + "/events.json")

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/webhooks/{token}", s.handleWebhookReceive)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/api/webhooks/rcv-token",
		strings.NewReader(`{"eventType":"Test"}`))
	req.Header.Set("Authorization", authHeader("resolvarr", "rcv-secret"))
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("strict mode valid auth should 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	evs := s.WebhookLog.list("inst-1")
	if len(evs) != 1 {
		t.Fatalf("expected 1 logged event, got %d", len(evs))
	}
	if evs[0].EventType != "Test" {
		t.Errorf("event type = %q, want Test", evs[0].EventType)
	}
}

// TestHandleWebhookReceive_GraceMode_AcceptsUnsignedWithWarning proves
// the back-compat path: a legacy webhook URL (no Secret, no Require-
// Signature) keeps working. The unsigned event lands in the log as a
// normal event AND a "(unsigned)" warning is also logged so the user
// sees the nudge to flip strict mode.
func TestHandleWebhookReceive_GraceMode_AcceptsUnsignedWithWarning(t *testing.T) {
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
			URL:    "http://radarr.test:7878",
			APIKey: "abc",
			Webhook: core.WebhookConfig{
				Token:            "rcv-token",
				Secret:           "rcv-secret", // configured but not enforced
				RequireSignature: false,        // grace mode
				LoggingEnabled:   true,
			},
		}}
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	s := &Server{App: &core.App{Config: store}}
	s.WebhookLog = newWebhookLog(dir + "/events.json")

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/webhooks/{token}", s.handleWebhookReceive)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/api/webhooks/rcv-token",
		strings.NewReader(`{"eventType":"Test"}`))
	// No Authorization header — legacy unsigned event.
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("grace mode unsigned should 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	evs := s.WebhookLog.list("inst-1")
	// Expect 2 entries: the "(unsigned)" warning + the real Test event.
	if len(evs) != 2 {
		t.Fatalf("expected 2 logged events (warning + real), got %d", len(evs))
	}
	// List returns newest first — the real event was logged after the
	// warning, so newest=Test, then warning=(unsigned).
	foundUnsigned := false
	foundTest := false
	for _, e := range evs {
		if e.EventType == "(unsigned)" {
			foundUnsigned = true
		}
		if e.EventType == "Test" {
			foundTest = true
		}
	}
	if !foundUnsigned {
		t.Errorf("grace-mode unsigned event should produce an (unsigned) warning entry")
	}
	if !foundTest {
		t.Errorf("grace-mode unsigned event should still log the real Test event")
	}
}

// TestHandleWebhookGet_ReturnsSecretAndRequireSignature locks the
// admin-side getter contract: GET /api/instances/{id}/webhook returns
// Secret and RequireSignature unmasked so the wizard's Summary step
// can render them in copy-buttons.
func TestHandleWebhookGet_ReturnsSecretAndRequireSignature(t *testing.T) {
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
			URL:    "http://radarr.test:7878",
			APIKey: "abc",
			Webhook: core.WebhookConfig{
				Token:            "tok-xxx",
				Secret:           "sec-yyy",
				RequireSignature: true,
				LoggingEnabled:   true,
			},
		}}
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	s := &Server{App: &core.App{Config: store}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/instances/{id}/webhook", s.handleWebhookGet)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/instances/inst-1/webhook", nil)
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["token"] != "tok-xxx" {
		t.Errorf("token = %v, want tok-xxx", resp["token"])
	}
	if resp["secret"] != "sec-yyy" {
		t.Errorf("secret = %v, want sec-yyy", resp["secret"])
	}
	if resp["requireSignature"] != true {
		t.Errorf("requireSignature = %v, want true", resp["requireSignature"])
	}
}

// TestHandleWebhookDelete_ClearsSecretAndRequireSignature locks the
// cleanup contract: DELETE wipes every webhook field including the
// new Secret + RequireSignature so the instance returns to the
// "not configured" state.
func TestHandleWebhookDelete_ClearsSecretAndRequireSignature(t *testing.T) {
	dir := t.TempDir()
	store := core.NewConfigStore(dir)
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := store.Update(func(c *core.Config) {
		c.Instances = []core.Instance{{
			ID: "inst-1", Name: "Radarr", Type: "radarr",
			URL: "http://x", APIKey: "abc",
			Webhook: core.WebhookConfig{
				Token: "t", Secret: "s", RequireSignature: true, LoggingEnabled: true,
			},
		}}
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	s := &Server{App: &core.App{Config: store}}
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/instances/{id}/webhook", s.handleWebhookDelete)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/instances/inst-1/webhook", nil)
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	cfg := store.Get()
	wh := cfg.Instances[0].Webhook
	if wh.Token != "" || wh.Secret != "" || wh.RequireSignature || wh.LoggingEnabled {
		t.Errorf("DELETE should clear every webhook field, got %+v", wh)
	}
}
