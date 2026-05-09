package api

// webhook_adapters.go — per-function single-item engine adapters for the
// M-Webhook dispatcher. Each adapter follows the same shape:
//
//   1. Decode the typed payload from the raw Connect-event body.
//   2. Resolve the rule's effective config (rule snapshot wins over global).
//   3. Call the engine helper in single-item (N=1) mode.
//   4. Diff against the item's current Arr tags, batch the writes via
//      arr.Client.EditorApplyTags.
//   5. Return a stable, sorted summary string for the History row.
//
// Architectural rule 1 (engine-only decisions): NO substring / regex
// matching here. Every tag decision goes through engine.* helpers,
// every Arr write through arr.Client. The bash tagarr_import.sh
// v1.6.0/v1.6.1 SiC-in-Jurassic regression — handler reimplementing
// matches_release_group inline, dropping the word-boundary anchor —
// is the cautionary tale.
//
// Architectural rule 2 (single-item scope): Connect events identify
// ONE specific item (movie/series + optional episode). Adapters
// operate on THAT ITEM ONLY. NEVER walk the full library, NEVER
// iterate all movies/series, NEVER fan out to N items.
//
//   - Engine helpers consume one MediaInfo, not a slice.
//   - Tag reads use arr.Client.GetItemTags(ctx, appType, itemID),
//     NOT ListItems(ctx, appType) which fetches the whole library.
//   - Tag writes use EditorApplyTags(ctx, appType, []int{itemID}, ...),
//     NOT bulk apply to discovered N items.
//   - Recover walks grab-history for the specific movieID / seriesID
//     + episodeID — not series-wide.
//   - Sync to secondary mirrors THIS item's decisions via TmdbID-match
//     to one secondary item — not a full TmdbID cross-walk.
//   - Discover surfaces THIS one releaseGroup if unknown — not a
//     library-wide unknown-group sweep.
//   - File Delete strips managed tags from THIS item only.
//
// Library scan's per-N walks are a deliberate batch model. Webhook
// fires per-event = per-item. Mixing them means a chatty Connect
// feed (whole-season grab → 24 events) collapses to 24 library
// walks per fired rule. Don't introduce that scaling trap.
//
// Engine inputs are typed structs (NOT the lowest-common-denominator
// envelope — that only carries id+title+year for the recent-events
// summary). Decode failures abort that single function with status=
// "error"; sibling functions in the same rule chain still fire.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"resolvarr/internal/arr"
	"resolvarr/internal/core"
	"resolvarr/internal/core/dvdetect"
	"resolvarr/internal/core/engine"
)

// downloadEventPayload captures the Radarr Download / Sonarr Download
// (== Import or Upgrade) event shape we need for the Tag Audio + Tag
// Video + DV adapters. Sonarr carries `episodeFile`, Radarr carries
// `movieFile` — both have the same MediaInfo sub-shape that
// arr.MediaInfo / engine.MediaInfo already model.
//
// Decoded once per (rule × event) fire — cheap relative to the disk +
// network round-trips that follow, so each adapter parses for itself.
//
// IsUpgrade flags whether the import is replacing an existing file
// (CF/quality cutoff) versus a fresh import. Tag Audio + Tag Video
// + Tag DV Details ignore this flag — engine helpers are idempotent
// over mediaInfo, so re-reading the new file's mediaInfo and re-
// emitting overwrites the old set cleanly. Recover / Sync / Discover
// adapters DO care (Recover may already have backfilled releaseGroup
// on the prior import; Sync should mirror only on the actual upgrade
// boundary). Future task #7/#8/#9 will read this field.
type downloadEventPayload struct {
	IsUpgrade bool `json:"isUpgrade,omitempty"`
	// DownloadID is the indexer/download-client tracking id Arr emits
	// on Grab + Download events. Recover uses it to filter the per-
	// item history down to the EXACT Grab event that produced this
	// import (mirrors bash tagarr_import.sh fix_release_group_from_history
	// + tagarr_import_sonarr.sh equivalent — both pin to download_id
	// rather than the looser title+year match Library scan's Recover
	// uses, because we know the precise grab here).
	DownloadID string `json:"downloadId,omitempty"`
	Movie      *struct {
		ID     int    `json:"id"`
		Title  string `json:"title"`
		Year   int    `json:"year,omitempty"`
		TmdbID int    `json:"tmdbId,omitempty"`
	} `json:"movie,omitempty"`
	MovieFile *arr.MovieFile `json:"movieFile,omitempty"`

	Series *struct {
		ID     int    `json:"id"`
		Title  string `json:"title"`
		TvdbID int    `json:"tvdbId,omitempty"`
	} `json:"series,omitempty"`
	Episodes []struct {
		ID            int `json:"id"`
		EpisodeNumber int `json:"episodeNumber"`
		SeasonNumber  int `json:"seasonNumber"`
	} `json:"episodes,omitempty"`
	// Sonarr's episodeFile carries mediaInfo same as Radarr's movieFile.
	// arr.MovieFile field shape matches; we reuse it rather than declare
	// a Sonarr-specific twin. Sonarr-only per-episode fields not
	// relevant to file-mediaInfo-driven adapters are dropped on decode.
	EpisodeFile *arr.MovieFile `json:"episodeFile,omitempty"`
}

// dispatchTagAudio runs the single-item Audio-tag engine path on the
// movie/series the Connect event identifies. Mirrors the Library scan
// runAudioTags loop body but for N=1 — same engine helper, same
// arr.Client.EditorApplyTags writes, same managed-tags safety bound.
//
// Sonarr semantics divergence (LOAD-BEARING): Library scan's Sonarr
// handler aggregates audio tags across ALL episodes of a series with
// the user's per-bucket strategy (all-occurring / strict / highest).
// The webhook adapter only sees ONE episode's mediaInfo at fire-time,
// so it tags the series from that one episode. Two consequences worth
// the user knowing about:
//   - First episode's audio drives the series tags until the next fire.
//   - A subsequent Library scan with all-occurring aggregation may
//     re-introduce tags the webhook removed (Library scan saw 5 episodes,
//     webhook saw 1 — different desired sets).
// This is by design (Connect events are inherently single-file). The
// wizard help-panel for Tag Audio on Sonarr should make this trade-off
// explicit so users understand why the tags they see may flip.
func (s *Server) dispatchTagAudio(
	ctx context.Context,
	rule *core.WebhookRule,
	cfg core.Config,
	tagDetails []arr.TagDetail,
	env *connectEventEnvelope,
	body []byte,
) functionResult {
	if env.EventType != string(core.WebhookEventDownload) {
		return functionResult{Function: core.WebhookFnTagAudio, OK: true, Summary: "skipped (not a Download event)"}
	}
	audioCfg := pickAudioTagsConfig(rule, cfg)
	engineCfg := core.AudioTagsToEngine(audioCfg)
	if !engineCfg.Audio.Enabled {
		return functionResult{Function: core.WebhookFnTagAudio, OK: true, Summary: "skipped (Audio bucket disabled)"}
	}

	var payload downloadEventPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return functionResult{Function: core.WebhookFnTagAudio, OK: false, Summary: "decode payload failed", Err: err}
	}

	ed := extractDownload(rule.AppType, payload)
	if !ed.OK {
		return functionResult{Function: core.WebhookFnTagAudio, OK: true, Summary: "skipped (no mediaInfo on event payload)"}
	}
	// HasMediaInfo guard — when Connect emits before Arr probed mediaInfo
	// (race on slow imports), engine.AudioTagsForFile would return empty
	// desired, which would strip ALL existing managed audio tags from
	// the file (worst case: upgrade event where old file had real tags).
	// DV adapter has the same guard; Audio + Video need it too.
	if !ed.HasMediaInfo {
		return functionResult{Function: core.WebhookFnTagAudio, OK: true, Summary: "skipped (mediaInfo not yet populated — try again on next event)"}
	}

	desired := engine.AudioTagsForFile(ed.MediaInfo, engineCfg)

	var managed map[string]string
	if audioCfg.RemoveOrphanedTags {
		managed = engine.AllPossibleAudioTags(engineCfg)
	} else {
		managed = engine.EmittableAudioTags(engineCfg)
	}

	inst := findInstanceByID(cfg, rule.InstanceID)
	if inst == nil {
		return functionResult{
			Function: core.WebhookFnTagAudio, OK: false,
			Summary: "instance vanished between event receive and dispatch",
		}
	}
	client := s.arrClientFor(inst)

	res := applyAutoTagDiff(ctx, client, rule.AppType, ed.ItemID, desired, managed, tagDetails)
	res.Function = core.WebhookFnTagAudio
	return res
}

// applyAutoTagDiff is the shared diff + lazy-create + apply logic for
// auto-tag adapters (Tag Audio + Tag Video + DV detail). Pulled out so
// task #7 can land Tag Video and Tag DV Details by calling this with
// their own desired/managed sets.
//
// Contract:
//   - desired: stable-ordered slice of labels the engine wants applied
//     (slice order, NOT map order — drives the deterministic summary)
//   - managed: label → bucket name (cleanup safety bound — labels
//     outside this map are not ours and stay untouched)
//   - tagDetails: label↔ID map fetched once per fire by the dispatcher
//     and shared across functions on the same rule
//
// Returns a functionResult with Function unset (caller fills it in)
// and either OK=true with a summary OR OK=false with an error.
//
// "Item not found" (ErrItemNotFound) is treated as a clean skip rather
// than an error — covers the race where the user deleted the item in
// Arr between event receive and dispatcher fire.
func applyAutoTagDiff(
	ctx context.Context,
	client *arr.Client,
	appType string,
	itemID int,
	desired []string,
	managed map[string]string,
	tagDetails []arr.TagDetail,
) functionResult {
	labelToID := make(map[string]int, len(tagDetails))
	idToLabel := make(map[int]string, len(tagDetails))
	for _, t := range tagDetails {
		labelToID[t.Label] = t.ID
		idToLabel[t.ID] = t.Label
	}

	currentTagIDs, err := client.GetItemTags(ctx, appType, itemID)
	if err != nil {
		if errors.Is(err, arr.ErrItemNotFound) {
			return functionResult{OK: true, Summary: "skipped (item no longer in library)"}
		}
		return functionResult{OK: false, Summary: "fetch current tags", Err: err}
	}

	desiredSet := make(map[string]struct{}, len(desired))
	for _, tag := range desired {
		desiredSet[tag] = struct{}{}
	}
	currentManaged := map[string]struct{}{}
	for _, tid := range currentTagIDs {
		label, ok := idToLabel[tid]
		if !ok {
			continue
		}
		if _, isManaged := managed[label]; !isManaged {
			continue
		}
		currentManaged[label] = struct{}{}
	}

	// Iterate the desired SLICE (stable order from the engine), not the
	// set. Map iteration would scramble the summary across identical
	// fires — making Connect retries / replays look different in History.
	var toAdd []string
	for _, tag := range desired {
		if _, alreadyOn := currentManaged[tag]; alreadyOn {
			continue
		}
		toAdd = append(toAdd, tag)
	}
	// Removed labels come from a map; sort for stable summary order.
	var toRemove []string
	for label := range currentManaged {
		if _, stillDesired := desiredSet[label]; stillDesired {
			continue
		}
		toRemove = append(toRemove, label)
	}
	sort.Strings(toRemove)

	if len(toAdd) == 0 && len(toRemove) == 0 {
		return functionResult{
			OK:      true,
			Summary: fmt.Sprintf("no change (%d kept)", len(desiredSet)),
		}
	}

	// Lazy tag-create: webhook fires single-item, so creating one tag
	// at a time stays O(toAdd) per fire. Library scan batches because
	// it walks N items — single-item parity isn't worth the complexity.
	for _, label := range toAdd {
		if _, exists := labelToID[label]; exists {
			continue
		}
		created, cerr := client.CreateTag(ctx, label)
		if cerr != nil {
			return functionResult{OK: false, Summary: fmt.Sprintf("create tag %q", label), Err: cerr}
		}
		labelToID[label] = created.ID
	}

	addIDs := make([]int, 0, len(toAdd))
	for _, label := range toAdd {
		addIDs = append(addIDs, labelToID[label])
	}
	removeIDs := make([]int, 0, len(toRemove))
	for _, label := range toRemove {
		if id, ok := labelToID[label]; ok {
			removeIDs = append(removeIDs, id)
		}
	}

	if len(addIDs) > 0 {
		if err := client.EditorApplyTags(ctx, appType, []int{itemID}, addIDs, "add"); err != nil {
			return functionResult{OK: false, Summary: "apply add", Err: err}
		}
	}
	if len(removeIDs) > 0 {
		if err := client.EditorApplyTags(ctx, appType, []int{itemID}, removeIDs, "remove"); err != nil {
			return functionResult{OK: false, Summary: "apply remove", Err: err}
		}
	}

	kept := len(desiredSet) - len(toAdd)
	return functionResult{OK: true, Summary: formatAutoTagSummary(toAdd, toRemove, kept)}
}

// formatAutoTagSummary builds the user-visible "+N (...) -N (...) =N"
// string. Pure function so it's directly unit-testable without going
// near arr.Client. Parts are joined with " " for terseness in the
// Activity tab's history rows.
func formatAutoTagSummary(toAdd, toRemove []string, kept int) string {
	parts := make([]string, 0, 3)
	if len(toAdd) > 0 {
		parts = append(parts, fmt.Sprintf("+%d (%s)", len(toAdd), strings.Join(toAdd, ", ")))
	}
	if len(toRemove) > 0 {
		parts = append(parts, fmt.Sprintf("-%d (%s)", len(toRemove), strings.Join(toRemove, ", ")))
	}
	if kept > 0 {
		parts = append(parts, fmt.Sprintf("=%d", kept))
	}
	return strings.Join(parts, " ")
}

// dispatchTagDvDetail runs the single-item DV-detail engine path.
// Radarr-only per WebhookFunctionAppliesTo (Sonarr's mediaInfo doesn't
// expose DV profile/layer/CM-version), so the AppType branch is
// pre-validated by the CRUD layer.
//
// Single-item scope (Architectural rule 2):
//   - One file in. inst.TranslatePath(ed.FilePath) for the local mount.
//   - DvCache keyed (FileID, size, mtime, dvVersion) — Connect retries
//     are FREE because the second fire hits the cached extract.
//   - Single dovi_tool invocation. NEVER iterate the library.
//   - applyAutoTagDiff writes to ONE itemID.
//
// HasMediaInfo gating: when the Connect event arrives BEFORE Arr has
// populated mediaInfo (race; rare but real on slow imports), we MUST
// NOT emit no-dv — that would strip existing DV tags from a file
// that actually IS DV. Library scan handles this by skipping the
// row entirely (scan_dv_detail.go:228+ guard); webhook adapter does
// the same via the HasMediaInfo predicate before any emit decision.
//
// HdrTypeIndicatesDv early-skip: with confirmed mediaInfo, when the
// type is SDR or non-DV HDR (HDR10 / HDR10Plus / PQ / blank), we
// emit the no-dv tag without firing dovi_tool. Saves the seconds the
// subprocess would burn on every non-DV import. Branch sits ABOVE the
// tools-check + Status() exec so non-DV fires never pay the
// `dovi_tool --version` cost.
//
// No BypassDvCache toggle: webhook fires are file-identity events —
// (FileID, size, mtime) tuple uniquely identifies the file content,
// so cache invalidation belongs in Library scan UI (where the user
// is debugging a misclassified extract), not on the per-event hot path.
func (s *Server) dispatchTagDvDetail(
	ctx context.Context,
	rule *core.WebhookRule,
	cfg core.Config,
	tagDetails []arr.TagDetail,
	env *connectEventEnvelope,
	body []byte,
) functionResult {
	if env.EventType != string(core.WebhookEventDownload) {
		return functionResult{Function: core.WebhookFnTagDvDetail, OK: true, Summary: "skipped (not a Download event)"}
	}
	dvCfg := pickDvDetailConfig(rule, cfg)
	engineCfg := core.DvDetailToEngine(dvCfg)
	if !engineCfg.Enabled {
		return functionResult{Function: core.WebhookFnTagDvDetail, OK: true, Summary: "skipped (DV detail disabled)"}
	}

	var payload downloadEventPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return functionResult{Function: core.WebhookFnTagDvDetail, OK: false, Summary: "decode payload failed", Err: err}
	}
	ed := extractDownload(rule.AppType, payload)
	if !ed.OK {
		return functionResult{Function: core.WebhookFnTagDvDetail, OK: true, Summary: "skipped (no mediaInfo on event payload)"}
	}
	// HasMediaInfo guard — see doc-comment above for the rationale.
	// When Arr emits the event before mediaInfo is populated, we have
	// no DV signal either way. Skip without touching tags.
	if !ed.HasMediaInfo {
		return functionResult{Function: core.WebhookFnTagDvDetail, OK: true, Summary: "skipped (mediaInfo not yet populated — try again on next event)"}
	}

	inst := findInstanceByID(cfg, rule.InstanceID)
	if inst == nil {
		return functionResult{Function: core.WebhookFnTagDvDetail, OK: false, Summary: "instance vanished between event receive and dispatch"}
	}
	client := s.arrClientFor(inst)

	// Build managed set once — both branches share it.
	var managed map[string]string
	if dvCfg.RemoveOrphanedTags {
		managed = engine.AllPossibleDvDetailTags(engineCfg)
	} else {
		managed = engine.EmittableDvDetailTags(engineCfg)
	}

	// Early-skip BEFORE the tools/Status() exec — non-DV files don't
	// need dovi_tool, so we save the per-event subprocess cost on the
	// 80%+ of imports that are SDR or non-DV HDR.
	if !engine.HdrTypeIndicatesDv(ed.MediaInfo.VideoDynamicRangeType) {
		desired := engine.EmitNoDvTag(engineCfg)
		res := applyAutoTagDiff(ctx, client, rule.AppType, ed.ItemID, desired, managed, tagDetails)
		res.Function = core.WebhookFnTagDvDetail
		return res
	}

	// DV-candidate file. Need dovi_tool. Tools ship baked into the
	// image as of v0.3.5, but defensive check kept — a future slim
	// build target may strip them.
	if s.DvTools.Dir == "" {
		return functionResult{Function: core.WebhookFnTagDvDetail, OK: false, Summary: "DV tools not configured"}
	}
	// Sub-context for the version-probe — same 5s ceiling Library scan
	// uses (scan_dv_detail.go:84). A hung binary can't stall the
	// receive context past this bound.
	toolsCtx, toolsCancel := context.WithTimeout(ctx, 5*time.Second)
	state := s.DvTools.Status(toolsCtx)
	toolsCancel()
	if !state.Installed || state.DvVersion == "" {
		return functionResult{Function: core.WebhookFnTagDvDetail, OK: false, Summary: "DV tools not reachable on PATH"}
	}
	dvVersion := state.DvVersion

	containerPath := inst.TranslatePath(ed.FilePath)
	if containerPath == "" {
		return functionResult{Function: core.WebhookFnTagDvDetail, OK: false, Summary: "Arr returned no path for this file"}
	}
	statInfo, statErr := os.Stat(containerPath)
	if statErr != nil {
		return functionResult{Function: core.WebhookFnTagDvDetail, OK: false, Summary: "media file unreachable: " + containerPath + " — check path mappings"}
	}
	size := statInfo.Size()
	mtime := statInfo.ModTime().Unix()

	// Cache lookup. Connect retries / Upgrade events on the same file
	// hit the cached extract (size + mtime hash); a re-import with
	// new content invalidates via mtime change.
	var detail engine.DvDetail
	var foundRPU bool
	var fromCache bool
	var cacheChanged bool
	if s.DvCache != nil {
		if entry, hit := s.DvCache.Get(ed.FileID, size, mtime, dvVersion); hit {
			detail = entry.Detail
			foundRPU = entry.Found
			fromCache = true
		}
	}
	if !fromCache {
		runner := dvdetect.Runner{
			DvBin: s.DvTools.ResolveDvBin(),
			FfBin: s.DvTools.ResolveFfBin(),
		}
		d, ok, runErr := runner.Detect(ctx, containerPath)
		switch {
		case errors.Is(runErr, dvdetect.ErrToolsMissing):
			return functionResult{Function: core.WebhookFnTagDvDetail, OK: false, Summary: "DV tools missing on PATH"}
		case runErr != nil:
			// Real extraction error → emit no-dv tag (TRaSH parity).
			// Cache nothing — transient error self-heals next fire.
			detail = engine.DvDetail{}
			foundRPU = false
		case !ok:
			// Extraction succeeded; file had no RPU.
			detail = d
			foundRPU = false
			if s.DvCache != nil {
				s.DvCache.Put(ed.FileID, size, mtime, dvVersion, d, false)
				cacheChanged = true
			}
		default:
			detail = d
			foundRPU = true
			if s.DvCache != nil {
				s.DvCache.Put(ed.FileID, size, mtime, dvVersion, d, true)
				cacheChanged = true
			}
		}
	}

	// Persist cache changes so a container restart doesn't lose the
	// extract. Library scan does this once at end-of-scan; webhooks
	// flush per-fire because we have no scan-level boundary. Cost is
	// one atomic .tmp → rename + JSON marshal (~milliseconds for
	// typical cache sizes). Save errors logged but don't fail the
	// adapter — the in-memory entry is still valid for this process'
	// lifetime, matching the runlog persist-failure pattern.
	if cacheChanged && s.DvCache != nil {
		if err := s.DvCache.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "resolvarr: webhook DV cache save failed: %v\n", err)
		}
	}

	var desired []string
	if foundRPU {
		desired = engine.EmitDvDetailTags(detail, engineCfg)
	} else {
		desired = engine.EmitNoDvTag(engineCfg)
	}

	res := applyAutoTagDiff(ctx, client, rule.AppType, ed.ItemID, desired, managed, tagDetails)
	res.Function = core.WebhookFnTagDvDetail
	return res
}

// pickDvDetailConfig is the DV twin of pickAudioTagsConfig — same
// snapshot-vs-global semantics.
func pickDvDetailConfig(rule *core.WebhookRule, cfg core.Config) core.DvDetailConfig {
	if rule.DvDetail != nil {
		return *rule.DvDetail
	}
	return cfg.DvDetail
}

// dispatchDiscover surfaces an unknown release-group from the imported
// file as a new entry in cfg.ReleaseGroups, gated by rule.Filters.
// Radarr-only per WebhookFunctionAppliesTo (Sonarr import-script bash
// has no Discovery concept; matches that scope decision).
//
// Bash-parity flow (`tagarr_import.sh:1086-1165`, ENABLE_DISCOVERY=true):
//   1. Skip when releaseGroup is empty or "Unknown" (bash line 1094).
//   2. Build known-groups set from cfg.ReleaseGroups (matching AppType,
//      both Enabled + Disabled — disabled is the bash-config-commented
//      equivalent and should still suppress re-discovery).
//   3. Skip when group is already known (line 1098).
//   4. Build combined-fields filter input (lowercased relativePath +
//      sceneName + rg) — same as Library scan's runDiscover at
//      scan_discover.go:87-89.
//   5. Run engine.CheckQuality + engine.CheckAudio against rule.Filters.
//      Pass-through → discovery succeeds; reject → silent skip.
//   6. Insert new ReleaseGroup into cfg.ReleaseGroups via ConfigStore.
//      Update. Enabled=DiscoverAutoEnable (rule field; default false →
//      bash AUTO_TAG_DISCOVERED=false → "commented" entry awaiting
//      manual review).
//
// Single-item scope (Architectural rule 2):
//   - One file. Known-set built from cfg.ReleaseGroups (in-memory
//     scan over ~5-50 entries, NOT a library walk).
//   - One config write per fired Connect event for an unknown group.
//   - NEVER calls ListItems.
//
// Connect-retry idempotence: second fire walks the same known-set
// which now contains the just-added group → skip (line 3 above).
//
// Concurrent-safe: ConfigStore.Update holds the store mutex; concurrent
// Connect events that discover the same group race the read-modify-
// write — last-writer-wins inserts a duplicate. Mitigated by the inner
// closure's per-rg-Tag dedup check before append.
func (s *Server) dispatchDiscover(
	ctx context.Context,
	rule *core.WebhookRule,
	cfg core.Config,
	env *connectEventEnvelope,
	body []byte,
) functionResult {
	if env.EventType != string(core.WebhookEventDownload) {
		return functionResult{Function: core.WebhookFnDiscover, OK: true, Summary: "skipped (not a Download event)"}
	}

	var payload downloadEventPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return functionResult{Function: core.WebhookFnDiscover, OK: false, Summary: "decode payload failed", Err: err}
	}
	ed := extractDownload(rule.AppType, payload)
	if !ed.OK {
		return functionResult{Function: core.WebhookFnDiscover, OK: true, Summary: "skipped (no movieFile on event payload)"}
	}

	// Bash skip (line 1094): require non-empty + non-Unknown rg.
	rg := strings.TrimSpace(ed.ReleaseGroup)
	if rg == "" || strings.EqualFold(rg, "Unknown") {
		return functionResult{Function: core.WebhookFnDiscover, OK: true, Summary: "skipped (no releaseGroup on file)"}
	}
	rgLower := strings.ToLower(rg)

	// Known-group set (active OR disabled — disabled is bash-commented
	// equivalent). Match by EITHER Search-collision OR Tag-collision,
	// matching Library scan's applyDiscoverWriteBack dedup at
	// scan_discover.go:222-223. Tag-collision matters when a manual
	// group has Search="oldname" Tag="newgroup" — pure Search-only
	// dedup would let the adapter append a second group with Tag=
	// "newgroup", silently breaking Tag uniqueness.
	for _, g := range cfg.ReleaseGroups {
		if !strings.EqualFold(g.Type, rule.AppType) {
			continue
		}
		if strings.EqualFold(g.Search, rg) || strings.EqualFold(g.Tag, rgLower) {
			return functionResult{Function: core.WebhookFnDiscover, OK: true, Summary: "skipped (group already known: " + g.Search + ")"}
		}
	}

	// Filter pass: same combined-fields-text Library scan's runDiscover
	// uses (scan_discover.go:87-89) — relativePath + sceneName + rg,
	// lowercased + space-joined. Library-scan parity wins over bash-
	// import parity here: tagarr_import.sh:1103 feeds ONLY
	// MOVIE_FILE_RELATIVE to check_quality_match / check_audio_match,
	// while tagarr.sh's batch tagger + container engine.DecideTag use
	// the combined string. Picking combined here keeps webhook + scan
	// emitting identical desired-set decisions for the same file; the
	// `tagarr_import.sh` divergence pre-dates the container.
	combined := strings.ToLower(ed.MediaInfo.RelativePath) + " " +
		strings.ToLower(ed.MediaInfo.SceneName) + " " +
		rgLower
	filterCfg := pickFiltersConfig(rule, cfg)
	if !engine.CheckQuality(filterCfg, combined) {
		return functionResult{Function: core.WebhookFnDiscover, OK: true, Summary: "skipped (failed quality filter)"}
	}
	if !engine.CheckAudio(filterCfg, combined) {
		return functionResult{Function: core.WebhookFnDiscover, OK: true, Summary: "skipped (failed audio filter)"}
	}

	// Build the new ReleaseGroup. Tag uses the lowercased rg (Radarr's
	// `^[a-z0-9-]+$` validator — preserve the strict format the existing
	// release-groups already follow). Search keeps original case
	// (engine.MatchReleaseGroup is case-insensitive but the Display
	// string benefits from preserving what the user sees in releases).
	newGroup := core.ReleaseGroup{
		ID:      genID(),
		Search:  rg,
		Tag:     rgLower,
		Display: rg,
		Mode:    "filtered",
		Type:    rule.AppType,
		Enabled: rule.DiscoverAutoEnable,
	}

	// Persist. Inner dedup check guards against the rare race where two
	// concurrent receivers discover the same group simultaneously, AND
	// catches Tag-collision (Search != "rg" but existing Tag == rgLower).
	var addedID string
	updateErr := s.App.Config.Update(func(c *core.Config) {
		for _, g := range c.ReleaseGroups {
			if !strings.EqualFold(g.Type, rule.AppType) {
				continue
			}
			if strings.EqualFold(g.Search, rg) || strings.EqualFold(g.Tag, rgLower) {
				// Lost the race or Tag-collision — skip the append.
				return
			}
		}
		c.ReleaseGroups = append(c.ReleaseGroups, newGroup)
		addedID = newGroup.ID
	})
	if updateErr != nil {
		return functionResult{Function: core.WebhookFnDiscover, OK: false, Summary: "save discovered group", Err: updateErr}
	}
	if addedID == "" {
		// Race lost — group already present after our pre-check (concurrent
		// Discover beat us). Idempotent semantically, surface it.
		return functionResult{Function: core.WebhookFnDiscover, OK: true, Summary: "skipped (race: another fire just added " + rg + ")"}
	}

	state := "commented (awaiting review)"
	if rule.DiscoverAutoEnable {
		state = "active"
	}

	// Auto-apply path: when DiscoverAutoEnable=true, bash
	// tagarr_import.sh:1169-1184 doesn't just write the new RG to
	// config — it ALSO applies the tag to the triggering movie in
	// the same run. Container's adapter isolation means Tag-RG (which
	// runs after Discover in canonicalFunctionOrder) sees the
	// pre-receive cfg snapshot WITHOUT the new group, so it would
	// miss the tag-apply on the current event. Apply directly here
	// to match bash's auto-tag-discovered semantics ("the import
	// script can tag immediately because it handles a single movie
	// at a time" — bash conf-sample line 252-256).
	if rule.DiscoverAutoEnable {
		inst := findInstanceByID(cfg, rule.InstanceID)
		if inst == nil {
			return functionResult{
				Function: core.WebhookFnDiscover, OK: true,
				Summary: fmt.Sprintf("discovered %s — added as %s (auto-apply skipped: instance vanished)", rg, state),
			}
		}
		client := s.arrClientFor(inst)
		// Get-or-create the tag on Arr side. ListTagDetails per fire
		// is fine — Discover-with-DiscoverAutoEnable is rare (one fire
		// per genuinely-new release-group sighted).
		tagDetails, err := client.ListTagDetails(ctx)
		if err != nil {
			return functionResult{
				Function: core.WebhookFnDiscover, OK: false,
				Summary: fmt.Sprintf("discovered %s but failed to list tags for auto-apply", rg), Err: err,
			}
		}
		var tagID int
		for _, t := range tagDetails {
			if t.Label == newGroup.Tag {
				tagID = t.ID
				break
			}
		}
		if tagID == 0 {
			created, cerr := client.CreateTag(ctx, newGroup.Tag)
			if cerr != nil {
				return functionResult{
					Function: core.WebhookFnDiscover, OK: false,
					Summary: fmt.Sprintf("discovered %s but failed to create tag for auto-apply", rg), Err: cerr,
				}
			}
			tagID = created.ID
		}
		if err := client.EditorApplyTags(ctx, rule.AppType, []int{ed.ItemID}, []int{tagID}, "add"); err != nil {
			return functionResult{
				Function: core.WebhookFnDiscover, OK: false,
				Summary: fmt.Sprintf("discovered %s + added as active but failed to apply tag to current movie", rg), Err: err,
			}
		}
		return functionResult{
			Function: core.WebhookFnDiscover, OK: true,
			Summary: fmt.Sprintf("discovered %s — added as active + applied tag to current movie", rg),
		}
	}

	return functionResult{
		Function: core.WebhookFnDiscover, OK: true,
		Summary: fmt.Sprintf("discovered %s — added as %s", rg, state),
	}
}

// (Doc anchor — see findRecoveryGroupByDownloadID below for the
// helper that does the per-rule history filtering. Library scan's
// equivalent uses engine.FindImportedGrabGroup with title+year fuzzy
// matching; webhook gets exact downloadId from the Connect event so
// the lookup is precise.)
//
// dispatchRecover backfills the moviefile's / episodefile's releaseGroup
// field when the imported file landed without one. Bash-parity with
// `tagarr_import.sh` (Radarr) `fix_release_group_from_history` and
// `tagarr_import_sonarr.sh` equivalent — both walk per-item history,
// filter to the Grab event whose downloadId matches the import event's,
// extract releaseGroup from the Grab's sourceTitle.data, PUT it back
// to the moviefile/episodefile, and trigger a RenameFiles command so
// the file on disk reflects the corrected metadata.
//
// Why this is more precise than Library scan's Recover: webhook receives
// the EXACT download_id of the import, so the history-filter resolves
// to ONE record. Library scan's Recover walks history with title+year
// and picks the most recent matching Grab — fuzzier because no scan-
// time download_id available.
//
// Single-item scope (Architectural rule 2):
//   - One movie/series + one file. ListHistory{ForMovie,ForSeries}
//     fetches per-item history (~50 events typical), NOT a library walk.
//   - Filter to grabbed events with matching downloadId.
//   - Single PUT to update releaseGroup. Single RenameFiles command.
//   - NEVER iterate the library.
//
// Skip conditions (clean OK=true skip, no Arr write):
//   - Not a Download event.
//   - releaseGroup already populated on the file (no recovery needed).
//   - downloadId missing on event (rare — older Arr versions, manual
//     import, Test event).
//   - History walk yields no matching Grab event (downloadId not found
//     in history; could be a manual import).
//
// Filename-safety check (Library scan's scan_recover.go:137-150 uses
// engine.ParseReleaseGroupFromFilename to flag files whose filename
// already contains an RG token but mediaInfo doesn't — preventing the
// scan from overwriting visible-name truth with a different fuzzy-
// matched value). Webhook adapter does NOT replicate this check
// because download_id pinning gives a precise match: the grab event
// whose download_id matches THIS import is the exact source of the
// imported file. No fuzzy-match risk to defend against.
func (s *Server) dispatchRecover(
	ctx context.Context,
	rule *core.WebhookRule,
	cfg core.Config,
	env *connectEventEnvelope,
	body []byte,
) functionResult {
	if env.EventType != string(core.WebhookEventDownload) {
		return functionResult{Function: core.WebhookFnRecover, OK: true, Summary: "skipped (not a Download event)"}
	}

	var payload downloadEventPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return functionResult{Function: core.WebhookFnRecover, OK: false, Summary: "decode payload failed", Err: err}
	}
	ed := extractDownload(rule.AppType, payload)
	if !ed.OK {
		return functionResult{Function: core.WebhookFnRecover, OK: true, Summary: "skipped (no movieFile on event payload)"}
	}

	// Already-populated releaseGroup → bash skips (line 951-952 of
	// tagarr_import.sh). The unknown-string sentinels mirror bash's
	// `[ -z "$RELEASE_GROUP_FIELD" ] || [ "$RELEASE_GROUP_FIELD" = "Unknown" ] || [ "$RELEASE_GROUP_FIELD" = "null" ]`.
	if !needsRecovery(ed.ReleaseGroup) {
		return functionResult{Function: core.WebhookFnRecover, OK: true, Summary: "skipped (releaseGroup already known: " + ed.ReleaseGroup + ")"}
	}

	if ed.DownloadID == "" {
		return functionResult{Function: core.WebhookFnRecover, OK: true, Summary: "skipped (no downloadId on event — manual import?)"}
	}

	if ed.FileID == 0 {
		return functionResult{Function: core.WebhookFnRecover, OK: true, Summary: "skipped (no fileId on event)"}
	}

	inst := findInstanceByID(cfg, rule.InstanceID)
	if inst == nil {
		return functionResult{Function: core.WebhookFnRecover, OK: false, Summary: "instance vanished between event receive and dispatch"}
	}
	client := s.arrClientFor(inst)

	// Fetch per-item history. Radarr: /api/v3/history/movie?movieId=N.
	// Sonarr: /api/v3/history/series?seriesId=N. Both return ~50 events
	// for a typical library item — bounded, not a library walk.
	var history []arr.HistoryRecord
	var listErr error
	switch rule.AppType {
	case "radarr":
		history, listErr = client.ListHistoryForMovie(ctx, ed.ItemID)
	case "sonarr":
		history, listErr = client.ListHistoryForSeries(ctx, ed.ItemID)
	default:
		return functionResult{Function: core.WebhookFnRecover, OK: false, Summary: "unsupported appType: " + rule.AppType}
	}
	if listErr != nil {
		return functionResult{Function: core.WebhookFnRecover, OK: false, Summary: "list history", Err: listErr}
	}

	// Find the Grab event whose downloadId matches our import.
	grabRG, found := findRecoveryGroupByDownloadID(history, ed.DownloadID)
	if !found {
		return functionResult{Function: core.WebhookFnRecover, OK: true, Summary: "skipped (no grab event in history matched downloadId " + ed.DownloadID + ")"}
	}
	if grabRG == "" {
		return functionResult{Function: core.WebhookFnRecover, OK: true, Summary: "skipped (matched grab event had no releaseGroup field)"}
	}

	// Read-modify-write the file record. Same map[string]any-preserve-
	// every-field pattern Library scan's Recover uses (avoids stripping
	// audio/video/quality fields Arr expects on PUT).
	var currentJSON []byte
	var fetchErr error
	switch rule.AppType {
	case "radarr":
		currentJSON, fetchErr = client.GetMovieFile(ctx, ed.FileID)
	case "sonarr":
		currentJSON, fetchErr = client.GetEpisodefile(ctx, ed.FileID)
	}
	if fetchErr != nil {
		return functionResult{Function: core.WebhookFnRecover, OK: false, Summary: "fetch file record", Err: fetchErr}
	}

	var updateErr error
	switch rule.AppType {
	case "radarr":
		updateErr = client.UpdateMovieFileReleaseGroup(ctx, ed.FileID, currentJSON, grabRG)
	case "sonarr":
		updateErr = client.UpdateEpisodefileReleaseGroup(ctx, ed.FileID, currentJSON, grabRG)
	}
	if updateErr != nil {
		return functionResult{Function: core.WebhookFnRecover, OK: false, Summary: "patch releaseGroup", Err: updateErr}
	}

	// Trigger RenameFiles so the file on disk reflects the corrected
	// metadata. Best-effort: a rename failure doesn't unwind the field
	// patch above — releaseGroup is already corrected in Arr's DB, so
	// subsequent Tag-RG runs work; the file just keeps its current
	// (unrenamed) name until a manual rename or scheduled run.
	var renameErr error
	switch rule.AppType {
	case "radarr":
		renameErr = client.TriggerRadarrRenameFiles(ctx, ed.ItemID, []int{ed.FileID})
	case "sonarr":
		renameErr = client.TriggerSonarrRenameFiles(ctx, ed.ItemID, []int{ed.FileID})
	}
	if renameErr != nil {
		// Field patch succeeded — surface the rename failure as a
		// partial. Status="ok" because the load-bearing work landed;
		// the rename is cosmetic.
		return functionResult{
			Function: core.WebhookFnRecover, OK: true,
			Summary: fmt.Sprintf("recovered releaseGroup=%s (rename command failed: %v)", grabRG, renameErr),
		}
	}

	return functionResult{
		Function: core.WebhookFnRecover, OK: true,
		Summary: fmt.Sprintf("recovered releaseGroup=%s + triggered rename", grabRG),
	}
}

// needsRecovery returns true when the event-payload releaseGroup is
// missing or one of Arr's "no-group" sentinels. Mirrors bash:
// `[ -z "$RELEASE_GROUP_FIELD" ] || [ "$RELEASE_GROUP_FIELD" = "Unknown" ] || [ "$RELEASE_GROUP_FIELD" = "null" ]`.
func needsRecovery(rg string) bool {
	rg = strings.TrimSpace(rg)
	if rg == "" {
		return true
	}
	switch strings.ToLower(rg) {
	case "unknown", "null":
		return true
	}
	return false
}

// findRecoveryGroupByDownloadID walks the history list for the Grab
// event whose downloadId matches and returns its parsed releaseGroup.
// Bash:
//
//	jq -r --arg dlid "$download_id" 'map(select(.eventType=="grabbed"
//	   and .downloadId == $dlid)) | sort_by(.date) | last | .data |
//	   (.releaseGroup // .ReleaseGroup // "")'
//
// Returns ("", false) when no Grab event matches the downloadId.
// Returns (rg, true) when matched and rg can be extracted, with two
// fallback layers:
//
//  1. Arr's pre-parsed data.releaseGroup field (bash-parity primary)
//  2. engine.ParseReleaseGroupTolerant on data.sourceTitle when (1)
//     is empty — fixes the Rango/Matilda failure mode where Arr's
//     own filename parser bombs on " - <RG>" but the indexer release-
//     title still has the rg in extractable form.
//
// Returns ("", true) ONLY when both layers fail (rare — Arr's parser
// bombed AND tolerant parser couldn't find rg in sourceTitle either).
// Caller treats as "matched but empty" → skip with no error.
//
// downloadId comparison is case-insensitive — qBit + Arr have been
// observed disagreeing on hash casing across the link, exactly the
// same defence the existing findInstanceByWebhookToken uses for tokens.
// Bash uses straight `==`; Go is more defensive (one-line, no perf cost).
func findRecoveryGroupByDownloadID(history []arr.HistoryRecord, downloadID string) (string, bool) {
	if downloadID == "" {
		return "", false
	}
	var match *arr.HistoryRecord
	for i := range history {
		if history[i].EventType != "grabbed" {
			continue
		}
		// Case-insensitive: qBit + Arr have been observed disagreeing
		// on hash casing across the link. Bash uses straight `==`; we
		// add EqualFold defence-in-depth (perf cost is one ASCII tolower
		// per record, negligible on a ~50-record history walk).
		if !strings.EqualFold(history[i].DownloadID, downloadID) {
			continue
		}
		// Most recent Grab wins on duplicates (re-grab of the same
		// release would create another record). Container is more
		// defensive than bash here: bash jq pipeline takes `.[0]`
		// (first record returned by Arr API, which is typically
		// newest-first but not guaranteed). Container sorts
		// explicitly by Date.After so the contract is independent
		// of API response ordering.
		if match == nil || history[i].Date.After(match.Date) {
			match = &history[i]
		}
	}
	if match == nil {
		return "", false
	}
	rg := match.ReleaseGroup()
	if rg != "" {
		return rg, true
	}
	// Tolerant fallback: when Arr's parser bombed at grab-time (e.g.
	// indexer title "Rango 2011 ... DoVi - SumVision" — strict parser
	// rejects " SumVision" as multi-token), data.releaseGroup is empty
	// but data.sourceTitle still has the rg embedded. Run our tolerant
	// parser on the original release-title to recover it.
	if match.SourceTitle != "" {
		if tolerantRG, ok := engine.ParseReleaseGroupTolerant(match.SourceTitle); ok {
			return tolerantRG, true
		}
	}
	return "", true
}

// dispatchSyncToSecondary mirrors the rule's release-group tag decisions
// from the primary instance to a secondary Radarr instance via TmdbID
// match. Bash-parity with `tagarr_import.sh:1244-1306` (ENABLE_SYNC_TO_
// SECONDARY=true) — bash mirrors the just-computed tags_to_add/
// tags_to_remove sets from primary to secondary's matching movie.
//
// Container divergence (architecturally cleaner): adapter recomputes
// the rule's RG decisions independently for the secondary instance
// (using the same engine.DecideTag flow Tag-RG uses, with ed.MediaInfo
// from the primary's import event). This avoids cross-adapter state
// passing while still landing the same desired-set on secondary
// because rule snapshots are deterministic.
//
// Auto-tags (Audio/Video/DV) are NOT synced — they're file-property-
// derived from the secondary's OWN file (which may be a different
// release; e.g. 4K secondary copy has different mediaInfo than 1080p
// primary). The scheduler-runner has per-bucket AudioTagsTarget /
// VideoTagsTarget for that flow; webhook scope keeps Sync narrow to
// release-group decisions matching the bash baseline.
//
// SyncSkipOrphanCleanup gates the strip-pass: when false (default),
// adapter does a full reconcile against the rule's RG snapshot
// (stricter than bash — strips secondary's managed-RG tags that don't
// match the rule's current decision, even if they came from manual
// edits or earlier Library scans). When true, only emits ADDS — bash-
// additive flow that leaves secondary's existing tags untouched.
// Library scan's full-library M3e Sync has an orphan-cleanup-pass
// across all secondary movies; the webhook can't replicate that per
// event without breaking single-item scope. Run a periodic Library
// scan Tag-RG with Sync for full secondary reconciliation.
//
// Single-item scope (Architectural rule 2):
//   - Secondary lookup via arr.Client.GetMovieByTmdbID — single round-
//     trip, NOT ListItems library-walk.
//   - applyAutoTagDiff on secondary's matching movie ID only.
//   - Skip cleanly when no TmdbID, no secondary configured, or movie
//     missing in secondary.
//
// Radarr-only per WebhookFunctionAppliesTo (validator-gated). Sonarr
// has no equivalent in bash and is out of scope.
//
// Secondary-instance resolution:
//   - rule.SyncToInstanceID populated → use that instance.
//   - empty → scheduler-style "first other Radarr instance != primary"
//     fallback. Mirrors core.JobOptions.SyncToInstanceID semantics.
func (s *Server) dispatchSyncToSecondary(
	ctx context.Context,
	rule *core.WebhookRule,
	cfg core.Config,
	env *connectEventEnvelope,
	body []byte,
) functionResult {
	if env.EventType != string(core.WebhookEventDownload) {
		return functionResult{Function: core.WebhookFnSyncToSecondary, OK: true, Summary: "skipped (not a Download event)"}
	}

	// Bash-parity gate: bash tagarr_import.sh:1244-1306 mirrors
	// primary's tags_to_add/tags_to_remove sets — both are empty when
	// the rule didn't run RG-tagging on primary. Container's recompute-
	// on-secondary model would otherwise STRIP managed-RG tags from
	// secondary even when no primary decision happened (rule has only
	// [SyncToSecondary]) — surprising for users migrating from bash.
	// Gate Sync on Tag-RG so the rule has to declare the tag-decision
	// intent for the mirror to fire.
	if !rule.HasFunction(core.WebhookFnTagReleaseGroups) {
		return functionResult{Function: core.WebhookFnSyncToSecondary, OK: true, Summary: "skipped (rule must enable Tag Release Groups for Sync to mirror)"}
	}

	var payload downloadEventPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return functionResult{Function: core.WebhookFnSyncToSecondary, OK: false, Summary: "decode payload failed", Err: err}
	}
	ed := extractDownload(rule.AppType, payload)
	if !ed.OK {
		return functionResult{Function: core.WebhookFnSyncToSecondary, OK: true, Summary: "skipped (no movieFile on event payload)"}
	}
	if ed.TmdbID == 0 {
		return functionResult{Function: core.WebhookFnSyncToSecondary, OK: true, Summary: "skipped (no tmdbId — can't cross-match)"}
	}

	// Resolve secondary instance.
	secondary := pickSyncTarget(rule, cfg)
	if secondary == nil {
		return functionResult{Function: core.WebhookFnSyncToSecondary, OK: true, Summary: "skipped (no secondary " + rule.AppType + " instance configured)"}
	}
	if secondary.ID == rule.InstanceID {
		return functionResult{Function: core.WebhookFnSyncToSecondary, OK: false, Summary: "syncToInstanceId points at the rule's primary — invalid config"}
	}

	secClient := s.arrClientFor(secondary)
	secMovie, found, err := secClient.GetMovieByTmdbID(ctx, ed.TmdbID)
	if err != nil {
		return functionResult{Function: core.WebhookFnSyncToSecondary, OK: false, Summary: "lookup secondary by tmdbId", Err: err}
	}
	if !found {
		return functionResult{
			Function: core.WebhookFnSyncToSecondary, OK: true,
			Summary: fmt.Sprintf("skipped (movie tmdbId=%d not in %s library)", ed.TmdbID, secondary.Name),
		}
	}

	// Recompute the rule's RG decisions for the secondary, using the
	// SAME mediaInfo + filter snapshot the primary fired with. Engine
	// is deterministic; rule snapshots are identical between adapter
	// invocations; result on secondary's tag set equals what Tag-RG
	// adapter would have computed had it fired on the secondary's
	// counterpart event (modulo the file actually existing there).
	groups := resolveRuleReleaseGroups(rule, cfg)
	if len(groups) == 0 {
		return functionResult{Function: core.WebhookFnSyncToSecondary, OK: true, Summary: "skipped (no active release groups for this rule)"}
	}
	filterCfg := pickFiltersConfig(rule, cfg)
	mf := engine.MovieFile{
		RelativePath: ed.MediaInfo.RelativePath,
		SceneName:    ed.MediaInfo.SceneName,
		ReleaseGroup: ed.ReleaseGroup,
	}
	var desired []string
	managed := map[string]string{}
	for _, g := range groups {
		// Always include in managed when computing desired so we can
		// emit ShouldHave decisions; whether managed flows into
		// applyAutoTagDiff (and thus drives orphan-removal) is gated
		// by SyncSkipOrphanCleanup below.
		managed[g.Tag] = "releaseGroup"
		d := engine.DecideTag(mf, engine.GroupConfig{
			Search:  g.Search,
			Tag:     g.Tag,
			Display: g.Display,
			Mode:    g.Mode,
		}, filterCfg)
		if d.ShouldHave {
			desired = append(desired, g.Tag)
		}
	}

	// Honour SyncSkipOrphanCleanup. Field semantics (matching the doc-
	// comment on core.WebhookRule.SyncSkipOrphanCleanup):
	//   - false (default): full reconcile — adapter's recompute on
	//     secondary may strip managed-RG tags secondary has that
	//     don't match the rule's current decision. Stricter than
	//     bash; consistent with rule-snapshot-as-truth model.
	//   - true: adds-only (matches bash-additive flow at
	//     tagarr_import.sh:1262-1290). Pass empty managed →
	//     applyAutoTagDiff's currentManaged will be empty →
	//     toRemove=[] → only adds emit. Tags secondary has from
	//     other sources (manual edits, prior Library scans) survive.
	syncManaged := managed
	if rule.SyncSkipOrphanCleanup {
		syncManaged = map[string]string{}
	}

	// Need secondary's own tag-details map (separate from the receive-
	// scoped cache which is keyed on the rule's primary). Single
	// ListTagDetails per Sync fire — bounded.
	secTagDetails, err := secClient.ListTagDetails(ctx)
	if err != nil {
		return functionResult{Function: core.WebhookFnSyncToSecondary, OK: false, Summary: "list secondary tags", Err: err}
	}

	res := applyAutoTagDiff(ctx, secClient, secondary.Type, secMovie.ID, desired, syncManaged, secTagDetails)
	res.Function = core.WebhookFnSyncToSecondary
	// Prefix the secondary's name on EVERY return path (success AND
	// error) so Activity-tab History rows always show which instance
	// the operation targeted — debugging "apply add: <error>" without
	// knowing it failed on secondary is otherwise opaque.
	res.Summary = "→ " + secondary.Name + ": " + res.Summary
	return res
}

// pickSyncTarget resolves the rule's secondary-instance target —
// explicit SyncToInstanceID wins; empty falls back to "first other
// instance of matching AppType". Mirrors scheduler runner's secondary-
// pick semantics (jobs.go SyncToInstanceID doc-comment).
//
// Returns nil when no candidate exists (rule has only one instance of
// its AppType) — adapter treats nil as a clean skip.
func pickSyncTarget(rule *core.WebhookRule, cfg core.Config) *core.Instance {
	if rule.SyncToInstanceID != "" {
		for i := range cfg.Instances {
			if cfg.Instances[i].ID == rule.SyncToInstanceID {
				return &cfg.Instances[i]
			}
		}
		return nil
	}
	// Fallback: first other instance of matching AppType (NOT the
	// rule's primary). Deterministic over Instances slice ordering;
	// scheduler does the same.
	for i := range cfg.Instances {
		inst := &cfg.Instances[i]
		if inst.ID == rule.InstanceID {
			continue
		}
		if !strings.EqualFold(inst.Type, rule.AppType) {
			continue
		}
		return inst
	}
	return nil
}

// dispatchTagReleaseGroups runs the single-item release-group tag flow
// on the imported file. Radarr-only per WebhookFunctionAppliesTo —
// Sonarr import-time tagging on release-groups is out of scope (no
// Active-groups concept on series in the current model; deferred).
//
// Single-item scope (Architectural rule 2):
//   - One file in. RelativePath / SceneName / ReleaseGroup from event payload.
//   - Per-rule-group iteration → engine.DecideTag(mf, group, filterCfg)
//     produces ShouldHave booleans.
//   - desired = group.Tag for each ShouldHave=true group.
//   - managed = group.Tag for every resolved group (regardless of decision).
//   - applyAutoTagDiff writes to ONE itemID.
//   - NEVER iterate the library — Library scan's per-N walk is the
//     batch model; this is the per-event single-file mirror.
//
// Filter snapshot: rule.Filters (pointer) wins over cfg.Filters.Radarr.
// nil-rule.Filters → use globals (back-compat for pre-snapshot rules).
//
// Group subset: rule.ReleaseGroupIDs nil → all of cfg.ReleaseGroups
// matching AppType + Enabled. Populated subset → only listed IDs.
// Empty []string → no groups (clean skip; user explicitly opted out).
func (s *Server) dispatchTagReleaseGroups(
	ctx context.Context,
	rule *core.WebhookRule,
	cfg core.Config,
	tagDetails []arr.TagDetail,
	env *connectEventEnvelope,
	body []byte,
) functionResult {
	if env.EventType != string(core.WebhookEventDownload) {
		return functionResult{Function: core.WebhookFnTagReleaseGroups, OK: true, Summary: "skipped (not a Download event)"}
	}

	var payload downloadEventPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return functionResult{Function: core.WebhookFnTagReleaseGroups, OK: false, Summary: "decode payload failed", Err: err}
	}
	ed := extractDownload(rule.AppType, payload)
	if !ed.OK {
		return functionResult{Function: core.WebhookFnTagReleaseGroups, OK: true, Summary: "skipped (no movieFile on event payload)"}
	}

	groups := resolveRuleReleaseGroups(rule, cfg)
	if len(groups) == 0 {
		return functionResult{Function: core.WebhookFnTagReleaseGroups, OK: true, Summary: "skipped (no active release groups for this rule)"}
	}

	filterCfg := pickFiltersConfig(rule, cfg)

	mf := engine.MovieFile{
		RelativePath: ed.MediaInfo.RelativePath,
		SceneName:    ed.MediaInfo.SceneName,
		ReleaseGroup: ed.ReleaseGroup,
	}

	// Walk the resolved groups, computing desired (ShouldHave=true) +
	// managed (every group, for cleanup safety bound).
	var desired []string
	managed := map[string]string{}
	for _, g := range groups {
		managed[g.Tag] = "releaseGroup"
		d := engine.DecideTag(mf, engine.GroupConfig{
			Search:  g.Search,
			Tag:     g.Tag,
			Display: g.Display,
			Mode:    g.Mode,
		}, filterCfg)
		if d.ShouldHave {
			desired = append(desired, g.Tag)
		}
	}

	inst := findInstanceByID(cfg, rule.InstanceID)
	if inst == nil {
		return functionResult{Function: core.WebhookFnTagReleaseGroups, OK: false, Summary: "instance vanished between event receive and dispatch"}
	}
	client := s.arrClientFor(inst)

	res := applyAutoTagDiff(ctx, client, rule.AppType, ed.ItemID, desired, managed, tagDetails)
	res.Function = core.WebhookFnTagReleaseGroups
	return res
}

// resolveRuleReleaseGroups returns the ReleaseGroups the rule should
// evaluate against — filtered by AppType + Enabled flag + the rule's
// ReleaseGroupIDs subset (when populated). Mirrors the scheduler-runner
// resolution at fire-time.
//
// Edge cases:
//   - rule.ReleaseGroupIDs == nil → all of cfg.ReleaseGroups of matching
//     AppType + Enabled (pre-migration / "use globals" semantics).
//   - rule.ReleaseGroupIDs == []string{} → empty (user explicitly chose
//     zero groups; rule effectively a no-op for Tag-RG / Discover).
//   - rule.ReleaseGroupIDs populated → only listed IDs, still filtered
//     by AppType + Enabled.
func resolveRuleReleaseGroups(rule *core.WebhookRule, cfg core.Config) []core.ReleaseGroup {
	out := []core.ReleaseGroup{}
	for _, rg := range cfg.ReleaseGroups {
		// Empty-Tag / empty-Search defence-in-depth — Library scan
		// drops these (scan_tag.go:59-61) so engine.DecideTag can't
		// route ShouldHave=true with an empty label. API validator
		// rejects these at save-time but a hand-edited resolvarr.json
		// could slip through; cheap belt-and-braces.
		if rg.Tag == "" || rg.Search == "" {
			continue
		}
		if !strings.EqualFold(rg.Type, rule.AppType) {
			continue
		}
		if !rg.Enabled {
			continue
		}
		if rule.ReleaseGroupIDs != nil {
			matched := false
			for _, id := range rule.ReleaseGroupIDs {
				if id == rg.ID {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		out = append(out, rg)
	}
	return out
}

// pickFiltersConfig resolves the engine.FilterConfig the rule should use
// — rule snapshot when populated, global per-Arr-type config when nil.
// Matches the per-rule snapshot architectural rule on
// core.WebhookRule.Filters.
func pickFiltersConfig(rule *core.WebhookRule, cfg core.Config) engine.FilterConfig {
	if rule.Filters != nil {
		return *rule.Filters
	}
	switch rule.AppType {
	case "radarr":
		return cfg.Filters.Radarr
	case "sonarr":
		return cfg.Filters.Sonarr
	}
	return engine.FilterConfig{}
}

// dispatchFileDeleteCleanup strips managed tags from the item when its
// file is deleted. Fires on Radarr's MovieFileDelete and Sonarr's
// EpisodeFileDelete. Connect-payload shape mirrors the Download event
// (movie+movieFile / series+episodeFile) sans isUpgrade + with an
// extra deleteReason field — neither relevant to the strip flow.
//
// Single-item scope (Architectural rule 2):
//   - One movie/series ID identified by the event.
//   - Strip ALL managed tags from THAT item only.
//   - Single GetItemTags + single EditorApplyTags("remove", ...).
//   - NEVER walk the library, NEVER scan other items.
//
// Managed universe semantics (load-bearing): the rule's snapshots
// define what "managed by us" means. AudioTags / VideoTags / DvDetail
// snapshots → AllPossible*Tags(cfg). ReleaseGroupIDs → resolved subset
// of cfg.ReleaseGroups[].Tag values; nil/empty subset falls back to
// the GLOBAL list (pre-migration rules + scheduler-style "all" intent).
//
// Why "AllPossible" and not "Emittable": cleanup-on-delete must remove
// tags the user previously had under a now-disabled bucket. Emittable
// would skip tags from disabled buckets; AllPossible includes them.
// Same safety bound Library scan's cleanup-unused-tags pass uses.
//
// File Delete IS NOT GATED on rule.Functions including the auto-tag
// functions. A rule with [FileDeleteCleanup] alone still strips
// audio/video/DV/RG tags via the rule's snapshots — the file is gone,
// every derived tag should follow.
//
// Bash-divergence (bredth): bash file-delete (tagarr_import.sh:597-
// 610) only strips RELEASE_GROUPS-derived tags. Container's union
// covers Audio + Video + DV-derived tags too. This catches cases
// bash misses (mediaInfo-tags from a deleted upgrade-source) but
// can collide with USER-MANUAL tags whose label happens to match an
// engine-vocabulary value (e.g. user manually added "truehd" as a
// personal tracking tag — File Delete strips it). Pre-existing trap
// shared with Library scan's cleanup pass; flag in wizard help.
//
// Sonarr-aggressivity: tags are series-level on Sonarr. One episode-
// delete strips ALL managed series-tags via this adapter, even though
// the other N-1 episodes still drive the aggregated set. Library scan
// Audio/Video re-applies on next batch run. Pair FileDeleteCleanup
// with a scheduled Library scan Audio/Video for clean reconciliation
// on Sonarr; otherwise series-tags are transiently empty between
// the EpisodeFileDelete fire and the next aggregation run.
func (s *Server) dispatchFileDeleteCleanup(
	ctx context.Context,
	rule *core.WebhookRule,
	cfg core.Config,
	tagDetails []arr.TagDetail,
	env *connectEventEnvelope,
	body []byte,
) functionResult {
	// Event-type gate: Radarr fires MovieFileDelete + MovieFileDeleteForUpgrade,
	// Sonarr fires EpisodeFileDelete + EpisodeFileDeleteForUpgrade. The
	// ForUpgrade variants must trigger cleanup too — when the old file
	// makes way for a higher-quality replacement, its mediaInfo-derived
	// tags need to come off (incoming file's webhook fires Tag Audio /
	// Video / DV with the new mediaInfo, which would only diff against
	// any existing managed tags; without this delete-side strip, stale
	// codec/atmos/dv tags from the old file linger). Bash
	// tagarr_import.sh:574 defends against both variants.
	isRadarrDelete := env.EventType == string(core.WebhookEventMovieFileDelete) ||
		env.EventType == string(core.WebhookEventMovieFileDeleteForUpgrade)
	isSonarrDelete := env.EventType == string(core.WebhookEventEpisodeFileDelete) ||
		env.EventType == string(core.WebhookEventEpisodeFileDeleteForUpgrade)
	switch rule.AppType {
	case "radarr":
		if !isRadarrDelete {
			return functionResult{Function: core.WebhookFnFileDeleteClean, OK: true, Summary: "skipped (not a Radarr file-delete event)"}
		}
	case "sonarr":
		if !isSonarrDelete {
			return functionResult{Function: core.WebhookFnFileDeleteClean, OK: true, Summary: "skipped (not a Sonarr file-delete event)"}
		}
	default:
		return functionResult{Function: core.WebhookFnFileDeleteClean, OK: true, Summary: "skipped (unsupported appType)"}
	}

	var payload downloadEventPayload // delete events share the movie+movieFile / series+episodeFile shape
	if err := json.Unmarshal(body, &payload); err != nil {
		return functionResult{Function: core.WebhookFnFileDeleteClean, OK: false, Summary: "decode payload failed", Err: err}
	}
	// Reuse the Download extractor — the delete payload carries the
	// same item-identity fields. mediaInfo will be absent/empty (Arr
	// has nothing to probe), but we don't need it: this is a strip-
	// only flow, no engine emit.
	itemID := pickItemIDForDelete(rule.AppType, payload)
	if itemID == 0 {
		return functionResult{Function: core.WebhookFnFileDeleteClean, OK: true, Summary: "skipped (no movie/series id on event payload)"}
	}

	inst := findInstanceByID(cfg, rule.InstanceID)
	if inst == nil {
		return functionResult{Function: core.WebhookFnFileDeleteClean, OK: false, Summary: "instance vanished between event receive and dispatch"}
	}
	client := s.arrClientFor(inst)

	// Build the union managed-set across every config family the rule
	// covers. Snapshot-or-global falls through per the WebhookRule
	// per-rule snapshot rule (architectural twin of scheduler).
	managed := buildFileDeleteManagedSet(rule, cfg)
	if len(managed) == 0 {
		return functionResult{Function: core.WebhookFnFileDeleteClean, OK: true, Summary: "skipped (rule manages no tag universe)"}
	}

	// Empty desired set + populated managed set means "remove every
	// managed tag that's currently on the item". applyAutoTagDiff
	// already does exactly this — pass desired=nil and it computes
	// toRemove = currentManaged - desired = currentManaged.
	res := applyAutoTagDiff(ctx, client, rule.AppType, itemID, nil, managed, tagDetails)
	res.Function = core.WebhookFnFileDeleteClean
	return res
}

// pickItemIDForDelete extracts the movie or series ID from a file-delete
// event payload. Unlike Download events we don't need mediaInfo — just
// the parent ID we're stripping tags from.
func pickItemIDForDelete(appType string, p downloadEventPayload) int {
	switch appType {
	case "radarr":
		if p.Movie != nil {
			return p.Movie.ID
		}
	case "sonarr":
		if p.Series != nil {
			return p.Series.ID
		}
	}
	return 0
}

// buildFileDeleteManagedSet unions every managed-tag universe the rule
// can reach: AudioTags + VideoTags + DvDetail (Radarr only) + the
// resolved release-group tags. Each branch consults rule snapshot first,
// global config as fallback (per-rule-snapshot architectural rule).
//
// Returned map: tag-label → bucket-name (matches the contract
// applyAutoTagDiff expects from engine.AllPossible*Tags). The bucket-
// name is informational here — applyAutoTagDiff doesn't read it.
func buildFileDeleteManagedSet(rule *core.WebhookRule, cfg core.Config) map[string]string {
	out := map[string]string{}
	merge := func(in map[string]string) {
		for k, v := range in {
			out[k] = v
		}
	}

	// Audio (both Arrs)
	audioCfg := pickAudioTagsConfig(rule, cfg)
	merge(engine.AllPossibleAudioTags(core.AudioTagsToEngine(audioCfg)))

	// Video (both Arrs)
	videoCfg := pickVideoTagsConfig(rule, cfg)
	merge(engine.AllPossibleVideoTags(core.VideoTagsToEngine(videoCfg)))

	// DV detail (Radarr only — Sonarr mediaInfo lacks the fields)
	if rule.AppType == "radarr" {
		dvCfg := pickDvDetailConfig(rule, cfg)
		merge(engine.AllPossibleDvDetailTags(core.DvDetailToEngine(dvCfg)))
	}

	// Release-group tags. Two cases:
	//   nil ReleaseGroupIDs: pre-migration / "use globals" semantics —
	//     every cfg.ReleaseGroup of the matching Arr type counts as
	//     managed. Mirrors scheduler runner.
	//   populated subset: only the listed RGs count as managed. User
	//     who narrowed their rule to {FLUX, NTb} doesn't accidentally
	//     strip an unrelated rg-XEBEC tag they've manually applied.
	rgIDs := rule.ReleaseGroupIDs
	for _, rg := range cfg.ReleaseGroups {
		if !strings.EqualFold(rg.Type, rule.AppType) {
			continue
		}
		if rgIDs != nil {
			// Subset mode — only listed IDs count.
			matched := false
			for _, id := range rgIDs {
				if id == rg.ID {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		if rg.Tag != "" {
			out[rg.Tag] = "releaseGroup"
		}
	}

	return out
}

// pickAudioTagsConfig resolves the AudioTagsConfig the rule should run
// against — rule snapshot when populated, global config when nil. Mirrors
// the per-rule snapshot architectural rule documented on
// core.WebhookRule.AudioTags.
func pickAudioTagsConfig(rule *core.WebhookRule, cfg core.Config) core.AudioTagsConfig {
	if rule.AudioTags != nil {
		return *rule.AudioTags
	}
	return cfg.AudioTags
}

// pickVideoTagsConfig is the Video twin of pickAudioTagsConfig — same
// snapshot-vs-global semantics, different bucket family (Resolution +
// Codec + HDR rather than the single Audio bucket).
func pickVideoTagsConfig(rule *core.WebhookRule, cfg core.Config) core.VideoTagsConfig {
	if rule.VideoTags != nil {
		return *rule.VideoTags
	}
	return cfg.VideoTags
}

// dispatchTagVideo runs the single-item Video-tag engine path on the
// movie/series the Connect event identifies. Mirror of dispatchTagAudio
// — same shape, different engine helper. Resolution / codec / HDR are
// separate buckets but share one diff/apply path via applyAutoTagDiff.
//
// Sonarr semantics divergence applies identically here: webhook sees one
// episode's mediaInfo, Library scan aggregates across the whole series.
// See dispatchTagAudio for the load-bearing comment.
func (s *Server) dispatchTagVideo(
	ctx context.Context,
	rule *core.WebhookRule,
	cfg core.Config,
	tagDetails []arr.TagDetail,
	env *connectEventEnvelope,
	body []byte,
) functionResult {
	if env.EventType != string(core.WebhookEventDownload) {
		return functionResult{Function: core.WebhookFnTagVideo, OK: true, Summary: "skipped (not a Download event)"}
	}
	videoCfg := pickVideoTagsConfig(rule, cfg)
	engineCfg := core.VideoTagsToEngine(videoCfg)
	if !engineCfg.Resolution.Enabled && !engineCfg.Codec.Enabled && !engineCfg.HDR.Enabled {
		return functionResult{Function: core.WebhookFnTagVideo, OK: true, Summary: "skipped (no video buckets enabled)"}
	}

	var payload downloadEventPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return functionResult{Function: core.WebhookFnTagVideo, OK: false, Summary: "decode payload failed", Err: err}
	}

	ed := extractDownload(rule.AppType, payload)
	if !ed.OK {
		return functionResult{Function: core.WebhookFnTagVideo, OK: true, Summary: "skipped (no mediaInfo on event payload)"}
	}
	// HasMediaInfo guard — same race protection as Tag Audio + DV.
	// Without this, a fresh-import event arriving before mediaInfo is
	// populated would strip existing managed video tags via empty
	// desired-set diff.
	if !ed.HasMediaInfo {
		return functionResult{Function: core.WebhookFnTagVideo, OK: true, Summary: "skipped (mediaInfo not yet populated — try again on next event)"}
	}

	desired := engine.VideoTagsForFile(ed.MediaInfo, ed.QualityResolution, engineCfg)

	var managed map[string]string
	if videoCfg.RemoveOrphanedTags {
		managed = engine.AllPossibleVideoTags(engineCfg)
	} else {
		managed = engine.EmittableVideoTags(engineCfg)
	}

	inst := findInstanceByID(cfg, rule.InstanceID)
	if inst == nil {
		return functionResult{
			Function: core.WebhookFnTagVideo, OK: false,
			Summary: "instance vanished between event receive and dispatch",
		}
	}
	client := s.arrClientFor(inst)

	res := applyAutoTagDiff(ctx, client, rule.AppType, ed.ItemID, desired, managed, tagDetails)
	res.Function = core.WebhookFnTagVideo
	return res
}

// extractedDownload bundles the fields downstream adapters care about
// from a Connect Download event. Struct-shape (vs the previous 4-tuple)
// scales cleanly as more adapters land — Tag DV Details needs the
// absolute path for dovi_tool extraction, Tag Release Groups needs
// the parsed releaseGroup, Recover needs the file ID for the moviefile
// PUT. Adding a field here is a one-liner; growing a tuple beyond 4
// returns is the antipattern we cut off here.
//
// OK is the "this event carries the file we need" predicate. False
// means Test event / older Arr without mediaInfo / unknown AppType.
//
// HasMediaInfo distinguishes "Arr definitely probed this file and these
// are the values" (true, even when fields are zero — file is genuinely
// SDR/no-codec/etc.) from "Arr emitted the file row before mediaInfo
// was populated" (false). Tag DV Details specifically MUST NOT strip
// existing DV tags when HasMediaInfo=false — the file might be DV but
// we just can't tell yet. Library scan parallel: scan_dv_detail.go
// guards on `item.MovieFile.MediaInfo != nil` for the same reason.
type extractedDownload struct {
	OK              bool
	ItemID          int
	MediaInfo       engine.MediaInfo
	HasMediaInfo    bool
	QualityResolution int
	ReleaseGroup    string // movieFile.releaseGroup or episodeFile.releaseGroup — empty when the release didn't carry one
	FileID          int    // movieFile.id / episodeFile.id — for adapters that PUT to /api/v3/moviefile/{id}
	FilePath        string // absolute path Arr reports — translated through Instance.PathMappings by adapters that open the file (Tag DV Details)
	DownloadID      string // event-level download_id — Recover pins history-walk to the exact Grab event that produced this import
	TmdbID          int    // movie.tmdbId from event — Sync-to-secondary uses to find the matching movie on the target instance
}

// extractDownload pulls the file-aware shape from the typed Connect
// payload, gated by Arr type. Returns ed.OK=false when the relevant
// *File is nil (Test stub, older Arr without mediaInfo, unknown
// AppType).
//
// Generalised from the Audio-only helper so every Download-driven
// adapter (Tag Audio / Tag Video / Tag DV Details / Tag Release
// Groups / Recover / Sync / Discover) can share one extraction path.
func extractDownload(appType string, p downloadEventPayload) extractedDownload {
	var file *arr.MovieFile
	var itemID int
	var tmdbID int
	switch appType {
	case "radarr":
		if p.Movie == nil || p.MovieFile == nil {
			return extractedDownload{}
		}
		itemID = p.Movie.ID
		tmdbID = p.Movie.TmdbID
		file = p.MovieFile
	case "sonarr":
		// Sonarr applies tags at the SERIES level — episode metadata
		// determines WHAT to tag, but the ID written to is the series
		// ID. Same model as the Library scan auto-tags Sonarr handler.
		// See dispatchTagAudio's Sonarr-divergence comment for the
		// load-bearing semantics this implies.
		if p.Series == nil || p.EpisodeFile == nil {
			return extractedDownload{}
		}
		itemID = p.Series.ID
		file = p.EpisodeFile
	default:
		return extractedDownload{}
	}
	out := extractedDownload{
		OK:           true,
		ItemID:       itemID,
		FileID:       file.ID,
		FilePath:     file.Path,
		ReleaseGroup: file.ReleaseGroup,
		DownloadID:   p.DownloadID,
		TmdbID:       tmdbID,
	}
	if file.MediaInfo != nil {
		out.HasMediaInfo = true
		out.MediaInfo = engine.MediaInfo{
			Height:                  file.MediaInfo.Height,
			VideoCodec:              file.MediaInfo.VideoCodec,
			VideoBitDepth:           file.MediaInfo.VideoBitDepth,
			VideoDynamicRangeType:   file.MediaInfo.VideoDynamicRangeType,
			AudioCodec:              file.MediaInfo.AudioCodec,
			AudioChannels:           file.MediaInfo.AudioChannels,
			AudioAdditionalFeatures: file.MediaInfo.AudioAdditionalFeatures,
		}
	}
	// RelativePath + SceneName live on MovieFile, not MediaInfo, so they
	// carry through regardless of HasMediaInfo. Engine's audio Atmos-
	// fallback reads RelativePath when audioAdditionalFeatures is blank.
	out.MediaInfo.RelativePath = file.RelativePath
	out.MediaInfo.SceneName = file.SceneName
	if file.Quality != nil {
		out.QualityResolution = file.Quality.Quality.Resolution
	}
	return out
}

// findInstanceByID returns a pointer into cfg.Instances. Adapters use
// this to resolve the rule's primary InstanceID + sync target's
// SyncToInstanceID to live arr.Client objects. Returns nil if the
// instance was deleted between Connect-event receive and adapter
// dispatch — the calling adapter should treat that as a clean skip.
func findInstanceByID(cfg core.Config, id string) *core.Instance {
	for i := range cfg.Instances {
		if cfg.Instances[i].ID == id {
			return &cfg.Instances[i]
		}
	}
	return nil
}
