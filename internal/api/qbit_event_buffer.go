package api

// qbit_event_buffer.go — per-rule fixed-window debouncer for qBit-side
// "torrent added" hook events. The hook (Slice 3 of M-qBit-add) catches
// cross-seed and any non-Sonarr-Connect torrent additions; bursts of
// 5-10 events within seconds are common (cross-seed swap-search). The
// buffer collapses bursts into ONE history entry + ONE notification
// per rule per window.
//
// Lifecycle:
//   1. handleQbitTorrentAdded receives a hook → calls Enqueue(ruleID, ev, windowSec)
//   2. First event for a rule opens a window (time.AfterFunc timer)
//   3. Subsequent events for the same rule append to the open window
//   4. Window expires → flush() drains events + invokes flushFn
//   5. flushFn (server-supplied closure) classifies + applies tags
//      + writes ONE WebhookRuleRun history entry per drained window
//
// Concurrency:
//   - All reads/writes of pending map go through b.mu
//   - Timer's AfterFunc closure runs flush(ruleID) on a goroutine —
//     also locks b.mu
//   - FlushAll (graceful shutdown) atomically swaps the map then
//     drains outside the lock; in-flight Enqueues racing with shutdown
//     may open new windows that won't get drained — acceptable since
//     the buffer is in-memory anyway, restart loses pending events
//     by design

import (
	"context"
	"sync"
	"time"
)

const (
	// defaultAggregationWindow applies when a rule's
	// QbitSeRules.AggregationWindowSeconds is 0 (unset / migration
	// safety). Cross-seed bursts typically complete within 30s; 60s
	// gives headroom while keeping latency tolerable.
	defaultAggregationWindow = 60 * time.Second

	// minAggregationWindow caps the lower bound. 1s is "near instant"
	// for users who want minimal aggregation but still want bursts
	// coalesced; sub-second windows offer no practical benefit + add
	// timer churn.
	minAggregationWindow = 1 * time.Second

	// maxAggregationWindow caps the upper bound. Belt-and-braces
	// against typos like 86400 — schedule editor will validate this
	// in Slice 6 too.
	maxAggregationWindow = 1 * time.Hour
)

// qbitAddEvent is one entry in the per-rule debounce buffer. Captured
// from the qBit "Run external program on torrent added" hook payload
// at receive time; replayed at window-close through the flush callback.
type qbitAddEvent struct {
	InfoHash string    // qBit torrent hash (lowercased at receive)
	Name     string    // torrent name — what engine.DetermineQbitTag classifies
	Category string    // qBit category at add-time (may be empty)
	Received time.Time // for ordering inside the window
}

// qbitFlushFn is invoked when a window expires. The buffer hands the
// caller (the api.Server) a snapshot of all events that landed in the
// window for the given rule — caller is responsible for classify +
// apply + history-append. Always called with a non-empty events
// slice (the buffer never opens a window for zero events).
type qbitFlushFn func(ctx context.Context, ruleID string, events []qbitAddEvent)

// qbitEventBuffer is the buffer struct. Construct via newQbitEventBuffer.
type qbitEventBuffer struct {
	mu      sync.Mutex
	pending map[string]*pendingQbitWindow // key: ruleID
	flush   qbitFlushFn
}

type pendingQbitWindow struct {
	ruleID   string
	events   []qbitAddEvent
	timer    *time.Timer
	openedAt time.Time
}

// newQbitEventBuffer constructs a buffer with the given flush callback.
// Caller wires the callback to do classification + tag-apply + history
// append. Flush runs in a goroutine spawned by time.AfterFunc.
func newQbitEventBuffer(flush qbitFlushFn) *qbitEventBuffer {
	return &qbitEventBuffer{
		pending: make(map[string]*pendingQbitWindow),
		flush:   flush,
	}
}

// resolveWindow normalises a rule's stored AggregationWindowSeconds
// into the actual time.Duration the buffer uses. Zero → default,
// out-of-range values clamped. Pure function for testability.
func resolveWindow(seconds int) time.Duration {
	if seconds <= 0 {
		return defaultAggregationWindow
	}
	d := time.Duration(seconds) * time.Second
	if d < minAggregationWindow {
		return minAggregationWindow
	}
	if d > maxAggregationWindow {
		return maxAggregationWindow
	}
	return d
}

// Enqueue appends an event to the rule's debounce window, opening a
// new window if none exists. windowSeconds is the rule's configured
// AggregationWindowSeconds (passed through resolveWindow).
//
// Safe to call concurrently from multiple receiver goroutines for
// the same rule — the lock around the pending map serialises window
// open + append.
func (b *qbitEventBuffer) Enqueue(ruleID string, ev qbitAddEvent, windowSeconds int) {
	if ruleID == "" {
		return
	}
	window := resolveWindow(windowSeconds)

	b.mu.Lock()
	defer b.mu.Unlock()

	if pw, exists := b.pending[ruleID]; exists {
		// Append to open window. Late events between timer-fire and
		// flush-acquiring-lock get included in the flush — fine, no
		// correctness issue.
		pw.events = append(pw.events, ev)
		return
	}

	// Open new window. Capture ruleID in the closure so AfterFunc
	// can drain the right entry.
	pw := &pendingQbitWindow{
		ruleID:   ruleID,
		events:   []qbitAddEvent{ev},
		openedAt: time.Now().UTC(),
	}
	pw.timer = time.AfterFunc(window, func() { b.fire(ruleID) })
	b.pending[ruleID] = pw
}

// fire is the AfterFunc-triggered drain. Pops the window for ruleID
// and invokes the flush callback. Called on the timer goroutine.
func (b *qbitEventBuffer) fire(ruleID string) {
	b.mu.Lock()
	pw, exists := b.pending[ruleID]
	if !exists {
		// Window was already drained (FlushAll, or duplicate fire from
		// a manually-stopped timer). Defensive no-op.
		b.mu.Unlock()
		return
	}
	delete(b.pending, ruleID)
	events := pw.events
	b.mu.Unlock()

	// Flush outside the lock — classify/apply/persist may take
	// hundreds of ms (Arr API roundtrips, ConfigStore.Update),
	// holding the lock would block other Enqueues unnecessarily.
	if len(events) > 0 {
		b.flush(context.Background(), ruleID, events)
	}
}

// FlushAll drains every pending window synchronously. Called on
// graceful shutdown so in-flight cross-seed bursts get processed
// before the process exits. Safe to call multiple times.
//
// Race window: an Enqueue arriving after FlushAll's lock release
// but before the process exits will open a NEW window that won't
// be drained. Acceptable — buffer is in-memory by design and a
// hard kill would lose pending events anyway. main.go should call
// FlushAll before the http.Server.Shutdown ack so the receive
// path is already drained.
func (b *qbitEventBuffer) FlushAll() {
	b.mu.Lock()
	pendingSnap := b.pending
	b.pending = make(map[string]*pendingQbitWindow)
	b.mu.Unlock()

	for ruleID, pw := range pendingSnap {
		// Stop the timer so its AfterFunc doesn't double-drain. fire()
		// is defensive against this anyway (delete() already happened),
		// but stopping prevents a useless goroutine launch.
		pw.timer.Stop()
		if len(pw.events) > 0 {
			b.flush(context.Background(), ruleID, pw.events)
		}
	}
}

// PendingCount returns the number of rules with open windows. Test +
// debug helper; production code shouldn't read this for logic.
func (b *qbitEventBuffer) PendingCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.pending)
}
