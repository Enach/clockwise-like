package api

import (
	"context"
	"time"

	"github.com/Enach/paceday/backend/calendar"
	"github.com/Enach/paceday/backend/conference"
	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/nlp"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
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

// ManagerWorkflow is the engine surface used by manager HTTP handlers. Keeping
// it behind an interface lets the handlers be tested without calendar/OAuth
// calls while preserving the production engine implementation.
type ManagerWorkflow interface {
	DetectTeam(ctx context.Context, managerID uuid.UUID) (*engine.DetectResult, error)
	GetGaps(ctx context.Context, managerID uuid.UUID) ([]engine.CadenceGap, error)
	GetMemberWeek(ctx context.Context, managerID uuid.UUID, member *storage.ManagerTeamMember, weekStart time.Time) (*engine.MemberWeekStats, error)
}

// TeamAvailability is the engine surface used by formal-team availability and
// analytics handlers.
type TeamAvailability interface {
	FindSlots(ctx context.Context, teamID uuid.UUID, day time.Time, durationMinutes int) ([]engine.TeamSlot, error)
	GetTeamAnalytics(ctx context.Context, teamID uuid.UUID, weekStart time.Time) (*engine.TeamAnalytics, error)
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
