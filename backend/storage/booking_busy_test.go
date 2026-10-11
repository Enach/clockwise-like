package storage

import (
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
)

// PAC-64: a confirmed booking makes its hosts busy whether or not a calendar
// event was created for it, and focus blocks belong to the user who owns them.

func mustUser(t *testing.T, db *sql.DB, email string) uuid.UUID {
	t.Helper()
	u, err := UpsertUser(db, email, email, "", "google", email)
	if err != nil {
		t.Fatalf("upsert %s: %v", email, err)
	}
	return u.ID
}

func TestGetConfirmedBookingsForUser_CountsHostedLinksWithoutCalendarEvents(t *testing.T) {
	db := openTestDB(t)
	owner := mustUser(t, db, "pac64-owner@example.com")
	accepted := mustUser(t, db, "pac64-accepted@example.com")
	pending := mustUser(t, db, "pac64-pending@example.com")
	stranger := mustUser(t, db, "pac64-stranger@example.com")

	link, err := CreateSchedulingLink(db, &SchedulingLink{
		OwnerUserID: owner, Slug: "pac64-link", Title: "PAC-64", DurationOptions: []int{30},
		DaysOfWeek: []int{1, 2, 3, 4, 5}, WindowStart: "09:00", WindowEnd: "17:00", UsageType: "reusable", Active: true,
	})
	if err != nil {
		t.Fatalf("create link: %v", err)
	}
	for id, status := range map[uuid.UUID]string{owner: "accepted", accepted: "accepted", pending: "pending"} {
		if _, err := AddLinkHost(db, link.ID, id, status); err != nil {
			t.Fatalf("add host: %v", err)
		}
	}

	day := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	confirmed := day.Add(13 * time.Hour)
	cancelled := day.Add(15 * time.Hour)
	for _, b := range []*Booking{
		{LinkID: link.ID, BookerName: "A", BookerEmail: "a@x.com", StartTime: confirmed, EndTime: confirmed.Add(30 * time.Minute), Status: "confirmed"},
		{LinkID: link.ID, BookerName: "B", BookerEmail: "b@x.com", StartTime: cancelled, EndTime: cancelled.Add(30 * time.Minute), Status: "cancelled"},
	} {
		created, err := CreateBooking(db, b)
		if err != nil {
			t.Fatalf("create booking: %v", err)
		}
		// CreateBooking always stores the default status; set the one we want.
		if _, err := db.Exec(`UPDATE bookings SET status = $1 WHERE id = $2`, b.Status, created.ID); err != nil {
			t.Fatalf("set status: %v", err)
		}
	}
	// No booking_events rows: no calendar event was ever created.

	for name, tc := range map[string]struct {
		user uuid.UUID
		want int
	}{
		"owner":            {owner, 1},
		"accepted co-host": {accepted, 1},
		"pending co-host":  {pending, 0},
		"stranger":         {stranger, 0},
	} {
		times, err := GetConfirmedBookingsForUser(db, tc.user, day, day.Add(24*time.Hour))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := len(times) / 2; got != tc.want {
			t.Errorf("%s: %d busy booking(s), want %d", name, got, tc.want)
		}
		if tc.want == 1 && (!times[0].Equal(confirmed) || !times[1].Equal(confirmed.Add(30*time.Minute))) {
			t.Errorf("%s: busy range %v..%v, want the confirmed booking", name, times[0], times[1])
		}
	}
}

func TestListFocusBlocksForUserWeek_OnlyTheOwnersBlocks(t *testing.T) {
	db := openTestDB(t)
	alice := mustUser(t, db, "pac64-alice@example.com")
	bob := mustUser(t, db, "pac64-bob@example.com")
	monday := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)

	if err := SaveFocusBlock(db, alice, "evt-alice", "2026-10-19", monday.Add(10*time.Hour), monday.Add(11*time.Hour)); err != nil {
		t.Fatalf("save alice: %v", err)
	}
	if err := SaveFocusBlock(db, bob, "evt-bob", "2026-10-19", monday.Add(12*time.Hour), monday.Add(13*time.Hour)); err != nil {
		t.Fatalf("save bob: %v", err)
	}

	got, err := ListFocusBlocksForUserWeek(db, alice, monday)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].GoogleEventID != "evt-alice" {
		t.Fatalf("alice's week = %+v, want only her own block", got)
	}
}
