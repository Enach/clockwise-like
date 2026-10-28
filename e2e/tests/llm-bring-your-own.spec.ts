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
      await expect(notice).toContainText('No AI provider is set up');
      await expect(notice).toContainText('You can turn them on in Settings');
      await expect(notice).not.toContainText('admin');
      // The heading says "AI features are off"; the reason line does not repeat it.
      expect((await notice.textContent())?.match(/AI features are off/g)).toHaveLength(1);
      await notice.getByRole('link', { name: /open ai settings/i }).click();
      await expect(page).toHaveURL(/\/settings#ai$/);
      await expect(page.getByTestId('ai-off-notice')).toBeVisible();

      // AC-21: this user has no organisation, though the stored choice is
      // "organization" (every new account on a generic domain). The select
      // shows that value under a neutral label; nothing on the AI section
      // offers or mentions an organisation.
      const select = page.getByLabel('AI provider');
      await expect(select).toHaveValue('organization');
      await expect(select.locator('option:checked')).toHaveText('No provider chosen');
      const enabled = await select.locator('option:not([disabled])').evaluateAll((els) => els.map((el) => (el as HTMLOptionElement).value));
      expect(enabled).toEqual(['', 'openai', 'anthropic', 'azure_openai', 'bedrock', 'vertex']);
      const section = page.locator('#ai');
      await expect(section).toBeVisible();
      await expect(section).not.toContainText(/organi[sz]ation/i);
      const optionTexts = await select.locator('option').allTextContents();
      for (const text of optionTexts) expect(text).not.toMatch(/organi[sz]ation/i);
    } finally {
      await context.close();
    }
  });

  // Amended by PAC-73 Part 2 (spec docs/specs/NEW-llm-bring-your-own-part2.md
  // §2.4; P2-AC-14, P2-AC-31, P2-AC-35; manifest consumersAffected), not
  // deleted. Part 1 sent an organisation member to no admin because no admin
  // could act; with Part 2 an organisation with no AI configuration names its
  // administrator (row 6: enablers [user, organization_admin]), and a member
  // stored as "follow my organisation" sees "Use my organisation's AI"
  // selected and enabled. A user with no organisation is unchanged (the test
  // above).
  test('AC-21 / P2-AC-14, P2-AC-31, P2-AC-35: an organisation member with no organisation AI is told the admin can set it up', async ({ browser, apiAs }) => {
    const user = await freshUser('orgmember');
    const domain = `byok-${user.id.slice(0, 8)}.paceday.test`;
    const org = await queryOne<{ id: string }>('INSERT INTO organizations (name, domain) VALUES ($1, $1) RETURNING id', [domain]);
    await query('UPDATE users SET org_id = $1 WHERE id = $2', [org!.id, user.id]);
    const api = apiAs(user);
    const both = ['organization_admin', 'user'];

    const settings = await (await api.get('/api/settings')).json();
    expect(settings).toMatchObject({
      llmProvider: 'organization',
      aiStatus: { configured: false, inOrganization: true, notConfigured: { reason: 'nothing_configured' } },
    });
    expect([...settings.aiStatus.notConfigured.enablers].sort()).toEqual(both);
    expect(settings.aiStatus.notConfigured.message).toMatch(/administrator of your organisation/);
    const parsed = await (await api.post('/api/nlp/parse', { text: 'meet alice tomorrow' })).json();
    expect(parsed.not_configured.reason).toBe('nothing_configured');
    expect([...parsed.not_configured.enablers].sort()).toEqual(both);
    const probe = await api.post('/api/llm/test', {});
    expect(probe.status(), await probe.text()).toBe(409);
    const probed = (await probe.json()).notConfigured;
    expect(probed.reason).toBe('nothing_configured');
    expect([...probed.enablers].sort()).toEqual(both);

    const context = await browser.newContext({ storageState: storageStateFor(user) });
    const page = await context.newPage();
    try {
      // P2-AC-35: the dashboard says so.
      await page.goto('/app');
      await page.getByLabel('Describe what to schedule').fill('meet alice tomorrow');
      await page.getByRole('button', { name: /^send$/i }).click();
      const barNotice = page.getByTestId('ai-off-notice');
      await expect(barNotice).toBeVisible();
      await expect(barNotice).toContainText('an administrator of your organisation can set up its AI');

      await page.goto('/settings');
      const notice = page.getByTestId('ai-off-notice');
      await expect(notice).toContainText('No AI provider is set up');
      await expect(notice).toContainText('You can turn them on in Settings, or an administrator of your organisation can set up its AI.');
      const select = page.getByLabel('AI provider');
      await expect(select).toHaveValue('organization');
      await expect(select.locator('option:checked')).toHaveText("Use my organisation's AI");
      const enabled = await select.locator('option:not([disabled])').evaluateAll((els) => els.map((el) => (el as HTMLOptionElement).value));
      expect(enabled).toEqual(['', 'organization', 'openai', 'anthropic', 'azure_openai', 'bedrock', 'vertex']);
      // The organisation AI section, read by every member: nothing set up yet.
      await expect(page.getByTestId('org-ai-section')).toContainText('Not set up');
      await expect(page.getByTestId('org-ai-section')).toContainText('An administrator of your organisation can change this.');
    } finally {
      await context.close();
    }
  });

  test('Settings: switching provider resets the model, and a removed unsaved key is tested as removed', async ({ browser, apiAs }) => {
    const user = await freshUser('switch');
    const key = `sk-e2e-${randomUUID()}`;
    const api = apiAs(user);
    expect((await api.patch('/api/settings', { llmProvider: 'anthropic', llmModel: 'claude-haiku-4-5-20251001', llmApiKey: key })).status()).toBe(200);

    const context = await browser.newContext({ storageState: storageStateFor(user) });
    const page = await context.newPage();
    try {
      await page.goto('/settings');
      const select = page.getByLabel('AI provider');
      const model = page.getByRole('textbox', { name: /^Model/ });
      await expect(model).toHaveValue('claude-haiku-4-5-20251001');
      await select.selectOption('openai');
      await expect(model).toHaveValue('');
      await select.selectOption('anthropic');
      await expect(model).toHaveValue('claude-haiku-4-5-20251001');

      await page.getByRole('button', { name: /remove saved key/i }).click();
      await page.getByRole('button', { name: /test connection/i }).click();
      await expect(page.getByText('Anthropic needs an API key. Enter one to test it.')).toBeVisible();
    } finally {
      await context.close();
    }
    // Nothing was saved: the key is still on file.
    expect(await (await api.get('/api/settings')).json()).toMatchObject({ llmProvider: 'anthropic', llmApiKeysSet: { anthropic: true } });
  });

  // Contract challenge rev. 2.1, W-1: a key is bound to the provider it was
  // removed or typed for. Removing the OpenAI key and then switching to
  // Anthropic must not send an empty key for Anthropic (which would delete the
  // Anthropic key and keep the OpenAI one), and a key typed for OpenAI must
  // not be saved for Anthropic.
  test('W-1: remove the OpenAI key, switch to Anthropic, save: the Anthropic key survives', async ({ browser, apiAs }) => {
    const user = await freshUser('w1');
    const api = apiAs(user);
    expect((await api.patch('/api/settings', { llmProvider: 'anthropic', llmApiKey: `sk-e2e-${randomUUID()}` })).status()).toBe(200);
    expect((await api.patch('/api/settings', { llmProvider: 'openai', llmApiKey: `sk-e2e-${randomUUID()}` })).status()).toBe(200);
    expect(await (await api.get('/api/settings')).json()).toMatchObject({
      llmProvider: 'openai',
      llmApiKeysSet: { openai: true, anthropic: true },
    });

    const context = await browser.newContext({ storageState: storageStateFor(user) });
    const page = await context.newPage();
    const patches: Record<string, unknown>[] = [];
    page.on('request', (req) => {
      if (req.method() === 'PATCH' && new URL(req.url()).pathname.endsWith('/api/settings')) patches.push(req.postDataJSON());
    });
    try {
      await page.goto('/settings');
      const select = page.getByLabel('AI provider');
      await expect(select).toHaveValue('openai');
      await page.getByRole('button', { name: /remove saved key/i }).click();
      await select.selectOption('anthropic');
      // The Anthropic key is still shown as on file.
      await expect(page.getByRole('button', { name: /remove saved key/i })).toBeVisible();
      await page.getByRole('button', { name: /save changes/i }).click();
      await expect(page.getByText(/settings saved/i)).toBeVisible();
    } finally {
      await context.close();
    }

    expect(patches).toHaveLength(1);
    expect(patches[0]).toMatchObject({ llmProvider: 'anthropic' });
    expect(patches[0]).not.toHaveProperty('llmApiKey');
    expect(await (await api.get('/api/settings')).json()).toMatchObject({
      llmProvider: 'anthropic',
      llmApiKeysSet: { anthropic: true, openai: true },
      aiStatus: { configured: true, provider: 'anthropic' },
    });
  });

  test('W-1: a key typed for OpenAI is dropped when the user switches to Anthropic', async ({ browser, apiAs }) => {
    const user = await freshUser('w1typed');
    const api = apiAs(user);
    expect((await api.patch('/api/settings', { llmProvider: 'openai' })).status()).toBe(200);
    const typed = `sk-e2e-${randomUUID()}`;

    const context = await browser.newContext({ storageState: storageStateFor(user) });
    const page = await context.newPage();
    const patches: Record<string, unknown>[] = [];
    page.on('request', (req) => {
      if (req.method() === 'PATCH' && new URL(req.url()).pathname.endsWith('/api/settings')) patches.push(req.postDataJSON());
    });
    try {
      await page.goto('/settings');
      await expect(page.getByLabel('AI provider')).toHaveValue('openai');
      await page.getByRole('textbox', { name: /^API key/ }).fill(typed);
      await page.getByLabel('AI provider').selectOption('anthropic');
      await expect(page.getByRole('textbox', { name: /^API key/ })).toHaveValue('');
      await page.getByRole('button', { name: /test connection/i }).click();
      await expect(page.getByText('Anthropic needs an API key. Enter one to test it.')).toBeVisible();
      await page.getByRole('button', { name: /save changes/i }).click();
      await expect(page.getByText(/settings saved/i)).toBeVisible();
    } finally {
      await context.close();
    }

    expect(patches).toHaveLength(1);
    expect(JSON.stringify(patches[0])).not.toContain(typed);
    expect(patches[0]).not.toHaveProperty('llmApiKey');
    expect(await (await api.get('/api/settings')).json()).toMatchObject({
      llmProvider: 'anthropic',
      llmApiKeysSet: { anthropic: false, openai: false },
    });
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
