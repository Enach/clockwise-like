package calendar

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	googlecalendar "google.golang.org/api/calendar/v3"
	"google.golang.org/api/googleapi"
)

func (c *CalendarClient) ListEvents(ctx context.Context, calendarID string, timeMin, timeMax time.Time) ([]*googlecalendar.Event, error) {
	resp, err := c.service.Events.List(calendarID).
		Context(ctx).
		TimeMin(timeMin.Format(time.RFC3339)).
		TimeMax(timeMax.Format(time.RFC3339)).
		SingleEvents(true).
		OrderBy("startTime").
		Do()
	if err != nil {
		return nil, err
	}
	return resp.Items, nil
}

// maxEventPages bounds ListEventsComplete: a provider that keeps answering a
// page token past this is treated as unreadable rather than looped on.
const maxEventPages = 100

// ErrEventPagesExceeded is ListEventsComplete's answer when the provider's
// pages do not end within maxEventPages.
var ErrEventPagesExceeded = errors.New("calendar: event list did not end within the page limit")

// ListEventsComplete reads every event in [timeMin, timeMax), following the
// provider's nextPageToken until the last page (PAC-88 contract section 2.3:
// the one read per host covers a window of up to 44 days, past Google's
// default page). Any page failing is the error: a partial list is never
// returned, so the caller treats the calendar as unreadable (PAC-72 D-6).
// ctx carries the caller's per-host limit across all pages. ListEvents keeps
// its first-page behaviour for its existing callers.
func (c *CalendarClient) ListEventsComplete(ctx context.Context, calendarID string, timeMin, timeMax time.Time) ([]*googlecalendar.Event, error) {
	items := make([]*googlecalendar.Event, 0)
	token := ""
	for page := 0; page < maxEventPages; page++ {
		call := c.service.Events.List(calendarID).
			Context(ctx).
			TimeMin(timeMin.Format(time.RFC3339)).
			TimeMax(timeMax.Format(time.RFC3339)).
			SingleEvents(true).
			OrderBy("startTime")
		if token != "" {
			call = call.PageToken(token)
		}
		resp, err := call.Do()
		if err != nil {
			return nil, err
		}
		items = append(items, resp.Items...)
		if resp.NextPageToken == "" {
			return items, nil
		}
		token = resp.NextPageToken
	}
	return nil, ErrEventPagesExceeded
}

func (c *CalendarClient) CreateEvent(ctx context.Context, calendarID string, event *googlecalendar.Event) (*googlecalendar.Event, error) {
	return c.service.Events.Insert(calendarID, event).Context(ctx).Do()
}

func (c *CalendarClient) UpdateEvent(ctx context.Context, calendarID, eventID string, event *googlecalendar.Event) (*googlecalendar.Event, error) {
	return c.service.Events.Update(calendarID, eventID, event).Context(ctx).Do()
}

// DeclineEvent updates the current user's RSVP and notifies the other
// attendees. The caller must have already verified that the event is an
// incoming invitation that is still pending or tentative.
func (c *CalendarClient) DeclineEvent(ctx context.Context, calendarID, eventID string, event *googlecalendar.Event) (*googlecalendar.Event, error) {
	return c.service.Events.Update(calendarID, eventID, event).
		Context(ctx).
		SendUpdates("all").
		Do()
}

func (c *CalendarClient) DeleteEvent(ctx context.Context, calendarID, eventID string) error {
	return c.service.Events.Delete(calendarID, eventID).Context(ctx).Do()
}

// UpdateEventSendUpdates writes the event like UpdateEvent and passes
// sendUpdates ("all" or "none") to Google, which decides whether the guests
// are emailed about the change (PAC-29, API-018).
func (c *CalendarClient) UpdateEventSendUpdates(ctx context.Context, calendarID, eventID string, event *googlecalendar.Event, sendUpdates string) (*googlecalendar.Event, error) {
	return c.service.Events.Update(calendarID, eventID, event).
		Context(ctx).
		SendUpdates(sendUpdates).
		Do()
}

// DeleteEventSendUpdates deletes the event like DeleteEvent and passes
// sendUpdates ("all" or "none") to Google (PAC-29, API-042).
func (c *CalendarClient) DeleteEventSendUpdates(ctx context.Context, calendarID, eventID, sendUpdates string) error {
	return c.service.Events.Delete(calendarID, eventID).
		Context(ctx).
		SendUpdates(sendUpdates).
		Do()
}

// IsEventGone reports whether err is Google saying the event does not exist:
// a 404, or a 410 for one that was deleted (PAC-29, API-045). Any other
// failure (transport, permission, quota) is not.
func IsEventGone(err error) bool {
	var gerr *googleapi.Error
	if !errors.As(err, &gerr) {
		return false
	}
	return gerr.Code == http.StatusNotFound || gerr.Code == http.StatusGone
}

func (c *CalendarClient) GetEvent(ctx context.Context, calendarID, eventID string) (*googlecalendar.Event, error) {
	return c.service.Events.Get(calendarID, eventID).Context(ctx).Do()
}

func (c *CalendarClient) AddGoogleMeet(ctx context.Context, calendarID, eventID string, event *googlecalendar.Event) (*googlecalendar.Event, error) {
	event.ConferenceData = &googlecalendar.ConferenceData{
		CreateRequest: &googlecalendar.CreateConferenceRequest{
			RequestId:             uuid.NewString(),
			ConferenceSolutionKey: &googlecalendar.ConferenceSolutionKey{Type: "hangoutsMeet"},
		},
	}
	return c.service.Events.Update(calendarID, eventID, event).
		Context(ctx).ConferenceDataVersion(1).Do()
}

func (c *CalendarClient) ClearGoogleMeet(ctx context.Context, calendarID, eventID string, event *googlecalendar.Event) (*googlecalendar.Event, error) {
	event.ConferenceData = nil
	event.NullFields = append(event.NullFields, "ConferenceData")
	return c.service.Events.Update(calendarID, eventID, event).
		Context(ctx).ConferenceDataVersion(1).Do()
}

func ConferenceEntryURL(event *googlecalendar.Event) string {
	if event == nil || event.ConferenceData == nil {
		return ""
	}
	for _, entry := range event.ConferenceData.EntryPoints {
		if entry != nil && entry.Uri != "" && (entry.EntryPointType == "video" || entry.EntryPointType == "") {
			return entry.Uri
		}
	}
	return ""
}
