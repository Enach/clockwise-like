package engine

// PAC-122 batch 2 review (#297), minor 1: SF-044 bare-UID to per-instance
// migration, and the instance half of coveredByUnreadable.

import (
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/storage"
)

// A recurring event's blocker made before SF-044 (keyed by the bare UID) is
// replaced by one blocker per instance; a second sync changes nothing.
func TestPAC122_SF044_BareUIDBlockerMigratesToInstances(t *testing.T) {
	pac81Scale(t)
	db := openTestDB(t)
	c := newB2Calendar(t)
	day := nextWeekday(time.Now())
	ics := fmt.Sprintf("BEGIN:VEVENT\r\nUID:gym\r\nSUMMARY:Gym\r\nDTSTAMP:20250101T000000Z\r\nDTSTART:%s\r\nDTEND:%s\r\nRRULE:FREQ=WEEKLY;COUNT=2\r\nEND:VEVENT\r\n",
		day.Add(10*time.Hour).Format("20060102T150405Z"), day.Add(11*time.Hour).Format("20060102T150405Z"))
	ctx, cal := b2Personal(t, db, ics)
	if err := storage.UpsertPersonalBlocker(db, cal, "gym", "work-old"); err != nil {
		t.Fatal(err)
	}
	if err := (&PersonalBlocker{DB: db}).Sync(ctx, cal); err != nil {
		t.Fatal(err)
	}
	got := blockerIDs(t, db, cal)
	sort.Strings(got)
	want := []string{
		"gym/" + day.Add(10*time.Hour).Format("20060102T150405Z"),
		"gym/" + day.AddDate(0, 0, 7).Add(10*time.Hour).Format("20060102T150405Z"),
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("blockers = %v, want %v", got, want)
	}
	created, deletes := c.counts()
	c.mu.Lock()
	deleted := strings.Join(c.deletes, ",")
	c.mu.Unlock()
	if created != 2 || deletes != 1 || deleted != "work-old" {
		t.Fatalf("first sync: created %d, deleted %q; want 2 and the old blocker", created, deleted)
	}

	if err := (&PersonalBlocker{DB: db}).Sync(ctx, cal); err != nil {
		t.Fatal(err)
	}
	if c2, d2 := c.counts(); c2 != created || d2 != deletes {
		t.Fatalf("second sync created %d and deleted %d more", c2-created, d2-deletes)
	}
}

// A blocker of one instance of a series the feed carries but cannot read
// ("<UID>/<start>") is kept, like the series' own blocker.
func TestPAC122_SF044_UnreadableSeriesKeepsInstanceBlockers(t *testing.T) {
	pac81Scale(t)
	db := openTestDB(t)
	c := newB2Calendar(t)
	day := nextWeekday(time.Now())
	ics := fmt.Sprintf("BEGIN:VEVENT\r\nUID:series\r\nDTSTAMP:20250101T000000Z\r\nDTSTART:%s\r\nDTEND:%s\r\nRRULE:FREQ=SOMETIMES\r\nEND:VEVENT\r\n",
		day.Add(10*time.Hour).Format("20060102T150405Z"), day.Add(11*time.Hour).Format("20060102T150405Z"))
	ctx, cal := b2Personal(t, db, ics)
	instance := "series/" + day.Add(10*time.Hour).Format("20060102T150405Z")
	if err := storage.UpsertPersonalBlocker(db, cal, instance, "work-1"); err != nil {
		t.Fatal(err)
	}
	// A blocker of another UID that merely starts the same way is stale.
	if err := storage.UpsertPersonalBlocker(db, cal, "series-2", "work-2"); err != nil {
		t.Fatal(err)
	}
	if err := (&PersonalBlocker{DB: db}).Sync(ctx, cal); err != nil {
		t.Fatal(err)
	}
	if got := blockerIDs(t, db, cal); len(got) != 1 || got[0] != instance {
		t.Fatalf("blockers = %v, want only %s", got, instance)
	}
	if created, deletes := c.counts(); created != 0 || deletes != 1 {
		t.Fatalf("created %d, deleted %d; want 0 and 1 (series-2)", created, deletes)
	}
}
