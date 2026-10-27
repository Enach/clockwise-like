/**
 * PAC-40: the scheduling-link lifecycle, seen from the app.
 *
 * AC-10: an owner deletes a link on the Links page; after a reload it is gone
 * from the page, its public booking page shows the not-found state, and its
 * row is still in the database (a soft delete, spec D-2).
 *
 * AC-18: a single-use link that was booked once stays used up when that
 * booking is cancelled (spec D-6). Nothing in Paceday cancels a booking yet,
 * so the test sets the status in the database, on a link seeded for this test
 * alone (LINKS.cancelledSingleUse), never the shared exhausted link.
 */

import { test, expect } from '../fixtures';
import { LINKS } from '../seed/ids';

test.describe('scheduling-link lifecycle (PAC-40)', () => {
  test('AC-10: a link deleted on the Links page is gone after a reload, and its booking page is not found', async ({
    authedPage,
    authedApi,
    page,
    api,
    db,
    assertNotDemo,
  }) => {
    const suffix = Date.now();
    const slug = `e2e-pac40-delete-${suffix}`;
    const title = `E2E PAC-40 Delete ${suffix}`;
    const created = await authedApi.post('/api/scheduling-links', {
      title,
      slug,
      duration_options: [30],
      days_of_week: [1, 2, 3, 4, 5],
      window_start_time: '09:00',
      window_end_time: '10:00',
      min_notice_minutes: 0,
      usage_type: 'reusable',
    });
    expect(created.status()).toBe(201);
    const link = (await created.json()) as { id: string };

    try {
      expect((await api.get(`/api/book/${slug}`)).status()).toBe(200);

      await authedPage.goto('/app/links');
      const del = authedPage.getByRole('button', { name: `Delete ${title}`, exact: true });
      await expect(del).toBeVisible();
      authedPage.once('dialog', (d) => d.accept());
      await del.click();
      await expect(del).toHaveCount(0);

      await authedPage.reload();
      // The page has loaded the list again: the seeded owned link is on it.
      await expect(
        authedPage.getByRole('button', { name: `Delete ${LINKS.owned.title}`, exact: true }),
      ).toBeVisible();
      await expect(authedPage.getByRole('button', { name: `Delete ${title}`, exact: true })).toHaveCount(0);
      await expect(authedPage.getByText(title, { exact: true })).toHaveCount(0);

      await page.goto(`/book/${slug}`);
      await expect(page.getByRole('heading', { name: /link not found/i })).toBeVisible();
      await assertNotDemo(page);

      // Not found through the API too, and nothing was destroyed.
      expect((await authedApi.get(`/api/scheduling-links/${link.id}`)).status()).toBe(404);
      const rows = await db.query('SELECT active FROM scheduling_links WHERE id = $1', [link.id]);
      expect(rows).toEqual([expect.objectContaining({ active: false })]);
    } finally {
      await db.query('DELETE FROM scheduling_links WHERE slug = $1', [slug]);
    }
  });

  test('AC-18: a single-use link stays used up when its booking is cancelled', async ({
    page,
    api,
    db,
    assertNotDemo,
  }) => {
    const link = LINKS.cancelledSingleUse;
    // Precondition from the seed: booked once, so already used up.
    expect((await api.get(`/api/book/${link.slug}`)).status()).toBe(410);

    await db.query(`UPDATE bookings SET status = 'cancelled' WHERE id = $1`, [link.bookingId]);
    const rows = await db.query('SELECT status FROM bookings WHERE link_id = $1', [link.id]);
    expect(rows).toEqual([expect.objectContaining({ status: 'cancelled' })]);

    expect((await api.get(`/api/book/${link.slug}`)).status()).toBe(410);
    await page.goto(`/book/${link.slug}`);
    await expect(page.getByRole('heading', { name: /link no longer available/i })).toBeVisible();
    await assertNotDemo(page);
  });
});
