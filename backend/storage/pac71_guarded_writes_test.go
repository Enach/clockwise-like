package storage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// PAC-71 (review of #242): every request-path write of account data runs
// under the in-request re-check. With a session guard whose generation is no
// longer the account's, every guarded writer refuses before writing; with a
// current one, it writes as the plain function does.
func TestPAC71_EveryGuardedWriterRefusesAStaleSession(t *testing.T) {
	db := openTestDB(t)
	u := pac71User(t, db, "stale-writer@example.com", "")
	mustExec(t, db, `UPDATE users SET session_generation = 1 WHERE id = $1`, u)
	g := &SessionGuard{UserID: u, Gen: 0}
	ctx := WithSessionGuard(context.Background(), g)
	id, n := uuid.New(), int64(1)
	now := time.Now()
	errs := map[string]error{}
	add := func(name string, err error) { errs[name] = err }
	add("AcceptTeamInvite", AcceptTeamInviteCtx(ctx, db, id))
	_, err := AddLinkHostCtx(ctx, db, id, u, "pending")
	add("AddLinkHost", err)
	add("AddTeamMember", AddTeamMemberCtx(ctx, db, id, u, "member"))
	_, _, _, err = ConfirmManagerTeamMembersCtx(ctx, db, u, id, []string{"x@y.test"})
	add("ConfirmManagerTeamMembers", err)
	_, err = CreateHabitCtx(ctx, db, &Habit{UserID: u, Title: "h", DurationMinutes: 30})
	add("CreateHabit", err)
	_, err = CreateNoMeetingZoneCtx(ctx, db, id, 1, "09:00", "10:00", "z")
	add("CreateNoMeetingZone", err)
	_, err = CreateSchedulingLinkCtx(ctx, db, &SchedulingLink{OwnerUserID: u})
	add("CreateSchedulingLink", err)
	_, err = CreateTeamInviteCtx(ctx, db, id, "x@y.test", u)
	add("CreateTeamInvite", err)
	add("DeactivateHabit", DeactivateHabitCtx(ctx, db, id))
	add("DeleteFocusBlock", DeleteFocusBlockCtx(ctx, db, u, "e"))
	add("DeleteManagerTeamMemberByEmail", DeleteManagerTeamMemberByEmailCtx(ctx, db, u, "x@y.test"))
	add("DeleteNoMeetingZone", DeleteNoMeetingZoneCtx(ctx, db, id, id))
	add("DeletePersonalBlocker", DeletePersonalBlockerCtx(ctx, db, n, "e"))
	_, err = DeletePersonalCalendarForUserCtx(ctx, db, n, u)
	add("DeletePersonalCalendarForUser", err)
	add("DeleteSchedulingLink", DeleteSchedulingLinkCtx(ctx, db, id))
	add("DeleteTeam", DeleteTeamCtx(ctx, db, id))
	add("DeleteWorkspaceConnection", DeleteWorkspaceConnectionCtx(ctx, db, u, "slack"))
	_, _, err = InviteLinkHostCtx(ctx, db, id, u)
	add("InviteLinkHost", err)
	add("MarkPersonalCalendarSynced", MarkPersonalCalendarSyncedCtx(ctx, db, n, u))
	add("PatchManagerTeamMember", PatchManagerTeamMemberCtx(ctx, db, u, "x@y.test", "X", "weekly", nil))
	add("PatchManagerTeamMemberForTeam", PatchManagerTeamMemberForTeamCtx(ctx, db, u, id, "x@y.test", "X", "weekly", nil))
	_, err = RecordCredentialLossCtx(ctx, db, u, CredentialGoogle)
	add("RecordCredentialLoss", err)
	_, err = RemoveLinkHostCtx(ctx, db, id, u)
	add("RemoveLinkHost", err)
	add("RemoveLinkHostsExcept", RemoveLinkHostsExceptCtx(ctx, db, id, u, nil))
	_, err = RemoveMicrosoftCredentialCtx(ctx, db, u)
	add("RemoveMicrosoftCredential", err)
	add("RemoveTeamMember", RemoveTeamMemberCtx(ctx, db, id, u))
	add("RemoveTeamMemberKeepingOwner", RemoveTeamMemberKeepingOwnerCtx(ctx, db, id, u))
	_, err = RemoveZoomCredentialCtx(ctx, db, u)
	add("RemoveZoomCredential", err)
	add("RenameTeam", RenameTeamCtx(ctx, db, id, "n"))
	add("SaveFocusBlock", SaveFocusBlockCtx(ctx, db, u, "e", "2030-01-07", now, now.Add(time.Hour)))
	add("SaveRecapSettingsForUser", SaveRecapSettingsForUserCtx(ctx, db, u, &Settings{}))
	add("SaveZoomCredential", SaveZoomCredentialCtx(ctx, db, u, ZoomCredential{AccessToken: "z"}))
	add("UnassignManagerTeamMember", UnassignManagerTeamMemberCtx(ctx, db, u, id, "x@y.test"))
	_, err = UpdateHabitCtx(ctx, db, id, &Habit{})
	add("UpdateHabit", err)
	_, err = UpdateHabitOccurrenceStatusCtx(ctx, db, id, id, u, "done")
	add("UpdateHabitOccurrenceStatus", err)
	add("UpdateMicrosoftCredential", UpdateMicrosoftCredentialCtx(ctx, db, u, "{}"))
	_, err = UpdateNoMeetingZoneCtx(ctx, db, id, id, 1, "09:00", "10:00", "z")
	add("UpdateNoMeetingZone", err)
	_, err = UpdatePersonalCalendarCtx(ctx, db, n, u, nil, nil, nil)
	add("UpdatePersonalCalendar", err)
	_, err = UpdateSchedulingLinkCtx(ctx, db, id, &SchedulingLink{})
	add("UpdateSchedulingLink", err)
	add("UpdateZoomCredential", UpdateZoomCredentialCtx(ctx, db, u, ZoomCredential{}))
	_, err = UpsertAnalyticsWeekCtx(ctx, db, &AnalyticsWeek{UserID: u})
	add("UpsertAnalyticsWeek", err)
	_, err = UpsertHabitOccurrenceCtx(ctx, db, &HabitOccurrence{})
	add("UpsertHabitOccurrence", err)
	add("UpsertManagerTeamMember", UpsertManagerTeamMemberCtx(ctx, db, &ManagerTeamMember{ManagerUserID: u}))
	add("UpsertManagerTeamMemberForTeam", UpsertManagerTeamMemberForTeamCtx(ctx, db, id, &ManagerTeamMember{ManagerUserID: u}))
	add("UpsertMeetingBrief", UpsertMeetingBriefCtx(ctx, db, &MeetingBrief{UserID: u}))
	add("UpsertOneOnOneOccurrence", UpsertOneOnOneOccurrenceCtx(ctx, db, u, "x@y.test", "e", now))
	add("UpsertPersonalBlocker", UpsertPersonalBlockerCtx(ctx, db, n, "p", "w"))
	add("UpsertTeamAnalytics", UpsertTeamAnalyticsCtx(ctx, db, &TeamAnalyticsRow{ManagerUserID: u}))
	add("UpsertUserProfile", UpsertUserProfileCtx(ctx, db, &UserProfile{UserID: u, IsManager: true}))
	add("SaveSettingsForUser", SaveSettingsForUserCtx(ctx, db, u, &Settings{}))
	add("UpsertWorkspaceConnection", UpsertWorkspaceConnectionCtx(ctx, db, &WorkspaceConnection{UserID: u, Provider: "slack", AccessToken: "x"}))
	_, err = InsertPersonalCalendarCtx(ctx, db, &PersonalCalendar{UserID: u})
	add("InsertPersonalCalendar", err)
	_, err = CreateTeamCtx(ctx, db, "t", u)
	add("CreateTeam", err)
	_, err = RespondToHostInviteCtx(ctx, db, id, u, "accepted")
	add("RespondToHostInvite", err)
	_, _, err = ConsumeSSOStepupGrant(ctx, db, u, id, SSOGrantPurposeSSOWrite, now)
	add("ConsumeSSOStepupGrant", err)
	add("DeleteOrgAIConfirmations", DeleteOrgAIConfirmations(ctx, db, u))
	WriteAuditLogCtx(ctx, db, u, "focus_created", "{}")

	for name, err := range errs {
		if !errors.Is(err, ErrStaleSession) {
			t.Errorf("%s: err = %v, want ErrStaleSession", name, err)
		}
	}
	if !g.Stale() {
		t.Fatal("the guard was not marked stale")
	}
	if k, err := FirstNonEmptyKind(db, u); err != nil || k != "" {
		t.Fatalf("a stale session stored %q (%v)", k, err)
	}
}

// With a current guard the guarded writers write, and a function with its
// own transaction joins the re-check's.
func TestPAC71_GuardedWritersWriteForACurrentSession(t *testing.T) {
	db := openTestDB(t)
	u := pac71User(t, db, "current-writer@example.com", "")
	ctx := WithSessionGuard(context.Background(), &SessionGuard{UserID: u, Gen: 0})
	team, err := CreateTeamCtx(ctx, db, "t", u)
	if err != nil {
		t.Fatal(err)
	}
	if err := AddTeamMemberCtx(ctx, db, team.ID, u, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := RenameTeamCtx(ctx, db, team.ID, "renamed"); err != nil {
		t.Fatal(err)
	}
	if err := RemoveTeamMemberKeepingOwnerCtx(ctx, db, team.ID, u); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("joined transaction: %v", err)
	}
	if err := SaveZoomCredentialCtx(ctx, db, u, ZoomCredential{AccessToken: "z", RefreshToken: "r"}); err != nil {
		t.Fatal(err)
	}
	if c, _ := LoadZoomCredentialCtx(ctx, db, u); c == nil || c.AccessToken != "z" {
		t.Fatalf("zoom = %+v", c)
	}
	if removed, err := RemoveZoomCredentialCtx(ctx, db, u); err != nil || removed == "" {
		t.Fatal(removed, err)
	}
	if err := UpsertUserProfileCtx(ctx, db, &UserProfile{UserID: u, IsManager: true, AnalyticsSharedWithManager: true}); err != nil {
		t.Fatal(err)
	}
	WriteAuditLogCtx(ctx, db, u, "focus_created", "{}")
	var audits int
	_ = db.QueryRow(`SELECT count(*) FROM audit_log WHERE user_id = $1`, u).Scan(&audits)
	if audits != 1 {
		t.Fatalf("audit rows = %d", audits)
	}
	if k, _ := FirstNonEmptyKind(db, u); k == "" {
		t.Fatal("nothing was written")
	}
}
