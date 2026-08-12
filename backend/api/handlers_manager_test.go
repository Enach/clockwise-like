package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

	w := managerRequest(t, r, http.MethodPost, "/api/manager/detect", managerID, nil)
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
	w := managerRequest(t, r, http.MethodGet, "/api/manager/team?week=not-a-date", managerID, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
	}
}
