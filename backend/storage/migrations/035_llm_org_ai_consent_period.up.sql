-- PAC-87 (contracts/features/PAC-87.md §3.1; spec docs/specs/PAC-87.md §3.1,
-- §3.8): a member's confirmation covers the organisation's provider for one
-- consent period, which only a provider change (first setup, change, removal)
-- starts. Text normative in the contract; header comments trimmed only.

-- (a) consent period start, per organisation; at deploy the current
--     configuration starts its period at its current version (spec §3.8).
ALTER TABLE org_ai_configurations
    ADD COLUMN consent_since_version BIGINT NOT NULL DEFAULT 0;
UPDATE org_ai_configurations SET consent_since_version = configuration_version;
ALTER TABLE org_ai_configurations
    ADD CONSTRAINT org_ai_configurations_consent_since_check
    CHECK (consent_since_version >= 0 AND consent_since_version <= configuration_version);

-- (b) P87-D5: confirmations of members whose stored choice is not
--     'organization' end at deploy (a member with no settings row follows by
--     default, 029_llm_bring_your_own.up.sql:14, and keeps theirs).
DELETE FROM org_ai_confirmations f
WHERE EXISTS (
    SELECT 1 FROM settings s
    WHERE s.user_id = f.user_id AND s.llm_provider <> 'organization'
);

-- (c) a confirmation stores the consent period it covers. A stored value
--     equal to its organisation's current version equals the new
--     consent_since_version (stays confirmed, AC-18 X); an older one is below
--     it and never matches again (P87-D4, AC-18 Y). The CHECK (>= 1) stays.
ALTER TABLE org_ai_confirmations
    RENAME COLUMN configuration_version TO consent_since_version;
-- The column CHECK of 031 keeps its generated name across a column rename;
-- rename it too so the name says what it checks (challenge 2026-11-12).
ALTER TABLE org_ai_confirmations
    RENAME CONSTRAINT org_ai_confirmations_configuration_version_check
    TO org_ai_confirmations_consent_since_version_check;
