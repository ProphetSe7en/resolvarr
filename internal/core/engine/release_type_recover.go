package engine

import (
	"regexp"
	"strings"
)

// release_type_recover.go — pure decision logic for the Sonarr-only
// "Recover release type" feature. The API handler adapts arr history +
// episode-file data into these inputs and applies the verdict via
// ManualImport. See docs/resolvarr/release-type-recovery-design.md §5.
//
// This file covers the durable tiers of the cascade (Tier 1 stored field
// + Tier 3 sourceTitle inference). The optional Tier 2 (downloadId -> qBit
// confirmation) is layered in the handler, since it needs a live client.

var (
	// Multi-episode: S03E04E05 or S03E04-E05 (one file, several episodes).
	releaseMultiEpisodeRE = regexp.MustCompile(`(?i)\bS\d{1,3}E\d{1,3}(?:[ ._-]?E\d{1,3})+`)
	// Single episode: S03E04.
	releaseEpisodeRE = regexp.MustCompile(`(?i)\bS\d{1,3}E\d{1,3}`)
	// Season pack: a season tag with NO episode marker — "S03" not followed
	// by E, or "Season 3".
	releaseSeasonRE = regexp.MustCompile(`(?i)(?:\bS\d{1,3}(?:[^E0-9]|$)|\bSeason[ ._]?\d{1,3})`)
)

// ClassifyReleaseTypeFromTitle infers Sonarr's release type from a grab's
// release name (sourceTitle). Returns one of the ReleaseType* constants,
// or "" when the title carries no recognisable season/episode marker.
//
//	"Show.S03E04E05.1080p"  -> multiEpisode
//	"Show.S03E04.1080p"     -> singleEpisode
//	"Show.S03.1080p" / "Season 3" (no E) -> seasonPack
func ClassifyReleaseTypeFromTitle(title string) string {
	switch {
	case releaseMultiEpisodeRE.MatchString(title):
		return ReleaseTypeMultiEpisode
	case releaseEpisodeRE.MatchString(title):
		return ReleaseTypeSingleEpisode
	case releaseSeasonRE.MatchString(title):
		return ReleaseTypeSeasonPack
	default:
		return ""
	}
}

// NormaliseReleaseType maps any casing/spelling of a Sonarr release type
// (field value "SeasonPack"/"seasonPack", etc.) to the canonical
// ReleaseType* constant, or "" for empty/"unknown"/unrecognised.
func NormaliseReleaseType(s string) string {
	l := strings.ToLower(strings.TrimSpace(s))
	switch {
	case strings.Contains(l, "season"):
		return ReleaseTypeSeasonPack
	case strings.Contains(l, "multi"):
		return ReleaseTypeMultiEpisode
	case strings.Contains(l, "single"):
		return ReleaseTypeSingleEpisode
	default:
		return "" // "", "unknown", or anything we don't model
	}
}

// ReleaseTypeGrab is one grab event reduced to what the cascade needs.
type ReleaseTypeGrab struct {
	SourceTitle  string
	ReleaseGroup string
	FieldType    string // grab's stored data.releaseType ("" on pre-v4 grabs)
}

// ReleaseTypeRecoverInput is one episode file plus the grab history for
// its episode (the handler pre-filters history to grab events for this
// file's episode).
type ReleaseTypeRecoverInput struct {
	CurrentType  string // the file's current releaseType ("unknown" etc.)
	ReleaseGroup string // the file's releaseGroup (the confirmation key)
	Grabs        []ReleaseTypeGrab
}

// Confidence levels surfaced in the preview.
//
// Unconfirmed is the honest verdict for a Single/Multi episode determined
// only from a grab's sourceTitle (no stored field): a season pack produces
// byte-identical per-episode files on disk and can exist with no grab at all
// (cross-seed), so without qBit we cannot prove the file isn't actually from
// a pack. These are shown but never auto-applied. (See the Whiskey Cavalier
// case in docs/resolvarr/release-type-recovery-design.md.)
const (
	ReleaseTypeConfHigh        = "high"
	ReleaseTypeConfMedium      = "medium"
	ReleaseTypeConfUnconfirmed = "unconfirmed"
)

// ReleaseTypeGrabEvidence is one grab from the episode's history, annotated
// with what it implies and whether it counted toward the verdict. Surfaced
// in the preview so the user can see WHY a file got High vs Medium.
type ReleaseTypeGrabEvidence struct {
	SourceTitle    string // the grab's release name
	ReleaseGroup   string // the grab's release group
	StoredType     string // Sonarr's stored grab releaseType, "" on pre-v4 grabs
	ImpliedType    string // what this grab implies (stored field, else from the name)
	GroupMatch     bool   // this grab's group matches the file's group
	UsedInDecision bool   // this grab counted toward the verdict
}

// ReleaseTypeVerdict is the cascade outcome for one episode file.
type ReleaseTypeVerdict struct {
	RecoveredType string // canonical type, or "" when undeterminable
	Confidence    string // "high" | "medium" | "" (none)
	Source        string // "field" | "title" — which tier decided
	GroupMatched  bool   // a grab matched the file's release group
	IsCandidate   bool   // recovered is determinable AND differs from current
	Reason        string // short one-line summary for the collapsed row
	Explanation   string // full plain-language "why this confidence" for the drill-down

	Evidence []ReleaseTypeGrabEvidence // every grab considered, annotated
}

// DecideReleaseTypeRecovery runs the durable cascade (Tier 1 field, Tier 3
// sourceTitle) over one file's grabs and returns a verdict. Conservative:
// disagreeing grabs or no determinable type yield no candidate.
//
// Confidence: field used OR group-matched title -> high; title without a
// group match (grabs couldn't be tied to this exact file) -> medium. The
// handler's optional Tier 2 (qBit) can confirm/raise a medium verdict.
func DecideReleaseTypeRecovery(in ReleaseTypeRecoverInput) ReleaseTypeVerdict {
	fileGroup := strings.ToLower(strings.TrimSpace(in.ReleaseGroup))

	// Annotate every grab: its implied type + whether its group matches the
	// file. Group-matched grabs tie a specific download to this file.
	anyGroupMatch := false
	evidence := make([]ReleaseTypeGrabEvidence, 0, len(in.Grabs))
	for _, g := range in.Grabs {
		stored := NormaliseReleaseType(g.FieldType)
		implied := stored
		if implied == "" {
			implied = ClassifyReleaseTypeFromTitle(g.SourceTitle)
		}
		gm := fileGroup != "" && strings.ToLower(strings.TrimSpace(g.ReleaseGroup)) == fileGroup
		if gm {
			anyGroupMatch = true
		}
		evidence = append(evidence, ReleaseTypeGrabEvidence{
			SourceTitle:  g.SourceTitle,
			ReleaseGroup: g.ReleaseGroup,
			StoredType:   stored,
			ImpliedType:  implied,
			GroupMatch:   gm,
		})
	}

	// Decide which grabs count: the group-matched ones tie to this file; if
	// none match (no file group, or no grab shares it) fall back to all grabs
	// and cap confidence at medium.
	usedField := false
	typeSet := map[string]bool{}
	for i := range evidence {
		ev := &evidence[i]
		if anyGroupMatch && !ev.GroupMatch {
			continue // a grab from a different release didn't make this file
		}
		ev.UsedInDecision = true
		if ev.ImpliedType != "" {
			typeSet[ev.ImpliedType] = true
		}
		if ev.StoredType != "" {
			usedField = true
		}
	}

	switch {
	case len(typeSet) == 0:
		return ReleaseTypeVerdict{
			Reason:      "no grab carried a determinable release type",
			Explanation: "None of the grabs in this episode's history had a stored release type or a recognisable season/episode pattern, so the type can't be worked out.",
			Evidence:    evidence,
		}
	case len(typeSet) > 1:
		return ReleaseTypeVerdict{
			Reason:      "grabs disagree on the release type, left alone",
			Explanation: "The grabs that match this file point to more than one release type, so it's left alone rather than guessed.",
			Evidence:    evidence,
		}
	}

	var recovered string
	for t := range typeSet {
		recovered = t
	}

	source := "title"
	if usedField {
		source = "field"
	}

	confidence, reason, explanation := releaseTypeRationale(recovered, usedField, anyGroupMatch)

	current := NormaliseReleaseType(in.CurrentType)
	isCandidate := recovered != "" && recovered != current

	return ReleaseTypeVerdict{
		RecoveredType: recovered,
		Confidence:    confidence,
		Source:        source,
		GroupMatched:  anyGroupMatch,
		IsCandidate:   isCandidate,
		Reason:        reason,
		Explanation:   explanation,
		Evidence:      evidence,
	}
}

// releaseTypeRationale decides the confidence and builds the row reason +
// drill-down explanation. The key asymmetry (no qBit): a Season Pack is safe
// to assert from grab evidence, but a Single/Multi episode determined only
// from a sourceTitle is NOT — a pack imported per-episode looks byte-identical
// on disk and can exist with no grab (cross-seed). So sourceTitle-only
// singles are "unconfirmed" until qBit (or a stored field) settles it.
func releaseTypeRationale(recovered string, usedField, groupMatched bool) (confidence, reason, explanation string) {
	isPack := recovered == ReleaseTypeSeasonPack
	switch {
	case usedField:
		// Sonarr's own grab-time record — authoritative for any type.
		return ReleaseTypeConfHigh,
			"from Sonarr's stored grab type",
			"Sonarr stored the release type on the grab event, so this is taken directly from Sonarr's own record. That's why it's High."
	case isPack && groupMatched:
		return ReleaseTypeConfHigh,
			"season pack grab, release group matched",
			"A grab whose release group matches this file is a season pack, and a season pack is unambiguous. That's why it's High."
	case isPack:
		return ReleaseTypeConfMedium,
			"season pack grab, no release-group match",
			"The grab looks like a season pack but no grab's release group matched this file, so it's Medium, worth a quick look."
	default:
		// Single/Multi from a sourceTitle only — cannot be trusted on its own.
		return ReleaseTypeConfUnconfirmed,
			"looks like a single episode, but unconfirmed",
			"The grab name looks like a single episode, but a season pack produces byte-identical per-episode files on disk and can exist without a grab (cross-seed). Without qBit we can't prove this file isn't actually from a season pack, so it's left Unconfirmed and not applied automatically."
	}
}
