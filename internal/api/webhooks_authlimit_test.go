package api

// webhooks_authlimit_test.go — coverage for the auth-event log
// rate-limiter (authLogRateLimiter). The limiter prevents an attacker
// or a chatty client from flooding the 100-entry ring buffer with
// (rejected) / (unsigned) appends, which would otherwise evict every
// legitimate event in ~50-100 requests.
//
// State is per-(instance, reason) keyed; tests cover:
//   - first event in a window logs
//   - repeat within window suppressed
//   - repeat after window logs again
//   - different reasons on same instance don't cross-suppress
//   - different instances on same reason don't cross-suppress
//
// We inject `now` instead of time.Sleep so the after-window case is
// deterministic + fast.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"resolvarr/internal/core"
)

func TestAuthLogRateLimiter_FirstEventLogs(t *testing.T) {
	l := &authLogRateLimiter{}
	if !l.shouldLog("inst-1", "auth-rejected:missing Authorization header") {
		t.Errorf("first event for a new (instance, reason) pair must log")
	}
}

func TestAuthLogRateLimiter_RepeatWithinWindow(t *testing.T) {
	// Inject a frozen clock so the second call is unambiguously
	// "1 second after" the first — no time.Sleep, no flake.
	clock := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	l := &authLogRateLimiter{now: func() time.Time { return clock }}
	if !l.shouldLog("inst-1", "auth-rejected:wrong secret") {
		t.Fatalf("first event must log")
	}
	// Advance 1 second — still well inside the 5-minute window.
	clock = clock.Add(time.Second)
	if l.shouldLog("inst-1", "auth-rejected:wrong secret") {
		t.Errorf("repeat within window must NOT log")
	}
	// Advance to 4m59s after first — still inside window.
	clock = clock.Add(4*time.Minute + 58*time.Second)
	if l.shouldLog("inst-1", "auth-rejected:wrong secret") {
		t.Errorf("repeat at 4m59s must NOT log (still inside 5m window)")
	}
}

func TestAuthLogRateLimiter_RepeatAfterWindow(t *testing.T) {
	clock := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	l := &authLogRateLimiter{now: func() time.Time { return clock }}
	if !l.shouldLog("inst-1", "auth-rejected:wrong secret") {
		t.Fatalf("first event must log")
	}
	// Advance just past the 5-minute window.
	clock = clock.Add(authLogWindow + time.Second)
	if !l.shouldLog("inst-1", "auth-rejected:wrong secret") {
		t.Errorf("repeat after window must log again (gives user 'still happening' nudge)")
	}
}

func TestAuthLogRateLimiter_DifferentReasonsCoexist(t *testing.T) {
	// A flooding "wrong secret" reason must not suppress a different
	// "missing header" reason on the same instance — each distinct
	// misconfiguration deserves at least one log entry per window.
	clock := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	l := &authLogRateLimiter{now: func() time.Time { return clock }}
	if !l.shouldLog("inst-1", "auth-rejected:wrong secret") {
		t.Fatalf("first wrong-secret must log")
	}
	if !l.shouldLog("inst-1", "auth-rejected:missing header") {
		t.Errorf("different reason on same instance must NOT be suppressed by the first reason's window")
	}
	// The unsigned-grace-mode warning uses its own reason key —
	// also independent of the rejected reasons.
	if !l.shouldLog("inst-1", "auth-unsigned") {
		t.Errorf("(unsigned) warning must NOT be suppressed by rejected-event reasons")
	}
}

func TestAuthLogRateLimiter_DifferentInstancesCoexist(t *testing.T) {
	// Two instances flooded with the same reason must each get their
	// own first-of-window log — the limiter is per-instance.
	clock := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	l := &authLogRateLimiter{now: func() time.Time { return clock }}
	if !l.shouldLog("inst-1", "auth-rejected:wrong secret") {
		t.Fatalf("inst-1 first event must log")
	}
	if !l.shouldLog("inst-2", "auth-rejected:wrong secret") {
		t.Errorf("inst-2 with same reason must NOT be suppressed by inst-1's window")
	}
}

// TestHandleWebhookReceive_StrictMode_FloodIsCoalesced exercises the
// end-to-end HTTP path: 50 unsigned rejections to the same strict-mode
// instance must produce exactly ONE (rejected) ring-buffer entry, not
// 50. Without the rate-limiter, this test would assert 50 entries and
// the legitimate-event-eviction described in the concern would happen.
func TestHandleWebhookReceive_StrictMode_FloodIsCoalesced(t *testing.T) {
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
	s := &Server{
		App:            &core.App{Config: store},
		authLogLimiter: &authLogRateLimiter{},
	}
	s.WebhookLog = newWebhookLog(dir + "/events.json")

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/webhooks/{token}", s.handleWebhookReceive)

	// Fire 50 unsigned requests in a tight loop — every one rejects,
	// but only the first should land in the ring buffer.
	for i := 0; i < 50; i++ {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost,
			"/api/webhooks/rcv-token",
			strings.NewReader(`{"eventType":"Test"}`))
		// Deliberately no Authorization header — strict mode rejects.
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("iter %d: expected 401, got %d", i, rr.Code)
		}
	}

	evs := s.WebhookLog.list("inst-1")
	if len(evs) != 1 {
		t.Fatalf("flood of 50 unsigned events should coalesce to 1 ring entry, got %d", len(evs))
	}
	if evs[0].EventType != "(rejected)" {
		t.Errorf("coalesced entry type = %q, want (rejected)", evs[0].EventType)
	}
}

// TestHandleWebhookReceive_GraceMode_UnsignedFloodIsCoalesced is the
// grace-mode equivalent: 50 unsigned events should produce exactly
// 50 real "Test" events PLUS exactly 1 "(unsigned)" warning — the
// real events are NOT rate-limited (they're legitimate Connect data),
// only the warning meta-row is.
func TestHandleWebhookReceive_GraceMode_UnsignedFloodIsCoalesced(t *testing.T) {
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
				RequireSignature: false, // grace mode
				LoggingEnabled:   true,
			},
		}}
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	s := &Server{
		App:            &core.App{Config: store},
		authLogLimiter: &authLogRateLimiter{},
	}
	s.WebhookLog = newWebhookLog(dir + "/events.json")

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/webhooks/{token}", s.handleWebhookReceive)

	const n = 25 // keep under ring cap (100) so we can count exactly
	for i := 0; i < n; i++ {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost,
			"/api/webhooks/rcv-token",
			strings.NewReader(`{"eventType":"Test"}`))
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("iter %d: expected 200, got %d", i, rr.Code)
		}
	}

	evs := s.WebhookLog.list("inst-1")
	unsignedCount := 0
	realCount := 0
	for _, e := range evs {
		switch e.EventType {
		case "(unsigned)":
			unsignedCount++
		case "Test":
			realCount++
		}
	}
	if unsignedCount != 1 {
		t.Errorf("grace-mode flood should produce exactly 1 (unsigned) warning, got %d", unsignedCount)
	}
	if realCount != n {
		t.Errorf("grace-mode flood should still log all %d real Test events, got %d", n, realCount)
	}
}
