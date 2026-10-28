package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// PAC-50 (spec docs/specs/PAC-50.md §4.1, §4.2, §4.6, §4.8): admin step-up
// grants, the SSO event table and the Microsoft tenant pin.

// SSOStepupGrantTTL is a grant's lifetime (spec §4.1). It is deliberately not
// configurable.
const SSOStepupGrantTTL = 10 * time.Minute

// SSOGrantPurposeSSOWrite is the purpose of every grant PAC-50 issues and the
// only purpose an SSO configuration write consumes (contract revision 8.1,
// N5; spec §4.8). A later feature reusing the admin proof issues and consumes
// its own purpose, so neither can spend the other's grant.
const SSOGrantPurposeSSOWrite = "sso_write"

// ErrTenantPinned is returned when an org is already pinned to a different
// Microsoft tenant. The pin is never moved by the application (spec §4.3, NB5).
var ErrTenantPinned = errors.New("organization is pinned to another Microsoft tenant")

// SSOStepupGrant is one granted admin step-up.
type SSOStepupGrant struct {
	ID         uuid.UUID  `json:"id"`
	UserID     uuid.UUID  `json:"user_id"`
	OrgID      uuid.UUID  `json:"org_id"`
	Provider   string     `json:"provider"`
	Purpose    string     `json:"purpose"`
	IssuedAt   time.Time  `json:"issued_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	ConsumedAt *time.Time `json:"consumed_at"`
}

// SSOEvent is one row of the operator-only SSO event table.
type SSOEvent struct {
	ID        uuid.UUID       `json:"id"`
	UserID    uuid.UUID       `json:"user_id"`
	OrgID     *uuid.UUID      `json:"org_id"`
	Kind      string          `json:"kind"`
	Details   json.RawMessage `json:"details"`
	CreatedAt time.Time       `json:"created_at"`
}

// The two event kinds and their details (spec §4.6). Details are marshalled,
// never concatenated.
const (
	SSOEventStepup          = "sso_stepup"
	SSOEventProviderWritten = "sso_provider_written"
)

type ssoStepupDetails struct {
	OrgID    *uuid.UUID `json:"org_id"`
	Provider string     `json:"provider"`
	Outcome  string     `json:"outcome"`
	Reason   *string    `json:"reason"`
	// Purpose is present only for a step-up started for another purpose
	// than an SSO write (PAC-73 Part 2, P2-AC-6), so PAC-50's records are
	// unchanged.
	Purpose string `json:"purpose,omitempty"`
}

type ssoProviderWrittenDetails struct {
	OrgID      uuid.UUID `json:"org_id"`
	ProviderID uuid.UUID `json:"provider_id"`
	Op         string    `json:"op"`
	Via        string    `json:"via"`
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func insertSSOEvent(ctx context.Context, q execer, userID uuid.UUID, orgID *uuid.UUID, kind string, details any) error {
	if userID == uuid.Nil {
		return fmt.Errorf("insertSSOEvent: the acting user is required")
	}
	raw, err := json.Marshal(details)
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx,
		`INSERT INTO sso_events (user_id, org_id, kind, details) VALUES ($1, $2, $3, $4)`,
		userID, orgID, kind, string(raw))
	return err
}

// IssueSSOStepupGrant records a grant for (userID, orgID) with the given
// purpose, issued at now.
func IssueSSOStepupGrant(ctx context.Context, db *sql.DB, userID, orgID uuid.UUID, provider, purpose string, now time.Time) (*SSOStepupGrant, error) {
	return issueGrant(ctx, db, userID, orgID, provider, purpose, now)
}

type queryRower interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func issueGrant(ctx context.Context, q queryRower, userID, orgID uuid.UUID, provider, purpose string, now time.Time) (*SSOStepupGrant, error) {
	g := &SSOStepupGrant{UserID: userID, OrgID: orgID, Provider: provider, Purpose: purpose}
	err := q.QueryRowContext(ctx, `
		INSERT INTO sso_stepup_grants (user_id, org_id, provider, purpose, issued_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, issued_at, expires_at`,
		userID, orgID, provider, purpose, now.UTC(), now.UTC().Add(SSOStepupGrantTTL),
	).Scan(&g.ID, &g.IssuedAt, &g.ExpiresAt)
	if err != nil {
		return nil, err
	}
	return g, nil
}

// ConsumeSSOStepupGrant atomically consumes the caller's newest live grant for
// (userID, orgID) with the given purpose at now (spec §4.2 step 5a): one
// statement selects and marks it, so of two concurrent callers exactly one
// gets it. A grant of another purpose is never selected. ok is false when
// there is no live grant; nothing is changed then.
func ConsumeSSOStepupGrant(ctx context.Context, db *sql.DB, userID, orgID uuid.UUID, purpose string, now time.Time) (id uuid.UUID, ok bool, err error) {
	err = db.QueryRowContext(ctx, `
		UPDATE sso_stepup_grants SET consumed_at = $3
		WHERE id = (
			SELECT id FROM sso_stepup_grants
			WHERE user_id = $1 AND org_id = $2 AND purpose = $4
			  AND consumed_at IS NULL AND expires_at > $3
			ORDER BY issued_at DESC
			LIMIT 1
			FOR UPDATE
		) AND consumed_at IS NULL
		RETURNING id`, userID, orgID, now.UTC(), purpose).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, err
	}
	return id, true, nil
}

// GetSSOStepupGrant returns one grant by id, or nil.
func GetSSOStepupGrant(ctx context.Context, db *sql.DB, id uuid.UUID) (*SSOStepupGrant, error) {
	g := &SSOStepupGrant{}
	var consumed sql.NullTime
	err := db.QueryRowContext(ctx, `
		SELECT id, user_id, org_id, provider, purpose, issued_at, expires_at, consumed_at
		FROM sso_stepup_grants WHERE id = $1`, id).
		Scan(&g.ID, &g.UserID, &g.OrgID, &g.Provider, &g.Purpose, &g.IssuedAt, &g.ExpiresAt, &consumed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if consumed.Valid {
		t := consumed.Time
		g.ConsumedAt = &t
	}
	return g, nil
}

// SSOStepupOutcome is the result of one step-up callback.
type SSOStepupOutcome struct {
	UserID   uuid.UUID
	OrgID    *uuid.UUID // nil only when no org was known (state_mismatch)
	Provider string     // empty when the state could not be read
	Granted  bool
	Reason   string // the spec §4.6 enum; empty when granted
	// PinTenant, on a granted Microsoft step-up, is the tenant id to pin the
	// org to (idempotent; ErrTenantPinned when pinned elsewhere).
	PinTenant string
	// Purpose is the write the grant may authorise: SSOGrantPurposeSSOWrite
	// when empty (PAC-50), or SSOGrantPurposeLLMOrgKeyWrite (PAC-73 Part 2).
	Purpose string
	Now     time.Time
}

// RecordSSOStepupOutcome records a step-up outcome and, when granted, pins the
// tenant and issues the grant, all in one transaction: if any part fails,
// nothing is issued (spec Decisions, D-4). A denied outcome returns a nil grant.
func RecordSSOStepupOutcome(ctx context.Context, db *sql.DB, o SSOStepupOutcome) (*SSOStepupGrant, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	purpose := o.Purpose
	if purpose == "" {
		purpose = SSOGrantPurposeSSOWrite
	}
	details := ssoStepupDetails{OrgID: o.OrgID, Provider: o.Provider, Outcome: "denied"}
	if purpose != SSOGrantPurposeSSOWrite {
		details.Purpose = purpose
	}
	var grant *SSOStepupGrant
	if o.Granted {
		if o.OrgID == nil {
			return nil, fmt.Errorf("RecordSSOStepupOutcome: a grant needs an org")
		}
		details.Outcome = "granted"
		if o.PinTenant != "" {
			res, err := tx.ExecContext(ctx, `
				UPDATE organizations SET ms_tenant_id = $2
				WHERE id = $1 AND (ms_tenant_id IS NULL OR ms_tenant_id = $2)`, *o.OrgID, o.PinTenant)
			if err != nil {
				return nil, err
			}
			if n, err := res.RowsAffected(); err != nil {
				return nil, err
			} else if n != 1 {
				return nil, ErrTenantPinned
			}
		}
		// The grant carries the purpose the step-up was started for.
		if grant, err = issueGrant(ctx, tx, o.UserID, *o.OrgID, o.Provider, purpose, o.Now); err != nil {
			return nil, err
		}
	} else {
		r := o.Reason
		details.Reason = &r
	}
	if err := insertSSOEvent(ctx, tx, o.UserID, o.OrgID, SSOEventStepup, details); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return grant, nil
}

// GetOrgMSTenantID returns the org's pinned Microsoft tenant id, or "".
func GetOrgMSTenantID(ctx context.Context, db *sql.DB, orgID uuid.UUID) (string, error) {
	var tid sql.NullString
	err := db.QueryRowContext(ctx, `SELECT ms_tenant_id FROM organizations WHERE id = $1`, orgID).Scan(&tid)
	if err != nil {
		return "", err
	}
	return tid.String, nil
}

// ListSSOEventsByUser returns the events a user caused, oldest first. For
// operators and tests; no API operation exposes the table (spec §4.6).
func ListSSOEventsByUser(ctx context.Context, db *sql.DB, userID uuid.UUID) ([]SSOEvent, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, user_id, org_id, kind, details::text, created_at
		FROM sso_events WHERE user_id = $1 ORDER BY created_at, id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]SSOEvent, 0)
	for rows.Next() {
		var e SSOEvent
		var org uuid.NullUUID
		var details string
		if err := rows.Scan(&e.ID, &e.UserID, &org, &e.Kind, &details, &e.CreatedAt); err != nil {
			return nil, err
		}
		if org.Valid {
			o := org.UUID
			e.OrgID = &o
		}
		e.Details = json.RawMessage(details)
		out = append(out, e)
	}
	return out, rows.Err()
}

// UpsertSSOProviderRecorded writes the provider row (domain lower-cased) and
// its sso_provider_written event in one transaction, so a write is never left
// unrecorded. via is "stepup" or "breakglass".
func UpsertSSOProviderRecorded(ctx context.Context, db *sql.DB, p *SSOProvider, actor, orgID uuid.UUID, via string) (*SSOProvider, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	stored, inserted, err := upsertSSOProvider(ctx, tx, p)
	if err != nil {
		return nil, err
	}
	op := "update"
	if inserted {
		op = "create"
	}
	if err := insertSSOEvent(ctx, tx, actor, &orgID, SSOEventProviderWritten,
		ssoProviderWrittenDetails{OrgID: orgID, ProviderID: stored.ID, Op: op, Via: via}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return stored, nil
}

// SaveOIDCProviderRecorded stores a createSsoProvider write (PAC-22's
// SaveOIDCProvider rules: the kept secret is re-decided inside the
// transaction, the active state of a replace is kept) and its
// sso_provider_written event in one transaction, so a write is never left
// unrecorded and a refused or failed store records nothing. The domain is
// stored lower-cased. via is "stepup" or "breakglass".
func SaveOIDCProviderRecorded(ctx context.Context, db *sql.DB, w OIDCProviderWrite, actor, orgID uuid.UUID, via string) (*SSOProvider, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	stored, inserted, err := saveOIDCProvider(ctx, tx, w)
	if err != nil {
		return nil, err
	}
	op := "update"
	if inserted {
		op = "create"
	}
	if err := insertSSOEvent(ctx, tx, actor, &orgID, SSOEventProviderWritten,
		ssoProviderWrittenDetails{OrgID: orgID, ProviderID: stored.ID, Op: op, Via: via}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return stored, nil
}

// DeleteSSOProviderRecorded deletes the provider row for the lower-cased
// domain and records the deletion in one transaction. found is false, and
// nothing is recorded, when there was no row (API-067).
func DeleteSSOProviderRecorded(ctx context.Context, db *sql.DB, domain string, actor, orgID uuid.UUID, via string) (bool, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	var id uuid.UUID
	err = tx.QueryRowContext(ctx,
		`DELETE FROM sso_providers WHERE lower(domain) = $1 RETURNING id`,
		strings.ToLower(domain)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := insertSSOEvent(ctx, tx, actor, &orgID, SSOEventProviderWritten,
		ssoProviderWrittenDetails{OrgID: orgID, ProviderID: id, Op: "delete", Via: via}); err != nil {
		return false, err
	}
	return true, tx.Commit()
}
