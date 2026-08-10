-- Backend/frontend contract fields for scheduling links and personal calendars.
ALTER TABLE scheduling_links
    ADD COLUMN IF NOT EXISTS min_notice_minutes INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS usage_type TEXT NOT NULL DEFAULT 'reusable',
    ADD COLUMN IF NOT EXISTS max_uses INTEGER;

ALTER TABLE personal_calendars
    ADD COLUMN IF NOT EXISTS last_synced_at TIMESTAMPTZ;
