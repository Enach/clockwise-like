package storage

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func teamRole(t *testing.T, db *sql.DB, teamID, userID uuid.UUID) string {
	t.Helper()
	var role string
	err := db.QueryRow(`SELECT role FROM team_members WHERE team_id = $1 AND user_id = $2`, teamID, userID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return ""
	}
	if err != nil {
		t.Fatalf("read role: %v", err)
	}
	return role
}

// PAC-25: the guarded removal refuses only a removal that leaves zero owners.
func TestRemoveTeamMemberKeepingOwner(t *testing.T) {
	db := openTestDB(t)
	o1 := insertTestUser(t, db, "pac25-store-o1@test.com", "O1")
	o2 := insertTestUser(t, db, "pac25-store-o2@test.com", "O2")
	m := insertTestUser(t, db, "pac25-store-m@test.com", "M")
	outsider := insertTestUser(t, db, "pac25-store-x@test.com", "X")
	team, err := CreateTeam(db, "PAC-25 storage", o1)
	if err != nil {
		t.Fatalf("create team: %v", err)
	}
	for u, role := range map[uuid.UUID]string{o1: "owner", o2: "owner", m: "member"} {
		if err := AddTeamMember(db, team.ID, u, role); err != nil {
			t.Fatalf("add %s: %v", role, err)
		}
	}

	if err := RemoveTeamMemberKeepingOwner(db, team.ID, outsider); err != nil {
		t.Fatalf("non-member: %v, want nil (no-op)", err)
	}
	if err := RemoveTeamMemberKeepingOwner(db, team.ID, m); err != nil || teamRole(t, db, team.ID, m) != "" {
		t.Fatalf("member: err=%v role=%q, want removed", err, teamRole(t, db, team.ID, m))
	}
	if err := RemoveTeamMemberKeepingOwner(db, team.ID, o1); err != nil || teamRole(t, db, team.ID, o1) != "" {
		t.Fatalf("owner with a co-owner: err=%v role=%q, want removed", err, teamRole(t, db, team.ID, o1))
	}
	if err := RemoveTeamMemberKeepingOwner(db, team.ID, o2); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("last owner: err=%v, want ErrLastOwner", err)
	}
	if got := teamRole(t, db, team.ID, o2); got != "owner" {
		t.Fatalf("last owner role after refusal = %q, want owner", got)
	}
	if err := RemoveTeamMemberKeepingOwner(db, uuid.New(), o2); err != nil {
		t.Fatalf("unknown team: %v, want nil (no-op)", err)
	}
}

// PAC-25: the caller's own host status, with "no row" distinct from an error.
func TestGetLinkHostStatus(t *testing.T) {
	db := openTestDB(t)
	owner := insertTestUser(t, db, "pac25-hs-owner@test.com", "Owner")
	pending := insertTestUser(t, db, "pac25-hs-pending@test.com", "Pending")
	stranger := insertTestUser(t, db, "pac25-hs-stranger@test.com", "Stranger")
	link, err := CreateSchedulingLink(db, &SchedulingLink{
		OwnerUserID: owner, Slug: "pac25-host-status", Title: "HS", DurationOptions: []int{30},
		DaysOfWeek: []int{1}, WindowStart: "09:00", WindowEnd: "17:00", UsageType: "reusable", Active: true,
	})
	if err != nil {
		t.Fatalf("create link: %v", err)
	}
	if _, err := AddLinkHost(db, link.ID, pending, "pending"); err != nil {
		t.Fatalf("add host: %v", err)
	}
	if status, found, err := GetLinkHostStatus(db, link.ID, pending); err != nil || !found || status != "pending" {
		t.Fatalf("pending: %q %v %v", status, found, err)
	}
	if status, found, err := GetLinkHostStatus(db, link.ID, stranger); err != nil || found || status != "" {
		t.Fatalf("stranger: %q %v %v", status, found, err)
	}
}
