package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
}

func TestPublicBookingSlotsContract(t *testing.T) {
	ownerID := createTestUser(t, "public-slots-owner@example.com")
	link := createPublicBookingLink(t, ownerID, "public-slots", "Public Slots", 0, "reusable", nil)
	day := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	eng := &fakeBookingFlow{slotsResult: []engine.AvailableSlot{{Start: time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC), End: time.Date(2026, 9, 12, 10, 30, 0, 0, time.UTC)}}}
	r := setupPublicBookingRoutes(t, eng)

	req := httptest.NewRequest(http.MethodGet, "/api/book/"+link.Slug+"/slots?date=2026-09-12&duration=30", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	if !eng.slotsDate.Equal(day) || eng.slotsDuration != 30 {
		t.Fatalf("engine call = %v duration %d, want %v and 30", eng.slotsDate, eng.slotsDuration, day)
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
	start := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	end := start.Add(30 * time.Minute)
	eng := &fakeBookingFlow{confirmResult: &storage.Booking{ID: bookingID, LinkID: link.ID, BookerName: "Alice", BookerEmail: "alice@example.com", StartTime: start, EndTime: end, Notes: "Need a demo"}}
	r := setupPublicBookingRoutes(t, eng)

	body := `{"name":"Alice","email":"alice@example.com","start":"2026-09-12T10:00:00Z","duration_minutes":30,"notes":"Need a demo"}`
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
	r := setupPublicBookingRoutes(t, &fakeBookingFlow{})

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
		{name: "booking rejects too soon", method: http.MethodPost, path: "/api/book/" + link.Slug, body: `{"name":"Alice","email":"alice@example.com","start":"2026-08-11T10:00:00Z","duration_minutes":30}`, want: http.StatusUnprocessableEntity, wantContains: "booking is too soon"},
		{name: "booking rejects invalid duration", method: http.MethodPost, path: "/api/book/" + link.Slug, body: `{"name":"Alice","email":"alice@example.com","start":"2026-09-12T10:00:00Z","duration_minutes":45}`, want: http.StatusBadRequest, wantContains: "invalid duration"},
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
	eng := &fakeBookingFlow{confirmErr: errors.New("calendar write failed")}
	r := setupPublicBookingRoutes(t, eng)

	body := `{"name":"Alice","email":"alice@example.com","start":"2026-09-12T10:00:00Z","duration_minutes":30}`
	req := httptest.NewRequest(http.MethodPost, "/api/book/"+link.Slug, bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body: %s", w.Code, w.Body.String())
	}
}
