package engine

import (
	"context"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/storage"
)

// PAC-64: a host's booking slots are blocked by the host's own focus blocks
// and confirmed bookings (even with no calendar event), and by nobody else's
// focus blocks.
func TestCollectiveSlots_BlocksOnlyTheHostsFocusAndBookings(t *testing.T) {
	db := openTestDB(t)
	mk := func(email string) *storage.User {
		u, err := storage.UpsertUser(db, email, email, "", "google", email)
		if err != nil {
			t.Fatalf("upsert %s: %v", email, err)
		}
		return u
	}
	host := mk("pac64-host@example.com")
	other := mk("pac64-other@example.com")

	link, err := storage.CreateSchedulingLink(db, &storage.SchedulingLink{
		OwnerUserID: host.ID, Slug: "pac64-slots", Title: "PAC-64 slots", DurationOptions: []int{30},
		DaysOfWeek: []int{1, 2, 3, 4, 5}, WindowStart: "09:00", WindowEnd: "17:00", UsageType: "reusable", Active: true,
	})
	if err != nil {
		t.Fatalf("create link: %v", err)
	}
	if _, err := storage.AddLinkHost(db, link.ID, host.ID, "accepted"); err != nil {
		t.Fatalf("add host: %v", err)
	}

	day := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC) // a Monday
	at := func(h, m int) time.Time { return day.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute) }

	// The host's own focus block, 10:00-11:00: must block.
	if err := storage.SaveFocusBlock(db, host.ID, "evt-host", "2026-10-19", at(10, 0), at(11, 0)); err != nil {
		t.Fatalf("save host block: %v", err)
	}
	// Another user's focus block, 12:00-13:00: must NOT block this host.
	if err := storage.SaveFocusBlock(db, other.ID, "evt-other", "2026-10-19", at(12, 0), at(13, 0)); err != nil {
		t.Fatalf("save other block: %v", err)
	}
	// A confirmed booking on the host's link at 14:00, with no calendar event
	// (no booking_events row): must block.
	if _, err := storage.CreateBooking(db, &storage.Booking{
		LinkID: link.ID, BookerName: "Booker", BookerEmail: "booker@example.com",
		StartTime: at(14, 0), EndTime: at(14, 30),
	}); err != nil {
		t.Fatalf("create booking: %v", err)
	}

	slots, err := (&BookingEngine{DB: db}).CollectiveSlots(context.Background(), link, day, 30)
	if err != nil {
		t.Fatalf("CollectiveSlots: %v", err)
	}
	offered := map[string]bool{}
	for _, s := range slots {
		offered[s.Start.UTC().Format("15:04")] = true
	}

	for _, blocked := range []string{"10:00", "10:30", "14:00"} {
		if offered[blocked] {
			t.Errorf("%s is offered, but the host is busy then", blocked)
		}
	}
	for _, free := range []string{"09:00", "12:00", "12:30", "15:00"} {
		if !offered[free] {
			t.Errorf("%s is not offered, but the host is free then (another user's focus block must not count)", free)
		}
	}
}
