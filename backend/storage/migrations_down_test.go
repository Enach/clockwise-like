package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/Enach/paceday/backend/internal/testdb"
)

// PAC-81 Part D (spec NEW-deploy-hardening AC-D5, F-D2, D-13): every
// migration has a down file; the down files that destroy data say so on
// their first line; and an empty database migrated to the latest version can
// go down to 005 and up again, with the schema identical each way.

// destructiveDowns are the down files this work added (F-D2); each must start
// with a "-- DESTROYS DATA:" comment naming what it destroys.
var destructiveDowns = []string{
	"006_auth.down.sql",
	"007_organizations.down.sql",
	"014_workspace_connections.down.sql",
	"015_daily_recap.down.sql",
	"016_manager.down.sql",
}

func TestEveryMigrationHasADownFile(t *testing.T) {
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, e := range entries {
		names[e.Name()] = true
	}
	ups := 0
	for n := range names {
		if !strings.HasSuffix(n, ".up.sql") {
			continue
		}
		ups++
		down := strings.TrimSuffix(n, ".up.sql") + ".down.sql"
		if !names[down] {
			t.Errorf("%s has no %s", n, down)
		}
	}
	if ups == 0 {
		t.Fatal("no migrations embedded")
	}
	for _, d := range destructiveDowns {
		raw, err := fs.ReadFile(migrationFiles, "migrations/"+d)
		if err != nil {
			t.Errorf("%s: %v", d, err)
			continue
		}
		first, _, _ := strings.Cut(string(raw), "\n")
		if !strings.HasPrefix(first, "-- DESTROYS DATA:") || len(strings.TrimSpace(strings.TrimPrefix(first, "-- DESTROYS DATA:"))) < 10 {
			t.Errorf("%s must start with a \"-- DESTROYS DATA: <what>\" comment, starts with %q", d, first)
		}
	}
}

// schemaSnapshot describes the public schema: columns with type, nullability
// and default; constraints with their definition; indexes with their
// definition; installed extensions. Names Postgres generates are kept: a
// down/up cycle must reproduce them exactly.
func schemaSnapshot(t *testing.T, db *sql.DB) []string {
	t.Helper()
	queries := map[string]string{
		"column": `SELECT table_name || '.' || column_name || ' ' || data_type || ' null=' || is_nullable || ' default=' || COALESCE(column_default, '')
			FROM information_schema.columns WHERE table_schema = 'public'`,
		"constraint": `SELECT c.conrelid::regclass::text || ' ' || c.conname || ' ' || pg_get_constraintdef(c.oid)
			FROM pg_constraint c JOIN pg_namespace n ON n.oid = c.connamespace WHERE n.nspname = 'public'`,
		"index":     `SELECT indexdef FROM pg_indexes WHERE schemaname = 'public'`,
		"extension": `SELECT extname FROM pg_extension WHERE extname <> 'plpgsql'`,
		"table":     `SELECT table_name || ' ' || table_type FROM information_schema.tables WHERE table_schema = 'public'`,
	}
	var out []string
	for kind, q := range queries {
		rows, err := db.Query(q)
		if err != nil {
			t.Fatalf("snapshot %s: %v", kind, err)
		}
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				t.Fatal(err)
			}
			out = append(out, kind+" "+s)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
	}
	sort.Strings(out)
	return out
}

func diffSnapshots(a, b []string) []string {
	in := func(xs []string) map[string]bool {
		m := map[string]bool{}
		for _, x := range xs {
			m[x] = true
		}
		return m
	}
	ma, mb := in(a), in(b)
	var d []string
	for _, x := range a {
		if !mb[x] {
			d = append(d, "- "+x)
		}
	}
	for _, x := range b {
		if !ma[x] {
			d = append(d, "+ "+x)
		}
	}
	return d
}

func TestMigrationsDownTo005AndUpAgain(t *testing.T) {
	open := func() (*sql.DB, *migrate.Migrate) {
		t.Helper()
		dsn, cleanup, err := testdb.Create()
		if err != nil {
			t.Fatalf("create test database: %v", err)
		}
		db, err := sql.Open("pgx", dsn)
		if err != nil {
			_ = cleanup()
			t.Fatal(err)
		}
		t.Cleanup(func() {
			db.Close()
			if err := cleanup(); err != nil {
				t.Errorf("cleanup: %v", err)
			}
		})
		src, err := iofs.New(migrationFiles, "migrations")
		if err != nil {
			t.Fatal(err)
		}
		driver, err := postgres.WithInstance(db, &postgres.Config{})
		if err != nil {
			t.Fatal(err)
		}
		m, err := migrate.NewWithInstance("iofs", src, "postgres", driver)
		if err != nil {
			t.Fatal(err)
		}
		return db, m
	}
	step := func(m *migrate.Migrate, what string, f func() error) {
		t.Helper()
		if err := f(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			t.Fatalf("%s: %v", what, err)
		}
	}

	// Reference: a fresh database at 005.
	refDB, refM := open()
	step(refM, "fresh up to 005", func() error { return refM.Migrate(5) })
	at005 := schemaSnapshot(t, refDB)

	db, m := open()
	step(m, "up to latest", m.Up)
	latest, _, err := m.Version()
	if err != nil {
		t.Fatal(err)
	}
	atLatest := schemaSnapshot(t, db)

	// Down one version at a time, so a failure names the file.
	for v := latest; v > 5; v-- {
		step(m, fmt.Sprintf("down from %d", v), func() error { return m.Steps(-1) })
		if dirty := migrationDirty(t, db); dirty {
			t.Fatalf("down from %d left the database dirty", v)
		}
	}
	if d := diffSnapshots(at005, schemaSnapshot(t, db)); len(d) > 0 {
		t.Fatalf("after migrating down to 005 the schema differs from a fresh 005:\n%s", strings.Join(d, "\n"))
	}

	step(m, "up again to latest", m.Up)
	if d := diffSnapshots(atLatest, schemaSnapshot(t, db)); len(d) > 0 {
		t.Fatalf("after down to 005 and up again the schema differs from the first latest:\n%s", strings.Join(d, "\n"))
	}
	t.Logf("latest=%d; down %d→5 one step at a time and up again; %d schema facts compared at latest, %d at 005", latest, latest, len(atLatest), len(at005))
}

func migrationDirty(t *testing.T, db *sql.DB) bool {
	t.Helper()
	var dirty bool
	err := db.QueryRow(`SELECT dirty FROM schema_migrations LIMIT 1`).Scan(&dirty)
	if errors.Is(err, sql.ErrNoRows) {
		return false
	}
	if err != nil {
		t.Fatal(err)
	}
	return dirty
}
