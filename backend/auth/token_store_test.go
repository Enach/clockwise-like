package auth

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/storage"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"golang.org/x/oauth2"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()

	container, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("testdb"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() { container.Terminate(ctx) })

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("get connection string: %v", err)
	}

	db, err := storage.Open(dsn)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestLoadToken_Empty(t *testing.T) {
	db := openTestDB(t)
	tok, err := LoadToken(db)
	if err != nil {
		t.Fatalf("LoadToken on empty DB: %v", err)
	}
	if tok != nil {
		t.Error("expected nil token on empty DB")
	}
}

func TestUpsertAndLoadToken(t *testing.T) {
	db := openTestDB(t)

	expiry := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	tok := &oauth2.Token{
		AccessToken:  "access-abc",
		RefreshToken: "refresh-xyz",
		Expiry:       expiry,
	}

	if err := UpsertToken(db, tok); err != nil {
		t.Fatalf("UpsertToken: %v", err)
	}

	got, err := LoadToken(db)
	if err != nil {
		t.Fatalf("LoadToken: %v", err)
	}
	if got == nil {
		t.Fatal("expected token, got nil")
		return
	}
	if got.AccessToken != "access-abc" {
		t.Errorf("AccessToken = %q, want access-abc", got.AccessToken)
	}
	if got.RefreshToken != "refresh-xyz" {
		t.Errorf("RefreshToken = %q, want refresh-xyz", got.RefreshToken)
	}
	if !got.Expiry.Equal(expiry) {
		t.Errorf("Expiry = %v, want %v", got.Expiry, expiry)
	}
}

func TestUpsertToken_Update(t *testing.T) {
	db := openTestDB(t)

	tok1 := &oauth2.Token{AccessToken: "tok1", RefreshToken: "ref1", Expiry: time.Now().Add(time.Hour)}
	UpsertToken(db, tok1)

	tok2 := &oauth2.Token{AccessToken: "tok2", RefreshToken: "ref2", Expiry: time.Now().Add(2 * time.Hour)}
	if err := UpsertToken(db, tok2); err != nil {
		t.Fatalf("UpsertToken update: %v", err)
	}

	got, err := LoadToken(db)
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != "tok2" {
		t.Errorf("expected updated token, got %q", got.AccessToken)
	}
}

func TestDeleteToken(t *testing.T) {
	db := openTestDB(t)

	tok := &oauth2.Token{AccessToken: "acc", RefreshToken: "ref", Expiry: time.Now().Add(time.Hour)}
	UpsertToken(db, tok)

	if err := DeleteToken(db); err != nil {
		t.Fatalf("DeleteToken: %v", err)
	}

	got, err := LoadToken(db)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Error("expected nil token after delete")
	}
}

func TestIsConnected(t *testing.T) {
	db := openTestDB(t)

	if IsConnected(db) {
		t.Error("should not be connected on empty DB")
	}

	tok := &oauth2.Token{AccessToken: "acc", RefreshToken: "ref", Expiry: time.Now().Add(time.Hour)}
	UpsertToken(db, tok)

	if !IsConnected(db) {
		t.Error("should be connected after inserting token with refresh token")
	}

	tok2 := &oauth2.Token{AccessToken: "acc", RefreshToken: "", Expiry: time.Now().Add(time.Hour)}
	UpsertToken(db, tok2)
	if IsConnected(db) {
		t.Error("should not be connected without refresh token")
	}
}

func TestTokenFromDB(t *testing.T) {
	db := openTestDB(t)

	tok, err := TokenFromDB(db)
	if err != nil {
		t.Fatal(err)
	}
	if tok != nil {
		t.Error("expected nil")
	}
}

func TestSaveToken(t *testing.T) {
	db := openTestDB(t)
	tok := &oauth2.Token{AccessToken: "a", RefreshToken: "r", Expiry: time.Now().Add(time.Hour)}
	if err := SaveToken(db, tok); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}
}
