package api

import (
	"errors"
	"net/http"
	"testing"

	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

// PAC-122 SF-107 (docs/factory/silent-failures.md, the batch 4 follow-up): the
// formal-team member check (requireMember) behind team detail, the zone list,
// availability and analytics answered a failed membership read with the 403,
// so an outage read as "not a member". A failed read is now the 500; a caller
// with no membership row is still the 403.

// pac122SF107Paths are the four member-checked routes for team.
func pac122SF107Paths(team uuid.UUID) []string {
	base := "/api/teams/" + team.String()
	return []string{
		base,
		base + "/no-meeting-zones",
		base + "/availability?date=2026-04-28&duration=30",
		base + "/analytics?date=2026-04-28",
	}
}

// SF-107: a failed membership read of a real member is the 500, with the
// generic body and a Sentry report, on all four routes.
func TestPAC122_SF107_MemberCheckReadErrorIs500(t *testing.T) {
	r := setupTeamRoutes(t)
	owner := createTestUser(t, "pac122-sf107-owner@example.com")
	member := createTestUser(t, "pac122-sf107-member@example.com")
	team := createManagerTestTeam(t, owner, "pac122 sf107")
	if err := storage.AddTeamMember(openTestDB(t), team.ID, member, "member"); err != nil {
		t.Fatal(err)
	}
	storage.InstallTeamMemberRead(t, func(teamID, userID uuid.UUID) error {
		if teamID == team.ID && (userID == owner || userID == member) {
			return errors.New("pac122: membership read failed")
		}
		return nil
	})
	for _, u := range []uuid.UUID{owner, member} {
		for _, path := range pac122SF107Paths(team.ID) {
			t.Run(path, func(t *testing.T) {
				reported, _ := captureServerErrors(t)
				w := managerRequest(t, r, http.MethodGet, path, u, nil)
				pac122AssertServerError(t, w, reported)
			})
		}
	}
}

// SF-107: a signed-in user who is not a member, and a caller naming a team
// that does not exist, are still the 403 "forbidden" on all four routes.
func TestPAC122_SF107_NonMemberStillForbidden(t *testing.T) {
	r := setupTeamRoutes(t)
	owner := createTestUser(t, "pac122-sf107-o@example.com")
	outsider := createTestUser(t, "pac122-sf107-x@example.com")
	team := createManagerTestTeam(t, owner, "pac122 sf107 forbidden")
	for _, c := range []struct {
		name string
		u    uuid.UUID
		team uuid.UUID
	}{
		{"non-member", outsider, team.ID},
		{"stranger to an unknown team", outsider, uuid.New()},
		{"member of another team", owner, uuid.New()},
	} {
		for _, path := range pac122SF107Paths(c.team) {
			t.Run(c.name+" "+path, func(t *testing.T) {
				reported, _ := captureServerErrors(t)
				w := managerRequest(t, r, http.MethodGet, path, c.u, nil)
				if w.Code != http.StatusForbidden {
					t.Fatalf("status = %d, want 403; body: %s", w.Code, w.Body.String())
				}
				if len(*reported) != 0 {
					t.Errorf("a refusal was reported as a server error: %v", *reported)
				}
			})
		}
	}
}
