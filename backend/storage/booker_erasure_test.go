package storage

// PAC-40 part B, storage side: the transactional audit writer and the
// operator listing (contract §5). The job itself is exercised end to end in
// engine/booker_erasure_test.go.

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// WriteAuditLogTx returns its failure and is atomic with the transaction.
func TestPAC40B_WriteAuditLogTx(t *testing.T) {
	db := openTestDB(t)
	owner := mustUser(t, db, "pac40b-audit@example.com")
	count := func() int {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM audit_log WHERE user_id = $1`, owner).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteAuditLogTx(context.Background(), tx, uuid.Nil, AuditBookersErased, `{}`); !errors.Is(err, ErrAuditNoSubject) {
		t.Errorf("no subject: %v; want ErrAuditNoSubject", err)
	}
	if err := WriteAuditLogTx(context.Background(), tx, owner, AuditBookersErased, `{}`); err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback()
	if n := count(); n != 0 {
		t.Errorf("entries after rollback = %d; want 0 (written in the caller's transaction)", n)
	}

	tx, _ = db.Begin()
	if err := WriteAuditLogTx(context.Background(), tx, uuid.New(), AuditBookersErased, `{}`); err == nil {
		t.Error("an unknown subject (foreign key) was not returned as an error")
	}
	_ = tx.Rollback()

	tx, _ = db.Begin()
	if err := WriteAuditLogTx(context.Background(), tx, owner, AuditBookersErased, `{"a":1}`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 1 {
		t.Errorf("entries after commit = %d; want 1", n)
	}
}

// The operator listing: deleted links with a booking awaiting erasure, due,
// overdue or due within the window; never a live link, an erased link, or
// one due later; ids and instants only.
func TestPAC40B_ListBookerErasureStatus(t *testing.T) {
	db := openTestDB(t)
	owner := mustUser(t, db, "pac40b-status@example.com")
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	mk := func(slug string, end time.Time, deleted bool) uuid.UUID {
		l := pac40Link(t, db, owner, slug)
		pac40Booking(t, db, l.ID, slug, end.Add(-30*time.Minute), "")
		if deleted {
			if ok, err := DeleteSchedulingLink(db, l.ID); err != nil || !ok {
				t.Fatal(ok, err)
			}
		}
		return l.ID
	}
	overdue := mk("st-overdue", now.Add(-BookerRetention-3*24*time.Hour), true)
	dueSoon := mk("st-soon", now.Add(-BookerRetention+6*24*time.Hour), true)
	_ = mk("st-later", now.Add(-BookerRetention+8*24*time.Hour), true)
	_ = mk("st-live", now.Add(-BookerRetention-30*24*time.Hour), false)
	erased := mk("st-erased", now.Add(-BookerRetention-30*24*time.Hour), true)
	if _, err := db.Exec(`UPDATE bookings SET booker_name = '', booker_email = '', booker_erased_at = $2 WHERE link_id = $1`, erased, now); err != nil {
		t.Fatal(err)
	}

	got, err := ListBookerErasureStatus(context.Background(), db, now, 7*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].LinkID != overdue || got[1].LinkID != dueSoon {
		t.Fatalf("listing = %+v; want the overdue then the due-soon link", got)
	}
	for _, s := range got {
		if s.OwnerUserID != owner || s.Awaiting != 1 || !s.ErasureDueAt.Equal(s.LastBookingEndAt.Add(BookerRetention)) || s.DeletedAt.IsZero() {
			t.Errorf("row %+v", s)
		}
	}
	due, err := ListBookerErasureDue(context.Background(), db, now)
	if err != nil || len(due) != 1 || due[0] != overdue {
		t.Errorf("due at now = %v, %v; want the overdue link only", due, err)
	}
}

// The listing's schema floor is migration 037, read from the embedded files.
func TestPAC40B_LifecycleMigrationVersion(t *testing.T) {
	v, err := LifecycleMigrationVersion()
	if err != nil || v != 37 {
		t.Fatalf("LifecycleMigrationVersion = %d, %v; want 37", v, err)
	}
	if _, err := embeddedMigrationVersion("_no_such_migration.up.sql"); err == nil {
		t.Error("a missing migration was found")
	}
}

// erasureFixture is one deleted link owned by a fresh user whose only booking
// ended a year before now, so it is due at now.
func erasureFixture(t *testing.T, db *sql.DB, slug string, now time.Time) (uuid.UUID, uuid.UUID) {
	t.Helper()
	owner := mustUser(t, db, slug+"@example.com")
	l := pac40Link(t, db, owner, slug)
	pac40Booking(t, db, l.ID, slug+"-old", now.Add(-365*24*time.Hour), "")
	if ok, err := DeleteSchedulingLink(db, l.ID); err != nil || !ok {
		t.Fatal(ok, err)
	}
	return owner, l.ID
}

// erasureState is every booking's name, email and erased flag for linkID.
func erasureState(t *testing.T, db *sql.DB, linkID uuid.UUID) map[uuid.UUID]string {
	t.Helper()
	rows, err := db.Query(`SELECT id, booker_name || '|' || booker_email || '|' || (booker_erased_at IS NOT NULL)::text
		FROM bookings WHERE link_id = $1`, linkID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[uuid.UUID]string{}
	for rows.Next() {
		var id uuid.UUID
		var s string
		if err := rows.Scan(&id, &s); err != nil {
			t.Fatal(err)
		}
		out[id] = s
	}
	return out
}

func erasureEntries(t *testing.T, db *sql.DB, owner uuid.UUID) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM audit_log WHERE user_id = $1 AND action = $2`, owner, AuditBookersErased).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Review of #269, finding 1 (K4-3, step 2): a booking that is not yet due and
// lands after the due scan, before the link's lock, makes the link not due:
// nothing is erased, no entry. Fails only if BOTH the in-transaction re-check
// and the UPDATE's due rule are removed (either one alone stops it).
func TestPAC40B_NotDueBookingAddedAfterTheScanSurvives(t *testing.T) {
	db := openTestDB(t)
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	owner, link := erasureFixture(t, db, "pac40b-late-scan", now)
	due, err := ListBookerErasureDue(context.Background(), db, now)
	if err != nil || len(due) != 1 || due[0] != link {
		t.Fatalf("scan = %v, %v; want the link due", due, err)
	}
	pac40Booking(t, db, link, "pac40b-late-scan-new", now.Add(24*time.Hour), "")
	before := erasureState(t, db, link)

	n, err := EraseLinkBookers(context.Background(), db, link, now)
	if err != nil || n != 0 {
		t.Fatalf("erase = %d, %v; want 0, nil (the link is no longer due)", n, err)
	}
	if after := erasureState(t, db, link); !reflect.DeepEqual(after, before) {
		t.Errorf("bookings changed: %v -> %v", before, after)
	}
	if e := erasureEntries(t, db, owner); e != 0 {
		t.Errorf("audit entries = %d; want 0", e)
	}
}

// Review of #269, finding 1 (K4-3, step 3): a not-yet-due booking that becomes
// visible to the transaction after the re-check passed (simulated by inserting
// it on the erasure's own transaction) makes the UPDATE match nothing: no
// booking is erased, no entry is written, and the transaction (with the
// simulated insert) rolls back. Fails if the UPDATE's due rule is removed:
// both bookings would then be erased and committed with an entry.
func TestPAC40B_NotDueBookingVisibleUnderTheLockSurvives(t *testing.T) {
	db := openTestDB(t)
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	owner, link := erasureFixture(t, db, "pac40b-under-lock", now)
	before := erasureState(t, db, link)
	seen := false
	InstallAfterBookerErasureRecheck(t, func(tx *sql.Tx, id uuid.UUID) error {
		seen = true
		_, err := tx.Exec(`INSERT INTO bookings (link_id, booker_name, booker_email, start_time, end_time, notes)
			VALUES ($1, 'Late Booker', 'late@booker.test', $2, $3, '')`,
			id, now.Add(24*time.Hour), now.Add(25*time.Hour))
		return err
	})

	n, err := EraseLinkBookers(context.Background(), db, link, now)
	if err != nil || n != 0 || !seen {
		t.Fatalf("erase = %d, %v (seam ran %v); want 0, nil after the re-check passed", n, err, seen)
	}
	if after := erasureState(t, db, link); !reflect.DeepEqual(after, before) {
		t.Errorf("bookings = %v; want unchanged %v", after, before)
	}
	if e := erasureEntries(t, db, owner); e != 0 {
		t.Errorf("audit entries = %d; want 0", e)
	}
}

// Review of #269, finding 2 (contract §5 step 4): when the audit insert fails,
// the erasure of that link rolls back with it: bookings unchanged, no entry,
// the error returned (its class only is ever logged). A test-only trigger
// makes the insert fail. Fails if the audit write moves after the commit.
func TestPAC40B_FailedAuditInsertRollsTheErasureBack(t *testing.T) {
	db := openTestDB(t)
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	owner, link := erasureFixture(t, db, "pac40b-audit-fails", now)
	if _, err := db.Exec(`
		CREATE FUNCTION pac40b_refuse_audit() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'test: audit insert refused'; END $$;
		CREATE TRIGGER pac40b_refuse_audit BEFORE INSERT ON audit_log
		FOR EACH ROW WHEN (NEW.action = 'scheduling_link_bookers_erased')
		EXECUTE FUNCTION pac40b_refuse_audit();`); err != nil {
		t.Fatal(err)
	}
	before := erasureState(t, db, link)

	n, err := EraseLinkBookers(context.Background(), db, link, now)
	var pg *pgconn.PgError
	if n != 0 || !errors.As(err, &pg) || pg.Code != "P0001" {
		t.Fatalf("erase = %d, %v; want 0 and the trigger's P0001", n, err)
	}
	if after := erasureState(t, db, link); !reflect.DeepEqual(after, before) {
		t.Errorf("bookings changed although the audit entry failed: %v -> %v", before, after)
	}
	if e := erasureEntries(t, db, owner); e != 0 {
		t.Errorf("audit entries = %d; want 0", e)
	}

	// Without the fault the next attempt erases and records it.
	if _, err := db.Exec(`DROP TRIGGER pac40b_refuse_audit ON audit_log`); err != nil {
		t.Fatal(err)
	}
	if n, err := EraseLinkBookers(context.Background(), db, link, now); err != nil || n != 1 {
		t.Fatalf("retry = %d, %v; want 1", n, err)
	}
	if e := erasureEntries(t, db, owner); e != 1 {
		t.Errorf("audit entries after retry = %d; want 1", e)
	}
}
