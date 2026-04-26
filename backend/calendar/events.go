package calendar

import (
	"context"
	"time"

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

func (c *CalendarClient) DeleteEvent(ctx context.Context, calendarID, eventID string) error {
	return c.service.Events.Delete(calendarID, eventID).Context(ctx).Do()
}

func (c *CalendarClient) GetEvent(ctx context.Context, calendarID, eventID string) (*googlecalendar.Event, error) {
	return c.service.Events.Get(calendarID, eventID).Context(ctx).Do()
}
