package api

import (
	"context"
	"strings"
	"testing"

	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

// pac149AddPending is the PAC-149 replacement of the fixture
// storage.AddLinkHost(db, linkID, userID, "pending") (contract md §6, §9): a
// pending row can no longer point at an account, so it records a pending
// invitation to the account's stored address and makes the account that
// address's domain-confirmed holder, so the account can still list, accept,
// decline and leave it as the pre-PAC-149 fixture allowed.
func pac149AddPending(db storage.Querier, linkID, userID uuid.UUID) (*storage.LinkHost, error) {
	var email string
	if err := db.QueryRowContext(context.Background(),
		`UPDATE users SET address_standing = 'domain_confirmed' WHERE id = $1 RETURNING email`, userID).Scan(&email); err != nil {
		return nil, err
	}
	return storage.AddLinkInvitation(db, linkID, email)
}

// pac149Key is the test-side key of a pending invitation to addr: the id of
// the one account whose stored address equals addr ignoring letter case, or,
// when none or several carry it, pac149AddrKey(addr). It is a fixture view
// only: the server never makes this match on the inviter's side.
func pac149Key(t testing.TB, db storage.Querier, addr string) uuid.UUID {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), `SELECT id FROM users WHERE lower(email) = lower($1)`, addr)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(ids) == 1 {
		return ids[0]
	}
	return pac149AddrKey(addr)
}

// pac149AddrKey is the fixture key of an invitation to an address no single
// account carries.
func pac149AddrKey(addr string) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("pac149:"+strings.ToLower(addr)))
}

// pac149InvitationsTo counts pending invitations to addr (ignoring letter
// case) on any link.
func pac149InvitationsTo(t testing.TB, db storage.Querier, addr string) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM scheduling_link_hosts WHERE user_id IS NULL AND lower(invitee_email) = lower($1)`, addr).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// pac149AddHost is storage.AddLinkHost for fixtures that seed a status from a
// variable: "pending" becomes an invitation to the account's address
// (pac149AddPending), any other status the account's entry.
func pac149AddHost(db storage.Querier, linkID, userID uuid.UUID, status string) (*storage.LinkHost, error) {
	if status == "pending" {
		return pac149AddPending(db, linkID, userID)
	}
	return storage.AddLinkHost(db, linkID, userID, status)
}

// pac149FixtureUserSQL is the fixture view of a host row's account: the row's
// user_id for an entry, and for a pending invitation the one account whose
// stored address equals the invitation's ignoring case (NULL when none or
// several). Test-side only: the server never makes this match on the
// inviter's side.
const pac149FixtureUserSQL = `COALESCE(user_id, (SELECT u.id FROM users u WHERE lower(u.email) = lower(invitee_email)
	AND NOT EXISTS (SELECT 1 FROM users v WHERE lower(v.email) = lower(invitee_email) AND v.id <> u.id)))`

// mustEmail is the account's stored address.
func mustEmail(t testing.TB, db storage.Querier, id uuid.UUID) string {
	t.Helper()
	var email string
	if err := db.QueryRowContext(context.Background(), `SELECT email FROM users WHERE id = $1`, id).Scan(&email); err != nil {
		t.Fatal(err)
	}
	return email
}
