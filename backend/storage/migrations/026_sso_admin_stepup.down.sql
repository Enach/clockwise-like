-- 026_sso_admin_stepup.down.sql — reverts PAC-50's schema. Drops the grant and
-- SSO event tables (ids and short enums, no user data) and the tenant pin, and
-- restores the raw UNIQUE(domain). Backfilled domains stay lower-cased (spec
-- §7): every reader compares lower-cased and the only mixed-case writer was
-- the defect itself.
ALTER TABLE organizations DROP COLUMN IF EXISTS ms_tenant_id;
DROP INDEX IF EXISTS sso_providers_domain_lower_key;
ALTER TABLE sso_providers ADD CONSTRAINT sso_providers_domain_key UNIQUE (domain);
CREATE INDEX IF NOT EXISTS idx_sso_providers_domain ON sso_providers(domain);
DROP TABLE IF EXISTS sso_events;
DROP TABLE IF EXISTS sso_stepup_grants;
