package storage

import (
	"testing"
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
