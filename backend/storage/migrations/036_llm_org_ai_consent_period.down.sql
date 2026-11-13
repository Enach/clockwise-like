-- PAC-87 down (contracts/features/PAC-87.md §3.2, §3.3): for development,
-- tests and staging; production rollback is docs/runbook.md "Rollback".
-- Keep only confirmations of followers recorded in a period that started at
-- the organisation's CURRENT version (no key or destination change since, so
-- recorded at exactly the current state); end all others.
DELETE FROM org_ai_confirmations f
USING org_ai_configurations c
WHERE c.org_id = f.org_id
  AND f.consent_since_version <> c.configuration_version;
DELETE FROM org_ai_confirmations f
WHERE NOT EXISTS (SELECT 1 FROM org_ai_configurations c WHERE c.org_id = f.org_id);
DELETE FROM org_ai_confirmations f
WHERE EXISTS (
    SELECT 1 FROM settings s
    WHERE s.user_id = f.user_id AND s.llm_provider <> 'organization'
);
ALTER TABLE org_ai_confirmations
    RENAME CONSTRAINT org_ai_confirmations_consent_since_version_check
    TO org_ai_confirmations_configuration_version_check;
ALTER TABLE org_ai_confirmations
    RENAME COLUMN consent_since_version TO configuration_version;
ALTER TABLE org_ai_configurations
    DROP CONSTRAINT IF EXISTS org_ai_configurations_consent_since_check;
ALTER TABLE org_ai_configurations DROP COLUMN IF EXISTS consent_since_version;
