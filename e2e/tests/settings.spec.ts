/**
 * PAC-24: a user's settings are their own, the provider key is write-only, and
 * a save changes only what it sends.
 *
 * Every test creates its own users rather than editing the seeded ones: other
 * specs depend on the seeded users' working hours (booking windows, focus
 * weeks), and the suite runs in parallel.
 */

import { randomUUID } from 'node:crypto';

import { test, expect } from '../fixtures';
import { storageStateFor, type SessionUser } from '../auth';
import { query } from '../db';

async function freshUser(label: string): Promise<SessionUser> {
  const id = randomUUID();
  const user = { id, email: `e2e.settings.${label}.${id.slice(0, 8)}@paceday.test`, name: `E2E Settings ${label}` };
  await query('INSERT INTO users (id, email, name, created_at) VALUES ($1, $2, $3, NOW())', [user.id, user.email, user.name]);
  return user;
}

test.describe('PAC-24 — per-user settings', () => {
  test('AC-1/AC-2: two users, two documents; a new user reads the defaults', async ({ apiAs }) => {
    const alice = await freshUser('alice');
    const bob = await freshUser('bob');

    const first = await apiAs(bob).get('/api/settings');
    expect(first.status()).toBe(200);
    expect(await first.json()).toMatchObject({ workStart: '09:00', timezone: 'UTC', recapSendTime: '08:00', llmApiKeySet: false });

    const patched = await apiAs(alice).patch('/api/settings', { workStart: '07:00', timezone: 'Europe/Paris' });
    expect(patched.status(), await patched.text()).toBe(200);

    expect(await (await apiAs(alice).get('/api/settings')).json()).toMatchObject({ workStart: '07:00', timezone: 'Europe/Paris' });
    expect(await (await apiAs(bob).get('/api/settings')).json()).toMatchObject({ workStart: '09:00', timezone: 'UTC' });
  });

  test('AC-11/AC-12/AC-13: the key is never returned, its presence is, and a round trip keeps it', async ({ apiAs }) => {
    const user = await freshUser('key');
    const key = `sk-e2e-${randomUUID()}`;
    const api = apiAs(user);

    const saved = await api.patch('/api/settings', { llmProvider: 'openai', llmApiKey: key });
    expect(saved.status(), await saved.text()).toBe(200);
    for (const res of [saved, await api.get('/api/settings')]) {
      const text = await res.text();
      expect(text).not.toContain(key);
      expect(JSON.parse(text)).toMatchObject({ llmApiKeySet: true });
      expect(JSON.parse(text)).not.toHaveProperty('llmApiKey');
    }

    // Submit back exactly what was read: the key stays.
    const doc = await (await api.get('/api/settings')).json();
    expect((await api.patch('/api/settings', doc)).status()).toBe(200);
    expect(await (await api.get('/api/settings')).json()).toMatchObject({ llmApiKeySet: true });

    // An explicit empty value clears it.
    expect((await api.patch('/api/settings', { llmApiKey: '' })).status()).toBe(200);
    expect(await (await api.get('/api/settings')).json()).toMatchObject({ llmApiKeySet: false });
  });

  test('AC-6/AC-8: a PATCH changes only what it sends; null and unknown properties are refused', async ({ apiAs }) => {
    const user = await freshUser('merge');
    const api = apiAs(user);
    expect((await api.patch('/api/settings', { focusLabel: 'Deep', bufferBeforeMinutes: 10 })).status()).toBe(200);
    expect((await api.patch('/api/settings', { focusDailyTargetMinutes: 90 })).status()).toBe(200);
    expect(await (await api.get('/api/settings')).json()).toMatchObject({
      focusLabel: 'Deep',
      bufferBeforeMinutes: 10,
      focusDailyTargetMinutes: 90,
    });

    for (const body of [{ focusLabel: null }, { focus_label: 'snake' }, { notASetting: 1 }]) {
      const res = await api.patch('/api/settings', body);
      expect(res.status(), JSON.stringify(body)).toBe(400);
    }
    expect(await (await api.get('/api/settings')).json()).toMatchObject({ focusLabel: 'Deep' });
  });

  test('AC-20: the settings page shows the stored value and an edit survives a reload', async ({ browser, apiAs }) => {
    const user = await freshUser('ui');
    expect((await apiAs(user).patch('/api/settings', { focusLabel: 'Stored label' })).status()).toBe(200);

    const context = await browser.newContext({ storageState: storageStateFor(user) });
    const page = await context.newPage();
    try {
      await page.goto('/settings');
      const label = page.getByLabel('Label', { exact: true });
      await expect(label).toHaveValue('Stored label');

      await label.fill('Edited in the browser');
      await page.getByRole('button', { name: /save changes/i }).click();
      await expect(page.getByText(/settings saved/i)).toBeVisible();

      await page.reload();
      await expect(page.getByLabel('Label', { exact: true })).toHaveValue('Edited in the browser');
    } finally {
      await context.close();
    }
    expect(await (await apiAs(user).get('/api/settings')).json()).toMatchObject({ focusLabel: 'Edited in the browser' });
  });
});
