package api

// webhook_per_rule.go — receive endpoint for the per-rule webhook URL
// added in M-per-rule-webhook. Routes POST /api/webhooks/rule/{token}
// directly to the rule whose Webhook.Token matches — no dispatch over
// sibling rules.
//
// Auth: same Basic-auth-with-shared-secret model as the instance
// webhook (validateWebhookAuth + rule-level RequireSignature). Reuses
// the same body cap + parse + Recent-activity ring-buffer integration.
//
// The dispatcher delegate is dispatchSingleWebhookRule (extracted
// from dispatchWebhookRules in the same slice) so the execution
// pipeline stays identical to the instance-URL path — canonical
// function order + auto-strip-on-delete + per-bucket-strip-on-delete
// all run the same way regardless of which URL the event arrived at.

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"resolvarr/internal/core"
)

// handleWebhookReceivePerRule is the per-rule webhook receiver.
//
//	POST /api/webhooks/rule/{ruleToken}
//	Headers: Authorization: Basic <user:secret> when RequireSignature on
//	Body:    Sonarr/Radarr Connect JSON
//
// Responses:
//   - 200 + summary on success (Connect retries on 5xx, abandons after 4xx)
//   - 401 on auth failure (Basic-auth missing or secret mismatch)
//   - 404 on unknown token (leaks less than 403 — a probe can't tell
//     "rule deleted" from "wrong URL")
//   - 503 when WebhookLog isn't initialised yet (early-boot race)
//
// Rule-deleted-mid-flight handling: lookup happens at receive time,
// so a rule deleted between Sonarr's POST and our handler's runtime
// returns 404 — Sonarr/Radarr stops retrying eventually. No history
// entry is created for events that don't route.
//
// Recent activity logging stays scoped to the rule's instance ID so
// events still show up under the same "instance" filter on the
// Recent activity sub-tab.
func (s *Server) handleWebhookReceivePerRule(w http.ResponseWriter, r *http.Request) {
	if s.WebhookLog == nil {
		writeError(w, 503, "webhook log not initialised")
		return
	}
	token := r.PathValue("ruleToken")
	if token == "" {
		writeError(w, 404, "not found")
		return
	}
	cfg := s.App.Config.Get()
	rule, _ := core.FindRuleByWebhookToken(cfg.WebhookRules, token)
	if rule == nil {
		writeError(w, 404, "not found")
		return
	}
	// Resolve the instance the rule belongs to — needed for ring-
	// buffer scoping + executeWebhookRule's tag-details fetch path.
	// Rule-without-instance is a stale config state; treat as 404
	// to keep Connect from retrying forever.
	var inst *core.Instance
	for i := range cfg.Instances {
		if cfg.Instances[i].ID == rule.InstanceID {
			inst = &cfg.Instances[i]
			break
		}
	}
	if inst == nil {
		writeError(w, 404, "not found")
		return
	}

	// Rule-level auth — same Basic-auth-with-shared-secret semantics
	// as the instance receiver. Rule's own Secret + RequireSignature.
	// Defensive: rule.Webhook should be non-nil here (FindRuleBy
	// WebhookToken only returns rules with Webhook.Token set), but
	// double-check so a future code path that skips that invariant
	// doesn't NPE.
	if rule.Webhook == nil {
		writeError(w, 500, "rule webhook config missing")
		return
	}
	authOK, authReason := validateWebhookAuth(r, rule.Webhook.Secret, rule.Webhook.RequireSignature)
	if !authOK {
		// Rate-limited ring-buffer logging — same pattern as the
		// instance receiver. Key on (instance, rule, reason) so
		// rejections on rule A don't suppress rejections on rule B.
		if s.WebhookLog != nil && s.shouldLogAuthEvent(inst.ID, "rule-auth-rejected:"+rule.ID+":"+authReason) {
			s.WebhookLog.append(WebhookEvent{
				ID:         genID(),
				InstanceID: inst.ID,
				ReceivedAt: time.Now().UTC(),
				EventType:  "(rejected)",
				Title:      "Rule \"" + rule.Name + "\" authentication rejected: " + authReason,
				Raw:        json.RawMessage(`null`),
			})
		}
		writeError(w, 401, "authentication failed: "+authReason)
		return
	}
	// Soft-warn on unsigned events when RequireSignature is off (same
	// "you should turn on Require signature" nudge as the instance
	// path).
	if !rule.Webhook.RequireSignature && r.Header.Get("Authorization") == "" && s.WebhookLog != nil && s.shouldLogAuthEvent(inst.ID, "rule-auth-unsigned:"+rule.ID) {
		s.WebhookLog.append(WebhookEvent{
			ID:         genID(),
			InstanceID: inst.ID,
			ReceivedAt: time.Now().UTC(),
			EventType:  "(unsigned)",
			Title:      "Rule \"" + rule.Name + "\" received without Authorization header — paste the Secret into Sonarr/Radarr's Webhook password field and turn on 'Require signature'",
			Raw:        json.RawMessage(`null`),
		})
	}

	// Body cap + read — identical bounds as the instance receiver.
	if r.ContentLength > webhookBodyMaxBytes {
		writeError(w, 413, "body too large")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, webhookBodyMaxBytes))
	if err != nil {
		writeError(w, 400, "read body: "+err.Error())
		return
	}

	// Decode the envelope for ring-buffer summary + dispatcher routing.
	// Unparseable JSON still logs (same defensive behaviour as instance
	// receiver) so the user can see malformed test events in Recent
	// activity.
	var env connectEventEnvelope
	rawForStorage := json.RawMessage(body)
	if jerr := json.Unmarshal(body, &env); jerr != nil {
		env.EventType = "(unparseable)"
		wrapped, werr := json.Marshal(map[string]string{"_unparseable": string(body)})
		if werr == nil {
			rawForStorage = json.RawMessage(wrapped)
		} else {
			rawForStorage = json.RawMessage(`{"_unparseable":"<encode failed>"}`)
		}
	}
	if env.EventType == "" {
		env.EventType = "(unknown)"
	}
	title, subtitle := summariseEvent(&env)

	// Logging — gated on the INSTANCE-level LoggingEnabled toggle.
	// Per-rule URLs share the instance's Recent-activity ring so
	// users still get one log per instance regardless of which URL
	// the event arrived at. A future enhancement could give rules
	// their own LoggingEnabled, but for v1 the instance toggle is
	// authoritative.
	logged := false
	logCount := 0
	if inst.Webhook.LoggingEnabled {
		ev := WebhookEvent{
			ID:         genID(),
			InstanceID: inst.ID,
			ReceivedAt: time.Now().UTC(),
			EventType:  env.EventType,
			Title:      title,
			Subtitle:   subtitle,
			Raw:        rawForStorage,
		}
		logCount = s.WebhookLog.append(ev)
		logged = true
	}

	rulesFired := s.dispatchSingleWebhookRule(r.Context(), inst, rule, &env, body)

	writeJSON(w, map[string]any{
		"status":     "ok",
		"logged":     logged,
		"eventType":  env.EventType,
		"count":      logCount,
		"rulesFired": rulesFired,
		"ruleId":     rule.ID,
	})
}
