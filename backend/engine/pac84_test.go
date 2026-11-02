package engine

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/internal/secretbox"
	"github.com/Enach/paceday/backend/storage"
	"golang.org/x/oauth2"
)

// PAC-84 AC-18: a host whose stored calendar credential cannot be decrypted
// is unreadable (busy, D-6), never a storage failure (D-7, the 500); and
// on an act path the same failure is ErrCredentialStore (500), never "not
// connected".
func TestPAC84_UndecryptableHostIsBusyNotAStorageFailure(t *testing.T) {
	db := openTestDB(t)
	host := pac72EngineUser(t, db, "pac84-host@example.com")
	if err := auth.UpsertUserToken(db, host, &oauth2.Token{AccessToken: "a", RefreshToken: "r", Expiry: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	// AC-5 state: the access token copied into the refresh-token column.
	if _, err := db.Exec(`UPDATE oauth_tokens SET refresh_token = access_token WHERE user_id = $1`, host); err != nil {
		t.Fatal(err)
	}
	link := pac72EngineLink(t, db, host, "pac84-undecryptable", host)
	slots, err := (&BookingEngine{DB: db}).CollectiveSlots(context.Background(), link, pac72Monday, 30)
	if err != nil {
		t.Fatalf("CollectiveSlots failed (a decryption failure is not a D-7 storage failure): %v", err)
	}
	if len(slots) != 0 {
		t.Fatalf("offered %d slots; an unreadable host is busy", len(slots))
	}
	_, err = LoadCalendarToken(db, host)
	if !errors.Is(err, ErrCredentialStore) || !errors.Is(err, secretbox.ErrDecrypt) || errors.Is(err, ErrCalendarNotConnected) {
		t.Fatalf("LoadCalendarToken = %v", err)
	}
	if lost, _ := storage.IsCredentialLost(db, host, storage.CredentialGoogle); lost {
		t.Fatal("marked lost")
	}
}

// PAC-84 AC-17: the personal-blocker cron skips only the calendar whose
// secret cannot be decrypted (no fetch with it) and processes the others.
func TestPAC84_PersonalBlockerSkipsOnlyTheUndecryptableCalendar(t *testing.T) {
	db := openTestDB(t)
	user := pac72EngineUser(t, db, "pac84-blocker@example.com")
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nEND:VCALENDAR\r\n"))
	}))
	defer srv.Close()
	good, err := storage.InsertPersonalCalendar(db, &storage.PersonalCalendar{UserID: user, Provider: "webcal", Name: "Good", URL: srv.URL, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	bad, err := storage.InsertPersonalCalendar(db, &storage.PersonalCalendar{UserID: user, Provider: "webcal", Name: "Bad", URL: "https://unused.invalid", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	// The good calendar's sealed url copied to the bad row: bound to
	// another row, it cannot be decrypted there.
	if _, err := db.Exec(`UPDATE personal_calendars SET url = (SELECT url FROM personal_calendars WHERE id = $1) WHERE id = $2`, good, bad); err != nil {
		t.Fatal(err)
	}
	if err := (&PersonalBlocker{DB: db}).SyncAllForUser(context.Background(), user); err != nil {
		t.Fatalf("SyncAllForUser: %v", err)
	}
	if n := hits.Load(); n != 1 {
		t.Fatalf("%d fetches, want 1 (the good calendar only)", n)
	}
}
