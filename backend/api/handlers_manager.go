package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/storage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

type managerHandlers struct {
	db          *sql.DB
	oauthConfig *oauth2.Config
}

// ── Profile ───────────────────────────────────────────────────────────────────

func (h *managerHandlers) getProfile(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	p, err := storage.GetOrCreateUserProfile(h.db, userID)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	count := 0
	_ = h.db.QueryRow(`SELECT COUNT(*) FROM manager_team_members WHERE manager_user_id=$1`, userID).Scan(&count)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"is_manager":        p.IsManager,
		"detected_at":       p.DetectedAt,
		"team_member_count": count,
	})
}

func (h *managerHandlers) postProfile(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	var body struct {
		IsManager bool `json:"is_manager"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	p, err := storage.GetOrCreateUserProfile(h.db, userID)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	p.IsManager = body.IsManager
	if body.IsManager && p.DetectedAt == nil {
		// Trigger detection async
		eng := &engine.ManagerEngine{DB: h.db, OAuthConfig: h.oauthConfig}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_, _ = eng.DetectTeam(ctx, userID)
		}()
	}
	_ = storage.UpsertUserProfile(h.db, p)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(p)
}

// ── Team Detection ─────────────────────────────────────────────────────────────

func (h *managerHandlers) detect(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	eng := &engine.ManagerEngine{DB: h.db, OAuthConfig: h.oauthConfig}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	result, err := eng.DetectTeam(ctx, userID)
	if err != nil {
		http.Error(w, "detection error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

// ── Team CRUD ─────────────────────────────────────────────────────────────────

func (h *managerHandlers) getTeam(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	weekStr := r.URL.Query().Get("week")
	weekStart := currentWeekStart()
	if weekStr != "" {
		if t, err := time.Parse("2006-01-02", weekStr); err == nil {
			weekStart = t
		}
	}
	priorWeekStart := weekStart.Add(-7 * 24 * time.Hour)

	members, err := storage.ListManagerTeamMembers(h.db, userID)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}

	eng := &engine.ManagerEngine{DB: h.db, OAuthConfig: h.oauthConfig}

	type memberResp struct {
		Email          string                   `json:"email"`
		DisplayName    string                   `json:"display_name"`
		Source         string                   `json:"source"`
		Cadence        string                   `json:"cadence"`
		LastOneOnOneAt *time.Time               `json:"last_one_on_one_at"`
		IsPacedayUser  bool                     `json:"is_paceday_user"`
		ThisWeek       *engine.MemberWeekStats  `json:"this_week"`
		LastWeek       *engine.MemberWeekStats  `json:"last_week"`
		FocusTrendPct  float64                  `json:"focus_trend_pct"`
	}

	var result []memberResp
	for _, m := range members {
		thisWeek, _ := eng.GetMemberWeek(r.Context(), userID, m, weekStart)
		lastWeek, _ := eng.GetMemberWeek(r.Context(), userID, m, priorWeekStart)
		if thisWeek == nil {
			thisWeek = &engine.MemberWeekStats{}
		}
		if lastWeek == nil {
			lastWeek = &engine.MemberWeekStats{}
		}
		result = append(result, memberResp{
			Email:          m.MemberEmail,
			DisplayName:    m.DisplayName,
			Source:         m.Source,
			Cadence:        m.Cadence,
			LastOneOnOneAt: m.LastOneOnOneAt,
			IsPacedayUser:  m.MemberUserID != nil,
			ThisWeek:       thisWeek,
			LastWeek:       lastWeek,
			FocusTrendPct:  engine.TrendPct(thisWeek.FocusMinutes, lastWeek.FocusMinutes),
		})
	}
	if result == nil {
		result = []memberResp{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"members": result})
}

func (h *managerHandlers) addMember(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	var body struct {
		Email            string `json:"email"`
		DisplayName      string `json:"display_name"`
		Cadence          string `json:"cadence"`
		CadenceCustomDays *int  `json:"cadence_custom_days"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Email == "" {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if body.Cadence == "" {
		body.Cadence = "none"
	}
	m := &storage.ManagerTeamMember{
		ManagerUserID:    userID,
		MemberEmail:      body.Email,
		DisplayName:      body.DisplayName,
		Source:           "manual",
		Cadence:          body.Cadence,
		CadenceCustomDays: body.CadenceCustomDays,
	}
	// Resolve member_user_id if they're a Paceday user
	var uid uuid.UUID
	if err := h.db.QueryRow(`SELECT id FROM users WHERE email=$1`, body.Email).Scan(&uid); err == nil {
		m.MemberUserID = &uid
	}
	if err := storage.UpsertManagerTeamMember(h.db, m); err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(m)
}

func (h *managerHandlers) deleteMember(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	email := chi.URLParam(r, "email")
	_ = storage.DeleteManagerTeamMemberByEmail(h.db, userID, email)
	w.WriteHeader(http.StatusNoContent)
}

func (h *managerHandlers) patchMember(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	email := chi.URLParam(r, "email")
	var body struct {
		DisplayName      *string `json:"display_name"`
		Cadence          *string `json:"cadence"`
		CadenceCustomDays *int   `json:"cadence_custom_days"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	m, err := storage.GetManagerTeamMemberByEmail(h.db, userID, email)
	if err == sql.ErrNoRows {
		http.Error(w, "not found", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	displayName := m.DisplayName
	cadence := m.Cadence
	if body.DisplayName != nil {
		displayName = *body.DisplayName
	}
	if body.Cadence != nil {
		cadence = *body.Cadence
	}
	if err := storage.PatchManagerTeamMember(h.db, userID, email, displayName, cadence, body.CadenceCustomDays); err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	updated, _ := storage.GetManagerTeamMemberByEmail(h.db, userID, email)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(updated)
}

// ── Cadence gaps ──────────────────────────────────────────────────────────────

func (h *managerHandlers) getGaps(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	eng := &engine.ManagerEngine{DB: h.db, OAuthConfig: h.oauthConfig}
	gaps, err := eng.GetGaps(r.Context(), userID)
	if err != nil {
		http.Error(w, "gap detection error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if gaps == nil {
		gaps = []engine.CadenceGap{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"gaps": gaps})
}

func (h *managerHandlers) scheduleMember(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	email := chi.URLParam(r, "email")

	var body struct {
		SuggestedDate string `json:"suggested_date"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	m, err := storage.GetManagerTeamMemberByEmail(h.db, userID, email)
	if err == sql.ErrNoRows {
		http.Error(w, "not found", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}

	q := url.Values{}
	q.Set("title", fmt.Sprintf("1:1 with %s", m.DisplayName))
	q.Set("attendees", email)
	q.Set("duration", "30")
	if body.SuggestedDate != "" {
		q.Set("date", body.SuggestedDate)
	}
	prefillURL := "/app/calendar/new?" + q.Encode()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"prefill_url": prefillURL})
}

// ── Analytics ────────────────────────────────────────────────────────────────

func (h *managerHandlers) getAnalytics(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	weekStr := r.URL.Query().Get("week")
	weekStart := currentWeekStart()
	if weekStr != "" {
		if t, err := time.Parse("2006-01-02", weekStr); err == nil {
			weekStart = t
		}
	}
	months := 3
	members, err := storage.ListManagerTeamMembers(h.db, userID)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}

	type weekData struct {
		WeekStart      string                  `json:"week_start"`
		FocusMinutes   int                     `json:"focus_minutes"`
		MeetingMinutes int                     `json:"meeting_minutes"`
		FreeMinutes    int                     `json:"free_minutes"`
		DataAvailable  bool                    `json:"data_available"`
	}
	type memberAnalytics struct {
		Email   string     `json:"email"`
		Name    string     `json:"display_name"`
		Weeks   []weekData `json:"weeks"`
	}
	_ = months

	eng := &engine.ManagerEngine{DB: h.db, OAuthConfig: h.oauthConfig}
	var result []memberAnalytics
	numWeeks := months * 4
	for _, m := range members {
		var weeks []weekData
		for i := 0; i < numWeeks; i++ {
			ws := weekStart.Add(time.Duration(-i*7) * 24 * time.Hour)
			stats, _ := eng.GetMemberWeek(r.Context(), userID, m, ws)
			if stats == nil {
				stats = &engine.MemberWeekStats{}
			}
			weeks = append(weeks, weekData{
				WeekStart:      ws.Format("2006-01-02"),
				FocusMinutes:   stats.FocusMinutes,
				MeetingMinutes: stats.MeetingMinutes,
				FreeMinutes:    stats.FreeMinutes,
				DataAvailable:  stats.DataAvailable,
			})
		}
		result = append(result, memberAnalytics{Email: m.MemberEmail, Name: m.DisplayName, Weeks: weeks})
	}
	if result == nil {
		result = []memberAnalytics{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"members": result})
}

// ── helpers ───────────────────────────────────────────────────────────────────

func currentWeekStart() time.Time {
	now := time.Now().UTC()
	weekday := int(now.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	monday := now.Add(-time.Duration(weekday-1) * 24 * time.Hour)
	return time.Date(monday.Year(), monday.Month(), monday.Day(), 0, 0, 0, 0, time.UTC)
}

