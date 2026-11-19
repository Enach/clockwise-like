/**
 * PAC-98 AC-18, AC-19: Paceday has no password sign-in. A personal address
 * goes to a provider, never to a password form.
 *
 *   <random>@gmail.com  detection answers google; the dialog starts Google
 *                       with the typed address as hint (AC-18).
 *   <random>@yahoo.com  detection answers generic; the dialog shows the
 *                       personal-address step (Google with the hint, no
 *                       Microsoft button, no password field) and nothing
 *                       requests /api/auth/login or /api/auth/register (AC-19).
 *
 * A consumer domain never reaches the MX resolver (backend/auth/sso_detect.go,
 * proven with the fake resolver by backend/auth/pac98_detect_test.go AC-1/AC-3),
 * so these journeys need no DNS stand-in fixture. Nothing is mocked on
 * Paceday's side: the dialog posts to the real /api/auth/detect and the
 * Google start is the real backend route. The journey stops at that start:
 * lib/provider-start.ts reads the backend's real 302 and keeps the browser
 * from following it, so Google is never loaded (CI run 44422703172 loaded the
 * real Google page here, whose own CSP reports failed the test). The other
 * helpers are repeated from pac97-signin-detect.spec.ts because a spec file
 * cannot import another spec file without registering its tests twice.
 *
 * This stack configures Google sign-in only (Microsoft waits for PAC-99), so
 * "no Microsoft button although Microsoft is available" is the web's vitest
 * (PAC-98 AC-8), not this journey.
 *
 * Test ids: e2e/TESTIDS-REQUIRED.md section 7a (web PR smart-calendar-flow#149).
 */

import { randomUUID } from 'node:crypto';

import type { Page, Request, Route } from '@playwright/test';

import { test, expect } from '../fixtures';
import { stopAtProviderStarts } from '../lib/provider-start';

/** Every request this page makes to a path ending in /api/auth/login or /api/auth/register. */
function recordPasswordRoutes(page: Page): Request[] {
  const hits: Request[] = [];
  page.on('request', (r) => {
    const path = new URL(r.url()).pathname.replace(/\/+$/, '');
    if (path.endsWith('/api/auth/login') || path.endsWith('/api/auth/register')) hits.push(r);
  });
  return hits;
}

/** Every request to a provider start (/api/auth/google or /api/auth/microsoft) this page makes. */
function recordProviderStarts(page: Page): Request[] {
  const starts: Request[] = [];
  page.on('request', (r) => {
    const path = new URL(r.url()).pathname;
    if (path === '/api/auth/google' || path === '/api/auth/microsoft') starts.push(r);
  });
  return starts;
}

/** Opens /signin and waits until the dialog holds availability (the top Google button renders). */
async function openSignIn(page: Page): Promise<void> {
  const availability = page.waitForResponse((r) => new URL(r.url()).pathname === '/api/integrations/availability');
  await page.goto('/signin');
  expect((await availability).status()).toBe(200);
  await expect(
    page.getByRole('dialog').getByRole('button', { name: /^(Continue|Sign up) with Google$/ }).first(),
  ).toBeVisible();
}

/**
 * Types the email, continues, and returns detection's answer, read from the
 * real backend's response through route.fetch() (the page may navigate at
 * once on google, which drops the body). The page gets the response unchanged.
 */
async function continueWithEmail(page: Page, email: string): Promise<string> {
  const dialog = page.getByRole('dialog');
  await expect(dialog).toBeVisible();
  await dialog.getByLabel('Work email').fill(email);

  let resolveAnswer: (a: { status: number; body: string }) => void = () => {};
  const answer = new Promise<{ status: number; body: string }>((resolve) => (resolveAnswer = resolve));
  const passThrough = async (route: Route) => {
    if (route.request().method() !== 'POST') return route.fallback();
    const response = await route.fetch();
    const body = await response.text();
    resolveAnswer({ status: response.status(), body });
    await route.fulfill({ response, body });
  };
  await page.route('**/api/auth/detect', passThrough);
  try {
    await dialog.getByRole('button', { name: 'Continue', exact: true }).click();
    const res = await answer;
    expect(res.status).toBe(200);
    const parsed = JSON.parse(res.body) as { type: string; provider_name?: string; redirect_url?: string };
    // Only sso carries a provider name or a redirect path.
    expect(parsed.provider_name).toBeUndefined();
    expect(parsed.redirect_url).toBeUndefined();
    return parsed.type;
  } finally {
    await page.unroute('**/api/auth/detect', passThrough);
  }
}

function freshLocal(): string {
  return `pac98-${randomUUID().slice(0, 8)}`;
}

test.describe('PAC-98 — personal addresses go to a provider, never a password form', () => {
  test('AC-18: a gmail.com address goes straight to the Google start with the typed address as hint', async ({
    page,
  }) => {
    const providerStarts = await stopAtProviderStarts(page);
    const passwordRoutes = recordPasswordRoutes(page);
    const email = `${freshLocal()}@gmail.com`;
    await openSignIn(page);

    expect(await continueWithEmail(page, email)).toBe('google');

    // The browser's next Paceday request is the API's Google start, with the hint.
    const start = await providerStarts.first;
    expect(start.url.pathname).toBe('/api/auth/google');
    // The page itself navigated to the start, at the top level.
    expect(start.isNavigation).toBe(true);
    expect(start.isMainFrame).toBe(true);
    expect(start.url.searchParams.get('login_hint')).toBe(email);
    // The real start forwards it to Google; the journey stops before Google loads.
    expect(start.status).toBe(302);
    expect(start.location?.host).toBe('accounts.google.com');
    expect(start.location?.searchParams.get('login_hint')).toBe(email);
    expect(passwordRoutes).toHaveLength(0);
  });

  test('AC-19: a yahoo.com address gets the personal-address step: Google with the hint, no password, no Microsoft', async ({
    page,
  }) => {
    const providerStarts = await stopAtProviderStarts(page);
    const passwordRoutes = recordPasswordRoutes(page);
    const starts = recordProviderStarts(page);
    const email = `${freshLocal()}@yahoo.com`;
    await openSignIn(page);

    expect(await continueWithEmail(page, email)).toBe('generic');

    const dialog = page.getByRole('dialog');
    const step = page.getByTestId('auth-personal-step');
    await expect(step).toBeVisible();
    await expect(step.getByTestId('auth-personal-email')).toHaveText(email);
    await expect(step.getByTestId('auth-personal-google')).toBeVisible();
    // No password field anywhere in the dialog (nor the page), no Microsoft button.
    await expect(dialog.getByLabel(/password/i)).toHaveCount(0);
    await expect(page.locator('input[type="password"]')).toHaveCount(0);
    await expect(dialog.getByRole('button', { name: /microsoft/i })).toHaveCount(0);
    // Nothing navigated yet.
    expect(new URL(page.url()).pathname).toBe('/signin');
    expect(starts).toHaveLength(0);
    // The email and its kind are remembered for this tab only (AC-15, PAC-97 D-10).
    expect(
      await page.evaluate(() => [
        window.sessionStorage.getItem('paceday:signin-email'),
        window.sessionStorage.getItem('paceday:signin-email-kind'),
      ]),
    ).toEqual([email, 'personal']);

    // A click checks /api/health (PAC-83), then starts Google with the hint.
    await step.getByTestId('auth-personal-google').click();
    const start = await providerStarts.first;
    expect(start.url.pathname).toBe('/api/auth/google');
    // The page itself navigated to the start, at the top level.
    expect(start.isNavigation).toBe(true);
    expect(start.isMainFrame).toBe(true);
    expect(start.url.searchParams.get('login_hint')).toBe(email);
    // The real start sends it on to Google, which is never loaded.
    expect(start.status).toBe(302);
    expect(start.location?.host).toBe('accounts.google.com');
    expect(start.location?.searchParams.get('login_hint')).toBe(email);
    expect(starts.filter((r) => new URL(r.url()).pathname === '/api/auth/microsoft')).toHaveLength(0);

    // During the whole journey, no password route was requested.
    expect(passwordRoutes).toHaveLength(0);
  });
});
