package engine

import (
	"context"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/storage"
)

// PAC-54: GetGaps works over the roster it is given, and a member never met
// is a gap now rather than always one cadence interval away.
func TestGetGaps_RosterGiven(t *testing.T) {
	db := openTestDB(t)
	manager, err := storage.UpsertUser(db, "pac54-manager@example.com", "Manager", "", "google", "pac54-manager")
	if err != nil {
		t.Fatalf("upsert manager: %v", err)
	}
	now := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	e := &ManagerEngine{DB: db, Clock: FixedClock{T: now}}

	recent := now.AddDate(0, 0, -2)
	overdue := now.AddDate(0, 0, -10)
	member := func(email, cadence string, last *time.Time) *storage.ManagerTeamMember {
		return &storage.ManagerTeamMember{ManagerUserID: manager.ID, MemberEmail: email, DisplayName: email, Cadence: cadence, LastOneOnOneAt: last}
	}
	roster := []*storage.ManagerTeamMember{
		member("never-met@example.com", "weekly", nil),
		member("overdue@example.com", "weekly", &overdue),
		member("recent@example.com", "monthly", &recent),
		member("no-cadence@example.com", "none", nil),
		member("booked@example.com", "weekly", nil),
	}
	if _, err := db.Exec(`INSERT INTO one_on_one_occurrences (manager_user_id, member_email, calendar_event_id, occurred_at)
		VALUES ($1, 'booked@example.com', 'evt-pac54', $2)`, manager.ID, now.AddDate(0, 0, 3)); err != nil {
		t.Fatalf("insert occurrence: %v", err)
	}

	gaps, err := e.GetGaps(context.Background(), manager.ID, roster)
	if err != nil {
		t.Fatalf("GetGaps: %v", err)
	}
	if len(gaps) != 2 {
		t.Fatalf("gaps = %+v, want overdue then never-met", gaps)
	}
	if gaps[0].MemberEmail != "overdue@example.com" || gaps[0].DaysOverdue != 3 {
		t.Errorf("first gap = %+v, want overdue@example.com 3 days overdue", gaps[0])
	}
	if gaps[1].MemberEmail != "never-met@example.com" || !gaps[1].NextExpectedBy.Equal(now) || gaps[1].DaysOverdue != 0 || gaps[1].LastOneOnOneAt != nil {
		t.Errorf("second gap = %+v, want never-met@example.com due now", gaps[1])
	}

	if gaps, err := e.GetGaps(context.Background(), manager.ID, nil); err != nil || len(gaps) != 0 {
		t.Errorf("empty roster: gaps=%+v err=%v, want none", gaps, err)
	}
}
