package api

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/storage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

type schedulingLinkHandlers struct {
	bookEng *engine.BookingEngine
	db      *sql.DB
}

func newSchedulingLinkHandlers(db *sql.DB, oauthConfig *oauth2.Config) *schedulingLinkHandlers {
	return &schedulingLinkHandlers{
		bookEng: &engine.BookingEngine{DB: db, OAuthConfig: oauthConfig},
		db:      db,
	}
}

// POST /api/scheduling-links
func (h *schedulingLinkHandlers) createLink(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())

	var body struct {
		Title           string `json:"title"`
		DurationOptions []int  `json:"duration_options"`
		DaysOfWeek      []int  `json:"days_of_week"`
		WindowStart     string `json:"window_start_time"`
		WindowEnd       string `json:"window_end_time"`
		BufferBefore    int    `json:"buffer_before"`
		BufferAfter     int    `json:"buffer_after"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if body.Title == "" {
		writeError(w, "title is required", http.StatusBadRequest)
		return
	}

	u, err := storage.GetUserByID(h.db, userID)
	if err != nil || u == nil {
		writeError(w, "user not found", http.StatusInternalServerError)
		return
	}

	dur := 30
	if len(body.DurationOptions) > 0 {
		dur = body.DurationOptions[0]
	}
	slug, err := h.bookEng.GenerateSlug(u.Name, dur)
	if err != nil {
		writeError(w, "slug generation failed", http.StatusInternalServerError)
		return
	}

	if len(body.DurationOptions) == 0 {
		body.DurationOptions = []int{30}
	}
	if len(body.DaysOfWeek) == 0 {
		body.DaysOfWeek = []int{1, 2, 3, 4, 5}
	}
	if body.WindowStart == "" {
		body.WindowStart = "09:00"
	}
	if body.WindowEnd == "" {
		body.WindowEnd = "17:00"
	}

	link, err := storage.CreateSchedulingLink(h.db, &storage.SchedulingLink{
		OwnerUserID:     userID,
		Slug:            slug,
		Title:           body.Title,
		DurationOptions: body.DurationOptions,
		DaysOfWeek:      body.DaysOfWeek,
		WindowStart:     body.WindowStart,
		WindowEnd:       body.WindowEnd,
		BufferBefore:    body.BufferBefore,
		BufferAfter:     body.BufferAfter,
	})
	if err != nil {
		writeError(w, "create failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Auto-accept the owner as a host.
	_, _ = storage.AddLinkHost(h.db, link.ID, userID, "accepted")

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(link)
}

// GET /api/scheduling-links
func (h *schedulingLinkHandlers) listLinks(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	links, err := storage.ListSchedulingLinksByUser(h.db, userID)
	if err != nil {
		writeError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if links == nil {
		links = []*storage.SchedulingLink{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(links)
}

// GET /api/scheduling-links/{id}
func (h *schedulingLinkHandlers) getLink(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, "invalid id", http.StatusBadRequest)
		return
	}
	link, err := storage.GetSchedulingLinkByID(h.db, id)
	if err != nil {
		writeError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if link == nil {
		writeError(w, "not found", http.StatusNotFound)
		return
	}

	hosts, _ := storage.GetLinkHosts(h.db, id)
	link.Hosts = hosts

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(link)
}

// PATCH /api/scheduling-links/{id}
func (h *schedulingLinkHandlers) updateLink(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, "invalid id", http.StatusBadRequest)
		return
	}

	existing, err := storage.GetSchedulingLinkByID(h.db, id)
	if err != nil {
		writeError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if existing == nil {
		writeError(w, "not found", http.StatusNotFound)
		return
	}
	if existing.OwnerUserID != userID {
		writeError(w, "forbidden", http.StatusForbidden)
		return
	}

	var body struct {
		Title           *string `json:"title"`
		DurationOptions []int   `json:"duration_options"`
		DaysOfWeek      []int   `json:"days_of_week"`
		WindowStart     *string `json:"window_start_time"`
		WindowEnd       *string `json:"window_end_time"`
		BufferBefore    *int    `json:"buffer_before"`
		BufferAfter     *int    `json:"buffer_after"`
		Active          *bool   `json:"active"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	updated := *existing
	if body.Title != nil {
		updated.Title = *body.Title
	}
	if body.DurationOptions != nil {
		updated.DurationOptions = body.DurationOptions
	}
	if body.DaysOfWeek != nil {
		updated.DaysOfWeek = body.DaysOfWeek
	}
	if body.WindowStart != nil {
		updated.WindowStart = *body.WindowStart
	}
	if body.WindowEnd != nil {
		updated.WindowEnd = *body.WindowEnd
	}
	if body.BufferBefore != nil {
		updated.BufferBefore = *body.BufferBefore
	}
	if body.BufferAfter != nil {
		updated.BufferAfter = *body.BufferAfter
	}
	if body.Active != nil {
		updated.Active = *body.Active
	}

	result, err := storage.UpdateSchedulingLink(h.db, id, &updated)
	if err != nil {
		writeError(w, "update failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

// DELETE /api/scheduling-links/{id}
func (h *schedulingLinkHandlers) deleteLink(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, "invalid id", http.StatusBadRequest)
		return
	}

	existing, err := storage.GetSchedulingLinkByID(h.db, id)
	if err != nil {
		writeError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if existing == nil {
		writeError(w, "not found", http.StatusNotFound)
		return
	}
	if existing.OwnerUserID != userID {
		writeError(w, "forbidden", http.StatusForbidden)
		return
	}

	if err := storage.DeleteSchedulingLink(h.db, id); err != nil {
		writeError(w, "delete failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GET /api/scheduling-links/{id}/bookings
func (h *schedulingLinkHandlers) listBookings(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, "invalid id", http.StatusBadRequest)
		return
	}

	link, err := storage.GetSchedulingLinkByID(h.db, id)
	if err != nil {
		writeError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if link == nil {
		writeError(w, "not found", http.StatusNotFound)
		return
	}
	if link.OwnerUserID != userID {
		writeError(w, "forbidden", http.StatusForbidden)
		return
	}

	bookings, err := storage.GetBookingsByLink(h.db, id)
	if err != nil {
		writeError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if bookings == nil {
		bookings = []*storage.Booking{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(bookings)
}

// POST /api/scheduling-links/{id}/hosts
func (h *schedulingLinkHandlers) inviteHost(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, "invalid id", http.StatusBadRequest)
		return
	}

	link, err := storage.GetSchedulingLinkByID(h.db, id)
	if err != nil {
		writeError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if link == nil {
		writeError(w, "not found", http.StatusNotFound)
		return
	}
	if link.OwnerUserID != userID {
		writeError(w, "forbidden", http.StatusForbidden)
		return
	}

	var body struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Email == "" {
		writeError(w, "email is required", http.StatusBadRequest)
		return
	}

	invitee, err := storage.GetUserByEmail(h.db, body.Email)
	if err != nil || invitee == nil {
		writeError(w, "user not found", http.StatusNotFound)
		return
	}

	host, err := storage.AddLinkHost(h.db, id, invitee.ID, "pending")
	if err != nil {
		writeError(w, "invite failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(host)
}

// GET /api/scheduling-links/host-invites
func (h *schedulingLinkHandlers) listHostInvites(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	invites, err := storage.GetPendingInvitesForUser(h.db, userID)
	if err != nil {
		writeError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if invites == nil {
		invites = []*storage.LinkHost{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(invites)
}

// POST /api/scheduling-links/host-invites/{id}/accept
func (h *schedulingLinkHandlers) acceptInvite(w http.ResponseWriter, r *http.Request) {
	h.respondToInvite(w, r, "accepted")
}

// POST /api/scheduling-links/host-invites/{id}/decline
func (h *schedulingLinkHandlers) declineInvite(w http.ResponseWriter, r *http.Request) {
	h.respondToInvite(w, r, "declined")
}

func (h *schedulingLinkHandlers) respondToInvite(w http.ResponseWriter, r *http.Request, status string) {
	userID := userIDFromCtx(r.Context())
	linkID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, "invalid id", http.StatusBadRequest)
		return
	}
	if err := storage.RespondToHostInvite(h.db, linkID, userID, status); err != nil {
		writeError(w, "update failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": status})
}
