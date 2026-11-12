/**
 * PAC-88: the public booking page shows times in the host's time zone by
 * default and lets the visitor switch the displayed zone (spec
 * docs/specs/PAC-88.md, contract contracts/features/PAC-88.md).
 *
 * Read-only journeys. Nothing here books or writes a calendar: the booking
 * journeys (AC-9, AC-21) are in tests/pac88-booking.spec.ts, serial, on
 * "booking Mondays" this file never displays (seed/ids.ts PAC88_LINKS).
 *
 *  - "API" describes run against the server alone (api PR): the listing's
 *    echo, the bounded overview, the day zone and the seed (C-1 end to end).
 *  - "page" describes need the web PR (smart-calendar-flow#144): the zone
 *    label, the switcher, "Use my time zone" and the regrouped days. Their
 *    selectors are the contract's test ids (section 3.5) and the time
 *    buttons' accessible names (spec 3.9), as listed in TESTIDS-REQUIRED.md.
 *
 * The global browser zone stays UTC (playwright.config.ts, AC-28); each
 * describe that needs another zone sets it with test.use({ timezoneId }).
 *
 * Every expected instant comes from the server's own answer read through the
 * API in the same run, never from a fixed instant: the backend clock cannot be
 * controlled (lib/dates.ts).
 */

import type { Page } from '@playwright/test';

import { test, expect, type ApiClient } from '../fixtures';
import { PAC88_LINKS } from '../seed/ids';
import { nextBookableWeekday, isoDate } from '../lib/dates';
import {
  addDays,
  dateIn,
  dayCell,
  expectListed,
  nextTransition,
  pickDay,
  switchZone,
  winterMonday,
  zoneLabel,
} from '../lib/zones';

type Slot = { start: string; end: string };
type Listing = {
  slots: Slot[];
  available_dates: string[];
  time_zone: string;
  display_time_zone: string;
  range_from: string;
  range_to: string;
};

async function listing(api: ApiClient, slug: string, params: Record<string, string | number>): Promise<Listing> {
  const res = await api.get(`/api/book/${slug}/slots`, { duration: 30, ...params });
  expect(res.status(), await res.text()).toBe(200);
  return (await res.json()) as Listing;
}

/**
 * The contract's equivalence (C-1), end to end: the starts of day `day` in
 * `zone` as the regrouping of the PAC-72 explicit-date answers (no
 * display_time_zone) of the link days around it.
 */
async function regrouped(api: ApiClient, slug: string, day: string, zone: string): Promise<string[]> {
  const starts: string[] = [];
  for (let i = -2; i <= 2; i++) {
    const l = await listing(api, slug, { date: addDays(day, i) });
    for (const s of l.slots) if (dateIn(s.start, zone) === day) starts.push(s.start);
  }
  return starts.sort();
}

/** The AC-10 Monday: first winter Monday (Paris) at least two days ahead. */
const AC10_MONDAY = winterMonday('Europe/Paris', 2);

test.describe('PAC-88 API — listing in a display zone', () => {
  test('echoes the link zone, the display zone and the range; days are display-zone days', async ({ api }) => {
    const info = await api.get(`/api/book/${PAC88_LINKS.paris.slug}`);
    expect(info.status()).toBe(200);
    const linkInfo = (await info.json()) as { time_zone: string; hosts: unknown[]; booking_horizon_days: number };
    // AC-2 (server half): collective link, LA co-host, owner's zone.
    expect(linkInfo.time_zone).toBe('Europe/Paris');
    expect(linkInfo.hosts).toHaveLength(2);
    expect(linkInfo.booking_horizon_days).toBe(365);

    // Default overview: today in the display zone + 41 days, no slot list.
    const ov = await listing(api, PAC88_LINKS.paris.slug, { display_time_zone: 'Pacific/Auckland' });
    expect(ov.time_zone).toBe(linkInfo.time_zone); // AC-31
    expect(ov.display_time_zone).toBe('Pacific/Auckland');
    expect(ov.range_from).toBe(dateIn(new Date(), 'Pacific/Auckland'));
    expect(ov.range_to).toBe(addDays(ov.range_from, 41));
    expect(ov.slots).toEqual([]);

    // AC-10 (server half): the Monday-only Paris link in Auckland.
    const mon = await listing(api, PAC88_LINKS.paris.slug, { date: AC10_MONDAY, display_time_zone: 'Pacific/Auckland' });
    const tue = await listing(api, PAC88_LINKS.paris.slug, { date: addDays(AC10_MONDAY, 1), display_time_zone: 'Pacific/Auckland' });
    expect(mon.range_from).toBe(AC10_MONDAY);
    expect(mon.range_to).toBe(AC10_MONDAY);
    expect(mon.slots).toHaveLength(12);
    expect(tue.slots).toHaveLength(19);
    expect(await regrouped(api, PAC88_LINKS.paris.slug, AC10_MONDAY, 'Pacific/Auckland')).toEqual(
      mon.slots.map((s) => s.start).sort(),
    );
    const week = await listing(api, PAC88_LINKS.paris.slug, {
      from: addDays(AC10_MONDAY, -1),
      to: addDays(AC10_MONDAY, 5),
      display_time_zone: 'Pacific/Auckland',
    });
    expect(week.available_dates).toEqual([AC10_MONDAY, addDays(AC10_MONDAY, 1)]);
  });

  test('refuses an unknown display zone and an over-long range with the contract messages', async ({ api }) => {
    for (const [params, message] of [
      [{ display_time_zone: 'Not/AZone' }, 'invalid display_time_zone, use an IANA time zone name'],
      [{ display_time_zone: 'localtime' }, 'invalid display_time_zone, use an IANA time zone name'],
      [{ from: '2030-01-01', to: '2030-02-12' }, 'range must not exceed 42 days'],
      [{ from: '2030-01-01' }, 'from and to must be given together'],
      [{ date: '2030-01-01', from: '2030-01-01', to: '2030-01-02' }, 'date cannot be combined with from or to'],
    ] as Array<[Record<string, string>, string]>) {
      const res = await api.get(`/api/book/${PAC88_LINKS.paris.slug}/slots`, params);
      expect(res.status(), JSON.stringify(params)).toBe(400);
      expect(await res.json()).toEqual({ error: message });
    }
  });

  test('a link whose owner has no zone is UTC (A-4)', async ({ api }) => {
    const info = (await (await api.get(`/api/book/${PAC88_LINKS.noZone.slug}`)).json()) as { time_zone: string };
    expect(info.time_zone).toBe('UTC');
    const day = isoDate(nextBookableWeekday(2));
    const l = await listing(api, PAC88_LINKS.noZone.slug, { date: day });
    expect(l.time_zone).toBe('UTC');
    expect(l.display_time_zone).toBe('UTC');
    expect(l.slots[0]!.start).toMatch(new RegExp(`^${day}T09:00:00`));
    expect(l.slots).toHaveLength(31);
  });
});

/** Open the booking page of `slug` and wait for its zone label. */
async function openPage(page: Page, slug: string, title: string): Promise<void> {
  await page.goto(`/book/${slug}`);
  await expect(page.getByRole('heading', { name: title })).toBeVisible();
  await expect(page.getByTestId('booking-tz-label')).toBeVisible();
}

test.describe('PAC-88 page — browser in Asia/Tokyo', () => {
  test.use({ timezoneId: 'Asia/Tokyo' });

  test('AC-1: a Paris link shows Europe/Paris and the offered instants in Paris', async ({ page, api, assertNotDemo }) => {
    const expected = await listing(api, PAC88_LINKS.paris.slug, { date: AC10_MONDAY, display_time_zone: 'Europe/Paris' });
    expect(expected.slots).toHaveLength(31);
    await openPage(page, PAC88_LINKS.paris.slug, PAC88_LINKS.paris.title);
    await expect(page.getByTestId('booking-tz-switcher')).toHaveValue('Europe/Paris');
    await pickDay(page, AC10_MONDAY);
    await expect(page.getByTestId('booking-tz-label')).toHaveText(zoneLabel('Europe/Paris', expected.slots[0]!.start));
    await expectListed(page, expected.slots.map((s) => s.start), 'Europe/Paris');
    // AC-13: a non-link weekday is not selectable.
    await expect(dayCell(page, addDays(AC10_MONDAY, 2))).toBeDisabled();
    // Nothing announced on load (AC-27).
    await expect(page.getByTestId('booking-tz-announcer')).toHaveText('');
    await assertNotDemo(page);
  });

  test('AC-3: an owner without a zone shows UTC (+00:00), whatever the browser', async ({ page, api, assertNotDemo }) => {
    const day = isoDate(nextBookableWeekday(2));
    const expected = await listing(api, PAC88_LINKS.noZone.slug, { date: day, display_time_zone: 'UTC' });
    await openPage(page, PAC88_LINKS.noZone.slug, PAC88_LINKS.noZone.title);
    await expect(page.getByTestId('booking-tz-switcher')).toHaveValue('UTC');
    await expect(page.getByTestId('booking-tz-label')).toHaveText(/^\s*UTC\s*\(UTC\+00:00\)\s*$/);
    await pickDay(page, day);
    await expectListed(page, expected.slots.map((s) => s.start), 'UTC');
    await assertNotDemo(page);
  });
});

test.describe('PAC-88 page — browser in America/Los_Angeles', () => {
  test.use({ timezoneId: 'America/Los_Angeles' });

  test('AC-2: a collective link with a Los Angeles co-host shows the owner\'s Europe/Paris', async ({ page }) => {
    await openPage(page, PAC88_LINKS.paris.slug, PAC88_LINKS.paris.title);
    await expect(page.getByTestId('booking-tz-switcher')).toHaveValue('Europe/Paris');
    await expect(page.getByTestId('booking-tz-label')).toHaveText(/^\s*Europe\/Paris\s*\(UTC\+0[12]:00\)\s*$/);
  });
});

test.describe('PAC-88 page — browser in America/New_York', () => {
  test.use({ timezoneId: 'America/New_York' });

  test('AC-7: "Use my time zone (America/New_York)" shows every time in New York', async ({ page, api }) => {
    const expected = await listing(api, PAC88_LINKS.paris.slug, { date: AC10_MONDAY, display_time_zone: 'America/New_York' });
    expect(expected.slots).toHaveLength(31); // 03:00-10:30 EST: one New York day
    await openPage(page, PAC88_LINKS.paris.slug, PAC88_LINKS.paris.title);
    const useMine = page.getByTestId('booking-tz-use-mine');
    await expect(useMine).toHaveText(/Use my time zone \(America\/New_York\)/);
    await useMine.click();
    await expect(page.getByTestId('booking-tz-switcher')).toHaveValue('America/New_York');
    await expect(page.getByTestId('booking-tz-back-to-host')).toBeVisible();
    await expect(page.getByTestId('booking-tz-announcer')).toHaveText(/America\/New_York/);
    await pickDay(page, AC10_MONDAY);
    await expectListed(page, expected.slots.map((s) => s.start), 'America/New_York');
  });
});

test.describe('PAC-88 page — browser in Europe/Paris', () => {
  test.use({ timezoneId: 'Europe/Paris' });

  test('AC-7: no "Use my time zone" when the browser is already in the link\'s zone', async ({ page }) => {
    await openPage(page, PAC88_LINKS.paris.slug, PAC88_LINKS.paris.title);
    await expect(page.getByTestId('booking-tz-switcher')).toHaveValue('Europe/Paris');
    await expect(page.getByTestId('booking-tz-use-mine')).toHaveCount(0);
  });

  test('AC-10: the Monday-only Paris link in Pacific/Auckland lists Monday and Tuesday only', async ({ page, api }) => {
    const tuesday = addDays(AC10_MONDAY, 1);
    const mon = await listing(api, PAC88_LINKS.paris.slug, { date: AC10_MONDAY, display_time_zone: 'Pacific/Auckland' });
    const tue = await listing(api, PAC88_LINKS.paris.slug, { date: tuesday, display_time_zone: 'Pacific/Auckland' });
    expect(mon.slots).toHaveLength(12); // 9:00 PM .. 11:45 PM (UTC+13:00)
    expect(tue.slots).toHaveLength(19); // 12:00 AM .. 4:30 AM

    await openPage(page, PAC88_LINKS.paris.slug, PAC88_LINKS.paris.title);
    await switchZone(page, 'Pacific/Auckland');
    await pickDay(page, AC10_MONDAY);
    await expectListed(page, mon.slots.map((s) => s.start), 'Pacific/Auckland');
    await pickDay(page, tuesday);
    await expectListed(page, tue.slots.map((s) => s.start), 'Pacific/Auckland');
    // AC-13: no other day of that week lists a time.
    for (const other of [-1, 2, 3, 4, 5]) {
      await expect(dayCell(page, addDays(AC10_MONDAY, other))).toBeDisabled();
    }
  });

  /**
   * AC-19: deterministic DST. For the next offset change of America/New_York
   * and of Europe/Paris at least two days ahead (tz database, at run time),
   * and for the change day and the day either side in each of the two zones,
   * the server's day answer (display_time_zone) equals the regrouped PAC-72
   * explicit-date answers, and the page lists exactly those instants with
   * their offsets. Never the date-less mode over the horizon. Nobody books
   * PAC88_LINKS.dst.
   */
  test('AC-19: transition days and their neighbours, in New York and in Paris', async ({ page, api }) => {
    test.setTimeout(300_000);
    const zones = ['America/New_York', 'Europe/Paris'];
    const transitions = zones.map((z) => nextTransition(z, 2));
    const views: Array<{ zone: string; day: string; starts: string[] }> = [];
    for (const t of transitions) {
      for (const zone of zones) {
        const change = dateIn(t, zone);
        for (const day of [addDays(change, -1), change, addDays(change, 1)]) {
          const answer = await listing(api, PAC88_LINKS.dst.slug, { date: day, display_time_zone: zone });
          expect(answer.time_zone).toBe('America/New_York');
          expect(answer.display_time_zone).toBe(zone);
          const starts = answer.slots.map((s) => s.start);
          // C-1 end to end, against the server's own explicit-date answers.
          expect([...starts].sort(), `${zone} ${day}`).toEqual(await regrouped(api, PAC88_LINKS.dst.slug, day, zone));
          expect(starts.length, `${zone} ${day} lists something`).toBeGreaterThan(0);
          views.push({ zone, day, starts });
        }
      }
    }

    await openPage(page, PAC88_LINKS.dst.slug, PAC88_LINKS.dst.title);
    for (const zone of zones) {
      if (zone !== 'America/New_York') await switchZone(page, zone); // the link's zone is the default
      for (const v of views.filter((x) => x.zone === zone)) {
        await pickDay(page, v.day);
        await expectListed(page, v.starts, zone);
      }
      // Back to the first month for the next zone's views.
      await page.goto(`/book/${PAC88_LINKS.dst.slug}`);
      await expect(page.getByTestId('booking-tz-label')).toBeVisible();
    }
  });
});

