-- 028_credential_losses.up.sql — PAC-67: one row per (owner, provider) while a
-- stored credential is recorded as LOST: its renewal was permanently refused
-- by the provider (spec PAC-67 AC-8, D-1). The credential itself stays where it
-- is (oauth_tokens, settings.microsoft_tokens, settings.zoom_tokens); this
-- table only marks it unusable. The primary key makes "one loss, one
-- connection_lost entry" hold even when several requests or processes hit the
-- same refusal (AC-9, AC-25). A reconnect or a disconnect deletes the row.
CREATE TABLE IF NOT EXISTS credential_losses (
    user_id  UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider TEXT        NOT NULL CHECK (provider IN ('google', 'outlook', 'zoom')),
    lost_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, provider)
);
