CREATE TABLE personal_calendars (
    id               SERIAL PRIMARY KEY,
    provider         TEXT NOT NULL,
    name             TEXT NOT NULL DEFAULT '',
    url              TEXT NOT NULL DEFAULT '',
    credentials_json TEXT NOT NULL DEFAULT '',
    enabled          BOOLEAN NOT NULL DEFAULT TRUE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE personal_blockers (
    id                   SERIAL PRIMARY KEY,
    personal_calendar_id INTEGER NOT NULL REFERENCES personal_calendars(id) ON DELETE CASCADE,
    personal_event_id    TEXT NOT NULL,
    work_event_id        TEXT NOT NULL,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (personal_calendar_id, personal_event_id)
);
