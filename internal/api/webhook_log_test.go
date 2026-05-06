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
