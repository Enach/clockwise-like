/**
 * Viewing the calendar.
 *
 * Two halves.
 *
 * The disconnected state (AC-24): a user with no stored calendar credential.
 * That is a real product state — every user is in it before they finish OAuth
 * — and it has a defined shape (401 "not connected") that the UI must render
 * as an error rather than as an empty week or as demo data. The four original
 * seeded users have no credential, which is what keeps these true.
 *
 * A connected calendar (PAC-43): GET /api/calendar/events ends at
 * calendar.NewClient, which in the e2e stack is pointed at the Google Calendar
 * stand-in (e2e/fake-google) by GOOGLE_CALENDAR_API_BASE_URL. The stand-in is
 * the external provider, not the backend: the backend's handler, client
 * library, credential and DTO mapping are all real. Each scenario seeds the
 * provider's state through e2e/fake.ts and asserts what the backend and the
 * UI made of it, plus what the provider was asked.
 */

import { test, expect } from '../fixtures';
import { atUtc, instant, isoDate, nextBookableWeekday, weekDay } from '../lib/dates';
import { fake } from '../fake';
import { CONNECTED } from '../seed/ids';

test.describe('calendar — the disconnected state', () => {
  test('GET /api/calendar/events is 401 when the user has no calendar token', async ({
    authedApi,
  }) => {
    const day = nextBookableWeekday(1);
    const res = await authedApi.get('/api/calendar/events', {
      start: `${isoDate(day)}T00:00:00Z`,
      end: `${isoDate(day)}T23:59:59Z`,
    });

    // 401 and not 500: handlers_calendar.go maps the missing token to
    // "not connected" with StatusUnauthorized. A 500 here would mean the
    // handler tried to reach Google (which the compose file blackholes) instead
    // of checking the token first — a real regression this catches.
    expect(res.status()).toBe(401);
    expect(await res.text()).toContain('not connected');
  });

  test('GET /api/calendar/events validates its time range before anything else', async ({
    authedApi,
  }) => {
    const res = await authedApi.get('/api/calendar/events', {
      start: 'not-a-timestamp',
      end: 'also-not',
    });
    expect(res.status()).toBe(400);
  });

  test('the dashboard surfaces the failure instead of inventing a week', async ({
    authedPage,
    assertNotDemo,
  }) => {
    await authedPage.goto('/app');
    await expect(authedPage).toHaveURL(/\/app$/);

    // The calendar chrome renders regardless — it is the shell, not the data.
    await expect(authedPage.getByRole('group', { name: 'Calendar view' })).toBeVisible();
    await expect(authedPage.getByRole('group', { name: 'Calendar navigation' })).toBeVisible();

    // With no calendar connected, /api/calendar/events answers 401 "not
    // connected". Since smart-calendar-flow#82 the dashboard says so plainly
    // ("Connect your calendar…") instead of misreporting it as an expired
    // session. src/api/client.ts only falls back to demo data on
    // ApiUnreachableError, and a 401 is an ApiHttpError, so the prompt MUST be
    // visible.
    await expect(authedPage.getByText(/connect your calendar to see your week/i)).toBeVisible();
    await expect(authedPage.getByText(/session expired/i)).toHaveCount(0);

    // The crucial half: it must not have quietly substituted fixtures.
    await assertNotDemo(authedPage);
  });

});

test.describe('calendar — a connected calendar (PAC-43, through the stand-in)', () => {
  const USER = CONNECTED.calendar;

  test('AC-16: a connected calendar renders its events on the week grid', async ({
    apiAs,
    pageAs,
    assertNotDemo,
  }) => {
    // This week, so the dashboard shows it without navigating. Two days that
    // are not today, so TodayAgenda does not list the same titles a second
    // time.
    const todayIdx = (new Date().getUTCDay() + 6) % 7; // 0 = Monday
    const [d1, d2] = [0, 1, 2, 3, 4].filter((d) => d !== todayIdx);
    const run = Date.now();
    const inWeek = [
      {
        summary: `E2E standup ${run}`,
        start: { dateTime: atUtc(weekDay(0, d1!), '09:30') },
        end: { dateTime: atUtc(weekDay(0, d1!), '10:00') },
      },
      {
        summary: `E2E planning ${run}`,
        description: 'quarterly',
        start: { dateTime: atUtc(weekDay(0, d2!), '14:00') },
        end: { dateTime: atUtc(weekDay(0, d2!), '15:30') },
        attendees: [{ email: USER.email, responseStatus: 'accepted', self: true }],
      },
    ];
    // Outside the window: must not be returned.
    const outside = {
      summary: `E2E next week ${run}`,
      start: { dateTime: atUtc(weekDay(1, 1), '11:00') },
      end: { dateTime: atUtc(weekDay(1, 1), '12:00') },
    };
    await fake.clearCalendar(USER.email);
    const seeded = await fake.seedEvents(USER.email, [...inWeek, outside]);

    const start = `${isoDate(weekDay(0))}T00:00:00Z`;
    const end = `${isoDate(weekDay(1))}T00:00:00Z`;
    const mark = await fake.mark();
    const res = await apiAs(USER).get('/api/calendar/events', { start, end });
    expect(res.status(), await res.text()).toBe(200);

    // Exactly those events, in the backend's CalendarEvent shape.
    const body = (await res.json()) as Array<{ id: string; title: string; start: string; end: string }>;
    expect(
      body.map((e) => ({ id: e.id, title: e.title, start: instant(e.start), end: instant(e.end) })),
    ).toEqual(
      seeded.slice(0, 2).map((e) => ({
        id: e.id,
        title: e.summary,
        start: instant(e.start.dateTime!),
        end: instant(e.end.dateTime!),
      })),
    );

    // The stand-in was asked for that window, with this user's credential.
    const asked = await fake.requests(mark, USER.label);
    expect(
      asked.filter(
        (r) =>
          r.operation === 'events.list' &&
          r.calendar_id === USER.email &&
          instant(r.time_min!) === instant(start) &&
          instant(r.time_max!) === instant(end),
      ),
    ).toHaveLength(1);

    // And the user sees them on the week grid. The titles are user-visible
    // content, not a CSS hook; FullCalendar offers no role for an event.
    const page = await pageAs(USER);
    await page.goto('/app');
    await expect(page.getByRole('group', { name: 'Calendar view' })).toBeVisible();
    for (const e of inWeek) {
      await expect(page.getByText(e.summary, { exact: true })).toBeVisible();
    }
    await expect(page.getByText(outside.summary, { exact: true })).toHaveCount(0);
    await expect(page.getByText(/connect your calendar to see your week/i)).toHaveCount(0);
    await assertNotDemo(page);
  });

  test('AC-17: free/busy for an attendee reflects that attendee\'s real calendar', async ({
    apiAs,
  }) => {
    // engine.FreeBusyService caches every answer, an "unknown" one included,
    // for 15 minutes per (requester, attendee, date) in process memory (spec
    // §V3-5). So: an attendee no other scenario — and no earlier run against
    // this stack — uses, seeded BEFORE the first request.
    //
    // Only the primary-provider case: the deployment's provider setting is
    // google (seed.ts verify checks it). This asserts nothing about a user of
    // another provider; that is PAC-47's.
    const attendee = `e2e.ac17.${Date.now()}@attendee.e2e.test`;
    const day = weekDay(7, 2);
    const busy = [
      [atUtc(day, '09:00'), atUtc(day, '10:30')],
      [atUtc(day, '15:00'), atUtc(day, '16:00')],
    ] as const;
    await fake.seedEvents(attendee, [
      { summary: 'busy 1', start: { dateTime: busy[0][0] }, end: { dateTime: busy[0][1] } },
      { summary: 'busy 2', start: { dateTime: busy[1][0] }, end: { dateTime: busy[1][1] } },
      // Free time on the attendee's calendar is not busy, as on Google.
      {
        summary: 'transparent',
        transparency: 'transparent',
        start: { dateTime: atUtc(day, '12:00') },
        end: { dateTime: atUtc(day, '13:00') },
      },
    ]);

    const mark = await fake.mark();
    const res = await apiAs(USER).post('/api/freebusy', {
      emails: [attendee],
      start_time: atUtc(day, '00:00'),
      end_time: atUtc(day, '23:59'),
    });
    expect(res.status(), await res.text()).toBe(200);
    const body = (await res.json()) as {
      participants: Array<{ email: string; status: string; busy: Array<{ start: string; end: string }> }>;
    };

    // "known", not "unknown": per spec §V2-2.1 the only observable difference
    // between reaching the stand-in and reaching nothing on this path.
    expect(body.participants).toHaveLength(1);
    expect(body.participants[0]!.email).toBe(attendee);
    expect(body.participants[0]!.status).toBe('known');
    expect(body.participants[0]!.busy.map((b) => [instant(b.start), instant(b.end)])).toEqual(
      busy.map(([s, e]) => [instant(s), instant(e)]),
    );

    // Asked once, for that attendee, with the requester's credential.
    const asked = await fake.requests(mark, USER.label);
    expect(
      asked.filter((r) => r.operation === 'freebusy.query' && r.freebusy_ids?.includes(attendee)),
    ).toHaveLength(1);
  });

  test('AC-21: a stored credential the provider rejects is refused, not served', async ({
    apiAs,
    pageAs,
    assertNotDemo,
  }) => {
    // AC-21's first half is every other connected scenario: the stand-in
    // refuses any request without an accepted bearer (stand-in.setup.ts proves
    // it), so AC-16, AC-18, AC-19 and AC-20 pass only because the backend
    // still sends the user's credential. This is the second half: a user
    // whose stored credential the provider does not accept.
    const rejected = CONNECTED.rejected;
    const start = `${isoDate(weekDay(0))}T00:00:00Z`;
    const end = `${isoDate(weekDay(1))}T00:00:00Z`;
    const mark = await fake.mark();

    const res = await apiAs(rejected).get('/api/calendar/events', { start, end });
    expect(res.ok(), await res.text()).toBe(false);
    // Refused with the generic 5xx body: since PAC-21 the provider's own
    // error text goes to the server log and Sentry, never to the client.
    expect(res.status()).toBe(500);
    const body = await res.text();
    expect(body.trim()).toBe('internal error');
    expect(body).not.toMatch(/invalid authentication credentials|googleapi/i);

    // It really reached the stand-in and was refused there — not refused
    // locally for want of a token.
    const asked = await fake.requests(mark, 'rejected');
    expect(asked.filter((r) => r.operation === 'events.list' && r.status === 401).length).toBeGreaterThan(0);

    // And the screen says so rather than inventing a week.
    const page = await pageAs(rejected);
    await page.goto('/app');
    await expect(
      page.getByRole('alert').filter({ hasText: /couldn.t load your calendar/i }),
    ).toBeVisible();
    await assertNotDemo(page);
  });
});
