/**
 * PAC-43 group D, and AC-3: what the SHIPPED BINARY does at startup.
 *
 * The configuration decision has its own table tests (backend/config). These
 * scenarios exist because a validator's return value proves nothing about the
 * process: main.go could ignore the error, call the decision after it starts
 * listening, or never call it. So each case starts the image global setup
 * built from backend/ as a bare container on the e2e network and observes the
 * process: its exit status, whether /api/health ever answered on its port,
 * and what it printed.
 *
 * Every refusal case uses a base environment that DOES boot (the positive
 * control below proves it, with a valid database, secret and test encryption
 * key), so a refusal can only come from the calendar settings — never from a
 * missing DATABASE_URL, JWT_SECRET or PACEDAY_ENCRYPTION_KEY (PAC-84 AC-8b:
 * each refusal case also checks its output does not name the key variable).
 *
 * No page, no browser. These run in the normal suite, so `make e2e` (which
 * runs `npm test`) is their gate (plan §2.4, §7 item 7).
 */

import { readFile } from 'node:fs/promises';
import { join } from 'node:path';

import { test, expect } from '@playwright/test';

import {
  E2E_DATABASE_URL_IN_NETWORK,
  productionReferenceEnvironment,
  stackBackendLogs,
  startBackend,
} from '../lib/docker';
import { BASE_URL } from '../auth';
import { REPO_ROOT } from '../global-setup';
import {
  ALLOW_TEST_KEY,
  E2E_ENCRYPTION_KEY,
  RUNBOOK_ENCRYPTION_KEY,
} from '../lib/encryption';

const ADDRESS = 'GOOGLE_CALENDAR_API_BASE_URL';
const DECLARATION = 'PACEDAY_ALLOW_STANDINS';
const VALID_STANDIN = 'http://fake-google:8080/calendar/v3/';
const ENCRYPTION_KEY = 'PACEDAY_ENCRYPTION_KEY';

/** Everything main.go needs to boot, and nothing calendar-related. */
const BOOTABLE = {
  PORT: '8080',
  DATABASE_URL: E2E_DATABASE_URL_IN_NETWORK,
  JWT_SECRET: 'e2e-startup-spec-only',
  // PAC-84 (contract section 11): the committed e2e test key, and the opt-in
  // without which the backend refuses those key bytes (AC-30).
  [ENCRYPTION_KEY]: E2E_ENCRYPTION_KEY,
  [ALLOW_TEST_KEY]: 'true',
  TZ: 'UTC',
};

// Host ports for the bare containers: one per case, so the cases can run in
// parallel with each other and with the stack (8088, 15433, 18090, 18091).
const PORT = {
  ac3: 18101,
  control: 18102,
  ac12: 18103,
  ac12False: 18104,
  ac13Scheme: 18105,
  ac13Userinfo: 18106,
  ac13Public: 18107,
  ac13LinkLocal: 18108,
  ac13Zone: 18109,
  ac26: 18110,
  pac84NoKey: 18111,
  pac84Malformed: 18112,
  pac84TestKey: 18113,
};

const uniq = (label: string) => `pac43-startup-${label}-${process.pid}`;

/** A refused start: non-zero exit, never served, never reached the listener. */
function expectRefused(r: Awaited<ReturnType<typeof startBackend>>) {
  expect(r.exitCode, `the process must exit; output:\n${r.output}`).not.toBeNull();
  expect(r.exitCode).not.toBe(0);
  expect(r.served, 'GET /api/health must never have answered').toBe(false);
  // main.go prints this immediately before http.ListenAndServe.
  expect(r.output).not.toContain('server listening');
  // And the refusal precedes the database: nothing was migrated or opened.
  expect(r.output).toContain('refusing to start');
}

/**
 * PAC-84 AC-8b: a refusal case that is not about the encryption key must not
 * be refused for the key — its output never names the key variable.
 */
function expectRefusedForOwnReason(r: Awaited<ReturnType<typeof startBackend>>) {
  expectRefused(r);
  expect(r.output, 'refused for the encryption key instead of its own reason').not.toContain(ENCRYPTION_KEY);
}

test.describe('startup — the production reference environment', () => {
  test('AC-3: the built image boots and serves health with docker-compose.prod.yml\'s backend environment', async () => {
    const env = await productionReferenceEnvironment();

    // The input is what the spec says it is: production's own block, which
    // sets neither new variable and no proxy.
    expect(Object.keys(env)).toContain('JWT_SECRET');
    expect(env).not.toHaveProperty(ADDRESS);
    expect(env).not.toHaveProperty(DECLARATION);
    for (const proxy of ['HTTP_PROXY', 'HTTPS_PROXY', 'NO_PROXY', 'http_proxy', 'https_proxy']) {
      expect(env).not.toHaveProperty(proxy);
    }

    // The one substitution: main.go fatals without a reachable database, so
    // DATABASE_URL points at the e2e postgres on the stack's network.
    env.DATABASE_URL = E2E_DATABASE_URL_IN_NETWORK;

    // PAC-84 (AC-8b, contract section 11): production gets the encryption key
    // from encryption.env through the backend's env_file, never from its
    // environment: block, so the reference environment cannot carry it. The
    // test adds the committed e2e key (and the opt-in it needs) explicitly.
    env[ENCRYPTION_KEY] = E2E_ENCRYPTION_KEY;
    env[ALLOW_TEST_KEY] = 'true';

    const r = await startBackend(uniq('ac3'), env, PORT.ac3);
    expect(r.served, `it must answer /api/health; output:\n${r.output}`).toBe(true);
    expect(r.output).toContain('server listening on :8080');
    // Production prints no redirection notice.
    expect(r.output).not.toContain('calendar traffic is redirected');
  });

  test('PAC-84 AC-8b/AC-30: docker-compose.prod.yml hands the backend its key through encryption.env only', async () => {
    const text = (await readFile(join(REPO_ROOT, 'docker-compose.prod.yml'), 'utf8')).replace(/\r/g, '');
    // The backend service's lines, comments dropped: what compose reads.
    const lines = text.split('\n');
    const start = lines.indexOf('  backend:');
    expect(start, 'docker-compose.prod.yml has no backend service').toBeGreaterThanOrEqual(0);
    const block: string[] = [];
    for (const line of lines.slice(start + 1)) {
      if (/^ {0,2}\S/.test(line)) break;
      if (line.trim().startsWith('#')) continue;
      block.push(line.replace(/\s+#.*$/, ''));
    }
    const live = block.join('\n');
    expect(live).toContain(['    env_file:', '      - path: ./encryption.env', '        required: false'].join('\n'));
    const envStart = block.indexOf('    environment:');
    expect(envStart).toBeGreaterThanOrEqual(0);
    const environment: string[] = [];
    for (const line of block.slice(envStart + 1)) {
      if (!line.startsWith('      ')) break;
      environment.push(line);
    }
    expect(environment.join('\n')).toContain('JWT_SECRET');
    const uncommentedText = lines.filter((l) => !l.trim().startsWith('#')).map((l) => l.replace(/\s+#.*$/, '')).join('\n');
    for (const name of [ENCRYPTION_KEY, `${ENCRYPTION_KEY}_PREVIOUS`, ALLOW_TEST_KEY]) {
      expect(environment.join('\n'), `the backend environment: must not name ${name}`).not.toContain(name);
      expect(uncommentedText, `docker-compose.prod.yml must not name ${name}`).not.toContain(`${name}:`);
      expect(uncommentedText).not.toContain(`${name}=`);
    }
  });
});

test.describe('startup — a redirected deployment', () => {
  test('positive control: the refusal cases\' base environment boots with a valid redirection', async () => {
    const r = await startBackend(uniq('control'), {
      ...BOOTABLE,
      [DECLARATION]: 'true',
      [ADDRESS]: VALID_STANDIN,
    }, PORT.control);
    expect(r.served, `output:\n${r.output}`).toBe(true);
  });

  test('AC-14: the running stack announces the redirection, its target, and that it is not production', async ({
    request,
  }) => {
    // It serves...
    const health = await request.get(`${BASE_URL}/api/health`);
    expect(health.status()).toBe(200);

    // ...and said so before it started serving.
    const logs = await stackBackendLogs();
    const notice = logs.split('\n').find((l) => l.includes('calendar traffic is redirected'));
    expect(notice, `backend output:\n${logs}`).toBeDefined();
    expect(notice).toContain(VALID_STANDIN);
    expect(notice).toMatch(/not a production deployment/i);
    expect(logs.indexOf('calendar traffic is redirected')).toBeLessThan(
      logs.indexOf('server listening'),
    );
  });
});

test.describe('startup — refusals', () => {
  test('AC-12: an address without the test-mode declaration refuses to boot, naming both settings', async () => {
    const r = await startBackend(uniq('ac12'), { ...BOOTABLE, [ADDRESS]: VALID_STANDIN }, PORT.ac12);
    expectRefusedForOwnReason(r);
    expect(r.output).toContain(ADDRESS);
    expect(r.output).toContain(DECLARATION);
  });

  test('AC-12: a declaration that is not true counts as no declaration', async () => {
    const r = await startBackend(uniq('ac12-false'), {
      ...BOOTABLE,
      [DECLARATION]: 'false',
      [ADDRESS]: VALID_STANDIN,
    }, PORT.ac12False);
    expectRefusedForOwnReason(r);
    expect(r.output).toContain(ADDRESS);
    expect(r.output).toContain(DECLARATION);
  });

  // AC-13, one case per refusal class: AC-5 (unintelligible: scheme,
  // userinfo), AC-15 (non-permitted literal: public, link-local) and AC-6's
  // zone-scoped row.
  const refused: Array<[keyof typeof PORT, string, string]> = [
    ['ac13Scheme', 'AC-5 scheme', 'ftp://fake-google:8080/calendar/v3/'],
    ['ac13Userinfo', 'AC-5 userinfo', 'http://fake-google@evil.example.com/calendar/v3/'],
    ['ac13Public', 'AC-15 public literal', 'http://8.8.8.8:8080/calendar/v3/'],
    ['ac13LinkLocal', 'AC-15 link-local literal', 'http://169.254.169.254/calendar/v3/'],
    ['ac13Zone', 'AC-6 zone-scoped literal', 'http://[::1%25lo]:8080/calendar/v3/'],
  ];
  for (const [port, label, value] of refused) {
    test(`AC-13: ${label} refuses to boot, naming the value`, async () => {
      const r = await startBackend(uniq(port.toLowerCase()), {
        ...BOOTABLE,
        [DECLARATION]: 'true',
        [ADDRESS]: value,
      }, PORT[port]);
      expectRefusedForOwnReason(r);
      expect(r.output).toContain(value);
    });
  }

  test('AC-26: an address without the API root refuses to boot, naming the value and /calendar/v3/', async () => {
    const value = 'http://fake-google:8080/calendar/v3';
    const r = await startBackend(uniq('ac26'), {
      ...BOOTABLE,
      [DECLARATION]: 'true',
      [ADDRESS]: value,
    }, PORT.ac26);
    expectRefusedForOwnReason(r);
    expect(r.output).toContain(`"${value}"`);
    expect(r.output).toContain('/calendar/v3/');
    expect(r.output).toMatch(/trailing slash/);
  });
});

test.describe('startup — encryption key refusals (PAC-84)', () => {
  test('AC-8: no key refuses before listening, naming the variable and the runbook section', async () => {
    const env: Record<string, string> = { ...BOOTABLE };
    delete env[ENCRYPTION_KEY];
    const r = await startBackend(uniq('pac84-nokey'), env, PORT.pac84NoKey);
    expectRefused(r);
    expect(r.output).toContain(ENCRYPTION_KEY);
    expect(r.output).toContain(RUNBOOK_ENCRYPTION_KEY);
    // Refused before the database: nothing was migrated.
    expect(r.output).not.toContain('encryption: key');
  });

  test('AC-9: a malformed key refuses, naming the problem and never echoing the input', async () => {
    const body = 'bm90LTMyLWJ5dGVzLW9mLWtleQ==';
    const r = await startBackend(uniq('pac84-malformed'), {
      ...BOOTABLE,
      [ENCRYPTION_KEY]: `devkey:${body}`,
    }, PORT.pac84Malformed);
    expectRefused(r);
    expect(r.output).toContain(`${ENCRYPTION_KEY} is malformed`);
    expect(r.output).toContain('need 32');
    expect(r.output).toContain(RUNBOOK_ENCRYPTION_KEY);
    expect(r.output).not.toContain(body);
    expect(r.output).not.toContain(Buffer.from(body, 'base64').toString('utf8'));
  });

  test('AC-30: the committed e2e key without the opt-in refuses', async () => {
    const env: Record<string, string> = { ...BOOTABLE };
    delete env[ALLOW_TEST_KEY];
    const r = await startBackend(uniq('pac84-testkey'), env, PORT.pac84TestKey);
    expectRefused(r);
    expect(r.output).toContain(`${ENCRYPTION_KEY} is the committed e2e test key "e2e-test"; generate a real key`);
    expect(r.output).not.toContain(E2E_ENCRYPTION_KEY.split(':')[1]);
  });
});
