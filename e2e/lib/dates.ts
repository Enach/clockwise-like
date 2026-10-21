/**
 * Date helpers.
 *
 * There is NO way to control the backend's clock from outside the process —
 * engine/clock.go defines a Clock interface and a FixedClock test double, but
 * `engine.SystemClock` is a package-level variable set to a realClock and
 * main.go never reads an env var to replace it. See e2e/README.md
 * "Deterministic clock". Everything time-dependent here is therefore expressed
 * RELATIVE to the wall clock, never as an absolute instant.
 *
 * The whole suite runs with timezoneId: 'UTC' (playwright.config.ts) and the
 * backend container runs with TZ=UTC (docker-compose.e2e.yml), so browser-side
 * date formatting and server-side window arithmetic agree.
 */

const DAY_MS = 24 * 60 * 60 * 1000;

/** YYYY-MM-DD in UTC, which is the format every Paceday date param uses. */
export function isoDate(d: Date): string {
  return d.toISOString().slice(0, 10);
}

/** Monday 00:00 UTC of the week containing `d`. ISO weeks: Monday is day 1. */
export function mondayOf(d: Date): Date {
  const x = new Date(Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), d.getUTCDate()));
  const dow = x.getUTCDay(); // 0 = Sunday
  x.setUTCDate(x.getUTCDate() + (dow === 0 ? -6 : 1 - dow));
  return x;
}

/**
 * A weekday at least `minDaysAhead` in the future, used as the booking target.
 *
 * Weekday-ness is not strictly required — engine.BookingEngine.CollectiveSlots
 * ignores link.DaysOfWeek entirely and only uses the time window — but picking
 * a weekday keeps the fixture honest about what a user would actually book,
 * and means the test still passes if that bug is ever fixed.
 */
export function nextBookableWeekday(minDaysAhead = 7, now = new Date()): Date {
  const d = new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate()));
  d.setTime(d.getTime() + minDaysAhead * DAY_MS);
  while (d.getUTCDay() === 0 || d.getUTCDay() === 6) {
    d.setTime(d.getTime() + DAY_MS);
  }
  return d;
}

/**
 * The `index`-th weekday on or after `nextBookableWeekday()` (index 0 is that
 * day itself). Distinct indices are distinct weekdays on every day of the
 * week, which `nextBookableWeekday(n)` does not guarantee: it adds calendar
 * days and rolls a weekend forward, so on a Wednesday offsets 10, 11 and 12
 * all become the same Monday. Two journeys that book the same link on the same
 * day then race for the same slot, and whichever runs second fails.
 *
 * Use this for every journey that writes a booking. Indices 0–5 stay within
 * 7–15 days ahead, inside the 7–18-day band `weekDay()` keeps focus blocks
 * out of.
 */
export function bookingDay(index: number, now = new Date()): Date {
  const d = nextBookableWeekday(7, now);
  for (let i = 0; i < index; i++) {
    d.setTime(d.getTime() + DAY_MS);
    while (d.getUTCDay() === 0 || d.getUTCDay() === 6) {
      d.setTime(d.getTime() + DAY_MS);
    }
  }
  return d;
}

/** An RFC3339 instant at HH:MM UTC on `date`, the shape POST /api/book wants. */
export function atUtc(date: Date, hhmm: string): string {
  const [h, m] = hhmm.split(':').map(Number);
  return new Date(
    Date.UTC(date.getUTCFullYear(), date.getUTCMonth(), date.getUTCDate(), h, m, 0, 0),
  ).toISOString();
}

/**
 * Monday 00:00 UTC of the week `offsetWeeks` from this one, plus `weekday`
 * days (0 = Monday). Used to give each calendar journey its own week:
 * the booking specs use 7–18 days ahead, and a focus block is busy time for
 * its owner, who may also host a booking link, so a journey that writes focus
 * blocks stays out of those weeks (seed.sql, focus_blocks).
 */
export function weekDay(offsetWeeks: number, weekday = 0, now = new Date()): Date {
  return new Date(mondayOf(now).getTime() + (offsetWeeks * 7 + weekday) * DAY_MS);
}

/**
 * Normalise an RFC3339 instant to one spelling, so a server "…T09:00:00Z" and
 * a client "…T09:00:00.000Z" for the same instant compare equal. Compare
 * instants, never strings.
 */
export function instant(s: string): string {
  return new Date(s).toISOString();
}

export { DAY_MS };
