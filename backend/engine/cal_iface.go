package engine

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Enach/clockwise-like/backend/auth"
	"github.com/Enach/clockwise-like/backend/calendar"
	googlecalendar "google.golang.org/api/calendar/v3"
	"golang.org/x/oauth2"
)

type calendarOps interface {
	calendarID() string
	listEvents(ctx context.Context, timeMin, timeMax time.Time) ([]*googlecalendar.Event, error)
	createEvent(ctx context.Context, event *googlecalendar.Event) (*googlecalendar.Event, error)
	updateEvent(ctx context.Context, eventID string, event *googlecalendar.Event) (*googlecalendar.Event, error)
	deleteEvent(ctx context.Context, eventID string) error
	getEvent(ctx context.Context, eventID string) (*googlecalendar.Event, error)
	getFreeBusy(ctx context.Context, emails []string, timeMin, timeMax time.Time) (map[string][]calendar.TimeSlot, error)
}

type realOps struct{ c *calendar.CalendarClient }

func (r realOps) calendarID() string { return r.c.CalendarID }

func (r realOps) listEvents(ctx context.Context, tMin, tMax time.Time) ([]*googlecalendar.Event, error) {
	return r.c.ListEvents(ctx, r.c.CalendarID, tMin, tMax)
}

func (r realOps) createEvent(ctx context.Context, event *googlecalendar.Event) (*googlecalendar.Event, error) {
	return r.c.CreateEvent(ctx, r.c.CalendarID, event)
}

func (r realOps) updateEvent(ctx context.Context, eventID string, event *googlecalendar.Event) (*googlecalendar.Event, error) {
	return r.c.UpdateEvent(ctx, r.c.CalendarID, eventID, event)
}

func (r realOps) deleteEvent(ctx context.Context, eventID string) error {
	return r.c.DeleteEvent(ctx, r.c.CalendarID, eventID)
}

func (r realOps) getEvent(ctx context.Context, eventID string) (*googlecalendar.Event, error) {
	return r.c.GetEvent(ctx, r.c.CalendarID, eventID)
}

func (r realOps) getFreeBusy(ctx context.Context, emails []string, tMin, tMax time.Time) (map[string][]calendar.TimeSlot, error) {
	return r.c.GetFreeBusy(ctx, emails, tMin, tMax)
}

func newCalOps(ctx context.Context, db *sql.DB, oauthConfig *oauth2.Config) (calendarOps, error) {
	token, err := auth.TokenFromDB(db)
	if err != nil || token == nil {
		return nil, fmt.Errorf("not authenticated")
	}
	ts := auth.TokenSource(ctx, oauthConfig, token)
	client, err := calendar.NewClient(ctx, ts)
	if err != nil {
		return nil, err
	}
	return realOps{c: client}, nil
}
