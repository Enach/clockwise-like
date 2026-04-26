CREATE TABLE IF NOT EXISTS user_profiles (
    user_id          UUID        PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    is_manager       BOOLEAN     NOT NULL DEFAULT false,
    detected_at      TIMESTAMPTZ,
    analytics_shared_with_manager BOOLEAN NOT NULL DEFAULT true,
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS manager_team_members (
    id                   UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    manager_user_id      UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    member_email         TEXT        NOT NULL,
    member_user_id       UUID        REFERENCES users(id) ON DELETE SET NULL,
    display_name         TEXT        NOT NULL DEFAULT '',
    source               TEXT        NOT NULL DEFAULT 'auto' CHECK (source IN ('auto', 'manual')),
    cadence              TEXT        NOT NULL DEFAULT 'none'
        CHECK (cadence IN ('weekly', 'biweekly', 'monthly', 'custom', 'none')),
    cadence_custom_days  INT,
    last_one_on_one_at   TIMESTAMPTZ,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (manager_user_id, member_email)
);

CREATE TABLE IF NOT EXISTS one_on_one_occurrences (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    manager_user_id   UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    member_email      TEXT        NOT NULL,
    calendar_event_id TEXT        NOT NULL,
    occurred_at       TIMESTAMPTZ NOT NULL,
    UNIQUE (manager_user_id, calendar_event_id)
);

CREATE TABLE IF NOT EXISTS team_analytics_cache (
    id               UUID    PRIMARY KEY DEFAULT gen_random_uuid(),
    manager_user_id  UUID    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    member_email     TEXT    NOT NULL,
    week_start       DATE    NOT NULL,
    focus_minutes    INT     NOT NULL DEFAULT 0,
    meeting_minutes  INT     NOT NULL DEFAULT 0,
    free_minutes     INT     NOT NULL DEFAULT 0,
    is_paceday_user  BOOLEAN NOT NULL DEFAULT false,
    data_available   BOOLEAN NOT NULL DEFAULT true,
    computed_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (manager_user_id, member_email, week_start)
);

CREATE INDEX IF NOT EXISTS idx_manager_members_mgr    ON manager_team_members(manager_user_id);
CREATE INDEX IF NOT EXISTS idx_ooo_mgr_date           ON one_on_one_occurrences(manager_user_id, occurred_at DESC);
CREATE INDEX IF NOT EXISTS idx_team_analytics_mgr_wk  ON team_analytics_cache(manager_user_id, week_start DESC);
