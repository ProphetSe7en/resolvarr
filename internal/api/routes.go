package api

import (
	"net/http"

	"resolvarr/internal/auth"
)

// RegisterRoutes wires every resolvarr API endpoint onto mux. Static asset
// serving is the caller's responsibility so the embed.FS can live in
// package main where the //go:embed directive is resolved.
func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	// Meta
	mux.HandleFunc("GET /api/version", s.handleVersion)
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/health/detailed", s.handleHealthDetailed)

	// Config
	mux.HandleFunc("GET /api/config", s.handleGetConfig)

	// Display
	mux.HandleFunc("PUT /api/config/display", s.handleUpdateDisplay)
	mux.HandleFunc("GET /api/config/logging", s.handleGetLogging)
	mux.HandleFunc("PUT /api/config/logging", s.handleUpdateLogging)

	// Discord

	// Authentication policy. Rate-limited because the transition-to-none
	// path requires a current-password confirm; an attacker who got past
	// session-auth (e.g. via an unrelated bug) shouldn't be able to
	// brute-force that confirm to disable auth entirely.
	mux.Handle("PUT /api/config/auth", auth.AuthRateLimitMiddleware(s.AuthStore)(http.HandlerFunc(s.handleUpdateAuth)))

	// Instances
	mux.HandleFunc("POST /api/instances", s.handleAddInstance)
	mux.HandleFunc("PUT /api/instances/{id}", s.handleUpdateInstance)
	mux.HandleFunc("DELETE /api/instances/{id}", s.handleDeleteInstance)
	mux.HandleFunc("POST /api/instances/test", s.handleTestInstance)      // unsaved form
	mux.HandleFunc("POST /api/instances/{id}/test", s.handleTestInstance) // saved instance

	// Tags
	mux.HandleFunc("GET /api/instances/{id}/tags", s.handleListTags)
	mux.HandleFunc("GET /api/instances/{id}/tag-items", s.handleTagItems)
	mux.HandleFunc("GET /api/instances/{id}/items-with-tags", s.handleItemsWithTags)
	mux.HandleFunc("DELETE /api/instances/{id}/tags/{tagId}", s.handleDeleteTag)
	mux.HandleFunc("POST /api/instances/{id}/tags/rename", s.handleRenameTag)

	// Release Groups
	mux.HandleFunc("GET /api/groups", s.handleListGroups)
	mux.HandleFunc("POST /api/groups", s.handleAddGroup)
	mux.HandleFunc("PUT /api/groups/{id}", s.handleUpdateGroup)
	mux.HandleFunc("DELETE /api/groups/{id}", s.handleDeleteGroup)

	// Filters
	mux.HandleFunc("GET /api/filters", s.handleGetFilters)
	mux.HandleFunc("PUT /api/filters", s.handleUpdateFilters)

	// Auto tags (M4) — split by stream type. Audio = audio bucket
	// only; Video = resolution + codec + HDR. Each section has its
	// own RemoveOrphanedTags toggle and own scan handler.
	mux.HandleFunc("GET /api/audio-tags", s.handleGetAudioTags)
	mux.HandleFunc("PUT /api/audio-tags", s.handleUpdateAudioTags)
	mux.HandleFunc("GET /api/video-tags", s.handleGetVideoTags)
	mux.HandleFunc("PUT /api/video-tags", s.handleUpdateVideoTags)

	// DV detail (M4b) — opt-in Dolby Vision profile/CM tagging.
	// Config CRUD + tools installer endpoints. Install/uninstall
	// are gated by dvToolsMu (TryLock → 429 on collision) so two
	// click events can't race writes to the same on-disk binaries.
	mux.HandleFunc("GET /api/dv-detail", s.handleGetDvDetail)
	mux.HandleFunc("PUT /api/dv-detail", s.handleUpdateDvDetail)
	// Status endpoint is the only DV-tools surface left — install + uninstall
	// dropped after the move to bake-in via the Dockerfile dv-tools stage.
	// Status reads `which ffmpeg` / `which dovi_tool` for a defensive
	// "Tools unreachable" indicator if the image build is broken.
	mux.HandleFunc("GET /api/tools/dv/status", s.handleDvToolsStatus)
	// DV detail cache management. Stats powers the cache panel on the
	// DV detail tab; Clear is the user-fired "wipe + force re-extract"
	// action.
	mux.HandleFunc("GET /api/dv-cache/stats", s.handleDvCacheStats)
	mux.HandleFunc("DELETE /api/dv-cache", s.handleDvCacheClear)
	// DV-detail scan progress + cancel — single in-flight scan per
	// container; UI polls progress every ~1s while running and POSTs
	// cancel to flip the context.
	mux.HandleFunc("GET /api/scan/dvdetail/progress", s.handleDvScanProgress)
	mux.HandleFunc("POST /api/scan/dvdetail/cancel", s.handleDvScanCancel)
	// Adhoc-scan history — lists/serves the JSON dumps every scan
	// handler writes to /config/logs/scan-{action}-*.json. Powers
	// the History viewer under Activity.
	mux.HandleFunc("GET /api/scan/history", s.handleScanHistory)
	mux.HandleFunc("GET /api/scan/history/{file}", s.handleScanHistoryFile)

	// Scan (M3 — tag / discover / cleanup / recover, dispatched via action field)
	mux.HandleFunc("POST /api/scan/run", s.handleScanRun)

	// Recover exclusions — per-instance "skip these in next scan" lists.
	// User flags faulty / unfixable items; Recover scan filters them out
	// before the per-item history walk. Restored via the "Show excluded"
	// panel + per-row Include-again button.
	mux.HandleFunc("GET /api/recover/exclusions/{instanceId}", s.handleListRecoverExclusions)
	mux.HandleFunc("POST /api/recover/exclusions/{instanceId}", s.handleAddRecoverExclusions)
	mux.HandleFunc("DELETE /api/recover/exclusions/{instanceId}", s.handleRemoveRecoverExclusions)

	// Schedules (M3d — saved workflows fired by cron)
	mux.HandleFunc("GET /api/schedules", s.handleListSchedules)
	mux.HandleFunc("POST /api/schedules", s.handleCreateSchedule)
	mux.HandleFunc("GET /api/schedules/{id}", s.handleGetSchedule)
	mux.HandleFunc("PUT /api/schedules/{id}", s.handleUpdateSchedule)
	mux.HandleFunc("DELETE /api/schedules/{id}", s.handleDeleteSchedule)
	mux.HandleFunc("POST /api/schedules/{id}/run", s.handleRunSchedule)
	mux.HandleFunc("GET /api/schedules/{id}/runs/{startedAt}/result", s.handleGetScheduleRunResult)

	// Notification agents (multi-provider) — see internal/core/agents/
	// and docs/notification-agents-pattern.md.
	mux.HandleFunc("GET /api/notifications/agents", s.handleListNotificationAgents)
	mux.HandleFunc("POST /api/notifications/agents", s.handleCreateNotificationAgent)
	mux.HandleFunc("PUT /api/notifications/agents/{id}", s.handleUpdateNotificationAgent)
	mux.HandleFunc("DELETE /api/notifications/agents/{id}", s.handleDeleteNotificationAgent)
	mux.HandleFunc("POST /api/notifications/agents/test", s.handleTestNotificationAgentInline)
	mux.HandleFunc("POST /api/notifications/agents/{id}/test", s.handleTestNotificationAgent)

	// Webhooks (M-Webhook foundation — logging-only today). The receive
	// path lives under /api/webhooks/{token} so the whole URL pasted
	// into Sonarr/Radarr Connect carries the auth bit. The other
	// endpoints are admin-side and live under the standard per-
	// instance namespace so they share the auth middleware.
	// qBittorrent instances — user-managed list, paired with Arr
	// webhook configs via WebhookConfig.QbitInstanceID when functions
	// land. Standalone CRUD + Test Connection. Backlog scan (Service B)
	// targets one of these directly without going through any Arr.
	mux.HandleFunc("GET /api/qbit-instances", s.handleListQbitInstances)
	mux.HandleFunc("POST /api/qbit-instances", s.handleCreateQbitInstance)
	mux.HandleFunc("PUT /api/qbit-instances/{id}", s.handleUpdateQbitInstance)
	mux.HandleFunc("DELETE /api/qbit-instances/{id}", s.handleDeleteQbitInstance)
	mux.HandleFunc("POST /api/qbit-instances/{id}/test", s.handleTestQbitInstance)
	mux.HandleFunc("POST /api/qbit-instances/test", s.handleTestQbitInline)

	mux.HandleFunc("POST /api/webhooks/{token}", s.handleWebhookReceive)
	mux.HandleFunc("GET /api/instances/{id}/webhook", s.handleWebhookGet)
	mux.HandleFunc("GET /api/instances/{id}/webhook/events", s.handleWebhookListEvents)
	mux.HandleFunc("GET /api/instances/{id}/webhook/events/stream", s.handleWebhookEventsStream)
	mux.HandleFunc("DELETE /api/instances/{id}/webhook/events", s.handleWebhookClearEvents)
	mux.HandleFunc("POST /api/instances/{id}/webhook/rotate", s.handleWebhookRotateToken)
	mux.HandleFunc("PUT /api/instances/{id}/webhook/logging", s.handleWebhookSetLogging)
	mux.HandleFunc("DELETE /api/instances/{id}/webhook", s.handleWebhookDelete)
}

// RegisterAuthRoutes wires the setup wizard, login/logout, and
// auth-API endpoints onto mux. Kept as a separate method so callers
// that choose to disable auth entirely (tests, future CLI variants)
// can skip this without touching the main route set.
//
// The four credential-acceptance endpoints (POST /setup, POST /login,
// POST /api/auth/change-password, PUT /api/config/auth) are wrapped
// in AuthRateLimitMiddleware: 5 attempts per IP per minute, 429 with
// Retry-After on overflow. Stops brute-force on bcrypt-hot endpoints
// without an external rate-limiter dependency.
func (s *Server) RegisterAuthRoutes(mux *http.ServeMux) {
	rateLimit := auth.AuthRateLimitMiddleware(s.AuthStore)

	mux.HandleFunc("GET /setup", s.handleSetupPage)
	mux.Handle("POST /setup", rateLimit(http.HandlerFunc(s.handleSetupSubmit)))
	mux.HandleFunc("GET /login", s.handleLoginPage)
	mux.Handle("POST /login", rateLimit(http.HandlerFunc(s.handleLoginSubmit)))
	mux.HandleFunc("POST /logout", s.handleLogout)

	mux.HandleFunc("GET /api/auth/status", s.handleAuthStatus)
	mux.HandleFunc("GET /api/auth/api-key", s.handleGetAPIKey)
	mux.HandleFunc("POST /api/auth/regenerate-api-key", s.handleRegenAPIKey)
	mux.Handle("POST /api/auth/change-password", rateLimit(http.HandlerFunc(s.handleChangePassword)))
}
