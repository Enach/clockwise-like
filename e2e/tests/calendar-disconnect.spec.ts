/**
 * Disconnecting a Google calendar keeps the user signed in (PAC-35 AC-11,
 * audit API-061).
 *
 * Before PAC-35, DELETE /api/auth/disconnect also expired the auth_token
 * cookie, so the browser dropped its session and the next guarded navigation
 * bounced to the landing page. This journey drives the dashboard's calendar
 * card in a real browser context, whose cookie jar applies whatever Set-Cookie
 * the disconnect answer carries, then asks the server, with that same jar, a
 * question that needs no calendar (GET /api/auth/me), so "calendar not
 * connected" cannot be mistaken for "signed out".
 *
 * The user is DISCONNECTING (seed/ids.ts): it exists only for this journey,
 * so removing its credential takes nothing away from another journey.
 */

import { test, expect } from '../fixtures';
import { BASE_URL } from '../auth';
import { DISCONNECTING } from '../seed/ids';

test.describe('calendar disconnect (PAC-35)', () => {
  test('AC-11: disconnecting a Google calendar keeps the session', async ({ pageAs }) => {
    const page = await pageAs(DISCONNECTING);
    const request = page.context().request;

    const before = await request.get(`${BASE_URL}/api/auth/status`);
    expect(before.status()).toBe(200);
    expect(await before.json()).toMatchObject({ connected: true, provider: 'google' });

    await page.goto('/app');
    await expect(page).toHaveURL(/\/app$/);

    const disconnected = page.waitForResponse(
      (res) => res.url().endsWith('/api/auth/disconnect') && res.request().method() === 'DELETE',
    );
    await page.getByRole('button', { name: 'Disconnect', exact: true }).click();
    const res = await disconnected;
    expect(res.status()).toBe(204);
    // The answer must not touch the session cookie at all.
    expect(res.headers()['set-cookie'] ?? '').not.toContain('auth_token');

    await expect(page.getByText('Not connected', { exact: true }).first()).toBeVisible();

    // The calendar reads as not connected ...
    const after = await request.get(`${BASE_URL}/api/auth/status`);
    expect(await after.json()).toMatchObject({ connected: false, provider: 'google' });

    // ... and the same browser session is still signed in.
    const me = await request.get(`${BASE_URL}/api/auth/me`);
    expect(me.status()).toBe(200);
    expect((await me.json()).email).toBe(DISCONNECTING.email);

    await page.goto('/app');
    await expect(page).toHaveURL(/\/app$/);
  });
});
