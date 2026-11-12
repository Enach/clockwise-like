package api

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/storage"
	"golang.org/x/oauth2"
)

// PAC-71, review of #242: a binding that lands MID-HANDLER, between one
// storage call of the request and the next, or after the handler spawned
// background work. storage.GuardedCallHook runs before every in-request
// re-check; bindAtGuardedCall commits Henry's real rule (b) binding at the
// n-th one (every concurrent caller waits for it), so everything from that
// call on runs after the binding.

func bindAtGuardedCall(t *testing.T, n int64, d string) *atomic.Int64 {
	t.Helper()
	var calls atomic.Int64
	var once sync.Once
	storage.GuardedCallHook = func(context.Context) {
		if calls.Add(1) >= n {
			once.Do(func() {
				sessionOf(t, pac71Google(t, googleProfile(pac71GoogleID(), "henry@"+d, true, d)))
			})
		}
	}
	t.Cleanup(func() { storage.GuardedCallHook = nil })
	return &calls
}

// BLOCKING 1: the Google disconnect reads the settings (re-check 1, current)
// and then deletes the credential (re-check 2). The binding commits between
// them: 401, and Henry's credential is intact.
func TestPAC71_MidHandler_DisconnectAfterTheBindingKeepsHenrysCredential(t *testing.T) {
	r := pac71Router(t, &oauth2.Config{})
	d := pac71Domain()
	a := pac71Legacy(t, "henry@"+d, "google", "")
	bindAtGuardedCall(t, 2, d)
	assert401(t, doAs(t, r, http.MethodDelete, "/api/auth/disconnect", "", sessionCookie(t, a, 0)))
	st := readAccount(t, a)
	if st.identity == "" || st.gen != 1 {
		t.Fatalf("A = %+v, want bound", st)
	}
	if st.googleAccess == "" {
		t.Fatal("the old-generation disconnect removed Henry's credential")
	}
}

// The same disconnect after the operator's reassign with
// -keep-calendar-credentials, which advances the generation and keeps the
// credential.
func TestPAC71_MidHandler_DisconnectAfterAReassignKeepsTheCredential(t *testing.T) {
	r := pac71Router(t, &oauth2.Config{})
	d := pac71Domain()
	g1 := pac71GoogleID()
	a := pac71Bound(t, "henry@"+d, googleIdentity(g1), storage.AddressDomainConfirmed)
	seedGoogleToken(t, a, "henry-kept")
	var once sync.Once
	var calls atomic.Int64
	storage.GuardedCallHook = func(context.Context) {
		if calls.Add(1) >= 2 {
			once.Do(func() {
				if _, err := storage.ReassignAccount(context.Background(), openTestDB(t), storage.ReassignRequest{
					Account: a, Identity: googleIdentity(g1), Evidence: storage.EvidenceAuthoritative, KeepCalendarCredentials: true,
					Run: storage.OperatorRun{Operator: "op", Reason: "test"},
				}); err != nil {
					t.Error(err)
				}
			})
		}
	}
	t.Cleanup(func() { storage.GuardedCallHook = nil })
	assert401(t, doAs(t, r, http.MethodDelete, "/api/auth/disconnect", "", sessionCookie(t, a, 0)))
	if readAccount(t, a).googleAccess != "henry-kept" {
		t.Fatal("the credential kept by the operator was removed")
	}
}

// BLOCKING 2: turning manager mode on spawns team detection. The binding
// commits at the first re-check, whichever of the handler and the spawned
// detection reaches one first: the detection must make no Google call with
// Henry's credential and write no manager rows into A.
func TestPAC71_MidHandler_SpawnedDetectionKeepsTheSessionGuard(t *testing.T) {
	cal := newPAC67Calendar(t)
	r := pac71Router(t, &oauth2.Config{Endpoint: oauth2.Endpoint{TokenURL: "http://127.0.0.1:1/token"}})
	d := pac71Domain()
	a := pac71Legacy(t, "henry@"+d, "google", "")
	calls := bindAtGuardedCall(t, 1, d)
	assert401(t, doAs(t, r, http.MethodPost, "/api/manager/profile", `{"is_manager":true}`, sessionCookie(t, a, 0)))
	// The handler's own write is one re-check; the detection's credential load
	// is the other. Wait for both (the detection returns at its first refusal).
	deadline := time.Now().Add(10 * time.Second)
	for calls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if calls.Load() < 2 {
		t.Fatal("the spawned detection never reached the re-check: it runs without the session guard")
	}
	time.Sleep(100 * time.Millisecond) // let a refused detection return
	st := readAccount(t, a)
	if st.googleAccess == "" {
		t.Fatal("the binding did not commit")
	}
	if cal.saw(st.googleAccess) {
		t.Fatal("the detection called Google with Henry's credential")
	}
	var roster, profiles int
	_ = openTestDB(t).QueryRow(`SELECT (SELECT count(*) FROM manager_team_members WHERE manager_user_id = $1),
		(SELECT count(*) FROM user_profiles WHERE user_id = $1 AND (is_manager OR detected_at IS NOT NULL))`, a).Scan(&roster, &profiles)
	if roster != 0 || profiles != 0 {
		t.Fatalf("manager rows written into A: roster %d, profile %d", roster, profiles)
	}
}
