package api

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"resolvarr/internal/core"
	"resolvarr/internal/core/agents"
)

// scheduler_notify.go — outbound notifications for scheduled runs.
//
// Live runs (Tag library Run scan, Quick fix-all, Recover) deliberately
// do NOT notify — the user has the UI open and reads the result inline.
// Scheduled runs fire in the background, so a Discord/Gotify/NTFY ping
// is the only way the user knows they happened.
//
// Embed format mirrors bash tagarr.sh:
//   - Title: "Resolvarr — <schedule>"
//   - Color: orange (16753920) for ok, red for error, gray for partial
//   - Fields-grid: Primary inline + Secondary inline (when sync) +
//     Runtime full-width
//   - Footer: "Resolvarr v<version> by ProphetSe7en" (handled by provider)
//
// Discord renders the fields as side-by-side columns; non-rich providers
// (Gotify, NTFY, Pushover, Apprise) flatten Fields into a plain-text
// Message via buildPlainMessage so something useful lands there too.
//
// Per-agent event flags (agent.Events.OnScheduleSuccess / OnScheduleFailure)
// gate which agents receive each fire — so the user can route errors to
// Pushover and routine summaries to Discord, for example.

// notifyScheduleResult builds a NotificationPayload from the run outcome
// and dispatches it to every enabled agent whose Events flag matches.
// Best-effort: each agent's Notify failure is logged inside the agents
// package; this function never blocks the run.
func (r *schedulerRunner) notifyScheduleResult(inst *core.Instance, job core.ScheduledJob, summary core.RunSummary, runErr error, duration time.Duration) {
	cfg := r.server.App.Config.Get()
	if len(cfg.NotificationAgents) == 0 {
		return
	}

	severity := core.NotificationSeverityInfo
	if runErr != nil || summary.Status == "error" {
		severity = core.NotificationSeverityCritical
	} else if summary.Status == "partial" {
		severity = core.NotificationSeverityWarning
	}

	title := fmt.Sprintf("Resolvarr — %s", job.Name)
	fields := buildScheduleFields(inst, job, summary, runErr, duration)
	plainMessage := buildPlainScheduleMessage(fields)
	detail := buildScheduleDetail(job, summary, runErr)

	payload := core.NotificationPayload{
		Title:    title,
		Message:  plainMessage,
		Color:    scheduleEmbedColor(inst, severity),
		Severity: severity,
		Route:    core.NotificationRouteDefault,
		Fields:   fields,
		Detail:   detail,
	}

	for _, agent := range cfg.NotificationAgents {
		if !agent.Enabled {
			continue
		}
		if !scheduleEventMatches(agent, runErr) {
			continue
		}
		r.server.App.DispatchNotificationAgent(agent, payload)
	}
}

// scheduleEventMatches returns true when the agent has opted into the
// event class this run represents. Success runs match agents with
// OnScheduleSuccess; errored runs match OnScheduleFailure.
func scheduleEventMatches(agent core.NotificationAgent, runErr error) bool {
	if runErr != nil {
		return agent.Events.OnScheduleFailure
	}
	return agent.Events.OnScheduleSuccess
}

// scheduleEmbedColor maps severity to the Discord-style accent color.
// Orange for routine success matches the bash tagarr.sh palette so users
// migrating from scripts see familiar coloring.
func scheduleEmbedColor(inst *core.Instance, severity core.NotificationSeverity) int {
	if severity == core.NotificationSeverityCritical {
		return 15548997 // red: failed run stays red regardless of app
	}
	t := ""
	if inst != nil {
		t = inst.Type
	}
	return appColor(t) // Sonarr blue / Radarr gold
}

// buildScheduleFields constructs the embed-fields slice in bash tagarr.sh's
// shape: Primary + Secondary as inline columns, Runtime as a full-width
// row. Error runs replace the per-mode fields with a single Error block.
func buildScheduleFields(inst *core.Instance, job core.ScheduledJob, summary core.RunSummary, runErr error, duration time.Duration) []agents.PayloadField {
	if runErr != nil {
		return []agents.PayloadField{
			{
				Name:   "Error",
				Value:  truncateField(runErr.Error()),
				Inline: false,
			},
			{
				Name:   "Runtime",
				Value:  formatRuntimeValue(job, duration),
				Inline: false,
			},
		}
	}

	var fields []agents.PayloadField

	switch job.Mode {
	case core.JobModeTag:
		fields = append(fields, tagModeFields(inst, summary)...)
	case core.JobModeDiscover:
		fields = append(fields, discoverModeField(summary))
	case core.JobModeRecover:
		fields = append(fields, recoverModeField(summary))
	case core.JobModeAudioTags:
		fields = append(fields, autoTagsModeFields(inst, summary, "Audio tags")...)
	case core.JobModeVideoTags:
		fields = append(fields, autoTagsModeFields(inst, summary, "Video tags")...)
	case core.JobModeDvDetail:
		fields = append(fields, dvDetailModeFields(inst, summary)...)
	case core.JobModePlexSync:
		fields = append(fields, plexSyncModeField(summary)...)
	case core.JobModeCombined:
		// Combined schedules can include any phase. Surface a field
		// block per phase that produced a response. Order matches the
		// chain run order so the embed reads top-to-bottom the same
		// way the user configured + watched it run.
		if cr, ok := summary.Result.(combinedScheduleResult); ok {
			if cr.Discover != nil {
				fields = append(fields, discoverModeField(core.RunSummary{Result: cr.Discover}))
			}
			if cr.Recover != nil {
				fields = append(fields, recoverModeField(core.RunSummary{Result: cr.Recover}))
			}
			if cr.Tag != nil {
				fields = append(fields, tagModeFields(inst, summary)...)
			}
			if cr.AudioTags != nil {
				fields = append(fields, autoTagsModeFields(inst, core.RunSummary{Result: cr.AudioTags}, "Audio tags")...)
			}
			if cr.VideoTags != nil {
				fields = append(fields, autoTagsModeFields(inst, core.RunSummary{Result: cr.VideoTags}, "Video tags")...)
			}
			if cr.DvDetail != nil {
				fields = append(fields, dvDetailModeFields(inst, core.RunSummary{Result: cr.DvDetail})...)
			}
			if cr.PlexSync != nil {
				fields = append(fields, plexSyncModeField(core.RunSummary{Result: cr.PlexSync})...)
			}
		} else {
			fields = append(fields, tagModeFields(inst, summary)...)
		}
	}

	fields = append(fields, agents.PayloadField{
		Name:   "Runtime",
		Value:  formatRuntimeValue(job, duration),
		Inline: false,
	})
	return fields
}

// tagModeFields builds the Primary + (when sync was on) Secondary count
// columns. Bash uses "Tagged: N\nUntagged: N" — the after-run count of
// items WITH and WITHOUT the tag. We compute the same from totals:
//   Tagged   = ToKeep + Added
//   Untagged = TotalItems - Tagged - NoFile
// Apply runs use Applied.ItemsAdded; preview runs use Totals.ToAdd.
func tagModeFields(inst *core.Instance, summary core.RunSummary) []agents.PayloadField {
	var resp *scanResponse
	switch v := summary.Result.(type) {
	case *scanResponse:
		resp = v
	case combinedScheduleResult:
		resp = v.Tag
	}
	if resp == nil {
		// No structured result — emit the summary string as a single
		// field so something lands in the embed.
		return []agents.PayloadField{{
			Name:   inst.Name,
			Value:  truncateField(summary.Summary),
			Inline: false,
		}}
	}

	out := []agents.PayloadField{
		{
			Name:   fmt.Sprintf("Primary (%s)", inst.Name),
			Value:  formatTaggedUntagged(resp, false),
			Inline: true,
		},
	}
	hasSecondary := resp.Totals.SecondaryToAdd > 0 || resp.Totals.SecondaryToRemove > 0 || resp.Totals.SecondaryToKeep > 0 || resp.Totals.SecondaryMissing > 0
	if hasSecondary {
		secName := "secondary"
		if resp.Applied != nil && resp.Applied.Secondary != nil && resp.Applied.Secondary.InstanceName != "" {
			secName = resp.Applied.Secondary.InstanceName
		}
		out = append(out, agents.PayloadField{
			Name:   fmt.Sprintf("Secondary (%s)", secName),
			Value:  formatTaggedUntagged(resp, true),
			Inline: true,
		})
	}

	// Tag-cleanup info when applicable.
	if resp.Applied != nil && len(resp.Applied.TagsCreated) > 0 {
		out = append(out, agents.PayloadField{
			Name:   "New tags",
			Value:  "`" + strings.Join(resp.Applied.TagsCreated, "`, `") + "`",
			Inline: false,
		})
	}
	if resp.Applied != nil && len(resp.Applied.TagsDeleted) > 0 {
		out = append(out, agents.PayloadField{
			Name:   "Cleaned up",
			Value:  "`" + strings.Join(resp.Applied.TagsDeleted, "`, `") + "`",
			Inline: false,
		})
	}

	// (Combined-mode discover/recover/extratags fields are emitted by
	// buildScheduleFields' dispatcher loop — used to tail-append a
	// Discover field here for combined results, but that double-
	// emitted the field once the dispatcher learned to surface every
	// phase explicitly.)
	return out
}

// formatTaggedUntagged builds the "Tagged: N / Untagged: N" line bash
// emits. Bash semantics (matched here):
//
//   Tagged   = tag-additions in this run (newly applied)
//   Untagged = tag-removals in this run (movies that no longer match
//              the filters and have the tag pulled back off)
//
// In apply mode we read what actually happened (Applied.ItemsAdded /
// ItemsRemoved); in preview we read what would happen (Totals.ToAdd /
// ToRemove). secondary=true reads the secondary-side counters.
func formatTaggedUntagged(resp *scanResponse, secondary bool) string {
	if secondary {
		added := resp.Totals.SecondaryToAdd
		removed := resp.Totals.SecondaryToRemove
		if resp.Applied != nil && resp.Applied.Secondary != nil {
			added = resp.Applied.Secondary.ItemsAdded
			removed = resp.Applied.Secondary.ItemsRemoved
		}
		return fmt.Sprintf("Tagged: %d\nUntagged: %d", added, removed)
	}
	added := resp.Totals.ToAdd
	removed := resp.Totals.ToRemove
	if resp.Applied != nil {
		added = resp.Applied.ItemsAdded
		removed = resp.Applied.ItemsRemoved
	}
	return fmt.Sprintf("Tagged: %d\nUntagged: %d", added, removed)
}

// discoverModeField builds the single field for a discover-only schedule.
func discoverModeField(summary core.RunSummary) agents.PayloadField {
	resp, _ := summary.Result.(*scanResponse)
	count := 0
	if resp != nil {
		count = resp.Totals.Discovered
	}
	return agents.PayloadField{
		Name:   "Discover",
		Value:  fmt.Sprintf("%d new release-group candidate%s found", count, plural(count)),
		Inline: false,
	}
}

// recoverModeField builds the field for a recover-only schedule.
func recoverModeField(summary core.RunSummary) agents.PayloadField {
	resp, _ := summary.Result.(*scanResponse)
	if resp == nil {
		return agents.PayloadField{
			Name:   "Recover",
			Value:  truncateField(summary.Summary),
			Inline: false,
		}
	}
	t := resp.Totals
	return agents.PayloadField{
		Name:   "Recover",
		Value:  fmt.Sprintf("Would-fix: %d\nFixed: %d\nFailed: %d", t.RecoverWouldFix, t.RecoverFixed, t.RecoverFixFailed),
		Inline: false,
	}
}

// autoTagsModeFields builds the field block for an audiotags or
// videotags scan. label is the human-readable section name
// ("Audio tags" / "Video tags"). Apply-mode reports actual writes;
// preview reports would-do counts. Mediainfo-missing flagged
// separately so users notice legacy imports needing a Radarr
// re-probe.
func autoTagsModeFields(inst *core.Instance, summary core.RunSummary, label string) []agents.PayloadField {
	resp, _ := summary.Result.(*scanResponse)
	if resp == nil {
		return []agents.PayloadField{{
			Name:   inst.Name + " — " + label,
			Value:  truncateField(summary.Summary),
			Inline: false,
		}}
	}

	added := resp.Totals.ToAdd
	removed := resp.Totals.ToRemove
	kept := resp.Totals.ToKeep
	applyMode := resp.Applied != nil
	if applyMode {
		added = resp.Applied.ItemsAdded
		removed = resp.Applied.ItemsRemoved
	}
	addLabel, removeLabel, keepLabel := "To add", "To remove", "To keep"
	if applyMode {
		addLabel, removeLabel, keepLabel = "Added", "Removed", "Kept"
	}
	value := fmt.Sprintf("%s: %d\n%s: %d\n%s: %d", addLabel, added, removeLabel, removed, keepLabel, kept)
	if resp.Totals.MissingMediaInfo > 0 {
		value += fmt.Sprintf("\nMissing media-info: %d", resp.Totals.MissingMediaInfo)
	}
	out := []agents.PayloadField{{
		Name:   label + " (" + inst.Name + ")",
		Value:  value,
		Inline: true,
	}}
	if resp.Applied != nil && len(resp.Applied.TagsCreated) > 0 {
		out = append(out, agents.PayloadField{
			Name:   "New " + strings.ToLower(label),
			Value:  "`" + strings.Join(resp.Applied.TagsCreated, "`, `") + "`",
			Inline: false,
		})
	}
	return out
}

// dvDetailModeFields builds the embed field block for a dvdetail run.
// Two-field layout: a "DV detail (instance)" inline block with the
// add/remove/keep counts (verb-tense per mode), and — when any
// extraction outcomes are non-zero — a separate "DV extraction"
// inline block surfacing the cache-hit + extracted + no-rpu + failed
// + unreachable counters. Splitting the extraction stats out keeps
// the primary count column readable and makes failure modes obvious.
//
// Tag-creation footer mirrors extraTagsModeFields so the user sees
// any newly-created Radarr tags from this Apply (e.g. first-time
// emit of "fel" or "cm4" creates the tag in Radarr).
func dvDetailModeFields(inst *core.Instance, summary core.RunSummary) []agents.PayloadField {
	resp, _ := summary.Result.(*scanResponse)
	if resp == nil {
		return []agents.PayloadField{{
			Name:   inst.Name + " — DV detail",
			Value:  truncateField(summary.Summary),
			Inline: false,
		}}
	}

	added := resp.Totals.ToAdd
	removed := resp.Totals.ToRemove
	kept := resp.Totals.ToKeep
	applyMode := resp.Applied != nil
	if applyMode {
		added = resp.Applied.ItemsAdded
		removed = resp.Applied.ItemsRemoved
	}
	addLabel, removeLabel, keepLabel := "To add", "To remove", "To keep"
	if applyMode {
		addLabel, removeLabel, keepLabel = "Added", "Removed", "Kept"
	}
	out := []agents.PayloadField{{
		Name:   "DV detail (" + inst.Name + ")",
		Value:  fmt.Sprintf("%s: %d\n%s: %d\n%s: %d", addLabel, added, removeLabel, removed, keepLabel, kept),
		Inline: true,
	}}

	// Extraction stats only when something happened — a "0 cached, 0
	// extracted" block for a library with zero DV files would just be
	// noise. Surface as a sibling inline column so it stacks visually
	// with the count column on Discord.
	t := resp.Totals
	if t.DvCandidates > 0 || t.DvExtractFailed > 0 || t.DvFileUnreachable > 0 || t.DvToolsMissing > 0 {
		var parts []string
		parts = append(parts, fmt.Sprintf("Candidates: %d", t.DvCandidates))
		if t.DvCacheHits > 0 {
			parts = append(parts, fmt.Sprintf("Cached: %d", t.DvCacheHits))
		}
		if t.DvExtracted > 0 {
			parts = append(parts, fmt.Sprintf("Extracted: %d", t.DvExtracted))
		}
		if t.DvExtractedNoRpu > 0 {
			parts = append(parts, fmt.Sprintf("No RPU: %d", t.DvExtractedNoRpu))
		}
		if t.DvExtractFailed > 0 {
			parts = append(parts, fmt.Sprintf("⚠ Failed: %d", t.DvExtractFailed))
		}
		if t.DvFileUnreachable > 0 {
			parts = append(parts, fmt.Sprintf("⚠ Unreachable: %d", t.DvFileUnreachable))
		}
		if t.DvToolsMissing > 0 {
			// User-facing fix is "go install"; keep the warning visually
			// distinct from extraction-runtime failures so the user knows
			// to look at the install banner first.
			parts = append(parts, fmt.Sprintf("⚠ Tools missing: %d", t.DvToolsMissing))
		}
		out = append(out, agents.PayloadField{
			Name:   "DV extraction",
			Value:  strings.Join(parts, "\n"),
			Inline: true,
		})
	}

	if resp.Applied != nil && len(resp.Applied.TagsCreated) > 0 {
		out = append(out, agents.PayloadField{
			Name:   "New DV-detail tags",
			Value:  "`" + strings.Join(resp.Applied.TagsCreated, "`, `") + "`",
			Inline: false,
		})
	}
	return out
}

// plexSyncModeField builds the Plex label-sync count block for a
// scheduled run, bringing the scan path to parity with the webhook
// path's PlexSyncDetail section. A sync can touch many items across
// several labels, so we surface matched/total plus a per-label
// add/remove/in-sync breakdown — the same shape users see in the
// result modal. Falls back to the one-line summary when no structured
// run is attached.
func plexSyncModeField(summary core.RunSummary) []agents.PayloadField {
	run, _ := summary.Result.(*core.PlexLabelRuleRun)
	if run == nil {
		if summary.Summary == "" {
			return nil
		}
		return []agents.PayloadField{{
			Name:   "Plex sync",
			Value:  truncateField(summary.Summary),
			Inline: false,
		}}
	}

	mode := run.RunMode
	if mode == "" {
		mode = "apply"
	}
	header := fmt.Sprintf("Matched %d / %d", run.Matched, run.ItemsTotal)
	if run.Unmatched > 0 {
		header += fmt.Sprintf(" (%d unmatched)", run.Unmatched)
	}
	header += " · " + mode
	lines := []string{header}

	// Per-label breakdown, ordered by label name for stable output.
	labelSet := map[string]struct{}{}
	for l := range run.Added {
		labelSet[l] = struct{}{}
	}
	for l := range run.Removed {
		labelSet[l] = struct{}{}
	}
	for l := range run.InSync {
		labelSet[l] = struct{}{}
	}
	ordered := make([]string, 0, len(labelSet))
	for l := range labelSet {
		ordered = append(ordered, l)
	}
	sort.Strings(ordered)
	for _, l := range ordered {
		var parts []string
		if run.Added[l] > 0 {
			parts = append(parts, fmt.Sprintf("+%d", run.Added[l]))
		}
		if run.Removed[l] > 0 {
			parts = append(parts, fmt.Sprintf("-%d", run.Removed[l]))
		}
		if run.InSync[l] > 0 {
			parts = append(parts, fmt.Sprintf("%d in sync", run.InSync[l]))
		}
		if len(parts) == 0 {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s: %s", l, strings.Join(parts, ", ")))
	}

	// Name a few of the unmatched items so the user can see WHAT didn't
	// match without opening the run, with a "+N more" tail when the run
	// went over the preview length.
	if len(run.UnmatchedItems) > 0 {
		const maxShow = 5
		names := make([]string, 0, maxShow)
		for i, u := range run.UnmatchedItems {
			if i >= maxShow {
				break
			}
			n := u.Title
			if u.Year > 0 {
				n += fmt.Sprintf(" (%d)", u.Year)
			}
			names = append(names, n)
		}
		line := "Unmatched: " + strings.Join(names, ", ")
		if run.Unmatched > len(names) {
			line += fmt.Sprintf(" (+%d more)", run.Unmatched-len(names))
		}
		lines = append(lines, line)
	}

	return []agents.PayloadField{{
		Name:   "Plex sync",
		Value:  truncateField(strings.Join(lines, "\n")),
		Inline: false,
	}}
}

// buildDvDetailDetail constructs the follow-up content for a dvdetail
// run. Three sections, each gated on having content:
//
//  1. "**DV detail — added/to add:**" — per-tag rollup (mel/fel/etc
//     with counts) wrapped in a code block. Same shape as the Extra
//     tags detail since the rollup payload uses the same scanDvDetailRollup
//     row type with action/tag/count.
//
//  2. "**DV detail — removed/to remove:**" — symmetric subtraction
//     side. Cleanup pass + RemoveOrphanedTags can produce these.
//
//  3. "**DV detail — extraction warnings:**" — when failed +
//     unreachable rows exist, list the per-movie reasons so the user
//     has a starting point for diagnosis. Truncated to the first 25
//     to keep the message under Discord's content limit; the full
//     list is in the per-run log file at LogPath.
//
// Returns empty when there's nothing actionable (preview with all
// kept, or no DV candidates).
func buildDvDetailDetail(summary core.RunSummary) string {
	resp, _ := summary.Result.(*scanResponse)
	if resp == nil {
		return ""
	}

	applyMode := resp.Applied != nil
	addSuffix, removeSuffix := "to add", "to remove"
	addHeader, removeHeader := "**DV detail — to add:**", "**DV detail — to remove:**"
	if applyMode {
		addSuffix, removeSuffix = "added", "removed"
		addHeader, removeHeader = "**DV detail — added:**", "**DV detail — removed:**"
	}

	formatRollup := func(rows []scanDvDetailRollup, suffix string) string {
		if len(rows) == 0 {
			return ""
		}
		var b strings.Builder
		for _, r := range rows {
			// Width 16 fits "dvprofile8" + a typical user prefix
			// like "dv-" → "dv-dvprofile8" = 13 chars with margin.
			b.WriteString(fmt.Sprintf("%-16s %d %s\n", r.Tag, r.Count, suffix))
		}
		return b.String()
	}

	var addRows, removeRows []scanDvDetailRollup
	for _, r := range resp.Totals.DvDetailRollups {
		switch r.Action {
		case "add":
			addRows = append(addRows, r)
		case "remove":
			removeRows = append(removeRows, r)
		}
	}

	var sections []string
	if body := formatRollup(addRows, addSuffix); body != "" {
		sections = append(sections, addHeader+"\n```\n"+body+"```")
	}
	if body := formatRollup(removeRows, removeSuffix); body != "" {
		sections = append(sections, removeHeader+"\n```\n"+body+"```")
	}

	// Extraction-warning section. Walks the per-movie items list for
	// status="failed" / "tools-missing" / "file-unreachable" rows.
	// Two caps work in tandem: dvDetailFailureLineMax truncates each
	// individual line so a long Unraid path + long Radarr title (which
	// can together push 250+ chars) can't balloon a single row, and
	// dvDetailFailureBodyMax stops appending when the cumulative body
	// would push past Discord's 2000-char content limit minus the
	// header + code-fence + footer overhead. Full failure list is in
	// the per-run log file at JobRun.LogPath.
	if resp.Totals.DvExtractFailed > 0 || resp.Totals.DvFileUnreachable > 0 || resp.Totals.DvToolsMissing > 0 {
		var failureLines []string
		runningBytes := 0
		for _, item := range resp.Items {
			if item.DvStatus != "failed" && item.DvStatus != "tools-missing" {
				continue
			}
			title := item.Title
			if item.Year > 0 {
				title = fmt.Sprintf("%s (%d)", item.Title, item.Year)
			}
			reason := ""
			if len(item.DvDecisions) > 0 {
				reason = item.DvDecisions[0].Reason
			}
			if reason == "" {
				reason = item.DvStatus
			}
			line := truncateRune(fmt.Sprintf("%s — %s", title, reason), dvDetailFailureLineMax)
			// Budget check: appending this line + a "\n" would exceed
			// the body cap. Stop here — caller surfaces a "…and N
			// more" footer.
			if runningBytes+len(line)+1 > dvDetailFailureBodyMax {
				break
			}
			failureLines = append(failureLines, line)
			runningBytes += len(line) + 1
		}
		if len(failureLines) > 0 {
			body := strings.Join(failureLines, "\n") + "\n"
			total := resp.Totals.DvExtractFailed + resp.Totals.DvFileUnreachable + resp.Totals.DvToolsMissing
			if total > len(failureLines) {
				body += fmt.Sprintf("…and %d more (see log file)\n", total-len(failureLines))
			}
			sections = append(sections, "**DV detail — extraction warnings:**\n```\n"+body+"```")
		}
	}

	return strings.Join(sections, "\n\n")
}

// dvDetailFailureLineMax caps the per-row length so a long Radarr
// title (~200 chars) plus a long path-translated reason (~100 chars)
// can't produce a single 250+ char line. 150 covers a typical title
// + short-form reason; longer reasons still survive in the log file
// at JobRun.LogPath where the full text lives.
//
// dvDetailFailureBodyMax is the cumulative byte budget for the
// failure-section body inside the code block. Discord caps message
// content at 2000 chars; subtracting the section header
// (~50 chars), code-fence overhead (~10), trailing newline + footer
// "…and N more (see log file)" (~40) leaves ~1900 for the body.
// Picking 1500 gives generous margin for the inevitable expansion
// of header copy as features land.
const (
	dvDetailFailureLineMax = 150
	dvDetailFailureBodyMax = 1500
)

// truncateRune cuts s to at most maxBytes bytes without slicing
// inside a UTF-8 rune. Falls back to byte-cap when s is already
// short. Suffix " …" added when truncation happened so the reader
// sees the line was clipped.
func truncateRune(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes - 2 // leave room for " …" suffix
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		// Walk back to the rune boundary.
		cut--
	}
	if cut <= 0 {
		return s[:maxBytes]
	}
	return s[:cut] + " …"
}

// formatRuntimeValue builds bash's "Completed in Xm Ys | Dry-run: <bool>"
// line. Format follows tagarr.sh: minutes+seconds for runs >=60s, fractional
// seconds otherwise. Dry-run inverts run-mode: apply → "false", preview
// → "true".
func formatRuntimeValue(job core.ScheduledJob, duration time.Duration) string {
	dryRun := "true"
	if job.Options.RunMode == "" || job.Options.RunMode == "apply" {
		dryRun = "false"
	}
	return fmt.Sprintf("Completed in %s | Dry-run: %s", formatDurationHuman(duration), dryRun)
}

// formatDurationHuman renders a duration in tagarr.sh-compatible form.
// Sub-minute → "12.3s"; longer → "9m 24s" with whole-second precision.
// Hours rare for a tag-mode run; lump into Xm if it ever happens.
func formatDurationHuman(d time.Duration) string {
	secs := int(d.Seconds())
	if secs < 60 {
		// Sub-minute — show 1-decimal precision so quick runs aren't 0s.
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	mins := secs / 60
	rem := secs % 60
	return fmt.Sprintf("%dm %ds", mins, rem)
}

// buildScheduleDetail constructs the follow-up "**Tagged Movies:**\n```\n
// ...```" content matching tagarr.sh's per-group movie list. Returns
// empty string when the run has no actionable detail (preview only,
// no items, error path), in which case Discord skips the follow-up.
func buildScheduleDetail(job core.ScheduledJob, summary core.RunSummary, runErr error) string {
	if runErr != nil {
		return ""
	}
	switch job.Mode {
	case core.JobModeTag:
		return buildTagDetail(summary)
	case core.JobModeAudioTags:
		return buildAutoTagsDetailFromResponse(asScanResp(summary), "Audio tags")
	case core.JobModeVideoTags:
		return buildAutoTagsDetailFromResponse(asScanResp(summary), "Video tags")
	case core.JobModeDvDetail:
		return buildDvDetailDetail(summary)
	case core.JobModeCombined:
		// Combined: every phase's detail block stacked, separated by
		// blank lines. Order matches the chain run order.
		cr, _ := summary.Result.(combinedScheduleResult)
		var parts []string
		if d := buildCombinedDiscoverDetail(summary); d != "" {
			parts = append(parts, d)
		}
		if d := buildTagDetailFromCombined(summary); d != "" {
			parts = append(parts, d)
		}
		if d := buildAutoTagsDetailFromResponse(cr.AudioTags, "Audio tags"); d != "" {
			parts = append(parts, d)
		}
		if d := buildAutoTagsDetailFromResponse(cr.VideoTags, "Video tags"); d != "" {
			parts = append(parts, d)
		}
		if cr.DvDetail != nil {
			if d := buildDvDetailDetail(core.RunSummary{Result: cr.DvDetail}); d != "" {
				parts = append(parts, d)
			}
		}
		return strings.Join(parts, "\n\n")
	case core.JobModeDiscover:
		return buildDiscoverDetail(summary)
	}
	return ""
}

// asScanResp narrows summary.Result to *scanResponse, returning nil
// when the type doesn't match. Tiny helper because the audiotags +
// videotags detail builders both unwrap the same way.
func asScanResp(summary core.RunSummary) *scanResponse {
	resp, _ := summary.Result.(*scanResponse)
	return resp
}

// buildAutoTagsDetailFromResponse builds the per-tag rollup block
// for an audiotags or videotags response. label is the human-
// readable section name ("Audio tags" / "Video tags").
//
//	**<label> applied:**
//	```
//	 resolution
//	2160p     787 added
//	 codec
//	h265      523 added
//	```
//
// Apply-mode says "added/removed"; preview says "to add/to remove".
// Returns empty when the response is nil or has no rollup rows.
func buildAutoTagsDetailFromResponse(resp *scanResponse, label string) string {
	if resp == nil || len(resp.Totals.AutoTagRollups) == 0 {
		return ""
	}

	var addRows, removeRows []scanAutoTagRollup
	for _, b := range resp.Totals.AutoTagRollups {
		switch b.Action {
		case "add":
			addRows = append(addRows, b)
		case "remove":
			removeRows = append(removeRows, b)
		}
	}

	applyMode := resp.Applied != nil
	var sections []string

	formatRows := func(rows []scanAutoTagRollup, suffix string) string {
		if len(rows) == 0 {
			return ""
		}
		bucketOrder := []string{}
		byBucket := map[string][]scanAutoTagRollup{}
		for _, r := range rows {
			if _, seen := byBucket[r.Bucket]; !seen {
				bucketOrder = append(bucketOrder, r.Bucket)
			}
			byBucket[r.Bucket] = append(byBucket[r.Bucket], r)
		}
		var b strings.Builder
		for i, bucket := range bucketOrder {
			if i > 0 {
				b.WriteString("\n")
			}
			b.WriteString(" " + bucket + "\n")
			for _, r := range byBucket[bucket] {
				b.WriteString(fmt.Sprintf("%-20s %d %s\n", r.Tag, r.Count, suffix))
			}
		}
		return b.String()
	}

	addSuffix, removeSuffix := "to add", "to remove"
	addHeader := fmt.Sprintf("**%s — to add:**", label)
	removeHeader := fmt.Sprintf("**%s — to remove:**", label)
	if applyMode {
		addSuffix, removeSuffix = "added", "removed"
		addHeader = fmt.Sprintf("**%s — added:**", label)
		removeHeader = fmt.Sprintf("**%s — removed:**", label)
	}

	if body := formatRows(addRows, addSuffix); body != "" {
		sections = append(sections, addHeader+"\n```\n"+body+"```")
	}
	if body := formatRows(removeRows, removeSuffix); body != "" {
		sections = append(sections, removeHeader+"\n```\n"+body+"```")
	}
	return strings.Join(sections, "\n\n")
}

// buildTagDetail builds the per-group movie listing for a tag-mode
// run. Bash format:
//
//   **Tagged Movies:**
//   ```
//    DisplayName1
//   Movie A ✓
//   Movie B
//
//    DisplayName2
//   Movie C
//   ```
//
// ✓ marks movies that were also added in secondary (sync mode). The
// leading space before each group name matches bash literal output.
func buildTagDetail(summary core.RunSummary) string {
	resp, _ := summary.Result.(*scanResponse)
	return formatTagDetailFromResponse(resp)
}

func buildTagDetailFromCombined(summary core.RunSummary) string {
	cr, ok := summary.Result.(combinedScheduleResult)
	if !ok || cr.Tag == nil {
		return ""
	}
	return formatTagDetailFromResponse(cr.Tag)
}

func formatTagDetailFromResponse(resp *scanResponse) string {
	if resp == nil || len(resp.Items) == 0 {
		return ""
	}
	tagged := buildPerGroupMovieList(resp, "add")
	untagged := buildPerGroupMovieListWithReason(resp, "remove")

	var sections []string
	if tagged != "" {
		sections = append(sections, "**Tagged Movies:**\n```\n"+tagged+"```")
	}
	if untagged != "" {
		sections = append(sections, "**Untagged Movies:**\n```\n"+untagged+"```")
	}
	return strings.Join(sections, "\n\n")
}

// buildPerGroupMovieList groups the response's items by release-group
// display name, listing each movie with its secondary-side ✓ marker
// when sync also took the same action. Used for the additive side
// (Tagged Movies). Returns the body lines without code-block fences;
// caller wraps. Empty string when no items match the action.
func buildPerGroupMovieList(resp *scanResponse, action string) string {
	type movieEntry struct {
		title       string
		secondaryOK bool
	}
	groupOrder := []string{}
	groups := map[string][]movieEntry{}
	for _, item := range resp.Items {
		for _, d := range item.Decisions {
			if d.Action != action {
				continue
			}
			displayName := d.GroupDisplay
			if displayName == "" {
				displayName = d.GroupTag
			}
			if _, seen := groups[displayName]; !seen {
				groupOrder = append(groupOrder, displayName)
			}
			title := item.Title
			if item.Year > 0 {
				title = fmt.Sprintf("%s (%d)", item.Title, item.Year)
			}
			groups[displayName] = append(groups[displayName], movieEntry{
				title:       title,
				secondaryOK: d.SecondaryAction == action,
			})
		}
	}
	if len(groupOrder) == 0 {
		return ""
	}
	var b strings.Builder
	for i, group := range groupOrder {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, " %s\n", group)
		for _, m := range groups[group] {
			b.WriteString(m.title)
			if m.secondaryOK {
				b.WriteString(" ✓")
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}

// buildPerGroupMovieListWithReason is like buildPerGroupMovieList but
// includes the engine's per-decision Reason in parentheses after each
// title. Used for the subtractive side (Untagged Movies) so the user
// sees WHY a tag was pulled (audio filter / quality filter / matched
// only old release / etc). Bash includes the same context in its
// untagged-movies follow-up.
func buildPerGroupMovieListWithReason(resp *scanResponse, action string) string {
	groupOrder := []string{}
	type entry struct {
		title  string
		reason string
	}
	groups := map[string][]entry{}
	for _, item := range resp.Items {
		for _, d := range item.Decisions {
			if d.Action != action {
				continue
			}
			displayName := d.GroupDisplay
			if displayName == "" {
				displayName = d.GroupTag
			}
			if _, seen := groups[displayName]; !seen {
				groupOrder = append(groupOrder, displayName)
			}
			title := item.Title
			if item.Year > 0 {
				title = fmt.Sprintf("%s (%d)", item.Title, item.Year)
			}
			groups[displayName] = append(groups[displayName], entry{
				title:  title,
				reason: d.Reason,
			})
		}
	}
	if len(groupOrder) == 0 {
		return ""
	}
	var b strings.Builder
	for i, group := range groupOrder {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, " %s\n", group)
		for _, m := range groups[group] {
			b.WriteString(m.title)
			if m.reason != "" {
				fmt.Fprintf(&b, " (%s)", m.reason)
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}

// buildDiscoverDetail builds the candidate-listing for a discover-only
// run. Bash format (left-pad group name to 20 chars):
//
//   **Discovered: 14 groups | 190 movies**
//   ```
//   126811               11 movies
//   12GaugeShotgun       2 movies
//   ...
//   Dry-run: not written to config
//   ```
func buildDiscoverDetail(summary core.RunSummary) string {
	resp, _ := summary.Result.(*scanResponse)
	return formatDiscoverDetailFromResponse(resp, true /* standalone discover is always dry-run for schedules */)
}

func buildCombinedDiscoverDetail(summary core.RunSummary) string {
	cr, ok := summary.Result.(combinedScheduleResult)
	if !ok || cr.Discover == nil {
		return ""
	}
	return formatDiscoverDetailFromResponse(cr.Discover, false /* combined-mode auto-adds in apply, dry-run when preview */)
}

func formatDiscoverDetailFromResponse(resp *scanResponse, dryRun bool) string {
	if resp == nil || len(resp.Discovered) == 0 {
		return ""
	}
	groupCount := len(resp.Discovered)
	var movieTotal int
	for _, d := range resp.Discovered {
		movieTotal += d.Count
	}
	var b strings.Builder
	fmt.Fprintf(&b, "**Discovered: %d group%s | %d movie%s**\n```\n", groupCount, plural(groupCount), movieTotal, plural(movieTotal))
	for _, d := range resp.Discovered {
		// Left-pad group name to 20 chars for column alignment.
		fmt.Fprintf(&b, "%-20s %d movie%s\n", d.Search, d.Count, plural(d.Count))
	}
	if dryRun {
		b.WriteString("\nDry-run: not written to config\n")
	}
	b.WriteString("```")
	return b.String()
}

// buildPlainScheduleMessage flattens the fields-grid into plain text for
// providers that don't support fields (Gotify, NTFY, Pushover, Apprise).
// Each field becomes "<Name>: <Value>" — newlines inside the value
// preserved. Discord ignores Message when Fields is non-empty.
func buildPlainScheduleMessage(fields []agents.PayloadField) string {
	if len(fields) == 0 {
		return ""
	}
	var b strings.Builder
	for i, f := range fields {
		if i > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "**%s**\n%s", f.Name, f.Value)
	}
	return b.String()
}

// truncateField caps a free-text value at the Discord field-value limit
// (1024 chars). Used for Error / no-result-summary fields where the
// content can be long.
func truncateField(s string) string {
	const maxField = 1000 // bit of headroom under the 1024 hard cap
	if len(s) <= maxField {
		return s
	}
	return s[:maxField-1] + "…"
}
