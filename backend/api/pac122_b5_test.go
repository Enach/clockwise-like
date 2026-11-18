package api

// PAC-122 phase 2, batch 5, SF-046: a recap preview whose day cannot be
// read is the documented 500, not a recap saying "a clear calendar today".

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPAC122_SF046_PreviewWithUnreadableDayIs500(t *testing.T) {
	db := openTestDB(t)
	h := &dailyRecapHandlers{db: db}
	user := createTestUser(t, "pac122-sf046-preview@example.com")
	reported, _ := captureServerErrors(t)

	req := withUser(httptest.NewRequest(http.MethodPost, "/api/settings/daily-recap/preview", nil), user)
	w := httptest.NewRecorder()
	h.preview(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("setup: preview = %d %s", w.Code, w.Body.String())
	}

	// Today's brief whose stored results cannot be decoded (SF-080): the
	// day's read fails for this user only (the test database is shared).
	if _, err := db.Exec(`INSERT INTO meeting_briefs (calendar_event_id, user_id, generated_at, slack_results)
		VALUES ('evt-sf046', $1, NOW(), '{"x":1}'::jsonb)`, user); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	h.preview(w, withUser(httptest.NewRequest(http.MethodPost, "/api/settings/daily-recap/preview", nil), user))
	if w.Code != http.StatusInternalServerError || strings.TrimSpace(w.Body.String()) != `{"error":"db error"}` {
		t.Fatalf("preview = %d %s, want 500 db error", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "clear calendar") {
		t.Fatal("the preview claimed a clear calendar")
	}
	if len(*reported) == 0 {
		t.Fatal("the failure was not reported")
	}
}
