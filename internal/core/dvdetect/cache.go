package dvdetect

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"resolvarr/internal/core/engine"
)

// Cache persists DvDetail results across runs so the slow ffmpeg +
// dovi_tool RPU extraction (1-3 seconds per file) doesn't repeat
// for files we've already analysed.
//
// Keyed by (movieFileId, size). The file size invalidates the
// cache when a re-encode replaces the file — same movieFileId can
// resolve to a different file after Radarr re-imports an upgrade.
//
// Persisted to /config/dv-cache.json (or wherever the configDir
// points). Atomic writes via tempfile + os.Rename so a crash mid-
// save can't corrupt the file. Concurrent reads/writes are guarded
// by a sync.RWMutex (data); concurrent Save calls are serialised
// by saveMu so on-disk state always reflects one consistent
// snapshot of the in-memory map.
type Cache struct {
	path   string
	mu     sync.RWMutex
	saveMu sync.Mutex
	data   map[string]Entry
}

// cacheFileVersion is bumped whenever the on-disk schema changes
// in a non-additive way (struct rename, key-format change, semantic
// flip). LoadCache compares against this and discards mismatching
// caches as if they were corrupt-empty — preferable to silently
// loading entries with the wrong shape, which would surface as
// zero-valued Detail fields and wasted re-extraction.
const cacheFileVersion = 1

// fileEnvelope wraps the cache map with a schema version. Older
// dv-cache.json files (no envelope, just a bare map of entries)
// will fail to unmarshal cleanly; LoadCache handles that case by
// treating it as a fresh cache.
type fileEnvelope struct {
	Version int              `json:"version"`
	Entries map[string]Entry `json:"entries"`
}

// Entry is what the cache stores per (movieFileId, size) pair.
// Found distinguishes "extraction succeeded, here's the detail"
// from "extraction succeeded, no RPU found" (the legitimate "API
// said DV but stream actually has none" case — caller emits no-dv
// tag). Both states get cached; only hard errors don't.
type Entry struct {
	MovieFileID int              `json:"movieFileId"`
	Size        int64            `json:"size"`
	Detail      engine.DvDetail  `json:"detail"`
	Found       bool             `json:"found"`     // false = "tried, no RPU" — no-dv branch
	CachedAt    time.Time        `json:"cachedAt"`
}

// LoadCache reads the persisted cache file. Returns an empty
// cache (zero entries, ready to use) when:
//   - the file doesn't exist yet (first-run)
//   - the file is empty (touched but never written)
//   - the file has a different schema version (we discard rather
//     than mis-load entries with the wrong shape)
//
// Surfaces parse errors only for genuinely-corrupt JSON so a real
// data-corruption event doesn't get silently overwritten on the
// next Save.
func LoadCache(configDir string) (*Cache, error) {
	c := &Cache{
		path: filepath.Join(configDir, "dv-cache.json"),
		data: make(map[string]Entry),
	}
	raw, err := os.ReadFile(c.path)
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return nil, fmt.Errorf("read dv cache: %w", err)
	}
	if len(raw) == 0 {
		return c, nil
	}
	var env fileEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("parse dv cache (%s): %w", c.path, err)
	}
	if env.Version != cacheFileVersion {
		// Wrong / unset version → treat as fresh. Next Save will
		// overwrite with the current schema. Acceptable because
		// re-extraction is correct (just slow) and a wrong-schema
		// load is incorrect (zero-value Detail fields would mask
		// real data).
		return c, nil
	}
	if env.Entries != nil {
		c.data = env.Entries
	}
	return c, nil
}

// cacheKey is the canonical map key. movieFileId alone isn't
// sufficient — Radarr reuses the integer when a file is replaced.
// Including size makes the cache self-invalidate when bytes change.
func cacheKey(movieFileID int, size int64) string {
	return fmt.Sprintf("%d:%d", movieFileID, size)
}

// Get returns a cached entry if (movieFileId, size) is known.
// Second return is false for cache misses — caller does the slow
// ffmpeg+dovi_tool work, then calls Put to memoise.
func (c *Cache) Get(movieFileID int, size int64) (Entry, bool) {
	if c == nil {
		return Entry{}, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.data[cacheKey(movieFileID, size)]
	return e, ok
}

// Put memoises a detection result. Always overwrites the existing
// entry for the key. Caller must call Save() to persist; in-memory
// updates alone don't survive a process restart.
//
// Both successful detail (Found=true) and no-RPU (Found=false) get
// cached — repeating either is wasteful. Hard errors must NOT be
// cached: they're often transient (transcode in progress, file
// briefly unreadable) and a stuck cache entry would mask future
// recovery.
func (c *Cache) Put(movieFileID int, size int64, detail engine.DvDetail, found bool) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data[cacheKey(movieFileID, size)] = Entry{
		MovieFileID: movieFileID,
		Size:        size,
		Detail:      detail,
		Found:       found,
		CachedAt:    time.Now().UTC(),
	}
}

// Delete removes one entry. Used by the "rescan this movie" UI
// action when the user wants to force a fresh extraction.
func (c *Cache) Delete(movieFileID int, size int64) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.data, cacheKey(movieFileID, size))
}

// Clear wipes every entry. Used by the "Clear cache" button on
// the DV detail tab. Doesn't delete the file from disk — the next
// Save writes an empty JSON object.
func (c *Cache) Clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data = make(map[string]Entry)
}

// Len returns the number of cached entries. Used for the cache
// status line on the DV detail tab ("127 cached, 3 stale").
func (c *Cache) Len() int {
	if c == nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.data)
}

// Save writes the cache atomically: tempfile in the same directory
// + os.Rename. Same-directory tempfile is deliberate so the rename
// stays within one filesystem (rename across mounts isn't atomic
// and can fail with EXDEV on container-bind-mount layouts).
//
// saveMu serialises concurrent Save calls so that two goroutines
// can't both marshal-then-rename and produce a final on-disk file
// that mirrors *neither* in-memory snapshot exactly. Without this,
// a Put landing between two parallel marshals could result in the
// later-renamed-but-staler bytes winning. The lock is held across
// the full write+rename; data-map RLock is briefly taken inside
// for the marshal itself so reads aren't blocked beyond that.
func (c *Cache) Save() error {
	if c == nil {
		return errors.New("nil cache")
	}
	c.saveMu.Lock()
	defer c.saveMu.Unlock()
	c.mu.RLock()
	env := fileEnvelope{Version: cacheFileVersion, Entries: c.data}
	raw, err := json.MarshalIndent(env, "", "  ")
	c.mu.RUnlock()
	if err != nil {
		return fmt.Errorf("marshal dv cache: %w", err)
	}
	dir := filepath.Dir(c.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir cache dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, "dv-cache-*.tmp")
	if err != nil {
		return fmt.Errorf("create cache tempfile: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("write cache tempfile: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("close cache tempfile: %w", err)
	}
	// Match config-store semantics: 0o600 for any file that lives
	// under /config (it can carry instance metadata via cached
	// movie IDs — not credentials, but tighter is fine).
	if err := os.Chmod(tmpName, 0o600); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("chmod cache tempfile: %w", err)
	}
	if err := os.Rename(tmpName, c.path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("rename cache file: %w", err)
	}
	return nil
}

// PruneStaleByLiveSet removes entries whose key isn't in the
// supplied liveKeys set. Caller passes the set of (movieFileId,
// size) keys observed during the most recent library scan; entries
// not in that set must be from movies that were deleted or
// re-encoded and are no longer reachable. Returns the number of
// entries removed.
//
// CALLER PRECONDITION — only call after a scan that walked the
// full library successfully. Aborted / partial-failure scans must
// NOT call this: a half-built liveKeys set would wipe legitimate
// cache entries for movies the scan never reached, forcing slow
// re-extraction on the next run. Scan handler responsibility, not
// the cache's: cache trusts what it's told.
//
// Save() must be called separately to persist the prune.
func (c *Cache) PruneStaleByLiveSet(liveKeys map[string]bool) int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	removed := 0
	for k := range c.data {
		if !liveKeys[k] {
			delete(c.data, k)
			removed++
		}
	}
	return removed
}

// LiveKey is exported so the scan handler can build the live-set
// without re-implementing the key format.
func LiveKey(movieFileID int, size int64) string {
	return cacheKey(movieFileID, size)
}
