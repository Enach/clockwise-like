package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/storage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func setupSchedulingLinkRoutes(t *testing.T) *chi.Mux {
	t.Helper()
	h := newSchedulingLinkHandlers(openTestDB(t), nil)
	r := chi.NewRouter()
	r.Post("/api/scheduling-links", h.createLink)
	r.Get("/api/scheduling-links", h.listLinks)
	r.Get("/api/scheduling-links/host-invites", h.listHostInvites)
	r.Get("/api/scheduling-links/invites", h.listHostInvites)
	r.Post("/api/scheduling-links/host-invites/{id}/accept", h.acceptInvite)
	r.Post("/api/scheduling-links/host-invites/{id}/decline", h.declineInvite)
	r.Get("/api/scheduling-links/{id}", h.getLink)
	r.Patch("/api/scheduling-links/{id}", h.updateLink)
	r.Delete("/api/scheduling-links/{id}", h.deleteLink)
	r.Get("/api/scheduling-links/{id}/bookings", h.listBookings)
	r.Post("/api/scheduling-links/{id}/hosts", h.inviteHost)
	r.Post("/api/scheduling-links/{id}/accept", h.acceptInvite)
	r.Post("/api/scheduling-links/{id}/decline", h.declineInvite)
	r.Post("/api/scheduling-links/{id}/leave", h.leaveLink)
	return r
}

func createSchedulingLinkRecord(t *testing.T, ownerID uuid.UUID, slug, title string) *storage.SchedulingLink {
	t.Helper()
	link, err := storage.CreateSchedulingLink(openTestDB(t), &storage.SchedulingLink{
		OwnerUserID:     ownerID,
		Slug:            slug,
		Title:           title,
		DurationOptions: []int{30},
		DaysOfWeek:      []int{1, 2, 3, 4, 5},
		WindowStart:     "09:00",
		WindowEnd:       "17:00",
		UsageType:       "reusable",
		Active:          true,
	})
	if err != nil {
		t.Fatalf("create scheduling link: %v", err)
	}
	if _, err := storage.AddLinkHost(openTestDB(t), link.ID, ownerID, "accepted"); err != nil {
		t.Fatalf("add owner host: %v", err)
	}
	return link
}

func decodeOwnedShared(t *testing.T, body *bytes.Buffer) map[string][]schedulingLinkDTO {
	t.Helper()
	var got map[string][]schedulingLinkDTO
	if err := json.NewDecoder(body).Decode(&got); err != nil {
		t.Fatalf("decode owned/shared response: %v", err)
	}
	return got
}

func TestSchedulingLinksCreateAndListContract(t *testing.T) {
	r := setupSchedulingLinkRoutes(t)
	ownerID := createTestUser(t, "sched-owner@example.com")
	coHostID := createTestUser(t, "sched-cohost@example.com")
	_ = coHostID

	body := `{"title":"Intro Call","slug":" Intro Call ","duration_options":[30,45],"days_of_week":[1,3,5],"window_start_time":"10:00","window_end_time":"16:00","buffer_before":5,"buffer_after":10,"min_notice_minutes":120,"usage_type":"reusable","co_host_emails":["sched-cohost@example.com"]}`
	req := httptest.NewRequest(http.MethodPost, "/api/scheduling-links", bytes.NewBufferString(body))
	req = withUser(req, ownerID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body: %s", w.Code, w.Body.String())
	}
	var created schedulingLinkDTO
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.Slug != "intro-call" {
		t.Fatalf("slug = %q, want intro-call", created.Slug)
	}
	if created.Title != "Intro Call" || created.WindowStart != "10:00:00" || created.WindowEnd != "16:00:00" {
		t.Fatalf("created = %+v, want normalized scheduling link fields", created)
	}
	if len(created.Durations) != 2 || created.Durations[0] != 30 || len(created.Days) != 3 || !created.IsOwner {
		t.Fatalf("created = %+v, want durations/days/is_owner populated", created)
	}
	if len(created.Hosts) != 2 {
		t.Fatalf("hosts = %+v, want owner + pending cohost", created.Hosts)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/scheduling-links", nil)
	req = withUser(req, ownerID)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200", w.Code)
	}
	got := decodeOwnedShared(t, w.Body)
	if len(got["owned"]) != 1 || len(got["shared"]) != 0 {
		t.Fatalf("owned/shared = %+v, want one owned and no shared", got)
	}
}

func TestSchedulingLinksCreateRejectsInvalidUsageType(t *testing.T) {
	r := setupSchedulingLinkRoutes(t)
	ownerID := createTestUser(t, "sched-invalid-usage@example.com")

	req := httptest.NewRequest(http.MethodPost, "/api/scheduling-links", bytes.NewBufferString(`{"title":"Bad","slug":"bad","usage_type":"forever"}`))
	req = withUser(req, ownerID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body: %s", w.Code, w.Body.String())
	}
}

func TestSchedulingLinksPatchUsesFrontendAliases(t *testing.T) {
	r := setupSchedulingLinkRoutes(t)
	ownerID := createTestUser(t, "sched-patch-owner@example.com")
	coHostID := createTestUser(t, "sched-patch-cohost@example.com")
	_ = coHostID
	link := createSchedulingLinkRecord(t, ownerID, "patch-link", "Patch Link")

	body := `{"title":"Updated Link","durations":[15,30],"days":["tue","thu"],"window_start":"11:00","window_end":"15:00","min_notice_minutes":5,"buffer_before":3,"buffer_after":7,"usage_type":"single_use","active":false,"co_host_emails":["sched-patch-cohost@example.com"]}`
	req := httptest.NewRequest(http.MethodPatch, "/api/scheduling-links/"+link.ID.String(), bytes.NewBufferString(body))
	req = withUser(req, ownerID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("patch status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var updated schedulingLinkDTO
	if err := json.NewDecoder(w.Body).Decode(&updated); err != nil {
		t.Fatalf("decode patch response: %v", err)
	}
	if updated.Title != "Updated Link" || updated.WindowStart != "11:00:00" || updated.WindowEnd != "15:00:00" {
		t.Fatalf("updated = %+v, want patched title/window", updated)
	}
	if len(updated.Durations) != 2 || len(updated.Days) != 2 || updated.MinNoticeMinutes != 5 || updated.Active || updated.UsageType != "single_use" {
		t.Fatalf("updated = %+v, want alias fields applied and min_notice clamped", updated)
	}
	if len(updated.Hosts) != 2 {
		t.Fatalf("hosts = %+v, want owner + invited cohost", updated.Hosts)
	}
}

func TestSchedulingLinksHostInviteRespondAndLeaveFlow(t *testing.T) {
	r := setupSchedulingLinkRoutes(t)
	ownerID := createTestUser(t, "sched-flow-owner@example.com")
	inviteeID := createTestUser(t, "sched-flow-invitee@example.com")
	link := createSchedulingLinkRecord(t, ownerID, "flow-link", "Flow Link")

	req := httptest.NewRequest(http.MethodPost, "/api/scheduling-links/"+link.ID.String()+"/hosts", bytes.NewBufferString(`{"email":"sched-flow-invitee@example.com"}`))
	req = withUser(req, ownerID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("invite status = %d, want 201; body: %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/scheduling-links/host-invites", nil)
	req = withUser(req, inviteeID)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list invites status = %d, want 200", w.Code)
	}
	var invites []hostInviteDTO
	if err := json.NewDecoder(w.Body).Decode(&invites); err != nil {
		t.Fatalf("decode invites response: %v", err)
	}
	if len(invites) != 1 || invites[0].LinkID != link.ID.String() || invites[0].OwnerEmail != "sched-flow-owner@example.com" {
		t.Fatalf("invites = %+v, want one invite for the created link", invites)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/scheduling-links/host-invites/"+link.ID.String()+"/accept", nil)
	req = withUser(req, inviteeID)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("accept status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/scheduling-links/"+link.ID.String()+"/leave", nil)
	req = withUser(req, inviteeID)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("leave status = %d, want 204; body: %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/scheduling-links/"+link.ID.String()+"/leave", nil)
	req = withUser(req, ownerID)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("owner leave status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestSchedulingLinksOwnerOnlyBookingsAndDelete(t *testing.T) {
	r := setupSchedulingLinkRoutes(t)
	ownerID := createTestUser(t, "sched-booking-owner@example.com")
	otherID := createTestUser(t, "sched-booking-other@example.com")
	link := createSchedulingLinkRecord(t, ownerID, "booking-link", "Booking Link")
	_, err := storage.CreateBooking(openTestDB(t), &storage.Booking{
		LinkID:      link.ID,
		BookerName:  "Alice",
		BookerEmail: "alice@example.com",
		StartTime:   time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC),
		EndTime:     time.Date(2026, 8, 12, 10, 30, 0, 0, time.UTC),
		Status:      "confirmed",
	})
	if err != nil {
		t.Fatalf("create booking: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/scheduling-links/"+link.ID.String()+"/bookings", nil)
	req = withUser(req, ownerID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("owner bookings status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var bookings []*storage.Booking
	if err := json.NewDecoder(w.Body).Decode(&bookings); err != nil {
		t.Fatalf("decode bookings response: %v", err)
	}
	if len(bookings) != 1 || bookings[0].BookerEmail != "alice@example.com" {
		t.Fatalf("bookings = %+v, want one booking", bookings)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/scheduling-links/"+link.ID.String()+"/bookings", nil)
	req = withUser(req, otherID)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("other bookings status = %d, want 403; body: %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/scheduling-links/"+link.ID.String(), nil)
	req = withUser(req, otherID)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("other delete status = %d, want 403; body: %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/scheduling-links/"+link.ID.String(), nil)
	req = withUser(req, ownerID)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("owner delete status = %d, want 204; body: %s", w.Code, w.Body.String())
	}
}

func TestSchedulingLinksValidationErrorsUse422(t *testing.T) {
	r := setupSchedulingLinkRoutes(t)
	ownerID := createTestUser(t, "sched-validation-owner@example.com")
	link := createSchedulingLinkRecord(t, ownerID, "validation-link", "Validation Link")

	tests := []struct {
		name   string
		method string
		path   string
		body   string
		want   string
	}{
		{name: "create requires durations", method: http.MethodPost, path: "/api/scheduling-links", body: `{"title":"Bad","duration_options":[],"days_of_week":[1],"window_start_time":"09:00","window_end_time":"10:00","usage_type":"reusable"}`, want: "at least one duration is required"},
		{name: "create requires days", method: http.MethodPost, path: "/api/scheduling-links", body: `{"title":"Bad","duration_options":[30],"days_of_week":[],"window_start_time":"09:00","window_end_time":"10:00","usage_type":"reusable"}`, want: "at least one day is required"},
		{name: "create rejects inverted window", method: http.MethodPost, path: "/api/scheduling-links", body: `{"title":"Bad","duration_options":[30],"days_of_week":[1],"window_start_time":"18:00","window_end_time":"09:00","usage_type":"reusable"}`, want: "window_start must be before window_end"},
		{name: "create rejects negative buffers", method: http.MethodPost, path: "/api/scheduling-links", body: `{"title":"Bad","duration_options":[30],"days_of_week":[1],"window_start_time":"09:00","window_end_time":"10:00","buffer_before":-1,"usage_type":"reusable"}`, want: "buffers must be zero or positive"},
		{name: "create recurring needs max uses", method: http.MethodPost, path: "/api/scheduling-links", body: `{"title":"Bad","duration_options":[30],"days_of_week":[1],"window_start_time":"09:00","window_end_time":"10:00","usage_type":"recurring"}`, want: "max_uses must be set to a positive integer for recurring links"},
		{name: "patch recurring needs max uses", method: http.MethodPatch, path: "/api/scheduling-links/" + link.ID.String(), body: `{"usage_type":"recurring","max_uses":0}`, want: "max_uses must be set to a positive integer for recurring links"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, bytes.NewBufferString(tc.body))
			req = withUser(req, ownerID)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422; body: %s", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), tc.want) {
				t.Fatalf("body = %s, want message containing %q", w.Body.String(), tc.want)
			}
		})
	}
}
