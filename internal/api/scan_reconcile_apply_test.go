package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"resolvarr/internal/core"
	"resolvarr/internal/core/engine"
)

// End-to-end tests for runReconcile's apply pass against a fake Sonarr
// and a fake qBittorrent. They pin the two safety properties the
// automatic (webhook) path depends on: a season pack is only moved when
// EVERY episode in it is redundant, and a torrent already sitting in the
// target category is not "moved" again.

const (
	hashPack    = "pppppppppppppppppppppppppppppppppppppppp" // ep 101 redundant, ep 102 needed
	hashSingle  = "ssssssssssssssssssssssssssssssssssssssss" // ep 103 redundant
	hashAlready = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" // ep 104 redundant, already moved
)

// fakeSonarrForReconcile serves the handful of endpoints runReconcile
// reads for series 7. Everything else answers an empty list — the
// history / download-client reads are best-effort. With
// withImportEval, /manualimport reports the score each queued file
// would import at (50, same as its grab score); without it the
// evaluation is unavailable.
func fakeSonarrForReconcile(t *testing.T, withImportEval bool) *httptest.Server {
	t.Helper()
	return fakeSonarrForReconcileOpts(t, withImportEval, true)
}

// fakeSonarrForReconcileOpts adds withFileScores=false: /episodefile
// fails, so the score of the file already on disk is unknown.
func fakeSonarrForReconcileOpts(t *testing.T, withImportEval, withFileScores bool) *httptest.Server {
	t.Helper()
	eps := map[string][]int{hashPack: {101, 102}, hashSingle: {103}, hashAlready: {104}}
	// Without file scores the stuck releases score below 0, so they'd
	// only look redundant because the unread on-disk score counts as 0.
	stuckScore := 50
	if !withFileScores {
		stuckScore = -10
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v3/queue":
			row := func(hash string, ep int) map[string]any {
				return map[string]any{
					"downloadId": hash, "title": "Show.S01." + hash[:1], "status": "completed",
					"trackedDownloadState": "importBlocked", "customFormatScore": stuckScore,
					"seriesId": 7, "episodeId": ep,
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"records": []any{
				row(hashPack, 101), row(hashPack, 102),
				row(hashSingle, 103),
				row(hashAlready, 104),
			}})
		case "/api/v3/episode":
			// 102 has no file — the pack is the only copy of it.
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": 101, "episodeFileId": 1001},
				{"id": 102, "episodeFileId": 0},
				{"id": 103, "episodeFileId": 1003},
				{"id": 104, "episodeFileId": 1004},
			})
		case "/api/v3/episodefile":
			if !withFileScores {
				http.Error(w, "boom", http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": 1001, "seriesId": 7, "customFormatScore": 100},
				{"id": 1003, "seriesId": 7, "customFormatScore": 100},
				{"id": 1004, "seriesId": 7, "customFormatScore": 100},
			})
		case "/api/v3/manualimport":
			ids := eps[r.URL.Query().Get("downloadId")]
			if !withImportEval || len(ids) == 0 {
				_, _ = w.Write([]byte("[]"))
				return
			}
			var items []map[string]any
			for _, id := range ids {
				items = append(items, map[string]any{
					"customFormatScore": stuckScore,
					"episodes":          []map[string]any{{"id": id}},
				})
			}
			_ = json.NewEncoder(w).Encode(items)
		default:
			_, _ = w.Write([]byte("[]"))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// fakeQbitWithCategories holds hash → current category and records
// every setCategory call.
type fakeQbitWithCategories struct {
	mu   sync.Mutex
	cats map[string]string
	sets []string // "hash=category"
	srv  *httptest.Server
}

func newFakeQbitWithCategories(t *testing.T, cats map[string]string) *fakeQbitWithCategories {
	t.Helper()
	q := &fakeQbitWithCategories{cats: cats}
	q.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q.mu.Lock()
		defer q.mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/auth/login"):
			_, _ = w.Write([]byte("Ok."))
		case strings.HasSuffix(r.URL.Path, "/torrents/info"):
			h := strings.ToLower(r.URL.Query().Get("hashes"))
			cat, ok := q.cats[h]
			if !ok {
				_, _ = w.Write([]byte("[]"))
				return
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{{"hash": h, "name": "x", "category": cat}})
		case strings.HasSuffix(r.URL.Path, "/torrents/setCategory"):
			_ = r.ParseForm()
			h, c := r.PostForm.Get("hashes"), r.PostForm.Get("category")
			q.sets = append(q.sets, h+"="+c)
			q.cats[strings.ToLower(h)] = c
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(q.srv.Close)
	return q
}

func runReconcileApplyForTest(t *testing.T, applyItems []string) (*scanResponse, *fakeQbitWithCategories) {
	t.Helper()
	return runReconcileApplyWith(t, applyItems, true)
}

func runReconcileApplyWith(t *testing.T, applyItems []string, withImportEval bool) (*scanResponse, *fakeQbitWithCategories) {
	t.Helper()
	return runReconcileApplyOpts(t, applyItems, fakeSonarrForReconcile(t, withImportEval))
}

func runReconcileApplyOpts(t *testing.T, applyItems []string, sonarr *httptest.Server) (*scanResponse, *fakeQbitWithCategories) {
	t.Helper()
	q := newFakeQbitWithCategories(t, map[string]string{
		hashPack:    "tv",
		hashSingle:  "tv",
		hashAlready: "cleanup",
	})
	inst := &core.Instance{ID: "s1", Name: "Sonarr", Type: "sonarr", URL: sonarr.URL, APIKey: "k"}
	cfg := core.Config{
		Instances:     []core.Instance{*inst},
		QbitInstances: []core.QbitInstance{{ID: "q1", Name: "qBit", URL: q.srv.URL}},
	}
	s := &Server{App: &core.App{HTTPClient: http.DefaultClient}}
	resp, apiErr := s.runReconcile(context.Background(), cfg, inst, "sonarr", scanRunRequest{
		InstanceID:          "s1",
		Action:              "reconcile",
		Mode:                "apply",
		ReconcileTargets:    []scanReconcileTarget{{QbitInstanceID: "q1", PostCategory: "cleanup"}},
		ReconcileApplyItems: applyItems,
	})
	if apiErr != nil {
		t.Fatalf("runReconcile: %v", apiErr.Message)
	}
	return resp, q
}

func TestRunReconcileApply_LeavesPartlyNeededPackAlone(t *testing.T) {
	resp, q := runReconcileApplyForTest(t, nil)

	// Only the single redundant download moves. The pack is redundant
	// for episode 101 but is the only copy of 102 — moving it to the
	// cleanup category would let the user's cleanup delete 102.
	if len(q.sets) != 1 || q.sets[0] != hashSingle+"=cleanup" {
		t.Fatalf("setCategory calls = %v, want only %s=cleanup", q.sets, hashSingle)
	}
	for _, row := range resp.Reconcile {
		if row.DownloadID == hashPack && row.Status == "recategorised" {
			t.Errorf("pack row for episode %d reported recategorised", row.EpisodeID)
		}
	}
	// Per-row verdicts are still reported honestly.
	var packVerdicts []string
	for _, row := range resp.Reconcile {
		if row.DownloadID == hashPack {
			packVerdicts = append(packVerdicts, row.Status)
		}
	}
	want := []string{string(engine.StuckRedundant), string(engine.StuckNeedsAttention)}
	if strings.Join(packVerdicts, ",") != strings.Join(want, ",") {
		t.Errorf("pack verdicts = %v, want %v", packVerdicts, want)
	}
}

func TestRunReconcileApply_AlreadyInCategoryIsNotMovedAgain(t *testing.T) {
	resp, q := runReconcileApplyForTest(t, nil)

	for _, s := range q.sets {
		if strings.HasPrefix(s, hashAlready) {
			t.Errorf("torrent already in the target category was re-set: %s", s)
		}
	}
	if resp.Totals.ReconcileRecategorised != 1 {
		t.Errorf("ReconcileRecategorised = %d, want 1 (the already-moved one must not count)", resp.Totals.ReconcileRecategorised)
	}
	d := summariseStuckReconcile(resp)
	if d.Stuck != 3 || d.Moved != 1 || d.AlreadyMoved != 1 || d.NeedsAttention != 1 || d.Failed != 0 {
		t.Errorf("summary = %+v, want 3 stuck downloads: 1 moved, 1 already moved, 1 needs attention", d)
	}
	if len(d.Moves) != 1 || d.Moves[0].Category != "cleanup" {
		t.Errorf("moves = %+v, want the one real move", d.Moves)
	}
}

func TestRunReconcileApply_ExplicitSelectionIsHonoured(t *testing.T) {
	// The user ticked the pack on the Stuck downloads page — their call.
	_, q := runReconcileApplyForTest(t, []string{hashPack})
	if len(q.sets) != 1 || q.sets[0] != hashPack+"=cleanup" {
		t.Fatalf("setCategory calls = %v, want only the selected pack", q.sets)
	}
}

func TestJoinLinesWithinField_StaysUnderDiscordLimit(t *testing.T) {
	long := strings.Repeat("Some.Very.Long.Release.Title.2160p.WEB-DL.DDP5.1.Atmos.DV.HDR10.H.265-GROUP ", 2) // ~150 chars
	lines := make([]string, 10)
	for i := range lines {
		lines[i] = long + "— qBit → cleanup"
	}
	got := joinLinesWithinField(lines)
	if len(got) > 1024 {
		t.Fatalf("field is %d chars, over Discord's 1024 limit", len(got))
	}
	if !strings.Contains(got, "more") {
		t.Errorf("dropped lines without saying so: %q", got)
	}
	short := []string{"a", "b"}
	if got := joinLinesWithinField(short); got != "a\nb" {
		t.Errorf("short list = %q, want unchanged", got)
	}
	if got := joinLinesWithinField([]string{strings.Repeat("x", 3000)}); len(got) > 1024 {
		t.Errorf("single long line is %d chars, over the limit", len(got))
	}
}

func TestStuckReconcileNotification_SweepFailureIsReported(t *testing.T) {
	d := StuckReconcileDetail{Scope: "Show", SweepError: "arr list queue: connection refused"}
	fields := appendStuckReconcileSection(nil, &d)
	if len(fields) != 1 || !strings.Contains(fields[0].Value, "connection refused") {
		t.Fatalf("sweep failure fields = %+v, want one field carrying the error", fields)
	}
	title := composeTitle(core.WebhookEventDownload, []functionResult{{
		Function: core.WebhookFnStuckReconcile, Changed: true, Detail: d,
	}}, "Show", "", nil)
	if !strings.Contains(title, "failed") {
		t.Errorf("title %q reads as a success for a failed sweep", title)
	}
}

func TestRunReconcileApply_UnknownImportScoreIsNotMovedAutomatically(t *testing.T) {
	// Without Sonarr's import evaluation the row is judged on its grab
	// score, which can understate what the file would import at. Show
	// the verdict, but don't act on it without the user.
	resp, q := runReconcileApplyWith(t, nil, false)
	if len(q.sets) != 0 {
		t.Fatalf("setCategory calls = %v, want none", q.sets)
	}
	var single string
	for _, row := range resp.Reconcile {
		if row.DownloadID == hashSingle {
			single = row.Status
		}
	}
	if single != string(engine.StuckRedundant) {
		t.Errorf("verdict = %q, want it still reported as redundant", single)
	}
	// The user ticking it is their call.
	_, q = runReconcileApplyWith(t, []string{hashSingle}, false)
	if len(q.sets) != 1 {
		t.Errorf("explicit selection: setCategory calls = %v, want one", q.sets)
	}
}

func TestReconcileClients_TorrentInTwoClientsIsNotGuessed(t *testing.T) {
	const hashA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	qa := newQbitFanoutFake(t, hashA)
	qb := newQbitFanoutFake(t, hashA) // cross-seeded copy
	cfg := core.Config{QbitInstances: []core.QbitInstance{
		{ID: "q1", Name: "movies", URL: qa.srv.URL},
		{ID: "q2", Name: "seedbox", URL: qb.srv.URL},
	}}
	rc, apiErr := newReconcileClients(cfg, []scanReconcileTarget{
		{QbitInstanceID: "q1", PostCategory: "a"},
		{QbitInstanceID: "q2", PostCategory: "b"},
	})
	if apiErr != nil {
		t.Fatalf("newReconcileClients: %v", apiErr.Message)
	}
	_, _, ok, err := rc.clientFor(context.Background(), hashA)
	if ok || err == nil || !strings.Contains(err.Error(), "several") {
		t.Fatalf("hash in two clients = (found %v, err %v), want an ambiguity error", ok, err)
	}
}

func TestNewReconcileClients_RejectsClientListedTwice(t *testing.T) {
	cfg := core.Config{QbitInstances: []core.QbitInstance{{ID: "q1", Name: "movies", URL: "http://127.0.0.1:1"}}}
	targets := reconcileTargetsFromRequest(scanRunRequest{ReconcileTargets: []scanReconcileTarget{
		{QbitInstanceID: "q1", PostCategory: "a"},
		{QbitInstanceID: "q1", PostCategory: "b"},
	}})
	if _, err := newReconcileClients(cfg, targets); err == nil {
		t.Error("one client with two categories accepted, want rejection")
	}
}

// newStuckSweepServer wires a Server whose config holds the fake Sonarr,
// the fake qBit and one stuck-downloads rule with a 1s delay.
func newStuckSweepServer(t *testing.T, ruleMut func(*core.WebhookRule)) (*Server, *fakeQbitWithCategories, core.WebhookRule) {
	t.Helper()
	sonarr := fakeSonarrForReconcile(t, true)
	q := newFakeQbitWithCategories(t, map[string]string{hashPack: "tv", hashSingle: "tv", hashAlready: "cleanup"})
	store := core.NewConfigStore(t.TempDir())
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	rule := core.WebhookRule{
		ID: "r1", Name: "stuck", Enabled: true, InstanceID: "s1", AppType: "sonarr",
		Functions: []core.WebhookFunction{core.WebhookFnStuckReconcile},
		StuckReconcile: &core.StuckReconcileRules{
			Targets:      []core.StuckReconcileTarget{{QbitInstanceID: "q1", PostCategory: "cleanup"}},
			DeferSeconds: 1,
		},
	}
	if ruleMut != nil {
		ruleMut(&rule)
	}
	if err := store.Update(func(c *core.Config) {
		c.Instances = []core.Instance{{ID: "s1", Name: "Sonarr", Type: "sonarr", URL: sonarr.URL, APIKey: "k"}}
		c.QbitInstances = []core.QbitInstance{{ID: "q1", Name: "qBit", URL: q.srv.URL}}
		c.WebhookRules = []core.WebhookRule{rule}
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return &Server{App: &core.App{Config: store, HTTPClient: http.DefaultClient}}, q, rule
}

func fireStuckSweepEvent(t *testing.T, s *Server, rule core.WebhookRule) {
	t.Helper()
	body := []byte(`{"eventType":"Download","series":{"id":7,"title":"Show"}}`)
	var env connectEventEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("envelope: %v", err)
	}
	inst := s.App.Config.Get().Instances[0]
	if !s.scheduleDeferredStuckReconcile(&rule, &inst, &env, body) {
		t.Fatal("sweep was not scheduled")
	}
}

func waitForRuleHistory(s *Server, n int, within time.Duration) int {
	deadline := time.Now().Add(within)
	for {
		got := len(s.App.Config.Get().WebhookRules[0].History)
		if got >= n || time.Now().After(deadline) {
			return got
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestDeferredStuckSweep_RunsAndRecordsHistory(t *testing.T) {
	s, q, rule := newStuckSweepServer(t, nil)
	fireStuckSweepEvent(t, s, rule)
	if got := waitForRuleHistory(s, 1, 5*time.Second); got != 1 {
		t.Fatalf("history entries = %d, want 1 after the deferred sweep", got)
	}
	q.mu.Lock()
	sets := append([]string(nil), q.sets...)
	q.mu.Unlock()
	if len(sets) != 1 || sets[0] != hashSingle+"=cleanup" {
		t.Errorf("setCategory calls = %v, want only %s=cleanup", sets, hashSingle)
	}
}

func TestDeferredStuckSweep_SkipsWhenRulePausedMeanwhile(t *testing.T) {
	s, q, rule := newStuckSweepServer(t, nil)
	fireStuckSweepEvent(t, s, rule)
	// The user pauses the rule while the sweep is pending.
	if err := s.App.Config.Update(func(c *core.Config) { c.WebhookRules[0].Enabled = false }); err != nil {
		t.Fatalf("pause: %v", err)
	}
	// Wait for the timer to fire and the sweep to give up on the
	// paused rule (it frees its key when it does).
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.stuckSweepMu.Lock()
		pending := len(s.stuckSweepPending)
		s.stuckSweepMu.Unlock()
		if pending == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("sweep still pending long after its deadline")
		}
		time.Sleep(50 * time.Millisecond)
	}
	// The key is freed just before the rule check; give the callback a
	// moment to finish before asserting it did nothing.
	time.Sleep(200 * time.Millisecond)
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.sets) != 0 {
		t.Errorf("paused rule still moved torrents: %v", q.sets)
	}
	if got := len(s.App.Config.Get().WebhookRules[0].History); got != 0 {
		t.Errorf("paused rule recorded %d history entries, want 0", got)
	}
}

func TestRunReconcileApply_UnknownExistingFileScoreIsNotMovedAutomatically(t *testing.T) {
	// /episodefile failing leaves the on-disk file's score at 0. A stuck
	// release scoring below that would look redundant against a file
	// whose real score was never read.
	resp, q := runReconcileApplyOpts(t, nil, fakeSonarrForReconcileOpts(t, true, false))
	redundant := 0
	for _, row := range resp.Reconcile {
		if row.Status == string(engine.StuckRedundant) {
			redundant++
		}
	}
	if redundant == 0 {
		t.Fatal("test setup: expected rows judged redundant against the unread (0) score")
	}
	if len(q.sets) != 0 {
		t.Fatalf("setCategory calls = %v, want none while the existing file's score is unknown", q.sets)
	}
}
