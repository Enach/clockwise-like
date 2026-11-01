/**
 * PAC-66 AC-10: an Azure OpenAI key goes only to hosts the operator approved.
 *
 * The compose stack approves exactly one Azure OpenAI host
 * (PACEDAY_LLM_APPROVED_HOSTS_AZURE_OPENAI in docker-compose.e2e.yml). Nothing
 * listens on it and there is no TLS fake, so this journey proves only what such
 * a stack can: an unapproved endpoint is refused on write, and a stored one
 * (forced by a direct database write, as a value saved before PAC-66 would be)
 * is refused on probe with a 422. That exactly one request reaches an approved
 * host is proven at unit level (backend/api/pac66_llm_destination_test.go,
 * AC-6) with a sentinel key and a recording transport.
 *
 * NEW-llm-bring-your-own (contract C-1): Azure OpenAI now runs only on a key
 * the user bound to it, and "AI not configured" (no key) is decided before the
 * destination. So the user saves an Azure key first, which keeps the 422
 * pinned; without a key the same probe is the 409 needs_key.
 */

import { randomUUID } from 'node:crypto';

import { test, expect } from '../fixtures';
import { type SessionUser } from '../auth';
import { query } from '../db';

const APPROVED = 'https://e2e-approved.openai.azure.com';
const UNAPPROVED = 'https://attacker.example';
const KEY = 'sk-e2e-pac66-azure-sentinel';

async function freshUser(): Promise<SessionUser> {
  const id = randomUUID();
  const user = { id, email: `e2e.pac66.${id.slice(0, 8)}@paceday.test`, name: 'E2E PAC-66' };
  await query('INSERT INTO users (id, email, name, created_at) VALUES ($1, $2, $3, NOW())', [user.id, user.email, user.name]);
  return user;
}

test.describe('PAC-66 — approved LLM destinations', () => {
  test('AC-10: an unapproved Azure endpoint is refused on write and on probe', async ({ apiAs }) => {
    const user = await freshUser();
    const api = apiAs(user);

    // Without a key the provider is not configured, whatever its endpoint (C-1).
    const keyless = await api.patch('/api/settings', { llmProvider: 'azure_openai', azureEndpoint: APPROVED });
    expect(keyless.status(), await keyless.text()).toBe(200);
    const notConfigured = await api.post('/api/llm/test', {});
    expect(notConfigured.status(), await notConfigured.text()).toBe(409);
    expect((await notConfigured.json()).notConfigured).toMatchObject({ reason: 'needs_key', provider: 'azure_openai' });

    const saved = await api.patch('/api/settings', { llmApiKey: KEY });
    expect(saved.status(), await saved.text()).toBe(200);
    expect(await saved.text()).not.toContain(KEY);

    const refused = await api.patch('/api/settings', { azureEndpoint: UNAPPROVED });
    expect(refused.status(), await refused.text()).toBe(400);
    expect(refused.headers()['content-type']).toContain('application/json');
    expect((await refused.json()).error).toMatch(/^azureEndpoint /);
    expect(await (await api.get('/api/settings')).json()).toMatchObject({ azureEndpoint: APPROVED });

    await query('UPDATE settings SET azure_endpoint = $1 WHERE user_id = $2', [UNAPPROVED, user.id]);
    const probe = await api.post('/api/llm/test', {});
    expect(probe.status(), await probe.text()).toBe(422);
    expect(probe.headers()['content-type']).toContain('application/json');
    const body = await probe.json();
    expect(body).toMatchObject({ reason: 'destination_not_approved', property: 'azureEndpoint' });
    expect(body.error).toContain('not an approved');
    expect(JSON.stringify(body)).not.toContain(KEY);
  });
});
