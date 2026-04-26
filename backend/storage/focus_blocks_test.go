package storage

import (
	"testing"
	"time"
)

func TestSaveFocusBlock(t *testing.T) {
	db := openTestDB(t)

	start := time.Date(2025, 1, 6, 9, 0, 0, 0, time.UTC)
	end := time.Date(2025, 1, 6, 10, 0, 0, 0, time.UTC)

	if err := SaveFocusBlock(db, "evt-001", "2025-01-06", start, end); err != nil {
		t.Fatalf("SaveFocusBlock: %v", err)
	}
}

func TestSaveFocusBlock_Idempotent(t *testing.T) {
	db := openTestDB(t)

	start := time.Date(2025, 1, 6, 9, 0, 0, 0, time.UTC)
	end := time.Date(2025, 1, 6, 10, 0, 0, 0, time.UTC)

	if err := SaveFocusBlock(db, "evt-001", "2025-01-06", start, end); err != nil {
		t.Fatal(err)
	}
	// Second insert should be ignored (INSERT OR IGNORE)
	if err := SaveFocusBlock(db, "evt-001", "2025-01-06", start, end); err != nil {
		t.Fatalf("duplicate save should not error: %v", err)
	}
}

func TestListFocusBlocksForWeek(t *testing.T) {
	db := openTestDB(t)

	monday := time.Date(2025, 1, 6, 0, 0, 0, 0, time.UTC)
	start1 := monday.Add(9 * time.Hour)
	end1 := monday.Add(10 * time.Hour)
	start2 := monday.AddDate(0, 0, 1).Add(9 * time.Hour)
	end2 := monday.AddDate(0, 0, 1).Add(10 * time.Hour)

	SaveFocusBlock(db, "evt-001", "2025-01-06", start1, end1)
	SaveFocusBlock(db, "evt-002", "2025-01-07", start2, end2)

	// Outside week
	outStart := monday.AddDate(0, 0, 7).Add(9 * time.Hour)
	outEnd := monday.AddDate(0, 0, 7).Add(10 * time.Hour)
	SaveFocusBlock(db, "evt-003", "2025-01-13", outStart, outEnd)

	blocks, err := ListFocusBlocksForWeek(db, monday)
	if err != nil {
		t.Fatalf("ListFocusBlocksForWeek: %v", err)
	}
	if len(blocks) != 2 {
		t.Errorf("got %d blocks, want 2", len(blocks))
	}
}

func TestDeleteFocusBlock(t *testing.T) {
	db := openTestDB(t)

	start := time.Date(2025, 1, 6, 9, 0, 0, 0, time.UTC)
	end := time.Date(2025, 1, 6, 10, 0, 0, 0, time.UTC)
	SaveFocusBlock(db, "evt-001", "2025-01-06", start, end)

	if err := DeleteFocusBlock(db, "evt-001"); err != nil {
		t.Fatalf("DeleteFocusBlock: %v", err)
	}

	blocks, _ := ListFocusBlocksForWeek(db, time.Date(2025, 1, 6, 0, 0, 0, 0, time.UTC))
	if len(blocks) != 0 {
		t.Errorf("expected 0 blocks after delete, got %d", len(blocks))
	}
}

func TestFocusMinutesForDay(t *testing.T) {
	db := openTestDB(t)

	start := time.Date(2025, 1, 6, 9, 0, 0, 0, time.UTC)
	end := time.Date(2025, 1, 6, 10, 30, 0, 0, time.UTC)
	SaveFocusBlock(db, "evt-001", "2025-01-06", start, end)

	mins, err := FocusMinutesForDay(db, "2025-01-06")
	if err != nil {
		t.Fatalf("FocusMinutesForDay: %v", err)
	}
	if mins != 90 {
		t.Errorf("got %d minutes, want 90", mins)
	}

	// Empty day returns 0
	mins2, err := FocusMinutesForDay(db, "2025-01-07")
	if err != nil {
		t.Fatal(err)
	}
	if mins2 != 0 {
		t.Errorf("got %d minutes for empty day, want 0", mins2)
	}
}

func TestWriteAuditLog(t *testing.T) {
	db := openTestDB(t)
	// Should not panic
	WriteAuditLog(db, "test_action", `{"key":"value"}`)
	WriteAuditLog(db, "another_action", "")
}
