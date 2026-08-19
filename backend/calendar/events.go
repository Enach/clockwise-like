package calendar

import (
	"context"
	"time"

	"github.com/google/uuid"
	googlecalendar "google.golang.org/api/calendar/v3"
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
