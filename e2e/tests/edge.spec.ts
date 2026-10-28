/**
 * PAC-81 Part B, AC-B1 and AC-B2 (e2e half) — the real stack's edge.
 *
 * The integration tests in nginx/test cover these against a stub upstream;
 * this file checks the same properties with the real backend and the real
 * frontend bundle behind the shipped nginx config.
 */
import type { APIResponse } from '@playwright/test';

import { test, expect } from '../fixtures';
import { LINKS } from '../seed/ids';
import { BASE_URL } from '../auth';

function headerValues(res: APIResponse, name: string): string[] {
  return res
    .headersArray()
    .filter((h) => h.name.toLowerCase() === name.toLowerCase())
    .map((h) => h.value);
}

function assertEdgeHeaders(res: APIResponse, what: string): void {
  expect(headerValues(res, 'X-Content-Type-Options'), what).toEqual(['nosniff']);
  expect(headerValues(res, 'X-Frame-Options'), what).toEqual(['DENY']);
  expect(headerValues(res, 'Referrer-Policy'), what).toEqual(['strict-origin-when-cross-origin']);
  const csp = headerValues(res, 'Content-Security-Policy');
  expect(csp, what).toHaveLength(1);
  expect(csp[0], what).toContain("frame-ancestors 'none'");
  expect(csp[0], what).toMatch(/(^|;)\s*script-src 'self'\s*(;|$)/);
  expect(headerValues(res, 'Strict-Transport-Security'), `${what}: HSTS is the proxy's`).toEqual([]);
}

test.describe('PAC-81 AC-B1/AC-B2: the nginx edge on the real stack', () => {
  test('every kind of response carries the security headers exactly once, and none redirects to https', async ({
    request,
  }) => {
    const get = (path: string, headers?: Record<string, string>) =>
      request.get(`${BASE_URL}${path}`, { maxRedirects: 0, headers });

    const cases: Array<[string, string, number]> = [
      ['SPA page', '/', 200],
      ['SPA deep link (fallback)', `/book/${LINKS.uiBooking.slug}`, 200],
      ['API JSON', '/api/health', 200],
      ['API error', `/api/book/e2e-no-such-link-ever`, 404],
      ['nginx health', '/nginx-health', 200],
    ];
    for (const [what, path, status] of cases) {
      for (const headers of [undefined, { 'X-Forwarded-Proto': 'http' }, { 'X-Forwarded-Proto': 'https' }]) {
        const res = await get(path, headers);
        expect(res.status(), `${what} ${JSON.stringify(headers)}`).toBe(status);
        assertEdgeHeaders(res, what);
        expect(res.headers()['location'] ?? '', what).not.toMatch(/^https:/i);
      }
    }

    // A static asset the SPA page references.
    const html = await (await get('/')).text();
    const asset = html.match(/src="(\/assets\/[^"]+\.js)"/)?.[1];
    expect(asset, 'the SPA page references a bundled script').toBeTruthy();
    const assetRes = await get(asset!);
    expect(assetRes.status()).toBe(200);
    assertEdgeHeaders(assetRes, 'static asset');
  });

  test("nginx's own 413 is the backend's JSON shape and carries the headers", async ({ request }) => {
    const res = await request.post(`${BASE_URL}/api/book/${LINKS.apiBooking.slug}`, {
      data: Buffer.alloc(1024 * 1024 + 1, 'a'),
      headers: { 'Content-Type': 'application/json' },
      maxRedirects: 0,
    });
    expect(res.status()).toBe(413);
    expect(res.headers()['content-type']).toBe('application/json');
    // The exact body of the backend's own 413 (writeError's json.Encoder output,
    // trailing newline included): binding merge condition of the PAC-81 Part A
    // contract.
    expect(await res.text()).toBe('{"error":"request body too large"}' + String.fromCharCode(10));
    assertEdgeHeaders(res, '413');
  });
});
