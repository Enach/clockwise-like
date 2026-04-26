ALTER TABLE settings ADD COLUMN conferencing_provider TEXT NOT NULL DEFAULT 'meet';
ALTER TABLE settings ADD COLUMN zoom_tokens          TEXT NOT NULL DEFAULT '';
