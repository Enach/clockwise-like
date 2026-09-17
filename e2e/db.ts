/**
 * Direct Postgres access for seeding and for assertions that must look at the
 * row the backend actually wrote.
 *
 * Why the tests are allowed to touch the database at all: the factory rule is
 * "no test may pass when the feature is broken" (docs/factory/README.md §3).
 * A booking test that only checks the confirmation screen would still pass if
 * the handler returned a fabricated 201 without persisting. Reading the
 * bookings row closes that hole. The database is never used to FAKE a
 * behaviour — only to verify one, and only after the HTTP call that should
 * have caused it.
 */

import { Pool, type QueryResultRow } from 'pg';

export const DATABASE_URL =
  process.env.E2E_DATABASE_URL ??
  'postgres://paceday_e2e:paceday_e2e@127.0.0.1:15433/paceday_e2e';

let pool: Pool | undefined;

function getPool(): Pool {
  if (!pool) {
    pool = new Pool({ connectionString: DATABASE_URL, max: 4 });
  }
  return pool;
}

export async function query<T extends QueryResultRow = QueryResultRow>(
  sql: string,
  params: unknown[] = [],
): Promise<T[]> {
  const res = await getPool().query<T>(sql, params);
  return res.rows;
}

export async function queryOne<T extends QueryResultRow = QueryResultRow>(
  sql: string,
  params: unknown[] = [],
): Promise<T | undefined> {
  return (await query<T>(sql, params))[0];
}

/** Run a whole script as one statement batch (used for seed.sql). */
export async function exec(sql: string): Promise<void> {
  const client = await getPool().connect();
  try {
    await client.query(sql);
  } finally {
    client.release();
  }
}

export async function closePool(): Promise<void> {
  if (pool) {
    await pool.end();
    pool = undefined;
  }
}

/**
 * Wait until the backend has finished golang-migrate. The migration table is a
 * better readiness signal than /api/health for the seeder, because health
 * answers on a router that is registered after storage.Open() returns — but
 * only the schema tells us WHICH migration version we are seeding against.
 */
export async function waitForSchema(timeoutMs = 120_000): Promise<number> {
  const deadline = Date.now() + timeoutMs;
  let lastError: unknown;
  while (Date.now() < deadline) {
    try {
      const row = await queryOne<{ version: string; dirty: boolean }>(
        'SELECT version, dirty FROM schema_migrations LIMIT 1',
      );
      if (row && !row.dirty) {
        // scheduling_links.min_notice_minutes arrives in migration 019 and the
        // manager assignment CHECKs in 023; if the newest thing this suite
        // needs is missing, the stack is older than the harness.
        const col = await queryOne(
          `SELECT 1 FROM information_schema.columns
            WHERE table_name = 'manager_team_member_assignments'
              AND column_name = 'cadence'`,
        );
        if (col) return Number(row.version);
      }
      if (row?.dirty) {
        throw new Error(
          `migrations are dirty at version ${row.version} — the backend failed mid-migration`,
        );
      }
    } catch (e) {
      lastError = e;
      if (e instanceof Error && e.message.includes('dirty')) throw e;
    }
    await new Promise((r) => setTimeout(r, 500));
  }
  throw new Error(
    `schema not ready after ${timeoutMs}ms (last error: ${String(lastError)}). ` +
      `Is the backend container up? Try: docker compose -p paceday-e2e -f e2e/docker-compose.e2e.yml logs backend`,
  );
}
