package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

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
}

func newBookingHandlers(db *sql.DB, oauthConfig *oauth2.Config) *bookingHandlers {
	return newBookingHandlersWithEngine(db, &engine.BookingEngine{DB: db, OAuthConfig: oauthConfig})
}

func newBookingHandlersWithEngine(db *sql.DB, eng BookingFlow) *bookingHandlers {
	return &bookingHandlers{eng: eng, db: db}
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

func (h *bookingHandlers) slotAllowed(link *storage.SchedulingLink, slot engine.AvailableSlot, now time.Time) bool {
	return !slot.Start.Before(now.Add(time.Duration(link.MinNoticeMinutes) * time.Minute))
}

// GET /api/book/{slug}/slots?date=YYYY-MM-DD&duration=30
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
	now := time.Now()
	dateStr := r.URL.Query().Get("date")
	if dateStr == "" {
		days := []string{}
		for i := 0; i < 60; i++ {
			date := now.AddDate(0, 0, i)
			slots, e := h.eng.CollectiveSlots(r.Context(), link, date, duration)
			if e != nil {
				// PAC-81 AC-A2: a budget cut or an ended request is a 503,
				// never a truncated list of dates.
				if budgetCut(e) || r.Context().Err() != nil {
					budget.WriteExpired(w)
					return
				}
				continue
			}
			for _, slot := range slots {
				if h.slotAllowed(link, slot, now) {
					days = append(days, date.Format("2006-01-02"))
					break
				}
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
	slots, err := h.eng.CollectiveSlots(r.Context(), link, date, duration)
	if budgetCut(err) {
		budget.WriteExpired(w) // AC-A18: a host not read, never a partial 200
		return
	}
	if err != nil {
		writeError(w, "internal error", http.StatusInternalServerError)
		return
	}
	available := make([]engine.AvailableSlot, 0, len(slots))
	for _, slot := range slots {
		if h.slotAllowed(link, slot, now) {
			available = append(available, slot)
		}
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
		actualDuration := int(end.Sub(start).Minutes())
		if requestedDuration != 0 && requestedDuration != actualDuration {
			writeError(w, "duration does not match end time", http.StatusBadRequest)
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
	if !h.slotAllowed(link, engine.AvailableSlot{Start: start, End: end}, time.Now()) {
		writeError(w, "booking is too soon", http.StatusUnprocessableEntity)
		return
	}
	if conflict, e := storage.HasOverlappingBooking(db, link.ID, start, end); e != nil {
		writeError(w, "booking conflict check failed", http.StatusInternalServerError)
		return
	} else if conflict {
		writeError(w, "slot is no longer available", http.StatusConflict)
		return
	}
	// PAC-81 AC-A19: 503 only when the budget ran out before the insert was
	// sent (nothing saved); once sent, the insert's own result is 201 or 500.
	booking, err := h.eng.ConfirmBooking(r.Context(), link, body.Name, body.Email, start, end, body.Notes)
	if errors.Is(err, budget.ErrExpired) {
		budget.WriteExpired(w)
		return
	}
	if err != nil {
		writeServerError(w, r, "booking failed", err, http.StatusInternalServerError)
		return
	}
	// The booking is saved: the answer is the 201, past the budget if need be.
	saved := storage.Bind(context.WithoutCancel(r.Context()), h.db)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(h.confirmation(saved, link, booking))
}

// budgetCut reports whether err means the request budget cut the work short
// (PAC-81 AC-A17, AC-A18): the answer is the shared 503.
func budgetCut(err error) bool { return errors.Is(err, budget.ErrExpired) }
