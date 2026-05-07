package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"resolvarr/internal/arr"
	"resolvarr/internal/auth"
	"resolvarr/internal/core"
	"resolvarr/internal/netsec"
)

// reRadarrTagLabel mirrors Radarr's TagController.cs validator
// (`^[a-z0-9-]+$` with RegexOptions.IgnoreCase). We additionally
// pre-lowercase the label before checking + sending to the Arr,
// matching what TagService.Add does server-side via
// ToLowerInvariant. Sonarr's TagController has no validator, so
// non-empty is the only constraint. Both Arrs use a case-sensitive
// FindByLabel-then-lowercase dedup path, so sending pre-lowercased
// avoids a UNIQUE-constraint trip when an existing tag already
// matches case-insensitively.
var reRadarrTagLabel = regexp.MustCompile(`^[a-z0-9-]+$`)

// normalizeTagLabel returns the trimmed + lowercased form of label
// (what both Arrs will store). Empty is preserved as empty.
func normalizeTagLabel(label string) string {
	return strings.ToLower(strings.TrimSpace(label))
}

// validateTagLabel enforces the per-Arr rule against the already-
// normalized label. Returns "" on success, an end-user-facing
// reason string otherwise.
func validateTagLabel(label, instType string) string {
	if label == "" {
		return "tag label cannot be empty"
	}
	if instType == "radarr" && !reRadarrTagLabel.MatchString(label) {
		return "Radarr only accepts a-z, 0-9 and hyphens"
	}
	return ""
}

// handleVersion exposes container metadata the frontend needs to render
// times in the user's host context: the version string plus the
// container's timezone (TZ env var) and locale (LANG env var). The
// frontend formats every timestamp with these values so the UI matches
// the host system regardless of which browser the admin is using —
// addresses the case where a Norwegian admin viewing tagarr from an
// en-US browser would otherwise see MM/DD/YYYY + AM/PM despite the
// container running in Europe/Oslo.
//
// Defaults when env vars are missing: timezone falls back to whatever
// time.Local resolves to (UTC if no /etc/localtime); locale falls back
// to en-GB (24h DD/MM/YYYY — neutral European format).
func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]string{
		"version":  s.Version,
		"timezone": detectTimezone(),
		"locale":   detectLocale(),
	})
}

// detectTimezone returns the container's IANA timezone name (e.g.
// "Europe/Oslo"). Reads $TZ first — that's what Unraid and Docker
// templates set. Falls back to time.Local.String() which reads
// /etc/localtime; on a bare container that's typically "UTC" or
// "Local" if /etc/localtime is missing.
func detectTimezone() string {
	if tz := strings.TrimSpace(os.Getenv("TZ")); tz != "" {
		return tz
	}
	return time.Local.String()
}

// detectLocale converts a POSIX LANG value (e.g. "nb_NO.UTF-8") into
// a BCP 47 locale tag (e.g. "nb-NO") suitable for JS Intl APIs.
// When LANG is unset / "C" / "POSIX" — common on Unraid templates
// which only set TZ — falls back to localeFromTZ so a Europe/Oslo
// container yields nb-NO without requiring the user to manually add
// LANG=nb_NO.UTF-8 to their template's Extra Parameters.
//
// Locale is what drives the JS Intl format choice: nb-NO renders
// `28.04.2026, 17:30:00` (24h, dotted), en-US renders
// `4/28/2026, 5:30:00 PM` (12h, slashed). Letting locale decide
// 12h vs 24h instead of forcing one is what the user asked for —
// "vis det som er riktig basert på hvor container kjører".
func detectLocale() string {
	lang := strings.TrimSpace(os.Getenv("LANG"))
	if lang != "" && lang != "C" && lang != "POSIX" {
		if i := strings.Index(lang, "."); i >= 0 {
			lang = lang[:i]
		}
		return strings.ReplaceAll(lang, "_", "-")
	}
	return localeFromTZ(detectTimezone())
}

// localeFromTZ maps an IANA timezone to a sensible default BCP 47
// locale. Covers the major Tagarr-relevant zones (Europe + North
// America + a handful of others). The mapping is heuristic — a TZ
// represents a clock, not a language, and bilingual zones like
// Europe/Brussels (Dutch + French) pick one — but it's far better
// than defaulting everyone to en-GB regardless of where the
// container runs. Users who want a different locale than their TZ
// implies can still set LANG=xx_YY.UTF-8 in their Docker template's
// Extra Parameters; that wins over the TZ heuristic.
//
// The fallback is en-GB (24h, DD/MM/YYYY) for the world's
// 24h-format majority — closer to neutral than en-US would be.
func localeFromTZ(tz string) string {
	switch tz {
	// Nordics
	case "Europe/Oslo":
		return "nb-NO"
	case "Europe/Stockholm":
		return "sv-SE"
	case "Europe/Copenhagen":
		return "da-DK"
	case "Europe/Helsinki":
		return "fi-FI"
	case "Europe/Reykjavik":
		return "is-IS"
	// Western Europe
	case "Europe/London":
		return "en-GB"
	case "Europe/Dublin":
		return "en-IE"
	case "Europe/Berlin":
		return "de-DE"
	case "Europe/Vienna":
		return "de-AT"
	case "Europe/Zurich":
		return "de-CH"
	case "Europe/Paris":
		return "fr-FR"
	case "Europe/Brussels":
		return "nl-BE" // bilingual — Dutch slightly more common on Flemish Unraid setups
	case "Europe/Amsterdam":
		return "nl-NL"
	case "Europe/Madrid":
		return "es-ES"
	case "Europe/Rome":
		return "it-IT"
	case "Europe/Lisbon":
		return "pt-PT"
	// Central / Eastern Europe
	case "Europe/Warsaw":
		return "pl-PL"
	case "Europe/Prague":
		return "cs-CZ"
	case "Europe/Budapest":
		return "hu-HU"
	case "Europe/Athens":
		return "el-GR"
	case "Europe/Moscow":
		return "ru-RU"
	case "Europe/Istanbul":
		return "tr-TR"
	// North America (all 12h, MM/DD/YYYY)
	case "America/New_York", "America/Chicago", "America/Denver", "America/Los_Angeles",
		"America/Phoenix", "America/Anchorage", "America/Detroit", "America/Indiana/Indianapolis":
		return "en-US"
	case "America/Toronto", "America/Vancouver", "America/Edmonton",
		"America/Halifax", "America/St_Johns", "America/Winnipeg":
		return "en-CA"
	case "America/Mexico_City":
		return "es-MX"
	case "America/Sao_Paulo":
		return "pt-BR"
	case "America/Argentina/Buenos_Aires":
		return "es-AR"
	// Asia / Pacific
	case "Asia/Tokyo":
		return "ja-JP"
	case "Asia/Seoul":
		return "ko-KR"
	case "Asia/Shanghai":
		return "zh-CN"
	case "Asia/Taipei":
		return "zh-TW"
	case "Asia/Hong_Kong":
		return "zh-HK"
	case "Asia/Singapore":
		return "en-SG"
	case "Australia/Sydney", "Australia/Melbourne", "Australia/Brisbane",
		"Australia/Perth", "Australia/Adelaide", "Australia/Darwin", "Australia/Hobart":
		return "en-AU"
	case "Pacific/Auckland":
		return "en-NZ"
	}
	return "en-GB"
}

// handleHealth is the Docker healthcheck endpoint. Always public, no
// auth required — baseline §1.1 calls out that auth-gated healthcheck
// endpoints cause "unhealthy" immediately after auth lands (bit both
// Constat and Clonarr). Returns a minimal JSON body so monitors can
// tell "container responds with 200 + version" apart from "proxy
// returns 200 for everything".
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]string{"status": "ok", "version": s.Version})
}

// handleHealthDetailed returns the cached application-level health
// snapshot: per Radarr/Sonarr instance reachability + version + lag,
// Discord configuration state, and tagarr's own uptime. Reads the
// cache only — it never probes inline, so a Dashboard poll doesn't
// trigger fresh Arr hits. The poller refreshes the cache every 60s in
// a background goroutine.
//
// Auth-gated on purpose — instance names, URLs (implicit in response
// structure), and upstream versions are useful fingerprint fodder for
// an attacker.
func (s *Server) handleHealthDetailed(w http.ResponseWriter, r *http.Request) {
	if s.Health == nil {
		writeError(w, 503, "health poller not initialised")
		return
	}
	writeJSON(w, s.Health.Snapshot())
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	// ConfigStore.Get returns a deep copy already, so mutating slices here
	// can't leak back into the store. Still, mask secrets before writing
	// the response — Radarr/Sonarr API keys AND the Discord webhook URL
	// are bearer credentials that must not appear in plaintext to ANY
	// caller, session-authenticated admin or not.
	cfg := s.App.Config.Get()
	for i := range cfg.Instances {
		if cfg.Instances[i].APIKey != "" {
			cfg.Instances[i].APIKey = maskKey(cfg.Instances[i].APIKey)
		}
		// Webhook token is path-component-as-secret — anyone with the
		// URL can post Connect events. Mask it on the broad config
		// endpoint; dedicated GET /api/instances/{id}/webhook returns
		// the unmasked value for admin-side display + the configuration
		// wizard's Summary step.
		if cfg.Instances[i].Webhook.Token != "" {
			cfg.Instances[i].Webhook.Token = maskSentinel
		}
	}
	// qBit passwords masked for the same reason as Arr API keys —
	// bearer credentials must not appear in plaintext on /api/config.
	// The dedicated /api/qbit-instances surface also masks; this
	// covers any consumer still reading the broader config endpoint.
	for i := range cfg.QbitInstances {
		if cfg.QbitInstances[i].Password != "" {
			cfg.QbitInstances[i].Password = maskSentinel
		}
	}
	cfg.Discord.WebhookURL = maskSecret(cfg.Discord.WebhookURL, maskedDiscordWebhook)
	// Mask credentials inside notification agents so the legacy /api/config
	// endpoint can't leak webhook URLs/tokens. The dedicated
	// /api/notifications/agents endpoint also masks (handleListNotification-
	// Agents) but this protects readers that still consume /api/config.
	for i, a := range cfg.NotificationAgents {
		cfg.NotificationAgents[i].Config = core.MaskNotificationAgentConfig(a.Type, a.Config)
	}
	writeJSON(w, cfg)
}

// handleUpdateDisplay accepts the full DisplayConfig (uiScale +
// timeFormat) and saves. timeFormat is optional — empty string is
// equivalent to "auto" (let locale decide 12h vs 24h).
func (s *Server) handleUpdateDisplay(w http.ResponseWriter, r *http.Request) {
	var req core.DisplayConfig
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "invalid body")
		return
	}
	switch req.UIScale {
	case "1", "1.1", "1.2":
		// OK
	default:
		writeError(w, 400, "uiScale must be 1, 1.1, or 1.2")
		return
	}
	switch req.TimeFormat {
	case "", "auto", "24h", "12h":
		// OK
	default:
		writeError(w, 400, "timeFormat must be auto, 24h, or 12h")
		return
	}
	if err := s.App.Config.Update(func(c *core.Config) { c.Display = req }); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

// handleUpdateLogging accepts {debug, keepDays} and saves to Config.Logging.
// KeepDays is clamped 1–90 (default 14 when 0). Debug toggles are picked
// up by the next call without restart since the run-logger reads the
// snapshot lazily.
func (s *Server) handleUpdateLogging(w http.ResponseWriter, r *http.Request) {
	var req core.LoggingConfig
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "invalid body")
		return
	}
	if req.KeepDays == 0 {
		req.KeepDays = 14
	}
	if req.KeepDays < 1 || req.KeepDays > 90 {
		writeError(w, 400, "keepDays must be between 1 and 90")
		return
	}
	if err := s.App.Config.Update(func(c *core.Config) { c.Logging = req }); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"status": "ok", "logging": req})
}

// handleGetLogging returns the current LoggingConfig + the resolved log
// file path so the Settings UI can show the user where logs live without
// hardcoding /config/logs.
func (s *Server) handleGetLogging(w http.ResponseWriter, r *http.Request) {
	cfg := s.App.Config.Get()
	resp := map[string]any{
		"debug":    cfg.Logging.Debug,
		"keepDays": cfg.Logging.KeepDays,
	}
	if s.App.RunLog != nil {
		resp["logPath"] = s.App.RunLog.LogPath()
	}
	writeJSON(w, resp)
}

// handleUpdateAuth updates the authentication policy fields — the five
// knobs exposed in the Security panel: Authentication mode, who's
// required to log in, Trusted Networks / Trusted Proxies, and session
// TTL. Credentials (username, bcrypt hash, API key) are not touched here
// — those have their own handlers.
//
// Flipping Authentication to "none" is the ONE destructive control in
// the Radarr/Sonarr parity model (per baseline §3.4 / Clonarr v2.0.6
// handlers.go:252-256) — the request body must include the current
// admin password as confirm_password. Every other transition is safe
// to apply with just a valid session cookie. Users don't get prompted
// for a password to toggle LAN bypass, raise session TTL, or edit the
// trusted-proxy list — that would be friction without security gain.
//
// Env-locked fields (TRUSTED_NETWORKS / TRUSTED_PROXIES set at process
// start — baseline T63) are silently preserved from the auth store,
// even if the UI returned a different value. Writing to them via this
// endpoint is a no-op by design: the lock is the lock.
func (s *Server) handleUpdateAuth(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	var req struct {
		Authentication         string `json:"authentication"`
		AuthenticationRequired string `json:"authenticationRequired"`
		TrustedProxies         string `json:"trustedProxies"`
		TrustedNetworks        string `json:"trustedNetworks"`
		SessionTTLDays         int    `json:"sessionTtlDays"`
		ConfirmPassword        string `json:"confirm_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "invalid body")
		return
	}

	switch req.Authentication {
	case "forms", "basic", "none":
	default:
		writeError(w, 400, "authentication must be forms, basic, or none")
		return
	}
	switch req.AuthenticationRequired {
	case "enabled", "disabled_for_local_addresses":
	default:
		writeError(w, 400, "authenticationRequired must be enabled or disabled_for_local_addresses")
		return
	}
	if req.SessionTTLDays < 1 || req.SessionTTLDays > 365 {
		writeError(w, 400, "sessionTtlDays must be between 1 and 365")
		return
	}

	// Password-confirm gate on transition to "none". Only fires when
	// current != "none" — re-saving an already-disabled auth panel is
	// not a new transition and doesn't re-prompt.
	current := s.App.Config.Get().Authentication
	if req.Authentication == "none" && current != "none" {
		if strings.TrimSpace(req.ConfirmPassword) == "" {
			writeError(w, 400, "Disabling authentication requires your current password in confirm_password")
			return
		}
		if !s.AuthStore.VerifyPassword(s.AuthStore.Username(), req.ConfirmPassword) {
			writeError(w, 401, "current password is incorrect")
			return
		}
	}

	// Build the new auth.Config off the current one so env-locked fields
	// (AuthFilePath, SessionsFilePath, Max*, TrustedNetworksLocked, etc.)
	// survive — UpdateConfig also preserves them, but doing it here makes
	// the intent visible.
	newAuthCfg := s.AuthStore.Config()
	newAuthCfg.Mode = auth.AuthMode(req.Authentication)
	newAuthCfg.Requirement = auth.Requirement(req.AuthenticationRequired)
	newAuthCfg.SessionTTL = time.Duration(req.SessionTTLDays) * 24 * time.Hour

	// Loud-fail when the UI tries to write a locked field (baseline T63).
	// Silently preserving would leave a scripted client / stale tab
	// believing its change took effect when it didn't. 409 says "your
	// view is out of sync — refresh /api/auth/status".
	if newAuthCfg.TrustedNetworksLocked && strings.TrimSpace(req.TrustedNetworks) != strings.TrimSpace(newAuthCfg.TrustedNetworksRaw) {
		writeError(w, 409, "trustedNetworks is locked by the TRUSTED_NETWORKS env var; value unchanged")
		return
	}
	if newAuthCfg.TrustedProxiesLocked && strings.TrimSpace(req.TrustedProxies) != strings.TrimSpace(newAuthCfg.TrustedProxiesRaw) {
		writeError(w, 409, "trustedProxies is locked by the TRUSTED_PROXIES env var; value unchanged")
		return
	}

	if !newAuthCfg.TrustedNetworksLocked {
		nets, err := netsec.ParseTrustedNetworks(req.TrustedNetworks)
		if err != nil {
			writeError(w, 400, "trustedNetworks: "+err.Error())
			return
		}
		newAuthCfg.TrustedNetworks = nets
	}
	if !newAuthCfg.TrustedProxiesLocked {
		ips, err := netsec.ParseTrustedProxies(req.TrustedProxies)
		if err != nil {
			writeError(w, 400, "trustedProxies: "+err.Error())
			return
		}
		newAuthCfg.TrustedProxies = ips
	}

	// Validate before persisting. Catches bad enum combos etc. so we
	// don't partially-apply on disk only to have UpdateConfig reject.
	if err := auth.ValidateConfig(newAuthCfg); err != nil {
		writeError(w, 400, err.Error())
		return
	}

	// Persist BEFORE applying in-memory (reverses the previous order).
	// If the disk write fails, runtime stays consistent with the
	// already-persisted old config — the UI sees the error, the user
	// retries, nothing drifted. The old order could leave resolvarr.json
	// behind runtime on a disk-full / permission failure, reverting the
	// policy silently on restart.
	if err := s.App.Config.Update(func(c *core.Config) {
		c.Authentication = req.Authentication
		c.AuthenticationRequired = req.AuthenticationRequired
		c.SessionTTLDays = req.SessionTTLDays
		if !newAuthCfg.TrustedNetworksLocked {
			c.TrustedNetworks = req.TrustedNetworks
		}
		if !newAuthCfg.TrustedProxiesLocked {
			c.TrustedProxies = req.TrustedProxies
		}
	}); err != nil {
		writeError(w, 500, err.Error())
		return
	}

	// Apply in-memory. UpdateConfig re-runs ValidateConfig as defense
	// in depth — the duplicate cost is negligible (enum switch + two
	// comparisons) and it guards against a future caller that forgets
	// the pre-validation step above.
	if err := s.AuthStore.UpdateConfig(newAuthCfg); err != nil {
		// Disk is ahead of runtime now — log it for ops visibility.
		// On the next container restart, initAuth will re-apply from
		// resolvarr.json, catching up. Return 500 so the UI knows
		// something went wrong mid-transition.
		log.Printf("auth: UpdateConfig failed AFTER resolvarr.json persisted — runtime policy unchanged, restart will reconcile: %v", err)
		writeError(w, 500, "policy saved but in-memory apply failed; restart container to take effect")
		return
	}

	writeJSON(w, map[string]string{"status": "ok"})
}

// ---- Instances ----

type instanceRequest struct {
	Name         string             `json:"name"`
	Type         string             `json:"type"`
	IconVariant  string             `json:"iconVariant"`
	URL          string             `json:"url"`
	APIKey       string             `json:"apiKey"`
	PathMappings []core.PathMapping `json:"pathMappings,omitempty"`
}

func (req *instanceRequest) validate() error {
	req.Name = strings.TrimSpace(req.Name)
	req.URL = strings.TrimRight(strings.TrimSpace(req.URL), "/")
	req.APIKey = strings.TrimSpace(req.APIKey)
	if req.IconVariant == "" {
		req.IconVariant = "standard"
	}
	if req.Name == "" {
		return fmt.Errorf("name is required")
	}
	if req.Type != "radarr" && req.Type != "sonarr" {
		return fmt.Errorf("type must be radarr or sonarr")
	}
	if req.IconVariant != "standard" && req.IconVariant != "4k" {
		return fmt.Errorf("iconVariant must be standard or 4k")
	}
	if !strings.HasPrefix(req.URL, "http://") && !strings.HasPrefix(req.URL, "https://") {
		return fmt.Errorf("URL must start with http:// or https://")
	}
	if req.APIKey == "" {
		return fmt.Errorf("API key is required")
	}
	// Path-mappings — both sides must be non-empty if present. Frontend
	// already filters empty rows on save, but a hand-edited POST could
	// still smuggle a half-row through. Reject defensively.
	cleaned := req.PathMappings[:0]
	for i, m := range req.PathMappings {
		from := strings.TrimSpace(m.From)
		to := strings.TrimSpace(m.To)
		if from == "" && to == "" {
			continue // skip wholly-empty rows
		}
		if from == "" || to == "" {
			return fmt.Errorf("pathMappings[%d]: both from and to must be non-empty", i)
		}
		cleaned = append(cleaned, core.PathMapping{From: from, To: to})
	}
	req.PathMappings = cleaned
	return nil
}

func (s *Server) handleAddInstance(w http.ResponseWriter, r *http.Request) {
	var req instanceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "invalid body")
		return
	}
	if err := req.validate(); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	// Baseline T73 fail-closed: an API key that looks like our own mask
	// cannot be a real Arr key. Reject on create — no existing value to
	// preserve when building a new record, so silently accepting would
	// persist a non-functional placeholder.
	if isMasked(req.APIKey) {
		writeError(w, 400, "API key is required (the masked placeholder is not a real key)")
		return
	}
	inst := core.Instance{
		ID:           genID(),
		Name:         req.Name,
		Type:         req.Type,
		IconVariant:  req.IconVariant,
		URL:          req.URL,
		APIKey:       req.APIKey,
		PathMappings: req.PathMappings,
	}
	if err := s.App.Config.Update(func(c *core.Config) {
		c.Instances = append(c.Instances, inst)
	}); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	// Mask key in the response — UI only needs to see that the save
	// succeeded, not the real value.
	inst.APIKey = maskKey(inst.APIKey)
	writeJSON(w, inst)
}

func (s *Server) handleUpdateInstance(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req instanceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "invalid body")
		return
	}
	// Skip APIKey requirement in validate() — we handle "preserve existing
	// on masked input" ourselves below so an unchanged Save survives.
	// Run the other validators by temporarily putting a real-looking key
	// in the struct; swap back right after.
	pristineKey := req.APIKey
	if req.APIKey == "" || isMasked(req.APIKey) {
		req.APIKey = "placeholder-not-persisted"
	}
	if err := req.validate(); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	req.APIKey = pristineKey

	found := false
	err := s.App.Config.Update(func(c *core.Config) {
		for i := range c.Instances {
			if c.Instances[i].ID == id {
				c.Instances[i].Name = req.Name
				c.Instances[i].Type = req.Type
				c.Instances[i].IconVariant = req.IconVariant
				c.Instances[i].URL = req.URL
				c.Instances[i].PathMappings = req.PathMappings
				// Preserve stored API key when the UI returns the mask or an
				// empty value — the admin saved the form without editing the
				// key field, so there's no intent to change the secret.
				if req.APIKey != "" && !isMasked(req.APIKey) {
					c.Instances[i].APIKey = req.APIKey
				}
				found = true
				return
			}
		}
	})
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if !found {
		writeError(w, 404, "instance not found")
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *Server) handleDeleteInstance(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	found := false
	err := s.App.Config.Update(func(c *core.Config) {
		out := c.Instances[:0]
		for _, inst := range c.Instances {
			if inst.ID == id {
				found = true
				continue
			}
			out = append(out, inst)
		}
		c.Instances = out
	})
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if !found {
		writeError(w, 404, "instance not found")
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *Server) handleTestInstance(w http.ResponseWriter, r *http.Request) {
	// Test either an already-saved instance (by id) or an unsaved form (body).
	var reqURL, reqAPIKey string
	id := r.PathValue("id")

	if id != "" {
		found := false
		for _, inst := range s.App.Config.Get().Instances {
			if inst.ID == id {
				reqURL = inst.URL
				reqAPIKey = inst.APIKey
				found = true
				break
			}
		}
		if !found {
			writeError(w, 404, "instance not found")
			return
		}
	} else {
		var req instanceRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, 400, "invalid body")
			return
		}
		if err := req.validate(); err != nil {
			writeError(w, 400, err.Error())
			return
		}
		// Test with a masked-placeholder key would hit Arr with a bad auth
		// header and return a misleading error. Require a real key.
		if isMasked(req.APIKey) {
			writeError(w, 400, "API key is required to test — the masked placeholder is not a real key")
			return
		}
		reqURL = req.URL
		reqAPIKey = req.APIKey
	}

	client := &arr.Client{URL: reqURL, APIKey: reqAPIKey, HTTP: s.App.HTTPClient}
	version, err := client.TestConnection()
	if err != nil {
		writeError(w, 502, err.Error())
		return
	}
	writeJSON(w, map[string]string{"status": "ok", "version": version})
}

// ---- Tags ----

// instanceByID resolves the path {id} to an Instance, or writes a 404 and returns nil.
func (s *Server) instanceByID(w http.ResponseWriter, id string) *core.Instance {
	for _, inst := range s.App.Config.Get().Instances {
		if inst.ID == id {
			return &inst
		}
	}
	writeError(w, 404, "instance not found")
	return nil
}

// arrClientFor builds an arr.Client for the given instance. When the
// debug-log toggle is on, a per-request hook is attached that funnels
// each Arr API call into runs.log via App.RunLog.Debug. Hook is nil
// when debug is off so the hot path stays zero-overhead.
func (s *Server) arrClientFor(inst *core.Instance) *arr.Client {
	c := &arr.Client{URL: inst.URL, APIKey: inst.APIKey, HTTP: s.App.HTTPClient, InstanceName: inst.Name}
	if s.App != nil && s.App.RunLog != nil && s.App.RunLog.DebugEnabled() {
		instName := inst.Name
		c.DebugLog = func(method, path string, status int, latency time.Duration, summary string) {
			fields := []string{
				"instance=" + instName,
				"method=" + method,
				"path=" + path,
				"status=" + itoa(status),
				"latency_ms=" + itoa(int(latency.Milliseconds())),
			}
			if summary != "" {
				fields = append(fields, "summary="+summary)
			}
			s.App.RunLog.Debug("arr", "http", fields...)
		}
	}
	return c
}

// handleListTags returns every tag plus its usage count.
func (s *Server) handleListTags(w http.ResponseWriter, r *http.Request) {
	inst := s.instanceByID(w, r.PathValue("id"))
	if inst == nil {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	client := s.arrClientFor(inst)
	details, err := client.ListTagDetails(ctx)
	if err != nil {
		writeError(w, 502, err.Error())
		return
	}

	type tagOut struct {
		ID         int            `json:"id"`
		Label      string         `json:"label"`
		UsageCount int            `json:"usageCount"`
		// NonItemUsage maps surface-name → count for every non-movie /
		// non-series reference the tag carries (Lists, Custom Formats,
		// Notifications, etc.). Empty when the tag is only attached to
		// items and is safe to delete. Frontend uses this to warn the
		// user BEFORE they hit Delete and get a cryptic Arr API error.
		NonItemUsage map[string]int `json:"nonItemUsage,omitempty"`
	}
	out := make([]tagOut, 0, len(details))
	for _, d := range details {
		out = append(out, tagOut{
			ID: d.ID, Label: d.Label, UsageCount: d.UsageCount(),
			NonItemUsage: d.NonItemUsage(),
		})
	}
	writeJSON(w, out)
}

// handleDeleteTag removes the tag from every item that uses it, and optionally
// deletes the tag definition itself.
// Query: ?keepDefinition=true to keep the empty tag definition in Arr.
func (s *Server) handleDeleteTag(w http.ResponseWriter, r *http.Request) {
	inst := s.instanceByID(w, r.PathValue("id"))
	if inst == nil {
		return
	}
	tagID, err := strconv.Atoi(r.PathValue("tagId"))
	if err != nil {
		writeError(w, 400, "invalid tagId")
		return
	}
	keepDefinition := r.URL.Query().Get("keepDefinition") == "true"
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	client := s.arrClientFor(inst)
	details, err := client.ListTagDetails(ctx)
	if err != nil {
		writeError(w, 502, "list tags: "+err.Error())
		return
	}
	var target *arr.TagDetail
	for i := range details {
		if details[i].ID == tagID {
			target = &details[i]
			break
		}
	}
	if target == nil {
		writeError(w, 404, "tag not found on instance")
		return
	}

	removedFrom := target.UsageCount()
	if removedFrom > 0 {
		if err := client.EditorApplyTags(ctx, inst.Type, target.UsageIDs(), []int{tagID}, "remove"); err != nil {
			writeError(w, 502, "remove tag from items: "+err.Error())
			return
		}
	}
	if !keepDefinition {
		if err := client.DeleteTag(ctx, tagID); err != nil {
			writeError(w, 502, "delete tag definition: "+err.Error())
			return
		}
	}
	writeJSON(w, map[string]any{
		"status":            "ok",
		"removedFrom":       removedFrom,
		"label":             target.Label,
		"definitionDeleted": !keepDefinition,
	})
}

// handleTagItems returns the movies/series that use each of the given tags.
// Used to build the preview tables for delete and rename.
// Query: ?ids=1,2,3
func (s *Server) handleTagItems(w http.ResponseWriter, r *http.Request) {
	inst := s.instanceByID(w, r.PathValue("id"))
	if inst == nil {
		return
	}
	raw := strings.TrimSpace(r.URL.Query().Get("ids"))
	if raw == "" {
		writeError(w, 400, "ids parameter is required")
		return
	}
	wanted := make(map[int]bool)
	for _, str := range strings.Split(raw, ",") {
		id, err := strconv.Atoi(strings.TrimSpace(str))
		if err != nil {
			writeError(w, 400, "ids must be comma-separated integers")
			return
		}
		wanted[id] = true
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	client := s.arrClientFor(inst)
	tags, err := client.ListTagDetails(ctx)
	if err != nil {
		writeError(w, 502, "list tags: "+err.Error())
		return
	}
	labels := make(map[int]string, len(tags))
	for _, t := range tags {
		labels[t.ID] = t.Label
	}

	items, err := client.ListItems(ctx, inst.Type)
	if err != nil {
		writeError(w, 502, "list items: "+err.Error())
		return
	}

	type itemOut struct {
		ID           int    `json:"id"`
		Title        string `json:"title"`
		Year         int    `json:"year,omitempty"`
		TmdbID       int    `json:"tmdbId,omitempty"`
		TvdbID       int    `json:"tvdbId,omitempty"`
		ReleaseGroup string `json:"releaseGroup,omitempty"`
		SceneName    string `json:"sceneName,omitempty"`
		RelativePath string `json:"relativePath,omitempty"`
	}
	type groupOut struct {
		TagID int       `json:"tagId"`
		Label string    `json:"label"`
		Items []itemOut `json:"items"`
	}
	groups := make(map[int]*groupOut)
	for tagID := range wanted {
		groups[tagID] = &groupOut{TagID: tagID, Label: labels[tagID], Items: []itemOut{}}
	}
	for _, it := range items {
		for _, tid := range it.Tags {
			if !wanted[tid] {
				continue
			}
			out := itemOut{ID: it.ID, Title: it.Title, Year: it.Year, TmdbID: it.TmdbID, TvdbID: it.TvdbID}
			if it.MovieFile != nil {
				out.ReleaseGroup = it.MovieFile.ReleaseGroup
				out.SceneName = it.MovieFile.SceneName
				out.RelativePath = it.MovieFile.RelativePath
			}
			groups[tid].Items = append(groups[tid].Items, out)
		}
	}
	out := make([]*groupOut, 0, len(groups))
	for _, g := range groups {
		sort.Slice(g.Items, func(i, j int) bool { return g.Items[i].Title < g.Items[j].Title })
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	writeJSON(w, out)
}

// handleItemsWithTags returns the full library as a flat list of items
// with their tag IDs, plus a tag-id → label dictionary, for client-side
// query evaluation by the Tag inventory search field. One round-trip per
// "first search this session" — frontend caches and re-evaluates locally
// for subsequent queries.
func (s *Server) handleItemsWithTags(w http.ResponseWriter, r *http.Request) {
	inst := s.instanceByID(w, r.PathValue("id"))
	if inst == nil {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	client := s.arrClientFor(inst)
	tags, err := client.ListTagDetails(ctx)
	if err != nil {
		writeError(w, 502, "list tags: "+err.Error())
		return
	}
	items, err := client.ListItems(ctx, inst.Type)
	if err != nil {
		writeError(w, 502, "list items: "+err.Error())
		return
	}

	type tagOut struct {
		ID    int    `json:"id"`
		Label string `json:"label"`
	}
	type itemOut struct {
		ID           int    `json:"id"`
		Title        string `json:"title"`
		Year         int    `json:"year,omitempty"`
		TmdbID       int    `json:"tmdbId,omitempty"`
		TvdbID       int    `json:"tvdbId,omitempty"`
		Tags         []int  `json:"tags"`
		ReleaseGroup string `json:"releaseGroup,omitempty"`
		SceneName    string `json:"sceneName,omitempty"`
		RelativePath string `json:"relativePath,omitempty"`
	}
	tagsOut := make([]tagOut, 0, len(tags))
	for _, t := range tags {
		tagsOut = append(tagsOut, tagOut{ID: t.ID, Label: t.Label})
	}
	sort.Slice(tagsOut, func(i, j int) bool { return tagsOut[i].Label < tagsOut[j].Label })

	itemsOut := make([]itemOut, 0, len(items))
	for _, it := range items {
		out := itemOut{ID: it.ID, Title: it.Title, Year: it.Year, TmdbID: it.TmdbID, TvdbID: it.TvdbID, Tags: it.Tags}
		if out.Tags == nil {
			out.Tags = []int{}
		}
		if it.MovieFile != nil {
			out.ReleaseGroup = it.MovieFile.ReleaseGroup
			out.SceneName = it.MovieFile.SceneName
			out.RelativePath = it.MovieFile.RelativePath
		}
		itemsOut = append(itemsOut, out)
	}
	sort.Slice(itemsOut, func(i, j int) bool { return itemsOut[i].Title < itemsOut[j].Title })

	writeJSON(w, map[string]any{
		"tags":  tagsOut,
		"items": itemsOut,
	})
}

// handleRenameTag renames a tag. If newLabel matches an existing tag, this
// becomes a merge: items with the old tag gain the existing tag, old tag is
// removed (and optionally deleted).
// Body: {oldId, newLabel, keepOldDefinition?: bool}
func (s *Server) handleRenameTag(w http.ResponseWriter, r *http.Request) {
	inst := s.instanceByID(w, r.PathValue("id"))
	if inst == nil {
		return
	}
	var req struct {
		OldID             int    `json:"oldId"`
		NewLabel          string `json:"newLabel"`
		KeepOldDefinition bool   `json:"keepOldDefinition"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "invalid body")
		return
	}
	if req.OldID == 0 {
		writeError(w, 400, "oldId is required")
		return
	}
	// Normalize + per-app-type validate. Sends pre-lowercased to the
	// Arr so the case-sensitive FindByLabel race upstream code has
	// can't trip on "MyTag" vs DB row "mytag". Frontend sanitises
	// keystrokes, but a curl client could still post mixed case or
	// disallowed chars — defence-in-depth.
	req.NewLabel = normalizeTagLabel(req.NewLabel)
	if reason := validateTagLabel(req.NewLabel, inst.Type); reason != "" {
		writeError(w, 400, reason)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	client := s.arrClientFor(inst)
	details, err := client.ListTagDetails(ctx)
	if err != nil {
		writeError(w, 502, "list tags: "+err.Error())
		return
	}
	var old *arr.TagDetail
	var existing *arr.TagDetail // non-nil when newLabel matches another tag (merge case)
	for i := range details {
		if details[i].ID == req.OldID {
			old = &details[i]
		}
		if strings.EqualFold(details[i].Label, req.NewLabel) && details[i].ID != req.OldID {
			existing = &details[i]
		}
	}
	if old == nil {
		writeError(w, 404, "tag not found on instance")
		return
	}
	if old.Label == req.NewLabel {
		writeJSON(w, map[string]any{"status": "unchanged", "movedCount": 0})
		return
	}

	var newID int
	var merged bool
	if existing != nil {
		newID = existing.ID
		merged = true
	} else {
		newTag, err := client.CreateTag(ctx, req.NewLabel)
		if err != nil {
			writeError(w, 502, "create new tag: "+err.Error())
			return
		}
		newID = newTag.ID
	}

	moved := old.UsageCount()
	if moved > 0 {
		items := old.UsageIDs()
		if err := client.EditorApplyTags(ctx, inst.Type, items, []int{newID}, "add"); err != nil {
			writeError(w, 502, "add new tag to items: "+err.Error())
			return
		}
		if err := client.EditorApplyTags(ctx, inst.Type, items, []int{req.OldID}, "remove"); err != nil {
			writeError(w, 502, "remove old tag from items: "+err.Error())
			return
		}
	}
	if !req.KeepOldDefinition {
		if err := client.DeleteTag(ctx, req.OldID); err != nil {
			writeError(w, 502, "delete old tag: "+err.Error())
			return
		}
	}
	writeJSON(w, map[string]any{
		"status":     "ok",
		"newId":      newID,
		"movedCount": moved,
		"merged":     merged,
	})
}
