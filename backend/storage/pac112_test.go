package storage

// PAC-112 (contracts/features/PAC-112.md) at the storage layer: the shared
// ECMA-262 \s test, the database lowercasing used to merge entries, the seams
// (PAC-80 rules) and the create transaction's report.

import (
	"context"
	"database/sql"
	"testing"
	"unicode"

	"github.com/google/uuid"
)

// The set moved from api/handlers_credentials.go (PAC-67) unchanged: every
// rune in ECMA-262's WhiteSpace and LineTerminator, nothing else. U+0085 is
// the case Go's unicode.IsSpace gets wrong one way, U+FEFF the other way.
func TestPAC112_ECMASpaceIsTheContractSet(t *testing.T) {
	ecma := map[rune]bool{'\t': true, '\n': true, '\v': true, '\f': true, '\r': true, ' ': true,
		'\u00a0': true, '\u1680': true, '\u2028': true, '\u2029': true, '\u202f': true, '\u205f': true,
		'\u3000': true, '\ufeff': true}
	for r := rune(0x2000); r <= 0x200a; r++ { // the remaining Zs
		ecma[r] = true
	}
	for r := rune(0); r <= 0x10000; r++ {
		if got := ECMASpace(r); got != ecma[r] {
			t.Errorf("ECMASpace(%U) = %v, want %v", r, got, ecma[r])
		}
	}
	if unicode.IsSpace('\u0085') == ECMASpace('\u0085') || unicode.IsSpace('\ufeff') == ECMASpace('\ufeff') {
		t.Error("the probe runes no longer tell ECMA \\s from unicode.IsSpace")
	}
}

func TestPAC112_BlankAndTrim(t *testing.T) {
	for _, c := range []struct {
		in, trimmed string
		blank       bool
	}{
		{"", "", true},
		{"   ", "", true},
		{"\ufeff", "", true},
		{"\u3000\t\u00a0", "", true},
		{"\u0085", "\u0085", false},
		{" a@b.test\ufeff", "a@b.test", false},
		{"\u2028A@B.test \u00a0", "A@B.test", false},
	} {
		if got := BlankECMA(c.in); got != c.blank {
			t.Errorf("BlankECMA(%q) = %v", c.in, got)
		}
		if got := TrimECMASpace(c.in); got != c.trimmed {
			t.Errorf("TrimECMASpace(%q) = %q, want %q", c.in, got, c.trimmed)
		}
	}
}

// Contract md §3.3 rule 1: entries are merged by the database's lower(), the
// comparison the carriers lookup applies, in request order.
func TestPAC112_LowerAllIsTheDatabaseLower(t *testing.T) {
	db := openTestDB(t)
	in := []string{"B@Acme.test", "ÉLODIE@acme.test", "b@acme.test", "ǅ@x.test"}
	got, err := lowerAll(db, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(in) {
		t.Fatalf("lowerAll = %v", got)
	}
	for i, v := range in {
		var want string
		if err := db.QueryRow(`SELECT lower($1)`, v).Scan(&want); err != nil {
			t.Fatal(err)
		}
		if got[i] != want {
			t.Errorf("lowerAll[%d] = %q, the database's lower() = %q", i, got[i], want)
		}
	}
	if got[0] != got[2] {
		t.Errorf("two spellings of one address are not merged: %q %q", got[0], got[2])
	}
	if empty, err := lowerAll(db, nil); err != nil || len(empty) != 0 {
		t.Errorf("lowerAll(nil) = %v %v", empty, err)
	}
}

// PAC-80 rules for the PAC-112 seams: installed through Install…(tb, fn) and
// cleared when the test ends.
func TestPAC112_SeamsNeverOutliveTheirTest(t *testing.T) {
	t.Run("installs and leaves them", func(t *testing.T) {
		// PAC-149: the co-host lookup seam is gone with the lookup; the
		// carriers-read, invitee-match and statement seams replace it.
		InstallCarriersRead(t, func(string) {})
		InstallInviteeMatch(t, func(uuid.UUID) error { return nil })
		InstallCoHostStatement(t, func(string, []any) {})
		InstallCoHostWrite(t, func(uuid.UUID, string) error { return nil })
		InstallCreateOwnerHost(t, func(uuid.UUID) error { return nil })
	})
	if _, ok := carriersRead.get(); ok {
		t.Error("carriers-read seam outlived its test")
	}
	if _, ok := inviteeMatch.get(); ok {
		t.Error("invitee-match seam outlived its test")
	}
	if _, ok := coHostStatement.get(); ok {
		t.Error("statement seam outlived its test")
	}
	if _, ok := coHostWrite.get(); ok {
		t.Error("co-host write seam outlived its test")
	}
	if _, ok := createOwnerHost.get(); ok {
		t.Error("owner-row seam outlived its test")
	}
}

func pac112StorageUser(t *testing.T, db *sql.DB, email string, confirmed bool) uuid.UUID {
	t.Helper()
	id := mustUser(t, db, email)
	if confirmed {
		if _, err := db.Exec(`UPDATE users SET address_standing = 'domain_confirmed' WHERE id = $1`, id); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

// Contract md §3.1: without a list the create reports nothing; with [] it
// reports empty arrays; the owner's row is a plain accepted insert either way.
// Contract md §2.1: the report is per entry, counts per person.
func TestPAC112_CreateTransactionReports(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	owner := pac112StorageUser(t, db, "pac112-s-owner-"+uuid.NewString()[:8]+"@acme.test", true)
	newLink := func() *SchedulingLink {
		return &SchedulingLink{OwnerUserID: owner, Slug: "pac112-s-" + uuid.NewString()[:8], Title: "PAC-112",
			DurationOptions: []int{30}, DaysOfWeek: []int{1}, WindowStart: "09:00", WindowEnd: "17:00", UsageType: "reusable"}
	}
	l, changes, err := CreateSchedulingLinkWithCoHostsCtx(ctx, db, newLink(), nil)
	if err != nil || changes != nil {
		t.Fatalf("no list: %v %+v", err, changes)
	}
	if s, ok, err := GetLinkHostStatus(db, l.ID, owner); err != nil || !ok || s != "accepted" {
		t.Fatalf("owner row: %q %v %v", s, ok, err)
	}
	_, changes, err = CreateSchedulingLinkWithCoHostsCtx(ctx, db, newLink(), []string{})
	if err != nil || changes == nil || len(changes.Addresses) != 0 || changes.Removed == nil || len(changes.Removed) != 0 {
		t.Fatalf("[]: %v %+v", err, changes)
	}

	bAddr := "pac112-s-b-" + uuid.NewString()[:8] + "@acme.test"
	b := pac112StorageUser(t, db, bAddr, true)
	l, changes, err = CreateSchedulingLinkWithCoHostsCtx(ctx, db, newLink(), []string{bAddr, " " + bAddr, "nobody-" + bAddr})
	if err != nil {
		t.Fatal(err)
	}
	// PAC-149: an address no account carries is invited like any other.
	want := []CoHostOutcome{{bAddr, CoHostInvited}, {bAddr, CoHostInvited}, {"nobody-" + bAddr, CoHostInvited}}
	if len(changes.Addresses) != len(want) {
		t.Fatalf("addresses = %+v", changes.Addresses)
	}
	for i := range want {
		if changes.Addresses[i] != want[i] {
			t.Errorf("addresses[%d] = %+v, want %+v", i, changes.Addresses[i], want[i])
		}
	}
	if changes.InvitedCount != 2 || changes.NotAddedCount != 0 || changes.RemovedCount != 0 {
		t.Errorf("counts = %+v", changes)
	}
	if _, ok, err := GetLinkHostStatus(db, l.ID, b); err != nil || ok {
		t.Errorf("B has an account-keyed row: %v %v", ok, err)
	}
	var invited int
	if err := db.QueryRow(`SELECT count(*) FROM scheduling_link_hosts WHERE link_id = $1 AND user_id IS NULL AND status = 'pending'`, l.ID).Scan(&invited); err != nil || invited != 2 {
		t.Errorf("invitations = %d %v, want 2", invited, err)
	}

	// Over the limit on create: H is the owner alone, so six new entries are
	// refused and nothing is written.
	var before int
	_ = db.QueryRow(`SELECT COUNT(*) FROM scheduling_links WHERE owner_user_id = $1`, owner).Scan(&before)
	six := []string{"1-" + bAddr, "2-" + bAddr, "3-" + bAddr, "4-" + bAddr, "5-" + bAddr, "6-" + bAddr}
	if _, _, err := CreateSchedulingLinkWithCoHostsCtx(ctx, db, newLink(), six); err != ErrCoHostListTooLong {
		t.Fatalf("six new entries: %v, want ErrCoHostListTooLong", err)
	}
	var after int
	_ = db.QueryRow(`SELECT COUNT(*) FROM scheduling_links WHERE owner_user_id = $1`, owner).Scan(&after)
	if after != before {
		t.Errorf("links %d -> %d after a refused create", before, after)
	}
}
