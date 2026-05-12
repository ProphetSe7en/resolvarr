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
	"os"
	"strings"
	"sync"
	"time"

	"resolvarr/internal/core"
)

// webhookBodyMaxBytes caps the POST body. Connect events in the wild
// run ~1-50 KB; 1 MB is 20× margin, prevents a hostile/buggy client
// from streaming gigabytes into our memory.
const webhookBodyMaxBytes = 1 << 20 // 1 MiB

// authLogWindow is the per-(instance,reason) coalescing window for
// rejected-auth and unsigned-grace-mode ring-buffer appends. After the
// first matching event in a window, subsequent matching events are
// dropped silently; the first event in the next window logs again,
// giving the user a "still happening" nudge without drowning the
// 100-entry ring buffer in repeated warnings.
//
// Five minutes is a tradeoff: short enough that the user sees a fresh
// warning if they refresh the UI 10 minutes after a misconfiguration
// started, long enough that a 30-second Health-poll loop or an
// auth-flood attacker can't push real events out of the ring within
// a typical browse session.
const authLogWindow = 5 * time.Minute

// authLogRateLimiter coalesces (rejected) / (unsigned) ring-buffer
// appends per instance to prevent log poisoning. State lives on the
// Server (one map per Server lifetime). Per-instance + per-reason
// keying so a flooding token's "auth failed" doesn't suppress a
// different real-issue warning. Different rejection reasons
// (missing-header vs wrong-secret) stay separately rate-limited so
// each distinct misconfiguration gets at least one log entry per
// window.
//
// now is injectable for tests (defaults to time.Now). Server restart
// resets state — acceptable; a fresh start logs the first event again.
type authLogRateLimiter struct {
	mu       sync.Mutex
	lastSeen map[string]time.Time // key: instanceID + ":" + reason
	now      func() time.Time     // nil → time.Now
}

// shouldLog returns true the first time a given (instance, reason)
// pair is seen within authLogWindow, false otherwise. Mutating the
// lastSeen map on every call (including suppressed ones would be
// fine too) is intentional: we record the "last attempt" only on
// the first log of a window, so a steady attack stream with one
// log every 5 minutes shows the user a "still happening" cadence
// rather than going completely silent.
func (l *authLogRateLimiter) shouldLog(instanceID, reason string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.lastSeen == nil {
		l.lastSeen = make(map[string]time.Time)
	}
	nowFn := l.now
	if nowFn == nil {
		nowFn = time.Now
	}
	now := nowFn()
	key := instanceID + ":" + reason
	if last, seen := l.lastSeen[key]; seen && now.Sub(last) < authLogWindow {
		return false
	}
	l.lastSeen[key] = now
	return true
}

// shouldLogAuthEvent is the Server-side entry point for the auth log
// rate-limiter. Lazily allocates the limiter on first use — older
// tests build `Server{}` directly without going through NewServer,
// and we don't want them to nil-deref here.
//
// Concurrent-safe via authLogRateLimiter's internal mutex; the
// lazy-alloc itself races against parallel callers but the worst case
// is one wasted struct (the second writer's struct gets dropped on
// the floor when the first wins). Production goes through NewServer
// which sets the field up-front, so this race is test-only.
func (s *Server) shouldLogAuthEvent(instanceID, reason string) bool {
	if s.authLogLimiter == nil {
		s.authLogLimiter = &authLogRateLimiter{}
	}
	return s.authLogLimiter.shouldLog(instanceID, reason)
}

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

// generateWebhookSecret returns a fresh base64url-encoded random secret
// for the shared-secret-as-Basic-auth-password authentication. Same
// entropy + encoding as the URL token because the same threat model
// applies (must be infeasible to brute-force, must be URL-safe enough
// to copy-paste into Sonarr/Radarr's password field which sometimes
// disallows specific characters).
func generateWebhookSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// validateWebhookAuth checks the Authorization: Basic header against
// the instance's stored Secret. Returns (true, "") on pass.
//
// Modes:
//   - RequireSignature=false + no auth header → pass (legacy/grace
//     mode). Caller should log a warning to the ring-buffer so the user
//     sees the "you should turn on Require signature" reminder.
//   - RequireSignature=false + auth header present + matches → pass
//   - RequireSignature=false + auth header present + mismatch → fail.
//     The user TRIED to authenticate (they pasted SOMETHING into the
//     password field), the mismatch means a misconfiguration we should
//     surface rather than silently accept.
//   - RequireSignature=true + no auth header → fail
//   - RequireSignature=true + auth header present + matches → pass
//   - RequireSignature=true + auth header present + mismatch → fail
//
// Empty Secret on the instance is a config-time error — the validator
// on the require-signature endpoint prevents saving RequireSignature=
// true with empty Secret. If we hit that combination at fire-time
// anyway (manually-edited config.json, future bug), fail closed.
//
// Constant-time compare against the secret defangs a per-byte timing
// oracle. Probably not weaponisable over HTTP for a 256-bit secret,
// but subtle.ConstantTimeCompare is one line and keeps the receiver
// honest if entropy ever drops in a future change.
func validateWebhookAuth(r *http.Request, secret string, requireSignature bool) (bool, string) {
	authHeader := r.Header.Get("Authorization")

	// Grace mode: no auth header AND not required → pass (legacy).
	if authHeader == "" {
		if requireSignature {
			return false, "missing Authorization header (require-signature is on)"
		}
		return true, ""
	}

	// Decode Basic auth. Reject non-Basic schemes outright — Sonarr/
	// Radarr only emits Basic, so anything else is either a bug or an
	// attempt to probe for a different auth surface.
	//
	// RFC 7235 §2.1 says the auth scheme is case-insensitive. Sonarr/
	// Radarr emit canonical "Basic " today, but a future client-library
	// change could ship "basic " / "bAsIc " and we'd reject a perfectly
	// valid request. EqualFold on the scheme token keeps us spec-correct.
	// The single-space separator between scheme and value is what RFC
	// 7235 specifies (and what every real Connect client sends); we
	// don't try to tolerate tabs or multiple spaces here.
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Basic") {
		return false, "Authorization scheme must be Basic"
	}
	encoded := parts[1]
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return false, "malformed base64 in Authorization header"
	}
	credParts := strings.SplitN(string(decoded), ":", 2)
	if len(credParts) != 2 {
		return false, "malformed credentials format"
	}
	suppliedSecret := credParts[1]

	// Empty stored Secret is a configuration error — the validator
	// on /require-signature prevents saving the strict+empty
	// combination, and even in grace mode a request with an auth
	// header that hits an empty stored Secret can never compare
	// equal in a meaningful way. Fail closed.
	if secret == "" {
		return false, "instance has no Secret configured"
	}
	if subtle.ConstantTimeCompare([]byte(suppliedSecret), []byte(secret)) != 1 {
		return false, "Authorization secret does not match"
	}
	return true, ""
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

	// Shared-secret-as-Basic-auth-password validation. Sonarr/Radarr's
	// Webhook implementation sends the user-configured username +
	// password as standard HTTP Basic auth (Authorization: Basic
	// <base64(user:pass)>). The user pastes the resolvarr-generated
	// Secret as the password field; any non-empty username works.
	//
	// Grace mode (RequireSignature=false): unsigned events pass with
	// a warning to the ring-buffer so the user sees the "you should
	// turn on Require signature" reminder in Recent activity.
	//
	// Strict mode (RequireSignature=true): missing/wrong Auth header
	// rejects with 401 + a rejected-event entry in the ring-buffer
	// so debugging is straightforward from the UI.
	authOK, authReason := validateWebhookAuth(r, inst.Webhook.Secret, inst.Webhook.RequireSignature)
	if !authOK {
		// Log to ring-buffer for visibility in Recent activity. The
		// EventType "(rejected)" makes the row obviously distinct from
		// real Connect events; the rejection reason lands in the
		// Title field so it's visible without expanding the row.
		//
		// Rate-limited per (instance, reason): an attacker spamming
		// the URL with bad creds, or a misconfigured client looping
		// at 30 s intervals, would otherwise evict every legitimate
		// event from the 100-entry ring within ~50 seconds and
		// hammer the on-disk persist (~500 KB × 100 / minute).
		// First event in the 5-minute window logs; subsequent
		// matching events are dropped silently. Different reasons
		// (missing header vs wrong secret) stay separately limited,
		// so a distinct misconfiguration still surfaces.
		if s.WebhookLog != nil && s.shouldLogAuthEvent(inst.ID, "auth-rejected:"+authReason) {
			s.WebhookLog.append(WebhookEvent{
				ID:         genID(),
				InstanceID: inst.ID,
				ReceivedAt: time.Now().UTC(),
				EventType:  "(rejected)",
				Title:      "Authentication rejected: " + authReason,
				Raw:        json.RawMessage(`null`),
			})
		}
		writeError(w, 401, "authentication failed: "+authReason)
		return
	}
	// Grace-mode soft-warning: auth header was absent + RequireSignature
	// is off. Log to the ring-buffer so the user sees the nudge to flip
	// strict mode once they've pasted the Secret into Sonarr/Radarr.
	// We only emit the warning on the unsigned path (no Authorization
	// header at all) — a present-but-matching header in grace mode is
	// the happy path during opt-in transition and doesn't need a warning.
	//
	// Rate-limited per instance via the same limiter as rejections,
	// keyed separately ("auth-unsigned") so a chatty Connect setup
	// emitting Health events every 30 seconds doesn't fill 99 of 100
	// ring slots with (unsigned) warnings.
	if !inst.Webhook.RequireSignature && r.Header.Get("Authorization") == "" && s.WebhookLog != nil && s.shouldLogAuthEvent(inst.ID, "auth-unsigned") {
		s.WebhookLog.append(WebhookEvent{
			ID:         genID(),
			InstanceID: inst.ID,
			ReceivedAt: time.Now().UTC(),
			EventType:  "(unsigned)",
			Title:      "Received without Authorization header — paste the Secret into Sonarr/Radarr's Webhook password field and turn on 'Require signature'",
			Raw:        json.RawMessage(`null`),
		})
	}

	// Read body with cap. Sonarr/Radarr send ~1-50 KB; 1 MiB ceiling
	// is paranoid but cheap. ContentLength check first so an honest
	// oversize request rejects without buffering. Body is read even
	// when LoggingEnabled is off — webhook RULES fire on the dispatcher
	// path independent of logging, so we always need the parsed
	// envelope.
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

	// Logging persists when LoggingEnabled is on. The dispatcher path
	// runs on every event regardless — rules are independent of the
	// raw-event log.
	logged := false
	logCount := 0
	if inst.Webhook.LoggingEnabled {
		ev := WebhookEvent{
			ID:         genID(),
			InstanceID: inst.ID,
			ReceivedAt: time.Now().UTC(),
			EventType:  env.EventType,
			Title:      title,
			Subtitle:   subtitle,
			Raw:        rawForStorage,
		}
		logCount = s.WebhookLog.append(ev)
		logged = true
	}

	// Rule dispatch — walks Config.WebhookRules for the resolved
	// instance and fires matching rules' enabled functions. Today's
	// adapters are stubs (see webhook_dispatch.go); real engine
	// calls land per-function in the upcoming tasks.
	rulesFired := s.dispatchWebhookRules(r.Context(), inst, &env, body)

	writeJSON(w, map[string]any{
		"status":     "ok",
		"logged":     logged,
		"eventType":  env.EventType,
		"count":      logCount,
		"rulesFired": rulesFired,
	})
}

// handleWebhookEventsStream is the SSE push channel. Browser opens
// EventSource('/api/instances/{id}/webhook/events/stream'), server
// holds the connection open, fan-out from webhookLog.append delivers
// new events as they arrive. Connection closes when the browser
// navigates away (r.Context().Done()) or the server shuts down.
//
// Format: each event is one SSE record like
//   event: webhook
//   data: <one-line JSON of WebhookEvent>
//   <blank line>
// Plus a `: heartbeat` comment every 25s to keep proxies / load
// balancers from idle-closing the connection. EventSource handles
// reconnect automatically on connection drop.
func (s *Server) handleWebhookEventsStream(w http.ResponseWriter, r *http.Request) {
	if s.WebhookLog == nil {
		writeError(w, 503, "webhook log not initialised")
		return
	}
	inst := s.instanceByID(w, r.PathValue("id"))
	if inst == nil {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		// Theoretically possible behind a buffering reverse proxy
		// that strips the Flusher interface. Fall back to a 501
		// rather than silently leak the connection.
		writeError(w, 501, "streaming not supported on this connection")
		return
	}

	// SSE headers. Cache-Control: no-cache prevents a sneaky proxy
	// from caching the stream; X-Accel-Buffering: no tells nginx
	// to flush each chunk through immediately.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-store")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	// Send an initial comment so the EventSource onopen fires
	// immediately — browsers wait for any data before transitioning
	// from CONNECTING to OPEN.
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	ch, unsubscribe := s.WebhookLog.Subscribe(inst.ID)
	if ch == nil {
		// Subscriber cap hit — too many active streams on this
		// instance. Headers already sent (200 OK), so we can't
		// switch to 503; write a final SSE error event + close.
		fmt.Fprint(w, "event: error\ndata: {\"error\":\"subscriber cap reached — close some browser tabs and retry\"}\n\n")
		flusher.Flush()
		return
	}
	defer unsubscribe()

	heartbeat := time.NewTicker(25 * time.Second)
	defer heartbeat.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			// SSE comment line — recognised by the spec, ignored
			// by EventSource. Just keeps the TCP connection
			// alive past proxy idle timeouts (typically 30-60s).
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case ev, ok := <-ch:
			if !ok {
				// Channel closed (server shutdown / forced
				// unsubscribe). Bail.
				return
			}
			payload, err := json.Marshal(ev)
			if err != nil {
				// Skip — log to stderr but keep the stream open.
				fmt.Fprintf(os.Stderr, "resolvarr: SSE marshal: %v\n", err)
				continue
			}
			// json.Marshal never emits raw newlines in its output
			// (it escapes them as \n in strings), so the SSE
			// `data:` line is single-line by construction.
			// Switching to MarshalIndent would break framing.
			if _, err := fmt.Fprintf(w, "event: webhook\ndata: %s\n\n", payload); err != nil {
				return
			}
			flusher.Flush()
		}
	}
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
	// Generate Secret alongside Token so the user gets both artifacts
	// in one click. The Secret is the shared password they paste into
	// Sonarr/Radarr → Connect → Webhook → password.
	//
	// On rotate (instance already had a token), the existing
	// RequireSignature flag is preserved BUT the user will need to
	// re-paste the new Secret into Sonarr/Radarr's Connect config
	// before strict mode keeps working — the UI surfaces this in
	// the rotation toast and in the rotate-button title text.
	secret, err := generateWebhookSecret()
	if err != nil {
		writeError(w, 500, "generate secret: "+err.Error())
		return
	}
	if err := s.App.Config.Update(func(c *core.Config) {
		for i := range c.Instances {
			if c.Instances[i].ID == inst.ID {
				c.Instances[i].Webhook.Token = token
				c.Instances[i].Webhook.Secret = secret
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
	// LoggingEnabled + RequireSignature flags). RequireSignature is
	// preserved by Update (we only wrote Token/Secret/LoggingEnabled),
	// so the UI can tell the user "your old strict mode is still on,
	// but you need to paste the new Secret into Sonarr/Radarr to keep
	// events flowing".
	loggingEnabled := false
	requireSignature := false
	if cfg := s.App.Config.Get(); cfg.Instances != nil {
		for _, in := range cfg.Instances {
			if in.ID == inst.ID {
				loggingEnabled = in.Webhook.LoggingEnabled
				requireSignature = in.Webhook.RequireSignature
				break
			}
		}
	}
	// TODO(M-Webhook Phase 2 Slices C-H): when Auto-configure ships,
	// the response will optionally include the Arr-side notification
	// ID + a "wired" flag so the UI can show "Auto-configured — Sonarr
	// knows the new Secret" instead of "paste this into Sonarr". Until
	// then the manual paste-flow is the only path and the UI surfaces
	// the Secret prominently in the wizard's Summary step.
	writeJSON(w, map[string]any{
		"token":            token,
		"secret":           secret,
		"url":              url,
		"loggingEnabled":   loggingEnabled,
		"requireSignature": requireSignature,
	})
}

// handleWebhookGet returns the unmasked webhook config for an instance,
// including the computed URL the user pastes into Sonarr/Radarr. The
// configuration wizard's Summary step + the Webhooks UI's "Copy URL"
// button both call this. Admin-side auth-gated. Returns an empty token
// + URL when no webhook is configured.
//
// Secret and RequireSignature are also unmasked here — the wizard's
// Summary step renders the Secret in a copy-to-clipboard input so the
// user can paste it into Sonarr/Radarr's Connect → Webhook → password
// field. The broader /api/config endpoint masks both Token and Secret
// (see handleGetConfig) for any consumer that might log the response.
func (s *Server) handleWebhookGet(w http.ResponseWriter, r *http.Request) {
	inst := s.instanceByID(w, r.PathValue("id"))
	if inst == nil {
		return
	}
	resp := map[string]any{
		"token":            inst.Webhook.Token,
		"secret":           inst.Webhook.Secret,
		"requireSignature": inst.Webhook.RequireSignature,
		"loggingEnabled":   inst.Webhook.LoggingEnabled,
		"url":              "",
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

// handleWebhookDelete clears the webhook configuration for an instance
// — wipes Token + LoggingEnabled so the receiver path stops accepting
// the previous URL and the row reverts to its "not configured" state.
// 200 on success regardless of whether a token was set (idempotent;
// caller can hit this safely without checking first). The recent-
// activity log under /config/webhook-events.json is NOT touched —
// it's keyed by instance ID, not by token, and the user has a
// separate Clear log button for that.
func (s *Server) handleWebhookDelete(w http.ResponseWriter, r *http.Request) {
	inst := s.instanceByID(w, r.PathValue("id"))
	if inst == nil {
		return
	}
	if err := s.App.Config.Update(func(c *core.Config) {
		for i := range c.Instances {
			if c.Instances[i].ID == inst.ID {
				c.Instances[i].Webhook.Token = ""
				c.Instances[i].Webhook.Secret = ""
				c.Instances[i].Webhook.RequireSignature = false
				c.Instances[i].Webhook.LoggingEnabled = false
				return
			}
		}
	}); err != nil {
		writeError(w, 500, "save: "+err.Error())
		return
	}
	writeJSON(w, map[string]any{"status": "ok"})
}

// handleWebhookSetRequireSignature toggles the RequireSignature flag
// on an existing webhook config. Body: `{"enabled": bool}`.
//
// Validator: rejects {enabled:true} with 400 when the instance's
// stored Secret is empty — flipping strict mode on with no secret
// configured would brick the receiver path (the validator would
// fail-close on every incoming event with "instance has no Secret
// configured"). The user must click Configure webhook first to
// generate a Secret, then come back to flip strict mode.
//
// Returns 409 if no token is configured at all (same idempotency
// gate as handleWebhookSetLogging — "configure first, then tune
// flags").
func (s *Server) handleWebhookSetRequireSignature(w http.ResponseWriter, r *http.Request) {
	inst := s.instanceByID(w, r.PathValue("id"))
	if inst == nil {
		return
	}
	if inst.Webhook.Token == "" {
		writeError(w, 409, "webhook not configured for this instance — generate a token first")
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
		writeError(w, 400, "invalid body: "+err.Error())
		return
	}
	// Validator: turning strict mode on requires a stored Secret. If
	// the user is turning it OFF that's always allowed (downgrades to
	// grace mode never need the secret to validate).
	if req.Enabled && inst.Webhook.Secret == "" {
		writeError(w, 400, "Generate a secret first by clicking Configure webhook")
		return
	}
	if err := s.App.Config.Update(func(c *core.Config) {
		for i := range c.Instances {
			if c.Instances[i].ID == inst.ID {
				c.Instances[i].Webhook.RequireSignature = req.Enabled
				return
			}
		}
	}); err != nil {
		writeError(w, 500, "save: "+err.Error())
		return
	}
	writeJSON(w, map[string]any{"status": "ok", "requireSignature": req.Enabled})
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
