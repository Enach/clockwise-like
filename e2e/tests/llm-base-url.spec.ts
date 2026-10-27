/**
 * PAC-66-B AC-B17: a user-set OpenAI or Ollama base URL cannot make the server
 * send a request to another service on the stack's network.
 *
 * The compose stack approves no Ollama origin and no extra OpenAI or Anthropic
 * host (docker-compose.e2e.yml sets none of PACEDAY_LLM_APPROVED_HOSTS_OPENAI,
 * _ANTHROPIC or PACEDAY_LLM_APPROVED_ORIGINS_OLLAMA). The database is
 * reachable from the backend at postgres:5432, which stands in for any
 * internal service. On the stack this test observes responses, not requests:
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

    // As a value stored before PAC-66-B would be: both base URLs forced, so the
    // forced address is the effective destination of ollama and of openai.
    await query('UPDATE settings SET llm_base_url = $1, ollama_base_url = $1 WHERE user_id = $2', [INTERNAL, user.id]);

    const ollama = await api.post('/api/llm/test', { llmProvider: 'ollama' });
    expect(ollama.status(), await ollama.text()).toBe(422);
    expect(ollama.headers()['content-type']).toContain('application/json');
    const ollamaBody = await ollama.json();
    expect(ollamaBody).toMatchObject({ reason: 'provider_not_enabled' });
    expect(ollamaBody.error).toContain('not enabled on this deployment');

    const openai = await api.post('/api/llm/test', { llmProvider: 'openai' });
    expect(openai.status(), await openai.text()).toBe(422);
    const openaiBody = await openai.json();
    expect(openaiBody).toMatchObject({ reason: 'destination_not_approved', property: 'llmBaseUrl' });
    expect(openaiBody.error).toContain('not an approved');
    for (const body of [ollamaBody, openaiBody]) {
      expect(JSON.stringify(body)).not.toContain(KEY);
    }
  });
});
