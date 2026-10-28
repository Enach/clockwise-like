/**
 * PAC-50 AC-23: SSO configuration writes need an administrator step-up.
 *
 * The API-level journey (there is no SSO admin page, spec §3): an org member
 * with no grant is refused with a `stepup_url`; following it through the
 * Google stand-in (e2e/fake-google/idp.go), which signs the member in as an
 * administrator of the org domain, comes back with `stepup=granted`; the same
 * write then succeeds and the row exists.
 *
 * The test plays the browser. The backend sends the browser to the stand-in at
 * its in-network address (PACEDAY_GOOGLE_IDP_BASE_URL); from the test host the
 * stand-in is reachable on its published port, so the test requests the same
 * path and query there. Everything else goes through nginx, with the session
 * and the state cookie in the request context's own cookie jar.
 */

import { randomUUID } from 'node:crypto';

import { test, expect } from '../fixtures';
import { BASE_URL, storageStateFor, type SessionUser } from '../auth';
import { query } from '../db';
import { FAKE_GOOGLE_URL } from '../fake';

// The scope Google's step-up requests beyond the identity scopes (contract
// SsoWriteStepupRequired.stepup_scopes; requested only by the step-up, AC-24).
const DIRECTORY_SCOPE = 'https://www.googleapis.com/auth/admin.directory.user.readonly';

async function orgMember(): Promise<{ user: SessionUser; domain: string }> {
  const id = randomUUID();
  const domain = `pac50-${id.slice(0, 8)}.test`;
  const user = { id, email: `admin@${domain}`, name: 'E2E SSO admin' };
  const org = (
    await query<{ id: string }>('INSERT INTO organizations (name, domain) VALUES ($1, $2) RETURNING id', [
      `PAC-50 ${id.slice(0, 8)}`,
      domain,
    ])
  )[0];
  if (!org) throw new Error('organization not created');
  await query('INSERT INTO users (id, email, name, org_id, created_at) VALUES ($1, $2, $3, $4, NOW())', [
    user.id,
    user.email,
    user.name,
    org.id,
  ]);
  return { user, domain };
}

test.describe('PAC-50 — SSO admin step-up', () => {
  test('AC-23: refused without a grant, granted through the stand-in, then the write succeeds', async ({ playwright }) => {
    const { user, domain } = await orgMember();
    const ctx = await playwright.request.newContext({ baseURL: BASE_URL, storageState: storageStateFor(user) });
    try {
      const body = {
        domain,
        provider_name: 'E2E Okta',
        provider_type: 'oidc',
        oidc_issuer: 'https://idp.example',
        oidc_client_id: 'cid',
        oidc_client_secret: 'secret',
      };
      const rows = async () =>
        (await query<{ n: string }>('SELECT count(*) AS n FROM sso_providers WHERE domain = $1', [domain]))[0]?.n;

      // 1. No grant: 403 with the step-up URL, what the optional step-up would
      //    request and what is lost without it (owner 2026-10-27, spec §9 D-11;
      //    the e2e stack configures Google only, so there is no microsoft key),
      //    nothing written.
      const refused = await ctx.post('/api/admin/sso', { data: body });
      expect(refused.status()).toBe(403);
      const refusal = await refused.json();
      expect(refusal).toEqual({
        error: 'sso_write_requires_admin_stepup',
        stepup_url: '/api/admin/sso/stepup',
        stepup_scopes: { google: [DIRECTORY_SCOPE] },
        without_stepup: ['sso_configuration_writes'],
      });
      expect(await rows()).toBe('0');

      // 2. The start, resolved against the API base (not the page), with the provider.
      const start = await ctx.get(`${refusal.stepup_url}?provider=google`, { maxRedirects: 0 });
      expect(start.status()).toBe(302);
      const toIdP = new URL(start.headers()['location'] ?? '');
      expect(`${toIdP.origin}${toIdP.pathname}`).toBe('http://fake-google:8080/o/oauth2/v2/auth');
      expect(toIdP.searchParams.get('redirect_uri')).toBe(`${BASE_URL}/api/admin/sso/stepup/callback`);
      expect(toIdP.searchParams.get('login_hint')).toBe(user.email);
      // The start requests exactly what the 403 announced, beyond identity scopes.
      expect(toIdP.searchParams.get('scope')?.split(' ')).toContain(DIRECTORY_SCOPE);

      // 3. The browser at the provider (the stand-in's published port).
      const atIdP = await ctx.get(`${FAKE_GOOGLE_URL}${toIdP.pathname}${toIdP.search}`, { maxRedirects: 0 });
      expect(atIdP.status()).toBe(302);
      const back = atIdP.headers()['location'] ?? '';
      expect(back.startsWith(`${BASE_URL}/api/admin/sso/stepup/callback?`)).toBe(true);

      // 4. Back at the callback, with the session and the state cookie.
      const done = await ctx.get(back, { maxRedirects: 0 });
      expect(done.status()).toBe(302);
      expect(done.headers()['location']).toBe(`${BASE_URL}/settings?stepup=granted`);

      // 5. The same write now succeeds, and the row exists.
      const written = await ctx.post('/api/admin/sso', { data: body });
      expect(written.status()).toBe(201);
      expect((await written.json()).Domain).toBe(domain);
      expect(await rows()).toBe('1');

      // One grant, one write: the next write needs a new step-up.
      expect((await ctx.post('/api/admin/sso', { data: body })).status()).toBe(403);

      const events = await query<{ kind: string; details: Record<string, unknown> }>(
        'SELECT kind, details FROM sso_events WHERE user_id = $1 ORDER BY created_at, id',
        [user.id],
      );
      expect(events.map((e) => e.kind)).toEqual(['sso_stepup', 'sso_provider_written']);
      expect(events[0]?.details).toMatchObject({ provider: 'google', outcome: 'granted', reason: null });
      expect(events[1]?.details).toMatchObject({ op: 'create', via: 'stepup' });
    } finally {
      await ctx.dispose();
    }
  });

  test('a subdomain, foreign or padded domain is 403 and nothing is written (exact domain rule, owner 2026-10-27)', async ({
    playwright,
  }) => {
    const { user, domain } = await orgMember();
    const ctx = await playwright.request.newContext({ baseURL: BASE_URL, storageState: storageStateFor(user) });
    try {
      for (const d of [`sub.${domain}`, `other-${domain}`, ` ${domain} `]) {
        const res = await ctx.post('/api/admin/sso', {
          data: { domain: d, provider_name: 'X', provider_type: 'oidc' },
        });
        expect(res.status()).toBe(403);
        expect(await res.json()).toEqual({ error: 'provider_domain_mismatch' });
        const counted = await query<{ n: string }>(
          'SELECT count(*) AS n FROM sso_providers WHERE lower(domain) = lower($1)',
          [d],
        );
        expect(counted[0]?.n).toBe('0');
      }
    } finally {
      await ctx.dispose();
    }
  });

  test('AC-24: Google sign-in requests no step-up or admin-consent scope', async ({ playwright }) => {
    const ctx = await playwright.request.newContext({ baseURL: BASE_URL });
    try {
      const start = await ctx.get('/api/auth/google', { maxRedirects: 0 });
      expect(start.status()).toBe(302);
      const scopes = new URL(start.headers()['location'] ?? '').searchParams.get('scope')?.split(' ') ?? [];
      expect(scopes.length).toBeGreaterThan(0);
      expect(scopes).not.toContain(DIRECTORY_SCOPE);
      expect(scopes.filter((s) => s.includes('admin.directory'))).toEqual([]);
    } finally {
      await ctx.dispose();
    }
  });
});
