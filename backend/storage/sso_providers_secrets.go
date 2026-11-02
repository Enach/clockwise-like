package storage

import (
	"context"

	"github.com/Enach/paceday/backend/internal/secretbox"
	"github.com/google/uuid"
)

// PAC-84: the SSO client secret (S10) and SAML certificate (S11) are bound
// to the row id, never the domain (contract 3.3: the domain text is
// rewritten in place). A read opens each on its own: a failure leaves the
// field "", sets SecretErr, and never fails the read (K-2: the sign-in
// handlers map any read error to 404).

// HasClientSecret is presence: the stored value is non-empty (D-3, AC-16b).
func (p *SSOProvider) HasClientSecret() bool {
	return p.OIDCClientSecret != "" || p.rawClientSecret != ""
}

// HasSAMLCert is presence of the stored certificate.
func (p *SSOProvider) HasSAMLCert() bool { return p.SAMLCert != "" || p.rawSAMLCert != "" }

// ClientSecretErr is the decryption failure of the client secret, if any.
func (p *SSOProvider) ClientSecretErr() error { return p.clientSecretErr }

func (p *SSOProvider) openSecrets() {
	var err error
	p.rawClientSecret, p.rawSAMLCert = p.OIDCClientSecret, p.SAMLCert
	if p.OIDCClientSecret, err = openSecret(bindSSO("oidc_client_secret", p.ID), p.rawClientSecret, uuid.Nil); err != nil {
		p.clientSecretErr = err
		p.SecretErr = err
	}
	if p.SAMLCert, err = openSecret(bindSSO("saml_cert", p.ID), p.rawSAMLCert, uuid.Nil); err != nil && p.SecretErr == nil {
		p.SecretErr = err
	}
}

// writeSSOSecrets seals the client secret and certificate for row p.ID and
// writes them; it runs in the transaction of the row statement (contract
// 3.3). keepRaw, when not nil, is the stored client-secret text to keep
// byte-identical (a write that left the secret out, K-10).
func writeSSOSecrets(ctx context.Context, q queryRower, p *SSOProvider, secret, cert string, keepRaw *string) error {
	var sealedSecret string
	var err error
	if keepRaw != nil {
		sealedSecret = *keepRaw
	} else if sealedSecret, err = secretbox.Seal(bindSSO("oidc_client_secret", p.ID), secret); err != nil {
		return err
	}
	sealedCert, err := secretbox.Seal(bindSSO("saml_cert", p.ID), cert)
	if err != nil {
		return err
	}
	if err := q.QueryRowContext(ctx, `UPDATE sso_providers SET oidc_client_secret = $2, saml_cert = $3 WHERE id = $1 RETURNING id`,
		p.ID, sealedSecret, sealedCert).Scan(&p.ID); err != nil {
		return err
	}
	p.OIDCClientSecret, p.SAMLCert = secret, cert
	p.rawClientSecret, p.rawSAMLCert = sealedSecret, sealedCert
	if keepRaw == nil {
		p.clientSecretErr, p.SecretErr = nil, nil
	}
	return nil
}
