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
	// Per-mode allowlist mirrors scan.go's dispatcher. Sonarr supports
	// recover (M3c) + audiotags + videotags (M-Sonarr) + combined chains
	// over those three. Tag, discover, dvdetail are Radarr-only — the
	// wizard catalog's appliesTo gates prevent saving Sonarr rules with
	// those modes, but defend at the schedule entry-point too in case a
	// hand-edited resolvarr.json sneaks one through.
	if appType == "sonarr" {
		switch job.Mode {
		case core.JobModeRecover, core.JobModeAudioTags, core.JobModeVideoTags, core.JobModeMissingEpisodes, core.JobModePlexSync, core.JobModeTbaRefresh, core.JobModeCombined:
			// supported
		default:
			return core.RunSummary{}, fmt.Errorf("Sonarr schedules support recover / audiotags / videotags / missingepisodes / plexsync / tbarefresh / combined only — got %q", job.Mode)
		}
	} else if appType != "radarr" {
		return core.RunSummary{}, fmt.Errorf("schedule against unknown instance type %q", appType)
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
	case core.JobModeMissingEpisodes:
		summary, runErr = r.runMissingEpisodesSchedule(ctx, inst, appType, job)
	case core.JobModePlexSync:
		summary, runErr = r.runPlexSyncSchedule(ctx, cfg, inst, appType, job)
	case core.JobModeTbaRefresh:
		summary, runErr = r.runTbaRefreshSchedule(ctx, inst, appType, job)
	case core.JobModeQbitSeTag:
		summary, runErr = r.runQbitSeSchedule(ctx, cfg, inst, appType, job)
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
		TagSource:         job.Options.TagSource,
		FilterOnlyTag:     job.Options.FilterOnlyTag,
	}
	if job.Options.SyncToSecondary {
		req.SyncToInstanceID = resolveSyncTarget(cfg, inst, appType, job.Options.SyncToInstanceID)
	}
	// Filter-only branches to runTagFilterOnly; everything else stays
	// on the per-group runTag path. Same dispatch as the live HTTP
	// handler at scan_tag.go:handleScanTag.
	var (
		resp   *scanResponse
		apiErr *apiError
	)
	if req.TagSource == "filter-only" {
		resp, apiErr = r.server.runTagFilterOnly(ctx, cfg, inst, appType, filterCfg, req)
	} else {
		resp, apiErr = r.server.runTag(ctx, cfg, inst, appType, filterCfg, req)
	}
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
	target := core.ValidJobTarget(job.Options.AudioTagsTarget)
	mode := defaultMode(job.Options.RunMode, "apply")

	var primaryResp *scanResponse
	if target.IncludesPrimary() {
		req := scanRunRequest{InstanceID: job.InstanceID, Mode: mode, Action: "audiotags"}
		resp, apiErr := r.server.runAudioTags(ctx, cfg, inst, appType, req)
		r.server.auditScan("schedule:"+job.ID, "audiotags", inst, req, resp, errMsgOf(apiErr))
		if apiErr != nil {
			return core.RunSummary{}, apiErr
		}
		primaryResp = resp
	}

	if target.IncludesSecondary() {
		resp2, secInst := r.runAutoTagOnSecondary(ctx, cfg, inst, appType, job, "audiotags")
		if resp2 != nil {
			combined := combinedScheduleResult{AudioTags: primaryResp, AudioTagsSecondary: resp2}
			parts := []string{}
			if primaryResp != nil {
				parts = append(parts, "audiotags ("+inst.Name+"): "+summarizeAutoTagsResponse(primaryResp, "audio tags").Summary)
			}
			parts = append(parts, "audiotags ("+secInst.Name+"): "+summarizeAutoTagsResponse(resp2, "audio tags").Summary)
			return core.RunSummary{Status: "ok", Summary: strings.Join(parts, "; "), Result: combined}, nil
		}
	}

	if primaryResp == nil {
		return core.RunSummary{Status: "error", Summary: "audiotags target=secondary but no secondary instance reachable"},
			fmt.Errorf("audiotags: target=secondary but secondary unreachable for schedule %s", job.ID)
	}
	out := summarizeAutoTagsResponse(primaryResp, "audio tags")
	out.Result = primaryResp
	return out, nil
}

// runVideoTagsSchedule — same shape, video-stream side.
func (r *schedulerRunner) runVideoTagsSchedule(ctx context.Context, cfg core.Config, inst *core.Instance, appType string, job core.ScheduledJob) (core.RunSummary, error) {
	target := core.ValidJobTarget(job.Options.VideoTagsTarget)
	mode := defaultMode(job.Options.RunMode, "apply")

	var primaryResp *scanResponse
	if target.IncludesPrimary() {
		req := scanRunRequest{InstanceID: job.InstanceID, Mode: mode, Action: "videotags"}
		resp, apiErr := r.server.runVideoTags(ctx, cfg, inst, appType, req)
		r.server.auditScan("schedule:"+job.ID, "videotags", inst, req, resp, errMsgOf(apiErr))
		if apiErr != nil {
			return core.RunSummary{}, apiErr
		}
		primaryResp = resp
	}

	if target.IncludesSecondary() {
		resp2, secInst := r.runAutoTagOnSecondary(ctx, cfg, inst, appType, job, "videotags")
		if resp2 != nil {
			combined := combinedScheduleResult{VideoTags: primaryResp, VideoTagsSecondary: resp2}
			parts := []string{}
			if primaryResp != nil {
				parts = append(parts, "videotags ("+inst.Name+"): "+summarizeAutoTagsResponse(primaryResp, "video tags").Summary)
			}
			parts = append(parts, "videotags ("+secInst.Name+"): "+summarizeAutoTagsResponse(resp2, "video tags").Summary)
			return core.RunSummary{Status: "ok", Summary: strings.Join(parts, "; "), Result: combined}, nil
		}
	}

	if primaryResp == nil {
		return core.RunSummary{Status: "error", Summary: "videotags target=secondary but no secondary instance reachable"},
			fmt.Errorf("videotags: target=secondary but secondary unreachable for schedule %s", job.ID)
	}
	out := summarizeAutoTagsResponse(primaryResp, "video tags")
	out.Result = primaryResp
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
	target := core.ValidJobTarget(job.Options.DvDetailTarget)
	mode := defaultMode(job.Options.RunMode, "apply")

	// A-chain: primary instance.
	var primaryResp *scanResponse
	if target.IncludesPrimary() {
		req := scanRunRequest{
			InstanceID:    job.InstanceID,
			Mode:          mode,
			Action:        "dvdetail",
			BypassDvCache: job.Options.BypassDvCache,
		}
		resp, apiErr := r.server.runDvDetail(ctx, cfg, inst, appType, req)
		r.server.auditScan("schedule:"+job.ID, "dvdetail", inst, req, resp, errMsgOf(apiErr))
		if apiErr != nil {
			return core.RunSummary{}, apiErr
		}
		primaryResp = resp
	}

	// B-chain: secondary. Single-mode dvdetail rules with target=both
	// or 'secondary' aggregate the secondary into a combined-shape
	// result so the schedule's history modal renders both halves via
	// the existing combined-result drill-in. Single-instance flow
	// (target=primary only) keeps the legacy raw scanResponse shape
	// so existing rule history isn't reformatted.
	if target.IncludesSecondary() {
		resp2, secInst := r.runAutoTagOnSecondary(ctx, cfg, inst, appType, job, "dvdetail")
		if resp2 != nil {
			combined := combinedScheduleResult{
				DvDetail:          primaryResp,
				DvDetailSecondary: resp2,
			}
			parts := []string{}
			if primaryResp != nil {
				parts = append(parts, "dvdetail ("+inst.Name+"): "+summarizeDvDetailResponse(primaryResp).Summary)
			}
			parts = append(parts, "dvdetail ("+secInst.Name+"): "+summarizeDvDetailResponse(resp2).Summary)
			return core.RunSummary{
				Status:  "ok",
				Summary: strings.Join(parts, "; "),
				Result:  combined,
			}, nil
		}
		// Secondary couldn't be resolved; fall through to primary-only
		// summary if we ran one.
	}

	if primaryResp == nil {
		// target was 'secondary' but secondary unreachable, OR target
		// was 'primary' but the IncludesPrimary branch above caught
		// the apiErr. The first case lands here.
		return core.RunSummary{Status: "error", Summary: "DV detail target=secondary but no secondary instance reachable"},
			fmt.Errorf("dvdetail: target=secondary but secondary unreachable for schedule %s", job.ID)
	}
	out := summarizeDvDetailResponse(primaryResp)
	out.Result = primaryResp
	return out, nil
}

// runMissingEpisodesSchedule runs the standalone missing-episodes phase
// (Sonarr only). Same logic as the missingepisodes branch in
// runCombinedSchedule but as its own JobMode for users who want only
// this one phase scheduled. Reads snapshot off job.MissingEpisodes;
// falls back to sensible defaults when nil (legacy schedule with the
// mode set via API but no per-rule snapshot persisted).
func (r *schedulerRunner) runMissingEpisodesSchedule(ctx context.Context, inst *core.Instance, appType string, job core.ScheduledJob) (core.RunSummary, error) {
	if appType != "sonarr" {
		return core.RunSummary{}, fmt.Errorf("missingepisodes schedule requires Sonarr instance, got %s", appType)
	}
	meCfg := job.MissingEpisodes
	if meCfg == nil {
		meCfg = &core.MissingEpisodesConfig{
			ThresholdPercent:  70,
			BufferHours:       24,
			IncludeContinuing: true,
			IncludeEnded:      true,
		}
	}
	threshold := float64(meCfg.ThresholdPercent) / 100.0
	if threshold == 0 {
		threshold = 0.7
	}
	previewResp, apiErr := r.server.runMissingEpisodesPreview(
		ctx, inst,
		threshold, meCfg.BufferHours,
		meCfg.IncludeContinuing, meCfg.IncludeEnded, meCfg.IncludeSpecials,
	)
	r.server.auditScan("schedule:"+job.ID, "missingepisodes", inst, scanRunRequest{InstanceID: job.InstanceID, Action: "missingepisodes"}, nil, errMsgOf(apiErr))
	if apiErr != nil {
		return core.RunSummary{Status: "error"}, apiErr
	}
	parts := []string{
		fmt.Sprintf("scanned %d", previewResp.SeriesScanned),
		fmt.Sprintf("%d with gaps", previewResp.SeriesWithGaps),
		fmt.Sprintf("%d missing episodes", previewResp.TotalMissingEpisodes),
	}
	chainMode := defaultMode(job.Options.RunMode, "apply")
	if chainMode == "apply" && previewResp.SeriesWithGaps > 0 {
		if meCfg.ActionTag {
			seriesIDs := make([]int, 0, len(previewResp.Series))
			for _, s := range previewResp.Series {
				seriesIDs = append(seriesIDs, s.SeriesID)
			}
			tagResp, tagErr := r.server.runMissingEpisodesTag(ctx, inst, meCfg.TagName, seriesIDs, true)
			if tagErr != nil {
				parts = append(parts, "tag failed: "+tagErr.Message)
			} else {
				parts = append(parts, fmt.Sprintf("tagged %d / removed %d", tagResp.Applied, tagResp.Removed))
			}
		}
		if meCfg.ActionSearch {
			episodeIDs := make([]int, 0)
			for _, s := range previewResp.Series {
				for _, season := range s.Seasons {
					for _, ep := range season.MissingEpisodes {
						episodeIDs = append(episodeIDs, ep.EpisodeID)
					}
				}
			}
			const searchChunkSize = 500
			triggered := 0
			var searchErrors int
			for i := 0; i < len(episodeIDs); i += searchChunkSize {
				end := i + searchChunkSize
				if end > len(episodeIDs) {
					end = len(episodeIDs)
				}
				_, searchErr := r.server.runMissingEpisodesSearch(ctx, inst, episodeIDs[i:end])
				if searchErr != nil {
					searchErrors++
				} else {
					triggered += end - i
				}
			}
			if searchErrors > 0 {
				parts = append(parts, fmt.Sprintf("searched %d (%d chunk errors)", triggered, searchErrors))
			} else if triggered > 0 {
				parts = append(parts, fmt.Sprintf("searched %d", triggered))
			}
		}
	}
	return core.RunSummary{
		Status:  "ok",
		Summary: strings.Join(parts, ", "),
		Result:  previewResp,
	}, nil
}

// runPlexSyncSchedule runs the standalone plexsync phase (Radarr or
// Sonarr). Reads the inline config off job.PlexSync and fires the bulk
// engine via the shared runPlexSyncFromConfig path — same code the
// one-off /api/plex-sync/run endpoint uses. Config-resolve failures
// surface as a returned error; engine-level problems come back inside
// the run (status "error") and are mapped onto the summary.
func (r *schedulerRunner) runPlexSyncSchedule(ctx context.Context, cfg core.Config, inst *core.Instance, appType string, job core.ScheduledJob) (core.RunSummary, error) {
	if job.PlexSync == nil {
		return core.RunSummary{Status: "error"}, fmt.Errorf("plexsync schedule has no Plex sync config")
	}
	runMode := defaultMode(job.Options.RunMode, "apply")
	run, err := r.server.runPlexSyncFromConfig(ctx, cfg, job.PlexSync, job.InstanceID, "scheduled", runMode)
	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	}
	r.server.auditScan("schedule:"+job.ID, "plexsync", inst, scanRunRequest{InstanceID: job.InstanceID, Action: "plexsync"}, nil, errMsg)
	if err != nil {
		return core.RunSummary{Status: "error"}, err
	}
	status := run.Status
	if status == "" {
		status = "ok"
	}
	return core.RunSummary{
		Status:  status,
		Summary: run.Summary,
		Result:  run,
	}, nil
}

// runQbitSeSchedule runs the standalone qbitsetag phase (Sonarr only):
// tag every torrent in the configured qBit instance by Season /
// Episode / Unmatched. Reuses runQbitSeScanWithRules — the same code
// the one-off run + the webhook backlog button use. Apply mode tags
// every taggable torrent (no per-row selection in the automated path);
// preview mode reports the plan only.
func (r *schedulerRunner) runQbitSeSchedule(ctx context.Context, cfg core.Config, inst *core.Instance, appType string, job core.ScheduledJob) (core.RunSummary, error) {
	if appType != "sonarr" {
		return core.RunSummary{Status: "error"}, fmt.Errorf("qbitsetag schedule requires Sonarr instance, got %s", appType)
	}
	if job.QbitSe == nil {
		return core.RunSummary{Status: "error"}, fmt.Errorf("qbitsetag schedule has no qBit S/E config")
	}
	apply := defaultMode(job.Options.RunMode, "apply") == "apply"
	res, apiErr := r.server.runQbitSeScanWithRules(ctx, cfg, job.QbitSe, "", nil, apply)
	r.server.auditScan("schedule:"+job.ID, "qbitsetag", inst, scanRunRequest{InstanceID: job.InstanceID, Action: "qbitsetag"}, nil, errMsgOf(apiErr))
	if apiErr != nil {
		return core.RunSummary{Status: "error"}, apiErr
	}
	status := "ok"
	summary := ""
	switch v := res.(type) {
	case qbitSeBacklogApplyResponse:
		summary = fmt.Sprintf("tagged %d torrent(s)", v.Applied)
		if v.Failed > 0 {
			status = "partial"
			summary += fmt.Sprintf(", %d failed", v.Failed)
		}
	case qbitSeBacklogPreviewResponse:
		summary = fmt.Sprintf("preview: %d taggable, %d already tagged", v.TotalTaggable, v.TotalAlreadyOK)
	}
	return core.RunSummary{Status: status, Summary: summary, Result: res}, nil
}

// runTbaRefreshSchedule runs the standalone tbarefresh phase (Sonarr
// only). Preview finds TBA files for the configured series filters;
// in apply mode it then renames EVERY file found (no per-file
// selection in the automated path) fire-and-forget per series.
func (r *schedulerRunner) runTbaRefreshSchedule(ctx context.Context, inst *core.Instance, appType string, job core.ScheduledJob) (core.RunSummary, error) {
	if appType != "sonarr" {
		return core.RunSummary{Status: "error"}, fmt.Errorf("tbarefresh schedule requires Sonarr instance, got %s", appType)
	}
	cfg := job.TbaRefresh
	if cfg == nil {
		cfg = &core.TbaRefreshConfig{IncludeContinuing: true, IncludeEnded: true}
	}
	preview, apiErr := r.server.runTbaRefreshPreview(ctx, inst, cfg.IncludeContinuing, cfg.IncludeEnded, cfg.IncludeSpecials)
	r.server.auditScan("schedule:"+job.ID, "tbarefresh", inst, scanRunRequest{InstanceID: job.InstanceID, Action: "tbarefresh"}, nil, errMsgOf(apiErr))
	if apiErr != nil {
		return core.RunSummary{Status: "error"}, apiErr
	}
	parts := []string{fmt.Sprintf("%d TBA files across %d series", preview.TotalFiles, preview.SeriesWithTba)}
	chainMode := defaultMode(job.Options.RunMode, "apply")
	if chainMode == "apply" && preview.TotalFiles > 0 {
		groups := make([]tbaRefreshApplyGroup, 0, len(preview.Series))
		for _, ser := range preview.Series {
			ids := make([]int, 0, len(ser.Files))
			for _, f := range ser.Files {
				ids = append(ids, f.EpisodeFileID)
			}
			groups = append(groups, tbaRefreshApplyGroup{SeriesID: ser.SeriesID, FileIDs: ids})
		}
		applyResp, applyErr := r.server.runTbaRefreshApply(ctx, inst, groups)
		if applyErr != nil {
			parts = append(parts, "rename failed: "+applyErr.Message)
		} else {
			parts = append(parts, fmt.Sprintf("queued %d renames", applyResp.Queued))
			if len(applyResp.Errors) > 0 {
				parts = append(parts, fmt.Sprintf("%d series failed", len(applyResp.Errors)))
			}
		}
	}
	return core.RunSummary{
		Status:  "ok",
		Summary: strings.Join(parts, ", "),
		Result:  preview,
	}, nil
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
	includeMissingEpisodes := false
	includePlexSync := false
	includeTbaRefresh := false
	includeQbitSe := false
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
		case core.JobModeMissingEpisodes:
			// Sonarr-only — gated below by appType check. Snapshot
			// config lives on job.MissingEpisodes.
			if appType == "sonarr" {
				includeMissingEpisodes = true
			}
		case core.JobModePlexSync:
			// Runs LAST in the chain (after every tag-writing phase) so
			// Plex reads the final Arr-side tag state. Snapshot config
			// lives on job.PlexSync; gated below to require it.
			if job.PlexSync != nil {
				includePlexSync = true
			}
		case core.JobModeTbaRefresh:
			// Sonarr-only file-rename phase. Independent of the tag
			// phases. Snapshot config lives on job.TbaRefresh.
			if appType == "sonarr" {
				includeTbaRefresh = true
			}
		case core.JobModeQbitSeTag:
			// Sonarr-only qBit-side phase — tags torrents in qBit (not Arr
			// items). Snapshot config lives on job.QbitSe.
			if appType == "sonarr" && job.QbitSe != nil {
				includeQbitSe = true
			}
		}
	}
	if !includeDiscover && !includeRecover && !includeTag &&
		!includeAudioTags && !includeVideoTags && !includeDvDetail && !includeMissingEpisodes && !includePlexSync && !includeTbaRefresh && !includeQbitSe {
		return core.RunSummary{}, fmt.Errorf("combined schedule must include at least one of discover/recover/tag/audiotags/videotags/dvdetail/missingepisodes/plexsync/tbarefresh/qbitsetag")
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
			TagSource:         job.Options.TagSource,
			FilterOnlyTag:     job.Options.FilterOnlyTag,
		}
		if job.Options.SyncToSecondary {
			tagReq.SyncToInstanceID = resolveSyncTarget(cfg, inst, appType, job.Options.SyncToInstanceID)
		}
		// Same filter-only branch as runTagSchedule.
		var (
			resp   *scanResponse
			apiErr *apiError
		)
		if tagReq.TagSource == "filter-only" {
			resp, apiErr = r.server.runTagFilterOnly(ctx, cfg, inst, appType, filterCfg, tagReq)
		} else {
			resp, apiErr = r.server.runTag(ctx, cfg, inst, appType, filterCfg, tagReq)
		}
		r.server.auditScan("schedule:"+job.ID, "tag", inst, tagReq, resp, errMsgOf(apiErr))
		if apiErr != nil {
			phaseErr = apiErr
		} else {
			combined = append(combined, summarizeTagResponse(resp).Summary)
			combinedResult.Tag = resp
		}
	}

	// Auto-tag phases (audiotags / videotags / dvdetail) follow the
	// "A-chain → B-chain" execution model: every phase whose target
	// includes the primary instance fires first, in fixed order
	// (audio → video → DV); then every phase whose target includes
	// the secondary instance fires, in the same order. Token allow-
	// lists are universal — the per-rule config carried in
	// AudioTags/VideoTags/DvDetail is applied to whichever instance
	// the phase fires on.
	audioTarget := core.ValidJobTarget(job.Options.AudioTagsTarget)
	videoTarget := core.ValidJobTarget(job.Options.VideoTagsTarget)
	dvTarget := core.ValidJobTarget(job.Options.DvDetailTarget)

	// A-chain — primary instance.
	if includeAudioTags && phaseErr == nil && audioTarget.IncludesPrimary() {
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
	}
	if includeVideoTags && phaseErr == nil && videoTarget.IncludesPrimary() {
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
	}
	if includeDvDetail && phaseErr == nil && dvTarget.IncludesPrimary() {
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

	// B-chain — secondary instance. Each phase resolves the secondary
	// independently via runAutoTagOnSecondary which uses
	// SyncToInstanceID (or first-of-same-type fallback). Skipped
	// silently when no secondary is reachable.
	if includeAudioTags && phaseErr == nil && audioTarget.IncludesSecondary() {
		if resp2, secInst := r.runAutoTagOnSecondary(ctx, cfg, inst, appType, job, "audiotags"); resp2 != nil {
			combined = append(combined, "audiotags ("+secInst.Name+"): "+summarizeAutoTagsResponse(resp2, "audio tags").Summary)
			combinedResult.AudioTagsSecondary = resp2
		}
	}
	if includeVideoTags && phaseErr == nil && videoTarget.IncludesSecondary() {
		if resp2, secInst := r.runAutoTagOnSecondary(ctx, cfg, inst, appType, job, "videotags"); resp2 != nil {
			combined = append(combined, "videotags ("+secInst.Name+"): "+summarizeAutoTagsResponse(resp2, "video tags").Summary)
			combinedResult.VideoTagsSecondary = resp2
		}
	}
	if includeDvDetail && phaseErr == nil && dvTarget.IncludesSecondary() {
		if resp2, secInst := r.runAutoTagOnSecondary(ctx, cfg, inst, appType, job, "dvdetail"); resp2 != nil {
			combined = append(combined, "dvdetail ("+secInst.Name+"): "+summarizeDvDetailResponse(resp2).Summary)
			combinedResult.DvDetailSecondary = resp2
		}
	}

	// Missing Episodes phase — Sonarr only. Snapshot config is read off
	// job.MissingEpisodes (per-rule snapshot, validated on save). Mirrors
	// the QFA chain-runner logic: always preview, then conditionally tag
	// + search based on the snapshot's ActionTag / ActionSearch flags.
	// Preview mode short-circuits both writes — runMode='preview' keeps
	// the whole chain read-only.
	if includeMissingEpisodes && phaseErr == nil {
		meCfg := job.MissingEpisodes
		if meCfg == nil {
			// Defensive: phase enabled but no snapshot. Treat as preview-
			// only with sensible defaults.
			meCfg = &core.MissingEpisodesConfig{
				ThresholdPercent:  70,
				BufferHours:       24,
				IncludeContinuing: true,
				IncludeEnded:      true,
			}
		}
		threshold := float64(meCfg.ThresholdPercent) / 100.0
		if threshold == 0 {
			threshold = 0.7
		}
		previewResp, apiErr := r.server.runMissingEpisodesPreview(
			ctx, inst,
			threshold, meCfg.BufferHours,
			meCfg.IncludeContinuing, meCfg.IncludeEnded, meCfg.IncludeSpecials,
		)
		r.server.auditScan("schedule:"+job.ID, "missingepisodes", inst, scanRunRequest{InstanceID: job.InstanceID, Action: "missingepisodes"}, nil, errMsgOf(apiErr))
		if apiErr != nil {
			phaseErr = apiErr
		} else {
			combined = append(combined, fmt.Sprintf("missingepisodes: scanned %d / %d with gaps / %d episodes",
				previewResp.SeriesScanned, previewResp.SeriesWithGaps, previewResp.TotalMissingEpisodes))
			combinedResult.MissingEpisodes = previewResp
			chainMode := defaultMode(job.Options.RunMode, "apply")
			if chainMode == "apply" && previewResp.SeriesWithGaps > 0 {
				if meCfg.ActionTag {
					seriesIDs := make([]int, 0, len(previewResp.Series))
					for _, s := range previewResp.Series {
						seriesIDs = append(seriesIDs, s.SeriesID)
					}
					tagResp, tagErr := r.server.runMissingEpisodesTag(ctx, inst, meCfg.TagName, seriesIDs, true)
					if tagErr != nil {
						combined = append(combined, "missingepisodes tag failed: "+tagErr.Message)
					} else {
						combined = append(combined, fmt.Sprintf("missingepisodes tag: applied %d / removed %d", tagResp.Applied, tagResp.Removed))
					}
				}
				if meCfg.ActionSearch {
					episodeIDs := make([]int, 0)
					for _, s := range previewResp.Series {
						for _, season := range s.Seasons {
							for _, ep := range season.MissingEpisodes {
								episodeIDs = append(episodeIDs, ep.EpisodeID)
							}
						}
					}
					// Sonarr SearchEpisodes is capped at 500 IDs per call.
					// Chunk so a big backlog scan doesn't error out.
					const searchChunkSize = 500
					triggered := 0
					var searchErrors int
					for i := 0; i < len(episodeIDs); i += searchChunkSize {
						end := i + searchChunkSize
						if end > len(episodeIDs) {
							end = len(episodeIDs)
						}
						_, searchErr := r.server.runMissingEpisodesSearch(ctx, inst, episodeIDs[i:end])
						if searchErr != nil {
							searchErrors++
						} else {
							triggered += end - i
						}
					}
					if searchErrors > 0 {
						combined = append(combined, fmt.Sprintf("missingepisodes search: triggered %d (errors on %d chunks)", triggered, searchErrors))
					} else if triggered > 0 {
						combined = append(combined, fmt.Sprintf("missingepisodes search: triggered %d", triggered))
					}
				}
			}
		}
	}

	// Plex sync runs LAST — after every Arr-tag-writing phase above so
	// the labels it mirrors reflect the final tag state of this run.
	// Mirrors the webhook canonicalFunctionOrder placement (Plex sync
	// after the Tag* functions).
	if includePlexSync && phaseErr == nil {
		chainMode := defaultMode(job.Options.RunMode, "apply")
		run, syncErr := r.server.runPlexSyncFromConfig(ctx, cfg, job.PlexSync, job.InstanceID, "scheduled", chainMode)
		errMsg := ""
		if syncErr != nil {
			errMsg = syncErr.Error()
		}
		r.server.auditScan("schedule:"+job.ID, "plexsync", inst, scanRunRequest{InstanceID: job.InstanceID, Action: "plexsync"}, nil, errMsg)
		if syncErr != nil {
			phaseErr = syncErr
		} else {
			combined = append(combined, "plexsync: "+run.Summary)
			combinedResult.PlexSync = &run
			// Engine-level failure (Plex write 4xx, per-item errors) comes
			// back with run.Status == "error" and a nil syncErr. Bubble it
			// into phaseErr so the combined schedule reports error/partial
			// rather than a misleading "ok" — matches runPlexSyncSchedule,
			// which maps run.Status onto the standalone RunSummary.
			if run.Status == "error" {
				phaseErr = fmt.Errorf("plexsync: %s", run.Summary)
			}
		}
	}

	// TBA refresh — Sonarr-only file rename, independent of the tag
	// phases. Apply mode renames every TBA file found.
	if includeTbaRefresh && phaseErr == nil {
		tbaCfg := job.TbaRefresh
		if tbaCfg == nil {
			tbaCfg = &core.TbaRefreshConfig{IncludeContinuing: true, IncludeEnded: true}
		}
		preview, apiErr := r.server.runTbaRefreshPreview(ctx, inst, tbaCfg.IncludeContinuing, tbaCfg.IncludeEnded, tbaCfg.IncludeSpecials)
		r.server.auditScan("schedule:"+job.ID, "tbarefresh", inst, scanRunRequest{InstanceID: job.InstanceID, Action: "tbarefresh"}, nil, errMsgOf(apiErr))
		if apiErr != nil {
			phaseErr = apiErr
		} else {
			line := fmt.Sprintf("tbarefresh: %d TBA files across %d series", preview.TotalFiles, preview.SeriesWithTba)
			if defaultMode(job.Options.RunMode, "apply") == "apply" && preview.TotalFiles > 0 {
				groups := make([]tbaRefreshApplyGroup, 0, len(preview.Series))
				for _, ser := range preview.Series {
					ids := make([]int, 0, len(ser.Files))
					for _, f := range ser.Files {
						ids = append(ids, f.EpisodeFileID)
					}
					groups = append(groups, tbaRefreshApplyGroup{SeriesID: ser.SeriesID, FileIDs: ids})
				}
				if applyResp, applyErr := r.server.runTbaRefreshApply(ctx, inst, groups); applyErr != nil {
					line += ", rename failed: " + applyErr.Message
				} else {
					line += fmt.Sprintf(", queued %d renames", applyResp.Queued)
				}
			}
			combined = append(combined, line)
			combinedResult.TbaRefresh = preview
		}
	}

	// qbitsetag runs independently of the Arr-side phases — it tags
	// torrents in qBit, not Arr items. Sonarr-only; gated above.
	if includeQbitSe && phaseErr == nil {
		apply := defaultMode(job.Options.RunMode, "apply") == "apply"
		res, apiErr := r.server.runQbitSeScanWithRules(ctx, cfg, job.QbitSe, "", nil, apply)
		r.server.auditScan("schedule:"+job.ID, "qbitsetag", inst, scanRunRequest{InstanceID: job.InstanceID, Action: "qbitsetag"}, nil, errMsgOf(apiErr))
		if apiErr != nil {
			phaseErr = apiErr
		} else {
			switch v := res.(type) {
			case qbitSeBacklogApplyResponse:
				line := fmt.Sprintf("qbitsetag: tagged %d torrent(s)", v.Applied)
				if v.Failed > 0 {
					line += fmt.Sprintf(", %d failed", v.Failed)
				}
				combined = append(combined, line)
				combinedResult.QbitSe = v
			case qbitSeBacklogPreviewResponse:
				combined = append(combined, fmt.Sprintf("qbitsetag: %d taggable, %d already tagged (preview)", v.TotalTaggable, v.TotalAlreadyOK))
				combinedResult.QbitSe = v
			}
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
	Tag                *scanResponse                  `json:"tag,omitempty"`
	Discover           *scanResponse                  `json:"discover,omitempty"`
	Recover            *scanResponse                  `json:"recover,omitempty"`
	AudioTags          *scanResponse                  `json:"audioTags,omitempty"`
	AudioTagsSecondary *scanResponse                  `json:"audioTagsSecondary,omitempty"`
	VideoTags          *scanResponse                  `json:"videoTags,omitempty"`
	VideoTagsSecondary *scanResponse                  `json:"videoTagsSecondary,omitempty"`
	DvDetail           *scanResponse                  `json:"dvDetail,omitempty"`
	DvDetailSecondary  *scanResponse                  `json:"dvDetailSecondary,omitempty"`
	MissingEpisodes    *missingEpisodesPreviewResponse `json:"missingEpisodes,omitempty"`
	PlexSync           *core.PlexLabelRuleRun          `json:"plexSync,omitempty"`
	TbaRefresh         *tbaRefreshPreviewResponse      `json:"tbaRefresh,omitempty"`
	QbitSe             any                             `json:"qbitSe,omitempty"` // qbitSeBacklogApplyResponse (apply) or qbitSeBacklogPreviewResponse (preview)
}

// combinedScheduleHasAnyResult returns true when at least one phase
// produced a response. Used to gate the Result field — saves an
// empty `{}` from being persisted to the run-result JSON file.
func combinedScheduleHasAnyResult(r combinedScheduleResult) bool {
	return r.Tag != nil || r.Discover != nil || r.Recover != nil ||
		r.AudioTags != nil || r.AudioTagsSecondary != nil ||
		r.VideoTags != nil || r.VideoTagsSecondary != nil ||
		r.DvDetail != nil || r.DvDetailSecondary != nil ||
		r.MissingEpisodes != nil || r.PlexSync != nil || r.TbaRefresh != nil ||
		r.QbitSe != nil
}

// runAutoTagOnSecondary fires runAudioTags / runVideoTags / runDvDetail
// against the resolved secondary instance. The caller decides whether
// to invoke this based on the per-bucket JobTarget (audio/video/dv
// each carry their own target). resolveSyncTarget falls back to the
// first other-of-same-type instance when SyncToInstanceID is empty,
// which matches the wizard's "Auto-pick first available" semantics.
//
// Returns nil response when:
//   - secondary instance can't be resolved (deleted / wrong type)
//   - phase action isn't audiotags / videotags / dvdetail
//   - the run errored (apiErr != nil)
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
	case "dvdetail":
		req.BypassDvCache = job.Options.BypassDvCache
		resp, apiErr := r.server.runDvDetail(ctx, cfg, secInst, appType, req)
		r.server.auditScan("schedule:"+job.ID, "dvdetail", secInst, req, resp, errMsgOf(apiErr))
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
		noun := "movies"
		if resp.Instance.Type == "sonarr" {
			noun = "episode files"
		}
		parts = append(parts, "no "+noun+" needed recovery")
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
