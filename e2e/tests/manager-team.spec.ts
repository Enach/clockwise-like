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
import { ANALYTICS, TEAMS, USERS } from '../seed/ids';
import { isoDate, mondayOf } from '../lib/dates';
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

  test.fixme(
    'rescanning the calendar detects 1:1s and proposes team members',
    // BLOCKED: the "Re-scan calendar for team members" action posts to
    // /api/manager/detect -> engine.ManagerEngine.DetectTeam(), which begins
    // with calendar.NewClient() and returns "calendar client"/"list events"
    // errors without one. Detection is defined entirely in terms of recurring
    // two-person events on the manager's Google Calendar, so it cannot be
    // exercised without SEAM-REQUIRED.md item A.
    async () => {},
  );
});
