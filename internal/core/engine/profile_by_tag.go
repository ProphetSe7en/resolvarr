// Package engine — profile_by_tag.go
//
// Profile-by-tag: decide which quality profile an item should use from the
// tags it carries, via AND/OR rules. Pure (no I/O); the handler supplies items
// + rules and applies the resulting moves through the arr.Client.
//
// The rule model mirrors purgebot v1.5 (src/bot.js matchRule): each rule is a
// list of conditions joined by per-condition operators forming OR-of-AND
// groups, with AND binding tighter than OR ("A AND B OR C" = "(A AND B) OR C").
// A rule matches when ANY group's conditions all match. Multiple rules combine
// as OR. For now the only condition Type is "tag" (Value = tag id); the generic
// shape leaves room for CF / filename / genre conditions later.
package engine

import (
	"strconv"
	"strings"
)

// ProfileCondition is one row of a rule. Type is "tag" for now; Value is the
// tag id (as a string, matching the UI select); Join is "and" (default) or
// "or" and decides how this condition attaches to the chain.
type ProfileCondition struct {
	Type  string `json:"type"`
	Value string `json:"value"`
	Join  string `json:"join,omitempty"`
	// Not negates the condition: when true, the condition matches an item that
	// does NOT satisfy it (e.g. "does not have tag X"). Combined with Join this
	// gives AND NOT / OR NOT. An invalid condition never matches, negated or not.
	Not bool `json:"not,omitempty"`
}

// ProfileRule is a set of conditions plus the target quality profile to assign
// when the rule matches.
type ProfileRule struct {
	Conditions []ProfileCondition `json:"conditions"`
	ProfileID  int                `json:"profileId"`
}

// ProfileItem is the engine's view of a movie/series for profile evaluation.
// (Engine has no arr import; the handler maps arr.Item -> ProfileItem.)
type ProfileItem struct {
	ID               int
	Title            string
	Year             int
	Tags             []int
	CurrentProfileID int
}

// ProfileMove is an item that should change profile.
type ProfileMove struct {
	ItemID           int
	Title            string
	Year             int
	FromProfileID    int
	ToProfileID      int
	MatchedRuleIndex int // index into the rules slice that decided the move
}

// ProfileConflict is an item that matched rules pointing at more than one
// distinct profile — ambiguous, so it is surfaced and never moved.
type ProfileConflict struct {
	ItemID              int
	Title               string
	Year                int
	CandidateProfileIDs []int
}

func itemHasTag(item ProfileItem, tagID int) bool {
	for _, t := range item.Tags {
		if t == tagID {
			return true
		}
	}
	return false
}

// matchProfileCondition evaluates a single condition against an item.
func matchProfileCondition(item ProfileItem, c ProfileCondition) bool {
	switch c.Type {
	case "tag":
		id, err := strconv.Atoi(strings.TrimSpace(c.Value))
		if err != nil || id <= 0 {
			return false // invalid condition never matches, even negated
		}
		has := itemHasTag(item, id)
		if c.Not {
			return !has
		}
		return has
	}
	return false
}

// matchProfileRule mirrors purgebot matchRule: split the condition chain into
// AND-groups at each join=="or"; the rule matches when ANY group's conditions
// all match. AND binds tighter than OR.
func matchProfileRule(item ProfileItem, rule ProfileRule) bool {
	if len(rule.Conditions) == 0 {
		return false
	}
	var groups [][]ProfileCondition
	var current []ProfileCondition
	for _, c := range rule.Conditions {
		if len(current) == 0 {
			current = append(current, c)
		} else if strings.EqualFold(strings.TrimSpace(c.Join), "or") {
			groups = append(groups, current)
			current = []ProfileCondition{c}
		} else {
			current = append(current, c)
		}
	}
	if len(current) > 0 {
		groups = append(groups, current)
	}
	for _, g := range groups {
		all := true
		for _, c := range g {
			if !matchProfileCondition(item, c) {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}

// PlanProfileMoves evaluates every item against every rule and returns the
// moves to apply plus the conflicts to surface.
//
//   - 0 matching rules            -> skip (no move).
//   - matched rules agree on one  -> move if it differs from the item's current
//     profile (else no-op, not returned).
//   - matched rules disagree      -> conflict (surfaced, never moved).
//
// Rules with no conditions or a non-positive ProfileID are ignored. Idempotent.
func PlanProfileMoves(items []ProfileItem, rules []ProfileRule) ([]ProfileMove, []ProfileConflict) {
	var moves []ProfileMove
	var conflicts []ProfileConflict

	for _, it := range items {
		var candidates []int            // distinct target profiles, in first-seen order
		ruleByProfile := map[int]int{}  // profileID -> deciding rule index
		for ri, r := range rules {
			if r.ProfileID <= 0 || len(r.Conditions) == 0 {
				continue
			}
			if !matchProfileRule(it, r) {
				continue
			}
			if _, seen := ruleByProfile[r.ProfileID]; !seen {
				ruleByProfile[r.ProfileID] = ri
				candidates = append(candidates, r.ProfileID)
			}
		}

		switch len(candidates) {
		case 0:
			// no rule matched
		case 1:
			target := candidates[0]
			if target != it.CurrentProfileID {
				moves = append(moves, ProfileMove{
					ItemID:           it.ID,
					Title:            it.Title,
					Year:             it.Year,
					FromProfileID:    it.CurrentProfileID,
					ToProfileID:      target,
					MatchedRuleIndex: ruleByProfile[target],
				})
			}
		default:
			conflicts = append(conflicts, ProfileConflict{
				ItemID:              it.ID,
				Title:               it.Title,
				Year:                it.Year,
				CandidateProfileIDs: candidates,
			})
		}
	}
	return moves, conflicts
}
