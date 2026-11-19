package storage

import (
	"context"
	"testing"

	"github.com/Enach/paceday/backend/internal/testdb"
)

// Migration 045 through the real runners (contract md §4): the
// migrate-when-stopped procedure applies 44 -> 45 (paceday_go_lower and the
// ten-action CHECK); golang-migrate's down step drops the function and keeps
// the erase records and the wider CHECK (PAC-71: no down migration removes
// an operator record); the up step re-applies.
func TestPAC134_Migration045UpDownUp(t *testing.T) {
	want, err := EraseAddressMigrationVersion()
	if err != nil || want != 45 {
		t.Fatalf("erase migration %d %v", want, err)
	}
	dsn, cleanup, err := testdb.Create()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cleanup() }()
	restore := SetMigrationCeilingForTest(want - 1)
	db, err := Open(dsn)
	restore()
	if err != nil {
		t.Fatal(err)
	}
	if v, _, _, _ := SchemaVersion(context.Background(), db); v != want-1 {
		t.Fatalf("at %d", v)
	}
	db.Close()
	pac116Idle(t, dsn)
	res, err := MigrateWhenStopped(context.Background(), dsn, MigrateOptions{Caller: EraseAddressApp})
	if err != nil || !res.Applied || res.To != want {
		t.Fatalf("migrate: %+v %v", res, err)
	}
	db, err = OpenNoMigrate(dsn, "pac134-test")
	if err != nil {
		t.Fatal(err)
	}
	p134Exec(t, db, `INSERT INTO operator_audit (action, operator, reason) VALUES ('erase_address', 'o', 'r'), ('erase_address_dry_run', 'o', 'r')`)
	if got := p134Row(t, db, `SELECT paceday_go_lower('ÜNAL@Ext.Example')`); got != "ünal@ext.example" {
		t.Fatalf("paceday_go_lower: %q", got)
	}
	db.Close()
	pac116Idle(t, dsn)

	pac149Steps(t, dsn, -1)
	db, err = OpenNoMigrate(dsn, "pac134-test")
	if err != nil {
		t.Fatal(err)
	}
	if p134Count(t, db, `SELECT count(*) FROM pg_proc WHERE proname = 'paceday_go_lower'`) != 0 ||
		p134Count(t, db, `SELECT count(*) FROM operator_audit WHERE action LIKE 'erase_address%'`) != 2 {
		t.Fatal("045 down: the function is still there or an erase record went")
	}
	if _, err := db.Exec(`INSERT INTO operator_audit (action, operator, reason) VALUES ('erase_address', 'o', 'r')`); err != nil {
		t.Fatalf("045 down narrowed the CHECK: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO operator_audit (action, operator, reason) VALUES ('erase_other', 'o', 'r')`); err == nil {
		t.Fatal("an unknown action was admitted")
	}
	db.Close()
	pac116Idle(t, dsn)

	pac149Steps(t, dsn, 1)
	db, err = OpenNoMigrate(dsn, "pac134-test")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if v, dirty, _, _ := SchemaVersion(context.Background(), db); v != want || dirty ||
		p134Count(t, db, `SELECT count(*) FROM operator_audit WHERE action LIKE 'erase_address%'`) != 3 ||
		p134Row(t, db, `SELECT paceday_go_lower('İnal')`) != "inal" {
		t.Fatalf("045 up again: at %d dirty %v", v, dirty)
	}
}
