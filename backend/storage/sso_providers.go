package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
)

type SSOProvider struct {
	ID               uuid.UUID
	Domain           string
	ProviderName     string
	ProviderType     string // "oidc" | "saml"
	Enabled          bool
	OIDCIssuer       string
	OIDCClientID     string
	OIDCClientSecret string
	SAMLEntryPoint   string
	SAMLIssuer       string
	SAMLCert         string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

const ssoProviderCols = `id, domain, provider_name, provider_type, enabled,
	oidc_issuer, oidc_client_id, oidc_client_secret,
	saml_entry_point, saml_issuer, saml_cert,
	created_at, updated_at`

func scanSSOProvider(s interface{ Scan(...any) error }) (*SSOProvider, error) {
	var p SSOProvider
	err := s.Scan(
		&p.ID, &p.Domain, &p.ProviderName, &p.ProviderType, &p.Enabled,
		&p.OIDCIssuer, &p.OIDCClientID, &p.OIDCClientSecret,
		&p.SAMLEntryPoint, &p.SAMLIssuer, &p.SAMLCert,
		&p.CreatedAt, &p.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &p, err
}

func GetSSOProviderByDomain(db *sql.DB, domain string) (*SSOProvider, error) {
	row := db.QueryRowContext(context.Background(), `
		SELECT `+ssoProviderCols+`
		FROM sso_providers
		WHERE domain = $1 AND enabled = true
	`, domain)
	return scanSSOProvider(row)
}

// GetSSOProviderByDomainAnyState returns the configuration stored under
// exactly this domain, active or suspended (PAC-22).
func GetSSOProviderByDomainAnyState(db *sql.DB, domain string) (*SSOProvider, error) {
	row := db.QueryRowContext(context.Background(), `
		SELECT `+ssoProviderCols+`
		FROM sso_providers
		WHERE domain = $1
	`, domain)
	return scanSSOProvider(row)
}

func UpsertSSOProvider(db *sql.DB, p *SSOProvider) (*SSOProvider, error) {
	row := db.QueryRowContext(context.Background(), `
		INSERT INTO sso_providers
			(domain, provider_name, provider_type, enabled,
			 oidc_issuer, oidc_client_id, oidc_client_secret,
			 saml_entry_point, saml_issuer, saml_cert)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (domain) DO UPDATE SET
			provider_name      = EXCLUDED.provider_name,
			provider_type      = EXCLUDED.provider_type,
			enabled            = EXCLUDED.enabled,
			oidc_issuer        = EXCLUDED.oidc_issuer,
			oidc_client_id     = EXCLUDED.oidc_client_id,
			oidc_client_secret = EXCLUDED.oidc_client_secret,
			saml_entry_point   = EXCLUDED.saml_entry_point,
			saml_issuer        = EXCLUDED.saml_issuer,
			saml_cert          = EXCLUDED.saml_cert,
			updated_at         = NOW()
		RETURNING `+ssoProviderCols,
		p.Domain, p.ProviderName, p.ProviderType, p.Enabled,
		p.OIDCIssuer, p.OIDCClientID, p.OIDCClientSecret,
		p.SAMLEntryPoint, p.SAMLIssuer, p.SAMLCert,
	)
	return scanSSOProvider(row)
}

// ErrSSOClientSecretRequired: a write left the client secret out and there is
// no secret to keep (no configuration under the domain, a configuration that
// is not oidc, or one whose stored secret is empty). PAC-22 spec AC-25.
var ErrSSOClientSecretRequired = errors.New("oidc_client_secret is required")

// ErrSSOClientSecretTargetChanged: a write left the client secret out while
// changing the issuer or the client id the secret is sent with. PAC-22 spec
// D-13, AC-24.
var ErrSSOClientSecretTargetChanged = errors.New("oidc_client_secret is required when oidc_issuer or oidc_client_id changes")

// ErrSSOClientSecretEmpty: a write supplied an empty client secret. No write
// can clear the secret (PAC-22 spec AC-26); the handler refuses "" and null
// first, and storage refuses it again so no caller can store one.
var ErrSSOClientSecretEmpty = errors.New("oidc_client_secret must not be empty")

// KeepableClientSecret decides whether a write that leaves the client secret
// out may keep the one stored on current (nil when nothing is stored). It
// returns nil, ErrSSOClientSecretRequired or ErrSSOClientSecretTargetChanged.
// The comparison is byte for byte: a change of case or a trailing slash is a
// change (PAC-22 spec D-13).
func KeepableClientSecret(current *SSOProvider, issuer, clientID string) error {
	if current == nil || current.ProviderType != "oidc" || current.OIDCClientSecret == "" {
		return ErrSSOClientSecretRequired
	}
	if current.OIDCIssuer != issuer || current.OIDCClientID != clientID {
		return ErrSSOClientSecretTargetChanged
	}
	return nil
}

// OIDCProviderWrite is a createSsoProvider write (PAC-22). ClientSecret nil
// means the client secret was left out and the stored one is to be kept.
type OIDCProviderWrite struct {
	Domain       string
	ProviderName string
	Issuer       string
	ClientID     string
	ClientSecret *string
}

// SaveOIDCProvider stores an OIDC configuration under w.Domain exactly as
// given. A create starts enabled; a replace never writes enabled, so it keeps
// the active state stored at the moment it is stored, and it stores the SAML
// values as absent. When w.ClientSecret is nil, whether the stored secret may
// be kept is decided again here, inside the transaction that stores the write,
// against the row as it is then (PAC-22 spec D-14, AC-27, AC-28): if it may
// not, nothing is stored and KeepableClientSecret's error is returned. A
// supplied secret that is empty is refused with ErrSSOClientSecretEmpty and
// nothing is stored (AC-26).
func SaveOIDCProvider(db *sql.DB, w OIDCProviderWrite) (*SSOProvider, error) {
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	current, err := scanSSOProvider(tx.QueryRowContext(ctx, `
		SELECT `+ssoProviderCols+`
		FROM sso_providers
		WHERE domain = $1
		FOR UPDATE
	`, w.Domain))
	if err != nil {
		return nil, err
	}

	var secret string
	if w.ClientSecret == nil {
		if err := KeepableClientSecret(current, w.Issuer, w.ClientID); err != nil {
			return nil, err
		}
		secret = current.OIDCClientSecret
	} else {
		if *w.ClientSecret == "" {
			return nil, ErrSSOClientSecretEmpty
		}
		secret = *w.ClientSecret
	}

	var saved *SSOProvider
	if current != nil {
		saved, err = scanSSOProvider(tx.QueryRowContext(ctx, `
			UPDATE sso_providers SET
				provider_name      = $2,
				provider_type      = 'oidc',
				oidc_issuer        = $3,
				oidc_client_id     = $4,
				oidc_client_secret = $5,
				saml_entry_point   = '',
				saml_issuer        = '',
				saml_cert          = '',
				updated_at         = NOW()
			WHERE id = $1
			RETURNING `+ssoProviderCols,
			current.ID, w.ProviderName, w.Issuer, w.ClientID, secret))
	} else {
		// No row was there to lock. A row inserted concurrently since is
		// replaced as any replace is, and keeps its own active state; this
		// path always carries a supplied secret (a left-out one was refused
		// above), so no empty secret can be stored.
		saved, err = scanSSOProvider(tx.QueryRowContext(ctx, `
			INSERT INTO sso_providers
				(domain, provider_name, provider_type, enabled,
				 oidc_issuer, oidc_client_id, oidc_client_secret,
				 saml_entry_point, saml_issuer, saml_cert)
			VALUES ($1, $2, 'oidc', true, $3, $4, $5, '', '', '')
			ON CONFLICT (domain) DO UPDATE SET
				provider_name      = EXCLUDED.provider_name,
				provider_type      = EXCLUDED.provider_type,
				oidc_issuer        = EXCLUDED.oidc_issuer,
				oidc_client_id     = EXCLUDED.oidc_client_id,
				oidc_client_secret = EXCLUDED.oidc_client_secret,
				saml_entry_point   = '',
				saml_issuer        = '',
				saml_cert          = '',
				updated_at         = NOW()
			RETURNING `+ssoProviderCols,
			w.Domain, w.ProviderName, w.Issuer, w.ClientID, secret))
	}
	if err != nil {
		return nil, err
	}
	if saved == nil {
		return nil, sql.ErrNoRows
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return saved, nil
}

func ListSSOProvidersByOrg(db *sql.DB, orgID uuid.UUID) ([]*SSOProvider, error) {
	rows, err := db.QueryContext(context.Background(), `
		SELECT s.id, s.domain, s.provider_name, s.provider_type, s.enabled,
		       s.oidc_issuer, s.oidc_client_id, s.oidc_client_secret,
		       s.saml_entry_point, s.saml_issuer, s.saml_cert,
		       s.created_at, s.updated_at
		FROM sso_providers s
		JOIN organizations o ON o.domain = s.domain
		WHERE o.id = $1
		ORDER BY s.created_at
	`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*SSOProvider
	for rows.Next() {
		p, err := scanSSOProvider(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func DeleteSSOProvider(db *sql.DB, domain string) error {
	_, err := db.ExecContext(context.Background(),
		`DELETE FROM sso_providers WHERE domain = $1`, domain)
	return err
}
