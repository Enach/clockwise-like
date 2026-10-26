package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Enach/paceday/backend/storage"
)

// PAC-28 / API-033: list-shaped response bodies and fields encode as `[]`
// when empty, never `null`.

func TestListBlocks_EmptyWeek_EncodesEmptyArray(t *testing.T) {
	db := openTestDB(t)
	h := &focusHandlers{eng: &mockFocusEngine{}, db: db}

	// A week far from any seeded data.
	req := httptest.NewRequest(http.MethodGet, "/api/focus/blocks?week=1999-01-04", nil)
	w := httptest.NewRecorder()
	h.listBlocks(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := strings.TrimSpace(w.Body.String()); got != "[]" {
		t.Errorf("body = %s, want []", got)
	}
}

// Week mode used to answer 200 [] when every day failed. PAC-36 (API-034)
// made a failing day fail the request, so the same input is now the generic
// 500 and never a null or empty array.
func TestCompress_WeekEveryDayFails_Is500NotEmptyArray(t *testing.T) {
	h := &scheduleHandlers{eng: &mockCompressor{err: errors.New("calendar down")}, smart: &mockScheduler{}}

	req := httptest.NewRequest(http.MethodPost, "/api/schedule/compress", bytes.NewBufferString(`{"week":"2025-01-06"}`))
	w := httptest.NewRecorder()
	h.compress(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body %s", w.Code, w.Body.String())
	}
	exactJSONError(t, w, "internal error")
}

func TestApplyCompress_NothingToApply_EncodesEmptyArrays(t *testing.T) {
	// The engine reports nil for both lists, exactly as CompressionEngine.Apply
	// does when it is handed no proposals.
	h := &scheduleHandlers{eng: &mockCompressor{}, smart: &mockScheduler{}}

	req := httptest.NewRequest(http.MethodPost, "/api/schedule/compress/apply", bytes.NewBufferString(`{"proposals":[]}`))
	w := httptest.NewRecorder()
	h.applyCompress(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", w.Body.String(), err)
	}
	for _, k := range []string{"applied", "failed"} {
		if string(body[k]) != "[]" {
			t.Errorf("%s = %s, want []", k, body[k])
		}
	}
}

// getTeam only reaches an empty member list when ListTeamMembers fails (the
// caller is always a member), which cannot be provoked through the handler,
// so the response body builder is tested directly.
func TestTeamDetailBody_NoMembers_EncodesEmptyArray(t *testing.T) {
	raw, err := json.Marshal(teamDetailBody(&storage.Team{Name: "Empty"}, nil))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"members":[]`) {
		t.Errorf("body = %s, want \"members\":[]", raw)
	}
}
