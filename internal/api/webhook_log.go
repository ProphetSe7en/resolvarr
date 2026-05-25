package api

// webhook_log.go — per-instance ring buffer for received Connect events.
// Logging-only feature today; functions wire in a later release. The
// buffer exists so users can verify their Sonarr/Radarr Connect setup
// hits resolvarr (instead of squinting at server stderr) and so we
// capture real-world JSON shapes for the function-mapping work that
// follows.
//
// Persistence: the whole map is written atomically (.tmp → rename) on
// every append. Connect-event rate is low (a few per minute peak) so
// the simpler write-everything pattern beats journaling complexity.
// Loaded once on Server startup; subsequent reads come from the
// in-memory copy under the mutex.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// webhookEventsCap caps the per-instance ring at 100. FIFO eviction
// when the cap is hit. Picked to balance "enough for soak testing"
// against "small enough to JSON-write on every event without I/O
// pressure". 100 events × ~5 KB JSON = ~500 KB peak per instance.
const webhookEventsCap = 100

// webhookSubscriberCap limits live SSE subscribers per instance.
// Defence against a runaway browser extension / loop that piles
// up subscriptions and turns fan-out into an O(N) walk. 32 covers
// every realistic UI usage (one tab, maybe two, plus reconnects);
// past that we assume something's wrong and reject the new stream.
const webhookSubscriberCap = 32

// webhookEventsPath is the on-disk JSON file. Lives under /config so
// it persists across container restarts (same convention as the
// scan dumps under /config/logs/). Single file rather than per-event
// files because the access pattern is "show me the last 100 for this
// instance" — one read covers it.
const webhookEventsPath = "/config/webhook-events.json"

// WebhookEvent is one received Connect event, ready for the recent-
// events panel to render. ID lets the frontend key the row + drives
// the expand-to-see-JSON toggle. EventType / Summary / Title come
// from the parser pulling a few fields out of Raw so the card has
// labels without the frontend re-parsing the JSON. Raw is the full
// decoded body — frontend renders it pretty-printed on expand.
type WebhookEvent struct {
	ID         string          `json:"id"`
	InstanceID string          `json:"instanceId"`
	ReceivedAt time.Time       `json:"receivedAt"`
	EventType  string          `json:"eventType"`         // "Test", "Grab", "Download", etc. — straight from Arr's `eventType` field
	Title      string          `json:"title,omitempty"`   // movie/series title pulled out for the card
	Subtitle   string          `json:"subtitle,omitempty"` // year / S01E05 / etc. — best-effort second line
	Raw        json.RawMessage `json:"raw"`               // full decoded body, pretty-printable on expand
	// Outcomes records which rule(s) fired on this event and what
	// they did. Populated AFTER dispatch completes via
	// webhookLog.attachOutcomes — append() writes the raw event, the
	// handler updates outcomes once rule dispatch returns. Empty slice
	// means "no rule matched"; UI renders that as a grey "no rule
	// matched" chip in the Outcome column.
	//
	// Drives the Recent Activity table's Outcome column + the "Made
	// changes" / "No change" / "Errors only" filters without a join
	// against per-rule history.
	Outcomes []WebhookEventOutcome `json:"outcomes,omitempty"`
}

// WebhookEventOutcome is the per-rule summary attached to a logged
// event. Denormalised copy of the relevant fields from the rule's
// WebhookRuleRun so Recent Activity can render the row WITHOUT a
// join across every rule's History slice. A single event with
// N matching rules carries N outcomes (e.g. tag-rg rule fires +
// recover rule fires on the same Grab event).
//
// StartedAt is the join key back to the WebhookRuleRun in
// Rule.History when the UI needs the full run detail.
type WebhookEventOutcome struct {
	RuleID    string    `json:"ruleId"`
	RuleName  string    `json:"ruleName,omitempty"`
	Status    string    `json:"status"` // "ok" | "partial" | "error"
	Changed   bool      `json:"changed,omitempty"`
	Summary   string    `json:"summary,omitempty"`
	StartedAt time.Time `json:"startedAt"`
}

// webhookLog is a per-instance ring buffer + atomic JSON-file mirror.
// One global instance lives on Server. Mutex serialises everything
// (read/write are equally cheap; events arrive at human pace).
//
// subscribers holds active SSE listeners keyed by instanceID. Each
// listener is a buffered channel — append() fans out new events to
// every subscriber for that instance via a non-blocking send so a
// slow client can't back-pressure the receiver. Slow clients drop
// events; they can refresh to recover via the GET /events endpoint.
// subMu is a separate mutex from `mu` so a long subscriber-iteration
// during fan-out doesn't block append callers from queuing further
// events. (Today fan-out is fast — a handful of subscribers — but
// keeping the mutexes separate keeps the design honest if N grows.)
type webhookLog struct {
	mu          sync.Mutex
	events      map[string][]WebhookEvent // instanceID → recent events, newest last
	persistPath string

	subMu       sync.Mutex
	subscribers map[string][]chan WebhookEvent // instanceID → live SSE listeners
}

// newWebhookLog constructs the log + best-effort loads any persisted
// events from /config/webhook-events.json. Errors during load are
// logged to stderr but never fatal — a corrupt or missing file just
// starts the user with an empty log.
func newWebhookLog(persistPath string) *webhookLog {
	l := &webhookLog{
		events:      make(map[string][]WebhookEvent),
		persistPath: persistPath,
		subscribers: make(map[string][]chan WebhookEvent),
	}
	l.loadFromDisk()
	return l
}

// loadFromDisk reads the JSON file under the mutex. Missing file
// is a normal first-run state. Decode errors are logged + the
// in-memory map stays empty (don't bring down the server because
// some old write was malformed).
func (l *webhookLog) loadFromDisk() {
	l.mu.Lock()
	defer l.mu.Unlock()
	data, err := os.ReadFile(l.persistPath)
	if err != nil {
		if !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "resolvarr: webhook log load: %v\n", err)
		}
		return
	}
	var loaded map[string][]WebhookEvent
	if err := json.Unmarshal(data, &loaded); err != nil {
		fmt.Fprintf(os.Stderr, "resolvarr: webhook log decode: %v (starting empty)\n", err)
		return
	}
	if loaded != nil {
		l.events = loaded
	}
}

// persistLocked writes the current map to disk atomically. Caller
// must hold l.mu. Errors are logged to stderr — losing one event
// to a write failure is acceptable, especially since ring eviction
// already drops events; we don't surface the error to the HTTP
// caller because Sonarr/Radarr would then retry the webhook + spam
// the log on transient FS issues.
func (l *webhookLog) persistLocked() {
	dir := filepath.Dir(l.persistPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "resolvarr: webhook log mkdir: %v\n", err)
		return
	}
	data, err := json.MarshalIndent(l.events, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolvarr: webhook log marshal: %v\n", err)
		return
	}
	tmp := l.persistPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "resolvarr: webhook log write: %v\n", err)
		return
	}
	if err := os.Rename(tmp, l.persistPath); err != nil {
		fmt.Fprintf(os.Stderr, "resolvarr: webhook log rename: %v\n", err)
		_ = os.Remove(tmp)
	}
}

// append pushes a new event to the per-instance ring + persists.
// FIFO eviction once the slice exceeds webhookEventsCap. Returns
// the (possibly new) ring length so the HTTP handler can include
// it in the ack response — handy for Connect setup verification.
//
// After persisting, fans the event out to every active SSE
// subscriber for that instance. Fan-out is non-blocking — a
// subscriber whose buffered channel is full gets the event dropped
// rather than back-pressuring this call. The subscriber can
// recover by re-listing via GET /webhook/events when their handler
// catches up. Connect-event arrival is the hot path; we don't want
// a stuck client to block it.
func (l *webhookLog) append(ev WebhookEvent) int {
	l.mu.Lock()
	bucket := l.events[ev.InstanceID]
	bucket = append(bucket, ev)
	if len(bucket) > webhookEventsCap {
		// FIFO eviction. Allocate a fresh backing array sized
		// exactly to webhookEventsCap so the original (now
		// holding ~5 KB Raw bytes per evicted event) can GC.
		// A naive `bucket = bucket[excess:]` keeps the original
		// array alive forever — slice header just slides forward,
		// the leaked space holds dead WebhookEvent values that
		// the GC can never reclaim because the live slice still
		// references the array.
		excess := len(bucket) - webhookEventsCap
		next := make([]WebhookEvent, webhookEventsCap)
		copy(next, bucket[excess:])
		bucket = next
	}
	l.events[ev.InstanceID] = bucket
	l.persistLocked()
	count := len(bucket)
	l.mu.Unlock()
	// Fan-out happens AFTER releasing the events mutex so a slow
	// channel send can't block append callers waiting on `mu`.
	l.fanOut(ev)
	return count
}

// fanOut delivers the event to every active subscriber for the
// instance. Non-blocking sends — channels are buffered, full
// attachOutcomes updates the most-recent event with the given ID in
// the per-instance ring with the supplied rule outcomes. Called by
// dispatch after rule fires complete so the Recent Activity table
// can render the Outcome column from the event itself (no join
// against per-rule history).
//
// The lookup walks the per-instance ring from the newest end —
// just-appended events are at the end, so this terminates in O(1)
// for the common case. Silently no-op when the event has aged out
// of the ring or never logged (logging disabled per-instance).
//
// Persists immediately so the outcome is durable across container
// restarts. Same write-everything pattern as append().
func (l *webhookLog) attachOutcomes(instanceID, eventID string, outcomes []WebhookEventOutcome) {
	if eventID == "" || len(outcomes) == 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	bucket := l.events[instanceID]
	for i := len(bucket) - 1; i >= 0; i-- {
		if bucket[i].ID == eventID {
			bucket[i].Outcomes = outcomes
			l.events[instanceID] = bucket
			l.persistLocked()
			return
		}
	}
	// Event aged out or never logged — no-op.
}

// channels mean "client is too slow", drop the event for them.
// Held under subMu so subscribe/unsubscribe can't race with
// the broadcast.
func (l *webhookLog) fanOut(ev WebhookEvent) {
	l.subMu.Lock()
	defer l.subMu.Unlock()
	for _, ch := range l.subscribers[ev.InstanceID] {
		select {
		case ch <- ev:
			// Delivered.
		default:
			// Subscriber's buffer is full — skip. Browser can
			// recover by hitting GET /webhook/events to refresh
			// from the persisted ring.
		}
	}
}

// Subscribe registers a buffered channel for new events on the
// given instance. Returns the channel and an unsubscribe func the
// caller MUST invoke (typically via defer) when done — failing
// to unsubscribe leaks the channel into the subscribers map and
// causes future fan-outs to attempt sends on a goner.
//
// Buffer size 16 matches the worst-case Connect burst (Sonarr's
// season-pack import can fire ~10 EpisodeFile events back-to-back).
// Most browsers consume an SSE event in <1ms, so the buffer is
// generous insurance.
//
// Returns nil channel + no-op unsubscribe when the per-instance
// subscriber cap is hit — defence against a runaway browser /
// extension piling up subscriptions. SSE handler treats a nil
// channel as "stream rejected" and returns 503.
func (l *webhookLog) Subscribe(instanceID string) (<-chan WebhookEvent, func()) {
	l.subMu.Lock()
	if len(l.subscribers[instanceID]) >= webhookSubscriberCap {
		l.subMu.Unlock()
		return nil, func() {}
	}
	ch := make(chan WebhookEvent, 16)
	l.subscribers[instanceID] = append(l.subscribers[instanceID], ch)
	l.subMu.Unlock()
	unsubscribe := func() {
		l.subMu.Lock()
		defer l.subMu.Unlock()
		subs := l.subscribers[instanceID]
		for i, c := range subs {
			if c == ch {
				// Order doesn't matter — swap-and-truncate.
				subs[i] = subs[len(subs)-1]
				l.subscribers[instanceID] = subs[:len(subs)-1]
				close(ch)
				return
			}
		}
		// Already removed (e.g. double-unsubscribe). Idempotent.
	}
	return ch, unsubscribe
}

// list returns a deep-enough copy of the events for an instance,
// newest first. Each WebhookEvent's Raw slice is duplicated too so
// callers can't observe a half-mutated view if append() reuses the
// underlying byte-slice (today nothing does, but defence-in-depth
// against a future code path that wanted to mutate Raw in place).
func (l *webhookLog) list(instanceID string) []WebhookEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	bucket := l.events[instanceID]
	out := make([]WebhookEvent, len(bucket))
	// Reverse-copy so newest is first — matches what the UI wants.
	for i, ev := range bucket {
		// Copy the byte-slice rather than aliasing the live one.
		copied := ev
		if ev.Raw != nil {
			rawCopy := make(json.RawMessage, len(ev.Raw))
			copy(rawCopy, ev.Raw)
			copied.Raw = rawCopy
		}
		out[len(bucket)-1-i] = copied
	}
	return out
}

// clear wipes events for one instance. Used by the "Clear log" button
// on the Webhooks UI. Persists the truncation so a restart doesn't
// re-surface the cleared events.
func (l *webhookLog) clear(instanceID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.events[instanceID]; !ok {
		return
	}
	delete(l.events, instanceID)
	l.persistLocked()
}

// lastReceived returns the newest event's timestamp for the instance,
// or the zero time when no events exist yet. Cheap status pill on
// the Webhooks UI uses it for "Last: 2 min ago" / "Never received".
func (l *webhookLog) lastReceived(instanceID string) time.Time {
	l.mu.Lock()
	defer l.mu.Unlock()
	bucket := l.events[instanceID]
	if len(bucket) == 0 {
		return time.Time{}
	}
	return bucket[len(bucket)-1].ReceivedAt
}
