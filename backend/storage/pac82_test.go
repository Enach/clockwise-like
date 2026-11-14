package storage

// PAC-82 (contracts/features/PAC-82.md sections 3-6): the booking save is
// decided again under ordered advisory keys. These tests hold one save inside
// its transaction (AfterBookingLocks / AfterBookingInsert) on real PostgreSQL
// and observe the other in pg_locks, never by sleeping alone.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// pac82Gate holds one save at a seam until released; err, when set before
// release, is what AfterBookingInsert returns (a rollback).
type pac82Gate struct {
	reached chan struct{}
	release chan struct{}
	once    sync.Once
	err     error
}

func newPAC82Gate() *pac82Gate {
	return &pac82Gate{reached: make(chan struct{}), release: make(chan struct{})}
}

func (g *pac82Gate) wait(t *testing.T, what string) {
	t.Helper()
	select {
	case <-g.reached:
	case <-time.After(10 * time.Second):
		t.Fatalf("%s never reached its seam", what)
	}
}

func (g *pac82Gate) open() { g.once.Do(func() { close(g.release) }) }

// pac82Seams installs the two storage seams, dispatching by booker email.
type pac82Seams struct {
	mu      sync.Mutex
	locks   map[string]*pac82Gate
	inserts map[string]*pac82Gate
}

func installPAC82Seams(t *testing.T) *pac82Seams {
	t.Helper()
	s := &pac82Seams{locks: map[string]*pac82Gate{}, inserts: map[string]*pac82Gate{}}
	AfterBookingLocks = func(b *Booking) { s.pass(s.locks, b) }
	AfterBookingInsert = func(b *Booking) error {
		if g := s.pass(s.inserts, b); g != nil {
			return g.err
		}
		return nil
	}
	t.Cleanup(func() {
		AfterBookingLocks, AfterBookingInsert = nil, nil
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, g := range s.locks {
			g.open()
		}
		for _, g := range s.inserts {
			g.open()
		}
	})
	return s
}

func (s *pac82Seams) pass(m map[string]*pac82Gate, b *Booking) *pac82Gate {
	s.mu.Lock()
	g := m[b.BookerEmail]
	s.mu.Unlock()
	if g == nil {
		return nil
	}
	select {
	case <-g.reached:
	default:
		close(g.reached)
	}
	<-g.release
	return g
}

func (s *pac82Seams) atLocks(email string) *pac82Gate {
	s.mu.Lock()
	defer s.mu.Unlock()
	g := newPAC82Gate()
	s.locks[email] = g
	return g
}

func (s *pac82Seams) atInsert(email string) *pac82Gate {
	s.mu.Lock()
	defer s.mu.Unlock()
	g := newPAC82Gate()
	s.inserts[email] = g
	return g
}

type pac82Result struct {
	b   *Booking
	err error
}

// pac82Save runs one save on its own connection and goroutine.
func pac82Save(db *sql.DB, linkID uuid.UUID, start, end time.Time, email, key string) <-chan pac82Result {
	out := make(chan pac82Result, 1)
	go func() {
		conn, err := db.Conn(context.Background())
		if err != nil {
			out <- pac82Result{nil, err}
			return
		}
		defer conn.Close()
		b, err := SaveBookingOnLiveLink(context.Background(), conn, &Booking{LinkID: linkID, BookerName: "Booker", BookerEmail: email,
			StartTime: start, EndTime: end, IdempotencyKey: key}, nil)
		out <- pac82Result{b, err}
	}()
	return out
}

func pac82Await(t *testing.T, ch <-chan pac82Result, what string) pac82Result {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(15 * time.Second):
		t.Fatalf("%s did not finish", what)
		panic("unreachable")
	}
}

// pac82AdvisoryWaiters counts advisory lock requests of db's database that
// are not granted.
func pac82AdvisoryWaiters(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pg_locks
		WHERE locktype = 'advisory' AND NOT granted
		  AND database = (SELECT oid FROM pg_database WHERE datname = current_database())`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func pac82AwaitAdvisoryWaiters(t *testing.T, db *sql.DB, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for pac82AdvisoryWaiters(t, db) < n {
		if time.Now().After(deadline) {
			t.Fatalf("no advisory waiter appeared (want %d)", n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func pac82LinkWith(t *testing.T, db *sql.DB, owner uuid.UUID, usage string, maxUses *int) *SchedulingLink {
	t.Helper()
	l, err := CreateSchedulingLink(db, &SchedulingLink{
		OwnerUserID: owner, Slug: "pac82-" + uuid.NewString()[:8], Title: "PAC-82", DurationOptions: []int{30},
		DaysOfWeek: []int{1, 2, 3, 4, 5}, WindowStart: "09:00", WindowEnd: "17:00", UsageType: usage, MaxUses: maxUses, Active: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AddLinkHost(db, l.ID, owner, "accepted"); err != nil {
		t.Fatal(err)
	}
	return l
}

func pac82Slot(h, m int) (time.Time, time.Time) {
	s := time.Date(2031, 3, 3, h, m, 0, 0, time.UTC)
	return s, s.Add(30 * time.Minute)
}

func pac82Key(label string) string {
	return strings.ReplaceAll("pac82-"+label+"-"+uuid.NewString(), ".", "_")
}

// AC-4 (and AC-31): R held after its keys; S on another link of the same host,
// overlapping (not equal) slot, waits on an advisory key. R commits: S is host
// busy and wrote nothing. R rolls back instead: S saves.
func TestPAC82_OverlappingSaveWaitsThenLosesOrWins(t *testing.T) {
	for _, commit := range []bool{true, false} {
		t.Run(fmt.Sprintf("commit=%v", commit), func(t *testing.T) {
			db := openTestDB(t)
			seams := installPAC82Seams(t)
			host := mustUser(t, db, "pac82-ac4-host@example.com")
			l1 := pac82LinkWith(t, db, host, "reusable", nil)
			l2 := pac82LinkWith(t, db, host, "reusable", nil)
			gate := seams.atInsert("r@booker.test")
			if !commit {
				gate.err = errors.New("injected failure")
			}
			rs, re := pac82Slot(10, 0)
			r := pac82Save(db, l1.ID, rs, re, "r@booker.test", "")
			gate.wait(t, "R")
			ss, se := pac82Slot(10, 15)
			s := pac82Save(db, l2.ID, ss, se, "s@booker.test", "")
			pac82AwaitAdvisoryWaiters(t, db, 1)
			gate.open()
			rr := pac82Await(t, r, "R")
			sr := pac82Await(t, s, "S")
			if commit {
				if rr.err != nil || !errors.Is(sr.err, ErrHostBusy) {
					t.Fatalf("R %v, S %v; want R saved, S ErrHostBusy", rr.err, sr.err)
				}
				if pac101Bookings(t, db, l2.ID) != 0 {
					t.Fatal("S wrote a row")
				}
			} else {
				if rr.err == nil || sr.err != nil {
					t.Fatalf("R %v, S %v; want R rolled back, S saved", rr.err, sr.err)
				}
				if pac101Bookings(t, db, l1.ID) != 0 || pac101Bookings(t, db, l2.ID) != 1 {
					t.Fatal("rows do not match R rolled back, S saved")
				}
			}
		})
	}
}

// AC-31: same link, the winner held after its insert, the loser waiting on
// the keys: the loser's re-check sees the committed winner (READ COMMITTED,
// separate statements) and answers taken.
func TestPAC82_RecheckSeesTheWinnerCommittedDuringTheWait(t *testing.T) {
	db := openTestDB(t)
	seams := installPAC82Seams(t)
	host := mustUser(t, db, "pac82-ac31@example.com")
	link := pac82LinkWith(t, db, host, "reusable", nil)
	gate := seams.atInsert("w@booker.test")
	s, e := pac82Slot(11, 0)
	w := pac82Save(db, link.ID, s, e, "w@booker.test", "")
	gate.wait(t, "winner")
	l := pac82Save(db, link.ID, s, e, "l@booker.test", "")
	pac82AwaitAdvisoryWaiters(t, db, 1)
	gate.open()
	if r := pac82Await(t, w, "winner"); r.err != nil {
		t.Fatal(r.err)
	}
	if r := pac82Await(t, l, "loser"); !errors.Is(r.err, ErrSlotTaken) {
		t.Fatalf("loser: %v, want ErrSlotTaken", r.err)
	}
	if n := pac101Bookings(t, db, link.ID); n != 1 {
		t.Fatalf("%d rows, want 1", n)
	}
}

// AC-5 (amended by contract section 4.3) and PAC-101 AC-16 as amended: two
// fine saves on an unlimited link whose slots are at least 15 minutes apart,
// both held after their keys at once: no advisory waiter. A coarse save does
// wait for a fine save of a common host at another time.
func TestPAC82_FineSavesFarApartDoNotWaitCoarseDoes(t *testing.T) {
	db := openTestDB(t)
	seams := installPAC82Seams(t)
	host := mustUser(t, db, "pac82-ac5@example.com")
	link := pac82LinkWith(t, db, host, "reusable", nil)
	as, ae := pac82Slot(9, 0)
	bs, be := pac82Slot(9, 45) // 15 minutes after the first ends
	ka := BookingAdvisoryKeys([]uuid.UUID{host}, link.ID, false, as, ae)
	kb := BookingAdvisoryKeys([]uuid.UUID{host}, link.ID, false, bs, be)
	for _, x := range ka {
		for _, y := range kb {
			if x.Class == BookingLockClassBucket && x == y {
				t.Fatalf("bucket key %+v shared: the derivation, not the save, would make them wait", x)
			}
		}
	}
	ga, gb := seams.atLocks("a@booker.test"), seams.atLocks("b@booker.test")
	a := pac82Save(db, link.ID, as, ae, "a@booker.test", "")
	b := pac82Save(db, link.ID, bs, be, "b@booker.test", "")
	ga.wait(t, "A")
	gb.wait(t, "B")
	if n := pac82AdvisoryWaiters(t, db); n != 0 {
		t.Fatalf("%d advisory waiter(s) with two far-apart fine saves held", n)
	}
	// A coarse save of the same host at a third time waits for both.
	prev := BookingKeyCap
	BookingKeyCap = 1
	defer func() { BookingKeyCap = prev }()
	cs, ce := pac82Slot(14, 0)
	c := pac82Save(db, link.ID, cs, ce, "c@booker.test", "")
	pac82AwaitAdvisoryWaiters(t, db, 1)
	ga.open()
	gb.open()
	for _, r := range []pac82Result{pac82Await(t, a, "A"), pac82Await(t, b, "B"), pac82Await(t, c, "C")} {
		if r.err != nil {
			t.Fatal(r.err)
		}
	}
	if n := pac101Bookings(t, db, link.ID); n != 3 {
		t.Fatalf("%d rows, want 3", n)
	}
}

// AC-6: two links with no host in common, same time, both held: no waiter.
func TestPAC82_LinksWithoutCommonHostDoNotWait(t *testing.T) {
	db := openTestDB(t)
	seams := installPAC82Seams(t)
	h1 := mustUser(t, db, "pac82-ac6-1@example.com")
	h2 := mustUser(t, db, "pac82-ac6-2@example.com")
	l1 := pac82LinkWith(t, db, h1, "reusable", nil)
	l2 := pac82LinkWith(t, db, h2, "reusable", nil)
	s, e := pac82Slot(10, 0)
	for _, x := range BookingAdvisoryKeys([]uuid.UUID{h1}, l1.ID, false, s, e) {
		for _, y := range BookingAdvisoryKeys([]uuid.UUID{h2}, l2.ID, false, s, e) {
			if x.Class == y.Class && x.ObjID == y.ObjID && !(x.Shared && y.Shared) {
				t.Fatalf("hash collision %+v: pick other users", x)
			}
		}
	}
	g1, g2 := seams.atLocks("1@booker.test"), seams.atLocks("2@booker.test")
	a := pac82Save(db, l1.ID, s, e, "1@booker.test", "")
	b := pac82Save(db, l2.ID, s, e, "2@booker.test", "")
	g1.wait(t, "1")
	g2.wait(t, "2")
	if n := pac82AdvisoryWaiters(t, db); n != 0 {
		t.Fatalf("%d advisory waiter(s)", n)
	}
	g1.open()
	g2.open()
	if pac82Await(t, a, "1").err != nil || pac82Await(t, b, "2").err != nil {
		t.Fatal("a save failed")
	}
}

// AC-7 / AC-8: a use-limited link serializes all its saves on its uses key;
// the second waits while the first is held, then finds the limit reached.
func TestPAC82_UseLimitedSavesWaitAndRecount(t *testing.T) {
	three := 3
	for _, c := range []struct {
		name    string
		usage   string
		max     *int
		stored  int
		wantErr error
	}{
		{"single use, none stored", "single_use", nil, 0, ErrLinkExhausted},
		{"max 3, two stored", "recurring", &three, 2, ErrLinkExhausted},
		{"max 3, one stored", "recurring", &three, 1, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			db := openTestDB(t)
			seams := installPAC82Seams(t)
			host := mustUser(t, db, "pac82-ac8@example.com")
			link := pac82LinkWith(t, db, host, c.usage, c.max)
			for i := 0; i < c.stored; i++ {
				s, e := pac82Slot(9+i, 0)
				if r := pac82Await(t, pac82Save(db, link.ID, s, e, fmt.Sprintf("pre%d@booker.test", i), ""), "seed"); r.err != nil {
					t.Fatal(r.err)
				}
			}
			gate := seams.atLocks("first@booker.test")
			fs, fe := pac82Slot(14, 0)
			first := pac82Save(db, link.ID, fs, fe, "first@booker.test", "")
			gate.wait(t, "first")
			ss, se := pac82Slot(16, 0) // far apart: only the uses key is common
			second := pac82Save(db, link.ID, ss, se, "second@booker.test", "")
			pac82AwaitAdvisoryWaiters(t, db, 1)
			var class int32
			if err := db.QueryRow(`SELECT classid::int FROM pg_locks WHERE locktype = 'advisory' AND NOT granted`).Scan(&class); err != nil || class != BookingLockClassUses {
				t.Fatalf("waiting on class %d (%v), want %d", class, err, BookingLockClassUses)
			}
			gate.open()
			if r := pac82Await(t, first, "first"); r.err != nil {
				t.Fatal(r.err)
			}
			r := pac82Await(t, second, "second")
			if !errors.Is(r.err, c.wantErr) || (c.wantErr == nil && r.err != nil) {
				t.Fatalf("second: %v, want %v", r.err, c.wantErr)
			}
			want := c.stored + 2
			if c.wantErr != nil {
				want = c.stored + 1
			}
			if n := pac101Bookings(t, db, link.ID); n != want {
				t.Fatalf("%d rows, want %d", n, want)
			}
		})
	}
}

// AC-15: the same keyed attempt, A held after its keys; A' waits; A commits:
// A' is the replay of A's booking. A rolls back instead: A' saves a new one.
func TestPAC82_SameKeyWaitsThenReplaysOrSaves(t *testing.T) {
	for _, commit := range []bool{true, false} {
		t.Run(fmt.Sprintf("commit=%v", commit), func(t *testing.T) {
			db := openTestDB(t)
			seams := installPAC82Seams(t)
			host := mustUser(t, db, "pac82-ac15@example.com")
			link := pac82LinkWith(t, db, host, "reusable", nil)
			key := pac82Key("ac15")
			s, e := pac82Slot(10, 0)
			gate := seams.atInsert("k@booker.test")
			if !commit {
				gate.err = errors.New("injected failure")
			}
			a := pac82Save(db, link.ID, s, e, "k@booker.test", key)
			gate.wait(t, "A")
			// A' carries the same details; its own seam is not armed (the
			// gate is consumed by A's first pass, so it passes straight on).
			seams.mu.Lock()
			delete(seams.inserts, "k@booker.test")
			seams.mu.Unlock()
			a2 := pac82Save(db, link.ID, s, e, "k@booker.test", key)
			pac82AwaitAdvisoryWaiters(t, db, 1)
			gate.open()
			ra, ra2 := pac82Await(t, a, "A"), pac82Await(t, a2, "A'")
			if commit {
				if ra.err != nil || ra2.err != nil || !ra2.b.Replayed || ra2.b.ID != ra.b.ID {
					t.Fatalf("A %+v %v, A' %+v %v; want A' the replay of A", ra.b, ra.err, ra2.b, ra2.err)
				}
			} else if ra.err == nil || ra2.err != nil || ra2.b.Replayed {
				t.Fatalf("A %v, A' %+v %v; want A rolled back and A' a new booking", ra.err, ra2.b, ra2.err)
			}
			if n := pac101Bookings(t, db, link.ID); n != 1 {
				t.Fatalf("%d rows, want 1", n)
			}
		})
	}
}

// AC-16 (unique-index path): the same key, different non-overlapping times,
// so no common bucket key: A held after its insert, A' inserts and waits on
// the unique index; A commits: A' gets 23505, resolved as key reused.
func TestPAC82_SameKeyOtherDetailsResolvesTheUniqueViolation(t *testing.T) {
	db := openTestDB(t)
	seams := installPAC82Seams(t)
	host := mustUser(t, db, "pac82-ac16@example.com")
	link := pac82LinkWith(t, db, host, "reusable", nil)
	key := pac82Key("ac16")
	gate := seams.atInsert("a@booker.test")
	as, ae := pac82Slot(9, 0)
	a := pac82Save(db, link.ID, as, ae, "a@booker.test", key)
	gate.wait(t, "A")
	bs, be := pac82Slot(15, 0)
	b := pac82Save(db, link.ID, bs, be, "a@booker.test", key)
	pac101AwaitWaiters(t, db, 1)
	gate.open()
	if r := pac82Await(t, a, "A"); r.err != nil {
		t.Fatal(r.err)
	}
	if r := pac82Await(t, b, "A'"); !errors.Is(r.err, ErrIdempotencyKeyReused) {
		t.Fatalf("A': %+v %v, want ErrIdempotencyKeyReused", r.b, r.err)
	}
	if n := pac101Bookings(t, db, link.ID); n != 1 {
		t.Fatalf("%d rows, want 1", n)
	}
}

// AC-23: a save waiting past the (shortened) bound on an advisory key, and on
// the unique index, fails with 55P03 and writes nothing.
func TestPAC82_WaitsGiveUpAtTheBound(t *testing.T) {
	pac101ShortBound(t)
	db := openTestDB(t)
	seams := installPAC82Seams(t)
	host := mustUser(t, db, "pac82-ac23@example.com")
	link := pac82LinkWith(t, db, host, "reusable", nil)
	s, e := pac82Slot(10, 0)

	gate := seams.atLocks("holder@booker.test")
	holder := pac82Save(db, link.ID, s, e, "holder@booker.test", "")
	gate.wait(t, "holder")
	r := pac101WithinBound(t, "advisory waiter", func() pac82Result { return <-pac82Save(db, link.ID, s, e, "w@booker.test", "") })
	if !pac101IsLockTimeout(r.err) {
		t.Fatalf("advisory waiter: %v, want 55P03", r.err)
	}
	gate.open()
	if hr := pac82Await(t, holder, "holder"); hr.err != nil {
		t.Fatal(hr.err)
	}

	key := pac82Key("ac23")
	ig := seams.atInsert("ih@booker.test")
	is, ie := pac82Slot(12, 0)
	ih := pac82Save(db, link.ID, is, ie, "ih@booker.test", key)
	ig.wait(t, "insert holder")
	os, oe := pac82Slot(15, 0)
	r = pac101WithinBound(t, "unique-index waiter", func() pac82Result { return <-pac82Save(db, link.ID, os, oe, "iw@booker.test", key) })
	if !pac101IsLockTimeout(r.err) {
		t.Fatalf("unique-index waiter: %v, want 55P03", r.err)
	}
	ig.open()
	if hr := pac82Await(t, ih, "insert holder"); hr.err != nil {
		t.Fatal(hr.err)
	}
	if n := pac101Bookings(t, db, link.ID); n != 2 {
		t.Fatalf("%d rows, want the two holders' only", n)
	}
}

// AC-9 / AC-11 / AC-12 / AC-13 at the save: a stored key replays only for the
// same link, the same details and under 24 hours.
func TestPAC82_KeyDecisionAtTheSave(t *testing.T) {
	db := openTestDB(t)
	host := mustUser(t, db, "pac82-key@example.com")
	link := pac82LinkWith(t, db, host, "reusable", nil)
	other := pac82LinkWith(t, db, host, "reusable", nil)
	key := pac82Key("ac9")
	s, e := pac82Slot(10, 0)
	first := pac82Await(t, pac82Save(db, link.ID, s, e, "k@booker.test", key), "first")
	if first.err != nil || first.b.Replayed {
		t.Fatalf("first: %+v %v", first.b, first.err)
	}
	again := pac82Await(t, pac82Save(db, link.ID, s, e, "k@booker.test", key), "replay")
	if again.err != nil || !again.b.Replayed || again.b.ID != first.b.ID {
		t.Fatalf("replay: %+v %v", again.b, again.err)
	}
	if r := pac82Await(t, pac82Save(db, link.ID, s, e, "other@booker.test", key), "other email"); !errors.Is(r.err, ErrIdempotencyKeyReused) {
		t.Fatalf("other email: %v", r.err)
	}
	// AC-13: the same key on another link is a new request there.
	if r := pac82Await(t, pac82Save(db, other.ID, s.Add(2*time.Hour), e.Add(2*time.Hour), "k@booker.test", key), "other link"); r.err != nil || r.b.Replayed {
		t.Fatalf("other link: %+v %v, want a new booking", r.b, r.err)
	}
	// AC-12: 24 hours old (or more) is reused whatever the details.
	if _, err := db.Exec(`UPDATE bookings SET created_at = now() - interval '24 hours' WHERE id = $1`, first.b.ID); err != nil {
		t.Fatal(err)
	}
	if r := pac82Await(t, pac82Save(db, link.ID, s, e, "k@booker.test", key), "expired"); !errors.Is(r.err, ErrIdempotencyKeyReused) {
		t.Fatalf("24 h old: %v, want ErrIdempotencyKeyReused", r.err)
	}
	if n := pac101Bookings(t, db, link.ID); n != 1 {
		t.Fatalf("%d rows on the link, want 1", n)
	}
}

// AC-21 / AC-22: migration 038 builds on existing duplicate and cross-link
// overlapping bookings without changing them; a new overlapping booking is
// then refused; the down migration keeps every row and loses only the keys.
func TestPAC82_Migration038(t *testing.T) {
	db, to := migrationTestDB(t)
	to(37)
	owner := seedMigrationUser(t, db, "m38-owner@example.com", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	cohost := seedMigrationUser(t, db, "m38-cohost@example.com", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	mkLink := func(slug string, o uuid.UUID) uuid.UUID {
		var id uuid.UUID
		if err := db.QueryRow(`INSERT INTO scheduling_links (owner_user_id, slug, title) VALUES ($1, $2, $2) RETURNING id`, o, slug).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO scheduling_link_hosts (link_id, user_id, status) VALUES ($1, $2, 'accepted')`, id, o); err != nil {
			t.Fatal(err)
		}
		return id
	}
	l1, l2 := mkLink("m38-a", owner), mkLink("m38-b", cohost)
	if _, err := db.Exec(`INSERT INTO scheduling_link_hosts (link_id, user_id, status) VALUES ($1, $2, 'accepted')`, l2, owner); err != nil {
		t.Fatal(err)
	}
	s, e := pac82Slot(10, 0)
	for _, row := range []struct {
		link uuid.UUID
		off  time.Duration
	}{{l1, 0}, {l1, 0}, {l2, 15 * time.Minute}} {
		if _, err := db.Exec(`INSERT INTO bookings (link_id, booker_name, booker_email, start_time, end_time) VALUES ($1, 'B', 'b@m38.test', $2, $3)`,
			row.link, s.Add(row.off), e.Add(row.off)); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := func() string {
		t.Helper()
		var out string
		if err := db.QueryRow(`SELECT string_agg(id::text || link_id::text || start_time::text || end_time::text || status || booker_email, ',' ORDER BY id) FROM bookings`).Scan(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	before := snapshot()
	to(38)
	if snapshot() != before {
		t.Fatal("migration 038 changed existing bookings")
	}
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = SaveBookingOnLiveLink(context.Background(), conn, &Booking{LinkID: l1, BookerName: "N", BookerEmail: "n@m38.test", StartTime: s, EndTime: e, IdempotencyKey: pac82Key("m38")}, nil)
	if !errors.Is(err, ErrSlotTaken) {
		t.Fatalf("new overlapping booking: %v, want ErrSlotTaken", err)
	}
	ks, ke := pac82Slot(15, 0)
	if _, err := SaveBookingOnLiveLink(context.Background(), conn, &Booking{LinkID: l1, BookerName: "K", BookerEmail: "k@m38.test", StartTime: ks, EndTime: ke, IdempotencyKey: pac82Key("m38k")}, nil); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	var bad error
	_, bad = db.Exec(`UPDATE bookings SET idempotency_key = 'short' WHERE booker_email = 'k@m38.test'`)
	if bad == nil {
		t.Error("the format check accepted a 5-character key")
	}
	withKeys := snapshot()
	to(37)
	if snapshot() != withKeys {
		t.Fatal("down 038 lost a booking row")
	}
	var col int
	if err := db.QueryRow(`SELECT COUNT(*) FROM information_schema.columns WHERE table_name = 'bookings' AND column_name = 'idempotency_key'`).Scan(&col); err != nil || col != 0 {
		t.Fatalf("idempotency_key column still there after down (%v)", err)
	}
	to(38)
}
