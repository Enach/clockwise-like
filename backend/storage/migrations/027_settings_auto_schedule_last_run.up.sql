-- 027_settings_auto_schedule_last_run.up.sql — PAC-49: the per-user watermark
-- the focus scheduler runs from. A user's next scheduled run is their first
-- slot after this instant, evaluated in their own timezone. NULL for everyone
-- at migration time: the scheduler treats NULL on an enabled row as "now" and
-- does not run that user on that tick, so applying this never causes a burst
-- of runs. Nothing else in the schema changes.
ALTER TABLE settings ADD COLUMN auto_schedule_last_run TIMESTAMPTZ;
