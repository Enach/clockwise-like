# Spec — PAC-83: Web client: 4-second abort falls back to preview data on slow but legitimate requests

- **Linear**: https://linear.app/paceday/issue/PAC-83/web-client-4-second-abort-falls-back-to-preview-data-on-slow-but
- **Status**: draft (challenged 2026-11-01, accept-with-changes); revision 2,
  2026-11-01: owner answer to OQ-1 applied (demo only in non-production
  builds, §3.3, D-11, AC-15c/AC-30); OQ-4 resolved by the contract
  (`contracts/features/PAC-83.md` §5)
- **Author**: spec-author
- **Inherited scope**: none. `docs/factory/api-audit.md` has no open finding on
  client timeouts or preview data. PAC-81 Part A recorded this as follow-up
  F-W1 / "WEB-timeouts" (`docs/specs/NEW-deploy-hardening.md:371-379`,
  `contracts/features/PAC-81-part-a.yaml:121`,
  `contracts/features/PAC-81-part-a.md:322-330`), and
  `docs/operator/proxy.md:112-113` tells operators the web aborts after 4 s
  "regardless": that sentence must be corrected when this ships.
- **Repos**: the product change is web only (Enach/smart-calendar-flow). No
  server behaviour change; the contract gains one description sentence on
  the shared budget 503 (revision 2, `contracts/features/PAC-83.md` §2). Enach/clockwise-like changes too, but only in
  test and docs: the e2e demo-data guard (`e2e/fixtures.ts:250-261`, §2.6),
  the e2e scenarios of AC-5/AC-10/AC-24, and the `docs/operator/proxy.md`
  sentence.
- **Code surveyed**: smart-calendar-flow `origin/main` @ `8966115`;
  clockwise-like `origin/main` @ `953f96e` (challenge, 2026-11-01).

## 1. Problem

Every call the web app makes to the Paceday server is cut off by the browser
after 4 seconds. A call that is cut off — or that fails at the network level,
or that receives a non-JSON answer — is treated as "the server is not there",
and from that moment the app silently switches the whole browser tab to
**invented data**: a fake calendar, fake teams, fake booking links, fake slots,
and fake success for writes ("event created", "booking link saved", "calendar
synced") that may or may not have happened on the server.

Legitimate server work routinely takes longer than 4 s. Since PAC-81 the server
gives every request up to 90 s and answers with its own error when that runs
out. Natural-language scheduling makes up to two AI calls of up to 30 s each;
collective slot listing and team availability read every host's calendar;
personal-calendar sync, schedule suggestions and focus runs call calendar
providers. On a slow day all of these exceed 4 s.

Who pays:

- **A signed-in user** sees a plausible but invented calendar or team, may act
  on it, and is told writes succeeded when they did not (or did, and then
  repeats them). On several pages nothing says the data is invented.
- **An anonymous booker** on a public booking page is told "Link not found"
  for a link that exists, or "no available times" when there are times — and
  because the switch is remembered for the whole tab, reloading does not help.
- **A booker whose booking is slow to save** is told "we can't reach the
  scheduling service, try again" for a booking that may already be saved;
  trying again can produce a second booking or a confusing conflict.
- **Someone signing in while the server is slow** is silently dropped into the
  demo instead of being signed in.

The server's own answers for these cases (its "request timed out" error, its
"request body too large" error) never reach the user today, because the
browser has already given up.

## 2. Current behaviour

### 2.1 The client wrapper

- Every call through `requestApi` (`src/api/client.ts:583-631`) arms a 4 s
  abort: `NETWORK_TIMEOUT_MS = 4000` (`:318`), `setTimeout(() => ctrl.abort(), …)`
  (`:590-591`). No caller can pass a different deadline or its own abort
  signal (no other `signal` use in `src/api`).
- A fetch rejection — the 4 s abort, a network error, a DNS failure — becomes
  `ApiUnreachableError` (`:600-602`), except when the page is being left
  (`pagehide`), which becomes `ApiRequestCancelledError` (`:384-399`, `:601`).
- A 2xx answer that is not JSON (e.g. the SPA `index.html`) also becomes
  `ApiUnreachableError("Non-JSON response")` (`:618-625`).
- Any HTTP error status becomes `ApiHttpError` and is never replaced by mock
  data (`:320-335`). `apiErrorMessage` (`:556-581`) shows the server's
  `error`/`message`/`detail` if present, so the server's
  `{"error":"request timed out"}` would be shown verbatim as
  "request timed out"; `{"error":"request body too large"}` likewise. For
  `ApiUnreachableError` it says "Backend unreachable — showing preview data."
  (`:557`) — even for calls that have no preview fallback.

### 2.2 Automatic preview ("mock") mode

- `withFallback` (`src/api/client.ts:784-797`): if the tab is already in mock
  mode it returns the mock **without trying the server** (`:785`); otherwise
  on `ApiUnreachableError` it switches mock mode on (`:792`) and returns the
  mock.
- Mock mode is stored in `sessionStorage` (`"clockwise:mock-mode"`, `:63-88`),
  so it survives reloads for the life of the tab.
- It is switched off only by a successful `withFallback` call (impossible
  while it is on, see `:785`) or by the health probe `api.health`
  (`:1234-1250`), which itself uses the 4 s abort and turns mock mode **on**
  when the probe fails at the network level (`:1245`).
- The health probe runs every 30 s only where `MockBanner` is mounted
  (`src/components/MockBanner.tsx:8`, `src/hooks/useHealthPing.ts:20-45`):
  Dashboard (`src/pages/Dashboard.tsx:354`), Settings
  (`src/pages/Settings.tsx:239`) and Audit (`src/pages/Audit.tsx:26`). The
  banner reads "Backend not reachable — showing demo data." On every other
  page (links, team, manager, public booking, team invite) mock data is shown
  **with no banner** and nothing ever switches mock mode off.
- The manager API has its own `withFallback` (`src/api/manager.ts:12-15`) that
  never switches mock mode on, but returns mock data whenever the tab is
  already in mock mode.
- Outside `withFallback`, `ConferencingSection.handleZoomConnect`
  (`src/components/ConferencingSection.tsx:101-113`) checks `isUsingMocks()`
  and, when it is on, calls `mockZoomConnect()` and toasts "Zoom connected
  (demo)" instead of starting the real OAuth round-trip. So a signed-in user
  whose tab fell into mock mode on another page is told Zoom is connected.
- The comment at `src/api/client.ts:59-61` states the purpose: a fallback
  "when the Go backend at /api is not reachable (e.g. inside the Lovable
  preview)". There is no other detection of the Lovable preview.

### 2.3 Demo session

- A separate flag `paceday:demo` (`DEMO_SESSION_KEY`, `src/api/client.ts:90-103`)
  marks a demo session; `isDemoSession()` is "never switched on by an
  unreachable backend" according to its comment.
- In fact the only way into a demo session is `loginDemo`
  (`src/contexts/AuthContext.tsx:107-118`), called by `fallbackDemo` in the
  sign-in dialog (`src/components/auth/AuthDialog.tsx:65-70`), which runs
  **silently** when: the 2 s health check before a Google/Microsoft sign-in
  fails (`:72-78`, `src/lib/api.ts:28-48`), the sign-in email discovery call
  fails at the network level (`:114-117`, comment "silent demo fallback per
  T-26"), or password sign-in/register fails at the network level
  (`:314-318`, `:151`). There is no explicit "try the demo" control; the demo
  is left through "Exit demo" (`src/pages/Index.tsx:57-72`).
- `loginDemo` also switches mock mode on (`AuthContext.tsx:109`), and a demo
  restored on mount does too (`:81-89`). The demo's data therefore comes from
  the **same** mock-mode switch as the automatic fallback: the demo does not
  work without it today.
- The demo label: `DemoBanner` (`src/components/DemoBanner.tsx`) says "Demo
  mode — backend is offline. Data is simulated…", can be dismissed for the
  tab (`paceday:demo-banner-dismissed`), and is mounted only on Dashboard
  (`src/pages/Dashboard.tsx:353`) and Links (`src/pages/Links.tsx:387`). The
  Navbar shows the name "Demo" and "Exit demo" (`src/components/Navbar.tsx:55,152`).
  `ScheduleSuggestModal` toasts "Changes are local" in a demo
  (`src/components/ScheduleSuggestModal.tsx:126`).

### 2.4 Operation survey

Timeout for every row: **4 s** (the `requestApi` abort), except where noted.
"Invented data?" = can a real signed-in user (or an anonymous booker on the
public booking page) be shown made-up data or a made-up outcome after a
timeout / network error / non-JSON answer, without being in a demo session.
"Why > 4 s" names server work that legitimately exceeds 4 s (all requests are
bounded by the server's 90 s budget).

#### Calendar, scheduling, settings — `src/api/client.ts` (global `withFallback`)

| Operation | Line | Fallback on unreachable | Invented data? | Why > 4 s |
|---|---|---|---|---|
| Health probe | 1234 | sets mock mode on, returns fake "ok" | yes — it is what turns mock mode on for the tab | DB pool wait under load |
| Free/busy | 1261 | mock coverage heuristic | yes | one provider read per attendee |
| Calendar connection status | 1266 | mock auth state | yes | — |
| Integration availability | 1271 | every provider "available" | yes (offers sign-ins that are not configured) | — |
| Calendar disconnect | 1285 | mock success | yes (fake success) | provider revocation call |
| Read settings | 1295 | default settings (`DEFAULT_SETTINGS`, `:107-149`) | yes | — |
| Save settings | 1300 | mock success | yes (fake save) | — |
| Calendar events (week) | 1319 | seeded mock events | yes | provider read |
| Focus run | 1338 | mock focus blocks "created" | yes (fake write) | provider reads + writes per day |
| Focus blocks | 1395 | mock blocks | yes | — |
| Clear focus blocks | 1408 | mock success | yes (fake delete) | provider deletes |
| Schedule suggest | 1433 | mock slots | yes | provider reads per attendee |
| Schedule create | 1450 | mock event "created" | yes (fake create; real one may exist) | provider write + invites |
| Compression preview | 1474 | empty list | yes ("nothing to compress") | provider reads (no UI caller today, API-016) |
| Compression apply | 1479 | every proposal "applied" | yes (fake write) | provider writes |
| Natural-language parse | 1492 | regex mock parse + mock slots | yes | up to 2 AI calls × 30 s + suggest |
| Natural-language confirm | 1530 | mock event "created" | yes (fake create) | provider write |
| Activity log | 1552 | mock audit entries | yes | — |
| Personal calendars list | 1563 | mock calendars | yes | — |
| Add personal calendar | 1571 | mock success | yes (fake add) | URL fetch/validation |
| Update personal calendar | 1597 | mock success | yes | — |
| Delete personal calendar | 1612 | mock success | yes | — |
| Personal calendar sync | 1620 | "synced now" | yes (fake sync) | feed fetch + up to many event writes |
| AI provider test | 1633 | mock probe (`:1011`) | yes (fake "reached provider") | one AI call (≤ 30 s) |
| Edit event | 1648 | mock success | yes (fake edit) | provider write + invites |
| Delete event | 1669 | mock success | yes (fake delete) | provider delete |
| Room search | 1680 | `MOCK_ROOMS` (`:154`) | yes | directory read |
| Attendee suggestions | 1713 | `MOCK_DIRECTORY` (`:164`) | yes (fake people) | directory read |
| Add conference link | 1728 | random Meet/Zoom/Teams URL | yes (a fake, unusable meeting link) | provider call |
| Remove conference link | 1745 | mock success | yes | provider call |
| Conference providers | 1755 | mock statuses | yes | — |
| Zoom disconnect | 1785 | "revocation confirmed" | yes (fake confirmed revocation, contradicts PAC-67) | Zoom revoke call |
| Microsoft disconnect | 1794 | "not offered" | yes | — |
| SSO step-up options | 466-470 | none (error) | no — but message says "showing preview data" (`:557`) | — |
| Organisation AI read / write / choose / consent | 481-549 | none (error) | no — same misleading message | step-up checks |

#### Booking links — `src/api/schedulingLinks.ts` (global `withFallback` unless noted)

| Operation | Line | Fallback on unreachable | Invented data? | Why > 4 s |
|---|---|---|---|---|
| List my links | 488 | mock links (seeded `:208-298`) | yes, no banner | — |
| Co-host invites list / accept / decline | 504, 521, 529 | mock list / mock success | yes | — |
| Create link (+ add hosts, re-read) | 543 | mock link "created" | yes (fake create) | several sequential calls |
| Update link | 583 | mock success | yes | several sequential calls |
| Delete / leave link | 644, 652 | mock success | yes | — |
| Bookings on a link | 661 | mock bookings | yes | — |
| **Public: link info** (anonymous booker) | 697 | mock lookup by slug (`:321-327`): a real slug → "Link not found"; a slug equal to a seeded demo slug (`intro-chat`, `product-deepdive`, `design-review`) → an invented link | **yes** — and the mock flag persists for the tab and no health probe runs on this page, so reloading keeps saying "Link not found" (`src/pages/PublicBooking.tsx:208-233`) | DB pool wait under load |
| **Public: slots for a date** (anonymous booker) | 738 | mock slot generator; for a real slug → empty list, shown as no times | **yes** (false "no availability") | one calendar read per host (collective), up to the 90 s budget; 503 on budget expiry |
| Public: slots without a date | 735-737 | short-circuits to empty, no request | no (never calls the server; PAC-72's date-less listing is not used by the web) | — |
| **Public: create booking** | 767 (`demoOrReal`, `:477-479`) | none outside a demo session; error | no invented data, **but** after a 4 s abort the booker is told "We can't reach the scheduling service. Check your connection and try again." (`PublicBooking.tsx:197-198`) while the booking may be saved (the server answers 201 or 500 once the insert is sent, never its 503) | conflict check over every host's calendar, insert, then host events |

The public page uses `retry: false` for its queries and the booking mutation
(`PublicBooking.tsx:115,144,175`); the app-wide default is also
`retry: false` for queries (`src/App.tsx:22-26`) and react-query does not
retry mutations by default. So nothing retries automatically today — keep it.

#### Teams — `src/api/teams.ts` (global `withFallback`)

| Operation | Line | Fallback | Invented data? |
|---|---|---|---|
| List teams (+ one detail per team) | 299 | local-storage mock teams | yes, no banner |
| Team detail | 318 | mock team | yes |
| Create / rename / delete team | 324, 341, 351 | mock success | yes (fake writes) |
| Invite / remove member | 363, 381 | mock success | yes |
| No-meeting zones add / update / delete | 394, 414, 435 | mock success | yes |
| **Find a time** (team availability) | 441 | mock slots | yes — and this is a several-host read that legitimately exceeds 4 s |
| Team analytics | 458 | mock analytics | yes |
| Team invite preview / accept | 207-213 | none (error) | no, but misleading "showing preview data" message |

#### Manager — `src/api/manager.ts` (local `withFallback`, `:12-15`)

Detect (369), profile read/write (382, 397), roster (411), add/update/remove
member (430, 449, 469), confirm detection (482), 1:1 gaps (495), analytics
(510), prefill URL (533): no fallback on their own failure (error), **but**
mock data whenever the tab is already in mock mode — which another page's
timeout can cause. Invented data: yes, indirectly.

#### Outside `requestApi` — `src/lib/api.ts`

- `checkApiAvailability` (`:28-48`): 2 s abort, result cached for the page;
  false → silent demo (§2.3).
- `apiFetch` (`:64-95`): **no timeout**; used for `/auth/me`
  (`AuthContext.tsx:57`), sign-in discovery, password login/register, logout.
  A network failure in sign-in → silent demo (§2.3). A network failure on
  `/auth/me` keeps the last user and marks the session stale
  (`AuthContext.tsx:63-77`) — correct, no invented data.

### 2.5 Tests that pin today's behaviour

These assert the automatic fallback and will have to be inverted, not deleted:
`src/api/errors.test.ts:51` ("falls back to preview data only when the backend
is unreachable"), `:61`, `:66`, `:84`, `:90`; `src/api/pageLeave.test.ts:58`;
`src/api/audit.test.ts:83`; `src/api/integrationAvailability.test.ts:55`;
`src/api/managerTeam.test.ts:206-231`; `src/api/credentialLifecycle.test.ts:55`
(preview answers without a request). No test exercises a request slower than
4 s, a 503 budget answer, or a 413 in the web. The e2e suite (in
clockwise-like) has a public booking journey (`e2e/tests/public-booking.spec.ts`)
with no slow-server case, and no way to make the stack slow: the calendar
stand-in `e2e/fake-google` has a control plane (`fixture.go:20-113`: tokens,
calendars, events, request log, redirect) but no latency or drop control.

### 2.6 Other consumers of the automatic fallback (challenge, 2026-11-01)

- **The e2e demo-data guard** (clockwise-like). `assertNotDemo`
  (`e2e/fixtures.ts:250-261`), which every authenticated UI test calls, proves
  the test talked to the real backend by asserting the exact sentence
  "Backend not reachable — showing demo data" is absent, plus the manager
  seed names. `e2e/README.md:234-237,272` and `e2e/TESTIDS-REQUIRED.md:111`
  describe it as the suite's most important anti-false-pass check. Once
  this spec changes that sentence, the guard passes vacuously — silently
  disarmed — unless it is updated in the same change.
- **Lovable agent instructions** (web repo). `AGENTS.md:13` ("Preserve
  explicit mock/preview fallback behavior"), `docs/lovable-prompts/00-global-context.md:13`,
  `01-real-api-first.md:14,26`, `05-conferencing.md:19`, `07-manager-team.md:36`,
  `08-final-qa.md:30` and `09-current-issues-and-scheduling-preferences.md:94`
  tell the Lovable agent, which edits this repo, to keep the unreachable-
  backend fallback. Left as is, they direct a later Lovable edit to put it
  back.
- **MCP server** (clockwise-like `mcp/`): its backend client
  (`mcp/client.go:19`) has no client timeout and no mock fallback. Not
  affected; it relies on the server's own bounds.

### 2.7 The server-side bounds, as built (challenge, 2026-11-01)

Verified on clockwise-like `origin/main` @ `953f96e`:

- **Handler budget 90 s** (`backend/internal/budget/budget.go:32`, applied to
  every `/api` route at `backend/api/routes.go:16,40`). When it runs out
  before the handler has started its answer, the server sends
  `503 {"error":"request timed out"}` (`budget.go:44-51`), contract
  `RequestBudgetExpired` (`contracts/openapi/openapi.yaml:14561-14608`).
- **Write backstop 120 s** (`backend/internal/lifecycle/lifecycle.go:207`,
  `http.Server.WriteTimeout`). This is **not an answer**: a response written
  after it is lost and the connection is closed when the handler returns.
- **nginx 130 s** (`nginx/nginx.conf:66-67`, `proxy_read_timeout`/
  `proxy_send_timeout`). If the backend has sent nothing by then, nginx
  answers **504 with its own HTML page**; a backend connection closed
  without an answer becomes nginx's **502 HTML**. nginx's own 413 is JSON
  (`nginx.conf:108-116`).
- **The one request the budget does not cut: booking create after commit.**
  `ConfirmBooking` commits just before the insert (`backend/engine/booking.go:396`);
  from then on the middleware waits for the handler (`budget.go:207-212`),
  the insert runs detached (`booking.go:400`) with no time bound of its own,
  and the handler answers 201 or 500 (`backend/api/handlers_booking.go:393-412`).
  So a booking can answer 201 after 90 s; an insert slower than ~120 s yields
  no backend answer and the browser gets nginx's 502/504 HTML. Before commit
  a client disconnect cancels the request and nothing is saved
  (`budget.go:199-224`, `booking.go:396-398`).
- **Other 503s** exist with JSON bodies ("Zoom OAuth not configured",
  "Microsoft OAuth not configured", "… step-up not configured", Slack,
  Notion), and an operator's proxy can answer 502/503/504 (or Cloudflare
  52x) with HTML. Status 503 alone therefore does not identify the budget
  answer; the body does (`RequestBudgetExpiredError` is already generated in
  the web: `src/api/generated/schemas.ts:403-404`).
- **Operator proxies.** `docs/operator/proxy.md:93-110` requires the proxy's
  read timeout to exceed 130 s, names AWS ALB's default 60 s and
  Cloudflare's 100 s as too short. No doc recommends Cloudflare.

## 3. Desired behaviour

### 3.1 The client waits as long as the server may legitimately work

The server answers every request it has not committed to within its 90 s
budget; behind that, its 120 s write backstop silently drops late answers and
nginx gives up at 130 s with its own 502/504 page (§2.7). The browser must
never give up before the last hop Paceday ships can answer. The app waits for
every call — including the sign-in, sign-out and "who am I" calls that do not
go through `requestApi` — at least until nginx's 130 s bound has passed, so
whatever the stack says — success, "request timed out", a gateway error, any
other error — reaches the user. Only when that longer bound passes with no
answer does the app conclude the connection is lost. Today's unbounded calls
(`apiFetch`, §2.4) get the same bound, so a dead connection does not spin
forever.

The one exception is the background "is the server there?" probe, which is not
a user action: it may give up quickly, and its failure only changes a status
indicator (§3.4).

### 3.2 Waiting is visible

Every user-started action shows that it is working from the moment it starts
(disabled control and a busy indicator) — most already do. If an action is
still running after a few seconds, the user is told it is still working and
that it can take up to about a minute and a half. Natural-language scheduling
says from the start that it is asking the AI and may take up to a minute.
Background reads (calendar week, lists) keep their existing loading states
and never replace content with invented content.

### 3.3 Invented data only in an explicit demo

Made-up data is shown only in a demo session that the person chose knowingly,
from a control that says it is a demo, and it is labelled as a demo on every
page while it lasts. Nothing else — a timeout, a network error, a non-JSON
answer, a failed health probe, a failed sign-in attempt — ever starts a demo
or shows made-up data. A real signed-in user and an anonymous booker never see
made-up data, made-up empty results, or made-up success.

A tab that carries the old "preview mode" flag from an earlier version of the
app behaves as a normal tab: the leftover flag is ignored and cleared.

**The demo exists only in non-production builds (owner, 2026-11-01, OQ-1
(c)).** Whether demo data is served is decided by the demo session alone
(today's `paceday:demo` flag), never by a separate "mock mode" that other
code can switch on. There is exactly one way in: one explicit entry control
on the sign-in screen, labelled as a demo with made-up data ("Explore a demo
— made-up data"). The demo label is shown on every page of the signed-in app
while the demo lasts, cannot be dismissed, and says the data is a demo — it
never says the server is offline (today's `DemoBanner` text does). The Zoom
"connect" in a demo stays local (`mockZoomConnect`); outside a demo it always
starts the real OAuth hand-off.

*Which builds are non-production.* The web is built by Vite; a build's
**mode** decides. A build in mode `production` — `vite build` / `npm run
build`, which is what the shipped Paceday web image runs
(clockwise-like `docker/frontend.dockerfile`, also used by the e2e stack),
and what a Lovable "publish" runs — is a production build. Every other mode
is non-production: the Vite dev server (`vite`, mode `development`, which
is what the Lovable preview runs), `vite build --mode development`
(`npm run build:dev`) and the unit-test runner (mode `test`). No runtime
setting, environment variable, URL parameter or storage flag can turn the
demo on in a production build: the decision is taken when the bundle is
built, and a production bundle does not contain the demo at all — not its
entry control, not its label, not its data (seeded events, links, teams,
manager roster, rooms, directory), not the code that sets the demo session.
This is checked on the built files, not on the source (AC-30). The exact
build-time interface (flag name, module layout, the check) is the
contract's (`contracts/features/PAC-83.md` §4).

*The entry control is never reached after an error.* It is shown on the
sign-in screen's initial view only. No error — a failed sign-in, an
unreachable or slow server, a failed health check — starts the demo,
offers it, links to it or mentions it; the sign-in dialog's error view does
not contain the entry control. The person reaches it only by choosing it on
the initial view of a non-production build.

The repository's Lovable agent instructions (§2.6) are updated in the same
change so they no longer ask for an automatic fallback.

### 3.4 What the user is told instead

Failures are shown where the action happened, with a way to try again for
reads, and with an honest statement of the outcome for writes:

| Situation | Reads | Writes |
|---|---|---|
| Server says the request ran out of time: status 503 **and** a body matching `RequestBudgetExpiredError` (`{"error":"request timed out"}`); a 503 with any other body is not this row | "This took too long and was stopped. Try again." + retry | Same wording; plus, for every write except booking create (the only operation whose contract says the 503 means nothing was written, §2.7), "Refresh to check whether it was applied." |
| Gateway or proxy error: any 5xx whose body is not JSON (nginx 502/504, an operator proxy's 502/503/504/52x page) | same as the "can't reach" row below | same as the "didn't confirm" row below |
| Server says the request is too large (413) | n/a | "This is too large to send" naming what to shorten (e.g. the request text); no retry button (the same request would fail again) |
| No answer within the client's bound, network failure, or a non-JSON answer | "Can't reach Paceday right now. Check your connection and try again." + retry; content already on screen stays, marked as possibly out of date | "Paceday didn't confirm this. It may or may not have been applied — refresh to check before trying again." |
| Any other HTTP error | today's mapping (unchanged) | today's mapping (unchanged) |
| The page is being left (navigation, tab closed) | nothing shown, nothing recorded | nothing shown, nothing recorded |

The persistent banner on Dashboard/Settings/Audit stops saying "showing demo
data" for an unreachable server; it says the server can't be reached and that
what is on screen may be out of date, with a retry. No message anywhere says
"showing preview data" outside a demo.

Sign-in when the server can't be reached shows an error in the sign-in dialog
with a retry, and does not start the demo. A server that is merely slow does
not block sign-in: the check made before a Google/Microsoft sign-in (today a
2 s probe, §2.4) may not refuse a sign-in because the server took longer than
2 s; it uses at least the background probe's bound (D-2).

### 3.5 Booking is never retried for the booker, and a lost answer is honest

- The app never sends a booking twice on its own — not on timeout, not on
  network error, not on reconnect.
- If the booking answer is lost (client bound reached, network failure,
  non-JSON answer, a gateway/proxy error page of any 5xx status, including an
  HTML 503) or the server answers its generic server error (which, per
  PAC-81, does not prove nothing was saved), the booker is told: "We
  couldn't confirm your booking. It may have been saved — check your email
  for a confirmation before booking again." The chosen time and typed details
  stay on screen. The app refreshes the times for that day so the booker can
  see whether the slot is gone; it does not offer a one-click "book again".
- If the server answers its budget 503 for a booking — status 503 with the
  `RequestBudgetExpiredError` body, which per the contract
  (`contracts/openapi/openapi.yaml:2833,14601-14603`) proves nothing was saved — the booker is told
  the booking was not made because the service was too slow, and may try
  again by pressing the button themselves. Only that exact answer says "not
  made"; every other 5xx says "may have been saved".
- PAC-82 (server idempotency, not built) is expected to make a repeated
  booking request safe. Until it ships, this spec adds nothing the client
  would have to send for it, and the "may have been saved" path offers no
  one-click resend. PAC-82's spec owns revisiting this wording (e.g. a safe
  "try again") once a repeat cannot create a second booking.
- The public page never shows "Link not found" or "no available times" unless
  the server said so.

## 4. Acceptance criteria

Test levels: `[unit]` = vitest in the web repo (component/API-layer tests
with a controllable fake server and fake timers); `[e2e]` = Playwright
against the compose stack in this repo, with timing and network failures
controlled at the browser by request interception (OQ-4, resolved). `[contract]` is not used: no interface changes.

**Waiting long enough**

AC-1. Given a signed-in user and a server that answers a calendar-week read
after 10 s, when the dashboard loads, then the real events are shown, no
invented events are shown at any time, and the tab is not marked as being in
preview/demo mode.  [unit]

AC-2. Given a signed-in user and a server that answers the natural-language
parse after 65 s (two slow AI calls), when the user submits a request, then a
progress message mentioning the AI is visible from the start until the answer,
and the real suggestions are shown afterwards.  [unit]

AC-3. Given any user-started call (each row of §2.4 whose operation a user
starts), and the sign-in, sign-out and "who am I" calls, when the server has
not answered after 131 s, then the call is still waiting (the client has not
given up); and when it has not answered after the client's bound (D-1, longer
than nginx's 130 s), then the "can't reach" outcome of §3.4 is shown (for
booking create: the "may have been saved" outcome of §3.5). Tested per
operation family (read, write, booking create, `apiFetch`) with fake
timers.  [unit]

AC-4. Given a call that is still running after the "still working" threshold
(factory-assumed 8 s, D-3), then the "still working, may take up to about a
minute and a half" message is visible; before the threshold it is not.  [unit]

AC-5. Given a public booking link with two hosts and a server that lists
slots after 20 s, when a booker picks a date, then the real slots are shown
and no invented slots or "no available times" message appear meanwhile.
[e2e]

AC-6. Given a team owner on "Find a time" and a server that answers after
20 s, then the real slots are shown, never mock slots.  [unit]

**Never invented data**

AC-7. Given a signed-in user (not in a demo), for every operation in §2.4
that has a mock fallback today, when the call fails at the network level, or
gets no answer within the client bound, or gets a non-JSON 2xx answer, then
the operation's caller receives an error — never mock data or mock success —
and a second call afterwards goes to the server (no tab-wide switch). One
table-driven test over every such operation, whose table is derived from the
API modules' exported operations (not a hand-kept list), so an operation added
later with a mock path outside a demo is covered and fails it.  [unit]

AC-8. Given the health probe fails at the network level, then no data call
afterwards returns mock data, and the banner says the server can't be reached
(no mention of demo/preview data).  [unit]

AC-9. Given a tab whose session storage carries the old preview-mode flag
set and no demo session, when the app loads, then calls go to the server and
the flag is cleared.  [unit]

AC-10. Given an anonymous booker on a real booking link and a server whose
link-info read fails at the network level once, then the page says the
service can't be reached with a "Try again" control (never "Link not found"),
and pressing it, with the server now answering, shows the real link; a reload
in the same tab also shows the real link.  [unit] and [e2e]

AC-11. Given an anonymous booker whose slot listing fails at the network
level, then the page shows the "can't reach" message with a retry, never an
empty list of times and never "no available times".  [unit]

AC-12. Given a booker on a link whose slug equals one of the demo seed slugs
(e.g. `intro-chat`), when the server is unreachable, then no demo link or demo
slots are shown.  [unit]

AC-13. Given a manager-page call in a tab where another page's call has just
failed at the network level, then the manager call goes to the server (no
mock data).  [unit]

**Demo only when chosen**

AC-14. Given the sign-in dialog and a server that cannot be reached (health
check, discovery and password sign-in each tested), when the user tries to
sign in, then an error with a retry is shown in the dialog and no demo session
starts. And given a server whose pre-sign-in check answers after 3 s, the
Google/Microsoft sign-in proceeds (no error, no demo).  [unit]

AC-15. Given a non-production build and the person explicitly starts the
demo from its entry control, then demo data is shown, every signed-in page shows a "demo" label that has
no dismiss control and does not say the server is offline, and no request
reaches the server for demo data; leaving the demo returns to the signed-out
state with no demo data left in the tab.  [unit]

AC-15b. Given no demo session, the demo data path is unreachable: with the
old mock-mode flag forced on in session storage, and after any failure of
AC-7, no operation returns demo data and "Connect Zoom" starts the real
OAuth hand-off (never "Zoom connected (demo)"). And the only code path that
sets the demo session is the single entry control of §3.3 (a unit test
renders the sign-in dialog under every failure of AC-14 and finds the demo
session flag unset).  [unit]

AC-15c. Given a non-production build and the sign-in dialog showing the
error of any AC-14 failure, then the error view contains no demo entry
control and no text mentioning a demo; the entry control is present on the
initial view.  [unit]

AC-30. Given the web built in production mode (`npm run build`), then the
built files contain no demo entry control, no demo label, no demo data and
no code that sets the demo session (checked by searching the built bundle
for the markers the contract pins), and the sign-in screen served by the
e2e stack (which runs the production image) shows no demo entry control.
Given the same source built in a non-production mode, the same search finds
the markers (so the check cannot pass vacuously).  [build check in the web
gate] and [e2e]

**Server answers are shown**

AC-16. Given the server answers `503 {"error":"request timed out"}` to a read,
then the user sees the §3.4 "took too long" message with a retry (not the raw
text "request timed out", not "showing preview data").  [unit]

AC-17. Given the server answers the budget 503 to a non-booking write, then
the user sees "took too long" and, unless that operation's contract says
nothing was written, the "refresh to check" sentence; no success toast.
[unit]

AC-18. Given the server answers `413 {"error":"request body too large"}`,
then the user sees the "too large" message and no retry control for that
submission.  [unit]

AC-19. Given a write whose answer is lost (client bound, network failure,
non-JSON answer, or a 5xx with a non-JSON body such as nginx's 502/504 page
or a proxy's HTML 503), then no success is shown and the "may or may not have been
applied — refresh to check" message is shown; the client does not resend it.
[unit]

AC-20. Given any request rejected because the page is being left, then no
error message is shown, nothing is retried, and no state changes (today's
`pagehide` behaviour kept).  [unit]

**Booking create**

AC-21. Given a booker submits a booking and the server's answer is lost
(client bound reached, network failure or non-JSON answer), then exactly one
booking request was sent, the page says the booking may have been saved and
to check email before booking again, the chosen time and typed details are
kept, the day's times are re-read, and no control re-sends the booking in one
click from that message.  [unit]

AC-22. Given the server answers the booking with its generic 500, or with
any 5xx whose body is not JSON (502/504 HTML from nginx, a proxy's HTML 503
or 524), then the same "may have been saved" message as AC-21 is shown.
[unit]

AC-22b. Given the booking is answered with a 503 whose JSON body is not
`RequestBudgetExpiredError` (e.g. `{"error":"service unavailable"}`), then the
page does not say "the booking was not made"; it shows the "may have been
saved" message.  [unit]

AC-23. Given the server answers the booking with the budget 503, then the
page says the booking was not made and the booker may try again; exactly one
request was sent until the booker presses the button again.  [unit]

AC-24. Given a slow booking (server answers 201 after 30 s), then the booker
sees the confirmation and exactly one booking exists on the server.  [e2e]

AC-25. Given the booking request is sent and the network connection drops
before the answer, then the client sends no second booking request at any
later time (reconnect, focus, timer).  [unit]

**Messages**

AC-26. No user-visible string says "preview data" or "demo data" outside a
demo session; checked by rendering every error state of AC-7..AC-19.  [unit]

AC-27. Given a read answered with nginx's 504 HTML page (or a 502 HTML page),
then the user sees the "can't reach" message with a retry, not "The server
had a problem" and no raw HTML.  [unit]

**Guards outside the product code**

AC-28. In clockwise-like, `assertNotDemo` (`e2e/fixtures.ts`) fails when the
web shows its demo label or its "can't reach" banner (by stable test id, not
an English sentence), and still fails on the manager seed names. Shown by a
guard self-test that renders each state and expects the guard to fail.
[e2e]

AC-29. `AGENTS.md` and `docs/lovable-prompts/*` in the web repo contain no
instruction to fall back to mock/preview data when the backend is
unreachable (a grep for "fallback" in those files finds only the explicit-demo
wording).  [doc check in the PR]

## 5. Explicitly out of scope

- **Server-side idempotency of booking create (PAC-82).** This spec stops the
  client from causing duplicates; making a repeated booking request safe is
  PAC-82's.
- **Changing any server budget or limit**, or the server's 503/413 bodies
  (PAC-81, merged).
- **A cancel button for long calls.** Leaving the page still cancels them.
  A user-facing cancel would need the server to stop work on cancel, which is
  not guaranteed; can be a follow-up.
- **Streaming progress from the server** (e.g. "first AI call done"). The
  progress shown is client-side elapsed-time feedback only.
- **Retry policies for reads beyond a manual "Try again".** No automatic
  read retries are added (today there are none).
- **Room data fidelity (API-040/041)** — the real rooms list lacks fields the
  mock had; removing the mock fallback makes that visible but fixing it is
  those findings' job.
- **Correcting `docs/operator/proxy.md:112-113`** is in scope only as a
  one-sentence doc fix in this repo's PR when the web change ships (it is
  stale the moment it does); no other operator doc changes.
- **The MCP server's backend client** (`mcp/client.go:19`, no timeout, no
  fallback): unaffected (§2.6).
- **Supporting proxies with read timeouts under 130 s** (Cloudflare's 100 s,
  ALB's default 60 s): unsupported per `docs/operator/proxy.md`; D-10.

## 6. What must not change

- HTTP errors are never replaced by invented data (today's rule,
  `client.ts:320-335`; `errors.test.ts:77` status mapping; PAC-35 disconnect
  409 test `pac35AuthFlows.test.ts:47`).
- A request aborted because the page is being left never changes state
  (`pageLeave.test.ts:20-49`, NEW-e2e-pac55-ac10).
- The public booking page's handling of 400/409/410/422/429
  (`PublicBooking.tsx:183-196`) and its rate-limit wording — no test names
  besides `PublicBooking.test.tsx`; keep it green.
- A network failure on the signed-in user check keeps the last known user and
  marks the session stale (`AuthContext.tsx:63-77`); none of this spec's
  changes signs a user out.
- No automatic retries: app default `retry: false` (`App.tsx:24`) and
  `retry: false` on the booking page.
- The demo, in non-production builds (D-11), keeps its current data and seeded content; only
  its label (§3.3) and its entry change.
- Tests listed in §2.5 are rewritten to the new rule (invert, don't delete);
  their pagehide and HTTP-error halves stay as they are.

## 7. Rollback

Web-only change, no migration; the contract change is one description
sentence (no shape change), kept on revert. Reverting the web PR
restores the 4 s abort and the automatic preview fallback exactly as today.
Nothing is stored server-side; the only client-side state touched is the
session-storage preview flag, which the old code reads as before (a cleared
flag means "not in preview", today's default). If the `proxy.md` sentence was
updated in this repo it is reverted with it. The e2e guard change (AC-28)
lands in clockwise-like and must accept both the old and the new web while
the two repos' merges are not simultaneous (it may check the old sentence and
the new test ids together), so neither revert breaks the other repo's suite.

## 8. Decisions

| ID | Decision | Source |
|---|---|---|
| D-1 | One client bound for every user-started call and every `apiFetch` call, longer than nginx's 130 s (assumed: 135 s, matching the "at least 135 s" proxy.md asks of operators), instead of per-operation shorter deadlines. The server bounds every uncommitted request at 90 s and answers with its own error; a committed booking can still answer 201 after 90 s, and past 120 s only nginx can answer (502/504). A client bound under 130 s would race nginx's answer and turn a known gateway error into silence; a shorter per-operation deadline would reintroduce the bug. A bound still exists so a dead connection does not spin forever. (Challenge 2026-11-01: was 125 s, sized on the 120 s write backstop, which is a silent cut and not an answer.) | factory-assumed |
| D-2 | The background health probe keeps a short bound (assumed: 10 s); its failure only drives the "can't reach the server" banner and never changes what data calls return. | factory-assumed |
| D-3 | "Still working" feedback after 8 s on any user-started call; natural-language parse shows AI progress from the start. | factory-assumed |
| D-4 | Invented data only in an explicit, labelled demo session; every automatic path into preview data (unreachable server, timeout, non-JSON answer, failed probe, failed sign-in) is removed. | factory-assumed; whether the demo remains at all is OQ-1 |
| D-5 | Booking create is never re-sent by the client; a lost answer, a generic 500 or any gateway 5xx says the booking may have been saved; only the server's budget 503 (status 503 **and** `RequestBudgetExpiredError` body) says it was not made. Verified against the code: before commit the handler answers 4xx/500/budget-503 with nothing saved; after commit only 201 or 500 (`handlers_booking.go:393-412`, `booking.go:396-415`). A 500 before commit ("booking conflict check failed") also means nothing was saved, but the contract does not pin 500 bodies, so the web does not distinguish it (conservative, honest). | factory-assumed, follows PAC-81 Part A's contract (503 only when nothing was saved); challenge 2026-11-01 |
| D-6 | A non-JSON 2xx answer is treated as "can't reach Paceday" (not preview), since it means the request did not reach the API. | factory-assumed |
| D-7 | No automatic retries for reads or writes; a manual "Try again" for reads only. | factory-assumed (keeps today's `retry: false`) |
| D-8 | A 5xx with a non-JSON body (nginx 502/504 page, an operator proxy's 502/503/504/52x page) is a gateway error: "can't reach" for reads, "didn't confirm / may have been saved" for writes and booking. The backend's own 5xx are always JSON (`server_errors.go`, `budget.go`), so the body tells them apart. The budget 503 is recognised by status **and** `RequestBudgetExpiredError` body, never by status alone. | challenge 2026-11-01, from §2.7 |
| D-9 | Demo data is keyed on the demo session alone, entered through one control; the shared "mock mode" switch is removed. OQ-1 (a)/(c) then only change that control. | challenge 2026-11-01 |
| D-11 | The demo exists only in non-production builds (Vite mode other than `production`: dev server and Lovable preview, `build:dev`, unit tests). A production build contains no demo entry, label, data or demo-session code, and never shows invented data; no automatic fallback anywhere. The entry is one explicit, labelled control on the sign-in screen's initial view, never reached from an error. Replaces the factory default (b). | owner, 2026-11-01 (OQ-1 (c)) |
| D-10 | Cloudflare-proxied (or any proxy under 130 s) deployments are an unsupported configuration per `docs/operator/proxy.md:93-110`; the client bound is not lowered for them. There the browser receives the proxy's HTML 5xx (e.g. Cloudflare 524 at 100 s), which D-8 shows honestly; no invented data, no false "not made". | challenge 2026-11-01 (resolves OQ-5) |

## 9. Open questions

- **OQ-1 — answered (owner, 2026-11-01): (c).** Keep the demo only in
  non-production builds (Lovable preview, dev). Production builds contain no
  demo entry and never show invented data; no automatic fallback anywhere.
  Applied in §3.3, D-11, AC-15, AC-15c, AC-30. The Lovable preview (a
  non-production build) still shows "can't reach Paceday" after a failure;
  the demo there is reached only through its explicit entry control.
- **OQ-2 — resolved (challenge, 2026-11-01).** The bundle's
  `RequestBudgetExpired` response (`contracts/openapi/openapi.yaml:14561-14608`)
  says nothing was written only for `createPublicBooking`; for
  `syncPersonalCalendar` it says the blockers already created are kept; it
  says nothing for any other write. So every write except booking create
  shows "refresh to check" on the budget 503 (§3.4).
- **OQ-3 — resolved (challenge, 2026-11-01).** Yes:
  `src/api/generated/schemas.ts:403-404` has `RequestBudgetExpiredError`
  (`z.literal("request timed out")`), and `schemas.types.d.ts` carries the
  503 entries. No regeneration needed. Recognising the 503 by body is now a
  requirement (§3.4, D-8), not a question.
- **OQ-4 — resolved (contract, 2026-11-01).** The e2e ACs (AC-5, AC-10,
  AC-24) control timing and failure at the browser's network layer with
  Playwright request interception (`page.route`) against the real compose
  stack: the real server answers, and the browser receives that answer late
  (AC-5: 20 s; AC-24: the real 201 after 30 s) or the first link-info request
  fails at the network level (AC-10). No test hook is added to the server,
  nginx or fake-google. What this does not exercise — the server itself being
  slow — is covered by PAC-81's backend tests; a fake-google latency control
  was rejected because PAC-81/PAC-72 bound each host read at 15 s and treat
  an unread host as busy, so a provider delay long enough to matter turns
  into "no slots" rather than a slow answer. AC-24's "exactly one booking"
  is checked in the database. Details: `contracts/features/PAC-83.md` §5.
- **OQ-5 — resolved (challenge, 2026-11-01): D-1 (135 s) and D-10.** No hop
  we ship cuts before the server answers: budget 90 s < write backstop 120 s
  < nginx 130 s < client 135 s; operators must run their proxy above 130 s.
  Cloudflare is not recommended anywhere (proxy.md calls its 100 s too
  short), so it is an unsupported configuration, shown honestly by D-8. The
  10 s probe (D-2) and 8 s "still working" (D-3) stand.

## Challenge log — 2026-11-01

Spec-challenger, against smart-calendar-flow `origin/main` @ `8966115` and
clockwise-like `origin/main` @ `953f96e`. **Verdict: accept-with-changes**
(changes applied in this revision). §2 citations checked: requestApi,
withFallback, health, manager fallback, demo flow, AuthDialog, PublicBooking
and lib/api line numbers are correct; the 32 `withFallback` calls in
`client.ts` all appear in §2.4.

| # | Severity | Issue | Verdict | Change |
|---|---|---|---|---|
| C-1 | blocking | The "120 s write backstop" was treated as an answer the client waits for (§3.1, D-1, AC-3). It is `WriteTimeout` (`lifecycle.go:207`): a silent cut. Past 120 s only nginx answers (502/504 HTML at 130 s). A committed booking is not cut by the 90 s budget at all. | valid | New §2.7; §3.1 rewritten; D-1 bound now > nginx 130 s (135 s); AC-3 checks 131 s. |
| C-2 | blocking | Gateway/proxy 5xx with HTML bodies (nginx 502/504, proxy 503/524) fell under "today's mapping": a write or booking that may have been applied would show "The server had a problem. Try again shortly." | valid | §3.4 new row, D-8, AC-19/22/27 extended. |
| C-3 | blocking | Booking "not made" keyed on a 503 without saying how it is recognised; an operator proxy's HTML 503, or a JSON 503 of another kind, would falsely tell the booker nothing was saved. | valid | §3.5 and D-5: status **and** `RequestBudgetExpiredError` body; AC-22b. |
| C-4 | blocking | The e2e anti-false-pass guard `assertNotDemo` (clockwise-like `e2e/fixtures.ts:250-261`) matches the exact banner sentence this spec removes; it would pass vacuously. "Web only" was wrong. | valid | §2.6, Repos line, AC-28, rollback note on cross-repo ordering. |
| C-5 | blocking | Demo data rides on the same mock-mode switch as the fallback (`AuthContext.tsx:85,109`); removing the fallback naively breaks the demo, and OQ-1 was not isolated. | valid | §3.3 isolation paragraph, D-9, AC-15b. |
| C-6 | major | Missed consumer: `ConferencingSection.tsx:101-113` fakes "Zoom connected (demo)" whenever mock mode is on. | valid | §2.2 bullet; §3.3; AC-15b. |
| C-7 | major | Lovable agent instructions (`AGENTS.md:13`, `docs/lovable-prompts/*`) require keeping the automatic fallback; a later Lovable edit would restore it. | valid | §2.6, §3.3, AC-29. |
| C-8 | major | `DemoBanner` is dismissible, says "backend is offline", and is mounted on 2 pages; AC-15's "every page shows a label" was unfalsifiable against an unspecified label. | valid | §2.3 facts; §3.3 and AC-15 require a non-dismissible label that never says offline. |
| C-9 | major | The 2 s pre-sign-in probe would turn a slow server into a sign-in error (no longer a demo, but still a blocked legitimate sign-in). `apiFetch` had no bound and was outside AC-3. | valid | §3.1, §3.4 sign-in paragraph, AC-3 and AC-14 extended. |
| C-10 | minor | AC-7's "adding an operation makes it fail" was not falsifiable with a hand-kept table. | valid | AC-7 requires the table derived from exported operations. |
| C-11 | minor | OQ-2, OQ-3, OQ-5 were answerable from the repos. | resolved | OQ-2/3/5 resolved in §9; OQ-4 narrowed (no latency hook exists). |
| C-12 | checked, no change | Cloudflare 100 s: not recommended anywhere, already flagged as too short in proxy.md. | out of scope | D-10, §5. |
| C-13 | checked, no change | Booking 500 "may have been saved": conservative but correct per contract; a pre-commit 500 is indistinguishable without a pinned body. | kept | D-5 note. |
| C-14 | checked, no change | MCP: own client, no timeout, no fallback; unaffected. | n/a | §2.6, §5. |

Remaining owner question: **OQ-1** (keep a demo? factory default (b), now a
one-control change either way). *Answered by the owner 2026-11-01: (c),
see revision 2 below.*

## Revision 2 — 2026-11-01 (owner answer OQ-1; OQ-4 routed)

- OQ-1 answered (c): §3.3 rewritten (non-production builds defined by Vite
  mode; production bundle without any demo; entry control explicit, labelled,
  never reached after an error); D-11 added; AC-15 reworded; AC-15c and AC-30
  added; OQ-1 marked answered.
- OQ-4 resolved by the contract: browser-level interception for the e2e ACs,
  no server test hook; the gap (server-side slowness) is PAC-81's tests'.
