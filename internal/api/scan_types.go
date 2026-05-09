package api

import (
	"time"

	"resolvarr/internal/arr"
	"resolvarr/internal/core"
	"resolvarr/internal/core/engine"
)

// scan_types.go — request, response, and per-action sub-types for the
// /api/scan/run endpoint and its companion handlers (scan.go dispatcher,
// scan_tag.go, scan_discover.go, scan_cleanup.go, scan_recover.go).
//
// Strict contract on the scan handlers: orchestration only — every per-
// movie, per-group tag decision is delegated to engine.DecideTag(). This
// types file holds nothing but data shapes; the type docs note where
// each field comes from but don't introduce decision semantics.

// scanRunRequest is the POST body for /api/scan/run.
//
// Action selects WHICH job to run (tag / discover / recover / combined),
// matching `core.JobMode` values. Mode (preview vs apply) is meaningful
// only for action="tag" — discover always runs as preview-only and writes
// no config until the user POSTs selected candidates to /api/groups.
//
// Action is optional; empty means "tag" (back-compat with v1 callers).
//
// IncludeKnown is the discover-mode equivalent of bash `--discover-clean`.
// Default (false) suppresses release-groups already present in config —
// classic "show me what's new". When true, the known-set is ignored and
// EVERY library group passing the filters is reported, useful for an
// audit-style "what does my whole filter-pass inventory look like".
//
// SyncToInstanceID enables M3e secondary-sync (tag-mode only). Empty =
// primary-only (default). Set = after primary decisions, mirror to the
// secondary instance via TmdbID matching, with orphan cleanup. Bash
// equivalent: ENABLE_SYNC_TO_SECONDARY=true with SECONDARY_RADARR_*
// pointing at the second instance.
type scanRunRequest struct {
	// AuditSource is the call-site label used by audit logging. Empty
	// (adhoc /api/scan/run) renders as "adhoc"; the scheduler runner
	// sets it to "schedule:<id>" so runs.log can distinguish manual
	// Quick fix-all runs from cron-driven schedule fires. Not in JSON.
	AuditSource string `json:"-"`

	InstanceID            string   `json:"instanceId"`
	SyncToInstanceID      string   `json:"syncToInstanceId,omitempty"` // tag-mode: also mirror primary decisions to this secondary instance via TmdbID
	Mode                  string   `json:"mode"`                       // "preview" | "apply"
	Action                string   `json:"action,omitempty"`                // "tag" (default) | "discover" | "cleanup" | "recover" — combined deferred
	RunGroups             []string `json:"runGroups,omitempty"`             // tag-mode: empty = all groups of instance's type
	IncludeKnown          bool     `json:"includeKnown,omitempty"`          // discover-mode: report ALL passing groups, including configured ones (--discover-clean parity)
	// Discover write-back: when DiscoverWriteBack is true, persist
	// every newly-found release-group into cfg.ReleaseGroups; when
	// AutoActivateDiscovered is also true, the new entries land
	// Enabled. Both are honoured for action="discover" only. Schedule-
	// path mirrors these via JobOptions; Quick fix-all and saved
	// rule both share this request shape so the same auto-add UX
	// works for both.
	DiscoverWriteBack      bool `json:"discoverWriteBack,omitempty"`
	AutoActivateDiscovered bool `json:"autoActivateDiscovered,omitempty"`
	CleanupUnusedTags     bool     `json:"cleanupUnusedTags,omitempty"`     // tag-mode chain: after apply, delete managed tags with 0 movies (bash CLEANUP_UNUSED_TAGS=true parity)
	CleanupLabels         []string `json:"cleanupLabels,omitempty"`         // cleanup-action only: when non-empty, restrict apply-mode deletes to these labels (intersection with candidates set)

	// Tag-mode source selector. Empty / "active" = match Active-list
	// release groups (legacy default). "discover" = Discover→Tag chain.
	// "filter-only" = ignore release group; tag every movie passing the
	// quality+audio filter with FilterOnlyTag. Filter-only mode replaces
	// the broken "shared tag across multiple groups" pattern (which
	// flapped on every alternating run) with a clean single-tag rule.
	// See dev/analysis/filter-only-tag.md.
	TagSource     string `json:"tagSource,omitempty"`
	FilterOnlyTag string `json:"filterOnlyTag,omitempty"`

	// Recover-action fields (M3c). Bash tagarr_recover.sh parity for the
	// per-movie release-group recovery flow.
	RecoverRename     bool  `json:"recoverRename,omitempty"`     // apply-only: trigger RenameFiles after a successful patch (bash RENAME=true default)
	RecoverItems      []int `json:"recoverItems,omitempty"`      // optional movie-ID filter; empty = all affected (--movie parity)
	RecoverApplyItems []int `json:"recoverApplyItems,omitempty"` // apply-only: when non-empty, restrict apply to these movie IDs from the would-fix set (per-row UI exclude support)

	// Per-request overlay (rule-style). When set, these overrides win
	// over the persisted globals for THIS run only. Used by the
	// Quick fix-all wizard so the user can test alternative rules
	// without touching their saved Library scan config. Same shape as
	// ScheduledJob's per-rule snapshots — reuses the applyRuleOverlay
	// helper that schedules also use, so semantics are identical.
	//
	// nil means "use globals" (back-compat for every existing caller).
	OverlayFilters         *engine.FilterConfig  `json:"overlayFilters,omitempty"`
	OverlayAudioTags       *core.AudioTagsConfig `json:"overlayAudioTags,omitempty"`
	OverlayVideoTags       *core.VideoTagsConfig `json:"overlayVideoTags,omitempty"`
	OverlayDvDetail        *core.DvDetailConfig  `json:"overlayDvDetail,omitempty"`
	OverlayReleaseGroupIDs []string              `json:"overlayReleaseGroupIds,omitempty"`

	// OverlayInjectGroups appends ephemeral ReleaseGroups to cfg for
	// THIS request only — never persisted, never visible to other
	// requests. Used by the QFA chain in preview-mode to flow Discover's
	// findings into the Tag phase ("show me what would happen if these
	// groups were added") without writing anything to disk. Each entry
	// must have Type matching the request's instance type; mismatched
	// entries are dropped silently.
	OverlayInjectGroups []core.ReleaseGroup `json:"overlayInjectGroups,omitempty"`

	// BypassDvCache skips the on-disk DV cache for this scan only —
	// every file goes through ffmpeg + dovi_tool fresh, no Get-on-hit
	// short-circuit and no Put-on-success memoise. Library-scan UI
	// surfaces this as a checkbox in the Run controls. Saved rules
	// can pin it via JobOptions.BypassDvCache so a "fresh extraction
	// every time" rule doesn't depend on user action. Default false
	// (cache active — same behaviour as before this flag existed).
	BypassDvCache bool `json:"bypassDvCache,omitempty"`
}

// scanDecision is the per-(movie, group) public shape returned in preview.
// Mirrors engine.Decision plus the Arr-side hasTag comparison; the Action
// field is the composition that the UI renders as the checklist row.
//
// SecondaryAction / SecondaryHasTag are populated only when the request
// included SyncToInstanceID AND the secondary instance contained a movie
// matching this primary movie's TmdbID. SecondaryAction values:
//
//	"add"  | "remove" | "keep" | "skip" (mirrors primary semantics on
//	                                     the secondary instance)
//	"missing" — secondary doesn't have this movie at all (TmdbID unmatched)
//	""        — sync was not requested (back-compat omitempty)
type scanDecision struct {
	GroupID         string `json:"groupId"`
	GroupTag        string `json:"groupTag"`
	GroupDisplay    string `json:"groupDisplay"`
	ShouldHave      bool   `json:"shouldHave"`
	HasTag          bool   `json:"hasTag"`
	Action          string `json:"action"` // "add" | "remove" | "keep" | "skip"
	Matched         bool   `json:"matched"`
	MatchLocation   string `json:"matchLocation,omitempty"`
	Quality         string `json:"quality,omitempty"`
	QualityDetail   string `json:"qualityDetail,omitempty"`
	Audio           string `json:"audio,omitempty"`
	AudioDetail     string `json:"audioDetail,omitempty"`
	Reason          string `json:"reason,omitempty"`
	SecondaryAction string `json:"secondaryAction,omitempty"` // mirror action on secondary, or "missing" / ""
	SecondaryHasTag bool   `json:"secondaryHasTag,omitempty"` // current state on secondary (when present)
}

// scanItem is one movie's view in a preview response.
//
// Year, TmdbID, and the file-field block (RelativePath/SceneName/
// ReleaseGroup) are surfaced on every preview item so the UI can
// expand a row to verify the engine's decisions against the actual
// file context — same drill-down pattern Discover uses, ported to
// tag-mode for visual consistency. Quality/audio detail strings
// are not duplicated here; they're already on each Decision per
// group, since the same movie can score differently against two
// groups with different filter configs in the future.
//
// AutoDecisions is populated for action="audiotags" / "videotags"
// runs. DvDecisions for "dvdetail". UI renders both with the same
// drill-down row pattern (tag label + add/remove/keep verdict).
type scanItem struct {
	ID             int                    `json:"id"`
	TmdbID         int                    `json:"tmdbId,omitempty"`
	TvdbID         int                    `json:"tvdbId,omitempty"` // Sonarr — series tvdbId
	Title          string                 `json:"title"`
	Year           int                    `json:"year,omitempty"`
	CurrentTags    []int                  `json:"currentTags"`
	ReleaseGroup   string                 `json:"releaseGroup,omitempty"` // raw rg from movieFile
	SceneName      string                 `json:"sceneName,omitempty"`
	RelativePath   string                 `json:"relativePath,omitempty"`
	Decisions      []scanDecision         `json:"decisions,omitempty"`
	AutoDecisions  []scanAutoTagDecision  `json:"autoDecisions,omitempty"` // M4 — audiotags / videotags
	DvDecisions    []scanDvDetailDecision `json:"dvDecisions,omitempty"`   // M4b — dvdetail
	// DvDetail surfaces the parsed RPU summary on every dvdetail-mode
	// preview row (profile / layer / cm-version) so the drill-down can
	// show the user the underlying facts behind the tag verdicts.
	// Empty when the file wasn't a DV candidate / extraction failed /
	// extraction returned no RPU.
	DvDetail *scanDvDetailFacts `json:"dvDetail,omitempty"`
	// DvStatus describes the per-movie outcome of the DV-detail
	// extraction phase. Surfaces in the response so the UI can render
	// the right badge per row without re-deriving from counters.
	// Empty for non-dvdetail runs.
	DvStatus string `json:"dvStatus,omitempty"` // see scanDvDetailDecision.Status — same vocabulary

	// ----- Sonarr Audio/Video aggregate fields (M-Sonarr) -----
	//
	// Populated only on Sonarr action="audiotags"/"videotags" runs.
	// SeriesID + SeriesTitle alias ID + Title for self-documenting
	// payloads (existing Sonarr-recover code uses the same fields).
	// EpisodeFileCount + Episodes give the result-panel a per-series
	// drill-in so the user can see exactly which episodes contributed
	// which tags to the series-level aggregate. Empty on Radarr.
	SeriesID         int                 `json:"seriesId,omitempty"`
	SeriesTitle      string              `json:"seriesTitle,omitempty"`
	EpisodeFileCount int                 `json:"episodeFileCount,omitempty"`
	Episodes         []scanSeriesEpisode `json:"episodes,omitempty"`
	// Error populates when the per-series episodefiles fetch failed —
	// the row appears in the result list as a fix-failed-style entry
	// so the user sees that the series wasn't checked. Empty otherwise.
	Error string `json:"error,omitempty"`
}

// scanSeriesEpisode is one episode-file's view inside a Sonarr
// audio/video result-row's drill-in. Carries enough mediaInfo to
// render a per-episode summary line PLUS the per-episode tag set
// the engine emitted (ContributedTags) so the user can verify why
// each tag landed on the parent series.
type scanSeriesEpisode struct {
	EpisodeFileID int    `json:"episodeFileId"`
	SeasonNumber  int    `json:"seasonNumber"`
	RelativePath  string `json:"relativePath,omitempty"`
	SceneName     string `json:"sceneName,omitempty"`
	// Compact mediaInfo summary — UI renders directly without re-deriving.
	Resolution    string `json:"resolution,omitempty"`    // "1080p" | "" (resolution-bucket label)
	VideoCodec    string `json:"videoCodec,omitempty"`    // "h265" / "h264" / "av1" / ""
	HDR           string `json:"hdr,omitempty"`           // "sdr" / "hdr10" / "hdr10plus" / "dv" / "pq"
	VideoBitDepth int    `json:"videoBitDepth,omitempty"` // 8 / 10 (raw int — 10bit tag derived in engine)
	AudioCodec    string `json:"audioCodec,omitempty"`    // "truehd" / "eac3" / etc
	AudioChannels string `json:"audioChannels,omitempty"` // "7-1" / "5-1" / "2-0"
	HasAtmos      bool   `json:"hasAtmos,omitempty"`
	// ContributedTags is the per-episode tag set the active scan's
	// engine config would emit for THIS file alone. Lets the drill-in
	// show "S02E05 contributed: hdr10, h265, atmos" so the aggregate
	// doesn't look magical.
	ContributedTags []string `json:"contributedTags,omitempty"`
}

// scanAutoTagDecision is one (movie, auto-tag-label) decision returned
// by the audiotags or videotags scan. Bucket identifies which bucket
// produced the tag ("audio" / "resolution" / "codec" / "hdr") so the
// UI can group rows in the per-movie drill-down. Tag is the full
// prefixed label ("resolution:1080p"). Action: "add" | "remove" | "keep".
type scanAutoTagDecision struct {
	Bucket string `json:"bucket"`           // "audio" | "resolution" | "codec" | "hdr"
	Tag    string `json:"tag"`              // full prefixed label
	Action string `json:"action"`           // "add" | "remove" | "keep"
	Reason string `json:"reason,omitempty"` // optional drill-down note
}

// scanTotals rolls up counts across the run. Action totals (toAdd/toRemove/
// toKeep) are sums across all (item, group) pairs — a single item matching
// two groups for two "add" actions counts as 2 in ToAdd. The Discovered
// total counts unique release-groups found during a discover run, not
// movies — sums the entries in the response's Discovered slice.
type scanTotals struct {
	Items      int `json:"items"`              // movies considered
	Groups     int `json:"groups,omitempty"`   // release-groups evaluated (tag-mode only)
	ToAdd      int `json:"toAdd,omitempty"`
	ToRemove   int `json:"toRemove,omitempty"`
	ToKeep     int `json:"toKeep,omitempty"`
	NoFile     int `json:"noFile,omitempty"`     // movies without a movieFile — skipped, matches tagarr.sh
	Discovered int `json:"discovered,omitempty"` // discover-mode: # unique release-groups passing filters
	// Secondary-sync aggregate counts (M3e). Populated only when the
	// request set SyncToInstanceID. Each is summed across all (movie, group)
	// pairs the same way the primary totals are.
	SecondaryToAdd    int `json:"secondaryToAdd,omitempty"`
	SecondaryToRemove int `json:"secondaryToRemove,omitempty"`
	SecondaryToKeep   int `json:"secondaryToKeep,omitempty"`
	SecondaryMissing  int `json:"secondaryMissing,omitempty"` // primary movie not found in secondary by TmdbID
	SecondaryOrphans  int `json:"secondaryOrphans,omitempty"` // queued via orphan-cleanup pass (subset of SecondaryToRemove)

	// Tag-cleanup candidates (M3-tag-cleanup). Populated for action="cleanup"
	// (always) and for action="tag" when CleanupUnusedTags=true on the request.
	// Each entry is a managed tag label (from cfg.ReleaseGroups) whose final
	// usage count would be 0 after this run. Apply-mode of cleanup or tag+cleanup
	// chain deletes the corresponding tag from Radarr; preview just lists.
	TagsToDelete          []scanCleanupCandidate `json:"tagsToDelete,omitempty"`
	SecondaryTagsToDelete []scanCleanupCandidate `json:"secondaryTagsToDelete,omitempty"`

	// Recover-mode bucket counts (M3c). Mirrors bash tagarr_recover.sh's
	// per-instance summary block. Each affected movie lands in exactly one
	// bucket; counts sum to RecoverAffected.
	RecoverAffected     int `json:"recoverAffected,omitempty"`     // movies with empty/unknown releaseGroup that were considered
	RecoverWouldFix     int `json:"recoverWouldFix,omitempty"`     // verified grab found, group recovered (preview)
	RecoverFixed        int `json:"recoverFixed,omitempty"`        // apply-only: PUT succeeded
	RecoverFlagged      int `json:"recoverFlagged,omitempty"`      // filename has a group but app field is empty — manual verify
	RecoverNoHistory    int `json:"recoverNoHistory,omitempty"`    // no grab/import events
	RecoverNoGroup      int `json:"recoverNoGroup,omitempty"`      // verified grab but it carries no releaseGroup either
	RecoverFailedVerify int `json:"recoverFailedVerify,omitempty"` // no grab matched the newest import
	RecoverFixFailed    int `json:"recoverFixFailed,omitempty"`    // apply-only: PUT or rename returned an error
	RecoverRenameFailed int `json:"recoverRenameFailed,omitempty"` // apply-only: PUT succeeded but RenameFiles command failed

	// Auto-tags totals (M4 — action="audiotags" / "videotags").
	// AutoTagRollups is a per-bucket-tag rollup
	// ("resolution:1080p" → 598 movies) used by the UI to render the
	// summary table without re-scanning Items client-side. Same shape
	// for both audio and video scans; per-response.
	// MissingMediaInfo counts movies whose movieFile.mediaInfo was nil
	// and (for video scans) no quality.resolution fallback was usable.
	AutoTagRollups   []scanAutoTagRollup `json:"autoTagRollups,omitempty"`
	MissingMediaInfo int                 `json:"missingMediaInfo,omitempty"`

	// DV-detail totals (M4b — action="dvdetail"). Per-tag rollup
	// parallel to ExtraTagBuckets, plus per-status counters so the UI
	// banner can surface the run summary at a glance:
	//
	//   "Scanned 1077 movies. 312 DV candidates. 287 cached. 25
	//    extracted (3 had no RPU, 1 failed). 73 add, 14 keep, 2 remove."
	//
	// DvNonCandidates counts movies whose mediaInfo HDR type doesn't
	// indicate DV — the fast-path skip. DvCandidates is the set we
	// actually try to extract / cache-lookup.
	DvDetailRollups   []scanDvDetailRollup `json:"dvDetailRollups,omitempty"`
	DvNonCandidates   int                  `json:"dvNonCandidates,omitempty"`   // mediaInfo HDR-type isn't DV — skipped fast
	DvCandidates      int                  `json:"dvCandidates,omitempty"`      // HDR-type indicates DV — extraction attempted
	DvCacheHits       int                  `json:"dvCacheHits,omitempty"`       // cached entry found, extraction skipped
	DvExtracted       int                  `json:"dvExtracted,omitempty"`       // ran ffmpeg+dovi_tool successfully
	DvExtractedNoRpu  int                  `json:"dvExtractedNoRpu,omitempty"`  // extraction succeeded but file had no RPU
	DvExtractFailed   int                  `json:"dvExtractFailed,omitempty"`   // extraction errored — see per-row Reason
	DvFileUnreachable int                  `json:"dvFileUnreachable,omitempty"` // post-translation path doesn't exist on disk
	DvToolsMissing    int                  `json:"dvToolsMissing,omitempty"`    // dvdetect tools weren't installed when this row's extraction was attempted (mid-scan uninstall, or partial install — distinct from the run-level pre-flight gate which 400s before walking the library)
}

// scanDvDetailDecision is one (movie, dv-detail-tag) decision returned
// by the dvdetail scan. Mirrors scanExtraDecision but with the engine
// bucket fixed to "dvdetail" and an additional Status that carries
// the RPU-extraction outcome — DV detail can fail in ways extra-tags
// can't, and the UI badge needs to know.
//
// Status values (one-of):
//
//	cached     — found in dv-cache.json, no extraction run
//	extracted  — cache miss, ffmpeg+dovi_tool ran successfully
//	no-rpu     — extraction succeeded but the file had no DV RPU
//	             (legitimate "API said DV but stream actually has none"
//	             case — emits no detail tags but isn't an error)
//	failed     — extraction errored (timeout, file unreadable, ffmpeg
//	             internal error). Detail empty; row reports the error
//	             string in Reason for the drill-down.
//	skipped    — file isn't a DV candidate (HdrTypeIndicatesDv=false).
//	             Decisions slice empty; included so the UI can show
//	             "skipped, not a DV file" rather than silently omit.
//	tools-missing — dvdetect tools weren't installed at scan time.
//	                Distinct from "failed" because the user's fix is
//	                "go install" rather than "look at the error".
type scanDvDetailDecision struct {
	Tag    string `json:"tag"`              // full prefixed label, e.g. "fel" or "dv-fel"
	Action string `json:"action"`           // "add" | "remove" | "keep"
	Status string `json:"status"`           // see vocabulary above
	Reason string `json:"reason,omitempty"` // failure-mode detail for "failed" rows
}

// scanDvDetailFacts is the parsed RPU summary surfaced per-row so the
// drill-down can show the underlying facts (profile/layer/cm-version)
// behind the tag verdicts. Populated only for rows where extraction
// (or cache hit) produced a result; empty otherwise.
//
// Profile=0 / Layer="" / CMVersion=0 are valid no-info states (matches
// engine.DvDetail's zero-value semantic). The UI renders them as "—".
type scanDvDetailFacts struct {
	Profile   int    `json:"profile,omitempty"`   // 5 / 7 / 8 / 0 (unknown)
	Layer     string `json:"layer,omitempty"`     // "fel" / "mel" / "" (n/a or unknown)
	CMVersion int    `json:"cmVersion,omitempty"` // 2 / 4 / 0 (unknown)
}

// scanDvDetailRollup summarises one dv-detail label across the run.
// Bucket is always "dvdetail" but kept for response-shape consistency
// with scanExtraBucketCount; UIs can render both flows through the
// same table component.
type scanDvDetailRollup struct {
	Tag    string `json:"tag"`
	Action string `json:"action"` // "add" | "remove" | "keep"
	Count  int    `json:"count"`
}

// scanAutoTagRollup summarises one audiotags / videotags label across
// the run. Action and Tag together identify the row; Count is the
// number of movies that received that decision. Sorted by (action,
// bucket, tag) in the response so the UI doesn't re-sort downstream.
type scanAutoTagRollup struct {
	Bucket string `json:"bucket"` // "audio" | "resolution" | "codec" | "hdr"
	Tag    string `json:"tag"`    // full prefixed label
	Action string `json:"action"` // "add" | "remove" | "keep"
	Count  int    `json:"count"`
}

// scanRecoverItem is one movie's per-recover-decision view. Mirrors the
// "process_item" output buckets in tagarr_recover.sh:618-768.
//
// Status values (one-of):
//
//	would-fix       — verified grab found, group ready to apply (preview)
//	fixed           — apply succeeded, releaseGroup written to Radarr
//	fix-failed      — apply attempted but PUT or rename returned error
//	flagged         — filename already carries a group; manual verify recommended
//	no-history      — no grab/import events for this movie
//	no-rls-group    — verified grab found but it has no releaseGroup either
//	failed-verify   — no grab matched the newest import (downloadId/title+year fallback both failed)
//
// HistorySummary surfaces enough of the underlying history to power the
// drill-down UI: import event metadata for would-fix rows, filter-rejection
// reason for flagged rows. Empty when not applicable.
type scanRecoverItem struct {
	ID                int    `json:"id"` // movie ID (Radarr) or episodefile ID (Sonarr) — unique row identity
	Title             string `json:"title"`
	Year              int    `json:"year,omitempty"`
	TmdbID            int    `json:"tmdbId,omitempty"`
	TvdbID            int    `json:"tvdbId,omitempty"`     // Sonarr-only: parent series tvdbId
	SeriesID          int    `json:"seriesId,omitempty"`   // Sonarr-only: needed for the rename command (different shape than Radarr's movieId)
	SeriesTitle       string `json:"seriesTitle,omitempty"` // Sonarr-only: parent series title (Title above carries the per-episode label like "Show — S01E05")
	SeasonNumber      int    `json:"seasonNumber,omitempty"`
	MovieFileID       int    `json:"movieFileId,omitempty"`
	RelativePath      string `json:"relativePath,omitempty"`
	SceneName         string `json:"sceneName,omitempty"`         // raw movieFile.sceneName — may be empty (Radarr only sets it for scene-imported releases)
	CurrentGroup      string `json:"currentGroup,omitempty"`      // app's current value — usually "" or "Unknown"
	Status            string `json:"status"`
	RecoveredGroup    string `json:"recoveredGroup,omitempty"`    // would-fix / fixed: the group we'd write
	FilenameGroup     string `json:"filenameGroup,omitempty"`     // flagged: the group we found in the filename
	FilenameReject    string `json:"filenameReject,omitempty"`    // when filename had a candidate that was rejected (codec/resolution etc.) — surfaces the reason
	ImportSourceTitle string `json:"importSourceTitle,omitempty"` // would-fix: sourceTitle of the matching import event (UI verification)
	ImportDate        string `json:"importDate,omitempty"`        // would-fix: ISO8601 of the matching import event date
	RenameTriggered   bool   `json:"renameTriggered,omitempty"`   // apply-only: RenameFiles command sent successfully
	Error             string `json:"error,omitempty"`             // populated when Status=fix-failed
}

// scanCleanupCandidate is one tag label flagged for cleanup. Returned for
// both standalone cleanup action and the tag-mode cleanup chain. Count is
// always 0 by definition for candidates — included for audit/visibility
// (e.g., a future UI tooltip "had 0 matches in this run").
//
// Label and TagID are paired: Label comes from cfg.ReleaseGroups (the user's
// managed list), TagID is the resolved Radarr-side tag ID for that label.
// Cleanup deletion is gated on this pairing — tag IDs that don't have a
// matching managed label are NEVER touched.
type scanCleanupCandidate struct {
	Label string `json:"label"`
	TagID int    `json:"tagId"`
	Count int    `json:"count"` // 0 by definition for cleanup candidates
}

// scanDiscoveredGroup is one new release-group surfaced by a discover run.
// Mirrors the bash `discovered_groups` map entry (display + quality detail
// + audio detail) plus a count + sample-movie references for the UI to
// show the user what the group "looks like" before they decide to add it.
//
// The Search field is the raw release-group string from the first movie
// that triggered discovery (preserves original case; bash uses rg_original
// for the display field but lowercases for dedup-key purposes — same here,
// the dedup-key never leaves the handler).
//
// Samples is a per-group drill-down — up to discoveredMaxSamples movies
// that triggered discovery for this group. Populated in insertion order
// (first hit first). UI uses it for "click row to verify it's not a false
// match" — each sample carries the raw fields that drove the filter pass
// so the user can audit before clicking Add.
type scanDiscoveredGroup struct {
	Search           string                 `json:"search"`           // raw release-group string from library (first sample's case)
	Count            int                    `json:"count"`            // # movies whose release-group matches AND that pass filters
	SampleMovieID    int                    `json:"sampleMovieId"`    // first movie that triggered discovery (= Samples[0].MovieID)
	SampleMovieTitle string                 `json:"sampleMovieTitle"` // first movie's title (= Samples[0].Title)
	QualityDetail    string                 `json:"qualityDetail"`    // from first sample — "MA WEB-DL" / "Play WEB-DL" / "Unknown WEB-DL"
	AudioDetail      string                 `json:"audioDetail"`      // from first sample — "TrueHD Atmos" / "DTS-X" / etc.
	Samples          []scanDiscoveredSample `json:"samples"`          // up to discoveredMaxSamples movies that drove the discovery
}

// scanDiscoveredSample is one movie that triggered discovery for a group.
// All of these fields except QualityDetail/AudioDetail come straight from
// Arr's movieFile — the user uses them to verify the match isn't a false
// positive. Quality + audio detail are computed per-movie (not just from
// the first sample) so the user sees variation across the group.
//
// MovieID + TmdbID are both surfaced so the user can cross-reference the
// match against Radarr's own UI (movieId for the URL — /movie/<id>) and
// against TMDb (tmdbId — for the canonical identity that secondary-sync
// will match against in M3e). Year is for UI display so the title reads
// "Movie (2024)" instead of just "Movie", which matters for franchises
// with multiple entries (e.g. Mario Bros. 1993 vs Mario Bros. 2023).
type scanDiscoveredSample struct {
	MovieID       int    `json:"movieId"`
	TmdbID        int    `json:"tmdbId,omitempty"`
	Title         string `json:"title"`
	Year          int    `json:"year,omitempty"`
	ReleaseGroup  string `json:"releaseGroup"`            // raw rg field from movieFile
	SceneName     string `json:"sceneName,omitempty"`     // raw sceneName field (often empty for non-scene)
	RelativePath  string `json:"relativePath,omitempty"`  // raw relativePath — the actual file as Arr stores it
	QualityDetail string `json:"qualityDetail,omitempty"` // per-movie evaluation
	AudioDetail   string `json:"audioDetail,omitempty"`   // per-movie evaluation
}

// discoveredMaxSamples caps the per-group sample list. Bumped from 10 to
// 100 on 2026-04-26 after smoke-test feedback — first-10 was useful for
// quick spot-checks but a group like the user's TheFarm (76 movies) lost
// 66 entries the user wanted to see. 100 covers every realistic group
// size in a homelab; truly enormous groups (>100) still get truncated
// with a "showing first N of M" notice in the UI.
const discoveredMaxSamples = 100

// scanApplied reports what actually hit Arr in apply mode.
type scanApplied struct {
	TagsCreated  []string              `json:"tagsCreated"`
	TagsDeleted  []string              `json:"tagsDeleted,omitempty"` // cleanup action or tag-mode CleanupUnusedTags chain
	ItemsAdded   int                   `json:"itemsAdded"`            // total (item, tagLabel) add pairs applied
	ItemsRemoved int                   `json:"itemsRemoved"`          // total (item, tagLabel) remove pairs applied
	Secondary    *scanSecondaryApplied `json:"secondary,omitempty"`
	// DiscoverAdded lists release groups appended to cfg.ReleaseGroups
	// by an action="discover" run with DiscoverWriteBack=true.
	// Frontend chains (Quick fix-all) use this to extend their
	// per-request RG-overlay so the Tag phase later in the chain
	// includes the just-added groups. Empty for non-discover
	// actions.
	DiscoverAdded []scanDiscoverAdded `json:"discoverAdded,omitempty"`
}

// scanDiscoverAdded is one auto-added release group surfaced in
// scanApplied.DiscoverAdded. Carries enough for the frontend to
// extend its overlayReleaseGroupIds for subsequent phases AND
// surface a "X added (enabled/disabled)" line in the result panel.
type scanDiscoverAdded struct {
	ID      string `json:"id"`
	Search  string `json:"search"`
	Tag     string `json:"tag"`
	Enabled bool   `json:"enabled"`
}

// scanSecondaryApplied reports what hit the secondary instance during a
// sync-enabled tag-apply. OrphansRemoved is the subset of ItemsRemoved
// that came from the orphan-cleanup pass (secondary movies the primary
// said "false" or "unknown" for); the rest came from the per-movie sync
// pass (primary explicitly said "remove" and secondary had the tag).
type scanSecondaryApplied struct {
	InstanceID     string   `json:"instanceId"`
	InstanceName   string   `json:"instanceName"`
	TagsCreated    []string `json:"tagsCreated"`
	TagsDeleted    []string `json:"tagsDeleted,omitempty"` // cleanup chain on the secondary side
	ItemsAdded     int      `json:"itemsAdded"`
	ItemsRemoved   int      `json:"itemsRemoved"`
	OrphansRemoved int      `json:"orphansRemoved"`
}

// scanInstanceInfo is a narrow view of the targeted instance echoed back
// to the caller so the UI can label the result without a second fetch.
type scanInstanceInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// scanResponse is the full API response for every action+mode combination.
// Action echoes back what the request asked for so the UI can switch on it
// when rendering. Items / Applied are tag-mode only; Discovered is
// discover-mode only.
type scanResponse struct {
	Mode       string                `json:"mode"`
	Action     string                `json:"action,omitempty"` // "tag" (default) | "discover" | "cleanup" | "recover"
	Instance   scanInstanceInfo      `json:"instance"`
	Totals     scanTotals            `json:"totals"`
	Items      []scanItem            `json:"items,omitempty"`      // tag preview only
	Applied    *scanApplied          `json:"applied,omitempty"`    // tag apply only
	Discovered []scanDiscoveredGroup `json:"discovered,omitempty"` // discover only
	Recover    []scanRecoverItem     `json:"recover,omitempty"`    // recover only
}

// scanTimeout is the upper bound for a single /api/scan/run invocation.
// Tag-mode against a ~5000-movie library against a LAN Radarr takes a few
// seconds (one GET /movie + one GET /tag/detail + a handful of editor
// batches). 60 seconds is generous for preview and realistic for apply
// even on slow indexers. Schedules running this as a background job can
// use a larger timeout — that path lands in a later commit.
const scanTimeout = 60 * time.Second

// Compile-time guard: this file uses arr.Item which must carry MovieFile.
// If the field is removed or renamed we want a build break here, not
// a silent nil deref at runtime.
var _ = func() any { return arr.Item{}.MovieFile }
