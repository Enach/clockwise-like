package engine

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/calendar"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
	googlecalendar "google.golang.org/api/calendar/v3"
)

const personalBlockerTitle = "[Personal] Busy"
const personalLookAheadDays = 14

// PersonalBlocker syncs events from personal calendars into the work calendar as opaque blockers.
type PersonalBlocker struct {
	DB          *sql.DB
	OAuthConfig *oauth2.Config
	Clock       Clock
}

func (b *PersonalBlocker) now() time.Time {
	if b.Clock != nil {
		return b.Clock.Now()
	}
	return SystemClock.Now()
}

// SyncAll is a thin compatibility wrapper that requires userID in ctx.
// Cron callers must use SyncAllForUser instead.
func (b *PersonalBlocker) SyncAll(ctx context.Context) error {
	userID := auth.UserIDFromContext(ctx)
	if userID == uuid.Nil {
		return fmt.Errorf("PersonalBlocker.SyncAll: userID is required (use SyncAllForUser from cron paths)")
	}
	return b.SyncAllForUser(ctx, userID)
}

// SyncAllForUser syncs every enabled personal calendar owned by the given user.
// Injects userID into ctx so newCalOps / oauth_tokens queries are scoped correctly.
func (b *PersonalBlocker) SyncAllForUser(ctx context.Context, userID uuid.UUID) error {
	if userID == uuid.Nil {
		return fmt.Errorf("PersonalBlocker.SyncAllForUser: userID is required")
	}
	ctx = context.WithValue(ctx, auth.UserIDKey, userID)
	cals, err := storage.ListPersonalCalendarsByUser(b.DB, userID)
	if err != nil {
		return fmt.Errorf("list personal calendars: %w", err)
	}
	for _, cal := range cals {
		if !cal.Enabled {
			continue
		}
		if err := b.syncOne(ctx, &cal); err != nil {
			log.Printf("personal_blocker: sync cal %d (%s): %v", cal.ID, cal.Name, err)
		}
	}
	return nil
}

func (b *PersonalBlocker) Preview(ctx context.Context, calID int64, start, end time.Time) ([]calendar.GenericEvent, error) {
	pc, err := storage.GetPersonalCalendar(b.DB, calID)
	if err != nil {
		return nil, fmt.Errorf("get calendar: %w", err)
	}
	return calendar.ReadPersonalEvents(ctx, pc, start, end)
}

func (b *PersonalBlocker) Sync(ctx context.Context, calID int64) error {
	pc, err := storage.GetPersonalCalendar(b.DB, calID)
	if err != nil {
		return fmt.Errorf("get calendar: %w", err)
	}
	return b.syncOne(ctx, pc)
}

func (b *PersonalBlocker) syncOne(ctx context.Context, pc *storage.PersonalCalendar) error {
	start := b.now().Truncate(24 * time.Hour)
	end := start.AddDate(0, 0, personalLookAheadDays)

	events, err := calendar.ReadPersonalEvents(ctx, pc, start, end)
	if err != nil {
		return fmt.Errorf("read events: %w", err)
	}

	calOps, err := newCalOps(ctx, b.DB, b.OAuthConfig)
	if err != nil {
		return fmt.Errorf("work calendar: %w", err)
	}

	s, _ := storage.GetSettings(b.DB)

	incoming := make(map[string]calendar.GenericEvent, len(events))
	for _, e := range events {
		incoming[e.ID] = e
	}

	existing, err := storage.ListPersonalBlockers(b.DB, pc.ID)
	if err != nil {
		return err
	}

	for _, blocker := range existing {
		if _, found := incoming[blocker.PersonalEventID]; !found {
			_ = calOps.deleteEvent(ctx, blocker.WorkEventID)
			_ = storage.DeletePersonalBlocker(b.DB, pc.ID, blocker.PersonalEventID)
		}
	}

	for _, e := range events {
		if !personalEventDuringWorkHours(e, s) {
			continue
		}
		existing, _ := storage.GetPersonalBlocker(b.DB, pc.ID, e.ID)
		if existing != nil {
			continue
		}
		created, err := calOps.createEvent(ctx, &googlecalendar.Event{
			Summary:     personalBlockerTitle,
			Description: "Auto-created personal time blocker",
			Start:       &googlecalendar.EventDateTime{DateTime: e.Start.UTC().Format(time.RFC3339)},
			End:         &googlecalendar.EventDateTime{DateTime: e.End.UTC().Format(time.RFC3339)},
		})
		if err != nil {
			log.Printf("personal_blocker: create blocker for event %s: %v", e.ID, err)
			continue
		}
		_ = storage.UpsertPersonalBlocker(b.DB, pc.ID, e.ID, created.Id)
	}
	return nil
}

func personalEventDuringWorkHours(e calendar.GenericEvent, s *storage.Settings) bool {
	if s == nil {
		return true
	}
	loc, err := time.LoadLocation(s.Timezone)
	if err != nil {
		loc = time.UTC
	}
	workStartRaw, workEndRaw, enabled := s.WorkWindow(e.Start.In(loc))
	if !enabled {
		return false
	}
	workStart := parseHHMM(workStartRaw, e.Start, loc)
	workEnd := parseHHMM(workEndRaw, e.Start, loc)
	return e.Start.Before(workEnd) && e.End.After(workStart)
}
