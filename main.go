// Command resolvarr starts the resolvarr HTTP server. Business logic lives in
// internal/core, HTTP handlers in internal/api, external Arr clients in
// internal/arr, authentication in internal/auth. This file wires them
// together.
package main

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"resolvarr/internal/api"
	"resolvarr/internal/auth"
	"resolvarr/internal/core"
	"resolvarr/internal/core/dvdetect"
	"resolvarr/internal/health"
	"resolvarr/internal/netsec"
	"resolvarr/internal/utils"
)

// Version is overridden at build time via -ldflags.
var Version = "dev"

//go:embed ui/static
var staticFiles embed.FS

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	port := os.Getenv("PORT")
	if port == "" {
		port = "6075"
	}

	configDir := os.Getenv("CONFIG_DIR")
	if configDir == "" {
		configDir = "/config"
	}

	app, err := core.NewApp(configDir)
	if err != nil {
		log.Printf("warning: could not load config: %v", err)
		// app is still non-nil per core.NewApp contract — the setup UI
		// stays reachable so a user can recover from a corrupted config.
	}
	app.Version = Version

	authStore := initAuth(ctx, configDir, app.Config)

	// Background health poller — fills /api/health/detailed cache every
	// 60s. Panics are caught by utils.SafeGo so one bad Arr response
	// can't kill the container.
	healthPoller := health.NewPoller(app.Config, app.HTTPClient, Version)
	utils.SafeGo("health-poller", func() {
		healthPoller.Start(ctx, 60*time.Second)
	})

	server := api.NewServer(app, Version, authStore, healthPoller)

	// M4b Dolby Vision detail tagging — opt-in tools install + scan
	// pipeline. AttachDV plugs in the Tools value (resolves
	// /config/tools/{ffmpeg,dovi_tool}) + the persistent extraction
	// cache (/config/dv-cache.json). Cache load is best-effort: a
	// corrupted file logs and starts fresh rather than blocking
	// boot — the extraction loop tolerates a nil cache and the next
	// successful Save overwrites the broken file.
	dvTools := dvdetect.DefaultTools(configDir)
	dvCache, dvCacheErr := dvdetect.LoadCache(configDir)
	if dvCacheErr != nil {
		log.Printf("dv-cache load failed (starting fresh): %v", dvCacheErr)
		dvCache = nil
	}
	server.AttachDV(dvTools, dvCache)

	// Scheduler (M3d) — wires cron-driven schedule firing through the
	// api-side Runner adapter. Constructed AFTER the server because the
	// adapter holds a reference back to server.runX methods. SafeGo'd
	// like the other background goroutines so a panic mid-fire doesn't
	// kill the container; ctx-driven shutdown drains in-flight runs up
	// to the cron's own grace period.
	scheduler := core.NewScheduler(app, server.NewSchedulerRunner(), filepath.Join(configDir, "logs"))
	server.AttachScheduler(scheduler)
	utils.SafeGo("scheduler", func() {
		scheduler.Start(ctx)
	})

	mux := http.NewServeMux()
	server.RegisterRoutes(mux)
	server.RegisterAuthRoutes(mux)

	// Static UI assets — embedded at build time from ui/static.
	staticFS, err := fs.Sub(staticFiles, "ui/static")
	if err != nil {
		log.Fatalf("static embed: %v", err)
	}
	// Process <!--#include "path"--> markers in index.html so duplicated
	// markup blocks (e.g. the Recover result panel that's rendered on
	// the Run mode + Release Groups sub-tabs) live in a single
	// partials/ file. Substitution runs once at startup; the result is
	// cached and served directly. partials/ stays under static/ so the
	// /partials/<name>.html URLs are still reachable for debugging.
	indexBytes, indexErr := readProcessedIndex(staticFS)
	if indexErr != nil {
		log.Fatalf("process index.html includes: %v", indexErr)
	}
	mux.Handle("GET /", indexHandler(indexBytes, http.FileServer(http.FS(staticFS))))

	// Middleware chain, outermost first:
	//   SecurityHeaders → CSRF → Auth → mux
	// SecurityHeaders runs first so every response — including auth
	// challenges and static files — gets the shared security headers.
	// CSRF runs before Auth because the CSRF cookie is maintained for
	// unauthenticated callers too (setup/login forms need it).
	var handler http.Handler = authStore.Middleware(mux)
	handler = authStore.CSRFMiddleware(handler)
	handler = auth.SecurityHeadersMiddleware(handler)

	// WriteTimeout intentionally disabled (0 = no timeout). DV detail
	// scans on libraries with bypass-cache or freshly-cleared cache can
	// take 30+ seconds (one ffmpeg+dovi_tool extraction per file × N
	// files). A 30-second server WriteTimeout would close the
	// connection mid-scan; the handler keeps running server-side
	// (writes the dump + audit) but the browser sees the response
	// fetch reject mid-flight, which triggers a second POST/api/scan/run
	// from one of: chain-runner error-recovery, browser-level
	// retry-on-network-error, or Alpine's reactive cascade catching
	// the rejected promise. Per-handler context timeouts already cap
	// runtime safely (scanTimeout=60s for tag/discover/audio/video,
	// DvDetailScanTimeout=30min for DV). IdleTimeout still trims
	// stale-keep-alive connections so this isn't a connection-leak
	// vector. ReadTimeout caps how long we'll wait for the request
	// body (request bodies are small JSON, 15s is plenty).
	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 0,
		IdleTimeout:  60 * time.Second,
	}

	log.Printf("Resolvarr v%s starting on port %s", Version, port)
	fmt.Printf("[%s] Web UI available at http://localhost:%s\n", time.Now().Format("2006-01-02 15:04:05"), port)

	utils.SafeGo("http-server", func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server: %v", err)
		}
	})

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	log.Println("Shutting down…")
	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = srv.Shutdown(shutdownCtx)
	if app != nil && app.RunLog != nil {
		app.RunLog.Close()
	}
}

// includeRE matches `<!--#include "path"-->` markers in index.html.
// Path is relative to the static FS root (i.e. "partials/foo.html"
// resolves to "ui/static/partials/foo.html" before fs.Sub stripped the
// prefix). Whitespace inside the marker is tolerated so the source HTML
// stays readable.
var includeRE = regexp.MustCompile(`(?s)<!--\s*#include\s+"([^"]+)"\s*-->`)

// readProcessedIndex reads ui/static/index.html, recursively substitutes
// every <!--#include "path"--> marker with the contents of that path
// from the same FS, and returns the processed bytes. Recursion is
// shallow (one round-trip) — partials don't include other partials.
// A missing partial logs and leaves the marker in place so the rest of
// the page still renders.
func readProcessedIndex(staticFS fs.FS) ([]byte, error) {
	raw, err := fs.ReadFile(staticFS, "index.html")
	if err != nil {
		return nil, err
	}
	processed := includeRE.ReplaceAllFunc(raw, func(match []byte) []byte {
		m := includeRE.FindSubmatch(match)
		if m == nil {
			return match
		}
		path := string(m[1])
		partial, perr := fs.ReadFile(staticFS, path)
		if perr != nil {
			log.Printf("warning: include not found: %s (%v) — leaving marker in place", path, perr)
			return match
		}
		return partial
	})
	return processed, nil
}

// indexHandler serves the processed index.html for "/" and "/index.html"
// requests, and falls through to the file-server for everything else
// (JS, CSS, images, partials/*).
func indexHandler(indexBytes []byte, fileServer http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache")
			_, _ = w.Write(indexBytes)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

// initAuth loads auth policy from the resolvarr.json config store, applies
// env-var overrides for trust-boundary fields (baseline T63), validates,
// loads existing credentials from /config/auth.json, and returns a
// ready-to-use Store. Refuses to start on unknown enum values or a
// malformed auth.json — a container that cannot enforce its own auth
// policy must not pretend to be up.
func initAuth(ctx context.Context, configDir string, configStore *core.ConfigStore) *auth.Store {
	cfg := auth.DefaultConfig()
	cfg.AuthFilePath = filepath.Join(configDir, "auth.json")
	cfg.SessionsFilePath = filepath.Join(configDir, "sessions.json")

	appCfg := configStore.Get()
	if appCfg.Authentication != "" {
		cfg.Mode = auth.AuthMode(appCfg.Authentication)
	}
	if appCfg.AuthenticationRequired != "" {
		cfg.Requirement = auth.Requirement(appCfg.AuthenticationRequired)
	}
	if appCfg.SessionTTLDays > 0 {
		cfg.SessionTTL = time.Duration(appCfg.SessionTTLDays) * 24 * time.Hour
	}

	// Env-var override for the trust-boundary config (baseline T63). If
	// the env var is set at process start, that value wins over the
	// config-file value AND the UI cannot change it. Use this in Unraid
	// templates / docker-compose to lock the trust boundary against
	// UI-takeover attacks (session hijack, a local-bypass peer adding
	// itself to the trust list).
	if envNets := strings.TrimSpace(os.Getenv("TRUSTED_NETWORKS")); envNets != "" {
		nets, err := netsec.ParseTrustedNetworks(envNets)
		if err != nil {
			log.Fatalf("auth: invalid TRUSTED_NETWORKS env var: %v", err)
		}
		cfg.TrustedNetworks = nets
		cfg.TrustedNetworksLocked = true
		cfg.TrustedNetworksRaw = envNets
		log.Printf("auth: trusted_networks locked by TRUSTED_NETWORKS env var (%d entries)", len(nets))
	} else if appCfg.TrustedNetworks != "" {
		nets, err := netsec.ParseTrustedNetworks(appCfg.TrustedNetworks)
		if err != nil {
			log.Fatalf("auth: invalid trustedNetworks config: %v", err)
		}
		cfg.TrustedNetworks = nets
	}

	if envProxies := strings.TrimSpace(os.Getenv("TRUSTED_PROXIES")); envProxies != "" {
		ips, err := netsec.ParseTrustedProxies(envProxies)
		if err != nil {
			log.Fatalf("auth: invalid TRUSTED_PROXIES env var: %v", err)
		}
		cfg.TrustedProxies = ips
		cfg.TrustedProxiesLocked = true
		cfg.TrustedProxiesRaw = envProxies
		log.Printf("auth: trusted_proxies locked by TRUSTED_PROXIES env var (%d entries)", len(ips))
	} else if appCfg.TrustedProxies != "" {
		ips, err := netsec.ParseTrustedProxies(appCfg.TrustedProxies)
		if err != nil {
			log.Fatalf("auth: invalid trustedProxies config: %v", err)
		}
		cfg.TrustedProxies = ips
	}

	if err := auth.ValidateConfig(cfg); err != nil {
		log.Fatalf("auth config refuses to start: %v", err)
	}

	store := auth.NewStore(cfg)
	if _, err := store.Load(); err != nil {
		log.Fatalf("auth: load credentials: %v", err)
	}

	if store.IsConfigured() {
		log.Printf("auth: mode=%s required=%s user=%s", cfg.Mode, cfg.Requirement, store.Username())
	} else {
		log.Printf("auth: no credentials yet — first run, /setup wizard will prompt for admin user")
	}
	if cfg.Mode == auth.ModeNone {
		log.Printf("auth: WARNING — authentication is DISABLED via authentication=none. Do not expose this container to untrusted networks.")
	}

	// Reap expired sessions every 5 minutes. utils.SafeGo recovers
	// panics so a bad session entry can't kill the container.
	utils.SafeGo("session-cleanup", func() {
		t := time.NewTicker(5 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				store.CleanupExpiredSessions()
			}
		}
	})

	// Periodic loud warning while mode=none. Handles live-reload
	// transitions both ways — a user disabling auth via the UI still
	// sees the reminder on every tick.
	utils.SafeGo("auth-none-warning", func() {
		t := time.NewTicker(60 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if store.Config().Mode == auth.ModeNone {
					log.Printf("auth: WARNING — authentication is still DISABLED. Every request is admin. Re-enable auth or restrict to 127.0.0.1.")
				}
			}
		}
	})

	return store
}
