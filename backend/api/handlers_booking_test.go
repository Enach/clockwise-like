package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/storage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type fakeBookingFlow struct {
	slotsDate     time.Time
	slotsDuration int
	slotsResult   []engine.AvailableSlot
	slotsErr      error
	confirmLinkID uuid.UUID
	confirmName   string
	confirmEmail  string
	confirmStart  time.Time
	confirmEnd    time.Time
	confirmNotes  string
	confirmResult *storage.Booking
	confirmErr    error
}

func (f *fakeBookingFlow) CollectiveSlots(_ context.Context, link *storage.SchedulingLink, date time.Time, durationMinutes int) ([]engine.AvailableSlot, error) {
	f.slotsDate = date
	f.slotsDuration = durationMinutes
	if link != nil {
		f.confirmLinkID = link.ID
	}
	return f.slotsResult, f.slotsErr
}

func (f *fakeBookingFlow) ConfirmBooking(_ context.Context, link *storage.SchedulingLink, bookerName, bookerEmail string, start, end time.Time, notes string) (*storage.Booking, error) {
	if link != nil {
		f.confirmLinkID = link.ID
	}
	f.confirmName = bookerName
	f.confirmEmail = bookerEmail
	f.confirmStart = start
	f.confirmEnd = end
	f.confirmNotes = notes
	return f.confirmResult, f.confirmErr
}

func setupPublicBookingRoutes(t *testing.T, eng BookingFlow) *chi.Mux {
	t.Helper()
	// NEW-booking-rate-limit: every test shares one peer address.
	resetBookingLimiters(t)
	h := newBookingHandlersWithEngine(openTestDB(t), eng)
	r := chi.NewRouter()
	r.Get("/api/book/{slug}", h.getLinkInfo)
	r.Get("/api/book/{slug}/slots", h.getSlots)
	r.Post("/api/book/{slug}", h.createBooking)
	return r
}

func createPublicBookingLink(t *testing.T, ownerID uuid.UUID, slug, title string, minNotice int, usage string, maxUses *int) *storage.SchedulingLink {
	t.Helper()
	link, err := storage.CreateSchedulingLink(openTestDB(t), &storage.SchedulingLink{
		OwnerUserID:      ownerID,
		Slug:             slug,
		Title:            title,
		DurationOptions:  []int{30, 60},
		DaysOfWeek:       []int{1, 2, 3, 4, 5},
		WindowStart:      "09:00",
		WindowEnd:        "17:00",
		MinNoticeMinutes: minNotice,
		UsageType:        usage,
		MaxUses:          maxUses,
		Active:           true,
	})
	if err != nil {
		t.Fatalf("create public booking link: %v", err)
	}
	if _, err := storage.AddLinkHost(openTestDB(t), link.ID, ownerID, "accepted"); err != nil {
		t.Fatalf("add link owner host: %v", err)
	}
	return link
}

// futureBookingStart is 10:00 UTC on the first weekday at least 72 hours
// ahead: a start a createPublicBookingLink link offers whatever day the suite
// runs (a link day, inside the hours, on the grid, after the notice, inside
// the 60-day horizon; PAC-72).
func futureBookingStart() time.Time {
	future := time.Now().UTC().Add(72 * time.Hour)
	for future.Weekday() == time.Saturday || future.Weekday() == time.Sunday {
		future = future.AddDate(0, 0, 1)
	}
	return time.Date(future.Year(), future.Month(), future.Day(), 10, 0, 0, 0, time.UTC)
}

func TestPublicBookingLinkInfoContract(t *testing.T) {
	ownerID := createTestUser(t, "public-link-owner@example.com")
	link := createPublicBookingLink(t, ownerID, "public-intro", "Public Intro", 90, "reusable", nil)
	r := setupPublicBookingRoutes(t, &fakeBookingFlow{})

	req := httptest.NewRequest(http.MethodGet, "/api/book/"+link.Slug, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var got publicLinkDTO
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Slug != link.Slug || got.Title != link.Title || got.MinNoticeMinutes != 90 || got.UsageType != "reusable" {
		t.Fatalf("public link = %+v, want link metadata", got)
	}
	if len(got.Durations) != 2 || len(got.Hosts) != 1 || got.Hosts[0].Email != "public-link-owner@example.com" {
		t.Fatalf("public link = %+v, want durations and owner host", got)
	}
	// PAC-72 AC-32: the zone in use (UTC: the owner has no settings) and the
	// link's horizon (60 by default).
	if got.TimeZone != "UTC" || got.BookingHorizonDays != 60 {
		t.Fatalf("public link zone/horizon = %q/%d, want UTC/60", got.TimeZone, got.BookingHorizonDays)
	}
}

func TestPublicBookingSlotsContract(t *testing.T) {
	ownerID := createTestUser(t, "public-slots-owner@example.com")
	link := createPublicBookingLink(t, ownerID, "public-slots", "Public Slots", 0, "reusable", nil)
	start := futureBookingStart()
	day := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.UTC)
	eng := &fakeBookingFlow{slotsResult: []engine.AvailableSlot{{Start: start, End: start.Add(30 * time.Minute)}}}
	r := setupPublicBookingRoutes(t, eng)

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/book/%s/slots?date=%s&duration=30", link.Slug, day.Format(time.DateOnly)), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	// PAC-72 revision 2 (D-9): the engine gets the calendar day in the link's
	// zone (UTC here, the owner has no settings), as a time on that day
	// (noon, whose wall clock always exists).
	if want := day.Add(12 * time.Hour); !eng.slotsDate.Equal(want) || eng.slotsDate.Location() != time.UTC || eng.slotsDuration != 30 {
		t.Fatalf("engine call = %v duration %d, want %v and 30", eng.slotsDate, eng.slotsDuration, want)
	}
	var got struct {
		Slots          []engine.AvailableSlot `json:"slots"`
		AvailableDates []string               `json:"available_dates"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode slots response: %v", err)
	}
	if len(got.Slots) != 1 || got.Slots[0].Start.Hour() != 10 || len(got.AvailableDates) != 0 {
		t.Fatalf("slots response = %+v, want one slot and no summary dates", got)
	}
}

func TestPublicBookingCreateContract(t *testing.T) {
	ownerID := createTestUser(t, "public-create-owner@example.com")
	link := createPublicBookingLink(t, ownerID, "public-create", "Public Create", 0, "reusable", nil)
	bookingID := uuid.New()
	start := futureBookingStart()
	end := start.Add(30 * time.Minute)
	eng := &fakeBookingFlow{
		slotsResult:   []engine.AvailableSlot{{Start: start, End: end}}, // PAC-72: the listing offers it
		confirmResult: &storage.Booking{ID: bookingID, LinkID: link.ID, BookerName: "Alice", BookerEmail: "alice@example.com", StartTime: start, EndTime: end, Notes: "Need a demo"},
	}
	r := setupPublicBookingRoutes(t, eng)

	body := fmt.Sprintf(`{"name":"Alice","email":"alice@example.com","start":"%s","duration_minutes":30,"notes":"Need a demo"}`, start.Format(time.RFC3339))
	req := httptest.NewRequest(http.MethodPost, "/api/book/"+link.Slug, bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body: %s", w.Code, w.Body.String())
	}
	if eng.confirmLinkID != link.ID || eng.confirmName != "Alice" || eng.confirmEmail != "alice@example.com" || !eng.confirmStart.Equal(start) || !eng.confirmEnd.Equal(end) || eng.confirmNotes != "Need a demo" {
		t.Fatalf("confirm call = %+v, want booking payload forwarded", eng)
	}
	var got bookingConfirmationDTO
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode booking response: %v", err)
	}
	if got.ID != bookingID.String() || got.LinkSlug != link.Slug || got.DurationMinutes != 30 || got.BookerEmail != "alice@example.com" {
		t.Fatalf("booking response = %+v, want confirmation dto", got)
	}
}

func TestPublicBookingErrorsContract(t *testing.T) {
	ownerID := createTestUser(t, "public-errors-owner@example.com")
	link := createPublicBookingLink(t, ownerID, "public-errors", "Public Errors", 120, "reusable", nil)
	// PAC-72: a fixed clock (Tuesday 2030-01-08 10:00 UTC) so the too-soon
	// start is always on a link day, inside the hours and on the grid: only
	// the 120-minute notice refuses it. The engine reports it free.
	now := time.Date(2030, 1, 8, 10, 0, 0, 0, time.UTC)
	futureStart := now.AddDate(0, 0, 1).Format(time.RFC3339)
	tooSoonAt := now.Add(30 * time.Minute)
	tooSoon := tooSoonAt.Format(time.RFC3339)
	eng := &fakeBookingFlow{slotsResult: []engine.AvailableSlot{{Start: tooSoonAt, End: tooSoonAt.Add(30 * time.Minute)}}}
	h := newBookingHandlersWithEngine(openTestDB(t), eng)
	h.now = func() time.Time { return now }
	r := chi.NewRouter()
	r.Get("/api/book/{slug}/slots", h.getSlots)
	r.Post("/api/book/{slug}", h.createBooking)
	// The same link with specific refusals names the rule: too_soon.
	specific := createPublicBookingLink(t, ownerID, "public-errors-specific", "Public Errors Specific", 120, "reusable", nil)
	if _, err := openTestDB(t).Exec(`UPDATE scheduling_links SET refusal_message = 'specific' WHERE id = $1`, specific.ID); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name         string
		method       string
		path         string
		body         string
		want         int
		wantContains string
	}{
		{name: "slots reject invalid duration", method: http.MethodGet, path: "/api/book/" + link.Slug + "/slots?duration=45", want: http.StatusBadRequest, wantContains: "invalid duration"},
		{name: "slots reject invalid date", method: http.MethodGet, path: "/api/book/" + link.Slug + "/slots?date=12-08-2026", want: http.StatusBadRequest, wantContains: "invalid date format"},
		{name: "booking requires fields", method: http.MethodPost, path: "/api/book/" + link.Slug, body: `{}`, want: http.StatusBadRequest, wantContains: "name, email, and start are required"},
		{name: "booking rejects too soon", method: http.MethodPost, path: "/api/book/" + link.Slug, body: fmt.Sprintf(`{"name":"Alice","email":"alice@example.com","start":"%s","duration_minutes":30}`, tooSoon), want: http.StatusConflict, wantContains: `{"error":"slot is no longer available"}`},
		{name: "booking rejects too soon (specific: the notice rule)", method: http.MethodPost, path: "/api/book/" + specific.Slug, body: fmt.Sprintf(`{"name":"Alice","email":"alice@example.com","start":"%s","duration_minutes":30}`, tooSoon), want: http.StatusConflict, wantContains: `{"error":"slot is too soon","reason":"too_soon"}`},
		{name: "booking rejects invalid duration", method: http.MethodPost, path: "/api/book/" + link.Slug, body: fmt.Sprintf(`{"name":"Alice","email":"alice@example.com","start":"%s","duration_minutes":45}`, futureStart), want: http.StatusBadRequest, wantContains: "invalid duration"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, bytes.NewBufferString(tc.body))
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d; body: %s", w.Code, tc.want, w.Body.String())
			}
			if tc.wantContains != "" && !bytes.Contains(w.Body.Bytes(), []byte(tc.wantContains)) {
				t.Fatalf("body = %s, want substring %q", w.Body.String(), tc.wantContains)
			}
		})
	}
}

func TestPublicBookingPropagatesEngineFailure(t *testing.T) {
	ownerID := createTestUser(t, "public-engine-owner@example.com")
	link := createPublicBookingLink(t, ownerID, "public-engine", "Public Engine", 0, "reusable", nil)
	start := futureBookingStart()
	eng := &fakeBookingFlow{
		slotsResult: []engine.AvailableSlot{{Start: start, End: start.Add(30 * time.Minute)}}, // PAC-72: the listing offers it
		confirmErr:  errors.New("calendar write failed"),
	}
	r := setupPublicBookingRoutes(t, eng)

	body := fmt.Sprintf(`{"name":"Alice","email":"alice@example.com","start":"%s","duration_minutes":30}`, start.Format(time.RFC3339))
	req := httptest.NewRequest(http.MethodPost, "/api/book/"+link.Slug, bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body: %s", w.Code, w.Body.String())
	}
}
