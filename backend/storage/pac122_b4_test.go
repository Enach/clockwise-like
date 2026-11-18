package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// PAC-122 phase 2, batch 4 (docs/factory/silent-failures.md SF-011): weekly
// detection (an "auto" upsert) never replaces the cadence of a row the
// manager set by hand ("manual"); every other combination still takes the
// new cadence.
func TestPAC122_SF011_DetectionKeepsAManualCadence(t *testing.T) {
	db := openTestDB(t)
	mgr, err := UpsertUser(db, "pac122-sf011@example.com", "M", "", "google", "sub-pac122-sf011")
	if err != nil {
		t.Fatal(err)
	}
	days := 10
	read := func(email string) *ManagerTeamMember {
		t.Helper()
		m, err := GetManagerTeamMemberByEmail(db, mgr.ID, email)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}

	// A manual row with a custom cadence, then detection says "weekly".
	if err := UpsertManagerTeamMember(db, &ManagerTeamMember{ManagerUserID: mgr.ID, MemberEmail: "manual@example.com", DisplayName: "Manual", Source: "manual", Cadence: "custom", CadenceCustomDays: &days}); err != nil {
		t.Fatal(err)
	}
	if err := UpsertManagerTeamMember(db, &ManagerTeamMember{ManagerUserID: mgr.ID, MemberEmail: "manual@example.com", DisplayName: "Detected", Source: "auto", Cadence: "weekly"}); err != nil {
		t.Fatal(err)
	}
	if m := read("manual@example.com"); m.Cadence != "custom" || m.CadenceCustomDays == nil || *m.CadenceCustomDays != days || m.Source != "manual" || m.DisplayName != "Manual" {
		t.Fatalf("manual row after detection = %+v (custom days %v), want custom/%d kept", m, m.CadenceCustomDays, days)
	}

	// An auto row still follows detection.
	if err := UpsertManagerTeamMember(db, &ManagerTeamMember{ManagerUserID: mgr.ID, MemberEmail: "auto@example.com", DisplayName: "Auto", Source: "auto", Cadence: "weekly"}); err != nil {
		t.Fatal(err)
	}
	if err := UpsertManagerTeamMember(db, &ManagerTeamMember{ManagerUserID: mgr.ID, MemberEmail: "auto@example.com", DisplayName: "Auto", Source: "auto", Cadence: "monthly"}); err != nil {
		t.Fatal(err)
	}
	if m := read("auto@example.com"); m.Cadence != "monthly" {
		t.Fatalf("auto row cadence = %q, want monthly", m.Cadence)
	}

	// A manual write over any row takes the manual cadence.
	if err := UpsertManagerTeamMember(db, &ManagerTeamMember{ManagerUserID: mgr.ID, MemberEmail: "manual@example.com", DisplayName: "Manual", Source: "manual", Cadence: "biweekly"}); err != nil {
		t.Fatal(err)
	}
	if m := read("manual@example.com"); m.Cadence != "biweekly" || m.CadenceCustomDays != nil {
		t.Fatalf("manual over manual = %+v, want biweekly, no custom days", m)
	}
}

// pac122b4FailOwnerInserts makes every team_members INSERT for userID fail
// until the test ends.
func pac122b4FailOwnerInserts(t *testing.T, db *sql.DB, userID uuid.UUID) {
	t.Helper()
	name := "pac122b4_fail_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := db.Exec(fmt.Sprintf(`CREATE FUNCTION %[1]s() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.user_id = '%[2]s'::uuid THEN RAISE EXCEPTION 'pac122: forced owner insert failure'; END IF;
			RETURN NEW;
		END $$`, name, userID)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(fmt.Sprintf(`CREATE TRIGGER %[1]s BEFORE INSERT ON team_members FOR EACH ROW EXECUTE FUNCTION %[1]s()`, name)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON team_members`, name))
		_, _ = db.Exec(fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, name))
	})
}

// SF-008, the joined path: with a session guard, CreateTeamCtx runs inside
// the PAC-71 re-check's transaction. The team and its owner row are written
// together, and a failed owner insert rolls the team back with it.
func TestPAC122_SF008_CreateTeamUnderSessionGuard(t *testing.T) {
	db := openTestDB(t)
	u := pac71User(t, db, "pac122-sf008-guard@example.com", "")
	ctx := WithSessionGuard(context.Background(), &SessionGuard{UserID: u, Gen: 0})

	team, err := CreateTeamCtx(ctx, db, "guarded", u)
	if err != nil {
		t.Fatal(err)
	}
	if m, err := GetTeamMember(db, team.ID, u); err != nil || m.Role != "owner" {
		t.Fatalf("owner row = %+v, %v; want owner", m, err)
	}

	pac122b4FailOwnerInserts(t, db, u)
	if team, err := CreateTeamCtx(ctx, db, "guarded-fail", u); err == nil {
		t.Fatalf("CreateTeamCtx = %+v, nil; want the owner insert error", team)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM teams WHERE created_by=$1 AND name='guarded-fail'`, u).Scan(&n); err != nil || n != 0 {
		t.Fatalf("teams left = %d (%v), want 0", n, err)
	}
}
