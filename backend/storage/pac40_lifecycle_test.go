package storage

import (
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
)

// PAC-40: deleted is a state of its own, distinct from paused, and keeps every
// row (D-2); uses_count is the number of booking rows whatever their status
// (D-6, D-8, contract C-3).

func pac40Link(t *testing.T, db *sql.DB, owner uuid.UUID, slug, usage string, maxUses *int) *SchedulingLink {
	t.Helper()
	l, err := CreateSchedulingLink(db, &SchedulingLink{
		OwnerUserID: owner, Slug: slug, Title: slug, DurationOptions: []int{30},
		DaysOfWeek: []int{1, 2, 3, 4, 5}, WindowStart: "09:00", WindowEnd: "17:00", UsageType: usage, MaxUses: maxUses, Active: true,
	})
	if err != nil {
		t.Fatalf("create link: %v", err)
	}
	if _, err := AddLinkHost(db, l.ID, owner, "accepted"); err != nil {
		t.Fatalf("owner host: %v", err)
	}
	return l
}

func pac40Booking(t *testing.T, db *sql.DB, linkID uuid.UUID, start time.Time, status string) {
	t.Helper()
	b, err := CreateBooking(db, &Booking{LinkID: linkID, BookerName: "B", BookerEmail: "b@x.com", StartTime: start, EndTime: start.Add(30 * time.Minute)})
	if err != nil {
		t.Fatalf("create booking: %v", err)
	}
	if _, err := db.Exec(`UPDATE bookings SET status = $1 WHERE id = $2`, status, b.ID); err != nil {
		t.Fatalf("set status: %v", err)
	}
}

// AC-7 and AC-9: deleting keeps the link's bookings and host rows, and its
// confirmed bookings keep their hosts busy.
func TestDeleteSchedulingLinkKeepsRowsAndBusyTime(t *testing.T) {
	db := openTestDB(t)
	owner := mustUser(t, db, "pac40-keep-owner@example.com")
	cohost := mustUser(t, db, "pac40-keep-cohost@example.com")
	pending := mustUser(t, db, "pac40-keep-pending@example.com")
	link := pac40Link(t, db, owner, "pac40-keep", "reusable", nil)
	if _, err := AddLinkHost(db, link.ID, cohost, "accepted"); err != nil {
		t.Fatal(err)
	}
	if _, err := AddLinkHost(db, link.ID, pending, "pending"); err != nil {
		t.Fatal(err)
	}
	future := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Hour)
	pac40Booking(t, db, link.ID, future, "confirmed")
	pac40Booking(t, db, link.ID, future.Add(2*time.Hour), "cancelled")

	if err := DeleteSchedulingLink(db, link.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	var bookings, hosts int
	if err := db.QueryRow(`SELECT COUNT(*) FROM bookings WHERE link_id = $1`, link.ID).Scan(&bookings); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM scheduling_link_hosts WHERE link_id = $1`, link.ID).Scan(&hosts); err != nil {
		t.Fatal(err)
	}
	if bookings != 2 || hosts != 3 {
		t.Fatalf("after delete: %d booking rows, %d host rows; want 2 and 3", bookings, hosts)
	}
	for name, u := range map[string]uuid.UUID{"owner": owner, "co-host": cohost} {
		times, err := GetConfirmedBookingsForUser(db, u, future.Add(-time.Hour), future.Add(time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		if len(times) != 2 || !times[0].Equal(future) {
			t.Fatalf("%s busy times after delete = %v, want the confirmed booking", name, times)
		}
	}
	if exists, err := SlugExists(db, link.Slug); err != nil || !exists {
		t.Fatalf("SlugExists after delete = %v, %v; want reserved", exists, err)
	}
}

// Every read path that names the link misses a deleted one; a paused one stays.
func TestDeletedLinkIsInvisibleToReadsPausedIsNot(t *testing.T) {
	db := openTestDB(t)
	owner := mustUser(t, db, "pac40-reads-owner@example.com")
	invitee := mustUser(t, db, "pac40-reads-invitee@example.com")
	deleted := pac40Link(t, db, owner, "pac40-reads-deleted", "reusable", nil)
	paused := pac40Link(t, db, owner, "pac40-reads-paused", "reusable", nil)
	for _, l := range []*SchedulingLink{deleted, paused} {
		if _, err := AddLinkHost(db, l.ID, invitee, "pending"); err != nil {
			t.Fatal(err)
		}
	}
	paused.Active = false
	if _, err := UpdateSchedulingLink(db, paused.ID, paused); err != nil {
		t.Fatal(err)
	}
	if err := DeleteSchedulingLink(db, deleted.ID); err != nil {
		t.Fatal(err)
	}

	if l, err := GetSchedulingLinkByID(db, deleted.ID); err != nil || l != nil {
		t.Fatalf("GetSchedulingLinkByID(deleted) = %+v, %v; want nil", l, err)
	}
	if l, err := GetSchedulingLinkByID(db, paused.ID); err != nil || l == nil || l.Active {
		t.Fatalf("GetSchedulingLinkByID(paused) = %+v, %v; want the paused link", l, err)
	}
	links, err := ListSchedulingLinksByUser(db, owner)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 || links[0].ID != paused.ID {
		t.Fatalf("ListSchedulingLinksByUser = %d links, want only the paused one", len(links))
	}
	invites, err := GetPendingInvitesForUser(db, invitee)
	if err != nil {
		t.Fatal(err)
	}
	if len(invites) != 1 || invites[0].LinkID != paused.ID {
		t.Fatalf("GetPendingInvitesForUser = %d invites, want only the paused link's", len(invites))
	}
	if matched, err := RespondToHostInvite(db, deleted.ID, invitee, "accepted"); err != nil || matched {
		t.Fatalf("RespondToHostInvite(deleted) = %v, %v; want no match", matched, err)
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM scheduling_link_hosts WHERE link_id = $1 AND user_id = $2`, deleted.ID, invitee).Scan(&status); err != nil || status != "pending" {
		t.Fatalf("invite status on the deleted link = %q, %v; want pending", status, err)
	}
	if matched, err := RespondToHostInvite(db, paused.ID, invitee, "accepted"); err != nil || !matched {
		t.Fatalf("RespondToHostInvite(paused) = %v, %v; want a match (PAC-30 D-5)", matched, err)
	}
	deleted.Active = true
	if l, err := UpdateSchedulingLink(db, deleted.ID, deleted); err != nil || l != nil {
		t.Fatalf("UpdateSchedulingLink(deleted) = %+v, %v; want no row", l, err)
	}
	if l, err := GetSchedulingLinkBySlug(db, deleted.Slug); err != nil || l != nil {
		t.Fatalf("GetSchedulingLinkBySlug(deleted) = %+v, %v; want nil", l, err)
	}
	// Deleting twice changes nothing and is not an error at this layer.
	if err := DeleteSchedulingLink(db, deleted.ID); err != nil {
		t.Fatal(err)
	}
}

// D-6 / C-3: uses_count counts every booking row on every read path.
func TestUsesCountIncludesCancelledBookings(t *testing.T) {
	db := openTestDB(t)
	owner := mustUser(t, db, "pac40-count-owner@example.com")
	max := 2
	link := pac40Link(t, db, owner, "pac40-count", "recurring", &max)
	start := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Hour)
	pac40Booking(t, db, link.ID, start, "confirmed")
	pac40Booking(t, db, link.ID, start.Add(time.Hour), "cancelled")

	byID, err := GetSchedulingLinkByID(db, link.ID)
	if err != nil {
		t.Fatal(err)
	}
	bySlug, err := GetSchedulingLinkBySlug(db, link.Slug)
	if err != nil {
		t.Fatal(err)
	}
	listed, err := ListSchedulingLinksByUser(db, owner)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := UpdateSchedulingLink(db, link.ID, byID)
	if err != nil {
		t.Fatal(err)
	}
	for name, n := range map[string]int{"by id": byID.UsesCount, "by slug": bySlug.UsesCount, "list": listed[0].UsesCount, "update": updated.UsesCount} {
		if n != 2 {
			t.Errorf("%s: uses_count = %d, want 2 (cancelled counts)", name, n)
		}
	}
}

// AC-12, AC-17a and spec section 7: migration 028 leaves pre-existing inactive
// links paused, counts bookings cancelled before it, and its down migration
// turns a deleted link back into a paused one without losing a row.
func TestMigration028DeletedMarker(t *testing.T) {
	db, to := migrationTestDB(t)
	to(27)
	owner := seedMigrationUser(t, db, "pac40-mig-owner@example.com", time.Now())
	inactive, limited := uuid.New(), uuid.New()
	if _, err := db.Exec(`INSERT INTO scheduling_links (id, owner_user_id, slug, title, active) VALUES ($1, $2, 'pac40-mig-inactive', 'Inactive', false)`, inactive, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO scheduling_links (id, owner_user_id, slug, title, usage_type, max_uses) VALUES ($1, $2, 'pac40-mig-limited', 'Limited', 'recurring', 1)`, limited, owner); err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Add(48 * time.Hour)
	if _, err := db.Exec(`INSERT INTO bookings (link_id, booker_name, booker_email, start_time, end_time, status) VALUES ($1, 'B', 'b@x.com', $2, $3, 'cancelled')`, limited, start, start.Add(30*time.Minute)); err != nil {
		t.Fatal(err)
	}

	to(28)
	links, err := ListSchedulingLinksByUser(db, owner)
	if err != nil {
		t.Fatal(err)
	}
	got := map[uuid.UUID]*SchedulingLink{}
	for _, l := range links {
		got[l.ID] = l
	}
	if l := got[inactive]; l == nil || l.Active {
		t.Fatalf("pre-existing inactive link after 028 = %+v; want listed and paused (D-1)", l)
	}
	if l := got[limited]; l == nil || l.UsesCount != 1 {
		t.Fatalf("limited link after 028 = %+v; want uses_count 1 (D-8)", l)
	}

	// Delete one, then down: it reappears as paused; up again: still listed.
	if err := DeleteSchedulingLink(db, limited); err != nil {
		t.Fatal(err)
	}
	to(27)
	var active bool
	var bookings int
	if err := db.QueryRow(`SELECT active FROM scheduling_links WHERE id = $1`, limited).Scan(&active); err != nil || active {
		t.Fatalf("deleted link after down = active %v, %v; want present and inactive", active, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM bookings WHERE link_id = $1`, limited).Scan(&bookings); err != nil || bookings != 1 {
		t.Fatalf("bookings after down = %d, %v; want 1", bookings, err)
	}
	to(28)
	if l, err := GetSchedulingLinkByID(db, limited); err != nil || l == nil || l.Active {
		t.Fatalf("after up again = %+v, %v; want a paused link", l, err)
	}
}
