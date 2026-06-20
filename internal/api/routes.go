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
	mux.HandleFunc("GET /api/instances/{id}/quality-profiles", s.handleListQualityProfiles)
	mux.HandleFunc("GET /api/instances/{id}/qbit-categories", s.handleReconcileQbitCategories)
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

	// Missing-episodes scanner (Tag Library → Sonarr → Missing episodes).
	// Sonarr-only. Distinct from the action-dispatched /api/scan/run
	// because this surface is a Tag Library tool, not a general
	// per-instance scan — it has its own preview / search / tag triplet
	// instead of preview+apply with a tag dimension.
	mux.HandleFunc("POST /api/scan/missing-episodes/preview", s.handleMissingEpisodesPreview)
	mux.HandleFunc("POST /api/scan/missing-episodes/search", s.handleMissingEpisodesSearch)
	mux.HandleFunc("POST /api/scan/missing-episodes/tag", s.handleMissingEpisodesTag)

	// TBA refresh (Sonarr only) — find files imported as "...- TBA.mkv"
	// whose episode now has a real title, and trigger Sonarr's rename.
	// preview = detect; apply = fire RenameFiles per series.
	mux.HandleFunc("POST /api/scan/tba-refresh/preview", s.handleTbaRefreshPreview)
	mux.HandleFunc("POST /api/scan/tba-refresh/apply", s.handleTbaRefreshApply)

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

	// M-qBit-add Slice 3 — qBit's "Run external program on torrent
	// added" hook target. qBit curls this per-torrent; per-rule
	// debounce buffer aggregates burst-events. X-API-Key auth via
	// per-instance WebhookSecret.
	mux.HandleFunc("POST /api/qbit/torrent-added/{instanceId}", s.handleQbitTorrentAdded)

	// M-qBit-add Slice 4 — per-instance webhook config endpoints.
	// Session-authenticated UI helpers for showing the curl, auto-
	// configuring qBit's autorun field, rotating the secret,
	// synthetically testing the receiver, and resetting.
	mux.HandleFunc("GET /api/qbit-instances/{id}/webhook", s.handleQbitWebhookConfig)
	mux.HandleFunc("POST /api/qbit-instances/{id}/webhook/configure", s.handleQbitConfigureWebhook)
	mux.HandleFunc("POST /api/qbit-instances/{id}/webhook/rotate-secret", s.handleQbitRotateWebhookSecret)
	mux.HandleFunc("POST /api/qbit-instances/{id}/webhook/test", s.handleQbitTestWebhookEndpoint)
	mux.HandleFunc("POST /api/qbit-instances/{id}/webhook/reset", s.handleQbitResetWebhook)

	mux.HandleFunc("POST /api/webhooks/{token}", s.handleWebhookReceive)
	// M-per-rule-webhook — per-rule receive URL. Routes directly to one
	// rule (no instance-dispatcher loop) so Sonarr/Radarr can have one
	// Connect-webhook per rule with full control over which rule fires
	// from which URL.
	mux.HandleFunc("POST /api/webhooks/rule/{ruleToken}", s.handleWebhookReceivePerRule)
	// Per-rule webhook config CRUD — UI-side helpers for the rule's
	// own URL + Secret. Mirrors the instance-level webhook handlers
	// at webhooks.go:633+ but scoped to a single rule rather than
	// the instance.
	mux.HandleFunc("GET /api/webhook-rules/{id}/webhook", s.handleGetPerRuleWebhook)
	mux.HandleFunc("POST /api/webhook-rules/{id}/webhook/generate", s.handleGeneratePerRuleWebhook)
	mux.HandleFunc("POST /api/webhook-rules/{id}/webhook/rotate-secret", s.handleRotatePerRuleWebhookSecret)
	mux.HandleFunc("PUT /api/webhook-rules/{id}/webhook/require-signature", s.handleSetPerRuleWebhookRequireSignature)
	mux.HandleFunc("DELETE /api/webhook-rules/{id}/webhook", s.handleDeletePerRuleWebhook)
	mux.HandleFunc("GET /api/instances/{id}/webhook", s.handleWebhookGet)
	mux.HandleFunc("GET /api/instances/{id}/webhook/events", s.handleWebhookListEvents)
	mux.HandleFunc("GET /api/instances/{id}/webhook/events/stream", s.handleWebhookEventsStream)
	mux.HandleFunc("DELETE /api/instances/{id}/webhook/events", s.handleWebhookClearEvents)
	// Manual re-run of a logged event (failed/partial). Preview computes
	// what would fire (no execution); replay re-dispatches the saved payload.
	mux.HandleFunc("GET /api/webhooks/events/{id}/replay-preview", s.handleWebhookReplayPreview)
	mux.HandleFunc("POST /api/webhooks/events/{id}/replay", s.handleWebhookReplay)
	mux.HandleFunc("POST /api/instances/{id}/webhook/rotate", s.handleWebhookRotateToken)
	mux.HandleFunc("POST /api/instances/{id}/webhook/rotate-secret", s.handleWebhookRotateSecret)
	mux.HandleFunc("PUT /api/instances/{id}/webhook/logging", s.handleWebhookSetLogging)
	mux.HandleFunc("PUT /api/instances/{id}/webhook/require-signature", s.handleWebhookSetRequireSignature)
	mux.HandleFunc("DELETE /api/instances/{id}/webhook", s.handleWebhookDelete)

	// Arr download-client list — surfaces Sonarr/Radarr's configured
	// download clients (qBit / sabnzbd / etc.) with pre/post category
	// names already extracted. Used by the qBit Category Fix rule
	// editor so the user picks a download client and the category
	// names auto-populate from the Arr's config. ?refresh=1 invalidates
	// the 5-min cache for that instance.
	mux.HandleFunc("GET /api/instances/{id}/download-clients", s.handleListArrDownloadClients)

	// Webhook rules — saved-rule objects fired by Connect events.
	// Architectural twin of /api/schedules. CRUD only; the dispatch
	// path lives inside handleWebhookReceive.
	mux.HandleFunc("GET /api/webhook-rules", s.handleListWebhookRules)
	mux.HandleFunc("POST /api/webhook-rules", s.handleCreateWebhookRule)
	mux.HandleFunc("GET /api/webhook-rules/_meta", s.handleWebhookRulesMeta)
	mux.HandleFunc("GET /api/webhook-rules/{id}", s.handleGetWebhookRule)
	mux.HandleFunc("PUT /api/webhook-rules/{id}", s.handleUpdateWebhookRule)
	mux.HandleFunc("DELETE /api/webhook-rules/{id}", s.handleDeleteWebhookRule)

	// qBit S/E backlog-fix — wizard step 3c "Run backlog fix" button.
	// Preview returns proposed tags; apply runs the same scan + writes.
	mux.HandleFunc("POST /api/webhook-rules/qbit-se-backlog/preview", s.handleQbitSeBacklogPreview)
	mux.HandleFunc("POST /api/webhook-rules/qbit-se-backlog/apply", s.handleQbitSeBacklogApply)
	// One-off qBit S/E run (Tag Library sub-tab) — inline config, no saved rule.
	mux.HandleFunc("POST /api/qbit-se/run/preview", s.handleQbitSeRunPreview)
	mux.HandleFunc("POST /api/qbit-se/run/apply", s.handleQbitSeRunApply)

	// Plex instances + Plex label rules. Plex instances are managed
	// standalone (same shape as qBit instances) so a single Plex can
	// be referenced from multiple label rules. /test probes
	// credentials; /fetch-libraries refreshes the Libraries cache
	// from Plex's /library/sections.
	mux.HandleFunc("GET /api/plex-instances", s.handleListPlexInstances)
	mux.HandleFunc("POST /api/plex-instances", s.handleCreatePlexInstance)
	mux.HandleFunc("PUT /api/plex-instances/{id}", s.handleUpdatePlexInstance)
	mux.HandleFunc("DELETE /api/plex-instances/{id}", s.handleDeletePlexInstance)
	mux.HandleFunc("POST /api/plex-instances/{id}/test", s.handleTestPlexInstance)
	mux.HandleFunc("POST /api/plex-instances/test", s.handleTestPlexInline)
	mux.HandleFunc("POST /api/plex-instances/{id}/fetch-libraries", s.handleFetchPlexLibraries)
	// Diagnostic — fetch raw items from a Plex library with optional
	// title-substring filter. Surfaces GUIDs + Labels so users can
	// verify what Plex returns when label-sync results look off.
	mux.HandleFunc("GET /api/plex-instances/{id}/inspect", s.handleInspectPlexLibrary)

	// One-off Plex label sync from inline config — no saved rule. Body
	// {arrInstanceId, runMode, plexLabelSync:{...}}. Nothing persisted;
	// the Tag Library / Plex label sync tab's run form posts here. The
	// schedule + webhook paths reach the same engine via their own
	// inline config (no HTTP hop).
	mux.HandleFunc("POST /api/plex-sync/run", s.handleRunPlexSync)

	// Profile by tag — one-off Library scan (Radarr + Sonarr). Preview/apply
	// inline rules that move items to a quality profile based on their tags.
	mux.HandleFunc("POST /api/profile-by-tag/run", s.handleRunProfileByTag)
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
