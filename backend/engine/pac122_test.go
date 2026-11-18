package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/internal/lifecycle/lifecycletest"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

// PAC-122 phase 2, batch O: background failures in the engine reach the
// lifecycle reporter (Sentry in the backend). Behaviour is unchanged.

// SF-098: auto-decline returns every user's failure, not only the first.
func TestPAC122_AutoDeclineReturnsEveryFailure(t *testing.T) {
	db := openTestDB(t)
	var users []uuid.UUID
	for _, email := range []string{"pac122-decline-a@pac122.test", "pac122-decline-b@pac122.test"} {
		u := pac72EngineUser(t, db, email)
		s, _ := storage.GetOrCreateSettingsByUser(db, u)
		s.AutoDeclineOutsideWorkingHours = true
		if err := storage.SaveSettingsForUser(db, u, s); err != nil {
			t.Fatal(err)
		}
		users = append(users, u)
	}
	// No OAuth configuration: every enabled user's run fails.
	err := (&AutoDeclineService{DB: db}).RunAll(context.Background())
	if err == nil {
		t.Fatal("RunAll succeeded with every user failing")
	}
	for _, u := range users {
		if !strings.Contains(err.Error(), u.String()) {
			t.Errorf("RunAll error %q does not name user %s", err, u)
		}
	}
}

// pac122Recap is a user whose recap is due now (08:30 UTC, to a channel)
// with a Slack connection, and a Slack stand-in that accepts or refuses.
func pac122Recap(t *testing.T, ok bool) (*DailyRecapService, uuid.UUID, func() int) {
	t.Helper()
	pac81Scale(t)
	db := openTestDB(t)
	var posts atomic.Int32
	slack := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/chat.postMessage") {
			posts.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		if ok {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "ts": "1.2", "message": map[string]string{"ts": "1.2"}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "channel_not_found"})
	}))
	t.Cleanup(slack.Close)
	prev := slackAPIBase
	slackAPIBase = slack.URL
	t.Cleanup(func() { slackAPIBase = prev })

	u, err := storage.UpsertUser(db, "pac122-recap@pac122.test", "R", "", "google", "pac122-recap")
	if err != nil {
		t.Fatal(err)
	}
	s, _ := storage.GetOrCreateSettingsByUser(db, u.ID)
	s.RecapEnabled, s.RecapSendTime, s.RecapSendTo, s.RecapChannelID, s.Timezone = true, "08:30", "channel", "C1", "UTC"
	if err := storage.SaveSettingsForUser(db, u.ID, s); err != nil {
		t.Fatal(err)
	}
	if err := storage.SaveRecapSettingsForUser(db, u.ID, s); err != nil {
		t.Fatal(err)
	}
	if err := storage.UpsertWorkspaceConnection(db, &storage.WorkspaceConnection{UserID: u.ID, Provider: "slack", AccessToken: "a",
		BotToken: "xoxb-pac122", WorkspaceID: "W", Scopes: []string{}}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 2, 8, 30, 0, 0, time.UTC)
	svc := &DailyRecapService{DB: db, Clock: FixedClock{T: now}}
	return svc, u.ID, func() int { return int(posts.Load()) }
}

// SF-082 (reporting half): a failed Slack send is reported; it is still
// recorded as failed, as before.
func TestPAC122_RecapSendFailureReported(t *testing.T) {
	rec := lifecycletest.Record(t)
	svc, user, posts := pac122Recap(t, false)
	svc.RunForUser(context.Background(), user, "R")
	if posts() != 1 {
		t.Fatalf("%d posts, want 1", posts())
	}
	got := rec.Matching("daily-recap", "send error")
	if len(got) != 1 || got[0].Unit.ID != user.String() {
		t.Fatalf("reports %+v", rec.All())
	}
	var status string
	if err := svc.DB.QueryRow(`SELECT status FROM recap_sends WHERE user_id = $1`, user).Scan(&status); err != nil || status != "failed" {
		t.Fatalf("recorded %q, %v; want failed", status, err)
	}
}

// SF-082 (reporting half): a failed record of the send is reported, no
// longer dropped; the send itself is unchanged.
func TestPAC122_RecapInsertFailureReported(t *testing.T) {
	rec := lifecycletest.Record(t)
	svc, user, posts := pac122Recap(t, true)
	// The record's write fails while its read works (PAC-122 batch 5: a
	// failed read of whether the recap was sent now stops the send, SF-082).
	if _, err := svc.DB.Exec(`CREATE FUNCTION pac122_refuse_recap_send() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'recap_sends write refused'; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DB.Exec(`CREATE TRIGGER pac122_refuse_recap_send BEFORE INSERT OR UPDATE ON recap_sends
		FOR EACH ROW EXECUTE FUNCTION pac122_refuse_recap_send()`); err != nil {
		t.Fatal(err)
	}
	svc.RunForUser(context.Background(), user, "R")
	if posts() != 1 {
		t.Fatalf("%d posts, want 1", posts())
	}
	got := rec.Matching("daily-recap", "record sent send")
	if len(got) != 1 || got[0].Unit.ID != user.String() {
		t.Fatalf("reports %+v", rec.All())
	}
	if !strings.Contains(got[0].Err.Error(), "recap_sends") {
		t.Fatalf("report %q does not carry the storage failure", got[0].Err)
	}
}

// SF-042 (reporting half): a calendar whose sync fails is reported by its
// id; SyncAllForUser still returns nil and the other calendar still syncs.
func TestPAC122_PersonalCalendarSyncFailureReported(t *testing.T) {
	rec := lifecycletest.Record(t)
	db := openTestDB(t)
	user := pac72EngineUser(t, db, "pac122-blocker@example.com")
	var goodHits atomic.Int32
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		goodHits.Add(1)
		_, _ = w.Write([]byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nEND:VCALENDAR\r\n"))
	}))
	defer good.Close()
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer broken.Close()
	// The readable calendar fails later, at the work calendar (the user has
	// none connected); the broken one fails at its read.
	goodID, err := storage.InsertPersonalCalendar(db, &storage.PersonalCalendar{UserID: user, Provider: "webcal", Name: "Gym", URL: good.URL, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	badID, err := storage.InsertPersonalCalendar(db, &storage.PersonalCalendar{UserID: user, Provider: "webcal", Name: "Dentist", URL: broken.URL, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := (&PersonalBlocker{DB: db}).SyncAllForUser(context.Background(), user); err != nil {
		t.Fatalf("SyncAllForUser: %v (behaviour changed)", err)
	}
	if goodHits.Load() != 1 {
		t.Fatalf("good calendar fetched %d times", goodHits.Load())
	}
	byID := map[string]string{}
	for _, r := range rec.Matching("personal-calendar-sync", "") {
		byID[r.Unit.ID] = r.Err.Error()
	}
	if len(byID) != 2 || !strings.Contains(byID[strconv.FormatInt(badID, 10)], "read events") ||
		!strings.Contains(byID[strconv.FormatInt(goodID, 10)], "calendar not connected") {
		t.Fatalf("reports %+v", rec.All())
	}
	for _, msg := range byID {
		if strings.Contains(msg, "Dentist") || strings.Contains(msg, "Gym") {
			t.Fatalf("report carries a calendar name: %s", msg)
		}
	}
}

// PAC-122 review blocker 2: a webcal URL is a secret and may carry an
// address; an unreachable feed's failure reaches neither the report nor the
// log with any part of its path or query.
func TestPAC122_WebcalFailureCarriesNoURL(t *testing.T) {
	rec := lifecycletest.Record(t)
	var logs bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(prev) })
	db := openTestDB(t)
	user := pac72EngineUser(t, db, "pac122-webcal@example.com")
	// A closed listener: the fetch fails to connect.
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()
	secretURL := deadURL + "/calendar/ical/jane.doe%40example.com/private-s3cr3tt0ken/basic.ics"
	id, err := storage.InsertPersonalCalendar(db, &storage.PersonalCalendar{UserID: user, Provider: "webcal", Name: "Private", URL: secretURL, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := (&PersonalBlocker{DB: db}).SyncAllForUser(context.Background(), user); err != nil {
		t.Fatal(err)
	}
	got := rec.Matching("personal-calendar-sync", "fetch")
	if len(got) != 1 || got[0].Unit.ID != strconv.FormatInt(id, 10) {
		t.Fatalf("the fetch failure was not reported: %+v", rec.All())
	}
	for _, leak := range []string{"%40", "jane.doe", "s3cr3tt0ken", "basic.ics"} {
		if strings.Contains(got[0].Err.Error(), leak) {
			t.Errorf("report carries %q: %v", leak, got[0].Err)
		}
		if strings.Contains(logs.String(), leak) {
			t.Errorf("log carries %q: %s", leak, logs.String())
		}
	}
}
