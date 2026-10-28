package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Enach/paceday/backend/api/gen"
	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/engine"
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

func (h *bookingHandlers) publicLink(link *storage.SchedulingLink) publicLinkDTO {
	dto := publicLinkDTO{Slug: link.Slug, Title: link.Title, Durations: link.DurationOptions, Hosts: []publicHostDTO{}, MinNoticeMinutes: link.MinNoticeMinutes, UsageType: link.UsageType}
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

func (h *bookingHandlers) confirmation(link *storage.SchedulingLink, booking *storage.Booking) bookingConfirmationDTO {
	dto := bookingConfirmationDTO{ID: booking.ID.String(), LinkSlug: link.Slug, Title: link.Title, Start: booking.StartTime.UTC().Format(time.RFC3339), End: booking.EndTime.UTC().Format(time.RFC3339), DurationMinutes: int(booking.EndTime.Sub(booking.StartTime).Minutes()), Hosts: []publicHostDTO{}, BookerName: booking.BookerName, BookerEmail: booking.BookerEmail, Notes: booking.Notes}
	for _, host := range func() []*storage.LinkHost { hosts, _ := storage.GetAcceptedHosts(h.db, link.ID); return hosts }() {
		if user, err := storage.GetUserByID(h.db, host.UserID); err == nil && user != nil {
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
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(h.publicLink(link))
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

// bookingHorizonDays is the fixed booking horizon (PAC-72 D-1): today (UTC)
// and the next 59 days, for both listing modes and the booking.
const bookingHorizonDays = 60

// errSlotUnavailable is the one body of every offerable-start refusal (PAC-72
// D-5, D-12), pinned by the contract's BookingSlotUnavailableError.
var errSlotUnavailable = string(gen.SlotIsNoLongerAvailable)

// utcDay is the UTC calendar day containing t, at 00:00 UTC.
func utcDay(t time.Time) time.Time {
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
}

// linkOpenOn reports whether day (a UTC day) is one of the link's days of the
// week (0 = Sunday).
func linkOpenOn(link *storage.SchedulingLink, day time.Time) bool {
	for _, d := range link.DaysOfWeek {
		if d == int(day.Weekday()) {
			return true
		}
	}
	return false
}

// offeredSlots is the offerable-start rule (PAC-72), the one rule both the
// slot listing and the booking apply: the slots of duration minutes on the UTC
// day containing day that are on one of the link's days, inside the booking
// horizon, free for every accepted host (CollectiveSlots: the link's window on
// the 15-minute grid, busy time, at least one accepted host) and at least the
// minimum notice after now. Days, hours and the horizon are evaluated in UTC
// whatever zone the process runs in (D-9). The returned slots are in UTC. An
// error means busy time could not be gathered; nothing may be offered or
// accepted on that day.
func (h *bookingHandlers) offeredSlots(ctx context.Context, link *storage.SchedulingLink, day time.Time, duration int, now time.Time) ([]engine.AvailableSlot, error) {
	day = utcDay(day)
	today := utcDay(now)
	out := make([]engine.AvailableSlot, 0)
	if day.Before(today) || !day.Before(today.AddDate(0, 0, bookingHorizonDays)) || !linkOpenOn(link, day) {
		return out, nil
	}
	slots, err := h.eng.CollectiveSlots(ctx, link, day, duration)
	if err != nil {
		return nil, err
	}
	earliest := now.Add(time.Duration(link.MinNoticeMinutes) * time.Minute)
	for _, slot := range slots {
		if !slot.Start.Before(earliest) {
			out = append(out, engine.AvailableSlot{Start: slot.Start.UTC(), End: slot.End.UTC()})
		}
	}
	return out, nil
}

// GET /api/book/{slug}/slots?date=YYYY-MM-DD&duration=30
func (h *bookingHandlers) getSlots(w http.ResponseWriter, r *http.Request) {
	// NEW-booking-rate-limit: counted before anything else, so 429 precedes
	// every other status.
	if !allowPublicBooking(w, r, bookingSlotsLimiter) {
		return
	}
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
	duration := 30
	if len(link.DurationOptions) > 0 {
		duration = link.DurationOptions[0]
	}
	if raw := r.URL.Query().Get("duration"); raw != "" {
		duration, err = strconv.Atoi(raw)
		if err != nil || !durationAllowed(link, duration) {
			writeError(w, "invalid duration", http.StatusBadRequest)
			return
		}
	}
	now := h.now()
	dateStr := r.URL.Query().Get("date")
	if dateStr == "" {
		// The horizon's days that offer at least one slot. A day whose busy
		// time cannot be gathered is omitted (PAC-72 D-7).
		days := []string{}
		today := utcDay(now)
		for i := 0; i < bookingHorizonDays; i++ {
			date := today.AddDate(0, 0, i)
			slots, e := h.offeredSlots(r.Context(), link, date, duration, now)
			if e == nil && len(slots) > 0 {
				days = append(days, date.Format("2006-01-02"))
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"available_dates": days, "slots": []engine.AvailableSlot{}})
		return
	}
	date, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		writeError(w, "invalid date format, use YYYY-MM-DD", http.StatusBadRequest)
		return
	}
	available, err := h.offeredSlots(r.Context(), link, date, duration, now)
	if err != nil {
		writeError(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"slots": available, "available_dates": []string{}})
}

// POST /api/book/{slug}
func (h *bookingHandlers) createBooking(w http.ResponseWriter, r *http.Request) {
	// NEW-booking-rate-limit: counted before the link lookup and the body, so
	// 429 precedes every other status and a refused request writes nothing.
	if !allowPublicBooking(w, r, bookingCreateLimiter) {
		return
	}
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
	if !durationAllowed(link, requestedDuration) {
		writeError(w, "invalid duration", http.StatusBadRequest)
		return
	}
	if !end.After(start) {
		writeError(w, "end must be after start", http.StatusBadRequest)
		return
	}
	// The offerable-start rule (PAC-72): [start, end) must be exactly a slot
	// the listing offers now, compared as instants; anything else is the one
	// 409, and nothing is written. Busy time that cannot be gathered is a 500,
	// never a 409.
	offered, err := h.offeredSlots(r.Context(), link, start, requestedDuration, h.now())
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
		writeError(w, errSlotUnavailable, http.StatusConflict)
		return
	}
	// The rule already counts this link's own bookings as busy; this check is
	// kept as a second guard on the same link.
	if conflict, e := storage.HasOverlappingBooking(h.db, link.ID, slot.Start, slot.End); e != nil {
		writeError(w, "booking conflict check failed", http.StatusInternalServerError)
		return
	} else if conflict {
		writeError(w, errSlotUnavailable, http.StatusConflict)
		return
	}
	booking, err := h.eng.ConfirmBooking(r.Context(), link, body.Name, body.Email, slot.Start, slot.End, body.Notes)
	if err != nil {
		writeServerError(w, r, "booking failed", err, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(h.confirmation(link, booking))
}
