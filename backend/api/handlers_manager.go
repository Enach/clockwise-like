package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/internal/lifecycle"
	"github.com/Enach/paceday/backend/storage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

// The reads that answer a write with the row it produced (PAC-26, D-3). They
// are variables so a test can make the read-back fail after the write has
// committed; production code never reassigns them.
var (
	readManagerMember        = storage.GetManagerTeamMemberByEmail
	readManagerMemberForTeam = storage.GetManagerTeamMemberByEmailForTeam
	readTeam                 = storage.GetTeam
)

// readMemberInScope reads one roster row: the team view when teamID is set,
// the manager's global row otherwise.
func readMemberInScope(db *sql.DB, managerID uuid.UUID, teamID *uuid.UUID, email string) (*storage.ManagerTeamMember, error) {
	if teamID != nil {
		return readManagerMemberForTeam(db, managerID, *teamID, email)
	}
	return readManagerMember(db, managerID, email)
}

type managerHandlers struct {
	db        *sql.DB
	newEngine func() ManagerWorkflow
}

func newManagerHandlers(db *sql.DB, oauthConfig *oauth2.Config) *managerHandlers {
	return newManagerHandlersWithEngineFactory(db, func() ManagerWorkflow {
		return &engine.ManagerEngine{DB: db, OAuthConfig: oauthConfig}
	})
}

func newManagerHandlersWithEngineFactory(db *sql.DB, newEngine func() ManagerWorkflow) *managerHandlers {
	return &managerHandlers{db: db, newEngine: newEngine}
}

// ── Profile ───────────────────────────────────────────────────────────────────

func (h *managerHandlers) getProfile(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	// PAC-80 item 2: the default row is created under the session re-check.
	p, err := storage.GetOrCreateUserProfileCtx(r.Context(), h.db, userID)
	if err != nil {
		logServerError(r, "db error", err, http.StatusInternalServerError)
		writeError(w, "db error", http.StatusInternalServerError)
		return
	}
	// PAC-122 SF-076: a failed count is the 500, never team_member_count 0.
	count, err := storage.CountManagerTeamMembers(h.db, userID)
	if err != nil {
		writeServerError(w, r, "getProfile: roster count", err, http.StatusInternalServerError)
		return
	}

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
		writeError(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	// PAC-80 item 2 (spec AC-5, AC-9): nothing is written into the account
	// before the detection is spawned. The current profile is read under the
	// re-check without creating a row; the default row, if missing, is created
	// under the re-check after the spawn, so a session that goes stale at any
	// point up to it leaves no profile row at all.
	cur, err := storage.GetUserProfileCtx(r.Context(), h.db, userID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		logServerError(r, "db error", err, http.StatusInternalServerError)
		writeError(w, "db error", http.StatusInternalServerError)
		return
	}
	if body.IsManager && (cur == nil || cur.DetectedAt == nil) {
		// Trigger detection async
		eng := h.newEngine()
		// PAC-81 AC-A6: a tracked unit, awaited by the drain (own 30 s).
		// PAC-71: GoFrom keeps the request's values, so the session guard
		// stays: detection's token load and roster writes are re-checked.
		// PAC-122 SF-096: its error is logged and reported, not discarded.
		lifecycle.GoFromErr(r.Context(), lifecycle.Unit{Kind: "manager-detection", ID: userID.String()}, func(work context.Context) error {
			ctx, cancel := context.WithTimeout(work, 30*time.Second)
			defer cancel()
			_, err := eng.DetectTeam(ctx, userID)
			return err
		})
	}
	p, err := storage.GetOrCreateUserProfileCtx(r.Context(), h.db, userID)
	if err != nil {
		logServerError(r, "db error", err, http.StatusInternalServerError)
		writeError(w, "db error", http.StatusInternalServerError)
		return
	}
	p.IsManager = body.IsManager
	// PAC-122 SF-009: a failed write is the 500, never a 200 echoing a value
	// that was not stored.
	if err := storage.UpsertUserProfileCtx(r.Context(), h.db, p); err != nil {
		writeServerError(w, r, "postProfile", err, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(p)
}

// ── Team Detection ─────────────────────────────────────────────────────────────

func (h *managerHandlers) detect(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	teamID, ok := h.requiredManagerTeamScope(w, r, userID, true)
	if !ok {
		return
	}
	eng := h.newEngine()

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	result, err := eng.DetectTeam(ctx, userID)
	if err != nil {
		writeCalendarFailure(w, r, "detection error", err)
		return
	}
	type candidateResponse struct {
		Email           string `json:"email"`
		DisplayName     string `json:"display_name"`
		AlreadyAssigned bool   `json:"already_assigned"`
	}
	candidates := make([]candidateResponse, 0, len(result.Candidates))
	eligible := 0
	for _, candidate := range result.Candidates {
		_, lookupErr := storage.GetManagerTeamMemberByEmailForTeam(h.db, userID, *teamID, candidate.Email)
		alreadyAssigned := lookupErr == nil
		if lookupErr != nil && lookupErr != sql.ErrNoRows {
			logServerError(r, "db error", lookupErr, http.StatusInternalServerError)
			writeError(w, "db error", http.StatusInternalServerError)
			return
		}
		if !alreadyAssigned {
			eligible++
		}
		candidates = append(candidates, candidateResponse{
			Email: candidate.Email, DisplayName: candidate.DisplayName, AlreadyAssigned: alreadyAssigned,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"team_id":    teamID.String(),
		"scanned_at": result.ScannedAt,
		"detected":   len(candidates),
		"eligible":   eligible,
		"assigned":   0,
		"skipped":    len(candidates) - eligible,
		"candidates": candidates,
	})
}

func (h *managerHandlers) confirmDetection(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	teamID, ok := h.requiredManagerTeamScope(w, r, userID, true)
	if !ok {
		return
	}
	var body struct {
		Emails []string `json:"emails"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if body.Emails == nil {
		writeError(w, "emails is required", http.StatusUnprocessableEntity)
		return
	}
	normalized := make([]string, 0, len(body.Emails))
	for _, raw := range body.Emails {
		email, err := normalizeManagerEmail(raw)
		if err != nil {
			writeError(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		normalized = append(normalized, email)
	}
	assigned, skipped, total, err := storage.ConfirmManagerTeamMembersCtx(r.Context(), h.db, userID, *teamID, normalized)
	if err != nil {
		logServerError(r, "db error", err, http.StatusInternalServerError)
		writeError(w, "db error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"team_id": teamID.String(), "assigned": assigned, "skipped": skipped, "total": total,
	})
}

// ── Team CRUD ─────────────────────────────────────────────────────────────────

func (h *managerHandlers) getTeam(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	teamID, ok := h.requiredManagerTeamScope(w, r, userID, false)
	if !ok {
		return
	}
	weekStr := r.URL.Query().Get("week")
	weekStart := currentWeekStart()
	if weekStr != "" {
		t, err := time.Parse("2006-01-02", weekStr)
		if err != nil {
			writeError(w, "week must be YYYY-MM-DD", http.StatusBadRequest)
			return
		}
		weekStart = t
	}
	priorWeekStart := weekStart.Add(-7 * 24 * time.Hour)

	members, err := h.listManagerMembers(userID, teamID, includeAdded(r))
	if err != nil {
		logServerError(r, "db error", err, http.StatusInternalServerError)
		writeError(w, "db error", http.StatusInternalServerError)
		return
	}

	eng := h.newEngine()

	type memberResp struct {
		Email             string                  `json:"email"`
		DisplayName       string                  `json:"display_name"`
		Source            string                  `json:"source"`
		Cadence           string                  `json:"cadence"`
		CadenceCustomDays *int                    `json:"cadence_custom_days"`
		LastOneOnOneAt    *time.Time              `json:"last_one_on_one_at"`
		IsPacedayUser     bool                    `json:"is_paceday_user"`
		ThisWeek          *engine.MemberWeekStats `json:"this_week"`
		LastWeek          *engine.MemberWeekStats `json:"last_week"`
		FocusTrendPct     float64                 `json:"focus_trend_pct"`
	}

	var result []memberResp
	for _, m := range members {
		var thisWeek, lastWeek *engine.MemberWeekStats
		isPacedayUser := false
		if m.Source != "added" { // PAC-129: no calendar read, nothing stored
			// PAC-130 (contract md §2.2): the entry's branch, and so its
			// is_paceday_user, is decided once, then both weeks are read on
			// it. PAC-122 SF-040 and PAC-130 D-9: a failed read is the 500,
			// never zeroes and never "not on Paceday".
			res, err := eng.MemberWeeks(r.Context(), userID, m, []time.Time{weekStart, priorWeekStart})
			if err != nil {
				writeServerError(w, r, "getTeam: member week", err, http.StatusInternalServerError)
				return
			}
			isPacedayUser = res.IsPacedayUser
			thisWeek, lastWeek = res.Weeks[0], res.Weeks[1]
		}
		if thisWeek == nil {
			thisWeek = &engine.MemberWeekStats{}
		}
		if lastWeek == nil {
			lastWeek = &engine.MemberWeekStats{}
		}
		result = append(result, memberResp{
			Email:             m.MemberEmail,
			DisplayName:       m.DisplayName,
			Source:            m.Source,
			Cadence:           m.Cadence,
			CadenceCustomDays: m.CadenceCustomDays,
			LastOneOnOneAt:    m.LastOneOnOneAt,
			IsPacedayUser:     isPacedayUser,
			ThisWeek:          thisWeek,
			LastWeek:          lastWeek,
			FocusTrendPct:     engine.TrendPct(thisWeek.FocusMinutes, lastWeek.FocusMinutes),
		})
	}
	if result == nil {
		result = []memberResp{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"team_id": teamID.String(),
		"members": result,
	})
}

func (h *managerHandlers) addMember(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	teamID, ok := h.managerTeamScope(w, r, userID, true)
	if !ok {
		return
	}
	var body struct {
		Email             string `json:"email"`
		DisplayName       string `json:"display_name"`
		Cadence           string `json:"cadence"`
		CadenceCustomDays *int   `json:"cadence_custom_days"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	email, err := normalizeManagerEmail(body.Email)
	if err != nil {
		writeError(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	if body.Cadence == "" {
		body.Cadence = "none"
	}
	body.Cadence = strings.ToLower(strings.TrimSpace(body.Cadence))
	if err := validateManagerCadence(body.Cadence, body.CadenceCustomDays); err != nil {
		writeError(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	displayName := strings.TrimSpace(body.DisplayName)
	if displayName == "" {
		displayName = strings.Split(email, "@")[0]
	}
	m := &storage.ManagerTeamMember{
		ManagerUserID:     userID,
		MemberEmail:       email,
		DisplayName:       displayName,
		Source:            "manual",
		Cadence:           body.Cadence,
		CadenceCustomDays: body.CadenceCustomDays,
	}
	// PAC-130 (spec D-2, contract md §3.1): the add never looks the address
	// up to find an account and stores no account link, so its answer and
	// its work are the same whether or not an account holds the address.
	if teamID != nil {
		err = storage.UpsertManagerTeamMemberForTeamCtx(r.Context(), h.db, *teamID, m)
	} else {
		err = storage.UpsertManagerTeamMemberCtx(r.Context(), h.db, m)
	}
	if err != nil {
		logServerError(r, "db error", err, http.StatusInternalServerError)
		writeError(w, "db error", http.StatusInternalServerError)
		return
	}
	// Answer with the stored row, through the team view when scoped (PAC-26,
	// API-031, D-4): the pre-insert struct has a zero id and zero timestamps.
	stored, err := readMemberInScope(h.db, userID, teamID, email)
	if err != nil {
		logServerError(r, "db error", err, http.StatusInternalServerError)
		writeError(w, "db error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(withoutAccountLink(stored))
}

func (h *managerHandlers) deleteMember(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	email := managerEmailParam(r)
	raw := strings.TrimSpace(r.URL.Query().Get("team_id"))
	if raw == "" {
		if err := storage.DeleteManagerTeamMemberByEmailCtx(r.Context(), h.db, userID, email); err != nil {
			writeServerError(w, r, "remove member", err, http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	teamID, err := uuid.Parse(raw)
	if err != nil {
		writeError(w, "team_id must be a valid UUID", http.StatusBadRequest)
		return
	}
	// PAC-129 (contract §5.3, D-8): one transaction under the team lock, with
	// the owner check, the carrier lookup (PAC-71 revoking site: every account
	// carrying the address, at any standing, in any letter case) and the
	// membership reads inside it; then the memberships, this team's
	// assignment, the added entry and every invitation of the team for the
	// address (P3-m1: matched on lower(TrimAddressSpace(path))).
	err = storage.RemoveManagerTeamPersonCtx(r.Context(), h.db, userID, teamID, email)
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, storage.ErrNotTeamOwner):
		writeError(w, "forbidden", http.StatusForbidden)
	case errors.Is(err, storage.ErrLastOwner):
		writeError(w, "the team owner cannot be removed", http.StatusUnprocessableEntity)
	default:
		writeServerError(w, r, "remove member", err, http.StatusInternalServerError)
	}
}

func (h *managerHandlers) patchMember(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	teamID, ok := h.managerTeamScope(w, r, userID, true)
	if !ok {
		return
	}
	email := managerEmailParam(r)
	var body struct {
		DisplayName       *string `json:"display_name"`
		Cadence           *string `json:"cadence"`
		CadenceCustomDays *int    `json:"cadence_custom_days"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	m, err := h.getOrCreateScopedMember(r.Context(), userID, teamID, email)
	if errors.Is(err, errAddedWithoutRoster) {
		writeError(w, msgAddedWithoutRoster, http.StatusUnprocessableEntity)
		return
	}
	if err == sql.ErrNoRows {
		http.Error(w, "not found", http.StatusNotFound)
		return
	} else if err != nil {
		logServerError(r, "db error", err, http.StatusInternalServerError)
		writeError(w, "db error", http.StatusInternalServerError)
		return
	}
	displayName := m.DisplayName
	cadence := m.Cadence
	if body.DisplayName != nil {
		displayName = *body.DisplayName
	}
	if body.Cadence != nil {
		cadence = strings.ToLower(strings.TrimSpace(*body.Cadence))
	}
	customDays := m.CadenceCustomDays
	if body.CadenceCustomDays != nil {
		customDays = body.CadenceCustomDays
	}
	if body.Cadence != nil && cadence != "custom" {
		customDays = nil
	}
	if err := validateManagerCadence(cadence, customDays); err != nil {
		writeError(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		displayName = strings.Split(m.MemberEmail, "@")[0]
	}
	if teamID == nil {
		err = storage.PatchManagerTeamMemberCtx(r.Context(), h.db, userID, email, displayName, cadence, customDays)
	} else {
		err = storage.PatchManagerTeamMemberForTeamCtx(r.Context(), h.db, userID, *teamID, email, displayName, cadence, customDays)
	}
	if err != nil {
		logServerError(r, "db error", err, http.StatusInternalServerError)
		writeError(w, "db error", http.StatusInternalServerError)
		return
	}
	// A failed re-read is a 500, never 200 null (PAC-26, API-032, D-3).
	updated, err := readMemberInScope(h.db, userID, teamID, email)
	if err != nil {
		logServerError(r, "db error", err, http.StatusInternalServerError)
		writeError(w, "db error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(withoutAccountLink(updated))
}

// ── Cadence gaps ──────────────────────────────────────────────────────────────

func (h *managerHandlers) getGaps(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	teamID, ok := h.managerTeamScope(w, r, userID, false)
	if !ok {
		return
	}
	// Compute over the roster in scope, so a team's own members and cadences
	// are what count (PAC-54).
	members, err := h.listManagerMembers(userID, teamID, false)
	if err != nil {
		logServerError(r, "db error", err, http.StatusInternalServerError)
		writeError(w, "db error", http.StatusInternalServerError)
		return
	}
	gaps, err := h.newEngine().GetGaps(r.Context(), userID, members)
	if err != nil {
		writeServerError(w, r, "gap detection error", err, http.StatusInternalServerError)
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
	teamID, ok := h.managerTeamScope(w, r, userID, false)
	if !ok {
		return
	}
	email := managerEmailParam(r)

	var body struct {
		SuggestedDate string `json:"suggested_date"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
		writeError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if body.SuggestedDate != "" {
		if _, err := time.Parse("2006-01-02", body.SuggestedDate); err != nil {
			writeError(w, "suggested_date must be YYYY-MM-DD", http.StatusUnprocessableEntity)
			return
		}
	}

	m, err := h.getOrCreateScopedMember(r.Context(), userID, teamID, email)
	if errors.Is(err, errAddedWithoutRoster) {
		writeError(w, msgAddedWithoutRoster, http.StatusUnprocessableEntity)
		return
	}
	if err == sql.ErrNoRows {
		http.Error(w, "not found", http.StatusNotFound)
		return
	} else if err != nil {
		logServerError(r, "db error", err, http.StatusInternalServerError)
		writeError(w, "db error", http.StatusInternalServerError)
		return
	}

	q := url.Values{}
	q.Set("title", fmt.Sprintf("1:1 with %s", m.DisplayName))
	q.Set("attendees", email)
	q.Set("duration", "30")
	if body.SuggestedDate != "" {
		q.Set("date", body.SuggestedDate)
	}
	prefillURL := "/app?" + q.Encode()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"prefill_url": prefillURL})
}

// ── Analytics ────────────────────────────────────────────────────────────────

func (h *managerHandlers) getAnalytics(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	teamID, ok := h.managerTeamScope(w, r, userID, false)
	if !ok {
		return
	}
	weekStr := r.URL.Query().Get("week")
	weekStart := currentWeekStart()
	if weekStr != "" {
		t, err := time.Parse("2006-01-02", weekStr)
		if err != nil {
			writeError(w, "week must be YYYY-MM-DD", http.StatusBadRequest)
			return
		}
		weekStart = t
	}
	months := 3
	members, err := h.listManagerMembers(userID, teamID, teamID != nil && includeAdded(r))
	if err != nil {
		logServerError(r, "db error", err, http.StatusInternalServerError)
		writeError(w, "db error", http.StatusInternalServerError)
		return
	}

	type weekData struct {
		WeekStart      string `json:"week_start"`
		FocusMinutes   int    `json:"focus_minutes"`
		MeetingMinutes int    `json:"meeting_minutes"`
		FreeMinutes    int    `json:"free_minutes"`
		DataAvailable  bool   `json:"data_available"`
	}
	type memberAnalytics struct {
		Email         string     `json:"email"`
		Name          string     `json:"display_name"`
		IsPacedayUser bool       `json:"is_paceday_user"`
		DataAvailable bool       `json:"data_available"`
		Weeks         []weekData `json:"weeks"`
	}
	_ = months

	eng := h.newEngine()
	var result []memberAnalytics
	numWeeks := months * 4
	weekStarts := make([]time.Time, numWeeks)
	for i := range weekStarts {
		weekStarts[i] = weekStart.Add(time.Duration(-i*7) * 24 * time.Hour)
	}
	for _, m := range members {
		weeks := make([]weekData, 0, numWeeks)
		anyMeasured := false
		isPacedayUser := false
		var stored []*engine.MemberWeekStats
		if m.Source != "added" { // PAC-129: unmeasured, team_analytics_cache never written
			// PAC-130 (contract md §2.2): one branch per entry, decided
			// before its weeks are read. PAC-122 SF-040 and PAC-130 D-9: a
			// failed read is the 500, never "no data" or "not on Paceday".
			res, err := eng.MemberWeeks(r.Context(), userID, m, weekStarts)
			if err != nil {
				writeServerError(w, r, "getAnalytics: member week", err, http.StatusInternalServerError)
				return
			}
			isPacedayUser = res.IsPacedayUser
			stored = res.Weeks
		}
		for i, ws := range weekStarts {
			var stats *engine.MemberWeekStats
			if i < len(stored) {
				stats = stored[i]
			}
			if stats == nil {
				stats = &engine.MemberWeekStats{}
			}
			anyMeasured = anyMeasured || stats.DataAvailable
			weeks = append(weeks, weekData{
				WeekStart:      ws.Format("2006-01-02"),
				FocusMinutes:   stats.FocusMinutes,
				MeetingMinutes: stats.MeetingMinutes,
				FreeMinutes:    stats.FreeMinutes,
				DataAvailable:  stats.DataAvailable,
			})
		}
		// is_paceday_user matches the roster's derivation (PAC-130: visible on
		// Paceday to the caller); data_available is any(weeks[].data_available),
		// so it cannot disagree with the buckets (PAC-34 D-1).
		result = append(result, memberAnalytics{
			Email:         m.MemberEmail,
			Name:          m.DisplayName,
			IsPacedayUser: isPacedayUser,
			DataAvailable: anyMeasured,
			Weeks:         weeks,
		})
	}
	if result == nil {
		result = []memberAnalytics{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"members": result})
}

// ── helpers ───────────────────────────────────────────────────────────────────

func (h *managerHandlers) requiredManagerTeamScope(w http.ResponseWriter, r *http.Request, userID uuid.UUID, requireOwner bool) (*uuid.UUID, bool) {
	if strings.TrimSpace(r.URL.Query().Get("team_id")) == "" {
		writeError(w, "team_id is required", http.StatusBadRequest)
		return nil, false
	}
	return h.managerTeamScope(w, r, userID, requireOwner)
}

func (h *managerHandlers) managerTeamScope(w http.ResponseWriter, r *http.Request, userID uuid.UUID, requireOwner bool) (*uuid.UUID, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("team_id"))
	if raw == "" {
		return nil, true
	}
	teamID, err := uuid.Parse(raw)
	if err != nil {
		writeError(w, "team_id must be a valid UUID", http.StatusBadRequest)
		return nil, false
	}
	// PAC-122 SF-074: only "no membership row" or a non-owner role (when
	// ownership is required) is the 403; a failed read is the 500.
	membership, err := storage.GetTeamMember(h.db, teamID, userID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		writeServerError(w, r, "manager team scope: membership", err, http.StatusInternalServerError)
		return nil, false
	}
	if err != nil || (requireOwner && membership.Role != "owner") {
		writeError(w, "forbidden", http.StatusForbidden)
		return nil, false
	}
	return &teamID, true
}

func (h *managerHandlers) listManagerMembers(userID uuid.UUID, teamID *uuid.UUID, withAdded bool) ([]*storage.ManagerTeamMember, error) {
	if teamID == nil {
		return storage.ListManagerTeamMembers(h.db, userID)
	}
	assigned, err := storage.ListManagerTeamMembersForTeam(h.db, userID, *teamID)
	if err != nil {
		return nil, err
	}
	byEmail := make(map[string]*storage.ManagerTeamMember, len(assigned))
	for _, member := range assigned {
		byEmail[strings.ToLower(member.MemberEmail)] = member
	}
	formalMembers, err := storage.ListTeamMembers(h.db, *teamID)
	if err != nil {
		return nil, err
	}
	for _, formal := range formalMembers {
		email := strings.ToLower(strings.TrimSpace(formal.Email))
		if email == "" || formal.UserID == userID {
			continue
		}
		if _, exists := byEmail[email]; exists {
			continue
		}
		// The formal entry names its account in memory, from the team
		// membership, never from a stored link: the engine reads it for this
		// source only (PAC-130 contract md §2.2 step 3).
		memberUserID := formal.UserID
		displayName := strings.TrimSpace(formal.Name)
		if displayName == "" {
			displayName = strings.Split(email, "@")[0]
		}
		byEmail[email] = &storage.ManagerTeamMember{
			ManagerUserID: userID,
			MemberEmail:   email,
			MemberUserID:  &memberUserID,
			DisplayName:   displayName,
			Source:        "formal",
			Cadence:       "none",
		}
	}
	// PAC-129 (contract §5.1): with include_added, the team's added people
	// with no assignment and no formal membership, read-only: no account
	// signal (MemberUserID nil), cadence none, never measured or stored.
	if withAdded {
		added, err := storage.ListAddedMembers(h.db, *teamID)
		if err != nil {
			return nil, err
		}
		for _, a := range added {
			if _, exists := byEmail[a.Email]; exists {
				continue
			}
			displayName := strings.TrimSpace(a.DisplayName)
			if displayName == "" {
				displayName = strings.Split(a.Email, "@")[0]
			}
			byEmail[a.Email] = &storage.ManagerTeamMember{
				ManagerUserID: userID,
				MemberEmail:   a.Email,
				DisplayName:   displayName,
				Source:        "added",
				Cadence:       "none",
			}
		}
	}
	result := make([]*storage.ManagerTeamMember, 0, len(byEmail))
	for _, member := range byEmail {
		result = append(result, member)
	}
	sort.Slice(result, func(i, j int) bool {
		return strings.ToLower(result[i].DisplayName) < strings.ToLower(result[j].DisplayName)
	})
	return result, nil
}

func (h *managerHandlers) getOrCreateScopedMember(ctx context.Context, userID uuid.UUID, teamID *uuid.UUID, email string) (*storage.ManagerTeamMember, error) {
	if teamID == nil {
		return storage.GetManagerTeamMemberByEmail(h.db, userID, email)
	}
	member, err := storage.GetManagerTeamMemberByEmailForTeam(h.db, userID, *teamID, email)
	if err == nil || err != sql.ErrNoRows {
		return member, err
	}
	formalMembers, err := storage.ListTeamMembers(h.db, *teamID)
	if err != nil {
		return nil, err
	}
	for _, formal := range formalMembers {
		if !strings.EqualFold(formal.Email, email) || formal.UserID == userID {
			continue
		}
		// PAC-71 granting site: a roster entry points only at a formal member
		// who holds the address domain-confirmed.
		if ok, herr := storage.HoldsConfirmed(h.db, formal.UserID, email); herr != nil || !ok {
			continue
		}
		// PAC-130 (spec D-6, contract md §3.2): the row and assignment are
		// written with no account link; the entry check above stays.
		displayName := strings.TrimSpace(formal.Name)
		if displayName == "" {
			displayName = strings.Split(email, "@")[0]
		}
		member = &storage.ManagerTeamMember{
			ManagerUserID: userID, MemberEmail: strings.ToLower(email),
			DisplayName: displayName, Source: "manual", Cadence: "none",
		}
		if err := storage.UpsertManagerTeamMemberForTeamCtx(ctx, h.db, *teamID, member); err != nil {
			return nil, err
		}
		return member, nil
	}
	// PAC-129 (contract §5.2, D-13): a person added to the team with no roster
	// entry on it is read-only here; nothing is written for them.
	if _, err := storage.GetAddedMember(h.db, *teamID, email); err == nil {
		return nil, errAddedWithoutRoster
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return nil, sql.ErrNoRows
}

// withoutAccountLink is a stored roster row as the add and patch answer it:
// member_user_id is always null (PAC-130 spec D-4, contract md §3.1), even
// for a link migration 042 kept, which nothing reads or answers any more.
func withoutAccountLink(m *storage.ManagerTeamMember) *storage.ManagerTeamMember {
	out := *m
	out.MemberUserID = nil
	return &out
}

// errAddedWithoutRoster is the patch and schedule 422 for a person added to
// the team who has no roster entry on it (PAC-129 spec §3.9, AC-31).
var errAddedWithoutRoster = errors.New(msgAddedWithoutRoster)

const msgAddedWithoutRoster = "this person was added to the team without Paceday access; add them with Add person to track 1:1s"

// includeAdded reads include_added: only "true" is true (PAC-129).
func includeAdded(r *http.Request) bool {
	return r.URL.Query().Get("include_added") == "true"
}

func currentWeekStart() time.Time {
	now := time.Now().UTC()
	weekday := int(now.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	monday := now.Add(-time.Duration(weekday-1) * 24 * time.Hour)
	return time.Date(monday.Year(), monday.Month(), monday.Day(), 0, 0, 0, 0, time.UTC)
}

// normalizeManagerEmail is the one address normaliser (PAC-129 contract §3.0):
// trim the address set of contract §2.2 (storage.TrimAddressSpace), lowercase,
// then net/mail.ParseAddress, whose address must equal the result. Manager
// add, detection confirm, inviteTeamMember, addTeamPerson, removeTeamPerson
// and the Manager {email} paths use it, and storage writes the same form.
func normalizeManagerEmail(raw string) (string, error) {
	email := strings.ToLower(storage.TrimAddressSpace(raw))
	if email == "" {
		return "", fmt.Errorf("email is required")
	}
	parsed, err := mail.ParseAddress(email)
	if err != nil || parsed.Address != email {
		return "", fmt.Errorf("email must be a valid email address")
	}
	return email, nil
}

func managerEmailParam(r *http.Request) string {
	raw := chi.URLParam(r, "email")
	decoded, err := url.PathUnescape(raw)
	if err != nil {
		return raw
	}
	return decoded
}
func validateManagerCadence(cadence string, customDays *int) error {
	switch cadence {
	case "weekly", "biweekly", "monthly", "none":
		if customDays != nil {
			return fmt.Errorf("custom_cadence_days is only valid with custom cadence")
		}
	case "custom":
		if customDays == nil || *customDays < 1 || *customDays > 365 {
			return fmt.Errorf("custom_cadence_days must be between 1 and 365")
		}
	default:
		return fmt.Errorf("cadence must be weekly, biweekly, monthly, custom, or none")
	}
	return nil
}
