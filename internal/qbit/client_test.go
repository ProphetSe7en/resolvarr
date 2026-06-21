package qbit

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestNormaliseBaseURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
		err  bool
	}{
		{"direct ip + port", "http://192.168.1.100:8080", "http://192.168.1.100:8080", false},
		{"trailing slash stripped", "http://192.168.1.100:8080/", "http://192.168.1.100:8080", false},
		{"reverse proxy with subpath", "https://qbit.example.com/qbit", "https://qbit.example.com/qbit", false},
		{"reverse proxy subpath trailing slash", "https://qbit.example.com/qbit/", "https://qbit.example.com/qbit", false},
		{"https with port", "https://qbit.example.com:8443", "https://qbit.example.com:8443", false},
		{"strips query + fragment", "http://x:8080/?foo=1#bar", "http://x:8080", false},
		{"empty string rejected", "", "", true},
		{"whitespace rejected", "   ", "", true},
		{"no scheme rejected", "qbit.example.com", "", true},
		{"ftp scheme rejected", "ftp://qbit.example.com", "", true},
		{"missing host rejected", "http://", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := normaliseBaseURL(c.in)
			if c.err {
				if err == nil {
					t.Errorf("expected error for %q, got nil + %q", c.in, got)
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error for %q: %v", c.in, err)
				return
			}
			if got != c.want {
				t.Errorf("normaliseBaseURL(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestApiURL_PreservesSubpath confirms that reverse-proxy URLs with
// a subpath component get API paths appended correctly. This is the
// load-bearing fix that lets users behind nginx / traefik / Cloudflare
// expose qBit at e.g. https://example.com/qbit/ and have resolvarr
// hit https://example.com/qbit/api/v2/auth/login.
func TestApiURL_PreservesSubpath(t *testing.T) {
	c := &Client{baseURL: "https://example.com/qbit"}
	cases := []struct {
		path string
		want string
	}{
		{"/api/v2/auth/login", "https://example.com/qbit/api/v2/auth/login"},
		{"/api/v2/torrents/info", "https://example.com/qbit/api/v2/torrents/info"},
		{"api/v2/torrents/info", "https://example.com/qbit/api/v2/torrents/info"}, // missing leading slash auto-fixed
	}
	for _, tc := range cases {
		got := c.apiURL(tc.path)
		if got != tc.want {
			t.Errorf("apiURL(%q) on subpath base = %q, want %q", tc.path, got, tc.want)
		}
	}
}

// TestLogin_Success uses an httptest server to emulate qBit's
// /api/v2/auth/login endpoint and verifies the happy path: form-encoded
// creds in the body, Referer header, "Ok." response → cookieReady true.
func TestLogin_Success(t *testing.T) {
	loginCalls := int64(0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&loginCalls, 1)
		if r.URL.Path != "/api/v2/auth/login" {
			t.Errorf("login hit unexpected path %q", r.URL.Path)
		}
		if r.Method != "POST" {
			t.Errorf("login expected POST, got %s", r.Method)
		}
		if r.Header.Get("Referer") == "" {
			t.Errorf("login missing Referer header")
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("login form parse: %v", err)
		}
		if r.Form.Get("username") != "admin" || r.Form.Get("password") != "adminpw" {
			t.Errorf("login form: got u=%q p=%q", r.Form.Get("username"), r.Form.Get("password"))
		}
		http.SetCookie(w, &http.Cookie{Name: "SID", Value: "test-session-id"})
		w.WriteHeader(200)
		w.Write([]byte("Ok."))
	}))
	defer server.Close()

	c, err := New(Config{URL: server.URL, Username: "admin", Password: "adminpw"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := c.Login(context.Background()); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if !c.cookieReady {
		t.Errorf("cookieReady should be true after successful login")
	}
	if got := atomic.LoadInt64(&loginCalls); got != 1 {
		t.Errorf("expected 1 login call, got %d", got)
	}

	// Idempotent — second Login() call short-circuits when cookie
	// is already set. Verified by the call counter not advancing.
	if err := c.Login(context.Background()); err != nil {
		t.Errorf("idempotent Login should not error: %v", err)
	}
	// Actually the public Login() always calls loginLocked which
	// re-runs login. cookieReady is just a cache hint for Do(); it
	// doesn't gate explicit Login() calls. Verify second call
	// counter incremented (intentional explicit re-login).
	if got := atomic.LoadInt64(&loginCalls); got != 2 {
		t.Errorf("expected 2 login calls after explicit re-login, got %d", got)
	}
}

// TestLogin_BadCredentials covers qBit's idiomatic "200 OK + 'Fails.' body"
// for wrong creds. cookieReady must stay false + error must be
// surface-able to the user.
func TestLogin_BadCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("Fails."))
	}))
	defer server.Close()

	c, _ := New(Config{URL: server.URL, Username: "x", Password: "y"})
	err := c.Login(context.Background())
	if err == nil {
		t.Fatal("expected error on Fails. body, got nil")
	}
	if c.cookieReady {
		t.Errorf("cookieReady should be false after rejected login")
	}
	if !strings.Contains(err.Error(), "rejected") {
		t.Errorf("error should mention 'rejected': %q", err.Error())
	}
}

// TestLogin_NoAuthShortCircuit covers the LocalHostAuth-disabled case
// where qBit's WebUI is configured for no-auth on localhost. Empty
// creds → no login network call, cookieReady is set true so Do()
// can proceed.
func TestLogin_NoAuthShortCircuit(t *testing.T) {
	called := int64(0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&called, 1)
	}))
	defer server.Close()

	c, _ := New(Config{URL: server.URL}) // empty u + p
	if err := c.Login(context.Background()); err != nil {
		t.Fatalf("Login no-auth should succeed: %v", err)
	}
	if !c.cookieReady {
		t.Errorf("cookieReady should be true even on no-auth shortcut")
	}
	if got := atomic.LoadInt64(&called); got != 0 {
		t.Errorf("no-auth Login should make NO network call, got %d", got)
	}
}

// TestDo_RetriesOn401 covers the session-expired path: first request
// hits a stale cookie → 401 → Client must re-login and retry once.
// Second request returns 200. Verifies the retry happens transparently
// without surfacing the 401 to the caller.
func TestDo_RetriesOn401(t *testing.T) {
	loginCalls := int64(0)
	infoCalls := int64(0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			n := atomic.AddInt64(&loginCalls, 1)
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "session-" + string(rune('0'+n))})
			w.WriteHeader(200)
			w.Write([]byte("Ok."))
		case "/api/v2/torrents/info":
			n := atomic.AddInt64(&infoCalls, 1)
			if n == 1 {
				w.WriteHeader(401)
				return
			}
			w.WriteHeader(200)
			w.Write([]byte("[]"))
		}
	}))
	defer server.Close()

	c, _ := New(Config{URL: server.URL, Username: "u", Password: "p"})
	_, err := c.ListTorrents(context.Background(), "")
	if err != nil {
		t.Fatalf("ListTorrents should succeed after retry: %v", err)
	}
	if got := atomic.LoadInt64(&loginCalls); got != 2 {
		t.Errorf("expected 2 login calls (initial + after-401), got %d", got)
	}
	if got := atomic.LoadInt64(&infoCalls); got != 2 {
		t.Errorf("expected 2 listTorrents calls (initial + retry), got %d", got)
	}
}

// TestPing_ForcesFreshLogin — Test Connection should not reuse a
// cached session because the user just edited credentials and we
// need to verify the NEW creds actually work, not the old session.
func TestPing_ForcesFreshLogin(t *testing.T) {
	loginCalls := int64(0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			atomic.AddInt64(&loginCalls, 1)
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "abc"})
			w.WriteHeader(200)
			w.Write([]byte("Ok."))
		case "/api/v2/torrents/info":
			w.WriteHeader(200)
			w.Write([]byte("[]"))
		}
	}))
	defer server.Close()

	c, _ := New(Config{URL: server.URL, Username: "u", Password: "p"})
	// First Login warms cookie.
	if err := c.Login(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt64(&loginCalls); got != 1 {
		t.Fatalf("setup login: want 1, got %d", got)
	}
	// Ping forces a fresh login.
	if err := c.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt64(&loginCalls); got != 2 {
		t.Errorf("Ping should force re-login: want 2 total logins, got %d", got)
	}
}

// TestScrubErr_RedactsProxyToken: a transport error embedding the qui
// /proxy/<token> URL (Go's *url.Error includes the full request URL) must
// have the token redacted before it can reach the log or the UI.
func TestScrubErr_RedactsProxyToken(t *testing.T) {
	token := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	c, err := New(Config{URL: "http://qbit:7476/proxy/" + token})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.proxyToken != token {
		t.Fatalf("proxyToken = %q, want the token", c.proxyToken)
	}
	raw := errors.New(`Get "http://qbit:7476/proxy/` + token + `/api/v2/torrents/info?filter=stalled": dial tcp: lookup qbit: no such host`)
	got := c.scrubErr(raw).Error()
	if strings.Contains(got, token) {
		t.Errorf("scrubErr leaked the token: %q", got)
	}
	if !strings.Contains(got, "/proxy/<redacted>") {
		t.Errorf("scrubErr should mark the redaction, got %q", got)
	}
	// Direct (non-proxy) URLs have no token: scrubErr is a passthrough.
	c2, _ := New(Config{URL: "http://192.168.1.100:8080"})
	e2 := errors.New("dial tcp 192.168.1.100:8080: connect: connection refused")
	if c2.scrubErr(e2).Error() != e2.Error() {
		t.Errorf("scrubErr altered a non-proxy error")
	}
}
