package api

// webhook_replay.go: manual re-run of a logged Connect event.
//
// When an event in Recent Activity failed (a rule errored, e.g. qBit
// unreachable), the operator can fix the cause and re-run the exact same
// event without waiting for Sonarr/Radarr to send another. The original
// payload is retained on the logged event (WebhookEvent.Raw), so a replay
// re-parses it and feeds it back through the same dispatcher the live
// receiver uses.
//
// Two endpoints:
//   - GET  .../replay-preview: computes which rules + functions WOULD
//     fire, with no execution, so the UI can show a clear "this is what
//     it will do" before the user commits.
//   - POST .../replay: appends a fresh log entry (so the original
//     failure stays visible) and re-dispatches against it.
//
// Replay uses the instance-level dispatch path (dispatchWebhookRules), so
// rules with their own per-rule webhook URL are excluded, both the
// preview and the run skip them identically, keeping the preview honest.

import (
	"encoding/json"
	"net/http"
	"time"

	"resolvarr/internal/core"
)

// replayRulePreview is one rule that would fire on replay, with the
// function keys it would run (frontend maps keys to labels).
type replayRulePreview struct {
	RuleID    string   `json:"ruleId"`
	RuleName  string   `json:"ruleName"`
	Functions []string `json:"functions"`
}

type replayPreviewResponse struct {
	Replayable   bool                `json:"replayable"`
	Reason       string              `json:"reason,omitempty"` // why not replayable
	InstanceID   string              `json:"instanceId,omitempty"`
	InstanceName string              `json:"instanceName,omitempty"`
	EventType    string              `json:"eventType,omitempty"`
	Title        string              `json:"title,omitempty"`
	Rules        []replayRulePreview `json:"rules"` // empty = nothing would fire
}

// resolveReplay loads the event + its instance + parses the envelope,
// returning the shared validation used by both endpoints. The *apiError
// is nil on success; otherwise it carries the user-facing reason.
func (s *Server) resolveReplay(id string) (ev WebhookEvent, inst *core.Instance, env *connectEventEnvelope, cfg core.Config, apiErr *apiError) {
	if s.WebhookLog == nil {
		return ev, nil, nil, cfg, newAPIError(503, "webhook log not initialised")
	}
	ev, instID, ok := s.WebhookLog.findByID(id)
	if !ok {
		return ev, nil, nil, cfg, newAPIError(404, "event not found (it may have aged out of the activity log)")
	}
	// Synthetic log entries (auth-rejected / unsigned notices) carry a
	// null payload and nothing to re-run.
	if len(ev.Raw) == 0 || string(ev.Raw) == "null" {
		return ev, nil, nil, cfg, newAPIError(400, "this entry has no saved payload to re-run")
	}
	cfg = s.App.Config.Get()
	for i := range cfg.Instances {
		if cfg.Instances[i].ID == instID {
			inst = &cfg.Instances[i]
			break
		}
	}
	if inst == nil {
		return ev, nil, nil, cfg, newAPIError(400, "the instance this event belonged to no longer exists")
	}
	var parsed connectEventEnvelope
	if err := json.Unmarshal(ev.Raw, &parsed); err != nil {
		return ev, nil, nil, cfg, newAPIError(400, "saved payload could not be parsed: "+err.Error())
	}
	if parsed.EventType == "" {
		return ev, nil, nil, cfg, newAPIError(400, "saved payload has no event type to match rules against")
	}
	return ev, inst, &parsed, cfg, nil
}

// matchingReplayRules computes, without executing, which enabled rules on
// the instance would fire for the event and which of their functions
// apply. Mirrors executeWebhookRule's qualify + per-function gating, and
// excludes per-rule-URL rules exactly as dispatchWebhookRules does, so the
// preview matches what the replay will actually run.
func matchingReplayRules(cfg core.Config, inst *core.Instance, event core.WebhookConnectEvent) []replayRulePreview {
	out := []replayRulePreview{}
	for i := range cfg.WebhookRules {
		rule := cfg.WebhookRules[i]
		if rule.InstanceID != inst.ID || !rule.Enabled || rule.HasOwnWebhookURL() {
			continue
		}
		if !rule.FiresOn(event) && !rule.FiresAutoStripOnDelete(event) && !rule.FiresPerBucketStripOnDelete(event) {
			continue
		}
		var fns []string
		for _, fn := range canonicalFunctionOrder {
			if !rule.HasFunction(fn) {
				continue
			}
			for _, e := range core.EventsForFunction(fn, rule.AppType) {
				if e == event {
					fns = append(fns, string(fn))
					break
				}
			}
		}
		out = append(out, replayRulePreview{RuleID: rule.ID, RuleName: rule.Name, Functions: fns})
	}
	return out
}

// handleWebhookReplayPreview (GET) describes what a replay would do.
func (s *Server) handleWebhookReplayPreview(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ev, inst, env, cfg, apiErr := s.resolveReplay(id)
	if apiErr != nil {
		// A non-replayable event is a normal answer, not an error: return
		// 200 with replayable=false so the UI can explain it inline.
		if apiErr.Status == 400 || apiErr.Status == 404 {
			writeJSON(w, replayPreviewResponse{Replayable: false, Reason: apiErr.Message, Rules: []replayRulePreview{}})
			return
		}
		writeError(w, apiErr.Status, apiErr.Message)
		return
	}
	event := core.WebhookConnectEvent(env.EventType)
	writeJSON(w, replayPreviewResponse{
		Replayable:   true,
		InstanceID:   inst.ID,
		InstanceName: inst.Name,
		EventType:    env.EventType,
		Title:        ev.Title,
		Rules:        matchingReplayRules(cfg, inst, event),
	})
}

// handleWebhookReplay (POST) appends a fresh log entry copying the
// original payload + metadata, then re-dispatches against it. The new
// entry (marked Replay) keeps the original failure visible alongside the
// re-run result. Returns the number of rules that fired.
func (s *Server) handleWebhookReplay(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ev, inst, env, _, apiErr := s.resolveReplay(id)
	if apiErr != nil {
		writeError(w, apiErr.Status, apiErr.Message)
		return
	}

	newID := genID()
	s.WebhookLog.append(WebhookEvent{
		ID:         newID,
		InstanceID: inst.ID,
		ReceivedAt: time.Now().UTC(),
		EventType:  ev.EventType,
		Title:      ev.Title,
		Subtitle:   ev.Subtitle,
		Raw:        ev.Raw,
		Replay:     true,
	})

	fired := s.dispatchWebhookRules(r.Context(), inst, env, ev.Raw, newID)
	writeJSON(w, map[string]any{"ok": true, "eventId": newID, "rulesFired": fired})
}
