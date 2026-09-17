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

import { test as base, expect, type APIRequestContext, type Page } from '@playwright/test';

import { PRIMARY_SESSION, SECOND_SESSION, BASE_URL, mintSessionToken } from './auth';
import { query, queryOne } from './db';
import { USERS } from './seed/ids';

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
};

export const test = base.extend<Fixtures>({
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

  authedPage: async ({ browser }, use) => {
    const context = await browser.newContext({ storageState: PRIMARY_SESSION() });
    const page = await context.newPage();
    await use(page);
    await context.close();
  },

  secondUserPage: async ({ browser }, use) => {
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
