import { defineConfig, devices } from '@playwright/test';

import { BASE_URL } from './auth';

/**
 * Retries are 0 everywhere, including CI.
 *
 * The factory rule is "no test may pass when the feature is broken"
 * (docs/factory/README.md §3). A retry count above zero converts an
 * intermittent product bug — a race in the booking conflict check, a slot list
 * that is sometimes empty — into a green run, which is the exact failure mode
 * the gate exists to prevent. The suite is written to wait on state rather than
 * on time, so a retry should never be needed; if one would help, that is a
 * finding, not a configuration problem.
 *
 * `trace: 'on-first-retry'` is kept as the factory brief asks, so that setting
 * E2E_RETRIES=1 locally while debugging produces a trace. It costs nothing at
 * retries: 0.
 */
export default defineConfig({
  testDir: './tests',
  outputDir: './test-results',

  // A whole stack is coming up behind this; the individual assertions are still
  // tight (5s) so a hung backend fails fast rather than eating the test budget.
  timeout: 60_000,
  expect: { timeout: 5_000 },

  globalSetup: './global-setup.ts',
  globalTeardown: './global-teardown.ts',

  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: Number(process.env.E2E_RETRIES ?? 0),
  workers: process.env.CI ? 2 : undefined,

  reporter: process.env.CI
    ? [['list'], ['html', { outputFolder: 'playwright-report', open: 'never' }], ['github']]
    : [['list'], ['html', { outputFolder: 'playwright-report', open: 'never' }]],

  use: {
    baseURL: BASE_URL,
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',
    actionTimeout: 10_000,
    navigationTimeout: 20_000,

    // Determinism. The backend container runs TZ=UTC and every seeded window is
    // expressed in UTC, so the browser must agree or a slot rendered as
    // "9:00 AM" would not be the 09:00Z the server offered.
    timezoneId: 'UTC',
    locale: 'en-US',

    // The app is same-origin behind nginx; nothing here needs a real cert.
    ignoreHTTPSErrors: false,
  },

  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],
});
