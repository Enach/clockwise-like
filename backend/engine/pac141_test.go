package engine

// PAC-141: the guardrail baseline's unregistered focus-block reads. A failed
// read of focus blocks is never "no focus blocks": the smart scheduler fails
// (its documented 500), and team availability counts the member unknown,
// never free (PAC-133's rule). Each test fails on the code before the fix.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

// SF-111: a failed read of the caller's focus blocks fails Suggest; slots
// that may land on focus time are not offered.
func TestPAC141_SuggestFocusReadErrorIsAnError(t *testing.T) {
	db := openTestDB(t)
	sched := &SmartScheduler{DB: db, calOps: &mockCalOps{calID: "primary@calendar.google.com"}}
	ctx, user := smartScheduleUser(t, db, "pac141-suggest@example.com")
	readErr := errors.New("pac141: focus read failed")
	storage.InstallFocusBlocksRead(t, func(id uuid.UUID) error {
		if id == user {
			return readErr
		}
		return nil
	})

	now := time.Now().UTC()
	got, err := sched.Suggest(ctx, ScheduleRequest{DurationMinutes: 30, RangeStart: now, RangeEnd: now.Add(5 * 24 * time.Hour), Title: "x"})
	if !errors.Is(err, readErr) {
		t.Fatalf("Suggest = %+v, %v; want the focus read error", got, err)
	}
}

// SF-112: a member whose focus blocks cannot be read is counted unknown in
// every slot, never free, even when their calendar was read.
func TestPAC141_TeamAvailabilityFocusReadErrorIsUnknown(t *testing.T) {
	pac81Scale(t)
	db := openTestDB(t)
	g := newGoogleStub(t)
	team := pac81Team(t, db, g, 2, 0)
	members, err := storage.ListTeamMembers(db, team)
	if err != nil || len(members) != 2 {
		t.Fatalf("members = %d, %v", len(members), err)
	}
	failing := members[1].UserID

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	day := nextWeekday(time.Now())
	e := &TeamAvailabilityEngine{DB: db}

	// Both calendars readable: both free.
	slots, err := e.FindSlots(ctx, team, day, 30)
	if err != nil || len(slots) == 0 {
		t.Fatalf("setup: slots %d err %v", len(slots), err)
	}
	if s := slots[0]; s.FreeCount != 2 || s.UnknownCount != 0 {
		t.Fatalf("setup: slot %+v, want 2 free", s)
	}

	storage.InstallFocusBlocksRead(t, func(id uuid.UUID) error {
		if id == failing {
			return errors.New("pac141: focus read failed")
		}
		return nil
	})
	slots, err = e.FindSlots(ctx, team, day, 30)
	if err != nil || len(slots) == 0 {
		t.Fatalf("slots %d err %v", len(slots), err)
	}
	for _, s := range slots {
		if s.FreeCount != 1 || s.UnknownCount != 1 || s.TotalCount != 2 {
			t.Fatalf("slot %+v: want 1 free, 1 unknown of 2", s)
		}
	}
}
