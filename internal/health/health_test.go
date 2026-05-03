package health

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"resolvarr/internal/core"
)

// fakeArrServer stands in for Radarr/Sonarr. Returns 200 with a version
// string on /api/v3/system/status when happy, 401 when unhappy. Lets
// TestPoller exercise both the OK and error branches without a real
// Arr running in the test runner.
func fakeArrServer(t *testing.T, happy bool, version string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/system/status" {
			http.NotFound(w, r)
			return
		}
		if !happy {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"version":"` + version + `","appName":"Radarr","instanceName":"Test"}`))
	}))
}

func newStoreWithInstances(t *testing.T, instances []core.Instance, discord core.DiscordConfig) *core.ConfigStore {
	t.Helper()
	dir := t.TempDir()
	s := core.NewConfigStore(dir)
	if err := s.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := s.Update(func(c *core.Config) {
		c.Instances = instances
		c.Discord = discord
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	return s
}

func TestPoller_initialSnapshotHasSelfStarting(t *testing.T) {
	// Before Start runs, Snapshot() should still return something
	// sensible. UI calls during the first second of boot will hit
	// this path.
	p := NewPoller(newStoreWithInstances(t, nil, core.DiscordConfig{}), http.DefaultClient, "v0")
	snap := p.Snapshot()
	if snap.Self.Status != StatusOK || snap.Self.Message != "starting" {
		t.Fatalf("initial self: %+v", snap.Self)
	}
	if len(snap.Dependencies) != 0 {
		t.Fatalf("initial deps: %+v (expected empty)", snap.Dependencies)
	}
}

func TestPoller_probesArrInstanceOK(t *testing.T) {
	srv := fakeArrServer(t, true, "5.6.7")
	defer srv.Close()

	store := newStoreWithInstances(t, []core.Instance{
		{ID: "1", Name: "Radarr", Type: "radarr", URL: srv.URL, APIKey: "k"},
	}, core.DiscordConfig{})
	p := NewPoller(store, &http.Client{Timeout: 5 * time.Second}, "v0")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.pollOnce(ctx)

	snap := p.Snapshot()
	if len(snap.Dependencies) != 1 {
		t.Fatalf("expected 1 dep, got %d", len(snap.Dependencies))
	}
	dep := snap.Dependencies[0]
	if dep.Status != StatusOK {
		t.Errorf("status: got %s, want ok", dep.Status)
	}
	if dep.Version != "5.6.7" {
		t.Errorf("version: got %q, want 5.6.7", dep.Version)
	}
	if dep.Name != "Radarr" || dep.Type != "radarr" {
		t.Errorf("name/type: got %q/%q", dep.Name, dep.Type)
	}
	if dep.LagMs < 0 {
		t.Errorf("lagMs: %d", dep.LagMs)
	}
}

func TestPoller_probesArrInstanceError(t *testing.T) {
	srv := fakeArrServer(t, false, "")
	defer srv.Close()

	store := newStoreWithInstances(t, []core.Instance{
		{ID: "1", Name: "BadKey", Type: "radarr", URL: srv.URL, APIKey: "wrong"},
	}, core.DiscordConfig{})
	p := NewPoller(store, &http.Client{Timeout: 5 * time.Second}, "v0")

	p.pollOnce(context.Background())
	snap := p.Snapshot()
	if len(snap.Dependencies) != 1 {
		t.Fatalf("expected 1 dep, got %d", len(snap.Dependencies))
	}
	dep := snap.Dependencies[0]
	if dep.Status != StatusError {
		t.Errorf("status: got %s, want error", dep.Status)
	}
	if dep.Message == "" {
		t.Errorf("expected error message, got empty")
	}
}

func TestPoller_discordConfiguredEnabled(t *testing.T) {
	store := newStoreWithInstances(t, nil, core.DiscordConfig{Enabled: true, WebhookURL: "https://discord.com/api/webhooks/x/y"})
	p := NewPoller(store, http.DefaultClient, "v0")
	p.pollOnce(context.Background())

	snap := p.Snapshot()
	if len(snap.Dependencies) != 1 {
		t.Fatalf("expected 1 dep, got %d", len(snap.Dependencies))
	}
	dep := snap.Dependencies[0]
	if dep.Type != "discord" || dep.Status != StatusOK {
		t.Errorf("got %+v, want type=discord status=ok", dep)
	}
}

func TestPoller_discordConfiguredDisabled(t *testing.T) {
	store := newStoreWithInstances(t, nil, core.DiscordConfig{Enabled: false, WebhookURL: "https://discord.com/api/webhooks/x/y"})
	p := NewPoller(store, http.DefaultClient, "v0")
	p.pollOnce(context.Background())

	snap := p.Snapshot()
	if len(snap.Dependencies) != 1 {
		t.Fatalf("expected 1 dep, got %d", len(snap.Dependencies))
	}
	dep := snap.Dependencies[0]
	if dep.Type != "discord" || dep.Status != StatusUnknown {
		t.Errorf("got %+v, want type=discord status=unknown", dep)
	}
}

func TestPoller_noDiscordNoEntry(t *testing.T) {
	// Empty webhook URL → no Discord row at all. UI shouldn't show a
	// Discord entry when the user hasn't configured one.
	store := newStoreWithInstances(t, nil, core.DiscordConfig{Enabled: true, WebhookURL: ""})
	p := NewPoller(store, http.DefaultClient, "v0")
	p.pollOnce(context.Background())

	snap := p.Snapshot()
	if len(snap.Dependencies) != 0 {
		t.Fatalf("expected 0 deps for unset webhook, got %+v", snap.Dependencies)
	}
}

func TestPoller_uptimeGrows(t *testing.T) {
	p := NewPoller(newStoreWithInstances(t, nil, core.DiscordConfig{}), http.DefaultClient, "v0")
	s1 := p.Snapshot()
	time.Sleep(1100 * time.Millisecond)
	s2 := p.Snapshot()
	if s2.UptimeS <= s1.UptimeS {
		t.Errorf("uptime didn't grow: %d → %d", s1.UptimeS, s2.UptimeS)
	}
}
