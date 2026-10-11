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
