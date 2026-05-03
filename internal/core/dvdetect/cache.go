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

// Cache persists DvDetail results across runs so the ffmpeg +
// dovi_tool RPU extraction doesn't repeat for files we've already
// analysed. Per-file extraction is fast on remux-style sources
// (typically tens of milliseconds per file once dovi_tool finds
// the RPU SEI in the first GOP), but cumulative over a 5000-movie
// library it's still measurable — the cache turns "minutes" into
// "milliseconds" on rescan.
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
//
// Version history:
//   v1 — original (key: movieFileId:size, no tool-version validation)
//   v2 — file-identity triplet key (movieFileId:size:mtime) plus
//        per-entry DoviToolVersion; mismatch on either bypasses the
//        cache. Bumped 2026-05-03 to make cache trustworthy enough
//        to default Skip cache OFF: file replacements that keep size
//        (mtime catches them) and dovi_tool upgrades (per-entry
//        version field catches them) no longer return stale detail.
const cacheFileVersion = 2

// fileEnvelope wraps the cache map with a schema version. Older
// dv-cache.json files (no envelope, just a bare map of entries)
// will fail to unmarshal cleanly; LoadCache handles that case by
// treating it as a fresh cache.
type fileEnvelope struct {
	Version int              `json:"version"`
	Entries map[string]Entry `json:"entries"`
}

// Entry is what the cache stores per (movieFileId, size, mtime) triplet.
// Found distinguishes "extraction succeeded, here's the detail"
// from "extraction succeeded, no RPU found" (the legitimate "API
// said DV but stream actually has none" case — caller emits no-dv
// tag). Both states get cached; only hard errors don't.
//
// Mtime + DoviToolVersion are the v2 additions:
//   - Mtime is the file's modification time (Unix seconds). Same
//     movieFileId + same size + different mtime = file was replaced
//     in-place (rare but possible: cp/rsync over the file outside
//     Radarr). Cache miss on mtime mismatch via the key.
//   - DoviToolVersion is the `dovi_tool --version` first-line output
//     captured at the time of extraction. Compared at Get-time
//     against the current scan's version; mismatch → cache miss
//     even if file-identity matches. Catches dovi_tool upgrades
//     (new version may detect different layer/CM-version semantics).
type Entry struct {
	MovieFileID     int              `json:"movieFileId"`
	Size            int64            `json:"size"`
	Mtime           int64            `json:"mtime"`
	DoviToolVersion string           `json:"doviToolVersion"`
	Detail          engine.DvDetail  `json:"detail"`
	Found           bool             `json:"found"`     // false = "tried, no RPU" — no-dv branch
	CachedAt        time.Time        `json:"cachedAt"`
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

// cacheKey is the canonical map key. File-identity triplet —
// movieFileId alone isn't sufficient (Radarr reuses the integer
// when a file is replaced), and (movieFileId, size) alone misses
// in-place replacements that happen to keep size constant. The
// mtime catches those.
//
// dovi_tool version is intentionally NOT in the key — it's a
// per-scan constant; we'd rather store one entry per file and
// reject on tool-mismatch at Get-time than fragment the cache by
// every dovi_tool version we've ever run.
func cacheKey(movieFileID int, size, mtime int64) string {
	return fmt.Sprintf("%d:%d:%d", movieFileID, size, mtime)
}

// Get returns a cached entry if (movieFileId, size, mtime) is
// known AND the entry's DoviToolVersion matches the current scan's
// version. Second return is false for cache misses — caller does
// the slow ffmpeg+dovi_tool work, then calls Put to memoise.
//
// Tool-version mismatch is treated as a miss (not a hit with a
// version warning) so the next Put overwrites the entry with
// fresh detail under the new tool. No "old vs new" comparison is
// surfaced — the user only sees fresh data.
func (c *Cache) Get(movieFileID int, size, mtime int64, doviToolVersion string) (Entry, bool) {
	if c == nil {
		return Entry{}, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.data[cacheKey(movieFileID, size, mtime)]
	if !ok {
		return Entry{}, false
	}
	if e.DoviToolVersion != doviToolVersion {
		return Entry{}, false
	}
	return e, true
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
func (c *Cache) Put(movieFileID int, size, mtime int64, doviToolVersion string, detail engine.DvDetail, found bool) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data[cacheKey(movieFileID, size, mtime)] = Entry{
		MovieFileID:     movieFileID,
		Size:            size,
		Mtime:           mtime,
		DoviToolVersion: doviToolVersion,
		Detail:          detail,
		Found:           found,
		CachedAt:        time.Now().UTC(),
	}
}

// Delete removes one entry. Used by the "rescan this movie" UI
// action when the user wants to force a fresh extraction.
func (c *Cache) Delete(movieFileID int, size, mtime int64) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.data, cacheKey(movieFileID, size, mtime))
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

// Stats describes what's in the cache right now. Used by the
// "Clear DV cache" UI on the DV detail tab.
type Stats struct {
	EntryCount    int       `json:"entryCount"`
	FileSizeBytes int64     `json:"fileSizeBytes"`            // 0 when no file on disk yet
	OldestCachedAt time.Time `json:"oldestCachedAt,omitempty"` // zero when EntryCount == 0
	NewestCachedAt time.Time `json:"newestCachedAt,omitempty"`
}

// Stats reads in-memory entry count + disk file size + oldest/newest
// CachedAt timestamps. File size is os.Stat (cheap). On-disk file
// missing is not an error — fresh caches return FileSizeBytes=0.
func (c *Cache) Stats() Stats {
	if c == nil {
		return Stats{}
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	s := Stats{EntryCount: len(c.data)}
	for _, e := range c.data {
		if s.OldestCachedAt.IsZero() || e.CachedAt.Before(s.OldestCachedAt) {
			s.OldestCachedAt = e.CachedAt
		}
		if e.CachedAt.After(s.NewestCachedAt) {
			s.NewestCachedAt = e.CachedAt
		}
	}
	if info, err := os.Stat(c.path); err == nil {
		s.FileSizeBytes = info.Size()
	}
	return s
}

// ClearAndSave wipes every entry and persists the result. Save() is
// atomic on disk (tempfile + rename); Clear takes the data write-lock
// then releases it before Save acquires its locks, so a Put landing
// in the millisecond window between the two will be persisted. The
// user-visible behaviour is "clear cache, with a tiny race for any
// concurrent extraction in flight" — practically harmless because
// the user clicks Clear when they want fresh data, and an in-flight
// Put will land that fresh data anyway.
//
// Used by the "Clear DV cache" UI button. Returns the Save error so
// the handler can surface "wiped in memory but couldn't write" to
// the user (very rare — disk full / permissions).
func (c *Cache) ClearAndSave() error {
	if c == nil {
		return errors.New("nil cache")
	}
	c.Clear()
	return c.Save()
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
func LiveKey(movieFileID int, size, mtime int64) string {
	return cacheKey(movieFileID, size, mtime)
}
