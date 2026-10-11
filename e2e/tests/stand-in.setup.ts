/**
 * The `stand-in` setup project (playwright.config.ts). Every browser and API
 * spec depends on it, and its teardown is tests/hermeticity.spec.ts (AC-23),
 * which is what makes Playwright read the sink after the whole run, whether
 * the run passed or not.
 *
 * What it checks is the precondition every PAC-43 scenario shares: the
 * stand-in is the one global setup prepared — it accepts each seeded
 * credential, refuses the one that must be refused, and the backend reaches
 * it. A broken stand-in then fails here, once and clearly, rather than as
 * twenty confusing scenario failures.
 */

import { test as setup, expect } from '@playwright/test';

import { BASE_URL, mintSessionToken } from '../auth';
import { fake } from '../fake';
import { CONNECTED } from '../seed/ids';

setup('the backend reaches the stand-in with a seeded credential, and the stand-in refuses an unseeded one', async ({
  request,
}) => {
  const today = new Date().toISOString().slice(0, 10);
  const window = { start: `${today}T00:00:00Z`, end: `${today}T23:59:59Z` };

  // A probe user's calendar through the real backend path.
  const accepted = CONNECTED.calendar;
  const mark = await fake.mark();
  const ok = await request.get(`${BASE_URL}/api/calendar/events`, {
    headers: { Authorization: `Bearer ${mintSessionToken(accepted)}` },
    params: window,
  });
  expect(ok.status(), await ok.text()).toBe(200);

  const rejected = CONNECTED.rejected;
  const refused = await request.get(`${BASE_URL}/api/calendar/events`, {
    headers: { Authorization: `Bearer ${mintSessionToken(rejected)}` },
    params: window,
  });
  expect(refused.ok()).toBe(false);

  const arrivals = await fake.requests(mark);
  expect(arrivals.map((r) => [r.credential, r.status])).toEqual(
    expect.arrayContaining([
      [accepted.label, 200],
      ['rejected', 401],
    ]),
  );
});
