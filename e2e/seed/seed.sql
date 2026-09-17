-- Paceday e2e fixtures.
--
-- Contract with the rest of the harness:
--   * Runs AFTER the backend has booted, because the backend runs the
--     golang-migrate migrations in storage.Open() at startup. This file
--     therefore assumes the schema is at head and never creates tables.
--   * IDEMPOTENT. Every row is keyed by a fixed UUID (see seed/ids.ts) and is
--     deleted before being re-inserted, so re-running it from a dirty database
--     produces byte-identical fixtures. It also deletes rows the TESTS create
--     (bookings, focus blocks, manager assignments) so a previous run's state
--     can never leak into this one.
--   * SELF-CONTAINED per journey. Each mutating test owns its own scheduling
--     link AND its own host user, because engine.BookingEngine.hostBusy()
--     treats a host's confirmed bookings as busy time. Sharing a host across
--     two booking tests would make them order-dependent.
--
-- Anything a test asserts on must be VISIBLY unlike the frontend's built-in
-- demo fixtures (src/api/manager.ts SEED_MEMBERS: "Sarah Chen", "Miguel
-- Alvarez", ...). Every name here is prefixed "E2E " for that reason: if a
-- test ever passes against demo data instead of the backend, the assertion
-- fails rather than silently succeeding.

BEGIN;

-- ── Tear down everything this fixture set owns ───────────────────────────────
-- Order matters only where ON DELETE CASCADE does not cover it.

DELETE FROM bookings
 WHERE link_id IN (
   SELECT id FROM scheduling_links WHERE slug LIKE 'e2e-%'
 );

DELETE FROM scheduling_links WHERE slug LIKE 'e2e-%';

DELETE FROM manager_team_member_assignments
 WHERE manager_user_id IN (
   SELECT id FROM users WHERE email LIKE 'e2e.%@paceday.test'
 );

DELETE FROM manager_team_members
 WHERE manager_user_id IN (
   SELECT id FROM users WHERE email LIKE 'e2e.%@paceday.test'
 );

DELETE FROM teams WHERE name LIKE 'E2E %';

-- focus_blocks has NO user_id filter in storage.ListFocusBlocksForWeek, so a
-- stray block is global busy time for every booking host. Clear them all.
DELETE FROM focus_blocks;

DELETE FROM users WHERE email LIKE 'e2e.%@paceday.test';

-- ── Users ────────────────────────────────────────────────────────────────────
-- No oauth_tokens rows are created for ANY of these users, and that is
-- deliberate, not an omission:
--   * auth.LoadUserToken() returning nil is the cheap, deterministic "calendar
--     not connected" path. Every calendar-touching code path checks it first
--     and skips the network entirely.
--   * Seeding a fake token would instead send the backend at
--     www.googleapis.com, which the compose file blackholes — a slower and
--     less honest failure.
-- The consequence is documented per journey in e2e/README.md.

INSERT INTO users (id, email, name, avatar_url, provider, provider_id) VALUES
  ('11111111-1111-4111-8111-111111111111', 'e2e.primary@paceday.test', 'E2E Primary Owner',  '', 'google', 'e2e-primary'),
  ('22222222-2222-4222-8222-222222222222', 'e2e.second@paceday.test',  'E2E Second User',    '', 'google', 'e2e-second'),
  ('33333333-3333-4333-8333-333333333333', 'e2e.report@paceday.test',  'E2E Alpha Report',   '', 'google', 'e2e-report'),
  ('44444444-4444-4444-8444-444444444444', 'e2e.beta@paceday.test',    'E2E Beta Report',    '', 'google', 'e2e-beta');

-- ── Settings ─────────────────────────────────────────────────────────────────
-- Every other settings column carries a NOT NULL DEFAULT (migrations 001, 012,
-- 019, 020, 021), so only the identifying + behaviour-relevant ones are set.
-- timezone MUST be a name time.LoadLocation can resolve; engine.FocusTimeEngine
-- errors out on an unknown one.
-- calendar_provider stays 'google': calendar.NewProvider() is dead code (no
-- production call site), so setting 'webcal' here would change nothing and
-- would misrepresent what the stack does.

INSERT INTO settings (user_id, timezone, work_start, work_end, focus_daily_target_minutes, calendar_provider)
VALUES
  ('11111111-1111-4111-8111-111111111111', 'UTC', '09:00', '18:00', 240, 'google'),
  ('22222222-2222-4222-8222-222222222222', 'UTC', '09:00', '18:00', 240, 'google'),
  ('33333333-3333-4333-8333-333333333333', 'UTC', '09:00', '18:00', 240, 'google'),
  ('44444444-4444-4444-8444-444444444444', 'UTC', '09:00', '18:00', 240, 'google')
ON CONFLICT (user_id) DO UPDATE SET
  timezone = EXCLUDED.timezone,
  work_start = EXCLUDED.work_start,
  work_end = EXCLUDED.work_end,
  focus_daily_target_minutes = EXCLUDED.focus_daily_target_minutes,
  calendar_provider = EXCLUDED.calendar_provider;

-- ── User profiles ────────────────────────────────────────────────────────────
-- detected_at MUST be non-NULL for the primary user. src/api/manager.ts derives
-- the client-side flag `onboarding_profile_selected` as
--   Boolean(raw.detected_at) || <previously stored local value>
-- and components/RequireAuth.tsx redirects to /app/onboarding when it is false.
-- Without this row every authenticated navigation would land on onboarding.
-- (The backend has no onboarding_profile_selected field at all — see
-- TESTIDS-REQUIRED.md "Contract gaps found while writing this suite".)

INSERT INTO user_profiles (user_id, is_manager, detected_at, analytics_shared_with_manager) VALUES
  ('11111111-1111-4111-8111-111111111111', true,  '2024-01-01T00:00:00Z', true),
  ('22222222-2222-4222-8222-222222222222', false, '2024-01-01T00:00:00Z', true),
  ('33333333-3333-4333-8333-333333333333', false, '2024-01-01T00:00:00Z', true),
  ('44444444-4444-4444-8444-444444444444', false, '2024-01-01T00:00:00Z', true)
ON CONFLICT (user_id) DO UPDATE SET
  is_manager = EXCLUDED.is_manager,
  detected_at = EXCLUDED.detected_at,
  analytics_shared_with_manager = EXCLUDED.analytics_shared_with_manager;

-- ── Teams ────────────────────────────────────────────────────────────────────
-- Two teams with the SAME manager but DISJOINT member sets. That disjointness
-- is the whole point of the manager-roster scoping test: if
-- api/handlers_manager.go listManagerMembers() ever stops filtering by
-- ?team_id=, Beta's member shows up in Alpha's roster and the test fails.

INSERT INTO teams (id, name, created_by) VALUES
  ('aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', 'E2E Team Alpha', '11111111-1111-4111-8111-111111111111'),
  ('bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', 'E2E Team Beta',  '11111111-1111-4111-8111-111111111111');

INSERT INTO team_members (team_id, user_id, role) VALUES
  ('aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', '11111111-1111-4111-8111-111111111111', 'owner'),
  ('aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', '33333333-3333-4333-8333-333333333333', 'member'),
  ('bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', '11111111-1111-4111-8111-111111111111', 'owner'),
  ('bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', '44444444-4444-4444-8444-444444444444', 'member');

-- ── Manager roster ───────────────────────────────────────────────────────────
-- member_email must be lower(btrim(...)): migration 023 added a CHECK for it.
-- manager_team_member_assignments is what scopes a member to ONE team.

INSERT INTO manager_team_members
  (manager_user_id, member_email, member_user_id, display_name, source, cadence)
VALUES
  ('11111111-1111-4111-8111-111111111111', 'e2e.report@paceday.test',
   '33333333-3333-4333-8333-333333333333', 'E2E Alpha Report', 'manual', 'weekly'),
  ('11111111-1111-4111-8111-111111111111', 'e2e.beta@paceday.test',
   '44444444-4444-4444-8444-444444444444', 'E2E Beta Report',  'manual', 'biweekly');

INSERT INTO manager_team_member_assignments
  (manager_user_id, team_id, member_email, cadence)
VALUES
  ('11111111-1111-4111-8111-111111111111', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
   'e2e.report@paceday.test', 'weekly'),
  ('11111111-1111-4111-8111-111111111111', 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb',
   'e2e.beta@paceday.test', 'biweekly');

-- ── Analytics ────────────────────────────────────────────────────────────────
-- engine.ManagerEngine.GetMemberWeek() reads analytics_weeks directly for a
-- member who IS a Paceday user, with no calendar call at all. These rows are
-- what make the roster's focus numbers assertable to an exact value rather
-- than "some number appeared". The week_start values are filled in by
-- seed/seed.ts, which knows the current Monday — SQL alone cannot express
-- "the Monday the test will ask for" deterministically. The {{THIS_MONDAY}} /
-- {{PRIOR_MONDAY}} tokens are substituted there before the file is executed.

DELETE FROM analytics_weeks
 WHERE user_id IN (SELECT id FROM users WHERE email LIKE 'e2e.%@paceday.test');

INSERT INTO analytics_weeks (user_id, week_start, focus_minutes, meeting_minutes, free_minutes)
VALUES
  ('33333333-3333-4333-8333-333333333333', '{{THIS_MONDAY}}',  615, 240, 1545),
  ('33333333-3333-4333-8333-333333333333', '{{PRIOR_MONDAY}}', 410, 300, 1690),
  ('44444444-4444-4444-8444-444444444444', '{{THIS_MONDAY}}',  120, 900,  380),
  ('44444444-4444-4444-8444-444444444444', '{{PRIOR_MONDAY}}', 100, 880,  420);

-- team_analytics_cache would short-circuit GetMemberWeek for 4h; clear it so
-- the roster is always recomputed from analytics_weeks above.
DELETE FROM team_analytics_cache
 WHERE manager_user_id IN (SELECT id FROM users WHERE email LIKE 'e2e.%@paceday.test');

-- ── Scheduling links ─────────────────────────────────────────────────────────
-- One link per mutating test, each with a DIFFERENT host user and a DIFFERENT
-- time window, so no two booking tests can make each other fail.
--   uses_count is NOT a column: storage/scheduling_links.go computes it as
--   COUNT(bookings WHERE status <> 'cancelled'), so "exhausted" is seeded by
--   inserting a booking, not by setting a counter.
--   min_notice_minutes = 0 so every slot in the window is bookable today.

INSERT INTO scheduling_links
  (id, owner_user_id, slug, title, duration_options, days_of_week,
   window_start_time, window_end_time, buffer_before, buffer_after,
   min_notice_minutes, usage_type, max_uses, active)
VALUES
  -- Booked through the UI by an anonymous visitor.
  ('c1111111-1111-4111-8111-111111111111', '11111111-1111-4111-8111-111111111111',
   'e2e-ui-booking-30min', 'E2E UI Booking', '{30}', '{1,2,3,4,5}',
   '09:00', '11:00', 0, 0, 0, 'reusable', NULL, true),
  -- Booked through the HTTP API only.
  ('c2222222-2222-4222-8222-222222222222', '22222222-2222-4222-8222-222222222222',
   'e2e-api-booking-30min', 'E2E API Booking', '{30,60}', '{1,2,3,4,5}',
   '13:00', '15:00', 0, 0, 0, 'reusable', NULL, true),
  -- Single-use link that is ALREADY exhausted -> GET must answer 410.
  ('c3333333-3333-4333-8333-333333333333', '33333333-3333-4333-8333-333333333333',
   'e2e-exhausted-30min', 'E2E Exhausted Link', '{30}', '{1,2,3,4,5}',
   '15:00', '17:00', 0, 0, 0, 'single_use', NULL, true),
  -- Read-only link the authenticated owner sees on /app/links.
  ('c4444444-4444-4444-8444-444444444444', '11111111-1111-4111-8111-111111111111',
   'e2e-owned-45min', 'E2E Owned Link', '{45}', '{1,2,3,4,5}',
   '09:00', '17:00', 0, 0, 0, 'reusable', NULL, true);

-- Host rows. 'accepted' is what storage.GetAcceptedHosts() filters on; a link
-- with no accepted host renders with zero hosts and zero coverage.
INSERT INTO scheduling_link_hosts (link_id, user_id, status, responded_at) VALUES
  ('c1111111-1111-4111-8111-111111111111', '11111111-1111-4111-8111-111111111111', 'accepted', NOW()),
  ('c2222222-2222-4222-8222-222222222222', '22222222-2222-4222-8222-222222222222', 'accepted', NOW()),
  ('c3333333-3333-4333-8333-333333333333', '33333333-3333-4333-8333-333333333333', 'accepted', NOW()),
  ('c4444444-4444-4444-8444-444444444444', '11111111-1111-4111-8111-111111111111', 'accepted', NOW());

-- The booking that exhausts the single-use link. Dated far in the past so it
-- can never overlap a window a live test books into.
INSERT INTO bookings (id, link_id, booker_name, booker_email, start_time, end_time, status, notes)
VALUES
  ('d3333333-3333-4333-8333-333333333333', 'c3333333-3333-4333-8333-333333333333',
   'E2E Past Booker', 'past.booker@paceday.test',
   '2020-01-06T15:00:00Z', '2020-01-06T15:30:00Z', 'confirmed', 'seeded — exhausts the link');

COMMIT;
