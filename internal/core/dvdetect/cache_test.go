package dvdetect

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"resolvarr/internal/core/engine"
)

func TestCache_LoadEmptyDir(t *testing.T) {
	// First-run scenario: configDir exists, dv-cache.json doesn't.
	// LoadCache must return a usable empty cache, not an error.
	dir := t.TempDir()
	c, err := LoadCache(dir)
	if err != nil {
		t.Fatalf("LoadCache: %v", err)
	}
	if c.Len() != 0 {
		t.Errorf("Len = %d, want 0", c.Len())
	}
	if _, ok := c.Get(1, 1024, 0, ""); ok {
		t.Error("Get on empty cache returned ok=true")
	}
}

// testTool is the dovi_tool version string used across cache tests
// so each test doesn't repeat the constant. Real scans capture this
// from `dovi_tool --version` at scan-start.
const testTool = "dovi_tool 2.1.2"

func TestCache_PutGetRoundtrip(t *testing.T) {
	c, _ := LoadCache(t.TempDir())
	want := engine.DvDetail{Profile: 7, Layer: "fel", CMVersion: 2}
	c.Put(42, 1024*1024, 1700000000, testTool, want, true)
	got, ok := c.Get(42, 1024*1024, 1700000000, testTool)
	if !ok {
		t.Fatal("Get returned ok=false after Put")
	}
	if got.Detail != want {
		t.Errorf("Detail = %+v, want %+v", got.Detail, want)
	}
	if !got.Found {
		t.Error("Found = false, want true")
	}
	if got.MovieFileID != 42 || got.Size != 1024*1024 || got.Mtime != 1700000000 {
		t.Errorf("key roundtrip wrong: id=%d size=%d mtime=%d", got.MovieFileID, got.Size, got.Mtime)
	}
	if got.DoviToolVersion != testTool {
		t.Errorf("DoviToolVersion = %q, want %q", got.DoviToolVersion, testTool)
	}
	if got.CachedAt.IsZero() {
		t.Error("CachedAt is zero — Put didn't stamp it")
	}
}

func TestCache_SizeChangeInvalidatesEntry(t *testing.T) {
	// Same movieFileId, different size = different cache key. The
	// docstring on cacheKey claims this is the self-invalidation
	// mechanism for re-imported upgrades; pin it.
	c, _ := LoadCache(t.TempDir())
	c.Put(7, 1000, 1700000000, testTool, engine.DvDetail{Profile: 7}, true)
	if _, ok := c.Get(7, 2000, 1700000000, testTool); ok {
		t.Error("different size returned a hit — keys must include size")
	}
	if _, ok := c.Get(7, 1000, 1700000000, testTool); !ok {
		t.Error("original key now missing")
	}
}

func TestCache_MtimeChangeInvalidatesEntry(t *testing.T) {
	// Same movieFileId + size, different mtime = in-place file
	// replacement (cp/rsync over the file outside Radarr). The
	// v2 cache key includes mtime to catch this — without it, a
	// byte-size-coincidence replacement would return stale detail.
	c, _ := LoadCache(t.TempDir())
	c.Put(7, 1000, 1700000000, testTool, engine.DvDetail{Profile: 7}, true)
	if _, ok := c.Get(7, 1000, 1700000001, testTool); ok {
		t.Error("different mtime returned a hit — keys must include mtime")
	}
	if _, ok := c.Get(7, 1000, 1700000000, testTool); !ok {
		t.Error("original key now missing")
	}
}

func TestCache_DoviToolVersionMismatchIsMiss(t *testing.T) {
	// Same file-identity, different dovi_tool version = treat as
	// miss. A new tool version may detect different layer/CM-version
	// semantics on the same file; returning the old result would
	// silently lock the user into outdated tags after a tool bump.
	c, _ := LoadCache(t.TempDir())
	c.Put(7, 1000, 1700000000, "dovi_tool 2.1.2", engine.DvDetail{Profile: 7, Layer: "fel"}, true)
	if _, ok := c.Get(7, 1000, 1700000000, "dovi_tool 2.2.0"); ok {
		t.Error("different dovi_tool version returned a hit — must miss on version mismatch")
	}
	// Same version still hits.
	if _, ok := c.Get(7, 1000, 1700000000, "dovi_tool 2.1.2"); !ok {
		t.Error("same version missed — version-equality should hit")
	}
}

func TestCache_FoundFalseStillHit(t *testing.T) {
	// "Tried, no RPU" must register as a cache hit (Found=false).
	// If callers treated Found=false as a miss, every no-DV-stream
	// movie would re-trigger the slow extraction every scan.
	c, _ := LoadCache(t.TempDir())
	c.Put(99, 500, 1700000000, testTool, engine.DvDetail{}, false)
	got, ok := c.Get(99, 500, 1700000000, testTool)
	if !ok {
		t.Fatal("Get on no-RPU entry returned ok=false")
	}
	if got.Found {
		t.Error("Found = true, want false")
	}
}

func TestCache_DeleteRemovesEntry(t *testing.T) {
	c, _ := LoadCache(t.TempDir())
	c.Put(1, 100, 1700000000, testTool, engine.DvDetail{Profile: 8}, true)
	c.Delete(1, 100, 1700000000)
	if _, ok := c.Get(1, 100, 1700000000, testTool); ok {
		t.Error("Get returned ok=true after Delete")
	}
}

func TestCache_Clear(t *testing.T) {
	c, _ := LoadCache(t.TempDir())
	c.Put(1, 100, 1700000000, testTool, engine.DvDetail{}, true)
	c.Put(2, 200, 1700000000, testTool, engine.DvDetail{}, true)
	c.Clear()
	if c.Len() != 0 {
		t.Errorf("Len = %d after Clear, want 0", c.Len())
	}
}

func TestCache_SaveLoadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	c1, _ := LoadCache(dir)
	want := engine.DvDetail{Profile: 7, Layer: "mel", CMVersion: 4}
	c1.Put(101, 5000, 1700000000, testTool, want, true)
	c1.Put(202, 6000, 1700000001, testTool, engine.DvDetail{}, false)
	if err := c1.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// Verify file permission tightened to 0o600.
	info, err := os.Stat(filepath.Join(dir, "dv-cache.json"))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("perm = %o, want 0o600", got)
	}
	// Reopen and verify content survived.
	c2, err := LoadCache(dir)
	if err != nil {
		t.Fatalf("LoadCache (second open): %v", err)
	}
	if c2.Len() != 2 {
		t.Errorf("Len after reload = %d, want 2", c2.Len())
	}
	got, ok := c2.Get(101, 5000, 1700000000, testTool)
	if !ok {
		t.Fatal("Found entry missing after roundtrip")
	}
	if got.Detail != want {
		t.Errorf("Detail after reload = %+v, want %+v", got.Detail, want)
	}
	if got.Mtime != 1700000000 || got.DoviToolVersion != testTool {
		t.Errorf("v2 fields lost on roundtrip: mtime=%d tool=%q", got.Mtime, got.DoviToolVersion)
	}
	noRPU, ok := c2.Get(202, 6000, 1700000001, testTool)
	if !ok {
		t.Fatal("Found=false entry missing after roundtrip")
	}
	if noRPU.Found {
		t.Error("Found flag flipped after reload")
	}
}

func TestCache_LoadCorruptJSONFails(t *testing.T) {
	// A corrupted cache file must surface as an error, not silently
	// reset to empty — silent reset would mask data corruption AND
	// erase real entries on next Save.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "dv-cache.json"), []byte("{{{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCache(dir); err == nil {
		t.Fatal("LoadCache returned nil error on corrupt JSON")
	}
}

func TestCache_LoadEmptyFileIsOK(t *testing.T) {
	// Empty file (touched but never written) is benign — treat as
	// fresh cache. Distinct from corrupt JSON which must error.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "dv-cache.json"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadCache(dir)
	if err != nil {
		t.Fatalf("LoadCache on empty file: %v", err)
	}
	if c.Len() != 0 {
		t.Errorf("Len = %d on empty-file load, want 0", c.Len())
	}
}

func TestCache_PruneStaleByLiveSet(t *testing.T) {
	c, _ := LoadCache(t.TempDir())
	c.Put(1, 100, 1700000000, testTool, engine.DvDetail{}, true)
	c.Put(2, 200, 1700000000, testTool, engine.DvDetail{}, true)
	c.Put(3, 300, 1700000000, testTool, engine.DvDetail{}, true)
	live := map[string]bool{
		LiveKey(1, 100, 1700000000): true,
		LiveKey(3, 300, 1700000000): true,
		// 2 omitted — should be pruned
	}
	removed := c.PruneStaleByLiveSet(live)
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}
	if c.Len() != 2 {
		t.Errorf("Len after prune = %d, want 2", c.Len())
	}
	if _, ok := c.Get(2, 200, 1700000000, testTool); ok {
		t.Error("pruned entry still present")
	}
}

func TestCache_SaveLeavesNoTempfileBehind(t *testing.T) {
	// Sanity: Save() must not leave its *.tmp scratch file in the
	// cache dir after a successful rename. (Real partial-write
	// crash testing would need fault injection — out of scope.)
	dir := t.TempDir()
	c, _ := LoadCache(dir)
	c.Put(1, 100, 1700000000, testTool, engine.DvDetail{Profile: 8}, true)
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "dv-cache-*.tmp"))
	if len(matches) > 0 {
		t.Errorf("tempfiles leaked: %v", matches)
	}
}

func TestCache_SaveProducesValidJSON(t *testing.T) {
	// Catch silent struct-tag drift: the persisted JSON must be
	// valid + parseable into the expected envelope shape.
	dir := t.TempDir()
	c, _ := LoadCache(dir)
	c.Put(7, 9999, 1700000000, testTool, engine.DvDetail{Profile: 7, Layer: "fel", CMVersion: 2}, true)
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "dv-cache.json"))
	if err != nil {
		t.Fatal(err)
	}
	var parsed fileEnvelope
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.Version != cacheFileVersion {
		t.Errorf("envelope version = %d, want %d", parsed.Version, cacheFileVersion)
	}
	e, ok := parsed.Entries[LiveKey(7, 9999, 1700000000)]
	if !ok {
		t.Fatal("expected key missing in JSON")
	}
	if e.Detail.Profile != 7 || e.Detail.Layer != "fel" || e.Detail.CMVersion != 2 {
		t.Errorf("unmarshalled detail wrong: %+v", e.Detail)
	}
}

func TestCache_LoadDifferentVersionTreatedAsFresh(t *testing.T) {
	// Schema-version mismatch must not silently mis-load entries
	// (which would surface as zero-value DvDetail). Discard cleanly.
	dir := t.TempDir()
	wrongVersion := fileEnvelope{
		Version: cacheFileVersion + 999,
		Entries: map[string]Entry{
			LiveKey(1, 100, 1700000000): {
				MovieFileID: 1, Size: 100, Mtime: 1700000000, DoviToolVersion: testTool,
				Detail: engine.DvDetail{Profile: 7, Layer: "fel", CMVersion: 2},
				Found:  true,
			},
		},
	}
	raw, _ := json.Marshal(wrongVersion)
	if err := os.WriteFile(filepath.Join(dir, "dv-cache.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadCache(dir)
	if err != nil {
		t.Fatalf("LoadCache: %v", err)
	}
	if c.Len() != 0 {
		t.Errorf("Len = %d on mismatched version, want 0 (fresh)", c.Len())
	}
}

func TestCache_LoadLegacyBareMapTreatedAsFresh(t *testing.T) {
	// Pre-envelope cache format (just `map[string]Entry`) must not
	// crash and must not import legacy entries (their schema is
	// considered unverified). Treated as fresh; next Save writes
	// the new envelope format.
	dir := t.TempDir()
	legacy := map[string]Entry{
		LiveKey(1, 100, 1700000000): {MovieFileID: 1, Size: 100, Mtime: 1700000000, DoviToolVersion: testTool, Found: true},
	}
	raw, _ := json.Marshal(legacy)
	if err := os.WriteFile(filepath.Join(dir, "dv-cache.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadCache(dir)
	if err != nil {
		t.Fatalf("LoadCache: %v", err)
	}
	if c.Len() != 0 {
		t.Errorf("Len on legacy bare-map = %d, want 0", c.Len())
	}
}

func TestCache_NilSafe(t *testing.T) {
	// Methods must no-op on nil receiver so callers that haven't
	// called LoadCache yet (e.g. cache disabled in config) don't
	// panic. Save returns an error so misuse is observable.
	var c *Cache
	c.Put(1, 1, 0, "", engine.DvDetail{}, true)
	c.Delete(1, 1, 0)
	c.Clear()
	if got, ok := c.Get(1, 1, 0, ""); ok || got.Found {
		t.Error("nil Get returned a hit")
	}
	if c.Len() != 0 {
		t.Error("nil Len != 0")
	}
	if c.PruneStaleByLiveSet(nil) != 0 {
		t.Error("nil Prune != 0")
	}
	if err := c.Save(); err == nil {
		t.Error("nil Save returned nil error")
	}
	// New methods used by the cache-clear UI must also be nil-safe.
	if got := c.Stats(); got.EntryCount != 0 || got.FileSizeBytes != 0 {
		t.Errorf("nil Stats = %+v, want zero value", got)
	}
	if err := c.ClearAndSave(); err == nil {
		t.Error("nil ClearAndSave returned nil error")
	}
}

func TestCache_StatsEmpty(t *testing.T) {
	c, _ := LoadCache(t.TempDir())
	s := c.Stats()
	if s.EntryCount != 0 || s.FileSizeBytes != 0 {
		t.Errorf("Stats on empty cache = %+v, want zero counts", s)
	}
	if !s.OldestCachedAt.IsZero() || !s.NewestCachedAt.IsZero() {
		t.Error("Stats on empty cache returned non-zero timestamps")
	}
}

func TestCache_StatsCountsAndTimestamps(t *testing.T) {
	c, _ := LoadCache(t.TempDir())
	// Sleep between Puts so CachedAt timestamps differ measurably.
	// time.Now().UTC() resolution can collapse three back-to-back
	// Puts into the same instant on Linux; without the sleep the
	// Oldest != Newest assertion below would pass for the wrong
	// reason (equality satisfies !After). 1ms is plenty for the
	// time package's monotonic clock.
	c.Put(1, 100, 1700000000, testTool, engine.DvDetail{Profile: 8}, true)
	time.Sleep(time.Millisecond)
	c.Put(2, 200, 1700000000, testTool, engine.DvDetail{Profile: 7}, false)
	time.Sleep(time.Millisecond)
	c.Put(3, 300, 1700000000, testTool, engine.DvDetail{Profile: 5}, true)
	s := c.Stats()
	if s.EntryCount != 3 {
		t.Errorf("EntryCount = %d, want 3", s.EntryCount)
	}
	if s.OldestCachedAt.IsZero() || s.NewestCachedAt.IsZero() {
		t.Error("Stats with entries returned zero timestamps")
	}
	if !s.OldestCachedAt.Before(s.NewestCachedAt) {
		t.Errorf("OldestCachedAt %v not strictly before NewestCachedAt %v", s.OldestCachedAt, s.NewestCachedAt)
	}
	// File size is 0 — Save() hasn't been called.
	if s.FileSizeBytes != 0 {
		t.Errorf("FileSizeBytes = %d before Save, want 0", s.FileSizeBytes)
	}
}

func TestCache_StatsFileSizeAfterSave(t *testing.T) {
	dir := t.TempDir()
	c, _ := LoadCache(dir)
	c.Put(1, 100, 1700000000, testTool, engine.DvDetail{Profile: 8, Layer: "mel"}, true)
	if err := c.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	s := c.Stats()
	if s.FileSizeBytes <= 0 {
		t.Errorf("FileSizeBytes = %d after Save, want > 0", s.FileSizeBytes)
	}
}

func TestCache_ClearAndSavePersists(t *testing.T) {
	dir := t.TempDir()
	c, _ := LoadCache(dir)
	c.Put(1, 100, 1700000000, testTool, engine.DvDetail{Profile: 8}, true)
	c.Put(2, 200, 1700000000, testTool, engine.DvDetail{Profile: 7}, true)
	if err := c.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// Sanity: file has content before clear.
	if s := c.Stats(); s.FileSizeBytes == 0 {
		t.Fatalf("FileSizeBytes = 0 before clear, want > 0")
	}
	if err := c.ClearAndSave(); err != nil {
		t.Fatalf("ClearAndSave: %v", err)
	}
	// In-memory wiped.
	if c.Len() != 0 {
		t.Errorf("Len = %d after ClearAndSave, want 0", c.Len())
	}
	// On-disk file persists with empty entries — re-load proves it.
	c2, err := LoadCache(dir)
	if err != nil {
		t.Fatalf("re-load: %v", err)
	}
	if c2.Len() != 0 {
		t.Errorf("re-loaded Len = %d, want 0", c2.Len())
	}
}

func TestCache_ConcurrentReadWriteIsSafe(t *testing.T) {
	// Use the race detector via `go test -race` to catch any
	// missed locking. Blasts Put/Get/Len/Save concurrently so
	// missing locking surfaces under -race. Save calls in the mix
	// catch the saveMu serialisation hole specifically.
	c, _ := LoadCache(t.TempDir())
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			c.Put(id, int64(id*1000), 1700000000, testTool, engine.DvDetail{Profile: 7}, true)
			_, _ = c.Get(id, int64(id*1000), 1700000000, testTool)
		}(i)
	}
	for i := 0; i < 25; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = c.Len()
		}()
	}
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = c.Save()
		}()
	}
	wg.Wait()
	if c.Len() != 50 {
		t.Errorf("Len after concurrent puts = %d, want 50", c.Len())
	}
	// Final on-disk file must be a valid envelope after the
	// concurrent Save flurry — saveMu guarantees no half-written
	// state lands on disk.
	c2, err := LoadCache(filepath.Dir(c.path))
	if err != nil {
		t.Fatalf("re-load after concurrent Saves: %v", err)
	}
	if c2.Len() == 0 {
		t.Error("on-disk cache is empty after concurrent Save — saveMu may not be serialising properly")
	}
}
