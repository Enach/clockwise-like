// PAC-71 in-request re-check for the request-path writes of account data
// (contract §5 D1–D14; plan §4). Each XCtx is X run through
// WithSessionGeneration: under a session guard it runs in the transaction
// whose first statement re-checks the generation, and refuses with
// ErrStaleSession instead of writing; without one it is X.

package storage

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
)

// AcceptTeamInviteCtx is AcceptTeamInvite under the in-request re-check (PAC-71, contract Â§5).
func AcceptTeamInviteCtx(ctx context.Context, db *sql.DB, inviteID uuid.UUID) (err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		return AcceptTeamInvite(q, inviteID)
	})
	return err
}

// AddLinkHostCtx is AddLinkHost under the in-request re-check (PAC-71, contract Â§5).
func AddLinkHostCtx(ctx context.Context, db *sql.DB, linkID uuid.UUID, userID uuid.UUID, status string) (r0 *LinkHost, err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		var e error
		r0, e = AddLinkHost(q, linkID, userID, status)
		return e
	})
	return r0, err
}

// AddTeamMemberCtx is AddTeamMember under the in-request re-check (PAC-71, contract Â§5).
func AddTeamMemberCtx(ctx context.Context, db *sql.DB, teamID uuid.UUID, userID uuid.UUID, role string) (err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		return AddTeamMember(q, teamID, userID, role)
	})
	return err
}

// ConfirmManagerTeamMembersCtx is ConfirmManagerTeamMembers under the in-request re-check (PAC-71, contract Â§5).
func ConfirmManagerTeamMembersCtx(ctx context.Context, db *sql.DB, managerID uuid.UUID, teamID uuid.UUID, emails []string) (r0 int, r1 int, r2 int, err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		var e error
		r0, r1, r2, e = ConfirmManagerTeamMembers(q, managerID, teamID, emails)
		return e
	})
	return r0, r1, r2, err
}

// CreateHabitCtx is CreateHabit under the in-request re-check (PAC-71, contract Â§5).
func CreateHabitCtx(ctx context.Context, db *sql.DB, h *Habit) (r0 *Habit, err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		var e error
		r0, e = CreateHabit(q, h)
		return e
	})
	return r0, err
}

// CreateNoMeetingZoneCtx is CreateNoMeetingZone under the in-request re-check (PAC-71, contract Â§5).
func CreateNoMeetingZoneCtx(ctx context.Context, db *sql.DB, teamID uuid.UUID, dayOfWeek int, startTime string, endTime string, label string) (r0 *NoMeetingZone, err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		var e error
		r0, e = CreateNoMeetingZone(q, teamID, dayOfWeek, startTime, endTime, label)
		return e
	})
	return r0, err
}

// CreateSchedulingLinkCtx is CreateSchedulingLink under the in-request re-check (PAC-71, contract Â§5).
func CreateSchedulingLinkCtx(ctx context.Context, db *sql.DB, l *SchedulingLink) (r0 *SchedulingLink, err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		var e error
		r0, e = CreateSchedulingLink(q, l)
		return e
	})
	return r0, err
}

// CreateTeamInviteCtx is CreateTeamInvite under the in-request re-check (PAC-71, contract Â§5).
func CreateTeamInviteCtx(ctx context.Context, db *sql.DB, teamID uuid.UUID, inviteeEmail string, invitedBy uuid.UUID) (r0 *TeamInvite, err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		var e error
		r0, e = CreateTeamInvite(q, teamID, inviteeEmail, invitedBy)
		return e
	})
	return r0, err
}

// DeactivateHabitCtx is DeactivateHabit under the in-request re-check (PAC-71, contract Â§5).
func DeactivateHabitCtx(ctx context.Context, db *sql.DB, id uuid.UUID) (err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		return DeactivateHabit(q, id)
	})
	return err
}

// DeleteFocusBlockCtx is DeleteFocusBlock under the in-request re-check (PAC-71, contract Â§5).
func DeleteFocusBlockCtx(ctx context.Context, db *sql.DB, userID uuid.UUID, googleEventID string) (err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		return DeleteFocusBlock(q, userID, googleEventID)
	})
	return err
}

// DeleteManagerTeamMemberByEmailCtx is DeleteManagerTeamMemberByEmail under the in-request re-check (PAC-71, contract Â§5).
func DeleteManagerTeamMemberByEmailCtx(ctx context.Context, db *sql.DB, managerID uuid.UUID, email string) (err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		return DeleteManagerTeamMemberByEmail(q, managerID, email)
	})
	return err
}

// DeleteNoMeetingZoneCtx is DeleteNoMeetingZone under the in-request re-check (PAC-71, contract Â§5).
func DeleteNoMeetingZoneCtx(ctx context.Context, db *sql.DB, id uuid.UUID, teamID uuid.UUID) (err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		return DeleteNoMeetingZone(q, id, teamID)
	})
	return err
}

// DeletePersonalBlockerCtx is DeletePersonalBlocker under the in-request re-check (PAC-71, contract Â§5).
func DeletePersonalBlockerCtx(ctx context.Context, db *sql.DB, calID int64, personalEventID string) (err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		return DeletePersonalBlocker(q, calID, personalEventID)
	})
	return err
}

// DeletePersonalCalendarForUserCtx is DeletePersonalCalendarForUser under the in-request re-check (PAC-71, contract Â§5).
func DeletePersonalCalendarForUserCtx(ctx context.Context, db *sql.DB, id int64, userID uuid.UUID) (r0 bool, err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		var e error
		r0, e = DeletePersonalCalendarForUser(q, id, userID)
		return e
	})
	return r0, err
}

// DeleteSchedulingLinkCtx is DeleteSchedulingLink under the in-request re-check (PAC-71, contract Â§5).
func DeleteSchedulingLinkCtx(ctx context.Context, db *sql.DB, id uuid.UUID) (err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		return DeleteSchedulingLink(q, id)
	})
	return err
}

// DeleteTeamCtx is DeleteTeam under the in-request re-check (PAC-71, contract Â§5).
func DeleteTeamCtx(ctx context.Context, db *sql.DB, id uuid.UUID) (err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		return DeleteTeam(q, id)
	})
	return err
}

// DeleteWorkspaceConnectionCtx is DeleteWorkspaceConnection under the in-request re-check (PAC-71, contract Â§5).
func DeleteWorkspaceConnectionCtx(ctx context.Context, db *sql.DB, userID uuid.UUID, provider string) (err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		return DeleteWorkspaceConnection(q, userID, provider)
	})
	return err
}

// InviteLinkHostCtx is InviteLinkHost under the in-request re-check (PAC-71, contract Â§5).
func InviteLinkHostCtx(ctx context.Context, db *sql.DB, linkID uuid.UUID, userID uuid.UUID) (r0 *LinkHost, r1 bool, err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		var e error
		r0, r1, e = InviteLinkHost(q, linkID, userID)
		return e
	})
	return r0, r1, err
}

// MarkPersonalCalendarSyncedCtx is MarkPersonalCalendarSynced under the in-request re-check (PAC-71, contract Â§5).
func MarkPersonalCalendarSyncedCtx(ctx context.Context, db *sql.DB, id int64, userID uuid.UUID) (err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		return MarkPersonalCalendarSynced(q, id, userID)
	})
	return err
}

// PatchManagerTeamMemberCtx is PatchManagerTeamMember under the in-request re-check (PAC-71, contract Â§5).
func PatchManagerTeamMemberCtx(ctx context.Context, db *sql.DB, managerID uuid.UUID, email string, displayName string, cadence string, customDays *int) (err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		return PatchManagerTeamMember(q, managerID, email, displayName, cadence, customDays)
	})
	return err
}

// PatchManagerTeamMemberForTeamCtx is PatchManagerTeamMemberForTeam under the in-request re-check (PAC-71, contract Â§5).
func PatchManagerTeamMemberForTeamCtx(ctx context.Context, db *sql.DB, managerID uuid.UUID, teamID uuid.UUID, email string, displayName string, cadence string, customDays *int) (err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		return PatchManagerTeamMemberForTeam(q, managerID, teamID, email, displayName, cadence, customDays)
	})
	return err
}

// RecordCredentialLossCtx is RecordCredentialLoss under the in-request re-check (PAC-71, contract Â§5).
func RecordCredentialLossCtx(ctx context.Context, db *sql.DB, userID uuid.UUID, provider string) (r0 bool, err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		var e error
		r0, e = RecordCredentialLoss(q, userID, provider)
		return e
	})
	return r0, err
}

// RemoveLinkHostCtx is RemoveLinkHost under the in-request re-check (PAC-71, contract Â§5).
func RemoveLinkHostCtx(ctx context.Context, db *sql.DB, linkID uuid.UUID, userID uuid.UUID) (r0 bool, err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		var e error
		r0, e = RemoveLinkHost(q, linkID, userID)
		return e
	})
	return r0, err
}

// RemoveLinkHostsExceptCtx is RemoveLinkHostsExcept under the in-request re-check (PAC-71, contract Â§5).
func RemoveLinkHostsExceptCtx(ctx context.Context, db *sql.DB, linkID uuid.UUID, ownerID uuid.UUID, keep []uuid.UUID) (err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		return RemoveLinkHostsExcept(q, linkID, ownerID, keep)
	})
	return err
}

// RemoveMicrosoftCredentialCtx is RemoveMicrosoftCredential under the in-request re-check (PAC-71, contract Â§5).
func RemoveMicrosoftCredentialCtx(ctx context.Context, db *sql.DB, userID uuid.UUID) (r0 string, err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		var e error
		r0, e = RemoveMicrosoftCredential(q, userID)
		return e
	})
	return r0, err
}

// RemoveTeamMemberCtx is RemoveTeamMember under the in-request re-check (PAC-71, contract Â§5).
func RemoveTeamMemberCtx(ctx context.Context, db *sql.DB, teamID uuid.UUID, userID uuid.UUID) (err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		return RemoveTeamMember(q, teamID, userID)
	})
	return err
}

// RemoveTeamMemberKeepingOwnerCtx is RemoveTeamMemberKeepingOwner under the in-request re-check (PAC-71, contract Â§5).
func RemoveTeamMemberKeepingOwnerCtx(ctx context.Context, db *sql.DB, teamID uuid.UUID, userID uuid.UUID) (err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		return RemoveTeamMemberKeepingOwner(q, teamID, userID)
	})
	return err
}

// RemoveZoomCredentialCtx is RemoveZoomCredential under the in-request re-check (PAC-71, contract Â§5).
func RemoveZoomCredentialCtx(ctx context.Context, db *sql.DB, userID uuid.UUID) (r0 string, err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		var e error
		r0, e = RemoveZoomCredential(q, userID)
		return e
	})
	return r0, err
}

// RenameTeamCtx is RenameTeam under the in-request re-check (PAC-71, contract Â§5).
func RenameTeamCtx(ctx context.Context, db *sql.DB, id uuid.UUID, name string) (err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		return RenameTeam(q, id, name)
	})
	return err
}

// SaveFocusBlockCtx is SaveFocusBlock under the in-request re-check (PAC-71, contract Â§5).
func SaveFocusBlockCtx(ctx context.Context, db *sql.DB, userID uuid.UUID, googleEventID string, date string, start time.Time, end time.Time) (err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		return SaveFocusBlock(q, userID, googleEventID, date, start, end)
	})
	return err
}

// SaveRecapSettingsForUserCtx is SaveRecapSettingsForUser under the in-request re-check (PAC-71, contract Â§5).
func SaveRecapSettingsForUserCtx(ctx context.Context, db *sql.DB, userID uuid.UUID, s *Settings) (err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		return SaveRecapSettingsForUser(q, userID, s)
	})
	return err
}

// SaveZoomCredentialCtx is SaveZoomCredential under the in-request re-check (PAC-71, contract Â§5).
func SaveZoomCredentialCtx(ctx context.Context, db *sql.DB, userID uuid.UUID, c ZoomCredential) (err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		return SaveZoomCredential(q, userID, c)
	})
	return err
}

// UnassignManagerTeamMemberCtx is UnassignManagerTeamMember under the in-request re-check (PAC-71, contract Â§5).
func UnassignManagerTeamMemberCtx(ctx context.Context, db *sql.DB, managerID uuid.UUID, teamID uuid.UUID, email string) (err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		return UnassignManagerTeamMember(q, managerID, teamID, email)
	})
	return err
}

// UpdateHabitCtx is UpdateHabit under the in-request re-check (PAC-71, contract Â§5).
func UpdateHabitCtx(ctx context.Context, db *sql.DB, id uuid.UUID, h *Habit) (r0 *Habit, err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		var e error
		r0, e = UpdateHabit(q, id, h)
		return e
	})
	return r0, err
}

// UpdateHabitOccurrenceStatusCtx is UpdateHabitOccurrenceStatus under the in-request re-check (PAC-71, contract Â§5).
func UpdateHabitOccurrenceStatusCtx(ctx context.Context, db *sql.DB, occurrenceID uuid.UUID, habitID uuid.UUID, userID uuid.UUID, status string) (r0 *HabitOccurrence, err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		var e error
		r0, e = UpdateHabitOccurrenceStatus(q, occurrenceID, habitID, userID, status)
		return e
	})
	return r0, err
}

// UpdateMicrosoftCredentialCtx is UpdateMicrosoftCredential under the in-request re-check (PAC-71, contract Â§5).
func UpdateMicrosoftCredentialCtx(ctx context.Context, db *sql.DB, userID uuid.UUID, record string) (err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		return UpdateMicrosoftCredential(q, userID, record)
	})
	return err
}

// UpdateNoMeetingZoneCtx is UpdateNoMeetingZone under the in-request re-check (PAC-71, contract Â§5).
func UpdateNoMeetingZoneCtx(ctx context.Context, db *sql.DB, id uuid.UUID, teamID uuid.UUID, dayOfWeek int, startTime string, endTime string, label string) (r0 *NoMeetingZone, err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		var e error
		r0, e = UpdateNoMeetingZone(q, id, teamID, dayOfWeek, startTime, endTime, label)
		return e
	})
	return r0, err
}

// UpdatePersonalCalendarCtx is UpdatePersonalCalendar under the in-request re-check (PAC-71, contract Â§5).
func UpdatePersonalCalendarCtx(ctx context.Context, db *sql.DB, id int64, userID uuid.UUID, name *string, url *string, enabled *bool) (r0 *PersonalCalendar, err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		var e error
		r0, e = UpdatePersonalCalendar(q, id, userID, name, url, enabled)
		return e
	})
	return r0, err
}

// UpdateSchedulingLinkCtx is UpdateSchedulingLink under the in-request re-check (PAC-71, contract Â§5).
func UpdateSchedulingLinkCtx(ctx context.Context, db *sql.DB, id uuid.UUID, l *SchedulingLink) (r0 *SchedulingLink, err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		var e error
		r0, e = UpdateSchedulingLink(q, id, l)
		return e
	})
	return r0, err
}

// UpdateZoomCredentialCtx is UpdateZoomCredential under the in-request re-check (PAC-71, contract Â§5).
func UpdateZoomCredentialCtx(ctx context.Context, db *sql.DB, userID uuid.UUID, c ZoomCredential) (err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		return UpdateZoomCredential(q, userID, c)
	})
	return err
}

// UpsertAnalyticsWeekCtx is UpsertAnalyticsWeek under the in-request re-check (PAC-71, contract Â§5).
func UpsertAnalyticsWeekCtx(ctx context.Context, db *sql.DB, a *AnalyticsWeek) (r0 *AnalyticsWeek, err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		var e error
		r0, e = UpsertAnalyticsWeek(q, a)
		return e
	})
	return r0, err
}

// UpsertHabitOccurrenceCtx is UpsertHabitOccurrence under the in-request re-check (PAC-71, contract Â§5).
func UpsertHabitOccurrenceCtx(ctx context.Context, db *sql.DB, o *HabitOccurrence) (r0 *HabitOccurrence, err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		var e error
		r0, e = UpsertHabitOccurrence(q, o)
		return e
	})
	return r0, err
}

// UpsertManagerTeamMemberCtx is UpsertManagerTeamMember under the in-request re-check (PAC-71, contract Â§5).
func UpsertManagerTeamMemberCtx(ctx context.Context, db *sql.DB, m *ManagerTeamMember) (err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		return UpsertManagerTeamMember(q, m)
	})
	return err
}

// UpsertManagerTeamMemberForTeamCtx is UpsertManagerTeamMemberForTeam under the in-request re-check (PAC-71, contract Â§5).
func UpsertManagerTeamMemberForTeamCtx(ctx context.Context, db *sql.DB, teamID uuid.UUID, m *ManagerTeamMember) (err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		return UpsertManagerTeamMemberForTeam(q, teamID, m)
	})
	return err
}

// UpsertMeetingBriefCtx is UpsertMeetingBrief under the in-request re-check (PAC-71, contract Â§5).
func UpsertMeetingBriefCtx(ctx context.Context, db *sql.DB, b *MeetingBrief) (err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		return UpsertMeetingBrief(q, b)
	})
	return err
}

// UpsertOneOnOneOccurrenceCtx is UpsertOneOnOneOccurrence under the in-request re-check (PAC-71, contract Â§5).
func UpsertOneOnOneOccurrenceCtx(ctx context.Context, db *sql.DB, managerID uuid.UUID, memberEmail string, calendarEventID string, occurredAt time.Time) (err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		return UpsertOneOnOneOccurrence(q, managerID, memberEmail, calendarEventID, occurredAt)
	})
	return err
}

// UpsertPersonalBlockerCtx is UpsertPersonalBlocker under the in-request re-check (PAC-71, contract Â§5).
func UpsertPersonalBlockerCtx(ctx context.Context, db *sql.DB, calID int64, personalEventID string, workEventID string) (err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		return UpsertPersonalBlocker(q, calID, personalEventID, workEventID)
	})
	return err
}

// UpsertTeamAnalyticsCtx is UpsertTeamAnalytics under the in-request re-check (PAC-71, contract Â§5).
func UpsertTeamAnalyticsCtx(ctx context.Context, db *sql.DB, r *TeamAnalyticsRow) (err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		return UpsertTeamAnalytics(q, r)
	})
	return err
}

// UpsertUserProfileCtx is UpsertUserProfile under the in-request re-check (PAC-71, contract Â§5).
func UpsertUserProfileCtx(ctx context.Context, db *sql.DB, p *UserProfile) (err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		return UpsertUserProfile(q, p)
	})
	return err
}

// WriteAuditLogCtx is WriteAuditLog under the in-request re-check (PAC-71, contract Â§5).
func WriteAuditLogCtx(ctx context.Context, db *sql.DB, userID uuid.UUID, action string, details string) {
	_ = WithSessionGeneration(ctx, db, func(q Querier) error {
		WriteAuditLog(q, userID, action, details)
		return nil
	})
}
