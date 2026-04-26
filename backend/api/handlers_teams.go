package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/smtp"
	"os"
	"strings"
	"time"

	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/storage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

type teamHandlers struct {
	db          *sql.DB
	oauthConfig *oauth2.Config
}

func newTeamHandlers(db *sql.DB, cfg *oauth2.Config) *teamHandlers {
	return &teamHandlers{db: db, oauthConfig: cfg}
}

// POST /api/teams
func (h *teamHandlers) createTeam(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	if userID == uuid.Nil {
		writeError(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Name) == "" {
		writeError(w, "name is required", http.StatusBadRequest)
		return
	}
	team, err := storage.CreateTeam(h.db, strings.TrimSpace(body.Name), userID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Creator is owner.
	_ = storage.AddTeamMember(h.db, team.ID, userID, "owner")

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(team)
}

// GET /api/teams
func (h *teamHandlers) listTeams(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	if userID == uuid.Nil {
		writeError(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	teams, err := storage.ListTeamsForUser(h.db, userID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if teams == nil {
		teams = []*storage.Team{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(teams)
}

// GET /api/teams/:id
func (h *teamHandlers) getTeam(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	teamID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, "invalid team id", http.StatusBadRequest)
		return
	}

	if _, err := h.requireMember(teamID, userID); err != nil {
		writeError(w, "forbidden", http.StatusForbidden)
		return
	}

	team, err := storage.GetTeam(h.db, teamID)
	if err == sql.ErrNoRows {
		writeError(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	members, _ := storage.ListTeamMembers(h.db, teamID)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"team":    team,
		"members": members,
	})
}

// PATCH /api/teams/:id
func (h *teamHandlers) patchTeam(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	teamID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, "invalid team id", http.StatusBadRequest)
		return
	}
	if err := h.requireOwner(teamID, userID); err != nil {
		writeError(w, "forbidden", http.StatusForbidden)
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Name) == "" {
		writeError(w, "name is required", http.StatusBadRequest)
		return
	}
	if err := storage.RenameTeam(h.db, teamID, strings.TrimSpace(body.Name)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	team, _ := storage.GetTeam(h.db, teamID)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(team)
}

// DELETE /api/teams/:id
func (h *teamHandlers) deleteTeam(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	teamID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, "invalid team id", http.StatusBadRequest)
		return
	}
	if err := h.requireOwner(teamID, userID); err != nil {
		writeError(w, "forbidden", http.StatusForbidden)
		return
	}
	if err := storage.DeleteTeam(h.db, teamID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/teams/:id/members/invite
func (h *teamHandlers) inviteMember(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	teamID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, "invalid team id", http.StatusBadRequest)
		return
	}
	if err := h.requireOwner(teamID, userID); err != nil {
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

	invite, err := storage.CreateTeamInvite(h.db, teamID, strings.ToLower(strings.TrimSpace(body.Email)), userID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	team, _ := storage.GetTeam(h.db, teamID)
	inviter, _ := storage.GetUserByID(h.db, userID)
	inviterName := "Someone"
	if inviter != nil && inviter.Name != "" {
		inviterName = inviter.Name
	}
	teamName := "a Paceday team"
	if team != nil {
		teamName = team.Name
	}
	go sendTeamInviteEmail(invite.InviteeEmail, inviterName, teamName, invite.Token)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(invite)
}

// GET /api/teams/invites/:token (public)
func (h *teamHandlers) getInvite(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	_ = storage.ExpireOldInvites(h.db)
	invite, err := storage.GetTeamInviteByToken(h.db, token)
	if err == sql.ErrNoRows {
		writeError(w, "invite not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if invite.Status != "pending" {
		writeError(w, "invite is "+invite.Status, http.StatusGone)
		return
	}
	team, _ := storage.GetTeam(h.db, invite.TeamID)
	inviter, _ := storage.GetUserByID(h.db, invite.InvitedBy)
	teamName := ""
	if team != nil {
		teamName = team.Name
	}
	inviterName := ""
	if inviter != nil {
		inviterName = inviter.Name
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"teamName":    teamName,
		"inviterName": inviterName,
		"email":       invite.InviteeEmail,
	})
}

// POST /api/teams/invites/:token/accept
func (h *teamHandlers) acceptInvite(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	if userID == uuid.Nil {
		writeError(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	token := chi.URLParam(r, "token")
	_ = storage.ExpireOldInvites(h.db)
	invite, err := storage.GetTeamInviteByToken(h.db, token)
	if err == sql.ErrNoRows {
		writeError(w, "invite not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if invite.Status != "pending" {
		writeError(w, "invite is "+invite.Status, http.StatusGone)
		return
	}
	// Ensure the logged-in user's email matches the invite.
	user, err := storage.GetUserByID(h.db, userID)
	if err != nil || user == nil || !strings.EqualFold(user.Email, invite.InviteeEmail) {
		writeError(w, "this invite is for a different email address", http.StatusForbidden)
		return
	}
	if err := storage.AddTeamMember(h.db, invite.TeamID, userID, "member"); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := storage.AcceptTeamInvite(h.db, invite.ID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DELETE /api/teams/:id/members/:userId
func (h *teamHandlers) removeMember(w http.ResponseWriter, r *http.Request) {
	callerID := userIDFromCtx(r.Context())
	teamID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, "invalid team id", http.StatusBadRequest)
		return
	}
	targetID, err := uuid.Parse(chi.URLParam(r, "userId"))
	if err != nil {
		writeError(w, "invalid user id", http.StatusBadRequest)
		return
	}

	// Allow owner to remove anyone, or self-removal.
	if callerID != targetID {
		if err := h.requireOwner(teamID, callerID); err != nil {
			writeError(w, "forbidden", http.StatusForbidden)
			return
		}
	}
	if err := storage.RemoveTeamMember(h.db, teamID, targetID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/teams/:id/no-meeting-zones
func (h *teamHandlers) createZone(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	teamID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, "invalid team id", http.StatusBadRequest)
		return
	}
	if err := h.requireOwner(teamID, userID); err != nil {
		writeError(w, "forbidden", http.StatusForbidden)
		return
	}
	var body struct {
		DayOfWeek int    `json:"dayOfWeek"`
		StartTime string `json:"startTime"`
		EndTime   string `json:"endTime"`
		Label     string `json:"label"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if body.StartTime == "" || body.EndTime == "" {
		writeError(w, "startTime and endTime are required", http.StatusBadRequest)
		return
	}
	zone, err := storage.CreateNoMeetingZone(h.db, teamID, body.DayOfWeek, body.StartTime, body.EndTime, body.Label)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(zone)
}

// GET /api/teams/:id/no-meeting-zones
func (h *teamHandlers) listZones(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	teamID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, "invalid team id", http.StatusBadRequest)
		return
	}
	if _, err := h.requireMember(teamID, userID); err != nil {
		writeError(w, "forbidden", http.StatusForbidden)
		return
	}
	zones, err := storage.ListNoMeetingZones(h.db, teamID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if zones == nil {
		zones = []*storage.NoMeetingZone{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(zones)
}

// DELETE /api/teams/:id/no-meeting-zones/:zoneId
func (h *teamHandlers) deleteZone(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	teamID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, "invalid team id", http.StatusBadRequest)
		return
	}
	if err := h.requireOwner(teamID, userID); err != nil {
		writeError(w, "forbidden", http.StatusForbidden)
		return
	}
	zoneID, err := uuid.Parse(chi.URLParam(r, "zoneId"))
	if err != nil {
		writeError(w, "invalid zone id", http.StatusBadRequest)
		return
	}
	if err := storage.DeleteNoMeetingZone(h.db, zoneID, teamID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GET /api/teams/:id/availability?date=2026-04-28&duration=60
func (h *teamHandlers) availability(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	teamID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, "invalid team id", http.StatusBadRequest)
		return
	}
	if _, err := h.requireMember(teamID, userID); err != nil {
		writeError(w, "forbidden", http.StatusForbidden)
		return
	}

	dateStr := r.URL.Query().Get("date")
	day, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		writeError(w, "date must be YYYY-MM-DD", http.StatusBadRequest)
		return
	}

	durationMinutes := 60
	if d := r.URL.Query().Get("duration"); d != "" {
		if _, err := fmt.Sscanf(d, "%d", &durationMinutes); err != nil || durationMinutes <= 0 {
			writeError(w, "duration must be a positive integer (minutes)", http.StatusBadRequest)
			return
		}
	}

	eng := &engine.TeamAvailabilityEngine{DB: h.db, OAuthConfig: h.oauthConfig}
	slots, err := eng.FindSlots(r.Context(), teamID, day, durationMinutes)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if slots == nil {
		slots = []engine.TeamSlot{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"slots": slots})
}

// GET /api/teams/:id/analytics?date=2026-04-28
func (h *teamHandlers) analyticsHandler(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	teamID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, "invalid team id", http.StatusBadRequest)
		return
	}
	if _, err := h.requireMember(teamID, userID); err != nil {
		writeError(w, "forbidden", http.StatusForbidden)
		return
	}

	dateStr := r.URL.Query().Get("date")
	weekStart := startOfWeek(time.Now())
	if dateStr != "" {
		d, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			writeError(w, "date must be YYYY-MM-DD", http.StatusBadRequest)
			return
		}
		weekStart = startOfWeek(d)
	}

	eng := &engine.TeamAvailabilityEngine{DB: h.db, OAuthConfig: h.oauthConfig}
	analytics, err := eng.GetTeamAnalytics(r.Context(), teamID, weekStart)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(analytics)
}

// --- Helpers ---

func (h *teamHandlers) requireMember(teamID, userID uuid.UUID) (*storage.TeamMember, error) {
	m, err := storage.GetTeamMember(h.db, teamID, userID)
	if err != nil {
		return nil, err
	}
	return m, nil
}

func (h *teamHandlers) requireOwner(teamID, userID uuid.UUID) error {
	m, err := storage.GetTeamMember(h.db, teamID, userID)
	if err != nil || m.Role != "owner" {
		return fmt.Errorf("not owner")
	}
	return nil
}

func sendTeamInviteEmail(to, inviterName, teamName, token string) {
	smtpHost := os.Getenv("SMTP_HOST")
	smtpPort := os.Getenv("SMTP_PORT")
	smtpUser := os.Getenv("SMTP_USER")
	smtpPass := os.Getenv("SMTP_PASS")
	smtpFrom := os.Getenv("SMTP_FROM")
	baseURL := os.Getenv("BASE_URL")

	if smtpHost == "" || smtpFrom == "" {
		return
	}
	if smtpPort == "" {
		smtpPort = "587"
	}
	if baseURL == "" {
		baseURL = "https://paceday.app"
	}

	subject := fmt.Sprintf("%s invited you to join the \"%s\" team on Paceday", inviterName, teamName)
	body := fmt.Sprintf(
		"Hi,\n\n%s has invited you to join the \"%s\" team on Paceday.\n\nAccept your invite:\n%s/invites/%s\n\nThis link expires in 7 days.\n\nThe Paceday team",
		inviterName, teamName, baseURL, token,
	)
	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\n\r\n%s", smtpFrom, to, subject, body)

	var auth smtp.Auth
	if smtpUser != "" {
		auth = smtp.PlainAuth("", smtpUser, smtpPass, smtpHost)
	}
	_ = smtp.SendMail(smtpHost+":"+smtpPort, auth, smtpFrom, []string{to}, []byte(msg))
}
