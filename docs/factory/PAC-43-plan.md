# Implementation plan — PAC-43: calendar-dependent behaviour must be verifiable before release

> ## Plan v3 — realigned to the accepted spec (v3 + §V3-7), 2026-10-11
>
> The spec is now **accepted** (`docs/specs/PAC-43.md`, status line `:4`). Binding
> set: **AC-1 to AC-28 as amended by Revision v3 and §V3-7.** Every "AC-n" below
> means that text. What changed from plan v2:
>
> 1. **One construction path for every deployment (§1.3).** v2 had two paths and a
>    production path "exactly as it is today". §V3-2 and §V3-7 item 1 retire that:
>    production now builds its calendar client the same way the test deployment does,
>    and changes in four deliberate, enumerated ways (E1–E4). Every "production is
>    unchanged" sentence and every AG-2 reference is gone from this plan.
> 2. **No ambient proxy (§1.3, AC-28).** The base transport's `Proxy` is nil in every
>    deployment (§V3-3).
> 3. **The single entry point is named (§1.3):** `calendar.NewClient`
>    (`backend/calendar/client.go:16`). All ten production call sites use it, and
>    AC-27 tests through it (§V3-7 item 3).
> 4. **Tests rewritten** for AC-1, AC-3, AC-6 (per-row table), AC-8/AC-9 (connection
>    attempts recorded, no unbindable listener), AC-21, AC-26 (new), AC-27 (new), and
>    AC-28 (new, proxy variables set before process start *and* a same-process
>    positive control).
> 5. **OQ-1 and the re-challenge are done** (§5 steps 1–2, §7 items 1–2 closed). §7
>    item 3 (token refresh margin) is also closed, by reading.
> 6. **Every `file:line` re-verified** against `main` `0c75490` (this branch differs
>    from `main` only in the spec). Moved: the e2e `test.fixme` lines, `go.mod`/`go.sum`,
>    `freebusy_service.go`, `main.go` cron ranges, `e2e/package.json`.
>
> *Plan v2 (2026-09-17), for the record:* moved the control from a startup host-string
> check to a connection-time peer-address rule, added redirect refusal, moved the
> startup-refusal tests to the built image, and added the stand-in's request log,
> redirect mode and recording sink.

- **Spec**: `docs/specs/PAC-43.md` — accepted (v3 + V3-7), 2026-10-11
- **Contract**: **none, deliberately.** See §0.
- **Resolves**: no audit finding and no `x-uncertain` marker. Makes the
  *verification* half of API-014/015/016/017/028/029/041/044 possible (spec §V3-1
  corrects the header: no `x-uncertain` marker becomes resolvable).

> **Stage prerequisites.** The spec is accepted: the `## Challenge — 2026-10-11
> (V3-7)` verdict is *accept*. There is no contract artifact, and there should not
> be one (§0). `contract-author` still owes the one-line no-boundary-change note in
> `contracts/features/PAC-43.md` (§5 step 3); it does not exist yet.
>
> **Nothing in this plan was compiled, run or tested.** Every file path was opened
> before being named. Module source was read from `go env GOMODCACHE`
> (`C:\Users\jouzu\go\pkg\mod`) at the pinned versions:
> `google.golang.org/api@v0.292.0`, `cloud.google.com/go/auth@v0.22.0`,
> `golang.org/x/oauth2@v0.36.0`, and Go 1.25.5's `net` and `net/http`.

---

## 0. Why there is no contract stage

Stage 2 exists so the two repositories cannot drift, and drift is possible only
where a shape crosses the wire. This change adds two deployment configuration
values and changes how the backend builds its *outbound* calendar client. No
request, response, status code or operation of the product's own API changes, so
the bytes on `/api/calendar/events` are the same before and after (AC-24, §V2-6).

Production's outbound client *does* change internally (E1–E4, spec §V3-7 item 1).
Those are changes to how the backend talks to Google, not to how the frontend talks
to the backend, so they are a spec and plan matter, not a contract one.

Two consequences for the gate:

- `make openapi-check` should pass unchanged. If it does not, the change touched a
  handler, and that is a scope failure, not a contract question.
- `contracts/openapi/MIGRATION.md` does not move. No operation is touched.

Spec OQ-3 asks whether a redirected deployment should advertise itself over the
API. A yes would be a boundary change and would create a contract stage. The spec
recommends no, and this plan assumes no.

---

## 1. Approach

The backend has three pieces. In order of importance (the reverse of size): the
connection guard, the shared construction path that carries it, and the demoted
startup check.

### 1.1 The control: a guarded dialler, applied per connection

AC-7 to AC-9 require that, when the guard is on, a calendar credential is only
written onto a connection whose peer address is in a fixed set of non-routable
ranges. The address is judged immediately before each connection, on every hop,
and anything that cannot be judged is refused.

**The mechanism is `net.Dialer.Control`.** Go calls it after resolution and before
the connect syscall, and passes it the literal `ip:port` about to be used
(`$GOROOT/src/net/dial.go:205-212`). That is the only point in the Go network
stack that sees the address being *used*, not the address that was looked up a
moment ago. This is what closes the time-of-check window in AC-9. It also keeps the
dialler's normal behaviour: the system resolver when no test resolver is supplied,
and dual-stack fallback across addresses. A refused attempt fails that attempt, and
when every address is refused the dial fails.

The predicate is a pure function over `netip.Addr`:

- **Unmap first.** `netip.Addr.Unmap()` turns `::ffff:127.0.0.1` into `127.0.0.1`,
  so a mapped address is judged as the address it maps to (AC-7).
- **Permit** `127.0.0.0/8`, `::1/128`, `10.0.0.0/8`, `172.16.0.0/12`,
  `192.168.0.0/16`, `fc00::/7`. This is exactly AC-7's set (spec §V3-5, "OQ-2 now
  agrees with AC-7").
- **Refuse everything else.** The test table names, one by one, the cases a loose
  check lets through: `169.254.0.0/16` and `fe80::/10` (link-local, where instance
  metadata lives), `0.0.0.0`, `::`, and `172.32.0.1`, which sits next to the private
  range and is public.
- **Do not** use `net.IP.IsPrivate()` alone. It does not cover loopback and does not
  exclude link-local. Build the set explicitly from prefixes, so the §2.1 table
  transcribes it directly.

The error returned from `Control` names the refused peer, which is what AC-8 asks
the failure to say.

**Two test seams are structural, so they are stated here and not found late.**
Both are unexported, and both are nil in every production build:

- **The resolver.** The dialler's `net.Dialer.Resolver` must be assignable from
  tests. Tests supply a `*net.Resolver` with `PreferGo: true` and a `Dial` that
  reaches an in-process DNS responder. Go honours `PreferGo` on Windows as well as
  Linux (`$GOROOT/src/net/conf.go:196-214`, `lookup_windows.go:94`).
- **The attempt recorder.** This is an optional hook chained *after* the guard
  inside `Control`. It sees only the addresses the guard let through, at the last
  point before `connect`, so "the recorder saw no attempt to X" is the observable
  form of "no connection is attempted to X" (AC-6, AC-8, AC-9).

### 1.2 The second control: redirects are not followed

`http.Client.CheckRedirect` returns a named error, for example a package-level
"calendar: redirect refused" sentinel. It must **not** return
`http.ErrUseLastResponse`: that hands the 3xx back as a response, the library then
tries to decode it, and the failure is confusing instead of clear.

Per §V3-2, this policy now applies **in every deployment, production included**
(E2). It is no longer belt-and-braces for the test stack only. The library's
default path follows redirects and re-attaches the credential on every hop
(`auth@v0.22.0/httptransport/httptransport.go:243-245` sets no `CheckRedirect`,
and `transport.go:550` sets the header on a clone of every request). So without
this policy, production would forward a user's token to wherever a 3xx pointed.

### 1.3 One construction path for every deployment

**This replaces plan v2's "two construction paths".** Spec §V3-2 and §V3-7 item 1
(E1–E4) govern it.

**The single entry point is `calendar.NewClient(ctx, tokenSource)`**
(`backend/calendar/client.go:16-22`). Its signature does not change. It is what all
ten production call sites call (§2.6 lists them, re-verified on `main`). It is also
what the two in-package callers call (`client_factory.go:28`, which is dead, and
`personal_reader.go:30`). AC-27 tests through this function and nothing lower.

From that entry point, every deployment does the following:

1. **The credential** comes from the same token source the call site passes today.
   For eight of the call sites that is the refreshing source from
   `auth.TokenSource` (`backend/auth/google_oauth.go:32-34`). For
   `personal_reader.go:30` it is a static source, as today. The credential source
   does not change.
2. **The base transport** is a clone of `http.DefaultTransport` with:
   - **`Proxy` set to nil** (§V3-3, E3). The default is `http.ProxyFromEnvironment`
     (`$GOROOT/src/net/http/transport.go:45-46`).
   - **`DialContext` set to the §1.1 dialler**, which uses the same 30-second
     connect timeout and keep-alive as the default dialler it replaces
     (`transport.go:47-50`). The guard predicate runs in `Control` **only when the
     configuration decision says the guard is on**, that is, in the test deployment.
     In production the dialler is a plain dialler and `Control` is nil.
   - **The two tunings the library's default path applies today**, so E1 does not
     quietly change connection behaviour: `MaxIdleConnsPerHost = 100`, and an
     HTTP/2 read-idle ping of about 31 seconds
     (`auth@v0.22.0/httptransport/transport.go:118-143`). On Go 1.25 the second one
     can be set through `http.Transport.HTTP2` without importing `x/net/http2`.
     These tunings are parity, not spec. If the implementer drops them, say so in
     the PR.

   The base transport is built **once per configuration**, when `Configure` is
   called (see below), not on every `NewClient` call. Today the library clones a
   fresh transport on every construction (`transport.go:123`), and the ten call
   sites construct per request. Sharing one transport is cheaper and changes
   nothing observable.
3. **The credential transport** is an `oauth2.Transport` (`Source` = the token
   source from step 1, `Base` = the shared base transport), built for each
   `NewClient` call. It sets the bearer on a clone of every request
   (`golang.org/x/oauth2@v0.36.0/transport.go:45-55`), whatever the scheme. That
   settles OQ-1(b) for the path we now own.
4. **The client** is `http.Client{Transport: <step 3>, CheckRedirect: <§1.2>}`.
5. **It is passed to the library** as
   `googlecalendar.NewService(ctx, option.WithHTTPClient(c), option.WithEndpoint(e))`.
   This works because of how the pinned library handles these options:
   - `option.WithHTTPClient` makes `htransport.NewClient` return the supplied client
     as it is, before either credential stack is considered
     (`google.golang.org/api@v0.292.0/transport/http/dial.go:44-46`). That precedes
     the new-auth branch at `:48-53`, which the generated service otherwise forces
     with `internaloption.EnableNewAuthLibrary()` (`calendar/v3/calendar-gen.go:186`).
     So the library adds no credential transport, no redirect policy and no proxy
     behaviour of its own.
   - `WithHTTPClient` and the absence of `WithTokenSource` pass `Validate`
     (`internal/settings.go:166-236`).
   - **The legacy-stack switch therefore has no effect** on calendar traffic (E4).
     `GOOGLE_API_GO_EXPERIMENTAL_DISABLE_NEW_AUTH_LIB`
     (`internal/settings.go:27,120`) is read only after `:44-46` has already
     returned.
   - **The endpoint is still resolved first** (`dial.go:39`). A value containing
     `://` is used verbatim, with no scheme check (`internal/cba.go:174-176`).
     `calendar-gen.go:200-202` then sets `Service.BasePath` to it.
   - **The endpoint `e` must end in `/calendar/v3/`.** Requests are built with
     `googleapi.ResolveRelative` (`googleapi/googleapi.go:377-400`, which uses
     `url.ResolveReference`). Without the trailing slash the `v3` segment is
     dropped. So:
     - in production, `e` is the published address
       `https://www.googleapis.com/calendar/v3/`, the same literal as the library's
       `basePath` (`calendar-gen.go:100`);
     - in the test deployment, `e` is the configured stand-in address, already
       checked by §1.4 (AC-26).

**Where the decision lives.** `backend/calendar` holds one unexported package-level
value: the configured endpoint, the guard flag, and the shared base transport built
from them. It has one exported setter, `Configure`, which takes the
`backend/config` decision.

- **Its zero value is the production decision** (published address, guard off). So
  a process, or a test, that never calls `Configure` gets exactly what production
  gets.
- `main.go` calls `Configure` once, before any cron or route exists (§2.6).
- The value must be safe against a concurrent read, because tests reconfigure it.
  An `atomic.Pointer` or a mutex both work. Tests that call `Configure` must not run
  in parallel with each other, and must restore the previous value with
  `t.Cleanup`.

**What production gives up, all named by the spec (§V3-2):**

- the new auth library's universe-domain check (`auth@v0.22.0/httptransport/transport.go:538-547`),
  which matters only outside `googleapis.com`;
- the OpenTelemetry wrapper. The v3 challenge confirmed the backend configures no
  tracing SDK.

**What it keeps:** the same address, the same token source, the same bearer header,
and the same responses (§V3-7 item 1).

### 1.4 The demoted startup check

A small `backend/config` package reads nothing itself. `main.go` hands it two raw
strings, and it returns one of two things:

- **A decision**: the endpoint, the guard flag, and an optional startup notice.
- **An error** naming the offending value.

Its rules:

- **Neither value set, or only the declaration set** → the production decision:
  published address, guard off, no notice (AC-1, AC-2).
- **Address set, declaration not set** → refused, naming both settings (AC-4).
- **Address set and the declaration set** → parsed with `net/url`. It is refused,
  naming the value, when:
  - it is unparseable, has no host, or has a scheme that is not `http`/`https`
    (AC-5);
  - it carries userinfo (AC-5);
  - its host is a literal outside the permitted set (AC-15). Hosts in square
    brackets are parsed with `netip.ParseAddr`, and the IPv4 dotted-quad form is
    parsed the same way;
  - its host is a zone-scoped literal such as `[::1%25lo]` (AC-6);
  - its path does not end in `/calendar/v3/` (AC-26). The refusal names the required
    ending.

  Otherwise the result is a decision with the configured address, the guard on, and
  a notice that names the target and says the deployment is not production (AC-14).

**It classifies no host by its shape.** A host that does not parse as a literal is a
*name*, and every name is accepted here, whatever it looks like. That covers
`2130706433`, `0x7f000001`, `017700000001`, `127.1`, `0x7f.0.0.1`, `fake-google`,
`localhost` and `evil.example.com` (AC-6 rows 1–2). Names are judged by §1.1 at
connect time. Its doc comment says in one sentence that it is not the security
control, and that §1.1 is.

**Variable names (this plan's choice; the spec names none):**

- `PACEDAY_ALLOW_STANDINS`: the test-mode declaration. It counts as declared only
  when `strconv.ParseBool` returns true.
- `GOOGLE_CALENDAR_API_BASE_URL`: the address, matching `e2e/SEAM-REQUIRED.md`
  item A.

The published address constant lives in `backend/config`, which is a leaf package.
`backend/calendar` imports it.

### 1.5 The stand-in

The stand-in is a Go service under `e2e/fake-google/`, and it is still the largest
part of the work by volume (§4.1). The backend change is about a hundred lines, and
a dozen of them carry the safety property: the predicate, and `Proxy = nil`.

---

## 2. Backend — `Enach/clockwise-like`

### 2.1 Tests to write first — the guard and the redirect policy (§1.1, §1.2)

**Write these first.** They carry the safety property, and writing them first keeps
the implementation from drifting back towards a string check.

**Shared test helper, used by §2.1–§2.3:** `backend/calendar/dnsdouble_test.go`
*(new)*, in `package calendar`. It provides:

- an in-process DNS responder whose answer per name can be swapped between queries;
- a `*net.Resolver` pointed at it (`PreferGo`, `Dial`);
- the attempt recorder of §1.1.

It uses `golang.org/x/net/dns/dnsmessage`. `golang.org/x/net v0.58.0` is already in
the module graph as an indirect requirement (`backend/go.mod:69`), so this moves it
to a direct requirement through `go mod tidy`. No new module is downloaded.

**Test names use `.test` names** (for example `stand-in.pac43.test`). That keeps any
hosts file out of the answer and stays out of the search-domain path.

| Test | File | Proves | Fails today because | AC |
|---|---|---|---|---|
| `TestPermittedPeer_Table` | `backend/calendar/guard_test.go` *(new)* | The §1.1 predicate. Permits `127.0.0.1`, `127.0.0.2`, `127.1.2.3`, `::1`, `::ffff:127.0.0.1`, `10.1.2.3`, `172.16.0.1`, `172.31.255.254`, `192.168.1.1`, `fd00::1`. Refuses `169.254.169.254`, `fe80::1`, `0.0.0.0`, `::`, `172.32.0.1`, `8.8.8.8`, `142.250.185.78`. One subtest per row, so a failure names the address. | The predicate does not exist (compile failure). | AC-7 |
| `TestGuard_RefusesNonPermittedResolution` | `guard_test.go` | Guard on, and the name resolves to `142.250.185.78`, then `169.254.169.254`. A calendar operation through `NewClient` fails, the error names the refused peer, and the recorder logs no attempt to that address. | The library dials whatever resolution returns; there is no guard and nothing to configure. | AC-8 |
| `TestGuard_JudgesEveryConnection` | `guard_test.go` | Guard on, and the answer is `127.0.0.1` (an `httptest` listener) for call 1, then a public address for call 2. Call 1 reaches the listener (positive control; the recorder sees it). Call 2 fails, and the recorder sees no attempt to the public address. **The listener disables keep-alive**, so call 2 must dial. A pooled connection is not a new connection, and reusing one would make the test pass vacuously. | As AC-8. | AC-9 |
| `TestClient_DoesNotFollowRedirect` | `guard_test.go` | Guard on. Server A answers `302`. **Two subtests:** Location is server B on loopback (inside the permitted set), and Location is a `.test` name resolving to a public address. In both, the operation fails with the named redirect error, B's handler is never entered, and the recorder sees no second dial. | The library follows up to 10 redirects and re-attaches the bearer on each one. | AC-10 |
| `TestClient_GuardOnStillSendsBearer` | `guard_test.go` | Guard on, configured address on loopback, static token `T`. The request arrives with `Authorization: Bearer T`. This is the in-process half of AC-21's "the credential is still sent", for the guarded configuration. | No configuration can point the client anywhere. | AC-21 (unit support) |

### 2.2 Tests to write first — the shared path through the real entry point (§1.3)

| Test | File | Proves | Fails today because | AC |
|---|---|---|---|---|
| `TestNewClient_UnconfiguredIsProductionDecision` | `backend/calendar/client_endpoint_test.go` *(new, `package calendar`)* | With `Configure` never called, and again after `Configure` with the production decision: `c.service.BasePath` is exactly the published address, and the package's decision has the guard off. It fails the moment anyone gives the redirection a default or turns the guard on unconditionally. | `Configure` and the decision do not exist (compile failure). | AC-1, AC-2 |
| `TestPublishedEndpoint_MatchesLibraryDefault` | `client_endpoint_test.go` | The published-address constant equals the `BasePath` of a bare `googlecalendar.NewService(...)`, which is the library's own `basePath` (`calendar-gen.go:100`). This catches drift on a library upgrade. | The constant does not exist. | AC-1 |
| `TestNewClient_HonoursConfiguredBasePath` | `client_endpoint_test.go` | After `Configure` with `http://stand-in.pac43.test:NNNN/calendar/v3/`, `BasePath` is that value verbatim, trailing slash included. | No configuration exists. | AC-1 (inverse), AC-26 |
| `TestCalendarClient_ListEventsReachesConfiguredEndpoint` | `client_endpoint_test.go` | Against an `httptest.Server`: the request arrives at `/calendar/v3/calendars/primary/events` with `timeMin`/`timeMax`, carries `Authorization`, and the events decode. | No configuration exists. | AC-16 (unit support) |
| `TestCalendarClient_GetFreeBusyReachesConfiguredEndpoint` | `client_endpoint_test.go` | `POST /calendar/v3/freeBusy`, with the requested identifiers in the body. | As above. | AC-17 (unit support) |
| `TestCalendarClient_CreateAndDeleteEventReachConfiguredEndpoint` | `client_endpoint_test.go` | `POST`, then `DELETE`, on `/calendar/v3/calendars/primary/events[/{id}]`. | As above. | AC-18, AC-19 (unit support) |
| `TestNewClient_ProductionSettingsSendBearerAndRefuseRedirect` | `backend/calendar/client_production_test.go` *(new, **external** `package calendar_test`)* | **AC-27, through the real entry point.** The test uses only exported API, exactly as the ten call sites do. It calls `calendar.Configure` with production's decision except for the endpoint, which is listener A's `/calendar/v3/` URL (guard off). It calls `calendar.NewClient(ctx, oauth2.StaticTokenSource(T))`, then `ListEvents`. A receives `Authorization: Bearer T`. A then answers `302` to listener B: the operation fails, and B records nothing. **Repeated under `t.Setenv("GOOGLE_API_GO_EXPERIMENTAL_DISABLE_NEW_AUTH_LIB", "true")`.** The library reads it with `os.Getenv` on every call, so `t.Setenv` is sufficient. Being in the external package is what stops it from calling a lower-level builder. | Today `NewClient` cannot be pointed anywhere, and the default stack follows the 302 and re-sends `T` to B (`httptransport.go:243-245`, `transport.go:550`). | AC-27 |
| `TestNoAmbientProxy` + `TestNoAmbientProxy_Child` | `backend/calendar/proxy_test.go` *(new, `package calendar`)* | **AC-28, with both protections against a vacuous pass (§V3-7 item 3).** The parent re-runs the test binary (`os.Args[0] -test.run=^TestNoAmbientProxy_Child$`) with the proxy variables already set in the child's environment, **before that process starts**. The variables are `HTTP_PROXY`, `http_proxy`, `HTTPS_PROXY` and `https_proxy`, each pointing at a recorder R inside the child; `NO_PROXY`, `no_proxy` and `REQUEST_METHOD` are removed. In the child: **(1) positive control:** a plain `http.Client` using `http.ProxyFromEnvironment`, sent to the stand-in name, reaches R. **(2)** A shared-path client from `NewClient`, configured for `http://stand-in.pac43.test:NNNN/calendar/v3/` (a name, never `localhost` or a loopback literal, because both are proxy-exempt) and resolved by the DNS double to stand-in S, runs `ListEvents` with the guard **on**, then with it **off**. S receives both requests directly, and R records only the positive control's connection. The parent asserts that the child passed and that its output reports the positive control as observed. | The default transport uses `ProxyFromEnvironment`. Today's path cannot be pointed at S at all. | AC-28 |

**Two notes on `proxy_test.go`.**

- **The per-process cache.** `ProxyFromEnvironment` reads the variables once per
  process (`$GOROOT/src/net/http/transport.go:955-962`, `envProxyOnce`). That is why
  the variables are set before the child starts, and also why the child runs the
  positive control first, in the same process. Either protection alone satisfies
  §V3-7; this test has both.
- **Windows.** Environment variable names are case-insensitive there, so `HTTP_PROXY`
  and `http_proxy` are the same variable and the "lower-case" rows collapse into the
  upper-case ones. Setting both names is harmless. The evidence is equivalent on
  Windows and complete on Linux.

**Why the client-shape tests are internal.** `TestNewClient_UnconfiguredIsProductionDecision`
asserts on `CalendarClient.service.BasePath`, which is unexported
(`backend/calendar/client.go:11-14`). AC-27 is deliberately the opposite: an
external test, so it can only reach the client through the exported entry point.

### 2.3 Tests to write first — the demoted startup check (§1.4)

All of these fail today because `backend/config` does not exist.

| Test | File | Proves | AC |
|---|---|---|---|
| `TestCalendarEndpoint_BothUnsetIsProductionDecision` | `backend/config/endpoints_test.go` *(new)* | Neither variable set gives the published address, the guard off, no notice and no error. | AC-1 |
| `TestCalendarEndpoint_DeclarationAloneIsInert` | `endpoints_test.go` | Declaration only gives the same result as above. | AC-2 |
| `TestCalendarEndpoint_AddressWithoutDeclarationIsRefused` | `endpoints_test.go` | Address set and declaration absent or false: an error whose text names **both** variables. | AC-4 |
| `TestCalendarEndpoint_RejectsUnintelligibleValue` | `endpoints_test.go` | Table: unparseable, empty host, `ftp://`, and userinfo (`http://fake-google@evil.example.com/calendar/v3/`). Each is refused with the value named, and none is partially applied. Userinfo is refused, never stripped. | AC-5 |
| `TestCalendarEndpoint_HostOutcomeTable` | `endpoints_test.go` | **AC-6's table, row by row**, each as `http://<host>:8080/calendar/v3/` with the declaration set. Accepted as a **name**, guard on: `2130706433`, `0x7f000001`, `017700000001`, `127.1`, `0x7f.0.0.1`, `fake-google`, `localhost`, `evil.example.com`. Accepted as a **permitted literal**: `[::1]`, `[0:0:0:0:0:0:0:1]`, `[::ffff:127.0.0.1]`. Refused, naming the value: `[::1%25lo]` (zone), `0.0.0.0` and `[::]` (AC-15), `fake-google@evil.example.com` (userinfo). | AC-6 (first half) |
| `TestGuard_NameRowsNeverTrusted` | `backend/calendar/guard_test.go` | **AC-6 second half.** For each row the table above accepted as a name, configure it (guard on) and have the DNS double answer `142.250.185.78`. A calendar operation fails, and the recorder sees no attempt to that address. *(For `localhost`, see §7 item 4.)* | AC-6 (second half) |
| `TestCalendarEndpoint_RefusesNonPermittedLiteral` | `endpoints_test.go` | Literals: `8.8.8.8`, `169.254.169.254`, `0.0.0.0` and `[::]` are refused, naming the value. `127.0.0.1`, `127.0.0.2`, `[::1]`, `[::ffff:127.0.0.1]` and `10.0.0.5` are accepted. `fake-google` and `evil.example.com` are **accepted here**, because names are judged at connect time. The comment on that row points at AC-15 and §1.4. | AC-15 |
| `TestCalendarEndpoint_RequiresAPIRoot` | `endpoints_test.go` | `http://fake-google:8080`, `http://fake-google:8080/`, `http://fake-google:8080/calendar/v3` (no trailing slash) and `http://fake-google:8080/other/` are each refused. The text names the value **and** the required ending `/calendar/v3/`. | AC-26 (unit) |
| `TestCalendarEndpoint_NoticeNamesTargetAndDisclaimsProduction` | `endpoints_test.go` | A valid override gives a notice that contains the endpoint and says the deployment is not production. | AC-14 (unit support) |

### 2.4 Tests that run a process, not a function

AC-3, AC-12, AC-13, AC-14 and AC-26's second half assert that a **started server**
did or did not serve. They live in the e2e suite. `CLAUDE.md` exempts `main.go`
wiring from coverage, and a validator test cannot observe a process.

| Scenario | File | Proves | Fails today because | AC |
|---|---|---|---|---|
| *the production reference environment boots and answers health* | `e2e/tests/startup.spec.ts` *(new)* | The built backend image runs with **the backend `environment:` block of `docker-compose.prod.yml:33` onward**, each `${…}` replaced by a placeholder. `DATABASE_URL` points at the e2e postgres on the stack's network, because `main.go:38-41` fatals without a database. It binds and `GET /api/health` (`backend/api/routes.go:21`) succeeds within the suite's startup timeout. Neither new variable is set. | It passes today. It is a **regression guard** that the new startup step and E1–E4 do not brick production; the v3 challenge accepted that this is legitimate for this criterion. | AC-3 |
| *an address without the declaration refuses to boot* | `startup.spec.ts` | Non-zero exit, nothing accepted on the port, and output naming `GOOGLE_CALENDAR_API_BASE_URL` and `PACEDAY_ALLOW_STANDINS`. | The variable is ignored, so the process boots. | AC-12 |
| *an unintelligible, non-permitted-literal, or zone-scoped address refuses to boot* | `startup.spec.ts` | One case per refusal class (AC-5, AC-15, AC-6's zone row). Non-zero exit, and output naming the value. | As above. | AC-13 |
| *an address without the API root refuses to boot* | `startup.spec.ts` | `http://fake-google:8080/calendar/v3` (no trailing slash): non-zero exit, and output naming the value and `/calendar/v3/`. | As above. | AC-26 (e2e) |
| *a redirected deployment announces itself* | `startup.spec.ts` | The running stack's backend output says redirection is in effect, names the target, and says it is not production. | No notice exists. | AC-14 |

**Mechanism.** These run the image directly (`docker run --rm -e …`), with the exit
status and captured output as the assertion, so they sit in their own spec file
with no page fixture. A shell target that `make e2e` invokes is an acceptable
alternative, **provided the gate runs it** (§7 item 7).

The refusals happen before `storage.Open` (§2.6). So AC-12, AC-13 and AC-26 need no
database. AC-3 does.

### 2.5 Existing tests that change: none

`backend/calendar/calendar_test.go:23-36` (`TestNewClient`) builds a client with a
static fake token and asserts it is non-nil with calendar id `"primary"`. It makes
no request and keeps passing through the shared path. No other test in `backend/`
reaches `calendar.NewClient`: the engines use the `calendarOps` double
(`backend/engine/cal_iface.go:15-23`). So nothing currently protects the endpoint or
the request path, which is spec §2.9's finding and why §2.1–§2.3 are long.

### 2.6 Files to change

| File | Change | Risk |
|---|---|---|
| `backend/config/endpoints.go` *(new package)* | The §1.4 decision function, plus the published-address constant. It is pure (no `os.Getenv`). It classifies no host by shape. Its doc comment says it is not the security control. | Low. Its only callers are `main.go` and `backend/calendar`'s zero value. |
| `backend/calendar/guard.go` *(new)* | The §1.1 predicate over `netip.Addr`, and the dialler: a `net.Dialer` with an injectable `Resolver` and a `Control` that applies the predicate **only when the guard is on**, followed by the optional test recorder. | **This file and the `Proxy = nil` line carry the safety property.** A permissive edit here is silent, so it should get most of the review attention. |
| `backend/calendar/transport.go` *(new)* | `Configure`; the unexported package-level decision with its production zero value; the shared base transport (`DefaultTransport` clone, `Proxy` nil, the guarded dialler, the parity tunings); the named redirect error and `CheckRedirect`; and the per-call `oauth2.Transport` + `http.Client` assembly. | **Medium-high. This now runs in production (E1–E3).** It is held by AC-27, AC-28 and AC-1. A mistake shows as a calendar error, not as wrong data (§V3-7 item 1). |
| `backend/calendar/client.go` | `NewClient` (`:16-22`) calls `NewService` with `option.WithHTTPClient(...)` + `option.WithEndpoint(...)` from the shared path, in place of `option.WithTokenSource` (`:17`). **No signature change**, so none of the ten call sites below is touched. | Medium. This is the line where production moves off the library's default stack. |
| `backend/main.go` | Immediately after the `PORT` read (`:28-31`), and **before** the `DATABASE_URL` check (`:33-36`) and `storage.Open` (`:38`): read both variables, call the decision function, `log.Fatal` on error, `log.Printf` any notice, and call `calendar.Configure` **always**, with the production decision too. So an unsafe deployment refuses before it touches anything. | Low. The ordering matters: this must precede the crons that can call the provider, which are focus (`:51-53`), personal blocker (`:58-79`), auto-decline (`:84-97`) and manager (`:113-132`). |
| `backend/go.mod`, `backend/go.sum` | `golang.org/x/net` moves from indirect (`go.mod:69`) to direct, for `dnsmessage` in tests. Same version, `v0.58.0`. | None. No new module. |
| `.env.example` | Both keys under a new section, both empty. A comment says plainly that a non-empty `GOOGLE_CALENDAR_API_BASE_URL` makes the server refuse to start unless `PACEDAY_ALLOW_STANDINS=true`, and that no production deployment sets either. | None. |
| `e2e/docker-compose.e2e.yml` | Both set on `backend` (§4.3). | None to production. |

**The ten production call sites, all through `calendar.NewClient` and all
untouched** (re-verified on `main` with `grep -rn "calendar.NewClient"`):
`backend/engine/cal_iface.go:60`, `booking.go:206`, `manager.go:196`,
`analytics.go:217`, `habits.go:258`, `auto_decline.go:87`,
`team_availability.go:85`, `freebusy_service.go:133`,
`backend/api/handlers_calendar.go:144`, `handlers_events.go:31`. There are two
in-package callers: `backend/calendar/client_factory.go:28` (dead, because
`NewProvider` has no callers) and `personal_reader.go:30` (the personal-calendar
sync, AC-25). `googlecalendar.NewService` is constructed only at `client.go:17`, and
`mcp/` does not import `backend/calendar`.

**`docker-compose.yml` and `docker-compose.prod.yml` are deliberately not changed.**
`e2e/SEAM-REQUIRED.md` item A suggests adding the key to the production compose file
"so the key is visible in one place". I reject that. Spec §V2-7 forbids any
mechanism that makes redirection reachable in production, and a commented-out key in
the production compose file is one uncomment away from being set.

### 2.7 Migration

**None.** There is no schema change, no data change and no backfill. If any step
seems to need a migration, stop: §V2-6 names that as the signal that scope has grown.

### 2.8 Generated code

**Nothing moves.** `make openapi-check` must pass with no diff.

---

## 3. Frontend — `Enach/smart-calendar-flow`

**No change, and no frontend PR.** Nothing on the product's wire changes (§0). The
frontend takes part only as the thing under test. AC-18 and AC-20 assert what
appears on screen, through the production bundle the e2e stack already builds
(`docker/frontend.dockerfile`, wired at `e2e/docker-compose.e2e.yml:102-117`).

**One risk to manage.** The frontend silently substitutes demo fixtures when the
backend is unreachable (`e2e/README.md:168-171`). Every new UI scenario must call
`assertNotDemo` (`e2e/fixtures.ts:77,125`). Missing test ids are PAC-44's: document
the selector inline and file it in `e2e/TESTIDS-REQUIRED.md`, and never use a
CSS-class selector.

---

## 4. E2E

### 4.1 The stand-in: `e2e/fake-google/`, written in Go

| File | Contents |
|---|---|
| `e2e/fake-google/go.mod` *(new)* | Its own module, like `docker/fileserver/go.mod`. It imports `google.golang.org/api/calendar/v3` for types, so it has a real `go.sum`. |
| `e2e/fake-google/main.go` *(new)* | Routing under `/calendar/v3/`, the bearer check, listen. |
| `e2e/fake-google/store.go` *(new)* | In-memory state keyed by calendar id, behind a mutex. |
| `e2e/fake-google/events.go` *(new)* | The five event operations. |
| `e2e/fake-google/freebusy.go` *(new)* | The free/busy query. |
| `e2e/fake-google/calendarlist.go` *(new)* | The calendar list, for rooms. |
| `e2e/fake-google/fixture.go` *(new)* | The control plane. |
| `e2e/fake-google/sink/main.go` *(new)* | The AC-23 recording sink: a second entrypoint in the same module (§4.3). |
| `e2e/fake-google/store_test.go` *(new)* | The window filter (an event overlapping the boundary is in, one outside is out) and the free/busy projection. |
| `e2e/fake-google/Dockerfile` *(new)* | Two stages to `scratch`, building both binaries, mirroring `backend/Dockerfile`. |

**Why Go:**

- The pattern already exists (`backend/Dockerfile:1-14`, `docker/fileserver`).
- Importing `calendar/v3` means the stand-in marshals the exact structs the client
  unmarshals. Hand-written JSON would reintroduce, inside the harness, the
  wrong-shape class of defect the suite exists to catch (API-014 to API-029).
- `e2e/` has no service runtime of its own. Its `package.json` devDependencies
  (`:22-29`) are test tooling only.

The cost is that this module has a real dependency tree, and the image build needs
registry access.

**The surface it must serve**, read from source. Every path is under the
`/calendar/v3/` root.

| Method | Path | Called by |
|---|---|---|
| `GET` | `/calendars/{calendarId}/events` | `ListEvents` (`backend/calendar/events.go:11-23`); also `SuggestAttendees` (`rooms_attendees.go:46-48`) and the personal sync (`personal_reader.go:34`) |
| `POST` | `/calendars/{calendarId}/events` | `CreateEvent` (`events.go:25-27`) |
| `GET` | `/calendars/{calendarId}/events/{eventId}` | `GetEvent` (`events.go:47-49`) |
| `PUT` | `/calendars/{calendarId}/events/{eventId}` | `UpdateEvent` (`:29-31`), `DeclineEvent` (`:36-41`), `AddGoogleMeet` (`:51-60`), `ClearGoogleMeet` (`:62-67`) |
| `DELETE` | `/calendars/{calendarId}/events/{eventId}` | `DeleteEvent` (`events.go:43-45`) |
| `POST` | `/freeBusy` | `GetFreeBusy` (`backend/calendar/freebusy.go:15-41`) |
| `GET` | `/users/me/calendarList` | `ListRooms` (`rooms_attendees.go:24-43`) |

`SEAM-REQUIRED.md` §3 says `PATCH/PUT`, but the writes are **`PUT` only**: all four
go through `Events.Update` (`events.go:37,58,65`). The stand-in must:

- **honour** `timeMin`/`timeMax` (`events.go:14-15`);
- **ignore** unknown query parameters (`alt`, `prettyPrint`, `sendUpdates`,
  `conferenceDataVersion`), not reject them.

**Required behaviour:**

- State lives in memory and is reset per scenario.
- **Requests with no bearer, or a bearer outside its accepted set, get 401** with
  Google's error envelope. The accepted set comes from the control plane, and reset
  installs the seeded tokens. AC-21's second half removes the connected user's token
  from that set, so a credential-keyed rejection needs no extra user.
- Control plane under `/__fixture/`, which the real API does not have:
  `POST /__fixture/reset`; `POST /__fixture/calendars/{id}/events` to seed;
  `PUT /__fixture/tokens` to set the accepted bearers; `GET /__fixture/requests`;
  `GET /__fixture/health`.
- **The request log is load-bearing (AC-22, AC-25).** Each entry carries the method,
  the path, the calendar id or the queried identifiers, and **which seeded token**
  was presented (a label, never the value). That last field is what attributes
  AC-25's arrival to the personal-calendar credential and not the work one.
- **Redirect mode (AC-11).** `POST /__fixture/redirect-next` with a target makes the
  next product-route request answer `302` to it. It is armed per request and
  disarmed after firing.

### 4.2 Seed changes

The four seeded users have no credential rows on purpose (`e2e/seed/seed.sql:54-62`),
and AC-24's scenarios depend on that (`e2e/tests/calendar.spec.ts:25,42,52`,
`focus-time.spec.ts:59`). So the connected user is a **fifth user**.

| File | Change |
|---|---|
| `e2e/seed/ids.ts` | Add `USERS.connected`: a fixed UUID in the established pattern, `e2e.connected@paceday.test` (so the `DELETE … LIKE 'e2e.%@paceday.test'` at `seed.sql:50` cleans it up). |
| `e2e/seed/seed.sql` | Insert that user. Insert one `oauth_tokens` row (access token `e2e-connected-work`, expiry far in the future). Insert one `personal_calendars` row for them with `provider = 'google'` and `credentials_json` = an `oauth2.Token` JSON (access token `e2e-connected-personal`, expiry far in the future), so AC-25 has something to sync. Amend the comment at `:54-62` so it stays true. The provider setting is already `'google'` (`:75-90`), which is AC-17's deployment-wide precondition. |
| `e2e/seed/seed.ts` | Add the user, the token row and the personal-calendar row to `verify()` (`:52`). |

**Why the expiry must be in the future.** `auth.TokenSource`
(`backend/auth/google_oauth.go:32-34`) returns a reuse source that refreshes only
when the token is invalid. With a 10-second margin it treats a far-future expiry as
valid (`golang.org/x/oauth2@v0.36.0/token.go:22,138-147`, `oauth2.go:308-320`). A
past expiry would send a refresh to `oauth2.googleapis.com`, which reaches the
sink: an AC-23 failure that looks like a broken seam.

### 4.3 Compose wiring — `e2e/docker-compose.e2e.yml`

- **Pin the network** to an explicit RFC 1918 subnet. Today the file declares no
  `networks:` (spec §V3-5). Give `fake-google` and the sink fixed addresses on it.
- **Add `fake-google`**, built from `e2e/fake-google/Dockerfile`, with a healthcheck
  on `/__fixture/health`, `restart: "no"`, and its control port published on
  `127.0.0.1` only (like postgres at `:46-47`). It gets no nginx route.
- **Add `sink`**, the same image with the sink binary. It listens on `:443` and
  `:80` and logs the peer, the TLS SNI if present, and the time, then closes. Its
  log is readable from the host through a published `127.0.0.1` port.
- **On `backend`**, set `PACEDAY_ALLOW_STANDINS: "true"` and
  `GOOGLE_CALENDAR_API_BASE_URL: http://fake-google:8080/calendar/v3/`. Add
  `fake-google: {condition: service_healthy}` and `sink` to `depends_on`.
- **Change `extra_hosts` (`:87-96`) from `127.0.0.1` to the sink's fixed address.**
  Keep every name. Inside the single-process backend container, `127.0.0.1` is the
  backend's own loopback, not a sink (spec §V3-5). Token refresh is not redirected,
  so this mapping is what keeps a refresh credential on the test host, and AC-23 is
  its evidence. Rewrite the HERMETICITY header (`:17-22`) to match.
- **Add a comment** that the stand-in's address is private, and so permitted, and
  that moving it to a routable address makes it fail closed at connect time. Per
  OQ-2, the fix for that is to move the stand-in, not to widen the set.

### 4.4 Scenarios

| Scenario | File | AC |
|---|---|---|
| *a connected calendar renders its events on the week grid* — currently `test.fixme` | `e2e/tests/calendar.spec.ts:76` | AC-16 |
| *free/busy for an attendee reflects that attendee's real calendar* — currently `test.fixme` | `e2e/tests/calendar.spec.ts:86` | AC-17 |
| *running focus time creates blocks and they appear on the calendar* — currently `test.fixme` | `e2e/tests/focus-time.spec.ts:79` | AC-18 |
| *clearing a week removes the blocks from both the database and the calendar* — currently `test.fixme` | `e2e/tests/focus-time.spec.ts:90` | AC-19 |
| *rescanning the calendar detects 1:1s and proposes team members* — currently `test.fixme` | `e2e/tests/manager-team.spec.ts:137` | AC-20 |
| *the stand-in refuses a missing or rejected credential, and the user is refused* — **new** | `e2e/tests/calendar.spec.ts` | AC-21 |
| *every redirected behaviour arrives at the stand-in* — **new** | `e2e/tests/seam.spec.ts` *(new)* | AC-22 |
| *nothing reached the sink during the run* — **new**, last in the run | `e2e/tests/seam.spec.ts` | AC-23 |
| *an on-demand personal-calendar sync reaches the stand-in* — **new** | `e2e/tests/seam.spec.ts` | AC-25 |
| *the shipped binary refuses a redirection on the week view* — **new** | `e2e/tests/seam.spec.ts` | AC-11 |
| startup behaviour, five cases — **new** | `e2e/tests/startup.spec.ts` *(new)* | AC-3, AC-12, AC-13, AC-14, AC-26 |

**Notes the implementer must not skip:**

- **AC-21 covers AC-16, AC-18, AC-19 and AC-20.** Each passes only because the
  stand-in accepted a bearer. Its second half is new: remove the connected user's
  token from the accepted set, request the week view, and assert the user is
  refused. Because there is one construction path, this exercises **production's**
  credential attachment (§V3-2). It does not cover AC-17 (§V2-2.1).
- **AC-22 iterates §V2-3's list, minus auto-decline** (AG-5: no on-demand trigger;
  it runs only from `main.go:84-97`). For each member, assert a matching, attributed
  arrival in the stand-in's log, regardless of what the product returned.
- **AC-25** triggers `POST /api/personal-calendars/{id}/sync` (`backend/api/routes.go:112`,
  handled at `handlers_personal.go:180-205`, which calls `blocker.Sync` at `:191`).
  It asserts:
  - an arrival labelled with the personal token (`personal_reader.go:30-34`);
  - the work-calendar arrivals that `syncOne` makes through `newCalOps`
    (`backend/engine/personal_blocker.go:87-95`);
  - and, from the AC-23 sink, nothing.
- **AC-11 uses the week view** (`GET /api/calendar/events`,
  `handlers_calendar.go:144`), whose failures reach the user. Arm `redirect-next`
  with a target on `www.googleapis.com`. That name maps to the sink, which is a
  *permitted* private address, so the guard would let a second hop through, and
  only the redirect policy can stop it. That is the point of using it. Assert that
  the screen shows a failure and the sink records nothing.
- **AC-17**: seed the stand-in **before** the first free/busy request, and use an
  attendee and date no other scenario uses. A failure is cached as `unknown` for 15
  minutes (`backend/engine/freebusy_service.go:96,139-150`; spec §V3-5). The
  precondition is deployment-wide (`:75`, `backend/storage/settings.go:227`).
- **AC-23 runs last**, over the whole run's sink log.

New helper: `e2e/fake.ts` *(new)*, a typed client for the control plane and the sink
log (`reset`, `seedEvents`, `setAcceptedTokens`, `redirectNext`, `requests`,
`sinkConnections`).

**Out of this issue:** `e2e/tests/auth-session.spec.ts:115` stays `test.fixme` (the
sign-in seam, §V2-7). `scheduling-links.spec.ts:144` is no longer disabled on
`main` (spec §V3-1).

**Expect AC-18 and AC-19 to fail on first run against API-014 and API-015, and
possibly API-017** (`docs/factory/api-audit.md:248-251`). Assert what the product
*should* return. A scenario that cannot pass yet is re-disabled with a comment
naming the finding and the issue that will re-enable it (README §3 rule 4). It is
never loosened.

**Caches.** One cache is in scope, the free/busy cache above. AG-1 keeps its own
behaviour untested. The subscription-feed cache never caches
(`backend/calendar/webcal_client.go:53`, `personal_reader.go:16-17`).
`FreeBusyService.Clock` is an exported field (`freebusy_service.go:50`) that
`main.go` never sets. That is out of scope (§V2-7).

---

## 5. Sequencing

1. **DONE — 2026-10-11. OQ-1 settled by reading the pinned source** (commit
   `76f93ac`, corrected after the v2 challenge): (a) yes, (b) yes on both stacks,
   (c) the new auth library, and `WithHTTPClient` displaces both credential and
   redirect handling. Spec section "OQ-1 settled by reading the pinned source".
2. **DONE — 2026-10-11. Re-challenge.** The v2 challenge returned accept-with-changes
   (two blocking). Revision v3 answered them. The v3 challenge returned
   accept-with-changes (one blocking). §V3-7 answered it. The §V3-7 challenge returned
   **accept** (commit `4465b35`). Merge point passed: the spec was accepted before
   any code was written.
3. `contract-author` records the one-line no-boundary-change note in
   `contracts/features/PAC-43.md` (§0). It does not exist yet.
4. **Backend, tests first, in this order:**
   - write the §2.1 guard and redirect tests, with the DNS double;
   - write the §2.2 shared-path tests, including **AC-27 (external package)** and
     **AC-28 (re-exec child)**;
   - write the §2.3 configuration tests;
   - watch every one of them fail;
   - then write `guard.go`, `transport.go`, the `client.go` change, `config/endpoints.go`,
     the `main.go` wiring and `.env.example`.

   The guard's table comes first because that is what keeps the implementation from
   drifting back to a string check.
5. `make verify` on the backend, plus coverage at or above the floor (`CLAUDE.md`).
   **Merge point: the backend PR merges on its own.** It changes nothing a user can
   see. It **does** change production's outbound client (E1–E4), and the PR body
   must say so in those words. That change is held by AC-1, AC-27 and AC-28 in this
   PR, and by AC-3 once the e2e PR lands.
   - **Between the two merges**, AC-3, AC-12, AC-13, AC-14 and AC-26's e2e half are
     unproven. The window should be short. The production-facing risk in it is
     carried by AC-27 and AC-28.
6. **The stand-in and the sink**: `e2e/fake-google/`, with the request log, the
   accepted-token set, the redirect mode, two tests, and the sink entrypoint. Run
   them in isolation against `curl` before anything is wired to them.
7. **Compose and seed**: the pinned network, both services, `extra_hosts` remapped
   to the sink, the two backend variables, and the fifth user with the token and
   personal-calendar rows. Verify by starting the stack and checking for the AC-14
   notice.
8. **Scenarios, one at a time:**
   - the §2.4 startup cases;
   - then AC-11;
   - then AC-22, AC-25 and AC-23;
   - then AC-16, AC-17, AC-21, AC-18, AC-19 and AC-20.

   This order means the first failures are the seam's, and later ones are the
   product's.
9. Update `e2e/README.md`:
   - §3 (`:61`) and §5 (`:111`) describe no seam;
   - §9 (`:244`) needs the two variables;
   - §10 (`:258-270`) says the `make e2e` target does not exist, but it does
     (`Makefile:167-179`).

   Update `e2e/SEAM-REQUIRED.md`: item A is done (with the PUT-only correction to
   §3), and items B and C remain.
10. **Merge point: the e2e PR merges second**, either with `make e2e` green or with
    each still-disabled scenario naming its issue.
11. **Stage 7, before the first release that contains step 5** (§V3-7 item 3): the
    release manager inspects the **running** production backend container's
    environment for `HTTP_PROXY`/`HTTPS_PROXY`/`NO_PROXY`, in either case. A
    daemon-level or client-level proxy can inject these without any repository file
    showing it. If a proxy is present and is the host's only route to Google, hold
    the release and return the proxy requirement to spec (§V3-3).

There is no frontend PR, so no cross-repo ordering applies.

---

## 6. Rollback

**There is no migration, so nothing is lost either way.** The down path is a code
revert. **That revert changes production too** (§V3-7 item 2). It restores the
library's default credential stack, its redirect-following, its honouring of
ambient proxy variables, and the effect of the legacy-stack switch: E1–E4 in
reverse. It is a code deployment, not a configuration change.

```
# 1. Revert the backend change (restores production's library-default client).
git revert <backend commit>

# 2. Revert the e2e change: restores the test.fixme markers on AC-16..AC-20's
#    scenarios, removes fake-google and the sink, restores extra_hosts to
#    127.0.0.1, and removes the fifth seeded user.
git revert <e2e commit>

# 3. Tear the stack down so no orphaned container survives.
docker compose -p paceday-e2e -f e2e/docker-compose.e2e.yml down -v --remove-orphans
```

Two companion steps are covered by step 2 and are not left to whoever reverts:

- re-disable the scenarios for AC-16 to AC-20, with a comment naming this issue;
- put `e2e/README.md` §5 back.

**A partial rollback of the test stack only** remains available. Remove the two
variables from `e2e/docker-compose.e2e.yml` and re-disable the scenarios. The
backend then runs with the production decision. **This is not a rollback of
production.** Production does not set the variables, so removing them changes
nothing there, and only a code revert undoes E1–E4. Plan v2 said this would make
the backend "byte-for-byte what it is today". That is no longer true, and the plan
no longer claims it.

---

## 7. What I could not determine

**Closed since plan v2:**

1. ~~**How the pinned provider client behaves (OQ-1 a/b/c).**~~ **Settled
   2026-10-11** by reading the module source. See the spec section "OQ-1 settled",
   and §1.3, which cites each line it rests on. Plan v1's trust-store contingency
   was false (`backend/Dockerfile:10`). Under the shared path it is also moot: the
   bearer is attached over plain HTTP (`x/oauth2@v0.36.0/transport.go:45-55`).
2. ~~**Replace or join, and is the trailing slash required.**~~ **Settled:** the
   endpoint replaces the base path (`calendar-gen.go:200-202`), and the trailing
   `/calendar/v3/` is required (`googleapi.go:377-400`). Now enforced by AC-26 and
   tested in §2.3.
3. ~~**Whether the seeded credential survives without a refresh.**~~ **Settled by
   reading:** the reuse token source refreshes only an invalid token, with a
   10-second margin (`x/oauth2@v0.36.0/token.go:22,138-147`, `oauth2.go:308-320`).
   A far-future expiry never refreshes (§4.2).

**Still open:**

4. **Whether the DNS double can make `localhost` resolve to a public address.** This
   is AC-6's second half for the `localhost` row. Go's resolver consults the hosts
   file before DNS (`hostLookupFilesDNS`), and on most machines that maps
   `localhost` to loopback. *Cheapest settlement*: the first run of
   `TestGuard_NameRowsNeverTrusted`. If `localhost` cannot be forced public, assert
   that row as "resolves to loopback and is permitted", with a comment, and record it
   in the PR. That is not a weakening: the guard still judges the answer actually
   used.
5. **The exact query parameters the library adds.** Already neutralised by "ignore
   unknown parameters". *Settlement*: the stand-in's log on the first AC-16 run.
6. **Whether the operator's stacks all fit the permitted set** (OQ-2). Spec §V3-5
   records it as an assumption: the e2e compose stack is the only topology, and this
   plan pins its subnet (§4.3). *Settlement*: ask whoever runs other stacks before
   the backend PR opens. If one needs a routable address, the answer is to move the
   stand-in, never to widen or configure the set (§V2-7).
7. **Whether §2.4's process tests belong in Playwright** or in a shell target that
   `make e2e` runs. Either is acceptable if the gate runs it. *Settlement*: the first
   attempt at AC-12.
8. **How much of the newly enabled surface passes.** I expect AC-18 and AC-19 to
   hit API-014 and API-015, and AC-16's UI half to need PAC-44 selectors.
   *Settlement*: §5 step 8, one scenario at a time.
9. **Whether production reaches Google through a host-injected proxy** (E3).
   Nothing in the repository sets one (`docker-compose.prod.yml:33` onward), but a
   daemon setting would not show there. *Settlement*: the stage-7 check in §5 step 11.
   This plan cannot settle it, and neither can stage 4.
