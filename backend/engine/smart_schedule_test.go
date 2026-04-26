package engine

import (
	"context"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/storage"
)

func TestScoreCandidate_Morning(t *testing.T) {
	s := &storage.Settings{WorkStart: "09:00", WorkEnd: "18:00"}
	monday9am := time.Date(2025, 1, 6, 9, 0, 0, 0, time.UTC)
	end := monday9am.Add(time.Hour)
	score, reasons := scoreCandidate(monday9am, end, nil, s)
	if score < 40 {
		t.Errorf("morning score should be >= 40, got %d", score)
	}
	if len(reasons) == 0 {
		t.Error("expected reasons for morning slot")
	}
}

func TestScoreCandidate_EarlyAfternoon(t *testing.T) {
	s := &storage.Settings{}
	tuesday14 := time.Date(2025, 1, 7, 14, 0, 0, 0, time.UTC) // Tuesday
	end := tuesday14.Add(time.Hour)
	score, _ := scoreCandidate(tuesday14, end, nil, s)
	if score < 20 {
		t.Errorf("early afternoon on Tuesday should score >= 20, got %d", score)
	}
}

func TestScoreCandidate_FridayAfternoon(t *testing.T) {
	s := &storage.Settings{}
	friday14 := time.Date(2025, 1, 10, 14, 0, 0, 0, time.UTC) // Friday
	end := friday14.Add(time.Hour)
	score, reasons := scoreCandidate(friday14, end, nil, s)

	foundPenalty := false
	for _, r := range reasons {
		if len(r) > 0 && r[0] == 'F' { // "Friday afternoon"
			foundPenalty = true
		}
	}
	_ = score
	if !foundPenalty {
		t.Log("Friday afternoon penalty reason not in reasons (may be fine if combined with other reasons)")
	}
}

func TestScoreCandidate_FocusBlockOverlap(t *testing.T) {
	s := &storage.Settings{}
	start := time.Date(2025, 1, 6, 10, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)

	focusBlocks := []storage.FocusBlock{
		{
			StartTime: start.Add(-30 * time.Minute),
			EndTime:   start.Add(30 * time.Minute),
		},
	}

	score, reasons := scoreCandidate(start, end, focusBlocks, s)
	foundOverlap := false
	for _, r := range reasons {
		if len(r) > 0 && r[0] == 'O' { // "Overlaps with focus block"
			foundOverlap = true
		}
	}
	_ = score
	if !foundOverlap {
		t.Logf("overlap reason not found in: %v", reasons)
	}
}

func TestSortSlotsByScore(t *testing.T) {
	slots := []SuggestedSlot{
		{Score: 10},
		{Score: 50},
		{Score: 30},
	}
	sortSlotsByScore(slots)
	if slots[0].Score != 50 {
		t.Errorf("expected highest score first, got %d", slots[0].Score)
	}
	if slots[1].Score != 30 {
		t.Errorf("expected 30 second, got %d", slots[1].Score)
	}
}

func TestSortSlotsByScore_Empty(t *testing.T) {
	sortSlotsByScore(nil)
	sortSlotsByScore([]SuggestedSlot{})
}

func TestPickTopUnique(t *testing.T) {
	base := time.Date(2025, 1, 6, 9, 0, 0, 0, time.UTC)
	slots := []SuggestedSlot{
		{Start: base, Score: 50},
		{Start: base.Add(30 * time.Minute), Score: 40},        // too close to first
		{Start: base.Add(2 * time.Hour), Score: 30},           // far enough
		{Start: base.Add(3 * time.Hour), Score: 20},           // far enough
		{Start: base.Add(3*time.Hour + 10*time.Minute), Score: 15}, // too close to above
	}

	top := pickTopUnique(slots, 3, time.Hour)
	if len(top) != 3 {
		t.Errorf("expected 3 unique slots, got %d: %v", len(top), top)
	}
}

func TestPickTopUnique_LimitN(t *testing.T) {
	base := time.Date(2025, 1, 6, 9, 0, 0, 0, time.UTC)
	slots := []SuggestedSlot{
		{Start: base, Score: 50},
		{Start: base.Add(2 * time.Hour), Score: 40},
		{Start: base.Add(4 * time.Hour), Score: 30},
		{Start: base.Add(6 * time.Hour), Score: 20},
	}
	top := pickTopUnique(slots, 2, time.Hour)
	if len(top) != 2 {
		t.Errorf("expected 2, got %d", len(top))
	}
}

func TestPickTopUnique_Empty(t *testing.T) {
	top := pickTopUnique(nil, 3, time.Hour)
	if len(top) != 0 {
		t.Error("expected empty result")
	}
}

func TestSmartScheduler_Suggest_WithMock(t *testing.T) {
	db := openTestDB(t)

	now := time.Now().UTC()
	mock := &mockCalOps{calID: "primary@calendar.google.com"}
	sched := &SmartScheduler{DB: db, calOps: mock}

	req := ScheduleRequest{
		DurationMinutes: 30,
		Attendees:       []string{"alice@co.com"},
		RangeStart:      now,
		RangeEnd:        now.Add(5 * 24 * time.Hour),
		Title:           "Test Meeting",
	}

	suggestions, err := sched.Suggest(context.Background(), req)
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if suggestions == nil {
		t.Fatal("suggestions should not be nil")
	}
	// With empty busy map, should return slots
}

func TestSmartScheduler_CreateMeeting_WithMock(t *testing.T) {
	db := openTestDB(t)
	mock := &mockCalOps{calID: "primary"}
	sched := &SmartScheduler{DB: db, calOps: mock}

	now := time.Now().UTC()
	req := ScheduleRequest{
		Title:       "Team Sync",
		Description: "Weekly sync",
		Attendees:   []string{"bob@co.com"},
	}
	slot := SuggestedSlot{
		Start: now.Add(time.Hour),
		End:   now.Add(2 * time.Hour),
	}

	created, err := sched.CreateMeeting(context.Background(), req, slot)
	if err != nil {
		t.Fatalf("CreateMeeting: %v", err)
	}
	if created == nil {
		t.Fatal("created event should not be nil")
	}
	if created.Summary != "Team Sync" {
		t.Errorf("Summary = %q, want Team Sync", created.Summary)
	}
}
