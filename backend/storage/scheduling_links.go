package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lib/pq"
)

// SchedulingLink represents a public booking page.
type SchedulingLink struct {
	ID               uuid.UUID `json:"id"`
	OwnerUserID      uuid.UUID `json:"owner_user_id"`
	Slug             string    `json:"slug"`
	Title            string    `json:"title"`
	DurationOptions  []int     `json:"duration_options"`
	DaysOfWeek       []int     `json:"days_of_week"`
	WindowStart      string    `json:"window_start_time"`
	WindowEnd        string    `json:"window_end_time"`
	BufferBefore     int       `json:"buffer_before"`
	BufferAfter      int       `json:"buffer_after"`
	MinNoticeMinutes int       `json:"min_notice_minutes"`
	UsageType        string    `json:"usage_type"`
	MaxUses          *int      `json:"max_uses,omitempty"`
	// UsesCount is every booking stored for the link, cancelled ones
	// included: a use, once spent, stays spent (PAC-40 D-6, API-051).
	UsesCount int         `json:"uses_count"`
	Active    bool        `json:"active"`
	CreatedAt time.Time   `json:"created_at"`
	Hosts     []*LinkHost `json:"hosts,omitempty"`
	// BookingHorizonDays is how many days ahead the link can be booked,
	// counting today in the link's zone (PAC-72 D-1): 1..365, default 60.
	BookingHorizonDays int `json:"booking_horizon_days"`
	// RefusalMessage is the link owner's choice of 409 body (PAC-72
	// D-2/D-5): "generic" (default) or "specific".
	RefusalMessage string `json:"refusal_message"`
}

// DefaultBookingHorizonDays is the horizon of a link created without one and
// of every link that existed before PAC-72 revision 2.
const DefaultBookingHorizonDays = 60

// RefusalGeneric and RefusalSpecific are the two refusal_message values.
const (
	RefusalGeneric  = "generic"
	RefusalSpecific = "specific"
)

// LinkHost is one accepted or pending co-host on a scheduling link.
type LinkHost struct {
	ID          uuid.UUID  `json:"id"`
	LinkID      uuid.UUID  `json:"link_id"`
	UserID      uuid.UUID  `json:"user_id"`
	Status      string     `json:"status"`
	InvitedAt   time.Time  `json:"invited_at"`
	RespondedAt *time.Time `json:"responded_at,omitempty"`
}

// Booking represents a confirmed meeting booked via a scheduling link.
type Booking struct {
	ID          uuid.UUID `json:"id"`
	LinkID      uuid.UUID `json:"link_id"`
	BookerName  string    `json:"booker_name"`
	BookerEmail string    `json:"booker_email"`
	StartTime   time.Time `json:"start_time"`
	EndTime     time.Time `json:"end_time"`
	Status      string    `json:"status"`
	Notes       string    `json:"notes"`
	CreatedAt   time.Time `json:"created_at"`
	// IdempotencyKey is the optional key of the public booking attempt that
	// saved the row (PAC-82); "" is none. Never returned in any response.
	IdempotencyKey string `json:"-"`
	// Replayed marks a booking returned for a request that carried the key
	// of this already-saved booking with the same details: nothing was
	// written (PAC-82). Never part of a response.
	Replayed bool `json:"-"`
}

// BookingEvent links a booking to a specific host's calendar event ID.
type BookingEvent struct {
	ID              uuid.UUID `json:"id"`
	BookingID       uuid.UUID `json:"booking_id"`
	UserID          uuid.UUID `json:"user_id"`
	CalendarEventID string    `json:"calendar_event_id"`
	CreatedAt       time.Time `json:"created_at"`
}

// --- SchedulingLink CRUD ---------------------------------------------------

func CreateSchedulingLink(db Querier, l *SchedulingLink) (*SchedulingLink, error) {
	if l.UsageType == "" {
		l.UsageType = "reusable"
	}
	if len(l.DurationOptions) == 0 {
		l.DurationOptions = []int{30}
	}
	if len(l.DaysOfWeek) == 0 {
		l.DaysOfWeek = []int{1, 2, 3, 4, 5}
	}
	if l.BookingHorizonDays == 0 {
		l.BookingHorizonDays = DefaultBookingHorizonDays
	}
	if l.RefusalMessage == "" {
		l.RefusalMessage = RefusalGeneric
	}
	row := db.QueryRowContext(context.Background(), `
		INSERT INTO scheduling_links
			(owner_user_id, slug, title, duration_options, days_of_week,
			 window_start_time, window_end_time, buffer_before, buffer_after,
             min_notice_minutes, usage_type, max_uses, booking_horizon_days, refusal_message)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		RETURNING id, owner_user_id, slug, title, duration_options, days_of_week,
			window_start_time, window_end_time, buffer_before, buffer_after,
            min_notice_minutes, usage_type, max_uses,
            (SELECT COUNT(*) FROM bookings b WHERE b.link_id = scheduling_links.id),
            active, created_at, booking_horizon_days, refusal_message
	`, l.OwnerUserID, l.Slug, l.Title,
		pq.Array(l.DurationOptions), pq.Array(l.DaysOfWeek),
		l.WindowStart, l.WindowEnd, l.BufferBefore, l.BufferAfter,
		l.MinNoticeMinutes, l.UsageType, l.MaxUses, l.BookingHorizonDays, l.RefusalMessage)
	return scanSchedulingLink(row)
}

// GetSchedulingLinkBySlug is the public lookup: a link that is paused or
// deleted is not found (PAC-40: deleted_at IS NULL as well as active, so a
// deleted link is never bookable even on a row that somehow kept active =
// true, challenge K4-1).
func GetSchedulingLinkBySlug(db DBTX, slug string) (*SchedulingLink, error) {
	row := db.QueryRowContext(context.Background(), `
		SELECT id, owner_user_id, slug, title, duration_options, days_of_week,
			window_start_time, window_end_time, buffer_before, buffer_after,
            min_notice_minutes, usage_type, max_uses,
            (SELECT COUNT(*) FROM bookings b WHERE b.link_id = scheduling_links.id),
            active, created_at, booking_horizon_days, refusal_message
		FROM scheduling_links WHERE slug = $1 AND active = true AND deleted_at IS NULL
	`, slug)
	return scanSchedulingLink(row)
}

// GetSchedulingLinkByID returns a live or paused link; a deleted one is nil,
// exactly as an id that never existed (PAC-40 D-9). Every live by-id path
// reads through it, so "deleted" is checked where "no such link" is: before
// any owner or host check and before a request body is read (contract §3).
// The owner reaches a deleted link only through the archive reads below.
func GetSchedulingLinkByID(db DBTX, id uuid.UUID) (*SchedulingLink, error) {
	if err := fireSeamErr(&linkReadByID, id); err != nil {
		return nil, err
	}
	row := db.QueryRowContext(context.Background(), `
		SELECT id, owner_user_id, slug, title, duration_options, days_of_week,
			window_start_time, window_end_time, buffer_before, buffer_after,
            min_notice_minutes, usage_type, max_uses,
            (SELECT COUNT(*) FROM bookings b WHERE b.link_id = scheduling_links.id),
            active, created_at, booking_horizon_days, refusal_message
		FROM scheduling_links WHERE id = $1 AND deleted_at IS NULL
	`, id)
	return scanSchedulingLink(row)
}

func ListSchedulingLinksByUser(db DBTX, userID uuid.UUID) ([]*SchedulingLink, error) {
	rows, err := db.QueryContext(context.Background(), `
		SELECT DISTINCT sl.id, sl.owner_user_id, sl.slug, sl.title,
			sl.duration_options, sl.days_of_week,
			sl.window_start_time, sl.window_end_time,
			sl.buffer_before, sl.buffer_after, sl.min_notice_minutes, sl.usage_type, sl.max_uses,
            (SELECT COUNT(*) FROM bookings b WHERE b.link_id = sl.id),
            sl.active, sl.created_at, sl.booking_horizon_days, sl.refusal_message
		FROM scheduling_links sl
		LEFT JOIN scheduling_link_hosts slh ON slh.link_id = sl.id
		WHERE sl.deleted_at IS NULL
		  AND (sl.owner_user_id = $1 OR (slh.user_id = $1 AND slh.status = 'accepted'))
		ORDER BY sl.created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSchedulingLinks(rows)
}

// UpdateSchedulingLink writes l over link id. A zero BookingHorizonDays or an
// empty RefusalMessage keeps the stored value (PAC-72: callers that predate
// those settings never reset them). A deleted link is never written: the
// UPDATE re-checks deleted_at IS NULL itself, so a PATCH that read the link
// before a concurrent delete committed cannot write active = true back onto
// it; the result is then nil, nil, as for an unknown id (PAC-40, K4-1).
func UpdateSchedulingLink(db Querier, id uuid.UUID, l *SchedulingLink) (*SchedulingLink, error) {
	row := db.QueryRowContext(context.Background(), `
		UPDATE scheduling_links SET
			slug = $2, title = $3, duration_options = $4, days_of_week = $5,
			window_start_time = $6, window_end_time = $7,
			buffer_before = $8, buffer_after = $9, min_notice_minutes = $10,
            usage_type = $11, max_uses = $12, active = $13,
            booking_horizon_days = COALESCE(NULLIF($14, 0), booking_horizon_days),
            refusal_message = COALESCE(NULLIF($15, ''), refusal_message)
		WHERE id = $1 AND deleted_at IS NULL
		RETURNING id, owner_user_id, slug, title, duration_options, days_of_week,
			window_start_time, window_end_time, buffer_before, buffer_after,
            min_notice_minutes, usage_type, max_uses,
            (SELECT COUNT(*) FROM bookings b WHERE b.link_id = scheduling_links.id),
            active, created_at, booking_horizon_days, refusal_message
	`, id, l.Slug, l.Title, pq.Array(l.DurationOptions), pq.Array(l.DaysOfWeek),
		l.WindowStart, l.WindowEnd, l.BufferBefore, l.BufferAfter, l.MinNoticeMinutes,
		l.UsageType, l.MaxUses, l.Active, l.BookingHorizonDays, l.RefusalMessage)
	return scanSchedulingLink(row)
}

// DeleteSchedulingLink moves link id to the deleted state: it records the
// instant and pauses it, and keeps the link, its host rows and its bookings
// (PAC-40 D-2). deleted is false, with nothing written, when the link does
// not exist or is already deleted: a repeated or concurrent delete never
// moves deleted_at, the instant the archive shows (K4-2).
func DeleteSchedulingLink(db Querier, id uuid.UUID) (deleted bool, err error) {
	res, err := db.ExecContext(context.Background(),
		`UPDATE scheduling_links SET deleted_at = NOW(), active = false
		 WHERE id = $1 AND deleted_at IS NULL`, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func scanSchedulingLink(row *sql.Row) (*SchedulingLink, error) {
	var l SchedulingLink
	var durOpts, daysOfWeek pq.Int64Array
	err := row.Scan(
		&l.ID, &l.OwnerUserID, &l.Slug, &l.Title,
		&durOpts, &daysOfWeek,
		&l.WindowStart, &l.WindowEnd, &l.BufferBefore, &l.BufferAfter,
		&l.MinNoticeMinutes, &l.UsageType, &l.MaxUses, &l.UsesCount,
		&l.Active, &l.CreatedAt, &l.BookingHorizonDays, &l.RefusalMessage,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	l.DurationOptions = int64SliceToInt(durOpts)
	l.DaysOfWeek = int64SliceToInt(daysOfWeek)
	return &l, nil
}

func scanSchedulingLinks(rows *sql.Rows) ([]*SchedulingLink, error) {
	var out []*SchedulingLink
	for rows.Next() {
		var l SchedulingLink
		var durOpts, daysOfWeek pq.Int64Array
		if err := rows.Scan(
			&l.ID, &l.OwnerUserID, &l.Slug, &l.Title,
			&durOpts, &daysOfWeek,
			&l.WindowStart, &l.WindowEnd, &l.BufferBefore, &l.BufferAfter,
			&l.MinNoticeMinutes, &l.UsageType, &l.MaxUses, &l.UsesCount,
			&l.Active, &l.CreatedAt, &l.BookingHorizonDays, &l.RefusalMessage,
		); err != nil {
			return nil, err
		}
		l.DurationOptions = int64SliceToInt(durOpts)
		l.DaysOfWeek = int64SliceToInt(daysOfWeek)
		out = append(out, &l)
	}
	return out, rows.Err()
}

// --- LinkHost CRUD ---------------------------------------------------------

func AddLinkHost(db Querier, linkID, userID uuid.UUID, status string) (*LinkHost, error) {
	row := db.QueryRowContext(context.Background(), `
		INSERT INTO scheduling_link_hosts (link_id, user_id, status)
		VALUES ($1, $2, $3)
		ON CONFLICT (link_id, user_id) DO UPDATE SET status = EXCLUDED.status
		RETURNING id, link_id, user_id, status, invited_at, responded_at
	`, linkID, userID, status)
	return scanLinkHost(row)
}

// InviteLinkHost adds userID to linkID as a pending co-host. If the user is
// already a host of the link, their row is returned unchanged and created is
// false: an invite never changes an existing host's status, so re-inviting an
// accepted co-host cannot drop their calendar from the link's slots (PAC-61).
//
// It takes no lock itself. The PAC-120 co-host count (at most
// CoHostListLimit, checked in inviteLinkHostLocked) is exact only while every
// inserter into scheduling_link_hosts takes the link lock first; a caller
// that inserts here, or anywhere else, without that lock can push a link past
// the limit.
func InviteLinkHost(db Querier, linkID, userID uuid.UUID) (host *LinkHost, created bool, err error) {
	row := db.QueryRowContext(context.Background(), `
		INSERT INTO scheduling_link_hosts (link_id, user_id, status)
		VALUES ($1, $2, 'pending')
		ON CONFLICT (link_id, user_id) DO NOTHING
		RETURNING id, link_id, user_id, status, invited_at, responded_at
	`, linkID, userID)
	host, err = scanLinkHost(row)
	if err != nil {
		return nil, false, err
	}
	if host != nil {
		return host, true, nil
	}
	// DO NOTHING returned no row: the user is already a host. Return them as is.
	row = db.QueryRowContext(context.Background(), `
		SELECT id, link_id, user_id, status, invited_at, responded_at
		FROM scheduling_link_hosts WHERE link_id = $1 AND user_id = $2
	`, linkID, userID)
	host, err = scanLinkHost(row)
	return host, false, err
}

// RemoveLinkHostsExcept deletes every non-owner host of linkID whose user is
// not in keep. Hosts in keep are left exactly as they are.
func RemoveLinkHostsExcept(db Querier, linkID, ownerID uuid.UUID, keep []uuid.UUID) error {
	ids := make([]string, len(keep))
	for i, id := range keep {
		ids[i] = id.String()
	}
	_, err := db.ExecContext(context.Background(), `
		DELETE FROM scheduling_link_hosts
		WHERE link_id = $1 AND user_id <> $2 AND NOT (user_id::text = ANY($3::text[]))
	`, linkID, ownerID, pq.Array(ids))
	return err
}

func GetLinkHosts(db DBTX, linkID uuid.UUID) ([]*LinkHost, error) {
	rows, err := db.QueryContext(context.Background(), `
		SELECT id, link_id, user_id, status, invited_at, responded_at
		FROM scheduling_link_hosts WHERE link_id = $1 ORDER BY invited_at
	`, linkID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*LinkHost
	for rows.Next() {
		h, err := scanLinkHostRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// GetLinkHostStatus returns the user's own host status on the link. found is
// false, with a nil error, when the user has no host row on it; any other
// failure is returned so a caller can tell "no row" from "could not read".
func GetLinkHostStatus(db DBTX, linkID, userID uuid.UUID) (status string, found bool, err error) {
	err = db.QueryRowContext(context.Background(), `
		SELECT status FROM scheduling_link_hosts WHERE link_id = $1 AND user_id = $2
	`, linkID, userID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return status, true, nil
}

func GetAcceptedHosts(db DBTX, linkID uuid.UUID) ([]*LinkHost, error) {
	rows, err := db.QueryContext(context.Background(), `
		SELECT id, link_id, user_id, status, invited_at, responded_at
		FROM scheduling_link_hosts WHERE link_id = $1 AND status = 'accepted'
	`, linkID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*LinkHost
	for rows.Next() {
		h, err := scanLinkHostRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// GetPendingInvitesForUser lists the user's pending co-host invites on live or
// paused links; an invite to a deleted link is omitted (PAC-40 AC-5).
func GetPendingInvitesForUser(db DBTX, userID uuid.UUID) ([]*LinkHost, error) {
	rows, err := db.QueryContext(context.Background(), `
		SELECT h.id, h.link_id, h.user_id, h.status, h.invited_at, h.responded_at
		FROM scheduling_link_hosts h
		JOIN scheduling_links l ON l.id = h.link_id
		WHERE h.user_id = $1 AND h.status = 'pending' AND l.deleted_at IS NULL
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*LinkHost
	for rows.Next() {
		h, err := scanLinkHostRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// RemoveLinkHost deletes userID's host row on linkID. removed is false when
// no row matched (the user was never a host, or already left): PAC-30. A
// deleted link's host rows are kept (PAC-40 D-2, AC-5): the DELETE matches
// only on a link that is not deleted.
func RemoveLinkHost(db Querier, linkID, userID uuid.UUID) (removed bool, err error) {
	res, err := db.ExecContext(context.Background(), `
		DELETE FROM scheduling_link_hosts
		WHERE link_id = $1 AND user_id = $2
		  AND EXISTS (SELECT 1 FROM scheduling_links l WHERE l.id = $1 AND l.deleted_at IS NULL)`,
		linkID, userID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// RespondToHostInvite sets userID's host row on linkID to status. matched is
// false when the user has no host row on that link (or no such link exists,
// or the link is deleted, PAC-40 AC-5); nothing is written in that case:
// PAC-30.
func RespondToHostInvite(db Querier, linkID, userID uuid.UUID, status string) (matched bool, err error) {
	res, err := db.Exec(`
		UPDATE scheduling_link_hosts
		SET status = $3, responded_at = NOW()
		WHERE link_id = $1 AND user_id = $2
		  AND EXISTS (SELECT 1 FROM scheduling_links l WHERE l.id = $1 AND l.deleted_at IS NULL)
	`, linkID, userID, status)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func scanLinkHost(row *sql.Row) (*LinkHost, error) {
	var h LinkHost
	var respondedAt sql.NullTime
	err := row.Scan(&h.ID, &h.LinkID, &h.UserID, &h.Status, &h.InvitedAt, &respondedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if respondedAt.Valid {
		h.RespondedAt = &respondedAt.Time
	}
	return &h, err
}

func scanLinkHostRow(rows *sql.Rows) (*LinkHost, error) {
	var h LinkHost
	var respondedAt sql.NullTime
	err := rows.Scan(&h.ID, &h.LinkID, &h.UserID, &h.Status, &h.InvitedAt, &respondedAt)
	if respondedAt.Valid {
		h.RespondedAt = &respondedAt.Time
	}
	return &h, err
}

// --- Booking CRUD ----------------------------------------------------------

func CreateBooking(db DBTX, b *Booking) (*Booking, error) {
	row := db.QueryRowContext(context.Background(), `
		INSERT INTO bookings (link_id, booker_name, booker_email, start_time, end_time, notes)
		VALUES ($1,$2,$3,$4,$5,$6)
		RETURNING id, link_id, booker_name, booker_email, start_time, end_time, status, notes, created_at
	`, b.LinkID, b.BookerName, b.BookerEmail, b.StartTime, b.EndTime, b.Notes)
	return scanBooking(row)
}

// --- PAC-101: writes ordered against the link's delete ----------------------

// ErrLinkGone is the commit-point outcome "the link is gone" (PAC-101
// contract §3): the link was deleted (or, for a booking, paused) before the
// write took its lock on the link, so nothing was written. It is a result of
// its own, never a nil row with a nil error.
var ErrLinkGone = errors.New("scheduling link is gone")

// LinkLockTimeout bounds each wait for a lock on a scheduling link (and the
// rows a link write locks after it) in a request-path transaction: PostgreSQL
// lock_timeout semantics, per lock acquisition (contract §4.2). Past it the
// statement fails with SQLSTATE 55P03 and the transaction is rolled back. A
// package variable only so that tests can shorten it; nothing else assigns it.
var LinkLockTimeout = 10 * time.Second

// The PATCH's test seam (afterLinkPatchUpdate, PAC-101 contract §5) lives in
// test_seams.go (PAC-80 item 1).

// setLinkLockTimeout sets the bound for the rest of q's transaction only:
// set_config(..., true) is SET LOCAL, so it ends with the transaction, commit
// or rollback, and never reaches the next user of the pooled connection
// (contract §4.3; a session-level setting is forbidden there).
func setLinkLockTimeout(q Querier) error {
	_, err := q.ExecContext(context.Background(), `SELECT set_config('lock_timeout', $1, true)`,
		fmt.Sprintf("%dms", LinkLockTimeout.Milliseconds()))
	return err
}

// lockLiveLinkForShare is the host writes' commit-point check: it locks link
// id FOR SHARE when it is not deleted. FOR SHARE conflicts with the delete's
// FOR NO KEY UPDATE (so a delete in progress makes this wait, and a delete
// started after it waits for this transaction) and not with another FOR SHARE.
// After a wait, READ COMMITTED re-evaluates the WHERE on the newest version,
// so a delete that committed meanwhile yields ErrLinkGone. Pausing does not
// count: host writes work on a paused link (contract §2.3).
func lockLiveLinkForShare(q Querier, id uuid.UUID) error {
	var one int
	err := q.QueryRowContext(context.Background(),
		`SELECT 1 FROM scheduling_links WHERE id = $1 AND deleted_at IS NULL FOR SHARE`, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrLinkGone
	}
	return err
}

// SaveBookingOnLiveLink is the booking save at the commit point (PAC-101
// contract section 3.1 step 5, as replaced by PAC-82 contract section 3): one
// explicit READ COMMITTED transaction on conn that sets the link-lock bound
// for itself, locks the link FOR SHARE while it is active and not deleted,
// takes the save's advisory keys (BookingAdvisoryKeys) one statement each in
// ascending order, and only then decides the booking again in separate
// statements: the idempotency key (replay or key reused), the link's use
// limit, the overlap with the link's own bookings and with every rule host's
// bookings on any link. Each outcome is distinct and never nil, nil:
// ErrLinkGone, ErrIdempotencyKeyReused, ErrLinkExhausted, ErrSlotTaken,
// ErrHostBusy, a booking with Replayed set (nothing written), or the saved
// booking. wrap, when not nil, wraps the transaction for the insert statement
// only (the PAC-90 seam).
//
// There is deliberately no deferred rollback: on a panic the caller cancels
// ctx, which makes database/sql close any open row and roll the transaction
// back; a deferred Rollback would instead wait on a row the panic left open
// (PAC-90 item 1, challenge C-1).
func SaveBookingOnLiveLink(ctx context.Context, conn *sql.Conn, b *Booking, wrap func(Querier) Querier) (*Booking, error) {
	// READ COMMITTED (the default): every statement after a wait takes a new
	// snapshot and sees the winner (contract challenge C-2).
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	booking, err := saveBookingInTx(ctx, tx, b, wrap)
	if errors.Is(err, errBookingKeyConflict) {
		// Step 9: a same-key request with other details took other keys and
		// committed first; resolve against its stored row (step 6's rule).
		_ = tx.Rollback()
		replay, reused, lerr := ResolveIdempotencyKey(conn, b.LinkID, b.IdempotencyKey, b)
		switch {
		case lerr != nil:
			return nil, lerr
		case replay != nil:
			return replay, nil
		case reused:
			return nil, ErrIdempotencyKeyReused
		}
		return nil, errors.New("idempotency key conflict without a stored row")
	}
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if booking.Replayed {
		_ = tx.Rollback()
		return booking, nil
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return booking, nil
}

// The outcomes of a save that wrote nothing (PAC-82 contract section 3).
var (
	// ErrSlotTaken: a non-cancelled booking of the same link overlaps the
	// slot (the 409 taken).
	ErrSlotTaken = errors.New("slot already booked on this link")
	// ErrHostBusy: a non-cancelled booking on another link of a rule host
	// overlaps the slot (the 409 host_busy).
	ErrHostBusy = errors.New("a host is booked at that time")
	// ErrLinkExhausted: the link's use limit is reached (the 410).
	ErrLinkExhausted = errors.New("link use limit reached")
	// ErrIdempotencyKeyReused: the key is stored on this link with other
	// details, or 24 hours ago or more (the key-reused 409).
	ErrIdempotencyKeyReused = errors.New("idempotency key already used")
)

// errBookingKeyConflict is the insert's 23505 on (link, key).
var errBookingKeyConflict = errors.New("idempotency key conflict")

// BookingIdempotencyTTL is how long a stored key replays (PAC-82 D-7).
const BookingIdempotencyTTL = 24 * time.Hour

// MatchesAttempt reports whether the stored booking b has the details of the
// attempt a (PAC-82 contract section 1.2): name and email byte for byte (the
// caller trims them), start and end as instants, notes byte for byte.
func (b *Booking) MatchesAttempt(a *Booking) bool {
	return b.BookerName == a.BookerName && b.BookerEmail == a.BookerEmail &&
		b.StartTime.Equal(a.StartTime) && b.EndTime.Equal(a.EndTime) && b.Notes == a.Notes
}

// ResolveIdempotencyKey decides a keyed attempt against the booking stored on
// linkID with key (contract sections 1.2 and 3 step 6): none stored is nil,
// false; stored with the attempt's details and younger than 24 hours by the
// database clock is that booking with Replayed set; anything else is
// reused = true.
func ResolveIdempotencyKey(db RowQuerier, linkID uuid.UUID, key string, attempt *Booking) (replay *Booking, reused bool, err error) {
	if key == "" {
		return nil, false, nil
	}
	var stored Booking
	var expired bool
	err = db.QueryRowContext(context.Background(), `
		SELECT id, link_id, booker_name, booker_email, start_time, end_time, status, notes, created_at,
		       now() - created_at >= make_interval(secs => $3)
		FROM bookings WHERE link_id = $1 AND idempotency_key = $2`,
		linkID, key, BookingIdempotencyTTL.Seconds()).Scan(
		&stored.ID, &stored.LinkID, &stored.BookerName, &stored.BookerEmail,
		&stored.StartTime, &stored.EndTime, &stored.Status, &stored.Notes, &stored.CreatedAt, &expired)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if expired || !stored.MatchesAttempt(attempt) {
		return nil, true, nil
	}
	stored.IdempotencyKey = key
	stored.Replayed = true
	return &stored, false, nil
}

// bookingLimit is the link's use limit: 1 for single_use, max_uses for a
// recurring link that has one; limited is false otherwise.
func bookingLimit(usageType string, maxUses sql.NullInt64) (limit int64, limited bool) {
	switch {
	case usageType == "single_use":
		return 1, true
	case usageType == "recurring" && maxUses.Valid:
		return maxUses.Int64, true
	}
	return 0, false
}

func saveBookingInTx(ctx context.Context, tx *sql.Tx, b *Booking, wrap func(Querier) Querier) (*Booking, error) {
	// 1. The PAC-101 bound, for every wait of this transaction.
	if err := setLinkLockTimeout(tx); err != nil {
		return nil, err
	}
	// 2. The link FOR SHARE, live only.
	var owner uuid.UUID
	var usageType string
	var maxUses sql.NullInt64
	err := tx.QueryRowContext(ctx, `
		SELECT owner_user_id, usage_type, max_uses FROM scheduling_links
		WHERE id = $1 AND active AND deleted_at IS NULL FOR SHARE`, b.LinkID).Scan(&owner, &usageType, &maxUses)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrLinkGone
	}
	if err != nil {
		return nil, err
	}
	// 3. The rule host set (the PAC-72 accepted hosts) and the lock host set
	// (owner and accepted co-hosts), plain reads.
	ruleHosts, lockHosts, err := bookingHostSets(ctx, tx, b.LinkID, owner)
	if err != nil {
		return nil, err
	}
	limit, useLimited := bookingLimit(usageType, maxUses)
	// 4. The advisory keys, one statement per key, ascending. A statement
	// taking several keys is forbidden (contract section 3 step 4).
	for _, k := range BookingAdvisoryKeys(lockHosts, b.LinkID, useLimited, b.StartTime, b.EndTime) {
		q := `SELECT pg_advisory_xact_lock($1::int, $2::int)`
		if k.Shared {
			q = `SELECT pg_advisory_xact_lock_shared($1::int, $2::int)`
		}
		if _, err := tx.ExecContext(ctx, q, k.Class, k.ObjID); err != nil {
			return nil, err
		}
	}
	// 5. The seam.
	if f, ok := afterBookingLocks.get(); ok {
		f(b)
	}
	// 6. The key, decided under the keys. Steps 6-8 are separate statements
	// issued after the last lock statement returned (challenge C-2).
	if b.IdempotencyKey != "" {
		replay, reused, err := ResolveIdempotencyKey(tx, b.LinkID, b.IdempotencyKey, b)
		if err != nil {
			return nil, err
		}
		if replay != nil {
			return replay, nil
		}
		if reused {
			return nil, ErrIdempotencyKeyReused
		}
	}
	// 7. The use limit, every status counted (PAC-40 D-6).
	if useLimited {
		var n int64
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM bookings WHERE link_id = $1`, b.LinkID).Scan(&n); err != nil {
			return nil, err
		}
		if n >= limit {
			return nil, ErrLinkExhausted
		}
	}
	// 8. The overlap: this link's bookings, then the rule hosts' bookings on
	// any link they own or co-host as accepted (the bookings half of the
	// PAC-72 rule, GetBusyBookingsForUser's predicate; raw overlap, no
	// buffers). If that rule ever widens bookings, the buckets must cover
	// the widened interval.
	var taken bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM bookings
		WHERE link_id = $1 AND status <> 'cancelled'
		  AND start_time < $3 AND end_time > $2)`, b.LinkID, b.StartTime, b.EndTime).Scan(&taken); err != nil {
		return nil, err
	}
	if taken {
		return nil, ErrSlotTaken
	}
	if len(ruleHosts) > 0 {
		var busy bool
		if err := tx.QueryRowContext(ctx, `
			SELECT EXISTS (SELECT 1 FROM bookings bk
			JOIN scheduling_links l ON l.id = bk.link_id
			WHERE bk.status <> 'cancelled'
			  AND bk.start_time < $3 AND bk.end_time > $2
			  AND (l.owner_user_id = ANY($1::uuid[])
			       OR EXISTS (SELECT 1 FROM scheduling_link_hosts h
			                  WHERE h.link_id = l.id AND h.user_id = ANY($1::uuid[]) AND h.status = 'accepted')))`,
			pq.Array(ruleHosts), b.StartTime, b.EndTime).Scan(&busy); err != nil {
			return nil, err
		}
		if busy {
			return nil, ErrHostBusy
		}
	}
	// 9. The insert, carrying the key when there is one.
	var q Querier = tx
	if wrap != nil {
		q = wrap(tx)
	}
	var key sql.NullString
	if b.IdempotencyKey != "" {
		key = sql.NullString{String: b.IdempotencyKey, Valid: true}
	}
	row := q.QueryRowContext(ctx, `
		INSERT INTO bookings (link_id, booker_name, booker_email, start_time, end_time, notes, idempotency_key)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, link_id, booker_name, booker_email, start_time, end_time, status, notes, created_at
	`, b.LinkID, b.BookerName, b.BookerEmail, b.StartTime, b.EndTime, b.Notes, key)
	var out Booking
	err = row.Scan(&out.ID, &out.LinkID, &out.BookerName, &out.BookerEmail,
		&out.StartTime, &out.EndTime, &out.Status, &out.Notes, &out.CreatedAt)
	if isUniqueViolationOn(err, "bookings_link_idempotency_key") {
		return nil, errBookingKeyConflict
	}
	if err != nil {
		return nil, err
	}
	out.IdempotencyKey = b.IdempotencyKey
	if f, ok := afterBookingInsert.get(); ok {
		if err := f(b); err != nil {
			return nil, err
		}
	}
	return &out, nil
}

// bookingHostSets reads the link's accepted hosts (GetAcceptedHosts' rule
// host set, as strings) and the lock host set: the owner and those hosts.
func bookingHostSets(ctx context.Context, tx *sql.Tx, linkID, owner uuid.UUID) (ruleHosts []string, lockHosts []uuid.UUID, err error) {
	rows, err := tx.QueryContext(ctx, `SELECT user_id FROM scheduling_link_hosts WHERE link_id = $1 AND status = 'accepted'`, linkID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	ruleHosts = []string{}
	lockHosts = []uuid.UUID{owner}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, nil, err
		}
		ruleHosts = append(ruleHosts, id.String())
		lockHosts = append(lockHosts, id)
	}
	return ruleHosts, lockHosts, rows.Err()
}

// isUniqueViolationOn reports whether err is SQLSTATE 23505 on constraint.
func isUniqueViolationOn(err error, constraint string) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505" && pg.ConstraintName == constraint
}

// ErrCoHostLimitReached is the single invitation refused because the link
// already has CoHostListLimit or more co-hosts (non-owner host rows, any
// status), decided under the link lock inside the write (PAC-120 contract md
// §2, §3 step 5). Nothing was written; the caller answers 422.
var ErrCoHostLimitReached = errors.New("scheduling link has reached its co-host limit")

// inviteLinkHostLocked is the single invitation behind the link lock (PAC-120
// contract md §3). The link is locked FOR NO KEY UPDATE, the mode of the
// PATCH and the delete, so invitations are ordered against each other and
// against list saves (md §4.1), and the owner is read under that lock. Then:
// an existing row is returned unchanged (PAC-61); the owner's own address is
// today's InviteLinkHost, never counted; CoHostListLimit or more non-owner
// rows is ErrCoHostLimitReached; otherwise the pending row is inserted. Link
// gone is decided by the lock step only, never by the insert's zero rows.
// Every writer that can add a host row to an existing link takes this lock
// in a conflicting mode first, so the count is exact (md §3).
func inviteLinkHostLocked(ctx context.Context, q Querier, linkID, userID uuid.UUID) (*LinkHost, bool, error) {
	var owner uuid.UUID
	err := q.QueryRowContext(context.Background(),
		`SELECT owner_user_id FROM scheduling_links WHERE id = $1 AND deleted_at IS NULL FOR NO KEY UPDATE`,
		linkID).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, ErrLinkGone
	}
	if err != nil {
		return nil, false, err
	}
	if fn, ok := afterLinkInviteLock.get(); ok {
		if err := fn(ctx, linkID); err != nil {
			return nil, false, err
		}
	}
	existing, err := scanLinkHost(q.QueryRowContext(context.Background(), `
		SELECT id, link_id, user_id, status, invited_at, responded_at
		FROM scheduling_link_hosts WHERE link_id = $1 AND user_id = $2
	`, linkID, userID))
	if err != nil {
		return nil, false, err
	}
	if existing != nil {
		return existing, false, nil
	}
	if userID == owner {
		return InviteLinkHost(q, linkID, userID)
	}
	var coHosts int
	if err := q.QueryRowContext(context.Background(),
		`SELECT count(*) FROM scheduling_link_hosts WHERE link_id = $1 AND user_id <> $2`,
		linkID, owner).Scan(&coHosts); err != nil {
		return nil, false, err
	}
	if coHosts >= CoHostListLimit {
		return nil, false, ErrCoHostLimitReached
	}
	return InviteLinkHost(q, linkID, userID)
}

// respondToHostInviteLocked is RespondToHostInvite behind the link lock; a
// gone link is matched = false, the PAC-30 404 by design.
func respondToHostInviteLocked(q Querier, linkID, userID uuid.UUID, status string) (bool, error) {
	if err := lockLiveLinkForShare(q, linkID); errors.Is(err, ErrLinkGone) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	return RespondToHostInvite(q, linkID, userID, status)
}

// removeLinkHostLocked is RemoveLinkHost (leave) behind the link lock.
func removeLinkHostLocked(q Querier, linkID, userID uuid.UUID) (bool, error) {
	if err := lockLiveLinkForShare(q, linkID); errors.Is(err, ErrLinkGone) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	return RemoveLinkHost(q, linkID, userID)
}

// updateSchedulingLinkAndCoHosts is a PATCH as one unit (contract §3.2, spec
// D-5): the guarded link UPDATE is the transaction's first lock on the link
// (exclusive; never FOR SHARE first and an upgrade after, which would let two
// PATCHes deadlock), then, when coHostEmails is not nil, the co-host list is
// set by difference (PAC-61) on the same transaction and reported (PAC-112
// contract md §3.2: H is read after the UPDATE, so the over-5 rule is decided
// under the lock and a deleted link is ErrLinkGone first). A link deleted
// before the UPDATE is ErrLinkGone; any error makes the caller roll
// everything back. changes is nil exactly when coHostEmails is nil.
func updateSchedulingLinkAndCoHosts(q Querier, id uuid.UUID, l *SchedulingLink, ownerID uuid.UUID, coHostEmails []string) (*SchedulingLink, *CoHostChanges, error) {
	result, err := UpdateSchedulingLink(q, id, l)
	if err != nil {
		return nil, nil, err
	}
	if result == nil {
		return nil, nil, ErrLinkGone
	}
	if f, ok := afterLinkPatchUpdate.get(); ok {
		if err := f(id); err != nil {
			return nil, nil, err
		}
	}
	if coHostEmails == nil {
		return result, nil, nil
	}
	// PAC-71: the keep set is a KEEPING site (every account carrying a listed
	// address, any standing, ignoring letter case, AC-43); new entries are a
	// GRANTING site (only the one domain-confirmed holder). The owner's row is
	// never written (PAC-112 contract md §2.2 rule 1).
	changes, err := setCoHostList(q, id, ownerID, coHostEmails)
	if err != nil {
		return nil, nil, err
	}
	return result, changes, nil
}

func HasOverlappingBooking(db DBTX, linkID uuid.UUID, start, end time.Time) (bool, error) {
	var exists bool
	err := db.QueryRowContext(context.Background(), `
		SELECT EXISTS(SELECT 1 FROM bookings
		WHERE link_id = $1 AND status <> 'cancelled'
		  AND start_time < $3 AND end_time > $2)`, linkID, start, end).Scan(&exists)
	return exists, err
}

func GetBookingsByLink(db DBTX, linkID uuid.UUID) ([]*Booking, error) {
	rows, err := db.QueryContext(context.Background(), `
		SELECT id, link_id, booker_name, booker_email, start_time, end_time, status, notes, created_at
		FROM bookings WHERE link_id = $1 ORDER BY start_time DESC
	`, linkID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Booking
	for rows.Next() {
		var b Booking
		if err := rows.Scan(
			&b.ID, &b.LinkID, &b.BookerName, &b.BookerEmail,
			&b.StartTime, &b.EndTime, &b.Status, &b.Notes, &b.CreatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, &b)
	}
	return out, rows.Err()
}

// GetBusyBookingsForUser returns the time ranges of every booking that is not
// cancelled (pending or confirmed, PAC-72 D-11) across any scheduling link the
// user hosts: links they own, and links where they are an accepted co-host.
// It does not depend on a calendar event having been created for the booking
// (booking_events), so a host with no calendar connected, or whose event
// creation failed, is still busy at a booked time (PAC-64).
func GetBusyBookingsForUser(db DBTX, userID uuid.UUID, start, end time.Time) ([]time.Time, error) {
	rows, err := db.QueryContext(context.Background(), `
		SELECT b.start_time, b.end_time
		FROM bookings b
		JOIN scheduling_links l ON l.id = b.link_id
		WHERE b.status <> 'cancelled'
		  AND b.start_time < $3
		  AND b.end_time   > $2
		  AND (l.owner_user_id = $1
		       OR EXISTS (SELECT 1 FROM scheduling_link_hosts h
		                  WHERE h.link_id = l.id AND h.user_id = $1 AND h.status = 'accepted'))
		ORDER BY b.start_time
	`, userID, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var times []time.Time
	for rows.Next() {
		var s, e time.Time
		if err := rows.Scan(&s, &e); err != nil {
			return nil, err
		}
		times = append(times, s, e)
	}
	return times, rows.Err()
}

func scanBooking(row *sql.Row) (*Booking, error) {
	var b Booking
	err := row.Scan(
		&b.ID, &b.LinkID, &b.BookerName, &b.BookerEmail,
		&b.StartTime, &b.EndTime, &b.Status, &b.Notes, &b.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &b, err
}

// --- BookingEvent CRUD -----------------------------------------------------

func SaveBookingEvent(db DBTX, bookingID, userID uuid.UUID, calEventID string) error {
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO booking_events (booking_id, user_id, calendar_event_id)
		VALUES ($1, $2, $3)
	`, bookingID, userID, calEventID)
	return err
}

// --- Slug helpers -----------------------------------------------------------

func SlugExists(db DBTX, slug string) (bool, error) {
	var exists bool
	err := db.QueryRowContext(context.Background(),
		`SELECT EXISTS(SELECT 1 FROM scheduling_links WHERE slug = $1)`, slug).Scan(&exists)
	return exists, err
}

// --- Archive of deleted links (PAC-40 §3.2) -----------------------------------

// ArchivedLink is one deleted link as its owner's archive shows it. Counts and
// instants are over every booking of the link, whatever its status (D-12).
type ArchivedLink struct {
	ID          uuid.UUID `json:"id"`
	OwnerUserID uuid.UUID `json:"owner_user_id"`
	Slug        string    `json:"slug"`
	Title       string    `json:"title"`
	DeletedAt   time.Time `json:"deleted_at"`
	// BookingsCount is every booking of the link (the uses_count rule).
	BookingsCount int `json:"bookings_count"`
	// AwaitingErasure is the number of bookings whose booker's name and email
	// are still stored (booker_erased_at IS NULL).
	AwaitingErasure int `json:"awaiting_erasure"`
	// LastBookingEndAt is the latest end_time of any booking; nil without one.
	LastBookingEndAt *time.Time `json:"last_booking_end_at"`
	// ErasedAt is the latest booker_erased_at; nil when none was erased.
	ErasedAt *time.Time `json:"erased_at"`
}

// ArchivedBooking is one booking of an archived link, with the instant its
// booker's name and email were erased (nil before). An erased booking stores
// empty strings in both, which the API never puts on the wire (contract §2.2).
type ArchivedBooking struct {
	Booking
	BookerErasedAt *time.Time `json:"booker_erased_at"`
}

const archivedLinkColumns = `
	l.id, l.owner_user_id, l.slug, l.title, l.deleted_at,
	(SELECT COUNT(*) FROM bookings b WHERE b.link_id = l.id),
	(SELECT COUNT(*) FROM bookings b WHERE b.link_id = l.id AND b.booker_erased_at IS NULL),
	(SELECT MAX(b.end_time) FROM bookings b WHERE b.link_id = l.id),
	(SELECT MAX(b.booker_erased_at) FROM bookings b WHERE b.link_id = l.id)`

// ListArchivedSchedulingLinks returns ownerID's deleted links, most recently
// deleted first. It is filtered on owner, so co-hosts and invitees never see
// another owner's archived link (R-1, AC-23).
func ListArchivedSchedulingLinks(db DBTX, ownerID uuid.UUID) ([]*ArchivedLink, error) {
	rows, err := db.QueryContext(context.Background(), `
		SELECT `+archivedLinkColumns+`
		FROM scheduling_links l
		WHERE l.owner_user_id = $1 AND l.deleted_at IS NOT NULL
		ORDER BY l.deleted_at DESC, l.id
	`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*ArchivedLink, 0)
	for rows.Next() {
		a, err := scanArchivedLink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetArchivedSchedulingLink returns link id when it is deleted AND owned by
// ownerID, in one lookup; nil otherwise. An id that never existed, a live or
// paused link (even the caller's own) and another owner's deleted link are
// therefore the same nil (contract §2.1, AC-6a, AC-23).
func GetArchivedSchedulingLink(db DBTX, ownerID, id uuid.UUID) (*ArchivedLink, error) {
	row := db.QueryRowContext(context.Background(), `
		SELECT `+archivedLinkColumns+`
		FROM scheduling_links l
		WHERE l.id = $1 AND l.owner_user_id = $2 AND l.deleted_at IS NOT NULL
	`, id, ownerID)
	a, err := scanArchivedLink(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return a, err
}

// GetArchivedBookings returns every booking of linkID, the future and
// cancelled ones included, in the live bookings read's order (AC-22). The
// caller has already established through GetArchivedSchedulingLink that the
// link is the caller's and deleted.
func GetArchivedBookings(db DBTX, linkID uuid.UUID) ([]*ArchivedBooking, error) {
	rows, err := db.QueryContext(context.Background(), `
		SELECT id, link_id, booker_name, booker_email, start_time, end_time, status, notes, created_at,
		       booker_erased_at
		FROM bookings WHERE link_id = $1 ORDER BY start_time DESC
	`, linkID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*ArchivedBooking, 0)
	for rows.Next() {
		var b ArchivedBooking
		var erasedAt sql.NullTime
		if err := rows.Scan(
			&b.ID, &b.LinkID, &b.BookerName, &b.BookerEmail,
			&b.StartTime, &b.EndTime, &b.Status, &b.Notes, &b.CreatedAt, &erasedAt,
		); err != nil {
			return nil, err
		}
		if erasedAt.Valid {
			t := erasedAt.Time
			b.BookerErasedAt = &t
		}
		out = append(out, &b)
	}
	return out, rows.Err()
}

func scanArchivedLink(row rowScanner) (*ArchivedLink, error) {
	var a ArchivedLink
	var lastEnd, erasedAt sql.NullTime
	if err := row.Scan(&a.ID, &a.OwnerUserID, &a.Slug, &a.Title, &a.DeletedAt,
		&a.BookingsCount, &a.AwaitingErasure, &lastEnd, &erasedAt); err != nil {
		return nil, err
	}
	if lastEnd.Valid {
		t := lastEnd.Time
		a.LastBookingEndAt = &t
	}
	if erasedAt.Valid {
		t := erasedAt.Time
		a.ErasedAt = &t
	}
	return &a, nil
}

// --- PAC-112: the co-host list on create and PATCH --------------------------

// ECMASpace reports whether r is in ECMA-262's \s set (WhiteSpace and
// LineTerminator), the set the contract's `pattern: '\S'` uses. Neither Go's
// unicode.IsSpace (it adds U+0085 and lacks U+FEFF) nor regexp's ASCII-only \s
// is that set. Moved here from api/handlers_credentials.go (PAC-67, API-052)
// unchanged, so the co-host list (PAC-112 contract md §3.4) applies the same
// test.
func ECMASpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', '\u00a0', '\u1680', '\u2028', '\u2029', '\u202f', '\u205f', '\u3000', '\ufeff':
		return true
	}
	return unicode.Is(unicode.Zs, r)
}

// BlankECMA reports whether s has no character outside ECMA-262 \s: the
// schema's `pattern: '\S'` fails on exactly these strings.
func BlankECMA(s string) bool {
	return strings.IndexFunc(s, func(r rune) bool { return !ECMASpace(r) }) < 0
}

// TrimECMASpace removes leading and trailing ECMA-262 \s characters.
func TrimECMASpace(s string) string {
	return strings.TrimFunc(s, ECMASpace)
}

// Co-host outcomes (PAC-112 contract md §2.2), as on the wire.
const (
	CoHostOwnAddress  = "own_address"
	CoHostAlreadyHost = "already_host"
	CoHostInvited     = "invited"
	CoHostNotAdded    = "not_added"
)

// CoHostListLimit is the length above which a co-host list may only keep or
// remove hosts (PAC-112 spec D-8, contract md §3.3).
const CoHostListLimit = 5

// ErrCoHostListTooLong is a list longer than CoHostListLimit with an entry
// that reaches no current host of the link, decided inside the write's
// transaction; the caller answers 422 and nothing is written.
var ErrCoHostListTooLong = errors.New("co-host list longer than 5 names someone who is not a current host")

// CoHostOutcome is the outcome of one request entry; Email is the entry
// trimmed of ECMA-262 \s, letter case kept.
//
// The JSON tags of CoHostOutcome, RemovedCoHost and CoHostChanges are the
// storage-owned encoding a keyed create stores for its replay (PAC-119
// contract md §3.1, challenge m-3). Their names equal the wire names of
// co_host_changes, but the wire is always written by the api package's DTO,
// never by marshalling these structs into a response.
type CoHostOutcome struct {
	Email   string `json:"email"`
	Outcome string `json:"outcome"`
}

// RemovedCoHost is a non-owner host a save removed, with the status it had.
type RemovedCoHost struct {
	UserID uuid.UUID `json:"user_id"`
	Email  string    `json:"email"`
	Status string    `json:"status"`
}

// CoHostChanges is what one save did to a link's co-hosts (contract md §2.1).
type CoHostChanges struct {
	Addresses     []CoHostOutcome `json:"addresses"`
	Removed       []RemovedCoHost `json:"removed"`
	InvitedCount  int             `json:"invited_count"`
	RemovedCount  int             `json:"removed_count"`
	NotAddedCount int             `json:"not_added_count"`
}

// linkHostEntry is one row of a link's host set as read inside a write.
type linkHostEntry struct {
	status string
	email  string
}

// readLinkHostSet reads every host row of linkID with its account's stored
// address, on the write's transaction (contract md §2.2's H).
func readLinkHostSet(q Querier, linkID uuid.UUID) (map[uuid.UUID]linkHostEntry, error) {
	rows, err := q.QueryContext(context.Background(), `
		SELECT h.user_id, h.status, u.email
		FROM scheduling_link_hosts h JOIN users u ON u.id = h.user_id
		WHERE h.link_id = $1`, linkID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uuid.UUID]linkHostEntry{}
	for rows.Next() {
		var id uuid.UUID
		var e linkHostEntry
		if err := rows.Scan(&id, &e.status, &e.email); err != nil {
			return nil, err
		}
		out[id] = e
	}
	return out, rows.Err()
}

// lowerAll returns PostgreSQL's lower() of each value, in order: the same
// lowercasing the carriers lookup applies, so two entries share one lookup
// only when the database would resolve them identically (contract md §3.3
// rule 1; a Go-side ToLower is not that comparison for non-ASCII text).
func lowerAll(q Querier, values []string) ([]string, error) {
	if len(values) == 0 {
		return []string{}, nil
	}
	rows, err := q.QueryContext(context.Background(), `
		SELECT lower(v) FROM unnest($1::text[]) WITH ORDINALITY AS t(v, n) ORDER BY n`, pq.Array(values))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]string, 0, len(values))
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) != len(values) {
		return nil, fmt.Errorf("lowerAll: %d values, %d results", len(values), len(out))
	}
	return out, nil
}

// coHostLookupResult is one distinct address's lookup and outcome.
type coHostLookupResult struct {
	carriers []Carrier
	outcome  string
	holder   uuid.UUID // the invitee when outcome is invited
}

// setCoHostList writes entries as linkID's complete co-host list on q (the
// create's or the PATCH's transaction, after the link's lock) and reports
// what it did (PAC-112 contract md §2, §3.2, §3.3). It reads the host set H
// first; each distinct address (identical after the ECMA trim and lower())
// is looked up once, in request order, and a list longer than
// CoHostListLimit is refused with ErrCoHostListTooLong at the first address
// that reaches no current host, before any further lookup and before any
// write. Then every non-owner host not carried by an entry (the PAC-71 keep
// rule) is removed and every new domain-confirmed holder is invited
// (pending; an existing host keeps its status, PAC-61). The owner's row is
// never written. Any error leaves the caller to roll back.
func setCoHostList(q Querier, linkID, ownerID uuid.UUID, entries []string) (*CoHostChanges, error) {
	hosts, err := readLinkHostSet(q, linkID)
	if err != nil {
		return nil, fmt.Errorf("co-host host set: %w", err)
	}
	trimmed := make([]string, len(entries))
	for i, e := range entries {
		trimmed[i] = TrimECMASpace(e)
	}
	keys, err := lowerAll(q, trimmed)
	if err != nil {
		return nil, fmt.Errorf("co-host lookup: %w", err)
	}
	limited := len(entries) > CoHostListLimit
	results := make(map[string]*coHostLookupResult, len(keys))
	order := make([]string, 0, len(keys))
	for i, key := range keys {
		if _, seen := results[key]; seen {
			continue
		}
		r, err := lookupCoHost(q, trimmed[i], ownerID, hosts)
		if err != nil {
			return nil, err
		}
		if limited && !r.reaches {
			return nil, ErrCoHostListTooLong
		}
		results[key] = &r.coHostLookupResult
		order = append(order, key)
	}

	changes := &CoHostChanges{Addresses: make([]CoHostOutcome, 0, len(entries)), Removed: make([]RemovedCoHost, 0)}
	// Keep set: every account carrying a listed address (PAC-71 AC-43).
	keep := make([]string, 0, len(order))
	for _, key := range order {
		for _, c := range results[key].carriers {
			keep = append(keep, c.ID.String())
		}
	}
	if changes.Removed, err = removeCoHostsExcept(q, linkID, ownerID, keep, hosts); err != nil {
		return nil, err
	}

	invited := map[uuid.UUID]bool{}
	for _, key := range order {
		r := results[key]
		if r.outcome != CoHostInvited || invited[r.holder] {
			continue
		}
		if err := coHostInvite(q, linkID, r.holder); err != nil {
			return nil, err
		}
		invited[r.holder] = true
	}

	notAdded := map[string]bool{}
	for i, key := range keys {
		r := results[key]
		changes.Addresses = append(changes.Addresses, CoHostOutcome{Email: trimmed[i], Outcome: r.outcome})
		if r.outcome == CoHostNotAdded {
			notAdded[key] = true
		}
	}
	changes.InvitedCount = len(invited)
	changes.RemovedCount = len(changes.Removed)
	changes.NotAddedCount = len(notAdded)
	return changes, nil
}

// coHostLookupReach is a lookupCoHost result: the outcome, and whether some
// carrier of the address is a current host (the owner included).
type coHostLookupReach struct {
	coHostLookupResult
	reaches bool
}

// lookupCoHost is one lookup of the co-host list (one read of an address's
// carriers, contract md §3.3) and the outcome it gives, in the binding order
// of contract md §2.2. hosts is H, read under the link's lock.
func lookupCoHost(q Querier, address string, ownerID uuid.UUID, hosts map[uuid.UUID]linkHostEntry) (coHostLookupReach, error) {
	out := coHostLookupReach{coHostLookupResult: coHostLookupResult{outcome: CoHostNotAdded}}
	if address == "" {
		return out, nil
	}
	if err := fireSeamErr(&coHostLookup, address); err != nil {
		return out, fmt.Errorf("co-host lookup: %w", err)
	}
	carriers, err := carriersOfTrimmed(q, address)
	if err != nil {
		return out, fmt.Errorf("co-host lookup: %w", err)
	}
	out.carriers = carriers
	nonOwnerHost := false
	for _, c := range carriers {
		_, isHost := hosts[c.ID]
		if isHost || c.ID == ownerID {
			out.reaches = true
			if c.ID != ownerID {
				nonOwnerHost = true
			}
		}
	}
	confirmed := len(carriers) == 1 && carriers[0].Standing == AddressDomainConfirmed
	switch {
	case confirmed && carriers[0].ID == ownerID:
		out.outcome = CoHostOwnAddress
	case nonOwnerHost:
		out.outcome = CoHostAlreadyHost
	case confirmed:
		out.outcome, out.holder = CoHostInvited, carriers[0].ID
	}
	return out, nil
}

// removeCoHostsExcept deletes every non-owner host of linkID whose account is
// not in keep and returns them with the status they had and the address read
// in H. A removed host without an address in H fails the save: the answer's
// removed list is never degraded (contract md §2.1).
func removeCoHostsExcept(q Querier, linkID, ownerID uuid.UUID, keep []string, hosts map[uuid.UUID]linkHostEntry) ([]RemovedCoHost, error) {
	rows, err := q.QueryContext(context.Background(), `
		DELETE FROM scheduling_link_hosts
		WHERE link_id = $1 AND user_id <> $2 AND NOT (user_id::text = ANY($3::text[]))
		RETURNING user_id, status`, linkID, ownerID, pq.Array(keep))
	if err != nil {
		return nil, fmt.Errorf("co-host removal: %w", err)
	}
	defer rows.Close()
	removed := make([]RemovedCoHost, 0)
	for rows.Next() {
		var rh RemovedCoHost
		if err := rows.Scan(&rh.UserID, &rh.Status); err != nil {
			return nil, fmt.Errorf("co-host removal: %w", err)
		}
		h, ok := hosts[rh.UserID]
		if !ok {
			return nil, fmt.Errorf("co-host removal: no address read for %s", rh.UserID)
		}
		rh.Email = h.email
		removed = append(removed, rh)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("co-host removal: %w", err)
	}
	return removed, nil
}

// coHostInvite writes one new pending co-host row. The invitee was not in the
// host set read under the link's lock, so the row must be new; a conflict
// means that read was wrong and fails the save rather than misreport it.
func coHostInvite(q Querier, linkID, userID uuid.UUID) error {
	if fn, ok := coHostWrite.get(); ok {
		if err := fn(linkID, userID); err != nil {
			return fmt.Errorf("co-host invite: %w", err)
		}
	}
	_, created, err := InviteLinkHost(q, linkID, userID)
	if err != nil {
		return fmt.Errorf("co-host invite: %w", err)
	}
	if !created {
		return fmt.Errorf("co-host invite: %s became a host during the save", userID)
	}
	return nil
}

// createSchedulingLinkWithCoHosts is a create as one unit (PAC-112 contract
// md §3.1, PAC-119 contract md §4.2): the link, then, for a keyed create, the
// key row (claimed before any host or invitation work, so a concurrent
// duplicate stops there), the owner's accepted host row by a plain insert
// (never the upsert that could demote the owner, API-048), and, when
// coHostEmails is not nil, the co-host list with its outcomes, whose
// storage-owned encoding a keyed create then stores on its key row. A unique
// violation on the link's slug or on the (owner, key) pair is a
// *LinkCreateConflictError. Any error makes the caller roll everything back,
// so a failed or refused create never stores a key.
func createSchedulingLinkWithCoHosts(q Querier, l *SchedulingLink, coHostEmails []string, key string, digest []byte) (*SchedulingLink, *CoHostChanges, error) {
	created, err := CreateSchedulingLink(q, l)
	if isUniqueViolationOn(err, LinkSlugConstraint) {
		return nil, nil, &LinkCreateConflictError{Constraint: LinkSlugConstraint, Err: err}
	}
	if err != nil {
		return nil, nil, err
	}
	if key != "" {
		if len(digest) != sha256.Size {
			return nil, nil, fmt.Errorf("link create key: digest of %d bytes", len(digest))
		}
		_, err := q.ExecContext(context.Background(), `
			INSERT INTO scheduling_link_create_keys (owner_user_id, idempotency_key, link_id, request_sha256)
			VALUES ($1, $2, $3, $4)`, created.OwnerUserID, key, created.ID, digest)
		if isUniqueViolationOn(err, LinkCreateKeyConstraint) {
			return nil, nil, &LinkCreateConflictError{Constraint: LinkCreateKeyConstraint, Err: err}
		}
		if err != nil {
			return nil, nil, fmt.Errorf("link create key: %w", err)
		}
	}
	if err := fireSeamErr(&createOwnerHost, created.ID); err != nil {
		return nil, nil, fmt.Errorf("owner host: %w", err)
	}
	if _, err := q.ExecContext(context.Background(), `
		INSERT INTO scheduling_link_hosts (link_id, user_id, status) VALUES ($1, $2, 'accepted')`,
		created.ID, created.OwnerUserID); err != nil {
		return nil, nil, fmt.Errorf("owner host: %w", err)
	}
	var changes *CoHostChanges
	if coHostEmails != nil {
		if changes, err = setCoHostList(q, created.ID, created.OwnerUserID, coHostEmails); err != nil {
			return nil, nil, err
		}
		if key != "" {
			encoded, err := json.Marshal(changes)
			if err != nil {
				return nil, nil, fmt.Errorf("link create key outcomes: %w", err)
			}
			if _, err := q.ExecContext(context.Background(), `
				UPDATE scheduling_link_create_keys SET co_host_changes = $3
				WHERE owner_user_id = $1 AND idempotency_key = $2`,
				created.OwnerUserID, key, string(encoded)); err != nil {
				return nil, nil, fmt.Errorf("link create key outcomes: %w", err)
			}
		}
	}
	if err := fireSeamErr(&beforeLinkCreateCommit, created.ID); err != nil {
		return nil, nil, fmt.Errorf("before link create commit: %w", err)
	}
	return created, changes, nil
}

// --- PAC-132: the saved link's roster, read inside the write ----------------

// LinkRosterHost is one host of a link with its user record: what the create
// (201) and PATCH (200) answers show in `hosts` (PAC-132, SF-029).
type LinkRosterHost struct {
	UserID    uuid.UUID
	Status    string
	Email     string
	Name      string
	AvatarURL string
}

// ErrHostUserMissing is a host row whose user record could not be found.
var ErrHostUserMissing = errors.New("host user record missing")

// readLinkRoster reads linkID's host rows, in GetLinkHosts' order, and each
// host's user record on q, strictly: a failed read, or a host with no user
// record, is an error and never a shorter roster or a blank email. The create
// and PATCH transactions call it after their last write, so its error rolls
// the whole write back and the 500 leaves nothing behind (PAC-132, SF-029).
func readLinkRoster(ctx context.Context, q Querier, linkID uuid.UUID) ([]*LinkRosterHost, error) {
	if err := fireSeamErr(&linkRosterHosts, linkID); err != nil {
		return nil, fmt.Errorf("link roster hosts: %w", err)
	}
	rows, err := q.QueryContext(ctx, `
		SELECT user_id, status FROM scheduling_link_hosts WHERE link_id = $1 ORDER BY invited_at`, linkID)
	if err != nil {
		return nil, fmt.Errorf("link roster hosts: %w", err)
	}
	var roster []*LinkRosterHost
	for rows.Next() {
		var h LinkRosterHost
		if err := rows.Scan(&h.UserID, &h.Status); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("link roster hosts: %w", err)
		}
		roster = append(roster, &h)
	}
	// Closed before the user reads: the transaction's connection serves one
	// statement at a time.
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("link roster hosts: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("link roster hosts: %w", err)
	}
	for _, h := range roster {
		if err := fireSeamErr2(&linkRosterUser, linkID, h.UserID); err != nil {
			return nil, fmt.Errorf("link roster user: %w", err)
		}
		err := q.QueryRowContext(ctx, `SELECT email, name, avatar_url FROM users WHERE id = $1`, h.UserID).
			Scan(&h.Email, &h.Name, &h.AvatarURL)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrHostUserMissing
		}
		if err != nil {
			return nil, fmt.Errorf("link roster user: %w", err)
		}
	}
	return roster, nil
}

// --- PAC-119: keyed link create (contracts/features/PAC-119.md) -------------

// The two unique constraints a create can violate (contract md §4.2, §3.1).
const (
	// LinkSlugConstraint is scheduling_links' unique slug (migration 009).
	LinkSlugConstraint = "scheduling_links_slug_key"
	// LinkCreateKeyConstraint is the key table's primary key (owner, key).
	LinkCreateKeyConstraint = "scheduling_link_create_keys_owner_key"
)

// LinkCreateKeyWindow is how long a stored key replays (spec §3.6): from 24 h
// on, by the database clock, a key gets the expired refusal for ever.
const LinkCreateKeyWindow = 24 * time.Hour

// ErrLinkCreateConflict is the sentinel every *LinkCreateConflictError
// matches with errors.Is: the create's link insert or key insert met a
// unique violation and the create was rolled back.
var ErrLinkCreateConflict = errors.New("link create unique violation")

// LinkCreateConflictError is a create rolled back on a unique violation of
// Constraint (LinkSlugConstraint or LinkCreateKeyConstraint). A keyed caller
// then resolves its key again in a new guarded transaction (R3); a keyless
// slug violation stays the 500 (spec §5).
type LinkCreateConflictError struct {
	Constraint string
	Err        error
}

func (e *LinkCreateConflictError) Error() string {
	return "link create unique violation on " + e.Constraint
}

// Is makes every LinkCreateConflictError match ErrLinkCreateConflict.
func (e *LinkCreateConflictError) Is(target error) bool { return target == ErrLinkCreateConflict }

func (e *LinkCreateConflictError) Unwrap() error { return e.Err }

// linkCreateCanonicalV1 is the canonical form v1 of a link create (contract
// md §2.3), marshalled by encoding/json in this field order. Every value is
// the one the save would store, plus the normalised co-host list.
type linkCreateCanonicalV1 struct {
	V                  int      `json:"v"`
	Title              string   `json:"title"`
	Slug               string   `json:"slug"`
	DurationOptions    []int    `json:"duration_options"`
	DaysOfWeek         []int    `json:"days_of_week"`
	WindowStart        string   `json:"window_start_time"`
	WindowEnd          string   `json:"window_end_time"`
	BufferBefore       int      `json:"buffer_before"`
	BufferAfter        int      `json:"buffer_after"`
	MinNoticeMinutes   int      `json:"min_notice_minutes"`
	UsageType          string   `json:"usage_type"`
	MaxUses            *int     `json:"max_uses"`
	BookingHorizonDays int      `json:"booking_horizon_days"`
	RefusalMessage     string   `json:"refusal_message"`
	CoHostEmails       []string `json:"co_host_emails"`
}

// linkCreateCanonical returns the canonical bytes v1 of l, a link as the
// create's validation left it (title trimmed, slug normalised and not yet
// generated, aliases and defaults resolved, windows HH:MM, usage lowered,
// max_uses cleared unless recurring), with lowered the co-host entries
// already trimmed and lowered by lowerAll; nil means the request carried no
// list. The list is deduplicated and sorted ascending by bytes here; [] stays
// [] and differs from no list.
func linkCreateCanonical(l *SchedulingLink, lowered []string) []byte {
	c := linkCreateCanonicalV1{
		V: 1, Title: l.Title, Slug: l.Slug, DurationOptions: l.DurationOptions, DaysOfWeek: l.DaysOfWeek,
		WindowStart: l.WindowStart, WindowEnd: l.WindowEnd, BufferBefore: l.BufferBefore,
		BufferAfter: l.BufferAfter, MinNoticeMinutes: l.MinNoticeMinutes, UsageType: l.UsageType,
		MaxUses: l.MaxUses, BookingHorizonDays: l.BookingHorizonDays, RefusalMessage: l.RefusalMessage,
	}
	if c.UsageType == "" {
		c.UsageType = "reusable"
	}
	if c.BookingHorizonDays == 0 {
		c.BookingHorizonDays = DefaultBookingHorizonDays
	}
	if c.RefusalMessage == "" {
		c.RefusalMessage = RefusalGeneric
	}
	if lowered != nil {
		set := make(map[string]struct{}, len(lowered))
		c.CoHostEmails = make([]string, 0, len(lowered))
		for _, v := range lowered {
			if _, dup := set[v]; dup {
				continue
			}
			set[v] = struct{}{}
			c.CoHostEmails = append(c.CoHostEmails, v)
		}
		sort.Strings(c.CoHostEmails)
	}
	b, err := json.Marshal(c)
	if err != nil {
		// Every field is a string, an int, a slice of them or a nil-able
		// pointer to an int: Marshal cannot fail.
		panic(fmt.Sprintf("linkCreateCanonical: %v", err))
	}
	return b
}

// linkCreateDigest is SHA-256 of linkCreateCanonical, on q (the resolution
// transaction, for lowerAll).
func linkCreateDigest(q Querier, l *SchedulingLink, coHostEmails []string) ([]byte, error) {
	var lowered []string
	if coHostEmails != nil {
		trimmed := make([]string, len(coHostEmails))
		for i, e := range coHostEmails {
			trimmed[i] = TrimECMASpace(e)
		}
		var err error
		if lowered, err = lowerAll(q, trimmed); err != nil {
			return nil, fmt.Errorf("link create digest: %w", err)
		}
	}
	sum := sha256.Sum256(linkCreateCanonical(l, lowered))
	return sum[:], nil
}

// LinkCreateAttempt is one keyed create request as the resolution needs it.
// Digest is nil until the first resolution (R1) computes it from Link and
// CoHostEmails; R2, R3 and the create transaction then reuse it.
type LinkCreateAttempt struct {
	Key string
	// Link is the validated link before slug generation.
	Link *SchedulingLink
	// CoHostEmails is the request's list as sent; nil is no list.
	CoHostEmails []string
	Digest       []byte
}

// LinkCreateKeyOutcome is what a resolution decided (contract md §1.2).
type LinkCreateKeyOutcome int

const (
	// LinkCreateKeyNotStored: nothing is stored under the key.
	LinkCreateKeyNotStored LinkCreateKeyOutcome = iota
	// LinkCreateKeyReplay: row 3, the same request under 24 h.
	LinkCreateKeyReplay
	// LinkCreateKeyDeleted: row 1, the key's link is deleted.
	LinkCreateKeyDeleted
	// LinkCreateKeyExpired: row 2, the original save is 24 h old or more.
	LinkCreateKeyExpired
	// LinkCreateKeyReused: row 4, another request (or a NULL digest).
	LinkCreateKeyReused
)

// LinkCreateKeyResolution is a resolution's answer. Link and Changes are set
// for a replay (Changes nil when the original carried no list); LinkID is set
// for the replay and for the expired and key-reused refusals.
type LinkCreateKeyResolution struct {
	Outcome LinkCreateKeyOutcome
	Link    *SchedulingLink
	Changes *CoHostChanges
	LinkID  uuid.UUID
}

// storedLinkCreateKey is the key read's row (contract md §4.1 step 4).
type storedLinkCreateKey struct {
	digest  []byte
	changes []byte
	deleted bool
	expired bool
	link    SchedulingLink
}

// readLinkCreateKey is the key read: the key row of (owner, key) joined to
// its link, in one MVCC statement that takes no row lock. nil when no row.
func readLinkCreateKey(ctx context.Context, q Querier, ownerID uuid.UUID, key string) (*storedLinkCreateKey, error) {
	var s storedLinkCreateKey
	var durOpts, daysOfWeek pq.Int64Array
	var changes sql.NullString
	err := q.QueryRowContext(ctx, `
		SELECT k.request_sha256, k.co_host_changes, l.deleted_at IS NOT NULL,
		       now() - k.created_at >= make_interval(secs => $3),
		       l.id, l.owner_user_id, l.slug, l.title, l.duration_options, l.days_of_week,
		       l.window_start_time, l.window_end_time, l.buffer_before, l.buffer_after,
		       l.min_notice_minutes, l.usage_type, l.max_uses,
		       (SELECT COUNT(*) FROM bookings b WHERE b.link_id = l.id),
		       l.active, l.created_at, l.booking_horizon_days, l.refusal_message
		FROM scheduling_link_create_keys k
		JOIN scheduling_links l ON l.id = k.link_id
		WHERE k.owner_user_id = $1 AND k.idempotency_key = $2`,
		ownerID, key, LinkCreateKeyWindow.Seconds()).Scan(
		&s.digest, &changes, &s.deleted, &s.expired,
		&s.link.ID, &s.link.OwnerUserID, &s.link.Slug, &s.link.Title, &durOpts, &daysOfWeek,
		&s.link.WindowStart, &s.link.WindowEnd, &s.link.BufferBefore, &s.link.BufferAfter,
		&s.link.MinNoticeMinutes, &s.link.UsageType, &s.link.MaxUses, &s.link.UsesCount,
		&s.link.Active, &s.link.CreatedAt, &s.link.BookingHorizonDays, &s.link.RefusalMessage)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("link create key read: %w", err)
	}
	if changes.Valid {
		s.changes = []byte(changes.String)
	}
	s.link.DurationOptions = int64SliceToInt(durOpts)
	s.link.DaysOfWeek = int64SliceToInt(daysOfWeek)
	return &s, nil
}

// decideLinkCreateKey applies contract md §1.2's rules, first match wins:
// deleted, expired, same digest (replay), anything else (key reused, a NULL
// digest included).
func decideLinkCreateKey(s *storedLinkCreateKey, digest []byte) (*LinkCreateKeyResolution, error) {
	switch {
	case s == nil:
		return &LinkCreateKeyResolution{Outcome: LinkCreateKeyNotStored}, nil
	case s.deleted:
		return &LinkCreateKeyResolution{Outcome: LinkCreateKeyDeleted}, nil
	case s.expired:
		return &LinkCreateKeyResolution{Outcome: LinkCreateKeyExpired, LinkID: s.link.ID}, nil
	case s.digest != nil && bytes.Equal(s.digest, digest):
		res := &LinkCreateKeyResolution{Outcome: LinkCreateKeyReplay, Link: &s.link, LinkID: s.link.ID}
		if s.changes != nil {
			var c CoHostChanges
			if err := json.Unmarshal(s.changes, &c); err != nil {
				return nil, fmt.Errorf("link create key outcomes: %w", err)
			}
			res.Changes = &c
		}
		return res, nil
	}
	return &LinkCreateKeyResolution{Outcome: LinkCreateKeyReused, LinkID: s.link.ID}, nil
}

// ResolveLinkCreateKeyCtx is the resolution transaction R (contract md §4.1;
// R1, R2 and R3 are this function): one READ COMMITTED transaction that,
// under the request's session guard, fires the PAC-71 mid-request seam,
// re-checks the generation, computes the attempt's digest when it has none
// yet (R1), reads the key row joined to its link, fires
// afterLinkCreateKeyRead, re-checks the generation again and commits; only
// then is the answer decided from the row, by contract md §1.2. A stale
// session is ErrStaleSession with nothing of the row. When nothing is stored,
// afterLinkCreateKeyResolved fires after the commit, before the return.
// Without a session guard (no request session) the re-checks are skipped.
func ResolveLinkCreateKeyCtx(ctx context.Context, db DBTX, ownerID uuid.UUID, a *LinkCreateAttempt) (*LinkCreateKeyResolution, error) {
	g := SessionGuardFrom(ctx)
	if g != nil {
		fireGuardedCallHook(ctx)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		if g != nil {
			g.MarkUnavailable()
			return nil, fmt.Errorf("%w: %w", ErrGenerationUnavailable, err)
		}
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if g != nil {
		if err := recheck(ctx, tx, g.UserID, g.Gen, g); err != nil {
			return nil, err
		}
	}
	if a.Digest == nil {
		if a.Digest, err = linkCreateDigest(tx, a.Link, a.CoHostEmails); err != nil {
			return nil, err
		}
	}
	stored, err := readLinkCreateKey(ctx, tx, ownerID, a.Key)
	if err != nil {
		return nil, err
	}
	if fn, ok := afterLinkCreateKeyRead.get(); ok {
		fn(ctx, a.Key)
	}
	if g != nil {
		if err := recheck(ctx, tx, g.UserID, g.Gen, g); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	res, err := decideLinkCreateKey(stored, a.Digest)
	if err != nil {
		return nil, err
	}
	if res.Outcome == LinkCreateKeyNotStored {
		if fn, ok := afterLinkCreateKeyResolved.get(); ok {
			fn(ctx, a.Key)
		}
	}
	return res, nil
}

// ClearExpiredLinkCreateKeyData is the cleanup of contract md §3.3: in its
// own transaction, under the link-lock bound, it clears the request digest
// and the stored co-host outcomes of every key whose original save is 24 h
// old or more, and returns how many rows it cleared. Owner, key, link and
// time stay for ever. Concurrent runs are idempotent.
func ClearExpiredLinkCreateKeyData(ctx context.Context, db DBTX) (int64, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := setLinkLockTimeout(tx); err != nil {
		return 0, err
	}
	res, err := tx.ExecContext(ctx, `
		UPDATE scheduling_link_create_keys SET request_sha256 = NULL, co_host_changes = NULL
		WHERE request_sha256 IS NOT NULL AND created_at <= now() - make_interval(secs => $1)`,
		LinkCreateKeyWindow.Seconds())
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return n, nil
}
