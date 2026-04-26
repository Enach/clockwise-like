CREATE TABLE IF NOT EXISTS settings (
    id SERIAL PRIMARY KEY,
    work_start TEXT NOT NULL DEFAULT '09:00',
    work_end TEXT NOT NULL DEFAULT '18:00',
    timezone TEXT NOT NULL DEFAULT 'UTC',
    focus_min_block_minutes INTEGER NOT NULL DEFAULT 25,
    focus_max_block_minutes INTEGER NOT NULL DEFAULT 120,
    focus_daily_target_minutes INTEGER NOT NULL DEFAULT 240,
    focus_label TEXT NOT NULL DEFAULT 'Focus Time',
    focus_color TEXT NOT NULL DEFAULT '#4F46E5',
    lunch_start TEXT NOT NULL DEFAULT '12:00',
    lunch_end TEXT NOT NULL DEFAULT '13:00',
    protect_lunch BOOLEAN NOT NULL DEFAULT TRUE,
    buffer_before_minutes INTEGER NOT NULL DEFAULT 5,
    buffer_after_minutes INTEGER NOT NULL DEFAULT 5,
    compression_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    auto_schedule_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    auto_schedule_cron TEXT NOT NULL DEFAULT '0 8 * * *',
    llm_provider TEXT NOT NULL DEFAULT '',
    llm_model TEXT NOT NULL DEFAULT '',
    llm_api_key TEXT NOT NULL DEFAULT '',
    llm_base_url TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS oauth_tokens (
    id SERIAL PRIMARY KEY,
    access_token TEXT NOT NULL,
    refresh_token TEXT NOT NULL,
    expiry TIMESTAMPTZ NOT NULL,
    calendar_id TEXT NOT NULL DEFAULT 'primary',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS focus_blocks (
    id SERIAL PRIMARY KEY,
    google_event_id TEXT NOT NULL UNIQUE,
    start_time TIMESTAMPTZ NOT NULL,
    end_time TIMESTAMPTZ NOT NULL,
    date TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS audit_log (
    id SERIAL PRIMARY KEY,
    action TEXT NOT NULL,
    details TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
