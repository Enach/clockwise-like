ALTER TABLE settings
    DROP COLUMN IF EXISTS auto_decline_outside_working_hours,
    DROP COLUMN IF EXISTS out_of_hours_meetings_per_week;
