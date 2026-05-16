package api

import (
	"net/http/httptest"
	"strings"
	"testing"

	"resolvarr/internal/core"
)

// Tests for the WebhookCallbackURL override path — validator +
// resolver. The handler-level tests in qbit_webhook_config_test.go
// cover the request/response wiring; here we pin the pure functions
// so a future refactor can't quietly drop a rule.

func TestValidateQbitWebhookCallbackURL_Empty(t *testing.T) {
	got, err := validateQbitWebhookCallbackURL("")
	if err != nil {
		t.Errorf("empty input should accept (means clear override), got %v", err)
	}
	if got != "" {
		t.Errorf("empty input should return empty string, got %q", got)
	}
}

func TestValidateQbitWebhookCallbackURL_AcceptsHttpHostPort(t *testing.T) {
	got, err := validateQbitWebhookCallbackURL("http://resolvarr:6075")
	if err != nil {
		t.Fatalf("expected accept, got %v", err)
	}
	if got != "http://resolvarr:6075" {
		t.Errorf("normalised = %q, want http://resolvarr:6075", got)
	}
}

func TestValidateQbitWebhookCallbackURL_AcceptsHttps(t *testing.T) {
	got, err := validateQbitWebhookCallbackURL("https://resolvarr.example.com")
	if err != nil {
		t.Fatalf("expected accept, got %v", err)
	}
	if got != "https://resolvarr.example.com" {
		t.Errorf("normalised = %q", got)
	}
}

func TestValidateQbitWebhookCallbackURL_TrimsWhitespace(t *testing.T) {
	got, err := validateQbitWebhookCallbackURL("  http://resolvarr:6075  ")
	if err != nil {
		t.Fatalf("expected accept, got %v", err)
	}
	if got != "http://resolvarr:6075" {
		t.Errorf("trim failed: %q", got)
	}
}

func TestValidateQbitWebhookCallbackURL_RejectsNonHttpScheme(t *testing.T) {
	_, err := validateQbitWebhookCallbackURL("ftp://resolvarr:6075")
	if err == nil || !strings.Contains(err.Error(), "http:// or https://") {
		t.Errorf("expected scheme error, got %v", err)
	}
}

func TestValidateQbitWebhookCallbackURL_RejectsBareHost(t *testing.T) {
	_, err := validateQbitWebhookCallbackURL("resolvarr:6075")
	if err == nil {
		t.Errorf("expected reject for missing scheme, got accept")
	}
}

func TestValidateQbitWebhookCallbackURL_RejectsPath(t *testing.T) {
	_, err := validateQbitWebhookCallbackURL("http://resolvarr:6075/api/foo")
	if err == nil || !strings.Contains(err.Error(), "base-only") {
		t.Errorf("expected path-reject, got %v", err)
	}
}

func TestValidateQbitWebhookCallbackURL_RejectsQuery(t *testing.T) {
	_, err := validateQbitWebhookCallbackURL("http://resolvarr:6075?x=1")
	if err == nil {
		t.Errorf("expected reject for query string, got accept")
	}
}

func TestResolveQbitWebhookURL_UsesOverrideWhenSet(t *testing.T) {
	inst := &core.QbitInstance{
		ID:                 "abc",
		WebhookCallbackURL: "http://resolvarr:6075",
	}
	req := httptest.NewRequest("GET", "http://192.168.1.50:6075/whatever", nil)
	got := resolveQbitWebhookURL(req, inst)
	want := "http://resolvarr:6075/api/qbit/torrent-added/abc"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveQbitWebhookURL_FallsBackToRequestHost(t *testing.T) {
	inst := &core.QbitInstance{
		ID: "abc",
	}
	req := httptest.NewRequest("GET", "http://192.168.1.50:6075/whatever", nil)
	got := resolveQbitWebhookURL(req, inst)
	want := "http://192.168.1.50:6075/api/qbit/torrent-added/abc"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveQbitWebhookURL_TrimsTrailingSlashOnOverride(t *testing.T) {
	inst := &core.QbitInstance{
		ID:                 "abc",
		WebhookCallbackURL: "http://resolvarr:6075/",
	}
	req := httptest.NewRequest("GET", "http://x/y", nil)
	got := resolveQbitWebhookURL(req, inst)
	// Validator strips trailing slashes, but defensive trim in
	// resolver matters when a future caller bypasses the validator
	// (test fixture, migration, etc.). Pin both behaviours.
	if strings.Contains(got, "//api/") {
		t.Errorf("got double-slash on join: %q", got)
	}
}

func TestResolveQbitWebhookURL_NilInstance(t *testing.T) {
	// All current call sites 404 on nil instance upstream, so this
	// branch is purely defensive — pin "no panic, returns empty
	// string" so a future regression surfaces visibly.
	req := httptest.NewRequest("GET", "http://x/y", nil)
	got := resolveQbitWebhookURL(req, nil)
	if got != "" {
		t.Errorf("expected empty string on nil instance, got %q", got)
	}
}
