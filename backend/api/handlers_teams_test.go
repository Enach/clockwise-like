package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/storage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// setupTeamRoutes creates a chi router with team routes wired up against the shared test DB.
func setupTeamRoutes(t *testing.T) *chi.Mux {
	t.Helper()
	db := openTestDB(t)
	th := newTeamHandlers(db, nil)
	r := chi.NewRouter()
	r.Post("/api/teams", th.createTeam)
	r.Get("/api/teams", th.listTeams)
	r.Get("/api/teams/invites/{token}", th.getInvite)
	r.Post("/api/teams/invites/{token}/accept", th.acceptInvite)
	r.Get("/api/teams/{id}", th.getTeam)
	r.Patch("/api/teams/{id}", th.patchTeam)
	r.Delete("/api/teams/{id}", th.deleteTeam)
	r.Post("/api/teams/{id}/members/invite", th.inviteMember)
	r.Delete("/api/teams/{id}/members/{userId}", th.removeMember)
	r.Post("/api/teams/{id}/no-meeting-zones", th.createZone)
	r.Get("/api/teams/{id}/no-meeting-zones", th.listZones)
	r.Delete("/api/teams/{id}/no-meeting-zones/{zoneId}", th.deleteZone)
	r.Get("/api/teams/{id}/availability", th.availability)
	r.Get("/api/teams/{id}/analytics", th.analyticsHandler)
	return r
}

// withUser injects a userID into a request context (simulates JWT middleware).
func withUser(r *http.Request, userID uuid.UUID) *http.Request {
	ctx := context.WithValue(r.Context(), auth.UserIDKey, userID)
	return r.WithContext(ctx)
}

// createTestUser inserts a minimal user row and returns its ID.
func createTestUser(t *testing.T, email string) uuid.UUID {
	t.Helper()
	db := openTestDB(t)
	var id uuid.UUID
	err := db.QueryRow(
		`INSERT INTO users (email, name) VALUES ($1, $2) ON CONFLICT (email) DO UPDATE SET name=EXCLUDED.name RETURNING id`,
		email, "Test User",
	).Scan(&id)
	if err != nil {
		t.Fatalf("create test user: %v", err)
	}
	return id
}

func TestCreateTeam_Success(t *testing.T) {
	r := setupTeamRoutes(t)
	userID := createTestUser(t, "owner-team@example.com")

	body, _ := json.Marshal(map[string]string{"name": "Alpha Team"})
	req := httptest.NewRequest(http.MethodPost, "/api/teams", bytes.NewReader(body))
	req = withUser(req, userID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body: %s", w.Code, w.Body.String())
	}
	var team storage.Team
	if err := json.Unmarshal(w.Body.Bytes(), &team); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if team.Name != "Alpha Team" {
		t.Errorf("name = %q, want Alpha Team", team.Name)
	}
	if team.ID == uuid.Nil {
		t.Error("expected non-nil team ID")
	}
}

func TestCreateTeam_MissingName(t *testing.T) {
	r := setupTeamRoutes(t)
	userID := createTestUser(t, "owner2@example.com")

	body, _ := json.Marshal(map[string]string{"name": ""})
	req := httptest.NewRequest(http.MethodPost, "/api/teams", bytes.NewReader(body))
	req = withUser(req, userID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestCreateTeam_Unauthorized(t *testing.T) {
	r := setupTeamRoutes(t)
	body, _ := json.Marshal(map[string]string{"name": "X"})
	req := httptest.NewRequest(http.MethodPost, "/api/teams", bytes.NewReader(body))
	// No user in context
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestListTeams_Empty(t *testing.T) {
	r := setupTeamRoutes(t)
	userID := createTestUser(t, "lister@example.com")

	req := httptest.NewRequest(http.MethodGet, "/api/teams", nil)
	req = withUser(req, userID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var teams []storage.Team
	if err := json.Unmarshal(w.Body.Bytes(), &teams); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

func TestGetTeam_NotMember(t *testing.T) {
	r := setupTeamRoutes(t)
	ownerID := createTestUser(t, "owner3@example.com")
	otherID := createTestUser(t, "other@example.com")

	// Create a team as owner.
	db := openTestDB(t)
	team, err := storage.CreateTeam(db, "Secret Team", ownerID)
	if err != nil {
		t.Fatalf("create team: %v", err)
	}
	_ = storage.AddTeamMember(db, team.ID, ownerID, "owner")

	req := httptest.NewRequest(http.MethodGet, "/api/teams/"+team.ID.String(), nil)
	req = withUser(req, otherID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestPatchTeam_OwnerCanRename(t *testing.T) {
	r := setupTeamRoutes(t)
	ownerID := createTestUser(t, "renamer@example.com")

	db := openTestDB(t)
	team, err := storage.CreateTeam(db, "Old Name", ownerID)
	if err != nil {
		t.Fatalf("create team: %v", err)
	}
	_ = storage.AddTeamMember(db, team.ID, ownerID, "owner")

	body, _ := json.Marshal(map[string]string{"name": "New Name"})
	req := httptest.NewRequest(http.MethodPatch, "/api/teams/"+team.ID.String(), bytes.NewReader(body))
	req = withUser(req, ownerID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
}

func TestDeleteTeam_OwnerOnly(t *testing.T) {
	r := setupTeamRoutes(t)
	ownerID := createTestUser(t, "deleter@example.com")
	memberID := createTestUser(t, "member-del@example.com")

	db := openTestDB(t)
	team, err := storage.CreateTeam(db, "Temp Team", ownerID)
	if err != nil {
		t.Fatalf("create team: %v", err)
	}
	_ = storage.AddTeamMember(db, team.ID, ownerID, "owner")
	_ = storage.AddTeamMember(db, team.ID, memberID, "member")

	// Member cannot delete.
	req := httptest.NewRequest(http.MethodDelete, "/api/teams/"+team.ID.String(), nil)
	req = withUser(req, memberID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("member delete: status = %d, want 403", w.Code)
	}

	// Owner can delete.
	req = httptest.NewRequest(http.MethodDelete, "/api/teams/"+team.ID.String(), nil)
	req = withUser(req, ownerID)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Errorf("owner delete: status = %d, want 204", w.Code)
	}
}

func TestNoMeetingZones_CRUD(t *testing.T) {
	r := setupTeamRoutes(t)
	ownerID := createTestUser(t, "zone-owner@example.com")

	db := openTestDB(t)
	team, _ := storage.CreateTeam(db, "Zone Team", ownerID)
	_ = storage.AddTeamMember(db, team.ID, ownerID, "owner")

	// Create zone.
	body, _ := json.Marshal(map[string]interface{}{
		"dayOfWeek": 5,
		"startTime": "14:00",
		"endTime":   "17:00",
		"label":     "Focus Friday",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/teams/"+team.ID.String()+"/no-meeting-zones", bytes.NewReader(body))
	req = withUser(req, ownerID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("create zone: status = %d, want 201; body: %s", w.Code, w.Body.String())
	}
	var zone storage.NoMeetingZone
	_ = json.Unmarshal(w.Body.Bytes(), &zone)
	if zone.ID == uuid.Nil {
		t.Fatal("expected zone ID")
	}

	// List zones.
	req = httptest.NewRequest(http.MethodGet, "/api/teams/"+team.ID.String()+"/no-meeting-zones", nil)
	req = withUser(req, ownerID)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("list zones: status = %d", w.Code)
	}

	// Delete zone.
	req = httptest.NewRequest(http.MethodDelete, "/api/teams/"+team.ID.String()+"/no-meeting-zones/"+zone.ID.String(), nil)
	req = withUser(req, ownerID)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Errorf("delete zone: status = %d", w.Code)
	}
}

func TestAvailability_InvalidDate(t *testing.T) {
	r := setupTeamRoutes(t)
	ownerID := createTestUser(t, "avail@example.com")

	db := openTestDB(t)
	team, _ := storage.CreateTeam(db, "Avail Team", ownerID)
	_ = storage.AddTeamMember(db, team.ID, ownerID, "owner")

	req := httptest.NewRequest(http.MethodGet, "/api/teams/"+team.ID.String()+"/availability?date=notadate", nil)
	req = withUser(req, ownerID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestAvailability_ValidDate(t *testing.T) {
	r := setupTeamRoutes(t)
	ownerID := createTestUser(t, "avail2@example.com")

	db := openTestDB(t)
	team, _ := storage.CreateTeam(db, "Avail Team 2", ownerID)
	_ = storage.AddTeamMember(db, team.ID, ownerID, "owner")

	date := time.Now().AddDate(0, 0, 1).Format("2006-01-02")
	req := httptest.NewRequest(http.MethodGet, "/api/teams/"+team.ID.String()+"/availability?date="+date+"&duration=60", nil)
	req = withUser(req, ownerID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	// Returns 200 with slots (may be empty, cal not connected).
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
}

func TestAnalytics_ReturnsOK(t *testing.T) {
	r := setupTeamRoutes(t)
	ownerID := createTestUser(t, "analytics@example.com")

	db := openTestDB(t)
	team, _ := storage.CreateTeam(db, "Analytics Team", ownerID)
	_ = storage.AddTeamMember(db, team.ID, ownerID, "owner")

	req := httptest.NewRequest(http.MethodGet, "/api/teams/"+team.ID.String()+"/analytics?date=2024-01-01", nil)
	req = withUser(req, ownerID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
}

func TestGetInvite_NotFound(t *testing.T) {
	r := setupTeamRoutes(t)

	req := httptest.NewRequest(http.MethodGet, "/api/teams/invites/nonexistent-token", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

type fakeTeamAvailabilityEngine struct {
	slots          []engine.TeamSlot
	slotCalls      int
	analyticsCalls int
	lastDay        time.Time
	lastDuration   int
	lastWeekStart  time.Time
}

func (f *fakeTeamAvailabilityEngine) FindSlots(_ context.Context, _ uuid.UUID, day time.Time, durationMinutes int) ([]engine.TeamSlot, error) {
	f.slotCalls++
	f.lastDay = day
	f.lastDuration = durationMinutes
	return f.slots, nil
}

func (f *fakeTeamAvailabilityEngine) GetTeamAnalytics(_ context.Context, _ uuid.UUID, weekStart time.Time) (*engine.TeamAnalytics, error) {
	f.analyticsCalls++
	f.lastWeekStart = weekStart
	return &engine.TeamAnalytics{MemberBreakdown: []engine.MemberAnalyticsSummary{}}, nil
}

func setupTeamAvailabilityRoutes(t *testing.T, availability TeamAvailability) *chi.Mux {
	t.Helper()
	h := newTeamHandlersWithAvailabilityEngine(openTestDB(t), availability)
	r := chi.NewRouter()
	r.Get("/api/teams/{id}/availability", h.availability)
	r.Get("/api/teams/{id}/analytics", h.analyticsHandler)
	return r
}

func TestTeamAvailability_UsesInjectedEngineAndQuery(t *testing.T) {
	fake := &fakeTeamAvailabilityEngine{
		slots: []engine.TeamSlot{{Start: time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC), End: time.Date(2026, 9, 14, 10, 30, 0, 0, time.UTC), QualityScore: 90}},
	}
	r := setupTeamAvailabilityRoutes(t, fake)
	ownerID := createTestUser(t, "availability-contract@example.com")
	db := openTestDB(t)
	team, err := storage.CreateTeam(db, "Availability Contract", ownerID)
	if err != nil {
		t.Fatalf("create team: %v", err)
	}
	if err := storage.AddTeamMember(db, team.ID, ownerID, "owner"); err != nil {
		t.Fatalf("add owner: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/teams/"+team.ID.String()+"/availability?date=2026-09-14&duration=30", nil)
	req = withUser(req, ownerID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
	}
	if fake.slotCalls != 1 || fake.lastDuration != 30 || fake.lastDay.Format("2006-01-02") != "2026-09-14" {
		t.Fatalf("engine call = %d/%s/%d", fake.slotCalls, fake.lastDay.Format("2006-01-02"), fake.lastDuration)
	}
}

func TestNoMeetingZone_ValidatesAndConvertsWeekdayAtBoundary(t *testing.T) {
	r := setupTeamRoutes(t)
	ownerID := createTestUser(t, "zone-contract@example.com")
	db := openTestDB(t)
	team, err := storage.CreateTeam(db, "Zone Contract", ownerID)
	if err != nil {
		t.Fatalf("create team: %v", err)
	}
	if err := storage.AddTeamMember(db, team.ID, ownerID, "owner"); err != nil {
		t.Fatalf("add owner: %v", err)
	}
	path := "/api/teams/" + team.ID.String() + "/no-meeting-zones"

	for name, input := range map[string]any{
		"weekday zero":  map[string]any{"dayOfWeek": 0, "startTime": "09:00", "endTime": "10:00"},
		"inverted time": map[string]any{"dayOfWeek": 1, "startTime": "10:00", "endTime": "09:00"},
	} {
		t.Run(name, func(t *testing.T) {
			encoded, _ := json.Marshal(input)
			req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(encoded))
			req = withUser(req, ownerID)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
			}
		})
	}

	encoded, _ := json.Marshal(map[string]any{"dayOfWeek": 7, "startTime": "09:00", "endTime": "10:00", "label": "Sunday"})
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(encoded))
	req = withUser(req, ownerID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("valid zone: status = %d; body: %s", w.Code, w.Body.String())
	}
	var returned storage.NoMeetingZone
	if err := json.Unmarshal(w.Body.Bytes(), &returned); err != nil {
		t.Fatalf("decode zone: %v", err)
	}
	if returned.DayOfWeek != 7 {
		t.Fatalf("response weekday = %d, want 7", returned.DayOfWeek)
	}
	zones, err := storage.ListNoMeetingZones(db, team.ID)
	if err != nil || len(zones) != 1 {
		t.Fatalf("stored zones = %d, err=%v", len(zones), err)
	}
	if zones[0].DayOfWeek != 0 {
		t.Fatalf("stored weekday = %d, want legacy Sunday=0", zones[0].DayOfWeek)
	}
}

func TestListTeams_ReturnsAllMemberships(t *testing.T) {
	r := setupTeamRoutes(t)
	userID := createTestUser(t, "multi-team@example.com")
	db := openTestDB(t)

	first, err := storage.CreateTeam(db, "Alpha", userID)
	if err != nil {
		t.Fatalf("create first team: %v", err)
	}
	second, err := storage.CreateTeam(db, "Beta", userID)
	if err != nil {
		t.Fatalf("create second team: %v", err)
	}
	if err := storage.AddTeamMember(db, first.ID, userID, "owner"); err != nil {
		t.Fatalf("add first membership: %v", err)
	}
	if err := storage.AddTeamMember(db, second.ID, userID, "owner"); err != nil {
		t.Fatalf("add second membership: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/teams", nil)
	req = withUser(req, userID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d; body: %s", w.Code, w.Body.String())
	}

	var teams []storage.Team
	if err := json.NewDecoder(w.Body).Decode(&teams); err != nil {
		t.Fatalf("decode teams: %v", err)
	}
	if len(teams) != 2 {
		t.Fatalf("teams = %d, want 2", len(teams))
	}
	seen := map[uuid.UUID]bool{}
	for _, team := range teams {
		seen[team.ID] = true
	}
	if !seen[first.ID] || !seen[second.ID] {
		t.Fatalf("returned team IDs = %v, want both memberships", seen)
	}
}

// --- PAC-25: DELETE /api/teams/{id}/members/{userId} never leaves a team
// without an owner (spec AC-7..AC-13, AC-17, AC-19; D-3..D-6). ---

// pac25Team creates a team with the given owners and members, written directly
// to storage (the product cannot create a second owner; AC-13, AC-19).
func pac25Team(t *testing.T, name string, owners, members []uuid.UUID) uuid.UUID {
	t.Helper()
	db := openTestDB(t)
	team, err := storage.CreateTeam(db, name, owners[0])
	if err != nil {
		t.Fatalf("create team: %v", err)
	}
	for _, o := range owners {
		if err := storage.AddTeamMember(db, team.ID, o, "owner"); err != nil {
			t.Fatalf("add owner: %v", err)
		}
	}
	for _, m := range members {
		if err := storage.AddTeamMember(db, team.ID, m, "member"); err != nil {
			t.Fatalf("add member: %v", err)
		}
	}
	return team.ID
}

func pac25Remove(t *testing.T, r http.Handler, teamID, caller, target uuid.UUID) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, "/api/teams/"+teamID.String()+"/members/"+target.String(), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, withUser(req, caller))
	return w
}

// pac25Role reads the user's role in the team back from storage; "" means no row.
func pac25Role(t *testing.T, teamID, userID uuid.UUID) string {
	t.Helper()
	var role string
	err := openTestDB(t).QueryRow(`SELECT role FROM team_members WHERE team_id = $1 AND user_id = $2`, teamID, userID).Scan(&role)
	if err == sql.ErrNoRows {
		return ""
	}
	if err != nil {
		t.Fatalf("read role: %v", err)
	}
	return role
}

func pac25MembershipSnapshot(t *testing.T, teamID uuid.UUID) string {
	t.Helper()
	rows, err := openTestDB(t).Query(`SELECT user_id, role, joined_at FROM team_members WHERE team_id = $1 ORDER BY user_id`, teamID)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var u uuid.UUID
		var role string
		var joined time.Time
		if err := rows.Scan(&u, &role, &joined); err != nil {
			t.Fatalf("scan snapshot: %v", err)
		}
		b.WriteString(u.String() + "|" + role + "|" + joined.UTC().Format(time.RFC3339Nano) + "\n")
	}
	return b.String()
}

func assertLastOwnerRefused(t *testing.T, what string, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("%s: status = %d, want 422; body: %s", what, w.Code, w.Body.String())
	}
	exactJSONError(t, w, "the team owner cannot be removed")
}

// AC-7: the only owner removing themself is refused and nothing changes.
func TestPAC25SoleOwnerSelfRemovalIs422(t *testing.T) {
	r := setupTeamRoutes(t)
	owner := createTestUser(t, "pac25-ac7-owner@example.com")
	member := createTestUser(t, "pac25-ac7-member@example.com")
	team := pac25Team(t, "PAC-25 AC-7", []uuid.UUID{owner}, []uuid.UUID{member})
	before := pac25MembershipSnapshot(t, team)
	assertLastOwnerRefused(t, "sole owner leaves", pac25Remove(t, r, team, owner, owner))
	if got := pac25Role(t, team, owner); got != "owner" {
		t.Fatalf("owner role after refusal = %q, want owner", got)
	}
	if after := pac25MembershipSnapshot(t, team); after != before {
		t.Fatalf("membership changed:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// AC-8: a sole owner who is also the only member is refused; the team survives.
func TestPAC25SoleOwnerAloneSelfRemovalIs422(t *testing.T) {
	r := setupTeamRoutes(t)
	owner := createTestUser(t, "pac25-ac8-owner@example.com")
	team := pac25Team(t, "PAC-25 AC-8", []uuid.UUID{owner}, nil)
	assertLastOwnerRefused(t, "sole member leaves", pac25Remove(t, r, team, owner, owner))
	if got := pac25Role(t, team, owner); got != "owner" {
		t.Fatalf("owner role after refusal = %q, want owner", got)
	}
	if _, err := storage.GetTeam(openTestDB(t), team); err != nil {
		t.Fatalf("team gone after refusal: %v", err)
	}
}

// AC-9 (pin): a member removes themself.
func TestPAC25MemberSelfRemovalIs204(t *testing.T) {
	r := setupTeamRoutes(t)
	owner := createTestUser(t, "pac25-ac9-owner@example.com")
	member := createTestUser(t, "pac25-ac9-member@example.com")
	team := pac25Team(t, "PAC-25 AC-9", []uuid.UUID{owner}, []uuid.UUID{member})
	if w := pac25Remove(t, r, team, member, member); w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body: %s", w.Code, w.Body.String())
	}
	if got := pac25Role(t, team, member); got != "" {
		t.Fatalf("member still has role %q", got)
	}
}

// AC-10 (pin): the owner removes a member.
func TestPAC25OwnerRemovesMemberIs204(t *testing.T) {
	r := setupTeamRoutes(t)
	owner := createTestUser(t, "pac25-ac10-owner@example.com")
	member := createTestUser(t, "pac25-ac10-member@example.com")
	team := pac25Team(t, "PAC-25 AC-10", []uuid.UUID{owner}, []uuid.UUID{member})
	if w := pac25Remove(t, r, team, owner, member); w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body: %s", w.Code, w.Body.String())
	}
	if got := pac25Role(t, team, member); got != "" {
		t.Fatalf("member still has role %q", got)
	}
}

// AC-11 (pin): a member cannot remove another member.
func TestPAC25MemberRemovesOtherMemberIs403(t *testing.T) {
	r := setupTeamRoutes(t)
	owner := createTestUser(t, "pac25-ac11-owner@example.com")
	m := createTestUser(t, "pac25-ac11-m@example.com")
	n := createTestUser(t, "pac25-ac11-n@example.com")
	team := pac25Team(t, "PAC-25 AC-11", []uuid.UUID{owner}, []uuid.UUID{m, n})
	w := pac25Remove(t, r, team, m, n)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body: %s", w.Code, w.Body.String())
	}
	if got := pac25Role(t, team, n); got != "member" {
		t.Fatalf("N role = %q, want member", got)
	}
}

// AC-12 (pin): removing a non-member is a 204 no-op.
func TestPAC25OwnerRemovesNonMemberIs204NoOp(t *testing.T) {
	r := setupTeamRoutes(t)
	owner := createTestUser(t, "pac25-ac12-owner@example.com")
	outsider := createTestUser(t, "pac25-ac12-outsider@example.com")
	team := pac25Team(t, "PAC-25 AC-12", []uuid.UUID{owner}, nil)
	before := pac25MembershipSnapshot(t, team)
	if w := pac25Remove(t, r, team, owner, outsider); w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body: %s", w.Code, w.Body.String())
	}
	if after := pac25MembershipSnapshot(t, team); after != before {
		t.Fatalf("membership changed:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// AC-13: with two owners the first may leave; the second, now last, may not.
func TestPAC25SecondOwnerMayLeaveLastMayNot(t *testing.T) {
	r := setupTeamRoutes(t)
	o1 := createTestUser(t, "pac25-ac13-o1@example.com")
	o2 := createTestUser(t, "pac25-ac13-o2@example.com")
	team := pac25Team(t, "PAC-25 AC-13", []uuid.UUID{o1, o2}, nil)
	if w := pac25Remove(t, r, team, o1, o1); w.Code != http.StatusNoContent {
		t.Fatalf("first owner leaves: status = %d, want 204; body: %s", w.Code, w.Body.String())
	}
	if got := pac25Role(t, team, o2); got != "owner" {
		t.Fatalf("O2 role = %q, want owner", got)
	}
	assertLastOwnerRefused(t, "last owner leaves", pac25Remove(t, r, team, o2, o2))
	if got := pac25Role(t, team, o2); got != "owner" {
		t.Fatalf("O2 role after refusal = %q, want owner", got)
	}
}

// AC-17 (pin): a member naming the owner is told 403, not 422, so the answer
// does not reveal who the owner is.
func TestPAC25MemberRemovesOwnerIs403Not422(t *testing.T) {
	r := setupTeamRoutes(t)
	owner := createTestUser(t, "pac25-ac17-owner@example.com")
	member := createTestUser(t, "pac25-ac17-member@example.com")
	team := pac25Team(t, "PAC-25 AC-17", []uuid.UUID{owner}, []uuid.UUID{member})
	w := pac25Remove(t, r, team, member, owner)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body: %s", w.Code, w.Body.String())
	}
	exactJSONError(t, w, "forbidden")
	if got := pac25Role(t, team, owner); got != "owner" {
		t.Fatalf("owner role = %q, want owner", got)
	}
}

// AC-19: two owners removing themselves at the same time never leave the team
// without an owner, and at most one of them is answered 204.
func TestPAC25ConcurrentOwnerSelfRemovalKeepsAnOwner(t *testing.T) {
	r := setupTeamRoutes(t)
	o1 := createTestUser(t, "pac25-ac19-o1@example.com")
	o2 := createTestUser(t, "pac25-ac19-o2@example.com")
	const attempts = 40
	for i := 0; i < attempts; i++ {
		team := pac25Team(t, fmt.Sprintf("PAC-25 AC-19 #%d", i), []uuid.UUID{o1, o2}, nil)
		start := make(chan struct{})
		codes := make([]int, 2)
		bodies := make([]string, 2)
		var wg sync.WaitGroup
		for j, who := range []uuid.UUID{o1, o2} {
			wg.Add(1)
			go func(j int, who uuid.UUID) {
				defer wg.Done()
				req := httptest.NewRequest(http.MethodDelete, "/api/teams/"+team.String()+"/members/"+who.String(), nil)
				w := httptest.NewRecorder()
				<-start
				r.ServeHTTP(w, withUser(req, who))
				codes[j], bodies[j] = w.Code, w.Body.String()
			}(j, who)
		}
		close(start)
		wg.Wait()
		var owners int
		if err := openTestDB(t).QueryRow(`SELECT count(*) FROM team_members WHERE team_id = $1 AND role = 'owner'`, team).Scan(&owners); err != nil {
			t.Fatalf("count owners: %v", err)
		}
		if owners < 1 {
			t.Fatalf("attempt %d: team left with %d owners; answers %v %q", i, owners, codes, bodies)
		}
		ok := 0
		for j, c := range codes {
			switch c {
			case http.StatusNoContent:
				ok++
			case http.StatusUnprocessableEntity:
				if strings.TrimSpace(bodies[j]) != `{"error":"the team owner cannot be removed"}` {
					t.Fatalf("attempt %d: 422 body %q", i, bodies[j])
				}
			default:
				t.Fatalf("attempt %d: unexpected answer %d %q", i, c, bodies[j])
			}
		}
		if ok != 1 {
			t.Fatalf("attempt %d: %d requests answered 204, want exactly 1 (answers %v)", i, ok, codes)
		}
	}
}
