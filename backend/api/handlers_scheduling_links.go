package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

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

// These DTOs deliberately use the frontend names. Storage keeps the original
// database-oriented names so the MCP and internal engine callers remain stable.
type schedulingLinkDTO struct {
	ID               string              `json:"id"`
	OwnerID          string              `json:"owner_id"`
	Title            string              `json:"title"`
	Slug             string              `json:"slug"`
	Durations        []int               `json:"durations"`
	Days             []string            `json:"days"`
	WindowStart      string              `json:"window_start"`
	WindowEnd        string              `json:"window_end"`
	BufferBefore     int                 `json:"buffer_before"`
	BufferAfter      int                 `json:"buffer_after"`
	MinNoticeMinutes int                 `json:"min_notice_minutes"`
	UsageType        string              `json:"usage_type"`
	MaxUses          *int                `json:"max_uses,omitempty"`
	UsesCount        int                 `json:"uses_count"`
	Active           bool                `json:"active"`
	Hosts            []schedulingHostDTO `json:"hosts"`
	CreatedAt        string              `json:"created_at"`
	IsOwner          bool                `json:"is_owner"`
	MyStatus         string              `json:"my_status,omitempty"`
}

type schedulingHostDTO struct {
	UserID    string `json:"user_id,omitempty"`
	Email     string `json:"email"`
	Name      string `json:"name,omitempty"`
	AvatarURL string `json:"avatar_url,omitempty"`
	IsOwner   bool   `json:"is_owner"`
	Status    string `json:"status"`
}

type hostInviteDTO struct {
	LinkID     string `json:"link_id"`
	LinkTitle  string `json:"link_title"`
	OwnerName  string `json:"owner_name"`
	OwnerEmail string `json:"owner_email"`
	InvitedAt  string `json:"invited_at"`
}

type schedulingLinkInput struct {
	Title            string   `json:"title"`
	Slug             string   `json:"slug"`
	DurationOptions  []int    `json:"duration_options"`
	Durations        []int    `json:"durations"`
	DaysOfWeek       []int    `json:"days_of_week"`
	Days             []string `json:"days"`
	WindowStart      string   `json:"window_start_time"`
	WindowStartAlias string   `json:"window_start"`
	WindowEnd        string   `json:"window_end_time"`
	WindowEndAlias   string   `json:"window_end"`
	BufferBefore     int      `json:"buffer_before"`
	BufferAfter      int      `json:"buffer_after"`
	MinNoticeMinutes int      `json:"min_notice_minutes"`
	UsageType        string   `json:"usage_type"`
	MaxUses          *int     `json:"max_uses"`
	CoHostEmails     []string `json:"co_host_emails"`
}

type schedulingLinkPatch struct {
	Title            *string  `json:"title"`
	Slug             *string  `json:"slug"`
	DurationOptions  []int    `json:"duration_options"`
	Durations        []int    `json:"durations"`
	DaysOfWeek       []int    `json:"days_of_week"`
	Days             []string `json:"days"`
	WindowStart      *string  `json:"window_start_time"`
	WindowStartAlias *string  `json:"window_start"`
	WindowEnd        *string  `json:"window_end_time"`
	WindowEndAlias   *string  `json:"window_end"`
	BufferBefore     *int     `json:"buffer_before"`
	BufferAfter      *int     `json:"buffer_after"`
	MinNoticeMinutes *int     `json:"min_notice_minutes"`
	UsageType        *string  `json:"usage_type"`
	MaxUses          *int     `json:"max_uses"`
	Active           *bool    `json:"active"`
	CoHostEmails     []string `json:"co_host_emails"`
}

func dayNames(days []int) []string {
	out := make([]string, 0, len(days))
	for _, day := range days {
		if name, ok := map[int]string{0: "sun", 1: "mon", 2: "tue", 3: "wed", 4: "thu", 5: "fri", 6: "sat"}[day]; ok {
			out = append(out, name)
		}
	}
	return out
}

func dayNumbers(days []string) []int {
	out := make([]int, 0, len(days))
	for _, raw := range days {
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case "sun", "sunday", "0":
			out = append(out, 0)
		case "mon", "monday", "1":
			out = append(out, 1)
		case "tue", "tuesday", "2":
			out = append(out, 2)
		case "wed", "wednesday", "3":
			out = append(out, 3)
		case "thu", "thursday", "4":
			out = append(out, 4)
		case "fri", "friday", "5":
			out = append(out, 5)
		case "sat", "saturday", "6":
			out = append(out, 6)
		}
	}
	return out
}

func normalizeSlug(raw string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(raw)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
		} else if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func (in schedulingLinkInput) normalized() (storage.SchedulingLink, error) {
	var durations []int
	switch {
	case in.DurationOptions != nil:
		durations = in.DurationOptions
	case in.Durations != nil:
		durations = in.Durations
	default:
		durations = []int{30}
	}
	var days []int
	switch {
	case in.DaysOfWeek != nil:
		days = in.DaysOfWeek
	case in.Days != nil:
		days = dayNumbers(in.Days)
	default:
		days = []int{1, 2, 3, 4, 5}
	}
	start := in.WindowStart
	if start == "" {
		start = in.WindowStartAlias
	}
	if start == "" {
		start = "09:00"
	}
	end := in.WindowEnd
	if end == "" {
		end = in.WindowEndAlias
	}
	if end == "" {
		end = "17:00"
	}
	link := storage.SchedulingLink{
		Slug: normalizeSlug(in.Slug), Title: strings.TrimSpace(in.Title),
		DurationOptions: durations, DaysOfWeek: days, WindowStart: start, WindowEnd: end,
		BufferBefore: in.BufferBefore, BufferAfter: in.BufferAfter,
		MinNoticeMinutes: in.MinNoticeMinutes, UsageType: in.UsageType, MaxUses: in.MaxUses,
	}
	if err := validateSchedulingLink(&link); err != nil {
		return storage.SchedulingLink{}, err
	}
	return link, nil
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func parseClock(value string) (time.Time, error) {
	trimmed := strings.TrimSpace(value)
	for _, layout := range []string{"15:04", "15:04:05"} {
		if parsed, err := time.Parse(layout, trimmed); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, &httpError{Code: http.StatusUnprocessableEntity, Msg: "window times must use HH:MM"}
}

func validateSchedulingLink(link *storage.SchedulingLink) *httpError {
	if strings.TrimSpace(link.Title) == "" {
		return &httpError{Code: http.StatusUnprocessableEntity, Msg: "title is required"}
	}
	if len(link.DurationOptions) == 0 {
		return &httpError{Code: http.StatusUnprocessableEntity, Msg: "at least one duration is required"}
	}
	for _, duration := range link.DurationOptions {
		if duration <= 0 {
			return &httpError{Code: http.StatusUnprocessableEntity, Msg: "durations must be positive integers"}
		}
	}
	if len(link.DaysOfWeek) == 0 {
		return &httpError{Code: http.StatusUnprocessableEntity, Msg: "at least one day is required"}
	}
	seenDays := make(map[int]struct{}, len(link.DaysOfWeek))
	for _, day := range link.DaysOfWeek {
		if day < 0 || day > 6 {
			return &httpError{Code: http.StatusUnprocessableEntity, Msg: "days_of_week must contain values between 0 and 6"}
		}
		seenDays[day] = struct{}{}
	}
	if len(seenDays) == 0 {
		return &httpError{Code: http.StatusUnprocessableEntity, Msg: "at least one day is required"}
	}
	start, err := parseClock(link.WindowStart)
	if err != nil {
		return err.(*httpError)
	}
	end, err := parseClock(link.WindowEnd)
	if err != nil {
		return err.(*httpError)
	}
	if !start.Before(end) {
		return &httpError{Code: http.StatusUnprocessableEntity, Msg: "window_start must be before window_end"}
	}
	if link.BufferBefore < 0 || link.BufferAfter < 0 {
		return &httpError{Code: http.StatusUnprocessableEntity, Msg: "buffers must be zero or positive"}
	}
	if link.MinNoticeMinutes < 0 {
		return &httpError{Code: http.StatusUnprocessableEntity, Msg: "min_notice_minutes must be zero or positive"}
	}
	usage := strings.ToLower(strings.TrimSpace(link.UsageType))
	if usage == "" {
		usage = "reusable"
	}
	if usage != "reusable" && usage != "recurring" && usage != "single_use" {
		return &httpError{Code: http.StatusUnprocessableEntity, Msg: "usage_type must be reusable, recurring, or single_use"}
	}
	link.UsageType = usage
	if usage == "recurring" {
		if link.MaxUses == nil || *link.MaxUses <= 0 {
			return &httpError{Code: http.StatusUnprocessableEntity, Msg: "max_uses must be set to a positive integer for recurring links"}
		}
	} else {
		link.MaxUses = nil
	}
	link.WindowStart = start.Format("15:04")
	link.WindowEnd = end.Format("15:04")
	return nil
}

func parseOptionalInt(value *string) (*int, error) {
	if value == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil, nil
	}
	parsed, err := strconv.Atoi(trimmed)
	if err != nil {
		return nil, &httpError{Code: http.StatusUnprocessableEntity, Msg: "max_uses must be a positive integer"}
	}
	return &parsed, nil
}

func toSchedulingLinkDTO(db *sql.DB, link *storage.SchedulingLink, viewer uuid.UUID, includeHosts bool) schedulingLinkDTO {
	dto := schedulingLinkDTO{
		ID: link.ID.String(), OwnerID: link.OwnerUserID.String(), Title: link.Title, Slug: link.Slug,
		Durations: link.DurationOptions, Days: dayNames(link.DaysOfWeek), WindowStart: link.WindowStart,
		WindowEnd: link.WindowEnd, BufferBefore: link.BufferBefore, BufferAfter: link.BufferAfter,
		MinNoticeMinutes: link.MinNoticeMinutes, UsageType: link.UsageType, MaxUses: link.MaxUses,
		UsesCount: link.UsesCount, Active: link.Active, CreatedAt: link.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		IsOwner: link.OwnerUserID == viewer,
	}
	if dto.UsageType == "" {
		dto.UsageType = "reusable"
	}
	if includeHosts {
		hosts, _ := storage.GetLinkHosts(db, link.ID)
		dto.Hosts = make([]schedulingHostDTO, 0, len(hosts))
		for _, host := range hosts {
			h := schedulingHostDTO{UserID: host.UserID.String(), IsOwner: host.UserID == link.OwnerUserID, Status: host.Status}
			if user, err := storage.GetUserByID(db, host.UserID); err == nil && user != nil {
				h.Email, h.Name, h.AvatarURL = user.Email, user.Name, user.AvatarURL
			}
			dto.Hosts = append(dto.Hosts, h)
			if host.UserID == viewer {
				dto.MyStatus = host.Status
			}
		}
	}
	if dto.Hosts == nil {
		dto.Hosts = []schedulingHostDTO{}
	}
	return dto
}

func (h *schedulingLinkHandlers) writeLink(w http.ResponseWriter, link *storage.SchedulingLink, viewer uuid.UUID) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(toSchedulingLinkDTO(h.db, link, viewer, true))
}

// POST /api/scheduling-links
func (h *schedulingLinkHandlers) createLink(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	var input schedulingLinkInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	link, err := input.normalized()
	if err != nil {
		e := err.(*httpError)
		writeError(w, e.Msg, e.Code)
		return
	}
	owner, err := storage.GetUserByID(h.db, userID)
	if err != nil || owner == nil {
		writeError(w, "user not found", http.StatusInternalServerError)
		return
	}
	if link.Slug == "" {
		link.Slug, err = h.bookEng.GenerateSlug(owner.Name, link.DurationOptions[0])
		if err != nil {
			writeError(w, "slug generation failed", http.StatusInternalServerError)
			return
		}
	} else if exists, e := storage.SlugExists(h.db, link.Slug); e != nil {
		writeError(w, "slug check failed", http.StatusInternalServerError)
		return
	} else if exists {
		writeError(w, "slug already exists", http.StatusConflict)
		return
	}
	link.OwnerUserID = userID
	created, err := storage.CreateSchedulingLink(h.db, &link)
	if err != nil {
		writeError(w, "create failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err = storage.AddLinkHost(h.db, created.ID, userID, "accepted"); err != nil {
		writeError(w, "owner host failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	for _, email := range input.CoHostEmails {
		invitee, e := storage.GetUserByEmail(h.db, strings.ToLower(strings.TrimSpace(email)))
		if e != nil || invitee == nil {
			continue
		}
		_, _ = storage.AddLinkHost(h.db, created.ID, invitee.ID, "pending")
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	h.writeLink(w, created, userID)
}

// GET /api/scheduling-links
func (h *schedulingLinkHandlers) listLinks(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	links, err := storage.ListSchedulingLinksByUser(h.db, userID)
	if err != nil {
		writeError(w, "internal error", http.StatusInternalServerError)
		return
	}
	owned, shared := []schedulingLinkDTO{}, []schedulingLinkDTO{}
	for _, link := range links {
		dto := toSchedulingLinkDTO(h.db, link, userID, true)
		if dto.IsOwner {
			owned = append(owned, dto)
		} else {
			shared = append(shared, dto)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"owned": owned, "shared": shared})
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
	h.writeLink(w, link, userIDFromCtx(r.Context()))
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
	var patch schedulingLinkPatch
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		writeError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	updated := *existing
	slugPatched := false
	if patch.Title != nil {
		updated.Title = strings.TrimSpace(*patch.Title)
	}
	if patch.Slug != nil {
		updated.Slug = normalizeSlug(*patch.Slug)
		slugPatched = true
	}
	if patch.DurationOptions != nil {
		updated.DurationOptions = patch.DurationOptions
	}
	if patch.Durations != nil {
		updated.DurationOptions = patch.Durations
	}
	if patch.DaysOfWeek != nil {
		updated.DaysOfWeek = patch.DaysOfWeek
	}
	if patch.Days != nil {
		updated.DaysOfWeek = dayNumbers(patch.Days)
	}
	if patch.WindowStart != nil {
		updated.WindowStart = *patch.WindowStart
	} else if patch.WindowStartAlias != nil {
		updated.WindowStart = *patch.WindowStartAlias
	}
	if patch.WindowEnd != nil {
		updated.WindowEnd = *patch.WindowEnd
	} else if patch.WindowEndAlias != nil {
		updated.WindowEnd = *patch.WindowEndAlias
	}
	if patch.BufferBefore != nil {
		updated.BufferBefore = *patch.BufferBefore
	}
	if patch.BufferAfter != nil {
		updated.BufferAfter = *patch.BufferAfter
	}
	if patch.MinNoticeMinutes != nil {
		updated.MinNoticeMinutes = *patch.MinNoticeMinutes
	}
	if patch.UsageType != nil {
		updated.UsageType = strings.ToLower(strings.TrimSpace(*patch.UsageType))
	}
	if patch.MaxUses != nil {
		updated.MaxUses = patch.MaxUses
	}
	if patch.Active != nil {
		updated.Active = *patch.Active
	}
	if slugPatched && updated.Slug == "" {
		writeError(w, "slug cannot be empty", http.StatusUnprocessableEntity)
		return
	}
	if validationErr := validateSchedulingLink(&updated); validationErr != nil {
		writeError(w, validationErr.Msg, validationErr.Code)
		return
	}
	if updated.Slug != existing.Slug {
		if exists, e := storage.SlugExists(h.db, updated.Slug); e != nil {
			writeError(w, "slug check failed", http.StatusInternalServerError)
			return
		} else if exists {
			writeError(w, "slug already exists", http.StatusConflict)
			return
		}
	}
	result, err := storage.UpdateSchedulingLink(h.db, id, &updated)
	if err != nil {
		writeError(w, "update failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if patch.CoHostEmails != nil {
		_ = storage.RemoveNonOwnerLinkHosts(h.db, id, userID)
		for _, email := range patch.CoHostEmails {
			if u, e := storage.GetUserByEmail(h.db, strings.ToLower(strings.TrimSpace(email))); e == nil && u != nil {
				_, _ = storage.AddLinkHost(h.db, id, u.ID, "pending")
			}
		}
	}
	h.writeLink(w, result, userID)
}

// DELETE /api/scheduling-links/{id}
func (h *schedulingLinkHandlers) deleteLink(w http.ResponseWriter, r *http.Request) {
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
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Email) == "" {
		writeError(w, "email is required", http.StatusBadRequest)
		return
	}
	invitee, err := storage.GetUserByEmail(h.db, strings.ToLower(strings.TrimSpace(body.Email)))
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

// GET /api/scheduling-links/host-invites and /api/scheduling-links/invites
func (h *schedulingLinkHandlers) listHostInvites(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	invites, err := storage.GetPendingInvitesForUser(h.db, userID)
	if err != nil {
		writeError(w, "internal error", http.StatusInternalServerError)
		return
	}
	out := make([]hostInviteDTO, 0, len(invites))
	for _, invite := range invites {
		link, e := storage.GetSchedulingLinkByID(h.db, invite.LinkID)
		if e != nil || link == nil {
			continue
		}
		owner, e := storage.GetUserByID(h.db, link.OwnerUserID)
		if e != nil || owner == nil {
			continue
		}
		out = append(out, hostInviteDTO{LinkID: link.ID.String(), LinkTitle: link.Title, OwnerName: owner.Name, OwnerEmail: owner.Email, InvitedAt: invite.InvitedAt.UTC().Format("2006-01-02T15:04:05.000Z07:00")})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// POST /api/scheduling-links/host-invites/{id}/accept|decline and aliases /{id}/accept|decline
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
func (h *schedulingLinkHandlers) acceptInvite(w http.ResponseWriter, r *http.Request) {
	h.respondToInvite(w, r, "accepted")
}
func (h *schedulingLinkHandlers) declineInvite(w http.ResponseWriter, r *http.Request) {
	h.respondToInvite(w, r, "declined")
}

// POST /api/scheduling-links/{id}/leave
func (h *schedulingLinkHandlers) leaveLink(w http.ResponseWriter, r *http.Request) {
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
	if link.OwnerUserID == userID {
		writeError(w, "owner cannot leave link", http.StatusBadRequest)
		return
	}
	if err := storage.RemoveLinkHost(h.db, id, userID); err != nil {
		writeError(w, "leave failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
