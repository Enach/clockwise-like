package engine

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log"
	"net/smtp"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/calendar"
	"github.com/Enach/paceday/backend/internal/budget"
	"github.com/Enach/paceday/backend/internal/lifecycle"
	"github.com/Enach/paceday/backend/internal/outbound"
	"github.com/Enach/paceday/backend/internal/peritem"
	"github.com/Enach/paceday/backend/storage"
	"github.com/getsentry/sentry-go"
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

// CollectiveSlots returns time slots where ALL accepted hosts are simultaneously free.
//
// PAC-81 AC-A18: each host's read (credential renewal plus one calendar
// list) runs on the bounded per-item pool. A host whose calendar fails with a
// provider error, is not connected, or times out on its own limit is
// treated as free, as before. A host not read because of the budget, or cut
// by it, makes the whole listing a budget error (503), never a slot list that
// silently ignores that host.
func (e *BookingEngine) CollectiveSlots(
	ctx context.Context,
	link *storage.SchedulingLink,
	date time.Time,
	durationMinutes int,
) ([]AvailableSlot, error) {
	hosts, err := storage.GetAcceptedHosts(storage.Bind(ctx, e.DB), link.ID)
	if err != nil {
		return nil, err
	}

	// Parse link window for the given date.
	windowStart := parseTime(link.WindowStart, date)
	windowEnd := parseTime(link.WindowEnd, date)

	// Collect merged busy intervals across all accepted hosts.
	perHost := make([][]interval, len(hosts))
	if err := peritem.Run(ctx, len(hosts), peritem.Read, HostReadLimit(), func(callCtx context.Context, i int) error {
		busy, err := e.hostBusy(callCtx, hosts[i].UserID, windowStart, windowEnd, link)
		if err != nil {
			return err // tolerate per-host errors
		}
		perHost[i] = busy
		return nil
	}); err != nil {
		return nil, err
	}
	var allBusy []interval
	for _, busy := range perHost {
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

// HostReadLimit is one per-item call's own limit: a credential renewal plus
// one provider call (10 s + 15 s, spec D-4).
func HostReadLimit() time.Duration {
	l := outbound.Current()
	return l.Identity + l.Provider
}

func (e *BookingEngine) hostBusy(
	ctx context.Context,
	userID uuid.UUID,
	start, end time.Time,
	link *storage.SchedulingLink,
) ([]interval, error) {
	var busy []interval
	db := storage.Bind(ctx, e.DB)

	// Calendar events from Google/Outlook.
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
	blocks, err := storage.ListFocusBlocksForUserWeek(db, userID, start)
	if err == nil {
		for _, fb := range blocks {
			busy = append(busy, interval{start: fb.StartTime, end: fb.EndTime})
		}
	}

	// Existing confirmed bookings for this user.
	times, err := storage.GetConfirmedBookingsForUser(db, userID, start, end)
	if err == nil {
		for i := 0; i+1 < len(times); i += 2 {
			busy = append(busy, interval{start: times[i], end: times[i+1]})
		}
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
	db := storage.Bind(ctx, e.DB)
	token, err := auth.LoadUserToken(db, userID)
	if err != nil || token == nil {
		return nil, fmt.Errorf("no token for user %s", userID)
	}
	// The host's own credential, renewed and stored for the host (PAC-67, D-12).
	ts := auth.UserTokenSource(ctx, db, e.OAuthConfig, userID, token)
	return calendar.NewClient(ctx, ts)
}

// --- Booking flow ----------------------------------------------------------

// ConfirmBooking saves the booking, then creates the host calendar events
// and sends the confirmation email.
//
// PAC-81 AC-A19 (spec F-12): the budget is checked once, immediately before
// the insert is sent. If it has already answered, nothing is written and the
// result is budget.ErrExpired (503). Once the insert is sent, only its own
// result counts: it runs detached from the budget, and its error is the 500.
// After the insert, the host events and the email run as one tracked unit
// (booking-completion), with their own per-call limits, independent of the
// request; the caller gets the saved booking when the unit finishes or the
// budget runs out, whichever is first.
func (e *BookingEngine) ConfirmBooking(
	ctx context.Context,
	link *storage.SchedulingLink,
	bookerName, bookerEmail string,
	start, end time.Time,
	notes string,
) (*storage.Booking, error) {
	// Re-verify availability.
	hosts, err := storage.GetAcceptedHosts(storage.Bind(ctx, e.DB), link.ID)
	if err != nil {
		return nil, fmt.Errorf("get hosts: %w", err)
	}

	if !budget.Commit(ctx) {
		return nil, budget.ErrExpired
	}
	booking, err := storage.CreateBooking(storage.Bind(context.WithoutCancel(ctx), e.DB), &storage.Booking{
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

	done := e.startCompletion(link, booking, hosts)
	select {
	case <-done:
	case <-ctx.Done():
	}
	return booking, nil
}

// completion is the post-commit unit's progress, read by its abandonment
// log line (AC-A7, AC-A19): hosts done and not done, by user id.
type completion struct {
	mu        sync.Mutex
	hosts     []uuid.UUID
	done      map[uuid.UUID]bool
	emailSent bool
}

func (c *completion) markHost(id uuid.UUID) {
	c.mu.Lock()
	c.done[id] = true
	c.mu.Unlock()
}

func (c *completion) detail() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var done, notDone []string
	for _, h := range c.hosts {
		if c.done[h] {
			done = append(done, h.String())
		} else {
			notDone = append(notDone, h.String())
		}
	}
	return fmt.Sprintf("hosts done: [%s]; hosts not done: [%s]; email sent: %t",
		strings.Join(done, " "), strings.Join(notDone, " "), c.emailSent)
}

// startCompletion starts the booking-completion unit and returns a channel
// closed when it finishes.
func (e *BookingEngine) startCompletion(link *storage.SchedulingLink, booking *storage.Booking, hosts []*storage.LinkHost) <-chan struct{} {
	prog := &completion{done: map[uuid.UUID]bool{}}
	for _, h := range hosts {
		prog.hosts = append(prog.hosts, h.UserID)
	}
	finished := make(chan struct{})
	lifecycle.Go(lifecycle.Unit{Kind: "booking-completion", ID: booking.ID.String(), Detail: prog.detail}, func(ctx context.Context) {
		defer close(finished)
		e.completeBooking(ctx, link, booking, hosts, prog)
	})
	return finished
}

// completeBooking creates each host's event (attendees: the hosts listed so
// far and the booker, as before) and then sends the confirmation email.
func (e *BookingEngine) completeBooking(ctx context.Context, link *storage.SchedulingLink, booking *storage.Booking, hosts []*storage.LinkHost, prog *completion) {
	db := storage.Bind(ctx, e.DB)
	type hostEvent struct {
		userID    uuid.UUID
		attendees []*googlecalendar.EventAttendee
	}
	var hostEmails, hostNames []string
	var events []hostEvent
	for _, h := range hosts {
		u, err := storage.GetUserByID(db, h.UserID)
		if err != nil || u == nil {
			continue
		}
		hostEmails = append(hostEmails, u.Email)
		hostNames = append(hostNames, u.Name)
		attendees := make([]*googlecalendar.EventAttendee, 0, len(hostEmails)+1)
		for _, email := range hostEmails {
			attendees = append(attendees, &googlecalendar.EventAttendee{Email: email})
		}
		attendees = append(attendees, &googlecalendar.EventAttendee{Email: booking.BookerEmail})
		events = append(events, hostEvent{userID: h.UserID, attendees: attendees})
	}

	start, end := booking.StartTime, booking.EndTime
	_ = peritem.Run(ctx, len(events), peritem.Create, HostReadLimit(), func(callCtx context.Context, i int) error {
		he := events[i]
		calClient, err := e.calendarClientForUser(callCtx, he.userID)
		if err != nil {
			prog.markHost(he.userID) // not connected: nothing to create
			return err
		}
		ev := &googlecalendar.Event{
			Summary:     link.Title,
			Description: booking.Notes,
			Start:       &googlecalendar.EventDateTime{DateTime: start.Format(time.RFC3339)},
			End:         &googlecalendar.EventDateTime{DateTime: end.Format(time.RFC3339)},
			Attendees:   he.attendees,
		}
		created, err := calClient.CreateEvent(callCtx, calClient.CalendarID, ev)
		if err != nil {
			return err
		}
		// An answer that arrived is always recorded.
		_ = storage.SaveBookingEvent(storage.Bind(context.WithoutCancel(callCtx), e.DB), booking.ID, he.userID, created.Id)
		prog.markHost(he.userID)
		return nil
	})

	// The confirmation email (AC-A8): a failure is logged with the booking
	// id and reported, never retried (D-15).
	err := sendConfirmationEmail(link.Title, booking.BookerName, booking.BookerEmail,
		strings.Join(hostNames, " and "), start, end)
	if err != nil {
		msg := outbound.RedactAddresses(err.Error())
		log.Printf("ERROR: booking confirmation email failed for booking %s: %s", booking.ID, msg)
		sentry.CaptureException(fmt.Errorf("booking confirmation email failed for booking %s: %s", booking.ID, msg))
		return
	}
	prog.mu.Lock()
	prog.emailSent = true
	prog.mu.Unlock()
}

// --- ICS / email -----------------------------------------------------------

func sendConfirmationEmail(title, bookerName, bookerEmail, hostNames string, start, end time.Time) error {
	smtpHost := os.Getenv("SMTP_HOST")
	smtpPort := os.Getenv("SMTP_PORT")
	smtpUser := os.Getenv("SMTP_USER")
	smtpPass := os.Getenv("SMTP_PASS")
	smtpFrom := os.Getenv("SMTP_FROM")

	if smtpHost == "" || smtpFrom == "" {
		return nil // email not configured
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
	return sendMail(addr, auth, smtpFrom, []string{bookerEmail}, []byte(body))
}

// sendMail is the SMTP sender (a test seam): 15 s from dial to QUIT.
var sendMail = outbound.SendMail

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
