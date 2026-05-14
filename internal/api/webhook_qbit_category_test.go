package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"resolvarr/internal/arr"
	"resolvarr/internal/core"
)

// webhook_qbit_category_test.go — skip-path coverage + full happy-path
// integration for dispatchQbitCategoryFix. The dispatcher's three-
// layer verification (payload sanity / Arr history / qBit state) is
// exercised by per-case fakes wired into the same Server struct the
// production code uses.

// arrFake serves the two endpoints the category-fix dispatcher hits:
// /api/v3/downloadclient (for category resolution) +
// /api/v3/history?downloadId=... (for import-confirmation verification).
type arrFake struct {
	downloadClients     []arr.ArrDownloadClient
	historyByDownloadID map[string][]arr.HistoryRecord
	srv                 *httptest.Server
}

func newArrFake(t *testing.T, dc []arr.ArrDownloadClient, hist map[string][]arr.HistoryRecord) *arrFake {
	a := &arrFake{downloadClients: dc, historyByDownloadID: hist}
	a.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/v3/downloadclient"):
			_ = json.NewEncoder(w).Encode(a.downloadClients)
		case strings.HasPrefix(r.URL.Path, "/api/v3/history"):
			dl := r.URL.Query().Get("downloadId")
			records := a.historyByDownloadID[dl]
			_ = json.NewEncoder(w).Encode(map[string]any{"records": records})
		default:
			t.Errorf("unexpected Arr path %q", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(a.srv.Close)
	return a
}

// qbitFake serves the qBit endpoints used by the category fix flow:
// /api/v2/auth/login → "Ok.", /api/v2/torrents/info → list, and
// /api/v2/torrents/setCategory → success.
type qbitFake struct {
	torrentsByHash map[string]string // hash → category
	setCategoryHits int32             // exported via atomic
	lastCategorySet string
	srv             *httptest.Server
}

func newQbitFake(t *testing.T, torrents map[string]string) *qbitFake {
	q := &qbitFake{torrentsByHash: torrents}
	q.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			w.Write([]byte("Ok."))
		case "/api/v2/torrents/info":
			hash := r.URL.Query().Get("hashes")
			cat, ok := q.torrentsByHash[hash]
			if !ok {
				w.Write([]byte("[]"))
				return
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"hash": hash, "name": "any", "category": cat, "tags": "", "state": "uploading"},
			})
		case "/api/v2/torrents/setCategory":
			_ = r.ParseForm()
			q.lastCategorySet = r.Form.Get("category")
			atomic.AddInt32(&q.setCategoryHits, 1)
			w.WriteHeader(200)
		default:
			t.Logf("unexpected qBit path %q", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(q.srv.Close)
	return q
}

// makeCategoryFixServer wires a Server + ConfigStore with the rule, an
// Arr instance pointing at the arrFake, and a qBit instance pointing
// at the qbitFake. Returns the Server + the rule pointer for assertions.
func makeCategoryFixServer(t *testing.T, arrSrv *arrFake, qbitSrv *qbitFake, ruleMutator func(*core.WebhookRule)) (*Server, *core.WebhookRule) {
	dir := t.TempDir()
	store := core.NewConfigStore(dir)
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	rule := &core.WebhookRule{
		ID:         "r1",
		Name:       "test",
		Enabled:    true,
		InstanceID: "arr1",
		AppType:    "radarr",
		Functions:  []core.WebhookFunction{core.WebhookFnQbitCategoryFix},
		QbitCategoryFix: &core.QbitCategoryFixRules{
			QbitInstanceID:             "q1",
			ArrDownloadClientID:        1,
			PreImportCategorySnapshot:  "qbit-movies",
			PostImportCategorySnapshot: "qbit-movies-imp",
		},
	}
	if ruleMutator != nil {
		ruleMutator(rule)
	}
	if err := store.Update(func(c *core.Config) {
		c.Instances = []core.Instance{
			{ID: "arr1", Name: "Radarr", Type: "radarr", URL: arrSrv.srv.URL, APIKey: "key"},
		}
		c.QbitInstances = []core.QbitInstance{
			{ID: "q1", Name: "qBit", URL: qbitSrv.srv.URL, Username: "", Password: ""},
		}
		c.WebhookRules = []core.WebhookRule{*rule}
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	s := &Server{App: &core.App{Config: store, HTTPClient: http.DefaultClient}}
	return s, rule
}

func TestDispatchQbitCategoryFix_HappyPath(t *testing.T) {
	dc := []arr.ArrDownloadClient{
		{ID: 1, Name: "qbt-main", Implementation: "QBittorrent",
			Fields: []arr.ArrDownloadClientField{
				{Name: "movieCategory", Value: "qbit-movies"},
				{Name: "movieImportedCategory", Value: "qbit-movies-imp"},
			}},
	}
	hist := map[string][]arr.HistoryRecord{
		"DEADBEEF": {{EventType: "downloadFolderImported"}},
	}
	arrSrv := newArrFake(t, dc, hist)
	qbitSrv := newQbitFake(t, map[string]string{"DEADBEEF": "qbit-movies"}) // stuck on pre-import

	s, rule := makeCategoryFixServer(t, arrSrv, qbitSrv, nil)
	cfg := s.App.Config.Get()
	env := &connectEventEnvelope{EventType: "Download"}
	body := []byte(`{"isUpgrade":false,"downloadId":"DEADBEEF","movie":{"id":42,"title":"X"},"movieFile":{"id":1,"relativePath":"x.mkv","mediaInfo":{}}}`)

	res := s.dispatchQbitCategoryFix(context.Background(), rule, cfg, env, body)
	if !res.OK {
		t.Fatalf("dispatch failed: %+v", res)
	}
	if !strings.Contains(res.Summary, "changed category") {
		t.Errorf("summary = %q, want 'changed category ...'", res.Summary)
	}
	if atomic.LoadInt32(&qbitSrv.setCategoryHits) != 1 {
		t.Errorf("setCategory hits = %d, want 1", qbitSrv.setCategoryHits)
	}
	if qbitSrv.lastCategorySet != "qbit-movies-imp" {
		t.Errorf("category set = %q, want qbit-movies-imp", qbitSrv.lastCategorySet)
	}
}

func TestDispatchQbitCategoryFix_SkipNotDownloadEvent(t *testing.T) {
	arrSrv := newArrFake(t, nil, nil)
	qbitSrv := newQbitFake(t, nil)
	s, rule := makeCategoryFixServer(t, arrSrv, qbitSrv, nil)
	cfg := s.App.Config.Get()
	env := &connectEventEnvelope{EventType: "Grab"}
	res := s.dispatchQbitCategoryFix(context.Background(), rule, cfg, env, []byte("{}"))
	if !res.OK || !strings.Contains(res.Summary, "not a Download event") {
		t.Errorf("want OK skip not-download, got %+v", res)
	}
}

func TestDispatchQbitCategoryFix_SkipNoMovieFile(t *testing.T) {
	arrSrv := newArrFake(t, nil, nil)
	qbitSrv := newQbitFake(t, nil)
	s, rule := makeCategoryFixServer(t, arrSrv, qbitSrv, nil)
	cfg := s.App.Config.Get()
	env := &connectEventEnvelope{EventType: "Download"}
	body := []byte(`{"downloadId":"DEADBEEF","movie":{"id":42,"title":"X"}}`)
	res := s.dispatchQbitCategoryFix(context.Background(), rule, cfg, env, body)
	if !res.OK || !strings.Contains(res.Summary, "not an import") {
		t.Errorf("want OK skip no-moviefile, got %+v", res)
	}
}

func TestDispatchQbitCategoryFix_SkipNoDownloadID(t *testing.T) {
	arrSrv := newArrFake(t, nil, nil)
	qbitSrv := newQbitFake(t, nil)
	s, rule := makeCategoryFixServer(t, arrSrv, qbitSrv, nil)
	cfg := s.App.Config.Get()
	env := &connectEventEnvelope{EventType: "Download"}
	body := []byte(`{"downloadId":"","movie":{"id":42,"title":"X"},"movieFile":{"id":1,"relativePath":"x.mkv"}}`)
	res := s.dispatchQbitCategoryFix(context.Background(), rule, cfg, env, body)
	if !res.OK || !strings.Contains(res.Summary, "no downloadId") {
		t.Errorf("want OK skip no-downloadId, got %+v", res)
	}
}

func TestDispatchQbitCategoryFix_SkipNoImportConfirmation(t *testing.T) {
	dc := []arr.ArrDownloadClient{
		{ID: 1, Name: "qbt-main", Implementation: "QBittorrent",
			Fields: []arr.ArrDownloadClientField{
				{Name: "movieCategory", Value: "qbit-movies"},
				{Name: "movieImportedCategory", Value: "qbit-movies-imp"},
			}},
	}
	// History contains only a "grabbed" event — no import-confirmation.
	hist := map[string][]arr.HistoryRecord{
		"DEADBEEF": {{EventType: "grabbed"}},
	}
	arrSrv := newArrFake(t, dc, hist)
	qbitSrv := newQbitFake(t, map[string]string{"DEADBEEF": "qbit-movies"})

	s, rule := makeCategoryFixServer(t, arrSrv, qbitSrv, nil)
	cfg := s.App.Config.Get()
	env := &connectEventEnvelope{EventType: "Download"}
	body := []byte(`{"downloadId":"DEADBEEF","movie":{"id":42,"title":"X"},"movieFile":{"id":1,"relativePath":"x.mkv"}}`)

	res := s.dispatchQbitCategoryFix(context.Background(), rule, cfg, env, body)
	if !res.OK || !strings.Contains(res.Summary, "Arr history-walk didn't confirm") {
		t.Errorf("want OK skip no-import-confirmation, got %+v", res)
	}
	if atomic.LoadInt32(&qbitSrv.setCategoryHits) != 0 {
		t.Errorf("qBit was touched despite missing import-confirmation")
	}
}

func TestDispatchQbitCategoryFix_SkipTorrentGone(t *testing.T) {
	dc := []arr.ArrDownloadClient{
		{ID: 1, Name: "qbt-main", Implementation: "QBittorrent",
			Fields: []arr.ArrDownloadClientField{
				{Name: "movieCategory", Value: "qbit-movies"},
				{Name: "movieImportedCategory", Value: "qbit-movies-imp"},
			}},
	}
	hist := map[string][]arr.HistoryRecord{
		"DEADBEEF": {{EventType: "downloadFolderImported"}},
	}
	arrSrv := newArrFake(t, dc, hist)
	// qBit doesn't have the torrent.
	qbitSrv := newQbitFake(t, map[string]string{})

	s, rule := makeCategoryFixServer(t, arrSrv, qbitSrv, nil)
	cfg := s.App.Config.Get()
	env := &connectEventEnvelope{EventType: "Download"}
	body := []byte(`{"downloadId":"DEADBEEF","movie":{"id":42,"title":"X"},"movieFile":{"id":1,"relativePath":"x.mkv"}}`)

	res := s.dispatchQbitCategoryFix(context.Background(), rule, cfg, env, body)
	if !res.OK || !strings.Contains(res.Summary, "removed from qBit") {
		t.Errorf("want OK skip torrent-gone, got %+v", res)
	}
}

func TestDispatchQbitCategoryFix_SkipAlreadyPostCategory(t *testing.T) {
	dc := []arr.ArrDownloadClient{
		{ID: 1, Name: "qbt-main", Implementation: "QBittorrent",
			Fields: []arr.ArrDownloadClientField{
				{Name: "movieCategory", Value: "qbit-movies"},
				{Name: "movieImportedCategory", Value: "qbit-movies-imp"},
			}},
	}
	hist := map[string][]arr.HistoryRecord{
		"DEADBEEF": {{EventType: "downloadFolderImported"}},
	}
	arrSrv := newArrFake(t, dc, hist)
	// Category already post-import — Arr did its job.
	qbitSrv := newQbitFake(t, map[string]string{"DEADBEEF": "qbit-movies-imp"})

	s, rule := makeCategoryFixServer(t, arrSrv, qbitSrv, nil)
	cfg := s.App.Config.Get()
	env := &connectEventEnvelope{EventType: "Download"}
	body := []byte(`{"downloadId":"DEADBEEF","movie":{"id":42,"title":"X"},"movieFile":{"id":1,"relativePath":"x.mkv"}}`)

	res := s.dispatchQbitCategoryFix(context.Background(), rule, cfg, env, body)
	if !res.OK || !strings.Contains(res.Summary, "Arr completed the swap") {
		t.Errorf("want OK skip already-correct, got %+v", res)
	}
	if atomic.LoadInt32(&qbitSrv.setCategoryHits) != 0 {
		t.Errorf("qBit was touched despite category already correct")
	}
}

func TestDispatchQbitCategoryFix_SkipUserSetCategory(t *testing.T) {
	dc := []arr.ArrDownloadClient{
		{ID: 1, Name: "qbt-main", Implementation: "QBittorrent",
			Fields: []arr.ArrDownloadClientField{
				{Name: "movieCategory", Value: "qbit-movies"},
				{Name: "movieImportedCategory", Value: "qbit-movies-imp"},
			}},
	}
	hist := map[string][]arr.HistoryRecord{
		"DEADBEEF": {{EventType: "downloadFolderImported"}},
	}
	arrSrv := newArrFake(t, dc, hist)
	// User manually set a different category.
	qbitSrv := newQbitFake(t, map[string]string{"DEADBEEF": "user-custom"})

	s, rule := makeCategoryFixServer(t, arrSrv, qbitSrv, nil)
	cfg := s.App.Config.Get()
	env := &connectEventEnvelope{EventType: "Download"}
	body := []byte(`{"downloadId":"DEADBEEF","movie":{"id":42,"title":"X"},"movieFile":{"id":1,"relativePath":"x.mkv"}}`)

	res := s.dispatchQbitCategoryFix(context.Background(), rule, cfg, env, body)
	if !res.OK || !strings.Contains(res.Summary, "leaving user-set value alone") {
		t.Errorf("want OK skip user-set, got %+v", res)
	}
	if atomic.LoadInt32(&qbitSrv.setCategoryHits) != 0 {
		t.Errorf("qBit was touched despite user-set category")
	}
}

func TestDispatchQbitCategoryFix_FallbackToSnapshot(t *testing.T) {
	// Arr download-client lookup misses (empty list returned), so the
	// adapter falls back to the rule's snapshot fields. Verify the fix
	// still applies.
	arrSrv := newArrFake(t, nil, map[string][]arr.HistoryRecord{
		"DEADBEEF": {{EventType: "downloadFolderImported"}},
	})
	qbitSrv := newQbitFake(t, map[string]string{"DEADBEEF": "qbit-movies"})

	s, rule := makeCategoryFixServer(t, arrSrv, qbitSrv, nil)
	cfg := s.App.Config.Get()
	env := &connectEventEnvelope{EventType: "Download"}
	body := []byte(`{"downloadId":"DEADBEEF","movie":{"id":42,"title":"X"},"movieFile":{"id":1,"relativePath":"x.mkv"}}`)

	res := s.dispatchQbitCategoryFix(context.Background(), rule, cfg, env, body)
	if !res.OK {
		t.Fatalf("snapshot fallback failed: %+v", res)
	}
	if qbitSrv.lastCategorySet != "qbit-movies-imp" {
		t.Errorf("snapshot-fallback applied wrong category: %q", qbitSrv.lastCategorySet)
	}
}

func TestDispatchQbitCategoryFix_NilCriteria(t *testing.T) {
	arrSrv := newArrFake(t, nil, nil)
	qbitSrv := newQbitFake(t, nil)
	s, rule := makeCategoryFixServer(t, arrSrv, qbitSrv, func(r *core.WebhookRule) {
		r.QbitCategoryFix = nil
	})
	cfg := s.App.Config.Get()
	env := &connectEventEnvelope{EventType: "Download"}
	res := s.dispatchQbitCategoryFix(context.Background(), rule, cfg, env, []byte(`{}`))
	if res.OK {
		t.Errorf("want OK=false on nil criteria, got %+v", res)
	}
}

func TestDispatchQbitCategoryFix_GhostQbitInstance(t *testing.T) {
	arrSrv := newArrFake(t, nil, map[string][]arr.HistoryRecord{
		"DEADBEEF": {{EventType: "downloadFolderImported"}},
	})
	qbitSrv := newQbitFake(t, nil)
	s, rule := makeCategoryFixServer(t, arrSrv, qbitSrv, func(r *core.WebhookRule) {
		r.QbitCategoryFix.QbitInstanceID = "ghost"
	})
	cfg := s.App.Config.Get()
	env := &connectEventEnvelope{EventType: "Download"}
	body := []byte(`{"downloadId":"DEADBEEF","movie":{"id":42,"title":"X"},"movieFile":{"id":1,"relativePath":"x.mkv"}}`)
	res := s.dispatchQbitCategoryFix(context.Background(), rule, cfg, env, body)
	if res.OK || !strings.Contains(res.Summary, "qbit instance \"ghost\" not found") {
		t.Errorf("want error on missing qbit, got %+v", res)
	}
}

// Validator tests — exercise the qbitCategoryFix branch of
// webhookRuleRequest.validate.

func TestValidateWebhookRule_QbitCategoryFix(t *testing.T) {
	dir := t.TempDir()
	store := core.NewConfigStore(dir)
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := store.Update(func(c *core.Config) {
		c.Instances = []core.Instance{{ID: "arr1", Name: "Radarr", Type: "radarr", URL: "http://x", APIKey: "k"}}
		c.QbitInstances = []core.QbitInstance{{ID: "q1", Name: "qBit", URL: "http://q"}}
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cfg := store.Get()

	mk := func(mut func(*webhookRuleRequest)) *webhookRuleRequest {
		req := &webhookRuleRequest{
			Name: "test", Enabled: true, InstanceID: "arr1", AppType: "radarr",
			Functions: []core.WebhookFunction{core.WebhookFnQbitCategoryFix},
			QbitCategoryFix: &core.QbitCategoryFixRules{
				QbitInstanceID: "q1", ArrDownloadClientID: 1,
				PreImportCategorySnapshot:  "qbit-movies",
				PostImportCategorySnapshot: "qbit-movies-imp",
			},
		}
		if mut != nil {
			mut(req)
		}
		return req
	}

	cases := []struct {
		name     string
		mut      func(*webhookRuleRequest)
		wantSub  string
	}{
		{"happy", nil, ""},
		{"missing struct", func(r *webhookRuleRequest) { r.QbitCategoryFix = nil }, "qbitCategoryFix criteria required"},
		{"missing qbit id", func(r *webhookRuleRequest) { r.QbitCategoryFix.QbitInstanceID = "" }, "qbitInstanceId is required"},
		{"ghost qbit id", func(r *webhookRuleRequest) { r.QbitCategoryFix.QbitInstanceID = "ghost" }, "qbitInstanceId not found"},
		{"zero dl id", func(r *webhookRuleRequest) { r.QbitCategoryFix.ArrDownloadClientID = 0 }, "must be a positive integer"},
		{"empty pre", func(r *webhookRuleRequest) { r.QbitCategoryFix.PreImportCategorySnapshot = "" }, "Sonarr/Radarr's download-client config doesn't have both"},
		{"equal cats", func(r *webhookRuleRequest) {
			r.QbitCategoryFix.PreImportCategorySnapshot = "same"
			r.QbitCategoryFix.PostImportCategorySnapshot = "same"
		}, "must differ"},
		// The regex permits spaces / slashes / dots / unicode in qBit category
		// names (qBit accepts these on disk). The only rejected classes are
		// ASCII control chars and `\` (Windows path separator).
		{"invalid pre chars (backslash)", func(r *webhookRuleRequest) { r.QbitCategoryFix.PreImportCategorySnapshot = "bad\\back" }, "forbidden characters"},
		{"invalid pre chars (control char)", func(r *webhookRuleRequest) { r.QbitCategoryFix.PreImportCategorySnapshot = "bad\x01ctrl" }, "forbidden characters"},
		// Permissive cases that USED to be rejected by the strict regex — make
		// sure the validator accepts them now so autoFillQbitCategories doesn't
		// surface real-world Arr configs and then trip the save flow.
		{"permissive — space", func(r *webhookRuleRequest) {
			r.QbitCategoryFix.PreImportCategorySnapshot = "qbit movies"
			r.QbitCategoryFix.PostImportCategorySnapshot = "qbit movies imp"
		}, ""},
		{"permissive — slash", func(r *webhookRuleRequest) {
			r.QbitCategoryFix.PreImportCategorySnapshot = "sonarr/active"
			r.QbitCategoryFix.PostImportCategorySnapshot = "sonarr/imported"
		}, ""},
		{"permissive — dot", func(r *webhookRuleRequest) {
			r.QbitCategoryFix.PreImportCategorySnapshot = "qbit.movies"
			r.QbitCategoryFix.PostImportCategorySnapshot = "qbit.movies.imp"
		}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := mk(tc.mut)
			err := req.validate(cfg)
			if tc.wantSub == "" {
				if err != nil {
					t.Errorf("happy case rejected: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("want error containing %q, got nil", tc.wantSub)
			}
			if !strings.Contains(err.Message, tc.wantSub) {
				t.Errorf("got error %q, want substring %q", err.Message, tc.wantSub)
			}
		})
	}
}
