package api

// PAC-29: PATCH /api/events/{id} answers the CalendarEvent DTO (API-017) and
// honours send_updates, room_resource_email and bare-date start/end (API-018,
// API-042..045); DELETE /api/events/{id} honours send_updates and maps a
// Google 404/410 to 404. Every test runs the real handler against a fake
// Google Calendar that records what the server asked Google to do.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/Enach/paceday/backend/calendar"
	"github.com/Enach/paceday/backend/config"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
	googlecalendar "google.golang.org/api/calendar/v3"
)

// pac29Google is a Google Calendar stand-in for the single event evt-1 (plus
// the events list). GET answers getStatus (default 200) with getBody; PUT
// records its query and body and echoes the body back with the keys Google
// adds (kind, etag, htmlLink); DELETE records its query and answers
// deleteStatus (default 204).
type pac29Google struct {
	t            *testing.T
	getStatus    int
	getBody      string
	putStatus    int
	deleteStatus int

	mu          sync.Mutex
	gets        int
	puts        int
	deletes     int
	putQuery    url.Values
	putBody     map[string]any
	putResponse []byte
	deleteQuery url.Values
}

func googleError(w http.ResponseWriter, code int) {
	w.WriteHeader(code)
	_, _ = w.Write([]byte(`{"error":{"code":` + strconv.Itoa(code) + `,"message":"fake google error"}}`))
}

func newPAC29Google(t *testing.T, g *pac29Google) *pac29Google {
	t.Helper()
	g.t = t
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		g.mu.Lock()
		defer g.mu.Unlock()
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/events"):
			_, _ = w.Write([]byte(`{"items":[` + g.getBody + `]}`))
		case !strings.HasSuffix(r.URL.Path, "/events/evt-1"):
			googleError(w, http.StatusNotFound)
		case r.Method == http.MethodGet:
			g.gets++
			if g.getStatus != 0 && g.getStatus != http.StatusOK {
				googleError(w, g.getStatus)
				return
			}
			_, _ = w.Write([]byte(g.getBody))
		case r.Method == http.MethodPut:
			g.puts++
			g.putQuery = r.URL.Query()
			raw, _ := io.ReadAll(r.Body)
			g.putBody = map[string]any{}
			_ = json.Unmarshal(raw, &g.putBody)
			if g.putStatus != 0 {
				googleError(w, g.putStatus)
				return
			}
			echo := map[string]any{}
			_ = json.Unmarshal(raw, &echo)
			echo["kind"] = "calendar#event"
			echo["etag"] = `"3181161784712000"`
			echo["htmlLink"] = "https://calendar.example.test/evt-1"
			g.putResponse, _ = json.Marshal(echo)
			_, _ = w.Write(g.putResponse)
		case r.Method == http.MethodDelete:
			g.deletes++
			g.deleteQuery = r.URL.Query()
			if g.deleteStatus != 0 && g.deleteStatus != http.StatusNoContent {
				googleError(w, g.deleteStatus)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(srv.Close)
	calendar.Configure(config.CalendarEndpoint{BaseURL: srv.URL + config.CalendarAPIRoot, Guard: true})
	t.Cleanup(func() { calendar.Configure(config.ProductionCalendarEndpoint()) })
	return g
}

func (g *pac29Google) counts() (gets, puts, deletes int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.gets, g.puts, g.deletes
}

// sendUpdates returns the sendUpdates query of the last update or delete request.
func (g *pac29Google) sendUpdates(method string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if method == http.MethodDelete {
		return g.sendUpdates(http.MethodDelete)
	}
	return g.sendUpdates(http.MethodPut)
}

func (g *pac29Google) lastPutResponse() []byte {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.putResponse
}

// putEvent is the event the server last asked Google to write.
func (g *pac29Google) putEvent() map[string]any {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.putBody == nil {
		g.t.Fatal("no update request reached Google")
	}
	return g.putBody
}

func eventRouter(t *testing.T) *chi.Mux {
	t.Helper()
	h := &eventHandlers{db: openTestDB(t), oauthConfig: &oauth2.Config{}}
	r := chi.NewRouter()
	r.Patch("/api/events/{id}", h.patchEvent)
	r.Delete("/api/events/{id}", h.deleteEvent)
	return r
}

func doEvent(t *testing.T, user uuid.UUID, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, target, bytes.NewReader([]byte(body)))
	if user != uuid.Nil {
		req = withUser(req, user)
	}
	eventRouter(t).ServeHTTP(rec, req)
	return rec
}

const pac29TimedEvent = `{"id":"evt-1","status":"confirmed","summary":"Old","description":"d","location":"l",
"start":{"dateTime":"2030-01-07T10:00:00Z"},"end":{"dateTime":"2030-01-07T11:00:00Z"},
"hangoutLink":"https://meet.google.com/abc-defg-hij","colorId":"7",
"attendees":[{"email":"a@x.com","displayName":"A","responseStatus":"accepted"},
{"email":"room-1@resource.calendar.google.com","resource":true,"responseStatus":"accepted"}]}`

const pac29AllDayEvent = `{"id":"evt-1","status":"confirmed","summary":"Offsite",
"start":{"date":"2026-05-04"},"end":{"date":"2026-05-05"}}`

// AC-1: the PATCH 200 body is exactly toCalendarEventDTO of the event Google
// returned, never Google's own keys.
func TestPAC29PatchEventAnswersCalendarEventDTO(t *testing.T) {
	g := newPAC29Google(t, &pac29Google{getBody: pac29TimedEvent})
	user := userWithGoogleToken(t, "pac29-patch-dto@example.com")
	rec := doEvent(t, user, http.MethodPatch, "/api/events/evt-1", `{"title":"New"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q", ct)
	}

	var fromGoogle googlecalendar.Event
	if err := json.Unmarshal(g.lastPutResponse(), &fromGoogle); err != nil {
		t.Fatalf("decode fake response: %v", err)
	}
	want, _ := json.Marshal(toCalendarEventDTO(&fromGoogle))
	assertSameJSON(t, rec.Body.Bytes(), want)

	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	for _, k := range []string{"id", "title", "start", "end"} {
		if _, ok := body[k]; !ok {
			t.Fatalf("body lacks %q: %s", k, rec.Body.String())
		}
	}
	if body["title"] != "New" {
		t.Fatalf("title = %v, want New", body["title"])
	}
	if _, ok := body["start"].(string); !ok {
		t.Fatalf("start is %T, want a string: %s", body["start"], rec.Body.String())
	}
	for _, k := range []string{"summary", "htmlLink", "hangoutLink", "kind", "etag"} {
		if _, ok := body[k]; ok {
			t.Fatalf("body carries Google's key %q: %s", k, rec.Body.String())
		}
	}
	if strings.Contains(rec.Body.String(), "resource.calendar.google.com") {
		t.Fatalf("room attendee leaked into the DTO: %s", rec.Body.String())
	}
}

// AC-3: for one Google event, the list element and the PATCH body agree.
func TestPAC29ListAndPatchAgree(t *testing.T) {
	newPAC29Google(t, &pac29Google{getBody: pac29TimedEvent})
	user := userWithGoogleToken(t, "pac29-list-patch@example.com")

	ch := &calendarHandlers{db: openTestDB(t), oauthConfig: &oauth2.Config{}}
	listRec := httptest.NewRecorder()
	ch.listEvents(listRec, withUser(httptest.NewRequest(http.MethodGet,
		"/api/calendar/events?start=2030-01-07T00:00:00Z&end=2030-01-08T00:00:00Z", nil), user))
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d; body %q", listRec.Code, listRec.Body.String())
	}
	var list []json.RawMessage
	if err := json.Unmarshal(listRec.Body.Bytes(), &list); err != nil || len(list) != 1 {
		t.Fatalf("list = %s (%v), want one element", listRec.Body.String(), err)
	}

	rec := doEvent(t, user, http.MethodPatch, "/api/events/evt-1", `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch status = %d; body %q", rec.Code, rec.Body.String())
	}
	assertSameJSON(t, rec.Body.Bytes(), list[0])
}

func assertSameJSON(t *testing.T, got, want []byte) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("got is not JSON: %q", got)
	}
	if err := json.Unmarshal(want, &w); err != nil {
		t.Fatalf("want is not JSON: %q", want)
	}
	if !reflect.DeepEqual(g, w) {
		t.Fatalf("JSON differs\n got: %s\nwant: %s", got, want)
	}
}

// AC-6 (PATCH half): send_updates reaches Google as sendUpdates; absent is
// none; anything else is a 400 and nothing is written.
func TestPAC29PatchSendUpdates(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
	}{
		{"all", `{"title":"x","send_updates":"all"}`, "all"},
		{"none", `{"title":"x","send_updates":"none"}`, "none"},
		{"absent", `{"title":"x"}`, "none"},
		{"null", `{"title":"x","send_updates":null}`, "none"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := newPAC29Google(t, &pac29Google{getBody: pac29TimedEvent})
			user := userWithGoogleToken(t, "pac29-su-"+tc.name+"@example.com")
			rec := doEvent(t, user, http.MethodPatch, "/api/events/evt-1", tc.body)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d; body %q", rec.Code, rec.Body.String())
			}
			if got := g.sendUpdates(http.MethodPut); got != tc.want {
				t.Fatalf("sendUpdates = %q, want %q", got, tc.want)
			}
		})
	}
	for _, bad := range []string{`"bogus"`, `"ALL"`, `""`, `"externalOnly"`} {
		t.Run("invalid "+bad, func(t *testing.T) {
			g := newPAC29Google(t, &pac29Google{getBody: pac29TimedEvent})
			user := userWithGoogleToken(t, "pac29-su-bad@example.com")
			rec := doEvent(t, user, http.MethodPatch, "/api/events/evt-1", `{"title":"x","send_updates":`+bad+`}`)
			assertFixed4xx(t, rec, http.StatusBadRequest, "invalid send_updates")
			if gets, puts, _ := g.counts(); gets != 0 || puts != 0 {
				t.Fatalf("Google was called (gets=%d puts=%d)", gets, puts)
			}
		})
	}
}

// AC-6 (DELETE half), AC-10 (DELETE half).
func TestPAC29DeleteSendUpdatesAndNotFound(t *testing.T) {
	for _, tc := range []struct {
		name, query, want string
	}{
		{"all", "?send_updates=all", "all"},
		{"none", "?send_updates=none", "none"},
		{"absent", "", "none"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := newPAC29Google(t, &pac29Google{})
			user := userWithGoogleToken(t, "pac29-del-"+tc.name+"@example.com")
			rec := doEvent(t, user, http.MethodDelete, "/api/events/evt-1"+tc.query, "")
			if rec.Code != http.StatusNoContent {
				t.Fatalf("status = %d; body %q", rec.Code, rec.Body.String())
			}
			if got := g.sendUpdates(http.MethodDelete); got != tc.want {
				t.Fatalf("sendUpdates = %q, want %q", got, tc.want)
			}
		})
	}
	for _, q := range []string{"?send_updates=bogus", "?send_updates=", "?send_updates=ALL"} {
		t.Run("invalid "+q, func(t *testing.T) {
			g := newPAC29Google(t, &pac29Google{})
			user := userWithGoogleToken(t, "pac29-del-bad@example.com")
			rec := doEvent(t, user, http.MethodDelete, "/api/events/evt-1"+q, "")
			assertFixed4xx(t, rec, http.StatusBadRequest, "invalid send_updates")
			if _, _, deletes := g.counts(); deletes != 0 {
				t.Fatalf("Google delete was called %d times", deletes)
			}
		})
	}
	for _, tc := range []struct {
		google, status int
		want           string
	}{
		{http.StatusNotFound, http.StatusNotFound, "event not found"},
		{http.StatusGone, http.StatusNotFound, "event not found"},
		{http.StatusInternalServerError, http.StatusInternalServerError, "internal error"},
	} {
		t.Run("google "+http.StatusText(tc.google), func(t *testing.T) {
			newPAC29Google(t, &pac29Google{deleteStatus: tc.google})
			user := userWithGoogleToken(t, "pac29-del-err@example.com")
			rec := doEvent(t, user, http.MethodDelete, "/api/events/evt-1", "")
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d; body %q", rec.Code, tc.status, rec.Body.String())
			}
			exactJSONError(t, rec, tc.want)
		})
	}
}

// AC-7: bare dates keep an event all-day; a mixed pair is a 400 with no write;
// two date-times are an explicit conversion.
func TestPAC29PatchAllDay(t *testing.T) {
	t.Run("bare dates stay all-day", func(t *testing.T) {
		g := newPAC29Google(t, &pac29Google{getBody: pac29AllDayEvent})
		user := userWithGoogleToken(t, "pac29-allday-dates@example.com")
		rec := doEvent(t, user, http.MethodPatch, "/api/events/evt-1", `{"start":"2026-05-06","end":"2026-05-07"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d; body %q", rec.Code, rec.Body.String())
		}
		ev := g.putEvent()
		assertEventTime(t, ev, "start", map[string]any{"date": "2026-05-06"})
		assertEventTime(t, ev, "end", map[string]any{"date": "2026-05-07"})
		var body map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if body["start"] != "2026-05-06" || body["end"] != "2026-05-07" {
			t.Fatalf("DTO start/end = %v/%v", body["start"], body["end"])
		}
	})
	t.Run("title only leaves the dates", func(t *testing.T) {
		g := newPAC29Google(t, &pac29Google{getBody: pac29AllDayEvent})
		user := userWithGoogleToken(t, "pac29-allday-title@example.com")
		rec := doEvent(t, user, http.MethodPatch, "/api/events/evt-1", `{"title":"x"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d; body %q", rec.Code, rec.Body.String())
		}
		ev := g.putEvent()
		assertEventTime(t, ev, "start", map[string]any{"date": "2026-05-04"})
		assertEventTime(t, ev, "end", map[string]any{"date": "2026-05-05"})
	})
	for _, body := range []string{
		`{"start":"2026-05-06T10:00:00Z"}`,
		`{"end":"2026-05-06T10:00:00Z"}`,
		`{"start":"2026-05-06","end":"2026-05-06T10:00:00Z"}`,
	} {
		t.Run("mixed "+body, func(t *testing.T) {
			g := newPAC29Google(t, &pac29Google{getBody: pac29AllDayEvent})
			user := userWithGoogleToken(t, "pac29-allday-mixed@example.com")
			rec := doEvent(t, user, http.MethodPatch, "/api/events/evt-1", body)
			assertFixed4xx(t, rec, http.StatusBadRequest, "start and end must both be dates or both be date-times")
			if _, puts, _ := g.counts(); puts != 0 {
				t.Fatalf("Google update was called %d times", puts)
			}
		})
	}
	t.Run("a date alone on a timed event is mixed", func(t *testing.T) {
		g := newPAC29Google(t, &pac29Google{getBody: pac29TimedEvent})
		user := userWithGoogleToken(t, "pac29-timed-mixed@example.com")
		rec := doEvent(t, user, http.MethodPatch, "/api/events/evt-1", `{"start":"2030-01-08"}`)
		assertFixed4xx(t, rec, http.StatusBadRequest, "start and end must both be dates or both be date-times")
		if _, puts, _ := g.counts(); puts != 0 {
			t.Fatalf("Google update was called %d times", puts)
		}
	})
	t.Run("two date-times convert explicitly", func(t *testing.T) {
		g := newPAC29Google(t, &pac29Google{getBody: pac29AllDayEvent})
		user := userWithGoogleToken(t, "pac29-allday-convert@example.com")
		rec := doEvent(t, user, http.MethodPatch, "/api/events/evt-1", `{"start":"2026-05-06T12:00:00+02:00","end":"2026-05-06T11:00:00.000Z"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d; body %q", rec.Code, rec.Body.String())
		}
		ev := g.putEvent()
		assertEventTime(t, ev, "start", map[string]any{"dateTime": "2026-05-06T10:00:00Z"})
		assertEventTime(t, ev, "end", map[string]any{"dateTime": "2026-05-06T11:00:00Z"})
	})
	for _, body := range []string{`{"start":"2026-02-30"}`, `{"start":""}`, `{"end":"tomorrow"}`, `{"start":"2026-05-06 10:00"}`} {
		t.Run("invalid "+body, func(t *testing.T) {
			g := newPAC29Google(t, &pac29Google{getBody: pac29AllDayEvent})
			user := userWithGoogleToken(t, "pac29-allday-invalid@example.com")
			rec := doEvent(t, user, http.MethodPatch, "/api/events/evt-1", body)
			assertFixed4xx(t, rec, http.StatusBadRequest, "invalid body")
			if gets, puts, _ := g.counts(); gets != 0 || puts != 0 {
				t.Fatalf("Google was called (gets=%d puts=%d)", gets, puts)
			}
		})
	}
}

func assertEventTime(t *testing.T, ev map[string]any, key string, want map[string]any) {
	t.Helper()
	got, _ := ev[key].(map[string]any)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Google %s = %v, want exactly %v", key, got, want)
	}
}

const pac29AttendeeEvent = `{"id":"evt-1","status":"confirmed","summary":"Sync",
"start":{"dateTime":"2030-01-07T10:00:00Z"},"end":{"dateTime":"2030-01-07T11:00:00Z"},
"attendees":[{"email":"a@x.com","displayName":"A","responseStatus":"accepted"},
{"email":"room-1@resource.calendar.google.com","resource":true,"responseStatus":"accepted"},
{"email":"c@x.com","displayName":"C","responseStatus":"tentative"}]}`

func attendeesOf(t *testing.T, ev map[string]any) map[string]map[string]any {
	t.Helper()
	list, _ := ev["attendees"].([]any)
	out := map[string]map[string]any{}
	for _, a := range list {
		m, _ := a.(map[string]any)
		email, _ := m["email"].(string)
		if _, dup := out[email]; dup {
			t.Fatalf("attendee %q written twice: %v", email, list)
		}
		out[email] = m
	}
	return out
}

// AC-8: attendees keeps existing entries and rooms; room_resource_email adds a
// room; attendee_details is ignored; a non-room room address is a 400.
func TestPAC29PatchAttendeesAndRooms(t *testing.T) {
	t.Run("keeps entries and the room, ignores attendee_details", func(t *testing.T) {
		g := newPAC29Google(t, &pac29Google{getBody: pac29AttendeeEvent})
		user := userWithGoogleToken(t, "pac29-att-keep@example.com")
		rec := doEvent(t, user, http.MethodPatch, "/api/events/evt-1",
			`{"attendees":["A@x.com","b@x.com","b@x.com"],"attendee_details":[{"email":"a@x.com","rsvp":"declined"}],"room_resource_email":""}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d; body %q", rec.Code, rec.Body.String())
		}
		got := attendeesOf(t, g.putEvent())
		if len(got) != 3 {
			t.Fatalf("attendees = %v, want a@x.com, b@x.com and room-1", got)
		}
		if a := got["a@x.com"]; a["displayName"] != "A" || a["responseStatus"] != "accepted" {
			t.Fatalf("a@x.com lost its Google entry: %v", a)
		}
		if b := got["b@x.com"]; b == nil || len(b) != 1 {
			t.Fatalf("b@x.com = %v, want an email-only entry", b)
		}
		if r := got["room-1@resource.calendar.google.com"]; r == nil || r["responseStatus"] != "accepted" {
			t.Fatalf("room-1 not kept: %v", got)
		}
		if _, ok := got["c@x.com"]; ok {
			t.Fatalf("c@x.com was not listed and should be removed: %v", got)
		}
	})
	t.Run("room_resource_email adds a room", func(t *testing.T) {
		g := newPAC29Google(t, &pac29Google{getBody: pac29AttendeeEvent})
		user := userWithGoogleToken(t, "pac29-att-room2@example.com")
		rec := doEvent(t, user, http.MethodPatch, "/api/events/evt-1",
			`{"attendees":["a@x.com","b@x.com"],"room_resource_email":"room-2@resource.calendar.google.com"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d; body %q", rec.Code, rec.Body.String())
		}
		got := attendeesOf(t, g.putEvent())
		r2 := got["room-2@resource.calendar.google.com"]
		if got["room-1@resource.calendar.google.com"] == nil || r2 == nil || r2["resource"] != true {
			t.Fatalf("want both rooms, room-2 as a resource: %v", got)
		}
	})
	t.Run("room_resource_email alone keeps the guests", func(t *testing.T) {
		g := newPAC29Google(t, &pac29Google{getBody: pac29AttendeeEvent})
		user := userWithGoogleToken(t, "pac29-att-roomonly@example.com")
		rec := doEvent(t, user, http.MethodPatch, "/api/events/evt-1",
			`{"room_resource_email":"room-1@resource.calendar.google.com"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d; body %q", rec.Code, rec.Body.String())
		}
		if got := attendeesOf(t, g.putEvent()); len(got) != 3 {
			t.Fatalf("attendees = %v, want the three existing ones unchanged", got)
		}
	})
	t.Run("a new room address in attendees is a resource", func(t *testing.T) {
		g := newPAC29Google(t, &pac29Google{getBody: pac29AttendeeEvent})
		user := userWithGoogleToken(t, "pac29-att-roomlisted@example.com")
		rec := doEvent(t, user, http.MethodPatch, "/api/events/evt-1", `{"attendees":["a@x.com","room-9@resource.calendar.google.com"]}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d; body %q", rec.Code, rec.Body.String())
		}
		if r := attendeesOf(t, g.putEvent())["room-9@resource.calendar.google.com"]; r == nil || r["resource"] != true {
			t.Fatalf("room-9 = %v, want a resource attendee", r)
		}
	})
	t.Run("empty list keeps only the room", func(t *testing.T) {
		g := newPAC29Google(t, &pac29Google{getBody: pac29AttendeeEvent})
		user := userWithGoogleToken(t, "pac29-att-empty@example.com")
		rec := doEvent(t, user, http.MethodPatch, "/api/events/evt-1", `{"attendees":[]}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d; body %q", rec.Code, rec.Body.String())
		}
		got := attendeesOf(t, g.putEvent())
		if len(got) != 1 || got["room-1@resource.calendar.google.com"] == nil {
			t.Fatalf("attendees = %v, want only room-1", got)
		}
	})
	t.Run("invalid room_resource_email", func(t *testing.T) {
		g := newPAC29Google(t, &pac29Google{getBody: pac29AttendeeEvent})
		user := userWithGoogleToken(t, "pac29-att-badroom@example.com")
		rec := doEvent(t, user, http.MethodPatch, "/api/events/evt-1", `{"room_resource_email":"x@example.com"}`)
		assertFixed4xx(t, rec, http.StatusBadRequest, "invalid room_resource_email")
		if gets, puts, _ := g.counts(); gets != 0 || puts != 0 {
			t.Fatalf("Google was called (gets=%d puts=%d)", gets, puts)
		}
	})
	t.Run("the round-1 challenge request is a 200", func(t *testing.T) {
		g := newPAC29Google(t, &pac29Google{getBody: pac29AttendeeEvent})
		user := userWithGoogleToken(t, "pac29-att-b2@example.com")
		rec := doEvent(t, user, http.MethodPatch, "/api/events/evt-1",
			`{"title":"Standup","start":"2026-05-01T10:00:00Z","end":"2026-05-01T10:30:00Z","room_resource_email":"","attendee_details":[{"email":"a@x.com"}],"attendees":["a@x.com"],"send_updates":"all"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d; body %q", rec.Code, rec.Body.String())
		}
		if g.sendUpdates(http.MethodPut) != "all" {
			t.Fatalf("sendUpdates = %q", g.sendUpdates(http.MethodPut))
		}
	})
}

// AC-10 (PATCH half): only a Google 404/410 or a cancelled event is a 404.
func TestPAC29PatchGetEventFailures(t *testing.T) {
	for _, tc := range []struct {
		name       string
		google     *pac29Google
		status     int
		want       string
		wantGetHit bool
	}{
		{"google 404", &pac29Google{getStatus: http.StatusNotFound}, http.StatusNotFound, "event not found", true},
		{"google 410", &pac29Google{getStatus: http.StatusGone}, http.StatusNotFound, "event not found", true},
		{"google 500", &pac29Google{getStatus: http.StatusInternalServerError}, http.StatusInternalServerError, "internal error", true},
		{"google 403", &pac29Google{getStatus: http.StatusForbidden}, http.StatusInternalServerError, "internal error", true},
		{"cancelled", &pac29Google{getBody: `{"id":"evt-1","status":"cancelled"}`}, http.StatusNotFound, "event not found", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := newPAC29Google(t, tc.google)
			user := userWithGoogleToken(t, "pac29-get-err@example.com")
			rec := doEvent(t, user, http.MethodPatch, "/api/events/evt-1", `{"title":"x"}`)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d; body %q", rec.Code, tc.status, rec.Body.String())
			}
			exactJSONError(t, rec, tc.want)
			if gets, puts, _ := g.counts(); gets == 0 || puts != 0 {
				t.Fatalf("gets=%d puts=%d, want GetEvent called and no update", gets, puts)
			}
		})
	}
	t.Run("google update fails", func(t *testing.T) {
		newPAC29Google(t, &pac29Google{getBody: pac29TimedEvent, putStatus: http.StatusBadRequest})
		user := userWithGoogleToken(t, "pac29-put-err@example.com")
		rec := doEvent(t, user, http.MethodPatch, "/api/events/evt-1", `{"title":"x"}`)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d; body %q", rec.Code, rec.Body.String())
		}
		exactJSONError(t, rec, "internal error")
	})
}

// AC-12: request-only checks come before the credential and before Google.
func TestPAC29ErrorPrecedence(t *testing.T) {
	g := newPAC29Google(t, &pac29Google{getStatus: http.StatusNotFound})
	noCred := createTestUser(t, "pac29-precedence-nocred@example.com")

	rec := doEvent(t, noCred, http.MethodPatch, "/api/events/evt-1", `{"send_updates":"bogus"}`)
	assertFixed4xx(t, rec, http.StatusBadRequest, "invalid send_updates")

	rec = doEvent(t, noCred, http.MethodDelete, "/api/events/evt-1?send_updates=bogus", "")
	assertFixed4xx(t, rec, http.StatusBadRequest, "invalid send_updates")

	withCred := userWithGoogleToken(t, "pac29-precedence-cred@example.com")
	rec = doEvent(t, withCred, http.MethodPatch, "/api/events/evt-1", `{"room_resource_email":"x@example.com"}`)
	assertFixed4xx(t, rec, http.StatusBadRequest, "invalid room_resource_email")

	if gets, puts, deletes := g.counts(); gets+puts+deletes != 0 {
		t.Fatalf("Google was called (gets=%d puts=%d deletes=%d)", gets, puts, deletes)
	}

	// The no-credential path itself is unchanged: a valid request is a 401.
	rec = doEvent(t, noCred, http.MethodPatch, "/api/events/evt-1", `{"title":"x"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body %q", rec.Code, rec.Body.String())
	}
}
