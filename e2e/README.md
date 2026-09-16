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

That single command builds the images, starts four containers, waits for
`/api/health`, seeds the database, runs the suite, and tears the stack down.

---

## 2. Prerequisites

| Need | Why | Check |
|---|---|---|
| **Docker** with Compose v2 | the suite runs the whole stack | `docker compose version` |
| **Node ≥ 20.11** | Playwright, and `tsx` for the seeder | `node -v` |
| **The frontend checkout at `<repo-root>/frontend/`** | `docker/frontend.dockerfile` builds the SPA from there | `ls ../frontend/package.json` |
| Free ports **8088** and **15433** on 127.0.0.1 | deliberately non-default, so a dev stack can run beside this one | `lsof -i :8088 -i :15433` |

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
- **Outbound provider hosts are blackholed.** `www.googleapis.com` and friends
  resolve to `127.0.0.1` inside the backend container. A leaked call fails in
  milliseconds instead of hanging, and a test run can never reach the real
  internet. See [`SEAM-REQUIRED.md`](./SEAM-REQUIRED.md) §4.

**Timing**: roughly 4–8 minutes cold (Go and npm builds dominate), 60–90 seconds
warm with cached layers. The suite itself is about 25 seconds.

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
| Manager roster scoped to the selected team, exact per-member figures, cross-team leak, 403/400 | **covered, UI + API** | `tests/manager-team.spec.ts` |
| View the calendar — the *disconnected* contract, and the UI surfacing it rather than faking it | **covered** | `tests/calendar.spec.ts` |
| View the calendar — with events | **`test.fixme`** — needs a fake Google Calendar; see `SEAM-REQUIRED.md` item A |
| Run focus time and see blocks appear | **`test.fixme`** — same seam; focus blocks *are* Google events |
| Manager 1:1 detection ("re-scan calendar") | **`test.fixme`** — same seam |
| The real Google OAuth callback | **`test.fixme`** — `SEAM-REQUIRED.md` item B |
| Creating a link through the Links **page** | **`test.fixme`** — needs test ids, not a seam; `TESTIDS-REQUIRED.md` §2 |

Every `test.fixme` carries a comment naming what blocks it, as
`docs/factory/README.md` §3 rule 4 requires.

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
| `could not find <Month> <day> in the booking calendar` | react-day-picker's day-button label format changed. `lib/ui.ts pickBookingDate` explains the two selectors it tries; the real fix is `TESTIDS-REQUIRED.md` §1. |

Inspect the database directly while the stack is up:

```bash
psql postgres://paceday_e2e:paceday_e2e@127.0.0.1:15433/paceday_e2e
```

---

## 8. Adding a scenario

1. **Find out whether it is reachable.** Follow the handler in
   `backend/api/routes.go` down to the engine. If any step calls
   `calendar.NewClient`, the journey needs a Google Calendar and cannot be
   tested today — write it as `test.fixme` with a comment naming the call site,
   and add it to the table in §5. Do not invent a way around it; read
   `SEAM-REQUIRED.md` first.
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

---

## 10. Wiring into `make verify`

`docs/factory/README.md` §3 makes `make e2e` part of the one gate. There is no
`e2e` target in the root `Makefile` yet; the intended body is:

```make
.PHONY: e2e
e2e:
	cd e2e && npm ci && npx playwright install --with-deps chromium && npm test
```

Adding it is a one-line change to the root `Makefile` and is left to whoever
lands the `make verify` target, so that this directory does not half-create it.
