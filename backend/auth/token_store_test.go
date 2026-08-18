package auth

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/internal/testdb"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn, cleanup, err := testdb.Create()
	if err != nil {
		t.Fatalf("create test database: %v", err)
	}
	db, err := storage.Open(dsn)
	if err != nil {
		_ = cleanup()
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() {
		db.Close()
		if err := cleanup(); err != nil {
			t.Errorf("cleanup test database: %v", err)
		}
	})
	return db
}

func seedUser(t *testing.T, db *sql.DB, email string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := db.Exec(
		`INSERT INTO users (id, email, name, created_at) VALUES ($1, $2, $2, NOW())`,
		id, email,
	)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return id
}

func TestLoadUserToken_NilUser(t *testing.T) {
	db := openTestDB(t)
	if _, err := LoadUserToken(db, uuid.Nil); !errors.Is(err, ErrNoUser) {
		t.Fatalf("LoadUserToken(uuid.Nil): want ErrNoUser, got %v", err)
	}
}

func TestLoadUserToken_Empty(t *testing.T) {
	db := openTestDB(t)
	uid := seedUser(t, db, "alice@example.com")
	tok, err := LoadUserToken(db, uid)
	if err != nil {
		t.Fatalf("LoadUserToken on empty DB: %v", err)
	}
	if tok != nil {
		t.Fatalf("expected nil token on empty DB, got %+v", tok)
	}
}

func TestUpsertAndLoadUserToken(t *testing.T) {
	db := openTestDB(t)
	uid := seedUser(t, db, "alice@example.com")
	expiry := time.Now().Add(time.Hour).UTC().Truncate(time.Microsecond)
	tok := &oauth2.Token{
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
		Expiry:       expiry,
	}
	if err := UpsertUserToken(db, uid, tok); err != nil {
		t.Fatalf("UpsertUserToken: %v", err)
	}

	got, err := LoadUserToken(db, uid)
	if err != nil {
		t.Fatalf("LoadUserToken: %v", err)
	}
	if got == nil {
		t.Fatal("expected token, got nil")
		return
	}
	if got.AccessToken != tok.AccessToken || got.RefreshToken != tok.RefreshToken {
		t.Errorf("token fields mismatch: got %+v want %+v", got, tok)
	}
}

func TestUpsertUserToken_NilUser(t *testing.T) {
	db := openTestDB(t)
	tok := &oauth2.Token{AccessToken: "a", RefreshToken: "r", Expiry: time.Now().Add(time.Hour)}
	if err := UpsertUserToken(db, uuid.Nil, tok); !errors.Is(err, ErrNoUser) {
		t.Fatalf("want ErrNoUser, got %v", err)
	}
}

func TestUpsertUserToken_Update(t *testing.T) {
	db := openTestDB(t)
	uid := seedUser(t, db, "alice@example.com")
	tok1 := &oauth2.Token{AccessToken: "a1", RefreshToken: "r1", Expiry: time.Now().Add(time.Hour)}
	tok2 := &oauth2.Token{AccessToken: "a2", RefreshToken: "r2", Expiry: time.Now().Add(2 * time.Hour)}

	if err := UpsertUserToken(db, uid, tok1); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if err := UpsertUserToken(db, uid, tok2); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	got, err := LoadUserToken(db, uid)
	if err != nil {
		t.Fatalf("LoadUserToken: %v", err)
	}
	if got.AccessToken != "a2" || got.RefreshToken != "r2" {
		t.Errorf("expected updated token, got %+v", got)
	}
}

func TestUpsertUserToken_PerUserIsolation(t *testing.T) {
	db := openTestDB(t)
	alice := seedUser(t, db, "alice@example.com")
	bob := seedUser(t, db, "bob@example.com")
	atok := &oauth2.Token{AccessToken: "alice-token", RefreshToken: "alice-r", Expiry: time.Now().Add(time.Hour)}
	btok := &oauth2.Token{AccessToken: "bob-token", RefreshToken: "bob-r", Expiry: time.Now().Add(time.Hour)}
	if err := UpsertUserToken(db, alice, atok); err != nil {
		t.Fatalf("alice upsert: %v", err)
	}
	if err := UpsertUserToken(db, bob, btok); err != nil {
		t.Fatalf("bob upsert: %v", err)
	}
	got, err := LoadUserToken(db, alice)
	if err != nil || got == nil {
		t.Fatalf("LoadUserToken(alice) got %+v err %v", got, err)
	}
	if got.AccessToken != "alice-token" {
		t.Errorf("alice's token was overwritten by bob's upsert: got %q", got.AccessToken)
	}
}

func TestDeleteUserToken(t *testing.T) {
	db := openTestDB(t)
	uid := seedUser(t, db, "alice@example.com")
	if err := UpsertUserToken(db, uid, &oauth2.Token{AccessToken: "a", RefreshToken: "r", Expiry: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := DeleteUserToken(db, uid); err != nil {
		t.Fatalf("DeleteUserToken: %v", err)
	}
	got, err := LoadUserToken(db, uid)
	if err != nil {
		t.Fatalf("LoadUserToken after delete: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil token after delete, got %+v", got)
	}
	if err := DeleteUserToken(db, uuid.Nil); !errors.Is(err, ErrNoUser) {
		t.Fatalf("DeleteUserToken(uuid.Nil) want ErrNoUser, got %v", err)
	}
}
