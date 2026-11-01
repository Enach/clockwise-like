/**
 * PAC-81 Part C, owner decision Q-O2 (2026-11-01): only the public booking
 * page may be framed, by any site. Every other page stays unframeable
 * (frame-ancestors 'none', X-Frame-Options: DENY).
 *
 * A page on another origin frames the real booking page and the real /app
 * through nginx. The booking page renders inside the frame; /app is refused
 * by the browser.
 *
 * The embedding page is served by a real HTTP server the test starts on
 * 127.0.0.1, at another port, so on another origin than BASE_URL. It must be
 * a local address: Chromium's Local Network Access checks stop a page in the
 * public address space (which includes any page fulfilled by page.route)
 * from framing localhost (net::ERR_BLOCKED_BY_LOCAL_NETWORK_ACCESS_CHECKS),
 * whatever the headers say. In production the embedding site and Paceday are
 * both public, so that check does not apply there.
 */
import http from 'node:http';
import type { AddressInfo } from 'node:net';

import { test, expect, CSP_VIOLATIONS_EXPECTED } from '../fixtures';
import { BASE_URL } from '../auth';
import { LINKS } from '../seed/ids';

/** Serves `html` on 127.0.0.1 at an OS-chosen port; returns its origin. */
async function embedder(html: string): Promise<{ origin: string; close: () => Promise<void> }> {
  const server = http.createServer((_req, res) => {
    res.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8' });
    res.end(html);
  });
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
  const { port } = server.address() as AddressInfo;
  return {
    origin: `http://127.0.0.1:${port}`,
    close: () => new Promise<void>((resolve) => server.close(() => resolve())),
  };
}

const isFrameAncestors = (v: { directive: string; text: string }) => /frame-ancestors/.test(`${v.directive} ${v.text}`);

test.describe('PAC-81 Q-O2: framing', () => {
  test('another site can frame /book/<slug> but not /app', async ({ page, cspViolations }) => {
    // The refused /app frame is reported as a frame-ancestors violation; it is
    // asserted below instead of failing the test.
    test.info().annotations.push({ type: CSP_VIOLATIONS_EXPECTED, description: 'the refused /app frame' });
    const bookUrl = `${BASE_URL}/book/${LINKS.uiBooking.slug}`;
    const appUrl = `${BASE_URL}/app`;
    const site = await embedder(`<!doctype html><html><body>
      <iframe name="book" src="${bookUrl}" width="800" height="600"></iframe>
      <iframe name="app" src="${appUrl}" width="800" height="600"></iframe>
    </body></html>`);
    try {
      expect(site.origin, 'the embedder is another origin').not.toBe(new URL(BASE_URL).origin);
      const failed: string[] = [];
      page.on('requestfailed', (r) => failed.push(`${r.url()} ${r.failure()?.errorText}`));

      await page.goto(`${site.origin}/embed.html`);

      // The booking page loads and renders inside the frame.
      const book = page.frame({ name: 'book' });
      expect(book, 'the booking frame exists').not.toBeNull();
      await expect(book!.getByRole('heading', { name: LINKS.uiBooking.title }), `failed requests: ${failed.join('; ')}`).toBeVisible({
        timeout: 15_000,
      });
      expect(book!.url()).toBe(bookUrl);

      // /app is refused by its frame-ancestors, not by anything else.
      await expect
        .poll(() => cspViolations.some(isFrameAncestors), {
          message: 'the browser reported the refused /app frame',
          timeout: 15_000,
        })
        .toBe(true);
      const app = page.frame({ name: 'app' });
      expect(app, 'the app frame element exists').not.toBeNull();
      // The SPA's mount point is in the booking frame (control) and never in
      // the refused one, whatever error document the browser put there.
      await expect(book!.locator('#root')).toHaveCount(1);
      await expect(app!.locator('#root')).toHaveCount(0);
      expect(failed.filter((f) => f.includes('LOCAL_NETWORK_ACCESS')), 'blocked by the browser, not by the headers').toEqual([]);

      // Only that refusal: nothing on the framed booking page was blocked.
      expect(cspViolations.filter((v) => !isFrameAncestors(v))).toEqual([]);
    } finally {
      await site.close();
    }
  });

  test('the headers that decide it', async ({ request }) => {
    const book = await request.get(`/book/${LINKS.uiBooking.slug}`);
    expect(book.headers()['content-security-policy']).toContain('frame-ancestors *');
    expect(book.headers()['x-frame-options']).toBeUndefined();
    const app = await request.get('/app');
    expect(app.headers()['content-security-policy']).toContain("frame-ancestors 'none'");
    expect(app.headers()['x-frame-options']).toBe('DENY');
    const api = await request.get(`/api/book/${LINKS.uiBooking.slug}`);
    expect(api.headers()['x-frame-options']).toBe('DENY');
  });
});
