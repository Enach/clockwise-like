/**
 * PAC-88: booking after switching the displayed zone (AC-9) and the
 * confirmation in the displayed zone (AC-21). Needs the web PR
 * (smart-calendar-flow#144).
 *
 * Write isolation (contract PAC-88 section 6): these are the ONLY journeys
 * that write to PAC88_LINKS.paris's hosts (a booking creates an event on the
 * Paris and the Los Angeles stand-in calendars and is busy time). They run
 * serially in this one file, on a "booking Monday" that no read-only journey
 * displays (tests/pac88-zones.spec.ts uses the AC-10 winter Monday), and each
 * picks its start at run time as the first start the server offers then, so a
 * retry (E2E_RETRIES) takes the next free start instead of a 409. Each journey
 * removes its own bookings; the stand-in calendars of the two hosts are
 * cleared after the file, since only this file writes them.
 */

import type { Request } from '@playwright/test';

import { test, expect } from '../fixtures';
import { fake } from '../fake';
import { PAC88_HOSTS, PAC88_LINKS } from '../seed/ids';
import { slotButtons } from '../lib/ui';
import {
  clockTime,
  dateIn,
  loose,
  longDate,
  nextMonday,
  pickDay,
  slotName,
  switchZone,
  utcOffset,
  winterMonday,
} from '../lib/zones';

test.describe.configure({ mode: 'serial' });
test.use({ timezoneId: 'Europe/Paris' });

const LINK = PAC88_LINKS.paris;
const AC10_MONDAY = winterMonday('Europe/Paris', 2);
/** The first Paris Monday at least two days ahead that the read-only file never displays. */
const BOOKING_MONDAY = nextMonday('Europe/Paris', 2, [AC10_MONDAY]);

type Slot = { start: string; end: string };

test.afterAll(async () => {
  await fake.clearCalendar(PAC88_HOSTS.paris.email);
  await fake.clearCalendar(PAC88_HOSTS.losAngeles.email);
});

/**
 * The first start the server offers on BOOKING_MONDAY (a Paris link day), as
 * displayed in `zone`: that start's date in `zone` and its exact string.
 */
async function firstOffered(api: import('../fixtures').ApiClient, zone: string): Promise<{ day: string; slot: Slot }> {
  const link = await api.get(`/api/book/${LINK.slug}/slots`, { date: BOOKING_MONDAY, duration: 30 });
  expect(link.status()).toBe(200);
  const offered = ((await link.json()) as { slots: Slot[] }).slots;
  expect(offered.length, `an offered start on ${BOOKING_MONDAY}`).toBeGreaterThan(0);
  const first = offered[0]!;
  const day = dateIn(first.start, zone);
  const shown = await api.get(`/api/book/${LINK.slug}/slots`, { date: day, duration: 30, display_time_zone: zone });
  expect(shown.status()).toBe(200);
  const slot = ((await shown.json()) as { slots: Slot[] }).slots.find((s) => s.start === first.start);
  expect(slot, 'the display-zone listing offers the same start string').toBeDefined();
  return { day, slot: slot! };
}

test('AC-9: booking after a zone switch sends the offered start string; same answers as from the host zone', async ({
  page,
  api,
  db,
}) => {
  const zone = 'Asia/Tokyo';
  const prefix = `e2e.booker.pac88.ac9.${Date.now()}`;
  const { day, slot } = await firstOffered(api, zone);

  await page.goto(`/book/${LINK.slug}`);
  await expect(page.getByRole('heading', { name: LINK.title })).toBeVisible();
  await switchZone(page, zone);
  await pickDay(page, day);
  const button = slotButtons(page).first();
  await expect(button).toHaveAccessibleName(slotName(slot.start, zone));
  await button.click();
  await page.getByLabel('Name', { exact: true }).fill('E2E PAC-88 AC-9');
  await page.getByLabel('Email', { exact: true }).fill(`${prefix}@paceday.test`);

  const posted = page.waitForRequest((r: Request) => r.method() === 'POST' && new URL(r.url()).pathname === `/api/book/${LINK.slug}`);
  const answered = page.waitForResponse((r) => r.request().method() === 'POST' && new URL(r.url()).pathname === `/api/book/${LINK.slug}`);
  await page.getByRole('button', { name: /confirm booking/i }).click();
  const body = (await posted).postDataJSON() as Record<string, unknown>;
  // AC-9 / AC-24: exactly the offered string, no zone in the request.
  expect(body.start).toBe(slot.start);
  expect(Object.keys(body).filter((k) => /zone/i.test(k))).toEqual([]);
  expect((await answered).status()).toBe(201);
  await expect(page.getByRole('heading', { name: /you are confirmed/i })).toBeVisible();

  const row = await db.queryOne<{ start: string }>(
    `SELECT to_char(start_time AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"') AS start
       FROM bookings WHERE link_id = $1 AND booker_email = $2`,
    [LINK.id, `${prefix}@paceday.test`],
  );
  expect(row?.start).toBe(new Date(slot.start).toISOString().replace('.000Z', 'Z'));

  // The same start from the host's zone (a request body without any zone, as
  // the page sends it from any zone) is now the generic 409 with nothing
  // written: the answer does not depend on the zone shown.
  const dup = await api.post(`/api/book/${LINK.slug}`, {
    name: 'E2E PAC-88 AC-9 dup',
    email: `${prefix}.dup@paceday.test`,
    start: slot.start,
    duration: 30,
  });
  expect(dup.status()).toBe(409);
  expect(await dup.json()).toEqual({ error: 'slot is no longer available' });
  const dupRow = await db.queryOne('SELECT 1 FROM bookings WHERE booker_email = $1', [`${prefix}.dup@paceday.test`]);
  expect(dupRow).toBeUndefined();

  await db.deleteBookingsByBooker(prefix);
});

test('AC-21: the confirmation shows the Tokyo date, times, zone and offset; "Book another time" stays in Tokyo', async ({
  page,
  api,
  db,
}) => {
  const zone = 'Asia/Tokyo';
  const prefix = `e2e.booker.pac88.ac21.${Date.now()}`;
  const { day, slot } = await firstOffered(api, zone);

  await page.goto(`/book/${LINK.slug}`);
  await expect(page.getByRole('heading', { name: LINK.title })).toBeVisible();
  await switchZone(page, zone);
  await pickDay(page, day);
  await expect(slotButtons(page).first()).toHaveAccessibleName(slotName(slot.start, zone));
  await slotButtons(page).first().click();
  await page.getByLabel('Name', { exact: true }).fill('E2E PAC-88 AC-21');
  await page.getByLabel('Email', { exact: true }).fill(`${prefix}@paceday.test`);
  await page.getByRole('button', { name: /confirm booking/i }).click();
  await expect(page.getByRole('heading', { name: /you are confirmed/i })).toBeVisible();

  // Tokyo date, "start – end · 30 min", "Asia/Tokyo (UTC+09:00)".
  const date = longDate(slot.start, zone).replace(/^[A-Za-z]+, /, ''); // "January 12, 2027"
  await expect(page.getByText(new RegExp(loose(date))).first()).toBeVisible();
  const times = `${loose(clockTime(slot.start, zone))}\\s*–\\s*${loose(clockTime(slot.end, zone))}\\s*·\\s*30\\s*min`;
  await expect(page.getByText(new RegExp(times)).first()).toBeVisible();
  await expect(page.getByText(`${zone} (${utcOffset(slot.start, zone)})`).first()).toBeVisible();
  expect(utcOffset(slot.start, zone)).toBe('UTC+09:00');

  await page.getByRole('button', { name: /book another time/i }).click();
  await expect(page.getByTestId('booking-tz-switcher')).toHaveValue(zone);
  await expect(page.getByTestId('booking-tz-label')).toHaveText(new RegExp(`^\\s*Asia/Tokyo\\s*\\(UTC\\+09:00\\)\\s*$`));

  await db.deleteBookingsByBooker(prefix);
});
