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

// Bounds on user-supplied rule data — guard against direct-API misuse
// or future-wizard bugs that would otherwise tank the renamer's hot
// path or balloon the config file. Picked generously so honest users
// never hit them; rejection messages tell exactly which limit tripped.
const (
	webhookRuleNameMaxLen          = 200  // generous; UI displays will truncate at ~40
	webhookRuleTokenListMaxLen     = 200  // SourceTokens / MovieVersionTokens / GroupBlocklist / ReleaseGroupIDs each
	webhookRuleCustomTokensMaxLen  = 50   // typical user has 5-10
	webhookRuleCustomLabelMaxLen   = 80
	webhookRuleCustomRegexMaxLen   = 500  // RE2 compiles these in microseconds; cap deters DoS-by-config-edit
	webhookRuleTokenEntryMaxLen    = 80   // per-entry length on token allow-lists
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
	Name                  string                    `json:"name"`
	Enabled               bool                      `json:"enabled"`
	InstanceID            string                    `json:"instanceId"`
	AppType               string                    `json:"appType"`
	Functions             []core.WebhookFunction    `json:"functions"`
	Filters               *engine.FilterConfig      `json:"filters,omitempty"`
	AudioTags             *core.AudioTagsConfig     `json:"audioTags,omitempty"`
	VideoTags             *core.VideoTagsConfig     `json:"videoTags,omitempty"`
	DvDetail              *core.DvDetailConfig      `json:"dvDetail,omitempty"`
	ReleaseGroupIDs       []string                  `json:"releaseGroupIds,omitempty"`
	SyncToInstanceID      string                    `json:"syncToInstanceId,omitempty"`
	SyncSkipOrphanCleanup bool                      `json:"syncSkipOrphanCleanup,omitempty"`
	DiscoverAutoEnable    bool                      `json:"discoverAutoEnable,omitempty"`
	GrabRename            *core.GrabRenameCriteria  `json:"grabRename,omitempty"`
	QbitSe                *core.QbitSeRules         `json:"qbitSe,omitempty"`
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
		if apiErr := validateGrabRenameCriteria(req.GrabRename); apiErr != nil {
			return apiErr
		}
	}
	if seen[core.WebhookFnQbitSeTag] {
		if req.QbitSe == nil {
			return newAPIError(400, "qbitSe rules required when qbitSeTag function is enabled")
		}
		if !req.QbitSe.TagSeason && !req.QbitSe.TagEpisode {
			return newAPIError(400, "qbitSe must enable at least one of tagSeason / tagEpisode")
		}
		if req.QbitSe.QbitInstanceID == "" {
			return newAPIError(400, "qbitSe.qbitInstanceId is required")
		}
		if !qbitInstanceExists(cfg, req.QbitSe.QbitInstanceID) {
			return newAPIError(400, "qbitSe.qbitInstanceId not found")
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
func validateGrabRenameCriteria(c *core.GrabRenameCriteria) *apiError {
	if !core.ValidGrabRenameTarget(c.RenameTarget) {
		return newAPIError(400, "grabRename.renameTarget must be 'torrent', 'file', 'both', or empty (defaults to torrent)")
	}
	// v1 limit: only "torrent" is wired in the adapter. Reject other
	// values at save-time to prevent users saving a rule that silently
	// no-ops because the file/both adapter paths aren't implemented yet.
	if c.RenameTarget == core.GrabRenameTargetFile || c.RenameTarget == core.GrabRenameTargetBoth {
		return newAPIError(400, "grabRename.renameTarget '"+c.RenameTarget+"' not yet supported in this version (file/both rename lands when torrent-only proves insufficient)")
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
		!c.TriggerOnSceneMismatch &&
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
//   { "functionsByAppType": { "radarr": [...], "sonarr": [...] },
//     "eventsByFunction":   { "radarr": { "tagAudio": ["Download"], ... },
//                              "sonarr": { ... } } }
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
func collectApplicableFunctions(appType string) []core.WebhookFunction {
	all := []core.WebhookFunction{
		core.WebhookFnTagReleaseGroups,
		core.WebhookFnDiscover,
		core.WebhookFnTagAudio,
		core.WebhookFnTagVideo,
		core.WebhookFnTagDvDetail,
		core.WebhookFnRecover,
		core.WebhookFnSyncToSecondary,
		core.WebhookFnFileDeleteClean,
		core.WebhookFnGrabRename,
		core.WebhookFnQbitSeTag,
	}
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
func (s *Server) handleListWebhookRules(w http.ResponseWriter, r *http.Request) {
	cfg := s.App.Config.Get()
	out := cfg.WebhookRules
	if out == nil {
		out = []core.WebhookRule{}
	}
	writeJSON(w, out)
}

// handleGetWebhookRule — GET /api/webhook-rules/{id}.
func (s *Server) handleGetWebhookRule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	cfg := s.App.Config.Get()
	for _, wr := range cfg.WebhookRules {
		if wr.ID == id {
			writeJSON(w, wr)
			return
		}
	}
	writeError(w, 404, "webhook rule not found")
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
