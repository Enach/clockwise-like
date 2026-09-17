# Spec — PAC-43: calendar-dependent behaviour must be verifiable before release

- **Linear**: https://linear.app/paceday/issue/PAC-43/add-a-base-url-seam-to-backendcalendar-so-external-calendar-providers
- **Status**: **v2 — revised after challenge.** v1 was `draft`; `spec-challenger`
  returned *accept-with-changes* with four blocking findings on 2026-09-17.
- **Author**: spec-author

> ## ⚠ READ THIS FIRST — document layout
>
> This file contains three things, in this order:
>
> 1. **§0–§8 — the v1 spec.** Retained for the record. **§3, §4, §6 and §8 are
>    superseded** and are marked as such at their headings. §0, §1, §2, §5 and §7
>    survive v1 unchanged except where §2 is *extended* by v2 §V2-2.
> 2. **`## Challenge — 2026-09-17` — the challenger's review. It applies to v1.**
> 3. **`# Revision v2` at the end of the file — the binding text.** Where v2
>    disagrees with v1, v2 wins. The acceptance criteria that stages 2–5 must build
>    against are **v2 AC-1 through AC-25**, not v1 AC-1 through AC-14. A mapping
>    table from v1 numbers to v2 numbers is at v2 §V2-5.
>
> The one-line summary of what changed: **v1 tried to make redirection safe by
> validating a host string once at startup, and the challenger showed five ways
> that fails open. v2 stops validating names altogether and instead refuses to
> open a connection whose peer address is outside a fixed set of
> non-internet-routable ranges, judged per connection immediately before the
> connection is made, on every hop — and refuses to follow redirects at all.**
- **Inherited scope**:
  - **Resolved by this spec**: none. This work changes no HTTP operation, so it
    inherits no `x-uncertain` marker and closes no audit finding by itself.
  - **Made resolvable by this spec, by observation rather than by change**:
    U-14, U-15, U-16, U-19, U-23 (all five say "observe a response" and none can
    be observed today above the unit level on a calendar path), and the
    *verification* half of API-014, API-015, API-016, API-017, API-028, API-029,
    API-041, API-044. See §1.3 — this is the argument for doing the work.
  - **Split out of this spec, filed as PAC-47**: the dead work-calendar provider
    switch (§0.2). It is a user-visible product bug, it is not a testability
    problem, and bundling it would make this change unrevertable. PAC-47 carries
    `x-uncertain` U-18.

> **A note on this spec's own evidence.** The environment this spec was written
> in blocks `proxy.golang.org` and the npm registry, so nothing here was
> established by compiling, running or testing anything. Every claim in §2 comes
> from reading source and carries a `file:line`. Where I am inferring rather than
> observing — most importantly about how the vendored Google client behaves when
> its endpoint is overridden — I say so, and it is an open question in §8 rather
> than a claim.

---

## 0. Scope: this issue carries two problems, and only one of them is this spec

The Linear issue asks for a seam and, under "adjacent finding, arguably worse",
reports that the work-calendar provider setting is ignored. I verified both. They
are real, and they are not the same problem.

### 0.1 The seam

Nothing that reads or writes a work calendar can be exercised by any test that
runs outside the Go process, because the only way to reach an external calendar
is through a constructor that hardcodes the provider's address (§2.1). This is a
*process* problem: it costs the team the ability to find defects, and it costs
users because the defects it hides are shipping (§1.3). It changes no behaviour
anybody can see.

### 0.2 The dead provider switch

The settings UI offers three work-calendar providers. The backend reads that
choice in exactly two places, neither of which fetches calendar data, and the
function that *would* honour it has no callers anywhere (§2.4). A user who
selects WebCal is told they are connected and syncing, and then every calendar
screen tells them they are not connected. That is a *product* problem with a
named victim, and it deserves its own issue rather than a paragraph in this one.

**They should not be one spec.** Three reasons:

1. **They have different shapes.** The seam adds one piece of deployment
   configuration whose only correct production value is "unset". Honouring the
   provider setting changes what ten call sites do for every existing user, and
   requires a product decision that has no owner today: what a scheduling feature
   that must *write* an event is supposed to do when the chosen provider is
   read-only (§2.5).
2. **Fixing the provider switch does not remove the need for the seam.** The one
   provider that would be injectable through configuration — the subscription
   feed — cannot create events, so focus time, compression, habits and personal
   blocking stay untestable through it. `e2e/SEAM-REQUIRED.md` §2 item C makes
   the same point and I confirmed it: the read-only refusal is at
   `backend/calendar/webcal_client.go:47-49`.
3. **Bundling them makes the revert dangerous.** The seam is revertable by
   deleting configuration. Honouring the provider setting is not: it changes the
   live behaviour of every calendar path, and a revert after users have switched
   provider leaves them silently back on the wrong calendar.

| Spec | Covers |
|---|---|
| **PAC-43** (this one) | The seam, for the primary work-calendar provider only. No user-visible change. |
| **PAC-47** — *the work-calendar provider setting is ignored* | The provider choice is honoured, or the UI stops offering choices the server cannot serve. Carries U-18. |

PAC-47 also picks up `x-uncertain` U-18 (`docs/factory/api-audit.md:462`), which
asks for the authoritative list of provider values to be decided and enforced — a
question that only has an answer once somebody decides which of them the server
actually supports.

---

## 1. Problem

### 1.1 What cannot be checked

Roughly half of what Paceday does for a user happens against their work calendar:
seeing their week, finding focus time and defending it, compressing a fragmented
day, scheduling a habit, declining meetings outside working hours, showing a
team's availability, detecting who reports to whom, and answering whether an
attendee is free. None of it can be exercised by an automated test that runs the
real server, because the only way to reach a calendar is hardcoded to the real
provider and no test may use a real account.

The result is visible in the test suite as an admission. Five scenarios in
`e2e/tests/` are written out, named, and then disabled with a comment saying they
cannot run (§2.7). They are not gaps somebody forgot; they are gaps somebody
documented and could not close.

### 1.2 Who this costs, and how

**The team**, immediately: every calendar change is released on the strength of
unit tests over doubles plus somebody clicking through it. There is no gate.
`docs/factory/README.md` §2 stage 5 requires an e2e scenario covering the spec's
acceptance criteria before a feature is admitted; for any calendar feature that
gate cannot be met, so either the gate is waived or the feature does not ship.

**Users**, already, and this is the part that turns a testing concern into a
product one.

### 1.3 The defects this has already allowed through

The API audit found the calendar and focus surfaces to be the worst in the
product, and the defects it found are exactly the class an end-to-end test
catches on the first run:

- The focus-time list, the focus-run summary and the compression preview each
  return a shape the frontend cannot read, so those screens render blank or
  `undefined` (API-014, API-015, API-016 — `docs/factory/api-audit.md:248-250`).
- Three mutation endpoints return a raw provider object instead of the documented
  one, so the title, start and end of an event the user just edited come back
  undefined (API-017, `api-audit.md:251`).
- One response carries two different key casings for the same data (API-028,
  `api-audit.md:262`).
- A read-modify-write through the UI silently removes a room booking from an
  event (API-044, `api-audit.md:278`).

Every one of these is a defect a user meets on a screen. Every one of them would
fail an end-to-end assertion in the first minute. None of them could have one.
Five more open uncertainties in the register (U-14, U-15, U-16, U-19, U-23) are
marked *"observe a response"* — the register's own cheapest class of fix — and
cannot be observed at all on a calendar path today.

So the honest statement of the problem is not "we would like better tests". It is
**the part of the product with the most confirmed user-facing defects is the only
part with no way to detect them**, and the reason is one constructor.

### 1.4 Why the answer has to be a change to production code

A test double can be substituted inside the Go process — the engine packages
already do it (§2.6). That proves the algorithms. It cannot prove the thing that
is actually broken: what crosses the HTTP boundary, in the running server, in the
shape the browser receives. Every defect in §1.3 lives in exactly the gap between
those two, which is why in-process doubles did not catch any of them.

Proving that gap requires running the real binary and giving it somewhere other
than the real provider to talk to. The binary has no such setting. Adding one is
the smallest thing that can be true.

---

## 2. Current behaviour

All citations are from reading. Nothing was executed.

### 2.1 One constructor, one hardcoded address

`backend/calendar/client.go:16-22`:

```go
func NewClient(ctx context.Context, tokenSource oauth2.TokenSource) (*CalendarClient, error) {
	svc, err := googlecalendar.NewService(ctx, option.WithTokenSource(tokenSource))
	...
}
```

The only client option passed is the credential. The vendored client's compiled-in
base path is used. *I am inferring, not observing, that the vendored Google client
honours no endpoint environment variable and that `option.WithEndpoint` is the only
override* — this matches `e2e/SEAM-REQUIRED.md` §1.1 and the library's documented
behaviour, but I could not fetch the module to confirm it (see the evidence note,
and OQ-1).

### 2.2 Every calendar call site goes through it

`grep -rn "calendar.NewClient" --include=*.go .` gives twelve hits: ten outside the
package, two inside it.

| File:line | Reached by |
|---|---|
| `backend/engine/cal_iface.go:60` | `newCalOps` → focus time, compression, smart schedule, NLP confirm |
| `backend/engine/booking.go:206` | collective slots, booking confirmation |
| `backend/engine/manager.go:196` | team detection |
| `backend/engine/analytics.go:217` | weekly analytics recompute |
| `backend/engine/habits.go:258` | habit scheduling |
| `backend/engine/auto_decline.go:87` | auto-decline cron |
| `backend/engine/team_availability.go:85` | team availability |
| `backend/engine/freebusy_service.go:133` | free/busy, Google branch |
| `backend/api/handlers_calendar.go:144` | calendar events, raw free/busy |
| `backend/api/handlers_events.go:31` | event patch and delete |
| `backend/calendar/client_factory.go:28` | *(in-package; see §2.4 — unreachable)* |
| `backend/calendar/personal_reader.go:30` | *(in-package; personal calendars, a different feature)* |

This is the useful half of the finding. There is one place to change, and it is
reached by everything.

### 2.3 No configuration reaches it

`backend/main.go` reads seven environment variables: `PORT` (:28),
`DATABASE_URL` (:33), the three Google OAuth values (:45-47), `JWT_SECRET` (:134),
`ALLOWED_ORIGIN` (:138) and `FRONTEND_URL` (:139). None concerns a calendar
endpoint. The engines that call the constructor receive a `*sql.DB` and an
`*oauth2.Config` and no configuration object, so there is nothing to thread a value
through even if one existed.

### 2.4 The interface that looks like a seam has no callers

`backend/calendar/provider.go:21-25` declares `Provider`, and
`backend/calendar/client_factory.go:14-34` declares `NewProvider`, which switches
on the stored provider name over `"outlook"`, `"webcal"` and a `"google"` default.
The `"webcal"` branch (`client_factory.go:22-26`) takes a URL straight out of
settings, which would be a ready-made injection point.

**`NewProvider` has no callers.** `grep -rn "NewProvider" --include=*.go .` across
the whole repository — backend and MCP server — returns five hits: its own
definition and doc comment (`client_factory.go:13-14`), an unrelated OIDC
provider constructor (`backend/auth/oidc.go:41`), and a differently-named
conferencing factory with two live callers
(`backend/conference/factory.go:11`, called at
`backend/api/handlers_conferencing.go:38` and `:145`). Nothing calls the calendar
one. The `Provider` interface itself is referenced only inside its own package.

**One correction to the issue's phrasing, which makes the bug worse rather than
better.** The issue says `settings.calendar_provider` is "entirely ignored by the
running server". That is not quite right, and the exception is the problem:

- `backend/api/handlers_auth.go:91-125` branches on it. For `"webcal"` it reports
  `connected: true` whenever a feed URL is stored (`:105-110`) — without fetching
  anything.
- `backend/engine/freebusy_service.go:119-138` branches on it, but only for
  `"outlook"`. `"webcal"` is not a case, so it falls into `default:` (`:128`) and
  free/busy is answered from **Google**.

Nothing else reads it. So the stored choice is honoured only where it cannot
fail, and every path that actually fetches calendar data ignores it.

The setting is also stored on the global singleton settings row —
`storage.GetSettings` is pinned to `WHERE id = 1`
(`backend/storage/settings.go:227`), which is API-002
(`docs/factory/api-audit.md:236`) — so in a multi-user deployment one user's
choice is everybody's.

The contract has already recorded a belief about this that turns out to be false:
`contracts/openapi/paths/scheduling.yaml:1380-1386` describes `calendarProvider`
and `docs/factory/api-audit.md:462` (U-18) says an unknown value "falls through
the provider switch". There is no live provider switch to fall through.

### 2.5 What the user is offered, and what they get

`smart-calendar-flow/src/components/WorkCalendarConnection.tsx:14-18` declares
three providers — Google Calendar, Microsoft Outlook, and "WebCal / iCal URL",
labelled "Read-only feed". The picker is filtered by
`GET /api/integrations/availability`, which reports WebCal as
`{available: true, reason: "built_in"}` unconditionally
(`backend/api/integration_availability.go:26`), so WebCal is always offered.
Selecting it writes the provider name and the feed URL into settings
(`WorkCalendarConnection.tsx:69-78`).

The user then sees, from `GET /api/auth/status`, "WebCal / iCal URL — connected"
and "Syncing in real time" (`WorkCalendarConnection.tsx:117-124`,
`backend/api/handlers_auth.go:105-110`). And the calendar itself is empty with an
error, because the events handler ignores the setting entirely and asks for a
Google credential that a WebCal user has no reason to have
(`backend/api/handlers_calendar.go:139-145`).

This is the separate issue in §0.2. It is stated here because §2 is where evidence
belongs, and because a reader needs to see that it is a different problem before
accepting that it is being filed elsewhere.

*(The one genuinely live subscription-feed path is
`backend/calendar/personal_reader.go:13-46`, used by the personal-calendar
blocker at `backend/engine/personal_blocker.go:72,87`. That is the "personal
calendars" feature, not the work-calendar provider, and it works.)*

### 2.6 The in-process seam that exists, and why it is not enough

`backend/engine/cal_iface.go:15-23` defines an unexported `calendarOps`
interface, and four engines accept a double through an unexported field:
`FocusTimeEngine` (`focus_time.go:45,48-52`), `CompressionEngine`
(`compression.go:35,39-42`), `SmartScheduler` (`smart_schedule.go:42,46-49`),
and the analytics and habits engines via their own helpers
(`analytics.go:211`, `habits.go:252`). Six unit-test files use it
(`focus_time_test.go:310,341`, `compression_test.go:215-338`,
`smart_schedule_test.go:139,196`).

The fields are unexported and `backend/main.go` never assigns one, so the double
is reachable only from inside the package. This is the seam that proves the
algorithms and cannot prove the boundary (§1.4).

### 2.7 What the test suite says about itself

`e2e/tests/` contains seven disabled scenarios. Five are disabled by this problem,
each naming the constructor in its comment:

| Spec file | Disabled scenario |
|---|---|
| `tests/calendar.spec.ts:73` | *a connected calendar renders its events on the week grid* |
| `tests/calendar.spec.ts:83` | *free/busy for an attendee reflects that attendee's real calendar* |
| `tests/focus-time.spec.ts:75` | *running focus time creates blocks and they appear on the calendar* |
| `tests/focus-time.spec.ts:86` | *clearing a week removes the blocks from both the database and the calendar* |
| `tests/manager-team.spec.ts:137` | *rescanning the calendar detects 1:1s and proposes team members* |

The other two are blocked on different things and are **not** in this spec's scope:
`tests/auth-session.spec.ts:115` (*the real Google OAuth callback issues a
session*) needs the sign-in endpoints redirected, which is §5; and
`tests/scheduling-links.spec.ts:144` (*a link created through the Links page
appears on the public booking page*) needs frontend test ids and is PAC-44.

The suite currently keeps itself honest by mapping every provider hostname to
`127.0.0.1` inside the backend container
(`e2e/docker-compose.e2e.yml:87-96`) so an escaping call fails instantly rather
than reaching the internet, and by seeding users with no stored credential so
every calendar path takes its "not connected" branch
(`e2e/README.md:81-84`, `e2e/SEAM-REQUIRED.md` §4).

### 2.8 The clock cannot be driven either, and it does not block this

`backend/engine/clock.go` defines a `Clock` interface (`:8-10`), a real
implementation (`:13-15`), a package-level `var SystemClock Clock = realClock{}`
(`:19`) and a `FixedClock` double (`:22-24`). Six engines fall back to
`SystemClock` when their optional `Clock` field is nil — habits (`habits.go:53-56`),
free/busy (`freebusy_service.go:57-60`), personal blocker
(`personal_blocker.go:29-32`), manager (`manager.go:29-32`), daily recap
(`daily_recap.go:26-29`).

**`backend/main.go` never assigns `SystemClock` and never sets a `Clock` field on
any engine it constructs** (§2.3 lists every variable it reads; none is a time).
So the server's notion of "now" cannot be influenced from outside the process.
I confirm the report in `e2e/README.md:129-139`.

Two consequences matter for this spec and are handled rather than fixed:

- None of the five scenarios in §2.7 needs a fixed clock. Each chooses its own
  week or day and the suite's fixtures are relative (`e2e/lib/dates.ts`).
- Two in-memory caches with a fifteen-minute lifetime sit in the paths those
  scenarios exercise — the free/busy result cache
  (`backend/engine/freebusy_service.go:140,157-166`) and the subscription-feed
  fetch cache (`backend/calendar/webcal_client.go:26,52-57`). With no way to
  advance the clock, a test cannot expire them; it can only avoid them by using a
  distinct cache key. This is a constraint on how the scenarios are written, not
  a reason to change the clock, and it is OQ-5.

Making the clock drivable is the same shape of change as this one and is
explicitly **not** in this spec (§5).

### 2.9 What protects the current behaviour today

Almost nothing, and this is itself a finding.

`backend/calendar/calendar_test.go` is 37 lines and contains two tests. One asserts
that a struct holds the two times assigned to it (`:10-21`). The other constructs
a client with a fake credential and asserts the returned value is non-nil and that
its calendar id is `"primary"` (`:23-36`). Neither makes a request. There is no
test anywhere that asserts where a calendar request is sent.

So the guarantee that this change does not alter production behaviour cannot come
from an existing test. It has to be created by this work, which is AC-1.

---

## 3. Desired behaviour — **SUPERSEDED by v2 §V2-3**

> *v1 text retained for the record. Its claim that the permitted addresses are
> "structurally incapable of being somewhere real" was false as specified
> (challenge, blocking 1) and its claim that "there is no path that keeps talking
> to the real provider" was false as written (blocking 4). Read v2 §V2-3 instead.*

**Nothing a user can see changes.** Not the events on their week, not the focus
blocks, not the errors they get when they have not connected a calendar, not the
address their calendar data travels to. A deployment that is not being tested
behaves identically before and after, byte for byte on the wire, and there is a
test that says so rather than a reviewer's assurance.

**A test deployment can be told to direct calendar traffic at a stand-in.** An
operator running the product against a stand-in states two separate things: that
this deployment is permitted to use stand-ins for external services at all, and
where the calendar stand-in is. Neither implies the other. Stating only the first
changes nothing; stating only the second is refused.

**The unsafe combinations are refused loudly, at startup, before any request is
served.** A deployment that names a stand-in without having declared itself a test
deployment does not start. A deployment that names an address outside the narrow
set a stand-in can plausibly live at does not start. In both cases the refusal
names the offending value. The server never silently ignores a redirection it was
asked for, because a test run that believes it is isolated and is not is worse
than a test run that fails.

**The addresses a stand-in may live at are structurally incapable of being
somewhere real.** Redirection carries the user's calendar credential to wherever
it points, so the permitted set is not "anything the operator types". It is
confined to addresses that only exist inside a machine or inside a private
container network, and any address that could belong to a name on the public
internet is refused. This is a property of the value, checked mechanically, not a
warning in a comment.

**A deployment that has been redirected says so, unmistakably, in its startup
record**, in terms that make it obvious to anyone reading the logs that this is
not a production deployment.

**Every calendar-dependent behaviour is redirected together.** There is no path
that keeps talking to the real provider after redirection — partial redirection
would produce a test run that passes while a feature is broken, which is the one
outcome the suite exists to prevent.

**The stand-in is talked to exactly as the real service is.** It receives the same
requests, carrying the same credential, and its answers are interpreted by the
same code. In particular the credential is still sent and still required, so a
test cannot pass while the credential path is broken.

**What becomes true as a consequence**: the five scenarios named in §2.7 can be
written as real assertions against the running stack, and the calendar and focus
surfaces become subject to the same release gate as everything else.

---

## 4. Acceptance criteria — **SUPERSEDED by v2 §V2-4**

> *v1 text retained for the record. v1 AC-3 to AC-6 were labelled `[unit]` and
> asserted something a unit test cannot falsify (blocking 3); v1 AC-13 could not
> fail (blocking 4); v1 AC-4 constrained only the first hop (blocking 2). Build
> against v2 AC-1 to AC-25. Mapping table at v2 §V2-5.*

Each is falsifiable today. AC-1 through AC-6 fail because the configuration they
describe does not exist; AC-7 through AC-12 are the five disabled scenarios of
§2.7 and fail because they cannot be written. AC-13 is the one criterion an
existing test already protects, and it is here to be kept green.

**No change without configuration**

AC-1. Given a deployment with no calendar redirection configured and no test-mode
declaration, when the server handles any calendar-dependent request, then the
outbound request is addressed exactly as it is today — same address, same
credential, same body — and nothing about the request differs from the current
build.  `[unit]`

AC-2. Given a deployment that declares test mode but configures no redirection,
when the server starts, then it starts normally and AC-1 still holds — declaring
test mode grants nothing by itself.  `[unit]`

**Unsafe configuration is refused at startup**

AC-3. Given a deployment that configures a calendar redirection but does not
declare test mode, when the server is started, then it does not begin serving
requests, and its output names the setting that was refused and why.  `[unit]`

AC-4. Given a deployment that declares test mode and configures a redirection to
an address that is not confined to the local machine or a private container
network, when the server is started, then it does not begin serving requests, and
its output names the rejected address.  `[unit]`

AC-5. Given a deployment that declares test mode and configures a syntactically
malformed redirection, when the server is started, then it does not begin serving
requests, and its output names the rejected value. A redirection is never
partially applied and never silently discarded.  `[unit]`

AC-6. Given a deployment that declares test mode and configures a valid
redirection, when the server starts, then its startup output states that calendar
traffic is redirected, names where to, and states that the deployment is not a
production deployment.  `[unit]`

**What becomes provable**

AC-7. Given a redirected deployment whose stand-in holds a known set of events in
a known window, and a user with a stored calendar credential, when that user's
calendar is requested for that window, then the response describes exactly those
events, the stand-in records having been asked, and no connection is attempted to
the real provider.  `[e2e]`
*(the disabled scenario at `tests/calendar.spec.ts:73`)*

AC-8. Given the same, when free/busy is requested for an attendee the stand-in
holds busy windows for, then the reported busy windows are exactly those, and the
attendee's coverage is reported as known rather than unknown — the branch that
today can only be reached by a real account.  `[e2e]`
*(`tests/calendar.spec.ts:83`)*

AC-9. Given a redirected deployment, a user with a stored credential, and a week
in which the stand-in holds two meetings, when focus time is run for that week,
then focus blocks exist in the product's own store, matching events exist on the
stand-in, and the user sees those blocks on their week.  `[e2e]`
*(`tests/focus-time.spec.ts:75`)*

AC-10. Given the state AC-9 leaves behind, when that week is cleared, then the
blocks are gone from the product's store **and** the corresponding events are gone
from the stand-in — neither alone is sufficient.  `[e2e]`
*(`tests/focus-time.spec.ts:86`)*

AC-11. Given a redirected deployment whose stand-in holds recurring two-person
meetings between a manager and two colleagues, when the manager rescans for team
members, then those two colleagues are proposed and nobody else is.  `[e2e]`
*(`tests/manager-team.spec.ts:137`)*

AC-12. Given a redirected deployment whose stand-in refuses any request that
carries no credential, when any of AC-7 through AC-11 runs, then it succeeds —
proving the credential is still being sent. And given a user whose stored
credential the stand-in rejects, when their calendar is requested, then they are
refused rather than served.  `[e2e]`

**No escape hatches**

AC-13. Given a redirected deployment in which every real provider address is
unreachable, when each calendar-dependent feature is exercised in turn, then none
of them fails with a connection error — every path went to the stand-in.  `[e2e]`

**Preserved**

AC-14. Given a user with no stored calendar credential, when they request their
calendar, then they are refused exactly as they are today, and the interface says
so rather than inventing a week.  `[e2e]`
*(already proven by `tests/calendar.spec.ts:25,42,52` — these must stay green)*

---

## 5. Explicitly out of scope

- **Redirecting sign-in and identity.** The sixth disabled scenario
  (`tests/auth-session.spec.ts:115`) needs the sign-in exchange and the profile
  lookup redirected, which is `e2e/SEAM-REQUIRED.md` item B. It is excluded
  deliberately and for a reason beyond scope discipline: it is the authentication
  path. A mistake in a calendar redirect makes a test fail; a mistake in an
  authentication redirect is a login bypass. It also buys one scenario, and the
  suite already exercises everything downstream of sign-in legitimately, because a
  validly signed session token *is* the session with no server-side store
  (`e2e/README.md:92-107`). Separate issue, same shape, argued on its own merits.
- **Redirecting the other calendar provider's API.** No disabled scenario names
  it and no journey exists that would use it. The moment the first such journey is
  written, it should be done in exactly this shape.
- **Making the provider choice work (§0.2).** Filed as PAC-47. Carries U-18.
- **Making the clock drivable (§2.8).** Confirmed impossible today, confirmed not
  to block any of AC-7 to AC-11, and a change with a much wider blast radius: the
  affected engines fall back to a package-level default, so a redirected clock
  changes the behaviour of every one of them at once. When a scenario needs it —
  the recap send time and the two fifteen-minute caches are the obvious
  candidates — it gets its own spec.
- **Building the stand-in itself and enabling the scenarios.** They are the point,
  but they are test assets, not product behaviour, and they belong to stages 3 and
  5. This spec is done when the seam exists and AC-1 to AC-6 hold.
- **Fixing the defects the new scenarios will find.** API-014 through API-044
  (§1.3) are real and will start failing loudly. Each is its own issue. This spec
  must not be held open until they are fixed, and the scenarios written for AC-7
  to AC-11 must assert what the product *should* do — if that means they fail on
  day one, that is the finding, and per `docs/factory/README.md` §3 rule 4 a
  scenario disabled for that reason names the issue that will re-enable it.
- **Any mechanism that makes redirection possible in a production build.** See
  §6 — this is a prohibition, not a deferral.

---

## 6. What must not change — **SUPERSEDED by v2 §V2-6**

> *v1 text retained for the record. The final two bullets overstated the guarantee
> AC-4 delivered. Read v2 §V2-6 instead.*

- **Where a production deployment sends calendar traffic.** Protected by no test
  today (§2.9). AC-1 is the new guard, and it is the single most important
  criterion in this spec.
- **What a user sees, anywhere.** No screen, no response body, no error text, no
  status code. If the plan proposes a response change, scope has grown and this
  spec does not cover it.
- **No stored data changes.** There is no migration. If the plan proposes one,
  that is the same signal.
- **The behaviour of a user with no connected calendar.** Every seeded user in the
  suite is in that state today and four passing scenarios depend on it
  (`tests/calendar.spec.ts:25,42,52`, `tests/focus-time.spec.ts:56`). Adding
  redirection must not turn "not connected" into "connected to a stand-in" for a
  user who has stored no credential.
- **The credential must still be required.** A redirection that made requests work
  without a credential would let the suite go green over a broken credential path.
  AC-12 is the guard.
- **Redirection must never be reachable in production, by any route.** Not by
  setting one value, not by a defaulted value, not by an operator mistake that
  degrades quietly. The failure mode being designed against is not an attacker: it
  is a copied environment file. And the consequence is worse than the usual
  server-side request forgery shape, because the request that gets redirected
  carries a live credential with full access to the user's calendar — so a
  misconfiguration does not merely make the server fetch a URL, it hands every
  user's calendar credential to whoever is listening. This is why AC-3 refuses to
  start rather than ignoring the value, and why AC-4 constrains the address by
  construction rather than by documentation.
- **The suite's existing isolation.** The provider hostnames mapped to the local
  machine (`e2e/docker-compose.e2e.yml:87-96`) must stay mapped. Redirection is
  what makes the stand-in reachable; the blackhole is what proves nothing escaped.
  Removing it because "we have a stand-in now" would delete the evidence for AC-13.

---

## 7. Rollback

**No migration. No stored data. Nothing to lose.**

Reverting is deleting the change. A production deployment is unaffected in either
direction, because it never sets either of the two values — which is the same
property that makes AC-1 the safety argument for shipping it.

Two things do not revert automatically and must be done in the same revert:

1. **The scenarios enabled under AC-7 to AC-11 must go back to disabled**, each
   with a comment naming this issue, or the suite goes red for a reason unrelated
   to whatever prompted the revert.
2. **The stand-in must be removed from the test stack**, or the stack starts a
   service nothing talks to. Harmless, but confusing later.

A revert restores the situation in §1: the calendar surface becomes unverifiable
again and the defects in §1.3 become undetectable again. That is a reason to fix
forward rather than revert, but unlike a data-losing change it is not a reason to
hesitate — nothing is destroyed.

---

## 8. Open questions — **SUPERSEDED by v2 §V2-8**

> *v1 text retained for the record. OQ-1's contingency was factually wrong, OQ-1
> was three questions rather than two, OQ-2 is answered by v2 §V2-3, and OQ-5
> rested on a cache that is not in scope. Read v2 §V2-8 instead.*

**OQ-1 — Does the vendored Google client accept an endpoint that is not served
over TLS, while a credential is attached?** *Owner: stage 4. Settle with one local
run, a few minutes.* The whole design assumes a stand-in can be reached over plain
HTTP inside a container network. If the client refuses — either because the
endpoint override rejects a non-TLS scheme, or because the credential transport
does — then the stand-in needs a certificate that the backend image trusts, which
changes the test stack's wiring but not one word of §3 or §4. This is the single
assumption this spec rests on that I could not check, because the module registry
is blocked here. It must be settled **before** stage 4 starts, not during it.

**OQ-2 — What exactly is the permitted set of addresses in AC-4?** *Owner:
contract-author, with whoever operates the stacks.* My proposal is: the local
machine, or a name with no dots in it — a container-network service name. That is
mechanical, checkable in a few lines, and structurally excludes every public host,
because every public host name has a dot. It would also reject a deployment that
wants to reach a stand-in at an internal fully-qualified name. I do not know
whether any target topology needs that. If one does, the answer is not to loosen
the rule generally; it is to decide deliberately and write down why.

**OQ-3 — Should a redirected deployment advertise that fact over the API?**
*Owner: contract-author.* It would let the suite assert the stack is in the state
it thinks it is, rather than inferring it from behaviour. Against: it is a new
response field for a test's benefit, on a surface that is already reachable
without a session (`backend/api/routes.go:52`), and AC-7 already proves
redirection is live. My recommendation is no — startup output only (AC-6) — but a
contract author should be the one to say so.

**OQ-4 — One test-mode declaration for all external services, or one per
service?** *Owner: contract-author, decided now even though only one service is in
scope.* Items B and the other provider will follow, and retrofitting the shape is
more expensive than choosing it. My recommendation: a single declaration that
permits stand-ins at all, with each service's address configured separately, so
that enabling the declaration redirects nothing by itself (AC-2). Deciding this
now costs nothing; deciding it twice costs a migration of everyone's environment
files.

**OQ-5 — How do the new scenarios avoid the two fifteen-minute caches?** *Owner:
plan-author and e2e-author.* §2.8: a free/busy result and a subscription-feed
fetch are both cached for fifteen minutes and the clock cannot be advanced. The
cheapest answer is that each scenario uses a distinct cache key — a distinct
attendee address and date — which is what `e2e/README.md` rule 6 already requires
of every mutating scenario for a different reason. If that turns out not to be
enough for AC-8, the alternative is a clock, and that is out of scope (§5), so
this needs to be settled at planning time rather than discovered at assert time.

**OQ-6 — Is there any use for this outside the test suite?** *Owner: whoever runs
the product locally.* If somebody wants an offline development mode against a
stand-in, the constraints in §3 might be the wrong shape — a developer's machine
is not a container network. I have assumed there is no such use and designed for
the narrowest case. If that assumption is wrong it is much cheaper to know now,
because relaxing AC-4 later is exactly the change nobody will want to review.

**OQ-7 — Does splitting off the provider-choice bug (§0.2, PAC-47) risk the suite
locking it in?** *Owner: spec-challenger, to push back on.* The scenarios written for AC-7
to AC-11 all exercise the primary provider, which is the path that works. None of
them asserts anything about the provider setting, so I believe they neither
enshrine nor obstruct the fix. But a suite that covers only the working path is
how a bug becomes permanent, and I would rather be told I am wrong about that now.

---

## Challenge — 2026-09-17 — **applies to v1 of this spec**

> *This is `spec-challenger`'s review of the v1 text above. It is retained
> verbatim and unedited. Every blocking finding and every correction in it is
> answered in `# Revision v2` at the end of this file; v2 §V2-0 maps each finding
> to the text that resolves it. Nothing in this section has been softened to make
> the revision look better.*

**Verdict**: accept-with-changes

The central argument holds and I verified it independently. `googlecalendar.NewService`
is constructed in **exactly one place in the repository** — `backend/calendar/client.go:17`
— and nowhere else; `grep` for `google.golang.org/api/calendar/v3` returns 35 files, all of
which import it for *types* only. `NewClient` has twelve call sites and the ten external
ones in §2.2 are each correct at the line cited. The MCP server (`mcp/`) does not import
`backend/calendar` at all. So "one constructor reaches everything" is true, and the spec does
not overstate its reach. §2.4 (the dead `NewProvider`), §2.6, §2.9 and the §2.7 fixme table
are all accurate against the source.

What does not hold is the safety argument. §3 and §6 make a guarantee — that the permitted
addresses are *"structurally incapable of being somewhere real"* and that redirection can
never hand out a credential — which AC-4 as described does not deliver, in five distinct
ways. And four of the fourteen criteria cannot be falsified by the test class they are
labelled with. Those are the blocking items.

---

### Blocking

**1. The host restriction is not structural, and §3/§6 claim that it is.**
OQ-2 proposes "a loopback literal, or a name with no dots", on the reasoning that *"every
public host name has a dot"*. That is true of the **name** and false of the **resolution**,
and the design checks the name. Ways I found to defeat it, without an attacker:

- **Search domains.** A single-label name is resolved by appending `search` suffixes from
  `/etc/resolv.conf`. Under Kubernetes (`search …svc.cluster.local`, `ndots:5`), under EC2
  (`search ec2.internal`), and under any corporate resolver configured with a company's own
  *public* zone, `fake-google` resolves to whatever that zone says — including a public
  address. The value passes validation and the credential leaves the host. This needs no
  attacker: a copied env file plus an inherited resolver config is enough, which is exactly
  the failure mode §6 names.
- **DNS rebinding / time-of-check.** Validation runs once, on a string, at startup. The name
  is resolved again on **every** calendar request for the life of the process, and nothing
  pins the answer. A name that resolves to `127.0.0.1` at boot resolves wherever a
  short-TTL record says a minute later. Only a literal address is checkable at all; a name
  is a promise made by something outside the process.
- **Loopback forms a literal comparison misses.** The plan's accept-table is
  `127.0.0.1`, `localhost`, `[::1]`, `fake-google`. The spec must state which property is
  being tested, because the following are each either loopback-or-local and would be
  *rejected* by a naive match, or non-loopback and would be *accepted*:
  `0.0.0.0`; the rest of `127.0.0.0/8` (`127.0.0.2`, `127.1.2.3`); `[::]`;
  `[0:0:0:0:0:0:0:1]`; `[::ffff:127.0.0.1]` (IPv4-mapped — `net.IP.IsLoopback` accepts it, a
  string match does not); `[::1%25lo]` (percent-encoded zone id).
- **The two buckets overlap, and the overlap fails open.** `2130706433`, `0x7f000001` and
  `017700000001` are all encodings of `127.0.0.1`. Go's `net.ParseIP` rejects every one of
  them (it has rejected leading zeros since 1.17 and never accepted the integer form). So
  they fall out of the "is it a loopback literal" branch — and straight into "has no dots",
  where they are promoted to *trusted container service name* and handed to the resolver.
  Anything that fails to parse as an IP must not be silently reclassified as trusted.
  `127.1` and `0x7f.0.0.1` have dots and are rejected, which is the right answer for the
  wrong reason.
- **Userinfo in the authority.** `http://fake-google@evil.example.com/calendar/v3/`.
  `net/url.Parse` gets this right (`u.Host` is `evil.example.com`), but only if the check
  reads the parsed host and not a hand-rolled split; and `http://127.0.0.1#@evil.example.com/`
  and a backslash-in-authority variant differ between Go and a browser. The spec must require
  that a value carrying userinfo is **refused**, not that the userinfo is ignored.

*What resolves it*: the spec must stop asserting a structural property it does not have.
Either §3/§6 drop "structurally incapable of being somewhere real" and state the weaker
truth — that the check reduces the blast radius of a mistyped value and does not survive a
hostile or merely unusual resolver — or AC-4 states the property that would actually be
structural: that the configured address cannot resolve off the local host **at any point in
the process's lifetime**, which is a different and stronger requirement than validating a
string once at boot. Either is acceptable; the present text promises the second and
specifies the first.

**2. The spec says nothing about redirects, and the credential leaves on hop two.**
This is the largest hole and it is independent of finding 1. AC-4 constrains the address the
server is *configured* with. It does not constrain where the response from that address sends
it next. A stand-in — or anything that has taken its place on a container network — that
answers `302 Location: https://attacker.example.com/…` gets the Go HTTP client to follow it,
and the credential is applied by a `RoundTripper` on each round trip rather than being set on
the original request, so Go's cross-domain sensitive-header stripping does not see it. The
permitted-host check is never consulted for the second hop. I could not read the vendored
transport to confirm the exact layering (see "could not settle"), but the requirement gap is
there whichever way the library behaves: **the spec contains no requirement about redirects
at all**, while §6 states the guarantee in absolute terms — *"hands every user's calendar
credential to whoever is listening… this is why AC-4 constrains the address by construction"*.

*What resolves it*: a criterion requiring that a redirected deployment refuse to follow a
response that directs it to a host outside the permitted set, and that AC-4's guarantee be
restated as applying to every hop rather than to the configured value. If the decision is to
accept the risk because a stand-in is trusted, that must be written down as an accepted
residual risk with the reason, not left unmentioned.

**3. AC-3 through AC-6 are labelled `[unit]` and cannot be falsified by a unit test.**
Each is stated as *"when the server is started, then it does not begin serving requests"*.
A table-driven test over a pure validator falsifies "the validator returns an error"; it
cannot falsify "the process refuses to serve". The gap between those two is the wiring in
`main.go` — whether it reads the right variable names, whether it calls the validator at all,
whether it fatals or logs and continues, and whether it does so before anything can serve —
and `CLAUDE.md` explicitly exempts `main.go` wiring from the coverage requirement. So the four
criteria that carry the entire safety case are the ones nothing is required to test. This is
the same shape as §1.4's own argument: the in-process test proves the algorithm and not the
boundary.

*What resolves it*: AC-3 to AC-6 are re-labelled to whatever class actually exercises a
started process, or each is split into the part a unit test can falsify (the decision) and the
part it cannot (the refusal to serve), with the second half given a test class that can
observe a process that did or did not come up.

**4. AC-13 cannot fail, and §3 states a universal that §5 contradicts.**
Two problems in one criterion.

- *It cannot fail on the free/busy path.* `backend/engine/freebusy_service.go:130-137`
  discards both errors — `if err == nil` on the constructor and `fetched, _ =` on the fetch.
  A connection refused to a blackholed `www.googleapis.com` therefore produces `coverage:
  "unknown"`, an empty slot list and a **200**. AC-13 says *"none of them fails with a
  connection error"*; that is satisfied by a path that never reached the stand-in at all.
  The criterion as written would pass over exactly the defect it exists to catch.
- *"Every calendar-dependent behaviour is redirected together. There is no path that keeps
  talking to the real provider"* (§3) is false as written, and §5 says so three paragraphs
  later. `backend/calendar/outlook_client.go:15` and `backend/conference/teams.go:13` keep
  `https://graph.microsoft.com/v1.0` as a package constant, and
  `backend/api/handlers_auth.go:172,195` keep the userinfo literal. Those are deliberately
  out of scope — but then §3's universal is wrong and AC-13's *"each calendar-dependent
  feature in turn"* has no defined membership.

*What resolves it*: AC-13 asserts positive evidence at the stand-in — that each named path
was observed arriving there — rather than the absence of an error at the product's edge; and
§3's universal is narrowed to the provider actually being redirected, with AC-13 enumerating
the exact set of features it covers.

---

### Non-blocking

1. **"Refuse to start" is right, but its only reachable context is production.** In a test
   deployment the declaration is set, so AC-3's branch never fires there. The branch exists
   solely for deployments that set an endpoint without declaring test mode — i.e. production
   with a stray variable. Refusing is still the correct call (§6's reasoning is sound: a run
   that believes it is isolated and is not is the worse outcome), but the spec presents it as
   costless and it is not: a variable inherited from a shared env file or a platform that
   injects by prefix turns the next restart — possibly an unrelated autoscale event — into a
   full outage with no config change. The spec should name that cost and the operator's
   recovery path explicitly rather than leaving it to be discovered.
2. **The refusal never reaches monitoring.** `main.go:26` defers `sentry.Flush`; `log.Fatal`
   calls `os.Exit`, which does not run deferred functions. AC-3/4/5's *"its output names the
   offending value"* will be true of stdout and false of everything an on-call engineer is
   watching.
3. **The redirection is wider than §2.2 says.** A package-level override in `NewClient` also
   redirects `backend/calendar/personal_reader.go:30` — the Google branch of the
   *personal*-calendar reader, driven by the `@every 30m` cron `main.go` registers at `:59-77`.
   The spec calls that "a different feature" and excludes it from the ten, which is right for
   the problem statement but wrong for the blast radius: in a redirected deployment that cron
   will fire at the stand-in on its own schedule, with whatever credentials are stored. Worth
   one sentence in §3, because a reader of §2.2 will not expect it.
4. **OQ-5: distinct cache keys is isolation, not coverage — say so.** I read the key
   (`freebusy_service.go:98`): `userID + ":" + email + ":" + date`. Distinct keys genuinely do
   isolate scenarios from each other, and the suite already requires that for another reason.
   But it is not a solution to the cache; it is a decision that the cache's own behaviour —
   that a second call within fifteen minutes is served from memory, and that expiry refetches —
   stays permanently uncovered, on the one surface the spec argues is the least covered in the
   product. The spec should record that as an accepted gap rather than an open question with a
   "cheapest answer". Note also that `FreeBusyService.Clock` is an **exported** field
   (`freebusy_service.go:49`) that `main.go` simply never assigns, so §2.8's framing of the
   clock as a wide-blast-radius change overstates it for this one engine.
5. **OQ-7 — you asked to be pushed back on, and I think you are right, narrowly.** The five
   scenarios assert nothing about `calendar_provider`, so they neither enshrine nor obstruct
   PAC-47. But there is a second-order risk you did not name: AC-8 goes through
   `freebusy_service.go:119-138`, whose `default:` branch is *the bug* — `"webcal"` falling
   through to Google. A green AC-8 is a test asserting that the default branch answers from
   Google, which is correct for a Google user and is the exact line PAC-47 must change. The
   scenario should be written so that it constrains the Google case only, or PAC-47 will
   arrive to find a green test over the code it needs to rewrite.
6. **Item B's exclusion is sound, and the stated reason is the weaker of the two available.**
   "It is the authentication path, where a mistake is a login bypass" is real but proves too
   much — it would exclude ever testing sign-in. The stronger reason is the one in the same
   paragraph and it checks out: I read `auth-session.spec.ts:115`'s neighbourhood and
   `e2e/README.md:92-107`, and a validly signed JWT *is* the session with no server-side
   store, so everything downstream of the callback is already exercised legitimately. Item B
   buys exactly one scenario — the callback handler's own code exchange, userinfo fetch,
   upsert and persist. That is a genuine coverage gap and a small one. Lead with the
   cost/benefit, not the danger; the danger argument invites the reply "then it needs doing
   carefully, not never".
7. **§8 OQ-1's contingency is more expensive than it needs to be and partly wrong** — see
   citations.

---

### Criteria I could not falsify

- **AC-3, AC-4, AC-5, AC-6** — as `[unit]`. See blocking 3. The unit test falsifies the
  validator's return value; no test named here distinguishes a build that refuses to serve
  from one that logs the same message and serves anyway.
- **AC-13** — see blocking 4. On the free/busy path the product swallows the connection error
  and returns 200 with `coverage: "unknown"`, so "no connection error" is true of a run in
  which nothing reached the stand-in.
- **AC-12, first half** — *"given a stand-in that refuses any request that carries no
  credential, when any of AC-7 through AC-11 runs, then it succeeds — proving the credential
  is still being sent"*. It proves it for AC-7, AC-9, AC-10 and AC-11, whose paths surface the
  failure. It does not prove it for AC-8: a 401 on the free/busy path is discarded at
  `freebusy_service.go:135` and reported as `coverage: "unknown"`. AC-8's own assertion that
  coverage is `"known"` happens to catch it, so the criterion is salvageable — but it is being
  proven by AC-8's assertion, not by AC-12's premise, and AC-12 should say which criteria it
  actually covers.
- **AC-1** — falsifiable, but not by the means §4 implies. *"the outbound request is addressed
  exactly as it is today — same address, same credential, same body"* cannot be observed
  without an outbound request, and the production case has nowhere to send one. What is
  actually falsifiable is the constructed client's base path, which is what the plan tests.
  The criterion should be stated in terms of what can be observed, or it will be quietly
  reinterpreted at stage 4.

---

### Citations I checked and found wrong

- **§2.8 and OQ-5, *"the subscription-feed fetch cache (`backend/calendar/webcal_client.go:26`)
  … sits in the paths those scenarios exercise"*** — wrong twice over. (a) No path in the five
  scenarios reaches a `WebcalClient`: its only two live constructors are
  `client_factory.go:26` (dead — `NewProvider` has no callers, as §2.4 itself establishes) and
  `personal_reader.go:16`, which is the personal-calendar feature that §2.5 correctly puts
  outside this spec. (b) More decisively, **that cache cannot cache anything**:
  `personal_reader.go:16` calls `NewWebcalClient(pc.URL)` and immediately `ListEvents` on the
  fresh instance, so `cachedAt` is always the zero time and `webcal_client.go:52-57` never
  hits. There is one fifteen-minute cache in scope, not two.
- **OQ-1's contingency, *"the stand-in needs a certificate that the backend image trusts…
  the `scratch` backend image needs a trust store"*** (repeated in plan §7 item 1 and in the
  Linear comment) — the scratch image **already has one**. `backend/Dockerfile:10` copies
  `/etc/ssl/certs/ca-certificates.crt` from the builder stage into `scratch`, and `:11` copies
  `zoneinfo`. If OQ-1 resolves against plain HTTP, the work is adding one certificate to an
  existing bundle, not adding a trust store to an image that has none. This matters because
  OQ-1 is presented as the assumption the whole design rests on and its downside is
  overstated.
- **§3, *"Every calendar-dependent behaviour is redirected together. There is no path that
  keeps talking to the real provider after redirection"*** — contradicted by §5 and by
  `outlook_client.go:15`, `conference/teams.go:13`, `handlers_auth.go:172,195`. See blocking 4.
- **Not wrong, and I checked it because it looked like it should be**: §2.7's claim that
  `manager-team.spec.ts:137` is blocked by this seam. It is. The fixme comment names
  `/api/manager/detect` → `ManagerEngine.DetectTeam()` → `calendar.NewClient` at
  `manager.go:196`, and `e2e/README.md:122` lists *"Manager 1:1 detection (re-scan calendar)"*
  as `test.fixme` on this seam. What is separately covered "for other reasons" is the manager
  **roster** (`README.md:118`, covered UI + API), which is a different scenario in the same
  file. All five journeys in §2.7 are correctly attributed.

---

### Consumers this breaks

**None found.** This is the one place the spec is stronger than it claims. I searched for
every construction of a Google calendar service across both Go modules:
`googlecalendar.NewService` appears exactly once, at `backend/calendar/client.go:17`. No
handler, engine, cron, test helper or the `mcp/` binary builds one by another route, and
`mcp/` does not import `backend/calendar` at all. `backend/calendar/calendar_test.go:23-37`
is the only existing test of the constructor, it asserts nothing about the endpoint, and it
keeps passing untouched. There is no consumer of current behaviour to break because there is
no current behaviour anything can observe — which is §2.9's finding, and it is correct.

---

### One thing I could not settle, and exactly what would

**OQ-1 is still open and I could not close it from the repository.** There is no `vendor/`
directory under `backend/`, `go env GOMODCACHE` points at `/root/go/pkg/mod` which is empty,
and a filesystem-wide search for any `google.golang.org` source directory returns nothing.
The registries are blocked here. So the question cannot be answered by reading, and I did not
run anything.

Two things I can add to it from the repository, which sharpen what has to be checked:

1. **The version is pinned, so the answer is deterministic.** `backend/go.sum` pins
   `google.golang.org/api v0.292.0` and `cloud.google.com/go/auth v0.22.0`. Whatever the
   answer is, it is a fact about those two versions and can be recorded once.
2. **OQ-1 is three questions, not one, and the spec asks only the first two.** (a) Does
   `option.WithEndpoint` accept a non-TLS scheme, or reject it? (b) Does the credential
   transport attach a bearer token over plain HTTP, or refuse? (c) **Which credential stack
   does `option.WithTokenSource` select in v0.292.0** — the legacy `golang.org/x/oauth2`
   transport, or the newer `cloud.google.com/go/auth` path that is now an indirect
   dependency? (c) determines the answers to (a) and (b) and also determines the redirect
   behaviour in blocking finding 2, and it is not asked anywhere in the spec or the plan.

*What would settle all three*: on a machine with registry access, `go mod download
google.golang.org/api` and then read three files at the pinned version —
`option/option.go` (`WithEndpoint`), `internal/settings.go` (its validation and which
credential path it chooses), and `transport/http/dial.go` (how the token source becomes a
`RoundTripper`, and therefore whether the credential is re-applied on a redirect). That is
reading, not running, and it answers (a), (b), (c) and blocking finding 2 together. The
ten-line `httptest` program in plan §7 answers (a) and (b) empirically but will not tell you
(c) or the redirect behaviour, so it is the weaker of the two checks despite being the one
both documents propose.

---

# Revision v2 — 2026-09-17

- **Supersedes**: v1 §3, §4, §6, §8. Extends v1 §2 (new §V2-2) and v1 §5 (new §V2-7).
- **Author**: spec-author, revising after `spec-challenger`'s accept-with-changes.
- **Status**: ready for re-challenge.

> **Evidence note, restated for v2.** The environment still blocks
> `proxy.golang.org` and the npm registry. Nothing in this revision was compiled,
> run or tested. Every new claim about current behaviour carries a `file:line`
> that I opened myself during this revision — including the four citations the
> challenger reported as wrong, all four of which I re-checked independently
> rather than taking on trust. §V2-8 states precisely what remains unsettleable
> here and what would settle it.

---

## V2-0. What each blocking finding forced, and where it is answered

| Challenge finding | v1 was wrong because | Resolved in |
|---|---|---|
| **1. The host restriction is not structural and fails open** | v1 validated a *name*, classified `no dots` as trusted, and therefore promoted `2130706433`, `0x7f000001` and `017700000001` — none of which parse as an address — into the trusted bucket. It also could not survive a search domain, a short-TTL record, or an unusual loopback spelling. | §V2-3 (the control moves from name-validation to connection-time address judgement), AC-5 to AC-9, AC-15 |
| **2. Redirects entirely unaddressed** | v1 constrained the first hop only; a `3xx` from the stand-in carried the credential anywhere. | §V2-3, AC-10, AC-11 — plus the connection-time rule of finding 1, which applies to every hop and is why the two findings share one mechanism |
| **3. AC-3 to AC-6 labelled `[unit]` but assert "does not begin serving"** | A pure validator cannot falsify a process refusing to serve, and the wiring that could be wrong is coverage-exempt. | Each is split: the **decision** is `[unit]` (AC-4, AC-5, AC-6, AC-15), the **refusal to serve** is `[e2e]` against the built image (AC-3, AC-12, AC-13, AC-14) |
| **4. AC-13 cannot fail; §3's universal is contradicted by §5** | The free/busy path discards both errors, so "no connection error" is true of a run that reached nothing. And Graph and the identity lookup stay hardcoded. | §V2-2.1 (the swallowing, verified), §V2-3 (the universal narrowed and its non-members enumerated), AC-22 and AC-23 (positive arrival evidence, enumerated membership) |

Every correction the challenger raised under *"Citations I checked and found wrong"* and every non-blocking item is answered in §V2-2, §V2-7 or §V2-8. None is dismissed.

---

## V2-1. The idea that changed

v1 asked: *which host strings are safe to configure?* Every defeat the challenger
found is a defeat of that question, not of a particular answer to it. A name is a
promise made by something outside the process — a resolver, a search list, a TTL —
and no amount of string inspection converts a promise into a property.

v2 asks a different question: **where is this connection actually going, at the
moment the operating system is about to make it?** That question has an answer
the process owns. It is a number, not a name. It is available immediately before
the connect call, on every connection, on every hop, after every resolution. And
a credential that is only ever written onto a connection whose peer is inside a
fixed set of non-internet-routable ranges cannot leave the private network, no
matter what was configured, resolved or redirected.

This is why findings 1 and 2 collapse into one mechanism. A redirect is just
another connection, and it is judged the same way.

Two consequences worth stating before the requirements:

- **Startup validation survives, demoted.** It still refuses the unsafe
  *combination* (a redirection with no test-mode declaration) and it still
  refuses a value nobody could have meant. But it is a usability check that turns
  a typo into a fast failure. **It is no longer the safety control and §V2-6 says
  so in terms.** The safety control is the connection-time rule.
- **Nothing is ever promoted by failing to parse.** The single worst defeat in the
  challenge — numeric address forms falling out of "is it a loopback literal" and
  into "has no dots, therefore trusted" — is impossible in v2 because the trusted
  bucket does not exist. There is no classification of hosts by shape at all.

---

## V2-2. Current behaviour — additions and corrections to v1 §2

v1 §2 stands except as below. These were opened and read during this revision.

### V2-2.1 The free/busy path discards the errors that would prove it reached anything

The challenger's blocking finding 4 is correct and I verified it independently.
`backend/engine/freebusy_service.go:121-137`: the provider switch's `default:`
branch loads the user's token, and then

```go
gc, err := calendar.NewClient(qCtx, ts)
if err == nil {
    fetched, _ = gc.GetFreeBusy(qCtx, queryEmails, start, end)
}
```

Both errors are dropped — the constructor's by the `if err == nil`, the fetch's by
the blank identifier. `fetched` stays nil, and at `:139-150` a nil entry produces
`coverage = "unknown"`, an empty slot list, and a normal success response. A
connection refused to a blackholed provider address is therefore indistinguishable,
at the product's edge, from an attendee whose calendar is genuinely unknown.

Two consequences, both of which change the criteria:

1. **No criterion may be satisfied by the absence of an error on this path.** v1
   AC-13 was; v2 AC-22 asserts arrivals at the stand-in instead.
2. **This is a product defect and it is not this spec's to fix.** It is filed
   separately (§V2-7). Fixing it here would change an observable response, which
   v1 §6 correctly forbids.

The same shape appears in the `"outlook"` branch at `:124-127` (`fetched, _ =`),
which is out of scope for a different reason (§V2-3).

### V2-2.2 The subscription-feed cache is not in scope, and cannot cache

The challenger is right and v1 §2.8 was wrong twice. I re-read both files.

`backend/calendar/webcal_client.go:25-27` constructs a client with a fifteen-minute
TTL, and `:50-56` returns the cached slice when `time.Since(c.cachedAt) < c.cacheTTL`
**and** `c.cached != nil`. Its only two constructors in the repository are
`backend/calendar/client_factory.go:26` — dead, because `NewProvider` has no
callers, which v1 §2.4 itself establishes — and
`backend/calendar/personal_reader.go:16`, which calls `NewWebcalClient(pc.URL)` and
then `ListEvents` on the instance it has just created, at `:17`. `cachedAt` is the
zero time on every call and the instance is discarded afterwards, so the branch at
`:50` is never taken. That cache has never cached anything.

**There is one fifteen-minute cache in the scope of this work, not two**: the
free/busy result cache at `backend/engine/freebusy_service.go:139,155-172`.
v1 §2.8's second bullet and v1 OQ-5 are corrected accordingly, and the cache
question is now an accepted gap rather than an open question (§V2-8, AG-1).

### V2-2.3 The image already has a trust store

The challenger is right. `backend/Dockerfile:9-12` is a `scratch` stage that copies
`/etc/ssl/certs/ca-certificates.crt` from the builder at `:10` and `zoneinfo` at
`:11`. v1 OQ-1's contingency — *"the `scratch` image needs a trust store"* — is
false. If the credential transport turns out to refuse a non-TLS endpoint, the
work is adding one certificate to a bundle that exists, not adding a bundle. This
matters because v1 presented OQ-1 as carrying a large contingent cost, and it does
not.

### V2-2.4 The clock: one engine's is already reachable

Verified: `backend/engine/freebusy_service.go:49` declares `Clock Clock` as an
**exported** field on `FreeBusyService`, and `:53-58` falls back to `SystemClock`
only when it is nil. `backend/main.go` never assigns it (the variables it reads are
listed in v1 §2.3; none is a time). So for this one engine, driving the clock needs
no new seam — only an assignment. v1 §2.8 framed the clock uniformly as a
wide-blast-radius change; that is true of the package-level default and not true
here. It stays out of scope (§V2-7), but the reason is now accurate.

### V2-2.5 Redirection reaches one more caller than v1 §2.2 lists

`backend/calendar/personal_reader.go:30` constructs a client through the same
constructor, for the *personal*-calendar reader, which
`backend/main.go:57-77` drives on an `@every 30m` schedule. v1 called this "a
different feature" and excluded it from the ten call sites, which is right for the
problem statement and wrong for the blast radius: a redirected deployment redirects
that background sync too, on its own timer, with whatever credentials are stored.
§V2-3 now says so, and AC-22 lists it as a member of the redirected set rather than
leaving a reader of v1 §2.2 to be surprised.

### V2-2.6 What stays hardcoded

Re-verified for §V2-3's enumeration: `backend/calendar/outlook_client.go:15` and
`backend/conference/teams.go:13` each hold `https://graph.microsoft.com/v1.0` as a
package constant, and `backend/api/handlers_auth.go:172` and `:195` each hold the
literal `https://www.googleapis.com/oauth2/v2/userinfo` inline in a request
construction. None of these is redirected by this work.

---

## V2-3. Desired behaviour (replaces v1 §3)

**Nothing a user can see changes.** Unchanged from v1 and still the first
requirement: not the events on their week, not the focus blocks, not the errors
they get when they have not connected a calendar, not the address their calendar
data travels to. A deployment that is not being tested behaves identically before
and after, and a test says so rather than a reviewer.

**A test deployment can be told to direct primary work-calendar traffic at a
stand-in.** The operator states two separate things: that this deployment may use
stand-ins for external services at all, and where the calendar stand-in is.
Neither implies the other. Stating only the first changes nothing; stating only
the second is refused before anything is served.

### The guarantee, stated as the property that actually holds

**A calendar credential is only ever written onto a connection whose peer address
lies inside a fixed, compiled-in set of ranges that are not routable on the public
internet.** This is judged:

- **on the address, never on the name.** No host is ever classified as safe or
  unsafe by the shape of its name — not by whether it contains a dot, not by
  whether it looks like a number, not by its length. A name is only ever an input
  to resolution, and resolution's *output* is what is judged. There is no bucket
  that an unparseable value can fall into and be trusted by.
- **immediately before each connection is established**, not once when
  configuration is read. A name that resolved acceptably a minute ago is judged
  again now, on the answer being used now.
- **on every connection the calendar traffic makes**, including any that results
  from being told to go somewhere else.
- **fail-closed.** Anything that cannot be judged is refused. A resolution that
  yields no permitted address is a failure, never a fallback.

The permitted set is fixed in the build and is not operator-configurable: the
loopback range in both address families, and the private ranges an isolated
container network is drawn from. It **excludes** link-local addresses — which is
where cloud platforms put instance metadata and credentials — and it excludes the
unspecified address. A deployment cannot widen it by configuration, because a set
an operator can widen is a set an operator can widen by accident, and the failure
mode this is designed against is a copied environment file rather than an attacker.

This is the property v1 §3 claimed and did not have. It is worth being exact about
what it is not: it does not prevent a stand-in that is genuinely on the private
network from receiving the credential — that is the entire point of the feature —
and it does not protect a deployment whose private network already contains
something hostile. What it does is make "the credential reached a public host"
impossible by construction rather than improbable by configuration, which is the
failure the product cannot survive.

### Redirection is not followed

**In a redirected deployment, a response directing the client somewhere else is
never followed. It surfaces as a failure.** This is a second, independent
requirement, not a consequence of the first: the connection rule would already stop
the credential leaving the private network on hop two, but a stand-in that can move
the client around inside the private network is still a stand-in that can make a
test pass against the wrong thing. Refusing to follow makes the first hop the only
hop, which is also what makes AC-22's arrival log mean what it says.

### The unsafe combinations are refused loudly, before anything is served

A deployment that names a stand-in without declaring itself a test deployment does
not begin serving. A deployment whose named stand-in cannot be understood at all
does not begin serving. A deployment that names a stand-in at a literal address
outside the permitted set does not begin serving. In each case the process exits
and its output names the offending value and why it was refused — and a criterion
observes the *process*, not a validator's return value.

**This startup refusal is a convenience, not the safety control.** It catches a
typo in seconds instead of at the first calendar request. The safety control is the
connection rule above, and it holds even for a deployment that passes every startup
check.

### A redirected deployment says so, unmistakably

Its startup output states that calendar traffic is redirected, names where to, and
states that this is not a production deployment.

### What is redirected, exactly — and what is not

v1 claimed *"there is no path that keeps talking to the real provider"*. That was
false as written and v1 §5 contradicted it. The accurate statement, which AC-22
enumerates and asserts:

**Redirected — every path that reaches the primary work-calendar provider's API:**
viewing a week's events; free/busy for an attendee; focus-time creation and
clearing; compression; smart scheduling and natural-language confirmation;
collective slots and booking confirmation; manager 1:1 detection; weekly analytics
recompute; habit scheduling; auto-decline; team availability; event patch and
delete; room and attendee suggestion; **and the personal-calendar background sync's
primary-provider branch** (§V2-2.5), which runs on its own timer and which a reader
of v1 §2.2 would not have expected.

**Not redirected, deliberately, and each one keeps talking to the real service:**
the second calendar provider's API and the conferencing integration that shares it
(§V2-2.6); sign-in, token exchange and token refresh; the identity lookup after
sign-in. The first is out of scope because no journey needs it (§V2-7). The last
two are the sign-in seam, which is a separate issue.

**One residual risk follows from that last exclusion and is accepted here rather
than left implicit.** Token refresh is not redirected, so in a redirected
deployment a refresh still leaves the host, carrying a refresh credential — which
is a longer-lived secret than the access credential this whole design is protecting.
The test stack's existing isolation is what contains it: the provider hostnames are
mapped to the local machine inside the container, so a refresh fails fast rather
than reaching anything. **That isolation is therefore load-bearing for safety and
not merely for hermeticity**, which v1 §6 did not say, and AC-23 is what proves it
is still in place.

### The stand-in is talked to exactly as the real service is

It receives the same requests, carrying the same credential, and its answers are
interpreted by the same code. The credential is still sent and still required, so a
test cannot pass while the credential path is broken.

**With one honest exception, because v2 introduces it.** The connection rule and
the refusal to follow redirection have to be applied by something the redirected
deployment installs, and a production deployment installs nothing — that is what
makes "nothing changes without configuration" true. So the redirected deployment's
request path is not byte-for-byte the production one; it is the production one plus
two guards. Whether those guards can be installed without also displacing the
credential-attaching machinery the provider's own client library supplies is the
question §V2-8 OQ-1(c) exists to settle, and it is the single largest thing this
revision cannot verify from here.

### What becomes true as a consequence

The five scenarios named in v1 §2.7 can be written as real assertions against the
running stack, and the calendar and focus surfaces become subject to the same
release gate as everything else.

---

## V2-4. Acceptance criteria (replaces v1 §4)

Twenty-five criteria in eight groups. Every one names the observation that would
falsify it. The label is the class of test that can actually make that observation:
`[unit]` where the observation is a return value or an in-process request,
`[e2e]` where it requires a process that did or did not come up, or the running
stack. **No criterion is labelled `[unit]` if falsifying it requires observing a
started process** — that was blocking finding 3, and the split below is the answer.

There are no `[contract]` criteria. No request, response, status code or operation
changes, which is why there is no contract artifact (plan §0).

### A. No change without configuration

**AC-1.** Given a deployment with neither the test-mode declaration nor a calendar
redirection configured, when a calendar client is constructed, then it is
configured with the provider's own published address, no connection guard is
installed, no redirection policy is installed, and the credential is attached by
the provider library's own machinery — i.e. the construction is indistinguishable
from today's.
*Falsified by*: a test that constructs the client with no configuration and
inspects the constructed object's target address and the shape of its request path;
it fails the moment anyone gives the redirection a default value or installs a
guard unconditionally. `[unit]`

**AC-2.** Given a deployment that declares test mode but configures no redirection,
when a calendar client is constructed, then AC-1 still holds in full. Declaring
test mode grants nothing by itself.
*Falsified by*: the same inspection with only the declaration set. `[unit]`

**AC-3.** Given the built server image and exactly the configuration a production
deployment supplies today, when it is started, then it serves requests normally.
*Falsified by*: starting the image with today's environment and finding it exits or
never binds. This is the guard that the new startup step cannot brick production,
and it is the criterion that would catch a validator that rejects the empty case.
`[e2e]`

### B. Configuration decisions (the part a pure validator owns)

**AC-4.** Given a calendar redirection configured and no test-mode declaration,
when the configuration is evaluated, then it is refused, and the refusal names both
settings — the one that was set and the one that was not.
*Falsified by*: a table-driven test asserting the refusal and its text. `[unit]`

**AC-5.** Given a configured redirection that cannot be understood — unparseable,
no host, a scheme that is not HTTP or HTTPS, or an authority carrying user
information — when the configuration is evaluated, then it is refused and the
refusal names the value. A redirection is never partially applied and never
silently discarded.
*Falsified by*: a table-driven test over those forms. A value carrying user
information is **refused**, not sanitised — it signals a confused operator, and
guessing what they meant is how the confusion reaches production. `[unit]`

**AC-6.** Given the table `2130706433`, `0x7f000001`, `017700000001`, `127.1`,
`0x7f.0.0.1`, `fake-google`, `localhost`, `[::1]`, `[0:0:0:0:0:0:0:1]`,
`[::ffff:127.0.0.1]`, `[::1%25lo]`, `0.0.0.0`, `[::]`, `evil.example.com`, and
`fake-google@evil.example.com` as configured hosts, when each is evaluated, then
**no outcome depends on the shape of the host** — not on whether it contains a dot,
not on whether it resembles a number, not on its length. Each is either understood
(and therefore subject to group C at connection time) or refused for a reason AC-5
names. In particular, a value that fails to parse as an address is **never**
reclassified as trusted.
*Falsified by*: that exact table, asserting the outcome for each and asserting that
the code path taken carries no notion of a trusted host name. This criterion exists
because the worst defect in v1 was a value falling out of one bucket into another;
it is here to make that reclassification a test failure rather than a code review
question. `[unit]`

**AC-15.** Given a configured redirection whose host is a **literal address**
outside the permitted ranges — a public address, a link-local address, or the
unspecified address — when the configuration is evaluated, then it is refused and
the refusal names the value. Given one whose host is a **name**, when the
configuration is evaluated, then it is accepted regardless of the name, because
names are judged at connection time.
*Falsified by*: a table containing a public literal, `169.254.169.254`, `0.0.0.0`,
a private literal, a loopback literal in each of its spellings, and two names —
asserting refusal, acceptance, and that the refusal text names the value. This is
the demoted startup check, and the criterion is deliberately worded so that a
reader cannot mistake it for the safety control. `[unit]`

### C. The connection-time guarantee (the safety control)

**AC-7.** Given the connection guard and the peer addresses `127.0.0.1`,
`127.0.0.2`, `127.1.2.3`, `::1`, `::ffff:127.0.0.1`, a private address from each
permitted range, an IPv6 unique-local address, `169.254.169.254`, a link-local
IPv6 address, `0.0.0.0`, `::`, `172.32.0.1` (outside the private range that
neighbours it), and two public addresses, when each is judged, then exactly the
loopback and private ones are permitted and every other is refused. An
IPv4-mapped IPv6 address is judged as the address it maps to.
*Falsified by*: that table. The neighbouring-range case and the mapped case are
there because both are classic off-by-one errors in exactly this kind of check.
`[unit]`

**AC-8.** Given a redirected deployment whose configured stand-in name resolves to
an address outside the permitted set, when any calendar operation is attempted,
then the operation fails, no bytes are written to that peer, and in particular no
credential is. The failure names that the peer address was refused.
*Falsified by*: an in-process test that installs a resolution answer outside the
permitted set, runs a calendar operation, and asserts both the failure and that a
recording listener at that address received no connection. This is the search-domain
defeat from the challenge — a single-label name resolving publicly with no attacker
involved — and it must fail closed. `[unit]`

**AC-9.** Given a redirected deployment, when the same configured name resolves to
a permitted address on one request and to a non-permitted address on the next, then
the first request succeeds and the second is refused. The judgement is made per
connection, from the answer that connection is using.
*Falsified by*: an in-process test with a resolution answer that changes between two
calls. This is the rebinding and time-of-check defeat; a design that validates once
at startup cannot pass it. `[unit]`

**AC-10.** Given a redirected deployment, when the stand-in answers a calendar
request by directing the client to another location — inside the permitted set or
outside it — then the direction is not followed, the operation fails, and the second
location receives no request of any kind.
*Falsified by*: two in-process servers, the first answering with a redirection to
the second; assert the failure and that the second recorded nothing. The
"inside the permitted set" half is deliberate: refusing only external redirections
would leave a stand-in able to move the client around the private network, which is
how a test passes against the wrong service. `[unit]`

**AC-11.** Given the running redirected stack and a stand-in placed in a mode where
it answers with a direction to an address outside the permitted set, when a
calendar-dependent screen is used, then the user is shown a failure, and a recording
sink at the redirection target records no connection and no credential.
*Falsified by*: driving that screen with the stand-in in that mode and inspecting the
sink. AC-10 proves the policy; this proves the policy is installed in the binary
that actually ships to the test stack. `[e2e]`

### D. Startup refusal, observed on a process

Each of these is the half of a group-B criterion that a validator cannot prove.
They are `[e2e]` because falsifying them requires a process that either came up or
did not.

**AC-12.** Given the built server image configured with a calendar redirection and
no test-mode declaration, when it is started, then it exits without success, it
never accepts a request on its port, and its output names the setting that was
refused and why.
*Falsified by*: starting the image with that environment; assert non-zero exit, a
refused connection on its port, and the message. `[e2e]`

**AC-13.** Given the built server image with the test-mode declaration and a
redirection that AC-5 or AC-15 refuses, when it is started, then it exits without
success, never accepts a request, and its output names the rejected value.
*Falsified by*: the same, once per refusal class. `[e2e]`

**AC-14.** Given the built server image with the test-mode declaration and a valid
redirection, when it is started, then it serves requests, and its startup output
states that calendar traffic is redirected, names the target, and states that the
deployment is not a production deployment.
*Falsified by*: starting the stack and reading the first lines of its output. This
is also the cheapest end-to-end proof that the whole configuration chain is
connected, which is why the plan sequences it before any scenario. `[e2e]`

### E. What becomes provable — the five journeys

**AC-16.** Given a redirected deployment whose stand-in holds a known set of events
in a known window, and a user with a stored calendar credential, when that user's
calendar is requested for that window, then the response describes exactly those
events, and the stand-in records having been asked for that window.
*(the scenario disabled at `tests/calendar.spec.ts:73`)* `[e2e]`

**AC-17.** Given the same, and given a requesting user whose stored work-calendar
provider is the primary one, when free/busy is requested for an attendee the
stand-in holds busy windows for, then the reported busy windows are exactly those
and the attendee's availability is reported as **known** rather than unknown.
*(`tests/calendar.spec.ts:83`)*
**This criterion constrains the primary-provider case only.** It must not be
written in a way that asserts anything about what a user of a different configured
provider receives: that behaviour is the subject of the separate provider-choice
issue, and a green assertion over it would arrive as a passing test on the exact
lines that issue must rewrite. The "known rather than unknown" half is not
decoration — per §V2-2.1 it is the only observable difference between reaching the
stand-in and reaching nothing at all on this path. `[e2e]`

**AC-18.** Given a redirected deployment, a user with a stored credential, and a
week in which the stand-in holds two meetings, when focus time is run for that week,
then focus blocks exist in the product's own store, matching events exist on the
stand-in, and the user sees those blocks on their week.
*(`tests/focus-time.spec.ts:75`)* `[e2e]`

**AC-19.** Given the state AC-18 leaves behind, when that week is cleared, then the
blocks are gone from the product's store **and** the corresponding events are gone
from the stand-in — neither alone is sufficient.
*(`tests/focus-time.spec.ts:86`)* `[e2e]`

**AC-20.** Given a redirected deployment whose stand-in holds recurring two-person
meetings between a manager and two colleagues, when the manager rescans for team
members, then those two colleagues are proposed and nobody else is.
*(`tests/manager-team.spec.ts:137`)* `[e2e]`

### F. The credential is still required

**AC-21.** Given a redirected deployment whose stand-in refuses any request that
carries no credential, when AC-16, AC-18, AC-19 and AC-20 run, then each succeeds —
which proves the credential is still being sent on those paths. And given a user
whose stored credential the stand-in rejects, when their calendar is requested, then
they are refused rather than served.
**This criterion does not cover AC-17.** A rejected credential on the free/busy path
is discarded (§V2-2.1) and reported as unknown availability with a normal response,
so a missing credential there would not surface as a failure. What catches it is
AC-17's own assertion that availability is reported as known. Naming that here
rather than leaving it implicit is the point: v1's version of this criterion claimed
a coverage it did not have. `[e2e]`

### G. No escape hatches — positive evidence only

**AC-22.** Given a redirected deployment, when each behaviour enumerated in §V2-3 as
redirected is exercised in turn, then **for each one the stand-in's own record shows
at least one request arriving that is attributable to that exercise**. The absence of
an error at the product's edge does not satisfy this criterion for any member of the
list.
*Falsified by*: exercising the list and reading the stand-in's request record; a
member with no arrival fails, regardless of what the product returned. v1's version
of this criterion was satisfied by a run in which nothing reached the stand-in at
all, because the one path it most needed to cover returns success on failure
(§V2-2.1). Membership of the list is closed and enumerated in §V2-3 so that
"each calendar-dependent feature" has a definition rather than a gesture. `[e2e]`

**AC-23.** Given a redirected deployment in which every real provider address is
mapped to a recording sink inside the deployment, when the whole suite runs, then the
sink records no connection at all.
*Falsified by*: reading the sink after a full run. This replaces the current
arrangement, which maps those names to an address where nothing listens: nothing
listening proves a connection failed, but produces no evidence of how many were
attempted or from which path. Per §V2-3 this is load-bearing for the token-refresh
residual risk and not only for hermeticity, so it must produce evidence rather than
silence. `[e2e]`

### H. Preserved

**AC-24.** Given a user with no stored calendar credential, when they request their
calendar, then they are refused exactly as they are today, and the interface says so
rather than inventing a week.
*(already proven by `tests/calendar.spec.ts:25,42,52`, which must stay green)*
`[e2e]`

**AC-25.** Given a redirected deployment, when the personal-calendar background sync
runs on its own schedule, then its primary-provider branch reaches the stand-in and
not the real provider.
*Falsified by*: the stand-in's request record, and the sink of AC-23. This is here
because §V2-2.5 makes it true whether or not anyone intended it; an unstated
consequence of a security-relevant change is a finding, and stating it turns it into
a test. `[e2e]`

---

## V2-5. Mapping from v1 criteria

| v1 | v2 | What changed |
|---|---|---|
| AC-1 | AC-1 | Restated as what is observable — the constructed client — rather than an outbound request a production deployment has nowhere to send |
| AC-2 | AC-2 | Unchanged in substance |
| — | AC-3 | **New.** Nothing in v1 proved the new startup step cannot break a production boot |
| AC-3 | AC-4 + AC-12 | Split: the decision is `[unit]`, the refusal to serve is `[e2e]` |
| AC-4 | AC-7, AC-8, AC-9, AC-15 + AC-13 | **Replaced.** The address check moved from startup name-validation to connection-time address judgement; the startup remnant is AC-15 and is explicitly not the control |
| AC-5 | AC-5, AC-6 + AC-13 | Split, and extended with the table that makes the fail-open reclassification a test failure |
| AC-6 | AC-14 | Re-labelled `[e2e]`: it asserts what a started process printed |
| — | AC-10, AC-11 | **New.** Redirects, which v1 did not mention |
| AC-7 | AC-16 | Unchanged in substance |
| AC-8 | AC-17 | Narrowed to the primary-provider case; the availability assertion is now load-bearing |
| AC-9, AC-10, AC-11 | AC-18, AC-19, AC-20 | Unchanged in substance |
| AC-12 | AC-21 | Now states which criteria it covers and which it does not |
| AC-13 | AC-22 + AC-23 | **Rewritten.** Positive arrival evidence, enumerated membership, and a recording sink instead of silence |
| AC-14 | AC-24 | Unchanged |
| — | AC-25 | **New.** The background sync is redirected too |

---

## V2-6. What must not change (replaces v1 §6)

- **Where a production deployment sends calendar traffic.** Protected by no test
  today (v1 §2.9). AC-1 and AC-3 are the new guards and they are the two most
  important criteria in this spec.
- **What a user sees, anywhere.** No screen, no response body, no error text, no
  status code. If the plan proposes a response change, scope has grown. In
  particular the free/busy error swallowing in §V2-2.1 **must not be fixed here**,
  however tempting: fixing it changes what a user sees.
- **No stored data changes.** There is no migration. If the plan proposes one, that
  is the same signal.
- **The behaviour of a user with no connected calendar.** Every seeded user in the
  suite is in that state today and four passing scenarios depend on it. Adding
  redirection must not turn "not connected" into "connected to a stand-in" for a
  user who has stored no credential. AC-24.
- **The credential must still be required.** AC-21, with the exception it names.
- **Redirection must never carry a credential to a public address, by any route.**
  This is v1's requirement with its guarantee repaired. The failure mode is not an
  attacker; it is a copied environment file, an inherited resolver configuration, a
  short-TTL record, or a stand-in that has been replaced by something else on the
  same network. The control is the connection rule in §V2-3, and it is stated as a
  property of every connection because every one of those failure modes defeats a
  property of a configured string. **The startup validation is not this control and
  no reviewer should treat it as one** — §V2-3 and AC-15 both say so, because v1's
  §6 said the opposite and that is what the challenge was about.
- **The suite's provider-hostname isolation.** It must stay, and per AC-23 it must
  become a recording sink rather than a closed port. §V2-3 explains why it is now
  load-bearing for safety: token refresh is not redirected, so that mapping is what
  keeps a refresh credential from leaving a test host. Removing it because "we have
  a stand-in now" would delete both the evidence for AC-22 and that containment.

### The cost of refusing to start, and how an operator recovers

v1 presented the refusal as costless. It is not, and the challenger is right that
the only deployment whose behaviour it can change is production.

The branch fires when a redirection is configured without a test-mode declaration.
In a test deployment the declaration is set, so it never fires there. So its only
reachable context is a production deployment that has acquired the variable —
from a shared environment file, from a platform that injects by prefix, or from a
copied template. The cost is that the next restart, which may be an unrelated
autoscale event long after the variable was introduced, is a full outage.

Refusing is still correct: a deployment that believes it is isolated and is not
hands out every user's calendar credential, and that is strictly worse than being
down. But the cost is named here rather than discovered, and so is the recovery:
**remove the calendar redirection variable from the environment and restart.** The
refusal message names that variable (AC-4, AC-12), which is what makes the recovery
a single obvious step rather than a search.

One known gap, which this work does not widen. The refusal is a fatal startup exit,
and the process's existing fatal startup exits do not reach the monitoring pipeline
either — `backend/main.go:26` defers the monitoring flush and a fatal exit does not
run deferred functions, which is already true of the existing required-configuration
failure at `:34`. So an operator sees this in the process's output and not in an
alert. That is a pre-existing property of every fatal startup path, it is identical
for the new one, and fixing it belongs to whoever owns startup observability rather
than to this spec. It is recorded because an on-call engineer reading AC-12 could
otherwise reasonably expect a page.

---

## V2-7. Explicitly out of scope (replaces v1 §5)

- **Redirecting sign-in and identity.** Leading with the cost/benefit, which is the
  stronger argument and the one the challenger is right that v1 buried: everything
  downstream of the sign-in callback is already exercised legitimately, because a
  validly signed session token *is* the session with no server-side store. What
  redirecting sign-in buys is exactly one scenario — the callback handler's own
  exchange, identity lookup, upsert and persist. That is a genuine coverage gap and
  a small one. It is also the authentication path, where a mistake is a bypass
  rather than a failing test, which is a reason to do it carefully and separately —
  not a reason to never do it. Separate issue, same shape.
- **Redirecting the second provider's API and the conferencing integration that
  shares it.** No disabled scenario names them and no journey exists that would use
  them. The moment the first such journey is written, it should be done in exactly
  this shape — including the connection rule, which is the part that generalises.
- **Making the provider choice work.** Filed as PAC-47. AC-17's narrowing is what
  keeps this spec from getting in its way.
- **Fixing the discarded errors on the free/busy path** (§V2-2.1). It is a real
  defect — a user is told an attendee's availability is unknown when the server in
  fact could not reach anything — and fixing it changes an observable response,
  which §V2-6 forbids here. **It should be filed as its own issue**, and it is the
  first thing the new suite will make visible. Until it is fixed, no criterion in
  this spec may rest on an error surfacing from that path, which is why AC-17 and
  AC-21 are worded as they are.
- **Making the clock drivable.** Still out of scope, with the reason corrected by
  §V2-2.4: it is a wide-blast-radius change for the engines that fall back to the
  package-level default, and a one-line assignment for the free/busy engine whose
  field is already exported. Neither is needed by AC-16 to AC-20.
- **Building the stand-in and enabling the scenarios.** They are the point, but they
  are test assets belonging to stages 3 and 5.
- **Fixing the defects the new scenarios will find.** Each is its own issue. The
  scenarios must assert what the product *should* do; if that means they fail on day
  one, that is the finding, and a scenario disabled for that reason names the issue
  that will re-enable it.
- **Any mechanism that makes redirection possible in a production build, or that
  lets a deployment widen the permitted address set.** A prohibition, not a
  deferral. §V2-3 fixes the set in the build for this reason.

---

## V2-8. Open questions and accepted gaps (replaces v1 §8)

### Open questions

**OQ-1 — how does the pinned provider client behave, in three parts?** *Owner:
stage 4, before any code is written. Settled by reading, not running.* v1 asked two
of these; the challenger identified the third, which determines the other two and
also determines finding 2. All three are facts about a pinned version:
`backend/go.mod:22` pins `google.golang.org/api v0.292.0` and `:26` pins
`cloud.google.com/go/auth v0.22.0` as an indirect dependency, both confirmed in
`backend/go.sum:184-185` and `:1-2`.

  - **(a)** Does the endpoint override accept a non-TLS scheme, or reject it?
  - **(b)** Does the credential transport attach a bearer credential over plain
    HTTP, or refuse?
  - **(c)** **Which credential stack does the token-source option select at
    v0.292.0** — the legacy `golang.org/x/oauth2` transport path, or the newer
    `cloud.google.com/go/auth` path that is now an indirect dependency? This is the
    question v1 did not ask. It determines (a) and (b); it determines whether the
    library's redirect behaviour re-applies the credential on a second hop, which is
    blocking finding 2; and it determines whether the connection guard and the
    redirection policy of §V2-3 can be installed **alongside** the library's own
    credential machinery or only **instead of** it. That last point is the one that
    can change this spec rather than only the plan: if supplying a client that
    carries the guards displaces the library's credential attachment, the redirected
    deployment attaches the credential itself, and §V2-3's "talked to exactly as the
    real service is" acquires a second exception that AC-21 must be written around.

  *What settles all three*: on a machine with registry access, fetch the module and
  **read three files at the pinned version** — the option definitions (`option/option.go`),
  the settings validation and credential-path selection (`internal/settings.go`), and
  the transport construction that turns a token source into a round tripper
  (`transport/http/dial.go`). That is reading, not running, and it answers (a), (b),
  (c) and the redirect layering together. A small empirical program answers (a) and
  (b) only, and is therefore the weaker check despite being the one v1 and the plan
  both proposed.

  **This cannot be settled in the environment this spec was written in, and I
  confirmed that rather than assuming it.** There is no `vendor/` directory under
  `backend/`; `go env GOMODCACHE` is `/root/go/pkg/mod`, whose download cache
  contains only `golang.org`; and a filesystem search for any `google.golang.org`
  source directory returns nothing. The registries are blocked. It must be settled
  **before** stage 4 starts.

  *Correction carried from the challenge*: v1's contingency for a negative answer —
  that the image would need a trust store — is false. It already has one
  (§V2-2.3). The contingent work is adding one certificate to an existing bundle.

**OQ-2 — is the permitted address set the right one, for the topologies that will
actually run this?** *Owner: contract-author, with whoever operates the stacks.*
§V2-3 fixes it as loopback plus the private ranges, excluding link-local and the
unspecified address. That covers a stand-in on the same host and a stand-in on an
isolated container network, which are the two topologies in evidence. It would
refuse a stand-in reachable only over a routable address — a shared test
environment, say. **If a topology needs that, the answer is not to widen the set or
make it configurable; it is to place the stand-in where the rule already allows.**
Recording this so that the first person who hits it does not resolve it by adding a
knob. v1's OQ-2 — "what is the permitted set of host strings" — is closed: there is
no permitted set of host strings.

**OQ-3 — should a redirected deployment advertise that fact over the API?** *Owner:
contract-author.* Unchanged from v1. It would let the suite assert the stack is in
the state it thinks it is. Against: a new response field for a test's benefit, on a
surface reachable without a session, and AC-14 plus AC-22 already prove redirection
is live. Recommendation: no.

**OQ-4 — one test-mode declaration for all external services, or one per service?**
*Owner: contract-author, decided now.* Unchanged from v1. Recommendation: a single
declaration that permits stand-ins at all, with each service's address configured
separately, so the declaration redirects nothing by itself (AC-2).

**OQ-5 — is the recording sink of AC-23 cheap enough to be worth it?** *Owner:
plan-author.* AC-23 asks for something that records connections where the current
arrangement has nothing listening. The cost is a small always-on listener in the
test stack. The benefit is that "nothing escaped" becomes evidence rather than an
inference from silence, and that the token-refresh residual risk in §V2-3 acquires a
proof. If the plan finds this disproportionate, the alternative is to keep the
closed port and downgrade AC-23 to asserting the mapping's presence — which is a
weaker criterion, and the trade should be made deliberately rather than by omission.
*(v1's OQ-5 asked how the scenarios avoid the fifteen-minute caches. It is closed
and demoted to AG-1 below.)*

**OQ-6 — is there any use for this outside the test suite?** *Owner: whoever runs
the product locally.* Unchanged from v1, and §V2-3's permitted set answers it better
than v1's rule did: a developer's own machine is loopback, which is permitted, so an
offline development mode would work without relaxing anything. If somebody wants
one, it costs nothing extra.

### Accepted gaps — stated as decisions, not questions

**AG-1 — the free/busy result cache stays uncovered, permanently, and that is a
decision.** There is exactly one fifteen-minute cache in scope (§V2-2.2), keyed per
user, attendee and date (`backend/engine/freebusy_service.go:98`). The clock cannot
be advanced from outside the process, so no scenario can observe the cache expiring
or being served from. Every scenario will use a distinct key, which the suite already
requires for other reasons. **That is isolation, not coverage**: it means the
scenarios do not interfere with each other, and it means the cache's own behaviour —
serving from memory within the window, refetching after it — is never exercised, on
the surface this spec argues is the least covered in the product. v1 filed this as an
open question with a "cheapest answer"; it is not a question, it is a gap being
accepted, and it will be closed only by a clock issue.

**AG-2 — the redirected request path is not byte-for-byte the production one.** See
§V2-3's exception and OQ-1(c). The guards exist only in a redirected deployment, so
what the suite exercises is production-plus-two-guards. This is unavoidable — a guard
installed in production would violate AC-1 — and it is the reason AC-1 and AC-3 assert
the production path's shape directly rather than inferring it.

**AG-3 — a hostile private network defeats this, by design.** The connection rule
confines the credential to addresses that are not routable on the public internet. It
does not distinguish the intended stand-in from anything else on that network. A test
host whose private network already contains something hostile is outside what this
control addresses, and no address-based control could address it. Stated so that
nobody reads §V2-3's guarantee as broader than it is.

**AG-4 — a fatal startup refusal does not reach monitoring.** §V2-6. Pre-existing for
every fatal startup path, not widened here, recorded so AC-12 is not misread as
producing an alert.

### Closed since v1

**v1 OQ-7 — does splitting off the provider-choice bug risk the suite locking it
in?** *Closed.* The challenger's answer is right and sharper than the question: the
five scenarios assert nothing about the stored provider, so they neither enshrine nor
obstruct PAC-47 — **except** on the free/busy path, whose fall-through branch is the
bug itself. A green criterion there is a test asserting that the fall-through answers
from the primary provider, which is correct for a primary-provider user and is the
exact line PAC-47 must change. AC-17 is therefore narrowed in its own text to the
primary-provider case. The question is answered; the narrowing is the answer.
