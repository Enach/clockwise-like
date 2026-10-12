-- 024_settings_per_user.up.sql — PAC-24: every user has their own settings row.
--
-- Until now the handlers read and wrote the single row id = 1, and no code path
-- created a row per user. This gives every user a row, then makes user_id
-- NOT NULL so no settings row can belong to nobody. Migration 018 is not
-- amended (it has shipped). The anchor is the legacy shared row, id = 1.

-- 0. Normalise stored values the PAC-24 contract's Settings schema does not
--    admit, to the value the code already behaved as (contract revision 3,
--    challenge finding 2). Without this a GET could return a document the
--    client rejects, or one it cannot save back.
UPDATE settings SET calendar_provider = 'google'
    WHERE calendar_provider IS NULL OR calendar_provider NOT IN ('google', 'outlook', 'webcal');
UPDATE settings SET conferencing_provider = 'meet'
    WHERE conferencing_provider IS NULL OR conferencing_provider NOT IN ('meet', 'zoom', 'teams');
-- recap_send_to needs nothing: a CHECK constraint already limits it to dm/channel.
UPDATE settings SET
    focus_min_block_minutes        = GREATEST(focus_min_block_minutes, 0),
    focus_max_block_minutes        = GREATEST(focus_max_block_minutes, 0),
    focus_daily_target_minutes     = GREATEST(focus_daily_target_minutes, 0),
    out_of_hours_meetings_per_week = GREATEST(out_of_hours_meetings_per_week, 0),
    buffer_before_minutes          = GREATEST(buffer_before_minutes, 0),
    buffer_after_minutes           = GREATEST(buffer_after_minutes, 0),
    buffer_min_meeting_minutes     = GREATEST(buffer_min_meeting_minutes, 0);
-- A clock the old loose check let through (99:99) is not a time; the column
-- default is. The empty string stays: it is a meaningful stored state.
UPDATE settings SET work_start = DEFAULT
    WHERE work_start <> '' AND work_start !~ '^([01][0-9]|2[0-3]):[0-5][0-9]$';
UPDATE settings SET work_end = DEFAULT
    WHERE work_end <> '' AND work_end !~ '^([01][0-9]|2[0-3]):[0-5][0-9]$';
UPDATE settings SET lunch_start = DEFAULT
    WHERE lunch_start <> '' AND lunch_start !~ '^([01][0-9]|2[0-3]):[0-5][0-9]$';
UPDATE settings SET lunch_end = DEFAULT
    WHERE lunch_end <> '' AND lunch_end !~ '^([01][0-9]|2[0-3]):[0-5][0-9]$';
-- workingHours.mode: WorkWindow matched by_day case-insensitively and treated
-- anything else as all_days, so that is what the stored value becomes.
UPDATE settings SET working_hours = jsonb_set(working_hours, '{mode}',
        to_jsonb(CASE
            WHEN lower(btrim(working_hours->>'mode')) = 'by_day' THEN 'by_day'
            WHEN btrim(COALESCE(working_hours->>'mode', '')) = '' THEN ''
            ELSE 'all_days' END))
    WHERE working_hours ? 'mode'
      AND working_hours->>'mode' NOT IN ('', 'all_days', 'by_day');
-- Weekday keys that are not exactly lowercase never matched WorkWindow's or
-- LunchWindow's lookup; removing them changes no behaviour.
UPDATE settings SET working_hours = jsonb_set(working_hours, '{days}', COALESCE(
        (SELECT jsonb_object_agg(d.key, d.value) FROM jsonb_each(working_hours->'days') d
         WHERE d.key IN ('monday','tuesday','wednesday','thursday','friday','saturday','sunday')),
        '{}'::jsonb))
    WHERE jsonb_typeof(working_hours->'days') = 'object';
UPDATE settings SET lunch_breaks = COALESCE(
        (SELECT jsonb_object_agg(d.key, d.value) FROM jsonb_each(lunch_breaks) d
         WHERE d.key IN ('monday','tuesday','wednesday','thursday','friday','saturday','sunday')),
        '{}'::jsonb)
    WHERE jsonb_typeof(lunch_breaks) = 'object';

-- 1. The legacy `INSERT INTO settings (id) VALUES (1)` bypassed the sequence;
--    move it past every id in use so step 3's inserts cannot collide.
SELECT setval(pg_get_serial_sequence('settings', 'id'),
              COALESCE((SELECT MAX(id) FROM settings), 0) + 1, false);

-- 2. Give an unattributed shared row to the earliest user, if that user has no
--    row of their own. Only the lowest such row, so the unique constraint on
--    user_id cannot be violated (018's blanket UPDATE could).
UPDATE settings
SET user_id = (SELECT id FROM users ORDER BY created_at, id LIMIT 1)
WHERE id = (SELECT MIN(id) FROM settings WHERE user_id IS NULL)
  AND EXISTS (SELECT 1 FROM users)
  AND NOT EXISTS (
      SELECT 1 FROM settings own
      WHERE own.user_id = (SELECT id FROM users ORDER BY created_at, id LIMIT 1)
  );

-- 3. Every user without a row gets one. Passive preferences are copied from
--    the anchor (spec PAC-24, "Decisions — 2026-10-12"); everything that acts
--    on a calendar by itself, every credential, provider, endpoint, calendar
--    identity and recap setting stays at its column default.
INSERT INTO settings (
    user_id,
    work_start, work_end, working_hours,
    lunch_start, lunch_end, lunch_breaks, protect_lunch,
    timezone,
    focus_min_block_minutes, focus_max_block_minutes, focus_daily_target_minutes,
    focus_label, focus_color,
    buffer_before_minutes, buffer_after_minutes, buffer_enabled,
    buffer_min_meeting_minutes, buffer_skip_back_to_back,
    out_of_hours_meetings_per_week
)
SELECT
    u.id,
    a.work_start, a.work_end, a.working_hours,
    a.lunch_start, a.lunch_end, a.lunch_breaks, a.protect_lunch,
    a.timezone,
    a.focus_min_block_minutes, a.focus_max_block_minutes, a.focus_daily_target_minutes,
    a.focus_label, a.focus_color,
    a.buffer_before_minutes, a.buffer_after_minutes, a.buffer_enabled,
    a.buffer_min_meeting_minutes, a.buffer_skip_back_to_back,
    a.out_of_hours_meetings_per_week
FROM users u
CROSS JOIN (SELECT * FROM settings WHERE id = 1) a
WHERE NOT EXISTS (SELECT 1 FROM settings own WHERE own.user_id = u.id);

-- With no anchor row, every remaining user gets the product defaults.
INSERT INTO settings (user_id)
SELECT u.id FROM users u
WHERE NOT EXISTS (SELECT 1 FROM settings own WHERE own.user_id = u.id);

-- 4. A row belonging to nobody is unreachable once every read takes a user.
DELETE FROM settings WHERE user_id IS NULL;

-- 5. No settings row without an owner, from now on. settings.id is left alone:
--    rollback relies on id = 1 still being the anchor's row.
ALTER TABLE settings ALTER COLUMN user_id SET NOT NULL;
