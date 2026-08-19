package engine

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/calendar"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
	googlecalendar "google.golang.org/api/calendar/v3"
)

const AutoDeclineWorkingDays = 10

// AutoDeclineService declines incoming invitations that fall outside a user's
// working hours or protected lunch. It is deliberately opt-in through the
// per-user setting and only acts on pending/tentative RSVP states.
type AutoDeclineService struct {
	DB          *sql.DB
	OAuthConfig *oauth2.Config
}

// RunAll applies the policy to every user who explicitly enabled it.
func (s *AutoDeclineService) RunAll(ctx context.Context) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("auto-decline: database is required")
	}
	userIDs, err := storage.ListUsersWithAutoDecline(s.DB)
	if err != nil {
		return err
	}

	var firstErr error
	for _, userID := range userIDs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		count, runErr := s.RunForUser(ctx, userID, time.Now())
		if runErr != nil {
			log.Printf("auto-decline for user %s: %v", userID, runErr)
			if firstErr == nil {
				firstErr = runErr
			}
			continue
		}
		if count > 0 {
			log.Printf("auto-decline for user %s: declined %d invitation(s)", userID, count)
		}
	}
	return firstErr
}

// RunForUser scans the next ten working days and sends declined RSVP updates
// for eligible incoming invitations. It returns the number of successful
// declines.
func (s *AutoDeclineService) RunForUser(ctx context.Context, userID uuid.UUID, now time.Time) (int, error) {
	if s == nil || s.DB == nil {
		return 0, fmt.Errorf("auto-decline: database is required")
	}
	settings, err := storage.GetSettingsByUser(s.DB, userID)
	if err != nil {
		return 0, err
	}
	if settings == nil || !settings.AutoDeclineOutsideWorkingHours {
		return 0, nil
	}
	if s.OAuthConfig == nil {
		return 0, fmt.Errorf("auto-decline: OAuth configuration is required")
	}

	token, err := auth.LoadUserToken(s.DB, userID)
	if err != nil {
		return 0, err
	}
	if token == nil {
		return 0, fmt.Errorf("auto-decline: no calendar token for user %s", userID)
	}

	loc := settingsLocation(settings.Timezone)
	start, end := nextWorkingDaysWindow(now.In(loc), AutoDeclineWorkingDays)
	client, err := calendar.NewClient(ctx, auth.TokenSource(ctx, s.OAuthConfig, token))
	if err != nil {
		return 0, err
	}
	calendarID := "primary"
	events, err := client.ListEvents(ctx, calendarID, start, end)
	if err != nil {
		return 0, err
	}

	declined := 0
	var firstErr error
	for _, event := range events {
		self := pendingSelfAttendee(event)
		if !shouldAutoDecline(event, settings, loc) || self == nil {
			continue
		}
		self.ResponseStatus = "declined"
		if _, err := client.DeclineEvent(ctx, calendarID, event.Id, event); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		declined++
	}
	return declined, firstErr
}

// nextWorkingDaysWindow returns [now, midnight after the Nth weekday]. The
// current day counts when it is Monday-Friday; weekends do not count.
func nextWorkingDaysWindow(now time.Time, days int) (time.Time, time.Time) {
	if days <= 0 {
		return now, now
	}
	cursor := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	counted := 0
	for {
		if isWorkingDay(cursor.Weekday()) {
			counted++
			if counted == days {
				return now, cursor.AddDate(0, 0, 1)
			}
		}
		cursor = cursor.AddDate(0, 0, 1)
	}
}

func isWorkingDay(day time.Weekday) bool {
	return day >= time.Monday && day <= time.Friday
}

func settingsLocation(name string) *time.Location {
	if name == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.UTC
	}
	return loc
}

func pendingSelfAttendee(event *googlecalendar.Event) *googlecalendar.EventAttendee {
	if event == nil {
		return nil
	}
	for _, attendee := range event.Attendees {
		if attendee == nil || !attendee.Self || attendee.Organizer {
			continue
		}
		status := strings.ToLower(attendee.ResponseStatus)
		if status == "needsaction" || status == "tentative" || status == "pending" {
			return attendee
		}
	}
	return nil
}

func shouldAutoDecline(event *googlecalendar.Event, settings *storage.Settings, loc *time.Location) bool {
	if event == nil || settings == nil || event.Status == "cancelled" {
		return false
	}
	// An organizer owns the invitation; declining it would mutate an event the
	// user controls rather than responding to an incoming invitation.
	if event.Organizer == nil || event.Organizer.Self {
		return false
	}
	if pendingSelfAttendee(event) == nil || event.Start == nil || event.End == nil {
		return false
	}
	// Google all-day events use Date, not DateTime. They are not safely
	// comparable to working-hour intervals and are intentionally untouched.
	if event.Start.Date != "" || event.End.Date != "" || event.Start.DateTime == "" || event.End.DateTime == "" {
		return false
	}
	start, err := time.Parse(time.RFC3339, event.Start.DateTime)
	if err != nil {
		return false
	}
	end, err := time.Parse(time.RFC3339, event.End.DateTime)
	if err != nil || !end.After(start) {
		return false
	}
	start = start.In(loc)
	end = end.In(loc)

	workStartRaw, workEndRaw, enabled := settings.WorkWindow(start)
	if !enabled {
		return true
	}
	workStart, ok := policyClock(workStartRaw, start)
	if !ok {
		return false
	}
	workEnd, ok := policyClock(workEndRaw, start)
	if !ok {
		return false
	}
	if start.Before(workStart) || end.After(workEnd) {
		return true
	}

	lunchStartRaw, lunchEndRaw, lunchEnabled := settings.LunchWindow(start)
	if !lunchEnabled {
		return false
	}
	lunchStart, ok := policyClock(lunchStartRaw, start)
	if !ok {
		return false
	}
	lunchEnd, ok := policyClock(lunchEndRaw, start)
	if !ok {
		return false
	}
	return start.Before(lunchEnd) && end.After(lunchStart)
}

func policyClock(value string, date time.Time) (time.Time, bool) {
	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return time.Time{}, false
	}
	hour, hourErr := time.ParseInLocation("15", parts[0], date.Location())
	minute, minuteErr := time.ParseInLocation("04", parts[1], date.Location())
	if hourErr != nil || minuteErr != nil {
		return time.Time{}, false
	}
	return time.Date(date.Year(), date.Month(), date.Day(), hour.Hour(), minute.Minute(), 0, 0, date.Location()), true
}
