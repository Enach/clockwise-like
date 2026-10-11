package engine

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	googlecalendar "google.golang.org/api/calendar/v3"
)

// PAC-28 / API-033: every response slice the engine hands to a handler must
// encode as `[]` when empty, never `null`. The frontend maps over these.

// assertJSONEmptyArrays marshals v and asserts each named key is the literal `[]`.
func assertJSONEmptyArrays(t *testing.T, v any, keys ...string) {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("unmarshal %s: %v", raw, err)
	}
	for _, k := range keys {
		got, ok := fields[k]
		if !ok {
			t.Errorf("key %q missing from %s", k, raw)
			continue
		}
		if string(got) != "[]" {
			t.Errorf("%s = %s, want []", k, got)
		}
	}
}

func seedFocusUser(t *testing.T, eng *FocusTimeEngine, dailyTarget int) uuid.UUID {
	t.Helper()
	userID := uuid.New()
	if _, err := eng.DB.Exec(`INSERT INTO users (id, email, name, created_at) VALUES ($1, $2, $2, NOW())`, userID, userID.String()+"@example.com"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := eng.DB.Exec(`INSERT INTO settings (user_id, work_start, work_end, timezone, focus_min_block_minutes, focus_max_block_minutes, focus_daily_target_minutes, updated_at)
		VALUES ($1, '09:00', '17:00', 'UTC', 30, 120, $2, NOW())`, userID, dailyTarget); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	return userID
}

func TestFocusRun_NothingCreated_EncodesEmptyArrays(t *testing.T) {
	db := openTestDB(t)
	eng := &FocusTimeEngine{DB: db, calOps: &mockCalOps{calID: "primary", events: []*googlecalendar.Event{}}}
	// A zero daily target means every day is already "met": all five days are
	// skipped, nothing is created and nothing errors.
	userID := seedFocusUser(t, eng, 0)

	result, err := eng.RunForUser(context.Background(), userID, time.Date(2025, 1, 6, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("RunForUser: %v", err)
	}
	assertJSONEmptyArrays(t, result, "createdBlocks", "errors")
}

func TestFocusRun_NothingSkipped_EncodesEmptyArrays(t *testing.T) {
	db := openTestDB(t)
	eng := &FocusTimeEngine{DB: db, calOps: &mockCalOps{calID: "primary", events: []*googlecalendar.Event{}}}
	// An empty calendar and a positive target: every day gets a block, so
	// no day is skipped and nothing errors.
	userID := seedFocusUser(t, eng, 120)

	result, err := eng.RunForUser(context.Background(), userID, time.Date(2025, 1, 6, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("RunForUser: %v", err)
	}
	assertJSONEmptyArrays(t, result, "skippedDays", "errors")
}

func TestCompressionEngine_SuggestForDay_NoProposals_EncodesEmptyArray(t *testing.T) {
	db := openTestDB(t)
	eng := &CompressionEngine{DB: db, calOps: &mockCalOps{calID: "primary", events: []*googlecalendar.Event{}}}

	result, err := eng.SuggestForDay(context.Background(), time.Date(2025, 1, 6, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("SuggestForDay: %v", err)
	}
	assertJSONEmptyArrays(t, result, "proposals")
}

func TestScheduleSuggestions_NoCandidates_EncodesEmptyArray(t *testing.T) {
	assertJSONEmptyArrays(t, &ScheduleSuggestions{Slots: pickTopUnique(nil, 3, time.Hour)}, "slots")
}

func TestGetTeamAnalytics_NoMembers_EncodesEmptyArray(t *testing.T) {
	db := openTestDB(t)
	eng := &TeamAvailabilityEngine{DB: db}

	analytics, err := eng.GetTeamAnalytics(context.Background(), uuid.New(), time.Date(2025, 1, 6, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("GetTeamAnalytics: %v", err)
	}
	assertJSONEmptyArrays(t, analytics, "member_breakdown")
}
