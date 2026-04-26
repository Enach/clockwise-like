package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Enach/clockwise-like/backend/engine"
)

// mockFocusEngine implements FocusEngine.
type mockFocusEngine struct {
	runResult  *engine.FocusRunResult
	runErr     error
	clearCount int
	clearErr   error
}

func (m *mockFocusEngine) Run(_ context.Context, _ time.Time) (*engine.FocusRunResult, error) {
	return m.runResult, m.runErr
}
func (m *mockFocusEngine) ClearWeek(_ context.Context, _ time.Time) (int, error) {
	return m.clearCount, m.clearErr
}

func TestRunFocus_Success(t *testing.T) {
	db := openTestDB(t)
	mock := &mockFocusEngine{
		runResult: &engine.FocusRunResult{TotalMinutes: 120},
	}
	h := &focusHandlers{eng: mock, db: db}

	body := `{"week":"2025-01-06"}`
	req := httptest.NewRequest(http.MethodPost, "/api/focus/run", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	h.runFocus(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	var result engine.FocusRunResult
	json.Unmarshal(w.Body.Bytes(), &result)
	if result.TotalMinutes != 120 {
		t.Errorf("TotalMinutes = %d, want 120", result.TotalMinutes)
	}
}

func TestRunFocus_InvalidWeek(t *testing.T) {
	db := openTestDB(t)
	mock := &mockFocusEngine{runResult: &engine.FocusRunResult{}}
	h := &focusHandlers{eng: mock, db: db}

	body := `{"week":"not-a-date"}`
	req := httptest.NewRequest(http.MethodPost, "/api/focus/run", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	h.runFocus(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestRunFocus_Error(t *testing.T) {
	db := openTestDB(t)
	mock := &mockFocusEngine{runErr: errors.New("calendar error")}
	h := &focusHandlers{eng: mock, db: db}

	body := `{}`
	req := httptest.NewRequest(http.MethodPost, "/api/focus/run", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	h.runFocus(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
}

func TestRunFocus_NoBody(t *testing.T) {
	db := openTestDB(t)
	mock := &mockFocusEngine{runResult: &engine.FocusRunResult{TotalMinutes: 60}}
	h := &focusHandlers{eng: mock, db: db}

	// Empty body → defaults to current week
	req := httptest.NewRequest(http.MethodPost, "/api/focus/run", bytes.NewBufferString(`{}`))
	w := httptest.NewRecorder()
	h.runFocus(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestListBlocks(t *testing.T) {
	db := openTestDB(t)
	mock := &mockFocusEngine{}
	h := &focusHandlers{eng: mock, db: db}

	req := httptest.NewRequest(http.MethodGet, "/api/focus/blocks?week=2025-01-06", nil)
	w := httptest.NewRecorder()
	h.listBlocks(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestListBlocks_NoWeek(t *testing.T) {
	db := openTestDB(t)
	h := &focusHandlers{eng: &mockFocusEngine{}, db: db}

	req := httptest.NewRequest(http.MethodGet, "/api/focus/blocks", nil)
	w := httptest.NewRecorder()
	h.listBlocks(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestListBlocks_InvalidWeek(t *testing.T) {
	db := openTestDB(t)
	h := &focusHandlers{eng: &mockFocusEngine{}, db: db}

	req := httptest.NewRequest(http.MethodGet, "/api/focus/blocks?week=bad", nil)
	w := httptest.NewRecorder()
	h.listBlocks(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestClearBlocks_Success(t *testing.T) {
	db := openTestDB(t)
	mock := &mockFocusEngine{clearCount: 3}
	h := &focusHandlers{eng: mock, db: db}

	req := httptest.NewRequest(http.MethodDelete, "/api/focus/blocks?week=2025-01-06", nil)
	w := httptest.NewRecorder()
	h.clearBlocks(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	var body map[string]int
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["deleted"] != 3 {
		t.Errorf("deleted = %d, want 3", body["deleted"])
	}
}

func TestClearBlocks_Error(t *testing.T) {
	db := openTestDB(t)
	mock := &mockFocusEngine{clearErr: errors.New("delete failed")}
	h := &focusHandlers{eng: mock, db: db}

	req := httptest.NewRequest(http.MethodDelete, "/api/focus/blocks", nil)
	w := httptest.NewRecorder()
	h.clearBlocks(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
}

func TestClearBlocks_InvalidWeek(t *testing.T) {
	db := openTestDB(t)
	h := &focusHandlers{eng: &mockFocusEngine{}, db: db}

	req := httptest.NewRequest(http.MethodDelete, "/api/focus/blocks?week=invalid", nil)
	w := httptest.NewRecorder()
	h.clearBlocks(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}
