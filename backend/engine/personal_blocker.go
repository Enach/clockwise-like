package engine

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/calendar"
	"github.com/Enach/paceday/backend/internal/peritem"
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
	pc, err := storage.GetPersonalCalendar(storage.Bind(ctx, b.DB), calID)
	if err != nil {
		return fmt.Errorf("get calendar: %w", err)
	}
	return b.syncOne(ctx, pc)
}

// syncOne mirrors one personal calendar into blockers on the work calendar.
//
// PAC-81 AC-A17: deletes of stale blockers and creates of new ones run on the
// bounded per-item pool. A create starts only with its full limit plus the
// stop margin left before the budget or the drain ends, is never cut short,
// and is recorded whenever the provider's answer arrives (on a context the
// budget does not cancel). When the budget stops the sync early, syncOne
// returns budget.ErrExpired and the caller does not stamp last_synced_at; a
// re-run finds the recorded blockers and creates only the missing ones.
func (b *PersonalBlocker) syncOne(ctx context.Context, pc *storage.PersonalCalendar) error {
	start := b.now().Truncate(24 * time.Hour)
	end := start.AddDate(0, 0, personalLookAheadDays)

	events, err := calendar.ReadPersonalEvents(ctx, pc, start, end)
	if err != nil {
		return fmt.Errorf("read events: %w", err)
	}

	db := storage.Bind(ctx, b.DB)
	record := storage.Bind(context.WithoutCancel(ctx), b.DB)
	calOps, err := newCalOps(ctx, db, b.OAuthConfig)
	if err != nil {
		return fmt.Errorf("work calendar: %w", err)
	}

	s, _ := storage.GetOrCreateSettingsByUser(db, pc.UserID)

	incoming := make(map[string]calendar.GenericEvent, len(events))
	for _, e := range events {
		incoming[e.ID] = e
	}

	existing, err := storage.ListPersonalBlockers(db, pc.ID)
	if err != nil {
		return err
	}

	var stale []storage.PersonalBlocker
	for _, blocker := range existing {
		if _, found := incoming[blocker.PersonalEventID]; !found {
			stale = append(stale, blocker)
		}
	}
	limit := HostReadLimit()
	if err := peritem.Run(ctx, len(stale), peritem.Read, limit, func(callCtx context.Context, i int) error {
		_ = calOps.deleteEvent(callCtx, stale[i].WorkEventID)
		return storage.DeletePersonalBlocker(record, pc.ID, stale[i].PersonalEventID)
	}); err != nil {
		return err
	}

	var missing []calendar.GenericEvent
	for _, e := range events {
		if !personalEventDuringWorkHours(e, s) {
			continue
		}
		if found, _ := storage.GetPersonalBlocker(db, pc.ID, e.ID); found != nil {
			continue
		}
		missing = append(missing, e)
	}
	return peritem.Run(ctx, len(missing), peritem.Create, limit, func(callCtx context.Context, i int) error {
		e := missing[i]
		created, err := calOps.createEvent(callCtx, &googlecalendar.Event{
			Summary:     personalBlockerTitle,
			Description: "Auto-created personal time blocker",
			Start:       &googlecalendar.EventDateTime{DateTime: e.Start.UTC().Format(time.RFC3339)},
			End:         &googlecalendar.EventDateTime{DateTime: e.End.UTC().Format(time.RFC3339)},
		})
		if err != nil {
			log.Printf("personal_blocker: create blocker for event %s: %v", e.ID, err)
			return err
		}
		return storage.UpsertPersonalBlocker(record, pc.ID, e.ID, created.Id)
	})
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
