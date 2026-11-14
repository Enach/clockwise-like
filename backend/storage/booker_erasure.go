package storage

// PAC-40 part B (contracts/features/PAC-40.md §5; spec docs/specs/PAC-40.md
// §3.5): bookers' names and emails on a DELETED scheduling link are erased
// 90 x 24 h after the link's latest booking end. This file holds the due scan,
// the one-transaction-per-link erasure and the operator listing; the run loop,
// its log line and its error hygiene are engine.BookerErasure.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

// BookerRetention is the time a deleted link's bookers' names and emails are
// kept after its latest booking end: exactly 90 x 24 h, never 90 calendar days
// across a DST change (owner, 2026-10-27; contract §5 "Due rule"). Compared as
// end_time <= now - BookerRetention, computed in Go, so no session time zone
// enters it. Not an operator setting (manifest forbiddenPaths).
const BookerRetention = 90 * 24 * time.Hour

// bookerErasureSet is the ONE place that names what erasure blanks (spec §3.5
// "What is erased"). The bookings_booker_erased_blank CHECK (migration 037)
// requires both columns to be empty strings once booker_erased_at is set.
//
// OQ-8 (owner, open; factory default: notes are KEPT, D-11): if the owner
// answers that notes are erased too, add notes (set to the empty string) to
// this list. That is a contract revision (manifest followUps); the archive
// reads notes as stored, so nothing else in the backend changes.
const bookerErasureSet = `booker_name = '', booker_email = ''`

// AuditBookersErased is the audit action an erasure writes in the link
// owner's activity log (contract calendar.yaml AuditEntry.action).
const AuditBookersErased = "scheduling_link_bookers_erased"

// BookersErasedDetails is the audit entry's details (contract §5 "Audit
// entry", AC-31): marshalled with encoding/json, never a booker's name or
// email. link_title is the owner's own text.
type BookersErasedDetails struct {
	LinkID           uuid.UUID `json:"link_id"`
	LinkTitle        string    `json:"link_title"`
	BookingsErased   int       `json:"bookings_erased"`
	LastBookingEndAt string    `json:"last_booking_end_at"`
	RanAt            string    `json:"ran_at"`
}

// ListBookerErasureDue returns the deleted links due at now, in id order
// (contract §5 "Due rule"): deleted, at least one booking whose booker is
// not yet erased, and max(end_time) over ALL its bookings, every status,
// <= now - BookerRetention. Live, paused and pre-037 inactive links have
// deleted_at null and are never returned (AC-29).
func ListBookerErasureDue(ctx context.Context, db DBTX, now time.Time) ([]uuid.UUID, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT l.id
		FROM scheduling_links l
		WHERE l.deleted_at IS NOT NULL
		  AND EXISTS (SELECT 1 FROM bookings b WHERE b.link_id = l.id AND b.booker_erased_at IS NULL)
		  AND (SELECT max(b.end_time) FROM bookings b WHERE b.link_id = l.id) <= $1
		ORDER BY l.id
	`, now.Add(-BookerRetention))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]uuid.UUID, 0)
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// The erasure transaction's test seams (afterBookerErasureRecheck,
// afterBookerErasureUpdate) live in test_seams.go (PAC-80 item 1).

// EraseLinkBookers erases linkID's bookers' names and emails if, inside its
// own transaction, the link is still deleted and due at now (contract §5 "One
// link = one transaction", binding):
//
//  1. the link-lock bound (PAC-101 §4.2, 10 s per lock wait, SET LOCAL), then
//     the link row FOR UPDATE: only FOR UPDATE conflicts with a booking
//     insert's FOR SHARE (PAC-101) and its foreign key's FOR KEY SHARE, so no
//     booking lands on the link while this transaction is open (K4-3);
//  2. the due rule re-evaluated;
//  3. the UPDATE, which carries the due rule in the same statement (K4-3);
//  4. when rows changed, the owner's audit entry via WriteAuditLogTx;
//  5. commit.
//
// Lock order: link row, its bookings rows, then the audit insert's FOR KEY
// SHARE on the owner's users row (PAC-101 §4.1, "erasure"). Nothing here
// makes a network call. It returns the number of bookings erased (0 when the
// link is gone, live again, not due, or already erased: no audit entry
// then). Any error rolls the link back entirely; the error is the driver's
// and may repeat row data, so the caller classifies it and never logs it.
func EraseLinkBookers(ctx context.Context, db DBTX, linkID uuid.UUID, now time.Time) (int, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := setLinkLockTimeout(tx); err != nil {
		return 0, err
	}
	var owner uuid.UUID
	var title string
	err = tx.QueryRowContext(ctx, `
		SELECT owner_user_id, title FROM scheduling_links
		WHERE id = $1 AND deleted_at IS NOT NULL
		FOR UPDATE
	`, linkID).Scan(&owner, &title)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	cutoff := now.Add(-BookerRetention)
	var lastEnd sql.NullTime
	var awaiting int
	if err := tx.QueryRowContext(ctx, `
		SELECT max(end_time), count(*) FILTER (WHERE booker_erased_at IS NULL)
		FROM bookings WHERE link_id = $1
	`, linkID).Scan(&lastEnd, &awaiting); err != nil {
		return 0, err
	}
	if !lastEnd.Valid || awaiting == 0 || lastEnd.Time.After(cutoff) {
		return 0, nil
	}
	if f, ok := afterBookerErasureRecheck.get(); ok {
		if err := f(tx, linkID); err != nil {
			return 0, err
		}
	}
	res, err := tx.ExecContext(ctx, `
		UPDATE bookings SET `+bookerErasureSet+`, booker_erased_at = $2
		WHERE link_id = $1 AND booker_erased_at IS NULL
		  AND (SELECT max(b.end_time) FROM bookings b WHERE b.link_id = $1) <= $3
	`, linkID, now, cutoff)
	if err != nil {
		return 0, err
	}
	n64, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if n64 == 0 {
		return 0, nil
	}
	if f, ok := afterBookerErasureUpdate.get(); ok {
		if err := f(linkID); err != nil {
			return 0, err
		}
	}
	details, err := json.Marshal(BookersErasedDetails{
		LinkID: linkID, LinkTitle: title, BookingsErased: int(n64),
		LastBookingEndAt: lastEnd.Time.UTC().Format(time.RFC3339),
		RanAt:            now.UTC().Format(time.RFC3339),
	})
	if err != nil {
		return 0, err
	}
	if err := WriteAuditLogTx(ctx, tx, owner, "scheduling_link_bookers_erased", string(details)); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int(n64), nil
}

// BookerErasureStatus is one line of `paceday-operator booker-erasure status`
// (contract §5 "Operator listing", AC-37): ids and instants only, never a
// name, an email or a title.
type BookerErasureStatus struct {
	LinkID           uuid.UUID `json:"link_id"`
	OwnerUserID      uuid.UUID `json:"owner_user_id"`
	DeletedAt        time.Time `json:"deleted_at"`
	LastBookingEndAt time.Time `json:"last_booking_end_at"`
	ErasureDueAt     time.Time `json:"erasure_due_at"`
	Awaiting         int       `json:"awaiting"`
}

// ListBookerErasureStatus returns every deleted link with a booking awaiting
// erasure whose erasure is due by now + within (due, overdue, or due within
// the window), soonest due first.
func ListBookerErasureStatus(ctx context.Context, db DBTX, now time.Time, within time.Duration) ([]BookerErasureStatus, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT l.id, l.owner_user_id, l.deleted_at, agg.last_end, agg.awaiting
		FROM scheduling_links l
		CROSS JOIN LATERAL (
			SELECT max(b.end_time) AS last_end,
			       count(*) FILTER (WHERE b.booker_erased_at IS NULL) AS awaiting
			FROM bookings b WHERE b.link_id = l.id
		) agg
		WHERE l.deleted_at IS NOT NULL AND agg.awaiting > 0
		  AND agg.last_end <= $1
		ORDER BY agg.last_end, l.id
	`, now.Add(within-BookerRetention))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]BookerErasureStatus, 0)
	for rows.Next() {
		var s BookerErasureStatus
		if err := rows.Scan(&s.LinkID, &s.OwnerUserID, &s.DeletedAt, &s.LastBookingEndAt, &s.Awaiting); err != nil {
			return nil, err
		}
		s.ErasureDueAt = s.LastBookingEndAt.Add(BookerRetention)
		out = append(out, s)
	}
	return out, rows.Err()
}

// LifecycleMigrationVersion is the number of migration 037 (PAC-40), read
// from the embedded files: the schema the booker-erasure listing needs.
func LifecycleMigrationVersion() (uint, error) {
	return embeddedMigrationVersion("_scheduling_link_lifecycle.up.sql")
}
