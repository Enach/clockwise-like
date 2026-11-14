package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Enach/paceday/backend/api/gen"
	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/internal/budget"
	"github.com/Enach/paceday/backend/storage"
	"github.com/go-chi/chi/v5"
	"golang.org/x/oauth2"
)

type bookingHandlers struct {
	eng BookingFlow
	db  *sql.DB
	// now is the clock the offerable-start rule reads (minimum notice and
	// horizon); tests fix it (PAC-72).
	now func() time.Time
	// ownerZone reads the link owner's profile zone (PAC-72 D-9); nil is
	// storage.GetUserTimezone. Tests replace it to inject a read failure.
	ownerZone ownerZoneReader
}

func newBookingHandlers(db *sql.DB, oauthConfig *oauth2.Config) *bookingHandlers {
	return newBookingHandlersWithEngine(db, &engine.BookingEngine{DB: db, OAuthConfig: oauthConfig})
}

func newBookingHandlersWithEngine(db *sql.DB, eng BookingFlow) *bookingHandlers {
	return &bookingHandlers{eng: eng, db: db, now: time.Now}
}

type publicHostDTO struct {
	Email     string `json:"email"`
	Name      string `json:"name,omitempty"`
	AvatarURL string `json:"avatar_url,omitempty"`
}

type publicLinkDTO struct {
	Slug             string          `json:"slug"`
	Title            string          `json:"title"`
	Durations        []int           `json:"durations"`
	Hosts            []publicHostDTO `json:"hosts"`
	MinNoticeMinutes int             `json:"min_notice_minutes"`
	UsageType        string          `json:"usage_type"`
	Coverage         coverageDTO     `json:"coverage"`
	// TimeZone is the zone the link's days and hours are evaluated in, and
	// BookingHorizonDays the link's horizon (PAC-72 revision 2, for PAC-88).
	TimeZone           string `json:"time_zone"`
	BookingHorizonDays int    `json:"booking_horizon_days"`
}

type coverageDTO struct {
	Total   int `json:"total"`
	Checked int `json:"checked"`
}

type bookingConfirmationDTO struct {
	ID              string          `json:"id"`
	LinkSlug        string          `json:"link_slug"`
	Title           string          `json:"title"`
	Start           string          `json:"start"`
	End             string          `json:"end"`
	DurationMinutes int             `json:"duration_minutes"`
	Hosts           []publicHostDTO `json:"hosts"`
	BookerName      string          `json:"booker_name"`
	BookerEmail     string          `json:"booker_email"`
	Notes           string          `json:"notes,omitempty"`
}

func linkExhausted(link *storage.SchedulingLink) bool {
	if link == nil {
		return true
	}
	if link.UsageType == "single_use" {
		return link.UsesCount >= 1
	}
	return link.UsageType == "recurring" && link.MaxUses != nil && link.UsesCount >= *link.MaxUses
}

func (h *bookingHandlers) publicLink(link *storage.SchedulingLink, loc *time.Location) publicLinkDTO {
	dto := publicLinkDTO{Slug: link.Slug, Title: link.Title, Durations: link.DurationOptions, Hosts: []publicHostDTO{}, MinNoticeMinutes: link.MinNoticeMinutes, UsageType: link.UsageType,
		TimeZone: loc.String(), BookingHorizonDays: horizonDays(link)}
	if dto.UsageType == "" {
		dto.UsageType = "reusable"
	}
	hosts, _ := storage.GetAcceptedHosts(h.db, link.ID)
	for _, host := range hosts {
		user, err := storage.GetUserByID(h.db, host.UserID)
		if err != nil || user == nil {
			continue
		}
		dto.Hosts = append(dto.Hosts, publicHostDTO{Email: user.Email, Name: user.Name, AvatarURL: user.AvatarURL})
		dto.Coverage.Total++
		if token, err := auth.LoadUserToken(h.db, host.UserID); err == nil && token != nil {
			dto.Coverage.Checked++
		}
	}
	return dto
}

func (h *bookingHandlers) confirmation(db storage.DBTX, link *storage.SchedulingLink, booking *storage.Booking) bookingConfirmationDTO {
	dto := bookingConfirmationDTO{ID: booking.ID.String(), LinkSlug: link.Slug, Title: link.Title, Start: booking.StartTime.UTC().Format(time.RFC3339), End: booking.EndTime.UTC().Format(time.RFC3339), DurationMinutes: int(booking.EndTime.Sub(booking.StartTime).Minutes()), Hosts: []publicHostDTO{}, BookerName: booking.BookerName, BookerEmail: booking.BookerEmail, Notes: booking.Notes}
	for _, host := range func() []*storage.LinkHost { hosts, _ := storage.GetAcceptedHosts(db, link.ID); return hosts }() {
		if user, err := storage.GetUserByID(db, host.UserID); err == nil && user != nil {
			dto.Hosts = append(dto.Hosts, publicHostDTO{Email: user.Email, Name: user.Name, AvatarURL: user.AvatarURL})
		}
	}
	return dto
}

// GET /api/book/{slug}
func (h *bookingHandlers) getLinkInfo(w http.ResponseWriter, r *http.Request) {
	link, err := storage.GetSchedulingLinkBySlug(h.db, chi.URLParam(r, "slug"))
	if err != nil {
		writeError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if link == nil {
		writeError(w, "not found", http.StatusNotFound)
		return
	}
	if linkExhausted(link) {
		writeError(w, "link is no longer accepting bookings", http.StatusGone)
		return
	}
	// PAC-72 revision 2.1: a failed read of the owner's zone is the 500,
	// never UTC.
	loc, err := linkLocation(h.db, h.ownerZone, link.OwnerUserID)
	if err != nil {
		writeServerError(w, r, "link zone", err, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(h.publicLink(link, loc))
}

func durationAllowed(link *storage.SchedulingLink, duration int) bool {
	if duration <= 0 {
		return false
	}
	if len(link.DurationOptions) == 0 {
		return duration == 30
	}
	for _, allowed := range link.DurationOptions {
		if allowed == duration {
			return true
		}
	}
	return false
}

// errSlotUnavailable is the generic body of every offerable-start refusal
// (PAC-72 D-5, D-12), pinned by the contract's BookingSlotUnavailableError.
var errSlotUnavailable = string(gen.SlotIsNoLongerAvailable)

// publicSlotsDTO is PublicSlotsResponse: every key always present (PAC-88
// contract section 2.5).
type publicSlotsDTO struct {
	AvailableDates  []string               `json:"available_dates"`
	DisplayTimeZone string                 `json:"display_time_zone"`
	RangeFrom       string                 `json:"range_from"`
	RangeTo         string                 `json:"range_to"`
	Slots           []engine.AvailableSlot `json:"slots"`
	TimeZone        string                 `json:"time_zone"`
}

// GET /api/book/{slug}/slots?date=YYYY-MM-DD&duration=30
// GET /api/book/{slug}/slots?from=YYYY-MM-DD&to=YYYY-MM-DD&display_time_zone=Zone
//
// PAC-88: the answer's days are days of the day zone (display_time_zone when
// given, else the link's zone); an offered start falls on day D exactly when
// its date read in that zone is D. Without date the answer is the bounded
// overview (at most 42 days, default today + 41 in the day zone). Each
// accepted host's calendar is read at most once per request.
func (h *bookingHandlers) getSlots(w http.ResponseWriter, r *http.Request) {
	// NEW-booking-rate-limit: counted before anything else, so 429 precedes
	// every other status.
	if !allowPublicBooking(w, r, bookingSlotsLimiter) {
		return
	}
	// PAC-81 AC-A9: every database call waits for a pooled connection only
	// until the budget ends.
	link, err := storage.GetSchedulingLinkBySlug(storage.Bind(r.Context(), h.db), chi.URLParam(r, "slug"))
	if err != nil {
		writeError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if link == nil {
		writeError(w, "not found", http.StatusNotFound)
		return
	}
	if linkExhausted(link) {
		writeError(w, "link is no longer accepting bookings", http.StatusGone)
		return
	}
	q := r.URL.Query()
	duration := 30
	if len(link.DurationOptions) > 0 {
		duration = link.DurationOptions[0]
	}
	if raw := q.Get("duration"); raw != "" {
		duration, err = strconv.Atoi(raw)
		if err != nil || !durationAllowed(link, duration) {
			writeError(w, "invalid duration", http.StatusBadRequest)
			return
		}
	}
	// The link's zone first (PAC-72 D-9): a failed read is the 500 for the
	// whole request, never UTC and never a list with days left out.
	loc, err := linkLocation(storage.Bind(r.Context(), h.db), h.ownerZone, link.OwnerUserID)
	if err != nil {
		writeServerError(w, r, "link zone", err, http.StatusInternalServerError)
		return
	}

	now := h.now()
	// PAC-88 checks, in the contract's order (section 2.4).
	dayZone, dayZoneName := loc, loc.String()
	if name := q.Get("display_time_zone"); name != "" {
		z, ok := loadDisplayZone(name)
		if !ok {
			writeError(w, errInvalidDisplayZone, http.StatusBadRequest)
			return
		}
		dayZone, dayZoneName = z, name
	}
	dateStr, fromStr, toStr := q.Get("date"), q.Get("from"), q.Get("to")
	if dateStr != "" && (fromStr != "" || toStr != "") {
		writeError(w, "date cannot be combined with from or to", http.StatusBadRequest)
		return
	}
	var from, to time.Time
	if dateStr != "" {
		if from, err = time.Parse(time.DateOnly, dateStr); err != nil {
			writeError(w, "invalid date format, use YYYY-MM-DD", http.StatusBadRequest)
			return
		}
		to = from
	} else {
		var fromErr, toErr error
		if fromStr != "" {
			from, fromErr = time.Parse(time.DateOnly, fromStr)
		}
		if fromErr != nil {
			writeError(w, "invalid from, use YYYY-MM-DD", http.StatusBadRequest)
			return
		}
		if toStr != "" {
			to, toErr = time.Parse(time.DateOnly, toStr)
		}
		if toErr != nil {
			writeError(w, "invalid to, use YYYY-MM-DD", http.StatusBadRequest)
			return
		}
		if (fromStr == "") != (toStr == "") {
			writeError(w, "from and to must be given together", http.StatusBadRequest)
			return
		}
		if fromStr == "" {
			y, m, d := now.In(dayZone).Date()
			from = time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
			to = from.AddDate(0, 0, maxOverviewDays-1)
		}
		if to.Before(from) {
			writeError(w, "to must not be before from", http.StatusBadRequest)
			return
		}
		if daysBetween(from, to) >= maxOverviewDays {
			writeError(w, "range must not exceed 42 days", http.StatusBadRequest)
			return
		}
	}

	offered, err := h.offeredOnLinkDays(r.Context(), link, loc, neededLinkDays(from, to, dayZone, loc), duration, now)
	if err != nil {
		// PAC-81 AC-A2, AC-A18, PAC-72 AC-39: a budget cut or an ended
		// request is a 503, never a partial 200; a storage failure is the
		// 500 in both modes (PAC-88 C-4), never an omitted day.
		if budgetCut(err) || r.Context().Err() != nil {
			budget.WriteExpired(w)
			return
		}
		writeError(w, "internal error", http.StatusInternalServerError)
		return
	}

	out := publicSlotsDTO{
		AvailableDates: []string{}, Slots: []engine.AvailableSlot{},
		TimeZone: loc.String(), DisplayTimeZone: dayZoneName,
		RangeFrom: from.Format(time.DateOnly), RangeTo: to.Format(time.DateOnly),
	}
	if dateStr != "" {
		for _, s := range offered {
			if s.Start.In(dayZone).Format(time.DateOnly) == out.RangeFrom {
				out.Slots = append(out.Slots, s)
			}
		}
	} else {
		seen := map[string]bool{}
		for _, s := range offered {
			d := s.Start.In(dayZone).Format(time.DateOnly)
			if d >= out.RangeFrom && d <= out.RangeTo && !seen[d] {
				seen[d] = true
				out.AvailableDates = append(out.AvailableDates, d)
			}
		}
		sort.Strings(out.AvailableDates)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// POST /api/book/{slug}
//
// Order of evaluation (PAC-82 contract section 1.1). Without a key (or with
// an empty one), unchanged: 429, 404, 410, body 400s, zone 500, cheap 409s,
// taken 409, host reads (host_busy 409 / 503), save. With a key: 429, 404,
// the key's 400, the body 400s that do not depend on the link, the replay
// decision, 410, "invalid duration" 400, then as without a key. A keyed
// request about to answer a 409 or 410 looks for its key once more first.
func (h *bookingHandlers) createBooking(w http.ResponseWriter, r *http.Request) {
	// NEW-booking-rate-limit: counted before the link lookup and the body, so
	// 429 precedes every other status and a refused request writes nothing.
	if !allowPublicBooking(w, r, bookingCreateLimiter) {
		return
	}
	// PAC-81 AC-A9: reads before the insert wait for a pooled connection
	// only until the budget ends.
	db := storage.Bind(r.Context(), h.db)
	link, err := storage.GetSchedulingLinkBySlug(db, chi.URLParam(r, "slug"))
	if err != nil {
		writeError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if link == nil {
		writeError(w, "not found", http.StatusNotFound)
		return
	}
	key, ok := idempotencyKey(r)
	if !ok {
		writeError(w, errInvalidIdempotencyKey, http.StatusBadRequest)
		return
	}
	keyed := key != ""
	if !keyed && linkExhausted(link) {
		writeError(w, "link is no longer accepting bookings", http.StatusGone)
		return
	}
	if afterBookingLinkRead != nil {
		afterBookingLinkRead()
	}
	var body struct {
		Name            string `json:"name"`
		Email           string `json:"email"`
		Start           string `json:"start"`
		End             string `json:"end"`
		Notes           string `json:"notes"`
		Duration        *int   `json:"duration"`
		DurationMinutes *int   `json:"duration_minutes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	body.Name, body.Email = strings.TrimSpace(body.Name), strings.TrimSpace(body.Email)
	if body.Name == "" || body.Email == "" || body.Start == "" {
		writeError(w, "name, email, and start are required", http.StatusBadRequest)
		return
	}
	start, err := time.Parse(time.RFC3339, body.Start)
	if err != nil {
		writeError(w, "invalid start time, use RFC3339", http.StatusBadRequest)
		return
	}
	var end time.Time
	requestedDuration := 0
	if body.DurationMinutes != nil {
		requestedDuration = *body.DurationMinutes
	}
	if body.Duration != nil {
		requestedDuration = *body.Duration
	}
	if body.End != "" {
		end, err = time.Parse(time.RFC3339, body.End)
		if err != nil {
			writeError(w, "invalid end time, use RFC3339", http.StatusBadRequest)
			return
		}
		// The length must be exactly a whole number of minutes; one with
		// seconds or a fraction is never rounded to an accepted duration
		// (PAC-72 D-8).
		length := end.Sub(start)
		whole := length%time.Minute == 0
		actualDuration := int(length / time.Minute)
		if requestedDuration != 0 && (!whole || requestedDuration != actualDuration) {
			writeError(w, "duration does not match end time", http.StatusBadRequest)
			return
		}
		if !whole {
			writeError(w, "invalid duration", http.StatusBadRequest)
			return
		}
		requestedDuration = actualDuration
	} else {
		if requestedDuration == 0 {
			requestedDuration = 30
			if len(link.DurationOptions) > 0 {
				requestedDuration = link.DurationOptions[0]
			}
		}
		end = start.Add(time.Duration(requestedDuration) * time.Minute)
	}
	// Keyless: "invalid duration" keeps its place before "end must be after
	// start". Keyed: it depends on the link's current durations, so it comes
	// after the replay decision and the 410 (contract challenge C-4).
	if !keyed && !durationAllowed(link, requestedDuration) {
		writeError(w, "invalid duration", http.StatusBadRequest)
		return
	}
	if !end.After(start) {
		writeError(w, "end must be after start", http.StatusBadRequest)
		return
	}
	attempt := &storage.Booking{LinkID: link.ID, BookerName: body.Name, BookerEmail: body.Email,
		StartTime: start, EndTime: end, Notes: body.Notes, IdempotencyKey: key}
	// refuse answers a refusal; a keyed request first looks for its key once
	// more, so a retry racing its own original's commit gets the replay
	// rather than a refusal of its own booking (contract challenge C-3).
	refuse := func(write func()) {
		if keyed && h.answerKeyed(w, r, link, attempt) {
			return
		}
		write()
	}
	if keyed {
		if h.answerKeyed(w, r, link, attempt) {
			return
		}
		if afterBookingReplayLookup != nil {
			afterBookingReplayLookup()
		}
		if linkExhausted(link) {
			refuse(func() { writeError(w, "link is no longer accepting bookings", http.StatusGone) })
			return
		}
		if !durationAllowed(link, requestedDuration) {
			writeError(w, "invalid duration", http.StatusBadRequest)
			return
		}
	}
	// The offerable-start rule (PAC-72): [start, end) must be exactly a slot
	// the listing offers now, compared as instants; anything else is the 409
	// and nothing is written. Order of evaluation (revision 2.1): the link's
	// zone (a failed read is the 500, never UTC); then the checks that need
	// no host data (horizon, days/hours/grid, notice); only then busy time
	// (this link's own bookings, then every accepted host's). Busy time that
	// cannot be gathered from storage is a 500, never a 409.
	loc, err := linkLocation(db, h.ownerZone, link.OwnerUserID)
	if err != nil {
		logServerError(r, "booking conflict check", err, http.StatusInternalServerError)
		writeError(w, "booking conflict check failed", http.StatusInternalServerError)
		return
	}
	now := h.now()
	if reason, refused := cheapRefusal(link, loc, start, end, requestedDuration, now); refused {
		refuse(func() { writeRefusal(w, link, reason) })
		return
	}
	if conflict, e := storage.HasOverlappingBooking(db, link.ID, start, end); e != nil {
		logServerError(r, "booking conflict check", e, http.StatusInternalServerError)
		writeError(w, "booking conflict check failed", http.StatusInternalServerError)
		return
	} else if conflict {
		refuse(func() { writeRefusal(w, link, gen.Taken) })
		return
	}
	offered, err := h.offeredSlots(r.Context(), link, loc, calendarDay(start, loc), requestedDuration, now)
	if budgetCut(err) {
		budget.WriteExpired(w) // AC-A18: a host not read is the 503, never a 409
		return
	}
	if err != nil {
		logServerError(r, "booking conflict check", err, http.StatusInternalServerError)
		writeError(w, "booking conflict check failed", http.StatusInternalServerError)
		return
	}
	var slot *engine.AvailableSlot
	for i := range offered {
		if offered[i].Start.Equal(start) && offered[i].End.Equal(end) {
			slot = &offered[i]
			break
		}
	}
	if slot == nil {
		// Every cheap check passed, so the slot is busy for a host: a
		// calendar event (with buffers), a focus block, a booking on another
		// link, an unreadable calendar (D-6) or no accepted host (D-10).
		refuse(func() { writeRefusal(w, link, gen.HostBusy) })
		return
	}
	// PAC-81 AC-A19: 503 only when the budget ran out before the insert was
	// sent (nothing saved); once sent, the insert's own result is 201 or 500.
	booking, err := h.eng.ConfirmBooking(r.Context(), link, body.Name, body.Email, slot.Start, slot.End, body.Notes, key)
	switch {
	case errors.Is(err, budget.ErrExpired):
		budget.WriteExpired(w)
		return
	case errors.Is(err, engine.ErrLinkGone):
		// PAC-101: the link was deleted or paused while the request was
		// handled, before the save took its lock on it. Nothing was written:
		// the same 404 as for a link deleted before the request.
		writeError(w, "not found", http.StatusNotFound)
		return
	case errors.Is(err, engine.ErrHostCalendarUnreadable):
		// PAC-72 D-6: an unreadable host calendar counts as busy.
		refuse(func() { writeRefusal(w, link, gen.HostBusy) })
		return
	case errors.Is(err, engine.ErrSlotTaken):
		// PAC-82: lost, at the save, to a booking of this link saved first.
		refuse(func() { writeRefusal(w, link, gen.Taken) })
		return
	case errors.Is(err, engine.ErrHostBusy):
		// PAC-82: lost, at the save, to a host's booking on another link.
		refuse(func() { writeRefusal(w, link, gen.HostBusy) })
		return
	case errors.Is(err, engine.ErrLinkExhausted):
		// PAC-82: the use limit was reached by bookings saved first.
		refuse(func() { writeError(w, "link is no longer accepting bookings", http.StatusGone) })
		return
	case errors.Is(err, engine.ErrIdempotencyKeyReused):
		writeError(w, errIdempotencyKeyReused, http.StatusConflict)
		return
	case err != nil:
		writeServerError(w, r, "booking failed", err, http.StatusInternalServerError)
		return
	}
	// The booking is saved (or, PAC-82, replayed): the answer is the 201,
	// past the budget if need be.
	h.writeBooked(w, r, link, booking)
}

// writeBooked writes the 201 of a saved or replayed booking, built from the
// stored row with the link's current title and hosts.
func (h *bookingHandlers) writeBooked(w http.ResponseWriter, r *http.Request, link *storage.SchedulingLink, booking *storage.Booking) {
	saved := storage.Bind(context.WithoutCancel(r.Context()), h.db)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(h.confirmation(saved, link, booking))
}

// The PAC-82 bodies: the key's 400 and the key-reused 409.
const (
	errInvalidIdempotencyKey = "invalid Idempotency-Key header"
	errIdempotencyKeyReused  = "idempotency key already used"
)

// idempotencyKeyPattern is the contract's format: 16 to 255 characters from
// A-Z a-z 0-9 - _ (components/parameters/BookingIdempotencyKey).
var idempotencyKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,255}$`)

// idempotencyKey reads the request's Idempotency-Key. Absent or empty is
// "", true (a request without a key). ok is false for a value outside the
// format (quotes included) or a header sent on two or more field lines.
func idempotencyKey(r *http.Request) (key string, ok bool) {
	values := r.Header.Values("Idempotency-Key")
	switch {
	case len(values) == 0:
		return "", true
	case len(values) > 1:
		return "", false
	case values[0] == "":
		return "", true
	case !idempotencyKeyPattern.MatchString(values[0]):
		return "", false
	}
	return values[0], true
}

// answerKeyed decides a keyed attempt on the pool (contract section 1.1): a
// booking saved with the key on this link with the same details is the
// replay 201; with other details or 24 hours old or more, the key-reused
// 409. It reports whether it answered (a failed read is the 500).
func (h *bookingHandlers) answerKeyed(w http.ResponseWriter, r *http.Request, link *storage.SchedulingLink, attempt *storage.Booking) bool {
	replay, reused, err := storage.ResolveIdempotencyKey(storage.Bind(r.Context(), h.db), link.ID, attempt.IdempotencyKey, attempt)
	switch {
	case err != nil:
		writeServerError(w, r, "booking idempotency lookup", err, http.StatusInternalServerError)
		return true
	case replay != nil:
		h.writeBooked(w, r, link, replay)
		return true
	case reused:
		writeError(w, errIdempotencyKeyReused, http.StatusConflict)
		return true
	}
	return false
}

// afterBookingReplayLookup is a test seam (PAC-82 AC-29): it runs in
// createBooking after a keyed request's first replay lookup found no booking
// and before any check that can refuse it. Nil in production.
var afterBookingReplayLookup func()

// afterBookingLinkRead is a test seam (PAC-101 contract §5): it runs in
// createBooking after the link read and the 410 check, before the body is
// read and before any host calendar is read. Nil in production.
var afterBookingLinkRead func()

// budgetCut reports whether err means the request budget cut the work short
// (PAC-81 AC-A17, AC-A18): the answer is the shared 503.
func budgetCut(err error) bool { return errors.Is(err, budget.ErrExpired) }
