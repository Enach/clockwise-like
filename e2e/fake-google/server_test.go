package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	googlecalendar "google.golang.org/api/calendar/v3"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

// bearerTransport attaches a fixed bearer, as oauth2.Transport does in the
// backend, without pulling x/oauth2 into this module's direct requirements.
type bearerTransport struct{ token string }

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	if b.token != "" {
		r.Header.Set("Authorization", "Bearer "+b.token)
	}
	return http.DefaultTransport.RoundTrip(r)
}

// noRedirect mirrors the backend's policy, so a 302 surfaces as an error.
var errRedirect = errors.New("redirect refused")

func client(t *testing.T, base, token string) *googlecalendar.Service {
	t.Helper()
	hc := &http.Client{
		Transport:     bearerTransport{token: token},
		CheckRedirect: func(*http.Request, []*http.Request) error { return errRedirect },
	}
	svc, err := googlecalendar.NewService(context.Background(),
		option.WithHTTPClient(hc), option.WithEndpoint(base+apiRoot))
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func newTestServer(t *testing.T) (*Store, *httptest.Server) {
	t.Helper()
	store := NewStore()
	store.SetTokens([]Credential{{Token: "tok-a", Label: "a-work", Calendar: "a@example.test"}})
	srv := httptest.NewServer(NewServer(store))
	t.Cleanup(srv.Close)
	return store, srv
}

// The library round trip: what the backend's client sends is what the
// stand-in parses, and what the stand-in answers is what the client decodes.
// Every request is logged with its operation, resolved calendar and label.
func TestProductSurface_RoundTripWithRealClient(t *testing.T) {
	store, srv := newTestServer(t)
	svc := client(t, srv.URL, "tok-a")
	ctx := context.Background()

	created, err := svc.Events.Insert("primary", &googlecalendar.Event{
		Summary: "planning",
		Start:   &googlecalendar.EventDateTime{DateTime: "2030-01-07T10:00:00Z"},
		End:     &googlecalendar.EventDateTime{DateTime: "2030-01-07T11:00:00Z"},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if created.Id == "" {
		t.Fatal("insert must assign an id")
	}

	list, err := svc.Events.List("primary").Context(ctx).
		TimeMin("2030-01-07T00:00:00Z").TimeMax("2030-01-08T00:00:00Z").
		SingleEvents(true).OrderBy("startTime").Do()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list.Items) != 1 || list.Items[0].Summary != "planning" {
		t.Fatalf("list returned %+v", list.Items)
	}

	fb, err := svc.Freebusy.Query(&googlecalendar.FreeBusyRequest{
		TimeMin: "2030-01-07T00:00:00Z", TimeMax: "2030-01-08T00:00:00Z",
		Items: []*googlecalendar.FreeBusyRequestItem{{Id: "a@example.test"}, {Id: "ghost@example.test"}},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("freebusy: %v", err)
	}
	if got := fb.Calendars["a@example.test"].Busy; len(got) != 1 || got[0].Start != "2030-01-07T10:00:00Z" {
		t.Fatalf("busy for a = %+v", got)
	}
	if errs := fb.Calendars["ghost@example.test"].Errors; len(errs) != 1 || errs[0].Reason != "notFound" {
		t.Fatalf("an unknown calendar must carry notFound, got %+v", errs)
	}

	if err := svc.Events.Delete("primary", created.Id).Context(ctx).Do(); err != nil {
		t.Fatalf("delete: %v", err)
	}

	log := store.Requests(0)
	wantOps := []string{"events.insert", "events.list", "freebusy.query", "events.delete"}
	if len(log) != len(wantOps) {
		t.Fatalf("logged %d requests, want %d: %+v", len(log), len(wantOps), log)
	}
	for i, op := range wantOps {
		if log[i].Operation != op || log[i].Credential != "a-work" {
			t.Errorf("entry %d = %s/%q, want %s/a-work", i, log[i].Operation, log[i].Credential, op)
		}
	}
	if log[1].CalendarID != "a@example.test" || log[1].TimeMin != "2030-01-07T00:00:00Z" {
		t.Errorf("list entry must record the resolved calendar and window, got %+v", log[1])
	}
	if len(log[2].FreeBusy) != 2 {
		t.Errorf("freebusy entry must record the queried ids, got %+v", log[2])
	}
}

// A missing or unaccepted bearer is refused with Google's envelope, and the
// log says which, without ever recording the bearer.
func TestProductSurface_RefusesMissingAndUnknownCredential(t *testing.T) {
	store, srv := newTestServer(t)
	for _, tc := range []struct{ token, wantLabel string }{
		{"", ""},
		{"not-a-seeded-token", "rejected"},
	} {
		_, err := client(t, srv.URL, tc.token).Events.List("primary").Do()
		var gerr *googleapi.Error
		if !errors.As(err, &gerr) || gerr.Code != http.StatusUnauthorized {
			t.Fatalf("token %q: want a 401 googleapi.Error, got %v", tc.token, err)
		}
		last := store.Requests(store.LastSeq() - 1)[0]
		if last.Credential != tc.wantLabel || last.Status != http.StatusUnauthorized {
			t.Errorf("token %q logged as %q/%d", tc.token, last.Credential, last.Status)
		}
	}
}

// Redirect mode fires once, for the armed label only.
func TestRedirectNext_FiresOnceForItsLabel(t *testing.T) {
	store, srv := newTestServer(t)
	store.SetTokens([]Credential{
		{Token: "tok-a", Label: "a-work", Calendar: "a@example.test"},
		{Token: "tok-b", Label: "b-work", Calendar: "b@example.test"},
	})
	store.ArmRedirect(Redirect{Label: "a-work", Location: "http://elsewhere.example.test/calendar/v3/x"})

	if _, err := client(t, srv.URL, "tok-b").Events.List("primary").Do(); err != nil {
		t.Fatalf("another label must not consume the redirect: %v", err)
	}
	_, err := client(t, srv.URL, "tok-a").Events.List("primary").Do()
	if !errors.Is(err, errRedirect) {
		t.Fatalf("armed label must get a 302, got %v", err)
	}
	if _, err := client(t, srv.URL, "tok-a").Events.List("primary").Do(); err != nil {
		t.Fatalf("the redirect must disarm after firing: %v", err)
	}
}

// The control plane's request log is readable after a mark.
func TestFixture_RequestsSince(t *testing.T) {
	_, srv := newTestServer(t)
	_, _ = client(t, srv.URL, "tok-a").Events.List("primary").Do()
	resp, err := http.Get(srv.URL + "/__fixture/requests?since=0")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		LastSeq  int64      `json:"last_seq"`
		Requests []LogEntry `json:"requests"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.LastSeq != 1 || len(body.Requests) != 1 || body.Requests[0].At.After(time.Now()) {
		t.Fatalf("unexpected log %+v", body)
	}
}

// A counted arming fires that many times; count 0 disarms early.
func TestRedirectNext_CountAndDisarm(t *testing.T) {
	store, srv := newTestServer(t)
	three, zero := 3, 0
	store.ArmRedirect(Redirect{Label: "a-work", Location: "http://elsewhere.example.test/", Count: &three})
	for i := 0; i < 2; i++ {
		if _, err := client(t, srv.URL, "tok-a").Events.List("primary").Do(); !errors.Is(err, errRedirect) {
			t.Fatalf("request %d must be redirected, got %v", i, err)
		}
	}
	store.ArmRedirect(Redirect{Label: "a-work", Count: &zero})
	if _, err := client(t, srv.URL, "tok-a").Events.List("primary").Do(); err != nil {
		t.Fatalf("count 0 must disarm: %v", err)
	}
}
