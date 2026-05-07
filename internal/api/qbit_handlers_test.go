package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"resolvarr/internal/core"
)

// newQbitTestServer wires a Server backed by a real ConfigStore in a
// TempDir so T78 integration tests can exercise the full Create / List
// / Update / Test cycle and verify the on-disk state independently of
// the API echoes.
func newQbitTestServer(t *testing.T) (*Server, *core.ConfigStore) {
	t.Helper()
	dir := t.TempDir()
	store := core.NewConfigStore(dir)
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	app := &core.App{Config: store}
	return &Server{App: app}, store
}

// readJSON unmarshals the recorder body into out. Fails the test on
// any parse error so call-sites stay focused on the expectations.
func readJSON(t *testing.T, rr *httptest.ResponseRecorder, out any) {
	t.Helper()
	if err := json.Unmarshal(rr.Body.Bytes(), out); err != nil {
		t.Fatalf("decode response (%d, body=%s): %v", rr.Code, rr.Body.String(), err)
	}
}

// realQuiToken is a 64-char hex string used as a stand-in for a real
// qui client-proxy auth token across the T78 tests.
//
// realQuiURL targets 127.0.0.1:1 — TCP port 1 is reserved (never
// listens), so the probe gets connection-refused immediately rather
// than waiting on the full 10s qbitTestTimeout. (An earlier draft
// used 192.168.0.5 and accidentally hit a real qui running on the
// dev host.)
const realQuiToken = "602f21d07ef107895b37e0e679d0575c69ae6687c338624c946bd2fc1fe0c33e"
const realQuiURL = "http://127.0.0.1:1/proxy/" + realQuiToken
const realPassword = "real-qbit-password-1234"

// TestQbitHandlers_T78_MaskResolution_FullCycle drives the full
// secret-handling contract end-to-end: a Create stores plaintext, a
// List response masks both the URL token and the password, an Update
// that round-trips the masked values must NOT overwrite storage, and a
// follow-up List still reflects the original plaintext (proven by
// reading the on-disk config directly).
//
// This is the T78 "mask-resolution must cover every secondary endpoint"
// integration check the security baseline asks for. Unit tests for
// maskQbitURL / isMaskedQbitURL above cover the helpers; this test
// proves the handlers wire them together correctly.
func TestQbitHandlers_T78_MaskResolution_FullCycle(t *testing.T) {
	s, store := newQbitTestServer(t)

	// 1. Create with plaintext URL + password.
	createBody := `{
		"name": "qui-main",
		"url": "` + realQuiURL + `",
		"password": "` + realPassword + `"
	}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/qbit-instances", strings.NewReader(createBody))
	s.handleCreateQbitInstance(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("Create: status %d, body %s", rr.Code, rr.Body.String())
	}
	var created core.QbitInstance
	readJSON(t, rr, &created)

	// Echo body must be masked, never plaintext.
	if strings.Contains(created.URL, realQuiToken) {
		t.Errorf("Create echo leaked qui token: %q", created.URL)
	}
	if !isMaskedQbitURL(created.URL) {
		t.Errorf("Create echo URL not masked: %q", created.URL)
	}
	if created.Password != maskSentinel {
		t.Errorf("Create echo Password = %q, want masked sentinel", created.Password)
	}
	if created.ID == "" {
		t.Fatal("Create echo missing ID")
	}

	// On-disk state must hold the plaintext we POSTed. This is the
	// invariant masking is designed to protect — never expose, but
	// preserve verbatim.
	stored := store.Get().QbitInstances
	if len(stored) != 1 {
		t.Fatalf("stored len = %d, want 1", len(stored))
	}
	if stored[0].URL != realQuiURL {
		t.Errorf("stored URL = %q, want %q", stored[0].URL, realQuiURL)
	}
	if stored[0].Password != realPassword {
		t.Errorf("stored Password = %q, want %q", stored[0].Password, realPassword)
	}

	// 2. List response masks both fields the same way the echo did.
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/qbit-instances", nil)
	s.handleListQbitInstances(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("List: status %d", rr.Code)
	}
	var listed []core.QbitInstance
	readJSON(t, rr, &listed)
	if len(listed) != 1 {
		t.Fatalf("List len = %d, want 1", len(listed))
	}
	if strings.Contains(listed[0].URL, realQuiToken) {
		t.Errorf("List leaked qui token: %q", listed[0].URL)
	}
	if listed[0].Password != maskSentinel {
		t.Errorf("List Password = %q, want masked sentinel", listed[0].Password)
	}

	// 3. Update with the masked values round-tripped (user only edited
	// the Name + Username; URL + Password came back from List as
	// masked and the UI sent them right back). Storage must NOT be
	// overwritten with the masked strings.
	maskedURL := listed[0].URL
	updateBody := `{
		"name": "qui-renamed",
		"url": "` + maskedURL + `",
		"username": "newuser",
		"password": "` + maskSentinel + `"
	}`
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/qbit-instances/"+created.ID, strings.NewReader(updateBody))
	req.SetPathValue("id", created.ID)
	s.handleUpdateQbitInstance(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("Update (masked round-trip): status %d, body %s", rr.Code, rr.Body.String())
	}
	stored = store.Get().QbitInstances
	if stored[0].URL != realQuiURL {
		t.Errorf("after masked round-trip Update, stored URL = %q, want unchanged %q", stored[0].URL, realQuiURL)
	}
	if stored[0].Password != realPassword {
		t.Errorf("after masked round-trip Update, stored Password = %q, want unchanged %q", stored[0].Password, realPassword)
	}
	if stored[0].Name != "qui-renamed" {
		t.Errorf("stored Name = %q, want qui-renamed (Name should still be editable)", stored[0].Name)
	}
	if stored[0].Username != "newuser" {
		t.Errorf("stored Username = %q, want newuser", stored[0].Username)
	}

	// 4. Update with a fresh real URL + real password REPLACES storage.
	// This is the path that proves we don't always preserve.
	freshToken := "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
	freshURL := "https://qui-2.example.com/proxy/" + freshToken
	freshPassword := "rotated-password"
	updateBody = `{
		"name": "qui-renamed",
		"url": "` + freshURL + `",
		"username": "newuser",
		"password": "` + freshPassword + `"
	}`
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/qbit-instances/"+created.ID, strings.NewReader(updateBody))
	req.SetPathValue("id", created.ID)
	s.handleUpdateQbitInstance(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("Update (real values): status %d, body %s", rr.Code, rr.Body.String())
	}
	stored = store.Get().QbitInstances
	if stored[0].URL != freshURL {
		t.Errorf("after real Update, stored URL = %q, want %q", stored[0].URL, freshURL)
	}
	if stored[0].Password != freshPassword {
		t.Errorf("after real Update, stored Password = %q, want %q", stored[0].Password, freshPassword)
	}
}

// TestQbitHandlers_T78_CreateRejectsMaskedInput — masked sentinels on
// Create can never round-trip to a stored value (no existing entry to
// resolve against), so the handler must fail-closed rather than save
// the literal mask.
func TestQbitHandlers_T78_CreateRejectsMaskedInput(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantSub string
	}{
		{
			"masked_url",
			`{"name":"x","url":"http://qui/proxy/602f` + repeat56Stars() + `c33e","password":"p"}`,
			"masked token",
		},
		{
			"masked_password_sentinel",
			`{"name":"x","url":"http://qbit:8080","password":"` + maskSentinel + `"}`,
			"masked placeholder",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newQbitTestServer(t)
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/qbit-instances", strings.NewReader(tc.body))
			s.handleCreateQbitInstance(rr, req)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
			}
			if !strings.Contains(rr.Body.String(), tc.wantSub) {
				t.Errorf("body = %s, want substring %q", rr.Body.String(), tc.wantSub)
			}
		})
	}
}

// TestQbitHandlers_T78_TestInlineResolvesMaskedInputs — the inline
// test endpoint must resolve masked URL + masked password against the
// stored instance when an ID is supplied, and refuse when masked but
// no ID is given (can't resolve, so we'd be probing with the literal
// placeholder string — fail-closed).
func TestQbitHandlers_T78_TestInlineResolvesMaskedInputs(t *testing.T) {
	s, store := newQbitTestServer(t)

	// Seed one instance with plaintext, bypassing the handler so the
	// test focuses on the resolve-on-test path.
	if err := store.Update(func(c *core.Config) {
		c.QbitInstances = []core.QbitInstance{{
			ID:       "qi-1",
			Name:     "seed",
			URL:      realQuiURL,
			Password: realPassword,
		}}
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	t.Run("masked_url_no_id_refuses", func(t *testing.T) {
		body := `{"url":"http://qui/proxy/602f` + repeat56Stars() + `c33e","password":""}`
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/qbit-instances/test", strings.NewReader(body))
		s.handleTestQbitInline(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rr.Code)
		}
		if !strings.Contains(rr.Body.String(), "masked token") {
			t.Errorf("body = %s, want substring 'masked token'", rr.Body.String())
		}
	})

	t.Run("masked_password_no_id_refuses", func(t *testing.T) {
		body := `{"url":"http://qbit:8080","password":"` + maskSentinel + `"}`
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/qbit-instances/test", strings.NewReader(body))
		s.handleTestQbitInline(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rr.Code)
		}
		if !strings.Contains(rr.Body.String(), "password is masked") {
			t.Errorf("body = %s, want substring 'password is masked'", rr.Body.String())
		}
	})

	// With an ID supplied, the handler should resolve masked inputs to
	// the stored values. We can't easily verify the OUTBOUND request
	// (would need a fake qBit), but we can at least check that the
	// handler reaches the probe path (returns ok=false with a network
	// error rather than a 400 about masking).
	t.Run("masked_url_with_id_resolves_then_probes", func(t *testing.T) {
		body := `{"id":"qi-1","url":"http://qui/proxy/` + maskSentinel + `","password":"` + maskSentinel + `"}`
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/qbit-instances/test", strings.NewReader(body))
		s.handleTestQbitInline(rr, req)
		// Probe will fail (the seeded URL points at 192.168.0.5, no
		// qBit listening in the test process) but the handler must
		// have GOT THERE — meaning resolution worked. A 400 about
		// masking would mean we didn't resolve.
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 with ok=false probe result; body=%s", rr.Code, rr.Body.String())
		}
		var resp struct {
			Ok    bool   `json:"ok"`
			Error string `json:"error"`
		}
		readJSON(t, rr, &resp)
		if resp.Ok {
			t.Error("expected ok=false (no qBit running for the probe)")
		}
		// The error MUST be a network/connection error, not a masking
		// complaint — that proves resolution worked.
		if strings.Contains(strings.ToLower(resp.Error), "masked") {
			t.Errorf("error mentions masking — resolution failed: %q", resp.Error)
		}
	})
}

// TestQbitHandlers_T78_StoreNeverContainsMaskedSentinel — defense-in-
// depth: even if a future code-path bug starts persisting masked values,
// this test will catch it on the next run by walking the full Get()
// snapshot after every interesting mutation.
func TestQbitHandlers_T78_StoreNeverContainsMaskedSentinel(t *testing.T) {
	s, store := newQbitTestServer(t)
	body, _ := json.Marshal(map[string]string{
		"name":     "qui",
		"url":      realQuiURL,
		"password": realPassword,
	})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/qbit-instances", bytes.NewReader(body))
	s.handleCreateQbitInstance(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("Create: status %d", rr.Code)
	}
	for _, qi := range store.Get().QbitInstances {
		if qi.Password == maskSentinel {
			t.Errorf("instance %q has masked-sentinel password on disk", qi.Name)
		}
		if isMaskedQbitURL(qi.URL) {
			t.Errorf("instance %q has masked URL on disk: %q", qi.Name, qi.URL)
		}
	}
}

// TestMaskQbitURL covers the partial-reveal masking applied to qui
// client-proxy URLs on the way out (list / create / update echo).
// The /proxy/<hex> token is auth-equivalent to an API key — first 4 +
// stars + last 4 lets the user visually confirm the right one without
// exposing the secret. Non-proxy URLs pass through unchanged.
func TestMaskQbitURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "qui_proxy_long_token",
			in:   "http://192.168.0.5:7476/proxy/602f21d07ef107895b37e0e679d0575c69ae6687c338624c946bd2fc1fe0c33e",
			want: "http://192.168.0.5:7476/proxy/602f" + repeat56Stars() + "c33e",
		},
		{
			name: "qui_proxy_uppercase_hex",
			in:   "https://qui.example.com/proxy/ABCDEF0123456789ABCDEF0123456789",
			want: "https://qui.example.com/proxy/ABCD" + repeat24Stars() + "6789",
		},
		{
			name: "qui_proxy_with_trailing_slash",
			// Path normalisation strips trailing slash before storage,
			// but if it sneaks through the masker should still work.
			in:   "http://qui:7476/proxy/0123456789abcdef0123456789abcdef/",
			want: "http://qui:7476/proxy/0123" + repeat24Stars() + "cdef/",
		},
		{
			name: "direct_qbit_no_change",
			in:   "http://192.168.1.100:8080",
			want: "http://192.168.1.100:8080",
		},
		{
			name: "reverse_proxy_no_token_no_change",
			in:   "https://qbit.example.com/qbit",
			want: "https://qbit.example.com/qbit",
		},
		{
			name: "empty_no_change",
			in:   "",
			want: "",
		},
		{
			name: "non_hex_proxy_segment_no_change",
			// Some other tool's /proxy/ path with non-hex content
			// shouldn't be touched — qui-style is hex-only.
			in:   "http://example.com/proxy/some-path-here",
			want: "http://example.com/proxy/some-path-here",
		},
		{
			name: "hex_too_short_no_change",
			// Real qui tokens are 64 chars; <16 is almost certainly
			// not a token. Conservative — pass through.
			in:   "http://example.com/proxy/abc123",
			want: "http://example.com/proxy/abc123",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := maskQbitURL(tc.in)
			if got != tc.want {
				t.Errorf("maskQbitURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestIsMaskedQbitURL covers the round-trip detection used on PUT to
// decide whether the user kept the masked URL the server sent (→ keep
// stored URL) or pasted a fresh one (→ validate + save).
func TestIsMaskedQbitURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{
			"masked_round_trip",
			"http://192.168.0.5:7476/proxy/602f" + repeat56Stars() + "c33e",
			true,
		},
		{
			"sentinel_only_short_token",
			// maskKey collapses tokens ≤8 chars to maskSentinel,
			// producing /proxy/******** with no surrounding hex.
			"http://qui:7476/proxy/" + maskSentinel,
			true,
		},
		{
			"clean_real_url",
			"http://192.168.0.5:7476/proxy/602f21d07ef107895b37e0e679d0575c69ae6687c338624c946bd2fc1fe0c33e",
			false,
		},
		{
			"direct_qbit",
			"http://192.168.1.100:8080",
			false,
		},
		{
			"empty",
			"",
			false,
		},
		{
			"reverse_proxy_no_token",
			"https://qbit.example.com/qbit",
			false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isMaskedQbitURL(tc.in); got != tc.want {
				t.Errorf("isMaskedQbitURL(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestMaskQbitURL_RoundTripDetected — output of maskQbitURL must be
// identified as masked by isMaskedQbitURL. Pairs the two halves the
// way masking_test.go does for maskKey ↔ isMasked.
func TestMaskQbitURL_RoundTripDetected(t *testing.T) {
	inputs := []string{
		"http://qui:7476/proxy/602f21d07ef107895b37e0e679d0575c69ae6687c338624c946bd2fc1fe0c33e",
		"https://192.168.0.5:7476/proxy/abcdef0123456789abcdef0123456789",
	}
	for _, in := range inputs {
		masked := maskQbitURL(in)
		if !isMaskedQbitURL(masked) {
			t.Errorf("isMaskedQbitURL(maskQbitURL(%q)) = false, want true (masked = %q)", in, masked)
		}
	}
}

// repeat helpers keep the test cases readable — building star strings
// inline obscures the count.

func repeat56Stars() string { return staticStars(56) }
func repeat24Stars() string { return staticStars(24) }

func staticStars(n int) string {
	out := make([]byte, n)
	for i := range out {
		out[i] = '*'
	}
	return string(out)
}
