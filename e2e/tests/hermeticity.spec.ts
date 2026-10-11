/**
 * PAC-43 AC-23: nothing reached a real provider during the run.
 *
 * Inside the backend container every real provider host maps to the recording
 * sink (docker-compose.e2e.yml extra_hosts). Token refresh, sign-in and the
 * identity lookup are not redirected to the stand-in (spec §V2-3), so this
 * mapping is what keeps a refresh credential on the test host, and the sink's
 * log is the evidence that it held.
 *
 * This file is the `hermeticity` project, the teardown of the `stand-in` setup
 * project (playwright.config.ts): Playwright runs it after every test in
 * every project that depends on `stand-in` has finished, pass or fail, so it
 * reads the sink over the whole run.
 */

import { test, expect } from '@playwright/test';

import { SINK_ADDRESS, SINK_HOSTS, sink } from '../fake';
import { docker, E2E_NETWORK } from '../lib/docker';
import { COMPOSE_FILE, COMPOSE_PROJECT } from '../global-setup';

/** The positive control's own request, recognisable in the sink log. */
const PROBE_PATH = '/__pac43-sink-probe__';

test.describe.configure({ mode: 'default' });

test.describe('AC-23 — the recording sink after the whole run', () => {
  test('every provider host name maps to the sink inside the backend container', async () => {
    const id = await docker(['compose', '-p', COMPOSE_PROJECT, '-f', COMPOSE_FILE, 'ps', '-q', 'backend']);
    expect(id.code, id.stderr).toBe(0);
    const inspect = await docker(['inspect', id.stdout.trim(), '--format', '{{json .HostConfig.ExtraHosts}}']);
    expect(inspect.code, inspect.stderr).toBe(0);
    const mapped = (JSON.parse(inspect.stdout) as string[]).slice().sort();
    expect(mapped).toEqual(SINK_HOSTS.map((h) => `${h}:${SINK_ADDRESS}`).sort());
  });

  // AC-23: nothing in the run reached a real Google host. Before PAC-65 the
  // status endpoint called Google userinfo on every dashboard load and this
  // had to excuse those connections; it no longer does, so none are excused.
  // The only connections allowed are the positive control's own probe.
  test('AC-23: the sink records no connection at all', async () => {
    const all = await sink.connections();
    const escaped = all.filter((c) => c.path !== PROBE_PATH);
    expect(escaped, `connections reached the sink:
${JSON.stringify(escaped, null, 2)}`).toEqual([]);
  });

  test('positive control: the sink records a credentialed request that reaches it', async () => {
    // From a throwaway container on the stack's network, mapped exactly like
    // the backend. Run AFTER the reads above, so it cannot pollute them.
    const before = (await sink.connections()).length;
    const probe = await docker([
      'run', '--rm', '--network', E2E_NETWORK,
      '--add-host', `www.googleapis.com:${SINK_ADDRESS}`,
      'paceday-e2e-sink:local', '/sink', '-probe', `http://www.googleapis.com${PROBE_PATH}`,
    ]);
    expect(probe.code, probe.stdout + probe.stderr).toBe(0);
    const added = (await sink.connections()).slice(before);
    expect(added).toEqual([
      expect.objectContaining({
        listener: ':80',
        host: 'www.googleapis.com',
        path: PROBE_PATH,
        credential_presented: true,
      }),
    ]);
  });
});
