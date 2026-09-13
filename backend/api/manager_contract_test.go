package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

type pace023PreviewWorkflow struct {
	candidates []engine.DetectedCandidate
}

func (f *pace023PreviewWorkflow) DetectTeam(context.Context, uuid.UUID) (*engine.DetectResult, error) {
	return &engine.DetectResult{Candidates: f.candidates, ScannedAt: time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)}, nil
}
func (*pace023PreviewWorkflow) GetGaps(context.Context, uuid.UUID) ([]engine.CadenceGap, error) {
	return nil, nil
}
func (*pace023PreviewWorkflow) GetMemberWeek(context.Context, uuid.UUID, *storage.ManagerTeamMember, time.Time) (*engine.MemberWeekStats, error) {
	return &engine.MemberWeekStats{}, nil
}

func TestPACE023PreviewThenConfirmWireContract(t *testing.T) {
	manager := createTestUser(t, "pace023-preview-manager@example.com")
	team := createManagerTestTeam(t, manager, "PACE023 Preview")
	db := openTestDB(t)
	for _, item := range []engine.DetectedCandidate{
		{Email: "alpha@example.com", DisplayName: "Alpha"},
		{Email: "beta@example.com", DisplayName: "Beta"},
	} {
		if err := storage.UpsertManagerTeamMember(db, &storage.ManagerTeamMember{
			ManagerUserID: manager, MemberEmail: item.Email, DisplayName: item.DisplayName, Source: "auto", Cadence: "none",
		}); err != nil {
			t.Fatalf("seed candidate: %v", err)
		}
	}
	r := setupManagerRoutes(t, &pace023PreviewWorkflow{candidates: []engine.DetectedCandidate{
		{Email: "alpha@example.com", DisplayName: "Alpha"},
		{Email: "beta@example.com", DisplayName: "Beta"},
	}})

	missingScope := managerRequest(t, r, http.MethodPost, "/api/manager/detect", manager, nil)
	if missingScope.Code != http.StatusBadRequest {
		t.Fatalf("missing team_id status = %d", missingScope.Code)
	}
	preview := managerRequest(t, r, http.MethodPost, "/api/manager/detect?team_id="+team.ID.String(), manager, nil)
	if preview.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", preview.Code, preview.Body.String())
	}
	var previewBody struct {
		TeamID     string `json:"team_id"`
		Detected   int    `json:"detected"`
		Eligible   int    `json:"eligible"`
		Assigned   int    `json:"assigned"`
		Skipped    int    `json:"skipped"`
		Candidates []struct {
			Email           string `json:"email"`
			DisplayName     string `json:"display_name"`
			AlreadyAssigned bool   `json:"already_assigned"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(preview.Body.Bytes(), &previewBody); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if previewBody.TeamID != team.ID.String() || previewBody.Detected != 2 || previewBody.Eligible != 2 || previewBody.Assigned != 0 || previewBody.Skipped != 0 || len(previewBody.Candidates) != 2 {
		t.Fatalf("preview = %#v", previewBody)
	}

	confirm := managerRequest(t, r, http.MethodPost, "/api/manager/detect/confirm?team_id="+team.ID.String(), manager, map[string]any{
		"emails": []string{" ALPHA@example.com ", "beta@example.com"},
	})
	if confirm.Code != http.StatusOK {
		t.Fatalf("confirm status=%d body=%s", confirm.Code, confirm.Body.String())
	}
	var confirmed struct {
		TeamID   string `json:"team_id"`
		Assigned int    `json:"assigned"`
		Skipped  int    `json:"skipped"`
		Total    int    `json:"total"`
	}
	if err := json.Unmarshal(confirm.Body.Bytes(), &confirmed); err != nil {
		t.Fatalf("decode confirm: %v", err)
	}
	if confirmed.TeamID != team.ID.String() || confirmed.Assigned != 2 || confirmed.Skipped != 0 || confirmed.Total != 2 {
		t.Fatalf("confirm = %#v", confirmed)
	}
	repeat := managerRequest(t, r, http.MethodPost, "/api/manager/detect/confirm?team_id="+team.ID.String(), manager, map[string]any{
		"emails": []string{"alpha@example.com", "unknown@example.com"},
	})
	if repeat.Code != http.StatusOK {
		t.Fatalf("repeat status=%d body=%s", repeat.Code, repeat.Body.String())
	}
}

func TestPACE023RealEnginePreservesExternalRosterWhenFreeBusyUnavailable(t *testing.T) {
	manager := createTestUser(t, "pace023-real-manager@example.com")
	teamA := createManagerTestTeam(t, manager, "PACE023 Real A")
	teamB := createManagerTestTeam(t, manager, "PACE023 Real B")
	db := openTestDB(t)
	emails := make([]string, 0, 10)
	for i := 0; i < 10; i++ {
		email := fmt.Sprintf("external-%02d@contract.invalid", i)
		emails = append(emails, email)
		if err := storage.UpsertManagerTeamMember(db, &storage.ManagerTeamMember{
			ManagerUserID: manager, MemberEmail: email, DisplayName: fmt.Sprintf("External %02d", i), Source: "auto", Cadence: "none",
		}); err != nil {
			t.Fatalf("seed member %d: %v", i, err)
		}
	}
	if assigned, _, _, err := storage.ConfirmManagerTeamMembers(db, manager, teamA.ID, emails); err != nil || assigned != 10 {
		t.Fatalf("confirm A assigned=%d err=%v", assigned, err)
	}
	h := newManagerHandlersWithEngineFactory(db, func() ManagerWorkflow {
		return &engine.ManagerEngine{DB: db, OAuthConfig: nil}
	})
	r := setupManagerRoutes(t, h.newEngine())
	response := managerRequest(t, r, http.MethodGet, "/api/manager/team?team_id="+teamA.ID.String()+"&week=2026-09-07", manager, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("team A status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		TeamID  string `json:"team_id"`
		Members []struct {
			Email    string                 `json:"email"`
			ThisWeek engine.MemberWeekStats `json:"this_week"`
			LastWeek engine.MemberWeekStats `json:"last_week"`
		} `json:"members"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode roster: %v", err)
	}
	if body.TeamID != teamA.ID.String() || len(body.Members) != 10 {
		t.Fatalf("team A id=%q members=%d", body.TeamID, len(body.Members))
	}
	for _, member := range body.Members {
		if member.ThisWeek.DataAvailable || member.LastWeek.DataAvailable {
			t.Fatalf("unavailable FreeBusy reported available for %s", member.Email)
		}
	}
	other := managerRequest(t, r, http.MethodGet, "/api/manager/team?team_id="+teamB.ID.String()+"&week=2026-09-07", manager, nil)
	if other.Code != http.StatusOK {
		t.Fatalf("team B status=%d body=%s", other.Code, other.Body.String())
	}
	var otherBody struct {
		Members []json.RawMessage `json:"members"`
	}
	if err := json.Unmarshal(other.Body.Bytes(), &otherBody); err != nil || len(otherBody.Members) != 0 {
		t.Fatalf("team B must remain empty: count=%d err=%v", len(otherBody.Members), err)
	}
}
