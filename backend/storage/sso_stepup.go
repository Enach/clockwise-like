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
func consumeSSOStepupGrant(ctx context.Context, db Querier, userID, orgID uuid.UUID, purpose string, now time.Time) (id uuid.UUID, ok bool, err error) {
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

// StepupDenialRecordBound is how many denied step-up outcomes of one user are
// recorded individually per StepupDenialWindow (PAC-75 contract §3.1; owner
// OQ-1 factory default). Each one beyond is counted in
// sso_stepup_denial_windows.left_out instead. Granted outcomes are always
// recorded.
const StepupDenialRecordBound = 5

// StepupDenialWindow is the length of a denial window. A window starts at a
// multiple of it since the Unix epoch (UTC-aligned), from the outcome's Now.
const StepupDenialWindow = 10 * time.Minute

// StepupDenialWindowStart is the start of the denial window holding t:
// floor(unix_seconds(t) / 600) * 600, in UTC (contract §3.1). Go's zero time
// is a whole number of windows before the Unix epoch, so Truncate is aligned
// to the epoch too.
func StepupDenialWindowStart(t time.Time) time.Time {
	return t.UTC().Truncate(StepupDenialWindow)
}

// SSOStepupRecord is what recording one outcome did.
type SSOStepupRecord struct {
	// Grant is the issued grant, nil for a denied outcome.
	Grant *SSOStepupGrant
	// WindowStart and LeftOut describe a denied outcome's window after this
	// record: LeftOut is 0 when this outcome was recorded individually, and
	// the window's left-out count otherwise. Both are zero for a grant.
	WindowStart time.Time
	LeftOut     int
}

// RecordSSOStepupOutcome records a step-up outcome and, when granted, pins the
// tenant and issues the grant, all in one transaction: if any part fails,
// nothing is issued (spec Decisions, D-4). A denied outcome returns a nil
// grant, and is recorded individually only within the PAC-75 bound
// (RecordSSOStepupOutcomeWindow).
func RecordSSOStepupOutcome(ctx context.Context, db DBTX, o SSOStepupOutcome) (*SSOStepupGrant, error) {
	rec, err := RecordSSOStepupOutcomeWindow(ctx, db, o)
	return rec.Grant, err
}

// RecordSSOStepupOutcomeWindow is RecordSSOStepupOutcome reporting what was
// done with a denied outcome (PAC-75 contract §3.3). One READ COMMITTED
// transaction:
//
//  1. the PAC-71 re-check, which KEY SHARE-locks the session's user; when no
//     guard names o.UserID, the acting user's row is KEY SHARE-locked
//     explicitly, so it is always the first lock (lock order, contract §3.5:
//     users, then one window row, then organizations through the event's FK);
//  2. granted: tenant pin, grant and event, as before; no window statement;
//  3. denied: a transaction-local lock_timeout (LinkLockTimeout), then one
//     upsert of the (user, window) row deciding "recorded or counted";
//  4. the sso_stepup event only when this outcome is recorded individually
//     (the returned left_out is 0);
//  5. commit. Any failure rolls everything back: no count without its
//     event, no event without its count.
func RecordSSOStepupOutcomeWindow(ctx context.Context, db DBTX, o SSOStepupOutcome) (SSOStepupRecord, error) {
	var rec SSOStepupRecord
	tx, err := db.BeginTx(ctx, nil)
	// PAC-71 in-request re-check, in this transaction.
	if err == nil {
		if rerr := RecheckSessionGeneration(ctx, tx); rerr != nil {
			_ = tx.Rollback()
			err = rerr
		}
	}
	if err != nil {
		return rec, err
	}
	defer func() { _ = tx.Rollback() }()

	// PAC-75 §3.3 step 1: without a guard for the acting user, the re-check
	// locked nothing of theirs; take the same KEY SHARE explicitly, before
	// the window row (contract §3.5: without it a concurrent account
	// deletion deadlocks with the window row's FK check).
	if g := SessionGuardFrom(ctx); g == nil || g.UserID != o.UserID {
		var one int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM users WHERE id = $1 FOR KEY SHARE`, o.UserID).Scan(&one); err != nil {
			return SSOStepupRecord{}, fmt.Errorf("lock step-up actor: %w", err)
		}
	}

	purpose := o.Purpose
	if purpose == "" {
		purpose = SSOGrantPurposeSSOWrite
	}
	details := ssoStepupDetails{OrgID: o.OrgID, Provider: o.Provider, Outcome: "denied"}
	if purpose != SSOGrantPurposeSSOWrite {
		details.Purpose = purpose
	}
	if o.Granted {
		if o.OrgID == nil {
			return SSOStepupRecord{}, fmt.Errorf("RecordSSOStepupOutcome: a grant needs an org")
		}
		details.Outcome = "granted"
		if o.PinTenant != "" {
			res, err := tx.ExecContext(ctx, `
				UPDATE organizations SET ms_tenant_id = lower($2)
				WHERE id = $1 AND (ms_tenant_id IS NULL OR lower(ms_tenant_id) = lower($2))`, *o.OrgID, o.PinTenant)
			if err != nil {
				return SSOStepupRecord{}, err
			}
			if n, err := res.RowsAffected(); err != nil {
				return SSOStepupRecord{}, err
			} else if n != 1 {
				return SSOStepupRecord{}, ErrTenantPinned
			}
		}
		// The grant carries the purpose the step-up was started for.
		if rec.Grant, err = issueGrant(ctx, tx, o.UserID, *o.OrgID, o.Provider, purpose, o.Now); err != nil {
			return SSOStepupRecord{}, err
		}
	} else {
		r := o.Reason
		details.Reason = &r
		now := o.Now
		if now.IsZero() {
			now = time.Now()
		}
		rec.WindowStart = StepupDenialWindowStart(now)
		if err := setLinkLockTimeout(tx); err != nil {
			return SSOStepupRecord{}, err
		}
		// left_out grows only once recorded has reached the bound, and
		// recorded never grows after that: left_out = 0 after this statement
		// means this statement incremented recorded (contract §3.3).
		if err := tx.QueryRowContext(ctx, `
			INSERT INTO sso_stepup_denial_windows AS w (user_id, window_start, recorded)
			VALUES ($1, $2, 1)
			ON CONFLICT (user_id, window_start) DO UPDATE SET
			  recorded         = w.recorded + CASE WHEN w.recorded < $3 THEN 1 ELSE 0 END,
			  left_out         = w.left_out + CASE WHEN w.recorded < $3 THEN 0 ELSE 1 END,
			  last_left_out_at = CASE WHEN w.recorded < $3 THEN w.last_left_out_at ELSE $4 END
			RETURNING left_out`,
			o.UserID, rec.WindowStart, StepupDenialRecordBound, now.UTC(),
		).Scan(&rec.LeftOut); err != nil {
			return SSOStepupRecord{}, err
		}
		fireAfterStepupDenialUpsert(o.UserID)
	}
	if rec.LeftOut == 0 {
		if err := insertSSOEvent(ctx, tx, o.UserID, o.OrgID, SSOEventStepup, details); err != nil {
			return SSOStepupRecord{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return SSOStepupRecord{}, err
	}
	return rec, nil
}

// SSOStepupDenialWindow is one row of sso_stepup_denial_windows (operators
// and tests; no API operation reads it, contract §3.6).
type SSOStepupDenialWindow struct {
	UserID        uuid.UUID  `json:"user_id"`
	WindowStart   time.Time  `json:"window_start"`
	Recorded      int        `json:"recorded"`
	LeftOut       int        `json:"left_out"`
	LastLeftOutAt *time.Time `json:"last_left_out_at"`
}

// ListSSOStepupDenialWindows returns a user's denial windows, oldest first.
func ListSSOStepupDenialWindows(ctx context.Context, db *sql.DB, userID uuid.UUID) ([]SSOStepupDenialWindow, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT user_id, window_start, recorded, left_out, last_left_out_at
		FROM sso_stepup_denial_windows WHERE user_id = $1 ORDER BY window_start`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]SSOStepupDenialWindow, 0)
	for rows.Next() {
		var w SSOStepupDenialWindow
		var last sql.NullTime
		if err := rows.Scan(&w.UserID, &w.WindowStart, &w.Recorded, &w.LeftOut, &last); err != nil {
			return nil, err
		}
		if last.Valid {
			t := last.Time
			w.LastLeftOutAt = &t
		}
		out = append(out, w)
	}
	return out, rows.Err()
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
	// PAC-71 in-request re-check, in this transaction.
	if err == nil {
		if rerr := RecheckSessionGeneration(ctx, tx); rerr != nil {
			_ = tx.Rollback()
			err = rerr
		}
	}
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
	// PAC-71 in-request re-check, in this transaction.
	if err == nil {
		if rerr := RecheckSessionGeneration(ctx, tx); rerr != nil {
			_ = tx.Rollback()
			err = rerr
		}
	}
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
