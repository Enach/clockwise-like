package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

// PAC-80 item 2 (spec docs/specs/PAC-80.md AC-4, AC-5): on a request with a
// session, the default manager-profile row is created only under the
// session re-check. A binding that commits after authentication and before
// the handler's first storage call leaves the account with no profile row.

func pac80ProfileRows(t *testing.T, a uuid.UUID) (profiles, roster int) {
	t.Helper()
	if err := openTestDB(t).QueryRow(`SELECT (SELECT count(*) FROM user_profiles WHERE user_id = $1),
		(SELECT count(*) FROM manager_team_members WHERE manager_user_id = $1)`, a).Scan(&profiles, &roster); err != nil {
		t.Fatal(err)
	}
	return profiles, roster
}

// AC-4: GET /api/manager/profile.
func TestPAC80_GetProfileAfterTheBindingCreatesNoRow(t *testing.T) {
	r := pac71Router(t, &oauth2.Config{})
	d := pac71Domain()
	a := pac71Legacy(t, "henry@"+d, "google", "")
	bindAtGuardedCall(t, 1, d)
	assert401(t, doAs(t, r, http.MethodGet, "/api/manager/profile", "", sessionCookie(t, a, 0)))
	if readAccount(t, a).identity == "" {
		t.Fatal("the binding did not commit")
	}
	if profiles, _ := pac80ProfileRows(t, a); profiles != 0 {
		t.Fatalf("a stale GET created %d profile rows", profiles)
	}
}

// AC-5: POST /api/manager/profile {"is_manager":true}: no profile row, no
// roster row, no team-detection call with the newly bound credential.
func TestPAC80_PostProfileAfterTheBindingCreatesNoRow(t *testing.T) {
	cal := newPAC67Calendar(t)
	r := pac71Router(t, &oauth2.Config{Endpoint: oauth2.Endpoint{TokenURL: "http://127.0.0.1:1/token"}})
	d := pac71Domain()
	a := pac71Legacy(t, "henry@"+d, "google", "")
	calls := bindAtGuardedCall(t, 1, d)
	assert401(t, doAs(t, r, http.MethodPost, "/api/manager/profile", `{"is_manager":true}`, sessionCookie(t, a, 0)))
	time.Sleep(200 * time.Millisecond) // a detection, had one been spawned, would run now
	st := readAccount(t, a)
	if st.googleAccess == "" {
		t.Fatal("the binding did not commit")
	}
	if cal.saw(st.googleAccess) {
		t.Fatal("team detection called Google with Henry's credential")
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("guarded calls = %d, want 1 (the refused read; no detection spawned)", n)
	}
	if profiles, roster := pac80ProfileRows(t, a); profiles != 0 || roster != 0 {
		t.Fatalf("a stale POST wrote profile %d, roster %d", profiles, roster)
	}
}
