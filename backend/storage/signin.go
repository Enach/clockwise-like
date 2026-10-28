package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/Enach/paceday/backend/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// PAC-71 sign-in resolution: the one implementation of spec
// docs/specs/PAC-71.md §3 "The sign-in rule" and migration rules (a) and (b),
// shared by the Google, Microsoft and OIDC callbacks (contract SIGN-IN step 4).
// The account is chosen only by identity; an address never links a sign-in to
// an account that has a bound identity.

// SignInHow says how a sign-in reached its account.
type SignInHow string

const (
	SignInReached SignInHow = "reached" // the identity was already bound
	SignInCreated SignInHow = "created" // no account carried the address
	SignInRuleA   SignInHow = "rule_a"  // an unbound legacy account bound under rule (a)
	SignInRuleB   SignInHow = "rule_b"  // an unbound legacy account bound under rule (b)
)

// Bound reports whether this sign-in wrote a migration binding.
func (h SignInHow) Bound() bool { return h == SignInRuleA || h == SignInRuleB }

// RuleA is a provider's migration rule (a): the recorded last-sign-in
// identifier (users.provider_id) must have the provider's format and equal the
// incoming subject, with at least MinEvidence (spec §3 rule (a), contract §3
// item 6). SSO has no rule (a).
type RuleA struct {
	Matches     func(recorded, subject string) bool
	MinEvidence Evidence
}

// SignInRequest is one completed provider sign-in.
type SignInRequest struct {
	Identity      Identity
	Email         string // the reported address
	Name          string
	AvatarURL     string
	Evidence      Evidence
	FirstProvider string // users.provider when an account is created: google | microsoft | sso
	RuleA         *RuleA // nil: the provider never binds a pre-existing account (SSO, D-10)
	// StoreCredential stores the provider credential on the chosen account,
	// inside the sign-in's transaction (nil: nothing to store, SSO).
	StoreCredential func(q Querier, userID uuid.UUID, how SignInHow) error
}

// SignInResult is the account a sign-in reached and the generation its session
// must carry.
type SignInResult struct {
	User *User
	Gen  int64
	How  SignInHow
}

// RefusalError is a refused sign-in (spec §3 "Refusal"). Nothing was written.
// Cause, Account and Kind go to the server log only.
type RefusalError struct {
	Cause   string
	Account uuid.UUID // the existing account concerned, or uuid.Nil
	Kind    string    // rule (b)'s first non-empty kind, when that decided it
}

func (e *RefusalError) Error() string {
	msg := "sign-in refused: " + e.Cause
	if e.Account != uuid.Nil {
		msg += " (account " + e.Account.String() + ")"
	}
	if e.Kind != "" {
		msg += " (first non-empty kind: " + e.Kind + ")"
	}
	return msg
}

// SignInHooks are deterministic interleaving seams for tests (spec §4
// conventions). BeforeStore runs after the first checks and before the
// transaction that creates or binds the account; production leaves it nil.
var SignInHooks struct {
	BeforeStore func(req SignInRequest)
}

func refuse(cause string, account uuid.UUID, kind string) (*SignInResult, error) {
	return nil, &RefusalError{Cause: cause, Account: account, Kind: kind}
}

// ResolveSignIn applies the sign-in rule to req. A *RefusalError is a
// refusal; any other error is a failure to create or bind (nothing written).
func ResolveSignIn(ctx context.Context, db *sql.DB, req SignInRequest) (*SignInResult, error) {
	req.Email = strings.TrimSpace(req.Email)
	if req.Identity.Subject == "" || req.Identity.Provider == "" {
		return refuse("the provider answer carries no subject", uuid.Nil, "")
	}

	// Rules 1 and 2: a bound identity reaches its account, whatever address.
	uid, found, err := AccountForIdentity(db, req.Identity)
	if err != nil {
		return nil, err
	}
	if found {
		return reachBound(ctx, db, uid, req)
	}
	if req.Email == "" {
		return refuse("the provider answer carries no address", uuid.Nil, "")
	}

	carriers, err := CarriersOf(db, req.Email)
	if err != nil {
		return nil, err
	}
	switch {
	case len(carriers) >= 2:
		return refuse(fmt.Sprintf("case collision: %d accounts carry the address", len(carriers)), carriers[0].ID, "")
	case len(carriers) == 1:
		c := carriers[0]
		if c.Bound {
			// Rule 3: the address never links to a bound account.
			return refuse("the address belongs to an account bound to another identity", c.ID, "")
		}
		if req.RuleA == nil {
			// D-10: SSO never reaches or binds an existing account.
			return refuse("an SSO identity never binds an existing account", c.ID, "")
		}
		how, kind, err := migrationRule(db, c, req)
		if err != nil {
			return nil, err
		}
		if how == "" {
			return refuse("unbound pre-existing account: neither rule (a) nor rule (b) applies", c.ID, kind)
		}
		if SignInHooks.BeforeStore != nil {
			SignInHooks.BeforeStore(req)
		}
		return bindLegacy(ctx, db, c, how, req)
	}

	// Rule 5: no account carries the address.
	if req.Evidence == EvidenceNone {
		return refuse("a new identity without evidence for its address", uuid.Nil, "")
	}
	if SignInHooks.BeforeStore != nil {
		SignInHooks.BeforeStore(req)
	}
	return createAccount(ctx, db, req)
}

// migrationRule decides rule (a) or (b) for an unbound legacy carrier. kind is
// rule (b)'s first non-empty kind when the account is not empty.
func migrationRule(q Querier, c Carrier, req SignInRequest) (SignInHow, string, error) {
	if req.RuleA.Matches(c.ProviderID, req.Identity.Subject) && req.Evidence >= req.RuleA.MinEvidence && req.Evidence > EvidenceNone {
		return SignInRuleA, "", nil
	}
	if req.Evidence != EvidenceAuthoritative {
		return "", "", nil
	}
	kind, err := FirstNonEmptyKind(q, c.ID)
	if err != nil {
		return "", "", err
	}
	if kind != "" {
		return "", kind, nil
	}
	return SignInRuleB, "", nil
}

// reachBound is rule 1: the identity's account, in one transaction that
// re-confirms the binding under the row lock (so an operator step cannot slip
// between the lookup and the session), stores the credential and reads the
// generation the session carries. The stored address and standing are never
// changed (spec D-7).
func reachBound(ctx context.Context, db *sql.DB, uid uuid.UUID, req SignInRequest) (*SignInResult, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var gen int64
	err = tx.QueryRow(`
		SELECT u.session_generation FROM users u
		JOIN user_identities i ON i.user_id = u.id
		WHERE u.id = $1 AND i.provider = $2 AND i.tenant = $3 AND i.subject = $4
		FOR NO KEY UPDATE OF u`, uid, req.Identity.Provider, req.Identity.Tenant, req.Identity.Subject).Scan(&gen)
	if errors.Is(err, sql.ErrNoRows) {
		return refuse("the binding changed during the sign-in", uid, "")
	}
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`UPDATE users SET name = $2, avatar_url = $3, updated_at = NOW() WHERE id = $1`, uid, req.Name, req.AvatarURL); err != nil {
		return nil, err
	}
	if req.StoreCredential != nil {
		if err := req.StoreCredential(tx, uid, SignInReached); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return finish(db, uid, gen, SignInReached)
}

// bindLegacy is the atomic binding of spec §3 "Binding is one atomic step":
// lock the account (after every request lease on it has ended), re-check every
// condition under the lock, write the binding, the standing and the recorded
// identifier, advance the generation, store the credential; all or nothing.
func bindLegacy(ctx context.Context, db *sql.DB, c Carrier, how SignInHow, req SignInRequest) (*SignInResult, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockAccountExclusive(tx, c.ID); err != nil {
		return nil, err
	}
	var providerID string
	if err := tx.QueryRow(`SELECT provider_id FROM users WHERE id = $1 FOR UPDATE`, c.ID).Scan(&providerID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return refuse("the account disappeared during the sign-in", c.ID, "")
		}
		return nil, err
	}
	if bound, err := GetBoundIdentity(tx, c.ID); err != nil {
		return nil, err
	} else if bound != nil {
		return refuse("a competing sign-in bound the account first", c.ID, "")
	}
	carriers, err := CarriersOf(tx, req.Email)
	if err != nil {
		return nil, err
	}
	if len(carriers) != 1 || carriers[0].ID != c.ID {
		return refuse("the address's carriers changed during the sign-in", c.ID, "")
	}
	c.ProviderID = providerID
	again, kind, err := migrationRule(tx, c, req)
	if err != nil {
		return nil, err
	}
	if again != how {
		return refuse("the migration rule no longer holds under the lock", c.ID, kind)
	}
	if _, err := tx.Exec(`
		INSERT INTO user_identities (user_id, provider, tenant, subject, evidence, bound_by)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		c.ID, req.Identity.Provider, req.Identity.Tenant, req.Identity.Subject, req.Evidence.String(), string(how)); err != nil {
		if isUniqueViolation(err) {
			return refuse("the identity was bound to another account concurrently", c.ID, "")
		}
		return nil, err
	}
	var gen int64
	if err := tx.QueryRow(`
		UPDATE users SET address_standing = $2, provider_id = $3, name = $4, avatar_url = $5,
			session_generation = session_generation + 1, updated_at = NOW()
		WHERE id = $1 RETURNING session_generation`,
		c.ID, req.Evidence.standing(), req.Identity.Subject, req.Name, req.AvatarURL).Scan(&gen); err != nil {
		return nil, err
	}
	if req.StoreCredential != nil {
		if err := req.StoreCredential(tx, c.ID, how); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if how == SignInRuleA && req.Evidence == EvidenceMailbox && !domain.IsGenericDomain(domain.ExtractDomain(req.Email)) {
		// D-25: a rule (a) binding on mailbox evidence for a non-consumer
		// domain may be a pre-PAC-71 takeover the record cannot reveal.
		log.Printf("pac71: REVIEW rule (a) binding on mailbox evidence: account %s bound to %s for a non-consumer domain (operator procedure: unbind and reassign if disputed)", c.ID, req.Identity)
	}
	log.Printf("pac71: account %s bound to %s under %s (evidence %s); generation advanced to %d", c.ID, req.Identity, how, req.Evidence, gen)
	return finish(db, c.ID, gen, how)
}

// createAccount is rule 5: a new account bound to the identity, atomic
// against a concurrent creation for the same address in any letter case
// (users_email_key_unique) or for the same identity.
func createAccount(ctx context.Context, db *sql.DB, req SignInRequest) (*SignInResult, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var uid uuid.UUID
	var gen int64
	err = tx.QueryRow(`
		INSERT INTO users (email, email_key, name, avatar_url, provider, provider_id, address_standing)
		VALUES ($1, lower($1), $2, $3, $4, $5, $6)
		RETURNING id, session_generation`,
		req.Email, req.Name, req.AvatarURL, req.FirstProvider, req.Identity.Subject, req.Evidence.standing()).Scan(&uid, &gen)
	if isUniqueViolation(err) {
		return refuse("a concurrent sign-in created an account for this address", uuid.Nil, "")
	}
	if err != nil {
		return nil, err
	}
	// A row without email_key (an existing case collision, or one the old API
	// inserted) is not covered by the index: count every carrier.
	carriers, err := CarriersOf(tx, req.Email)
	if err != nil {
		return nil, err
	}
	if len(carriers) != 1 {
		return refuse("another account carries the address", carriers[0].ID, "")
	}
	if _, err := tx.Exec(`
		INSERT INTO user_identities (user_id, provider, tenant, subject, evidence, bound_by)
		VALUES ($1, $2, $3, $4, $5, 'created')`,
		uid, req.Identity.Provider, req.Identity.Tenant, req.Identity.Subject, req.Evidence.String()); err != nil {
		if isUniqueViolation(err) {
			return refuse("the identity was bound to another account concurrently", uuid.Nil, "")
		}
		return nil, err
	}
	if req.StoreCredential != nil {
		if err := req.StoreCredential(tx, uid, SignInCreated); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return finish(db, uid, gen, SignInCreated)
}

func finish(db *sql.DB, uid uuid.UUID, gen int64, how SignInHow) (*SignInResult, error) {
	u, err := GetUserByID(db, uid)
	if err != nil {
		return nil, err
	}
	return &SignInResult{User: u, Gen: gen, How: how}, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
