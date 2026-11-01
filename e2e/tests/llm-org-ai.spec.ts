/**
 * PAC-73 Part 2 (docs/specs/NEW-llm-bring-your-own-part2.md): an
 * organisation-wide AI provider and key.
 *
 * An administrator proves it with PAC-50's step-up started for this purpose,
 * through the Google stand-in (e2e/fake-google/idp.go, which signs the member
 * in as an administrator of the org domain), and sets the organisation's AI.
 * Every member reads the same configuration, never the key; a member is told
 * and asked once before their first AI request on it (owner O-13), and
 * nothing is sent before they confirm.
 *
 * The public OpenAI host is approved by the product but unreachable from the
 * stack, so a request that is sent fails at the provider (parse 500, probe
 * 502): that is how these journeys tell "sent" from "not configured" (200 /
 * 409 with notConfigured, nothing sent). Break-glass is not configured on the
 * e2e stack (no PACEDAY_ADMIN_EMAILS); it is covered by the backend tests.
 */

import { randomUUID } from 'node:crypto';

import { test, expect } from '../fixtures';
import { BASE_URL, storageStateFor, type SessionUser } from '../auth';
import { query } from '../db';
import { FAKE_GOOGLE_URL } from '../fake';

const DIRECTORY_SCOPE = 'https://www.googleapis.com/auth/admin.directory.user.readonly';
const READ_WORDS = /\b(read|reads|see)\b/i;

async function orgWithMembers(): Promise<{ admin: SessionUser; member: SessionUser; other: SessionUser; domain: string }> {
  const id = randomUUID().slice(0, 8);
  const domain = `pac73-${id}.test`;
  const org = (await query<{ id: string }>('INSERT INTO organizations (name, domain) VALUES ($1, $2) RETURNING id', [`PAC-73 ${id}`, domain]))[0];
  if (!org) throw new Error('organization not created');
  const make = async (local: string, name: string): Promise<SessionUser> => {
    const user = { id: randomUUID(), email: `${local}@${domain}`, name };
    await query('INSERT INTO users (id, email, name, org_id, created_at) VALUES ($1, $2, $3, $4, NOW())', [user.id, user.email, user.name, org.id]);
    return user;
  };
  return { admin: await make('admin', 'E2E org AI admin'), member: await make('member', 'E2E org AI member'), other: await make('other', 'E2E org AI other'), domain };
}

test.describe('PAC-73 Part 2 — the organisation AI configuration', () => {
  test('P2-AC-1/2/3/7/30/32, P2-AC-14/22/26/38: an admin steps up for this purpose and sets it; a member is asked once, then it is used', async ({
    playwright,
    browser,
  }) => {
    const { admin, member, other } = await orgWithMembers();
    const key = `sk-e2e-org-${randomUUID()}`;
    const adminApi = await playwright.request.newContext({ baseURL: BASE_URL, storageState: storageStateFor(admin) });
    const memberApi = await playwright.request.newContext({ baseURL: BASE_URL, storageState: storageStateFor(member) });
    try {
      // P2-AC-1: no proof. The 403 says how to step up for this purpose.
      const refused = await adminApi.patch('/api/organization/ai', { data: { configurationVersion: 0, provider: 'openai', apiKey: key } });
      expect(refused.status()).toBe(403);
      expect(await refused.json()).toEqual({
        error: 'organization_ai_write_requires_admin_stepup',
        stepup_url: '/api/admin/sso/stepup',
        stepup_purpose: 'llm_org_key_write',
        stepup_scopes: { google: [DIRECTORY_SCOPE] },
        without_stepup: ['organization_ai_configuration_writes'],
      });
      const options = await adminApi.get('/api/admin/sso/stepup/options?purpose=llm_org_key_write');
      expect(await options.json()).toMatchObject({ without_stepup: ['organization_ai_configuration_writes'] });

      // P2-AC-3: the step-up started for this purpose lands with it.
      const start = await adminApi.get('/api/admin/sso/stepup?provider=google&purpose=llm_org_key_write', { maxRedirects: 0 });
      expect(start.status()).toBe(302);
      const toIdP = new URL(start.headers()['location'] ?? '');
      const atIdP = await adminApi.get(`${FAKE_GOOGLE_URL}${toIdP.pathname}${toIdP.search}`, { maxRedirects: 0 });
      const done = await adminApi.get(atIdP.headers()['location'] ?? '', { maxRedirects: 0 });
      expect(done.headers()['location']).toBe(`${BASE_URL}/settings?stepup=granted&purpose=llm_org_key_write`);
      const grants = await query<{ purpose: string }>('SELECT purpose FROM sso_stepup_grants WHERE user_id = $1 AND consumed_at IS NULL', [admin.id]);
      expect(grants.map((g) => g.purpose)).toEqual(['llm_org_key_write']);

      // P2-AC-2: the change succeeds once; a second needs a new step-up.
      const saved = await adminApi.patch('/api/organization/ai', { data: { configurationVersion: 0, provider: 'openai', apiKey: key } });
      expect(saved.status(), await saved.text()).toBe(200);
      expect(await saved.json()).toMatchObject({ configured: true, provider: 'openai', keySet: true, configurationVersion: 1 });
      expect((await adminApi.patch('/api/organization/ai', { data: { configurationVersion: 1, model: 'gpt-4o' } })).status()).toBe(403);

      // P2-AC-7: every member reads the same bytes, never the key.
      const readA = await (await adminApi.get('/api/organization/ai')).text();
      const readM = await (await memberApi.get('/api/organization/ai')).text();
      expect(readM).toBe(readA);
      for (const body of [readA, await saved.text(), await (await memberApi.get('/api/settings')).text()]) expect(body).not.toContain(key);

      // P2-AC-14 / P2-AC-22: row 5 until the member confirms; nothing is sent.
      const unconfirmed = await (await memberApi.post('/api/nlp/parse', { data: { text: 'meet alice tomorrow' } })).json();
      expect(unconfirmed.not_configured).toMatchObject({ reason: 'organization_unconfirmed', enablers: ['user'] });
      expect(unconfirmed.error).toMatch(/\baccess\b/);
      expect(unconfirmed.error).not.toMatch(READ_WORDS);
      // P2-AC-38: a settings write storing "organization" records nothing.
      expect((await memberApi.patch('/api/settings', { data: { llmProvider: 'organization' } })).status()).toBe(200);
      expect((await query('SELECT 1 FROM org_ai_confirmations WHERE user_id = $1', [member.id])).length).toBe(0);
    } finally {
      await adminApi.dispose();
      await memberApi.dispose();
    }

    // P2-AC-22 / P2-AC-26 in the web: the bar shows the notice with a confirm
    // action; confirming sends the request (it reaches the provider, which
    // the stack cannot reach, so the bar shows a failure, not the notice).
    const mctx = await browser.newContext({ storageState: storageStateFor(member) });
    const page = await mctx.newPage();
    try {
      await page.goto('/app');
      await page.getByLabel('Describe what to schedule').fill('meet alice tomorrow');
      await page.getByRole('button', { name: /^send$/i }).click();
      const notice = page.getByTestId('ai-off-notice');
      await expect(notice).toContainText('your organisation provides AI through OpenAI');
      expect(await notice.textContent()).not.toMatch(READ_WORDS);
      await notice.getByTestId('ai-org-confirm').click();
      const consent = page.getByTestId('org-ai-consent-text');
      await expect(consent).toContainText("your organisation's AI provider account (OpenAI), which its administrators can access outside Paceday");
      expect(await consent.textContent()).not.toMatch(READ_WORDS);
      expect((await query('SELECT 1 FROM org_ai_confirmations WHERE user_id = $1', [member.id])).length).toBe(0);
      await page.getByRole('button', { name: /confirm and turn ai on/i }).click();
      await expect(page.getByTestId('ai-off-notice')).toHaveCount(0);
      const rows = await query<{ configuration_version: string }>('SELECT configuration_version FROM org_ai_confirmations WHERE user_id = $1', [member.id]);
      expect(rows.map((r) => String(r.configuration_version))).toEqual(['1']);

      // P2-AC-30 / P2-AC-31: Settings shows the member the same section and
      // their choice selected; the key never.
      await page.goto('/settings');
      const section = page.getByTestId('org-ai-section');
      await expect(section).toContainText('OpenAI');
      await expect(section).toContainText('A key is set');
      await expect(section).toContainText('An administrator of your organisation can change this.');
      await expect(page.getByLabel('AI provider').locator('option:checked')).toHaveText("Use my organisation's AI");
      expect(await page.content()).not.toContain(key);
    } finally {
      await mctx.close();
    }

    // P2-AC-38: choosing "Use my organisation's AI" in Settings shows the
    // notice and, on confirm, confirms the displayed version.
    const otherApi = await playwright.request.newContext({ baseURL: BASE_URL, storageState: storageStateFor(other) });
    try {
      expect((await otherApi.patch('/api/settings', { data: { llmProvider: 'openai' } })).status()).toBe(200);
    } finally {
      await otherApi.dispose();
    }
    const octx = await browser.newContext({ storageState: storageStateFor(other) });
    const opage = await octx.newPage();
    try {
      await opage.goto('/settings');
      await opage.getByLabel('AI provider').selectOption('organization');
      await expect(opage.getByTestId('org-ai-consent-text')).toContainText('(OpenAI)');
      await opage.getByRole('button', { name: /confirm and turn ai on/i }).click();
      await expect(opage.getByTestId('org-ai-consent-text')).toHaveCount(0);
      await expect(opage.getByLabel('AI provider')).toHaveValue('organization');
      const s = await query<{ llm_provider: string }>('SELECT llm_provider FROM settings WHERE user_id = $1', [other.id]);
      expect(s[0]?.llm_provider).toBe('organization');
      expect((await query('SELECT 1 FROM org_ai_confirmations WHERE user_id = $1', [other.id])).length).toBe(1);
    } finally {
      await octx.close();
    }

    // P2-AC-32: the admin's landing is the organisation AI section's; the
    // SSO section shows no outcome for it.
    const actx = await browser.newContext({ storageState: storageStateFor(admin) });
    const apage = await actx.newPage();
    try {
      await apage.goto('/settings?stepup=granted&purpose=llm_org_key_write');
      await expect(apage.getByTestId('org-ai-stepup-outcome')).toContainText('Administrator confirmation granted');
      await expect(apage.getByTestId('org-ai-form')).toBeVisible();
      await expect(apage.getByText(/used by your next SSO configuration change/)).toHaveCount(0);
    } finally {
      await actx.close();
    }
  });
});
