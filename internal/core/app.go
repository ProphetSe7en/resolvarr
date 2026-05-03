// Package core holds the domain model and application state for tagarr.
// It owns the App struct, Config types, and ConfigStore. HTTP handlers
// live in internal/api and consume core via a pointer to App.
package core

import (
	"fmt"
	"net/http"
	"time"

	"resolvarr/internal/netsec"
)

// App is the running tagarr application. Fields are exported so that
// internal/api handlers can reach them directly. No external package
// should construct App — use NewApp.
type App struct {
	// HTTPClient is used for LAN-destined traffic (Radarr/Sonarr on the
	// user's private network). It's a plain client — SafeHTTPClient
	// would reject RFC1918 targets unless each Arr URL was allowlisted,
	// which is more ceremony than protection for this use case. The
	// user configures each URL explicitly via the Instances panel.
	HTTPClient *http.Client

	// NotifyClient is for trusted notification endpoints — Gotify on the
	// user's LAN, internal webhooks, etc. Plain timeout-only client; no
	// SSRF blocking. Used by agents that target services the user has
	// explicitly configured to be reachable from this container.
	NotifyClient *http.Client

	// SafeClient is for user-supplied URLs that hit the public internet
	// — Discord webhooks, Pushover, NTFY. Wrapped in netsec.NewSafeHTTPClient
	// so a malicious or misconfigured webhook URL can't be coerced into
	// hitting an internal endpoint (DNS rebinding or IP literal pointing
	// at 127.0.0.1 / RFC1918).
	SafeClient *http.Client

	// Version is the build-time version string (-ldflags overrides "dev").
	// Used by notification providers to populate footer / user-agent fields
	// so users can see which tagarr version sent the message.
	Version string

	Config *ConfigStore

	// RunLog writes audit + debug lines to /config/logs/runs.log. Audit is
	// always on; Debug is gated on Config.Logging.Debug, which the logger
	// reads through cfgSnapshot every call so the Settings UI toggle takes
	// effect immediately without restart.
	RunLog *RunLogger
}

// NewApp loads (or initializes) the config at configDir and returns a
// ready-to-run App with default HTTP clients. The caller is responsible
// for wiring routes and starting the HTTP server.
//
// A non-nil App is returned even when config load fails — the caller may
// log the error and proceed, leaving the setup UI usable to repair a
// corrupted resolvarr.json. This preserves the pre-refactor behavior where
// a failed Load left the configStore zero-initialized but still valid.
func NewApp(configDir string) (*App, error) {
	cfg := NewConfigStore(configDir)
	loadErr := cfg.Load()
	app := &App{
		Config:       cfg,
		HTTPClient:   &http.Client{Timeout: 15 * time.Second},
		NotifyClient: &http.Client{Timeout: 10 * time.Second},
		SafeClient:   netsec.NewSafeHTTPClient(10*time.Second, nil),
	}
	// RunLog reads the live snapshot via Config.Get() so a Settings
	// toggle takes effect without restart. logDir is shared with the
	// scheduler at /config/logs.
	app.RunLog = NewRunLogger(configDir+"/logs", func() LoggingConfig {
		return cfg.Get().Logging
	})
	if loadErr != nil {
		return app, fmt.Errorf("load config: %w", loadErr)
	}
	return app, nil
}
