ALTER TABLE personal_calendars ALTER COLUMN user_id DROP NOT NULL;
ALTER TABLE settings ALTER COLUMN user_id DROP NOT NULL;
ALTER TABLE settings DROP CONSTRAINT IF EXISTS settings_user_id_unique;
