package api

// PAC-122 phase 2, batch 2 (calendar; light rows): SF-035 (blockers are
// tagged), SF-036 (a credential read failure is not "disconnected"),
// SF-069 (no orphaned provider meeting) and SF-070 (audit details are JSON).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/internal/lifecycle/lifecycletest"
	"github.com/Enach/paceday/backend/nlp"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
	googlecalendar "google.golang.org/api/calendar/v3"
)

// SF-035: a personal-calendar blocker is is_personal_block:true on the
// wire; a meeting (even one titled like a blocker) carries no such key.
func TestPAC122_SF035_PersonalBlockIsTagged(t *testing.T) {
	cases := []struct {
		name string
		ev   *googlecalendar.Event
		want bool
	}{
		{"marked blocker", &googlecalendar.Event{Id: "b1", Summary: "[Personal] Busy", ExtendedProperties: &googlecalendar.EventExtendedProperties{Private: map[string]string{"paceday_personal_blocker": "1"}}}, true},
		{"blocker created before the marker", &googlecalendar.Event{Id: "b2", Summary: "[Personal] Busy", Description: "Auto-created personal time blocker"}, true},
		{"a user's own event with the blocker's title", &googlecalendar.Event{Id: "m1", Summary: "[Personal] Busy"}, false},
		{"meeting", &googlecalendar.Event{Id: "m2", Summary: "1:1"}, false},
	}
	for _, tc := range cases {
		body, err := json.Marshal(toCalendarEventDTO(tc.ev))
		if err != nil {
			t.Fatal(err)
		}
		has := strings.Contains(string(body), `"is_personal_block":true`)
		if has != tc.want {
			t.Errorf("%s: %s, want is_personal_block %v", tc.name, body, tc.want)
		}
		if !tc.want && strings.Contains(string(body), "is_personal_block") {
			t.Errorf("%s: the key is present on a non-blocker: %s", tc.name, body)
		}
	}
}

// SF-036: a Google or Microsoft credential that cannot be read is the
// documented 500, never a "connected: false" row (an undecryptable one
// still reads as not connected: TestPAC84_ConferencingUndecryptableIsTheDocumented500).
func TestPAC122_SF036_CredentialReadFailureIs500(t *testing.T) {
	t.Run("google", func(t *testing.T) {
		w := newPAC67World(t)
		u := w.user("sf036-google-" + uuid.NewString()[:6] + "@pac122.test")
		w.seedGoogle(u, "g", "g-r", pac67Future)
		prev := loadGoogleTokenForStatus
		loadGoogleTokenForStatus = func(context.Context, storage.DBTX, uuid.UUID) (*oauth2.Token, error) {
			return nil, errors.New("pac122 forced credential read failure")
		}
		t.Cleanup(func() { loadGoogleTokenForStatus = prev })
		assertBody(t, w.do(u, http.MethodGet, "/api/conference/providers", ""), http.StatusInternalServerError, internalError)
	})
	t.Run("microsoft", func(t *testing.T) {
		w := newPAC67World(t)
		u := w.user("sf036-ms-" + uuid.NewString()[:6] + "@pac122.test")
		w.seedGoogle(u, "g", "g-r", pac67Future)
		// A stored value that opens but is not a credential record.
		w.exec(`INSERT INTO settings (user_id) VALUES ($1) ON CONFLICT (user_id) DO NOTHING`, u)
		w.exec(`UPDATE settings SET microsoft_tokens = 'not a credential' WHERE user_id = $1`, u)
		assertBody(t, w.do(u, http.MethodGet, "/api/conference/providers", ""), http.StatusInternalServerError, internalError)
	})
	t.Run("no credential is still disconnected", func(t *testing.T) {
		w := newPAC67World(t)
		u := w.user("sf036-none-" + uuid.NewString()[:6] + "@pac122.test")
		w.assertRow(u, "google_meet", false)
		w.assertRow(u, "teams", false)
	})
}

// SF-069: when the event update fails after the Zoom or Teams meeting was
// created, the meeting is deleted at the provider; a failed delete is
// reported with the provider and meeting id.
func TestPAC122_SF069_UnattachedMeetingIsDeleted(t *testing.T) {
	for _, tc := range []struct{ provider, deletePath string }{
		{"zoom", "/api/meetings/81"},
		{"teams", "/api/me/onlineMeetings/m-1"},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			rec := lifecycletest.Record(t)
			w := newPAC67World(t)
			u := w.user("sf069-" + tc.provider + "-" + uuid.NewString()[:6] + "@pac122.test")
			w.seedGoogle(u, "g-live-"+tc.provider, "g-r", pac67Future)
			if tc.provider == "teams" {
				w.seedOutlook(u, "ms", "ms-r", pac67Future)
				w.providers(u, "google", "teams")
			} else {
				w.seedZoom(u, "z", "z-r", &pac67Future)
			}
			w.cal.mu.Lock()
			w.cal.failPut = true
			w.cal.mu.Unlock()

			assertBody(t, w.do(u, http.MethodPost, "/api/events/e1/conference", `{"provider":"`+tc.provider+`"}`), http.StatusInternalServerError, internalError)
			if n := len(w.d.Calls(tc.deletePath)); n != 1 {
				t.Fatalf("%d deletes of the unattached meeting, want 1", n)
			}
			if got := rec.Matching("conference-meeting-cleanup", ""); len(got) != 0 {
				t.Fatalf("a successful delete was reported: %+v", got)
			}

			// The provider refuses the delete: reported, by ids only.
			w.d.APIAnswerFor(strings.TrimPrefix(tc.deletePath, "/api"), http.StatusInternalServerError, `{"error":"stub"}`)
			assertBody(t, w.do(u, http.MethodPost, "/api/events/e1/conference", `{"provider":"`+tc.provider+`"}`), http.StatusInternalServerError, internalError)
			got := rec.Matching("conference-meeting-cleanup", "")
			if len(got) != 1 || !strings.HasPrefix(got[0].Unit.ID, tc.provider+"/") || strings.Contains(got[0].Err.Error(), "@") {
				t.Fatalf("reports = %+v", rec.All())
			}
		})
	}
}

// SF-070: a title with a quote (or an id with one) leaves valid JSON details.
func TestPAC122_SF070_AuditDetailsAreJSON(t *testing.T) {
	db := openTestDB(t)
	u := createTestUser(t, "sf070-"+uuid.NewString()[:6]+"@pac122.test")
	start := time.Now().UTC().Add(time.Hour)
	title := `Q3 "kick-off" \ review`

	smart := &mockScheduler{created: &googlecalendar.Event{Id: `evt"1`, Summary: title}}
	sh := &scheduleHandlers{eng: &mockCompressor{}, smart: smart, db: db}
	b, _ := json.Marshal(map[string]any{"title": title, "start": start.Format(time.RFC3339), "end": start.Add(time.Hour).Format(time.RFC3339)})
	w := httptest.NewRecorder()
	sh.createMeeting(w, withUser(httptest.NewRequest(http.MethodPost, "/api/schedule/create", bytes.NewReader(b)), u))
	if w.Code != http.StatusOK {
		t.Fatalf("create = %d %s", w.Code, w.Body.String())
	}

	nh := &nlpHandlers{svc: &mockNLPParser{}, smart: smart, db: db}
	b, _ = json.Marshal(map[string]any{
		"parse_result":        nlp.ParseResult{Intent: "schedule_meeting", Title: title, SuggestedSlots: []engine.SuggestedSlot{{Start: start, End: start.Add(time.Hour)}}},
		"selected_slot_index": 0,
	})
	w = httptest.NewRecorder()
	nh.confirm(w, withUser(httptest.NewRequest(http.MethodPost, "/api/nlp/confirm", bytes.NewReader(b)), u))
	if w.Code != http.StatusOK {
		t.Fatalf("confirm = %d %s", w.Code, w.Body.String())
	}

	entries, err := storage.ListAuditLog(db, u, storage.AuditMaxLimit)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, e := range entries {
		var details map[string]string
		if err := json.Unmarshal([]byte(e.Details), &details); err != nil {
			t.Errorf("%s details are not JSON: %s", e.Action, e.Details)
			continue
		}
		seen[e.Action] = true
		if details["event_id"] != `evt"1` {
			t.Errorf("%s event_id = %q", e.Action, details["event_id"])
		}
		if e.Action == "meeting_created" && details["title"] != title {
			t.Errorf("meeting_created title = %q", details["title"])
		}
	}
	if !seen["meeting_created"] || !seen["nlp_confirmed"] {
		t.Fatalf("entries = %+v", entries)
	}
}
