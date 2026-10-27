/**
 * Focus time.
 *
 * POST /api/focus/run -> engine.FocusTimeEngine.RunForUser -> newCalOps()
 * (engine/cal_iface.go), which loads the user's oauth token and returns
 * ErrCalendarNotConnected when there is none; with one, processDay() lists each
 * weekday's events and creates the focus blocks AS calendar events — the
 * focus_blocks table stores their event ids. There is no focus block without
 * a calendar.
 *
 * Without a credential (the four original seeded users): the read/clear side
 * of the focus API, and the fact that the run fails loudly rather than
 * reporting a fake success.
 *
 * With one (PAC-43, CONNECTED.focus): the calendar is the stand-in
 * (e2e/fake-google), reached through GOOGLE_CALENDAR_API_BASE_URL with the
 * real client and the real credential. AC-18 and AC-19 assert the three
 * places a block lives — the product's store, the provider, the screen.
 *
 * PAC-32 runs the engine the way a user does, from the dashboard's quick
 * action, and reads the run's response body as the web app does: the
 * camelCase FocusRunResult wrapper around snake_case blocks (PAC-26 tagged
 * the blocks; API-015 was the web app reading snake_case wrapper keys the
 * server never sent, which threw after a successful run).
 */

import { test, expect, type ApiClient, type Db } from '../fixtures';
import { atUtc, DAY_MS, instant, isoDate, mondayOf, weekDay } from '../lib/dates';
import { fake, type FakeEvent } from '../fake';
import { CONNECTED } from '../seed/ids';

/** settings.focus_label's default (migration 001); seed.sql does not set it. */
const FOCUS_LABEL = 'Focus Time';

// Each scenario owns its week. A focus block is busy time for its owner, who
// may also host a booking link (seed.sql), so these stay clear of the booking
// specs (7–18 days ahead), of seam.spec.ts (weeks 5 and 6) and of each other.
const AC18_WEEK = 4;
const AC19_WEEK = 8;
const PAC32_WEEK = 10;

test.describe('focus time — what is reachable without a calendar provider', () => {
  test('listing blocks for a week returns an empty set, not an error', async ({ authedApi }) => {
    const week = isoDate(mondayOf(new Date()));
    const res = await authedApi.get('/api/focus/blocks', { week });
    expect(res.status()).toBe(200);

    // storage.ListFocusBlocksForWeek reads the table directly, no calendar
    // involved. The seed asserts the table is empty, so this is an exact
    // expectation rather than a shape check. An empty week is `[]`, never
    // `null` (PAC-28).
    expect(await res.json()).toEqual([]);
  });

  test('an invalid week parameter is rejected before any work happens', async ({ authedApi }) => {
    const res = await authedApi.get('/api/focus/blocks', { week: '2026-13-45' });
    expect(res.status()).toBe(400);
  });

  test('listing blocks requires a session', async ({ api }) => {
    const res = await api.get('/api/focus/blocks');
    expect(res.status()).toBe(401);
  });

  test('running focus time fails loudly when no calendar is connected', async ({ authedApi }) => {
    const res = await authedApi.post('/api/focus/run', {
      week: isoDate(mondayOf(new Date())),
    });

    // The point of this assertion is the NEGATIVE: the endpoint must not
    // return 200 with an empty CreatedBlocks array, because a caller cannot
    // tell that apart from "there was no room this week". Since PAC-21
    // (API-054) it is the documented 401 CalendarNotConnected: JSON, one
    // fixed message, no provider or Go error text, no user id. The
    // middleware's own 401 is text/plain, so the two are told apart.
    expect(res.status()).toBe(401);
    expect(res.headers()['content-type']).toMatch(/^application\/json/);
    expect(await res.json()).toEqual({ error: 'calendar not connected' });

    // And nothing must have been written.
    const blocks = await authedApi.get('/api/focus/blocks', {
      week: isoDate(mondayOf(new Date())),
    });
    expect(await blocks.json()).toEqual([]);
  });

  test('the analytics meeting list answers 401 calendar not connected, not a 500 (API-054)', async ({
    authedApi,
  }) => {
    // Before PAC-21 this was a 500 whose body named the user id
    // ("internal error: no token for user <uuid>"), then a bare 500.
    const res = await authedApi.get('/api/analytics/meetings', {
      date: isoDate(mondayOf(new Date())),
    });
    expect(res.status()).toBe(401);
    expect(res.headers()['content-type']).toMatch(/^application\/json/);
    const body = await res.text();
    expect(JSON.parse(body)).toEqual({ error: 'calendar not connected' });
    expect(body).not.toMatch(/token|user [0-9a-f-]{36}/i);
  });

});

test.describe('focus time — with a connected calendar (PAC-43, through the stand-in)', () => {
  // Both scenarios act as the same user and read the stand-in's log by its
  // credential label, so they must not overlap in time: in order, in one
  // worker. ('default' rather than 'serial': a failure does not skip the
  // other.)
  test.describe.configure({ mode: 'default' });

  const USER = CONNECTED.focus;

  test('AC-18: running focus time creates blocks and they appear on the calendar', async ({
    apiAs,
    pageAs,
    db,
    assertNotDemo,
  }) => {
    const monday = weekDay(AC18_WEEK);
    const week = isoDate(monday);
    const meetings = await seedTwoMeetings(USER.email, AC18_WEEK, 'AC-18');

    try {
      const mark = await fake.mark();
      const res = await apiAs(USER).post('/api/focus/run', { week });
      expect(res.status(), await res.text()).toBe(200);
      const result = (await res.json()) as { errors: string[] | null };
      expect(result.errors ?? []).toEqual([]);

      // 1. The product's own store holds blocks for that week.
      const rows = await focusRows(db, week);
      expect(rows.length).toBeGreaterThan(0);

      // 2. Matching events exist on the provider: one per row, same id, same
      //    times, and nothing else was written to that week.
      const onProvider = (await fake.events(USER.email)).filter(
        (e) => inWeek(e.start.dateTime, monday) && !meetings.some((m) => m.id === e.id),
      );
      expect(
        onProvider.map((e) => ({ id: e.id, start: instant(e.start.dateTime!), end: instant(e.end.dateTime!) })),
      ).toEqual(rows.map((r) => ({ id: r.google_event_id, start: r.start, end: r.end })));
      for (const e of onProvider) {
        expect(e.summary).toBe(FOCUS_LABEL);
        expect(e.transparency).toBe('transparent');
      }

      // The engine read the week before writing it — no block overlaps the
      // provider's meetings, and every weekday was listed.
      for (const r of rows) {
        for (const m of meetings) {
          const overlaps = Date.parse(r.start) < Date.parse(m.end.dateTime!) && Date.parse(r.end) > Date.parse(m.start.dateTime!);
          expect(overlaps, `block ${r.start}–${r.end} overlaps ${m.summary}`).toBe(false);
        }
      }
      const asked = await fake.requests(mark, USER.label);
      expect(
        asked.filter((r) => r.operation === 'events.list' && inWeek(r.time_min, monday)),
      ).toHaveLength(5);
      expect(asked.filter((r) => r.operation === 'events.insert')).toHaveLength(rows.length);

      // 3. The user sees them on that week.
      const page = await pageAs(USER);
      await page.goto('/app');
      const nav = page.getByRole('group', { name: 'Calendar navigation' });
      await expect(nav).toBeVisible();
      for (let i = 0; i < AC18_WEEK; i++) {
        await nav.getByRole('button', { name: 'Next' }).click();
      }
      for (const m of meetings) {
        await expect(page.getByText(m.summary!, { exact: true })).toBeVisible();
      }
      // One rendered event per block. The label is user-visible content;
      // FullCalendar offers no role for an event. The dashboard decorates a
      // focus event's title with an icon (today "🎯 "), hence the prefix.
      await expect(page.getByText(new RegExp(`^\\W*${FOCUS_LABEL}$`))).toHaveCount(rows.length);
      await assertNotDemo(page);
    } finally {
      await clearFocusWeek(apiAs(USER), db, week);
    }
  });

  test('AC-19: clearing a week removes the blocks from both the database and the calendar', async ({
    apiAs,
    db,
  }) => {
    // AC-19's "state AC-18 leaves behind", rebuilt here so this scenario does
    // not depend on another having run: the same two meetings, the same run,
    // in this scenario's own week.
    const monday = weekDay(AC19_WEEK);
    const week = isoDate(monday);
    const meetings = await seedTwoMeetings(USER.email, AC19_WEEK, 'AC-19');

    try {
      const run = await apiAs(USER).post('/api/focus/run', { week });
      expect(run.status(), await run.text()).toBe(200);
      const created = await focusRows(db, week);
      expect(created.length).toBeGreaterThan(0);

      const mark = await fake.mark();
      const res = await apiAs(USER).delete(`/api/focus/blocks?week=${week}`);
      expect(res.status(), await res.text()).toBe(200);
      expect(await res.json()).toEqual({ deleted: created.length });

      // Gone from the product's store...
      expect(await focusRows(db, week)).toEqual([]);

      // ...AND from the provider — neither alone is sufficient. The meetings
      // the user already had are untouched.
      const left = (await fake.events(USER.email)).filter((e) => inWeek(e.start.dateTime, monday));
      expect(left.map((e) => e.id).sort()).toEqual(meetings.map((m) => m.id).sort());

      // One delete per block, each under this user's credential.
      const deletes = (await fake.requests(mark, USER.label)).filter(
        (r) => r.operation === 'events.delete',
      );
      expect(deletes.map((r) => r.event_id).sort()).toEqual(created.map((c) => c.google_event_id).sort());
      expect(deletes.every((r) => r.status === 204)).toBe(true);
    } finally {
      await clearFocusWeek(apiAs(USER), db, week);
    }
  });

  test('PAC-32: running focus from the dashboard reports the created blocks and shows them at their times', async ({
    apiAs,
    pageAs,
    db,
    assertNotDemo,
  }) => {
    const monday = weekDay(PAC32_WEEK);
    const week = isoDate(monday);
    await seedTwoMeetings(USER.email, PAC32_WEEK, 'PAC-32');
    const settings = await apiAs(USER).get('/api/settings');
    expect(settings.status()).toBe(200);
    const clock = ((await settings.json()) as { timeFormat?: string }).timeFormat === '24h' ? '24h' : '12h';

    try {
      const page = await pageAs(USER);
      await page.goto('/app');
      const nav = page.getByRole('group', { name: 'Calendar navigation' });
      await expect(nav).toBeVisible();
      for (let i = 0; i < PAC32_WEEK; i++) {
        await nav.getByRole('button', { name: 'Next' }).click();
      }
      await expect(page.locator(`.fc-timegrid-col[data-date="${week}"]`)).toBeVisible();

      const ran = page.waitForResponse(
        (r) => new URL(r.url()).pathname === '/api/focus/run' && r.request().method() === 'POST',
      );
      await page.getByRole('button', { name: /run focus engine/i }).click();
      const response = await ran;
      expect(response.status(), await response.text()).toBe(200);
      expect(response.request().postDataJSON()).toEqual({ week });

      // The run's body, under the keys the web app's generated type reads.
      const result = (await response.json()) as {
        weekStart: string;
        createdBlocks: Array<{ google_event_id: string; start_time: string; end_time: string; date: string }>;
        skippedDays: string[];
        totalMinutes: number;
        errors: string[];
      };
      expect(result.errors).toEqual([]);
      const rows = await focusRows(db, week);
      expect(rows.length).toBeGreaterThan(0);
      expect(
        result.createdBlocks
          .map((b) => ({ id: b.google_event_id, start: instant(b.start_time), end: instant(b.end_time) }))
          .sort((a, b) => a.start.localeCompare(b.start)),
      ).toEqual(rows.map((r) => ({ id: r.google_event_id, start: r.start, end: r.end })));
      expect(result.totalMinutes).toBe(
        rows.reduce((n, r) => n + (Date.parse(r.end) - Date.parse(r.start)) / 60_000, 0),
      );

      // The user is told what was created, not shown an error after a run that worked.
      const n = rows.length;
      await expect(page.getByText(`Created ${n} focus block${n === 1 ? '' : 's'}`, { exact: true })).toBeVisible();

      // And the blocks appear without a reload, in their day's column, at their
      // start times. A block of 30 minutes or less renders no time label.
      const label = (iso: string) =>
        new Date(iso)
          .toLocaleTimeString('en-US', clock === '24h'
            ? { hour: '2-digit', minute: '2-digit', hourCycle: 'h23', timeZone: 'UTC' }
            : { hour: '2-digit', minute: '2-digit', timeZone: 'UTC' })
          .replace(/\s+/g, ' ');
      const days = [...new Set(rows.map((r) => r.start.slice(0, 10)))];
      for (const day of days) {
        const inDay = rows.filter((r) => r.start.slice(0, 10) === day);
        const focusEvents = page
          .locator(`.fc-timegrid-col[data-date="${day}"] .fc-event`)
          .filter({ has: page.getByText(new RegExp(`^\\W*${FOCUS_LABEL}$`)) });
        await expect(focusEvents).toHaveCount(inDay.length);
        const want = inDay
          .filter((r) => Date.parse(r.end) - Date.parse(r.start) > 30 * 60_000)
          .map((r) => label(r.start))
          .sort();
        const shown = (await focusEvents.locator('.pd-event-time').allInnerTexts())
          .map((s) => s.trim().replace(/\s+/g, ' '))
          .sort();
        expect(shown, `focus block times on ${day}`).toEqual(want);
      }
      await assertNotDemo(page);
    } finally {
      await clearFocusWeek(apiAs(USER), db, week);
    }
  });
});

/** Two meetings in a week: Tuesday 10:00–11:00 and Thursday 14:00–15:30 UTC. */
async function seedTwoMeetings(
  calendarId: string,
  weekOffset: number,
  tag: string,
): Promise<Array<FakeEvent & { id: string }>> {
  // Start from an empty calendar: a previous run against a kept stack, or a
  // failed attempt, may have left meetings or blocks behind. Safe because the
  // two scenarios using this calendar run one after the other (above).
  await fake.clearCalendar(calendarId);
  const run = Date.now();
  return fake.seedEvents(calendarId, [
    {
      summary: `E2E ${tag} design review ${run}`,
      start: { dateTime: atUtc(weekDay(weekOffset, 1), '10:00') },
      end: { dateTime: atUtc(weekDay(weekOffset, 1), '11:00') },
    },
    {
      summary: `E2E ${tag} customer call ${run}`,
      start: { dateTime: atUtc(weekDay(weekOffset, 3), '14:00') },
      end: { dateTime: atUtc(weekDay(weekOffset, 3), '15:30') },
    },
  ]);
}

function inWeek(dateTime: string | undefined, monday: Date): boolean {
  if (!dateTime) return false;
  const t = Date.parse(dateTime);
  return t >= monday.getTime() && t < monday.getTime() + 7 * DAY_MS;
}

/** The focus_blocks rows of a week, as instants, in start order. */
async function focusRows(
  db: Db,
  week: string,
): Promise<Array<{ google_event_id: string; start: string; end: string }>> {
  const end = isoDate(new Date(Date.parse(`${week}T00:00:00Z`) + 7 * DAY_MS));
  const rows = await db.query<{ google_event_id: string; start_time: Date; end_time: Date }>(
    `SELECT google_event_id, start_time, end_time FROM focus_blocks
      WHERE date >= $1 AND date < $2 ORDER BY start_time`,
    [week, end],
  );
  return rows.map((r) => ({
    google_event_id: r.google_event_id,
    start: r.start_time.toISOString(),
    end: r.end_time.toISOString(),
  }));
}

/** Clear a focus week through the product, then make sure no block survives. */
async function clearFocusWeek(api: ApiClient, db: Db, week: string): Promise<void> {
  await api.delete(`/api/focus/blocks?week=${week}`);
  const end = isoDate(new Date(Date.parse(`${week}T00:00:00Z`) + 7 * DAY_MS));
  // A leftover block is busy time for its owner, who may host a booking link: never leave one.
  await db.query('DELETE FROM focus_blocks WHERE date >= $1 AND date < $2', [week, end]);
}
