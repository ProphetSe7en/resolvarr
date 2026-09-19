package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"resolvarr/internal/core"
)

// scan_reconcile_targets_test.go — coverage for the download-client
// fan-out on the reconcile apply path.
//
// The behaviour worth locking: with several qBittorrent clients
// configured, a stuck download must be acted on by the client that
// ACTUALLY holds its hash, using that client's own target category.
// Getting this wrong is silent — the wrong client returns 200 for a
// hash it doesn't have, or the run only ever touches client #1 and the
// other clients' stuck downloads quietly pile up forever.

func TestReconcileTargetsFromRequest_FoldsLegacyPair(t *testing.T) {
	cases := []struct {
		name string
		req  scanRunRequest
		want []scanReconcileTarget
	}{
		{
			name: "legacy pair only",
			req: scanRunRequest{
				ReconcileQbitInstanceID: "q1",
				ReconcilePostCategory:   " cleanup ",
			},
			want: []scanReconcileTarget{{QbitInstanceID: "q1", PostCategory: "cleanup"}},
		},
		{
			name: "targets only",
			req: scanRunRequest{
				ReconcileTargets: []scanReconcileTarget{
					{QbitInstanceID: "q1", PostCategory: "movies-done"},
					{QbitInstanceID: "q2", PostCategory: "tv-done"},
				},
			},
			want: []scanReconcileTarget{
				{QbitInstanceID: "q1", PostCategory: "movies-done"},
				{QbitInstanceID: "q2", PostCategory: "tv-done"},
			},
		},
		{
			// The UI sends both shapes: the modal's own pair AND the
			// full list including it. The pair must not produce a
			// duplicate row (the validator rejects duplicates, and a
			// duplicate would make the probe order decide the category).
			name: "legacy pair already covered by targets",
			req: scanRunRequest{
				ReconcileQbitInstanceID: "q1",
				ReconcilePostCategory:   "cleanup",
				ReconcileTargets: []scanReconcileTarget{
					{QbitInstanceID: "q1", PostCategory: "cleanup"},
					{QbitInstanceID: "q2", PostCategory: "tv-done"},
				},
			},
			want: []scanReconcileTarget{
				{QbitInstanceID: "q1", PostCategory: "cleanup"},
				{QbitInstanceID: "q2", PostCategory: "tv-done"},
			},
		},
		{
			name: "legacy pair names a client the list misses",
			req: scanRunRequest{
				ReconcileQbitInstanceID: "q9",
				ReconcilePostCategory:   "leftovers",
				ReconcileTargets: []scanReconcileTarget{
					{QbitInstanceID: "q1", PostCategory: "cleanup"},
				},
			},
			want: []scanReconcileTarget{
				{QbitInstanceID: "q1", PostCategory: "cleanup"},
				{QbitInstanceID: "q9", PostCategory: "leftovers"},
			},
		},
		{
			name: "nothing configured",
			req:  scanRunRequest{},
			want: []scanReconcileTarget{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := reconcileTargetsFromRequest(tc.req)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d targets %v, want %d %v", len(got), got, len(tc.want), tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("target[%d] = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// qbitFanoutFake is a minimal qBittorrent stand-in that owns a fixed set
// of hashes. /torrents/info?hashes=X returns the torrent only when this
// client holds it, which is exactly the signal the fan-out routes on.
// infoCalls counts probes so the memoisation test can assert on it.
type qbitFanoutFake struct {
	hashes    map[string]bool
	infoCalls int32
	srv       *httptest.Server
}

func newQbitFanoutFake(t *testing.T, hashes ...string) *qbitFanoutFake {
	t.Helper()
	q := &qbitFanoutFake{hashes: map[string]bool{}}
	for _, h := range hashes {
		q.hashes[strings.ToLower(h)] = true
	}
	q.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/auth/login"):
			_, _ = w.Write([]byte("Ok."))
		case strings.HasSuffix(r.URL.Path, "/torrents/info"):
			atomic.AddInt32(&q.infoCalls, 1)
			h := strings.ToLower(r.URL.Query().Get("hashes"))
			if h != "" && !q.hashes[h] {
				_, _ = w.Write([]byte("[]"))
				return
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"hash": h, "name": "release", "category": "pre", "state": "pausedUP"},
			})
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(q.srv.Close)
	return q
}

func TestReconcileClients_RoutesHashToOwningClient(t *testing.T) {
	const (
		hashA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		hashB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)
	qa := newQbitFanoutFake(t, hashA)
	qb := newQbitFanoutFake(t, hashB)

	cfg := core.Config{QbitInstances: []core.QbitInstance{
		{ID: "q1", Name: "movies", URL: qa.srv.URL},
		{ID: "q2", Name: "tv", URL: qb.srv.URL},
	}}
	rc, apiErr := newReconcileClients(cfg, []scanReconcileTarget{
		{QbitInstanceID: "q1", PostCategory: "movies-done"},
		{QbitInstanceID: "q2", PostCategory: "tv-done"},
	})
	if apiErr != nil {
		t.Fatalf("newReconcileClients: %v", apiErr.Message)
	}

	ctx := context.Background()

	// hashA lives in the movies client and must get THAT client's category.
	gotA, okA := rc.clientFor(ctx, hashA)
	if !okA {
		t.Fatal("hashA: no client claimed it")
	}
	if gotA.name != "movies" || gotA.category != "movies-done" {
		t.Errorf("hashA routed to %q/%q, want movies/movies-done", gotA.name, gotA.category)
	}

	// hashB lives in the tv client — the regression this whole file
	// exists for: before the fan-out it would have been sent to client
	// #1 and silently done nothing.
	gotB, okB := rc.clientFor(ctx, hashB)
	if !okB {
		t.Fatal("hashB: no client claimed it")
	}
	if gotB.name != "tv" || gotB.category != "tv-done" {
		t.Errorf("hashB routed to %q/%q, want tv/tv-done", gotB.name, gotB.category)
	}

	// A hash nobody owns is reported as unmatched rather than guessed at.
	if _, ok := rc.clientFor(ctx, "cccccccccccccccccccccccccccccccccccccccc"); ok {
		t.Error("unknown hash was claimed by a client")
	}
}

func TestReconcileClients_MemoisesLookupPerHash(t *testing.T) {
	const hashA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	qa := newQbitFanoutFake(t, hashA)
	qb := newQbitFanoutFake(t)

	cfg := core.Config{QbitInstances: []core.QbitInstance{
		{ID: "q1", Name: "movies", URL: qa.srv.URL},
		{ID: "q2", Name: "tv", URL: qb.srv.URL},
	}}
	rc, apiErr := newReconcileClients(cfg, []scanReconcileTarget{
		{QbitInstanceID: "q1", PostCategory: "movies-done"},
		{QbitInstanceID: "q2", PostCategory: "tv-done"},
	})
	if apiErr != nil {
		t.Fatalf("newReconcileClients: %v", apiErr.Message)
	}

	ctx := context.Background()
	// A Sonarr season pack is N queue rows sharing one hash; the probe
	// must happen once, not once per episode row.
	for i := 0; i < 5; i++ {
		if _, ok := rc.clientFor(ctx, hashA); !ok {
			t.Fatalf("lookup %d: hashA unclaimed", i)
		}
	}
	if got := atomic.LoadInt32(&qa.infoCalls); got != 1 {
		t.Errorf("movies client probed %d times, want 1 (memoised)", got)
	}
}

func TestReconcileClients_SingleClientSkipsProbe(t *testing.T) {
	qa := newQbitFanoutFake(t)

	cfg := core.Config{QbitInstances: []core.QbitInstance{
		{ID: "q1", Name: "movies", URL: qa.srv.URL},
	}}
	rc, apiErr := newReconcileClients(cfg, []scanReconcileTarget{
		{QbitInstanceID: "q1", PostCategory: "movies-done"},
	})
	if apiErr != nil {
		t.Fatalf("newReconcileClients: %v", apiErr.Message)
	}
	// With one client there is nothing to disambiguate, so the single-
	// client path must stay exactly as cheap as it was before the
	// fan-out landed: zero extra round-trips.
	got, ok := rc.clientFor(context.Background(), "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if !ok || got.name != "movies" {
		t.Fatalf("single-client lookup = (%+v, %v), want the only client", got, ok)
	}
	if n := atomic.LoadInt32(&qa.infoCalls); n != 0 {
		t.Errorf("single-client path made %d probe calls, want 0", n)
	}
}

func TestNewReconcileClients_RejectsBadTargets(t *testing.T) {
	cfg := core.Config{QbitInstances: []core.QbitInstance{
		{ID: "q1", Name: "movies", URL: "http://127.0.0.1:1"},
	}}
	if _, err := newReconcileClients(cfg, nil); err == nil {
		t.Error("empty target list accepted, want rejection")
	}
	if _, err := newReconcileClients(cfg, []scanReconcileTarget{{QbitInstanceID: "q1"}}); err == nil {
		t.Error("missing category accepted, want rejection")
	}
	if _, err := newReconcileClients(cfg, []scanReconcileTarget{{QbitInstanceID: "nope", PostCategory: "x"}}); err == nil {
		t.Error("unknown qBit instance accepted, want rejection")
	}
}
