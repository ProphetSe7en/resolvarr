package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"resolvarr/internal/core"
)

// newMissingEpisodesTestServer wires a Server + a fake Sonarr fronted
// by an httptest.Server. The fake routes /api/v3/series + /api/v3/episode
// + /api/v3/command + /api/v3/tag + editor URLs to the supplied handler.
func newMissingEpisodesTestServer(t *testing.T, sonarrHandler http.Handler) (*Server, *core.ConfigStore, *httptest.Server) {
	t.Helper()
	fakeSonarr := httptest.NewServer(sonarrHandler)
	t.Cleanup(fakeSonarr.Close)

	dir := t.TempDir()
	store := core.NewConfigStore(dir)
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Add a Sonarr instance pointing at the fake server, plus a Radarr
	// instance so we can test the type rejection path.
	if err := store.Update(func(c *core.Config) {
		c.Instances = append(c.Instances, core.Instance{
			ID: "s1", Name: "Sonarr-Test", Type: "sonarr",
			URL: fakeSonarr.URL, APIKey: "test-key",
		})
		c.Instances = append(c.Instances, core.Instance{
			ID: "r1", Name: "Radarr-Test", Type: "radarr",
			URL: fakeSonarr.URL, APIKey: "test-key",
		})
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	app := &core.App{
		Config:     store,
		HTTPClient: fakeSonarr.Client(),
	}
	return &Server{App: app}, store, fakeSonarr
}

// TestMissingEpisodesPreview_HappyPath drives the full preview pipeline:
// ListSeries + worker-pooled ListEpisodesForSeries + engine
// DetectMissingEpisodes per series + aggregation.
func TestMissingEpisodesPreview_HappyPath(t *testing.T) {
	// Two series. Series 1: ended, S1 has a gap at episode 3 (4/5 = 80%).
	// Series 2: ended, fully complete — should NOT appear in series list.
	seriesBody := `[
		{"id": 1, "title": "Gap Show", "status": "ended", "monitored": true},
		{"id": 2, "title": "Complete Show", "status": "ended", "monitored": true},
		{"id": 3, "title": "Unmonitored", "status": "ended", "monitored": false}
	]`
	ep1Body := `[
		{"id": 100, "seriesId": 1, "seasonNumber": 1, "episodeNumber": 1, "title": "E1",
		 "airDateUtc": "2024-01-01T00:00:00Z", "monitored": true, "hasFile": true},
		{"id": 101, "seriesId": 1, "seasonNumber": 1, "episodeNumber": 2, "title": "E2",
		 "airDateUtc": "2024-01-08T00:00:00Z", "monitored": true, "hasFile": true},
		{"id": 102, "seriesId": 1, "seasonNumber": 1, "episodeNumber": 3, "title": "E3",
		 "airDateUtc": "2024-01-15T00:00:00Z", "monitored": true, "hasFile": false},
		{"id": 103, "seriesId": 1, "seasonNumber": 1, "episodeNumber": 4, "title": "E4",
		 "airDateUtc": "2024-01-22T00:00:00Z", "monitored": true, "hasFile": true},
		{"id": 104, "seriesId": 1, "seasonNumber": 1, "episodeNumber": 5, "title": "E5",
		 "airDateUtc": "2024-01-29T00:00:00Z", "monitored": true, "hasFile": true}
	]`
	ep2Body := `[
		{"id": 200, "seriesId": 2, "seasonNumber": 1, "episodeNumber": 1, "title": "All1",
		 "airDateUtc": "2024-01-01T00:00:00Z", "monitored": true, "hasFile": true},
		{"id": 201, "seriesId": 2, "seasonNumber": 1, "episodeNumber": 2, "title": "All2",
		 "airDateUtc": "2024-01-08T00:00:00Z", "monitored": true, "hasFile": true}
	]`

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/series", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(seriesBody))
	})
	mux.HandleFunc("/api/v3/episode", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("seriesId") {
		case "1":
			_, _ = w.Write([]byte(ep1Body))
		case "2":
			_, _ = w.Write([]byte(ep2Body))
		default:
			http.Error(w, "unknown", 400)
		}
	})

	s, _, _ := newMissingEpisodesTestServer(t, mux)

	body := `{"instanceId":"s1","threshold":0.7,"bufferHours":24,"includeContinuing":true,"includeEnded":true}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/scan/missing-episodes/preview", strings.NewReader(body))
	s.handleMissingEpisodesPreview(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var resp missingEpisodesPreviewResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.SeriesScanned != 2 {
		t.Errorf("SeriesScanned: got %d want 2 (unmonitored skipped)", resp.SeriesScanned)
	}
	if resp.SeriesWithGaps != 1 {
		t.Errorf("SeriesWithGaps: got %d want 1", resp.SeriesWithGaps)
	}
	if resp.TotalMissingEpisodes != 1 {
		t.Errorf("TotalMissingEpisodes: got %d want 1", resp.TotalMissingEpisodes)
	}
	if len(resp.Series) != 1 {
		t.Fatalf("len(Series): got %d want 1", len(resp.Series))
	}
	if resp.Series[0].SeriesTitle != "Gap Show" {
		t.Errorf("Series[0].SeriesTitle: got %q want %q", resp.Series[0].SeriesTitle, "Gap Show")
	}
	if len(resp.Series[0].Seasons) != 1 {
		t.Fatalf("Series[0].Seasons: got %d want 1", len(resp.Series[0].Seasons))
	}
	missing := resp.Series[0].Seasons[0].MissingEpisodes
	if len(missing) != 1 || missing[0].EpisodeID != 102 {
		t.Errorf("missing episode: %+v", missing)
	}
}

// TestMissingEpisodesPreview_RejectsRadarr verifies the type guard.
func TestMissingEpisodesPreview_RejectsRadarr(t *testing.T) {
	mux := http.NewServeMux()
	s, _, _ := newMissingEpisodesTestServer(t, mux)
	body := `{"instanceId":"r1"}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/scan/missing-episodes/preview", strings.NewReader(body))
	s.handleMissingEpisodesPreview(rr, req)
	if rr.Code != 400 {
		t.Errorf("Radarr should be rejected with 400, got %d (body=%s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Sonarr-only") {
		t.Errorf("expected 'Sonarr-only' in body, got: %s", rr.Body.String())
	}
}

func TestMissingEpisodesPreview_RejectsMissingInstance(t *testing.T) {
	mux := http.NewServeMux()
	s, _, _ := newMissingEpisodesTestServer(t, mux)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/scan/missing-episodes/preview", strings.NewReader(`{}`))
	s.handleMissingEpisodesPreview(rr, req)
	if rr.Code != 400 {
		t.Errorf("missing instanceId should be 400, got %d", rr.Code)
	}
}

func TestMissingEpisodesPreview_RejectsBadThreshold(t *testing.T) {
	mux := http.NewServeMux()
	s, _, _ := newMissingEpisodesTestServer(t, mux)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/scan/missing-episodes/preview",
		strings.NewReader(`{"instanceId":"s1","threshold":1.5}`))
	s.handleMissingEpisodesPreview(rr, req)
	if rr.Code != 400 {
		t.Errorf("threshold>1 should be 400, got %d", rr.Code)
	}
}

func TestMissingEpisodesPreview_RejectsBadBody(t *testing.T) {
	mux := http.NewServeMux()
	s, _, _ := newMissingEpisodesTestServer(t, mux)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/scan/missing-episodes/preview",
		strings.NewReader(`not json`))
	s.handleMissingEpisodesPreview(rr, req)
	if rr.Code != 400 {
		t.Errorf("bad body should be 400, got %d", rr.Code)
	}
}

// TestMissingEpisodesSearch verifies the POST body shape against Sonarr.
func TestMissingEpisodesSearch(t *testing.T) {
	var captured atomic.Value // map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/command", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "method", 405)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		captured.Store(body)
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"id":42}`))
	})
	s, _, _ := newMissingEpisodesTestServer(t, mux)

	body := `{"instanceId":"s1","episodeIds":[100,101,102]}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/scan/missing-episodes/search", strings.NewReader(body))
	s.handleMissingEpisodesSearch(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	got, ok := captured.Load().(map[string]any)
	if !ok {
		t.Fatal("never captured request body")
	}
	if got["name"] != "EpisodeSearch" {
		t.Errorf("command name: got %v want EpisodeSearch", got["name"])
	}
	ids, ok := got["episodeIds"].([]any)
	if !ok || len(ids) != 3 {
		t.Errorf("episodeIds shape: %v", got["episodeIds"])
	}
}

func TestMissingEpisodesSearch_RejectsEmpty(t *testing.T) {
	mux := http.NewServeMux()
	s, _, _ := newMissingEpisodesTestServer(t, mux)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/scan/missing-episodes/search",
		strings.NewReader(`{"instanceId":"s1","episodeIds":[]}`))
	s.handleMissingEpisodesSearch(rr, req)
	if rr.Code != 400 {
		t.Errorf("empty episodeIds should be 400, got %d", rr.Code)
	}
}

func TestMissingEpisodesSearch_RejectsOver500(t *testing.T) {
	mux := http.NewServeMux()
	s, _, _ := newMissingEpisodesTestServer(t, mux)
	ids := make([]int, 501)
	for i := range ids {
		ids[i] = i + 1
	}
	bodyBytes, _ := json.Marshal(map[string]any{"instanceId": "s1", "episodeIds": ids})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/scan/missing-episodes/search", bytes.NewReader(bodyBytes))
	s.handleMissingEpisodesSearch(rr, req)
	if rr.Code != 400 {
		t.Errorf("501 episodeIds should be 400, got %d", rr.Code)
	}
}

// TestMissingEpisodesTag drives the apply path: tag created via
// CreateTag when missing, then EditorApplyTags add for the supplied
// series IDs, plus EditorApplyTags remove when RemoveFromOthers is
// set and existing carriers fall outside the supplied set.
func TestMissingEpisodesTag(t *testing.T) {
	// Sonarr currently has the tag (id=7), carried by series 5 and 6.
	// After the call we want: series 1 + 2 newly tagged; series 5
	// remains tagged (it's in the desired set); series 6 has tag removed.
	var (
		createCalled atomic.Bool
		addBody      atomic.Value
		removeBody   atomic.Value
	)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/tag/detail", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"id":7,"label":"missing-episodes","seriesIds":[5,6]}]`))
	})
	mux.HandleFunc("/api/v3/tag", func(w http.ResponseWriter, r *http.Request) {
		createCalled.Store(true)
		_, _ = w.Write([]byte(`{"id":7,"label":"missing-episodes"}`))
	})
	mux.HandleFunc("/api/v3/series/editor", func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		switch b["applyTags"] {
		case "add":
			addBody.Store(b)
		case "remove":
			removeBody.Store(b)
		}
		w.WriteHeader(202)
	})

	s, _, _ := newMissingEpisodesTestServer(t, mux)
	body := `{"instanceId":"s1","tagName":"missing-episodes","seriesIds":[1,2,5],"removeFromOthers":true}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/scan/missing-episodes/tag", strings.NewReader(body))
	s.handleMissingEpisodesTag(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var resp missingEpisodesTagResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Applied != 2 {
		t.Errorf("Applied: got %d want 2", resp.Applied)
	}
	if resp.Removed != 1 {
		t.Errorf("Removed: got %d want 1", resp.Removed)
	}
	if createCalled.Load() {
		t.Errorf("CreateTag should NOT have been called (tag already exists)")
	}
	addB, _ := addBody.Load().(map[string]any)
	if addB == nil {
		t.Fatal("add editor body never captured")
	}
	addIDs, _ := addB["seriesIds"].([]any)
	if len(addIDs) != 2 {
		t.Errorf("add seriesIds len: %d, want 2 (1,2)", len(addIDs))
	}
	removeB, _ := removeBody.Load().(map[string]any)
	if removeB == nil {
		t.Fatal("remove editor body never captured")
	}
	removeIDs, _ := removeB["seriesIds"].([]any)
	if len(removeIDs) != 1 {
		t.Errorf("remove seriesIds len: %d, want 1 (6)", len(removeIDs))
	}
	if removeIDs[0].(float64) != 6 {
		t.Errorf("remove series id: got %v want 6", removeIDs[0])
	}
}

// TestMissingEpisodesTag_CreatesWhenMissing exercises the path where
// Sonarr doesn't carry the tag yet — CreateTag should fire before the
// editor call.
func TestMissingEpisodesTag_CreatesWhenMissing(t *testing.T) {
	var createCalled atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/tag/detail", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	})
	mux.HandleFunc("/api/v3/tag", func(w http.ResponseWriter, r *http.Request) {
		createCalled.Store(true)
		_, _ = w.Write([]byte(`{"id":99,"label":"missing-episodes"}`))
	})
	mux.HandleFunc("/api/v3/series/editor", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(202)
	})

	s, _, _ := newMissingEpisodesTestServer(t, mux)
	body := `{"instanceId":"s1","seriesIds":[1,2]}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/scan/missing-episodes/tag", strings.NewReader(body))
	s.handleMissingEpisodesTag(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if !createCalled.Load() {
		t.Error("CreateTag should have been called (tag did not exist)")
	}
}

func TestMissingEpisodesTag_RejectsBadTagName(t *testing.T) {
	mux := http.NewServeMux()
	s, _, _ := newMissingEpisodesTestServer(t, mux)
	body := `{"instanceId":"s1","tagName":"Bad Name!!","seriesIds":[1]}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/scan/missing-episodes/tag", strings.NewReader(body))
	s.handleMissingEpisodesTag(rr, req)
	if rr.Code != 400 {
		t.Errorf("expected 400 for invalid tag name, got %d (body=%s)", rr.Code, rr.Body.String())
	}
}

// TestMissingEpisodesPreview_RejectsBothFiltersDisabled covers C1:
// when the caller unchecks both includeContinuing and includeEnded
// the scan would walk zero series and return a misleading "all
// complete" success. The handler must reject with 400 instead.
func TestMissingEpisodesPreview_RejectsBothFiltersDisabled(t *testing.T) {
	mux := http.NewServeMux()
	s, _, _ := newMissingEpisodesTestServer(t, mux)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/scan/missing-episodes/preview",
		strings.NewReader(`{"instanceId":"s1","threshold":0.7,"includeContinuing":false,"includeEnded":false}`))
	s.handleMissingEpisodesPreview(rr, req)
	if rr.Code != 400 {
		t.Errorf("both filters disabled should be 400, got %d (body=%s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "includeContinuing") {
		t.Errorf("error body should mention includeContinuing/includeEnded, got: %s", rr.Body.String())
	}
}

// TestMissingEpisodesPreview_ExcludesSpecialsByDefault covers B2:
// season 0 episodes are filtered out at the handler boundary unless
// includeSpecials is explicitly set. The series has one valid gap
// in S1 (E3 missing) AND a "missing" S0E2 special; only the S1 gap
// should be flagged.
func TestMissingEpisodesPreview_ExcludesSpecialsByDefault(t *testing.T) {
	seriesBody := `[{"id":1,"title":"Spec Show","status":"ended","monitored":true}]`
	// Season 0: 2 monitored specials, one missing. Season 1: 5 episodes,
	// E3 missing (80% coverage → above 70% threshold).
	epBody := `[
		{"id":50,"seriesId":1,"seasonNumber":0,"episodeNumber":1,"title":"Special 1",
		 "airDateUtc":"2024-01-01T00:00:00Z","monitored":true,"hasFile":true},
		{"id":51,"seriesId":1,"seasonNumber":0,"episodeNumber":2,"title":"Special 2",
		 "airDateUtc":"2024-02-01T00:00:00Z","monitored":true,"hasFile":false},
		{"id":100,"seriesId":1,"seasonNumber":1,"episodeNumber":1,"title":"E1",
		 "airDateUtc":"2024-01-01T00:00:00Z","monitored":true,"hasFile":true},
		{"id":101,"seriesId":1,"seasonNumber":1,"episodeNumber":2,"title":"E2",
		 "airDateUtc":"2024-01-08T00:00:00Z","monitored":true,"hasFile":true},
		{"id":102,"seriesId":1,"seasonNumber":1,"episodeNumber":3,"title":"E3",
		 "airDateUtc":"2024-01-15T00:00:00Z","monitored":true,"hasFile":false},
		{"id":103,"seriesId":1,"seasonNumber":1,"episodeNumber":4,"title":"E4",
		 "airDateUtc":"2024-01-22T00:00:00Z","monitored":true,"hasFile":true},
		{"id":104,"seriesId":1,"seasonNumber":1,"episodeNumber":5,"title":"E5",
		 "airDateUtc":"2024-01-29T00:00:00Z","monitored":true,"hasFile":true}
	]`
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/series", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(seriesBody))
	})
	mux.HandleFunc("/api/v3/episode", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(epBody))
	})
	s, _, _ := newMissingEpisodesTestServer(t, mux)

	// Default: specials excluded — only S1E3 should be flagged.
	body := `{"instanceId":"s1","threshold":0.7,"bufferHours":24,"includeContinuing":true,"includeEnded":true}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/scan/missing-episodes/preview", strings.NewReader(body))
	s.handleMissingEpisodesPreview(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var resp missingEpisodesPreviewResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.TotalMissingEpisodes != 1 {
		t.Errorf("default: TotalMissingEpisodes got %d want 1 (S1E3 only — specials excluded)", resp.TotalMissingEpisodes)
	}
	if len(resp.Series) != 1 || len(resp.Series[0].Seasons) != 1 {
		t.Fatalf("default: expected 1 series/1 season, got series=%d seasons=%d", len(resp.Series), len(resp.Series[0].Seasons))
	}
	if resp.Series[0].Seasons[0].SeasonNumber != 1 {
		t.Errorf("default: flagged season should be 1 (specials excluded), got %d", resp.Series[0].Seasons[0].SeasonNumber)
	}

	// Opt-in: include specials. Now season 0 (with the missing S0E2)
	// should also qualify — coverage 1/2 = 50% on its own is BELOW
	// 70% threshold, so use a lower threshold here to verify it
	// participates. Drop threshold to 0.4 so S0 qualifies too.
	body = `{"instanceId":"s1","threshold":0.4,"bufferHours":24,"includeContinuing":true,"includeEnded":true,"includeSpecials":true}`
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/scan/missing-episodes/preview", strings.NewReader(body))
	s.handleMissingEpisodesPreview(rr, req)
	if rr.Code != 200 {
		t.Fatalf("opt-in: status=%d body=%s", rr.Code, rr.Body.String())
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("opt-in decode: %v", err)
	}
	if resp.TotalMissingEpisodes != 2 {
		t.Errorf("opt-in: TotalMissingEpisodes got %d want 2 (S0E2 + S1E3)", resp.TotalMissingEpisodes)
	}
	if len(resp.Series) != 1 || len(resp.Series[0].Seasons) != 2 {
		t.Errorf("opt-in: expected 1 series/2 seasons, got series=%d seasons=%d", len(resp.Series), len(resp.Series[0].Seasons))
	}
}

// TestMissingEpisodesPreview_BufferHoursZero covers C2/C3: when the
// caller explicitly supplies bufferHours=0 we must honour it (= flag
// any aired episode immediately) rather than silently coercing to 24.
// The seed episode aired 1 minute ago — at bufferHours=0 it should be
// "aired and missing"; at bufferHours=24 (the previous silent coerce)
// it would be filtered as "not yet finished airing".
func TestMissingEpisodesPreview_BufferHoursZero(t *testing.T) {
	// 4-episode season: 3 on disk, 1 missing that aired one minute ago.
	// At bufferHours=0 → the missing episode counts as aired → flagged.
	// At bufferHours=24 → the missing episode is "still in flight" → season
	// is not finished → nothing flagged.
	// We can't pin "now" from outside the handler, so we date episodes
	// well in the past for the first three, and one minute in the past for the
	// missing one (relative to the handler's time.Now()).
	nowFixed := time.Now().UTC()
	oneMinAgo := nowFixed.Add(-1 * time.Minute).Format(time.RFC3339)
	threeDaysAgo := nowFixed.Add(-3 * 24 * time.Hour).Format(time.RFC3339)
	seriesBody := `[{"id":1,"title":"Buffer Show","status":"ended","monitored":true}]`
	epBody := `[
		{"id":1,"seriesId":1,"seasonNumber":1,"episodeNumber":1,"title":"E1","airDateUtc":"` + threeDaysAgo + `","monitored":true,"hasFile":true},
		{"id":2,"seriesId":1,"seasonNumber":1,"episodeNumber":2,"title":"E2","airDateUtc":"` + threeDaysAgo + `","monitored":true,"hasFile":true},
		{"id":3,"seriesId":1,"seasonNumber":1,"episodeNumber":3,"title":"E3","airDateUtc":"` + threeDaysAgo + `","monitored":true,"hasFile":true},
		{"id":4,"seriesId":1,"seasonNumber":1,"episodeNumber":4,"title":"E4 (just aired)","airDateUtc":"` + oneMinAgo + `","monitored":true,"hasFile":false}
	]`
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/series", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(seriesBody))
	})
	mux.HandleFunc("/api/v3/episode", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(epBody))
	})
	s, _, _ := newMissingEpisodesTestServer(t, mux)

	// bufferHours=0 explicitly → E4 counts as aired → flagged.
	body := `{"instanceId":"s1","threshold":0.5,"bufferHours":0,"includeContinuing":true,"includeEnded":true}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/scan/missing-episodes/preview", strings.NewReader(body))
	s.handleMissingEpisodesPreview(rr, req)
	if rr.Code != 200 {
		t.Fatalf("bufferHours=0: status=%d body=%s", rr.Code, rr.Body.String())
	}
	var resp missingEpisodesPreviewResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("bufferHours=0 decode: %v", err)
	}
	if resp.BufferHours != 0 {
		t.Errorf("bufferHours=0: response.BufferHours got %d want 0 (no silent coerce)", resp.BufferHours)
	}
	if resp.TotalMissingEpisodes != 1 {
		t.Errorf("bufferHours=0: TotalMissingEpisodes got %d want 1 (E4 should be flagged)", resp.TotalMissingEpisodes)
	}

	// Field absent from JSON → default 24 → E4 is "not yet aired" relative to
	// the buffer → season-not-finished → nothing flagged.
	body = `{"instanceId":"s1","threshold":0.5,"includeContinuing":true,"includeEnded":true}`
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/scan/missing-episodes/preview", strings.NewReader(body))
	s.handleMissingEpisodesPreview(rr, req)
	if rr.Code != 200 {
		t.Fatalf("default buffer: status=%d body=%s", rr.Code, rr.Body.String())
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("default buffer decode: %v", err)
	}
	if resp.BufferHours != 24 {
		t.Errorf("default buffer: response.BufferHours got %d want 24", resp.BufferHours)
	}
	if resp.TotalMissingEpisodes != 0 {
		t.Errorf("default buffer: TotalMissingEpisodes got %d want 0 (E4 still inside 24h window)", resp.TotalMissingEpisodes)
	}
}

// TestMissingEpisodesPreview_RejectsBufferHoursOver672 covers the new
// upper-bound check after we widened max from 168 to 672.
func TestMissingEpisodesPreview_RejectsBufferHoursOver672(t *testing.T) {
	mux := http.NewServeMux()
	s, _, _ := newMissingEpisodesTestServer(t, mux)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/scan/missing-episodes/preview",
		strings.NewReader(`{"instanceId":"s1","bufferHours":673,"includeContinuing":true,"includeEnded":true}`))
	s.handleMissingEpisodesPreview(rr, req)
	if rr.Code != 400 {
		t.Errorf("bufferHours=673 should be 400, got %d (body=%s)", rr.Code, rr.Body.String())
	}
}

// TestMissingEpisodesTag_RaceOn409Create covers B3: two concurrent
// callers both see the tag missing in their respective tag-detail
// snapshots → both POST CreateTag → second gets HTTP 409. The handler
// must re-fetch and proceed with the tag the winner created, returning
// 200 (not 502) to the loser.
func TestMissingEpisodesTag_RaceOn409Create(t *testing.T) {
	var (
		mu          sync.Mutex
		tagExists   bool // toggled true on first successful CreateTag
		createCount int32
	)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/tag/detail", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		exists := tagExists
		mu.Unlock()
		if exists {
			// Winner created it — losers see it on their second-pass refetch.
			_, _ = w.Write([]byte(`[{"id":42,"label":"missing-episodes","seriesIds":[]}]`))
			return
		}
		_, _ = w.Write([]byte(`[]`))
	})
	mux.HandleFunc("/api/v3/tag", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&createCount, 1)
		mu.Lock()
		if tagExists {
			mu.Unlock()
			// Loser: Sonarr returns 409 because winner already created
			// the tag with the same label.
			http.Error(w, "tag already exists", 409)
			return
		}
		tagExists = true
		mu.Unlock()
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"id":42,"label":"missing-episodes"}`))
	})
	mux.HandleFunc("/api/v3/series/editor", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(202)
	})

	s, _, _ := newMissingEpisodesTestServer(t, mux)

	// Fire two callers in parallel — one wins CreateTag, the other 409s
	// and must recover via the re-fetch path.
	body := `{"instanceId":"s1","tagName":"missing-episodes","seriesIds":[1,2]}`
	var wg sync.WaitGroup
	results := make([]int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/scan/missing-episodes/tag", strings.NewReader(body))
			s.handleMissingEpisodesTag(rr, req)
			results[i] = rr.Code
		}(i)
	}
	wg.Wait()

	for i, code := range results {
		if code != 200 {
			t.Errorf("caller %d: status=%d want 200 (race-aware recovery)", i, code)
		}
	}
	if got := atomic.LoadInt32(&createCount); got != 2 {
		t.Errorf("createCount: got %d want 2 (both callers attempted)", got)
	}
}
