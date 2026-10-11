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

/**
 * Users WITH a stored Google credential (PAC-43). Their calendar traffic goes
 * to the stand-in (e2e/fake-google), which accepts `token` and records it as
 * `label`; "primary" means the calendar whose id is the user's email.
 *
 * One user per journey, for the same reason each booking test owns its host
 * (README rule 6): the specs run in parallel against ONE stand-in, so a
 * journey must own the calendar it seeds, the focus week it fills and the
 * label it reads the request log by. The four USERS above still have no
 * credential at all, which is what keeps AC-24's "not connected" scenarios
 * true (seed.sql, "Users").
 */
export const CONNECTED = {
  /** AC-16 (week view), AC-17 (free/busy requester), AC-21. */
  calendar: {
    id: '55555555-5555-4555-8555-555555555555',
    email: 'e2e.connected@paceday.test',
    name: 'E2E Connected User',
    token: 'e2e-connected-work',
    label: 'connected-work',
  },
  /** AC-18 and AC-19: owns the focus week (FOCUS_WEEK_OFFSET in focus-time.spec.ts). */
  focus: {
    id: '66666666-6666-4666-8666-666666666666',
    email: 'e2e.focus@paceday.test',
    name: 'E2E Focus User',
    token: 'e2e-focus-work',
    label: 'focus-work',
  },
  /** AC-20: a manager of TEAMS.connectedManager whose 1:1s are detected. */
  manager: {
    id: '77777777-7777-4777-8777-777777777777',
    email: 'e2e.manager@paceday.test',
    name: 'E2E Connected Manager',
    token: 'e2e-manager-work',
    label: 'manager-work',
  },
  /** AC-11, AC-22, AC-25 (tests/seam.spec.ts, which runs serially). */
  seam: {
    id: '88888888-8888-4888-8888-888888888888',
    email: 'e2e.seam@paceday.test',
    name: 'E2E Seam User',
    token: 'e2e-seam-work',
    label: 'seam-work',
  },
  /**
   * AC-21's second half: has a stored credential the stand-in has never
   * accepted, so a credential-keyed refusal needs no change to shared state.
   */
  rejected: {
    id: '99999999-9999-4999-8999-999999999999',
    email: 'e2e.rejected@paceday.test',
    name: 'E2E Rejected Credential',
    token: 'e2e-rejected-work',
    label: 'rejected-work',
  },
} as const;

/** The seam user's personal calendar (AC-25): a google personal_calendars row. */
export const PERSONAL_CALENDAR = {
  id: 4301,
  name: 'E2E Seam Personal',
  token: 'e2e-seam-personal',
  label: 'seam-personal',
  /** What "primary" means for the personal token, on the stand-in. */
  calendar: 'e2e.seam.personal@personal.test',
} as const;

/**
 * Every credential the stand-in accepts, installed by global setup. The
 * rejected user's token is deliberately absent.
 */
export const ACCEPTED_CREDENTIALS = [
  ...[CONNECTED.calendar, CONNECTED.focus, CONNECTED.manager, CONNECTED.seam].map((u) => ({
    token: u.token,
    label: u.label,
    calendar: u.email,
  })),
  {
    token: PERSONAL_CALENDAR.token,
    label: PERSONAL_CALENDAR.label,
    calendar: PERSONAL_CALENDAR.calendar,
  },
];

export const TEAMS = {
  alpha: { id: 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', name: 'E2E Team Alpha' },
  beta: { id: 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', name: 'E2E Team Beta' },
  /** Owned by CONNECTED.manager: the scope AC-20's detection runs in. */
  connectedManager: { id: 'cccccccc-cccc-4ccc-8ccc-cccccccccccc', name: 'E2E Team Detected' },
  /** Owned by CONNECTED.seam: AC-22's detection and team availability. */
  seam: { id: 'dddddddd-dddd-4ddd-8ddd-dddddddddddd', name: 'E2E Team Seam' },
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

/** Hosted by CONNECTED.seam: AC-22's collective slots and booking confirmation. */
export const SEAM_LINK = {
  id: 'c5555555-5555-4555-8555-555555555555',
  slug: 'e2e-seam-30min',
  title: 'E2E Seam Link',
  hostUserId: CONNECTED.seam.id,
} as const;

/** Exact analytics values seed.sql writes, for the manager roster assertions. */
export const ANALYTICS = {
  alphaReport: { thisWeekFocusMinutes: 615, priorWeekFocusMinutes: 410 },
  betaReport: { thisWeekFocusMinutes: 120, priorWeekFocusMinutes: 100 },
} as const;

/** A slug that is guaranteed not to exist, for the 404 path. */
export const MISSING_SLUG = 'e2e-no-such-link-ever';
