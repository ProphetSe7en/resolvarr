package api

import (
	"strings"
	"testing"

	"resolvarr/internal/core"
)

func TestPlexSyncModeField_PerLabelBreakdown(t *testing.T) {
	run := &core.PlexLabelRuleRun{
		RunMode:    "apply",
		ItemsTotal: 484,
		Matched:    482,
		Unmatched:  2,
		Added:      map[string]int{"FEL": 60, "MEL": 12},
		Removed:    map[string]int{"FEL": 3},
		InSync:     map[string]int{"FEL": 33},
	}
	fields := plexSyncModeField(core.RunSummary{Result: run})
	if len(fields) != 1 {
		t.Fatalf("got %d fields, want 1: %+v", len(fields), fields)
	}
	f := fields[0]
	if f.Name != "Plex sync" {
		t.Errorf("field name = %q, want 'Plex sync'", f.Name)
	}
	for _, want := range []string{"Matched 482 / 484", "(2 unmatched)", "apply", "FEL: +60, -3, 33 in sync", "MEL: +12"} {
		if !strings.Contains(f.Value, want) {
			t.Errorf("value missing %q:\n%s", want, f.Value)
		}
	}
}

func TestPlexSyncModeField_PreviewModeLabel(t *testing.T) {
	run := &core.PlexLabelRuleRun{
		RunMode:    "preview",
		ItemsTotal: 100,
		Matched:    100,
		Added:      map[string]int{"EN": 5},
	}
	f := plexSyncModeField(core.RunSummary{Result: run})[0]
	if !strings.Contains(f.Value, "· preview") {
		t.Errorf("value missing preview marker: %s", f.Value)
	}
	// No unmatched → that clause must be absent.
	if strings.Contains(f.Value, "unmatched") {
		t.Errorf("value should omit unmatched when zero: %s", f.Value)
	}
}

func TestPlexSyncModeField_FallsBackToSummary(t *testing.T) {
	// No structured run attached → emit the one-line summary so something
	// still lands in the embed.
	fields := plexSyncModeField(core.RunSummary{Summary: "Preview: Matched 480 of 480 items"})
	if len(fields) != 1 {
		t.Fatalf("got %d fields, want 1", len(fields))
	}
	if !strings.Contains(fields[0].Value, "Matched 480 of 480") {
		t.Errorf("fallback value missing summary text: %s", fields[0].Value)
	}
}
