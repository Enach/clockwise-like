package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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
	ctx := context.WithValue(r.Context(), ctxUserID, userID)
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
