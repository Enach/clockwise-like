/**
 * PAC-34 AC-5: the invite page says who invited you and when the invite
 * expires, and the manager analytics panel says "no data" for a member with no
 * measured week instead of drawing zero minutes.
 *
 * Each test owns its users, team and invite (run-unique emails), so it cannot
 * contend with the seeded roster in manager-team.spec.ts or with a parallel run.
 *
 * Invite: GET /api/teams/invites/{token} -> handlers_teams.go getInvite, which
 * now carries team_id, expires_at and inviter_email beside teamName,
 * inviterName and email (API-021). The invite row is inserted directly because
 * POST /api/teams/{id}/members/invite also sends an email, which is not what
 * this journey is about.
 *
 * Analytics: GET /api/manager/analytics?team_id= -> getAnalytics ->
 * GetMemberWeek. The member is a Paceday user on a team the manager owns
 * (so the PAC-60 gate lets the read through) with no analytics_weeks row at
 * all, so all 12 buckets are unmeasured and the member's data_available is
 * false (API-022).
 */

import { test, expect, type Db } from '../fixtures';
import type { SessionUser } from '../auth';

async function freshUser(db: Db, label: string, run: string): Promise<SessionUser> {
  const email = `e2e.pac34.${label}.${run}@pac34.e2e.test`;
  const name = `E2E PAC34 ${label} ${run}`;
  const row = await db.queryOne<{ id: string }>(
    'INSERT INTO users (email, name) VALUES ($1, $2) RETURNING id',
    [email, name],
  );
  return { id: row!.id, email, name };
}

async function teamOwnedBy(db: Db, owner: SessionUser, run: string, members: SessionUser[]) {
  const team = await db.queryOne<{ id: string; name: string }>(
    'INSERT INTO teams (name, created_by) VALUES ($1, $2) RETURNING id, name',
    [`E2E PAC34 Team ${run}`, owner.id],
  );
  await db.query("INSERT INTO team_members (team_id, user_id, role) VALUES ($1, $2, 'owner')", [team!.id, owner.id]);
  for (const m of members) {
    await db.query("INSERT INTO team_members (team_id, user_id, role) VALUES ($1, $2, 'member')", [team!.id, m.id]);
  }
  return team!;
}

test.describe('PAC-34 invite preview', () => {
  test('the invite page shows the inviter and the expiry of a fresh invite', async ({ db, apiAs, pageAs, assertNotDemo }) => {
    const run = `${Date.now()}${Math.floor(Math.random() * 1000)}`;
    const inviter = await freshUser(db, 'inviter', run);
    const invitee = await freshUser(db, 'invitee', run);
    const team = await teamOwnedBy(db, inviter, run, []);
    const token = `e2e-pac34-${run}`;
    // Noon UTC seven days out: the same calendar date in every browser time zone the suite runs in.
    const expires = new Date(Date.now() + 7 * 24 * 3600 * 1000);
    expires.setUTCHours(12, 0, 0, 0);
    await db.query(
      'INSERT INTO team_invites (team_id, invitee_email, invited_by, token, expires_at) VALUES ($1, $2, $3, $4, $5)',
      [team.id, invitee.email, inviter.id, token, expires.toISOString()],
    );

    // The HTTP boundary first: exactly the six contract keys.
    const res = await apiAs(invitee).get(`/api/teams/invites/${token}`);
    expect(res.status(), await res.text()).toBe(200);
    const body = await res.json();
    expect(Object.keys(body).sort()).toEqual(
      ['email', 'expires_at', 'inviterName', 'inviter_email', 'teamName', 'team_id'].sort(),
    );
    expect(body).toMatchObject({
      team_id: team.id,
      teamName: team.name,
      inviterName: inviter.name,
      inviter_email: inviter.email,
      email: invitee.email,
    });
    expect(Date.parse(body.expires_at)).toBe(expires.getTime());

    // Then the page the invite email links to.
    const page = await pageAs(invitee);
    await page.goto(`/app/team/invite/${token}`);
    await assertNotDemo(page);
    await expect(page.getByText(team.name)).toBeVisible();
    const inviterLine = page.getByTestId('invite-inviter');
    await expect(inviterLine).toContainText(inviter.name);
    await expect(inviterLine).toContainText(inviter.email);
    const label = expires.toLocaleDateString('en-US', { month: 'long', day: 'numeric', year: 'numeric' });
    await expect(page.getByTestId('invite-expiry')).toContainText(`expires on ${label}`);
    await expect(page.locator('body')).not.toContainText('undefined');
  });
});

test.describe('PAC-34 manager analytics', () => {
  test("a member with no measured week shows 'no data', not 0 minutes", async ({ db, apiAs, pageAs, assertNotDemo }) => {
    const run = `${Date.now()}${Math.floor(Math.random() * 1000)}`;
    const manager = await freshUser(db, 'manager', run);
    const member = await freshUser(db, 'member', run);
    const team = await teamOwnedBy(db, manager, run, [member]);

    // The HTTP boundary: the member is a Paceday user with nothing measured.
    const res = await apiAs(manager).get('/api/manager/analytics', { team_id: team.id });
    expect(res.status(), await res.text()).toBe(200);
    const body = (await res.json()) as {
      members: Array<{ email: string; is_paceday_user: boolean; data_available: boolean; weeks: Array<{ data_available: boolean }> }>;
    };
    const wire = body.members.find((m) => m.email === member.email);
    expect(wire, JSON.stringify(body)).toBeDefined();
    expect(wire!.is_paceday_user).toBe(true);
    expect(wire!.data_available).toBe(false);
    expect(wire!.weeks).toHaveLength(12);
    expect(wire!.weeks.every((w) => !w.data_available)).toBe(true);

    // The panel the manager reads.
    const page = await pageAs(manager, { isManager: true });
    await page.goto('/app/team');
    await assertNotDemo(page);
    await page.getByRole('button', { name: 'Analytics', exact: true }).click();
    await page.getByRole('button', { name: 'Members', exact: true }).click();
    await page.getByRole('button', { name: new RegExp(member.name) }).click();
    await expect(page.getByTestId('analytics-member-no-data')).toBeVisible();
    await expect(page.getByTestId('analytics-member-no-data')).toContainText(/no data/i);
    await expect(page.locator('[title*="Focus 0m"]')).toHaveCount(0);
  });
});
