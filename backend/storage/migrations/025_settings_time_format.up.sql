-- 025_settings_time_format.up.sql — PAC-55: each user's 12/24-hour display
-- preference. Every existing row reads '12h', which is exactly today's display.
ALTER TABLE settings
    ADD COLUMN time_format TEXT NOT NULL DEFAULT '12h'
        CONSTRAINT settings_time_format_check CHECK (time_format IN ('12h', '24h'));
