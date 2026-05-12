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
	"strings"
)

// grabRenameMovieVersionTokens — TRaSH's "Optional Movie Versions" CF
// group. One regex per concept (e.g. `imax` matches both IMAX and
// IMAX Enhanced). Bash tagarr_import.sh:330-343 carries the same set.
//
// TRaSH source: docs/json/radarr/cf/optional-movie-versions.json
// (CF group ID f4f1474b963b24cf983455743aa9906c).
var grabRenameMovieVersionTokens = []namedTokenRegex{
	{Label: "Director's Cut", Pattern: regexp.MustCompile(`(?i)\bdirector('?s)?[._ -]?cut\b`)},
	{Label: "Theatrical", Pattern: regexp.MustCompile(`(?i)\btheatrical\b`)},
	{Label: "Extended", Pattern: regexp.MustCompile(`(?i)\bextended\b`)},
	{Label: "Unrated", Pattern: regexp.MustCompile(`(?i)\bunrated\b`)},
	{Label: "Uncut", Pattern: regexp.MustCompile(`(?i)\buncut\b`)},
	{Label: "Remaster", Pattern: regexp.MustCompile(`(?i)\bremaster(ed)?\b`)},
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
	{Label: "Open Matte", Pattern: regexp.MustCompile(`(?i)\bopen[ ._-]?matte\b`)},
}

// grabRenameSourceTokens — streaming-source flag tokens that influence
// CF scoring on release-title. These typically don't end up in the
// stripped torrent name, so renaming preserves the score.
//
// Container-policy expansion vs bash: bash tagarr_import.sh:319-322
// only checks MA WEB-DL / Play WEB-DL / plain WEB-DL. Container adds
// 8 streaming-services (AMZN/NF/DSNP/HMAX/HULU/PCOK/CR/ATVP) that
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
	"cakes":            true,
	"ggez":             true,
	"ggwp":             true,
	"glhf":             true,
	"gossip":           true,
	"naisu":            true,
	"kogi":             true,
	"peculate":         true,
	"slot":             true,
	"edith":            true,
	"ethel":            true,
	"eleanor":          true,
	"b2b":              true,
	"spamneggs":        true,
	"ftp":              true,
	"dirt":             true,
	"syncopy":          true,
	"bae":              true,
	"successfulcrab":   true,
	"nhtfs":            true,
	"surcode":          true,
	"b0mbardiers":      true,
	"d3us":             true,
	"brotherhood":      true,
	"w4k":              true,
	"strikes":          true,
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
