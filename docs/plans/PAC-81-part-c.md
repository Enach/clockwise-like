# PAC-81 Part C — plan: compose and images

- **Spec**: `docs/specs/NEW-deploy-hardening.md` on `main` at `48f6c60`
  (revision 3.2, amended here by revision 3.3). Part C: §2 F-C1..F-C8, F-B6,
  §4 AC-C1..AC-C9, decisions D-10..D-12, D-16, §11 (Part C after A and B; both
  merged, as is D).
- **Owner decisions applied (2026-11-01, binding)**:
  - **Q-O1 (b): MCP is reachable through the public site** — served by nginx
    on the public origin behind the TLS proxy, still behind `MCP_AUTH_TOKEN`.
    This supersedes the spec's default (a) "opt-in, loopback only" (D-12,
    AC-C4's "publishes only on `127.0.0.1`").
  - **Q-O2: only the public booking page may be framed.** That route gets
    `frame-ancestors *` and no `X-Frame-Options`; every other route keeps
    `frame-ancestors 'none'` and `X-Frame-Options: DENY`. This changes Part B's
    merged nginx config (D-8), so it ships here with tests.
  - Q-O3 (daily backup, 14 days) is Part D's, done. Q-O4: no outbox.
- **Repo**: `Enach/clockwise-like` only. No contract change: nginx, compose and
  the MCP SSE transport are outside the OpenAPI boundary.

## 1. Deviations from the spec (recorded in the spec as revision 3.3)

| Spec | Now | Why |
|---|---|---|
| D-12, AC-C4: MCP opt-in, published only on `127.0.0.1` | MCP opt-in (unchanged); when enabled it publishes **no port at all** and is reachable only through nginx at `/mcp/` on the public origin, behind `MCP_AUTH_TOKEN` | Q-O1 (b), owner 2026-11-01 |
| D-8, AC-B2/B3: `X-Frame-Options: DENY`, `frame-ancestors 'none'` on every response | The same everywhere **except** the public booking page `/book/{slug}`, which gets `frame-ancestors *` and no `X-Frame-Options` | Q-O2, owner 2026-11-01 |
| §5 "MCP over the public origin" out of scope; §10 follow-up | In scope (this part) | Q-O1 (b) |

Which route is "the public booking page": the web app's router
(`smart-calendar-flow` `origin/main` `src/App.tsx:102`) has exactly one public
booking route, `<Route path="/book/:slug" element={<PublicBooking />} />`.
nginx matches `^/book/[^/]+/?$` (one path segment, optional trailing slash).
The page's API calls (`/api/book/{slug}`, `/slots`, the POST) are `fetch`
calls from the framed document to its own origin: they are not documents, so
framing headers do not apply to them and they keep the default headers.
Assets and `/runtime-config.js` are subresources and need nothing either.

## 2. Design

### 2.1 Images (AC-C2, D-11)

- One required setting, **`PACEDAY_VERSION`**, the exact tag of the three
  Paceday images built from this repository (`enach/paceday-backend`,
  `enach/paceday-frontend` — built by `docker/frontend.dockerfile` here — and
  `enach/paceday-mcp`). Written `${PACEDAY_VERSION:?…}` in each `image:` line,
  so `docker compose config`/`up` stop with a message naming it. No default.
- nginx and Postgres are pinned to exact versions in the file:
  `nginx:1.31.8-alpine` (the version the floating `nginx:alpine` resolved to
  on 2026-11-01, so Part B's tested behaviour is unchanged) and
  `postgres:16.15-alpine` (same major as the data directory). Upgrading them
  is an edit of the compose file, reviewed like any change.
- `SENTRY_RELEASE` defaults to `PACEDAY_VERSION` (AC-C7).
- The nginx integration suite's default image becomes the pinned one (read
  from `docker-compose.prod.yml`), so the suite tests what ships.

### 2.2 Health checks and start order (AC-C3, D-10)

All checks call the binaries' health modes (Part A, AC-A16), since the images
are `FROM scratch`:

| Service | Test | Interval / timeout / retries | Start period |
|---|---|---|---|
| backend | `/server health` (asks `/api/ready`) | 10 s / 5 s / 3 | 120 s, `start_interval` 2 s |
| frontend | `/fileserver health` | 10 s / 5 s / 3 | 10 s |
| mcp-server | `/mcp-server health` | 10 s / 5 s / 3 | 10 s |
| nginx | `wget -q -O /dev/null http://127.0.0.1/nginx-health` | 10 s / 5 s / 3 | 10 s |
| postgres | `pg_isready` (unchanged) | 5 s / 5 s / 10 | — |

nginx `depends_on` backend and frontend with `condition: service_healthy`
(and mcp-server when enabled). The backend's 120 s start period covers
migrations, which run before the listener.

### 2.3 MCP opt-in through the public origin (AC-C4, AC-C5, Q-O1)

- **Mechanism: a second compose file, `docker-compose.mcp.yml`.** MCP is
  enabled by adding it: `docker compose -f docker-compose.prod.yml -f
  docker-compose.mcp.yml …` (or `COMPOSE_FILE=docker-compose.prod.yml:docker-compose.mcp.yml`
  in `.env`). `docker-compose.prod.yml` alone has no MCP service.
- In that file `MCP_AUTH_TOKEN: ${MCP_AUTH_TOKEN:?…}`. A required-variable
  expression in a file that is only read when MCP is enabled does what the
  rev-1 challenge measured a profile cannot: with MCP off nothing reads it;
  with MCP on and the token unset **or empty**, `config` and `up` stop before
  any container is created, naming `MCP_AUTH_TOKEN`, so there is no container
  to crash-loop.
- The MCP service publishes **no port**. The same file sets nginx's
  `NGINX_MCP_UPSTREAM=mcp-server:3001` and makes nginx wait for MCP to be
  healthy.
- nginx: `location /mcp/` proxies to the MCP server **without** rewriting the
  path, with `proxy_buffering off`, `proxy_cache off` and a 1 h
  `proxy_read_timeout` (an SSE stream is idle between events). All server-level
  headers (security headers, `X-Forwarded-*`, `Connection ""` for HTTP/1.1)
  apply unchanged; the `Authorization` header passes through; the access log
  records the path without the query string, so no session id is logged.
  With `NGINX_MCP_UPSTREAM` empty (MCP off), `/mcp/` answers
  `404 {"error":"not found"}` and never reaches the SPA.
- MCP server (`mcp/main.go`): a new optional `MCP_BASE_PATH`. When set
  (`/mcp` in `docker-compose.mcp.yml`) the SSE endpoint is
  `<base>/sse`, the message endpoint `<base>/message`, and the `endpoint`
  event carries the **relative** URL `/mcp/message?sessionId=…` instead of
  `http://0.0.0.0:3001/message…` (F-C3), so a client behind the public origin
  posts back to the same origin. Unset, the behaviour is unchanged. `/health`
  stays at the root and is not routed by nginx.
- The client URL is `https://<public host>/mcp/sse` with
  `Authorization: Bearer <MCP_AUTH_TOKEN>` (README).

### 2.4 Framing exception (Q-O2)

- `nginx.conf`: `map $uri $paceday_framable` (1 for `^/book/[^/]+/?$`), and
  `map $paceday_framable $paceday_frame_options` (`DENY`, or empty for the
  booking page — nginx sends no header whose value is empty).
- `paceday-render.sh` emits `map $paceday_framable $paceday_csp` with two
  policies identical except `frame-ancestors 'none'` / `frame-ancestors *`.
- Still only server-level `add_header … always` lines and no location-level
  `add_header`, so every response keeps exactly one of each header.

### 2.5 Grace period, logs, settings, ports (AC-C1, C6..C9)

- `stop_grace_period: 50s` (5 s notice + 30 s drain + 5 s cancel + 10 s) on
  backend, frontend, mcp-server and nginx; Postgres 60 s (a clean checkpoint).
- Every service: `logging: json-file`, `max-size: 10m`, `max-file: 5`.
- Backend environment gains exactly `APP_URL`, `BASE_URL`,
  `PACEDAY_ADMIN_EMAILS`, `SMTP_HOST/PORT/USER/PASS/FROM`, `SENTRY_DSN`,
  `SENTRY_ENVIRONMENT` (default `production`), `SENTRY_RELEASE` (default
  `PACEDAY_VERSION`), `PACEDAY_DB_MAX_OPEN_CONNS/MAX_IDLE_CONNS/CONN_MAX_LIFETIME/CONN_MAX_IDLE_TIME`
  (Part A introduced no shutdown settings: its bounds are fixed); loses
  `SESSION_SECRET`.
- No `443`, no certificate mount, no `TLS_CERT_DIR`; no `docs` service.
- nginx publishes `${NGINX_BIND_ADDRESS:+<addr>:}${NGINX_HTTP_PORT:-80}:80`:
  unset = all interfaces as today (IPv4 and IPv6); `127.0.0.1` for a proxy on
  the same host. `NGINX_HTTP_PORT` (default 80) is added because a same-host
  proxy usually holds port 80 itself.

## 3. AC → test

| AC | Test | Level |
|---|---|---|
| C1 | `backend/config/compose_prod_test.go` `TestProdComposeNoTLS` | unit |
| C2 | `TestProdComposeImagesPinned` (unit); `compose-test` `TestRenderRequiresVersion` (config without / with the setting, every image ref) | unit, integration |
| C3 | `TestProdComposeHealthChecks` (unit); `compose-test` `TestProductionStack` (all healthy, nginx after backend+frontend, readiness lost → unhealthy within 3 intervals), `TestSlowStartStillComesUp` (backend listening 90 s late, nginx not started meanwhile) | integration |
| C4 | `TestMCPOptInThroughNginx` (unit, incl. README); `TestRenderRequiresVersion` (no MCP: renders without the token), `TestProductionStack` (no MCP container, `/mcp/` 404), `TestMCPThroughNginx` (enabled: no published port, 401 without token, SSE handshake and an `initialize` round trip through nginx with it) | integration, unit |
| C5 | `TestMCPTokenRequired` (unset and empty: config and up fail naming `MCP_AUTH_TOKEN`, no MCP container created) + `TestMCPThroughNginx` (set: starts, stays healthy) | integration |
| C6 | `TestProdComposeGraceAndLogs` (unit); `TestProductionStack/stop during a request` (a request held 15 s by a table lock completes across `docker compose stop backend`; 15 s rather than 5 s so the test fails under Docker's default 10 s grace) | unit, integration |
| C7 | `TestProdComposeBackendEnv` | unit |
| C8 | `TestProdComposeNoDocs` | unit |
| C9 | `TestProdComposeNginxBind` (unit) + `TestRenderRequiresVersion` (rendered ports for unset / `127.0.0.1`) | unit, integration |
| Q-O2 | `nginx/test` `TestFramingOnlyBookingPage` (headers on `/book/x`, `/book/x/`, `/app`, `/`, `/api/book/x`, `/book`, `/book/x/y`), `TestCSPDirectives` per route; e2e `e2e/tests/framing.spec.ts` (a page on another origin frames `/book/<slug>` but not `/app`) | integration, e2e |
| Q-O1 | `nginx/test` `TestMCPRouteDisabledByDefault` (404 JSON, upstream never called), `TestMCPRouteProxiedAndStreamed` (proxied unrewritten, Authorization and client identity forwarded, query not logged, first event delivered at once), `TestMCPStreamIdleLongerThanAPIBound` (directives; a stream idle 135 s survives), `TestMCPUpstreamSettingIsValidated`; `mcp` `TestSSEBasePath`, `TestSSEBasePathFromEnv`, `TestSSENoBasePathUnchanged` | integration, unit |

The compose integration suite (`compose-test/`, Go, Docker Compose v2) builds
the backend and MCP images from this checkout and a frontend test image from
`docker/fileserver` with a placeholder page (the real SPA build needs the web
repository; the health mode is the file server's, unchanged), tags them with
a throwaway version, and runs each scenario as its own compose project with
nginx bound to `127.0.0.1` on a port from 18500–18599. Everything it creates
carries a run label and is removed at the end.

## 4. Docs

`docs/security.md` (B1/B2/B5 rows, §2 diagram, §6 risk 1), `docs/operator/proxy.md`
(§1 framing, §4 SSE timeout for `/mcp/`, §5 bind settings and MCP), `docs/runbook.md`
(version setting, health checks, grace period, MCP enablement, docs service),
`README.md` MCP section, `.env.example` (new settings, Part C markers removed).

## 5. Rollback

Revert the PR. The compose files return to floating tags (record the running
image digests first), nginx to framing denied everywhere. No data changes.

## 6. Not done here

- An image pipeline (spec F-C1: none in the repo) — `PACEDAY_VERSION` names
  whatever tag the operator publishes.
- `docker-compose.yml` (development) keeps its `docs` and MCP services; the
  spec's Part C is the production file.
- MCP → backend still carries no user authentication (spec §5, out of scope).

## 7. Found while implementing

- The streaming check (`TestMCPRouteProxiedAndStreamed`) also passes with
  `proxy_buffering on` in nginx 1.31.8 for this small event stream, so it
  cannot by itself prove the directive matters; `TestMCPStreamIdleLongerThanAPIBound`
  asserts the directives in the `/mcp/` location instead, and its 135 s idle
  stream does discriminate the 1 h read timeout from the 130 s default.
- "A stopped backend turns unhealthy within 3 intervals" is tested as a
  backend that cannot be ready (its database stopped): a stopped container
  has no health state. Measured 34 s at a 10 s interval (three failed probes
  plus probe time); the test allows 3 intervals + 10 s.
- AC-C6's 5 s request is a 15 s one here (a table lock), so the test fails
  under Docker's default 10 s grace; a control with `stop -t 3` gets nginx's
  502.
- `docker-compose.yml` (development) still passed `SESSION_SECRET`, which
  nothing reads; removed so `.env.example` can drop the deprecated entry.
