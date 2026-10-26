package api

// PAC-21 (API-054), AC-2: a signed-in user with no stored Google credential
// who calls an operation that cannot run without their calendar gets the
// documented 401 CalendarNotConnected, as JSON, with one fixed message. A
// failure to READ the credential store is not that 401: it is the operation's
// 500, with the generic body.
//
// The requests go through RegisterRoutes, the real middleware and the real
// engines, so the test proves the wiring, not only the helpers.

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/storage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

const pac21JWTSecret = "pac21-test-secret"

type pac21Op struct {
	name, method, path, body string
}

// pac21Fixture is a user with no Google credential who owns a team (for the
// manager detection scope) and a personal webcal calendar whose feed reads
// successfully (so syncPersonalCalendar reaches the work-calendar step).
type pac21Fixture struct {
	router *chi.Mux
	user   uuid.UUID
	bearer string
	ops    []pac21Op
}

func newPAC21Fixture(t *testing.T, email string) *pac21Fixture {
	t.Helper()
	db := openTestDB(t)
	user := createTestUser(t, email)
	if _, err := db.Exec(`DELETE FROM oauth_tokens WHERE user_id = $1`, user); err != nil {
		t.Fatalf("clear tokens: %v", err)
	}
	team, err := storage.CreateTeam(db, "pac21 team", user)
	if err != nil {
		t.Fatalf("create team: %v", err)
	}
	if err := storage.AddTeamMember(db, team.ID, user, "owner"); err != nil {
		t.Fatalf("add owner: %v", err)
	}
	feed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/calendar")
		_, _ = w.Write([]byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//pac21//test//EN\r\n" +
			"BEGIN:VEVENT\r\nUID:pac21-1\r\nDTSTAMP:20300101T000000Z\r\nDTSTART:20300108T100000Z\r\nDTEND:20300108T110000Z\r\nSUMMARY:Dentist\r\nEND:VEVENT\r\n" +
			"END:VCALENDAR\r\n"))
	}))
	t.Cleanup(feed.Close)
	calID, err := storage.InsertPersonalCalendar(db, &storage.PersonalCalendar{UserID: user, Provider: "webcal", Name: "pac21", URL: feed.URL, Enabled: true})
	if err != nil {
		t.Fatalf("insert personal calendar: %v", err)
	}
	bearer, err := auth.GenerateToken(user, email, pac21JWTSecret)
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	r := chi.NewRouter()
	RegisterRoutes(r, db, &oauth2.Config{}, pac21JWTSecret, "http://localhost", "http://localhost")

	slot := `{"start":"2030-01-07T10:00:00Z","end":"2030-01-07T10:30:00Z"}`
	return &pac21Fixture{
		router: r,
		user:   user,
		bearer: bearer,
		ops: []pac21Op{
			{"listCalendarEvents", http.MethodGet, "/api/calendar/events?start=2030-01-07T00:00:00Z&end=2030-01-08T00:00:00Z", ""},
			{"getCalendarFreeBusy", http.MethodGet, "/api/calendar/freebusy?start=2030-01-07T00:00:00Z&end=2030-01-08T00:00:00Z", ""},
			{"patchEvent", http.MethodPatch, "/api/events/evt-1", `{"title":"x"}`},
			{"deleteEvent", http.MethodDelete, "/api/events/evt-1", ""},
			{"listRooms", http.MethodGet, "/api/rooms", ""},
			{"suggestAttendees", http.MethodGet, "/api/attendees/suggest?q=a", ""},
			{"addEventConference", http.MethodPost, "/api/events/evt-1/conference", `{"provider":"custom","url":"https://meet.example.test/r"}`},
			{"removeEventConference", http.MethodDelete, "/api/events/evt-1/conference", ""},
			{"listAnalyticsMeetings", http.MethodGet, "/api/analytics/meetings?date=2030-01-07", ""},
			{"runFocus", http.MethodPost, "/api/focus/run", `{"week":"2030-01-07"}`},
			{"clearFocusBlocks", http.MethodDelete, "/api/focus/blocks?week=2030-01-07", ""},
			{"previewCompression (single day)", http.MethodPost, "/api/schedule/compress", `{"date":"2030-01-07"}`},
			{"previewCompression (week)", http.MethodPost, "/api/schedule/compress", `{"week":"2030-01-07"}`},
			{"applyCompression", http.MethodPost, "/api/schedule/compress/apply", `{"proposals":[]}`},
			{"suggestMeetingSlots", http.MethodPost, "/api/schedule/suggest", `{"duration_minutes":30,"range_start":"2030-01-07T09:00:00Z","range_end":"2030-01-07T17:00:00Z"}`},
			{"createScheduledMeeting", http.MethodPost, "/api/schedule/create", `{"title":"T","start":"2030-01-07T10:00:00Z","end":"2030-01-07T10:30:00Z"}`},
			{"confirmNaturalLanguage", http.MethodPost, "/api/nlp/confirm", `{"parse_result":{"intent":"schedule","title":"T","suggested_slots":[` + slot + `]},"selected_slot_index":0}`},
			{"previewManagerDetection", http.MethodPost, "/api/manager/detect?team_id=" + team.ID.String(), ""},
			{"syncPersonalCalendar", http.MethodPost, fmt.Sprintf("/api/personal-calendars/%d/sync", calID), ""},
		},
	}
}

func (f *pac21Fixture) do(op pac21Op) *httptest.ResponseRecorder {
	var body *bytes.Reader
	if op.body != "" {
		body = bytes.NewReader([]byte(op.body))
	} else {
		body = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(op.method, op.path, body)
	req.Header.Set("Authorization", "Bearer "+f.bearer)
	if op.body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

// exactJSONError asserts Content-Type application/json and a body that is
// exactly {"error": want}.
func exactJSONError(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json; body %q", ct, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not a JSON object: %q (%v)", rec.Body.String(), err)
	}
	if len(body) != 1 || body["error"] != want {
		t.Fatalf("body = %s, want exactly {\"error\":%q}", rec.Body.String(), want)
	}
}

// AC-2: every calendar-dependent operation answers 401 CalendarNotConnected.
func TestPAC21NoCalendarIsCalendarNotConnected(t *testing.T) {
	f := newPAC21Fixture(t, "pac21-no-calendar@example.com")
	if len(f.ops) != 19 { // 18 operations, previewCompression in both modes
		t.Fatalf("fixture lists %d requests, want 19", len(f.ops))
	}
	for _, op := range f.ops {
		t.Run(op.name, func(t *testing.T) {
			_, _ = captureServerErrors(t)
			rec := f.do(op)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401; body %q", rec.Code, rec.Body.String())
			}
			exactJSONError(t, rec, "calendar not connected")
			if strings.Contains(rec.Body.String(), f.user.String()) {
				t.Fatalf("body carries the user id: %q", rec.Body.String())
			}
		})
	}
}

// hideOAuthTokens makes every read of the credential store fail (the table is
// renamed for the duration of the test) while the rest of the database works,
// which is exactly "the credential store could not be read".
func hideOAuthTokens(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`ALTER TABLE oauth_tokens RENAME TO oauth_tokens_pac21_hidden`); err != nil {
		t.Fatalf("hide oauth_tokens: %v", err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec(`ALTER TABLE oauth_tokens_pac21_hidden RENAME TO oauth_tokens`); err != nil {
			t.Fatalf("restore oauth_tokens: %v", err)
		}
	})
}

// AC-2, second half: a credential-store read failure on the same operations
// is the 500 with the generic body, never the 401, and never the driver text.
func TestPAC21CredentialStoreFailureIs500NotCalendarNotConnected(t *testing.T) {
	f := newPAC21Fixture(t, "pac21-store-down@example.com")
	hideOAuthTokens(t, openTestDB(t))
	for _, op := range f.ops {
		t.Run(op.name, func(t *testing.T) {
			_, logs := captureServerErrors(t)
			rec := f.do(op)
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500; body %q", rec.Code, rec.Body.String())
			}
			exactJSONError(t, rec, "internal error")
			for _, leak := range []string{"oauth_tokens", "relation", "SQLSTATE"} {
				if strings.Contains(rec.Body.String(), leak) {
					t.Fatalf("body leaks %q: %q", leak, rec.Body.String())
				}
			}
			if !strings.Contains(logs.String(), "oauth_tokens") {
				t.Fatalf("the server log does not carry the store error; log = %q", logs.String())
			}
		})
	}
}

// previewCompression week mode used to answer 200 [] for a user with no
// calendar; a storage failure before any day is computed is the 500, not 200 [].
func TestPAC21PreviewCompressionWeekWithClosedDBIs500(t *testing.T) {
	_, _ = captureServerErrors(t)
	db := closedTestDB(t)
	user := uuid.New()
	bearer, err := auth.GenerateToken(user, "pac21-closed@example.com", pac21JWTSecret)
	if err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	RegisterRoutes(r, db, &oauth2.Config{}, pac21JWTSecret, "http://localhost", "http://localhost")
	req := httptest.NewRequest(http.MethodPost, "/api/schedule/compress", strings.NewReader(`{"week":"2030-01-07"}`))
	req.Header.Set("Authorization", "Bearer "+bearer)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (not 200 []); body %q", rec.Code, rec.Body.String())
	}
	exactJSONError(t, rec, "internal error")
}

// The middleware's own 401 is unchanged: text/plain, not the JSON envelope,
// so a client can tell "no session" from "no calendar" by Content-Type.
func TestPAC21MiddlewareUnauthorizedStaysTextPlain(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutes(r, openTestDB(t), &oauth2.Config{}, pac21JWTSecret, "http://localhost", "http://localhost")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/calendar/events?start=2030-01-07T00:00:00Z&end=2030-01-08T00:00:00Z", nil))
	if rec.Code != http.StatusUnauthorized || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain") {
		t.Fatalf("status %d, Content-Type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
}
