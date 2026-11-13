package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/Enach/paceday/backend/internal/secretbox"
	"github.com/Enach/paceday/backend/internal/secretbox/secretboxtest"
	"github.com/Enach/paceday/backend/internal/testdb"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

// PAC-84 contract section 8: the operator subcommands, their exit codes,
// flags, locks, presence check and audit rows.

type opEnv struct {
	t   *testing.T
	dsn string
	db  *sql.DB
	key string
}

func newOpEnv(t *testing.T) *opEnv {
	t.Helper()
	dsn, cleanup, err := testdb.Create()
	if err != nil {
		t.Fatalf("PostgreSQL unavailable: %v", err)
	}
	db, err := storage.Open(dsn)
	if err != nil {
		_ = cleanup()
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close(); _ = cleanup() })
	return &opEnv{t: t, dsn: dsn, db: db, key: secretboxtest.KeyValue("op-key")}
}

// newOpEnvAtEncryption is newOpEnv with every migration in this process (the
// test's and the CLI's) stopped at the encryption migration (034).
// downgrade-schema runs only from 034; once a later migration ships
// (PAC-71's 035) the CLI's own migrate-up would take the database past it,
// so the downgrade path is proven on a database held at 034 (owner decision
// 2026-11-12, contract "Stage-4 amendments"). TestPAC84_DowngradeRefusedAbove034
// covers the shipped schema.
func newOpEnvAtEncryption(t *testing.T) *opEnv {
	t.Helper()
	want, err := storage.EncryptionMigrationVersion()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(storage.SetMigrationCeilingForTest(want))
	e := newOpEnv(t)
	if v, dirty, _, err := storage.SchemaVersion(context.Background(), e.db); err != nil || v != want || dirty {
		t.Fatalf("the test database is not at the encryption migration: %d %v %v", v, dirty, err)
	}
	return e
}

func (e *opEnv) run(env map[string]string, args ...string) (int, string, string) {
	e.t.Helper()
	all := map[string]string{"DATABASE_URL": e.dsn}
	for k, v := range env {
		all[k] = v
	}
	var out, errw bytes.Buffer
	code := runEncryption(args, func(k string) string { return all[k] }, &out, &errw)
	return code, out.String(), errw.String()
}

func (e *opEnv) keyed() map[string]string { return map[string]string{"PACEDAY_ENCRYPTION_KEY": e.key} }

func (e *opEnv) audits(action string) int {
	var n int
	if err := e.db.QueryRow(`SELECT count(*) FROM operator_audit WHERE action = $1`, action).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *opEnv) seedPlain() uuid.UUID {
	e.t.Helper()
	var u uuid.UUID
	if err := e.db.QueryRow(`INSERT INTO users (email, name) VALUES ($1, 'Op') RETURNING id`, uuid.NewString()+"@op.example").Scan(&u); err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.db.Exec(`INSERT INTO settings (user_id, webcal_url, llm_api_key_openai) VALUES ($1, 'https://op.example/x.ics', 'sk-op-abcdefghijklmnop')`, u); err != nil {
		e.t.Fatal(err)
	}
	return u
}

// AC-21: every mutating subcommand refuses without --operator and --reason
// (exit 2, no audit); usage errors are exit 2.
func TestPAC84_OperatorUsage(t *testing.T) {
	e := newOpEnv(t)
	for _, sub := range []string{"convert", "rotate-data-key", "decrypt-all", "reset-lost-key", "downgrade-schema"} {
		if code, _, stderr := e.run(e.keyed(), sub); code != exitUsage || !strings.Contains(stderr, "--operator and --reason are required") {
			t.Fatalf("%s without operator: %d %s", sub, code, stderr)
		}
	}
	for _, args := range [][]string{{}, {"nope"}, {"status", "--bogus"}, {"rotate-data-key", "--operator", "o", "--reason", "r"},
		{"reset-lost-key", "--operator", "o", "--reason", "r"}, {"convert", "--operator", "o", "--reason", "r", "--batch", "0"}} {
		if code, _, _ := e.run(e.keyed(), args...); code != exitUsage {
			t.Fatalf("%v: exit %d, want 2", args, code)
		}
	}
	var out, errw bytes.Buffer
	if code := runEncryption([]string{"status"}, func(string) string { return "" }, &out, &errw); code != exitUsage {
		t.Fatal("missing DATABASE_URL must be exit 2")
	}
	if e.audits(storage.AuditEncryptionConvert) != 0 {
		t.Fatal("a refusal wrote an audit row")
	}
}

// AC-20, AC-21, AC-21d: status works with no key, --check exits 3 while
// plain values remain; convert needs the key (exit 4 without), converts,
// audits, and a second run reports 0 changes; status then shows no plain.
func TestPAC84_OperatorStatusAndConvert(t *testing.T) {
	e := newOpEnv(t)
	e.seedPlain()
	code, out, _ := e.run(nil, "status")
	if code != exitOK || !strings.Contains(out, "settings.webcal_url") || strings.Contains(out, "op.example") || strings.Contains(out, "sk-op") {
		t.Fatalf("status: %d\n%s", code, out)
	}
	if code, _, _ := e.run(nil, "status", "--check"); code != exitPlain {
		t.Fatalf("status --check with plain values: %d", code)
	}
	if code, _, stderr := e.run(nil, "convert", "--operator", "o", "--reason", "r"); code != exitKey || !strings.Contains(stderr, "PACEDAY_ENCRYPTION_KEY is not set") {
		t.Fatalf("convert without key: %d %s", code, stderr)
	}
	code, out, stderr := e.run(e.keyed(), "convert", "--operator", "ana", "--reason", "upgrade")
	if code != exitOK || !strings.Contains(out, "2 values encrypted") {
		t.Fatalf("convert: %d %s %s", code, out, stderr)
	}
	if strings.Contains(out+stderr, strings.SplitN(e.key, ":", 2)[1]) {
		t.Fatal("key printed")
	}
	code, out, _ = e.run(e.keyed(), "convert", "--operator", "ana", "--reason", "again")
	if code != exitOK || !strings.Contains(out, "0 values encrypted") {
		t.Fatalf("second convert: %d %s", code, out)
	}
	if e.audits(storage.AuditEncryptionConvert) != 2 {
		t.Fatal("one audit row per run")
	}
	var details string
	_ = e.db.QueryRow(`SELECT details::text FROM operator_audit WHERE action = 'encryption_convert' ORDER BY created_at LIMIT 1`).Scan(&details)
	if !strings.Contains(details, `"settings.webcal_url": {"encrypted": 1}`) || !strings.Contains(details, `"outcome": "completed"`) ||
		!strings.Contains(details, `"active": "op-key"`) {
		t.Fatalf("audit details %s", details)
	}
	if code, out, _ := e.run(nil, "status", "--check"); code != exitOK || !strings.Contains(out, "plain values: 0") {
		t.Fatalf("status after: %d %s", code, out)
	}
	// AC-31 / K-6: an operator key that does not wrap the active data key.
	other := map[string]string{"PACEDAY_ENCRYPTION_KEY": secretboxtest.KeyValue("other")}
	if code, _, stderr := e.run(other, "convert", "--operator", "o", "--reason", "r"); code != exitKey || !strings.Contains(stderr, `wrapped by key "op-key"`) {
		t.Fatalf("convert with another key: %d %s", code, stderr)
	}
	if code, _, _ := e.run(other, "rotate-data-key", "--operator", "o", "--reason", "r", "--retire", "1"); code != exitKey {
		t.Fatal("rotate with another key must be exit 4")
	}
	// K-12: the committed e2e key is refused (exit 4) without the opt-in.
	e2e := map[string]string{"PACEDAY_ENCRYPTION_KEY": "anything:cGFjZWRheS1lMmUtdGVzdC1rZXktTk9ULVNFQ1JFVCE="}
	if code, _, stderr := e.run(e2e, "convert", "--operator", "o", "--reason", "r"); code != exitKey || !strings.Contains(stderr, "committed e2e test key") {
		t.Fatalf("e2e key: %d %s", code, stderr)
	}
}

// AC-11b: rotate-data-key creates a new active key, re-encrypts away from
// the retired one, audits, and a second run with the same --retire is 0.
func TestPAC84_OperatorRotate(t *testing.T) {
	e := newOpEnv(t)
	e.seedPlain()
	if code, _, _ := e.run(e.keyed(), "convert", "--operator", "o", "--reason", "r"); code != exitOK {
		t.Fatal("convert")
	}
	code, out, stderr := e.run(e.keyed(), "rotate-data-key", "--operator", "o", "--reason", "suspected", "--retire", "1")
	if code != exitOK || !strings.Contains(out, "data key 1 retired; data key 2 is now active") || !strings.Contains(out, "2 values re-encrypted; 0 values left under data key 1") {
		t.Fatalf("rotate: %d %s %s", code, out, stderr)
	}
	code, out, _ = e.run(e.keyed(), "rotate-data-key", "--operator", "o", "--reason", "again", "--retire", "1")
	if code != exitOK || !strings.Contains(out, "0 values re-encrypted") {
		t.Fatalf("second rotate: %d %s", code, out)
	}
	if e.audits(storage.AuditEncryptionRotate) != 2 {
		t.Fatal("audit rows")
	}
	// A live backend still on key 1 makes the wait time out (exit 1, partial).
	dek := int32(1)
	if err := storage.WriteHeartbeat(context.Background(), e.db, storage.Instance{ID: uuid.New()}, &dek); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := e.run(e.keyed(), "rotate-data-key", "--operator", "o", "--reason", "r", "--retire", "2", "--wait", "1ms"); code != exitFailed || !strings.Contains(stderr, "still seal with another data key") {
		t.Fatalf("rotate with a lagging backend: %d %s", code, stderr)
	}
}

// AC-21b, AC-21c: the maintenance lock held -> exit 5 naming the holder;
// a live backend -> decrypt-all / reset-lost-key / downgrade-schema exit 6
// naming the runbook step, nothing changed, no audit row.
func TestPAC84_OperatorLockAndLiveBackend(t *testing.T) {
	e := newOpEnv(t)
	e.seedPlain()
	holder, err := storage.OpenAs(e.dsn, storage.OperatorApp("convert"))
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	lock, err := storage.TryMaintenanceLock(context.Background(), holder)
	if err != nil {
		t.Fatal(err)
	}
	code, _, stderr := e.run(e.keyed(), "convert", "--operator", "o", "--reason", "r")
	if code != exitLock || !strings.Contains(stderr, "paceday-operator encryption convert (pid") {
		t.Fatalf("lock held: %d %s", code, stderr)
	}
	lock.Release()
	if err := storage.WriteHeartbeat(context.Background(), e.db, storage.Instance{ID: uuid.New(), Hostname: "web-1"}, nil); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"decrypt-all", "--operator", "o", "--reason", "r"},
		{"reset-lost-key", "--operator", "o", "--reason", "r", "--confirm-lost-key"},
		{"downgrade-schema", "--operator", "o", "--reason", "r"},
	} {
		code, _, stderr := e.run(e.keyed(), args...)
		if code != exitBackends || !strings.Contains(stderr, `docs/runbook.md, "Encryption key - stop the backend first"`) {
			t.Fatalf("%s with a live backend: %d %s", args[0], code, stderr)
		}
	}
	var n int
	_ = e.db.QueryRow(`SELECT count(*) FROM operator_audit`).Scan(&n)
	if n != 0 {
		t.Fatal("refusals wrote audit rows")
	}
}

// AC-21, AC-22, AC-21d, AC-23: decrypt-all writes plaintext back and removes
// the data keys; downgrade-schema refuses while anything is encrypted (exit
// 1, version clean) and succeeds after; reset-lost-key refuses when a key
// opens a data key (exit 4) and resets with a new key.
func TestPAC84_OperatorDecryptDowngradeReset(t *testing.T) {
	e := newOpEnvAtEncryption(t)
	u := e.seedPlain()
	if code, _, _ := e.run(e.keyed(), "convert", "--operator", "o", "--reason", "r"); code != exitOK {
		t.Fatal("convert")
	}
	code, _, stderr := e.run(nil, "downgrade-schema", "--operator", "o", "--reason", "r")
	if code != exitFailed || !strings.Contains(stderr, `run "paceday-operator encryption decrypt-all" first`) {
		t.Fatalf("downgrade while encrypted: %d %s", code, stderr)
	}
	var dirty bool
	_ = e.db.QueryRow(`SELECT dirty FROM schema_migrations`).Scan(&dirty)
	if dirty || e.auditOutcome(storage.AuditEncryptionDowngrade) != "failed" {
		t.Fatal("refused downgrade left the database dirty, or wrote no failed audit row (review #6)")
	}
	if db2, err := storage.Open(e.dsn); err != nil {
		t.Fatalf("backend cannot open after a refused downgrade: %v", err)
	} else {
		db2.Close()
	}

	// reset-lost-key with the key that still opens the data key: exit 4.
	if code, _, stderr := e.run(e.keyed(), "reset-lost-key", "--operator", "o", "--reason", "r", "--confirm-lost-key"); code != exitKey || !strings.Contains(stderr, "the key is not lost") {
		t.Fatalf("reset with a working key: %d %s", code, stderr)
	}
	code, out, stderr := e.run(e.keyed(), "decrypt-all", "--operator", "o", "--reason", "rollback")
	if code != exitOK || !strings.Contains(out, "2 values decrypted") {
		t.Fatalf("decrypt-all: %d %s %s", code, out, stderr)
	}
	var webcal string
	_ = e.db.QueryRow(`SELECT webcal_url FROM settings WHERE user_id = $1`, u).Scan(&webcal)
	if webcal != "https://op.example/x.ics" {
		t.Fatal("decrypt-all did not write the plaintext back")
	}
	if code, out, _ := e.run(e.keyed(), "decrypt-all", "--operator", "o", "--reason", "again"); code != exitOK || !strings.Contains(out, "0 values decrypted") {
		t.Fatalf("second decrypt-all: %d %s", code, out)
	}
	code, out, stderr = e.run(nil, "downgrade-schema", "--operator", "o", "--reason", "rollback")
	if code != exitOK || !strings.Contains(out, "schema version") {
		t.Fatalf("downgrade: %d %s %s", code, out, stderr)
	}
	if e.audits(storage.AuditEncryptionDowngrade) != 2 || e.audits(storage.AuditEncryptionDecrypt) != 2 {
		t.Fatal("audit rows after downgrade")
	}
}

func TestPAC84_OperatorResetLostKey(t *testing.T) {
	e := newOpEnv(t)
	u := e.seedPlain()
	// An Outlook credential with a recorded loss: the reset removes it and
	// ends the loss (lossesCleared, review #6).
	if _, err := e.db.Exec(`UPDATE settings SET microsoft_tokens = '{"access_token":"ms"}' WHERE user_id = $1`, u); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.RecordCredentialLoss(e.db, u, storage.CredentialOutlook); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := e.run(e.keyed(), "convert", "--operator", "o", "--reason", "r"); code != exitOK {
		t.Fatal("convert")
	}
	fresh := map[string]string{"PACEDAY_ENCRYPTION_KEY": "new-key:" + base64.StdEncoding.EncodeToString(secretboxtest.RandomKey())}
	code, out, stderr := e.run(fresh, "reset-lost-key", "--operator", "o", "--reason", "key lost", "--confirm-lost-key")
	if code != exitOK || !strings.Contains(out, `created under key "new-key"`) {
		t.Fatalf("reset: %d %s %s", code, out, stderr)
	}
	var webcal, key string
	_ = e.db.QueryRow(`SELECT webcal_url, llm_api_key_openai FROM settings`).Scan(&webcal, &key)
	if webcal != "" || key != "" {
		t.Fatal("encrypted values not emptied")
	}
	deks, _ := storage.LoadDataKeys(context.Background(), e.db)
	if len(deks) != 1 || deks[0].KEKID != "new-key" {
		t.Fatalf("data keys %+v", deks)
	}
	if e.audits(storage.AuditEncryptionResetLost) != 1 {
		t.Fatal("audit")
	}
	cfg, _ := secretbox.ParseKeyConfig(fresh["PACEDAY_ENCRYPTION_KEY"], "")
	if _, err := storage.PrepareKeyring(context.Background(), e.db, cfg, true); err != nil {
		t.Fatalf("start with the new key: %v", err)
	}
}

func (e *opEnv) auditOutcome(action string) string {
	var o string
	_ = e.db.QueryRow(`SELECT details->>'outcome' FROM operator_audit WHERE action = $1 ORDER BY created_at DESC LIMIT 1`, action).Scan(&o)
	return o
}

// Review #6: reset-lost-key records lossesCleared.
func TestPAC84_OperatorResetRecordsLossesCleared(t *testing.T) {
	e := newOpEnv(t)
	u := e.seedPlain()
	if _, err := e.db.Exec(`UPDATE settings SET zoom_tokens = '{"access_token":"z","refresh_token":"r"}' WHERE user_id = $1`, u); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.RecordCredentialLoss(e.db, u, storage.CredentialZoom); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := e.run(e.keyed(), "convert", "--operator", "o", "--reason", "r"); code != exitOK {
		t.Fatal("convert")
	}
	fresh := map[string]string{"PACEDAY_ENCRYPTION_KEY": secretboxtest.KeyValue("fresh2")}
	if code, _, stderr := e.run(fresh, "reset-lost-key", "--operator", "o", "--reason", "lost", "--confirm-lost-key"); code != exitOK {
		t.Fatalf("reset: %d %s", code, stderr)
	}
	var lc string
	_ = e.db.QueryRow(`SELECT details->>'lossesCleared' FROM operator_audit WHERE action = 'encryption_reset_lost_key'`).Scan(&lc)
	if lc != "1" {
		t.Fatalf("lossesCleared = %q, want 1", lc)
	}
}

// Review #6: exit-1 paths after the lock write one failed audit row
// (rotate-data-key on a data key that does not exist).
func TestPAC84_OperatorFailureIsAudited(t *testing.T) {
	e := newOpEnv(t)
	e.seedPlain()
	if code, _, _ := e.run(e.keyed(), "convert", "--operator", "o", "--reason", "r"); code != exitOK {
		t.Fatal("convert")
	}
	if code, _, _ := e.run(e.keyed(), "rotate-data-key", "--operator", "o", "--reason", "r", "--retire", "99"); code != exitFailed {
		t.Fatal("rotate of a missing data key must exit 1")
	}
	if e.audits(storage.AuditEncryptionRotate) != 1 || e.auditOutcome(storage.AuditEncryptionRotate) != "failed" {
		t.Fatal("no failed audit row")
	}
}

// Review #3: after downgrade-schema no encryption subcommand re-applies
// this work's migration: status reports it and changes nothing, the
// mutating ones refuse (exit 1, audited) and the version stays below.
func TestPAC84_OperatorAfterDowngradeNeverMigratesUp(t *testing.T) {
	e := newOpEnvAtEncryption(t)
	e.seedPlain()
	for _, args := range [][]string{
		{"convert", "--operator", "o", "--reason", "r"},
		{"decrypt-all", "--operator", "o", "--reason", "r"},
		{"downgrade-schema", "--operator", "o", "--reason", "r"},
	} {
		if code, _, stderr := e.run(e.keyed(), args...); code != exitOK {
			t.Fatalf("%s: %d %s", args[0], code, stderr)
		}
	}
	want, _ := storage.EncryptionMigrationVersion()
	version := func() uint {
		var v uint
		if err := e.db.QueryRow(`SELECT version FROM schema_migrations`).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	if version() >= want {
		t.Fatal("not downgraded")
	}
	code, out, _ := e.run(nil, "status")
	if code != exitOK || !strings.Contains(out, "below this release's encryption migration") {
		t.Fatalf("status after downgrade: %d %s", code, out)
	}
	if code, _, _ := e.run(nil, "status", "--check"); code != exitFailed {
		t.Fatal("status --check after downgrade must not report success")
	}
	before := e.audits(storage.AuditEncryptionConvert)
	code, _, stderr := e.run(e.keyed(), "convert", "--operator", "o", "--reason", "r")
	if code != exitFailed || !strings.Contains(stderr, "applies no migration") {
		t.Fatalf("convert after downgrade: %d %s", code, stderr)
	}
	if e.audits(storage.AuditEncryptionConvert) != before+1 {
		t.Fatal("the refusal was not audited")
	}
	var tables int
	_ = e.db.QueryRow(`SELECT count(*) FROM pg_tables WHERE tablename LIKE 'encryption_%'`).Scan(&tables)
	if version() >= want || tables != 0 {
		t.Fatalf("migration re-applied: version %d, %d encryption tables", version(), tables)
	}
}
