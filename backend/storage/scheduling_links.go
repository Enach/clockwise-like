package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

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

// AfterLinkPatchUpdate is a test seam (PAC-101 contract §5): it runs inside a
// PATCH's transaction after the link UPDATE matched and before the co-host
// steps. A non-nil error rolls the whole PATCH back. Nil in production.
var AfterLinkPatchUpdate func(linkID uuid.UUID) error

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

// AfterBookingLocks is a test seam (PAC-82 contract section 6): it runs inside
// the save after every advisory key is held and before the re-checks. Nil in
// production.
var AfterBookingLocks func(b *Booking)

// AfterBookingInsert is a test seam (PAC-82 contract section 6): it runs
// after the insert and before COMMIT; a non-nil error rolls the save back and
// is returned. Nil in production.
var AfterBookingInsert func(b *Booking) error

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
	if f := AfterBookingLocks; f != nil {
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
	if f := AfterBookingInsert; f != nil {
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

// inviteLinkHostLocked is InviteLinkHost behind the link lock: link gone is
// decided by the lock step only, never by the insert's zero rows, which mean
// "already a host" (PAC-61, contract §3.2).
func inviteLinkHostLocked(q Querier, linkID, userID uuid.UUID) (*LinkHost, bool, error) {
	if err := lockLiveLinkForShare(q, linkID); err != nil {
		return nil, false, err
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
// set by difference (PAC-61) on the same transaction. A link deleted before
// the UPDATE is ErrLinkGone; any error makes the caller roll everything back.
func updateSchedulingLinkAndCoHosts(q Querier, id uuid.UUID, l *SchedulingLink, ownerID uuid.UUID, coHostEmails []string) (*SchedulingLink, error) {
	result, err := UpdateSchedulingLink(q, id, l)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, ErrLinkGone
	}
	if f := AfterLinkPatchUpdate; f != nil {
		if err := f(id); err != nil {
			return nil, err
		}
	}
	if coHostEmails == nil {
		return result, nil
	}
	// PAC-71: the keep set is a KEEPING site (every account carrying a listed
	// address, any standing, ignoring letter case, AC-43); new entries are a
	// GRANTING site (only the one domain-confirmed holder).
	keep, err := CarrierIDs(q, coHostEmails)
	if err != nil {
		return nil, fmt.Errorf("co-host lookup: %w", err)
	}
	if err := RemoveLinkHostsExcept(q, id, ownerID, keep); err != nil {
		return nil, fmt.Errorf("co-host removal: %w", err)
	}
	for _, email := range coHostEmails {
		uid, ok, err := ConfirmedHolder(q, email)
		if err != nil {
			return nil, fmt.Errorf("co-host lookup: %w", err)
		}
		if !ok || uid == ownerID {
			continue
		}
		if _, _, err := InviteLinkHost(q, id, uid); err != nil {
			return nil, fmt.Errorf("co-host invite: %w", err)
		}
	}
	return result, nil
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
