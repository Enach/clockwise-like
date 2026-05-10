-- 018_per_user_settings_and_calendars.up.sql
-- Make settings + personal_calendars per-user-keyed for cron iteration.
-- See ADR-0003.
--
-- We do NOT impose NOT NULL on settings.user_id here because the existing
-- insertDefaultSettings path (legacy single-row id=1 helper used by ~16 handler
-- call sites) doesn't yet supply a user_id. The cron iteration queries
-- explicitly filter `WHERE user_id IS NOT NULL AND auto_schedule_enabled`
-- so NULL rows are safely ignored. Follow-up ADR will migrate handlers to
-- always supply user_id and then tighten this constraint.

-- Best-effort backfill of settings.user_id from the first existing user, so
-- the single-tenant production deployment lands the legacy id=1 row on a real user.
UPDATE settings
SET user_id = (SELECT id FROM users ORDER BY created_at LIMIT 1)
WHERE user_id IS NULL
  AND EXISTS (SELECT 1 FROM users);

-- PostgreSQL UNIQUE allows multiple NULLs, so this constraint coexists with
-- the legacy "no user yet" default row produced by insertDefaultSettings.
ALTER TABLE settings
    ADD CONSTRAINT settings_user_id_unique UNIQUE (user_id);

-- personal_calendars: only backfill, don't tighten. Per-user query already
-- filters by user_id explicitly, so NULL rows are silently skipped.
UPDATE personal_calendars
SET user_id = (SELECT id FROM users ORDER BY created_at LIMIT 1)
WHERE user_id IS NULL
  AND EXISTS (SELECT 1 FROM users);
