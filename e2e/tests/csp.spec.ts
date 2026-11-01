/**
 * PAC-81 Part B, AC-B3 — the app works under nginx's enforced CSP.
 *
 * Every test in the suite already fails on any CSP violation (fixtures.ts,
 * `cspViolations`). This file adds what the rest of the suite does not
 * exercise:
 *   (a) a host whose avatar is a Google profile picture, shown on the public
 *       booking page — the one external image origin the app stores;
 *   (b) the file server's API-base injection switched ON (nginx-injected on
 *       127.0.0.1:18089, e2e/docker-compose.e2e.yml);
 *   the negative control: an avatar on an origin img-src does not allow is
 *       blocked, and the only violations are img-src ones.
 *
 * No image is fetched from the internet: the test intercepts the request and
 * serves a 1x1 PNG. A blocked image is never requested at all.
 */
import { test, expect, CSP_VIOLATIONS_EXPECTED } from '../fixtures';
import { AVATAR_HOSTS } from '../seed/ids';

const INJECTED_BASE_URL = process.env.E2E_INJECTED_BASE_URL ?? 'http://localhost:18089';
const INJECTED_API_BASE = `${INJECTED_BASE_URL}/api`;

const ONE_PIXEL_PNG = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==',
  'base64',
);

test.describe('PAC-81 AC-B3: the enforced CSP', () => {
  test('(a) a googleusercontent avatar loads on the public booking page with zero violations', async ({
    page,
    cspViolations,
    assertNotDemo,
  }) => {
    const host = AVATAR_HOSTS.google;
    let served = 0;
    await page.route('https://lh3.googleusercontent.com/**', (route) => {
      served += 1;
      return route.fulfill({ status: 200, contentType: 'image/png', body: ONE_PIXEL_PNG });
    });

    const res = await page.goto(`/book/${host.link.slug}`);
    expect(res?.headers()['content-security-policy']).toContain(
      "img-src 'self' data: https://*.googleusercontent.com",
    );
    await expect(page.getByRole('heading', { name: host.link.title })).toBeVisible();
    await assertNotDemo(page);

    const avatar = page.locator(`img[src="${host.avatarUrl}"]`).first();
    await expect(avatar).toBeVisible();
    await expect
      .poll(() => avatar.evaluate((img: HTMLImageElement) => img.complete && img.naturalWidth > 0))
      .toBe(true);
    expect(served, 'the avatar request reached the intercept, so the CSP allowed it').toBeGreaterThan(0);
    expect(cspViolations).toEqual([]);
  });

  test('(b) with the API-base injection active, the app loads its configuration and data with zero violations', async ({
    page,
    request,
    cspViolations,
    assertNotDemo,
  }) => {
    // The variant has no compose health check; wait for it here.
    await expect
      .poll(async () => (await request.get(`${INJECTED_BASE_URL}/runtime-config.js`).catch(() => null))?.status(), {
        timeout: 60_000,
      })
      .toBe(200);

    const html = await (await request.get(`${INJECTED_BASE_URL}/`)).text();
    expect(html, 'the injection is a same-origin script').toContain('<script src="/runtime-config.js"></script>');
    const inline = [...html.matchAll(/<script(?![^>]*\ssrc=)[^>]*>/g)].map((m) => m[0]);
    expect(inline, 'no inline <script> in the served page').toEqual([]);

    await page.route('https://lh3.googleusercontent.com/**', (route) =>
      route.fulfill({ status: 200, contentType: 'image/png', body: ONE_PIXEL_PNG }),
    );
    const host = AVATAR_HOSTS.google;
    const res = await page.goto(`${INJECTED_BASE_URL}/book/${host.link.slug}`);
    const csp = res?.headers()['content-security-policy'] ?? '';
    expect(csp).toMatch(/(^|;)\s*script-src 'self'\s*(;|$)/);

    expect(
      await page.evaluate(() => (window as Window & { __API_BASE_URL__?: string }).__API_BASE_URL__),
    ).toBe(INJECTED_API_BASE);
    // The link's title comes from the API, reached through the injected base.
    await expect(page.getByRole('heading', { name: host.link.title })).toBeVisible();
    await assertNotDemo(page);
    expect(cspViolations).toEqual([]);
  });

  test('negative control: an avatar on a non-allowed origin is blocked, and only by img-src', async ({
    page,
    cspViolations,
  }) => {
    test.info().annotations.push({
      type: CSP_VIOLATIONS_EXPECTED,
      description: 'the negative control asserts on its own violations',
    });
    const host = AVATAR_HOSTS.foreign;
    let requested = 0;
    await page.route('https://avatars.e2e-not-allowed.test/**', (route) => {
      requested += 1;
      return route.fulfill({ status: 200, contentType: 'image/png', body: ONE_PIXEL_PNG });
    });

    await page.goto(`/book/${host.link.slug}`);
    await expect(page.getByRole('heading', { name: host.link.title })).toBeVisible();

    await expect.poll(() => cspViolations.length).toBeGreaterThan(0);
    // Rev 2: at least one (the avatar may render more than once), every one
    // of them img-src, and no other directive.
    expect(
      cspViolations.filter((v) => v.directive !== 'img-src'),
      'violations of any directive other than img-src',
    ).toEqual([]);
    for (const v of cspViolations.filter((x) => x.source === 'event')) {
      expect(v.blocked).toBe(host.avatarUrl);
    }
    expect(requested, 'a blocked image is never requested').toBe(0);
  });
});
