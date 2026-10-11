package calendar

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"
	googlecalendar "google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"

	"github.com/Enach/paceday/backend/config"
)

func newTestClient(t *testing.T) *CalendarClient {
	t.Helper()
	c, err := NewClient(context.Background(), oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "T"}))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

// TestNewClient_UnconfiguredIsProductionDecision is AC-1 and AC-2: a process
// that never calls Configure, or calls it with the production decision, builds
// a client for the published address with the guard off.
func TestNewClient_UnconfiguredIsProductionDecision(t *testing.T) {
	prev := active.Load()
	t.Cleanup(func() { active.Store(prev) })

	check := func(t *testing.T) {
		t.Helper()
		c := newTestClient(t)
		if c.service.BasePath != config.PublishedCalendarEndpoint {
			t.Errorf("BasePath = %q, want the published address %q", c.service.BasePath, config.PublishedCalendarEndpoint)
		}
		if s := current(); s.guard {
			t.Error("guard is on in the production decision")
		}
	}

	t.Run("Configure never called", func(t *testing.T) {
		active.Store(nil)
		check(t)
	})
	for _, tc := range []struct{ name, url, allow string }{
		{"neither variable set", "", ""},
		{"declaration alone", "", "true"},
	} {
		t.Run("after Configure: "+tc.name, func(t *testing.T) {
			d, err := config.DecideCalendarEndpoint(tc.url, tc.allow)
			if err != nil {
				t.Fatalf("decision: %v", err)
			}
			Configure(d)
			check(t)
		})
	}
}

// TestPublishedEndpoint_MatchesLibraryDefault catches drift between the
// published-address constant and the library's own basePath on an upgrade.
func TestPublishedEndpoint_MatchesLibraryDefault(t *testing.T) {
	svc, err := googlecalendar.NewService(context.Background(), option.WithHTTPClient(http.DefaultClient))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if svc.BasePath != config.PublishedCalendarEndpoint {
		t.Errorf("library basePath = %q, constant = %q", svc.BasePath, config.PublishedCalendarEndpoint)
	}
}

// TestNewClient_HonoursConfiguredBasePath is AC-1's inverse and AC-26's unit
// support: the configured address is used verbatim, trailing slash included.
func TestNewClient_HonoursConfiguredBasePath(t *testing.T) {
	const want = "http://stand-in.pac43.test:8080/calendar/v3/"
	configureForTest(t, guardedStandIn(want), dialHooks{})
	if got := newTestClient(t).service.BasePath; got != want {
		t.Errorf("BasePath = %q, want %q", got, want)
	}
}

type recordedRequest struct {
	method, path, auth string
	query              map[string][]string
	body               []byte
}

// recordingStandIn answers every request with respond and records it.
func recordingStandIn(t *testing.T, respond func(w http.ResponseWriter, r *http.Request)) (*httptest.Server, func() []recordedRequest) {
	t.Helper()
	var mu sync.Mutex
	var reqs []recordedRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		reqs = append(reqs, recordedRequest{r.Method, r.URL.Path, r.Header.Get("Authorization"), r.URL.Query(), body})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		respond(w, r)
	}))
	t.Cleanup(srv.Close)
	configureForTest(t, guardedStandIn(srv.URL+"/calendar/v3/"), dialHooks{})
	return srv, func() []recordedRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]recordedRequest(nil), reqs...)
	}
}

// TestCalendarClient_ListEventsReachesConfiguredEndpoint is AC-16's unit support.
func TestCalendarClient_ListEventsReachesConfiguredEndpoint(t *testing.T) {
	_, reqs := recordingStandIn(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"items":[{"id":"e1","summary":"Standup"}]}`))
	})
	from := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	events, err := newTestClient(t).ListEvents(context.Background(), "primary", from, from.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 || events[0].Id != "e1" || events[0].Summary != "Standup" {
		t.Errorf("events did not decode: %+v", events)
	}
	got := reqs()
	if len(got) != 1 {
		t.Fatalf("requests = %d, want 1", len(got))
	}
	r := got[0]
	if r.method != http.MethodGet || r.path != "/calendar/v3/calendars/primary/events" {
		t.Errorf("request = %s %s", r.method, r.path)
	}
	if r.query["timeMin"] == nil || r.query["timeMax"] == nil {
		t.Errorf("timeMin/timeMax missing: %v", r.query)
	}
	if r.auth != "Bearer T" {
		t.Errorf("Authorization = %q", r.auth)
	}
}

// TestCalendarClient_GetFreeBusyReachesConfiguredEndpoint is AC-17's unit support.
func TestCalendarClient_GetFreeBusyReachesConfiguredEndpoint(t *testing.T) {
	_, reqs := recordingStandIn(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"calendars":{"a@x.test":{"busy":[{"start":"2026-10-12T09:00:00Z","end":"2026-10-12T10:00:00Z"}]}}}`))
	})
	from := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	fb, err := newTestClient(t).GetFreeBusy(context.Background(), []string{"a@x.test", "b@x.test"}, from, from.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("GetFreeBusy: %v", err)
	}
	if len(fb["a@x.test"]) != 1 {
		t.Errorf("busy slots did not decode: %+v", fb)
	}
	got := reqs()
	if len(got) != 1 || got[0].method != http.MethodPost || got[0].path != "/calendar/v3/freeBusy" {
		t.Fatalf("requests = %+v, want one POST /calendar/v3/freeBusy", got)
	}
	var body googlecalendar.FreeBusyRequest
	if err := json.Unmarshal(got[0].body, &body); err != nil {
		t.Fatalf("body: %v", err)
	}
	ids := make([]string, 0, len(body.Items))
	for _, it := range body.Items {
		ids = append(ids, it.Id)
	}
	if len(ids) != 2 || ids[0] != "a@x.test" || ids[1] != "b@x.test" {
		t.Errorf("queried identifiers = %v", ids)
	}
}

// TestCalendarClient_CreateAndDeleteEventReachConfiguredEndpoint is AC-18 and
// AC-19's unit support.
func TestCalendarClient_CreateAndDeleteEventReachConfiguredEndpoint(t *testing.T) {
	_, reqs := recordingStandIn(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_, _ = w.Write([]byte(`{"id":"new-1","summary":"Focus"}`))
	})
	c := newTestClient(t)
	ev, err := c.CreateEvent(context.Background(), "primary", &googlecalendar.Event{Summary: "Focus"})
	if err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}
	if ev.Id != "new-1" {
		t.Errorf("created id = %q", ev.Id)
	}
	if err := c.DeleteEvent(context.Background(), "primary", ev.Id); err != nil {
		t.Fatalf("DeleteEvent: %v", err)
	}
	got := reqs()
	if len(got) != 2 {
		t.Fatalf("requests = %d, want 2", len(got))
	}
	if got[0].method != http.MethodPost || got[0].path != "/calendar/v3/calendars/primary/events" {
		t.Errorf("create = %s %s", got[0].method, got[0].path)
	}
	if got[1].method != http.MethodDelete || got[1].path != "/calendar/v3/calendars/primary/events/new-1" {
		t.Errorf("delete = %s %s", got[1].method, got[1].path)
	}
	for _, r := range got {
		if r.auth != "Bearer T" {
			t.Errorf("%s %s Authorization = %q", r.method, r.path, r.auth)
		}
	}
}
