/**
 * Stopping a sign-in journey at the provider start, without loading the
 * provider.
 *
 * The API's provider starts (/api/auth/google, /api/auth/microsoft) answer
 * 302 to the real provider (accounts.google.com, login.microsoftonline.com).
 * A page.route on the provider host does NOT stop the browser there:
 * Playwright calls a route handler for the first URL of a request only, so
 * the redirect hop goes to the network and the real provider page loads
 * (with its own scripts and CSP reports; CI run 44422703172). Tests must
 * never reach a real external service (lib/offstack.ts).
 *
 * So the start itself is intercepted. Its request goes to the real backend
 * through route.fetch() with maxRedirects 0, the backend's own answer (status
 * and Location) is recorded for the test to assert on, and the browser gets a
 * blank stand-in page on the start's own URL instead of following Location.
 * Nothing on Paceday's side is mocked: the request, the backend route and its
 * response are real; only the provider is never contacted.
 */

import type { Page, Route } from '@playwright/test';

export const PROVIDER_START_PATHS = ['/api/auth/google', '/api/auth/microsoft'];

/** One provider start the page made, and what the real backend answered. */
export interface ProviderStart {
  /** The start's URL as the browser requested it (path, login_hint, ...). */
  url: URL;
  /** The backend's status: a redirect to the provider is 302. */
  status: number;
  /** The backend's Location (the provider URL), or null when there is none. */
  location: URL | null;
}

export interface ProviderStarts {
  /** Every start answered so far, in order. */
  answered: ProviderStart[];
  /** Resolves with the first start, once the backend has answered it. */
  first: Promise<ProviderStart>;
}

/** Intercepts this page's provider starts; see the file comment. */
export async function stopAtProviderStarts(page: Page): Promise<ProviderStarts> {
  const answered: ProviderStart[] = [];
  let resolveFirst: (s: ProviderStart) => void = () => {};
  const first = new Promise<ProviderStart>((resolve) => (resolveFirst = resolve));
  await page.route(
    (url) => PROVIDER_START_PATHS.includes(url.pathname),
    async (route: Route) => {
      const response = await route.fetch({ maxRedirects: 0 });
      const location = response.headers()['location'];
      const start: ProviderStart = {
        url: new URL(route.request().url()),
        status: response.status(),
        location: location ? new URL(location, route.request().url()) : null,
      };
      answered.push(start);
      resolveFirst(start);
      await route.fulfill({
        status: 200,
        contentType: 'text/html',
        body: '<!doctype html><title>provider start reached</title>',
      });
    },
  );
  return { answered, first };
}
