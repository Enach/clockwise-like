-- DESTROYS DATA: drops every organization (the organizations table) and each user's membership (users.org_id). Never run in production: roll back by restoring a backup (docs/runbook.md).
-- Reverses 007_organizations.up.sql.
DROP INDEX IF EXISTS idx_users_org_id;
ALTER TABLE users DROP COLUMN IF EXISTS org_id;

DROP INDEX IF EXISTS idx_organizations_domain;
DROP TABLE IF EXISTS organizations;
