# Spec — NEW-deploy-hardening: the production stack is not ready to run behind a TLS-terminating proxy

- **Linear**: none yet. Factory ticket id `NEW-deploy-hardening`; a PAC issue
  should be opened at intake and this file renamed or linked from it. Nothing
  was posted to Linear.
- **Status**: draft
- **Author**: spec-author (2026-10-28)
- **Surveyed at**: `Enach/clockwise-like` `origin/main` = `67b7966`;
  `Enach/smart-calendar-flow` `origin/main` = `9ceff3b`. Every `file:line`
  below is on those commits.
- **What was run to write this (factory README §3 rule 2)**: `git show`,
  `git grep`, `grep`, `sed`, `wc` on the two checkouts. **No test, build,
  `make verify`, container or nginx was run.** Every claim about behaviour is
  read from source; none is observed at runtime. Claims that depend on Go,
  nginx or Docker defaults (e.g. "SIGTERM ends the process at once", "nginx
  `proxy_read_timeout` defaults to 60 s") are documented defaults of those
  tools, not measurements, and are marked *(default)*.
- **Inherited scope**:
  - Owner decision 2026-10-27, "HTTPS is out of scope"
    (`.claude/factory/owner-decisions.md`): binding, implemented here in full.
  - Owner decision 2026-10-27, "Booking rate limits are abuse protection":
    **not** implemented here (it is its own follow-up of
    NEW-booking-rate-limit), but per-IP limits are only meaningful once this
    ticket makes the backend see the real client address (Part B), and the
    environment reference (Part D) must describe those settings once they
    exist.
  - `docs/security.md:52` (B1 row: "nginx HSTS still missing"),
    `docs/security.md:53` (B2 row: "only nginx exposes ports" — false today,
    see F-C3), `docs/security.md:85` (finding 3: "nginx production config
    lacks TLS / HSTS / CSP / rate-limit"). This work must make those lines
    true (AC-B10).
  - `docs/contributing.md:26` links `docs/runbook.md`, which does not exist.
  - No OpenAPI operation is changed except by AC-A9 (a readiness probe); the
    contract stage owns its shape. No `x-uncertain` marker or api-audit
    finding was found on `/api/health` (not checked exhaustively — the
    contract stage must re-check).

## 1. Problem

Paceday is meant to be deployed with `docker-compose.prod.yml` behind a load
balancer or reverse proxy that terminates TLS (owner, 2026-10-27). Read as it
is on `main`, that deployment has five classes of defect:

1. **The backend cannot tell clients apart.** Behind an external proxy every
   request reaches nginx from the proxy's address, nginx forwards that address
   as the client, and the backend's per-client abuse limits (sign-in detection,
   public booking, slot listing) then count every visitor in the world as one
   client. Twenty bookings in ten minutes, from anyone, would lock out every
   booker of every host.
2. **The server process is fragile.** It has no read or write timeouts, so a
   slow or malicious client can hold connections open indefinitely; it has no
   graceful shutdown, so every deploy or restart cuts in-flight requests and
   background jobs mid-write; its database pool is unbounded while Postgres
   accepts 100 connections *(default)*.
3. **The edge is half-configured.** The compose file publishes port 443 and
   mounts certificates for a TLS server that the bundled nginx config does not
   have; nginx sets none of the browser security headers; its proxy timeout
   *(default 60 s)* is shorter than or equal to the longest legitimate request
   the backend serves.
4. **The deployment is not reproducible or observable.** Every Paceday image is
   `:latest`, so "roll back" has no meaning; no service but Postgres has a
   health check; production Sentry events are tagged `development`; the MCP
   server is published on a plain-HTTP host port and crash-loops when its token
   is unset although the docs call the token optional.
5. **Operators have no runbook.** There is no backup, no restore procedure, no
   deploy order and no complete list of the settings the services read; five
   schema migrations have no down file.

Who pays: every booker and user (shared abuse budget, cut requests at deploy),
hosts (whose calendars the abuse limits are meant to protect), and the
operator (no rollback, no backups, no health signal).

## 2. Current behaviour

Findings are numbered `F-<part><n>` so that every AC in §4 can name the
finding it closes.

### Part A — backend process (`Enach/clockwise-like`)

- **F-A1. No server timeouts.** The backend serves with
  `http.ListenAndServe(":"+port, r)` (`backend/main.go:215`): no
  `ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout` or `IdleTimeout`. The MCP
  SSE server (`mcp/main.go:40`) and the static file server that serves the SPA
  and docs (`docker/fileserver/main.go:47`) do the same.
- **F-A2. No graceful shutdown.** There is no `signal.Notify`,
  `signal.NotifyContext` or `Server.Shutdown` anywhere in `backend/`, `mcp/` or
  `docker/` (`git grep` returned nothing). SIGTERM therefore ends the process
  at once *(Go default)*: in-flight requests are cut and cron jobs stop
  mid-run. When `ListenAndServe` returns, `log.Fatalf` (`backend/main.go:216`)
  calls `os.Exit`, which skips every `defer` in `main`: `sentry.Flush`
  (`:29`), `db.Close` (`:112`) and every cron `Stop` (`:123`, `:149`, `:167`,
  `:180`, `:202`).
- **F-A3. Cron shutdown would not wait even if it ran.** Four jobs use
  `robfig/cron` (`backend/main.go:129`, `:155`, `:171`, `:184`); their `Stop()`
  is deferred without waiting on the context it returns, so a running job is
  not awaited. The focus scheduler's `Stop` closes its ticker and explicitly
  does not wait for a tick in progress (`backend/scheduler/cron.go:83-93`, "A
  tick already in progress finishes on its own"). Job budgets today: personal
  blocker 5 min per user (`backend/main.go:138`), auto-decline 5 min (`:157`),
  recap 55 s (`:173`), manager detection 10 min (`:186`), focus tick 10 min
  per run (`backend/scheduler/cron.go:59`).
- **F-A4. Unbounded database pool.** `storage.Open` calls `sql.Open` and
  `Ping` only (`backend/storage/db.go:17-28`): no `SetMaxOpenConns`,
  `SetMaxIdleConns`, `SetConnMaxLifetime` or `SetConnMaxIdleTime` anywhere
  (`git grep` returned nothing). Postgres runs with its default
  `max_connections` = 100 *(default)*; the prod compose does not change it
  (`docker-compose.prod.yml:11-24`).
- **F-A5. Longest legitimate request, and requests with no bound at all.**
  - The longest *bounded* request handler is meeting-brief regeneration: 60 s
    (`backend/api/handlers_meeting_briefs.go:80`). Others: daily recap 10 s
    (`backend/api/handlers_daily_recap.go:172`), manager 10 s
    (`backend/api/handlers_manager.go:119`).
  - Natural-language parse calls the user's LLM with the request context only
    (`backend/api/handlers_nlp.go:28` → `backend/nlp/parser.go:166`), and may
    call it more than once (`parser.go:239`). The LLM HTTP clients set no
    `Timeout` (`backend/nlp/user_destination.go:145-148`,
    `backend/nlp/destination.go:202-205`). Today nothing bounds that request.
  - Slack and Notion OAuth token exchanges are built on
    `context.Background()` with `http.DefaultClient`, so not even a client
    disconnect cancels them (`backend/api/handlers_integrations.go:124,130`,
    `:254,261`).
  - Calendar and conferencing calls use the request context with
    `http.DefaultClient` (`backend/calendar/webcal_client.go:64`,
    `backend/calendar/outlook_client.go:56`, `backend/conference/zoom.go:50`,
    `backend/conference/teams.go:59`), so a request deadline would bound them.
    The Google calendar client dials with a 30 s timeout
    (`backend/calendar/guard.go:52`); free/busy queries are capped at 5 s each
    (`backend/engine/freebusy_service.go:133`).
  - Slot listing without a date asks the engine about 60 days in turn
    (NEW-booking-rate-limit §2, `backend/api/handlers_booking.go:172-185`). Its
    worst-case duration was not measured (§8 Q-P1).
- **F-A6. No request body cap in the backend.** No `http.MaxBytesReader`
  anywhere in `backend/` (the only `LimitReader` is on a Zoom response,
  `backend/conference/zoom_oauth.go:64`). The only cap is nginx's implicit
  `client_max_body_size` *(default 1 MiB)*; nginx sets none
  (`nginx/nginx.conf:1-17`). No upload or multipart request endpoint exists
  (`git grep FormFile|multipart` finds only an outgoing mail body,
  `backend/engine/booking.go:304`).
- **F-A7. Health is liveness only.** `GET /api/health` (`backend/api/routes.go:21`)
  returns a constant `{"status":"ok","version":"0.1.0"}`
  (`backend/api/handlers_health.go:8-14`): it does not touch the database and
  the version is hard-coded. Nothing reports "not ready" during startup
  migrations or shutdown.
- **F-A8. Access log lacks status and client.** `loggingMiddleware` logs
  method, path and duration only (`backend/api/middleware.go:105-111`); no
  status code, no client address (also acknowledged at `docs/security.md:53`).
  The path is logged without the query string, which is correct and must stay.
- **F-A9. Client address and scheme today.** The only client-address logic is
  `clientIP`: the socket peer, or `X-Real-IP` when the peer is in
  `TRUSTED_PROXIES`; `X-Forwarded-For` is never read
  (`backend/api/ratelimit_detect.go:12-18`, `:33`, `:94-114`), shared by the
  booking limiters (`backend/api/ratelimit_booking.go:15-18`). The backend
  never derives a scheme from the request: no `r.TLS`, `X-Forwarded-Proto`,
  `r.URL.Scheme` or `r.Host` use outside LLM destination checks (`git grep`).
  The `Secure` cookie attribute is a static setting, on unless
  `PACEDAY_INSECURE_COOKIES=true` (`backend/config/cookies.go:19-29`,
  `backend/api/cookies.go:9-21`, `backend/main.go:88-92`). Absolute URLs come
  from configured settings, never from the request: `FRONTEND_URL`
  (`backend/main.go:209`, used at `backend/api/handlers_auth.go:83,98`),
  `APP_URL` (`backend/api/handlers_sso.go:67,124`,
  `backend/api/handlers_sso_stepup.go:174`), `BASE_URL` with a hard-coded
  fallback `https://paceday.app` (`backend/api/handlers_teams.go:627-636`), and
  the provider redirect URLs. So behind a TLS proxy the backend is already
  scheme-correct **provided the operator configures `https://` public URLs and
  leaves Secure on**; nothing warns when that pairing is wrong (an `http://`
  public URL with Secure cookies makes browsers drop the session cookie on a
  non-localhost host).
- **F-A10. Sentry defaults in production.** `initSentry` falls back to a
  hard-coded DSN and to environment `development`
  (`backend/sentry.go:16`, `:106-108`); `SendDefaultPII` false, traces 0,
  scrubbing on (`:109-111`). The prod compose passes no `SENTRY_*` variable to
  the backend (`docker-compose.prod.yml:33-98`), so production events are sent
  to the default project tagged `development` with an empty release.

### Part B — nginx edge

- **F-B1. HTTP only already, but compose expects TLS.** The bundled config has
  one `listen 80` server and no redirect (`nginx/nginx.conf:2-3`); the prod
  compose nevertheless publishes `443:443` and mounts `${TLS_CERT_DIR}` at
  `/etc/letsencrypt` (`docker-compose.prod.yml:134-140`), and `.env.example`
  documents `TLS_CERT_DIR` (`.env.example:112-114`). Same config is used by the
  dev/base compose (`docker-compose.yml:93-98`) and the e2e stack
  (`e2e/docker-compose.e2e.yml:222-229`), which makes it e2e-testable.
- **F-B2. No security headers.** `nginx/nginx.conf:1-17` sets no
  `Content-Security-Policy`, `X-Frame-Options`, `X-Content-Type-Options` or
  `Referrer-Policy`, and neither do the backend middlewares
  (`backend/api/routes.go:17-19`) nor the file server
  (`docker/fileserver/main.go:29-45`). What a CSP must allow, from the web
  repo: Google Fonts stylesheet and font hosts
  (`smart-calendar-flow:index.html:18-20`), the Sentry ingest host
  (`smart-calendar-flow:src/lib/sentry.ts:19-20`, `.ingest.de.sentry.io`), a
  possibly cross-origin API when `API_BASE_URL`/`VITE_API_URL` is set
  (`smart-calendar-flow:src/lib/api.ts:16-22`,
  `smart-calendar-flow:Dockerfile.local:4-5` uses
  `https://paceday-api.nhochart.ovh`), an inline `<script>` the file server
  injects into `index.html` to carry the API base URL
  (`docker/fileserver/main.go:19-25`), and an inline `<style>` written by the
  chart component (`smart-calendar-flow:src/components/ui/chart.tsx`,
  `dangerouslySetInnerHTML`).
- **F-B3. The real client address is lost behind an external proxy.** nginx
  sets `X-Real-IP $remote_addr` on both locations (`nginx/nginx.conf:8`,
  `:15`) and has no `real_ip` configuration, so behind a load balancer
  `$remote_addr` is the load balancer. The backend trusts nginx
  (`docker-compose.prod.yml:47` defaults `TRUSTED_PROXIES` to the Docker
  private ranges) and so keys every abuse limit on the load balancer's
  address (F-A9). nginx forwards `X-Forwarded-For $proxy_add_x_forwarded_for`
  for `/api/` only (`:9`), appending to whatever the client sent; it forwards
  no `X-Forwarded-Proto` (the web repo's local config does, with `$scheme`:
  `smart-calendar-flow:nginx.local.conf:14`).
- **F-B4. Proxy timeouts and limits are implicit.** No `proxy_read_timeout`,
  `proxy_send_timeout`, `proxy_connect_timeout`, `client_max_body_size`,
  `client_header_timeout`/`client_body_timeout`, `limit_req` or `limit_conn`
  (`nginx/nginx.conf:1-17`). With the 60 s *(default)* `proxy_read_timeout`,
  a 60 s meeting-brief regeneration (F-A5) races nginx's 504, and an
  unbounded NLP parse is always cut at 60 s by nginx rather than by the
  backend.
- **F-B5. No `proxy_http_version 1.1`** on the `/api/` location, so nginx
  opens a new upstream connection per request *(default HTTP/1.0 upstream)*;
  the web repo's local config sets it (`smart-calendar-flow:nginx.local.conf:10`).
- **F-B6. The docs container is unreachable.** The prod compose runs a `docs`
  service (`docker-compose.prod.yml:108-110`) but nginx has no route to it
  (`nginx/nginx.conf:5-16`); the docs site has its own `docs/vercel.json`.

### Part C — compose, images, exposure

- **F-C1. Floating image tags.** `enach/paceday-backend:latest`,
  `paceday-frontend:latest`, `paceday-docs:latest`, `paceday-mcp:latest`
  (`docker-compose.prod.yml:28`, `:102`, `:109`, `:114`) and `nginx:alpine`
  (`:129`) float; `postgres:16-alpine` floats within 16 (`:12`). The base
  compose already pins the frontend through `FRONTEND_IMAGE_TAG`
  (`docker-compose.yml:68-71`). No image-publishing pipeline is in this repo
  (`.github/` absent; `docs/adr/0001-tech-stack.md:17` refers to a workflow
  that is not here).
- **F-C2. Health checks.** Only Postgres has one (`docker-compose.prod.yml:20-24`).
  Backend, frontend, docs and MCP images are `FROM scratch` with no shell or
  HTTP client (`backend/Dockerfile:9-14`, `docker/frontend.dockerfile:18-22`,
  `docs/Dockerfile:15-19`, `mcp/Dockerfile:10-15`), so a compose health check
  must call the service binary itself; the e2e stack already does this for its
  stand-ins (`e2e/docker-compose.e2e.yml:179`, `:199`, `-healthcheck` flag).
  nginx `depends_on` backend/frontend without a health condition
  (`docker-compose.prod.yml:131-133`).
- **F-C3. MCP server exposure.** The MCP SSE server is published on every
  host interface as plain HTTP (`docker-compose.prod.yml:118-119`; also
  `docker-compose.yml:87-88`), bypassing nginx and the TLS proxy, so its
  bearer token travels in clear if used from outside the host. It advertises
  its message endpoint as `http://0.0.0.0:<port>` (`mcp/main.go:38`), which a
  remote client cannot use. `docs/security.md:53` says "only nginx exposes
  ports", which is false.
- **F-C4. MCP token contradiction.** `mcp/main.go:29-31` refuses to start in
  SSE mode without `MCP_AUTH_TOKEN`; the prod compose defaults it to empty and
  calls it optional (`docker-compose.prod.yml:124-125`), as does
  `.env.example:109-110` ("leave empty for open access"). With the documented
  default, `mcp-server` exits at start and `restart: unless-stopped`
  (`:115`) restarts it forever.
- **F-C5. No stop grace period or log rotation.** No `stop_grace_period`
  (Docker sends SIGKILL 10 s after SIGTERM *(default)*) and no `logging:`
  options (json-file logs grow without bound *(default)*) anywhere in
  `docker-compose.prod.yml`.
- **F-C6. Stale and missing settings in compose.** `SESSION_SECRET` is marked
  required (`docker-compose.prod.yml:59-60`, `.env.example:7`) but no code
  reads it (the `os.Getenv` survey below). `APP_URL`, `BASE_URL`,
  `PACEDAY_ADMIN_EMAILS`, `SMTP_*`, `SENTRY_*` are read by the backend but not
  passed by the prod compose (`docker-compose.prod.yml:33-98`), so SSO,
  SSO step-up, invitation links and mail cannot be configured in production
  without editing the compose file.
- **F-C7. Restart policies** are `unless-stopped` on every prod service
  (`docker-compose.prod.yml:13`, `:29`, `:105`, `:110`, `:115`, `:130`). This
  is adequate and is not changed.
- **F-C8. Ports.** Postgres and the backend are not published in prod (good,
  `docker-compose.prod.yml:11-98`); nginx publishes `80` on every interface
  (`:135`), so a client that can reach the host directly bypasses the TLS
  proxy. The DB connection is `sslmode=disable` inside the compose network
  (`:35`), acceptable on a private bridge network.

### Part D — operations

- **F-D1. Settings read vs documented.** Variables read by `backend/` and
  `mcp/` (`git grep -E 'os\.(Getenv|LookupEnv)'` plus the `config.Env*` and
  `EnvAdminEmails` constants): `ALLOWED_ORIGIN`, `APP_URL`, `BACKEND_URL`,
  `BASE_URL`, `DATABASE_URL`, `FRONTEND_URL`, `GOOGLE_CALENDAR_API_BASE_URL`,
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
  `ZOOM_REDIRECT_URL`; plus `API_BASE_URL` in the file server
  (`docker/fileserver/main.go:22`) and `PACEDAY_TEST_DATABASE_URL` (tests
  only, `backend/internal/testdb/testdb.go:20`).
  **Absent from `.env.example`** (`.env.example:1-125`): `ALLOWED_ORIGIN`,
  `APP_URL`, `BASE_URL`, `FRONTEND_URL`, `PACEDAY_ADMIN_EMAILS`,
  `PACEDAY_GOOGLE_IDP_BASE_URL`, `PACEDAY_MICROSOFT_IDP_BASE_URL`,
  `PACEDAY_PUBLIC_BOOKING_LIMIT`, `PACEDAY_PUBLIC_SLOTS_LIMIT`, `SMTP_*`
  (five), `MCP_TRANSPORT`, `MCP_PORT`, `BACKEND_URL`, `PORT`,
  `DATABASE_URL`, `API_BASE_URL`. **Present but unread**: `SESSION_SECRET`
  (`:7`), `TLS_CERT_DIR` (`:114`, used only by the compose mount to be
  removed). `.env.example:121` ships a real-looking Sentry DSN as the value.
- **F-D2. Migrations.** Migrations run automatically at backend start, up
  only (`backend/storage/db.go:25`, `:44`). Five have no down file:
  `006_auth`, `007_organizations`, `014_workspace_connections`,
  `015_daily_recap`, `016_manager` (`backend/storage/migrations/`, listing);
  the factory README requires a `.down.sql` for new migrations (stage 3), and
  `golang-migrate` cannot step down past a version whose down file is
  missing *(library behaviour)*.
- **F-D3. Backups.** No backup job, script, or restore procedure exists in
  the repo (`git grep -i 'backup|pg_dump|restore'` outside specs/plans finds
  only plan prose). Postgres data lives in the named volume `postgres_data`
  (`docker-compose.prod.yml:18-19`, `:142-143`). The factory's release gate
  requires "migrations applied to a backed-up database" (factory README §2,
  stage 7).
- **F-D4. No runbook or deploy order.** `docs/runbook.md` is linked
  (`docs/contributing.md:26`) and missing. `docker-compose.prod.yml:3-6` is the
  only deploy instruction.

## 3. Desired behaviour

After this work an operator can put the stack behind any TLS-terminating load
balancer or reverse proxy and get correct behaviour without editing code:

- The backend sees each visitor's real address, so abuse protection counts
  per visitor; a visitor cannot pick their own address by sending a
  forwarding header, because forwarding headers are believed only from proxy
  addresses the operator configured. Nothing is trusted by default.
- The stack speaks plain HTTP only and never redirects to HTTPS; HSTS and TLS
  are the proxy's job and the documentation says so. Every page carries the
  non-TLS browser security headers.
- Slow or stuck clients cannot hold server resources indefinitely; every
  request has a bounded lifetime, and the bounds are ordered so that the
  backend's own error, not a proxy's cut, is what a client sees when a
  legitimate slow operation (an AI call) runs out of time.
- A deploy or restart lets in-flight requests and running background jobs
  finish within a stated budget, then stops cleanly, flushing error reports.
- The database pool never asks Postgres for more connections than it can
  give, even with an old and a new backend running side by side during a
  deploy.
- Every service reports whether it is alive, and the backend reports whether
  it is ready to serve (database reachable, not shutting down).
- The deployment names exact image versions, so a rollback is "deploy the
  previous version"; the MCP server is not reachable from outside the host
  unless the owner decides otherwise; production error reports carry the
  right environment and release.
- An operator has one document listing every setting with its default and
  purpose, one backup-and-restore procedure that has been rehearsed, and one
  deploy order including what to do when a deploy fails.

### Values (all factory-assumed unless marked; justified in Decisions)

| Bound | Value |
|---|---|
| Time to receive request headers | 10 s |
| Time to receive a whole request | 30 s |
| Backend's own deadline for one request (handler budget) | 60 s |
| Time to write a whole response (hard backstop) | 90 s |
| Idle keep-alive connection | 120 s |
| nginx: upstream read / send | 100 s |
| Largest accepted request body | 1 MiB |
| Shutdown drain budget (requests and running jobs) | 30 s |
| Container stop grace period | 40 s |
| DB pool: open / idle / lifetime / idle time | 20 / 10 / 30 min / 5 min |
| Readiness DB check timeout | 2 s |

## 4. Acceptance criteria

Test levels: `[unit]` Go/TS unit tests; `[integration]` a test that starts a
real process or container (backend binary, nginx with the shipped config,
Postgres) but not the full stack; `[e2e]` the Playwright suite against the
compose e2e stack; `[manual-ops]` a written, dated rehearsal recorded in the
PR or runbook (not automatable by agents). Each AC names the finding it
closes.

### Part A — backend process: timeouts, deadlines, shutdown, pool (F-A1..F-A10)

AC-A1 (F-A1). Given the backend is running, when a client opens a connection
and sends request headers slower than the header bound (one byte per second),
then the server closes the connection no later than header bound + 1 s; and
when a client sends headers then stalls in the body, the connection is closed
no later than the whole-request bound + 1 s. [integration]

AC-A2 (F-A1, F-A5). Given a request whose handler runs longer than the
handler budget (a test route or a stubbed slow dependency), when the budget
elapses, then the handler's context is cancelled, the client receives a JSON
error response with a 5xx status (not a dropped connection), and the response
is complete before the write backstop. [unit]

AC-A3 (F-A5). Given a natural-language parse whose AI provider never answers,
when the handler budget elapses, then the request ends with the backend's
error response and no outbound AI request is still open afterwards; the same
holds for Slack and Notion connection callbacks whose token endpoint never
answers. [unit]

AC-A4 (F-A5). Given meeting-brief regeneration, whose own deadline is 60 s
today, when it runs for its full budget, then the server neither cuts the
connection nor overrides its error: the client gets the brief or the
handler's own error. The handler budget is never shorter than any deadline a
handler sets itself. [unit]

AC-A5 (F-A1). Given an idle keep-alive connection, when the idle bound
elapses with no request, then the server closes it. [integration]

AC-A6 (F-A2, F-A3). Given the backend serving a request that takes 5 s and a
background job mid-run, when the process receives SIGTERM, then: it stops
accepting new connections at once; readiness (AC-A9) reports not ready from
that moment; the in-flight request completes with its normal response; no
background job starts a new run; the running job is allowed to finish; and
the process exits 0 within the drain budget. [integration]

AC-A7 (F-A2, F-A3, F-A10). Given in-flight work that outlives the drain
budget, when the budget elapses, then the process cancels it, closes the
database pool, flushes pending error reports, logs which work was abandoned,
and exits non-zero within drain budget + 5 s. Given SIGINT, the behaviour is
the same as SIGTERM. [integration]

AC-A8 (F-A4). Given the default configuration, when more concurrent requests
need the database than the open-connection cap, then Postgres never sees more
than the cap of connections from one backend process (measured from
`pg_stat_activity`), excess requests wait rather than fail, and idle
connections beyond the idle cap are closed. The caps are operator settings
with the stated defaults; a non-positive or unparseable value is ignored
with a startup warning and the default applies. [integration]

AC-A9 (F-A7). Given the backend has finished migrations and the database
answers within the readiness timeout, when the readiness probe is called,
then it answers success; given the database is unreachable, or the process is
shutting down, it answers 503 within readiness timeout + 1 s. The existing
liveness response keeps its current status and body for as long as the
process can serve HTTP. Neither probe requires authentication or is counted
by any abuse limit. [contract, unit, integration]

AC-A10 (F-A6). Given any backend route, when a request body larger than the
body cap is sent directly to the backend (not through nginx), then the
backend answers 413 with the JSON error shape and the handler does not
process it; a body exactly at the cap is processed normally. [unit]

AC-A11 (F-A8). Given any request, when it completes, then one access-log line
records method, path without query string, status, duration and the client
address as the abuse limits resolve it; given a request whose query string
carries a token, the token does not appear in the log. [unit]

AC-A12 (F-A9). Given cookies are Secure (the default), when any configured
public URL the backend uses for redirects or links (front-end URL, app URL,
invitation base URL) is `http://` on a host other than `localhost`/`127.0.0.1`,
then the backend logs one startup warning naming the setting and saying the
browser will drop the session cookie; no warning when all are `https://` or
when insecure cookies are explicitly enabled. Startup is never refused for
this. [unit]

AC-A13 (F-A1). Given the MCP SSE server and the static file server, when a
client sends headers slower than the header bound, then each closes the
connection within the bound + 1 s; an established MCP SSE stream that is
idle but open is **not** closed by any write bound (it is long-lived by
design). [integration]

### Part B — nginx: HTTP only, headers, trusted proxy, client address (F-B1..F-B6, F-A9)

AC-B1 (F-B1). Given the shipped nginx configuration, when it is loaded, then
it listens only on plain HTTP, has no TLS server, and no request to any path
is answered with a redirect to `https://` (checked for `/`, `/api/health`, a
SPA deep link, and a request carrying `X-Forwarded-Proto: http` from a
trusted proxy). [integration, e2e]

AC-B2 (F-B2). Given any response served through nginx — SPA page, static
asset, API JSON, API error, SPA fallback — when it reaches the client, then it
carries exactly one each of `X-Content-Type-Options: nosniff`,
`X-Frame-Options: DENY`, `Referrer-Policy: strict-origin-when-cross-origin`,
and a `Content-Security-Policy`; and it carries **no**
`Strict-Transport-Security`. [e2e]

AC-B3 (F-B2). Given the CSP, when it is read, then it forbids framing by
any origin, forbids plugins/objects, restricts the base URI and form targets
to the site, and does **not** allow inline script or `eval`. Given the e2e
suite runs every journey through nginx, then the browser reports zero CSP
violations (collected from the page's security-policy violation events) on
every test, including with the runtime API-base injection active and with
Sentry initialisation enabled. [e2e]

AC-B4 (F-B2). Given the operator configures a cross-origin API address or an
additional connect destination, when the stack starts, then the CSP allows
connections to exactly those origins in addition to the defaults, and no
others. [integration]

AC-B5 (F-B3). Given nginx is configured with a trusted-proxy list containing
address P, when a request arrives from P carrying forwarding headers that name
client C (and C prepended by a further forged entry), then the backend's
abuse limits and access log see C — the right-most address not in the
trusted list — and the scheme the proxy reported is passed upstream. [e2e]

AC-B6 (F-B3). Given a request from an address **not** in the trusted-proxy
list, when it carries `X-Forwarded-For`, `X-Real-IP` and `X-Forwarded-Proto`
headers naming another client and `https`, then the backend sees the socket
peer as the client and the scheme as `http`; the forged headers reach the
backend neither as `X-Real-IP` nor as `X-Forwarded-Proto`. [e2e]

AC-B7 (F-B3). Given no trusted-proxy list is configured (the default), when
any request carries forwarding headers, then they are ignored as in AC-B6.
[integration]

AC-B8 (F-B4). Given an API request the backend answers after 70 s (longer
than nginx's old default, shorter than the backend's write backstop), when it
is proxied, then the client receives the backend's response, not a 504 from
nginx. Given a body larger than the body cap sent through nginx, then nginx
answers 413 without contacting the backend. [integration]

AC-B9 (F-B5). Given sequential API requests through nginx, when they are
served, then nginx reuses upstream connections (upstream keep-alive). Proven
by the backend observing fewer new connections than requests. [integration]

AC-B10 (inherited, F-C3). Given `docs/security.md`, when it is read after this
work, then the B1 and B2 rows and finding 3 make no claim the configuration
contradicts: TLS and HSTS are stated as the proxy's responsibility; CSP and
the other headers are stated as set by nginx; "only nginx exposes ports" is
true or corrected. [review]

### Part C — compose, images, health checks, MCP exposure (F-C1..F-C8, F-A10, F-B6)

AC-C1 (F-B1). Given the production compose file, when it is read, then it
publishes no port 443 and mounts no certificate directory, and `.env.example`
no longer documents a certificate directory. [unit — a file test alongside
the existing deployment-file tests, `backend/config/deployment_files_test.go`]

AC-C2 (F-C1). Given the production compose file, when it is rendered without
a version setting, then rendering fails with a message naming the missing
setting; when a version is set, every Paceday image uses that exact tag, and
nginx and Postgres use exact versions (no `latest`, no bare major). No image
reference in the file ends in `:latest`. [unit]

AC-C3 (F-C2; depends on AC-A9). Given the production stack is up, when a
service stops answering, then its health check turns unhealthy within 3
intervals: backend (readiness, via its own binary), MCP server and file
servers (liveness via their own binaries), nginx (a request to its own
health path). nginx starts only after backend and frontend are healthy.
[integration]

AC-C4 (F-C3, owner question Q-O1). Given the production compose file, when
it is rendered, then the MCP server publishes no port on a non-loopback
interface. [unit]

AC-C5 (F-C4). Given the production compose and `.env.example`, when read,
then the MCP token is described as required for the SSE transport, and
rendering the compose file without it fails with a message naming it (rather
than a crash-looping container). [unit]

AC-C6 (F-C5; depends on Part A's drain budget). Given the production compose
file, then every Paceday service has a stop grace period of at least drain
budget + 10 s, and every service's logs are size-capped and rotated.
[unit] Given `docker compose stop backend` during a 5 s request, the request
completes (AC-A6 through the real stop path). [integration]

AC-C7 (F-A10, F-C6). Given the production compose file, then it passes every
backend setting listed as production-relevant in the environment reference
(Part D) — including the error-monitoring DSN, environment and release, the
app and invitation base URLs, the step-up admin list and mail settings — and
does not pass `SESSION_SECRET`; the error-monitoring environment defaults to
`production` in that file. [unit]

AC-C8 (F-B6). Given the production compose file, then it either routes the
docs service through nginx or does not run it; it does not run a container
nothing can reach. [unit]

AC-C9 (F-C8). Given the production compose file, then the address nginx
binds on the host is an operator setting (default: all interfaces, as today),
documented in the proxy requirements (AC-D3) as "restrict to the proxy's
network". [unit]

### Part D — operations documentation and rollback readiness (F-D1..F-D4)

AC-D1 (F-D1). Given the source tree, when a test enumerates every environment
variable read by the backend, the MCP server and the file server, then each
is either documented in `.env.example` with purpose, default and
"required/optional/test-only", or listed in an explicit test-only allowlist;
and every variable in `.env.example` is read by some service or by the
compose file. The test fails today on F-D1's lists. [unit]

AC-D2 (F-D1). Given `.env.example`, then no value in it is a real credential
or a real DSN; monitoring is off until the operator sets it. [unit]

AC-D3 (F-D4). Given the operator documentation, when read, then it states
the proxy requirements: TLS and HSTS (with a recommended value) at the
proxy; forwarding headers the proxy must set and how to configure the trusted
list for nginx; the proxy's idle/read timeout must exceed nginx's upstream
read bound; the nginx host port must not be reachable except from the proxy.
[review]

AC-D4 (F-D3). Given the backup procedure, when an operator follows it on a
running production stack, then it produces a consistent dump without
stopping the backend, on a stated schedule with stated retention; and given
a fresh host, following the restore procedure with that dump and the same
image version yields a stack whose readiness probe succeeds and whose row
counts for users, events-linked tables and bookings match the source.
Rehearsed once, date and outcome recorded in the runbook. [manual-ops,
integration for the script part]

AC-D5 (F-D4, F-D2). Given the runbook, then it states the deploy order
(back up → pull pinned images → start Postgres → start backend, which
migrates → wait for readiness → start the rest), what an operator does when
the backend fails to become ready after a migration (restore the backup and
redeploy the previous version; never edit `schema_migrations` by hand), and
how to roll back an image without a schema change. [review, manual-ops]

AC-D6 (F-D2). Given the migrations directory, when a test lists it, then
every `.up.sql` has a matching `.down.sql`; for 006, 007, 014, 015 and 016
the down file reverses the schema change and begins with a comment stating
what data it destroys. Given an empty database migrated up to the latest
version, when migrated down to 005 and up again, then both directions
succeed. [unit, integration]

AC-D7 (F-D4). Given `docs/contributing.md`, then its runbook link resolves
to an existing document. [unit]

AC-D8 (F-A10). Given the production configuration documented in the env
reference, then the error-monitoring release is set to the deployed image
version, and the env reference says how. [review]

### What the falsifiers look like today

Every AC above fails on `67b7966` as read: A1/A5/A13 (no timeouts, F-A1), A2-A4
(no handler budget, F-A5), A6/A7 (no signal handling, F-A2), A8 (no pool caps,
F-A4), A9 (no readiness), A10 (no MaxBytes), A11 (no status/client in the
log), A12 (no warning), B1 (443 published — the nginx half already passes;
AC-B1 is a regression guard for it), B2-B4 (no headers), B5-B7 (no real_ip),
B8 (60 s default, no body cap), B9 (HTTP/1.0 upstream), C1-C9, D1-D7 per
the findings cited. These "fails today" statements are read from source, not
run.

## 5. Explicitly out of scope

- **TLS and HSTS in Paceday.** Owner, 2026-10-27: the proxy terminates TLS
  and sets HSTS. Paceday never redirects to HTTPS.
- **Making booking abuse limits operator settings, and limiting the link-info
  read at 1000/s per IP.** Owner decisions 2026-10-27, implemented by the
  NEW-booking-rate-limit follow-up. Today the counts are fixed in production
  (`backend/config/booking_limits.go:9-15`). This spec only makes the
  address those limits key on correct (Part B) and requires the env reference
  to document whatever settings exist when Part D lands.
- **nginx-level rate limiting (`limit_req`/`limit_conn`).** Abuse protection
  is application-level by owner decision; an nginx limit would key on the
  same address and duplicate it. `docs/security.md:85` is updated to say so.
- **Cross-replica abuse limits** (API-062 residue). Counts stay per process.
- **An image build/publish pipeline.** No CI is in the repo (F-C1); this spec
  makes the compose file demand a version, not produce one.
- **Making the MCP server reachable over the public origin.** Pending owner
  question Q-O1; until answered it is loopback-only (AC-C4). Fixing the
  advertised `0.0.0.0` endpoint (F-C3) belongs to that follow-up.
- **Changing `/api/health`'s body** (e.g. the hard-coded version). Liveness is
  preserved as is; readiness is new.
- **Off-host backup storage, encryption at rest, point-in-time recovery.**
  The runbook names them as operator choices; the procedure here is dumps.
- **Postgres tuning** (`max_connections`, memory). The pool cap is sized to
  the default instead.
- **Multi-replica / zero-downtime deploys.** Sizing (pool caps) allows two
  backends during a deploy; orchestrating it is not in scope.
- **The web repo's local-only `nginx.local.conf`/`Dockerfile.local`.** Not a
  production artefact; it is not changed. (Its hard-coded
  `paceday-api.nhochart.ovh` default is noted in F-B2 only for CSP sizing.)

## 6. What must not change

- HTTP only, no redirect (already true: `nginx/nginx.conf:2-3`). No test
  protects it today — AC-B1 adds the guard.
- The client-identification rule (socket peer; `X-Real-IP` only from
  `TRUSTED_PROXIES`; `X-Forwarded-For` never read by the backend; an
  unparseable entry trusts less). Protected by
  `backend/api/ratelimit_detect_test.go` and the booking limiter tests. This
  work changes what nginx puts in `X-Real-IP`, not how the backend reads it.
- Cookies Secure by default; insecure only by explicit opt-in. Protected by
  `backend/config/cookies_test.go`.
- `/api/health` status and body. Protected by whatever e2e uses it (the plan
  must name the test; none was identified here).
- Startup refusals for unsafe stand-in configuration (`backend/main.go:40-67`).
  Protected by `backend/config/endpoints_test.go`, `idp_test.go`.
- Access logs never contain query strings (`backend/api/middleware.go:109`).
  No test protects it today — AC-A11 adds one.
- The e2e stack keeps working with `PACEDAY_INSECURE_COOKIES=true` over plain
  HTTP (`e2e/docker-compose.e2e.yml:135`) and with no trusted proxy
  configured, i.e. all e2e traffic still one client unless a test configures
  otherwise (NEW-booking-rate-limit AC-13 depends on it).
- The deployment-file assertions already in
  `backend/config/deployment_files_test.go` (no Ollama, no ambient identity).

## 7. Rollback

- Part A: revert the backend commit; no migration, no stored state. Timeouts,
  pool caps and shutdown handling disappear together.
- Part B: revert the nginx config. If an operator has configured a trusted
  proxy list, it is then ignored and abuse limits key on the proxy again
  (today's behaviour). If the CSP breaks a page in production, reverting the
  header lines alone is safe; there is no persisted state.
- Part C: revert the compose file. An operator who already set a version and
  removed `SESSION_SECRET` is unaffected by the revert except that the
  version is ignored (`:latest` again).
- Part D: docs revert freely. The added down migrations are only used when an
  operator migrates down; running 006's down **destroys every user account**
  and its dependants, which is why the runbook's rollback path is "restore
  the backup", not "migrate down". Reverting the down files restores the
  current state (down impossible past 016).

## 8. Open questions

### Owner (product-level)

Q-O1. **Should the MCP server be reachable from outside the host?** Today it
is published on a plain-HTTP port on every interface and advertises an
unusable `0.0.0.0` endpoint (F-C3). Options: (a) loopback only — local MCP
clients on the host; (b) served through the public origin under a path,
behind the TLS proxy, with its bearer token; (c) not deployed in production.
Default applied by this spec: **(a)**. Owner: product owner.

Q-O2. **May the public booking page be embedded in an iframe on other
sites?** The owner decision says nginx sets X-Frame-Options; this spec sets
`DENY` and `frame-ancestors 'none'` everywhere, which forbids embedding the
booking page. `docs/src/content/docs/scheduling/what-are-scheduling-links.mdx:23`
says "embed the link on a help page", read here as placing a link, not a
frame. Default: **no framing anywhere**. Owner: product owner.

Q-O3. **Backup objective.** How much data may be lost (RPO) and how long
must backups be kept? Default: **daily logical dump, 14 days retained,
RPO 24 h**, stored on the host volume with a documented copy-off step.
Owner: product owner / operator.

### Plan stage (not for the owner)

Q-P1. Worst-case duration of the 60-day slot listing with cold free/busy
caches (F-A5). If it can exceed the 60 s handler budget, the plan must either
bound it inside the handler or show it cannot; the budget is not raised to
fit it without a challenge.

Q-P2. Mechanism for a CSP without inline script while the file server
injects the API base URL inline (F-B2): a hash computed at file-server start,
a nonce, or moving the value to a separately served script. Whichever is
chosen, the web repo must not need a Lovable-edited change to keep loading;
if it does, cross-repo ordering (factory README §1) applies.

Q-P3. How nginx gets the operator's trusted-proxy list and extra CSP origins
at start (template substitution, an included file). AC-B4/B5/B7 constrain
behaviour only.

Q-P4. Whether the backend's own trusted-proxy default (Docker private ranges,
`docker-compose.prod.yml:47`) should narrow to the compose network once nginx
is the only peer. Kept as is unless the challenger finds a peer other than
nginx can reach the backend.

## Decisions

Owner-decided:

- **D-1 (owner, 2026-10-27).** Paceday ships an HTTP server; the proxy
  terminates TLS. nginx is HTTP only and never redirects to HTTPS. The 443
  publish and certificate mount are removed. Forwarding headers are trusted
  only from configured proxies, so Secure cookies, absolute URLs and per-IP
  limits use the real scheme/address. HSTS is the proxy's, documented. nginx
  sets CSP, X-Frame-Options, nosniff and Referrer-Policy.
- **D-2 (owner, 2026-10-27).** Booking limits are abuse protection and become
  operator settings with stated defaults; their 429 says what it protects and
  when to retry. Recorded here because Part B is what makes per-IP keying
  real; implemented by the NEW-booking-rate-limit follow-up, not this spec.

Factory-assumed (pending challenge; none needs the owner unless the
challenger says otherwise):

- **D-3. Secure cookies and absolute URLs stay configuration-driven**, not
  derived per request from `X-Forwarded-Proto`. The backend already does not
  read the request scheme (F-A9); deriving it would add a spoofable input for
  no gain, since a deployment has one public origin. D-1's requirement is met
  by: the scheme reaches the backend only from a trusted proxy (AC-B5/B6), and
  a startup warning catches the one misconfiguration that breaks sign-in
  (AC-A12).
- **D-4. Timeout ladder.** Header 10 s (stops slow-header attacks; any real
  client sends headers in well under a second). Whole request 30 s (bodies
  are ≤ 1 MiB; 30 s allows ~35 KB/s). Handler budget 60 s: equal to the
  longest deadline a handler sets today (meeting brief, 60 s, F-A5) and
  applied to the unbounded AI parse and OAuth exchanges; AC-A4 forbids the
  budget being shorter than any handler's own deadline. Write backstop 90 s:
  must exceed the handler budget so the backend's error response, not a cut
  connection, is what the client sees (60 s + 30 s for the response). nginx
  upstream read 100 s: must exceed the write backstop so nginx never cuts
  first. The proxy in front must exceed nginx (documented, AC-D3 — e.g. an
  AWS ALB's 60 s default idle timeout is too short). Idle 120 s: above
  common proxy keep-alive (60–75 s) so the server is not the side that drops
  a reused connection mid-request.
- **D-5. Drain budget 30 s, stop grace 40 s.** 30 s covers every bounded
  request except an AI call near its 60 s budget; cutting that rare request at
  deploy is preferred to doubling deploy time. Background jobs are allowed
  the same 30 s and then cancelled; jobs are idempotent per run (the next tick
  redoes the work) — the challenger should check this claim for the personal
  blocker and auto-decline, which were not read in depth. Grace = drain + 10 s
  so Docker's SIGKILL never lands inside the drain.
- **D-6. Pool caps 20 open / 10 idle / 30 min lifetime / 5 min idle time,
  operator-settable.** Postgres default 100 connections, 3 reserved for
  superusers *(default)* → 97 usable. Two backends during a deploy use at
  most 40, leaving 57 for migrations, backups (`pg_dump` uses 1), `psql`, and
  a manual job. The lifetime recycles connections across Postgres failover or
  restarts; the idle time returns unused connections after a burst.
- **D-7. Body cap 1 MiB** in nginx and the backend: matches nginx's implicit
  default today, so no currently-accepted request is newly refused; no upload
  endpoint exists (F-A6). The challenger should name any request that can
  legitimately exceed it.
- **D-8. Security header values.** `X-Frame-Options: DENY` plus CSP
  `frame-ancestors 'none'` (Q-O2); `Referrer-Policy:
  strict-origin-when-cross-origin` (keeps booking slugs and tokens in paths
  from leaking to third parties while preserving same-origin analytics);
  `nosniff`. CSP baseline: `default-src 'self'`; scripts self only (no
  inline, no eval); styles self + Google Fonts stylesheet host + inline
  styles (required by the chart component, F-B2); fonts self + Google Fonts
  font host; images self + `data:`; connect self + the Sentry ingest host +
  operator-configured API origin; `object-src 'none'`; `base-uri 'self'`;
  `form-action 'self'`. Enforced, not report-only, because AC-B3 proves zero
  violations on every e2e journey before merge.
- **D-9. Real client address in nginx**: the right-most forwarded address
  not in the trusted list (recursive), trusted list empty by default. Empty
  default means "behind a proxy, limits are shared until configured" — the
  safe failure (no spoofing); the runbook makes configuring it a deploy step.
- **D-10. Health checks call the service binaries** (scratch images have no
  shell), following the e2e stand-ins' `-healthcheck` precedent.
- **D-11. Image versions are required, with no default.** A missing version
  fails `docker compose config` rather than silently pulling `latest`.
- **D-12. MCP loopback-only by default** (Q-O1 default (a)).
- **D-13. Every migration gets a down file**, destructive ones labelled; the
  runbook's rollback is restore-from-backup, never migrate-down in
  production.
- **D-14. Backups: daily `pg_dump`, 14 days** (Q-O3 default).

## Proposed split

The ticket is too large for one PR. Four independently mergeable parts, in
this order of value; A and B have no dependency on each other.

| Part | Scope | ACs | Depends on |
|---|---|---|---|
| **A** | Backend (and MCP/file-server) timeouts, handler budget, graceful shutdown, pool caps, body cap, readiness, access log, public-URL warning | 13 (A1–A13) | — (readiness touches the contract: stage 2 for AC-A9 only) |
| **B** | nginx: HTTP-only guard, security headers + CSP, trusted proxy / real client address and scheme, timeouts/body cap/upstream keep-alive, `docs/security.md` | 10 (B1–B10) | — (B8's 70 s test assumes A's write backstop; until A merges, the test uses a stub upstream) |
| **C** | Compose: remove 443/certificates, pinned versions, health checks, MCP exposure and token, grace period and log rotation, settings passed, docs service, nginx bind address | 9 (C1–C9) | C3 and C6's integration half need A |
| **D** | Ops: env reference and its completeness test, proxy requirements, backup/restore runbook and rehearsal, deploy order and rollback, down migrations | 8 (D1–D8) | D3/D5/D8 describe A–C's values; land last or update with them |

Total: 40 acceptance criteria.
