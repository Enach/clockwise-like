package scheduler

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/internal/lifecycle/lifecycletest"
	"github.com/google/uuid"
)

// PAC-122 SF-098: the focus cron reports each failure, with each day's
// error rather than their count, through the lifecycle hook. The watermark
// still advances on a failed run (pinned by
// TestFocusCron_FailedRunAdvancesAndOthersRun).
func TestPAC122_FocusCronReportsEachFailure(t *testing.T) {
	rec := lifecycletest.Record(t)
	db := openTestDB(t)
	a := seedUser(t, db, "pac122-focus-a@example.com")
	b := seedUser(t, db, "pac122-focus-b@example.com")
	enable(t, db, a, at(6, 30), "0 7 * * *", "Europe/Paris")
	enable(t, db, b, at(6, 30), "0 7 * * *", "Europe/Paris")
	fc, logs := newTestCron(db, func(_ context.Context, u uuid.UUID, _ time.Time) (*engine.FocusRunResult, error) {
		if u == a {
			return nil, fmt.Errorf("not authenticated")
		}
		return &engine.FocusRunResult{Errors: []string{"2026-07-01: create event: 503", "2026-07-02: save block: db down"}}, nil
	})
	fc.tick(at(7, 0))

	if got := rec.Matching("cron", "run for user "+a.String()+" failed: not authenticated"); len(got) != 1 || got[0].Unit.ID != "focus-scheduler" {
		t.Errorf("run failure reports %+v", rec.All())
	}
	for _, day := range []string{"2026-07-01: create event: 503", "2026-07-02: save block: db down"} {
		if got := rec.Matching("cron", b.String()+": day error: "+day); len(got) != 1 {
			t.Errorf("day error %q: reports %+v", day, rec.All())
		}
		if logs.mentioning(day) != 1 {
			t.Errorf("day error %q not logged", day)
		}
	}
	for _, r := range rec.All() {
		if strings.Contains(r.Err.Error(), "@example.com") {
			t.Fatalf("report carries an email address: %v", r.Err)
		}
	}
	for _, u := range []uuid.UUID{a, b} {
		if w := watermark(t, db, u); w == nil || !w.Equal(at(7, 0)) {
			t.Errorf("watermark = %v, want the 07:00 tick", w)
		}
	}
}

// PAC-122 SF-098: a stored schedule the loop cannot use is reported, not
// only logged.
func TestPAC122_FocusCronReportsUnusableStoredValue(t *testing.T) {
	rec := lifecycletest.Record(t)
	db := openTestDB(t)
	a := seedUser(t, db, "pac122-focus-tz@example.com")
	enable(t, db, a, at(6, 30), "0 7 * * *", "Europe/Paris")
	if _, err := db.Exec(`UPDATE settings SET timezone = 'Mars/Olympus' WHERE user_id = $1`, a); err != nil {
		t.Fatal(err)
	}
	fc, _ := newTestCron(db, (&recorder{}).run)
	fc.tick(at(7, 0))
	if got := rec.Matching("cron", "stored timezone"); len(got) != 1 {
		t.Fatalf("reports %+v", rec.All())
	}
}
