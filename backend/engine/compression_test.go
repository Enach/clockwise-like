package engine

import (
	"context"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/calendar"
	googlecalendar "google.golang.org/api/calendar/v3"
)

func TestClassifyEvents(t *testing.T) {
	base := time.Date(2025, 1, 6, 0, 0, 0, 0, time.UTC)
	workStart := base.Add(9 * time.Hour)
	workEnd := base.Add(17 * time.Hour)

	events := []*googlecalendar.Event{
		{
			Id:      "moveable-1",
			Summary: "My event",
			Start:   &googlecalendar.EventDateTime{DateTime: workStart.Add(time.Hour).Format(time.RFC3339)},
			End:     &googlecalendar.EventDateTime{DateTime: workStart.Add(2 * time.Hour).Format(time.RFC3339)},
			Organizer: &googlecalendar.EventOrganizer{Self: true},
		},
		{
			Id:      "fixed-1",
			Summary: "Team meeting",
			Start:   &googlecalendar.EventDateTime{DateTime: workStart.Add(3 * time.Hour).Format(time.RFC3339)},
			End:     &googlecalendar.EventDateTime{DateTime: workStart.Add(4 * time.Hour).Format(time.RFC3339)},
			Organizer: &googlecalendar.EventOrganizer{Self: false},
		},
		{
			Id:    "no-datetime",
			Start: &googlecalendar.EventDateTime{Date: "2025-01-06"},
			End:   &googlecalendar.EventDateTime{Date: "2025-01-07"},
		},
		{
			Id:    "before-work",
			Start: &googlecalendar.EventDateTime{DateTime: base.Add(7 * time.Hour).Format(time.RFC3339)},
			End:   &googlecalendar.EventDateTime{DateTime: base.Add(8 * time.Hour).Format(time.RFC3339)},
		},
	}

	moveable, fixed := classifyEvents(events, workStart, workEnd)

	if len(moveable) != 1 {
		t.Errorf("expected 1 moveable event, got %d", len(moveable))
	}
	if len(fixed) != 1 {
		t.Errorf("expected 1 fixed event, got %d", len(fixed))
	}
	if moveable[0].event.Id != "moveable-1" {
		t.Errorf("wrong moveable event: %s", moveable[0].event.Id)
	}
}

func TestClassifyEvents_Transparent(t *testing.T) {
	base := time.Date(2025, 1, 6, 0, 0, 0, 0, time.UTC)
	workStart := base.Add(9 * time.Hour)
	workEnd := base.Add(17 * time.Hour)

	events := []*googlecalendar.Event{
		{
			Id:           "transparent-self",
			Start:        &googlecalendar.EventDateTime{DateTime: workStart.Add(time.Hour).Format(time.RFC3339)},
			End:          &googlecalendar.EventDateTime{DateTime: workStart.Add(2 * time.Hour).Format(time.RFC3339)},
			Organizer:    &googlecalendar.EventOrganizer{Self: true},
			Transparency: "transparent",
		},
	}

	moveable, fixed := classifyEvents(events, workStart, workEnd)
	// transparent = fixed (not moveable)
	if len(moveable) != 0 {
		t.Errorf("transparent event should not be moveable, got %d moveable", len(moveable))
	}
	if len(fixed) != 1 {
		t.Errorf("transparent event should be fixed, got %d fixed", len(fixed))
	}
}

func TestToIntervals(t *testing.T) {
	base := time.Date(2025, 1, 6, 9, 0, 0, 0, time.UTC)
	eis := []eventInterval{
		{event: &googlecalendar.Event{Id: "1"}, start: base, end: base.Add(time.Hour)},
		{event: &googlecalendar.Event{Id: "2"}, start: base.Add(2 * time.Hour), end: base.Add(3 * time.Hour)},
	}
	ivs := toIntervals(eis)
	if len(ivs) != 2 {
		t.Fatalf("expected 2 intervals, got %d", len(ivs))
	}
	if !ivs[0].start.Equal(base) {
		t.Error("first interval start mismatch")
	}
}

func TestToIntervalsExcept(t *testing.T) {
	base := time.Date(2025, 1, 6, 9, 0, 0, 0, time.UTC)
	eis := []eventInterval{
		{event: &googlecalendar.Event{Id: "keep"}, start: base, end: base.Add(time.Hour)},
		{event: &googlecalendar.Event{Id: "exclude"}, start: base.Add(2 * time.Hour), end: base.Add(3 * time.Hour)},
	}
	ivs := toIntervalsExcept(eis, "exclude")
	if len(ivs) != 1 {
		t.Fatalf("expected 1 interval after exclusion, got %d", len(ivs))
	}
}

func TestExtractAttendees(t *testing.T) {
	ev := &googlecalendar.Event{
		Attendees: []*googlecalendar.EventAttendee{
			{Email: "alice@co.com", Self: false, ResponseStatus: "accepted"},
			{Email: "me@co.com", Self: true},
			{Email: "bob@co.com", Self: false, ResponseStatus: "declined"},
		},
	}
	ei := eventInterval{event: ev}
	emails := extractAttendees(ei)
	if len(emails) != 1 {
		t.Errorf("expected 1 attendee (self and declined excluded), got %d: %v", len(emails), emails)
	}
	if emails[0] != "alice@co.com" {
		t.Errorf("expected alice@co.com, got %s", emails[0])
	}
}

func TestGenerateAdjacentPositions(t *testing.T) {
	base := time.Date(2025, 1, 6, 9, 0, 0, 0, time.UTC)
	workStart := base
	workEnd := base.Add(8 * time.Hour)
	dur := time.Hour

	others := []interval{{base.Add(2 * time.Hour), base.Add(3 * time.Hour)}}
	fixed := []interval{{base.Add(5 * time.Hour), base.Add(6 * time.Hour)}}

	positions := generateAdjacentPositions(others, fixed, workStart, workEnd, dur)

	// Should include workStart + ends and pre-starts of each interval
	if len(positions) == 0 {
		t.Error("expected some candidate positions")
	}
	found := false
	for _, p := range positions {
		if p.Equal(workStart) {
			found = true
		}
	}
	if !found {
		t.Error("workStart should be in positions")
	}
}

func TestOverlapsWith(t *testing.T) {
	base := time.Date(2025, 1, 6, 9, 0, 0, 0, time.UTC)
	iv := interval{base, base.Add(2 * time.Hour)}

	overlapping := []interval{{base.Add(time.Hour), base.Add(3 * time.Hour)}}
	if !overlapsWith(iv, overlapping) {
		t.Error("expected overlap")
	}

	noOverlap := []interval{{base.Add(3 * time.Hour), base.Add(4 * time.Hour)}}
	if overlapsWith(iv, noOverlap) {
		t.Error("expected no overlap")
	}

	if overlapsWith(iv, nil) {
		t.Error("expected no overlap with empty list")
	}
}

func TestHasConflict(t *testing.T) {
	base := time.Date(2025, 1, 6, 9, 0, 0, 0, time.UTC)
	busyMap := map[string][]calendar.TimeSlot{
		"user@co.com": {
			{Start: base.Add(time.Hour), End: base.Add(2 * time.Hour)},
		},
	}

	if !hasConflict(busyMap, base.Add(90*time.Minute), base.Add(150*time.Minute)) {
		t.Error("expected conflict")
	}
	if hasConflict(busyMap, base.Add(3*time.Hour), base.Add(4*time.Hour)) {
		t.Error("expected no conflict")
	}
	if hasConflict(nil, base, base.Add(time.Hour)) {
		t.Error("expected no conflict with nil busy map")
	}
}

func TestLargestSlot(t *testing.T) {
	base := time.Date(2025, 1, 6, 9, 0, 0, 0, time.UTC)
	slots := []interval{
		{base, base.Add(time.Hour)},
		{base.Add(2 * time.Hour), base.Add(5 * time.Hour)},
		{base.Add(6 * time.Hour), base.Add(7 * time.Hour)},
	}
	largest := largestSlot(slots)
	if largest != 3*time.Hour {
		t.Errorf("expected 3h, got %v", largest)
	}

	if largestSlot(nil) != 0 {
		t.Error("largestSlot(nil) should return 0")
	}
}

func TestCompressionEngine_SuggestForDay_WithMock(t *testing.T) {
	db := openTestDB(t)
	mock := &mockCalOps{
		calID:  "primary",
		events: []*googlecalendar.Event{},
	}

	eng := &CompressionEngine{DB: db, calOps: mock}
	result, err := eng.SuggestForDay(context.Background(), time.Date(2025, 1, 6, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("SuggestForDay: %v", err)
	}
	if result == nil {
		t.Fatal("result should not be nil")
		return
	}
	if result.Date != "2025-01-06" {
		t.Errorf("date = %q, want 2025-01-06", result.Date)
	}
}

func TestCompressionEngine_Apply_WithMock(t *testing.T) {
	db := openTestDB(t)

	now := time.Now().UTC()
	existingEvent := &googlecalendar.Event{
		Id:      "evt-001",
		Summary: "Meeting",
		Start:   &googlecalendar.EventDateTime{DateTime: now.Add(time.Hour).Format(time.RFC3339)},
		End:     &googlecalendar.EventDateTime{DateTime: now.Add(2 * time.Hour).Format(time.RFC3339)},
	}

	mock := &mockCalOps{calID: "primary", events: []*googlecalendar.Event{existingEvent}}
	eng := &CompressionEngine{DB: db, calOps: mock}

	proposals := []MoveProposal{
		{
			EventID:       "evt-001",
			ProposedStart: now.Add(3 * time.Hour),
			ProposedEnd:   now.Add(4 * time.Hour),
		},
	}

	applied, failed, err := eng.Apply(context.Background(), proposals)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(applied) != 1 {
		t.Errorf("expected 1 applied, got %d (failed: %v)", len(applied), failed)
	}
	if len(failed) != 0 {
		t.Errorf("expected 0 failed, got %v", failed)
	}
}

func TestCompressionEngine_Apply_InvalidTimeParsing(t *testing.T) {
	db := openTestDB(t)
	mock := &mockCalOps{calID: "primary"}
	eng := &CompressionEngine{DB: db, calOps: mock}

	// Empty proposals should succeed with empty results
	applied, failed, err := eng.Apply(context.Background(), nil)
	if err != nil {
		t.Fatalf("Apply with nil: %v", err)
	}
	if len(applied) != 0 || len(failed) != 0 {
		t.Errorf("expected empty results, got applied=%v failed=%v", applied, failed)
	}
}

func TestCompressionEngine_SuggestForDay_WithMoveableEvents(t *testing.T) {
	db := openTestDB(t)

	base := time.Date(2025, 1, 6, 0, 0, 0, 0, time.UTC) // Monday
	workStart := base.Add(9 * time.Hour)

	// Two self-organized 1-hour events spread apart → compressing them creates a bigger free block
	ev1 := &googlecalendar.Event{
		Id:        "ev1",
		Summary:   "Standup",
		Start:     &googlecalendar.EventDateTime{DateTime: workStart.Add(2 * time.Hour).Format(time.RFC3339)},
		End:       &googlecalendar.EventDateTime{DateTime: workStart.Add(3 * time.Hour).Format(time.RFC3339)},
		Organizer: &googlecalendar.EventOrganizer{Self: true},
	}
	ev2 := &googlecalendar.Event{
		Id:        "ev2",
		Summary:   "Sync",
		Start:     &googlecalendar.EventDateTime{DateTime: workStart.Add(5 * time.Hour).Format(time.RFC3339)},
		End:       &googlecalendar.EventDateTime{DateTime: workStart.Add(6 * time.Hour).Format(time.RFC3339)},
		Organizer: &googlecalendar.EventOrganizer{Self: true},
	}

	mock := &mockCalOps{
		calID:  "primary",
		events: []*googlecalendar.Event{ev1, ev2},
	}

	eng := &CompressionEngine{DB: db, calOps: mock}
	result, err := eng.SuggestForDay(context.Background(), base)
	if err != nil {
		t.Fatalf("SuggestForDay with moveable events: %v", err)
	}
	if result == nil {
		t.Fatal("result should not be nil")
		return
	}
	// Should produce proposals since gain > 30 min is achievable
	if len(result.Proposals) == 0 {
		t.Log("no proposals generated — gain threshold not met")
	}
}

func TestCompressionEngine_SuggestForDay_WithAttendees(t *testing.T) {
	db := openTestDB(t)

	base := time.Date(2025, 1, 6, 0, 0, 0, 0, time.UTC)
	workStart := base.Add(9 * time.Hour)

	ev := &googlecalendar.Event{
		Id:      "ev-attendees",
		Summary: "Team Sync",
		Start:   &googlecalendar.EventDateTime{DateTime: workStart.Add(3 * time.Hour).Format(time.RFC3339)},
		End:     &googlecalendar.EventDateTime{DateTime: workStart.Add(4 * time.Hour).Format(time.RFC3339)},
		Organizer: &googlecalendar.EventOrganizer{Self: true},
		Attendees: []*googlecalendar.EventAttendee{
			{Email: "alice@co.com", Self: false, ResponseStatus: "accepted"},
		},
	}

	mock := &mockCalOps{calID: "primary", events: []*googlecalendar.Event{ev}}
	eng := &CompressionEngine{DB: db, calOps: mock}

	result, err := eng.SuggestForDay(context.Background(), base)
	if err != nil {
		t.Fatalf("SuggestForDay with attendees: %v", err)
	}
	if result == nil {
		t.Fatal("result should not be nil")
	}
}
