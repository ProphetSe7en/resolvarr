// Package qbit is resolvarr's qBittorrent WebUI API client.
//
// Scope: just enough to support the M-Webhook qBit-touching functions
// (S/E tag on Sonarr Grab + qBit grab rename + the future backlog
// scan). Keep the surface small — login, list torrents, add tags,
// rename torrent. Read-only metadata queries (transfer/info etc.)
// are out of scope.
//
// Auth model: cookie-session login (POST /api/v2/auth/login →
// `SID=...` Set-Cookie header). Cached per-Client. On a 401 we
// re-login once and retry the request — covers the typical case
// where qBit's session expired between resolvarr's last fire and
// now. Repeated 401s past the retry get bubbled up so callers
// can surface the error.
//
// URL handling: the Client is constructed with a BASE URL that
// includes whatever subpath the user configured (direct
// `http://192.168.1.100:8080` or reverse-proxied
// `https://qbit.example.com/qbit`). All API paths are appended
// to this base via path-join so subpath-prefixed deployments
// behind nginx / traefik / swag / Cloudflare tunnels work
// transparently. Trailing slashes on the base URL are stripped.
//
// TLS: the Client honours a TrustedCerts flag — when true it
// skips certificate verification. Off by default (verify enabled).
// The user explicitly opts in for self-signed setups.
package qbit

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Config is the per-instance configuration the Client needs to talk
// to one qBit. Mirrors the user-visible fields in Settings → qBit
// instances. Username + Password may be empty for setups with
// LocalHostAuth disabled (qBit's localhost-no-auth bypass); the
// login flow short-circuits on empty creds.
type Config struct {
	URL          string
	Username     string
	Password     string
	TrustedCerts bool
}

// Client talks to one qBit instance. Safe for concurrent use; the
// session cookie is mutex-guarded. Construct once per QbitInstance,
// re-use across handler calls.
//
// Retry policy is conservative: a single re-login attempt on 401.
// We don't retry network errors (connection refused / DNS / etc.) —
// those bubble up immediately so the UI can surface them rather
// than silently spinning.
type Client struct {
	cfg     Config
	baseURL string // normalised: trailing slash stripped, scheme verified
	http    *http.Client

	// proxyToken is the qui /proxy/<token> secret extracted from baseURL,
	// or "" for direct/reverse-proxy URLs. Held so scrubErr can redact it
	// from error strings: Go's net/http wraps failures in a *url.Error
	// that embeds the full request URL (token included), which would
	// otherwise leak the secret into the UI's status/test messages.
	proxyToken string

	mu          sync.Mutex
	cookieReady bool // login already attempted + cookie set in jar
}

// proxyTokenRE extracts a qui /proxy/<hex> token from a base URL.
var proxyTokenRE = regexp.MustCompile(`(?i)/proxy/([a-f0-9]{16,})`)

// scrubErr redacts this client's qui proxy token from an error message so
// it never reaches the UI in plaintext. Returns the original error when
// there's no token or the token isn't present (preserves wrapping).
func (c *Client) scrubErr(err error) error {
	if err == nil || c.proxyToken == "" {
		return err
	}
	msg := err.Error()
	if !strings.Contains(msg, c.proxyToken) {
		return err
	}
	return errors.New(strings.ReplaceAll(msg, c.proxyToken, "<redacted>"))
}

// New constructs a Client. Validates the URL up front so callers
// don't see opaque errors deep in the request path. Returns the
// Client + nil error on success.
func New(cfg Config) (*Client, error) {
	base, err := normaliseBaseURL(cfg.URL)
	if err != nil {
		return nil, err
	}
	jar, _ := cookiejar.New(nil)
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
		Jar:     jar,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				// User explicitly opts in via the per-instance
				// TrustedCerts flag — self-signed qBit setups are
				// common on homelab LAN. Gated behind an admin
				// toggle, not a default. #nosec G402
				InsecureSkipVerify: cfg.TrustedCerts, // #nosec G402 -- opt-in for self-signed LAN qBit
			},
			// Sane defaults — qBit is fast + local; long timeouts
			// just hide bugs.
			MaxIdleConns:        4,
			IdleConnTimeout:     30 * time.Second,
			TLSHandshakeTimeout: 10 * time.Second,
		},
	}
	var token string
	if m := proxyTokenRE.FindStringSubmatch(base); len(m) == 2 {
		token = m[1]
	}
	return &Client{
		cfg:        cfg,
		baseURL:    base,
		http:       httpClient,
		proxyToken: token,
	}, nil
}

// normaliseBaseURL strips trailing slashes and verifies the URL
// has an http(s) scheme + host. Subpath segments are preserved —
// `https://example.com/qbit/` becomes `https://example.com/qbit`.
func normaliseBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("qbit URL is empty")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid qbit URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("qbit URL scheme must be http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return "", errors.New("qbit URL missing host")
	}
	// Drop any query / fragment — they don't make sense on a base.
	u.RawQuery = ""
	u.Fragment = ""
	out := u.String()
	out = strings.TrimRight(out, "/")
	return out, nil
}

// apiURL composes a full request URL by joining the configured
// base URL with the API path. Path must start with "/" (e.g.
// "/api/v2/auth/login"). For reverse-proxy setups where the user's
// base URL already has a subpath, this preserves it correctly:
//
//	base = https://example.com/qbit
//	path = /api/v2/auth/login
//	→     https://example.com/qbit/api/v2/auth/login
func (c *Client) apiURL(path string) string {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return c.baseURL + path
}

// Login authenticates against qBit and caches the session cookie
// in the client's jar. Idempotent on success — subsequent calls
// short-circuit if a cookie is already present. Callers don't
// usually need to call this directly; Do() handles login lazily.
func (c *Client) Login(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.loginLocked(ctx)
}

// loginLocked is the internal login implementation. Caller must
// hold c.mu. Resets cookieReady on failure so a retry can start
// fresh.
func (c *Client) loginLocked(ctx context.Context) error {
	// LocalHostAuth-disabled setups: empty creds + no need to login.
	// qBit returns 200 OK on every API call without a session in
	// that mode. We mark cookieReady so subsequent calls skip the
	// login path.
	if c.cfg.Username == "" && c.cfg.Password == "" {
		c.cookieReady = true
		return nil
	}

	form := url.Values{}
	form.Set("username", c.cfg.Username)
	form.Set("password", c.cfg.Password)

	req, err := http.NewRequestWithContext(ctx, "POST",
		c.apiURL("/api/v2/auth/login"),
		strings.NewReader(form.Encode()))
	if err != nil {
		c.cookieReady = false
		return fmt.Errorf("build login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// qBit checks Referer for CSRF — set it to the base URL to satisfy.
	req.Header.Set("Referer", c.baseURL)

	resp, err := c.http.Do(req)
	if err != nil {
		c.cookieReady = false
		// Scrub the qui token from the transport error before it surfaces.
		return fmt.Errorf("login request failed: %w", c.scrubErr(err))
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	bodyStr := strings.TrimSpace(string(body))

	if resp.StatusCode == 403 {
		// qBit returns 403 when auth is locked due to too many
		// failures (default: 1 hour ban after N bad attempts).
		c.cookieReady = false
		return errors.New("qbit login forbidden — auth might be temporarily banned (qBit's 'too many failed attempts' lockout)")
	}
	if resp.StatusCode != 200 {
		c.cookieReady = false
		return fmt.Errorf("qbit login HTTP %d: %s", resp.StatusCode, bodyStr)
	}
	// qBit returns the literal text "Ok." on success and "Fails."
	// on bad credentials — both with HTTP 200, distinguish by body.
	if bodyStr != "Ok." {
		c.cookieReady = false
		return fmt.Errorf("qbit login rejected: %s", bodyStr)
	}
	c.cookieReady = true
	return nil
}

// Do issues a request to qBit's API path with auto-login + 401-retry.
// Caller passes the API path (starting with "/") and an optional
// body (form-encoded or JSON; caller sets Content-Type via setHeader).
// On a 401 the cookie is cleared, login retried once, request
// re-issued; further 401s bubble up.
func (c *Client) Do(ctx context.Context, method, path string, body io.Reader, setHeader func(http.Header)) (*http.Response, error) {
	// Lazy login so the caller doesn't have to remember to Login()
	// before every request.
	c.mu.Lock()
	if !c.cookieReady {
		if err := c.loginLocked(ctx); err != nil {
			c.mu.Unlock()
			return nil, err
		}
	}
	c.mu.Unlock()

	resp, err := c.doOnce(ctx, method, path, body, setHeader)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 401 {
		return resp, nil
	}
	// 401 — session expired or invalidated. Drain + close, force
	// re-login, retry once.
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	c.mu.Lock()
	c.cookieReady = false
	if err := c.loginLocked(ctx); err != nil {
		c.mu.Unlock()
		return nil, fmt.Errorf("re-login after 401 failed: %w", err)
	}
	c.mu.Unlock()
	return c.doOnce(ctx, method, path, body, setHeader)
}

// doOnce is one request attempt — no retry logic. Used by Do() for
// the initial + retried-after-401 attempts.
func (c *Client) doOnce(ctx context.Context, method, path string, body io.Reader, setHeader func(http.Header)) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.apiURL(path), body)
	if err != nil {
		return nil, fmt.Errorf("build %s %s: %w", method, path, err)
	}
	req.Header.Set("Referer", c.baseURL)
	if setHeader != nil {
		setHeader(req.Header)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		// Transport-level failure (DNS, refused, timeout). Scrub the qui
		// proxy token from the error first: Go's *url.Error embeds the full
		// request URL (token included), which must not reach the log or the
		// UI. The path arg itself is token-free.
		err = c.scrubErr(err)
		fmt.Fprintf(os.Stderr, "resolvarr: qbit %s %s -> transport error: %v\n", method, path, err)
		return nil, err
	}
	// Surface qBit-side rejections in the container log. qBit answers a
	// CSRF Referer/Host mismatch (common behind a reverse proxy that
	// rewrites the origin) with a 4xx; without this line the only signal
	// is the per-event UI error, which the operator can't correlate to a
	// server-side cause. Path is the token-free API path, safe to log.
	if resp.StatusCode >= 400 {
		fmt.Fprintf(os.Stderr, "resolvarr: qbit %s %s -> HTTP %d\n", method, path, resp.StatusCode)
	}
	return resp, nil
}

// Torrent is a minimal subset of qBit's /api/v2/torrents/info entry —
// enough for our needs (list-torrent + tag-by-name + tag-by-hash).
// Add fields here as features need them rather than mirroring the
// full upstream shape.
type Torrent struct {
	Hash     string `json:"hash"`
	Name     string `json:"name"`
	Tags     string `json:"tags"` // comma-separated; "" when none
	Category string `json:"category"`
	State    string `json:"state"`
}

// listTorrentsMaxBytes caps the response decode for ListTorrents.
// 50 MiB is generous for honest libraries (10k torrents × ~500 bytes/
// entry ≈ 5 MB; 50 MiB is 10× headroom) but bounds attacker-controlled
// or compromised qBit endpoints from streaming arbitrarily large
// payloads into our memory.
//
// getTorrentMaxBytes is the per-hash variant — single-torrent response
// from /torrents/info?hashes=N is bounded by qBit returning at most
// one entry, so 64 KiB is plenty.
const (
	listTorrentsMaxBytes = 50 << 20 // 50 MiB
	getTorrentMaxBytes   = 64 << 10 // 64 KiB
	// listFilesMaxBytes caps /torrents/files. A season pack is a few
	// dozen files × ~200 bytes/entry; 8 MiB is huge headroom while
	// still bounding a compromised endpoint.
	listFilesMaxBytes = 8 << 20 // 8 MiB
)

// TorrentFile is the subset of /api/v2/torrents/files we need to rename
// individual files inside a (season-pack) torrent. Name is the path
// RELATIVE to the torrent root (e.g. "Season 03/the.last.kingdom.s03e01
// ...mkv") — exactly the oldPath /renameFile expects.
type TorrentFile struct {
	Index int    `json:"index"`
	Name  string `json:"name"`
	Size  int64  `json:"size"`
}

// ListTorrents fetches every torrent matching the optional filter.
// Pass empty string for filter to get all torrents (typical Test
// Connection use case — the test just needs to confirm we got a
// 200 response with parseable JSON, not the full payload).
//
// Body capped at 50 MiB via http.MaxBytesReader to bound memory use
// against misconfigured / hostile / compromised qBit endpoints.
func (c *Client) ListTorrents(ctx context.Context, filter string) ([]Torrent, error) {
	path := "/api/v2/torrents/info"
	if filter != "" {
		path += "?filter=" + url.QueryEscape(filter)
	}
	resp, err := c.Do(ctx, "GET", path, nil, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("listTorrents HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var list []Torrent
	if err := json.NewDecoder(io.LimitReader(resp.Body, listTorrentsMaxBytes)).Decode(&list); err != nil {
		return nil, fmt.Errorf("decode listTorrents response: %w", err)
	}
	return list, nil
}

// GetTorrent fetches a single torrent's metadata by hash. Returns
// (torrent, true, nil) on hit, (zero, false, nil) when the hash isn't
// in qBit's library, (zero, false, err) on real failures (auth /
// network / 5xx).
//
// Bash equivalent (tagarr_import.sh:217-225) retries with backoff
// because qBit may not have indexed the torrent yet right after
// /torrents/add returns. Adapter does that retry; this client method
// is the single round-trip primitive.
//
// Hash comparison is case-insensitive on qBit's side — the API
// accepts mixed-case hex. We pass it through verbatim and let qBit
// match.
func (c *Client) GetTorrent(ctx context.Context, hash string) (Torrent, bool, error) {
	if hash == "" {
		return Torrent{}, false, fmt.Errorf("hash is required")
	}
	path := "/api/v2/torrents/info?hashes=" + url.QueryEscape(hash)
	resp, err := c.Do(ctx, "GET", path, nil, nil)
	if err != nil {
		return Torrent{}, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return Torrent{}, false, fmt.Errorf("getTorrent HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var list []Torrent
	if err := json.NewDecoder(io.LimitReader(resp.Body, getTorrentMaxBytes)).Decode(&list); err != nil {
		return Torrent{}, false, fmt.Errorf("decode getTorrent response: %w", err)
	}
	if len(list) == 0 {
		return Torrent{}, false, nil
	}
	return list[0], true, nil
}

// RenameTorrent updates the qBit torrent's display name (the "Name"
// field shown in the qBit UI; what Radarr/Sonarr's import parser
// reads via the qBit API for re-scoring at import time). Files on
// disk are NOT touched — qBit just relabels the torrent entry.
//
// Used by the M-Webhook Grab Rename adapter to fix display-names
// that the indexer-supplied torrent name strips. Idempotent — qBit
// returns 200 even when the new name equals the old.
//
// API: POST /api/v2/torrents/rename with form-encoded
// `hash=<hash>&name=<newName>`. qBit returns 200 OK on success;
// 404 when the hash isn't in the client; 409 when newName is
// invalid (non-empty whitespace-only is the typical reject case
// — qBit's filename sanitiser).
func (c *Client) RenameTorrent(ctx context.Context, hash, newName string) error {
	if hash == "" {
		return fmt.Errorf("hash is required")
	}
	if newName == "" {
		return fmt.Errorf("newName is required")
	}
	form := url.Values{}
	form.Set("hash", hash)
	form.Set("name", newName)
	body := strings.NewReader(form.Encode())
	resp, err := c.Do(ctx, "POST", "/api/v2/torrents/rename", body, func(h http.Header) {
		h.Set("Content-Type", "application/x-www-form-urlencoded")
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 200 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	snippet := strings.TrimSpace(string(bodyBytes))
	switch resp.StatusCode {
	case 404:
		return fmt.Errorf("renameTorrent: hash not found in qBit (HTTP 404): %s", snippet)
	case 409:
		return fmt.Errorf("renameTorrent: name rejected by qBit (HTTP 409): %s", snippet)
	}
	if snippet == "" {
		return fmt.Errorf("renameTorrent HTTP %d", resp.StatusCode)
	}
	return fmt.Errorf("renameTorrent HTTP %d: %s", resp.StatusCode, snippet)
}

// ListTorrentFiles returns the files inside a torrent (their paths
// relative to the torrent root, plus index + size). Used by the Grab
// Rename "files" target to rename each episode file inside a season
// pack so Sonarr scores it correctly at import.
//
// API: GET /api/v2/torrents/files?hash=<hash>. qBit has the file list
// from torrent metadata as soon as the torrent is added (before the
// download completes), so this works on an in-flight grab. Returns 404
// when the hash isn't in the client.
func (c *Client) ListTorrentFiles(ctx context.Context, hash string) ([]TorrentFile, error) {
	if hash == "" {
		return nil, fmt.Errorf("hash is required")
	}
	path := "/api/v2/torrents/files?hash=" + url.QueryEscape(hash)
	resp, err := c.Do(ctx, "GET", path, nil, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("listTorrentFiles HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var files []TorrentFile
	if err := json.NewDecoder(io.LimitReader(resp.Body, listFilesMaxBytes)).Decode(&files); err != nil {
		return nil, fmt.Errorf("decode listTorrentFiles response: %w", err)
	}
	return files, nil
}

// RenameFile renames a single file INSIDE a torrent (on disk), as
// opposed to RenameTorrent which only relabels the torrent entry.
// oldPath/newPath are relative to the torrent root (the same shape
// ListTorrentFiles returns in Name). Works on incomplete torrents —
// qBit renames the in-progress target path.
//
// API: POST /api/v2/torrents/renameFile with form-encoded
// `hash=<hash>&oldPath=<old>&newPath=<new>`. 200 on success; 404 when
// the hash isn't found; 409 when the new path is invalid / collides
// (qBit's filename sanitiser).
func (c *Client) RenameFile(ctx context.Context, hash, oldPath, newPath string) error {
	if hash == "" {
		return fmt.Errorf("hash is required")
	}
	if oldPath == "" || newPath == "" {
		return fmt.Errorf("oldPath and newPath are required")
	}
	form := url.Values{}
	form.Set("hash", hash)
	form.Set("oldPath", oldPath)
	form.Set("newPath", newPath)
	body := strings.NewReader(form.Encode())
	resp, err := c.Do(ctx, "POST", "/api/v2/torrents/renameFile", body, func(h http.Header) {
		h.Set("Content-Type", "application/x-www-form-urlencoded")
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 200 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	snippet := strings.TrimSpace(string(bodyBytes))
	switch resp.StatusCode {
	case 404:
		return fmt.Errorf("renameFile: hash not found in qBit (HTTP 404): %s", snippet)
	case 409:
		return fmt.Errorf("renameFile: path rejected by qBit (HTTP 409): %s", snippet)
	}
	if snippet == "" {
		return fmt.Errorf("renameFile HTTP %d", resp.StatusCode)
	}
	return fmt.Errorf("renameFile HTTP %d: %s", resp.StatusCode, snippet)
}

// AddTags adds one or more tags to the given torrent hashes. qBit
// auto-creates tags that don't exist yet. Idempotent — re-applying
// an already-present tag is a no-op (qBit returns 200 either way).
//
// API: POST /api/v2/torrents/addTags with form-encoded
// `hashes=<h1>|<h2>&tags=<t1>,<t2>`. Hash separator is `|`, tag
// separator is `,` — qBit's documented convention.
//
// Used by:
//   - M-Webhook qBit S/E adapter on Sonarr Grab
//   - Backlog-fix scan in the wizard step 3c flow
//
// Empty hashes or empty tags → no-op (returns nil without contacting
// qBit). Surface clean rejection at the call site if the user
// misconfigures.
func (c *Client) AddTags(ctx context.Context, hashes []string, tags []string) error {
	if len(hashes) == 0 || len(tags) == 0 {
		return nil
	}
	form := url.Values{}
	form.Set("hashes", strings.Join(hashes, "|"))
	form.Set("tags", strings.Join(tags, ","))
	body := strings.NewReader(form.Encode())
	resp, err := c.Do(ctx, "POST", "/api/v2/torrents/addTags", body, func(h http.Header) {
		h.Set("Content-Type", "application/x-www-form-urlencoded")
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 200 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	snippet := strings.TrimSpace(string(bodyBytes))
	if snippet == "" {
		return fmt.Errorf("addTags HTTP %d", resp.StatusCode)
	}
	return fmt.Errorf("addTags HTTP %d: %s", resp.StatusCode, snippet)
}

// SetTorrentCategory changes the qBit torrent's category — the
// post-import flow Sonarr/Radarr normally drives when a download
// completes. Used by the M-Webhook qBit Category Fix function to
// reconcile torrents stuck on their pre-import category after a real
// import.
//
// qBit auto-creates categories that don't exist yet (with whatever
// default save-path the user has configured for unknown categories,
// which is qBit's behaviour — not ours to override). Empty category
// is valid and removes the category from the torrent.
//
// API: POST /api/v2/torrents/setCategory with form-encoded
// `hashes=<hash>&category=<name>`. qBit returns 200 on success;
// 409 when the category name is invalid (e.g. contains slash on
// older qBit versions); 404 isn't documented but we handle it
// defensively.
//
// Idempotent — re-applying the same category is a no-op on qBit's
// side (returns 200 either way).
func (c *Client) SetTorrentCategory(ctx context.Context, hash, category string) error {
	if hash == "" {
		return fmt.Errorf("hash is required")
	}
	form := url.Values{}
	form.Set("hashes", hash)
	form.Set("category", category)
	body := strings.NewReader(form.Encode())
	resp, err := c.Do(ctx, "POST", "/api/v2/torrents/setCategory", body, func(h http.Header) {
		h.Set("Content-Type", "application/x-www-form-urlencoded")
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 200 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	snippet := strings.TrimSpace(string(bodyBytes))
	switch resp.StatusCode {
	case 404:
		return fmt.Errorf("setTorrentCategory: hash not found in qBit (HTTP 404): %s", snippet)
	case 409:
		return fmt.Errorf("setTorrentCategory: category name rejected by qBit (HTTP 409): %s", snippet)
	}
	if snippet == "" {
		return fmt.Errorf("setTorrentCategory HTTP %d", resp.StatusCode)
	}
	return fmt.Errorf("setTorrentCategory HTTP %d: %s", resp.StatusCode, snippet)
}

// Ping is the cheapest health-check call — used by the Settings →
// qBit instances Test Connection button. Logs in (if not already)
// and issues a tiny listTorrents to confirm the API actually
// responds, not just that login returned Ok.
//
// Returns nil on success. On failure the error message is
// surface-able to the user.
func (c *Client) Ping(ctx context.Context) error {
	// Force a fresh login so Test Connection actually validates the
	// supplied creds rather than reusing a stale cookie.
	c.mu.Lock()
	c.cookieReady = false
	if err := c.loginLocked(ctx); err != nil {
		c.mu.Unlock()
		return err
	}
	c.mu.Unlock()
	// One trivial API call to confirm the session works end-to-end.
	// `filter=stalled` returns fast (server doesn't have to walk
	// every torrent) and the empty result on a fresh qBit is fine.
	_, err := c.ListTorrents(ctx, "stalled")
	return err
}
