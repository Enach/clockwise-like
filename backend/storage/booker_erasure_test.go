package storage

// PAC-40 part B, storage side: the transactional audit writer and the
// operator listing (contract §5). The job itself is exercised end to end in
// engine/booker_erasure_test.go.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
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
	if err := WriteAuditLogTx(tx, uuid.Nil, AuditBookersErased, `{}`); !errors.Is(err, ErrAuditNoSubject) {
		t.Errorf("no subject: %v; want ErrAuditNoSubject", err)
	}
	if err := WriteAuditLogTx(tx, owner, AuditBookersErased, `{}`); err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback()
	if n := count(); n != 0 {
		t.Errorf("entries after rollback = %d; want 0 (written in the caller's transaction)", n)
	}

	tx, _ = db.Begin()
	if err := WriteAuditLogTx(tx, uuid.New(), AuditBookersErased, `{}`); err == nil {
		t.Error("an unknown subject (foreign key) was not returned as an error")
	}
	_ = tx.Rollback()

	tx, _ = db.Begin()
	if err := WriteAuditLogTx(tx, owner, AuditBookersErased, `{"a":1}`); err != nil {
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
