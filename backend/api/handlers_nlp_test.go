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

	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/nlp"
	googlecalendar "google.golang.org/api/calendar/v3"
)

// mockNLPParser implements NLPParser.
type mockNLPParser struct {
	result *nlp.ParseResult
	err    error
}

func (m *mockNLPParser) Parse(_ context.Context, _ string) (*nlp.ParseResult, error) {
	return m.result, m.err
}

func TestNLPParse_Success(t *testing.T) {
	db := openTestDB(t)
	mockParser := &mockNLPParser{
		result: &nlp.ParseResult{Intent: "schedule_focus", DurationMinutes: 90},
	}
	h := &nlpHandlers{svc: mockParser, smart: &mockScheduler{}, db: db}

	body := `{"text":"block focus time tomorrow"}`
	req := httptest.NewRequest(http.MethodPost, "/api/nlp/parse", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	h.parse(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}

	var result nlp.ParseResult
	json.Unmarshal(w.Body.Bytes(), &result)
	if result.Intent != "schedule_focus" {
		t.Errorf("Intent = %q, want schedule_focus", result.Intent)
	}
}

func TestNLPParse_MissingText(t *testing.T) {
	db := openTestDB(t)
	h := &nlpHandlers{svc: &mockNLPParser{}, smart: &mockScheduler{}, db: db}

	body := `{"text":""}`
	req := httptest.NewRequest(http.MethodPost, "/api/nlp/parse", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	h.parse(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestNLPParse_InvalidJSON(t *testing.T) {
	db := openTestDB(t)
	h := &nlpHandlers{svc: &mockNLPParser{}, smart: &mockScheduler{}, db: db}

	req := httptest.NewRequest(http.MethodPost, "/api/nlp/parse", bytes.NewBufferString("not json"))
	w := httptest.NewRecorder()
	h.parse(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestNLPParse_ParseError(t *testing.T) {
	db := openTestDB(t)
	mockParser := &mockNLPParser{err: errors.New("LLM API error")}
	h := &nlpHandlers{svc: mockParser, smart: &mockScheduler{}, db: db}

	body := `{"text":"do something"}`
	req := httptest.NewRequest(http.MethodPost, "/api/nlp/parse", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	h.parse(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
}

func TestNLPConfirm_Success(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().UTC()

	smartMock := &mockScheduler{
		created: &googlecalendar.Event{Id: "confirmed-event", Summary: "Team Sync"},
	}
	h := &nlpHandlers{svc: &mockNLPParser{}, smart: smartMock, db: db}

	body := map[string]interface{}{
		"parse_result": nlp.ParseResult{
			Intent:          "schedule_meeting",
			Title:           "Team Sync",
			DurationMinutes: 30,
			Attendees:       []string{"alice@co.com"},
			SuggestedSlots: []engine.SuggestedSlot{
				{Start: now.Add(time.Hour), End: now.Add(2 * time.Hour), Score: 50},
			},
		},
		"selected_slot_index": 0,
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/nlp/confirm", bytes.NewReader(b))
	w := httptest.NewRecorder()
	h.confirm(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
}

func TestNLPConfirm_InvalidJSON(t *testing.T) {
	db := openTestDB(t)
	h := &nlpHandlers{svc: &mockNLPParser{}, smart: &mockScheduler{}, db: db}

	req := httptest.NewRequest(http.MethodPost, "/api/nlp/confirm", bytes.NewBufferString("bad"))
	w := httptest.NewRecorder()
	h.confirm(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestNLPConfirm_NoSlots(t *testing.T) {
	db := openTestDB(t)
	h := &nlpHandlers{svc: &mockNLPParser{}, smart: &mockScheduler{}, db: db}

	body := map[string]interface{}{
		"parse_result":        nlp.ParseResult{Intent: "schedule_meeting"},
		"selected_slot_index": 0,
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/nlp/confirm", bytes.NewReader(b))
	w := httptest.NewRecorder()
	h.confirm(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestNLPConfirm_InvalidSlotIndex(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().UTC()
	h := &nlpHandlers{svc: &mockNLPParser{}, smart: &mockScheduler{}, db: db}

	body := map[string]interface{}{
		"parse_result": nlp.ParseResult{
			SuggestedSlots: []engine.SuggestedSlot{
				{Start: now, End: now.Add(time.Hour)},
			},
		},
		"selected_slot_index": 5, // out of range
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/nlp/confirm", bytes.NewReader(b))
	w := httptest.NewRecorder()
	h.confirm(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestNLPConfirm_CreateError(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().UTC()
	smartMock := &mockScheduler{createErr: errors.New("calendar error")}
	h := &nlpHandlers{svc: &mockNLPParser{}, smart: smartMock, db: db}

	body := map[string]interface{}{
		"parse_result": nlp.ParseResult{
			Intent: "schedule_meeting",
			Title:  "Meeting",
			SuggestedSlots: []engine.SuggestedSlot{
				{Start: now.Add(time.Hour), End: now.Add(2 * time.Hour)},
			},
		},
		"selected_slot_index": 0,
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/nlp/confirm", bytes.NewReader(b))
	w := httptest.NewRecorder()
	h.confirm(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
}
