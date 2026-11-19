/**
 * What the browser may reach during a test: the e2e stack on this machine,
 * nothing else. Tests must never reach a real external service (a real
 * provider page loads its own scripts, logs its own CSP reports and changes
 * without notice, so a journey that reaches one fails or passes for reasons
 * outside the product).
 *
 * Used twice:
 *  - fixtures.ts routes every browser request to an origin that is not on the
 *    stack, aborts it and fails the test (the `offStackRequests` fixture);
 *  - playwright.config.ts gives Chromium a resolver that resolves only these
 *    hosts, which also blocks what routing cannot see (the hops of an HTTP
 *    redirect: Playwright calls a route handler for the first URL only).
 *
 * A request a test answers itself with page.route (route.fulfill) never
 * leaves the browser and is not off-stack: that is how a journey stops at a
 * provider (tests/pac97-signin-detect.spec.ts) or serves an avatar
 * (tests/csp.spec.ts).
 */

/**
 * Off-stack hosts a test may still reach for real. Empty on purpose: add a
 * host here only with a named PAC issue and a reason no stand-in can do.
 */
export const OFF_STACK_ALLOWLIST: readonly string[] = [];

/**
 * Off-stack resources the app itself loads on every page, answered by a
 * stand-in in the browser instead of the real service. The SPA's index.html
 * links Google Fonts (smart-calendar-flow index.html; nginx's CSP allows
 * fonts.googleapis.com and fonts.gstatic.com for it). The stylesheet is
 * answered with an empty one, so no font file is ever requested and the
 * page renders in its fallback fonts. Nothing a journey asserts depends on
 * the typeface.
 */
export const OFF_STACK_STAND_INS: Readonly<Record<string, { status: number; contentType: string; body: string }>> = {
  'fonts.googleapis.com': { status: 200, contentType: 'text/css', body: '/* e2e stand-in: no web fonts */' },
  'fonts.gstatic.com': { status: 404, contentType: 'text/plain', body: 'e2e stand-in: no web fonts' },
};

/** The stand-in answer for `url`, when its host has one. */
export function offStackStandIn(url: string): (typeof OFF_STACK_STAND_INS)[string] | undefined {
  try {
    return OFF_STACK_STAND_INS[new URL(url).hostname.toLowerCase()];
  } catch {
    return undefined;
  }
}

/** The stack's browser-visible origins, as configured (auth.ts, csp.spec.ts, fake.ts defaults). */
const STACK_URLS = [
  process.env.E2E_BASE_URL ?? 'http://localhost:8088',
  process.env.E2E_INJECTED_BASE_URL ?? 'http://localhost:18089',
  process.env.E2E_FAKE_GOOGLE_URL ?? 'http://127.0.0.1:18090',
  process.env.E2E_SINK_URL ?? 'http://127.0.0.1:18091',
];

function hostOf(url: string): string | null {
  try {
    return new URL(url).hostname.toLowerCase();
  } catch {
    return null;
  }
}

/**
 * Every host the stack is on (names and IP literals), plus the allowlist:
 * what the browser's resolver may resolve.
 */
export function stackHosts(): string[] {
  // Loopback: the stack's published ports and servers a test starts itself.
  const hosts = new Set<string>(['localhost', '127.0.0.1', '[::1]', '::1']);
  for (const u of STACK_URLS) {
    const h = hostOf(u);
    if (h) hosts.add(h);
  }
  for (const h of OFF_STACK_ALLOWLIST) hosts.add(h.toLowerCase());
  return [...hosts];
}

const STACK_HOSTS = new Set<string>(stackHosts());

/**
 * True when the browser may reach `url`: a non-network scheme (data:, blob:,
 * about:, ...), a loopback host, a *.localhost name, a configured stack host
 * or an allowlisted one.
 */
export function isOnStack(url: string): boolean {
  let u: URL;
  try {
    u = new URL(url);
  } catch {
    return false;
  }
  if (!['http:', 'https:', 'ws:', 'wss:'].includes(u.protocol)) return true;
  const host = u.hostname.toLowerCase();
  return STACK_HOSTS.has(host) || host.endsWith('.localhost');
}

/**
 * Chromium's --host-resolver-rules value: every host fails to resolve except
 * the stack's. The rules apply to IP literals too (observed: without an
 * EXCLUDE, 127.0.0.1 fails with ERR_NAME_NOT_RESOLVED), so loopback is
 * excluded explicitly and a public IP literal is refused like a name.
 */
export function hostResolverRules(): string {
  const excludes = stackHosts().map((h) => `EXCLUDE ${h}`);
  return ['MAP * ~NOTFOUND', ...excludes, 'EXCLUDE *.localhost'].join(', ');
}
