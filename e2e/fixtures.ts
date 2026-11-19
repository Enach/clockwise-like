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
  type Request,
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
import { OFF_STACK_ALLOWLIST, isOnStack } from './lib/offstack';

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

/**
 * Only violations reported by a document on the e2e stack count
 * (lib/offstack.ts isOnStack): the app's own pages through nginx, the
 * injected variant, and pages a test serves on 127.0.0.1 (framing.spec.ts).
 * A document on any other origin is a test's own stand-in (route.fulfill) or
 * a page the off-stack guard below already fails the test for; its policy is
 * not the app's. A report whose document cannot be told is counted, so this
 * scoping never drops one of the app's.
 */
function countsForApp(documentUrl: string | undefined): boolean {
  return !documentUrl || isOnStack(documentUrl);
}

async function watchCsp(context: BrowserContext, sink: CspViolation[]): Promise<void> {
  await context.exposeBinding(
    '__pacedayCspViolation',
    (_source, v: { directive: string; blocked: string; document: string }) => {
      if (!countsForApp(v.document)) return;
      sink.push({ source: 'event', directive: v.directive, blocked: v.blocked, text: '' });
    },
  );
  await context.addInitScript(() => {
    document.addEventListener('securitypolicyviolation', (e) => {
      const report = (window as unknown as {
        __pacedayCspViolation?: (v: { directive: string; blocked: string; document: string }) => void;
      }).__pacedayCspViolation;
      report?.({
        directive: e.effectiveDirective || e.violatedDirective,
        blocked: e.blockedURI,
        document: e.documentURI || document.location.href,
      });
    });
  });
  context.on('console', (msg) => {
    const text = msg.text();
    if (!/Content Security Policy/i.test(text) || !/Refused|violat/i.test(text)) return;
    if (!countsForApp(msg.page()?.url())) return;
    const m = text.match(/directive:?\s*"([a-z-]+)/i);
    sink.push({ source: 'console', directive: m?.[1] ?? 'unknown', blocked: '', text });
  });
}

/**
 * No test may reach a real external service. Every browser request to an
 * origin off the e2e stack (lib/offstack.ts) is recorded here, aborted where
 * it can be, and the auto fixture `offStackRequests` fails the test if there
 * is any, naming each URL. Allowlist: OFF_STACK_ALLOWLIST, empty.
 *
 * How a request is caught:
 *  - a context WebSocket route, for every WebSocket to an origin off the
 *    stack (WebSockets never reach context.route): recorded and closed
 *    before it connects;
 *  - the context route below, for every request no page.route answered. A
 *    test that fulfils a host with page.route (page routes run first) keeps
 *    that request in the browser, and it is not recorded. The app loads
 *    nothing off the stack of its own (PAC-143: its fonts are self-hosted);
 *  - the hops of an HTTP redirect, which Playwright never hands to a route
 *    handler (it calls one "for the first url" only), cannot be aborted
 *    here. They are recorded from the request event (`redirect`), and
 *    Chromium's resolver (playwright.config.ts, --host-resolver-rules)
 *    refuses every host off the stack, so the hop never connects;
 *  - any other request whose host the resolver refused (`unresolved`; say a
 *    page.route handler that called route.continue() on an off-stack URL,
 *    which skips the context route), from the requestfailed event.
 *
 * A test that provokes an off-stack request on purpose (the guard's
 * self-test) annotates itself with OFF_STACK_REQUESTS_EXPECTED.
 */
export interface OffStackRequest {
  url: string;
  /**
   * routed: aborted by the context route, or a WebSocket closed by the
   * context's WebSocket route. redirect: a redirect's hop off the
   * stack (routes never see one; the resolver refuses it). unresolved: any
   * other request whose host the resolver refused.
   */
  how: 'routed' | 'redirect' | 'unresolved';
}

export const OFF_STACK_REQUESTS_EXPECTED = 'off-stack-requests-expected';

async function watchOffStack(context: BrowserContext, sink: OffStackRequest[]): Promise<void> {
  const seen = new WeakSet<object>();
  const record = (request: Request, how: OffStackRequest['how']) => {
    if (seen.has(request)) return;
    seen.add(request);
    sink.push({ url: request.url(), how });
  };
  await context.route(
    (url) => !isOnStack(url.href),
    (route) => {
      record(route.request(), 'routed');
      return route.abort('blockedbyclient');
    },
  );
  // WebSockets do not go through context.route. A routed WebSocket that is
  // never connectToServer()'d never reaches the server; it is recorded and
  // closed.
  await context.routeWebSocket(
    (url) => !isOnStack(url.href),
    async (ws) => {
      sink.push({ url: ws.url(), how: 'routed' });
      await ws.close();
    },
  );
  context.on('request', (r) => {
    if (r.redirectedFrom() && !isOnStack(r.url())) record(r, 'redirect');
  });
  context.on('requestfailed', (r) => {
    // The resolver resolves only the stack's hosts, so a host that did not
    // resolve was off the stack. Other failures (a request cancelled, or one
    // the app's CSP refused, which the cspViolations fixture judges) never
    // left the browser and are not recorded.
    if (/ERR_NAME_NOT_RESOLVED/.test(r.failure()?.errorText ?? '')) record(r, 'unresolved');
  });
}

interface BrowserGuards {
  csp: CspViolation[];
  offStack: OffStackRequest[];
}

async function guardContext(context: BrowserContext, guards: BrowserGuards): Promise<void> {
  if (watchedContexts.has(context)) return;
  watchedContexts.add(context);
  await watchCsp(context, guards.csp);
  await watchOffStack(context, guards.offStack);
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
  /** The sinks every browser context of this test reports to (see guardContext). */
  browserGuards: BrowserGuards;
  /**
   * Every browser request to an origin off the e2e stack in this test.
   * Automatic: a test with any fails unless annotated OFF_STACK_REQUESTS_EXPECTED.
   */
  offStackRequests: OffStackRequest[];
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
  browserGuards: [
    async ({ browser }, use) => {
      const guards: BrowserGuards = { csp: [], offStack: [] };
      // Wrap browser.newContext for the duration of this test, so contexts the
      // spec (or Playwright's own `context` fixture) creates are watched too.
      const original = browser.newContext.bind(browser) as Browser['newContext'];
      browser.newContext = (async (options?: Parameters<Browser['newContext']>[0]) => {
        const context = await original(options);
        await guardContext(context, guards);
        return context;
      }) as Browser['newContext'];
      try {
        await use(guards);
      } finally {
        browser.newContext = original;
      }
    },
    { auto: true },
  ],

  cspViolations: [
    async ({ browserGuards }, use, testInfo) => {
      await use(browserGuards.csp);
      if (!testInfo.annotations.some((a) => a.type === CSP_VIOLATIONS_EXPECTED)) {
        expect(browserGuards.csp, 'CSP violations reported by the browser (PAC-81 AC-B3)').toEqual([]);
      }
    },
    { auto: true },
  ],

  offStackRequests: [
    async ({ browserGuards }, use, testInfo) => {
      await use(browserGuards.offStack);
      if (!testInfo.annotations.some((a) => a.type === OFF_STACK_REQUESTS_EXPECTED)) {
        expect(
          browserGuards.offStack,
          'the browser requested an origin off the e2e stack. Tests must never reach a real external ' +
            'service: answer that host with page.route (route.fulfill) or stop before the redirect. ' +
            `See e2e/lib/offstack.ts (allowlist: ${JSON.stringify(OFF_STACK_ALLOWLIST)}).`,
        ).toEqual([]);
      }
    },
    { auto: true },
  ],

  // The built-in context, in case it was created before the wrapper above.
  context: async ({ context, browserGuards }, use) => {
    await guardContext(context, browserGuards);
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
    await use(assertNotDemo);
  },
});

/**
 * The anti-false-pass guard (PAC-83 AC-28, contract
 * contracts/features/PAC-83.md §5.2): fails when the page shows anything that
 * means the test did not talk to a live backend.
 *
 * Exported on its own (and handed out by the `assertNotDemo` fixture) so the
 * guard self-test (tests/guard-self-test.spec.ts) runs this exact function
 * against pages built with page.setContent.
 *
 * Every marker is a stable test id the web renders (contract §5.1), except
 * two kept on purpose:
 *  - the old banner sentence, so the guard still works against the web from
 *    before PAC-83 while the two repos' merges are not simultaneous (spec §7);
 *    remove it once no deployed web renders it;
 *  - the manager demo roster names, which only the demo seed contains.
 */
export async function assertNotDemo(page: Page): Promise<void> {
  // The non-dismissible label every signed-in page shows during a demo
  // (demo builds only; never in the production image the suite runs).
  await expect(page.getByTestId('demo-label'), 'the demo label is on screen').toHaveCount(0);
  // The persistent "can't reach Paceday" banner: the health probe failed.
  await expect(
    page.getByTestId('server-unreachable-banner'),
    'the "can\'t reach Paceday" banner is on screen',
  ).toHaveCount(0);
  // An inline "can't reach" error. Since PAC-83 an unreachable backend no
  // longer shows invented data but this error, so a test that only asserts
  // something is ABSENT (an empty list, no conflict) would otherwise pass
  // against a dead backend. Tests that provoke a lost answer on purpose
  // (public-booking.spec.ts AC-10) do not call the guard while it is shown.
  await expect(
    page.locator('[data-testid="api-error"][data-error-kind="lost"]'),
    'an inline "can\'t reach" error is on screen',
  ).toHaveCount(0);
  // Transitional: the web before PAC-83 (src/components/MockBanner.tsx)
  // rendered this sentence when src/api/client.ts had fallen back to its
  // built-in demo fixtures.
  await expect(
    page.getByText(/Backend not reachable — showing demo data/i),
    'the pre-PAC-83 demo-data banner is on screen',
  ).toHaveCount(0);
  // The manager demo roster (src/api/manager.ts SEED_MEMBERS before PAC-83,
  // src/demo after). Never in a real roster.
  await expect(
    page.getByText(/Sarah Chen|Miguel Alvarez|Priya Nair/),
    'a demo roster name is on screen',
  ).toHaveCount(0);
}

export { expect };
