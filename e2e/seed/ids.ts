/**
 * The single source of truth for every identifier that seed.sql inserts.
 *
 * Tests import from here rather than writing literals, so a fixture change is
 * a compile error in every test that depended on it instead of a silent
 * mismatch that turns into a flaky assertion.
 */

export const USERS = {
  /** Owner of the UI-booking link, manager of both teams, the "logged in" user. */
  primary: {
    id: '11111111-1111-4111-8111-111111111111',
    email: 'e2e.primary@paceday.test',
    name: 'E2E Primary Owner',
  },
  /** Second real user. Host of the API-booking link. Used for isolation tests. */
  second: {
    id: '22222222-2222-4222-8222-222222222222',
    email: 'e2e.second@paceday.test',
    name: 'E2E Second User',
  },
  /** Member of Team Alpha only. Host of the exhausted link. */
  alphaReport: {
    id: '33333333-3333-4333-8333-333333333333',
    email: 'e2e.report@paceday.test',
    name: 'E2E Alpha Report',
  },
  /** Member of Team Beta only. Must NEVER appear in Team Alpha's roster. */
  betaReport: {
    id: '44444444-4444-4444-8444-444444444444',
    email: 'e2e.beta@paceday.test',
    name: 'E2E Beta Report',
  },
} as const;

export const TEAMS = {
  alpha: { id: 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', name: 'E2E Team Alpha' },
  beta: { id: 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', name: 'E2E Team Beta' },
} as const;

export const LINKS = {
  /** Booked through the browser by an anonymous visitor. Window 09:00–11:00 UTC. */
  uiBooking: {
    id: 'c1111111-1111-4111-8111-111111111111',
    slug: 'e2e-ui-booking-30min',
    title: 'E2E UI Booking',
    hostUserId: USERS.primary.id,
    windowStart: '09:00',
    windowEnd: '11:00',
    durations: [30],
  },
  /** Booked through the HTTP API. Window 13:00–15:00 UTC — never overlaps uiBooking. */
  apiBooking: {
    id: 'c2222222-2222-4222-8222-222222222222',
    slug: 'e2e-api-booking-30min',
    title: 'E2E API Booking',
    hostUserId: USERS.second.id,
    windowStart: '13:00',
    windowEnd: '15:00',
    durations: [30, 60],
  },
  /** single_use with one seeded booking already present -> every read is 410 Gone. */
  exhausted: {
    id: 'c3333333-3333-4333-8333-333333333333',
    slug: 'e2e-exhausted-30min',
    title: 'E2E Exhausted Link',
    hostUserId: USERS.alphaReport.id,
  },
  /** Owned by the primary user; read-only, listed on /app/links. */
  owned: {
    id: 'c4444444-4444-4444-8444-444444444444',
    slug: 'e2e-owned-45min',
    title: 'E2E Owned Link',
    hostUserId: USERS.primary.id,
    durations: [45],
  },
} as const;

/** Exact analytics values seed.sql writes, for the manager roster assertions. */
export const ANALYTICS = {
  alphaReport: { thisWeekFocusMinutes: 615, priorWeekFocusMinutes: 410 },
  betaReport: { thisWeekFocusMinutes: 120, priorWeekFocusMinutes: 100 },
} as const;

/** A slug that is guaranteed not to exist, for the 404 path. */
export const MISSING_SLUG = 'e2e-no-such-link-ever';
