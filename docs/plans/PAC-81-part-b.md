# PAC-81 Part B — plan: nginx behind the TLS-terminating proxy

- **Spec**: `docs/specs/NEW-deploy-hardening.md` on
  `factory/deploy-hardening-spec` at `539316b` (revision 2; the rev-3 work in
  progress touches only Part A's AC-A19). Part B: §2 F-B1..F-B7, §4
  AC-B1..AC-B12, decisions D-1, D-8, D-9, D-16, F-1, §11 shared-file rule.
- **Owner decisions applied**: 2026-10-27 "HTTPS is out of scope" (HTTP only,
  never redirect, forwarding headers trusted only from a configured proxy,
  HSTS on the proxy, nginx sets CSP / X-Frame-Options / nosniff /
  Referrer-Policy). PAC-81 Q-O2 is still open; the factory default applies:
  **no framing anywhere** (`X-Frame-Options: DENY`, `frame-ancestors 'none'`).
- **Repo**: `Enach/clockwise-like` only. No contract change (nginx and the file
  server are not part of the OpenAPI boundary).

## 1. Design

### 1.1 How nginx gets its runtime settings (Q-P3)

`set_real_ip_from` takes one address per directive and the CSP's
`connect-src` depends on operator settings, so a static file cannot satisfy
AC-B4/B5/B7. The official image's `templates/` envsubst cannot either (one
directive per list item; no defaults for unset variables).

- `nginx/nginx.conf` stays the server configuration, still mounted as
  `conf.d/default.conf`. It holds the server block, the log format and the
  static maps. It references names that only the render step defines
  (`paceday_backend`, `paceday_frontend`, `$paceday_csp`,
  `$paceday_peer_trusted`), so loading it without the render step fails at
  start — fail closed, never "running without the headers".
- `nginx/paceday-render.sh` (POSIX sh, run as the container entrypoint with
  `/bin/sh`, so no executable bit is needed on a bind mount) validates the
  settings, writes `conf.d/00-paceday-runtime.conf`, then `exec`s the image's
  own `/docker-entrypoint.sh "$@"`. Any invalid value exits non-zero, so the
  container does not start.
- Settings (all optional):
  | Variable | Default | Effect |
  |---|---|---|
  | `NGINX_TRUSTED_PROXIES` | empty | Addresses/CIDRs whose `X-Forwarded-For` / `X-Forwarded-Proto` are believed (D-9). Empty: nothing trusted. |
  | `NGINX_CSP_SENTRY_ORIGIN` | `https://o4512081160896512.ingest.de.sentry.io` | The frontend's Sentry ingest origin in `connect-src`. Empty = default (AC-B4 "unset does not remove it"). Must match the frontend build's DSN. |
  | `NGINX_CSP_CONNECT_ORIGINS` | empty | Extra `connect-src` origins, comma or space separated. |
  | `API_BASE_URL` | empty | The same variable the frontend service reads. When it is an absolute URL its origin is added to `connect-src` (cross-origin API, AC-B4). |
  | `NGINX_BACKEND_UPSTREAM` / `NGINX_FRONTEND_UPSTREAM` | `backend:8080` / `frontend:80` | Upstream `host:port`. Needed so the e2e injection variant (AC-B3(b)) and the integration tests can point the shipped config at another upstream without a second config file. |
  Origins must be `http(s)://host[:port]` (no path, no wildcard, no quote or
  `;`), proxies must be IP/CIDR literals other than `/0`, upstreams
  `host:port`. Every raw value is refused first if it contains any control
  character (newline, CR, tab, ...): grep matches line by line, so a valid
  first line would otherwise carry the rest into the config (review of #245).
- The three compose files that run nginx (`docker-compose.yml`,
  `docker-compose.prod.yml`, `e2e/docker-compose.e2e.yml`) change only their
  nginx service: the render script mount, `entrypoint`/`command`, and the
  environment above (§6 of the spec: the e2e mount changes in the same PR).
  The rest of the compose work (443, certificates, versions, health checks)
  is Part C's and is not touched.
- `.gitattributes`: `nginx/** text eol=lf`, so a Windows `core.autocrlf`
  checkout does not hand `/bin/sh` a CRLF script.

### 1.2 Real client and scheme (AC-B5..B7, D-9)

- `set_real_ip_from` per trusted entry, `real_ip_header X-Forwarded-For`,
  `real_ip_recursive on`: right-most address not in the list. Emitted only
  when the list is non-empty.
- `geo $realip_remote_addr $paceday_peer_trusted` (the socket peer, before
  real-IP rewriting): 1 for a listed peer, else 0.
- `X-Real-IP $remote_addr` (the real client after rewriting);
  `X-Forwarded-For`: from a trusted peer, the chain as received plus that
  peer's own address (`$http_x_forwarded_for, $realip_remote_addr`; just the
  peer when it sent no chain), so every hop is recorded and the client appears
  once; otherwise just `$remote_addr` (a forged chain from an untrusted peer
  is dropped). *Review of #245:* `$proxy_add_x_forwarded_for` appended the
  client a second time after the real-IP rewrite. AC-B5's wording "whose last
  entry is C" assumed that; the client C is now the entry the proxy appended
  and the proxy P is last, which is the usual X-Forwarded-For convention. The
  backend never reads this header (spec §6); `X-Forwarded-Proto`: `https` only when a trusted
  peer said `https`, else `http`.
- Access log format `paceday`: real client, method, path **without the query
  string** (`$uri`, consistent with spec §6 for the backend log), status,
  bytes, request time, upstream time.

### 1.3 Headers and CSP (AC-B2..B4, D-8)

Set once, at server level, with `always` (so 4xx/5xx and nginx's own 413
carry them); no location sets `add_header`, so nothing is lost to nginx's
inheritance rule. The same five headers are removed from upstream responses
with `proxy_hide_header`, because the backend's `http.Error` already sets
`X-Content-Type-Options` and AC-B2 requires exactly one of each. HSTS is
hidden too: it is the proxy's (D-1).

CSP: `default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'
https://fonts.googleapis.com; font-src 'self' data: https://fonts.gstatic.com;
img-src 'self' data: https://*.googleusercontent.com; connect-src 'self'
<sentry> <api origin> <extras>; object-src 'none'; base-uri 'self';
form-action 'self'; frame-ancestors 'none'`.

### 1.4 API-base injection without inline script (Q-P2) — the file server

**Choice: a separately served script.** `docker/fileserver/main.go` serves
`/runtime-config.js` (`window.__API_BASE_URL__=<JSON string>;`) and, when
`API_BASE_URL` is set, injects `<script src="/runtime-config.js"></script>`
before `</head>` instead of an inline script.

Why not a hash computed at start: the CSP is set by nginx, a different
container, so nginx would have to recompute the same SHA-256 over byte-exact
script text from the same variable — two implementations that must agree on
every byte, and a mismatch is a broken site. A second CSP header from the file
server instead would be intersected with nginx's and break AC-B2's "exactly
one". A same-origin script is allowed by `script-src 'self'` with no coupling.
It is a classic (non-module) script in `<head>`, so it runs before the
module bundle, exactly as the inline script did; the web repo is unchanged.
The value is JSON-encoded (today it is concatenated raw into the HTML — a
quote or `</script>` in the setting breaks out). `Cache-Control: no-cache` on
both the HTML and the script so a changed setting is picked up.

Part A owns the file server's timeouts and health mode (§11); this change only
extracts the handler into a testable function and keeps `ListenAndServe` as
is, so Part A's edit and this one touch different lines.

### 1.5 Limits, keep-alive, health, docs (AC-B8, B9, B11, B12)

- `client_max_body_size 1m` (server). For `/api/`, `error_page 413` goes to a
  named location answering `{"error":"request body too large"}` with
  `application/json` (the backend's `writeError` shape).
- `proxy_read_timeout` / `proxy_send_timeout` 130 s (server level).
- `upstream` blocks with `keepalive 16`, `proxy_http_version 1.1` and an empty
  `Connection` header.
- `location = /nginx-health`: `return 200`, `access_log off`, no upstream.
- No docs location (D-16): `/docs/` falls through to `location /` (the SPA).

## 2. Tests (written before the implementation)

New Go module `nginx/test` (like `e2e/fake-google`), added to `make test`.
It needs Docker; it builds a stub binary (`nginx/test/stub`) for linux and runs
it in containers on a throwaway network with fixed addresses, so the client
address nginx sees is controlled. Containers and the network are removed in
`t.Cleanup`. The nginx image is `nginx:alpine` unless `PACEDAY_NGINX_IMAGE`
is set (Part C pins the version).

| AC | Test |
|---|---|
| B1 | `TestConfigHasNoTLSOrHTTPSRedirect` (static) + `TestNoRedirectToHTTPS` (`/`, `/api/health`, deep link, trusted `X-Forwarded-Proto: http`); e2e `edge.spec.ts` "no redirect" |
| B2 | `TestSecurityHeadersOnEveryResponse` (SPA page, asset, API JSON, API error, SPA fallback, nginx 413; upstream copies hidden; no HSTS); e2e `edge.spec.ts` "headers" |
| B3 | `TestCSPDirectives` (directive-level checks); e2e `csp.spec.ts` (googleusercontent avatar, injection variant, negative control) — **e2e not run here** |
| B4 | `TestCSPConnectSrc` (default, changed, empty, extras + API origin, invalid value refuses to start) |
| B5 | `TestTrustedProxyForwardsRealClient` |
| B6 | `TestUntrustedPeerIsTheClient` |
| B7 | `TestNoTrustedListByDefault` |
| B8 | `TestSlowUpstreamIsNotCut` (125 s), `TestOversizedAPIBodyIsJSON413` |
| B9 | `TestUpstreamKeepAlive` |
| B10 | `docs/security.md` checklist — review |
| B11 | `TestHealthPath` |
| B12 | `TestNoDocsRoute` |
| §1.4 | `docker/fileserver/main_test.go` (no inline script, served script, JSON escaping, unset = no tag, SPA fallback) |
| all | `TestConfigLoads` (`nginx -t` through the render script) |

e2e seed: two users and two links — one host with a
`https://lh3.googleusercontent.com/...` avatar (positive), one with an avatar on
a non-allowed origin (negative control). The e2e stack gains
`frontend-injected` + `nginx-injected` (same image, `API_BASE_URL` set,
`NGINX_FRONTEND_UPSTREAM=frontend-injected:80`) for AC-B3(b). The e2e suite is
the orchestrator's gate and is not run in this PR.

## 3. Rollback

Revert the commit. nginx returns to the static config (limits key on the proxy
again, headers gone), the file server returns to the inline script. No data
or schema is involved.

## 4. Known risk recorded for the gate

The web's Zod 4 probes `new Function("")` on the first object parse
(`zod/v4/core/util.js` `allowsEval`, unless `z.config({ jitless: true })`);
`src/lib/sentry.ts` parses a `z.object` at startup. Under `script-src 'self'`
Chromium reports that caught probe as a `script-src` violation, so AC-B3's
"zero violations" is expected to fail until the web sets `jitless`. That is a
web-repo change (factory README §1) and is raised in the PR, not worked around
by allowing `'unsafe-eval'`.

## 5. Found while implementing

- **F-B5 does not reproduce on today's `nginx:alpine` (nginx 1.31.8).** With
  the explicit `keepalive`, `proxy_http_version 1.1` and `Connection ""`
  all removed, the stub still saw one upstream connection for 20 requests:
  this nginx keeps upstream connections alive by default. The explicit
  settings stay, so the behaviour does not depend on which version Part C
  pins. `TestUpstreamKeepAlive` was shown to falsify a non-keep-alive edge
  by forcing `Connection: close` (20 connections, test fails).
- **Mutation checks run** (each restored afterwards): `frame-ancestors *`,
  no `proxy_hide_header X-Content-Type-Options`, forwarded scheme/chain
  believed from any peer, health path logged, no JSON `error_page 413`,
  `proxy_read_timeout 60s`. Each was caught by the test named for its AC.

### Spec deviation: `font-src` allows `data:` (D-8 lists `'self' https://fonts.gstatic.com`)

The reviewer of the web's Zod-jitless PR (Enach/smart-calendar-flow#139) loaded
the real web build under this CSP. Every page load reported a `font-src`
violation: FullCalendar injects its icon font `fcicons` as
`url("data:application/x-font-ttf;…")`, so without `data:` the calendar
icons are missing and the e2e zero-violation guard fails. `data:` only, no
wildcard. A `data:` font carries no script, and `img-src` already allows
`data:`.

The web build was grepped for every other `data:` / `blob:` use
(#139 head `d552113`, `vite build`):
- `font-src`: the `fcicons` font above is the only one.
- `connect-src`: `new Request("data:,")` in Sentry's fetch-support probe. It
  constructs a request and never fetches it, so no violation.
- `blob:`: appears only as a string in a URL-scheme list. The one
  `URL.createObjectURL` is the `.ics` download (`<a download>` click), a
  navigation/download that no fetch directive governs.
- None: workers, media, `data:` images in CSS (`img-src` allows `data:` anyway).

## 6. Merge order

The companion web PR that sets Zod's `jitless` mode (finding §4) must merge
before this PR: with this CSP enforced and Zod probing `new Function`, every
browser test fails the zero-violation guard.
