# Implementation plan — PAC-43: calendar-dependent behaviour must be verifiable before release

> ## Plan v2 — realigned to spec v2 (2026-09-17)
>
> `spec-challenger` returned the spec accept-with-changes with four blocking
> findings, and `docs/specs/PAC-43.md` now carries a `# Revision v2` section that
> supersedes its §3, §4, §6 and §8. **This plan builds against v2 AC-1 to AC-25.**
> Every "spec AC-n" reference below is a v2 number.
>
> What changed in this plan as a result:
>
> 1. **The security control moved out of `backend/config` and into the transport.**
>    Plan v1 made a startup host-string check the control. Spec v2 §V2-3 makes a
>    connection-time peer-address rule the control and demotes the startup check to
>    a typo-catcher. §1 and §2 are rewritten around that.
> 2. **Redirects are handled** — they were absent from plan v1 entirely. §1 and §2.
> 3. **Startup-refusal tests are no longer unit tests.** They run the built image.
>    New §2.4.
> 4. **The stand-in grows two things**: a request log that AC-22 reads, and a mode
>    in which it answers with a redirection so AC-11 can observe the policy in the
>    shipped binary. §4.1.
> 5. **The blackhole becomes a recording sink** (AC-23). §4.3.
> 6. **§7 item 1's contingency was factually wrong** and is corrected; OQ-1 is now
>    three questions and is settled by reading module source, not by running a
>    program. §5 and §7.

- **Spec**: `docs/specs/PAC-43.md` — **build against `# Revision v2`**
- **Contract**: **none, deliberately.** See §0.
- **Resolves**: no audit finding and no `x-uncertain` marker directly. Makes
  U-14, U-15, U-16, U-19, U-23 resolvable by observation, and makes the
  verification half of API-014/015/016/017/028/029/041/044 possible. See spec §1.3.

> **Stage prerequisites, stated rather than assumed.** `plan-author.md` says to
> stop and say so if the spec or contract is not accepted. Both are irregular here
> and I am proceeding under an explicit instruction to produce stages 1–3 together:
>
> 1. **The spec is at v2, challenged once and not yet re-challenged.**
>    `spec-challenger` returned v1 accept-with-changes; v2 answers the four blocking
>    findings but has not itself been reviewed. This plan must not be executed until
>    it has, and in any case not until spec OQ-1 is settled (§5 step 1, §7 item 1) —
>    it carries an assumption that would change §1.3 and §4.1 if it is false.
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

There are now **three** pieces in the backend, not one, and the order of
importance is the reverse of the order of size.

### 1.1 The control: a guarded dialler, applied per connection

Spec v2 §V2-3 requires that a calendar credential only ever be written onto a
connection whose peer address is inside a fixed set of non-routable ranges, judged
immediately before each connection, on every hop, failing closed.

**The mechanism is `net.Dialer.Control`.** It is invoked after resolution and
before the connect syscall, and it is handed the literal `ip:port` the kernel is
about to use. That is the only place in the Go network stack where the address
being *used* is visible, as opposed to the address that was *looked up a moment
ago* — which is what closes the time-of-check window in spec AC-9. A design that
resolves, checks, and then dials the original name re-resolves and does not satisfy
AC-9; a design that resolves, checks, and dials the checked literal is acceptable
but strictly more code for a weaker guarantee. Use `Control`.

The predicate is a pure function over `netip.Addr`:

- **unmap first.** `netip.Addr.Unmap()` turns `::ffff:127.0.0.1` into `127.0.0.1`
  so it is judged as what it is (spec AC-7).
- **permit** `127.0.0.0/8`, `::1/128`, `10.0.0.0/8`, `172.16.0.0/12`,
  `192.168.0.0/16`, `fc00::/7`.
- **refuse everything else**, and refuse these explicitly and by name in the test
  table because they are the ones a loose check lets through: `169.254.0.0/16` and
  `fe80::/10` (link-local — this is where instance metadata lives), `0.0.0.0`,
  `::`, and `172.32.0.1`, which neighbours the private range and is public.
- **do not** use `net.IP.IsPrivate()` alone: it does not cover loopback and does
  not exclude link-local. Compose the set explicitly from prefixes so the test
  table in §2 is a direct transcription of it.

This predicate is the whole safety argument. It is a dozen lines and it is the
part of this change that deserves the most review attention.

### 1.2 The second control: redirects are not followed

Spec AC-10 requires that in a redirected deployment a `3xx` is never followed,
**including to a permitted address**. `http.Client.CheckRedirect` returning an
error achieves it. Returning `http.ErrUseLastResponse` does **not** — that yields
the 3xx to the caller as a success, which the provider library would then try to
decode, producing a confusing failure instead of a clear one. Return a named error.

This is belt-and-braces with §1.1: the dialler would already stop hop two leaving
the private network. The reason both exist is stated in spec §V2-3 — the dialler
does not stop a stand-in from moving the client to a *different service on the same
private network*, which is how a suite goes green against the wrong thing.

### 1.3 The plumbing: how the guards reach the provider client

**This is the part the plan cannot fully specify yet, and it must not pretend
otherwise.** The guards live on an `*http.Client`. Supplying one to the provider
library means `option.WithHTTPClient`, and `option.WithHTTPClient` is documented as
conflicting with `option.WithTokenSource` — the library will not build a credential
transport over a client you supplied. If that holds at the pinned version, the
redirected construction path attaches the credential itself:

```
oauth2.Transport{Source: ts, Base: <transport carrying the guarded dialler>}
   ↓ wrapped in http.Client{CheckRedirect: refuse}
   ↓ option.WithHTTPClient(...) + option.WithEndpoint(...)
```

and the production path stays exactly as it is today — one option, the library's
own transport, the library's own base path. **Two construction paths, chosen by
whether a redirection is configured.** Spec AC-1 and AC-3 are what hold the
production path still; spec AG-2 records the resulting asymmetry as accepted.

Whether that layering is right is **spec OQ-1(c)**, and it is the one thing that
must be settled before this section is implemented (§5 step 1, §7 item 1). If the
pinned version instead selects the `cloud.google.com/go/auth` path, the token
source may be adapted rather than replaced and the wrapping differs. The predicate
in §1.1 and the policy in §1.2 are unaffected either way — which is deliberate:
the safety argument does not depend on the library's internals, only on the fact
that every connection it makes goes through a dialler we supplied.

### 1.4 The demoted startup check

A small `backend/config` package still reads the two variables and returns either
"no override", or an endpoint plus the startup notice, or an error. It is still
called from `main.go` before routes are mounted and before either cron starts,
because a bad value should fail the boot rather than surface as a 500 on one
user's screen at the first calendar request.

**What changed is what it checks and what it claims.** It refuses the unsafe
*combination* (spec AC-4), refuses what it cannot parse (AC-5), and refuses a
**literal** address outside the permitted prefixes (AC-15). It does **not**
classify host names. There is no "single-label names are trusted" rule and no dot
counting — that rule is what spec v2 removed, and re-introducing it in this package
would reinstate the defect the challenge was about. A configured host that is a
name is accepted here and judged by §1.1 at connect time. Spec AC-6 is the test
that this is true.

Its doc comment must say, in one sentence, that it is not the security control and
that §1.1 is. A future reader who assumes otherwise will loosen it.

### 1.5 The stand-in

**A Go service under `e2e/fake-google/`, and it is still the largest part of this
work by volume.** The argument is in §4.1. The backend change is now perhaps fifty
lines rather than fifteen, and a dozen of those carry the entire security property.

---

## 2. Backend — `Enach/clockwise-like`

### 2.1 Tests to write first — the control (§1.1, §1.2)

**Write these before the configuration tests.** They carry the security property,
and writing them first stops the implementation drifting back towards a
string-validation design.

| Test | File | Proves | Spec AC |
|---|---|---|---|
| `TestPermittedPeer_Table` | `backend/calendar/guard_test.go` *(new)* | The §1.1 predicate over `netip.Addr`: permits `127.0.0.1`, `127.0.0.2`, `127.1.2.3`, `::1`, `::ffff:127.0.0.1`, `10.1.2.3`, `172.16.0.1`, `172.31.255.254`, `192.168.1.1`, `fd00::1`; refuses `169.254.169.254`, `fe80::1`, `0.0.0.0`, `::`, `172.32.0.1`, `8.8.8.8`, `142.250.185.78`. Every row asserted individually so a failure names the address. | AC-7 |
| `TestGuardedDialler_RefusesNonPermittedPeer` | `backend/calendar/guard_test.go` *(new)* | With a resolver double answering a public address for the configured name, a calendar call fails before connect; a recording listener at that address logs nothing; the error text names the refused peer. | AC-8 |
| `TestGuardedDialler_JudgesEveryConnection` | `backend/calendar/guard_test.go` *(new)* | A resolver double that answers a permitted address on call 1 and a non-permitted one on call 2: first call succeeds, second is refused. Falsifies any design that validates once at startup. | AC-9 |
| `TestRedirectedClient_DoesNotFollowRedirect` | `backend/calendar/guard_test.go` *(new)* | Two `httptest.Server`s; the first answers `302` to the second. The operation errors and server two's handler is never entered. **Run twice** — once with the target outside the permitted set, once with it inside — because refusing only external redirects is the subtle wrong answer. | AC-10 |
| `TestRedirectedClient_StillSendsCredential` | `backend/calendar/guard_test.go` *(new)* | Against an `httptest.Server` on loopback: the arriving request carries a bearer credential. Guards the §1.3 rewiring — if `WithHTTPClient` displaces the library's credential attachment and nothing re-attaches it, this is what catches it. | AC-21 |

`guard_test.go` is the internal-test form (`package calendar`) because the
predicate and the dialler are unexported. The resolver double is injected through
the `Resolver` field of the `net.Dialer` the redirected path builds; that field
must therefore be assignable in tests, which is a constraint on how §1.1 is
structured and is stated here so it is not discovered late.

### 2.2 Tests to write first — construction (§1.3)

| Test | File | Proves | Spec AC |
|---|---|---|---|
| `TestNewClient_ProductionPathIsUnchanged` | `backend/calendar/client_endpoint_test.go` *(new)* | With no override configured: the constructed service's `BasePath` equals the library's own literal, **and** no guarded dialler and no redirect policy are installed. Fails the moment anyone gives the override a default or installs a guard unconditionally. | AC-1, AC-2 |
| `TestNewClient_HonoursConfiguredBasePath` | `backend/calendar/client_endpoint_test.go` *(new)* | After the override is set, `BasePath` is exactly the configured value, trailing slash preserved. Also settles §7 item 2 on its first run. | AC-1 |
| `TestCalendarClient_ListEventsReachesConfiguredEndpoint` | `backend/calendar/client_endpoint_test.go` *(new)* | Against an `httptest.Server`: the request arrives at `/calendars/primary/events`, carries an `Authorization` header, and the decoded events come back. | AC-16, AC-21 |
| `TestCalendarClient_GetFreeBusyReachesConfiguredEndpoint` | `backend/calendar/client_endpoint_test.go` *(new)* | Against an `httptest.Server`: `POST /freeBusy` with the requested identifiers in the body. | AC-17 |
| `TestCalendarClient_CreateAndDeleteEventReachConfiguredEndpoint` | `backend/calendar/client_endpoint_test.go` *(new)* | `POST` then `DELETE` on `/calendars/primary/events[/{id}]` — the two verbs focus time depends on. | AC-18, AC-19 |

Internal-test form for the same reason: the assertion is on
`CalendarClient.service.BasePath`, which is unexported
(`backend/calendar/client.go:11-14`). Asserting on the constructed client rather
than on the environment variable is the only test in the repository that would
catch someone defaulting the override to a real address.

### 2.3 Tests to write first — the demoted startup check (§1.4)

| Test | File | Proves | Spec AC |
|---|---|---|---|
| `TestCalendarEndpoint_BothUnsetReturnsNoOverride` | `backend/config/endpoints_test.go` *(new)* | Neither variable set → empty endpoint, no notice, no error. The production case. | AC-1 |
| `TestCalendarEndpoint_FlagWithoutURLIsInert` | `backend/config/endpoints_test.go` *(new)* | Test mode declared, no URL → empty endpoint, no error. | AC-2 |
| `TestCalendarEndpoint_URLWithoutFlagIsRefused` | `backend/config/endpoints_test.go` *(new)* | URL set, test mode not declared → error whose text names **both** variables. | AC-4 |
| `TestCalendarEndpoint_RejectsUnintelligibleValue` | `backend/config/endpoints_test.go` *(new)* | Table: unparseable, empty host, non-HTTP scheme, **and an authority carrying userinfo** (`http://fake-google@evil.example.com/calendar/v3/`) — each refused with the value named, none partially applied. Userinfo is refused, never stripped. | AC-5 |
| `TestCalendarEndpoint_DoesNotClassifyHostsByShape` | `backend/config/endpoints_test.go` *(new)* | The spec AC-6 table verbatim: `2130706433`, `0x7f000001`, `017700000001`, `127.1`, `0x7f.0.0.1`, `fake-google`, `localhost`, `[::1]`, `[0:0:0:0:0:0:0:1]`, `[::ffff:127.0.0.1]`, `[::1%25lo]`, `0.0.0.0`, `[::]`, `evil.example.com`, `fake-google@evil.example.com`. Each is either accepted-pending-connect-time-judgement or refused for a reason `TestCalendarEndpoint_RejectsUnintelligibleValue` names — **and no row is accepted because of the shape of its host**. This is the regression test for the exact defect the challenge found: nothing that fails to parse as an address may be reclassified as trusted. | AC-6 |
| `TestCalendarEndpoint_RefusesNonPermittedLiteral` | `backend/config/endpoints_test.go` *(new)* | Table of **literal** hosts: a public IP, `169.254.169.254`, `0.0.0.0`, `[::]` → refused with the value named; `127.0.0.1`, `[::1]`, `10.0.0.5` → accepted; `fake-google` and `evil.example.com` → **accepted here**, because names are judged at connect time. That last row is the one a reviewer will query; the comment must point at spec AC-15 and §1.4. | AC-15 |
| `TestCalendarEndpoint_NoticeNamesTargetAndDisclaimsProduction` | `backend/config/endpoints_test.go` *(new)* | On a valid override the returned notice contains the endpoint and states the deployment is not production. | AC-14 |

### 2.4 Tests that run a process, not a function

Spec AC-3, AC-12, AC-13 and AC-14 assert that a **started server** did or did not
serve. `CLAUDE.md` exempts `main.go` wiring from coverage, and a unit test over the
validator cannot observe a process — which was the challenge's blocking finding 3.
These therefore live in the e2e suite, not in `backend/`.

| Scenario | File | Proves | Spec AC |
|---|---|---|---|
| *a production-shaped configuration still boots* | `e2e/tests/startup.spec.ts` *(new)* | The built backend image with today's environment starts and answers its health route. Guards against the new startup step bricking production. | AC-3 |
| *an endpoint without the declaration refuses to boot* | `e2e/tests/startup.spec.ts` *(new)* | Non-zero exit, nothing accepted on the port, output names the setting. | AC-12 |
| *an unintelligible or non-permitted-literal endpoint refuses to boot* | `e2e/tests/startup.spec.ts` *(new)* | One case per refusal class; non-zero exit, output names the value. | AC-13 |
| *a redirected deployment announces itself* | `e2e/tests/startup.spec.ts` *(new)* | The running stack's backend output states redirection is in effect, names the target, and disclaims production. | AC-14 |
| *the shipped binary refuses a redirection* | `e2e/tests/seam.spec.ts` *(new)* | With the stand-in in its redirect mode (§4.1), the screen shows a failure and the recording sink (§4.3) logs no connection. AC-10 proves the policy; this proves it is in the binary. | AC-11 |

**Mechanism**: these run the image directly rather than through the compose stack —
`docker run --rm -e ... <backend image>` with the exit status and captured output as
the assertion, which is why they are a separate spec file with no page fixture. A
Playwright test may do this; it is still the suite that owns "does the built artifact
behave". If the implementer finds a cheaper host for them (a shell script invoked by
`make e2e`), that is acceptable **provided the gate runs it** — the criterion is that
something in `make verify` observes a process, not that Playwright does.

**Existing tests that change: none.**
`backend/calendar/calendar_test.go:23-36` (`TestNewClient`) constructs a client
with a fake credential and asserts it is non-nil with calendar id `"primary"`. It
makes no request and asserts nothing about the endpoint, so it keeps passing
untouched. That is also the finding in spec §2.9 — nothing currently protects the
endpoint, which is why the table above is as long as it is.

### Files to change

| File | Change | Risk |
|---|---|---|
| `backend/config/endpoints.go` *(new package, new file)* | One exported function taking the two raw values and returning a result (endpoint, notice) or an error. Pure: no `os.Getenv` inside it. Refuses the unsafe combination, the unintelligible value, and a non-permitted **literal** — and classifies no host by name shape (§1.4). Its doc comment states that it is not the security control. | Low. No caller but `main.go`. |
| `backend/calendar/guard.go` *(new file)* | The §1.1 peer predicate over `netip.Addr`, the `net.Dialer` carrying it in `Control`, and the `CheckRedirect` of §1.2. Roughly a dozen meaningful lines. | **This is the file the security property lives in.** It is small and it should get the majority of review attention. A permissive edit here is silent. |
| `backend/calendar/client.go` | Add an unexported package-level base URL and an exported setter. `NewClient` keeps today's single-option construction when it is empty, and takes the guarded path of §1.3 when it is not. No signature change, so none of the ten call sites in spec §2.2 is touched. | Medium, and higher than plan v1 said. Two construction paths now exist and only one is exercised in production. `TestNewClient_ProductionPathIsUnchanged` is what holds the production one still. The setter must be called before any request; §5 sequences it before the crons start. |
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
the thing under test: spec AC-18 and AC-20 assert that blocks and proposed team
members appear on screen, which exercises the production bundle the e2e stack
already builds (`docker/frontend.dockerfile`, wired at
`e2e/docker-compose.e2e.yml:102-117`).

One real risk sits here and belongs in this section rather than being discovered at
assert time. The frontend silently substitutes built-in demo fixtures when the
backend is unreachable (`e2e/README.md:168-171`), so a UI assertion in AC-18 or
AC-20 could pass against demo data while the feature is broken. Every new UI
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
**honour** `timeMin`/`timeMax` — AC-16 asserts a specific window — and must
**ignore unknown query parameters** rather than rejecting them, because the client
library adds its own (`alt`, `prettyPrint`) that are not in this list.

**Behaviour required of it**

- State in memory, keyed by calendar id, reset per scenario. No persistence: the
  stand-in restarting mid-run should be a visible failure, not a silent one.
- **Reject any request with no `Authorization: Bearer` header**, with the Google
  error envelope and a 401. Spec AC-21 depends on this: a permissive stand-in would
  let the suite go green over a broken credential path.
- A control plane under a path the real API does not have, so it can never be
  confused for a product route: `POST /__fixture/reset`,
  `POST /__fixture/calendars/{calendarId}/events` to seed, and
  `GET /__fixture/requests` returning the method-and-path log.
- A `GET /__fixture/health` for the compose healthcheck.

**Two requirements spec v2 adds to the stand-in.**

1. **The request log is load-bearing, not diagnostic.** Spec AC-22 is satisfied
   only by a recorded arrival per enumerated behaviour — the absence of an error at
   the product's edge proves nothing, because the free/busy path returns success on
   failure (spec §V2-2.1, verified at `backend/engine/freebusy_service.go:130-137`).
   So each entry must carry enough to attribute it: method, path, the calendar id
   or queried identifiers, and whether a credential was present. A log of bare
   method-and-path cannot distinguish "focus time asked" from "the week view
   asked", and AC-22 needs per-behaviour attribution.
2. **A redirect mode**, armed through the control plane
   (`POST /__fixture/redirect-next` with a target), making the next product-route
   request answer `302` to that target instead of serving. This is what spec AC-11
   exercises: it is the only way to observe that the refusal of §1.2 is compiled
   into the binary the test stack actually runs, rather than merely present in a
   unit test. It must be **armed per request and disarmed after firing**, so a
   scenario cannot leave the stand-in redirecting for the rest of the run.

**Tests for the stand-in itself**: `e2e/fake-google/store_test.go` covers the
window filter (an event overlapping the boundary is included, one outside is not)
and the free/busy projection. Two tests, because a stand-in that filters wrongly
produces a test failure that will be read as a product bug and cost an afternoon.

### 4.2 Seed changes

The four seeded users have **no credential rows**, deliberately
(`e2e/seed/seed.sql:54`), and four passing scenarios depend on that state
(`e2e/tests/calendar.spec.ts:25,42,52`, `e2e/tests/focus-time.spec.ts:56`). Spec
AC-24 requires those to stay green.

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
- **The stand-in must land on the compose network, not on a published host port.**
  Its address from inside the backend container will be a `172.16/12` address,
  which is inside the permitted set of §1.1. Nothing extra is needed for that —
  but it is the reason spec OQ-2 fixes the set the way it does, and anyone who
  later moves the stand-in to a routable address will find it refused at connect
  time rather than at configuration time. Worth a comment in the compose file.
- **Keep `extra_hosts` (`:87-96`) — and turn the blackhole into a recording sink.**
  Spec AC-23 replaces "nothing is listening on `127.0.0.1`" with "something is
  listening and it recorded no connections". The current arrangement proves a
  connection failed; it produces no evidence of how many were attempted or from
  which path, and spec §V2-3 now makes that mapping load-bearing for a real
  residual risk — token refresh is not redirected, so this mapping is what keeps a
  refresh credential from leaving a test host.
  *Shape*: a tiny listener on the ports the mapped names would be reached on
  (`443`, and `80` if anything uses it), bound inside the backend container's
  network, logging peer and SNI and closing. It can be the same Go module as the
  stand-in with a second entrypoint, or a second tiny binary — cheaper than it
  sounds, and spec OQ-5 is the decision point if the implementer disagrees. The
  mapped names themselves **must not change**: removing them because "we have a
  stand-in now" deletes both AC-22's evidence and that containment.

`e2e/README.md` §3, §5 and §9 all describe the current situation as having no seam;
all three need updating in the same change, and §5's table is the one a reader
checks first.

### 4.4 Scenarios

| Scenario | File | Spec AC |
|---|---|---|
| *a connected calendar renders its events on the week grid* — currently `test.fixme` | `e2e/tests/calendar.spec.ts:73` | AC-16 |
| *free/busy for an attendee reflects that attendee's real calendar* — currently `test.fixme` | `e2e/tests/calendar.spec.ts:83` | AC-17 |
| *running focus time creates blocks and they appear on the calendar* — currently `test.fixme` | `e2e/tests/focus-time.spec.ts:75` | AC-18 |
| *clearing a week removes the blocks from both the database and the calendar* — currently `test.fixme` | `e2e/tests/focus-time.spec.ts:86` | AC-19 |
| *rescanning the calendar detects 1:1s and proposes team members* — currently `test.fixme` | `e2e/tests/manager-team.spec.ts:137` | AC-20 |
| *a calendar request the provider rejects is surfaced, not swallowed* — **new** | `e2e/tests/calendar.spec.ts` | AC-21 |
| *every redirected behaviour is observed arriving at the stand-in* — **new** | `e2e/tests/seam.spec.ts` *(new)* | AC-22 |
| *nothing reached a real provider address during the run* — **new** | `e2e/tests/seam.spec.ts` *(new)* | AC-23 |
| *the personal-calendar sync is redirected too* — **new** | `e2e/tests/seam.spec.ts` *(new)* | AC-25 |
| *the shipped binary refuses a redirection* — **new** | `e2e/tests/seam.spec.ts` *(new)* | AC-11 |
| startup behaviour, five cases — **new** | `e2e/tests/startup.spec.ts` *(new)* | AC-3, AC-12, AC-13, AC-14 |

**Three notes the implementer must not skip.**

- **AC-22 needs a membership list, and spec §V2-3 provides one.** The scenario
  iterates the enumerated behaviours and asserts a matching arrival in the
  stand-in's log for each. A behaviour with no arrival fails **regardless of what
  the product returned** — that is the whole point of the rewrite, and a scenario
  that asserts "no error occurred" instead reintroduces the defect.
- **AC-17 must constrain the primary-provider case only.** It goes through
  `backend/engine/freebusy_service.go:119-138`, whose `default:` branch *is* the bug
  PAC-47 exists to fix. A scenario written as "whatever the provider setting,
  free/busy answers from the stand-in" is a green test over the exact lines PAC-47
  must rewrite. Seed the connected user with the primary provider and assert nothing
  about any other value.
- **AC-25 needs the background sync to have run.** It is on a thirty-minute timer
  (`backend/main.go:57-77`), which no scenario can wait for. Either the scenario
  triggers the same work through a route that does it synchronously, or it is
  written as an assertion over the stand-in's log at the end of a long run and
  marked accordingly. If neither is workable, say so and re-open it rather than
  quietly dropping AC-25 — it exists because the redirection reaches further than
  spec §2.2's table suggests.

Plus a new helper `e2e/fake.ts` *(new)*: a typed client for the control plane —
`reset()`, `seedEvents()`, `requests()` — so no scenario writes a raw fetch against
the fixture routes.

**The two scenarios that are not this issue's** stay disabled and keep their
comments: `e2e/tests/auth-session.spec.ts:115` (needs the sign-in redirect, spec
§5) and `e2e/tests/scheduling-links.spec.ts:144` (needs PAC-44's test ids).

**Expect the newly enabled scenarios to fail on first run, and treat that as the
result rather than an obstacle.** API-014, API-015 and API-017
(`docs/factory/api-audit.md:248-251`) are confirmed shape defects on exactly the
focus-blocks, focus-run and event-patch responses that AC-18 and AC-19 assert
against. The scenarios must assert what the product *should* return. If a defect
cannot be fixed in the same change, the scenario is disabled again with a comment
naming the audit finding and the issue that will re-enable it, per
`docs/factory/README.md` §3 rule 4 — never loosened to match the broken shape.

**The fifteen-minute cache** — singular. Plan v1 and spec v1 said two; the second
one does not exist in any path these scenarios take and cannot cache anything,
because `backend/calendar/personal_reader.go:16` builds a fresh
`WebcalClient` and calls `ListEvents` on it immediately, so the branch at
`backend/calendar/webcal_client.go:50-56` is never taken (spec §V2-2.2, verified).

The one that is real is the free/busy result cache, keyed per
user-plus-address-plus-date (`backend/engine/freebusy_service.go:98,139,155-172`),
whose clock cannot be advanced from outside the process. AC-17 must use an attendee
address and a date no other scenario uses. **Record that as isolation, not
coverage**: per spec AG-1 the cache's own behaviour stays permanently untested, and
that is an accepted gap, not a solved problem. If a scenario needs two different
answers for one key, it cannot be written today — that is a finding for a clock
issue, not a reason to relax an assertion.

*(One thing that is cheaper than spec §2.8 implied: `FreeBusyService.Clock` is an
**exported** field, `backend/engine/freebusy_service.go:49`, that `main.go` simply
never assigns. Driving this one engine's clock needs an assignment, not a seam. It
is still out of scope — spec §V2-7 — but if a later issue picks it up, it should
know it is a one-liner here and a wider change elsewhere.)*

---

## 5. Sequencing

1. **Settle spec OQ-1 first, before anything else is written — and settle it by
   reading, not by running.** It is three questions now, not two, and the third
   determines the other two and the redirect layering of §1.3. On a machine with
   registry access: `go mod download google.golang.org/api`, then read
   `option/option.go`, `internal/settings.go` and `transport/http/dial.go` at
   **v0.292.0** (`backend/go.mod:22`, `backend/go.sum:184-185`). The ten-line
   `httptest` program plan v1 proposed answers (a) and (b) only and tells you
   nothing about (c) or about redirects, so it is the weaker check. See §7 item 1.
2. **Re-challenge.** The spec is at v2 with four blocking findings addressed;
   `spec-challenger` has not seen v2. **Merge point: v2 is accepted before any code
   is written.**
3. `contract-author` records the one-line no-boundary-change note (§0).
4. **Backend, tests first, and in this order**: §2.1 (the guard) before §2.2
   (construction) before §2.3 (configuration). Writing the guard's table first is
   what keeps the implementation from drifting back to a string check. Watch them
   fail, then write `backend/calendar/guard.go`, then the change to
   `backend/calendar/client.go`, then `backend/config/endpoints.go`, then the
   wiring in `backend/main.go`, then `.env.example`.
4b. **§2.4's process tests** land with the e2e change, not the backend one — they
   need the built image. If the backend PR merges first (step 5), spec AC-3, AC-12,
   AC-13 and AC-14 are unproven between the two merges. That is acceptable and it
   is stated rather than hidden: the window is short and the production-facing risk
   in it is covered by `TestNewClient_ProductionPathIsUnchanged`.
5. `make verify` on the backend. **Merge point: the backend PR merges on its own.**
   It is independently safe — it changes nothing a user can see, adds no
   dependency, and leaves the suite exactly as it is. It should not wait for the
   stand-in.
6. **The stand-in**: `e2e/fake-google/` with its request log and redirect mode, its
   two tests, and the recording sink of §4.3 — built and run in isolation against
   `curl` before anything is wired to it.
7. **Compose and seed**: the new service, the sink, the two backend variables, the
   fifth seeded user and the token row. Verify by starting the stack and confirming
   the backend's startup output carries the redirection notice (spec AC-14) — the
   cheapest proof the whole chain is connected.
8. **Scenarios**, one at a time, in this order: the §2.4 startup cases first
   (AC-3, AC-12, AC-13, AC-14 — they need no product surface at all), then AC-11
   (the redirect refusal, which needs only the stand-in), then AC-22 and AC-23,
   then AC-16, AC-17, AC-21, AC-18, AC-19, AC-20, AC-25. Ascending order of how
   much product surface each touches means the first failure is the seam's fault
   and every later one is the product's.
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

1. **How the pinned provider client behaves, in three parts.** Spec OQ-1, and the
   assumption §1.3 rests on. Plan v1 asked two of the three and proposed the weaker
   way to answer them; both are corrected here.
   - (a) Does `option.WithEndpoint` accept a non-TLS scheme, or reject it?
   - (b) Does the credential transport attach a bearer credential over plain HTTP?
   - (c) **Which credential stack does `option.WithTokenSource` select at
     v0.292.0** — the legacy `golang.org/x/oauth2` transport, or the
     `cloud.google.com/go/auth` path now pinned as an indirect dependency
     (`backend/go.mod:26`, `backend/go.sum:1-2`)? This determines (a) and (b), it
     determines whether `option.WithHTTPClient` really does displace the library's
     credential attachment (§1.3), and it determines the library's own redirect
     layering — which is spec blocking finding 2. **Plan v1 did not ask it.**

   *Settlement, and it is reading rather than running*: on a machine with registry
   access, `go mod download google.golang.org/api`, then read `option/option.go`,
   `internal/settings.go` and `transport/http/dial.go` at the pinned version. That
   answers all three together. **I could not do it here**: there is no `vendor/`
   under `backend/`, `go env GOMODCACHE` is `/root/go/pkg/mod` whose download cache
   holds only `golang.org`, and a filesystem search for any `google.golang.org`
   source directory returns nothing. The registries are blocked.

   *Correction to plan v1*: its contingency — *"`backend/Dockerfile`'s `scratch`
   image grows a trust store"* — is **false and was false when written**.
   `backend/Dockerfile:9-12` is a `scratch` stage that already copies
   `/etc/ssl/certs/ca-certificates.crt` from the builder at `:10`. If (a) or (b)
   resolves against plain HTTP, the work is adding one certificate to an existing
   bundle. The contingency was overstated, which matters because it was presented
   as the cost of the assumption the whole plan rests on.
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
   request log during the first AC-16 run.
5. **Whether the permitted *address* set is right for the topologies that will run
   this.** Spec OQ-2 — and note what is no longer in question: plan v1's
   "loopback literal or single-label host" rule is **gone**, because a single-label
   host resolves wherever a search domain says it does, and because the values
   `2130706433`, `0x7f000001` and `017700000001` failed to parse as addresses and
   were therefore promoted into the trusted bucket. There is no permitted set of
   host *names* any more. What remains open is whether loopback plus the private
   ranges covers every stack that will run this; a stand-in reachable only over a
   routable address would be refused at connect time. *Settlement*: ask whoever
   operates the stacks before the backend PR opens. **If the answer is "we need a
   routable address", the response is to move the stand-in, not to widen the set
   and not to make it configurable** — spec §V2-7 forbids the latter outright.
7. **Whether the process tests of §2.4 belong in Playwright.** They assert exit
   status and captured output of `docker run`, which is not what the suite is shaped
   for. A shell target invoked by `make e2e` would be a better fit and is acceptable
   provided the gate runs it. I could not try either. *Settlement*: the implementer's
   first attempt at AC-12 will make it obvious within an hour.
6. **How much of the newly enabled surface actually passes.** I expect AC-18 and
   AC-19 to fail against API-014/API-015, and AC-16's UI half to depend on
   selectors PAC-44 has not added yet. I could not run any of it. *Cheapest
   settlement*: step 8 of §5 — enable them one at a time, in the order given, and
   let the first run be the measurement rather than trying to predict it here.
