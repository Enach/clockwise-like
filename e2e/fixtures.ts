/**
 * Shared Playwright fixtures.
 *
 * Design rule: tests must be runnable in any order and in parallel. That rules
 * out a "reset the database between specs" fixture, which would be a global
 * mutex disguised as a helper. Instead, isolation comes from the fixtures
 * themselves: every mutating journey owns a distinct scheduling link with a
 * distinct host user and a distinct time window (see seed/seed.sql), so no two
 * tests can contend. The `db` fixture's cleanup helpers are deliberately narrow:
 * they act on the rows THIS test created, keyed by its own unique booker-email
 * prefix, never on a whole table or a whole link.
 */

import {
  test as base,
  expect,
  type APIRequestContext,
  type Browser,
  type BrowserContext,
  type Page,
} from '@playwright/test';

import {
  PRIMARY_SESSION,
  SECOND_SESSION,
  BASE_URL,
  mintSessionToken,
  storageStateFor,
  type SessionUser,
} from './auth';
import { query, queryOne } from './db';
import { USERS } from './seed/ids';

/**
 * PAC-81 Part B, AC-B3: "the browser reports zero CSP violations on every
 * test". nginx sends an enforced Content-Security-Policy; every browser
 * context a test opens — the built-in `page`/`context`, the fixtures below and
 * any `browser.newContext()` a spec calls itself — reports each violation
 * here, and the auto fixture `cspViolations` fails the test if there is any.
 *
 * Two sources, because neither alone is complete: the document's
 * `securitypolicyviolation` event (structured: the effective directive and
 * the blocked URI) and Chromium's console line ("Refused to ... violates the
 * following Content Security Policy directive: ..."). A test that expects
 * violations (the negative control in tests/csp.spec.ts) annotates itself
 * with `CSP_VIOLATIONS_EXPECTED` and asserts on the list itself.
 */
export interface CspViolation {
  source: 'event' | 'console';
  directive: string;
  blocked: string;
  text: string;
}

export const CSP_VIOLATIONS_EXPECTED = 'csp-violations-expected';

const watchedContexts = new WeakSet<BrowserContext>();

async function watchCsp(context: BrowserContext, sink: CspViolation[]): Promise<void> {
  if (watchedContexts.has(context)) return;
  watchedContexts.add(context);
  await context.exposeBinding(
    '__pacedayCspViolation',
    (_source, v: { directive: string; blocked: string }) => {
      sink.push({ source: 'event', directive: v.directive, blocked: v.blocked, text: '' });
    },
  );
  await context.addInitScript(() => {
    document.addEventListener('securitypolicyviolation', (e) => {
      const report = (window as unknown as {
        __pacedayCspViolation?: (v: { directive: string; blocked: string }) => void;
      }).__pacedayCspViolation;
      report?.({ directive: e.effectiveDirective || e.violatedDirective, blocked: e.blockedURI });
    });
  });
  context.on('console', (msg) => {
    const text = msg.text();
    if (!/Content Security Policy/i.test(text) || !/Refused|violat/i.test(text)) return;
    const m = text.match(/directive:?\s*"([a-z-]+)/i);
    sink.push({ source: 'console', directive: m?.[1] ?? 'unknown', blocked: '', text });
  });
}

/** A thin, typed client for the HTTP boundary, going through nginx. */
export class ApiClient {
  constructor(
    private readonly request: APIRequestContext,
    private readonly token?: string,
  ) {}

  private headers(): Record<string, string> {
    // The Bearer branch of requireAuth() rather than the cookie branch, so the
    // API tests cover the second accepted mechanism too.
    return this.token ? { Authorization: `Bearer ${this.token}` } : {};
  }

  get(path: string, params?: Record<string, string | number>) {
    return this.request.get(`${BASE_URL}${path}`, { headers: this.headers(), params });
  }

  post(path: string, data?: unknown) {
    return this.request.post(`${BASE_URL}${path}`, { headers: this.headers(), data });
  }

  patch(path: string, data?: unknown) {
    return this.request.patch(`${BASE_URL}${path}`, { headers: this.headers(), data });
  }

  delete(path: string) {
    return this.request.delete(`${BASE_URL}${path}`, { headers: this.headers() });
  }
}

export interface Db {
  query: typeof query;
  queryOne: typeof queryOne;
  /**
   * Delete the bookings THIS test created, identified by its own unique booker
   * email prefix. Scoped that narrowly on purpose: a broader "clear the link"
   * helper would let two parallel tests on the same link delete each other's
   * rows, which is exactly the order-dependence the suite must not have.
   * Cross-run cleanliness is seed.sql's job, not this one's.
   */
  deleteBookingsByBooker(emailPrefix: string): Promise<void>;
  /** Delete all focus blocks. They are global (no user_id filter in storage). */
  resetFocusBlocks(): Promise<void>;
}

type Fixtures = {
  /**
   * Every CSP violation reported in this test (AC-B3). Automatic: a test with
   * any violation fails unless it is annotated CSP_VIOLATIONS_EXPECTED.
   */
  cspViolations: CspViolation[];
  /** Anonymous API client — no Authorization header. Public routes only. */
  api: ApiClient;
  /** API client authenticated as the primary seeded user, via Bearer. */
  authedApi: ApiClient;
  /** A page already carrying the primary user's session cookie + local state. */
  authedPage: Page;
  /** A page authenticated as the second seeded user (for cross-user checks). */
  secondUserPage: Page;
  /** Direct database access, for verifying that a write really happened. */
  db: Db;
  /** Asserts the frontend is talking to the real backend, not its demo data. */
  assertNotDemo: (page: Page) => Promise<void>;
  /**
   * API client authenticated as any seeded user, via Bearer. Used for the
   * PAC-43 connected users (seed/ids.ts CONNECTED), whose calendar is the
   * stand-in's.
   */
  apiAs: (user: SessionUser) => ApiClient;
  /** A page signed in as any seeded user; its context closes after the test. */
  pageAs: (user: SessionUser, opts?: { isManager?: boolean }) => Promise<Page>;
};

export const test = base.extend<Fixtures>({
  cspViolations: [
    async ({ browser }, use, testInfo) => {
      const sink: CspViolation[] = [];
      // Wrap browser.newContext for the duration of this test, so contexts the
      // spec (or Playwright's own `context` fixture) creates are watched too.
      const original = browser.newContext.bind(browser) as Browser['newContext'];
      browser.newContext = (async (options?: Parameters<Browser['newContext']>[0]) => {
        const context = await original(options);
        await watchCsp(context, sink);
        return context;
      }) as Browser['newContext'];
      try {
        await use(sink);
      } finally {
        browser.newContext = original;
      }
      if (!testInfo.annotations.some((a) => a.type === CSP_VIOLATIONS_EXPECTED)) {
        expect(sink, 'CSP violations reported by the browser (PAC-81 AC-B3)').toEqual([]);
      }
    },
    { auto: true },
  ],

  // The built-in context, in case it was created before the wrapper above.
  context: async ({ context, cspViolations }, use) => {
    await watchCsp(context, cspViolations);
    await use(context);
  },

  api: async ({ playwright }, use) => {
    const request = await playwright.request.newContext();
    await use(new ApiClient(request));
    await request.dispose();
  },

  authedApi: async ({ playwright }, use) => {
    const request = await playwright.request.newContext();
    await use(new ApiClient(request, mintSessionToken(USERS.primary)));
    await request.dispose();
  },

  authedPage: async ({ browser, cspViolations: _csp }, use) => {
    // Depends on cspViolations so its newContext wrapper is in place first.
    const context = await browser.newContext({ storageState: PRIMARY_SESSION() });
    const page = await context.newPage();
    await use(page);
    await context.close();
  },

  secondUserPage: async ({ browser, cspViolations: _csp }, use) => {
    // Depends on cspViolations so its newContext wrapper is in place first.
    const context = await browser.newContext({ storageState: SECOND_SESSION() });
    const page = await context.newPage();
    await use(page);
    await context.close();
  },

  db: async ({}, use) => {
    await use({
      query,
      queryOne,
      async deleteBookingsByBooker(emailPrefix: string) {
        if (!emailPrefix.startsWith('e2e.booker.')) {
          throw new Error(
            `refusing to delete bookings by prefix "${emailPrefix}" — must start with "e2e.booker."`,
          );
        }
        await query('DELETE FROM bookings WHERE booker_email LIKE $1', [`${emailPrefix}%`]);
      },
      async resetFocusBlocks() {
        await query('DELETE FROM focus_blocks');
      },
    });
  },

  apiAs: async ({ playwright }, use) => {
    const request = await playwright.request.newContext();
    await use((user: SessionUser) => new ApiClient(request, mintSessionToken(user)));
    await request.dispose();
  },

  pageAs: async ({ browser, cspViolations: _csp }, use) => {
    // Depends on cspViolations so its newContext wrapper is in place first.
    const contexts: Array<Awaited<ReturnType<typeof browser.newContext>>> = [];
    await use(async (user: SessionUser, opts: { isManager?: boolean } = {}) => {
      const context = await browser.newContext({ storageState: storageStateFor(user, opts) });
      contexts.push(context);
      return context.newPage();
    });
    for (const c of contexts) await c.close();
  },

  assertNotDemo: async ({}, use) => {
    await use(async (page: Page) => {
      // src/components/MockBanner.tsx renders this exact sentence whenever
      // src/api/client.ts has fallen back to its built-in demo fixtures. If it
      // is on screen, whatever else the test asserted proves nothing about the
      // backend.
      await expect(
        page.getByText(/Backend not reachable — showing demo data/i),
      ).toHaveCount(0);
      // src/api/manager.ts SEED_MEMBERS — the demo roster. Never in a real one.
      await expect(page.getByText(/Sarah Chen|Miguel Alvarez|Priya Nair/)).toHaveCount(0);
    });
  },
});

export { expect };
