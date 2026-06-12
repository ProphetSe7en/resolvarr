package api

import (
	"fmt"
	"strings"

	"resolvarr/internal/core"
	"resolvarr/internal/core/agents"
)

// qbit_event_notify.go: outbound notification for the qBit "torrent
// added" hook (handleQbitTorrentAdded -> flushQbitAggregated).
//
// The qBit-add path eagerly applies Season/Episode tags but historically
// had no notification surface: a freshly added torrent has no prior tags,
// so the add-hook tags first and the Connect-event qbitSeTag always sees
// the tag already applied and stays silent. Result: S/E tagging never
// notified. This wires the missing surface.
//
// It reuses buildNotificationPayload (the exact Connect-event builder)
// by synthesising one qbitSeTag functionResult, so the embed (title,
// colour, fields, footer/timestamp) is identical to every other resolvarr
// notification rather than a bespoke layout.
//
// Gated on OnGrab (a qBit add is grab-adjacent tagging; reusing OnGrab
// matches what users already enable for S/E notifications). Dedup: this
// path's tag is always new (just-added torrent), so it is the notifying
// surface and the Connect qbitSeTag stays silent (already-tagged) →
// exactly one notification, with or without the add-hook.

// notifyQbitAddResult fires one notification per flushed qBit-add window
// when at least one tag was applied. Windows that matched no rule return
// before reaching here; failure-only windows are recorded in History but
// not notified (consistent with "only actual changes notify").
func (s *Server) notifyQbitAddResult(ruleID string, results []qbitTagResult, status string) {
	cfg := s.App.Config.Get()
	rule := findWebhookRuleByID(cfg, ruleID)
	if rule == nil || !rule.NotifyOnFire {
		return
	}
	event := core.WebhookEventGrab
	recipients := resolveNotificationAgents(rule, cfg.NotificationAgents, event)
	if len(recipients) == 0 {
		return
	}

	// Primary applied tag for this window + a representative torrent name.
	var primary *qbitTagResult
	for i := range results {
		if !results[i].failed && results[i].applied > 0 {
			primary = &results[i]
			break
		}
	}
	if primary == nil {
		return
	}
	itemTitle := "(torrent)"
	if len(primary.examples) > 0 {
		itemTitle = primary.examples[0]
		if extra := primary.applied - 1; extra > 0 {
			itemTitle = fmt.Sprintf("%s (+%d more)", itemTitle, extra)
		}
	}

	qbitName := ""
	if rule.QbitSe != nil {
		if qi := findQbitInstanceByID(cfg, rule.QbitSe.QbitInstanceID); qi != nil {
			qbitName = qi.Name
		}
	}

	syn := []functionResult{{
		Function: core.WebhookFnQbitSeTag,
		OK:       true,
		Changed:  true,
		Summary:  fmt.Sprintf("tagged %d with %q", primary.applied, primary.tag),
		Detail: QbitSeDetail{
			Tag:            primary.tag,
			Classification: qbitSeClassification(primary.tag),
			QbitInstance:   qbitName,
		},
	}}
	inst := findInstanceByID(cfg, rule.InstanceID)
	run := core.WebhookRuleRun{ItemTitle: itemTitle}

	// The heading is the function itself ("qBit Season tagged"), not the
	// item. The qBit-add path only has the torrent's scene name, never a
	// clean "Series Title (YYYY)", so putting it in the heading reads as
	// messy and implies an episode (not a torrent) was tagged. The scene
	// name goes in a Torrent field instead.
	title := fmt.Sprintf("qBit %s tagged", primary.tag)
	torrentNames := make([]string, 0, 5)
	for _, r := range results {
		if r.failed {
			continue
		}
		for _, ex := range r.examples {
			if len(torrentNames) < 5 {
				torrentNames = append(torrentNames, ex)
			}
		}
	}
	torrentField := agents.PayloadField{
		Name:   "Torrent",
		Value:  truncateField(strings.Join(torrentNames, "\n")),
		Inline: false,
	}

	for _, a := range recipients {
		// "{}" stands in for the absent Connect body; the qBit-add path
		// has no release payload, so filename/poster extraction yield
		// empty and those fields simply omit, matching a Connect grab.
		payload, fire := buildNotificationPayload(rule, inst, event, syn, []byte("{}"), run, a.Functions)
		if !fire {
			continue
		}
		payload.Title = title
		payload.Fields = append(payload.Fields, torrentField)
		s.App.DispatchNotificationAgent(a, payload)
	}
}

// qbitSeClassification maps a qBit S/E tag to the human-readable
// classification shown in the embed (mirrors the Connect adapter).
func qbitSeClassification(tag string) string {
	switch strings.ToLower(tag) {
	case "season":
		return "Season pack"
	case "episode":
		return "Episode"
	}
	return tag
}
