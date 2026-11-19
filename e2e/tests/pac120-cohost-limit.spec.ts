/**
 * PAC-120 (contracts/features/PAC-120.md §7.2, spec docs/specs/PAC-120.md
 * AC-18): a link has at most 5 co-hosts on the single invitation too. Full
 * stack: nginx -> backend -> Postgres; nothing is mocked or routed.
 *
 * The journey owns its users (run-unique, domain-confirmed so they can be
 * invited: PAC-71) and its link, and removes them at the end, so it runs in
 * any order and in parallel with the other journeys. Everything goes through
 * the API under /api/ (nginx); the database is read only to assert the rows.
 */

import { test, expect, type Db } from '../fixtures';
import type { SessionUser } from '../auth';

const LIMIT_BODY = {
  error: 'this link has reached its limit of 5 co-hosts; remove a co-host before inviting another',
};

function runId(): string {
  return `${Date.now()}${Math.floor(Math.random() * 100000)}`;
}

/** A fresh account holding its address domain-confirmed (PAC-71 grant rule). */
async function freshUser(db: Db, label: string, run: string): Promise<SessionUser> {
  const email = `e2e.pac120.${label}.${run}@pac120.e2e.test`;
  const name = `E2E PAC120 ${label} ${run}`;
  const row = await db.queryOne<{ id: string }>(
    `INSERT INTO users (email, name, address_standing) VALUES ($1, $2, 'domain_confirmed') RETURNING id`,
    [email, name],
  );
  await db.query(
    `INSERT INTO user_identities (user_id, provider, tenant, subject, evidence, bound_by)
     VALUES ($1, 'google', '', $2, 'authoritative', 'created')`,
    [row!.id, `pac120-${row!.id}`],
  );
  return { id: row!.id, email, name };
}

async function cleanup(db: Db, users: SessionUser[]) {
  const ids = users.map((u) => u.id);
  await db.query('DELETE FROM scheduling_links WHERE owner_user_id = ANY($1::uuid[])', [ids]);
  await db.query('DELETE FROM users WHERE id = ANY($1::uuid[])', [ids]);
}

test.describe('PAC-120 the single invitation stops at 5 co-hosts', () => {
  test('AC-18: a sixth invitation is the 422 and the 5 co-hosts are unchanged', async ({ db, apiAs }) => {
    const run = runId();
    const owner = await freshUser(db, 'ac18-owner', run);
    const hosts: SessionUser[] = [];
    for (let i = 0; i < 6; i++) hosts.push(await freshUser(db, `ac18-h${i}`, run));
    try {
      const api = apiAs(owner);
      const created = await api.post('/api/scheduling-links', {
        title: `E2E PAC120 AC-18 ${run}`,
        duration_options: [30],
        days_of_week: [1, 2, 3, 4, 5],
        window_start_time: '09:00',
        window_end_time: '17:00',
        min_notice_minutes: 0,
        usage_type: 'reusable',
      });
      expect(created.status(), await created.text()).toBe(201);
      const { id } = (await created.json()) as { id: string };

      for (const h of hosts.slice(0, 5)) {
        const res = await api.post(`/api/scheduling-links/${id}/hosts`, { email: h.email });
        expect(res.status(), await res.text()).toBe(201);
      }

      const sixth = hosts[5]!;
      const refused = await api.post(`/api/scheduling-links/${id}/hosts`, { email: sixth.email });
      expect(refused.status(), await refused.text()).toBe(422);
      expect(refused.headers()['content-type']).toContain('application/json');
      expect(await refused.json()).toEqual(LIMIT_BODY);

      // The strict single-link read still lists exactly the same 5 co-hosts.
      const read = await api.get(`/api/scheduling-links/${id}`);
      expect(read.status(), await read.text()).toBe(200);
      const body = (await read.json()) as { hosts: Array<{ email: string; status: string; is_owner: boolean }> };
      const coHosts = body.hosts.filter((h) => !h.is_owner).map((h) => h.email);
      expect(coHosts.sort()).toEqual(hosts.slice(0, 5).map((h) => h.email).sort());

      // The rows: the owner plus the 5, and nothing for the sixth.
      // PAC-149: a pending invitation holds the address, not the account.
      const rows = await db.query<{ k: string; status: string }>(
        'SELECT COALESCE(user_id::text, lower(invitee_email)) AS k, status FROM scheduling_link_hosts WHERE link_id = $1',
        [id],
      );
      const byEmail = Object.fromEntries([owner, sixth, ...hosts].map((u) => [u.email.toLowerCase(), u.id]));
      const byUser = Object.fromEntries(rows.map((r) => [byEmail[r.k] ?? r.k, r.status]));
      expect(Object.keys(byUser).sort()).toEqual([owner.id, ...hosts.slice(0, 5).map((h) => h.id)].sort());
      expect(byUser[sixth.id]).toBeUndefined();
      for (const h of hosts.slice(0, 5)) expect(byUser[h.id]).toBe('pending');
    } finally {
      await cleanup(db, [owner, ...hosts]);
    }
  });
});
