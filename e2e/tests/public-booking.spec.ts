/**
 * Anonymous public booking — the highest-value journey in this suite.
 *
 * PAC-81 Part A, owner decision PAC-72 D-6 (2026-11-01): a host whose calendar
 * cannot be read counts as BUSY. The links booked here are therefore hosted by
 * users connected through the calendar stand-in (seed/ids.ts BOOKING_HOSTS):
 *
 *   GET  /api/book/{slug}        api/handlers_booking.go getLinkInfo — pure
 *                                database: the link, its accepted hosts, and a
 *                                coverage count derived from whether each host
 *                                has an oauth token. No calendar call.
 *   GET  /api/book/{slug}/slots  -> engine.BookingEngine.CollectiveSlots ->
 *                                hostBusy(): the host's calendar is read on
 *                                the stand-in (empty unless a booking wrote an
 *                                event), plus focus_blocks and confirmed
 *                                bookings from the database.
 *   POST /api/book/{slug}        -> engine.ConfirmBooking: every host calendar
 *                                is read first (D-6), the booking row is
 *                                written, then each host's event is created on
 *                                the stand-in. Confirmation email is skipped
 *                                because SMTP_HOST is unset.
 *
 * A booking leaves an event in its host's stand-in calendar, which is busy
 * time on the next listing. Every journey that books removes its bookings AND
 * its host's stand-in events at the end (fake.clearCalendar: the host's
 * calendar belongs to this file alone), so a retry starts clean. Clearing
 * while another journey of this file runs is harmless: their checks read
 * the bookings table, which still holds their own rows.
 *
 * Isolation: this file books against LINKS.uiBooking (host BOOKING_HOSTS.ui,
 * window 09:00–11:00 UTC) and LINKS.apiBooking (host BOOKING_HOSTS.api, window
 * 13:00–15:00 UTC). Different hosts means
 * storage.GetConfirmedBookingsForUser() can never see the other file's rows,
 * so the two can run in either order and in parallel.
 *
 * Within this file, every journey that writes a booking takes its own
 * `bookingDay(index)` — distinct indices are distinct weekdays whatever today
 * is. The journeys used to take `nextBookableWeekday(8..12)`, which collapses
 * three offsets onto one Monday when the suite runs on a Wednesday, so the two
 * UI journeys raced for the same 09:00 slot and the loser failed.
 */

import { test, expect } from '../fixtures';
import { BOOKING_HOSTS, LINKS, MISSING_SLUG } from '../seed/ids';
import { fake } from '../fake';
import { atUtc, bookingDay, instant, isoDate, nextBookableWeekday } from '../lib/dates';
import { pickBookingDate, slotButtons } from '../lib/ui';

test.describe('public booking — link info', () => {
  test('serves a live link to an anonymous visitor with its real hosts', async ({ api }) => {
    const res = await api.get(`/api/book/${LINKS.uiBooking.slug}`);
    expect(res.status()).toBe(200);

    const body = await res.json();
    expect(body).toMatchObject({
      slug: LINKS.uiBooking.slug,
      title: LINKS.uiBooking.title,
      durations: [30],
      usage_type: 'reusable',
      min_notice_minutes: 0,
    });

    // The host list comes from storage.GetAcceptedHosts joined to users — if
    // the join broke, the array would be empty and this would fail.
    expect(body.hosts).toHaveLength(1);
    // Changed with PAC-72 D-6: the host is the connected BOOKING_HOSTS.ui (it
    // was the unconnected primary user, the link's owner).
    expect(body.hosts[0]).toMatchObject({
      email: BOOKING_HOSTS.ui.email,
      name: BOOKING_HOSTS.ui.name,
    });

    // coverage.checked counts hosts with an oauth token: the one host has one,
    // so 1 total / 1 checked (it was 1 / 0 while the host was unconnected).
    expect(body.coverage).toEqual({ total: 1, checked: 1 });
  });

  test('answers 404 for a slug that does not exist', async ({ api }) => {
    const res = await api.get(`/api/book/${MISSING_SLUG}`);
    expect(res.status()).toBe(404);
  });

  test('answers 410 for a single-use link that has already been booked', async ({ api }) => {
    // The seed does not set a counter — it inserts a booking, and
    // storage.scanSchedulingLink derives uses_count from it. This therefore
    // exercises the real exhaustion rule.
    const res = await api.get(`/api/book/${LINKS.exhausted.slug}`);
    expect(res.status()).toBe(410);
  });

  test('renders a "no longer available" page for the exhausted link', async ({
    page,
    assertNotDemo,
  }) => {
    await page.goto(`/book/${LINKS.exhausted.slug}`);
    await expect(
      page.getByRole('heading', { name: /link no longer available/i }),
    ).toBeVisible();
    await assertNotDemo(page);
  });
});

test.describe('public booking — slots', () => {
  test('offers every slot in the configured window when no host is busy', async ({ api }) => {
    const date = nextBookableWeekday();
    const res = await api.get(`/api/book/${LINKS.uiBooking.slug}/slots`, {
      date: isoDate(date),
      duration: 30,
    });
    expect(res.status()).toBe(200);

    const { slots } = await res.json();

    // The window is 09:00–11:00 with a 30-minute duration and a 15-minute
    // stride (engine/booking.go CollectiveSlots), so the exact expected set is
    // 09:00, 09:15 … 10:30 — seven slots. Asserting the exact list, rather than
    // "more than zero", is what makes this fail if the slot maths regresses.
    const expected = ['09:00', '09:15', '09:30', '09:45', '10:00', '10:15', '10:30'].map((t) =>
      atUtc(date, t),
    );
    expect(slots.map((s: { start: string }) => instant(s.start))).toEqual(expected);
    expect(instant(slots[0].end)).toBe(atUtc(date, '09:30'));
  });

  test('rejects a duration the link does not offer', async ({ api }) => {
    const res = await api.get(`/api/book/${LINKS.uiBooking.slug}/slots`, {
      date: isoDate(nextBookableWeekday()),
      duration: 45, // the link offers [30] only
    });
    expect(res.status()).toBe(400);
  });
});

test.describe('public booking — booking through the API', () => {
  test('creates a persisted booking and then refuses the same slot', async ({ api, db }) => {
    const date = bookingDay(1);
    const start = atUtc(date, '13:00');
    const end = atUtc(date, '13:30');
    const bookerEmail = `e2e.booker.api.${Date.now()}@paceday.test`;

    const res = await api.post(`/api/book/${LINKS.apiBooking.slug}`, {
      name: 'E2E API Booker',
      email: bookerEmail,
      start,
      end,
      notes: 'booked by the e2e suite',
    });
    expect(res.status()).toBe(201);

    const conf = await res.json();
    expect(instant(conf.start)).toBe(start);
    expect(instant(conf.end)).toBe(end);
    expect(conf).toMatchObject({
      link_slug: LINKS.apiBooking.slug,
      title: LINKS.apiBooking.title,
      duration_minutes: 30,
      booker_email: bookerEmail,
      notes: 'booked by the e2e suite',
    });
    // The host is BOOKING_HOSTS.api (PAC-72 D-6; it was the second user).
    expect(conf.hosts).toEqual([
      expect.objectContaining({ email: BOOKING_HOSTS.api.email }),
    ]);

    // The response could be fabricated; the row cannot. A 201 without a row is
    // exactly the "passes while broken" case this check exists to catch.
    const row = await db.queryOne<{ id: string; status: string; booker_email: string }>(
      'SELECT id, status, booker_email FROM bookings WHERE id = $1',
      [conf.id],
    );
    expect(row).toBeDefined();
    expect(row!.status).toBe('confirmed');
    expect(row!.booker_email).toBe(bookerEmail);

    // Second attempt at the same slot must hit storage.HasOverlappingBooking.
    const dup = await api.post(`/api/book/${LINKS.apiBooking.slug}`, {
      name: 'E2E Second Booker',
      email: `e2e.booker.apidup.${Date.now()}@paceday.test`,
      start,
      end,
    });
    expect(dup.status()).toBe(409);

    // And the now-taken slot must disappear from the offered list.
    const slotsRes = await api.get(`/api/book/${LINKS.apiBooking.slug}/slots`, {
      date: isoDate(date),
      duration: 30,
    });
    const { slots } = await slotsRes.json();
    expect(slots.map((s: { start: string }) => s.start)).not.toContain(start);

    await db.deleteBookingsByBooker('e2e.booker.api.');
    await fake.clearCalendar(BOOKING_HOSTS.api.email);
  });

  test('rejects a booking with no name or email', async ({ api }) => {
    const date = bookingDay(2);
    const res = await api.post(`/api/book/${LINKS.apiBooking.slug}`, {
      name: '  ',
      email: '',
      start: atUtc(date, '14:00'),
    });
    expect(res.status()).toBe(400);
  });

  test('refuses to book an exhausted single-use link', async ({ api }) => {
    const date = bookingDay(3);
    const res = await api.post(`/api/book/${LINKS.exhausted.slug}`, {
      name: 'E2E Late Booker',
      email: 'e2e.booker.late@paceday.test',
      start: atUtc(date, '15:00'),
    });
    expect(res.status()).toBe(410);
  });
});

test.describe('public booking — the anonymous browser journey', () => {
  test('a visitor with no session can pick a slot and get a confirmation', async ({
    page,
    db,
    assertNotDemo,
  }) => {
    const date = bookingDay(4);
    const bookerEmail = `e2e.booker.ui.${Date.now()}@paceday.test`;

    // No storageState: this context has never been authenticated.
    await page.goto(`/book/${LINKS.uiBooking.slug}`);

    // Title comes from the database via GET /api/book/{slug}; if the request
    // failed the page would render the "We couldn't load this link" heading.
    await expect(
      page.getByRole('heading', { name: LINKS.uiBooking.title }),
    ).toBeVisible();
    // Rendered from link.durations, i.e. from the database row.
    await expect(page.getByText('30 minutes')).toBeVisible();
    await assertNotDemo(page);

    await pickBookingDate(page, date);

    // Wait on state, never on time: the slot list is populated by the
    // /slots query and the first button appearing IS the completion signal.
    const slots = slotButtons(page);
    await expect(slots.first()).toBeVisible();
    const slotCount = await slots.count();
    expect(slotCount).toBe(7); // same window arithmetic as the API test above

    const chosen = (await slots.first().textContent())?.trim();
    await slots.first().click();

    // Step 3 — the booker form. Labels are real <label for> pairs.
    await expect(page.getByRole('button', { name: /^change$/i })).toBeVisible();
    await page.getByLabel('Name', { exact: true }).fill('E2E UI Booker');
    await page.getByLabel('Email', { exact: true }).fill(bookerEmail);
    await page.getByLabel(/notes/i).fill('booked through the browser');

    await page.getByRole('button', { name: /confirm booking/i }).click();

    await expect(
      page.getByRole('heading', { name: /you are confirmed/i }),
    ).toBeVisible();
    await expect(page.getByText(bookerEmail)).toBeVisible();
    expect(chosen).toBeTruthy();

    // The screen says it worked; the database decides whether it did.
    const row = await db.queryOne<{
      booker_name: string;
      booker_email: string;
      notes: string;
      status: string;
    }>(
      `SELECT booker_name, booker_email, notes, status
         FROM bookings
        WHERE link_id = $1 AND booker_email = $2`,
      [LINKS.uiBooking.id, bookerEmail],
    );
    expect(row).toBeDefined();
    expect(row).toMatchObject({
      booker_name: 'E2E UI Booker',
      booker_email: bookerEmail,
      notes: 'booked through the browser',
      status: 'confirmed',
    });

    await db.deleteBookingsByBooker('e2e.booker.ui.');
    await fake.clearCalendar(BOOKING_HOSTS.ui.email);
  });

  test('a visitor who loses the race to a slot is told, not silently double-booked', async ({
    page,
    api,
    db,
  }) => {
    // This is the honest way to test a server-side rejection in the browser.
    // Empty-field validation cannot be driven here: PublicBooking.tsx marks the
    // name and email Inputs `required`, so the browser's native validation
    // blocks submission before React's onSubmit (and its error message) ever
    // runs. There is nothing for an e2e test to observe.
    const date = bookingDay(5);

    await page.goto(`/book/${LINKS.uiBooking.slug}`);
    await expect(page.getByRole('heading', { name: LINKS.uiBooking.title })).toBeVisible();

    await pickBookingDate(page, date);
    const slots = slotButtons(page);
    await expect(slots.first()).toBeVisible();

    // Read the slot the visitor is about to take, then have somebody else take
    // it. No timing assumptions: the page already holds its slot list.
    const chosenStart = await page.evaluate(() => {
      const btn = Array.from(document.querySelectorAll('button')).find((b) =>
        /^\s*\d{1,2}:\d{2}\s*(AM|PM)\s*$/i.test(b.textContent ?? ''),
      );
      return btn?.textContent?.trim() ?? null;
    });
    expect(chosenStart).toBeTruthy();

    const stolen = await api.post(`/api/book/${LINKS.uiBooking.slug}`, {
      name: 'E2E Faster Booker',
      email: `e2e.booker.race.${Date.now()}@paceday.test`,
      start: atUtc(date, '09:00'),
      end: atUtc(date, '09:30'),
    });
    expect(stolen.status()).toBe(201);

    await slots.first().click();
    await page.getByLabel('Name', { exact: true }).fill('E2E Slow Booker');
    await page
      .getByLabel('Email', { exact: true })
      .fill(`e2e.booker.slow.${Date.now()}@paceday.test`);
    await page.getByRole('button', { name: /confirm booking/i }).click();

    // storage.HasOverlappingBooking -> 409 -> the 409 branch of bookMutation's
    // onError. The user must see this, and must NOT see a confirmation.
    await expect(page.getByText(/just taken|no longer available/i)).toBeVisible();
    await expect(page.getByRole('heading', { name: /you are confirmed/i })).toHaveCount(0);

    // Exactly one booking exists for that instant — the fast one.
    const rows = await db.query(
      'SELECT id FROM bookings WHERE link_id = $1 AND start_time = $2',
      [LINKS.uiBooking.id, atUtc(date, '09:00')],
    );
    expect(rows).toHaveLength(1);

    await db.deleteBookingsByBooker('e2e.booker.race.');
    await db.deleteBookingsByBooker('e2e.booker.slow.');
    await fake.clearCalendar(BOOKING_HOSTS.ui.email);
  });
});

test.describe('public booking — an unreadable host calendar (PAC-72 D-6)', () => {
  // Owner decision PAC-72 D-6 (2026-11-01): a host whose calendar cannot be
  // read counts as busy. LINKS.unreadableCoHost is collective: its first host
  // (BOOKING_HOSTS.d6) is connected and free, its co-host (USERS.betaReport)
  // has no calendar connected. Every slot needs both, so none is offered, and
  // a booking is the existing 409 with nothing written.
  test('a collective link with an unconnected co-host offers no slot and refuses a booking', async ({
    api,
    db,
  }) => {
    const date = nextBookableWeekday();
    const slotsRes = await api.get(`/api/book/${LINKS.unreadableCoHost.slug}/slots`, {
      date: isoDate(date),
      duration: 30,
    });
    expect(slotsRes.status()).toBe(200);
    expect((await slotsRes.json()).slots).toEqual([]);

    const res = await api.post(`/api/book/${LINKS.unreadableCoHost.slug}`, {
      name: 'E2E D-6 Booker',
      email: `e2e.booker.d6.${Date.now()}@paceday.test`,
      start: atUtc(date, '09:00'),
      end: atUtc(date, '09:30'),
    });
    expect(res.status()).toBe(409);
    expect(await res.json()).toEqual({ error: 'slot is no longer available' });

    const rows = await db.query('SELECT id FROM bookings WHERE link_id = $1', [
      LINKS.unreadableCoHost.id,
    ]);
    expect(rows).toHaveLength(0);
    // Nothing was written to the connected host's calendar either.
    expect(await fake.events(BOOKING_HOSTS.d6.email)).toEqual([]);
  });
});
