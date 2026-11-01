package api

import (
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/api/gen"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

// Unit tests of the PAC-72 revision 2 rule helpers; no database.

func TestLinkLocation(t *testing.T) {
	boom := errors.New("read failed")
	for _, tc := range []struct {
		name  string
		tz    string
		found bool
		err   error
		want  string
	}{
		{"no settings row", "", false, nil, "UTC"},
		{"empty name", "", true, nil, "UTC"},
		{"unknown name", "Mars/Olympus_Mons", true, nil, "UTC"},
		{"Local never means the process zone", "Local", true, nil, "UTC"},
		{"a real zone", "Asia/Tokyo", true, nil, "Asia/Tokyo"},
		{"a failed read", "", false, boom, ""},
	} {
		read := func(storage.DBTX, uuid.UUID) (string, bool, error) { return tc.tz, tc.found, tc.err }
		loc, err := linkLocation(nil, read, uuid.New())
		if tc.err != nil {
			if !errors.Is(err, boom) || loc != nil {
				t.Errorf("%s: %v %v, want the error and no zone", tc.name, loc, err)
			}
			continue
		}
		if err != nil || loc.String() != tc.want {
			t.Errorf("%s: %v %v, want %s", tc.name, loc, err, tc.want)
		}
	}
}

func TestCalendarDaysAndHorizon(t *testing.T) {
	tokyo, _ := time.LoadLocation("Asia/Tokyo")
	instant := time.Date(2030, 1, 7, 20, 0, 0, 0, time.UTC) // 2030-01-08 05:00 JST
	if d := calendarDay(instant, tokyo); d.Format(time.DateOnly) != "2030-01-08" || d.Location() != tokyo || d.Hour() != 12 {
		t.Errorf("calendarDay = %v", d)
	}
	if d, err := parseCalendarDay("2030-01-08", tokyo); err != nil || !d.Equal(time.Date(2030, 1, 8, 3, 0, 0, 0, time.UTC)) {
		t.Errorf("parseCalendarDay = %v %v", d, err)
	}
	if _, err := parseCalendarDay("08/01/2030", tokyo); err == nil {
		t.Errorf("a malformed date parsed")
	}
	a := time.Date(2030, 3, 30, 12, 0, 0, 0, time.UTC)
	for b, want := range map[time.Time]int{
		time.Date(2030, 3, 31, 23, 0, 0, 0, time.UTC): 1,
		time.Date(2030, 3, 29, 0, 0, 0, 0, time.UTC):  -1,
		time.Date(2031, 3, 30, 0, 0, 0, 0, time.UTC):  365,
	} {
		if got := daysBetween(a, b); got != want {
			t.Errorf("daysBetween(%v, %v) = %d, want %d", a, b, got, want)
		}
	}
	link := &storage.SchedulingLink{}
	if horizonDays(link) != 60 {
		t.Errorf("unset horizon = %d, want 60", horizonDays(link))
	}
	link.BookingHorizonDays = 2
	today := time.Date(2030, 1, 7, 12, 0, 0, 0, time.UTC)
	for offset, want := range map[int]bool{-1: false, 0: true, 1: true, 2: false} {
		if got := inHorizon(link, today, today.AddDate(0, 0, offset)); got != want {
			t.Errorf("inHorizon(+%d) = %v, want %v", offset, got, want)
		}
	}
}

// The cheap checks in precedence order: beyond the horizon, outside the
// link's days/hours/grid, inside the notice; a past day is never beyond the
// horizon.
func TestCheapRefusal(t *testing.T) {
	link := &storage.SchedulingLink{DaysOfWeek: []int{1, 2, 3, 4, 5}, WindowStart: "09:00", WindowEnd: "17:00", MinNoticeMinutes: 120, BookingHorizonDays: 10}
	now := time.Date(2030, 1, 8, 8, 0, 0, 0, time.UTC) // Tuesday 08:00
	at := func(d, hh, mm int) time.Time { return time.Date(2030, 1, d, hh, mm, 0, 0, time.UTC) }
	for _, tc := range []struct {
		name  string
		start time.Time
		want  gen.BookingRefusalReason
	}{
		{"beyond the horizon on a Saturday", at(19, 10, 0), gen.BeyondHorizon},
		{"Saturday", at(12, 10, 0), gen.OutsideLinkHours},
		{"outside the hours and inside the notice", at(8, 8, 30), gen.OutsideLinkHours},
		{"inside the notice", at(8, 9, 30), gen.TooSoon},
		{"yesterday inside the window", at(7, 10, 0), gen.TooSoon},
		{"last Sunday", at(6, 10, 0), gen.OutsideLinkHours},
		{"off the grid", at(9, 10, 5), gen.OutsideLinkHours},
		{"offerable", at(9, 10, 0), ""},
	} {
		got, refused := cheapRefusal(link, time.UTC, tc.start, tc.start.Add(30*time.Minute), 30, now)
		if got != tc.want || refused != (tc.want != "") {
			t.Errorf("%s: %q %v, want %q", tc.name, got, refused, tc.want)
		}
	}
}

func TestWriteRefusalPairs(t *testing.T) {
	for _, setting := range []string{"", storage.RefusalGeneric, storage.RefusalSpecific} {
		for reason, text := range refusalText {
			rec := httptest.NewRecorder()
			writeRefusal(rec, &storage.SchedulingLink{RefusalMessage: setting}, reason)
			want := `{"error":"slot is no longer available"}` + "\n"
			if setting == storage.RefusalSpecific {
				want = `{"error":"` + string(text) + `","reason":"` + string(reason) + `"}` + "\n"
			}
			if rec.Code != 409 || rec.Body.String() != want || rec.Header().Get("Content-Type") != "application/json" {
				t.Errorf("%q/%s: %d %q, want 409 %q", setting, reason, rec.Code, rec.Body.String(), want)
			}
		}
	}
	if len(refusalText) != 5 {
		t.Errorf("%d reasons paired, want the contract's 5", len(refusalText))
	}
}
