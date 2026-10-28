-- DESTROYS DATA: drops every user's daily-recap settings (the recap_* columns of settings) and the record of recaps already sent (recap_sends). Never run in production: roll back by restoring a backup (docs/runbook.md).
-- Reverses 015_daily_recap.up.sql.
DROP TABLE IF EXISTS recap_sends;

ALTER TABLE settings
    DROP COLUMN IF EXISTS recap_include_habits,
    DROP COLUMN IF EXISTS recap_include_focus,
    DROP COLUMN IF EXISTS recap_include_briefs,
    DROP COLUMN IF EXISTS recap_channel_id,
    DROP COLUMN IF EXISTS recap_send_to,
    DROP COLUMN IF EXISTS recap_send_time,
    DROP COLUMN IF EXISTS recap_enabled;
