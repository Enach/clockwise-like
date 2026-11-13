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
  /** PAC-72 AC-20: hosts OFFERABLE_LINK; tests/public-booking.spec.ts owns its calendar. */
  offerable: {
    id: 'b3b3b3b3-b3b3-4b3b-8b3b-b3b3b3b3b3b3',
    email: 'e2e.offerable@paceday.test',
    name: 'E2E Offerable Host',
    token: 'e2e-offerable-work',
    label: 'offerable-work',
  },
  /**
   * PAC-41 AC-1, AC-14 (tests/pac41-rooms.spec.ts): books rooms on events of
   * its own calendar. The room calendars it defines are run-unique.
   */
  rooms: {
    id: 'f4f4f4f4-f4f4-4f4f-8f4f-f4f4f4f4f4f4',
    email: 'e2e.rooms@paceday.test',
    name: 'E2E Rooms User',
    token: 'e2e-rooms-work',
    label: 'rooms-work',
  },
} as const;

/**
 * PAC-35 AC-11: a Google-calendar user who disconnects their calendar in the
 * app. Owned by tests/calendar-disconnect.spec.ts alone: the journey deletes
 * this user's stored credential, so no other journey may depend on it.
 */
export const DISCONNECTING = {
  id: 'a1a1a1a1-a1a1-4a1a-8a1a-a1a1a1a1a1a1',
  email: 'e2e.disconnect@paceday.test',
  name: 'E2E Disconnect User',
  token: 'e2e-disconnect-work',
  label: 'disconnect-work',
} as const;

/**
 * PAC-81 Part A, owner decision PAC-72 D-6 (2026-11-01): a host whose calendar
 * cannot be read (not connected, provider error, slow) counts as BUSY on
 * public slot listing and booking. Every public booking link therefore needs
 * hosts whose calendars the stand-in answers. These users are those hosts,
 * one per journey (README rule 6): each owns its stand-in calendar (its email,
 * as for CONNECTED) and nothing else reads it. The four USERS stay
 * unconnected (AC-24), so they host no bookable link any more; they still own
 * the links the specs expect them to own.
 */
export const BOOKING_HOSTS = {
  /** Accepted host of LINKS.uiBooking (owned by USERS.primary). */
  ui: {
    id: 'e1e1e1e1-e1e1-4e1e-8e1e-e1e1e1e1e1e1',
    email: 'e2e.host.ui@paceday.test',
    name: 'E2E UI Booking Host',
    token: 'e2e-host-ui-work',
    label: 'host-ui-work',
  },
  /** Accepted host of LINKS.apiBooking (owned by USERS.second). */
  api: {
    id: 'e2e2e2e2-e2e2-4e2e-8e2e-e2e2e2e2e2e2',
    email: 'e2e.host.api@paceday.test',
    name: 'E2E API Booking Host',
    token: 'e2e-host-api-work',
    label: 'host-api-work',
  },
  /** Creates a link and has it booked (tests/scheduling-links.spec.ts). */
  author: {
    id: 'e3e3e3e3-e3e3-4e3e-8e3e-e3e3e3e3e3e3',
    email: 'e2e.host.author@paceday.test',
    name: 'E2E Link Author',
    token: 'e2e-host-author-work',
    label: 'host-author-work',
  },
  /** PAC-55 AC-11's host (tests/time-format.spec.ts). */
  timeFormat: {
    id: 'e4e4e4e4-e4e4-4e4e-8e4e-e4e4e4e4e4e4',
    email: 'e2e.host.timefmt@paceday.test',
    name: 'E2E Time Format Host',
    token: 'e2e-host-timefmt-work',
    label: 'host-timefmt-work',
  },
  /** The readable host of LINKS.unreadableCoHost (PAC-72 D-6's own case). */
  d6: {
    id: 'e5e5e5e5-e5e5-4e5e-8e5e-e5e5e5e5e5e5',
    email: 'e2e.host.d6@paceday.test',
    name: 'E2E D-6 Host',
    token: 'e2e-host-d6-work',
    label: 'host-d6-work',
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


export const TEAMS = {
  alpha: { id: 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', name: 'E2E Team Alpha' },
  beta: { id: 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', name: 'E2E Team Beta' },
  /** Owned by CONNECTED.manager: the scope AC-20's detection runs in. */
  connectedManager: { id: 'cccccccc-cccc-4ccc-8ccc-cccccccccccc', name: 'E2E Team Detected' },
  /** Owned by CONNECTED.seam: AC-22's detection and team availability. */
  seam: { id: 'dddddddd-dddd-4ddd-8ddd-dddddddddddd', name: 'E2E Team Seam' },
} as const;

export const LINKS = {
  /**
   * Booked through the browser by an anonymous visitor. Window 09:00–11:00 UTC.
   * Owned by USERS.primary (listed on /app/links); hosted by BOOKING_HOSTS.ui.
   */
  uiBooking: {
    id: 'c1111111-1111-4111-8111-111111111111',
    slug: 'e2e-ui-booking-30min',
    title: 'E2E UI Booking',
    ownerUserId: USERS.primary.id,
    hostUserId: BOOKING_HOSTS.ui.id,
    windowStart: '09:00',
    windowEnd: '11:00',
    durations: [30],
  },
  /**
   * Booked through the HTTP API. Window 13:00–15:00 UTC — never overlaps
   * uiBooking. Owned by USERS.second; hosted by BOOKING_HOSTS.api.
   */
  apiBooking: {
    id: 'c2222222-2222-4222-8222-222222222222',
    slug: 'e2e-api-booking-30min',
    title: 'E2E API Booking',
    ownerUserId: USERS.second.id,
    hostUserId: BOOKING_HOSTS.api.id,
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
  /**
   * PAC-72 D-6 (owner, 2026-11-01): a collective link whose second accepted
   * host (USERS.betaReport) has no calendar connected. Its calendar cannot be
   * read, so the link offers no slot and refuses every booking with 409.
   */
  unreadableCoHost: {
    id: 'c8888888-8888-4888-8888-888888888888',
    slug: 'e2e-d6-unreadable-30min',
    title: 'E2E D-6 Unreadable Co-host',
    hostUserId: BOOKING_HOSTS.d6.id,
    coHostUserId: USERS.betaReport.id,
  },
} as const;

/** Hosted by CONNECTED.seam: AC-22's collective slots and booking confirmation. */
export const SEAM_LINK = {
  id: 'c5555555-5555-4555-8555-555555555555',
  slug: 'e2e-seam-30min',
  title: 'E2E Seam Link',
  hostUserId: CONNECTED.seam.id,
} as const;

/**
 * Hosted by CONNECTED.offerable alone (PAC-72 AC-20): Monday to Friday,
 * 09:00-12:00 UTC, 30 minutes, no notice.
 */
export const OFFERABLE_LINK = {
  id: 'c9999999-9999-4999-8999-999999999999',
  slug: 'e2e-offerable-30min',
  title: 'E2E Offerable Link',
  hostUserId: CONNECTED.offerable.id,
} as const;

/**
 * PAC-81 Part B, AC-B3 (tests/csp.spec.ts). Each host owns one link and shows
 * its avatar on that link's public booking page. The image is never fetched
 * from the internet: the test intercepts the request. Connected through the
 * stand-in like BOOKING_HOSTS (PAC-72 D-6), so their pages offer slots.
 */
export const AVATAR_HOSTS = {
  /** Positive case: a Google profile picture, allowed by img-src. */
  google: {
    id: 'b1b1b1b1-b1b1-4b1b-8b1b-b1b1b1b1b1b1',
    email: 'e2e.avatar.google@paceday.test',
    name: 'E2E Avatar Google',
    avatarUrl: 'https://lh3.googleusercontent.com/a/e2e-avatar-google=s96-c',
    token: 'e2e-avatar-google-work',
    label: 'avatar-google-work',
    link: { id: 'c6666666-6666-4666-8666-666666666666', slug: 'e2e-avatar-google-30min', title: 'E2E Avatar Google Link' },
  },
  /** Negative control: an origin img-src does not allow. */
  foreign: {
    id: 'b2b2b2b2-b2b2-4b2b-8b2b-b2b2b2b2b2b2',
    email: 'e2e.avatar.foreign@paceday.test',
    name: 'E2E Avatar Foreign',
    avatarUrl: 'https://avatars.e2e-not-allowed.test/e2e-avatar-foreign.png',
    token: 'e2e-avatar-foreign-work',
    label: 'avatar-foreign-work',
    link: { id: 'c7777777-7777-4777-8777-777777777777', slug: 'e2e-avatar-foreign-30min', title: 'E2E Avatar Foreign Link' },
  },
} as const;

/**
 * PAC-88 (contract contracts/features/PAC-88.md section 6): the booking page's
 * time zone journeys. Each host is connected through the stand-in (a token it
 * does not accept is an unreadable calendar, which D-6 makes busy, so every
 * PAC-88 link would offer nothing). New York, Paris and Los Angeles carry a
 * settings row with that profile zone; the no-zone owner has NO settings row
 * (seed.sql deletes any, seed.ts verifies the absence), which is what makes
 * its link's zone UTC (PAC-72 A-4, AC-3).
 */
export const PAC88_HOSTS = {
  /** Owner and only host of PAC88_LINKS.dst (AC-19). */
  newYork: {
    id: 'b4b4b4b4-b4b4-4b4b-8b4b-b4b4b4b4b4b4',
    email: 'e2e.pac88.ny@paceday.test',
    name: 'E2E PAC-88 New York Host',
    zone: 'America/New_York',
    token: 'e2e-pac88-ny-work',
    label: 'pac88-ny-work',
  },
  /** Owner and host of PAC88_LINKS.paris (AC-1, AC-2, AC-7, AC-9, AC-10, AC-21). */
  paris: {
    id: 'b5b5b5b5-b5b5-4b5b-8b5b-b5b5b5b5b5b5',
    email: 'e2e.pac88.paris@paceday.test',
    name: 'E2E PAC-88 Paris Host',
    zone: 'Europe/Paris',
    token: 'e2e-pac88-paris-work',
    label: 'pac88-paris-work',
  },
  /** Accepted co-host of PAC88_LINKS.paris: its zone must change nothing (A-2, AC-2). */
  losAngeles: {
    id: 'b6b6b6b6-b6b6-4b6b-8b6b-b6b6b6b6b6b6',
    email: 'e2e.pac88.la@paceday.test',
    name: 'E2E PAC-88 Los Angeles Co-host',
    zone: 'America/Los_Angeles',
    token: 'e2e-pac88-la-work',
    label: 'pac88-la-work',
  },
  /** Owner and host of PAC88_LINKS.noZone: no settings row at all (A-4, AC-3). */
  noZone: {
    id: 'b7b7b7b7-b7b7-4b7b-8b7b-b7b7b7b7b7b7',
    email: 'e2e.pac88.utc@paceday.test',
    name: 'E2E PAC-88 No-zone Host',
    zone: null,
    token: 'e2e-pac88-utc-work',
    label: 'pac88-utc-work',
  },
} as const;

/**
 * PAC-88 links (contract section 6). Duration 30 only, no notice, no buffers,
 * generic refusal. Write isolation: only tests/pac88-booking.spec.ts books,
 * and only on PAC88_LINKS.paris, serially, on "booking Mondays" no read-only
 * journey displays; the dst and noZone links' calendars are written by no
 * journey.
 */
export const PAC88_LINKS = {
  /** Every day 00:00-23:59 New York time, 365-day horizon: AC-19's DST link. */
  dst: {
    id: 'cbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb',
    slug: 'e2e-pac88-dst-30min',
    title: 'E2E PAC-88 DST Link',
    ownerUserId: PAC88_HOSTS.newYork.id,
    hostUserIds: [PAC88_HOSTS.newYork.id],
    daysOfWeek: [0, 1, 2, 3, 4, 5, 6],
    windowStart: '00:00',
    windowEnd: '23:59',
    horizonDays: 365,
  },
  /**
   * Monday 09:00-17:00 Paris time, 365-day horizon, collective with a Los
   * Angeles co-host. (Not cccccccc-...: that id is TEAMS.connectedManager.)
   */
  paris: {
    id: 'cacacaca-caca-4aca-8aca-cacacacacaca',
    slug: 'e2e-pac88-paris-30min',
    title: 'E2E PAC-88 Paris Link',
    ownerUserId: PAC88_HOSTS.paris.id,
    hostUserIds: [PAC88_HOSTS.paris.id, PAC88_HOSTS.losAngeles.id],
    daysOfWeek: [1],
    windowStart: '09:00',
    windowEnd: '17:00',
    horizonDays: 365,
  },
  /** Monday to Friday 09:00-17:00 in UTC (owner without a zone), 60 days. */
  noZone: {
    id: 'cddddddd-dddd-4ddd-8ddd-dddddddddddd',
    slug: 'e2e-pac88-utc-30min',
    title: 'E2E PAC-88 No-zone Link',
    ownerUserId: PAC88_HOSTS.noZone.id,
    hostUserIds: [PAC88_HOSTS.noZone.id],
    daysOfWeek: [1, 2, 3, 4, 5],
    windowStart: '09:00',
    windowEnd: '17:00',
    horizonDays: 60,
  },
} as const;

/**
 * Every credential the stand-in accepts, installed by global setup. The
 * rejected user's token is deliberately absent.
 */
export const ACCEPTED_CREDENTIALS = [
  ...[
    CONNECTED.calendar,
    CONNECTED.focus,
    CONNECTED.manager,
    CONNECTED.seam,
    CONNECTED.offerable,
    CONNECTED.rooms,
    DISCONNECTING,
    ...Object.values(BOOKING_HOSTS),
    ...Object.values(AVATAR_HOSTS),
    ...Object.values(PAC88_HOSTS),
  ].map((u) => ({
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

/**
 * Every user and every scheduling link seed.sql inserts, derived from the
 * fixture objects above. seed.ts verify() compares the database against these
 * exact sets, so adding a fixture here (and to seed.sql) needs no count to be
 * kept in step by hand, and a row seed.sql adds without an entry here — or the
 * reverse — fails seeding.
 */
export const SEEDED_USER_IDS: readonly string[] = [
  ...Object.values(USERS).map((u) => u.id),
  ...Object.values(CONNECTED).map((u) => u.id),
  DISCONNECTING.id,
  ...Object.values(AVATAR_HOSTS).map((u) => u.id),
  ...Object.values(BOOKING_HOSTS).map((u) => u.id),
  ...Object.values(PAC88_HOSTS).map((u) => u.id),
];

export const SEEDED_LINK_IDS: readonly string[] = [
  ...Object.values(LINKS).map((l) => l.id),
  SEAM_LINK.id,
  OFFERABLE_LINK.id,
  ...Object.values(AVATAR_HOSTS).map((u) => u.link.id),
  ...Object.values(PAC88_LINKS).map((l) => l.id),
];

/**
 * Accepted host rows seed.sql inserts: one per link, plus the unconnected
 * co-host of LINKS.unreadableCoHost and the Los Angeles co-host of
 * PAC88_LINKS.paris.
 */
export const SEEDED_ACCEPTED_HOST_ROWS = SEEDED_LINK_IDS.length + 2;

/** Exact analytics values seed.sql writes, for the manager roster assertions. */
export const ANALYTICS = {
  alphaReport: { thisWeekFocusMinutes: 615, priorWeekFocusMinutes: 410 },
  betaReport: { thisWeekFocusMinutes: 120, priorWeekFocusMinutes: 100 },
} as const;

/** A slug that is guaranteed not to exist, for the 404 path. */
export const MISSING_SLUG = 'e2e-no-such-link-ever';
