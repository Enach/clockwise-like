/**
 * PAC-66-B AC-B17: a user-set OpenAI base URL cannot make the server send a
 * request to another service on the stack's network.
 *
 * The compose stack approves no extra OpenAI or Anthropic host
 * (docker-compose.e2e.yml sets neither PACEDAY_LLM_APPROVED_HOSTS_OPENAI nor
 * _ANTHROPIC). The database is reachable from the backend at postgres:5432,
 * which stands in for any internal service. NEW-llm-bring-your-own replaced the
 * Ollama half: an account stored on Ollama is "AI not configured" with reason
 * provider_removed, and "ollama" is no longer a value a client may name. On the stack this test observes responses, not requests:
 * that a refusal sends zero requests, and that exactly one request reaches an
 * approved destination, is proven at unit level
 * (backend/api/pac66b_base_url_test.go, backend/nlp/user_destination_test.go)
 * with a sentinel key and a recording transport.
 */

import { randomUUID } from 'node:crypto';

import { test, expect } from '../fixtures';
import { type SessionUser } from '../auth';
import { query } from '../db';

const INTERNAL = 'http://postgres:5432';
const KEY = 'sk-e2e-pac66b-sentinel';

async function freshUser(): Promise<SessionUser> {
  const id = randomUUID();
  const user = { id, email: `e2e.pac66b.${id.slice(0, 8)}@paceday.test`, name: 'E2E PAC-66-B' };
  await query('INSERT INTO users (id, email, name, created_at) VALUES ($1, $2, $3, NOW())', [user.id, user.email, user.name]);
  return user;
}

test.describe('PAC-66-B — approved destinations for user-set base URLs', () => {
  test('AC-B17: an internal base URL is refused on write and, once stored, on probe', async ({ apiAs }) => {
    const user = await freshUser();
    const api = apiAs(user);

    const saved = await api.patch('/api/settings', { llmProvider: 'openai', llmApiKey: KEY });
    expect(saved.status(), await saved.text()).toBe(200);
    const before = (await (await api.get('/api/settings')).json()).llmBaseUrl;

    const refused = await api.patch('/api/settings', { llmBaseUrl: INTERNAL });
    expect(refused.status(), await refused.text()).toBe(400);
    expect(refused.headers()['content-type']).toContain('application/json');
    expect((await refused.json()).error).toMatch(/^llmBaseUrl /);
    expect(await (await api.get('/api/settings')).json()).toMatchObject({ llmBaseUrl: before });

    // As a value stored before PAC-66-B would be: the base URL forced, so the
    // forced address is the effective destination of openai.
    await query('UPDATE settings SET llm_base_url = $1 WHERE user_id = $2', [INTERNAL, user.id]);

    const openai = await api.post('/api/llm/test', { llmProvider: 'openai' });
    expect(openai.status(), await openai.text()).toBe(422);
    const openaiBody = await openai.json();
    expect(openaiBody).toMatchObject({ reason: 'destination_not_approved', property: 'llmBaseUrl' });
    expect(openaiBody.error).toContain('not an approved');
    expect(JSON.stringify(openaiBody)).not.toContain(KEY);
  });

  // Replaces the Ollama probe case (spec D-11, NEW-llm-bring-your-own AC-10).
  test('AC-10: an account stored on Ollama is not configured, and ollama cannot be named', async ({ apiAs }) => {
    const user = await freshUser();
    const api = apiAs(user);
    expect((await api.get('/api/settings')).status()).toBe(200);
    await query("UPDATE settings SET llm_provider = 'ollama', llm_base_url = $1, ollama_base_url = $1 WHERE user_id = $2", [INTERNAL, user.id]);

    for (const body of [{}, { llmProvider: 'unsupported', llmModel: '' }]) {
      const probe = await api.post('/api/llm/test', body);
      expect(probe.status(), await probe.text()).toBe(409);
      expect((await probe.json()).notConfigured).toMatchObject({ reason: 'provider_removed' });
    }
    const named = await api.post('/api/llm/test', { llmProvider: 'ollama' });
    expect(named.status(), await named.text()).toBe(400);

    const parse = await api.post('/api/nlp/parse', { text: 'meet bob tomorrow' });
    expect(parse.status(), await parse.text()).toBe(200);
    const parsed = await parse.json();
    expect(parsed).toMatchObject({ intent: 'unknown', not_configured: { reason: 'provider_removed' } });
    expect(parsed.error).toContain('Ollama is no longer supported');
  });
});
