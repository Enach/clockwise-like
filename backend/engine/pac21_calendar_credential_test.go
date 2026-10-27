package engine

// PAC-21 (API-054): the engines tell "this user has no calendar connected"
// (ErrCalendarNotConnected, a 401 at the HTTP layer) apart from "the stored
// credential could not be read" (ErrCredentialStore, a 500). Before, both
// were the same opaque error and both became a 500 — or, in analytics, a 500
// whose text named the user id.

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

func pac21ClosedDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("pgx", "postgres://pac21:pac21@127.0.0.1:1/pac21?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	return db
}

func TestPAC21LoadCalendarToken(t *testing.T) {
	db := openTestDB(t)
	_, user := smartScheduleUser(t, db, "pac21-engine@example.com")

	if _, err := LoadCalendarToken(db, user); !errors.Is(err, ErrCalendarNotConnected) {
		t.Fatalf("no stored token: err = %v, want ErrCalendarNotConnected", err)
	}
	if _, err := LoadCalendarToken(db, uuid.Nil); !errors.Is(err, ErrCalendarNotConnected) {
		t.Fatalf("no user: err = %v, want ErrCalendarNotConnected", err)
	}
	_, err := LoadCalendarToken(pac21ClosedDB(t), user)
	if !errors.Is(err, ErrCredentialStore) || errors.Is(err, ErrCalendarNotConnected) {
		t.Fatalf("store failure: err = %v, want ErrCredentialStore and not ErrCalendarNotConnected", err)
	}
	if !strings.Contains(err.Error(), "database is closed") {
		t.Fatalf("store failure must keep the cause for the log: %v", err)
	}
	if err := auth.UpsertUserToken(db, user, &oauth2.Token{AccessToken: "t", Expiry: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if tok, err := LoadCalendarToken(db, user); err != nil || tok == nil || tok.AccessToken != "t" {
		t.Fatalf("stored token: %v %v", tok, err)
	}
}

// Every engine entry point the 18 operations reach maps a missing credential
// to ErrCalendarNotConnected, and never names the user in the error.
func TestPAC21EnginesReportCalendarNotConnected(t *testing.T) {
	db := openTestDB(t)
	ctx, user := smartScheduleUser(t, db, "pac21-engines@example.com")
	cfg := &oauth2.Config{}
	week := time.Date(2030, 1, 7, 0, 0, 0, 0, time.UTC)

	calls := map[string]func() error{
		"newCalOps": func() error { _, err := newCalOps(ctx, db, cfg); return err },
		"FocusTimeEngine.Run": func() error {
			_, err := (&FocusTimeEngine{DB: db, OAuthConfig: cfg}).Run(ctx, week)
			return err
		},
		"FocusTimeEngine.ClearWeek": func() error {
			_, err := (&FocusTimeEngine{DB: db, OAuthConfig: cfg}).ClearWeek(ctx, week)
			return err
		},
		"CompressionEngine.CheckCalendar": func() error { return (&CompressionEngine{DB: db, OAuthConfig: cfg}).CheckCalendar(ctx) },
		"CompressionEngine.SuggestForDay": func() error {
			_, err := (&CompressionEngine{DB: db, OAuthConfig: cfg}).SuggestForDay(ctx, week)
			return err
		},
		"CompressionEngine.Apply": func() error {
			_, _, err := (&CompressionEngine{DB: db, OAuthConfig: cfg}).Apply(ctx, nil)
			return err
		},
		"SmartScheduler.Suggest": func() error {
			_, err := (&SmartScheduler{DB: db, OAuthConfig: cfg}).Suggest(ctx, ScheduleRequest{DurationMinutes: 30, RangeStart: week, RangeEnd: week.Add(8 * time.Hour)})
			return err
		},
		"SmartScheduler.CreateMeeting": func() error {
			_, err := (&SmartScheduler{DB: db, OAuthConfig: cfg}).CreateMeeting(ctx, ScheduleRequest{Title: "T"}, SuggestedSlot{Start: week.Add(10 * time.Hour), End: week.Add(11 * time.Hour)})
			return err
		},
		"AnalyticsEngine.WeekMeetings": func() error {
			_, err := (&AnalyticsEngine{DB: db, OAuthConfig: cfg}).WeekMeetings(ctx, user, week)
			return err
		},
		"ManagerEngine.DetectTeam": func() error {
			_, err := (&ManagerEngine{DB: db, OAuthConfig: cfg}).DetectTeam(ctx, user)
			return err
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			err := call()
			if !errors.Is(err, ErrCalendarNotConnected) {
				t.Fatalf("err = %v, want ErrCalendarNotConnected", err)
			}
			if strings.Contains(err.Error(), user.String()) {
				t.Fatalf("error names the user: %v", err)
			}
		})
	}
}

// The personal-calendar sync reaches the work calendar after reading the
// feed; a missing work credential surfaces as ErrCalendarNotConnected.
func TestPAC21PersonalSyncReportsCalendarNotConnected(t *testing.T) {
	db := openTestDB(t)
	_, user := smartScheduleUser(t, db, "pac21-personal@example.com")
	calID, err := storage.InsertPersonalCalendar(db, &storage.PersonalCalendar{UserID: user, Provider: "webcal", Name: "p", URL: pac21ICSFeed(t), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	b := &PersonalBlocker{DB: db, OAuthConfig: &oauth2.Config{}}
	ctx := context.WithValue(context.Background(), auth.UserIDKey, user)
	if err := b.Sync(ctx, calID); !errors.Is(err, ErrCalendarNotConnected) {
		t.Fatalf("err = %v, want ErrCalendarNotConnected", err)
	}
}

// pac21ICSFeed serves a one-event iCalendar feed and returns its URL.
func pac21ICSFeed(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/calendar")
		_, _ = w.Write([]byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//pac21//test//EN\r\n" +
			"BEGIN:VEVENT\r\nUID:pac21-1\r\nDTSTAMP:20300101T000000Z\r\nDTSTART:20300108T100000Z\r\nDTEND:20300108T110000Z\r\nSUMMARY:Dentist\r\nEND:VEVENT\r\n" +
			"END:VCALENDAR\r\n"))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// AC-6, U-07 (assumed by the factory, pending owner confirmation): there is no
// 15-minute rule; candidate starts step 30 minutes from range_start.
func TestSmartScheduler_Suggest_StepsThirtyMinutes(t *testing.T) {
	db := openTestDB(t)
	ctx, user := smartScheduleUser(t, db, "pac21-u07@example.com")
	settings, err := storage.GetOrCreateSettingsByUser(db, user)
	if err != nil {
		t.Fatal(err)
	}
	loc, err := time.LoadLocation(settings.Timezone)
	if err != nil {
		loc = time.UTC
	}
	// A future weekday whose work window is enabled; range_start at :07.
	day := time.Date(2030, 1, 7, 0, 0, 0, 0, loc)
	for i := 0; i < 14; i++ {
		if _, _, enabled := settings.WorkWindow(day); enabled {
			break
		}
		day = day.AddDate(0, 0, 1)
	}
	startText, endText, _ := settings.WorkWindow(day)
	ws, err := time.ParseInLocation("2006-01-02 15:04", day.Format(time.DateOnly)+" "+startText, loc)
	if err != nil {
		t.Fatal(err)
	}
	we, err := time.ParseInLocation("2006-01-02 15:04", day.Format(time.DateOnly)+" "+endText, loc)
	if err != nil {
		t.Fatal(err)
	}
	rangeStart := ws.Add(7 * time.Minute)
	sched := &SmartScheduler{DB: db, calOps: &mockCalOps{calID: "primary"}}
	got, err := sched.Suggest(ctx, ScheduleRequest{DurationMinutes: 30, RangeStart: rangeStart, RangeEnd: we})
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if len(got.Slots) == 0 {
		t.Fatal("no slots over an empty calendar")
	}
	offGrid := false
	for _, s := range got.Slots {
		if d := s.Start.Sub(rangeStart); d < 0 || d%(30*time.Minute) != 0 {
			t.Fatalf("start %s is %s after range_start %s, not a multiple of 30 minutes", s.Start, d, rangeStart)
		}
		if s.Start.In(loc).Minute()%15 != 0 {
			offGrid = true
		}
	}
	if !offGrid {
		t.Fatalf("every start is on a 15-minute boundary; a 15-minute rule would explain that: %v", got.Slots)
	}
}
