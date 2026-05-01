package api

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/storage"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

var sharedTestDB *sql.DB

func TestMain(m *testing.M) {
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
		// Docker unavailable — fail loudly so CI catches it.
		_, _ = os.Stderr.WriteString("start postgres container: " + err.Error() + "\n")
		os.Exit(1)
	}

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		container.Terminate(ctx) //nolint:errcheck
		_, _ = os.Stderr.WriteString("get connection string: " + err.Error() + "\n")
		os.Exit(1)
	}

	db, err := storage.Open(dsn)
	if err != nil {
		container.Terminate(ctx) //nolint:errcheck
		_, _ = os.Stderr.WriteString("open test db: " + err.Error() + "\n")
		os.Exit(1)
	}

	sharedTestDB = db
	code := m.Run()
	db.Close()
	container.Terminate(ctx) //nolint:errcheck
	os.Exit(code)
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	return sharedTestDB
}
