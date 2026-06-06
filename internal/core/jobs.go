package core

import (
	"time"

	"resolvarr/internal/core/engine"
)

// JobMode selects which of tagarr's batch actions the scheduled job
// runs. Tag/Discover/Recover map 1:1 onto tagarr.sh modes; Combined
// chains a user-picked subset in a single scheduled run.
type JobMode string

const (
	JobModeTag             JobMode = "tag"
	JobModeDiscover        JobMode = "discover"
	JobModeRecover         JobMode = "recover"
	JobModeAudioTags       JobMode = "audiotags"       // M4 — audio-stream auto-tags from mediaInfo
	JobModeVideoTags       JobMode = "videotags"       // M4 — video-stream auto-tags (resolution / codec / HDR) from mediaInfo
	JobModeDvDetail        JobMode = "dvdetail"        // M4b — Dolby Vision profile / CM tags via ffmpeg+dovi_tool
	JobModeMissingEpisodes JobMode = "missingepisodes" // Sonarr only — gap scan + optional Tag/Search
	JobModePlexSync        JobMode = "plexsync"        // Arr tags → Plex labels/collections; config on ScheduledJob.PlexSync
	JobModeTbaRefresh      JobMode = "tbarefresh"      // Sonarr only — rename TBA-imported files; config on ScheduledJob.TbaRefresh
	JobModeQbitSeTag       JobMode = "qbitsetag"       // Sonarr only — tag qBit torrents by Season/Episode; config on ScheduledJob.QbitSe
	JobModeReconcile       JobMode = "reconcile"       // Radarr + Sonarr — clear stuck queue downloads; config on ScheduledJob.Reconcile
	JobModeCombined        JobMode = "combined"
)

// ReconcileConfig is the per-schedule snapshot for the reconcile phase
// (clear stuck downloads). Apply-mode moves redundant downloads to
// PostCategory on the given qBit instance; preview-mode just reports.
// QbitInstanceID empty + apply = the phase records an error (nothing to
// act against). The run mode comes from JobOptions.RunMode like the
// other phases.
type ReconcileConfig struct {
	QbitInstanceID string `json:"qbitInstanceId,omitempty"`
	PostCategory   string `json:"postCategory,omitempty"`
}

// ValidJobMode returns true when m is one of the accepted values.
// Used by handlers to reject garbage before saving a schedule.
func ValidJobMode(m JobMode) bool {
	switch m {
	case JobModeTag, JobModeDiscover, JobModeRecover,
		JobModeAudioTags, JobModeVideoTags, JobModeDvDetail,
		JobModeMissingEpisodes, JobModePlexSync, JobModeTbaRefresh, JobModeQbitSeTag, JobModeCombined:
		return true
	}
	return false
}

// JobTarget picks which instance(s) an auto-tag phase runs on inside
// a combined-mode chain. Empty defaults to "primary" everywhere it's
// read so legacy rules (saved before this field existed) keep their
// historical behaviour.
type JobTarget string

const (
	JobTargetPrimary   JobTarget = "primary"
	JobTargetSecondary JobTarget = "secondary"
	JobTargetBoth      JobTarget = "both"
)

// IncludesPrimary returns true when the target says the phase should
// run on the rule's primary instance. Empty target defaults to true
// (legacy behaviour: phase always ran on primary).
func (t JobTarget) IncludesPrimary() bool {
	return t == "" || t == JobTargetPrimary || t == JobTargetBoth
}

// IncludesSecondary returns true when the target says the phase should
// also fan out to the secondary instance.
func (t JobTarget) IncludesSecondary() bool {
	return t == JobTargetSecondary || t == JobTargetBoth
}

// ValidJobTarget normalises a JobTarget — empty / unknown values
// collapse to JobTargetPrimary so a malformed-config rule still runs
// somewhere instead of silently dropping the phase.
func ValidJobTarget(t JobTarget) JobTarget {
	switch t {
	case JobTargetPrimary, JobTargetSecondary, JobTargetBoth:
		return t
	}
	return JobTargetPrimary
}

// JobOptions holds every per-run toggle from the bash configs and CLI
// flags, tagged by which mode each field applies to. Not every field
// is meaningful in every mode — handlers validate the applicable
// subset before accepting a submit. Zero-valued fields are omitted
// from the JSON, so a Recover job's stored options don't carry dead
// Tag-mode fields.
type JobOptions struct {
	// Common to every mode
	RunMode    string `json:"runMode,omitempty"`    // "preview" | "apply"
	DebugTrace bool   `json:"debugTrace,omitempty"` // per-item decision log

	// Tag-mode
	SyncToSecondary        bool     `json:"syncToSecondary,omitempty"`
	SyncToInstanceID       string   `json:"syncToInstanceId,omitempty"` // explicit target for sync; empty = scheduler picks first other of same type (3+ instance support)
	IncludeDiscovery       bool     `json:"includeDiscovery,omitempty"`
	AutoActivateDiscovered bool     `json:"autoActivateDiscovered,omitempty"`
	CleanupUnusedTags      bool     `json:"cleanupUnusedTags,omitempty"`
	RunForGroups           []string `json:"runForGroups,omitempty"` // empty = all configured

	// TagSource picks which decision engine the tag phase uses:
	//   "" or "active"   — match Active-list release groups (legacy default)
	//   "discover"       — Discover→Tag chain (find new groups, then tag)
	//   "filter-only"    — ignore release group entirely; tag every movie
	//                      passing the quality + audio filter with FilterOnlyTag
	// Filter-only mode is the architecturally clean replacement for the
	// "shared tag across multiple groups" pattern that used to flap on
	// every alternating run.
	TagSource     string `json:"tagSource,omitempty"`
	FilterOnlyTag string `json:"filterOnlyTag,omitempty"` // only meaningful when TagSource == "filter-only"

	// Per-bucket instance targets for the auto-tag phases. Each is one
	// of: "primary" (default) | "secondary" | "both". Drives the
	// "A-chain → B-chain" execution model — the head phases (discover/
	// recover/tag) run on the rule's primary instance once; then each
	// auto-tag phase fans out to whichever instance(s) its target says.
	// Token allow-lists are universal: the per-rule config (which
	// codecs / channels / resolutions / DV-detail values to emit) is
	// applied to whichever instance(s) the phase fires on.
	//
	// Distinct from SyncToSecondary which mirrors release-group tag
	// decisions to a second instance via TmdbID; auto-tags are
	// mediaInfo-derived per file so a blind mirror would write the
	// wrong tags (a 4K version has different mediaInfo than the
	// 1080p version). Each instance gets auto-tags based on its own
	// files.
	//
	// AutoTagsRunOnSecondary is the legacy boolean (pre per-bucket
	// targets). Migrated to AudioTagsTarget + VideoTagsTarget on
	// Config.Load: true → 'both' on both, false → 'primary'. Kept on
	// the struct without a JSON tag so old persisted JSON parses
	// cleanly into the new shape via the migration step.
	AutoTagsRunOnSecondary bool      `json:"autoTagsRunOnSecondary,omitempty"`
	AudioTagsTarget        JobTarget `json:"audioTagsTarget,omitempty"`
	VideoTagsTarget        JobTarget `json:"videoTagsTarget,omitempty"`
	DvDetailTarget         JobTarget `json:"dvDetailTarget,omitempty"`

	// DV-detail-mode
	//
	// BypassDvCache makes a saved rule's DV detail phase skip the
	// /config/dv-cache.json memo on every fire — full ffmpeg +
	// dovi_tool re-extraction for every file. Only meaningful when
	// the rule includes DV detail (combined-mode with "dvdetail" in
	// CombinedModes, or single-mode with Mode = "dvdetail").
	// Off by default (cache active). Setting this to true on a 5000-
	// movie Radarr is fine for an occasional refresh-extraction rule
	// but wasteful as a daily cron — same trade-off as the per-scan
	// checkbox in Library scan's Run controls.
	BypassDvCache bool `json:"bypassDvCache,omitempty"`

	// Discover-mode
	DiscoverWriteBack     bool `json:"discoverWriteBack,omitempty"`     // true = write commented entries, false = clean-report only
	DiscoverScanSecondary bool `json:"discoverScanSecondary,omitempty"` // also walk secondary instance's library

	// Recover-mode
	RecoverIncludeSecondary bool `json:"recoverIncludeSecondary,omitempty"` // include Radarr/Sonarr secondary (based on Mode's instance Type)
	RecoverIncludeSonarr    bool `json:"recoverIncludeSonarr,omitempty"`    // also run for Sonarr instances
	RecoverSonarrSecondary  bool `json:"recoverSonarrSecondary,omitempty"`
	RecoverTestItemID       int  `json:"recoverTestItemId,omitempty"` // 0 = full library scan

	// Combined-mode — user-picked subset of {discover, recover, tag,
	// extratags}. Runtime ordering is fixed by the scheduler runner
	// (Discover → Recover → Tag → Extra tags); membership in the slice
	// just opts each phase in. Cleanup is a tail of the Tag phase
	// gated by CleanupUnusedTags above.
	CombinedModes []JobMode `json:"combinedModes,omitempty"`
}

// ScheduledJob is one saved workflow — Mode + Instance + Options +
// cron + per-rule config snapshots. Users create these via the wizard
// in the Scheduled runs / Rules section. Stored inline in resolvarr.json
// under Config.Schedules so the whole workflow + history travels with
// a single file copy.
//
// Per-rule config: Filters / ExtraTags / ReleaseGroupIDs make each
// schedule a self-contained "config preset". Two architectural rules:
//
//  1. Schedules do NOT read globals at fire-time. They read their own
//     snapshot. Changing the global Library scan UI does NOT affect
//     existing schedules — that's the whole point of the rule model.
//
//  2. Existing schedules created before per-rule fields landed get
//     migrated at Load: nil fields get a snapshot of the matching
//     global config (Filters by instance type, full ExtraTagsConfig,
//     all currently-active matching ReleaseGroups). Migration is
//     one-shot and persisted; subsequent Loads see populated fields.
//
// Filters and ExtraTags are full snapshots (independent value-types).
// ReleaseGroupIDs is a SUBSET-by-ID into the globally-managed
// cfg.ReleaseGroups[] master list — the per-group config (search /
// tag / display / mode) is still maintained globally so a user
// renaming a group propagates everywhere; the schedule just selects
// which ones apply to itself.
type ScheduledJob struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Mode       JobMode    `json:"mode"`
	InstanceID string     `json:"instanceId"`
	Cron       string     `json:"cron"`
	Enabled    bool       `json:"enabled"`
	Options    JobOptions `json:"options"`

	// Per-rule config snapshots. nil means "not yet configured / use
	// global" (back-compat for schedules created before the rule
	// model landed). Post-migration these are always populated.

	// Filters is the schedule's own copy of the per-Arr-type filter
	// config matching the schedule's instance type (Radarr or Sonarr).
	// Single FilterConfig per schedule — a schedule fires against ONE
	// instance, so it only needs ONE side's rules.
	Filters *engine.FilterConfig `json:"filters,omitempty"`

	// AudioTags / VideoTags / DvDetail are the schedule's own copies
	// of the corresponding global configs. nil = "use global at
	// fire-time" (back-compat for schedules that predate the rule
	// model). Post-migration these are non-nil for every schedule.
	AudioTags *AudioTagsConfig `json:"audioTags,omitempty"`
	VideoTags *VideoTagsConfig `json:"videoTags,omitempty"`
	DvDetail  *DvDetailConfig  `json:"dvDetail,omitempty"`

	// ReleaseGroupIDs is the subset of globally-defined RGs this
	// schedule activates. Refers by ID into cfg.ReleaseGroups[]; the
	// per-group config (search/tag/display/mode) is global. nil =
	// not yet configured (pre-migration); empty slice = explicitly
	// no groups (user picked none). Post-migration this is non-nil
	// for every schedule.
	ReleaseGroupIDs []string `json:"releaseGroupIds,omitempty"`

	// MissingEpisodes is the per-schedule snapshot for the
	// missingepisodes phase (Sonarr-only — Tag Library → Missing
	// episodes / QFA combined mode). nil = phase not enabled on this
	// schedule. When present, the chain runner + scheduler runner
	// read threshold / bufferHours / include filters / action toggles
	// off this snapshot rather than a global (no global for this
	// phase today — it lives only as a per-rule snapshot).
	MissingEpisodes *MissingEpisodesConfig `json:"missingEpisodes,omitempty"`

	// PlexSync is the per-schedule snapshot for the plexsync phase
	// (Arr tags → Plex labels/collections). nil = phase not enabled on
	// this schedule. Same snapshot model as MissingEpisodes: the config
	// lives only here (no global standalone "Plex label rule"), so the
	// scheduler runner reads instance-agnostic Plex settings off this
	// field and synthesizes the engine input via
	// PlexLabelSyncConfig.AsPlexLabelRule(job.InstanceID, appType).
	PlexSync *PlexLabelSyncConfig `json:"plexSync,omitempty"`

	// TbaRefresh is the per-schedule snapshot for the tbarefresh phase
	// (Sonarr-only — rename episode files imported as "TBA" once the
	// real title is known). nil = phase not enabled. All-scalar config
	// (no slices/maps), so ConfigStore.Get's slice-append copy is
	// enough; no deep-copy block needed (same as MissingEpisodesConfig).
	TbaRefresh *TbaRefreshConfig `json:"tbaRefresh,omitempty"`

	// QbitSe is the per-schedule snapshot for the qbitsetag phase
	// (Sonarr-only — tag qBit torrents by Season / Episode / Unmatched).
	// nil = phase not enabled. Reuses core.QbitSeRules (same shape the
	// webhook rule + one-off run carry); the scheduler runner hands it
	// straight to runQbitSeScanWithRules. AggregationWindowSeconds is
	// ignored here (it only matters for the webhook qBit-Add debounce).
	QbitSe *QbitSeRules `json:"qbitSe,omitempty"`

	// Reconcile is the per-schedule snapshot for the reconcile phase
	// (clear stuck downloads). nil = phase not enabled.
	Reconcile *ReconcileConfig `json:"reconcile,omitempty"`

	// History holds the last N runs — N=5 today, configurable later.
	// Runs older than the cap land in the log file (LogPath on each
	// JobRun) and are no longer surfaced in the UI's rolling table.
	// Cap is maxInMemoryHistory in scheduler.go (currently 7); files
	// for runs beyond the cap are deleted from disk in the same step.
	History []JobRun `json:"history,omitempty"`
}

// MissingEpisodesConfig is the per-schedule (and per-webhook-rule-
// snapshot if that ever lands) configuration for the Sonarr-only
// missing-episodes phase. Mirrors the standalone Library scan tab's
// config plus per-rule action toggles.
//
// ThresholdPercent: 0-100 percent of aired episodes that must be on
// disk before the season is considered "covered". Default 70.
//
// BufferHours: 0-672 hours. Episodes that aired within this window
// are NOT flagged as missing (gives indexers time to publish). 0
// means "any aired episode is fair game"; the dispatcher honours
// 0 explicitly. Default 24 (matches Sonarr's own default check).
//
// Action toggles: at fire-time, the chain runner / scheduler runner
// fires preview always; tag-apply when ActionTag is true; Sonarr
// search-trigger when ActionSearch is true. Both can be on. Both
// off = preview only (still produces a result row).
//
// TagName: tag label written to Sonarr when ActionTag is true.
// Validated against `^[a-z0-9_-]+$` (Sonarr's tag-label regex);
// empty / whitespace defaults to "missing-episodes" at fire-time.
type MissingEpisodesConfig struct {
	ThresholdPercent  int    `json:"thresholdPercent"`
	BufferHours       int    `json:"bufferHours"`
	IncludeContinuing bool   `json:"includeContinuing"`
	IncludeEnded      bool   `json:"includeEnded"`
	IncludeSpecials   bool   `json:"includeSpecials"`
	TagName           string `json:"tagName"`
	ActionTag         bool   `json:"actionTag"`
	ActionSearch      bool   `json:"actionSearch"`
}

// TbaRefreshConfig is the per-schedule (and QFA) snapshot for the
// tbarefresh phase. Same include-toggles as the standalone Library
// scan tab. A scheduled/QFA run in apply mode renames every TBA file
// it finds (no per-file selection in the automated path); preview mode
// just reports the count.
type TbaRefreshConfig struct {
	IncludeContinuing bool `json:"includeContinuing"`
	IncludeEnded      bool `json:"includeEnded"`
	IncludeSpecials   bool `json:"includeSpecials"`
}

// JobRun summarises one execution of a schedule. Kept narrow on
// purpose — detailed per-item traces belong in the log file, not in
// resolvarr.json. Status maps 1:1 onto the bash script's exit semantics:
//
//   - "ok"      primary job completed without errors
//   - "partial" primary completed but secondary or per-item ops failed
//   - "error"   primary could not complete (Arr unreachable, etc.)
type JobRun struct {
	StartedAt  time.Time `json:"startedAt"`
	DurationMs int64     `json:"durationMs"`
	Status     string    `json:"status"`
	Summary    string    `json:"summary"`              // short, e.g. "14 tags added, 2 removed"
	LogPath    string    `json:"logPath,omitempty"`    // path to the per-run log file on disk
	ResultPath string    `json:"resultPath,omitempty"` // path to the per-run scan-response JSON on disk
}
