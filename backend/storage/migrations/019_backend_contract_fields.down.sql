ALTER TABLE personal_calendars DROP COLUMN IF EXISTS last_synced_at;
ALTER TABLE scheduling_links
    DROP COLUMN IF EXISTS max_uses,
    DROP COLUMN IF EXISTS usage_type,
    DROP COLUMN IF EXISTS min_notice_minutes;
