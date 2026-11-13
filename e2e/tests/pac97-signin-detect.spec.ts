/**
 * PAC-97 AC-27: sign-in by email follows the domain's mail host.
 *
 * The backend's MX queries go to the DNS stand-in in fake-google
 * (PACEDAY_MX_RESOLVER, e2e/fake-google/dns.go), which answers by name:
 *   <x>.google-mx.pac97.test     the gorgias.com shape (Google Workspace MX)
 *   <x>.gateway-mx.pac97.test    the mimecast.test shape (a mail gateway)
 *   <x>.microsoft-mx.pac97.test  Exchange Online MX
 * Every test uses its own random subdomain, because detection caches each
 * domain for an hour and the suite runs in parallel and with E2E_KEEP_STACK.
 *
 * Nothing here is mocked on Paceday's side: the dialog posts to the real
 * /api/auth/detect, and the provider starts are the real backend routes. The
 * journey stops at the provider: the browser's navigation to Google or
 * Microsoft (which the backend's start redirects to) is answered by a
 * Playwright route on those hosts, so the test host never reaches them. The
 * backend itself connects to nothing (tests/hermeticity.spec.ts still reads
 * an empty sink at the end of the run).
 *
 * Test ids: e2e/TESTIDS-REQUIRED.md section 7 (web PR smart-calendar-flow#147).
 */

import { randomUUID } from 'node:crypto';

import type { Page, Request, Route } from '@playwright/test';

import { test, expect } from '../fixtures';

const PROVIDER_HOSTS = ['accounts.google.com', 'login.microsoftonline.com'];

function freshDomain(zone: 'google-mx' | 'gateway-mx' | 'microsoft-mx'): string {
  return `p${randomUUID().slice(0, 8)}.${zone}.pac97.test`;
}

/** Answers the browser's navigation to a real provider, so it never leaves the test host. */
async function stopAtProviders(page: Page): Promise<void> {
  for (const host of PROVIDER_HOSTS) {
    await page.route(`https://${host}/**`, (route) =>
      route.fulfill({ status: 200, contentType: 'text/html', body: '<!doctype html><title>provider stand-in</title>' }),
    );
  }
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

/**
 * Opens the sign-in dialog, types the email, continues, and returns detection's answer.
 *
 * On google or microsoft the page navigates away at once, and the browser
 * then drops the detect response body. So the request is passed through to
 * the real backend with route.fetch() and the body is read there, before the
 * page sees it; the page gets the backend's response unchanged. Nothing is
 * stubbed: status, headers and body are the backend's own.
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
    return (JSON.parse(res.body) as { type: string }).type;
  } finally {
    await page.unroute('**/api/auth/detect', passThrough);
  }
}

test.describe('PAC-97 — sign-in detection by MX', () => {
  test('AC-27: a Google Workspace domain goes straight to the Google start with the typed address as hint', async ({
    page,
  }) => {
    await stopAtProviders(page);
    const email = `alice@${freshDomain('google-mx')}`;
    await page.goto('/signin');

    const start = page.waitForRequest((r) => new URL(r.url()).pathname === '/api/auth/google');
    const startResponse = page.waitForResponse((r) => new URL(r.url()).pathname === '/api/auth/google');
    expect(await continueWithEmail(page, email)).toBe('google');

    // The browser's next Paceday request is the API's Google start, with the hint.
    const req = await start;
    expect(new URL(req.url()).searchParams.get('login_hint')).toBe(email);
    // The real start forwards it to the provider.
    const res = await startResponse;
    expect(res.status()).toBe(302);
    const toGoogle = new URL(res.headers()['location'] ?? '');
    expect(toGoogle.host).toBe('accounts.google.com');
    expect(toGoogle.searchParams.get('login_hint')).toBe(email);
  });

  test('AC-27: a gateway domain gets the choose step, nothing navigates until a provider is clicked', async ({
    page,
  }) => {
    await stopAtProviders(page);
    const starts = recordProviderStarts(page);
    const email = `bob@${freshDomain('gateway-mx')}`;
    await page.goto('/signin');

    expect(await continueWithEmail(page, email)).toBe('choose');

    const step = page.getByTestId('auth-choose-step');
    await expect(step).toBeVisible();
    await expect(step.getByTestId('auth-choose-email')).toHaveText(email);
    await expect(step.getByTestId('auth-choose-google')).toBeVisible();
    // Never the password form for a company domain.
    await expect(page.getByRole('dialog').getByLabel(/password/i)).toHaveCount(0);
    // No navigation: still on the sign-in page, no provider start requested.
    expect(new URL(page.url()).pathname).toBe('/signin');
    expect(starts).toHaveLength(0);
    // The typed email is remembered for this tab only (AC-25/AC-26 storage).
    expect(await page.evaluate(() => window.sessionStorage.getItem('paceday:signin-email'))).toBe(email);

    // Back keeps the email (AC-21), and continuing again offers the choice again.
    await step.getByTestId('auth-choose-back').click();
    await expect(page.getByRole('dialog').getByLabel('Work email')).toHaveValue(email);
    expect(await continueWithEmail(page, email)).toBe('choose');
    await expect(page.getByTestId('auth-choose-step')).toBeVisible();
    expect(starts).toHaveLength(0);

    // A click starts the provider with the hint.
    const start = page.waitForRequest((r) => new URL(r.url()).pathname === '/api/auth/google');
    await page.getByTestId('auth-choose-google').click();
    expect(new URL((await start).url()).searchParams.get('login_hint')).toBe(email);
  });

  test.fixme('AC-27: on the choose step, clicking Microsoft requests /api/auth/microsoft with the hint', async () => {
    // Blocked by PAC-99 ("e2e: enable Microsoft sign-in in the compose stack
    // and re-pin step-up scopes"). In this stack, not in the product: the e2e backend configures
    // Google sign-in only (no MICROSOFT_CLIENT_ID/SECRET/REDIRECT_URL), so
    // /api/integrations/availability reports Microsoft unavailable and the
    // choose step shows no auth-choose-microsoft button. Configuring Microsoft
    // in e2e/docker-compose.e2e.yml also turns on the Microsoft SSO step-up,
    // which changes the pinned stepup_scopes in tests/sso-stepup.spec.ts and
    // tests/llm-org-ai.spec.ts (outside PAC-97's allowed paths). Unblocks when
    // PAC-99 lands that compose change and re-pins those two expectations;
    // until then the click itself is covered by the web PR's vitest
    // (AC-19); the journey below (PAC-98 AC-20) pins the choose step instead.
  });

  // PAC-98 AC-20 (spec D-6) replaces "an Exchange Online domain goes straight
  // to the Microsoft start": this stack configures Google sign-in only, and a
  // client that has read availability never starts a provider the deployment
  // does not offer. PAC-99, which configures Microsoft here, re-pins this to
  // "navigates to the Microsoft start with the hint".
  test('PAC-98 AC-20: an Exchange Online domain on the Google-only stack gets the choose step, no Microsoft start', async ({
    page,
  }) => {
    await stopAtProviders(page);
    const starts = recordProviderStarts(page);
    const email = `carol@${freshDomain('microsoft-mx')}`;
    const availability = page.waitForResponse((r) => new URL(r.url()).pathname === '/api/integrations/availability');
    await page.goto('/signin');
    // D-6 applies once availability has loaded (while loading, the web navigates as before).
    expect((await availability).status()).toBe(200);
    // The top Google button renders only once the dialog holds availability.
    await expect(
      page.getByRole('dialog').getByRole('button', { name: /^(Continue|Sign up) with Google$/ }).first(),
    ).toBeVisible();

    expect(await continueWithEmail(page, email)).toBe('microsoft');

    const step = page.getByTestId('auth-choose-step');
    await expect(step).toBeVisible();
    await expect(step.getByTestId('auth-choose-email')).toHaveText(email);
    await expect(step.getByTestId('auth-choose-google')).toBeVisible();
    await expect(page.getByTestId('auth-choose-microsoft')).toHaveCount(0);
    await expect(page.getByRole('dialog').getByRole('button', { name: /microsoft/i })).toHaveCount(0);
    await expect(page.getByRole('dialog').getByLabel(/password/i)).toHaveCount(0);
    expect(new URL(page.url()).pathname).toBe('/signin');
    expect(starts.filter((r) => new URL(r.url()).pathname === '/api/auth/microsoft')).toHaveLength(0);
    expect(starts).toHaveLength(0);
  });
});
