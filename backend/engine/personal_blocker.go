package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/calendar"
	"github.com/Enach/paceday/backend/internal/lifecycle"
	"github.com/Enach/paceday/backend/internal/peritem"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
	googlecalendar "google.golang.org/api/calendar/v3"
)

const personalBlockerTitle = "[Personal] Busy"
const personalBlockerDescription = "Auto-created personal time blocker"
const personalLookAheadDays = 14

// personalBlockerMarker is the private extended property set on every
// blocker the sync creates (PAC-122 SF-035), so a blocker is recognised by
// what Paceday wrote rather than by a title anyone can type.
const personalBlockerMarker = "paceday_personal_blocker"

// IsPersonalBlocker reports whether e is a "[Personal] Busy" blocker the
// personal-calendar sync created: it carries the private marker, or (a
// blocker created before the marker existed) both the blocker's title and
// its description.
func IsPersonalBlocker(e *googlecalendar.Event) bool {
	if e == nil {
		return false
	}
	if e.ExtendedProperties != nil && e.ExtendedProperties.Private[personalBlockerMarker] == "1" {
		return true
	}
	return e.Summary == personalBlockerTitle && e.Description == personalBlockerDescription
}

// getPersonalBlocker is storage.GetPersonalBlocker; a test replaces it to
// make the lookup fail (PAC-122 SF-021).
var getPersonalBlocker = storage.GetPersonalBlocker

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
	cals, err := storage.ListPersonalCalendarsByUserCtx(ctx, storage.Bind(ctx, b.DB), userID)
	if err != nil {
		return fmt.Errorf("list personal calendars: %w", err)
	}
	for _, cal := range cals {
		if !cal.Enabled {
			continue
		}
		if err := b.syncOne(ctx, &cal); err != nil {
			log.Printf("personal_blocker: sync cal %d (%s): %v", cal.ID, cal.Name, err)
			// PAC-122 SF-042 (reporting half): the failure reaches error
			// monitoring by calendar id (never its name); the user's other
			// calendars still sync and the result is unchanged.
			lifecycle.Report(lifecycle.Unit{Kind: "personal-calendar-sync", ID: strconv.FormatInt(cal.ID, 10)},
				fmt.Errorf("personal_blocker: sync cal %d for user %s: %w", cal.ID, userID, err))
		}
	}
	return nil
}

func (b *PersonalBlocker) Preview(ctx context.Context, calID int64, start, end time.Time) ([]calendar.GenericEvent, error) {
	pc, err := storage.GetPersonalCalendarCtx(ctx, storage.Bind(ctx, b.DB), calID)
	if err != nil {
		return nil, fmt.Errorf("get calendar: %w", err)
	}
	events, err := calendar.ReadPersonalEvents(ctx, pc, start, end)
	if err != nil {
		return nil, err
	}
	// An unreadable feed event has no time to preview (PAC-122 SF-044).
	readable := events[:0]
	for _, e := range events {
		if !e.Unreadable() {
			readable = append(readable, e)
		}
	}
	return readable, nil
}

func (b *PersonalBlocker) Sync(ctx context.Context, calID int64) error {
	pc, err := storage.GetPersonalCalendarCtx(ctx, storage.Bind(ctx, b.DB), calID)
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

	// PAC-122 SF-021: without the user's settings there are no work hours to
	// test, and a nil settings read as "always during work hours" (blockers
	// outside work hours). The sync stops instead.
	s, err := storage.GetOrCreateSettingsByUserCtx(ctx, db, pc.UserID)
	if err != nil {
		return fmt.Errorf("read settings: %w", err)
	}

	incoming := make(map[string]calendar.GenericEvent, len(events))
	var unreadable []string
	for _, e := range events {
		if e.Unreadable() {
			unreadable = append(unreadable, e.ID)
			continue
		}
		incoming[e.ID] = e
	}
	if len(unreadable) > 0 {
		// PAC-122 SF-044: the calendar id and a count only (never a UID or a
		// title, which are the user's).
		log.Printf("personal_blocker: cal %d: %d feed event(s) unreadable; their blockers are kept", pc.ID, len(unreadable))
	}

	existing, err := storage.ListPersonalBlockers(db, pc.ID)
	if err != nil {
		return err
	}

	var stale []storage.PersonalBlocker
	for _, blocker := range existing {
		if _, found := incoming[blocker.PersonalEventID]; found || coveredByUnreadable(blocker.PersonalEventID, unreadable) {
			continue
		}
		stale = append(stale, blocker)
	}

	// PAC-122 SF-020: a blocker's row is deleted only once its work-calendar
	// event is gone (deleted now, or already absent: 404/410). When the event
	// delete fails the row stays, so the next sync retries it, and the
	// failure is this sync's error (reported per calendar by SyncAllForUser).
	var mu sync.Mutex
	var failed []error
	fail := func(err error) error {
		mu.Lock()
		failed = append(failed, err)
		mu.Unlock()
		return err
	}
	limit := HostReadLimit()
	if err := peritem.Run(ctx, len(stale), peritem.Read, limit, func(callCtx context.Context, i int) error {
		if err := calOps.deleteEvent(callCtx, stale[i].WorkEventID); err != nil && !calendar.IsEventGone(err) {
			return fail(fmt.Errorf("delete blocker %d event: %w", stale[i].ID, err))
		}
		if err := storage.DeletePersonalBlockerCtx(ctx, record, pc.ID, stale[i].PersonalEventID); err != nil {
			return fail(fmt.Errorf("delete blocker %d row: %w", stale[i].ID, err))
		}
		return nil
	}); err != nil {
		return err
	}

	var missing []calendar.GenericEvent
	for _, e := range events {
		if e.Unreadable() || !e.End.After(e.Start) || !personalEventDuringWorkHours(e, s) {
			continue
		}
		// PAC-122 SF-021: a failed lookup is not "no blocker yet" (that
		// created a duplicate); the sync stops before creating anything.
		found, err := getPersonalBlocker(db, pc.ID, e.ID)
		if err != nil {
			return errors.Join(append(failed, fmt.Errorf("look up blocker: %w", err))...)
		}
		if found != nil {
			continue
		}
		missing = append(missing, e)
	}
	if err := peritem.Run(ctx, len(missing), peritem.Create, limit, func(callCtx context.Context, i int) error {
		e := missing[i]
		created, err := calOps.createEvent(callCtx, &googlecalendar.Event{
			Summary:     personalBlockerTitle,
			Description: personalBlockerDescription,
			Start:       &googlecalendar.EventDateTime{DateTime: e.Start.UTC().Format(time.RFC3339)},
			End:         &googlecalendar.EventDateTime{DateTime: e.End.UTC().Format(time.RFC3339)},
			ExtendedProperties: &googlecalendar.EventExtendedProperties{
				Private: map[string]string{personalBlockerMarker: "1"},
			},
		})
		if err != nil {
			log.Printf("personal_blocker: cal %d: create blocker: %v", pc.ID, err)
			return err
		}
		return storage.UpsertPersonalBlockerCtx(ctx, record, pc.ID, e.ID, created.Id)
	}); err != nil {
		return err
	}
	return errors.Join(failed...)
}

// coveredByUnreadable reports whether a blocker for personalEventID belongs
// to an event the feed carries but could not read: the event itself (its
// UID) or one of its instances ("<UID>/..."). Its blocker is kept.
func coveredByUnreadable(personalEventID string, unreadable []string) bool {
	for _, uid := range unreadable {
		if personalEventID == uid || strings.HasPrefix(personalEventID, uid+"/") {
			return true
		}
	}
	return false
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
