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
	googlecalendar "google.golang.org/api/calendar/v3"
)

// mockCompressor implements Compressor.
type mockCompressor struct {
	result    *engine.CompressionResult
	err       error
	applied   []string
	failed    []string
	applyErr  error
}

func (m *mockCompressor) SuggestForDay(_ context.Context, _ time.Time) (*engine.CompressionResult, error) {
	return m.result, m.err
}
func (m *mockCompressor) Apply(_ context.Context, _ []engine.MoveProposal) ([]string, []string, error) {
	return m.applied, m.failed, m.applyErr
}

// mockScheduler implements Scheduler.
type mockScheduler struct {
	suggestions *engine.ScheduleSuggestions
	suggestErr  error
	created     *googlecalendar.Event
	createErr   error
}

func (m *mockScheduler) Suggest(_ context.Context, _ engine.ScheduleRequest) (*engine.ScheduleSuggestions, error) {
	return m.suggestions, m.suggestErr
}
func (m *mockScheduler) CreateMeeting(_ context.Context, _ engine.ScheduleRequest, _ engine.SuggestedSlot) (*googlecalendar.Event, error) {
	return m.created, m.createErr
}

func TestCompress_SingleDay(t *testing.T) {
	db := openTestDB(t)
	mock := &mockCompressor{result: &engine.CompressionResult{Date: "2025-01-06"}}
	h := &scheduleHandlers{eng: mock, smart: &mockScheduler{}, db: db}

	body := `{"date":"2025-01-06"}`
	req := httptest.NewRequest(http.MethodPost, "/api/schedule/compress", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	h.compress(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestCompress_Week(t *testing.T) {
	db := openTestDB(t)
	mock := &mockCompressor{result: &engine.CompressionResult{Date: "2025-01-06"}}
	h := &scheduleHandlers{eng: mock, smart: &mockScheduler{}, db: db}

	body := `{"week":"2025-01-06"}`
	req := httptest.NewRequest(http.MethodPost, "/api/schedule/compress", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	h.compress(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestCompress_InvalidWeek(t *testing.T) {
	db := openTestDB(t)
	h := &scheduleHandlers{eng: &mockCompressor{}, smart: &mockScheduler{}, db: db}

	body := `{"week":"bad"}`
	req := httptest.NewRequest(http.MethodPost, "/api/schedule/compress", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	h.compress(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestCompress_Error(t *testing.T) {
	db := openTestDB(t)
	mock := &mockCompressor{err: errors.New("calendar error")}
	h := &scheduleHandlers{eng: mock, smart: &mockScheduler{}, db: db}

	body := `{}`
	req := httptest.NewRequest(http.MethodPost, "/api/schedule/compress", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	h.compress(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
}

func TestCompress_InvalidDate(t *testing.T) {
	db := openTestDB(t)
	h := &scheduleHandlers{eng: &mockCompressor{}, smart: &mockScheduler{}, db: db}

	body := `{"date":"not-a-date"}`
	req := httptest.NewRequest(http.MethodPost, "/api/schedule/compress", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	h.compress(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestApplyCompress_Success(t *testing.T) {
	db := openTestDB(t)
	mock := &mockCompressor{applied: []string{"evt-1"}}
	h := &scheduleHandlers{eng: mock, smart: &mockScheduler{}, db: db}

	now := time.Now().UTC()
	proposals := map[string]interface{}{
		"proposals": []map[string]string{
			{
				"event_id":       "evt-1",
				"proposed_start": now.Add(time.Hour).Format(time.RFC3339),
				"proposed_end":   now.Add(2 * time.Hour).Format(time.RFC3339),
			},
		},
	}
	body, _ := json.Marshal(proposals)
	req := httptest.NewRequest(http.MethodPost, "/api/schedule/compress/apply", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.applyCompress(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestApplyCompress_InvalidJSON(t *testing.T) {
	db := openTestDB(t)
	h := &scheduleHandlers{eng: &mockCompressor{}, smart: &mockScheduler{}, db: db}

	req := httptest.NewRequest(http.MethodPost, "/api/schedule/compress/apply", bytes.NewBufferString("not json"))
	w := httptest.NewRecorder()
	h.applyCompress(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestApplyCompress_InvalidTime(t *testing.T) {
	db := openTestDB(t)
	mock := &mockCompressor{applied: []string{}}
	h := &scheduleHandlers{eng: mock, smart: &mockScheduler{}, db: db}

	proposals := map[string]interface{}{
		"proposals": []map[string]string{
			{
				"event_id":       "evt-1",
				"proposed_start": "not-a-time",
				"proposed_end":   "also-not-a-time",
			},
		},
	}
	body, _ := json.Marshal(proposals)
	req := httptest.NewRequest(http.MethodPost, "/api/schedule/compress/apply", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.applyCompress(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d (invalid time goes to failed list, not error), want 200", w.Code)
	}
}

func TestApplyCompress_EngineError(t *testing.T) {
	db := openTestDB(t)
	mock := &mockCompressor{applyErr: errors.New("apply failed")}
	h := &scheduleHandlers{eng: mock, smart: &mockScheduler{}, db: db}

	now := time.Now().UTC()
	proposals := map[string]interface{}{
		"proposals": []map[string]string{
			{
				"event_id":       "evt-1",
				"proposed_start": now.Add(time.Hour).Format(time.RFC3339),
				"proposed_end":   now.Add(2 * time.Hour).Format(time.RFC3339),
			},
		},
	}
	body, _ := json.Marshal(proposals)
	req := httptest.NewRequest(http.MethodPost, "/api/schedule/compress/apply", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.applyCompress(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
}

func TestSuggestMeeting_Success(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().UTC()
	mock := &mockScheduler{
		suggestions: &engine.ScheduleSuggestions{
			Slots: []engine.SuggestedSlot{{Start: now.Add(time.Hour), End: now.Add(2 * time.Hour)}},
		},
	}
	h := &scheduleHandlers{eng: &mockCompressor{}, smart: mock, db: db}

	body := map[string]interface{}{
		"duration_minutes": 60,
		"attendees":        []string{"alice@co.com"},
		"range_start":      now.Format(time.RFC3339),
		"range_end":        now.Add(7 * 24 * time.Hour).Format(time.RFC3339),
		"title":            "Test Meeting",
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/schedule/suggest", bytes.NewReader(b))
	w := httptest.NewRecorder()
	h.suggestMeeting(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestSuggestMeeting_InvalidJSON(t *testing.T) {
	db := openTestDB(t)
	h := &scheduleHandlers{eng: &mockCompressor{}, smart: &mockScheduler{}, db: db}

	req := httptest.NewRequest(http.MethodPost, "/api/schedule/suggest", bytes.NewBufferString("bad"))
	w := httptest.NewRecorder()
	h.suggestMeeting(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestSuggestMeeting_InvalidRange(t *testing.T) {
	db := openTestDB(t)
	h := &scheduleHandlers{eng: &mockCompressor{}, smart: &mockScheduler{}, db: db}

	body := `{"range_start":"bad","range_end":"bad"}`
	req := httptest.NewRequest(http.MethodPost, "/api/schedule/suggest", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	h.suggestMeeting(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestSuggestMeeting_Error(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().UTC()
	mock := &mockScheduler{suggestErr: errors.New("freebusy error")}
	h := &scheduleHandlers{eng: &mockCompressor{}, smart: mock, db: db}

	body := map[string]interface{}{
		"range_start": now.Format(time.RFC3339),
		"range_end":   now.Add(24 * time.Hour).Format(time.RFC3339),
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/schedule/suggest", bytes.NewReader(b))
	w := httptest.NewRecorder()
	h.suggestMeeting(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
}

func TestCreateMeeting_Success(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().UTC()
	mock := &mockScheduler{
		created: &googlecalendar.Event{Id: "new-evt", Summary: "Team Sync"},
	}
	h := &scheduleHandlers{eng: &mockCompressor{}, smart: mock, db: db}

	body := map[string]interface{}{
		"title":     "Team Sync",
		"start":     now.Add(time.Hour).Format(time.RFC3339),
		"end":       now.Add(2 * time.Hour).Format(time.RFC3339),
		"attendees": []string{"bob@co.com"},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/schedule/create", bytes.NewReader(b))
	w := httptest.NewRecorder()
	h.createMeeting(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestCreateMeeting_InvalidJSON(t *testing.T) {
	db := openTestDB(t)
	h := &scheduleHandlers{eng: &mockCompressor{}, smart: &mockScheduler{}, db: db}

	req := httptest.NewRequest(http.MethodPost, "/api/schedule/create", bytes.NewBufferString("bad"))
	w := httptest.NewRecorder()
	h.createMeeting(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestCreateMeeting_InvalidTime(t *testing.T) {
	db := openTestDB(t)
	h := &scheduleHandlers{eng: &mockCompressor{}, smart: &mockScheduler{}, db: db}

	body := `{"title":"Test","start":"bad","end":"bad"}`
	req := httptest.NewRequest(http.MethodPost, "/api/schedule/create", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	h.createMeeting(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestCreateMeeting_Error(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().UTC()
	mock := &mockScheduler{createErr: errors.New("calendar error")}
	h := &scheduleHandlers{eng: &mockCompressor{}, smart: mock, db: db}

	body := map[string]interface{}{
		"title": "Test",
		"start": now.Add(time.Hour).Format(time.RFC3339),
		"end":   now.Add(2 * time.Hour).Format(time.RFC3339),
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/schedule/create", bytes.NewReader(b))
	w := httptest.NewRecorder()
	h.createMeeting(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
}
