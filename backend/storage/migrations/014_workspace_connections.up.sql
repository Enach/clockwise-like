CREATE TABLE IF NOT EXISTS workspace_connections (
    id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider         TEXT        NOT NULL CHECK (provider IN ('slack', 'notion')),
    access_token     TEXT        NOT NULL,
    bot_token        TEXT        NOT NULL DEFAULT '',
    workspace_id     TEXT        NOT NULL DEFAULT '',
    workspace_name   TEXT        NOT NULL DEFAULT '',
    scopes           TEXT[]      NOT NULL DEFAULT '{}',
    connected_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at       TIMESTAMPTZ,
    UNIQUE (user_id, provider)
);

CREATE TABLE IF NOT EXISTS meeting_briefs (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    calendar_event_id   TEXT        NOT NULL,
    user_id             UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    generated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    slack_results       JSONB       NOT NULL DEFAULT '[]',
    notion_results      JSONB       NOT NULL DEFAULT '[]',
    brief_text          TEXT        NOT NULL DEFAULT '',
    status              TEXT        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'ready', 'failed')),
    UNIQUE (user_id, calendar_event_id)
);

CREATE INDEX IF NOT EXISTS idx_workspace_connections_user ON workspace_connections(user_id);
CREATE INDEX IF NOT EXISTS idx_meeting_briefs_user_event  ON meeting_briefs(user_id, calendar_event_id);
CREATE INDEX IF NOT EXISTS idx_meeting_briefs_generated   ON meeting_briefs(generated_at DESC);
