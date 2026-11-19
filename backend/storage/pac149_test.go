package storage

// PAC-149 at the storage layer (docs/specs/PAC-149.md AC-9; contract
// contracts/features/PAC-149.md §2, §4.0): no inviter action makes a row
// point at an account, and a stranger's pending invitation no longer makes an
// unbound legacy account non-empty under PAC-71 rule (b).

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func pac149Count(t *testing.T, q Querier, query string, args ...any) int {
	t.Helper()
	var n int
	if err := q.QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

// AC-9: O invites H (a domain-confirmed holder) and N (no account) by both
// paths: no stored row points at H's account until H answers, and every
// pending row is an invitation (the CHECK holds). An unbound legacy account
// whose only data is another owner's pending invitation to its address is
// empty (rule (b) lets it bind); with an accepted or declined entry instead
// it is refused with the existing kind.
func TestPAC149_AC9_NoAccountLinkAndBinding(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	owner := mustUser(t, db, "pac149-ac9-owner@example.com")
	hAddr, nAddr := "pac149-ac9-hold@example.com", "pac149-ac9-none@example.com"
	h := mustUser(t, db, hAddr)
	if _, err := db.Exec(`UPDATE users SET address_standing = 'domain_confirmed' WHERE id = $1`, h); err != nil {
		t.Fatal(err)
	}
	single := pac40Link(t, db, owner, "pac149-ac9-single")
	for _, addr := range []string{hAddr, nAddr} {
		if e, created, err := InviteLinkAddressCtx(ctx, db, single.ID, addr); err != nil || !created || e.UserID != uuid.Nil {
			t.Fatalf("single invite %s: %+v %v %v", addr, e, created, err)
		}
	}
	listed, changes, err := CreateSchedulingLinkWithCoHostsCtx(ctx, db, &SchedulingLink{OwnerUserID: owner, Slug: "pac149-ac9-list",
		Title: "AC-9", DurationOptions: []int{30}, DaysOfWeek: []int{1}, WindowStart: "09:00", WindowEnd: "17:00", Active: true},
		[]string{hAddr, nAddr})
	if err != nil || changes.InvitedCount != 2 {
		t.Fatalf("list: %+v %v", changes, err)
	}
	if n := pac149Count(t, db, `SELECT count(*) FROM scheduling_link_hosts WHERE user_id = $1`, h); n != 0 {
		t.Fatalf("%d rows point at H's account before H answered", n)
	}
	if n := pac149Count(t, db, `SELECT count(*) FROM scheduling_link_hosts WHERE status = 'pending' AND (user_id IS NOT NULL OR invitee_email IS NULL)`); n != 0 {
		t.Fatalf("%d pending rows are not invitations", n)
	}
	if _, err := db.Exec(`INSERT INTO scheduling_link_hosts (link_id, user_id, status) VALUES ($1, $2, 'pending')`, single.ID, h); err == nil {
		t.Error("the storage accepted a pending row that points at an account")
	}
	if ok, err := RespondToHostInvite(db, listed.ID, h, "accepted"); err != nil || !ok {
		t.Fatalf("H accepts: %v %v", ok, err)
	}
	if n := pac149Count(t, db, `SELECT count(*) FROM scheduling_link_hosts WHERE user_id = $1`, h); n != 1 {
		t.Fatalf("after H answered: %d rows of H, want 1", n)
	}

	// PAC-71 rule (b): the emptiness of an unbound legacy account.
	for _, c := range []struct {
		name, status, want string
	}{
		{"pending invitation only", "pending", ""},
		{"accepted entry", "accepted", "scheduling_link_host"},
		{"declined entry", "declined", "scheduling_link_host"},
	} {
		t.Run(c.name, func(t *testing.T) {
			addr := "pac149-ac9-legacy-" + uuid.NewString()[:8] + "@example.com"
			var legacy uuid.UUID
			if err := db.QueryRow(`INSERT INTO users (email, name) VALUES ($1, 'Legacy') RETURNING id`, addr).Scan(&legacy); err != nil {
				t.Fatal(err)
			}
			other := mustUser(t, db, "pac149-ac9-other-"+uuid.NewString()[:8]+"@example.com")
			l := pac40Link(t, db, other, "pac149-ac9-"+uuid.NewString()[:8])
			if c.status == "pending" {
				if _, _, err := InviteLinkAddressCtx(ctx, db, l.ID, addr); err != nil {
					t.Fatal(err)
				}
			} else if _, err := AddLinkHost(db, l.ID, legacy, c.status); err != nil {
				t.Fatal(err)
			}
			if kind, err := FirstNonEmptyKind(db, legacy); err != nil || kind != c.want {
				t.Errorf("FirstNonEmptyKind = %q %v, want %q", kind, err, c.want)
			}
		})
	}
}

// Contract md §2.1: the single invite decides, under the lock, the owner's
// address, then an existing entry, then well-formedness, then the limit
// (D-18): a legacy entry whose stored address is malformed is "unchanged",
// never the malformed refusal; a malformed new address on a full link is the
// malformed refusal, not the limit.
func TestPAC149_SingleInvitePrecedence(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	owner := mustUser(t, db, "pac149-prec-owner@example.com")
	l := pac40Link(t, db, owner, "pac149-prec")
	legacy := mustUser(t, db, "pac149-prec-legacy@example.com")
	if _, err := db.Exec(`UPDATE users SET email = 'Legacy Name <legacy-prec@example.com>' WHERE id = $1`, legacy); err != nil {
		t.Fatal(err)
	}
	if _, err := AddLinkHost(db, l.ID, legacy, "accepted"); err != nil {
		t.Fatal(err)
	}
	if e, created, err := InviteLinkAddressCtx(ctx, db, l.ID, "LEGACY NAME <legacy-prec@example.com>"); err != nil || created || e.UserID != legacy || e.Status != "accepted" {
		t.Fatalf("legacy malformed entry: %+v %v %v", e, created, err)
	}
	for i := 0; i < 4; i++ {
		if _, err := AddLinkInvitation(db, l.ID, "pac149-prec-"+string(rune('a'+i))+"@example.com"); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := InviteLinkAddressCtx(ctx, db, l.ID, "not-an-address"); !errors.Is(err, ErrCoHostAddressMalformed) {
		t.Fatalf("malformed at the limit: %v, want ErrCoHostAddressMalformed", err)
	}
	if _, _, err := InviteLinkAddressCtx(ctx, db, l.ID, "pac149-prec-new@example.com"); !errors.Is(err, ErrCoHostLimitReached) {
		t.Fatalf("new at the limit: %v", err)
	}
	if e, created, err := InviteLinkAddressCtx(ctx, db, l.ID, strings.ToUpper("pac149-prec-a@example.com")); err != nil || created || e.Email != "pac149-prec-a@example.com" {
		t.Fatalf("re-invite at the limit: %+v %v %v", e, created, err)
	}
}

// Contract md §2.3: well-formedness is the team invitation's rule on the
// lowercased trimmed address.
func TestPAC149_WellFormedCoHostAddress(t *testing.T) {
	for addr, want := range map[string]bool{
		"pat@example.com":           true,
		"Pat@Example.COM":           true,
		"pat+tag@sub.example.co.uk": true,
		"user@localhost":            true,
		"a@b":                       true,
		"pat@[127.0.0.1]":           true,
		"pat@-x.example":            true,
		"pat@x_y.example":           true,
		"":                          false,
		"not-an-address":            false,
		"Pat <pat@example.com>":     false,
		"<pat@example.com>":         false,
		"pat@example.com (comment)": false,
		"pat@example.com, x@y.test": false,
		"a@b c":                     false,
		"pat@":                      false,
		"@example.com":              false,
		"pat.example.com":           false,
	} {
		if got := WellFormedCoHostAddress(addr); got != want {
			t.Errorf("WellFormedCoHostAddress(%q) = %v, want %v", addr, got, want)
		}
	}
}
