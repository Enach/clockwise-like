-- 032_identity_binding.down.sql — PAC-71 rollback (spec §7, contract rollback).
-- Drops the bindings, the address standings, the session generations and the
-- case-insensitive key. Accounts, credentials, memberships and settings are
-- untouched. operator_audit is deliberately KEPT: the audit of operator steps
-- survives a rollback. Sessions ended by a generation advance become valid
-- again under the reverted code until they expire: rotate the signing secret.
DROP TABLE IF EXISTS user_identities;
DROP INDEX IF EXISTS users_email_lower_idx;
DROP INDEX IF EXISTS users_email_key_unique;
ALTER TABLE users DROP COLUMN IF EXISTS email_key;
ALTER TABLE users DROP COLUMN IF EXISTS address_standing;
ALTER TABLE users DROP COLUMN IF EXISTS session_generation;
