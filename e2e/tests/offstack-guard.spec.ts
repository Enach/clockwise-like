/**
 * The off-stack guard (fixtures.ts `offStackRequests`, lib/offstack.ts) is
 * itself tested: a guard that never fires would let a journey reach a real
 * provider again without anyone noticing (CI run 44422703172 loaded the real
 * Google sign-in page from pac98-signin-personal.spec.ts).
 *
 * Every off-stack host here is under .invalid (RFC 2606), which never
 * resolves, so even a broken guard sends nothing anywhere.
 */
import http from 'node:http';
import type { AddressInfo } from 'node:net';

import { test, expect, OFF_STACK_REQUESTS_EXPECTED } from '../fixtures';
import { BASE_URL } from '../auth';
import { OFF_STACK_ALLOWLIST, hostResolverRules, isOnStack } from '../lib/offstack';

function expectOffStack(): void {
  test.info().annotations.push({
    type: OFF_STACK_REQUESTS_EXPECTED,
    description: 'the guard self-test asserts on its own off-stack requests',
  });
}

test.describe('off-stack guard self-test', () => {
  test('the allowlist is empty and the stack is what the suite is configured on', () => {
    expect(OFF_STACK_ALLOWLIST).toEqual([]);
    for (const url of [BASE_URL, 'http://127.0.0.1:18090/x', 'http://[::1]:1/', 'http://app.localhost/', 'data:,x', 'about:blank']) {
      expect(isOnStack(url), url).toBe(true);
    }
    for (const url of [
      'https://accounts.google.com/o/oauth2/v2/auth',
      'https://www.gstatic.com/_/mss/boq-identity/x.js',
      'https://login.microsoftonline.com/common/oauth2/v2.0/authorize',
      'https://slack.com/oauth/v2/authorize',
      'https://zoom.us/oauth/authorize',
      'http://8.8.8.8/',
      'http://localhost.evil.invalid/',
    ]) {
      expect(isOnStack(url), url).toBe(false);
    }
    expect(hostResolverRules()).toMatch(/^MAP \* ~NOTFOUND, EXCLUDE localhost/);
  });

  test('a request to an off-stack origin is aborted and recorded', async ({ page, offStackRequests }) => {
    expectOffStack();
    const failure = await page.goto('http://off-stack.e2e.invalid/start').then(
      () => null,
      (e: Error) => e,
    );
    expect(failure?.message).toMatch(/ERR_BLOCKED_BY_CLIENT/);
    expect(offStackRequests).toEqual([{ url: 'http://off-stack.e2e.invalid/start', how: 'routed' }]);
  });

  test('the off-stack hop of a redirect is recorded and never connects', async ({
    page,
    offStackRequests,
  }) => {
    expectOffStack();
    // A local server whose only answer is a redirect off the stack, like a
    // provider start's 302. Playwright routes the first URL only, so this hop
    // reaches the network layer, where the resolver rule refuses its name.
    let hops = 0;
    const server = http.createServer((_req, res) => {
      hops += 1;
      res.writeHead(302, { Location: 'http://redirect-hop.e2e.invalid/landing' });
      res.end();
    });
    await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
    const { port } = server.address() as AddressInfo;
    const start = `http://127.0.0.1:${port}/start`;
    try {
      const failure = await page.goto(start).then(
        () => null,
        (e: Error) => e,
      );
      expect(hops, 'the local server answered the redirect').toBe(1);
      // The resolver refused the hop's host: nothing connected to it.
      expect(failure?.message).toMatch(/ERR_NAME_NOT_RESOLVED/);
      expect(offStackRequests).toEqual([{ url: 'http://redirect-hop.e2e.invalid/landing', how: 'redirect' }]);
    } finally {
      await new Promise<void>((resolve) => server.close(() => resolve()));
    }
  });

  test('the resolver refuses an off-stack address itself, not only names that do not exist', async ({
    page,
    offStackRequests,
  }) => {
    expectOffStack();
    // 192.0.2.1 is TEST-NET-1 (RFC 5737): never routed anywhere. Without the
    // resolver rule a redirect hop to it would try to connect (and time out
    // or be unreachable); with the rule Chromium refuses it at once as
    // ERR_NAME_NOT_RESOLVED, which is what proves the rule is in force.
    const server = http.createServer((_req, res) => {
      res.writeHead(302, { Location: 'http://192.0.2.1/landing' });
      res.end();
    });
    await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
    const { port } = server.address() as AddressInfo;
    try {
      const failure = await page.goto(`http://127.0.0.1:${port}/start`).then(
        () => null,
        (e: Error) => e,
      );
      expect(failure?.message).toMatch(/ERR_NAME_NOT_RESOLVED/);
      expect(offStackRequests).toEqual([{ url: 'http://192.0.2.1/landing', how: 'redirect' }]);
    } finally {
      await new Promise<void>((resolve) => server.close(() => resolve()));
    }
  });

  test('a page.route that lets an off-stack request through is refused and recorded', async ({
    page,
    offStackRequests,
  }) => {
    expectOffStack();
    // route.continue() sends the request to the network without the context
    // route ever seeing it; the resolver is what stops it.
    await page.route('http://192.0.2.1/**', (route) => route.continue());
    const failure = await page.goto('http://192.0.2.1/continued').then(
      () => null,
      (e: Error) => e,
    );
    expect(failure?.message).toMatch(/ERR_NAME_NOT_RESOLVED/);
    expect(offStackRequests).toEqual([{ url: 'http://192.0.2.1/continued', how: 'unresolved' }]);
  });

  test('a host the test fulfils itself with page.route is not off-stack', async ({ page, offStackRequests }) => {
    // No annotation: the fixture itself fails this test if anything is recorded.
    let served = 0;
    await page.route('https://provider.e2e.invalid/**', (route) => {
      served += 1;
      return route.fulfill({ status: 200, contentType: 'text/html', body: '<!doctype html><title>stand-in</title>' });
    });
    await page.goto('https://provider.e2e.invalid/authorize');
    await expect(page).toHaveTitle('stand-in');
    expect(served).toBe(1);
    expect(offStackRequests).toEqual([]);
  });

  test("the app's Google Fonts stylesheet is answered by the stand-in, never fetched", async ({
    page,
    offStackRequests,
  }) => {
    const fonts: string[] = [];
    page.on('response', (r) => {
      if (new URL(r.url()).hostname === 'fonts.googleapis.com') fonts.push(`${r.status()} ${r.headers()['content-type']}`);
    });
    await page.goto('/signin');
    await expect(page.getByRole('dialog')).toBeVisible();
    // If the web ever stops linking Google Fonts, drop the stand-in
    // (lib/offstack.ts OFF_STACK_STAND_INS) and this test with it.
    await expect.poll(() => fonts.length, 'the web links the Google Fonts stylesheet').toBeGreaterThan(0);
    for (const f of fonts) expect(f).toBe('200 text/css');
    expect(offStackRequests).toEqual([]);
  });
});
