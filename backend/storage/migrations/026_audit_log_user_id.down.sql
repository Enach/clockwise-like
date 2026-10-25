-- 026_audit_log_user_id.down.sql — PAC-23. LOSES DATA: no row is deleted, but
-- every attribution recorded since the up migration is destroyed, irrecoverably,
-- and running up again leaves the whole table unattributed (every user's log
-- empty). The supported rollback is code-only: revert the application and leave
-- this column in place (docs/factory/PAC-23-plan.md §6). Run this only if the
-- feature is abandoned.
ALTER TABLE audit_log DROP CONSTRAINT IF EXISTS audit_log_user_id_present;
DROP INDEX IF EXISTS audit_log_user_id_created_at_id_idx;
ALTER TABLE audit_log DROP COLUMN IF EXISTS user_id;
