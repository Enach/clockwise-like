package storage

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
)

type Team struct {
	ID        uuid.UUID  `json:"id"`
	OrgID     *uuid.UUID `json:"orgId,omitempty"`
	Name      string     `json:"name"`
	CreatedBy uuid.UUID  `json:"createdBy"`
	CreatedAt time.Time  `json:"createdAt"`
}

type TeamMember struct {
	TeamID   uuid.UUID `json:"teamId"`
	UserID   uuid.UUID `json:"userId"`
	Role     string    `json:"role"`
	JoinedAt time.Time `json:"joinedAt"`
	// Populated on read
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
}

type TeamInvite struct {
	ID           uuid.UUID `json:"id"`
	TeamID       uuid.UUID `json:"teamId"`
	InviteeEmail string    `json:"inviteeEmail"`
	InvitedBy    uuid.UUID `json:"invitedBy"`
	Token        string    `json:"token"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"createdAt"`
	ExpiresAt    time.Time `json:"expiresAt"`
}

type NoMeetingZone struct {
	ID         uuid.UUID `json:"id"`
	TeamID     uuid.UUID `json:"teamId"`
	DayOfWeek  int       `json:"dayOfWeek"`
	StartTime  string    `json:"startTime"`
	EndTime    string    `json:"endTime"`
	Label      string    `json:"label"`
	CreatedAt  time.Time `json:"createdAt"`
}

// --- Teams ---

func CreateTeam(db *sql.DB, name string, createdBy uuid.UUID) (*Team, error) {
	row := db.QueryRow(`
		INSERT INTO teams (name, created_by) VALUES ($1, $2)
		RETURNING id, org_id, name, created_by, created_at`,
		name, createdBy)
	return scanTeam(row)
}

func GetTeam(db *sql.DB, id uuid.UUID) (*Team, error) {
	row := db.QueryRow(`SELECT id, org_id, name, created_by, created_at FROM teams WHERE id = $1`, id)
	return scanTeam(row)
}

func ListTeamsForUser(db *sql.DB, userID uuid.UUID) ([]*Team, error) {
	rows, err := db.Query(`
		SELECT t.id, t.org_id, t.name, t.created_by, t.created_at
		FROM teams t
		JOIN team_members tm ON tm.team_id = t.id
		WHERE tm.user_id = $1
		ORDER BY t.created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var teams []*Team
	for rows.Next() {
		t, err := scanTeamRow(rows)
		if err != nil {
			return nil, err
		}
		teams = append(teams, t)
	}
	return teams, rows.Err()
}

func RenameTeam(db *sql.DB, id uuid.UUID, name string) error {
	_, err := db.Exec(`UPDATE teams SET name = $1 WHERE id = $2`, name, id)
	return err
}

func DeleteTeam(db *sql.DB, id uuid.UUID) error {
	_, err := db.Exec(`DELETE FROM teams WHERE id = $1`, id)
	return err
}

func scanTeam(row *sql.Row) (*Team, error) {
	var t Team
	var orgID *uuid.UUID
	err := row.Scan(&t.ID, &orgID, &t.Name, &t.CreatedBy, &t.CreatedAt)
	if err != nil {
		return nil, err
	}
	t.OrgID = orgID
	return &t, nil
}

func scanTeamRow(rows *sql.Rows) (*Team, error) {
	var t Team
	var orgID *uuid.UUID
	err := rows.Scan(&t.ID, &orgID, &t.Name, &t.CreatedBy, &t.CreatedAt)
	if err != nil {
		return nil, err
	}
	t.OrgID = orgID
	return &t, nil
}

// --- Team members ---

func AddTeamMember(db *sql.DB, teamID, userID uuid.UUID, role string) error {
	_, err := db.Exec(`
		INSERT INTO team_members (team_id, user_id, role)
		VALUES ($1, $2, $3)
		ON CONFLICT (team_id, user_id) DO NOTHING`,
		teamID, userID, role)
	return err
}

func RemoveTeamMember(db *sql.DB, teamID, userID uuid.UUID) error {
	_, err := db.Exec(`DELETE FROM team_members WHERE team_id = $1 AND user_id = $2`, teamID, userID)
	return err
}

func GetTeamMember(db *sql.DB, teamID, userID uuid.UUID) (*TeamMember, error) {
	row := db.QueryRow(`
		SELECT tm.team_id, tm.user_id, tm.role, tm.joined_at,
		       COALESCE(u.name,''), COALESCE(u.email,'')
		FROM team_members tm
		JOIN users u ON u.id = tm.user_id
		WHERE tm.team_id = $1 AND tm.user_id = $2`, teamID, userID)
	var m TeamMember
	err := row.Scan(&m.TeamID, &m.UserID, &m.Role, &m.JoinedAt, &m.Name, &m.Email)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func ListTeamMembers(db *sql.DB, teamID uuid.UUID) ([]*TeamMember, error) {
	rows, err := db.Query(`
		SELECT tm.team_id, tm.user_id, tm.role, tm.joined_at,
		       COALESCE(u.name,''), COALESCE(u.email,'')
		FROM team_members tm
		JOIN users u ON u.id = tm.user_id
		WHERE tm.team_id = $1
		ORDER BY tm.joined_at ASC`, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var members []*TeamMember
	for rows.Next() {
		var m TeamMember
		if err := rows.Scan(&m.TeamID, &m.UserID, &m.Role, &m.JoinedAt, &m.Name, &m.Email); err != nil {
			return nil, err
		}
		members = append(members, &m)
	}
	return members, rows.Err()
}

// --- Invites ---

func CreateTeamInvite(db *sql.DB, teamID uuid.UUID, inviteeEmail string, invitedBy uuid.UUID) (*TeamInvite, error) {
	token := uuid.New().String()
	expiresAt := time.Now().Add(7 * 24 * time.Hour)
	row := db.QueryRow(`
		INSERT INTO team_invites (team_id, invitee_email, invited_by, token, expires_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, team_id, invitee_email, invited_by, token, status, created_at, expires_at`,
		teamID, inviteeEmail, invitedBy, token, expiresAt)
	return scanInvite(row)
}

func GetTeamInviteByToken(db *sql.DB, token string) (*TeamInvite, error) {
	row := db.QueryRow(`
		SELECT id, team_id, invitee_email, invited_by, token, status, created_at, expires_at
		FROM team_invites WHERE token = $1`, token)
	return scanInvite(row)
}

func AcceptTeamInvite(db *sql.DB, inviteID uuid.UUID) error {
	_, err := db.Exec(`UPDATE team_invites SET status = 'accepted' WHERE id = $1`, inviteID)
	return err
}

func ExpireOldInvites(db *sql.DB) error {
	_, err := db.Exec(`UPDATE team_invites SET status = 'expired' WHERE status = 'pending' AND expires_at < NOW()`)
	return err
}

func scanInvite(row *sql.Row) (*TeamInvite, error) {
	var i TeamInvite
	err := row.Scan(&i.ID, &i.TeamID, &i.InviteeEmail, &i.InvitedBy, &i.Token, &i.Status, &i.CreatedAt, &i.ExpiresAt)
	if err != nil {
		return nil, err
	}
	return &i, nil
}

// --- No-meeting zones ---

func CreateNoMeetingZone(db *sql.DB, teamID uuid.UUID, dayOfWeek int, startTime, endTime, label string) (*NoMeetingZone, error) {
	row := db.QueryRow(`
		INSERT INTO no_meeting_zones (team_id, day_of_week, start_time, end_time, label)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, team_id, day_of_week, start_time, end_time, label, created_at`,
		teamID, dayOfWeek, startTime, endTime, label)
	return scanZone(row)
}

func ListNoMeetingZones(db *sql.DB, teamID uuid.UUID) ([]*NoMeetingZone, error) {
	rows, err := db.Query(`
		SELECT id, team_id, day_of_week, start_time, end_time, label, created_at
		FROM no_meeting_zones WHERE team_id = $1 ORDER BY day_of_week, start_time`, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var zones []*NoMeetingZone
	for rows.Next() {
		var z NoMeetingZone
		if err := rows.Scan(&z.ID, &z.TeamID, &z.DayOfWeek, &z.StartTime, &z.EndTime, &z.Label, &z.CreatedAt); err != nil {
			return nil, err
		}
		zones = append(zones, &z)
	}
	return zones, rows.Err()
}

func DeleteNoMeetingZone(db *sql.DB, id, teamID uuid.UUID) error {
	_, err := db.Exec(`DELETE FROM no_meeting_zones WHERE id = $1 AND team_id = $2`, id, teamID)
	return err
}

func scanZone(row *sql.Row) (*NoMeetingZone, error) {
	var z NoMeetingZone
	err := row.Scan(&z.ID, &z.TeamID, &z.DayOfWeek, &z.StartTime, &z.EndTime, &z.Label, &z.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &z, nil
}
