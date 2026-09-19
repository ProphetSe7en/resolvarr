package api

import (
	"encoding/json"
	"sync"
	"testing"

	"resolvarr/internal/core"
)

// webhook_stuck_reconcile_test.go — scope resolution + sweep coalescing.
//
// Both exist to honour Architectural rule 2 (webhook_adapters.go): an
// adapter acts on the item its event names and nothing more. A season
// pack emits one Download event per episode, all naming the same series,
// so without coalescing one grab would queue N identical sweeps of the
// same queue — the "chatty Connect feed" scaling trap that rule calls
// out by name.

func downloadBody(t *testing.T, payload map[string]any) []byte {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return b
}

func TestStuckReconcileScope(t *testing.T) {
	cases := []struct {
		name       string
		appType    string
		payload    map[string]any
		wantMovie  int
		wantSeries int
		wantLabel  string
		wantOK     bool
	}{
		{
			name:      "radarr movie",
			appType:   "radarr",
			payload:   map[string]any{"movie": map[string]any{"id": 42, "title": "Dune: Part Two"}},
			wantMovie: 42,
			wantLabel: "Dune: Part Two",
			wantOK:    true,
		},
		{
			name:       "sonarr series",
			appType:    "sonarr",
			payload:    map[string]any{"series": map[string]any{"id": 7, "title": "The Bear"}},
			wantSeries: 7,
			wantLabel:  "The Bear",
			wantOK:     true,
		},
		{
			// A titleless item still scopes correctly — the label is
			// only ever cosmetic, the id is what narrows the sweep.
			name:      "movie without a title still scopes",
			appType:   "radarr",
			payload:   map[string]any{"movie": map[string]any{"id": 9, "title": ""}},
			wantMovie: 9,
			wantLabel: "this movie",
			wantOK:    true,
		},
		{
			// Nothing to narrow to => clean skip, never a full sweep.
			name:    "radarr event with no movie block",
			appType: "radarr",
			payload: map[string]any{"eventType": "Download"},
			wantOK:  false,
		},
		{
			name:    "sonarr event with no series block",
			appType: "sonarr",
			payload: map[string]any{"eventType": "Download"},
			wantOK:  false,
		},
		{
			// Cross-type payloads must not resolve: a Sonarr rule fed a
			// movie block has nothing legitimate to act on.
			name:    "sonarr rule, movie payload",
			appType: "sonarr",
			payload: map[string]any{"movie": map[string]any{"id": 42, "title": "Dune"}},
			wantOK:  false,
		},
		{
			name:    "unknown app type",
			appType: "lidarr",
			payload: map[string]any{"movie": map[string]any{"id": 1, "title": "x"}},
			wantOK:  false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, s, label, ok := stuckReconcileScope(tc.appType, downloadBody(t, tc.payload))
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !tc.wantOK {
				return
			}
			if m != tc.wantMovie || s != tc.wantSeries {
				t.Errorf("ids = (movie %d, series %d), want (%d, %d)", m, s, tc.wantMovie, tc.wantSeries)
			}
			if label != tc.wantLabel {
				t.Errorf("label = %q, want %q", label, tc.wantLabel)
			}
		})
	}
}

func TestStuckReconcileScope_MalformedBody(t *testing.T) {
	if _, _, _, ok := stuckReconcileScope("radarr", []byte("{not json")); ok {
		t.Error("malformed body resolved a scope, want a clean skip")
	}
}

func TestClaimStuckSweep_CoalescesSeasonPack(t *testing.T) {
	s := &Server{}
	// 24 episodes of one series under one rule collapse to ONE sweep.
	key := stuckSweepKey("rule-1", "sonarr", 0, 7)
	claimed := 0
	for i := 0; i < 24; i++ {
		if s.claimStuckSweep(key) {
			claimed++
		}
	}
	if claimed != 1 {
		t.Errorf("claimed %d sweeps for one season pack, want 1", claimed)
	}

	// A different series under the same rule is its own sweep.
	if !s.claimStuckSweep(stuckSweepKey("rule-1", "sonarr", 0, 8)) {
		t.Error("a different series was wrongly coalesced")
	}
	// So is the same series under a different rule — two rules each
	// have their own targets and their own history entry.
	if !s.claimStuckSweep(stuckSweepKey("rule-2", "sonarr", 0, 7)) {
		t.Error("a different rule was wrongly coalesced")
	}

	// Once the sweep starts, a later import for that series queues again.
	s.releaseStuckSweep(key)
	if !s.claimStuckSweep(key) {
		t.Error("key stayed claimed after release — later imports would never sweep again")
	}
}

func TestClaimStuckSweep_Concurrent(t *testing.T) {
	// Connect events arrive on separate handler goroutines, so the
	// claim has to be race-free AND exactly-once under contention.
	s := &Server{}
	key := stuckSweepKey("rule-1", "radarr", 42, 0)
	const n = 50
	var wg sync.WaitGroup
	results := make(chan bool, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- s.claimStuckSweep(key)
		}()
	}
	wg.Wait()
	close(results)
	claimed := 0
	for r := range results {
		if r {
			claimed++
		}
	}
	if claimed != 1 {
		t.Errorf("%d goroutines claimed the same key, want exactly 1", claimed)
	}
}

func TestStuckReconcileDeferDelay(t *testing.T) {
	cases := []struct {
		name string
		rule *core.WebhookRule
		want int // seconds
	}{
		{"nil rule falls back to the default", nil, 60},
		{"no config falls back to the default", &core.WebhookRule{}, 60},
		{
			"zero means default",
			&core.WebhookRule{StuckReconcile: &core.StuckReconcileRules{DeferSeconds: 0}},
			60,
		},
		{
			"explicit value is honoured",
			&core.WebhookRule{StuckReconcile: &core.StuckReconcileRules{DeferSeconds: 120}},
			120,
		},
		{
			// A hand-edited config must not park a timer for hours.
			"over the cap is clamped",
			&core.WebhookRule{StuckReconcile: &core.StuckReconcileRules{DeferSeconds: 99999}},
			maxStuckReconcileDeferSeconds,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := int(stuckReconcileDeferDelay(tc.rule).Seconds()); got != tc.want {
				t.Errorf("delay = %ds, want %ds", got, tc.want)
			}
		})
	}
}
