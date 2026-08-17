package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/conference"
	"github.com/Enach/paceday/backend/storage"
	"github.com/go-chi/chi/v5"
	googlecalendar "google.golang.org/api/calendar/v3"
)

type fakeConferenceEventClient struct {
	calendarID      string
	event           *googlecalendar.Event
	updated         *googlecalendar.Event
	addMeetResult   *googlecalendar.Event
	clearMeetResult *googlecalendar.Event
	getErr          error
	updateErr       error
	addMeetErr      error
	clearMeetErr    error
}

func (f *fakeConferenceEventClient) CurrentCalendarID() string { return f.calendarID }
func (f *fakeConferenceEventClient) GetEvent(context.Context, string, string) (*googlecalendar.Event, error) {
	return f.event, f.getErr
}
func (f *fakeConferenceEventClient) UpdateEvent(_ context.Context, _ string, _ string, event *googlecalendar.Event) (*googlecalendar.Event, error) {
	f.updated = event
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	return event, nil
}
func (f *fakeConferenceEventClient) AddGoogleMeet(_ context.Context, _ string, _ string, event *googlecalendar.Event) (*googlecalendar.Event, error) {
	f.updated = event
	if f.addMeetErr != nil {
		return nil, f.addMeetErr
	}
	return f.addMeetResult, nil
}
func (f *fakeConferenceEventClient) ClearGoogleMeet(_ context.Context, _ string, _ string, event *googlecalendar.Event) (*googlecalendar.Event, error) {
	f.updated = event
	if f.clearMeetErr != nil {
		return nil, f.clearMeetErr
	}
	return f.clearMeetResult, nil
}

type fakeConferenceProvider struct {
	details *conference.Details
	err     error
}

func (f fakeConferenceProvider) CreateMeeting(context.Context, string, time.Time, time.Time) (*conference.Details, error) {
	return f.details, f.err
}

type fakeConferenceProviderFactory struct {
	provider     conference.Provider
	err          error
	seenProvider string
}

func (f *fakeConferenceProviderFactory) ProviderForRequest(_ context.Context, provider string, _ *storage.Settings) (conference.Provider, error) {
	f.seenProvider = provider
	return f.provider, f.err
}

func setupConferencingRoutes(t *testing.T, client ConferenceEventClient, factory ConferenceProviderFactory) *chi.Mux {
	t.Helper()
	h := newConferencingHandlers(openTestDB(t), nil)
	if client != nil {
		h.loadEventClient = func(context.Context) (ConferenceEventClient, error) { return client, nil }
	}
	if factory != nil {
		h.providerFactory = factory
	}
	r := chi.NewRouter()
	r.Get("/api/conference/providers", h.providers)
	r.Post("/api/events/{id}/conference", h.addEventConference)
	r.Delete("/api/events/{id}/conference", h.removeEventConference)
	return r
}

func TestConferencingProvidersContract(t *testing.T) {
	for _, key := range []string{
		"GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_SECRET", "GOOGLE_REDIRECT_URL",
		"MICROSOFT_CLIENT_ID", "MICROSOFT_CLIENT_SECRET", "MICROSOFT_REDIRECT_URL",
		"ZOOM_CLIENT_ID", "ZOOM_CLIENT_SECRET", "ZOOM_REDIRECT_URL",
	} {
		t.Setenv(key, "configured-for-test")
	}
	t.Setenv("GOOGLE_REDIRECT_URL", "http://localhost:8080/api/auth/callback")
	t.Setenv("MICROSOFT_REDIRECT_URL", "http://localhost:8080/api/auth/microsoft/callback")
	t.Setenv("ZOOM_REDIRECT_URL", "http://localhost:8080/api/auth/zoom/callback")
	userID := createTestUser(t, "conf-providers@example.com")
	r := setupConferencingRoutes(t, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/conference/providers", nil)
	req = withUser(req, userID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var got []conferenceProviderStatus
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 4 || got[0].Provider != "google_meet" || got[3].Provider != "custom" || !got[3].Connected {
		t.Fatalf("providers = %+v", got)
	}
}

func TestAddCustomConferenceContract(t *testing.T) {
	client := &fakeConferenceEventClient{calendarID: "primary", event: &googlecalendar.Event{Summary: "Planning"}}
	r := setupConferencingRoutes(t, client, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/events/evt-1/conference", bytes.NewBufferString(`{"provider":"custom","url":"https://meet.example.test/room"}`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var got conferenceLinkResponse
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Provider != "custom" || got.URL != "https://meet.example.test/room" {
		t.Fatalf("response = %+v", got)
	}
	provider, url := conferenceFromEvent(client.updated)
	if provider != "custom" || url != got.URL {
		t.Fatalf("event conference = %q %q", provider, url)
	}
}

func TestAddGoogleMeetConferenceContract(t *testing.T) {
	updated := &googlecalendar.Event{HangoutLink: "https://meet.google.com/abc-defg-hij"}
	client := &fakeConferenceEventClient{calendarID: "primary", event: &googlecalendar.Event{Summary: "Planning"}, addMeetResult: updated}
	r := setupConferencingRoutes(t, client, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/events/evt-1/conference", bytes.NewBufferString(`{"provider":"google_meet"}`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
}

func TestAddConferenceErrorsContract(t *testing.T) {
	start := "2026-08-12T10:00:00Z"
	end := "2026-08-12T10:30:00Z"
	client := &fakeConferenceEventClient{calendarID: "primary", event: &googlecalendar.Event{Summary: "Planning", Start: &googlecalendar.EventDateTime{DateTime: start}, End: &googlecalendar.EventDateTime{DateTime: end}}}
	factory := &fakeConferenceProviderFactory{err: errors.New("unsupported conference provider \"whereby\"")}
	r := setupConferencingRoutes(t, client, factory)
	tests := []struct {
		name, body, wantContains string
		want                     int
	}{
		{name: "missing provider", body: `{}`, want: http.StatusBadRequest, wantContains: "provider is required"},
		{name: "custom requires url", body: `{"provider":"custom"}`, want: http.StatusBadRequest, wantContains: "url is required for custom conferences"},
		{name: "unsupported provider", body: `{"provider":"whereby"}`, want: http.StatusBadRequest, wantContains: "unsupported conference provider"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/events/evt-1/conference", bytes.NewBufferString(tc.body))
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d; body: %s", w.Code, tc.want, w.Body.String())
			}
			if !bytes.Contains(w.Body.Bytes(), []byte(tc.wantContains)) {
				t.Fatalf("body = %s, want %q", w.Body.String(), tc.wantContains)
			}
		})
	}
}

func TestAddConferenceConflictReturns409(t *testing.T) {
	event := &googlecalendar.Event{ExtendedProperties: &googlecalendar.EventExtendedProperties{Private: map[string]string{"paceday_conference_provider": "zoom", "paceday_conference_url": "https://zoom.us/j/123"}}}
	client := &fakeConferenceEventClient{calendarID: "primary", event: event}
	r := setupConferencingRoutes(t, client, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/events/evt-1/conference", bytes.NewBufferString(`{"provider":"teams"}`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body: %s", w.Code, w.Body.String())
	}
}

func TestRemoveConferenceContract(t *testing.T) {
	event := &googlecalendar.Event{HangoutLink: "https://meet.google.com/abc-defg-hij", ExtendedProperties: &googlecalendar.EventExtendedProperties{Private: map[string]string{"paceday_conference_provider": "zoom", "paceday_conference_url": "https://zoom.us/j/123"}}}
	client := &fakeConferenceEventClient{calendarID: "primary", event: event, clearMeetResult: event}
	r := setupConferencingRoutes(t, client, nil)
	req := httptest.NewRequest(http.MethodDelete, "/api/events/evt-1/conference", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body: %s", w.Code, w.Body.String())
	}
}
