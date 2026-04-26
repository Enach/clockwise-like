package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/engine"
)

func TestFreeBusy_Unauthorized(t *testing.T) {
	h := newFreeBusyHandlers(openTestDB(t), nil)
	req := httptest.NewRequest(http.MethodPost, "/api/freebusy", nil)
	w := httptest.NewRecorder()
	h.query(w, req) // no user in context
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestFreeBusy_TooManyEmails(t *testing.T) {
	h := newFreeBusyHandlers(openTestDB(t), nil)
	userID := createTestUser(t, "freebusy-user@example.com")

	emails := make([]string, 21)
	for i := range emails {
		emails[i] = "user@company.com"
	}
	body, _ := json.Marshal(map[string]interface{}{
		"emails":     emails,
		"start_time": time.Now().Format(time.RFC3339),
		"end_time":   time.Now().Add(time.Hour).Format(time.RFC3339),
	})
	req := httptest.NewRequest(http.MethodPost, "/api/freebusy", bytes.NewReader(body))
	req = withUser(req, userID)
	w := httptest.NewRecorder()
	h.query(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestFreeBusy_InvalidStartTime(t *testing.T) {
	h := newFreeBusyHandlers(openTestDB(t), nil)
	userID := createTestUser(t, "freebusy-user2@example.com")

	body, _ := json.Marshal(map[string]interface{}{
		"emails":     []string{"alice@company.com"},
		"start_time": "not-a-date",
		"end_time":   time.Now().Add(time.Hour).Format(time.RFC3339),
	})
	req := httptest.NewRequest(http.MethodPost, "/api/freebusy", bytes.NewReader(body))
	req = withUser(req, userID)
	w := httptest.NewRecorder()
	h.query(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestFreeBusy_PersonalEmailsReturnUnknown(t *testing.T) {
	db := openTestDB(t)
	h := newFreeBusyHandlers(db, nil)
	userID := createTestUser(t, "freebusy-user3@example.com")

	body, _ := json.Marshal(map[string]interface{}{
		"emails":     []string{"someone@gmail.com", "other@hotmail.com"},
		"start_time": time.Now().Format(time.RFC3339),
		"end_time":   time.Now().Add(time.Hour).Format(time.RFC3339),
	})
	req := httptest.NewRequest(http.MethodPost, "/api/freebusy", bytes.NewReader(body))
	req = withUser(req, userID)
	w := httptest.NewRecorder()
	h.query(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Results []engine.ParticipantBusy `json:"results"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, r := range resp.Results {
		if r.Coverage != "unknown" {
			t.Errorf("email %s: coverage = %q, want unknown", r.Email, r.Coverage)
		}
	}
}

func TestFreeBusy_EmptyEmailsReturns400(t *testing.T) {
	h := newFreeBusyHandlers(openTestDB(t), nil)
	userID := createTestUser(t, "freebusy-user4@example.com")

	body, _ := json.Marshal(map[string]interface{}{
		"emails":     []string{},
		"start_time": time.Now().Format(time.RFC3339),
		"end_time":   time.Now().Add(time.Hour).Format(time.RFC3339),
	})
	req := httptest.NewRequest(http.MethodPost, "/api/freebusy", bytes.NewReader(body))
	req = withUser(req, userID)
	w := httptest.NewRecorder()
	h.query(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}
