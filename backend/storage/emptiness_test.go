package storage

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// PAC-40 (spec §6, contract §4): a deleted link and its bookings, erased or
// not, are still account data for PAC-71's emptiness rule. The owner's
// account is not empty because of the deleted link, and a co-host's is not
// empty because of their kept host row.
func TestPAC40_DeletedAndErasedLinksStillCountAsAccountData(t *testing.T) {
	db := openTestDB(t)
	owner := mustUser(t, db, "pac40-empty-owner@example.com")
	cohost := mustUser(t, db, "pac40-empty-cohost@example.com")
	for _, u := range []uuid.UUID{owner, cohost} {
		if k, err := FirstNonEmptyKind(db, u); err != nil || k != "" {
			t.Fatalf("fresh account %s: %q %v; want empty", u, k, err)
		}
	}
	link := pac40Link(t, db, owner, "pac40-empty")
	if _, err := AddLinkHost(db, link.ID, cohost, "accepted"); err != nil {
		t.Fatal(err)
	}
	b := pac40Booking(t, db, link.ID, "jo", time.Date(2026, 3, 9, 10, 0, 0, 0, time.UTC), "")
	if ok, err := DeleteSchedulingLink(db, link.ID); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if _, err := db.Exec(`UPDATE bookings SET booker_name = '', booker_email = '', booker_erased_at = NOW() WHERE id = $1`, b.ID); err != nil {
		t.Fatal(err)
	}
	if k, err := FirstNonEmptyKind(db, owner); err != nil || k != "scheduling_link_owned" {
		t.Errorf("owner of a deleted, erased link: %q %v; want scheduling_link_owned", k, err)
	}
	if k, err := FirstNonEmptyKind(db, cohost); err != nil || k != "scheduling_link_host" {
		t.Errorf("co-host of a deleted link: %q %v; want scheduling_link_host", k, err)
	}
	// The bookings alone still count (D11): with the owner's link row aside,
	// the booking check is what remains.
	var bookingKind bool
	if err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM bookings b JOIN scheduling_links l ON l.id = b.link_id WHERE l.owner_user_id = $1)`, owner).Scan(&bookingKind); err != nil || !bookingKind {
		t.Errorf("D11 check on an erased booking of a deleted link = %v, %v; want true", bookingKind, err)
	}
}
