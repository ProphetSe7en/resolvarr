package engine

import "testing"

// tag-condition shorthand
func tagCond(id, join string) ProfileCondition {
	return ProfileCondition{Type: "tag", Value: id, Join: join}
}

func TestMatchProfileRule_SingleTag(t *testing.T) {
	rule := ProfileRule{Conditions: []ProfileCondition{tagCond("5", "")}, ProfileID: 2}
	if !matchProfileRule(ProfileItem{Tags: []int{5}}, rule) {
		t.Error("item with tag 5 should match single-tag rule")
	}
	if matchProfileRule(ProfileItem{Tags: []int{9}}, rule) {
		t.Error("item without tag 5 should not match")
	}
}

func TestMatchProfileRule_AND(t *testing.T) {
	// tag 5 AND tag 6
	rule := ProfileRule{Conditions: []ProfileCondition{tagCond("5", ""), tagCond("6", "and")}, ProfileID: 2}
	if !matchProfileRule(ProfileItem{Tags: []int{5, 6}}, rule) {
		t.Error("both tags present -> match")
	}
	if matchProfileRule(ProfileItem{Tags: []int{5}}, rule) {
		t.Error("only one tag -> no match for AND")
	}
}

func TestMatchProfileRule_OR(t *testing.T) {
	// tag 5 OR tag 6
	rule := ProfileRule{Conditions: []ProfileCondition{tagCond("5", ""), tagCond("6", "or")}, ProfileID: 2}
	if !matchProfileRule(ProfileItem{Tags: []int{6}}, rule) {
		t.Error("either tag present -> match")
	}
	if matchProfileRule(ProfileItem{Tags: []int{7}}, rule) {
		t.Error("neither tag -> no match")
	}
}

func TestMatchProfileRule_Precedence_A_AND_B_OR_C(t *testing.T) {
	// (5 AND 6) OR 7
	rule := ProfileRule{Conditions: []ProfileCondition{
		tagCond("5", ""), tagCond("6", "and"), tagCond("7", "or"),
	}, ProfileID: 2}
	if !matchProfileRule(ProfileItem{Tags: []int{5, 6}}, rule) {
		t.Error("first AND-group satisfied -> match")
	}
	if !matchProfileRule(ProfileItem{Tags: []int{7}}, rule) {
		t.Error("second group (just 7) satisfied -> match")
	}
	if matchProfileRule(ProfileItem{Tags: []int{5}}, rule) {
		t.Error("only 5 (need 5 AND 6, or 7) -> no match")
	}
	if matchProfileRule(ProfileItem{Tags: []int{6}}, rule) {
		t.Error("only 6 -> no match")
	}
}

func TestMatchProfileRule_BadValueAndEmpty(t *testing.T) {
	if matchProfileRule(ProfileItem{Tags: []int{5}}, ProfileRule{ProfileID: 2}) {
		t.Error("no conditions -> no match")
	}
	bad := ProfileRule{Conditions: []ProfileCondition{{Type: "tag", Value: "notanint"}}, ProfileID: 2}
	if matchProfileRule(ProfileItem{Tags: []int{5}}, bad) {
		t.Error("unparseable tag value -> no match")
	}
}

func TestPlanProfileMoves_MoveAndNoOp(t *testing.T) {
	rules := []ProfileRule{{Conditions: []ProfileCondition{tagCond("5", "")}, ProfileID: 2}}
	items := []ProfileItem{
		{ID: 1, Title: "A", Tags: []int{5}, CurrentProfileID: 1}, // 1 -> 2 move
		{ID: 2, Title: "B", Tags: []int{5}, CurrentProfileID: 2}, // already on 2 -> no-op
		{ID: 3, Title: "C", Tags: []int{9}, CurrentProfileID: 1}, // no rule -> skip
	}
	moves, conflicts := PlanProfileMoves(items, rules)
	if len(conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %+v", conflicts)
	}
	if len(moves) != 1 || moves[0].ItemID != 1 || moves[0].ToProfileID != 2 || moves[0].FromProfileID != 1 {
		t.Fatalf("want one move 1->2 for item 1, got %+v", moves)
	}
}

func TestPlanProfileMoves_RulesAgree_SingleMove(t *testing.T) {
	// Two rules both targeting profile 2 -> agreement, not a conflict.
	rules := []ProfileRule{
		{Conditions: []ProfileCondition{tagCond("5", "")}, ProfileID: 2},
		{Conditions: []ProfileCondition{tagCond("6", "")}, ProfileID: 2},
	}
	items := []ProfileItem{{ID: 1, Tags: []int{5, 6}, CurrentProfileID: 1}}
	moves, conflicts := PlanProfileMoves(items, rules)
	if len(conflicts) != 0 || len(moves) != 1 || moves[0].ToProfileID != 2 {
		t.Fatalf("agreeing rules should yield one move, got moves=%+v conflicts=%+v", moves, conflicts)
	}
}

func TestPlanProfileMoves_Conflict(t *testing.T) {
	// Item matches two rules with DIFFERENT target profiles -> conflict, no move.
	rules := []ProfileRule{
		{Conditions: []ProfileCondition{tagCond("5", "")}, ProfileID: 2},
		{Conditions: []ProfileCondition{tagCond("6", "")}, ProfileID: 3},
	}
	items := []ProfileItem{{ID: 1, Title: "X", Tags: []int{5, 6}, CurrentProfileID: 1}}
	moves, conflicts := PlanProfileMoves(items, rules)
	if len(moves) != 0 {
		t.Fatalf("conflict must not move: %+v", moves)
	}
	if len(conflicts) != 1 || len(conflicts[0].CandidateProfileIDs) != 2 {
		t.Fatalf("want one conflict with 2 candidates, got %+v", conflicts)
	}
}

func TestPlanProfileMoves_IgnoresInvalidRules(t *testing.T) {
	rules := []ProfileRule{
		{Conditions: []ProfileCondition{tagCond("5", "")}, ProfileID: 0},  // bad profile
		{Conditions: nil, ProfileID: 2},                                   // no conditions
		{Conditions: []ProfileCondition{tagCond("5", "")}, ProfileID: 4},  // valid
	}
	items := []ProfileItem{{ID: 1, Tags: []int{5}, CurrentProfileID: 1}}
	moves, conflicts := PlanProfileMoves(items, rules)
	if len(conflicts) != 0 || len(moves) != 1 || moves[0].ToProfileID != 4 {
		t.Fatalf("only the valid rule should apply: moves=%+v conflicts=%+v", moves, conflicts)
	}
}
