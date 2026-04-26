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
