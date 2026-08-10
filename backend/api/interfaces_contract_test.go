package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/nlp"
	googlecalendar "google.golang.org/api/calendar/v3"
)

// Compile-time assertions keep the handler ports honest when an implementation
// changes. These fail at compile time instead of waiting for an integration test
// to discover a broken adapter.
var (
	_ FocusEngine = (*engine.FocusTimeEngine)(nil)
	_ Compressor  = (*engine.CompressionEngine)(nil)
	_ Scheduler   = (*engine.SmartScheduler)(nil)
	_ NLPParser   = (*nlp.NLPService)(nil)
)

type contractFocusEngine struct {
	runWeek     time.Time
	runResult   *engine.FocusRunResult
	clearWeek   time.Time
	clearResult int
	runErr      error
	clearErr    error
}

func (f *contractFocusEngine) Run(_ context.Context, week time.Time) (*engine.FocusRunResult, error) {
	f.runWeek = week
	return f.runResult, f.runErr
}

func (f *contractFocusEngine) ClearWeek(_ context.Context, week time.Time) (int, error) {
	f.clearWeek = week
	return f.clearResult, f.clearErr
}

type contractCompressor struct {
	proposals []engine.MoveProposal
	applied   []string
	failed    []string
	err       error
}

func (c *contractCompressor) SuggestForDay(context.Context, time.Time) (*engine.CompressionResult, error) {
	return nil, nil
}

func (c *contractCompressor) Apply(_ context.Context, proposals []engine.MoveProposal) ([]string, []string, error) {
	c.proposals = proposals
	return c.applied, c.failed, c.err
}

type contractScheduler struct {
	suggestRequest *engine.ScheduleRequest
	suggestions    *engine.ScheduleSuggestions
	err            error
}

func (s *contractScheduler) Suggest(_ context.Context, req engine.ScheduleRequest) (*engine.ScheduleSuggestions, error) {
	s.suggestRequest = &req
	return s.suggestions, s.err
}

func (s *contractScheduler) CreateMeeting(context.Context, engine.ScheduleRequest, engine.SuggestedSlot) (*googlecalendar.Event, error) {
	return nil, s.err
}

type contractParser struct {
	text   string
	result *nlp.ParseResult
	err    error
}

func (p *contractParser) Parse(_ context.Context, text string) (*nlp.ParseResult, error) {
	p.text = text
	return p.result, p.err
}

func TestFocusHandlerUsesFocusEnginePort(t *testing.T) {
	week := time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC)
	fake := &contractFocusEngine{
		runResult: &engine.FocusRunResult{WeekStart: week, TotalMinutes: 90},
	}
	h := &focusHandlers{eng: fake}

	req := httptest.NewRequest(http.MethodPost, "/api/focus/run", bytes.NewBufferString(`{"week":"2026-05-04"}`))
	recorder := httptest.NewRecorder()
	h.runFocus(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if !fake.runWeek.Equal(week) {
		t.Fatalf("engine week = %v, want %v", fake.runWeek, week)
	}
	var got engine.FocusRunResult
	if err := json.NewDecoder(recorder.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !got.WeekStart.Equal(week) || got.TotalMinutes != 90 {
		t.Fatalf("response = %+v, want focus result", got)
	}
}

func TestScheduleSuggestUsesSchedulerPort(t *testing.T) {
	fake := &contractScheduler{
		suggestions: &engine.ScheduleSuggestions{Slots: []engine.SuggestedSlot{{
			Start: time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC),
			End:   time.Date(2026, 5, 4, 10, 30, 0, 0, time.UTC),
			Score: 42,
		}}},
	}
	h := &scheduleHandlers{smart: fake}
	body := `{"duration_minutes":30,"attendees":["alice@example.com"],"range_start":"2026-05-04T09:00:00Z","range_end":"2026-05-04T17:00:00Z","title":"Planning"}`

	req := httptest.NewRequest(http.MethodPost, "/api/schedule/suggest", bytes.NewBufferString(body))
	recorder := httptest.NewRecorder()
	h.suggestMeeting(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if fake.suggestRequest == nil {
		t.Fatal("scheduler was not called")
	}
	if fake.suggestRequest.DurationMinutes != 30 || fake.suggestRequest.Title != "Planning" {
		t.Fatalf("request = %+v, want duration 30 and title Planning", fake.suggestRequest)
	}
	if !reflect.DeepEqual(fake.suggestRequest.Attendees, []string{"alice@example.com"}) {
		t.Fatalf("attendees = %v, want [alice@example.com]", fake.suggestRequest.Attendees)
	}

	var got engine.ScheduleSuggestions
	if err := json.NewDecoder(recorder.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got.Slots) != 1 || got.Slots[0].Score != 42 {
		t.Fatalf("response = %+v, want one score-42 slot", got)
	}
}

func TestCompressionApplyUsesEventIDArrays(t *testing.T) {
	fake := &contractCompressor{applied: []string{"event-1"}, failed: []string{"event-2"}}
	h := &scheduleHandlers{eng: fake}
	body := `{"proposals":[{"event_id":"event-1","proposed_start":"2026-05-04T10:00:00Z","proposed_end":"2026-05-04T11:00:00Z"},{"event_id":"bad","proposed_start":"not-a-time","proposed_end":"2026-05-04T11:00:00Z"}]}`

	req := httptest.NewRequest(http.MethodPost, "/api/schedule/compress/apply", bytes.NewBufferString(body))
	recorder := httptest.NewRecorder()
	h.applyCompress(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if len(fake.proposals) != 1 || fake.proposals[0].EventID != "event-1" {
		t.Fatalf("proposals = %+v, want only valid event-1", fake.proposals)
	}
	var got struct {
		Applied []string `json:"applied"`
		Failed  []string `json:"failed"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !reflect.DeepEqual(got.Applied, []string{"event-1"}) {
		t.Fatalf("applied = %v, want [event-1]", got.Applied)
	}
	if !reflect.DeepEqual(got.Failed, []string{"bad: invalid time format", "event-2"}) {
		t.Fatalf("failed = %v, want [bad event-2]", got.Failed)
	}
}

func TestNLPHandlerUsesParserPort(t *testing.T) {
	fake := &contractParser{result: &nlp.ParseResult{Intent: "schedule_focus", Title: "Focus Time"}}
	h := &nlpHandlers{svc: fake}

	req := httptest.NewRequest(http.MethodPost, "/api/nlp/parse", bytes.NewBufferString(`{"text":"find focus time"}`))
	recorder := httptest.NewRecorder()
	h.parse(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if fake.text != "find focus time" {
		t.Fatalf("parser text = %q, want find focus time", fake.text)
	}
	var got nlp.ParseResult
	if err := json.NewDecoder(recorder.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Intent != "schedule_focus" || got.Title != "Focus Time" {
		t.Fatalf("response = %+v, want focus parse result", got)
	}
}
