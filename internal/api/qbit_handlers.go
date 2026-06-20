package api

// qbit_handlers.go — CRUD + Test Connection for the user-managed
// qBittorrent instances list. Pairing with Arr instances happens
// elsewhere (WebhookConfig.QbitInstanceID, when functions land);
// this file is just the standalone instance-management surface.
//
// Endpoints:
//   GET    /api/qbit-instances              list (passwords masked)
//   POST   /api/qbit-instances              create
//   PUT    /api/qbit-instances/{id}         update (preserves masked password)
//   DELETE /api/qbit-instances/{id}         delete
//   POST   /api/qbit-instances/{id}/test    Test Connection (uses stored creds)
//   POST   /api/qbit-instances/test         Test Connection inline (body has full creds — used by the Add modal before save)

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"resolvarr/internal/core"
	"resolvarr/internal/qbit"
)

// qbitTestTimeout caps the test-connection probe so a misconfigured
// URL doesn't tie up the request goroutine for the full client
// timeout. Test should respond fast on a healthy qBit.
const qbitTestTimeout = 10 * time.Second

// proxyTokenPattern captures a qui-style client-proxy token in the
// URL — `/proxy/<hex>`. The token IS the auth (qui has no per-request
// login flow when the token is in the path), so it gets the same
// partial-reveal masking as API keys.
var proxyTokenPattern = regexp.MustCompile(`(?i)(/proxy/)([a-f0-9]{16,})`)

// maskedProxyPattern detects URLs we've already masked: a /proxy/
// segment whose body contains at least one star. Used on the PUT path
// to know whether to preserve the stored URL.
var maskedProxyPattern = regexp.MustCompile(`(?i)/proxy/[^/]*\*`)

// maskQbitURL replaces a qui-proxy token with a partial-reveal form
// (first 4 hex + stars + last 4 hex via maskKey) so the user can
// visually confirm the right token from the row list without seeing
// the full secret. URLs with no embedded auth token (direct qBit,
// reverse-proxied URLs without /proxy/<hex>) pass through unchanged
// — host:port is connection info, not auth.
func maskQbitURL(raw string) string {
	return proxyTokenPattern.ReplaceAllStringFunc(raw, func(match string) string {
		parts := proxyTokenPattern.FindStringSubmatch(match)
		if len(parts) != 3 {
			return match
		}
		return parts[1] + maskKey(parts[2])
	})
}

// isMaskedQbitURL returns true if the URL's /proxy/ segment contains
// our masked-output stars — which means the user round-tripped a value
// the server sent earlier and we should preserve the stored token on
// PUT instead of validating + saving the masked form.
func isMaskedQbitURL(raw string) bool {
	return maskedProxyPattern.MatchString(raw)
}

// proxyTokenSegment matches the whole `/proxy/<token>` path segment
// (token = any run of non-slash/query/fragment chars), masked or not.
// Used to splice a real token back into an edited URL.
var proxyTokenSegment = regexp.MustCompile(`(?i)/proxy/[^/?#]+`)

// restoreQbitProxyToken splices the real /proxy/<token> from the stored
// URL into the submitted URL, preserving any host/port edits the user
// made around the masked token. The submitted URL is known to carry a
// masked token (caller checks isMaskedQbitURL). If the stored URL has no
// real token to recover (shouldn't happen in practice), the safe
// fallback is the stored URL unchanged.
func restoreQbitProxyToken(submitted, stored string) string {
	m := proxyTokenPattern.FindStringSubmatch(stored)
	if len(m) != 3 {
		return stored
	}
	realSegment := "/proxy/" + m[2]
	return proxyTokenSegment.ReplaceAllString(submitted, realSegment)
}

// validateQbitInstanceBody rejects malformed creates / updates. Lets
// the qbit.New() URL validation do the heavy lifting on the URL
// shape; here we just check the user-facing constraints.
func validateQbitInstanceBody(req core.QbitInstance, all []core.QbitInstance, ignoreID string) error {
	req.Name = strings.TrimSpace(req.Name)
	req.URL = strings.TrimSpace(req.URL)
	req.Username = strings.TrimSpace(req.Username)
	if req.Name == "" {
		return errors.New("name is required")
	}
	if req.URL == "" {
		return errors.New("URL is required")
	}
	// Sanity-check the URL via the same parser the client uses.
	if _, err := qbit.New(qbit.Config{URL: req.URL}); err != nil {
		return err
	}
	// Name uniqueness across instances (case-insensitive). Skip
	// the entry being updated.
	lower := strings.ToLower(req.Name)
	for _, existing := range all {
		if existing.ID == ignoreID {
			continue
		}
		if strings.ToLower(existing.Name) == lower {
			return fmt.Errorf("name %q is already used by another qBit instance", req.Name)
		}
	}
	return nil
}

// handleListQbitInstances returns every configured qBit instance with
// the password masked. Same pattern as handleGetConfig's API-key
// masking — the unmasked value is fetched via /test, never returned
// in a plain GET.
func (s *Server) handleListQbitInstances(w http.ResponseWriter, r *http.Request) {
	cfg := s.App.Config.Get()
	out := make([]core.QbitInstance, 0, len(cfg.QbitInstances))
	for _, qi := range cfg.QbitInstances {
		copy := qi
		copy.URL = maskQbitURL(copy.URL)
		if copy.Password != "" {
			copy.Password = maskSentinel
		}
		// WebhookSecret only surfaces via the dedicated /webhook
		// endpoint (Slice 4) — list view masks it like the password.
		if copy.WebhookSecret != "" {
			copy.WebhookSecret = maskSentinel
		}
		out = append(out, copy)
	}
	writeJSON(w, out)
}

// handleCreateQbitInstance adds a new entry. ID auto-generated.
// Password from the request is stored as-is (no masking on POST —
// this is the create path; the user just supplied it).
func (s *Server) handleCreateQbitInstance(w http.ResponseWriter, r *http.Request) {
	var req core.QbitInstance
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&req); err != nil {
		writeError(w, 400, "invalid body: "+err.Error())
		return
	}
	cfg := s.App.Config.Get()
	// Reject masked-token round-trips on Create — there's no
	// stored value to preserve when adding a brand-new entry,
	// so a masked URL or password here is always a UI bug or
	// copy-paste mistake.
	if isMaskedQbitURL(req.URL) {
		writeError(w, 400, "URL contains a masked token placeholder — paste the real qui proxy URL")
		return
	}
	if req.Password == maskSentinel {
		writeError(w, 400, "password cannot be the masked placeholder — supply the real password or leave blank for qui proxy / no-auth setups")
		return
	}
	if err := validateQbitInstanceBody(req, cfg.QbitInstances, ""); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	// WebhookSecret is generated up-front so the qBit-side webhook
	// (M-qBit-add — catches cross-seed adds) is ready as soon as the
	// instance exists. User flips it on later via the dedicated webhook-
	// config endpoint; without a secret pre-stamped that endpoint would
	// have to lazy-generate AND save in the same call, which complicates
	// the round-trip. Cheap to generate, never exposed in plain echos.
	secret, err := generateWebhookSecret()
	if err != nil {
		writeError(w, 500, "generate webhook secret: "+err.Error())
		return
	}
	created := core.QbitInstance{
		ID:            genID(),
		Name:          strings.TrimSpace(req.Name),
		URL:           strings.TrimSpace(req.URL),
		Username:      strings.TrimSpace(req.Username),
		Password:      req.Password, // intentionally NOT trimmed — passwords can have leading/trailing whitespace
		TrustedCerts:  req.TrustedCerts,
		WebhookSecret: secret,
	}
	if err := s.App.Config.Update(func(c *core.Config) {
		c.QbitInstances = append(c.QbitInstances, created)
	}); err != nil {
		writeError(w, 500, "save: "+err.Error())
		return
	}
	// Echo back with URL token + password + webhook secret masked.
	// Webhook secret is exposed only via the dedicated /webhook
	// endpoint (Slice 4) where the user can copy the curl command.
	echo := created
	echo.URL = maskQbitURL(echo.URL)
	if echo.Password != "" {
		echo.Password = maskSentinel
	}
	if echo.WebhookSecret != "" {
		echo.WebhookSecret = maskSentinel
	}
	writeJSON(w, echo)
}

// handleUpdateQbitInstance edits an existing entry. Empty / masked
// password preserves the stored value (so the user can edit Name /
// URL / Username without re-typing the password every time).
func (s *Server) handleUpdateQbitInstance(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, 400, "id required")
		return
	}
	var req core.QbitInstance
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&req); err != nil {
		writeError(w, 400, "invalid body: "+err.Error())
		return
	}
	cfg := s.App.Config.Get()
	// Find existing first — needed for both URL + password
	// preservation when the user round-tripped masked values.
	var existing *core.QbitInstance
	for i := range cfg.QbitInstances {
		if cfg.QbitInstances[i].ID == id {
			existing = &cfg.QbitInstances[i]
			break
		}
	}
	if existing == nil {
		writeError(w, 404, "qBit instance not found")
		return
	}
	// URL preservation: when the input still carries our masked
	// /proxy/<stars> token, the user kept the token (they can't see the
	// real one) but may have edited the host/port around it. Splice the
	// real token from the stored URL back into the submitted URL so
	// those edits survive, instead of discarding the whole URL. A fresh
	// real token, or a non-proxy URL, is treated as a real edit and
	// validated as-is.
	if isMaskedQbitURL(req.URL) {
		req.URL = restoreQbitProxyToken(req.URL, existing.URL)
	}
	if err := validateQbitInstanceBody(req, cfg.QbitInstances, id); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	// Password preservation — masked-sentinel input keeps the
	// stored value (user round-tripped the placeholder unchanged).
	// Empty is a valid input (qui proxy / no-auth setups) but we
	// can't tell whether the user meant "preserve" or "clear",
	// so for now empty also preserves. To explicitly clear a
	// stored password the user can edit the URL-only path or
	// delete + re-create the instance. Real new password replaces.
	password := req.Password
	if password == "" || password == maskSentinel {
		password = existing.Password
	}
	// Preserve webhook-related fields — the rule editor doesn't touch
	// them, and the dedicated /webhook endpoints are the only place
	// they get rotated/reset. Also backfill WebhookSecret if missing
	// (existing instances saved before this field landed; first save
	// stamps a secret so the webhook flow is ready when the user
	// clicks Configure).
	webhookSecret := existing.WebhookSecret
	if webhookSecret == "" {
		gen, err := generateWebhookSecret()
		if err != nil {
			writeError(w, 500, "generate webhook secret: "+err.Error())
			return
		}
		webhookSecret = gen
	}
	updated := core.QbitInstance{
		ID:                      id,
		Name:                    strings.TrimSpace(req.Name),
		URL:                     strings.TrimSpace(req.URL),
		Username:                strings.TrimSpace(req.Username),
		Password:                password,
		TrustedCerts:            req.TrustedCerts,
		WebhookSecret:           webhookSecret,
		PreviousAutorunBackup:   existing.PreviousAutorunBackup,
		WebhookConfiguredInQbit: existing.WebhookConfiguredInQbit,
	}
	if err := s.App.Config.Update(func(c *core.Config) {
		for i := range c.QbitInstances {
			if c.QbitInstances[i].ID == id {
				c.QbitInstances[i] = updated
				return
			}
		}
	}); err != nil {
		writeError(w, 500, "save: "+err.Error())
		return
	}
	echo := updated
	echo.URL = maskQbitURL(echo.URL)
	if echo.Password != "" {
		echo.Password = maskSentinel
	}
	if echo.WebhookSecret != "" {
		echo.WebhookSecret = maskSentinel
	}
	writeJSON(w, echo)
}

// handleDeleteQbitInstance removes an entry. Eventual: also clear
// any WebhookConfig.QbitInstanceID references pointing at this ID.
// Today (no per-function flags wired yet) there's nothing to clean
// up; keep the cleanup pass for the session that wires the
// pairing-aware flags.
func (s *Server) handleDeleteQbitInstance(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, 400, "id required")
		return
	}
	if err := s.App.Config.Update(func(c *core.Config) {
		out := make([]core.QbitInstance, 0, len(c.QbitInstances))
		for _, qi := range c.QbitInstances {
			if qi.ID != id {
				out = append(out, qi)
			}
		}
		c.QbitInstances = out
	}); err != nil {
		writeError(w, 500, "save: "+err.Error())
		return
	}
	writeJSON(w, map[string]any{"status": "ok"})
}

// handleTestQbitInstance probes the saved credentials for an
// existing instance ID. Used by the per-row Test Connection button
// after the entry is already saved.
func (s *Server) handleTestQbitInstance(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, 400, "id required")
		return
	}
	cfg := s.App.Config.Get()
	var qi *core.QbitInstance
	for i := range cfg.QbitInstances {
		if cfg.QbitInstances[i].ID == id {
			qi = &cfg.QbitInstances[i]
			break
		}
	}
	if qi == nil {
		writeError(w, 404, "qBit instance not found")
		return
	}
	s.runQbitTest(w, r, qbit.Config{
		URL:          qi.URL,
		Username:     qi.Username,
		Password:     qi.Password,
		TrustedCerts: qi.TrustedCerts,
	})
}

// handleTestQbitInline probes credentials supplied in the request
// body — used by the Add modal's Test Connection button BEFORE the
// user clicks Save (no entry exists yet). Body shape mirrors the
// CRUD body but only URL / Username / Password / TrustedCerts are
// read; Name + ID are ignored.
//
// If the body's Password is the masked sentinel and an existing-
// instance ID is supplied, fall back to the stored password — lets
// the Edit modal test without re-typing the password. Empty body
// password just attempts no-auth.
func (s *Server) handleTestQbitInline(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID           string `json:"id,omitempty"`
		URL          string `json:"url"`
		Username     string `json:"username"`
		Password     string `json:"password"`
		TrustedCerts bool   `json:"trustedCerts"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&req); err != nil {
		writeError(w, 400, "invalid body: "+err.Error())
		return
	}
	if req.URL == "" {
		writeError(w, 400, "URL is required")
		return
	}
	url := strings.TrimSpace(req.URL)
	password := req.Password
	urlMasked := isMaskedQbitURL(url)
	pwMasked := password == maskSentinel
	if (urlMasked || pwMasked || password == "") && req.ID != "" {
		// Edit modal — resolve any masked field via the stored
		// instance so the test uses real values without the user
		// re-typing. Empty password also lands here so "edit, only
		// touched the username" tests with the saved password.
		cfg := s.App.Config.Get()
		for _, qi := range cfg.QbitInstances {
			if qi.ID == req.ID {
				if urlMasked {
					url = qi.URL
				}
				if pwMasked || password == "" {
					password = qi.Password
				}
				break
			}
		}
	} else if urlMasked {
		writeError(w, 400, "URL contains a masked token — paste the real qui proxy URL or include the instance id")
		return
	} else if pwMasked {
		writeError(w, 400, "password is masked — supply the real password or include the instance id")
		return
	}
	s.runQbitTest(w, r, qbit.Config{
		URL:          url,
		Username:     strings.TrimSpace(req.Username),
		Password:     password,
		TrustedCerts: req.TrustedCerts,
	})
}

// runQbitTest is the shared probe path: build a Client, force a
// fresh login + listTorrents call, surface the result. Caps via
// qbitTestTimeout so a wrong URL doesn't stall the request.
func (s *Server) runQbitTest(w http.ResponseWriter, r *http.Request, cfg qbit.Config) {
	client, err := qbit.New(cfg)
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), qbitTestTimeout)
	defer cancel()
	if err := client.Ping(ctx); err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "message": "Connected — qBit accepted the credentials."})
}
