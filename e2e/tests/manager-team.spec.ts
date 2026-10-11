/**
 * Manager team roster, scoped to the selected team.
 *
 * This journey IS reachable without a calendar provider, which is not obvious
 * and was verified by reading engine/manager.go:
 *   GET /api/manager/team?team_id= -> handlers_manager.go getTeam() ->
 *   listManagerMembers() (pure database: manager_team_member_assignments
 *   joined with the formal team_members of that team) and then, per member,
 *   engine.ManagerEngine.GetMemberWeek(). GetMemberWeek takes the
 *   `member.MemberUserID != nil` branch for a member who is a Paceday user and
 *   reads analytics_weeks straight out of Postgres — no calendar call at all.
 *   Only the EXTERNAL-member branch goes through FreeBusyService, and even that
 *   skips the network when the manager has no oauth token.
 * The seed therefore links both roster members to real users and gives them
 * analytics_weeks rows, so the focus figures below are exact.
 *
 * The scoping assertion is the load-bearing one: Alpha and Beta have disjoint
 * members, so if the ?team_id= filter is dropped, Beta's member leaks into
 * Alpha's roster and this fails.
 */

import { test, expect } from '../fixtures';
import { BASE_URL, mintSessionToken } from '../auth';
import { ANALYTICS, CONNECTED, TEAMS, USERS } from '../seed/ids';
import { atUtc, DAY_MS, isoDate, mondayOf } from '../lib/dates';
import { fake, type FakeEvent } from '../fake';
import { switchTeam } from '../lib/ui';

const thisWeek = () => isoDate(mondayOf(new Date()));

test.describe('manager roster — the API', () => {
  test('returns only the members assigned to the requested team', async ({ authedApi }) => {
    const res = await authedApi.get('/api/manager/team', {
      team_id: TEAMS.alpha.id,
      week: thisWeek(),
    });
    expect(res.status()).toBe(200);

    const body = await res.json();
    expect(body.team_id).toBe(TEAMS.alpha.id);

    const emails = body.members.map((m: { email: string }) => m.email);
    expect(emails).toEqual([USERS.alphaReport.email]);
    // The scoping failure mode, stated explicitly.
    expect(emails).not.toContain(USERS.betaReport.email);

    const member = body.members[0];
    expect(member).toMatchObject({
      email: USERS.alphaReport.email,
      display_name: USERS.alphaReport.name,
      cadence: 'weekly',
      is_paceday_user: true,
    });

    // Exact figures from analytics_weeks. A shape-only assertion here would
    // still pass if GetMemberWeek silently returned zeroes for everyone.
    expect(member.this_week.focus_minutes).toBe(ANALYTICS.alphaReport.thisWeekFocusMinutes);
    expect(member.last_week.focus_minutes).toBe(ANALYTICS.alphaReport.priorWeekFocusMinutes);
    expect(member.this_week.data_available).toBe(true);
  });

  test('the other team returns its own, different roster', async ({ authedApi }) => {
    const res = await authedApi.get('/api/manager/team', {
      team_id: TEAMS.beta.id,
      week: thisWeek(),
    });
    expect(res.status()).toBe(200);

    const body = await res.json();
    expect(body.members.map((m: { email: string }) => m.email)).toEqual([
      USERS.betaReport.email,
    ]);
    expect(body.members[0]).toMatchObject({
      display_name: USERS.betaReport.name,
      cadence: 'biweekly',
    });
    expect(body.members[0].this_week.focus_minutes).toBe(
      ANALYTICS.betaReport.thisWeekFocusMinutes,
    );
  });

  test('requires a team_id', async ({ authedApi }) => {
    const res = await authedApi.get('/api/manager/team', { week: thisWeek() });
    expect(res.status()).toBe(400);
  });

  test('refuses a team the caller does not belong to', async ({ playwright }) => {
    // The second seeded user is in neither team.
    const request = await playwright.request.newContext();
    const res = await request.get(`${BASE_URL}/api/manager/team`, {
      headers: { Authorization: `Bearer ${mintSessionToken(USERS.second)}` },
      params: { team_id: TEAMS.alpha.id, week: thisWeek() },
    });
    // managerTeamScope() -> storage.GetTeamMember errors -> 403.
    expect(res.status()).toBe(403);
    await request.dispose();
  });

  test('rejects a malformed team_id', async ({ authedApi }) => {
    const res = await authedApi.get('/api/manager/team', { team_id: 'not-a-uuid' });
    expect(res.status()).toBe(400);
  });
});

test.describe('manager roster — in the browser', () => {
  test('the roster shows only the active team and follows the team switcher', async ({
    authedPage,
    assertNotDemo,
  }) => {
    await authedPage.goto('/app/team');

    // The teams list comes from GET /api/teams. The header names the active one.
    await expect(authedPage.getByRole('heading', { name: 'My Team' })).toBeVisible();

    // teamsApi.list() sorts by name, and the page defaults to the first team,
    // so Alpha is active on arrival.
    await expect(
      authedPage.getByRole('heading', { name: `${TEAMS.alpha.name} members` }),
    ).toBeVisible();
    // .first(): an overdue member is also listed in the 1:1 gaps section above
    // the roster, so the name legitimately appears more than once.
    await expect(authedPage.getByText(USERS.alphaReport.name).first()).toBeVisible();
    await expect(authedPage.getByText(USERS.betaReport.name)).toHaveCount(0);

    // Demo data would show Sarah Chen and friends here; assertNotDemo makes
    // that impossible to mistake for a pass.
    await assertNotDemo(authedPage);

    await switchTeam(authedPage, TEAMS.beta.name);

    await expect(
      authedPage.getByRole('heading', { name: `${TEAMS.beta.name} members` }),
    ).toBeVisible();
    await expect(authedPage.getByText(USERS.betaReport.name).first()).toBeVisible();
    await expect(authedPage.getByText(USERS.alphaReport.name)).toHaveCount(0);
  });

});

test.describe('manager 1:1 detection — with a connected calendar (PAC-43, through the stand-in)', () => {
  const MANAGER = CONNECTED.manager;

  test('AC-20: scanning the calendar detects 1:1s and proposes exactly those team members', async ({
    pageAs,
    assertNotDemo,
  }) => {
    // POST /api/manager/detect -> engine.ManagerEngine.DetectTeam: list the
    // manager's events for now ± 42 days and keep recurring meetings with
    // exactly one other person, 15–90 minutes long (engine/manager.go
    // collectOneOnOneCandidates). The stand-in holds, as Google returns them
    // with singleEvents=true, expanded instances carrying recurringEventId:
    const run = Date.now();
    const ada = { email: `e2e.ada.${run}@colleague.e2e.test`, displayName: 'E2E Ada Colleague' };
    const bob = { email: `e2e.bob.${run}@colleague.e2e.test`, displayName: 'E2E Bob Colleague' };
    const cyd = { email: `e2e.cyd.${run}@colleague.e2e.test`, displayName: 'E2E Cyd Group' };
    const dan = { email: `e2e.dan.${run}@colleague.e2e.test`, displayName: 'E2E Dan OneOff' };
    const eve = { email: `e2e.eve.${run}@colleague.e2e.test`, displayName: 'E2E Eve Workshop' };
    const me = { email: MANAGER.email, self: true, responseStatus: 'accepted' };
    const daysAgo = (n: number, hhmm: string) => atUtc(new Date(Date.now() - n * DAY_MS), hhmm);
    const instance = (
      series: string,
      n: number,
      start: string,
      minutes: number,
      who: Array<{ email: string; displayName?: string }>,
    ): FakeEvent => ({
      summary: `E2E ${series}`,
      recurringEventId: `${series}-${run}`,
      start: { dateTime: daysAgo(n, start) },
      end: { dateTime: new Date(Date.parse(daysAgo(n, start)) + minutes * 60_000).toISOString() },
      attendees: [me, ...who.map((w) => ({ ...w, responseStatus: 'accepted' }))],
    });

    await fake.clearCalendar(MANAGER.email);
    await fake.seedEvents(MANAGER.email, [
      // Weekly 1:1 with Ada: proposed.
      instance('ada-1on1', 7, '10:00', 30, [ada]),
      instance('ada-1on1', 14, '10:00', 30, [ada]),
      instance('ada-1on1', 21, '10:00', 30, [ada]),
      // Biweekly 1:1 with Bob: proposed.
      instance('bob-1on1', 10, '14:00', 45, [bob]),
      instance('bob-1on1', 24, '14:00', 45, [bob]),
      // A recurring three-person meeting: not a 1:1.
      instance('group-sync', 8, '11:00', 30, [ada, cyd]),
      instance('group-sync', 15, '11:00', 30, [ada, cyd]),
      // A single, non-recurring two-person meeting: not a series.
      {
        summary: 'E2E one-off coffee',
        start: { dateTime: daysAgo(5, '15:00') },
        end: { dateTime: daysAgo(5, '15:30') },
        attendees: [me, { ...dan, responseStatus: 'accepted' }],
      },
      // A recurring two-person meeting longer than 90 minutes: not a 1:1.
      instance('eve-workshop', 9, '13:00', 120, [eve]),
      instance('eve-workshop', 16, '13:00', 120, [eve]),
    ]);

    const mark = await fake.mark();
    const page = await pageAs(MANAGER, { isManager: true });
    await page.goto('/app/team');
    await assertNotDemo(page);

    // The team was seeded with nobody in it, so the page offers a scan.
    const detect = page.waitForResponse(
      (r) => r.url().includes('/api/manager/detect') && r.request().method() === 'POST',
    );
    await page.getByRole('button', { name: 'Scan calendar now' }).click();
    const res = await detect;
    expect(res.status(), await res.text()).toBe(200);

    // Exactly the two colleagues, and nobody else, are proposed — in the API...
    const body = (await res.json()) as { candidates: Array<{ email: string }> };
    expect(body.candidates.map((c) => c.email).sort()).toEqual([ada.email, bob.email].sort());

    // ...and on screen, in the preview the manager confirms from.
    const dialog = page.getByRole('dialog', { name: 'Choose people to add' });
    await expect(dialog).toBeVisible();
    await expect(dialog.getByRole('checkbox')).toHaveCount(2);
    await expect(dialog.getByText(ada.email)).toBeVisible();
    await expect(dialog.getByText(bob.email)).toBeVisible();
    for (const other of [cyd, dan, eve]) {
      await expect(dialog.getByText(other.email)).toHaveCount(0);
    }

    // Detection read the manager's calendar over the six-week window, with
    // the manager's credential.
    const asked = (await fake.requests(mark, MANAGER.label)).filter(
      (r) => r.operation === 'events.list' && r.calendar_id === MANAGER.email,
    );
    expect(asked).toHaveLength(1);
    const spanDays = (Date.parse(asked[0]!.time_max!) - Date.parse(asked[0]!.time_min!)) / DAY_MS;
    expect(Math.round(spanDays)).toBe(84);
  });
});
