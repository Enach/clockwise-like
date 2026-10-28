package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// PAC-71 operator procedure (spec docs/specs/PAC-71.md §3 "Operator
// procedure", D-22, D-25; contract §9; plan §8). Operator-only: these run from
// the server-side command backend/cmd/paceday-operator and have no API route,
// web page or MCP tool. Every run is audited in operator_audit, which no
// account change and no down migration removes.

// ErrOperatorInput is wrapped by every refusal caused by the operator's input.
var ErrOperatorInput = errors.New("operator input")

// OperatorRun names who runs a step and why; both are required.
type OperatorRun struct {
	Operator string
	Reason   string
}

func (r OperatorRun) validate() error {
	if strings.TrimSpace(r.Operator) == "" || strings.TrimSpace(r.Reason) == "" {
		return fmt.Errorf("%w: operator and reason are required", ErrOperatorInput)
	}
	return nil
}

// FreedAddress is the non-deliverable placeholder an operator gives an
// account whose address is freed (the .invalid TLD never delivers mail).
func FreedAddress(id uuid.UUID) string { return "freed+" + id.String() + "@paceday.invalid" }

// removal is one kind of data an operator step can remove.
type removal struct {
	name  string
	stmts []string
}

var (
	removeCalendarCredentials = []removal{
		{"google_calendar_credential", []string{`DELETE FROM oauth_tokens WHERE user_id = $1`, `DELETE FROM credential_losses WHERE user_id = $1 AND provider = 'google'`}},
		{"microsoft_credential", []string{`UPDATE settings SET microsoft_tokens = NULL WHERE user_id = $1`, `DELETE FROM credential_losses WHERE user_id = $1 AND provider = 'outlook'`}},
		{"personal_calendars", []string{`DELETE FROM personal_calendars WHERE user_id = $1`}},
	}
	removeOther = map[string]removal{
		"slack":    {"slack_connection", []string{`DELETE FROM workspace_connections WHERE user_id = $1 AND provider = 'slack'`}},
		"notion":   {"notion_connection", []string{`DELETE FROM workspace_connections WHERE user_id = $1 AND provider = 'notion'`}},
		"zoom":     {"zoom_credential", []string{`UPDATE settings SET zoom_tokens = '' WHERE user_id = $1`, `DELETE FROM credential_losses WHERE user_id = $1 AND provider = 'zoom'`}},
		"ai-keys":  {"ai_keys", []string{`UPDATE settings SET llm_api_key = '', llm_api_key_openai = '', llm_api_key_anthropic = '', llm_api_key_azure_openai = '', llm_api_key_bedrock = '', llm_api_key_vertex = '', llm_base_url = '' WHERE user_id = $1`}},
		"external": {"external_settings", []string{`UPDATE settings SET webcal_url = '', calendar_email = '', recap_channel_id = '' WHERE user_id = $1`}},
	}
)

// OtherRemovals lists the names ReassignAccount's remove list accepts.
func OtherRemovals() []string { return []string{"slack", "notion", "zoom", "ai-keys", "external"} }

func applyRemovals(tx *sql.Tx, id uuid.UUID, rs []removal) ([]string, error) {
	done := make([]string, 0, len(rs))
	for _, r := range rs {
		for _, s := range r.stmts {
			if _, err := tx.Exec(s, id); err != nil {
				return nil, fmt.Errorf("remove %s: %w", r.name, err)
			}
		}
		done = append(done, r.name)
	}
	return done, nil
}

// lockForOperator starts an operator transaction on an account: it waits for
// every request lease on the account, then locks its row.
func lockForOperator(ctx context.Context, db *sql.DB, id uuid.UUID) (*sql.Tx, string, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, "", err
	}
	if err := lockAccountExclusive(tx, id); err != nil {
		_ = tx.Rollback()
		return nil, "", err
	}
	var email string
	if err := tx.QueryRow(`SELECT email FROM users WHERE id = $1 FOR UPDATE`, id).Scan(&email); err != nil {
		_ = tx.Rollback()
		if errors.Is(err, sql.ErrNoRows) {
			return nil, "", fmt.Errorf("%w: no account %s", ErrOperatorInput, id)
		}
		return nil, "", err
	}
	return tx, email, nil
}

func audit(tx *sql.Tx, action string, run OperatorRun, account *uuid.UUID, email, prev, next, evidence string, removed []string, details map[string]any) error {
	if removed == nil {
		removed = []string{}
	}
	if details == nil {
		details = map[string]any{}
	}
	rj, _ := json.Marshal(removed)
	dj, _ := json.Marshal(details)
	null := func(s string) any {
		if s == "" {
			return nil
		}
		return s
	}
	_, err := tx.Exec(`
		INSERT INTO operator_audit (action, operator, reason, account_id, account_email, previous_identity, new_identity, evidence, removed, details)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		action, run.Operator, run.Reason, account, null(email), null(prev), null(next), null(evidence), string(rj), string(dj))
	return err
}

// FreeAddress is operator procedure step 3: the account is never deleted; its
// address becomes FreedAddress, its standing unconfirmed, and with squatter
// every stored credential, connection and key is removed. The generation
// advances, ending every session and every connect in flight. The binding is
// kept. Audited.
func FreeAddress(ctx context.Context, db *sql.DB, id uuid.UUID, squatter bool, run OperatorRun) (string, error) {
	if err := run.validate(); err != nil {
		return "", err
	}
	tx, email, err := lockForOperator(ctx, db, id)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	placeholder := FreedAddress(id)
	if _, err := tx.Exec(`
		UPDATE users SET email = $2, email_key = lower($2), address_standing = 'unconfirmed',
			session_generation = session_generation + 1, updated_at = NOW()
		WHERE id = $1`, id, placeholder); err != nil {
		return "", err
	}
	removed := []string{}
	if squatter {
		rs := append([]removal{}, removeCalendarCredentials...)
		for _, k := range OtherRemovals() {
			rs = append(rs, removeOther[k])
		}
		if removed, err = applyRemovals(tx, id, rs); err != nil {
			return "", err
		}
	}
	prev := ""
	if b, err := GetBoundIdentity(tx, id); err != nil {
		return "", err
	} else if b != nil {
		prev = b.String()
	}
	if err := audit(tx, "free_address", run, &id, email, prev, prev, "", removed,
		map[string]any{"freed_address": email, "placeholder": placeholder, "squatter": squatter}); err != nil {
		return "", err
	}
	return placeholder, tx.Commit()
}

// ParseOperatorIdentity reads google:<sub> or microsoft:<tenant id>:<object id>.
func ParseOperatorIdentity(s string) (Identity, error) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	switch {
	case len(parts) == 2 && parts[0] == IdentityGoogle && parts[1] != "":
		return Identity{Provider: IdentityGoogle, Subject: parts[1]}, nil
	case len(parts) == 3 && parts[0] == IdentityMicrosoft && parts[1] != "" && parts[2] != "":
		return Identity{Provider: IdentityMicrosoft, Tenant: strings.ToLower(parts[1]), Subject: strings.ToLower(parts[2])}, nil
	}
	return Identity{}, fmt.Errorf("%w: identity must be google:<sub> or microsoft:<tenant>:<oid>, got %q", ErrOperatorInput, s)
}

// ReassignRequest is the "unbind and reassign" step.
type ReassignRequest struct {
	Account                 uuid.UUID
	Identity                Identity
	Evidence                Evidence
	KeepCalendarCredentials bool     // the operator confirmed they were issued to Identity
	Remove                  []string // OtherRemovals names the verified person disowns
	Run                     OperatorRun
}

// ReassignResult says what the step did.
type ReassignResult struct {
	Previous string
	Removed  []string
	Gen      int64
}

// ReassignAccount replaces (or writes) the account's binding with the
// verified identity (spec §3 "Unbind and reassign", AC-49): standing from the
// evidence the refusal log recorded, the recorded identifier set to the
// subject, every calendar credential removed unless kept, the named other
// data removed, the generation advanced. Data the account holds is otherwise
// unchanged. Audited.
func ReassignAccount(ctx context.Context, db *sql.DB, req ReassignRequest) (*ReassignResult, error) {
	if err := req.Run.validate(); err != nil {
		return nil, err
	}
	if req.Identity.Provider != IdentityGoogle && req.Identity.Provider != IdentityMicrosoft || req.Identity.Subject == "" {
		return nil, fmt.Errorf("%w: only a Google or Microsoft identity can be bound by the operator", ErrOperatorInput)
	}
	if req.Evidence != EvidenceAuthoritative && req.Evidence != EvidenceMailbox {
		return nil, fmt.Errorf("%w: evidence must be authoritative or mailbox", ErrOperatorInput)
	}
	rs := []removal{}
	if !req.KeepCalendarCredentials {
		rs = append(rs, removeCalendarCredentials...)
	}
	for _, name := range req.Remove {
		r, ok := removeOther[strings.TrimSpace(name)]
		if !ok {
			return nil, fmt.Errorf("%w: unknown removal %q", ErrOperatorInput, name)
		}
		rs = append(rs, r)
	}
	tx, email, err := lockForOperator(ctx, db, req.Account)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	prev := ""
	if b, err := GetBoundIdentity(tx, req.Account); err != nil {
		return nil, err
	} else if b != nil {
		prev = b.String()
	}
	if other, found, err := AccountForIdentity(tx, req.Identity); err != nil {
		return nil, err
	} else if found && other != req.Account {
		return nil, fmt.Errorf("%w: %s is bound to another account (%s); free or reassign that one first", ErrOperatorInput, req.Identity, other)
	}
	if _, err := tx.Exec(`DELETE FROM user_identities WHERE user_id = $1`, req.Account); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`
		INSERT INTO user_identities (user_id, provider, tenant, subject, evidence, bound_by)
		VALUES ($1, $2, $3, $4, $5, 'operator')`,
		req.Account, req.Identity.Provider, req.Identity.Tenant, req.Identity.Subject, req.Evidence.String()); err != nil {
		return nil, err
	}
	var gen int64
	if err := tx.QueryRow(`
		UPDATE users SET address_standing = $2, provider_id = $3,
			session_generation = session_generation + 1, updated_at = NOW()
		WHERE id = $1 RETURNING session_generation`,
		req.Account, req.Evidence.standing(), req.Identity.Subject).Scan(&gen); err != nil {
		return nil, err
	}
	removed, err := applyRemovals(tx, req.Account, rs)
	if err != nil {
		return nil, err
	}
	if err := audit(tx, "reassign", req.Run, &req.Account, email, prev, req.Identity.String(), req.Evidence.String(), removed,
		map[string]any{"kept_calendar_credentials": req.KeepCalendarCredentials}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &ReassignResult{Previous: prev, Removed: removed, Gen: gen}, nil
}

// AdvanceAllGenerations is the one-time deploy step of OQ-6 (spec AC-44):
// every account's generation advances by one, so every existing session gets
// 401 on its next request. Nothing else changes. Audited once. Whether a
// release runs it is the owner's decision.
func AdvanceAllGenerations(ctx context.Context, db *sql.DB, run OperatorRun) (int64, error) {
	if err := run.validate(); err != nil {
		return 0, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	// Like every advance, lock the account rows FOR UPDATE first, so no
	// in-request re-check is between its comparison and its read or write.
	if _, err := tx.Exec(`SET LOCAL lock_timeout = '30s'`); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`SELECT id FROM users FOR UPDATE`); err != nil {
		return 0, err
	}
	res, err := tx.Exec(`UPDATE users SET session_generation = session_generation + 1`)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if err := audit(tx, "advance_all", run, nil, "", "", "", "", nil, map[string]any{"accounts": n}); err != nil {
		return 0, err
	}
	return n, tx.Commit()
}
