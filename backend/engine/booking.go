package engine

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/smtp"
	"os"
	"strings"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/calendar"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
	googlecalendar "google.golang.org/api/calendar/v3"
)

// AvailableSlot is a candidate booking slot.
type AvailableSlot struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// BookingEngine handles collective availability and booking flows.
type BookingEngine struct {
	DB          *sql.DB
	OAuthConfig *oauth2.Config
	// Reads are the storage reads behind a host's busy time. The zero value
	// uses the storage package; tests replace one read to inject a storage
	// failure (PAC-72 AC-17).
	Reads BookingReads
}

// BookingReads are the storage reads CollectiveSlots makes. A nil field means
// the storage package function of the same purpose.
type BookingReads struct {
	AcceptedHosts func(db *sql.DB, linkID uuid.UUID) ([]*storage.LinkHost, error)
	FocusBlocks   func(db *sql.DB, userID uuid.UUID, weekStart time.Time) ([]storage.FocusBlock, error)
	HostBookings  func(db *sql.DB, userID uuid.UUID, start, end time.Time) ([]time.Time, error)
}

func (r BookingReads) acceptedHosts(db *sql.DB, linkID uuid.UUID) ([]*storage.LinkHost, error) {
	if r.AcceptedHosts != nil {
		return r.AcceptedHosts(db, linkID)
	}
	return storage.GetAcceptedHosts(db, linkID)
}

func (r BookingReads) focusBlocks(db *sql.DB, userID uuid.UUID, weekStart time.Time) ([]storage.FocusBlock, error) {
	if r.FocusBlocks != nil {
		return r.FocusBlocks(db, userID, weekStart)
	}
	return storage.ListFocusBlocksForUserWeek(db, userID, weekStart)
}

func (r BookingReads) hostBookings(db *sql.DB, userID uuid.UUID, start, end time.Time) ([]time.Time, error) {
	if r.HostBookings != nil {
		return r.HostBookings(db, userID, start, end)
	}
	return storage.GetBusyBookingsForUser(db, userID, start, end)
}

// --- Slug generation -------------------------------------------------------

// GenerateSlug creates a URL-safe slug for a scheduling link.
// Format: {first-name}-{duration}min, e.g. "nicolas-30min".
// Appends a 4-char random suffix on collision.
func (e *BookingEngine) GenerateSlug(ownerName string, durationMinutes int) (string, error) {
	first := strings.ToLower(strings.Fields(ownerName)[0])
	first = slugify(first)
	base := fmt.Sprintf("%s-%dmin", first, durationMinutes)

	candidate := base
	for range 10 {
		exists, err := storage.SlugExists(e.DB, candidate)
		if err != nil {
			return "", err
		}
		if !exists {
			return candidate, nil
		}
		b := make([]byte, 2)
		_, _ = rand.Read(b)
		candidate = base + "-" + hex.EncodeToString(b)
	}
	return "", fmt.Errorf("could not generate unique slug")
}

func slugify(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else if r == '-' || r == '_' || r == ' ' {
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// --- Collective availability -----------------------------------------------

// CollectiveSlots returns time slots where ALL accepted hosts are simultaneously
// free, every 15 minutes from the link's window start on date, in date's
// location. It is the busy-time half of the offerable-start rule that the
// public slot listing and the public booking share (PAC-72): a link with no
// accepted host offers nothing (D-10), and a storage failure while reading the
// hosts, a host's focus blocks or the bookings on a host's links is an error,
// never "free" (D-7). Only a host whose external calendar cannot be read is
// treated as free for calendar events (D-6).
func (e *BookingEngine) CollectiveSlots(
	ctx context.Context,
	link *storage.SchedulingLink,
	date time.Time,
	durationMinutes int,
) ([]AvailableSlot, error) {
	hosts, err := e.Reads.acceptedHosts(e.DB, link.ID)
	if err != nil {
		return nil, err
	}
	if len(hosts) == 0 {
		return []AvailableSlot{}, nil
	}

	// Parse link window for the given date.
	windowStart := parseTime(link.WindowStart, date)
	windowEnd := parseTime(link.WindowEnd, date)

	// Collect merged busy intervals across all accepted hosts.
	var allBusy []interval
	for _, host := range hosts {
		busy, err := e.hostBusy(ctx, host.UserID, windowStart, windowEnd, link)
		if err != nil {
			return nil, err
		}
		allBusy = append(allBusy, busy...)
	}
	merged := mergeIntervals(allBusy)

	// Candidate slots every 15 minutes.
	dur := time.Duration(durationMinutes) * time.Minute
	var slots []AvailableSlot
	for t := windowStart; t.Add(dur).Before(windowEnd) || t.Add(dur).Equal(windowEnd); t = t.Add(15 * time.Minute) {
		slotEnd := t.Add(dur)
		iv := interval{start: t, end: slotEnd}
		if !overlapsMerged(iv, merged) {
			slots = append(slots, AvailableSlot{Start: t, End: slotEnd})
		}
	}
	return slots, nil
}

func (e *BookingEngine) hostBusy(
	ctx context.Context,
	userID uuid.UUID,
	start, end time.Time,
	link *storage.SchedulingLink,
) ([]interval, error) {
	var busy []interval

	// Calendar events from Google/Outlook. A calendar that cannot be read
	// (no credential, provider error) leaves the host free for events
	// (PAC-72 D-6).
	if calClient, err := e.calendarClientForUser(ctx, userID); err == nil {
		events, err := calClient.ListEvents(ctx, calClient.CalendarID, start, end)
		if err == nil {
			for _, ev := range events {
				if ev.Transparency == "transparent" {
					continue
				}
				s, e2 := parseEventTime(ev)
				if !s.IsZero() && !e2.IsZero() {
					busy = append(busy, interval{start: s, end: e2})
				}
			}
		}
	}

	// Buffer time around each event.
	if link.BufferBefore > 0 || link.BufferAfter > 0 {
		buffered := make([]interval, len(busy))
		for i, b := range busy {
			buffered[i] = interval{
				start: b.start.Add(-time.Duration(link.BufferBefore) * time.Minute),
				end:   b.end.Add(time.Duration(link.BufferAfter) * time.Minute),
			}
		}
		busy = append(busy, buffered...)
	}

	// Focus blocks from DB.
	// Only this host's focus blocks (PAC-64): another user's focus time does
	// not make this host busy.
	blocks, err := e.Reads.focusBlocks(e.DB, userID, start)
	if err != nil {
		return nil, fmt.Errorf("focus blocks: %w", err)
	}
	for _, fb := range blocks {
		busy = append(busy, interval{start: fb.StartTime, end: fb.EndTime})
	}

	// Bookings that are not cancelled, on any link this user hosts (PAC-72
	// D-11).
	times, err := e.Reads.hostBookings(e.DB, userID, start, end)
	if err != nil {
		return nil, fmt.Errorf("host bookings: %w", err)
	}
	for i := 0; i+1 < len(times); i += 2 {
		busy = append(busy, interval{start: times[i], end: times[i+1]})
	}

	return busy, nil
}

func overlapsMerged(iv interval, merged []interval) bool {
	for _, b := range merged {
		if iv.start.Before(b.end) && iv.end.After(b.start) {
			return true
		}
	}
	return false
}

func parseTime(hhmm string, date time.Time) time.Time {
	parts := strings.SplitN(hhmm, ":", 2)
	if len(parts) != 2 {
		return date
	}
	var h, m int
	_, _ = fmt.Sscanf(parts[0], "%d", &h)
	_, _ = fmt.Sscanf(parts[1], "%d", &m)
	return time.Date(date.Year(), date.Month(), date.Day(), h, m, 0, 0, date.Location())
}

func parseEventTime(ev *googlecalendar.Event) (time.Time, time.Time) {
	if ev.Start == nil || ev.End == nil {
		return time.Time{}, time.Time{}
	}
	parse := func(s string) time.Time {
		t, _ := time.Parse(time.RFC3339, s)
		return t
	}
	return parse(ev.Start.DateTime), parse(ev.End.DateTime)
}

func (e *BookingEngine) calendarClientForUser(ctx context.Context, userID uuid.UUID) (*calendar.CalendarClient, error) {
	token, err := auth.LoadUserToken(e.DB, userID)
	if err != nil || token == nil {
		return nil, fmt.Errorf("no token for user %s", userID)
	}
	// The host's own credential, renewed and stored for the host (PAC-67, D-12).
	ts := auth.UserTokenSource(ctx, e.DB, e.OAuthConfig, userID, token)
	return calendar.NewClient(ctx, ts)
}

// --- Booking flow ----------------------------------------------------------

// ConfirmBooking creates calendar events on all accepted host calendars,
// saves the booking to DB, and sends a confirmation email. It does not check
// availability: the caller books only a slot CollectiveSlots offers (the
// public booking handler's offerable-start rule, PAC-72).
func (e *BookingEngine) ConfirmBooking(
	ctx context.Context,
	link *storage.SchedulingLink,
	bookerName, bookerEmail string,
	start, end time.Time,
	notes string,
) (*storage.Booking, error) {
	hosts, err := storage.GetAcceptedHosts(e.DB, link.ID)
	if err != nil {
		return nil, fmt.Errorf("get hosts: %w", err)
	}

	booking, err := storage.CreateBooking(e.DB, &storage.Booking{
		LinkID:      link.ID,
		BookerName:  bookerName,
		BookerEmail: bookerEmail,
		StartTime:   start,
		EndTime:     end,
		Notes:       notes,
	})
	if err != nil {
		return nil, fmt.Errorf("create booking: %w", err)
	}

	// Collect host display names for the calendar event.
	var hostEmails []string
	var hostNames []string
	for _, h := range hosts {
		u, err := storage.GetUserByID(e.DB, h.UserID)
		if err != nil || u == nil {
			continue
		}
		hostEmails = append(hostEmails, u.Email)
		hostNames = append(hostNames, u.Name)

		// Create calendar event on this host's calendar.
		calClient, err := e.calendarClientForUser(ctx, h.UserID)
		if err != nil {
			continue
		}
		attendees := make([]*googlecalendar.EventAttendee, 0, len(hostEmails)+1)
		for _, email := range hostEmails {
			attendees = append(attendees, &googlecalendar.EventAttendee{Email: email})
		}
		attendees = append(attendees, &googlecalendar.EventAttendee{Email: bookerEmail})

		ev := &googlecalendar.Event{
			Summary:     link.Title,
			Description: notes,
			Start:       &googlecalendar.EventDateTime{DateTime: start.Format(time.RFC3339)},
			End:         &googlecalendar.EventDateTime{DateTime: end.Format(time.RFC3339)},
			Attendees:   attendees,
		}
		created, err := calClient.CreateEvent(ctx, calClient.CalendarID, ev)
		if err == nil {
			_ = storage.SaveBookingEvent(e.DB, booking.ID, h.UserID, created.Id)
		}
	}

	// Send confirmation email to booker.
	go sendConfirmationEmail(link.Title, bookerName, bookerEmail,
		strings.Join(hostNames, " and "), start, end)

	return booking, nil
}

// --- ICS / email -----------------------------------------------------------

func sendConfirmationEmail(title, bookerName, bookerEmail, hostNames string, start, end time.Time) {
	smtpHost := os.Getenv("SMTP_HOST")
	smtpPort := os.Getenv("SMTP_PORT")
	smtpUser := os.Getenv("SMTP_USER")
	smtpPass := os.Getenv("SMTP_PASS")
	smtpFrom := os.Getenv("SMTP_FROM")

	if smtpHost == "" || smtpFrom == "" {
		return // email not configured
	}
	if smtpPort == "" {
		smtpPort = "587"
	}

	ics := generateICS(title, bookerName, bookerEmail, hostNames, start, end)

	boundary := "boundary-paceday-ics"
	body := strings.Join([]string{
		"MIME-Version: 1.0",
		fmt.Sprintf("Content-Type: multipart/mixed; boundary=%q", boundary),
		fmt.Sprintf("From: Paceday <%s>", smtpFrom),
		fmt.Sprintf("To: %s", bookerEmail),
		fmt.Sprintf("Subject: Confirmed — %s on %s", title, start.Format("Mon Jan 2")),
		"",
		"--" + boundary,
		"Content-Type: text/plain; charset=utf-8",
		"",
		fmt.Sprintf("Hi %s,\n\nYour meeting with %s is confirmed.\n\nDate: %s\nTime: %s – %s UTC\n",
			bookerName, hostNames,
			start.Format("Monday, January 2 2006"),
			start.Format("15:04"), end.Format("15:04")),
		"",
		"--" + boundary,
		"Content-Type: text/calendar; method=REQUEST",
		"Content-Disposition: attachment; filename=invite.ics",
		"",
		ics,
		"--" + boundary + "--",
	}, "\r\n")

	addr := smtpHost + ":" + smtpPort
	var auth smtp.Auth
	if smtpUser != "" {
		auth = smtp.PlainAuth("", smtpUser, smtpPass, smtpHost)
	}
	_ = smtp.SendMail(addr, auth, smtpFrom, []string{bookerEmail}, []byte(body))
}

func generateICS(title, bookerName, bookerEmail, hostNames string, start, end time.Time) string {
	uid := fmt.Sprintf("%d@paceday", start.UnixNano())
	dtStart := start.UTC().Format("20060102T150405Z")
	dtEnd := end.UTC().Format("20060102T150405Z")
	return strings.Join([]string{
		"BEGIN:VCALENDAR",
		"VERSION:2.0",
		"PRODID:-//Paceday//Paceday//EN",
		"METHOD:REQUEST",
		"BEGIN:VEVENT",
		"UID:" + uid,
		"DTSTART:" + dtStart,
		"DTEND:" + dtEnd,
		"SUMMARY:" + title,
		"DESCRIPTION:Meeting with " + hostNames,
		"ORGANIZER;CN=Paceday:mailto:noreply@paceday.app",
		"ATTENDEE;CN=" + bookerName + ":mailto:" + bookerEmail,
		"END:VEVENT",
		"END:VCALENDAR",
	}, "\r\n")
}
