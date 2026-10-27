# Spec — NEW-nginx-tls-security-headers: Production edge serves plain HTTP only — add TLS, an HTTP→HTTPS redirect and browser security headers

- **Linear**: none yet. Factory ticket `NEW-nginx-tls-security-headers`; a
  search of Linear for "nginx" (2026-10-27) found no issue for this work, only
  PAC-22 and PAC-50, which mention the edge in passing. The parent issue must
  be filed before the contract/plan stages so stage PRs have something to
  reference (README §6).
- **Status**: draft
- **Author**: spec-author
- **Inherited scope**: none from `docs/factory/api-audit.md` or `x-uncertain`
  markers — this work touches no API operation. It closes open risk **#3** in
  `docs/security.md:85` ("nginx production config lacks TLS / HSTS / CSP /
  rate-limit") for its TLS, HSTS and CSP parts only; rate limiting stays open
  (§5). It also makes the B1 row of the STRIDE table (`docs/security.md:52`,
  "nginx HSTS still missing") true, so that row must be updated by whoever
  implements this.

## 1. Problem

Paceday's production stack publishes the reverse proxy on ports 80 and 443 and
mounts a Let's Encrypt certificate directory into it, but the proxy only ever
listens on port 80. Nothing answers on 443, nothing redirects HTTP to HTTPS, and
no response carries any browser security header.

Who it hurts, and how:

- **Every signed-in user.** The session cookie is marked `Secure` in production
  (PAC-20). A browser will not store or send a `Secure` cookie over plain HTTP,
  so on an HTTP-only edge either sign-in silently fails, or something other than
  this proxy is terminating TLS in front of it (§8 OQ-1). Either way the
  repository does not describe a working, secure production edge.
- **Any user on an untrusted network.** Without TLS, calendar data (meeting
  titles, attendees — "High" sensitivity in `docs/security.md:13`), OAuth
  redirects and the session itself cross the network in clear text. Without
  HSTS, a user who types the bare domain can be downgraded even once TLS exists.
- **Any user who can be lured to a hostile page.** Without a framing policy the
  app can be embedded and clickjacked; without a content policy an injected
  script runs with the user's session and can exfiltrate to any origin; without
  `nosniff` a mistyped upload or response can be sniffed into script.

The cost is that the "TLS at edge" boundary the threat model assumes
(`docs/security.md:40`, B1) does not exist in the configuration we ship.

## 2. Current behaviour

### 2.1 The proxy configuration

`nginx/nginx.conf` is the whole edge configuration (17 lines, CRLF):

- `:1-3` — one `server` block, `listen 80`, `server_name localhost`. No other
  `listen`, no `ssl_certificate`, no `return 301`.
- `:5-10` — `location /api/` proxies to `http://backend:8080`, forwarding
  `Host`, `X-Real-IP` (`$remote_addr`) and `X-Forwarded-For`. No
  `X-Forwarded-Proto`.
- `:12-16` — `location /` proxies to `http://frontend:80`, forwarding `Host` and
  `X-Real-IP`.
- No `add_header` anywhere: no HSTS, CSP, `X-Frame-Options`,
  `X-Content-Type-Options` or `Referrer-Policy`. No ACME challenge location.

The file was last changed by the initial compose commit (`937a329`).

### 2.2 Where that one file is used

The same file is mounted, unmodified, as the proxy's only site configuration in
three stacks:

| Stack | Mount | Published ports |
|---|---|---|
| production | `docker-compose.prod.yml:121-133` | `80:80`, `443:443` (`:128-129`); `${TLS_CERT_DIR:-/etc/letsencrypt}` mounted read-only at `/etc/letsencrypt` (`:133`) |
| local / base | `docker-compose.yml:91-100` | `80:80` only, no certificate mount |
| e2e | `e2e/docker-compose.e2e.yml:200-212` | `127.0.0.1:8088:80` only, no certificate mount |

The e2e mount is deliberate and load-bearing: its comment
(`e2e/docker-compose.e2e.yml:205-206`) says "The production routing rules,
unmodified. If this file breaks, the suite must break", and the e2e README
(`e2e/README.md:81-82`) says the suite proves the `/api/` rule because every
request goes through the proxy. The e2e base URL is plain HTTP
(`e2e/README.md:318`, `http://localhost:8088`); `e2e/playwright.config.ts:55-56`
notes nothing needs a real certificate and sets `ignoreHTTPSErrors: false`.

**Consequence for this work:** a configuration that requires certificate files
to exist, or that redirects every port-80 request, cannot be mounted as-is into
the local and e2e stacks — neither has certificates, and both are reached over
plain HTTP. The current single-file arrangement cannot simply gain a TLS block.

`.env.example:89-91` documents `TLS_CERT_DIR=/etc/letsencrypt` as "production
only". Nothing in the repository obtains or renews certificates: no certbot
service in any compose file, no ACME webroot mount, no renewal script.

### 2.3 What the proxied applications already do

- **Backend.** Cookies are `Secure` unless `PACEDAY_INSECURE_COOKIES=true`
  (`backend/api/cookies.go:5-21`, decided at startup by
  `backend/config/cookies.go:19`); `docker-compose.prod.yml` does not set that
  variable, so production cookies are `Secure`. The backend never sees TLS
  itself and reads no `X-Forwarded-Proto` (no non-test Go file references it).
  It sets no security headers of its own except `X-Content-Type-Options:
  nosniff` on responses written through `http.Error` (pinned by
  `backend/api/handlers_audit_test.go:213-214`). The per-client rate limiter
  trusts `X-Real-IP` from the proxy (`backend/api/ratelimit_detect.go:14-18`,
  PAC-39). The backend serves no HTML pages (no non-test Go file contains
  `<html`/`<script`).
- **Frontend file server** (`docker/fileserver/main.go`). Serves the built SPA
  and falls back to `index.html` for unknown paths (`:29-42`). When
  `API_BASE_URL` is non-empty it **injects an inline script** into
  `index.html` (`:19-25`) to set `window.__API_BASE_URL__`. Production leaves
  it empty by default (`docker-compose.prod.yml:97`, `${API_BASE_URL:-}`); e2e
  sets it empty on purpose (`e2e/docker-compose.e2e.yml:193-197`). It sets no
  security headers.
- **The SPA** (smart-calendar-flow, `origin/main` `ad52160`). Origins it loads
  from or talks to, which a content policy must permit:
  - Google Fonts: stylesheet from `https://fonts.googleapis.com`, font files
    from `https://fonts.gstatic.com` (`index.html:18-20`).
  - Sentry: the browser SDK posts to the DSN's ingest host; the built-in
    default DSN's host is `o4512081160896512.ingest.de.sentry.io`
    (`src/lib/sentry.ts:19-20`). A build may override the DSN through
    `VITE_SENTRY_DSN`.
  - The API: same origin, relative `/api`, unless `API_BASE_URL` is set
    (`src/lib/api.ts:17`).
  - Host avatars: `<img src={avatar_url}>`
    (`src/components/links/HostAvatars.tsx:58`); the value comes from the
    identity provider's profile picture, i.e. an arbitrary `https:` URL.
  - Runtime-injected styles: the app uses `sonner` and `recharts`
    (`package.json:69-70`) and has 47 JSX `style=`/`dangerouslySetInnerHTML`
    sites in `src/`; inline style attributes and injected `<style>` elements
    are therefore in use.
  - No iframes, workers, `eval` or `blob:` usage were found in `src/`
    (excluding tests).

### 2.4 Other edge facts relevant to scope

- The MCP server publishes `3001:3001` directly, bypassing the proxy
  (`docker-compose.prod.yml:112`). It is the subject of a separate factory
  ticket (MCP SSE exposure) and is not touched here.
- The `docs` service exists in production (`docker-compose.prod.yml:101-103`)
  but has no proxy route; it is unreachable through the edge today.
- `docs/security.md:85` records the TLS/HSTS/CSP gap as "deferred by user".
  This ticket is the factory taking it up; no recorded owner decision limits
  how.

## 3. Desired behaviour

**In production**, the edge speaks HTTPS using the certificate the production
stack already mounts. Anyone arriving over plain HTTP is sent permanently to the
same host, path and query over HTTPS — with one exception: certificate-authority
domain-validation requests (the ACME HTTP-01 challenge path) are answered over
plain HTTP, never redirected, so certificates can be issued and renewed. Only
modern TLS versions are accepted.

**Every response the edge returns over HTTPS** — the SPA shell, its static
assets, API successes and API errors, and the edge's own error pages — tells the
browser:

- to use HTTPS only for this host for a stated period (HSTS);
- which origins may supply script, style, fonts, images and connections, and
  that the page may not be framed by anyone (a content policy that the current
  SPA, its Sentry reporting and its API calls all run under without a single
  violation);
- not to be framed (also via the legacy header, for older browsers);
- not to guess content types;
- to send only the origin, not the full URL, to other sites.

**In the local and e2e stacks**, nothing about how developers and the test suite
reach the app changes: plain HTTP on the same ports, no certificate required, no
redirect. Those stacks still route requests with exactly the same rules
production uses (not a copy that can drift), and they send the same content,
framing, sniffing and referrer headers production sends — so the e2e suite runs
the real SPA bundle under the production content policy and fails if that policy
breaks the app. They do not send HSTS (it has no meaning over plain HTTP and
must never be cached for `localhost`).

Routing is otherwise unchanged: the same two route prefixes reach the same two
upstreams with the same forwarded client-identity headers.

## 4. Acceptance criteria

Tags: `[config]` — proven by running the proxy's own configuration test in the
same proxy image production uses, with a throwaway self-signed certificate;
`[stack]` — proven by bringing up the production-shaped proxy (with that
throwaway certificate) in front of stub or real upstreams and making HTTP
requests to it; `[e2e]` — proven by the existing Playwright suite against the
e2e stack.

**TLS and redirect (production configuration)**

AC-1. Given the production edge configuration and a self-signed test certificate
placed where production mounts its certificates, when the proxy image's
configuration test is run, then it exits 0. Given the same configuration with
the certificate absent, it exits non-zero (proving the TLS listener really
depends on the mounted certificate).  `[config]`

AC-2. Given the local/e2e edge configuration and **no** certificate anywhere,
when the proxy image's configuration test is run, then it exits 0.  `[config]`

AC-3. Given the production edge running, when a client requests
`http://<host>/some/path?x=1` (any method, any path other than AC-4's), then the
response is `301` with `Location: https://<host>/some/path?x=1` (same host, path
and query) and no body is proxied to either upstream.  `[stack]`

AC-4. Given the production edge running and a file `T` present in the ACME
challenge directory, when a client requests
`http://<host>/.well-known/acme-challenge/T`, then the response is `200` with
that file's content and no redirect; and for a token that does not exist the
response is `404`, not `301`.  `[stack]`

AC-5. Given the production edge running, when a client completes a TLS 1.2 or
TLS 1.3 handshake on port 443, then it succeeds and the presented certificate is
the mounted one; when a client offers only TLS 1.0 or TLS 1.1, then the
handshake fails.  `[stack]`

AC-6. Given the production edge running, when a client requests over HTTPS
`/api/health` and `/`, then they return the backend's health response and the
SPA shell respectively — the same answers the plain-HTTP e2e stack gives today —
and the backend receives `X-Real-IP` equal to the client's address (the PAC-39
rate-limiter contract).  `[stack]`

**Security headers**

AC-7. Given the production edge running, when a client requests over HTTPS each
of: the SPA shell `/`, an unknown SPA route, a static asset, `/api/health`, an
API route that answers 401 or 404, and a path for which the edge itself produces
an error, then **every** response carries exactly one of each of
`Strict-Transport-Security`, `Content-Security-Policy`, `X-Frame-Options`,
`X-Content-Type-Options` and `Referrer-Policy`, with the values recorded in D-3
to D-7. (Where an upstream already sets the same header with the same value, a
duplicate identical value is acceptable; a conflicting value is a failure.)
`[stack]`

AC-8. Given either edge configuration, when a client receives any response over
**plain HTTP** (production's redirect and ACME answers; every local/e2e
response), then it carries no `Strict-Transport-Security` header.  `[stack]` /
`[e2e]`

AC-9. Given the e2e stack, when the SPA shell and an API response are fetched,
then they carry the `Content-Security-Policy`, `X-Frame-Options`,
`X-Content-Type-Options` and `Referrer-Policy` values recorded in D-4 to D-7 —
the same values production sends.  `[e2e]`

**The policy does not break the product**

AC-10. Given the e2e stack serving the production content policy, when the full
existing e2e suite runs, then it passes, and no `securitypolicyviolation` event
(or CSP console error) is observed on any page the suite visits. Today the suite
passes with no policy at all, so this fails if the policy blocks anything the
SPA does.  `[e2e]`

AC-11. Given the e2e stack, when page script issues a request to the Sentry
ingest host of the SPA's default DSN, then no content-policy violation is raised
for it (a network failure in the hermetic stack is acceptable; a policy block is
not).  `[e2e]`

AC-12. Given the e2e stack, when a page on a different origin tries to display
the app in a frame, then the browser refuses to render it.  `[e2e]`

**Process (from the ticket; not Given/When/Then)**

- The api gate and the e2e gate both exit 0 on the final commit of the
  implementation, with their output in the PR.

## 5. Explicitly out of scope

- **Certificate issuance and renewal.** This work answers the ACME challenge
  path from a directory and reads certificates from the mounted directory. It
  does not add a certbot (or other ACME client) service, cron, or reload hook;
  how certificates are obtained today is OQ-2.
- **Rate limiting at the edge** — the other half of `docs/security.md` risk #3.
  Separate concern, separate ticket.
- **The MCP server's directly published port** — separate factory ticket.
- **Routing the `docs` service** through the edge.
- **Backend awareness of TLS** (e.g. forwarding and trusting a protocol header).
  No backend behaviour changes here; PAC-50 already treats `Secure` as a
  deployment property.
- **HSTS preload** and `includeSubDomains` (D-3).
- **A cross-origin API deployment** (non-empty `API_BASE_URL`): the recorded
  policy supports the documented same-origin deployment only (D-8, OQ-3).
- **Moving security headers into the frontend file server or the backend.**
  The edge is the single place they are set.
- **HTTP/2, OCSP stapling, cipher-suite tuning beyond protocol versions,
  `Permissions-Policy`, COOP/COEP.** Reasonable hardening, not asked for, and
  each carries its own breakage risk.
- **Report-only rollout / CSP reporting endpoint.** The policy is enforced from
  the start (D-4); a reporting endpoint would need a backend change.

## 6. What must not change

- **Routing**: `/api/` → backend and `/` → frontend, with `Host`, `X-Real-IP`
  and (on `/api/`) `X-Forwarded-For` forwarded as today. Protected by the e2e
  suite (`e2e/global-setup.ts:61-63` polls `/api/health` through the proxy;
  every UI test needs `/`). **Finding:** nothing protects the forwarded headers
  except PAC-39's behaviour indirectly; AC-6 adds an explicit check.
- **The e2e suite still exercises the production routing rules, not a copy**
  (`e2e/docker-compose.e2e.yml:205-206`). If the change splits the
  configuration, the routing part must be shared by both, and a change to
  production routing must still be able to break the suite.
- **Local and e2e entrypoints** stay plain HTTP on their current ports
  (`80`, `127.0.0.1:8088`), with no certificate requirement and no redirect.
  Protected by the e2e gate itself.
- **Cookie behaviour**: unchanged (`Secure` in production, insecure only with
  `PACEDAY_INSECURE_COOKIES=true`). Protected by PAC-20's tests.
- **The backend's `nosniff` on `http.Error` responses**
  (`backend/api/handlers_audit_test.go:213-214`) — the edge must not strip or
  contradict it.
- **Production ports and mounts** already declared (`80`, `443`, the
  certificate directory) — this work uses them; it should not need to rename or
  move them. Any new mount (the ACME webroot) is additive.

## 7. Rollback

There is no migration and no data change. Rollback is reverting the edge
configuration and restarting the proxy container.

**One part does not roll back cleanly: HSTS.** Once a browser has seen the HSTS
header it will refuse plain HTTP for that host until `max-age` expires. So:

- Rolling back the **headers** (e.g. because the content policy breaks
  something in production that e2e did not catch) is safe at any time: revert
  the header set, keep the HTTPS listener and redirect.
- Rolling back **to plain HTTP only** is not safe while any browser holds the
  HSTS entry: those users cannot reach the site until it expires. This is the
  reason D-3 starts with a short `max-age`. The release note must state the
  value shipped and the date after which an HTTP-only rollback stops locking
  users out.
- If the content policy has to be loosened in an emergency, the fastest safe
  move is to remove the `Content-Security-Policy` header alone; the other four
  headers carry no breakage risk for this SPA.

## 8. Open questions

- **OQ-1 — Is there already a TLS terminator in front of this proxy in
  production?** Owner: product owner (operator). Production cookies are
  `Secure` (§2.3) and the proxy has no TLS, so either sign-in is broken in
  production today or something upstream (router, tunnel, CDN, host-level
  proxy) terminates TLS and forwards plain HTTP. If the latter, an unconditional
  HTTP→HTTPS redirect here loops forever. Resolve by the operator stating the
  production network path before release; the release-manager must check it
  before deploying (a `curl -I http://<host>` from outside showing which server
  answers settles it). If an upstream terminator exists, this spec needs a
  revision, not a workaround in the plan.
- **OQ-2 — How are certificates obtained and renewed today, and for which
  domain?** Owner: product owner (operator). The certificate path inside the
  mounted directory contains the domain name (the prod compose comments use
  `paceday.nhochart.ovh` as an example, `docker-compose.prod.yml:50`). The plan
  must not hard-code a domain the operator has not confirmed; resolve by the
  operator naming the domain and the ACME client (certbot webroot vs
  standalone, host vs container). If renewal uses standalone mode it needs
  port 80 free and conflicts with this proxy — that would also be a spec
  revision.
- **OQ-3 — Does production set `API_BASE_URL` to a separate origin?** Owner:
  product owner. If it does, the SPA's runtime config is an inline script
  (`docker/fileserver/main.go:22-25`) that `script-src 'self'` blocks, and the
  API origin is outside `connect-src`, so the SPA would break. D-8 assumes it
  does not. Resolve by the operator confirming; if it does, a follow-up must
  move the runtime config out of an inline script or the policy must name the
  origin and a hash.
- **OQ-4 — Does any production build override the Sentry DSN to a different
  ingest host?** Owner: frontend-implementer / operator. D-4 permits only the
  default DSN's host. A different host would silently stop frontend error
  reporting (the SDK fails open, `src/lib/sentry.ts` header comment). Resolve by
  checking the production build's `VITE_SENTRY_DSN`.
- **OQ-5 — Lovable edits the SPA directly.** Owner: product owner. A future
  Lovable change that adds a third-party origin (analytics, a new font CDN, an
  embed) will be blocked by the policy. AC-10 makes that fail in e2e rather than
  in production, but only for pages the suite visits. Should the frontend repo
  carry a note (or a check) naming the edge policy? Not resolved here.
- **OQ-6 — HSTS ramp.** Owner: product owner. D-3 ships one day. When, and on
  what evidence, does it go to one year? Suggested: after OQ-1/OQ-2 are settled
  and one week of clean production traffic.

## Decisions

*Assumed by the factory, pending owner confirmation (2026-10-27). Chosen as the
most conservative option consistent with PRODUCT.md and earlier owner decisions:
reversible, deletes no data, widens no visibility.*

- **D-1 — TLS is production-only; local and e2e stay plain HTTP and share the
  production routing rules.** The alternative — giving the e2e stack a
  self-signed certificate and running the suite over HTTPS — would change every
  test's base URL and certificate handling at once, for no extra coverage of the
  product. The TLS listener and redirect are proven by `[config]`/`[stack]`
  checks (AC-1 to AC-8) instead. Assumed by the factory, pending owner
  confirmation.
- **D-2 — Local/e2e send the same non-HSTS headers as production.** This is
  what makes "the policy must not break the SPA" falsifiable: the e2e suite
  already runs the real production bundle (`e2e/docker-compose.e2e.yml:183-197`),
  so serving it under the production policy turns the whole suite into the CSP
  regression test (AC-9, AC-10). HSTS is excluded (AC-8). Assumed by the
  factory, pending owner confirmation.
- **D-3 — HSTS value: `max-age=86400`** (one day), no `includeSubDomains`, no
  `preload`. Short on purpose: there is no staging environment
  (`.claude/agents/release-manager.md:7-9`), OQ-1 and OQ-2 are unanswered, and
  HSTS is the one part of this change that cannot be rolled back instantly (§7).
  `includeSubDomains` is omitted because the production host is itself a
  subdomain of a domain whose other hosts this project does not control. Raising
  it is OQ-6. Assumed by the factory, pending owner confirmation.
- **D-4 — Content policy (enforced, not report-only):**

  ```
  default-src 'self';
  script-src 'self';
  style-src 'self' 'unsafe-inline' https://fonts.googleapis.com;
  font-src 'self' https://fonts.gstatic.com data:;
  img-src 'self' data: https:;
  connect-src 'self' https://o4512081160896512.ingest.de.sentry.io;
  object-src 'none';
  base-uri 'self';
  form-action 'self';
  frame-ancestors 'none'
  ```

  (sent as one header line, directives separated by `; `). Reasons, directive
  by directive:
  - `script-src 'self'` — the production bundle loads only same-origin module
    scripts; no inline script exists in the default (same-origin) deployment
    (§2.3). No `'unsafe-inline'`, no `'unsafe-eval'`: that is the point of the
    header.
  - `style-src … 'unsafe-inline'` — required by inline style attributes and
    runtime-injected styles (§2.3). Inline *style* is a much smaller risk than
    inline script; removing it would require frontend changes outside scope.
  - `font-src`/`style-src` Google hosts — `index.html:18-20`. `data:` fonts are
    allowed because bundled CSS may inline small fonts.
  - `img-src https:` — avatars are arbitrary identity-provider URLs (§2.3).
    Loading an image does not expose user data to a new party beyond what the
    avatar link already does; it widens no visibility.
  - `connect-src` — same-origin API plus exactly the default DSN's Sentry
    ingest host (narrower than a `*.sentry.io` wildcard; see OQ-4).
  - `form-action 'self'`, `base-uri 'self'`, `object-src 'none'` — no feature
    uses cross-origin form posts, `<base>` or plugins; these close common
    injection follow-ups. AC-10 covers sign-in journeys in e2e to catch a
    form-action surprise.
  - `frame-ancestors 'none'` — the app is not designed to be embedded anywhere.
  - `upgrade-insecure-requests` is deliberately **not** included: it would
    rewrite same-origin subresource URLs to HTTPS on the plain-HTTP e2e stack
    and break it, and with the redirect and HSTS in place it adds nothing in
    production.

  Assumed by the factory, pending owner confirmation.
- **D-5 — `X-Frame-Options: DENY`**, in addition to `frame-ancestors 'none'`
  (older browsers ignore the latter). Assumed by the factory, pending owner
  confirmation.
- **D-6 — `X-Content-Type-Options: nosniff`.** Identical to what the backend
  already sends on `http.Error` responses, so a duplicate is harmless (AC-7).
  Assumed by the factory, pending owner confirmation.
- **D-7 — `Referrer-Policy: strict-origin-when-cross-origin`.** Booking and
  invite URLs carry bearer-like slugs and tokens in the path; this policy sends
  only the origin to other sites, never the path. `no-referrer` was considered
  and rejected as unnecessary breakage for same-origin analytics of the API.
  Assumed by the factory, pending owner confirmation.
- **D-8 — Same-origin deployment assumed.** The policy permits only the
  same-origin API. The documented production default is same-origin
  (`docker-compose.prod.yml:51-52,97`). A cross-origin deployment is OQ-3 and is
  out of scope. Assumed by the factory, pending owner confirmation.
- **D-9 — TLS protocol versions: 1.2 and 1.3 only** (AC-5). Every browser the
  SPA's build targets supports TLS 1.2. Assumed by the factory, pending owner
  confirmation.
- **D-10 — Headers are set at the edge only, on every response including
  errors.** One place to audit; covers API error responses and the edge's own
  error pages, which an upstream-only approach would miss. Assumed by the
  factory, pending owner confirmation.
