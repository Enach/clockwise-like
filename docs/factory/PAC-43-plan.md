# Implementation plan — PAC-43: calendar-dependent behaviour must be verifiable before release

- **Spec**: `docs/specs/PAC-43.md`
- **Contract**: **none, deliberately.** See §0.
- **Resolves**: no audit finding and no `x-uncertain` marker directly. Makes
  U-14, U-15, U-16, U-19, U-23 resolvable by observation, and makes the
  verification half of API-014/015/016/017/028/029/041/044 possible. See spec §1.3.

> **Stage prerequisites, stated rather than assumed.** `plan-author.md` says to
> stop and say so if the spec or contract is not accepted. Both are irregular here
> and I am proceeding under an explicit instruction to produce stages 1–3 together:
>
> 1. **The spec is `draft`, not `challenged`.** `spec-challenger` has not run. This
>    plan must not be executed until it has, because the spec carries an assumption
>    (OQ-1) that would change §4 of this plan if it turns out to be false.
> 2. **There is no contract artifact and there should not be one.** Stage 2 owns
>    the HTTP boundary. This change alters no request, no response, no status code
>    and no operation. `contract-author`'s correct output is a one-line note in
>    `contracts/features/PAC-43.md` recording that the boundary is unchanged and
>    why, so that the gate has something to point at. Nothing in
>    `contracts/openapi/paths/*.yaml` moves.
>
> **Nothing in this plan was compiled, run or tested.** The environment blocks
> `proxy.golang.org` and the npm registry. Every file path was opened before being
> named; every behavioural claim carries a `file:line`. §7 lists what I could not
> settle by reading.

---

## 0. Why there is no contract stage

The factory's stage 2 exists so the two repositories cannot drift. Drift is
possible only where a shape crosses the wire. This change adds two deployment
configuration values and one client option; the bytes on `/api/calendar/events`
are identical before and after, which is the whole point (spec AC-1, §6).

Two consequences for the gate:

- `make openapi-check` should pass unchanged. If it does not, something in the
  change touched a handler, and that is a scope failure, not a contract question.
- `contracts/openapi/MIGRATION.md` does not move. No operation goes from
  `handwritten` to `generated`, because no operation is touched.

Spec OQ-3 asks whether a redirected deployment should advertise itself over the
API. If a contract author answers yes, that **is** a boundary change, a contract
stage appears, and §2 of this plan grows a handler. The spec recommends no, and
this plan assumes no.

---

## 1. Approach

**The change is one client option, reached through one package variable, set once
from `main.go` after validation.** `backend/calendar/client.go:16` currently
passes a single option to the vendored constructor. It will pass a second one when
a package-level base URL is non-empty. That single function is reached by all ten
external call sites (spec §2.2), so nothing else in `backend/` changes.

**The reading and validation of configuration does not live in the calendar
package.** `e2e/SEAM-REQUIRED.md` §2 item A proposes `os.Getenv` inside
`NewClient`, and the same document anticipates the objection. I am taking the
objection: the env var must not be read in the constructor, for a reason that is
about safety rather than taste. Spec AC-3, AC-4 and AC-5 require the server to
**refuse to start** on an unsafe value. A value read lazily inside a constructor is
first evaluated on the first calendar request, by which time the server has been
serving for some time and the failure surfaces as a 500 on one user's screen
instead of as a boot failure. Validation has to happen before `main` mounts
routes, so configuration has to be read there.

So: a new, tiny `backend/config` package reads both variables and returns either
"no override", or a validated endpoint plus the startup notice, or an error.
`main.go` calls it once, `log.Fatal`s on the error, prints the notice, and hands
the endpoint to `backend/calendar`. The validation function is pure and takes its
inputs as arguments, so every branch of spec AC-2 through AC-6 is a table-driven
unit test with no process to start. `main.go` keeps only the two lines that
`CLAUDE.md` exempts from coverage as wiring.

**The stand-in is a Go service under `e2e/fake-google/`, and it is the largest
part of this work by volume.** The argument is in §4.1. The backend change is
about fifteen lines; treating it as the hard part would be a misreading of where
the risk is.

---

## 2. Backend — `Enach/clockwise-like`

### Tests to write first

| Test | File | Proves | Spec AC |
|---|---|---|---|
| `TestCalendarEndpoint_BothUnsetReturnsNoOverride` | `backend/config/endpoints_test.go` *(new)* | Neither variable set → empty endpoint, no notice, no error. The production case. | AC-1 |
| `TestCalendarEndpoint_FlagWithoutURLIsInert` | `backend/config/endpoints_test.go` *(new)* | Test-mode declared, no URL → empty endpoint, no error. Declaring test mode redirects nothing. | AC-2 |
| `TestCalendarEndpoint_URLWithoutFlagIsRefused` | `backend/config/endpoints_test.go` *(new)* | URL set, test mode not declared → error whose text names both variables. | AC-3 |
| `TestCalendarEndpoint_RejectsHostOutsidePermittedSet` | `backend/config/endpoints_test.go` *(new)* | Table: `www.googleapis.com`, `attacker.example.com`, `calendar.googleapis.com.evil.net`, a bare public IP, and a dotted internal name — each refused, each error naming the value. | AC-4 |
| `TestCalendarEndpoint_AcceptsLoopbackAndServiceName` | `backend/config/endpoints_test.go` *(new)* | Table: `http://127.0.0.1:9099/calendar/v3/`, `http://localhost:9099/calendar/v3/`, `http://[::1]:9099/calendar/v3/`, `http://fake-google:8080/calendar/v3/` — each accepted, each returned verbatim. | AC-4 |
| `TestCalendarEndpoint_RejectsMalformedValue` | `backend/config/endpoints_test.go` *(new)* | Table: unparseable, empty host, non-HTTP scheme, and a value missing the trailing path segment the client requires — each refused with the value named, none partially applied. | AC-5 |
| `TestCalendarEndpoint_NoticeNamesTargetAndDisclaimsProduction` | `backend/config/endpoints_test.go` *(new)* | On a valid override the returned notice contains the endpoint and states the deployment is not production. | AC-6 |
| `TestNewClient_DefaultBasePathIsTheGoogleAPI` | `backend/calendar/client_endpoint_test.go` *(new)* | With no override set, the constructed service's `BasePath` equals the library's Google literal. Fails the moment anyone gives the override a default. This is AC-1's real guard. | AC-1 |
| `TestNewClient_HonoursConfiguredBasePath` | `backend/calendar/client_endpoint_test.go` *(new)* | After the package override is set, `BasePath` is exactly the configured value, trailing slash preserved. | AC-1, AC-7 |
| `TestCalendarClient_ListEventsReachesConfiguredEndpoint` | `backend/calendar/client_endpoint_test.go` *(new)* | Against an `httptest.Server`: the request arrives at `/calendars/primary/events`, carries an `Authorization` header, and the decoded events come back. | AC-7, AC-12 |
| `TestCalendarClient_GetFreeBusyReachesConfiguredEndpoint` | `backend/calendar/client_endpoint_test.go` *(new)* | Against an `httptest.Server`: `POST /freeBusy` with the requested identifiers in the body. | AC-8 |
| `TestCalendarClient_CreateAndDeleteEventReachConfiguredEndpoint` | `backend/calendar/client_endpoint_test.go` *(new)* | `POST` then `DELETE` on `/calendars/primary/events[/{id}]` — the two verbs focus time depends on. | AC-9, AC-10 |

These are the internal-test form (`package calendar`), because the assertion in the
first two is on `CalendarClient.service.BasePath`, which is unexported
(`backend/calendar/client.go:11-14`). That is deliberate: asserting on the
constructed client is a far stronger guard than asserting on the env var, and it
is the only test in the repository that would catch someone defaulting the
override to a real address.

**Existing tests that change: none.**
`backend/calendar/calendar_test.go:23-36` (`TestNewClient`) constructs a client
with a fake credential and asserts it is non-nil with calendar id `"primary"`. It
makes no request and asserts nothing about the endpoint, so it keeps passing
untouched. That is also the finding in spec §2.9 — nothing currently protects the
endpoint, which is why the table above is as long as it is.

### Files to change

| File | Change | Risk |
|---|---|---|
| `backend/config/endpoints.go` *(new package, new file)* | One exported function that takes the two raw values and returns a small result (endpoint, notice) or an error. Pure: no `os.Getenv` inside it, so every branch is testable. Host validation per spec AC-4 / OQ-2: accept only a loopback literal or a single-label host. | Low. No caller but `main.go`. |
| `backend/calendar/client.go` | Add an unexported package-level base URL and an exported setter; in `NewClient`, append `option.WithEndpoint` when it is non-empty. No signature change, so none of the ten call sites in spec §2.2 is touched. | Low, but it is the file the whole change rests on. The setter must be called before any request; §5 sequences it before the crons start. |
| `backend/main.go` | Immediately after the existing `PORT` read (`:28-31`) and **before** `storage.Open` (`:38`): read the two variables, call the validator, `log.Fatal` on error, `log.Printf` the notice, call the calendar setter. Placing it before the database open means an unsafe deployment refuses to start without having touched anything. | Low. Two `os.Getenv` calls and three statements. Ordering matters: it must precede `focusCron.Start()` (`:50-52`) and the personal-blocker cron (`:59-70`), either of which could fire a calendar call. |
| `.env.example` | Add both keys under a new section, both empty, with a comment saying plainly that a non-empty calendar endpoint makes the server refuse to start unless test mode is declared, and that no production deployment sets either. | None. |
| `e2e/docker-compose.e2e.yml` | Set both on the `backend` service. See §4.3. | None to production. |

**Deliberately not changed: `docker-compose.yml`.**
`e2e/SEAM-REQUIRED.md` §2 item A suggests adding the key to the production compose
file "so the key is visible in one place". I am rejecting that. Spec §6 requires
that redirection never be reachable in production by any route, and the failure
mode named there is a copied environment file. A commented-out key in the
production compose file is one uncomment away from being set, by someone who will
reasonably assume that a key shipped in the production compose file is a
production key. `.env.example` is where it is documented, with the refusal
behaviour spelled out, and `e2e/docker-compose.e2e.yml` is where it is set. If a
reviewer disagrees, that is a discussion worth having before merge and not after.

### Migration

**None.** No schema change, no data change, no backfill. There is nothing to
reverse and nothing to lose.

If any step of the implementation appears to need a migration, stop: spec §6 names
that as the signal that scope has grown beyond what this spec covers.

### Generated code

**Nothing moves.** No operation changes state in `contracts/openapi/MIGRATION.md`,
because no operation is touched (§0). `make openapi-check` must pass with no diff;
if it produces one, the change has strayed into a handler.

---

## 3. Frontend — `Enach/smart-calendar-flow`

**No change, and no frontend PR.**

Nothing on the wire changes (§0), so no generated artifact regenerates and the
cross-repo ordering rule has nothing to order. The frontend participates only as
the thing under test: spec AC-9 and AC-11 assert that blocks and proposed team
members appear on screen, which exercises the production bundle the e2e stack
already builds (`docker/frontend.dockerfile`, wired at
`e2e/docker-compose.e2e.yml:102-117`).

One real risk sits here and belongs in this section rather than being discovered at
assert time. The frontend silently substitutes built-in demo fixtures when the
backend is unreachable (`e2e/README.md:168-171`), so a UI assertion in AC-9 or
AC-11 could pass against demo data while the feature is broken. Every new UI
scenario must call the existing `assertNotDemo` fixture (`e2e/fixtures.ts`), as
the suite's own rule 7 already requires.

The related frontend work — test ids — is **PAC-44** and is independent of this.
Where a new scenario in §4.4 needs a selector the frontend does not offer, it must
follow the suite's rule 4: document the selector inline and file the missing id in
`e2e/TESTIDS-REQUIRED.md`, not invent a CSS-class selector.

---

## 4. E2E

### 4.1 The stand-in: `e2e/fake-google/`, written in Go

**New files**

| File | Contents |
|---|---|
| `e2e/fake-google/go.mod` *(new)* | Own module, as `docker/fileserver/go.mod` is. |
| `e2e/fake-google/main.go` *(new)* | Routing, bearer-credential check, listen. |
| `e2e/fake-google/store.go` *(new)* | In-memory state keyed by calendar id, guarded by a mutex. |
| `e2e/fake-google/events.go` *(new)* | The five event operations. |
| `e2e/fake-google/freebusy.go` *(new)* | The free/busy query. |
| `e2e/fake-google/calendarlist.go` *(new)* | The calendar list, for rooms. |
| `e2e/fake-google/fixture.go` *(new)* | The control plane. |
| `e2e/fake-google/store_test.go` *(new)* | The stand-in is code; it gets tests. See below. |
| `e2e/fake-google/Dockerfile` *(new)* | Two-stage to `scratch`, mirroring `backend/Dockerfile`. |

**Go rather than Node.** Three reasons, and one cost I am accepting knowingly.

1. *The pattern already exists.* The repository builds two small Go binaries into
   `scratch` images — `backend/Dockerfile` and `docker/fileserver` (its own module,
   `docker/fileserver/go.mod`). The base image, the build recipe and the reviewer's
   familiarity are all already here.
2. *It can marshal the exact types the client unmarshals.* Importing
   `google.golang.org/api/calendar/v3` means the stand-in emits the same `Event`
   and `FreeBusyResponse` structs that `backend/calendar/events.go` and
   `freebusy.go` decode into. Hand-written JSON would reintroduce, inside the test
   harness, precisely the wrong-shape defect class the suite exists to catch
   (API-014 through API-029, spec §1.3). A harness with that bug produces a green
   run over a broken product, which is worse than no harness.
3. *`e2e/`'s only Node dependency is Playwright* (`e2e/package.json:21-27`). A Node
   service means a second lockfile to pin, audit and keep in step.

The cost: unlike `docker/fileserver`, this module has a real dependency tree, so it
gets a `go.sum` and the e2e image build needs registry access. That is a genuine
downside and it is the reason to name it here rather than discover it in review. It
is outweighed by reason 2.

**The surface, enumerated from the source rather than from `SEAM-REQUIRED.md`.**
I read the three files and confirmed the table; one row in that document needed
correcting.

| Method | Path | Called by |
|---|---|---|
| `GET` | `/calendars/{calendarId}/events` | `ListEvents` (`backend/calendar/events.go:11-23`), also the read behind `SuggestAttendees` (`rooms_attendees.go:48`) |
| `POST` | `/calendars/{calendarId}/events` | `CreateEvent` (`events.go:25-27`) |
| `GET` | `/calendars/{calendarId}/events/{eventId}` | `GetEvent` (`events.go:47-49`) |
| `PUT` | `/calendars/{calendarId}/events/{eventId}` | `UpdateEvent` (`events.go:29-31`), `DeclineEvent` (`:36-41`), `AddGoogleMeet` (`:51-60`), `ClearGoogleMeet` (`:62-67`) |
| `DELETE` | `/calendars/{calendarId}/events/{eventId}` | `DeleteEvent` (`events.go:43-45`) |
| `POST` | `/freeBusy` | `GetFreeBusy` (`backend/calendar/freebusy.go:15-41`) |
| `GET` | `/users/me/calendarList` | `ListRooms` (`rooms_attendees.go:24-42`) |

**Correction to `SEAM-REQUIRED.md` §3**: that table lists the decline and
conferencing operations as `PATCH/PUT`. They are **`PUT` only** — all four go
through `Events.Update` (`events.go:37`, `:58`, `:65`), never `Events.Patch`. A
stand-in that implemented `PATCH` would be dead code and a stand-in that
implemented only `PATCH` would fail every write. The query parameters those calls
add are `sendUpdates=all` (`events.go:39`) and `conferenceDataVersion=1`
(`events.go:59`, `:66`); the list call adds `timeMin`, `timeMax`,
`singleEvents=true` and `orderBy=startTime` (`events.go:14-17`). The stand-in must
**honour** `timeMin`/`timeMax` — AC-7 asserts a specific window — and must
**ignore unknown query parameters** rather than rejecting them, because the client
library adds its own (`alt`, `prettyPrint`) that are not in this list.

**Behaviour required of it**

- State in memory, keyed by calendar id, reset per scenario. No persistence: the
  stand-in restarting mid-run should be a visible failure, not a silent one.
- **Reject any request with no `Authorization: Bearer` header**, with the Google
  error envelope and a 401. Spec AC-12 depends on this: a permissive stand-in would
  let the suite go green over a broken credential path.
- A control plane under a path the real API does not have, so it can never be
  confused for a product route: `POST /__fixture/reset`,
  `POST /__fixture/calendars/{calendarId}/events` to seed, and
  `GET /__fixture/requests` returning the method-and-path log. The last one is what
  makes spec AC-13 assertable — it is the only way to prove *which* paths were
  reached rather than merely that nothing errored.
- A `GET /__fixture/health` for the compose healthcheck.

**Tests for the stand-in itself**: `e2e/fake-google/store_test.go` covers the
window filter (an event overlapping the boundary is included, one outside is not)
and the free/busy projection. Two tests, because a stand-in that filters wrongly
produces a test failure that will be read as a product bug and cost an afternoon.

### 4.2 Seed changes

The four seeded users have **no credential rows**, deliberately
(`e2e/seed/seed.sql:54`), and four passing scenarios depend on that state
(`e2e/tests/calendar.spec.ts:25,42,52`, `e2e/tests/focus-time.spec.ts:56`). Spec
AC-14 requires those to stay green.

**So the connected user is a fifth user, not a change to the existing four.**

| File | Change |
|---|---|
| `e2e/seed/ids.ts` | Add `USERS.connected` with a fixed UUID in the established `5555…` pattern and a `@paceday.test` address. |
| `e2e/seed/seed.sql` | Insert that user; insert one `oauth_tokens` row for them; amend the comment at `:54`, which currently asserts no token rows exist for *any* user, so it stays true. |
| `e2e/seed/seed.ts` | Add the new user and the token row to `verify()`, so a missing fixture fails at setup rather than confusingly at assert time. |

**The token's expiry must be far in the future.** `auth.TokenSource`
(`backend/auth/google_oauth.go:32-34`) returns `config.TokenSource(...)`, which
refreshes an expired token against `google.Endpoint` — `oauth2.googleapis.com`,
which the compose file maps to `127.0.0.1`
(`e2e/docker-compose.e2e.yml:90`). A seeded token with a past expiry would
therefore fail at the *token* endpoint, not the calendar one, and the failure would
look exactly like a broken seam. The columns are plain text
(`backend/auth/token_store.go:44-49`), so the row is a straightforward insert.

### 4.3 Compose wiring

`e2e/docker-compose.e2e.yml`:

- Add a `fake-google` service built from `e2e/fake-google/Dockerfile`, with a
  healthcheck on its fixture-health route and `restart: "no"` like its siblings.
- On the `backend` service, add the test-mode declaration and the calendar
  endpoint pointing at `http://fake-google:8080/calendar/v3/`. The single-label
  host is exactly what the validator in §2 permits, and the trailing
  `/calendar/v3/` is what the client's base-path concatenation requires (§7, item
  2).
- Add `fake-google` to the backend's `depends_on` with `condition: service_healthy`.
- **Do not publish the stand-in's port and do not add an nginx route to it.** The
  tests reach its control plane from the host; that needs a published port on
  127.0.0.1 in the same style as postgres's `15433`, and nothing more. Routing it
  through nginx would put a test fixture behind the production routing rules.
- **Keep `extra_hosts` exactly as it is** (`:87-96`). Spec §6 is explicit: the
  redirect is what makes the stand-in reachable, the blackhole is what proves
  nothing escaped, and deleting it would remove the evidence for AC-13.

`e2e/README.md` §3, §5 and §9 all describe the current situation as having no seam;
all three need updating in the same change, and §5's table is the one a reader
checks first.

### 4.4 Scenarios

| Scenario | File | Spec AC |
|---|---|---|
| *a connected calendar renders its events on the week grid* — currently `test.fixme` | `e2e/tests/calendar.spec.ts:73` | AC-7 |
| *free/busy for an attendee reflects that attendee's real calendar* — currently `test.fixme` | `e2e/tests/calendar.spec.ts:83` | AC-8 |
| *running focus time creates blocks and they appear on the calendar* — currently `test.fixme` | `e2e/tests/focus-time.spec.ts:75` | AC-9 |
| *clearing a week removes the blocks from both the database and the calendar* — currently `test.fixme` | `e2e/tests/focus-time.spec.ts:86` | AC-10 |
| *rescanning the calendar detects 1:1s and proposes team members* — currently `test.fixme` | `e2e/tests/manager-team.spec.ts:137` | AC-11 |
| *a calendar request the provider rejects is surfaced, not swallowed* — **new** | `e2e/tests/calendar.spec.ts` | AC-12 |
| *every calendar-dependent endpoint reaches the stand-in* — **new** | `e2e/tests/seam.spec.ts` *(new)* | AC-13 |

Plus a new helper `e2e/fake.ts` *(new)*: a typed client for the control plane —
`reset()`, `seedEvents()`, `requests()` — so no scenario writes a raw fetch against
the fixture routes.

**The two scenarios that are not this issue's** stay disabled and keep their
comments: `e2e/tests/auth-session.spec.ts:115` (needs the sign-in redirect, spec
§5) and `e2e/tests/scheduling-links.spec.ts:144` (needs PAC-44's test ids).

**Expect the newly enabled scenarios to fail on first run, and treat that as the
result rather than an obstacle.** API-014, API-015 and API-017
(`docs/factory/api-audit.md:248-251`) are confirmed shape defects on exactly the
focus-blocks, focus-run and event-patch responses that AC-9 and AC-10 assert
against. The scenarios must assert what the product *should* return. If a defect
cannot be fixed in the same change, the scenario is disabled again with a comment
naming the audit finding and the issue that will re-enable it, per
`docs/factory/README.md` §3 rule 4 — never loosened to match the broken shape.

**The fifteen-minute caches** (spec §2.8, OQ-5). AC-8 goes through
`engine.FreeBusyService`, which caches per user-plus-address-plus-date for fifteen
minutes (`backend/engine/freebusy_service.go:140,157-166`) and whose clock cannot
be advanced. The scenario must use an attendee address and a date no other
scenario uses. If a single scenario needs two different answers for one key — for
example asserting an update is reflected — it cannot be written today and that is
a finding for a clock issue, not a reason to relax the assertion.

---

## 5. Sequencing

1. **Settle OQ-1 first, before anything else is written.** Whether the vendored
   client will talk to a non-TLS endpoint with a credential attached decides
   whether §4.1 needs a certificate. A few minutes in a local sandbox with registry
   access; see §7 item 1. Everything downstream assumes the answer is yes.
2. `spec-challenger` runs against `docs/specs/PAC-43.md`. **Merge point: the spec
   is accepted before any code is written.**
3. `contract-author` records the one-line no-boundary-change note (§0).
4. **Backend, tests first**: write all twelve tests in §2, watch them fail, then
   write `backend/config/endpoints.go`, then the change to
   `backend/calendar/client.go`, then the wiring in `backend/main.go`, then
   `.env.example`.
5. `make verify` on the backend. **Merge point: the backend PR merges on its own.**
   It is independently safe — it changes nothing a user can see, adds no
   dependency, and leaves the suite exactly as it is. It should not wait for the
   stand-in.
6. **The stand-in**: `e2e/fake-google/` and its two tests, built and run in
   isolation against `curl` before anything is wired to it.
7. **Compose and seed**: the new service, the two backend variables, the fifth
   seeded user and the token row. Verify by starting the stack and confirming the
   backend's startup log carries the redirection notice (spec AC-6) — that is the
   cheapest proof the whole chain is connected.
8. **Scenarios**, one at a time, in this order: AC-13 first (it is the broadest and
   the cheapest to debug), then AC-7, AC-8, AC-12, AC-9, AC-10, AC-11. Enabling
   them in ascending order of how much product surface they touch means the first
   failure is the seam's fault and every later one is the product's.
9. Update `e2e/README.md` §3, §5, §9 and `e2e/SEAM-REQUIRED.md` (item A is now
   done; items B and C remain and should say so).
10. **Merge point: the e2e PR merges second**, after `make e2e` is green or with
    each remaining disabled scenario naming the issue that will enable it.

No frontend PR, so no cross-repo ordering applies.

---

## 6. Rollback

**No migration, so nothing is lost in either direction.** Stating that plainly, as
the format requires: the down path is a code revert and nothing else.

```
# 1. Revert the backend change.
git revert <backend commit>

# 2. Revert the e2e change, which restores the test.fixme markers,
#    removes the fake-google service and removes the fifth seeded user.
git revert <e2e commit>

# 3. Tear the stack down so no orphaned fake-google container survives.
docker compose -p paceday-e2e -f e2e/docker-compose.e2e.yml down -v --remove-orphans
```

A production deployment needs no action, because it never set either variable —
which is the same property that makes spec AC-1 the argument for shipping this at
all.

Two things do not revert by themselves and are covered by step 2 rather than being
left to whoever does the revert:

- The scenarios enabled in §4.4 must go back to `test.fixme` with a comment naming
  this issue, or the suite goes red for a reason unrelated to the revert.
- `e2e/README.md` §5's coverage table must go back to describing them as blocked.

**Partial rollback is available and is the preferred first move.** If the stand-in
misbehaves but the backend change is sound, remove the two environment variables
from `e2e/docker-compose.e2e.yml` and re-disable the scenarios. The backend reverts
to production behaviour without a code change, because with no override configured
`NewClient` is byte-for-byte what it is today. That property is worth protecting in
review.

---

## 7. What I could not determine

1. **Whether the vendored Google client will use a plain-HTTP endpoint with a
   credential attached.** Spec OQ-1, and the one assumption this whole plan rests
   on. I could not fetch `google.golang.org/api` to read `option.WithEndpoint`'s
   handling of the scheme, or whether the credential transport objects to
   non-TLS. *Cheapest settlement*: on a machine with registry access, a
   ten-line program that calls the constructor with a static credential and an
   `httptest.Server` URL, then calls the events list and prints the error. Minutes.
   If the answer is no, §4.1 grows a self-signed certificate and
   `backend/Dockerfile`'s `scratch` image grows a trust store — a real change to
   the plan, but to §4 only, not to the spec.
2. **Whether `option.WithEndpoint` replaces the base path or is joined to it, and
   whether the trailing slash is required.** I have assumed replace-verbatim and a
   required trailing `/calendar/v3/`, following `SEAM-REQUIRED.md` §2. *Cheapest
   settlement*: `TestNewClient_HonoursConfiguredBasePath` in §2 answers it on the
   first run, which is why it is in the table before any wiring exists.
3. **Whether the seeded credential survives the request path without a refresh.**
   I have reasoned that a future expiry avoids it (§4.2) from reading
   `backend/auth/token_store.go:44-49` and `backend/auth/google_oauth.go:32-34`,
   but I have not confirmed the library's validity margin. *Cheapest settlement*:
   the stand-in's request log (`GET /__fixture/requests`) will show the calendar
   call arriving, or it will not — and if it does not, the backend log will show a
   connection refused to the token endpoint, which is unambiguous.
4. **The exact query parameters the client adds.** I read the four the code sets
   (§4.1) but the library adds its own. I have specified "ignore unknown
   parameters" to make this not matter. *Cheapest settlement*: the stand-in's
   request log during the first AC-7 run.
5. **Whether the permitted-host rule in §2 is the right shape.** Spec OQ-2. I have
   planned for "loopback literal or single-label host" because it is mechanical and
   excludes every public name by construction. Whether a target topology needs a
   dotted internal name is a question for whoever runs the stacks, not something I
   can read out of the repository. *Cheapest settlement*: ask before the backend PR
   opens; changing it afterwards means changing a security control, which is the
   change nobody will want to review twice.
6. **How much of the newly enabled surface actually passes.** I expect AC-9 and
   AC-10 to fail against API-014/API-015, and AC-7's UI half to depend on
   selectors PAC-44 has not added yet. I could not run any of it. *Cheapest
   settlement*: step 8 of §5 — enable them one at a time, in the order given, and
   let the first run be the measurement rather than trying to predict it here.
