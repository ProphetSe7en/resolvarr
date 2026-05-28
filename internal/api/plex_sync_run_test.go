package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"resolvarr/internal/core"
)

// postPlexSyncRun is a tiny helper that marshals a request body and
// drives handleRunPlexSync, returning the recorder.
func postPlexSyncRun(t *testing.T, s *Server, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	s.handleRunPlexSync(rr, httptest.NewRequest(http.MethodPost, "/api/plex-sync/run", bytes.NewReader(b)))
	return rr
}

// TestValidJobMode_PlexSync locks the new schedule mode into the
// accepted set so a plexsync schedule survives the save-time guard.
func TestValidJobMode_PlexSync(t *testing.T) {
	if !core.ValidJobMode(core.JobModePlexSync) {
		t.Fatal("JobModePlexSync should be a valid job mode")
	}
}

// TestRunPlexSync_RejectsMissingConfig — the one-off endpoint requires
// an inline plexLabelSync config; a body without it is a 400, not a
// nil-deref panic.
func TestRunPlexSync_RejectsMissingConfig(t *testing.T) {
	s, store := newTestServerWithPlex(t)
	seedPlexLabelRuleFixture(t, store)

	rr := postPlexSyncRun(t, s, map[string]any{"arrInstanceId": "arr-r", "runMode": "preview"})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("missing config should be 400, got %d (%s)", rr.Code, rr.Body.String())
	}
}

// TestRunPlexSync_RejectsBadRunMode guards the runMode allowlist.
func TestRunPlexSync_RejectsBadRunMode(t *testing.T) {
	s, store := newTestServerWithPlex(t)
	seedPlexLabelRuleFixture(t, store)

	rr := postPlexSyncRun(t, s, map[string]any{
		"arrInstanceId": "arr-r",
		"runMode":       "nonsense",
		"plexLabelSync": core.PlexLabelSyncConfig{
			PlexInstanceID: "plex-1",
			LibraryKeys:    []string{"1"},
			Labels:         []string{"4k"},
		},
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("bad runMode should be 400, got %d (%s)", rr.Code, rr.Body.String())
	}
}

// TestRunPlexSync_RejectsUnknownArrInstance — a config-resolve failure
// surfaces as 404 before any client is built.
func TestRunPlexSync_RejectsUnknownArrInstance(t *testing.T) {
	s, store := newTestServerWithPlex(t)
	seedPlexLabelRuleFixture(t, store)

	rr := postPlexSyncRun(t, s, map[string]any{
		"arrInstanceId": "arr-missing",
		"plexLabelSync": core.PlexLabelSyncConfig{
			PlexInstanceID: "plex-1",
			LibraryKeys:    []string{"1"},
			Labels:         []string{"4k"},
		},
	})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown Arr instance should be 404, got %d (%s)", rr.Code, rr.Body.String())
	}
}

// TestRunPlexSync_RejectsEmptyLabels exercises the shared validator
// through the one-off path — a config with no labels is rejected before
// the engine runs.
func TestRunPlexSync_RejectsEmptyLabels(t *testing.T) {
	s, store := newTestServerWithPlex(t)
	seedPlexLabelRuleFixture(t, store)

	rr := postPlexSyncRun(t, s, map[string]any{
		"arrInstanceId": "arr-r",
		"plexLabelSync": core.PlexLabelSyncConfig{
			PlexInstanceID: "plex-1",
			LibraryKeys:    []string{"1"},
			Labels:         []string{},
		},
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("empty labels should be 400, got %d (%s)", rr.Code, rr.Body.String())
	}
}

// TestRunPlexSync_RejectsRadarrPointingAtShowLibrary locks the
// Radarr→movie / Sonarr→show library-type filter on the one-off path
// (same guard the standalone CRUD + webhook validators enforce).
func TestRunPlexSync_RejectsRadarrPointingAtShowLibrary(t *testing.T) {
	s, store := newTestServerWithPlex(t)
	seedPlexLabelRuleFixture(t, store)

	rr := postPlexSyncRun(t, s, map[string]any{
		"arrInstanceId": "arr-r", // radarr
		"plexLabelSync": core.PlexLabelSyncConfig{
			PlexInstanceID: "plex-1",
			LibraryKeys:    []string{"3"}, // "TV Shows" — a show library
			Labels:         []string{"4k"},
		},
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("Radarr pointing at show library should be 400, got %d (%s)", rr.Code, rr.Body.String())
	}
}

// TestSchedule_AcceptsPlexSyncSnapshot verifies a plexsync-mode
// schedule persists its inline config — the wire path the Schedule
// wizard's Sync-to-Plex step takes.
func TestSchedule_AcceptsPlexSyncSnapshot(t *testing.T) {
	s, store := newTestServerWithPlex(t)
	seedPlexLabelRuleFixture(t, store)

	body, _ := json.Marshal(map[string]any{
		"name":       "Nightly Plex sync",
		"mode":       "plexsync",
		"instanceId": "arr-r",
		"cron":       "",
		"enabled":    true,
		"plexSync": core.PlexLabelSyncConfig{
			PlexInstanceID: "plex-1",
			LibraryKeys:    []string{"1"},
			Labels:         []string{"4k", "hdr"},
		},
	})
	rr := httptest.NewRecorder()
	s.handleCreateSchedule(rr, httptest.NewRequest(http.MethodPost, "/api/schedules", bytes.NewReader(body)))
	if rr.Code != http.StatusOK {
		t.Fatalf("create plexsync schedule status %d (%s)", rr.Code, rr.Body.String())
	}
	stored := store.Get().Schedules
	if len(stored) != 1 || stored[0].PlexSync == nil {
		t.Fatalf("PlexSync snapshot not persisted: %+v", stored)
	}
	if len(stored[0].PlexSync.Labels) != 2 {
		t.Errorf("PlexSync labels lost: %+v", stored[0].PlexSync)
	}
}

// TestSchedule_PlexSyncDeepCopyIsolation locks the pointer-aliasing
// guard: mutating the PlexSync config on a Get()-returned schedule
// (as the validator does in place) must not corrupt the store.
func TestSchedule_PlexSyncDeepCopyIsolation(t *testing.T) {
	s, store := newTestServerWithPlex(t)
	seedPlexLabelRuleFixture(t, store)

	body, _ := json.Marshal(map[string]any{
		"name":       "Nightly Plex sync",
		"mode":       "plexsync",
		"instanceId": "arr-r",
		"enabled":    true,
		"plexSync": core.PlexLabelSyncConfig{
			PlexInstanceID: "plex-1",
			LibraryKeys:    []string{"1"},
			Labels:         []string{"4k"},
			LabelDisplay:   map[string]string{"4k": "4K"},
		},
	})
	rr := httptest.NewRecorder()
	s.handleCreateSchedule(rr, httptest.NewRequest(http.MethodPost, "/api/schedules", bytes.NewReader(body)))
	if rr.Code != http.StatusOK {
		t.Fatalf("create status %d (%s)", rr.Code, rr.Body.String())
	}

	// Mutate the returned copy as an in-place validator would.
	snap := store.Get().Schedules
	snap[0].PlexSync.Labels[0] = "MUTATED"
	snap[0].PlexSync.LabelDisplay["4k"] = "MUTATED"

	// Re-read — the store must be untouched.
	fresh := store.Get().Schedules
	if fresh[0].PlexSync.Labels[0] != "4k" {
		t.Errorf("store Labels corrupted by caller mutation: %q", fresh[0].PlexSync.Labels[0])
	}
	if fresh[0].PlexSync.LabelDisplay["4k"] != "4K" {
		t.Errorf("store LabelDisplay corrupted by caller mutation: %q", fresh[0].PlexSync.LabelDisplay["4k"])
	}
}
