package engine

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

// PAC-72: the busy-time half of the offerable-start rule, which the slot
// listing and the booking share through CollectiveSlots.

func pac72EngineLink(t *testing.T, db *sql.DB, owner uuid.UUID, slug string, hosts ...uuid.UUID) *storage.SchedulingLink {
	t.Helper()
	link, err := storage.CreateSchedulingLink(db, &storage.SchedulingLink{
		OwnerUserID: owner, Slug: slug, Title: slug, DurationOptions: []int{30},
		DaysOfWeek: []int{1, 2, 3, 4, 5}, WindowStart: "09:00", WindowEnd: "12:00", UsageType: "reusable", Active: true,
	})
	if err != nil {
		t.Fatalf("create link: %v", err)
	}
	for _, h := range hosts {
		if _, err := storage.AddLinkHost(db, link.ID, h, "accepted"); err != nil {
			t.Fatalf("add host: %v", err)
		}
	}
	return link
}

func pac72EngineUser(t *testing.T, db *sql.DB, email string) uuid.UUID {
	t.Helper()
	u, err := storage.UpsertUser(db, email, email, "", "google", email)
	if err != nil {
		t.Fatalf("upsert %s: %v", email, err)
	}
	return u.ID
}

var pac72Monday = time.Date(2030, 1, 7, 0, 0, 0, 0, time.UTC)

func offeredAt(slots []AvailableSlot) map[string]bool {
	out := map[string]bool{}
	for _, s := range slots {
		out[s.Start.UTC().Format("15:04")] = true
	}
	return out
}

// D-10 / AC-23: a link with no accepted host offers no slot.
func TestPAC72_CollectiveSlots_NoAcceptedHostOffersNothing(t *testing.T) {
	db := openTestDB(t)
	owner := pac72EngineUser(t, db, "pac72-e-hostless@example.com")
	link := pac72EngineLink(t, db, owner, "pac72-e-hostless") // no host row at all

	slots, err := (&BookingEngine{DB: db}).CollectiveSlots(context.Background(), link, pac72Monday, 30)
	if err != nil {
		t.Fatalf("CollectiveSlots: %v", err)
	}
	if len(slots) != 0 {
		t.Fatalf("a link with no accepted host offered %d slots, want none", len(slots))
	}
}

// D-11 / AC-4b: a pending booking on another link the host hosts is busy time.
func TestPAC72_CollectiveSlots_PendingBookingOnAnotherLinkIsBusy(t *testing.T) {
	db := openTestDB(t)
	host := pac72EngineUser(t, db, "pac72-e-pending@example.com")
	link := pac72EngineLink(t, db, host, "pac72-e-pending-this", host)
	other := pac72EngineLink(t, db, host, "pac72-e-pending-other", host)
	at := pac72Monday.Add(10 * time.Hour)
	b, err := storage.CreateBooking(db, &storage.Booking{LinkID: other.ID, BookerName: "P", BookerEmail: "p@example.com", StartTime: at, EndTime: at.Add(30 * time.Minute)})
	if err != nil {
		t.Fatalf("create booking: %v", err)
	}
	if _, err := db.Exec(`UPDATE bookings SET status = 'pending' WHERE id = $1`, b.ID); err != nil {
		t.Fatalf("set pending: %v", err)
	}

	slots, err := (&BookingEngine{DB: db}).CollectiveSlots(context.Background(), link, pac72Monday, 30)
	if err != nil {
		t.Fatalf("CollectiveSlots: %v", err)
	}
	got := offeredAt(slots)
	for _, busy := range []string{"09:45", "10:00", "10:15"} {
		if got[busy] {
			t.Errorf("%s offered over a pending booking on another link of the host", busy)
		}
	}
	for _, free := range []string{"09:30", "10:30"} {
		if !got[free] {
			t.Errorf("%s not offered, but the host is free then", free)
		}
	}
}

// D-7 / AC-17b, AC-17c: a storage failure while reading a host's focus blocks
// or bookings is an error, never "the host is free".
func TestPAC72_CollectiveSlots_StorageFailureIsAnError(t *testing.T) {
	db := openTestDB(t)
	host := pac72EngineUser(t, db, "pac72-e-storage@example.com")
	link := pac72EngineLink(t, db, host, "pac72-e-storage", host)
	boom := errors.New("injected storage failure")

	for name, reads := range map[string]BookingReads{
		"accepted hosts": {AcceptedHosts: func(*sql.DB, uuid.UUID) ([]*storage.LinkHost, error) { return nil, boom }},
		"focus blocks":   {FocusBlocks: func(*sql.DB, uuid.UUID, time.Time) ([]storage.FocusBlock, error) { return nil, boom }},
		"bookings":       {HostBookings: func(*sql.DB, uuid.UUID, time.Time, time.Time) ([]time.Time, error) { return nil, boom }},
	} {
		t.Run(name, func(t *testing.T) {
			slots, err := (&BookingEngine{DB: db, Reads: reads}).CollectiveSlots(context.Background(), link, pac72Monday, 30)
			if !errors.Is(err, boom) {
				t.Fatalf("CollectiveSlots = %d slots, err %v; want the storage error", len(slots), err)
			}
		})
	}
}

// D-6 / AC-17d: a host whose external calendar cannot be read (here: no
// credential at all) is still treated as free for calendar events.
func TestPAC72_CollectiveSlots_UnreadableCalendarIsFree(t *testing.T) {
	db := openTestDB(t)
	host := pac72EngineUser(t, db, "pac72-e-nocal@example.com")
	link := pac72EngineLink(t, db, host, "pac72-e-nocal", host)

	slots, err := (&BookingEngine{DB: db}).CollectiveSlots(context.Background(), link, pac72Monday, 30)
	if err != nil {
		t.Fatalf("CollectiveSlots: %v", err)
	}
	// 09:00 to 11:30 every 15 minutes: the whole window.
	if len(slots) != 11 {
		t.Fatalf("offered %d slots, want the whole window (11)", len(slots))
	}
}
