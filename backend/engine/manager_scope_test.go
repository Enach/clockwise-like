package engine

import (
	"context"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

// PAC-60: a Paceday user's weekly minutes are visible only to an owner of a
// team that user is on, whatever route put them on the caller's 1:1 list.

type scopeFixture struct {
	eng       *ManagerEngine
	owner     *storage.User
	report    *storage.User
	stranger  *storage.User
	weekStart time.Time
}

func newScopeFixture(t *testing.T) *scopeFixture {
	t.Helper()
	db := openTestDB(t)
	mk := func(email string) *storage.User {
		u, err := storage.UpsertUser(db, email, email, "", "google", email)
		if err != nil {
			t.Fatalf("upsert user %s: %v", email, err)
		}
		return u
	}
	owner := mk("owner@scope.test")
	report := mk("report@scope.test")
	stranger := mk("stranger@scope.test")

	team, err := storage.CreateTeam(db, "Scope team", owner.ID)
	if err != nil {
		t.Fatalf("create team: %v", err)
	}
	if err := storage.AddTeamMember(db, team.ID, owner.ID, "owner"); err != nil {
		t.Fatalf("add owner: %v", err)
	}
	if err := storage.AddTeamMember(db, team.ID, report.ID, "member"); err != nil {
		t.Fatalf("add report: %v", err)
	}

	week := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	for _, u := range []*storage.User{owner, report, stranger} {
		if _, err := storage.UpsertAnalyticsWeek(db, &storage.AnalyticsWeek{
			UserID: u.ID, WeekStart: week, FocusMinutes: 615, MeetingMinutes: 240, FreeMinutes: 1545,
		}); err != nil {
			t.Fatalf("seed analytics for %s: %v", u.Email, err)
		}
		// Sharing on, as for any user who has a profile: the leak needs no
		// unusual setting on the victim's side.
		if err := storage.UpsertUserProfile(db, &storage.UserProfile{UserID: u.ID, AnalyticsSharedWithManager: true}); err != nil {
			t.Fatalf("seed profile for %s: %v", u.Email, err)
		}
	}
	return &scopeFixture{eng: &ManagerEngine{DB: db}, owner: owner, report: report, stranger: stranger, weekStart: week}
}

func (f *scopeFixture) memberFor(managerID uuid.UUID, u *storage.User) *storage.ManagerTeamMember {
	id := u.ID
	return &storage.ManagerTeamMember{ManagerUserID: managerID, MemberEmail: u.Email, MemberUserID: &id, Cadence: "weekly"}
}

func TestGetMemberWeek_TeamOwnerSeesTheirMember(t *testing.T) {
	f := newScopeFixture(t)
	got, err := f.eng.GetMemberWeek(context.Background(), f.owner.ID, f.memberFor(f.owner.ID, f.report), f.weekStart)
	if err != nil {
		t.Fatalf("GetMemberWeek: %v", err)
	}
	if !got.DataAvailable || got.FocusMinutes != 615 || got.MeetingMinutes != 240 {
		t.Fatalf("owner should see the report's week, got %+v", got)
	}
}

func TestGetMemberWeek_NoSharedTeamSeesNothing(t *testing.T) {
	f := newScopeFixture(t)
	// The stranger put the report's email on their own 1:1 list (no team).
	got, err := f.eng.GetMemberWeek(context.Background(), f.stranger.ID, f.memberFor(f.stranger.ID, f.report), f.weekStart)
	if err != nil {
		t.Fatalf("GetMemberWeek: %v", err)
	}
	if got.DataAvailable || got.FocusMinutes != 0 || got.MeetingMinutes != 0 || got.FreeMinutes != 0 {
		t.Fatalf("a user with no shared team must see nothing, got %+v", got)
	}
}

func TestGetMemberWeek_OwnerCannotReadSomeoneOffTheirTeams(t *testing.T) {
	f := newScopeFixture(t)
	got, _ := f.eng.GetMemberWeek(context.Background(), f.owner.ID, f.memberFor(f.owner.ID, f.stranger), f.weekStart)
	if got.DataAvailable || got.FocusMinutes != 0 {
		t.Fatalf("a team owner must not see a user who is on none of their teams, got %+v", got)
	}
}

func TestGetMemberWeek_MemberCannotReadTheOwner(t *testing.T) {
	f := newScopeFixture(t)
	got, _ := f.eng.GetMemberWeek(context.Background(), f.report.ID, f.memberFor(f.report.ID, f.owner), f.weekStart)
	if got.DataAvailable || got.FocusMinutes != 0 {
		t.Fatalf("a team member is not an owner and must not see the owner's minutes, got %+v", got)
	}
}

func TestGetMemberWeek_IgnoresFiguresCachedBeforeTheGate(t *testing.T) {
	f := newScopeFixture(t)
	db := f.eng.DB
	// A cache row written for the stranger before PAC-60 must not be served.
	if err := storage.UpsertTeamAnalytics(db, &storage.TeamAnalyticsRow{
		ManagerUserID: f.stranger.ID, MemberEmail: f.report.Email, WeekStart: f.weekStart,
		FocusMinutes: 615, MeetingMinutes: 240, FreeMinutes: 1545, IsPacedayUser: true, DataAvailable: true,
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	got, _ := f.eng.GetMemberWeek(context.Background(), f.stranger.ID, f.memberFor(f.stranger.ID, f.report), f.weekStart)
	if got.DataAvailable || got.FocusMinutes != 0 {
		t.Fatalf("a cached figure from before the gate was served: %+v", got)
	}
}
