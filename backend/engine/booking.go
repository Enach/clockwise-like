package engine

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
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
	// Reads are the storage reads behind a host's busy time. The zero value
	// uses the storage package; tests replace one read to inject a storage
	// failure (PAC-72 AC-17).
	Reads BookingReads
}

// BookingReads are the storage reads CollectiveSlots makes. A nil field means
// the storage package function of the same purpose.
type BookingReads struct {
	AcceptedHosts func(db storage.DBTX, linkID uuid.UUID) ([]*storage.LinkHost, error)
	FocusBlocks   func(db storage.DBTX, userID uuid.UUID, weekStart time.Time) ([]storage.FocusBlock, error)
	HostBookings  func(db storage.DBTX, userID uuid.UUID, start, end time.Time) ([]time.Time, error)
	// OwnerZone reads the link owner's stored zone for the confirmation
	// email (PAC-88); nil is storage.GetUserTimezone.
	OwnerZone func(db storage.DBTX, userID uuid.UUID) (tz string, found bool, err error)
}

func (r BookingReads) acceptedHosts(db storage.DBTX, linkID uuid.UUID) ([]*storage.LinkHost, error) {
	if r.AcceptedHosts != nil {
		return r.AcceptedHosts(db, linkID)
	}
	return storage.GetAcceptedHosts(db, linkID)
}

func (r BookingReads) focusBlocks(db storage.DBTX, userID uuid.UUID, weekStart time.Time) ([]storage.FocusBlock, error) {
	if r.FocusBlocks != nil {
		return r.FocusBlocks(db, userID, weekStart)
	}
	return storage.ListFocusBlocksForUserWeek(db, userID, weekStart)
}

func (r BookingReads) hostBookings(db storage.DBTX, userID uuid.UUID, start, end time.Time) ([]time.Time, error) {
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
// free: the link's grid slots on date's calendar day (GridSlots, in date's
// location, which is the link's zone, PAC-72 D-9), minus busy time. It is
// CollectiveSlotsDays for one day.
func (e *BookingEngine) CollectiveSlots(
	ctx context.Context,
	link *storage.SchedulingLink,
	date time.Time,
	durationMinutes int,
) ([]AvailableSlot, error) {
	out, err := e.CollectiveSlotsDays(ctx, link, []time.Time{date}, durationMinutes)
	if err != nil {
		return nil, err
	}
	return out[0], nil
}

// dayWindow is one link day's window (LinkWindow), ok false when it has none.
type dayWindow struct {
	day        time.Time
	start, end time.Time
	ok         bool
}

// CollectiveSlotsDays is CollectiveSlots for several link days at once: out[i]
// is exactly what CollectiveSlots(days[i]) answers, computed with ONE
// external calendar read per accepted host over the window from the earliest
// day's opening to the latest day's close (PAC-88 contract section 2.3, C-3).
// It is the busy-time half of the offerable-start rule that the public slot
// listing and the public booking share (PAC-72): a link with no accepted host
// offers nothing (D-10), and a storage failure while reading the hosts, a
// host's focus blocks or the bookings on a host's links is an error, never
// "free" (D-7). days are calendar days in the link's zone (their location).
//
// Equivalence with one read per day (PAC-88 C-1): each day's busy time is
// built only from the events overlapping that day's own window (buffers are
// applied to those events), the focus blocks read from that day's opening and
// the bookings overlapping that day's window, exactly as a one-day read saw
// them. Storage reads stay per day; only the provider call is shared.
//
// PAC-81 AC-A18: each host's read (credential renewal plus the calendar
// list, every page of it) runs on the bounded per-item pool. Owner decision
// PAC-72 D-6 (2026-11-01): a host whose calendar fails with a provider error
// (on any page), is not connected, or times out on its own limit is busy for
// every requested day's whole window. A host not read because of the budget,
// or cut by it, makes the whole listing a budget error (503), never a slot
// list that silently ignores that host.
func (e *BookingEngine) CollectiveSlotsDays(
	ctx context.Context,
	link *storage.SchedulingLink,
	days []time.Time,
	durationMinutes int,
) ([][]AvailableSlot, error) {
	out := make([][]AvailableSlot, len(days))
	for i := range out {
		out[i] = make([]AvailableSlot, 0)
	}
	if len(days) == 0 {
		return out, nil
	}
	hosts, err := e.Reads.acceptedHosts(storage.Bind(ctx, e.DB), link.ID)
	if err != nil {
		return nil, err
	}
	if len(hosts) == 0 {
		return out, nil
	}

	// Each day's window in the link's zone, with the daylight-saving edge
	// rules (PAC-72 R21.2), and the one window the calendar read covers.
	windows := make([]dayWindow, len(days))
	var readStart, readEnd time.Time
	found := false
	for i, d := range days {
		ws, we, ok := LinkWindow(link, d)
		windows[i] = dayWindow{day: d, start: ws, end: we, ok: ok}
		if !ok {
			continue
		}
		if !found || ws.Before(readStart) {
			readStart = ws
		}
		if !found || we.After(readEnd) {
			readEnd = we
		}
		found = true
	}
	if !found {
		return out, nil
	}

	// perHost[h][i] is host h's busy time on day i. A storage failure
	// (PAC-72 D-7) is kept apart from an unreadable calendar: it fails the
	// listing instead of making the host busy.
	perHost := make([][][]interval, len(hosts))
	storeErr := make([]error, len(hosts))
	if err := peritem.Run(ctx, len(hosts), peritem.Read, HostReadLimit(), func(callCtx context.Context, i int) error {
		busy, err := e.hostBusyDays(callCtx, ctx, hosts[i].UserID, readStart, readEnd, windows, link)
		var se *storageReadError
		if errors.As(err, &se) {
			storeErr[i] = err
			return err
		}
		if err != nil {
			all := make([][]interval, len(windows))
			for j, w := range windows {
				if w.ok {
					all[j] = []interval{{start: w.start, end: w.end}}
				}
			}
			perHost[i] = all
			return err
		}
		perHost[i] = busy
		return nil
	}); err != nil {
		return nil, err
	}
	for _, err := range storeErr {
		if err != nil {
			return nil, err
		}
	}

	for j, w := range windows {
		if !w.ok {
			continue
		}
		var dayBusy []interval
		for _, busy := range perHost {
			if busy != nil {
				dayBusy = append(dayBusy, busy[j]...)
			}
		}
		merged := mergeIntervals(dayBusy)
		// Candidate slots on the 15-minute wall-clock grid.
		for _, c := range GridSlots(link, w.day, durationMinutes) {
			if !overlapsMerged(interval{start: c.Start, end: c.End}, merged) {
				out[j] = append(out[j], c)
			}
		}
	}
	return out, nil
}

// HostReadLimit is one per-item call's own limit: a credential renewal plus
// one provider call (10 s + 15 s, spec D-4).
func HostReadLimit() time.Duration {
	l := outbound.Current()
	return l.Identity + l.Provider
}

// storageReadError marks a host's busy-time read that failed in storage, not
// in the calendar provider: an error, never "busy" or "free" (PAC-72 D-7).
type storageReadError struct{ err error }

func (e *storageReadError) Error() string { return e.err.Error() }
func (e *storageReadError) Unwrap() error { return e.err }

// ErrHostCalendarUnreadable is returned by ConfirmBooking when an accepted
// host's calendar cannot be read: owner decision PAC-72 D-6, the booking is
// refused with the existing 409 and nothing is written.
var ErrHostCalendarUnreadable = errors.New("a host's calendar could not be read")

// hostBusyDays reads one host's busy time for each window: one calendar read
// (every page) over [readStart, readEnd), then per window the events
// overlapping it (with the link's buffers), the host's focus blocks read from
// the window's opening and the bookings overlapping it. The calendar call
// runs on ctx (the per-item deadline); the database reads run on dbCtx (the
// request), so a slow calendar never leaves the host's existing bookings out
// of busy time. The error is non-nil when the host's calendar could not be
// read (no connection, provider error on any page, own timeout): the caller
// treats the host as busy (PAC-72 D-6). A storage read failure is a
// *storageReadError, which the caller returns as an error instead (D-7).
func (e *BookingEngine) hostBusyDays(
	ctx, dbCtx context.Context,
	userID uuid.UUID,
	readStart, readEnd time.Time,
	windows []dayWindow,
	link *storage.SchedulingLink,
) ([][]interval, error) {
	db := storage.Bind(dbCtx, e.DB)

	// Calendar events from Google/Outlook: one complete read (PAC-88 C-10).
	calClient, err := e.calendarClientForUser(ctx, dbCtx, userID)
	if err != nil {
		return nil, err
	}
	events, err := calClient.ListEventsComplete(ctx, calClient.CalendarID, readStart, readEnd)
	if err != nil {
		return nil, err
	}
	var evs []interval
	for _, ev := range events {
		if ev.Transparency == "transparent" {
			continue
		}
		s, e2 := parseEventTime(ev)
		if !s.IsZero() && !e2.IsZero() {
			evs = append(evs, interval{start: s, end: e2})
		}
	}

	out := make([][]interval, len(windows))
	for j, w := range windows {
		if !w.ok {
			continue
		}
		var busy []interval
		// The events a one-day read of this window returns: those
		// overlapping it (the provider's timeMin/timeMax filter).
		for _, iv := range evs {
			if iv.start.Before(w.end) && iv.end.After(w.start) {
				busy = append(busy, iv)
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

		// Only this host's focus blocks (PAC-64): another user's focus
		// time does not make this host busy. Read per day from the day's
		// opening, so a window of many days is covered (PAC-88 C-10).
		blocks, err := e.Reads.focusBlocks(db, userID, w.start)
		if err != nil {
			return nil, &storageReadError{fmt.Errorf("focus blocks: %w", err)}
		}
		for _, fb := range blocks {
			busy = append(busy, interval{start: fb.StartTime, end: fb.EndTime})
		}

		// Bookings that are not cancelled, on any link this user hosts
		// (PAC-72 D-11).
		times, err := e.Reads.hostBookings(db, userID, w.start, w.end)
		if err != nil {
			return nil, &storageReadError{fmt.Errorf("host bookings: %w", err)}
		}
		for i := 0; i+1 < len(times); i += 2 {
			busy = append(busy, interval{start: times[i], end: times[i+1]})
		}
		out[j] = busy
	}
	return out, nil
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

// calendarClientForUser builds the host's client: the token is read on dbCtx,
// the calendar (and any renewal) runs on ctx.
func (e *BookingEngine) calendarClientForUser(ctx, dbCtx context.Context, userID uuid.UUID) (*calendar.CalendarClient, error) {
	db := storage.Bind(dbCtx, e.DB)
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
// and sends the confirmation email. Whether a readable host calendar is busy
// at the slot is not checked here: the caller books only a start
// CollectiveSlots offers (the public booking handler's offerable-start rule,
// PAC-72).
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

	// PAC-72 D-6 (owner, 2026-11-01): every accepted host's calendar must be
	// readable for the slot, or the booking is refused (409) and nothing is
	// written. The reads run on the per-item pool (AC-A18): a host the budget
	// did not reach is the 503. Whether a readable calendar is busy at the
	// slot stays PAC-72's check (API-094).
	unreadable := make([]bool, len(hosts))
	if err := peritem.Run(ctx, len(hosts), peritem.Read, HostReadLimit(), func(callCtx context.Context, i int) error {
		calClient, err := e.calendarClientForUser(callCtx, ctx, hosts[i].UserID)
		if err == nil {
			_, err = calClient.ListEvents(callCtx, calClient.CalendarID, start, end)
		}
		unreadable[i] = err != nil
		return err
	}); err != nil {
		return nil, err
	}
	for _, u := range unreadable {
		if u {
			return nil, ErrHostCalendarUnreadable
		}
	}

	// The insert's connection is acquired while the budget still bounds the
	// wait (a saturated pool ends in the 503 with nothing written); only
	// then does the request commit, and the insert itself runs to its own
	// result on that connection.
	if beforeBookingConn != nil {
		beforeBookingConn()
	}
	conn, err := e.DB.Conn(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil, budget.ErrExpired
		}
		return nil, fmt.Errorf("booking connection: %w", err)
	}
	if !budget.Commit(ctx) {
		_ = conn.Close()
		return nil, budget.ErrExpired
	}
	booking, err := storage.CreateBooking(storage.BindConn(context.WithoutCancel(ctx), conn), &storage.Booking{
		LinkID:      link.ID,
		BookerName:  bookerName,
		BookerEmail: bookerEmail,
		StartTime:   start,
		EndTime:     end,
		Notes:       notes,
	})
	_ = conn.Close() // released before the completion unit needs the pool
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

// beforeBookingConn is a test seam: it runs just before the insert's
// connection is acquired.
var beforeBookingConn func()

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

// summary is detail plus whether every host is done.
func (c *completion) summary() (string, bool) {
	c.mu.Lock()
	all := len(c.done) == len(c.hosts)
	c.mu.Unlock()
	return c.detail(), all
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
	// The host events are not gated by the drain deadline (a booking saved
	// during the drain still gets them): each call has its own limit, and
	// only the drain's hard cancellation cuts them (AC-A7 then logs the unit).
	_ = peritem.Run(ctx, len(events), peritem.Detached, HostReadLimit(), func(callCtx context.Context, i int) error {
		he := events[i]
		calClient, err := e.calendarClientForUser(callCtx, ctx, he.userID)
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

	if d, all := prog.summary(); !all {
		log.Printf("ERROR: booking %s: not every host event was created (%s)", booking.ID, d)
		sentry.CaptureException(fmt.Errorf("booking %s: not every host event was created (%s)", booking.ID, d))
	}

	// The confirmation email (AC-A8): a failure is logged with the booking
	// id and reported, never retried (D-15).
	// PAC-88: in the link's zone read now, then UTC (owner OQ-4).
	err := e.sendBookingEmail(db, link, booking, strings.Join(hostNames, " and "))
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

// emailZone is the link's zone for the confirmation email, read at compose
// time by the PAC-72 rule (PAC-88 contract section 4): the owner's profile
// zone; no settings row, an empty name, "Local" or a name that does not load
// is UTC. A failed read is the error (the caller sends in UTC and warns).
func (e *BookingEngine) emailZone(db storage.DBTX, ownerID uuid.UUID) (*time.Location, error) {
	read := e.Reads.OwnerZone
	if read == nil {
		read = storage.GetUserTimezone
	}
	tz, found, err := read(db, ownerID)
	if err != nil {
		return time.UTC, err
	}
	if !found || tz == "" || tz == "Local" {
		return time.UTC, nil
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.UTC, nil
	}
	return loc, nil
}

// sendBookingEmail reads the link's zone, then composes and sends the
// confirmation. An unreadable zone is still sent, in UTC, and logged as a
// WARN with the booking id (AC-23).
func (e *BookingEngine) sendBookingEmail(db storage.DBTX, link *storage.SchedulingLink, booking *storage.Booking, hostNames string) error {
	loc, err := e.emailZone(db, link.OwnerUserID)
	if err != nil {
		log.Printf("WARN: booking confirmation email for booking %s: link zone unreadable, sent in UTC: %s",
			booking.ID, outbound.RedactAddresses(err.Error()))
		loc = time.UTC
	}
	return sendConfirmationEmail(link.Title, booking.BookerName, booking.BookerEmail, hostNames,
		booking.StartTime, booking.EndTime, loc)
}

// utcOffset is the offset in force at t in t's location, "UTC+hh:mm" or
// "UTC−hh:mm" (U+2212 MINUS SIGN).
func utcOffset(t time.Time) string {
	_, off := t.Zone()
	sign := "+"
	if off < 0 {
		sign, off = "−", -off
	}
	return fmt.Sprintf("UTC%s%02d:%02d", sign, off/3600, off%3600/60)
}

// composeConfirmation is the confirmation email's subject date and body text
// (PAC-88 contract section 4, owner OQ-4): the link's zone with its offset,
// then UTC; UTC only when the zone is UTC.
func composeConfirmation(bookerName, hostNames string, start, end time.Time, loc *time.Location) (subjectDate, text string) {
	us, ue := start.UTC(), end.UTC()
	if loc == nil || loc.String() == "UTC" {
		return us.Format("Monday, January 2"), fmt.Sprintf(
			"Hi %s,\n\nYour meeting with %s is confirmed.\n\nDate: %s\nTime: %s – %s UTC\n",
			bookerName, hostNames, us.Format("Monday, January 2, 2006"), us.Format("15:04"), ue.Format("15:04"))
	}
	ls, le := start.In(loc), end.In(loc)
	timeLine := fmt.Sprintf("%s – %s, %s (%s)", ls.Format("3:04 PM"), le.Format("3:04 PM"), loc.String(), utcOffset(ls))
	if utcOffset(ls) != utcOffset(le) {
		// The meeting spans a change of offset.
		timeLine = fmt.Sprintf("%s (%s) – %s (%s), %s", ls.Format("3:04 PM"), utcOffset(ls), le.Format("3:04 PM"), utcOffset(le), loc.String())
	}
	return ls.Format("Monday, January 2"), fmt.Sprintf(
		"Hi %s,\n\nYour meeting with %s is confirmed.\n\nDate: %s\nTime: %s\nIn UTC: %s – %s UTC on %s\n",
		bookerName, hostNames, ls.Format("Monday, January 2, 2006"), timeLine,
		us.Format("15:04"), ue.Format("15:04"), us.Format("Monday, January 2, 2006"))
}

func sendConfirmationEmail(title, bookerName, bookerEmail, hostNames string, start, end time.Time, loc *time.Location) error {
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
	subjectDate, text := composeConfirmation(bookerName, hostNames, start, end, loc)

	boundary := "boundary-paceday-ics"
	body := strings.Join([]string{
		"MIME-Version: 1.0",
		fmt.Sprintf("Content-Type: multipart/mixed; boundary=%q", boundary),
		fmt.Sprintf("From: Paceday <%s>", smtpFrom),
		fmt.Sprintf("To: %s", bookerEmail),
		fmt.Sprintf("Subject: Confirmed — %s on %s", title, subjectDate),
		"",
		"--" + boundary,
		"Content-Type: text/plain; charset=utf-8",
		"",
		text,
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
