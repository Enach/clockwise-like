ALTER TABLE settings ADD COLUMN calendar_provider TEXT NOT NULL DEFAULT 'google';
ALTER TABLE settings ADD COLUMN microsoft_tokens TEXT;
ALTER TABLE settings ADD COLUMN webcal_url TEXT NOT NULL DEFAULT '';
ALTER TABLE settings ADD COLUMN calendar_email TEXT NOT NULL DEFAULT '';
