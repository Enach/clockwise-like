-- DESTROYS DATA: drops every user account (the users table) and the user_id link on oauth_tokens, settings, focus_blocks and personal_calendars, so every remaining row in those tables loses its owner. Never run in production: roll back by restoring a backup (docs/runbook.md).
-- Reverses 006_auth.up.sql. Every later migration's down file must already
-- have run; 007's down removes users.org_id first.
ALTER TABLE personal_calendars DROP COLUMN IF EXISTS user_id;
ALTER TABLE focus_blocks       DROP COLUMN IF EXISTS user_id;
ALTER TABLE settings           DROP COLUMN IF EXISTS user_id; -- drops settings_user_id_idx with it
ALTER TABLE oauth_tokens       DROP COLUMN IF EXISTS user_id; -- drops oauth_tokens_user_id_idx with it

DROP TABLE IF EXISTS users;

-- 006 installed pgcrypto (for gen_random_uuid); nothing at 005 uses it.
DROP EXTENSION IF EXISTS "pgcrypto";
