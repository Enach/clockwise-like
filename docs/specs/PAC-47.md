# Spec — PAC-47: Selecting Outlook or WebCal shows "connected", then an empty calendar

- **Linear**: https://linear.app/paceday/issue/PAC-47
- **Status**: draft
- **Author**: spec-author
- **Inherited scope**:
  - **U-18** (`docs/factory/api-audit.md:486`). PAC-24 closed the "enforce the
    set" half: the server now refuses any value outside `google | outlook | webcal`
    (`backend/api/handlers_settings.go:32,272-273`). The half that is still wrong
    is the contract's explanation of what the values *do*. The settings schema
    description (`contracts/openapi/paths/scheduling.yaml:2081-2089`) says three
    consumers switch on the value, the first being `calendar.NewProvider`; that
    function has no caller (§2.1), so the description documents a switch that does
    not run. The contract stage must rewrite it to whatever is true after this
    change.
  - **API-076** (`docs/factory/api-audit.md:334`, open, low): the calendar
    event-list operation declares a personal-block flag that is never set. This
    spec changes what that operation returns for two of three providers, so under
    touch-it-fix-it the contract stage must resolve it (declare it truthfully or
    remove it) rather than carry it into new code paths.
  - No `x-uncertain` marker sits on the event-list, auth-status, integration-
    availability or disconnect operations today (checked in
    `contracts/openapi/openapi.yaml` on 2026-10-26).
  - **Not inherited**: API-029 (untagged free/busy slot keys, open) and API-061
    (disconnect also signs the user out, open). Both sit on operations this spec
    does not require changing; if the contract stage ends up touching either
    operation, it inherits that finding.

## 0. What changed since the issue was written

PAC-47 was filed on 2026-09-17, before PAC-24 and #204 landed. Re-reading the code
on `main` at `7de7044`:

- Settings are **per user** now (`backend/storage/settings.go:239-273`,
  `GetOrCreateSettingsByUser`, `WHERE user_id = $1`). The issue's third
  consequence — one user's choice changing what every other user is told — no
  longer holds (API-002 and API-009 are marked Fixed (PAC-24) at
  `docs/factory/api-audit.md:260,267`). It is dropped from this spec.
- The Microsoft credential is per user (`backend/auth/microsoft_oauth.go:49-89`).
- The free/busy service now has an Outlook branch that reads the caller's own
  Microsoft credential (`backend/engine/freebusy_service.go:122-127`); the WebCal
  case still falls through to Google (§2.4).
- Everything else the issue describes is still true and is re-cited below.

## 1. Problem

Paceday's product description promises three kinds of work calendar: Google,
Microsoft 365/Outlook, and read-only webcal/ICS feeds (`smart-calendar-flow/PRODUCT.md`,
"Operating Context"). The Settings screen offers all three.

A user who picks Outlook or a WebCal/iCal feed is then told by the connection panel
that their calendar is **connected** and **"Syncing in real time"**. The week view,
which is the first screen of the product, does not show that calendar. Depending on
how the user signed in it either shows an error, or — worse — quietly shows a
**different** calendar: the Google calendar of the account they signed in with.

So the user is told two contradictory things by the same server, and in the second
case is shown data from a calendar they did not choose to have read. Nothing on
screen lets the user or a support engineer tell which statement is true. For a
product whose positioning is "the simplest way to own your day", a calendar that
claims to be connected and shows nothing (or the wrong thing) is a first-session
failure: the user cannot get past it without help.

## 2. Current behaviour

### 2.1 The provider choice is stored and never used to read events

- The choice is a per-user setting, validated against
  `google | outlook | webcal` (`backend/api/handlers_settings.go:32,272-273`) and
  stored with the feed address next to it (`backend/storage/settings.go:195-197`).
- A function that would turn the choice into a calendar reader exists,
  `calendar.NewProvider` (`backend/calendar/client_factory.go:14-34`), switching on
  `outlook`, `webcal` and a Google default. `grep -rn "NewProvider"` over
  `backend/` and `mcp/` finds no caller of it; the other hits are the conferencing
  factory (`backend/conference/factory.go:20`, called from
  `backend/api/handlers_conferencing.go:47,172`) and the OIDC library
  (`backend/auth/oidc.go:41`).
- Its Outlook branch could not work if it were called: it builds the bearer token
  from `msAccessToken`, which returns `""` unconditionally
  (`backend/calendar/client_factory.go:20,78-79`).
- `calendar.ReadPersonalEvents` (`backend/calendar/personal_reader.go:13-46`) also
  switches on a provider name and also has no caller outside its package.

### 2.2 The week view always reads Google

- The event-list handler builds its client from the caller's stored **Google**
  credential and nothing else (`backend/api/handlers_calendar.go:133-144`, used at
  `:161-171`). The provider setting is not consulted.
- With no Google credential it answers 401 "calendar not connected"
  (`engine.LoadCalendarToken`, `backend/engine/cal_iface.go:31-44`;
  `writeCalendarFailure`, `backend/api/server_errors.go:59-75`).
- Every other calendar call site in the backend also builds a Google client
  directly: `backend/api/handlers_events.go:26-33`, `backend/engine/cal_iface.go:90`,
  `analytics.go:216`, `auto_decline.go:87`, `booking.go:208`, `habits.go:255`,
  `manager.go:196`, `team_availability.go:85`, `freebusy_service.go:133`.

Consequence by sign-in route:

| Signed in with | Picks | Week view today |
|---|---|---|
| Google (credential stored at sign-in, `backend/api/handlers_auth.go:69-73`) | Outlook or WebCal | Shows the **Google** calendar's events, not the chosen one |
| A route that stores no Google credential (e.g. OIDC SSO) | Outlook or WebCal | 401 → the dashboard shows "Couldn't load your calendar" (`smart-calendar-flow/src/pages/Dashboard.tsx:435-442`) while Settings says connected |

The issue's phrase "an empty calendar" is the second row as the user perceives it;
the first row was not described in the issue and is the more serious one.

### 2.3 The connection status reports a connection it has not verified

`GET /api/auth/status` (`backend/api/handlers_auth.go:82-133`):

- `webcal` (`:106-111`): `connected` is `true` whenever a feed address is stored.
  Nothing is fetched.
- `outlook` (`:98-105`): `connected` is `true` whenever a Microsoft refresh token is
  stored. Nothing is fetched, and the token is never used to read events (§2.2).
- `google` (`:112-132`): `connected` is `true` whenever a Google token is stored.
  Nothing is fetched (deliberately, PAC-65).

The frontend trusts this flag. The connection panel shows
"<provider> — connected" and "Syncing in real time" from it
(`smart-calendar-flow/src/components/WorkCalendarConnection.tsx:108-122`), and the
dashboard suppresses its "Connect your calendar" prompt whenever it is `true`
(`smart-calendar-flow/src/pages/Dashboard.tsx:113-114,445-462`). When the prompt
does show, its button always starts the **Google** connection
(`Dashboard.tsx:455-457`, `api.authConnectUrl()` with no provider), whatever the
user chose.

### 2.4 Free/busy for a WebCal user is answered from Google

`FreeBusyService` (`backend/engine/freebusy_service.go:119-138`) switches on the
provider. `outlook` reads the caller's Microsoft credential (`:122-127`). `webcal`
has no case and falls into `default:` (`:128-137`), which loads the caller's
**Google** credential and queries Google. A user who said "read this feed" has a
different calendar queried on their behalf.

### 2.5 The readers that exist, and what they lack

- **WebCal** (`backend/calendar/webcal_client.go`): fetches the stored address with
  `http.DefaultClient` (`:60-68`). There is no restriction on what address it may
  reach — no connection guard, unlike the Google client, which goes through the
  guarded construction path of PAC-43 (`backend/calendar/transport.go`,
  `backend/calendar/guard.go`) when a stand-in is declared. A user-supplied address
  fetched by the server is a request-forgery surface the moment anything calls it.
  The HTTP status is not checked before parsing (`:64-70`). Fetch errors wrap the
  `net/url` error (`:66`), whose text contains the full feed address. A
  `webcal://` address (the form calendar apps usually hand out) is passed to
  `http.NewRequestWithContext` as-is (`:60`); the Go client has no transport for
  that scheme. Its 15-minute cache (`:19-22,52-57`) is per instance and has no
  effect when a new instance is built per request (noted in PAC-43 §V2-2.2).
- **Outlook** (`backend/calendar/outlook_client.go`): reads `calendarView` from a
  hard-coded Microsoft Graph address (`:15,72-76`). There is no way to point it at
  a stand-in; PAC-50 §4.1 records the same gap
  (`docs/specs/PAC-50.md:81`). Non-2xx responses are returned as errors with the
  provider's body in the message (`:63-65`). There is no refresh of an expired
  access token anywhere (tracked by PAC-67).
- **Microsoft connection**: the connect flow exists
  (`backend/api/handlers_microsoft_auth.go:17-88`) and, on success, stores the
  credential and flips the provider to `outlook`
  (`backend/auth/microsoft_oauth.go:68`).

### 2.6 What tests cover today

- `backend/api/integration_availability_test.go:31-32` asserts WebCal is always
  offered.
- `backend/api/pac21_calendar_not_connected_test.go` and
  `backend/engine/pac21_calendar_credential_test.go` use WebCal **personal**
  calendars (a different feature) and the Google not-connected path.
- `e2e/tests/calendar.spec.ts` covers the Google path only: 401 with no token
  (`:27`), dashboard error (`:58`), events rendered from the stand-in (`:87`),
  free/busy (`:165`), rejected credential (`:221`).
- No test, unit or e2e, exercises the work-calendar provider setting with
  `outlook` or `webcal`. That absence is how §2.1–§2.4 survived.

## 3. Desired behaviour

1. **The calendar the user chose is the calendar the week view shows.** A user
   whose work calendar is Outlook sees their Outlook events. A user whose work
   calendar is a WebCal/iCal feed sees the feed's events. Neither ever sees their
   Google calendar instead, even if a Google credential is on file from sign-in.
   Google users see exactly what they see today.
2. **"Connected" is a claim the server has checked.** For Outlook and WebCal, the
   product reports the calendar as connected only when it has actually been able
   to read it. If it cannot — no feed address, feed unreachable, feed not a
   calendar, no Microsoft credential, Microsoft rejects the credential — the
   connection panel says *not connected* and the week view shows the "connect your
   calendar" state, not an empty week presented as if it were the truth and not a
   generic error. The two screens never disagree.
3. **The way back is for the calendar the user chose.** The not-connected prompt
   on the week view takes a WebCal or Outlook user to fix *that* connection, not to
   start a Google connection.
4. **Free/busy is answered only from the chosen calendar.** A WebCal user's
   free/busy is never answered by querying another provider on their behalf. A feed
   carries no information about other people, so for other attendees the answer is
   "unknown", the same as any other unanswerable attendee today.
5. **A feed address is a secret and an untrusted destination.** Private iCal
   addresses embed an access token, so the address never appears in an error shown
   to the user or written to the server log. The server fetches it only from
   addresses on the public internet; in a deployment declared as a test deployment
   it may instead reach only the same non-routable stand-in ranges the calendar
   stand-in already uses, and nothing else. A `webcal://` address is read as its
   HTTPS equivalent.
6. **Outlook can be proven against a stand-in**, under the same test-mode
   declaration that already governs the Google calendar stand-in, and is refused
   at startup without it — exactly as the Google redirection is.
7. **Feed events are read-only in the product.** The week view does not offer to
   edit or delete an event that came from a feed; if a change is attempted anyway,
   it is refused with a clear message and nothing is written to any calendar.

## 4. Acceptance criteria

The stand-ins named below are test doubles reachable only under the test-mode
declaration. "The week view" is the dashboard's calendar for the current week.

**Outlook**

AC-1. Given a user whose work calendar is Outlook, holding a valid per-user
Microsoft credential, and a Microsoft stand-in serving two events this week
("Standup" Mon 09:00–09:15 UTC, "Review" Wed 14:00–15:00 UTC), when they open the
week view, then both events are listed with those titles at those times, and no
request reaches the Google stand-in.  [e2e]

AC-1b. Given the same user also holds a Google credential from signing in with
Google, and the Google stand-in serves an event "GOOGLE-ONLY" this week, when they
open the week view, then "GOOGLE-ONLY" is not listed and the two Outlook events
are.  [e2e]

AC-1c. Given a user whose work calendar is Outlook, when the server asks Microsoft
for their events, then the request carries that user's own stored access token
(not an empty bearer, not another user's).  [unit]

**WebCal**

AC-2. Given a user whose work calendar is WebCal with a reachable feed address
serving a valid iCalendar document with two events this week, when they open the
week view, then both events are listed with the feed's titles and times, and no
request reaches the Google stand-in.  [e2e]

AC-2b. Given the stored feed address uses the `webcal://` scheme, when the server
reads it, then it fetches the HTTPS equivalent and the events are listed as in
AC-2.  [unit]

AC-2c. Given a WebCal user, when they try to change or delete an event from the
feed, then the product refuses with a message that the calendar is read-only and
no write request reaches any calendar provider or stand-in.  [contract]

**Not connected is said, never implied**

AC-3. Given a user whose work calendar is WebCal and whose feed address does not
answer (connection refused) — or answers with a non-2xx status, or with a body
that is not an iCalendar document — when they open the week view, then the
connection panel says not connected, the week view shows the "connect your
calendar" state, and at no moment is "connected" shown together with an empty
week.  [e2e for "does not answer"; unit for the other two]

AC-3b. Given a user whose work calendar is Outlook and the Microsoft stand-in
rejects their credential with 401, when they open the week view, then the result
is the same as AC-3.  [e2e]

AC-3c. Given a user whose work calendar is Outlook with no Microsoft credential on
file, or WebCal with no feed address, when the connection status is asked for,
then it reports not connected.  [unit]

AC-3d. Given AC-3's not-connected state for a WebCal or Outlook user, when they
use the week view's connect action, then it leads to reconnecting the provider
they chose, and does not start a Google connection.  [e2e]

**Free/busy**

AC-4. Given a WebCal user who also holds a Google credential, when free/busy is
requested on their behalf for any attendee, then no request reaches Google (or its
stand-in) and every attendee's coverage is reported as unknown.  [unit]

**The feed address**

AC-5. Given a feed address containing a query-string token and a fetch that
fails, when the failure is logged and answered, then neither the server log line
nor any response body contains the token or the full address.  [unit]

AC-5b. Given a deployment with no test-mode declaration, and a stored feed address
whose host resolves to a loopback, RFC 1918, link-local or unique-local address,
when the server reads the feed, then it refuses before connecting and the user is
shown not connected.  [unit]

AC-5c. Given a declared test deployment, and a feed address resolving to a public
address, when the server reads the feed, then it refuses before connecting.  [unit]

**Stand-in redirection**

AC-6. Given a deployment that redirects Microsoft calendar traffic to a stand-in
without the test-mode declaration, when the server starts, then it refuses to
start, with the same behaviour the Google calendar redirection has today.  [unit]

**Gate**

AC-7. api-gate, web-gate and e2e-gate each print `rc=0` on the final commits of
the implementing branches.  [gate]

## 5. Explicitly out of scope

- **Everything other than reading the week and answering free/busy.** Focus time,
  buffers, compression, habits, auto-decline, analytics, booking, manager views and
  team availability all still read and write Google directly (§2.2). Routing them
  through the chosen provider needs a product answer to "what does a feature that
  must *write* do when the chosen calendar is read-only?" (§8 OQ-1). Doing it here
  would make this an unreviewably large change touching every engine; it is
  proposed as a follow-up issue. Until then those features keep today's behaviour
  for Outlook and WebCal users.
- **Outlook token refresh and revocation.** PAC-67. AC-1 assumes a valid token;
  an expired one is handled by AC-3b's not-connected path, which is honest if not
  convenient.
- **Withdrawing Outlook or WebCal from the picker.** Rejected: PRODUCT.md lists
  both as supported calendars (see Decisions D-1).
- **Personal calendars** (the separate "add another calendar to block time"
  feature). Different setting, different screens.
- **What "disconnect" does for an Outlook or WebCal user.** Today it deletes the
  Google credential and signs the user out (API-061). Not changed here; recorded
  as §8 OQ-4.
- **The standalone free/busy operation used by no frontend screen** (API-091).
  Only the free/busy service behind scheduling features is in scope (AC-4).
- **Google's connection status semantics** (§8 OQ-3).

## 6. What must not change

- **Google users see the same week view.** Protected by `e2e/tests/calendar.spec.ts`
  AC-16 (`:87`) and AC-21 (`:221`); keep them green unmodified.
- **The 401 "calendar not connected" answer for a user with no usable
  credential**, and the dashboard's distinction between "not connected" and "could
  not load" (PAC-21). Protected by `calendar.spec.ts:27,58` and
  `backend/api/pac21_calendar_not_connected_test.go`.
- **The status endpoint makes no outbound call for Google users** (PAC-65). No
  automated test pins this; the contract stage should add one or say why not.
- **Production deployments still refuse every stand-in redirection** unless
  declared (PAC-43). Protected by the `backend/config` table tests and
  `e2e/tests/startup.spec.ts`.
- **The accepted set of provider values** (`google | outlook | webcal`) and the
  400 for anything else (PAC-24). Protected by the settings validation tests.
- **Settings stay per user.** Nothing here may reintroduce a shared row or read
  another user's credential (API-002, API-009).
- **Managers see aggregate focus minutes only, never event titles** (PRODUCT.md).
  Outlook and WebCal event titles are as private as Google ones.

## 7. Rollback

No data model change is expected: the provider choice, the feed address and the
Microsoft credential are already stored per user. Reverting the backend and
frontend PRs restores today's behaviour (Google-only reads, unverified
"connected"). Nothing a user stored is deleted by either direction. If the contract
stage finds a stored field is needed (for example a "last read succeeded" marker),
its down migration drops only that derived field and loses nothing the user
entered.

Revert order is the reverse of the merge order: frontend first, then backend, so
the frontend never expects a status shape the server no longer sends.

## 8. Open questions

- **OQ-1 — writes under a read-only or non-Google calendar.** What should focus
  time, habits, compression and personal blocking do for a WebCal user, and should
  they write to Outlook for an Outlook user? Owner: product (Nicolas Hochart).
  Resolution: a decision recorded on the follow-up issue; not needed for this spec,
  which leaves those features as they are (§5).
- **OQ-2 — how "checked" is "connected".** Verifying on every status request means
  one outbound read per dashboard load; trusting the last read's outcome means the
  status can be stale for one load. The spec requires only that the two screens
  never disagree and that "connected" is never shown for a calendar that has not
  been read successfully (AC-3). Owner: contract-author. Resolution: the contract
  stage chooses and records why, taking PAC-65's reason (keep credentials inside
  the calendar seam) into account.
- **OQ-3 — should Google's "connected" also be verified?** Today it is "a token
  is stored" (§2.3). A revoked Google token currently produces a "could not load"
  error rather than an empty week, so the PAC-47 symptom does not occur, but the
  same dishonesty exists. Owner: product. Resolution: decide whether to widen this
  spec or file it separately; the factory assumed separately (D-4).
- **OQ-4 — disconnect for Outlook and WebCal users.** Should "Disconnect" clear the
  Microsoft credential / feed address rather than the Google credential?
  Owner: product. Resolution: a follow-up issue alongside API-061.
- **OQ-5 — feed refresh interval.** The reader's 15-minute cache is ineffective
  today (§2.5). How fresh must a feed be? "Syncing in real time" is not true for a
  feed under any implementation. Owner: product, for the copy; contract-author,
  for the cache. Resolution: the frontend must not say "real time" for a feed; the
  exact wording is a frontend-stage choice.
- **OQ-6 — size and time limits on a feed.** A feed is user-chosen and can be
  arbitrarily large or slow. Owner: contract-author. Resolution: state limits in
  the contract; this spec only requires that exceeding them is reported as not
  connected (AC-3) rather than hanging the week view.

## Decisions

- **D-1 — honour the setting rather than withdraw the options.** *Assumed by the
  factory, pending owner confirmation.* PRODUCT.md lists Microsoft 365/Outlook and
  read-only webcal/ICS feeds as calendars Paceday connects to; withdrawing them
  would contradict a recorded product fact, and the ticket's acceptance criteria
  require both to list events.
- **D-2 — scope is reading the week and free/busy only.** *Assumed by the factory,
  pending owner confirmation.* Write-side features keep today's behaviour (§5,
  OQ-1). This is the conservative option: it changes no data any feature writes.
- **D-3 — a WebCal user's free/busy stops querying Google.** *Assumed by the
  factory, pending owner confirmation.* It narrows which calendar is read on the
  user's behalf to the one they chose; the cost is "unknown" coverage for other
  attendees, which the product already handles.
- **D-4 — Google's "connected" semantics are not changed here.** *Assumed by the
  factory, pending owner confirmation.* See OQ-3.
- **D-5 — feed fetches reach only public addresses in production, and only the
  stand-in ranges in a declared test deployment.** *Assumed by the factory, pending
  owner confirmation.* Without it, making the feed reader live turns a user-typed
  address into a server-side request to the internal network. It reuses the
  stand-in ranges PAC-43 already fixed (`backend/config/endpoints.go:49-60`) rather
  than inventing a second list.
- **D-6 — a `webcal://` address is read as HTTPS, never plain HTTP.** *Assumed by
  the factory, pending owner confirmation.* It is how calendar apps publish feeds;
  choosing HTTPS keeps an embedded token off the wire in clear.
