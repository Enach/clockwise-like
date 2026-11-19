/**
 * PAC-119 (contracts/features/PAC-119.md, spec docs/specs/PAC-119.md §4): a
 * link create is safe to retry after a lost answer. Full stack: nginx ->
 * backend -> Postgres and the production frontend bundle (the paired
 * smart-calendar-flow branch factory/pac-119).
 *
 * Journeys (spec ACs marked [e2e]):
 * - AC-25: the first create reaches the real server and is saved; only its
 *   answer is dropped at the browser's network layer (page.route: route.fetch
 *   sends the real request, then route.abort('failed') loses the answer, which
 *   is exactly a network failure after the server committed, the PAC-82
 *   AC-28 precedent in public-booking.spec.ts). Nothing is mocked: the second
 *   press goes to the server untouched and is answered by it with the replay.
 * - AC-15 (e2e half): two identical keyless API creates are two links; the
 *   existing API and UI create journeys keep passing unchanged.
 * - AC-26 lives in the two existing UI-create journeys
 *   (scheduling-links.spec.ts "creates a link through the editor" and
 *   pac112-cohosts.spec.ts AC-24), which now also assert one keyed POST per
 *   press.
 *
 * Each test owns its users and links (run-unique, domain-confirmed like the
 * PAC-112 journeys) and removes them at the end; the key rows go with the
 * links (ON DELETE CASCADE). Selectors are roles and accessible names: the
 * editor dialog ("New scheduling link", then "Edit link" once the create
 * answered and the editor stayed open), "Create link", "Co-host email",
 * "Add", "Remove <email>", the role=alert message (contract md §7.4 texts,
 * U+2014 dashes) and the "Save results" status.
 */

import type { Locator, Page } from '@playwright/test';
import { test, expect, type Db } from '../fixtures';
import type { SessionUser } from '../auth';

const OUTCOME_UNKNOWN =
  "Paceday didn't confirm that this link was created. Press Create link again — it won't create a second link.";
const KEY_FORMAT = /^[A-Za-z0-9_-]{16,255}$/;

function runId(): string {
  return `${Date.now()}${Math.floor(Math.random() * 100000)}`;
}

/** A fresh account holding its address domain-confirmed (PAC-71 grant rule). */
async function freshUser(db: Db, label: string, run: string): Promise<SessionUser> {
  const email = `e2e.pac119.${label}.${run}@pac119.e2e.test`;
  const name = `E2E PAC119 ${label} ${run}`;
  const row = await db.queryOne<{ id: string }>(
    `INSERT INTO users (email, name, address_standing) VALUES ($1, $2, 'domain_confirmed') RETURNING id`,
    [email, name],
  );
  await db.query(
    `INSERT INTO user_identities (user_id, provider, tenant, subject, evidence, bound_by)
     VALUES ($1, 'google', '', $2, 'authoritative', 'created')`,
    [row!.id, `pac119-${row!.id}`],
  );
  return { id: row!.id, email, name };
}

async function cleanup(db: Db, users: SessionUser[]) {
  const ids = users.map((u) => u.id);
  await db.query('DELETE FROM scheduling_links WHERE owner_user_id = ANY($1::uuid[])', [ids]);
  await db.query('DELETE FROM users WHERE id = ANY($1::uuid[])', [ids]);
}

const isCreate = (method: string, url: string) =>
  method === 'POST' && new URL(url).pathname.replace(/\/$/, '') === '/api/scheduling-links';

async function fillNewLink(page: Page, title: string): Promise<Locator> {
  await page.goto('/app/links');
  await page.getByRole('button', { name: /new link/i }).click();
  const drawer = page.getByRole('dialog', { name: /new scheduling link/i });
  await expect(drawer).toBeVisible();
  await drawer.getByLabel('Title', { exact: true }).fill(title);
  const thirty = drawer.getByRole('button', { name: '30 min', exact: true });
  if ((await thirty.getAttribute('aria-pressed')) !== 'true') await thirty.click();
  await expect(thirty).toHaveAttribute('aria-pressed', 'true');
  await drawer.getByLabel('Start', { exact: true }).fill('09:00');
  await drawer.getByLabel('End', { exact: true }).fill('17:00');
  return drawer;
}

async function addCoHost(drawer: Locator, email: string) {
  await drawer.getByLabel('Co-host email').fill(email);
  await drawer.getByRole('button', { name: 'Add', exact: true }).click();
  await expect(drawer.getByRole('button', { name: `Remove ${email}`, exact: true })).toBeVisible();
}

test.describe('PAC-119 link create is safe to retry', () => {
  test('AC-25: an answer lost after the server saved; pressing Create link again makes no second link', async ({
    db,
    apiAs,
    pageAs,
  }) => {
    test.setTimeout(90_000);
    const run = runId();
    const a = await freshUser(db, 'ac25-owner', run);
    const b = await freshUser(db, 'ac25-b', run);
    // C carries no account, so the save reports it not added and the editor
    // stays open with its results (PAC-112 §5.4), where B's outcome shows.
    const c = `e2e.pac119.ac25-nobody.${run}@pac119.e2e.test`;
    const page = await pageAs(a);
    const posts: Array<{ key: string | null; body: string | null }> = [];
    let firstStatus: number | undefined;
    await page.route(
      (url) => url.pathname.replace(/\/$/, '') === '/api/scheduling-links',
      async (route) => {
        const req = route.request();
        if (req.method() !== 'POST') return route.continue();
        posts.push({ key: await req.headerValue('idempotency-key'), body: req.postData() });
        if (posts.length === 1) {
          // The server commits the link; the browser never gets the answer.
          const res = await route.fetch();
          firstStatus = res.status();
          return route.abort('failed');
        }
        return route.continue();
      },
    );
    try {
      const title = `E2E PAC119 AC-25 ${run}`;
      const drawer = await fillNewLink(page, title);
      await addCoHost(drawer, b.email);
      await addCoHost(drawer, c);

      await drawer.getByRole('button', { name: 'Create link', exact: true }).click();
      // The answer is lost: the editor stays a create editor and says that
      // pressing again will not create a second link.
      await expect(drawer.getByRole('alert')).toContainText(OUTCOME_UNKNOWN);
      await expect(drawer.getByRole('alert')).not.toContainText('refresh to check');
      expect(firstStatus, 'the first request reached the server and was saved').toBe(201);
      expect(posts).toHaveLength(1);
      const saved = await db.query<{ id: string }>('SELECT id FROM scheduling_links WHERE owner_user_id = $1', [a.id]);
      expect(saved).toHaveLength(1);

      const replayed = page.waitForResponse((r) => isCreate(r.request().method(), r.url()));
      await drawer.getByRole('button', { name: 'Create link', exact: true }).click();
      const replay = await replayed;
      expect(replay.status()).toBe(201);
      const { id } = (await replay.json()) as { id: string };
      expect(id).toBe(saved[0]!.id);

      // One attempt, sent twice: the same key and the same body.
      expect(posts).toHaveLength(2);
      expect(posts[0]!.key).toMatch(KEY_FORMAT);
      expect(posts[1]!.key).toBe(posts[0]!.key);
      expect(posts[1]!.body).toBe(posts[0]!.body);

      // The editor reports B invited, from the replayed answer; it stayed
      // open and is now the editor of the created link ("Edit link").
      const editor = page.getByRole('dialog', { name: /edit link/i });
      await expect(editor).toBeVisible();
      const results = editor.getByRole('status', { name: 'Save results' });
      await expect(results).toContainText('1 co-host invited — they can accept from Paceday.');
      await expect(results).toContainText(`${c} couldn't be added as a co-host.`);
      await expect(editor.getByRole('alert')).toHaveCount(0);

      // Exactly one new link; B invited to it once; one key row.
      const links = await db.query<{ id: string; title: string }>(
        'SELECT id, title FROM scheduling_links WHERE owner_user_id = $1',
        [a.id],
      );
      expect(links).toEqual([{ id, title }]);
      // PAC-149: B's pending invitation holds B's address, not B's account.
      const bRows = await db.query<{ link_id: string; status: string }>(
        'SELECT link_id, status FROM scheduling_link_hosts WHERE user_id IS NULL AND lower(invitee_email) = lower($1)',
        [b.email],
      );
      expect(bRows).toEqual([{ link_id: id, status: 'pending' }]);
      const invites = await apiAs(b).get('/api/scheduling-links/host-invites');
      expect(invites.status()).toBe(200);
      const invited = ((await invites.json()) as Array<{ link_id: string }>).filter((i) => i.link_id === id);
      expect(invited).toHaveLength(1);
      const keys = await db.query<{ link_id: string }>(
        'SELECT link_id FROM scheduling_link_create_keys WHERE owner_user_id = $1',
        [a.id],
      );
      expect(keys).toEqual([{ link_id: id }]);
    } finally {
      await page.unrouteAll({ behavior: 'ignoreErrors' });
      await cleanup(db, [a, b]);
    }
  });

  test('AC-15: two identical keyless creates are two links', async ({ db, apiAs }) => {
    const run = runId();
    const a = await freshUser(db, 'ac15-owner', run);
    try {
      const body = {
        title: `E2E PAC119 AC-15 ${run}`,
        duration_options: [30],
        days_of_week: [1, 2, 3, 4, 5],
        window_start_time: '09:00',
        window_end_time: '17:00',
      };
      const api = apiAs(a);
      const first = await api.post('/api/scheduling-links', body);
      const second = await api.post('/api/scheduling-links', body);
      expect(first.status(), await first.text()).toBe(201);
      expect(second.status(), await second.text()).toBe(201);
      const ids = [((await first.json()) as { id: string }).id, ((await second.json()) as { id: string }).id];
      expect(ids[0]).not.toBe(ids[1]);
      const rows = await db.query<{ n: string }>('SELECT count(*) AS n FROM scheduling_links WHERE owner_user_id = $1', [a.id]);
      expect(Number(rows[0]!.n)).toBe(2);
      const keys = await db.query<{ n: string }>(
        'SELECT count(*) AS n FROM scheduling_link_create_keys WHERE owner_user_id = $1',
        [a.id],
      );
      expect(Number(keys[0]!.n)).toBe(0);
    } finally {
      await cleanup(db, [a]);
    }
  });
});
