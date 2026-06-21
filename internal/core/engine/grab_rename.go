package engine

// grab_rename.go — built-in token vocabularies + diff helpers for the
// M-Webhook Grab Rename adapter. Pure functions; no I/O.
//
// The adapter detects token mismatches between the indexer-supplied
// release-title (Connect Grab event's release.releaseTitle) and the
// current qBit torrent display name. When ≥1 enabled trigger has a
// diff (or TriggerAlways=true), the adapter fires a rename via qBit's
// /api/v2/torrents/rename endpoint with a parser-friendly target.
//
// Token vocabularies are HARDCODED with explicit `// TRaSH source: ...`
// comments. Future iteration may pull live from TRaSH-Guides; for v1
// hardcoding is faster + perfectly forward-compatible (TRaSH lists
// are stable).

import (
	"regexp"
	"strconv"
	"strings"
)

// grabRenameMovieVersionTokens: TRaSH's "Optional Movie Versions" CF
// group (cf-groups/optional-movie-versions.json, group ID
// f4f1474b963b24cf983455743aa9906c). One token per group CF, each with a
// RE2-safe regex (TRaSH's lookbehind/lookahead specs can't port to Go
// verbatim, see the IMAX Exclude note). Mirror any change into bash
// tagarr_import.sh's GRAB_RENAME_MOVIE_VERSION block.
//
// Synced to the full 11-CF group: added IMAX Enhanced, 4K Remaster,
// Special Edition, Uncensored. Special Edition's
// full TRaSH catch-all (generic cut/version/edition) is intentionally NOT
// mirrored: those need TRaSH's negative lookbehind to avoid false hits,
// which RE2 lacks; the literal "Special Edition" + "Uncensored" cover the
// distinct cases, and Director's Cut / Extended / Unrated / Uncut are
// already separate tokens.
var grabRenameMovieVersionTokens = []namedTokenRegex{
	{Label: "Director's Cut", Pattern: regexp.MustCompile(`(?i)\bdirector('?s)?[._ -]?cut\b`)},
	{Label: "Theatrical", Pattern: regexp.MustCompile(`(?i)\btheatrical\b`)},
	{Label: "Extended", Pattern: regexp.MustCompile(`(?i)\bextended\b`)},
	{Label: "Unrated", Pattern: regexp.MustCompile(`(?i)\bunrated\b`)},
	{Label: "Uncut", Pattern: regexp.MustCompile(`(?i)\buncut\b`)},
	{Label: "Remaster", Pattern: regexp.MustCompile(`(?i)\bremaster(ed)?\b`)},
	// 4K Remaster, distinct from generic Remaster so losing the "4K" (a
	// source-master label, not resolution) from "4K Remaster" still fires.
	{Label: "4K Remaster", Pattern: regexp.MustCompile(`(?i)\b4k[._ -]?remaster(ed)?\b`)},
	{Label: "Criterion", Pattern: regexp.MustCompile(`(?i)\bcriterion\b`)},
	{Label: "Masters of Cinema", Pattern: regexp.MustCompile(`(?i)\b(masters[._ -]?of[._ -]?cinema|moc)\b`)},
	{Label: "Vinegar Syndrome", Pattern: regexp.MustCompile(`(?i)\bvinegar[._ -]?syndrome\b`)},
	{Label: "Hybrid", Pattern: regexp.MustCompile(`(?i)\bhybrid\b`)},
	{
		Label:   "IMAX",
		Pattern: regexp.MustCompile(`(?i)\bimax\b`),
		// "NON-IMAX" titles intentionally flag themselves as not IMAX —
		// Radarr's NON-IMAX CF (TRaSH `\b((?<!NON[ ._-])IMAX)\b`) excludes
		// these. We can't use lookbehind in Go RE2, so the Exclude branch
		// drops matches when "non[._ -]+imax" appears in the input.
		Exclude: regexp.MustCompile(`(?i)\bnon[._ -]+imax\b`),
	},
	// IMAX Enhanced, distinct from plain IMAX so "IMAX Enhanced → IMAX"
	// (the "Enhanced" stripped) fires. Plain IMAX matches both, so it alone
	// can't detect the loss.
	{Label: "IMAX Enhanced", Pattern: regexp.MustCompile(`(?i)\bimax[._ -]?enhanced\b`)},
	{Label: "Open Matte", Pattern: regexp.MustCompile(`(?i)\bopen[ ._-]?matte\b`)},
	{Label: "Special Edition", Pattern: regexp.MustCompile(`(?i)\bspecial[._ -]?edition\b`)},
	{Label: "Uncensored", Pattern: regexp.MustCompile(`(?i)\buncensored\b`)},
}

// grabRenameSourceTokens — streaming-source flag tokens that influence
// CF scoring on release-title. These typically don't end up in the
// stripped torrent name, so renaming preserves the score.
//
// Container-policy expansion vs bash: bash tagarr_import.sh:319-322
// only checks MA WEB-DL / Play WEB-DL / plain WEB-DL. Container adds
// streaming-services (iT/AMZN/NF/DSNP/HMAX/HULU/PCOK/CR/ATVP) that
// Sonarr-bash users typically configure via GRAB_RENAME_CUSTOM_TOKENS
// — built-in is more user-friendly + works for both Arrs.
//
// "WEB-DL" is included as a final fallback (mirrors bash line 322).
// Order matters for diff-output readability: more-specific MA WEB-DL
// / Play WEB-DL fire first; bare "WEB-DL" only adds to the diff when
// the more-specific patterns don't match (DiffMissingTokens iterates
// in slice order; both can appear in output if both differ between
// current + grab — duplicate-rename-trigger is harmless because
// rename happens once regardless of how many tokens triggered).
//
// TRaSH source: per-service Streaming Service CFs under
// docs/json/radarr/cf/streaming-services/.
var grabRenameSourceTokens = []namedTokenRegex{
	{Label: "MA WEB-DL", Pattern: regexp.MustCompile(`(?i)\bma(\]?\s*\[?|[._-])web([-.]?dl)?`)},
	{Label: "Play WEB-DL", Pattern: regexp.MustCompile(`(?i)\bplay(\]?\s*\[?|[._-])web([-.]?dl)?`)},
	// iTunes — spelled "iT" or "iTunes" in release names, ALWAYS
	// directly before WEB-DL (verified against real .torrents/movies:
	// "2160p.iT.WEB-DL", "iTunes.WEB-DL", "1080p iT WEB-DL"). Anchoring
	// to the WEB token is what makes this safe: a bare "\bit\b" would
	// false-match the English word "it" / titles like "It Follows", but
	// "iT" only counts as the iTunes source when WEB-DL follows it.
	{Label: "iT", Pattern: regexp.MustCompile(`(?i)\b(it|itunes)[._ ]web([-._]?dl)?`)},
	{Label: "AMZN", Pattern: regexp.MustCompile(`(?i)\bamzn\b`)},
	{Label: "NF", Pattern: regexp.MustCompile(`(?i)\bnf\b`)},
	{Label: "DSNP", Pattern: regexp.MustCompile(`(?i)\bdsnp\b`)},
	{Label: "HMAX", Pattern: regexp.MustCompile(`(?i)\bhmax\b`)},
	{Label: "HULU", Pattern: regexp.MustCompile(`(?i)\bhulu\b`)},
	{Label: "PCOK", Pattern: regexp.MustCompile(`(?i)\bpcok\b`)},
	{Label: "CR", Pattern: regexp.MustCompile(`(?i)\bcr\b`)},
	{Label: "ATVP", Pattern: regexp.MustCompile(`(?i)\batvp\b`)},
	{Label: "WEB-DL", Pattern: regexp.MustCompile(`(?i)\bweb[-.]?dl\b`)},
}

// grabRenameAudioTokens — release-title-only audio flags. Most CFs
// score on MediaInfo (Radarr/Sonarr extracts from file directly), so
// renaming for audio is "usually cosmetic" — but useful when CFs
// score on the release title (Audio Advanced 1 setups).
//
// TRaSH source: docs/json/radarr/cf/audio-formats.json + Audio
// Advanced 1 / 2 CF groups. Subset chosen to cover the title-relevant
// tokens (Atmos / TrueHD etc.); HD-Audio fields like channel counts
// are MediaInfo-derived and not relevant to release-title scoring.
var grabRenameAudioTokens = []namedTokenRegex{
	{Label: "TrueHD Atmos", Pattern: regexp.MustCompile(`(?i)\btruehd\b.*\batmos\b|\batmos\b.*\btruehd\b`)},
	{Label: "Atmos", Pattern: regexp.MustCompile(`(?i)\batmos\b`)},
	{Label: "TrueHD", Pattern: regexp.MustCompile(`(?i)\btruehd\b`)},
	{Label: "DTS-X", Pattern: regexp.MustCompile(`(?i)\bdts[._-]?x\b`)},
	{Label: "DTS-HD MA", Pattern: regexp.MustCompile(`(?i)\bdts[._ -]?hd[._ -]?ma\b`)},
	{Label: "DTS-ES", Pattern: regexp.MustCompile(`(?i)\bdts[._ -]?es\b`)},
	{Label: "EAC3 Atmos", Pattern: regexp.MustCompile(`(?i)\beac3.*\batmos\b|\batmos.*\beac3\b`)},
}

// grabRenameHdrTokens: dynamic-range title flags. Unlike the post-import
// CF score (which Radarr derives from the file's MediaInfo), these matter
// during the DOWNLOAD WINDOW: tools like autobrr push the release name
// while the file is still downloading, an RSS sync then sees the (maybe
// stripped) torrent name, and Arr compares the two NAMES, with no
// MediaInfo yet. A "HDR10+" turning into "HDR" makes those names
// disagree, so preserving the granular token keeps it consistent.
//
// RE2-safe: TRaSH's HDR CF distinguishes HDR10 vs HDR10+ with a lookahead
// Go can't run, but here we only need to detect the LOSS of the granular
// token, which the literal "+/Plus" match does without lookahead.
var grabRenameHdrTokens = []namedTokenRegex{
	// Trailing \b only on the "plus" branch: "+" is a non-word char, so a
	// \b after it fails (non-word followed by space is not a boundary).
	{Label: "HDR10+", Pattern: regexp.MustCompile(`(?i)\bhdr10(\+|plus\b)`)},
	{Label: "Dolby Vision", Pattern: regexp.MustCompile(`(?i)\b(dv|dovi|dolby[ .]?v(ision)?)\b`)},
	{Label: "HLG", Pattern: regexp.MustCompile(`(?i)\bhlg\b`)},
}

// grabRenameLanguageTokens: French audio-version release-NAME tags (plus
// MULTi). Like HDR, these matter during the download window (autobrr
// pushes the full release name, an RSS sync sees the stripped torrent
// name, and the Arr compares the two NAMES before MediaInfo exists). A
// French tracker release pushed as "MULTi.VFQ" arriving in qBit as
// "MULTi" (VFQ stripped) loses the score until the name is restored.
//
// Scope is deliberately narrow: only TRaSH language CFs that score on the
// release-NAME belong here, which is exactly the French audio-version set
// (MULTi + the VF*/VO* variants + VOSTFR). German is intentionally NOT
// here: TRaSH's german.json / german-dl.json score on the Arr's Language
// field (LanguageSpecification, from MediaInfo), which a torrent rename
// can't influence. TRaSH ships no Italian / Spanish / Dutch / Nordic
// Radarr CFs at all. A user who needs something else can add a Custom
// token. Sources: docs/json/radarr/cf/multi.json + french-vf*/vof/voq/
// vostfr.json.
//
// RE2-safe: TRaSH's regexes use lookbehind/lookahead (e.g.
// "(?<=MULTi[ .])FR", "Multi(?![ ._-]?sub)") which Go can't run; here we
// only need to detect the LOSS of a literal token, so word-bounded
// literals (plus the Exclude field for the Multi-subs case) suffice. The
// bare 2-letter "VQ" CF is omitted: \bvq\b false-matches inside titles.
var grabRenameLanguageTokens = []namedTokenRegex{
	// MULTi, but not "Multi-subs" (subs-only, a different CF). Exclude
	// drops the match when the subs form is present.
	{Label: "MULTi", Pattern: regexp.MustCompile(`(?i)\bmulti\b`), Exclude: regexp.MustCompile(`(?i)\bmulti[ ._-]?subs?\b`)},
	{Label: "TrueFrench", Pattern: regexp.MustCompile(`(?i)\btrue[ ._-]?french\b`)},
	{Label: "VOSTFR", Pattern: regexp.MustCompile(`(?i)\bvostfr\b`)},
	{Label: "VFF", Pattern: regexp.MustCompile(`(?i)\bvff\b`)},
	{Label: "VFQ", Pattern: regexp.MustCompile(`(?i)\bvfq\b`)},
	{Label: "VF2", Pattern: regexp.MustCompile(`(?i)\bvf2\b`)},
	{Label: "VFB", Pattern: regexp.MustCompile(`(?i)\bvfb\b`)},
	{Label: "VFI", Pattern: regexp.MustCompile(`(?i)\bvfi\b`)},
	{Label: "VOF", Pattern: regexp.MustCompile(`(?i)\bvof\b`)},
	{Label: "VOQ", Pattern: regexp.MustCompile(`(?i)\bvoq\b`)},
}

// grabRenameSceneCFGroups is the lowercased name-set of release-groups
// the TRaSH Scene CF identifies as legitimate scene releases. When a
// rule's TriggerOnSceneMismatch fires, this set is consulted: if rg
// IS in the set → leave it (preserve scene CF scoring); if rg is NOT
// in the set → consider the torrent name "fake-scene" (P2P/bootleg
// with stripped tokens) and trigger rename to the indexer release-
// title.
//
// TRaSH source: docs/json/radarr/cf/scene.json group list.
var grabRenameSceneCFGroups = map[string]bool{
	"cakes":          true,
	"ggez":           true,
	"ggwp":           true,
	"glhf":           true,
	"gossip":         true,
	"naisu":          true,
	"kogi":           true,
	"peculate":       true,
	"slot":           true,
	"edith":          true,
	"ethel":          true,
	"eleanor":        true,
	"b2b":            true,
	"spamneggs":      true,
	"ftp":            true,
	"dirt":           true,
	"syncopy":        true,
	"bae":            true,
	"successfulcrab": true,
	"nhtfs":          true,
	"surcode":        true,
	"b0mbardiers":    true,
	"d3us":           true,
	"brotherhood":    true,
	"w4k":            true,
	"strikes":        true,
}

// sceneResolutionRE detects the resolution token (required prefix for
// scene-pattern recognition).
var sceneResolutionRE = regexp.MustCompile(`(?i)\b[0-9]{3,4}p\b`)

// sceneWebTokenRE matches the bare "WEB" token at a word boundary —
// includes "WEB-FLUX" (the WEB before the hyphen-RG separator counts
// because `\b` matches between letter and `-`). Container divergence
// from bash's stricter `(^|[_. ])WEB([_. ]|$)` which required `_`/`.`/
// space delimiters and missed the common "WEB-RG" stripped form.
var sceneWebTokenRE = regexp.MustCompile(`(?i)\bWEB\b`)

// sceneWebDLRE matches WEB-DL / WEB.DL / WEBDL — the real-source form
// that should NOT be classified as scene-stripped.
var sceneWebDLRE = regexp.MustCompile(`(?i)\bWEB[-._]?DL\b`)

// namedTokenRegex pairs a user-facing label with a pre-compiled regex.
// Stored as []slice (not map) so the diff helpers iterate in stable
// order, producing deterministic summaries.
//
// Exclude is an optional negative pattern: when it matches the input,
// the token is treated as NO match even if Pattern matches. Simulates
// negative lookbehind/lookahead which Go RE2 doesn't support. Example:
// IMAX sets Exclude=`(?i)\bnon[._ -]+imax\b` so titles flagged
// "NON-IMAX" don't false-match the IMAX trigger (Radarr uses CFs to
// distinguish IMAX from NON-IMAX; we'd otherwise rename grabs that
// explicitly mark themselves as not-IMAX).
//
// Edge case: a title containing BOTH "NON-IMAX" and a separate plain
// "IMAX" returns no match — rare enough that we accept the false-
// negative rather than carry a position-walking matcher.
type namedTokenRegex struct {
	Label   string
	Pattern *regexp.Regexp
	Exclude *regexp.Regexp
}

// MovieVersionTokens / SourceTokens / AudioTokens return slice copies
// of the built-in vocabularies. Adapters consume these via
// DiffMissingTokens; UI layers consume the labels directly to render
// "this rule will check for: X / Y / Z" hints in the wizard.
func MovieVersionTokens() []string { return tokensLabels(grabRenameMovieVersionTokens) }
func SourceTokens() []string       { return tokensLabels(grabRenameSourceTokens) }
func AudioTokens() []string        { return tokensLabels(grabRenameAudioTokens) }
func HdrTokens() []string          { return tokensLabels(grabRenameHdrTokens) }
func LanguageTokens() []string     { return tokensLabels(grabRenameLanguageTokens) }

func tokensLabels(set []namedTokenRegex) []string {
	out := make([]string, len(set))
	for i, t := range set {
		out[i] = t.Label
	}
	return out
}

// IsKnownSceneGroup returns true when rg matches any group in the
// TRaSH Scene CF list (case-insensitive). Used by the
// TriggerOnSceneMismatch logic to distinguish legit scene releases
// (rg IS a scene group → leave torrent name alone) from "fake-scene"
// titles (rg NOT in the set → rename to indexer release-title).
func IsKnownSceneGroup(rg string) bool {
	if rg == "" {
		return false
	}
	return grabRenameSceneCFGroups[strings.ToLower(strings.TrimSpace(rg))]
}

// IsSceneNamingPattern returns true when the input matches the
// scene-stripped torrent-name heuristic: resolution token + bare "WEB"
// (word-boundary on both sides; catches WEB-RG style) AND no WEB-DL /
// WEB.DL form. Indicates the indexer release-title's "WEB-DL" was
// stripped to bare "WEB" during torrent-name generation, which loses
// CF-scoring tokens at Radarr's import-time parse.
func IsSceneNamingPattern(input string) bool {
	if input == "" {
		return false
	}
	if !sceneResolutionRE.MatchString(input) {
		return false
	}
	if sceneWebDLRE.MatchString(input) {
		return false // real WEB-DL release, not scene-stripped
	}
	return sceneWebTokenRE.MatchString(input)
}

// leadingForeignBracketRE matches a leading "[...]" segment plus the
// separators that follow it. Group 1 is the bracket's inner text.
var leadingForeignBracketRE = regexp.MustCompile(`^\[([^\]]*)\][._ -]*`)

// HasLeadingForeignBracket reports whether name begins with a bracketed
// segment containing non-Latin (non-ASCII) characters, e.g.
// "[<non-Latin title>].Movie.Year...-RG". Radarr's parser takes such a
// prefix as the release group and drops the real trailing "-RG", so the
// imported file lands with the foreign text as its group. A plain-ASCII
// "[Group]" anime-style prefix is NOT flagged (Radarr handles those, and
// they're often the real group). Detection only — StripLeadingForeignBracket
// does the removal; the target is cleaned regardless of the grab title,
// so there's no "grab lacks it" guard (clean-in-place model).
func HasLeadingForeignBracket(name string) bool {
	m := leadingForeignBracketRE.FindStringSubmatch(strings.TrimSpace(name))
	return m != nil && hasNonASCII(m[1])
}

// StripLeadingForeignBracket removes a leading non-Latin "[...]" prefix
// (and the separators after it). No-op when the lead bracket is absent or
// plain-ASCII.
func StripLeadingForeignBracket(name string) string {
	trimmed := strings.TrimSpace(name)
	m := leadingForeignBracketRE.FindStringSubmatch(trimmed)
	if m == nil || !hasNonASCII(m[1]) {
		return name
	}
	return trimmed[len(m[0]):]
}

// fileExtensionRE matches a trailing video-container extension — these
// never belong in a torrent display name.
var fileExtensionRE = regexp.MustCompile(`(?i)\.(mkv|mp4|avi|m4v|mov|wmv|flv|webm|ts|mpg|mpeg|m2ts)$`)

// HasFileExtension reports whether name ends in a video-container
// extension.
func HasFileExtension(name string) bool {
	return fileExtensionRE.MatchString(strings.TrimRight(name, " "))
}

// StripFileExtension removes a trailing video-container extension.
// No-op when absent.
func StripFileExtension(name string) string {
	return fileExtensionRE.ReplaceAllString(strings.TrimRight(name, " "), "")
}

// hasNonASCII reports whether s contains any rune outside the 7-bit
// ASCII range (the cheap "is this Latin-script" proxy).
func hasNonASCII(s string) bool {
	for _, r := range s {
		if r > 127 {
			return true
		}
	}
	return false
}

// consecutiveYearRE matches two 4-digit years (1900-2099) back-to-back
// with one separator, capturing BOTH years so callers can compare them
// (Go's RE2 has no backreferences, so "same year" can't be expressed in
// the pattern — it's checked in code). (?:19|20)\d{2} excludes resolution
// tokens like 2160/1080 (they start 21/10, not 19/20).
var consecutiveYearRE = regexp.MustCompile(`\b((?:19|20)\d{2})[._ -]((?:19|20)\d{2})\b`)

// HasDuplicateYear reports whether name contains the SAME year twice
// consecutively (the genuine-duplication case: "Movie.2020.2020").
// Different consecutive years are NOT a duplicate (they're a year in the
// title plus the release year, "Movie.2049.2017"), so this returns
// false for those.
func HasDuplicateYear(name string) bool {
	for _, m := range consecutiveYearRE.FindAllStringSubmatch(name, -1) {
		if m[1] == m[2] {
			return true
		}
	}
	return false
}

// CollapseDuplicateYear removes a consecutive same-year duplication,
// keeping a single instance: "Movie.2020.2020.1080p..." →
// "Movie.2020.1080p...". Loops so a (rare) triple "2020.2020.2020"
// collapses fully. No-op on names without a duplicate, and never touches
// different consecutive years (only m[1]==m[2] matches are collapsed).
func CollapseDuplicateYear(name string) string {
	for {
		out := consecutiveYearRE.ReplaceAllStringFunc(name, func(s string) string {
			m := consecutiveYearRE.FindStringSubmatch(s)
			if m != nil && m[1] == m[2] {
				return m[1] // same year twice → keep one
			}
			return s // different years → leave untouched
		})
		if out == name {
			return out
		}
		name = out
	}
}

// HasBadNaming reports whether name carries any objective "bad naming"
// defect Radarr mis-parses: a leading non-Latin bracket, a trailing
// video extension, or a same-year duplication. Drives the Bad-naming
// trigger (whether to fire a rename when the display is malformed).
func HasBadNaming(name string) bool {
	return HasLeadingForeignBracket(name) || HasFileExtension(name) || HasDuplicateYear(name)
}

// CleanReleaseName removes the bad-naming defects from a rename target:
// strips a leading non-Latin bracket, collapses a same-year duplication,
// and strips a trailing video extension. Each step is a no-op when its
// defect is absent, so a correctly-named release passes through unchanged.
func CleanReleaseName(name string) string {
	out := StripLeadingForeignBracket(name)
	out = CollapseDuplicateYear(out)
	out = StripFileExtension(out)
	return out
}

// ResolveReleaseGroup picks the trustworthy release-group between Radarr's
// pre-parsed value and the one parseable from the name's trailing "-RG".
// Radarr is trusted by default, but overridden by the name when its value
// is an obvious mis-parse:
//   - empty (Radarr's parser bombed) → use the name;
//   - equal to a leading non-Latin bracket (Radarr took the bracket as
//     the group, e.g. "<non-Latin>") → use the name;
//   - itself non-ASCII (garbage) → use the name.
//
// A normal ASCII group that matches the name is always kept, so correctly
// named releases are never touched.
func ResolveReleaseGroup(radarrRG, name string) string {
	radarrRG = strings.TrimSpace(radarrRG)
	nameRG, _ := ParseReleaseGroupTolerant(name)
	if radarrRG == "" {
		return nameRG
	}
	if nameRG != "" {
		if m := leadingForeignBracketRE.FindStringSubmatch(strings.TrimSpace(name)); m != nil &&
			hasNonASCII(m[1]) && strings.EqualFold(strings.TrimSpace(m[1]), radarrRG) {
			return nameRG // Radarr took the leading bracket as the group
		}
		if hasNonASCII(radarrRG) {
			return nameRG // Radarr's value is non-Latin garbage
		}
	}
	return radarrRG
}

// DiffMissingTokens returns the labels of tokens present in `grab` but
// missing from `current`. Callers pick which token set (movie versions
// / sources / audio) to consult based on the rule's enabled triggers.
//
// Order is preserved from the slice (deterministic summaries — same
// inputs always produce the same label list).
func DiffMissingTokens(current, grab string, set []namedTokenRegex) []string {
	if grab == "" {
		return nil
	}
	// matches honours both the primary Pattern and an optional Exclude
	// negative pattern (see namedTokenRegex doc-comment). Used to skip
	// e.g. NON-IMAX titles when checking the IMAX trigger.
	matches := func(t namedTokenRegex, s string) bool {
		if !t.Pattern.MatchString(s) {
			return false
		}
		if t.Exclude != nil && t.Exclude.MatchString(s) {
			return false
		}
		return true
	}
	var out []string
	for _, t := range set {
		// Token must be PRESENT in grab AND ABSENT from current.
		if !matches(t, grab) {
			continue
		}
		if matches(t, current) {
			continue
		}
		out = append(out, t.Label)
	}
	return out
}

// DiffMissingMovieVersions / DiffMissingSources / DiffMissingAudio are
// thin wrappers over DiffMissingTokens that return the labels missing
// from current vs grab for each built-in vocabulary. Adapter calls
// these per-trigger; an empty result means "nothing to recover via
// this category".
func DiffMissingMovieVersions(current, grab string) []string {
	return DiffMissingTokens(current, grab, grabRenameMovieVersionTokens)
}

func DiffMissingSources(current, grab string) []string {
	return DiffMissingTokens(current, grab, grabRenameSourceTokens)
}

func DiffMissingAudio(current, grab string) []string {
	return DiffMissingTokens(current, grab, grabRenameAudioTokens)
}

func DiffMissingHdr(current, grab string) []string {
	return DiffMissingTokens(current, grab, grabRenameHdrTokens)
}

func DiffMissingLanguage(current, grab string) []string {
	return DiffMissingTokens(current, grab, grabRenameLanguageTokens)
}

// MatchCustomTokens applies a slice of user-defined "Label:regex"
// pairs (compiled at adapter call-time) and returns the labels of
// custom tokens present in grab but missing from current.
//
// Adapter compiles the regexes once per fire (cheap — RE2 compiles
// in microseconds). Pre-compile would risk staleness if the rule's
// CustomTokens list changes between fires. The adapter's caller
// validates each regex at save-time (webhook_rules.go validator),
// so the regex is guaranteed compilable.
func MatchCustomTokens(current, grab string, customTokens []CompiledCustomToken) []string {
	if grab == "" {
		return nil
	}
	var out []string
	for _, t := range customTokens {
		if t.Pattern == nil {
			continue
		}
		if !t.Pattern.MatchString(grab) {
			continue
		}
		if t.Pattern.MatchString(current) {
			continue
		}
		out = append(out, t.Label)
	}
	return out
}

// CompiledCustomToken pairs a user-supplied label with a runtime-
// compiled regex. Adapter builds these from rule.GrabRename.CustomTokens
// once per fire; fail-soft (skip the entry on compile-error since
// validator should have caught it but defence in depth).
type CompiledCustomToken struct {
	Label   string
	Pattern *regexp.Regexp
}

// ---------------------------------------------------------------------------
// Season-pack per-file rename ("files" target) — Sonarr only.
//
// Sonarr scores a season pack per file at import, not by the torrent
// name, so a torrent-name rename can't fix scene-stripped inner files.
// The "files" target renames each episode file to the release title
// with that file's SxxEyy substituted in, so each file parses with the
// full release tokens (NF, WEB-DL, group, ...). These two helpers are
// the pure transform; the per-file trigger loop + qBit calls live in
// the API adapter.
// ---------------------------------------------------------------------------

// seasonEpisodeTokenRE pulls the SxxEyy span (incl. multi-episode
// continuations S03E01E02 / S03E01-E02) out of a file name. Group 1 is
// the season digits.
var seasonEpisodeTokenRE = regexp.MustCompile(`(?i)\bS(\d{1,4})E\d{1,3}(?:[-E]+\d{1,3})*\b`)

// ParseSeasonEpisodeToken extracts the canonical SxxEyy token from a
// file name (e.g. "the.last.kingdom.s03e01.proper...mkv" → "S03E01",
// season 3). Multi-episode spans are preserved ("S03E01E02"). Returns
// ("", 0, false) when the name carries no SxxEyy.
func ParseSeasonEpisodeToken(name string) (token string, season int, ok bool) {
	m := seasonEpisodeTokenRE.FindStringSubmatch(name)
	if m == nil {
		return "", 0, false
	}
	s, err := strconv.Atoi(m[1])
	if err != nil {
		return "", 0, false
	}
	// m[0] is the whole SxxEyy(+) span; upper-case so the rebuilt title
	// is consistent regardless of the file's casing.
	return strings.ToUpper(m[0]), s, true
}

// BuildSeasonPackEpisodeTitle takes a season-level grab title (e.g.
// "The Last Kingdom S03 1080p Proper NF WEB-DL DD+ 5.1 x264-STRiFE")
// and a per-episode token parsed from a file inside the pack ("S03E01",
// season 3) and returns the grab title with the season token expanded to
// that episode token:
//
//	"The Last Kingdom S03 1080p ... -STRiFE" + "S03E01"
//	  → "The Last Kingdom S03E01 1080p ... -STRiFE"
//
// Matches the season token as "S03"/"S3" or "Season 03"/"Season 3" for
// the season episodeToken belongs to. Only a season-ONLY token is
// replaced (not one already followed by E<n>), so a grab title that is
// already per-episode is left alone (returns false → caller uses the
// title as-is or skips). Returns ("", false) when episodeToken is
// malformed or no matching season token exists — caller skips that file
// rather than guessing.
func BuildSeasonPackEpisodeTitle(grabTitle, episodeToken string, season int) (string, bool) {
	episodeToken = strings.ToUpper(strings.TrimSpace(episodeToken))
	if grabTitle == "" || episodeToken == "" || season <= 0 {
		return "", false
	}
	n := strconv.Itoa(season)
	sxx := regexp.MustCompile(`(?i)\bS0*` + n + `\b`)
	seasonWord := regexp.MustCompile(`(?i)\bSeason\s+0*` + n + `\b`)

	if loc := firstSeasonOnlyMatch(grabTitle, sxx); loc != nil {
		return grabTitle[:loc[0]] + episodeToken + grabTitle[loc[1]:], true
	}
	if loc := seasonWord.FindStringIndex(grabTitle); loc != nil {
		return grabTitle[:loc[0]] + episodeToken + grabTitle[loc[1]:], true
	}
	return "", false
}

// firstSeasonOnlyMatch returns the first Sxx match that is NOT already
// followed by an episode marker (E<digit>) — a genuine season-only
// token, not the "Sxx" half of an existing "SxxEyy". RE2 has no negative
// lookahead, so matches are filtered here.
func firstSeasonOnlyMatch(s string, re *regexp.Regexp) []int {
	for _, loc := range re.FindAllStringIndex(s, -1) {
		end := loc[1]
		if end < len(s) {
			rest := s[end:]
			if len(rest) >= 2 && (rest[0] == 'E' || rest[0] == 'e') && rest[1] >= '0' && rest[1] <= '9' {
				continue // already SxxEyy
			}
		}
		return loc
	}
	return nil
}
