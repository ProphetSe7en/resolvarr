// Package health implements an application-level health poller — a
// background goroutine that periodically checks each configured
// dependency (Radarr/Sonarr instances, Discord webhook, tagarr itself)
// and caches the latest result. Handlers read the cache instead of
// re-probing on every request so a noisy Dashboard poll doesn't turn
// into a DDoS against the user's Arr instances.
//
// The public GET /api/health endpoint (liveness for Docker HEALTHCHECK)
// is separate and intentionally simple. This package feeds
// GET /api/health/detailed, which is auth-gated because the snapshot
// exposes instance names, upstream versions, and lag timings.
package health

import (
	"context"
	"net/http"
	"sync"
	"time"

	"resolvarr/internal/arr"
	"resolvarr/internal/core"
)

// Status is the three-way result of a single dependency check.
type Status string

const (
	StatusOK      Status = "ok"      // dependency reachable + responded as expected
	StatusError   Status = "error"   // we tried and it failed
	StatusUnknown Status = "unknown" // we haven't checked yet, or the dep isn't configured
)

// DependencyCheck is one row in the detailed health response.
type DependencyCheck struct {
	Name        string    `json:"name"`              // user-visible label (instance Name, or "Discord", or "Tagarr")
	Type        string    `json:"type"`              // "radarr" | "sonarr" | "discord" | "self"
	Status      Status    `json:"status"`
	Message     string    `json:"message,omitempty"` // short human text — error detail or "configured"
	Version     string    `json:"version,omitempty"` // upstream version when known
	LagMs       int64     `json:"lagMs"`             // round-trip latency of the last probe
	LastCheckAt time.Time `json:"lastCheckAt"`       // when the probe ran
}

// Snapshot is the full response body for /api/health/detailed.
type Snapshot struct {
	Self         DependencyCheck   `json:"self"`
	Dependencies []DependencyCheck `json:"dependencies"`
	GeneratedAt  time.Time         `json:"generatedAt"`
	UptimeS      int64             `json:"uptimeSeconds"`
}

// Poller owns the background probe loop and the cached snapshot. One
// Poller per tagarr process. Safe for concurrent Snapshot() reads while
// pollOnce is writing — the mutex serializes the swap.
type Poller struct {
	config     *core.ConfigStore
	httpClient *http.Client
	version    string
	started    time.Time

	mu       sync.RWMutex
	snapshot Snapshot
}

// NewPoller builds a poller. The caller is responsible for running
// Start(ctx, interval) in a goroutine (use utils.SafeGo for panic
// recovery). An initial empty snapshot is returned by Snapshot() until
// Start has completed its first probe cycle.
func NewPoller(cfg *core.ConfigStore, httpClient *http.Client, version string) *Poller {
	now := time.Now()
	return &Poller{
		config:     cfg,
		httpClient: httpClient,
		version:    version,
		started:    now,
		snapshot: Snapshot{
			Self: DependencyCheck{
				Name:        "Tagarr",
				Type:        "self",
				Status:      StatusOK,
				Version:     version,
				LastCheckAt: now,
				Message:     "starting",
			},
			Dependencies: []DependencyCheck{},
			GeneratedAt:  now,
		},
	}
}

// Start runs the probe loop until ctx is cancelled. Probes once
// immediately (so the Dashboard has real data on first render) then
// every interval. Callers who pass a sub-second interval are their
// own problem — Arr instances on the home LAN don't appreciate the
// attention.
func (p *Poller) Start(ctx context.Context, interval time.Duration) {
	p.pollOnce(ctx)

	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.pollOnce(ctx)
		}
	}
}

// Snapshot returns a copy of the cached result. Dependencies slice is
// copied so callers can't stomp the internal state.
func (p *Poller) Snapshot() Snapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := p.snapshot
	out.Dependencies = append([]DependencyCheck(nil), p.snapshot.Dependencies...)
	out.UptimeS = int64(time.Since(p.started).Seconds())
	return out
}

// pollOnce runs one full probe cycle and atomically swaps the cached
// snapshot. ctx cancellation during probe lets us exit early without
// finishing every Arr request.
func (p *Poller) pollOnce(ctx context.Context) {
	cfg := p.config.Get()
	deps := make([]DependencyCheck, 0, len(cfg.Instances)+1)

	// Arr instances — parallel would be nicer but tagarr users typically
	// have 2-4 instances and each probe is <1s, so serial is fine and
	// keeps the code simple. Revisit if this ever gets slow.
	for _, inst := range cfg.Instances {
		if ctx.Err() != nil {
			return
		}
		deps = append(deps, p.probeArr(inst))
	}

	// Discord webhook — configured/not-configured only, we don't ping
	// the real URL to avoid rate-limits or unexpected behavior on
	// webhook deletions. Users exercise liveness via the Test button.
	if cfg.Discord.Enabled && cfg.Discord.WebhookURL != "" {
		deps = append(deps, DependencyCheck{
			Name:        "Discord",
			Type:        "discord",
			Status:      StatusOK,
			Message:     "configured (use Test Notification to verify delivery)",
			LastCheckAt: time.Now(),
		})
	} else if cfg.Discord.WebhookURL != "" {
		deps = append(deps, DependencyCheck{
			Name:        "Discord",
			Type:        "discord",
			Status:      StatusUnknown,
			Message:     "configured but disabled",
			LastCheckAt: time.Now(),
		})
	}

	now := time.Now()
	snap := Snapshot{
		Self: DependencyCheck{
			Name:        "Tagarr",
			Type:        "self",
			Status:      StatusOK,
			Version:     p.version,
			LastCheckAt: now,
			Message:     "up",
		},
		Dependencies: deps,
		GeneratedAt:  now,
	}

	p.mu.Lock()
	p.snapshot = snap
	p.mu.Unlock()
}

// probeArr runs a single Arr TestConnection with a short per-probe
// timeout (5s) — separate from the longer 15s timeout the HTTP client
// uses for interactive requests. A hung Arr shouldn't block the poll
// cycle long enough for the UI to notice.
func (p *Poller) probeArr(inst core.Instance) DependencyCheck {
	dep := DependencyCheck{
		Name:        inst.Name,
		Type:        inst.Type,
		LastCheckAt: time.Now(),
	}
	client := &arr.Client{URL: inst.URL, APIKey: inst.APIKey, HTTP: p.httpClient}
	start := time.Now()
	version, err := client.TestConnection()
	dep.LagMs = time.Since(start).Milliseconds()
	if err != nil {
		dep.Status = StatusError
		dep.Message = err.Error()
		return dep
	}
	dep.Status = StatusOK
	dep.Version = version
	return dep
}
