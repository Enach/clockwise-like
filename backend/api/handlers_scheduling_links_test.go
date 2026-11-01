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
	ownerID := createConfirmedUser(t, "sched-owner@example.com")
	coHostID := createConfirmedUser(t, "sched-cohost@example.com")
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
	ownerID := createConfirmedUser(t, "sched-invalid-usage@example.com")

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
	ownerID := createConfirmedUser(t, "sched-patch-owner@example.com")
	coHostID := createConfirmedUser(t, "sched-patch-cohost@example.com")
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
	ownerID := createConfirmedUser(t, "sched-flow-owner@example.com")
	inviteeID := createConfirmedUser(t, "sched-flow-invitee@example.com")
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
	ownerID := createConfirmedUser(t, "sched-booking-owner@example.com")
	otherID := createConfirmedUser(t, "sched-booking-other@example.com")
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
	ownerID := createConfirmedUser(t, "sched-validation-owner@example.com")
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

// PAC-61: re-inviting or re-saving co-hosts must never change an existing
// host's status. Before the fix both paths reset accepted hosts to pending,
// which silently dropped their calendars from the link's slots.

func hostStatuses(t *testing.T, linkID uuid.UUID) map[uuid.UUID]string {
	t.Helper()
	hosts, err := storage.GetLinkHosts(openTestDB(t), linkID)
	if err != nil {
		t.Fatalf("get hosts: %v", err)
	}
	out := make(map[uuid.UUID]string, len(hosts))
	for _, h := range hosts {
		out[h.UserID] = h.Status
	}
	return out
}

func TestSchedulingLinksReinviteKeepsExistingHostStatus(t *testing.T) {
	r := setupSchedulingLinkRoutes(t)
	ownerID := createConfirmedUser(t, "pac61-owner@example.com")
	acceptedID := createConfirmedUser(t, "pac61-accepted@example.com")
	declinedID := createConfirmedUser(t, "pac61-declined@example.com")
	link := createSchedulingLinkRecord(t, ownerID, "pac61-reinvite", "Reinvite")
	db := openTestDB(t)
	for id, status := range map[uuid.UUID]string{acceptedID: "accepted", declinedID: "declined"} {
		if _, err := storage.AddLinkHost(db, link.ID, id, "pending"); err != nil {
			t.Fatalf("add host: %v", err)
		}
		if _, err := storage.RespondToHostInvite(db, link.ID, id, status); err != nil {
			t.Fatalf("respond: %v", err)
		}
	}

	for _, email := range []string{"pac61-accepted@example.com", "pac61-declined@example.com"} {
		req := httptest.NewRequest(http.MethodPost, "/api/scheduling-links/"+link.ID.String()+"/hosts", bytes.NewBufferString(`{"email":"`+email+`"}`))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, withUser(req, ownerID))
		if w.Code != http.StatusOK {
			t.Fatalf("re-invite %s: status = %d, want 200 (already a host); body: %s", email, w.Code, w.Body.String())
		}
	}

	got := hostStatuses(t, link.ID)
	if got[acceptedID] != "accepted" {
		t.Errorf("accepted co-host is now %q after a re-invite, want accepted", got[acceptedID])
	}
	if got[declinedID] != "declined" {
		t.Errorf("declined co-host is now %q after a re-invite, want declined", got[declinedID])
	}
}

func TestSchedulingLinksPatchCoHostsSetsListByDifference(t *testing.T) {
	r := setupSchedulingLinkRoutes(t)
	ownerID := createConfirmedUser(t, "pac61-patch-owner@example.com")
	keptID := createConfirmedUser(t, "pac61-kept@example.com")
	droppedID := createConfirmedUser(t, "pac61-dropped@example.com")
	newID := createConfirmedUser(t, "pac61-new@example.com")
	link := createSchedulingLinkRecord(t, ownerID, "pac61-patch", "Patch")
	db := openTestDB(t)
	for _, id := range []uuid.UUID{keptID, droppedID} {
		if _, err := storage.AddLinkHost(db, link.ID, id, "pending"); err != nil {
			t.Fatalf("add host: %v", err)
		}
		if _, err := storage.RespondToHostInvite(db, link.ID, id, "accepted"); err != nil {
			t.Fatalf("accept: %v", err)
		}
	}

	body := `{"co_host_emails":["pac61-kept@example.com","pac61-new@example.com"]}`
	req := httptest.NewRequest(http.MethodPatch, "/api/scheduling-links/"+link.ID.String(), bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, withUser(req, ownerID))
	if w.Code != http.StatusOK {
		t.Fatalf("patch status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	got := hostStatuses(t, link.ID)
	if got[keptID] != "accepted" {
		t.Errorf("a listed, accepted co-host is now %q, want accepted", got[keptID])
	}
	if _, still := got[droppedID]; still {
		t.Errorf("an unlisted co-host is still a host (%q), want removed", got[droppedID])
	}
	if got[newID] != "pending" {
		t.Errorf("a newly listed co-host is %q, want pending", got[newID])
	}
	if got[ownerID] != "accepted" {
		t.Errorf("the owner is now %q, want accepted", got[ownerID])
	}
}

// PAC-30 (API-020): accept, decline and leave answer 404 and change nothing
// when the caller has no host row on the link. Before the fix they answered
// 200/204 having changed nothing.

func hostRowsSnapshot(t *testing.T, linkID uuid.UUID) string {
	t.Helper()
	hosts, err := storage.GetLinkHosts(openTestDB(t), linkID)
	if err != nil {
		t.Fatalf("get hosts: %v", err)
	}
	rows := make([]string, 0, len(hosts))
	for _, h := range hosts {
		responded := "nil"
		if h.RespondedAt != nil {
			responded = h.RespondedAt.UTC().Format(time.RFC3339Nano)
		}
		rows = append(rows, h.ID.String()+"|"+h.UserID.String()+"|"+h.Status+"|"+responded)
	}
	return strings.Join(rows, "\n")
}

func assertNotFoundJSON(t *testing.T, what string, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Code != http.StatusNotFound {
		t.Fatalf("%s: status = %d, want 404; body: %s", what, w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("%s: Content-Type = %q, want application/json", what, ct)
	}
	if body := strings.TrimSpace(w.Body.String()); body != `{"error":"not found"}` {
		t.Errorf("%s: body = %s, want {\"error\":\"not found\"}", what, body)
	}
}

func TestSchedulingLinksRespondAndLeaveWithoutHostRowIs404(t *testing.T) {
	r := setupSchedulingLinkRoutes(t)
	ownerID := createConfirmedUser(t, "pac30-owner@example.com")
	inviteeID := createConfirmedUser(t, "pac30-invitee@example.com")
	strangerID := createConfirmedUser(t, "pac30-stranger@example.com")
	link := createSchedulingLinkRecord(t, ownerID, "pac30-no-row", "No Row")
	if _, err := storage.AddLinkHost(openTestDB(t), link.ID, inviteeID, "pending"); err != nil {
		t.Fatalf("add invitee host: %v", err)
	}
	before := hostRowsSnapshot(t, link.ID)

	paths := []string{
		"/api/scheduling-links/host-invites/" + link.ID.String() + "/accept",
		"/api/scheduling-links/host-invites/" + link.ID.String() + "/decline",
		"/api/scheduling-links/" + link.ID.String() + "/accept",
		"/api/scheduling-links/" + link.ID.String() + "/decline",
		"/api/scheduling-links/" + link.ID.String() + "/leave",
	}
	for _, p := range paths {
		req := httptest.NewRequest(http.MethodPost, p, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, withUser(req, strangerID))
		assertNotFoundJSON(t, "stranger POST "+p, w)
		if after := hostRowsSnapshot(t, link.ID); after != before {
			t.Fatalf("stranger POST %s changed host rows:\nbefore:\n%s\nafter:\n%s", p, before, after)
		}
	}
}

func TestSchedulingLinksAcceptWithHostRowIdIs404(t *testing.T) {
	r := setupSchedulingLinkRoutes(t)
	ownerID := createConfirmedUser(t, "pac30-rowid-owner@example.com")
	inviteeID := createConfirmedUser(t, "pac30-rowid-invitee@example.com")
	link := createSchedulingLinkRecord(t, ownerID, "pac30-rowid", "Row Id")
	row, err := storage.AddLinkHost(openTestDB(t), link.ID, inviteeID, "pending")
	if err != nil {
		t.Fatalf("add invitee host: %v", err)
	}
	before := hostRowsSnapshot(t, link.ID)

	for _, p := range []string{
		"/api/scheduling-links/host-invites/" + row.ID.String() + "/accept",
		"/api/scheduling-links/" + row.ID.String() + "/accept",
	} {
		req := httptest.NewRequest(http.MethodPost, p, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, withUser(req, inviteeID))
		assertNotFoundJSON(t, "accept with host-row id "+p, w)
	}
	if after := hostRowsSnapshot(t, link.ID); after != before {
		t.Fatalf("accept with a host-row id changed host rows:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if got := hostStatuses(t, link.ID)[inviteeID]; got != "pending" {
		t.Errorf("invitee status = %q, want pending", got)
	}
}

func TestSchedulingLinksLeaveTwiceIs404(t *testing.T) {
	r := setupSchedulingLinkRoutes(t)
	ownerID := createConfirmedUser(t, "pac30-leave-owner@example.com")
	hostID := createConfirmedUser(t, "pac30-leave-host@example.com")
	link := createSchedulingLinkRecord(t, ownerID, "pac30-leave", "Leave")
	if _, err := storage.AddLinkHost(openTestDB(t), link.ID, hostID, "accepted"); err != nil {
		t.Fatalf("add host: %v", err)
	}

	path := "/api/scheduling-links/" + link.ID.String() + "/leave"
	req := httptest.NewRequest(http.MethodPost, path, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, withUser(req, hostID))
	if w.Code != http.StatusNoContent {
		t.Fatalf("first leave: status = %d, want 204; body: %s", w.Code, w.Body.String())
	}
	before := hostRowsSnapshot(t, link.ID)

	req = httptest.NewRequest(http.MethodPost, path, nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, withUser(req, hostID))
	assertNotFoundJSON(t, "second leave", w)
	if after := hostRowsSnapshot(t, link.ID); after != before {
		t.Fatalf("second leave changed host rows:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestSchedulingLinksRespondOnUnknownLinkIs404(t *testing.T) {
	r := setupSchedulingLinkRoutes(t)
	userID := createConfirmedUser(t, "pac30-unknown@example.com")
	missing := uuid.New().String()
	for _, p := range []string{
		"/api/scheduling-links/host-invites/" + missing + "/accept",
		"/api/scheduling-links/host-invites/" + missing + "/decline",
		"/api/scheduling-links/" + missing + "/leave",
	} {
		req := httptest.NewRequest(http.MethodPost, p, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, withUser(req, userID))
		assertNotFoundJSON(t, "unknown link POST "+p, w)
	}
}

func TestSchedulingLinksInvitedHostAcceptsIs200(t *testing.T) {
	for slug, prefix := range map[string]string{
		"pac30-accept-hostinvites": "/api/scheduling-links/host-invites/",
		"pac30-accept-bylink":      "/api/scheduling-links/",
	} {
		t.Run(slug, func(t *testing.T) {
			r := setupSchedulingLinkRoutes(t)
			ownerID := createConfirmedUser(t, slug+"-owner@example.com")
			inviteeEmail := slug + "-invitee@example.com"
			inviteeID := createConfirmedUser(t, inviteeEmail)
			link := createSchedulingLinkRecord(t, ownerID, slug, "Accept")

			req := httptest.NewRequest(http.MethodPost, "/api/scheduling-links/"+link.ID.String()+"/hosts", bytes.NewBufferString(`{"email":"`+inviteeEmail+`"}`))
			w := httptest.NewRecorder()
			r.ServeHTTP(w, withUser(req, ownerID))
			if w.Code != http.StatusCreated {
				t.Fatalf("invite: status = %d, want 201; body: %s", w.Code, w.Body.String())
			}

			req = httptest.NewRequest(http.MethodPost, prefix+link.ID.String()+"/accept", nil)
			w = httptest.NewRecorder()
			r.ServeHTTP(w, withUser(req, inviteeID))
			if w.Code != http.StatusOK {
				t.Fatalf("accept: status = %d, want 200; body: %s", w.Code, w.Body.String())
			}
			if body := strings.TrimSpace(w.Body.String()); body != `{"status":"accepted"}` {
				t.Errorf("accept: body = %s, want {\"status\":\"accepted\"}", body)
			}
			hosts, err := storage.GetLinkHosts(openTestDB(t), link.ID)
			if err != nil {
				t.Fatalf("get hosts: %v", err)
			}
			found := false
			for _, h := range hosts {
				if h.UserID != inviteeID {
					continue
				}
				found = true
				if h.Status != "accepted" {
					t.Errorf("invitee status = %q, want accepted", h.Status)
				}
				if h.RespondedAt == nil {
					t.Errorf("invitee responded_at is nil, want set")
				}
			}
			if !found {
				t.Fatalf("invitee host row missing after accept")
			}
		})
	}
}

// --- PAC-25: GET /api/scheduling-links/{id} is readable only by the owner and
// accepted co-hosts (spec AC-1..AC-5, D-1, D-2; contract C-2). ---

// pac25LinkFixture is link L owned by A with co-host rows B accepted, C pending
// and D declined, plus E who has no host row at all.
type pac25LinkFixture struct {
	r                 *chi.Mux
	link              *storage.SchedulingLink
	owner, accepted   uuid.UUID
	pending, declined uuid.UUID
	stranger          uuid.UUID
}

func newPAC25LinkFixture(t *testing.T, prefix string) pac25LinkFixture {
	t.Helper()
	db := openTestDB(t)
	f := pac25LinkFixture{r: setupSchedulingLinkRoutes(t)}
	f.owner = createConfirmedUser(t, prefix+"-owner@example.com")
	f.accepted = createConfirmedUser(t, prefix+"-accepted@example.com")
	f.pending = createConfirmedUser(t, prefix+"-pending@example.com")
	f.declined = createConfirmedUser(t, prefix+"-declined@example.com")
	f.stranger = createConfirmedUser(t, prefix+"-stranger@example.com")
	for _, u := range []uuid.UUID{f.owner, f.accepted, f.pending, f.declined} {
		if _, err := db.Exec(`UPDATE users SET avatar_url = $1 WHERE id = $2`, "https://avatars.example.com/"+u.String(), u); err != nil {
			t.Fatalf("set avatar: %v", err)
		}
	}
	f.link = createSchedulingLinkRecord(t, f.owner, prefix+"-link", "PAC-25 "+prefix)
	for u, status := range map[uuid.UUID]string{f.accepted: "accepted", f.pending: "pending", f.declined: "declined"} {
		if _, err := storage.AddLinkHost(db, f.link.ID, u, status); err != nil {
			t.Fatalf("add %s host: %v", status, err)
		}
	}
	return f
}

func (f pac25LinkFixture) get(t *testing.T, caller uuid.UUID) *httptest.ResponseRecorder {
	t.Helper()
	return f.getID(t, caller, f.link.ID.String())
}

func (f pac25LinkFixture) getID(t *testing.T, caller uuid.UUID, id string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/scheduling-links/"+id, nil)
	w := httptest.NewRecorder()
	f.r.ServeHTTP(w, withUser(req, caller))
	return w
}

// listEntry is the caller's entry for L in GET /api/scheduling-links, as raw
// JSON: the list is the read that already shows L to its owner and accepted
// co-hosts, so the detail must answer them with the same record.
func (f pac25LinkFixture) listEntry(t *testing.T, caller uuid.UUID) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/scheduling-links", nil)
	w := httptest.NewRecorder()
	f.r.ServeHTTP(w, withUser(req, caller))
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d; body: %s", w.Code, w.Body.String())
	}
	var got map[string][]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	for _, group := range [][]json.RawMessage{got["owned"], got["shared"]} {
		for _, raw := range group {
			var probe struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(raw, &probe)
			if probe.ID == f.link.ID.String() {
				return string(raw)
			}
		}
	}
	t.Fatalf("link %s is not in the caller's list: %s", f.link.ID, w.Body.String())
	return ""
}

// assertRefusedLikeUnknown is AC-1's "the same status and the same body as for
// a well-formed identifier that matches no link", plus "no host, no email, no
// title and no slug".
func (f pac25LinkFixture) assertRefusedLikeUnknown(t *testing.T, what string, caller uuid.UUID) {
	t.Helper()
	unknown := f.getID(t, caller, uuid.New().String())
	assertNotFoundJSON(t, what+" (unknown id baseline)", unknown)
	w := f.get(t, caller)
	assertNotFoundJSON(t, what, w)
	if w.Body.String() != unknown.Body.String() {
		t.Fatalf("%s: body %q differs from the unknown-id body %q", what, w.Body.String(), unknown.Body.String())
	}
	body := w.Body.String()
	for _, leak := range []string{"hosts", "@example.com", f.link.Title, f.link.Slug, f.link.ID.String()} {
		if strings.Contains(body, leak) {
			t.Fatalf("%s: body %q carries %q", what, body, leak)
		}
	}
}

// AC-1: a caller with no host row on L gets the unknown-id answer.
func TestPAC25GetLinkStrangerIsNotFound(t *testing.T) {
	f := newPAC25LinkFixture(t, "pac25-ac1")
	f.assertRefusedLikeUnknown(t, "stranger", f.stranger)
}

// AC-2: a pending invitee gets the unknown-id answer.
func TestPAC25GetLinkPendingInviteeIsNotFound(t *testing.T) {
	f := newPAC25LinkFixture(t, "pac25-ac2")
	f.assertRefusedLikeUnknown(t, "pending invitee", f.pending)
}

// AC-3: an invitee who declined gets the unknown-id answer.
func TestPAC25GetLinkDeclinedInviteeIsNotFound(t *testing.T) {
	f := newPAC25LinkFixture(t, "pac25-ac3")
	f.assertRefusedLikeUnknown(t, "declined invitee", f.declined)
}

// Contract 404 description: a co-host who left is refused like a stranger.
func TestPAC25GetLinkCoHostWhoLeftIsNotFound(t *testing.T) {
	f := newPAC25LinkFixture(t, "pac25-left")
	req := httptest.NewRequest(http.MethodPost, "/api/scheduling-links/"+f.link.ID.String()+"/leave", nil)
	w := httptest.NewRecorder()
	f.r.ServeHTTP(w, withUser(req, f.accepted))
	if w.Code != http.StatusNoContent {
		t.Fatalf("leave status = %d; body: %s", w.Code, w.Body.String())
	}
	f.assertRefusedLikeUnknown(t, "co-host who left", f.accepted)
}

// AC-4 (pin): an accepted co-host reads the whole record, roster included.
func TestPAC25GetLinkAcceptedCoHostReadsFullRoster(t *testing.T) {
	f := newPAC25LinkFixture(t, "pac25-ac4")
	w := f.get(t, f.accepted)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var got schedulingLinkDTO
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.IsOwner || got.MyStatus != "accepted" {
		t.Fatalf("is_owner=%v my_status=%q, want false/accepted", got.IsOwner, got.MyStatus)
	}
	if got.ID != f.link.ID.String() || got.OwnerID != f.owner.String() || got.Title != f.link.Title || got.Slug != f.link.Slug {
		t.Fatalf("link fields = %+v, want the stored link %+v", got, f.link)
	}
	want := map[string]string{
		f.owner.String(): "accepted", f.accepted.String(): "accepted",
		f.pending.String(): "pending", f.declined.String(): "declined",
	}
	if len(got.Hosts) != len(want) {
		t.Fatalf("hosts = %+v, want %d rows", got.Hosts, len(want))
	}
	for _, h := range got.Hosts {
		if want[h.UserID] != h.Status {
			t.Fatalf("host %s status %q, want %q", h.UserID, h.Status, want[h.UserID])
		}
		if !strings.HasSuffix(h.Email, "@example.com") || h.Name == "" || h.AvatarURL != "https://avatars.example.com/"+h.UserID {
			t.Fatalf("host %+v lacks email, name or avatar", h)
		}
	}
	if list := f.listEntry(t, f.accepted); strings.TrimSpace(w.Body.String()) != list {
		t.Fatalf("detail differs from the co-host's list entry:\ndetail: %s\nlist:   %s", w.Body.String(), list)
	}
}

// AC-5 (pin): the owner reads the same record as before PAC-25.
func TestPAC25GetLinkOwnerReadsFullRecord(t *testing.T) {
	f := newPAC25LinkFixture(t, "pac25-ac5")
	w := f.get(t, f.owner)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var got schedulingLinkDTO
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.IsOwner || got.MyStatus != "accepted" || len(got.Hosts) != 4 {
		t.Fatalf("got is_owner=%v my_status=%q hosts=%d, want true/accepted/4", got.IsOwner, got.MyStatus, len(got.Hosts))
	}
	if list := f.listEntry(t, f.owner); strings.TrimSpace(w.Body.String()) != list {
		t.Fatalf("detail differs from the owner's list entry:\ndetail: %s\nlist:   %s", w.Body.String(), list)
	}
}

// hideTable makes every read of one table fail for the duration of the test
// while the rest of the database works (the pattern of hideOAuthTokens).
func hideTable(t *testing.T, table string) {
	t.Helper()
	db := openTestDB(t)
	if _, err := db.Exec(`ALTER TABLE ` + table + ` RENAME TO ` + table + `_pac25_hidden`); err != nil {
		t.Fatalf("hide %s: %v", table, err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec(`ALTER TABLE ` + table + `_pac25_hidden RENAME TO ` + table); err != nil {
			t.Fatalf("restore %s: %v", table, err)
		}
	})
}

func assertInternalErrorNoLink(t *testing.T, what string, f pac25LinkFixture, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("%s: status = %d, want 500; body: %s", what, w.Code, w.Body.String())
	}
	exactJSONError(t, w, "internal error")
	for _, leak := range []string{f.link.Title, f.link.Slug, "@example.com"} {
		if strings.Contains(w.Body.String(), leak) {
			t.Fatalf("%s: body %q carries %q", what, w.Body.String(), leak)
		}
	}
}

// C-2a: an accepted co-host whose host-status lookup fails gets 500, not 404 or 200.
func TestPAC25GetLinkHostStatusFailureForCoHostIs500(t *testing.T) {
	f := newPAC25LinkFixture(t, "pac25-c2a")
	_, _ = captureServerErrors(t)
	hideTable(t, "scheduling_link_hosts")
	assertInternalErrorNoLink(t, "co-host, host lookup down", f, f.get(t, f.accepted))
}

// C-2b: a caller with no host row whose host-status lookup fails gets 500 and
// no part of the link.
func TestPAC25GetLinkHostStatusFailureForStrangerIs500(t *testing.T) {
	f := newPAC25LinkFixture(t, "pac25-c2b")
	_, _ = captureServerErrors(t)
	hideTable(t, "scheduling_link_hosts")
	assertInternalErrorNoLink(t, "stranger, host lookup down", f, f.get(t, f.stranger))
}

// C-2c: a failed roster read is 500, never 200 with hosts emptied (host rows
// unreadable, owner caller) or an email dropped (host users unreadable).
func TestPAC25GetLinkRosterFailureIs500(t *testing.T) {
	t.Run("host rows unreadable", func(t *testing.T) {
		f := newPAC25LinkFixture(t, "pac25-c2c-rows")
		_, _ = captureServerErrors(t)
		hideTable(t, "scheduling_link_hosts")
		assertInternalErrorNoLink(t, "owner, roster rows down", f, f.get(t, f.owner))
	})
	t.Run("host users unreadable", func(t *testing.T) {
		f := newPAC25LinkFixture(t, "pac25-c2c-users")
		_, _ = captureServerErrors(t)
		hideTable(t, "users")
		assertInternalErrorNoLink(t, "co-host, roster users down", f, f.get(t, f.accepted))
	})
}
