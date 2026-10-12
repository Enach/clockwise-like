-- 024_settings_per_user.down.sql — loosen the constraint and nothing else.
--
-- The per-user rows created by the up migration hold every setting saved since
-- the upgrade; deleting them is the one irreversible act available here, so the
-- down migration keeps them. Re-applying 024 picks them straight back up.
ALTER TABLE settings ALTER COLUMN user_id DROP NOT NULL;
