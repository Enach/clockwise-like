/**
 * Focus time.
 *
 * STATUS: "run focus time and see blocks appear" is NOT reachable. This was
 * checked against the code, not guessed:
 *
 *   POST /api/focus/run -> engine.FocusTimeEngine.RunForUser, whose third step
 *   is `client, err := e.calClient(ctx)`. calClient falls through to
 *   newCalOps() (engine/cal_iface.go), which loads the user's oauth token and
 *   returns `fmt.Errorf("not authenticated")` when there is none. RunForUser
 *   returns that error, and handlers_focus.go turns it into a 500. So with no
 *   token, the run cannot start.
 *
 *   Seeding a token does not help either: processDay() then calls
 *   listEvents() and createEvent() on a googlecalendar.Service pointed at the
 *   library's hardcoded base URL. The blocks the journey is supposed to produce
 *   ARE Google Calendar events — the focus_blocks table stores their event ids.
 *   There is no code path that creates a focus block without a calendar.
 *
 * There is a real seam INSIDE the package — engine.FocusTimeEngine has an
 * unexported `calOps calendarOps` field that its unit tests substitute — but it
 * is unexported and never set from main.go, so nothing outside the process can
 * reach it. SEAM-REQUIRED.md item A describes the smallest change that makes
 * this journey testable.
 *
 * What is asserted below is the part that does not need a calendar: the
 * read/clear side of the focus API, and the fact that the run fails loudly
 * rather than reporting a fake success.
 */

import { test, expect } from '../fixtures';
import { isoDate, mondayOf } from '../lib/dates';

test.describe('focus time — what is reachable without a calendar provider', () => {
  test('listing blocks for a week returns an empty set, not an error', async ({ authedApi }) => {
    const week = isoDate(mondayOf(new Date()));
    const res = await authedApi.get('/api/focus/blocks', { week });
    expect(res.status()).toBe(200);

    // storage.ListFocusBlocksForWeek reads the table directly, no calendar
    // involved. The seed asserts the table is empty, so this is an exact
    // expectation rather than a shape check.
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
    // tell that apart from "there was no room this week". A 500 carrying
    // "not authenticated" is the current, honest behaviour.
    expect(res.status()).toBe(500);
    expect(await res.text()).toContain('not authenticated');

    // And nothing must have been written.
    const blocks = await authedApi.get('/api/focus/blocks', {
      week: isoDate(mondayOf(new Date())),
    });
    expect(await blocks.json()).toEqual([]);
  });

  test.fixme(
    'running focus time creates blocks and they appear on the calendar',
    // BLOCKED: see the file header. Needs SEAM-REQUIRED.md item A so that
    // engine.FocusTimeEngine can list and create events against a fake Google
    // Calendar. Once that exists this test should: seed two meetings on the
    // fake provider, POST /api/focus/run, assert the response's createdBlocks
    // total matches settings.focus_daily_target_minutes for each free day,
    // assert the rows in focus_blocks, and assert the blocks render on /app.
    async () => {},
  );

  test.fixme(
    'clearing a week removes the blocks from both the database and the calendar',
    // BLOCKED: same seam. DELETE /api/focus/blocks -> FocusTimeEngine.ClearWeek
    // also goes through calOps to delete the Google events, so it cannot be
    // exercised end to end either.
    async () => {},
  );
});
