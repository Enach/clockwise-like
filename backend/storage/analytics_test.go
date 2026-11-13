package storage

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// PAC-28 / API-033: a week with no meetings stores `null` in the JSONB column
// (json.Marshal of a nil slice); reading it back must still encode `[]`.
func TestAnalyticsWeek_NoMeetingTitles_EncodesEmptyArray(t *testing.T) {
	db := openTestDB(t)
	owner := mustUser(t, db, "pac28-analytics@example.com")
	week := time.Date(2025, 1, 6, 0, 0, 0, 0, time.UTC)

	upserted, err := UpsertAnalyticsWeek(db, &AnalyticsWeek{UserID: owner, WeekStart: week})
	if err != nil {
		t.Fatalf("UpsertAnalyticsWeek: %v", err)
	}
	got, err := GetAnalyticsWeek(db, owner, week)
	if err != nil || got == nil {
		t.Fatalf("GetAnalyticsWeek: %v, %v", got, err)
	}
	trends, err := ListAnalyticsTrends(db, owner, 4)
	if err != nil || len(trends) != 1 {
		t.Fatalf("ListAnalyticsTrends: %v, %v", trends, err)
	}

	for name, a := range map[string]*AnalyticsWeek{"upsert": upserted, "get": got, "trends": trends[0]} {
		raw, err := json.Marshal(a)
		if err != nil {
			t.Fatalf("%s: marshal: %v", name, err)
		}
		if !strings.Contains(string(raw), `"top_meeting_titles":[]`) {
			t.Errorf("%s: want \"top_meeting_titles\":[], got %s", name, raw)
		}
	}
}

// PAC-42 §2.3: the DATE column arrives from pgx as a time.Time, so the week start
// must be read back as the stored Monday on all three paths, never the zero time.
// The stored values the team analytics path reads (meeting/focus minutes, focus
// score) and the 0..1 habit completion rate (spec D-3) come back unchanged.
func TestAnalyticsWeek_WeekStartRoundTripsOnAllPaths(t *testing.T) {
	db := openTestDB(t)
	owner := mustUser(t, db, "pac42-storage@example.com")
	week := time.Date(2025, 1, 6, 0, 0, 0, 0, time.UTC)
	in := &AnalyticsWeek{UserID: owner, WeekStart: week, MeetingMinutes: 615, FocusMinutes: 240,
		FocusScore: 28, HabitCompletionRate: 0.75}

	upserted, err := UpsertAnalyticsWeek(db, in)
	if err != nil || upserted == nil {
		t.Fatalf("UpsertAnalyticsWeek: %v, %v", upserted, err)
	}
	got, err := GetAnalyticsWeek(db, owner, week)
	if err != nil || got == nil {
		t.Fatalf("GetAnalyticsWeek: %v, %v", got, err)
	}
	trends, err := ListAnalyticsTrends(db, owner, 4)
	if err != nil || len(trends) != 1 {
		t.Fatalf("ListAnalyticsTrends: %v, %v", trends, err)
	}
	for name, a := range map[string]*AnalyticsWeek{"upsert": upserted, "get": got, "trends": trends[0]} {
		if !a.WeekStart.Equal(week) || a.WeekStart.Location() != time.UTC {
			t.Errorf("%s: WeekStart = %v, want %v (UTC)", name, a.WeekStart, week)
		}
		if a.MeetingMinutes != 615 || a.FocusMinutes != 240 || a.FocusScore != 28 || a.HabitCompletionRate != 0.75 {
			t.Errorf("%s: stored values changed: %+v", name, a)
		}
	}
}
