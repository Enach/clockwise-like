package engine

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/calendar"
	"github.com/Enach/paceday/backend/storage"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	googlecalendar "google.golang.org/api/calendar/v3"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()

	container, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("testdb"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() { container.Terminate(ctx) })

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("get connection string: %v", err)
	}

	db, err := storage.Open(dsn)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// mockCalOps implements calendarOps for tests.
type mockCalOps struct {
	calID       string
	events      []*googlecalendar.Event
	createdEvts []*googlecalendar.Event
	deletedEvts []string
	createErr   error
	listErr     error
}

func (m *mockCalOps) calendarID() string { return m.calID }

func (m *mockCalOps) listEvents(_ context.Context, _, _ time.Time) ([]*googlecalendar.Event, error) {
	return m.events, m.listErr
}

func (m *mockCalOps) createEvent(_ context.Context, ev *googlecalendar.Event) (*googlecalendar.Event, error) {
	if m.createErr != nil {
		return nil, m.createErr
	}
	out := *ev
	out.Id = "created-" + time.Now().Format("150405.000000000")
	m.createdEvts = append(m.createdEvts, &out)
	return &out, nil
}

func (m *mockCalOps) updateEvent(_ context.Context, _ string, ev *googlecalendar.Event) (*googlecalendar.Event, error) {
	return ev, nil
}

func (m *mockCalOps) deleteEvent(_ context.Context, eventID string) error {
	m.deletedEvts = append(m.deletedEvts, eventID)
	return nil
}

func (m *mockCalOps) getEvent(_ context.Context, eventID string) (*googlecalendar.Event, error) {
	for _, ev := range m.events {
		if ev.Id == eventID {
			return ev, nil
		}
	}
	return &googlecalendar.Event{Id: eventID}, nil
}

func (m *mockCalOps) getFreeBusy(_ context.Context, _ []string, _, _ time.Time) (map[string][]calendar.TimeSlot, error) {
	return map[string][]calendar.TimeSlot{}, nil
}

// subtractIntervals tests
func TestSubtractIntervals_NoOverlap(t *testing.T) {
	base := time.Date(2025, 1, 6, 9, 0, 0, 0, time.UTC)
	whole := interval{base, base.Add(8 * time.Hour)}
	free := subtractIntervals(whole, nil)
	if len(free) != 1 {
		t.Fatalf("expected 1 free slot, got %d", len(free))
	}
	if !free[0].start.Equal(whole.start) || !free[0].end.Equal(whole.end) {
		t.Error("free slot should equal whole when no busy")
	}
}

func TestSubtractIntervals_MiddleBusy(t *testing.T) {
	base := time.Date(2025, 1, 6, 9, 0, 0, 0, time.UTC)
	whole := interval{base, base.Add(8 * time.Hour)}
	busy := []interval{{base.Add(2 * time.Hour), base.Add(4 * time.Hour)}}

	free := subtractIntervals(whole, busy)
	if len(free) != 2 {
		t.Fatalf("expected 2 free slots, got %d", len(free))
	}
	if !free[0].end.Equal(busy[0].start) {
		t.Errorf("first free slot should end at busy start")
	}
	if !free[1].start.Equal(busy[0].end) {
		t.Errorf("second free slot should start at busy end")
	}
}

func TestSubtractIntervals_FullyBusy(t *testing.T) {
	base := time.Date(2025, 1, 6, 9, 0, 0, 0, time.UTC)
	whole := interval{base, base.Add(2 * time.Hour)}
	busy := []interval{{base.Add(-time.Hour), base.Add(3 * time.Hour)}}
	free := subtractIntervals(whole, busy)
	if len(free) != 0 {
		t.Errorf("expected 0 free slots, got %d", len(free))
	}
}

func TestSubtractIntervals_NonOverlappingBusy(t *testing.T) {
	base := time.Date(2025, 1, 6, 9, 0, 0, 0, time.UTC)
	whole := interval{base, base.Add(4 * time.Hour)}
	busy := []interval{{base.Add(5 * time.Hour), base.Add(6 * time.Hour)}}
	free := subtractIntervals(whole, busy)
	if len(free) != 1 {
		t.Fatalf("expected 1 free slot, got %d", len(free))
	}
}

func TestMergeIntervals(t *testing.T) {
	base := time.Date(2025, 1, 6, 9, 0, 0, 0, time.UTC)
	ivs := []interval{
		{base.Add(2 * time.Hour), base.Add(4 * time.Hour)},
		{base, base.Add(3 * time.Hour)},
		{base.Add(5 * time.Hour), base.Add(6 * time.Hour)},
	}
	merged := mergeIntervals(ivs)
	if len(merged) != 2 {
		t.Fatalf("expected 2 merged intervals, got %d", len(merged))
	}
	if !merged[0].start.Equal(base) {
		t.Error("first merged should start at base")
	}
	if !merged[0].end.Equal(base.Add(4 * time.Hour)) {
		t.Error("first merged should end at base+4h")
	}
}

func TestMergeIntervals_Empty(t *testing.T) {
	if len(mergeIntervals(nil)) != 0 {
		t.Error("mergeIntervals(nil) should return nil")
	}
}

func TestMergeIntervals_Adjacent(t *testing.T) {
	base := time.Date(2025, 1, 6, 9, 0, 0, 0, time.UTC)
	ivs := []interval{
		{base, base.Add(time.Hour)},
		{base.Add(time.Hour), base.Add(2 * time.Hour)},
	}
	merged := mergeIntervals(ivs)
	if len(merged) != 1 {
		t.Fatalf("adjacent intervals should merge into 1, got %d", len(merged))
	}
}

func TestScoreSlot(t *testing.T) {
	// Use Tuesday (Jan 7) to avoid Monday early-morning penalty
	morning := time.Date(2025, 1, 7, 10, 0, 0, 0, time.UTC) // Tuesday 10am
	evening := time.Date(2025, 1, 7, 17, 0, 0, 0, time.UTC)  // Tuesday 5pm (no bonus)

	mSlot := scoredSlot{iv: interval{morning, morning.Add(time.Hour)}}
	eSlot := scoredSlot{iv: interval{evening, evening.Add(time.Hour)}}

	mScore := scoreSlot(mSlot, morning)
	eScore := scoreSlot(eSlot, evening)

	if mScore <= eScore {
		t.Errorf("morning slot (%d) should score higher than evening (%d)", mScore, eScore)
	}
}

func TestScoreSlot_MondayPenalty(t *testing.T) {
	monday8am := time.Date(2025, 1, 6, 8, 0, 0, 0, time.UTC)
	monday11am := time.Date(2025, 1, 6, 11, 0, 0, 0, time.UTC)

	earlSlot := scoredSlot{iv: interval{monday8am, monday8am.Add(time.Hour)}}
	lateSlot := scoredSlot{iv: interval{monday11am, monday11am.Add(time.Hour)}}

	earlyScore := scoreSlot(earlSlot, monday8am)
	lateScore := scoreSlot(lateSlot, monday11am)

	if earlyScore >= lateScore {
		t.Errorf("early Monday (%d) should score lower than late Monday (%d)", earlyScore, lateScore)
	}
}

func TestParseHHMM(t *testing.T) {
	day := time.Date(2025, 1, 6, 0, 0, 0, 0, time.UTC)
	t1 := parseHHMM("09:30", day, time.UTC)
	if t1.Hour() != 9 || t1.Minute() != 30 {
		t.Errorf("expected 09:30, got %v", t1)
	}

	t2 := parseHHMM("18:00", day, time.UTC)
	if t2.Hour() != 18 || t2.Minute() != 0 {
		t.Errorf("expected 18:00, got %v", t2)
	}
}

func TestStartOfWeek(t *testing.T) {
	wednesday := time.Date(2025, 1, 8, 15, 30, 0, 0, time.UTC)
	monday := startOfWeek(wednesday)
	if monday.Weekday() != time.Monday {
		t.Errorf("startOfWeek returned %v, want Monday", monday.Weekday())
	}
	if monday.Hour() != 0 || monday.Minute() != 0 {
		t.Error("startOfWeek should return midnight")
	}
}

func TestStartOfWeek_Sunday(t *testing.T) {
	sunday := time.Date(2025, 1, 12, 10, 0, 0, 0, time.UTC)
	monday := startOfWeek(sunday)
	if monday.Weekday() != time.Monday {
		t.Errorf("startOfWeek(Sunday) returned %v, want Monday", monday.Weekday())
	}
}

func TestBuildBusy(t *testing.T) {
	base := time.Date(2025, 1, 6, 0, 0, 0, 0, time.UTC)
	workStart := base.Add(9 * time.Hour)
	workEnd := base.Add(17 * time.Hour)

	events := []*googlecalendar.Event{
		{
			Start:        &googlecalendar.EventDateTime{DateTime: workStart.Add(time.Hour).Format(time.RFC3339)},
			End:          &googlecalendar.EventDateTime{DateTime: workStart.Add(2 * time.Hour).Format(time.RFC3339)},
			Transparency: "",
		},
		{
			Start:        &googlecalendar.EventDateTime{DateTime: workStart.Format(time.RFC3339)},
			End:          &googlecalendar.EventDateTime{DateTime: workStart.Add(30 * time.Minute).Format(time.RFC3339)},
			Transparency: "transparent",
		},
	}

	s := &storage.Settings{ProtectLunch: false}
	busy := buildBusy(events, workStart, workEnd, s, base, time.UTC)
	if len(busy) != 1 {
		t.Fatalf("expected 1 busy interval (transparent skipped), got %d", len(busy))
	}
}

func TestBuildBusy_LunchProtected(t *testing.T) {
	base := time.Date(2025, 1, 6, 0, 0, 0, 0, time.UTC)
	workStart := base.Add(9 * time.Hour)
	workEnd := base.Add(17 * time.Hour)

	s := &storage.Settings{
		ProtectLunch: true,
		LunchStart:   "12:00",
		LunchEnd:     "13:00",
	}
	busy := buildBusy(nil, workStart, workEnd, s, base, time.UTC)
	if len(busy) != 1 {
		t.Fatalf("expected 1 busy (lunch), got %d", len(busy))
	}
}

func TestBuildBusy_EventBeforeWork(t *testing.T) {
	base := time.Date(2025, 1, 6, 0, 0, 0, 0, time.UTC)
	workStart := base.Add(9 * time.Hour)
	workEnd := base.Add(17 * time.Hour)

	events := []*googlecalendar.Event{
		{
			Start: &googlecalendar.EventDateTime{DateTime: base.Add(6 * time.Hour).Format(time.RFC3339)},
			End:   &googlecalendar.EventDateTime{DateTime: base.Add(7 * time.Hour).Format(time.RFC3339)},
		},
	}

	s := &storage.Settings{ProtectLunch: false}
	busy := buildBusy(events, workStart, workEnd, s, base, time.UTC)
	if len(busy) != 0 {
		t.Errorf("event before work hours should not be in busy, got %d", len(busy))
	}
}

func TestColorIDFromHex(t *testing.T) {
	if colorIDFromHex("#FF0000") != "7" {
		t.Error("colorIDFromHex should return 7")
	}
}

func TestFocusRun_WithMock(t *testing.T) {
	db := openTestDB(t)

	mock := &mockCalOps{
		calID:  "primary",
		events: []*googlecalendar.Event{},
	}

	eng := &FocusTimeEngine{
		DB:     db,
		calOps: mock,
	}

	monday := time.Date(2025, 1, 6, 0, 0, 0, 0, time.UTC)
	result, err := eng.Run(context.Background(), monday)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result == nil {
		t.Fatal("result should not be nil")
	}
}

func TestClearWeek_WithMock(t *testing.T) {
	db := openTestDB(t)

	start := time.Date(2025, 1, 6, 9, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	storage.SaveFocusBlock(db, "evt-to-delete", "2025-01-06", start, end)

	mock := &mockCalOps{calID: "primary"}
	eng := &FocusTimeEngine{DB: db, calOps: mock}

	n, err := eng.ClearWeek(context.Background(), time.Date(2025, 1, 6, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("ClearWeek: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 block cleared, got %d", n)
	}
	if len(mock.deletedEvts) != 1 || mock.deletedEvts[0] != "evt-to-delete" {
		t.Errorf("expected event evt-to-delete deleted, got %v", mock.deletedEvts)
	}
}

func TestSortIntervals(t *testing.T) {
	base := time.Date(2025, 1, 6, 0, 0, 0, 0, time.UTC)
	ivs := []interval{
		{base.Add(3 * time.Hour), base.Add(4 * time.Hour)},
		{base.Add(time.Hour), base.Add(2 * time.Hour)},
		{base, base.Add(30 * time.Minute)},
	}
	sortIntervals(ivs)
	if !ivs[0].start.Equal(base) {
		t.Errorf("after sort, first interval should start at base, got %v", ivs[0].start)
	}
}

func TestSortByScore(t *testing.T) {
	slots := []scoredSlot{{score: 10}, {score: 30}, {score: 20}}
	sortByScore(slots)
	if slots[0].score != 30 {
		t.Errorf("expected highest score first, got %d", slots[0].score)
	}
}
