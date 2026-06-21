package api

// webhook_qbit_activity.go: the qBit-webhook activity view + re-run.
//
// qBit-add events (qBit's "run on torrent added" hook) are logged into the
// shared webhook log keyed by the qBit instance ID (a separate key
// namespace from Arr-Connect events, which are keyed by Arr instance ID)
// (both are distinct UUIDs, so they never collide in the same map). This
// gives Recent Activity a dedicated "qBit webhook" view per qBit instance
// showing what each add did, why, and any error, plus a re-run button on
// failures (mirrors the Connect-event replay).

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// qbitAddRaw is the persisted payload of a qBit-add activity entry,
// enough to re-classify + re-tag on replay.
type qbitAddRaw struct {
	InfoHash string `json:"infoHash"`
	Name     string `json:"name"`
	Category string `json:"category"`
}

// logQbitAddActivity appends one activity entry per torrent to the webhook
// log under the qBit instance ID. evs are the per-rule eager-apply results
// (empty when no rule matched, still logged so a delivered-but-ignored
// add is visible).
func (s *Server) logQbitAddActivity(qbitInstanceID, id string, receivedAt time.Time, infoHash, name, category string, evs []qbitAddEvent, isReplay bool) {
	if s.WebhookLog == nil {
		return
	}
	outcomes := make([]WebhookEventOutcome, 0, len(evs))
	for _, ev := range evs {
		o := WebhookEventOutcome{RuleID: ev.RuleID, RuleName: ev.RuleName, StartedAt: receivedAt}
		switch {
		case ev.ApplyErrMsg != "":
			o.Status = "error"
			o.Summary = ev.ApplyErrMsg
		case ev.Matched:
			o.Status = "ok"
			o.Changed = true
			o.Summary = "tagged " + ev.AppliedTag
			if ev.Reason != "" {
				o.Summary += " (" + ev.Reason + ")"
			}
		default:
			o.Status = "ok"
			if ev.Reason != "" {
				o.Summary = "no tag: " + ev.Reason
			} else {
				o.Summary = "no tag applied"
			}
		}
		outcomes = append(outcomes, o)
	}
	raw, _ := json.Marshal(qbitAddRaw{InfoHash: infoHash, Name: name, Category: category})
	subtitle := category
	if subtitle == "" {
		subtitle = "(no category)"
	}
	s.WebhookLog.append(WebhookEvent{
		ID:         id,
		InstanceID: qbitInstanceID,
		ReceivedAt: receivedAt,
		EventType:  "qBit add",
		Title:      name,
		Subtitle:   subtitle,
		Raw:        raw,
		Outcomes:   outcomes,
		Replay:     isReplay,
	})
}

// handleQbitWebhookEvents lists the qBit-add activity entries for one qBit
// instance (newest first). Mirrors the Connect events list endpoint.
func (s *Server) handleQbitWebhookEvents(w http.ResponseWriter, r *http.Request) {
	if s.WebhookLog == nil {
		writeError(w, 503, "webhook log not initialised")
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeError(w, 400, "id required")
		return
	}
	writeJSON(w, s.WebhookLog.list(id))
}

// handleQbitWebhookReplay re-runs a logged qBit-add: it re-classifies the
// stored torrent against the qBit instance's current S/E rules and re-
// applies the tag, then logs a fresh (Replay-marked) activity entry so the
// original failure stays visible. Re-run only makes sense on a failed
// entry, but the endpoint doesn't gate on status (the UI does); it just
// re-does the work.
func (s *Server) handleQbitWebhookReplay(w http.ResponseWriter, r *http.Request) {
	if s.WebhookLog == nil {
		writeError(w, 503, "webhook log not initialised")
		return
	}
	eventID := r.PathValue("eventId")
	ev, qbitInstanceID, ok := s.WebhookLog.findByID(eventID)
	if !ok {
		writeError(w, 404, "event not found (it may have aged out of the activity log)")
		return
	}
	if len(ev.Raw) == 0 || string(ev.Raw) == "null" {
		writeError(w, 400, "this entry has no saved payload to re-run")
		return
	}
	var payload qbitAddRaw
	if err := json.Unmarshal(ev.Raw, &payload); err != nil || payload.InfoHash == "" {
		writeError(w, 400, "saved payload could not be parsed")
		return
	}

	cfg := s.App.Config.Get()
	qbitInst := findQbitInstanceByID(cfg, qbitInstanceID)
	if qbitInst == nil {
		writeError(w, 400, "the qBit instance this event belonged to no longer exists")
		return
	}
	matchingRules := matchQbitSeRulesForInstance(cfg, qbitInstanceID)

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	movieCats, seriesCats := s.buildArrCategorySets(ctx, cfg)

	now := time.Now().UTC()
	perRuleEvents := make([]qbitAddEvent, 0, len(matchingRules))
	for i := range matchingRules {
		rule := matchingRules[i]
		e := qbitAddEvent{
			InfoHash: payload.InfoHash,
			Name:     payload.Name,
			Category: payload.Category,
			Received: now,
			RuleID:   rule.ID,
			RuleName: rule.Name,
		}
		s.eagerApplyQbitSeTag(ctx, qbitInst, &rule, &e, movieCats, seriesCats)
		perRuleEvents = append(perRuleEvents, e)
	}
	s.logQbitAddActivity(qbitInstanceID, genID(), now, payload.InfoHash, payload.Name, payload.Category, perRuleEvents, true)

	writeJSON(w, map[string]any{"ok": true, "rulesRun": len(perRuleEvents)})
}
