package api

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

// PAC-122 phase 2, batch 0 (docs/factory/silent-failures.md §7): the three
// critical backend silent failures SF-001, SF-002 and SF-003. Each test fails
// on the code before the fix: the failure was answered as a success.

// pac122FailDeletes installs a trigger that makes every DELETE on table whose
// column equals value fail, and removes it when the test ends. It is scoped
// to the test's own rows, so other tests on the shared database are not
// affected.
func pac122FailDeletes(t *testing.T, table, column, value string) {
	t.Helper()
	db := openTestDB(t)
	name := "pac122_fail_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	fn := fmt.Sprintf(`CREATE FUNCTION %[1]s() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF OLD.%[2]s::text = %[3]s THEN RAISE EXCEPTION 'pac122: forced delete failure'; END IF;
			RETURN OLD;
		END $$`, name, column, quoteLiteral(value))
	if _, err := db.Exec(fn); err != nil {
		t.Fatalf("install failure function: %v", err)
	}
	if _, err := db.Exec(fmt.Sprintf(`CREATE TRIGGER %[1]s BEFORE DELETE ON %[2]s FOR EACH ROW EXECUTE FUNCTION %[1]s()`, name, table)); err != nil {
		t.Fatalf("install failure trigger: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON %s`, name, table))
		_, _ = db.Exec(fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, name))
	})
}

func quoteLiteral(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func pac122AssertServerError(t *testing.T, w *httptest.ResponseRecorder, reported *[]error) {
	t.Helper()
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body: %s", w.Code, w.Body.String())
	}
	if got := strings.TrimSpace(w.Body.String()); got != `{"error":"internal error"}` {
		t.Errorf("body = %s, want the generic internal error", got)
	}
	if len(*reported) == 0 {
		t.Error("the failure was not reported to Sentry")
	}
}

func pac122Count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := openTestDB(t).QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// pac122ScopedRemoval seeds a team whose member carries email, with a
// manager assignment and a pending invite for that email on the team.
func pac122ScopedRemoval(t *testing.T, prefix string) (owner, member uuid.UUID, team *storage.Team, email string) {
	t.Helper()
	db := openTestDB(t)
	owner = createConfirmedUser(t, prefix+"-owner@example.com")
	email = prefix + "-member@example.com"
	member = createConfirmedUser(t, email)
	team = createManagerTestTeam(t, owner, prefix+" team")
	if err := storage.AddTeamMember(db, team.ID, member, "member"); err != nil {
		t.Fatalf("add member: %v", err)
	}
	if err := storage.UpsertManagerTeamMemberForTeam(db, team.ID, &storage.ManagerTeamMember{
		ManagerUserID: owner, MemberEmail: email, DisplayName: "Member", Source: "manual", Cadence: "weekly",
	}); err != nil {
		t.Fatalf("assign member: %v", err)
	}
	if _, err := storage.CreateTeamInvite(db, team.ID, email, owner); err != nil {
		t.Fatalf("invite member: %v", err)
	}
	return owner, member, team, email
}

func pac122AssertNothingRemoved(t *testing.T, owner, member uuid.UUID, team *storage.Team, email string) {
	t.Helper()
	if n := pac122Count(t, `SELECT count(*) FROM team_members WHERE team_id=$1 AND user_id=$2`, team.ID, member); n != 1 {
		t.Errorf("team membership rows = %d, want 1 (nothing removed)", n)
	}
	if n := pac122Count(t, `SELECT count(*) FROM manager_team_member_assignments WHERE manager_user_id=$1 AND team_id=$2 AND member_email=$3`, owner, team.ID, email); n != 1 {
		t.Errorf("assignment rows = %d, want 1 (nothing removed)", n)
	}
	if n := pac122Count(t, `SELECT count(*) FROM team_invites WHERE team_id=$1 AND invitee_email=$2 AND status='pending'`, team.ID, email); n != 1 {
		t.Errorf("pending invites = %d, want 1 (nothing removed)", n)
	}
}

// SF-001: the last removal write (the pending-invite delete) fails. The
// answer is the 500, and the earlier writes (membership, assignment) are
// rolled back with it: a failure removes nothing.
func TestPAC122_SF001_FailedRemovalWriteRemovesNothing(t *testing.T) {
	r := setupManagerRoutes(t, &fakeManagerWorkflow{})
	owner, member, team, email := pac122ScopedRemoval(t, "pac122-sf001-write")
	pac122FailDeletes(t, "team_invites", "invitee_email", email)
	reported, _ := captureServerErrors(t)

	w := managerRequest(t, r, http.MethodDelete, "/api/manager/team/members/"+url.PathEscape(email)+"?team_id="+team.ID.String(), owner, nil)
	pac122AssertServerError(t, w, reported)
	pac122AssertNothingRemoved(t, owner, member, team, email)
}

// SF-001: the membership read fails with something other than "no row".
// That is a 500, not "not a member": the target is not silently skipped and
// nothing is removed.
func TestPAC122_SF001_MembershipReadErrorIs500(t *testing.T) {
	r := setupManagerRoutes(t, &fakeManagerWorkflow{})
	owner, member, team, email := pac122ScopedRemoval(t, "pac122-sf001-read")
	storage.InstallTeamMemberRead(t, func(teamID, userID uuid.UUID) error {
		if teamID == team.ID && userID == member {
			return errors.New("pac122: membership read failed")
		}
		return nil
	})
	reported, _ := captureServerErrors(t)

	w := managerRequest(t, r, http.MethodDelete, "/api/manager/team/members/"+url.PathEscape(email)+"?team_id="+team.ID.String(), owner, nil)
	pac122AssertServerError(t, w, reported)
	pac122AssertNothingRemoved(t, owner, member, team, email)
}

// SF-001: a non-member is still "not a member" (sql.ErrNoRows), so the
// assignment and invite are removed and the answer stays 204.
func TestPAC122_SF001_NonMemberStillRemovesAssignmentAndInvite(t *testing.T) {
	r := setupManagerRoutes(t, &fakeManagerWorkflow{})
	db := openTestDB(t)
	owner := createConfirmedUser(t, "pac122-sf001-nm-owner@example.com")
	email := "pac122-sf001-nm-outsider@example.com"
	createConfirmedUser(t, email) // carries the address, not a team member
	team := createManagerTestTeam(t, owner, "pac122 non-member")
	if err := storage.UpsertManagerTeamMemberForTeam(db, team.ID, &storage.ManagerTeamMember{
		ManagerUserID: owner, MemberEmail: email, DisplayName: "Outsider", Source: "manual", Cadence: "weekly",
	}); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if _, err := storage.CreateTeamInvite(db, team.ID, email, owner); err != nil {
		t.Fatalf("invite: %v", err)
	}
	w := managerRequest(t, r, http.MethodDelete, "/api/manager/team/members/"+url.PathEscape(email)+"?team_id="+team.ID.String(), owner, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body: %s", w.Code, w.Body.String())
	}
	if n := pac122Count(t, `SELECT count(*) FROM manager_team_member_assignments WHERE team_id=$1 AND member_email=$2`, team.ID, email); n != 0 {
		t.Errorf("assignment rows = %d, want 0", n)
	}
	if n := pac122Count(t, `SELECT count(*) FROM team_invites WHERE team_id=$1 AND invitee_email=$2 AND status='pending'`, team.ID, email); n != 0 {
		t.Errorf("pending invites = %d, want 0", n)
	}
}

// SF-001, global path (no team_id): a failed delete of the roster row is the
// 500 and the row stays.
func TestPAC122_SF001_GlobalDeleteFailureIs500(t *testing.T) {
	r := setupManagerRoutes(t, &fakeManagerWorkflow{})
	manager := createTestUser(t, "pac122-sf001-global@example.com")
	email := "pac122-sf001-global-report@example.com"
	if err := storage.UpsertManagerTeamMember(openTestDB(t), &storage.ManagerTeamMember{
		ManagerUserID: manager, MemberEmail: email, DisplayName: "Report", Source: "manual", Cadence: "weekly",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	pac122FailDeletes(t, "manager_team_members", "member_email", email)
	reported, _ := captureServerErrors(t)

	w := managerRequest(t, r, http.MethodDelete, "/api/manager/team/members/"+url.PathEscape(email), manager, nil)
	pac122AssertServerError(t, w, reported)
	if n := pac122Count(t, `SELECT count(*) FROM manager_team_members WHERE manager_user_id=$1 AND member_email=$2`, manager, email); n != 1 {
		t.Errorf("roster rows = %d, want 1", n)
	}
}

// pac122ExpiredInvite seeds a pending invite whose expires_at is in the past,
// as when the expiry sweep has not (or could not) run.
func pac122ExpiredInvite(t *testing.T, prefix string) (invitee uuid.UUID, team *storage.Team, token string) {
	t.Helper()
	db := openTestDB(t)
	owner := createConfirmedUser(t, prefix+"-owner@example.com")
	email := prefix + "-invitee@example.com"
	invitee = createConfirmedUser(t, email)
	team = createManagerTestTeam(t, owner, prefix+" team")
	inv, err := storage.CreateTeamInvite(db, team.ID, email, owner)
	if err != nil {
		t.Fatalf("invite: %v", err)
	}
	if _, err := db.Exec(`UPDATE team_invites SET expires_at = now() - interval '1 hour' WHERE id=$1`, inv.ID); err != nil {
		t.Fatalf("expire: %v", err)
	}
	return invitee, team, inv.Token
}

// pac122FailSweep makes ExpireOldInvites fail without writing, so the read
// that follows still works.
func pac122FailSweep(t *testing.T) {
	t.Helper()
	storage.InstallInviteSweep(t, func() error { return errors.New("pac122: invite sweep failed") })
}

func pac122AssertExpired(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Code != http.StatusGone {
		t.Fatalf("status = %d, want 410; body: %s", w.Code, w.Body.String())
	}
	if got := strings.TrimSpace(w.Body.String()); got != `{"error":"invite is expired"}` {
		t.Errorf("body = %s, want {\"error\":\"invite is expired\"}", got)
	}
}

func pac122AssertSweepReported(t *testing.T, reported *[]error, logs fmt.Stringer) {
	t.Helper()
	found := false
	for _, err := range *reported {
		if strings.Contains(err.Error(), "invite sweep failed") {
			found = true
		}
	}
	if !found {
		t.Errorf("the sweep failure was not reported to Sentry (reported %v)", *reported)
	}
	if !strings.Contains(logs.String(), "invite sweep failed") {
		t.Errorf("the sweep failure was not logged; log: %s", logs.String())
	}
}

// SF-002: the sweep fails and the invite is past expires_at. The preview
// still answers today's 410 "invite is expired", and the sweep failure is
// logged and reported.
func TestPAC122_SF002_PreviewRefusesExpiredInviteWhenSweepFails(t *testing.T) {
	r := setupTeamRoutes(t)
	invitee, _, token := pac122ExpiredInvite(t, "pac122-sf002-preview")
	pac122FailSweep(t)
	reported, logs := captureServerErrors(t)

	req := withUser(httptest.NewRequest(http.MethodGet, "/api/teams/invites/"+token, nil), invitee)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	pac122AssertExpired(t, w)
	pac122AssertSweepReported(t, reported, logs)
}

// SF-002: the sweep fails and the invite is past expires_at. Accept answers
// 410 "invite is expired" and no membership is created.
func TestPAC122_SF002_AcceptRefusesExpiredInviteWhenSweepFails(t *testing.T) {
	r := setupTeamRoutes(t)
	invitee, team, token := pac122ExpiredInvite(t, "pac122-sf002-accept")
	pac122FailSweep(t)
	reported, logs := captureServerErrors(t)

	req := withUser(httptest.NewRequest(http.MethodPost, "/api/teams/invites/"+token+"/accept", nil), invitee)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	pac122AssertExpired(t, w)
	pac122AssertSweepReported(t, reported, logs)
	if _, err := storage.GetTeamMember(openTestDB(t), team.ID, invitee); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("the invitee became a member (err=%v)", err)
	}
}

// SF-002: a failed sweep does not refuse an invite that is still valid; it
// is decided by expires_at alone, and the failure is still reported.
func TestPAC122_SF002_SweepFailureDoesNotRefuseAValidInvite(t *testing.T) {
	r := setupTeamRoutes(t)
	db := openTestDB(t)
	owner := createConfirmedUser(t, "pac122-sf002-valid-owner@example.com")
	email := "pac122-sf002-valid-invitee@example.com"
	invitee := createConfirmedUser(t, email)
	team := createManagerTestTeam(t, owner, "pac122 valid")
	inv, err := storage.CreateTeamInvite(db, team.ID, email, owner)
	if err != nil {
		t.Fatalf("invite: %v", err)
	}
	pac122FailSweep(t)
	reported, logs := captureServerErrors(t)

	req := withUser(httptest.NewRequest(http.MethodPost, "/api/teams/invites/"+inv.Token+"/accept", nil), invitee)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body: %s", w.Code, w.Body.String())
	}
	pac122AssertSweepReported(t, reported, logs)
	if _, err := storage.GetTeamMember(db, team.ID, invitee); err != nil {
		t.Errorf("the invitee is not a member: %v", err)
	}
}

// SF-003: a failed disconnect is the documented 500, and the stored token
// stays (the client must not be told it is gone).
func TestPAC122_SF003_FailedDisconnectIs500(t *testing.T) {
	for _, tc := range []struct {
		provider string
		handler  func(h *integrationsHandlers) http.HandlerFunc
	}{
		{"slack", func(h *integrationsHandlers) http.HandlerFunc { return h.slackDisconnect }},
		{"notion", func(h *integrationsHandlers) http.HandlerFunc { return h.notionDisconnect }},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			db := openTestDB(t)
			user := createTestUser(t, "pac122-sf003-"+tc.provider+"@example.com")
			if err := storage.UpsertWorkspaceConnection(db, &storage.WorkspaceConnection{
				UserID: user, Provider: tc.provider, AccessToken: "tok", Scopes: []string{"chat:write"}, ConnectedAt: time.Now(),
			}); err != nil {
				t.Fatalf("seed connection: %v", err)
			}
			pac122FailDeletes(t, "workspace_connections", "user_id", user.String())
			reported, _ := captureServerErrors(t)

			h := &integrationsHandlers{db: db}
			req := withUser(httptest.NewRequest(http.MethodDelete, "/api/integrations/"+tc.provider, nil), user)
			w := httptest.NewRecorder()
			tc.handler(h)(w, req)
			pac122AssertServerError(t, w, reported)
			if n := pac122Count(t, `SELECT count(*) FROM workspace_connections WHERE user_id=$1 AND provider=$2`, user, tc.provider); n != 1 {
				t.Errorf("connection rows = %d, want 1 (token still stored)", n)
			}
		})
	}
}
