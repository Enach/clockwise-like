package nlp

// PAC-122 phase 2, batch 2: SF-037 clauses 2 and 3 (a failed attendee
// extraction and an unparseable range date are reported in `error`, with
// PAC-133's precedence; clause 1, the suggestion step, is PAC-133's) and
// SF-070 (nlp_parsed details are JSON).

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/storage"
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

// SF-037 (second clause): a failed attendee extraction (the first
// completion fails, or does not answer JSON) is no longer silent: the parse
// answers as parsed, with the fixed sentence in `error`. Not a 500.
func TestPAC122_SF037_AttendeeExtractionFailureIsReported(t *testing.T) {
	for name, first := range map[string]string{"provider failure": "", "not JSON": "sure! alice"} {
		t.Run(name, func(t *testing.T) {
			db := openTestDB(t)
			ctx := settingsUser(t, db, b2LLM(t, first, b2Meeting))
			res, err := (&NLPService{DB: db}).Parse(ctx, "meet alice@co.com next week")
			if err != nil {
				t.Fatalf("Parse: %v (want the 200 answer)", err)
			}
			if res.Intent != "schedule_meeting" || res.Title != "Sync" || res.Error != attendeeExtractionMessage {
				t.Fatalf("Parse = %+v; want the parsed intent with %q", res, attendeeExtractionMessage)
			}
			if strings.Contains(res.Error, "provider down") || strings.Contains(res.Error, "sure!") {
				t.Fatalf("error carries provider text: %q", res.Error)
			}
		})
	}
}

// SF-037 precedence: an error the model already set is never replaced.
func TestPAC122_SF037_ModelErrorWinsOverAttendeeFailure(t *testing.T) {
	db := openTestDB(t)
	ctx := settingsUser(t, db, b2LLM(t, "", `{"intent":"schedule_meeting","title":"Sync","error":"which Alice?"}`))
	res, err := (&NLPService{DB: db}).Parse(ctx, "meet alice")
	if err != nil || res.Error != "which Alice?" {
		t.Fatalf("Parse = %+v, %v; want the model's error kept", res, err)
	}
}

// A successful extraction sets no error.
func TestPAC122_SF037_SuccessfulExtractionSetsNoError(t *testing.T) {
	db := openTestDB(t)
	ctx := settingsUser(t, db, b2LLM(t, `{"attendees":["alice@co.com"]}`, `{"intent":"schedule_focus","duration_minutes":60}`))
	res, err := (&NLPService{DB: db}).Parse(ctx, "focus")
	if err != nil || res.Error != "" || res.Intent != "schedule_focus" {
		t.Fatalf("Parse = %+v, %v", res, err)
	}
}

// SF-037 (third clause): a range date that does not parse is an unusable
// answer (intent unknown with an error), never a result with no range; the
// model's own error, when set, is kept.
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

	ctx = settingsUser(t, db, b2LLM(t, `{"intent":"schedule_meeting","range_start":"soon","error":"which week?"}`))
	res, err = (&NLPService{DB: db}).Parse(ctx, "sync soon")
	if err != nil || res.Intent != "unknown" || res.Error != "which week?" {
		t.Fatalf("Parse = %+v, %v; want intent unknown with the model's error", res, err)
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
