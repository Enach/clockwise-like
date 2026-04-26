package storage

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
)

// ── UserProfile ───────────────────────────────────────────────────────────────

type UserProfile struct {
	UserID                     uuid.UUID
	IsManager                  bool
	DetectedAt                 *time.Time
	AnalyticsSharedWithManager bool
	UpdatedAt                  time.Time
}

func GetOrCreateUserProfile(db *sql.DB, userID uuid.UUID) (*UserProfile, error) {
	_, err := db.Exec(`
		INSERT INTO user_profiles (user_id) VALUES ($1)
		ON CONFLICT (user_id) DO NOTHING`, userID)
	if err != nil {
		return nil, err
	}
	return GetUserProfile(db, userID)
}

func GetUserProfile(db *sql.DB, userID uuid.UUID) (*UserProfile, error) {
	row := db.QueryRow(`
		SELECT user_id, is_manager, detected_at, analytics_shared_with_manager, updated_at
		FROM user_profiles WHERE user_id = $1`, userID)
	var p UserProfile
	if err := row.Scan(&p.UserID, &p.IsManager, &p.DetectedAt, &p.AnalyticsSharedWithManager, &p.UpdatedAt); err != nil {
		return nil, err
	}
	return &p, nil
}

func UpsertUserProfile(db *sql.DB, p *UserProfile) error {
	_, err := db.Exec(`
		INSERT INTO user_profiles (user_id, is_manager, detected_at, analytics_shared_with_manager, updated_at)
		VALUES ($1,$2,$3,$4,now())
		ON CONFLICT (user_id) DO UPDATE SET
			is_manager=EXCLUDED.is_manager,
			detected_at=EXCLUDED.detected_at,
			analytics_shared_with_manager=EXCLUDED.analytics_shared_with_manager,
			updated_at=now()`,
		p.UserID, p.IsManager, p.DetectedAt, p.AnalyticsSharedWithManager)
	return err
}

// ── ManagerTeamMember ────────────────────────────────────────────────────────────────

type ManagerTeamMember struct {
	ID               uuid.UUID
	ManagerUserID    uuid.UUID
	MemberEmail      string
	MemberUserID     *uuid.UUID
	DisplayName      string
	Source           string // "auto" | "manual"
	Cadence          string // "weekly" | "biweekly" | "monthly" | "custom" | "none"
	CadenceCustomDays *int
	LastOneOnOneAt   *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

func UpsertManagerTeamMember(db *sql.DB, m *ManagerTeamMember) error {
	_, err := db.Exec(`
		INSERT INTO manager_team_members
			(manager_user_id, member_email, member_user_id, display_name, source,
			 cadence, cadence_custom_days, last_one_on_one_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (manager_user_id, member_email) DO UPDATE SET
			member_user_id=COALESCE(EXCLUDED.member_user_id, manager_team_members.member_user_id),
			display_name=CASE WHEN EXCLUDED.source='auto' AND manager_team_members.source='manual'
				THEN manager_team_members.display_name ELSE EXCLUDED.display_name END,
			source=CASE WHEN manager_team_members.source='manual' THEN 'manual' ELSE EXCLUDED.source END,
			cadence=EXCLUDED.cadence,
			cadence_custom_days=EXCLUDED.cadence_custom_days,
			last_one_on_one_at=GREATEST(EXCLUDED.last_one_on_one_at, manager_team_members.last_one_on_one_at),
			updated_at=now()`,
		m.ManagerUserID, m.MemberEmail, m.MemberUserID, m.DisplayName, m.Source,
		m.Cadence, m.CadenceCustomDays, m.LastOneOnOneAt)
	return err
}

func GetManagerTeamMemberByEmail(db *sql.DB, managerID uuid.UUID, email string) (*ManagerTeamMember, error) {
	row := db.QueryRow(`
		SELECT id, manager_user_id, member_email, member_user_id, display_name,
		       source, cadence, cadence_custom_days, last_one_on_one_at, created_at, updated_at
		FROM manager_team_members
		WHERE manager_user_id=$1 AND member_email=$2`, managerID, email)
	return scanTeamMember(row)
}

func ListManagerTeamMembers(db *sql.DB, managerID uuid.UUID) ([]*ManagerTeamMember, error) {
	rows, err := db.Query(`
		SELECT id, manager_user_id, member_email, member_user_id, display_name,
		       source, cadence, cadence_custom_days, last_one_on_one_at, created_at, updated_at
		FROM manager_team_members
		WHERE manager_user_id=$1
		ORDER BY display_name`, managerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var members []*ManagerTeamMember
	for rows.Next() {
		m, err := scanTeamMember(rows)
		if err != nil {
			return nil, err
		}
		members = append(members, m)
	}
	return members, rows.Err()
}

func DeleteManagerTeamMemberByEmail(db *sql.DB, managerID uuid.UUID, email string) error {
	_, err := db.Exec(`DELETE FROM manager_team_members WHERE manager_user_id=$1 AND member_email=$2`,
		managerID, email)
	return err
}

func PatchManagerTeamMember(db *sql.DB, managerID uuid.UUID, email, displayName, cadence string, customDays *int) error {
	_, err := db.Exec(`
		UPDATE manager_team_members
		SET display_name=$3, cadence=$4, cadence_custom_days=$5, updated_at=now()
		WHERE manager_user_id=$1 AND member_email=$2`,
		managerID, email, displayName, cadence, customDays)
	return err
}

type scanner interface {
	Scan(dest ...interface{}) error
}

func scanTeamMember(s scanner) (*ManagerTeamMember, error) {
	var m ManagerTeamMember
	if err := s.Scan(&m.ID, &m.ManagerUserID, &m.MemberEmail, &m.MemberUserID,
		&m.DisplayName, &m.Source, &m.Cadence, &m.CadenceCustomDays,
		&m.LastOneOnOneAt, &m.CreatedAt, &m.UpdatedAt); err != nil {
		return nil, err
	}
	return &m, nil
}

// ── OneOnOneOccurrence ────────────────────────────────────────────────────────

func UpsertOneOnOneOccurrence(db *sql.DB, managerID uuid.UUID, memberEmail, calendarEventID string, occurredAt time.Time) error {
	_, err := db.Exec(`
		INSERT INTO one_on_one_occurrences (manager_user_id, member_email, calendar_event_id, occurred_at)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (manager_user_id, calendar_event_id) DO NOTHING`,
		managerID, memberEmail, calendarEventID, occurredAt)
	return err
}

// ── TeamAnalyticsCache ────────────────────────────────────────────────────────

type TeamAnalyticsRow struct {
	ManagerUserID  uuid.UUID
	MemberEmail    string
	WeekStart      time.Time
	FocusMinutes   int
	MeetingMinutes int
	FreeMinutes    int
	IsPacedayUser  bool
	DataAvailable  bool
	ComputedAt     time.Time
}

func UpsertTeamAnalytics(db *sql.DB, r *TeamAnalyticsRow) error {
	_, err := db.Exec(`
		INSERT INTO team_analytics_cache
			(manager_user_id, member_email, week_start, focus_minutes, meeting_minutes,
			 free_minutes, is_paceday_user, data_available, computed_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,now())
		ON CONFLICT (manager_user_id, member_email, week_start) DO UPDATE SET
			focus_minutes=EXCLUDED.focus_minutes,
			meeting_minutes=EXCLUDED.meeting_minutes,
			free_minutes=EXCLUDED.free_minutes,
			is_paceday_user=EXCLUDED.is_paceday_user,
			data_available=EXCLUDED.data_available,
			computed_at=now()`,
		r.ManagerUserID, r.MemberEmail, r.WeekStart.Format("2006-01-02"),
		r.FocusMinutes, r.MeetingMinutes, r.FreeMinutes, r.IsPacedayUser, r.DataAvailable)
	return err
}

func GetTeamAnalytics(db *sql.DB, managerID uuid.UUID, memberEmail string, weekStart time.Time) (*TeamAnalyticsRow, error) {
	row := db.QueryRow(`
		SELECT manager_user_id, member_email, week_start,
		       focus_minutes, meeting_minutes, free_minutes, is_paceday_user, data_available, computed_at
		FROM team_analytics_cache
		WHERE manager_user_id=$1 AND member_email=$2 AND week_start=$3`,
		managerID, memberEmail, weekStart.Format("2006-01-02"))
	var r TeamAnalyticsRow
	if err := row.Scan(&r.ManagerUserID, &r.MemberEmail, &r.WeekStart,
		&r.FocusMinutes, &r.MeetingMinutes, &r.FreeMinutes, &r.IsPacedayUser, &r.DataAvailable, &r.ComputedAt); err != nil {
		return nil, err
	}
	return &r, nil
}

// ListManagerUserIDs returns all user IDs where is_manager=true (for batch jobs).
func ListManagerUserIDs(db *sql.DB) ([]uuid.UUID, error) {
	rows, err := db.Query(`SELECT user_id FROM user_profiles WHERE is_manager=true`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
