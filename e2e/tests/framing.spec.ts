/**
 * PAC-81 Part C, owner decision Q-O2 (2026-11-01): only the public booking
 * page may be framed, by any site. Every other page stays unframeable
 * (frame-ancestors 'none', X-Frame-Options: DENY).
 *
 * A page on another origin (http://embedder.paceday.test, served by the test
 * itself through page.route; it is the embedding site, not the backend, so
 * nothing of the app is mocked) frames the real booking page and the real
 * /app through nginx. The booking page renders inside the frame; /app is
 * refused by the browser.
 */
import { test, expect, CSP_VIOLATIONS_EXPECTED } from '../fixtures';
import { BASE_URL } from '../auth';
import { LINKS } from '../seed/ids';

const EMBEDDER = 'http://embedder.paceday.test';

test.describe('PAC-81 Q-O2: framing', () => {
  test('another site can frame /book/<slug> but not /app', async ({ page, cspViolations }) => {
    // The refused /app frame is reported as a frame-ancestors violation; it is
    // asserted below instead of failing the test.
    test.info().annotations.push({ type: CSP_VIOLATIONS_EXPECTED, description: 'the refused /app frame' });
    const bookUrl = `${BASE_URL}/book/${LINKS.uiBooking.slug}`;
    const appUrl = `${BASE_URL}/app`;
    await page.route(`${EMBEDDER}/**`, (route) =>
      route.fulfill({
        status: 200,
        contentType: 'text/html',
        body: `<!doctype html><html><body>
          <iframe name="book" src="${bookUrl}" width="800" height="600"></iframe>
          <iframe name="app" src="${appUrl}" width="800" height="600"></iframe>
        </body></html>`,
      }),
    );

    await page.goto(`${EMBEDDER}/embed.html`);

    // The booking page loads and renders inside the frame.
    const book = page.frame({ name: 'book' });
    expect(book, 'the booking frame exists').not.toBeNull();
    await expect(book!.getByRole('heading', { name: LINKS.uiBooking.title })).toBeVisible({ timeout: 15_000 });
    expect(book!.url()).toBe(bookUrl);

    // /app is refused: the frame never holds the app's document.
    await expect
      .poll(() => cspViolations.some((v) => /frame-ancestors/.test(`${v.directive} ${v.text}`)), {
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

    // Only that refusal: nothing on the framed booking page was blocked.
    const others = cspViolations.filter((v) => !/frame-ancestors/.test(`${v.directive} ${v.text}`));
    expect(others).toEqual([]);
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
