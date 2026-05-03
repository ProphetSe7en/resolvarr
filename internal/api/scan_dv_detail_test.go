package api

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"resolvarr/internal/core"
	"resolvarr/internal/core/dvdetect"
)

// scan_dv_detail_test.go — focused unit tests for the M4b dv-detail
// handler. Covers the early-out paths (disabled / tools-not-wired /
// tools-not-installed) and the flatten helper, since those don't
// need a working Arr client. Per-movie loop integration testing
// requires an httptest.Server fronting a fake Radarr; that lands as
// a follow-up if/when scan handler integration tests grow more
// generally across the api package (currently zero exist).

func TestFlattenDvDetailRollup_StableSort(t *testing.T) {
	in := map[string]int{
		"keep|cm2":   5,
		"add|fel":    10,
		"add|cm4":    3,
		"remove|mel": 1,
	}
	got := flattenDvDetailRollup(in)
	want := []scanDvDetailRollup{
		{Action: "add", Tag: "cm4", Count: 3},
		{Action: "add", Tag: "fel", Count: 10},
		{Action: "keep", Tag: "cm2", Count: 5},
		{Action: "remove", Tag: "mel", Count: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestFlattenDvDetailRollup_MalformedKeyDropped(t *testing.T) {
	in := map[string]int{
		"add|fel":      1,
		"no-pipe-here": 9,
		"empty|":       2,
	}
	got := flattenDvDetailRollup(in)
	if len(got) != 2 {
		t.Errorf("expected 2 entries (malformed dropped), got %d: %+v", len(got), got)
	}
	for _, e := range got {
		if e.Action == "" {
			t.Errorf("flattened entry has empty action: %+v", e)
		}
	}
}

// minimalServer builds a Server with just enough wired up for the
// early-out paths to fire. arrClientFor would 502 on first call, but
// the early-outs return apiError before that path runs.
func minimalServer(cfg core.Config) *Server {
	app := &core.App{}
	// ConfigStore can't be nil because runDvDetail goes through the
	// dispatcher's cfg argument, not s.App.Config.Get(). For the
	// early-out tests we hand cfg in directly, so app.Config can stay
	// zero-value.
	return &Server{App: app}
}

func TestRunDvDetail_DisabledReturns400(t *testing.T) {
	cfg := core.Config{
		DvDetail: core.DvDetailConfig{Enabled: false},
	}
	s := minimalServer(cfg)
	inst := &core.Instance{ID: "r", Name: "Radarr", Type: "radarr"}
	_, apiErr := s.runDvDetail(context.Background(), cfg, inst, "radarr", scanRunRequest{Mode: "preview"})
	if apiErr == nil {
		t.Fatal("expected apiError, got nil")
	}
	if apiErr.Status != 400 {
		t.Errorf("status = %d, want 400", apiErr.Status)
	}
	if !strings.Contains(apiErr.Message,"not enabled") {
		t.Errorf("message = %q, want substring 'not enabled'", apiErr.Message)
	}
}

func TestRunDvDetail_ToolsNotWiredReturns400(t *testing.T) {
	cfg := core.Config{
		DvDetail: core.DvDetailConfig{Enabled: true},
	}
	s := minimalServer(cfg)
	// s.DvTools.Dir is the zero value "" — tools-not-wired path.
	inst := &core.Instance{ID: "r", Type: "radarr"}
	_, apiErr := s.runDvDetail(context.Background(), cfg, inst, "radarr", scanRunRequest{Mode: "preview"})
	if apiErr == nil {
		t.Fatal("expected apiError, got nil")
	}
	if apiErr.Status != 400 {
		t.Errorf("status = %d, want 400", apiErr.Status)
	}
	if !strings.Contains(apiErr.Message,"not configured") {
		t.Errorf("message = %q, want substring 'not configured'", apiErr.Message)
	}
}

func TestRunDvDetail_ToolsNotInstalledReturns400(t *testing.T) {
	cfg := core.Config{
		DvDetail: core.DvDetailConfig{Enabled: true},
	}
	s := minimalServer(cfg)
	// Tools.Dir set, but VersionFn returns an error so Status reports
	// Installed=false. Mirrors a fresh container where the user
	// flipped Enabled but hasn't clicked Install yet.
	s.DvTools = dvdetect.Tools{
		Dir: t.TempDir(),
		VersionFn: func(ctx context.Context, bin, flag string) (string, error) {
			return "", errors.New("not present")
		},
	}
	inst := &core.Instance{ID: "r", Type: "radarr"}
	_, apiErr := s.runDvDetail(context.Background(), cfg, inst, "radarr", scanRunRequest{Mode: "preview"})
	if apiErr == nil {
		t.Fatal("expected apiError, got nil")
	}
	if apiErr.Status != 400 {
		t.Errorf("status = %d, want 400", apiErr.Status)
	}
	if !strings.Contains(apiErr.Message, "not reachable") {
		t.Errorf("message = %q, want substring 'not reachable'", apiErr.Message)
	}
}

