package api

// webhook_dispatch.go — rule dispatch path for the M-Webhook subsystem.
//
// Called from handleWebhookReceive after the token resolves to an
// instance + the body is decoded. Walks Config.WebhookRules filtered
// by (InstanceID match, Enabled, FiresOn(event-type)), runs each
// matched rule's enabled functions in canonical order, and appends a
// WebhookRuleRun summary to the rule's History.
//
// All 10 webhook adapters are wired through real engine paths
// (Tag Audio / Video / DV Details / File Delete / Tag-RG / Recover /
// Discover / Sync / Grab Rename / qBit S/E). The dispatcher's
// `default:` fallback "would fire" stub is now dead-for-valid-Functions
// — kept as defence against tampered configs with unknown function
// names making it past the validator.
//
// Architecture rule: adapters NEVER decide tags themselves. Every tag/RG
// match goes through the engine helpers (engine.MatchReleaseGroup,
// engine.DecideTag, engine.ExtraTagsForFile, engine.FindImportedGrabGroup).
// The bash tagarr_import.sh v1.6.0/v1.6.1 SiC-in-Jurassic regression is
// the cautionary tale — the bash handler reimplemented its own substring
// grep instead of routing through matches_release_group, lost the word-
// boundary anchor, and started matching SIC inside "jurassic". Don't
// repeat the mistake.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"resolvarr/internal/arr"
	"resolvarr/internal/core"
)

// canonicalFunctionOrder pins the execution order when a rule has
// multiple functions ticked. Mirrors the scheduler's combined-mode
// chain semantics: discovery → recovery → tag → audio → video → DV
// → file-delete → grab-rename → qBit-S/E. Order matters because some
// functions read state others write (Recover backfills releaseGroup
// before TagReleaseGroups runs the matcher).
var canonicalFunctionOrder = []core.WebhookFunction{
	// Recover BEFORE Discover — bash tagarr_import.sh runs recovery
	// (line 951) before the discovery check (line 1086). Discovery
	// uses the (potentially recovered) releaseGroup field. With
	// shared chain-state across adapters, Discover would see Recover's
	// patched value; today each adapter re-decodes the static event
	// payload independently, so Discover sees the ORIGINAL rg until
	// chain-state is wired (TODO). The declared order matches bash
	// regardless so the future fix lands without re-shuffling.
	core.WebhookFnRecover,
	core.WebhookFnDiscover,
	core.WebhookFnTagReleaseGroups,
	core.WebhookFnTagAudio,
	core.WebhookFnTagVideo,
	core.WebhookFnTagDvDetail,
	core.WebhookFnSyncToSecondary,
	core.WebhookFnFileDeleteClean,
	core.WebhookFnGrabRename,
	core.WebhookFnQbitSeTag,
	// QbitCategoryFix runs LAST in the chain because it's defensive
	// only — checking + reconciling the qBit-side category after
	// Sonarr/Radarr's own Import handling. The Arr-side dispatchers
	// (Recover / Tag-RG / Tag-Audio etc.) don't depend on the qBit
	// category being right, and qBit Category Fix doesn't read or
	// write Arr state, so order between siblings doesn't matter —
	// putting it at the tail keeps the chain easy to reason about
	// ("everything else happens first; then we reconcile qBit").
	core.WebhookFnQbitCategoryFix,
}

// functionResult is one (rule × function) outcome. Aggregated into the
// rule's WebhookRuleRun summary.
type functionResult struct {
	Function core.WebhookFunction
	OK       bool
	Summary  string
	Err      error
}

// pendingRuleRun pairs a fired rule with its run summary for the batched
// post-dispatch persistence. Collected during the iteration in
// dispatchWebhookRules and written in ONE ConfigStore.Update call
// after all rules for the event have fired — see #3 in the foundation
// review: 5 rules firing on one Download event would otherwise produce
// 5 sequential MarshalIndent + atomic-rename writes. Batching collapses
// it to one disk sync.
type pendingRuleRun struct {
	ruleID string
	run    core.WebhookRuleRun
}

// dispatchWebhookRules walks every rule for the given instance and fires
// the enabled functions whose Connect-event mapping matches the incoming
// event. Returns the number of rules that fired (for the receiver's ack
// payload — useful for debugging from the wizard's "Test event" flow).
//
// Errors inside an adapter do not abort the rest of the rule's chain
// or sibling rules — they're collected per-function in the run summary.
// A rule firing with one function failing produces status="partial";
// every function failing produces "error"; clean run produces "ok".
func (s *Server) dispatchWebhookRules(
	ctx context.Context,
	inst *core.Instance,
	env *connectEventEnvelope,
	body []byte,
) int {
	if env == nil || env.EventType == "" {
		return 0
	}
	event := core.WebhookConnectEvent(env.EventType)
	// Single config snapshot for the whole receive — every adapter on
	// every fired rule for this event reads the same view. Without
	// this, each adapter would call ConfigStore.Get() independently
	// and a UI mid-fire write could give the dispatcher's iteration
	// loop one shape and the adapter's resolve a different one
	// (instance vanished, bucket disabled, etc.). Pinning here keeps
	// "what the dispatcher saw" and "what the adapter ran on" identical.
	cfg := s.App.Config.Get()

	// Tag-details fetched lazily — once per receive when ANY rule
	// needs it, then shared across all adapters firing on this event.
	// Skips the network round-trip when no rule's enabled functions
	// need the label↔ID map (qBit-only rules don't read tags).
	var (
		tagDetailsByInst = map[string][]arr.TagDetail{}
		tagDetailsErrs   = map[string]error{}
	)

	var pending []pendingRuleRun
	for i := range cfg.WebhookRules {
		rule := cfg.WebhookRules[i]
		if rule.InstanceID != inst.ID || !rule.Enabled {
			continue
		}
		if !rule.FiresOn(event) {
			continue
		}
		// Canonical execution order — iterate canonicalFunctionOrder
		// and execute only the entries the rule actually has, gated
		// on event-applicability per function.
		started := time.Now().UTC()
		var results []functionResult
		for _, fn := range canonicalFunctionOrder {
			if !rule.HasFunction(fn) {
				continue
			}
			matches := false
			for _, e := range core.EventsForFunction(fn, rule.AppType) {
				if e == event {
					matches = true
					break
				}
			}
			if !matches {
				continue
			}
			// Fetch tag-details on first auto-tag function we see.
			// Cached per-instance keyed by inst.ID — different webhook
			// rules on the same Arr share one lookup. Errors are
			// stored too so a failing fetch doesn't retry per rule.
			tagDetails, tagErr := s.tagDetailsFor(ctx, inst, tagDetailsByInst, tagDetailsErrs)
			res := s.dispatchWebhookFunction(ctx, &rule, cfg, tagDetails, tagErr, fn, env, body)
			results = append(results, res)
		}
		if len(results) == 0 {
			// Rule matched the event-type at the Functions-level (FiresOn
			// returned true) but no individual function applied to this
			// specific event. Shouldn't happen with current matrix, but
			// guards against future asymmetric event mappings.
			continue
		}
		pending = append(pending, pendingRuleRun{
			ruleID: rule.ID,
			run:    buildWebhookRuleRun(env, body, started, results),
		})
	}
	if len(pending) > 0 {
		s.appendWebhookRuleRunsBatch(pending)
	}
	return len(pending)
}

// dispatchWebhookFunction is the per-function adapter dispatcher. Each
// case routes to a real engine + arr.Client path via the per-function
// helpers in webhook_adapters.go. Functions not yet wired fall through
// to the would-fire stub — the dispatcher loop can still exercise
// canonical-order + history persistence against partial coverage.
//
// cfg + tagDetails are pinned at the receive boundary (see
// dispatchWebhookRules) and shared across every adapter on every fired
// rule for this one event. tagDetailsErr is non-nil when the upstream
// fetch failed — auto-tag adapters surface this via OK=false instead
// of attempting to operate on an empty label↔ID map.
//
// All 10 webhook functions are wired (see canonicalFunctionOrder
// for execution-order; switch arms map function constant → adapter).
func (s *Server) dispatchWebhookFunction(
	ctx context.Context,
	rule *core.WebhookRule,
	cfg core.Config,
	tagDetails []arr.TagDetail,
	tagDetailsErr error,
	fn core.WebhookFunction,
	env *connectEventEnvelope,
	body []byte,
) functionResult {
	switch fn {
	case core.WebhookFnTagAudio:
		if r, blocked := requireTagDetails(fn, tagDetailsErr); blocked {
			return r
		}
		return s.dispatchTagAudio(ctx, rule, cfg, tagDetails, env, body)
	case core.WebhookFnTagVideo:
		if r, blocked := requireTagDetails(fn, tagDetailsErr); blocked {
			return r
		}
		return s.dispatchTagVideo(ctx, rule, cfg, tagDetails, env, body)
	case core.WebhookFnTagDvDetail:
		if r, blocked := requireTagDetails(fn, tagDetailsErr); blocked {
			return r
		}
		return s.dispatchTagDvDetail(ctx, rule, cfg, tagDetails, env, body)
	case core.WebhookFnFileDeleteClean:
		if r, blocked := requireTagDetails(fn, tagDetailsErr); blocked {
			return r
		}
		return s.dispatchFileDeleteCleanup(ctx, rule, cfg, tagDetails, env, body)
	case core.WebhookFnTagReleaseGroups:
		if r, blocked := requireTagDetails(fn, tagDetailsErr); blocked {
			return r
		}
		return s.dispatchTagReleaseGroups(ctx, rule, cfg, tagDetails, env, body)
	case core.WebhookFnRecover:
		// Recover doesn't read or write tags — it patches the file's
		// releaseGroup field + triggers a RenameFiles command. No
		// requireTagDetails gate.
		return s.dispatchRecover(ctx, rule, cfg, env, body)
	case core.WebhookFnDiscover:
		// Discover writes to cfg.ReleaseGroups (config), not to Arr
		// tags — no requireTagDetails gate.
		return s.dispatchDiscover(ctx, rule, cfg, env, body)
	case core.WebhookFnSyncToSecondary:
		// Sync writes to a SECONDARY instance's tags — primary's
		// tagDetails cache is the wrong instance, so adapter fetches
		// secondary's tag-details internally. No requireTagDetails
		// gate (gate operates on the per-rule primary cache).
		return s.dispatchSyncToSecondary(ctx, rule, cfg, env, body)
	case core.WebhookFnGrabRename:
		// Grab Rename touches qBit, not Arr — doesn't read or write
		// tags. No requireTagDetails gate.
		return s.dispatchGrabRename(ctx, rule, cfg, env, body)
	case core.WebhookFnQbitSeTag:
		// qBit S/E Tag adds qBit tags only; doesn't touch Arr.
		// Sonarr-only per validator.
		return s.dispatchQbitSeTag(ctx, rule, cfg, env, body)
	case core.WebhookFnQbitCategoryFix:
		// qBit Category Fix reads Arr history (read-only) + writes to
		// qBit (category swap). Doesn't read or write Arr tags — no
		// requireTagDetails gate.
		return s.dispatchQbitCategoryFix(ctx, rule, cfg, env, body)
	}
	return functionResult{
		Function: fn,
		OK:       true,
		Summary:  fmt.Sprintf("would fire %s on %s/%s (adapter not yet wired)", fn, rule.AppType, env.EventType),
	}
}

// requireTagDetails is the per-adapter short-circuit when the upstream
// tag-details fetch failed for this rule's instance. Adapters that
// need the label↔ID map call this first; non-tag adapters (Grab
// Rename / qBit S/E / Recover-only-history-walk) skip it. Returns
// blocked=true with a populated functionResult when the caller
// should return immediately.
func requireTagDetails(fn core.WebhookFunction, tagDetailsErr error) (functionResult, bool) {
	if tagDetailsErr == nil {
		return functionResult{}, false
	}
	return functionResult{Function: fn, OK: false, Summary: "list tags", Err: tagDetailsErr}, true
}

// tagDetailsFor lazily fetches the Arr tag-details list for an instance
// + caches per-receive. Adapters that need the label↔ID map all read
// the SAME slice (and the same error if the fetch failed) so a multi-
// function rule on a Download event makes one network call total
// regardless of how many auto-tag functions it has enabled.
//
// The cache is per-receive (lives in two maps owned by the caller's
// dispatch frame). Across receives the maps are fresh — rolling tag
// changes between fires are picked up next time.
func (s *Server) tagDetailsFor(
	ctx context.Context,
	inst *core.Instance,
	cache map[string][]arr.TagDetail,
	errCache map[string]error,
) ([]arr.TagDetail, error) {
	if cached, ok := cache[inst.ID]; ok {
		return cached, errCache[inst.ID]
	}
	if cachedErr, ok := errCache[inst.ID]; ok {
		return nil, cachedErr
	}
	client := s.arrClientFor(inst)
	td, err := client.ListTagDetails(ctx)
	if err != nil {
		errCache[inst.ID] = err
		return nil, err
	}
	cache[inst.ID] = td
	return td, nil
}

// buildWebhookRuleRun aggregates the per-function results into the
// rolling-history entry. Status semantics:
//   - "ok"      every function succeeded
//   - "partial" at least one function succeeded AND at least one failed
//   - "error"   every function failed
func buildWebhookRuleRun(env *connectEventEnvelope, body []byte, started time.Time, results []functionResult) core.WebhookRuleRun {
	var okCount, errCount int
	parts := make([]string, 0, len(results))
	for _, r := range results {
		if r.OK {
			okCount++
		} else {
			errCount++
		}
		if r.Err != nil {
			parts = append(parts, fmt.Sprintf("%s: %v", r.Function, r.Err))
		} else {
			parts = append(parts, fmt.Sprintf("%s: %s", r.Function, r.Summary))
		}
	}
	status := "ok"
	switch {
	case okCount == 0 && errCount > 0:
		status = "error"
	case errCount > 0:
		status = "partial"
	}
	title, ctxStr := summariseEvent(env)
	releaseTitle, filePath := extractReleaseAndFilePath(body)
	return core.WebhookRuleRun{
		StartedAt:    started,
		DurationMs:   time.Since(started).Milliseconds(),
		Status:       status,
		EventType:    env.EventType,
		ItemTitle:    title,
		ItemContext:  ctxStr,
		ReleaseTitle: releaseTitle,
		FilePath:     filePath,
		Summary:      strings.Join(parts, "; "),
	}
}

// extractReleaseAndFilePath pulls the indexer release-title +
// post-import file path from the raw Connect-event body. Defensive:
// every field absence is fine — empty strings render as "—" in the
// History modal. Used by buildWebhookRuleRun so the user can see
// grab-name vs import-name without cross-referencing Recent activity.
//
// Field-source map (matches Sonarr + Radarr Connect schemas):
//   Grab event:           release.releaseTitle  → ReleaseTitle (no FilePath yet)
//   Download event:       movieFile.sceneName / episodeFile.sceneName  → ReleaseTitle (when present)
//                         movieFile.relativePath / episodeFile.relativePath → FilePath
//   MovieFileDelete /
//   EpisodeFileDelete:    movieFile.relativePath / episodeFile.relativePath → FilePath
//                         (no ReleaseTitle on delete events — the indexer
//                         release-title isn't carried in deletion payloads)
func extractReleaseAndFilePath(body []byte) (releaseTitle, filePath string) {
	if len(body) == 0 {
		return "", ""
	}
	var probe struct {
		Release *struct {
			ReleaseTitle string `json:"releaseTitle"`
		} `json:"release,omitempty"`
		MovieFile *struct {
			RelativePath string `json:"relativePath"`
			SceneName    string `json:"sceneName"`
		} `json:"movieFile,omitempty"`
		EpisodeFile *struct {
			RelativePath string `json:"relativePath"`
			SceneName    string `json:"sceneName"`
		} `json:"episodeFile,omitempty"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return "", ""
	}
	if probe.Release != nil {
		releaseTitle = strings.TrimSpace(probe.Release.ReleaseTitle)
	}
	if probe.MovieFile != nil {
		filePath = strings.TrimSpace(probe.MovieFile.RelativePath)
		if releaseTitle == "" {
			releaseTitle = strings.TrimSpace(probe.MovieFile.SceneName)
		}
	}
	if probe.EpisodeFile != nil {
		if filePath == "" {
			filePath = strings.TrimSpace(probe.EpisodeFile.RelativePath)
		}
		if releaseTitle == "" {
			releaseTitle = strings.TrimSpace(probe.EpisodeFile.SceneName)
		}
	}
	return releaseTitle, filePath
}

// appendWebhookRuleRunsBatch pushes the collected runs onto each rule's
// rolling history in ONE ConfigStore.Update — important because chatty
// Connect feeds (whole-season Sonarr packs trigger 24-episode events)
// can fire many rules per receive, and per-rule Update calls would mean
// per-rule disk syncs.
//
// Trimming follows the scheduler's append-then-drop-oldest pattern
// (scheduler.go:317-320) — newer runs go to the tail, oldest pruned
// from the head. We do NOT sort: time.Now().UTC() inside one receiver
// goroutine is monotonic, so insertion order IS chronological. A
// previous version sorted-then-truncated which silently drops the
// just-appended run when StartedAt ties an existing entry.
//
// Save failures are logged to stderr (matching the same fallback
// pattern Config.Load uses) and the receive ack still returns 200 —
// Connect retries would just push the same event again, no point.
func (s *Server) appendWebhookRuleRunsBatch(pending []pendingRuleRun) {
	if len(pending) == 0 {
		return
	}
	// Group runs by ruleID so one rule firing twice in the same batch
	// (only possible via future fan-out — not today, but defensive)
	// gets both runs appended in chronological order.
	byRule := make(map[string][]core.WebhookRuleRun, len(pending))
	order := make([]string, 0, len(pending))
	for _, p := range pending {
		if _, exists := byRule[p.ruleID]; !exists {
			order = append(order, p.ruleID)
		}
		byRule[p.ruleID] = append(byRule[p.ruleID], p.run)
	}
	err := s.App.Config.Update(func(c *core.Config) {
		for _, ruleID := range order {
			for i := range c.WebhookRules {
				if c.WebhookRules[i].ID != ruleID {
					continue
				}
				c.WebhookRules[i].History = append(c.WebhookRules[i].History, byRule[ruleID]...)
				if len(c.WebhookRules[i].History) > core.MaxInMemoryHistory {
					excess := len(c.WebhookRules[i].History) - core.MaxInMemoryHistory
					c.WebhookRules[i].History = c.WebhookRules[i].History[excess:]
				}
				break
			}
		}
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolvarr: webhook rule history persist failed: %v\n", err)
	}
}

