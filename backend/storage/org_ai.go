package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Enach/paceday/backend/internal/secretbox"
	"github.com/google/uuid"
)

// PAC-73 Part 2 (spec docs/specs/NEW-llm-bring-your-own-part2.md §3.2, §3.4,
// §7; contract revision 3.1): the organisation's AI configuration, each
// member's confirmation of it, and the operator record of every change
// (migration 031_llm_org_ai).
//
// PAC-87 (spec docs/specs/PAC-87.md §3.1, §3.4; contract
// contracts/features/PAC-87.md §1, §5, §5a; migration
// 036_llm_org_ai_consent_period): a confirmation covers the organisation's
// provider for one consent period. Every provider change (first setup, a
// change to another provider, removal) starts a new period at the version it
// produces (ConsentSince); key, destination, model and personal-keys changes
// do not. A member is confirmed exactly when their stored period equals the
// organisation's current one.

// SSOGrantPurposeLLMOrgKeyWrite is the purpose of a step-up grant that may
// authorise one change of the organisation's AI configuration. An SSO write
// never consumes it, and an organisation-AI change never consumes an
// sso_write grant (PAC-50 AC-27).
const SSOGrantPurposeLLMOrgKeyWrite = "llm_org_key_write"

// SSOEventLLMOrgAIWritten is the operator record of an organisation-AI change.
const SSOEventLLMOrgAIWritten = "llm_org_ai_written"

// The contract's property names, used in operator records and messages.
const (
	OrgAIPropProvider            = "provider"
	OrgAIPropModel               = "model"
	OrgAIPropAPIKey              = "apiKey"
	OrgAIPropBaseURL             = "baseUrl"
	OrgAIPropAzureEndpoint       = "azureEndpoint"
	OrgAIPropAzureDeployment     = "azureDeployment"
	OrgAIPropAzureAPIVersion     = "azureApiVersion"
	OrgAIPropAWSRegion           = "awsRegion"
	OrgAIPropPersonalKeysAllowed = "personalKeysAllowed"
)

// OrgAIConfig is one organisation's stored AI configuration. An organisation
// with no stored row reads as the zero configuration with
// PersonalKeysAllowed true and Version 0 (contract: getOrganizationAI
// "neverSet").
type OrgAIConfig struct {
	Provider            string
	Model               string
	BaseURL             string
	AzureEndpoint       string
	AzureDeployment     string
	AzureAPIVersion     string
	AWSRegion           string
	APIKey              string
	PersonalKeysAllowed bool
	Version             int64
	// ConsentSince is the version that started the current consent period
	// (PAC-87): the version produced by the latest provider change, 0 before
	// any. Never above Version.
	ConsentSince int64
	UpdatedAt    *time.Time

	// PAC-84: the stored api_key text as read, and its decryption failure
	// (APIKey is then ""). Presence is the stored text being non-empty.
	rawKey string
	keyErr error
}

// KeyErr is the decryption failure of the stored key, nil when it opened.
func (c OrgAIConfig) KeyErr() error { return c.keyErr }

// KeyPresent reports a stored key (D-3: no decryption needed).
func (c OrgAIConfig) KeyPresent() bool { return c.APIKey != "" || c.keyErr != nil }

// defaultOrgAIConfig is what an organisation with no stored row reads as.
func defaultOrgAIConfig() OrgAIConfig { return OrgAIConfig{PersonalKeysAllowed: true} }

// Complete reports whether the configuration can be used and confirmed: a
// provider, a key, and for azure_openai an endpoint and a deployment (spec
// §3.1 "Complete"; contract OrganizationAIConfiguration.configured).
func (c OrgAIConfig) Complete() bool {
	if c.Provider == "" || !c.KeyPresent() {
		return false
	}
	if c.Provider == "azure_openai" {
		return c.AzureEndpoint != "" && c.AzureDeployment != ""
	}
	return true
}

// OrgAIState is one consistent read of a user's organisation membership, the
// organisation's AI configuration and the user's confirmation (spec §3.1:
// "read together, as one consistent state"). It comes from one SQL statement,
// so it is one snapshot: a request never combines values of two versions.
type OrgAIState struct {
	OrgID  *uuid.UUID
	Config OrgAIConfig
	// ConfirmedSince is the consent period the user's stored confirmation
	// covers (PAC-87); 0: no confirmation.
	ConfirmedSince int64
}

// InOrg reports whether the user belongs to an organisation.
func (s OrgAIState) InOrg() bool { return s.OrgID != nil }

// Forbids reports whether the user's organisation forbids personal keys.
func (s OrgAIState) Forbids() bool { return s.InOrg() && !s.Config.PersonalKeysAllowed }

// Confirmed reports whether the user's stored confirmation covers the
// consent period this state read (PAC-87 §3.1 "Confirmed"): no provider
// change since it was recorded. Read in the same statement as the
// configuration, so a request never pairs one configuration with another's
// consent marker.
func (s OrgAIState) Confirmed() bool {
	return s.InOrg() && s.ConfirmedSince != 0 && s.ConfirmedSince == s.Config.ConsentSince
}

const orgAIColumns = `c.provider, c.model, c.base_url, c.azure_endpoint, c.azure_deployment,
	c.azure_api_version, c.aws_region, c.api_key, c.personal_keys_allowed,
	c.configuration_version, c.consent_since_version, c.updated_at`

// orgAIScan holds the nullable columns of a LEFT JOIN on the configuration.
type orgAIScan struct {
	provider, model, baseURL, azureEndpoint, azureDeployment sql.NullString
	azureAPIVersion, awsRegion, apiKey                       sql.NullString
	personalKeysAllowed                                      sql.NullBool
	version, consentSince                                    sql.NullInt64
	updatedAt                                                sql.NullTime
}

func (s *orgAIScan) dest() []any {
	return []any{&s.provider, &s.model, &s.baseURL, &s.azureEndpoint, &s.azureDeployment,
		&s.azureAPIVersion, &s.awsRegion, &s.apiKey, &s.personalKeysAllowed, &s.version, &s.consentSince, &s.updatedAt}
}

func (s *orgAIScan) config(orgID uuid.UUID) OrgAIConfig {
	if !s.version.Valid {
		return defaultOrgAIConfig()
	}
	c := OrgAIConfig{
		Provider: s.provider.String, Model: s.model.String, BaseURL: s.baseURL.String,
		AzureEndpoint: s.azureEndpoint.String, AzureDeployment: s.azureDeployment.String,
		AzureAPIVersion: s.azureAPIVersion.String, AWSRegion: s.awsRegion.String,
		APIKey: s.apiKey.String, PersonalKeysAllowed: s.personalKeysAllowed.Bool, Version: s.version.Int64,
		ConsentSince: s.consentSince.Int64,
	}
	// PAC-84: opened on its own; a failure never fails the read (6.2).
	c.rawKey = s.apiKey.String
	if plain, err := openSecret(bindOrgAI(orgID), c.rawKey, uuid.Nil); err != nil {
		c.APIKey, c.keyErr = "", err
	} else {
		c.APIKey = plain
	}
	if s.updatedAt.Valid {
		t := s.updatedAt.Time.UTC()
		c.UpdatedAt = &t
	}
	return c
}

// ReadOrgAIState reads the user's organisation, its AI configuration and the
// user's confirmation in ONE statement. A user with no row reads as being in
// no organisation, as UserInOrganization does.
func ReadOrgAIState(ctx context.Context, db *sql.DB, userID uuid.UUID) (OrgAIState, error) {
	var st OrgAIState
	var org uuid.NullUUID
	var sc orgAIScan
	var confirmed sql.NullInt64
	err := db.QueryRowContext(ctx, `
		SELECT u.org_id, `+orgAIColumns+`, f.consent_since_version
		FROM users u
		LEFT JOIN org_ai_configurations c ON c.org_id = u.org_id
		LEFT JOIN org_ai_confirmations f ON f.user_id = u.id AND f.org_id = u.org_id
		WHERE u.id = $1`, userID).Scan(append(append([]any{&org}, sc.dest()...), &confirmed)...)
	if errors.Is(err, sql.ErrNoRows) {
		st.Config = defaultOrgAIConfig()
		return st, nil
	}
	if err != nil {
		return OrgAIState{}, fmt.Errorf("ReadOrgAIState: %w", err)
	}
	if org.Valid {
		o := org.UUID
		st.OrgID = &o
	}
	st.Config = sc.config(org.UUID)
	st.ConfirmedSince = confirmed.Int64
	return st, nil
}

type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// GetOrgAIConfig reads one organisation's configuration (the default when it
// has no row).
func GetOrgAIConfig(ctx context.Context, db *sql.DB, orgID uuid.UUID) (OrgAIConfig, error) {
	return getOrgAIConfig(ctx, db, orgID, "")
}

func getOrgAIConfig(ctx context.Context, q rowQuerier, orgID uuid.UUID, lock string) (OrgAIConfig, error) {
	var sc orgAIScan
	err := q.QueryRowContext(ctx, `SELECT `+orgAIColumns+` FROM org_ai_configurations c WHERE c.org_id = $1`+lock, orgID).
		Scan(sc.dest()...)
	if errors.Is(err, sql.ErrNoRows) {
		return defaultOrgAIConfig(), nil
	}
	if err != nil {
		return OrgAIConfig{}, err
	}
	return sc.config(orgID), nil
}

// OrgAIPatch is a change to the configuration as patchOrganizationAI's body
// states it: nil means absent (unchanged). Validation is the caller's; Merge
// only applies the contract's merge rules.
type OrgAIPatch struct {
	Provider            *string
	Model               *string
	APIKey              *string
	BaseURL             *string
	AzureEndpoint       *string
	AzureDeployment     *string
	AzureAPIVersion     *string
	AWSRegion           *string
	PersonalKeysAllowed *bool
}

// Merge applies p to stored (contract OrganizationAIConfigurationUpdate):
//
//   - a provider different from the stored one replaces the whole
//     configuration: every value not in the patch takes its default, except
//     the personal-keys setting (spec revision 2.1);
//   - provider "" removes the configuration: provider, model, destination
//     and key are emptied, the personal-keys setting is kept;
//   - the version advances by one on a provider change (including the
//     removal), on any apiKey in the patch (keys are never compared), and on
//     any destination value that changes; never otherwise, and it is never
//     reset, so a version is never reused (spec P2-D4, P2-D17);
//   - a provider change (including the removal) starts a new consent period
//     at the new version; nothing else moves it (PAC-87 §1), so consent
//     periods are never reused either.
//
// changed lists the contract names of the properties whose value changed
// (apiKey whenever present), for the operator record; never a value.
func (p OrgAIPatch) Merge(stored OrgAIConfig) (next OrgAIConfig, changed []string) {
	next = stored
	changed = make([]string, 0, 9)
	providerChanged := p.Provider != nil && *p.Provider != stored.Provider
	if providerChanged {
		next = OrgAIConfig{Provider: *p.Provider, PersonalKeysAllowed: stored.PersonalKeysAllowed,
			Version: stored.Version, ConsentSince: stored.ConsentSince, UpdatedAt: stored.UpdatedAt}
	}
	set := func(dst *string, v *string) {
		if v != nil {
			*dst = *v
		}
	}
	set(&next.Model, p.Model)
	set(&next.APIKey, p.APIKey)
	if p.APIKey != nil {
		next.rawKey, next.keyErr = "", nil // PAC-84: replaced, sealed anew
	}
	set(&next.BaseURL, p.BaseURL)
	set(&next.AzureEndpoint, p.AzureEndpoint)
	set(&next.AzureDeployment, p.AzureDeployment)
	set(&next.AzureAPIVersion, p.AzureAPIVersion)
	set(&next.AWSRegion, p.AWSRegion)
	if p.PersonalKeysAllowed != nil {
		next.PersonalKeysAllowed = *p.PersonalKeysAllowed
	}

	destinationChanged := false
	for _, d := range []struct {
		name     string
		old, new string
		dest     bool
	}{
		{OrgAIPropProvider, stored.Provider, next.Provider, false},
		{OrgAIPropModel, stored.Model, next.Model, false},
		{OrgAIPropBaseURL, stored.BaseURL, next.BaseURL, true},
		{OrgAIPropAzureEndpoint, stored.AzureEndpoint, next.AzureEndpoint, true},
		{OrgAIPropAzureDeployment, stored.AzureDeployment, next.AzureDeployment, true},
		{OrgAIPropAzureAPIVersion, stored.AzureAPIVersion, next.AzureAPIVersion, true},
		{OrgAIPropAWSRegion, stored.AWSRegion, next.AWSRegion, true},
	} {
		if d.old != d.new {
			changed = append(changed, d.name)
			if d.dest {
				destinationChanged = true
			}
		}
	}
	keyWritten := p.APIKey != nil || (providerChanged && stored.KeyPresent())
	if keyWritten {
		changed = append(changed, OrgAIPropAPIKey)
	}
	if stored.PersonalKeysAllowed != next.PersonalKeysAllowed {
		changed = append(changed, OrgAIPropPersonalKeysAllowed)
	}
	if providerChanged || keyWritten || destinationChanged {
		next.Version = stored.Version + 1
	}
	if providerChanged {
		next.ConsentSince = next.Version
	}
	return next, changed
}

// OrgAIWriteStatus is the outcome of WriteOrgAIConfig.
type OrgAIWriteStatus int

const (
	// OrgAIStored: the change was stored, the proof spent and recorded.
	OrgAIStored OrgAIWriteStatus = iota
	// OrgAIConflict: the stored version is not the one named; nothing was
	// stored, the grant is not consumed, the break-glass session not used.
	OrgAIConflict
	// OrgAIStepupRequired: no live llm_org_key_write grant and not a
	// break-glass address.
	OrgAIStepupRequired
	// OrgAIFreshLoginRequired: a break-glass address whose session is stale
	// or already made one organisation-AI change (spec P2-D13).
	OrgAIFreshLoginRequired
)

// OrgAIWriteHooks let a test observe or fail the write transaction at fixed
// points (P2-AC-2's concurrent case, P2-AC-5). Nil in production.
type OrgAIWriteHooks struct {
	// BeforeAuthorize runs in the transaction before the grant is consumed
	// or the break-glass session claimed.
	BeforeAuthorize func()
	// AfterAuthorize runs after a successful authorisation, before the
	// conditional write; a non-nil error fails the write (and rolls back).
	AfterAuthorize func() error
}

// OrgAIWrite is one patchOrganizationAI change after its 400s.
type OrgAIWrite struct {
	UserID, OrgID   uuid.UUID
	ExpectedVersion int64
	Patch           OrgAIPatch
	// BreakGlass: the caller's address is on the deployment's break-glass
	// list; SessionFresh: its session was issued under 10 minutes ago (and
	// not more than a minute in the future); SessionIAT: that issue time.
	BreakGlass   bool
	SessionFresh bool
	SessionIAT   time.Time
	Now          time.Time
	Hooks        *OrgAIWriteHooks
}

// OrgAIWriteOutcome is what WriteOrgAIConfig did. Config is the stored
// configuration after the change (OrgAIStored) or the current one
// (OrgAIConflict).
type OrgAIWriteOutcome struct {
	Status  OrgAIWriteStatus
	Config  OrgAIConfig
	Via     string // "stepup" | "breakglass" when stored
	Changed []string
}

// orgAIWrittenDetails is the operator record's details. PAC-87 (P87-D7,
// contract §5): consent_period_started is true exactly when the change
// altered the provider (members are asked again); consent_since_version is
// the consent period's start after the change. Never a value or a key.
type orgAIWrittenDetails struct {
	OrgID                uuid.UUID `json:"org_id"`
	Via                  string    `json:"via"`
	Changed              []string  `json:"changed"`
	ConfigurationVersion int64     `json:"configuration_version"`
	ConsentPeriodStarted bool      `json:"consent_period_started"`
	ConsentSinceVersion  int64     `json:"consent_since_version"`
	SessionIAT           *int64    `json:"session_iat,omitempty"`
}

// WriteOrgAIConfig is patchOrganizationAI steps 3 and 4 in ONE transaction
// (contract revision 3.1): the authorisation (consume the caller's newest
// live llm_org_key_write grant, else claim the fresh break-glass session
// once), then one conditional write that stores the change only if the
// stored version is still ExpectedVersion (no row counts as version 0), and
// the operator record. Any failure, a conflict included, rolls everything
// back: no grant is consumed and no session marked used unless the change
// is stored.
func WriteOrgAIConfig(ctx context.Context, db *sql.DB, w OrgAIWrite) (OrgAIWriteOutcome, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return OrgAIWriteOutcome{}, err
	}
	defer func() { _ = tx.Rollback() }()
	hooks := w.Hooks
	if hooks == nil {
		hooks = &OrgAIWriteHooks{}
	}

	// Step 3: authorisation.
	if hooks.BeforeAuthorize != nil {
		hooks.BeforeAuthorize()
	}
	via := ""
	var claim uuid.UUID
	_, granted, err := consumeGrant(ctx, tx, w.UserID, w.OrgID, SSOGrantPurposeLLMOrgKeyWrite, w.Now)
	if err != nil {
		return OrgAIWriteOutcome{}, err
	}
	switch {
	case granted:
		via = "stepup"
	case w.BreakGlass && w.SessionFresh:
		iat := w.SessionIAT.Unix()
		raw, err := json.Marshal(orgAIWrittenDetails{OrgID: w.OrgID, Via: "breakglass", Changed: []string{}, SessionIAT: &iat})
		if err != nil {
			return OrgAIWriteOutcome{}, err
		}
		// The partial unique index makes this the atomic "not used yet"
		// check: a second change with the same session inserts nothing.
		err = tx.QueryRowContext(ctx, `
			INSERT INTO sso_events (user_id, org_id, kind, details) VALUES ($1, $2, $3, $4)
			ON CONFLICT (user_id, (details->>'session_iat'))
				WHERE kind = 'llm_org_ai_written' AND details->>'via' = 'breakglass'
			DO NOTHING
			RETURNING id`, w.UserID, w.OrgID, SSOEventLLMOrgAIWritten, string(raw)).Scan(&claim)
		if errors.Is(err, sql.ErrNoRows) {
			return OrgAIWriteOutcome{Status: OrgAIFreshLoginRequired}, nil
		}
		if err != nil {
			return OrgAIWriteOutcome{}, err
		}
		via = "breakglass"
	case w.BreakGlass:
		return OrgAIWriteOutcome{Status: OrgAIFreshLoginRequired}, nil
	default:
		return OrgAIWriteOutcome{Status: OrgAIStepupRequired}, nil
	}
	if hooks.AfterAuthorize != nil {
		if err := hooks.AfterAuthorize(); err != nil {
			return OrgAIWriteOutcome{}, err
		}
	}

	// Step 4: the conditional write. The row is created at version 0 when
	// missing (rolled back with everything else on a conflict), then locked,
	// so the version compared is the latest committed one.
	if _, err := tx.ExecContext(ctx, `INSERT INTO org_ai_configurations (org_id) VALUES ($1) ON CONFLICT (org_id) DO NOTHING`, w.OrgID); err != nil {
		return OrgAIWriteOutcome{}, err
	}
	stored, err := getOrgAIConfig(ctx, tx, w.OrgID, " FOR UPDATE")
	if err != nil {
		return OrgAIWriteOutcome{}, err
	}
	if stored.Version != w.ExpectedVersion {
		return OrgAIWriteOutcome{Status: OrgAIConflict, Config: stored}, nil
	}
	next, changed := w.Patch.Merge(stored)
	now := w.Now.UTC()
	next.UpdatedAt = &now
	// PAC-84: a kept key is written back byte-identical (K-10); a new one
	// is sealed for this organisation.
	storedKey := next.rawKey
	if next.keyErr == nil && (next.rawKey == "" || next.APIKey != stored.APIKey) {
		if storedKey, err = secretbox.Seal(bindOrgAI(w.OrgID), next.APIKey); err != nil {
			return OrgAIWriteOutcome{}, err
		}
		next.rawKey = storedKey
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE org_ai_configurations SET provider = $2, model = $3, base_url = $4,
			azure_endpoint = $5, azure_deployment = $6, azure_api_version = $7, aws_region = $8,
			api_key = $9, personal_keys_allowed = $10, configuration_version = $11,
			updated_at = $12, updated_by = $13, consent_since_version = $15
		WHERE org_id = $1 AND configuration_version = $14`,
		w.OrgID, next.Provider, next.Model, next.BaseURL, next.AzureEndpoint, next.AzureDeployment,
		next.AzureAPIVersion, next.AWSRegion, storedKey, next.PersonalKeysAllowed, next.Version,
		now, w.UserID, w.ExpectedVersion, next.ConsentSince); err != nil {
		return OrgAIWriteOutcome{}, err
	}

	// The operator record, in the same transaction (contract revision 3.1,
	// NB-B). Never a value, never the key.
	details := orgAIWrittenDetails{OrgID: w.OrgID, Via: via, Changed: changed, ConfigurationVersion: next.Version,
		ConsentPeriodStarted: next.ConsentSince != stored.ConsentSince, ConsentSinceVersion: next.ConsentSince}
	if via == "breakglass" {
		iat := w.SessionIAT.Unix()
		details.SessionIAT = &iat
		raw, err := json.Marshal(details)
		if err != nil {
			return OrgAIWriteOutcome{}, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE sso_events SET details = $2 WHERE id = $1`, claim, string(raw)); err != nil {
			return OrgAIWriteOutcome{}, err
		}
	} else if err := insertSSOEvent(ctx, tx, w.UserID, &w.OrgID, SSOEventLLMOrgAIWritten, details); err != nil {
		return OrgAIWriteOutcome{}, err
	}
	if err := tx.Commit(); err != nil {
		return OrgAIWriteOutcome{}, err
	}
	return OrgAIWriteOutcome{Status: OrgAIStored, Config: next, Via: via, Changed: changed}, nil
}

// consumeGrant is ConsumeSSOStepupGrant on a transaction or a database.
func consumeGrant(ctx context.Context, q rowQuerier, userID, orgID uuid.UUID, purpose string, now time.Time) (uuid.UUID, bool, error) {
	var id uuid.UUID
	err := q.QueryRowContext(ctx, `
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

// OrgAIConfirmStatus is the outcome of ConfirmOrgAI.
type OrgAIConfirmStatus int

const (
	// OrgAIConfirmed: the confirmation of the current consent period is
	// recorded.
	OrgAIConfirmed OrgAIConfirmStatus = iota
	// OrgAIConfirmNotConfigured: the configuration is not complete; nothing
	// recorded (wins over a stale version).
	OrgAIConfirmNotConfigured
	// OrgAIConfirmChanged: the version shown is not in the current consent
	// period (a provider change since, or a version above the current one);
	// nothing recorded.
	OrgAIConfirmChanged
	// OrgAIConfirmNotMember: the caller no longer belongs to the organisation
	// named (they left it after their membership was checked); nothing
	// recorded.
	OrgAIConfirmNotMember
)

// lockUserRow takes the caller's users row FOR NO KEY UPDATE and returns
// their organisation (PAC-87 contract §5a): the lock that orders a
// confirmation against the same user's settings write and organisation
// change. FOR NO KEY UPDATE, not FOR UPDATE, so an administrator's
// sso_events insert (FOR KEY SHARE on their own users row) never waits on it.
func lockUserRow(ctx context.Context, tx *sql.Tx, userID uuid.UUID) (uuid.NullUUID, error) {
	var org uuid.NullUUID
	err := tx.QueryRowContext(ctx, `SELECT org_id FROM users WHERE id = $1 FOR NO KEY UPDATE`, userID).Scan(&org)
	if errors.Is(err, sql.ErrNoRows) {
		return uuid.NullUUID{}, nil
	}
	return org, err
}

// ConfirmOrgAI records the user's confirmation for their organisation of the
// version they were shown (confirmOrganizationAI, PAC-87 contract §1): it is
// accepted exactly when the configuration is complete and the version lies
// in the current consent period (consent start <= version <= current
// version), and what is stored is the consent period, never the version. One
// transaction: the caller's users row first (ordered against their own
// settings write and organisation change, §5a), then the configuration row
// FOR SHARE (an administrator's write holds it FOR UPDATE), so a provider
// change can never land between the check and the record. It returns the
// current configuration.
func ConfirmOrgAI(ctx context.Context, db *sql.DB, userID, orgID uuid.UUID, version int64, now time.Time) (OrgAIConfirmStatus, OrgAIConfig, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, OrgAIConfig{}, err
	}
	defer func() { _ = tx.Rollback() }()
	member, err := lockUserRow(ctx, tx, userID)
	if err != nil {
		return 0, OrgAIConfig{}, err
	}
	if !member.Valid || member.UUID != orgID {
		return OrgAIConfirmNotMember, OrgAIConfig{}, nil
	}
	cfg, err := getOrgAIConfig(ctx, tx, orgID, " FOR SHARE")
	if err != nil {
		return 0, OrgAIConfig{}, err
	}
	switch {
	case !cfg.Complete():
		return OrgAIConfirmNotConfigured, cfg, nil
	case version < cfg.ConsentSince || version > cfg.Version:
		return OrgAIConfirmChanged, cfg, nil
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO org_ai_confirmations (user_id, org_id, consent_since_version, confirmed_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_id, org_id) DO UPDATE
			SET consent_since_version = EXCLUDED.consent_since_version, confirmed_at = EXCLUDED.confirmed_at`,
		userID, orgID, cfg.ConsentSince, now.UTC()); err != nil {
		return 0, OrgAIConfig{}, err
	}
	if err := tx.Commit(); err != nil {
		return 0, OrgAIConfig{}, err
	}
	return OrgAIConfirmed, cfg, nil
}

// SettingsWriteStep names a storage access of SaveSettingsEndingOrgAIConfirmation,
// for tests that apply a concurrent change at each one (PAC-87 AC-23).
type SettingsWriteStep int

// The storage accesses of a settings write, in order.
const (
	SettingsWriteLock SettingsWriteStep = iota
	SettingsWriteReread
	SettingsWriteSave
	SettingsWriteEndConfirmation
	SettingsWriteCommit
	SettingsWriteDone
)

// SaveSettingsEndingOrgAIConfirmation saves s as userID's settings and ends
// the user's organisation-AI confirmation when, and only when, the save
// CHANGES the stored choice to "off" or to a different provider (PAC-87
// §3.4, P87-D3; contract §5a). One transaction: the caller's users row FOR
// NO KEY UPDATE (the lock ConfirmOrgAI takes first), the stored choice
// re-read inside it (never the copy the handler read before), the save, the
// end of the confirmation, commit. A concurrent confirmation of the same
// user therefore lands wholly before (and is ended by the change) or wholly
// after (an explicit confirmation after the change). before, when not nil,
// runs before each storage access (tests only). ended reports whether a
// confirmation was ended.
func SaveSettingsEndingOrgAIConfirmation(ctx context.Context, db *sql.DB, userID uuid.UUID, s *Settings, before func(SettingsWriteStep)) (ended bool, err error) {
	at := func(step SettingsWriteStep) {
		if before != nil {
			before(step)
		}
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	at(SettingsWriteLock)
	if _, err := lockUserRow(ctx, tx, userID); err != nil {
		return false, err
	}
	at(SettingsWriteReread)
	var stored string
	err = tx.QueryRowContext(ctx, `SELECT llm_provider FROM settings WHERE user_id = $1`, userID).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		stored = LLMChoiceOrganization // no row follows by default (029)
	} else if err != nil {
		return false, err
	}
	next := s.storedProvider()
	at(SettingsWriteSave)
	if err := saveSettingsAt(tx, userID, s, time.Now()); err != nil {
		return false, err
	}
	if next != stored && next != LLMChoiceOrganization {
		at(SettingsWriteEndConfirmation)
		res, err := tx.ExecContext(ctx, `DELETE FROM org_ai_confirmations WHERE user_id = $1`, userID)
		if err != nil {
			return false, err
		}
		n, _ := res.RowsAffected()
		ended = n > 0
	}
	at(SettingsWriteCommit)
	if err := tx.Commit(); err != nil {
		return false, err
	}
	at(SettingsWriteDone)
	return ended, nil
}
