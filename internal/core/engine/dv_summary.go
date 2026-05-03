package engine

import (
	"regexp"
)

// DvDetail captures the three Dolby Vision facts we care about, parsed
// from `dovi_tool info --summary` output. Closely tracks the bash
// script's extraction logic (TRaSH/Starr-taggers/Radarr-DV-HDR-
// Tagarr/dv-hdr_tagarr.sh:281-320) with one deliberate extension: the
// FEL detection regex also accepts the phrase "enhancement layer" as
// a synonym for the FEL keyword. Newer dovi_tool versions sometimes
// print the long form; the bash script would miss those. Test:
// TestParseDvSummary_FelDetectionViaEnhancementLayer pins the
// behaviour.
//
//   - Profile is the DV profile number. The script only branches on 7
//     and 8; 5 is theoretical (Apple iTunes-exclusive) and rare in the
//     wild. 0 means "summary didn't surface a profile we recognise".
//   - Layer is FEL or MEL — meaningful for Profile 7 only. Profile 8
//     is single-layer; Profile 5 is single-layer (and base-layer, no
//     enhancement). Empty when not applicable or unknown.
//   - CMVersion is 2 or 4 — the Content Mapping metadata version.
//     Defaults to 2 when the summary mentions any CM but not v4
//     (script's behaviour).
//
// Zero values mean "couldn't parse" — callers decide whether to fall
// through to the no-dv tag.
type DvDetail struct {
	Profile   int    // 5 / 7 / 8 / 0 (unknown)
	Layer     string // "fel" / "mel" / "" (n/a or unknown)
	CMVersion int    // 2 / 4 / 0 (unknown)
}

// Tags emits the bash-script-equivalent tag labels for this detail.
// Returns the labels in a stable order so callers can compare diffs
// reliably. Profile 5 and unknown-profile cases produce no profile
// tag (matching bash). The base "dv" tag is added by the calling
// scan-handler regardless of detail success — this list is just the
// extra DV-detail tags layered on top.
//
// Tag vocabulary matches the upstream bash script's set:
//
//	mel · fel · dvprofile8 · cm2 · cm4
//
// Profile 5 and unknown-profile produce no profile/layer tag (the
// movie still gets the base `dv` tag from the HDR-base layer).
func (d DvDetail) Tags() []string {
	var out []string
	switch d.Profile {
	case 7:
		if d.Layer == "fel" {
			out = append(out, "fel")
		} else {
			// Default to mel for Profile 7 without explicit FEL marker —
			// matches bash script behaviour at dv-hdr_tagarr.sh:288.
			out = append(out, "mel")
		}
	case 8:
		out = append(out, "dvprofile8")
	}
	switch d.CMVersion {
	case 4:
		out = append(out, "cm4")
	case 2:
		out = append(out, "cm2")
	}
	return out
}

var (
	// Profile detection — case-insensitive, allows whitespace variants
	// ("Profile: 7", "profile:7", "PROFILE 7"). dovi_tool prints
	// "Profile: 7.6" or "Profile: 8.1" — we strip the sub-version.
	// `\b` after the digit prevents matches against hypothetical
	// future "Profile: 70" / "Profile: 87" formats.
	reDvProfile7 = regexp.MustCompile(`(?i)profile\s*:?\s*7\b`)
	reDvProfile8 = regexp.MustCompile(`(?i)profile\s*:?\s*8\b`)

	// FEL detection — bash uses bare case-insensitive "FEL" substring
	// match; we extend with "enhancement layer" phrase as a synonym
	// for newer dovi_tool output (see DvDetail header comment).
	// Empty/false → MEL (Profile 7 always has either FEL or MEL
	// layer). MEL keyword alone isn't checked because absence of FEL
	// is sufficient evidence per the script.
	reDvFEL = regexp.MustCompile(`(?i)\bFEL\b|enhancement.{0,10}layer`)

	// CM version — "CM v4.0" / "CM v2.9" / "CM 4.0". Bash:
	//   grep -qiE "CM v4\.0"     → cm4
	//   grep -qiE "CM v[0-9]"    → cm2 (any other version)
	// `\b` before CM in both regexes prevents accidental substring
	// matches against unrelated tokens like "DCM" or "ACM".
	reDvCM4 = regexp.MustCompile(`(?i)\bCM\s*v?\s*4(\.\d+)?\b`)
	reDvCM  = regexp.MustCompile(`(?i)\bCM\s*v?\s*\d`)
)

// ParseDvSummary reads the text produced by `dovi_tool info -i <rpu>
// --summary` and extracts the DV facts. Tolerant of extra lines,
// blank input, or partial summaries — fields it can't parse stay at
// the zero value.
//
// Real summary text from dovi_tool 2.1.2 looks like:
//
//	Profile: 7.6
//	FEL
//	DM version: 1
//	CM v2.9
//
// or
//
//	Profile: 8.1
//	DM version: 2
//	CM v4.0
//
// Different combinations + sub-versions appear across different
// encodes. This parser ignores anything it doesn't recognise.
func ParseDvSummary(summary string) DvDetail {
	d := DvDetail{}
	if reDvProfile7.MatchString(summary) {
		d.Profile = 7
		if reDvFEL.MatchString(summary) {
			d.Layer = "fel"
		} else {
			d.Layer = "mel"
		}
	} else if reDvProfile8.MatchString(summary) {
		d.Profile = 8
	}
	switch {
	case reDvCM4.MatchString(summary):
		d.CMVersion = 4
	case reDvCM.MatchString(summary):
		d.CMVersion = 2
	}
	return d
}

// HdrTypeIndicatesDv mirrors the bash script's case-insensitive
// substring match for "DV" or "Dolby" in Radarr's mediaInfo
// videoDynamicRangeType. Used at the candidate-selection layer to
// decide whether to invoke the (slow) dovi_tool extraction.
//
// Radarr's known type strings: "" (SDR), "HDR10", "HDR10Plus",
// "PQ", "HLG", "DV", "DV HDR10", "DV HDR10Plus", "DV HLG", "DV SDR".
// All DV-flavoured strings start with "DV".
func HdrTypeIndicatesDv(hdrType string) bool {
	return reDvHint.MatchString(hdrType)
}

var reDvHint = regexp.MustCompile(`(?i)\b(DV|dolby)\b`)

// vocabDvDetail is the closed set of values M4b's DV-detail emits.
// Two logical groups: profile/layer ("mel" / "fel" / "dvprofile8")
// and CM version ("cm2" / "cm4"). Order chosen for display: layer
// info first (most distinctive), then CM version.
//
// Single source of truth — DvDetailVocabulary returns it,
// AllPossibleDvDetailTags + EmittableDvDetailTags both iterate it,
// and DvDetail.Tags emits values from this set. A new vocab value
// added here must therefore land in the parser too; the test suite
// (TestParseDvSummary_*) pins the parser-vs-vocab pair.
var vocabDvDetail = []string{"mel", "fel", "dvprofile8", "cm2", "cm4"}

// DvDetailVocabulary returns the closed set of DV-detail emit
// values, copied so callers can't mutate the package state. Used
// by the API layer to validate user-supplied AllowedValues against
// a known vocabulary, and by the UI to render the per-value
// allow-list checkboxes.
func DvDetailVocabulary() []string {
	return append([]string(nil), vocabDvDetail...)
}

// DvDetailConfig is the subset of core.DvDetailConfig the engine
// needs for emit decisions. Mirror struct kept here so the engine
// stays I/O-free + import-clean (engine never imports core).
//
// The same translation pattern as ExtraTagsToEngine — handlers
// build this from their core.DvDetailConfig before calling
// emit/possible/emittable helpers.
type DvDetailConfig struct {
	Enabled       bool
	Prefix        string
	AllowedValues []string // nil/empty = all 5 vocab values allowed (when SelectMode != "select")
	SelectMode    string   // "" or "all" (default — empty=all-allowed) | "select" (exact list, empty=tag nothing)
}

// allowedValuesSet builds a lookup set with two-mode semantics:
//   - SelectMode != "select": back-compat. Nil/empty list returns nil
//     (downstream interprets nil as "everything allowed").
//   - SelectMode == "select": exact. Empty list returns an empty set
//     (downstream filters everything out — explicit "tag nothing").
//
// Centralised so AllPossible / Emittable / EmitForFile share identical
// semantics — drift here would surface as inconsistent cleanup
// behaviour.
func dvDetailAllowedValuesSet(allowed []string, selectMode string) map[string]bool {
	if len(allowed) == 0 {
		if selectMode == "select" {
			return map[string]bool{} // explicit empty
		}
		return nil // legacy: empty == all
	}
	out := make(map[string]bool, len(allowed))
	for _, v := range allowed {
		out[v] = true
	}
	return out
}

// AllPossibleDvDetailTags returns the universe of tags this
// configuration could ever emit, regardless of Enabled flag or
// AllowedValues filter. Same role as ExtraTags.AllPossibleTags —
// the cleanup safety-bound for orphan removal.
//
// Map shape: prefixed-tag → "dvdetail" (single bucket name; we
// don't subdivide). Keys mean "this label is something the engine
// COULD emit, so cleanup may treat it as ours". User-defined or
// release-group tags are absent from the map and therefore safe.
//
// Deliberately ignores Enabled AND AllowedValues for the same
// reason as the ExtraTags version: disabling the feature must NOT
// orphan tags users already have unless they opt into cleanup
// (RemoveOrphanedTags=true), in which case the cleanup pass
// switches to EmittableDvDetailTags as its narrower bound.
func AllPossibleDvDetailTags(cfg DvDetailConfig) map[string]string {
	out := make(map[string]string, len(vocabDvDetail))
	for _, v := range vocabDvDetail {
		out[cfg.Prefix+v] = "dvdetail"
	}
	return out
}

// EmittableDvDetailTags returns only the labels this configuration
// would emit RIGHT NOW given Enabled + AllowedValues. Companion to
// AllPossibleDvDetailTags; used as the cleanup bound when the user
// opts OUT of orphan removal (RemoveOrphanedTags=false). Disabled
// or filtered-out values stay on already-tagged movies untouched.
func EmittableDvDetailTags(cfg DvDetailConfig) map[string]string {
	// Returns an empty (non-nil) map when disabled rather than nil,
	// because callers iterate this with `for k := range got` and
	// nil maps are fine for that — but a non-nil empty map reads
	// more clearly when the caller checks `len(got)==0` for "no
	// emittable labels right now". Companion EmitDvDetailTags
	// returns nil for the same condition; both yield len==0 so
	// callers can treat them identically.
	if !cfg.Enabled {
		return map[string]string{}
	}
	allowed := dvDetailAllowedValuesSet(cfg.AllowedValues, cfg.SelectMode)
	out := make(map[string]string, len(vocabDvDetail))
	for _, v := range vocabDvDetail {
		if allowed != nil && !allowed[v] {
			continue
		}
		out[cfg.Prefix+v] = "dvdetail"
	}
	return out
}

// EmitDvDetailTags returns the tag labels this configuration
// would emit for the given DvDetail extraction result. Return
// shape:
//
//   - nil when cfg.Enabled is false (master toggle short-circuit)
//   - nil when the DvDetail produces no bare values (zero-init,
//     Profile 5 with no CM info)
//   - possibly-empty (non-nil) slice when Enabled + bare values
//     present but every value filtered out by AllowedValues —
//     callers can `len(...)` either case identically
//
// AllowedValues entries that aren't in the canonical vocabulary
// are silently ignored at emit-time (the lookup map check just
// fails). API-layer validation should reject unknown values at
// save-time so the on-disk shape stays clean; the engine is
// permissive so a stale config doesn't crash a scan.
//
// Pure: same inputs always give the same outputs; safe to call
// from concurrent scan goroutines (every call allocates fresh
// `out` + `allowed` containers, no shared state).
func EmitDvDetailTags(detail DvDetail, cfg DvDetailConfig) []string {
	// Disabled and empty-detail return nil rather than empty
	// slice; the all-filtered-out case below returns a non-nil
	// empty slice. The asymmetry is deliberate — `nil` reads as
	// "no work to do at all", `[]string{}` reads as "we tried,
	// nothing matched". Callers should treat both as `len==0`.
	if !cfg.Enabled {
		return nil
	}
	bare := detail.Tags()
	if len(bare) == 0 {
		return nil
	}
	allowed := dvDetailAllowedValuesSet(cfg.AllowedValues, cfg.SelectMode)
	out := make([]string, 0, len(bare))
	for _, v := range bare {
		if allowed != nil && !allowed[v] {
			continue
		}
		out = append(out, cfg.Prefix+v)
	}
	return out
}
