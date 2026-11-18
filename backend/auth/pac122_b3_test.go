package auth

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/internal/secretbox"
	"golang.org/x/oauth2"
)

// failingOutlookRead is a Querier whose read of the stored Outlook
// credential fails; every other statement runs on the database.
type failingOutlookRead struct{ *sql.DB }

func (f failingOutlookRead) QueryRow(q string, args ...any) *sql.Row {
	if strings.Contains(q, "microsoft_tokens FROM settings") {
		return f.DB.QueryRow(`SELECT (1/0)::text`)
	}
	return f.DB.QueryRow(q, args...)
}

// PAC-122 SF-007: a failed read of the Outlook credential is an error, never
// "no credential" (as LoadUserToken). No row and an empty column still are.
func TestPAC122_SF007_OutlookReadFailureIsAnError(t *testing.T) {
	db := openTestDB(t)
	uid := seedUser(t, db, "pac122-sf007-load@example.com")
	if tok, err := LoadMicrosoftToken(db, uid); tok != nil || err != nil {
		t.Fatalf("no settings row = %v, %v; want nil, nil", tok, err)
	}
	if _, err := db.Exec(`INSERT INTO settings (user_id) VALUES ($1)`, uid); err != nil {
		t.Fatal(err)
	}
	if tok, err := LoadMicrosoftToken(db, uid); tok != nil || err != nil {
		t.Fatalf("no stored credential = %v, %v; want nil, nil", tok, err)
	}
	if err := SaveMicrosoftToken(db, uid, &oauth2.Token{AccessToken: "a1", RefreshToken: "r1", Expiry: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	tok, err := LoadMicrosoftToken(failingOutlookRead{db}, uid)
	if err == nil || tok != nil {
		t.Fatalf("failed read = %v, %v; want the error", tok, err)
	}
}

// PAC-122 SF-007: a store without a refresh token keeps the stored one; when
// the stored one cannot be read the store is refused, so the credential is
// never replaced by one that cannot be renewed.
func TestPAC122_SF007_StoreWithoutRefreshRefusedOnReadFailure(t *testing.T) {
	db := openTestDB(t)
	uid := seedUser(t, db, "pac122-sf007-store@example.com")
	if err := SaveMicrosoftToken(db, uid, &oauth2.Token{AccessToken: "a1", RefreshToken: "r1", Expiry: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := StoreMicrosoftCredential(failingOutlookRead{db}, uid, &oauth2.Token{AccessToken: "a2", Expiry: time.Now().Add(time.Hour)}, false); err == nil {
		t.Fatal("a store whose refresh token could not be carried over succeeded")
	}
	got, err := LoadMicrosoftToken(db, uid)
	if err != nil || got == nil || got.AccessToken != "a1" || got.RefreshToken != "r1" {
		t.Fatalf("stored credential = %+v, %v; want a1/r1 untouched", got, err)
	}
	// Unchanged: a readable stored credential gives its refresh token.
	if err := StoreMicrosoftCredential(db, uid, &oauth2.Token{AccessToken: "a3", Expiry: time.Now().Add(time.Hour)}, false); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadMicrosoftToken(db, uid); err != nil || got.AccessToken != "a3" || got.RefreshToken != "r1" {
		t.Fatalf("after a store without refresh = %+v, %v; want a3/r1", got, err)
	}
}

// PAC-122 SF-007 keeps PAC-84: a store without a refresh token over a stored
// credential that cannot be decrypted replaces it (the old refresh token is
// unrecoverable), rather than being refused like a failed read.
func TestPAC122_SF007_StoreOverUndecryptableReplacesIt(t *testing.T) {
	db := openTestDB(t)
	uid := seedUser(t, db, "pac122-sf007-pac84@example.com")
	if err := SaveMicrosoftToken(db, uid, &oauth2.Token{AccessToken: "a1", RefreshToken: "r1", Expiry: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	undecryptable := "pdenc:1:2147483000:" + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 48))
	if _, err := db.Exec(`UPDATE settings SET microsoft_tokens = $2 WHERE user_id = $1`, uid, undecryptable); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadMicrosoftToken(db, uid); !errors.Is(err, secretbox.ErrDecrypt) {
		t.Fatalf("fixture: load = %v, want a decryption failure", err)
	}
	if err := StoreMicrosoftCredential(db, uid, &oauth2.Token{AccessToken: "a2", Expiry: time.Now().Add(time.Hour)}, false); err != nil {
		t.Fatalf("store over an undecryptable credential = %v, want it replaced", err)
	}
	got, err := LoadMicrosoftToken(db, uid)
	if err != nil || got == nil || got.AccessToken != "a2" || got.RefreshToken != "" {
		t.Fatalf("stored credential = %+v, %v; want a2 with no refresh token", got, err)
	}
}
