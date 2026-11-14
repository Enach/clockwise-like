package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Enach/paceday/backend/api/gen"
	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/storage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

type schedulingLinkHandlers struct {
	bookEng *engine.BookingEngine
	db      *sql.DB
	// ownerZone reads a link owner's profile zone for SchedulingLink.time_zone
	// (PAC-72 D-9); nil is storage.GetUserTimezone. Tests inject a failure.
	ownerZone ownerZoneReader
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
	// PAC-72 revision 2: the link's horizon (D-1), refusal setting (D-2/D-5)
	// and the zone its days and hours are evaluated in (D-9, read-only).
	BookingHorizonDays int    `json:"booking_horizon_days"`
	RefusalMessage     string `json:"refusal_message"`
	TimeZone           string `json:"time_zone"`
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
	// PAC-72 revision 2: absent or null means 60 / generic.
	BookingHorizonDays *int    `json:"booking_horizon_days"`
	RefusalMessage     *string `json:"refusal_message"`
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
	// PAC-72 revision 2: absent or null leaves the stored value.
	BookingHorizonDays *int    `json:"booking_horizon_days"`
	RefusalMessage     *string `json:"refusal_message"`
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
		BookingHorizonDays: storage.DefaultBookingHorizonDays, RefusalMessage: storage.RefusalGeneric,
	}
	if in.BookingHorizonDays != nil {
		link.BookingHorizonDays = *in.BookingHorizonDays
	}
	if in.RefusalMessage != nil {
		link.RefusalMessage = *in.RefusalMessage
	}
	if err := validateSchedulingLink(&link); err != nil {
		return storage.SchedulingLink{}, err
	}
	return link, nil
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
	// PAC-72 revision 2 (D-1, A-3): 1..365 days.
	if link.BookingHorizonDays < 1 || link.BookingHorizonDays > 365 {
		return &httpError{Code: http.StatusUnprocessableEntity, Msg: "booking_horizon_days must be between 1 and 365"}
	}
	// PAC-72 revision 2 (D-2/D-5, A-1).
	if link.RefusalMessage != storage.RefusalGeneric && link.RefusalMessage != storage.RefusalSpecific {
		return &httpError{Code: http.StatusUnprocessableEntity, Msg: "refusal_message must be generic or specific"}
	}
	link.WindowStart = start.Format("15:04")
	link.WindowEnd = end.Format("15:04")
	return nil
}

func toSchedulingLinkDTO(db *sql.DB, link *storage.SchedulingLink, loc *time.Location, viewer uuid.UUID, includeHosts bool) schedulingLinkDTO {
	dto, _ := buildSchedulingLinkDTO(db, link, loc, viewer, includeHosts, false)
	return dto
}

// buildSchedulingLinkDTO builds the wire record. With strict set, a failed
// roster read (the host rows, or a host's user record) is returned as an error
// instead of degrading to an emptied roster or a host without an email: the
// single-link read answers 500 for it (PAC-25 C-2). Without strict, the error
// is always nil and the roster degrades as it always has.
//
// loc is the link's zone, read from the owner's settings by the caller
// (linkLocation): a failed read is the caller's 500 (PAC-72 revision 2.1).
func buildSchedulingLinkDTO(db *sql.DB, link *storage.SchedulingLink, loc *time.Location, viewer uuid.UUID, includeHosts, strict bool) (schedulingLinkDTO, error) {
	dto := schedulingLinkDTO{
		ID: link.ID.String(), OwnerID: link.OwnerUserID.String(), Title: link.Title, Slug: link.Slug,
		Durations: link.DurationOptions, Days: dayNames(link.DaysOfWeek), WindowStart: link.WindowStart,
		WindowEnd: link.WindowEnd, BufferBefore: link.BufferBefore, BufferAfter: link.BufferAfter,
		MinNoticeMinutes: link.MinNoticeMinutes, UsageType: link.UsageType, MaxUses: link.MaxUses,
		UsesCount: link.UsesCount, Active: link.Active, CreatedAt: link.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		IsOwner:            link.OwnerUserID == viewer,
		BookingHorizonDays: horizonDays(link), RefusalMessage: link.RefusalMessage, TimeZone: loc.String(),
	}
	if dto.RefusalMessage == "" {
		dto.RefusalMessage = storage.RefusalGeneric
	}
	if dto.UsageType == "" {
		dto.UsageType = "reusable"
	}
	if includeHosts {
		hosts, err := storage.GetLinkHosts(db, link.ID)
		if err != nil && strict {
			return dto, err
		}
		dto.Hosts = make([]schedulingHostDTO, 0, len(hosts))
		for _, host := range hosts {
			h := schedulingHostDTO{UserID: host.UserID.String(), IsOwner: host.UserID == link.OwnerUserID, Status: host.Status}
			user, err := storage.GetUserByID(db, host.UserID)
			if err == nil && user != nil {
				h.Email, h.Name, h.AvatarURL = user.Email, user.Name, user.AvatarURL
			} else if strict {
				if err == nil {
					err = errors.New("host user record missing")
				}
				return dto, err
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
	return dto, nil
}

func (h *schedulingLinkHandlers) writeLink(w http.ResponseWriter, link *storage.SchedulingLink, loc *time.Location, viewer uuid.UUID) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(toSchedulingLinkDTO(h.db, link, loc, viewer, true))
}

// zoneOf reads the link owner's zone for SchedulingLink.time_zone (PAC-72
// D-9). The error is a failed settings read, which the caller answers 500,
// never UTC (revision 2.1).
func (h *schedulingLinkHandlers) zoneOf(ownerID uuid.UUID) (*time.Location, error) {
	return linkLocation(h.db, h.ownerZone, ownerID)
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
	// The response's time_zone is the caller's zone; read before anything is
	// written, so a failed read creates no link (PAC-72 revision 2.1).
	loc, err := h.zoneOf(userID)
	if err != nil {
		writeServerError(w, r, "create link zone", err, http.StatusInternalServerError)
		return
	}
	if link.Slug == "" {
		if len(link.DurationOptions) == 0 {
			writeError(w, "at least one duration is required", http.StatusUnprocessableEntity)
			return
		}
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
	created, err := storage.CreateSchedulingLinkCtx(r.Context(), h.db, &link)
	if err != nil {
		writeServerError(w, r, "create failed", err, http.StatusInternalServerError)
		return
	}
	if _, err = storage.AddLinkHostCtx(r.Context(), h.db, created.ID, userID, "accepted"); err != nil {
		writeServerError(w, r, "owner host failed", err, http.StatusInternalServerError)
		return
	}
	for _, email := range input.CoHostEmails {
		// PAC-71 granting site: only the one domain-confirmed holder.
		inviteeID, ok, e := storage.ConfirmedHolder(h.db, email)
		if e != nil || !ok {
			continue
		}
		_, _ = storage.AddLinkHostCtx(r.Context(), h.db, created.ID, inviteeID, "pending")
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	h.writeLink(w, created, loc, userID)
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
	zones := map[uuid.UUID]*time.Location{}
	for _, link := range links {
		loc, ok := zones[link.OwnerUserID]
		if !ok {
			// A failed read of any listed link owner's zone (a shared
			// link's owner included) fails the list (PAC-72 AC-38).
			if loc, err = h.zoneOf(link.OwnerUserID); err != nil {
				writeServerError(w, r, "list links zone", err, http.StatusInternalServerError)
				return
			}
			zones[link.OwnerUserID] = loc
		}
		dto := toSchedulingLinkDTO(h.db, link, loc, userID, true)
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
//
// PAC-40: GetSchedulingLinkByID answers nil for a deleted link, so every
// by-id handler below (fetch, edit, delete, bookings, invite, leave) answers a
// deleted link with the unknown-id 404 for every caller, owner included,
// before the owner or host check and before reading a body (contract §3).
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
	// Readable only by the owner and accepted co-hosts, the callers who see
	// the link in their own list. Everyone else gets the unknown-id answer, so
	// the response does not reveal that the link exists (PAC-25 D-1, D-2).
	userID := userIDFromCtx(r.Context())
	if link.OwnerUserID != userID {
		status, found, err := storage.GetLinkHostStatus(h.db, link.ID, userID)
		if err != nil {
			writeServerError(w, r, "getLink host status", err, http.StatusInternalServerError)
			return
		}
		if !found || status != "accepted" {
			writeError(w, "not found", http.StatusNotFound)
			return
		}
	}
	loc, err := h.zoneOf(link.OwnerUserID)
	if err != nil {
		writeServerError(w, r, "getLink zone", err, http.StatusInternalServerError)
		return
	}
	dto, err := buildSchedulingLinkDTO(h.db, link, loc, userID, true, true)
	if err != nil {
		writeServerError(w, r, "getLink roster", err, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(dto)
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
	// The canonical spelling wins whenever it is carried (present and not
	// null; [] counts as carried and fails validation), as on create; the
	// alias is used only when the canonical spelling is absent or null
	// (PAC-40 D-5, API-049, AC-14, AC-14a).
	switch {
	case patch.DurationOptions != nil:
		updated.DurationOptions = patch.DurationOptions
	case patch.Durations != nil:
		updated.DurationOptions = patch.Durations
	}
	switch {
	case patch.DaysOfWeek != nil:
		updated.DaysOfWeek = patch.DaysOfWeek
	case patch.Days != nil:
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
	if patch.BookingHorizonDays != nil {
		updated.BookingHorizonDays = *patch.BookingHorizonDays
	}
	if patch.RefusalMessage != nil {
		updated.RefusalMessage = *patch.RefusalMessage
	}
	if slugPatched && updated.Slug == "" {
		writeError(w, "slug cannot be empty", http.StatusUnprocessableEntity)
		return
	}
	if validationErr := validateSchedulingLink(&updated); validationErr != nil {
		writeError(w, validationErr.Msg, validationErr.Code)
		return
	}
	// Read before the update, so a failed read changes nothing (PAC-72
	// revision 2.1).
	loc, err := h.zoneOf(existing.OwnerUserID)
	if err != nil {
		writeServerError(w, r, "update link zone", err, http.StatusInternalServerError)
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
	// PAC-101 (contract §3.2, spec D-5): the fields and, when co_host_emails
	// is carried, the co-host list (set by difference, PAC-61) are written in
	// one transaction whose first lock on the link is the UPDATE. A failed
	// co-host lookup or write is a 500 with nothing changed; a delete
	// committed before the UPDATE is the 404 with nothing changed (PAC-40
	// K4-1, AC-6b); a delete started after it waits for this PATCH.
	result, err := storage.UpdateSchedulingLinkAndCoHostsCtx(r.Context(), h.db, id, &updated, userID, patch.CoHostEmails)
	if err != nil {
		writeServerError(w, r, "update failed", err, http.StatusInternalServerError)
		return
	}
	if result == nil {
		writeError(w, "not found", http.StatusNotFound)
		return
	}
	h.writeLink(w, result, loc, userID)
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
	deleted, err := storage.DeleteSchedulingLinkCtx(r.Context(), h.db, id)
	if err != nil {
		writeServerError(w, r, "delete failed", err, http.StatusInternalServerError)
		return
	}
	if !deleted {
		// A concurrent delete committed after the read above: one 204, this
		// one 404, and deleted_at is not moved (PAC-40 K4-2, AC-6b).
		writeError(w, "not found", http.StatusNotFound)
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
	// PAC-71 granting site: an unconfirmed holder is "user not found" too,
	// so the answer does not reveal standing (contract §10, API-072).
	inviteeID, found, err := storage.ConfirmedHolder(h.db, body.Email)
	if err != nil || !found {
		writeError(w, "user not found", http.StatusNotFound)
		return
	}
	// An existing host keeps their status (PAC-61): 200 with their row as is;
	// a new invitee gets a pending row and 201.
	host, created, err := storage.InviteLinkHostCtx(r.Context(), h.db, id, inviteeID)
	if errors.Is(err, storage.ErrLinkGone) {
		// PAC-101: deleted after the read above, before the host row was
		// written; nothing was written, as for a link deleted before.
		writeError(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		writeServerError(w, r, "invite failed", err, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if created {
		w.WriteHeader(http.StatusCreated)
	}
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
	matched, err := storage.RespondToHostInviteCtx(r.Context(), h.db, linkID, userID, status)
	if err != nil {
		writeServerError(w, r, "update failed", err, http.StatusInternalServerError)
		return
	}
	if !matched {
		// PAC-30: no host row for the caller on this link (or no such link).
		// Same 404 for both, so the answer does not reveal whether a link exists.
		writeError(w, "not found", http.StatusNotFound)
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
	removed, err := storage.RemoveLinkHostCtx(r.Context(), h.db, id, userID)
	if err != nil {
		writeServerError(w, r, "leave failed", err, http.StatusInternalServerError)
		return
	}
	if !removed {
		// PAC-30: the caller has no host row on this link; nothing was deleted.
		writeError(w, "not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Owner archive of deleted links (PAC-40 §3.2, contract §2.1-§2.4) -------

// bookerErasureEnabled is BookerErasurePolicy.enabled: a property of the
// server build, never an operator setting (contract §2.4). True from part B
// on (PR split B, contract §6): this build runs the erasure job
// (engine.BookerErasure, started in main.go), so the delete confirmation may
// promise erasure and every archived link awaiting it carries its
// erasure_due_at.
const bookerErasureEnabled = true

// bookerRetentionDays is BookerErasurePolicy.retention_days: erasure is due
// 90 x 24 h after a deleted link's last booking end (owner, 2026-10-27). The
// contract pins it (minimum = maximum = 90); a different value is a contract
// change, not a setting. It is storage.BookerRetention in days, the figure
// the job itself uses.
const bookerRetentionDays = int(storage.BookerRetention / (24 * time.Hour))

func bookerErasurePolicy() gen.BookerErasurePolicy {
	return gen.BookerErasurePolicy{Enabled: bookerErasureEnabled, RetentionDays: bookerRetentionDays}
}

// archivedLinkDTO maps a stored archived link to the wire, every instant in
// UTC (Z suffix). erasure_due_at is set only for a link awaiting erasure in a
// build whose job runs.
func archivedLinkDTO(a *storage.ArchivedLink) gen.ArchivedSchedulingLink {
	dto := gen.ArchivedSchedulingLink{
		Id: a.ID, Title: a.Title, Slug: a.Slug, DeletedAt: a.DeletedAt.UTC(),
		BookingsCount: a.BookingsCount,
	}
	if a.LastBookingEndAt != nil {
		t := a.LastBookingEndAt.UTC()
		dto.LastBookingEndAt = &t
	}
	if a.ErasedAt != nil {
		t := a.ErasedAt.UTC()
		dto.ErasedAt = &t
	}
	switch {
	case a.BookingsCount == 0:
		dto.ErasureState = gen.NoBookings
	case a.AwaitingErasure > 0:
		dto.ErasureState = gen.AwaitingErasure
		if bookerErasureEnabled && dto.LastBookingEndAt != nil {
			due := dto.LastBookingEndAt.Add(storage.BookerRetention)
			dto.ErasureDueAt = &due
		}
	default:
		dto.ErasureState = gen.Erased
	}
	return dto
}

// archivedBookingDTO maps a stored booking to the archive's record. Every
// instant is UTC, so it is serialised with a Z suffix (the web's schema
// accepts only that). An erased
// booking (booker_erased_at set) answers null name and email, never the empty strings
// the database keeps (contract §2.2); a booking not erased answers both
// exactly as stored, as the live bookings read did (AC-22).
func archivedBookingDTO(b *storage.ArchivedBooking) gen.ArchivedSchedulingLinkBooking {
	dto := gen.ArchivedSchedulingLinkBooking{
		Id: b.ID, LinkId: b.LinkID, StartTime: b.StartTime.UTC(), EndTime: b.EndTime.UTC(),
		Status: b.Status, Notes: b.Notes, CreatedAt: b.CreatedAt.UTC(),
	}
	if b.BookerErasedAt != nil {
		t := b.BookerErasedAt.UTC()
		dto.BookerErasedAt = &t
		return dto
	}
	name, email := b.BookerName, b.BookerEmail
	dto.BookerName, dto.BookerEmail = &name, &email
	return dto
}

// GET /api/scheduling-links/archive
//
// The caller's own deleted links, most recently deleted first, plus the
// erasure policy the delete confirmation is built from. Owner only by
// construction (the read is filtered on the caller); read-only.
func (h *schedulingLinkHandlers) listArchivedLinks(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	links, err := storage.ListArchivedSchedulingLinks(h.db, userID)
	if err != nil {
		writeServerError(w, r, "list archive", err, http.StatusInternalServerError)
		return
	}
	out := gen.ArchivedSchedulingLinkListResponse{
		Links:         make([]gen.ArchivedSchedulingLink, 0, len(links)),
		BookerErasure: bookerErasurePolicy(),
	}
	for _, link := range links {
		out.Links = append(out.Links, archivedLinkDTO(link))
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// GET /api/scheduling-links/archive/{id}
//
// One archived link and every booking. A single lookup filtered on "owner =
// caller AND deleted", so an unknown id, a live or paused link and another
// owner's deleted link all get the same 404 body; never 403 (AC-6a, AC-23).
func (h *schedulingLinkHandlers) getArchivedLink(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, "invalid id", http.StatusBadRequest)
		return
	}
	link, err := storage.GetArchivedSchedulingLink(h.db, userID, id)
	if err != nil {
		writeServerError(w, r, "get archived link", err, http.StatusInternalServerError)
		return
	}
	if link == nil {
		writeError(w, "not found", http.StatusNotFound)
		return
	}
	bookings, err := storage.GetArchivedBookings(h.db, link.ID)
	if err != nil {
		writeServerError(w, r, "get archived bookings", err, http.StatusInternalServerError)
		return
	}
	out := gen.ArchivedSchedulingLinkDetail{
		Link:     archivedLinkDTO(link),
		Bookings: make([]gen.ArchivedSchedulingLinkBooking, 0, len(bookings)),
	}
	for _, b := range bookings {
		out.Bookings = append(out.Bookings, archivedBookingDTO(b))
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}
