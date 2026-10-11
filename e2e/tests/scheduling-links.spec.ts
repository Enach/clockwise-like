/**
 * Create a scheduling link as an authenticated user, then book it as an
 * anonymous visitor.
 *
 * This is the complete round trip the factory spec asks for, and it needs no
 * calendar provider at any step:
 *   POST /api/scheduling-links  — handlers_scheduling_links.go createLink():
 *       validates, generates a slug from the owner's name via
 *       engine.BookingEngine.GenerateSlug, writes the link, adds the owner as
 *       an ACCEPTED host. Pure database.
 *   GET  /api/book/{slug}       — the anonymous visitor sees the link that was
 *       just created, by a slug the server chose.
 *   POST /api/book/{slug}       — the booking lands, and the owner sees it on
 *       GET /api/scheduling-links/{id}/bookings.
 *
 * Isolation: each test creates its OWN link with a unique slug and its own
 * booker email, and deletes both at the end, so nothing here depends on or
 * disturbs the seeded fixtures or another test.
 */

import { test, expect } from '../fixtures';
import { LINKS, USERS } from '../seed/ids';
import { atUtc, instant, isoDate, nextBookableWeekday } from '../lib/dates';

test.describe('scheduling links', () => {
  test('a user creates a link and an anonymous visitor books it', async ({
    authedApi,
    api,
    db,
  }) => {
    const suffix = Date.now();
    const slug = `e2e-created-${suffix}`;
    const bookerEmail = `e2e.booker.created.${suffix}@paceday.test`;

    // ── create ────────────────────────────────────────────────────────────
    const created = await authedApi.post('/api/scheduling-links', {
      title: 'E2E Created Link',
      slug,
      duration_options: [30],
      days_of_week: [1, 2, 3, 4, 5],
      // A window that no seeded link uses, so the owner's other links can
      // never make these slots look busy.
      window_start_time: '16:00',
      window_end_time: '17:00',
      min_notice_minutes: 0,
      usage_type: 'reusable',
    });
    expect(created.status()).toBe(201);

    const link = await created.json();
    expect(link.slug).toBe(slug);
    expect(link.title).toBe('E2E Created Link');

    try {
      // The owner must have been added as an accepted host automatically;
      // without that the public link has zero hosts and no slots.
      const hosts = await db.query(
        `SELECT user_id, status FROM scheduling_link_hosts WHERE link_id = $1`,
        [link.id],
      );
      expect(hosts).toEqual([
        expect.objectContaining({ user_id: USERS.primary.id, status: 'accepted' }),
      ]);

      // ── the anonymous visitor ───────────────────────────────────────────
      const publicRes = await api.get(`/api/book/${slug}`);
      expect(publicRes.status()).toBe(200);
      const publicLink = await publicRes.json();
      expect(publicLink.title).toBe('E2E Created Link');
      expect(publicLink.hosts.map((h: { email: string }) => h.email)).toEqual([
        USERS.primary.email,
      ]);

      const date = nextBookableWeekday(14);
      const slotsRes = await api.get(`/api/book/${slug}/slots`, {
        date: isoDate(date),
        duration: 30,
      });
      const { slots } = await slotsRes.json();
      // 16:00–17:00 at a 15-minute stride with a 30-minute duration => 16:00,
      // 16:15, 16:30. Exact, because the arithmetic is deterministic.
      expect(slots.map((s: { start: string }) => instant(s.start))).toEqual([
        atUtc(date, '16:00'),
        atUtc(date, '16:15'),
        atUtc(date, '16:30'),
      ]);

      const booked = await api.post(`/api/book/${slug}`, {
        name: 'E2E Anonymous Visitor',
        email: bookerEmail,
        start: atUtc(date, '16:00'),
        duration_minutes: 30,
      });
      expect(booked.status()).toBe(201);
      const confirmation = await booked.json();
      expect(instant(confirmation.end)).toBe(atUtc(date, '16:30'));

      // ── back on the owner's side ────────────────────────────────────────
      const bookings = await authedApi.get(`/api/scheduling-links/${link.id}/bookings`);
      expect(bookings.status()).toBe(200);
      const list = await bookings.json();
      expect(list).toEqual([
        expect.objectContaining({
          booker_name: 'E2E Anonymous Visitor',
          booker_email: bookerEmail,
        }),
      ]);
    } finally {
      await db.deleteBookingsByBooker('e2e.booker.created.');
      await db.query('DELETE FROM scheduling_links WHERE slug = $1', [slug]);
    }
  });

  test('the owner sees their seeded link in the list, scoped to them', async ({
    authedApi,
  }) => {
    const res = await authedApi.get('/api/scheduling-links');
    expect(res.status()).toBe(200);
    // The list is {owned, shared} (contract: listSchedulingLinks). Check both
    // halves so a leaked link is caught wherever it would appear.
    const body = (await res.json()) as { owned: { slug: string }[]; shared: { slug: string }[] };
    const owned = body.owned.map((l) => l.slug);
    const slugs = [...owned, ...body.shared.map((l) => l.slug)];

    expect(owned).toContain(LINKS.owned.slug);
    expect(owned).toContain(LINKS.uiBooking.slug);
    // Owned by the second user — must not leak across accounts.
    expect(slugs).not.toContain(LINKS.apiBooking.slug);
  });

  test('a duplicate slug is refused', async ({ authedApi }) => {
    const res = await authedApi.post('/api/scheduling-links', {
      title: 'E2E Duplicate',
      slug: LINKS.owned.slug,
      duration_options: [30],
    });
    expect(res.status()).toBe(409);
  });

  test('creating a link requires a session', async ({ api }) => {
    const res = await api.post('/api/scheduling-links', {
      title: 'E2E Should Not Exist',
      duration_options: [30],
    });
    expect(res.status()).toBe(401);
  });

  // Driven entirely through roles and labels: since smart-calendar-flow#91 the
  // create drawer's inputs have <label htmlFor> and its toggles aria-pressed,
  // which TESTIDS-REQUIRED.md #2 accepts in place of test ids.
  test('a link created through the Links page appears on the public booking page', async ({
    authedPage,
    authedApi,
    page,
    db,
  }) => {
    const title = `E2E UI Link ${Date.now()}`;
    try {
      await authedPage.goto('/app/links');
      await authedPage.getByRole('button', { name: /new link/i }).click();
      const drawer = authedPage.getByRole('dialog', { name: /new scheduling link/i });
      await expect(drawer).toBeVisible();

      await drawer.getByLabel('Title', { exact: true }).fill(title);
      const thirty = drawer.getByRole('button', { name: '30 min', exact: true });
      if ((await thirty.getAttribute('aria-pressed')) !== 'true') await thirty.click();
      await expect(thirty).toHaveAttribute('aria-pressed', 'true');
      await drawer.getByLabel('Start', { exact: true }).fill('09:00');
      await drawer.getByLabel('End', { exact: true }).fill('17:00');

      await drawer.getByRole('button', { name: /create link/i }).click();
      await expect(drawer).toBeHidden();

      // The server has it, owned by this user.
      const res = await authedApi.get('/api/scheduling-links');
      expect(res.status()).toBe(200);
      const { owned } = (await res.json()) as { owned: { slug: string; title: string }[] };
      const created = owned.find((l) => l.title === title);
      expect(created).toBeDefined();

      // The card shows the URL Copy puts on the clipboard (smart-calendar-flow#89).
      await expect(authedPage.getByText(`/book/${created!.slug}`)).toBeVisible();

      // And an anonymous visitor reaches it.
      await page.goto(`/book/${created!.slug}`);
      await expect(page.getByRole('heading', { name: title })).toBeVisible();
    } finally {
      await db.query('DELETE FROM scheduling_links WHERE title = $1', [title]);
    }
  });
});
