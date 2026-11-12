/**
 * PAC-88 zone helpers: calendar dates and offsets of an instant in an IANA
 * zone, the next daylight-saving transition from the tz database at run time
 * (AC-19), and the booking page's zone controls.
 *
 * Everything is computed with Intl in the test process, never from a fixed
 * instant: the backend's clock cannot be controlled (lib/dates.ts), so the
 * journeys compare the page with the server's own answer for the same days.
 */

import { expect, type Locator, type Page } from '@playwright/test';

import { slotButtons } from './ui';

const DAY_MS = 24 * 60 * 60 * 1000;
const MINUS = '−';

/** YYYY-MM-DD of `t` in `zone`. */
export function dateIn(t: Date | string, zone: string): string {
  // en-CA formats dates as YYYY-MM-DD.
  return new Intl.DateTimeFormat('en-CA', {
    timeZone: zone,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  }).format(new Date(t));
}

/** Offset of `zone` at `t` in minutes east of UTC. */
export function offsetMinutes(t: Date | string, zone: string): number {
  const name = new Intl.DateTimeFormat('en-US', { timeZone: zone, timeZoneName: 'longOffset' })
    .formatToParts(new Date(t))
    .find((p) => p.type === 'timeZoneName')!.value; // "GMT-05:00", or "GMT" at zero
  const m = /GMT([+-])(\d{2}):(\d{2})/.exec(name);
  if (!m) return 0;
  const v = Number(m[2]) * 60 + Number(m[3]);
  return m[1] === '-' ? -v : v;
}

/** "UTC+09:00" / "UTC−05:00" (U+2212), as the booking page writes offsets. */
export function utcOffset(t: Date | string, zone: string): string {
  const off = offsetMinutes(t, zone);
  const sign = off < 0 ? MINUS : '+';
  const a = Math.abs(off);
  return `UTC${sign}${String(Math.floor(a / 60)).padStart(2, '0')}:${String(a % 60).padStart(2, '0')}`;
}

/** The plain date `iso` plus `n` days. */
export function addDays(iso: string, n: number): string {
  const d = new Date(`${iso}T00:00:00Z`);
  return new Date(d.getTime() + n * DAY_MS).toISOString().slice(0, 10);
}

/** A Date whose UTC fields are the plain date `iso` (what pickDay reads). */
export function plainDate(iso: string): Date {
  return new Date(`${iso}T00:00:00Z`);
}

/**
 * The first instant at least `minDaysAhead` days after `now` where `zone`'s
 * offset changes, found by an hourly scan then a minute bisection. Every zone
 * this suite uses changes offset at least twice a year, so a year's scan
 * always finds one.
 */
export function nextTransition(zone: string, minDaysAhead = 2, now = new Date()): Date {
  let a = now.getTime() + minDaysAhead * DAY_MS;
  const base = offsetMinutes(new Date(a), zone);
  const limit = a + 400 * DAY_MS;
  let b = a;
  while (b < limit && offsetMinutes(new Date(b), zone) === base) {
    a = b;
    b += 60 * 60 * 1000;
  }
  if (b >= limit) throw new Error(`no offset change of ${zone} within 400 days`);
  while (b - a > 60 * 1000) {
    const mid = a + Math.floor((b - a) / 2);
    if (offsetMinutes(new Date(mid), zone) === base) a = mid;
    else b = mid;
  }
  return new Date(b);
}

/**
 * The first Monday (a date in `zone`) at least `minDaysAhead` days ahead
 * whose month is December, January or February (AC-10's winter Monday:
 * Paris on UTC+01:00, Auckland on UTC+13:00).
 */
export function winterMonday(zone: string, minDaysAhead = 2, now = new Date()): string {
  let iso = addDays(dateIn(now, zone), minDaysAhead);
  for (let i = 0; i < 400; i++, iso = addDays(iso, 1)) {
    const d = plainDate(iso);
    const month = d.getUTCMonth(); // 0 = January
    if (d.getUTCDay() === 1 && (month === 11 || month === 0 || month === 1)) return iso;
  }
  throw new Error('no winter Monday within 400 days');
}

/** The first Monday (date in `zone`) at least `minDaysAhead` days ahead, skipping `avoid`. */
export function nextMonday(zone: string, minDaysAhead: number, avoid: string[] = [], now = new Date()): string {
  let iso = addDays(dateIn(now, zone), minDaysAhead);
  for (let i = 0; i < 400; i++, iso = addDays(iso, 1)) {
    if (plainDate(iso).getUTCDay() === 1 && !avoid.includes(iso)) return iso;
  }
  throw new Error('no Monday within 400 days');
}

/** "Monday, December 6, 2027": the date part of a time button's name. */
export function longDate(t: Date | string, zone: string): string {
  return new Intl.DateTimeFormat('en-US', {
    timeZone: zone,
    weekday: 'long',
    month: 'long',
    day: 'numeric',
    year: 'numeric',
  }).format(new Date(t));
}

/** "9:00 PM" (whatever space ICU puts before the day period). */
export function clockTime(t: Date | string, zone: string): string {
  return new Intl.DateTimeFormat('en-US', { timeZone: zone, hour: 'numeric', minute: '2-digit' }).format(
    new Date(t),
  );
}

function esc(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

/** A regex matching `s` with any whitespace where `s` has whitespace. */
export function loose(s: string): string {
  return esc(s).replace(/\s+/g, '\\s+');
}

/**
 * The accessible name of the time button for instant `t` shown in `zone`
 * (web contract, PAC-88 spec 3.9: "<Weekday, Month D, YYYY>, <time>, <zone>
 * <offset>").
 */
export function slotName(t: string, zone: string): RegExp {
  return new RegExp(
    `^${loose(longDate(t, zone))},\\s*${loose(clockTime(t, zone))},\\s*${esc(zone)}\\s+${esc(utcOffset(t, zone))}$`,
  );
}

/** The zone label's text: "<zone> (UTC±hh:mm)" at instant `t`. */
export function zoneLabel(zone: string, t: Date | string): RegExp {
  return new RegExp(`^\\s*${esc(zone)}\\s*\\(${esc(utcOffset(t, zone))}\\)\\s*$`);
}

/**
 * Choose `zone` in the booking page's switcher (combobox "Time zone",
 * data-testid booking-tz-switcher; options named "<zone> (UTC±hh:mm)").
 */
export async function switchZone(page: Page, zone: string): Promise<void> {
  const sw = page.getByTestId('booking-tz-switcher');
  await expect(sw).toBeEnabled();
  await sw.click();
  await sw.fill(zone);
  await page.getByRole('option', { name: new RegExp(`^${esc(zone)} \\(UTC`) }).first().click();
  await expect(sw).toHaveValue(zone);
}

/** The day cell of the plain date `iso` in the booking calendar, if on screen. */
export function dayCell(page: Page, iso: string): Locator {
  const d = plainDate(iso);
  const month = d.toLocaleString('en-US', { month: 'long', timeZone: 'UTC' });
  return page
    .getByRole('grid')
    .getByRole('button', { name: new RegExp(`${month}\\s+${d.getUTCDate()}(st|nd|rd|th)?,?\\s+${d.getUTCFullYear()}`, 'i') })
    .first();
}

/**
 * Move the calendar to the month of `iso` (up to 14 months ahead; the page
 * shows six weeks per month and requests one overview per grid) and return
 * that day's cell. Days are dates of the DISPLAYED zone.
 */
export async function showDay(page: Page, iso: string): Promise<Locator> {
  await expect(page.getByRole('grid')).toBeVisible();
  const target = plainDate(iso);
  const label = target.toLocaleString('en-US', { month: 'long', year: 'numeric', timeZone: 'UTC' });
  for (let hop = 0; hop < 15; hop++) {
    if (await page.getByText(label, { exact: true }).first().isVisible().catch(() => false)) {
      return dayCell(page, iso);
    }
    await page.getByRole('button', { name: /next month|go to the next month/i }).click();
  }
  throw new Error(`could not reach ${label} in the booking calendar`);
}

/** Select the day `iso` (displayed zone) once the overview has made it selectable. */
export async function pickDay(page: Page, iso: string): Promise<void> {
  const cell = await showDay(page, iso);
  await expect(cell).toBeEnabled();
  await cell.click();
}

/**
 * Assert the page lists exactly `starts` (UTC instants from the server, in
 * order) as time buttons named with their date, time, zone and offset in
 * `zone`.
 */
export async function expectListed(page: Page, starts: string[], zone: string): Promise<void> {
  const buttons = slotButtons(page);
  await expect(buttons).toHaveCount(starts.length);
  for (let i = 0; i < starts.length; i++) {
    await expect(buttons.nth(i)).toHaveAccessibleName(slotName(starts[i]!, zone));
  }
}
