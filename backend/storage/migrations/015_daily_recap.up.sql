ALTER TABLE settings
    ADD COLUMN IF NOT EXISTS recap_enabled            BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS recap_send_time          TIME    NOT NULL DEFAULT '08:00',
    ADD COLUMN IF NOT EXISTS recap_send_to            TEXT    NOT NULL DEFAULT 'dm'
        CHECK (recap_send_to IN ('dm', 'channel')),
    ADD COLUMN IF NOT EXISTS recap_channel_id         TEXT    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS recap_include_briefs     BOOLEAN NOT NULL DEFAULT true,
    ADD COLUMN IF NOT EXISTS recap_include_focus      BOOLEAN NOT NULL DEFAULT true,
    ADD COLUMN IF NOT EXISTS recap_include_habits     BOOLEAN NOT NULL DEFAULT true;

CREATE TABLE IF NOT EXISTS recap_sends (
    id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    sent_date        DATE        NOT NULL,
    sent_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    slack_message_ts TEXT        NOT NULL DEFAULT '',
    status           TEXT        NOT NULL DEFAULT 'sent' CHECK (status IN ('sent', 'failed')),
    UNIQUE (user_id, sent_date)
);

CREATE INDEX IF NOT EXISTS idx_recap_sends_user_date ON recap_sends(user_id, sent_date DESC);
