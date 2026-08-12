package api

import (
	"context"
	"time"

	"github.com/Enach/paceday/backend/calendar"
	"github.com/Enach/paceday/backend/conference"
	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/nlp"
	"github.com/Enach/paceday/backend/storage"
	googlecalendar "google.golang.org/api/calendar/v3"
)

type FocusEngine interface {
	Run(ctx context.Context, targetWeek time.Time) (*engine.FocusRunResult, error)
	ClearWeek(ctx context.Context, targetWeek time.Time) (int, error)
}

type Compressor interface {
	SuggestForDay(ctx context.Context, date time.Time) (*engine.CompressionResult, error)
	Apply(ctx context.Context, proposals []engine.MoveProposal) (applied []string, failed []string, err error)
}

type Scheduler interface {
	Suggest(ctx context.Context, req engine.ScheduleRequest) (*engine.ScheduleSuggestions, error)
	CreateMeeting(ctx context.Context, req engine.ScheduleRequest, slot engine.SuggestedSlot) (*googlecalendar.Event, error)
}

type NLPParser interface {
	Parse(ctx context.Context, text string) (*nlp.ParseResult, error)
}

type PersonalCalendarBlocker interface {
	Preview(ctx context.Context, personalCalendarID int64, start, end time.Time) ([]calendar.GenericEvent, error)
	Sync(ctx context.Context, personalCalendarID int64) error
}

type BookingFlow interface {
	CollectiveSlots(ctx context.Context, link *storage.SchedulingLink, date time.Time, durationMinutes int) ([]engine.AvailableSlot, error)
	ConfirmBooking(ctx context.Context, link *storage.SchedulingLink, bookerName, bookerEmail string, start, end time.Time, notes string) (*storage.Booking, error)
}

type ConferenceEventClient interface {
	CurrentCalendarID() string
	GetEvent(ctx context.Context, calendarID, eventID string) (*googlecalendar.Event, error)
	UpdateEvent(ctx context.Context, calendarID, eventID string, event *googlecalendar.Event) (*googlecalendar.Event, error)
	AddGoogleMeet(ctx context.Context, calendarID, eventID string, event *googlecalendar.Event) (*googlecalendar.Event, error)
	ClearGoogleMeet(ctx context.Context, calendarID, eventID string, event *googlecalendar.Event) (*googlecalendar.Event, error)
}

type ConferenceProviderFactory interface {
	ProviderForRequest(ctx context.Context, provider string, settings *storage.Settings) (conference.Provider, error)
}
