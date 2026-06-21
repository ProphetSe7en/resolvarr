package api

// webhook_rules.go — CRUD handlers for WebhookRule entries.
//
// Architectural twin: schedules.go. Same request → validate → persist
// → echo-back shape adapted for the M-Webhook rule model. Persistence
// goes through ConfigStore.Update (atomic .tmp → rename inherited);
// dispatcher reads cfg.WebhookRules at receive-time so no Reload-style
// hot-reload is needed.
//
// Validation gates: name non-empty, AppType valid + matches the linked
// instance's Type, every Function valid + applies to the AppType, the
// linked instance exists, optional SyncToInstanceID exists + matches
// AppType, qBit pairings exist when their function is enabled.

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"resolvarr/internal/core"
	"resolvarr/internal/core/engine"
)

// reQbitCategoryName mirrors qBit's actual permissive char rule for
// category names. qBit accepts most printable strings — slashes (for
// nested categories like `sonarr/imported`), spaces (`qbit movies`),
// dots (`qbit.movies`), unicode etc. all work in practice on current
// qBit releases. The only chars that genuinely break things are ASCII
// control chars (0x00-0x1f) and `\` (Windows path separator — qBit
// stores the category as a directory name on disk).
//
// Sonarr/Radarr's download-client UI is happy with any string; the
// `autoFillQbitCategories` UI in resolvarr auto-populates from the live
// Arr data, so a too-strict regex here rejects real-world configs at
// save-time with a confusing error message. Permissive set keeps the
// floor low while still blocking the two genuinely dangerous classes.
var reQbitCategoryName = regexp.MustCompile(`^[^\x00-\x1f\\]+$`)

// Bounds on user-supplied rule data — guard against direct-API misuse
// or future-wizard bugs that would otherwise tank the renamer's hot
// path or balloon the config file. Picked generously so honest users
// never hit them; rejection messages tell exactly which limit tripped.
const (
	webhookRuleNameMaxLen          = 200 // generous; UI displays will truncate at ~40
	webhookRuleTokenListMaxLen     = 200 // SourceTokens / MovieVersionTokens / GroupBlocklist / ReleaseGroupIDs each
	webhookRuleCustomTokensMaxLen  = 50  // typical user has 5-10
	webhookRuleCustomLabelMaxLen   = 80
	webhookRuleCustomRegexMaxLen   = 500       // RE2 compiles these in microseconds; cap deters DoS-by-config-edit
	webhookRuleTokenEntryMaxLen    = 80        // per-entry length on token allow-lists
	webhookRuleRequestBodyMaxBytes = 64 * 1024 // POST/PUT body cap — matches schedules.go posture; honest payloads are <5 KB
)

// webhookRuleRequest is the POST/PUT body. Mirrors core.WebhookRule with
// History + ID stripped — those are server-managed, not client input.
//
// Per-rule snapshots (Filters / AudioTags / VideoTags / DvDetail /
// ReleaseGroupIDs) are optional pointers — missing fields fall through
// to "preserve existing on update / nil on create" (the wizard always
// sends them, but a partial PATCH-style call would not).
type webhookRuleRequest struct {
	Name                  string                 `json:"name"`
	Enabled               bool                   `json:"enabled"`
	InstanceID            string                 `json:"instanceId"`
	AppType               string                 `json:"appType"`
	Functions             []core.WebhookFunction `json:"functions"`
	Filters               *engine.FilterConfig   `json:"filters,omitempty"`
	AudioTags             *core.AudioTagsConfig  `json:"audioTags,omitempty"`
	VideoTags             *core.VideoTagsConfig  `json:"videoTags,omitempty"`
	DvDetail              *core.DvDetailConfig   `json:"dvDetail,omitempty"`
	ReleaseGroupIDs       []string               `json:"releaseGroupIds,omitempty"`
	SyncToInstanceID      string                 `json:"syncToInstanceId,omitempty"`
	SyncSkipOrphanCleanup bool                   `json:"syncSkipOrphanCleanup,omitempty"`
	DiscoverAutoEnable    bool                   `json:"discoverAutoEnable,omitempty"`
	// TagSource + FilterOnlyTag mirror the same fields on
	// ScheduledJob.options + scanRunRequest. Webhook frontend sends
	// these only when the rule is in filter-only mode.
	TagSource       string                     `json:"tagSource,omitempty"`
	FilterOnlyTag   string                     `json:"filterOnlyTag,omitempty"`
	GrabRename      *core.GrabRenameCriteria   `json:"grabRename,omitempty"`
	QbitSe          *core.QbitSeRules          `json:"qbitSe,omitempty"`
	QbitCategoryFix *core.QbitCategoryFixRules `json:"qbitCategoryFix,omitempty"`
	PlexLabelSync   *core.PlexLabelSyncConfig  `json:"plexLabelSync,omitempty"`
	// NotifyOnFire — master per-rule kill-switch. Which agents receive
	// the notification + which functions each renders is per-agent
	// config (agents.Agent.Events + .Functions), not per-rule.
	NotifyOnFire bool `json:"notifyOnFire,omitempty"`
}

// validate enforces the rule contract before persistence. Returns nil
// on success, an apiError on any rule violation.
func (req *webhookRuleRequest) validate(cfg core.Config) *apiError {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return newAPIError(400, "name is required")
	}
	if len(name) > webhookRuleNameMaxLen {
		return newAPIError(400, "name too long (max 200 chars)")
	}
	appType := strings.ToLower(strings.TrimSpace(req.AppType))
	if appType != "radarr" && appType != "sonarr" {
		return newAPIError(400, "appType must be 'radarr' or 'sonarr'")
	}
	// Linked instance must exist + match AppType. Denormalised AppType
	// is the source of truth for function-applicability checks; we
	// cross-check it against the instance here so the rule can't claim
	// to be Radarr while pointing at a Sonarr instance.
	var inst *core.Instance
	for i := range cfg.Instances {
		if cfg.Instances[i].ID == req.InstanceID {
			inst = &cfg.Instances[i]
			break
		}
	}
	if inst == nil {
		return newAPIError(400, "instanceId not found")
	}
	if !strings.EqualFold(inst.Type, appType) {
		return newAPIError(400, "appType does not match the linked instance's type")
	}
	if len(req.Functions) == 0 {
		return newAPIError(400, "at least one function must be enabled")
	}
	if len(req.ReleaseGroupIDs) > webhookRuleTokenListMaxLen {
		return newAPIError(400, "releaseGroupIds too many entries (max 200)")
	}
	seen := map[core.WebhookFunction]bool{}
	for _, fn := range req.Functions {
		if !core.ValidWebhookFunction(fn) {
			return newAPIError(400, "unknown function: "+string(fn))
		}
		if !core.WebhookFunctionAppliesTo(fn, appType) {
			return newAPIError(400, "function '"+string(fn)+"' does not apply to "+appType+" instances")
		}
		if seen[fn] {
			return newAPIError(400, "duplicate function: "+string(fn))
		}
		seen[fn] = true
	}
	// SyncToInstanceID — when SyncToSecondary is enabled, an explicit
	// target is recommended but not required (empty = scheduler-style
	// "first other of same type" pick at fire-time, mirroring the
	// scheduler runner). When set, it must exist + be the same AppType
	// + different from the primary InstanceID.
	if seen[core.WebhookFnSyncToSecondary] && req.SyncToInstanceID != "" {
		if req.SyncToInstanceID == req.InstanceID {
			return newAPIError(400, "syncToInstanceId must differ from the rule's primary instanceId")
		}
		var target *core.Instance
		for i := range cfg.Instances {
			if cfg.Instances[i].ID == req.SyncToInstanceID {
				target = &cfg.Instances[i]
				break
			}
		}
		if target == nil {
			return newAPIError(400, "syncToInstanceId not found")
		}
		if !strings.EqualFold(target.Type, appType) {
			return newAPIError(400, "syncToInstanceId must point at a "+appType+" instance")
		}
	}
	if !seen[core.WebhookFnSyncToSecondary] && req.SyncToInstanceID != "" {
		return newAPIError(400, "syncToInstanceId is only meaningful when the syncToSecondary function is enabled")
	}
	// qBit pairings — required when the corresponding function is on.
	if seen[core.WebhookFnGrabRename] {
		if req.GrabRename == nil {
			return newAPIError(400, "grabRename criteria required when grabRename function is enabled")
		}
		if req.GrabRename.QbitInstanceID == "" {
			return newAPIError(400, "grabRename.qbitInstanceId is required")
		}
		if !qbitInstanceExists(cfg, req.GrabRename.QbitInstanceID) {
			return newAPIError(400, "grabRename.qbitInstanceId not found")
		}
		if apiErr := validateGrabRenameCriteria(req.GrabRename, appType); apiErr != nil {
			return apiErr
		}
	}
	if seen[core.WebhookFnQbitSeTag] {
		if e := validateQbitSeConfig(req.QbitSe, cfg); e != nil {
			return e
		}
	}
	// qBit Category Fix validation — required struct + qBit pairing
	// + non-empty distinct categories. The validator only enforces what
	// the user is actually saving (snapshot fields); fire-time may
	// re-resolve fresh values via the Arr download-client cache, but
	// snapshots are the floor.
	if seen[core.WebhookFnQbitCategoryFix] {
		if req.QbitCategoryFix == nil {
			return newAPIError(400, "qbitCategoryFix criteria required when qbitCategoryFix function is enabled")
		}
		if req.QbitCategoryFix.QbitInstanceID == "" {
			return newAPIError(400, "qbitCategoryFix.qbitInstanceId is required")
		}
		if !qbitInstanceExists(cfg, req.QbitCategoryFix.QbitInstanceID) {
			return newAPIError(400, "qbitCategoryFix.qbitInstanceId not found")
		}
		if req.QbitCategoryFix.ArrDownloadClientID <= 0 {
			return newAPIError(400, "qbitCategoryFix.arrDownloadClientId must be a positive integer (pick a download client from Sonarr/Radarr)")
		}
		preCat := strings.TrimSpace(req.QbitCategoryFix.PreImportCategorySnapshot)
		postCat := strings.TrimSpace(req.QbitCategoryFix.PostImportCategorySnapshot)
		if preCat == "" || postCat == "" {
			return newAPIError(400, "qbitCategoryFix requires both preImportCategorySnapshot and postImportCategorySnapshot — Sonarr/Radarr's download-client config doesn't have both fields set, edit it there first")
		}
		if strings.EqualFold(preCat, postCat) {
			return newAPIError(400, "qbitCategoryFix pre-import and post-import categories must differ")
		}
		// qBit category char-rule check — rejects only ASCII control chars
		// + backslash. Slashes, spaces, dots, unicode are all valid qBit
		// categories in the wild; autoFillQbitCategories surfaces whatever
		// the Arr's download-client UI saved, so the validator's floor is
		// "what qBit itself accepts on disk", not "what a Go identifier
		// would accept".
		if !reQbitCategoryName.MatchString(preCat) {
			return newAPIError(400, "qbitCategoryFix.preImportCategorySnapshot contains forbidden characters (control chars or backslash)")
		}
		if !reQbitCategoryName.MatchString(postCat) {
			return newAPIError(400, "qbitCategoryFix.postImportCategorySnapshot contains forbidden characters (control chars or backslash)")
		}
		// Persist the trimmed values back so the canonical shape is
		// stored.
		req.QbitCategoryFix.PreImportCategorySnapshot = preCat
		req.QbitCategoryFix.PostImportCategorySnapshot = postCat
	}
	// Plex label sync — required inline config + Plex-side validation.
	// Same pattern as the other function-config blocks above. Per-Plex-
	// instance + library + label whitelist validation lives in
	// ValidatePlexLabelSyncConfig — shared with the schedule + one-off
	// run paths that embed the same config.
	if seen[core.WebhookFnPlexLabelSync] {
		if req.PlexLabelSync == nil {
			return newAPIError(400, "plexLabelSync config required when the Sync to Plex function is enabled")
		}
		if err := core.ValidatePlexLabelSyncConfig(req.PlexLabelSync, cfg.PlexInstances, appType); err != nil {
			return newAPIError(400, "plexLabelSync: "+err.Error())
		}
	}
	if !seen[core.WebhookFnPlexLabelSync] && req.PlexLabelSync != nil {
		// User unticked the function but left the config block in the
		// payload — drop it so the persisted rule stays clean.
		req.PlexLabelSync = nil
	}
	// TagSource + FilterOnlyTag — symmetric with the schedule path's
	// validator at schedules.go (and scan.go for live HTTP scan calls).
	// Trim before evaluating so a wizard payload with " filter-only "
	// (whitespace) lands in the canonical "filter-only" branch.
	tagSource := strings.TrimSpace(req.TagSource)
	filterOnlyTag := strings.TrimSpace(req.FilterOnlyTag)
	switch tagSource {
	case "", "active", "discover", "filter-only":
		// ok — "discover" gated to Radarr in the UI (it implies the
		// Discover function, which is Radarr-only); the tag adapter
		// treats active/discover identically (per-group matching), so
		// no extra engine branch is needed here.
	default:
		return newAPIError(400, "tagSource must be 'active', 'discover' or 'filter-only' (or empty for active)")
	}
	// Filter-only is a Radarr-only feature today. Library scan
	// (runTagFilterOnly) lives in the Radarr scan path; Sonarr's
	// per-episode aggregation model has no filter-only equivalent.
	// Reject up-front so a Sonarr rule can't silently dispatch into
	// the filter-only branch on Tag-RG / Sync (where AppType isn't
	// gated) or be inconsistently mirrored on file-delete (where it
	// IS gated). Symmetric with scan_tag.go's "filter-only is Radarr-
	// only" stance.
	if tagSource == "filter-only" && !strings.EqualFold(appType, "radarr") {
		return newAPIError(400, "filter-only tag mode is supported on Radarr only")
	}
	// Filter-only requires a tag whenever ANY consumer of FilterOnlyTag
	// is enabled — Tag-RG (the primary tagger + auto-strip-on-delete
	// mirror) and Sync-to-secondary (the secondary mirror also
	// evaluates filter-only and writes the tag). Without a tag the
	// dispatcher would self-protect with a fire-time error per
	// function — cleaner to reject at save-time so the user knows
	// up-front the rule is half-configured.
	requiresFilterOnlyTag := tagSource == "filter-only" && (seen[core.WebhookFnTagReleaseGroups] || seen[core.WebhookFnSyncToSecondary])
	if requiresFilterOnlyTag {
		if filterOnlyTag == "" {
			return newAPIError(400, "filterOnlyTag is required when tagSource=filter-only and either tagReleaseGroups or syncToSecondary is enabled")
		}
		if !reTagName.MatchString(filterOnlyTag) {
			return newAPIError(400, "filterOnlyTag must be lowercase letters, digits, underscores, or dashes")
		}
		// Conflict check — symmetric with runTagFilterOnly's guard
		// (scan_tag.go:619-623). A filter-only tag whose name matches
		// any existing per-group rule's Tag for the same Arr type
		// would silently fight the per-group decision; reject up-front.
		// Disabled groups still hold the reservation (flipping Enabled
		// back on would re-introduce the conflict).
		for _, g := range cfg.ReleaseGroups {
			if !strings.EqualFold(g.Type, appType) {
				continue
			}
			if strings.EqualFold(g.Tag, filterOnlyTag) {
				return newAPIError(409, "filterOnlyTag '"+filterOnlyTag+"' collides with an Active group rule (group: "+g.Display+"). Pick a different name or remove the conflicting group.")
			}
		}
	}

	// Per-rule auto-tag snapshots — validate identically to the global
	// PUT handlers. Without this, a rule overlay can persist a snapshot
	// the global handlers would have rejected (bad prefix, unknown
	// allowed-value, malformed Labels override). The Labels feature
	// added 2026-05-16 surfaces this gap by letting users configure
	// per-value renames — the overlay path must enforce the same
	// closed-vocab + collision + regex rules as the global.
	if req.AudioTags != nil {
		if err := validateAudioTagsConfig(*req.AudioTags); err != nil {
			return newAPIError(400, "audioTags: "+err.Error())
		}
	}
	if req.VideoTags != nil {
		if err := validateVideoTagsConfig(*req.VideoTags); err != nil {
			return newAPIError(400, "videoTags: "+err.Error())
		}
	}
	if req.DvDetail != nil {
		if err := validateDvDetailConfig(*req.DvDetail); err != nil {
			return newAPIError(400, "dvDetail: "+err.Error())
		}
	}
	return nil
}

// qbitInstanceExists is a small helper since both qBit-using functions
// validate the same way. Linear scan over Config.QbitInstances.
func qbitInstanceExists(cfg core.Config, id string) bool {
	if id == "" {
		return false
	}
	for _, q := range cfg.QbitInstances {
		if q.ID == id {
			return true
		}
	}
	return false
}

// validateGrabRenameCriteria walks the user-supplied token allow-lists
// + custom-token regex array + the new RenameTarget enum. Surface-area gates:
//
//   - Token list size capped at webhookRuleTokenListMaxLen (200) — a
//     direct-API caller could otherwise PUT 10 000 entries and tank the
//     renamer hot-path on every Grab event.
//   - Empty / whitespace-only entries rejected — they'd silently always-
//     match in the renamer's strings.Contains pass.
//   - CustomTokens count + label/regex length capped — RE2 compiles
//     fast but a 100 KB pathological pattern still wastes startup memory.
//   - Each regex is compiled here. Save-time validation tells the user
//     which entry is malformed BEFORE it sits dormant in the rule and
//     fails silently every fire (per the previous "deferred to runtime"
//     comment, which masked exactly the kind of bug save-time
//     validation catches).
func validateGrabRenameCriteria(c *core.GrabRenameCriteria, appType string) *apiError {
	if !core.ValidGrabRenameTarget(c.RenameTarget) {
		return newAPIError(400, "grabRename.renameTarget must be 'torrent', 'file', 'both', or empty (defaults to torrent)")
	}
	// "file" renames each episode file inside the torrent — wired for
	// Sonarr only (it fixes season-pack per-file scoring, where Sonarr
	// parses each file by its own name). Reject it on Radarr, where it
	// isn't wired. "both" isn't wired anywhere yet.
	if c.RenameTarget == core.GrabRenameTargetFile && !strings.EqualFold(appType, "sonarr") {
		return newAPIError(400, "grabRename.renameTarget 'file' is Sonarr-only — it renames each episode file inside a season pack so Sonarr scores it correctly at import")
	}
	if c.RenameTarget == core.GrabRenameTargetBoth {
		return newAPIError(400, "grabRename.renameTarget 'both' not yet supported — use 'torrent', or 'file' on a Sonarr rule")
	}
	// Movie-version trigger is Radarr-only — applies to movie versions
	// like Director's Cut / IMAX / Theatrical that TV releases don't use.
	// UI hides the checkbox via x-show="ruleEditorInstanceType()==='radarr'"
	// but a direct-API caller could still set it on a Sonarr rule, where
	// it would silently never match. Reject up-front so the user knows
	// the trigger is misconfigured.
	if !strings.EqualFold(appType, "radarr") && c.TriggerOnMovieVersionMismatch {
		return newAPIError(400, "grabRename.triggerOnMovieVersionMismatch is Radarr-only — movie versions like Director's Cut / IMAX don't apply to TV releases")
	}
	// At least one trigger must be active — otherwise the rule fires
	// every Grab event but always skips with "no enabled trigger
	// detected a diff". Silent no-op rule is a UX foot-gun. Custom
	// tokens count as a trigger source (their match implicitly fires
	// rename). TriggerAlways is the explicit "rename every grab"
	// escape hatch.
	if !c.TriggerOnMissingReleaseGroup &&
		!c.TriggerOnMovieVersionMismatch &&
		!c.TriggerOnSourceMismatch &&
		!c.TriggerOnAudioMismatch &&
		!c.TriggerOnHdrMismatch &&
		!c.TriggerOnLanguageMismatch &&
		!c.TriggerOnSceneMismatch &&
		!c.TriggerOnBadNaming &&
		!c.TriggerAlways &&
		len(c.CustomTokens) == 0 {
		return newAPIError(400, "grabRename must enable at least one trigger (or define custom tokens) — otherwise the rule never fires")
	}
	if len(c.SourceTokens) > webhookRuleTokenListMaxLen {
		return newAPIError(400, "grabRename.sourceTokens too many entries (max 200)")
	}
	if apiErr := validateTokenList(c.SourceTokens, "sourceTokens"); apiErr != nil {
		return apiErr
	}
	if len(c.MovieVersionTokens) > webhookRuleTokenListMaxLen {
		return newAPIError(400, "grabRename.movieVersionTokens too many entries (max 200)")
	}
	if apiErr := validateTokenList(c.MovieVersionTokens, "movieVersionTokens"); apiErr != nil {
		return apiErr
	}
	if len(c.GroupBlocklist) > webhookRuleTokenListMaxLen {
		return newAPIError(400, "grabRename.groupBlocklist too many entries (max 200)")
	}
	if apiErr := validateTokenList(c.GroupBlocklist, "groupBlocklist"); apiErr != nil {
		return apiErr
	}
	if len(c.CustomTokens) > webhookRuleCustomTokensMaxLen {
		return newAPIError(400, "grabRename.customTokens too many entries (max 50)")
	}
	for i := range c.CustomTokens {
		// Trim in place so the persisted shape is canonical.
		c.CustomTokens[i].Label = strings.TrimSpace(c.CustomTokens[i].Label)
		c.CustomTokens[i].Regex = strings.TrimSpace(c.CustomTokens[i].Regex)
		if c.CustomTokens[i].Label == "" {
			return newAPIError(400, "grabRename.customTokens[].label is required")
		}
		if len(c.CustomTokens[i].Label) > webhookRuleCustomLabelMaxLen {
			return newAPIError(400, "grabRename.customTokens[].label too long (max 80 chars)")
		}
		if c.CustomTokens[i].Regex == "" {
			return newAPIError(400, "grabRename.customTokens[].regex is required")
		}
		if len(c.CustomTokens[i].Regex) > webhookRuleCustomRegexMaxLen {
			return newAPIError(400, "grabRename.customTokens[].regex too long (max 500 chars)")
		}
		if _, err := regexp.Compile(c.CustomTokens[i].Regex); err != nil {
			return newAPIError(400, "grabRename.customTokens["+c.CustomTokens[i].Label+"]: invalid regex: "+err.Error())
		}
	}
	return nil
}

// validateTokenList trims each entry in place + rejects empty / oversized
// entries. Trim-in-place keeps the persisted shape canonical so the
// dispatcher reads exactly what the user typed (less the leading /
// trailing whitespace).
func validateTokenList(list []string, fieldName string) *apiError {
	for i, entry := range list {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			return newAPIError(400, "grabRename."+fieldName+" contains an empty entry")
		}
		if len(entry) > webhookRuleTokenEntryMaxLen {
			return newAPIError(400, "grabRename."+fieldName+" entry too long (max 80 chars)")
		}
		list[i] = entry
	}
	return nil
}

// applyRequest copies the request fields onto a target rule. Used by
// both Create (target is a fresh rule) and Update (target is the
// existing rule). The Update path skips nil-pointer snapshots so a
// future quick-edit PUT that omits one of the snapshot fields doesn't
// silently wipe the existing per-rule config.
func (req *webhookRuleRequest) applyTo(rule *core.WebhookRule, isUpdate bool) {
	rule.Name = strings.TrimSpace(req.Name)
	rule.Enabled = req.Enabled
	rule.InstanceID = req.InstanceID
	rule.AppType = strings.ToLower(strings.TrimSpace(req.AppType))
	rule.Functions = append([]core.WebhookFunction(nil), req.Functions...)
	rule.SyncToInstanceID = strings.TrimSpace(req.SyncToInstanceID)
	rule.SyncSkipOrphanCleanup = req.SyncSkipOrphanCleanup
	rule.DiscoverAutoEnable = req.DiscoverAutoEnable
	rule.TagSource = strings.TrimSpace(req.TagSource)
	rule.FilterOnlyTag = strings.TrimSpace(req.FilterOnlyTag)
	if !isUpdate || req.Filters != nil {
		rule.Filters = req.Filters
	}
	if !isUpdate || req.AudioTags != nil {
		rule.AudioTags = req.AudioTags
	}
	if !isUpdate || req.VideoTags != nil {
		rule.VideoTags = req.VideoTags
	}
	if !isUpdate || req.DvDetail != nil {
		rule.DvDetail = req.DvDetail
	}
	if !isUpdate || req.ReleaseGroupIDs != nil {
		rule.ReleaseGroupIDs = req.ReleaseGroupIDs
	}
	if !isUpdate || req.GrabRename != nil {
		rule.GrabRename = req.GrabRename
	}
	if !isUpdate || req.QbitSe != nil {
		rule.QbitSe = req.QbitSe
	}
	if !isUpdate || req.QbitCategoryFix != nil {
		rule.QbitCategoryFix = req.QbitCategoryFix
	}
	if !isUpdate || req.PlexLabelSync != nil {
		rule.PlexLabelSync = req.PlexLabelSync
	}
	rule.NotifyOnFire = req.NotifyOnFire
	core.NormalizeWebhookRule(rule)
}

// handleWebhookRulesMeta — GET /api/webhook-rules/_meta. Surfaces the
// per-Arr-type function matrix + Connect-event mapping so the wizard
// renders strictly from server truth. Without this endpoint the
// frontend would reimplement WebhookFunctionAppliesTo + EventsForFunction
// in JS and the Sonarr/Radarr asymmetry would inevitably drift across
// the two languages — see project rule "per-instance-type feature
// visibility is the architectural model" + the per-instance-type-ux.md
// applicability matrix.
//
// Shape:
//
//	{ "functionsByAppType": { "radarr": [...], "sonarr": [...] },
//	  "eventsByFunction":   { "radarr": { "tagAudio": ["Download"], ... },
//	                           "sonarr": { ... } } }
func (s *Server) handleWebhookRulesMeta(w http.ResponseWriter, r *http.Request) {
	functionsByAppType := map[string][]core.WebhookFunction{
		"radarr": collectApplicableFunctions("radarr"),
		"sonarr": collectApplicableFunctions("sonarr"),
	}
	eventsByFunction := map[string]map[core.WebhookFunction][]core.WebhookConnectEvent{
		"radarr": collectEventsByFunction("radarr"),
		"sonarr": collectEventsByFunction("sonarr"),
	}
	writeJSON(w, map[string]any{
		"functionsByAppType": functionsByAppType,
		"eventsByFunction":   eventsByFunction,
	})
}

// collectApplicableFunctions returns the canonical-order list of
// functions that apply to a given Arr type. Used by the meta endpoint.
// Reads from core.AllWebhookFunctions() so new functions landing in
// the core list are automatically picked up — avoids the silent-drop
// trap a hardcoded list would create.
func collectApplicableFunctions(appType string) []core.WebhookFunction {
	all := core.AllWebhookFunctions()
	out := []core.WebhookFunction{}
	for _, fn := range all {
		if core.WebhookFunctionAppliesTo(fn, appType) {
			out = append(out, fn)
		}
	}
	return out
}

// collectEventsByFunction returns the per-(appType, function) Connect
// event mapping. Used by the meta endpoint.
func collectEventsByFunction(appType string) map[core.WebhookFunction][]core.WebhookConnectEvent {
	out := map[core.WebhookFunction][]core.WebhookConnectEvent{}
	for _, fn := range collectApplicableFunctions(appType) {
		out[fn] = core.EventsForFunction(fn, appType)
	}
	return out
}

// handleListWebhookRules — GET /api/webhook-rules.
//
// Per-rule Webhook.Token + Webhook.Secret are bearer credentials —
// masked in this broad listing so the response surface can't be used
// to enumerate active webhook tokens. Plain values are served only via
// the dedicated /api/webhook-rules/{id}/webhook endpoint where the
// admin user copies them into Sonarr/Radarr's Connect config.
func (s *Server) handleListWebhookRules(w http.ResponseWriter, r *http.Request) {
	cfg := s.App.Config.Get()
	out := cfg.WebhookRules
	if out == nil {
		out = []core.WebhookRule{}
	}
	for i := range out {
		maskWebhookRuleCreds(&out[i])
	}
	writeJSON(w, out)
}

// handleGetWebhookRule — GET /api/webhook-rules/{id}.
//
// Same Token/Secret masking story as handleListWebhookRules — even a
// single-rule fetch must not echo the bearer credentials.
func (s *Server) handleGetWebhookRule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	cfg := s.App.Config.Get()
	for _, wr := range cfg.WebhookRules {
		if wr.ID == id {
			maskWebhookRuleCreds(&wr)
			writeJSON(w, wr)
			return
		}
	}
	writeError(w, 404, "webhook rule not found")
}

// maskWebhookRuleCreds replaces the per-rule Webhook.Token +
// Webhook.Secret with the masked sentinel so the broad rule-listing /
// rule-fetch endpoints can return the rest of the rule struct without
// leaking the bearer credentials. Callers that genuinely need the
// plain values use the dedicated handleGetPerRuleWebhook endpoint.
//
// Critical: ConfigStore.Get deep-copies the outer slice/struct but
// pointer fields like *WebhookConfig are SHARED with the store. We
// MUST clone the Webhook substruct here before mutating it, otherwise
// every GET would replace the real Token/Secret with the sentinel in
// memory, and the next ConfigStore.Update would persist the masked
// values back to disk — destroying the user's real credentials. Test
// TestWebhookRule_PerRuleWebhookCreds_MaskedInListAndGet locks this.
func maskWebhookRuleCreds(r *core.WebhookRule) {
	if r == nil || r.Webhook == nil {
		return
	}
	cloned := *r.Webhook
	r.Webhook = &cloned
	if r.Webhook.Token != "" {
		r.Webhook.Token = maskSentinel
	}
	if r.Webhook.Secret != "" {
		r.Webhook.Secret = maskSentinel
	}
}

// handleCreateWebhookRule — POST /api/webhook-rules. Server assigns ID.
func (s *Server) handleCreateWebhookRule(w http.ResponseWriter, r *http.Request) {
	var req webhookRuleRequest
	// Body cap matches the rest of the codebase (schedules / qbit /
	// notifications). Auth-gated route, but unbounded streaming-decode
	// would let a compromised admin balloon memory before the
	// validator's slice caps fire.
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, webhookRuleRequestBodyMaxBytes)).Decode(&req); err != nil {
		writeError(w, 400, "invalid body")
		return
	}
	cfg := s.App.Config.Get()
	if apiErr := req.validate(cfg); apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	rule := core.WebhookRule{ID: genID()}
	req.applyTo(&rule, false)
	if err := s.App.Config.Update(func(c *core.Config) {
		c.WebhookRules = append(c.WebhookRules, rule)
	}); err != nil {
		writeError(w, 500, "save webhook rule: "+err.Error())
		return
	}
	// applyTo doesn't touch rule.Webhook today (per-rule webhook is
	// set via the dedicated /webhook endpoint, not the rule editor)
	// but defensively mask in case future applyTo carries it.
	maskWebhookRuleCreds(&rule)
	writeJSON(w, rule)
}

// handleUpdateWebhookRule — PUT /api/webhook-rules/{id}. Replaces editable
// fields; History is preserved.
func (s *Server) handleUpdateWebhookRule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req webhookRuleRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, webhookRuleRequestBodyMaxBytes)).Decode(&req); err != nil {
		writeError(w, 400, "invalid body")
		return
	}
	cfg := s.App.Config.Get()
	if apiErr := req.validate(cfg); apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	found := false
	if err := s.App.Config.Update(func(c *core.Config) {
		for i := range c.WebhookRules {
			if c.WebhookRules[i].ID != id {
				continue
			}
			found = true
			req.applyTo(&c.WebhookRules[i], true)
			// History intentionally untouched.
			return
		}
	}); err != nil {
		writeError(w, 500, "save webhook rule: "+err.Error())
		return
	}
	if !found {
		writeError(w, 404, "webhook rule not found")
		return
	}
	cfg = s.App.Config.Get()
	for _, wr := range cfg.WebhookRules {
		if wr.ID == id {
			// Per-rule webhook Token + Secret are bearer credentials
			// that may have been set previously via the dedicated
			// /webhook endpoint. Mask before echoing the updated rule.
			maskWebhookRuleCreds(&wr)
			writeJSON(w, wr)
			return
		}
	}
	writeError(w, 500, "post-update read failed")
}

// handleDeleteWebhookRule — DELETE /api/webhook-rules/{id}.
func (s *Server) handleDeleteWebhookRule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	found := false
	if err := s.App.Config.Update(func(c *core.Config) {
		out := c.WebhookRules[:0]
		for _, wr := range c.WebhookRules {
			if wr.ID == id {
				found = true
				continue
			}
			out = append(out, wr)
		}
		c.WebhookRules = out
	}); err != nil {
		writeError(w, 500, "delete webhook rule: "+err.Error())
		return
	}
	if !found {
		writeError(w, 404, "webhook rule not found")
		return
	}
	w.WriteHeader(204)
}
