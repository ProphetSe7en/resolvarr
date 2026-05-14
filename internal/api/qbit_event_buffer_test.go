package api

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestResolveWindow_ZeroUsesDefault — stored 0 (e.g. legacy rules
// without the field, or new rules where user kept the default) maps
// to defaultAggregationWindow, NOT to zero-duration "fire instantly".
// The "instant" semantic was deliberately dropped — see the field
// comment in webhook_rules.go.
func TestResolveWindow_ZeroUsesDefault(t *testing.T) {
	if got := resolveWindow(0); got != defaultAggregationWindow {
		t.Errorf("resolveWindow(0) = %v, want %v", got, defaultAggregationWindow)
	}
}

func TestResolveWindow_NegativeUsesDefault(t *testing.T) {
	if got := resolveWindow(-5); got != defaultAggregationWindow {
		t.Errorf("resolveWindow(-5) = %v, want %v", got, defaultAggregationWindow)
	}
}

func TestResolveWindow_ClampsLow(t *testing.T) {
	// Anything 1..1s rounds to minAggregationWindow (1s).
	if got := resolveWindow(1); got != minAggregationWindow {
		t.Errorf("resolveWindow(1) = %v, want %v", got, minAggregationWindow)
	}
}

func TestResolveWindow_ClampsHigh(t *testing.T) {
	if got := resolveWindow(99999); got != maxAggregationWindow {
		t.Errorf("resolveWindow(99999) = %v, want %v", got, maxAggregationWindow)
	}
}

func TestResolveWindow_NormalRange(t *testing.T) {
	if got := resolveWindow(60); got != 60*time.Second {
		t.Errorf("resolveWindow(60) = %v, want 60s", got)
	}
	if got := resolveWindow(120); got != 120*time.Second {
		t.Errorf("resolveWindow(120) = %v, want 120s", got)
	}
}

// TestQbitEventBuffer_OneWindow_OneFlush — base case: a single event
// for a rule opens a window, the window expires, the flush callback
// is invoked exactly once with that one event.
func TestQbitEventBuffer_OneWindow_OneFlush(t *testing.T) {
	var (
		flushed   atomic.Int32
		gotEvents []qbitAddEvent
		gotRule   string
		mu        sync.Mutex
	)
	buf := newQbitEventBuffer(func(_ context.Context, ruleID string, events []qbitAddEvent) {
		flushed.Add(1)
		mu.Lock()
		gotRule = ruleID
		gotEvents = append(gotEvents[:0], events...)
		mu.Unlock()
	})

	buf.Enqueue("rule-A", qbitAddEvent{InfoHash: "abc", Name: "Show.S01E01"}, 1)

	if buf.PendingCount() != 1 {
		t.Errorf("PendingCount after enqueue = %d, want 1", buf.PendingCount())
	}

	// Wait for the 1s window to fire — give a small margin for
	// scheduler latency.
	waitForFlush(t, &flushed, 1, 3*time.Second)

	mu.Lock()
	defer mu.Unlock()
	if gotRule != "rule-A" {
		t.Errorf("flushed ruleID = %q, want rule-A", gotRule)
	}
	if len(gotEvents) != 1 || gotEvents[0].InfoHash != "abc" {
		t.Errorf("flushed events = %+v, want one event with hash=abc", gotEvents)
	}
	if buf.PendingCount() != 0 {
		t.Errorf("PendingCount after flush = %d, want 0", buf.PendingCount())
	}
}

// TestQbitEventBuffer_BurstAggregation — five enqueues in rapid
// succession for the same rule become ONE flush with five events.
// The whole point of the buffer.
func TestQbitEventBuffer_BurstAggregation(t *testing.T) {
	var (
		flushed atomic.Int32
		gotLen  atomic.Int32
	)
	buf := newQbitEventBuffer(func(_ context.Context, _ string, events []qbitAddEvent) {
		flushed.Add(1)
		gotLen.Store(int32(len(events)))
	})

	for i := 0; i < 5; i++ {
		buf.Enqueue("rule-A", qbitAddEvent{InfoHash: "h", Name: "n"}, 1)
	}

	if buf.PendingCount() != 1 {
		t.Errorf("PendingCount = %d, want 1 (all events in one window)", buf.PendingCount())
	}

	waitForFlush(t, &flushed, 1, 3*time.Second)

	if got := gotLen.Load(); got != 5 {
		t.Errorf("flushed events count = %d, want 5", got)
	}
	if flushed.Load() != 1 {
		t.Errorf("flush invocations = %d, want exactly 1", flushed.Load())
	}
}

// TestQbitEventBuffer_DistinctRulesIndependent — events for rule-A
// and rule-B open separate windows that flush independently. Window
// for one rule doesn't drain the other.
func TestQbitEventBuffer_DistinctRulesIndependent(t *testing.T) {
	var (
		mu     sync.Mutex
		drains = map[string]int{}
	)
	buf := newQbitEventBuffer(func(_ context.Context, ruleID string, _ []qbitAddEvent) {
		mu.Lock()
		drains[ruleID]++
		mu.Unlock()
	})

	buf.Enqueue("rule-A", qbitAddEvent{InfoHash: "a"}, 1)
	buf.Enqueue("rule-B", qbitAddEvent{InfoHash: "b"}, 1)

	if buf.PendingCount() != 2 {
		t.Errorf("PendingCount = %d, want 2 (one window per rule)", buf.PendingCount())
	}

	// Wait for both windows.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		done := drains["rule-A"] == 1 && drains["rule-B"] == 1
		mu.Unlock()
		if done {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if drains["rule-A"] != 1 {
		t.Errorf("rule-A drains = %d, want 1", drains["rule-A"])
	}
	if drains["rule-B"] != 1 {
		t.Errorf("rule-B drains = %d, want 1", drains["rule-B"])
	}
}

// TestQbitEventBuffer_FlushAll_DrainsImmediately — graceful shutdown
// path. FlushAll synchronously processes every pending window without
// waiting for timers.
func TestQbitEventBuffer_FlushAll_DrainsImmediately(t *testing.T) {
	var flushed atomic.Int32
	buf := newQbitEventBuffer(func(_ context.Context, _ string, _ []qbitAddEvent) {
		flushed.Add(1)
	})

	// Enqueue with a long window so timers wouldn't fire during the test.
	buf.Enqueue("rule-A", qbitAddEvent{InfoHash: "a"}, 60)
	buf.Enqueue("rule-B", qbitAddEvent{InfoHash: "b"}, 60)
	buf.Enqueue("rule-C", qbitAddEvent{InfoHash: "c"}, 60)

	if buf.PendingCount() != 3 {
		t.Fatalf("PendingCount before FlushAll = %d, want 3", buf.PendingCount())
	}

	buf.FlushAll()

	if got := flushed.Load(); got != 3 {
		t.Errorf("flush invocations after FlushAll = %d, want 3", got)
	}
	if buf.PendingCount() != 0 {
		t.Errorf("PendingCount after FlushAll = %d, want 0", buf.PendingCount())
	}
}

// TestQbitEventBuffer_EmptyRuleIDIgnored — defensive: empty ruleID
// should never reach the buffer (the handler validates upstream),
// but if it does the enqueue is a silent no-op.
func TestQbitEventBuffer_EmptyRuleIDIgnored(t *testing.T) {
	var flushed atomic.Int32
	buf := newQbitEventBuffer(func(_ context.Context, _ string, _ []qbitAddEvent) {
		flushed.Add(1)
	})

	buf.Enqueue("", qbitAddEvent{InfoHash: "h"}, 1)

	if buf.PendingCount() != 0 {
		t.Errorf("PendingCount after empty-ruleID enqueue = %d, want 0", buf.PendingCount())
	}
	// Wait briefly to confirm no flush fires.
	time.Sleep(200 * time.Millisecond)
	if got := flushed.Load(); got != 0 {
		t.Errorf("flush invocations after empty-ruleID enqueue = %d, want 0", got)
	}
}

// TestQbitEventBuffer_FlushAll_Idempotent — calling FlushAll twice
// in a row drains once (second call is a no-op).
func TestQbitEventBuffer_FlushAll_Idempotent(t *testing.T) {
	var flushed atomic.Int32
	buf := newQbitEventBuffer(func(_ context.Context, _ string, _ []qbitAddEvent) {
		flushed.Add(1)
	})

	buf.Enqueue("rule-A", qbitAddEvent{InfoHash: "a"}, 60)
	buf.FlushAll()
	buf.FlushAll() // second call should be a no-op

	if got := flushed.Load(); got != 1 {
		t.Errorf("flush invocations after double FlushAll = %d, want 1", got)
	}
}

// waitForFlush polls the counter until it reaches `want` or `timeout`
// elapses. Timer-driven tests need a small margin because the
// AfterFunc-spawned goroutine may take a few ms to run after expiry.
func waitForFlush(t *testing.T, counter *atomic.Int32, want int32, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if counter.Load() >= want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("waitForFlush timed out: counter = %d, want %d after %v", counter.Load(), want, timeout)
}
