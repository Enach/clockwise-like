package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// PAC-73 Part 2 (spec docs/specs/NEW-llm-bring-your-own-part2.md §3.2, §3.4,
// §7; contract revision 3.1): the organisation's AI configuration, each
// member's confirmation of one version of it, and the operator record of
// every change (migration 031_llm_org_ai).

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
	UpdatedAt           *time.Time
}

// defaultOrgAIConfig is what an organisation with no stored row reads as.
func defaultOrgAIConfig() OrgAIConfig { return OrgAIConfig{PersonalKeysAllowed: true} }

// Complete reports whether the configuration can be used and confirmed: a
// provider, a key, and for azure_openai an endpoint and a deployment (spec
// §3.1 "Complete"; contract OrganizationAIConfiguration.configured).
func (c OrgAIConfig) Complete() bool {
	if c.Provider == "" || c.APIKey == "" {
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
	OrgID            *uuid.UUID
	Config           OrgAIConfig
	ConfirmedVersion int64 // 0: no confirmation
}

// InOrg reports whether the user belongs to an organisation.
func (s OrgAIState) InOrg() bool { return s.OrgID != nil }

// Forbids reports whether the user's organisation forbids personal keys.
func (s OrgAIState) Forbids() bool { return s.InOrg() && !s.Config.PersonalKeysAllowed }

// Confirmed reports whether the user's stored confirmation names exactly the
// version this state read (spec §3.1 "Confirmed").
func (s OrgAIState) Confirmed() bool {
	return s.InOrg() && s.ConfirmedVersion != 0 && s.ConfirmedVersion == s.Config.Version
}

const orgAIColumns = `c.provider, c.model, c.base_url, c.azure_endpoint, c.azure_deployment,
	c.azure_api_version, c.aws_region, c.api_key, c.personal_keys_allowed,
	c.configuration_version, c.updated_at`

// orgAIScan holds the nullable columns of a LEFT JOIN on the configuration.
type orgAIScan struct {
	provider, model, baseURL, azureEndpoint, azureDeployment sql.NullString
	azureAPIVersion, awsRegion, apiKey                        sql.NullString
	personalKeysAllowed                                       sql.NullBool
	version                                                   sql.NullInt64
	updatedAt                                                 sql.NullTime
}

func (s *orgAIScan) dest() []any {
	return []any{&s.provider, &s.model, &s.baseURL, &s.azureEndpoint, &s.azureDeployment,
		&s.azureAPIVersion, &s.awsRegion, &s.apiKey, &s.personalKeysAllowed, &s.version, &s.updatedAt}
}

func (s *orgAIScan) config() OrgAIConfig {
	if !s.version.Valid {
		return defaultOrgAIConfig()
	}
	c := OrgAIConfig{
		Provider: s.provider.String, Model: s.model.String, BaseURL: s.baseURL.String,
		AzureEndpoint: s.azureEndpoint.String, AzureDeployment: s.azureDeployment.String,
		AzureAPIVersion: s.azureAPIVersion.String, AWSRegion: s.awsRegion.String,
		APIKey: s.apiKey.String, PersonalKeysAllowed: s.personalKeysAllowed.Bool, Version: s.version.Int64,
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
		SELECT u.org_id, `+orgAIColumns+`, f.configuration_version
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
	st.Config = sc.config()
	st.ConfirmedVersion = confirmed.Int64
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
	return sc.config(), nil
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
//     reset, so a version is never reused (spec P2-D4, P2-D17).
//
// changed lists the contract names of the properties whose value changed
// (apiKey whenever present), for the operator record; never a value.
func (p OrgAIPatch) Merge(stored OrgAIConfig) (next OrgAIConfig, changed []string) {
	next = stored
	changed = make([]string, 0, 9)
	providerChanged := p.Provider != nil && *p.Provider != stored.Provider
	if providerChanged {
		next = OrgAIConfig{Provider: *p.Provider, PersonalKeysAllowed: stored.PersonalKeysAllowed,
			Version: stored.Version, UpdatedAt: stored.UpdatedAt}
	}
	set := func(dst *string, v *string) {
		if v != nil {
			*dst = *v
		}
	}
	set(&next.Model, p.Model)
	set(&next.APIKey, p.APIKey)
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
	keyWritten := p.APIKey != nil || (providerChanged && stored.APIKey != "")
	if keyWritten {
		changed = append(changed, OrgAIPropAPIKey)
	}
	if stored.PersonalKeysAllowed != next.PersonalKeysAllowed {
		changed = append(changed, OrgAIPropPersonalKeysAllowed)
	}
	if providerChanged || keyWritten || destinationChanged {
		next.Version = stored.Version + 1
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

type orgAIWrittenDetails struct {
	OrgID                uuid.UUID `json:"org_id"`
	Via                  string    `json:"via"`
	Changed              []string  `json:"changed"`
	ConfigurationVersion int64     `json:"configuration_version"`
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
	if _, err := tx.ExecContext(ctx, `
		UPDATE org_ai_configurations SET provider = $2, model = $3, base_url = $4,
			azure_endpoint = $5, azure_deployment = $6, azure_api_version = $7, aws_region = $8,
			api_key = $9, personal_keys_allowed = $10, configuration_version = $11,
			updated_at = $12, updated_by = $13
		WHERE org_id = $1 AND configuration_version = $14`,
		w.OrgID, next.Provider, next.Model, next.BaseURL, next.AzureEndpoint, next.AzureDeployment,
		next.AzureAPIVersion, next.AWSRegion, next.APIKey, next.PersonalKeysAllowed, next.Version,
		now, w.UserID, w.ExpectedVersion); err != nil {
		return OrgAIWriteOutcome{}, err
	}

	// The operator record, in the same transaction (contract revision 3.1,
	// NB-B). Never a value, never the key.
	details := orgAIWrittenDetails{OrgID: w.OrgID, Via: via, Changed: changed, ConfigurationVersion: next.Version}
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
	// OrgAIConfirmed: the confirmation for the version is recorded.
	OrgAIConfirmed OrgAIConfirmStatus = iota
	// OrgAIConfirmNotConfigured: the configuration is not complete; nothing
	// recorded (wins over a stale version).
	OrgAIConfirmNotConfigured
	// OrgAIConfirmChanged: the version is not the current one; nothing
	// recorded.
	OrgAIConfirmChanged
)

// ConfirmOrgAI records the user's confirmation of version for their
// organisation, only when that version is the current one of a complete
// configuration (confirmOrganizationAI). The check and the record are one
// transaction holding the configuration row, so an administrator's change
// cannot land between them. It returns the current configuration.
func ConfirmOrgAI(ctx context.Context, db *sql.DB, userID, orgID uuid.UUID, version int64, now time.Time) (OrgAIConfirmStatus, OrgAIConfig, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, OrgAIConfig{}, err
	}
	defer func() { _ = tx.Rollback() }()
	cfg, err := getOrgAIConfig(ctx, tx, orgID, " FOR SHARE")
	if err != nil {
		return 0, OrgAIConfig{}, err
	}
	switch {
	case !cfg.Complete():
		return OrgAIConfirmNotConfigured, cfg, nil
	case cfg.Version != version:
		return OrgAIConfirmChanged, cfg, nil
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO org_ai_confirmations (user_id, org_id, configuration_version, confirmed_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_id, org_id) DO UPDATE
			SET configuration_version = EXCLUDED.configuration_version, confirmed_at = EXCLUDED.confirmed_at`,
		userID, orgID, version, now.UTC()); err != nil {
		return 0, OrgAIConfig{}, err
	}
	if err := tx.Commit(); err != nil {
		return 0, OrgAIConfig{}, err
	}
	return OrgAIConfirmed, cfg, nil
}

// DeleteOrgAIConfirmations ends every stored confirmation of the user (a
// settings write that stores "off" or one of the five providers, spec §3.4,
// P2-AC-36).
func DeleteOrgAIConfirmations(ctx context.Context, db *sql.DB, userID uuid.UUID) error {
	_, err := db.ExecContext(ctx, `DELETE FROM org_ai_confirmations WHERE user_id = $1`, userID)
	return err
}
