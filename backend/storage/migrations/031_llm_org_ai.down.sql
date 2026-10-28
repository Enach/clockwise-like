-- 031_llm_org_ai.down.sql — reverts PAC-73 Part 2's schema.
--
-- THIS LOSES DATA (spec §7): every organisation's AI configuration including
-- its key, every member's confirmation, and the operator records of
-- organisation-AI changes. Release notes must say so. Grants issued for the
-- new purpose are deleted (useless without this part); the operator records
-- of the new kind must go before the kind can leave the CHECK. Leaves 030
-- applied: the database returns to 030.
DROP INDEX IF EXISTS sso_events_llm_org_ai_breakglass_once;
DELETE FROM sso_events WHERE kind = 'llm_org_ai_written';
ALTER TABLE sso_events DROP CONSTRAINT sso_events_kind_check;
ALTER TABLE sso_events ADD CONSTRAINT sso_events_kind_check
    CHECK (kind IN ('sso_stepup', 'sso_provider_written'));
DELETE FROM sso_stepup_grants WHERE purpose = 'llm_org_key_write';
DROP TABLE IF EXISTS org_ai_confirmations;
DROP TABLE IF EXISTS org_ai_configurations;
