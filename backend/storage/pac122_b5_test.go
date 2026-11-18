package storage

// PAC-122 phase 2, batch 5, SF-080: stored JSON that does not decode, and
// the occurrence date, no longer read back as empty or as the zero date.

import (
	"strings"
	"testing"
	"time"
)

// SF-080: an occurrence's scheduled_date is its date. It was scanned into a
// string the driver filled with RFC3339 text, the bare-date parse failed
// with its error discarded, and every occurrence read back as 0001-01-01.
func TestPAC122_SF080_OccurrenceDateReadsBack(t *testing.T) {
	db := openTestDB(t)
	u, err := UpsertUser(db, "sf080-date@pac122.test", "D", "", "google", "sf080-date")
	if err != nil {
		t.Fatal(err)
	}
	h, err := CreateHabit(db, &Habit{UserID: u.ID, Title: "x", DurationMinutes: 30, DaysOfWeek: []int{3},
		WindowStart: "09:00", WindowEnd: "10:00", Priority: 50, Color: "#5B7FFF"})
	if err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC)
	want := day
	o, err := UpsertHabitOccurrence(db, &HabitOccurrence{HabitID: h.ID, ScheduledDate: day,
		StartTime: day.Add(9 * time.Hour), EndTime: day.Add(10 * time.Hour), Status: "scheduled"})
	if err != nil {
		t.Fatal(err)
	}
	if !o.ScheduledDate.Equal(want) {
		t.Fatalf("upsert read back %v, want %v", o.ScheduledDate, want)
	}
	list, err := ListHabitOccurrences(db, h.ID, day, day)
	if err != nil || len(list) != 1 || !list[0].ScheduledDate.Equal(want) {
		t.Fatalf("list = %+v, %v; want one occurrence on %v", list, err, want)
	}
}

// SF-080: top_meeting_titles that do not decode are an error naming the
// week's row, not a week with no titles. A JSONB null is still [] (PAC-28).
func TestPAC122_SF080_UndecodableMeetingTitlesAreAnError(t *testing.T) {
	db := openTestDB(t)
	u, err := UpsertUser(db, "sf080-titles@pac122.test", "T", "", "google", "sf080-titles")
	if err != nil {
		t.Fatal(err)
	}
	week := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	a, err := UpsertAnalyticsWeek(db, &AnalyticsWeek{UserID: u.ID, WeekStart: week, TopMeetingTitles: []MeetingTitle{{Title: "Standup", DurationMinutes: 15}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE analytics_weeks SET top_meeting_titles = 'null'::jsonb WHERE id = $1`, a.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := GetAnalyticsWeek(db, u.ID, week); err != nil || got == nil || got.TopMeetingTitles == nil || len(got.TopMeetingTitles) != 0 {
		t.Fatalf("null titles = %+v, %v; want []", got, err)
	}
	if _, err := db.Exec(`UPDATE analytics_weeks SET top_meeting_titles = '{"title":"x"}'::jsonb WHERE id = $1`, a.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := GetAnalyticsWeek(db, u.ID, week); err == nil || !strings.Contains(err.Error(), a.ID.String()) {
		t.Fatalf("GetAnalyticsWeek = %+v, %v; want an error naming the row", got, err)
	}
	if got, err := ListAnalyticsTrends(db, u.ID, 8); err == nil || !strings.Contains(err.Error(), "top_meeting_titles") {
		t.Fatalf("ListAnalyticsTrends = %+v, %v; want the decode error", got, err)
	}
}

// SF-080: brief results that do not decode are an error naming the brief,
// not a brief with no sources.
func TestPAC122_SF080_UndecodableBriefResultsAreAnError(t *testing.T) {
	db := openTestDB(t)
	u, err := UpsertUser(db, "sf080-brief@pac122.test", "B", "", "google", "sf080-brief")
	if err != nil {
		t.Fatal(err)
	}
	if err := UpsertMeetingBrief(db, &MeetingBrief{CalendarEventID: "evt-1", UserID: u.ID, GeneratedAt: time.Now().UTC(), Status: "ready",
		SlackResults: []SlackMessage{}, NotionResults: []NotionPage{}}); err != nil {
		t.Fatal(err)
	}
	b, err := GetMeetingBrief(db, u.ID, "evt-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, col := range []string{"slack_results", "notion_results"} {
		if _, err := db.Exec(`UPDATE meeting_briefs SET slack_results = '[]', notion_results = '[]' WHERE id = $1`, b.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`UPDATE meeting_briefs SET `+col+` = '{"x":1}'::jsonb WHERE id = $1`, b.ID); err != nil {
			t.Fatal(err)
		}
		if got, err := GetMeetingBrief(db, u.ID, "evt-1"); err == nil || !strings.Contains(err.Error(), col) || !strings.Contains(err.Error(), b.ID.String()) {
			t.Fatalf("%s: GetMeetingBrief = %+v, %v; want the decode error naming the brief", col, got, err)
		}
		if got, err := ListMeetingBriefsForUser(db, u.ID); err == nil || !strings.Contains(err.Error(), col) {
			t.Fatalf("%s: ListMeetingBriefsForUser = %+v, %v; want the decode error", col, got, err)
		}
	}
}
