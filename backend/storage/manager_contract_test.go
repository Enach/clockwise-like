package storage

import (
	"database/sql"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/google/uuid"
)

func contractUser(t *testing.T, db interface {
	QueryRow(string, ...interface{}) *sql.Row
}, email string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := db.QueryRow(`INSERT INTO users (email, name) VALUES ($1,$2) RETURNING id`, email, email).Scan(&id); err != nil {
		t.Fatalf("create user: %v", err)
	}
	return id
}

func contractTeam(t *testing.T, db *sql.DB, owner uuid.UUID, name string) uuid.UUID {
	t.Helper()
	team, err := CreateTeam(db, name, owner)
	if err != nil {
		t.Fatalf("create team: %v", err)
	}
	if err := AddTeamMember(db, team.ID, owner, "owner"); err != nil {
		t.Fatalf("add owner: %v", err)
	}
	return team.ID
}

func TestPACE023ConfirmAssignmentsIsIdempotentAndTeamIsolated(t *testing.T) {
	db := openTestDB(t)
	manager := contractUser(t, db, "contract-manager@example.com")
	teamA := contractTeam(t, db, manager, "Contract A")
	teamB := contractTeam(t, db, manager, "Contract B")
	member := &ManagerTeamMember{
		ManagerUserID: manager, MemberEmail: "  PERSON@EXAMPLE.COM ", DisplayName: "Canonical Person",
		Source: "auto", Cadence: "weekly",
	}
	if err := UpsertManagerTeamMember(db, member); err != nil {
		t.Fatalf("upsert identity: %v", err)
	}

	assigned, skipped, total, err := ConfirmManagerTeamMembers(db, manager, teamA, []string{"PERSON@example.com"})
	if err != nil || assigned != 1 || skipped != 0 || total != 1 {
		t.Fatalf("first confirm = (%d,%d,%d,%v)", assigned, skipped, total, err)
	}
	assigned, skipped, total, err = ConfirmManagerTeamMembers(db, manager, teamA, []string{"person@example.com"})
	if err != nil || assigned != 0 || skipped != 1 || total != 1 {
		t.Fatalf("repeat confirm = (%d,%d,%d,%v)", assigned, skipped, total, err)
	}
	membersB, err := ListManagerTeamMembersForTeam(db, manager, teamB)
	if err != nil || len(membersB) != 0 {
		t.Fatalf("team B members = %d, err=%v", len(membersB), err)
	}

	if err := PatchManagerTeamMemberForTeam(db, manager, teamA, "person@example.com", "Team A Name", "monthly", nil); err != nil {
		t.Fatalf("patch team preference: %v", err)
	}
	if _, _, _, err := ConfirmManagerTeamMembers(db, manager, teamB, []string{"person@example.com"}); err != nil {
		t.Fatalf("confirm B: %v", err)
	}
	a, _ := GetManagerTeamMemberByEmailForTeam(db, manager, teamA, "person@example.com")
	b, _ := GetManagerTeamMemberByEmailForTeam(db, manager, teamB, "person@example.com")
	global, _ := GetManagerTeamMemberByEmail(db, manager, "person@example.com")
	if a.DisplayName != "Team A Name" || a.Cadence != "monthly" {
		t.Fatalf("team A preference = %#v", a)
	}
	if b.DisplayName != "Canonical Person" || b.Cadence != "weekly" {
		t.Fatalf("team B preference = %#v", b)
	}
	if global.DisplayName != "Canonical Person" {
		t.Fatalf("global canonical name changed: %q", global.DisplayName)
	}
}

func TestPACE023ConfirmAssignmentsRollsBackAtomically(t *testing.T) {
	db := openTestDB(t)
	manager := contractUser(t, db, "atomic-manager@example.com")
	team := contractTeam(t, db, manager, "Atomic team")
	for _, email := range []string{"first@example.com", "explode@example.com"} {
		if err := UpsertManagerTeamMember(db, &ManagerTeamMember{
			ManagerUserID: manager, MemberEmail: email, DisplayName: email, Source: "auto", Cadence: "none",
		}); err != nil {
			t.Fatalf("seed %s: %v", email, err)
		}
	}
	if _, err := db.Exec(`
		CREATE FUNCTION pace023_reject_assignment() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.member_email = 'explode@example.com' THEN RAISE EXCEPTION 'contract failure'; END IF;
			RETURN NEW;
		END $$;
		CREATE TRIGGER pace023_reject_assignment BEFORE INSERT ON manager_team_member_assignments
		FOR EACH ROW EXECUTE FUNCTION pace023_reject_assignment()`); err != nil {
		t.Fatalf("install failure trigger: %v", err)
	}
	if _, _, _, err := ConfirmManagerTeamMembers(db, manager, team, []string{"first@example.com", "explode@example.com"}); err == nil {
		t.Fatal("confirm unexpectedly succeeded")
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM manager_team_member_assignments WHERE team_id=$1`, team).Scan(&count); err != nil {
		t.Fatalf("count assignments: %v", err)
	}
	if count != 0 {
		t.Fatalf("partial commit left %d assignments", count)
	}
}

func TestPACE023MigrationNormalizesAndMergesLegacyEmailIdentity(t *testing.T) {
	db := openTestDB(t)

	source, err := iofs.New(migrationFiles, "migrations")
	if err != nil {
		t.Fatalf("create migration source: %v", err)
	}
	driver, err := postgres.WithInstance(db, &postgres.Config{})
	if err != nil {
		t.Fatalf("create migration driver: %v", err)
	}
	migrator, err := migrate.NewWithInstance("iofs", source, "postgres", driver)
	if err != nil {
		t.Fatalf("create migrator: %v", err)
	}
	if err := migrator.Steps(-1); err != nil {
		t.Fatalf("return test database to migration 22: %v", err)
	}

	manager := contractUser(t, db, "upgrade-manager@example.com")
	linkedUser := contractUser(t, db, "person@example.com")
	teamA := contractTeam(t, db, manager, "Upgrade A")
	teamB := contractTeam(t, db, manager, "Upgrade B")
	older := time.Now().UTC().Add(-48 * time.Hour)
	newer := older.Add(24 * time.Hour)

	for _, member := range []struct {
		email, name, source, cadence string
		memberUserID                 *uuid.UUID
		stamp                        time.Time
	}{
		{" Person@Example.COM ", "Auto Name", "auto", "weekly", nil, older},
		{"person@example.com", "Manual Canonical", "manual", "custom", &linkedUser, newer},
	} {
		if _, err := db.Exec(`
			INSERT INTO manager_team_members
				(manager_user_id, member_email, member_user_id, display_name, source,
				 cadence, cadence_custom_days, last_one_on_one_at, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,NULL,$7,$7,$7)`, manager, member.email,
			member.memberUserID, member.name, member.source, member.cadence, member.stamp); err != nil {
			t.Fatalf("seed legacy identity %q: %v", member.email, err)
		}
	}
	for _, assignment := range []struct {
		team  uuid.UUID
		email string
	}{
		{teamA, " Person@Example.COM "},
		{teamA, "person@example.com"},
		{teamB, " Person@Example.COM "},
	} {
		if _, err := db.Exec(`
			INSERT INTO manager_team_member_assignments
				(manager_user_id, team_id, member_email)
			VALUES ($1,$2,$3)`, manager, assignment.team, assignment.email); err != nil {
			t.Fatalf("seed legacy assignment %q: %v", assignment.email, err)
		}
	}

	if err := migrator.Steps(1); err != nil {
		t.Fatalf("apply migration 23 to dirty legacy data: %v", err)
	}

	var memberCount int
	var email, displayName, sourceName, globalCadence string
	var memberUserID uuid.UUID
	if err := db.QueryRow(`SELECT count(*) FROM manager_team_members WHERE manager_user_id=$1`,
		manager).Scan(&memberCount); err != nil {
		t.Fatalf("count consolidated identity: %v", err)
	}
	if err := db.QueryRow(`
		SELECT member_email, display_name, source, member_user_id, cadence
		FROM manager_team_members WHERE manager_user_id=$1`, manager).Scan(
		&email, &displayName, &sourceName, &memberUserID, &globalCadence); err != nil {
		t.Fatalf("read consolidated identity: %v", err)
	}
	if memberCount != 1 || email != "person@example.com" ||
		displayName != "Manual Canonical" || sourceName != "manual" || memberUserID != linkedUser {
		t.Fatalf("consolidated identity=(%d,%q,%q,%q,%s)",
			memberCount, email, displayName, sourceName, memberUserID)
	}

	if globalCadence != "none" {
		t.Fatalf("invalid global legacy cadence not sanitized: %q", globalCadence)
	}
	var assignmentCount int
	if err := db.QueryRow(`
		SELECT count(*) FROM manager_team_member_assignments
		WHERE manager_user_id=$1 AND member_email='person@example.com'`, manager,
	).Scan(&assignmentCount); err != nil {
		t.Fatalf("count consolidated assignments: %v", err)
	}
	if assignmentCount != 2 {
		t.Fatalf("consolidated assignments=%d, want one per team", assignmentCount)
	}
	var cadence string
	var customDays *int
	if err := db.QueryRow(`
		SELECT cadence, cadence_custom_days FROM manager_team_member_assignments
		WHERE manager_user_id=$1 AND team_id=$2`, manager, teamA).Scan(&cadence, &customDays); err != nil {
		t.Fatalf("read team A preference: %v", err)
	}
	if cadence != "weekly" || customDays != nil {
		t.Fatalf("valid legacy team cadence not preserved: %q/%v", cadence, customDays)
	}

	teamC := contractTeam(t, db, manager, "Upgrade C")
	assigned, skipped, total, err := ConfirmManagerTeamMembers(
		db, manager, teamC, []string{"PERSON@example.com"},
	)
	if err != nil || assigned != 1 || skipped != 0 || total != 1 {
		t.Fatalf("confirm normalized identity after upgrade=(%d,%d,%d,%v)",
			assigned, skipped, total, err)
	}
	if _, err := db.Exec(`
		INSERT INTO manager_team_member_assignments
			(manager_user_id, team_id, member_email)
		VALUES ($1,$2,'missing@example.com')`, manager, teamA); err == nil {
		t.Fatal("assignment FK no longer enforced after consolidation")
	}
	if _, err := db.Exec(`
		INSERT INTO manager_team_members
			(manager_user_id, member_email, display_name, source, cadence)
		VALUES ($1,' Mixed@Example.com ','Mixed','manual','none')`, manager); err == nil {
		t.Fatal("normalized-email check no longer enforced")
	}

	if err := migrator.Steps(-1); err != nil {
		t.Fatalf("roll migration 23 back to 22: %v", err)
	}
	version, dirty, err := migrator.Version()
	if err != nil {
		t.Fatalf("read migration version after rollback: %v", err)
	}
	if version != 22 || dirty {
		t.Fatalf("migration state after rollback=(%d, dirty=%t), want 22/false", version, dirty)
	}

	var addedColumnCount int
	if err := db.QueryRow(`
		SELECT count(*)
		FROM information_schema.columns
		WHERE table_schema=current_schema()
		  AND table_name='manager_team_member_assignments'
		  AND column_name IN ('display_name_override','cadence','cadence_custom_days')`,
	).Scan(&addedColumnCount); err != nil {
		t.Fatalf("inspect assignment columns after rollback: %v", err)
	}
	if addedColumnCount != 0 {
		t.Fatalf("migration 23 left %d assignment columns after rollback", addedColumnCount)
	}

	var fkCount int
	if err := db.QueryRow(`
		SELECT count(*) FROM pg_constraint
		WHERE conrelid='manager_team_member_assignments'::regclass
		  AND confrelid='manager_team_members'::regclass AND contype='f'`,
	).Scan(&fkCount); err != nil {
		t.Fatalf("inspect assignment FK after rollback: %v", err)
	}
	if fkCount != 1 {
		t.Fatalf("assignment FK count after rollback=%d, want 1", fkCount)
	}
}
