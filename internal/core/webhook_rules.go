package core

import (
	"strings"
	"time"

	"resolvarr/internal/core/engine"
)

// webhook_rules.go — saved-rule data model for the M-Webhook subsystem.
//
// Architectural twin: ScheduledJob in jobs.go. Same shape — ID + Name +
// Enabled + InstanceID + per-rule snapshot configs + History — adapted
// for the event-driven trigger model:
//
//   - No Cron field. Rules fire when matching Connect events arrive.
//   - Functions slice replaces JobMode. A rule can chain multiple
//     functions (Tag Audio + Tag Video + Recover all on Import) the
//     way scheduler combined-mode chains JobModes.
//   - Per-function settings (GrabRename criteria, qBit S/E rules) are
//     dedicated sub-structs because the bash GRAB_RENAME_* fields don't
//     map onto JobOptions.
//   - History entries are per fire (one rule + one Connect event), not
//     per cron run.
//
// Storage: top-level Config.WebhookRules — symmetric with Config.Schedules,
// not nested inside Instance. One config-file write per CRUD mutation
// (atomic .tmp → rename inherited from ConfigStore).

// WebhookFunction is one of the engine actions a webhook rule can fire
// in response to a Connect event. Modelled as named constants so the
// JSON wire-shape is stable + the per-Arr-type validator below can
// match against a canonical set rather than free-form strings.
type WebhookFunction string

const (
	WebhookFnTagReleaseGroups WebhookFunction = "tagReleaseGroups" // Radarr only — uses Active groups + Filters
	WebhookFnDiscover         WebhookFunction = "discover"         // Radarr only — surfaces unknown groups passing filters
	WebhookFnTagAudio         WebhookFunction = "tagAudio"         // Both Arrs
	WebhookFnTagVideo         WebhookFunction = "tagVideo"         // Both Arrs
	WebhookFnTagDvDetail      WebhookFunction = "tagDvDetail"      // Radarr only — Sonarr mediaInfo lacks DV fields
	WebhookFnRecover          WebhookFunction = "recover"          // Both Arrs
	WebhookFnSyncToSecondary  WebhookFunction = "syncToSecondary"  // Radarr only — TmdbID mirror
	WebhookFnFileDeleteClean  WebhookFunction = "fileDeleteClean"  // Both Arrs — MovieFileDelete / EpisodeFileDelete
	WebhookFnGrabRename       WebhookFunction = "grabRename"       // Both Arrs — qBit torrent rename on Grab
	WebhookFnQbitSeTag        WebhookFunction = "qbitSeTag"        // Sonarr only — qBit S/E tagging on Grab
	WebhookFnQbitCategoryFix  WebhookFunction = "qbitCategoryFix"  // Both Arrs — reconcile pre→post-import category on qBit if Arr's update silently failed
)

// allWebhookFunctions enumerates every defined function. Used by the
// validator (must be one of …) and by the wizard backend that surfaces
// the per-Arr-type checklist.
//
// WebhookFnFileDeleteClean is intentionally NOT in this list — the
// legacy all-or-nothing strip-on-delete function was retired in C7/C8
// of the M-webhook delete-semantics refactor. Per-bucket
// StripOnFileDelete flags on AudioTagsConfig / VideoTagsConfig /
// DvDetailConfig replace it. The constant itself stays defined +
// switched-on in WebhookFunctionAppliesTo / EventsForFunction so the
// C5 migration helper can still detect legacy-shape rules on first
// Load and convert them; new rules with the function in Functions
// fail validation here.
var allWebhookFunctions = []WebhookFunction{
	WebhookFnTagReleaseGroups,
	WebhookFnDiscover,
	WebhookFnTagAudio,
	WebhookFnTagVideo,
	WebhookFnTagDvDetail,
	WebhookFnRecover,
	WebhookFnSyncToSecondary,
	WebhookFnGrabRename,
	WebhookFnQbitSeTag,
	WebhookFnQbitCategoryFix,
}

// ValidWebhookFunction returns true when fn is one of the canonical
// constants. Used by the CRUD validator before persisting a rule.
func ValidWebhookFunction(fn WebhookFunction) bool {
	for _, candidate := range allWebhookFunctions {
		if candidate == fn {
			return true
		}
	}
	return false
}

// WebhookFunctionAppliesTo gates a function against an Arr type. Drives
// both the wizard (hide options that don't apply) and the validator
// (reject a Sonarr rule that ticks Tag Release Groups, etc.).
//
// "appType" matches Instance.Type — "radarr" or "sonarr". Empty / unknown
// types are rejected (returns false for every function) to keep the
// "implementation: each feature card carries an appliesTo declaration"
// rule honest.
func WebhookFunctionAppliesTo(fn WebhookFunction, appType string) bool {
	switch fn {
	case WebhookFnTagReleaseGroups, WebhookFnDiscover, WebhookFnTagDvDetail, WebhookFnSyncToSecondary:
		return appType == "radarr"
	case WebhookFnQbitSeTag:
		return appType == "sonarr"
	case WebhookFnTagAudio, WebhookFnTagVideo, WebhookFnRecover, WebhookFnFileDeleteClean, WebhookFnGrabRename, WebhookFnQbitCategoryFix:
		return appType == "radarr" || appType == "sonarr"
	}
	return false
}

// WebhookConnectEvent is the Connect-event discriminator we filter on.
// Sonarr/Radarr both emit `eventType` strings as listed below; we only
// model the ones that fire engine actions. Test / Health / Rename /
// MovieAdded / SeriesAdd are received + logged but don't dispatch.
type WebhookConnectEvent string

const (
	WebhookEventGrab               WebhookConnectEvent = "Grab"
	WebhookEventDownload           WebhookConnectEvent = "Download" // Sonarr/Radarr both — covers initial import + upgrade (isUpgrade flag distinguishes)
	WebhookEventMovieFileDelete    WebhookConnectEvent = "MovieFileDelete"
	WebhookEventEpisodeFileDelete  WebhookConnectEvent = "EpisodeFileDelete"
	// *ForUpgrade variants: Radarr/Sonarr emit these when a file is
	// deleted as part of an Upgrade flow (the old file makes way for
	// a higher-quality replacement). Bash tagarr_import.sh:574
	// defenderer mot begge varianter; container må også slå tag-
	// cleanup på begge så stale managed-tags ikke overlever upgrade
	// (file with old audio gets replaced; file-delete adapter must
	// fire to strip).
	WebhookEventMovieFileDeleteForUpgrade   WebhookConnectEvent = "MovieFileDeleteForUpgrade"
	WebhookEventEpisodeFileDeleteForUpgrade WebhookConnectEvent = "EpisodeFileDeleteForUpgrade"
)

// EventsForFunction returns the Connect events a function dispatches on.
// Used by the dispatcher (only iterate rules that match the incoming
// event) and by the wizard's Step 4 summary (compute the union of
// events the picked functions need so the user knows which Sonarr/Radarr
// notification triggers to enable).
//
// Returns nil when fn doesn't apply to appType OR when fn has no event
// dispatch (FileDeleteClean with empty/unknown appType falls through).
// Contract is symmetric with WebhookFunctionAppliesTo: both gate
// hand-edited / tampered configs from firing rules with empty AppType
// — defence in depth. Don't drop the per-appType branches in the
// FileDeleteClean case without also revisiting WebhookFunctionAppliesTo.
func EventsForFunction(fn WebhookFunction, appType string) []WebhookConnectEvent {
	// Gate every branch on AppliesTo so a tampered config with empty
	// or wrong AppType can't dispatch a function that doesn't belong
	// (e.g. a Sonarr rule with TagReleaseGroups would otherwise match
	// Download events here even though AppliesTo says no). Defence in
	// depth — the validator catches this at save-time too.
	if !WebhookFunctionAppliesTo(fn, appType) {
		return nil
	}
	switch fn {
	case WebhookFnTagReleaseGroups, WebhookFnDiscover, WebhookFnTagAudio, WebhookFnTagVideo,
		WebhookFnTagDvDetail, WebhookFnRecover, WebhookFnSyncToSecondary, WebhookFnQbitCategoryFix:
		return []WebhookConnectEvent{WebhookEventDownload}
	case WebhookFnFileDeleteClean:
		if appType == "radarr" {
			return []WebhookConnectEvent{WebhookEventMovieFileDelete, WebhookEventMovieFileDeleteForUpgrade}
		}
		// Sonarr — already validated by AppliesTo above.
		return []WebhookConnectEvent{WebhookEventEpisodeFileDelete, WebhookEventEpisodeFileDeleteForUpgrade}
	case WebhookFnGrabRename, WebhookFnQbitSeTag:
		return []WebhookConnectEvent{WebhookEventGrab}
	}
	return nil
}

// GrabRenameCriteria carries the user's per-rule rename rules. Mirrors
// the bash tagarr_import.conf.sample GRAB_RENAME_* fields with a few
// container-natural additions. Only meaningful when the rule has
// WebhookFnGrabRename in its Functions list — nil-pointer-safe in the
// dispatcher (a missing struct treats every check as "off").
//
// Architectural rule: this struct holds title-only token preservation
// only. MediaInfo-derived tokens (HDR / DV / codec / channels /
// resolution) are NOT in scope — Radarr/Sonarr read those from the
// file directly. See memory reference_tagarr_rename_design_principle.md
// for the full reasoning + how new criteria get evaluated.
//
// v1 rename target: torrent display name only (qBit /api/v2/torrents/
// rename). File rename is task-#8b if torrent rename proves
// insufficient on real-world testing.
//
// Trigger model (v1): all-or-nothing per category, OR'd together. A
// rename fires when ≥1 enabled trigger detects a diff between current
// qBit name and grab title (OR TriggerAlways=true). Each category
// has its own built-in token list (TRaSH-derived where applicable);
// users opt categories on/off but don't pick individual tokens (keeps
// the wizard simple).
type GrabRenameCriteria struct {
	// RenameTarget controls the rename surface. v1 only "torrent" is
	// wired; "file" / "both" land if torrent-only proves insufficient
	// for Radarr's import-time filename parser. Empty defaults to
	// "torrent" via doc-comment + adapter fallback.
	RenameTarget string `json:"renameTarget,omitempty"` // "torrent" | "file" | "both"

	// Triggers — all-or-nothing per category. OR'd; rename fires when
	// ≥1 trigger has a diff (or TriggerAlways=true).

	// TriggerOnMissingReleaseGroup: rename when the rg-token is not
	// extractable from current qBit name via Radarr's strict filename
	// parser (engine.ParseReleaseGroupFromFilename). Catches the
	// Rango/Matilda failure mode where rg is visually present but
	// the parser rejects on multi-token (space-dash-space).
	TriggerOnMissingReleaseGroup bool `json:"triggerOnMissingReleaseGroup,omitempty"`

	// TriggerOnMovieVersionMismatch: rename when grab title contains
	// a TRaSH "Optional Movie Versions" token the qBit name lacks.
	// Radarr-only (TV releases don't use these tokens).
	// Built-in token list: Director's Cut / Theatrical / Extended /
	// Unrated / Uncut / Remaster / Criterion / Masters of Cinema /
	// Vinegar Syndrome / Hybrid / IMAX / Open Matte. TRaSH source:
	// CF group f4f1474b963b24cf983455743aa9906c.
	TriggerOnMovieVersionMismatch bool `json:"triggerOnMovieVersionMismatch,omitempty"`

	// TriggerOnSourceMismatch: rename when grab title contains a
	// streaming-source token the qBit name lacks. Built-in token
	// list: MA / Play / AMZN / NF / DSNP / HMAX / HULU / PCOK / CR /
	// ATVP. TRaSH source: per-service CF entries.
	TriggerOnSourceMismatch bool `json:"triggerOnSourceMismatch,omitempty"`

	// TriggerOnAudioMismatch: rename when grab title contains an
	// audio-format token the qBit name lacks. Usually cosmetic for
	// Radarr (MediaInfo-derived) but useful when CFs score on the
	// release title. Built-in token list: TRaSH audio-formats.json
	// (TrueHD / Atmos / DTS-HD MA / DTS-X / DTS-ES / EAC3 Atmos).
	TriggerOnAudioMismatch bool `json:"triggerOnAudioMismatch,omitempty"`

	// TriggerOnSceneMismatch: nuanced — fire rename when the current
	// torrent name looks scene-stripped (has WEB without WEB-DL, etc.)
	// AND the release-group is NOT in the TRaSH Scene CF group list.
	// If rg IS a known scene group, leave it (legit scene release;
	// preserve scoring). Replaces the bash ExcludeSceneReleases flag
	// with a smarter detection.
	TriggerOnSceneMismatch bool `json:"triggerOnSceneMismatch,omitempty"`

	// TriggerAlways: bypass all token checks; rename if current qBit
	// name differs from grab title at all. Cosmetic-churn risk but
	// useful for users with custom CF setups not covered by the
	// built-in trigger list. Default off.
	TriggerAlways bool `json:"triggerAlways,omitempty"`

	// CustomTokens is the bash GRAB_RENAME_CUSTOM_TOKENS escape hatch —
	// each entry is a "Label:regex" pair. When ANY custom token
	// matches grab title but not qBit name, rename fires (independent
	// of the TriggerOn* flags above). Validation runs at save-time
	// (regex compile + length cap) so malformed entries reject early.
	CustomTokens []GrabRenameCustomToken `json:"customTokens,omitempty"`

	// GroupBlocklist is a list of release-group names that should NEVER
	// have rename applied — for groups where the user wants the original
	// tracker name preserved verbatim. Case-insensitive match.
	GroupBlocklist []string `json:"groupBlocklist,omitempty"`

	// QbitInstanceID picks which Config.QbitInstances entry the renamer
	// authenticates against. Empty = error (rule won't save). Multiple
	// rules can share one QbitInstanceID — credentials don't get
	// duplicated.
	QbitInstanceID string `json:"qbitInstanceId"`

	// Deprecated fields preserved for back-compat decode (older saved
	// rules from before the trigger-based model land cleanly):

	// AppendReleaseGroup — formerly the master "append rg when missing"
	// toggle (default true). Replaced by TriggerOnMissingReleaseGroup.
	// Kept on the struct so existing JSON decodes; ignored at fire-time.
	// Migrated on Load: when AppendReleaseGroup=true (or nil → defaulted
	// true) AND TriggerOnMissingReleaseGroup is false (default for
	// migrated configs), set the new flag to preserve user intent.
	AppendReleaseGroup *bool `json:"appendReleaseGroup,omitempty"`

	// SourceTokens / MovieVersionTokens — formerly per-token allow-lists
	// (subset of built-ins to preserve). Replaced by category-level
	// trigger flags. Kept for decode compat; the new model is all-or-
	// nothing per category. Empty / populated values both decode OK
	// but are ignored at fire-time.
	SourceTokens       []string `json:"sourceTokens,omitempty"`
	MovieVersionTokens []string `json:"movieVersionTokens,omitempty"`

	// ExcludeSceneReleases — formerly the "skip rename when scene"
	// toggle. Replaced by the smarter TriggerOnSceneMismatch + scene-
	// CF-group lookup logic. Kept for decode compat; ignored at fire-
	// time (the new TriggerOnSceneMismatch covers both directions).
	ExcludeSceneReleases bool `json:"excludeSceneReleases,omitempty"`
}

// GrabRenameTarget enumerates the supported rename surfaces for a
// Grab Rename rule. v1 only "torrent" is wired; the other values are
// declared so the wire-shape stays stable when "file" / "both" land.
const (
	GrabRenameTargetTorrent = "torrent" // qBit display name only (v1; safest, no disk impact)
	GrabRenameTargetFile    = "file"    // qBit's largest video file (renames on disk)
	GrabRenameTargetBoth    = "both"    // torrent + file
)

// ValidGrabRenameTarget returns true when t is one of the supported
// rename-target enum values OR the empty string (which the dispatcher
// resolves to GrabRenameTargetTorrent default at fire-time). Used by
// the rule validator before persistence.
func ValidGrabRenameTarget(t string) bool {
	switch t {
	case "", GrabRenameTargetTorrent, GrabRenameTargetFile, GrabRenameTargetBoth:
		return true
	}
	return false
}

// GrabRenameCustomToken is one user-defined "Label:regex" entry.
type GrabRenameCustomToken struct {
	Label string `json:"label"`
	Regex string `json:"regex"`
}

// AppendReleaseGroupOrDefault resolves the *bool field to a concrete
// bool, defaulting to true when the pointer is nil. Kept for back-
// compat reads of the legacy field; new code should consult
// TriggerOnMissingReleaseGroup directly.
func (c *GrabRenameCriteria) AppendReleaseGroupOrDefault() bool {
	if c == nil || c.AppendReleaseGroup == nil {
		return true
	}
	return *c.AppendReleaseGroup
}

// MigrateLegacyTriggerFlags backfills the new TriggerOn* flags on a
// GrabRenameCriteria struct loaded from older JSON that pre-dates the
// trigger-based model. Idempotent — re-running on already-migrated
// data is a no-op.
//
// Migration rules (preserve user intent from the old model):
//
//   AppendReleaseGroup is nil OR true → TriggerOnMissingReleaseGroup=true
//   AppendReleaseGroup is false       → TriggerOnMissingReleaseGroup=false
//
//   len(MovieVersionTokens) > 0       → TriggerOnMovieVersionMismatch=true
//                                        (user picked tokens — they care about
//                                        this category)
//   len(SourceTokens) > 0             → TriggerOnSourceMismatch=true
//   ExcludeSceneReleases=true         → leave it (nuanced TriggerOnSceneMismatch
//                                        replaces but isn't a 1:1 swap; user's
//                                        old scene-exclusion intent stays valid
//                                        as a separate signal until they update
//                                        the rule)
//
// Migration runs only when ALL TriggerOn* flags are false (the unmigrated
// state). Setting any trigger to true is treated as "this rule has been
// migrated" and the helper does nothing.
func (c *GrabRenameCriteria) MigrateLegacyTriggerFlags() {
	if c == nil {
		return
	}
	// Already migrated — at least one trigger is set.
	if c.TriggerOnMissingReleaseGroup ||
		c.TriggerOnMovieVersionMismatch ||
		c.TriggerOnSourceMismatch ||
		c.TriggerOnAudioMismatch ||
		c.TriggerOnSceneMismatch ||
		c.TriggerAlways {
		return
	}
	// Migrate AppendReleaseGroup default-true.
	if c.AppendReleaseGroup == nil || *c.AppendReleaseGroup {
		c.TriggerOnMissingReleaseGroup = true
	}
	if len(c.MovieVersionTokens) > 0 {
		c.TriggerOnMovieVersionMismatch = true
	}
	if len(c.SourceTokens) > 0 {
		c.TriggerOnSourceMismatch = true
	}
	// Legacy ExcludeSceneReleases=true → TriggerOnSceneMismatch=true.
	// Semantic shift: legacy meant "skip rename when scene", new means
	// "fire rename when current looks scene-stripped AND rg is NOT a
	// known scene group". Both express user intent to "do something
	// special with scene-related releases" — the new flag's nuanced
	// behaviour preserves CF-scoring for legit scene releases (rg in
	// scene-CF list) while fixing fake-scene cases (rg not in list →
	// likely P2P with stripped tokens). Closer to what the user was
	// trying to express than silently ignoring the legacy intent.
	if c.ExcludeSceneReleases {
		c.TriggerOnSceneMismatch = true
	}
}

// QbitSeRules holds the Sonarr-only qBit tag rules. Three-rule
// first-match-wins model mirroring the community Python
// qbittorrent_auto_tagger.py reference: each torrent name is run
// against Episode → Season → Unmatched in order, and the first
// matching enabled rule contributes ONE tag. Patterns are hardcoded
// in engine.qbit_se.go (battle-tested defaults); user controls the
// three Enabled toggles + the three tag names.
type QbitSeRules struct {
	// QbitInstanceID picks which qBit gets the tags. Same pairing model
	// as GrabRenameCriteria.QbitInstanceID — different rules can target
	// different qBits if the user runs separate qBit instances per
	// tracker / category.
	QbitInstanceID string `json:"qbitInstanceId"`

	// Episode rule — fires on S01E05 / S01E05E06 multi-ep / daily-show
	// 2024.10.15 patterns. Default tag name "Episode".
	EpisodeEnabled bool   `json:"episodeEnabled,omitempty"`
	EpisodeTag     string `json:"episodeTag,omitempty"`

	// Season rule — fires on bare S01 / Season 1 patterns when no
	// episode token is present (Episode wins when both could match).
	// Default tag name "Season".
	SeasonEnabled bool   `json:"seasonEnabled,omitempty"`
	SeasonTag     string `json:"seasonTag,omitempty"`

	// Unmatched rule — catch-all when neither Episode nor Season
	// matched. Lets the user filter qBit by "everything that isn't
	// recognisable TV" (movies, music, software, oddly-named TV).
	// Default tag name "Unmatched".
	UnmatchedEnabled bool   `json:"unmatchedEnabled,omitempty"`
	UnmatchedTag     string `json:"unmatchedTag,omitempty"`

	// Legacy fields preserved for decode compat with rules saved before
	// the three-rule first-match-wins model. Backfilled on Load via
	// MigrateLegacyQbitSeFlags; ignored at fire-time once migrated.
	// Never written by current code (omitempty).
	TagSeason  bool `json:"tagSeason,omitempty"`
	TagEpisode bool `json:"tagEpisode,omitempty"`
}

// MigrateLegacyQbitSeFlags backfills the new three-rule fields on a
// QbitSeRules struct loaded from older JSON that pre-dates the
// first-match-wins model. Idempotent — re-running on already-migrated
// data is a no-op.
//
// Migration rules:
//
//	legacy TagEpisode=true → EpisodeEnabled=true (otherwise false)
//	legacy TagSeason=true  → SeasonEnabled=true  (otherwise false)
//	UnmatchedEnabled       → true (new default — gives users the
//	                         "everything else" filter immediately)
//	*Tag fields             → defaults ("Episode" / "Season" / "Unmatched")
//	                          whenever empty
//
// Detection: a rule is considered legacy when none of EpisodeTag /
// SeasonTag / UnmatchedTag is populated AND no new-style Enabled flag
// is true. After migration the rule has the three default tag names
// set, so the same detection on a second pass identifies the rule as
// already-migrated and skips. Tag-name backfill (for migrated rules
// that user later cleared the name on) runs unconditionally so a
// blank tag name always resolves to a sensible default at fire-time.
func (r *QbitSeRules) MigrateLegacyQbitSeFlags() {
	if r == nil {
		return
	}
	// Already migrated when any new-style Enabled flag is set OR any
	// new-style tag name is populated. Idempotency contract: pristine
	// new-style rule (e.g. created by the wizard) lands here with at
	// least one EnabledFlag=true via the seed defaults — migration
	// short-circuits before clobbering the user's choice.
	migrated := r.EpisodeEnabled || r.SeasonEnabled || r.UnmatchedEnabled ||
		strings.TrimSpace(r.EpisodeTag) != "" ||
		strings.TrimSpace(r.SeasonTag) != "" ||
		strings.TrimSpace(r.UnmatchedTag) != ""
	if !migrated {
		// Legacy rule — backfill from old TagSeason / TagEpisode bools.
		r.EpisodeEnabled = r.TagEpisode
		r.SeasonEnabled = r.TagSeason
		// Unmatched defaults ON for migrated rules — gives users the
		// new "everything else" bucket immediately, matches Python
		// reference's "always have a fallback" posture.
		r.UnmatchedEnabled = true
	}
	// Tag-name defaults: backfill blanks every Load. Cheap + keeps
	// fire-time output sensible even when a user manually cleared a
	// tag name in their config file.
	if strings.TrimSpace(r.EpisodeTag) == "" {
		r.EpisodeTag = "Episode"
	}
	if strings.TrimSpace(r.SeasonTag) == "" {
		r.SeasonTag = "Season"
	}
	if strings.TrimSpace(r.UnmatchedTag) == "" {
		r.UnmatchedTag = "Unmatched"
	}
}

// QbitCategoryFixRules carries the per-rule config for the qBit
// Category Fix function. Sonarr/Radarr is supposed to update a torrent's
// qBit category from pre-import (e.g. "qbit-movies") to post-import
// ("qbit-movies-imp") when an import completes — but that update
// sometimes silently fails (qBit API timeout, version-specific bug,
// race). This function listens for Import events, verifies the import
// really happened, and corrects the category if it's stuck on the pre-
// import value.
//
// Categories are NOT stored on the rule directly. The rule stores a
// reference to one of Sonarr/Radarr's download-client entries
// (ArrDownloadClientID); the adapter fetches the live download-client
// config at fire-time (cached 5min) to resolve the pre/post category
// names. This avoids drift if the user renames categories in
// Sonarr/Radarr later.
//
// PreImportCategorySnapshot + PostImportCategorySnapshot are captured
// at save-time as fallback if the live fetch fails at fire-time (Arr
// unreachable, API key revoked, etc.). The UI's "Refresh from Arr"
// button re-syncs both snapshot + frontend cache.
type QbitCategoryFixRules struct {
	// QbitInstanceID picks which qBit gets the category change applied.
	// Same pairing model as GrabRenameCriteria.QbitInstanceID — multiple
	// rules can share one QbitInstanceID without duplicating creds.
	QbitInstanceID string `json:"qbitInstanceId"`

	// ArrDownloadClientID identifies the qBit download-client entry in
	// Sonarr/Radarr's Settings → Download Clients. Resolved at save-
	// time + re-fetched at fire-time (cached 5min) to pull fresh pre/
	// post category names. Type-int because the Arr's API uses an int
	// primary key on download-client rows.
	ArrDownloadClientID int `json:"arrDownloadClientId"`

	// PreImportCategorySnapshot is the category Sonarr/Radarr's
	// download-client config reports for the "before import" state at
	// the time the rule was saved. Used as a fallback when the live
	// fetch fails at fire-time. Auto-refreshed by the UI's "Refresh
	// from Arr" button.
	PreImportCategorySnapshot string `json:"preImportCategorySnapshot"`

	// PostImportCategorySnapshot is the category Sonarr/Radarr's
	// download-client config reports for the "after import" state at
	// the time the rule was saved. Same fallback role + UI refresh
	// flow as the pre-import snapshot.
	PostImportCategorySnapshot string `json:"postImportCategorySnapshot"`
}

// WebhookRule is one saved webhook workflow — InstanceID + Functions +
// per-function snapshots + History. The wizard creates these via the
// rule editor (parallel to the scheduler-rule editor); the dispatcher
// fires them when matching Connect events arrive.
//
// Per-rule snapshots: same architectural rule as ScheduledJob. The
// rule reads its own Filters / AudioTags / VideoTags / DvDetail /
// ReleaseGroupIDs at fire-time, NOT the global Library-scan config.
// Changing the global UI does NOT affect existing rules — that's the
// whole point of the rule model. Migration on Load backfills nil
// fields with snapshots of the matching globals (one-shot, persisted).
type WebhookRule struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Enabled    bool   `json:"enabled"`
	InstanceID string `json:"instanceId"`
	AppType    string `json:"appType"` // "radarr" | "sonarr" — denormalised from the linked instance for fast filter

	// Functions is the ordered set of engine actions this rule fires.
	// Order is informational; the dispatcher executes in a fixed order
	// (see scheduler_runner's combined-mode chain: Discover → Recover →
	// Tag → Audio → Video → DV → File-delete → qBit). Functions can
	// repeat across rules — two rules each with TagAudio is fine, both
	// fire on the same event.
	Functions []WebhookFunction `json:"functions"`

	// Per-function snapshots. nil = "use global at fire-time" pre-
	// migration; populated post-migration on first Load. Same shape as
	// ScheduledJob.

	Filters         *engine.FilterConfig `json:"filters,omitempty"`
	AudioTags       *AudioTagsConfig     `json:"audioTags,omitempty"`
	VideoTags       *VideoTagsConfig     `json:"videoTags,omitempty"`
	DvDetail        *DvDetailConfig      `json:"dvDetail,omitempty"`
	ReleaseGroupIDs []string             `json:"releaseGroupIds,omitempty"`

	// SyncToInstanceID picks the target instance for the SyncToSecondary
	// function. Empty + the function enabled = scheduler-style "first
	// other of same type" pick at fire-time.
	SyncToInstanceID string `json:"syncToInstanceId,omitempty"`

	// TagSource picks how the Tag-quality-releases function decides tags.
	// "" or "active" → match against rule's ReleaseGroupIDs (or globals);
	// "filter-only" → ignore groups entirely, apply the single FilterOnlyTag
	// to every movie/series passing Filters. Mirrors the same field on
	// ScheduledJob.options + scanRunRequest. Empty default = active
	// (legacy semantics — keeps existing rules firing through the
	// per-group Tag-RG branch).
	TagSource string `json:"tagSource,omitempty"`

	// FilterOnlyTag is the single tag emitted by filter-only mode.
	// Required when TagSource == "filter-only" and WebhookFnTagReleaseGroups
	// is in Functions; ignored otherwise. Validated against
	// `^[a-z0-9][a-z0-9_-]*$` (Radarr's tag-label regex) at save-time.
	// Default `lossless-web` is seeded by the wizard, never by load.
	FilterOnlyTag string `json:"filterOnlyTag,omitempty"`

	// DiscoverAutoEnable controls how new release-groups land when
	// Discover fires:
	//   - true:  added as Enabled=true (active — bash AUTO_TAG_DISCOVERED=true)
	//   - false: added as Enabled=false (commented — bash AUTO_TAG_DISCOVERED=false)
	// Only meaningful when WebhookFnDiscover is in Functions.
	// Default false matches bash conf default: discovered groups land
	// commented for manual review.
	DiscoverAutoEnable bool `json:"discoverAutoEnable,omitempty"`

	// SyncSkipOrphanCleanup mirrors the Library-scan rule of the same
	// name. Default behaviour is skip-orphan-cleanup (least-surprise on
	// Connect-driven fires); flipping this on enables full bash-parity
	// orphan removal on the secondary.
	SyncSkipOrphanCleanup bool `json:"syncSkipOrphanCleanup,omitempty"`

	// GrabRename + QbitSe + QbitCategoryFix are the per-function detail
	// configs. Pointer types so the JSON omits them when the function
	// isn't enabled.
	GrabRename      *GrabRenameCriteria   `json:"grabRename,omitempty"`
	QbitSe          *QbitSeRules          `json:"qbitSe,omitempty"`
	QbitCategoryFix *QbitCategoryFixRules `json:"qbitCategoryFix,omitempty"`

	// History holds the last N fires. Cap matches ScheduledJob (7) for
	// consistency in the Activity tab. Each entry is one (rule × event)
	// pair — rule firing on a Grab event with three functions enabled
	// produces ONE history entry summarising all three function results,
	// not three separate entries.
	History []WebhookRuleRun `json:"history,omitempty"`
}

// WebhookRuleRun summarises one rule fire. Modelled after JobRun so
// the Activity tab can render scheduler runs + webhook fires in the
// same row format. Status semantics match: "ok" = every function
// succeeded; "partial" = at least one function reported a per-item
// error; "error" = the rule couldn't fire at all (missing instance,
// qBit unreachable, etc.).
type WebhookRuleRun struct {
	StartedAt   time.Time `json:"startedAt"`
	DurationMs  int64     `json:"durationMs"`
	Status      string    `json:"status"`
	EventType   string    `json:"eventType"`             // "Grab" | "Download" | "MovieFileDelete" | "EpisodeFileDelete"
	ItemTitle   string    `json:"itemTitle,omitempty"`   // movie/series title from the Connect payload
	ItemContext string    `json:"itemContext,omitempty"` // year (Radarr) or "S01E05" (Sonarr)
	// ReleaseTitle — indexer release title from `release.releaseTitle`
	// on Grab events, or `movieFile.sceneName` / `episodeFile.sceneName`
	// on Download events when present. Lets the user verify what the
	// scene/indexer label was at fire-time without cross-referencing
	// the raw event JSON.
	ReleaseTitle string `json:"releaseTitle,omitempty"`
	// FilePath — `movieFile.relativePath` / `episodeFile.relativePath`
	// on Download/Delete events. The post-Radarr/Sonarr-rename filename
	// — usually different from ReleaseTitle. Critical for diagnosing
	// why a grab-time decision and an import-time decision drift apart
	// (e.g. release was X, Radarr renamed to Y, downstream tool reads Y).
	FilePath string `json:"filePath,omitempty"`
	Summary  string `json:"summary"` // short, e.g. "tagAudio: 3 added; grabRename: skipped (already correct)"
	LogPath  string `json:"logPath,omitempty"`
}

// FunctionsAppliesToCheck returns the first function on the rule that
// doesn't apply to the rule's AppType — for the validator. Empty
// string + nil error = clean. Used by handleCreateWebhookRule /
// handleUpdateWebhookRule before persisting.
func (r *WebhookRule) FunctionsApplyToType() WebhookFunction {
	for _, fn := range r.Functions {
		if !WebhookFunctionAppliesTo(fn, r.AppType) {
			return fn
		}
	}
	return ""
}

// HasFunction returns true when fn is in the rule's Functions slice.
// Used by the dispatcher to skip irrelevant function-call paths.
func (r *WebhookRule) HasFunction(fn WebhookFunction) bool {
	for _, f := range r.Functions {
		if f == fn {
			return true
		}
	}
	return false
}

// FiresOn returns true when the rule has at least one enabled function
// that dispatches on the given Connect event. Used by the dispatcher
// to filter rules per incoming event before walking their function
// list.
//
// Does NOT include the automatic Tag-RG strip-on-delete flow — that
// has its own gate (FiresAutoStripOnDelete) since it isn't driven by
// the user's Functions list. The dispatcher OR-combines both before
// deciding to enter the per-rule block.
func (r *WebhookRule) FiresOn(event WebhookConnectEvent) bool {
	for _, fn := range r.Functions {
		for _, e := range EventsForFunction(fn, r.AppType) {
			if e == event {
				return true
			}
		}
	}
	return false
}

// FiresAutoStripOnDelete returns true when the rule must run the
// automatic Tag-RG strip-on-delete flow for the given event. Gated on:
//   - AppType == "radarr" (Tag-RG is Radarr-only)
//   - rule has WebhookFnTagReleaseGroups in Functions
//   - event is MovieFileDelete or MovieFileDeleteForUpgrade
//
// Auto-strip enforces the Tag-RG invariant: primary's qualification
// is the single source of truth for release-group / filter-only tags.
// When the file disappears, primary no longer qualifies → tag must
// fall off both primary and (when Sync-to-Secondary is on) secondary
// — same flow as bash tagarr_import.sh:574+ with ENABLE_SYNC_TO_
// SECONDARY=true. NOT a user-toggleable function; intrinsic to having
// Tag-RG on the rule at all.
func (r *WebhookRule) FiresAutoStripOnDelete(event WebhookConnectEvent) bool {
	if r == nil || r.AppType != "radarr" {
		return false
	}
	if !r.HasFunction(WebhookFnTagReleaseGroups) {
		return false
	}
	return event == WebhookEventMovieFileDelete ||
		event == WebhookEventMovieFileDeleteForUpgrade
}

// FiresPerBucketStripOnDelete returns true when the rule should run
// the file-delete cleanup on the given event by virtue of per-bucket
// StripOnFileDelete opt-in (C6 of the M-webhook delete-semantics
// refactor). Independent of the legacy WebhookFnFileDeleteClean gate:
// covers post-C5 rules where the user opted in via Audio / Video /
// DV bucket snapshots instead of the all-or-nothing legacy function.
//
// Returns true when:
//   - event is the file-delete pair matching the rule's AppType
//     (MovieFileDelete{,ForUpgrade} for radarr; EpisodeFileDelete
//     {,ForUpgrade} for sonarr), AND
//   - at least one of the rule's bucket snapshots has
//     StripOnFileDelete=true (DV-detail counted Radarr-only — the
//     dispatcher's buildFileDeleteManagedSet already gates DV by
//     AppType).
//
// Snapshot nil → bucket falls back to globals at fire-time; the gate
// is intentionally snapshot-only so a global flip (no UI today) does
// not silently fan out strip-on-delete to every rule. C5 migration
// materialises snapshots for legacy-fn rules so they pass this gate
// post-migration without any UI change.
func (r *WebhookRule) FiresPerBucketStripOnDelete(event WebhookConnectEvent) bool {
	if r == nil {
		return false
	}
	switch r.AppType {
	case "radarr":
		if event != WebhookEventMovieFileDelete && event != WebhookEventMovieFileDeleteForUpgrade {
			return false
		}
	case "sonarr":
		if event != WebhookEventEpisodeFileDelete && event != WebhookEventEpisodeFileDeleteForUpgrade {
			return false
		}
	default:
		return false
	}
	if r.AudioTags != nil && r.AudioTags.StripOnFileDelete {
		return true
	}
	if r.VideoTags != nil && r.VideoTags.StripOnFileDelete {
		return true
	}
	if r.AppType == "radarr" && r.DvDetail != nil && r.DvDetail.StripOnFileDelete {
		return true
	}
	return false
}

// ConnectEventsNeeded returns the union of Connect events the rule's
// enabled functions dispatch on. Drives the wizard's Step 4 summary
// "Toggle on these notification triggers in Sonarr/Radarr" list.
//
// Includes the auto-strip-on-delete events when applicable so the
// wizard tells Tag-RG users to enable Movie File Delete notifications
// even when they haven't picked a function that nominally dispatches
// on that event (the auto-strip would otherwise silently never fire
// because the Connect side never sends the event).
func (r *WebhookRule) ConnectEventsNeeded() []WebhookConnectEvent {
	seen := map[WebhookConnectEvent]bool{}
	out := []WebhookConnectEvent{}
	for _, fn := range r.Functions {
		for _, e := range EventsForFunction(fn, r.AppType) {
			if seen[e] {
				continue
			}
			seen[e] = true
			out = append(out, e)
		}
	}
	// Auto-strip-on-delete adds MovieFileDelete + ForUpgrade for any
	// Radarr Tag-RG rule, regardless of whether the user also ticked
	// FileDeleteClean / Recover / etc. Without surfacing these the
	// wizard would tell the user "no Connect events needed" while the
	// invariant silently depends on the Connect-side firing them.
	if r != nil && r.AppType == "radarr" && r.HasFunction(WebhookFnTagReleaseGroups) {
		for _, e := range []WebhookConnectEvent{WebhookEventMovieFileDelete, WebhookEventMovieFileDeleteForUpgrade} {
			if seen[e] {
				continue
			}
			seen[e] = true
			out = append(out, e)
		}
	}
	// Per-bucket strip-on-delete surfaces the matching file-delete
	// events whenever any Audio / Video / DV-detail snapshot opts in,
	// so the wizard tells the user to enable the right Connect-side
	// notifications. Mirrors the auto-strip surfacing above — without
	// it, a user who ticks StripOnFileDelete but no other delete-
	// firing function would see "no triggers needed" while the strip
	// silently depends on the Connect-side delivering the event.
	if r != nil {
		var deleteEvents []WebhookConnectEvent
		switch r.AppType {
		case "radarr":
			deleteEvents = []WebhookConnectEvent{WebhookEventMovieFileDelete, WebhookEventMovieFileDeleteForUpgrade}
		case "sonarr":
			deleteEvents = []WebhookConnectEvent{WebhookEventEpisodeFileDelete, WebhookEventEpisodeFileDeleteForUpgrade}
		}
		surfaceDeletes := false
		for _, e := range deleteEvents {
			if r.FiresPerBucketStripOnDelete(e) {
				surfaceDeletes = true
				break
			}
		}
		if surfaceDeletes {
			for _, e := range deleteEvents {
				if seen[e] {
					continue
				}
				seen[e] = true
				out = append(out, e)
			}
		}
	}
	return out
}

// NormalizeWebhookRule trims whitespace + lowercases AppType + drops
// duplicate functions. Idempotent. Called from the request validator
// before persistence so what gets saved is the canonical shape.
func NormalizeWebhookRule(r *WebhookRule) {
	r.Name = strings.TrimSpace(r.Name)
	r.AppType = strings.ToLower(strings.TrimSpace(r.AppType))
	r.InstanceID = strings.TrimSpace(r.InstanceID)
	r.SyncToInstanceID = strings.TrimSpace(r.SyncToInstanceID)
	r.TagSource = strings.TrimSpace(r.TagSource)
	r.FilterOnlyTag = strings.TrimSpace(r.FilterOnlyTag)

	seen := map[WebhookFunction]bool{}
	out := r.Functions[:0]
	for _, fn := range r.Functions {
		// Trim whitespace before the dedup check — a request with
		// "tagAudio " (trailing space) would otherwise survive
		// normalisation, fail ValidWebhookFunction at validate-time
		// with a confusing "unknown function: tagAudio " error.
		// Trimming here gives the validator a clean canonical token.
		fn = WebhookFunction(strings.TrimSpace(string(fn)))
		if fn == "" || seen[fn] {
			continue
		}
		seen[fn] = true
		out = append(out, fn)
	}
	r.Functions = out

	if r.GrabRename != nil {
		r.GrabRename.QbitInstanceID = strings.TrimSpace(r.GrabRename.QbitInstanceID)
	}
	if r.QbitSe != nil {
		r.QbitSe.QbitInstanceID = strings.TrimSpace(r.QbitSe.QbitInstanceID)
	}
	if r.QbitCategoryFix != nil {
		r.QbitCategoryFix.QbitInstanceID = strings.TrimSpace(r.QbitCategoryFix.QbitInstanceID)
		r.QbitCategoryFix.PreImportCategorySnapshot = strings.TrimSpace(r.QbitCategoryFix.PreImportCategorySnapshot)
		r.QbitCategoryFix.PostImportCategorySnapshot = strings.TrimSpace(r.QbitCategoryFix.PostImportCategorySnapshot)
	}
}
