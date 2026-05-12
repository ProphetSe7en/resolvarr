package arr

// downloadclients.go — Radarr/Sonarr `/api/v3/downloadclient` reader.
//
// Used by the M-Webhook qBit Category Fix function: when a Sonarr/Radarr
// Import event fires we need to know which qBit category Arr expected
// to set before the import (pre-import) and after the import (post-
// import). These names live in the Arr's download-client config and
// the user already typed them once when adding the qBit client to
// Sonarr/Radarr — we surface them here so the resolvarr UI never asks
// for category names by hand.
//
// Field-naming quirk: Radarr's qBittorrent client uses fields named
// `movieCategory` + `movieImportedCategory`; Sonarr uses `tvCategory` +
// `tvImportedCategory`. The QbitPreImportCategory / QbitPostImportCategory
// helpers unify both with an appType discriminator so callers don't
// have to special-case per Arr.
//
// Scope: read-only listing. Writing / updating download clients is the
// user's job through Sonarr/Radarr's own UI — we never mutate this
// surface. SSRF + body bounds inherit from the shared arr.Client.do
// path so no extra hardening needed here.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

// ArrDownloadClient is the subset of /api/v3/downloadclient we need for
// the qBit category-fix function. Fields[] is a free-form key/value
// bag — Radarr/Sonarr serialise each implementation's settings here so
// one endpoint covers qBit / sabnzbd / nzbget / deluge / etc. Helpers
// below pull the typed qBit-relevant fields out of the bag.
//
// Implementation values seen in the wild:
//   - "QBittorrent" (Radarr + Sonarr — what we filter on)
//   - "SABnzbd", "NZBGet", "Deluge", "Transmission" etc. — ignored by
//     the category-fix function (no category model maps cleanly)
//
// Protocol is the radarr/sonarr classification ("torrent" / "usenet").
// We carry it through for the frontend to display alongside the row
// when there are multiple clients of the same implementation.
type ArrDownloadClient struct {
	ID                 int                      `json:"id"`
	Name               string                   `json:"name"`
	Implementation     string                   `json:"implementation"`
	ImplementationName string                   `json:"implementationName,omitempty"`
	Enable             bool                     `json:"enable"`
	Protocol           string                   `json:"protocol,omitempty"`
	Fields             []ArrDownloadClientField `json:"fields"`
}

// ArrDownloadClientField is one entry in the Fields[] bag. Value is
// `interface{}` because the Arr returns string / int / bool depending
// on the field's type — the typed-getter helpers (StringField /
// IntField) coerce defensively.
type ArrDownloadClientField struct {
	Name  string      `json:"name"`
	Value interface{} `json:"value,omitempty"`
}

// StringField returns the named field's value as a string. Empty
// string when the field is missing OR the value isn't a string. Coerces
// numeric values to their decimal representation so a field stored as
// JSON number (`category: 123`) still comes back as a non-empty string
// — defensive, the typical case is the value already being a string.
func (c *ArrDownloadClient) StringField(name string) string {
	for i := range c.Fields {
		if c.Fields[i].Name != name {
			continue
		}
		switch v := c.Fields[i].Value.(type) {
		case string:
			return v
		case float64:
			// JSON numbers decode to float64 by default. Render
			// integer-valued floats without trailing zeros.
			if v == float64(int64(v)) {
				return fmt.Sprintf("%d", int64(v))
			}
			return fmt.Sprintf("%g", v)
		case bool:
			if v {
				return "true"
			}
			return "false"
		}
		return ""
	}
	return ""
}

// IntField returns the named field's value as an int. Zero when the
// field is missing OR the value can't be coerced. Same defensive
// posture as StringField — float64 is the common JSON-decode shape
// for numeric values, so we coerce that explicitly.
func (c *ArrDownloadClient) IntField(name string) int {
	for i := range c.Fields {
		if c.Fields[i].Name != name {
			continue
		}
		switch v := c.Fields[i].Value.(type) {
		case float64:
			return int(v)
		case int:
			return v
		case int64:
			return int(v)
		}
		return 0
	}
	return 0
}

// QbitPreImportCategory returns the category Sonarr/Radarr applies to
// the torrent before import completes. Radarr names this field
// `movieCategory`; Sonarr names it `tvCategory`. Defaults fall through
// to `category` for forward-compat against future Arr field renames.
//
// appType must be "radarr" or "sonarr"; anything else returns the
// generic `category` value as a last resort (older Arr versions
// occasionally exposed this as the unified field name).
func (c *ArrDownloadClient) QbitPreImportCategory(appType string) string {
	switch appType {
	case "radarr":
		if v := c.StringField("movieCategory"); v != "" {
			return v
		}
	case "sonarr":
		if v := c.StringField("tvCategory"); v != "" {
			return v
		}
	}
	return c.StringField("category")
}

// QbitPostImportCategory returns the category Sonarr/Radarr changes the
// torrent to after a successful import. Radarr names this field
// `movieImportedCategory`; Sonarr names it `tvImportedCategory`.
// Defaults fall through to `importedCategory` for the same forward-
// compat reason as QbitPreImportCategory.
//
// Empty string when the user hasn't configured a post-import category
// — many users only set the pre-import category and let qBit auto-
// move based on label/category rules instead. The category-fix
// function treats empty post-import as "rule mis-configured, skip
// at save-time" rather than firing a meaningless rename.
func (c *ArrDownloadClient) QbitPostImportCategory(appType string) string {
	switch appType {
	case "radarr":
		if v := c.StringField("movieImportedCategory"); v != "" {
			return v
		}
	case "sonarr":
		if v := c.StringField("tvImportedCategory"); v != "" {
			return v
		}
	}
	return c.StringField("importedCategory")
}

// ListDownloadClients fetches every configured download client on the
// Arr instance. Returns all implementations (qBit / nzbget / deluge /
// etc.) — the caller filters by Implementation as needed.
//
// Uses arr.Client.do for transport: SSRF defence, debug logging, and
// 401-aware error wrapping inherit automatically. Body decode is
// unbounded because download-client lists are small (typical user has
// 1-4 clients) and the endpoint is auth-gated by the Arr's own
// X-Api-Key header check.
func (c *Client) ListDownloadClients(ctx context.Context) ([]ArrDownloadClient, error) {
	resp, err := c.do(ctx, "GET", "/api/v3/downloadclient", nil)
	if err != nil {
		return nil, fmt.Errorf("list download clients: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 {
		return nil, fmt.Errorf("list download clients: unauthorized — check API key")
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("list download clients: HTTP %d", resp.StatusCode)
	}
	var out []ArrDownloadClient
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("parse download clients: %w", err)
	}
	return out, nil
}

// ListHistoryByDownloadID fetches every history event for one download
// ID across the Arr's whole history. Used by the qBit Category Fix
// function to verify an Import event is real (the corresponding
// "downloadFolderImported" / "episodeFileImported" entry must exist
// in Arr's history before we touch qBit).
//
// Single round-trip — Radarr + Sonarr both honour the `downloadId`
// query parameter on `/api/v3/history`. pageSize=50 covers the worst
// case (a torrent that's been grabbed / imported / rejected / re-
// grabbed many times) without unbounded pagination work.
//
// Returns events in whatever order the Arr served (typically newest-
// first); caller filters / picks the most relevant event via
// FindImportedEvent below.
func (c *Client) ListHistoryByDownloadID(ctx context.Context, downloadID string) ([]HistoryRecord, error) {
	if downloadID == "" {
		return nil, fmt.Errorf("downloadId is required")
	}
	// URL-encode downloadID — it comes from the Connect event payload (Arr-
	// influenced) and could contain `&`, `#`, `?`, `%`, or unicode that would
	// corrupt the raw query string. QueryEscape handles every byte safely.
	path := "/api/v3/history?downloadId=" + url.QueryEscape(downloadID) + "&pageSize=50"
	resp, err := c.do(ctx, "GET", path, nil)
	if err != nil {
		return nil, fmt.Errorf("list history by downloadId: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 {
		return nil, fmt.Errorf("list history by downloadId: unauthorized — check API key")
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("list history by downloadId: HTTP %d", resp.StatusCode)
	}
	// /api/v3/history always returns the paginated shape (records:[...])
	// when called against the root endpoint (the per-movie / per-series
	// variants can return bare arrays, but the generic /history doesn't).
	// Be defensive against future Arr changes — try paginated first, fall
	// back to bare-array.
	var paged historyResponse
	if err := json.NewDecoder(resp.Body).Decode(&paged); err == nil && paged.Records != nil {
		return paged.Records, nil
	}
	// Fallback decode would need re-reading the body — skip the dance
	// and just return empty when the paginated decode misses (the
	// observed reality on every Radarr/Sonarr version is paginated
	// shape on this endpoint).
	return paged.Records, nil
}

// FindImportedEvent walks the history records and returns the most-
// recent event that confirms an import completed. Radarr emits
// "downloadFolderImported" once per finished import; Sonarr emits
// "episodeFileImported" once per imported episode file.
//
// Returns nil when no matching event is present — caller treats nil
// as "Arr's history doesn't confirm an import yet, skip the qBit
// touch". Records are scanned in order; we return a pointer to the
// first match (which is typically the newest given the Arr's typical
// newest-first sort, but we don't re-sort to avoid surprises).
//
// appType must be "radarr" or "sonarr"; anything else returns nil
// (defensive — symmetric with extractDownload's unknown-appType
// fall-through).
func FindImportedEvent(records []HistoryRecord, appType string) *HistoryRecord {
	var target string
	switch appType {
	case "radarr":
		target = "downloadFolderImported"
	case "sonarr":
		target = "episodeFileImported"
	default:
		return nil
	}
	for i := range records {
		if records[i].EventType == target {
			return &records[i]
		}
	}
	return nil
}
