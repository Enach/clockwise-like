package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// SchedulingLink represents a public booking page.
type SchedulingLink struct {
	ID              uuid.UUID `json:"id"`
	OwnerUserID     uuid.UUID `json:"owner_user_id"`
	Slug            string    `json:"slug"`
	Title           string    `json:"title"`
	DurationOptions []int     `json:"duration_options"`
	DaysOfWeek      []int     `json:"days_of_week"`
	WindowStart     string    `json:"window_start_time"`
	WindowEnd       string    `json:"window_end_time"`
	BufferBefore    int       `json:"buffer_before"`
	BufferAfter     int       `json:"buffer_after"`
	Active          bool      `json:"active"`
	CreatedAt       time.Time `json:"created_at"`
	Hosts           []*LinkHost `json:"hosts,omitempty"`
}

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

func CreateSchedulingLink(db *sql.DB, l *SchedulingLink) (*SchedulingLink, error) {
	row := db.QueryRowContext(context.Background(), `
		INSERT INTO scheduling_links
			(owner_user_id, slug, title, duration_options, days_of_week,
			 window_start_time, window_end_time, buffer_before, buffer_after)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		RETURNING id, owner_user_id, slug, title, duration_options, days_of_week,
			window_start_time, window_end_time, buffer_before, buffer_after, active, created_at
	`, l.OwnerUserID, l.Slug, l.Title,
		pq.Array(l.DurationOptions), pq.Array(l.DaysOfWeek),
		l.WindowStart, l.WindowEnd, l.BufferBefore, l.BufferAfter)
	return scanSchedulingLink(row)
}

func GetSchedulingLinkBySlug(db *sql.DB, slug string) (*SchedulingLink, error) {
	row := db.QueryRowContext(context.Background(), `
		SELECT id, owner_user_id, slug, title, duration_options, days_of_week,
			window_start_time, window_end_time, buffer_before, buffer_after, active, created_at
		FROM scheduling_links WHERE slug = $1 AND active = true
	`, slug)
	return scanSchedulingLink(row)
}

func GetSchedulingLinkByID(db *sql.DB, id uuid.UUID) (*SchedulingLink, error) {
	row := db.QueryRowContext(context.Background(), `
		SELECT id, owner_user_id, slug, title, duration_options, days_of_week,
			window_start_time, window_end_time, buffer_before, buffer_after, active, created_at
		FROM scheduling_links WHERE id = $1
	`, id)
	return scanSchedulingLink(row)
}

func ListSchedulingLinksByUser(db *sql.DB, userID uuid.UUID) ([]*SchedulingLink, error) {
	rows, err := db.QueryContext(context.Background(), `
		SELECT DISTINCT sl.id, sl.owner_user_id, sl.slug, sl.title,
			sl.duration_options, sl.days_of_week,
			sl.window_start_time, sl.window_end_time,
			sl.buffer_before, sl.buffer_after, sl.active, sl.created_at
		FROM scheduling_links sl
		LEFT JOIN scheduling_link_hosts slh ON slh.link_id = sl.id
		WHERE sl.owner_user_id = $1 OR (slh.user_id = $1 AND slh.status = 'accepted')
		ORDER BY sl.created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSchedulingLinks(rows)
}

func UpdateSchedulingLink(db *sql.DB, id uuid.UUID, l *SchedulingLink) (*SchedulingLink, error) {
	row := db.QueryRowContext(context.Background(), `
		UPDATE scheduling_links SET
			title = $2, duration_options = $3, days_of_week = $4,
			window_start_time = $5, window_end_time = $6,
			buffer_before = $7, buffer_after = $8, active = $9
		WHERE id = $1
		RETURNING id, owner_user_id, slug, title, duration_options, days_of_week,
			window_start_time, window_end_time, buffer_before, buffer_after, active, created_at
	`, id, l.Title, pq.Array(l.DurationOptions), pq.Array(l.DaysOfWeek),
		l.WindowStart, l.WindowEnd, l.BufferBefore, l.BufferAfter, l.Active)
	return scanSchedulingLink(row)
}

func DeleteSchedulingLink(db *sql.DB, id uuid.UUID) error {
	_, err := db.ExecContext(context.Background(),
		`UPDATE scheduling_links SET active = false WHERE id = $1`, id)
	return err
}

func scanSchedulingLink(row *sql.Row) (*SchedulingLink, error) {
	var l SchedulingLink
	err := row.Scan(
		&l.ID, &l.OwnerUserID, &l.Slug, &l.Title,
		pq.Array(&l.DurationOptions), pq.Array(&l.DaysOfWeek),
		&l.WindowStart, &l.WindowEnd, &l.BufferBefore, &l.BufferAfter,
		&l.Active, &l.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &l, err
}

func scanSchedulingLinks(rows *sql.Rows) ([]*SchedulingLink, error) {
	var out []*SchedulingLink
	for rows.Next() {
		var l SchedulingLink
		if err := rows.Scan(
			&l.ID, &l.OwnerUserID, &l.Slug, &l.Title,
			pq.Array(&l.DurationOptions), pq.Array(&l.DaysOfWeek),
			&l.WindowStart, &l.WindowEnd, &l.BufferBefore, &l.BufferAfter,
			&l.Active, &l.CreatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, &l)
	}
	return out, rows.Err()
}

// --- LinkHost CRUD ---------------------------------------------------------

func AddLinkHost(db *sql.DB, linkID, userID uuid.UUID, status string) (*LinkHost, error) {
	row := db.QueryRowContext(context.Background(), `
		INSERT INTO scheduling_link_hosts (link_id, user_id, status)
		VALUES ($1, $2, $3)
		ON CONFLICT (link_id, user_id) DO UPDATE SET status = EXCLUDED.status
		RETURNING id, link_id, user_id, status, invited_at, responded_at
	`, linkID, userID, status)
	return scanLinkHost(row)
}

func GetLinkHosts(db *sql.DB, linkID uuid.UUID) ([]*LinkHost, error) {
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

func GetAcceptedHosts(db *sql.DB, linkID uuid.UUID) ([]*LinkHost, error) {
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

func GetPendingInvitesForUser(db *sql.DB, userID uuid.UUID) ([]*LinkHost, error) {
	rows, err := db.QueryContext(context.Background(), `
		SELECT id, link_id, user_id, status, invited_at, responded_at
		FROM scheduling_link_hosts WHERE user_id = $1 AND status = 'pending'
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

func RespondToHostInvite(db *sql.DB, linkID, userID uuid.UUID, status string) error {
	_, err := db.ExecContext(context.Background(), `
		UPDATE scheduling_link_hosts
		SET status = $3, responded_at = NOW()
		WHERE link_id = $1 AND user_id = $2
	`, linkID, userID, status)
	return err
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

func CreateBooking(db *sql.DB, b *Booking) (*Booking, error) {
	row := db.QueryRowContext(context.Background(), `
		INSERT INTO bookings (link_id, booker_name, booker_email, start_time, end_time, notes)
		VALUES ($1,$2,$3,$4,$5,$6)
		RETURNING id, link_id, booker_name, booker_email, start_time, end_time, status, notes, created_at
	`, b.LinkID, b.BookerName, b.BookerEmail, b.StartTime, b.EndTime, b.Notes)
	return scanBooking(row)
}

func GetBookingsByLink(db *sql.DB, linkID uuid.UUID) ([]*Booking, error) {
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

// GetConfirmedBookingsForUser returns all confirmed booking time ranges for a user
// across any scheduling link they host.
func GetConfirmedBookingsForUser(db *sql.DB, userID uuid.UUID, start, end time.Time) ([]time.Time, error) {
	rows, err := db.QueryContext(context.Background(), `
		SELECT b.start_time, b.end_time
		FROM bookings b
		JOIN booking_events be ON be.booking_id = b.id
		WHERE be.user_id = $1
		  AND b.status = 'confirmed'
		  AND b.start_time < $3
		  AND b.end_time   > $2
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

func SaveBookingEvent(db *sql.DB, bookingID, userID uuid.UUID, calEventID string) error {
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO booking_events (booking_id, user_id, calendar_event_id)
		VALUES ($1, $2, $3)
	`, bookingID, userID, calEventID)
	return err
}

// --- Slug helpers -----------------------------------------------------------

func SlugExists(db *sql.DB, slug string) (bool, error) {
	var exists bool
	err := db.QueryRowContext(context.Background(),
		`SELECT EXISTS(SELECT 1 FROM scheduling_links WHERE slug = $1)`, slug).Scan(&exists)
	return exists, err
}
