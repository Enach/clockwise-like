package api

// PAC-21, AC-1 (behaviour half): every 4xx that used to embed err.Error()
// answers with its documented fixed message. Each case forces the underlying
// error to carry a distinctive marker and asserts that the status is
// unchanged, the body is exactly the fixed JSON envelope, and the marker is
// absent. The source-scan half of AC-1 is in pac21_error_bodies_test.go.
//
// listCalendarEvents/getCalendarFreeBusy (`not connected: <err>`) are covered
// by pac21_calendar_not_connected_test.go: the no-token 401 body is exact and
// the store-failure body is checked for the driver's text.

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/calendar"
	"github.com/Enach/paceday/backend/config"
	"github.com/Enach/paceday/backend/storage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
	googlecalendar "google.golang.org/api/calendar/v3"
)

const pac21Marker = "PAC21-LEAK-MARKER"

func assertFixed4xx(t *testing.T, rec *httptest.ResponseRecorder, status int, want string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d; body %q", rec.Code, status, rec.Body.String())
	}
	exactJSONError(t, rec, want)
	for _, leak := range []string{pac21Marker, "json:", "Go struct", "cannot unmarshal", "invalid character", "googleapi", "strconv", "parsing time"} {
		if strings.Contains(rec.Body.String(), leak) {
			t.Fatalf("body leaks %q: %q", leak, rec.Body.String())
		}
	}
}

// fakeGoogleEvents is a Google Calendar stand-in for one event id: GET returns
// the event (or a 404 whose message carries the marker), PUT echoes the body
// back the way Google does, with Google's own keys.
func fakeGoogleEvents(t *testing.T, notFound bool) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if !strings.HasSuffix(r.URL.Path, "/events/evt-1") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if notFound {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":404,"message":"` + pac21Marker + ` Not Found","errors":[{"reason":"notFound","message":"` + pac21Marker + `"}]}}`))
			return
		}
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(`{"id":"evt-1","summary":"Old","htmlLink":"https://calendar.example.test/evt-1","start":{"dateTime":"2030-01-07T10:00:00Z"},"end":{"dateTime":"2030-01-07T11:00:00Z"}}`))
		case http.MethodPut:
			var ev map[string]any
			_ = json.NewDecoder(r.Body).Decode(&ev)
			ev["htmlLink"] = "https://calendar.example.test/evt-1"
			_ = json.NewEncoder(w).Encode(ev)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(srv.Close)
	calendar.Configure(config.CalendarEndpoint{BaseURL: srv.URL + config.CalendarAPIRoot, Guard: true})
	t.Cleanup(func() { calendar.Configure(config.ProductionCalendarEndpoint()) })
}

func userWithGoogleToken(t *testing.T, email string) uuid.UUID {
	t.Helper()
	user := createTestUser(t, email)
	if err := auth.UpsertUserToken(openTestDB(t), user, &oauth2.Token{AccessToken: "pac21-cred", Expiry: time.Now().Add(24 * time.Hour)}); err != nil {
		t.Fatalf("store token: %v", err)
	}
	return user
}

func TestPAC21PatchEventNotFoundIsFixed(t *testing.T) {
	fakeGoogleEvents(t, true)
	user := userWithGoogleToken(t, "pac21-patch-404@example.com")
	h := &eventHandlers{db: openTestDB(t), oauthConfig: &oauth2.Config{}}
	r := chi.NewRouter()
	r.Patch("/api/events/{id}", h.patchEvent)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, withUser(httptest.NewRequest(http.MethodPatch, "/api/events/evt-1", strings.NewReader(`{"title":"x"}`)), user))
	assertFixed4xx(t, rec, http.StatusNotFound, "event not found")
}

// AC-6, U-04 (re-asserted by PAC-29, AC-1): the 200 body of PATCH
// /api/events/{id} is the CalendarEvent DTO the list returns, not the raw
// Google event (GoogleCalendarEventRaw, deleted from the contract by PAC-29).
func TestPatchEvent_SuccessBodyIsCalendarEventDTO(t *testing.T) {
	fakeGoogleEvents(t, false)
	user := userWithGoogleToken(t, "pac21-patch-200@example.com")
	h := &eventHandlers{db: openTestDB(t), oauthConfig: &oauth2.Config{}}
	r := chi.NewRouter()
	r.Patch("/api/events/{id}", h.patchEvent)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, withUser(httptest.NewRequest(http.MethodPatch, "/api/events/evt-1", strings.NewReader(`{"title":"New"}`)), user))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %q", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["title"] != "New" || body["start"] != "2030-01-07T10:00:00Z" || body["end"] != "2030-01-07T11:00:00Z" || body["id"] != "evt-1" {
		t.Fatalf("body is not the CalendarEvent DTO: %v", body)
	}
	for _, googleKey := range []string{"summary", "htmlLink"} {
		if _, ok := body[googleKey]; ok {
			t.Fatalf("body carries Google's key %q: %v", googleKey, body)
		}
	}
}

func TestPAC21ConferenceEventNotFoundIsFixed(t *testing.T) {
	client := &fakeConferenceEventClient{calendarID: "primary", getErr: errors.New("googleapi: Error 404: " + pac21Marker)}
	r := setupConferencingRoutes(t, client, nil)
	for _, tc := range []struct{ method, body string }{
		{http.MethodPost, `{"provider":"custom","url":"https://x.example.test"}`},
		{http.MethodDelete, ""},
	} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(tc.method, "/api/events/evt-1/conference", strings.NewReader(tc.body)))
		assertFixed4xx(t, rec, http.StatusNotFound, "event not found")
	}
}

func TestPAC21ConferenceUnparseableEventTimeIsFixed(t *testing.T) {
	user := createTestUser(t, "pac21-event-time@example.com")
	for _, ev := range []*googlecalendar.Event{
		{Summary: "S", Start: &googlecalendar.EventDateTime{DateTime: pac21Marker}, End: &googlecalendar.EventDateTime{DateTime: "2030-01-07T11:00:00Z"}},
		{Summary: "S", Start: &googlecalendar.EventDateTime{DateTime: "2030-01-07T10:00:00Z"}, End: &googlecalendar.EventDateTime{Date: pac21Marker}},
	} {
		client := &fakeConferenceEventClient{calendarID: "primary", event: ev}
		r := setupConferencingRoutes(t, client, &fakeConferenceProviderFactory{})
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, withUser(httptest.NewRequest(http.MethodPost, "/api/events/evt-1/conference", strings.NewReader(`{"provider":"zoom"}`)), user))
		assertFixed4xx(t, rec, http.StatusBadRequest, "event has no time")
	}
}

// A stored Zoom credential that does not decode is the fixed stored-credential
// sentence on both conference paths, never the JSON decoder's text.
func TestPAC21InvalidZoomCredentialIsFixed(t *testing.T) {
	db := openTestDB(t)
	user := createTestUser(t, "pac21-zoom@example.com")
	if _, err := storage.GetOrCreateSettingsByUser(db, user); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE settings SET conferencing_provider = 'zoom', zoom_tokens = $2 WHERE user_id = $1`, user, pac21Marker+"{"); err != nil {
		t.Fatal(err)
	}
	const want = "zoom: stored credential is invalid — reconnect at /api/auth/zoom"

	h := newConferencingHandlers(db, nil)
	rec := httptest.NewRecorder()
	h.createConference(rec, withUser(httptest.NewRequest(http.MethodPost, "/api/conference/create", strings.NewReader(`{"title":"T","start":"2030-01-07T10:00:00Z","end":"2030-01-07T11:00:00Z"}`)), user))
	assertFixed4xx(t, rec, http.StatusBadRequest, want)

	ev := &googlecalendar.Event{Summary: "S", Start: &googlecalendar.EventDateTime{DateTime: "2030-01-07T10:00:00Z"}, End: &googlecalendar.EventDateTime{DateTime: "2030-01-07T11:00:00Z"}}
	r := setupConferencingRoutes(t, &fakeConferenceEventClient{calendarID: "primary", event: ev}, nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, withUser(httptest.NewRequest(http.MethodPost, "/api/events/evt-1/conference", strings.NewReader(`{"provider":"zoom"}`)), user))
	assertFixed4xx(t, rec, http.StatusBadRequest, want)
}

// The provider factory's refusals are a closed list of fixed sentences; any
// other failure building a provider is a server error, logged, not echoed.
func TestPAC21ConferenceProviderRefusals(t *testing.T) {
	user := createTestUser(t, "pac21-provider@example.com")
	ev := &googlecalendar.Event{Summary: "S", Start: &googlecalendar.EventDateTime{DateTime: "2030-01-07T10:00:00Z"}, End: &googlecalendar.EventDateTime{DateTime: "2030-01-07T11:00:00Z"}}
	cases := []struct {
		name, provider string
		err            error
		status         int
		want           string
	}{
		{"teams not connected", "teams", errTeamsNotConnected, http.StatusBadRequest, "teams: not connected — visit /api/auth/microsoft to authenticate"},
		{"unsupported echoes the request's provider", "whereby", errUnsupportedConferenceProvider, http.StatusBadRequest, `unsupported conference provider "whereby"`},
		{"anything else is a 500", "zoom", errors.New(pac21Marker), http.StatusInternalServerError, "internal error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, logs := captureServerErrors(t)
			r := setupConferencingRoutes(t, &fakeConferenceEventClient{calendarID: "primary", event: ev}, &fakeConferenceProviderFactory{err: tc.err})
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, withUser(httptest.NewRequest(http.MethodPost, "/api/events/evt-1/conference", strings.NewReader(`{"provider":"`+tc.provider+`"}`)), user))
			assertFixed4xx(t, rec, tc.status, tc.want)
			if tc.status >= 500 && !strings.Contains(logs.String(), pac21Marker) {
				t.Fatalf("the server log does not carry the detail; log = %q", logs.String())
			}
		})
	}
}

func TestPAC21QueryFreeBusyInvalidJSONIsFixed(t *testing.T) {
	h := newFreeBusyHandlers(openTestDB(t), nil)
	user := uuid.New()
	for name, body := range map[string]func() *http.Request{
		"read error": func() *http.Request {
			return httptest.NewRequest(http.MethodPost, "/api/freebusy", iotest.ErrReader(errors.New(pac21Marker)))
		},
		"wrong type": func() *http.Request {
			return httptest.NewRequest(http.MethodPost, "/api/freebusy", strings.NewReader(`{"emails":"a@x.test"}`))
		},
		"not JSON": func() *http.Request {
			return httptest.NewRequest(http.MethodPost, "/api/freebusy", strings.NewReader(`nope`))
		},
	} {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.query(rec, withUser(body(), user))
			assertFixed4xx(t, rec, http.StatusBadRequest, "invalid JSON")
		})
	}
}

func TestPAC21PatchSettingsMessagesAreFixed(t *testing.T) {
	db := openTestDB(t)
	h := &settingsHandlers{db: db}
	user := createTestUser(t, "pac21-settings@example.com")

	t.Run("unreadable body", func(t *testing.T) {
		req := withUser(httptest.NewRequest(http.MethodPatch, "/api/settings/", iotest.ErrReader(errors.New(pac21Marker))), user)
		rec := httptest.NewRecorder()
		h.patchSettings(rec, req)
		assertFixed4xx(t, rec, http.StatusBadRequest, "invalid JSON")
	})
	for body, want := range map[string]string{
		`{"timeFormat":12}`:                 "invalid JSON: timeFormat has the wrong type",
		`{"focusMinBlockMinutes":"thirty"}`: "invalid JSON: focusMinBlockMinutes has the wrong type",
		`{"workingHours":"all day"}`:        "invalid JSON: workingHours has the wrong type",
		`{"workingHours":{"mode":"by_day","days":{"monday":{"enabled":true,"start":9,"end":"17:00"}}}}`: "invalid JSON: workingHours.days.monday.start has the wrong type",
		`{"updatedAt":"` + pac21Marker + `"}`:                "invalid JSON: updatedAt has the wrong type",
		`{"autoScheduleCron":"` + pac21Marker + ` 7 * * *"}`: "autoScheduleCron is invalid",
	} {
		t.Run(body, func(t *testing.T) {
			rec := settingsRequest(t, h, http.MethodPatch, user, body)
			assertFixed4xx(t, rec, http.StatusBadRequest, want)
			for _, goName := range []string{"storage.", "Settings.", "WorkingHours", "DaySchedule", "string", "time.Time"} {
				if strings.Contains(rec.Body.String(), goName) {
					t.Fatalf("body names Go type %q: %q", goName, rec.Body.String())
				}
			}
		})
	}
}

// AC-3b (U-06, API-024): a negative selected_slot_index is a 400, not a panic.
func TestPAC21ConfirmNegativeSlotIndexIs400(t *testing.T) {
	h := &nlpHandlers{smart: &mockScheduler{}, db: openTestDB(t)}
	body := `{"parse_result":{"intent":"schedule","title":"T","suggested_slots":[{"start":"2030-01-07T10:00:00Z","end":"2030-01-07T10:30:00Z"}]},"selected_slot_index":-1}`
	rec := httptest.NewRecorder()
	func() {
		defer func() {
			if p := recover(); p != nil {
				t.Fatalf("confirm panicked on a negative index: %v", p)
			}
		}()
		h.confirm(rec, withUser(httptest.NewRequest(http.MethodPost, "/api/nlp/confirm", bytes.NewBufferString(body)), uuid.New()))
	}()
	assertFixed4xx(t, rec, http.StatusBadRequest, "invalid selected_slot_index")
}
