/**
 * Running the built backend image as a bare process, for the startup
 * criteria (PAC-43 AC-3, AC-12, AC-13, AC-14, AC-26). These observe a
 * process — did it exit, did it ever answer on its port, what did it print —
 * which a unit test of the configuration decision cannot.
 */

import { spawn } from 'node:child_process';
import { readFile } from 'node:fs/promises';
import { join } from 'node:path';

import { COMPOSE_FILE, COMPOSE_PROJECT, REPO_ROOT } from '../global-setup';
import { SINK_ADDRESS, SINK_HOSTS } from '../fake';

/** The image global setup builds from backend/ (compose project + service). */
export const BACKEND_IMAGE = process.env.E2E_BACKEND_IMAGE ?? `${COMPOSE_PROJECT}-backend`;
/** The pinned e2e network (docker-compose.e2e.yml `networks.default.name`). */
export const E2E_NETWORK = 'paceday-e2e-net';
/** The e2e postgres, as seen from a container on E2E_NETWORK. */
export const E2E_DATABASE_URL_IN_NETWORK =
  'postgres://paceday_e2e:paceday_e2e@postgres:5432/paceday_e2e?sslmode=disable';

export interface DockerResult {
  code: number;
  stdout: string;
  stderr: string;
}

export function docker(args: string[], timeoutMs = 120_000): Promise<DockerResult> {
  return new Promise((resolve, reject) => {
    const child = spawn('docker', args, { cwd: REPO_ROOT, env: process.env });
    let stdout = '';
    let stderr = '';
    child.stdout.on('data', (d) => (stdout += d));
    child.stderr.on('data', (d) => (stderr += d));
    const timer = setTimeout(() => {
      child.kill('SIGKILL');
      reject(new Error(`docker ${args.join(' ')} timed out after ${timeoutMs}ms`));
    }, timeoutMs);
    child.on('error', (e) => {
      clearTimeout(timer);
      reject(e);
    });
    child.on('exit', (code) => {
      clearTimeout(timer);
      resolve({ code: code ?? -1, stdout, stderr });
    });
  });
}

/**
 * The production reference environment (spec §V3-1): the backend service's
 * `environment:` block of docker-compose.prod.yml, rendered by compose itself
 * with every `${…}` filled by a placeholder. Read from the file on every run,
 * so a change to the production file is a change to this test's input.
 */
export async function productionReferenceEnvironment(): Promise<Record<string, string>> {
  const file = join(REPO_ROOT, 'docker-compose.prod.yml');
  // A Windows checkout may carry CRLF; the block regex below is line-based.
  const text = (await readFile(file, 'utf8')).replace(/\r\n/g, '\n');
  // Only the backend service's variables get placeholders; the others (a TLS
  // directory, for instance) keep their defaults so the file still renders.
  const block = /^ {2}backend:\n([\s\S]*?)^ {2}\S/m.exec(text)?.[1];
  if (!block) throw new Error('docker-compose.prod.yml has no backend service block');
  const placeholders: Record<string, string> = {};
  for (const m of block.matchAll(/\$\{([A-Za-z_][A-Za-z0-9_]*)/g)) {
    const name = m[1] as string;
    placeholders[name] = `placeholder-${name.toLowerCase()}`;
  }
  const res = await new Promise<DockerResult>((resolve, reject) => {
    const child = spawn(
      'docker',
      ['compose', '-f', 'docker-compose.prod.yml', 'config', '--format', 'json'],
      { cwd: REPO_ROOT, env: { ...process.env, ...placeholders } },
    );
    let stdout = '';
    let stderr = '';
    child.stdout.on('data', (d) => (stdout += d));
    child.stderr.on('data', (d) => (stderr += d));
    child.on('error', reject);
    child.on('exit', (code) => resolve({ code: code ?? -1, stdout, stderr }));
  });
  if (res.code !== 0) {
    throw new Error(`docker compose config failed (${res.code}): ${res.stderr}`);
  }
  const config = JSON.parse(res.stdout) as {
    services: { backend: { environment?: Record<string, string | null> } };
  };
  const env: Record<string, string> = {};
  for (const [k, v] of Object.entries(config.services.backend.environment ?? {})) {
    env[k] = v ?? '';
  }
  return env;
}

export interface StartedBackend {
  /** Exit status, or null if it was still running when we stopped it. */
  exitCode: number | null;
  /** Everything it printed (stdout + stderr). */
  output: string;
  /** Whether GET /api/health ever answered 200 while it ran. */
  served: boolean;
}

/**
 * Start the backend image as a bare container on the e2e network, with
 * exactly `env`, published on 127.0.0.1:`hostPort`, and probe its health
 * endpoint until it either answers or the process exits.
 *
 * The provider host names map to the recording sink, exactly as in the stack,
 * so a process started here can never reach a real provider either (AC-23
 * covers it too).
 */
export async function startBackend(
  name: string,
  env: Record<string, string>,
  hostPort: number,
  opts: { untilServedOrMs?: number } = {},
): Promise<StartedBackend> {
  const limit = opts.untilServedOrMs ?? 60_000;
  const args = ['run', '-d', '--name', name, '--network', E2E_NETWORK, '-p', `127.0.0.1:${hostPort}:8080`];
  for (const h of SINK_HOSTS) args.push('--add-host', `${h}:${SINK_ADDRESS}`);
  for (const [k, v] of Object.entries(env)) args.push('-e', `${k}=${v}`);
  args.push(BACKEND_IMAGE);

  await docker(['rm', '-f', name]);
  const run = await docker(args);
  if (run.code !== 0) {
    throw new Error(`docker run ${name} failed: ${run.stderr}`);
  }

  let served = false;
  let exited = false;
  const waiter = docker(['wait', name], limit + 30_000).then((r) => {
    exited = true;
    return r;
  });

  const deadline = Date.now() + limit;
  while (!exited && !served && Date.now() < deadline) {
    try {
      const res = await fetch(`http://127.0.0.1:${hostPort}/api/health`, {
        signal: AbortSignal.timeout(1_000),
      });
      if (res.ok) served = true;
    } catch {
      // Not listening (yet, or ever). Keep probing until it exits.
    }
    if (!served && !exited) await new Promise((r) => setTimeout(r, 250));
  }

  let exitCode: number | null = null;
  if (exited) {
    exitCode = Number((await waiter).stdout.trim());
  }
  const logs = await docker(['logs', name]);
  await docker(['rm', '-f', name]);
  if (!exited) await waiter.catch(() => undefined);
  return { exitCode, output: logs.stdout + logs.stderr, served };
}

/** The running stack's backend output. */
export async function stackBackendLogs(): Promise<string> {
  const res = await docker([
    'compose', '-p', COMPOSE_PROJECT, '-f', COMPOSE_FILE, 'logs', '--no-color', '--no-log-prefix', 'backend',
  ]);
  if (res.code !== 0) throw new Error(`docker compose logs failed: ${res.stderr}`);
  return res.stdout + res.stderr;
}
