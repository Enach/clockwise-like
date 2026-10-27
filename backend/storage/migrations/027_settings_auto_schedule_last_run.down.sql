-- 027_settings_auto_schedule_last_run.down.sql — drops the watermark and with
-- it every user's "last ran at" history; re-applying the up migration gives
-- every row NULL, which the scheduler treats as "now" (no catch-up burst).
ALTER TABLE settings DROP COLUMN IF EXISTS auto_schedule_last_run;
