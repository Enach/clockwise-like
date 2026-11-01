# Spec — PAC-83: Web client: 4-second abort falls back to preview data on slow but legitimate requests

- **Linear**: https://linear.app/paceday/issue/PAC-83/web-client-4-second-abort-falls-back-to-preview-data-on-slow-but
- **Status**: draft
- **Author**: spec-author
- **Inherited scope**: none. `docs/factory/api-audit.md` has no open finding on
  client timeouts or preview data. PAC-81 Part A recorded this as follow-up
  F-W1 / "WEB-timeouts" (`docs/specs/NEW-deploy-hardening.md:371-379`,
  `contracts/features/PAC-81-part-a.yaml:121`,
  `contracts/features/PAC-81-part-a.md:322-330`), and
  `docs/operator/proxy.md:112-113` tells operators the web aborts after 4 s
  "regardless": that sentence must be corrected when this ships.
- **Repos**: web only (Enach/smart-calendar-flow). No server or contract change
  is expected; see OQ-3 for the one contract wording the web depends on.
- **Code surveyed**: smart-calendar-flow `origin/main` @ `8966115`.

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
  restored on mount does too (`:81-89`).

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
4 s, a 503 budget answer, or a 413 in the web. The e2e suite has a public
booking journey (`e2e/tests/public-booking.spec.ts`) with no slow-server case.

## 3. Desired behaviour

### 3.1 The client waits as long as the server may legitimately work

The server now guarantees an answer for every request: its own error at the
end of a 90 s budget, with a 120 s write backstop behind it, nginx at 130 s.
The browser must never give up before the server can answer. The app waits
for every call at least until the server's own last-resort bound has passed,
so whatever the server says — success, "request timed out", any other error —
reaches the user. Only when that longer bound passes with no answer does the
app conclude the connection is lost.

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

### 3.4 What the user is told instead

Failures are shown where the action happened, with a way to try again for
reads, and with an honest statement of the outcome for writes:

| Situation | Reads | Writes |
|---|---|---|
| Server says the request ran out of time (its 503 "request timed out") | "This took too long and was stopped. Try again." + retry | Same wording; plus, only where the server's contract does not say nothing was written, "Refresh to check whether it was applied." |
| Server says the request is too large (413) | n/a | "This is too large to send" naming what to shorten (e.g. the request text); no retry button (the same request would fail again) |
| No answer within the client's bound, network failure, or a non-JSON answer | "Can't reach Paceday right now. Check your connection and try again." + retry; content already on screen stays, marked as possibly out of date | "Paceday didn't confirm this. It may or may not have been applied — refresh to check before trying again." |
| Any other HTTP error | today's mapping (unchanged) | today's mapping (unchanged) |
| The page is being left (navigation, tab closed) | nothing shown, nothing recorded | nothing shown, nothing recorded |

The persistent banner on Dashboard/Settings/Audit stops saying "showing demo
data" for an unreachable server; it says the server can't be reached and that
what is on screen may be out of date, with a retry. No message anywhere says
"showing preview data" outside a demo.

Sign-in when the server can't be reached shows an error in the sign-in dialog
with a retry, and does not start the demo.

### 3.5 Booking is never retried for the booker, and a lost answer is honest

- The app never sends a booking twice on its own — not on timeout, not on
  network error, not on reconnect.
- If the booking answer is lost (client bound reached, network failure,
  non-JSON answer) or the server answers its generic server error (which,
  per PAC-81, does not prove nothing was saved), the booker is told: "We
  couldn't confirm your booking. It may have been saved — check your email
  for a confirmation before booking again." The chosen time and typed details
  stay on screen. The app refreshes the times for that day so the booker can
  see whether the slot is gone; it does not offer a one-click "book again".
- If the server answers its budget 503 for a booking (which per PAC-81 means
  nothing was saved), the booker is told the booking was not made because the
  service was too slow, and may try again by pressing the button themselves.
- The public page never shows "Link not found" or "no available times" unless
  the server said so.

## 4. Acceptance criteria

Test levels: `[unit]` = vitest in the web repo (component/API-layer tests
with a controllable fake server and fake timers); `[e2e]` = Playwright
against the compose stack in this repo, with a test-only way to make the
server slow (OQ-4). `[contract]` is not used: no interface changes.

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
starts), when the server has not answered after 119 s, then the call is still
waiting (the client has not given up); and when it has not answered after the
client's bound (which is longer than the server's 120 s write backstop), then
the "can't reach" outcome of §3.4 is shown. Tested per operation family
(read, write, booking create) with fake timers.  [unit]

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
table-driven test enumerating every such operation; adding an operation with
a mock fallback outside a demo makes it fail.  [unit]

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
starts.  [unit]

AC-15. Given the person explicitly starts the demo (if the demo is kept, OQ-1),
then demo data is shown, every page shows a "demo" label, and no request
reaches the server for demo data; leaving the demo returns to the signed-out
state with no demo data left in the tab.  [unit]

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
non-JSON answer), then no success is shown and the "may or may not have been
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

AC-22. Given the server answers the booking with its generic 500, then the
same "may have been saved" message as AC-21 is shown (the 500 does not prove
nothing was saved).  [unit]

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
- The demo, if kept (OQ-1), keeps its current content.
- Tests listed in §2.5 are rewritten to the new rule (invert, don't delete);
  their pagehide and HTTP-error halves stay as they are.

## 7. Rollback

Web-only change, no migration, no contract change. Reverting the web PR
restores the 4 s abort and the automatic preview fallback exactly as today.
Nothing is stored server-side; the only client-side state touched is the
session-storage preview flag, which the old code reads as before (a cleared
flag means "not in preview", today's default). If the `proxy.md` sentence was
updated in this repo it is reverted with it.

## 8. Decisions

| ID | Decision | Source |
|---|---|---|
| D-1 | One client bound for every user-started call, longer than the server's 120 s write backstop (assumed: 125 s), instead of per-operation shorter deadlines. The server already bounds every request at 90 s and answers with its own error; a shorter client deadline would reintroduce the bug for slow-but-legitimate work and hide the server's answer. A bound still exists so a dead connection does not spin forever. | factory-assumed |
| D-2 | The background health probe keeps a short bound (assumed: 10 s); its failure only drives the "can't reach the server" banner and never changes what data calls return. | factory-assumed |
| D-3 | "Still working" feedback after 8 s on any user-started call; natural-language parse shows AI progress from the start. | factory-assumed |
| D-4 | Invented data only in an explicit, labelled demo session; every automatic path into preview data (unreachable server, timeout, non-JSON answer, failed probe, failed sign-in) is removed. | factory-assumed; whether the demo remains at all is OQ-1 |
| D-5 | Booking create is never re-sent by the client; a lost answer or a generic 500 says the booking may have been saved; only the server's budget 503 says it was not made. | factory-assumed, follows PAC-81 Part A's contract (503 only when nothing was saved) |
| D-6 | A non-JSON 2xx answer is treated as "can't reach Paceday" (not preview), since it means the request did not reach the API. | factory-assumed |
| D-7 | No automatic retries for reads or writes; a manual "Try again" for reads only. | factory-assumed (keeps today's `retry: false`) |

## 9. Open questions

- **OQ-1 (owner, product).** Must a demo remain? Today the only way into it is
  being silently dropped there when sign-in can't reach the server; there is
  no explicit "try the demo" control, and the Lovable preview relies on the
  automatic fallback to show anything. Options: (a) remove the demo entirely
  — the Lovable preview then shows "can't reach Paceday"; (b) keep it behind
  an explicit, labelled "Explore a demo" control on the sign-in screen only;
  (c) keep it only in non-production builds. **Factory default: (b).**
  Resolve: owner answer in chat → `owner-decisions.md`.
- **OQ-2 (factory, contract stage).** For each write whose budget 503 is
  shown, does the contract state that nothing was written? The web wording
  "refresh to check" (AC-17) depends on it. Owner: contract-author; resolve by
  reading the 503 descriptions in the bundle and listing them in the
  contract-stage note (no contract change expected).
- **OQ-3 (factory).** Is the generated zod/TS type for the budget 503
  (`RequestBudgetExpiredError`, a one-value enum) already in the web's
  generated files? If not, regenerate types before stage 4 (§4 of the
  factory README); the web must recognise the 503 by its body, not by status
  alone (other 503s exist: Zoom/Microsoft not configured). Owner:
  contract-author.
- **OQ-4 (factory, e2e).** AC-5/AC-10/AC-24 need the compose stack to answer
  slowly (or drop a connection) on demand. Is there a test-only latency hook
  (e.g. a fake calendar provider with a configurable delay, or a proxy in the
  e2e network)? If none, the plan must add one or move these ACs to `[unit]`
  with a stated gap. Owner: plan-author / e2e-author.
- **OQ-5 (factory).** The 125 s / 10 s / 8 s numbers are assumptions sized
  from PAC-81's ladder (90 s budget < 120 s write backstop < 130 s nginx).
  Cloudflare-proxied deployments cut at 100 s (`docs/operator/proxy.md`); the
  browser would then see a proxy error page (non-JSON) at 100 s and show
  "can't reach". Acceptable? Owner: spec-challenger; resolve in the challenge.
