/**
 * PAC-148 AC-13: the organisation member list must not let anyone at a
 * consumer email domain enumerate Paceday accounts (spec
 * docs/specs/PAC-148.md AC-13; contract contracts/features/PAC-148.md §13).
 *
 * The viewer (seed/ids.ts PAC148_VIEWER) holds a verified address at a mail
 * provider on no consumer list (ISP), bound on mailbox evidence: what a
 * Google sign-in with no hosted domain leaves. Two other ISP accounts, one
 * mailbox-bound and one domain-confirmed, exist. All three are associated
 * with ISP's organisation, as the rule before PAC-148 left them at each
 * sign-in; the seed writes the associations directly and nothing re-runs a
 * sign-in, so this journey proves the rule is applied at READ time on the
 * real stack. (The sign-in itself cannot be driven here: the Google OAuth
 * callback is not configurable to the stand-in, auth-session.spec.ts and
 * SEAM-REQUIRED.md item B; the sign-in step is pinned by the backend's
 * TestPAC148_AC1_ConsumerDomainSeesNoOne.)
 *
 * Before PAC-148 the member list returned all three, Settings showed the
 * organisation AI section and the SSO step-up entry, and the two seeded
 * addresses showed "On Paceday" on the viewer's roster.
 *
 * Web hooks: org-ai-section (TESTIDS-REQUIRED.md), the SSO step-up section's
 * `#sso-admin` landmark, manager-member-row-<email>, manager-paceday-badge.
 */

import { test, expect } from '../fixtures';
import { PAC148_ISP_ACCOUNTS, PAC148_VIEWER } from '../seed/ids';

const VIEWER = PAC148_VIEWER;

test.describe('PAC-148 — a consumer-domain account sees no one', () => {
  test('AC-13: no member list, no organisation features, no "On Paceday" badge', async ({ apiAs, pageAs, assertNotDemo }) => {
    const api = apiAs(VIEWER);

    // The member list is empty, and /api/auth/me carries no organisation.
    const members = await api.get('/api/org/members');
    expect(members.status(), await members.text()).toBe(200);
    expect(await members.json()).toEqual([]);
    const me = await (await api.get('/api/auth/me')).json();
    expect(me).toMatchObject({ id: VIEWER.id, addressStanding: 'unconfirmed' });
    expect(me).not.toHaveProperty('org');
    const settings = await (await api.get('/api/settings')).json();
    expect(settings.aiStatus.inOrganization).toBe(false);
    const options = await api.get('/api/admin/sso/stepup/options');
    expect(options.status()).toBe(403);
    expect(await options.json()).toEqual({ error: 'org membership required' });

    // Settings: no organisation AI section and no SSO step-up entry.
    const page = await pageAs(VIEWER, { isManager: true });
    try {
      const optionsSeen = page.waitForResponse(
        (r) => r.url().includes('/api/admin/sso/stepup/options') && r.request().method() === 'GET',
      );
      await page.goto('/settings');
      await assertNotDemo(page);
      await expect(page.getByLabel('AI provider')).toBeVisible();
      expect((await optionsSeen).status()).toBe(403);
      await expect(page.getByLabel('AI provider').locator('option[value="organization"]:not([disabled])')).toHaveCount(0);
      await expect(page.getByTestId('org-ai-section')).toHaveCount(0);
      await expect(page.locator('#sso-admin')).toHaveCount(0);
    } finally {
      await page.close();
    }

    // The Manager roster: neither seeded ISP address is "On Paceday".
    const run = `${Date.now()}${Math.random().toString(36).slice(2, 6)}`;
    const created = await api.post('/api/teams', { name: `E2E PAC-148 ${run}` });
    expect(created.status(), await created.text()).toBe(201);
    const team = (await created.json()) as { id: string };
    try {
      const addrs = [PAC148_ISP_ACCOUNTS.mailbox.email, PAC148_ISP_ACCOUNTS.confirmed.email];
      for (const addr of addrs) {
        const added = await api.post(`/api/manager/team/members?team_id=${team.id}`, {
          email: addr,
          display_name: `E2E ${addr.split('@')[0]}`,
          cadence: 'weekly',
        });
        expect(added.status(), await added.text()).toBe(201);
      }
      const roster = await (await api.get(`/api/manager/team?team_id=${team.id}`)).json();
      for (const addr of addrs) {
        const entry = (roster.members as Array<{ email: string; is_paceday_user: boolean }>).find((m) => m.email === addr);
        expect(entry, `${addr} on the roster`).toBeDefined();
        expect(entry!.is_paceday_user, `${addr} is_paceday_user`).toBe(false);
      }
      const teamPage = await pageAs(VIEWER, { isManager: true });
      try {
        await teamPage.goto('/app/team');
        await assertNotDemo(teamPage);
        for (const addr of addrs) {
          const row = teamPage.getByTestId(`manager-member-row-${addr}`);
          await expect(row).toBeVisible();
          await expect(row.getByTestId('manager-paceday-badge')).toHaveCount(0);
        }
      } finally {
        await teamPage.close();
      }
    } finally {
      const deleted = await api.delete(`/api/teams/${team.id}`);
      expect([200, 204]).toContain(deleted.status());
    }
  });
});
