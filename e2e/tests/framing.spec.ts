/**
 * PAC-81 Part C, owner decision Q-O2 (2026-11-01): only the public booking
 * page may be framed, by any site. Every other page stays unframeable
 * (frame-ancestors 'none', X-Frame-Options: DENY).
 *
 * A page on another origin frames the real booking page and the real /app
 * through nginx. The booking page renders inside the frame; /app is refused
 * by the browser. PAC-91 adds crafted addresses that nginx used to normalise
 * to the booking page (refused now) and the booking page's home link.
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

  // PAC-91 AC-16 (spec D-3): nginx decides framability on the raw path too.
  // Addresses nginx used to normalise to a booking path while the browser and
  // the app saw another page (the team invite, which sends a signed-out
  // visitor to the home page; the not-found page) are now refused like any
  // other page.
  //
  // Spec AC-16 (revision 3) frames /settings/..%2Fbook/<slug> (200 from the
  // file server, the app's not-found page) rather than /book//<slug>: the
  // real file server (docker/fileserver, Go ServeMux) answers /book//<slug>
  // with a 301 to /book/<slug>, so the only document a frame can end on there
  // is the genuine booking page. That 301 carries DENY (header test below).
  test('PAC-91: crafted addresses that normalise to /book/ are refused', async ({ page, cspViolations }) => {
    test.info().annotations.push({ type: CSP_VIOLATIONS_EXPECTED, description: 'the two refused crafted frames' });
    const bookUrl = `${BASE_URL}/book/${LINKS.uiBooking.slug}`;
    const crafted = {
      invite: `${BASE_URL}/app/team/invite/..%2F..%2F..%2Fbook%2Fx`,
      settings: `${BASE_URL}/settings/..%2Fbook/${LINKS.uiBooking.slug}`,
    };
    const site = await embedder(`<!doctype html><html><body>
      <iframe name="book" src="${bookUrl}" width="800" height="600"></iframe>
      <iframe name="invite" src="${crafted.invite}" width="800" height="600"></iframe>
      <iframe name="settings" src="${crafted.settings}" width="800" height="600"></iframe>
    </body></html>`);
    try {
      const failed: string[] = [];
      page.on('requestfailed', (r) => failed.push(`${r.url()} ${r.failure()?.errorText}`));

      await page.goto(`${site.origin}/embed.html`);

      // Control: the real booking page still renders in its frame.
      const book = page.frame({ name: 'book' });
      expect(book, 'the booking frame exists').not.toBeNull();
      await expect(book!.getByRole('heading', { name: LINKS.uiBooking.title }), `failed requests: ${failed.join('; ')}`).toBeVisible({
        timeout: 15_000,
      });

      // One frame-ancestors violation per crafted frame.
      await expect
        .poll(() => cspViolations.filter(isFrameAncestors).length, {
          message: 'the browser reported both crafted frames as refused by frame-ancestors',
          timeout: 15_000,
        })
        .toBeGreaterThanOrEqual(2);
      for (const name of Object.keys(crafted)) {
        const frame = page.frame({ name });
        expect(frame, `${name}: the frame element exists`).not.toBeNull();
        // Whatever error document the browser put there, it is not the app.
        await expect(frame!.locator('#root')).toHaveCount(0);
      }
      await expect(book!.locator('#root')).toHaveCount(1);
      expect(failed.filter((f) => f.includes('LOCAL_NETWORK_ACCESS')), 'blocked by the browser, not by the headers').toEqual([]);
      expect(cspViolations.filter((v) => !isFrameAncestors(v))).toEqual([]);
    } finally {
      await site.close();
    }
  });

  // PAC-91 AC-4 (spec D-2, owner OQ-1 open: default "_top"). The booking
  // page's "paceday" link targets the top-level browsing context, so clicking
  // it inside a frame replaces the embedding page with Paceday's home page
  // instead of navigating the frame. Needs the PAC-91 web image
  // (Enach/smart-calendar-flow impl/pac-91-web). If OQ-1 is answered "new
  // tab" (TOP_LEVEL_LINK_TARGET = "_blank"), this expectation becomes: a
  // popup at `${BASE_URL}/`, the embedder's URL unchanged, the frame still on
  // /book/<slug>.
  test("PAC-91: the framed booking page's home link opens Paceday at the top level", async ({ page }) => {
    const bookUrl = `${BASE_URL}/book/${LINKS.uiBooking.slug}`;
    const site = await embedder(`<!doctype html><html><body>
      <iframe name="book" src="${bookUrl}" width="800" height="600"></iframe>
    </body></html>`);
    try {
      const failed: string[] = [];
      page.on('requestfailed', (r) => failed.push(`${r.url()} ${r.failure()?.errorText}`));
      await page.goto(`${site.origin}/embed.html`);

      const book = page.frame({ name: 'book' });
      expect(book, 'the booking frame exists').not.toBeNull();
      await expect(book!.getByRole('heading', { name: LINKS.uiBooking.title }), `failed requests: ${failed.join('; ')}`).toBeVisible({
        timeout: 15_000,
      });
      const home = book!.getByTestId('booking-home-link');
      await expect(home).toHaveAttribute('href', '/');
      await expect(home).toHaveAttribute('target', '_top');

      await home.click();

      // The top-level page is now Paceday's home page, unframed...
      await expect(page).toHaveURL(`${BASE_URL}/`, { timeout: 15_000 });
      await expect(page.locator('#root')).toHaveCount(1);
      // ...and no frame anywhere shows a Paceday page other than the booking page.
      const paceday = new URL(BASE_URL).origin;
      const framed = page
        .frames()
        .filter((f) => f !== page.mainFrame())
        .map((f) => f.url())
        .filter((u) => u.startsWith(paceday) && !new URL(u).pathname.startsWith('/book/'));
      expect(framed).toEqual([]);
    } finally {
      await site.close();
    }
  });

  // PAC-91 AC-5, guard: unframed, the home link still lands on the home page
  // (now a full document load, not an in-app transition).
  test("PAC-91: unframed, the booking page's home link goes to the home page", async ({ page }) => {
    await page.goto(`/book/${LINKS.uiBooking.slug}`);
    await expect(page.getByRole('heading', { name: LINKS.uiBooking.title })).toBeVisible({ timeout: 15_000 });
    await page.getByTestId('booking-home-link').click();
    await expect(page).toHaveURL(`${BASE_URL}/`);
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
    // PAC-91 AC-16, header half: the raw path decides too. The first two
    // answer 200 with the SPA, so only the headers keep them out of a frame;
    // /book//<slug> is the file server's 301 to the booking page, refused too.
    for (const [path, status] of [
      ['/app/team/invite/..%2F..%2F..%2Fbook%2Fx', 200],
      [`/settings/..%2Fbook/${LINKS.uiBooking.slug}`, 200],
      [`/book//${LINKS.uiBooking.slug}`, 301],
    ] as const) {
      const crafted = await request.get(path, { maxRedirects: 0 });
      expect(crafted.status(), path).toBe(status);
      expect(crafted.headers()['content-security-policy'], path).toContain("frame-ancestors 'none'");
      expect(crafted.headers()['x-frame-options'], path).toBe('DENY');
    }
  });
});
