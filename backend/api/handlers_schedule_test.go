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
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
	googlecalendar "google.golang.org/api/calendar/v3"
)

// mockCompressor implements Compressor.
type mockCompressor struct {
	checkErr error
	result    *engine.CompressionResult
	err       error
	applied   []string
	failed    []string
	applyErr  error
}

func (m *mockCompressor) CheckCalendar(context.Context) error { return m.checkErr }
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

// auditActionsFor lists the actions recorded against user, newest first (PAC-23).
func auditActionsFor(t *testing.T, user uuid.UUID) []string {
	t.Helper()
	entries, err := storage.ListAuditLog(openTestDB(t), user, storage.AuditMaxLimit)
	if err != nil {
		t.Fatalf("ListAuditLog: %v", err)
	}
	actions := make([]string, 0, len(entries))
	for _, e := range entries {
		actions = append(actions, e.Action)
	}
	return actions
}

// PAC-23 AC-5 / AC-14: POST /api/schedule/create is recorded against the caller.
func TestCreateSchedule_AuditEntrySubjectIsCaller(t *testing.T) {
	db := openTestDB(t)
	caller := createTestUser(t, "audit-schedule-create@example.com")
	other := createTestUser(t, "audit-schedule-create-other@example.com")
	h := &scheduleHandlers{eng: &mockCompressor{}, smart: &mockScheduler{created: &googlecalendar.Event{Id: "audit-evt"}}, db: db}
	now := time.Now().UTC()
	b, _ := json.Marshal(map[string]interface{}{
		"title": "Audit Sync",
		"start": now.Add(time.Hour).Format(time.RFC3339),
		"end":   now.Add(2 * time.Hour).Format(time.RFC3339),
	})
	w := httptest.NewRecorder()
	h.createMeeting(w, withUser(httptest.NewRequest(http.MethodPost, "/api/schedule/create", bytes.NewReader(b)), caller))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", w.Code, w.Body.String())
	}
	if got := auditActionsFor(t, caller); len(got) != 1 || got[0] != "meeting_created" {
		t.Errorf("caller's log = %v, want [meeting_created]", got)
	}
	if got := auditActionsFor(t, other); len(got) != 0 {
		t.Errorf("other user's log = %v, want empty", got)
	}
}

// PAC-29 AC-2: POST /api/schedule/create and POST /api/nlp/confirm answer the
// CalendarEvent DTO of the created event (toCalendarEventDTO, as the list),
// never Google's raw event, and still write their audit rows.
func TestPAC29CreateAndConfirmAnswerCalendarEventDTO(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)
	created := &googlecalendar.Event{
		Id:          "new-evt",
		Summary:     "Team Sync",
		HtmlLink:    "https://calendar.example.test/new-evt",
		Start:       &googlecalendar.EventDateTime{DateTime: now.Add(time.Hour).Format(time.RFC3339)},
		End:         &googlecalendar.EventDateTime{DateTime: now.Add(2 * time.Hour).Format(time.RFC3339)},
		Attendees:   []*googlecalendar.EventAttendee{{Email: "bob@co.com"}, {Email: "room-1@resource.calendar.google.com", Resource: true}},
		HangoutLink: "https://meet.google.com/abc-defg-hij",
	}
	want, _ := json.Marshal(toCalendarEventDTO(created))

	scheduleBody, _ := json.Marshal(map[string]interface{}{
		"title":     "Team Sync",
		"start":     now.Add(time.Hour).Format(time.RFC3339),
		"end":       now.Add(2 * time.Hour).Format(time.RFC3339),
		"attendees": []string{"bob@co.com"},
	})
	confirmBody, _ := json.Marshal(map[string]interface{}{
		"parse_result": map[string]interface{}{
			"intent":           "schedule_meeting",
			"title":            "Team Sync",
			"duration_minutes": 60,
			"attendees":        []string{"bob@co.com"},
			"suggested_slots":  []engine.SuggestedSlot{{Start: now.Add(time.Hour), End: now.Add(2 * time.Hour), Score: 50}},
		},
		"selected_slot_index": 0,
	})

	for _, tc := range []struct {
		name, path, action string
		body               []byte
		serve              func(http.ResponseWriter, *http.Request)
	}{
		{"schedule/create", "/api/schedule/create", "meeting_created", scheduleBody,
			(&scheduleHandlers{eng: &mockCompressor{}, smart: &mockScheduler{created: created}, db: db}).createMeeting},
		{"nlp/confirm", "/api/nlp/confirm", "nlp_confirmed", confirmBody,
			(&nlpHandlers{svc: &mockNLPParser{}, smart: &mockScheduler{created: created}, db: db}).confirm},
	} {
		t.Run(tc.name, func(t *testing.T) {
			user := createTestUser(t, "pac29-create-"+tc.action+"@example.com")
			w := httptest.NewRecorder()
			tc.serve(w, withUser(httptest.NewRequest(http.MethodPost, tc.path, bytes.NewReader(tc.body)), user))
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body %s", w.Code, w.Body.String())
			}
			assertSameJSON(t, w.Body.Bytes(), want)

			var body map[string]any
			_ = json.Unmarshal(w.Body.Bytes(), &body)
			if body["title"] != "Team Sync" {
				t.Fatalf("title = %v, want Team Sync: %s", body["title"], w.Body.String())
			}
			for _, k := range []string{"start", "end"} {
				if _, ok := body[k].(string); !ok {
					t.Fatalf("%s is %T, want a string: %s", k, body[k], w.Body.String())
				}
			}
			for _, k := range []string{"summary", "htmlLink", "hangoutLink"} {
				if _, ok := body[k]; ok {
					t.Fatalf("body carries Google's key %q: %s", k, w.Body.String())
				}
			}

			entries, err := storage.ListAuditLog(db, user, storage.AuditMaxLimit)
			if err != nil {
				t.Fatalf("ListAuditLog: %v", err)
			}
			if len(entries) != 1 || entries[0].Action != tc.action || !bytes.Contains([]byte(entries[0].Details), []byte(`"event_id":"new-evt"`)) {
				t.Fatalf("audit log = %+v, want one %s row with event_id new-evt", entries, tc.action)
			}
		})
	}
}
