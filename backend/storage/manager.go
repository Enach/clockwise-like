package storage

import (
	"database/sql"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ── UserProfile ───────────────────────────────────────────────────────────────

type UserProfile struct {
	UserID                     uuid.UUID  `json:"user_id"`
	IsManager                  bool       `json:"is_manager"`
	DetectedAt                 *time.Time `json:"detected_at"`
	AnalyticsSharedWithManager bool       `json:"analytics_shared_with_manager"`
	UpdatedAt                  time.Time  `json:"updated_at"`
}

// GetOrCreateUserProfile returns userID's profile, creating the row at its
// defaults when there is none. On a request path call GetOrCreateUserProfileCtx,
// which creates the row only under the in-request re-check (PAC-80 item 2).
func GetOrCreateUserProfile(db Querier, userID uuid.UUID) (*UserProfile, error) {
	_, err := db.Exec(`
		INSERT INTO user_profiles (user_id) VALUES ($1)
		ON CONFLICT (user_id) DO NOTHING`, userID)
	if err != nil {
		return nil, err
	}
	return GetUserProfile(db, userID)
}

func GetUserProfile(db Querier, userID uuid.UUID) (*UserProfile, error) {
	row := db.QueryRow(`
		SELECT user_id, is_manager, detected_at, analytics_shared_with_manager, updated_at
		FROM user_profiles WHERE user_id = $1`, userID)
	var p UserProfile
	if err := row.Scan(&p.UserID, &p.IsManager, &p.DetectedAt, &p.AnalyticsSharedWithManager, &p.UpdatedAt); err != nil {
		return nil, err
	}
	return &p, nil
}

func UpsertUserProfile(db Querier, p *UserProfile) error {
	if hook, ok := userProfileUpsert.get(); ok {
		if err := hook(p.UserID); err != nil {
			return err
		}
	}
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
	ID                uuid.UUID  `json:"id"`
	ManagerUserID     uuid.UUID  `json:"manager_user_id"`
	MemberEmail       string     `json:"member_email"`
	MemberUserID      *uuid.UUID `json:"member_user_id"`
	DisplayName       string     `json:"display_name"`
	Source            string     `json:"source"`  // "auto" | "manual"
	Cadence           string     `json:"cadence"` // "weekly" | "biweekly" | "monthly" | "custom" | "none"
	CadenceCustomDays *int       `json:"cadence_custom_days"`
	LastOneOnOneAt    *time.Time `json:"last_one_on_one_at"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

type sqlExecutor interface {
	Exec(query string, args ...interface{}) (sql.Result, error)
}

func upsertManagerTeamMember(exec sqlExecutor, m *ManagerTeamMember) error {
	m.MemberEmail = strings.ToLower(strings.TrimSpace(m.MemberEmail))
	m.DisplayName = strings.TrimSpace(m.DisplayName)
	_, err := exec.Exec(`
		INSERT INTO manager_team_members
			(manager_user_id, member_email, member_user_id, display_name, source,
			 cadence, cadence_custom_days, last_one_on_one_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (manager_user_id, member_email) DO UPDATE SET
			member_user_id=COALESCE(EXCLUDED.member_user_id, manager_team_members.member_user_id),
			display_name=CASE WHEN EXCLUDED.source='auto' AND manager_team_members.source='manual'
				THEN manager_team_members.display_name ELSE EXCLUDED.display_name END,
			source=CASE WHEN manager_team_members.source='manual' THEN 'manual' ELSE EXCLUDED.source END,
			-- PAC-122 SF-011: detection never replaces a manual row's cadence.
			cadence=CASE WHEN EXCLUDED.source='auto' AND manager_team_members.source='manual'
				THEN manager_team_members.cadence ELSE EXCLUDED.cadence END,
			cadence_custom_days=CASE WHEN EXCLUDED.source='auto' AND manager_team_members.source='manual'
				THEN manager_team_members.cadence_custom_days ELSE EXCLUDED.cadence_custom_days END,
			last_one_on_one_at=GREATEST(EXCLUDED.last_one_on_one_at, manager_team_members.last_one_on_one_at),
			updated_at=now()`,
		m.ManagerUserID, m.MemberEmail, m.MemberUserID, m.DisplayName, m.Source,
		m.Cadence, m.CadenceCustomDays, m.LastOneOnOneAt)
	return err
}

func UpsertManagerTeamMember(db Querier, m *ManagerTeamMember) error {
	return upsertManagerTeamMember(db, m)
}

func GetManagerTeamMemberByEmail(db *sql.DB, managerID uuid.UUID, email string) (*ManagerTeamMember, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	row := db.QueryRow(`
		SELECT id, manager_user_id, member_email, member_user_id, display_name,
		       source, cadence, cadence_custom_days, last_one_on_one_at, created_at, updated_at
		FROM manager_team_members
		WHERE manager_user_id=$1 AND member_email=$2`, managerID, email)
	return scanTeamMember(row)
}

// CountManagerTeamMembers counts the manager's global roster rows
// (manager_team_members), the profile's team_member_count (PAC-122 SF-076).
func CountManagerTeamMembers(db DBTX, managerID uuid.UUID) (int, error) {
	if hook, ok := managerRosterCount.get(); ok {
		if err := hook(managerID); err != nil {
			return 0, err
		}
	}
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM manager_team_members WHERE manager_user_id=$1`, managerID).Scan(&n)
	return n, err
}

func ListManagerTeamMembers(db DBTX, managerID uuid.UUID) ([]*ManagerTeamMember, error) {
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

func UpsertManagerTeamMemberForTeam(db Querier, teamID uuid.UUID, m *ManagerTeamMember) error {
	tx, sc, err := beginOn(db)
	if err != nil {
		return err
	}
	defer func() { _ = sc.Rollback() }()
	if err := upsertManagerTeamMember(tx, m); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		INSERT INTO manager_team_member_assignments
			(manager_user_id, team_id, member_email, cadence, cadence_custom_days)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (manager_user_id, team_id, member_email) DO UPDATE SET
			cadence=EXCLUDED.cadence,
			cadence_custom_days=EXCLUDED.cadence_custom_days`,
		m.ManagerUserID, teamID, m.MemberEmail, m.Cadence, m.CadenceCustomDays); err != nil {
		return err
	}
	return sc.Commit()
}

func ListManagerTeamMembersForTeam(db DBTX, managerID, teamID uuid.UUID) ([]*ManagerTeamMember, error) {
	rows, err := db.Query(`
		SELECT mtm.id, mtm.manager_user_id, mtm.member_email, mtm.member_user_id,
		       COALESCE(NULLIF(btrim(a.display_name_override), ''), mtm.display_name),
		       mtm.source, a.cadence, a.cadence_custom_days,
		       mtm.last_one_on_one_at, mtm.created_at, mtm.updated_at
		FROM manager_team_members mtm
		JOIN manager_team_member_assignments a
		  ON a.manager_user_id=mtm.manager_user_id
		 AND a.member_email=mtm.member_email
		WHERE a.manager_user_id=$1 AND a.team_id=$2
		ORDER BY mtm.display_name`, managerID, teamID)
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

func GetManagerTeamMemberByEmailForTeam(db *sql.DB, managerID, teamID uuid.UUID, email string) (*ManagerTeamMember, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	row := db.QueryRow(`
		SELECT mtm.id, mtm.manager_user_id, mtm.member_email, mtm.member_user_id,
		       COALESCE(NULLIF(btrim(a.display_name_override), ''), mtm.display_name),
		       mtm.source, a.cadence, a.cadence_custom_days,
		       mtm.last_one_on_one_at, mtm.created_at, mtm.updated_at
		FROM manager_team_members mtm
		JOIN manager_team_member_assignments a
		  ON a.manager_user_id=mtm.manager_user_id
		 AND a.member_email=mtm.member_email
		WHERE a.manager_user_id=$1 AND a.team_id=$2 AND a.member_email=$3`,
		managerID, teamID, email)
	return scanTeamMember(row)
}

func UnassignManagerTeamMember(db Querier, managerID, teamID uuid.UUID, email string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	_, err := db.Exec(`
		DELETE FROM manager_team_member_assignments
		WHERE manager_user_id=$1 AND team_id=$2 AND member_email=$3`,
		managerID, teamID, email)
	return err
}

func DeleteManagerTeamMemberByEmail(db Querier, managerID uuid.UUID, email string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	_, err := db.Exec(`DELETE FROM manager_team_members WHERE manager_user_id=$1 AND member_email=$2`,
		managerID, email)
	return err
}

func PatchManagerTeamMember(db Querier, managerID uuid.UUID, email, displayName, cadence string, customDays *int) error {
	email = strings.ToLower(strings.TrimSpace(email))
	_, err := db.Exec(`
		UPDATE manager_team_members
		SET display_name=$3, cadence=$4, cadence_custom_days=$5, updated_at=now()
		WHERE manager_user_id=$1 AND member_email=$2`,
		managerID, email, displayName, cadence, customDays)
	return err
}

// PatchManagerTeamMemberForTeam changes only team-scoped preferences. The
// canonical display name and email identity remain on manager_team_members.
func PatchManagerTeamMemberForTeam(db Querier, managerID, teamID uuid.UUID, email, displayName, cadence string, customDays *int) error {
	email = strings.ToLower(strings.TrimSpace(email))
	displayName = strings.TrimSpace(displayName)
	var override *string
	var canonical string
	if err := db.QueryRow(`
		SELECT display_name FROM manager_team_members
		WHERE manager_user_id=$1 AND member_email=$2`, managerID, email).Scan(&canonical); err != nil {
		return err
	}
	if displayName != "" && displayName != canonical {
		override = &displayName
	}
	result, err := db.Exec(`
		UPDATE manager_team_member_assignments
		SET display_name_override=$4, cadence=$5, cadence_custom_days=$6
		WHERE manager_user_id=$1 AND team_id=$2 AND member_email=$3`,
		managerID, teamID, email, override, cadence, customDays)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// ConfirmManagerTeamMembers atomically assigns known global candidates to one
// explicitly selected team. Duplicate and unknown emails are counted once as
// skipped; repeated confirmation is idempotent.
func ConfirmManagerTeamMembers(db Querier, managerID, teamID uuid.UUID, emails []string) (assigned, skipped, total int, err error) {
	tx, sc, err := beginOn(db)
	if err != nil {
		return 0, 0, 0, err
	}
	defer func() { _ = sc.Rollback() }()
	// PAC-134 L-3 (contract md §6.2): normalise and de-duplicate first
	// (empty and duplicate entries are skipped exactly as before), then
	// process the distinct addresses in byte order, the erase-address tool's
	// order on a manager's roster ((manager, member_email "C")), and read
	// each entry FOR KEY SHARE: a confirm racing an erasure (or a Manager
	// delete) of the entry waits for it, then answers skipped instead of
	// failing the assignment's foreign key after the delete commits.
	seen := make(map[string]struct{}, len(emails))
	distinct := make([]string, 0, len(emails))
	for _, raw := range emails {
		email := strings.ToLower(strings.TrimSpace(raw))
		if email == "" {
			skipped++
			continue
		}
		if _, duplicate := seen[email]; duplicate {
			skipped++
			continue
		}
		seen[email] = struct{}{}
		distinct = append(distinct, email)
	}
	sort.Strings(distinct)
	for _, email := range distinct {
		var exists bool
		if err := tx.QueryRow(`SELECT EXISTS(
			SELECT 1 FROM manager_team_members
			WHERE manager_user_id=$1 AND member_email=$2 FOR KEY SHARE)`, managerID, email).Scan(&exists); err != nil {
			return 0, 0, 0, err
		}
		if !exists {
			skipped++
			continue
		}
		result, err := tx.Exec(`
			INSERT INTO manager_team_member_assignments
				(manager_user_id, team_id, member_email, cadence, cadence_custom_days)
			SELECT manager_user_id, $2, member_email, cadence, cadence_custom_days
			FROM manager_team_members
			WHERE manager_user_id=$1 AND member_email=$3
			ON CONFLICT DO NOTHING`, managerID, teamID, email)
		if err != nil {
			return 0, 0, 0, err
		}
		if n, _ := result.RowsAffected(); n == 1 {
			assigned++
		} else {
			skipped++
		}
	}
	if err := tx.QueryRow(`
		SELECT count(*) FROM manager_team_member_assignments
		WHERE manager_user_id=$1 AND team_id=$2`, managerID, teamID).Scan(&total); err != nil {
		return 0, 0, 0, err
	}
	if err := sc.Commit(); err != nil {
		return 0, 0, 0, err
	}
	return assigned, skipped, total, nil
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

func UpsertOneOnOneOccurrence(db Querier, managerID uuid.UUID, memberEmail, calendarEventID string, occurredAt time.Time) error {
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

func UpsertTeamAnalytics(db Querier, r *TeamAnalyticsRow) error {
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

func GetTeamAnalytics(db DBTX, managerID uuid.UUID, memberEmail string, weekStart time.Time) (*TeamAnalyticsRow, error) {
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
func ListManagerUserIDs(db DBTX) ([]uuid.UUID, error) {
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

// AddressVisibleTo is the Manager visibility rule (PAC-130 spec D-1, contract
// md §2.1): whether an account carrying email (ignoring letter case, at any
// address standing) is one viewerID can already see in Paceday, either
//   - (a) a Paceday member (team_members, either role) of a team viewerID is
//     a Paceday member of (people added under PAC-129 never count), or
//   - (b) in viewerID's organisation member set, the set GET /api/org/members
//     lists (orgMemberSetSQL, shared with GetOrgMembers).
//
// It is one statement of the same shape whatever the address: an account
// outside (a) and (b) is never consulted, so for such an address the answer
// and the work are those of an address nobody carries (AC-8). No statement
// runs only because a carrier exists.
func AddressVisibleTo(q Querier, viewerID uuid.UUID, email string) (bool, error) {
	var visible bool
	err := q.QueryRow(`
		SELECT EXISTS (
			SELECT 1 FROM users u
			 WHERE lower(u.email) = lower($2)
			   AND (EXISTS (SELECT 1 FROM team_members v
			                  JOIN team_members m ON m.team_id = v.team_id
			                 WHERE v.user_id = $1 AND m.user_id = u.id)
			        OR u.id IN (`+orgMemberSetSQL("$1", "$3")+`)))`,
		viewerID, strings.TrimSpace(email), orgMembersExcluded(viewerID),
	).Scan(&visible)
	return visible, err
}
