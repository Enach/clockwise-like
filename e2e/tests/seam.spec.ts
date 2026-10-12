/**
 * PAC-43: the calendar seam itself, observed from the running stack.
 *
 *   AC-11  the shipped binary refuses a redirection, and the user sees a failure
 *   AC-22  every redirected behaviour arrives at the stand-in (positive evidence)
 *   AC-25  an on-demand personal-calendar sync reaches the stand-in
 *
 * AC-23 (nothing reached the sink during the whole run) is
 * tests/hermeticity.spec.ts, which Playwright runs after every other project.
 *
 * Attribution. Everything here acts as CONNECTED.seam, whose credentials no
 * other spec uses, and reads the stand-in's request log by that credential's
 * label after a mark taken immediately before acting. The tests in this file
 * run in order in one worker (mode 'default': in order, but a failure does not
 * skip the rest), so an arrival under the seam label after the mark was caused
 * by the exercise between the mark and the read. The two exercises whose
 * backend work is asynchronous (analytics recompute, habit scheduling) run
 * last and match on something only they produce.
 *
 * AC-22 asserts arrivals "regardless of what the product returned" (spec
 * §V2-4): a member whose endpoint answers 500 still passes if its request
 * reached the stand-in. The product's own answers are the subject of other
 * scenarios. The response status is attached to every failure message so a
 * reader can tell the two apart.
 */

import { test, expect, type ApiClient } from '../fixtures';
import { fake, sink, unexplainedSinkConnections, type FakeRequest } from '../fake';
import { CONNECTED, PERSONAL_CALENDAR, SEAM_LINK, TEAMS } from '../seed/ids';
import { atUtc, instant, isoDate, weekDay } from '../lib/dates';

test.describe.configure({ mode: 'default' });

const SEAM = CONNECTED.seam;
const LABEL = SEAM.label;

// The seam user's weeks. A focus block is its owner's busy time (seed.sql), so the
// focus week stays clear of the booking specs (7–18 days ahead) and of
// focus-time.spec.ts (week +4).
const FOCUS_WEEK = () => weekDay(5);
const EXERCISE_DAY = (weekday: number) => weekDay(6, weekday);

/** Run `act`, then wait for an arrival under the seam label that `match` accepts. */
async function expectArrival(
  act: () => Promise<{ status(): number } | void>,
  match: (r: FakeRequest) => boolean,
  what: string,
): Promise<FakeRequest[]> {
  const mark = await fake.mark();
  const res = await act();
  const status = res ? res.status() : 'n/a';
  let seen: FakeRequest[] = [];
  await expect
    .poll(
      async () => {
        seen = await fake.requests(mark, LABEL);
        return seen.some(match);
      },
      {
        message:
          `${what}: no matching arrival at the stand-in under "${LABEL}" ` +
          `(product answered ${status}; arrivals since mark: ${JSON.stringify(seen)})`,
        timeout: 15_000,
      },
    )
    .toBe(true);
  return seen;
}

const op = (name: string) => (r: FakeRequest) => r.operation === name;

test.describe('AC-11 — the shipped binary does not follow a redirection', () => {
  test('the week view shows a failure and the sink at the redirection target records nothing', async ({
    pageAs,
    apiAs,
    assertNotDemo,
  }) => {
    // www.googleapis.com maps to the SINK inside the backend container, and
    // the sink's address is private, so the connection guard would permit a
    // second hop there. Only the redirect policy can stop it — which is the
    // point of choosing this target.
    const target = 'http://www.googleapis.com/calendar/v3/calendars/primary/events';
    const sinkBefore = (await sink.connections()).length;
    const mark = await fake.mark();

    // Armed for every request under the seam label until disarmed, because
    // the dashboard may load more than one events range at once and the one
    // that fails must be the week view's.
    await fake.redirectNext(LABEL, target, 50);
    try {
      const page = await pageAs(SEAM);
      await page.goto('/app');
      await expect(page.getByRole('group', { name: 'Calendar view' })).toBeVisible();

      // The user is shown a failure (src/pages/Dashboard.tsx InlineError,
      // role="alert"), not an empty week and not demo data.
      await expect(
        page.getByRole('alert').filter({ hasText: /couldn.t load your calendar/i }),
      ).toBeVisible();
      await assertNotDemo(page);

      // The API says why, in the backend's own words.
      const day = weekDay(0, 0);
      const res = await apiAs(SEAM).get('/api/calendar/events', {
        start: `${isoDate(day)}T00:00:00Z`,
        end: `${isoDate(weekDay(1, 0))}T00:00:00Z`,
      });
      expect(res.status()).toBe(500);
      expect(await res.text()).toContain('redirect refused');
    } finally {
      await fake.disarmRedirect(LABEL);
    }

    // Positive evidence that the stand-in really answered 302: without it,
    // this test would pass for a backend that never reached the stand-in.
    const redirected = (await fake.requests(mark, LABEL)).filter((r) => r.status === 302);
    expect(redirected.length).toBeGreaterThan(0);

    // And the second hop never happened. The redirection target is plain
    // HTTP, so a followed redirect would arrive on the sink's :80 listener,
    // with the request head (and its Authorization header) recorded. Nothing
    // did.
    //
    // Only :80 is asserted here. Loading /app also calls GET
    // /api/auth/status, which for a connected user looks the user up at
    // https://www.googleapis.com/oauth2/v2/userinfo (handlers_auth.go
    // fetchUserEmail) — a TLS connection to the sink's :443 that has nothing
    // to do with the redirect. That lookup is the AC-23 defect recorded in
    // tests/hermeticity.spec.ts.
    const after = (await sink.connections()).slice(sinkBefore);
    const toTarget = after.filter((c) => c.listener === ':80');
    expect(toTarget, `sink connections during AC-11: ${JSON.stringify(after)}`).toEqual([]);
    expect(unexplainedSinkConnections(after), JSON.stringify(after)).toEqual([]);
  });
});

test.describe('AC-22 — every redirected behaviour arrives at the stand-in', () => {
  // Membership is closed: spec §V2-3's list, minus auto-decline (AG-5: it has
  // no on-demand trigger). The personal-calendar sync's member is the AC-25
  // scenario below.

  test.beforeAll(async () => {
    // A clean seam calendar per run of this file. The seam user's events are
    // seeded by the members that need them.
    await fake.clearCalendar(SEAM.email);
  });

  test('viewing a week\'s events', async ({ apiAs }) => {
    const start = `${isoDate(EXERCISE_DAY(0))}T00:00:00Z`;
    const end = `${isoDate(EXERCISE_DAY(7))}T00:00:00Z`;
    await expectArrival(
      () => apiAs(SEAM).get('/api/calendar/events', { start, end }),
      (r) => r.operation === 'events.list' && r.time_min === start && r.time_max === end,
      'GET /api/calendar/events',
    );
  });

  test('free/busy for an attendee (POST /api/freebusy)', async ({ apiAs }) => {
    // A key no other scenario uses: a failure is cached for 15 minutes.
    const attendee = `ac22.freebusy.${Date.now()}@seam.e2e.test`;
    const day = EXERCISE_DAY(4);
    await expectArrival(
      () =>
        apiAs(SEAM).post('/api/freebusy', {
          emails: [attendee],
          start_time: atUtc(day, '00:00'),
          end_time: atUtc(day, '23:59'),
        }),
      (r) => r.operation === 'freebusy.query' && (r.freebusy_ids ?? []).includes(attendee),
      'POST /api/freebusy',
    );
  });

  test('free/busy for an attendee (GET /api/calendar/freebusy)', async ({ apiAs }) => {
    const attendee = `ac22.calfreebusy.${Date.now()}@seam.e2e.test`;
    const day = EXERCISE_DAY(4);
    await expectArrival(
      () =>
        apiAs(SEAM).get('/api/calendar/freebusy', {
          start: `${isoDate(day)}T00:00:00Z`,
          end: `${isoDate(day)}T23:59:00Z`,
          attendees: attendee,
        }),
      (r) => r.operation === 'freebusy.query' && (r.freebusy_ids ?? []).includes(attendee),
      'GET /api/calendar/freebusy',
    );
  });

  test('focus-time creation', async ({ apiAs, db }) => {
    const week = isoDate(FOCUS_WEEK());
    try {
      await expectArrival(
        () => apiAs(SEAM).post('/api/focus/run', { week }),
        op('events.insert'),
        'POST /api/focus/run',
      );
    } finally {
      await clearFocusWeek(apiAs(SEAM), db, week);
    }
  });

  test('focus-time clearing', async ({ apiAs, db }) => {
    const week = isoDate(FOCUS_WEEK());
    try {
      // Self-contained: create the blocks this member then clears.
      const run = await apiAs(SEAM).post('/api/focus/run', { week });
      expect(run.status(), await run.text()).toBe(200);
      await expectArrival(
        () => apiAs(SEAM).delete(`/api/focus/blocks?week=${week}`),
        op('events.delete'),
        'DELETE /api/focus/blocks',
      );
    } finally {
      await clearFocusWeek(apiAs(SEAM), db, week);
    }
  });

  test('compression', async ({ apiAs }) => {
    const date = isoDate(EXERCISE_DAY(1));
    await expectArrival(
      () => apiAs(SEAM).post('/api/schedule/compress', { date }),
      (r) => r.operation === 'events.list' && (r.time_min ?? '').startsWith(date),
      'POST /api/schedule/compress',
    );
  });

  test('smart scheduling (suggest)', async ({ apiAs }) => {
    const day = EXERCISE_DAY(2);
    await expectArrival(
      () =>
        apiAs(SEAM).post('/api/schedule/suggest', {
          duration_minutes: 30,
          attendees: [SEAM.email],
          range_start: atUtc(day, '09:00'),
          range_end: atUtc(day, '17:00'),
          title: 'E2E AC-22 suggest',
        }),
      (r) => r.operation === 'events.list' || r.operation === 'freebusy.query',
      'POST /api/schedule/suggest',
    );
  });

  test('natural-language confirmation', async ({ apiAs }) => {
    const day = EXERCISE_DAY(2);
    const title = `E2E AC-22 NLP confirm ${Date.now()}`;
    await expectArrival(
      () =>
        apiAs(SEAM).post('/api/nlp/confirm', {
          parse_result: {
            intent: 'schedule',
            title,
            duration_minutes: 30,
            suggested_slots: [
              { start: atUtc(day, '15:00'), end: atUtc(day, '15:30'), score: 1, reasons: [] },
            ],
          },
          selected_slot_index: 0,
        }),
      (r) => r.operation === 'events.insert' && r.summary === title,
      'POST /api/nlp/confirm',
    );
  });

  test('collective slots', async ({ api }) => {
    await expectArrival(
      () =>
        api.get(`/api/book/${SEAM_LINK.slug}/slots`, {
          date: isoDate(EXERCISE_DAY(3)),
          duration: 30,
        }),
      (r) => r.operation === 'events.list' || r.operation === 'freebusy.query',
      'GET /api/book/{slug}/slots',
    );
  });

  test('booking confirmation', async ({ api, db }) => {
    const bookerPrefix = `e2e.booker.seam.${Date.now()}`;
    const day = EXERCISE_DAY(3);
    try {
      await expectArrival(
        () =>
          api.post(`/api/book/${SEAM_LINK.slug}`, {
            name: 'E2E Seam Booker',
            email: `${bookerPrefix}@paceday.test`,
            start: atUtc(day, '16:00'),
            end: atUtc(day, '16:30'),
          }),
        (r) => r.operation === 'events.insert' && r.summary === SEAM_LINK.title,
        'POST /api/book/{slug}',
      );
    } finally {
      await db.deleteBookingsByBooker(bookerPrefix);
    }
  });

  test('manager 1:1 detection', async ({ apiAs }) => {
    await expectArrival(
      () => apiAs(SEAM).post(`/api/manager/detect?team_id=${TEAMS.seam.id}`),
      (r) => {
        if (r.operation !== 'events.list' || !r.time_min || !r.time_max) return false;
        // DetectTeam scans now ± 42 days.
        const days = (Date.parse(r.time_max) - Date.parse(r.time_min)) / 86_400_000;
        return Math.round(days) === 84;
      },
      'POST /api/manager/detect',
    );
  });

  test('team availability', async ({ apiAs }) => {
    const date = isoDate(EXERCISE_DAY(2));
    await expectArrival(
      () => apiAs(SEAM).get(`/api/teams/${TEAMS.seam.id}/availability`, { date, duration: 30 }),
      (r) => r.operation === 'events.list' && (r.time_min ?? '').startsWith(date),
      'GET /api/teams/{id}/availability',
    );
  });

  test('event patch', async ({ apiAs }) => {
    const day = EXERCISE_DAY(1);
    const [ev] = await fake.seedEvents(SEAM.email, [
      { summary: 'E2E AC-22 to patch', start: { dateTime: atUtc(day, '10:00') }, end: { dateTime: atUtc(day, '10:30') } },
    ]);
    const title = `E2E AC-22 patched ${Date.now()}`;
    await expectArrival(
      () => apiAs(SEAM).patch(`/api/events/${ev!.id}`, { title }),
      (r) => r.operation === 'events.update' && r.event_id === ev!.id && r.summary === title,
      'PATCH /api/events/{id}',
    );
  });

  test('event delete', async ({ apiAs }) => {
    const day = EXERCISE_DAY(1);
    const [ev] = await fake.seedEvents(SEAM.email, [
      { summary: 'E2E AC-22 to delete', start: { dateTime: atUtc(day, '11:00') }, end: { dateTime: atUtc(day, '11:30') } },
    ]);
    await expectArrival(
      () => apiAs(SEAM).delete(`/api/events/${ev!.id}`),
      (r) => r.operation === 'events.delete' && r.event_id === ev!.id,
      'DELETE /api/events/{id}',
    );
  });

  test('room suggestion', async ({ apiAs }) => {
    await expectArrival(
      () => apiAs(SEAM).get('/api/rooms'),
      op('calendarList.list'),
      'GET /api/rooms',
    );
  });

  test('attendee suggestion', async ({ apiAs }) => {
    await expectArrival(
      () => apiAs(SEAM).get('/api/attendees/suggest'),
      op('events.list'),
      'GET /api/attendees/suggest',
    );
  });

  // The two asynchronous members last (see the file header).

  test('weekly analytics recompute (asynchronous)', async ({ apiAs }) => {
    await expectArrival(
      () => apiAs(SEAM).post('/api/analytics/recompute'),
      op('events.list'),
      'POST /api/analytics/recompute',
    );
  });

  test('habit scheduling (asynchronous)', async ({ apiAs }) => {
    const title = `E2E AC-22 habit ${Date.now()}`;
    await expectArrival(
      () => apiAs(SEAM).post('/api/habits', { title, duration_minutes: 30 }),
      (r) => r.operation === 'events.insert' && r.summary === title,
      'POST /api/habits',
    );
  });
});

test.describe('AC-25 — the personal-calendar sync reaches the stand-in', () => {
  test('an on-demand sync reads the personal calendar with the personal credential and writes blockers to the work calendar', async ({
    apiAs,
  }) => {
    // Inside the sync's look-ahead (today + 14 days) and inside work hours
    // (09:00–18:00 UTC, seed.sql settings), so it must become a blocker.
    const day = weekDay(1, 2);
    const start = atUtc(day, '12:00');
    const end = atUtc(day, '13:00');
    await fake.clearCalendar(PERSONAL_CALENDAR.calendar);
    await fake.seedEvents(PERSONAL_CALENDAR.calendar, [
      { summary: 'E2E dentist (personal)', start: { dateTime: start }, end: { dateTime: end } },
    ]);
    const sinkBefore = (await sink.connections()).length;
    const mark = await fake.mark();

    const res = await apiAs(SEAM).post(`/api/personal-calendars/${PERSONAL_CALENDAR.id}/sync`);
    expect(res.status(), await res.text()).toBe(200);

    const arrivals = await fake.requests(mark);

    // The primary-provider branch read the personal calendar, with the
    // PERSONAL credential (personal_reader.go), not the work one.
    const personalRead = arrivals.filter(
      (r) =>
        r.credential === PERSONAL_CALENDAR.label &&
        r.operation === 'events.list' &&
        r.calendar_id === PERSONAL_CALENDAR.calendar,
    );
    expect(personalRead, JSON.stringify(arrivals)).toHaveLength(1);

    // syncOne then wrote the blocker through the WORK credential (newCalOps).
    const blockerWrite = arrivals.filter(
      (r) =>
        r.credential === LABEL &&
        r.operation === 'events.insert' &&
        r.calendar_id === SEAM.email &&
        r.summary === '[Personal] Busy',
    );
    expect(blockerWrite, JSON.stringify(arrivals)).toHaveLength(1);

    // And it is really there, at the personal event's time.
    const work = await fake.events(SEAM.email);
    const blockers = work.filter((e) => e.summary === '[Personal] Busy');
    expect(
      blockers.map((e) => [instant(e.start.dateTime!), instant(e.end.dateTime!)]),
    ).toContainEqual([start, end]);

    // Nothing went to a real provider. (Specs running in parallel may add the
    // known status-lookup connection — see fake.ts unexplainedSinkConnections.
    // A personal sync that went to the published address instead would also
    // have produced no stand-in arrival above, so it cannot hide behind that.)
    const after = (await sink.connections()).slice(sinkBefore);
    expect(unexplainedSinkConnections(after), JSON.stringify(after)).toEqual([]);
  });
});

/** Clear a focus week through the product, then make sure no block survives. */
async function clearFocusWeek(
  api: ApiClient,
  db: { query: (sql: string, params?: unknown[]) => Promise<unknown[]> },
  week: string,
): Promise<void> {
  await api.delete(`/api/focus/blocks?week=${week}`);
  const end = isoDate(new Date(Date.parse(`${week}T00:00:00Z`) + 7 * 86_400_000));
  // A leftover block is busy time for its owner, who may host a booking link: never leave one.
  await db.query('DELETE FROM focus_blocks WHERE date >= $1 AND date < $2', [week, end]);
}
