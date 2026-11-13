/**
 * Applies seed/seed.sql against the already-migrated e2e database.
 *
 * Callable two ways:
 *   - from global-setup.ts (the normal path)
 *   - standalone: `npm run seed` — useful when debugging a single spec against
 *     a stack you left running.
 *
 * Idempotence is a property of seed.sql itself (it deletes before inserting),
 * so running this twice in a row is a no-op difference.
 */

import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

import { exec, query, waitForSchema, closePool } from '../db';
import { isoDate, mondayOf, DAY_MS } from '../lib/dates';
import {
  AVATAR_HOSTS,
  BOOKING_HOSTS,
  CONNECTED,
  DISCONNECTING,
  LINKS,
  PAC88_HOSTS,
  PAC88_LINKS,
  PERSONAL_CALENDAR,
  SEEDED_ACCEPTED_HOST_ROWS,
  SEEDED_LINK_IDS,
  SEEDED_USER_IDS,
  TEAMS,
  USERS,
} from './ids';

const here = dirname(fileURLToPath(import.meta.url));

export async function seed(): Promise<void> {
  const version = await waitForSchema();

  const thisMonday = mondayOf(new Date());
  const priorMonday = new Date(thisMonday.getTime() - 7 * DAY_MS);

  const raw = await readFile(join(here, 'seed.sql'), 'utf8');
  const sql = raw
    .replaceAll('{{THIS_MONDAY}}', isoDate(thisMonday))
    .replaceAll('{{PRIOR_MONDAY}}', isoDate(priorMonday));

  if (sql.includes('{{')) {
    throw new Error('seed.sql still contains an unsubstituted {{TOKEN}}');
  }

  await exec(sql);
  await verify();

  // eslint-disable-next-line no-console
  console.log(
    `[e2e seed] ok — schema version ${version}, week of ${isoDate(thisMonday)}`,
  );
}

/**
 * Fail loudly if the fixtures are not what the tests assume. Without this a
 * schema change that silently drops a seeded row turns into a confusing test
 * failure three files away instead of a clear seeding failure here.
 */
async function verify(): Promise<void> {
  const problems: string[] = [];

  // Exactly the seeded users and links, by id: the expected sets come from
  // seed/ids.ts, so a fixture added there and in seed.sql needs no count
  // updated here, and a row in one but not the other fails.
  const exactSets: Array<[string, string, unknown[], readonly string[]]> = [
    ['users', 'SELECT id::text AS id FROM users WHERE email LIKE $1', ['e2e.%@paceday.test'], SEEDED_USER_IDS],
    ['scheduling links', 'SELECT id::text AS id FROM scheduling_links WHERE slug LIKE $1', ['e2e-%'], SEEDED_LINK_IDS],
  ];
  for (const [label, sql, params, expected] of exactSets) {
    const found = (await query<{ id: string }>(sql, params)).map((r) => r.id).sort();
    const want = [...expected].sort();
    if (new Set(want).size !== want.length) {
      problems.push(`${label}: seed/ids.ts lists an id twice`);
    }
    const missing = want.filter((id) => !found.includes(id));
    const extra = found.filter((id) => !want.includes(id));
    if (missing.length > 0 || extra.length > 0) {
      problems.push(
        `${label}: expected ${want.length} row(s), found ${found.length}` +
          (missing.length ? `; missing ${missing.join(', ')}` : '') +
          (extra.length ? `; not in seed/ids.ts ${extra.join(', ')}` : ''),
      );
    }
  }

  const checks: Array<[string, string, unknown[], number]> = [
    ['teams', 'SELECT 1 FROM teams WHERE name LIKE $1', ['E2E %'], 4],
    [
      'team members',
      'SELECT 1 FROM team_members WHERE team_id = ANY($1::uuid[])',
      [[TEAMS.alpha.id, TEAMS.beta.id]],
      4,
    ],
    [
      'manager roster',
      'SELECT 1 FROM manager_team_members WHERE manager_user_id = $1',
      [USERS.primary.id],
      2,
    ],
    [
      'manager team assignments',
      'SELECT 1 FROM manager_team_member_assignments WHERE manager_user_id = $1',
      [USERS.primary.id],
      2,
    ],
    [
      'accepted hosts',
      `SELECT 1 FROM scheduling_link_hosts
        WHERE status = 'accepted' AND link_id = ANY($1::uuid[])`,
      [SEEDED_LINK_IDS],
      SEEDED_ACCEPTED_HOST_ROWS,
    ],
    // PAC-72 D-6: the D-6 link's second host is exactly the unconnected one.
    [
      'the D-6 link has its unconnected co-host',
      `SELECT 1 FROM scheduling_link_hosts
        WHERE link_id = $1 AND user_id = $2 AND status = 'accepted'`,
      [LINKS.unreadableCoHost.id, LINKS.unreadableCoHost.coHostUserId],
      1,
    ],
    // PAC-81 AC-B3: each avatar host is stored with the avatar its page shows.
    [
      'avatar hosts carry their avatar URLs',
      'SELECT 1 FROM users WHERE (id, avatar_url) IN (($1::uuid, $2), ($3::uuid, $4))',
      [
        AVATAR_HOSTS.google.id,
        AVATAR_HOSTS.google.avatarUrl,
        AVATAR_HOSTS.foreign.id,
        AVATAR_HOSTS.foreign.avatarUrl,
      ],
      2,
    ],
    [
      'exhausting booking',
      'SELECT 1 FROM bookings WHERE link_id = $1',
      [LINKS.exhausted.id],
      1,
    ],
    ['analytics weeks', 'SELECT 1 FROM analytics_weeks WHERE user_id = ANY($1::uuid[])',
      [[USERS.alphaReport.id, USERS.betaReport.id]], 4],
    // No oauth token for the four USERS is a LOAD-BEARING invariant: it is
    // what makes their calendar code paths take the fast "not connected"
    // branch (AC-24) instead of trying to reach the blackholed googleapis.com,
    // and what makes USERS.betaReport the unreadable co-host of PAC-72 D-6.
    [
      'no oauth tokens for the unconnected users (intentional)',
      'SELECT 1 FROM oauth_tokens WHERE user_id = ANY($1::uuid[])',
      [Object.values(USERS).map((u) => u.id)],
      0,
    ],
    // PAC-72 D-6: every host of a bookable link is connected through the
    // stand-in, each credential far from expiry (as for CONNECTED).
    [
      'booking and avatar hosts have one unexpired token each',
      `SELECT 1 FROM oauth_tokens
        WHERE user_id = ANY($1::uuid[]) AND expiry > NOW() + interval '1 year'`,
      [[...Object.values(BOOKING_HOSTS), ...Object.values(AVATAR_HOSTS)].map((u) => u.id)],
      Object.keys(BOOKING_HOSTS).length + Object.keys(AVATAR_HOSTS).length,
    ],
    // PAC-43: exactly the connected users have a credential, each one far
    // from expiry (a refresh would leave the test host — see seed.sql).
    [
      'connected users have one unexpired token each',
      `SELECT 1 FROM oauth_tokens
        WHERE user_id = ANY($1::uuid[]) AND expiry > NOW() + interval '1 year'`,
      [Object.values(CONNECTED).map((u) => u.id)],
      6,
    ],
    // PAC-35 AC-11: the disconnect journey's own user starts connected.
    [
      'the disconnecting user has one unexpired token',
      `SELECT 1 FROM oauth_tokens
        WHERE user_id = $1 AND expiry > NOW() + interval '1 year'`,
      [DISCONNECTING.id],
      1,
    ],
    [
      'the seam user has one google personal calendar',
      `SELECT 1 FROM personal_calendars
        WHERE id = $1 AND user_id = $2 AND provider = 'google' AND enabled`,
      [PERSONAL_CALENDAR.id, CONNECTED.seam.id],
      1,
    ],
    [
      'the deployment-wide provider setting is google (AC-17 precondition)',
      `SELECT 1 FROM settings WHERE calendar_provider <> 'google'`,
      [],
      0,
    ],
    // A focus block is busy time for its owner, and the e2e users host the
    // booking links, so a leftover block would silently shrink slot lists.
    ['no focus blocks', 'SELECT 1 FROM focus_blocks', [], 0],
    // PAC-88 (contract section 6): each zoned host carries exactly its zone,
    // the no-zone owner has no settings row at all (a stale one would change
    // AC-3 silently), every PAC-88 host is connected through the stand-in,
    // and the Paris link is collective with the Los Angeles co-host (AC-2).
    [
      'PAC-88 hosts carry their profile zones',
      `SELECT 1 FROM settings WHERE (user_id, timezone) IN
         (($1::uuid, $2), ($3::uuid, $4), ($5::uuid, $6))`,
      [
        PAC88_HOSTS.newYork.id, PAC88_HOSTS.newYork.zone,
        PAC88_HOSTS.paris.id, PAC88_HOSTS.paris.zone,
        PAC88_HOSTS.losAngeles.id, PAC88_HOSTS.losAngeles.zone,
      ],
      3,
    ],
    [
      'the PAC-88 no-zone owner has no settings row',
      'SELECT 1 FROM settings WHERE user_id = $1',
      [PAC88_HOSTS.noZone.id],
      0,
    ],
    [
      'PAC-88 hosts have one unexpired token each',
      `SELECT 1 FROM oauth_tokens
        WHERE user_id = ANY($1::uuid[]) AND expiry > NOW() + interval '1 year'`,
      [Object.values(PAC88_HOSTS).map((u) => u.id)],
      Object.keys(PAC88_HOSTS).length,
    ],
    [
      'PAC-88 links carry their horizons, days and windows',
      `SELECT 1 FROM scheduling_links
        WHERE (id, booking_horizon_days, days_of_week, window_start_time, window_end_time) IN
          (($1::uuid, $2::int, $3::int[], $4::time, $5::time), ($6::uuid, $7::int, $8::int[], $9::time, $10::time),
           ($11::uuid, $12::int, $13::int[], $14::time, $15::time))`,
      Object.values(PAC88_LINKS).flatMap((l) => [
        l.id, l.horizonDays, [...l.daysOfWeek], l.windowStart, l.windowEnd,
      ]),
      3,
    ],
    [
      'the PAC-88 Paris link has its Los Angeles co-host',
      `SELECT 1 FROM scheduling_link_hosts
        WHERE link_id = $1 AND user_id = ANY($2::uuid[]) AND status = 'accepted'`,
      [PAC88_LINKS.paris.id, [...PAC88_LINKS.paris.hostUserIds]],
      2,
    ],
  ];

  for (const [label, sql, params, expected] of checks) {
    const rows = await query(sql, params);
    if (rows.length !== expected) {
      problems.push(`${label}: expected ${expected} row(s), found ${rows.length}`);
    }
  }
  if (problems.length > 0) {
    throw new Error(`[e2e seed] fixture verification failed:\n  - ${problems.join('\n  - ')}`);
  }
}

// Standalone entrypoint (`npm run seed`). Deliberately an exact basename match:
// a loose `includes('seed')` would also fire when Playwright imports this
// module from global-setup, seeding twice.
const invokedDirectly =
  !!process.argv[1] && /(^|[\\/])seed\.ts$/.test(process.argv[1]);
if (invokedDirectly) {
  seed()
    .then(() => closePool())
    .catch(async (e) => {
      // eslint-disable-next-line no-console
      console.error(e);
      await closePool();
      process.exit(1);
    });
}
