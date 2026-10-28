# Plan: PAC-81 Part A (backend process hardening)

- **Spec**: `docs/specs/NEW-deploy-hardening.md` revision 3 (accepted), Part A,
  AC-A1..AC-A19, values table in section 3, decisions D-4..D-7, D-15, F-9, F-12.
- **Contract**: `contracts/features/PAC-81-part-a.yaml` revision 2 (accepted at
  e550327), bundle sha256 `ffcc55cc0a8fa14179f506e929749bff936cf10f208b94fdc350713ad035e70e`.
- **Stage**: backend-implementer (plan + implementation), branch
  `impl/pac-81-part-a-backend`.
- **Where this file lives**: `docs/plans/`, beside Part B's plan
  (`docs/plans/PAC-81-part-b.md`, on `main` with #245) and as the coordinator
  asked. The contract's `allowedPaths` list `docs/factory`, not `docs/plans`;
  this is the one path outside that list, recorded in the PR.

PAC-71 (#242) has not merged on `main`, so AC-A10's "guarded write and the
PAC-71 sign-in path" and the callback CONNECT branch are out of this PR (the
contract's `mergeInterplay` gives them to PAC-71's stage-4 PR). Part D (#246)
is not on `main`, so `.env.example` gets a plain addition.

## 1. Design

### 1.1 New internal packages (each with a `_test.go`)

| Package | Responsibility | ACs |
|---|---|---|
| `backend/internal/budget` | Request budget middleware (90 s). Runs the handler on its own goroutine with a deadline context; at expiry writes the shared `503 {"error":"request timed out"}` unless the handler has started writing or has **committed** (`budget.Commit`). Handlers may register a different expiry answer (`budget.OnExpiry`): the sign-in callbacks' failure redirect, step-up's denied redirect. Panics are re-raised on the serving goroutine so Sentry and net/http see them. `budget.ErrExpired` is the sentinel per-item loops return. | A2, A9, A19 |
| `backend/internal/lifecycle` | Tracked units (`Go`, `Track`): kind + identifier + optional detail, cancelled at the end of the drain. Shutdown sequence: notice (readiness 503, crons stop scheduling) → `Server.Shutdown` + wait for units within the drain → cancel request contexts and units, wait the cancel grace, log one line per abandoned unit, `Server.Close`. Reports clean / not clean (exit 0 / 1). Server constructor with the D-4 timeouts. Health-mode probe. | A1, A5, A6, A7, A16 |
| `backend/internal/outbound` | The outbound limits (LLM 30 s, provider 15 s, identity 10 s, token exchange 10 s, ICS 15 s + 10 MiB, SMTP 15 s), shared clients with `Timeout`, an identity-client context for `oauth2`/go-oidc, a size-capped reader, and an SMTP sender with one deadline from dial to QUIT. | A3, A8 |
| `backend/internal/peritem` | Bounded per-item execution: 4 workers; reads get `min(limit, remaining − 10 s)`; creates start only with `limit + 10 s` remaining and are never shortened (detached from the request's cancellation); no call starts at the margin; a not-started item, or a read cut by a budget-shortened deadline or by the budget itself, returns `budget.ErrExpired`. "Remaining" includes the drain deadline once shutdown has begun. | A17, A18, A19 |

### 1.2 Backend changes

- **Server** (`main.go`): `http.Server{ReadHeaderTimeout 10 s, ReadTimeout 30 s,
  WriteTimeout 120 s, IdleTimeout 120 s}`, `BaseContext` cancellable at the end of
  the drain; `signal.NotifyContext(SIGTERM, SIGINT)`; the lifecycle sequence; pool
  closed after it; `sentry.Flush`; exit code from the sequence. `paceday health`
  (first argument `health`) runs the health mode before anything else: no
  Sentry, no database, no secret; `GET http://127.0.0.1:$PORT/api/ready`, 2 s,
  exit 0 only on 200.
- **Middleware order** (outer → inner): Sentry (repanic) → access log → CORS →
  body cap → budget → router. The access log wraps everything that can write a
  status, so it records the status actually sent (budget 503 included); it logs
  `method path status duration client=<ip>` with the path only (never the
  query), and the client from `clientIP` with `TRUSTED_PROXIES` (the abuse
  limits' resolver).
- **Body cap**: 1 MiB. A declared `Content-Length` over the cap, or a body that
  reads past it, is answered `413 {"error":"request body too large"}` before the
  handler runs; a body of exactly 1 MiB is passed on.
- **Readiness** `GET /api/ready`: implemented as `generatedServer.GetReadiness`
  on the generated strict wrapper (MIGRATION.md row flipped). Shutdown flag read
  before starting or joining a ping and again when the answer is chosen; single
  flight (one ping in flight per process, joiners share its result) on a
  context that belongs to no request, `PingContext` with a 2 s timeout, which
  bounds the pool wait. `200 {"status":"ready"}` / `503 {"error":"not ready"}`.
  No auth, no rate limit. `/api/health` untouched.
- **Pool** (`config.DecideDBPool` + `storage.ApplyPool`): open 20, idle 10,
  lifetime 30 min, idle time 5 min; `PACEDAY_DB_MAX_OPEN_CONNS`,
  `PACEDAY_DB_MAX_IDLE_CONNS`, `PACEDAY_DB_CONN_MAX_LIFETIME`,
  `PACEDAY_DB_CONN_MAX_IDLE_TIME` (Go durations); non-positive or unparseable
  values are ignored with a startup warning. Migrations now release the
  connection they hold (today `runMigrations` keeps one pooled connection for
  the life of the process, which deadlocks a cap of 1).
- **Context-bound database calls on the AC-A9 paths**: `storage.DBTX` (the
  methods `*sql.DB` already has) and `storage.Bind(ctx, db)`, whose
  context-free calls run on the bound context, so the pool wait ends at the
  budget. Functions on the named paths take `storage.DBTX` instead of
  `*sql.DB` (callers passing `*sql.DB` compile unchanged); the handlers and
  engines on those paths pass `storage.Bind(ctx, db)`. Paths: booking create,
  slot listing with a date, natural-language parse, personal-calendar sync,
  Google and Microsoft sign-in callbacks.
- **Booking create (AC-A19)**: `budget.Commit` immediately before the insert;
  refused → 503, no row. The insert runs on a context detached from the
  budget; its own result decides 201 or 500. After commit, host events and the
  confirmation email run as one `lifecycle` unit `booking-completion <id>`
  (per-host creates through `peritem` create mode, then the email); its detail
  names the hosts done and not done. The handler waits for the unit or the
  budget, then answers 201 with the saved booking.
- **Slot listing (AC-A18, AC-A2)**: per-host reads through `peritem` read mode
  (renewal + list per item, item limit 25 s = 10 s renewal + 15 s list,
  D-4). Budget cut → 503. The 60-day listing stops on a budget error or a
  cancelled context and answers 503, never a truncated 200.
- **Team availability (AC-A18)**: per-member reads through `peritem`; budget cut → 503.
- **Personal sync (AC-A17)**: ICS fetch 15 s and 10 MiB (over the cap → error,
  no partial parse; the log names the limit); stale-blocker deletes in read
  mode, blocker creates in create mode; a create whose answer arrives is
  recorded on a context detached from the budget. A budget cut → 503, and
  `last_synced_at` is not stamped.
- **Callbacks (AC-A2)**: Google, Microsoft and SSO OIDC callbacks register the
  `/?error=auth_failed` redirect as their expiry answer. Step-up registers
  `denied&reason=state_mismatch` before check 0, then the reason of each check
  as it starts, and commits before step 5 (the grant record), which then runs
  on a detached context.
- **Outbound wiring (AC-A3)**: LLM clients (`nlp`), Slack/Notion token exchange,
  SSO tenant discovery, Outlook, Zoom, Teams, the recap's Slack calls, the
  Google calendar client, ICS, SMTP; identity calls (Google/Microsoft exchange,
  OIDC discovery/token/JWKS, Google/Microsoft/Zoom renewal) through the identity
  client context; Microsoft userinfo client 10 s (Google's 5 s is kept: it is
  under the limit). The step-up client keeps its 15 s.
- **Tracked background work (AC-A6, AC-A7)**: analytics recompute, habit
  re-optimisation (3 sites), manager detection, recap per-user goroutines
  (the 1 s stagger stops at shutdown), focus scheduler ticks, every cron job,
  team-invite and booking emails, the booking completion unit, and every
  request's handler goroutine (so one still blocked in a context-free database
  call after its response counts as abandoned).
- **Emails (AC-A8)**: `outbound.SendMail` (15 s dial to QUIT). A failure is
  logged at error level with the booking id / invite id (never an address) and
  sent to Sentry. Not retried.
- **Sentry (AC-A14)**: no hard-coded DSN; unset/empty `SENTRY_DSN` → no
  `sentry.Init`, log `monitoring disabled`; set → environment and release from
  `SENTRY_ENVIRONMENT`/`SENTRY_RELEASE`.
- **Public URL warning (AC-A15)**: `config.InsecurePublicURLs(getenv, secure)`
  returns one warning per `FRONTEND_URL`, `APP_URL`, `BASE_URL` that is `http://`
  on a host other than localhost/127.0.0.1/[::1] while cookies are Secure.
  Logged at startup, never fatal.

### 1.3 MCP and file server

- `mcp/main.go`: the SSE server gets the same server timeouts; the SSE stream
  clears its own read and write deadlines (an idle stream is never cut);
  unauthenticated `GET /health` liveness; `mcp health` health mode (reads
  `MCP_PORT`, no token needed). Its test file `mcp/main_test.go` is new (the
  contract lists `mcp/main.go`; a test next to it is required by AC-A16 and
  noted in the PR).
- `docker/fileserver/main.go`: server timeouts and `fileserver health` mode
  only (`GET /` on the local port). The API-base injection is untouched (Part B
  owns it). Part B (#245) merged first; this branch merged `main` and
  re-applied its half on top of Part B's handler (`newServer`, `healthMode`,
  the `health` argument), leaving the injection untouched.

## 2. AC → test (level)

| AC | Test | Level |
|---|---|---|
| A1 | `lifecycle.TestServerClosesSlowHeaders`, `TestServerClosesStalledBody` (real TCP, real `http.Server`, bounds scaled) | integration (in-process server) |
| A2 | `budget.TestExpiryAnswers503JSON`, `TestExpiryCustomRedirect`; `api.TestPAC81_SlotListing60DaysCancelledIs503`; `api.TestPAC81_SignInCallbacksRedirectAtBudget`; `api.TestPAC81_StepupCallbackAtBudgetBeforeCheck0` | unit |
| A3 | `outbound.TestClientFailsWithinLimit`, `TestReadLimited`, `TestSendMailDeadline`; per-site tests (LLM, Slack/Notion exchange, ICS, Outlook, Zoom, Teams, recap Slack, identity exchange/discovery/renewal) against a stub that accepts and never answers | unit |
| A4 | `api.TestPAC81_BudgetExceedsHandlerDeadlines` (asserts budget − each deadline/sum ≥ 30 s) | unit |
| A5 | `lifecycle.TestServerClosesIdleKeepAlive` | integration (in-process server) |
| A6 | `lifecycle.TestShutdownSequence` (5 s-style request, unit, readiness flag, refused connection after the notice, all scaled) and `api.TestPAC81_ReadinessDuringNotice` (readiness 503 while a normal request answers) | integration (in-process server) |
| A7 | `lifecycle.TestShutdownAbandonsAndLogs` (cancel, grace, one log line per unit with kind/id/detail, not clean) | integration (in-process server) |
| A8 | `engine.TestPAC81_BookingEmailFailureLogged`, `api.TestPAC81_InviteEmailFailureLogged` (stub SMTP refusing / hanging) | unit |
| A9 | `storage.TestPoolDefaultsAndOverrides`, `config.TestDecideDBPool`; `api.TestPAC81_HandlerGoroutineReturnsAtBudgetWithPoolHeld` (cap 1 held elsewhere; booking create, dated slots, parse, personal sync, Google/Microsoft callbacks return within budget + 1 s) | integration (Postgres) |
| A10 | `api.TestPAC81_PoolCapOnePaths` (cap 1: Google/Microsoft callbacks, booking create, dated slots, parse with stub LLM, personal sync with stub ICS, each < 10 s) | integration (Postgres) |
| A11 | `api.TestPAC81_Readiness*` (ready, DB down, pool held → 503 ≤ 3 s, shutting down, health unchanged, no auth/limit), `TestPAC81_Readiness50ConcurrentSingleFlight` | unit + integration (Postgres) |
| A12 | `api.TestPAC81_BodyCap*` (over → 413 JSON, handler not run; exactly 1 MiB processed; chunked) | unit |
| A13 | `api.TestPAC81_AccessLog*` (one line; no query; replaced status; budget 503; client IP) | unit |
| A14 | `main.TestPAC81_SentryOffWithoutDSN`, `TestPAC81_SentryOnCarriesEnvironmentRelease` (stub transport); `config.TestEnvExampleSentryDSNEmpty` | unit |
| A15 | `config.TestInsecurePublicURLs` | unit |
| A16 | `lifecycle.TestHealthProbe`; `main.TestPAC81_HealthMode`; `mcp.TestHealthMode`, `TestSSEStreamSurvivesWriteTimeout`, `TestSlowHeadersClosed`; `fileserver.TestHealthMode`, `TestSlowHeadersClosed` | unit + integration (in-process server) |
| A17 | `engine.TestPAC81_PersonalSync6EventsTwoWaves`, `TestPAC81_PersonalSync40EventsCutThenRerun`, `api.TestPAC81_PersonalSyncBudgetCutIs503NotStamped`, `calendar.TestWebcalLimits` | unit (+ Postgres) |
| A18 | `peritem.Test*`; `engine.TestPAC81_Slots3Hosts`, `TestPAC81_TeamAvailability5Members2Waves`, `TestPAC81_TeamAvailability20MembersCut`, `TestPAC81_HostProviderErrorTreatedFree`, `TestPAC81_OwnLimitTimeoutTreatedFree`, `TestPAC81_ShortenedDeadlineIsBudgetCut` | unit |
| A19 | `api.TestPAC81_BookingBudgetAfterCommitIs201`, `TestPAC81_BookingInsertHeldPastBudgetIs201`, `TestPAC81_BookingInsertFailsAfterBudgetIs500`, `TestPAC81_BookingBudgetBeforeInsertIs503NoRow`; `lifecycle` abandonment detail test (booking id, hosts done/not done) | unit + integration (Postgres) |

"Integration (in-process server)" means a real TCP listener and a real
`http.Server` in the test process, with the bounds scaled down; no test spawns
the compiled binary. The PR states this.

## 3. Coordinator requests from the Part B review (#245)

- The backend 413 body is byte-identical to nginx's:
  `{"error":"request body too large"}` plus a newline (`writeError`, a
  `json.Encoder`). `api.TestPAC81_BodyCapExactBytes` pins the exact bytes.
- The spec on this branch gets "Revision 3.1, from Part B implementation":
  D-8 `font-src 'self' data: https://fonts.gstatic.com` (FullCalendar's
  `fcicons` font), and AC-B5's `X-Forwarded-For` is `F, C, P` from a trusted
  proxy (nothing reads it; the backend uses only `X-Real-IP`).

## 4. Out of this PR

- PAC-71 paths in AC-A10 and the CONNECT-branch expiry answer (PAC-71 not merged).
- Container health check wiring, grace periods, compose (Part C), nginx (Part B).
- Context-free database calls off the AC-A9 paths stay context-free (spec §3:
  only the response is bounded there).
