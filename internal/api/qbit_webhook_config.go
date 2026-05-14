package api

// qbit_webhook_config.go — per-instance webhook config endpoints for
// M-qBit-add Slice 4. Lets the UI show the user a ready-to-paste curl
// command, auto-configure qBit's "Run external program on torrent
// added" field, rotate the secret, test the endpoint locally, and
// reset (restore the pre-our-config autorun value).
//
// Endpoints (all under /api/qbit-instances/{id}/webhook):
//
//	GET    /                  — return secret + curl + qBit current state
//	POST   /configure         — auto-configure qBit autorun (mode: append/replace)
//	POST   /rotate-secret     — generate new secret + reconfigure qBit if wired
//	POST   /test              — synthetic in-process ping (handler-reachable check)
//	POST   /reset             — restore PreviousAutorunBackup in qBit + clear our state
//
// Auth at this surface: standard session cookie (handlers run inside
// the resolvarr UI). The qBit-side webhook (handleQbitTorrentAdded
// in qbit_event.go) is the unauthenticated public surface that uses
// X-API-Key — these UI-side helpers reveal/manage that key.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"resolvarr/internal/core"
	"resolvarr/internal/qbit"
)

// qbitWebhookConfigResponse is the GET endpoint payload. Fields are
// best-effort: qbitState is populated when the prefs client GET
// succeeds, otherwise QbitFetchError carries the reason and the rest
// of the payload still works (manual paste still viable when qBit's
// API is unreachable / qui blocks reads).
type qbitWebhookConfigResponse struct {
	InstanceID  string             `json:"instanceId"`
	Secret      string             `json:"secret"`      // PLAINTEXT — only via this endpoint
	WebhookURL  string             `json:"webhookUrl"`  // POST target qBit will hit
	CurlCommand string             `json:"curlCommand"` // ready to paste into qBit
	QbitState   *qbitAutorunState  `json:"qbitState,omitempty"`
}

// qbitAutorunState is the snapshot of qBit's current "Run external
// program on torrent added" field. Used both to populate the GET
// response and to drive conflict detection in the configure handler.
type qbitAutorunState struct {
	CurrentProgram    string `json:"currentProgram"`
	CurrentEnabled    bool   `json:"currentEnabled"`
	FetchError        string `json:"fetchError,omitempty"`        // populated when prefs GET failed
	ConfiguredByUs    bool   `json:"configuredByUs"`              // true when currentProgram contains our /api/qbit/torrent-added/
	ThirdPartyContent bool   `json:"thirdPartyContent"`           // true when non-empty + not ours
}

// resolvarrSelfPathPrefix is the substring we look for in qBit's
// autorun program field to detect "this is our curl, not somebody
// else's script". Substring-match across the whole URL pattern (any
// instance ID) — handles multi-instance setups where one resolvarr
// configures multiple qBit instances.
const resolvarrSelfPathPrefix = "/api/qbit/torrent-added/"

// handleQbitWebhookConfig returns the per-instance webhook config:
// secret (plaintext), generated curl command, and qBit's current
// autorun state. Used by the UI to populate the "Webhook hook"
// section on each qBit instance card.
//
// Idempotent — every GET re-fetches qBit's preferences fresh, so
// the conflict-detection state is always current.
func (s *Server) handleQbitWebhookConfig(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, 400, "id required")
		return
	}
	cfg := s.App.Config.Get()
	inst := findQbitInstanceByID(cfg, id)
	if inst == nil {
		writeError(w, 404, "qBit instance not found")
		return
	}

	// Backfill safety — if the instance somehow lost its secret
	// (manual config edit, migration race), generate one now and
	// persist before returning. UI's "show curl" needs a real value.
	if inst.WebhookSecret == "" {
		secret, err := generateWebhookSecret()
		if err != nil {
			writeError(w, 500, "generate webhook secret: "+err.Error())
			return
		}
		if err := s.App.Config.Update(func(c *core.Config) {
			for i := range c.QbitInstances {
				if c.QbitInstances[i].ID == id {
					c.QbitInstances[i].WebhookSecret = secret
					return
				}
			}
		}); err != nil {
			writeError(w, 500, "save webhook secret: "+err.Error())
			return
		}
		inst.WebhookSecret = secret
	}

	webhookURL := buildResolvarrWebhookURL(r, id)
	curl := buildQbitCurlCommand(webhookURL, inst.WebhookSecret)

	resp := qbitWebhookConfigResponse{
		InstanceID:  id,
		Secret:      inst.WebhookSecret,
		WebhookURL:  webhookURL,
		CurlCommand: curl,
		QbitState:   s.fetchQbitAutorunState(r.Context(), inst),
	}
	writeJSON(w, resp)
}

// fetchQbitAutorunState reads qBit's current autorun state for
// conflict-detection purposes. Best-effort — wraps any error into
// the FetchError field so the GET endpoint can still return a
// useful response (curl + secret) even when qBit / qui is
// unreachable. ConfiguredByUs vs ThirdPartyContent is computed
// from the program string via substring match.
func (s *Server) fetchQbitAutorunState(ctx context.Context, inst *core.QbitInstance) *qbitAutorunState {
	state := &qbitAutorunState{}
	client, err := qbit.New(qbit.Config{
		URL:          inst.URL,
		Username:     inst.Username,
		Password:     inst.Password,
		TrustedCerts: inst.TrustedCerts,
	})
	if err != nil {
		state.FetchError = "qbit client init: " + err.Error()
		return state
	}
	// Cap the fetch — don't let a slow qBit hang the UI.
	fetchCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	program, enabled, err := client.GetAutorunOnAdded(fetchCtx)
	if err != nil {
		state.FetchError = err.Error()
		return state
	}
	state.CurrentProgram = program
	state.CurrentEnabled = enabled
	if program == "" {
		// Empty program — neither ours nor third-party.
		return state
	}
	if strings.Contains(program, resolvarrSelfPathPrefix) {
		state.ConfiguredByUs = true
	} else {
		state.ThirdPartyContent = true
	}
	return state
}

// qbitConfigureRequest is the POST /configure body.
type qbitConfigureRequest struct {
	Mode string `json:"mode"` // "append" | "replace" (ignored when current is empty / ours)
}

// handleQbitConfigureWebhook auto-configures qBit's autorun field via
// the prefs client. Three branches based on conflict detection:
//
//	state            → action
//	-------------------------
//	empty            → SET ours; clear PreviousAutorunBackup
//	already ours     → idempotent (no qBit write); return current
//	third-party      → mode=append → "<existing>; <ours>"; backup=existing
//	                   mode=replace → ours; backup=existing
//
// Any qBit-side failure (GET or SET) returns 502 with the qBit error
// in the body — UI can detect "qui blocked this" by examining the
// message and surface "Show command for manual paste" as the fallback.
func (s *Server) handleQbitConfigureWebhook(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, 400, "id required")
		return
	}
	var req qbitConfigureRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4*1024)).Decode(&req); err != nil {
		writeError(w, 400, "invalid body: "+err.Error())
		return
	}
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = "append" // safe default
	}
	if mode != "append" && mode != "replace" {
		writeError(w, 400, "mode must be \"append\" or \"replace\"")
		return
	}

	cfg := s.App.Config.Get()
	inst := findQbitInstanceByID(cfg, id)
	if inst == nil {
		writeError(w, 404, "qBit instance not found")
		return
	}
	if inst.WebhookSecret == "" {
		// Defensive — Slice 1 backfill should have stamped one. If
		// somehow missing, generate before continuing.
		secret, err := generateWebhookSecret()
		if err != nil {
			writeError(w, 500, "generate webhook secret: "+err.Error())
			return
		}
		inst.WebhookSecret = secret
	}

	client, err := qbit.New(qbit.Config{
		URL:          inst.URL,
		Username:     inst.Username,
		Password:     inst.Password,
		TrustedCerts: inst.TrustedCerts,
	})
	if err != nil {
		writeError(w, 500, "qbit client init: "+err.Error())
		return
	}

	// Cap the qBit roundtrip — auto-configure is interactive; users
	// hate clicks that hang.
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	currentProgram, _, err := client.GetAutorunOnAdded(ctx)
	if err != nil {
		writeError(w, 502, "read qBit preferences: "+err.Error())
		return
	}

	webhookURL := buildResolvarrWebhookURL(r, id)
	ourCommand := buildQbitCurlCommand(webhookURL, inst.WebhookSecret)

	var newProgram string
	var backup string
	switch {
	case currentProgram == "":
		newProgram = ourCommand
		backup = "" // nothing to back up

	case strings.Contains(currentProgram, resolvarrSelfPathPrefix):
		// Already ours — surgical replace via replaceResolvarrLine
		// preserves any surrounding third-party scripts the user
		// added via Append mode previously (or by hand). Blanket
		// overwrite would destroy `notify.sh; <our-curl>; log.sh`
		// scenarios. The replace is also how we refresh a stale
		// secret on the existing line — same helper rotate-secret
		// uses, so behaviour is consistent across both code paths.
		newProgram = replaceResolvarrLine(currentProgram, ourCommand)
		backup = "" // no third-party content to back up — already had our line

	default:
		// Third-party content — preserve via mode.
		backup = currentProgram
		switch mode {
		case "append":
			// Semicolon-separator runs both commands. qBit invokes
			// the field via shell; ; ignores exit codes so a failure
			// in the existing script doesn't block ours and vice
			// versa.
			newProgram = currentProgram + "; " + ourCommand
		case "replace":
			newProgram = ourCommand
		}
	}

	// Write to qBit FIRST. If qBit rejects (qui blocks, network),
	// we abort BEFORE persisting WebhookConfiguredInQbit so our
	// stored state stays accurate.
	if err := client.SetAutorunOnAdded(ctx, newProgram, true); err != nil {
		writeError(w, 502, "write qBit preferences: "+err.Error())
		return
	}

	// Persist the updated state. Backup is set when we overwrote
	// third-party content; cleared otherwise (idempotent re-Configure
	// shouldn't lose an earlier backup, so only WRITE the backup field
	// if backup is non-empty — preserves existing backup on subsequent
	// idempotent runs).
	if err := s.App.Config.Update(func(c *core.Config) {
		for i := range c.QbitInstances {
			if c.QbitInstances[i].ID == id {
				c.QbitInstances[i].WebhookConfiguredInQbit = true
				if backup != "" {
					c.QbitInstances[i].PreviousAutorunBackup = backup
				}
				return
			}
		}
	}); err != nil {
		writeError(w, 500, "persist configured-state: "+err.Error())
		return
	}

	writeJSON(w, map[string]any{
		"success":    true,
		"newProgram": newProgram,
		"backedUp":   backup != "",
	})
}

// handleQbitRotateWebhookSecret generates a fresh secret + persists.
// If the instance was previously auto-configured (WebhookConfiguredInQbit),
// also pushes the new secret into qBit's autorun field so the hook
// stays working without manual re-paste.
//
// On qBit-write failure during the auto-update phase: secret is still
// rotated locally, but the response indicates qBit is now out of sync
// — UI prompts user to re-Configure.
func (s *Server) handleQbitRotateWebhookSecret(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, 400, "id required")
		return
	}
	cfg := s.App.Config.Get()
	inst := findQbitInstanceByID(cfg, id)
	if inst == nil {
		writeError(w, 404, "qBit instance not found")
		return
	}

	newSecret, err := generateWebhookSecret()
	if err != nil {
		writeError(w, 500, "generate secret: "+err.Error())
		return
	}

	// Persist new secret first — local rotation always wins, qBit-side
	// update is best-effort.
	if err := s.App.Config.Update(func(c *core.Config) {
		for i := range c.QbitInstances {
			if c.QbitInstances[i].ID == id {
				c.QbitInstances[i].WebhookSecret = newSecret
				return
			}
		}
	}); err != nil {
		writeError(w, 500, "persist new secret: "+err.Error())
		return
	}

	resp := map[string]any{
		"success":         true,
		"secret":          newSecret,
		"qbitOutOfSync":   false,
		"qbitUpdateError": "",
	}

	if !inst.WebhookConfiguredInQbit {
		// Wasn't auto-configured — nothing to push. UI shows the new
		// secret + curl; user manually updates qBit if they had pasted.
		writeJSON(w, resp)
		return
	}

	// Push new secret into qBit. Use Configure-replace semantics on
	// just our own line: GET current, replace any existing
	// /api/qbit/torrent-added/ line with new curl, SET back.
	client, err := qbit.New(qbit.Config{
		URL:          inst.URL,
		Username:     inst.Username,
		Password:     inst.Password,
		TrustedCerts: inst.TrustedCerts,
	})
	if err != nil {
		resp["qbitOutOfSync"] = true
		resp["qbitUpdateError"] = "qbit client init: " + err.Error()
		writeJSON(w, resp)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	currentProgram, _, err := client.GetAutorunOnAdded(ctx)
	if err != nil {
		resp["qbitOutOfSync"] = true
		resp["qbitUpdateError"] = "read qBit preferences: " + err.Error()
		writeJSON(w, resp)
		return
	}
	webhookURL := buildResolvarrWebhookURL(r, id)
	newCurl := buildQbitCurlCommand(webhookURL, newSecret)
	newProgram := replaceResolvarrLine(currentProgram, newCurl)
	if err := client.SetAutorunOnAdded(ctx, newProgram, true); err != nil {
		resp["qbitOutOfSync"] = true
		resp["qbitUpdateError"] = "write qBit preferences: " + err.Error()
		writeJSON(w, resp)
		return
	}
	writeJSON(w, resp)
}

// handleQbitTestWebhookEndpoint synthetically POSTs to our own
// handleQbitTorrentAdded with a fake event, validating that the
// receiver path is wired correctly + the secret-compare passes.
// Does NOT verify qBit→resolvarr network reachability — only an
// actual qBit add can do that. UI labels this accordingly.
//
// Known v1 limitation: the synthetic event flows through the full
// handler including buffer enqueue. If any qbitSeTag rule exists
// for this instance with Unmatched enabled, the test event will
// eventually fire the flush + write a history entry tagged with
// the synthetic name. The marker name below is chosen to avoid
// the Episode + Season patterns (no S/E pattern in it) so it can
// only land on Unmatched. Refined skip-buffer path can come later
// if user complains about test events appearing in History.
func (s *Server) handleQbitTestWebhookEndpoint(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, 400, "id required")
		return
	}
	cfg := s.App.Config.Get()
	inst := findQbitInstanceByID(cfg, id)
	if inst == nil {
		writeError(w, 404, "qBit instance not found")
		return
	}
	if inst.WebhookSecret == "" {
		writeError(w, 409, "instance has no webhook secret — visit GET /webhook to generate one")
		return
	}

	// Build a synthetic POST against our own handler. Doesn't traverse
	// the network — just validates handler wiring.
	// Marker name deliberately lacks S/E pattern (S\d+E\d+ / bare S\d+
	// / "Season \d+") so the classifier doesn't tag it as Episode or
	// Season. Lands on Unmatched only if user has that branch on.
	body := strings.NewReader("infoHash=resolvarr-test-ping&name=RESOLVARR-TEST-PING-DO-NOT-USE&category=resolvarr-test")
	req := httptest.NewRequest(http.MethodPost, "/api/qbit/torrent-added/"+id, body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-API-Key", inst.WebhookSecret)
	req.SetPathValue("instanceId", id)
	rr := httptest.NewRecorder()
	s.handleQbitTorrentAdded(rr, req)

	if rr.Code != http.StatusAccepted {
		writeError(w, 502, fmt.Sprintf("synthetic test failed: handler returned %d — %s", rr.Code, strings.TrimSpace(rr.Body.String())))
		return
	}
	writeJSON(w, map[string]any{
		"success":         true,
		"handlerResponse": json.RawMessage(rr.Body.Bytes()),
		"note":            "Resolvarr's receiver is reachable. This does NOT verify qBit can reach resolvarr — only an actual qBit add proves end-to-end connectivity.",
	})
}

// handleQbitResetWebhook restores the pre-our-config autorun value
// in qBit (or clears the field if there was no backup), then clears
// PreviousAutorunBackup + WebhookConfiguredInQbit. After this, the
// hook stops firing and qBit's autorun is back to whatever the user
// had before they clicked Configure.
func (s *Server) handleQbitResetWebhook(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, 400, "id required")
		return
	}
	cfg := s.App.Config.Get()
	inst := findQbitInstanceByID(cfg, id)
	if inst == nil {
		writeError(w, 404, "qBit instance not found")
		return
	}

	client, err := qbit.New(qbit.Config{
		URL:          inst.URL,
		Username:     inst.Username,
		Password:     inst.Password,
		TrustedCerts: inst.TrustedCerts,
	})
	if err != nil {
		writeError(w, 500, "qbit client init: "+err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	restoreProgram := inst.PreviousAutorunBackup
	restoreEnabled := restoreProgram != "" // empty backup = field was empty + disabled before

	if err := client.SetAutorunOnAdded(ctx, restoreProgram, restoreEnabled); err != nil {
		writeError(w, 502, "write qBit preferences: "+err.Error())
		return
	}

	// Clear our state regardless — the rollback in qBit succeeded.
	if err := s.App.Config.Update(func(c *core.Config) {
		for i := range c.QbitInstances {
			if c.QbitInstances[i].ID == id {
				c.QbitInstances[i].PreviousAutorunBackup = ""
				c.QbitInstances[i].WebhookConfiguredInQbit = false
				return
			}
		}
	}); err != nil {
		writeError(w, 500, "persist reset state: "+err.Error())
		return
	}

	writeJSON(w, map[string]any{
		"success":            true,
		"restoredProgram":    restoreProgram,
		"restoredEnabled":    restoreEnabled,
		"hadPreviousBackup":  restoreProgram != "",
	})
}

// ---- helpers --------------------------------------------------------

// buildResolvarrWebhookURL returns the URL qBit will POST to. Uses
// the request's Host header (whatever public hostname the user
// reached the resolvarr UI at) — same pattern as the existing
// Sonarr/Radarr webhook URL builder. User can edit the curl command
// if their network setup means qBit should hit a different address
// (rare — usually qBit + resolvarr are on the same Docker network /
// LAN).
func buildResolvarrWebhookURL(r *http.Request, instanceID string) string {
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s/api/qbit/torrent-added/%s", scheme, r.Host, instanceID)
}

// buildQbitCurlCommand renders the curl command users paste into
// qBit's "Run external program on torrent added" field. Format:
//
//	curl -fsS -X POST "<URL>" \
//	  -H "X-API-Key: <SECRET>" \
//	  --data-urlencode "infoHash=%I" \
//	  --data-urlencode "name=%N" \
//	  --data-urlencode "category=%L"
//
// `-fsS` = silent + show-errors-on-failure + non-zero exit on HTTP
// error. Safe for `;`-append with existing scripts (semicolon
// ignores exit codes; following commands run regardless).
//
// %I / %N / %L are qBit placeholders (info hash / name / category).
// We escape them as %%I etc. via fmt.Sprintf so they survive the
// Go format pass and arrive in qBit's field literally.
func buildQbitCurlCommand(url, secret string) string {
	return fmt.Sprintf(
		"curl -fsS -X POST %q \\\n  -H %q \\\n  --data-urlencode \"infoHash=%%I\" \\\n  --data-urlencode \"name=%%N\" \\\n  --data-urlencode \"category=%%L\"",
		url, "X-API-Key: "+secret,
	)
}

// replaceResolvarrLine surgically swaps the existing /api/qbit/
// torrent-added/ curl line for a new one, leaving any surrounding
// third-party scripts intact. Used by rotate-secret so the user
// doesn't have to re-Configure after a rotate.
//
// Algorithm: split on "; " (qBit's recommended multi-command
// separator), find the part containing our path prefix, replace
// in-place, rejoin. If multiple parts match (unusual — duplicate
// curl from a buggy auto-config), only the first is replaced;
// extras pass through untouched (will be cleaned up next Configure).
//
// If no part matches (e.g. qBit autorun was edited externally to
// remove our line), returns the input unchanged + relies on caller
// to surface "qBit out of sync" so user re-Configures.
func replaceResolvarrLine(currentProgram, newCurl string) string {
	if !strings.Contains(currentProgram, resolvarrSelfPathPrefix) {
		return currentProgram
	}
	parts := strings.Split(currentProgram, "; ")
	for i, p := range parts {
		if strings.Contains(p, resolvarrSelfPathPrefix) {
			parts[i] = newCurl
			break
		}
	}
	return strings.Join(parts, "; ")
}
