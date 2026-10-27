/**
 * PAC-23: the activity log belongs to the person it is about.
 *
 * Covers AC-1, AC-3, AC-8 and AC-13 end to end, and AC-10's server half. Entries
 * are written straight into audit_log for fresh users: driving the six
 * recordable actions through the product (AC-5) needs a calendar and an LLM for
 * most of them, so AC-5 is proven by the backend's writer tests instead
 * (docs/factory/PAC-23-plan.md §4).
 *
 * The pre-migration row is written the way history arrives after migration
 * 026: the subject constraint is dropped, the row inserted, and the constraint
 * re-added NOT VALID, in one statement. The test deletes that row again.
 */

import { randomUUID } from 'node:crypto';

import { test, expect } from '../fixtures';
import { storageStateFor, type SessionUser } from '../auth';
import { exec, query } from '../db';

async function freshUser(label: string): Promise<SessionUser> {
  const id = randomUUID();
  const user = { id, email: `e2e.audit.${label}.${id.slice(0, 8)}@paceday.test`, name: `E2E Audit ${label}` };
  await query('INSERT INTO users (id, email, name, created_at) VALUES ($1, $2, $3, NOW())', [user.id, user.email, user.name]);
  return user;
}

async function record(user: SessionUser, action: string, details: string, minutesAgo: number): Promise<void> {
  await query(
    `INSERT INTO audit_log (user_id, action, details, created_at) VALUES ($1, $2, $3, NOW() - make_interval(mins => $4::int))`,
    [user.id, action, details, minutesAgo],
  );
}

test.describe('PAC-23 — the activity log is the reader\'s own', () => {
  test('AC-1/AC-3/AC-8/AC-13: the page shows my entries, newest first, and nobody else\'s', async ({ browser, apiAs }) => {
    const tag = randomUUID().slice(0, 8);
    const me = await freshUser('me');
    const colleague = await freshUser('colleague');
    const title = `E2E 1:1 re: PIP ${tag}`;
    const prompt = `E2E call with the recruiter ${tag}`;
    const history = `E2E pre-migration ${tag}`;

    await record(me, 'focus_created', `{"week_start":"2026-01-05","marker":"E2E mine older ${tag}"}`, 10);
    await record(me, 'meeting_scheduled', `{"event_id":"e2e-${tag}","title":"E2E mine newer ${tag}"}`, 1);
    await record(colleague, 'meeting_scheduled', `{"event_id":"e2e-c-${tag}","title":"${title}"}`, 2);
    await record(colleague, 'nlp_parsed', `{"text":"${prompt}","intent":"schedule_meeting"}`, 3);
    // One DO block is one statement: it commits whole or fails whole, and never
    // leaves the pooled connection inside an open transaction.
    await exec(`DO $$ BEGIN
      ALTER TABLE audit_log DROP CONSTRAINT audit_log_user_id_present;
      INSERT INTO audit_log (action, details, created_at) VALUES ('nlp_parsed', '${history}', NOW());
      ALTER TABLE audit_log ADD CONSTRAINT audit_log_user_id_present CHECK (user_id IS NOT NULL) NOT VALID;
      END $$;`);

    try {
      // The HTTP boundary, with the parameters a caller might try.
      for (const limit of [undefined, 1, 500, 10000]) {
        const res = await apiAs(me).get('/api/audit', limit === undefined ? undefined : { limit });
        expect(res.status()).toBe(200);
        const text = await res.text();
        expect(text).not.toContain(title);
        expect(text).not.toContain(prompt);
        expect(text).not.toContain(history);
      }
      const mine = await (await apiAs(me).get('/api/audit')).json();
      expect(mine.map((e: { action: string }) => e.action)).toEqual(['meeting_scheduled', 'focus_created']);
      const theirs = await (await apiAs(colleague).get('/api/audit')).json();
      expect(theirs.map((e: { action: string }) => e.action)).toEqual(['meeting_scheduled', 'nlp_parsed']);

      // The page, as the shipped client renders it.
      const context = await browser.newContext({ storageState: storageStateFor(me) });
      const page = await context.newPage();
      try {
        const audit = page.waitForResponse((r) => new URL(r.url()).pathname === '/api/audit');
        await page.goto('/settings/audit');
        const res = await audit;
        expect(new URL(res.url()).searchParams.get('limit')).toBe('50');
        const items = page.locator('main ul > li');
        await expect(items).toHaveCount(2);
        await expect(items.nth(0)).toContainText('meeting_scheduled');
        await expect(items.nth(0)).toContainText(`E2E mine newer ${tag}`);
        await expect(items.nth(1)).toContainText('focus_created');
        await expect(items.nth(1)).toContainText(`E2E mine older ${tag}`);
        const body = await page.locator('main').innerText();
        expect(body).not.toContain(title);
        expect(body).not.toContain(prompt);
        expect(body).not.toContain(history);
      } finally {
        await context.close();
      }
    } finally {
      await query('DELETE FROM audit_log WHERE user_id IS NULL AND details = $1', [history]);
    }
  });

  test('AC-2: a user with no activity gets [] and the empty state', async ({ browser, apiAs }) => {
    const quiet = await freshUser('quiet');
    const busy = await freshUser('busy');
    await record(busy, 'focus_created', '{}', 1);

    const res = await apiAs(quiet).get('/api/audit', { limit: 50 });
    expect(res.status()).toBe(200);
    expect(await res.json()).toEqual([]);

    const context = await browser.newContext({ storageState: storageStateFor(quiet) });
    const page = await context.newPage();
    try {
      await page.goto('/settings/audit');
      await expect(page.getByText('No activity yet')).toBeVisible();
    } finally {
      await context.close();
    }
  });

  test('AC-10/AC-11: no limit means 50; above the maximum means 500', async ({ apiAs }) => {
    const user = await freshUser('limits');
    await query(
      `INSERT INTO audit_log (user_id, action, details, created_at)
         SELECT $1, 'focus_created', '{}', NOW() - make_interval(secs => g) FROM generate_series(1, 520) g`,
      [user.id],
    );
    expect(await (await apiAs(user).get('/api/audit')).json()).toHaveLength(50);
    expect(await (await apiAs(user).get('/api/audit', { limit: 10000 })).json()).toHaveLength(500);
  });

  test('AC-12: an unauthenticated caller is refused', async ({ api }) => {
    const res = await api.get('/api/audit');
    expect(res.status()).toBe(401);
  });
});
