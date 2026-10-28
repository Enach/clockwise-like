-- 031_llm_org_ai.up.sql — PAC-73 Part 2 (spec docs/specs/NEW-llm-bring-your-own-part2.md
-- §7; contract revision 3.1): an organisation-wide AI configuration, each
-- member's confirmation of one version of it, and the operator record of
-- every organisation-AI change.
--
-- Numbered 031, the next free version on main (030_sso_admin_stepup is the
-- latest). No existing row is rewritten: settings rows, including every
-- stored "follow my organisation" choice, are untouched, and PAC-50's grant
-- table needs no change (its purpose column takes 'llm_org_key_write'
-- without a migration, 030_sso_admin_stepup.up.sql:13-25).

-- (a) One AI configuration per organisation. The row is never deleted by a
--     removal: provider '' empties provider, model, destination and key and
--     keeps configuration_version and personal_keys_allowed, so a version is
--     never reused within an organisation (spec P2-D17). It goes only with
--     the organisation. updated_at is NULL until the first change.
CREATE TABLE org_ai_configurations (
    org_id                UUID        PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    provider              TEXT        NOT NULL DEFAULT ''
        CHECK (provider IN ('', 'openai', 'anthropic', 'azure_openai', 'bedrock', 'vertex')),
    model                 TEXT        NOT NULL DEFAULT '',
    base_url              TEXT        NOT NULL DEFAULT '',
    azure_endpoint        TEXT        NOT NULL DEFAULT '',
    azure_deployment      TEXT        NOT NULL DEFAULT '',
    azure_api_version     TEXT        NOT NULL DEFAULT '',
    aws_region            TEXT        NOT NULL DEFAULT '',
    api_key               TEXT        NOT NULL DEFAULT '',
    personal_keys_allowed BOOLEAN     NOT NULL DEFAULT TRUE,
    configuration_version BIGINT      NOT NULL DEFAULT 0 CHECK (configuration_version >= 0),
    updated_at            TIMESTAMPTZ NULL,
    updated_by            UUID        NULL REFERENCES users(id) ON DELETE SET NULL
);

-- (b) One confirmation per (user, organisation): the version the member
--     confirmed (owner O-13). Recorded only by confirmOrganizationAI;
--     deleted by a settings write that stores "off" or a provider.
CREATE TABLE org_ai_confirmations (
    user_id               UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    org_id                UUID        NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    configuration_version BIGINT      NOT NULL CHECK (configuration_version >= 1),
    confirmed_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, org_id)
);

-- (c) The operator record of an organisation-AI change is an sso_events row
--     of a new kind. A break-glass change's record carries the session's
--     issue time (details.session_iat, Unix seconds), and at most one such
--     record exists per (user, session issue time): the insert, in the same
--     transaction as the conditional configuration write, is the atomic
--     "this session has not been used" check (spec P2-D13).
ALTER TABLE sso_events DROP CONSTRAINT sso_events_kind_check;
ALTER TABLE sso_events ADD CONSTRAINT sso_events_kind_check
    CHECK (kind IN ('sso_stepup', 'sso_provider_written', 'llm_org_ai_written'));
CREATE UNIQUE INDEX sso_events_llm_org_ai_breakglass_once
    ON sso_events (user_id, (details->>'session_iat'))
    WHERE kind = 'llm_org_ai_written' AND details->>'via' = 'breakglass';
