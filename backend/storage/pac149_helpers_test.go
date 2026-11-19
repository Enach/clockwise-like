package storage

import (
	"context"

	"github.com/google/uuid"
)

// pac149AddPending is the PAC-149 replacement of the fixture
// AddLinkHost(db, linkID, userID, "pending") (contract md §6, §9): a pending
// row can no longer point at an account, so it records a pending invitation
// to the account's stored address and makes the account that address's
// domain-confirmed holder, so the account can still list, accept, decline and
// leave it as the pre-PAC-149 fixture allowed.
func pac149AddPending(db Querier, linkID, userID uuid.UUID) (*LinkHost, error) {
	var email string
	if err := db.QueryRowContext(context.Background(),
		`UPDATE users SET address_standing = 'domain_confirmed' WHERE id = $1 RETURNING email`, userID).Scan(&email); err != nil {
		return nil, err
	}
	return AddLinkInvitation(db, linkID, email)
}
