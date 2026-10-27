package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/calendar"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
	googlecalendar "google.golang.org/api/calendar/v3"
)

// ErrCalendarNotConnected means the user has no stored Google Calendar
// credential, so an operation that must read or write their calendar cannot
// run. The HTTP layer answers it with the 401 CalendarNotConnected (PAC-21,
// API-054). Its text is fixed and names no user.
var ErrCalendarNotConnected = errors.New("calendar not connected")

// ErrCredentialStore wraps a failure to READ the stored credential. It is a
// server failure (the operation's 500), never "not connected": reporting a
// database outage as "connect your calendar" would send the user through
// OAuth for nothing.
var ErrCredentialStore = errors.New("read stored calendar credential")

// LoadCalendarToken loads the user's stored Google credential, telling a
// missing one (ErrCalendarNotConnected) apart from a store that could not be
// read (ErrCredentialStore, wrapping the cause for the server log).
func LoadCalendarToken(db *sql.DB, userID uuid.UUID) (*oauth2.Token, error) {
	token, err := auth.LoadUserToken(db, userID)
	if errors.Is(err, auth.ErrNoUser) {
		return nil, ErrCalendarNotConnected
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCredentialStore, err)
	}
	if token == nil {
		return nil, ErrCalendarNotConnected
	}
	return token, nil
}

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
	token, err := LoadCalendarToken(db, auth.UserIDFromContext(ctx))
	if err != nil {
		return nil, err
	}
	ts := auth.TokenSource(ctx, oauthConfig, token)
	client, err := calendar.NewClient(ctx, ts)
	if err != nil {
		return nil, err
	}
	return realOps{c: client}, nil
}
