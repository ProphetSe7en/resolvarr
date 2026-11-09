package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"resolvarr/internal/core"
)

// Schedule validation for the reconcile (stuck downloads) mode.

func reconcileScheduleCfg() core.Config {
	return core.Config{
		Instances:     []core.Instance{{ID: "i1", Type: "sonarr"}},
		QbitInstances: []core.QbitInstance{{ID: "q1", Name: "tv"}},
	}
}

func reconcileScheduleReq(rc *core.ReconcileConfig) *scheduleRequest {
	return &scheduleRequest{
		Name:       "stuck",
		Mode:       core.JobModeReconcile,
		InstanceID: "i1",
		Options:    core.JobOptions{RunMode: "apply"},
		Reconcile:  rc,
	}
}

func TestScheduleRequest_ReconcileApplyAccepted(t *testing.T) {
	req := reconcileScheduleReq(&core.ReconcileConfig{
		Targets: []core.StuckReconcileTarget{{QbitInstanceID: " q1 ", PostCategory: " cleanup "}},
	})
	if err := req.validate(reconcileScheduleCfg()); err != nil {
		t.Fatalf("valid reconcile schedule rejected: %v", err)
	}
	if got := req.Reconcile.Targets[0]; got.QbitInstanceID != "q1" || got.PostCategory != "cleanup" {
		t.Errorf("targets not trimmed before storing: %+v", got)
	}
}

func TestScheduleRequest_ReconcileApplyNeedsTargets(t *testing.T) {
	if err := reconcileScheduleReq(nil).validate(reconcileScheduleCfg()); err == nil {
		t.Error("apply-mode reconcile schedule with no reconcile config accepted")
	}
	if err := reconcileScheduleReq(&core.ReconcileConfig{}).validate(reconcileScheduleCfg()); err == nil {
		t.Error("apply-mode reconcile schedule with no targets accepted")
	}
}

func TestScheduleRequest_ReconcileValidatesLegacyPair(t *testing.T) {
	// The legacy single-client pair is folded into the run's targets, so
	// it must be validated like one.
	cases := map[string]*core.ReconcileConfig{
		"unknown client":   {QbitInstanceID: "gone", PostCategory: "cleanup"},
		"missing category": {QbitInstanceID: "q1"},
	}
	for name, rc := range cases {
		if err := reconcileScheduleReq(rc).validate(reconcileScheduleCfg()); err == nil {
			t.Errorf("%s: legacy reconcile pair accepted, want rejection", name)
		}
	}
	ok := reconcileScheduleReq(&core.ReconcileConfig{QbitInstanceID: "q1", PostCategory: "cleanup"})
	if err := ok.validate(reconcileScheduleCfg()); err != nil {
		t.Errorf("valid legacy pair rejected: %v", err)
	}
}

func TestScheduleRequest_ReconcileRejectsDuplicateClient(t *testing.T) {
	req := reconcileScheduleReq(&core.ReconcileConfig{
		Targets: []core.StuckReconcileTarget{
			{QbitInstanceID: "q1", PostCategory: "a"},
			{QbitInstanceID: "q1", PostCategory: "b"},
		},
	})
	if err := req.validate(reconcileScheduleCfg()); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Errorf("duplicate client: got %v, want a 'twice' rejection", err)
	}
}

func TestScheduleRequest_ReconcileIsNotACombinedPhase(t *testing.T) {
	req := &scheduleRequest{
		Name:       "chain",
		Mode:       core.JobModeCombined,
		InstanceID: "i1",
		Options:    core.JobOptions{CombinedModes: []core.JobMode{core.JobModeReconcile}},
	}
	if err := req.validate(reconcileScheduleCfg()); err == nil {
		t.Error("combined schedule with a reconcile phase accepted — the combined runner would skip it")
	}
}

func TestHandleUpdateSchedule_PauseKeepsSavedReconcileSnapshot(t *testing.T) {
	// The schedule card's pause/resume PUT doesn't echo the reconcile
	// snapshot. It must validate against the saved one, not be rejected
	// as "apply mode needs a target".
	store := core.NewConfigStore(t.TempDir())
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := store.Update(func(c *core.Config) {
		cfg := reconcileScheduleCfg()
		c.Instances = cfg.Instances
		c.QbitInstances = cfg.QbitInstances
		c.Schedules = []core.ScheduledJob{{
			ID: "j1", Name: "stuck", Mode: core.JobModeReconcile, InstanceID: "i1", Enabled: true,
			Options:   core.JobOptions{RunMode: "apply"},
			Reconcile: &core.ReconcileConfig{Targets: []core.StuckReconcileTarget{{QbitInstanceID: "q1", PostCategory: "cleanup"}}},
		}}
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	s := &Server{App: &core.App{Config: store}}
	body := `{"name":"stuck","mode":"reconcile","instanceId":"i1","enabled":false,"options":{"runMode":"apply"}}`
	req := httptest.NewRequest(http.MethodPut, "/api/schedules/j1", strings.NewReader(body))
	req.SetPathValue("id", "j1")
	rec := httptest.NewRecorder()
	s.handleUpdateSchedule(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("pause PUT = %d %s, want 200", rec.Code, rec.Body.String())
	}
	got := store.Get().Schedules[0]
	if got.Enabled || got.Reconcile == nil || len(got.Reconcile.Targets) != 1 {
		t.Errorf("after pause: enabled=%v reconcile=%+v, want paused with the snapshot kept", got.Enabled, got.Reconcile)
	}
}
