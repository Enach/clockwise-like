-- Per-user working-hours and lunch-break schedules.
ALTER TABLE settings
    ADD COLUMN IF NOT EXISTS working_hours JSONB NOT NULL DEFAULT '{"mode":"all_days","default":{"enabled":true,"start":"09:00","end":"18:00"},"days":{}}'::jsonb,
    ADD COLUMN IF NOT EXISTS lunch_breaks JSONB NOT NULL DEFAULT '{}'::jsonb;

UPDATE settings
SET working_hours = COALESCE(working_hours, '{"mode":"all_days","default":{"enabled":true,"start":"09:00","end":"18:00"},"days":{}}'::jsonb),
    lunch_breaks = COALESCE(lunch_breaks, '{}'::jsonb);
