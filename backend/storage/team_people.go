package storage

// PAC-129 (spec docs/specs/PAC-129.md; contract contracts/features/PAC-129.md
// §2-§5): people added to a team without an invitation or Paceday access, and
// every membership write ordered on the team row.
//
// Every write here is ONE storage-owned transaction (contract §4, the PAC-101
// C-2 rule behind withLinkWriteTx): with a session guard its first statement
// is the PAC-71 re-check; then the team row is locked FOR NO KEY UPDATE (the
// PAC-25 lock); then the afterTeamLock seam; then every read the decision
// rests on; then the writes.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// TeamMemberCap is the most members a team may hold, of both kinds, owner
// included (spec §3.10, D-11). Invitations do not count.
const TeamMemberCap = 50

// The refusals of the team writes. Each is one exact answer of the contract.
var (
	// ErrTeamGone: no team row under the lock (403, or 404 on acceptance).
	ErrTeamGone = errors.New("team not found")
	// ErrNotTeamOwner: the caller may not make this write (403).
	ErrNotTeamOwner = errors.New("forbidden")
	// ErrOwnAddress: addTeamPerson of the caller's own address (422).
	ErrOwnAddress = errors.New("you cannot add your own address")
	// ErrPacedayMemberAddress: addTeamPerson of a Paceday member's address (422).
	ErrPacedayMemberAddress = errors.New("this address belongs to a Paceday member of this team")
	// ErrTeamMemberLimit: an add or an invitation-that-adds at the cap (422).
	ErrTeamMemberLimit = errors.New(MsgTeamMemberLimit)
	// ErrTeamPersonNotFound: removeTeamPerson found nothing for the address (404).
	ErrTeamPersonNotFound = errors.New("person not found")
	// ErrInviteGone: the invitation no longer exists under the lock (404).
	ErrInviteGone = errors.New("invite not found")
	// ErrTeamFull: a legacy acceptance on a team at the cap (422).
	ErrTeamFull = errors.New(MsgTeamFull)
)

// The exact 422 texts of the cap (contract §3.1, §3.3, §3.4), as fixed
// strings a handler writes (PAC-21: never an error's text).
const (
	MsgTeamMemberLimit = "this team has reached its limit of 50 members; remove someone before adding another"
	MsgTeamFull        = "this team is full; ask its owner to remove someone before you accept"
)

// InviteNotPendingError is an acceptance of an invitation whose effective
// status, read under the lock, is no longer pending (410 "invite is <status>").
type InviteNotPendingError struct{ Status string }

func (e *InviteNotPendingError) Error() string { return "invite is " + e.Status }

// AddedMember is one person added to a team (a team_added_members row).
type AddedMember struct {
	TeamID      uuid.UUID
	Email       string
	DisplayName string
	AddedBy     *uuid.UUID
	CreatedAt   time.Time
	// PendingExpiry is set by AddTeamPersonCtx: the address's pending
	// invitation's expiry, read in the same transaction (not a column).
	PendingExpiry *time.Time
}

// TeamInviteOutcome is what InviteTeamPerson did (contract §3.3).
type TeamInviteOutcome string

const (
	InviteAddedAndInvited TeamInviteOutcome = "added_and_invited"
	InviteInvited         TeamInviteOutcome = "invited"
	InviteAlreadyPending  TeamInviteOutcome = "already_pending"
	InviteAlreadyMember   TeamInviteOutcome = "already_member"
)

// InviteResult is InviteTeamPerson's answer. Member is set for
// already_member, Added otherwise; Invitation is nil for already_member.
type InviteResult struct {
	Outcome    TeamInviteOutcome
	Invitation *TeamInvite
	Member     *TeamMember
	Added      *AddedMember
	// PendingExpiry is Added's pending invitation expiry after the call.
	PendingExpiry *time.Time
}

// withTeamWriteTx runs fn as one membership write on teamID (contract §4).
func withTeamWriteTx(ctx context.Context, db DBTX, op string, teamID uuid.UUID, fn func(q Querier) error) error {
	locked := func(q Querier) error {
		var one int
		err := q.QueryRowContext(ctx, `SELECT 1 FROM teams WHERE id = $1 FOR NO KEY UPDATE`, teamID).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrTeamGone
		}
		if err != nil {
			return err
		}
		if hook, ok := afterTeamLock.get(); ok {
			if err := hook(op, teamID); err != nil {
				return err
			}
		}
		return fn(q)
	}
	if g := SessionGuardFrom(ctx); g != nil {
		fireGuardedCallHook(ctx)
		return withGenerationTx(ctx, db, g.UserID, g.Gen, g, locked)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := locked(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// --- decision reads (inside the transaction) --------------------------------

// callerRole is the caller's role on the team; member is false when they hold
// no membership.
func callerRole(ctx context.Context, q Querier, teamID, userID uuid.UUID) (role string, member bool, err error) {
	err = q.QueryRowContext(ctx, `SELECT role FROM team_members WHERE team_id = $1 AND user_id = $2`, teamID, userID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return role, true, nil
}

// carrierRole is callerRole for a carrier of the address being removed: it
// fires the teamMemberRead seam as GetTeamMember does, and any error but "no
// row" is the caller's 500 (PAC-122 SF-001), never "not a member".
func carrierRole(ctx context.Context, q Querier, teamID, userID uuid.UUID) (string, bool, error) {
	if fn, ok := teamMemberRead.get(); ok {
		if err := fn(teamID, userID); err != nil {
			return "", false, err
		}
	}
	return callerRole(ctx, q, teamID, userID)
}

func accountAddress(ctx context.Context, q Querier, userID uuid.UUID) (string, error) {
	var email string
	err := q.QueryRowContext(ctx, `SELECT COALESCE(email, '') FROM users WHERE id = $1`, userID).Scan(&email)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return NormaliseStoredAddress(email), err
}

// pacedayMembersAt are the team's Paceday members whose account address is
// email (one address per account, compared lowercased).
func pacedayMembersAt(ctx context.Context, q Querier, teamID uuid.UUID, email string) ([]*TeamMember, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT tm.team_id, tm.user_id, tm.role, tm.joined_at, COALESCE(u.name,''), COALESCE(u.email,'')
		FROM team_members tm JOIN users u ON u.id = tm.user_id
		WHERE tm.team_id = $1 AND lower(u.email) = $2
		ORDER BY tm.joined_at, tm.user_id`, teamID, email)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*TeamMember, 0, 1)
	for rows.Next() {
		var m TeamMember
		if err := rows.Scan(&m.TeamID, &m.UserID, &m.Role, &m.JoinedAt, &m.Name, &m.Email); err != nil {
			return nil, err
		}
		out = append(out, &m)
	}
	return out, rows.Err()
}

func scanAdded(row interface{ Scan(...any) error }) (*AddedMember, error) {
	var a AddedMember
	var by uuid.NullUUID
	if err := row.Scan(&a.TeamID, &a.Email, &a.DisplayName, &by, &a.CreatedAt); err != nil {
		return nil, err
	}
	if by.Valid {
		id := by.UUID
		a.AddedBy = &id
	}
	return &a, nil
}

const addedColumns = `team_id, email, display_name, added_by, created_at`

// addedEntry is the team's added entry for email, or nil.
func addedEntry(ctx context.Context, q Querier, teamID uuid.UUID, email string) (*AddedMember, error) {
	a, err := scanAdded(q.QueryRowContext(ctx, `SELECT `+addedColumns+` FROM team_added_members WHERE team_id = $1 AND email = $2`, teamID, email))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return a, err
}

// memberCount is the cap's count: Paceday members plus added people.
func memberCount(ctx context.Context, q Querier, teamID uuid.UUID) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `
		SELECT (SELECT count(*) FROM team_members WHERE team_id = $1)
		     + (SELECT count(*) FROM team_added_members WHERE team_id = $1)`, teamID).Scan(&n)
	return n, err
}

// hasAddingOwner reports whether the entry's adder is currently an owner of
// the team (contract §3, "the adding owner").
func hasAddingOwner(ctx context.Context, q Querier, a *AddedMember) (bool, error) {
	if a.AddedBy == nil {
		return false, nil
	}
	role, member, err := callerRole(ctx, q, a.TeamID, *a.AddedBy)
	if err != nil {
		return false, err
	}
	return member && role == "owner", nil
}

// repointIfOrphan makes caller the adding owner when the entry has none
// (spec OQ-1, contract §3.1 step 6, §3.3 step 4).
func repointIfOrphan(ctx context.Context, q Querier, a *AddedMember, caller uuid.UUID) error {
	ok, err := hasAddingOwner(ctx, q, a)
	if err != nil || ok {
		return err
	}
	if _, err := q.ExecContext(ctx, `UPDATE team_added_members SET added_by = $3 WHERE team_id = $1 AND email = $2`, a.TeamID, a.Email, caller); err != nil {
		return err
	}
	c := caller
	a.AddedBy = &c
	return nil
}

func insertAdded(ctx context.Context, q Querier, teamID uuid.UUID, email, name string, caller uuid.UUID) (*AddedMember, error) {
	return scanAdded(q.QueryRowContext(ctx, `
		INSERT INTO team_added_members (team_id, email, display_name, added_by)
		VALUES ($1, $2, $3, $4) RETURNING `+addedColumns,
		teamID, NormaliseStoredAddress(email), TrimAddressSpace(name), caller))
}

// pendingInvitation is the address's pending invitation on the team (stored
// pending AND expiry after now(), spec D-12), or nil.
func pendingInvitation(ctx context.Context, q Querier, teamID uuid.UUID, email string) (*TeamInvite, error) {
	inv, err := scanInvite(q.QueryRowContext(ctx, `
		SELECT id, team_id, invitee_email, invited_by, token, status, created_at, expires_at
		FROM team_invites
		WHERE team_id = $1 AND invitee_email = $2 AND status = 'pending' AND expires_at > now()`, teamID, email))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return inv, err
}

// setPendingExpiry fills a.PendingExpiry from the address's pending invitation.
func setPendingExpiry(ctx context.Context, q Querier, a *AddedMember) error {
	inv, err := pendingInvitation(ctx, q, a.TeamID, a.Email)
	if err != nil || inv == nil {
		return err
	}
	t := inv.ExpiresAt
	a.PendingExpiry = &t
	return nil
}

// --- addTeamPerson (contract §3.1) -------------------------------------------

// AddTeamPersonCtx adds email (already normalised) to the team as an added
// person. created is false when the address already was one (the 200).
func AddTeamPersonCtx(ctx context.Context, db DBTX, teamID, caller uuid.UUID, email, name string) (person *AddedMember, created bool, err error) {
	email = NormaliseStoredAddress(email)
	err = withTeamWriteTx(ctx, db, "add", teamID, func(q Querier) error {
		role, member, err := callerRole(ctx, q, teamID, caller)
		if err != nil {
			return fmt.Errorf("owner check: %w", err) // 500 here (SF-074, OQ-12)
		}
		if !member || role != "owner" {
			return ErrNotTeamOwner
		}
		own, err := accountAddress(ctx, q, caller)
		if err != nil {
			return err
		}
		if own == email {
			return ErrOwnAddress
		}
		at, err := pacedayMembersAt(ctx, q, teamID, email)
		if err != nil {
			return err
		}
		if len(at) > 0 {
			return ErrPacedayMemberAddress
		}
		existing, err := addedEntry(ctx, q, teamID, email)
		if err != nil {
			return err
		}
		if existing != nil {
			if err := repointIfOrphan(ctx, q, existing, caller); err != nil {
				return err
			}
			person, created = existing, false
			return setPendingExpiry(ctx, q, person)
		}
		n, err := memberCount(ctx, q, teamID)
		if err != nil {
			return err
		}
		if n >= TeamMemberCap {
			return ErrTeamMemberLimit
		}
		if person, err = insertAdded(ctx, q, teamID, email, name, caller); err != nil {
			return err
		}
		created = true
		return setPendingExpiry(ctx, q, person) // a legacy invitation for the address
	})

	if err != nil {
		return nil, false, err
	}
	return person, created, nil
}

// --- removeTeamPerson (contract §3.2) ----------------------------------------

// RemoveTeamPersonCtx removes everything of the address (already normalised)
// from the team: Paceday memberships, the added entry and every invitation.
func RemoveTeamPersonCtx(ctx context.Context, db DBTX, teamID, caller uuid.UUID, email string) error {
	email = NormaliseStoredAddress(email)
	return withTeamWriteTx(ctx, db, "remove", teamID, func(q Querier) error {
		role, member, err := callerRole(ctx, q, teamID, caller)
		if err != nil {
			return fmt.Errorf("membership check: %w", err)
		}
		if !member {
			return ErrNotTeamOwner
		}
		own, err := accountAddress(ctx, q, caller)
		if err != nil {
			return err
		}
		if own != email && role != "owner" {
			return ErrNotTeamOwner
		}
		return removeAddressFromTeam(ctx, q, teamID, email, true)
	})
}

// removeAddressFromTeam deletes the address's memberships (refusing the last
// owner), its added entry and every invitation of the team for it. With
// notFoundIsError, finding none of them is ErrTeamPersonNotFound.
func removeAddressFromTeam(ctx context.Context, q Querier, teamID uuid.UUID, email string, notFoundIsError bool) error {
	at, err := pacedayMembersAt(ctx, q, teamID, email)
	if err != nil {
		return err
	}
	added, err := addedEntry(ctx, q, teamID, email)
	if err != nil {
		return err
	}
	var invites int
	if err := q.QueryRowContext(ctx, `SELECT count(*) FROM team_invites WHERE team_id = $1 AND invitee_email = $2`, teamID, email).Scan(&invites); err != nil {
		return err
	}
	if notFoundIsError && len(at) == 0 && added == nil && invites == 0 {
		return ErrTeamPersonNotFound
	}
	ids := make([]uuid.UUID, 0, len(at))
	removesOwner := false
	for _, m := range at {
		ids = append(ids, m.UserID)
		removesOwner = removesOwner || m.Role == "owner"
	}
	if removesOwner {
		var others int
		if err := q.QueryRowContext(ctx, `
			SELECT count(*) FROM team_members
			WHERE team_id = $1 AND role = 'owner' AND NOT (user_id::text = ANY($2::text[]))`, teamID, uuidArray(ids)).Scan(&others); err != nil {
			return err
		}
		if others == 0 {
			return ErrLastOwner
		}
	}
	if len(ids) > 0 {
		if _, err := q.ExecContext(ctx, `DELETE FROM team_members WHERE team_id = $1 AND user_id::text = ANY($2::text[])`, teamID, uuidArray(ids)); err != nil {
			return err
		}
	}
	if _, err := q.ExecContext(ctx, `DELETE FROM team_added_members WHERE team_id = $1 AND email = $2`, teamID, email); err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `DELETE FROM team_invites WHERE team_id = $1 AND invitee_email = $2`, teamID, email)
	return err
}

// --- inviteTeamMember (contract §3.3) ----------------------------------------

// InviteTeamPersonCtx is the invitation write: it adds an address not on the
// team, never duplicates a pending invitation, and answers a Paceday member's
// address as already_member. The email is sent by the caller after commit.
func InviteTeamPersonCtx(ctx context.Context, db DBTX, teamID, caller uuid.UUID, email, name string) (*InviteResult, error) {
	email = NormaliseStoredAddress(email)
	var res *InviteResult
	err := withTeamWriteTx(ctx, db, "invite", teamID, func(q Querier) error {
		role, member, err := callerRole(ctx, q, teamID, caller)
		// A storage error in the owner check is still the 403 on this existing
		// operation (SF-074 left open here, spec OQ-12).
		if err != nil || !member || role != "owner" {
			return ErrNotTeamOwner
		}
		at, err := pacedayMembersAt(ctx, q, teamID, email)
		if err != nil {
			return err
		}
		if len(at) > 0 {
			res = &InviteResult{Outcome: InviteAlreadyMember, Member: at[0]}
			return nil
		}
		added, err := addedEntry(ctx, q, teamID, email)
		if err != nil {
			return err
		}
		addedNow := false
		if added == nil {
			n, err := memberCount(ctx, q, teamID)
			if err != nil {
				return err
			}
			if n >= TeamMemberCap {
				return ErrTeamMemberLimit
			}
			if added, err = insertAdded(ctx, q, teamID, email, name, caller); err != nil {
				return err
			}
			addedNow = true
		} else if err := repointIfOrphan(ctx, q, added, caller); err != nil {
			return err
		}
		pending, err := pendingInvitation(ctx, q, teamID, email)
		if err != nil {
			return err
		}
		if pending != nil {
			exp := pending.ExpiresAt
			res = &InviteResult{Outcome: InviteAlreadyPending, Invitation: pending, Added: added, PendingExpiry: &exp}
			return nil
		}
		if _, err := q.ExecContext(ctx, `
			UPDATE team_invites SET status = 'expired'
			WHERE team_id = $1 AND invitee_email = $2 AND status = 'pending' AND expires_at <= now()`, teamID, email); err != nil {
			return err
		}
		inv, err := CreateTeamInvite(q, teamID, email, caller)
		if err != nil {
			return err
		}
		outcome := InviteInvited
		if addedNow {
			outcome = InviteAddedAndInvited
		}
		exp := inv.ExpiresAt
		res = &InviteResult{Outcome: outcome, Invitation: inv, Added: added, PendingExpiry: &exp}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// --- acceptTeamInvite (contract §3.4) ----------------------------------------

// AcceptTeamInviteForUserCtx is the acceptance write of invitation inviteID
// (of team teamID, read before the lock only to learn the team) by userID:
// re-read under the lock, then already-member, upgrade or capped legacy
// create, each with the invitation marked accepted, in one transaction.
func AcceptTeamInviteForUserCtx(ctx context.Context, db DBTX, inviteID, teamID, userID uuid.UUID) error {
	err := withTeamWriteTx(ctx, db, "accept", teamID, func(q Querier) error {
		inv, err := scanInvite(q.QueryRowContext(ctx, `
			SELECT id, team_id, invitee_email, invited_by, token,
			       CASE WHEN status = 'pending' AND expires_at <= now() THEN 'expired' ELSE status END,
			       created_at, expires_at
			FROM team_invites WHERE id = $1 AND team_id = $2`, inviteID, teamID))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInviteGone
		}
		if err != nil {
			return err
		}
		if inv.Status != "pending" {
			return &InviteNotPendingError{Status: inv.Status}
		}
		markAccepted := func() error { return AcceptTeamInvite(q, inviteID) }
		_, member, err := callerRole(ctx, q, teamID, userID)
		if err != nil {
			return err
		}
		if member {
			return markAccepted()
		}
		email := NormaliseStoredAddress(inv.InviteeEmail)
		added, err := addedEntry(ctx, q, teamID, email)
		if err != nil {
			return err
		}
		if added != nil {
			if _, err := q.ExecContext(ctx, `DELETE FROM team_added_members WHERE team_id = $1 AND email = $2`, teamID, email); err != nil {
				return err
			}
		} else {
			n, err := memberCount(ctx, q, teamID)
			if err != nil {
				return err
			}
			if n >= TeamMemberCap {
				return ErrTeamFull
			}
		}
		if err := AddTeamMember(q, teamID, userID, "member"); err != nil {
			return err
		}
		if hook, ok := afterAcceptMembershipStep.get(); ok {
			if err := hook(inviteID); err != nil {
				return err
			}
		}
		return markAccepted()
	})
	if errors.Is(err, ErrTeamGone) {
		return ErrInviteGone // the team was deleted: its invitations went with it
	}
	return err
}

// --- removeManagerTeamMember with team_id (contract §5.3) --------------------

// RemoveManagerTeamPersonCtx is the Manager-mode team-scoped removal under the
// team lock: the owner check, the carrier lookup and the membership reads run
// inside the transaction, then the memberships, this team's assignment, the
// added entry and every invitation of the team for the address are deleted.
// The manager's global roster row, its 1:1 history and cached figures stay.
// email is matched in its normal form (P3-m1: lower(TrimAddressSpace(path))).
func RemoveManagerTeamPersonCtx(ctx context.Context, db DBTX, managerID, teamID uuid.UUID, email string) error {
	email = NormaliseStoredAddress(email)
	err := withTeamWriteTx(ctx, db, "manager-remove", teamID, func(q Querier) error {
		role, member, err := callerRole(ctx, q, teamID, managerID)
		// The 403 on a storage error in the owner check stays (spec OQ-12).
		if err != nil || !member || role != "owner" {
			return ErrNotTeamOwner
		}
		carriers, err := CarrierIDs(q, []string{email})
		if err != nil {
			return err
		}
		ids := make([]uuid.UUID, 0, len(carriers))
		for _, id := range carriers {
			r, isMember, err := carrierRole(ctx, q, teamID, id)
			if err != nil {
				return err
			}
			if !isMember {
				continue
			}
			// The Manager path's own, role-based rule: never any owner (PAC-25 D-3).
			if r == "owner" {
				return ErrLastOwner
			}
			ids = append(ids, id)
		}
		if len(ids) > 0 {
			if _, err := q.ExecContext(ctx, `DELETE FROM team_members WHERE team_id = $1 AND user_id::text = ANY($2::text[])`, teamID, uuidArray(ids)); err != nil {
				return err
			}
		}
		if err := UnassignManagerTeamMember(q, managerID, teamID, email); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `DELETE FROM team_added_members WHERE team_id = $1 AND email = $2`, teamID, email); err != nil {
			return err
		}
		_, err = q.ExecContext(ctx, `DELETE FROM team_invites WHERE team_id = $1 AND invitee_email = $2`, teamID, email)
		return err
	})
	if errors.Is(err, ErrTeamGone) {
		return ErrNotTeamOwner
	}
	return err
}

// --- reads -------------------------------------------------------------------

// ListAddedMembers lists the team's added people by the time they were added.
func ListAddedMembers(db DBTX, teamID uuid.UUID) ([]*AddedMember, error) {
	if fn, ok := teamMembersListRead.get(); ok {
		if err := fn(teamID); err != nil {
			return nil, err
		}
	}
	rows, err := db.Query(`SELECT `+addedColumns+` FROM team_added_members WHERE team_id = $1 ORDER BY created_at, email`, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*AddedMember, 0)
	for rows.Next() {
		a, err := scanAdded(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetAddedMember is the team's added entry for email (normalised here), or
// sql.ErrNoRows.
func GetAddedMember(db DBTX, teamID uuid.UUID, email string) (*AddedMember, error) {
	a, err := addedEntry(context.Background(), db, teamID, NormaliseStoredAddress(email))
	if err == nil && a == nil {
		return nil, sql.ErrNoRows
	}
	return a, err
}

// PendingInvitationExpiries maps each address with a pending invitation on the
// team to that invitation's expiry.
func PendingInvitationExpiries(db DBTX, teamID uuid.UUID) (map[string]time.Time, error) {
	rows, err := db.Query(`
		SELECT invitee_email, expires_at FROM team_invites
		WHERE team_id = $1 AND status = 'pending' AND expires_at > now()`, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]time.Time{}
	for rows.Next() {
		var e string
		var t time.Time
		if err := rows.Scan(&e, &t); err != nil {
			return nil, err
		}
		out[e] = t
	}
	return out, rows.Err()
}

// AddingOwner is the account whose credential reads the added person: added_by
// while that account is an owner of the team; ok is false otherwise (the
// person's availability is then unknown, spec OQ-1).
func AddingOwner(db DBTX, a *AddedMember) (owner uuid.UUID, ok bool, err error) {
	ok, err = hasAddingOwner(context.Background(), db, a)
	if err != nil || !ok {
		return uuid.Nil, false, err
	}
	return *a.AddedBy, true, nil
}

func uuidArray(ids []uuid.UUID) interface{} {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id.String()
	}
	return pq.Array(out)
}

// CalendarProviderOf is the account's stored calendar provider ("" when it
// has no settings row). It reads only: a member's Find a time must not create
// the adding owner's settings row (PAC-129 §6).
func CalendarProviderOf(ctx context.Context, db DBTX, userID uuid.UUID) (string, error) {
	var p sql.NullString
	err := db.QueryRowContext(ctx, `SELECT calendar_provider FROM settings WHERE user_id = $1`, userID).Scan(&p)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return p.String, err
}
