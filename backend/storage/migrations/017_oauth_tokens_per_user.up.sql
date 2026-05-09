-- 017_oauth_tokens_per_user.up.sql
-- Convert oauth_tokens from single-row (id=1) to per-user-per-calendar.
-- Background: prior code hardcoded INSERT ... VALUES (1, ...) ON CONFLICT (id)
-- which meant the table only ever held one row; multi-tenant token storage
-- silently overwrote on every connection. This migration allows one token
-- per (user_id, calendar_id) and enforces user_id NOT NULL.

-- Drop any orphan rows lacking a user link before tightening the constraint.
DELETE FROM oauth_tokens WHERE user_id IS NULL;

ALTER TABLE oauth_tokens
    ADD CONSTRAINT oauth_tokens_user_calendar_unique UNIQUE (user_id, calendar_id);

ALTER TABLE oauth_tokens
    ALTER COLUMN user_id SET NOT NULL;
