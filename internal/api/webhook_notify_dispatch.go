// webhook_notify_dispatch.go — wires the M-Webhook notification
// framework into the dispatcher hot path. Called by dispatchWebhookRules
// after buildWebhookRuleRun completes for each fired rule.
//
// Gating model (power-to-the-agent, option A):
//
//   1. rule.NotifyOnFire = false → no notifications fire from this
//      rule, regardless of any agent's config. Per-rule master
//      kill-switch.
//
//   2. Each enabled agent whose `Events.OnX` flag matches the event
//      class (OnImport for Download, OnGrab for Grab, OnFileDelete
//      for Movie/EpisodeFileDelete*) is eligible.
//
//   3. For each eligible agent, the dispatcher builds a tailored
//      payload via `composeFields(..., agent.Functions)` — function-
//      level whitelist filters which sections render in THAT agent's
//      embed. The same fire produces different embeds for different
//      agents.
//
//   4. Empty title after filtering (= agent's whitelist eliminated
//      every changed function) → silent skip for that agent. User-
//      locked "only actual changes" rule honoured per-agent.
//
// The hook is split into three pure helpers + one Server method so
// each piece is independently testable:
//
//   - buildNotificationPayload — assembles agents.Payload from rule
//     + run + results + body + the agent's Functions whitelist.
//     Returns (zero, false) when composeTitle short-circuits to empty.
//
//   - resolveNotificationAgents — picks eligible agents per the
//     NotifyOnFire kill-switch + per-agent Events.OnX gate. Does NOT
//     consult Functions (that filters payload contents, not agent
//     eligibility).
//
//   - agentSubscribesToEvent — small predicate mapping
//     WebhookConnectEvent to the matching agents.Events.OnX flag.
//
//   - (s *Server).fireWebhookNotifications — the hook itself.
//     Stitches the helpers together + dispatches async via
//     app.DispatchNotificationAgent.

package api

import (
	"time"

	"resolvarr/internal/core"
	"resolvarr/internal/core/agents"
)

// buildNotificationPayload assembles the agents.Payload for one
// fired rule. Pure function; no I/O. Returns (zero, false) when
// the rule produced nothing to notify about — the dispatcher
// treats false as "skip".
//
// The skip path implements the rule "notifications must contain
// only actual changes". When every result has Changed=false
// (or every changed result is filtered out by the agent's Functions
// whitelist), composeTitle returns "" and we short-circuit with
// fire=false — caller skips dispatch for THIS agent silently.
//
// `allowedFunctions` is the per-agent function whitelist (from
// agents.Agent.Functions). Empty/nil = no filter; non-empty =
// composeTitle / pickColor / composeFields all see only results
// whose Function is in the list. Each agent gets its own
// buildNotificationPayload call with its own filter so the embed
// is tailored to what THAT agent subscribed to.
func buildNotificationPayload(
	rule *core.WebhookRule,
	inst *core.Instance,
	event core.WebhookConnectEvent,
	results []functionResult,
	body []byte,
	run core.WebhookRuleRun,
	allowedFunctions []string,
) (agents.Payload, bool) {
	title := composeTitle(event, results, run.ItemTitle, run.ItemContext, allowedFunctions)
	if title == "" {
		return agents.Payload{}, false
	}

	instType := ""
	instName := ""
	if inst != nil {
		instType = inst.Type
		instName = inst.Name
	}
	ruleName := ""
	if rule != nil {
		ruleName = rule.Name
	}
	_, filename := extractReleaseAndFilePath(body)

	payload := agents.Payload{
		Title:        title,
		Color:        pickColor(event, results, allowedFunctions),
		Fields:       composeFields(event, results, allowedFunctions, instName, ruleName, filename),
		ThumbnailURL: extractPosterURL(body, instType),
		Timestamp:    time.Now().UTC(),
		Severity:     agents.SeverityInfo,
		Route:        agents.RouteDefault,
	}
	return payload, true
}

// resolveNotificationAgents picks the agents that should receive a
// rule's notification. Power-to-the-agent model:
//
//  1. `rule.NotifyOnFire == false` → return nil (master kill-switch).
//  2. Else iterate enabled agents whose Events.OnX flag matches the
//     event class. Each agent's `Functions` whitelist then narrows
//     which embed sections render (handled later in
//     buildNotificationPayload, not here — this function is purely
//     about "which agents are eligible to see this event-class").
//
// Returns a nil slice when nothing should fire; the caller can
// short-circuit on `len(agents) == 0`.
//
// (The old per-rule `NotifyAgents` whitelist + `NotifyOnEveryEvent`
// debug flag were retired in commit 7.4d. Agents own their filter
// via Events + Functions config; rules just have an on/off toggle.)
func resolveNotificationAgents(
	rule *core.WebhookRule,
	allAgents []core.NotificationAgent,
	event core.WebhookConnectEvent,
) []core.NotificationAgent {
	if rule == nil || !rule.NotifyOnFire {
		return nil
	}
	var out []core.NotificationAgent
	for _, a := range allAgents {
		if !a.Enabled {
			continue
		}
		if !agentSubscribesToEvent(a, event) {
			continue
		}
		out = append(out, a)
	}
	return out
}

// agentSubscribesToEvent maps a WebhookConnectEvent to the matching
// agents.Events flag. Returns false for event types no agent flag
// covers (e.g. ApplicationUpdate, Test) — those never produce
// notifications via the webhook path.
func agentSubscribesToEvent(a core.NotificationAgent, event core.WebhookConnectEvent) bool {
	switch event {
	case core.WebhookEventGrab:
		return a.Events.OnGrab
	case core.WebhookEventDownload:
		// Download covers Import + Upgrade; both map to OnImport.
		// Sonarr/Radarr emit `isUpgrade` inside the body for callers
		// that want to discriminate, but the agent-level event flag
		// stays unified.
		return a.Events.OnImport
	case core.WebhookEventMovieFileDelete,
		core.WebhookEventMovieFileDeleteForUpgrade,
		core.WebhookEventEpisodeFileDelete,
		core.WebhookEventEpisodeFileDeleteForUpgrade:
		return a.Events.OnFileDelete
	}
	return false
}

// fireWebhookNotifications is the dispatcher hook. Called by
// dispatchWebhookRules / dispatchSingleWebhookRule after each rule's
// buildWebhookRuleRun completes. Resolves agents, assembles payload,
// and dispatches asynchronously so Discord/Gotify/NTFY latency
// doesn't slow the receiver's response to Sonarr/Radarr Connect
// (the underlying agents.Provider's Async() flag — true for Discord
// — drives the SafeGo wrap inside DispatchNotificationAgent).
//
// Pulls the agent list from the live ConfigStore.Get snapshot so
// admin-side adds/removes are picked up immediately. Doesn't hold
// the dispatcher's pendingRuleRun batching lock.
func (s *Server) fireWebhookNotifications(
	rule *core.WebhookRule,
	inst *core.Instance,
	env *connectEventEnvelope,
	body []byte,
	results []functionResult,
	run core.WebhookRuleRun,
) {
	if rule == nil || !rule.NotifyOnFire || env == nil {
		return
	}
	event := core.WebhookConnectEvent(env.EventType)
	cfg := s.App.Config.Get()
	recipients := resolveNotificationAgents(rule, cfg.NotificationAgents, event)
	if len(recipients) == 0 {
		return
	}
	for _, a := range recipients {
		// Per-agent payload build. Each agent's Functions whitelist
		// filters which results contribute to title/color/fields, so
		// "Discord Tag-only" and "Discord Cleanup-only" agents get
		// different embeds from the same fire. Empty Functions
		// (= "all") preserves backward-compat behaviour.
		payload, fire := buildNotificationPayload(rule, inst, event, results, body, run, a.Functions)
		if !fire {
			// composeTitle returned empty AND debug-mode off: this
			// agent's filter eliminated all changed sections from
			// this fire. Honours the "only actual changes" rule at
			// per-agent granularity — silent skip is correct.
			continue
		}
		s.App.DispatchNotificationAgent(a, payload)
	}
}
