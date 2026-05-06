package api

// webhooks.go — HTTP handlers for the Connect webhook subsystem.
//
// Three surfaces today (logging-only feature):
//
//   POST   /api/webhooks/{token}              — receive a Connect event
//   GET    /api/instances/{id}/webhook/events — list recent events for an instance
//   DELETE /api/instances/{id}/webhook/events — clear the per-instance log
//   POST   /api/instances/{id}/webhook/rotate — generate or regenerate the token
//
// Subsequent sessions wire function execution onto the receive path
// (release-group tag, DV detail, audio/video tags, qBit S/E tag, etc.)
// gated by per-function flags on Instance.Webhook. Today the receiver
// just decodes, summarises, persists, and acks.

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"resolvarr/internal/core"
)

// webhookBodyMaxBytes caps the POST body. Connect events in the wild
// run ~1-50 KB; 1 MB is 20× margin, prevents a hostile/buggy client
// from streaming gigabytes into our memory.
const webhookBodyMaxBytes = 1 << 20 // 1 MiB

// connectEventEnvelope is the lowest-common-denominator decode target
// across Sonarr + Radarr Connect events. We only pull out the fields
// we surface in the recent-events panel; the full body is preserved
// in WebhookEvent.Raw for the expand-to-see view.
//
// Sonarr and Radarr both emit `eventType` as the discriminator. The
// rest of the fields are best-effort union — Movie present on Radarr
// import/grab, Series + Episodes present on Sonarr equivalents.
// Unknown payloads (Health, ApplicationUpdate, etc.) decode cleanly
// with everything zero and the Title/Subtitle fields stay empty.
type connectEventEnvelope struct {
	EventType string `json:"eventType"`

	// Radarr fields
	Movie *struct {
		ID    int    `json:"id"`
		Title string `json:"title"`
		Year  int    `json:"year"`
	} `json:"movie,omitempty"`

	// Sonarr fields
	Series *struct {
		ID    int    `json:"id"`
		Title string `json:"title"`
		Year  int    `json:"year,omitempty"`
	} `json:"series,omitempty"`
	Episodes []struct {
		EpisodeNumber int `json:"episodeNumber"`
		SeasonNumber  int `json:"seasonNumber"`
	} `json:"episodes,omitempty"`

	// Health / ApplicationUpdate / ManualInteractionRequired carry
	// Level / Message in different shapes; one minor signal.
	Level   string `json:"level,omitempty"`
	Message string `json:"message,omitempty"`
}

// summariseEvent extracts a Title + Subtitle for the recent-events
// card. Defensive: every dereference is nil-checked because the
// envelope's pointer fields are absent on event types that don't
// carry them. Empty strings render as "(no title)" in the UI.
func summariseEvent(env *connectEventEnvelope) (title, subtitle string) {
	switch {
	case env.Movie != nil && env.Movie.Title != "":
		title = env.Movie.Title
		if env.Movie.Year > 0 {
			subtitle = fmt.Sprintf("%d", env.Movie.Year)
		}
	case env.Series != nil && env.Series.Title != "":
		title = env.Series.Title
		if len(env.Episodes) > 0 {
			// Compress contiguous episodes per season into "S01E05"
			// or "S01E05-E07" form. Multi-episode grabs can carry
			// 24 entries (whole-season packs); a single label keeps
			// the card readable.
			subtitle = formatEpisodes(env.Episodes)
		} else if env.Series.Year > 0 {
			subtitle = fmt.Sprintf("%d", env.Series.Year)
		}
	case env.Message != "":
		title = env.Message
		subtitle = env.Level
	}
	return title, subtitle
}

// formatEpisodes builds a compact "S01E05 + 3 more" label from the
// Sonarr Episodes array. Same shape Sonarr uses in its own UI for
// multi-episode events.
func formatEpisodes(eps []struct {
	EpisodeNumber int `json:"episodeNumber"`
	SeasonNumber  int `json:"seasonNumber"`
}) string {
	if len(eps) == 0 {
		return ""
	}
	first := fmt.Sprintf("S%02dE%02d", eps[0].SeasonNumber, eps[0].EpisodeNumber)
	if len(eps) == 1 {
		return first
	}
	return fmt.Sprintf("%s + %d more", first, len(eps)-1)
}

// findInstanceByWebhookToken scans the config for the instance whose
// WebhookConfig.Token matches the given token. Linear walk over the
// instance list — limited to ~10 instances on typical setups, so
// the simpler O(N) lookup beats keeping a separate index in sync.
// Returns nil if no instance has that token (or the token is empty).
//
// Comparison is constant-time per candidate token to remove a tiny
// timing oracle (== short-circuits on first byte mismatch). Probably
// not weaponisable over the network for a 256-bit base64url token,
// but the one-line subtle.ConstantTimeCompare keeps the receiver
// honest if the entropy ever drops in some future change.
func findInstanceByWebhookToken(cfg core.Config, token string) *core.Instance {
	if token == "" {
		return nil
	}
	tokenBytes := []byte(token)
	for i := range cfg.Instances {
		stored := cfg.Instances[i].Webhook.Token
		if stored == "" || len(stored) != len(token) {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(stored), tokenBytes) == 1 {
			return &cfg.Instances[i]
		}
	}
	return nil
}

// generateWebhookToken returns a fresh base64url-encoded random token.
// 32 bytes of crypto/rand → 43 chars in base64url (no padding) — long
// enough that brute-forcing the URL is infeasible (256 bits of entropy).
func generateWebhookToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// handleWebhookReceive is the public Connect endpoint. URL form:
//
//	POST /api/webhooks/{token}
//
// Body: Sonarr or Radarr Connect JSON. Response: 200 OK on success
// (Connect retries on 5xx, abandons after a couple 4xx — we want
// retries on transient failures, no retries on bad-token).
//
// Auth: the token in the URL path. Bad/unknown token → 404 (leaks
// less information than 403 — caller can't distinguish "not
// configured" from "wrong instance").
func (s *Server) handleWebhookReceive(w http.ResponseWriter, r *http.Request) {
	if s.WebhookLog == nil {
		writeError(w, 503, "webhook log not initialised")
		return
	}
	token := r.PathValue("token")
	if token == "" {
		writeError(w, 404, "not found")
		return
	}
	cfg := s.App.Config.Get()
	inst := findInstanceByWebhookToken(cfg, token)
	if inst == nil {
		writeError(w, 404, "not found")
		return
	}
	// Logging-only today: only persist when LoggingEnabled is on.
	// When functions land, this gate splits per-function — logging
	// can stay on without firing functions. The receiver still
	// returns 200 either way so Sonarr/Radarr doesn't retry.
	if !inst.Webhook.LoggingEnabled {
		writeJSON(w, map[string]any{"status": "ok", "logged": false})
		return
	}

	// Read body with cap. Sonarr/Radarr send ~1-50 KB; 1 MiB ceiling
	// is paranoid but cheap. ContentLength check first so an
	// honest oversize request rejects without buffering.
	if r.ContentLength > webhookBodyMaxBytes {
		writeError(w, 413, "body too large")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, webhookBodyMaxBytes))
	if err != nil {
		writeError(w, 400, "read body: "+err.Error())
		return
	}

	// Best-effort decode for the summary fields. A failed decode
	// doesn't drop the event — we still log the raw body so the
	// user can see the malformed JSON in the recent-events panel
	// and debug. (Sonarr emits one or two "test" events with
	// invalid JSON during version transitions; capturing them is
	// useful.)
	var env connectEventEnvelope
	rawForStorage := json.RawMessage(body)
	if jerr := json.Unmarshal(body, &env); jerr != nil {
		// Fall through with an empty envelope — Title/Subtitle
		// will be empty, EventType will be "(unparseable)".
		env.EventType = "(unparseable)"
		// Re-wrap the body as a JSON string so MarshalIndent on
		// the persist map doesn't choke trying to validate the
		// invalid bytes via json.RawMessage's MarshalJSON. Without
		// this every subsequent persist would fail until restart,
		// silently losing the on-disk log for ALL instances.
		wrapped, werr := json.Marshal(map[string]string{"_unparseable": string(body)})
		if werr == nil {
			rawForStorage = json.RawMessage(wrapped)
		} else {
			rawForStorage = json.RawMessage(`{"_unparseable":"<encode failed>"}`)
		}
	}
	if env.EventType == "" {
		env.EventType = "(unknown)"
	}
	title, subtitle := summariseEvent(&env)

	ev := WebhookEvent{
		ID:         genID(),
		InstanceID: inst.ID,
		ReceivedAt: time.Now().UTC(),
		EventType:  env.EventType,
		Title:      title,
		Subtitle:   subtitle,
		Raw:        rawForStorage,
	}
	count := s.WebhookLog.append(ev)
	writeJSON(w, map[string]any{
		"status":    "ok",
		"logged":    true,
		"eventType": env.EventType,
		"count":     count,
	})
}

// handleWebhookListEvents returns the recent-events list for an
// instance. Newest first.
func (s *Server) handleWebhookListEvents(w http.ResponseWriter, r *http.Request) {
	if s.WebhookLog == nil {
		writeJSON(w, []WebhookEvent{})
		return
	}
	inst := s.instanceByID(w, r.PathValue("id"))
	if inst == nil {
		return
	}
	events := s.WebhookLog.list(inst.ID)
	writeJSON(w, events)
}

// handleWebhookClearEvents wipes the per-instance log. Called from
// the "Clear log" button on the Webhooks UI. Idempotent — clearing
// an already-empty log is a no-op success.
func (s *Server) handleWebhookClearEvents(w http.ResponseWriter, r *http.Request) {
	if s.WebhookLog == nil {
		writeJSON(w, map[string]any{"status": "ok"})
		return
	}
	inst := s.instanceByID(w, r.PathValue("id"))
	if inst == nil {
		return
	}
	s.WebhookLog.clear(inst.ID)
	writeJSON(w, map[string]any{"status": "ok"})
}

// handleWebhookRotateToken generates a fresh token for the instance
// (or sets the first token, when the instance has none). Body: optional
// `{"loggingEnabled": *bool}` — the wizard's "Enable logging" toggle.
// LoggingEnabled is a pointer in the decoded struct so we can
// distinguish "field omitted" (preserve existing value) from "field
// present and false" (explicitly disable). Without that distinction
// a rotate-only call from the UI would silently flip logging off
// because Go's zero-value for bool is false. Returns the new full
// URL the user pastes into Sonarr/Radarr. The previous token is
// immediately invalidated; any URL pasted from a prior rotation
// stops working.
func (s *Server) handleWebhookRotateToken(w http.ResponseWriter, r *http.Request) {
	inst := s.instanceByID(w, r.PathValue("id"))
	if inst == nil {
		return
	}
	var req struct {
		LoggingEnabled *bool `json:"loggingEnabled,omitempty"`
	}
	// Body is optional; missing = preserve existing LoggingEnabled.
	if r.ContentLength > 0 {
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
			writeError(w, 400, "invalid body: "+err.Error())
			return
		}
	}
	token, err := generateWebhookToken()
	if err != nil {
		writeError(w, 500, "generate token: "+err.Error())
		return
	}
	if err := s.App.Config.Update(func(c *core.Config) {
		for i := range c.Instances {
			if c.Instances[i].ID == inst.ID {
				c.Instances[i].Webhook.Token = token
				if req.LoggingEnabled != nil {
					c.Instances[i].Webhook.LoggingEnabled = *req.LoggingEnabled
				}
				return
			}
		}
	}); err != nil {
		writeError(w, 500, "save: "+err.Error())
		return
	}
	// Build the URL the user pastes into Arr. Use the request's
	// Host header (the public-facing hostname the user reached us
	// on); falls back to the configured base URL if we can resolve
	// it. The frontend overrides this with the browser's current
	// origin when displaying — this is just a defensive default
	// for clients that ask the server.
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	host := r.Host
	url := fmt.Sprintf("%s://%s/api/webhooks/%s", scheme, host, token)
	// Re-read the persisted state so the response reflects what
	// actually got saved (including the preserved-or-changed
	// LoggingEnabled flag — see the *bool dance above).
	loggingEnabled := false
	if cfg := s.App.Config.Get(); cfg.Instances != nil {
		for _, in := range cfg.Instances {
			if in.ID == inst.ID {
				loggingEnabled = in.Webhook.LoggingEnabled
				break
			}
		}
	}
	writeJSON(w, map[string]any{
		"token":          token,
		"url":            url,
		"loggingEnabled": loggingEnabled,
	})
}

// handleWebhookGet returns the unmasked webhook config for an instance,
// including the computed URL the user pastes into Sonarr/Radarr. The
// configuration wizard's Summary step + the Webhooks UI's "Copy URL"
// button both call this. Admin-side auth-gated. Returns an empty token
// + URL when no webhook is configured.
func (s *Server) handleWebhookGet(w http.ResponseWriter, r *http.Request) {
	inst := s.instanceByID(w, r.PathValue("id"))
	if inst == nil {
		return
	}
	resp := map[string]any{
		"token":          inst.Webhook.Token,
		"loggingEnabled": inst.Webhook.LoggingEnabled,
		"url":            "",
	}
	if inst.Webhook.Token != "" {
		scheme := "http"
		if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
			scheme = "https"
		}
		resp["url"] = fmt.Sprintf("%s://%s/api/webhooks/%s", scheme, r.Host, inst.Webhook.Token)
	}
	writeJSON(w, resp)
}

// handleWebhookSetLogging toggles just the LoggingEnabled flag on an
// existing webhook config. Useful when the user wants to silence the
// receiver temporarily without rotating the token (and re-pasting in
// Arr). Body: `{"loggingEnabled": bool}`. 409 if no token configured
// (rotate-token first).
func (s *Server) handleWebhookSetLogging(w http.ResponseWriter, r *http.Request) {
	inst := s.instanceByID(w, r.PathValue("id"))
	if inst == nil {
		return
	}
	if inst.Webhook.Token == "" {
		writeError(w, 409, "webhook not configured for this instance — generate a token first")
		return
	}
	var req struct {
		LoggingEnabled bool `json:"loggingEnabled"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
		writeError(w, 400, "invalid body: "+err.Error())
		return
	}
	if err := s.App.Config.Update(func(c *core.Config) {
		for i := range c.Instances {
			if c.Instances[i].ID == inst.ID {
				c.Instances[i].Webhook.LoggingEnabled = req.LoggingEnabled
				return
			}
		}
	}); err != nil {
		writeError(w, 500, "save: "+err.Error())
		return
	}
	writeJSON(w, map[string]any{"status": "ok", "loggingEnabled": req.LoggingEnabled})
}
