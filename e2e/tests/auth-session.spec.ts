/**
 * Sign-in and landing authenticated.
 *
 * What is real here and what is not:
 *   REAL — the JWT goes through api/middleware.go requireAuth() untouched: the
 *   same HS256 signature check, the same `sub`/`email` claim parsing, the same
 *   cookie name. GET /api/auth/me then reads the user out of Postgres. If the
 *   middleware, the secret, the claim names or the user row were wrong, these
 *   tests fail.
 *   NOT COVERED — api/handlers_auth.go callback(), the Google OAuth code
 *   exchange. It calls config.Exchange() against accounts.google.com and then
 *   https://www.googleapis.com/oauth2/v2/userinfo, both unfakeable from outside
 *   the process (see SEAM-REQUIRED.md). The fixme at the bottom of this file
 *   names it.
 */

import { test, expect } from '../fixtures';
import { BASE_URL, mintExpiredSessionToken, mintSessionToken, storageStateFor } from '../auth';
import { USERS } from '../seed/ids';
import { openAccountMenu } from '../lib/ui';

test.describe('the API session boundary', () => {
  test('rejects a request with no credentials', async ({ api }) => {
    const res = await api.get('/api/auth/me');
    expect(res.status()).toBe(401);
  });

  test('rejects an expired token', async ({ playwright }) => {
    const request = await playwright.request.newContext();
    const res = await request.get(`${BASE_URL}/api/auth/me`, {
      headers: { Authorization: `Bearer ${mintExpiredSessionToken(USERS.primary)}` },
    });
    expect(res.status()).toBe(401);
    await request.dispose();
  });

  test('rejects a token signed with the wrong secret', async ({ playwright }) => {
    // Forged with a valid structure but a bad HMAC. If this ever returns 200,
    // the signature check is gone and every session is forgeable.
    const good = mintSessionToken(USERS.primary);
    const [header, payload] = good.split('.');
    const forged = `${header}.${payload}.AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA`;

    const request = await playwright.request.newContext();
    const res = await request.get(`${BASE_URL}/api/auth/me`, {
      headers: { Authorization: `Bearer ${forged}` },
    });
    expect(res.status()).toBe(401);
    await request.dispose();
  });

  test('accepts the Bearer header and returns the seeded user', async ({ authedApi }) => {
    const res = await authedApi.get('/api/auth/me');
    expect(res.status()).toBe(200);
    expect(await res.json()).toMatchObject({
      id: USERS.primary.id,
      email: USERS.primary.email,
      name: USERS.primary.name,
    });
  });

  test('accepts the auth_token cookie, which is what the browser uses', async ({ browser }) => {
    // Covers the OTHER branch of requireAuth(). The frontend never sends an
    // Authorization header — src/lib/api.ts uses credentials: "include" — so
    // this is the branch the UI depends on.
    const context = await browser.newContext({ storageState: storageStateFor(USERS.primary) });
    const res = await context.request.get(`${BASE_URL}/api/auth/me`);
    expect(res.status()).toBe(200);
    expect((await res.json()).email).toBe(USERS.primary.email);
    await context.close();
  });
});

test.describe('landing authenticated in the browser', () => {
  test('an anonymous visit to /app is bounced to the landing page', async ({ page }) => {
    await page.goto('/app');
    // components/RequireAuth.tsx redirects to "/" once the /me probe 401s.
    await expect(page).toHaveURL(new RegExp(`^${BASE_URL}/(\\?.*)?$`));
  });

  test('a session lands on the dashboard and shows the server-supplied identity', async ({
    authedPage,
    assertNotDemo,
  }) => {
    await authedPage.goto('/app');

    // Must stay on /app: a redirect to /app/onboarding would mean the profile
    // gate is unsatisfied, and a redirect to / would mean the session failed.
    await expect(authedPage).toHaveURL(/\/app$/);

    // The navbar's account menu renders user.name and user.email straight from
    // GET /api/auth/me. These values exist nowhere in the frontend bundle, so
    // seeing them proves the backend answered.
    await openAccountMenu(authedPage);
    await expect(authedPage.getByText(USERS.primary.name)).toBeVisible();
    await expect(authedPage.getByText(USERS.primary.email)).toBeVisible();

    await assertNotDemo(authedPage);
  });

  test('logging out ends the session', async ({ authedPage }) => {
    await authedPage.goto('/app');
    await expect(authedPage).toHaveURL(/\/app$/);

    await openAccountMenu(authedPage);
    await authedPage.getByRole('menuitem', { name: /log out/i }).click();

    // POST /api/auth/logout clears the cookie; the next guarded navigation must
    // bounce. Asserting the redirect rather than the absence of a cookie means
    // the test fails if the clearing does not actually take effect.
    await authedPage.goto('/app');
    await expect(authedPage).toHaveURL(new RegExp(`^${BASE_URL}/(\\?.*)?$`));
  });

  test.fixme(
    'the real Google OAuth callback issues a session',
    // BLOCKED: api/handlers_auth.go callback() calls auth.ExchangeCode() against
    // the hardcoded google.Endpoint and then fetchGoogleUserInfo() against the
    // hardcoded https://www.googleapis.com/oauth2/v2/userinfo. Neither takes a
    // base URL from configuration, so the exchange cannot be pointed at a fake
    // identity provider. Unblocked by SEAM-REQUIRED.md item B
    // (GOOGLE_OAUTH_ENDPOINT_BASE + GOOGLE_USERINFO_URL).
    async () => {},
  );
});
