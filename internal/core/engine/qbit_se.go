package engine

// qbit_se.go — pure helpers for qBit Season/Episode tagging.
// Container-only feature; no bash baseline.
//
// Tag-format conventions (hardcoded for v1):
//
//   Season-pack:           S01            (single tag for the season)
//   Single episode:        S01E05
//   Multi-episode (split): S01E05E06     (one tag covering both)
//
// Detection rules:
//
//   - Empty episodeNumbers → assumed season-pack regardless of toggle
//     state (Sonarr emits this on whole-season grabs where the indexer
//     listing wraps a season-archive without per-episode IDs).
//   - len(episodeNumbers) >= totalEpsInSeason (when known) → season-pack
//     even with explicit episode IDs (Sonarr can emit all 24 IDs for a
//     full-season grab).
//   - Otherwise → episode tag in S01E05 / S01E05E06 form.
//
// Total-eps-in-season is optional. When 0 (caller doesn't know), the
// "all episodes covered" heuristic falls back to len(episodeNumbers)>=10
// as a "probably a season pack" trigger. 10 is a generous threshold:
// most modern Sonarr libraries have 8-13 episodes per season; multi-
// episode grabs (S01E05E06) are 2-3 episodes. The threshold keeps
// "S01E05E06" off the season-only path while letting genuine season
// packs (12-13 episodes emitted in one grab) collapse to S01.

import (
	"fmt"
	"regexp"
	"sort"
)

// QbitSeRulesView is the engine-facing read-only view of the rule's
// per-tag toggles. Mirrors core.QbitSeRules to keep the engine
// independent of the persistent shape.
type QbitSeRulesView struct {
	TagSeason  bool
	TagEpisode bool
}

// QbitSeasonEpisodeTags returns the qBit tag labels to apply for a
// Sonarr Grab event. Pure function; no I/O.
//
// Inputs:
//   - seasonNum: Sonarr's release.seasonNumber (>=1 for valid grabs).
//   - episodeNums: release.episodeNumbers — may be empty (season pack),
//     single-element (one episode), multi-element (multi-ep release or
//     season pack). De-duplicated + sorted ascending in the output tag.
//   - totalEpsInSeason: optional hint from arr.Series.Statistics. 0
//     when caller doesn't know; helper falls back to the "10+ episodes
//     = season pack" heuristic.
//   - cfg: per-rule toggles.
//
// Empty result when:
//   - seasonNum < 1 (invalid input)
//   - both TagSeason + TagEpisode are false
//
// Order: season tag first when emitted, then episode tag. Adapter
// adds them as a single qBit AddTags call (qBit accepts a comma-list).
func QbitSeasonEpisodeTags(seasonNum int, episodeNums []int, totalEpsInSeason int, cfg QbitSeRulesView) []string {
	if seasonNum < 1 {
		return nil
	}
	if !cfg.TagSeason && !cfg.TagEpisode {
		return nil
	}

	// Normalise episode list: dedup + sort ascending.
	epsNorm := normalizeEpisodeList(episodeNums)

	// Detect season-pack:
	//   1. Explicit empty episode list (Sonarr's whole-season-archive form)
	//   2. Episode count covers the full season (>= totalEpsInSeason if known)
	//   3. Fallback heuristic: ≥10 episode IDs = "probably season pack"
	isSeasonPack := len(epsNorm) == 0
	if !isSeasonPack && totalEpsInSeason > 0 && len(epsNorm) >= totalEpsInSeason {
		isSeasonPack = true
	}
	if !isSeasonPack && totalEpsInSeason == 0 && len(epsNorm) >= 10 {
		isSeasonPack = true
	}

	var out []string
	if cfg.TagSeason {
		out = append(out, formatSeasonTag(seasonNum))
	}
	if cfg.TagEpisode && !isSeasonPack {
		// Episode tag — single (S01E05) or multi (S01E05E06).
		out = append(out, formatEpisodeTag(seasonNum, epsNorm))
	}
	return out
}

// formatSeasonTag returns the season-only tag, e.g. "S01" / "S12".
func formatSeasonTag(season int) string {
	return fmt.Sprintf("S%02d", season)
}

// formatEpisodeTag returns the episode tag, e.g. "S01E05" or "S01E05E06"
// or "S01E05E06E07". Multi-episode form concatenates each episode's
// "E<NN>" suffix in ascending order.
//
// Empty episode list returns the season-only tag — defensive; callers
// that want season-pack semantics handle the empty case separately
// before reaching here.
func formatEpisodeTag(season int, epsNorm []int) string {
	if len(epsNorm) == 0 {
		return formatSeasonTag(season)
	}
	out := formatSeasonTag(season)
	for _, e := range epsNorm {
		out += fmt.Sprintf("E%02d", e)
	}
	return out
}

// seasonEpisodeRE captures S01E05 / S01E05E06 / s01.e05.e06 etc. — the
// dominant scene-naming convention for episodic releases. Used by the
// backlog-fix preview to derive season/episodes from existing qBit
// torrent names without requiring Sonarr API lookups.
//
// Group structure:
//   1 = season number ("01", "1", etc. — caller atoi)
//   2 = trailing episode segment ("E05" / "E05E06" / "E05.E06" / etc.)
var seasonEpisodeRE = regexp.MustCompile(`(?i)\bS(\d{1,3})((?:[._ ]?E\d{1,3})*)\b`)

// seasonOnlyRE matches season-pack patterns: "S01" with no trailing
// E0Y, or the words "Season 1" / "Season.1". Backlog-fix detects
// season packs from these.
var seasonOnlyRE = regexp.MustCompile(`(?i)\b(?:S|Season[._ ])(\d{1,3})\b`)

// episodeNumRE extracts every "E0X" inside a captured episode segment.
var episodeNumRE = regexp.MustCompile(`(?i)E(\d{1,3})`)

// ParseSeasonEpisodeFromTitle attempts to extract (seasonNum, episodes[])
// from a torrent name / release title. Returns ok=false when the title
// doesn't carry a recognisable S/E token.
//
// Patterns recognised:
//   "Show.S01E05.WEB-DL-FLUX"      → (1, [5], true)
//   "Show.S01E05E06.WEB-DL-FLUX"   → (1, [5, 6], true)   // multi-ep
//   "Show.S01E05.E06.FLUX"         → (1, [5, 6], true)
//   "Show.S01.WEB-DL.FLUX"         → (1, nil, true)      // season pack
//   "Show.Season.1.Complete.FLUX"  → (1, nil, true)      // season pack
//   "Show.2024.WEB-DL"             → (0, nil, false)     // no S/E token
//
// Used by the backlog-fix scan to compute proposed tags from existing
// qBit torrents WITHOUT requiring a Sonarr API lookup. False-positive
// risk is low because the regex requires the literal "S" prefix at a
// word boundary.
func ParseSeasonEpisodeFromTitle(title string) (seasonNum int, episodes []int, ok bool) {
	if title == "" {
		return 0, nil, false
	}
	// Try S01E05(...) pattern first — most specific.
	if m := seasonEpisodeRE.FindStringSubmatch(title); m != nil {
		s, err := atoiSafe(m[1])
		if err != nil || s < 1 {
			return 0, nil, false
		}
		// m[2] is the trailing E0X(E0Y)* segment, possibly empty if
		// regex matched a bare "S01" (which we want to treat as
		// season-pack via the seasonOnlyRE branch below; let it fall
		// through if the episode segment is empty).
		if m[2] != "" {
			eps := []int{}
			for _, em := range episodeNumRE.FindAllStringSubmatch(m[2], -1) {
				e, err := atoiSafe(em[1])
				if err != nil || e < 1 {
					continue
				}
				eps = append(eps, e)
			}
			eps = normalizeEpisodeList(eps)
			if len(eps) > 0 {
				return s, eps, true
			}
		}
	}
	// Season-pack fallback: "S01" alone or "Season 1".
	if m := seasonOnlyRE.FindStringSubmatch(title); m != nil {
		s, err := atoiSafe(m[1])
		if err != nil || s < 1 {
			return 0, nil, false
		}
		return s, nil, true
	}
	return 0, nil, false
}

// atoiSafe is a tiny wrapper for the regex-numeric extracts.
func atoiSafe(s string) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("non-digit %q", c)
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

// normalizeEpisodeList dedups and sorts ascending. Skips zero/negative
// episode numbers (Sonarr typically doesn't emit those, but defensive).
func normalizeEpisodeList(eps []int) []int {
	if len(eps) == 0 {
		return nil
	}
	seen := map[int]bool{}
	out := make([]int, 0, len(eps))
	for _, e := range eps {
		if e < 1 || seen[e] {
			continue
		}
		seen[e] = true
		out = append(out, e)
	}
	sort.Ints(out)
	return out
}
