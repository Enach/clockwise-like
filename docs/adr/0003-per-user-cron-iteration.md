# ADR 0003: Per-user cron iteration (FocusCron + PersonalBlocker)

- Status: accepted
- Date: 2026-05-10
- Project: Paceday

## Context

PR #7 ([fix(security)](https://github.com/Enach/clockwise-like/pull/7)) closed a multi-tenant token leak by making `auth.LoadUserToken(db, uuid.Nil)` return `ErrNoUser` instead of silently falling back to a legacy `oauth_tokens.id = 1` row. Migration 017 enforces `UNIQUE (user_id, calendar_id)` and `NOT NULL` on `oauth_tokens.user_id`.

That fix surfaced a second-layer leak: two cron loops (`backend/scheduler/cron.go FocusCron.Reload` and the personal-blocker cron in `backend/main.go`) call into the engine with a fresh `context.Background()` and no userID — there is no per-user iteration. After migration 017, those crons will fail at `newCalOps` with `ErrNoUser` instead of leaking, but the *feature* is broken until the crons iterate users explicitly.

Two storage tables relevant to those crons (`settings` and `personal_calendars`) also still hold single-row state (legacy `id = 1` for settings; pre-migration `user_id` was nullable on both). Until those tables are per-user-keyed, "iterate users" has nowhere to draw from.

## Decision

Make both crons iterate users, mirroring the per-user pattern from migration 017:

1. **Migration 018** backfills `user_id` on both `settings` (best-effort to the first user via `ORDER BY created_at LIMIT 1`) and `personal_calendars`. Tightens to `NOT NULL` + `UNIQUE (user_id)` on settings (one row per user per session-currently-supported config). Personal_calendars remains many-per-user.

2. **New storage queries** (additive, do not remove existing single-tenant helpers):
   - `GetSettingsByUser(db, userID uuid.UUID) (*Settings, error)` — returns the row keyed by user_id, or `ErrNoUser` on `uuid.Nil`.
   - `ListUsersWithAutoSchedule(db) ([]UserScheduleConfig, error)` — returns `[]{UserID, CronExpr}` for users with `auto_schedule_enabled = true AND auto_schedule_cron <> ''`.
   - `ListPersonalCalendarsByUser(db, userID uuid.UUID) ([]PersonalCalendar, error)` — filters by `user_id`.

3. **Engine API** gains `*ForUser` variants:
   - `FocusTimeEngine.RunForUser(ctx, userID, targetWeek)` — builds ctx with `auth.UserIDKey` set, calls `GetSettingsByUser`, runs.
   - `PersonalBlocker.SyncAllForUser(ctx, userID)` — iterates `ListPersonalCalendarsByUser(db, userID)`.

   Existing `Run` / `SyncAll` delegate to the new methods after reading `userID` from `ctx` (so HTTP handler callers that already set `auth.UserIDKey` continue to work — see PR #7 changes).

4. **Cron loops iterate users**:
   - `FocusCron.Reload` queries `ListUsersWithAutoSchedule`, registers one `cron.AddFunc` per user, captures `userID` in the closure, and calls `eng.RunForUser(ctx, userID, time.Now())`. Reload is called on settings changes (handler-level hook left as a follow-up).
   - `main.go` personal-blocker cron groups personal calendars by user and runs `blocker.SyncAllForUser(ctx, userID)` for each.

5. Existing handler call sites (`GetSettings`, `ListPersonalCalendars`) remain unchanged in this PR. A follow-up ADR will migrate those to the `ByUser` variants — that's a 16-file edit best done as its own change.

## Consequences

- Positive: cron paths work again in a multi-tenant world. ErrNoUser is no longer reachable from any cron loop. Each user's auto-schedule runs against their own oauth_tokens, their own settings, their own personal_calendars.
- Positive: storage schema is closer to fully multi-tenant — only `settings` and `personal_calendars` remained ambiguous post-migration-017.
- Negative: handlers still use legacy `WHERE id = 1` queries. They continue to work because the backfill puts one row in settings with user_id = (first user). Multi-user deployments will hit handler bugs until follow-up ADR-0004 migrates them.
- Negative: `Reload` is called only at process start. If a user enables auto-schedule mid-run, the cron entry isn't added until the next restart. Follow-up: handler-level reload hook on settings PUT.

## Alternatives considered

- Iterate all users in a single cron tick instead of registering per-user entries: rejected because each user has their own `auto_schedule_cron` expression. Per-user registration honours each user's cadence.
- Delete the cron paths entirely and only allow on-demand `/api/focus/run`: rejected — auto-schedule is a documented product feature.
- Defer this PR and require manual `/api/focus/run` until handler refactor lands: rejected — leaves a documented feature broken in main longer than necessary.
