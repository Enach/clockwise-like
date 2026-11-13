package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/calendar"
	"github.com/Enach/paceday/backend/internal/oauthfake"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

// PAC-67: availability lookups renew the requester's Outlook or Google
// credential, keep degrading when it is refused (D-6), and never serve a
// result computed from a refused credential after a reconnect (AC-20); jobs
// acting for a user record that user's loss (AC-29).

var (
	pastExpiry   = time.Now().Add(-time.Hour)
	futureExpiry = time.Now().Add(time.Hour)
)

func pac67User(t *testing.T, db *sql.DB, email string) uuid.UUID {
	t.Helper()
	u, err := storage.UpsertUser(db, email, email, "", "google", email)
	if err != nil {
		t.Fatal(err)
	}
	return u.ID
}

func storeOutlookCredential(t *testing.T, db *sql.DB, user uuid.UUID, access, refresh string, expiry time.Time) {
	t.Helper()
	data, _ := json.Marshal(map[string]any{"access_token": access, "refresh_token": refresh, "expiry": expiry.UTC()})
	if _, err := db.Exec(`INSERT INTO settings (user_id, microsoft_tokens, calendar_provider) VALUES ($1, $2, 'outlook')
		ON CONFLICT (user_id) DO UPDATE SET microsoft_tokens = EXCLUDED.microsoft_tokens, calendar_provider = 'outlook'`, user, string(data)); err != nil {
		t.Fatal(err)
	}
}

func lossEntries(t *testing.T, db *sql.DB, user uuid.UUID) []string {
	t.Helper()
	entries, err := storage.ListAuditLog(db, user, 100)
	if err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, e := range entries {
		out = append(out, e.Action+" "+e.Details)
	}
	return out
}

// recordingOutlook stands in for Microsoft Graph's free/busy call: it takes
// the access token from the source it was built with, as the real client does.
type recordingOutlook struct {
	mu     sync.Mutex
	tokens []string
}

func (r *recordingOutlook) factory(ts oauth2.TokenSource) outlookFreeBusy {
	return outlookFunc(func(_ context.Context, emails []string, _, _ time.Time) (map[string][]calendar.TimeSlot, error) {
		tok, err := ts.Token()
		if err != nil {
			return nil, fmt.Errorf("outlook: get token: %w", err)
		}
		r.mu.Lock()
		r.tokens = append(r.tokens, tok.AccessToken)
		r.mu.Unlock()
		out := map[string][]calendar.TimeSlot{}
		for _, e := range emails {
			out[e] = []calendar.TimeSlot{}
		}
		return out, nil
	})
}

type outlookFunc func(ctx context.Context, emails []string, start, end time.Time) (map[string][]calendar.TimeSlot, error)

func (f outlookFunc) GetFreeBusy(ctx context.Context, emails []string, start, end time.Time) (map[string][]calendar.TimeSlot, error) {
	return f(ctx, emails, start, end)
}

func outlookService(db *sql.DB, d *oauthfake.Server, rec *recordingOutlook) *FreeBusyService {
	s := NewFreeBusyService(db, &oauth2.Config{Endpoint: oauth2.Endpoint{TokenURL: d.TokenURL()}})
	s.Microsoft = &auth.MicrosoftConfig{ClientID: "m", ClientSecret: "s", Endpoint: oauth2.Endpoint{AuthURL: d.URL + "/a", TokenURL: d.TokenURL()}}
	s.newOutlook = rec.factory
	return s
}

var lookupDay = time.Date(2030, 1, 7, 9, 0, 0, 0, time.UTC)

// AC-1, AC-2: an Outlook lookup renews A's expired credential with A's refresh
// token, queries with the new token, stores it for A, and never touches B's.
func TestPAC67_OutlookLookupRenewsForTheRequesterOnly(t *testing.T) {
	db := openTestDB(t)
	d := oauthfake.New()
	defer d.Close()
	alice := pac67User(t, db, "pac67-fb-alice@example.com")
	bob := pac67User(t, db, "pac67-fb-bob@example.com")
	storeOutlookCredential(t, db, alice, "a-old", "a-r", pastExpiry)
	storeOutlookCredential(t, db, bob, "b-old", "b-r", pastExpiry)
	bobBefore, _ := auth.LoadMicrosoftToken(db, bob)
	rec := &recordingOutlook{}
	s := outlookService(db, d, rec)

	res, err := s.Query(context.Background(), alice, []string{"x@corp.example"}, lookupDay, lookupDay.Add(8*time.Hour))
	if err != nil || len(res) != 1 || res[0].Coverage != "known" {
		t.Fatalf("Query = %+v, %v", res, err)
	}
	calls := d.Calls("/token")
	if len(calls) != 1 || calls[0].Form.Get("refresh_token") != "a-r" {
		t.Fatalf("renewals = %+v, want one with a-r", calls)
	}
	if len(rec.tokens) != 1 || rec.tokens[0] != "access-1" {
		t.Errorf("calendar call made with %v, want the renewed access-1", rec.tokens)
	}
	if tok, _ := auth.LoadMicrosoftToken(db, alice); tok.AccessToken != "access-1" || !tok.Expiry.After(time.Now()) {
		t.Errorf("alice's stored credential = %+v", tok)
	}
	if tok, _ := auth.LoadMicrosoftToken(db, bob); *tok != *bobBefore || d.Saw("b-r") || d.Saw("b-old") {
		t.Errorf("bob's credential changed or was sent: %+v", tok)
	}
}

// AC-8 Outlook availability row, AC-20: a refused credential degrades the
// lookup to unknown coverage, records one loss, and after a reconnect the
// same lookup within 15 minutes is asked again with the new credential.
func TestPAC67_RefusedLookupIsNotServedAfterReconnect(t *testing.T) {
	db := openTestDB(t)
	d := oauthfake.New()
	defer d.Close()
	d.Answer("a-dead", oauthfake.InvalidGrant)
	alice := pac67User(t, db, "pac67-fb-refused@example.com")
	storeOutlookCredential(t, db, alice, "a-old", "a-dead", pastExpiry)
	rec := &recordingOutlook{}
	s := outlookService(db, d, rec)
	q := func() []ParticipantBusy {
		res, err := s.Query(context.Background(), alice, []string{"x@corp.example"}, lookupDay, lookupDay.Add(8*time.Hour))
		if err != nil {
			t.Fatalf("Query failed instead of degrading: %v", err)
		}
		return res
	}
	if res := q(); res[0].Coverage != "unknown" || len(res[0].Busy) != 0 {
		t.Errorf("refused lookup = %+v, want unknown coverage and no busy periods", res)
	}
	if res := q(); res[0].Coverage != "unknown" {
		t.Errorf("second refused lookup = %+v", res)
	}
	if lost, _ := storage.IsCredentialLost(db, alice, storage.CredentialOutlook); !lost {
		t.Error("the loss is not recorded")
	}
	if got := lossEntries(t, db, alice); len(got) != 1 || got[0] != `connection_lost {"provider":"outlook"}` {
		t.Errorf("entries = %v", got)
	}

	// Reconnect (a Microsoft sign-in) with a working credential.
	if err := auth.SaveMicrosoftToken(db, alice, &oauth2.Token{AccessToken: "a-new", RefreshToken: "a-new-r", Expiry: futureExpiry}); err != nil {
		t.Fatal(err)
	}
	if res := q(); res[0].Coverage != "known" {
		t.Errorf("after reconnect = %+v, want known coverage", res)
	}
	if n := len(rec.tokens); n != 1 || rec.tokens[0] != "a-new" {
		t.Errorf("free/busy requests = %v, want one with the reconnected token", rec.tokens)
	}
}

// Caching after a temporary failure is unchanged (spec section 5).
func TestPAC67_TemporaryFailureLookupIsStillCached(t *testing.T) {
	db := openTestDB(t)
	d := oauthfake.New()
	defer d.Close()
	d.Answer("a-flaky", oauthfake.Unavailable)
	alice := pac67User(t, db, "pac67-fb-flaky@example.com")
	storeOutlookCredential(t, db, alice, "a-old", "a-flaky", pastExpiry)
	s := outlookService(db, d, &recordingOutlook{})
	renewals := 0
	for i := 0; i < 2; i++ {
		res, err := s.Query(context.Background(), alice, []string{"x@corp.example"}, lookupDay, lookupDay.Add(time.Hour))
		if err != nil || res[0].Coverage != "unknown" {
			t.Fatalf("Query = %+v, %v", res, err)
		}
		if i == 0 {
			renewals = len(d.Calls("/token"))
		}
	}
	if renewals == 0 || len(d.Calls("/token")) != renewals {
		t.Errorf("renewal requests %d then %d: the second lookup must be served from the cache as before", renewals, len(d.Calls("/token")))
	}
	if lost, _ := storage.IsCredentialLost(db, alice, storage.CredentialOutlook); lost {
		t.Error("a temporary failure was recorded as a loss")
	}
}

// --- AC-29: jobs acting for B ------------------------------------------------

func googleDouble(t *testing.T, outcome oauthfake.Outcome, refresh string) (*oauthfake.Server, *oauth2.Config) {
	t.Helper()
	d := oauthfake.New()
	t.Cleanup(d.Close)
	d.Answer(refresh, outcome)
	return d, &oauth2.Config{ClientID: "g", Endpoint: oauth2.Endpoint{TokenURL: d.TokenURL()}}
}

func seedGoogle(t *testing.T, db *sql.DB, user uuid.UUID, refresh string) {
	t.Helper()
	if err := auth.UpsertUserToken(db, user, &oauth2.Token{AccessToken: "g-old", RefreshToken: refresh, Expiry: pastExpiry}); err != nil {
		t.Fatal(err)
	}
}

type pac67Job struct {
	name string
	run  func(t *testing.T, db *sql.DB, cfg *oauth2.Config, user uuid.UUID) error
}

func pac67Jobs() []pac67Job {
	return []pac67Job{
		{"auto-decline", func(t *testing.T, db *sql.DB, cfg *oauth2.Config, user uuid.UUID) error {
			if _, err := db.Exec(`UPDATE settings SET auto_decline_outside_working_hours = TRUE WHERE user_id = $1`, user); err != nil {
				t.Fatal(err)
			}
			_, err := (&AutoDeclineService{DB: db, OAuthConfig: cfg}).RunForUser(context.Background(), user, time.Now())
			return err
		}},
		{"personal-blocker sync", func(t *testing.T, db *sql.DB, cfg *oauth2.Config, user uuid.UUID) error {
			day := time.Now().UTC().Add(48 * time.Hour)
			for day.Weekday() == time.Saturday || day.Weekday() == time.Sunday {
				day = day.Add(24 * time.Hour)
			}
			start := time.Date(day.Year(), day.Month(), day.Day(), 10, 0, 0, 0, time.UTC)
			ics := fmt.Sprintf("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VEVENT\r\nUID:pac67-personal\r\nSUMMARY:Dentist\r\nDTSTART:%s\r\nDTEND:%s\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n",
				start.Format("20060102T150405Z"), start.Add(time.Hour).Format("20060102T150405Z"))
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(ics)) }))
			t.Cleanup(srv.Close)
			if _, err := storage.InsertPersonalCalendar(db, &storage.PersonalCalendar{UserID: user, Provider: "webcal", Name: "Home", URL: srv.URL, Enabled: true}); err != nil {
				t.Fatal(err)
			}
			return (&PersonalBlocker{DB: db, OAuthConfig: cfg}).SyncAllForUser(context.Background(), user)
		}},
		{"weekly manager team detection", func(t *testing.T, db *sql.DB, cfg *oauth2.Config, user uuid.UUID) error {
			_, err := (&ManagerEngine{DB: db, OAuthConfig: cfg}).DetectTeam(context.Background(), user)
			return err
		}},
	}
}

func TestPAC67_JobsRecordTheOwnersLoss(t *testing.T) {
	for _, job := range pac67Jobs() {
		t.Run(job.name, func(t *testing.T) {
			db := openTestDB(t)
			bob := pac67User(t, db, "pac67-job-bob@example.com")
			other := pac67User(t, db, "pac67-job-other@example.com")
			if _, err := storage.GetOrCreateSettingsByUser(db, bob); err != nil {
				t.Fatal(err)
			}

			// Today's outcome when B's calendar cannot be used: a temporary failure.
			_, flakyCfg := googleDouble(t, oauthfake.Unavailable, "b-flaky")
			seedGoogle(t, db, bob, "b-flaky")
			flakyErr := job.run(t, db, flakyCfg, bob)
			if lost, _ := storage.IsCredentialLost(db, bob, storage.CredentialGoogle); lost {
				t.Error("a temporary failure was recorded as a loss")
			}
			if got := lossEntries(t, db, bob); len(got) != 0 {
				t.Errorf("entries after a temporary failure = %v", got)
			}

			_, deadCfg := googleDouble(t, oauthfake.InvalidGrant, "b-dead")
			seedGoogle(t, db, bob, "b-dead")
			deadErr := job.run(t, db, deadCfg, bob)
			if (flakyErr == nil) != (deadErr == nil) {
				t.Errorf("job outcome changed: temporary %v, refused %v", flakyErr, deadErr)
			}
			if lost, _ := storage.IsCredentialLost(db, bob, storage.CredentialGoogle); !lost {
				t.Error("bob's loss is not recorded")
			}
			if got := lossEntries(t, db, bob); len(got) != 1 || got[0] != `connection_lost {"provider":"google"}` {
				t.Errorf("bob's entries = %v", got)
			}
			if got := lossEntries(t, db, other); len(got) != 0 {
				t.Errorf("another user's log changed: %v", got)
			}
		})
	}
}
