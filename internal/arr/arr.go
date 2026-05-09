// Package arr is a thin client for Radarr/Sonarr v3 REST APIs.
// Both apps share the same endpoint shape for the functionality tagarr
// needs (system status, tag CRUD, item listing, editor tag-apply), so a
// single client serves both — the Type field on a config.Instance tells
// callers which path collection to hit.
package arr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client talks to a Radarr or Sonarr instance.
type Client struct {
	URL    string
	APIKey string
	HTTP   *http.Client

	// InstanceName is included in debug-log lines so multi-instance setups
	// (radarr + radarr4k + sonarr) can be told apart in runs.log. Optional —
	// empty falls back to the URL's host portion in the log line.
	InstanceName string

	// DebugLog, when non-nil, is invoked once per HTTP call with method,
	// path, status (or 0 on transport error), latency, and a short
	// summary string. Lets the api layer route Arr-API traffic into the
	// runs.log debug stream when the user toggles debug from Settings.
	// Nil = no logging (zero overhead).
	DebugLog func(method, path string, status int, latency time.Duration, summary string)
}

// SystemStatus is the subset of /api/v3/system/status we care about.
type SystemStatus struct {
	Version      string `json:"version"`
	AppName      string `json:"appName"`
	InstanceName string `json:"instanceName"`
}

// Tag is a label-only view of a tag definition.
type Tag struct {
	ID    int    `json:"id"`
	Label string `json:"label"`
}

// TagDetail is /api/v3/tag/detail — a tag plus the IDs of items that use it.
// Radarr populates MovieIDs; Sonarr populates SeriesIDs.
type TagDetail struct {
	ID        int    `json:"id"`
	Label     string `json:"label"`
	MovieIDs  []int  `json:"movieIds,omitempty"`
	SeriesIDs []int  `json:"seriesIds,omitempty"`
	// Non-item references — populated by Radarr/Sonarr's
	// /api/v3/tag/detail. Radarr/Sonarr refuse to delete a tag that
	// has any of these set; surfacing them lets the frontend warn
	// the user BEFORE they hit Delete and get a cryptic API error.
	// Different Arr types expose different subsets — we just
	// deserialize whatever's present and ignore the rest.
	NotificationIDs    []int `json:"notificationIds,omitempty"`
	RestrictionIDs     []int `json:"restrictionIds,omitempty"`
	IndexerIDs         []int `json:"indexerIds,omitempty"`
	ImportListIDs      []int `json:"importListIds,omitempty"`
	DownloadClientIDs  []int `json:"downloadClientIds,omitempty"`
	AutoTaggingIDs     []int `json:"autoTagIds,omitempty"`
	ReleaseProfileIDs  []int `json:"releaseProfileIds,omitempty"`
	DelayProfileIDs    []int `json:"delayProfileIds,omitempty"`
	RootFolderIDs      []int `json:"rootFolderIds,omitempty"` // Radarr only
}

// UsageCount returns the number of items (movies or series) that use the tag.
func (d TagDetail) UsageCount() int {
	if len(d.MovieIDs) > 0 {
		return len(d.MovieIDs)
	}
	return len(d.SeriesIDs)
}

// NonItemUsage returns a label → count map for every non-item
// reference the tag carries (Lists, Custom Formats, Notifications,
// etc.). Empty map means the tag is only attached to items and is
// safe to delete. Tags with non-item references would otherwise
// cause Radarr/Sonarr's DELETE /tag/{id} call to fail with a
// cryptic error — this helper lets the UI warn the user first.
func (d TagDetail) NonItemUsage() map[string]int {
	out := make(map[string]int)
	if n := len(d.NotificationIDs); n > 0 {
		out["Notifications"] = n
	}
	if n := len(d.RestrictionIDs); n > 0 {
		out["Restrictions"] = n
	}
	if n := len(d.IndexerIDs); n > 0 {
		out["Indexers"] = n
	}
	if n := len(d.ImportListIDs); n > 0 {
		out["Lists"] = n
	}
	if n := len(d.DownloadClientIDs); n > 0 {
		out["Download Clients"] = n
	}
	if n := len(d.AutoTaggingIDs); n > 0 {
		out["Auto-Tagging rules"] = n
	}
	if n := len(d.ReleaseProfileIDs); n > 0 {
		out["Release Profiles"] = n
	}
	if n := len(d.DelayProfileIDs); n > 0 {
		out["Delay Profiles"] = n
	}
	if n := len(d.RootFolderIDs); n > 0 {
		out["Root Folders"] = n
	}
	return out
}

// HasNonItemReferences reports whether deleting this tag will fail
// because Radarr/Sonarr considers it in-use somewhere outside the
// item list (Lists, CFs, etc.).
func (d TagDetail) HasNonItemReferences() bool {
	return len(d.NotificationIDs) > 0 ||
		len(d.RestrictionIDs) > 0 ||
		len(d.IndexerIDs) > 0 ||
		len(d.ImportListIDs) > 0 ||
		len(d.DownloadClientIDs) > 0 ||
		len(d.AutoTaggingIDs) > 0 ||
		len(d.ReleaseProfileIDs) > 0 ||
		len(d.DelayProfileIDs) > 0 ||
		len(d.RootFolderIDs) > 0
}

// UsageIDs returns the item IDs (movies or series) that use the tag, whichever is non-empty.
func (d TagDetail) UsageIDs() []int {
	if len(d.MovieIDs) > 0 {
		return d.MovieIDs
	}
	return d.SeriesIDs
}

// do builds and executes a request against the Arr instance.
// body may be nil. Returns the response (caller closes body) and the status code.
func (c *Client) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	endpoint := strings.TrimRight(c.URL, "/") + path
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, rdr)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("X-Api-Key", c.APIKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	start := time.Now()
	resp, err := c.HTTP.Do(req)
	if c.DebugLog != nil {
		latency := time.Since(start)
		status := 0
		summary := ""
		if err != nil {
			summary = "transport_error: " + err.Error()
		} else if resp != nil {
			status = resp.StatusCode
		}
		c.DebugLog(method, path, status, latency, summary)
	}
	return resp, err
}

// unwrap reads and discards a response body, returning an error for non-2xx.
func unwrap(resp *http.Response) error {
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if resp.StatusCode == 401 {
		return fmt.Errorf("unauthorized — check API key")
	}
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	snippet := strings.TrimSpace(string(bodyBytes))
	if snippet == "" {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return fmt.Errorf("HTTP %d: %s", resp.StatusCode, snippet)
}

// TestConnection hits /api/v3/system/status. Returns the version string.
func (c *Client) TestConnection() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	resp, err := c.do(ctx, "GET", "/api/v3/system/status", nil)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 {
		return "", fmt.Errorf("unauthorized — check API key")
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var s SystemStatus
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}
	if s.Version == "" {
		return "", fmt.Errorf("empty version in response — URL may not be an Arr instance")
	}
	return s.Version, nil
}

// ListTagDetails returns every tag along with the IDs of items that use it.
func (c *Client) ListTagDetails(ctx context.Context) ([]TagDetail, error) {
	resp, err := c.do(ctx, "GET", "/api/v3/tag/detail", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var tags []TagDetail
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return nil, fmt.Errorf("parse tags: %w", err)
	}
	return tags, nil
}

// CreateTag creates a new tag definition and returns the saved record (with ID).
func (c *Client) CreateTag(ctx context.Context, label string) (Tag, error) {
	resp, err := c.do(ctx, "POST", "/api/v3/tag", map[string]string{"label": label})
	if err != nil {
		return Tag{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Tag{}, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var t Tag
	if err := json.NewDecoder(resp.Body).Decode(&t); err != nil {
		return Tag{}, fmt.Errorf("parse created tag: %w", err)
	}
	return t, nil
}

// DeleteTag removes a tag definition. Arr returns 200/204 on success, 404 if missing.
func (c *Client) DeleteTag(ctx context.Context, id int) error {
	resp, err := c.do(ctx, "DELETE", fmt.Sprintf("/api/v3/tag/%d", id), nil)
	if err != nil {
		return err
	}
	return unwrap(resp)
}

// Item is a movie (Radarr) or series (Sonarr) — the fields we need for
// tag previews and scan decisions. We unmarshal both endpoints into the
// same struct; Radarr populates MovieFile, Sonarr leaves it nil (series
// don't carry a single file — episodeFile lookup is per-episode and not
// needed for v1 scan, which is Radarr-first).
type Item struct {
	ID        int        `json:"id"`
	Title     string     `json:"title"`
	Year      int        `json:"year,omitempty"`    // release year — used for UI display, not decisions
	TmdbID    int        `json:"tmdbId,omitempty"`  // for cross-instance matching (M3e Secondary sync, Tag inventory cross-compare)
	TvdbID    int        `json:"tvdbId,omitempty"`  // Sonarr equivalent of TmdbID — same role for cross-Sonarr compares
	Tags      []int      `json:"tags"`
	MovieFile *MovieFile `json:"movieFile,omitempty"`
	// Sonarr-only — series statistics. Used by M-Sonarr Audio/Video
	// scan handlers to skip series with episodeFileCount==0 before
	// firing the per-series /api/v3/episodefile call (saves N requests
	// against an empty library). Nil on Radarr.
	Statistics *SeriesStatistics `json:"statistics,omitempty"`
}

// SeriesStatistics is the subset of Sonarr's series.statistics we read.
// Sonarr fills this in on every /api/v3/series response.
type SeriesStatistics struct {
	EpisodeFileCount int `json:"episodeFileCount,omitempty"`
	EpisodeCount     int `json:"episodeCount,omitempty"`
}

// MovieFile is the subset of Radarr's movieFile we need to identify a
// release for tag decisions. All three string fields can be empty —
// an un-imported movie has no file at all (MovieFile == nil), and an
// imported one may have relativePath + sceneName but blank releaseGroup
// when the release didn't carry one in the filename. Matches the
// inputs `engine.DecideTag` expects via `engine.MovieFile`.
//
// ID is needed for M3c Recover (PUT /api/v3/moviefile/{id}) and for the
// RenameFiles command. Kept omitempty for forward compat — endpoints
// not returning the field (none today, but possible in mocks) won't
// fail to parse.
type MovieFile struct {
	ID           int        `json:"id,omitempty"`
	RelativePath string     `json:"relativePath"`
	SceneName    string     `json:"sceneName"`
	ReleaseGroup string     `json:"releaseGroup"`
	// Path is the absolute path Radarr reports for this file (e.g.
	// "/movies/Foo (2024)/foo.mkv"). The value is from Radarr's
	// container's perspective — the tagarr DV-detail extraction
	// translates it through Instance.PathMappings before opening.
	// Always emitted by Radarr's API; absent on the older Sonarr
	// episodeFile shape (Sonarr scan-handler is deferred anyway).
	Path string `json:"path,omitempty"`
	// Size is the file size in bytes. Used together with ID as the
	// dvdetect cache key — same ID can reuse after re-import, but
	// the size will differ and self-invalidate the stale entry.
	Size      int64      `json:"size,omitempty"`
	MediaInfo *MediaInfo `json:"mediaInfo,omitempty"`
	Quality   *Quality   `json:"quality,omitempty"`
}

// MediaInfo is the subset of Radarr/Sonarr's mediaInfo struct that
// extra-tags consumes. The Arr already runs ffprobe at import-time
// and exposes the structured fields here — we never re-probe. Fields
// not relevant to the four extra-tag buckets (resolution, codec,
// audio, HDR) are omitted to keep the wire payload small.
//
// Some fields are absent for files imported before mediaInfo became
// reliable (Radarr v3 era). Callers must guard nil and individual
// zero-values; engine.ExtraTagsForFile handles missing data via the
// Quality.Quality.Resolution top-level fallback.
type MediaInfo struct {
	// Video
	Height                  int    `json:"height,omitempty"`                  // 1080, 2160, etc — pixel height
	VideoCodec              string `json:"videoCodec,omitempty"`              // "x264" | "x265" | "AV1" | "MPEG2" | "VC1"
	VideoBitDepth           int    `json:"videoBitDepth,omitempty"`           // 8 or 10
	VideoDynamicRangeType   string `json:"videoDynamicRangeType,omitempty"`   // "" | "HDR10" | "HDR10Plus" | "DV" | "DV HDR10" | "PQ"
	// Audio
	AudioCodec              string  `json:"audioCodec,omitempty"`              // "TrueHD" | "DTS-X" | "EAC3" | etc
	AudioChannels           float64 `json:"audioChannels,omitempty"`           // 2.0 | 5.1 | 7.1
	AudioAdditionalFeatures string  `json:"audioAdditionalFeatures,omitempty"` // contains "Atmos" when the track is Atmos-encoded
}

// Quality is the top-level quality envelope Radarr/Sonarr wraps around
// the file. We read .Quality.Resolution as a fallback when mediaInfo
// is missing — it's populated even on legacy imports.
type Quality struct {
	Quality QualityValue `json:"quality"`
}

// QualityValue carries the integer resolution alongside the quality
// label ("HDTV-1080p", "WEBDL-2160p" etc). Only Resolution is read.
type QualityValue struct {
	ID         int    `json:"id,omitempty"`
	Name       string `json:"name,omitempty"`
	Resolution int    `json:"resolution,omitempty"` // 480 | 720 | 1080 | 2160 | 0
}

// ListItems returns all movies (Radarr) or series (Sonarr), with their tag IDs.
// Used to build tag-operation previews without asking per-item.
func (c *Client) ListItems(ctx context.Context, arrType string) ([]Item, error) {
	var path string
	switch arrType {
	case "radarr":
		path = "/api/v3/movie"
	case "sonarr":
		path = "/api/v3/series"
	default:
		return nil, fmt.Errorf("unknown arr type: %s", arrType)
	}
	resp, err := c.do(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var items []Item
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return nil, fmt.Errorf("parse items: %w", err)
	}
	return items, nil
}

// GetItemTags reads just the tag-IDs of one movie/series. Used by the
// M-Webhook single-item adapters where ListItems would be O(library)
// per fire — a Sonarr whole-season pack triggers 24 events back-to-
// back, so a single 1500-row library walk per event scales poorly.
//
// Returns an empty slice when the item exists but carries no tags
// (Arr's API is consistent here — the Tags array is always present,
// just possibly empty). Returns a wrapped HTTP error on 4xx/5xx and
// a domain "not found" sentinel on 404 so callers can distinguish
// "item gone, nothing to do" from real server errors.
//
// arrType must be "radarr" or "sonarr"; itemID is the movie or series
// ID returned by the Arr's own listing endpoints.
func (c *Client) GetItemTags(ctx context.Context, arrType string, itemID int) ([]int, error) {
	var path string
	switch arrType {
	case "radarr":
		path = fmt.Sprintf("/api/v3/movie/%d", itemID)
	case "sonarr":
		path = fmt.Sprintf("/api/v3/series/%d", itemID)
	default:
		return nil, fmt.Errorf("unknown arr type: %s", arrType)
	}
	resp, err := c.do(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return nil, ErrItemNotFound
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	// Decode into the small subset we need — the full /api/v3/movie/{id}
	// payload includes ~50 fields (statistics, alternative titles,
	// addOptions, etc.) we don't care about here. Anonymous struct
	// keeps the API tight; full Item shape stays in ListItems.
	var row struct {
		Tags []int `json:"tags"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&row); err != nil {
		return nil, fmt.Errorf("parse tags: %w", err)
	}
	if row.Tags == nil {
		row.Tags = []int{}
	}
	return row.Tags, nil
}

// ErrItemNotFound is returned by GetItemTags when the Arr returns 404
// for the requested ID — typically because the user deleted the
// movie/series between Connect-event receive and dispatcher fire. The
// adapter that gets this error should treat the rule fire as a clean
// skip ("item no longer in library"), not an error.
var ErrItemNotFound = fmt.Errorf("arr: item not found")

// GetMovieByTmdbID looks up a Radarr movie by TMDb ID. Used by the
// M-Webhook Sync-to-secondary adapter to find the matching movie on
// the secondary instance without walking ListItems (O(library) → O(1)
// network round-trip). Radarr's `/api/v3/movie?tmdbId=N` returns a
// JSON array (one element on hit, empty on miss).
//
// Returns (item, true, nil) on hit, (zero, false, nil) on miss
// (movie not in this Radarr's library), and (zero, false, err) on
// network / 5xx errors.
func (c *Client) GetMovieByTmdbID(ctx context.Context, tmdbID int) (Item, bool, error) {
	if tmdbID == 0 {
		return Item{}, false, nil
	}
	path := fmt.Sprintf("/api/v3/movie?tmdbId=%d", tmdbID)
	resp, err := c.do(ctx, "GET", path, nil)
	if err != nil {
		return Item{}, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return Item{}, false, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var items []Item
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return Item{}, false, fmt.Errorf("parse items: %w", err)
	}
	if len(items) == 0 {
		return Item{}, false, nil
	}
	// Radarr never returns multiple movies for one TmdbID (TmdbID is
	// unique per library). If the array has more than one, pick the
	// first defensively + log to stderr — happens-never territory.
	return items[0], true, nil
}

// EditorApplyTags calls /api/v3/{movie|series}/editor with applyTags add/remove.
// arrType must be "radarr" or "sonarr"; action must be "add" or "remove".
// itemIDs are movie or series IDs depending on type.
func (c *Client) EditorApplyTags(ctx context.Context, arrType string, itemIDs []int, tagIDs []int, action string) error {
	if len(itemIDs) == 0 {
		return nil
	}
	var path, idField string
	switch arrType {
	case "radarr":
		path = "/api/v3/movie/editor"
		idField = "movieIds"
	case "sonarr":
		path = "/api/v3/series/editor"
		idField = "seriesIds"
	default:
		return fmt.Errorf("unknown arr type: %s", arrType)
	}
	if action != "add" && action != "remove" {
		return fmt.Errorf("action must be add or remove, got %q", action)
	}
	body := map[string]any{
		idField:     itemIDs,
		"tags":      tagIDs,
		"applyTags": action,
	}
	resp, err := c.do(ctx, "PUT", path, body)
	if err != nil {
		return err
	}
	return unwrap(resp)
}

// ============================================================================
// M3c Recover support — history walking + movieFile patching + rename command
// ============================================================================

// HistoryRecord is the subset of /api/v3/history/movie events the recover
// engine needs. The Radarr endpoint returns a richer object (per-event
// quality, languages, etc.); we capture only the fields engine.FindImportedGrabGroup
// reads, plus Date for sorting.
//
// Matches `engine.HistoryRecord` shape — caller converts at the boundary.
// Keeping arr-side and engine-side as separate types preserves the
// "engine has no I/O / no http" contract.
type HistoryRecord struct {
	EventType    string    `json:"eventType"`
	Date         time.Time `json:"date"`
	SourceTitle  string    `json:"sourceTitle"`
	DownloadID   string    `json:"downloadId"`
	// EpisodeID is Sonarr-only — present on history events that target a
	// specific episode (the typical case). Used by the per-epfile recover
	// flow to filter series-level history down to events that belong to
	// the file being patched.
	EpisodeID    int       `json:"episodeId,omitempty"`
	// Data is decoded once with both casings of releaseGroup since Radarr's
	// API has been seen returning either `releaseGroup` or `ReleaseGroup`
	// depending on event source. We coalesce in ReleaseGroup() below.
	Data struct {
		ReleaseGroupLower string `json:"releaseGroup"`
		ReleaseGroupTitle string `json:"ReleaseGroup"`
	} `json:"data"`
}

// ReleaseGroup returns the data.releaseGroup field, falling back to
// data.ReleaseGroup if the lowercase form is missing. Bash:
// `(.data.releaseGroup // .data.ReleaseGroup // "")`.
func (h HistoryRecord) ReleaseGroup() string {
	if h.Data.ReleaseGroupLower != "" {
		return h.Data.ReleaseGroupLower
	}
	return h.Data.ReleaseGroupTitle
}

// historyResponse handles both Radarr response shapes:
//   1. Bare array: `[ {...}, {...} ]`
//   2. Paginated object: `{ "records": [ {...} ], "page": 1, ... }`
// Bash uses jq's ternary `if type == "object" then .records else . end`.
// We do the same in Go: try paginated first, fall back to bare-array.
type historyResponse struct {
	Records []HistoryRecord `json:"records"`
}

// ListHistoryForMovie fetches grab + import history for a Radarr movie.
// Used by the recover engine to find the grab that produced the current
// file. Returns events in whatever order Radarr served — caller sorts.
//
// The endpoint accepts pagination; we don't pass it (Radarr defaults to
// returning everything for the movie, which is bounded — typically <50
// events even for a much-replaced library). If a future user hits a
// massive history we can add pageSize, but Radarr internally caps at 1000.
func (c *Client) ListHistoryForMovie(ctx context.Context, movieID int) ([]HistoryRecord, error) {
	path := fmt.Sprintf("/api/v3/history/movie?movieId=%d", movieID)
	resp, err := c.do(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	// Read body once so we can attempt both decode paths.
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read history: %w", err)
	}
	// Try paginated shape first. If records is non-nil OR the JSON is
	// shaped as an object, we keep that result. Otherwise fall through
	// to bare-array decode.
	trimmed := bytes.TrimLeft(body, " \t\r\n")
	if len(trimmed) > 0 && trimmed[0] == '{' {
		var paged historyResponse
		if err := json.Unmarshal(body, &paged); err != nil {
			return nil, fmt.Errorf("parse history (paginated): %w", err)
		}
		return paged.Records, nil
	}
	var records []HistoryRecord
	if err := json.Unmarshal(body, &records); err != nil {
		return nil, fmt.Errorf("parse history (array): %w", err)
	}
	return records, nil
}

// GetMovieFile fetches the full movieFile JSON. Radarr requires the
// complete object on PUT (sparse PATCH not supported), so recover-apply
// fetches → patches → PUTs.
//
// Returns the raw bytes; the caller patches the releaseGroup field via
// json.RawMessage manipulation, preserving every field Radarr expects to
// echo back unchanged.
func (c *Client) GetMovieFile(ctx context.Context, movieFileID int) ([]byte, error) {
	path := fmt.Sprintf("/api/v3/moviefile/%d", movieFileID)
	resp, err := c.do(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read movieFile: %w", err)
	}
	return body, nil
}

// UpdateMovieFileReleaseGroup patches the releaseGroup field on a
// movieFile and PUTs it back. Bash equivalent: jq update + curl PUT.
// Accepts the raw JSON from GetMovieFile and the new release-group string;
// returns nil on 200/202, an error otherwise.
//
// We use map[string]any for the patch instead of a typed struct so we
// preserve every field Radarr included in the response (audio/video
// streams, language tags, edition info, mediaInfo block, etc.). Sparse
// PUT would drop them.
func (c *Client) UpdateMovieFileReleaseGroup(ctx context.Context, movieFileID int, currentJSON []byte, newReleaseGroup string) error {
	var obj map[string]any
	if err := json.Unmarshal(currentJSON, &obj); err != nil {
		return fmt.Errorf("decode movieFile for patch: %w", err)
	}
	obj["releaseGroup"] = newReleaseGroup
	path := fmt.Sprintf("/api/v3/moviefile/%d", movieFileID)
	resp, err := c.do(ctx, "PUT", path, obj)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// Radarr returns 200 (preferred) or 202 on success.
	if resp.StatusCode == 200 || resp.StatusCode == 202 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	snippet := strings.TrimSpace(string(bodyBytes))
	if snippet == "" {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return fmt.Errorf("HTTP %d: %s", resp.StatusCode, snippet)
}

// ============================================================================
// Sonarr Recover support — episodefile listing/patching + per-series history
// ============================================================================

// EpisodeFile is the subset of /api/v3/episodefile/{id} we need for the
// Sonarr recover flow. Mirrors arr.MovieFile shape so the engine layer
// can treat both the same way (releaseGroup-driven recovery). Episodes
// is the list of episode IDs this file covers — Sonarr can have one
// file per episode (typical) or one file covering several (multi-ep
// releases like S01E05E06). Used to filter series-level history down
// to events relevant to THIS specific file when recovering.
type EpisodeFile struct {
	ID           int          `json:"id"`
	SeriesID     int          `json:"seriesId"`
	SeasonNumber int          `json:"seasonNumber"`
	RelativePath string       `json:"relativePath"`
	SceneName    string       `json:"sceneName"`
	ReleaseGroup string       `json:"releaseGroup"`
	Episodes     []EpisodeRef `json:"episodes,omitempty"`
	// Path is the absolute container-side path Sonarr reports for this
	// file. Mirrors MovieFile.Path; populated on every modern Sonarr.
	Path string `json:"path,omitempty"`
	// Size in bytes — same role as MovieFile.Size (cache-key candidate
	// if DV-detail ever lands for Sonarr; harmless to carry today).
	Size int64 `json:"size,omitempty"`
	// MediaInfo + Quality drive the M-Sonarr Audio/Video tag pipeline.
	// Same shape as MovieFile (Sonarr returns identical JSON keys); the
	// engine reads codec / channels / resolution / HDR per-episode and
	// AggregateForSeries collapses to series-level tags. Quality.Quality
	// .Resolution is the legacy-import fallback when MediaInfo is absent
	// — onedr0p's tag-resolution.sh uses it for the same reason.
	MediaInfo *MediaInfo `json:"mediaInfo,omitempty"`
	Quality   *Quality   `json:"quality,omitempty"`
}

// EpisodeRef is just the ID — that's all the Sonarr recover flow needs
// to filter series-history down to per-epfile events.
type EpisodeRef struct {
	ID int `json:"id"`
}

// ListEpisodefiles fetches every episodefile for the given Sonarr series.
// The endpoint accepts ?seriesId=N — Sonarr returns a flat array regardless
// of season/episode count.
func (c *Client) ListEpisodefiles(ctx context.Context, seriesID int) ([]EpisodeFile, error) {
	path := fmt.Sprintf("/api/v3/episodefile?seriesId=%d", seriesID)
	resp, err := c.do(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var out []EpisodeFile
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("parse episodefiles: %w", err)
	}
	return out, nil
}

// GetEpisodefile fetches one episodefile in full, used as the read leg of
// the read-modify-write recover patch. Same Radarr-parity reasoning as
// GetMovieFile — Sonarr requires the complete object on PUT, so we fetch
// → patch → PUT to preserve every field Sonarr expects.
func (c *Client) GetEpisodefile(ctx context.Context, episodefileID int) ([]byte, error) {
	path := fmt.Sprintf("/api/v3/episodefile/%d", episodefileID)
	resp, err := c.do(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// UpdateEpisodefileReleaseGroup mirrors UpdateMovieFileReleaseGroup but
// against Sonarr's /api/v3/episodefile/{id}. Same map[string]any patch
// pattern preserves every field (mediaInfo, language, custom-format
// matches) Sonarr included in the response.
func (c *Client) UpdateEpisodefileReleaseGroup(ctx context.Context, episodefileID int, currentJSON []byte, newReleaseGroup string) error {
	var obj map[string]any
	if err := json.Unmarshal(currentJSON, &obj); err != nil {
		return fmt.Errorf("decode episodefile for patch: %w", err)
	}
	obj["releaseGroup"] = newReleaseGroup
	path := fmt.Sprintf("/api/v3/episodefile/%d", episodefileID)
	resp, err := c.do(ctx, "PUT", path, obj)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 200 || resp.StatusCode == 202 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	snippet := strings.TrimSpace(string(bodyBytes))
	if snippet == "" {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return fmt.Errorf("HTTP %d: %s", resp.StatusCode, snippet)
}

// ListHistoryForSeries fetches grab + import events for a Sonarr series.
// One call covers the whole series — the bash _process_sonarr flow does
// the same and filters per-episode client-side. Same pagination handling
// as ListHistoryForMovie since Sonarr returns the same dual-shape
// response (bare array OR paginated `{records:[...]}` object).
func (c *Client) ListHistoryForSeries(ctx context.Context, seriesID int) ([]HistoryRecord, error) {
	path := fmt.Sprintf("/api/v3/history/series?seriesId=%d", seriesID)
	resp, err := c.do(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read history: %w", err)
	}
	trimmed := bytes.TrimLeft(body, " \t\r\n")
	if len(trimmed) > 0 && trimmed[0] == '{' {
		var paged historyResponse
		if err := json.Unmarshal(body, &paged); err != nil {
			return nil, fmt.Errorf("parse history (paginated): %w", err)
		}
		return paged.Records, nil
	}
	var records []HistoryRecord
	if err := json.Unmarshal(body, &records); err != nil {
		return nil, fmt.Errorf("parse history (array): %w", err)
	}
	return records, nil
}

// TriggerSonarrRenameFiles is the Sonarr equivalent of TriggerRadarrRenameFiles.
// Sonarr's command name is the same ("RenameFiles") but the body uses
// seriesId + files (an array of episodefile IDs) instead of movieId.
func (c *Client) TriggerSonarrRenameFiles(ctx context.Context, seriesID int, fileIDs []int) error {
	if len(fileIDs) == 0 {
		return nil
	}
	body := map[string]any{
		"name":     "RenameFiles",
		"seriesId": seriesID,
		"files":    fileIDs,
	}
	resp, err := c.do(ctx, "POST", "/api/v3/command", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 200 || resp.StatusCode == 201 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	snippet := strings.TrimSpace(string(bodyBytes))
	if snippet == "" {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return fmt.Errorf("HTTP %d: %s", resp.StatusCode, snippet)
}

// TriggerRadarrRenameFiles posts the RenameFiles command for a movie's
// files. Bash: curl POST /command with {"name":"RenameFiles","movieId":X,"files":[Y]}.
//
// The command is async on Radarr's side — Radarr queues a job and returns
// 200/201 immediately. Caller doesn't wait for completion.
func (c *Client) TriggerRadarrRenameFiles(ctx context.Context, movieID int, fileIDs []int) error {
	if len(fileIDs) == 0 {
		return nil
	}
	body := map[string]any{
		"name":    "RenameFiles",
		"movieId": movieID,
		"files":   fileIDs,
	}
	resp, err := c.do(ctx, "POST", "/api/v3/command", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 200 || resp.StatusCode == 201 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	snippet := strings.TrimSpace(string(bodyBytes))
	if snippet == "" {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return fmt.Errorf("HTTP %d: %s", resp.StatusCode, snippet)
}
