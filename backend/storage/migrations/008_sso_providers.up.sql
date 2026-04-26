CREATE TABLE sso_providers (
  id                 UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  domain             TEXT        NOT NULL UNIQUE,
  provider_name      TEXT        NOT NULL,
  provider_type      TEXT        NOT NULL CHECK (provider_type IN ('oidc', 'saml')),
  enabled            BOOLEAN     NOT NULL DEFAULT true,
  oidc_issuer        TEXT        NOT NULL DEFAULT '',
  oidc_client_id     TEXT        NOT NULL DEFAULT '',
  oidc_client_secret TEXT        NOT NULL DEFAULT '',
  saml_entry_point   TEXT        NOT NULL DEFAULT '',
  saml_issuer        TEXT        NOT NULL DEFAULT '',
  saml_cert          TEXT        NOT NULL DEFAULT '',
  created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_sso_providers_domain ON sso_providers(domain);
