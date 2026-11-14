/**
 * PAC-40 (parts A and B): the scheduling-link lifecycle through the full
 * stack (nginx -> backend -> Postgres, and the built web app for the UI
 * journeys).
 *
 * AC-10: an owner deletes a link on the Links page; after a reload it is gone
 * from the page and its public booking page shows the not-found state. The
 * row is kept (deleted_at set, active false: spec D-2, migration 037).
 *
 * AC-25 (part B: booker_erasure.enabled is true in this build): the delete
 * confirmation says the link and its bookings move to the archive AND that
 * bookers' names and emails are erased 90 days after the last booking; the
 * archive then shows the link with its booking, the booker's name and email
 * and the erasure date; and it offers no control that edits, resumes,
 * restores or deletes the link.
 *
 * AC-32 (part B, rendered through the stack): a seeded deleted link whose
 * booking was already erased (LINKS.pac40Erased) is in the erased state in
 * the archive, the booking shows "erased" and no name or email. The job
 * itself (clock, schedule, transaction) is proven by the backend's unit
 * tests (engine/booker_erasure_test.go, main_pac40_test.go): it runs at
 * start-up and every 24 h and has no clock this suite can move.
 *
 * AC-18: a dedicated single-use link booked once stays used up when that
 * booking is cancelled (D-6). Nothing in Paceday cancels a booking yet, so
 * the test sets the status in the database, on LINKS.pac40CancelledSingleUse
 * alone, never the shared exhausted link.
 *
 * The seeded deleted link (LINKS.pac40Archived, owned by USERS.second) pins
 * AC-4, AC-21 (with part B's due instant), AC-22 and AC-23 against the real
 * stack.
 *
 * The UI journeys use the test ids of smart-calendar-flow#150 (PAC-40 part A
 * web), listed in e2e/TESTIDS-REQUIRED.md section 8.
 */

import { test, expect, type Db } from '../fixtures';
import { LINKS, MISSING_SLUG, USERS } from '../seed/ids';

const NOT_FOUND = { error: 'not found' };
const ARCHIVED = LINKS.pac40Archived;
const ERASED = LINKS.pac40Erased;
const RETENTION_MS = 90 * 24 * 60 * 60 * 1000;

/** end + 90 x 24 h, as the server serialises it (UTC, Z suffix). */
function dueAfter(end: string): string {
  return new Date(Date.parse(end) + RETENTION_MS).toISOString().replace('.000Z', 'Z');
}

/** A reusable link owned by the primary user, with a run-unique slug. */
async function createOwnedLink(
  authedApi: { post: (p: string, d?: unknown) => Promise<{ status(): number; json(): Promise<unknown> }> },
  label: string,
) {
  const run = `${Date.now()}${Math.floor(Math.random() * 1000)}`;
  const slug = `e2e-pac40-${label}-${run}`;
  const title = `E2E PAC-40 ${label} ${run}`;
  const res = await authedApi.post('/api/scheduling-links', {
    title,
    slug,
    duration_options: [30],
    days_of_week: [1, 2, 3, 4, 5],
    window_start_time: '09:00',
    window_end_time: '10:00',
    min_notice_minutes: 0,
    usage_type: 'reusable',
  });
  expect(res.status()).toBe(201);
  const { id } = (await res.json()) as { id: string };
  return { id, slug, title, run };
}

async function removeLink(db: Db, id: string) {
  await db.query('DELETE FROM bookings WHERE link_id = $1', [id]);
  await db.query('DELETE FROM scheduling_links WHERE id = $1', [id]);
}

test.describe('scheduling-link lifecycle (PAC-40)', () => {
  test('AC-10: a link deleted on the Links page is gone after a reload, and its booking page is not found', async ({
    authedPage,
    authedApi,
    page,
    api,
    db,
    assertNotDemo,
  }) => {
    const link = await createOwnedLink(authedApi, 'delete');
    try {
      expect((await api.get(`/api/book/${link.slug}`)).status()).toBe(200);

      await authedPage.goto('/app/links');
      await assertNotDemo(authedPage);
      const row = authedPage.getByTestId(`link-row-${link.slug}`);
      await expect(row).toBeVisible();
      await authedPage.getByRole('button', { name: `Delete ${link.title}`, exact: true }).click();
      const dialog = authedPage.getByTestId('link-delete-dialog');
      await expect(dialog).toBeVisible();
      const deleted = authedPage.waitForResponse(
        (r) => r.request().method() === 'DELETE' && r.url().endsWith(`/api/scheduling-links/${link.id}`),
      );
      await dialog.getByTestId('link-delete-confirm').click();
      expect((await deleted).status()).toBe(204);
      await expect(row).toHaveCount(0);

      await authedPage.reload();
      // The list has loaded again: the seeded owned link is on it.
      await expect(authedPage.getByTestId(`link-row-${LINKS.owned.slug}`)).toBeVisible();
      await expect(authedPage.getByTestId(`link-row-${link.slug}`)).toHaveCount(0);
      await expect(authedPage.getByText(link.title, { exact: true })).toHaveCount(0);

      await page.goto(`/book/${link.slug}`);
      await expect(page.getByRole('heading', { name: /link not found/i })).toBeVisible();
      await assertNotDemo(page);

      // The same answer as a slug that never existed, and the row is kept.
      const gone = await api.get(`/api/book/${link.slug}`);
      expect(gone.status()).toBe(404);
      expect(await gone.json()).toEqual(await (await api.get(`/api/book/${MISSING_SLUG}`)).json());
      const rows = await db.query<{ active: boolean; deleted: boolean }>(
        'SELECT active, deleted_at IS NOT NULL AS deleted FROM scheduling_links WHERE id = $1',
        [link.id],
      );
      expect(rows).toEqual([{ active: false, deleted: true }]);
    } finally {
      await removeLink(db, link.id);
    }
  });

  test('AC-25: delete moves the link and its booking to a read-only archive and promises erasure 90 days after the last booking', async ({
    authedPage,
    authedApi,
    db,
    assertNotDemo,
  }) => {
    const link = await createOwnedLink(authedApi, 'archive');
    const booker = { name: `E2E PAC-40 Booker ${link.run}`, email: `e2e.booker.pac40.${link.run}@paceday.test` };
    try {
      const booking = await db.queryOne<{ id: string }>(
        `INSERT INTO bookings (link_id, booker_name, booker_email, start_time, end_time, notes)
         VALUES ($1, $2, $3, '2020-02-03T09:00:00Z', '2020-02-03T09:30:00Z', 'PAC-40 e2e')
         RETURNING id`,
        [link.id, booker.name, booker.email],
      );

      await authedPage.goto('/app/links');
      await assertNotDemo(authedPage);
      await authedPage.getByRole('button', { name: `Delete ${link.title}`, exact: true }).click();
      const dialog = authedPage.getByTestId('link-delete-dialog');
      const copy = dialog.getByTestId('link-delete-copy');
      await expect(copy).toContainText(/no longer be able to book/i);
      await expect(copy).toContainText(/archive/i);
      // enabled=true in part B: the erasure sentence quotes retention_days
      // (contract §2.4); its copy is the web's, the figure is the server's.
      const erasure = dialog.getByTestId('link-delete-erasure');
      await expect(erasure).toBeVisible();
      await expect(erasure).toContainText(/names and emails/i);
      await expect(erasure).toContainText(/\b90 days\b/);
      await dialog.getByTestId('link-delete-confirm').click();
      await expect(authedPage.getByTestId(`link-row-${link.slug}`)).toHaveCount(0);

      await authedPage.getByTestId('links-archive-link').click();
      await expect(authedPage).toHaveURL(/\/app\/links\/archive$/);
      const archive = authedPage.getByTestId('archive-page');
      await expect(archive).toBeVisible();
      const row = archive.getByTestId(`archive-row-${link.slug}`);
      await expect(row).toBeVisible();
      await expect(row).toContainText(link.title);
      await expect(row).toHaveAttribute('data-erasure-state', 'awaiting_erasure');
      // The erasure date: 2020-02-03T09:30Z + 90 x 24 h = 2020-05-03T09:30Z.
      const date = row.getByTestId('archive-erasure');
      await expect(date).toContainText(/will be erased on/i);
      await expect(date).toContainText(/May/);
      await expect(date).toContainText(/2020/);
      const listed = (await (await authedApi.get('/api/scheduling-links/archive')).json()) as {
        links: Array<{ id: string; erasure_due_at: string | null }>;
        booker_erasure: unknown;
      };
      expect(listed.booker_erasure).toEqual({ enabled: true, retention_days: 90 });
      expect(listed.links.find((l) => l.id === link.id)?.erasure_due_at).toBe('2020-05-03T09:30:00Z');

      await row.getByTestId('archive-view-bookings').click();
      const detail = authedPage.getByTestId('archive-detail');
      await expect(detail).toBeVisible();
      const item = detail.getByTestId(`archive-booking-${booking!.id}`);
      await expect(item).toHaveAttribute('data-erased', 'false');
      await expect(item.getByTestId('archive-booker-name')).toHaveText(booker.name);
      await expect(item.getByTestId('archive-booker-email')).toHaveText(booker.email);
      await expect(item.getByTestId('archive-booker-erased')).toHaveCount(0);
      await expect(item.getByTestId('archive-booking-status')).toContainText(/confirmed/i);

      // Read-only: nothing in the archive edits, resumes, restores or deletes.
      const mutating = /edit|resume|restore|delete|pause|invite/i;
      await expect(archive.getByRole('button', { name: mutating })).toHaveCount(0);
      await expect(detail.getByRole('button', { name: mutating })).toHaveCount(0);
      await expect(archive.getByRole('switch')).toHaveCount(0);
    } finally {
      await removeLink(db, link.id);
    }
  });

  test('a seeded deleted link is gone from every live path and in its owner\'s archive only (AC-4, AC-21, AC-22, AC-23)', async ({
    api,
    authedApi,
    apiAs,
  }) => {
    const owner = apiAs(USERS.second);

    // AC-4: the public page, slots and booking answer as for a missing slug.
    const missing = await api.get(`/api/book/${MISSING_SLUG}`);
    for (const res of [
      await api.get(`/api/book/${ARCHIVED.slug}`),
      await api.get(`/api/book/${ARCHIVED.slug}/slots`, { date: '2030-01-07', duration: 30 }),
      await api.post(`/api/book/${ARCHIVED.slug}`, {
        name: 'Nobody',
        email: 'e2e.booker.pac40.nobody@paceday.test',
        start: '2030-01-07T10:00:00Z',
        duration: 30,
      }),
    ]) {
      expect(res.status()).toBe(404);
      expect(await res.json()).toEqual(await missing.json());
    }

    // AC-1, AC-2: not in the owner's list, not fetchable by its owner.
    const list = (await (await owner.get('/api/scheduling-links')).json()) as {
      owned: Array<{ id: string }>;
      shared: Array<{ id: string }>;
    };
    expect([...list.owned, ...list.shared].map((l) => l.id)).not.toContain(ARCHIVED.id);
    const fetched = await owner.get(`/api/scheduling-links/${ARCHIVED.id}`);
    expect(fetched.status()).toBe(404);
    expect(await fetched.json()).toEqual(NOT_FOUND);

    // AC-21: in the owner's archive, with its erasure date (part B: policy on).
    const archive = await owner.get('/api/scheduling-links/archive');
    expect(archive.status()).toBe(200);
    const body = (await archive.json()) as { links: Array<Record<string, unknown>>; booker_erasure: unknown };
    expect(body.booker_erasure).toEqual({ enabled: true, retention_days: 90 });
    expect(body.links.find((l) => l.id === ARCHIVED.id)).toEqual({
      id: ARCHIVED.id,
      title: ARCHIVED.title,
      slug: ARCHIVED.slug,
      deleted_at: ARCHIVED.deletedAt,
      bookings_count: 1,
      last_booking_end_at: ARCHIVED.booking.end,
      erasure_state: 'awaiting_erasure',
      erasure_due_at: dueAfter(ARCHIVED.booking.end),
      erased_at: null,
    });

    // AC-22: its booking, with the booker's name and email, through the archive.
    const detail = await owner.get(`/api/scheduling-links/archive/${ARCHIVED.id}`);
    expect(detail.status()).toBe(200);
    const d = (await detail.json()) as { bookings: Array<Record<string, unknown>> };
    expect(d.bookings).toEqual([
      expect.objectContaining({
        id: ARCHIVED.booking.id,
        booker_name: ARCHIVED.booking.name,
        booker_email: ARCHIVED.booking.email,
        start_time: ARCHIVED.booking.start,
        end_time: ARCHIVED.booking.end,
        booker_erased_at: null,
      }),
    ]);

    // AC-23: another user gets the never-existed answer and an archive without it.
    const other = await authedApi.get(`/api/scheduling-links/archive/${ARCHIVED.id}`);
    const unknown = await authedApi.get('/api/scheduling-links/archive/00000000-0000-4000-8000-000000000000');
    expect(other.status()).toBe(404);
    expect(await other.text()).toBe(await unknown.text());
    const theirs = (await (await authedApi.get('/api/scheduling-links/archive')).json()) as { links: Array<{ id: string }> };
    expect(theirs.links.map((l) => l.id)).not.toContain(ARCHIVED.id);
  });

  test('AC-32: an erased booking reads as erased, never as its old or a blank name and email', async ({
    apiAs,
    secondUserPage,
    assertNotDemo,
  }) => {
    const owner = apiAs(USERS.second);

    // The API: erased state, erased_at, no due date; the booking keeps its
    // times, status and notes, with null name and email and the instant.
    const list = (await (await owner.get('/api/scheduling-links/archive')).json()) as {
      links: Array<Record<string, unknown>>;
    };
    expect(list.links.find((l) => l.id === ERASED.id)).toEqual({
      id: ERASED.id,
      title: ERASED.title,
      slug: ERASED.slug,
      deleted_at: ERASED.deletedAt,
      bookings_count: 1,
      last_booking_end_at: ERASED.booking.end,
      erasure_state: 'erased',
      erasure_due_at: null,
      erased_at: ERASED.booking.erasedAt,
    });
    const detail = await owner.get(`/api/scheduling-links/archive/${ERASED.id}`);
    expect(detail.status()).toBe(200);
    const d = (await detail.json()) as { bookings: Array<Record<string, unknown>> };
    expect(d.bookings).toEqual([
      expect.objectContaining({
        id: ERASED.booking.id,
        booker_name: null,
        booker_email: null,
        booker_erased_at: ERASED.booking.erasedAt,
        start_time: ERASED.booking.start,
        end_time: ERASED.booking.end,
        status: 'confirmed',
        notes: ERASED.booking.notes,
      }),
    ]);

    // The page: the row is erased with its date, the booking says so.
    await secondUserPage.goto('/app/links/archive');
    await assertNotDemo(secondUserPage);
    const archive = secondUserPage.getByTestId('archive-page');
    const row = archive.getByTestId(`archive-row-${ERASED.slug}`);
    await expect(row).toHaveAttribute('data-erasure-state', 'erased');
    await expect(row.getByTestId('archive-erasure')).toContainText(/erased on/i);
    await row.getByTestId('archive-view-bookings').click();
    const item = secondUserPage.getByTestId('archive-detail').getByTestId(`archive-booking-${ERASED.booking.id}`);
    await expect(item).toHaveAttribute('data-erased', 'true');
    await expect(item.getByTestId('archive-booker-erased')).toBeVisible();
    await expect(item.getByTestId('archive-booker-name')).toHaveCount(0);
    await expect(item.getByTestId('archive-booker-email')).toHaveCount(0);
  });

  test('AC-18: a single-use link stays used up when its booking is cancelled', async ({
    page,
    api,
    db,
    assertNotDemo,
  }) => {
    const link = LINKS.pac40CancelledSingleUse;
    // From the seed: booked once, so already used up.
    expect((await api.get(`/api/book/${link.slug}`)).status()).toBe(410);

    await db.query(`UPDATE bookings SET status = 'cancelled' WHERE id = $1`, [link.bookingId]);
    const rows = await db.query<{ status: string }>('SELECT status FROM bookings WHERE link_id = $1', [link.id]);
    expect(rows).toEqual([{ status: 'cancelled' }]);

    const res = await api.get(`/api/book/${link.slug}`);
    expect(res.status()).toBe(410);
    expect(await res.json()).toEqual({ error: 'link is no longer accepting bookings' });
    await page.goto(`/book/${link.slug}`);
    await expect(page.getByRole('heading', { name: /link no longer available/i })).toBeVisible();
    await assertNotDemo(page);
  });
});
