/**
 * PAC-129: add a person to a team directly, without an invitation or a
 * Paceday account (spec docs/specs/PAC-129.md, contract
 * contracts/features/PAC-129.md).
 *
 *   AC-1, AC-3   the owner adds a person from the team page: added at once,
 *                labelled "Added (no Paceday access)", nothing sent, no
 *                invitation stored
 *   AC-9, AC-10  Find a time counts added people, read through the OWNER's
 *                credential only; an added account gets nothing back
 *   AC-15        removing an added person deletes the address and every
 *                invitation; the web says "removed" only on a removal
 *
 * The backend is real; the stand-in (e2e/fake-google) is the external
 * provider. Since PAC-129 it refuses more than 50 calendars per free/busy
 * request, as Google does (AC-14).
 *
 * Owner: CONNECTED.calendar, whose free/busy credential the stand-in accepts.
 * Every other person is run-unique, and every team is created and deleted by
 * its own journey. The journeys run serially: the team page shows the owner's
 * newest team, so two journeys of this file must not hold a team at once.
 *
 * The team page's tabs ("Protected hours", "Find a time") are plain buttons
 * in its tab bar (src/pages/Team.tsx TABS), not role="tab"; they are matched
 * as buttons by their exact name.
 *
 * Owner controls on the page (web factory/pac-129 @ 195a533, #180): the web
 * decides "me" from the signed-in session (/api/auth/me, lower-cased) and
 * shows owner controls to any owner of the team. Every journey that uses one
 * is signed in as OWNER, the owner of its team. "Add to team"
 * (team-add-person-open) and "Invite to Paceday" (team-invite-open) render
 * only for owners. The "Remove <name>" button exists only in the Manage
 * members sheet (opened from the Protected hours tab); it is labelled with the
 * display name (the name given, else the address; with the address in
 * parentheses only when two people share a name) and deletes at once, with no
 * confirmation. The Manager Team tab's own remove button (title "Remove",
 * AlertDialog) is never used here.
 * Find a time is asked for a Thursday thirteen weeks ahead, a day no other
 * journey seeds.
 *
 * Selectors are the web half's (smart-calendar-flow factory/pac-129, 195a533,
 * src/components/team/teamTexts.ts): team-add-person-open,
 * team-add-person-submit, team-person-<email>, team-person-added-label,
 * team-slot-counts; texts are contract §9's.
 */

import { test, expect, type Db } from '../fixtures';
import type { SessionUser } from '../auth';
import { fake } from '../fake';
import { CONNECTED } from '../seed/ids';
import { atUtc, isoDate, weekDay } from '../lib/dates';

test.describe.configure({ mode: 'serial' });

const OWNER = CONNECTED.calendar;
const DAY = () => weekDay(13, 3);

function runId(): string {
  return `${Date.now()}${Math.random().toString(36).slice(2, 6)}`;
}

async function freshUser(db: Db, label: string, run: string): Promise<SessionUser> {
  const email = `e2e.pac129.${label}.${run}@pac129.e2e.test`;
  const name = `PAC129 ${label} ${run}`;
  const row = await db.queryOne<{ id: string }>('INSERT INTO users (email, name) VALUES ($1, $2) RETURNING id', [email, name]);
  return { id: row!.id, email, name };
}

/** Team T owned by OWNER, with the given Paceday members. */
async function teamOf(db: Db, run: string, members: SessionUser[]): Promise<string> {
  const team = await db.queryOne<{ id: string }>('INSERT INTO teams (name, created_by) VALUES ($1, $2) RETURNING id', [
    `E2E PAC129 ${run}`,
    OWNER.id,
  ]);
  await db.query("INSERT INTO team_members (team_id, user_id, role) VALUES ($1, $2, 'owner')", [team!.id, OWNER.id]);
  for (const m of members) {
    await db.query("INSERT INTO team_members (team_id, user_id, role) VALUES ($1, $2, 'member')", [team!.id, m.id]);
  }
  return team!.id;
}

async function count(db: Db, sql: string, params: unknown[]): Promise<number> {
  const row = await db.queryOne<{ n: string }>(sql, params);
  return Number(row!.n);
}

test.describe('PAC-129 — people added to a team without Paceday access', () => {
  let teamId = '';
  test.afterEach(async ({ db }) => {
    if (teamId) await db.query('DELETE FROM teams WHERE id = $1', [teamId]);
    teamId = '';
  });

  test('AC-1, AC-3: the owner adds a person from the team page; nothing is sent', async ({ db, pageAs, assertNotDemo }) => {
    const run = runId();
    teamId = await teamOf(db, run, []);
    const pat = `e2e.pac129.pat.${run}@ext.e2e.test`;

    const page = await pageAs(OWNER);
    await page.goto('/app/team');
    await assertNotDemo(page);
    await page.getByRole('button', { name: 'Protected hours', exact: true }).click();
    await page.getByTestId('team-add-person-open').click();

    const dialog = page.getByRole('dialog', { name: 'Add to team without Paceday access' });
    await expect(dialog).toBeVisible();
    // The four required statements (spec §3.1), before submitting.
    await expect(dialog).toContainText('You are responsible for having a reason to add this person.');
    await expect(dialog).toContainText('Paceday sends them no email.');
    await expect(dialog).toContainText('Every member of this team will see their name and email.');
    await expect(dialog).toContainText(
      'Whenever any member uses Find a time, their free/busy is read through your calendar connection.',
    );
    await dialog.getByRole('combobox', { name: 'Email' }).fill(pat);

    const added = page.waitForResponse((r) => r.url().endsWith(`/api/teams/${teamId}/people`) && r.request().method() === 'POST');
    await page.getByTestId('team-add-person-submit').click();
    const res = await added;
    expect(res.status(), await res.text()).toBe(201);
    expect(await res.json()).toEqual({ email: pat, name: '', kind: 'added', userId: null, role: null, invitation: null });

    // "added", never "sent" or "invited"; the person is listed without a reload.
    await expect(page.getByText(`${pat} added.`)).toBeVisible();
    await expect(page.getByText(/invite sent|invited/i)).toHaveCount(0);
    // The add dialog closed on success; the sheet lists the person.
    await page.getByRole('button', { name: 'Manage members' }).click();
    const sheet = page.getByRole('dialog', { name: 'Manage members' });
    const row = sheet.getByTestId(`team-person-${pat}`);
    await expect(row).toBeVisible();
    await expect(row.getByTestId('team-person-added-label')).toHaveText('Added (no Paceday access)');

    // Persistence: one added entry, no invitation (so no email could be sent).
    expect(await count(db, 'SELECT count(*) AS n FROM team_added_members WHERE team_id = $1 AND email = $2', [teamId, pat])).toBe(1);
    expect(await count(db, 'SELECT count(*) AS n FROM team_invites WHERE team_id = $1', [teamId])).toBe(0);
  });

  test("AC-9, AC-10: Find a time counts added people through the owner's credential", async ({ db, apiAs, pageAs, assertNotDemo }) => {
    const run = runId();
    const day = DAY();
    const m = await freshUser(db, 'm', run); // a Paceday member with no calendar: unknown
    teamId = await teamOf(db, run, [m]);
    const x = `e2e.pac129.x.${run}@busy.e2e.test`; // busy 10:00-11:00 through the owner
    const y = `e2e.pac129.y.${run}@unseeded.e2e.test`; // the stand-in cannot see it: unknown
    const z = `e2e.pac129.z.${run}@gmail.com`; // personal domain: unknown, never asked
    const u = await freshUser(db, 'u', run); // an account holding an added address
    await fake.seedEvents(x, [{ summary: 'busy', start: { dateTime: atUtc(day, '10:00') }, end: { dateTime: atUtc(day, '11:00') } }]);
    // U's calendar exists at the stand-in (known), busy only the day before.
    const before = new Date(day.getTime() - 24 * 3600 * 1000);
    await fake.seedEvents(u.email, [{ summary: 'other day', start: { dateTime: atUtc(before, '10:00') }, end: { dateTime: atUtc(before, '11:00') } }]);

    const owner = apiAs(OWNER);
    for (const addr of [x, y, z, u.email]) {
      const res = await owner.post(`/api/teams/${teamId}/people`, { email: addr });
      expect(res.status(), await res.text()).toBe(201);
    }

    // AC-9: the added account gets nothing.
    const asU = apiAs(u);
    const teams = (await (await asU.get('/api/teams')).json()) as Array<{ id: string }>;
    expect(teams.map((t) => t.id)).not.toContain(teamId);
    for (const path of ['', '/availability?date=' + isoDate(day), '/analytics?date=' + isoDate(day)]) {
      expect((await asU.get(`/api/teams/${teamId}${path}`)).status()).toBe(403);
    }

    // AC-10 at the boundary, asked by the member: O and X and U known,
    // M, Y and Z unknown.
    const mark = await fake.mark();
    const res = await apiAs(m).get(`/api/teams/${teamId}/availability`, { date: isoDate(day), duration: 60 });
    expect(res.status(), await res.text()).toBe(200);
    const { slots } = (await res.json()) as {
      slots: Array<{ start: string; end: string; free_count: number; unknown_count: number; total_count: number }>;
    };
    expect(slots.length).toBeGreaterThan(0);
    const busyStart = Date.parse(atUtc(day, '10:00'));
    const busyEnd = Date.parse(atUtc(day, '11:00'));
    for (const s of slots) {
      expect(Date.parse(s.start) < busyEnd && Date.parse(s.end) > busyStart, `slot ${s.start} overlaps X's busy hour`).toBe(false);
      expect(Object.keys(s).sort()).toEqual(['end', 'free_count', 'quality_score', 'start', 'total_count', 'unknown_count']);
      expect(s).toMatchObject({ total_count: 6, unknown_count: 3, free_count: 3 });
    }

    // AC-29: the added people were read with the owner's credential, never
    // the member's (who has none) and never U's own; Z was never asked.
    const reads = (await fake.requests(mark)).filter((r) => r.operation === 'freebusy.query' && (r.freebusy_ids ?? []).includes(x));
    expect(reads.length).toBeGreaterThan(0);
    for (const r of reads) {
      expect(r.credential).toBe(OWNER.label);
      expect(r.freebusy_ids).not.toContain(z);
      expect(r.freebusy_ids!.length).toBeLessThanOrEqual(50);
    }

    // AC-10 on screen, for the member M (not an owner).
    const page = await pageAs(m);
    await page.goto('/app/team');
    await assertNotDemo(page);
    // Owner-only controls are not offered to a member.
    await page.getByRole('button', { name: 'Protected hours', exact: true }).click();
    await expect(page.getByRole('button', { name: 'Manage members' })).toBeVisible();
    await expect(page.getByTestId('team-add-person-open')).toHaveCount(0);
    await expect(page.getByTestId('team-invite-open')).toHaveCount(0);
    // Each slot shows the server's counts (SF-054). Opening the tab asks
    // nothing: availability is read only when "Find slots" is clicked, for
    // the Date and Duration chosen (web FindATimeTab at 195a533). The journey's
    // own day is chosen, so the counts are this team's: 3 of 6 free, 3 unknown.
    await page.getByRole('button', { name: 'Find a time', exact: true }).click();
    await page.getByLabel('Date', { exact: true }).fill(isoDate(day));
    const answered = page.waitForResponse(
      (r) =>
        r.request().method() === 'GET' &&
        r.url().includes(`/api/teams/${teamId}/availability`) &&
        new URL(r.url()).searchParams.get('date') === isoDate(day),
    );
    await page.getByRole('button', { name: 'Find slots', exact: true }).click();
    const shown = await answered;
    expect(shown.status(), await shown.text()).toBe(200);
    const shownSlots = ((await shown.json()) as { slots: Array<{ free_count: number; unknown_count: number; total_count: number }> }).slots;
    expect(shownSlots.length).toBeGreaterThan(0);
    expect(shownSlots[0]).toMatchObject({ free_count: 3, unknown_count: 3, total_count: 6 });
    await expect(page.getByTestId('team-slot-counts').first()).toHaveText('3 of 6 free · 3 unknown');
  });

  test('AC-15: removing an added person removes the address and its invitations', async ({ db, apiAs, pageAs, assertNotDemo }) => {
    const run = runId();
    teamId = await teamOf(db, run, []);
    const sam = `e2e.pac129.sam.${run}@ext.e2e.test`;
    const owner = apiAs(OWNER);
    const invited = await owner.post(`/api/teams/${teamId}/members/invite`, { email: sam, name: 'Sam' });
    expect(invited.status(), await invited.text()).toBe(201);
    const { outcome, invitation } = (await invited.json()) as { outcome: string; invitation: { token: string } };
    expect(outcome).toBe('added_and_invited');
    await db.query(
      "INSERT INTO team_invites (team_id, invitee_email, invited_by, token, status, expires_at) VALUES ($1, $2, $3, $4, 'expired', now() - interval '1 day')",
      [teamId, sam, OWNER.id, `e2e-pac129-old-${run}`],
    );

    const page = await pageAs(OWNER);
    await page.goto('/app/team');
    await assertNotDemo(page);
    await page.getByRole('button', { name: 'Protected hours', exact: true }).click();
    await page.getByRole('button', { name: 'Manage members' }).click();
    const sheet = page.getByRole('dialog', { name: 'Manage members' });
    const row = sheet.getByTestId(`team-person-${sam}`);
    await expect(row.getByTestId('team-person-added-label')).toBeVisible();

    // Labelled with the display name ("Sam", given with the invitation); it
    // deletes at once, with no confirmation dialog.
    const removed = page.waitForResponse((r) => r.url().includes(`/api/teams/${teamId}/people/`) && r.request().method() === 'DELETE');
    await sheet.getByRole('button', { name: 'Remove Sam', exact: true }).click();
    expect((await removed).status()).toBe(204);
    await expect(page.getByRole('alertdialog')).toHaveCount(0);
    await expect(page.getByText('Sam removed.')).toBeVisible();
    await expect(sheet.getByTestId(`team-person-${sam}`)).toHaveCount(0);

    // No row of T holds the address; the old link no longer works.
    expect(await count(db, 'SELECT count(*) AS n FROM team_added_members WHERE team_id = $1', [teamId])).toBe(0);
    expect(await count(db, 'SELECT count(*) AS n FROM team_invites WHERE team_id = $1', [teamId])).toBe(0);
    expect((await owner.get(`/api/teams/invites/${invitation.token}`)).status()).toBe(404);

    // A second removal is the 404, never a success (SF-089).
    const again = await owner.delete(`/api/teams/${teamId}/people/${encodeURIComponent(sam)}`);
    expect(again.status()).toBe(404);
    expect(await again.json()).toEqual({ error: 'person not found' });
  });
});
