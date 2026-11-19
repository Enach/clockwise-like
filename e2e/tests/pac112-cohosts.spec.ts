/**
 * PAC-112 (contracts/features/PAC-112.md, spec docs/specs/PAC-112.md §4): the
 * link editor saves its co-host list with the link, and every save says what
 * happened to each address. Full stack: nginx -> backend -> Postgres and the
 * production frontend bundle (the paired smart-calendar-flow branch); nothing
 * is mocked or routed.
 *
 * Journeys (spec ACs marked [e2e]): AC-1, AC-2, AC-3 (host and booking half,
 * and the public-slots half: PAC-118), AC-4, AC-11, AC-24, AC-32.
 *
 * Each test owns its users (run-unique emails, domain-confirmed like the seeded
 * ones, so they can be invited: PAC-71) and its links, and removes them at the
 * end, so the tests run in any order and in parallel. The exception is AC-3's
 * public-slots half, whose owner and co-host must be connected through the
 * calendar stand-in: it uses the seeded pair seed/ids.ts PAC112_HOSTS, which
 * no other journey touches, and removes only its own link and stand-in
 * events. Setup goes through the
 * API (create, invite, accept); the change under test goes through the
 * editor; the outcome is asserted in the editor, through the API after a
 * reload, and in the database rows.
 *
 * Selectors are roles and accessible names: the editor dialog ("Edit link" /
 * "New scheduling link"), the "Edit <title>" button on the Links page, the
 * "Co-host email" field, "Add", "Remove <email>", "Save changes" / "Create
 * link", and the "Save results" status (contract md §5.3 texts).
 */

import type { Page } from '@playwright/test';
import { test, expect, type ApiClient, type Db } from '../fixtures';
import type { SessionUser } from '../auth';
import { fake } from '../fake';
import { atUtc, instant, isoDate, nextBookableWeekday } from '../lib/dates';
import { PAC112_HOSTS } from '../seed/ids';

type Api = ApiClient;

function runId(): string {
  return `${Date.now()}${Math.floor(Math.random() * 100000)}`;
}

/** A fresh account holding its address domain-confirmed (PAC-71 grant rule). */
async function freshUser(db: Db, label: string, run: string): Promise<SessionUser> {
  const email = `e2e.pac112.${label}.${run}@pac112.e2e.test`;
  const name = `E2E PAC112 ${label} ${run}`;
  const row = await db.queryOne<{ id: string }>(
    `INSERT INTO users (email, name, address_standing) VALUES ($1, $2, 'domain_confirmed') RETURNING id`,
    [email, name],
  );
  await db.query(
    `INSERT INTO user_identities (user_id, provider, tenant, subject, evidence, bound_by)
     VALUES ($1, 'google', '', $2, 'authoritative', 'created')`,
    [row!.id, `pac112-${row!.id}`],
  );
  return { id: row!.id, email, name };
}

async function cleanup(db: Db, users: SessionUser[]) {
  const ids = users.map((u) => u.id);
  await db.query('DELETE FROM scheduling_links WHERE owner_user_id = ANY($1::uuid[])', [ids]);
  await db.query('DELETE FROM users WHERE id = ANY($1::uuid[])', [ids]);
}

/** A link owned by `owner`, through the API, with `invite` invited one by one. */
async function linkWith(api: Api, title: string, invite: SessionUser[] = []): Promise<string> {
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
  const { id } = (await created.json()) as { id: string };
  for (const u of invite) {
    // The single-invitation operation. Since PAC-120 it refuses a sixth
    // co-host (422), so AC-32 seeds its 7 host rows directly instead.
    const res = await api.post(`/api/scheduling-links/${id}/hosts`, { email: u.email });
    expect(res.status(), await res.text()).toBe(201);
  }
  return id;
}

async function respond(api: Api, linkId: string, answer: 'accept' | 'decline') {
  const res = await api.post(`/api/scheduling-links/host-invites/${linkId}/${answer}`);
  expect(res.status(), await res.text()).toBe(200);
}

/**
 * The link's host rows keyed by account. PAC-149: a pending invitation points
 * at no account (it holds the address); this fixture view keys it by the one
 * account whose stored address equals it ignoring case, or by the address when
 * none does. The server never makes that match on the owner's side.
 */
async function hostRows(db: Db, linkId: string): Promise<Record<string, string>> {
  const rows = await db.query<{ k: string; status: string }>(
    `SELECT COALESCE(h.user_id::text,
              (SELECT u.id::text FROM users u WHERE lower(u.email) = lower(h.invitee_email)
                 AND NOT EXISTS (SELECT 1 FROM users v WHERE lower(v.email) = lower(h.invitee_email) AND v.id <> u.id)),
              h.invitee_email) AS k, h.status
       FROM scheduling_link_hosts h WHERE h.link_id = $1`,
    [linkId],
  );
  return Object.fromEntries(rows.map((r) => [r.k, r.status]));
}

/** The roster from the strict single-link read, non-owner hosts only. */
async function coHostsOnServer(api: Api, linkId: string): Promise<Record<string, string>> {
  const res = await api.get(`/api/scheduling-links/${linkId}`);
  expect(res.status(), await res.text()).toBe(200);
  const body = (await res.json()) as { hosts: Array<{ email: string; status: string; is_owner: boolean }> };
  return Object.fromEntries(body.hosts.filter((h) => !h.is_owner).map((h) => [h.email, h.status]));
}

async function pendingInviteLinkIds(api: Api): Promise<string[]> {
  const res = await api.get('/api/scheduling-links/host-invites');
  expect(res.status(), await res.text()).toBe(200);
  return ((await res.json()) as Array<{ link_id: string }>).map((i) => i.link_id);
}

/** Opens the editor on an existing link and waits for its co-host baseline. */
async function openEditor(page: Page, title: string) {
  await page.goto('/app/links');
  await page.getByRole('button', { name: `Edit ${title}`, exact: true }).click();
  const drawer = page.getByRole('dialog', { name: /edit link/i });
  await expect(drawer).toBeVisible();
  // Contract md §5.1: co-host editing is enabled only once the strict read
  // has answered.
  await expect(drawer.getByLabel('Co-host email')).toBeEnabled();
  return drawer;
}

async function addCoHost(drawer: ReturnType<Page['getByRole']>, email: string) {
  await drawer.getByLabel('Co-host email').fill(email);
  await drawer.getByRole('button', { name: 'Add', exact: true }).click();
  await expect(drawer.getByRole('button', { name: `Remove ${email}`, exact: true })).toBeVisible();
}

test.describe('PAC-112 the link editor saves its co-host list', () => {
  test('AC-1: removing a pending co-host removes them; the accepted one stays', async ({ db, apiAs, pageAs }) => {
    const run = runId();
    const a = await freshUser(db, 'ac1-owner', run);
    const e = await freshUser(db, 'ac1-e', run);
    const b = await freshUser(db, 'ac1-b', run);
    try {
      const title = `E2E PAC112 AC-1 ${run}`;
      const id = await linkWith(apiAs(a), title, [e, b]);
      await respond(apiAs(e), id, 'accept');

      const page = await pageAs(a);
      const drawer = await openEditor(page, title);
      await expect(drawer.getByRole('button', { name: `Remove ${e.email}`, exact: true })).toBeVisible();
      await drawer.getByRole('button', { name: `Remove ${b.email}`, exact: true }).click();
      await drawer.getByRole('button', { name: 'Save changes' }).click();
      await expect(drawer).toBeHidden();

      await page.reload();
      const again = await openEditor(page, title);
      await expect(again.getByRole('button', { name: `Remove ${e.email}`, exact: true })).toBeVisible();
      await expect(again.getByRole('button', { name: `Remove ${b.email}`, exact: true })).toHaveCount(0);

      expect(await coHostsOnServer(apiAs(a), id)).toEqual({ [e.email]: 'accepted' });
      expect(await pendingInviteLinkIds(apiAs(b))).not.toContain(id);
      expect(await hostRows(db, id)).toEqual({ [a.id]: 'accepted', [e.id]: 'accepted' });
    } finally {
      await cleanup(db, [a, e, b]);
    }
  });

  test('AC-2: removing the only co-host leaves none', async ({ db, apiAs, pageAs }) => {
    const run = runId();
    const a = await freshUser(db, 'ac2-owner', run);
    const b = await freshUser(db, 'ac2-b', run);
    try {
      const title = `E2E PAC112 AC-2 ${run}`;
      const id = await linkWith(apiAs(a), title, [b]);
      expect(await pendingInviteLinkIds(apiAs(b))).toContain(id);

      const page = await pageAs(a);
      const drawer = await openEditor(page, title);
      await drawer.getByRole('button', { name: `Remove ${b.email}`, exact: true }).click();
      await drawer.getByRole('button', { name: 'Save changes' }).click();
      await expect(drawer).toBeHidden();

      await page.reload();
      const again = await openEditor(page, title);
      await expect(again.getByRole('button', { name: /^Remove / })).toHaveCount(0);
      expect(await coHostsOnServer(apiAs(a), id)).toEqual({});
      expect(await pendingInviteLinkIds(apiAs(b))).not.toContain(id);
      expect(await hostRows(db, id)).toEqual({ [a.id]: 'accepted' });
    } finally {
      await cleanup(db, [a, b]);
    }
  });

  test('AC-3: removing an accepted co-host removes them and keeps an earlier booking', async ({ db, apiAs, pageAs }) => {
    const run = runId();
    const a = await freshUser(db, 'ac3-owner', run);
    const e = await freshUser(db, 'ac3-e', run);
    try {
      const title = `E2E PAC112 AC-3 ${run}`;
      const id = await linkWith(apiAs(a), title, [e]);
      await respond(apiAs(e), id, 'accept');
      // A booking made before the save (setup only; the booking flow itself is
      // covered by public-booking.spec.ts).
      const booked = await db.queryOne<{ id: string }>(
        `INSERT INTO bookings (link_id, booker_name, booker_email, start_time, end_time, status, notes)
         VALUES ($1, 'E2E PAC112 Booker', $2, now() + interval '30 days', now() + interval '30 days 30 minutes', 'confirmed', 'before the save')
         RETURNING id`,
        [id, `e2e.booker.pac112.${run}@paceday.test`],
      );
      const before = await db.queryOne('SELECT * FROM bookings WHERE id = $1', [booked!.id]);

      const page = await pageAs(a);
      const drawer = await openEditor(page, title);
      await drawer.getByRole('button', { name: `Remove ${e.email}`, exact: true }).click();
      await drawer.getByRole('button', { name: 'Save changes' }).click();
      await expect(drawer).toBeHidden();

      await page.reload();
      const again = await openEditor(page, title);
      await expect(again.getByRole('button', { name: `Remove ${e.email}`, exact: true })).toHaveCount(0);
      expect(await coHostsOnServer(apiAs(a), id)).toEqual({});
      expect(await hostRows(db, id)).toEqual({ [a.id]: 'accepted' });
      expect(await db.queryOne('SELECT * FROM bookings WHERE id = $1', [booked!.id])).toEqual(before);
    } finally {
      await db.deleteBookingsByBooker(`e2e.booker.pac112.${run}`);
      await cleanup(db, [a, e]);
    }
  });

  // PAC-118: the public-slots clause. Needs an owner and a co-host whose
  // calendars the stand-in answers (an unreadable one is busy, PAC-72 D-6),
  // so it uses the seeded connected pair (seed/ids.ts PAC112_HOSTS), which
  // this journey alone owns. The link is run-unique; the co-host's calendar
  // is cleared before and after.
  test("AC-3: a removed co-host's busy time no longer removes the link's public slots", async ({
    api,
    db,
    apiAs,
    pageAs,
  }) => {
    const a = PAC112_HOSTS.owner;
    const e = PAC112_HOSTS.coHost;
    const run = runId();
    const title = `E2E PAC112 AC-3 slots ${run}`;
    const day = nextBookableWeekday();
    await fake.clearCalendar(e.email);
    try {
      // Monday to Friday 09:00-17:00 in the owner's zone (UTC), 30 minutes.
      const id = await linkWith(apiAs(a), title, [e]);
      await respond(apiAs(e), id, 'accept');
      expect(await hostRows(db, id)).toEqual({ [a.id]: 'accepted', [e.id]: 'accepted' });
      const link = await db.queryOne<{ slug: string }>('SELECT slug FROM scheduling_links WHERE id = $1', [id]);

      // The co-host has a meeting 10:00-10:30 UTC that day.
      await fake.seedEvents(e.email, [
        {
          summary: 'E2E PAC-112 co-host meeting',
          start: { dateTime: atUtc(day, '10:00') },
          end: { dateTime: atUtc(day, '10:30') },
        },
      ]);

      const publicStarts = async (): Promise<string[]> => {
        const res = await api.get(`/api/book/${link!.slug}/slots`, { date: isoDate(day), duration: 30 });
        expect(res.status(), await res.text()).toBe(200);
        const { slots } = (await res.json()) as { slots: Array<{ start: string }> };
        return slots.map((sl) => instant(sl.start));
      };
      // Every 30-minute start from 09:00 to 16:30 on the 15-minute stride
      // (engine/booking.go CollectiveSlots).
      const allStarts: string[] = [];
      for (let m = 9 * 60; m <= 16 * 60 + 30; m += 15) {
        allStarts.push(atUtc(day, `${String(Math.floor(m / 60)).padStart(2, '0')}:${String(m % 60).padStart(2, '0')}`));
      }
      const overlapping = ['09:45', '10:00', '10:15'].map((t) => atUtc(day, t));

      // While E is an accepted co-host, E's meeting removes the starts that
      // overlap it, and only those (the owner is free and readable).
      expect(await publicStarts()).toEqual(allStarts.filter((t) => !overlapping.includes(t)));

      const page = await pageAs(a);
      const drawer = await openEditor(page, title);
      await drawer.getByRole('button', { name: `Remove ${e.email}`, exact: true }).click();
      await drawer.getByRole('button', { name: 'Save changes' }).click();
      await expect(drawer).toBeHidden();

      await page.reload();
      const again = await openEditor(page, title);
      await expect(again.getByRole('button', { name: `Remove ${e.email}`, exact: true })).toHaveCount(0);
      expect(await hostRows(db, id)).toEqual({ [a.id]: 'accepted' });

      // E's meeting is still on E's calendar, and no longer removes a slot.
      expect((await fake.events(e.email)).map((ev) => ev.summary)).toContain('E2E PAC-112 co-host meeting');
      expect(await publicStarts()).toEqual(allStarts);
    } finally {
      // The pair is seeded, so the users stay. Every link of the owner goes,
      // deliberately: only this journey creates links for that user, and it
      // also removes one an earlier killed attempt left behind.
      await db.query('DELETE FROM scheduling_links WHERE owner_user_id = $1', [a.id]);
      await fake.clearCalendar(e.email);
    }
  });

  test('AC-4: adding G invites G and keeps the accepted and declined statuses', async ({ db, apiAs, pageAs }) => {
    const run = runId();
    const a = await freshUser(db, 'ac4-owner', run);
    const e = await freshUser(db, 'ac4-e', run);
    const f = await freshUser(db, 'ac4-f', run);
    const g = await freshUser(db, 'ac4-g', run);
    try {
      const title = `E2E PAC112 AC-4 ${run}`;
      const id = await linkWith(apiAs(a), title, [e, f]);
      await respond(apiAs(e), id, 'accept');
      await respond(apiAs(f), id, 'decline');

      const page = await pageAs(a);
      const drawer = await openEditor(page, title);
      await addCoHost(drawer, g.email);
      const saved = page.waitForResponse(
        (r) => r.request().method() === 'PATCH' && new URL(r.url()).pathname === `/api/scheduling-links/${id}`,
      );
      await drawer.getByRole('button', { name: 'Save changes' }).click();
      const res = await saved;
      // One request carrying the complete list (AC-5's server half).
      expect(new Set(res.request().postDataJSON().co_host_emails)).toEqual(new Set([e.email, f.email, g.email]));
      await expect(drawer).toBeHidden();

      expect(await hostRows(db, id)).toEqual({
        [a.id]: 'accepted',
        [e.id]: 'accepted',
        [f.id]: 'declined',
        [g.id]: 'pending',
      });
      expect(await pendingInviteLinkIds(apiAs(g))).toContain(id);
    } finally {
      await cleanup(db, [a, e, f, g]);
    }
  });

  // PAC-149 (spec AC-12): not_added now means only "not a well-formed
  // address", so the not-added case is a malformed address (one the editor's
  // own check lets through: a double dot), no longer an address nobody holds.
  test("AC-11: an address that can't be added is named, the invitee is counted once", async ({ db, apiAs, pageAs }) => {
    const run = runId();
    const a = await freshUser(db, 'ac11-owner', run);
    const b = await freshUser(db, 'ac11-b', run);
    const c = `e2e.pac112.ac11-bad..${run}@pac112.e2e.test`;
    try {
      const title = `E2E PAC112 AC-11 ${run}`;
      const id = await linkWith(apiAs(a), title);

      const page = await pageAs(a);
      const drawer = await openEditor(page, title);
      await addCoHost(drawer, b.email);
      await addCoHost(drawer, c);
      await drawer.getByRole('button', { name: 'Save changes' }).click();

      // The editor stays open and names C (contract md §5.3, §5.4).
      const results = drawer.getByRole('status', { name: 'Save results' });
      await expect(results).toContainText(`${c} isn't a valid email address.`);
      await expect(results).toContainText("1 co-host invited. Paceday doesn't email invitations, so let them know.");
      await expect(results).toContainText("1 address wasn't a valid email address.");
      await expect(drawer).toBeVisible();
      // After the baseline re-read: B's chip from the server, C's gone.
      await expect(drawer.getByRole('button', { name: `Remove ${b.email}`, exact: true })).toBeVisible();
      await expect(drawer.getByRole('button', { name: `Remove ${c}`, exact: true })).toHaveCount(0);
      const text = (await results.innerText()).toLowerCase();
      for (const banned of ['no paceday account', 'they can accept from paceday', "couldn't be added"]) {
        expect(text, `the save results say "${banned}"`).not.toContain(banned);
      }

      await page.reload();
      expect(await coHostsOnServer(apiAs(a), id)).toEqual({ [b.email]: 'pending' });
      expect(await hostRows(db, id)).toEqual({ [a.id]: 'accepted', [b.id]: 'pending' });
    } finally {
      await cleanup(db, [a, b]);
    }
  });

  test('AC-24: after a create that stays open, the next save edits the created link', async ({ db, pageAs }) => {
    const run = runId();
    const a = await freshUser(db, 'ac24-owner', run);
    const c = `e2e.pac112.ac24-bad..${run}@pac112.e2e.test`; // PAC-149: malformed
    try {
      const title = `E2E PAC112 AC-24 ${run}`;
      const page = await pageAs(a);
      await page.goto('/app/links');
      await page.getByRole('button', { name: /new link/i }).click();
      const drawer = page.getByRole('dialog', { name: /new scheduling link/i });
      await expect(drawer).toBeVisible();
      await drawer.getByLabel('Title', { exact: true }).fill(title);
      const thirty = drawer.getByRole('button', { name: '30 min', exact: true });
      if ((await thirty.getAttribute('aria-pressed')) !== 'true') await thirty.click();
      await drawer.getByLabel('Start', { exact: true }).fill('09:00');
      await drawer.getByLabel('End', { exact: true }).fill('17:00');
      await addCoHost(drawer, c);

      const requests: Array<{ method: string; path: string; key: string | null }> = [];
      page.on('request', (r) => {
        const path = new URL(r.url()).pathname;
        if (path.startsWith('/api/scheduling-links') && ['POST', 'PATCH'].includes(r.method())) {
          requests.push({ method: r.method(), path, key: r.headers()['idempotency-key'] ?? null });
        }
      });
      const created = page.waitForResponse(
        (r) => r.request().method() === 'POST' && new URL(r.url()).pathname.replace(/\/$/, '') === '/api/scheduling-links',
      );
      await drawer.getByRole('button', { name: /create link/i }).click();
      const createdRes = await created;
      expect(createdRes.status()).toBe(201);
      const { id } = (await createdRes.json()) as { id: string };

      // Stays open (C not added), now editing the created link: the same
      // drawer is retitled "Edit link" (contract md §5.4), so it is located
      // again by its new name, and the old name matches nothing.
      const editor = page.getByRole('dialog', { name: /edit link/i });
      await expect(editor).toBeVisible();
      await expect(page.getByRole('dialog', { name: /new scheduling link/i })).toHaveCount(0);
      const results = editor.getByRole('status', { name: 'Save results' });
      await expect(results).toContainText(`${c} isn't a valid email address.`);
      await expect(editor.getByRole('button', { name: 'Save changes' })).toBeVisible();
      await expect(editor.getByLabel('Co-host email')).toBeEnabled();

      const renamed = `${title} renamed`;
      await editor.getByLabel('Title', { exact: true }).fill(renamed);
      const patched = page.waitForResponse(
        (r) => r.request().method() === 'PATCH' && new URL(r.url()).pathname === `/api/scheduling-links/${id}`,
      );
      await editor.getByRole('button', { name: 'Save changes' }).click();
      expect((await patched).status()).toBe(200);

      expect(requests.filter((r) => r.method === 'POST')).toHaveLength(1);
      // PAC-119 AC-26 and AC-18: the one create carries an attempt key; the
      // edit that follows carries none.
      expect(requests.find((r) => r.method === 'POST')!.key).toMatch(/^[A-Za-z0-9_-]{16,255}$/);
      expect(requests.filter((r) => r.method === 'PATCH').map((r) => r.key)).toEqual([null]);
      const links = await db.query<{ id: string; title: string }>(
        'SELECT id, title FROM scheduling_links WHERE owner_user_id = $1',
        [a.id],
      );
      expect(links).toEqual([{ id, title: renamed }]);
    } finally {
      await cleanup(db, [a]);
    }
  });

  test('AC-32: a link with 7 co-hosts can lose one; the other 6 keep their status', async ({ db, apiAs, pageAs }) => {
    const run = runId();
    const a = await freshUser(db, 'ac32-owner', run);
    const hosts: SessionUser[] = [];
    for (let i = 0; i < 7; i++) hosts.push(await freshUser(db, `ac32-h${i}`, run));
    try {
      const title = `E2E PAC112 AC-32 ${run}`;
      const id = await linkWith(apiAs(a), title);
      // PAC-120 (contract md §7.1): the single invitation stops at 5 co-hosts,
      // so the 7 pending invitations are seeded in one statement, as after 7
      // invitations (PAC-149: to the addresses; a pending row points at no
      // account). They share invited_at; no assertion below depends on order.
      // Accept and decline still go through the API.
      await db.query(
        `INSERT INTO scheduling_link_hosts (link_id, invitee_email, status) SELECT $1, unnest($2::text[]), 'pending'`,
        [id, hosts.map((h) => h.email)],
      );
      await respond(apiAs(hosts[1]!), id, 'accept');
      await respond(apiAs(hosts[2]!), id, 'decline');
      await respond(apiAs(hosts[3]!), id, 'accept');
      const before = await hostRows(db, id);

      const page = await pageAs(a);
      const drawer = await openEditor(page, title);
      for (const h of hosts) {
        await expect(drawer.getByRole('button', { name: `Remove ${h.email}`, exact: true })).toBeVisible();
      }
      await drawer.getByRole('button', { name: `Remove ${hosts[1]!.email}`, exact: true }).click();
      await drawer.getByRole('button', { name: 'Save changes' }).click();
      await expect(drawer).toBeHidden();

      const expected = { ...before };
      delete expected[hosts[1]!.id];
      expect(await hostRows(db, id)).toEqual(expected);
      expect(Object.keys(expected)).toHaveLength(7); // the owner and 6 co-hosts
    } finally {
      await cleanup(db, [a, ...hosts]);
    }
  });
});
