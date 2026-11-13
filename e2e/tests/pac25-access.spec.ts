/**
 * PAC-25 AC-6 and AC-14, through the full stack (nginx -> backend -> Postgres).
 *
 * AC-6: GET /api/scheduling-links/{id} (handlers_scheduling_links.go getLink)
 * is readable only by the link's owner and accepted co-hosts. Anyone else gets
 * the same 404 {"error":"not found"} as for an id that matches no link, with no
 * part of the link in the body (spec D-1, D-2).
 *
 * AC-14: DELETE /api/teams/{id}/members/{userId} (handlers_teams.go
 * removeMember) refuses, with 422, the removal that would leave a team with no
 * owner, so a team's only owner cannot remove themself (spec D-3..D-5).
 *
 * Each test owns its users, link and team (run-unique emails) and removes them
 * at the end, so it cannot contend with the seeded fixtures or a parallel run.
 */

import { test, expect, type Db } from '../fixtures';
import type { SessionUser } from '../auth';

async function freshUser(db: Db, label: string, run: string): Promise<SessionUser> {
  const email = `e2e.pac25.${label}.${run}@pac25.e2e.test`;
  const name = `E2E PAC25 ${label} ${run}`;
  const row = await db.queryOne<{ id: string }>(
    'INSERT INTO users (email, name) VALUES ($1, $2) RETURNING id',
    [email, name],
  );
  return { id: row!.id, email, name };
}

async function removeUsers(db: Db, users: SessionUser[]) {
  await db.query('DELETE FROM users WHERE id = ANY($1::uuid[])', [users.map((u) => u.id)]);
}

test.describe('PAC-25 access rules', () => {
  test('AC-6: a link is not readable by a user who does not host it', async ({ db, apiAs }) => {
    const run = `${Date.now()}${Math.floor(Math.random() * 1000)}`;
    const owner = await freshUser(db, 'owner', run);
    const stranger = await freshUser(db, 'stranger', run);
    const slug = `e2e-pac25-${run}`;
    try {
      const created = await apiAs(owner).post('/api/scheduling-links', {
        title: `E2E PAC25 Link ${run}`,
        slug,
        duration_options: [30],
        days_of_week: [1, 2, 3, 4, 5],
        window_start_time: '09:00',
        window_end_time: '10:00',
        min_notice_minutes: 0,
        usage_type: 'reusable',
      });
      expect(created.status()).toBe(201);
      const link = await created.json();

      // The owner still reads it, roster included.
      const own = await apiAs(owner).get(`/api/scheduling-links/${link.id}`);
      expect(own.status()).toBe(200);
      const ownBody = await own.json();
      expect(ownBody.id).toBe(link.id);
      expect(ownBody.is_owner).toBe(true);
      expect(ownBody.hosts.map((h: { email: string }) => h.email)).toEqual([owner.email]);

      // A stranger gets exactly the unknown-id answer.
      const unknown = await apiAs(stranger).get('/api/scheduling-links/00000000-0000-4000-8000-000000000000');
      expect(unknown.status()).toBe(404);
      const refused = await apiAs(stranger).get(`/api/scheduling-links/${link.id}`);
      expect(refused.status()).toBe(404);
      const refusedText = await refused.text();
      expect(JSON.parse(refusedText)).toEqual({ error: 'not found' });
      expect(refusedText).toBe(await unknown.text());
      for (const leak of [owner.email, slug, link.title, 'hosts']) {
        expect(refusedText).not.toContain(leak);
      }
    } finally {
      await db.query('DELETE FROM scheduling_links WHERE slug = $1', [slug]);
      await removeUsers(db, [owner, stranger]);
    }
  });

  test("AC-14: a team's only owner cannot remove themself", async ({ db, apiAs }) => {
    const run = `${Date.now()}${Math.floor(Math.random() * 1000)}`;
    const owner = await freshUser(db, 'team-owner', run);
    let teamId: string | undefined;
    try {
      const created = await apiAs(owner).post('/api/teams', { name: `E2E PAC25 Team ${run}` });
      expect(created.status()).toBe(201);
      teamId = (await created.json()).id as string;

      const removed = await apiAs(owner).delete(`/api/teams/${teamId}/members/${owner.id}`);
      expect(removed.status()).toBe(422);
      expect(await removed.json()).toEqual({ error: 'the team owner cannot be removed' });

      const read = await apiAs(owner).get(`/api/teams/${teamId}`);
      expect(read.status()).toBe(200);
      const detail = await read.json();
      expect(detail.members).toEqual([
        expect.objectContaining({ userId: owner.id, role: 'owner' }),
      ]);
    } finally {
      if (teamId) await db.query('DELETE FROM teams WHERE id = $1', [teamId]);
      await removeUsers(db, [owner]);
    }
  });
});
