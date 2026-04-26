ALTER TABLE settings
    DROP COLUMN IF EXISTS buffer_enabled,
    DROP COLUMN IF EXISTS buffer_min_meeting_minutes,
    DROP COLUMN IF EXISTS buffer_skip_back_to_back;
