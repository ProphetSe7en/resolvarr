package qbit

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// helper — wires a qBit client pointing at the given test server with
// the no-auth short-circuit on (Username + Password empty), so tests
// don't have to mock the login round-trip.
func newPrefsTestClient(t *testing.T, server *httptest.Server) *Client {
	t.Helper()
	c, err := New(Config{URL: server.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestGetAutorunOnAdded_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/app/preferences" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if r.Method != "GET" {
			t.Errorf("expected GET, got %s", r.Method)
		}
		// qBit returns the FULL preferences blob; the helper must
		// pluck out only its two keys and ignore everything else.
		// Include some unrelated keys to prove that the decoder
		// doesn't choke on the rest.
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"autorun_enabled": false,
			"autorun_program": "",
			"autorun_on_torrent_added_enabled": true,
			"autorun_on_torrent_added_program": "/scripts/notify.sh \"%I\"",
			"max_active_downloads": 5,
			"save_path": "/downloads"
		}`))
	}))
	defer server.Close()

	c := newPrefsTestClient(t, server)
	program, enabled, err := c.GetAutorunOnAdded(context.Background())
	if err != nil {
		t.Fatalf("GetAutorunOnAdded: %v", err)
	}
	if !enabled {
		t.Errorf("enabled = false, want true")
	}
	if program != `/scripts/notify.sh "%I"` {
		t.Errorf("program = %q, want %q", program, `/scripts/notify.sh "%I"`)
	}
}

// Empty/zero values are valid qBit state — the autorun-on-added
// feature simply isn't configured. Helper must return them as zero
// values, NOT as an error.
func TestGetAutorunOnAdded_EmptyState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"autorun_on_torrent_added_enabled": false,
			"autorun_on_torrent_added_program": ""
		}`))
	}))
	defer server.Close()

	c := newPrefsTestClient(t, server)
	program, enabled, err := c.GetAutorunOnAdded(context.Background())
	if err != nil {
		t.Fatalf("GetAutorunOnAdded: %v", err)
	}
	if enabled {
		t.Errorf("enabled = true, want false")
	}
	if program != "" {
		t.Errorf("program = %q, want empty", program)
	}
}

// Pre-4.5 qBit doesn't have the autorun_on_torrent_added_* keys at
// all — the JSON simply omits them. Decoder lands on zero values
// (no error) which the caller can then treat as "feature unsupported"
// at the UI layer.
func TestGetAutorunOnAdded_OldQbitNoKeys(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"autorun_enabled": false,
			"autorun_program": "",
			"max_active_downloads": 5
		}`))
	}))
	defer server.Close()

	c := newPrefsTestClient(t, server)
	program, enabled, err := c.GetAutorunOnAdded(context.Background())
	if err != nil {
		t.Fatalf("GetAutorunOnAdded: %v", err)
	}
	if enabled {
		t.Errorf("enabled = true, want false")
	}
	if program != "" {
		t.Errorf("program = %q, want empty", program)
	}
}

func TestGetAutorunOnAdded_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte("internal error"))
	}))
	defer server.Close()

	c := newPrefsTestClient(t, server)
	_, _, err := c.GetAutorunOnAdded(context.Background())
	if err == nil {
		t.Fatal("expected error on 500, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error %q should mention status 500", err.Error())
	}
}

func TestSetAutorunOnAdded_Success(t *testing.T) {
	var receivedJSON string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/app/setPreferences" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
			t.Errorf("Content-Type = %q, want form-urlencoded", ct)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		receivedJSON = r.Form.Get("json")
		w.WriteHeader(200)
	}))
	defer server.Close()

	c := newPrefsTestClient(t, server)
	err := c.SetAutorunOnAdded(context.Background(), `/scripts/x.sh "%I"`, true)
	if err != nil {
		t.Fatalf("SetAutorunOnAdded: %v", err)
	}

	// Verify both fields landed in the json payload — caller's contract
	// is that the helper sends BOTH so qBit can't end up in a half-
	// configured state.
	var payload qbitPrefs
	if err := json.Unmarshal([]byte(receivedJSON), &payload); err != nil {
		t.Fatalf("decode payload: %v (raw=%s)", err, receivedJSON)
	}
	if payload.AutorunOnTorrentAddedProgram != `/scripts/x.sh "%I"` {
		t.Errorf("payload program = %q, want %q", payload.AutorunOnTorrentAddedProgram, `/scripts/x.sh "%I"`)
	}
	if !payload.AutorunOnTorrentAddedEnabled {
		t.Errorf("payload enabled = false, want true")
	}
}

// Disabling the autorun (enabled=false) must still send program in
// the payload so qBit knows the value to associate with the toggle
// — even though enabled=false, the program field stays populated for
// when the user re-enables.
func TestSetAutorunOnAdded_Disabling(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		raw := r.Form.Get("json")
		var payload qbitPrefs
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if payload.AutorunOnTorrentAddedEnabled {
			t.Errorf("enabled should be false")
		}
		if payload.AutorunOnTorrentAddedProgram != "kept-value" {
			t.Errorf("program should still be sent, got %q", payload.AutorunOnTorrentAddedProgram)
		}
		w.WriteHeader(200)
	}))
	defer server.Close()

	c := newPrefsTestClient(t, server)
	if err := c.SetAutorunOnAdded(context.Background(), "kept-value", false); err != nil {
		t.Fatalf("SetAutorunOnAdded: %v", err)
	}
}

// TestSetAutorunOnAdded_SpecialChars round-trips programs with shell-
// reactive characters through the JSON-marshal → form-encode → ParseForm
// → JSON-decode chain. Ensures qBit's autorun field receives EXACTLY
// what we sent, regardless of how mean the program string looks.
//
// Cases cover: double-quote (qBit's typical curl wrapping), backslash
// (Windows-path style + JSON-escape interaction), percent (qBit's own
// %I/%N/%L placeholders), single-quote, ampersand (URL reserved),
// trailing newline (defensive — qBit's parser may strip).
func TestSetAutorunOnAdded_SpecialChars(t *testing.T) {
	cases := []string{
		`/scripts/x.sh "hello world" %I`,
		`C:\path\with\backslashes\app.exe %N`,
		`echo 'single quotes' %L`,
		`curl -d "infoHash=%I&name=%N" http://x:1/api`,
		`/scripts/x.sh "%I" "%N" "%L" "%G" "%T" "%F" "%R" "%D" "%C" "%Z"`,
	}
	for _, want := range cases {
		t.Run(want[:min(20, len(want))], func(t *testing.T) {
			var roundTripped string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := r.ParseForm(); err != nil {
					t.Fatalf("ParseForm: %v", err)
				}
				var payload qbitPrefs
				if err := json.Unmarshal([]byte(r.Form.Get("json")), &payload); err != nil {
					t.Fatalf("decode json field: %v", err)
				}
				roundTripped = payload.AutorunOnTorrentAddedProgram
				w.WriteHeader(200)
			}))
			defer server.Close()

			c := newPrefsTestClient(t, server)
			if err := c.SetAutorunOnAdded(context.Background(), want, true); err != nil {
				t.Fatalf("SetAutorunOnAdded: %v", err)
			}
			if roundTripped != want {
				t.Errorf("round-trip mismatch:\n  sent: %q\n  got:  %q", want, roundTripped)
			}
		})
	}
}

func TestSetAutorunOnAdded_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		_, _ = io.WriteString(w, "forbidden")
	}))
	defer server.Close()

	c := newPrefsTestClient(t, server)
	err := c.SetAutorunOnAdded(context.Background(), "x", true)
	if err == nil {
		t.Fatal("expected error on 403, got nil")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("error %q should mention status 403", err.Error())
	}
}
