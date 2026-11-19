package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/calendar"
	"github.com/Enach/paceday/backend/config"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

// PAC-122 phase 2, batch 4 (docs/factory/silent-failures.md SF-010, SF-040):
// the manager engine's failed reads are errors, never "external" or "no data",
// and nothing computed from them is stored.

func pac122b4Count(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// pac122b4DetectionStub serves two weekly 1:1s of the manager with report.
func pac122b4DetectionStub(t *testing.T, managerEmail, report string) {
	t.Helper()
	now := time.Now().UTC()
	ev := func(id string, at time.Time) map[string]any {
		return map[string]any{
			"id": id, "recurringEventId": "series-1",
			"start": map[string]string{"dateTime": at.Format(time.RFC3339)},
			"end":   map[string]string{"dateTime": at.Add(30 * time.Minute).Format(time.RFC3339)},
			"attendees": []map[string]any{
				{"email": managerEmail, "self": true},
				{"email": report, "displayName": "Report"},
			},
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{ev("e1", now.Add(-14*24*time.Hour)), ev("e2", now.Add(-7*24*time.Hour))}})
	}))
	t.Cleanup(srv.Close)
	calendar.Configure(config.CalendarEndpoint{BaseURL: srv.URL + config.CalendarAPIRoot, Guard: true})
	t.Cleanup(func() { calendar.Configure(config.ProductionCalendarEndpoint()) })
}

// SF-010, rewritten by PAC-130 (was
// TestPAC122_SF010_DetectionHolderLookupErrorStoresNothing; spec D-2,
// contract md §3.3, §7): detection no longer looks a detected address up at
// all, so the guarded lookup is gone. A holder lookup that would fail is never reached: detection succeeds
// and stores the member with no account link (member_user_id NULL), for a
// held address as for any other. SF-010's rule (a failed read is the error)
// moves to the Manager visibility read (engine/pac130_test.go, AC-13).
func TestPAC122_SF010_DetectionLooksNoAddressUp(t *testing.T) {
	db := openTestDB(t)
	mgr, _ := pac81User(t, db, "pac122-sf010-boss@example.com")
	report := "pac122-sf010-report@example.com"
	pac81User(t, db, report)
	pac122b4DetectionStub(t, "pac122-sf010-boss@example.com", report)
	if _, err := db.Exec(`UPDATE users SET address_standing = 'domain_confirmed' WHERE lower(email) = $1`, report); err != nil {
		t.Fatal(err)
	}
	lookups := 0
	storage.InstallConfirmedHolderLookup(t, func(e string) error {
		if strings.EqualFold(e, report) {
			lookups++
			return errors.New("pac122: holder lookup failed")
		}
		return nil
	})

	res, err := (&ManagerEngine{DB: db, OAuthConfig: &oauth2.Config{}}).DetectTeam(context.Background(), mgr)
	if err != nil || res == nil || res.MembersAdded != 1 {
		t.Fatalf("DetectTeam = %+v, %v; want one member added, no error", res, err)
	}
	if lookups != 0 {
		t.Fatalf("holder lookups = %d, want 0 (detection looks no address up)", lookups)
	}
	if n := pac122b4Count(t, db, `SELECT count(*) FROM manager_team_members WHERE manager_user_id=$1 AND member_user_id IS NULL`, mgr); n != 1 {
		t.Fatalf("stored rows without a link = %d, want 1", n)
	}
	if n := pac122b4Count(t, db, `SELECT count(*) FROM manager_team_members WHERE manager_user_id=$1 AND member_user_id IS NOT NULL`, mgr); n != 0 {
		t.Fatalf("stored rows with a link = %d, want 0", n)
	}
}

// SF-040: the future-occurrence read of the gap check fails. GetGaps returns
// the error, never a false "no upcoming 1:1" gap.
func TestPAC122_SF040_GapCheckReadErrorIsAnError(t *testing.T) {
	db := openTestDB(t)
	mgr, _ := pac81User(t, db, "pac122-sf040-gaps@example.com")
	InstallManagerStep(t, func(step, _ string) error {
		if step == StepFutureOccurrences {
			return errors.New("pac122: occurrences read failed")
		}
		return nil
	})
	members := []*storage.ManagerTeamMember{{ManagerUserID: mgr, MemberEmail: "r@example.com", Cadence: "weekly"}}
	gaps, err := (&ManagerEngine{DB: db}).GetGaps(context.Background(), mgr, members)
	if err == nil {
		t.Fatalf("GetGaps = %+v, nil; want the read error", gaps)
	}
}

// pac122b4PacedayMember seeds a manager who owns a team with a Paceday member.
func pac122b4PacedayMember(t *testing.T, db *sql.DB, prefix string) (uuid.UUID, *storage.ManagerTeamMember) {
	t.Helper()
	mgr, _ := pac81User(t, db, prefix+"-mgr@example.com")
	email := prefix + "-member@example.com"
	member, _ := pac81User(t, db, email)
	team, err := storage.CreateTeam(db, prefix, mgr)
	if err != nil {
		t.Fatal(err)
	}
	for u, role := range map[uuid.UUID]string{mgr: "owner", member: "member"} {
		if err := storage.AddTeamMember(db, team.ID, u, role); err != nil {
			t.Fatal(err)
		}
	}
	if err := storage.UpsertUserProfile(db, &storage.UserProfile{UserID: member, AnalyticsSharedWithManager: true}); err != nil {
		t.Fatal(err)
	}
	// PAC-130: the manual entry's account is the address's single
	// domain-confirmed holder, found by the branch decision (no stored link).
	if _, err := db.Exec(`UPDATE users SET address_standing = 'domain_confirmed' WHERE id = $1`, member); err != nil {
		t.Fatal(err)
	}
	return mgr, &storage.ManagerTeamMember{ManagerUserID: mgr, MemberEmail: email, Source: "manual", Cadence: "weekly"}
}

// SF-040: each failed step of a Paceday member's week is the error, and no
// figure is cached: before the fix each read as "no data" and was cached
// for four hours (a failed owner check read as "no data", uncached).
func TestPAC122_SF040_MemberWeekStepErrorsAreErrors(t *testing.T) {
	db := openTestDB(t)
	week := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	for _, step := range []string{StepOwnerCheck, StepCacheRead, StepConsentRead, StepAnalyticsWeekRead, StepCacheWrite} {
		t.Run(step, func(t *testing.T) {
			mgr, m := pac122b4PacedayMember(t, db, "pac122-sf040-"+step)
			InstallManagerStep(t, func(s, email string) error {
				if s == step && email == m.MemberEmail {
					return errors.New("pac122: " + step + " failed")
				}
				return nil
			})
			stats, err := (&ManagerEngine{DB: db}).GetMemberWeek(context.Background(), mgr, m, week)
			if err == nil {
				t.Fatalf("GetMemberWeek = %+v, nil; want the %s error", stats, step)
			}
			if strings.Contains(err.Error(), "@") {
				t.Fatalf("error names an address: %v", err)
			}
			if n := pac122b4Count(t, db, `SELECT count(*) FROM team_analytics_cache WHERE manager_user_id=$1`, mgr); n != 0 {
				t.Fatalf("cached rows = %d, want 0", n)
			}
		})
	}
}

// SF-040: with no failure the Paceday member's week is read and cached as
// before (no analytics row is a real "no data").
func TestPAC122_SF040_MemberWeekStillCachesARealAnswer(t *testing.T) {
	db := openTestDB(t)
	mgr, m := pac122b4PacedayMember(t, db, "pac122-sf040-ok")
	week := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	stats, err := (&ManagerEngine{DB: db}).GetMemberWeek(context.Background(), mgr, m, week)
	if err != nil || stats == nil || stats.DataAvailable {
		t.Fatalf("GetMemberWeek = %+v, %v; want no data, no error", stats, err)
	}
	if n := pac122b4Count(t, db, `SELECT count(*) FROM team_analytics_cache WHERE manager_user_id=$1`, mgr); n != 1 {
		t.Fatalf("cached rows = %d, want 1", n)
	}
}

// SF-040: an external member whose free/busy cannot be read (here, the
// manager has no calendar credential: coverage unknown) is "no data" for
// this read only; nothing is cached, so the next read asks again. A failed
// free/busy step is the error.
func TestPAC122_SF040_ExternalUnknownCoverageIsNotCached(t *testing.T) {
	db := openTestDB(t)
	u, err := storage.UpsertUser(db, "pac122-sf040-ext-mgr@example.com", "M", "", "google", "sub-pac122-sf040-ext")
	if err != nil {
		t.Fatal(err)
	}
	m := &storage.ManagerTeamMember{ManagerUserID: u.ID, MemberEmail: "someone@pac122-ext.example", Source: "manual", Cadence: "weekly"}
	week := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	eng := &ManagerEngine{DB: db, OAuthConfig: &oauth2.Config{}}

	stats, err := eng.GetMemberWeek(context.Background(), u.ID, m, week)
	if err != nil || stats == nil || stats.DataAvailable {
		t.Fatalf("GetMemberWeek = %+v, %v; want no data, no error", stats, err)
	}
	if n := pac122b4Count(t, db, `SELECT count(*) FROM team_analytics_cache WHERE manager_user_id=$1`, u.ID); n != 0 {
		t.Fatalf("cached rows = %d, want 0 (unknown coverage is not a fact about the week)", n)
	}

	InstallManagerStep(t, func(s, _ string) error {
		if s == StepFreeBusy {
			return errors.New("pac122: free/busy failed")
		}
		return nil
	})
	if stats, err := eng.GetMemberWeek(context.Background(), u.ID, m, week); err == nil {
		t.Fatalf("GetMemberWeek = %+v, nil; want the free/busy error", stats)
	}
}
