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
import { LINKS, TEAMS, USERS } from './ids';

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
  const checks: Array<[string, string, unknown[], number]> = [
    ['users', 'SELECT 1 FROM users WHERE email LIKE $1', ['e2e.%@paceday.test'], 4],
    ['teams', 'SELECT 1 FROM teams WHERE name LIKE $1', ['E2E %'], 2],
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
    ['scheduling links', 'SELECT 1 FROM scheduling_links WHERE slug LIKE $1', ['e2e-%'], 4],
    [
      'accepted hosts',
      `SELECT 1 FROM scheduling_link_hosts
        WHERE status = 'accepted' AND link_id = ANY($1::uuid[])`,
      [[LINKS.uiBooking.id, LINKS.apiBooking.id, LINKS.exhausted.id, LINKS.owned.id]],
      4,
    ],
    [
      'exhausting booking',
      'SELECT 1 FROM bookings WHERE link_id = $1',
      [LINKS.exhausted.id],
      1,
    ],
    ['analytics weeks', 'SELECT 1 FROM analytics_weeks WHERE user_id = ANY($1::uuid[])',
      [[USERS.alphaReport.id, USERS.betaReport.id]], 4],
    // No oauth token for any seeded user is a LOAD-BEARING invariant: it is
    // what makes every calendar code path take its fast "not connected" branch
    // instead of trying to reach the blackholed googleapis.com.
    [
      'no oauth tokens (intentional)',
      'SELECT 1 FROM oauth_tokens WHERE user_id = ANY($1::uuid[])',
      [[USERS.primary.id, USERS.second.id, USERS.alphaReport.id, USERS.betaReport.id]],
      0,
    ],
    // Focus blocks are global busy time for booking (storage.ListFocusBlocksForWeek
    // takes no user id), so a leftover block would silently shrink slot lists.
    ['no focus blocks', 'SELECT 1 FROM focus_blocks', [], 0],
  ];

  const problems: string[] = [];
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
