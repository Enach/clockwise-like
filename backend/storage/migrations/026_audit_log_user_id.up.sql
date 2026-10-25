-- 026_audit_log_user_id.up.sql — PAC-23: every activity entry names the user
-- whose calendar it is about, and GET /api/audit returns only the caller's own.
--
-- Rows recorded before this migration cannot be attributed (spec PAC-23 §2.5).
-- They are KEPT, with user_id NULL, and returned to nobody (spec §3.3). They are
-- not deleted and not attributed by inference.

ALTER TABLE audit_log ADD COLUMN user_id UUID REFERENCES users(id) ON DELETE CASCADE;

-- Matches the scoped read exactly:
-- WHERE user_id = $1 ORDER BY created_at DESC, id DESC LIMIT $2.
CREATE INDEX audit_log_user_id_created_at_id_idx ON audit_log (user_id, created_at DESC, id DESC);

-- Every row written from now on must name a subject (spec §3.2); the foreign
-- key refuses a subject that is not a user, the zero UUID included.
--
-- NOT VALID on purpose, and it must NEVER be validated: the pre-PAC-23 rows are
-- permanent, deliberate violations of this constraint. Running
-- ALTER TABLE audit_log VALIDATE CONSTRAINT audit_log_user_id_present fails on
-- them, and the only ways to make it pass are deleting or inventing an owner
-- for that history, both of which the spec rejects.
ALTER TABLE audit_log ADD CONSTRAINT audit_log_user_id_present CHECK (user_id IS NOT NULL) NOT VALID;
