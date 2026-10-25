-- 026_sso_admin_stepup.up.sql — PAC-50 (spec docs/specs/PAC-50.md §4.8): SSO
-- writes are authorized by a short-lived admin step-up grant, every SSO write
-- and step-up outcome is recorded for operators, provider domains are unique
-- case-insensitively, and an org can be pinned to one Microsoft tenant.
--
-- Numbered 026, the next free version on main when this was written: the spec
-- reserved 025/026 for PAC-23/PAC-49, but PAC-55 has since taken 025 and
-- neither of those has landed (see the spec's Decisions, D-7).

-- 1. Grants: one row per granted step-up, good for one write within
--    expires_at. Consumed by a single UPDATE ... RETURNING (§4.2 step 5a).
CREATE TABLE sso_stepup_grants (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    org_id      UUID        NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    provider    TEXT        NOT NULL CHECK (provider IN ('google', 'microsoft')),
    issued_at   TIMESTAMPTZ NOT NULL,
    expires_at  TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ NULL,
    CHECK (expires_at > issued_at)
);
-- Serves "newest live grant for (user, org)".
CREATE INDEX idx_sso_stepup_grants_live
    ON sso_stepup_grants (user_id, org_id, issued_at DESC)
    WHERE consumed_at IS NULL;

-- 2. The SSO event table (§4.6). Operators only: no API reads it. org_id is
--    NULL only for a step-up refused before any org was known (an orgless
--    caller's state_mismatch). details is JSON, never concatenated text.
CREATE TABLE sso_events (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    org_id     UUID        NULL REFERENCES organizations(id) ON DELETE CASCADE,
    kind       TEXT        NOT NULL CHECK (kind IN ('sso_stepup', 'sso_provider_written')),
    details    JSONB       NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_sso_events_org_created ON sso_events (org_id, created_at);
CREATE INDEX idx_sso_events_user_created ON sso_events (user_id, created_at);

-- 3. Provider domains: lower-case what exists, then make uniqueness
--    case-insensitive. Two rows that differ only by case make the UPDATE fail
--    on the existing UNIQUE(domain): the migration stops rather than choosing
--    which row to keep (no user data is deleted here).
UPDATE sso_providers SET domain = lower(domain) WHERE domain <> lower(domain);
ALTER TABLE sso_providers DROP CONSTRAINT sso_providers_domain_key;
DROP INDEX IF EXISTS idx_sso_providers_domain;
CREATE UNIQUE INDEX sso_providers_domain_lower_key ON sso_providers (lower(domain));

-- 4. The Microsoft tenant pin (§4.3). NULL until the first successful
--    Microsoft step-up for the org.
ALTER TABLE organizations ADD COLUMN ms_tenant_id TEXT NULL;
