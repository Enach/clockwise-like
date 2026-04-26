CREATE TABLE habits (
  id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id          UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  title            TEXT        NOT NULL,
  duration_minutes INTEGER     NOT NULL,
  days_of_week     INTEGER[]   NOT NULL DEFAULT '{1,2,3,4,5}',
  window_start     TIME        NOT NULL DEFAULT '09:00',
  window_end       TIME        NOT NULL DEFAULT '17:00',
  priority         INTEGER     NOT NULL DEFAULT 50,
  color            TEXT        NOT NULL DEFAULT '#5B7FFF',
  active           BOOLEAN     NOT NULL DEFAULT true,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE habit_occurrences (
  id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  habit_id          UUID        NOT NULL REFERENCES habits(id) ON DELETE CASCADE,
  scheduled_date    DATE        NOT NULL,
  start_time        TIMESTAMPTZ NOT NULL,
  end_time          TIMESTAMPTZ NOT NULL,
  status            TEXT        NOT NULL DEFAULT 'scheduled'
                    CHECK (status IN ('scheduled','completed','missed','displaced')),
  calendar_event_id TEXT        NOT NULL DEFAULT '',
  created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (habit_id, scheduled_date)
);

CREATE INDEX idx_habits_user      ON habits(user_id);
CREATE INDEX idx_habit_occ_habit  ON habit_occurrences(habit_id);
CREATE INDEX idx_habit_occ_date   ON habit_occurrences(scheduled_date);
