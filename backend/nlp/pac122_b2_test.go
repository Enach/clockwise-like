package nlp

// PAC-122 phase 2, batch 2: SF-037 (a failed attendee extraction, an
// unparseable range and a failed slot search are no longer a 200 with
// nothing in it) and SF-070 (nlp_parsed details are JSON).

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/calendar"
	"github.com/Enach/paceday/backend/config"
	"github.com/Enach/paceday/backend/storage"
	"golang.org/x/oauth2"
)

// b2LLM answers the n-th completion with answers[n] (an empty answer is a
// 500), the last one repeated.
func b2LLM(t *testing.T, answers ...string) *storage.Settings {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		i := int(n.Add(1)) - 1
		if i >= len(answers) {
			i = len(answers) - 1
		}
		if answers[i] == "" {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"provider down"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": answers[i]}}}})
	}))
	t.Cleanup(srv.Close)
	forwardUserRequestsTo(t, srv)
	s := &storage.Settings{LLMProvider: "openai", Timezone: "UTC", WorkStart: "09:00", WorkEnd: "18:00"}
	s.LLMAPIKeys.Set("openai", "sk-test")
	return s
}

const b2Meeting = `{"intent":"schedule_meeting","title":"Sync","duration_minutes":30,"attendees":["alice@co.com"],"range_start":"2030-01-07","range_end":"2030-01-08"}`

// SF-037: a failed attendee extraction (the first completion) is the
// parse's error, as a failed second completion is, never "no attendees".
func TestPAC122_SF037_AttendeeExtractionFailureIsTheParseError(t *testing.T) {
	db := openTestDB(t)
	ctx := settingsUser(t, db, b2LLM(t, "", b2Meeting))
	if res, err := (&NLPService{DB: db}).Parse(ctx, "meet alice@co.com next week"); err == nil {
		t.Fatalf("Parse = %+v, want the extraction failure", res)
	}
	// An extraction answer that is not JSON is the invalid-JSON answer.
	ctx = settingsUser(t, db, b2LLM(t, "sure! alice", b2Meeting))
	res, err := (&NLPService{DB: db}).Parse(ctx, "meet alice@co.com next week")
	if err != nil || res.Intent != "unknown" || res.Error != invalidJSONMessage {
		t.Fatalf("Parse = %+v, %v; want intent unknown with %q", res, err, invalidJSONMessage)
	}
}

// SF-037: a range date that does not parse is an unusable answer, never a
// result with no range.
func TestPAC122_SF037_UnparseableRangeIsAnError(t *testing.T) {
	db := openTestDB(t)
	ctx := settingsUser(t, db, b2LLM(t, `{"intent":"schedule_meeting","title":"Sync","duration_minutes":30,"range_start":"next tuesday","range_end":"2030-01-08"}`))
	res, err := (&NLPService{DB: db}).Parse(ctx, "sync next tuesday")
	if err != nil {
		t.Fatal(err)
	}
	if res.Intent != "unknown" || res.Error != invalidRangeMessage {
		t.Fatalf("Parse = %+v, want intent unknown with %q", res, invalidRangeMessage)
	}
}

// SF-037: a slot search that fails (here: the calendar's freebusy answers
// 500) is the parse's error, not a 200 with no slots. A user with no
// calendar still gets a result with no slots.
func TestPAC122_SF037_SlotSearchFailureIsTheParseError(t *testing.T) {
	cal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"code":500,"message":"backend error"}}`))
	}))
	t.Cleanup(cal.Close)
	calendar.Configure(config.CalendarEndpoint{BaseURL: cal.URL + config.CalendarAPIRoot, Guard: true})
	t.Cleanup(func() { calendar.Configure(config.ProductionCalendarEndpoint()) })

	db := openTestDB(t)
	ctx := settingsUser(t, db, b2LLM(t, b2Meeting))
	if err := auth.UpsertUserToken(db, auth.UserIDFromContext(ctx), &oauth2.Token{AccessToken: "a", RefreshToken: "r", Expiry: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	svc := &NLPService{DB: db, OAuthConfig: &oauth2.Config{}}
	if res, err := svc.Parse(ctx, "meet alice@co.com"); err == nil || !strings.Contains(err.Error(), "suggest slots") {
		t.Fatalf("Parse = %+v, %v; want the slot search failure", res, err)
	}

	noCal := settingsUser(t, db, b2LLM(t, b2Meeting))
	res, err := svc.Parse(noCal, "meet alice@co.com")
	if err != nil || res.Intent != "schedule_meeting" || len(res.SuggestedSlots) != 0 {
		t.Fatalf("no calendar: Parse = %+v, %v", res, err)
	}
}

// SF-070: the nlp_parsed details are JSON whatever the typed text holds
// (Go's %q is not JSON for a control character).
func TestPAC122_SF070_NLPParsedDetailsAreJSON(t *testing.T) {
	db := openTestDB(t)
	ctx := settingsUser(t, db, b2LLM(t, `{"intent":"schedule_focus","duration_minutes":60}`))
	text := "focus \x01 \"deep\" work " + `\` + " now"
	if _, err := (&NLPService{DB: db}).Parse(ctx, text); err != nil {
		t.Fatal(err)
	}
	entries, err := storage.ListAuditLog(db, auth.UserIDFromContext(ctx), storage.AuditMaxLimit)
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries = %+v, %v", entries, err)
	}
	var d map[string]string
	if err := json.Unmarshal([]byte(entries[0].Details), &d); err != nil || d["text"] != text {
		t.Fatalf("details %q: %v", entries[0].Details, err)
	}
}
