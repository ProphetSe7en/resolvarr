package core

import "testing"

func TestAppendUnmatched_RecordsItemAndCount(t *testing.T) {
	r := &PlexLabelRuleRun{}
	r.AppendUnmatched(PlexUnmatchedItem{Title: "Berserk", Year: 1997, Side: "plex", Reason: "no shared ID"})
	if r.Unmatched != 1 {
		t.Errorf("Unmatched = %d, want 1", r.Unmatched)
	}
	if len(r.UnmatchedItems) != 1 || r.UnmatchedItems[0].Title != "Berserk" {
		t.Errorf("UnmatchedItems = %+v, want one Berserk entry", r.UnmatchedItems)
	}
}

func TestAppendUnmatched_CountClimbsPastCapButListBounded(t *testing.T) {
	r := &PlexLabelRuleRun{}
	total := PlexLabelRunUnmatchedCap + 25
	for i := 0; i < total; i++ {
		r.AppendUnmatched(PlexUnmatchedItem{Title: "x", Side: "plex"})
	}
	if r.Unmatched != total {
		t.Errorf("Unmatched = %d, want %d (count must stay accurate past the cap)", r.Unmatched, total)
	}
	if len(r.UnmatchedItems) != PlexLabelRunUnmatchedCap {
		t.Errorf("UnmatchedItems length = %d, want %d (list must be capped)", len(r.UnmatchedItems), PlexLabelRunUnmatchedCap)
	}
}
