package storage

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// pac72HorizonMigration is the version of the scheduling_link_horizon_refusal
// migration (PAC-72 revision 2). Renumbered with the files if it has to move
// past another feature's migration at merge time (manifest mergeConditions).
const pac72HorizonMigration = 32

// AC-25: links that existed before the migration read horizon 60 and generic
// refusals; the column checks hold 1..365 and generic|specific; the down
// migration returns the links to their previous shape (losing only the two
// settings), and up again restores the defaults.
func TestMigrationSchedulingLinkHorizonRefusal(t *testing.T) {
	db, to := migrationTestDB(t)
	to(pac72HorizonMigration - 1)
	owner := seedMigrationUser(t, db, "pac72-m@example.com", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	var linkID uuid.UUID
	if err := db.QueryRow(`INSERT INTO scheduling_links (owner_user_id, slug, title) VALUES ($1, 'pac72-m', 'Before') RETURNING id`, owner).Scan(&linkID); err != nil {
		t.Fatalf("seed link: %v", err)
	}

	to(pac72HorizonMigration)
	// Read at the schema under test, not through GetSchedulingLinkByID, whose
	// query names columns of later migrations (PAC-40's deleted_at, 037).
	l, err := readHorizonLinkAtSchema(db, linkID)
	if err != nil || l == nil {
		t.Fatalf("read migrated link: %v %v", l, err)
	}
	if l.BookingHorizonDays != 60 || l.RefusalMessage != RefusalGeneric || l.Title != "Before" {
		t.Errorf("migrated link = horizon %d, refusal %q, title %q; want 60, generic, Before", l.BookingHorizonDays, l.RefusalMessage, l.Title)
	}
	for _, bad := range []string{
		`UPDATE scheduling_links SET booking_horizon_days = 0 WHERE id = $1`,
		`UPDATE scheduling_links SET booking_horizon_days = 366 WHERE id = $1`,
		`UPDATE scheduling_links SET refusal_message = 'loud' WHERE id = $1`,
	} {
		if _, err := db.Exec(bad, linkID); err == nil || !strings.Contains(err.Error(), "check constraint") {
			t.Errorf("%s: err %v, want a check-constraint violation", bad, err)
		}
	}
	if _, err := db.Exec(`UPDATE scheduling_links SET booking_horizon_days = 365, refusal_message = 'specific' WHERE id = $1`, linkID); err != nil {
		t.Fatal(err)
	}

	to(pac72HorizonMigration - 1)
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM information_schema.columns WHERE table_name = 'scheduling_links' AND column_name IN ('booking_horizon_days', 'refusal_message')`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	var title string
	if err := db.QueryRow(`SELECT title FROM scheduling_links WHERE id = $1`, linkID).Scan(&title); err != nil || n != 0 || title != "Before" {
		t.Errorf("after down: %d new columns left, title %q (%v); want 0 and the link kept", n, title, err)
	}

	to(pac72HorizonMigration)
	if l, err = readHorizonLinkAtSchema(db, linkID); err != nil || l.BookingHorizonDays != 60 || l.RefusalMessage != RefusalGeneric {
		t.Errorf("up again: %+v %v, want the defaults (the down destroyed the chosen settings)", l, err)
	}
}

// The settings round-trip through create and update; an update that does not
// name them (zero values) keeps them.
func TestSchedulingLinkHorizonAndRefusalRoundTrip(t *testing.T) {
	db := openTestDB(t)
	owner := seedMigrationUser(t, db, "pac72-rt-"+uuid.NewString()[:8]+"@example.com", time.Now())
	created, err := CreateSchedulingLink(db, &SchedulingLink{OwnerUserID: owner, Slug: "pac72-rt-" + uuid.NewString()[:8], Title: "RT", WindowStart: "09:00", WindowEnd: "17:00"})
	if err != nil {
		t.Fatal(err)
	}
	if created.BookingHorizonDays != DefaultBookingHorizonDays || created.RefusalMessage != RefusalGeneric {
		t.Fatalf("created = %d %q, want the defaults", created.BookingHorizonDays, created.RefusalMessage)
	}
	created.BookingHorizonDays, created.RefusalMessage = 10, RefusalSpecific
	updated, err := UpdateSchedulingLink(db, created.ID, created)
	if err != nil || updated.BookingHorizonDays != 10 || updated.RefusalMessage != RefusalSpecific {
		t.Fatalf("updated = %+v %v", updated, err)
	}
	updated.BookingHorizonDays, updated.RefusalMessage, updated.Title = 0, "", "Renamed"
	kept, err := UpdateSchedulingLink(db, created.ID, updated)
	if err != nil || kept.BookingHorizonDays != 10 || kept.RefusalMessage != RefusalSpecific || kept.Title != "Renamed" {
		t.Fatalf("an update without the settings = %+v %v, want them kept", kept, err)
	}
	links, err := ListSchedulingLinksByUser(db, owner)
	if err != nil || len(links) != 1 || links[0].BookingHorizonDays != 10 || links[0].RefusalMessage != RefusalSpecific {
		t.Fatalf("list = %v %v", links, err)
	}
	bySlug, err := GetSchedulingLinkBySlug(db, created.Slug)
	if err != nil || bySlug.BookingHorizonDays != 10 {
		t.Fatalf("by slug = %+v %v", bySlug, err)
	}
}

// GetUserTimezone reads the stored zone and never creates a settings row
// (PAC-72 AC-30).
func TestGetUserTimezone(t *testing.T) {
	db := openTestDB(t)
	none := seedMigrationUser(t, db, "pac72-tz-none-"+uuid.NewString()[:8]+"@example.com", time.Now())
	tz, found, err := GetUserTimezone(db, none)
	if err != nil || found || tz != "" {
		t.Fatalf("no row: %q %v %v", tz, found, err)
	}
	var rows int
	if err := db.QueryRow(`SELECT count(*) FROM settings WHERE user_id = $1`, none).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("a settings row was created: %d %v", rows, err)
	}
	set := seedMigrationUser(t, db, "pac72-tz-set-"+uuid.NewString()[:8]+"@example.com", time.Now())
	if _, err := db.Exec(`INSERT INTO settings (user_id, timezone) VALUES ($1, 'Asia/Tokyo')`, set); err != nil {
		t.Fatal(err)
	}
	if tz, found, err := GetUserTimezone(db, set); err != nil || !found || tz != "Asia/Tokyo" {
		t.Fatalf("stored zone: %q %v %v", tz, found, err)
	}
	_ = db.Close()
	if _, _, err := GetUserTimezone(db, set); err == nil {
		t.Fatal("a failed read returned no error")
	}
}

// readHorizonLinkAtSchema reads the three fields the PAC-72 migration test
// asserts with a query valid at migration 032 itself.
func readHorizonLinkAtSchema(db Querier, id uuid.UUID) (*SchedulingLink, error) {
	var l SchedulingLink
	err := db.QueryRow(`SELECT title, booking_horizon_days, refusal_message FROM scheduling_links WHERE id = $1`, id).
		Scan(&l.Title, &l.BookingHorizonDays, &l.RefusalMessage)
	if err != nil {
		return nil, err
	}
	return &l, nil
}
