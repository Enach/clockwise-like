/**
 * Brings the stack up, waits for it, and seeds it.
 *
 * Why globalSetup and not playwright's `webServer`: `webServer` is built around
 * one command that owns one port and is killed when the run ends. This stack is
 * four containers, and a developer debugging a failure wants to leave it up.
 * globalSetup runs `docker compose up -d --wait`, and global-teardown.ts tears
 * it down unless E2E_KEEP_STACK=1.
 *
 * Set E2E_SKIP_COMPOSE=1 to run against a stack you started yourself. Setup
 * still waits for health and re-seeds, so the run is deterministic either way.
 */

import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';

import { BASE_URL } from './auth';
import { seed } from './seed/seed';
import { closePool } from './db';

const here = dirname(fileURLToPath(import.meta.url));
export const REPO_ROOT = resolve(here, '..');
export const COMPOSE_FILE = 'e2e/docker-compose.e2e.yml';
export const COMPOSE_PROJECT = 'paceday-e2e';

export function composeArgs(...rest: string[]): string[] {
  return ['compose', '-p', COMPOSE_PROJECT, '-f', COMPOSE_FILE, ...rest];
}

export function runCompose(args: string[], timeoutMs = 15 * 60_000): Promise<void> {
  return new Promise((resolvePromise, reject) => {
    const child = spawn('docker', args, {
      cwd: REPO_ROOT,
      stdio: 'inherit',
      env: process.env,
    });
    const timer = setTimeout(() => {
      child.kill('SIGKILL');
      reject(new Error(`docker ${args.join(' ')} timed out after ${timeoutMs}ms`));
    }, timeoutMs);
    child.on('error', (e) => {
      clearTimeout(timer);
      reject(
        new Error(
          `could not run docker (${e.message}). The e2e suite needs a working Docker daemon; ` +
            `see e2e/README.md "Prerequisites".`,
        ),
      );
    });
    child.on('exit', (code) => {
      clearTimeout(timer);
      if (code === 0) resolvePromise();
      else reject(new Error(`docker ${args.join(' ')} exited with ${code}`));
    });
  });
}

/**
 * Poll GET /api/health through nginx. This is the readiness signal the factory
 * spec names, and going through nginx means it also proves the /api/ proxy rule
 * in nginx/nginx.conf is wired correctly before a single test runs.
 */
async function waitForHealth(timeoutMs = 180_000): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  let lastError = 'no attempt made';
  while (Date.now() < deadline) {
    try {
      const res = await fetch(`${BASE_URL}/api/health`, {
        signal: AbortSignal.timeout(3_000),
      });
      if (res.ok) {
        const body = (await res.json()) as { status?: string };
        if (body.status === 'ok') return;
        lastError = `unexpected body ${JSON.stringify(body)}`;
      } else {
        lastError = `HTTP ${res.status}`;
      }
    } catch (e) {
      lastError = e instanceof Error ? e.message : String(e);
    }
    await new Promise((r) => setTimeout(r, 1_000));
  }
  throw new Error(
    `${BASE_URL}/api/health did not become healthy within ${timeoutMs}ms (last: ${lastError}).\n` +
      `Inspect with:  docker compose -p ${COMPOSE_PROJECT} -f ${COMPOSE_FILE} logs backend nginx`,
  );
}

/** The SPA must be served too, or every UI test fails with a blank page. */
async function waitForFrontend(timeoutMs = 60_000): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  let lastError = 'no attempt made';
  while (Date.now() < deadline) {
    try {
      const res = await fetch(BASE_URL, { signal: AbortSignal.timeout(3_000) });
      const html = await res.text();
      if (res.ok && html.includes('<div id="root"')) return;
      lastError = res.ok ? 'served HTML without the SPA root element' : `HTTP ${res.status}`;
    } catch (e) {
      lastError = e instanceof Error ? e.message : String(e);
    }
    await new Promise((r) => setTimeout(r, 1_000));
  }
  throw new Error(
    `${BASE_URL} did not serve the SPA within ${timeoutMs}ms (last: ${lastError}).\n` +
      `The frontend image is built from <repo-root>/frontend — see e2e/README.md "Prerequisites".`,
  );
}

export default async function globalSetup(): Promise<void> {
  if (process.env.E2E_SKIP_COMPOSE !== '1') {
    // --wait blocks until every service with a healthcheck is healthy; the
    // explicit polls below cover the services that have none.
    await runCompose(composeArgs('up', '-d', '--build', '--wait'));
  }

  await waitForHealth();
  await waitForFrontend();
  await seed();
  await closePool();
}
