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
 * storage.GetBusyBookingsForUser() can never see the other file's rows,
 * so the two can run in either order and in parallel.
 *
 * Within this file, every journey that writes a booking takes its own
 * `bookingDay(index)` — distinct indices are distinct weekdays whatever today
 * is. The journeys used to take `nextBookableWeekday(8..12)`, which collapses
 * three offsets onto one Monday when the suite runs on a Wednesday, so the two
 * UI journeys raced for the same 09:00 slot and the loser failed.
 */

import type { Page } from '@playwright/test';

import { test, expect } from '../fixtures';
import { fake } from '../fake';
import { BOOKING_HOSTS, CONNECTED, LINKS, MISSING_SLUG, OFFERABLE_LINK } from '../seed/ids';
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

    // PAC-72 revision 2 (AC-32): the zone the link's days and hours are in
    // (its owner's profile zone, UTC here) and the link's horizon (60 for
    // every seeded link, the migration's default).
    expect(body.time_zone).toBe('UTC');
    expect(body.booking_horizon_days).toBe(60);
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

/**
 * PAC-72 AC-20: the server books only a start the slot listing offers. The
 * link's host is CONNECTED.offerable, whose calendar is the stand-in's, so an
 * event seeded there is busy time the backend reads exactly as it reads
 * Google. This journey owns that calendar and that link.
 */
test.describe('public booking — only an offered start books (PAC-72)', () => {
  // Both journeys own CONNECTED.offerable's calendar and OFFERABLE_LINK's
  // refusal setting: they must not run at the same time (fullyParallel).
  test.describe.configure({ mode: 'serial' });

  test('books an offered slot and refuses a start the host is busy at, writing nothing', async ({
    api,
    db,
  }) => {
    const host = CONNECTED.offerable;
    const day = bookingDay(0);
    const bookerPrefix = `e2e.booker.offerable.${Date.now()}`;
    await fake.clearCalendar(host.email);
    try {
      // The host has a meeting 10:00-10:30 UTC that day.
      await fake.seedEvents(host.email, [
        {
          summary: 'E2E PAC-72 host meeting',
          start: { dateTime: atUtc(day, '10:00') },
          end: { dateTime: atUtc(day, '10:30') },
        },
      ]);

      // The listing offers the window 09:00-12:00 minus the meeting: every
      // 30-minute start overlapping 10:00-10:30 (09:45, 10:00, 10:15) is gone.
      const listing = await api.get(`/api/book/${OFFERABLE_LINK.slug}/slots`, {
        date: isoDate(day),
        duration: 30,
      });
      expect(listing.status()).toBe(200);
      const { slots } = await listing.json();
      const expected = ['09:00', '09:15', '09:30', '10:30', '10:45', '11:00', '11:15', '11:30'].map(
        (t) => atUtc(day, t),
      );
      expect(slots.map((sl: { start: string }) => instant(sl.start))).toEqual(expected);

      // An offered slot books: 201, a row, and the host's calendar insert.
      let mark = await fake.mark();
      const ok = await api.post(`/api/book/${OFFERABLE_LINK.slug}`, {
        name: 'E2E Offerable Booker',
        email: `${bookerPrefix}.ok@paceday.test`,
        start: atUtc(day, '09:00'),
        end: atUtc(day, '09:30'),
      });
      expect(ok.status()).toBe(201);
      const conf = await ok.json();
      const row = await db.queryOne<{ start_time: Date }>(
        'SELECT start_time FROM bookings WHERE id = $1',
        [conf.id],
      );
      expect(row && new Date(row.start_time).toISOString()).toBe(atUtc(day, '09:00'));
      const okRequests = await fake.requests(mark, host.label);
      expect(okRequests.filter((r) => r.operation === 'events.insert')).toHaveLength(1);

      // A start the listing does not offer (over the meeting) is refused:
      // 409, the one refusal body, no row, and no insert on the host's calendar.
      mark = await fake.mark();
      const refused = await api.post(`/api/book/${OFFERABLE_LINK.slug}`, {
        name: 'E2E Offerable Booker',
        email: `${bookerPrefix}.busy@paceday.test`,
        start: atUtc(day, '10:00'),
        end: atUtc(day, '10:30'),
      });
      expect(refused.status()).toBe(409);
      expect(await refused.json()).toEqual({ error: 'slot is no longer available' });
      const rows = await db.query(
        'SELECT 1 FROM bookings WHERE link_id = $1 AND start_time = $2',
        [OFFERABLE_LINK.id, atUtc(day, '10:00')],
      );
      expect(rows).toHaveLength(0);
      const refusedRequests = await fake.requests(mark, host.label);
      // The refusal read the calendar (that is how it knew) and wrote nothing.
      expect(refusedRequests.some((r) => r.operation === 'events.list')).toBe(true);
      expect(refusedRequests.filter((r) => r.operation === 'events.insert')).toHaveLength(0);
    } finally {
      await db.deleteBookingsByBooker(bookerPrefix);
      await fake.clearCalendar(host.email);
    }
  });

  /**
   * PAC-72 revision 2, AC-20 addition (owner decisions D-2/D-5, A-1): the
   * link owner switches the link to specific refusal reasons; the same start
   * over the host's meeting is then refused with the "host busy" reason and
   * its paired text, still writing nothing. The link is switched back to the
   * default (generic) at the end, so the journey above keeps its body.
   */
  test('a link switched to specific reasons refuses a busy start with host_busy', async ({
    api,
    apiAs,
    db,
  }) => {
    const host = CONNECTED.offerable;
    const owner = apiAs(host);
    const day = bookingDay(1);
    const bookerPrefix = `e2e.booker.specific.${Date.now()}`;
    await fake.clearCalendar(host.email);
    try {
      await fake.seedEvents(host.email, [
        {
          summary: 'E2E PAC-72 host meeting (specific)',
          start: { dateTime: atUtc(day, '10:00') },
          end: { dateTime: atUtc(day, '10:30') },
        },
      ]);
      const patched = await owner.patch(`/api/scheduling-links/${OFFERABLE_LINK.id}`, {
        refusal_message: 'specific',
      });
      expect(patched.status()).toBe(200);
      expect(await patched.json()).toMatchObject({
        refusal_message: 'specific',
        booking_horizon_days: 60,
        time_zone: 'UTC',
      });

      const mark = await fake.mark();
      const refused = await api.post(`/api/book/${OFFERABLE_LINK.slug}`, {
        name: 'E2E Specific Booker',
        email: `${bookerPrefix}.busy@paceday.test`,
        start: atUtc(day, '10:00'),
        end: atUtc(day, '10:30'),
      });
      expect(refused.status()).toBe(409);
      expect(await refused.json()).toEqual({
        error: 'a host is busy at that time',
        reason: 'host_busy',
      });
      const rows = await db.query(
        'SELECT 1 FROM bookings WHERE link_id = $1 AND start_time = $2',
        [OFFERABLE_LINK.id, atUtc(day, '10:00')],
      );
      expect(rows).toHaveLength(0);
      const requests = await fake.requests(mark, host.label);
      expect(requests.filter((r) => r.operation === 'events.insert')).toHaveLength(0);
    } finally {
      await owner.patch(`/api/scheduling-links/${OFFERABLE_LINK.id}`, { refusal_message: 'generic' });
      await db.deleteBookingsByBooker(bookerPrefix);
      await fake.clearCalendar(host.email);
    }
  });
});

/**
 * PAC-83: the web waits for a slow answer and never invents one.
 *
 * OQ-4 (contract contracts/features/PAC-83.md §5.3): timing and failure are
 * controlled at the browser's network layer with page.route, against the real
 * stack; nothing in the server, nginx or the stand-in changes. An answer is
 * HELD on a promise the test releases (route.fetch sends the real request
 * from Playwright, the test asserts the waiting state, then route.fulfill
 * hands the real answer to the page). The page's clock is installed before
 * page.goto and advanced with page.clock.fastForward, so "after 20 s / 30 s"
 * is page time and the scenario takes seconds; this works because the web
 * arms its bounds with setTimeout (contract §3.1).
 *
 * What these prove: the web does not give up early (the 4 s abort is gone)
 * and renders a late answer. They do not prove the 135 s bound's value (unit
 * AC-3) and do not make the server slow (PAC-81 Part A backend tests).
 *
 * Page time is advanced to 131 s after the request started: past nginx's
 * 130 s read timeout, below the client's 135 s bound (CLIENT_REQUEST_BOUND_MS).
 * The margin must not depend on real time (a loaded runner, the real
 * route.fetch round trip, the assertions): once the held request has
 * arrived the page clock is PAUSED, page time is advanced by exact amounts
 * from the page time the request was intercepted at, the answer is released
 * and delivered while still paused (the bound's timer cannot fire), and only
 * then does the clock resume.
 *
 * Isolation: these journeys use this file's hosts (BOOKING_HOSTS.ui and .api)
 * on days no other journey here uses (bookingDay(6) and (7)) and, for the
 * two-host link, at 11:00-12:00 UTC, which no seeded window of theirs covers.
 */
test.describe('public booking — slow or lost answers (PAC-83)', () => {
  /** Page time that shows the "still working" notice (STILL_WORKING_AFTER_MS is 8 s). */
  const PAST_STILL_WORKING_MS = 9_000;
  /** Brings the held request to 131 s of page time in total. */
  const TO_131_S_MS = 131_000 - PAST_STILL_WORKING_MS;
  const NO_TIMES = /no time available|no available times/i;

  /**
   * Pause the page clock (it runs in real time since page.clock.install())
   * and return the page time elapsed since `since`, a page Date.now() read
   * when the request was intercepted. pauseAt must not be in the page's past,
   * hence the second of headroom; it is counted in the returned value.
   */
  async function pauseClock(page: Page, since: number): Promise<number> {
    const now = await page.evaluate(() => Date.now());
    await page.clock.pauseAt(now + 1_000);
    return (await page.evaluate(() => Date.now())) - since;
  }

  /** A promise the route handler waits on, and the function that opens it. */
  function gate(): { wait: Promise<void>; open: () => void } {
    let open!: () => void;
    const wait = new Promise<void>((resolve) => {
      open = resolve;
    });
    return { wait, open };
  }

  test('AC-5: a two-host link whose slots answer late shows the real slots, never none', async ({
    page,
    api,
    apiAs,
    db,
    assertNotDemo,
  }) => {
    test.setTimeout(120_000);
    const owner = apiAs(BOOKING_HOSTS.ui);
    const coHost = apiAs(BOOKING_HOSTS.api);
    const slug = `e2e-pac83-two-hosts-${Date.now()}`;
    const title = 'E2E PAC-83 Two Hosts';
    const day = bookingDay(7);

    const created = await owner.post('/api/scheduling-links', {
      title,
      slug,
      duration_options: [30],
      days_of_week: [1, 2, 3, 4, 5],
      window_start_time: '11:00',
      window_end_time: '12:00',
      min_notice_minutes: 0,
      usage_type: 'reusable',
      co_host_emails: [BOOKING_HOSTS.api.email],
    });
    expect(created.status(), await created.text()).toBe(201);
    const link = (await created.json()) as { id: string };
    try {
      const accepted = await coHost.post(`/api/scheduling-links/host-invites/${link.id}/accept`);
      expect(accepted.status(), await accepted.text()).toBe(200);
      // Two accepted hosts, both connected through the stand-in (PAC-72 D-6).
      const info = await api.get(`/api/book/${slug}`);
      expect(info.status()).toBe(200);
      expect(((await info.json()) as { hosts: unknown[] }).hosts).toHaveLength(2);

      const release = gate();
      const arrived = gate();
      let served: { slots: Array<{ start: string }> } | undefined;
      let startedAt: number | undefined;
      // PAC-88: the page's first /slots request is now the day overview
      // (from/to); only the day listing (date=) is held. Setup, not an
      // assertion: the journey still holds the one answer it asserts on.
      await page.route(
        (url) => url.pathname === `/api/book/${slug}/slots` && url.searchParams.has('date'),
        async (route) => {
          startedAt ??= await page.evaluate(() => Date.now());
          const res = await route.fetch();
          served = (await res.json()) as { slots: Array<{ start: string }> };
          arrived.open();
          await release.wait;
          await route.fulfill({ response: res });
        },
      );

      await page.clock.install();
      await page.goto(`/book/${slug}`);
      await expect(page.getByRole('heading', { name: title })).toBeVisible();
      await pickBookingDate(page, day);
      await arrived.wait;

      // The real server answer: 11:00-12:00, 30 minutes, 15-minute stride,
      // both hosts free. Pinning it exactly is what gives "the real slots"
      // below its meaning.
      const expected = ['11:00', '11:15', '11:30'].map((t) => atUtc(day, t));
      expect(served!.slots.map((s) => instant(s.start))).toEqual(expected);

      // Waiting: nothing invented, no "no times", no error.
      const elapsed = await pauseClock(page, startedAt!);
      expect(elapsed, 'page time before the clock paused').toBeLessThan(PAST_STILL_WORKING_MS);
      await page.clock.fastForward(PAST_STILL_WORKING_MS - elapsed);
      await expect(page.getByTestId('still-working')).toBeVisible();
      await expect(slotButtons(page)).toHaveCount(0);
      await expect(page.getByText(NO_TIMES)).toHaveCount(0);
      await expect(page.getByTestId('api-error')).toHaveCount(0);

      await page.clock.fastForward(TO_131_S_MS);
      await expect(page.getByTestId('still-working')).toBeVisible();
      await expect(slotButtons(page)).toHaveCount(0);
      await expect(page.getByText(NO_TIMES)).toHaveCount(0);
      await expect(page.getByTestId('api-error')).toHaveCount(0);

      // Delivered while paused; the clock resumes only once the page has the
      // answer, so no real time is added before the 135 s bound is cleared.
      const answered = page.waitForResponse((r) => {
        const u = new URL(r.url());
        return u.pathname === `/api/book/${slug}/slots` && u.searchParams.has('date');
      });
      release.open();
      await answered;
      await page.clock.resume();
      await expect(slotButtons(page)).toHaveText([/^11:00\sAM$/, /^11:15\sAM$/, /^11:30\sAM$/]);
      await expect(page.getByTestId('still-working')).toHaveCount(0);
      await expect(page.getByText(NO_TIMES)).toHaveCount(0);
      await assertNotDemo(page);
    } finally {
      await page.unrouteAll({ behavior: 'ignoreErrors' });
      // As tests/scheduling-links.spec.ts does: the link (and its host rows)
      // is this test's own; nothing was booked on it.
      await db.query('DELETE FROM scheduling_links WHERE id = $1', [link.id]);
    }
  });

  test('AC-10: link info lost once shows "can\'t reach" with a retry, never "Link not found"', async ({
    page,
    assertNotDemo,
  }) => {
    const slug = LINKS.uiBooking.slug;
    let gets = 0;
    await page.route(
      (url) => url.pathname === `/api/book/${slug}`,
      async (route) => {
        // The same path takes the booking POST: only the read is failed.
        if (route.request().method() !== 'GET') return route.continue();
        gets += 1;
        if (gets === 1) return route.abort('failed');
        return route.continue();
      },
    );

    await page.goto(`/book/${slug}`);
    const error = page.locator('[data-testid="api-error"][data-error-kind="lost"]');
    await expect(error).toBeVisible();
    await expect(error).toHaveAttribute('data-effect', 'read');
    await expect(page.getByText(/link not found/i)).toHaveCount(0);
    await expect(page.getByRole('heading', { name: LINKS.uiBooking.title })).toHaveCount(0);
    // The guard is not called while the provoked error is on screen (§5.2).

    await error.getByTestId('api-error-retry').click();
    await expect(page.getByRole('heading', { name: LINKS.uiBooking.title })).toBeVisible();
    await expect(page.getByTestId('api-error')).toHaveCount(0);
    expect(gets).toBe(2);
    await assertNotDemo(page);

    await page.reload();
    await expect(page.getByRole('heading', { name: LINKS.uiBooking.title })).toBeVisible();
    await expect(page.getByText(/link not found/i)).toHaveCount(0);
    expect(gets).toBe(3);
    await assertNotDemo(page);
  });

  test('AC-24: a booking answered 201 late shows the confirmation, and exactly one booking exists', async ({
    page,
    db,
    assertNotDemo,
  }) => {
    test.setTimeout(120_000);
    const slug = LINKS.uiBooking.slug;
    const day = bookingDay(6);
    const bookerPrefix = `e2e.booker.pac83slow.${Date.now()}`;
    const bookerEmail = `${bookerPrefix}@paceday.test`;

    const release = gate();
    const arrived = gate();
    const bookingPosts: string[] = [];
    page.on('request', (req) => {
      if (req.method() === 'POST' && new URL(req.url()).pathname === `/api/book/${slug}`) {
        bookingPosts.push(req.url());
      }
    });
    let status: number | undefined;
    let startedAt: number | undefined;
    await page.route(
      (url) => url.pathname === `/api/book/${slug}`,
      async (route) => {
        if (route.request().method() !== 'POST') return route.continue();
        // The booking reaches the server once, sent from here; the page's own
        // request is still seen exactly once by page.on('request').
        startedAt ??= await page.evaluate(() => Date.now());
        const res = await route.fetch();
        status = res.status();
        arrived.open();
        await release.wait;
        await route.fulfill({ response: res });
      },
    );

    try {
      await page.clock.install();
      await page.goto(`/book/${slug}`);
      await expect(page.getByRole('heading', { name: LINKS.uiBooking.title })).toBeVisible();
      await pickBookingDate(page, day);
      const slots = slotButtons(page);
      await expect(slots).toHaveCount(7);
      await slots.last().click(); // 10:30 UTC
      await page.getByLabel('Name', { exact: true }).fill('E2E PAC-83 Slow Booker');
      await page.getByLabel('Email', { exact: true }).fill(bookerEmail);
      await page.getByRole('button', { name: /confirm booking/i }).click();
      await arrived.wait;
      expect(status).toBe(201);

      // Held: the page keeps waiting, says so, and claims nothing.
      const elapsed = await pauseClock(page, startedAt!);
      expect(elapsed, 'page time before the clock paused').toBeLessThan(PAST_STILL_WORKING_MS);
      await page.clock.fastForward(PAST_STILL_WORKING_MS - elapsed);
      await expect(page.getByTestId('still-working')).toBeVisible();
      await expect(page.getByRole('heading', { name: /you are confirmed/i })).toHaveCount(0);
      await expect(page.getByTestId('api-error')).toHaveCount(0);

      await page.clock.fastForward(TO_131_S_MS);
      await expect(page.getByRole('heading', { name: /you are confirmed/i })).toHaveCount(0);
      await expect(page.getByTestId('api-error')).toHaveCount(0);

      const answered = page.waitForResponse(
        (r) => r.request().method() === 'POST' && new URL(r.url()).pathname === `/api/book/${slug}`,
      );
      release.open();
      await answered;
      await page.clock.resume();
      await expect(page.getByRole('heading', { name: /you are confirmed/i })).toBeVisible();
      await expect(page.getByText(bookerEmail)).toBeVisible();
      await assertNotDemo(page);

      expect(bookingPosts).toHaveLength(1);
      const rows = await db.query<{ status: string }>(
        'SELECT status FROM bookings WHERE link_id = $1 AND booker_email = $2',
        [LINKS.uiBooking.id, bookerEmail],
      );
      expect(rows).toEqual([{ status: 'confirmed' }]);
      const atThatTime = await db.query(
        'SELECT 1 FROM bookings WHERE link_id = $1 AND start_time = $2',
        [LINKS.uiBooking.id, atUtc(day, '10:30')],
      );
      expect(atThatTime).toHaveLength(1);
    } finally {
      await page.unrouteAll({ behavior: 'ignoreErrors' });
      await db.deleteBookingsByBooker(bookerPrefix);
      await fake.clearCalendar(BOOKING_HOSTS.ui.email);
    }
  });
});

