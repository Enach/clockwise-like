package storage

// PAC-101 (contracts/features/PAC-101.md §3, §4): a write on a scheduling link
// is ordered against the link's delete by a row lock on the link. These tests
// hold one side's transaction open on another connection and observe the
// waiter in pg_stat_activity, never by sleeping alone.

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// pac101Twin opens a second, independent pool on db's database, so a test can
// hold a lock while db's own pool is capped.
func pac101Twin(t *testing.T, db *sql.DB) *sql.DB {
	t.Helper()
	var name string
	if err := db.QueryRow(`SELECT current_database()`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	dsn := os.Getenv("PACEDAY_TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://test:test@127.0.0.1:15432/testdb?sslmode=disable"
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	twin, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = twin.Close() })
	return twin
}

// pac101LockWaiters counts sessions of db's database waiting on a lock.
func pac101LockWaiters(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pg_stat_activity
		WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// pac101AwaitWaiters waits (at most 5 s) until n sessions wait on a lock.
func pac101AwaitWaiters(t *testing.T, db *sql.DB, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for pac101LockWaiters(t, db) < n {
		if time.Now().After(deadline) {
			t.Fatalf("no lock waiter appeared (want %d)", n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// pac101HoldDelete runs the owner's delete statement in a transaction it
// leaves open.
func pac101HoldDelete(t *testing.T, db *sql.DB, id uuid.UUID) *sql.Tx {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := DeleteSchedulingLink(tx, id); err != nil || !ok {
		t.Fatalf("held delete: %v %v", ok, err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	return tx
}

func pac101Save(db *sql.DB, linkID uuid.UUID, start time.Time, wrap func(Querier) Querier) (*Booking, error) {
	conn, err := db.Conn(context.Background())
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return SaveBookingOnLiveLink(context.Background(), conn, &Booking{LinkID: linkID, BookerName: "Bo", BookerEmail: "bo@booker.test",
		StartTime: start, EndTime: start.Add(30 * time.Minute)}, wrap)
}

func pac101Bookings(t *testing.T, db *sql.DB, id uuid.UUID) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM bookings WHERE link_id = $1`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// holdingQuerier signals once the insert statement has run (the link is held
// FOR SHARE by the save's transaction) and blocks until released.
type holdingQuerier struct {
	Querier
	ran     chan struct{}
	release chan struct{}
}

func (h holdingQuerier) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	row := h.Querier.QueryRowContext(ctx, q, args...)
	close(h.ran)
	<-h.release
	return row
}

func pac101Start() time.Time { return time.Date(2031, 3, 3, 10, 0, 0, 0, time.UTC) }

// AC-4: a delete held open makes the save wait (seen as a lock waiter); when
// the delete commits the save stores nothing and reports link gone; when it
// rolls back the save stores the booking.
func TestPAC101_SaveWaitsForAHeldDelete(t *testing.T) {
	db := openTestDB(t)
	owner := mustUser(t, db, "pac101-save-wait@example.com")
	for _, commit := range []bool{true, false} {
		link := pac40Link(t, db, owner, "pac101-save-wait-"+uuid.NewString()[:8])
		tx := pac101HoldDelete(t, db, link.ID)
		type result struct {
			b   *Booking
			err error
		}
		done := make(chan result, 1)
		go func() {
			b, err := pac101Save(db, link.ID, pac101Start(), nil)
			done <- result{b, err}
		}()
		pac101AwaitWaiters(t, db, 1)
		if commit {
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
		} else if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		r := <-done
		if commit {
			if !errors.Is(r.err, ErrLinkGone) || r.b != nil {
				t.Fatalf("after the delete committed: %+v, %v; want ErrLinkGone", r.b, r.err)
			}
			if n := pac101Bookings(t, db, link.ID); n != 0 {
				t.Fatalf("%d bookings stored on a deleted link", n)
			}
		} else {
			if r.err != nil || r.b == nil || r.b.LinkID != link.ID {
				t.Fatalf("after the delete rolled back: %+v, %v; want the booking", r.b, r.err)
			}
			if n := pac101Bookings(t, db, link.ID); n != 1 {
				t.Fatalf("%d bookings, want 1", n)
			}
		}
	}
}

// A pause held open behaves as a delete for the booking (contract §2.3).
func TestPAC101_SaveAfterAHeldPauseIsLinkGone(t *testing.T) {
	db := openTestDB(t)
	owner := mustUser(t, db, "pac101-pause@example.com")
	link := pac40Link(t, db, owner, "pac101-pause")
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`UPDATE scheduling_links SET active = false WHERE id = $1`, link.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := pac101Save(db, link.ID, pac101Start(), nil); done <- err }()
	pac101AwaitWaiters(t, db, 1)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, ErrLinkGone) {
		t.Fatalf("save after a pause: %v, want ErrLinkGone", err)
	}
	if n := pac101Bookings(t, db, link.ID); n != 0 {
		t.Fatalf("%d bookings on a paused link", n)
	}
}

// AC-5: a save holding the link FOR SHARE makes the owner's delete wait; the
// save commits, then the delete does; the booking is kept and listed in the
// archive. (No assertion created_at <= deleted_at: spec D-7.)
func TestPAC101_DeleteWaitsForASaveInProgress(t *testing.T) {
	db := openTestDB(t)
	owner := mustUser(t, db, "pac101-del-wait@example.com")
	link := pac40Link(t, db, owner, "pac101-del-wait")
	hold := holdingQuerier{ran: make(chan struct{}), release: make(chan struct{})}
	saved := make(chan *Booking, 1)
	go func() {
		b, err := pac101Save(db, link.ID, pac101Start(), func(q Querier) Querier { hold.Querier = q; return hold })
		if err != nil {
			t.Errorf("save: %v", err)
		}
		saved <- b
	}()
	<-hold.ran
	deleted := make(chan bool, 1)
	go func() {
		ok, err := DeleteSchedulingLinkCtx(context.Background(), db, link.ID)
		if err != nil {
			t.Errorf("delete: %v", err)
		}
		deleted <- ok
	}()
	pac101AwaitWaiters(t, db, 1)
	select {
	case <-deleted:
		t.Fatal("the delete completed while the save held the link")
	default:
	}
	close(hold.release)
	b := <-saved
	if ok := <-deleted; !ok || b == nil {
		t.Fatalf("delete %v, booking %+v; want both", ok, b)
	}
	if at, active := pac40DeletedAt(t, db, link.ID); !at.Valid || active {
		t.Fatalf("link not deleted: %v %v", at, active)
	}
	archived, err := GetArchivedBookings(db, link.ID)
	if err != nil || len(archived) != 1 || archived[0].ID != b.ID {
		t.Fatalf("archive = %+v, %v; want the booking", archived, err)
	}
}

// AC-16: two saves of different slots on one live link, both holding their
// lock at once: neither waits.
func TestPAC101_SavesOnOneLinkDoNotWaitForEachOther(t *testing.T) {
	db := openTestDB(t)
	owner := mustUser(t, db, "pac101-parallel@example.com")
	link := pac40Link(t, db, owner, "pac101-parallel")
	holds := []holdingQuerier{
		{ran: make(chan struct{}), release: make(chan struct{})},
		{ran: make(chan struct{}), release: make(chan struct{})},
	}
	var wg sync.WaitGroup
	for i := range holds {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			h := holds[i]
			start := pac101Start().Add(time.Duration(i) * time.Hour)
			if _, err := pac101Save(db, link.ID, start, func(q Querier) Querier { h.Querier = q; return h }); err != nil {
				t.Errorf("save %d: %v", i, err)
			}
		}(i)
	}
	for _, h := range holds {
		select {
		case <-h.ran:
		case <-time.After(5 * time.Second):
			t.Fatal("a save did not get its lock while the other held its own")
		}
	}
	if n := pac101LockWaiters(t, db); n != 0 {
		t.Fatalf("%d lock waiter(s) with two saves held; want none", n)
	}
	for _, h := range holds {
		close(h.release)
	}
	wg.Wait()
	if n := pac101Bookings(t, db, link.ID); n != 2 {
		t.Fatalf("%d bookings, want 2", n)
	}
}

// pac101IsLockTimeout reports whether err is SQLSTATE 55P03.
func pac101IsLockTimeout(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "55P03"
}

func pac101ShortBound(t *testing.T) {
	t.Helper()
	prev := LinkLockTimeout
	LinkLockTimeout = 300 * time.Millisecond
	t.Cleanup(func() { LinkLockTimeout = prev })
}

// AC-7 (storage half) and the timeout halves of AC-9/AC-13: a link lock held
// past the bound fails each write with 55P03, and nothing is written.
func TestPAC101_EveryLinkWriteGivesUpAtTheBound(t *testing.T) {
	pac101ShortBound(t)
	db := openTestDB(t)
	owner := mustUser(t, db, "pac101-bound-owner@example.com")
	cohost := mustUser(t, db, "pac101-bound-cohost@example.com")
	link := pac40Link(t, db, owner, "pac101-bound")
	if _, err := AddLinkHost(db, link.ID, cohost, "pending"); err != nil {
		t.Fatal(err)
	}
	invitee := mustUser(t, db, "pac101-bound-invitee@example.com")
	holder, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Rollback() }()
	if _, err := holder.Exec(`SELECT 1 FROM scheduling_links WHERE id = $1 FOR UPDATE`, link.ID); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	writes := map[string]func() error{
		"booking": func() error { _, err := pac101Save(db, link.ID, pac101Start(), nil); return err },
		"invite":  func() error { _, _, err := InviteLinkHostCtx(ctx, db, link.ID, invitee); return err },
		"accept":  func() error { _, err := RespondToHostInviteCtx(ctx, db, link.ID, cohost, "accepted"); return err },
		"leave":   func() error { _, err := RemoveLinkHostCtx(ctx, db, link.ID, cohost); return err },
		"patch": func() error {
			_, err := UpdateSchedulingLinkAndCoHostsCtx(ctx, db, link.ID, &SchedulingLink{Slug: link.Slug, Title: "x",
				DurationOptions: []int{30}, DaysOfWeek: []int{1}, WindowStart: "09:00", WindowEnd: "17:00", UsageType: "reusable", Active: true}, owner, nil)
			return err
		},
		"delete": func() error { _, err := DeleteSchedulingLinkCtx(ctx, db, link.ID); return err },
	}
	for name, write := range writes {
		began := time.Now()
		err := write()
		if !pac101IsLockTimeout(err) {
			t.Errorf("%s: %v, want SQLSTATE 55P03", name, err)
		}
		if took := time.Since(began); took > 5*time.Second {
			t.Errorf("%s took %s past a 300 ms bound", name, took)
		}
	}
	_ = holder.Rollback()
	if n := pac101Bookings(t, db, link.ID); n != 0 {
		t.Errorf("%d bookings written past the bound", n)
	}
	hosts, _ := GetLinkHosts(db, link.ID)
	if len(hosts) != 2 || hosts[1].Status != "pending" {
		t.Errorf("host rows changed past the bound: %d", len(hosts))
	}
	if l, _ := GetSchedulingLinkByID(db, link.ID); l == nil || l.Title != link.Title {
		t.Errorf("link changed past the bound: %+v", l)
	}
}

// Host writes against a delete held open: each waits; after the commit it
// writes nothing (invite: ErrLinkGone; accept/leave: no match).
func TestPAC101_HostWritesWaitForAHeldDeleteThenWriteNothing(t *testing.T) {
	db := openTestDB(t)
	owner := mustUser(t, db, "pac101-host-owner@example.com")
	cohost := mustUser(t, db, "pac101-host-cohost@example.com")
	invitee := mustUser(t, db, "pac101-host-invitee@example.com")
	ctx := context.Background()
	cases := map[string]func(id uuid.UUID) (wrote bool, err error){
		"invite": func(id uuid.UUID) (bool, error) {
			h, _, err := InviteLinkHostCtx(ctx, db, id, invitee)
			if errors.Is(err, ErrLinkGone) {
				return false, nil
			}
			return h != nil, err
		},
		"accept":  func(id uuid.UUID) (bool, error) { return RespondToHostInviteCtx(ctx, db, id, cohost, "accepted") },
		"decline": func(id uuid.UUID) (bool, error) { return RespondToHostInviteCtx(ctx, db, id, cohost, "declined") },
		"leave":   func(id uuid.UUID) (bool, error) { return RemoveLinkHostCtx(ctx, db, id, cohost) },
	}
	for name, write := range cases {
		link := pac40Link(t, db, owner, "pac101-host-"+uuid.NewString()[:8])
		if _, err := AddLinkHost(db, link.ID, cohost, "pending"); err != nil {
			t.Fatal(err)
		}
		tx := pac101HoldDelete(t, db, link.ID)
		type result struct {
			wrote bool
			err   error
		}
		done := make(chan result, 1)
		go func() { w, err := write(link.ID); done <- result{w, err} }()
		pac101AwaitWaiters(t, db, 1)
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		r := <-done
		if r.err != nil || r.wrote {
			t.Errorf("%s after the delete committed: wrote %v, %v", name, r.wrote, r.err)
		}
		hosts, _ := GetLinkHosts(db, link.ID)
		if len(hosts) != 2 || hosts[1].UserID != cohost || hosts[1].Status != "pending" {
			t.Errorf("%s: host rows changed on a deleted link", name)
		}
	}
}

// Two PATCHes of one link at once never deadlock: the UPDATE is each one's
// first lock on the link, so the second simply waits (contract §3.2).
func TestPAC101_ConcurrentPatchesDoNotDeadlock(t *testing.T) {
	db := openTestDB(t)
	owner := mustUser(t, db, "pac101-patches@example.com")
	link := pac40Link(t, db, owner, "pac101-patches")
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			l := *link
			l.Title = "t" + uuid.NewString()[:4]
			if _, err := UpdateSchedulingLinkAndCoHostsCtx(ctx, db, link.ID, &l, owner, []string{}); err != nil {
				t.Errorf("patch %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
}

// AC-18 (challenge C-5): on a pool capped at one connection, after a booking
// save, an invite, a PATCH and a lock timeout, the connection's lock_timeout
// is the server default again: the bound never leaks through the pool.
func TestPAC101_BoundDoesNotLeakThroughThePool(t *testing.T) {
	pac101ShortBound(t)
	db := openTestDB(t)
	owner := mustUser(t, db, "pac101-leak-owner@example.com")
	invitee := mustUser(t, db, "pac101-leak-invitee@example.com")
	link := pac40Link(t, db, owner, "pac101-leak")
	db.SetMaxOpenConns(1)
	twin := pac101Twin(t, db)
	ctx := context.Background()
	var pid0 int
	if err := db.QueryRow(`SELECT pg_backend_pid()`).Scan(&pid0); err != nil {
		t.Fatal(err)
	}
	check := func(after string) {
		t.Helper()
		var v string
		var pid int
		if err := db.QueryRow(`SELECT current_setting('lock_timeout'), pg_backend_pid()`).Scan(&v, &pid); err != nil {
			t.Fatal(err)
		}
		if pid != pid0 {
			t.Fatalf("after %s: a different backend (%d, was %d): the check proves nothing", after, pid, pid0)
		}
		if v != "0" {
			t.Fatalf("after %s: lock_timeout on the pooled connection = %q, want the server default 0", after, v)
		}
	}
	check("nothing")
	if _, err := pac101Save(db, link.ID, pac101Start(), nil); err != nil {
		t.Fatal(err)
	}
	check("a booking save")
	if _, _, err := InviteLinkHostCtx(ctx, db, link.ID, invitee); err != nil {
		t.Fatal(err)
	}
	check("an invite")
	l := *link
	l.Title = "renamed"
	if _, err := UpdateSchedulingLinkAndCoHostsCtx(ctx, db, link.ID, &l, owner, []string{}); err != nil {
		t.Fatal(err)
	}
	check("a PATCH")
	holder, err := twin.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := holder.Exec(`SELECT 1 FROM scheduling_links WHERE id = $1 FOR UPDATE`, link.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pac101Save(db, link.ID, pac101Start().Add(time.Hour), nil); !pac101IsLockTimeout(err) {
		t.Fatalf("held link: %v, want 55P03", err)
	}
	_ = holder.Rollback()
	check("a 55P03 timeout")
}
