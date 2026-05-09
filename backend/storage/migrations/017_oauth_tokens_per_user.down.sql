ALTER TABLE oauth_tokens ALTER COLUMN user_id DROP NOT NULL;
ALTER TABLE oauth_tokens DROP CONSTRAINT IF EXISTS oauth_tokens_user_calendar_unique;
