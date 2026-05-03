package api

import (
	"context"
	"fmt"
	"strings"
	"time"

	"resolvarr/internal/core"
	"resolvarr/internal/core/engine"
)

// scheduler_runner.go — implements core.Runner, the bridge from the
// generic scheduler to the scan-action handlers. Per the M3d analysis
// doc the dependency direction is api → core, so the scheduler in core
// can't import api types. Instead, core defines a small Runner interface
// (RunSchedule(ctx, ScheduledJob) → RunSummary) and api implements it
// here.
//
// Each fire converts ScheduledJob.Options to a scanRunRequest and
// dispatches to the appropriate run* method. The result's totals get
// summarized into RunSummary; per-item detail lands in RunSummary.Detail
// for the log file.

// schedulerRunner is the api-side adapter. Pointer-receiver so we can
// extend it later without touching every callsite.
type schedulerRunner struct {
	server *Server
}

// newSchedulerRunner constructs a schedulerRunner over an existing
// Server. main.go calls this after NewServer to wire the scheduler.
func newSchedulerRunner(s *Server) *schedulerRunner {
	return &schedulerRunner{server: s}
}

// RunSchedule implements core.Runner.
func (r *schedulerRunner) RunSchedule(ctx context.Context, job core.ScheduledJob) (core.RunSummary, error) {
	cfg := r.server.App.Config.Get()

	// Resolve instance.
	var inst *core.Instance
	for i := range cfg.Instances {
		if cfg.Instances[i].ID == job.InstanceID {
			inst = &cfg.Instances[i]
			break
		}
	}
	if inst == nil {
		return core.RunSummary{}, fmt.Errorf("instance %q not found", job.InstanceID)
	}
	appType := inst.Type
	if appType != "radarr" {
		// v1 scope match — see scan_*.go. Sonarr lands later per
		// per-instance-type-ux.md.
		return core.RunSummary{}, fmt.Errorf("schedule against non-radarr instance not supported in v1")
	}

	// Overlay schedule's per-rule config snapshots over the global cfg.
	// After this, downstream phase handlers see the schedule's own
	// Filters / ExtraTags / ReleaseGroups subset — not the live UI
	// state. Pre-rule-model schedules with nil per-rule fields fall
	// through to global (idempotent migration in Load handles them
	// on first start, but the overlay handles them safely either way).
	cfg = applyScheduleOverlay(cfg, job, appType)

	// Pick filter config (per-Arr-type).
	var filterCfg engine.FilterConfig
	switch appType {
	case "radarr":
		filterCfg = cfg.Filters.Radarr
	case "sonarr":
		filterCfg = cfg.Filters.Sonarr
	}

	// Wall-clock the dispatch so the notification embed can include
	// "Completed in Xs" matching bash tagarr.sh's Runtime field. The
	// scheduler.go fire() measures its own duration for JobRun.DurationMs;
	// the two differ by a few microseconds (instance lookup + filter
	// pick) which doesn't matter at the second granularity we display.
	start := time.Now()
	var summary core.RunSummary
	var runErr error
	switch job.Mode {
	case core.JobModeTag:
		summary, runErr = r.runTagSchedule(ctx, cfg, inst, appType, filterCfg, job)
	case core.JobModeDiscover:
		summary, runErr = r.runDiscoverSchedule(ctx, cfg, inst, appType, filterCfg, job)
	case core.JobModeRecover:
		summary, runErr = r.runRecoverSchedule(ctx, inst, appType, job)
	case core.JobModeAudioTags:
		summary, runErr = r.runAudioTagsSchedule(ctx, cfg, inst, appType, job)
	case core.JobModeVideoTags:
		summary, runErr = r.runVideoTagsSchedule(ctx, cfg, inst, appType, job)
	case core.JobModeDvDetail:
		summary, runErr = r.runDvDetailSchedule(ctx, cfg, inst, appType, job)
	case core.JobModeCombined:
		summary, runErr = r.runCombinedSchedule(ctx, cfg, inst, appType, filterCfg, job)
	default:
		return core.RunSummary{}, fmt.Errorf("unsupported job mode %q", job.Mode)
	}
	duration := time.Since(start)

	// Notification dispatch — best-effort, runs after every fire (success
	// or error). Live runs in the UI deliberately skip this hook; a user
	// with the page open reads the result inline. Routes through every
	// configured NotificationAgent whose Events flag matches; see
	// scheduler_notify.go for payload construction.
	r.notifyScheduleResult(inst, job, summary, runErr, duration)
	return summary, runErr
}

// runTagSchedule converts JobOptions → scanRunRequest and runs tag-mode.
// Cleanup-tail is included when JobOptions.CleanupUnusedTags is set
// (matches the Quick fix-all chain semantics).
func (r *schedulerRunner) runTagSchedule(ctx context.Context, cfg core.Config, inst *core.Instance, appType string, filterCfg engine.FilterConfig, job core.ScheduledJob) (core.RunSummary, error) {
	req := scanRunRequest{
		InstanceID:        job.InstanceID,
		Mode:              defaultMode(job.Options.RunMode, "apply"),
		Action:            "tag",
		RunGroups:         job.Options.RunForGroups,
		CleanupUnusedTags: job.Options.CleanupUnusedTags,
	}
	if job.Options.SyncToSecondary {
		req.SyncToInstanceID = resolveSyncTarget(cfg, inst, appType, job.Options.SyncToInstanceID)
	}
	resp, apiErr := r.server.runTag(ctx, cfg, inst, appType, filterCfg, req)
	r.server.auditScan("schedule:"+job.ID, "tag", inst, req, resp, errMsgOf(apiErr))
	if apiErr != nil {
		return core.RunSummary{}, apiErr
	}
	out := summarizeTagResponse(resp)
	out.Result = resp
	return out, nil
}

// runDiscoverSchedule runs discover-mode. Always preview-mode against
// Arr (no tags written), but JobOptions controls whether discovered
// candidates are appended to cfg.ReleaseGroups so the next scheduled
// Tag run picks them up:
//
//   DiscoverWriteBack=false                       — preview only, no config change
//   DiscoverWriteBack=true, AutoActivate=false    — append with Enabled=false
//   DiscoverWriteBack=true, AutoActivate=true     — append with Enabled=true (immediate use)
//
// Standalone Discover (Library scan → Release Groups → Find new
// groups) is unchanged — it still uses the +Add button per row for
// manual review. Schedules are the auto-add path.
func (r *schedulerRunner) runDiscoverSchedule(ctx context.Context, cfg core.Config, inst *core.Instance, appType string, filterCfg engine.FilterConfig, job core.ScheduledJob) (core.RunSummary, error) {
	req := scanRunRequest{
		InstanceID:   job.InstanceID,
		Mode:         "preview",
		Action:       "discover",
		IncludeKnown: false,
	}
	resp, apiErr := r.server.runDiscover(ctx, cfg, inst, appType, filterCfg, req)
	r.server.auditScan("schedule:"+job.ID, "discover", inst, req, resp, errMsgOf(apiErr))
	if apiErr != nil {
		return core.RunSummary{}, apiErr
	}
	summary := fmt.Sprintf("%d new release-group candidate%s", resp.Totals.Discovered, plural(resp.Totals.Discovered))
	// Preview-mode schedules are read-only — skip write-back even when
	// the rule has it enabled. Apply is the default for schedules so
	// the common case isn't affected; this matches the QFA chain gate.
	chainMode := defaultMode(job.Options.RunMode, "apply")
	if job.Options.DiscoverWriteBack && chainMode == "apply" && len(resp.Discovered) > 0 {
		added, persistErr := r.persistDiscoveredGroups(resp.Discovered, inst.Type, job.Options.AutoActivateDiscovered)
		if persistErr != nil {
			summary += fmt.Sprintf(" — write-back failed: %s", persistErr.Error())
		} else {
			verb := "added (disabled)"
			if job.Options.AutoActivateDiscovered {
				verb = "added + enabled"
			}
			summary += fmt.Sprintf(" — %d %s", len(added), verb)
			if r.server.Scheduler != nil {
				r.server.Scheduler.Reload()
			}
		}
	}
	return core.RunSummary{
		Status:  "ok",
		Summary: summary,
		Result:  resp,
	}, nil
}

// persistDiscoveredGroups is a thin wrapper around
// Server.applyDiscoverWriteBack that re-shapes the result back
// into []core.ReleaseGroup so the schedule-runner can inject the
// new groups into its local cfg.ReleaseGroups (for the in-chain
// Tag phase). Single source of truth for the persistence logic
// lives in scan_discover.go's applyDiscoverWriteBack — schedule +
// /api/scan/run share it so the same auto-add behaviour applies
// whether the user fires via Quick fix-all or a saved cron.
func (r *schedulerRunner) persistDiscoveredGroups(discovered []scanDiscoveredGroup, instType string, enable bool) ([]core.ReleaseGroup, error) {
	addedShort, err := r.server.applyDiscoverWriteBack(discovered, instType, enable)
	if err != nil {
		return nil, err
	}
	// Re-fetch the just-added rows from the store to get the full
	// ReleaseGroup shape (Display/Mode/Type/Search/Enabled) — the
	// scanDiscoverAdded shape only has ID/Search/Tag/Enabled. The
	// schedule-runner needs full ReleaseGroup for cfg.ReleaseGroups
	// injection so the Tag phase reads them like any other group.
	if len(addedShort) == 0 {
		return nil, nil
	}
	idSet := make(map[string]bool, len(addedShort))
	for _, a := range addedShort {
		idSet[a.ID] = true
	}
	var out []core.ReleaseGroup
	for _, g := range r.server.App.Config.Get().ReleaseGroups {
		if idSet[g.ID] {
			out = append(out, g)
		}
	}
	return out, nil
}

// runRecoverSchedule runs recover-mode in apply by default (otherwise
// the schedule never fixes anything). RecoverApplyItems is unused —
// the schedule applies to every would-fix candidate. Per-row exclude
// is a UI-only feature.
func (r *schedulerRunner) runRecoverSchedule(ctx context.Context, inst *core.Instance, appType string, job core.ScheduledJob) (core.RunSummary, error) {
	req := scanRunRequest{
		InstanceID:    job.InstanceID,
		Mode:          defaultMode(job.Options.RunMode, "apply"),
		Action:        "recover",
		RecoverRename: true, // Match bash RENAME=true default. Add a JobOptions field in v2 if a user wants to disable.
	}
	resp, apiErr := r.server.runRecover(ctx, inst, appType, req)
	r.server.auditScan("schedule:"+job.ID, "recover", inst, req, resp, errMsgOf(apiErr))
	if apiErr != nil {
		return core.RunSummary{}, apiErr
	}
	out := summarizeRecoverResponse(resp)
	out.Result = resp
	return out, nil
}

// resolveSyncTarget picks the instance ID to mirror tag decisions to.
// Resolution order:
//
//  1. preferred (job.Options.SyncToInstanceID) — if set AND it points
//     at a real instance of the right type AND it's not the primary,
//     use it. This is the explicit user choice from the new picker.
//  2. fallback — first instance of the same type whose ID isn't the
//     primary's. Preserves the legacy "auto-pick" behaviour for rules
//     that predate the picker, and is a sensible default for users
//     with exactly one secondary.
//
// Returns "" when no valid target exists (only one instance of this
// type, or preferred was bogus and no fallback either). Caller should
// treat empty as "skip sync silently" — the runTag handler does.
func resolveSyncTarget(cfg core.Config, primary *core.Instance, appType, preferred string) string {
	if preferred != "" {
		for _, i := range cfg.Instances {
			if i.ID == preferred && i.Type == appType && i.ID != primary.ID {
				return i.ID
			}
		}
		// Preferred ID didn't validate (instance deleted, type mismatch,
		// or aliases the primary). Fall through to auto-pick rather than
		// silently skipping — user enabled sync, they want SOME target.
	}
	for _, i := range cfg.Instances {
		if i.Type == appType && i.ID != primary.ID {
			return i.ID
		}
	}
	return ""
}

// applyScheduleOverlay returns a Config with the schedule's per-rule
// snapshots overlaid on the global cfg. Thin wrapper around
// applyRuleOverlay so schedule-mode and adhoc-quickfix-mode share the
// same overlay semantics — see applyRuleOverlay for the rules.
func applyScheduleOverlay(cfg core.Config, job core.ScheduledJob, appType string) core.Config {
	// Schedules don't inject ephemeral groups — that's a chain-runner-only
	// concept used by Quick fix-all to flow Discover findings into Tag
	// preview. Pass nil to keep the schedule path's overlay semantics.
	return applyRuleOverlay(cfg, job.Filters, job.AudioTags, job.VideoTags, job.DvDetail, job.ReleaseGroupIDs, appType, nil)
}

// applyRuleOverlay returns a Config with the supplied rule-style
// snapshots overlaid on the global cfg. The snapshots win when
// present; nil fields fall through to global (back-compat for
// pre-rule-model schedules that haven't been migrated yet AND every
// existing /api/scan/run caller that posts no overlay).
//
// This is the gate that makes schedules act as self-contained "config
// presets" — downstream phase handlers (runTag/runDiscover/runRecover/
// runAudioTags/runVideoTags/runDvDetail) read from
// cfg.{Filters,AudioTags,VideoTags,DvDetail,ReleaseGroups} as always;
// they don't need to know about who supplied the override.
// Quickfix mode reuses this same path so a one-shot Run-now with
// edited filters/RGs/auto-tags doesn't pollute saved globals.
//
// Important: cfg is a value type and the caller passes it by value, so
// the local mutations stay local to this function's return value. The
// store's underlying snapshot is unaffected (and ConfigStore.Get
// already deep-copies the relevant slices).
func applyRuleOverlay(cfg core.Config, filters *engine.FilterConfig, audioTags *core.AudioTagsConfig, videoTags *core.VideoTagsConfig, dvDetail *core.DvDetailConfig, releaseGroupIDs []string, appType string, injectGroups []core.ReleaseGroup) core.Config {
	if filters != nil {
		switch appType {
		case "sonarr":
			cfg.Filters.Sonarr = *filters
		default:
			cfg.Filters.Radarr = *filters
		}
	}
	if audioTags != nil {
		cfg.AudioTags = *audioTags
	}
	if videoTags != nil {
		cfg.VideoTags = *videoTags
	}
	if dvDetail != nil {
		cfg.DvDetail = *dvDetail
	}
	// nil = "use global RGs as-is"; non-empty = "restrict to this
	// subset". Empty (`[]`) is treated as nil here because JSON decoding
	// produces a non-nil empty slice for `[]` and we don't want a
	// stray quickfix-extratags-only payload to wholesale-zero the user's
	// RG list mid-chain. Schedules that *want* zero groups have no
	// reason to exist (Tag/Discover with no groups is a no-op rule),
	// and the saveRuleEditor validator already rejects them.
	if len(releaseGroupIDs) > 0 {
		idSet := make(map[string]bool, len(releaseGroupIDs))
		for _, id := range releaseGroupIDs {
			idSet[id] = true
		}
		var subset []core.ReleaseGroup
		for _, g := range cfg.ReleaseGroups {
			if idSet[g.ID] {
				subset = append(subset, g)
			}
		}
		cfg.ReleaseGroups = subset
	}
	// Append ephemeral injected groups AFTER the subset filter so they
	// can never be filtered out by an empty selection. Each must match
	// the active appType — mismatched entries are dropped silently
	// rather than treated as Radarr/Sonarr coercion errors.
	if len(injectGroups) > 0 {
		for _, g := range injectGroups {
			if g.Type != "" && g.Type != appType {
				continue
			}
			// Force enabled — the chain runner is the only caller that
			// uses this path, and an injected-but-disabled group would
			// be a no-op that confuses the tag preview.
			g.Enabled = true
			if g.Type == "" {
				g.Type = appType
			}
			cfg.ReleaseGroups = append(cfg.ReleaseGroups, g)
		}
	}
	return cfg
}

// runAudioTagsSchedule runs the audiotags scan against a single
// instance. Mirrors runTagSchedule's shape. Config lives in
// cfg.AudioTags, read live at fire-time per the rule model.
//
// Mode defaults to "apply" — a schedule that previewed-only would
// never actually update tags, defeating the purpose.
func (r *schedulerRunner) runAudioTagsSchedule(ctx context.Context, cfg core.Config, inst *core.Instance, appType string, job core.ScheduledJob) (core.RunSummary, error) {
	req := scanRunRequest{
		InstanceID: job.InstanceID,
		Mode:       defaultMode(job.Options.RunMode, "apply"),
		Action:     "audiotags",
	}
	resp, apiErr := r.server.runAudioTags(ctx, cfg, inst, appType, req)
	r.server.auditScan("schedule:"+job.ID, "audiotags", inst, req, resp, errMsgOf(apiErr))
	if apiErr != nil {
		return core.RunSummary{}, apiErr
	}
	out := summarizeAutoTagsResponse(resp, "audio tags")
	out.Result = resp
	return out, nil
}

// runVideoTagsSchedule — same shape, video-stream side.
func (r *schedulerRunner) runVideoTagsSchedule(ctx context.Context, cfg core.Config, inst *core.Instance, appType string, job core.ScheduledJob) (core.RunSummary, error) {
	req := scanRunRequest{
		InstanceID: job.InstanceID,
		Mode:       defaultMode(job.Options.RunMode, "apply"),
		Action:     "videotags",
	}
	resp, apiErr := r.server.runVideoTags(ctx, cfg, inst, appType, req)
	r.server.auditScan("schedule:"+job.ID, "videotags", inst, req, resp, errMsgOf(apiErr))
	if apiErr != nil {
		return core.RunSummary{}, apiErr
	}
	out := summarizeAutoTagsResponse(resp, "video tags")
	out.Result = resp
	return out, nil
}

// runDvDetailSchedule runs the dvdetail scan-action against a single
// instance. Mirror of runExtraTagsSchedule — config (Enabled / prefix /
// allowedValues / removeOrphanedTags) lives in cfg.DvDetail and is
// read live at fire-time per the documented "config not frozen on
// schedule" rule. Tools-availability is checked inside runDvDetail
// and surfaces as an apiError; the scheduler logs it like any other
// run failure.
//
// Mode defaults to "apply" — a periodic preview-only schedule would
// extract DV detail every fire and write nothing, defeating the
// purpose. Override via JobOptions.RunMode if a user wants a recurring
// audit-style preview (handy for monitoring extraction-failure rate
// without committing tag writes).
func (r *schedulerRunner) runDvDetailSchedule(ctx context.Context, cfg core.Config, inst *core.Instance, appType string, job core.ScheduledJob) (core.RunSummary, error) {
	req := scanRunRequest{
		InstanceID:    job.InstanceID,
		Mode:          defaultMode(job.Options.RunMode, "apply"),
		Action:        "dvdetail",
		BypassDvCache: job.Options.BypassDvCache,
	}
	resp, apiErr := r.server.runDvDetail(ctx, cfg, inst, appType, req)
	r.server.auditScan("schedule:"+job.ID, "dvdetail", inst, req, resp, errMsgOf(apiErr))
	if apiErr != nil {
		return core.RunSummary{}, apiErr
	}
	out := summarizeDvDetailResponse(resp)
	out.Result = resp
	return out, nil
}

// runCombinedSchedule chains discover → recover → tag → cleanup →
// audiotags → videotags → dvdetail per JobOptions.CombinedModes.
// Phase order mirrors the live-UI Quick fix-all flow:
//   - Phase 1: Discover (preview-only, never auto-adds)
//   - Phase 2: Recover (apply-all — patches missing releaseGroup
//              before Tag pass)
//   - Phase 3: Tag library (with optional cleanup-tail)
//   - Phase 4: Audio tags
//   - Phase 5: Video tags (must run before DV detail so HDR-bucket
//              "dv" tag exists before DV detail layers profile/CM
//              tags on top)
//   - Phase 6: DV detail (slow — fresh-cache extraction)
//
// Phases run sequentially; if any phase errors, downstream phases
// are skipped and the partial result is returned.
func (r *schedulerRunner) runCombinedSchedule(ctx context.Context, cfg core.Config, inst *core.Instance, appType string, filterCfg engine.FilterConfig, job core.ScheduledJob) (core.RunSummary, error) {
	includeDiscover := false
	includeRecover := false
	includeTag := false
	includeAudioTags := false
	includeVideoTags := false
	includeDvDetail := false
	for _, m := range job.Options.CombinedModes {
		switch m {
		case core.JobModeDiscover:
			includeDiscover = true
		case core.JobModeRecover:
			includeRecover = true
		case core.JobModeTag:
			includeTag = true
		case core.JobModeAudioTags:
			includeAudioTags = true
		case core.JobModeVideoTags:
			includeVideoTags = true
		case core.JobModeDvDetail:
			includeDvDetail = true
		}
	}
	if !includeDiscover && !includeRecover && !includeTag &&
		!includeAudioTags && !includeVideoTags && !includeDvDetail {
		return core.RunSummary{}, fmt.Errorf("combined schedule must include at least one of discover/recover/tag/audiotags/videotags/dvdetail")
	}

	var combined []string

	// Aggregate result for the history modal — frontend reads .tag and
	// .discover slots like it does on a live Quick fix-all run, so the
	// drill-in UI for both panes works identically when replayed.
	combinedResult := combinedScheduleResult{}

	// Track first phase error so we can surface partial results when the
	// second phase fails. Without this the discover output would be
	// silently dropped on tag-phase failures.
	var phaseErr error

	if includeDiscover {
		discoverReq := scanRunRequest{
			InstanceID:   job.InstanceID,
			Mode:         "preview",
			Action:       "discover",
			IncludeKnown: false,
		}
		resp, apiErr := r.server.runDiscover(ctx, cfg, inst, appType, filterCfg, discoverReq)
		r.server.auditScan("schedule:"+job.ID, "discover", inst, discoverReq, resp, errMsgOf(apiErr))
		if apiErr != nil {
			phaseErr = apiErr
		} else {
			combined = append(combined, fmt.Sprintf("discover: %d", resp.Totals.Discovered))
			combinedResult.Discover = resp
			// Discover write-back: when the rule asked for auto-add,
			// persist the new groups AND inject them into the local
			// cfg.ReleaseGroups used by subsequent phases. Without
			// the local injection, the Tag phase below would skip
			// the just-added groups because applyScheduleOverlay
			// already filtered cfg.ReleaseGroups to the rule's
			// pre-run RG-ID snapshot. Whole point of running
			// Discover before Tag in a combined chain is to tag
			// with the just-found groups in the same run.
			// Same preview-mode gate as runDiscoverSchedule — preview
			// chains stay read-only even when the rule has write-back on.
			combinedChainMode := defaultMode(job.Options.RunMode, "apply")
			if job.Options.DiscoverWriteBack && combinedChainMode == "apply" && len(resp.Discovered) > 0 {
				added, persistErr := r.persistDiscoveredGroups(resp.Discovered, inst.Type, job.Options.AutoActivateDiscovered)
				if persistErr != nil {
					combined = append(combined, "discover write-back failed: "+persistErr.Error())
				} else if len(added) > 0 {
					verb := "added (disabled)"
					if job.Options.AutoActivateDiscovered {
						verb = "added + enabled"
					}
					combined = append(combined, fmt.Sprintf("%d %s", len(added), verb))
					// Inject the newly-added groups into the local
					// cfg so the Tag phase below sees them. We
					// override the rule-overlay's RG-subset filter
					// for these specific entries — the rule didn't
					// pre-select them (couldn't, they didn't exist
					// at save-time) but the auto-add intent
					// implicitly selects them for THIS run.
					cfg.ReleaseGroups = append(cfg.ReleaseGroups, added...)
					if r.server.Scheduler != nil {
						r.server.Scheduler.Reload()
					}
				}
			}
			// Preview-mode ephemeral injection — mirrors the frontend QFA
			// chain (commit 20f866b). When the chain is in preview the
			// write-back path above is gated off, but the user still
			// expects subsequent phases to evaluate against discover's
			// findings ("show me what would happen if these groups were
			// added"). Build ephemeral ReleaseGroup entries and append
			// to the local cfg.ReleaseGroups for THIS run only — no
			// persistence, scheduler not reloaded.
			if combinedChainMode == "preview" && len(resp.Discovered) > 0 {
				seen := make(map[string]bool, len(cfg.ReleaseGroups))
				for _, g := range cfg.ReleaseGroups {
					seen[strings.ToLower(g.Tag)] = true
				}
				for i, d := range resp.Discovered {
					tag := strings.ToLower(d.Search)
					if tag == "" || seen[tag] {
						continue
					}
					seen[tag] = true
					cfg.ReleaseGroups = append(cfg.ReleaseGroups, core.ReleaseGroup{
						ID:      fmt.Sprintf("ephemeral-%d", i),
						Search:  d.Search,
						Tag:     tag,
						Display: d.Search,
						Mode:    "filtered",
						Type:    inst.Type,
						Enabled: true,
					})
				}
			}
		}
	}

	if includeRecover && phaseErr == nil {
		recoverReq := scanRunRequest{
			InstanceID:    job.InstanceID,
			Mode:          defaultMode(job.Options.RunMode, "apply"),
			Action:        "recover",
			RecoverRename: true, // Match bash RENAME=true default.
		}
		resp, apiErr := r.server.runRecover(ctx, inst, appType, recoverReq)
		r.server.auditScan("schedule:"+job.ID, "recover", inst, recoverReq, resp, errMsgOf(apiErr))
		if apiErr != nil {
			phaseErr = apiErr
		} else {
			combined = append(combined, summarizeRecoverResponse(resp).Summary)
			combinedResult.Recover = resp
		}
	}

	if includeTag && phaseErr == nil {
		tagReq := scanRunRequest{
			InstanceID:        job.InstanceID,
			Mode:              defaultMode(job.Options.RunMode, "apply"),
			Action:            "tag",
			RunGroups:         job.Options.RunForGroups,
			CleanupUnusedTags: job.Options.CleanupUnusedTags,
		}
		if job.Options.SyncToSecondary {
			tagReq.SyncToInstanceID = resolveSyncTarget(cfg, inst, appType, job.Options.SyncToInstanceID)
		}
		resp, apiErr := r.server.runTag(ctx, cfg, inst, appType, filterCfg, tagReq)
		r.server.auditScan("schedule:"+job.ID, "tag", inst, tagReq, resp, errMsgOf(apiErr))
		if apiErr != nil {
			phaseErr = apiErr
		} else {
			combined = append(combined, summarizeTagResponse(resp).Summary)
			combinedResult.Tag = resp
		}
	}

	if includeAudioTags && phaseErr == nil {
		req := scanRunRequest{
			InstanceID: job.InstanceID,
			Mode:       defaultMode(job.Options.RunMode, "apply"),
			Action:     "audiotags",
		}
		resp, apiErr := r.server.runAudioTags(ctx, cfg, inst, appType, req)
		r.server.auditScan("schedule:"+job.ID, "audiotags", inst, req, resp, errMsgOf(apiErr))
		if apiErr != nil {
			phaseErr = apiErr
		} else {
			combined = append(combined, summarizeAutoTagsResponse(resp, "audio tags").Summary)
			combinedResult.AudioTags = resp
		}
		// Optional second audio scan against secondary (independent —
		// NOT a mirror; secondary's own mediaInfo drives its tags).
		if phaseErr == nil && job.Options.AutoTagsRunOnSecondary && job.Options.SyncToSecondary {
			if resp2, secInst := r.runAutoTagOnSecondary(ctx, cfg, inst, appType, job, "audiotags"); resp2 != nil {
				combined = append(combined, "audiotags ("+secInst.Name+"): "+summarizeAutoTagsResponse(resp2, "audio tags").Summary)
				combinedResult.AudioTagsSecondary = resp2
			} else if secInst == nil {
				// Resolution failed silently — surfaces in the row
				// summary as missing block; not a hard error.
			}
		}
	}

	if includeVideoTags && phaseErr == nil {
		req := scanRunRequest{
			InstanceID: job.InstanceID,
			Mode:       defaultMode(job.Options.RunMode, "apply"),
			Action:     "videotags",
		}
		resp, apiErr := r.server.runVideoTags(ctx, cfg, inst, appType, req)
		r.server.auditScan("schedule:"+job.ID, "videotags", inst, req, resp, errMsgOf(apiErr))
		if apiErr != nil {
			phaseErr = apiErr
		} else {
			combined = append(combined, summarizeAutoTagsResponse(resp, "video tags").Summary)
			combinedResult.VideoTags = resp
		}
		if phaseErr == nil && job.Options.AutoTagsRunOnSecondary && job.Options.SyncToSecondary {
			if resp2, secInst := r.runAutoTagOnSecondary(ctx, cfg, inst, appType, job, "videotags"); resp2 != nil {
				combined = append(combined, "videotags ("+secInst.Name+"): "+summarizeAutoTagsResponse(resp2, "video tags").Summary)
				combinedResult.VideoTagsSecondary = resp2
			}
		}
	}

	if includeDvDetail && phaseErr == nil {
		req := scanRunRequest{
			InstanceID:    job.InstanceID,
			Mode:          defaultMode(job.Options.RunMode, "apply"),
			Action:        "dvdetail",
			BypassDvCache: job.Options.BypassDvCache,
		}
		resp, apiErr := r.server.runDvDetail(ctx, cfg, inst, appType, req)
		r.server.auditScan("schedule:"+job.ID, "dvdetail", inst, req, resp, errMsgOf(apiErr))
		if apiErr != nil {
			phaseErr = apiErr
		} else {
			combined = append(combined, summarizeDvDetailResponse(resp).Summary)
			combinedResult.DvDetail = resp
		}
	}

	if phaseErr != nil {
		// Partial-result error path — return the error AND any phase
		// that did succeed so the notification embed shows both the
		// completed phase's totals and the failed-phase error message.
		out := core.RunSummary{
			Status:  "error",
			Summary: strings.Join(combined, "; "),
		}
		if combinedScheduleHasAnyResult(combinedResult) {
			out.Result = combinedResult
		}
		return out, phaseErr
	}

	out := core.RunSummary{
		Status:  "ok",
		Summary: strings.Join(combined, "; "),
	}
	// Only set Result when at least one branch produced a response.
	// Otherwise writeRunResultFile would persist `{}` to disk because
	// its nil-check catches `interface{}(nil)` but not zero-value
	// structs. Today this is unreachable (validation gate above errors
	// out when no mode is enabled), but cheap defensive guard.
	if combinedScheduleHasAnyResult(combinedResult) {
		out.Result = combinedResult
	}
	return out, nil
}

// combinedScheduleResult shapes the persisted JSON for combined runs
// so the frontend can hydrate scanResults.{tag,discover,recover,
// audioTags,videoTags,dvDetail} from one history entry. Any field
// may be nil if that mode wasn't in CombinedModes.
//
// *Secondary slots are populated only when the rule opted into
// running auto-tags independently against the secondary instance
// (Options.AutoTagsRunOnSecondary). Each is its own scan against
// the secondary's mediaInfo; not a TmdbID-mirror.
type combinedScheduleResult struct {
	Tag                *scanResponse `json:"tag,omitempty"`
	Discover           *scanResponse `json:"discover,omitempty"`
	Recover            *scanResponse `json:"recover,omitempty"`
	AudioTags          *scanResponse `json:"audioTags,omitempty"`
	AudioTagsSecondary *scanResponse `json:"audioTagsSecondary,omitempty"`
	VideoTags          *scanResponse `json:"videoTags,omitempty"`
	VideoTagsSecondary *scanResponse `json:"videoTagsSecondary,omitempty"`
	DvDetail           *scanResponse `json:"dvDetail,omitempty"`
}

// combinedScheduleHasAnyResult returns true when at least one phase
// produced a response. Used to gate the Result field — saves an
// empty `{}` from being persisted to the run-result JSON file.
func combinedScheduleHasAnyResult(r combinedScheduleResult) bool {
	return r.Tag != nil || r.Discover != nil || r.Recover != nil ||
		r.AudioTags != nil || r.AudioTagsSecondary != nil ||
		r.VideoTags != nil || r.VideoTagsSecondary != nil ||
		r.DvDetail != nil
}

// runAutoTagOnSecondary fires runAudioTags/runVideoTags against the
// resolved secondary instance. Returns nil response when:
//   - SyncToSecondary isn't enabled (no target picker)
//   - secondary instance can't be resolved (deleted / wrong type)
//   - phase action isn't audiotags / videotags
//
// The secInst pointer is returned so the caller can include the
// secondary's name in the summary line. nil/nil means "skipped
// silently"; non-nil-resp means "scanned successfully".
func (r *schedulerRunner) runAutoTagOnSecondary(ctx context.Context, cfg core.Config, inst *core.Instance, appType string, job core.ScheduledJob, action string) (*scanResponse, *core.Instance) {
	secID := resolveSyncTarget(cfg, inst, appType, job.Options.SyncToInstanceID)
	if secID == "" {
		return nil, nil
	}
	var secInst *core.Instance
	for i := range cfg.Instances {
		if cfg.Instances[i].ID == secID {
			secInst = &cfg.Instances[i]
			break
		}
	}
	if secInst == nil {
		return nil, nil
	}
	req := scanRunRequest{
		InstanceID: secID,
		Mode:       defaultMode(job.Options.RunMode, "apply"),
		Action:     action,
	}
	switch action {
	case "audiotags":
		resp, apiErr := r.server.runAudioTags(ctx, cfg, secInst, appType, req)
		r.server.auditScan("schedule:"+job.ID, "audiotags", secInst, req, resp, errMsgOf(apiErr))
		if apiErr != nil {
			return nil, secInst
		}
		return resp, secInst
	case "videotags":
		resp, apiErr := r.server.runVideoTags(ctx, cfg, secInst, appType, req)
		r.server.auditScan("schedule:"+job.ID, "videotags", secInst, req, resp, errMsgOf(apiErr))
		if apiErr != nil {
			return nil, secInst
		}
		return resp, secInst
	}
	return nil, secInst
}

// defaultMode lets JobOptions.RunMode override the per-action default
// without forcing every schedule-creator to specify it.
func defaultMode(have, fallback string) string {
	if have == "preview" || have == "apply" {
		return have
	}
	return fallback
}

// summarizeTagResponse builds a one-line summary of a tag-mode run.
// Status mapping: "ok" when applied + cleanup completed without errors,
// "partial" when secondary differs from primary in completion (future
// surface — v1 always reports ok unless the run errored).
func summarizeTagResponse(resp *scanResponse) core.RunSummary {
	if resp == nil {
		return core.RunSummary{Status: "error", Summary: "nil response"}
	}
	parts := []string{}
	if resp.Applied != nil {
		parts = append(parts, fmt.Sprintf("%d added", resp.Applied.ItemsAdded))
		parts = append(parts, fmt.Sprintf("%d removed", resp.Applied.ItemsRemoved))
		if len(resp.Applied.TagsCreated) > 0 {
			parts = append(parts, fmt.Sprintf("%d tag%s created", len(resp.Applied.TagsCreated), plural(len(resp.Applied.TagsCreated))))
		}
		if len(resp.Applied.TagsDeleted) > 0 {
			parts = append(parts, fmt.Sprintf("%d tag%s deleted", len(resp.Applied.TagsDeleted), plural(len(resp.Applied.TagsDeleted))))
		}
		if resp.Applied.Secondary != nil {
			parts = append(parts, fmt.Sprintf("secondary: %d added, %d removed", resp.Applied.Secondary.ItemsAdded, resp.Applied.Secondary.ItemsRemoved))
		}
	} else {
		// Preview-mode (RunMode = "preview"): report would-do counts.
		parts = append(parts, fmt.Sprintf("preview: %d add, %d remove", resp.Totals.ToAdd, resp.Totals.ToRemove))
	}
	return core.RunSummary{
		Status:  "ok",
		Summary: strings.Join(parts, ", "),
	}
}

// summarizeAutoTagsResponse builds a one-line summary of an
// audiotags / videotags scan run. label is the human-friendly
// prefix ("audio tags" / "video tags") so the summary reads
// naturally in mixed combined-mode reports. Verb tense flips per
// mode — same idiom as summarizeTagResponse.
func summarizeAutoTagsResponse(resp *scanResponse, label string) core.RunSummary {
	if resp == nil {
		return core.RunSummary{Status: "error", Summary: "nil response"}
	}
	parts := []string{}
	if resp.Applied != nil {
		parts = append(parts, fmt.Sprintf("%s: %d added", label, resp.Applied.ItemsAdded))
		parts = append(parts, fmt.Sprintf("%d removed", resp.Applied.ItemsRemoved))
		if len(resp.Applied.TagsCreated) > 0 {
			parts = append(parts, fmt.Sprintf("%d tag%s created", len(resp.Applied.TagsCreated), plural(len(resp.Applied.TagsCreated))))
		}
	} else {
		parts = append(parts, fmt.Sprintf("%s preview: %d add, %d remove", label, resp.Totals.ToAdd, resp.Totals.ToRemove))
	}
	if resp.Totals.MissingMediaInfo > 0 {
		parts = append(parts, fmt.Sprintf("%d missing-mediainfo", resp.Totals.MissingMediaInfo))
	}
	return core.RunSummary{
		Status:  "ok",
		Summary: strings.Join(parts, ", "),
	}
}

// summarizeDvDetailResponse builds a one-line summary of a dvdetail run.
// Surfaces the run-shape numbers most relevant for "did this work":
// candidate count, cache hit rate, extraction outcomes (success / no-rpu
// / failed). Apply-mode also reports actual writes; preview reports
// would-do counts. Status flips to "partial" when any extraction
// failed — a single failure isn't enough to abort the run, but the
// user should see it so they know to investigate (corrupted file?
// tools mid-uninstall?).
func summarizeDvDetailResponse(resp *scanResponse) core.RunSummary {
	if resp == nil {
		return core.RunSummary{Status: "error", Summary: "nil response"}
	}
	t := resp.Totals
	status := "ok"
	// "partial" when any per-row failure happened. tools-missing
	// counts here too — if tools went away mid-scan (uninstall fired
	// from another goroutine), every subsequent row gets a tools-
	// missing status. Without surfacing it the schedule would
	// silently report "ok" when nothing actually got extracted.
	if t.DvExtractFailed > 0 || t.DvFileUnreachable > 0 || t.DvToolsMissing > 0 {
		status = "partial"
	}
	parts := []string{
		fmt.Sprintf("DV: %d candidate%s", t.DvCandidates, plural(t.DvCandidates)),
	}
	if t.DvCacheHits > 0 {
		parts = append(parts, fmt.Sprintf("%d cached", t.DvCacheHits))
	}
	if t.DvExtracted > 0 {
		parts = append(parts, fmt.Sprintf("%d extracted", t.DvExtracted))
	}
	if t.DvExtractedNoRpu > 0 {
		parts = append(parts, fmt.Sprintf("%d no-rpu", t.DvExtractedNoRpu))
	}
	if resp.Applied != nil {
		parts = append(parts, fmt.Sprintf("%d added", resp.Applied.ItemsAdded))
		parts = append(parts, fmt.Sprintf("%d removed", resp.Applied.ItemsRemoved))
		if len(resp.Applied.TagsCreated) > 0 {
			parts = append(parts, fmt.Sprintf("%d tag%s created", len(resp.Applied.TagsCreated), plural(len(resp.Applied.TagsCreated))))
		}
	} else {
		parts = append(parts, fmt.Sprintf("preview: %d add, %d remove", t.ToAdd, t.ToRemove))
	}
	if t.DvExtractFailed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", t.DvExtractFailed))
	}
	if t.DvFileUnreachable > 0 {
		parts = append(parts, fmt.Sprintf("%d unreachable", t.DvFileUnreachable))
	}
	if t.DvToolsMissing > 0 {
		parts = append(parts, fmt.Sprintf("%d tools-missing", t.DvToolsMissing))
	}
	return core.RunSummary{
		Status:  status,
		Summary: strings.Join(parts, ", "),
	}
}

// summarizeRecoverResponse builds a one-line summary of a recover-mode run.
func summarizeRecoverResponse(resp *scanResponse) core.RunSummary {
	if resp == nil {
		return core.RunSummary{Status: "error", Summary: "nil response"}
	}
	t := resp.Totals
	status := "ok"
	if t.RecoverFixFailed > 0 {
		status = "partial"
	}
	parts := []string{}
	if t.RecoverFixed > 0 {
		parts = append(parts, fmt.Sprintf("%d fixed", t.RecoverFixed))
	}
	if t.RecoverWouldFix > 0 {
		parts = append(parts, fmt.Sprintf("%d would-fix (preview)", t.RecoverWouldFix))
	}
	if t.RecoverFlagged > 0 {
		parts = append(parts, fmt.Sprintf("%d flagged", t.RecoverFlagged))
	}
	if t.RecoverFixFailed > 0 {
		parts = append(parts, fmt.Sprintf("%d fix-failed", t.RecoverFixFailed))
	}
	if len(parts) == 0 {
		parts = append(parts, "no movies needed recovery")
	}
	return core.RunSummary{
		Status:  status,
		Summary: strings.Join(parts, ", "),
	}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
