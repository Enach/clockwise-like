package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/storage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type fakeManagerWorkflow struct {
	detectCalls int
	gaps        []engine.CadenceGap
}

func (f *fakeManagerWorkflow) DetectTeam(context.Context, uuid.UUID) (*engine.DetectResult, error) {
	f.detectCalls++
	return &engine.DetectResult{MembersAdded: 1, IsManager: true}, nil
}

func (f *fakeManagerWorkflow) GetGaps(context.Context, uuid.UUID) ([]engine.CadenceGap, error) {
	return f.gaps, nil
}

func (*fakeManagerWorkflow) GetMemberWeek(context.Context, uuid.UUID, *storage.ManagerTeamMember, time.Time) (*engine.MemberWeekStats, error) {
	return &engine.MemberWeekStats{DataAvailable: true}, nil
}

func setupManagerRoutes(t *testing.T, workflow ManagerWorkflow) *chi.Mux {
	t.Helper()
	h := newManagerHandlersWithEngineFactory(openTestDB(t), func() ManagerWorkflow { return workflow })
	r := chi.NewRouter()
	r.Get("/api/manager/profile", h.getProfile)
	r.Post("/api/manager/profile", h.postProfile)
	r.Post("/api/manager/detect", h.detect)
	r.Post("/api/manager/detect/confirm", h.confirmDetection)
	r.Get("/api/manager/team", h.getTeam)
	r.Post("/api/manager/team/members", h.addMember)
	r.Delete("/api/manager/team/members/{email}", h.deleteMember)
	r.Patch("/api/manager/team/members/{email}", h.patchMember)
	r.Get("/api/manager/gaps", h.getGaps)
	r.Post("/api/manager/team/members/{email}/schedule", h.scheduleMember)
	r.Get("/api/manager/analytics", h.getAnalytics)
	return r
}

func managerRequest(t *testing.T, r http.Handler, method, path string, userID uuid.UUID, body any) *httptest.ResponseRecorder {
	t.Helper()
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(payload))
	req = withUser(req, userID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestManagerAddMember_NormalizesAndValidatesContract(t *testing.T) {
	r := setupManagerRoutes(t, &fakeManagerWorkflow{})
	managerID := createTestUser(t, "manager-contract@example.com")

	w := managerRequest(t, r, http.MethodPost, "/api/manager/team/members", managerID, map[string]any{
		"email": "  MEMBER@Example.com ", "display_name": "", "cadence": "custom", "cadence_custom_days": 14,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("valid member: status = %d; body: %s", w.Code, w.Body.String())
	}

	member, err := storage.GetManagerTeamMemberByEmail(openTestDB(t), managerID, "member@example.com")
	if err != nil {
		t.Fatalf("read member: %v", err)
	}
	if member.DisplayName != "member" || member.Cadence != "custom" || member.CadenceCustomDays == nil || *member.CadenceCustomDays != 14 {
		t.Fatalf("stored member = %#v", member)
	}

	for name, input := range map[string]any{
		"bad email":       map[string]any{"email": "not-an-email", "cadence": "none"},
		"bad cadence":     map[string]any{"email": "other@example.com", "cadence": "daily"},
		"bad custom days": map[string]any{"email": "third@example.com", "cadence": "custom", "cadence_custom_days": 0},
	} {
		t.Run(name, func(t *testing.T) {
			w := managerRequest(t, r, http.MethodPost, "/api/manager/team/members", managerID, input)
			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestManagerPatchMember_PreservesOmittedCustomDays(t *testing.T) {
	r := setupManagerRoutes(t, &fakeManagerWorkflow{})
	managerID := createTestUser(t, "manager-patch@example.com")
	days := 21
	if err := storage.UpsertManagerTeamMember(openTestDB(t), &storage.ManagerTeamMember{
		ManagerUserID: managerID, MemberEmail: "report@example.com", DisplayName: "Report", Source: "manual",
		Cadence: "custom", CadenceCustomDays: &days,
	}); err != nil {
		t.Fatalf("insert member: %v", err)
	}

	w := managerRequest(t, r, http.MethodPatch, "/api/manager/team/members/report%40example.com", managerID, map[string]string{"display_name": "Updated"})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
	}
	member, err := storage.GetManagerTeamMemberByEmail(openTestDB(t), managerID, "report@example.com")
	if err != nil {
		t.Fatalf("read updated member: %v", err)
	}
	if member.CadenceCustomDays == nil || *member.CadenceCustomDays != days {
		t.Fatalf("custom days = %#v, want %d", member.CadenceCustomDays, days)
	}

	w = managerRequest(t, r, http.MethodPatch, "/api/manager/team/members/report%40example.com", managerID, map[string]string{"cadence": "weekly"})
	if w.Code != http.StatusOK {
		t.Fatalf("standard cadence: status = %d; body: %s", w.Code, w.Body.String())
	}
	member, err = storage.GetManagerTeamMemberByEmail(openTestDB(t), managerID, "report@example.com")
	if err != nil || member.CadenceCustomDays != nil {
		t.Fatalf("standard cadence custom days = %#v, err=%v", member.CadenceCustomDays, err)
	}
}

func TestManagerDetect_UsesInjectedWorkflow(t *testing.T) {
	fake := &fakeManagerWorkflow{}
	r := setupManagerRoutes(t, fake)
	managerID := createTestUser(t, "manager-detect@example.com")

	team := createManagerTestTeam(t, managerID, "Detect team")
	w := managerRequest(t, r, http.MethodPost, "/api/manager/detect?team_id="+team.ID.String(), managerID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
	}
	if fake.detectCalls != 1 {
		t.Fatalf("detect calls = %d, want 1", fake.detectCalls)
	}
}

func TestManagerTeam_InvalidWeekIsRejected(t *testing.T) {
	r := setupManagerRoutes(t, &fakeManagerWorkflow{})
	managerID := createTestUser(t, "manager-week@example.com")
	team := createManagerTestTeam(t, managerID, "Invalid week team")
	w := managerRequest(t, r, http.MethodGet, "/api/manager/team?team_id="+team.ID.String()+"&week=not-a-date", managerID, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
	}
}

func createManagerTestTeam(t *testing.T, ownerID uuid.UUID, name string) *storage.Team {
	t.Helper()
	db := openTestDB(t)
	team, err := storage.CreateTeam(db, name, ownerID)
	if err != nil {
		t.Fatalf("create team: %v", err)
	}
	if err := storage.AddTeamMember(db, team.ID, ownerID, "owner"); err != nil {
		t.Fatalf("add owner: %v", err)
	}
	return team
}

func managerTeamEmails(t *testing.T, body []byte) []string {
	t.Helper()
	var response struct {
		Members []struct {
			Email  string `json:"email"`
			Source string `json:"source"`
		} `json:"members"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode manager team: %v; body: %s", err, body)
	}
	emails := make([]string, 0, len(response.Members))
	for _, member := range response.Members {
		emails = append(emails, member.Email)
	}
	return emails
}

func TestManagerTeam_SelectedFormalTeamsHaveDistinctMembers(t *testing.T) {
	r := setupManagerRoutes(t, &fakeManagerWorkflow{})
	ownerID := createTestUser(t, "manager-scoped@example.com")
	alphaMemberID := createTestUser(t, "alpha-member@example.com")
	betaMemberID := createTestUser(t, "beta-member@example.com")
	alpha := createManagerTestTeam(t, ownerID, "Scoped Alpha")
	beta := createManagerTestTeam(t, ownerID, "Scoped Beta")
	db := openTestDB(t)
	if err := storage.AddTeamMember(db, alpha.ID, alphaMemberID, "member"); err != nil {
		t.Fatalf("add alpha member: %v", err)
	}
	if err := storage.AddTeamMember(db, beta.ID, betaMemberID, "member"); err != nil {
		t.Fatalf("add beta member: %v", err)
	}

	alphaResponse := managerRequest(t, r, http.MethodGet, "/api/manager/team?team_id="+alpha.ID.String(), ownerID, nil)
	if alphaResponse.Code != http.StatusOK {
		t.Fatalf("alpha status = %d; body: %s", alphaResponse.Code, alphaResponse.Body.String())
	}
	betaResponse := managerRequest(t, r, http.MethodGet, "/api/manager/team?team_id="+beta.ID.String(), ownerID, nil)
	if betaResponse.Code != http.StatusOK {
		t.Fatalf("beta status = %d; body: %s", betaResponse.Code, betaResponse.Body.String())
	}
	if got := managerTeamEmails(t, alphaResponse.Body.Bytes()); len(got) != 1 || got[0] != "alpha-member@example.com" {
		t.Fatalf("alpha members = %v", got)
	}
	if got := managerTeamEmails(t, betaResponse.Body.Bytes()); len(got) != 1 || got[0] != "beta-member@example.com" {
		t.Fatalf("beta members = %v", got)
	}
}

func TestManagerTeam_ScopeAuthorizationAndValidation(t *testing.T) {
	r := setupManagerRoutes(t, &fakeManagerWorkflow{})
	ownerID := createTestUser(t, "manager-auth-owner@example.com")
	memberID := createTestUser(t, "manager-auth-member@example.com")
	outsiderID := createTestUser(t, "manager-auth-outsider@example.com")
	team := createManagerTestTeam(t, ownerID, "Scoped Auth")
	if err := storage.AddTeamMember(openTestDB(t), team.ID, memberID, "member"); err != nil {
		t.Fatalf("add member: %v", err)
	}

	invalid := managerRequest(t, r, http.MethodGet, "/api/manager/team?team_id=invalid", ownerID, nil)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid team ID status = %d", invalid.Code)
	}
	forbiddenRead := managerRequest(t, r, http.MethodGet, "/api/manager/team?team_id="+team.ID.String(), outsiderID, nil)
	if forbiddenRead.Code != http.StatusForbidden {
		t.Fatalf("outsider read status = %d", forbiddenRead.Code)
	}
	forbiddenWrite := managerRequest(t, r, http.MethodPost, "/api/manager/team/members?team_id="+team.ID.String(), memberID, map[string]any{
		"email": "new-report@example.com", "cadence": "none",
	})
	if forbiddenWrite.Code != http.StatusForbidden {
		t.Fatalf("non-owner write status = %d", forbiddenWrite.Code)
	}
}

func TestManagerTeam_ScopedDeletePreservesOtherAssignmentAndHistory(t *testing.T) {
	r := setupManagerRoutes(t, &fakeManagerWorkflow{})
	ownerID := createTestUser(t, "manager-delete-scoped@example.com")
	alpha := createManagerTestTeam(t, ownerID, "Delete Alpha")
	beta := createManagerTestTeam(t, ownerID, "Delete Beta")
	member := &storage.ManagerTeamMember{
		ManagerUserID: ownerID, MemberEmail: "shared-report@example.com", DisplayName: "Shared Report",
		Source: "manual", Cadence: "weekly",
	}
	db := openTestDB(t)
	if err := storage.UpsertManagerTeamMemberForTeam(db, alpha.ID, member); err != nil {
		t.Fatalf("assign alpha: %v", err)
	}
	if err := storage.UpsertManagerTeamMemberForTeam(db, beta.ID, member); err != nil {
		t.Fatalf("assign beta: %v", err)
	}

	response := managerRequest(t, r, http.MethodDelete, "/api/manager/team/members/shared-report%40example.com?team_id="+alpha.ID.String(), ownerID, nil)
	if response.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d; body: %s", response.Code, response.Body.String())
	}
	if alphaMembers, err := storage.ListManagerTeamMembersForTeam(db, ownerID, alpha.ID); err != nil || len(alphaMembers) != 0 {
		t.Fatalf("alpha assignments = %d, err=%v", len(alphaMembers), err)
	}
	if betaMembers, err := storage.ListManagerTeamMembersForTeam(db, ownerID, beta.ID); err != nil || len(betaMembers) != 1 {
		t.Fatalf("beta assignments = %d, err=%v", len(betaMembers), err)
	}
	if _, err := storage.GetManagerTeamMemberByEmail(db, ownerID, member.MemberEmail); err != nil {
		t.Fatalf("global member history record was removed: %v", err)
	}
}

func TestManagerTeam_PatchFormalMemberCreatesScopedSettings(t *testing.T) {
	r := setupManagerRoutes(t, &fakeManagerWorkflow{})
	ownerID := createTestUser(t, "manager-patch-formal@example.com")
	memberID := createTestUser(t, "formal-report@example.com")
	team := createManagerTestTeam(t, ownerID, "Patch Formal")
	db := openTestDB(t)
	if err := storage.AddTeamMember(db, team.ID, memberID, "member"); err != nil {
		t.Fatalf("add formal member: %v", err)
	}

	response := managerRequest(t, r, http.MethodPatch, "/api/manager/team/members/formal-report%40example.com?team_id="+team.ID.String(), ownerID, map[string]string{"cadence": "weekly"})
	if response.Code != http.StatusOK {
		t.Fatalf("patch status = %d; body: %s", response.Code, response.Body.String())
	}
	member, err := storage.GetManagerTeamMemberByEmailForTeam(db, ownerID, team.ID, "formal-report@example.com")
	if err != nil {
		t.Fatalf("read scoped settings: %v", err)
	}
	if member.Cadence != "weekly" {
		t.Fatalf("cadence = %q, want weekly", member.Cadence)
	}
}

func TestManagerTeam_AddMemberCreatesOnlySelectedTeamAssignment(t *testing.T) {
	r := setupManagerRoutes(t, &fakeManagerWorkflow{})
	ownerID := createTestUser(t, "manager-add-scoped@example.com")
	team := createManagerTestTeam(t, ownerID, "Add Scoped")

	response := managerRequest(t, r, http.MethodPost, "/api/manager/team/members?team_id="+team.ID.String(), ownerID, map[string]any{
		"email": "scoped-report@example.com", "display_name": "Scoped Report", "cadence": "biweekly",
	})
	if response.Code != http.StatusCreated {
		t.Fatalf("add status = %d; body: %s", response.Code, response.Body.String())
	}
	db := openTestDB(t)
	member, err := storage.GetManagerTeamMemberByEmailForTeam(db, ownerID, team.ID, "scoped-report@example.com")
	if err != nil {
		t.Fatalf("read scoped member: %v", err)
	}
	if member.DisplayName != "Scoped Report" || member.Cadence != "biweekly" {
		t.Fatalf("scoped member = %#v", member)
	}
	var inviteCount int
	if err := db.QueryRow(`SELECT count(*) FROM team_invites WHERE team_id=$1 AND invitee_email=$2`, team.ID, member.MemberEmail).Scan(&inviteCount); err != nil {
		t.Fatalf("count invites: %v", err)
	}
	if inviteCount != 0 {
		t.Fatalf("implicit invites = %d, want 0", inviteCount)
	}
}

func TestManagerScheduleMember_ReturnsCanonicalAppPrefillURL(t *testing.T) {
	r := setupManagerRoutes(t, &fakeManagerWorkflow{})
	managerID := createTestUser(t, "manager-schedule-canonical@example.com")
	if err := storage.UpsertManagerTeamMember(openTestDB(t), &storage.ManagerTeamMember{
		ManagerUserID: managerID, MemberEmail: "milos.kojic@gorgias.com", DisplayName: "Milos Kojic",
		Source: "manual", Cadence: "monthly",
	}); err != nil {
		t.Fatalf("insert member: %v", err)
	}

	response := managerRequest(t, r, http.MethodPost, "/api/manager/team/members/milos.kojic%40gorgias.com/schedule", managerID, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("schedule status = %d; body: %s", response.Code, response.Body.String())
	}
	var body struct {
		PrefillURL string `json:"prefill_url"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	parsed, err := url.Parse(body.PrefillURL)
	if err != nil {
		t.Fatalf("parse prefill URL: %v", err)
	}
	if parsed.Path != "/app" {
		t.Fatalf("prefill path = %q, want /app", parsed.Path)
	}
	if parsed.Query().Get("attendees") != "milos.kojic@gorgias.com" || parsed.Query().Get("duration") != "30" || parsed.Query().Get("title") != "1:1 with Milos Kojic" {
		t.Fatalf("prefill query = %v", parsed.Query())
	}
}

func TestManagerDetect_PreviewDoesNotAssignEitherTeam(t *testing.T) {
	fake := &fakeManagerWorkflow{}
	r := setupManagerRoutes(t, fake)
	managerID := createTestUser(t, "manager-detect-scoped@example.com")
	selected := createManagerTestTeam(t, managerID, "Detected Selected")
	other := createManagerTestTeam(t, managerID, "Detected Other")
	db := openTestDB(t)
	for _, email := range []string{"first-detected@example.com", "second-detected@example.com"} {
		if err := storage.UpsertManagerTeamMember(db, &storage.ManagerTeamMember{
			ManagerUserID: managerID, MemberEmail: email, DisplayName: email,
			Source: "auto", Cadence: "weekly",
		}); err != nil {
			t.Fatalf("insert detected member %s: %v", email, err)
		}
	}

	response := managerRequest(t, r, http.MethodPost, "/api/manager/detect?team_id="+selected.ID.String(), managerID, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("detect status = %d; body: %s", response.Code, response.Body.String())
	}
	var result struct {
		Assigned int `json:"assigned"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode detection result: %v", err)
	}
	if result.Assigned != 0 {
		t.Fatalf("preview assigned = %d, want 0", result.Assigned)
	}
	selectedMembers, err := storage.ListManagerTeamMembersForTeam(db, managerID, selected.ID)
	if err != nil || len(selectedMembers) != 0 {
		t.Fatalf("preview assigned selected members = %d, err=%v", len(selectedMembers), err)
	}
	otherMembers, err := storage.ListManagerTeamMembersForTeam(db, managerID, other.ID)
	if err != nil || len(otherMembers) != 0 {
		t.Fatalf("other assignments = %d, err=%v", len(otherMembers), err)
	}
}
