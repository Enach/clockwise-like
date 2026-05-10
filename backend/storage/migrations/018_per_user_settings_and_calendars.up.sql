-- 018_per_user_settings_and_calendars.up.sql
-- Migrate settings and personal_calendars from single-tenant (id=1 / NULL user_id)
-- to per-user-keyed rows. Mirrors migration 017 for oauth_tokens.

-- Drop orphan rows (no associated user). If there are users in the system, the
-- next step assigns the existing settings row to the first user as a best-effort
-- migration for the legacy single-user deployment.
UPDATE settings
SET user_id = (SELECT id FROM users ORDER BY created_at LIMIT 1)
WHERE user_id IS NULL
  AND EXISTS (SELECT 1 FROM users);

DELETE FROM settings WHERE user_id IS NULL;

UPDATE personal_calendars
SET user_id = (SELECT id FROM users ORDER BY created_at LIMIT 1)
WHERE user_id IS NULL
  AND EXISTS (SELECT 1 FROM users);

DELETE FROM personal_calendars WHERE user_id IS NULL;

ALTER TABLE settings
    ADD CONSTRAINT settings_user_id_unique UNIQUE (user_id);

ALTER TABLE settings
    ALTER COLUMN user_id SET NOT NULL;

ALTER TABLE personal_calendars
    ALTER COLUMN user_id SET NOT NULL;
