package storage

import (
	"database/sql"
	"testing"

	"github.com/Enach/paceday/backend/internal/testdb"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn, cleanup, err := testdb.Create()
	if err != nil {
		t.Fatalf("create test database: %v", err)
	}
	db, err := Open(dsn)
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
