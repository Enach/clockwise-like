# Spec — PAC-81 (NEW-deploy-hardening): the production stack is not ready to run behind a TLS-terminating proxy

- **Linear**: PAC-81, https://linear.app/paceday/issue/PAC-81 (factory ticket
  id `NEW-deploy-hardening`; the file keeps that name). Nothing was posted to
  Linear by this stage.
- **Status**: challenged (accept-with-changes, rev 0); **revision 1,
  2026-10-28** — see "Revision 1" at the end.
- **Author**: spec-author (rev 0 and rev 1, 2026-10-28)
- **Surveyed at**: `Enach/clockwise-like` `origin/main` = `67b7966` (rechecked
  for rev 1: unchanged); `Enach/smart-calendar-flow` `origin/main` = `9ceff3b`.
  PAC-71 is open as PR #242 (`origin/impl/pac-71-identity-binding-backend`);
  its effects are taken from the challenge (Non-blocking 1), not re-read.
- **What was run to write this (factory README §3 rule 2)**: `git show`,
  `git grep`, `grep`, `sed`, `wc` on the two checkouts. **No test, build,
  `make verify`, container or nginx was run.** Every claim about behaviour is
  read from source; none is observed at runtime. Claims that depend on Go,
  nginx, Docker, Postgres, Starlight or browser defaults are marked
  *(default)*: they are documented tool behaviour, not measurements.
- **Inherited scope**:
  - Owner decision 2026-10-27, "HTTPS is out of scope"
    (`.claude/factory/owner-decisions.md`): binding, implemented here in full.
  - Owner decision 2026-10-27, "Booking rate limits are abuse protection":
    implemented by the NEW-booking-rate-limit follow-up, not here. Part B
    makes the address those limits key on correct; Part D's environment
    reference describes the settings once they exist.
  - `docs/security.md:52` (B1), `:53` (B2, "only nginx exposes ports" —
    false, F-C3), `:56` (B5, "HTTP only on loopback", token "optional" —
    false, F-C3/F-C4), `:85` (finding 3). AC-B10 fixes all four.
  - `docs/contributing.md:26` links `docs/runbook.md`, which does not exist.
  - PAC-14 contract §2 (`contracts/features/PAC-14.md:53-58`) makes the
    hard-coded Sentry DSN the documented default. AC-A14 removes that default,
    so Part A goes through stage 2 to amend PAC-14 §2 (factory decision F-8).
  - Readiness (AC-A9) is a new HTTP operation: stage 2 for Part A.
  - `/api/health` consumers that must keep working: `e2e/global-setup.ts:61-70`
    (polls it through nginx before any test) and
    `smart-calendar-flow:src/lib/api.ts:28-39` (`checkApiAvailability`, 2 s,
    `res.ok`, decides the web's preview mode). See §6.

## 1. Problem

Paceday is meant to be deployed with `docker-compose.prod.yml` behind a load
balancer or reverse proxy that terminates TLS (owner, 2026-10-27). Read as it
is on `main`, that deployment has five classes of defect:

1. **The backend cannot tell clients apart.** Behind an external proxy every
   request reaches nginx from the proxy's address, nginx forwards that address
   as the client, and the backend's abuse limits (sign-in detection, public
   booking, slot listing) count every visitor in the world as one client.
   Twenty bookings in ten minutes, from anyone, would lock out every booker of
   every host.
2. **The server process is fragile.** It has no read or write timeouts and
   several outbound calls with no time limit, so a slow client or a slow
   provider can hold server resources indefinitely. It has no graceful
   shutdown, so every deploy cuts in-flight requests, background jobs and the
   booking confirmation email mid-flight, silently. Its database pool is
   unbounded while Postgres accepts 100 connections *(default)*.
3. **The edge is half-configured.** The compose file publishes port 443 and
   mounts certificates for a TLS server that the bundled nginx config does not
   have; nginx sets none of the browser security headers; its implicit limits
   (60 s read, 1 MiB body with an HTML error page) are accidental.
4. **The deployment is not reproducible or observable.** Every Paceday image is
   `:latest`, so "roll back" has no meaning; no service but Postgres has a
   health check; production Sentry events go to a hard-coded project tagged
   `development`; the MCP server is published on a plain-HTTP port and
   crash-loops when its token is unset although the docs call it optional.
5. **Operators have no runbook.** No backup, no restore procedure, no deploy
   order, no complete list of settings; five migrations have no down file.

Who pays: every booker and user (shared abuse budget, requests and
confirmation emails cut at deploy), hosts (whose calendars the abuse limits
protect), and the operator (no rollback, no backups, no health signal).

## 2. Current behaviour

Findings are numbered `F-<part><n>`; every AC in §4 names the findings it
closes. `F-W*` are web-repo findings that this ticket records but does not
fix (§10).

### Part A — backend process (`Enach/clockwise-like`)

- **F-A1. No server timeouts.** The backend serves with
  `http.ListenAndServe(":"+port, r)` (`backend/main.go:215`): no
  `ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout` or `IdleTimeout`. The MCP
  SSE server (`mcp/main.go:40`) and the static file server
  (`docker/fileserver/main.go:47`) do the same.
- **F-A2. No graceful shutdown.** No `signal.Notify`, `signal.NotifyContext`
  or `Server.Shutdown` anywhere in `backend/`, `mcp/` or `docker/`. SIGTERM
  ends the process at once *(Go default)*. When `ListenAndServe` returns,
  `log.Fatalf` (`backend/main.go:216`) calls `os.Exit`, which skips every
  `defer`: `sentry.Flush` (`:29`), `db.Close` (`:112`), the cron `Stop`s
  (`:123`, `:149`, `:167`, `:180`, `:202`).
- **F-A3. Cron shutdown does not wait.** Four `robfig/cron` jobs
  (`backend/main.go:129`, `:155`, `:171`, `:184`) have their `Stop()` deferred
  without waiting on the returned context. The focus scheduler's `Stop` does
  not wait for a tick in progress (`backend/scheduler/cron.go:83-93`). Job
  budgets: personal blocker 5 min per user (`backend/main.go:138`),
  auto-decline 5 min (`:157`), recap 55 s (`:173`), manager detection 10 min
  (`:186`), focus tick 10 min (`backend/scheduler/cron.go:59`). The recap job
  returns after launching one goroutine per user
  (`backend/engine/daily_recap.go:130`, 1 s stagger at `:131`), so even a
  waited cron would not wait for those goroutines.
- **F-A3b. Detached work started by handlers.** These outlive the request on
  `context.WithoutCancel` or `context.Background()` and nothing tracks them:
  analytics recompute (`backend/api/handlers_analytics.go:93-94`), habit
  re-optimisation (`backend/api/handlers_habits.go:137-138`, `:239-240`,
  `:382-383`), manager detection (`backend/api/handlers_manager.go:97-98`, own
  30 s), the team-invite email (`backend/api/handlers_teams.go:214`) and the
  **booking confirmation email** (`backend/engine/booking.go:277`). Both
  emails use `smtp.SendMail`, which has no timeout *(stdlib)*, and discard its
  error (`_ =` at `backend/api/handlers_teams.go:650`,
  `backend/engine/booking.go:330`): a failed confirmation email is not logged
  today, let alone one cut by a restart.
- **F-A4. Unbounded database pool.** `storage.Open` calls `sql.Open` and
  `Ping` only (`backend/storage/db.go:17-28`); no pool setting anywhere.
  Postgres `max_connections` = 100, 3 reserved for superusers *(default)*;
  the prod compose does not change it (`docker-compose.prod.yml:11-24`).
  Background fan-out shares the same pool: recap (one goroutine per user per
  minute, F-A3), focus ticks (per user), and PAC-71 adds one uncached read per
  authenticated request plus a transaction around guarded writes
  (`lock_timeout = '10s'`) and an operator binary with its own pool
  (challenge Non-blocking 1).
- **F-A5. Long requests and unbounded outbound calls.**
  - Bounded handler deadlines today: meeting-brief regeneration 60 s
    (`backend/api/handlers_meeting_briefs.go:80`, route `routes.go:200`; no
    caller in the web or `mcp/` — challenge Blocking 3), daily recap 10 s
    (`handlers_daily_recap.go:172`), manager 10 s (`handlers_manager.go:119`).
  - Natural-language parse calls the user's LLM with the request context only
    (`backend/api/handlers_nlp.go:28` → `backend/nlp/parser.go:166`), up to two
    calls (`parser.go:239`); the LLM clients set no `Timeout`
    (`backend/nlp/user_destination.go:145-148`,
    `backend/nlp/destination.go:202-205`).
  - Slack and Notion OAuth token exchanges run on `context.Background()` with
    `http.DefaultClient` (`backend/api/handlers_integrations.go:124,130`,
    `:254,261`).
  - Personal-calendar sync (`backend/api/handlers_personal.go:191`) fetches an
    ICS with `http.DefaultClient`, no time or size limit
    (`backend/calendar/webcal_client.go:60-70`), then makes one provider create
    per work-hours event over 14 days
    (`backend/engine/personal_blocker.go:19`, `:116-135`).
  - Outlook, Zoom, Teams and the recap's Slack calls use `http.DefaultClient`
    with no overall timeout (`backend/calendar/outlook_client.go:56`,
    `backend/conference/zoom.go:50`, `backend/conference/teams.go:59`,
    `backend/engine/daily_recap.go:472`, `:501`). The Google calendar client
    has a 30 s dial timeout (`backend/calendar/guard.go:52`) but no overall
    timeout (`backend/calendar/transport.go:93-98`). Free/busy queries are
    capped at 5 s (`backend/engine/freebusy_service.go:133`).
  - Schedule suggest (`routes.go:93`) and team availability (`routes.go:240`)
    call the engine with free/busy, bounded per query by the 5 s above.
  - The 60-day slot listing (`backend/api/handlers_booking.go:176-188`) is never
    called by the web, which always sends a date
    (`smart-calendar-flow:src/api/schedulingLinks.ts:690-692`); it is an abuse
    case, not legitimate load. Its loop `continue`s on error (`:181-183`), so a
    cancelled context today yields a truncated **200**.
- **F-A6. No request body cap in the backend** (no `http.MaxBytesReader` in
  `backend/`). nginx's implicit 1 MiB *(default)* is the only cap, and its 413
  is an HTML page *(default)* that the web client cannot parse as JSON. No
  upload endpoint exists.
- **F-A7. Health is liveness only.** `GET /api/health` (`routes.go:21`) returns
  a constant (`backend/api/handlers_health.go:8-14`). No readiness signal. No
  binary has a health mode for a container check; only the e2e stand-ins do
  (`e2e/docker-compose.e2e.yml:179`, `:199`).
- **F-A8. Access log lacks status and client** (`backend/api/middleware.go:105-111`;
  acknowledged at `docs/security.md:53`). The path is logged without the query
  string, which is correct. PAC-71's `guardedResponseWriter` replaces the
  handler's status (challenge Non-blocking 1), so the logged status must be the
  one actually sent.
- **F-A9. Client address and scheme today.** Client = socket peer, or
  `X-Real-IP` when the peer is in `TRUSTED_PROXIES`; `X-Forwarded-For` never
  read (`backend/api/ratelimit_detect.go:12-18`, `:33`, `:94-114`; shared by
  `ratelimit_booking.go:15-18`). The backend never reads a scheme from the
  request. `Secure` is a static setting (`backend/config/cookies.go:19-29`,
  `backend/api/cookies.go:9-21`). Absolute URLs come only from settings:
  `FRONTEND_URL` (`backend/main.go:209`; `handlers_auth.go:83,98`), `APP_URL`
  (`handlers_sso.go:67,124`; `handlers_sso_stepup.go:174`), `BASE_URL` with a
  hard-coded fallback `https://paceday.app` (`handlers_teams.go:627-636`).
  Nothing warns when an `http://` public URL is paired with Secure cookies,
  which makes browsers drop the session cookie on a non-localhost host
  *(browser default)*.
- **F-A10. Sentry is on by default, pointed at a fixed project.**
  `initSentry` uses `cmp.Or(os.Getenv("SENTRY_DSN"), defaultBackendDSN)` and
  environment `development` by default (`backend/sentry.go:16`, `:106-108`), so
  an empty `SENTRY_DSN` still sends; `.env.example:118` ("Leave empty to
  disable monitoring") is false; PAC-14 §2 (`contracts/features/PAC-14.md:53-58`)
  mandates that default. The prod compose passes no `SENTRY_*`
  (`docker-compose.prod.yml:33-98`).
- **F-A11. Avatar origins actually stored.** `users.avatar_url` is written only
  at sign-in: Google stores the userinfo `picture` (`handlers_auth.go:64`,
  field at `:239`), which Google serves from `lh3.googleusercontent.com` (and
  sibling `lhN` hosts) *(Google behaviour, not measured)*; Microsoft and SSO
  store `""` (`handlers_microsoft_auth.go:69`, `handlers_sso.go:155`). The web
  renders it as `<img src={h.avatar_url}>`
  (`smart-calendar-flow:src/components/links/HostAvatars.tsx:58`, public
  booking page included). The e2e seed sets every avatar to `''`
  (`e2e/seed/seed.sql:70-78`).

### Part B — nginx edge

- **F-B1. HTTP only already, but compose expects TLS.** One `listen 80` server,
  no redirect (`nginx/nginx.conf:2-3`); the prod compose publishes `443:443`
  and mounts `${TLS_CERT_DIR}` at `/etc/letsencrypt`
  (`docker-compose.prod.yml:134-140`); `.env.example:112-114` documents it.
  The same file is mounted by the base compose (`docker-compose.yml:93-98`) and
  the e2e stack, directly as `conf.d/default.conf`
  (`e2e/docker-compose.e2e.yml:229`).
- **F-B2. No security headers** (`nginx/nginx.conf:1-17`; nor the backend
  middlewares, `routes.go:17-19`; nor the file server,
  `docker/fileserver/main.go:29-45`). What a CSP must allow:
  Google Fonts (`smart-calendar-flow:index.html:18-20`), Sentry ingest
  (`smart-calendar-flow:src/lib/sentry.ts:19-20`), a cross-origin API when one
  is configured (`smart-calendar-flow:src/lib/api.ts:16-22`), the inline
  `<script>` the file server injects when `API_BASE_URL` is set
  (`docker/fileserver/main.go:19-25`; the e2e stack sets it empty,
  `e2e/docker-compose.e2e.yml:219`, so no injection is exercised today), the
  chart component's inline `<style>`
  (`smart-calendar-flow:src/components/ui/chart.tsx`), and Google avatar
  images (F-A11).
- **F-B3. The real client address is lost behind an external proxy.** nginx
  sets `X-Real-IP $remote_addr` (`nginx/nginx.conf:8`, `:15`) with no
  `real_ip` configuration; the backend trusts nginx
  (`docker-compose.prod.yml:47`), so every abuse limit keys on the load
  balancer. nginx appends `X-Forwarded-For` for `/api/` only (`:9`) and sends no
  `X-Forwarded-Proto`.
- **F-B4. Proxy timeouts and limits are implicit** (`nginx/nginx.conf:1-17`):
  `proxy_read_timeout` 60 s, `client_max_body_size` 1 MiB with an HTML 413
  *(defaults)*; no `limit_req`/`limit_conn`.
- **F-B5. No upstream keep-alive** (`proxy_http_version` unset on `/api/`,
  `nginx/nginx.conf:5-10`; HTTP/1.0 upstream *(default)*).
- **F-B6. The docs container is unreachable, and could not be routed safely.**
  The prod compose runs `docs` (`docker-compose.prod.yml:108-110`); nginx has
  no route to it (`nginx/nginx.conf:5-16`). The docs are Starlight
  (`docs/package.json:13`, `docs/astro.config.mjs:3,8`), whose theme, search
  and sidebar components ship inline scripts and whose search uses Pagefind
  (WebAssembly) (challenge Blocking 2, seen in the installed package). The
  docs are already built for Vercel (`docs/vercel.json:1-6`).
- **F-B7. nginx has no health path** of its own.

### Part C — compose, images, exposure

- **F-C1. Floating image tags**: `enach/paceday-{backend,frontend,docs,mcp}:latest`
  (`docker-compose.prod.yml:28`, `:102`, `:109`, `:114`), `nginx:alpine`
  (`:129`), `postgres:16-alpine` floats within 16 (`:12`). The base compose
  also defaults the frontend to `latest` (`${FRONTEND_IMAGE_TAG:-latest}`,
  `docker-compose.yml:71`). No image pipeline is in the repo (`.github/`
  absent; `docs/adr/0001-tech-stack.md:17` refers to one).
- **F-C2. Health checks**: only Postgres (`docker-compose.prod.yml:20-24`). The
  Paceday images are `FROM scratch` (`backend/Dockerfile:9-14`,
  `docker/frontend.dockerfile:18-22`, `docs/Dockerfile:15-19`,
  `mcp/Dockerfile:10-15`): a check must call the service binary. Migrations run
  before the listener starts (`backend/storage/db.go:25`, `backend/main.go:108`
  before `:215`). nginx `depends_on` without health conditions (`:131-133`).
- **F-C3. MCP exposure.** Published on every interface over plain HTTP
  (`docker-compose.prod.yml:118-119`; `docker-compose.yml:87-88`); advertises
  `http://0.0.0.0:<port>` (`mcp/main.go:38`). `README.md:56-63` documents
  `http://localhost:3001/sse` with no bearer header. `docs/security.md:53`
  ("only nginx exposes ports") and `:56` ("HTTP only on loopback", token
  "optional") are false. The MCP client sends no `Authorization` to the
  backend (`mcp/client.go:77`) — out of scope, and why loopback-only loses
  nothing today.
- **F-C4. MCP token contradiction.** SSE mode refuses to start without a token
  (`mcp/main.go:29-31`); compose defaults it empty and calls it optional
  (`docker-compose.prod.yml:124-125`; `.env.example:109-110`); with
  `restart: unless-stopped` (`:115`) it crash-loops.
- **F-C5. No stop grace period, no log rotation** in `docker-compose.prod.yml`
  (SIGKILL 10 s after SIGTERM; unbounded json-file logs *(defaults)*).
- **F-C6. Stale and missing settings.** `SESSION_SECRET` required
  (`docker-compose.prod.yml:59-60`, `.env.example:7`) but unread. `APP_URL`,
  `BASE_URL`, `PACEDAY_ADMIN_EMAILS`, `SMTP_*`, `SENTRY_*` are read but not
  passed (`docker-compose.prod.yml:33-98`).
- **F-C7. Restart policies** are `unless-stopped` everywhere; adequate,
  unchanged.
- **F-C8. Ports.** Postgres and backend unpublished (good); nginx publishes
  `80` on every interface (`:135`), so a host-reachable client bypasses the
  proxy. When the TLS proxy runs **on the same host** and connects to the
  published port, Docker's userland proxy presents the bridge gateway address,
  not `127.0.0.1`, as the peer *(Docker default)*.

### Part D — operations

- **F-D1. Settings read vs documented.** Read by `backend/` and `mcp/`
  (`git grep -E 'os\.(Getenv|LookupEnv)'` plus `config.Env*` and
  `EnvAdminEmails`): `ALLOWED_ORIGIN`, `APP_URL`, `BACKEND_URL`, `BASE_URL`,
  `DATABASE_URL`, `FRONTEND_URL`, `GOOGLE_CALENDAR_API_BASE_URL`,
  `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, `GOOGLE_REDIRECT_URL`,
  `JWT_SECRET`, `MCP_AUTH_TOKEN`, `MCP_PORT`, `MCP_TRANSPORT`,
  `MICROSOFT_CLIENT_ID`, `MICROSOFT_CLIENT_SECRET`, `MICROSOFT_REDIRECT_URL`,
  `NOTION_CLIENT_ID`, `NOTION_CLIENT_SECRET`, `NOTION_REDIRECT_URI`,
  `PACEDAY_ADMIN_EMAILS`, `PACEDAY_ALLOW_STANDINS`,
  `PACEDAY_GOOGLE_IDP_BASE_URL`, `PACEDAY_INSECURE_COOKIES`,
  `PACEDAY_LLM_APPROVED_HOSTS_{AZURE_OPENAI,VERTEX,BEDROCK,OPENAI,ANTHROPIC}`,
  `PACEDAY_LLM_APPROVED_ORIGINS_OLLAMA` (retired, read only to warn),
  `PACEDAY_MICROSOFT_IDP_BASE_URL`, `PACEDAY_PUBLIC_BOOKING_LIMIT`,
  `PACEDAY_PUBLIC_SLOTS_LIMIT`, `PORT`, `SENTRY_DSN`, `SENTRY_ENVIRONMENT`,
  `SENTRY_RELEASE`, `SLACK_CLIENT_ID`, `SLACK_CLIENT_SECRET`,
  `SLACK_REDIRECT_URI`, `SMTP_FROM`, `SMTP_HOST`, `SMTP_PASS`, `SMTP_PORT`,
  `SMTP_USER`, `TRUSTED_PROXIES`, `ZOOM_CLIENT_ID`, `ZOOM_CLIENT_SECRET`,
  `ZOOM_REDIRECT_URL`; `API_BASE_URL` (`docker/fileserver/main.go:22`);
  `PACEDAY_TEST_DATABASE_URL` (tests, `backend/internal/testdb/testdb.go:20`).
  Absent from `.env.example`: `ALLOWED_ORIGIN`, `APP_URL`, `BASE_URL`,
  `FRONTEND_URL`, `PACEDAY_ADMIN_EMAILS`, the two IdP base URLs, the two
  booking-limit counts, `SMTP_*`, `MCP_TRANSPORT`, `MCP_PORT`, `BACKEND_URL`,
  `PORT`, `DATABASE_URL`, `API_BASE_URL`. Present but unread: `SESSION_SECRET`
  (`:7`), `TLS_CERT_DIR` (`:114`). `.env.example:121` ships the real DSN.
- **F-D2. Migrations.** Run automatically, up only (`backend/storage/db.go:25`,
  `:44`). No down file for `006_auth`, `007_organizations`,
  `014_workspace_connections`, `015_daily_recap`, `016_manager`. `006`'s up
  creates `users` and adds `user_id` to `oauth_tokens`, `settings`,
  `focus_blocks`, `personal_calendars` (`006_auth.up.sql:3-20`). Migrating down
  to 005 runs every later down file (017…031, plus PAC-71's 032 if merged);
  none has been exercised by a test that this survey found.
- **F-D3. Backups.** None in the repo. Data lives in the named volume
  `postgres_data` (`docker-compose.prod.yml:18-19`, `:142-143`). The factory
  release gate requires a backed-up database (factory README §2, stage 7).
- **F-D4. No runbook or deploy order** (`docs/contributing.md:26` → missing
  `docs/runbook.md`; `docker-compose.prod.yml:3-6` is the only instruction).
  PAC-71 adds `docs/operator/PAC-71-account-recovery.md` (on its branch).
- **F-D5. A dump is not a working restore, and it is a secret.** Sessions are
  signed with `JWT_SECRET` (`backend/api/middleware.go:57`; the SSO step-up
  state key derives from it, `handlers_sso_stepup.go:142`); OAuth client
  secrets, `POSTGRES_PASSWORD` and `MCP_AUTH_TOKEN` live in `.env`. User LLM
  API keys and provider tokens are stored in plain columns (e.g.
  `029_llm_bring_your_own.up.sql:16-18`; no encryption code found in
  `backend/`), so a dump contains every user's provider credentials.

### Web repo (recorded, not fixed here — §10)

- **F-W1. The web aborts every API call after 4 s and switches to preview
  data.** `NETWORK_TIMEOUT_MS = 4000` (`smart-calendar-flow:src/api/client.ts:318`)
  aborts each `requestApi` call (`:590-591`) and treats it as unreachable
  (comment at `:1230`). Natural-language parse, personal-calendar sync and
  schedule suggest all go through it. Whatever the server's bounds, a browser
  call longer than 4 s ends in preview data. The server-side ladder in this
  spec therefore protects **server resources**; it does not make long calls
  succeed in the browser.
- **F-W2. The web's Sentry DSN falls back to a hard-coded value**
  (`smart-calendar-flow:src/lib/sentry.ts:55`), like F-A10.

## 3. Desired behaviour

After this work an operator can put the stack behind any TLS-terminating proxy
and get correct behaviour without editing code:

- The backend sees each visitor's real address, so abuse protection counts per
  visitor. Forwarding headers are believed only from proxy addresses the
  operator configured; nothing is trusted by default.
- The stack speaks plain HTTP only and never redirects to HTTPS; TLS and HSTS
  are the proxy's job and the documentation says so. Every response through
  nginx carries the non-TLS browser security headers, and the app works under
  them, including users' Google profile pictures.
- No server resource is held indefinitely: every outbound call has its own time
  limit, every request has a server-side budget longer than any legitimate
  piece of work inside it, and the outer bounds (write, nginx, proxy) are each
  longer than the one inside, so the backend's own error ends a too-long
  request. (This protects the server; it does not change what the browser does
  — F-W1.)
- A deploy marks the backend unready first, keeps serving for a short notice
  period, then stops accepting connections and lets in-flight requests,
  background jobs and outgoing emails finish within a stated budget. Work
  that cannot finish is cancelled and logged by name, never lost silently;
  the database pool closes only after that.
- The database pool never asks Postgres for more connections than it can give,
  even with an old and a new backend during a deploy, and no request path
  needs two pooled connections at once.
- Monitoring is off until the operator configures it, and then carries the
  right environment and release.
- Every service reports whether it is alive; the backend reports whether it is
  ready. Image versions are exact, so a rollback is "deploy the previous
  version". The MCP server is opt-in and reachable only from the host.
- One document lists every setting; one rehearsed procedure backs up and
  restores the data together with the secrets it needs; one deploy order says
  what to do when a deploy fails.

### Values (factory-assumed unless marked; justified in Decisions D-4..D-7)

| Bound | Value |
|---|---|
| Outbound: LLM call (each) | 30 s |
| Outbound: calendar, conferencing, Slack, Notion API call (each) | 15 s |
| Outbound: OAuth token exchange (Slack, Notion) | 10 s |
| Outbound: ICS fetch | 15 s, body ≤ 10 MiB |
| Outbound: SMTP send (dial to QUIT) | 15 s |
| Server: time to receive request headers | 10 s |
| Server: time to receive a whole request | 30 s |
| Server: handler budget (request context deadline) | 90 s |
| Server: write backstop | 120 s |
| Server: idle keep-alive | 120 s |
| nginx: upstream read / send | 130 s |
| Proxy in front (operator, documented) | > 130 s idle/read |
| Largest request body (nginx and backend) | 1 MiB, JSON 413 |
| Shutdown: unready notice (readiness 503, still serving) | 5 s |
| Shutdown: drain (no new connections; finish work) | 30 s |
| Shutdown: cancel grace (cancelled work returns) | 5 s |
| Container stop grace period | 50 s |
| DB pool: open / idle / max lifetime / max idle time | 20 / 10 / 30 min / 5 min |
| Readiness DB check | 2 s |
| Container health check start period (backend) | 120 s |

## 4. Acceptance criteria

Test levels: `[unit]` Go/TS unit tests; `[integration]` a test that starts real
processes or containers (backend binary, nginx with the shipped config and a
stub or real upstream, Postgres) but not the full e2e stack; `[e2e]` the
Playwright suite against the compose e2e stack; `[contract]` stage 2;
`[review]` checked by the reviewer **against the enumerated checklist in the
AC** (each item present or the AC fails); `[manual-ops]` a dated rehearsal
recorded in the runbook. Each AC names the findings it closes.

### Part A — backend process (F-A1..F-A11 except F-A11's CSP half)

AC-A1 (F-A1). Given the backend is running, when a client sends request
headers at one byte per second, then the server closes the connection no later
than the header bound + 1 s; when a client sends headers then stalls in the
body, no later than the whole-request bound + 1 s. [integration]

AC-A2 (F-A1, F-A5). Given a JSON API request whose handler is still running
when the handler budget elapses (test route or stubbed slow dependency), then
its context is cancelled and the client receives a complete JSON error response
with status 503 before the write backstop — never a dropped connection and
never a 2xx with partial data. Specifically, a 60-day slot listing whose
context is cancelled mid-loop answers 503, not a truncated 200. Given a
browser-navigation route (OAuth/SSO callbacks, which end in redirects today,
`handlers_auth.go:96-99`), when the budget elapses, then the response is the
route's existing failure redirect, not JSON. [unit]

AC-A3 (F-A5, F-A3b). Given each outbound dependency stubbed to accept the
connection and never answer — LLM provider, Google calendar, Outlook, Zoom,
Teams, Slack API, Notion API, Slack/Notion token endpoint, ICS server, SMTP
server — when the backend calls it, then the call fails within its outbound
limit (table, §3) + 1 s, independently of any request context, and no
connection to the stub remains open afterwards. Given an ICS response larger
than the ICS body cap, the fetch fails without reading past the cap. [unit]

AC-A4 (F-A5). Given meeting-brief regeneration whose own 60 s deadline is
reached, when it is reached, then the client gets the handler's own error
response, not the budget's 503. Generally: the handler budget exceeds every
deadline a handler sets itself, and every handler's worst-case sum of
sequential outbound limits on its success path (natural-language parse: two
LLM calls, 60 s), by at least 30 s; a unit test asserts this for each handler
that sets a deadline or makes more than one outbound call in sequence (the
plan lists them). [unit]

AC-A5 (F-A1). Given an idle keep-alive connection, when the idle bound elapses
with no request, then the server closes it. [integration]

AC-A6 (F-A2, F-A3, F-A3b). Shutdown sequence. Given the backend serving a 5 s
request, a cron job mid-run, a recap run with per-user goroutines, a
handler-started background task and a booking confirmation email in flight,
when the process receives SIGTERM (or SIGINT) at T0, then, in this order:
1. from T0, readiness answers 503 and no cron schedules a new run, while new
   connections are still accepted and requests (including new ones) are served
   normally until T0 + unready notice;
2. at T0 + unready notice, the listener stops accepting new connections;
3. in-flight requests complete with their normal responses, and every
   tracked background unit (cron runs, recap per-user goroutines, analytics
   recompute, habit re-optimisation, manager detection, team-invite and
   booking emails) is awaited;
4. when all have returned before T0 + notice + drain, the process closes the
   pool, flushes error reports and exits 0.
Observable: a readiness call at T0 + 1 s gets 503; a new request at T0 + 1 s
gets its normal response; a connection attempt at T0 + notice + 1 s is
refused; the 5 s request and the email complete. [integration]

AC-A7 (F-A2, F-A3, F-A3b, F-A10). Given tracked work still running at T0 +
notice + drain, when that moment passes, then the process cancels it, waits
at most the cancel grace for it to return, logs one line per abandoned unit
naming its kind and identifier (request method+path, job name, booking id,
invite id — never an email address), closes the pool only after every unit
has returned or been abandoned, flushes error reports, and exits non-zero
within notice + drain + cancel grace + 3 s of T0. [integration]

AC-A8 (F-A3b). Given a booking confirmation email that fails — SMTP error,
SMTP timeout, or abandoned by AC-A7 — then the failure is logged at error
level with the booking id and reported to error monitoring when it is
configured; it is **not** retried by the process (D-15; Q-O4). The same holds
for a team-invite email (invite id). Today both failures are silent (F-A3b).
[unit]

AC-A9 (F-A4). Given the default configuration, when more concurrent requests
need the database than the open-connection cap, then Postgres sees at most the
cap of connections from one backend (`pg_stat_activity`), excess requests wait,
and a request that cannot get a connection before its handler budget ends with
AC-A2's 503. Caps are operator settings with the stated defaults; a
non-positive or unparseable value is ignored with a startup warning. [integration]

AC-A10 (F-A4). Given the pool cap set to **1**, when each of these runs to
completion — Google and Microsoft sign-in callbacks, public booking create,
slot listing with a date, natural-language parse with a stub LLM,
personal-calendar sync with a stub ICS server, and, if PAC-71 has merged when
Part A is planned, a guarded write and the PAC-71 sign-in path — then each
completes within 10 s (a path that holds one pooled connection while waiting
for a second deadlocks and fails this test). [integration]

AC-A11 (F-A7). Given migrations have finished and the database answers within
the readiness timeout, when readiness is called, then it answers success; when
the database is unreachable, or shutdown has begun (AC-A6 step 1), it answers
503 within readiness timeout + 1 s. Liveness (`/api/health`) keeps its current
status and body, and both are unauthenticated and outside every abuse limit.
[contract, unit, integration]

AC-A12 (F-A6). Given a request body over the body cap sent directly to the
backend, then the backend answers 413 with the JSON error shape and the
handler does not run; a body exactly at the cap is processed. [unit]

AC-A13 (F-A8). Given any request, when it completes, then exactly one
access-log line records method, path without query string, the status code
actually sent on the wire, duration, and the client address as the abuse
limits resolve it. Given an inner middleware that replaces the handler's status
(as PAC-71's guarded writer does) the line records the replaced status; given
a budget expiry, it records 503; given a query string carrying a token, the
token is absent. [unit]

AC-A14 (F-A10, F-D1). Given `SENTRY_DSN` unset or empty, when the backend
starts, then error monitoring is off (no event is sent to any host — proven by
a stub transport receiving nothing on a captured error) and the backend logs
"monitoring disabled"; given it is set, events carry the configured environment
and release. `.env.example` ships an empty DSN and its comment is true.
PAC-14 contract §2 is amended through stage 2 to drop the default. [unit,
contract]

AC-A15 (F-A9). Given cookies are Secure (default), when any configured public
URL the backend uses for redirects or links (front-end URL, app URL, invitation
base URL) is `http://` on a host other than `localhost`/`127.0.0.1`/`[::1]`,
then the backend logs one startup warning per such setting naming it; none when
all are `https://` or insecure cookies are enabled; startup is never refused.
[unit]

AC-A16 (F-A1, F-A7, F-C2). Given the backend, MCP and file-server binaries,
when each is started in health mode, then it opens no database connection, runs
no migration, needs no secret, requests its own server's liveness (backend:
readiness) on the local port, and exits 0 only on a success answer within 2 s,
1 otherwise. Given a client sending headers slowly to the MCP or file server,
the connection is closed within the header bound + 1 s; an open, idle MCP SSE
stream is not closed by any write bound. [unit, integration]

AC-A17 (F-A5, D-5). Given personal-calendar sync creating blockers, when the
handler budget or the shutdown drain is about to end, then the sync starts no
new provider create unless at least one outbound limit remains, and a create
that the provider accepted is always recorded; so a sync interrupted by a
deadline or shutdown and re-run creates no duplicate blocker (stub provider
counts creates per source event: at most one). [unit]

### Part B — nginx edge (F-B1..F-B7, F-A11 CSP half)

AC-B1 (F-B1). Given the shipped nginx configuration, when it is loaded, then it
has no TLS listener and no response to `/`, `/api/health`, a SPA deep link, or
a request carrying `X-Forwarded-Proto: http` from a trusted proxy is a redirect
to `https://`. [integration, e2e]

AC-B2 (F-B2). Given any response through nginx — SPA page, static asset, API
JSON, API error, SPA fallback, nginx's own 413 — then it carries exactly one
each of `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`,
`Referrer-Policy: strict-origin-when-cross-origin` and a
`Content-Security-Policy`, and no `Strict-Transport-Security`. [integration, e2e]

AC-B3 (F-B2, F-A11). Given the CSP, then it forbids framing by any origin,
objects, inline script and `eval`, restricts base URI and form targets to the
site, and allows images from the site, `data:` and `https://*.googleusercontent.com`
only. Given the e2e suite with (a) one seeded user whose avatar is a
`https://lh3.googleusercontent.com/...` URL, shown on that user's public
booking page (the test serves the image locally by intercepting the request,
so no internet is needed), and (b) an e2e variant in which the API-base
injection is active, then the browser reports zero CSP violations on every
test. Negative control: a seeded avatar on a non-allowed origin produces
exactly one reported `img-src` violation, proving the collector works. [e2e]

AC-B4 (F-B2). Given the operator configures a cross-origin API address or
extra connect origins, when nginx starts, then the CSP allows connections to
exactly those origins plus the defaults (site, Sentry ingest). [integration]

AC-B5 (F-B3). Given nginx with the shipped config, a trusted-proxy list
containing address P, and a stub upstream in place of the backend that records
the headers it receives, when a request arrives from P with `X-Forwarded-For:
F, C` (F forged by the client, C appended by P) and `X-Forwarded-Proto: https`,
then the upstream receives `X-Real-IP: C`, an `X-Forwarded-For` whose last
entry is C, and `X-Forwarded-Proto: https`; nginx's access log shows C.
[integration]

AC-B6 (F-B3). Given the same setup, when a request arrives from an address Q
not in the list carrying `X-Forwarded-For`, `X-Real-IP` and
`X-Forwarded-Proto: https` that name another client, then the upstream receives
`X-Real-IP: Q` and `X-Forwarded-Proto: http`. [integration]

AC-B7 (F-B3). Given no trusted-proxy list (the default), every request is
treated as in AC-B6. The e2e stack runs with no list, so all e2e traffic stays
one client (§6). [integration]

AC-B8 (F-B4, F-A6). Given a stub upstream that answers after 125 s, when the
request is proxied, then the client receives the upstream's response, not a
504. Given an `/api/` request body over the cap, nginx answers 413 without
contacting the upstream, with `Content-Type: application/json` and the
backend's error shape. [integration]

AC-B9 (F-B5). Given 20 sequential API requests through nginx to a stub
upstream that counts accepted TCP connections, then it counts fewer than 20.
[integration]

AC-B10 (inherited, F-C3). `docs/security.md` after this work. [review]
Checklist — each must be present: (1) B1 row: TLS and HSTS are the proxy's
responsibility, with the recommended HSTS value from AC-D3; (2) B1/B7: CSP,
X-Frame-Options, nosniff and Referrer-Policy are set by nginx; (3) B2 row:
the published ports are nginx's HTTP port and, when enabled, MCP on loopback
only; (4) B2 row: access log records status and client address; (5) B5 row:
MCP is opt-in, loopback-only, and requires its token; (6) finding 3 rewritten:
TLS/HSTS out of scope by owner decision 2026-10-27, headers done, nginx-level
rate limiting not used because abuse protection is application-level;
(7) the backup-contains-credentials risk of F-D5 is listed as an open finding.

AC-B11 (F-B7, F-C2). Given nginx, when a request is made to its own health
path, then nginx answers 200 without contacting any upstream and without an
access-log line. [integration]

AC-B12 (F-B6, D-16). Given the shipped nginx configuration, then it has no
location serving the docs site; a request for `/docs/` gets the SPA fallback,
under the app's CSP. [integration]

### Part C — compose, images, health checks, MCP (F-C1..F-C8, F-B6)

AC-C1 (F-B1). Given the production compose file, then it publishes no 443 and
mounts no certificate directory, and `.env.example` documents none. [unit,
alongside `backend/config/deployment_files_test.go`]

AC-C2 (F-C1). Given the production compose file rendered without a version
setting, then rendering fails naming the setting; with one set, every Paceday
image uses that exact tag and nginx and Postgres use exact versions. No image
reference ends in `:latest` or a bare major. [unit]

AC-C3 (F-C2; needs A16, A11, B11). Given the production stack, then backend
(readiness via its health mode, start period 120 s), MCP and frontend (health
mode) and nginx (its health path) have health checks; nginx starts only after
backend and frontend are healthy; given a stopped backend, its check turns
unhealthy within 3 intervals; given a 90 s startup migration (simulated), the
stack still comes up. [integration]

AC-C4 (F-C3, Q-O1). Given the production compose file, then the MCP service
belongs to an opt-in profile and, when enabled, publishes only on `127.0.0.1`.
`README.md`'s SSE example shows the bearer header. [unit]

AC-C5 (F-C4). Given the MCP profile enabled and no token, rendering the
compose file fails naming the token; with the profile disabled, no token is
required. `.env.example` says required-when-enabled. [unit]

AC-C6 (F-C5). Given the production compose file, then every Paceday service has
a stop grace period ≥ notice + drain + cancel grace + 10 s (50 s), and every
service's logs are size-capped and rotated. Given `docker compose stop backend`
during a 5 s request, the request completes. [unit, integration]

AC-C7 (F-A10, F-C6). Given the production compose file, then it passes to the
backend exactly this list beyond today's: `APP_URL`, `BASE_URL`,
`PACEDAY_ADMIN_EMAILS`, `SMTP_HOST`, `SMTP_PORT`, `SMTP_USER`, `SMTP_PASS`,
`SMTP_FROM`, `SENTRY_DSN`, `SENTRY_ENVIRONMENT` (default `production` in this
file), `SENTRY_RELEASE` (default: the image version), the pool settings and
the shutdown settings Part A introduces; and does not pass `SESSION_SECRET`.
[unit]

AC-C8 (F-B6, D-16). Given the production compose file, then it has no docs
service. [unit]

AC-C9 (F-C8). Given the production compose file, then the host address nginx
binds is an operator setting (default all interfaces, as today). [unit]

### Part D — operations (F-D1..F-D5)

AC-D1 (F-D1). Given the source tree, when a test enumerates every environment
variable read by the backend, the MCP server and the file server, then each is
documented in `.env.example` with purpose, default, and one of
required/optional/test-only, or is in an explicit test-only allowlist; and
every variable in `.env.example` is read by a service or by the compose file.
Fails today on F-D1's lists. [unit]

AC-D2 (F-D4, F-C8). Operator documentation, proxy requirements. [review]
Checklist: (1) the proxy terminates TLS and sets HSTS, recommended
`max-age=31536000; includeSubDomains` (no `preload` unless the operator
chooses it); (2) it sets `X-Forwarded-For` (appending) and
`X-Forwarded-Proto`; (3) how to set nginx's trusted-proxy list, including the
same-host case: trust the Docker bridge gateway address the proxy appears as,
not `127.0.0.1`; (4) the proxy's idle/read timeout must exceed 130 s
(example: an AWS ALB's 60 s default is too short); (5) nginx's host port must
be reachable only from the proxy — with the proxy on the same host, bind nginx
to `127.0.0.1` and know that the peer nginx sees is the bridge gateway;
(6) health checks to configure on the proxy: readiness through nginx.

AC-D3 (F-D3, F-D5). Given the backup procedure, when an operator follows it on
a running stack, then it produces a consistent dump without stopping the
backend, on the stated schedule and retention; and given a fresh host,
following the restore procedure with that dump, the saved `.env` secrets
(`POSTGRES_PASSWORD`, `JWT_SECRET`, OAuth client secrets, `MCP_AUTH_TOKEN`)
and the same image version yields a stack whose readiness succeeds, whose row
count for **every table in the public schema** equals the source's, and where a
session cookie issued before the backup is still accepted. The procedure states
that dumps contain users' provider credentials and must be stored as secrets.
Rehearsed once; date and outcome recorded in the runbook. [integration for the
script, manual-ops for the rehearsal]

AC-D4 (F-D4, F-D2). Runbook, deploy and rollback. [review] Checklist: (1)
order: back up → pull pinned images → start Postgres → start backend (migrates)
→ wait for readiness → start the rest; (2) backend not ready after a migration
→ restore the backup and deploy the previous version; never edit
`schema_migrations` by hand; never migrate down in production; (3) image-only
rollback (no schema change) = deploy previous version; (4) expected deploy
interruption: unready notice + drain; (5) links PAC-71's account-recovery
document when it exists; (6) the docs site is published separately (D-16).

AC-D5 (F-D2). Given the migrations directory, every `.up.sql` has a matching
`.down.sql`; 006, 007, 014, 015, 016's down files reverse their schema change
(006's also drops `user_id` from `oauth_tokens`, `settings`, `focus_blocks`,
`personal_calendars`) and start with a comment stating what data they destroy.
Given an empty database migrated to the latest version, migrating down to 005
and up again succeeds; any existing down file (017…031, 032 if merged) that
fails this is in scope to fix. [unit, integration]

AC-D6 (F-D4). `docs/contributing.md`'s runbook link resolves to an existing
document. [unit]

AC-D7 (F-A10). The environment reference states that `SENTRY_RELEASE` should
be the deployed image version and that monitoring is off until `SENTRY_DSN` is
set. [unit — a doc test greps for both statements]

### What the falsifiers look like today

Read from source on `67b7966`, not run: A1/A5 (no timeouts), A2 (no budget;
the slot loop returns a truncated 200), A3 (no outbound limits), A4 (no
budget; inner deadlines unchecked), A6/A7 (no signal handling, untracked
goroutines), A8 (`_ =` on SendMail), A9 (no caps), A10 (no test — today's
behaviour unknown), A11 (no readiness), A12 (no cap), A13 (no status/client),
A14 (default DSN sends), A15 (no warning), A16 (no health mode), A17 (no
deadline check between creates); B1 is a regression guard (already passes for
nginx); B2–B12 per F-B*; C1–C9 per F-C*; D1–D7 per F-D*.

## 5. Explicitly out of scope

- **TLS and HSTS in Paceday** (owner, 2026-10-27).
- **Booking abuse limits as operator settings, and the 1000/s link-info
  limit** (owner, 2026-10-27; NEW-booking-rate-limit follow-up). Today the
  counts are fixed in production (`backend/config/booking_limits.go:9-15`).
- **The web's 4 s client abort (F-W1) and web Sentry default (F-W2)**: web
  tickets, §10. Part A's ladder protects the server only.
- **Durable email delivery (outbox, retry).** Failures are logged, not
  retried (D-15, Q-O4, §10).
- **nginx-level rate limiting**: abuse protection is application-level.
- **Cross-replica abuse limits** (API-062 residue).
- **An image build/publish pipeline** (F-C1).
- **MCP over the public origin and the `0.0.0.0` endpoint** (Q-O1).
- **Changing `/api/health`'s body.**
- **Encryption at rest of stored provider credentials (F-D5)**: §10; the
  runbook treats dumps as secrets meanwhile.
- **Off-host backup storage, PITR**: operator choices named in the runbook.
- **Postgres tuning**: the pool is sized to the default.
- **Multi-replica / zero-downtime deploys.**
- **The web repo's `nginx.local.conf`/`Dockerfile.local`** (local only).
- **Serving the docs site from this stack** (D-16).

## 6. What must not change

- HTTP only, no redirect (`nginx/nginx.conf:2-3`); AC-B1 adds the guard.
- Backend client identification (socket peer; `X-Real-IP` only from
  `TRUSTED_PROXIES`; `X-Forwarded-For` never read). Protected by
  `backend/api/ratelimit_detect_test.go` and the booking limiter tests. This
  work changes what nginx puts in `X-Real-IP`, not how the backend reads it.
- Cookies Secure by default (`backend/config/cookies_test.go`).
- `/api/health` status, body, no auth, no limit — guarded by
  `e2e/global-setup.ts:61-70` (whole suite fails to start otherwise) and relied
  on by `smart-calendar-flow:src/lib/api.ts:28-39`.
- Stand-in startup refusals (`backend/main.go:40-67`;
  `backend/config/endpoints_test.go`, `idp_test.go`).
- Access logs never contain query strings; AC-A13 adds the test.
- e2e: plain HTTP with `PACEDAY_INSECURE_COOKIES=true`
  (`e2e/docker-compose.e2e.yml:135`) and no trusted-proxy list in either nginx
  or the backend, so all e2e traffic is one client (NEW-booking-rate-limit
  AC-13). If nginx's config becomes a template, the e2e mount
  (`e2e/docker-compose.e2e.yml:229`) changes with it in the same PR.
- `backend/config/deployment_files_test.go` assertions.
- PAC-71 (#242) behaviour: its response writer, generation check and
  transaction must compose with the budget, access log and pool (AC-A10,
  AC-A13); none of its tests may change.

## 7. Rollback

- Part A: revert the backend commit (and PAC-14 contract amendment). No
  migration. Timeouts, caps, shutdown and the Sentry default revert together;
  monitoring then turns on again with the hard-coded DSN.
- Part B: revert nginx config. A configured trusted list is then ignored and
  limits key on the proxy again. Header lines can be reverted alone.
- Part C: revert the compose file; a set version is ignored (`:latest` again);
  the docs service returns (still unreachable).
- Part D: docs revert freely. Added down files are used only when migrating
  down; 006's down **destroys every user account and its dependants**, which
  is why the runbook rolls back by restore, never by migrate-down.
- Personal blockers duplicated by a SIGKILL before this work stay; nothing
  cleans them up.

## 8. Open questions

### Owner (product-level)

Q-O1. **Should the MCP server be reachable from outside the host?** (a)
loopback only, opt-in; (b) through the public origin behind the proxy; (c) not
offered in production. Default: **(a)**.

Q-O2. **May the public booking page be framed by other sites?** Default:
**no framing anywhere** (`docs/src/content/docs/scheduling/what-are-scheduling-links.mdx:23`
"embed the link" read as placing a link).

Q-O3. **Backup objective** (data loss tolerated, retention). Default: **daily
dump, 14 days, up to 24 h lost**.

Q-O4 (new in rev 1). **Must booking confirmation emails survive a restart
before production?** After this work, an email in flight at deploy gets up to
the drain to finish and is otherwise logged with the booking id, not resent;
the booker keeps the booking but may get no email. Default: **acceptable for
now; durable delivery is a follow-up (§10)**.

### Plan stage

Q-P2. CSP without inline script while the file server injects the API base
(F-B2): hash, nonce, or a separately served script. If the web repo must
change, cross-repo ordering (factory README §1) applies.

Q-P3. How nginx receives the trusted list and extra CSP origins at start;
AC-B4/B5/B7 constrain behaviour only. The e2e mount changes with it (§6).

Q-P4. Whether the backend's `TRUSTED_PROXIES` default
(`docker-compose.prod.yml:47`) should narrow to the compose network.

(Q-P1 from rev 0 is closed: the 60-day listing is not legitimate load —
F-A5.)

## 9. Decisions

Owner-decided:

- **D-1 (owner, 2026-10-27).** HTTP server only; the proxy terminates TLS;
  nginx HTTP only and never redirects; 443 publish and certificate mount
  removed; forwarding headers trusted only from configured proxies; HSTS on
  the proxy, documented; nginx sets CSP, X-Frame-Options, nosniff,
  Referrer-Policy.
- **D-2 (owner, 2026-10-27).** Booking limits are abuse protection and become
  operator settings — implemented by the NEW-booking-rate-limit follow-up.

Factory-level decisions (coordinator, 2026-10-28, recorded as such):

- **F-1 Avatars.** `img-src` allows the avatar origins actually stored: Google
  `*.googleusercontent.com` (F-A11). Microsoft and SSO store none; `data:` is
  allowed anyway. One e2e user is seeded with an external avatar (AC-B3).
- **F-2 Docs.** Docs get their own CSP on their own location, or are not routed
  — the spec chose not routed: D-16.
- **F-3 Timeout ladder from real clients.** The web's 4 s abort is a finding
  (F-W1) spun out to §10; the server ladder is sized from the longest
  server-side work with explicit outbound limits (D-4).
- **F-4 Budget > inner deadlines** (AC-A4, D-4).
- **F-5 Shutdown order**: readiness 503 first, then drain, then stop
  accepting (AC-A6; see D-5 for how "drain" is split into notice and drain).
- **F-6 Background work** joins a tracked group awaited within the drain;
  SMTP gets a timeout; an interrupted booking email is logged (AC-A6..A8);
  retry decided in D-15.
- **F-7 Part independence**: true order stated (§11); trusted-proxy ACs moved
  to an integration test with a stub upstream; scheme halves observed at the
  upstream header (D-3).
- **F-8 Sentry**: remove the backend DSN fallback; AC-A14; PAC-14 §2 amended
  in stage 2.

Factory-assumed (pending challenge):

- **D-3. The backend does not read `X-Forwarded-Proto`.** Absolute URLs and
  `Secure` stay configuration-driven (F-A9); a deployment has one public
  origin, and a per-request scheme would be a spoofable input for no gain.
  nginx still forwards the scheme (trusted proxy's value, else `http`) per
  D-1, for logs and future consumers; AC-B5/B6 observe it at the upstream.
  AC-A15 catches the one misconfiguration that breaks sign-in.
- **D-4. Timeout ladder.** Outbound limits: LLM 30 s per call (a short
  parse prompt completes far sooner *(provider behaviour, not measured)*; 30 s
  leaves room for slow models); provider APIs 15 s (single-object REST calls);
  token exchanges 10 s; ICS 15 s and 10 MiB (a large published calendar is a
  few MiB *(not measured)*); SMTP 15 s. Longest legitimate server-side work:
  natural-language parse, 2 × 30 s = 60 s; meeting brief, own 60 s deadline;
  personal-calendar sync, unbounded in event count, so it is cut by the budget
  and resumes on the next cron run (AC-A17 keeps that safe). Handler budget
  90 s = 60 s + 30 s for work before and after the slow part (AC-A4). Write
  backstop 120 s > budget + time to write an error. nginx 130 s > backstop.
  Proxy > 130 s (AC-D2). Header 10 s, whole request 30 s (1 MiB at ~35 KB/s),
  idle 120 s (above common proxy keep-alives of 60–75 s). These bounds do not
  change the browser's 4 s abort (F-W1).
- **D-5. Shutdown phases.** Unready notice 5 s: long enough for a health check
  polling every 1–2 s to see 503 before connections are refused, short enough
  not to lengthen deploys. Drain 30 s: covers every outbound limit (≤ 30 s), so
  any single in-flight call finishes; a request deep into a 90 s budget may be
  cut — accepted. Cancel grace 5 s. Stop grace 50 s = 5 + 30 + 5 + 10 margin,
  so Docker's SIGKILL never lands inside the sequence. Auto-decline is
  idempotent on re-run; the personal blocker is made safe by AC-A17 instead of
  being assumed idempotent (challenge Non-blocking 3).
- **D-6. Pool 20 open / 10 idle / 30 min / 5 min, operator-settable.** 97
  usable connections *(Postgres default)*; two backends during a deploy use
  40; the rest is for migrations, `pg_dump` (1), `psql`, and PAC-71's operator
  binary (its own pool, run by hand). Background fan-out (recap, focus) shares
  the cap: user requests may wait for a connection, bounded by the budget
  (AC-A9) — accepted; if it bites, a separate background cap is a follow-up.
  AC-A10 ensures no path needs two connections, so the cap cannot deadlock.
- **D-7. Body cap 1 MiB, JSON 413 from nginx and backend.** Matches today's
  implicit nginx limit, so nothing newly refused; no upload endpoint exists.
- **D-8. Header values.** `X-Frame-Options: DENY`, `frame-ancestors 'none'`
  (Q-O2); `Referrer-Policy: strict-origin-when-cross-origin`; `nosniff`. CSP:
  `default-src 'self'`; `script-src 'self'` (no inline, no eval); `style-src
  'self' 'unsafe-inline' https://fonts.googleapis.com` (inline styles needed by
  the chart component); `font-src 'self' https://fonts.gstatic.com`; `img-src
  'self' data: https://*.googleusercontent.com` (F-1); `connect-src 'self'` +
  the Sentry ingest host + operator-configured origins; `object-src 'none'`;
  `base-uri 'self'`; `form-action 'self'`; `frame-ancestors 'none'`. Enforced,
  because AC-B3 proves zero violations with a positive and negative control.
- **D-9. Real client in nginx**: right-most address not in the trusted list,
  recursive; list empty by default (spoofing impossible; limits shared until
  configured; the runbook makes configuring it a deploy step).
- **D-10. Health checks call the binaries**; backend start period 120 s
  because migrations run before the listener.
- **D-11. Image versions required, no default.**
- **D-12. MCP opt-in profile, loopback only** (Q-O1 (a)); its token is
  required only when enabled.
- **D-13. Every migration gets a down file**; production rollback is
  restore-from-backup.
- **D-14. Backups: daily `pg_dump`, 14 days** (Q-O3), stored with the `.env`
  secrets, both treated as secrets (F-D5).
- **D-15. Interrupted or failed emails are logged, not retried.** An
  in-process retry dies with the process it is meant to survive; real retry
  needs a persisted outbox, which is a feature of its own (§10, Q-O4).
- **D-16. The docs site is not served by this stack.** The prod compose drops
  the docs service and nginx gets no docs route. Reasons: (1) the docs are
  already built for separate hosting (`docs/vercel.json`); (2) the container is
  unreachable today (F-B6), so removing it changes nothing a user sees; (3)
  routing Starlight on the app's origin would need `script-src 'unsafe-inline'`
  and `'wasm-unsafe-eval'` on that origin's docs path, and a script injected
  into a same-origin docs page can call the app's API with the user's cookies
  — a weaker policy on the same origin weakens the app. AC-C8, AC-B12.

## 10. Follow-up tickets proposed

- **WEB-timeouts (web repo).** Per-operation client timeouts: calls the
  server bounds at more than 4 s (natural-language parse, personal-calendar
  sync, schedule suggest) get a client timeout at least the server's handler
  budget, and a timeout on them shows an error, not preview data (F-W1). Under
  factory README §1 ordering, after Part A.
- **WEB-sentry-default (web repo).** Drop the hard-coded DSN fallback
  (`src/lib/sentry.ts:55`) so web monitoring is also off until configured
  (F-W2), consistent with AC-A14.
- **Email outbox.** Persist outgoing booking/invite emails and retry them
  across restarts (D-15, Q-O4).
- **Credentials at rest.** Encrypt stored provider tokens and user LLM keys
  (F-D5).
- **MCP over the public origin**, if Q-O1 answers (b).

## 11. Proposed split and true order

| Part | Scope | ACs | Depends on |
|---|---|---|---|
| **A** | Server timeouts, outbound limits, handler budget, shutdown sequence with tracked background work, email failure logging, pool caps and single-connection paths, readiness, body cap, access log, Sentry default off, public-URL warning, binary health modes, blocker safety | 17 (A1–A17) | Stage 2 for A11 (readiness) and A14 (PAC-14 §2) |
| **B** | nginx: HTTP-only guard, headers and CSP (with avatars), trusted proxy / real client and scheme, timeouts, JSON 413, upstream keep-alive, health path, no docs route, `docs/security.md` | 12 (B1–B12) | None: every B test uses a stub upstream. B3's e2e runs against whatever backend is on `main` |
| **C** | Compose: no 443/certs, pinned versions, health checks, MCP opt-in loopback, grace period, log rotation, settings list, no docs service, nginx bind | 9 (C1–C9) | A (A16 health modes, A11 readiness, shutdown values, A14) and B (B11 health path) |
| **D** | `.env.example` completeness test, proxy requirements, backup/restore, runbook, down migrations, doc link, env reference | 7 (D1–D7) | D1, D5, D6 any time; D2–D4, D7 after C |

True order: **A and B in parallel → C → D (D1/D5/D6 may land earlier)**.
Total: 45 acceptance criteria.

## Challenge, revision 0 — 2026-10-28

**Verdict**: accept-with-changes

Checked against `Enach/clockwise-like` `origin/main` = `67b7966` (unchanged
since the survey) and `Enach/smart-calendar-flow` `origin/main` = `9ceff3b`;
PAC-71 read at `origin/impl/pac-71-identity-binding-backend` (PR #242, open).
Read with `git show` / `git grep` only; nothing was built or run. Most of §2
holds up (see "Citations"). The blocking items below are places where an AC
contradicts another AC or the chosen values, a test level cannot work, or
the spec's reasoning rests on a client that does not behave as assumed.

### Blocking

1. **The CSP blocks user avatars, and the e2e proof cannot notice** (D-8,
   line 810; AC-B3, lines 475-481). `img-src 'self' data:` refuses the
   provider picture URLs the backend stores (`backend/api/handlers_auth.go:64`
   stores Google `info.Picture`; PAC-71 keeps writing `avatar_url` from the
   sign-in). They are rendered as `<img src={h.avatar_url}>`
   (`smart-calendar-flow:src/components/links/HostAvatars.tsx:58`, public
   booking page included) and via `AvatarImage` (`src/components/ui/avatar.tsx`).
   The e2e seed sets every `avatar_url` to `''` (`e2e/seed/seed.sql:70-76`),
   so AC-B3's "zero violations on every e2e test" passes while production
   avatars break. Scenario: a Google user opens their booking link and their
   host picture is blocked. *Resolve*: §3/D-8 must say which image origins
   are allowed (or that avatars are served same-origin), and AC-B3 must
   include a journey whose user has a non-empty third-party avatar URL.

2. **The docs site cannot live under this CSP** (AC-C8, lines 562-564, option
   "routes the docs service through nginx"; AC-B2, lines 468-473, "any
   response served through nginx"; D-8 "no inline script"). The docs are
   Starlight (`docs/package.json`, `docs/astro.config.mjs`), whose
   `ThemeProvider`, `ThemeSelect`, `Search` and `SidebarPersister` components
   use `<script is:inline>` (seen in the installed
   `@astrojs/starlight/components/*.astro`), and its search uses Pagefind
   (WebAssembly). No e2e journey touches the docs, so AC-B3 cannot catch it.
   Scenario: the plan picks "route through nginx", the docs render without
   theme and search, nothing fails. *Resolve*: state which CSP the docs get
   (or that the docs are not run) and add an AC with zero violations on a
   docs page if they are routed.

3. **The timeout ladder is built for a client that does not exist** (§3,
   lines 334-337; D-4, lines 773-785; AC-A3/AC-A4). The web client aborts
   **every** `requestApi` call after 4 s and treats it as "unreachable",
   switching to preview data
   (`smart-calendar-flow:src/api/client.ts:318`, `:590-591`, comment at
   `:1230`). Natural-language parse (`:1494`), personal-calendar sync
   (`:1622`) and schedule suggest (`:1441`) all go through it. The 60 s
   meeting-brief refresh that anchors the handler budget
   (`POST /api/meetings/{event_id}/brief/refresh`, `routes.go:200`) has no
   caller in the web repo or in `mcp/`. So "the backend's own error, not a
   proxy's cut, is what a client sees when an AI call runs out of time" is
   false for every real browser call: the browser gives up at 4 s, whatever
   the server bounds are. *Resolve*: record the 4 s client bound as current
   behaviour in F-A5. Then either add a web-repo requirement (per-operation
   client timeouts at least as long as the server budget for AI/sync calls,
   under factory README §1 cross-repo ordering), or restate §3 and AC-A3/A4
   as server-resource protection only and justify the 60 s budget without
   the brief endpoint.

4. **AC-A4 fails by construction with the chosen values** (AC-A4, lines
   398-402; D-4, lines 776-778). The handler budget equals the brief's own
   deadline (60 s = 60 s). The brief's deadline is
   `context.WithTimeout(r.Context(), 60*time.Second)`
   (`handlers_meeting_briefs.go:80`), set *after* the budget started, so the
   effective deadline is always the budget's. The budget fires first, which
   contradicts "the client gets the brief or the handler's own error".
   *Resolve*: make the budget strictly longer than the longest handler
   deadline plus the work before it, or restate AC-A4.

5. **AC-A6 and AC-A9 contradict each other on shutdown** (AC-A6, lines
   407-412: "stops accepting new connections at once; readiness reports not
   ready from that moment"; AC-A9, lines 428-431: "or the process is shutting
   down, it answers 503"). Once new connections are refused, the readiness
   probe gets a refused connection, not a 503. The 503 is only observable on
   a request already in flight. *Resolve*: specify the order (e.g. readiness
   503 while connections are still accepted for a stated period, then stop
   accepting), or drop the 503-while-shutting-down clause.

6. **Detached work is outside the drain, and AC-A7 closes the pool under
   it** (F-A2/F-A3, lines 85-101; AC-A6/AC-A7, lines 407-418). Handlers start
   goroutines that outlive the request on `context.WithoutCancel` /
   `context.Background()`: analytics recompute
   (`handlers_analytics.go:93-96`), habit re-optimisation
   (`handlers_habits.go:137-140`, `:239-242`, `:383`), manager detection
   (`handlers_manager.go:97-101`), the team-invite email
   (`handlers_teams.go:214`) and the **booking confirmation email**
   (`engine/booking.go:277`). Both emails use `smtp.SendMail`, which has no
   timeout (`handlers_teams.go:650`, `engine/booking.go:330`). The recap cron
   returns after launching one goroutine per user (`engine/daily_recap.go:130`),
   so "the running job is allowed to finish" never sees those goroutines.
   Scenario: a booker books 1 s before a deploy; the confirmation email
   goroutine is abandoned (or its DB writes fail after the pool closes) and
   nothing is logged. *Resolve*: list these in F-A2/F-A3, and make AC-A6/A7
   say what happens to them: awaited within the drain, or abandoned and
   logged by name. The pool must close only after they have returned or been
   abandoned.

7. **AC-B5/AC-B6 cannot run as `[e2e]`, and the split's dependencies are
   wrong** (AC-B5/B6, lines 488-498; §6, lines 679-682; split, lines
   830-838). The e2e backend has no `TRUSTED_PROXIES`
   (`e2e/docker-compose.e2e.yml:136-137`), so it keys on nginx's address and
   cannot "see C". §6 requires that to stay true, and trust is set at stack
   start, so "unless a test configures otherwise" is not possible per test.
   The "access log sees C" half needs AC-A11 (Part A), so "A and B have no
   dependency" is false. AC-C7 needs Part D's environment reference
   ("listed as production-relevant in the environment reference (Part D)"),
   but D is ordered last and the table says C depends only on A. *Resolve*:
   give B5/B6 a test level that works (e.g. `[integration]` with its own
   nginx and backend, or a named second e2e profile), and fix the dependency
   column (B5/B6 → A11; C7 → D1, or move the list into C).

8. **AC-D2 cannot hold without a backend change no AC makes** (AC-D2, lines
   580-581: "monitoring is off until the operator sets it"). `initSentry`
   uses `cmp.Or(os.Getenv("SENTRY_DSN"), defaultBackendDSN)`
   (`backend/sentry.go:16`, `:106`), so an empty DSN sends to the hard-coded
   project. `.env.example:118` ("Leave empty to disable monitoring") is
   already false. The web does the same (`src/lib/sentry.ts:55`). The PAC-14
   contract §2 requires a default DSN. *Resolve*: either add an AC that
   changes the backend default (through stage 2, amending PAC-14 §2), or
   reword AC-D2 to cover only the `.env.example` text, and correct
   `.env.example:118`.

### Non-blocking

1. **PAC-71 (#242) impact.**
   - Every authenticated request adds one uncached read,
     `storage.CurrentGeneration` (in `requireAuth`).
   - Guarded writes hold one connection for a transaction around the
     handler's write (`storage/session.go` `withGenerationTx`).
     `lockAccountExclusive` waits up to `lock_timeout = '10s'`.
   - The sampled sign-in, org-AI and SSO step-up transactions commit before
     touching `db` again (e.g. `signin.go:181-209`). That was not
     exhaustive. Under a cap of 20, any path that holds a transaction while
     acquiring a second connection deadlocks at ≥ 20 concurrent requests.
     AC-A8 should exercise cap-level concurrency on the guarded-write and
     sign-in paths.
   - `guardedResponseWriter` replaces the handler's status, so AC-A11 must
     log the status actually sent. The handler-budget writer must compose
     with it (it exposes `Unwrap`).
   - The backend image gains `/paceday-operator` (run by
     `docker compose exec`, its own pool; count it in D-6's headroom).
     `docs/operator/PAC-71-account-recovery.md` should be linked from the
     runbook (AC-D5/AC-D7).
2. **Background fan-out shares the 20-connection cap.** The recap starts a
   goroutine per user every minute and focus ticks run per user, so a burst
   makes user requests wait (AC-A8 "wait rather than fail") with no bound.
   State whether that is acceptable.
3. **D-5's idempotency claim, checked as requested.**
   - Auto-decline is idempotent: a re-scan finds nothing pending
     (`engine/auto_decline.go` `RunForUser`).
   - The personal blocker is **not**. `syncOne` creates the work-calendar
     event, then records it (`engine/personal_blocker.go`, `createEvent` then
     `UpsertPersonalBlocker`). If cancellation lands after the provider
     accepted the create, the next run creates a duplicate blocker.
   - This already happens under SIGKILL today. But the spec uses
     idempotency to justify cancelling at 30 s: either accept duplicates
     explicitly (§7, runbook) or require that a create/record pair is never
     split by the drain.
4. **F-A5's inventory of long requests is incomplete.**
   - Personal-calendar sync (`handlers_personal.go:191`) fetches an ICS with
     `http.DefaultClient`, with no size or time limit
     (`calendar/webcal_client.go:64`), then makes one provider create per
     event over 14 days (`personal_blocker.go:19`).
   - Schedule suggest and team availability are not listed.
   - The 60-day slot listing is never called by the web, which always sends
     a date (`smart-calendar-flow:src/api/schedulingLinks.ts:692`), so Q-P1
     is an abuse case, not a legitimate one.
   - On a deadline, the listing loop's `continue`
     (`handlers_booking.go:181-183`) turns cancellation into a truncated
     **200**. AC-A2 should say a budget expiry never yields a partial 200.
5. **AC-A2 on browser-navigation callbacks.** Sign-in callbacks end in a
   redirect to the failure state (`handlers_auth.go:96-99`, PAC-35/API-011).
   A JSON 5xx at budget expiry would show raw JSON in the browser. The AC
   should define the behaviour for redirect-style routes.
6. **Health checks are new code in three binaries.** None of `backend/`,
   `mcp/main.go` or `docker/fileserver/main.go` has a health mode; only the
   e2e stand-ins do. AC-C3 should say so explicitly, plus:
   - Health mode opens no database connection, runs no migrations and needs
     no secrets.
   - `start_period` covers startup migrations, which run before the
     listener (`storage/db.go:25`). Otherwise `docker compose up` fails on
     nginx's `service_healthy` dependency during a long migration.
   - nginx has no health path today; Part B must add one.
7. **MCP.**
   - AC-C5 makes every operator set an MCP token even if they never use
     MCP; consider Q-O1 (c), or making the service optional.
   - `README.md:56-63` (`http://localhost:3001/sse`) still works with a
     loopback publish but shows no bearer header.
   - `docs/security.md:56` (B5 row: "HTTP only on loopback", token
     optional) is also false and is not in AC-B10.
   - The MCP client sends no `Authorization` to the backend
     (`mcp/client.go:77`). This is out of scope, but it is why loopback-only
     loses nothing today.
8. **AC-D6 runs all 26 existing down files** on the way down to 005
   (017…031, plus PAC-71's 032). Any broken one becomes in scope; say so.
   006's down also drops `user_id` from `oauth_tokens`, `settings`,
   `focus_blocks` and `personal_calendars`, not only `users`.
9. **AC-D4.** A dump alone does not restore a working stack: `JWT_SECRET`
   (sessions), OAuth client secrets and `POSTGRES_PASSWORD` live in `.env`.
   The procedure should name what to keep alongside the dump.
10. **Proxy on the same host (AC-D3).** When the TLS proxy runs on the host
    and connects to the published port, Docker's userland proxy presents the
    bridge gateway (not `127.0.0.1`) as the peer. The proxy requirements
    should say what to trust in that topology, and AC-C9's "restrict to the
    proxy's network" should account for it.
11. **e2e wiring.**
    - The e2e mounts `nginx.conf` directly as `conf.d/default.conf`
      (`e2e/docker-compose.e2e.yml:229`); any templating (Q-P3) changes that
      mount.
    - AC-B3's "with the runtime API-base injection active" needs an e2e
      variant: e2e sets `API_BASE_URL: ""` (`:219`), so no inline script is
      injected today.
12. **nginx's own 413 is an HTML page**, while AC-A10 promises the JSON shape
    from the backend; the web client parses JSON. State which one the client
    gets through nginx.

### Criteria I could not falsify
- AC-B5 (scheme half) and AC-B6 ("the backend sees … the scheme as `http`"):
  the backend reads no scheme (F-A9, D-3), so nothing observable
  distinguishes pass from fail. Name the observation point (e.g. the header
  value received upstream).
- AC-A9 "shutting down → 503": unobservable given AC-A6 (Blocking 5).
- AC-A6 "the running job is allowed to finish" for the recap: the cron job
  returns while its per-user goroutines run (Blocking 6).
- AC-D4 "row counts for users, events-linked tables and bookings":
  "events-linked tables" is not a list.
- AC-B10, AC-D3, AC-D5, AC-D8 are `[review]`. This is acceptable only if the
  reviewer gets a checklist of the exact sentences required; as written they
  pass on any wording.

### Citations I checked and found wrong
- `backend/api/handlers_booking.go:172-185` (60-day loop): the loop is at
  `:176-188`; `:172` is the end of duration validation.
- `docker-compose.yml:68-71` "the base compose already pins the frontend":
  it defaults to `latest` (`${FRONTEND_IMAGE_TAG:-latest}`, `:71`).
- §6 "`/api/health` … none was identified": `e2e/global-setup.ts:61-70`
  polls it through nginx, and `smart-calendar-flow:src/lib/api.ts:28-39`
  (`checkApiAvailability`, 2 s, `res.ok`) decides the web's preview mode
  from it.
- Correct as cited:
  - `main.go:29,112,123,149,167,180,202,215-216` and `scheduler/cron.go:59,83-93`.
  - `storage/db.go:17-28,44`.
  - `handlers_meeting_briefs.go:80`, `handlers_daily_recap.go:172`,
    `handlers_manager.go:119`.
  - `handlers_nlp.go:28`, `parser.go:166,239`,
    `user_destination.go:145-148`, `destination.go:202-205`.
  - `handlers_integrations.go:124,130,254,261`.
  - `guard.go:52`, `freebusy_service.go:133`, the four `DefaultClient`
    sites, `zoom_oauth.go:64`, `booking.go:304`.
  - `handlers_health.go:8-14`, `routes.go:17-21`, `middleware.go:105-111`,
    `ratelimit_detect.go:12-18,33,94-114`, `ratelimit_booking.go:15-18`.
  - `config/cookies.go:19-29`, `api/cookies.go:9-21`, `sentry.go:16,106-111`.
  - `handlers_sso.go:67,124`, `handlers_sso_stepup.go:174`,
    `handlers_teams.go:627-636`.
  - `fileserver/main.go:19-25,47`, `mcp/main.go:29-31,38,40`,
    `nginx.conf:1-17`.
  - Every prod compose line, `.env.example:7,109-114,121`,
    `e2e/docker-compose.e2e.yml:135,179,199,222-229`.
  - The five migrations without a down file, `testdb.go:20`,
    `docs/security.md:52,53,85`, `docs/contributing.md:26`,
    `adr/0001-tech-stack.md:17`.
  - Web: `index.html:18-20`, `sentry.ts:19-20`, `api.ts:16-22`.
  - The F-D1 variable lists (re-derived by `git grep`).

### Consumers this breaks
- `smart-calendar-flow/src/components/links/HostAvatars.tsx:58`,
  `src/components/ui/avatar.tsx`: provider avatars blocked by `img-src`
  (Blocking 1).
- `clockwise-like/docs` (Starlight inline scripts, Pagefind) if routed
  through nginx (Blocking 2).
- `clockwise-like/e2e/docker-compose.e2e.yml:229`: nginx config mount, if
  the config becomes a template.
- `clockwise-like/e2e/global-setup.ts:61-70` and
  `smart-calendar-flow/src/lib/api.ts:28-39`: depend on `/api/health`
  staying unauthenticated, unlimited and 200 (AC-A9 keeps this; name these
  as its guards in §6).
- PR #242 (PAC-71), `backend/api/middleware.go`: its response writer and
  per-request query must compose with the budget, access-log and pool
  changes (Non-blocking 1).


## Revision 1 — 2026-10-28

Author: spec-author. Responds to "Challenge, revision 0" above and to the
coordinator's factory-level decisions (recorded in §9 as F-1..F-8). §1–§11
above are the revised spec; the challenge text is kept unchanged. Verification
for this revision: `git show`/`git grep`/`sed` only, on `67b7966` and web
`9ceff3b`; nothing built or run. AC numbers changed: rev-0 A9 (readiness) is
now A11, A10 → A12, A11 → A13, A12 → A15, A13 → A16; rev-0 D2 merged into
A14; rev-0 D3/D4/D5/D6/D7/D8 → D2/D3/D4/D5/D6/D7.

| # | Challenge item | Outcome | Where / why |
|---|---|---|---|
| B1 | CSP blocks avatars; e2e cannot notice | **Applied** (F-1) | F-A11 finds the stored origins: Google `picture` only (`handlers_auth.go:64`); Microsoft and SSO store `""` (`handlers_microsoft_auth.go:69`, `handlers_sso.go:155`). D-8 adds `img-src https://*.googleusercontent.com`. AC-B3 seeds one user with a googleusercontent avatar shown on their public booking page, with a local intercept, plus a negative control that must produce exactly one violation. |
| B2 | Docs site cannot live under the CSP | **Applied** (F-2) | Chose "not routed" (D-16): the docs are already hosted separately (`docs/vercel.json`), the container is unreachable today, and Starlight would need inline scripts plus `wasm-unsafe-eval` on the app's own origin. ACs: C8 (no docs service), B12 (no docs location; `/docs/` gets the SPA under the app CSP). |
| B3 | Ladder built for a client that does not exist | **Applied** (F-3) | The web's 4 s abort is recorded as F-W1 and spun out as WEB-timeouts (§10). §3 and D-4 now state that the ladder protects server resources only. The ladder is re-derived from server-side work (parse 2×30 s, brief 60 s, sync cut by the budget), with explicit outbound limits for LLM, providers, Slack/Notion exchanges, ICS (time and size) and SMTP (AC-A3). |
| B4 | AC-A4 fails by construction | **Applied** (F-4) | Budget is now 90 s, at least 30 s above the 60 s brief deadline and the 60 s parse worst case. AC-A4 asserts the margin per handler. Backstop 120 s, nginx 130 s, proxy > 130 s. |
| B5 | A6 and A9 contradict on shutdown | **Applied** (F-5) | AC-A6 orders it: readiness 503 and still serving for the unready notice (5 s); then stop accepting; then drain (30 s); then cancel grace. Each step has an observation point. AC-A11's 503-while-shutting-down is now observable during the notice. |
| B6 | Detached work outside the drain; pool closed under it | **Applied** (F-6) | F-A3b lists the handler goroutines, both emails, the `SendMail` timeout gap and the discarded errors, plus the recap per-user goroutines. AC-A6: all of them join the tracked group awaited by the drain. AC-A7: abandoned units are logged by kind and id, and the pool closes only after them. AC-A3: SMTP limited to 15 s. AC-A8: failed or interrupted booking and invite emails are logged with ids, not retried (D-15). Q-O4 asks whether durable retry is needed; outbox ticket in §10. |
| B7 | B5/B6 cannot run as e2e; split dependencies wrong | **Applied** (F-7) | B5–B7 are now `[integration]`: shipped nginx, configured trusted list, stub upstream recording received headers. The scheme half is kept and observed as `X-Forwarded-Proto` at the upstream; the backend reads no scheme (D-3). B no longer depends on A, because the access-log half moved to nginx's own log and A13 is tested separately. C7 carries its own explicit variable list instead of pointing at Part D. True order in §11: A ∥ B → C → D, with D1/D5/D6 free to land earlier. |
| B8 | AC-D2 needs a backend change | **Applied** (F-8) | New AC-A14: an empty DSN means monitoring is off, proven by a stub transport that must receive nothing. `.env.example` is corrected. PAC-14 §2 is amended in stage 2 (Inherited scope). Rev-0 AC-D2 is merged into A14. The web default is F-W2 / §10. |
| N1 | PAC-71 interplay | **Applied** | F-A4/F-A8 record its effects. AC-A10 runs named paths with a pool cap of 1 (any path holding one connection while taking a second deadlocks and fails), including PAC-71's guarded write and sign-in if merged by planning. AC-A13 logs the status sent on the wire under a status-replacing inner writer. The operator binary is counted in D-6. AC-D4(5) links the recovery doc. §6 states that PAC-71 tests must not change. |
| N2 | Background fan-out shares the cap | **Applied** | D-6 accepts it: waits are bounded by the budget (AC-A9 ends a starved request with 503). A separate background cap is a follow-up if needed. |
| N3 | Personal blocker not idempotent | **Applied** | Not assumed idempotent any more (D-5). AC-A17: no create starts without a full outbound limit left before the budget or drain ends, and every accepted create is recorded. Re-run creates at most one blocker per source event. §7 notes that existing SIGKILL duplicates are not cleaned. |
| N4 | F-A5 inventory incomplete; partial 200 | **Applied** | F-A5 adds personal sync (ICS without limits, creates per event), suggest and team availability, and the recap Slack calls. The Google client has no overall timeout. The 60-day listing is reclassified as abuse (web always sends a date), which closes Q-P1. AC-A2: a cancelled listing answers 503, never a truncated 200. |
| N5 | Budget expiry on redirect routes | **Applied** | AC-A2: browser-navigation callbacks end in their existing failure redirect, not JSON. |
| N6 | Health checks are new code; start_period; nginx health | **Applied** | AC-A16: health mode in three binaries, with no DB, no migrations, no secrets. D-10/AC-C3: backend start period 120 s, tested with a simulated 90 s migration. AC-B11: nginx health path. |
| N7 | MCP token for non-users; README; security.md:56; client sends no auth | **Applied** | D-12/AC-C4/C5: MCP becomes an opt-in compose profile, so its token is required only when enabled. AC-C4: the README shows the bearer header. AC-B10(5): `docs/security.md:56` fixed. F-C3 records `mcp/client.go:77`; it stays out of scope. |
| N8 | D-down runs all 26 down files; 006 scope | **Applied** | AC-D5: any failing down file on the path to 005 is in scope. 006's down covers the four `user_id` columns (F-D2). |
| N9 | Backup without secrets is no restore | **Applied** | F-D5 adds a finding: dumps hold plain-text provider credentials. AC-D3: save `.env` secrets with the dump, check that a pre-backup session still works after restore, treat dumps as secrets. Encryption at rest is a §10 follow-up. |
| N10 | Same-host proxy address | **Applied** | F-C8 records that the peer is the bridge gateway. AC-D2(3)(5): trust the gateway and bind nginx to `127.0.0.1` in that topology. |
| N11 | e2e wiring: template mount; injection variant | **Applied** | §6: the e2e mount changes in the same PR as any templating. AC-B3(b) adds an e2e variant with the API-base injection active. |
| N12 | nginx's 413 is HTML | **Applied** | AC-B8: nginx's own `/api/` 413 is JSON in the backend's error shape. AC-B2 covers headers on it too. |
| U1 | B5/B6 scheme halves unfalsifiable | **Applied** | Observation point is now the upstream's received `X-Forwarded-Proto` (B5 `https`, B6 `http`). |
| U2 | A9 "shutting down → 503" unobservable | **Applied** | Observable during the unready notice (AC-A6 step 1, AC-A11). |
| U3 | A6 "job allowed to finish" for recap | **Applied** | Per-user recap goroutines are tracked units (AC-A6 step 3). |
| U4 | D4 "events-linked tables" | **Applied** | AC-D3: row count for every table in the public schema. |
| U5 | `[review]` ACs pass on any wording | **Applied** | B10, D2, D4 carry enumerated checklists. Rev-0 D8 became AC-D7, a `[unit]` doc grep. |
| C1 | `handlers_booking.go:172-185` wrong | **Applied** | Now `:176-188` (loop) and `:181-183` (`continue`). |
| C2 | "base compose already pins the frontend" wrong | **Applied** | F-C1 now says it defaults to `latest` (`docker-compose.yml:71`). |
| C3 | §6 "no `/api/health` guard identified" wrong | **Applied** | §6 and Inherited scope name `e2e/global-setup.ts:61-70` and the web's `src/lib/api.ts:28-39`. |
| X | Consumers this breaks | **Applied** | Avatars (B1), docs (B2), e2e mount (N11), health consumers (C3), PAC-71 middleware (N1) are each handled above. |

Not applied: none. Moved: rev-0 Q-P1 closed, not moved (N4). WEB-timeouts,
WEB-sentry-default, email outbox and credential encryption moved to §10 as
follow-up tickets; they are not resolved in this spec.

New owner question: **Q-O4** (whether booking confirmation emails must
survive a restart before production; default no, logged plus a follow-up
outbox). Q-O1..Q-O3 are unchanged.
