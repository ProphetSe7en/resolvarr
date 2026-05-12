package api

// arr_dl_cache.go — short-TTL cache for Arr `/api/v3/downloadclient`
// reads.
//
// Why: the qBit Category Fix function reads the download-client list
// on every Import event to resolve pre/post category names. A Sonarr
// whole-season pack import is N back-to-back Connect events for one
// series — without a cache that's N HTTP round-trips to the same Arr
// for the same list. The cache collapses to one fetch per 5 minutes
// per Arr instance.
//
// 5 min was picked so a user who edits the qBit category in Sonarr's
// Settings sees the change reflected in resolvarr within five minutes
// of saving (without forcing a Refresh button). The UI's Refresh-from-
// Arr button invalidates the cache for that instance so the change
// can be picked up immediately.
//
// Cache key: instance ID. Stored value carries fetchedAt + the result
// or the error — negative caching is included so a temporarily-down
// Arr doesn't fan out 100 retries during the 5-minute window.

import (
	"context"
	"sync"
	"time"

	"resolvarr/internal/arr"
	"resolvarr/internal/core"
)

// arrDLCacheTTL is the cache lifetime per (instance × entry). 5 minutes
// trades freshness for hot-path savings: the typical Connect feed
// (whole-season import) fits well inside one window, and a user editing
// download-client config in Sonarr/Radarr UI sees the change within
// 5min without any refresh dance.
const arrDLCacheTTL = 5 * time.Minute

// arrDLCacheEntry holds one cached fetch result. Negative results are
// cached too — when an Arr is unreachable for a few seconds we don't
// want 100 simultaneous Connect events for the same instance to fan
// out 100 retries; one error gets shared until TTL expires.
type arrDLCacheEntry struct {
	fetchedAt time.Time
	clients   []arr.ArrDownloadClient
	err       error
}

// arrDownloadClientCache is a per-Server, per-instance-ID cache for
// Arr download-client list reads. Safe for concurrent use; one mutex
// guards every entry (the map is small, contention is non-existent).
type arrDownloadClientCache struct {
	mu      sync.Mutex
	entries map[string]arrDLCacheEntry
	// now is a test seam — production code calls time.Now; tests inject
	// a deterministic clock. Default nil → real time.
	now func() time.Time
}

// newArrDownloadClientCache constructs an empty cache. main.go calls
// this once at boot + injects on Server via AttachArrDLCache.
func newArrDownloadClientCache() *arrDownloadClientCache {
	return &arrDownloadClientCache{
		entries: map[string]arrDLCacheEntry{},
	}
}

// timeNow returns the configured clock or time.Now as the default.
func (c *arrDownloadClientCache) timeNow() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// Get returns the cached list when fresh, otherwise calls the Arr +
// stores the result. Errors are cached too so a downed Arr doesn't
// fan out retries during the 5-minute window.
//
// Single-flight: protected by c.mu for the entire fetch. A second
// caller arriving while the first is mid-fetch will block briefly
// on the mutex; this is intentional — the alternative (per-call
// goroutine) opens up the same N-fan-out problem the cache exists
// to solve.
func (c *arrDownloadClientCache) Get(ctx context.Context, inst *core.Instance, client *arr.Client) ([]arr.ArrDownloadClient, error) {
	if inst == nil {
		return nil, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, ok := c.entries[inst.ID]; ok {
		if c.timeNow().Sub(entry.fetchedAt) < arrDLCacheTTL {
			return entry.clients, entry.err
		}
	}
	// Cache miss or expired — fetch fresh.
	clients, err := client.ListDownloadClients(ctx)
	c.entries[inst.ID] = arrDLCacheEntry{
		fetchedAt: c.timeNow(),
		clients:   clients,
		err:       err,
	}
	return clients, err
}

// Invalidate clears the cache entry for one instance. Called from
// the "Refresh from Arr" UI button so a user who just edited a
// download-client category in Sonarr/Radarr's UI can see the change
// in resolvarr immediately, without waiting for the 5min TTL.
func (c *arrDownloadClientCache) Invalidate(instanceID string) {
	if instanceID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, instanceID)
}
