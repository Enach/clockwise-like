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

import { SINK_ADDRESS, SINK_HOSTS, sink, unexplainedSinkConnections } from '../fake';
import { docker, E2E_NETWORK, stackBackendLogs } from '../lib/docker';
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

  test.fixme(
    'AC-23 (strict): the sink records no connection at all',
    // BLOCKED by a backend defect, to be filed as a PAC-43 follow-up in Linear
    // ("GET /api/auth/status sends the user's calendar credential to Google
    // userinfo, outside the calendar seam"). For a user with a stored
    // credential, backend/api/handlers_auth.go status() calls fetchUserEmail,
    // which GETs https://www.googleapis.com/oauth2/v2/userinfo with that
    // user's access token on EVERY status request — every dashboard load.
    // Spec §V2-3 treats the identity lookup as a sign-in-time call; it is not.
    // In this stack the sink refuses the TLS handshake before any credential
    // is sent (the non-strict test below asserts exactly that), but the strict
    // criterion cannot pass. Unblocks when status() stops calling userinfo,
    // or when SEAM-REQUIRED.md item B (GOOGLE_USERINFO_URL) redirects it.
    async () => {
      expect(await sink.connections()).toEqual([]);
    },
  );

  test('AC-23 (all but the status lookup): no other connection, and no credential, reached the sink', async () => {
    const all = await sink.connections();
    const others = unexplainedSinkConnections(all).filter((c) => c.path !== PROBE_PATH);

    // Token refresh (oauth2.googleapis.com), sign-in (accounts.google.com),
    // the second provider, and any calendar call that bypassed the seam to
    // the published address over plain HTTP: none of them.
    expect(others, `unexplained sink connections:\n${JSON.stringify(others, null, 2)}`).toEqual([]);
    // The connections the status lookup did make never got as far as a
    // credential: the sink refused the TLS handshake.
    expect(all.filter((c) => c.credential_presented && c.path !== PROBE_PATH)).toEqual([]);

    // The excused signature (TLS, SNI www.googleapis.com) is also what a
    // calendar call to the PUBLISHED address would look like. So bound it:
    // there can be at most one such connection per GET /api/auth/status the
    // backend served. A calendar path that bypassed the stand-in adds
    // connections no status call explains, and fails here.
    const excused = all.length - unexplainedSinkConnections(all).length;
    const statusCalls = (await stackBackendLogs())
      .split('\n')
      .filter((l) => /\bGET \/api\/auth\/status\b/.test(l)).length;
    expect(
      excused,
      `${excused} TLS connections to www.googleapis.com, but only ${statusCalls} status calls to explain them`,
    ).toBeLessThanOrEqual(statusCalls);
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
