-- 025_settings_time_format.down.sql — drops every user's choice; everyone
-- returns to the 12-hour display. Revert the web before or with the server.
ALTER TABLE settings DROP COLUMN IF EXISTS time_format;
