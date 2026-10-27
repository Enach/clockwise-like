# Paceday end-to-end suite

Playwright against the **real** compose stack: nginx → Go backend → Postgres,
plus the production frontend bundle. Nothing is mocked. There is no `page.route`
interception anywhere in this directory, and there never should be — the
`e2e-author` agent's one prohibition in `docs/factory/README.md` §5 is
"must not mock the backend".

This document assumes you have never seen this repository.

---

## 1. TL;DR

```bash
cd e2e
npm ci                        # first time only
npx playwright install --with-deps chromium   # first time only
npm test
```

That single command builds the images, starts six containers, waits for
`/api/health`, seeds the database, prepares the calendar stand-in, runs the
suite, and tears the stack down.

---

## 2. Prerequisites

| Need | Why | Check |
|---|---|---|
| **Docker** with Compose v2 | the suite runs the whole stack | `docker compose version` |
| **Node ≥ 20.11** | Playwright, and `tsx` for the seeder | `node -v` |
| **The frontend checkout at `<repo-root>/frontend/`** | `docker/frontend.dockerfile` builds the SPA from there | `ls ../frontend/package.json` |
| Free ports **8088**, **15433**, **18090**, **18091** and **18101–18110** on 127.0.0.1 | deliberately non-default, so a dev stack can run beside this one. 18090/18091 are the stand-in's and the sink's control planes; 18101–18110 are the bare backend containers `tests/startup.spec.ts` starts | `lsof -i :8088 -i :15433 -i :18090` |
| Free subnet **10.143.43.0/24** | the stack's network is pinned (§3) | `docker network ls` |

### The frontend checkout

`clockwise-like/frontend/` ships with only a README — the frontend lives in
`Enach/smart-calendar-flow` and CI clones it at build time (ADR-0002). For a
local e2e run, put it there yourself:

```bash
# from the repository root
git clone git@github.com:Enach/smart-calendar-flow.git /tmp/smart-calendar-flow
rm -rf frontend && ln -s /tmp/smart-calendar-flow frontend
# or, if you already have it as a sibling (make clone-frontend):
ln -sfn ../smart-calendar-flow frontend
```

Alternatively skip the build entirely and use a published image:

```bash
E2E_FRONTEND_IMAGE=enach/paceday-frontend:latest npm test
```

**Nothing here modifies the frontend repo.** Selectors the frontend does not
support yet are recorded in [`TESTIDS-REQUIRED.md`](./TESTIDS-REQUIRED.md).

---

## 3. What actually runs

`e2e/docker-compose.e2e.yml` is standalone, not an override of the root
`docker-compose.yml` — the root file pulls a published frontend image, uses a
persistent named volume and binds port 80, none of which a test run should do.

```
127.0.0.1:8088 ──► nginx ──/api/──► backend ──► postgres (tmpfs, 127.0.0.1:15433)
                      │                │
                      │                ├── calendar traffic ──► fake-google  10.143.43.10 (control 127.0.0.1:18090)
                      │                └── every real provider host ──► sink 10.143.43.20 (log 127.0.0.1:18091)
                      └───/────► frontend (production bundle, Go fileserver)
```

- **Postgres on tmpfs.** The database cannot survive a run, so a stale volume
  can never make the next run non-deterministic. It is exposed on 15433 for
  seeding and for assertions — note that `backend/internal/testdb` uses 15432
  for Go integration tests, so the two never collide.
- **The backend is not published.** Every request goes through nginx, which
  means the suite also proves `nginx/nginx.conf`'s `/api/` proxy rule works.
- **Migrations run themselves.** `storage.Open()` runs golang-migrate at boot,
  so the schema is always at head; the seeder waits for
  `schema_migrations` to be clean before it touches anything.
- **Google Calendar is a stand-in (PAC-43).** The backend runs with
  `PACEDAY_ALLOW_STANDINS=true` and
  `GOOGLE_CALENDAR_API_BASE_URL=http://fake-google:8080/calendar/v3/`, so every
  calendar call — all ten `calendar.NewClient` call sites and the
  personal-calendar sync — goes to `fake-google` (§5a) with the real client
  library and the user's real stored credential. With both set the backend
  connects calendar traffic only to loopback / RFC 1918 / IPv6 unique-local
  addresses and never follows a redirect; the stand-in's fixed address is
  inside that set. **That is not a mock of the backend**: the stand-in is the
  external provider.
- **Every other provider host goes to a recording sink.** `www.googleapis.com`,
  `oauth2.googleapis.com` (token refresh), `accounts.google.com` and the rest
  resolve, inside the backend container, to the `sink` service, which records
  every connection it is offered and serves nothing. (They used to map to
  `127.0.0.1`, which inside the backend container is the backend itself.)
  `tests/hermeticity.spec.ts` (AC-23) reads the sink after the whole run.
- **The network is pinned** to `10.143.43.0/24` so the stand-in's address is
  known to be private. Moving the stand-in to a routable address makes every
  calendar call fail closed; the fix is to move it back, never to widen the
  backend's permitted set.

**Timing**: roughly 4–8 minutes cold (Go and npm builds dominate), 60–90 seconds
warm with cached layers. The suite itself is about 30 seconds.

---

## 4. How a test signs in

`api/middleware.go requireAuth()` accepts a token from the `auth_token` cookie
or from an `Authorization: Bearer` header, validates it with HS256 against
`JWT_SECRET`, and reads two claims: `sub` (the user UUID) and `email`. **There
is no server-side session store.** A validly signed JWT *is* a session.

So `e2e/auth.ts` mints exactly the token `api/handlers_auth.go issueJWT()` would
mint and sets exactly the cookie it would set. That is not a mock: the token
goes through the real middleware, the real signature check and the real claim
parsing, and `GET /api/auth/me` then reads the user out of Postgres.

The Google consent screen is never driven — it would need a real account, a real
client secret and network egress, and would test Google rather than Paceday. The
one gap that leaves (the OAuth callback handler itself) is unfakeable for the
reasons in `SEAM-REQUIRED.md` item B, and is marked `test.fixme` in
`tests/auth-session.spec.ts`.

---

## 5. What is covered, and what is not

| Journey | Status | Where |
|---|---|---|
| Anonymous public booking: link info, slot maths, booking, conflict, exhaustion, 404/410 | **covered, UI + API** | `tests/public-booking.spec.ts` |
| Create a scheduling link, then book it anonymously through the public route | **covered, API round trip** | `tests/scheduling-links.spec.ts` |
| Sign in and land authenticated; cookie and Bearer branches; expired and forged tokens; logout | **covered** | `tests/auth-session.spec.ts` |
| Disconnect a Google calendar and stay signed in (PAC-35 AC-11) | **covered, UI + API** | `tests/calendar-disconnect.spec.ts` |
| Manager roster scoped to the selected team, exact per-member figures, cross-team leak, 403/400 | **covered, UI + API** | `tests/manager-team.spec.ts` |
| View the calendar — the *disconnected* contract, and the UI surfacing it rather than faking it (AC-24) | **covered** | `tests/calendar.spec.ts` |
| View the calendar — with events (AC-16) | **covered, UI + API + stand-in log** | `tests/calendar.spec.ts` |
| Free/busy for an attendee is known and exact (AC-17) | **covered, API + stand-in log** | `tests/calendar.spec.ts` |
| A credential the provider rejects is refused (AC-21) | **covered, UI + API** | `tests/calendar.spec.ts` |
| Run focus time and see blocks appear (AC-18) | **covered, DB + provider + UI** | `tests/focus-time.spec.ts` |
| Clear a focus week from the DB *and* the calendar (AC-19) | **covered, DB + provider** | `tests/focus-time.spec.ts` |
| Manager 1:1 detection — scan and proposal (AC-20) | **covered, UI + API** | `tests/manager-team.spec.ts` |
| A redirection from the provider is not followed (AC-11) | **covered, UI + API + sink** | `tests/seam.spec.ts` |
| Every redirected behaviour reaches the stand-in (AC-22) | **covered, one test per member** | `tests/seam.spec.ts` |
| Personal-calendar sync reaches the stand-in (AC-25) | **covered** | `tests/seam.spec.ts` |
| Startup: production env boots; unsafe calendar settings refuse to boot; the redirect is announced (AC-3, 12, 13, 14, 26) | **covered, on the built image** | `tests/startup.spec.ts` |
| Nothing reached a real provider during the run (AC-23) | **covered except the status lookup**; the strict form is **`test.fixme`** — `GET /api/auth/status` calls Google userinfo for a connected user (see the file) | `tests/hermeticity.spec.ts` |
| The real Google OAuth callback | **`test.fixme`** — `SEAM-REQUIRED.md` item B | `tests/auth-session.spec.ts` |
| Auto-decline reaching the stand-in | **not exercised** — spec AG-5: it only runs on its 15-minute cron | — |

Every `test.fixme` carries a comment naming what blocks it, as
`docs/factory/README.md` §3 rule 4 requires.

### 5a. The calendar stand-in and the sink

`fake-google/` is a small Go service (its own module, `go test ./...` there)
that imports `google.golang.org/api/calendar/v3`, so it marshals the exact
structs the backend's client unmarshals. It serves the seven operations the
backend calls under `/calendar/v3/` (events list/insert/get/update/delete —
writes are `PUT` only — `freeBusy`, `users/me/calendarList`) and refuses any
request without an accepted bearer with Google's 401 envelope. "primary"
means the calendar of the credential's owner.

Its control plane, `/__fixture/` on 127.0.0.1:18090, is driven through
`e2e/fake.ts`: seed or read a calendar's events, read the **request log**
(every product request, with the label of the credential it carried —
never the credential), and arm a **redirect** for one credential label.
`global-setup.ts` resets it and installs the seeded credentials
(`seed/ids.ts ACCEPTED_CREDENTIALS`).

**Isolation.** Specs run in parallel against one stand-in, so nothing resets
it per test. The seeded users with a credential (`seed/ids.ts CONNECTED`) are
one per journey; a scenario owns its user's calendar and its own week, and
reads the log by its own label after a mark it took. Focus blocks are global
busy time for every booking host, so each focus scenario uses a week no
booking spec touches (`lib/dates.ts weekDay`).

The `sink` (same image, `sink/main.go`) listens on :80 and :443 at
10.143.43.20, records peer, TLS SNI or request head and whether an
`Authorization` header was presented (never its value), and closes. Its log
is `GET http://127.0.0.1:18091/connections`; it covers the stack's lifetime,
so with `E2E_SKIP_COMPOSE=1` it spans earlier runs too.

**Project order.** `playwright.config.ts` has three projects: `stand-in`
(`tests/stand-in.setup.ts`, a precondition check), `chromium` (everything,
depends on `stand-in`) and `hermeticity` (`tests/hermeticity.spec.ts`), the
teardown of `stand-in` — Playwright runs it after every dependent test has
finished, pass or fail, which is what makes AC-23 a whole-run assertion.

### Deterministic clock

**There is none, and this suite does not pretend otherwise.**
`backend/engine/clock.go` defines a `Clock` interface and a `FixedClock` double,
but `engine.SystemClock` is a package variable that `main.go` never replaces
from configuration, so the backend's "now" cannot be driven from outside the
process. Every time-dependent fixture is therefore *relative*
(`lib/dates.ts`), and the backend container runs `TZ=UTC` while the browser runs
`timezoneId: 'UTC'` so the two agree on what a date string means. The change
that would give us a fixed clock is sketched in `SEAM-REQUIRED.md` §5; nothing
currently fixme'd is blocked on it.

---

## 6. The rules this suite holds itself to

1. **No test may pass when the feature is broken.** Assertions are exact
   (`slots` is compared to the full expected list, the roster's focus figure is
   compared to `615`), and every write is verified against the database row, not
   just the HTTP response. A 201 with no row is precisely the failure a
   response-only assertion would miss.
2. **Never mock the backend.** No `page.route`, no fixture server, no stubbed
   fetch.
3. **No `waitForTimeout`.** Every wait is on state — an element being visible,
   a URL matching, a count settling. `grep -rn waitForTimeout e2e/` must stay
   empty.
4. **Roles and test ids only, never CSS classes.** Where the frontend offers
   neither, the selector is documented inline and the missing id is filed in
   `TESTIDS-REQUIRED.md`.
5. **Retries are 0, including in CI.** A retry turns an intermittent product bug
   into a green run, which is the exact failure the gate exists to prevent. Set
   `E2E_RETRIES=1` while debugging to get a trace on the second attempt.
6. **Tests are independent and order-free.** There is deliberately *no*
   "reset the database between specs" fixture — that is a global mutex in
   disguise. Isolation comes from the fixtures: every mutating journey owns a
   distinct scheduling link with a distinct **host user** and a distinct **time
   window**, because `engine/booking.go hostBusy()` treats a host's confirmed
   bookings as busy time. Cleanup is by the test's own unique booker-email
   prefix (`db.deleteBookingsByBooker`), never by "clear this link".
7. **The demo-data guard.** `src/api/client.ts` silently substitutes built-in
   demo fixtures when the backend is unreachable. `fixtures.ts assertNotDemo`
   asserts both the "showing demo data" banner and the demo roster names are
   absent. Call it in every authenticated UI test.

---

## 7. Debugging a failure

```bash
# keep the containers after the run
E2E_KEEP_STACK=1 npm test

# run one spec, headed, with the inspector
npx playwright test tests/public-booking.spec.ts --headed --debug

# open the last HTML report (traces, screenshots, videos)
npm run report
```

Once the stack is up you can iterate without rebuilding:

```bash
npm run stack:up                      # build + start, wait for health
E2E_SKIP_COMPOSE=1 npm test           # reuse it; still re-seeds every run
npm run seed                          # re-seed on its own
npm run stack:logs                    # follow all containers
npm run stack:down                    # stop and remove
```

### Reading the failure

| Symptom | Almost always |
|---|---|
| `/api/health did not become healthy` | the backend exited at boot. `npm run stack:logs`. `main.go` `log.Fatal`s on a missing `DATABASE_URL` or `JWT_SECRET`; everything else (Sentry, OAuth, SMTP) degrades. |
| `did not serve the SPA` | `<repo-root>/frontend` is missing or empty — see §2. |
| `schema not ready` / `migrations are dirty` | a migration failed halfway. The version in the error names it; look at `backend/storage/migrations/`. |
| `fixture verification failed` | `seed/seed.ts verify()` found the database is not what the tests assume. The message names the row count that was wrong. Usually a schema change that dropped a seeded column. |
| A UI test sees "Sarah Chen" or "showing demo data" | the frontend could not reach `/api` and fell back to demo fixtures. The nginx proxy or the backend is down; the test is telling you the truth. |
| 401 everywhere | `E2E_JWT_SECRET` and the compose file's `JWT_SECRET` have drifted apart. They must match. |
| `[stand-in]` setup fails, or calendar scenarios get 500 "peer address refused" | the stand-in is down or not on its pinned address. `docker compose -p paceday-e2e -f e2e/docker-compose.e2e.yml logs fake-google backend`. "refusing to start" in the backend log means the two calendar variables are inconsistent — the message names which. |
| A calendar scenario sees "no matching arrival" | the backend answered without reaching the stand-in. The failure message lists what did arrive under that credential label; `GET http://127.0.0.1:18090/__fixture/requests` shows everything. |
| `could not find <Month> <day> in the booking calendar` | react-day-picker's day-button label format changed. `lib/ui.ts pickBookingDate` explains the two selectors it tries; the real fix is `TESTIDS-REQUIRED.md` §1. |

Inspect the database directly while the stack is up:

```bash
psql postgres://paceday_e2e:paceday_e2e@127.0.0.1:15433/paceday_e2e
```

---

## 8. Adding a scenario

1. **Find out whether it is reachable.** Follow the handler in
   `backend/api/routes.go` down to the engine. If any step calls
   `calendar.NewClient`, the journey needs a Google Calendar: use the stand-in
   (§5a) with a user from `seed/ids.ts CONNECTED` — add a new one if your
   journey writes to a calendar or a week another journey reads. Anything else
   that reaches a real provider (sign-in, token refresh, Microsoft Graph,
   Slack, Notion) is still unreachable; write it as `test.fixme` naming the
   blocker and read `SEAM-REQUIRED.md` first.
2. **Add fixtures to `seed/seed.sql`**, with a fixed UUID, and export the ids
   from `seed/ids.ts`. Delete before you insert, so the file stays idempotent.
   If your scenario mutates a scheduling link, give it **its own link and its
   own host user** — see rule 6 above. Add a row to `seed/seed.ts verify()` so a
   missing fixture fails loudly at setup rather than confusingly at assert time.
3. **Write the spec in `tests/`.** Use the fixtures from `fixtures.ts`:
   `api` (anonymous), `authedApi` (Bearer), `authedPage` / `secondUserPage`
   (cookie + local state), `db`, `assertNotDemo`.
4. **Assert exactly.** Prefer one assertion that cannot pass when the feature is
   broken over five that can. After any write, read the row back through `db`.
5. **Clean up by your own unique key**, in a `finally` or at the end of the
   test. Never clear a whole table or a whole link.
6. **Check the guardrails**: `npm run typecheck`, then
   `grep -rn "waitForTimeout\|page.route\|locator('\." tests/ lib/` should be
   empty.

---

## 9. Environment variables

| Variable | Default | Meaning |
|---|---|---|
| `E2E_BASE_URL` | `http://localhost:8088` | where nginx is |
| `E2E_DATABASE_URL` | `postgres://paceday_e2e:paceday_e2e@127.0.0.1:15433/paceday_e2e` | seeding + assertions |
| `E2E_JWT_SECRET` | `e2e-only-jwt-secret-not-for-any-real-deployment` | **must** match the compose file's `JWT_SECRET` |
| `E2E_SKIP_COMPOSE` | unset | `1` = do not start or stop the stack; still waits for health and re-seeds |
| `E2E_KEEP_STACK` | unset | `1` = leave the stack up after the run |
| `E2E_FRONTEND_IMAGE` | build locally | use a published frontend image instead |
| `E2E_RETRIES` | `0` | see rule 5 |
| `E2E_FAKE_GOOGLE_URL` | `http://127.0.0.1:18090` | the stand-in's control plane |
| `E2E_SINK_URL` | `http://127.0.0.1:18091` | the sink's log |
| `E2E_BACKEND_IMAGE` | `paceday-e2e-backend` | the image `tests/startup.spec.ts` runs; point it at another build to check the startup scenarios against it |

Set on the **backend** by `docker-compose.e2e.yml`, never by a production
deployment (the root and prod compose files deliberately do not mention them):

| Variable | e2e value | Meaning |
|---|---|---|
| `PACEDAY_ALLOW_STANDINS` | `true` | the test-mode declaration; alone it changes nothing |
| `GOOGLE_CALENDAR_API_BASE_URL` | `http://fake-google:8080/calendar/v3/` | where calendar traffic goes. Without the declaration, or not ending in `/calendar/v3/`, the server refuses to start |

---

## 10. Wiring into `make verify`

`docs/factory/README.md` §3 makes `make e2e` part of the one gate, and the
root `Makefile` has the target: it runs `npm test` here (or `npm run test:e2e`
if this package ever defines one). That includes `tests/startup.spec.ts`, so
the process-level startup criteria are gated by `make e2e` too.
