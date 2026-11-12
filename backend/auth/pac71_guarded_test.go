package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

// PAC-71 in-request re-check on the calendar credentials (contract §5): a
// load for a request whose session generation is no longer the account's
// returns nothing and marks the request stale.
func TestPAC71_CredentialLoadsReCheckTheSessionGeneration(t *testing.T) {
	db := openTestDB(t)
	u := seedUser(t, db, "pac71-guard@example.com")
	if err := UpsertUserToken(db, u, &oauth2.Token{AccessToken: "g", RefreshToken: "r", Expiry: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := StoreMicrosoftCredential(db, u, &oauth2.Token{AccessToken: "m", RefreshToken: "r"}, false); err != nil {
		t.Fatal(err)
	}
	var cp string
	_ = db.QueryRow(`SELECT calendar_provider FROM settings WHERE user_id = $1`, u).Scan(&cp)
	if cp != "google" {
		t.Fatalf("a connect-style store changed the work calendar to %q", cp)
	}
	current := storage.WithSessionGuard(context.Background(), &storage.SessionGuard{UserID: u, Gen: 0})
	if tok, err := LoadUserTokenCtx(current, db, u); err != nil || tok.AccessToken != "g" {
		t.Fatalf("current: %v %v", tok, err)
	}
	if tok, err := LoadMicrosoftTokenCtx(current, db, u); err != nil || tok.AccessToken != "m" {
		t.Fatalf("current ms: %v %v", tok, err)
	}
	if _, err := db.Exec(`UPDATE users SET session_generation = 1 WHERE id = $1`, u); err != nil {
		t.Fatal(err)
	}
	g := &storage.SessionGuard{UserID: u, Gen: 0}
	stale := storage.WithSessionGuard(context.Background(), g)
	if tok, err := LoadUserTokenCtx(stale, db, u); !errors.Is(err, storage.ErrStaleSession) || tok != nil || !g.Stale() {
		t.Fatalf("stale google: %v %v", tok, err)
	}
	if tok, err := LoadMicrosoftTokenCtx(stale, db, u); !errors.Is(err, storage.ErrStaleSession) || tok != nil {
		t.Fatalf("stale ms: %v %v", tok, err)
	}
	if _, err := LoadUserTokenCtx(stale, db, uuid.Nil); !errors.Is(err, ErrNoUser) {
		t.Fatal("nil user")
	}
	// The renewal store loads through the re-check too.
	src := UserTokenSource(stale, db, &oauth2.Config{}, u, &oauth2.Token{AccessToken: "old", Expiry: time.Now().Add(-time.Hour)})
	if _, err := src.Token(); err == nil {
		t.Fatal("a stale request renewed a credential")
	}
}

func TestPAC71_OIDCEmailUnverified(t *testing.T) {
	for raw, want := range map[string]bool{"": false, "true": false, "false": true, `"false"`: true, `"true"`: false} {
		u := OIDCUserInfo{EmailVerified: []byte(raw)}
		if got := u.EmailUnverified(); got != want {
			t.Errorf("%q: %v", raw, got)
		}
	}
}
