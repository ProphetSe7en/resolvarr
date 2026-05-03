package api

import (
	"sort"

	"resolvarr/internal/arr"
	"resolvarr/internal/core"
	"resolvarr/internal/core/engine"
)

// scan_helpers.go — small shared helpers used by multiple scan-action
// handlers. Pure functions — no I/O, no global state — so each callsite
// can be reasoned about locally.
//
// The engine-delegation contract on the scan handlers (every
// per-(movie, group) decision goes through engine.DecideTag) means these
// helpers do not get to encode tag logic. They're just data-shape utilities.

// composeAction names the (shouldHave, hasTag) pair. This is the full
// extent of tag-related computation in the handler — no matching, no
// filter evaluation, no should-have derivation. engine.Decision.ShouldHave
// is the source of truth; hasTag is a comparison against Arr's own state.
//
// Mirrors the comment on engine.Decision.ShouldHave verbatim.
func composeAction(shouldHave, hasTag bool) string {
	switch {
	case shouldHave && !hasTag:
		return "add"
	case shouldHave && hasTag:
		return "keep"
	case !shouldHave && hasTag:
		return "remove"
	default:
		return "skip"
	}
}

// setToSlice flattens a set of ints into a slice. Apply-phase
// accumulators use set semantics to dedupe item IDs across groups that
// happen to share a Tag label; Arr's editor endpoint takes a slice.
func setToSlice(m map[int]struct{}) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// managedLabelsForType returns the deduplicated list of managed tag labels
// for the given Arr type. Iterates cfg.ReleaseGroups regardless of Enabled
// state — a disabled group is the user's "pause but keep config" intent;
// its tag in Radarr might still exist from a previous run and is fair game
// for cleanup if it now has 0 movies (matches bash, which iterates
// RELEASE_GROUPS without checking enable state in the cleanup loop).
//
// The empty Tag string is filtered out — those entries are unusable for
// cleanup (no label to look up against Radarr's tag inventory).
func managedLabelsForType(cfg core.Config, appType string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, g := range cfg.ReleaseGroups {
		if g.Type != appType {
			continue
		}
		if g.Tag == "" {
			continue
		}
		if seen[g.Tag] {
			continue
		}
		seen[g.Tag] = true
		out = append(out, g.Tag)
	}
	return out
}

// computeCleanupCandidates lists managed tag labels whose final usage count
// would be 0 — i.e., the cleanup-eligible set.
//
// items     — current Arr library state for this instance
// labelToID — Arr's current label→tagID map (built from ListTagDetails)
// managed   — labels from cfg.ReleaseGroups (this is the safety bound; see
//             handleScanCleanup safety comment for why)
// addByTag, removeByTag — optional pending deltas from a tag-mode apply
//             pass that just ran. Pass nil for standalone cleanup.
//
// Logic: for each managed label, count items currently carrying its tagID,
// then add/subtract the pending deltas. Labels with final count <= 0 land
// in the candidate list.
//
// SAFETY: the managed argument is the ONLY source of label inputs. A label
// not in managed will never appear in the output, regardless of what's in
// labelToID or the delta maps. This matches bash's "for cfg in
// RELEASE_GROUPS" iteration bound.
func computeCleanupCandidates(items []arr.Item, labelToID map[string]int, managed []string, addByTag, removeByTag map[string]map[int]struct{}) []scanCleanupCandidate {
	out := make([]scanCleanupCandidate, 0, len(managed))
	for _, label := range managed {
		tagID, exists := labelToID[label]
		if !exists {
			// Tag never created in Arr (no movies ever matched). Nothing to
			// delete. Bash equivalent: primary_tag_ids[$tag_name] is empty.
			continue
		}
		count := 0
		for _, it := range items {
			for _, tid := range it.Tags {
				if tid == tagID {
					count++
					break
				}
			}
		}
		if addByTag != nil {
			count += len(addByTag[label])
		}
		if removeByTag != nil {
			count -= len(removeByTag[label])
		}
		if count <= 0 {
			out = append(out, scanCleanupCandidate{
				Label: label,
				TagID: tagID,
				Count: 0,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

// newestImportEvent walks history (already-sorted-or-not) for the newest
// downloadFolderImported / movieFileImported / episodeFileImported event.
// Returns a pointer so callers can distinguish "no import" (nil) from
// "import but empty fields". This is duplicate-effort with engine
// (FindImportedGrabGroup also locates the newest import internally) but
// the engine returns only the recovered group + status, not the import
// event itself. Worth the small cost of a second walk to keep the engine
// API focused.
func newestImportEvent(history []engine.HistoryRecord) *engine.HistoryRecord {
	var best *engine.HistoryRecord
	for i := range history {
		ev := &history[i]
		switch ev.EventType {
		case engine.HistoryEventDownloadFolderImported,
			engine.HistoryEventMovieFileImported,
			engine.HistoryEventEpisodeFileImported:
			if best == nil || ev.Date.After(best.Date) {
				best = ev
			}
		}
	}
	return best
}
