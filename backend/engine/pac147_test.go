package engine

// PAC-147: Suggest scores a slot in the caller's timezone (settings.timezone),
// whatever offset the request carried, and sees the caller's focus blocks over
// the whole requested range, not only the 7 days from range_start. Each test
// fails on the code before the fix.

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/calendar"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

const (
	pac147Morning   = "Morning slot preferred for important meetings"
	pac147Afternoon = "Early afternoon slot"
	pac147Friday    = "Friday afternoon — low energy"
	pac147EarlyWeek = "Earlier in week preferred for planning"
	pac147Focus     = "Overlaps with focus block"
)

// pac147User is a new caller whose settings carry tz; lunch protection is off
// so a midday slot is offered and scored.
func pac147User(t *testing.T, db *sql.DB, tz string) (context.Context, uuid.UUID) {
	t.Helper()
	ctx, user := smartScheduleUser(t, db, "pac147-"+uuid.NewString()+"@example.com")
	s, err := storage.GetOrCreateSettingsByUser(db, user)
	if err != nil {
		t.Fatal(err)
	}
	s.Timezone = tz
	s.ProtectLunch = false
	if err := storage.SaveSettingsForUser(db, user, s); err != nil {
		t.Fatal(err)
	}
	return ctx, user
}

// pac147One asks for exactly one 30-minute slot starting at start (sent in
// UTC, as the web sends toISOString()) and returns it.
func pac147One(t *testing.T, db *sql.DB, ctx context.Context, start time.Time) SuggestedSlot {
	t.Helper()
	start = start.UTC()
	got, err := (&SmartScheduler{DB: db, calOps: &mockCalOps{calID: "primary"}}).Suggest(ctx, ScheduleRequest{
		DurationMinutes: 30, RangeStart: start, RangeEnd: start.Add(30 * time.Minute), Title: "x",
	})
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if len(got.Slots) != 1 || !got.Slots[0].Start.Equal(start) {
		t.Fatalf("slots = %+v, want the one at %s", got.Slots, start)
	}
	return got.Slots[0]
}

func pac147At(t *testing.T, tz, local string) time.Time {
	t.Helper()
	loc, err := time.LoadLocation(tz)
	if err != nil {
		t.Fatal(err)
	}
	at, err := time.ParseInLocation("2006-01-02 15:04", local, loc)
	if err != nil {
		t.Fatal(err)
	}
	return at
}

// The probe cases from the PAC-135 review: every start is sent as Z and
// scored as the caller's local wall clock.
func TestPAC147_SuggestScoresInCallersTimezone(t *testing.T) {
	db := openTestDB(t)
	cases := []struct {
		name, tz, local string
		score           int
		reasons         []string
	}{
		// 08:00Z: was 10, losing the morning +40.
		{"Wednesday 09:00 Paris", "Europe/Paris", "2030-01-09 09:00", 50, []string{pac147Morning, pac147EarlyWeek}},
		// 11:00Z: was 50, wrongly given the morning +40.
		{"Wednesday 12:00 Paris", "Europe/Paris", "2030-01-09 12:00", 10, []string{pac147EarlyWeek}},
		// 15:00Z: was 10 with "Early afternoon slot" and the Friday penalty.
		{"Friday 10:00 New York", "America/New_York", "2030-01-11 10:00", 40, []string{pac147Morning}},
		// 01:00Z: was 10, losing the morning +40.
		{"Monday 10:00 Tokyo", "Asia/Tokyo", "2030-01-07 10:00", 50, []string{pac147Morning, pac147EarlyWeek}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx, _ := pac147User(t, db, c.tz)
			got := pac147One(t, db, ctx, pac147At(t, c.tz, c.local))
			if got.Score != c.score || !reflect.DeepEqual(got.Reasons, c.reasons) {
				t.Errorf("score %d reasons %q, want %d %q", got.Score, got.Reasons, c.score, c.reasons)
			}
		})
	}
}

// A caller with no usable timezone is scored in UTC (settings.timezone
// defaults to UTC; an unknown name falls back to UTC).
func TestPAC147_SuggestUnknownTimezoneScoresInUTC(t *testing.T) {
	db := openTestDB(t)
	ctx, _ := pac147User(t, db, "Mars/Olympus")
	got := pac147One(t, db, ctx, time.Date(2030, 1, 11, 14, 0, 0, 0, time.UTC))
	want := []string{pac147Afternoon, pac147Friday}
	if got.Score != 10 || !reflect.DeepEqual(got.Reasons, want) {
		t.Errorf("score %d reasons %q, want 10 %q", got.Score, got.Reasons, want)
	}
}

// A focus block on day 10 of a 14-day range is penalised: the focus read
// covers the whole range, not the 7 days from range_start.
func TestPAC147_SuggestFocusBlockOnDay10OfA14DayRange(t *testing.T) {
	db := openTestDB(t)
	ctx, user := pac147User(t, db, "UTC")
	rangeStart := time.Date(2030, 1, 7, 0, 0, 0, 0, time.UTC) // Monday
	rangeEnd := rangeStart.AddDate(0, 0, 14)
	slotStart := rangeStart.AddDate(0, 0, 9).Add(10 * time.Hour) // day 10: Wednesday 2030-01-16 10:00Z
	if err := storage.SaveFocusBlock(db, user, "pac147-day10", slotStart.Format(time.DateOnly), slotStart, slotStart.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	// The own calendar is busy everywhere but the one slot, so it is the answer.
	m := &mockCalOps{calID: "primary", fb: map[string]calendar.FreeBusyEntry{"primary": readable(
		calendar.TimeSlot{Start: rangeStart, End: slotStart},
		calendar.TimeSlot{Start: slotStart.Add(30 * time.Minute), End: rangeEnd},
	)}}
	got, err := (&SmartScheduler{DB: db, calOps: m}).Suggest(ctx, ScheduleRequest{
		DurationMinutes: 30, RangeStart: rangeStart, RangeEnd: rangeEnd, Title: "x",
	})
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if len(got.Slots) != 1 || !got.Slots[0].Start.Equal(slotStart) {
		t.Fatalf("slots = %+v, want the one at %s", got.Slots, slotStart)
	}
	want := []string{pac147Morning, pac147Focus, pac147EarlyWeek}
	if s := got.Slots[0]; s.Score != 30 || !reflect.DeepEqual(s.Reasons, want) {
		t.Errorf("score %d reasons %q, want 30 %q", s.Score, s.Reasons, want)
	}
}

// The range read returns blocks that overlap [from, to), whatever their date
// label, and only the owner's.
func TestPAC147_ListFocusBlocksForUserRange(t *testing.T) {
	db := openTestDB(t)
	_, user := smartScheduleUser(t, db, "pac147-range-"+uuid.NewString()+"@example.com")
	_, other := smartScheduleUser(t, db, "pac147-range-other-"+uuid.NewString()+"@example.com")
	from := time.Date(2030, 1, 7, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 14)
	save := func(u uuid.UUID, id string, start time.Time) {
		t.Helper()
		if err := storage.SaveFocusBlock(db, u, id, start.Format(time.DateOnly), start, start.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	save(user, "before", from.Add(-2*time.Hour))            // ends before from
	save(user, "straddles-from", from.Add(-30*time.Minute)) // overlaps from
	save(user, "day10", from.AddDate(0, 0, 9).Add(10*time.Hour))
	save(user, "straddles-to", to.Add(-30*time.Minute))
	save(user, "at-to", to) // starts at to: outside
	save(other, "other-day10", from.AddDate(0, 0, 9).Add(10*time.Hour))

	got, err := storage.ListFocusBlocksForUserRangeCtx(context.Background(), db, user, from, to)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, b := range got {
		ids = append(ids, b.GoogleEventID)
	}
	if want := []string{"straddles-from", "day10", "straddles-to"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("blocks = %v, want %v", ids, want)
	}
}
