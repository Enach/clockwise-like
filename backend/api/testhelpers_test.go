package api

import (
	"database/sql"
	"os"
	"testing"

	"github.com/Enach/paceday/backend/internal/testdb"
	"github.com/Enach/paceday/backend/storage"
)

var sharedTestDB *sql.DB

func TestMain(m *testing.M) {
	dsn, cleanup, err := testdb.Create()
	if err != nil {
		_, _ = os.Stderr.WriteString("create test database: " + err.Error() + "\n")
		os.Exit(1)
	}
	db, err := storage.Open(dsn)
	if err != nil {
		_ = cleanup()
		_, _ = os.Stderr.WriteString("open test database: " + err.Error() + "\n")
		os.Exit(1)
	}
	sharedTestDB = db
	code := m.Run()
	db.Close()
	if err := cleanup(); err != nil && code == 0 {
		_, _ = os.Stderr.WriteString("cleanup test database: " + err.Error() + "\n")
		code = 1
	}
	os.Exit(code)
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	return sharedTestDB
}
