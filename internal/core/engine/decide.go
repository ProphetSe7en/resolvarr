package engine

import "strings"

// MovieFile is the subset of Arr's movieFile we need to decide tags.
// Field names mirror Arr's JSON so calling code can populate from an
// API response without a translation layer. All three fields are
// optional — an imported movie may have releaseGroup set but sceneName
// blank, or the reverse.
type MovieFile struct {
	RelativePath string
	SceneName    string
	ReleaseGroup string
}

// GroupConfig is one entry from the user's RELEASE_GROUPS config,
// shaped as the colon-tuple bash reads: "search:tag:display:mode".
// Only Search and Mode participate in the tag decision; Tag and
// Display travel along for the caller's persistence / notification
// use.
type GroupConfig struct {
	Search  string // case-insensitive word-boundary match target
	Tag     string // label written to Arr's tag list
	Display string // human-readable name for logs
	Mode    string // "simple" | "filtered"
}

// QualityResult / AudioResult values emitted in Decision. Kept as
// exact strings because tagarr.sh's debug lines and Discord messages
// emit them verbatim; user-facing logs grep against these literals.
const (
	ResultPass         = "PASS"
	ResultFail         = "FAIL"
	ResultNA           = "N/A"
	ResultNASimpleMode = "N/A (simple mode)"
)

// Decision is the full result of evaluating one (movie, group, filters)
// tuple. Carries everything the apply phase, the Discord embeds, and
// the debug trace need — so the apply phase never re-runs the
// decision and callers can't diverge from what the engine said.
type Decision struct {
	// ShouldHave: true = movie ought to carry this tag; false = not.
	// Combine with the current tag state to pick an action:
	//   ShouldHave && !HasTag → add
	//   ShouldHave &&  HasTag → keep (no-op)
	//  !ShouldHave &&  HasTag → remove (with Reason)
	//  !ShouldHave && !HasTag → skip
	ShouldHave bool

	// Matched: did the group's Search match any of the three fields?
	// Used by the "Wrong release group" reason on removes — a false
	// match and a filter-fail both produce ShouldHave=false, but they
	// warrant different reasons.
	Matched       bool
	MatchLocation string // one of MatchLocation* constants, or "" on miss

	// QualityResult / AudioResult are the filter verdicts. For simple
	// mode they're both "N/A (simple mode)"; on a non-match the group
	// never reaches the filter so both are "N/A".
	QualityResult string
	QualityDetail string // human-readable, e.g. "MA WEB-DL" or "AMZN (not MA/Play)"
	AudioResult   string
	AudioDetail   string

	// Reason explains ShouldHave=false in user-facing terms. Emitted
	// as-is in removal notifications and debug logs. Values:
	//   "Wrong release group"   — Search didn't match any field
	//   "Failed quality"        — quality filter fail (audio passed)
	//   "Failed audio"          — audio filter fail (quality passed)
	//   "Failed quality & audio" — both failed
	// Empty when ShouldHave=true.
	Reason string
}

// DecideTag runs the full per-movie, per-group tag decision tree. A
// byte-for-byte port of tagarr.sh:826-1019 without the I/O (apply
// handlers persist the decision separately — this function is pure).
//
// The caller passes MovieFile, the group config, and the active
// FilterConfig. Decision is returned ready-to-persist. Lowercasing is
// done internally so callers don't have to mirror bash's jq
// ascii_downcase convention.
func DecideTag(mf MovieFile, group GroupConfig, cfg FilterConfig) Decision {
	// combined_for_filters mirrors tagarr.sh:816 — quality/audio
	// checks run against relativePath + sceneName + releaseGroup
	// joined with spaces. Joining with space prevents two tokens from
	// two fields merging into a false match (e.g. "MA" field + "WEB"
	// field bleeding into "MAWEB").
	relLower := strings.ToLower(mf.RelativePath)
	sceneLower := strings.ToLower(mf.SceneName)
	rgLower := strings.ToLower(mf.ReleaseGroup)
	combined := relLower + " " + sceneLower + " " + rgLower
	search := strings.ToLower(group.Search)

	matched, location := MatchReleaseGroup(rgLower, sceneLower, relLower, search)

	d := Decision{
		Matched:       matched,
		MatchLocation: location,
	}

	// No match — reject before running filters.
	if !matched {
		d.ShouldHave = false
		d.Reason = "Wrong release group"
		d.QualityResult = ResultNA
		d.AudioResult = ResultNA
		return d
	}

	// Simple mode — any match tags, filters don't run.
	if group.Mode == "simple" {
		d.ShouldHave = true
		d.QualityResult = ResultNASimpleMode
		d.AudioResult = ResultNASimpleMode
		return d
	}

	// Filtered mode — both quality AND audio must pass.
	qOK := CheckQuality(cfg, combined)
	aOK := CheckAudio(cfg, combined)

	if qOK {
		d.QualityResult = ResultPass
		d.QualityDetail = QualityDetailPass(combined)
	} else {
		d.QualityResult = ResultFail
		d.QualityDetail = QualityDetailFail(combined)
	}
	if aOK {
		d.AudioResult = ResultPass
		d.AudioDetail = AudioDetailPass(combined)
	} else {
		d.AudioResult = ResultFail
		d.AudioDetail = AudioDetailFail(combined)
	}

	if qOK && aOK {
		d.ShouldHave = true
		return d
	}

	d.ShouldHave = false
	switch {
	case !qOK && !aOK:
		d.Reason = "Failed quality & audio"
	case !qOK:
		d.Reason = "Failed quality"
	default: // !aOK
		d.Reason = "Failed audio"
	}
	return d
}
