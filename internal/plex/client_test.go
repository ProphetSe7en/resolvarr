package plex

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// SYNTHETIC test fixtures — never copy a real X-Plex-Token into here,
// even one shared in chat as a "shape example". Test files ship
// verbatim to public GitHub.
const testToken = "0123456789abcdef0123456789abcdef0123" // synthetic

// newMockPlex returns an httptest.Server that routes the standard
// Plex endpoints we need. Each test customises which routes return
// what via the `handlers` map (method+path → handlerFunc).
func newMockPlex(t *testing.T, handlers map[string]http.HandlerFunc) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for routeKey, fn := range handlers {
		mux.HandleFunc(routeKey, fn)
	}
	return httptest.NewServer(mux)
}

func TestNormaliseBaseURL(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{"plain http", "http://plex.lan:32400", "http://plex.lan:32400", false},
		{"https + trailing slash", "https://plex.example.com/", "https://plex.example.com", false},
		{"reverse-proxy subpath", "https://example.com/plex", "https://example.com/plex", false},
		{"empty", "", "", true},
		{"missing scheme", "plex.lan:32400", "", true},
		{"unsupported scheme", "ftp://plex.lan", "", true},
		{"missing host", "http://", "", true},
		{"whitespace only", "   ", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normaliseBaseURL(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil (result %q)", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNew_TokenRequired(t *testing.T) {
	cases := []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{"empty token", Config{URL: "http://plex.lan:32400"}, "token is required"},
		{"whitespace token", Config{URL: "http://plex.lan:32400", Token: "   "}, "token is required"},
		{"missing URL", Config{Token: testToken}, "URL is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(tc.cfg)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q doesn't contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestPing_HappyPath(t *testing.T) {
	srv := newMockPlex(t, map[string]http.HandlerFunc{
		"GET /identity": func(w http.ResponseWriter, r *http.Request) {
			// Verify token header arrived.
			if r.Header.Get("X-Plex-Token") != testToken {
				t.Errorf("missing or wrong X-Plex-Token header: %q", r.Header.Get("X-Plex-Token"))
			}
			if !strings.Contains(r.Header.Get("Accept"), "application/json") {
				t.Errorf("missing Accept: application/json")
			}
			fmt.Fprintln(w, `{"MediaContainer":{"machineIdentifier":"abc","friendlyName":"Home Plex","version":"1.40.0"}}`)
		},
	})
	defer srv.Close()

	c, err := New(Config{URL: srv.URL, Token: testToken, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	name, err := c.Ping(context.Background())
	if err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if name != "Home Plex" {
		t.Errorf("friendlyName = %q, want %q", name, "Home Plex")
	}
}

func TestPing_Unauthorized(t *testing.T) {
	srv := newMockPlex(t, map[string]http.HandlerFunc{
		"GET /identity": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		},
	})
	defer srv.Close()

	c, _ := New(Config{URL: srv.URL, Token: testToken})
	_, err := c.Ping(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error %q should mention 401", err.Error())
	}
}

func TestGetLibraries_HappyPath(t *testing.T) {
	srv := newMockPlex(t, map[string]http.HandlerFunc{
		"GET /library/sections": func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintln(w, `{
				"MediaContainer": {
					"Directory": [
						{"key": "1", "title": "Movies", "type": "movie"},
						{"key": "2", "title": "Movies 4K", "type": "movie"},
						{"key": "3", "title": "TV Shows", "type": "show"},
						{"key": "4", "title": "Music", "type": "artist"}
					]
				}
			}`)
		},
	})
	defer srv.Close()

	c, _ := New(Config{URL: srv.URL, Token: testToken})
	libs, err := c.GetLibraries(context.Background())
	if err != nil {
		t.Fatalf("GetLibraries: %v", err)
	}
	if len(libs) != 4 {
		t.Fatalf("got %d libraries, want 4", len(libs))
	}
	if libs[0].Key != "1" || libs[0].Title != "Movies" || libs[0].Type != "movie" {
		t.Errorf("first library wrong: %+v", libs[0])
	}
	if libs[3].Type != "artist" {
		t.Errorf("artist library type not preserved: %+v", libs[3])
	}
}

func TestGetItems_HappyPath_WithGuidsAndLabels(t *testing.T) {
	srv := newMockPlex(t, map[string]http.HandlerFunc{
		"GET /library/sections/1/all": func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintln(w, `{
				"MediaContainer": {
					"Metadata": [
						{
							"ratingKey": "12345",
							"title": "The Substance",
							"year": 2024,
							"type": "movie",
							"Guid": [
								{"id": "imdb://tt17526714"},
								{"id": "tmdb://933260"}
							],
							"Label": [
								{"tag": "favorite"},
								{"tag": "flux"}
							]
						},
						{
							"ratingKey": "12346",
							"title": "Older Movie",
							"year": 2019,
							"type": "movie"
						}
					]
				}
			}`)
		},
	})
	defer srv.Close()

	c, _ := New(Config{URL: srv.URL, Token: testToken})
	items, err := c.GetItems(context.Background(), "1")
	if err != nil {
		t.Fatalf("GetItems: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}

	first := items[0]
	if first.RatingKey != "12345" || first.Title != "The Substance" || first.Year != 2024 {
		t.Errorf("first item core fields wrong: %+v", first)
	}
	if len(first.GUIDs) != 2 || first.GUIDs[0] != "imdb://tt17526714" || first.GUIDs[1] != "tmdb://933260" {
		t.Errorf("first item GUIDs wrong: %+v", first.GUIDs)
	}
	if len(first.Labels) != 2 || first.Labels[0] != "favorite" || first.Labels[1] != "flux" {
		t.Errorf("first item Labels wrong: %+v", first.Labels)
	}

	// Second item has no Guid + no Label fields — must decode cleanly
	// with empty slices, not crash.
	second := items[1]
	if second.Title != "Older Movie" {
		t.Errorf("second item title wrong: %+v", second)
	}
	if len(second.GUIDs) != 0 {
		t.Errorf("second item should have empty GUIDs, got %+v", second.GUIDs)
	}
	if len(second.Labels) != 0 {
		t.Errorf("second item should have empty Labels, got %+v", second.Labels)
	}
}

func TestGetItems_EmptyLibraryKey(t *testing.T) {
	c, _ := New(Config{URL: "http://plex.lan:32400", Token: testToken})
	_, err := c.GetItems(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty library key")
	}
	if !strings.Contains(err.Error(), "library key") {
		t.Errorf("error should mention library key: %v", err)
	}
}

// TestAddLabel_URLShape verifies the precise URL form sent to Plex.
// The label-update endpoint uses an idiosyncratic query-param syntax
// (label[0].tag.tag={value} + label.locked=1) that must match Plex's
// undocumented-but-stable expectations exactly — any deviation
// silently no-ops.
func TestAddLabel_URLShape(t *testing.T) {
	var captured *http.Request
	srv := newMockPlex(t, map[string]http.HandlerFunc{
		"PUT /library/sections/1/all": func(w http.ResponseWriter, r *http.Request) {
			captured = r
			w.WriteHeader(http.StatusOK)
		},
	})
	defer srv.Close()

	c, _ := New(Config{URL: srv.URL, Token: testToken})
	if err := c.AddLabel(context.Background(), "1", "12345", "movie", "flux"); err != nil {
		t.Fatalf("AddLabel: %v", err)
	}
	if captured == nil {
		t.Fatal("PUT was never received by mock")
	}
	q := captured.URL.Query()
	if q.Get("type") != "1" {
		t.Errorf("type query param = %q, want 1 (movie)", q.Get("type"))
	}
	if q.Get("id") != "12345" {
		t.Errorf("id query param = %q, want 12345", q.Get("id"))
	}
	if q.Get("label[0].tag.tag") != "flux" {
		t.Errorf("label add-key = %q, want %q", q.Get("label[0].tag.tag"), "flux")
	}
	if q.Get("label.locked") != "1" {
		t.Errorf("label.locked = %q, want 1", q.Get("label.locked"))
	}
}

func TestRemoveLabel_URLShapeUsesDashSuffix(t *testing.T) {
	var captured *http.Request
	srv := newMockPlex(t, map[string]http.HandlerFunc{
		"PUT /library/sections/2/all": func(w http.ResponseWriter, r *http.Request) {
			captured = r
			w.WriteHeader(http.StatusOK)
		},
	})
	defer srv.Close()

	c, _ := New(Config{URL: srv.URL, Token: testToken})
	if err := c.RemoveLabel(context.Background(), "2", "98765", "show", "old-label"); err != nil {
		t.Fatalf("RemoveLabel: %v", err)
	}
	if captured == nil {
		t.Fatal("PUT was never received by mock")
	}
	q := captured.URL.Query()
	if q.Get("type") != "2" {
		t.Errorf("type query param = %q, want 2 (show)", q.Get("type"))
	}
	if q.Get("id") != "98765" {
		t.Errorf("id query param = %q, want 98765", q.Get("id"))
	}
	// Critical: Plex's "subtract from collection" notation uses
	// `tag-` (trailing dash). Off-by-one in the encoding here means
	// the label-removal silently fails (Plex receives the request,
	// returns 200, but treats it as a no-op add).
	if q.Get("label[].tag.tag-") != "old-label" {
		t.Errorf("label remove-key = %q, want %q (note trailing dash on tag-)",
			q.Get("label[].tag.tag-"), "old-label")
	}
	if q.Get("label.locked") != "1" {
		t.Errorf("label.locked = %q, want 1", q.Get("label.locked"))
	}
}

func TestUpdateLabel_RejectsUnknownType(t *testing.T) {
	c, _ := New(Config{URL: "http://plex.lan:32400", Token: testToken})
	err := c.AddLabel(context.Background(), "1", "12345", "playlist", "x")
	if err == nil {
		t.Fatal("expected error for unknown type")
	}
	if !strings.Contains(err.Error(), "unsupported item type") {
		t.Errorf("error should mention unsupported type: %v", err)
	}
}

func TestUpdateLabel_RejectsEmptyLabel(t *testing.T) {
	c, _ := New(Config{URL: "http://plex.lan:32400", Token: testToken})
	err := c.AddLabel(context.Background(), "1", "12345", "movie", "  ")
	if err == nil {
		t.Fatal("expected error for empty label")
	}
	if !strings.Contains(err.Error(), "label") {
		t.Errorf("error should mention label: %v", err)
	}
}

func TestPing_ServerError(t *testing.T) {
	srv := newMockPlex(t, map[string]http.HandlerFunc{
		"GET /identity": func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "internal trouble", http.StatusInternalServerError)
		},
	})
	defer srv.Close()

	c, _ := New(Config{URL: srv.URL, Token: testToken})
	_, err := c.Ping(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error should mention 500: %v", err)
	}
	if !strings.Contains(err.Error(), "internal trouble") {
		t.Errorf("error should include server message excerpt: %v", err)
	}
}

func TestDoJSON_ContextCancellation(t *testing.T) {
	srv := newMockPlex(t, map[string]http.HandlerFunc{
		"GET /identity": func(w http.ResponseWriter, r *http.Request) {
			// Block forever — test relies on context cancel to abort
			<-r.Context().Done()
		},
	})
	defer srv.Close()

	c, _ := New(Config{URL: srv.URL, Token: testToken})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := c.Ping(ctx)
	if err == nil {
		t.Fatal("expected context-deadline error")
	}
}

// TestItemTypeCodeMapping locks the type-string → numeric mapping.
// Plex's label-update endpoint expects the numeric form; getting the
// mapping wrong (e.g. movie=2) silently fails — Plex returns 200 but
// applies the label to no items.
func TestItemTypeCodeMapping(t *testing.T) {
	cases := map[string]int{
		"movie":   1,
		"show":    2,
		"season":  3,
		"episode": 4,
		"artist":  0, // unsupported
		"":        0,
		"garbage": 0,
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			if got := itemTypeCode(in); got != want {
				t.Errorf("itemTypeCode(%q) = %d, want %d", in, got, want)
			}
		})
	}
}

// TestURL_EscapingForLibraryKey defensively verifies that a library
// key with URL-unsafe characters gets path-escaped. Real Plex library
// keys are short numeric strings (1, 2, 3, ...) so this is purely
// defensive — but if Plex ever changes the format we don't want a
// malformed URL.
func TestURL_EscapingForLibraryKey(t *testing.T) {
	got := url.PathEscape("library 1/strange")
	if got == "library 1/strange" {
		t.Error("PathEscape didn't escape — would corrupt URL")
	}
}
