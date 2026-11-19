/**
 * PAC-130: Manager mode must not reveal whether an email address has a
 * Paceday account (spec docs/specs/PAC-130.md AC-6; contract
 * contracts/features/PAC-130.md §2, §5).
 *
 *   AC-6  one run whose roster is {H}, another whose roster is {N}, with the
 *         same name, cadence and stand-in free/busy: the expanded row reads
 *         the same with the address replaced, neither shows "On Paceday",
 *         and the "degraded" banner is in the same state.
 *
 * H is held, domain-confirmed, by an account that shares no team and no
 * organisation with the viewer, shares analytics with managers and has a
 * stored week full of focus; N is held by nobody. Before PAC-130 the {H} run
 * showed "On Paceday" and the banner (H's figures were withheld), and the {N}
 * run an estimate, so only the real server can make this fail: it is e2e
 * (spec challenge B-5).
 *
 * Viewer: CONNECTED.calendar, whose free/busy credential the stand-in accepts.
 * Each run creates its own team, named to sort first so the Team page opens on
 * it, and deletes it afterwards. H and N are run-unique and the same length,
 * so the participant cache, the analytics cache and other journeys cannot
 * answer for them. The account holding H is seeded through the database
 * (db.ts: seeding only; every assertion is on what the browser shows).
 *
 * Web hooks (TESTIDS-REQUIRED.md §3): manager-member-row-<email>,
 * manager-paceday-badge, manager-estimate-note, manager-degraded-banner.
 */

import { test, expect } from '../fixtures';
import { fake } from '../fake';
import { query } from '../db';
import { CONNECTED } from '../seed/ids';
import { atUtc, DAY_MS, isoDate, mondayOf } from '../lib/dates';

const VIEWER = CONNECTED.calendar;
const NAME = 'E2E Same Person';

interface RunResult {
  text: string;
  badges: number;
  banner: number;
  note: number;
}

test.describe('PAC-130 — Manager mode reveals no account existence', () => {
  test('AC-6: a roster of {H} and a roster of {N} render the same', async ({ apiAs, pageAs, assertNotDemo }) => {
    const run = `${Date.now()}${Math.random().toString(36).slice(2, 6)}`;
    const h = `e2e.pac130.h.${run}@ext.e2e.test`;
    const n = `e2e.pac130.n.${run}@ext.e2e.test`;
    expect(h.length).toBe(n.length);

    // The same busy hour for both, this week and the eleven before it.
    const monday = mondayOf(new Date());
    for (const addr of [h, n]) {
      await fake.seedEvents(
        addr,
        Array.from({ length: 12 }, (_, i) => {
          const day = new Date(monday.getTime() - i * 7 * DAY_MS + DAY_MS);
          return { summary: 'busy', start: { dateTime: atUtc(day, '10:00') }, end: { dateTime: atUtc(day, '11:00') } };
        }),
      );
    }

    // H: one domain-confirmed account outside the viewer's teams and
    // organisation, sharing analytics, with a stored week of focus.
    const [held] = await query<{ id: string }>(
      `INSERT INTO users (email, email_key, name, provider, address_standing)
       VALUES ($1, lower($1), 'E2E Held', 'google', 'domain_confirmed') RETURNING id`,
      [h],
    );
    if (!held) throw new Error('the account holding H was not created');
    await query(`INSERT INTO user_profiles (user_id, analytics_shared_with_manager) VALUES ($1, true)`, [held.id]);
    await query(
      `INSERT INTO analytics_weeks (user_id, week_start, focus_minutes, meeting_minutes, free_minutes)
       VALUES ($1, $2, 615, 240, 1545)`,
      [held.id, isoDate(monday)],
    );

    const api = apiAs(VIEWER);
    const once = async (addr: string, tag: string): Promise<RunResult> => {
      const created = await api.post('/api/teams', { name: `0000 PAC-130 ${tag} ${run}` });
      expect(created.status(), await created.text()).toBe(201);
      const team = (await created.json()) as { id: string };
      try {
        const added = await api.post(`/api/manager/team/members?team_id=${team.id}`, {
          email: addr,
          display_name: NAME,
          cadence: 'weekly',
        });
        expect(added.status(), await added.text()).toBe(201);
        expect(((await added.json()) as { member_user_id: unknown }).member_user_id).toBeNull();

        const page = await pageAs(VIEWER, { isManager: true });
        const analytics = page.waitForResponse(
          (r) => r.url().includes('/api/manager/analytics') && r.request().method() === 'GET',
        );
        await page.goto('/app/team');
        await assertNotDemo(page);
        const row = page.getByTestId(`manager-member-row-${addr}`);
        await expect(row).toBeVisible();
        expect((await analytics).status()).toBe(200);
        await row.getByRole('button', { name: 'Expand' }).click();
        await expect(row.getByRole('button', { name: 'Collapse' })).toBeVisible();
        const result: RunResult = {
          text: (await row.innerText()).split(addr).join('X'),
          badges: await row.getByTestId('manager-paceday-badge').count(),
          banner: await page.getByTestId('manager-degraded-banner').count(),
          note: await row.getByTestId('manager-estimate-note').count(),
        };
        await page.close();
        return result;
      } finally {
        const deleted = await api.delete(`/api/teams/${team.id}`);
        expect([200, 204]).toContain(deleted.status());
      }
    };

    const withH = await once(h, 'H');
    const withN = await once(n, 'N');

    expect(withH.badges, 'H shows "On Paceday"').toBe(0);
    expect(withN.badges, 'N shows "On Paceday"').toBe(0);
    expect(withH.text).not.toContain('On Paceday');
    expect(withH.banner).toBe(withN.banner);
    expect(withH.note).toBe(withN.note);
    expect(withH.text).toBe(withN.text);
    expect(withH.text).not.toContain('615');
  });
});
