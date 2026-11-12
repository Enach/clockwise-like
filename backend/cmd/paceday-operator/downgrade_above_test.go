package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Enach/paceday/backend/storage"
)

// Owner decision 2026-11-12 (PAC-84 contract "Stage-4 amendments", PAC-71
// manifest): once a migration later than the encryption one ships (PAC-71's
// 035_identity_binding), downgrade-schema refuses with exit 1, changes
// nothing, leaves schema_migrations clean and writes one failed audit row;
// the way back to a release before encryption at rest is a backup restore.
// Nothing in production runs the later migration's down step.
func TestPAC84_DowngradeRefusedAbove034(t *testing.T) {
	e := newOpEnv(t) // every embedded migration, as shipped
	want, err := storage.EncryptionMigrationVersion()
	if err != nil {
		t.Fatal(err)
	}
	e.seedPlain()
	// Nothing encrypted and no data key: the refusal below is the version's,
	// not the encrypted-values one.
	for _, args := range [][]string{
		{"convert", "--operator", "o", "--reason", "r"},
		{"decrypt-all", "--operator", "o", "--reason", "r"},
	} {
		if code, _, stderr := e.run(e.keyed(), args...); code != exitOK {
			t.Fatalf("%s: %d %s", args[0], code, stderr)
		}
	}
	type state struct {
		version uint
		dirty   bool
		tables  string
	}
	read := func() state {
		var s state
		if err := e.db.QueryRow(`SELECT version, dirty FROM schema_migrations`).Scan(&s.version, &s.dirty); err != nil {
			t.Fatal(err)
		}
		_ = e.db.QueryRow(`SELECT string_agg(tablename, ',' ORDER BY tablename) FROM pg_tables
			WHERE tablename IN ('encryption_data_keys', 'encryption_backend_heartbeats', 'user_identities', 'operator_audit')`).Scan(&s.tables)
		return s
	}
	before := read()
	if before.version <= want || before.dirty {
		t.Fatalf("the database is not above the encryption migration: %+v", before)
	}
	if before.tables != "encryption_backend_heartbeats,encryption_data_keys,operator_audit,user_identities" {
		t.Fatalf("tables before: %s", before.tables)
	}
	audits := e.audits(storage.AuditEncryptionDowngrade)

	code, out, stderr := e.run(nil, "downgrade-schema", "--operator", "o", "--reason", "back to the release before encryption")
	wantMsg := fmt.Sprintf("encryption downgrade-schema: the database is at version %d, not %d: roll back the later release first", before.version, want)
	if code != exitFailed || !strings.Contains(stderr, wantMsg) {
		t.Fatalf("downgrade above %d: exit %d, stdout %q, stderr %q", want, code, out, stderr)
	}
	if after := read(); after != before {
		t.Fatalf("the refused downgrade changed the database: %+v -> %+v", before, after)
	}
	if e.audits(storage.AuditEncryptionDowngrade) != audits+1 || e.auditOutcome(storage.AuditEncryptionDowngrade) != "failed" {
		t.Fatal("the refused downgrade did not write exactly one failed audit row")
	}
	if db2, err := storage.Open(e.dsn); err != nil {
		t.Fatalf("the backend cannot open after the refusal: %v", err)
	} else {
		db2.Close()
	}
}
