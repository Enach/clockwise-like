/**
 * PAC-74 (docs/specs/PAC-74.md): the "API key" field of the Settings page.
 * An emptied field never deletes the saved key; only "Remove saved key" does.
 *
 * AC-2: type in the key field, erase it, change another setting, save: the
 * PATCH carries no key, and the OpenAI key is still on file (flag, AI status
 * and the stored column, byte-identical: PAC-84 D-7), so a probe of the
 * stored key is attempted. AC-5 (e2e half): "Remove saved key", save: the
 * PATCH asks for the removal and the key is gone.
 *
 * The stack has no reachable LLM provider (llm-bring-your-own.spec.ts): an
 * OpenAI probe that uses a key reaches the provider call and fails there
 * (502), while a probe with no key is refused before sending (409
 * needs_key). "The test call uses the stored key" is therefore asserted as
 * 502, not 409; a 200 cannot be produced without a provider seam.
 */

import { randomUUID } from 'node:crypto';

import type { Page, Request } from '@playwright/test';

import { test, expect } from '../fixtures';
import { storageStateFor, type SessionUser } from '../auth';
import { query, queryOne } from '../db';

async function freshUser(label: string): Promise<SessionUser> {
  const id = randomUUID();
  const user = { id, email: `e2e.pac74.${label}.${id.slice(0, 8)}@paceday.test`, name: `E2E PAC-74 ${label}` };
  await query('INSERT INTO users (id, email, name, created_at) VALUES ($1, $2, $3, NOW())', [user.id, user.email, user.name]);
  return user;
}

async function storedOpenAIKey(userId: string): Promise<string | undefined> {
  const row = await queryOne<{ llm_api_key_openai: string }>('SELECT llm_api_key_openai FROM settings WHERE user_id = $1', [userId]);
  return row?.llm_api_key_openai;
}

function recordRequests(page: Page, method: string, path: string): Record<string, unknown>[] {
  const bodies: Record<string, unknown>[] = [];
  page.on('request', (req: Request) => {
    if (req.method() === method && new URL(req.url()).pathname.endsWith(path)) bodies.push(req.postDataJSON());
  });
  return bodies;
}

test.describe('PAC-74 — an emptied API key field never deletes the saved key', () => {
  test('AC-2: type, erase, change the model, save: the OpenAI key is still on file and is the one tested', async ({ browser, apiAs }) => {
    const user = await freshUser('erase');
    const api = apiAs(user);
    const key = `sk-e2e-${randomUUID()}`;
    expect((await api.patch('/api/settings', { llmProvider: 'openai', llmApiKey: key })).status()).toBe(200);
    const storedBefore = await storedOpenAIKey(user.id);
    expect(storedBefore).toBeTruthy();

    const context = await browser.newContext({ storageState: storageStateFor(user) });
    const page = await context.newPage();
    const patches = recordRequests(page, 'PATCH', '/api/settings');
    const probes = recordRequests(page, 'POST', '/api/llm/test');
    try {
      await page.goto('/settings');
      await expect(page.getByLabel('AI provider')).toHaveValue('openai');
      const field = page.getByRole('textbox', { name: /^API key/ });
      const remove = page.getByRole('button', { name: /remove saved key/i });
      await expect(field).toHaveAttribute('placeholder', 'A key is saved. Type to replace it');
      await expect(remove).toBeVisible();

      await field.pressSequentially('sk-typo');
      await expect(remove).toHaveCount(0);
      for (let i = 0; i < 'sk-typo'.length; i++) await field.press('Backspace');
      await expect(field).toHaveValue('');
      // AC-3, seen by a real user: the field is back to "a key is saved".
      await expect(field).toHaveAttribute('placeholder', 'A key is saved. Type to replace it');
      await expect(remove).toBeVisible();

      // AC-4: Test connection tests the stored key: it reaches the provider
      // call (502 here), never "needs an API key" (409).
      const probed = page.waitForResponse((r) => r.request().method() === 'POST' && new URL(r.url()).pathname.endsWith('/api/llm/test'));
      await page.getByRole('button', { name: /test connection/i }).click();
      expect((await probed).status()).toBe(502);
      await expect(page.getByText('OpenAI needs an API key. Enter one to test it.')).toHaveCount(0);

      await page.getByRole('textbox', { name: /^Model/ }).fill('gpt-4o');
      const saved = page.waitForResponse((r) => r.request().method() === 'PATCH' && new URL(r.url()).pathname.endsWith('/api/settings'));
      await page.getByRole('button', { name: /save changes/i }).click();
      expect((await saved).status()).toBe(200);
      await expect(page.getByText(/settings saved/i)).toBeVisible();
    } finally {
      await context.close();
    }

    expect(probes).toHaveLength(1);
    expect(probes[0]).toEqual({ llmProvider: 'openai', llmModel: '' });
    expect(patches).toHaveLength(1);
    expect(patches[0]).toEqual({ llmModel: 'gpt-4o' });
    expect(patches[0]).not.toHaveProperty('llmApiKey');

    expect(await (await api.get('/api/settings')).json()).toMatchObject({
      llmProvider: 'openai',
      llmModel: 'gpt-4o',
      llmApiKeySet: true,
      llmApiKeysSet: { openai: true },
      aiStatus: { configured: true, provider: 'openai' },
    });
    // The untouched key is written back byte-identical (PAC-84 D-7).
    expect(await storedOpenAIKey(user.id)).toBe(storedBefore);
    // A probe of the stored key is attempted (502: provider unreachable), not refused for a missing key.
    const probe = await api.post('/api/llm/test', {});
    expect(probe.status(), await probe.text()).toBe(502);
  });

  test('AC-5: Remove saved key, save: the OpenAI key is gone', async ({ browser, apiAs }) => {
    const user = await freshUser('remove');
    const api = apiAs(user);
    expect((await api.patch('/api/settings', { llmProvider: 'openai', llmApiKey: `sk-e2e-${randomUUID()}` })).status()).toBe(200);
    expect(await storedOpenAIKey(user.id)).toBeTruthy();

    const context = await browser.newContext({ storageState: storageStateFor(user) });
    const page = await context.newPage();
    const patches = recordRequests(page, 'PATCH', '/api/settings');
    try {
      await page.goto('/settings');
      await expect(page.getByLabel('AI provider')).toHaveValue('openai');
      await page.getByRole('button', { name: /remove saved key/i }).click();
      await expect(page.getByRole('textbox', { name: /^API key/ })).toHaveAttribute('placeholder', 'Paste an API key');
      const saved = page.waitForResponse((r) => r.request().method() === 'PATCH' && new URL(r.url()).pathname.endsWith('/api/settings'));
      await page.getByRole('button', { name: /save changes/i }).click();
      expect((await saved).status()).toBe(200);
      await expect(page.getByText(/settings saved/i)).toBeVisible();
      // After the save the page shows no key on file.
      await expect(page.getByRole('textbox', { name: /^API key/ })).toHaveAttribute('placeholder', 'Paste an API key');
      await expect(page.getByRole('button', { name: /remove saved key/i })).toHaveCount(0);
    } finally {
      await context.close();
    }

    expect(patches).toHaveLength(1);
    expect(patches[0]).toEqual({ llmApiKey: '' });
    expect(await (await api.get('/api/settings')).json()).toMatchObject({
      llmProvider: 'openai',
      llmApiKeySet: false,
      llmApiKeysSet: { openai: false },
      aiStatus: { configured: false, notConfigured: { reason: 'needs_key', provider: 'openai' } },
    });
    expect(await storedOpenAIKey(user.id)).toBeFalsy();
    const probe = await api.post('/api/llm/test', {});
    expect(probe.status(), await probe.text()).toBe(409);
    expect((await probe.json()).notConfigured).toMatchObject({ reason: 'needs_key', provider: 'openai' });
  });
});
