package engine

// qbit_se.go — pure helpers for qBit Season/Episode tagging.
//
// Three-rule first-match-wins model — direct port of the community
// Python reference qbittorrent_auto_tagger.py. Each torrent name is
// run against Episode → Season → Unmatched in order; the first
// matching enabled rule contributes ONE tag.
//
// Hardcoded patterns (battle-tested defaults from the Python script):
//
//	Episode:   (?i)S\d{1,3}E\d{1,3}                — S01E05 / S01E05E06 multi-ep
//	           OR  \b\d{4}\D+\d{2}\D+\d{2}\b       — 2024.10.15 daily-show date
//	Season:    (?i)(?:S\d{1,3}|Season[\s\.]\d{1,3}) AND NOT episode-token
//	Unmatched: catch-all when neither matched
//
// Why fully hardcoded instead of user-configurable: the patterns have
// soaked for years in the Python community (battle-tested across
// thousands of users). Letting users edit them is a foot-gun without
// upside; exposing only the per-rule Enabled toggle + tag name gives
// a clean migration path off the Python script with zero ambiguity
// about behaviour. If a user needs different patterns they can run
// both the script and resolvarr until we have a stronger reason to
// ship a custom-pattern UI.

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// QbitSeRulesView is the engine-facing read-only view of the rule's
// per-tag toggles + custom tag names. Mirrors core.QbitSeRules to
// keep the engine independent of the persistent shape.
type QbitSeRulesView struct {
	EpisodeEnabled   bool
	EpisodeTag       string
	SeasonEnabled    bool
	SeasonTag        string
	UnmatchedEnabled bool
	UnmatchedTag     string
}

// qbitEpisodePatterns is the ordered list of regexes considered an
// "episode-token match" — any one matching makes the torrent name
// episode-classified for the first-match-wins ordering. Two patterns:
//   - Standard scene S01E05 / S01E05E06 multi-ep
//   - Daily-show ISO-ish date pattern (e.g. "Show.2024.10.15")
//
// The daily-show pattern matches "<4 digits><sep><2 digits><sep><2
// digits>" where <sep> is a date separator (. _ - or space): catches
// Show.2024.10.15, Show.2024-10-15, Show 2024 10 15, Show.2024_10_15.
// The separator is restricted to those chars rather than "any non-
// digit", so a year followed by season tokens (e.g. "...2025.s01.PL.
// s01...") does NOT false-match as a date. Before the restriction the
// year + the two-digit season numbers around the language tag matched,
// and the season pack was mis-tagged Episode instead of Season. The
// remaining worst case is a genuine 4+2+2 date-shaped string that is
// not actually a release date, which just tags Episode (recoverable).
var qbitEpisodePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)S\d{1,3}E\d{1,3}`),
	regexp.MustCompile(`\b\d{4}[._ -]\d{2}[._ -]\d{2}\b`),
}

// qbitSeasonPattern matches season-pack tokens: bare S01 / S12 / etc.
// or worded "Season 1" / "Season.1". Combined with !epMatched in the
// caller so a torrent with S01E05 doesn't also count as a season match.
var qbitSeasonPattern = regexp.MustCompile(`(?i)(?:S\d{1,3}|Season[\s\.]\d{1,3})`)

// DetermineQbitTag returns the qBit tag to apply for a torrent name,
// or "" when no rule matches (or all matching rules are disabled).
// First-match-wins: Episode → Season → Unmatched.
//
// Each rule honours its Enabled toggle. Empty Tag string falls back
// to the default ("Episode" / "Season" / "Unmatched") so legacy or
// cleared configs still produce sensible output.
//
// Note on disabled-rule behaviour: disabling Episode does NOT push
// an episode-named torrent into Season or Unmatched. The first-match-
// wins ordering looks at what the NAME matches first, then checks if
// that rule is enabled. A name with S01E05 always classifies as
// "episode" — disabling the Episode rule means that torrent gets no
// tag rather than falling through to Season/Unmatched. Mirrors the
// Python reference's behaviour exactly.
func DetermineQbitTag(torrentName string, r QbitSeRulesView) string {
	if torrentName == "" {
		return ""
	}
	epMatched := false
	for _, p := range qbitEpisodePatterns {
		if p.MatchString(torrentName) {
			epMatched = true
			break
		}
	}
	if epMatched {
		if r.EpisodeEnabled {
			return defaultStr(r.EpisodeTag, "Episode")
		}
		// Episode-classified but rule disabled — no fall-through.
		return ""
	}
	seasonMatched := qbitSeasonPattern.MatchString(torrentName)
	if seasonMatched {
		if r.SeasonEnabled {
			return defaultStr(r.SeasonTag, "Season")
		}
		// Season-classified but rule disabled — no fall-through.
		return ""
	}
	if r.UnmatchedEnabled {
		return defaultStr(r.UnmatchedTag, "Unmatched")
	}
	return ""
}

// defaultStr returns def when s is empty / whitespace-only.
func defaultStr(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

// seasonEpisodeRE captures S01E05 / S01E05E06 / s01.e05.e06 etc. — the
// dominant scene-naming convention for episodic releases. Used by the
// backlog-fix preview to derive season/episodes from existing qBit
// torrent names without requiring Sonarr API lookups.
//
// Group structure:
//
//	1 = season number ("01", "1", etc. — caller atoi)
//	2 = trailing episode segment ("E05" / "E05E06" / "E05.E06" / etc.)
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
//
//	"Show.S01E05.WEB-DL-FLUX"      → (1, [5], true)
//	"Show.S01E05E06.WEB-DL-FLUX"   → (1, [5, 6], true)   // multi-ep
//	"Show.S01E05.E06.FLUX"         → (1, [5, 6], true)
//	"Show.S01.WEB-DL.FLUX"         → (1, nil, true)      // season pack
//	"Show.Season.1.Complete.FLUX"  → (1, nil, true)      // season pack
//	"Show.2024.WEB-DL"             → (0, nil, false)     // no S/E token
//
// Used by the backlog-fix scan to surface "what would this torrent
// match" data in the preview UI. Apply path uses DetermineQbitTag for
// the actual tag decision; this parser only provides the displayed
// "parsed season X / episodes [Y...]" hint.
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
