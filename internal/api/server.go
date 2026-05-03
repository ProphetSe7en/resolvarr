// Package api exposes resolvarr's HTTP surface. Handlers are methods on
// Server, which holds a pointer to the running core.App plus the version
// string surfaced by /api/version. Business logic lives in core; api is
// a thin adapter between HTTP requests and core state.
package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"resolvarr/internal/auth"
	"resolvarr/internal/core"
	"resolvarr/internal/core/dvdetect"
	"resolvarr/internal/health"
)

// Server holds the shared dependencies every HTTP handler needs. Routes
// are registered via RegisterRoutes.
type Server struct {
	App       *core.App
	Version   string
	AuthStore *auth.Store      // nil-tolerated for handlers that don't touch auth
	Health    *health.Poller   // nil-tolerated; /api/health/detailed returns 503 if missing
	Scheduler *core.Scheduler  // nil-tolerated; schedules CRUD/run-now return 503 if missing

	// M4b Dolby Vision detail tagging — tools (ffmpeg + dovi_tool)
	// ship baked into the image as of v0.3.5 (Dockerfile dv-tools
	// stage). Runtime install button + ENABLE_DV_TOOLS env var both
	// gone. DvTools.Status() is kept as a defensive health check
	// against $PATH (legacy /config/tools/ also checked first for
	// users with leftover bytes from the old install button).
	DvTools dvdetect.Tools  // value, not pointer — kept for legacy /config/tools/ fallback
	DvCache *dvdetect.Cache // nil signals "no on-disk memoisation; every scan does full extraction"

	// dvScanState tracks the in-flight DV-detail scan (one at a time).
	// Set when handleScanDvDetail starts, updated per-file by the loop,
	// cleared on completion. Drives the GET /api/scan/dvdetail/progress
	// poll the UI uses for the live progress bar + Cancel button.
	// Nil when no scan is running.
	DvScanState *DvScanState
	dvScanMu    sync.Mutex
}

// DvScanState is the atomic snapshot the progress endpoint returns.
// Updated by the dvdetail scan loop under dvScanMu so a poll never
// reads a partially-written value. Cancel flips the context — the
// loop checks ctx.Err() each iteration and exits cleanly with
// Status="cancelled".
type DvScanState struct {
	StartedAt    time.Time `json:"startedAt"`
	Total        int       `json:"total"`        // candidate count (after DV-type fast-path filter)
	Processed    int       `json:"processed"`    // items walked so far (cached + extracted both count)
	Extracted    int       `json:"extracted"`    // ran ffmpeg + dovi_tool successfully
	CacheHits    int       `json:"cacheHits"`
	Failed       int       `json:"failed"`       // extraction errored or file unreachable
	CurrentTitle string    `json:"currentTitle"` // movie currently being processed (best-effort)
	cancel       context.CancelFunc
}

// NewServer is a small constructor so main.go doesn't have to know the
// field names. AuthStore, Health, and Scheduler are all optional —
// passing nil is valid for tests that don't exercise those surfaces.
// Scheduler is set after server construction (it depends on Server's
// runX methods to implement core.Runner) — see main.go wiring.
func NewServer(app *core.App, version string, authStore *auth.Store, hp *health.Poller) *Server {
	return &Server{App: app, Version: version, AuthStore: authStore, Health: hp}
}

// AttachScheduler wires the scheduler in after construction. main.go
// builds the Server first, then constructs the Scheduler with a Runner
// adapter that points back at the Server (chicken-and-egg avoided by
// the post-construction injection).
func (s *Server) AttachScheduler(sch *core.Scheduler) {
	s.Scheduler = sch
}

// AttachDV wires the Dolby Vision tooling + persistent cache. main.go
// calls this once at boot. Tests that don't exercise DV-detail can
// skip this call; the run-handlers will surface a 400 instead of
// nil-derefing. Cache may be nil to disable on-disk memoisation
// (every scan does full extraction).
func (s *Server) AttachDV(tools dvdetect.Tools, cache *dvdetect.Cache) {
	s.DvTools = tools
	s.DvCache = cache
}

// NewSchedulerRunner exposes the api-side Runner adapter. main.go calls
// this to construct the Runner that the Scheduler invokes on every fire.
func (s *Server) NewSchedulerRunner() core.Runner {
	return newSchedulerRunner(s)
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	// Baseline T69: no-store on every JSON response. Even a masked
	// config blob shouldn't live on a shared proxy cache or a kiosk
	// browser after the admin logs out.
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// apiError carries an HTTP status code alongside the error message so
// handler-internal logic can return typed errors that the wrapper layer
// translates to the right writeError call. Lets us extract handler bodies
// into headless run* functions (callable from non-HTTP contexts like the
// M3d scheduler) while keeping HTTP semantics intact for the wrapper.
type apiError struct {
	Status  int
	Message string
}

func (e *apiError) Error() string { return e.Message }

// newAPIError is shorthand for `&apiError{status, msg}`.
func newAPIError(status int, msg string) *apiError {
	return &apiError{Status: status, Message: msg}
}

// writeAPIError translates an error into the right writeError call.
// *apiError values use their carried status; anything else is 500.
func writeAPIError(w http.ResponseWriter, err error) {
	if err == nil {
		return
	}
	if ae, ok := err.(*apiError); ok {
		writeError(w, ae.Status, ae.Message)
		return
	}
	writeError(w, 500, err.Error())
}

func genID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// errText is a tiny wrapper so validate() reads cleanly inside handlers.
type errText string

func (e errText) Error() string { return string(e) }

// ==== Credential masking =====================================================
// Radarr/Sonarr API keys and the Discord webhook URL are bearer credentials —
// anyone who sees them can post messages or control the linked service. They
// must not appear in plaintext in any GET response (session-authenticated
// admin or not). Pattern:
//   - GET handlers mask secrets before writing the response
//   - PUT handlers detect the mask on input and preserve the stored value so
//     the UI's "Save" without an edit is a no-op for secrets
//   - POST (create) handlers REJECT the mask on input (T73 fail-closed) —
//     there's no existing value to preserve when creating a new record

const (
	maskSentinel         = "********"
	maskedDiscordWebhook = "https://discord.com/api/webhooks/[MASKED]/[MASKED]"
)

// maskKey returns a partially-revealed form of an API key: first 4 chars,
// asterisks for the middle, last 4 chars. Keys short enough that this would
// leak structure collapse to the sentinel.
func maskKey(key string) string {
	if len(key) <= 8 {
		return maskSentinel
	}
	return key[:4] + strings.Repeat("*", len(key)-8) + key[len(key)-4:]
}

// isMasked detects whether a string was produced by maskKey — used to spot
// when the UI has returned a masked value the server sent earlier, so we
// know not to overwrite the real key with its own mask.
func isMasked(key string) bool {
	if key == "" || key == maskSentinel {
		return true
	}
	// maskKey output: 4 chars + N asterisks + 4 chars, total length >= 9.
	if len(key) < 9 {
		return false
	}
	mid := key[4 : len(key)-4]
	for _, c := range mid {
		if c != '*' {
			return false
		}
	}
	return len(mid) > 0
}

// maskSecret returns the placeholder when s is non-empty, otherwise empty.
// Empty-stays-empty so the UI can distinguish "not set" from "set but hidden".
func maskSecret(s, placeholder string) string {
	if s == "" {
		return ""
	}
	return placeholder
}

// preserveIfMasked returns existing when incoming equals the placeholder —
// the UI round-tripped the mask unchanged, so keep the real value. Otherwise
// returns incoming (including empty string, which is "delete this secret").
func preserveIfMasked(incoming, existing, placeholder string) string {
	if incoming == placeholder {
		return existing
	}
	return incoming
}
