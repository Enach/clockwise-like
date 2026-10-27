package engine

import (
	"context"
	"encoding/json"
	"reflect"
	"regexp"
	"sort"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/storage"
	googlecalendar "google.golang.org/api/calendar/v3"
)

// PAC-26 AC-2 on the real engine, plus U-23: a run that creates a block emits
// it as {google_event_id, start_time, end_time, date}, and skippedDays holds
// YYYY-MM-DD strings from both append sites (a day already at target and a
// disabled day).
func TestPAC26FocusRunBlockKeysAndSkippedDayFormat(t *testing.T) {
	db := openTestDB(t)
	eng := &FocusTimeEngine{DB: db, calOps: &mockCalOps{calID: "primary", events: []*googlecalendar.Event{}}}
	userID := seedFocusUser(t, eng, 60)

	// Tuesday is disabled: by_day mode with no Tuesday entry.
	if _, err := db.Exec(`UPDATE settings SET working_hours=$2::jsonb WHERE user_id=$1`, userID,
		`{"mode":"by_day","default":{"enabled":true,"start":"09:00","end":"17:00"},"days":{
			"monday":{"enabled":true,"start":"09:00","end":"17:00"},
			"wednesday":{"enabled":true,"start":"09:00","end":"17:00"},
			"thursday":{"enabled":true,"start":"09:00","end":"17:00"},
			"friday":{"enabled":true,"start":"09:00","end":"17:00"}}}`); err != nil {
		t.Fatalf("set working hours: %v", err)
	}
	// Monday is already at target.
	monday := time.Date(2025, 1, 6, 0, 0, 0, 0, time.UTC)
	if err := storage.SaveFocusBlock(db, userID, "existing", "2025-01-06", monday.Add(9*time.Hour), monday.Add(10*time.Hour)); err != nil {
		t.Fatalf("seed block: %v", err)
	}

	result, err := eng.RunForUser(context.Background(), userID, monday)
	if err != nil {
		t.Fatalf("RunForUser: %v", err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var body struct {
		CreatedBlocks []map[string]json.RawMessage `json:"createdBlocks"`
		SkippedDays   []string                     `json:"skippedDays"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.CreatedBlocks) == 0 {
		t.Fatalf("no block created: %s", raw)
	}
	for _, b := range body.CreatedBlocks {
		keys := make([]string, 0, len(b))
		for k := range b {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if want := []string{"date", "end_time", "google_event_id", "start_time"}; !reflect.DeepEqual(keys, want) {
			t.Fatalf("createdBlocks item keys = %v, want %v; body %s", keys, want, raw)
		}
	}
	if want := []string{"2025-01-06", "2025-01-07"}; !reflect.DeepEqual(body.SkippedDays, want) {
		t.Fatalf("skippedDays = %v, want %v (at target, then disabled)", body.SkippedDays, want)
	}
	dateRE := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	for _, d := range body.SkippedDays {
		if !dateRE.MatchString(d) {
			t.Fatalf("skippedDays entry %q is not YYYY-MM-DD", d)
		}
	}
}
