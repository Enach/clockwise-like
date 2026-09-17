# Spec — PAC-43: calendar-dependent behaviour must be verifiable before release

- **Linear**: https://linear.app/paceday/issue/PAC-43/add-a-base-url-seam-to-backendcalendar-so-external-calendar-providers
- **Status**: draft
- **Author**: spec-author
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

## 3. Desired behaviour

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

## 4. Acceptance criteria

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

## 6. What must not change

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

## 8. Open questions

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

## Challenge — 2026-09-17

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
