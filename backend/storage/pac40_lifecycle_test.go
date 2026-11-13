package storage

import (
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// PAC-40 part A, storage side: a deleted link is a state of its own (the
// instant is recorded, the link is paused, nothing else is removed), it is
// gone from every live read and write, and the archive reads see it.

func pac40Link(t *testing.T, db *sql.DB, owner uuid.UUID, slug string) *SchedulingLink {
	t.Helper()
	l, err := CreateSchedulingLink(db, &SchedulingLink{
		OwnerUserID: owner, Slug: slug, Title: "PAC-40 " + slug, DurationOptions: []int{30},
		DaysOfWeek: []int{1, 2, 3, 4, 5}, WindowStart: "09:00", WindowEnd: "17:00", UsageType: "reusable", Active: true,
	})
	if err != nil {
		t.Fatalf("create link: %v", err)
	}
	if _, err := AddLinkHost(db, l.ID, owner, "accepted"); err != nil {
		t.Fatalf("owner host: %v", err)
	}
	return l
}

func pac40Booking(t *testing.T, db *sql.DB, linkID uuid.UUID, name string, start time.Time, status string) *Booking {
	t.Helper()
	b, err := CreateBooking(db, &Booking{LinkID: linkID, BookerName: name, BookerEmail: name + "@booker.test",
		StartTime: start, EndTime: start.Add(30 * time.Minute), Notes: "note " + name})
	if err != nil {
		t.Fatalf("create booking: %v", err)
	}
	if status != "" {
		if _, err := db.Exec(`UPDATE bookings SET status = $1 WHERE id = $2`, status, b.ID); err != nil {
			t.Fatalf("set status: %v", err)
		}
		b.Status = status
	}
	return b
}

func pac40DeletedAt(t *testing.T, db *sql.DB, id uuid.UUID) (deletedAt sql.NullTime, active bool) {
	t.Helper()
	if err := db.QueryRow(`SELECT deleted_at, active FROM scheduling_links WHERE id = $1`, id).Scan(&deletedAt, &active); err != nil {
		t.Fatalf("read link row: %v", err)
	}
	return deletedAt, active
}

// AC-9 and K4-2: delete records the instant and pauses the link, keeps every
// booking (names and emails unchanged) and host row, and a repeated delete
// finds nothing and never moves the instant.
func TestPAC40_DeleteRecordsTheInstantAndKeepsEverything(t *testing.T) {
	db := openTestDB(t)
	owner := mustUser(t, db, "pac40-del-owner@example.com")
	cohost := mustUser(t, db, "pac40-del-cohost@example.com")
	link := pac40Link(t, db, owner, "pac40-del")
	if _, err := AddLinkHost(db, link.ID, cohost, "accepted"); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)
	b1 := pac40Booking(t, db, link.ID, "ann", start, "")
	b2 := pac40Booking(t, db, link.ID, "bob", start.Add(time.Hour), "cancelled")

	before := time.Now().Add(-time.Second)
	deleted, err := DeleteSchedulingLink(db, link.ID)
	if err != nil || !deleted {
		t.Fatalf("delete = %v, %v; want true, nil", deleted, err)
	}
	at, active := pac40DeletedAt(t, db, link.ID)
	if !at.Valid || at.Time.Before(before) || active {
		t.Fatalf("after delete: deleted_at = %v, active = %v; want a recorded instant and paused", at, active)
	}

	bookings, err := GetBookingsByLink(db, link.ID)
	if err != nil || len(bookings) != 2 {
		t.Fatalf("bookings after delete = %d, %v; want both kept", len(bookings), err)
	}
	for _, b := range bookings {
		want := b1
		if b.ID == b2.ID {
			want = b2
		}
		if b.BookerName != want.BookerName || b.BookerEmail != want.BookerEmail || b.Notes != want.Notes {
			t.Errorf("booking %s = %+v, want name/email/notes unchanged (%+v)", b.ID, b, want)
		}
	}
	hosts, err := GetLinkHosts(db, link.ID)
	if err != nil || len(hosts) != 2 {
		t.Fatalf("host rows after delete = %d, %v; want owner and co-host kept", len(hosts), err)
	}

	again, err := DeleteSchedulingLink(db, link.ID)
	if err != nil || again {
		t.Fatalf("repeated delete = %v, %v; want false, nil", again, err)
	}
	if at2, _ := pac40DeletedAt(t, db, link.ID); !at2.Time.Equal(at.Time) {
		t.Fatalf("repeated delete moved deleted_at from %v to %v", at.Time, at2.Time)
	}
	if ok, err := DeleteSchedulingLink(db, uuid.New()); err != nil || ok {
		t.Fatalf("delete of an unknown id = %v, %v; want false, nil", ok, err)
	}
}

// AC-6b (K4-2): concurrent deletes of one link: exactly one finds it, and
// the instant it recorded is the one that stays.
func TestPAC40_ConcurrentDeletesExactlyOneWins(t *testing.T) {
	db := openTestDB(t)
	owner := mustUser(t, db, "pac40-race-del@example.com")
	link := pac40Link(t, db, owner, "pac40-race-del")

	const n = 8
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make([]bool, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i], errs[i] = DeleteSchedulingLink(db, link.ID)
		}(i)
	}
	close(start)
	wg.Wait()
	wins := 0
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("delete %d: %v", i, errs[i])
		}
		if results[i] {
			wins++
		}
	}
	if wins != 1 {
		t.Fatalf("%d concurrent deletes found the link; want exactly 1", wins)
	}
	at, active := pac40DeletedAt(t, db, link.ID)
	if !at.Valid || active {
		t.Fatalf("deleted_at = %v, active = %v", at, active)
	}
	if again, _ := DeleteSchedulingLink(db, link.ID); again {
		t.Fatal("a later delete found the link again")
	}
	if at2, _ := pac40DeletedAt(t, db, link.ID); !at2.Time.Equal(at.Time) {
		t.Fatalf("deleted_at moved from %v to %v", at.Time, at2.Time)
	}
}

// AC-6b (K4-1): an update built from a read made before the delete committed
// writes nothing: the result is nil (the handler's 404) and the link stays
// deleted and paused, even though the update asks for active = true.
func TestPAC40_UpdateAfterDeleteWritesNothing(t *testing.T) {
	db := openTestDB(t)
	owner := mustUser(t, db, "pac40-upd@example.com")
	link := pac40Link(t, db, owner, "pac40-upd")
	read, err := GetSchedulingLinkByID(db, link.ID) // the PATCH's read
	if err != nil || read == nil {
		t.Fatalf("read: %v %v", read, err)
	}
	if ok, err := DeleteSchedulingLink(db, link.ID); err != nil || !ok {
		t.Fatalf("delete: %v %v", ok, err)
	}
	at, _ := pac40DeletedAt(t, db, link.ID)

	read.Title = "revived"
	read.Active = true
	got, err := UpdateSchedulingLink(db, link.ID, read)
	if err != nil || got != nil {
		t.Fatalf("update of a deleted link = %+v, %v; want nil, nil", got, err)
	}
	var title string
	var active bool
	var deletedAt sql.NullTime
	if err := db.QueryRow(`SELECT title, active, deleted_at FROM scheduling_links WHERE id = $1`, link.ID).Scan(&title, &active, &deletedAt); err != nil {
		t.Fatal(err)
	}
	if title != link.Title || active || !deletedAt.Valid || !deletedAt.Time.Equal(at.Time) {
		t.Fatalf("row = %q active=%v deleted_at=%v; want unchanged, paused, deleted at %v", title, active, deletedAt, at.Time)
	}
}

// AC-1, AC-4, AC-5, AC-8 at the storage boundary: every live read and write
// misses a deleted link, while a paused one is still there, and the slug
// stays reserved.
func TestPAC40_DeletedLinkMissesEveryLiveReadAndWrite(t *testing.T) {
	db := openTestDB(t)
	owner := mustUser(t, db, "pac40-live-owner@example.com")
	cohost := mustUser(t, db, "pac40-live-cohost@example.com")
	invitee := mustUser(t, db, "pac40-live-invitee@example.com")
	deleted := pac40Link(t, db, owner, "pac40-live-deleted")
	paused := pac40Link(t, db, owner, "pac40-live-paused")
	for _, l := range []*SchedulingLink{deleted, paused} {
		if _, err := AddLinkHost(db, l.ID, cohost, "accepted"); err != nil {
			t.Fatal(err)
		}
		if _, err := AddLinkHost(db, l.ID, invitee, "pending"); err != nil {
			t.Fatal(err)
		}
	}
	paused.Active = false
	if _, err := UpdateSchedulingLink(db, paused.ID, paused); err != nil {
		t.Fatal(err)
	}
	if ok, err := DeleteSchedulingLink(db, deleted.ID); err != nil || !ok {
		t.Fatal(ok, err)
	}

	if l, err := GetSchedulingLinkByID(db, deleted.ID); err != nil || l != nil {
		t.Errorf("by id: %+v %v; want nil", l, err)
	}
	if l, err := GetSchedulingLinkByID(db, paused.ID); err != nil || l == nil || l.Active {
		t.Errorf("paused by id: %+v %v; want it, inactive", l, err)
	}
	if l, err := GetSchedulingLinkBySlug(db, deleted.Slug); err != nil || l != nil {
		t.Errorf("by slug: %+v %v; want nil", l, err)
	}
	for _, u := range []uuid.UUID{owner, cohost} {
		links, err := ListSchedulingLinksByUser(db, u)
		if err != nil {
			t.Fatal(err)
		}
		if len(links) != 1 || links[0].ID != paused.ID {
			t.Errorf("list for %s = %d links; want only the paused one", u, len(links))
		}
	}
	invites, err := GetPendingInvitesForUser(db, invitee)
	if err != nil || len(invites) != 1 || invites[0].LinkID != paused.ID {
		t.Errorf("pending invites = %+v, %v; want only the paused link's", invites, err)
	}
	if ok, err := RespondToHostInvite(db, deleted.ID, invitee, "accepted"); err != nil || ok {
		t.Errorf("accept on a deleted link = %v, %v; want false", ok, err)
	}
	if st, found, _ := GetLinkHostStatus(db, deleted.ID, invitee); !found || st != "pending" {
		t.Errorf("invitee row after accept = %q %v; want still pending", st, found)
	}
	for _, u := range []uuid.UUID{cohost, invitee, owner} {
		if ok, err := RemoveLinkHost(db, deleted.ID, u); err != nil || ok {
			t.Errorf("leave of %s on a deleted link = %v, %v; want false", u, ok, err)
		}
	}
	if hosts, _ := GetLinkHosts(db, deleted.ID); len(hosts) != 3 {
		t.Errorf("host rows of the deleted link = %d; want 3 kept", len(hosts))
	}
	// Paused is unchanged by PAC-40 (PAC-30 D-5): accept and leave still work.
	if ok, err := RespondToHostInvite(db, paused.ID, invitee, "accepted"); err != nil || !ok {
		t.Errorf("accept on a paused link = %v, %v; want true", ok, err)
	}
	if ok, err := RemoveLinkHost(db, paused.ID, cohost); err != nil || !ok {
		t.Errorf("leave on a paused link = %v, %v; want true", ok, err)
	}
	if exists, err := SlugExists(db, deleted.Slug); err != nil || !exists {
		t.Errorf("slug of a deleted link: exists = %v, %v; want reserved (D-3)", exists, err)
	}
}

// AC-6b, third case: even a row whose CHECK was bypassed (deleted_at set,
// active = true) is not found by the public slug lookup.
func TestPAC40_PublicLookupFiltersDeletedEvenWhenActive(t *testing.T) {
	db := openTestDB(t) // isolated database: dropping the CHECK affects nothing else
	owner := mustUser(t, db, "pac40-forced@example.com")
	link := pac40Link(t, db, owner, "pac40-forced")
	if _, err := db.Exec(`ALTER TABLE scheduling_links DROP CONSTRAINT scheduling_links_deleted_inactive`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE scheduling_links SET deleted_at = NOW(), active = true WHERE id = $1`, link.ID); err != nil {
		t.Fatal(err)
	}
	if l, err := GetSchedulingLinkBySlug(db, link.Slug); err != nil || l != nil {
		t.Fatalf("public lookup of a forced deleted+active row = %+v, %v; want nil", l, err)
	}
}

// Migration 037's two CHECKs (contract §4, K4-1): a deleted link cannot be
// made active, and an erased booking holds empty strings in both booker columns.
func TestPAC40_ChecksRefuseADeletedActiveLinkAndAnErasedNonBlankBooking(t *testing.T) {
	db := openTestDB(t)
	owner := mustUser(t, db, "pac40-check@example.com")
	link := pac40Link(t, db, owner, "pac40-check")
	b := pac40Booking(t, db, link.ID, "carl", time.Date(2026, 3, 3, 10, 0, 0, 0, time.UTC), "")
	if ok, err := DeleteSchedulingLink(db, link.ID); err != nil || !ok {
		t.Fatal(ok, err)
	}
	checkViolation := func(what string, err error) {
		t.Helper()
		var pg *pgconn.PgError
		if !errors.As(err, &pg) || pg.Code != "23514" {
			t.Errorf("%s: err = %v; want a check violation (23514)", what, err)
		}
	}
	_, err := db.Exec(`UPDATE scheduling_links SET active = true WHERE id = $1`, link.ID)
	checkViolation("activate a deleted link", err)
	_, err = db.Exec(`UPDATE bookings SET booker_erased_at = NOW() WHERE id = $1`, b.ID)
	checkViolation("mark erased with the name kept", err)
	if _, err := db.Exec(`UPDATE bookings SET booker_name = '', booker_email = '', booker_erased_at = NOW() WHERE id = $1`, b.ID); err != nil {
		t.Errorf("a blank erased booking: %v; want accepted", err)
	}
}

// AC-15..AC-17 at the storage boundary (API-051): uses_count counts every
// booking, cancelled ones included, on every read that returns it.
func TestPAC40_UsesCountCountsCancelledBookings(t *testing.T) {
	db := openTestDB(t)
	owner := mustUser(t, db, "pac40-uses@example.com")
	link := pac40Link(t, db, owner, "pac40-uses")
	start := time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC)
	pac40Booking(t, db, link.ID, "dan", start, "cancelled")
	pac40Booking(t, db, link.ID, "eve", start.Add(time.Hour), "confirmed")

	byID, _ := GetSchedulingLinkByID(db, link.ID)
	bySlug, _ := GetSchedulingLinkBySlug(db, link.Slug)
	list, _ := ListSchedulingLinksByUser(db, owner)
	upd, _ := UpdateSchedulingLink(db, link.ID, byID)
	for name, got := range map[string]*SchedulingLink{"by id": byID, "by slug": bySlug, "list": list[0], "update": upd} {
		if got == nil || got.UsesCount != 2 {
			t.Errorf("%s: uses_count = %+v; want 2 (cancelled counted)", name, got)
		}
	}
}

// AC-7, first half: a deleted link's confirmed booking keeps its hosts busy.
// (The second half, after an erasure run, is part B.)
func TestPAC40_DeletedLinkBookingStaysBusy(t *testing.T) {
	db := openTestDB(t)
	owner := mustUser(t, db, "pac40-busy-owner@example.com")
	cohost := mustUser(t, db, "pac40-busy-cohost@example.com")
	link := pac40Link(t, db, owner, "pac40-busy")
	if _, err := AddLinkHost(db, link.ID, cohost, "accepted"); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 3, 5, 13, 0, 0, 0, time.UTC)
	pac40Booking(t, db, link.ID, "fay", start, "confirmed")
	if ok, err := DeleteSchedulingLink(db, link.ID); err != nil || !ok {
		t.Fatal(ok, err)
	}
	for _, u := range []uuid.UUID{owner, cohost} {
		busy, err := GetBusyBookingsForUser(db, u, start.Add(-time.Hour), start.Add(time.Hour))
		if err != nil || len(busy) != 2 || !busy[0].Equal(start) || !busy[1].Equal(start.Add(30*time.Minute)) {
			t.Errorf("busy for %s = %v, %v; want the deleted link's booking", u, busy, err)
		}
	}
}

// AC-21, AC-22, AC-23, AC-24 at the storage boundary: the archive lists the
// owner's deleted links only, newest deletion first, with counts and
// instants over every booking; the detail lookup is owner AND deleted.
func TestPAC40_ArchiveReads(t *testing.T) {
	db := openTestDB(t)
	owner := mustUser(t, db, "pac40-arch-owner@example.com")
	other := mustUser(t, db, "pac40-arch-other@example.com")
	if got, err := ListArchivedSchedulingLinks(db, owner); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("empty archive = %#v, %v; want a non-nil empty slice", got, err)
	}
	older := pac40Link(t, db, owner, "pac40-arch-older")
	newer := pac40Link(t, db, owner, "pac40-arch-newer")
	live := pac40Link(t, db, owner, "pac40-arch-live")
	foreign := pac40Link(t, db, other, "pac40-arch-foreign")
	t1 := time.Date(2026, 1, 5, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)
	pac40Booking(t, db, newer.ID, "gil", t1, "")
	late := pac40Booking(t, db, newer.ID, "hal", t2, "cancelled")
	for _, l := range []*SchedulingLink{older, newer, foreign} {
		if ok, err := DeleteSchedulingLink(db, l.ID); err != nil || !ok {
			t.Fatal(ok, err)
		}
	}
	// Make the order independent of the clock's resolution.
	if _, err := db.Exec(`UPDATE scheduling_links SET deleted_at = deleted_at - interval '1 hour' WHERE id = $1`, older.ID); err != nil {
		t.Fatal(err)
	}

	got, err := ListArchivedSchedulingLinks(db, owner)
	if err != nil || len(got) != 2 {
		t.Fatalf("archive = %d entries, %v; want the owner's two deleted links", len(got), err)
	}
	if got[0].ID != newer.ID || got[1].ID != older.ID {
		t.Fatalf("archive order = %s, %s; want newest deletion first", got[0].ID, got[1].ID)
	}
	n := got[0]
	if n.BookingsCount != 2 || n.AwaitingErasure != 2 || n.LastBookingEndAt == nil ||
		!n.LastBookingEndAt.Equal(late.EndTime) || n.ErasedAt != nil || n.Title != newer.Title || n.Slug != newer.Slug {
		t.Fatalf("archived link = %+v; want 2 bookings, last end %v (the cancelled one), none erased", n, late.EndTime)
	}
	if o := got[1]; o.BookingsCount != 0 || o.LastBookingEndAt != nil || o.AwaitingErasure != 0 {
		t.Fatalf("archived link without bookings = %+v", o)
	}

	if a, err := GetArchivedSchedulingLink(db, owner, newer.ID); err != nil || a == nil || a.ID != newer.ID {
		t.Fatalf("detail = %+v, %v", a, err)
	}
	for name, id := range map[string]uuid.UUID{"live": live.ID, "foreign": foreign.ID, "unknown": uuid.New()} {
		if a, err := GetArchivedSchedulingLink(db, owner, id); err != nil || a != nil {
			t.Errorf("detail of a %s link = %+v, %v; want nil", name, a, err)
		}
	}
	bookings, err := GetArchivedBookings(db, newer.ID)
	if err != nil || len(bookings) != 2 || bookings[0].ID != late.ID || bookings[0].BookerErasedAt != nil {
		t.Fatalf("archived bookings = %+v, %v; want both, latest start first, none erased", bookings, err)
	}

	// An erased row (part B writes it; forced here) reads with its instant.
	if _, err := db.Exec(`UPDATE bookings SET booker_name = '', booker_email = '', booker_erased_at = NOW() WHERE link_id = $1`, newer.ID); err != nil {
		t.Fatal(err)
	}
	a, _ := GetArchivedSchedulingLink(db, owner, newer.ID)
	if a.AwaitingErasure != 0 || a.ErasedAt == nil || a.BookingsCount != 2 {
		t.Fatalf("after a forced erasure: %+v; want 0 awaiting, erased_at set, count kept", a)
	}
	bookings, _ = GetArchivedBookings(db, newer.ID)
	if bookings[0].BookerErasedAt == nil || bookings[0].BookerName != "" {
		t.Fatalf("erased booking = %+v", bookings[0])
	}
}

// Migration 037 down (contract §4, spec §7): no row is lost, a deleted link
// comes back paused, an erased booking reads blank, and up again leaves
// every link not deleted.
func TestPAC40_Migration037DownKeepsEveryRow(t *testing.T) {
	db := openTestDB(t)
	owner := mustUser(t, db, "pac40-down@example.com")
	link := pac40Link(t, db, owner, "pac40-down")
	b := pac40Booking(t, db, link.ID, "ida", time.Date(2026, 3, 6, 10, 0, 0, 0, time.UTC), "")
	if ok, err := DeleteSchedulingLink(db, link.ID); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if _, err := db.Exec(`UPDATE bookings SET booker_name = '', booker_email = '', booker_erased_at = NOW() WHERE id = $1`, b.ID); err != nil {
		t.Fatal(err)
	}
	down, err := migrationFiles.ReadFile("migrations/037_scheduling_link_lifecycle.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(down)); err != nil {
		t.Fatalf("037 down: %v", err)
	}
	var active bool
	if err := db.QueryRow(`SELECT active FROM scheduling_links WHERE id = $1`, link.ID).Scan(&active); err != nil || active {
		t.Fatalf("deleted link after down: active = %v, %v; want kept, paused", active, err)
	}
	var name, email string
	if err := db.QueryRow(`SELECT booker_name, booker_email FROM bookings WHERE id = $1`, b.ID).Scan(&name, &email); err != nil || name != "" || email != "" {
		t.Fatalf("erased booking after down = %q %q, %v; want kept, blank", name, email, err)
	}
	up, err := migrationFiles.ReadFile("migrations/037_scheduling_link_lifecycle.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(up)); err != nil {
		t.Fatalf("037 up again: %v", err)
	}
	if l, err := GetSchedulingLinkByID(db, link.ID); err != nil || l == nil || l.Active {
		t.Fatalf("after up again: %+v, %v; want the link back as paused (the deletion is not restored, K4-8)", l, err)
	}
}
