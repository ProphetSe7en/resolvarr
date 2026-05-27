// Package plex is resolvarr's Plex Media Server API client.
//
// Scope: just enough to support the Plex label-sync feature (label sync
// from Radarr/Sonarr tags). Five operations:
//
//   - Ping            — validate URL + token + server reachability
//   - GetLibraries    — list movie + show libraries on the server
//   - GetItems        — fetch all items in a library with their
//                       GUIDs + current labels
//   - AddLabel        — apply one label to one item
//   - RemoveLabel     — remove one label from one item
//
// Read-only metadata (posters, ratings, watch state, file paths) is
// deliberately out of scope — resolvarr doesn't render Plex content,
// it only mutates labels.
//
// URL handling: the Client is constructed with a BASE URL that
// includes whatever subpath the user configured (direct
// `http://plex.lan:32400` or reverse-proxied `https://plex.example.com`).
// Trailing slashes on the base URL are stripped.
//
// Auth model: X-Plex-Token header on every request. Token is obtained
// by the user from Plex Web (Settings → Account → "Show advanced" →
// X-Plex-Token in browser DevTools network tab) and stored in
// resolvarr's PlexInstance config. Masked in API responses per
// security baseline §15. No login flow — token is long-lived.
//
// TLS: the Client honours a TrustedCerts flag — when true it skips
// certificate verification. Off by default (verify enabled). The user
// explicitly opts in for self-signed setups. Same pattern as the qbit
// client.
//
// Network model: Plex usually runs on the same LAN as resolvarr
// (RFC1918). Plain http.Client used by design — wrapping in
// SafeHTTPClient would reject LAN destinations unless allowlisted.
// Matches the architectural decision documented in core/app.go for
// LAN-bound Arr + qBit clients.
package plex

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client talks to a single Plex Media Server. Goroutine-safe — the
// underlying http.Client supports concurrent use. Construct via New;
// don't instantiate the struct directly.
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// Config carries the per-instance settings the user configures via
// resolvarr's Settings → Plex instances UI.
type Config struct {
	URL          string // e.g. "http://plex.lan:32400" or "https://plex.example.com" (trailing slash stripped)
	Token        string // X-Plex-Token — long-lived, user-supplied
	TrustedCerts bool   // skip TLS verification — opt-in for self-signed (rare on Plex; usually plaintext-LAN)
	Timeout      time.Duration // optional; 30s default if zero
}

// New constructs a Client. Validates URL parseability + token presence
// at construction time so callers get an immediate error rather than
// a deferred failure on the first API call.
func New(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.Token) == "" {
		return nil, fmt.Errorf("plex: token is required")
	}
	base, err := normaliseBaseURL(cfg.URL)
	if err != nil {
		return nil, err
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	httpClient := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				// User explicitly opts in via the per-instance
				// TrustedCerts flag — self-signed Plex setups
				// are uncommon (Plex usually serves plaintext on
				// LAN or termination at reverse proxy) but
				// supported. Gated behind admin toggle, not a
				// default. #nosec G402
				InsecureSkipVerify: cfg.TrustedCerts, // #nosec G402 -- opt-in for self-signed LAN Plex
			},
			MaxIdleConns:        4,
			IdleConnTimeout:     30 * time.Second,
			TLSHandshakeTimeout: 10 * time.Second,
		},
	}
	return &Client{
		baseURL:    base,
		token:      strings.TrimSpace(cfg.Token),
		httpClient: httpClient,
	}, nil
}

// normaliseBaseURL parses the user-supplied URL, validates scheme +
// host, and returns a canonical form with trailing slash stripped.
// Subpaths (for reverse-proxy deployments) are preserved.
func normaliseBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("plex: URL is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("plex: parse URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("plex: URL must be http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("plex: URL must include host")
	}
	return strings.TrimRight(u.String(), "/"), nil
}

// Ping validates the connection by hitting /identity (a cheap public
// endpoint that confirms "URL is correct, token is valid, server is
// up"). Returns the server's friendly name on success — used by the
// Settings UI to confirm the connection saved correctly.
func (c *Client) Ping(ctx context.Context) (friendlyName string, err error) {
	var resp identityResponse
	if err := c.doJSON(ctx, http.MethodGet, "/identity", nil, &resp); err != nil {
		return "", err
	}
	return resp.MediaContainer.FriendlyName, nil
}

// GetLibraries returns all libraries on the server. The Settings UI
// uses this to populate the library picker after the user saves a
// Plex instance.
func (c *Client) GetLibraries(ctx context.Context) ([]Library, error) {
	var resp librariesResponse
	if err := c.doJSON(ctx, http.MethodGet, "/library/sections", nil, &resp); err != nil {
		return nil, err
	}
	out := make([]Library, 0, len(resp.MediaContainer.Directory))
	for _, d := range resp.MediaContainer.Directory {
		out = append(out, Library{
			Key:   d.Key,
			Title: d.Title,
			Type:  d.Type,
		})
	}
	return out, nil
}

// GetItems lists every item in the given library section with the
// minimal metadata the sync needs (GUIDs + Labels). The Plex API
// returns the FULL Metadata block including posters, ratings, etc. —
// we surgically decode just what we need to keep allocation light on
// large libraries.
//
// For Sonarr (show libraries), this returns SERIES items only — Plex's
// /library/sections/{key}/all on a show library yields shows, not
// seasons or episodes. Series-only is intentional: per-season +
// per-episode label sync is out of scope.
func (c *Client) GetItems(ctx context.Context, libraryKey string) ([]Item, error) {
	if strings.TrimSpace(libraryKey) == "" {
		return nil, fmt.Errorf("plex: library key required")
	}
	var resp itemsResponse
	// includeMeta=1 + checkFiles=0 + includeChildren=0 — defensive
	// parameter set asking Plex to include the full per-item metadata
	// block (Label[], Genre[], Country[], etc.) without the per-file
	// metadata or per-episode children. Without includeMeta on some
	// Plex Server versions the Label array is omitted from the
	// response — items appear unlabelled and the engine would treat
	// every Arr-tagged item as "needs to add the label" even when
	// Plex already carries it. includeChildren=0 keeps episode/season
	// expansion off for show libraries (we only label series-level
	// per the design).
	path := fmt.Sprintf("/library/sections/%s/all?includeMeta=1&includeChildren=0&checkFiles=0",
		url.PathEscape(libraryKey))
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}
	out := make([]Item, 0, len(resp.MediaContainer.Metadata))
	for _, m := range resp.MediaContainer.Metadata {
		guids := make([]string, 0, len(m.Guid))
		for _, g := range m.Guid {
			guids = append(guids, g.ID)
		}
		labels := make([]string, 0, len(m.Label))
		for _, l := range m.Label {
			// Trim defensively — Plex normally returns clean strings
			// but trailing whitespace on a label would silently break
			// the case-insensitive match downstream (lower("FEL ") !=
			// lower("FEL")).
			tag := strings.TrimSpace(l.Tag)
			if tag != "" {
				labels = append(labels, tag)
			}
		}
		collections := make([]string, 0, len(m.Collection))
		for _, c := range m.Collection {
			tag := strings.TrimSpace(c.Tag)
			if tag != "" {
				collections = append(collections, tag)
			}
		}
		out = append(out, Item{
			RatingKey:   m.RatingKey,
			Title:       m.Title,
			Year:        m.Year,
			Type:        m.Type,
			GUIDs:       guids,
			Labels:      labels,
			Collections: collections,
		})
	}
	return out, nil
}

// FetchRawItemMetadata returns the literal JSON response body from
// /library/metadata/{ratingKey} — for diagnostic purposes only.
// Lets the inspect endpoint surface exactly what Plex sends so a
// JSON-shape mismatch in our typed decoder is visible at first
// glance. Capped at 64KB to keep the response sane.
func (c *Client) FetchRawItemMetadata(ctx context.Context, ratingKey string) ([]byte, error) {
	if strings.TrimSpace(ratingKey) == "" {
		return nil, fmt.Errorf("plex: rating key required")
	}
	fullURL := c.baseURL + fmt.Sprintf("/library/metadata/%s?includeMeta=1", url.PathEscape(ratingKey))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Plex-Token", c.token)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer drainAndClose(resp)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("plex: HTTP %d on /library/metadata/%s", resp.StatusCode, ratingKey)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64*1024))
}

// GetItemMetadata fetches the full per-item metadata for one
// ratingKey. Used to retrieve Label[] for items that came back from
// /library/sections/{key}/all without it — some Plex Server versions
// omit labels from the bulk list endpoint regardless of query
// params, and per-item /library/metadata/{ratingKey} is the only
// reliable way to read them. plexapi takes this same approach.
//
// Returns the populated Item (same shape as GetItems returns), or
// the zero value + error on miss / fetch failure. Caller decides
// whether to fall back to the bulk-fetched labels (typically empty)
// when this fails.
func (c *Client) GetItemMetadata(ctx context.Context, ratingKey string) (Item, error) {
	if strings.TrimSpace(ratingKey) == "" {
		return Item{}, fmt.Errorf("plex: rating key required")
	}
	var resp itemsResponse
	path := fmt.Sprintf("/library/metadata/%s?includeMeta=1", url.PathEscape(ratingKey))
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return Item{}, err
	}
	if len(resp.MediaContainer.Metadata) == 0 {
		return Item{}, fmt.Errorf("plex: no metadata returned for ratingKey %s", ratingKey)
	}
	m := resp.MediaContainer.Metadata[0]
	guids := make([]string, 0, len(m.Guid))
	for _, g := range m.Guid {
		guids = append(guids, g.ID)
	}
	labels := make([]string, 0, len(m.Label))
	for _, l := range m.Label {
		tag := strings.TrimSpace(l.Tag)
		if tag != "" {
			labels = append(labels, tag)
		}
	}
	collections := make([]string, 0, len(m.Collection))
	for _, c := range m.Collection {
		tag := strings.TrimSpace(c.Tag)
		if tag != "" {
			collections = append(collections, tag)
		}
	}
	return Item{
		RatingKey:   m.RatingKey,
		Title:       m.Title,
		Year:        m.Year,
		Type:        m.Type,
		GUIDs:       guids,
		Labels:      labels,
		Collections: collections,
	}, nil
}

// AddLabel applies one label to one item.
//
// Plex's label-update endpoint is PUT /library/sections/{sectionKey}/all
// with query params:
//
//	type           — numeric library-item type (1=movie, 2=show, ...)
//	id             — the item's ratingKey
//	label[0].tag.tag={label}  — add the label
//	label.locked=1 — lock the label field so Plex doesn't overwrite
//	                  it on the next metadata refresh
//
// Returns nil on HTTP 200. The endpoint is idempotent — re-applying
// an existing label is a no-op (no error).
func (c *Client) AddLabel(ctx context.Context, libraryKey, ratingKey, itemType, label string) error {
	return c.updateTag(ctx, libraryKey, ratingKey, itemType, label, "label", false)
}

// RemoveLabel removes one label from one item. Same endpoint as
// AddLabel but with the `label[].tag.tag-=` (note the trailing dash)
// query-param syntax — Plex's "subtract from collection" notation.
//
// Idempotent — removing a label that doesn't exist returns nil.
func (c *Client) RemoveLabel(ctx context.Context, libraryKey, ratingKey, itemType, label string) error {
	return c.updateTag(ctx, libraryKey, ratingKey, itemType, label, "label", true)
}

// AddCollection adds the item to one Plex Collection. Same endpoint
// as AddLabel but uses the `collection[0].tag.tag=X` query-param
// shape. Plex Collections are a separate metadata concept from
// Labels — visible in Plex Web as proper grouped views ("MEL
// Edition Movies", "Reference Audio Movies", etc.). Many users
// prefer Collections for taxonomic groupings; Labels for ad-hoc
// tagging. Engine targets one or the other per rule.
//
// Idempotent — re-adding a collection membership is a no-op.
func (c *Client) AddCollection(ctx context.Context, libraryKey, ratingKey, itemType, collection string) error {
	return c.updateTag(ctx, libraryKey, ratingKey, itemType, collection, "collection", false)
}

// RemoveCollection removes the item from one Plex Collection. Uses
// the `collection[].tag.tag-=X` trailing-dash subtract syntax.
//
// Idempotent — removing a non-existent collection membership is a
// no-op.
func (c *Client) RemoveCollection(ctx context.Context, libraryKey, ratingKey, itemType, collection string) error {
	return c.updateTag(ctx, libraryKey, ratingKey, itemType, collection, "collection", true)
}

// updateTag is the shared implementation for Add/Remove on both
// labels and collections. Plex's tag-update endpoint takes the same
// URL + auth + type+id triple regardless of which collection-type
// you're touching; only the query-param prefix changes ("label" vs
// "collection") and the matching ".locked" suffix that prevents the
// metadata agent from undoing our write on the next refresh.
//
// tagKind must be "label" or "collection"; any other value returns
// an unsupported-type error.
func (c *Client) updateTag(ctx context.Context, libraryKey, ratingKey, itemType, tagValue, tagKind string, remove bool) error {
	if strings.TrimSpace(libraryKey) == "" {
		return fmt.Errorf("plex: library key required")
	}
	if strings.TrimSpace(ratingKey) == "" {
		return fmt.Errorf("plex: rating key required")
	}
	typeCode := itemTypeCode(itemType)
	if typeCode == 0 {
		return fmt.Errorf("plex: unsupported item type %q (want movie or show)", itemType)
	}
	if strings.TrimSpace(tagValue) == "" {
		return fmt.Errorf("plex: %s cannot be empty", tagKind)
	}
	if tagKind != "label" && tagKind != "collection" {
		return fmt.Errorf("plex: unsupported tag kind %q (want label or collection)", tagKind)
	}

	// Build the form-encoded query. The add path uses the
	// `[0].tag.tag` array-element notation; the remove path uses
	// `[].tag.tag-` (trailing dash) per Plex's undocumented but
	// stable subtract-from-collection notation. The `.locked=1` flag
	// prevents Plex's metadata agents from overwriting our write on
	// the next refresh.
	tagParamKey := tagKind + "[0].tag.tag"
	if remove {
		tagParamKey = tagKind + "[].tag.tag-"
	}
	params := url.Values{}
	params.Set("type", strconv.Itoa(typeCode))
	params.Set("id", ratingKey)
	params.Set(tagParamKey, tagValue)
	params.Set(tagKind+".locked", "1")

	path := fmt.Sprintf("/library/sections/%s/all?%s",
		url.PathEscape(libraryKey),
		params.Encode())

	// PUT with empty body — Plex reads everything from the query string.
	return c.doJSON(ctx, http.MethodPut, path, nil, nil)
}

// doJSON is the single HTTP entry-point. Handles:
//   - URL composition (base + path)
//   - X-Plex-Token header
//   - Accept: application/json (Plex defaults to XML otherwise)
//   - Optional request body (json-encoded)
//   - 4xx/5xx error surfacing
//   - Optional response body (decoded into target)
//
// Passing nil target skips response decoding (for label-mutation
// endpoints that return empty body).
func (c *Client) doJSON(ctx context.Context, method, path string, body, target any) error {
	var reqBody io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("plex: encode request body: %w", err)
		}
		reqBody = strings.NewReader(string(buf))
	}

	fullURL := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, fullURL, reqBody)
	if err != nil {
		return fmt.Errorf("plex: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Plex-Token", c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("plex: %s %s: %w", method, path, err)
	}
	defer drainAndClose(resp)

	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("plex: 401 unauthorized — check X-Plex-Token")
	}
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("plex: 404 not found at %s", path)
	}
	if resp.StatusCode >= 400 {
		// Surface server-side error body when available — Plex sends
		// useful error messages in plain text for some endpoints.
		excerpt, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("plex: HTTP %d at %s: %s",
			resp.StatusCode, path, strings.TrimSpace(string(excerpt)))
	}
	if target == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		return fmt.Errorf("plex: decode response from %s: %w", path, err)
	}
	return nil
}

// drainAndClose drains the response body and closes it. Matches the
// pattern in internal/core/agents/* — keeps the connection pool healthy
// by ensuring keepalive can re-use connections.
func drainAndClose(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}
