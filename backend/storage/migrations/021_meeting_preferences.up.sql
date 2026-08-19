-- Meeting policy preferences.
ALTER TABLE settings
    ADD COLUMN IF NOT EXISTS out_of_hours_meetings_per_week INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS auto_decline_outside_working_hours BOOLEAN NOT NULL DEFAULT FALSE;
