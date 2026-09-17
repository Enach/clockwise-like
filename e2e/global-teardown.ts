/**
 * Tears the stack down, unless the developer asked to keep it.
 *
 * E2E_KEEP_STACK=1 leaves the containers running so a failure can be
 * investigated against the exact state that produced it — the database is on
 * tmpfs, so `docker compose down` destroys the evidence irrecoverably.
 */

import { closePool } from './db';
import { COMPOSE_FILE, COMPOSE_PROJECT, composeArgs, runCompose } from './global-setup';

export default async function globalTeardown(): Promise<void> {
  await closePool();

  if (process.env.E2E_KEEP_STACK === '1' || process.env.E2E_SKIP_COMPOSE === '1') {
    // eslint-disable-next-line no-console
    console.log(
      `[e2e] stack left running. Inspect:  docker compose -p ${COMPOSE_PROJECT} -f ${COMPOSE_FILE} logs -f\n` +
        `[e2e] tear down:                  docker compose -p ${COMPOSE_PROJECT} -f ${COMPOSE_FILE} down -v`,
    );
    return;
  }

  // -v removes anonymous volumes; the database itself is tmpfs so nothing
  // survives either way.
  await runCompose(composeArgs('down', '-v', '--remove-orphans'), 5 * 60_000);
}
