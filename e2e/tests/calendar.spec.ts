/**
 * Viewing the calendar.
 *
 * STATUS: the "see my meetings" half of this journey is NOT reachable, and no
 * amount of fixture work changes that. GET /api/calendar/events is
 * api/handlers_calendar.go listEvents() ->
 * calendarHandlers.getCalendarClient() -> calendar.NewClient(ctx, ts) ->
 * googlecalendar.NewService(ctx, option.WithTokenSource(ts)). There is no
 * option.WithEndpoint, no env var, and no injectable interface at or above that
 * call site. The events shown on the dashboard can only come from Google.
 * See SEAM-REQUIRED.md item A.
 *
 * What IS worth asserting today, and is asserted below, is the contract for a
 * user whose calendar is not connected. That is a real product state — every
 * user is in it before they finish OAuth — and it has a defined shape
 * (401 "not connected") that the UI must render as an error rather than as an
 * empty week or as demo data. Silently showing demo fixtures here would be a
 * genuine bug, and this file would catch it.
 */

import { test, expect } from '../fixtures';
import { isoDate, nextBookableWeekday } from '../lib/dates';

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

    // pages/Dashboard.tsx renders <InlineError title="Couldn't load your
    // calendar"> when the events query errors and there are no cached events.
    // src/api/client.ts only falls back to demo data on ApiUnreachableError,
    // and a 401 is an ApiHttpError, so the error MUST be visible.
    await expect(authedPage.getByText(/couldn't load your calendar/i)).toBeVisible();

    // The crucial half: it must not have quietly substituted fixtures.
    await assertNotDemo(authedPage);
  });

  test.fixme(
    'a connected calendar renders its events on the week grid',
    // BLOCKED: needs a fake Google Calendar API. calendar.NewClient() builds a
    // googlecalendar.Service with the library's hardcoded base URL and no
    // override is reachable from configuration. Unblocked by SEAM-REQUIRED.md
    // item A (GOOGLE_CALENDAR_API_BASE_URL threaded into
    // calendar.NewClient via option.WithEndpoint).
    async () => {},
  );

  test.fixme(
    'free/busy for an attendee reflects that attendee\'s real calendar',
    // BLOCKED: same seam. GET /api/calendar/freebusy and POST /api/freebusy
    // both end at calendar.CalendarClient.GetFreeBusy on the same hardcoded
    // service. engine.FreeBusyService degrades to coverage:"unknown" without a
    // token, which is already covered indirectly by the booking coverage
    // assertion, but the "known" branch needs the seam.
    async () => {},
  );
});
