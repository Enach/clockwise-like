package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/engine"
)

// PAC-36: malformed JSON is a 400 on runFocus and previewCompression
// (API-035), and a week-mode compression in which a day fails reports the
// failure instead of dropping the day (API-034). AC-1 (negative
// selected_slot_index) is pinned by TestPAC21ConfirmNegativeSlotIndexIs400.

// pac36MalformedBodies are present, non-null bodies that do not decode into
// the request object (contracts/features/PAC-36.md section 1).
var pac36MalformedBodies = []string{
	`{`,
	`not json`,
	`{"week":`,
	`[]`,
	`"2030-01-07"`,
	`5`,
	`true`,
	`{"week":5}`,
}

// pac36DefaultBodies mean "no options": they take the 200 path.
var pac36DefaultBodies = map[string]io.Reader{
	"absent":     nil,
	"empty":      strings.NewReader(""),
	"whitespace": strings.NewReader("  \n\t "),
	"null":       strings.NewReader("null"),
	"object":     strings.NewReader("{}"),
}

// countingFocusEngine records whether the engine was reached.
type countingFocusEngine struct {
	calls int
}

func (m *countingFocusEngine) Run(_ context.Context, _ time.Time) (*engine.FocusRunResult, error) {
	m.calls++
	return &engine.FocusRunResult{}, nil
}
func (m *countingFocusEngine) ClearWeek(_ context.Context, _ time.Time) (int, error) {
	return 0, nil
}

// dayCompressor answers SuggestForDay per day: failOn maps a weekday to the
// error that day's engine call returns. It records every day it was asked.
type dayCompressor struct {
	mu     sync.Mutex
	failOn map[time.Weekday]error
	days   []time.Time
}

func (m *dayCompressor) CheckCalendar(context.Context) error { return nil }
func (m *dayCompressor) SuggestForDay(_ context.Context, day time.Time) (*engine.CompressionResult, error) {
	m.mu.Lock()
	m.days = append(m.days, day)
	m.mu.Unlock()
	if err := m.failOn[day.Weekday()]; err != nil {
		return nil, err
	}
	return &engine.CompressionResult{Date: day.Format("2006-01-02"), Proposals: make([]engine.MoveProposal, 0)}, nil
}
func (m *dayCompressor) Apply(context.Context, []engine.MoveProposal) ([]string, []string, error) {
	return nil, nil, nil
}

func TestPAC36RunFocusMalformedJSONIs400(t *testing.T) {
	for _, body := range pac36MalformedBodies {
		t.Run(body, func(t *testing.T) {
			eng := &countingFocusEngine{}
			h := &focusHandlers{eng: eng}
			rec := httptest.NewRecorder()
			h.runFocus(rec, httptest.NewRequest(http.MethodPost, "/api/focus/run", bytes.NewBufferString(body)))
			assertFixed4xx(t, rec, http.StatusBadRequest, "invalid JSON")
			if eng.calls != 0 {
				t.Errorf("engine called %d times for a malformed body, want 0", eng.calls)
			}
		})
	}
}

func TestPAC36RunFocusDefaultBodiesAre200(t *testing.T) {
	for name, body := range pac36DefaultBodies {
		t.Run(name, func(t *testing.T) {
			eng := &countingFocusEngine{}
			h := &focusHandlers{eng: eng}
			rec := httptest.NewRecorder()
			h.runFocus(rec, httptest.NewRequest(http.MethodPost, "/api/focus/run", body))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body %q", rec.Code, rec.Body.String())
			}
			if eng.calls != 1 {
				t.Errorf("engine called %d times, want 1", eng.calls)
			}
		})
	}
}

func TestPAC36CompressMalformedJSONIs400(t *testing.T) {
	for _, body := range append(append([]string{}, pac36MalformedBodies...), `{"date":true}`) {
		t.Run(body, func(t *testing.T) {
			eng := &dayCompressor{}
			h := &scheduleHandlers{eng: eng, smart: &mockScheduler{}}
			rec := httptest.NewRecorder()
			h.compress(rec, httptest.NewRequest(http.MethodPost, "/api/schedule/compress", bytes.NewBufferString(body)))
			assertFixed4xx(t, rec, http.StatusBadRequest, "invalid JSON")
			if len(eng.days) != 0 {
				t.Errorf("engine asked for %d days on a malformed body, want 0", len(eng.days))
			}
		})
	}
}

func TestPAC36CompressDefaultBodiesAre200(t *testing.T) {
	for name, body := range pac36DefaultBodies {
		t.Run(name, func(t *testing.T) {
			eng := &dayCompressor{}
			h := &scheduleHandlers{eng: eng, smart: &mockScheduler{}}
			rec := httptest.NewRecorder()
			h.compress(rec, httptest.NewRequest(http.MethodPost, "/api/schedule/compress", body))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body %q", rec.Code, rec.Body.String())
			}
			var got []engine.CompressionResult
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode %q: %v", rec.Body.String(), err)
			}
			if len(got) != 1 {
				t.Errorf("single-day mode returned %d entries, want 1", len(got))
			}
		})
	}
}

func TestPAC36CompressWeekDayFailureIsReported(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		body   string
	}{
		{"generic error", errors.New("provider timeout"), http.StatusInternalServerError, "internal error"},
		{"calendar not connected", fmt.Errorf("wednesday: %w", engine.ErrCalendarNotConnected), http.StatusUnauthorized, calendarNotConnectedMessage},
	}
	for _, tc := range cases {
		for _, wd := range []time.Weekday{time.Monday, time.Wednesday, time.Friday} {
			t.Run(tc.name+"/"+wd.String(), func(t *testing.T) {
				eng := &dayCompressor{failOn: map[time.Weekday]error{wd: tc.err}}
				h := &scheduleHandlers{eng: eng, smart: &mockScheduler{}}
				rec := httptest.NewRecorder()
				h.compress(rec, httptest.NewRequest(http.MethodPost, "/api/schedule/compress", bytes.NewBufferString(`{"week":"2025-01-06"}`)))
				if rec.Code != tc.status {
					t.Fatalf("status = %d, want %d; body %q", rec.Code, tc.status, rec.Body.String())
				}
				exactJSONError(t, rec, tc.body)
				if strings.Contains(rec.Body.String(), "provider timeout") {
					t.Errorf("body leaks the engine error: %q", rec.Body.String())
				}
			})
		}
	}
}

func TestPAC36CompressWeekReturnsMondayToFriday(t *testing.T) {
	// Any day of the week, Sunday included, selects that week's Monday..Friday.
	for _, week := range []string{"2025-01-06", "2025-01-08", "2025-01-12"} {
		t.Run(week, func(t *testing.T) {
			eng := &dayCompressor{}
			h := &scheduleHandlers{eng: eng, smart: &mockScheduler{}}
			rec := httptest.NewRecorder()
			h.compress(rec, httptest.NewRequest(http.MethodPost, "/api/schedule/compress", bytes.NewBufferString(`{"week":"`+week+`"}`)))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body %q", rec.Code, rec.Body.String())
			}
			var got []engine.CompressionResult
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode %q: %v", rec.Body.String(), err)
			}
			want := []string{"2025-01-06", "2025-01-07", "2025-01-08", "2025-01-09", "2025-01-10"}
			if len(got) != len(want) {
				t.Fatalf("got %d entries, want %d: %s", len(got), len(want), rec.Body.String())
			}
			for i := range want {
				if got[i].Date != want[i] {
					t.Errorf("entry %d date = %q, want %q", i, got[i].Date, want[i])
				}
			}
		})
	}
}

// AC-2 also names confirmNaturalLanguage: its malformed body was already a
// 400 before PAC-36; this pins it beside the other two.
func TestPAC36ConfirmMalformedJSONIs400(t *testing.T) {
	for _, body := range []string{`{`, `not json`, `[]`, `{"selected_slot_index":"0"}`} {
		t.Run(body, func(t *testing.T) {
			h := &nlpHandlers{smart: &mockScheduler{}}
			rec := httptest.NewRecorder()
			h.confirm(rec, httptest.NewRequest(http.MethodPost, "/api/nlp/confirm", bytes.NewBufferString(body)))
			assertFixed4xx(t, rec, http.StatusBadRequest, "invalid JSON")
		})
	}
}
