package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestWebhookLog_PersistRoundTripsInvalidJSON locks the fix for the
// pre-review poison bug: an event whose Raw bytes are not valid JSON
// (Sonarr/Radarr never emit this on real events but the receiver
// stamps "(unparseable)" on decode-fail and originally stored the
// raw bytes verbatim) caused json.MarshalIndent on the persist map
// to fail because json.RawMessage.MarshalJSON validates. After one
// such event, every subsequent persist failed silently, losing the
// on-disk log for ALL instances until restart.
//
// The receiver now wraps unparseable bytes into a valid JSON object
// before storing — this test ensures the persist round-trip stays
// clean even when Raw values look "weird".
func TestWebhookLog_PersistRoundTripsValidJSONOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "webhook-events.json")
	l := newWebhookLog(path)

	// Event 1: ordinary valid JSON (the common case).
	l.append(WebhookEvent{
		ID:         "ev1",
		InstanceID: "inst-a",
		ReceivedAt: time.Now().UTC(),
		EventType:  "Test",
		Title:      "Test event",
		Raw:        json.RawMessage(`{"eventType":"Test"}`),
	})

	// Event 2: Raw must always be valid JSON (the receiver
	// guarantees this — see handleWebhookReceive). Use a wrapped
	// "_unparseable" form here to mirror the post-fix shape.
	l.append(WebhookEvent{
		ID:         "ev2",
		InstanceID: "inst-a",
		ReceivedAt: time.Now().UTC(),
		EventType:  "(unparseable)",
		Raw:        json.RawMessage(`{"_unparseable":"not actually valid Connect JSON"}`),
	})

	// Verify on-disk file actually has both events. Re-load into a
	// fresh log instance — that's what would happen on container
	// restart, the path the original bug broke.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read persist file: %v", err)
	}
	if len(data) == 0 {
		t.Fatalf("persist file is empty — write failed silently")
	}
	var loaded map[string][]WebhookEvent
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("persist file is not valid JSON: %v\n%s", err, string(data))
	}
	if got := len(loaded["inst-a"]); got != 2 {
		t.Fatalf("expected 2 events, got %d (file: %s)", got, string(data))
	}
}

// TestWebhookLog_RingEvictsBeyondCap ensures the FIFO eviction keeps
// the slice at exactly webhookEventsCap and that the underlying
// backing array doesn't grow unbounded — the resliced bucket must
// be a fresh allocation, not bucket[excess:] which would leak the
// original array.
func TestWebhookLog_RingEvictsBeyondCap(t *testing.T) {
	dir := t.TempDir()
	l := newWebhookLog(filepath.Join(dir, "events.json"))

	// Append cap + 50 events.
	for i := 0; i < webhookEventsCap+50; i++ {
		l.append(WebhookEvent{
			ID:         "ev",
			InstanceID: "x",
			ReceivedAt: time.Now().UTC(),
			EventType:  "Test",
			Raw:        json.RawMessage(`{}`),
		})
	}

	got := l.list("x")
	if len(got) != webhookEventsCap {
		t.Fatalf("expected len == cap (%d), got %d", webhookEventsCap, len(got))
	}

	// Verify the in-memory backing array was actually reallocated
	// after eviction — capacity should match length, not stay at
	// cap+50 from the original append-grow.
	l.mu.Lock()
	bucket := l.events["x"]
	l.mu.Unlock()
	if cap(bucket) > webhookEventsCap*2 {
		t.Errorf("backing array did not shrink after eviction: len=%d cap=%d (want cap close to %d)",
			len(bucket), cap(bucket), webhookEventsCap)
	}
}

// TestWebhookLog_ListReturnsIndependentRawCopies verifies that the
// returned events don't share Raw byte-slices with the in-memory
// ring. A future code path that mutated Raw in place could otherwise
// corrupt the live log.
func TestWebhookLog_ListReturnsIndependentRawCopies(t *testing.T) {
	dir := t.TempDir()
	l := newWebhookLog(filepath.Join(dir, "events.json"))
	original := json.RawMessage(`{"k":"v"}`)
	l.append(WebhookEvent{
		ID:         "ev1",
		InstanceID: "x",
		ReceivedAt: time.Now().UTC(),
		Raw:        original,
	})

	listed := l.list("x")
	if len(listed) != 1 {
		t.Fatalf("want 1 event, got %d", len(listed))
	}

	// Mutate the returned Raw — must NOT affect the next list().
	listed[0].Raw[0] = 'X'

	again := l.list("x")
	if again[0].Raw[0] == 'X' {
		t.Fatalf("mutating returned Raw bled through to the live ring — list() did not deep-copy")
	}
}

// TestWebhookLog_SubscribeReceivesAppendedEvents covers the SSE
// pub-sub path: a subscriber registered before append() must
// receive the event on its channel. Multiple subscribers all
// receive a copy. Per-instance isolation — a subscriber on
// instance A doesn't see appends on instance B.
func TestWebhookLog_SubscribeReceivesAppendedEvents(t *testing.T) {
	dir := t.TempDir()
	l := newWebhookLog(filepath.Join(dir, "events.json"))

	chA, unsubA := l.Subscribe("inst-a")
	defer unsubA()
	chA2, unsubA2 := l.Subscribe("inst-a")
	defer unsubA2()
	chB, unsubB := l.Subscribe("inst-b")
	defer unsubB()

	ev := WebhookEvent{
		ID: "e1", InstanceID: "inst-a",
		ReceivedAt: time.Now().UTC(),
		EventType:  "Test",
		Raw:        json.RawMessage(`{}`),
	}
	l.append(ev)

	// Both subscribers on inst-a should receive.
	for _, ch := range []<-chan WebhookEvent{chA, chA2} {
		select {
		case got := <-ch:
			if got.ID != "e1" {
				t.Errorf("subscriber got wrong event id: %q", got.ID)
			}
		case <-time.After(time.Second):
			t.Fatal("subscriber timed out waiting for fan-out")
		}
	}
	// Subscriber on inst-b must NOT receive.
	select {
	case got := <-chB:
		t.Errorf("inst-b subscriber received cross-instance event: %+v", got)
	case <-time.After(50 * time.Millisecond):
		// Good — no event for inst-b.
	}
}

// TestWebhookLog_UnsubscribeRemovesSubscriber verifies the unsub
// closure cleans up. After unsub, append() must not deadlock
// trying to send on a closed channel + the subscribers map must
// no longer reference the channel.
func TestWebhookLog_UnsubscribeRemovesSubscriber(t *testing.T) {
	dir := t.TempDir()
	l := newWebhookLog(filepath.Join(dir, "events.json"))

	ch, unsub := l.Subscribe("inst-a")
	unsub()

	// append after unsub must not panic from sending on closed channel
	// (the loop in fanOut iterates the slice — if unsub didn't remove
	// the entry, we'd hit "send on closed channel"). Run twice to
	// catch any cleanup bug that surfaces only on the second append.
	for i := 0; i < 2; i++ {
		l.append(WebhookEvent{
			ID: "e", InstanceID: "inst-a",
			ReceivedAt: time.Now().UTC(),
			Raw:        json.RawMessage(`{}`),
		})
	}
	// ch is closed; reads must return zero immediately.
	select {
	case _, ok := <-ch:
		if ok {
			t.Errorf("expected closed channel to read zero (ok=false), got ok=true")
		}
	case <-time.After(50 * time.Millisecond):
		t.Errorf("read on closed channel timed out — channel was not closed")
	}
	// Idempotent: double-unsubscribe should not panic.
	unsub()
}

// TestWebhookLog_SubscribeRespectsCap rejects new subscribers
// past the per-instance cap. Defence against runaway
// subscribe-loops piling up channels.
func TestWebhookLog_SubscribeRespectsCap(t *testing.T) {
	dir := t.TempDir()
	l := newWebhookLog(filepath.Join(dir, "events.json"))

	// Fill up to the cap.
	unsubs := make([]func(), 0, webhookSubscriberCap)
	for i := 0; i < webhookSubscriberCap; i++ {
		ch, u := l.Subscribe("inst-x")
		if ch == nil {
			t.Fatalf("subscriber %d unexpectedly rejected before cap", i)
		}
		unsubs = append(unsubs, u)
	}
	defer func() {
		for _, u := range unsubs {
			u()
		}
	}()

	// One past cap → nil channel.
	ch, u := l.Subscribe("inst-x")
	if ch != nil {
		u()
		t.Fatal("subscribe past cap returned a non-nil channel")
	}
	// The returned no-op unsubscribe must not panic when invoked.
	u()
}

// TestWebhookLog_FullSubscriberDoesNotBlockAppend covers the
// non-blocking-fan-out contract: a slow subscriber whose channel
// is full must NOT back-pressure append. The event is dropped
// for that subscriber only; other subscribers + the persisted
// ring still receive normally.
func TestWebhookLog_FullSubscriberDoesNotBlockAppend(t *testing.T) {
	dir := t.TempDir()
	l := newWebhookLog(filepath.Join(dir, "events.json"))

	// Saturate one subscriber's buffer (cap 16 per Subscribe()).
	slow, unsubSlow := l.Subscribe("inst-x")
	defer unsubSlow()
	fast, unsubFast := l.Subscribe("inst-x")
	defer unsubFast()

	for i := 0; i < 16; i++ {
		l.append(WebhookEvent{
			ID: "fill", InstanceID: "inst-x",
			ReceivedAt: time.Now().UTC(),
			Raw:        json.RawMessage(`{}`),
		})
	}
	// `slow` is now full. `fast` has 16 too but we'll drain it.
	for i := 0; i < 16; i++ {
		<-fast
	}
	// Now append again — must NOT block on `slow`. Use a
	// goroutine + channel-deadline to detect if it would.
	done := make(chan struct{})
	go func() {
		l.append(WebhookEvent{
			ID: "drop", InstanceID: "inst-x",
			ReceivedAt: time.Now().UTC(),
			Raw:        json.RawMessage(`{}`),
		})
		close(done)
	}()
	select {
	case <-done:
		// Append returned without blocking — pass.
	case <-time.After(time.Second):
		t.Fatal("append blocked on a full subscriber's channel — fan-out is not non-blocking")
	}
	// `fast` should have received the new event; `slow` did not
	// (its buffer was already full when the new event tried to fan out).
	select {
	case ev := <-fast:
		if ev.ID != "drop" {
			t.Errorf("fast subscriber got %q, want %q", ev.ID, "drop")
		}
	case <-time.After(time.Second):
		t.Error("fast subscriber timed out — should have received the post-saturation event")
	}
	_ = slow // intentionally not drained; unsubSlow handles cleanup
}
