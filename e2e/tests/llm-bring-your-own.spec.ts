/**
 * NEW-llm-bring-your-own, Part 1 (docs/specs/NEW-llm-bring-your-own.md):
 * Paceday runs AI only on a key the user brings; with none, AI features are
 * off and say so.
 *
 * The compose stack configures no LLM key anywhere and approves no extra
 * OpenAI/Anthropic host. The public OpenAI host is approved by the product but
 * unreachable from the stack, so a configured OpenAI probe reaches the
 * provider call and fails there (502), which is how these journeys tell "a
 * request was attempted" from "refused before sending" (422) and "not
 * configured" (409).
 */

import { randomUUID } from 'node:crypto';

import { test, expect } from '../fixtures';
import { storageStateFor, type SessionUser } from '../auth';
import { query, queryOne } from '../db';

async function freshUser(label: string): Promise<SessionUser> {
  const id = randomUUID();
  const user = { id, email: `e2e.byok.${label}.${id.slice(0, 8)}@paceday.test`, name: `E2E BYOK ${label}` };
  await query('INSERT INTO users (id, email, name, created_at) VALUES ($1, $2, $3, NOW())', [user.id, user.email, user.name]);
  return user;
}

const OLLAMA_ORIGIN = 'http://ollama.internal:11434';

test.describe('NEW-llm-bring-your-own — AI runs only on a key the user brings', () => {
  test('AC-34 / AC-21: a fresh user has AI off, and the dashboard says so instead of an error', async ({ browser, apiAs }) => {
    const user = await freshUser('fresh');
    const api = apiAs(user);

    const settings = await (await api.get('/api/settings')).json();
    expect(settings).toMatchObject({
      llmProvider: 'organization',
      llmApiKeySet: false,
      aiStatus: { configured: false, notConfigured: { reason: 'nothing_configured' } },
    });
    expect(settings).not.toHaveProperty('ollamaBaseUrl');
    expect(settings).not.toHaveProperty('awsProfile');

    const parse = await api.post('/api/nlp/parse', { text: 'meet alice tomorrow' });
    expect(parse.status(), await parse.text()).toBe(200);
    const parsed = await parse.json();
    expect(parsed).toMatchObject({ intent: 'unknown', not_configured: { reason: 'nothing_configured', enablers: ['user'] } });
    expect(parsed).not.toHaveProperty('range_start');

    const context = await browser.newContext({ storageState: storageStateFor(user) });
    const page = await context.newPage();
    try {
      await page.goto('/app');
      await page.getByLabel('Describe what to schedule').fill('meet alice tomorrow');
      await page.getByRole('button', { name: /^send$/i }).click();
      const notice = page.getByTestId('ai-off-notice');
      await expect(notice).toBeVisible();
      await expect(notice).toContainText('AI features are off');
      await expect(notice).toContainText('no AI provider is set up');
      await expect(notice).toContainText('You can turn them on in Settings');
      await expect(notice).not.toContainText('admin'); // no organisation for this user
      await notice.getByRole('link', { name: /open ai settings/i }).click();
      await expect(page).toHaveURL(/\/settings#ai$/);
      await expect(page.getByTestId('ai-off-notice')).toBeVisible();
    } finally {
      await context.close();
    }
  });

  test('AC-21: Settings offers only off and the five key-based providers, with no removed field', async ({ browser, apiAs }) => {
    const user = await freshUser('list');
    expect((await apiAs(user).patch('/api/settings', { llmProvider: 'openai', llmBaseUrl: '' })).status()).toBe(200);

    const context = await browser.newContext({ storageState: storageStateFor(user) });
    const page = await context.newPage();
    try {
      await page.goto('/settings');
      const select = page.getByLabel('AI provider');
      await expect(select).toHaveValue('openai');
      const options = await select.locator('option:not([disabled])').evaluateAll((els) => els.map((el) => (el as HTMLOptionElement).value));
      expect(options).toEqual(['', 'openai', 'anthropic', 'azure_openai', 'bedrock', 'vertex']);
      await expect(page.getByText(/ollama/i)).toHaveCount(0);
      for (const label of ['AWS Profile', 'GCP Project', 'GCP Location']) {
        await expect(page.getByLabel(label)).toHaveCount(0);
      }
      await expect(page.getByRole('textbox', { name: /^Base URL/ })).toHaveValue('');
      await expect(page.getByRole('textbox', { name: /^API key/ })).toBeVisible();

      await select.selectOption('bedrock');
      await expect(page.getByRole('textbox', { name: /^API key/ })).toBeVisible();
      await expect(page.getByLabel('Bedrock model ID')).toBeVisible();
      await expect(page.getByRole('textbox', { name: /^Base URL/ })).toHaveCount(0);
    } finally {
      await context.close();
    }
  });

  test('AC-37: testing an unsaved provider never uses the saved provider\'s key', async ({ browser, apiAs }) => {
    const user = await freshUser('probe');
    const key = `sk-e2e-${randomUUID()}`;
    const api = apiAs(user);
    expect((await api.patch('/api/settings', { llmProvider: 'openai', llmApiKey: key })).status()).toBe(200);

    const probe = await api.post('/api/llm/test', { llmProvider: 'anthropic' });
    expect(probe.status(), await probe.text()).toBe(409);
    const body = await probe.json();
    expect(body.notConfigured).toMatchObject({ reason: 'needs_key', provider: 'anthropic' });
    expect(body.error).not.toContain('AI features are off');
    expect(JSON.stringify(body)).not.toContain(key);

    const context = await browser.newContext({ storageState: storageStateFor(user) });
    const page = await context.newPage();
    try {
      await page.goto('/settings');
      await page.getByLabel('AI provider').selectOption('anthropic');
      await page.getByRole('button', { name: /test connection/i }).click();
      await expect(page.getByText('Anthropic needs an API key. Enter one to test it.')).toBeVisible();
    } finally {
      await context.close();
    }
    // Nothing was saved by the probe.
    expect(await (await api.get('/api/settings')).json()).toMatchObject({ llmProvider: 'openai', llmApiKeySet: true });
  });

  test('AC-9: an account stored on Ollama reads valid and writes back unchanged', async ({ apiAs }) => {
    const user = await freshUser('writeback');
    const api = apiAs(user);
    expect((await api.get('/api/settings')).status()).toBe(200);
    await query("UPDATE settings SET llm_provider = 'ollama', llm_api_key = 'sk-e2e-legacy', ollama_base_url = $1 WHERE user_id = $2", [OLLAMA_ORIGIN, user.id]);

    const doc = await (await api.get('/api/settings')).json();
    expect(doc).toMatchObject({
      llmProvider: 'unsupported',
      llmApiKeySet: false,
      llmUnboundApiKeySet: true,
      aiStatus: { configured: false, notConfigured: { reason: 'provider_removed' } },
    });
    expect(JSON.stringify(doc)).not.toContain('sk-e2e-legacy');

    const back = await api.patch('/api/settings', doc);
    expect(back.status(), await back.text()).toBe(200);
    const row = await queryOne<{ llm_provider: string; llm_api_key: string; ollama_base_url: string }>(
      'SELECT llm_provider, llm_api_key, ollama_base_url FROM settings WHERE user_id = $1',
      [user.id],
    );
    expect(row).toEqual({ llm_provider: 'ollama', llm_api_key: 'sk-e2e-legacy', ollama_base_url: OLLAMA_ORIGIN });
  });

  for (const clear of [true, false]) {
    test(`AC-39: a former Ollama user's base URL is shown for OpenAI and ${clear ? 'can be cleared' : 'is refused when kept'}`, async ({ browser, apiAs }) => {
      const user = await freshUser(clear ? 'clear' : 'keep');
      const api = apiAs(user);
      expect((await api.get('/api/settings')).status()).toBe(200);
      // The web's former Ollama field wrote the general base URL (spec §2.4).
      await query("UPDATE settings SET llm_provider = 'ollama', llm_base_url = $1 WHERE user_id = $2", [OLLAMA_ORIGIN, user.id]);

      const context = await browser.newContext({ storageState: storageStateFor(user) });
      const page = await context.newPage();
      try {
        await page.goto('/settings');
        await expect(page.getByTestId('ai-off-notice')).toContainText('Ollama is no longer supported');
        await page.getByLabel('AI provider').selectOption('openai');
        const baseUrl = page.getByRole('textbox', { name: /^Base URL/ });
        await expect(baseUrl).toHaveValue(OLLAMA_ORIGIN);
        if (clear) await baseUrl.fill('');
        await page.getByRole('textbox', { name: /^API key/ }).fill(`sk-e2e-${randomUUID()}`);
        await page.getByRole('button', { name: /save changes/i }).click();
        await expect(page.getByText(/settings saved/i)).toBeVisible();
      } finally {
        await context.close();
      }

      const doc = await (await api.get('/api/settings')).json();
      expect(doc).toMatchObject({
        llmProvider: 'openai',
        llmBaseUrl: clear ? '' : OLLAMA_ORIGIN,
        llmApiKeysSet: { openai: true },
        aiStatus: { configured: true, provider: 'openai' },
      });
      const probe = await api.post('/api/llm/test', {});
      if (clear) {
        // Reaches the provider call: api.openai.com is approved but unreachable here.
        expect(probe.status(), await probe.text()).toBe(502);
      } else {
        expect(probe.status(), await probe.text()).toBe(422);
        expect(await probe.json()).toMatchObject({ reason: 'destination_not_approved', property: 'llmBaseUrl' });
      }
    });
  }
});
