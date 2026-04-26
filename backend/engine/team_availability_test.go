package engine

import (
	"testing"
	"time"
)

func TestTeamQualityScore_NoMembers(t *testing.T) {
	slot := interval{
		start: time.Date(2024, 1, 2, 10, 0, 0, 0, time.UTC),
		end:   time.Date(2024, 1, 2, 11, 0, 0, 0, time.UTC),
	}
	if got := teamQualityScore(slot, nil); got != 100 {
		t.Errorf("expected 100 for no members, got %d", got)
	}
}

func TestTeamQualityScore_NoFocusBlocks(t *testing.T) {
	slot := interval{
		start: time.Date(2024, 1, 2, 10, 0, 0, 0, time.UTC),
		end:   time.Date(2024, 1, 2, 11, 0, 0, 0, time.UTC),
	}
	members := []memberBusyData{
		{busy: nil, focus: nil},
		{busy: nil, focus: nil},
	}
	if got := teamQualityScore(slot, members); got != 100 {
		t.Errorf("expected 100 when no focus blocks, got %d", got)
	}
}

func TestTeamQualityScore_AllDisrupted(t *testing.T) {
	slot := interval{
		start: time.Date(2024, 1, 2, 10, 0, 0, 0, time.UTC),
		end:   time.Date(2024, 1, 2, 11, 0, 0, 0, time.UTC),
	}
	focusBlock := interval{
		start: time.Date(2024, 1, 2, 9, 30, 0, 0, time.UTC),
		end:   time.Date(2024, 1, 2, 11, 30, 0, 0, time.UTC),
	}
	members := []memberBusyData{
		{focus: []interval{focusBlock}},
		{focus: []interval{focusBlock}},
	}
	if got := teamQualityScore(slot, members); got != 0 {
		t.Errorf("expected 0 when all disrupted, got %d", got)
	}
}

func TestTeamQualityScore_HalfDisrupted(t *testing.T) {
	slot := interval{
		start: time.Date(2024, 1, 2, 10, 0, 0, 0, time.UTC),
		end:   time.Date(2024, 1, 2, 11, 0, 0, 0, time.UTC),
	}
	focusBlock := interval{
		start: time.Date(2024, 1, 2, 9, 0, 0, 0, time.UTC),
		end:   time.Date(2024, 1, 2, 11, 30, 0, 0, time.UTC),
	}
	members := []memberBusyData{
		{focus: []interval{focusBlock}}, // disrupted
		{focus: nil},                    // not disrupted
	}
	got := teamQualityScore(slot, members)
	// 1 disrupted out of 2 → 100 - 50 = 50
	if got != 50 {
		t.Errorf("expected 50 for half-disrupted, got %d", got)
	}
}

func TestTeamQualityScore_NoOverlap(t *testing.T) {
	slot := interval{
		start: time.Date(2024, 1, 2, 14, 0, 0, 0, time.UTC),
		end:   time.Date(2024, 1, 2, 15, 0, 0, 0, time.UTC),
	}
	focusBlock := interval{
		start: time.Date(2024, 1, 2, 10, 0, 0, 0, time.UTC),
		end:   time.Date(2024, 1, 2, 12, 0, 0, 0, time.UTC),
	}
	members := []memberBusyData{
		{focus: []interval{focusBlock}},
	}
	if got := teamQualityScore(slot, members); got != 100 {
		t.Errorf("expected 100 when slot doesn't overlap focus, got %d", got)
	}
}

// TestTeamQualityScore_SingleMemberNoFocus ensures 100 when single member has no focus blocks.
func TestTeamQualityScore_SingleMemberNoFocus(t *testing.T) {
	slot := interval{
		start: time.Date(2024, 1, 2, 10, 0, 0, 0, time.UTC),
		end:   time.Date(2024, 1, 2, 11, 0, 0, 0, time.UTC),
	}
	members := []memberBusyData{{}}
	if got := teamQualityScore(slot, members); got != 100 {
		t.Errorf("expected 100, got %d", got)
	}
}

// TestTeamQualityScore_MultipleBlocksOneMember verifies only one disruption counted per member.
func TestTeamQualityScore_MultipleBlocksOneMember(t *testing.T) {
	slot := interval{
		start: time.Date(2024, 1, 2, 10, 0, 0, 0, time.UTC),
		end:   time.Date(2024, 1, 2, 11, 0, 0, 0, time.UTC),
	}
	// Member has two overlapping focus blocks — should still count as 1 disrupted member.
	fb1 := interval{time.Date(2024, 1, 2, 9, 0, 0, 0, time.UTC), time.Date(2024, 1, 2, 10, 30, 0, 0, time.UTC)}
	fb2 := interval{time.Date(2024, 1, 2, 10, 15, 0, 0, time.UTC), time.Date(2024, 1, 2, 11, 30, 0, 0, time.UTC)}
	members := []memberBusyData{
		{focus: []interval{fb1, fb2}},
	}
	got := teamQualityScore(slot, members)
	if got != 0 {
		t.Errorf("expected 0 (100%% disrupted), got %d", got)
	}
}
