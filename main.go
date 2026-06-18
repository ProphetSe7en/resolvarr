// Command resolvarr starts the resolvarr HTTP server. Business logic lives in
// internal/core, HTTP handlers in internal/api, external Arr clients in
// internal/arr, authentication in internal/auth. This file wires them
// together.
package main

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	htmltemplate "html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
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

	// Persistent process log — mirror everything the standard logger
	// writes to /config/logs/resolvarr.log. `docker logs` can come back
	// empty (crash-loop truncation, lost stdout, Unraid quirks), so a
	// file under the mounted /config volume is the one artefact a tester
	// can always send us to explain a failed start. Set up BEFORE config
	// load so config/auth/env failures are captured too. Best-effort: if
	// the log file can't be opened (e.g. /config not writable yet) we log
	// a warning to stderr and carry on rather than blocking boot.
	if logFile := setupProcessLog(configDir); logFile != nil {
		defer logFile.Close()
	}

	// Top-level panic capture. A panic before the HTTP server is up would
	// otherwise print only to stderr — which is exactly what goes missing
	// in a crash-loop. Recover, write the panic + full stack to the
	// persistent log, then exit non-zero so the orchestrator still sees
	// the failure. Per-goroutine panics are already caught by
	// utils.SafeGo; this covers the synchronous startup path in main.
	defer func() {
		if r := recover(); r != nil {
			log.Printf("FATAL: panic during startup: %v\n%s", r, debug.Stack())
			os.Exit(1)
		}
	}()

	log.Printf("==== Resolvarr v%s starting (pid %d) ====", Version, os.Getpid())
	log.Printf("startup: config_dir=%s port=%s", configDir, port)
	logStartupEnv()

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

	// Webhook event log (M-Webhook foundation, logging-only today).
	// Per-instance ring buffer for received Sonarr/Radarr Connect
	// events with on-disk persistence so events survive restarts.
	// Path under /config (volume-mounted on Unraid + Compose) so a
	// container rebuild keeps the log.
	server.AttachWebhookLog(filepath.Join(configDir, "webhook-events.json"))

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
	// Render index.html via html/template: `{{template "name.html" .}}` pulls in
	// partials/name.html so duplicated markup (e.g. the result panels) lives in a
	// single partials/ file. Runs once at startup; the result is cached and served
	// directly. partials/ stays under static/ so /partials/<name>.html URLs remain
	// reachable for debugging.
	indexBytes, indexErr := renderIndex(staticFS, indexData{Version: Version})
	if indexErr != nil {
		log.Fatalf("render index.html template: %v", indexErr)
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

// setupProcessLog points the standard logger at both stderr (so
// `docker logs` still works when it works) and /config/logs/resolvarr.log
// (so there's a durable artefact when it doesn't). Returns the open file
// for the caller to close on shutdown, or nil if the file couldn't be
// opened — in which case logging falls back to stderr-only and boot
// continues. To stop the file growing without bound on long-lived
// containers, it's truncated once it passes ~5 MiB; startup + the rare
// Fatalf/panic line are tiny, so the live tail always survives.
func setupProcessLog(configDir string) *os.File {
	// gosec G703 (path traversal): the path is built from configDir, the
	// operator-set CONFIG_DIR env var (default /config, a volume mount),
	// never from request/user/network input. It's the same trust level
	// as RunLogger's own /config/logs writes. Annotated known-safe.
	dir := filepath.Join(configDir, "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil { // #nosec G703 -- configDir is operator config (CONFIG_DIR env / default /config), not user input
		log.Printf("warning: could not create log dir %s (logging to stderr only): %v", dir, err)
		return nil
	}
	path := filepath.Join(dir, "resolvarr.log")
	flags := os.O_APPEND | os.O_CREATE | os.O_WRONLY
	if fi, err := os.Stat(path); err == nil && fi.Size() > 5*1024*1024 { // #nosec G703 -- configDir is operator config, not user input
		flags = os.O_TRUNC | os.O_CREATE | os.O_WRONLY
	}
	f, err := os.OpenFile(path, flags, 0o644) // #nosec G703 -- configDir is operator config (CONFIG_DIR env / default /config), not user input
	if err != nil {
		log.Printf("warning: could not open %s (logging to stderr only): %v", path, err)
		return nil
	}
	log.SetOutput(io.MultiWriter(os.Stderr, f))
	return f
}

// logStartupEnv records the environment that most often explains a
// failed or misbehaving start: the trust-boundary settings that shape
// the auth/proxy/CSRF behaviour a tester hits behind a reverse proxy,
// plus the timezone (so logged timestamps are unambiguous). Values are
// infra config, not secrets — no API keys or passwords come from the
// environment — so logging them verbatim to the operator's own /config
// volume is safe and is exactly the context we ask for when a tester
// reports "won't start" or "403".
func logStartupEnv() {
	show := func(name string) string {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			return v
		}
		return "(unset)"
	}
	log.Printf("startup: env TRUSTED_PROXIES=%s TRUSTED_NETWORKS=%s TZ=%s",
		show("TRUSTED_PROXIES"), show("TRUSTED_NETWORKS"), show("TZ"))
}

// indexData is the context passed to the page templates. The current UI fetches
// Version via the API, so these are not yet referenced in the HTML — they are
// threaded now so lifting clonarr v3's {{template}} shell partials (which take
// Version / BasePath) is a drop-in later.
type indexData struct {
	Version  string
	BasePath string
}

// renderIndex parses index.html + every partials/*.html as a Go html/template
// set and executes the index once at startup. `{{template "name.html" .}}` in
// index.html pulls in partials/name.html. This replaces the older
// <!--#include "path"--> string substitution; moving to html/template lets us
// lift clonarr v3's {{template}} shell partials directly and pass a typed
// context. The processed bytes are cached and served exactly as before.
func renderIndex(staticFS fs.FS, data indexData) ([]byte, error) {
	tmpl, err := htmltemplate.New("index.html").ParseFS(staticFS, "index.html", "partials/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse index templates: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "index.html", data); err != nil {
		return nil, fmt.Errorf("execute index template: %w", err)
	}
	return buf.Bytes(), nil
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
		// Force revalidation on the mutable text assets (JS / CSS /
		// partials). Without this the file-server sends no Cache-Control
		// and browsers heuristically cache app.js, so a new container
		// build serves fresh code from the embed.FS but the browser keeps
		// running the stale app.js until a manual hard-refresh. "no-cache"
		// = revalidate every load (304 when unchanged), so each build is
		// picked up automatically. Images/fonts/icons stay cacheable.
		switch filepath.Ext(r.URL.Path) {
		case ".js", ".css", ".html":
			w.Header().Set("Cache-Control", "no-cache")
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
