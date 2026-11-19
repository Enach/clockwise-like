package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

// PAC-150 (docs/factory/silent-failures.md SF-114): Manager patch and
// schedule, scoped to a team, match {email} to a formal member of the team
// only when that member's account holds the address domain-confirmed
// (PAC-71). A failed read in that match was read as "not matched", so a
// database fault told the manager the person was not on the team (404). It is
// the documented 500 now; a genuine no-match is still the 404.

// pac150Formal is a PAC-130 fixture with formal member R of team T (R's
// account holds its address domain-confirmed) and no roster entry for R.
func pac150Formal(t *testing.T) (*pac130Fix, string) {
	t.Helper()
	f := newPAC130(t)
	email := "formal@" + f.ext
	r := f.account(email, true, true, 0)
	if err := storage.AddTeamMember(f.db, f.team.ID, r, "member"); err != nil {
		t.Fatal(err)
	}
	return f, email
}

func pac150Patch(f *pac130Fix, email string) *httptest.ResponseRecorder {
	return managerRequest(f.t, f.r, http.MethodPatch, "/api/manager/team/members/"+url.PathEscape(email)+f.scope(), f.v, map[string]any{"cadence": "weekly"})
}

func pac150Schedule(f *pac130Fix, email string) *httptest.ResponseRecorder {
	return managerRequest(f.t, f.r, http.MethodPost, "/api/manager/team/members/"+url.PathEscape(email)+"/schedule"+f.scope(), f.v, nil)
}

// pac150AssertNoPersonalData fails when the address reached the answer, the
// log line or the Sentry report.
func pac150AssertNoPersonalData(t *testing.T, email string, w *httptest.ResponseRecorder, reported *[]error, logs string) {
	t.Helper()
	local := strings.Split(email, "@")[0]
	if strings.Contains(w.Body.String(), email) {
		t.Errorf("the answer carries the address: %s", w.Body.String())
	}
	if strings.Contains(logs, email) || strings.Contains(logs, local+"%40") {
		t.Errorf("the log line carries the address: %s", logs)
	}
	for _, err := range *reported {
		if strings.Contains(err.Error(), email) {
			t.Errorf("the report carries the address: %v", err)
		}
	}
}

// A failed holder lookup in the formal-member match is the 500, not the 404,
// for patch and for schedule; nothing is written.
func TestPAC150_HolderLookupFailureIs500(t *testing.T) {
	for name, call := range map[string]func(*pac130Fix, string) *httptest.ResponseRecorder{
		"patch": pac150Patch, "schedule": pac150Schedule,
	} {
		t.Run(name, func(t *testing.T) {
			f, email := pac150Formal(t)
			storage.InstallConfirmedHolderLookup(t, func(e string) error {
				if strings.EqualFold(e, email) {
					return errors.New("pac150: holder lookup failed")
				}
				return nil
			})
			reported, logs := captureServerErrors(t)
			w := call(f, email)
			pac122AssertServerError(t, w, reported)
			pac150AssertNoPersonalData(t, email, w, reported, logs.String())
			if n := pac122Count(t, `SELECT count(*) FROM manager_team_members WHERE manager_user_id=$1 AND member_email=$2`, f.v, email); n != 0 {
				t.Errorf("roster rows = %d, want 0", n)
			}
			if n := pac122Count(t, `SELECT count(*) FROM manager_team_member_assignments WHERE manager_user_id=$1 AND member_email=$2`, f.v, email); n != 0 {
				t.Errorf("assignments = %d, want 0", n)
			}
		})
	}
}

// The other reads of the match (the formal-member list and the added-person
// read) were already checked; pinned here so the path stays closed.
func TestPAC150_MemberListFailureIs500(t *testing.T) {
	for name, call := range map[string]func(*pac130Fix, string) *httptest.ResponseRecorder{
		"patch": pac150Patch, "schedule": pac150Schedule,
	} {
		t.Run(name, func(t *testing.T) {
			f, email := pac150Formal(t)
			team := f.team.ID
			storage.InstallTeamMembersListRead(t, func(id uuid.UUID) error {
				if id == team {
					return errors.New("pac150: member list failed")
				}
				return nil
			})
			reported, logs := captureServerErrors(t)
			w := call(f, email)
			pac122AssertServerError(t, w, reported)
			pac150AssertNoPersonalData(t, email, w, reported, logs.String())
		})
	}
}

// A genuine no-match stays the 404: an address nobody holds, and a formal
// member whose address is not domain-confirmed (PAC-71). With the lookup
// working, the formal member is matched and the answer is 200.
func TestPAC150_NoMatchIs404(t *testing.T) {
	for name, call := range map[string]func(*pac130Fix, string) *httptest.ResponseRecorder{
		"patch": pac150Patch, "schedule": pac150Schedule,
	} {
		t.Run(name, func(t *testing.T) {
			f, email := pac150Formal(t)
			if w := call(f, f.n); w.Code != http.StatusNotFound {
				t.Errorf("nobody = %d %s, want 404", w.Code, w.Body.String())
			}
			unconfirmed := "unconfirmed@" + f.ext
			u := f.account(unconfirmed, false, true, 0)
			if err := storage.AddTeamMember(f.db, f.team.ID, u, "member"); err != nil {
				t.Fatal(err)
			}
			if w := call(f, unconfirmed); w.Code != http.StatusNotFound {
				t.Errorf("unconfirmed formal member = %d %s, want 404", w.Code, w.Body.String())
			}
			if w := call(f, email); w.Code != http.StatusOK {
				t.Errorf("confirmed formal member = %d %s, want 200", w.Code, w.Body.String())
			}
		})
	}
}
