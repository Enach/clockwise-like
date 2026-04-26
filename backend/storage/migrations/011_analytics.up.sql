CREATE TABLE analytics_weeks (
  id                           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id                      UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  week_start                   DATE        NOT NULL,
  total_working_minutes        INTEGER     NOT NULL DEFAULT 0,
  meeting_minutes              INTEGER     NOT NULL DEFAULT 0,
  focus_minutes                INTEGER     NOT NULL DEFAULT 0,
  habit_minutes                INTEGER     NOT NULL DEFAULT 0,
  buffer_minutes               INTEGER     NOT NULL DEFAULT 0,
  personal_minutes             INTEGER     NOT NULL DEFAULT 0,
  free_minutes                 INTEGER     NOT NULL DEFAULT 0,
  meeting_count                INTEGER     NOT NULL DEFAULT 0,
  focus_block_count            INTEGER     NOT NULL DEFAULT 0,
  habit_completion_rate        FLOAT       NOT NULL DEFAULT 0,
  largest_focus_block_minutes  INTEGER     NOT NULL DEFAULT 0,
  top_meeting_titles           JSONB       NOT NULL DEFAULT '[]',
  focus_score                  INTEGER     NOT NULL DEFAULT 50,
  estimated_meeting_cost_minutes INTEGER   NOT NULL DEFAULT 0,
  computed_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (user_id, week_start)
);

CREATE INDEX idx_analytics_weeks_user ON analytics_weeks(user_id);
CREATE INDEX idx_analytics_weeks_date ON analytics_weeks(week_start);
