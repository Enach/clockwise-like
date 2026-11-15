package storage

// PAC-120 (contracts/features/PAC-120.md) at the storage layer: the single
// invitation's lock mode, its count rule (md §2, §3) and the three new seams
// (md §5, PAC-80 rules).

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func pac120User(t *testing.T, db *sql.DB, name string) uuid.UUID {
	t.Helper()
	return mustUser(t, db, "pac120-s-"+name+"-"+uuid.NewString()[:8]+"@example.com")
}

// pac120Link is a live link with the owner's accepted row and n co-hosts of
// rotating statuses (pending, accepted, declined).
func pac120Link(t *testing.T, db *sql.DB, owner uuid.UUID, n int) *SchedulingLink {
	t.Helper()
	l := pac40Link(t, db, owner, "pac120-s-"+uuid.NewString()[:8])
	statuses := []string{"pending", "accepted", "declined"}
	for i := 0; i < n; i++ {
		if _, err := AddLinkHost(db, l.ID, pac120User(t, db, "co"), statuses[i%3]); err != nil {
			t.Fatal(err)
		}
	}
	return l
}

func pac120HostCount(t *testing.T, db *sql.DB, id uuid.UUID) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM scheduling_link_hosts WHERE link_id = $1`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// md §2, §3 steps 3-6: every status counts, the owner never does, an
// existing row is returned before the count, and the limit is 5.
func TestPAC120_InviteCountRule(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	owner := pac120User(t, db, "owner")

	l := pac120Link(t, db, owner, 4)
	h, created, err := InviteLinkHostCtx(ctx, db, l.ID, pac120User(t, db, "fifth"))
	if err != nil || !created || h == nil || h.Status != "pending" {
		t.Fatalf("fifth: %+v %v %v", h, created, err)
	}
	if _, _, err := InviteLinkHostCtx(ctx, db, l.ID, pac120User(t, db, "sixth")); !errors.Is(err, ErrCoHostLimitReached) {
		t.Fatalf("sixth: %v, want ErrCoHostLimitReached", err)
	}
	if n := pac120HostCount(t, db, l.ID); n != 6 {
		t.Fatalf("%d host rows after a refusal, want owner + 5", n)
	}
	// An existing co-host is returned unchanged, before the count.
	if h2, created, err := InviteLinkHostCtx(ctx, db, l.ID, h.UserID); err != nil || created || h2.ID != h.ID {
		t.Fatalf("re-invite at 5: %+v %v %v", h2, created, err)
	}

	// The owner: never counted, with or without a row.
	l2 := pac40Link(t, db, owner, "pac120-s-"+uuid.NewString()[:8])
	if _, err := db.Exec(`DELETE FROM scheduling_link_hosts WHERE link_id = $1`, l2.ID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err := AddLinkHost(db, l2.ID, pac120User(t, db, "co"), "declined"); err != nil {
			t.Fatal(err)
		}
	}
	if oh, created, err := InviteLinkHostCtx(ctx, db, l2.ID, owner); err != nil || !created || oh.UserID != owner {
		t.Fatalf("owner without a row at 5: %+v %v %v", oh, created, err)
	}
	if oh, created, err := InviteLinkHostCtx(ctx, db, l2.ID, owner); err != nil || created || oh.UserID != owner {
		t.Fatalf("owner with a row at 5: %+v %v %v", oh, created, err)
	}
	if _, _, err := InviteLinkHostCtx(ctx, db, l2.ID, pac120User(t, db, "sixth")); !errors.Is(err, ErrCoHostLimitReached) {
		t.Fatalf("sixth after the owner's row: %v", err)
	}

	// A deleted link is ErrLinkGone, not the limit.
	if _, err := DeleteSchedulingLink(db, l2.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := InviteLinkHostCtx(ctx, db, l2.ID, pac120User(t, db, "late")); !errors.Is(err, ErrLinkGone) {
		t.Fatalf("deleted link: %v", err)
	}
}

// md §3 step 1, §4.1: while the invitation holds the link (seam), another
// transaction's FOR SHARE cannot be granted, a FOR KEY SHARE can: the mode
// is FOR NO KEY UPDATE, not FOR SHARE and not FOR UPDATE.
func TestPAC120_InviteLocksTheLinkForNoKeyUpdate(t *testing.T) {
	db := openTestDB(t)
	owner := pac120User(t, db, "lock-owner")
	l := pac120Link(t, db, owner, 1)
	ctx := context.WithValue(context.Background(), pac120CtxKey{}, "mine")
	var fired bool
	InstallAfterLinkInviteLock(t, func(c context.Context, id uuid.UUID) error {
		if id != l.ID || c.Value(pac120CtxKey{}) != "mine" {
			return nil
		}
		fired = true
		probe := func(mode string) error {
			tx, err := db.Begin()
			if err != nil {
				return err
			}
			defer func() { _ = tx.Rollback() }()
			_, err = tx.Exec(`SELECT 1 FROM scheduling_links WHERE id = $1 FOR `+mode+` NOWAIT`, l.ID)
			return err
		}
		// NOWAIT's refusal is SQLSTATE 55P03 (lock_not_available).
		if err := probe("SHARE"); !pac101IsLockTimeout(err) {
			t.Errorf("FOR SHARE beside the invitation: %v, want refused", err)
		}
		if err := probe("KEY SHARE"); err != nil {
			t.Errorf("FOR KEY SHARE beside the invitation: %v, want granted", err)
		}
		return nil
	})
	if _, created, err := InviteLinkHostCtx(ctx, db, l.ID, pac120User(t, db, "lock-x")); err != nil || !created {
		t.Fatalf("invite: %v %v", created, err)
	}
	if !fired {
		t.Fatal("afterLinkInviteLock did not fire with the request context")
	}
	// A non-nil seam error rolls the invitation back and is returned.
	boom := errors.New("pac120: seam")
	InstallAfterLinkInviteLock(t, func(context.Context, uuid.UUID) error { return boom })
	before := pac120HostCount(t, db, l.ID)
	if _, _, err := InviteLinkHostCtx(context.Background(), db, l.ID, pac120User(t, db, "lock-y")); !errors.Is(err, boom) {
		t.Fatalf("seam error: %v", err)
	}
	if n := pac120HostCount(t, db, l.ID); n != before {
		t.Fatal("a failed invitation wrote a row")
	}
}

type pac120CtxKey struct{}

// md §5: the lookup and link-read seams fail only the call they filter on,
// without querying, and are cleared by their uninstall and by test cleanup.
func TestPAC120_LookupAndLinkReadSeams(t *testing.T) {
	db := openTestDB(t)
	owner := pac120User(t, db, "seam-owner")
	l := pac120Link(t, db, owner, 0)
	other := pac120Link(t, db, owner, 0)
	addr := "pac120-s-seam-" + uuid.NewString()[:8] + "@example.com"
	boom := errors.New("pac120: injected")

	un := InstallConfirmedHolderLookup(t, func(email string) error {
		if email == addr {
			return boom
		}
		return nil
	})
	if _, _, err := ConfirmedHolder(db, addr); !errors.Is(err, boom) {
		t.Fatalf("lookup of the filtered address: %v", err)
	}
	if _, found, err := ConfirmedHolder(db, "nobody-"+addr); err != nil || found {
		t.Fatalf("lookup of another address: %v %v", found, err)
	}
	un()
	if _, _, err := ConfirmedHolder(db, addr); err != nil {
		t.Fatalf("lookup after uninstall: %v", err)
	}

	un = InstallLinkReadByID(t, func(id uuid.UUID) error {
		if id == l.ID {
			return boom
		}
		return nil
	})
	if got, err := GetSchedulingLinkByID(db, l.ID); !errors.Is(err, boom) || got != nil {
		t.Fatalf("read of the filtered link: %+v %v", got, err)
	}
	if got, err := GetSchedulingLinkByID(db, other.ID); err != nil || got == nil {
		t.Fatalf("read of another link: %+v %v", got, err)
	}
	un()
	if got, err := GetSchedulingLinkByID(db, l.ID); err != nil || got == nil {
		t.Fatalf("read after uninstall: %+v %v", got, err)
	}

	t.Run("cleared at test end", func(t *testing.T) {
		InstallAfterLinkInviteLock(t, func(context.Context, uuid.UUID) error { return nil })
		InstallConfirmedHolderLookup(t, func(string) error { return nil })
		InstallLinkReadByID(t, func(uuid.UUID) error { return nil })
	})
	if _, ok := afterLinkInviteLock.get(); ok {
		t.Error("afterLinkInviteLock outlived its test")
	}
	if _, ok := confirmedHolderLookup.get(); ok {
		t.Error("confirmedHolderLookup outlived its test")
	}
	if _, ok := linkReadByID.get(); ok {
		t.Error("linkReadByID outlived its test")
	}
}
