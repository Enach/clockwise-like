package storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/internal/lifecycle/lifecycletest"
	"github.com/Enach/paceday/backend/internal/secretbox"
	"github.com/google/uuid"
)

// PAC-142 (docs/factory/silent-failures.md SF-113): GetUserByID reports a
// failed organisation read instead of answering a user without an org, which
// a serialised User (org is omitempty) shows as "no organisation". An
// organisation that no longer exists is still no organisation, and
// GetUserByIDNoOrg (the readers that never use Org) does not read it at all.

// pac142UserInOrg makes a user in a fresh organisation.
func pac142UserInOrg(t *testing.T, db *sql.DB, tag string) (uuid.UUID, uuid.UUID) {
	t.Helper()
	u, err := UpsertUser(db, "pac142-"+tag+"@example.com", "P", "", "google", "sub-pac142-"+tag)
	if err != nil {
		t.Fatal(err)
	}
	org, err := UpsertOrg(db, "pac142-"+tag+"-"+uuid.NewString()[:8]+".example", "Org "+tag)
	if err != nil {
		t.Fatal(err)
	}
	if err := SetUserOrg(db, u.ID, org.ID); err != nil {
		t.Fatal(err)
	}
	return u.ID, org.ID
}

// failOrgRead makes GetOrgByID fail with err for org only (the test database
// is shared).
func failOrgRead(t *testing.T, org uuid.UUID, err error) {
	t.Helper()
	InstallOrgRead(t, func(id uuid.UUID) error {
		if id == org {
			return err
		}
		return nil
	})
}

func TestPAC142_GetUserByIDOrgReadErrorIsAnError(t *testing.T) {
	db := openTestDB(t)
	user, org := pac142UserInOrg(t, db, "orgerr")
	boom := errors.New("pac142: org read failed")
	failOrgRead(t, org, boom)

	u, err := GetUserByID(db, user)
	if !errors.Is(err, boom) {
		t.Fatalf("GetUserByID = %+v, %v; want the org read's error", u, err)
	}
	if errors.Is(err, sql.ErrNoRows) {
		t.Fatal("a failed org read must not read as a missing user")
	}
}

func TestPAC142_GetUserByIDLoadsTheOrg(t *testing.T) {
	db := openTestDB(t)
	user, org := pac142UserInOrg(t, db, "orgok")
	u, err := GetUserByID(db, user)
	if err != nil {
		t.Fatal(err)
	}
	if u.Org == nil || u.Org.ID != org || u.OrgID == nil || *u.OrgID != org {
		t.Fatalf("user = %+v, want org %s loaded", u, org)
	}
}

func TestPAC142_GetUserByIDDeletedOrgIsNoOrg(t *testing.T) {
	db := openTestDB(t)
	user, org := pac142UserInOrg(t, db, "orggone")
	failOrgRead(t, org, sql.ErrNoRows)

	u, err := GetUserByID(db, user)
	if err != nil {
		t.Fatalf("an org that no longer exists is no org, not an error: %v", err)
	}
	if u.Org != nil || u.OrgID == nil || *u.OrgID != org {
		t.Fatalf("user = %+v, want Org nil and OrgID kept", u)
	}
}

func TestPAC142_GetUserByIDNoOrgNeverReadsTheOrg(t *testing.T) {
	db := openTestDB(t)
	user, org := pac142UserInOrg(t, db, "noorg")
	failOrgRead(t, org, errors.New("pac142: org read failed"))

	u, err := GetUserByIDNoOrg(db, user)
	if err != nil {
		t.Fatalf("GetUserByIDNoOrg must not read the org: %v", err)
	}
	if u.ID != user || u.Org != nil || u.OrgID == nil || *u.OrgID != org || u.Email != "pac142-noorg@example.com" {
		t.Fatalf("user = %+v", u)
	}
	if _, err := GetUserByIDNoOrg(db, uuid.New()); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing user: %v, want sql.ErrNoRows", err)
	}
}

// The InstallUserRead seam covers both readers (PAC-141's tests rely on it
// through the callers moved to GetUserByIDNoOrg).
func TestPAC142_UserReadSeamCoversBothReaders(t *testing.T) {
	db := openTestDB(t)
	user, _ := pac142UserInOrg(t, db, "seam")
	boom := errors.New("pac142: user read failed")
	InstallUserRead(t, func(id uuid.UUID) error {
		if id == user {
			return boom
		}
		return nil
	})
	if _, err := GetUserByID(db, user); !errors.Is(err, boom) {
		t.Fatalf("GetUserByID: %v", err)
	}
	if _, err := GetUserByIDNoOrg(db, user); !errors.Is(err, boom) {
		t.Fatalf("GetUserByIDNoOrg: %v", err)
	}
}

// PAC-142 review: the keyring refresher reports a failed read of the active
// data key, sql.ErrNoRows included, instead of reading it as "no rotation
// due"; the tick carries on.
func TestPAC142_RefresherReportsActiveDataKeyReadFailure(t *testing.T) {
	db, _, _ := openEncDB(t)
	cfg := encKeys(t, "pac142")
	inst := Instance{ID: uuid.New()}
	load, err := LoadKeyring(context.Background(), db, cfg, inst)
	if err != nil {
		t.Fatal(err)
	}
	prev := secretbox.Current()
	secretbox.Install(load.Keyring)
	defer secretbox.Install(prev)
	// No row is active any more: ActiveDataKey answers sql.ErrNoRows.
	if _, err := db.Exec(`UPDATE encryption_data_keys SET state = 'retired', retired_at = now() WHERE state = 'active'`); err != nil {
		t.Fatal(err)
	}
	rec := lifecycletest.Record(t)
	r := &Refresher{DB: db, Cfg: cfg, Inst: inst, Lease: NewLease(time.Now()), Logf: func(string, ...any) {}}
	r.Tick(context.Background())
	got := rec.Matching("encryption", "read the active data key")
	if len(got) != 1 || !strings.Contains(got[0].Err.Error(), sql.ErrNoRows.Error()) {
		t.Fatalf("want one report of the active data key read, got %+v", rec.All())
	}
}
