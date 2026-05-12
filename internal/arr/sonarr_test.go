package arr

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newSonarrTestClient(t *testing.T, h http.Handler) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	return &Client{
		URL:    srv.URL,
		APIKey: "test-api-key",
		HTTP:   srv.Client(),
	}, srv
}

func TestListSeries_HappyPath(t *testing.T) {
	body := `[
		{"id": 1, "title": "Show A", "status": "ended", "monitored": true},
		{"id": 2, "title": "Show B", "status": "continuing", "monitored": false}
	]`
	c, srv := newSonarrTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/api/v3/series" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "bad path", 400)
			return
		}
		if r.Header.Get("X-Api-Key") != "test-api-key" {
			t.Errorf("missing X-Api-Key header")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()

	out, err := c.ListSeries(context.Background())
	if err != nil {
		t.Fatalf("ListSeries: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("want 2 series, got %d", len(out))
	}
	if out[0].ID != 1 || out[0].Title != "Show A" || out[0].Status != "ended" || !out[0].Monitored {
		t.Errorf("series[0] mismatch: %+v", out[0])
	}
	if out[1].ID != 2 || out[1].Title != "Show B" || out[1].Status != "continuing" || out[1].Monitored {
		t.Errorf("series[1] mismatch: %+v", out[1])
	}
}

func TestListSeries_HTTPError(t *testing.T) {
	c, srv := newSonarrTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", 500)
	}))
	defer srv.Close()
	if _, err := c.ListSeries(context.Background()); err == nil {
		t.Fatal("expected error on 500, got nil")
	}
}

func TestListEpisodesForSeries_HappyPath(t *testing.T) {
	body := `[
		{"id": 100, "seriesId": 1, "seasonNumber": 1, "episodeNumber": 1, "title": "Pilot",
		 "airDateUtc": "2026-01-15T20:00:00Z", "monitored": true, "hasFile": true},
		{"id": 101, "seriesId": 1, "seasonNumber": 1, "episodeNumber": 2, "title": "Two",
		 "airDateUtc": "2026-01-22T20:00:00Z", "monitored": true, "hasFile": false}
	]`
	c, srv := newSonarrTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/api/v3/episode" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "bad path", 400)
			return
		}
		if got := r.URL.Query().Get("seriesId"); got != "1" {
			t.Errorf("seriesId query: got %q want 1", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()

	out, err := c.ListEpisodesForSeries(context.Background(), 1)
	if err != nil {
		t.Fatalf("ListEpisodesForSeries: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("want 2 episodes, got %d", len(out))
	}
	wantAir1 := time.Date(2026, 1, 15, 20, 0, 0, 0, time.UTC)
	if !out[0].AirDateUtc.Equal(wantAir1) {
		t.Errorf("ep[0].AirDateUtc: got %v want %v", out[0].AirDateUtc, wantAir1)
	}
	if !out[0].HasFile {
		t.Errorf("ep[0].HasFile should be true")
	}
	if out[1].HasFile {
		t.Errorf("ep[1].HasFile should be false")
	}
	if !out[0].Monitored || !out[1].Monitored {
		t.Errorf("expected both monitored")
	}
}

func TestSearchEpisodes_HappyPath(t *testing.T) {
	c, srv := newSonarrTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/v3/command" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "bad path", 400)
			return
		}
		var got map[string]any
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Errorf("bad body: %v (body=%s)", err, string(raw))
			http.Error(w, "bad body", 400)
			return
		}
		if got["name"] != "EpisodeSearch" {
			t.Errorf("name: got %v want EpisodeSearch", got["name"])
		}
		ids, ok := got["episodeIds"].([]any)
		if !ok {
			t.Errorf("episodeIds missing or wrong type: %v", got["episodeIds"])
		} else if len(ids) != 3 {
			t.Errorf("episodeIds len: got %d want 3", len(ids))
		}
		w.WriteHeader(201)
		_, _ = io.WriteString(w, `{"id": 123}`)
	}))
	defer srv.Close()

	if err := c.SearchEpisodes(context.Background(), []int{1, 2, 3}); err != nil {
		t.Fatalf("SearchEpisodes: %v", err)
	}
}

func TestSearchEpisodes_EmptyNoCall(t *testing.T) {
	called := false
	c, srv := newSonarrTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()
	if err := c.SearchEpisodes(context.Background(), nil); err != nil {
		t.Fatalf("SearchEpisodes(nil): %v", err)
	}
	if called {
		t.Error("expected no HTTP call for empty episodeIds")
	}
}

func TestSearchEpisodes_HTTPError(t *testing.T) {
	c, srv := newSonarrTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad request", 400)
	}))
	defer srv.Close()
	err := c.SearchEpisodes(context.Background(), []int{1})
	if err == nil {
		t.Fatal("expected error on 400, got nil")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("expected HTTP 400 in error, got: %v", err)
	}
}
