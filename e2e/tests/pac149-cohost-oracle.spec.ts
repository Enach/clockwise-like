/**
 * PAC-149 (spec docs/specs/PAC-149.md AC-12; contract
 * contracts/features/PAC-149.md §5): a co-host invitation is to an address, so
 * nothing the owner sees in the link editor depends on whether the address
 * belongs to a Paceday account. Full stack: nginx -> backend -> Postgres and
 * the production frontend bundle (the paired smart-calendar-flow branch);
 * nothing is mocked or routed.
 *
 * One run invites H (an address held domain-confirmed by an account that has
 * nothing to do with the owner), the other N (an address no account carries),
 * through the link editor, on links with the same fields. After the save and a
 * reopen of the editor, the editor's rendered text with the address and the
 * title replaced is identical in the two runs, and neither says "couldn't be
 * added". Before PAC-149 the N run showed "couldn't be added as a co-host" and
 * the H run did not.
 *
 * Each test owns its users and links (run-unique) and removes them, so it runs
 * in any order and in parallel. Selectors are roles and accessible names, as in
 * pac112-cohosts.spec.ts.
 */

import type { Page } from '@playwright/test';
import { test, expect, type ApiClient, type Db } from '../fixtures';
import type { SessionUser } from '../auth';

function runId(): string {
  return `${Date.now()}${Math.floor(Math.random() * 100000)}`;
}

/** A fresh account holding its address domain-confirmed. */
async function freshUser(db: Db, email: string, name: string): Promise<SessionUser> {
  const row = await db.queryOne<{ id: string }>(
    `INSERT INTO users (email, name, address_standing) VALUES ($1, $2, 'domain_confirmed') RETURNING id`,
    [email, name],
  );
  await db.query(
    `INSERT INTO user_identities (user_id, provider, tenant, subject, evidence, bound_by)
     VALUES ($1, 'google', '', $2, 'authoritative', 'created')`,
    [row!.id, `pac149-${row!.id}`],
  );
  return { id: row!.id, email, name };
}

async function newLink(api: ApiClient, title: string): Promise<string> {
  const created = await api.post('/api/scheduling-links', {
    title,
    duration_options: [30],
    days_of_week: [1, 2, 3, 4, 5],
    window_start_time: '09:00',
    window_end_time: '17:00',
    min_notice_minutes: 0,
    usage_type: 'reusable',
  });
  expect(created.status(), await created.text()).toBe(201);
  return ((await created.json()) as { id: string }).id;
}

async function openEditor(page: Page, title: string) {
  await page.goto('/app/links');
  await page.getByRole('button', { name: `Edit ${title}`, exact: true }).click();
  const drawer = page.getByRole('dialog', { name: /edit link/i });
  await expect(drawer).toBeVisible();
  await expect(drawer.getByLabel('Co-host email')).toBeEnabled();
  return drawer;
}

/**
 * Invites addr on a new link titled title through the editor, then reopens
 * the editor and returns its rendered text with addr and title masked.
 */
async function inviteThroughTheEditor(page: Page, api: ApiClient, title: string, addr: string): Promise<string> {
  const id = await newLink(api, title);
  const drawer = await openEditor(page, title);
  await drawer.getByLabel('Co-host email').fill(addr);
  await drawer.getByRole('button', { name: 'Add', exact: true }).click();
  await expect(drawer.getByRole('button', { name: `Remove ${addr}`, exact: true })).toBeVisible();
  const saved = page.waitForResponse(
    (r) => r.request().method() === 'PATCH' && new URL(r.url()).pathname === `/api/scheduling-links/${id}`,
  );
  await drawer.getByRole('button', { name: 'Save changes' }).click();
  const res = await saved;
  expect(res.status(), await res.text()).toBe(200);
  const body = (await res.json()) as { co_host_changes: { addresses: Array<{ email: string; outcome: string }> } };
  expect(body.co_host_changes.addresses).toEqual([{ email: addr, outcome: 'invited' }]);
  await expect(drawer).toBeHidden();

  await page.reload();
  const again = await openEditor(page, title);
  await expect(again.getByRole('button', { name: `Remove ${addr}`, exact: true })).toBeVisible();
  await expect(again.getByText('Invite pending')).toBeVisible();
  const text = await again.innerText();
  expect(text).not.toContain("couldn't be added");
  return text.split(addr).join('<ADDR>').split(title).join('<TITLE>');
}

test.describe('PAC-149 the link editor shows an invitation the same whoever holds the address', () => {
  test('AC-12: inviting H and inviting N render the same editor', async ({ db, apiAs, pageAs }) => {
    const run = runId();
    const owner = await freshUser(db, `e2e.pac149.owner.${run}@pac149.e2e.test`, `E2E PAC149 Owner ${run}`);
    // Equal length, so nothing in the layout can differ by the address.
    const h = await freshUser(db, `e2e.pac149.hold.${run}@pac149.e2e.test`, `E2E PAC149 Holder ${run}`);
    const n = `e2e.pac149.none.${run}@pac149.e2e.test`;
    try {
      const page = await pageAs(owner);
      const api = apiAs(owner);
      const withH = await inviteThroughTheEditor(page, api, `E2E PAC149 AC-12 H ${run}`, h.email);
      const withN = await inviteThroughTheEditor(page, api, `E2E PAC149 AC-12 N ${run}`, n);
      expect(withN).toBe(withH);
      // The holder's account is not on the link until it answers.
      const rows = await db.query<{ n: string }>(
        'SELECT count(*) AS n FROM scheduling_link_hosts WHERE user_id = $1',
        [h.id],
      );
      expect(rows[0]!.n).toBe('0');
    } finally {
      await db.query('DELETE FROM scheduling_links WHERE owner_user_id = $1', [owner.id]);
      await db.query('DELETE FROM users WHERE id = ANY($1::uuid[])', [[owner.id, h.id]]);
    }
  });
});
