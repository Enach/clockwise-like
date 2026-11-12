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

-- A stray focus block is busy time for its owner, and the e2e users host the
-- booking links, so a leftover would shrink slot lists. Clear them all.
DELETE FROM focus_blocks;

DELETE FROM users WHERE email LIKE 'e2e.%@paceday.test';

-- ── Users ────────────────────────────────────────────────────────────────────
-- The first four users have NO oauth_tokens row, and that is deliberate:
--   * auth.LoadUserToken() returning nil is the cheap, deterministic "calendar
--     not connected" path. Every calendar-touching code path checks it first
--     and skips the network entirely.
--   * AC-24's scenarios (tests/calendar.spec.ts "the disconnected state",
--     tests/focus-time.spec.ts "fails loudly") depend on it.
--
-- The five 5…–9… users DO have one (PAC-43). Their calendar traffic goes to
-- the stand-in (e2e/fake-google), which accepts the access tokens below (see
-- seed/ids.ts ACCEPTED_CREDENTIALS) — except the 9… user's, which it never
-- accepts (AC-21). Each journey owns one of them; see seed/ids.ts CONNECTED.
-- Their expiry is far in the future ON PURPOSE: auth.TokenSource refreshes
-- only an invalid token, and a refresh would go to oauth2.googleapis.com,
-- which is mapped to the recording sink — an AC-23 failure that would look
-- like a broken seam.
--
-- PAC-81 Part A, owner decision PAC-72 D-6 (2026-11-01): a public booking
-- link's host whose calendar cannot be read counts as BUSY, so a link hosted
-- by an unconnected user offers no slot and refuses bookings (409). The
-- bookable links are therefore hosted by the e1…–e5… users (seed/ids.ts
-- BOOKING_HOSTS) and the avatar hosts, all connected through the stand-in
-- like the 5… users; the four 1…–4… users keep no credential (AC-24) and
-- host no bookable link (the exhausted link answers 410 before any calendar
-- read; the owned link is never booked).

INSERT INTO users (id, email, name, avatar_url, provider, provider_id) VALUES
  ('11111111-1111-4111-8111-111111111111', 'e2e.primary@paceday.test', 'E2E Primary Owner',  '', 'google', 'e2e-primary'),
  ('22222222-2222-4222-8222-222222222222', 'e2e.second@paceday.test',  'E2E Second User',    '', 'google', 'e2e-second'),
  ('33333333-3333-4333-8333-333333333333', 'e2e.report@paceday.test',  'E2E Alpha Report',   '', 'google', 'e2e-report'),
  ('44444444-4444-4444-8444-444444444444', 'e2e.beta@paceday.test',    'E2E Beta Report',    '', 'google', 'e2e-beta'),
  ('55555555-5555-4555-8555-555555555555', 'e2e.connected@paceday.test', 'E2E Connected User',      '', 'google', 'e2e-connected'),
  ('66666666-6666-4666-8666-666666666666', 'e2e.focus@paceday.test',     'E2E Focus User',          '', 'google', 'e2e-focus'),
  ('77777777-7777-4777-8777-777777777777', 'e2e.manager@paceday.test',   'E2E Connected Manager',   '', 'google', 'e2e-manager'),
  ('88888888-8888-4888-8888-888888888888', 'e2e.seam@paceday.test',      'E2E Seam User',           '', 'google', 'e2e-seam'),
  ('99999999-9999-4999-8999-999999999999', 'e2e.rejected@paceday.test',  'E2E Rejected Credential', '', 'google', 'e2e-rejected'),
  -- PAC-35 AC-11: disconnects its own calendar, so no other journey loses one.
  ('a1a1a1a1-a1a1-4a1a-8a1a-a1a1a1a1a1a1', 'e2e.disconnect@paceday.test', 'E2E Disconnect User',     '', 'google', 'e2e-disconnect'),
  -- PAC-81 AC-B3 (tests/csp.spec.ts): hosts whose avatar is shown on their
  -- public booking page. The Google one must load under the CSP; the other is
  -- the negative control and must be blocked (img-src). seed/ids.ts AVATAR_HOSTS.
  ('b1b1b1b1-b1b1-4b1b-8b1b-b1b1b1b1b1b1', 'e2e.avatar.google@paceday.test',  'E2E Avatar Google',
   'https://lh3.googleusercontent.com/a/e2e-avatar-google=s96-c', 'google', 'e2e-avatar-google'),
  ('b2b2b2b2-b2b2-4b2b-8b2b-b2b2b2b2b2b2', 'e2e.avatar.foreign@paceday.test', 'E2E Avatar Foreign',
   'https://avatars.e2e-not-allowed.test/e2e-avatar-foreign.png', 'google', 'e2e-avatar-foreign'),
  -- PAC-72 D-6: connected hosts of the bookable links (seed/ids.ts BOOKING_HOSTS).
  ('e1e1e1e1-e1e1-4e1e-8e1e-e1e1e1e1e1e1', 'e2e.host.ui@paceday.test',      'E2E UI Booking Host',  '', 'google', 'e2e-host-ui'),
  ('e2e2e2e2-e2e2-4e2e-8e2e-e2e2e2e2e2e2', 'e2e.host.api@paceday.test',     'E2E API Booking Host', '', 'google', 'e2e-host-api'),
  ('e3e3e3e3-e3e3-4e3e-8e3e-e3e3e3e3e3e3', 'e2e.host.author@paceday.test',  'E2E Link Author',      '', 'google', 'e2e-host-author'),
  ('e4e4e4e4-e4e4-4e4e-8e4e-e4e4e4e4e4e4', 'e2e.host.timefmt@paceday.test', 'E2E Time Format Host', '', 'google', 'e2e-host-timefmt'),
  ('e5e5e5e5-e5e5-4e5e-8e5e-e5e5e5e5e5e5', 'e2e.host.d6@paceday.test',      'E2E D-6 Host',         '', 'google', 'e2e-host-d6'),
  -- PAC-72 AC-20: hosts the offerable-start link; its calendar holds the event
  -- an unoffered start overlaps.
  ('b3b3b3b3-b3b3-4b3b-8b3b-b3b3b3b3b3b3', 'e2e.offerable@paceday.test', 'E2E Offerable Host',      '', 'google', 'e2e-offerable');

-- PAC-71: every seeded user is bound to a Google identity with authoritative
-- evidence, so its address is domain-confirmed and the journeys that resolve an
-- address to a person (co-hosts, manager roster, team invites) find it. The
-- subjects are decimal strings, the Google format (contract section 3 item 6).
UPDATE users SET address_standing = 'domain_confirmed' WHERE email LIKE 'e2e.%@paceday.test';
INSERT INTO user_identities (user_id, provider, tenant, subject, evidence, bound_by)
SELECT id, 'google', '', '71' || lpad((row_number() OVER (ORDER BY id))::text, 6, '0'), 'authoritative', 'created'
FROM users WHERE email LIKE 'e2e.%@paceday.test';

-- ON DELETE CASCADE from users removes these on the next run.
INSERT INTO oauth_tokens (user_id, access_token, refresh_token, expiry, calendar_id) VALUES
  ('55555555-5555-4555-8555-555555555555', 'e2e-connected-work', 'e2e-refresh-never-used', '2099-01-01T00:00:00Z', 'primary'),
  ('66666666-6666-4666-8666-666666666666', 'e2e-focus-work',     'e2e-refresh-never-used', '2099-01-01T00:00:00Z', 'primary'),
  ('77777777-7777-4777-8777-777777777777', 'e2e-manager-work',   'e2e-refresh-never-used', '2099-01-01T00:00:00Z', 'primary'),
  ('88888888-8888-4888-8888-888888888888', 'e2e-seam-work',      'e2e-refresh-never-used', '2099-01-01T00:00:00Z', 'primary'),
  ('99999999-9999-4999-8999-999999999999', 'e2e-rejected-work',  'e2e-refresh-never-used', '2099-01-01T00:00:00Z', 'primary'),
  ('a1a1a1a1-a1a1-4a1a-8a1a-a1a1a1a1a1a1', 'e2e-disconnect-work', 'e2e-refresh-never-used', '2099-01-01T00:00:00Z', 'primary'),
  -- PAC-72 D-6: the booking and avatar hosts (seed/ids.ts ACCEPTED_CREDENTIALS).
  ('e1e1e1e1-e1e1-4e1e-8e1e-e1e1e1e1e1e1', 'e2e-host-ui-work',        'e2e-refresh-never-used', '2099-01-01T00:00:00Z', 'primary'),
  ('e2e2e2e2-e2e2-4e2e-8e2e-e2e2e2e2e2e2', 'e2e-host-api-work',       'e2e-refresh-never-used', '2099-01-01T00:00:00Z', 'primary'),
  ('e3e3e3e3-e3e3-4e3e-8e3e-e3e3e3e3e3e3', 'e2e-host-author-work',    'e2e-refresh-never-used', '2099-01-01T00:00:00Z', 'primary'),
  ('e4e4e4e4-e4e4-4e4e-8e4e-e4e4e4e4e4e4', 'e2e-host-timefmt-work',   'e2e-refresh-never-used', '2099-01-01T00:00:00Z', 'primary'),
  ('e5e5e5e5-e5e5-4e5e-8e5e-e5e5e5e5e5e5', 'e2e-host-d6-work',        'e2e-refresh-never-used', '2099-01-01T00:00:00Z', 'primary'),
  ('b1b1b1b1-b1b1-4b1b-8b1b-b1b1b1b1b1b1', 'e2e-avatar-google-work',  'e2e-refresh-never-used', '2099-01-01T00:00:00Z', 'primary'),
  ('b2b2b2b2-b2b2-4b2b-8b2b-b2b2b2b2b2b2', 'e2e-avatar-foreign-work', 'e2e-refresh-never-used', '2099-01-01T00:00:00Z', 'primary'),
  ('b3b3b3b3-b3b3-4b3b-8b3b-b3b3b3b3b3b3', 'e2e-offerable-work', 'e2e-refresh-never-used', '2099-01-01T00:00:00Z', 'primary');

-- AC-25: a google personal calendar for the seam user. credentials_json is an
-- oauth2.Token as backend/calendar/personal_reader.go tokenFromJSON reads it.
-- The fixed id is far above anything the SERIAL would hand out in a run.
INSERT INTO personal_calendars (id, user_id, provider, name, credentials_json, enabled) VALUES
  (4301, '88888888-8888-4888-8888-888888888888', 'google', 'E2E Seam Personal',
   '{"access_token":"e2e-seam-personal","token_type":"Bearer","expiry":"2099-01-01T00:00:00Z"}', true);

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
  ('44444444-4444-4444-8444-444444444444', 'UTC', '09:00', '18:00', 240, 'google'),
  ('55555555-5555-4555-8555-555555555555', 'UTC', '09:00', '18:00', 240, 'google'),
  ('66666666-6666-4666-8666-666666666666', 'UTC', '09:00', '18:00', 240, 'google'),
  ('77777777-7777-4777-8777-777777777777', 'UTC', '09:00', '18:00', 240, 'google'),
  ('88888888-8888-4888-8888-888888888888', 'UTC', '09:00', '18:00', 240, 'google'),
  ('99999999-9999-4999-8999-999999999999', 'UTC', '09:00', '18:00', 240, 'google'),
  ('a1a1a1a1-a1a1-4a1a-8a1a-a1a1a1a1a1a1', 'UTC', '09:00', '18:00', 240, 'google'),
  -- PAC-72 revision 2 (D-9): OFFERABLE_LINK's owner. A link's days and hours
  -- are in its owner's profile zone; this owner's is stored explicitly (UTC,
  -- so the journeys' UTC instants keep their meaning) to exercise the read of
  -- a real settings row. Owners without a row are UTC as well (A-4).
  ('b3b3b3b3-b3b3-4b3b-8b3b-b3b3b3b3b3b3', 'UTC', '09:00', '18:00', 240, 'google')
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
  ('44444444-4444-4444-8444-444444444444', false, '2024-01-01T00:00:00Z', true),
  ('55555555-5555-4555-8555-555555555555', false, '2024-01-01T00:00:00Z', true),
  ('66666666-6666-4666-8666-666666666666', false, '2024-01-01T00:00:00Z', true),
  ('77777777-7777-4777-8777-777777777777', true,  '2024-01-01T00:00:00Z', true),
  ('88888888-8888-4888-8888-888888888888', true,  '2024-01-01T00:00:00Z', true),
  ('99999999-9999-4999-8999-999999999999', false, '2024-01-01T00:00:00Z', true),
  ('a1a1a1a1-a1a1-4a1a-8a1a-a1a1a1a1a1a1', false, '2024-01-01T00:00:00Z', true)
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
  ('bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', 'E2E Team Beta',  '11111111-1111-4111-8111-111111111111'),
  -- PAC-43: the connected manager's own team (AC-20 detects into it, so it
  -- starts with no roster) and the seam user's (AC-22).
  ('cccccccc-cccc-4ccc-8ccc-cccccccccccc', 'E2E Team Detected', '77777777-7777-4777-8777-777777777777'),
  ('dddddddd-dddd-4ddd-8ddd-dddddddddddd', 'E2E Team Seam',     '88888888-8888-4888-8888-888888888888');

INSERT INTO team_members (team_id, user_id, role) VALUES
  ('aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', '11111111-1111-4111-8111-111111111111', 'owner'),
  ('aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', '33333333-3333-4333-8333-333333333333', 'member'),
  ('bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', '11111111-1111-4111-8111-111111111111', 'owner'),
  ('bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', '44444444-4444-4444-8444-444444444444', 'member'),
  ('cccccccc-cccc-4ccc-8ccc-cccccccccccc', '77777777-7777-4777-8777-777777777777', 'owner'),
  ('dddddddd-dddd-4ddd-8ddd-dddddddddddd', '88888888-8888-4888-8888-888888888888', 'owner');

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
   '09:00', '17:00', 0, 0, 0, 'reusable', NULL, true),
  -- PAC-43 AC-22: hosted by the seam user, whose calendar is on the stand-in,
  -- so its slots and its booking confirmation reach the stand-in.
  ('c5555555-5555-4555-8555-555555555555', '88888888-8888-4888-8888-888888888888',
   'e2e-seam-30min', 'E2E Seam Link', '{30}', '{1,2,3,4,5}',
   '09:00', '17:00', 0, 0, 0, 'reusable', NULL, true),
  -- PAC-81 AC-B3: read-only public pages showing each avatar host.
  ('c6666666-6666-4666-8666-666666666666', 'b1b1b1b1-b1b1-4b1b-8b1b-b1b1b1b1b1b1',
   'e2e-avatar-google-30min', 'E2E Avatar Google Link', '{30}', '{1,2,3,4,5}',
   '09:00', '17:00', 0, 0, 0, 'reusable', NULL, true),
  ('c7777777-7777-4777-8777-777777777777', 'b2b2b2b2-b2b2-4b2b-8b2b-b2b2b2b2b2b2',
   'e2e-avatar-foreign-30min', 'E2E Avatar Foreign Link', '{30}', '{1,2,3,4,5}',
   '09:00', '17:00', 0, 0, 0, 'reusable', NULL, true),
  -- PAC-72 D-6: a collective link with a co-host whose calendar is not
  -- connected (USERS.betaReport): no slot, every booking 409.
  ('c8888888-8888-4888-8888-888888888888', 'e5e5e5e5-e5e5-4e5e-8e5e-e5e5e5e5e5e5',
   'e2e-d6-unreadable-30min', 'E2E D-6 Unreadable Co-host', '{30}', '{1,2,3,4,5}',
   '09:00', '17:00', 0, 0, 0, 'reusable', NULL, true),
  -- PAC-72 AC-20: hosted by the offerable host alone, whose calendar is on the
  -- stand-in, so an event there makes a start unofferable.
  ('c9999999-9999-4999-8999-999999999999', 'b3b3b3b3-b3b3-4b3b-8b3b-b3b3b3b3b3b3',
   'e2e-offerable-30min', 'E2E Offerable Link', '{30}', '{1,2,3,4,5}',
   '09:00', '12:00', 0, 0, 0, 'reusable', NULL, true);

-- Host rows. 'accepted' is what storage.GetAcceptedHosts() filters on; a link
-- with no accepted host renders with zero hosts and zero coverage. The UI and
-- API booking links are owned by the primary and second users but hosted by
-- connected users (PAC-72 D-6; seed/ids.ts BOOKING_HOSTS).
INSERT INTO scheduling_link_hosts (link_id, user_id, status, responded_at) VALUES
  ('c1111111-1111-4111-8111-111111111111', 'e1e1e1e1-e1e1-4e1e-8e1e-e1e1e1e1e1e1', 'accepted', NOW()),
  ('c2222222-2222-4222-8222-222222222222', 'e2e2e2e2-e2e2-4e2e-8e2e-e2e2e2e2e2e2', 'accepted', NOW()),
  ('c3333333-3333-4333-8333-333333333333', '33333333-3333-4333-8333-333333333333', 'accepted', NOW()),
  ('c4444444-4444-4444-8444-444444444444', '11111111-1111-4111-8111-111111111111', 'accepted', NOW()),
  ('c5555555-5555-4555-8555-555555555555', '88888888-8888-4888-8888-888888888888', 'accepted', NOW()),
  ('c6666666-6666-4666-8666-666666666666', 'b1b1b1b1-b1b1-4b1b-8b1b-b1b1b1b1b1b1', 'accepted', NOW()),
  ('c7777777-7777-4777-8777-777777777777', 'b2b2b2b2-b2b2-4b2b-8b2b-b2b2b2b2b2b2', 'accepted', NOW()),
  ('c8888888-8888-4888-8888-888888888888', 'e5e5e5e5-e5e5-4e5e-8e5e-e5e5e5e5e5e5', 'accepted', NOW()),
  ('c8888888-8888-4888-8888-888888888888', '44444444-4444-4444-8444-444444444444', 'accepted', NOW()),
  ('c9999999-9999-4999-8999-999999999999', 'b3b3b3b3-b3b3-4b3b-8b3b-b3b3b3b3b3b3', 'accepted', NOW());

-- The booking that exhausts the single-use link. Dated far in the past so it
-- can never overlap a window a live test books into.
INSERT INTO bookings (id, link_id, booker_name, booker_email, start_time, end_time, status, notes)
VALUES
  ('d3333333-3333-4333-8333-333333333333', 'c3333333-3333-4333-8333-333333333333',
   'E2E Past Booker', 'past.booker@paceday.test',
   '2020-01-06T15:00:00Z', '2020-01-06T15:30:00Z', 'confirmed', 'seeded — exhausts the link');

-- ── PAC-88: booking page time zone (contract PAC-88 section 6) ───────────────
-- Four hosts connected through the stand-in (seed/ids.ts PAC88_HOSTS, all in
-- ACCEPTED_CREDENTIALS: an unaccepted token is an unreadable calendar, which
-- PAC-72 D-6 makes busy, and every PAC-88 link would offer nothing).
INSERT INTO users (id, email, name, avatar_url, provider, provider_id) VALUES
  ('b4b4b4b4-b4b4-4b4b-8b4b-b4b4b4b4b4b4', 'e2e.pac88.ny@paceday.test',    'E2E PAC-88 New York Host',       '', 'google', 'e2e-pac88-ny'),
  ('b5b5b5b5-b5b5-4b5b-8b5b-b5b5b5b5b5b5', 'e2e.pac88.paris@paceday.test', 'E2E PAC-88 Paris Host',          '', 'google', 'e2e-pac88-paris'),
  ('b6b6b6b6-b6b6-4b6b-8b6b-b6b6b6b6b6b6', 'e2e.pac88.la@paceday.test',    'E2E PAC-88 Los Angeles Co-host', '', 'google', 'e2e-pac88-la'),
  ('b7b7b7b7-b7b7-4b7b-8b7b-b7b7b7b7b7b7', 'e2e.pac88.utc@paceday.test',   'E2E PAC-88 No-zone Host',        '', 'google', 'e2e-pac88-utc');

INSERT INTO oauth_tokens (user_id, access_token, refresh_token, expiry, calendar_id) VALUES
  ('b4b4b4b4-b4b4-4b4b-8b4b-b4b4b4b4b4b4', 'e2e-pac88-ny-work',    'e2e-refresh-never-used', '2099-01-01T00:00:00Z', 'primary'),
  ('b5b5b5b5-b5b5-4b5b-8b5b-b5b5b5b5b5b5', 'e2e-pac88-paris-work', 'e2e-refresh-never-used', '2099-01-01T00:00:00Z', 'primary'),
  ('b6b6b6b6-b6b6-4b6b-8b6b-b6b6b6b6b6b6', 'e2e-pac88-la-work',    'e2e-refresh-never-used', '2099-01-01T00:00:00Z', 'primary'),
  ('b7b7b7b7-b7b7-4b7b-8b7b-b7b7b7b7b7b7', 'e2e-pac88-utc-work',   'e2e-refresh-never-used', '2099-01-01T00:00:00Z', 'primary');

-- The three zoned hosts' profile zones (PAC-72 D-9: a link's days and hours
-- are its owner's zone; the LA co-host's zone must change nothing, A-2).
INSERT INTO settings (user_id, timezone, work_start, work_end, focus_daily_target_minutes, calendar_provider)
VALUES
  ('b4b4b4b4-b4b4-4b4b-8b4b-b4b4b4b4b4b4', 'America/New_York',    '09:00', '18:00', 240, 'google'),
  ('b5b5b5b5-b5b5-4b5b-8b5b-b5b5b5b5b5b5', 'Europe/Paris',        '09:00', '18:00', 240, 'google'),
  ('b6b6b6b6-b6b6-4b6b-8b6b-b6b6b6b6b6b6', 'America/Los_Angeles', '09:00', '18:00', 240, 'google')
ON CONFLICT (user_id) DO UPDATE SET
  timezone = EXCLUDED.timezone,
  work_start = EXCLUDED.work_start,
  work_end = EXCLUDED.work_end,
  focus_daily_target_minutes = EXCLUDED.focus_daily_target_minutes,
  calendar_provider = EXCLUDED.calendar_provider;

-- Stale-row guard: the no-zone owner must have NO settings row (A-4: its
-- link's zone is UTC). Anything that created one (a settings read through the
-- app creates defaults) would silently change AC-3. seed.ts verifies it.
DELETE FROM settings WHERE user_id = 'b7b7b7b7-b7b7-4b7b-8b7b-b7b7b7b7b7b7';

-- The links name booking_horizon_days explicitly (the other seeded links keep
-- the migration's default, 60). Monday-only Paris and every-day New York run
-- 365 days so AC-10's winter Monday and AC-19's next transitions are inside.
INSERT INTO scheduling_links
  (id, owner_user_id, slug, title, duration_options, days_of_week,
   window_start_time, window_end_time, buffer_before, buffer_after,
   min_notice_minutes, usage_type, max_uses, active, booking_horizon_days)
VALUES
  ('cbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', 'b4b4b4b4-b4b4-4b4b-8b4b-b4b4b4b4b4b4',
   'e2e-pac88-dst-30min', 'E2E PAC-88 DST Link', '{30}', '{0,1,2,3,4,5,6}',
   '00:00', '23:59', 0, 0, 0, 'reusable', NULL, true, 365),
  ('cacacaca-caca-4aca-8aca-cacacacacaca', 'b5b5b5b5-b5b5-4b5b-8b5b-b5b5b5b5b5b5',
   'e2e-pac88-paris-30min', 'E2E PAC-88 Paris Link', '{30}', '{1}',
   '09:00', '17:00', 0, 0, 0, 'reusable', NULL, true, 365),
  ('cddddddd-dddd-4ddd-8ddd-dddddddddddd', 'b7b7b7b7-b7b7-4b7b-8b7b-b7b7b7b7b7b7',
   'e2e-pac88-utc-30min', 'E2E PAC-88 No-zone Link', '{30}', '{1,2,3,4,5}',
   '09:00', '17:00', 0, 0, 0, 'reusable', NULL, true, 60);

INSERT INTO scheduling_link_hosts (link_id, user_id, status, responded_at) VALUES
  ('cbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', 'b4b4b4b4-b4b4-4b4b-8b4b-b4b4b4b4b4b4', 'accepted', NOW()),
  ('cacacaca-caca-4aca-8aca-cacacacacaca', 'b5b5b5b5-b5b5-4b5b-8b5b-b5b5b5b5b5b5', 'accepted', NOW()),
  ('cacacaca-caca-4aca-8aca-cacacacacaca', 'b6b6b6b6-b6b6-4b6b-8b6b-b6b6b6b6b6b6', 'accepted', NOW()),
  ('cddddddd-dddd-4ddd-8ddd-dddddddddddd', 'b7b7b7b7-b7b7-4b7b-8b7b-b7b7b7b7b7b7', 'accepted', NOW());

COMMIT;
