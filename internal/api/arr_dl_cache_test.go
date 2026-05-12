package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"resolvarr/internal/arr"
	"resolvarr/internal/core"
)

// arr_dl_cache_test.go — coverage for the per-instance Arr download-
// client cache. Uses an injected clock so the 5min TTL is testable
// in milliseconds, and a fixture httptest.Server to count fetches.

func newCountingArrServer(t *testing.T) (*httptest.Server, *int32) {
	var hits int32
	fixture := []arr.ArrDownloadClient{
		{ID: 1, Name: "qbt-main", Implementation: "QBittorrent",
			Fields: []arr.ArrDownloadClientField{
				{Name: "movieCategory", Value: "pre"},
				{Name: "movieImportedCategory", Value: "post"},
			}},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		_ = json.NewEncoder(w).Encode(fixture)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestArrDLCache_GetCached(t *testing.T) {
	srv, hits := newCountingArrServer(t)
	c := newArrDownloadClientCache()
	now := time.Now()
	c.now = func() time.Time { return now }

	inst := &core.Instance{ID: "i1"}
	client := &arr.Client{URL: srv.URL, APIKey: "k", HTTP: http.DefaultClient}

	// First call → fetch.
	if _, err := c.Get(context.Background(), inst, client); err != nil {
		t.Fatalf("first Get: %v", err)
	}
	if atomic.LoadInt32(hits) != 1 {
		t.Errorf("after 1st Get: hits = %d, want 1", *hits)
	}
	// Second call within TTL → cache hit.
	if _, err := c.Get(context.Background(), inst, client); err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if atomic.LoadInt32(hits) != 1 {
		t.Errorf("after 2nd Get (within TTL): hits = %d, want 1", *hits)
	}
}

func TestArrDLCache_ExpiresAfterTTL(t *testing.T) {
	srv, hits := newCountingArrServer(t)
	c := newArrDownloadClientCache()
	now := time.Now()
	c.now = func() time.Time { return now }

	inst := &core.Instance{ID: "i1"}
	client := &arr.Client{URL: srv.URL, APIKey: "k", HTTP: http.DefaultClient}

	if _, err := c.Get(context.Background(), inst, client); err != nil {
		t.Fatalf("first Get: %v", err)
	}
	// Bump clock past TTL.
	now = now.Add(arrDLCacheTTL + time.Second)
	if _, err := c.Get(context.Background(), inst, client); err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if atomic.LoadInt32(hits) != 2 {
		t.Errorf("after TTL bump: hits = %d, want 2 (cache should have expired)", *hits)
	}
}

func TestArrDLCache_Invalidate(t *testing.T) {
	srv, hits := newCountingArrServer(t)
	c := newArrDownloadClientCache()

	inst := &core.Instance{ID: "i1"}
	client := &arr.Client{URL: srv.URL, APIKey: "k", HTTP: http.DefaultClient}

	if _, err := c.Get(context.Background(), inst, client); err != nil {
		t.Fatalf("first Get: %v", err)
	}
	c.Invalidate("i1")
	if _, err := c.Get(context.Background(), inst, client); err != nil {
		t.Fatalf("post-invalidate Get: %v", err)
	}
	if atomic.LoadInt32(hits) != 2 {
		t.Errorf("after Invalidate: hits = %d, want 2", *hits)
	}
}

func TestArrDLCache_InvalidateUnknownInstance_NoOp(t *testing.T) {
	c := newArrDownloadClientCache()
	c.Invalidate("ghost") // shouldn't panic on empty cache
	c.Invalidate("")      // shouldn't panic on empty ID
}

func TestArrDLCache_ConcurrentGet_RaceSafe(t *testing.T) {
	// One inflight at a time guaranteed by the mutex — this exists for
	// the race detector to confirm we hold the lock around the map
	// access on both the read + write paths.
	srv, _ := newCountingArrServer(t)
	c := newArrDownloadClientCache()
	inst := &core.Instance{ID: "i1"}
	client := &arr.Client{URL: srv.URL, APIKey: "k", HTTP: http.DefaultClient}

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = c.Get(context.Background(), inst, client)
		}()
	}
	wg.Wait()
}

func TestArrDLCache_NilInstance(t *testing.T) {
	c := newArrDownloadClientCache()
	got, err := c.Get(context.Background(), nil, nil)
	if err != nil {
		t.Errorf("nil inst should return nil error, got %v", err)
	}
	if got != nil {
		t.Errorf("nil inst should return nil slice, got %+v", got)
	}
}

func TestArrDLCache_NegativeCaching(t *testing.T) {
	// Server that always returns 500 — ensure the cache stores the err
	// AND a second Get within TTL doesn't re-hit the server.
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(500)
	}))
	defer srv.Close()
	c := newArrDownloadClientCache()
	inst := &core.Instance{ID: "i1"}
	client := &arr.Client{URL: srv.URL, APIKey: "k", HTTP: http.DefaultClient}

	if _, err := c.Get(context.Background(), inst, client); err == nil {
		t.Fatal("expected error on first Get")
	}
	if _, err := c.Get(context.Background(), inst, client); err == nil {
		t.Fatal("expected cached error on second Get")
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Errorf("negative cache miss — hits = %d, want 1", hits)
	}
}
