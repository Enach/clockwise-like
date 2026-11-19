package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/internal/lifecycle"
	"github.com/Enach/paceday/backend/internal/lifecycle/lifecycletest"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

// PAC-122 phase 2, batch 4 (docs/factory/silent-failures.md §7, teams /
// manager, light rows): SF-008, SF-009, SF-010, SF-040, SF-074, SF-076,
// SF-077. Each test fails on the code before the fix: the failure was
// answered as a success (or as "forbidden").

// pac122FailInserts installs a trigger that makes every INSERT on table
// whose column equals value fail, removed when the test ends. It is scoped to
// the test's own rows, so other tests on the shared database are not affected.
func pac122FailInserts(t *testing.T, table, column, value string) {
	t.Helper()
	db := openTestDB(t)
	name := "pac122_fail_ins_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	fn := fmt.Sprintf(`CREATE FUNCTION %[1]s() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.%[2]s::text = %[3]s THEN RAISE EXCEPTION 'pac122: forced insert failure'; END IF;
			RETURN NEW;
		END $$`, name, column, quoteLiteral(value))
	if _, err := db.Exec(fn); err != nil {
		t.Fatalf("install failure function: %v", err)
	}
	if _, err := db.Exec(fmt.Sprintf(`CREATE TRIGGER %[1]s BEFORE INSERT ON %[2]s FOR EACH ROW EXECUTE FUNCTION %[1]s()`, name, table)); err != nil {
		t.Fatalf("install failure trigger: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON %s`, name, table))
		_, _ = db.Exec(fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, name))
	})
}

// SF-008: the owner's membership insert fails. The answer is the 500 and no
// team is left behind: before the fix the team was created (201) with no
// owner, so nobody could manage or delete it.
func TestPAC122_SF008_FailedOwnerInsertCreatesNoTeam(t *testing.T) {
	r := setupTeamRoutes(t)
	u := createTestUser(t, "pac122-sf008@example.com")
	pac122FailInserts(t, "team_members", "user_id", u.String())
	reported, _ := captureServerErrors(t)

	w := managerRequest(t, r, http.MethodPost, "/api/teams", u, map[string]string{"name": "Ownerless"})
	pac122AssertServerError(t, w, reported)
	if n := pac122Count(t, `SELECT count(*) FROM teams WHERE created_by=$1`, u); n != 0 {
		t.Fatalf("teams created = %d, want 0 (no ownerless team)", n)
	}
}

// SF-008: the success path stores the team and its owner together.
func TestPAC122_SF008_CreatedTeamHasItsOwner(t *testing.T) {
	r := setupTeamRoutes(t)
	u := createTestUser(t, "pac122-sf008-ok@example.com")
	w := managerRequest(t, r, http.MethodPost, "/api/teams", u, map[string]string{"name": "Owned"})
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
	}
	if n := pac122Count(t, `SELECT count(*) FROM team_members tm JOIN teams t ON t.id=tm.team_id WHERE t.created_by=$1 AND tm.user_id=$1 AND tm.role='owner'`, u); n != 1 {
		t.Fatalf("owner rows = %d, want 1", n)
	}
}

// SF-009: the profile write fails. The answer is the 500, not a 200 that
// echoes a flag nothing stored.
func TestPAC122_SF009_FailedProfileWriteIs500(t *testing.T) {
	r := setupManagerRoutes(t, &fakeManagerWorkflow{})
	u := createTestUser(t, "pac122-sf009@example.com")
	storage.InstallUserProfileUpsert(t, func(userID uuid.UUID) error {
		if userID == u {
			return errors.New("pac122: profile write failed")
		}
		return nil
	})
	reported, _ := captureServerErrors(t)

	// is_manager false: no detection is spawned.
	w := managerRequest(t, r, http.MethodPost, "/api/manager/profile", u, map[string]bool{"is_manager": false})
	pac122AssertServerError(t, w, reported)
}

// SF-010, rewritten by PAC-130 (was TestPAC122_SF010_HolderLookupErrorIs500;
// spec D-2, contract md §3.1, §7): the add no longer looks the address up, so
// a holder lookup that would fail is never reached. The add answers 201 with member_user_id null and stores the row
// with no account link, exactly as for an address nobody holds. SF-010's rule
// moves to the Manager visibility read (api/pac130_test.go, AC-13).
func TestPAC122_SF010_AddLooksNoAddressUp(t *testing.T) {
	r := setupManagerRoutes(t, &fakeManagerWorkflow{})
	u := createTestUser(t, "pac122-sf010-mgr@example.com")
	email := "pac122-sf010-report@example.com"
	createConfirmedUser(t, email)
	lookups := 0
	storage.InstallConfirmedHolderLookup(t, func(e string) error {
		if strings.EqualFold(strings.TrimSpace(e), email) {
			lookups++
			return errors.New("pac122: holder lookup failed")
		}
		return nil
	})

	w := managerRequest(t, r, http.MethodPost, "/api/manager/team/members", u, map[string]any{"email": email, "cadence": "weekly"})
	if w.Code != http.StatusCreated || !strings.Contains(w.Body.String(), `"member_user_id":null`) {
		t.Fatalf("status = %d, want 201 with member_user_id null; body: %s", w.Code, w.Body.String())
	}
	if lookups != 0 {
		t.Fatalf("holder lookups = %d, want 0 (the add looks no address up)", lookups)
	}
	if n := pac122Count(t, `SELECT count(*) FROM manager_team_members WHERE manager_user_id=$1 AND member_user_id IS NULL`, u); n != 1 {
		t.Fatalf("stored rows without a link = %d, want 1", n)
	}
}

// failingWeekWorkflow is a manager workflow whose member-week read fails for
// the week starting on failWeek (every week when it is zero).
type failingWeekWorkflow struct {
	fakeManagerWorkflow
	failWeek time.Time
}

func (f *failingWeekWorkflow) MemberWeeks(_ context.Context, _ uuid.UUID, _ *storage.ManagerTeamMember, weekStarts []time.Time) (*engine.MemberWeeks, error) {
	out := &engine.MemberWeeks{}
	for _, weekStart := range weekStarts {
		if f.failWeek.IsZero() || weekStart.Equal(f.failWeek) {
			return nil, errors.New("pac122: member week read failed")
		}
		out.Weeks = append(out.Weeks, &engine.MemberWeekStats{DataAvailable: true})
	}
	return out, nil
}

// SF-040: a failed member-week read is the 500 on the roster and on the
// analytics, never a member shown with zeroes or "no data".
func TestPAC122_SF040_FailedMemberWeekIs500(t *testing.T) {
	r := setupManagerRoutes(t, &failingWeekWorkflow{})
	u := createTestUser(t, "pac122-sf040-mgr@example.com")
	team := createManagerTestTeam(t, u, "pac122 sf040")
	m := &storage.ManagerTeamMember{ManagerUserID: u, MemberEmail: "pac122-sf040-report@example.com", DisplayName: "Report", Source: "manual", Cadence: "weekly"}
	if err := storage.UpsertManagerTeamMemberForTeam(openTestDB(t), team.ID, m); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"/api/manager/team?team_id=" + team.ID.String(),
		"/api/manager/analytics",
		"/api/manager/analytics?team_id=" + team.ID.String(),
	} {
		t.Run(path, func(t *testing.T) {
			reported, _ := captureServerErrors(t)
			w := managerRequest(t, r, http.MethodGet, path, u, nil)
			pac122AssertServerError(t, w, reported)
		})
	}
	// The roster reads this week and last week: either failing is the 500.
	for _, fail := range []string{"2026-11-09", "2026-11-02"} {
		t.Run("roster fails "+fail, func(t *testing.T) {
			ws, _ := time.Parse("2006-01-02", fail)
			r := setupManagerRoutes(t, &failingWeekWorkflow{failWeek: ws})
			reported, _ := captureServerErrors(t)
			w := managerRequest(t, r, http.MethodGet, "/api/manager/team?week=2026-11-09&team_id="+team.ID.String(), u, nil)
			pac122AssertServerError(t, w, reported)
		})
	}
}

// SF-074: a failed membership read in an owner check is the 500, never the
// 403 "forbidden", on the team routes and the manager team scope.
func TestPAC122_SF074_OwnerCheckReadErrorIs500(t *testing.T) {
	teams := setupTeamRoutes(t)
	// setupTeamRoutes does not mount the zone PATCH; it is an owner check too.
	teams.Patch("/api/teams/{id}/no-meeting-zones/{zoneId}", newTeamHandlers(openTestDB(t), nil).updateZone)
	manager := setupManagerRoutes(t, &fakeManagerWorkflow{})
	u := createTestUser(t, "pac122-sf074-owner@example.com")
	team := createManagerTestTeam(t, u, "pac122 sf074")
	storage.InstallTeamMemberRead(t, func(teamID, userID uuid.UUID) error {
		if teamID == team.ID && userID == u {
			return errors.New("pac122: membership read failed")
		}
		return nil
	})
	for _, c := range []struct {
		r            http.Handler
		method, path string
		body         any
	}{
		{teams, http.MethodPatch, "/api/teams/" + team.ID.String(), map[string]string{"name": "x"}},
		{teams, http.MethodDelete, "/api/teams/" + team.ID.String(), nil},
		{teams, http.MethodPost, "/api/teams/" + team.ID.String() + "/no-meeting-zones", map[string]any{"dayOfWeek": 1, "startTime": "09:00", "endTime": "10:00"}},
		{teams, http.MethodPatch, "/api/teams/" + team.ID.String() + "/no-meeting-zones/" + uuid.NewString(), map[string]any{"dayOfWeek": 1, "startTime": "09:00", "endTime": "10:00"}},
		{teams, http.MethodDelete, "/api/teams/" + team.ID.String() + "/no-meeting-zones/" + uuid.NewString(), nil},
		{teams, http.MethodDelete, "/api/teams/" + team.ID.String() + "/members/" + uuid.NewString(), nil},
		{manager, http.MethodGet, "/api/manager/team?team_id=" + team.ID.String(), nil},
		{manager, http.MethodGet, "/api/manager/gaps?team_id=" + team.ID.String(), nil},
	} {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			reported, _ := captureServerErrors(t)
			w := managerRequest(t, c.r, c.method, c.path, u, c.body)
			pac122AssertServerError(t, w, reported)
		})
	}
	if n := pac122Count(t, `SELECT count(*) FROM teams WHERE id=$1`, team.ID); n != 1 {
		t.Fatalf("team rows = %d, want 1 (nothing deleted)", n)
	}
}

// SF-074: a caller with no membership, or a member who is not an owner, is
// still the 403.
func TestPAC122_SF074_NonOwnerStillForbidden(t *testing.T) {
	teams := setupTeamRoutes(t)
	manager := setupManagerRoutes(t, &fakeManagerWorkflow{})
	owner := createTestUser(t, "pac122-sf074-o@example.com")
	member := createTestUser(t, "pac122-sf074-m@example.com")
	outsider := createTestUser(t, "pac122-sf074-x@example.com")
	team := createManagerTestTeam(t, owner, "pac122 sf074 forbidden")
	if err := storage.AddTeamMember(openTestDB(t), team.ID, member, "member"); err != nil {
		t.Fatal(err)
	}
	for _, u := range []uuid.UUID{member, outsider} {
		if w := managerRequest(t, teams, http.MethodPatch, "/api/teams/"+team.ID.String(), u, map[string]string{"name": "x"}); w.Code != http.StatusForbidden {
			t.Fatalf("PATCH status = %d, want 403; body: %s", w.Code, w.Body.String())
		}
		if w := managerRequest(t, manager, http.MethodPost, "/api/manager/team/members?team_id="+team.ID.String(), u, map[string]any{"email": "a@example.com"}); w.Code != http.StatusForbidden {
			t.Fatalf("manager add status = %d, want 403; body: %s", w.Code, w.Body.String())
		}
	}
	if w := managerRequest(t, manager, http.MethodGet, "/api/manager/team?team_id="+team.ID.String(), outsider, nil); w.Code != http.StatusForbidden {
		t.Fatalf("manager roster status = %d, want 403; body: %s", w.Code, w.Body.String())
	}
}

// SF-076: a failed roster count is the 500, never team_member_count 0.
func TestPAC122_SF076_FailedRosterCountIs500(t *testing.T) {
	r := setupManagerRoutes(t, &fakeManagerWorkflow{})
	u := createTestUser(t, "pac122-sf076@example.com")
	storage.InstallManagerRosterCount(t, func(managerID uuid.UUID) error {
		if managerID == u {
			return errors.New("pac122: count failed")
		}
		return nil
	})
	reported, _ := captureServerErrors(t)
	w := managerRequest(t, r, http.MethodGet, "/api/manager/profile", u, nil)
	pac122AssertServerError(t, w, reported)
}

// SF-077: the team and inviter reads for the invite email fail. The
// invitation stands (201) and the email still goes out with the generic
// names, but each failed read is reported under the email's unit, by id,
// never by address.
func TestPAC122_SF077_InviteEmailReadFailuresAreReported(t *testing.T) {
	r := setupTeamRoutes(t)
	owner := createTestUser(t, "pac122-sf077-owner@example.com")
	team := createManagerTestTeam(t, owner, "pac122 sf077")
	storage.InstallTeamRead(t, func(teamID uuid.UUID) error {
		if teamID == team.ID {
			return errors.New("pac122: team read failed")
		}
		return nil
	})
	storage.InstallUserRead(t, func(userID uuid.UUID) error {
		if userID == owner {
			return errors.New("pac122: inviter read failed")
		}
		return nil
	})
	rec := lifecycletest.Record(t)

	w := managerRequest(t, r, http.MethodPost, "/api/teams/"+team.ID.String()+"/members/invite", owner, map[string]string{"email": "pac122-sf077-invitee@example.com"})
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body: %s", w.Code, w.Body.String())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	lifecycle.Default().Wait(ctx)

	if got := rec.Matching("team-invite-email", "read team"); len(got) != 1 {
		t.Fatalf("team read reports = %+v, want 1", rec.All())
	}
	if got := rec.Matching("team-invite-email", "read inviter"); len(got) != 1 {
		t.Fatalf("inviter read reports = %+v, want 1", rec.All())
	}
	for _, rep := range rec.All() {
		if strings.Contains(rep.Err.Error(), "@") {
			t.Fatalf("report carries an address: %v", rep.Err)
		}
	}
}

// SF-074: the owner check inside the invitation and the manager's scoped
// removal transactions (PAC-129's rewrites of the existing operations): a
// failed read is the 500 and nothing is written, never the 403.
func TestPAC122_SF074_InTransactionOwnerCheckReadErrorIs500(t *testing.T) {
	teams := setupTeamRoutes(t)
	manager := setupManagerRoutes(t, &fakeManagerWorkflow{})
	owner, member, team, email := pac122ScopedRemoval(t, "pac122-sf074-tx")
	storage.InstallCallerRoleRead(t, func(teamID, userID uuid.UUID) error {
		if teamID == team.ID && userID == owner {
			return errors.New("pac122: owner check read failed")
		}
		return nil
	})

	reported, _ := captureServerErrors(t)
	w := managerRequest(t, teams, http.MethodPost, "/api/teams/"+team.ID.String()+"/members/invite", owner, map[string]string{"email": "pac122-sf074-new@example.com"})
	pac122AssertServerError(t, w, reported)
	if n := pac122Count(t, `SELECT count(*) FROM team_invites WHERE team_id=$1 AND invitee_email=$2`, team.ID, "pac122-sf074-new@example.com"); n != 0 {
		t.Fatalf("invitations = %d, want 0", n)
	}

	reported, _ = captureServerErrors(t)
	w = managerRequest(t, manager, http.MethodDelete, "/api/manager/team/members/"+url.PathEscape(email)+"?team_id="+team.ID.String(), owner, nil)
	pac122AssertServerError(t, w, reported)
	pac122AssertNothingRemoved(t, owner, member, team, email)
}
