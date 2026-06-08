package api

// scan_plex_sync.go — engine for the Plex label-sync feature.
//
// Computes the per-rule diff between an Arr's tags and a Plex library's
// labels, then either applies the diff (runMode="apply") or just
// reports it (runMode="preview"). Bi-directional within the rule's
// label whitelist:
//
//   - Arr has tag X, Plex doesn't have label X, X in whitelist
//     → ADD label X on Plex
//   - Arr lacks tag X, Plex has label X, X in whitelist
//     → REMOVE label X on Plex
//   - Plex has label Y, Y is NOT in whitelist
//     → UNTOUCHED (manual user labels outside the rule's scope stay)
//
// Match strategy is 4-tier (high → low confidence):
//
//   1. TMDB + IMDB compound key — Plex item's TMDB GUID + IMDB GUID
//      both match the same Arr item. Strongest, defends against the
//      rare Plex-scrape-error where one ID points at the wrong title.
//   2. TVDB + IMDB compound key — Sonarr equivalent. Same defence.
//   3. Single ID — TMDB → TVDB → IMDB in that order. Catches items
//      where Plex/Arr only carry one ID.
//   4. Normalised title + year — last resort. Year is required to
//      disambiguate remakes.
//
// Engine is structured as a pure-ish function: takes the rule + live
// Arr/Plex clients + a tag-ID→label map; returns a PlexLabelRuleRun.
// Callers (scheduler, webhook adapter, one-off wizard handler) build
// the clients + invoke the engine + persist the result.

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"resolvarr/internal/arr"
	"resolvarr/internal/core"
	"resolvarr/internal/plex"
)

// Caps for error + PerLabel slice growth live on core.PlexLabelRuleRun
// alongside the type — see PlexLabelRunErrorCap / PlexLabelRunPerLabelCap.

// runPlexLabelSyncForItem is the per-event variant of runPlexLabelSync
// — diff + apply for ONE pre-identified Arr item against ONE Plex
// label rule. Used by the webhook adapter where the upstream Tag*
// functions have just modified the item's Arr-side tag set and we
// want to propagate those changes to Plex right away, without
// walking the whole library.
//
// Inputs:
//
//	ctx        — request-scoped cancellation
//	rule       — the saved PlexLabelRule (snapshot)
//	arrItem    — the Arr item with its post-Tag-functions tag set
//	             already loaded. Caller fetched this AFTER any tag-
//	             modifying functions ran so the diff sees the final
//	             state.
//	arrClient  — pointed at the rule's bound Arr (used to load
//	             tag-ID → label name map)
//	plexClient — pointed at the rule's target Plex
//	plexInstance — for the cached Libraries name lookup
//	trigger    — "webhook" (or "manual" if invoked from a CLI later)
//	runMode    — overrides rule.RunMode for the caller
//
// Returns a PlexLabelRuleRun with ItemsTotal=1 + Matched=0|1. Caller
// (webhook adapter) decides whether to log + persist to rule history.
func (s *Server) runPlexLabelSyncForItem(
	ctx context.Context,
	rule core.PlexLabelRule,
	arrItem arr.Item,
	arrClient *arr.Client,
	plexClient *plex.Client,
	plexInstance core.PlexInstance,
	trigger string,
	runMode string,
) core.PlexLabelRuleRun {
	startedAt := time.Now()
	if runMode == "" {
		runMode = rule.RunMode
	}
	if runMode != "apply" && runMode != "preview" {
		runMode = "apply"
	}

	run := core.PlexLabelRuleRun{
		StartedAt: startedAt,
		Trigger:   trigger,
		RunMode:   runMode,
		Status:    "ok",
		Added:     map[string]int{},
		Removed:   map[string]int{},
		InSync:    map[string]int{},
	}
	fail := func(err error, summary string) core.PlexLabelRuleRun {
		run.Status = "error"
		run.Errors = append(run.Errors, err.Error())
		run.Summary = summary
		run.DurationMs = time.Since(startedAt).Milliseconds()
		return run
	}

	// 1. Tag-ID → label map (same as bulk run; needed to translate
	// whitelist label-names against the item's tag-ID array).
	tagDetails, err := arrClient.ListTagDetails(ctx)
	if err != nil {
		return fail(err, "Failed to load tags from Arr")
	}
	tagIDByLabel := make(map[string]int, len(tagDetails))
	for _, td := range tagDetails {
		key := strings.ToLower(strings.TrimSpace(td.Label))
		if key == "" {
			continue
		}
		tagIDByLabel[key] = td.ID
	}

	whitelistedTagIDs := make(map[int]string, len(rule.Labels))
	missingFromArr := []string{}
	for _, lbl := range rule.Labels {
		trimmed := strings.TrimSpace(lbl)
		if trimmed == "" {
			continue
		}
		id, ok := tagIDByLabel[strings.ToLower(trimmed)]
		if !ok {
			missingFromArr = append(missingFromArr, trimmed)
			continue
		}
		whitelistedTagIDs[id] = rule.DisplayLabel(trimmed)
	}
	whitelistByLower := make(map[string]string, len(rule.Labels))
	for _, lbl := range rule.Labels {
		trimmed := strings.TrimSpace(lbl)
		if trimmed != "" {
			canonical := rule.DisplayLabel(trimmed)
			whitelistByLower[strings.ToLower(canonical)] = canonical
		}
	}

	// 2. Library type lookup — needed for the per-item Plex API call.
	libTitleByKey := make(map[string]string, len(plexInstance.Libraries))
	libTypeByKey := make(map[string]string, len(plexInstance.Libraries))
	for _, lib := range plexInstance.Libraries {
		libTitleByKey[lib.Key] = lib.Title
		libTypeByKey[lib.Key] = lib.Type
	}
	if len(rule.Targets) == 0 {
		return fail(fmt.Errorf("rule has no targets"), "Rule has no Plex targets")
	}
	tgt := rule.Targets[0]
	if len(tgt.LibraryKeys) == 0 {
		return fail(fmt.Errorf("rule target has no libraries"), "Rule target has no libraries")
	}

	// 3. Find the matching Plex item by walking the rule's target
	// libraries. Per-event flow doesn't have a pre-built index — we
	// fetch each library's items + do a single lookup against the
	// known Arr IDs. For typical libraries this is one fetch per
	// configured library (1-3). plexapi does the same on watch-state
	// changes.
	//
	// Match strategy mirrors the bulk path: build a 1-item index of
	// the Arr side, then look up in each library's items via the
	// same matchPlexItemToArrItem helper.
	idx := buildPlexMatchIndex([]arr.Item{arrItem})
	// ItemsTotal = 1: the run represents one Arr item, regardless of how
	// many target libraries hold a copy.
	run.ItemsTotal = 1

	targetTypes := rule.EffectiveTargetTypes()
	matchedAny := false

	// Apply to EVERY selected target library that holds this item — not
	// just the first. A rule syncing to N libraries must label the item
	// in all of them; the old break-on-first-match labelled only the
	// first library and left the rest untouched.
	for _, libKey := range tgt.LibraryKeys {
		items, err := plexClient.GetItems(ctx, libKey)
		if err != nil {
			run.AppendError(fmt.Sprintf("library %s (%s): %v", libTitleByKey[libKey], libKey, err))
			run.Status = "partial"
			continue
		}

		// Find this library's copy of the Arr item, if present.
		var matchedPlex plex.Item
		found := false
		for _, plexItem := range items {
			if arr2, _ := matchPlexItemToArrItem(plexItem, idx); arr2 != nil {
				matchedPlex = plexItem
				found = true
				break
			}
		}
		if !found {
			// This selected library simply doesn't hold the item — not an
			// error; skip and try the next.
			continue
		}
		matchedAny = true

		matchedLibType := libTypeByKey[libKey]
		if matchedLibType == "" {
			matchedLibType = matchedPlex.Type
		}

		// Per-item metadata fetch — Label[] + Collection[] are omitted
		// from /all on many Plex versions; /library/metadata/{ratingKey}
		// is the reliable read.
		currentLabels := matchedPlex.Labels
		currentCollections := matchedPlex.Collections
		if full, err := plexClient.GetItemMetadata(ctx, matchedPlex.RatingKey); err != nil {
			run.AppendError(fmt.Sprintf("fetch metadata for %q: %v", matchedPlex.Title, err))
			// keep going with bulk values
		} else {
			currentLabels = full.Labels
			currentCollections = full.Collections
		}

		// Per-target-type diff + apply for THIS library's copy. Mirrors
		// the bulk path's per-item block; InSync recorded only on the
		// first target pass so multi-target rules don't double-count.
		for tIdx, targetType := range targetTypes {
			currentTagsForDiff := currentLabels
			if targetType == "collection" {
				currentTagsForDiff = currentCollections
			}
			diff := computePlexLabelDiff(
				arrItem.Tags,
				currentTagsForDiff,
				whitelistedTagIDs,
				whitelistByLower,
			)
			if tIdx == 0 {
				for _, lbl := range diff.inSync {
					run.InSync[lbl]++
				}
			}
			if len(diff.add) == 0 && len(diff.remove) == 0 {
				continue
			}
			useCollection := targetType == "collection"
			for _, lbl := range diff.add {
				if runMode == "apply" {
					var err error
					if useCollection {
						err = plexClient.AddCollection(ctx, libKey, matchedPlex.RatingKey, matchedLibType, lbl)
					} else {
						err = plexClient.AddLabel(ctx, libKey, matchedPlex.RatingKey, matchedLibType, lbl)
					}
					if err != nil {
						run.AppendError(fmt.Sprintf("add %q (%s) on %q: %v", lbl, targetType, matchedPlex.Title, err))
						run.Status = "partial"
						continue
					}
				}
				run.Added[lbl]++
				run.Changed = true
				run.AppendPerLabel(core.PlexLabelChange{
					Title:   matchedPlex.Title,
					Year:    matchedPlex.Year,
					Label:   lbl,
					Action:  "add",
					Target:  targetType,
					Library: libTitleByKey[libKey],
				})
			}
			for _, lbl := range diff.remove {
				if runMode == "apply" {
					var err error
					if useCollection {
						err = plexClient.RemoveCollection(ctx, libKey, matchedPlex.RatingKey, matchedLibType, lbl)
					} else {
						err = plexClient.RemoveLabel(ctx, libKey, matchedPlex.RatingKey, matchedLibType, lbl)
					}
					if err != nil {
						run.AppendError(fmt.Sprintf("remove %q (%s) on %q: %v", lbl, targetType, matchedPlex.Title, err))
						run.Status = "partial"
						continue
					}
				}
				run.Removed[lbl]++
				run.Changed = true
				run.AppendPerLabel(core.PlexLabelChange{
					Title:   matchedPlex.Title,
					Year:    matchedPlex.Year,
					Label:   lbl,
					Action:  "remove",
					Target:  targetType,
					Library: libTitleByKey[libKey],
				})
			}
		}
	}

	if !matchedAny {
		run.Unmatched = 1
		run.Summary = summarisePlexLabelRun(run, missingFromArr)
		run.DurationMs = time.Since(startedAt).Milliseconds()
		return run
	}
	run.Matched = 1

	if runMode == "preview" {
		run.Changed = false
	}
	run.Summary = summarisePlexLabelRun(run, missingFromArr)
	run.DurationMs = time.Since(startedAt).Milliseconds()
	return run
}

// runPlexLabelSync executes a single rule fire. Returns the
// PlexLabelRuleRun summary; caller appends to rule.History.
//
// Inputs:
//
//	ctx       — request-scoped cancellation
//	rule      — the saved PlexLabelRule (snapshot — engine doesn't
//	            re-read from store, so concurrent edits don't change
//	            the rule mid-fire)
//	arrClient — pre-built Arr client pointing at the rule's
//	            InstanceID. Caller resolves config → client.
//	plexClient — pre-built Plex client pointing at the rule's
//	            target Plex instance.
//	plexInstance — the PlexInstance struct (for the cached
//	            Libraries name lookup in result summaries)
//	trigger   — "scheduled" | "webhook" | "manual" — recorded on the
//	            PlexLabelRuleRun so the Activity tab can render the
//	            origin.
//	runMode   — overrides the rule's stored RunMode. Lets the one-off
//	            wizard run a saved "apply" rule in preview-mode for
//	            verification, or vice versa. Empty string inherits
//	            from the rule. Validated to "apply" or "preview".
func (s *Server) runPlexLabelSync(
	ctx context.Context,
	rule core.PlexLabelRule,
	arrClient *arr.Client,
	plexClient *plex.Client,
	plexInstance core.PlexInstance,
	trigger string,
	runMode string,
) core.PlexLabelRuleRun {
	startedAt := time.Now()
	if runMode == "" {
		runMode = rule.RunMode
	}
	if runMode != "apply" && runMode != "preview" {
		runMode = "apply"
	}

	run := core.PlexLabelRuleRun{
		StartedAt: startedAt,
		Trigger:   trigger,
		RunMode:   runMode,
		Status:    "ok",
		Added:     map[string]int{},
		Removed:   map[string]int{},
		InSync:    map[string]int{},
	}

	// Helper to finalise the run with a top-level error + early exit.
	// Keeps the timing + counters consistent across error paths.
	fail := func(err error, summary string) core.PlexLabelRuleRun {
		run.Status = "error"
		run.Errors = append(run.Errors, err.Error())
		run.Summary = summary
		run.DurationMs = time.Since(startedAt).Milliseconds()
		return run
	}

	// 1. Pull the Arr's full tag list so we can resolve tag-ID arrays
	//    to label names. The rule stores label names (case + spelling
	//    as the user typed); we lower-case-compare against Arr labels
	//    + Plex labels too.
	tagDetails, err := arrClient.ListTagDetails(ctx)
	if err != nil {
		return fail(err, "Failed to load tags from Arr")
	}
	tagIDByLabel := make(map[string]int, len(tagDetails))
	for _, td := range tagDetails {
		key := strings.ToLower(strings.TrimSpace(td.Label))
		if key == "" {
			continue
		}
		tagIDByLabel[key] = td.ID
	}

	// Resolve each whitelist label to the Arr tag-ID. Labels not yet
	// in Arr are recorded as warnings — the rule WILL still run,
	// those labels just can't match anything until the tag exists in
	// Arr. The "canonical" label stored in the map is the
	// DISPLAY string (rule.DisplayLabel applies any per-tag override
	// from rule.LabelDisplay) — that's what gets written to Plex.
	whitelistedTagIDs := make(map[int]string, len(rule.Labels)) // tagID → display label (what we write to Plex)
	missingFromArr := []string{}
	for _, lbl := range rule.Labels {
		trimmed := strings.TrimSpace(lbl)
		if trimmed == "" {
			continue
		}
		id, ok := tagIDByLabel[strings.ToLower(trimmed)]
		if !ok {
			missingFromArr = append(missingFromArr, trimmed)
			continue
		}
		whitelistedTagIDs[id] = rule.DisplayLabel(trimmed)
	}
	// Lower-case set of whitelist labels — used for Plex-side scoping
	// (manual labels outside the whitelist are sacrosanct). Keyed by
	// the DISPLAY label's lower-case so REMOVE matching catches the
	// label we'd write under the override, not the Arr-side raw name.
	whitelistByLower := make(map[string]string, len(rule.Labels))
	for _, lbl := range rule.Labels {
		trimmed := strings.TrimSpace(lbl)
		if trimmed != "" {
			canonical := rule.DisplayLabel(trimmed)
			whitelistByLower[strings.ToLower(canonical)] = canonical
		}
	}

	// 2. Pull every Arr item (movies for Radarr, series for Sonarr)
	//    with its tag list. Same path Tag inventory uses.
	arrItems, err := arrClient.ListItems(ctx, rule.AppType)
	if err != nil {
		return fail(err, "Failed to load items from Arr")
	}

	// Build the 4-tier match lookup tables in one pass.
	idx := buildPlexMatchIndex(arrItems)

	// 3. Build the Plex library-key → title map for the run summary
	//    + verify all rule.target.LibraryKeys exist on the live Plex.
	libTitleByKey := make(map[string]string, len(plexInstance.Libraries))
	libTypeByKey := make(map[string]string, len(plexInstance.Libraries))
	for _, lib := range plexInstance.Libraries {
		libTitleByKey[lib.Key] = lib.Title
		libTypeByKey[lib.Key] = lib.Type
	}

	if len(rule.Targets) == 0 {
		return fail(fmt.Errorf("rule has no targets"), "Rule has no Plex targets")
	}
	tgt := rule.Targets[0]
	if len(tgt.LibraryKeys) == 0 {
		return fail(fmt.Errorf("rule target has no libraries"), "Rule target has no libraries")
	}

	// 4. Walk each target library, fetch its items, compute + apply
	//    the per-item diff. Errors on individual libraries don't
	//    abort the run — we collect + continue, then surface in the
	//    summary so a misconfigured library doesn't lose work on the
	//    other libraries.
	//
	// matchedArrIDs deduplicates the Matched counter — an Arr item
	// that exists in two target libraries (rare but real: a movie
	// added to both Movies and Movies 4K) would otherwise count
	// twice, breaking the invariant `Matched <= distinct Arr items`.
	// Track by Arr ID, increment only on first sight.
	matchedArrIDs := make(map[int]struct{})
	for _, libKey := range tgt.LibraryKeys {
		items, err := plexClient.GetItems(ctx, libKey)
		if err != nil {
			run.AppendError(fmt.Sprintf("library %s (%s): %v", libTitleByKey[libKey], libKey, err))
			run.Status = "partial"
			continue
		}
		for _, plexItem := range items {
			run.ItemsTotal++
			// Match Plex item → Arr item via the ID + title tiers.
			arrItem, matchType := matchPlexItemToArrItem(plexItem, idx)

			// prefetched holds a per-item /library/metadata fetch when
			// tier 5 needed it (shows don't carry their folder path in
			// the bulk listing). Reused below for Label[]/Collection[]
			// so a path-matched item isn't fetched twice.
			var prefetched *plex.Item

			// Tier 5: path match. Only fires when every ID + title tier
			// missed. Shows lack Path in the bulk listing, so fetch the
			// per-item metadata to learn the folder first — bounded
			// cost, only for the unmatched gap.
			if arrItem == nil {
				path := plexItem.Path
				if path == "" {
					if full, err := plexClient.GetItemMetadata(ctx, plexItem.RatingKey); err == nil {
						prefetched = &full
						path = full.Path
					}
				}
				if path != "" {
					if a, t := matchPlexPathToArrItem(path, idx); a != nil {
						arrItem, matchType = a, t
					}
				}
			}

			if arrItem == nil {
				run.Unmatched++
				continue
			}
			// Increment Matched only on first sight of this Arr ID
			// — a movie added to both Movies and Movies 4K
			// libraries would otherwise count twice.
			if _, seen := matchedArrIDs[arrItem.ID]; !seen {
				matchedArrIDs[arrItem.ID] = struct{}{}
				run.Matched++
			}
			_ = matchType // tier label is debugging-only for now

			// Per-item metadata fetch — /library/sections/{key}/all
			// omits Label[] + Collection[] on some Plex Server
			// versions regardless of query params. Per-item
			// /library/metadata/{ratingKey} is the only reliable
			// way to read them. Falls back to the bulk-fetched
			// values on per-item failure so a single broken
			// ratingKey doesn't abort the run.
			currentLabels := plexItem.Labels
			currentCollections := plexItem.Collections
			if prefetched != nil {
				// Tier 5 already fetched this item — reuse it.
				currentLabels = prefetched.Labels
				currentCollections = prefetched.Collections
			} else if full, err := plexClient.GetItemMetadata(ctx, plexItem.RatingKey); err != nil {
				run.AppendError(fmt.Sprintf("fetch metadata for %q: %v", plexItem.Title, err))
				// keep going with bulk values — better partial than aborted
			} else {
				currentLabels = full.Labels
				currentCollections = full.Collections
			}
			itemType := plexItem.Type
			if itemType == "" {
				// Default to library type when Plex didn't echo back
				// the item type (rare but harmless guard).
				itemType = libTypeByKey[libKey]
			}

			// Per-target-type pass. Rules can target Labels,
			// Collections, or both; engine runs one full diff +
			// apply pass per target type using that side's current
			// metadata state. Aggregated counters mean +60 FEL on
			// the result modal might be 30 label + 30 collection
			// when both targets are picked — per-item PerLabel
			// rows carry a Target field so the detail list shows
			// which side each change is for. InSync is recorded
			// only on the FIRST target pass so the in-sync count
			// reflects "items already correct" (not "items × target
			// types"); add/remove counters DO sum across targets
			// because each is a distinct Plex write.
			targetTypes := rule.EffectiveTargetTypes()
			for tIdx, targetType := range targetTypes {
				currentTagsForDiff := currentLabels
				if targetType == "collection" {
					currentTagsForDiff = currentCollections
				}
				diff := computePlexLabelDiff(
					arrItem.Tags,
					currentTagsForDiff,
					whitelistedTagIDs,
					whitelistByLower,
				)
				// Record "in sync" counters first — these are no-
				// action items but the user wants to see how many
				// of each label are already correctly applied so
				// the math adds up against their known Arr-side
				// totals. Only on the first target pass to avoid
				// double-counting when a rule targets both Labels
				// and Collections — add/remove counts DO sum across
				// targets (each is a distinct Plex write).
				if tIdx == 0 {
					for _, lbl := range diff.inSync {
						run.InSync[lbl]++
					}
				}
				if len(diff.add) == 0 && len(diff.remove) == 0 {
					continue
				}
				useCollection := targetType == "collection"
				for _, lbl := range diff.add {
					if runMode == "apply" {
						var err error
						if useCollection {
							err = plexClient.AddCollection(ctx, libKey, plexItem.RatingKey, itemType, lbl)
						} else {
							err = plexClient.AddLabel(ctx, libKey, plexItem.RatingKey, itemType, lbl)
						}
						if err != nil {
							run.AppendError(fmt.Sprintf("add %q (%s) on %q: %v", lbl, targetType, plexItem.Title, err))
							run.Status = "partial"
							continue
						}
					}
					run.Added[lbl]++
					run.Changed = true
					run.AppendPerLabel(core.PlexLabelChange{
						Title:   plexItem.Title,
						Year:    plexItem.Year,
						Label:   lbl,
						Action:  "add",
						Target:  targetType,
						Library: libTitleByKey[libKey],
					})
				}
				for _, lbl := range diff.remove {
					if runMode == "apply" {
						var err error
						if useCollection {
							err = plexClient.RemoveCollection(ctx, libKey, plexItem.RatingKey, itemType, lbl)
						} else {
							err = plexClient.RemoveLabel(ctx, libKey, plexItem.RatingKey, itemType, lbl)
						}
						if err != nil {
							run.AppendError(fmt.Sprintf("remove %q (%s) on %q: %v", lbl, targetType, plexItem.Title, err))
							run.Status = "partial"
							continue
						}
					}
					run.Removed[lbl]++
					run.Changed = true
					run.AppendPerLabel(core.PlexLabelChange{
						Title:   plexItem.Title,
						Year:    plexItem.Year,
						Label:   lbl,
						Action:  "remove",
						Target:  targetType,
						Library: libTitleByKey[libKey],
					})
				}
			}
		}
	}

	// Preview mode never actually mutated state — clear the Changed
	// flag so the Activity tab's "Made changes" filter doesn't show
	// preview runs as if labels were written.
	if runMode == "preview" {
		run.Changed = false
	}

	run.Summary = summarisePlexLabelRun(run, missingFromArr)
	run.DurationMs = time.Since(startedAt).Milliseconds()
	return run
}

// plexLabelDiff is the per-item add/remove decision.
type plexLabelDiff struct {
	add    []string // label names to add to Plex (case as stored in whitelist)
	remove []string // label names to remove from Plex (case as it appears on Plex)
	// inSync collects whitelist labels that are already correctly
	// applied on this item (Arr has tag + Plex has label). Lets the
	// runner record per-label "no action needed" counts so the
	// result modal can show "FEL: +60 add, 0 remove, 33 in sync"
	// alongside the actions. Items where Arr lacks tag AND Plex
	// lacks label are NOT recorded — too noisy (every other item).
	inSync []string
}

// computePlexLabelDiff is the core decision function. Pure — easy to
// unit-test. Given an item's Arr tags + Plex labels + the whitelist
// (with tag-IDs resolved to canonical labels), returns the add/remove
// pair.
//
// Whitelist scope is enforced at both ends:
//
//   - ADD: only labels in the whitelist that Arr currently carries +
//     Plex doesn't already have. Case-insensitive compare against Plex
//     labels — Plex preserves whatever case the user typed for manual
//     labels.
//   - REMOVE: only Plex labels that match the whitelist + Arr doesn't
//     carry. Manual Plex labels outside the whitelist are untouched
//     even when Arr lacks the corresponding tag.
func computePlexLabelDiff(
	arrTagIDs []int,
	plexLabels []string,
	whitelistedTagIDs map[int]string,
	whitelistByLower map[string]string,
) plexLabelDiff {
	// Which whitelist labels does Arr currently have on this item?
	arrHas := make(map[string]bool, len(whitelistedTagIDs))
	for _, tid := range arrTagIDs {
		if canonical, ok := whitelistedTagIDs[tid]; ok {
			arrHas[strings.ToLower(canonical)] = true
		}
	}
	// Which whitelist labels does Plex currently have on this item?
	// Plex's label case is preserved as the user typed it; compare
	// case-insensitively to avoid double-tagging "4K" + "4k".
	plexHas := make(map[string]string, len(plexLabels))
	for _, pl := range plexLabels {
		lower := strings.ToLower(pl)
		if _, ok := whitelistByLower[lower]; ok {
			plexHas[lower] = pl
		}
	}

	diff := plexLabelDiff{}
	// ADD: whitelist labels Arr has + Plex doesn't.
	// IN-SYNC: whitelist labels Arr has + Plex has — already correct,
	// no action needed but worth recording so the result modal can
	// show "N already in sync" per label.
	for _, canonical := range whitelistedTagIDs {
		key := strings.ToLower(canonical)
		if arrHas[key] {
			if plexHas[key] == "" {
				diff.add = append(diff.add, canonical)
			} else {
				diff.inSync = append(diff.inSync, canonical)
			}
		}
	}
	// REMOVE: whitelist labels Plex has + Arr doesn't.
	for lowerLabel, plexExact := range plexHas {
		if !arrHas[lowerLabel] {
			diff.remove = append(diff.remove, plexExact)
		}
	}
	// Stable order so tests + summaries are deterministic.
	sort.Strings(diff.add)
	sort.Strings(diff.remove)
	sort.Strings(diff.inSync)
	return diff
}

// plexMatchIndex carries the 5-tier lookup tables built once per run.
// Compound keys defend against Plex's occasional scrape errors where
// one ID points at the wrong title; single-ID tiers catch items where
// only one identifier is present; title+year is a fallback; the path
// tiers are the last resort for items whose external IDs are missing or
// disagree between Plex and the Arr (Sonarr TVDB-primary vs a Plex item
// matched by the TMDB agent, or two different TVDB entries for the same
// show). pathFull/pathBase key the same Arr items by their on-disk
// folder so the file location bridges them — the file is the same on
// disk regardless of what ID each system assigned.
type plexMatchIndex struct {
	tmdbImdb map[plexCompoundKey]*arr.Item // (tmdbID, imdbID) → item
	tvdbImdb map[plexCompoundKey]*arr.Item // (tvdbID, imdbID) → item
	tmdb     map[int]*arr.Item
	tvdb     map[int]*arr.Item
	imdb     map[string]*arr.Item
	titleYr  map[plexTitleYearKey]*arr.Item // (normalisedTitle, year) → item
	pathFull map[string]*arr.Item           // normalised Arr folder path → item
	pathBase map[string]*arr.Item           // lower-cased folder basename → item (mount-agnostic); nil value = ambiguous (shared by 2+ items), never matched
}

type plexCompoundKey struct {
	intID  int
	textID string
}

type plexTitleYearKey struct {
	title string
	year  int
}

// buildPlexMatchIndex builds every lookup table in a single pass over
// the Arr item list. ~O(N) memory, ~O(N) time.
func buildPlexMatchIndex(items []arr.Item) *plexMatchIndex {
	idx := &plexMatchIndex{
		tmdbImdb: map[plexCompoundKey]*arr.Item{},
		tvdbImdb: map[plexCompoundKey]*arr.Item{},
		tmdb:     map[int]*arr.Item{},
		tvdb:     map[int]*arr.Item{},
		imdb:     map[string]*arr.Item{},
		titleYr:  map[plexTitleYearKey]*arr.Item{},
		pathFull: map[string]*arr.Item{},
		pathBase: map[string]*arr.Item{},
	}
	for i := range items {
		it := &items[i]
		imdb := strings.TrimSpace(it.ImdbID)
		if it.TmdbID != 0 && imdb != "" {
			idx.tmdbImdb[plexCompoundKey{intID: it.TmdbID, textID: imdb}] = it
		}
		if it.TvdbID != 0 && imdb != "" {
			idx.tvdbImdb[plexCompoundKey{intID: it.TvdbID, textID: imdb}] = it
		}
		if it.TmdbID != 0 {
			idx.tmdb[it.TmdbID] = it
		}
		if it.TvdbID != 0 {
			idx.tvdb[it.TvdbID] = it
		}
		if imdb != "" {
			idx.imdb[imdb] = it
		}
		if it.Year != 0 {
			idx.titleYr[plexTitleYearKey{
				title: normalisePlexTitle(it.Title),
				year:  it.Year,
			}] = it
		}
		if p := normalisePlexPath(it.Path); p != "" {
			idx.pathFull[p] = it
			if b := pathBasename(p); b != "" {
				key := strings.ToLower(b)
				if _, seen := idx.pathBase[key]; seen {
					// Two Arr items share this folder name (same
					// title+year in different roots, a remake, etc.).
					// Mark ambiguous (nil) so the basename tier refuses
					// to guess — full-path + ID tiers still resolve them.
					idx.pathBase[key] = nil
				} else {
					idx.pathBase[key] = it
				}
			}
		}
	}
	return idx
}

// matchPlexItemToArrItem walks the 4-tier strategy in priority order.
// Returns the matched item + the tier label, or (nil, "") on miss.
//
// Tier order:
//  1. TMDB+IMDB compound — strongest (two independent IDs agree)
//  2. TVDB+IMDB compound — Sonarr equivalent
//  3. Single ID — TMDB → TVDB → IMDB
//  4. Normalised title + year — last resort
func matchPlexItemToArrItem(item plex.Item, idx *plexMatchIndex) (*arr.Item, string) {
	tmdbID, tvdbID, imdbID := parsePlexGUIDs(item.GUIDs)

	// Tier 1: TMDB+IMDB
	if tmdbID != 0 && imdbID != "" {
		if a, ok := idx.tmdbImdb[plexCompoundKey{intID: tmdbID, textID: imdbID}]; ok {
			return a, "tmdb+imdb"
		}
	}
	// Tier 2: TVDB+IMDB
	if tvdbID != 0 && imdbID != "" {
		if a, ok := idx.tvdbImdb[plexCompoundKey{intID: tvdbID, textID: imdbID}]; ok {
			return a, "tvdb+imdb"
		}
	}
	// Tier 3: single ID — TMDB → TVDB → IMDB
	if tmdbID != 0 {
		if a, ok := idx.tmdb[tmdbID]; ok {
			return a, "tmdb"
		}
	}
	if tvdbID != 0 {
		if a, ok := idx.tvdb[tvdbID]; ok {
			return a, "tvdb"
		}
	}
	if imdbID != "" {
		if a, ok := idx.imdb[imdbID]; ok {
			return a, "imdb"
		}
	}
	// Tier 4: title + year
	if item.Year != 0 {
		if a, ok := idx.titleYr[plexTitleYearKey{
			title: normalisePlexTitle(item.Title),
			year:  item.Year,
		}]; ok {
			return a, "title+year"
		}
	}
	return nil, ""
}

// parsePlexGUIDs extracts TMDB / TVDB / IMDB IDs from a Plex item's
// GUID slice. Plex GUID format examples:
//
//	"tmdb://933260"
//	"tvdb://355567"
//	"imdb://tt17526714"
//	"plex://movie/abc123"     — Plex's own internal GUID (ignored)
//	"local://..."             — items without metadata match (ignored)
//
// Returns zero values for IDs that aren't present. Defensive against
// malformed values (e.g. "tmdb://abc" returns tmdbID=0 rather than
// parse-erroring).
func parsePlexGUIDs(guids []string) (tmdbID int, tvdbID int, imdbID string) {
	for _, g := range guids {
		g = strings.TrimSpace(g)
		if strings.HasPrefix(g, "tmdb://") {
			if v, err := strconv.Atoi(strings.TrimPrefix(g, "tmdb://")); err == nil {
				tmdbID = v
			}
		} else if strings.HasPrefix(g, "tvdb://") {
			if v, err := strconv.Atoi(strings.TrimPrefix(g, "tvdb://")); err == nil {
				tvdbID = v
			}
		} else if strings.HasPrefix(g, "imdb://") {
			imdbID = strings.TrimPrefix(g, "imdb://")
		}
	}
	return
}

// normalisePlexPath trims whitespace + a trailing slash so two reports
// of the same folder compare equal.
func normalisePlexPath(p string) string {
	return strings.TrimRight(strings.TrimSpace(p), "/")
}

// pathBasename returns the last path segment (the media folder name),
// e.g. "/data/media/tv/Show (2016) {tvdb-1}" → "Show (2016) {tvdb-1}".
func pathBasename(p string) string {
	p = normalisePlexPath(p)
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// pathTvdbRE / pathTmdbRE / pathImdbRE pull an ID token out of a media
// folder name. Sonarr/Radarr embed it in the folder ("{tvdb-307837}",
// "{tmdb-114339}", "{imdb-tt0519792}", also "[tvdbid-...]" variants), so
// even when the Plex metadata GUID disagrees with the Arr — different
// TVDB entries for the same show, or a TMDB-only Plex match against a
// TVDB-only Sonarr — the folder still carries the Arr's own ID. The
// separator class allows the "-", "id-", ":" and brace/bracket variants.
var (
	pathTvdbRE = regexp.MustCompile(`(?i)tvdb[^0-9]{0,4}([0-9]+)`)
	pathTmdbRE = regexp.MustCompile(`(?i)tmdb[^0-9]{0,4}([0-9]+)`)
	pathImdbRE = regexp.MustCompile(`(?i)imdb[^0-9a-z]{0,4}(tt[0-9]+)`)
)

// parsePathIDs extracts any tvdb/tmdb/imdb ID token embedded in a media
// folder path. Returns zero values for tokens that aren't present.
func parsePathIDs(p string) (tvdbID int, tmdbID int, imdbID string) {
	if m := pathTvdbRE.FindStringSubmatch(p); m != nil {
		if v, err := strconv.Atoi(m[1]); err == nil {
			tvdbID = v
		}
	}
	if m := pathTmdbRE.FindStringSubmatch(p); m != nil {
		if v, err := strconv.Atoi(m[1]); err == nil {
			tmdbID = v
		}
	}
	if m := pathImdbRE.FindStringSubmatch(p); m != nil {
		imdbID = strings.ToLower(m[1])
	}
	return
}

// matchPlexPathToArrItem is tier 5 — the last-resort match for items
// that missed every ID + title tier. It bridges Plex and the Arr via
// the on-disk location, which is identical because Plex scans the very
// files Sonarr/Radarr placed there. Three sub-steps, most precise first:
//
//	5a. ID token embedded in the folder ("{tvdb-307837}") — an exact
//	    match against the Arr's own ID, authoritative even when the
//	    Plex metadata GUID disagrees.
//	5b. Exact folder path (Plex Location == Arr path).
//	5c. Folder basename — mount-agnostic, so it still matches when Plex
//	    and the Arr mount the same storage at different roots.
//
// Returns the matched item + a tier label, or (nil, "") on miss.
func matchPlexPathToArrItem(plexPath string, idx *plexMatchIndex) (*arr.Item, string) {
	p := normalisePlexPath(plexPath)
	if p == "" {
		return nil, ""
	}
	tvdbID, tmdbID, imdbID := parsePathIDs(p)
	if tvdbID != 0 {
		if a, ok := idx.tvdb[tvdbID]; ok {
			return a, "path-tvdb"
		}
	}
	if tmdbID != 0 {
		if a, ok := idx.tmdb[tmdbID]; ok {
			return a, "path-tmdb"
		}
	}
	if imdbID != "" {
		if a, ok := idx.imdb[imdbID]; ok {
			return a, "path-imdb"
		}
	}
	if a, ok := idx.pathFull[p]; ok {
		return a, "path-full"
	}
	if b := pathBasename(p); b != "" {
		// nil value = ambiguous (more than one Arr item has this folder
		// name); skip rather than guess. Only a unique basename matches.
		if a, ok := idx.pathBase[strings.ToLower(b)]; ok && a != nil {
			return a, "path-base"
		}
	}
	return nil, ""
}

// titleNormaliser strips punctuation + collapses whitespace so Plex's
// "Star Wars: Episode IV - A New Hope" matches Arr's variant of the
// same title. Case-insensitive (lower-casing applied separately).
var titleNormaliserPunctuation = regexp.MustCompile(`[^a-z0-9\s]+`)
var titleNormaliserWhitespace = regexp.MustCompile(`\s+`)

func normalisePlexTitle(s string) string {
	lower := strings.ToLower(strings.TrimSpace(s))
	lower = titleNormaliserPunctuation.ReplaceAllString(lower, " ")
	lower = titleNormaliserWhitespace.ReplaceAllString(lower, " ")
	return strings.TrimSpace(lower)
}

// summarisePlexLabelRun builds the one-line activity-row description.
// Format:
//
//	"Matched 142 of 150 items; +12 labels, -3 labels"
//	"Preview: would add 12 labels, remove 3 labels"
//	"No changes (every item already in sync)"
//
// missingFromArr surfaces labels in the rule's whitelist that aren't
// configured as tags in Arr — they're a no-op for the engine but
// worth flagging in the activity row so the user notices.
func summarisePlexLabelRun(run core.PlexLabelRuleRun, missingFromArr []string) string {
	var b strings.Builder
	if run.RunMode == "preview" {
		b.WriteString("Preview: ")
	}
	totalAdds := 0
	for _, v := range run.Added {
		totalAdds += v
	}
	totalRemoves := 0
	for _, v := range run.Removed {
		totalRemoves += v
	}
	if totalAdds == 0 && totalRemoves == 0 {
		b.WriteString(fmt.Sprintf("No changes (every item already in sync, %d items scanned)", run.ItemsTotal))
	} else {
		verbAdd, verbRemove := "added", "removed"
		if run.RunMode == "preview" {
			verbAdd, verbRemove = "would add", "would remove"
		}
		parts := []string{}
		if totalAdds > 0 {
			parts = append(parts, fmt.Sprintf("%s %d %s", verbAdd, totalAdds, pluralise("label", totalAdds)))
		}
		if totalRemoves > 0 {
			parts = append(parts, fmt.Sprintf("%s %d %s", verbRemove, totalRemoves, pluralise("label", totalRemoves)))
		}
		b.WriteString(fmt.Sprintf("Matched %d of %d items; %s",
			run.Matched, run.ItemsTotal, strings.Join(parts, ", ")))
	}
	if len(missingFromArr) > 0 {
		b.WriteString(fmt.Sprintf(" — %d whitelist %s not in Arr (%s)",
			len(missingFromArr),
			pluralise("label", len(missingFromArr)),
			strings.Join(missingFromArr, ", ")))
	}
	return b.String()
}

func pluralise(s string, n int) string {
	if n == 1 {
		return s
	}
	return s + "s"
}

