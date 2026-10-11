package engine

import (
	"context"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/storage"
)

// A recap shows only the data of the user it is for: their focus blocks,
// habits and meeting briefs, never another user's.
func TestDailyRecap_OnlyTheUsersOwnData(t *testing.T) {
	db := openTestDB(t)
	alice, err := storage.UpsertUser(db, "recap-alice@example.com", "Alice", "", "google", "recap-alice")
	if err != nil {
		t.Fatalf("upsert alice: %v", err)
	}
	bob, err := storage.UpsertUser(db, "recap-bob@example.com", "Bob", "", "google", "recap-bob")
	if err != nil {
		t.Fatalf("upsert bob: %v", err)
	}
	day := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)

	for _, f := range []struct {
		user  *storage.User
		event string
		hour  int
	}{{alice, "evt-recap-alice", 9}, {bob, "evt-recap-bob", 14}} {
		start := day.Add(time.Duration(f.hour) * time.Hour)
		if err := storage.SaveFocusBlock(db, f.user.ID, f.event, "2026-10-19", start, start.Add(time.Hour)); err != nil {
			t.Fatalf("save focus block: %v", err)
		}
		var habitID string
		if err := db.QueryRow(`INSERT INTO habits (user_id, title, duration_minutes) VALUES ($1, $2, 30) RETURNING id`,
			f.user.ID, f.user.Name+" habit").Scan(&habitID); err != nil {
			t.Fatalf("insert habit: %v", err)
		}
		hs := start.Add(2 * time.Hour)
		if _, err := db.Exec(`INSERT INTO habit_occurrences (habit_id, scheduled_date, start_time, end_time) VALUES ($1, $2, $3, $4)`,
			habitID, "2026-10-19", hs, hs.Add(30*time.Minute)); err != nil {
			t.Fatalf("insert habit occurrence: %v", err)
		}
		if _, err := db.Exec(`INSERT INTO meeting_briefs (calendar_event_id, user_id, generated_at) VALUES ($1, $2, $3)`,
			f.event+"-brief", f.user.ID, day.Add(time.Hour)); err != nil {
			t.Fatalf("insert brief: %v", err)
		}
	}

	s := &DailyRecapService{DB: db}
	focus := s.fetchFocusBlocks(alice.ID, day)
	if len(focus) != 1 || focus[0].Start != "09:00" {
		t.Errorf("alice's focus blocks = %+v, want only her 09:00 block", focus)
	}
	habits := s.fetchHabits(alice.ID, day)
	if len(habits) != 1 || habits[0].Title != "Alice habit" || habits[0].WindowStart != "11:00" || habits[0].WindowEnd != "11:30" {
		t.Errorf("alice's habits = %+v, want only her 11:00-11:30 habit", habits)
	}
	meetings := s.fetchMeetings(context.Background(), alice.ID, day, day.Add(24*time.Hour))
	if len(meetings) != 1 || meetings[0].ID != "evt-recap-alice-brief" {
		t.Errorf("alice's meetings = %+v, want only her brief", meetings)
	}
}
