package engine

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/internal/lifecycle"
	"github.com/Enach/paceday/backend/internal/secretbox"
	"github.com/Enach/paceday/backend/storage"
	"golang.org/x/oauth2"
	googlecalendar "google.golang.org/api/calendar/v3"
)

// PAC-84 AC-17 (review #7): every cron skips only the user or item whose
// secret cannot be decrypted, makes no provider call with it, records no
// loss, and processes the others.

const pac84Undecryptable = "pdenc:1:2147483000:BwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcH"

func TestPAC84_DailyRecapSkipsOnlyTheUndecryptableUser(t *testing.T) {
	pac81Scale(t)
	db := openTestDB(t)
	var mu sync.Mutex
	var bearers []string
	slack := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/chat.postMessage") {
			mu.Lock()
			bearers = append(bearers, r.Header.Get("Authorization"))
			mu.Unlock()
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "message": map[string]string{"ts": "1.2"}})
	}))
	defer slack.Close()
	prev := slackAPIBase
	slackAPIBase = slack.URL
	defer func() { slackAPIBase = prev }()

	now := time.Date(2026, 11, 2, 8, 30, 0, 0, time.UTC)
	var good, bad storage.User
	for i, email := range []string{"pac84-recap-good@pac84.test", "pac84-recap-bad@pac84.test"} {
		u, err := storage.UpsertUser(db, email, email, "", "google", email)
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
			BotToken: "xoxb-" + email, WorkspaceID: "W", Scopes: []string{}}); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			good = *u
		} else {
			bad = *u
		}
	}
	if _, err := db.Exec(`UPDATE workspace_connections SET bot_token = $2 WHERE user_id = $1`, bad.ID, pac84Undecryptable); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
	(&DailyRecapService{DB: db, Clock: FixedClock{T: now}}).RunAll(ctx)
	cancel()
	lifecycle.Default().Wait(context.Background())
	if len(bearers) != 1 || bearers[0] != "Bearer xoxb-"+good.Email {
		t.Fatalf("Slack posts with %v, want exactly the good user's token", bearers)
	}
	var n int
	_ = db.QueryRow(`SELECT count(*) FROM recap_sends WHERE user_id = $1`, bad.ID).Scan(&n)
	if n != 0 {
		t.Fatal("a recap was recorded for the undecryptable user")
	}
}

func TestPAC84_MeetingBriefSkipsUndecryptableContextTokens(t *testing.T) {
	pac81Scale(t)
	db := openTestDB(t)
	var calls atomic.Int32
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"ok":true,"messages":{"matches":[]},"results":[]}`))
	}))
	defer stub.Close()
	prevS, prevN := slackAPIBase, notionAPIBase
	slackAPIBase, notionAPIBase = stub.URL, stub.URL
	defer func() { slackAPIBase, notionAPIBase = prevS, prevN }()

	u, err := storage.UpsertUser(db, "pac84-brief@pac84.test", "B", "", "google", "pac84-brief")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"slack", "notion"} {
		if err := storage.UpsertWorkspaceConnection(db, &storage.WorkspaceConnection{UserID: u.ID, Provider: p, AccessToken: "tok-" + p,
			BotToken: "bot-" + p, WorkspaceID: "W", Scopes: []string{}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`UPDATE workspace_connections SET access_token = $2, bot_token = $2 WHERE user_id = $1`, u.ID, pac84Undecryptable); err != nil {
		t.Fatal(err)
	}
	brief, err := (&MeetingBriefService{DB: db}).Generate(context.Background(), u.ID,
		&googlecalendar.Event{Id: "pac84-ev", Summary: "Quarterly planning review"}, true)
	if err != nil || brief == nil {
		t.Fatalf("Generate: %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("%d Slack/Notion calls with undecryptable tokens", calls.Load())
	}
	// Control: with readable tokens the same brief does call out.
	for _, p := range []string{"slack", "notion"} {
		_ = storage.UpsertWorkspaceConnection(db, &storage.WorkspaceConnection{UserID: u.ID, Provider: p, AccessToken: "tok-" + p,
			BotToken: "bot-" + p, WorkspaceID: "W", Scopes: []string{}})
	}
	_, _ = (&MeetingBriefService{DB: db}).Generate(context.Background(), u.ID,
		&googlecalendar.Event{Id: "pac84-ev", Summary: "Quarterly planning review"}, true)
	if calls.Load() == 0 {
		t.Fatal("control: readable tokens made no call; the test is not observing the fetches")
	}
}

// Auto-decline and auto-schedule (focus time): an undecryptable Google
// token is an error with errors.Is ErrDecrypt for that user only, with no
// token renewal attempt and no loss recorded.
func TestPAC84_AutoDeclineAndAutoScheduleUndecryptableToken(t *testing.T) {
	db := openTestDB(t)
	var tokenCalls atomic.Int32
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		tokenCalls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer idp.Close()
	cfg := &oauth2.Config{ClientID: "c", ClientSecret: "s", Endpoint: oauth2.Endpoint{TokenURL: idp.URL}}

	u, err := storage.UpsertUser(db, "pac84-decline@pac84.test", "D", "", "google", "pac84-decline")
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.UpsertUserToken(db, u.ID, &oauth2.Token{AccessToken: "a", RefreshToken: "r", Expiry: time.Now().Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE oauth_tokens SET refresh_token = $2 WHERE user_id = $1`, u.ID, pac84Undecryptable); err != nil {
		t.Fatal(err)
	}
	s, _ := storage.GetOrCreateSettingsByUser(db, u.ID)
	s.AutoDeclineOutsideWorkingHours = true
	if err := storage.SaveSettingsForUser(db, u.ID, s); err != nil {
		t.Fatal(err)
	}

	_, err = (&AutoDeclineService{DB: db, OAuthConfig: cfg}).RunForUser(context.Background(), u.ID, time.Now())
	if !errors.Is(err, secretbox.ErrDecrypt) {
		t.Fatalf("auto-decline: %v, want ErrDecrypt", err)
	}
	if err := (&AutoDeclineService{DB: db, OAuthConfig: cfg}).RunAll(context.Background()); !errors.Is(err, secretbox.ErrDecrypt) {
		t.Fatalf("auto-decline RunAll: %v", err)
	}
	_, err = (&FocusTimeEngine{DB: db, OAuthConfig: cfg}).RunForUser(context.Background(), u.ID, time.Now())
	if !errors.Is(err, secretbox.ErrDecrypt) || !errors.Is(err, ErrCredentialStore) {
		t.Fatalf("auto-schedule: %v, want ErrCredentialStore wrapping ErrDecrypt", err)
	}
	if tokenCalls.Load() != 0 {
		t.Fatal("a renewal was attempted with an undecryptable token")
	}
	if lost, _ := storage.IsCredentialLost(db, u.ID, storage.CredentialGoogle); lost {
		t.Fatal("marked lost")
	}
}
