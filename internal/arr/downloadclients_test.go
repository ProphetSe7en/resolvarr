package arr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// downloadclients_test.go — coverage for the ArrDownloadClient struct
// helpers + the Client.ListDownloadClients / ListHistoryByDownloadID
// network methods. Uses httptest.Server to stand in for the Arr.

func TestArrDownloadClient_StringField(t *testing.T) {
	c := ArrDownloadClient{Fields: []ArrDownloadClientField{
		{Name: "category", Value: "qbit-movies"},
		{Name: "port", Value: float64(8080)},
		{Name: "tls", Value: true},
		{Name: "ratio", Value: float64(1.5)},
	}}
	cases := []struct {
		field string
		want  string
	}{
		{"category", "qbit-movies"},
		{"port", "8080"},     // float64-as-int coerced cleanly
		{"tls", "true"},      // bool coerced to lower-case label
		{"ratio", "1.5"},     // non-int float kept as %g
		{"missing", ""},      // unknown field → empty
	}
	for _, tc := range cases {
		got := c.StringField(tc.field)
		if got != tc.want {
			t.Errorf("StringField(%q) = %q, want %q", tc.field, got, tc.want)
		}
	}
}

func TestArrDownloadClient_IntField(t *testing.T) {
	c := ArrDownloadClient{Fields: []ArrDownloadClientField{
		{Name: "port", Value: float64(8080)},
		{Name: "ratio", Value: float64(1.5)}, // truncates to int
		{Name: "label", Value: "hello"},      // non-numeric → 0
	}}
	cases := []struct {
		field string
		want  int
	}{
		{"port", 8080},
		{"ratio", 1},
		{"label", 0},
		{"missing", 0},
	}
	for _, tc := range cases {
		got := c.IntField(tc.field)
		if got != tc.want {
			t.Errorf("IntField(%q) = %d, want %d", tc.field, got, tc.want)
		}
	}
}

func TestArrDownloadClient_QbitCategoryHelpers(t *testing.T) {
	// Radarr-style fields.
	radarrClient := ArrDownloadClient{Fields: []ArrDownloadClientField{
		{Name: "movieCategory", Value: "qbit-movies"},
		{Name: "movieImportedCategory", Value: "qbit-movies-imp"},
	}}
	if got := radarrClient.QbitPreImportCategory("radarr"); got != "qbit-movies" {
		t.Errorf("radarr QbitPreImportCategory = %q, want qbit-movies", got)
	}
	if got := radarrClient.QbitPostImportCategory("radarr"); got != "qbit-movies-imp" {
		t.Errorf("radarr QbitPostImportCategory = %q, want qbit-movies-imp", got)
	}

	// Sonarr-style fields.
	sonarrClient := ArrDownloadClient{Fields: []ArrDownloadClientField{
		{Name: "tvCategory", Value: "qbit-tv"},
		{Name: "tvImportedCategory", Value: "qbit-tv-imp"},
	}}
	if got := sonarrClient.QbitPreImportCategory("sonarr"); got != "qbit-tv" {
		t.Errorf("sonarr QbitPreImportCategory = %q, want qbit-tv", got)
	}
	if got := sonarrClient.QbitPostImportCategory("sonarr"); got != "qbit-tv-imp" {
		t.Errorf("sonarr QbitPostImportCategory = %q, want qbit-tv-imp", got)
	}

	// Forward-compat fallback to unified `category` / `importedCategory`.
	fallbackClient := ArrDownloadClient{Fields: []ArrDownloadClientField{
		{Name: "category", Value: "fallback-cat"},
		{Name: "importedCategory", Value: "fallback-imp"},
	}}
	if got := fallbackClient.QbitPreImportCategory("radarr"); got != "fallback-cat" {
		t.Errorf("fallback radarr pre = %q, want fallback-cat", got)
	}
	if got := fallbackClient.QbitPostImportCategory("sonarr"); got != "fallback-imp" {
		t.Errorf("fallback sonarr post = %q, want fallback-imp", got)
	}

	// Empty when neither typed nor unified field set.
	empty := ArrDownloadClient{}
	if got := empty.QbitPreImportCategory("radarr"); got != "" {
		t.Errorf("empty pre = %q, want empty", got)
	}
	if got := empty.QbitPostImportCategory("radarr"); got != "" {
		t.Errorf("empty post = %q, want empty", got)
	}
}

func TestListDownloadClients(t *testing.T) {
	fixture := []ArrDownloadClient{
		{ID: 1, Name: "qbt-main", Implementation: "QBittorrent", Enable: true, Protocol: "torrent",
			Fields: []ArrDownloadClientField{
				{Name: "movieCategory", Value: "qbit-movies"},
				{Name: "movieImportedCategory", Value: "qbit-movies-imp"},
			}},
		{ID: 2, Name: "sab-main", Implementation: "Sabnzbd", Enable: true, Protocol: "usenet"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/downloadclient" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if r.Header.Get("X-Api-Key") != "secret" {
			t.Errorf("X-Api-Key not forwarded")
		}
		_ = json.NewEncoder(w).Encode(fixture)
	}))
	defer srv.Close()
	c := &Client{URL: srv.URL, APIKey: "secret", HTTP: http.DefaultClient}
	got, err := c.ListDownloadClients(context.Background())
	if err != nil {
		t.Fatalf("ListDownloadClients: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].ID != 1 || got[0].Name != "qbt-main" {
		t.Errorf("first row mismatch: %+v", got[0])
	}
	if got[0].QbitPreImportCategory("radarr") != "qbit-movies" {
		t.Errorf("category extraction broken on first row")
	}
}

func TestListDownloadClients_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
	}))
	defer srv.Close()
	c := &Client{URL: srv.URL, APIKey: "bad", HTTP: http.DefaultClient}
	_, err := c.ListDownloadClients(context.Background())
	if err == nil {
		t.Fatal("expected error on 401")
	}
}

func TestListHistoryByDownloadID(t *testing.T) {
	fixture := historyResponse{Records: []HistoryRecord{
		{EventType: "grabbed", Date: time.Now(), DownloadID: "ABCDEF"},
		{EventType: "downloadFolderImported", Date: time.Now(), DownloadID: "ABCDEF"},
	}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("downloadId"); got != "ABCDEF" {
			t.Errorf("downloadId param = %q, want ABCDEF", got)
		}
		if got := r.URL.Query().Get("pageSize"); got != "50" {
			t.Errorf("pageSize param = %q, want 50", got)
		}
		_ = json.NewEncoder(w).Encode(fixture)
	}))
	defer srv.Close()
	c := &Client{URL: srv.URL, APIKey: "k", HTTP: http.DefaultClient}
	got, err := c.ListHistoryByDownloadID(context.Background(), "ABCDEF")
	if err != nil {
		t.Fatalf("ListHistoryByDownloadID: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
}

func TestListHistoryByDownloadID_EncodesSpecialChars(t *testing.T) {
	// downloadID comes from the Connect-event payload (Arr-influenced) — chars
	// like `&`, `#`, `?`, `%` must be URL-encoded so they don't corrupt the
	// query string. Use a value that would split into two query params if the
	// raw string were spliced in: "ABC&foo=bar#frag".
	rawID := "ABC&foo=bar#frag"
	fixture := historyResponse{Records: []HistoryRecord{
		{EventType: "downloadFolderImported", Date: time.Now(), DownloadID: rawID},
	}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The Go net/http server parses the query for us — Query() returns the
		// decoded value of the downloadId param. There must be exactly one
		// downloadId param (not two — the second would mean `&foo=bar` leaked
		// out as a sibling param), and its decoded value must equal the raw ID.
		ids := r.URL.Query()["downloadId"]
		if len(ids) != 1 {
			t.Errorf("downloadId param count = %d, want 1 (got %v)", len(ids), ids)
		}
		if len(ids) >= 1 && ids[0] != rawID {
			t.Errorf("downloadId param = %q, want %q", ids[0], rawID)
		}
		if got := r.URL.Query().Get("foo"); got != "" {
			t.Errorf("foo param leaked through unencoded: %q", got)
		}
		_ = json.NewEncoder(w).Encode(fixture)
	}))
	defer srv.Close()
	c := &Client{URL: srv.URL, APIKey: "k", HTTP: http.DefaultClient}
	got, err := c.ListHistoryByDownloadID(context.Background(), rawID)
	if err != nil {
		t.Fatalf("ListHistoryByDownloadID: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
}

func TestListHistoryByDownloadID_EmptyID(t *testing.T) {
	c := &Client{URL: "http://invalid", APIKey: "k", HTTP: http.DefaultClient}
	if _, err := c.ListHistoryByDownloadID(context.Background(), ""); err == nil {
		t.Error("expected error on empty downloadId")
	}
}

func TestFindImportedEvent(t *testing.T) {
	records := []HistoryRecord{
		{EventType: "grabbed"},
		{EventType: "downloadFolderImported"},
		{EventType: "downloadFailed"},
	}
	if got := FindImportedEvent(records, "radarr"); got == nil || got.EventType != "downloadFolderImported" {
		t.Errorf("radarr FindImportedEvent = %+v, want downloadFolderImported entry", got)
	}
	sonarrRecords := []HistoryRecord{
		{EventType: "grabbed"},
		{EventType: "episodeFileImported"},
	}
	if got := FindImportedEvent(sonarrRecords, "sonarr"); got == nil || got.EventType != "episodeFileImported" {
		t.Errorf("sonarr FindImportedEvent = %+v, want episodeFileImported entry", got)
	}
	// No matching event.
	if got := FindImportedEvent(records, "sonarr"); got != nil {
		t.Errorf("sonarr against radarr records = %+v, want nil", got)
	}
	// Unknown appType → nil.
	if got := FindImportedEvent(records, "weird"); got != nil {
		t.Errorf("unknown appType = %+v, want nil", got)
	}
	// Empty input.
	if got := FindImportedEvent(nil, "radarr"); got != nil {
		t.Errorf("nil records = %+v, want nil", got)
	}
}
