# Spec — PAC-47: Selecting Outlook or WebCal shows "connected", then an empty calendar

- **Linear**: https://linear.app/paceday/issue/PAC-47
- **Status**: challenged — revised after round 1 (see "Revision 1")
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
  switches on a provider name. Unlike `NewProvider` it **is live**: it reads the
  separate *personal calendars* feature, called from
  `backend/engine/personal_blocker.go:72` (preview) and `:87` (sync), which are
  reached from `GET /api/personal-calendars/{id}/preview`,
  `POST /api/personal-calendars/{id}/sync` (`backend/api/routes.go:107-113`) and
  the 30-minute per-user cron (`backend/main.go:95-108`), and exercised for the
  Google branch by `e2e/tests/seam.spec.ts:384-413`. Its `webcal` branch
  (`personal_reader.go:15-17`) is the same WebCal reader described in §2.5. (The
  first version of this spec said it had no caller; that was wrong.)

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

For Outlook the table needs one more distinction, because "picking Outlook" in
Settings runs the Microsoft *sign-in* (§2.5): a Google-signed-in user whose
Microsoft account has **the same email** ends up with both credentials on one
Paceday account; one whose Microsoft account has **a different email** is signed
in to a different Paceday account (created if absent), and the account they
started from is unchanged and still on Google.

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

The dashboard has a **second** status surface, which the first version of this
spec missed: `smart-calendar-flow/src/components/ConnectionStatus.tsx`, rendered in
the dashboard sidebar (`Dashboard.tsx:579`). Its Connect button always starts the
Google connection (`ConnectionStatus.tsx:12-14`, `api.authConnectUrl()` with no
provider), it shows "Syncing in real time" whenever `connected` is true (`:59`),
and its Disconnect toast says "Disconnected from Google Calendar" whatever the
provider (`:19`).

Onboarding also trusts the flag: it skips the connect step whenever `connected`
is true (`smart-calendar-flow/src/pages/Onboarding.tsx:61-62`) and always advances
for `webcal` without checking anything (`:84-85`).

So there are three connect actions and three status lines in the product today:
the dashboard's not-connected prompt, the dashboard's status card
(`ConnectionStatus`) and the Settings work-calendar panel
(`WorkCalendarConnection`).

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
  `backend/calendar/guard.go`) when a stand-in is declared. This reader is **already
  reachable in production** through personal calendars (§2.1): any signed-in user
  who adds a WebCal personal calendar makes the server fetch an address they typed,
  on demand (preview, sync) and every 30 minutes (cron), with no restriction on
  where it points. The server-side request-forgery surface therefore exists today;
  it is not created by this spec. The HTTP status is not checked before parsing
  (`:64-70`). Fetch errors wrap the `net/url` error (`:66`), whose text contains the
  full feed address, and the personal-calendar sync logs that error verbatim
  (`backend/engine/personal_blocker.go:61`, `log.Printf(... %v, err)`), so feed
  addresses, including any token in them, are written to the server log today. A
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
- **Microsoft connection is a sign-in, not an attachment.** The flow
  (`backend/api/handlers_microsoft_auth.go:17-81`) is mounted without
  `requireAuth` (`backend/api/routes.go:35-36`) and never reads who is signed in.
  On callback it reads the Microsoft account's email (`:62`), resolves a Paceday
  user **by that email** with `storage.UpsertUser(..., "microsoft", ...)` (`:67`;
  `UpsertUser` is `INSERT ... ON CONFLICT (email) DO UPDATE`,
  `backend/storage/users.go:21-31`, so it creates a user when none has that email),
  stores the Microsoft credential **on that user** and flips that user's provider
  to `outlook` (`:74`; `backend/auth/microsoft_oauth.go:52-69`), and **issues a
  session for that user** (`:79`), replacing whatever session the browser held.
  The Settings picker already labels it "Sign in with Microsoft"
  (`smart-calendar-flow/src/components/WorkCalendarConnection.tsx:16`). Consequence:
  the credential lands on the account the browser was signed in to only when both
  accounts share an email address.

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
- No test covers the WebCal personal-calendar branch, the feed address in the
  sync log, or which account the Microsoft callback attaches its credential to.

### 2.7 Everything that writes to a calendar writes to Google

No write path consults the provider setting. The only readers of it are the
status handler, the free/busy service and the unused factory (§2.1, §2.3, §2.4).

- **Writes the user starts from the week view.** Drag-to-move
  (`smart-calendar-flow/src/pages/Dashboard.tsx:269`), edit and delete
  (`src/components/EventEditForm.tsx:95,122`), reschedule suggestions
  (`src/components/RescheduleSuggestions.tsx:238`) and add/remove conference link
  (`src/api/client.ts:1409,1423`) call `PATCH`/`DELETE /api/events/{id}` and
  `POST`/`DELETE /api/events/{id}/conference` (`backend/api/routes.go:116-129`).
  The server opens only a Google client for them
  (`backend/api/handlers_events.go:24-33`; conference routes via
  `handlers_conferencing.go:309,405`) and answers "event not found" when the id is
  not a Google event id (`handlers_events.go:55-58`), or 401 "calendar not
  connected" when the user has no Google credential.
- **Writes Paceday makes on its own.** Focus blocks (`backend/engine/focus_time.go:186`,
  cleanup at `focus_time_cleaner.go:34`), smart scheduling
  (`smart_schedule.go:326`), habits (`habits.go:242`), compression
  (`compression.go:187`) and personal blockers (`personal_blocker.go:92,111,124`)
  go through `newCalOps` (`cal_iface.go:84-95`), which loads the user's **Google**
  credential. Auto-decline declines invitations through a Google client
  (`auto_decline.go:87,105`). A booking confirmation creates an event on each
  host's Google calendar (`booking.go:202-209,269`) and, when a host has no Google
  credential, records the booking and skips that host's calendar event
  (`booking.go:252-255`).

Today these writes are visible to every user who signed in with Google, because the
week view shows Google. They are not visible to a user who chose Outlook or WebCal
once the week view shows that calendar instead (§3.1).

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
3. **The way back is for the calendar the user chose, and it keeps the user who
   they are.** Every connect action the product shows (the dashboard's
   not-connected prompt, the dashboard's calendar status card, and the Settings
   work-calendar panel) takes a WebCal or Outlook user to fix *that* connection,
   never to a Google connection. Connecting (or reconnecting) Outlook while signed
   in **attaches the Microsoft credential to the Paceday account that is signed
   in**, whatever email the Microsoft account has. It does not sign the browser in
   as anyone else, does not create a Paceday account, and does not change any other
   Paceday account. A connection is credited only to the account that started it.
   Signing in with Microsoft from the sign-in page, with nobody signed in, stays a
   sign-in, as today.
4. **Free/busy behind scheduling features is answered only from the chosen
   calendar.** A WebCal user's free/busy is never answered by querying another
   provider on their behalf. The feed describes the user's own time, so the user's
   own busy time comes from the feed; it carries no information about other
   people, so for other attendees the answer is "unknown", the same as any other
   unanswerable attendee today. (The standalone free/busy operation no screen uses
   is outside this spec, §5.)
5. **A feed address is a secret and an untrusted destination, wherever the server
   fetches one.** This applies to every server-side feed fetch: the work calendar
   *and* personal calendars, which use the same reader today. Private iCal
   addresses embed an access token, so the address never appears in an error shown
   to the user or written to the server log. The server fetches only from
   addresses on the public internet; in a deployment declared as a test deployment
   it may instead reach only the same non-routable stand-in ranges the calendar
   stand-in already uses, and nothing else. The rule holds for every hop: a
   redirect is followed only to an address that would itself be allowed, and the
   address that was checked is the address connected to. A `webcal://` address is
   read as its HTTPS equivalent. A refused or failed personal-calendar fetch is
   treated like any other failed read: it removes none of the blocks already
   placed from that calendar.
6. **Outlook can be proven against a stand-in**, under the same test-mode
   declaration that already governs the Google calendar stand-in, and is refused
   at startup without it — exactly as the Google redirection is.
7. **Outlook and feed events are read-only in the product.** For an event shown
   because the user's work calendar is Outlook or WebCal, the week view does not
   offer move, edit, delete, reschedule or add/remove-conference-link. If such a
   change is attempted anyway, it is refused with a message that says the calendar
   is read-only in Paceday (not "not found", not "not connected") and nothing is
   written to any calendar: not Google, not Microsoft, not the feed. Writing to
   Outlook is a later feature (§5).
8. **Paceday writes only to the calendar the user chose.** For a user whose work
   calendar is Outlook or WebCal, no Paceday feature writes to their Google
   calendar, even if a Google credential is on file from sign-in: focus blocks and
   their cleanup, smart scheduling, habits, compression, personal-calendar
   blockers and auto-decline all stop writing for that user, and a booking with
   that user as host is recorded without creating an event in their Google
   calendar (as already happens today for a host with no Google credential).
   Nothing already written is removed. Where the user starts one of these features
   on demand, the product says the feature is unavailable for their calendar
   instead of reporting success. Google users are unaffected.
9. **No status line claims more than is true for the chosen calendar.** No surface
   says "Syncing in real time" for a feed, and no surface names Google Calendar
   (in its status text or in a connect or disconnect message) for a user whose
   work calendar is Outlook or WebCal. Onboarding never shows a WebCal or Outlook
   calendar as connected unless the status says so; it may still let the user
   continue to the next step.

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

AC-1d. Given a user whose work calendar is Outlook, who also holds a Google
credential, and whose week view lists an Outlook event, when a move, edit, delete,
reschedule or add/remove-conference-link is requested for that event, then the
request is refused with the read-only answer of §3.7, and no write request reaches
the Google stand-in or the Microsoft stand-in.  [contract]

AC-1e. Given the same user without a Google credential, when the same requests are
made, then the answer is the same read-only refusal, not "calendar not
connected", and the status still reports connected.  [unit]

AC-1f. Given an Outlook or WebCal user's week view, when an event from that
calendar is shown, then no move, edit, delete, reschedule or conference-link
control is offered for it.  [e2e]

**WebCal**

AC-2. Given a user whose work calendar is WebCal with a reachable feed address
serving a valid iCalendar document with two events this week, when they open the
week view, then both events are listed with the feed's titles and times, and no
request reaches the Google stand-in.  [e2e]

AC-2b. Given the stored feed address uses the `webcal://` scheme, when the server
reads it, then it fetches the HTTPS equivalent and the events are listed as in
AC-2.  [unit]

AC-2c. Given a WebCal user, when a move, edit, delete, reschedule or
add/remove-conference-link is requested for an event from the feed, then the
product refuses with the read-only answer of §3.7 and no write request reaches any
calendar provider or stand-in.  [contract]

**Not connected is said, never implied**

AC-3. Given a user whose work calendar is WebCal and whose feed address does not
answer (connection refused) — or answers with a non-2xx status, or with a body
that is not an iCalendar document — when they open the week view, then the
connection panel and the dashboard status card both say not connected and the
week view shows the "connect your calendar" state, observed once the week's event
request has settled; and on a second load of the dashboard, once both the status
and the event requests have settled, no surface says connected.  [e2e for "does
not answer"; unit for the other two]

AC-3b. Given a user whose work calendar is Outlook and the Microsoft stand-in
rejects their credential with 401, when they open the week view, then the result
is the same as AC-3.  [e2e]

AC-3c. Given a user whose work calendar is Outlook with no Microsoft credential on
file, or WebCal with no feed address, when the connection status is asked for,
then it reports not connected.  [unit]

AC-3d. Given AC-3's not-connected state for a WebCal or Outlook user, when they
use **each** connect action the product shows (the dashboard's not-connected
prompt, the dashboard's calendar status card, and the Settings work-calendar
panel), then each leads to reconnecting the provider they chose (for WebCal, to
editing the feed address; for Outlook, to the Microsoft connection), and none
starts a Google connection.  [e2e]

AC-3e. Given user A signed in with Google as `a@x`, a separate existing Paceday
user B whose email is `b@y`, and a Microsoft stand-in account whose email is
`b@y`, when A connects Outlook from Settings and completes the Microsoft step,
then A holds the Microsoft credential and A's work calendar is Outlook; B's
credentials and settings are unchanged; no Paceday user is created; and the
browser's session is still A's (the next authenticated request is answered for
A).  [e2e]

AC-3f. Given user A started an Outlook connection, when the Microsoft step is
completed in a browser whose session is user C's (or no session), then no
Microsoft credential is stored on A or C by that completion and C's session is
not replaced.  [unit]

AC-3g. Given a WebCal user whose feed has been read successfully, when the
dashboard and Settings are shown, then neither says "Syncing in real time"; and
given an Outlook or WebCal user, when any status line, connect or disconnect
message is shown, then none names Google Calendar.  [e2e]

**Free/busy**

AC-4. Given a WebCal user who also holds a Google credential, and a feed with one
event this week, when the free/busy service behind scheduling features is asked
about the user and two other attendees, then no request reaches Google (or its
stand-in), the user's busy time is the feed event, and both other attendees'
coverage is reported as unknown.  [unit]

**The feed address**

AC-5 and AC-5b to AC-5e apply to **every server-side feed fetch**: each is
tested once for the work calendar and once for a WebCal personal calendar
(preview, and the sync that the cron also runs).

AC-5. Given a feed address containing a query-string token and a fetch that
fails, when the failure is logged and answered, then neither the server log line
nor any response body contains the token or the full address.  [unit]

AC-5b. Given a deployment with no test-mode declaration, and a stored feed address
whose host resolves to a loopback, RFC 1918, link-local or unique-local address,
when the server reads the feed, then it refuses before connecting and the user is
shown not connected (work calendar) or the read fails (personal calendar).  [unit]

AC-5c. Given a declared test deployment, and a feed address resolving to a public
address, when the server reads the feed, then it refuses before connecting.  [unit]

AC-5d. Given an allowed feed address that answers with a redirect to an address
AC-5b or AC-5c would refuse, when the server reads the feed, then it does not
connect to the redirect target.  [unit]

AC-5e. Given a feed host that resolves to an allowed address when checked and to a
refused address on any later lookup, when the server reads the feed, then no
connection is made to the refused address.  [unit]

AC-5f. Given a WebCal personal calendar that already has blockers placed in the
work calendar, when a sync's feed fetch is refused or fails, then every existing
blocker is still in place.  [unit]

**Stand-in redirection**

AC-6. Given a deployment that redirects Microsoft calendar traffic to a stand-in
without the test-mode declaration, when the server starts, then it refuses to
start, with the same behaviour the Google calendar redirection has today.  [unit]

**Writes go only to the chosen calendar**

AC-8. Given a user whose work calendar is Outlook (and, separately, WebCal) and
who holds a Google credential, when focus scheduling, focus cleanup, smart
scheduling, habit placement, compression, a personal-calendar sync and
auto-decline each run for them (on demand and from their scheduled runs), then no
write request reaches the Google stand-in, and every event already in the Google
stand-in is unchanged.  [unit; e2e for focus scheduling]

AC-8b. Given the same user, when they start one of those features on demand, then
the answer says the feature is unavailable for their calendar, not success.
[contract]

AC-8c. Given a scheduling link whose host's work calendar is Outlook and who holds
a Google credential, when a guest books a slot, then the booking is recorded and
no event is created in the Google stand-in.  [unit]

AC-8d. Given a Google user, when the same features run, then they write to the
Google stand-in exactly as before.  [unit; existing tests, see §6]

**Two users**

AC-9. Given user A on WebCal and user B on Outlook, each with different events,
when both open the week view (A, then B, then A again) and both statuses are asked
for, then each sees only their own events and their own status, and neither A's
feed contents nor B's Outlook events appear in the other's answers.  [unit]

**Gate (not a behaviour of the feature)**

AC-7. api-gate, web-gate and e2e-gate each print `rc=0` on the final commits of
the implementing branches. This is the factory's admission rule, listed so the
ticket's acceptance line is traceable; it needs no test.  [gate]

## 5. Explicitly out of scope

- **Writing to Outlook.** Paceday's own features (focus time, habits,
  compression, personal blockers, smart scheduling, auto-decline, booking events)
  and the week view's edit actions do not write to Outlook in this spec; for an
  Outlook or WebCal user they stop writing to Google (§3.7, §3.8) and nothing
  replaces them. Making them write to Outlook is a follow-up feature (§8 OQ-1).
- **Reads by other features.** Analytics, manager views and team availability
  still read Google for an Outlook or WebCal user who holds a Google credential.
  They do not write, and routing them is part of the same follow-up (OQ-1).
- **Outlook token refresh and revocation.** PAC-67. AC-1 assumes a valid token;
  an expired one is handled by AC-3b's not-connected path, which is honest if not
  convenient.
- **Withdrawing Outlook or WebCal from the picker.** Rejected: PRODUCT.md lists
  both as supported calendars (see Decisions D-1).
- **Personal calendars**, except for the feed-fetch rules. The personal-calendar
  feature's settings and screens do not change. Its WebCal fetches *are* in scope
  for §3.5 (address guard, redirect and lookup rules, redaction), because they use
  the same reader and the gap is live today (§2.5). Its blocker writes are in scope
  for §3.8 only.
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
- **Signing in with Microsoft with nobody signed in** still signs in (or creates)
  the Paceday user for that Microsoft email, as today. No test pins this today;
  the plan stage adds one beside AC-3e.
- **Disconnect is not made worse.** Its behaviour for Outlook and WebCal users
  (deletes the Google credential and signs out, API-061) is unchanged by this spec
  except for the wording of §3.9; it must not start deleting anything else.
- **Personal-calendar syncs from public WebCal addresses and from Google personal
  calendars keep working.** Protected for Google by `e2e/tests/seam.spec.ts:384-413`;
  nothing protects the WebCal branch today (§2.6).
- **Nothing a feature already wrote to a user's Google calendar is removed** by
  §3.8 taking effect.

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

- **OQ-1 — writing to Outlook, and features for a WebCal user.** Should focus
  time, habits, compression, personal blocking, booking events and the week view's
  edit actions write to Outlook for an Outlook user, and what should they do for a
  read-only WebCal user? Owner: product (Nicolas Hochart). Resolution: a decision
  on a follow-up issue. Until then this spec stops those writes for non-Google
  users (D-9) rather than writing where the user no longer looks.
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
  for the cache. Resolution: no status line on the dashboard (prompt and status
  card) or in Settings says "real time" for a feed (§3.9, AC-3g); the exact
  wording is a frontend-stage choice.
- **OQ-6 — size and time limits on a feed.** A feed is user-chosen and can be
  arbitrarily large or slow. Owner: contract-author. Resolution: state limits in
  the contract; this spec only requires that exceeding them is reported as not
  connected (AC-3) rather than hanging the week view.
- **OQ-7 — a stored plain `http://` feed address.** D-6's reason (keep an embedded
  token off the wire) applies to it too, but upgrading it to HTTPS can break a
  feed served only over HTTP, and refusing it can break an existing personal
  calendar. Owner: product (Nicolas Hochart). Resolution: decide at the contract
  stage or on a follow-up; until decided the factory assumes such an address is
  still read as stored (no new breakage), with the address guard and redaction of
  §3.5 applied.
- **OQ-8 — who builds the Microsoft Graph stand-in redirection.** PAC-50 §4.1
  plans the same redirection (`docs/specs/PAC-50.md:81`). Owner: contract-author
  of whichever of PAC-47 and PAC-50 reaches stage 2 first. Resolution: that
  feature builds it under the PAC-43 test-mode declaration and the other reuses
  it; AC-6 is satisfied by either.
- **OQ-9 — closing the parent issue.** The issue asks that the status endpoint stop
  reporting any unverified connection; Google stays unverified here (D-4, OQ-3).
  Owner: product. Resolution: PAC-47 is not closed by this feature while OQ-3 is
  open; the stage PRs use `Part of PAC-47`.

## Decisions

- **D-1 — honour the setting rather than withdraw the options.** *Assumed by the
  factory, pending owner confirmation.* PRODUCT.md lists Microsoft 365/Outlook and
  read-only webcal/ICS feeds as calendars Paceday connects to; withdrawing them
  would contradict a recorded product fact, and the ticket's acceptance criteria
  require both to list events.
- **D-2 — scope is reading the week and free/busy, plus stopping writes to a
  calendar the user did not choose.** *Assumed by the factory, pending owner
  confirmation.* Revised in Revision 1: the first version left write-side
  features writing to Google, which after this change would be a calendar the
  user is no longer shown (challenge Blocking 3). See D-9.
- **D-3 — a WebCal user's free/busy stops querying Google.** *Assumed by the
  factory, pending owner confirmation.* It narrows which calendar is read on the
  user's behalf to the one they chose; the cost is "unknown" coverage for other
  attendees, which the product already handles.
- **D-4 — Google's "connected" semantics are not changed here.** *Assumed by the
  factory, pending owner confirmation.* See OQ-3.
- **D-5 — feed fetches reach only public addresses in production, and only the
  stand-in ranges in a declared test deployment.** *Assumed by the factory, pending
  owner confirmation.* The feed reader is already live for personal calendars, so
  a user-typed address is already a server-side request to wherever it points
  (§2.5); routing the work calendar through it would add a second entry to the
  same hole. It reuses the stand-in ranges PAC-43 already fixed
  (`backend/config/endpoints.go:49-60`) rather than inventing a second list.
  Revised in Revision 1 (the first version said the reader was not yet live).
- **D-6 — a `webcal://` address is read as HTTPS, never plain HTTP.** *Assumed by
  the factory, pending owner confirmation.* It is how calendar apps publish feeds;
  choosing HTTPS keeps an embedded token off the wire in clear.
- **D-7 — connecting Outlook while signed in attaches the credential to the
  signed-in account.** *Assumed by the factory, pending owner confirmation.* The
  alternative, keeping it a sign-in, sends a user whose Microsoft email differs
  into another Paceday account and leaves the account they started from not
  connected, so §3.3's "way back" could never work for them. Attaching to the
  signed-in account changes no other account, creates none, and gives the account
  access only to a Microsoft calendar its user just proved they control. It
  follows the precedent of Zoom (connected behind the session, PAC-24,
  `backend/api/routes.go:37-39`) and of PAC-50's step-up, which is bound to the
  caller and must leave calendar settings untouched (PAC-50 AC-17). Sign-in from
  the sign-in page is unchanged.
- **D-8 — Outlook events are read-only in Paceday for now.** *Assumed by the
  factory, pending owner confirmation.* Writing to Outlook is new product scope
  (OQ-1); refusing is the option that writes nothing to any calendar, and it
  replaces today's misleading "not found" / "not connected" answers.
- **D-9 — no Paceday feature writes to Google for a user whose work calendar is
  not Google.** *Assumed by the factory, pending owner confirmation.* Of the three
  outcomes the challenge offered, this is the one with the fewest writes to a
  calendar the user did not choose. Keeping the writes visible would contradict
  §3.1 (the chosen calendar is the one shown); accepting the regression requires
  an owner decision the factory cannot make. It deletes nothing already written.
  The cost, recorded so the owner can weigh it: focus time, habits, compression,
  personal blocking, smart scheduling and auto-decline do nothing for Outlook and
  WebCal users until OQ-1 is answered, and bookings for such a host create no
  calendar event (as for a host with no Google credential today).
- **D-10 — the feed-fetch rules cover personal calendars too.** *Assumed by the
  factory, pending owner confirmation.* The reader is shared and the gap is live
  in production; closing it for one caller and leaving it open for the other
  would leave the request-forgery surface and the address-in-log leak in place.
  A refused fetch fails the read and removes no existing blocker (AC-5f), so no
  user data is lost; a personal calendar whose feed sits on a private address
  stops refreshing, which is the intended effect.

## Challenge — 2026-10-26

Reviewed by `spec-challenger` against the committed spec at `e100f68`, the code on
`main` at `7de7044` (api) and `4ded854` (web), and the Linear issue text. No
author reasoning beyond this file was read. Every citation below was opened and
read; nothing was compiled or run (this is a text-only stage).

**Verdict**: accept-with-changes

### Blocking

1. **"Connecting" or "reconnecting" Outlook signs the user in as whoever owns the
   Microsoft account; the spec describes it as attaching a credential.** §2.5 says
   the connect flow "on success, stores the credential and flips the provider to
   `outlook`". What it actually does is resolve a user from the Microsoft
   account's email, `storage.UpsertUser(... info.Email ..., "microsoft", ...)`
   (`backend/api/handlers_microsoft_auth.go:62-68`; `UpsertUser` is
   `ON CONFLICT (email)`, `backend/storage/users.go:21-31`), save the credential on
   *that* user (`:74`), and issue a session for that user (`:79`). The route is not
   behind `requireAuth` (`backend/api/routes.go:35-36`) and never looks at who is
   signed in. The picker already labels it "Sign in with Microsoft"
   (`smart-calendar-flow/src/components/WorkCalendarConnection.tsx:16`).
   Consequences the spec permits:
   - AC-3d passes when the "reconnect Outlook" button sends a Google-signed-in user
     (`a@x`) through Microsoft as `b@y`: the user lands in a *different Paceday
     account*, the original account is still not connected, and every AC still
     holds.
   - AC-1b's precondition (one user holding both a Google sign-in credential and an
     Outlook credential) is reachable only when the two accounts share an email
     address. The first row of the §2.2 table has the same limit.
   *Resolves when* §2.5 describes the flow as it is, and §3/AC-3d state what
   "connect Outlook" does to the signed-in account: it attaches the credential to
   the user who is signed in, or it is still a sign-in and the spec says so (and
   that a different email gives a different account). Either way, add an AC that
   checks which user ends up holding the credential and which session the browser
   holds afterwards.

2. **Outlook events become visible in the week view, but the week view's write
   actions still go to Google, and no requirement covers this.** After AC-1, the
   events on the grid are Outlook events with Graph ids. The dashboard offers
   drag-to-move (`smart-calendar-flow/src/pages/Dashboard.tsx:269`), edit and
   delete (`src/components/EventEditForm.tsx:95,122`), reschedule suggestions
   (`src/components/RescheduleSuggestions.tsx:238`) and add/remove conference
   (`src/api/client.ts:1409,1423`). On the server, `PATCH`/`DELETE /api/events/{id}`
   and the conference routes build a **Google** client only
   (`backend/api/handlers_events.go:24-33`, `routes.go:117-129`). An Outlook user
   with a Google credential gets "event not found" (`handlers_events.go:55-58`). An
   Outlook user without one gets the 401 "calendar not connected", while the status
   endpoint truthfully says connected, which contradicts §3.2 ("the two screens
   never disagree"). AC-2c makes feed events read-only. Outlook events get nothing,
   and §5 ("write-side features keep today's behaviour") does not cover them,
   because today every event the grid shows is a Google event that these actions
   *can* write.
   *Resolves when* the spec says what each of those actions does on an Outlook
   event: refused like a feed event (extend AC-2c to Outlook), or written to
   Outlook (in scope, with ACs). Either way, add an AC that no write for an
   Outlook-shown event reaches Google or its stand-in.

3. **Shipping only the read side makes Paceday's own writes invisible to the
   users this spec serves.** No write-side engine reads the provider setting: the
   only readers are the status handler, the free/busy service and the unused
   factory (`grep CalendarProvider`, see §2.1). Focus blocks, habits, compression
   and personal blockers keep writing to the Google calendar of any user who holds
   a Google credential (`backend/engine/cal_iface.go:84-95`;
   `backend/engine/personal_blocker.go:91`). Today an Outlook or WebCal user who
   signed in with Google sees those blocks, because the week view shows Google.
   After this spec, the week view shows only Outlook or the feed (AC-1b, AC-2), so
   the focus time Paceday schedules for them is written to a calendar they are no
   longer shown. That is a user-visible regression for the population in the first
   row of §2.2. §5 calls it "today's behaviour", but the user's experience of it
   changes. "Is it one spec" (§2 rule 7): this half leaves the system incoherent
   unless the spec says what happens.
   *Resolves when* the spec states, as a requirement with an AC, one of the
   following: Paceday stops writing to Google for a user whose chosen calendar is
   not Google (the conservative option: fewest writes to a calendar the user did
   not choose); writes stay but remain visible in the week view; or the
   regression is accepted as an owner decision, recorded under Decisions with the
   affected population named.

4. **The feed reader is already live, so the SSRF and the address-in-log leak
   exist in production today.** §2.1 says `calendar.ReadPersonalEvents` "has no
   caller outside its package", and §2.5 says the unguarded fetch becomes a
   request-forgery surface "the moment anything calls it" (D-5 rests on the same
   premise: "making the feed reader live"). Both are false.
   `backend/engine/personal_blocker.go:72,87` calls it. It runs through
   `GET /api/personal-calendars/{id}/preview`, `POST .../sync`
   (`backend/api/routes.go:107-113`, `handlers_personal.go:24`) and the per-user
   cron sync (`backend/main.go:104`),
   and `e2e/tests/seam.spec.ts:384-413` exercises it. Its `webcal` branch fetches a
   user-typed URL with `http.DefaultClient`
   (`backend/calendar/personal_reader.go:15-17`, `webcal_client.go:60-64`), and the
   sync failure path logs the wrapped `net/url` error, which carries the full feed
   address (`personal_blocker.go:61`). §5 puts personal calendars out of scope,
   but they share this reader, so AC-5/5b/5c either cover them by construction or
   leave a live hole beside the one being closed. The spec has to say which.
   *Resolves when* §2.1/§2.5/D-5 are corrected, and the spec states whether the
   address guard and redaction (AC-5, AC-5b, AC-5c) apply to every server-side
   feed fetch, personal calendars included. If they do not, the live gap is
   recorded as a finding with an owner, not left implicit.

5. **The dashboard has a second "connect" button and a second "Syncing in real
   time", and the spec does not name them.**
   `smart-calendar-flow/src/components/ConnectionStatus.tsx` is rendered on the
   dashboard (`Dashboard.tsx:579`). Its Connect button always starts Google
   (`:12-14`, `api.authConnectUrl()` with no provider), it says "Syncing in real
   time" whenever `connected` is true (`:59`), and its Disconnect toast says
   "Disconnected from Google Calendar" (`:19`). AC-3d tests "the week view's
   connect action" in the singular, so a frontend change that fixes only
   `Dashboard.tsx:455-457` passes while this button still sends a WebCal user to
   Google, which is exactly what §3.3 forbids. OQ-5's "the frontend must not say
   real time for a feed" names neither component.
   *Resolves when* §2.3 lists `ConnectionStatus.tsx`, and AC-3d plus the OQ-5
   resolution are stated for every connect action and status line the dashboard
   and Settings show (Dashboard prompt, `ConnectionStatus`,
   `WorkCalendarConnection`).

### Non-blocking

1. **AC-4 contradicts §3.4.** §3.4 says the answer is "unknown" *for other
   attendees* because a feed carries no information about other people. AC-4
   requires "every attendee", which includes the requesting user, about whom the
   feed *does* carry information. Pick one and make the other match.
2. **§3.4 is absolute, but §5 excludes `GET /api/calendar/freebusy` (API-091).**
   That handler still queries Google for a WebCal user (`routes.go:70`,
   `ch.freeBusy`). Qualify §3.4 to "the free/busy service behind scheduling
   features", or bring the operation into scope.
3. **AC-3 and OQ-2 pull against each other.** OQ-2 leaves open "trust the last
   read's outcome", which by its own description can show a stale "connected" for
   one load. AC-3 requires that "at no moment" is connected shown with an empty
   week. Either narrow OQ-2's options to those that satisfy AC-3, or state the
   observation point AC-3 is tested at (after the events request settles).
4. **SSRF requirements in §3.5 with no AC.** AC-5b checks only the stored host.
   §3.5's "fetches only from addresses on the public internet" also has to hold
   for a redirect from a public feed to a private or link-local address, and for
   a host that resolves differently between check and connect. Add ACs for the
   redirect case and for "the address checked is the address connected to". Also
   state what happens to a stored plain `http://` feed address: D-6 covers only
   `webcal://`, and its rationale (token off the wire) applies equally.
5. **No second-user AC.** Multi-tenancy is the historical weak spot and the
   spec touches per-user credentials and a reader cache. Add an AC with two users
   on different providers that verifies neither user's events, feed contents or
   status reach the other (for example, a cached feed result keyed by something
   other than the user).
6. **The issue's "either way" is narrowed.** The issue says `GET /api/auth/status`
   "must stop reporting a connection it has not verified". The spec leaves Google
   unverified (D-4, OQ-3). That is recorded as an assumption pending owner
   confirmation, which is acceptable, but the parent issue should not be closed by
   this feature while OQ-3 is open.
7. **Overlap with PAC-50.** PAC-50 §4.1 (`docs/specs/PAC-50.md:81`) plans a
   test-mode redirection of the Microsoft Graph base for three call sites,
   including `outlook_client.go:15`. AC-6 requires the same redirection. The spec
   should say which feature owns it, so that two seams are not built.
8. **Disconnect becomes more reachable for the wrong provider.** Once Outlook
   and WebCal users truthfully show "connected", both panels show Disconnect,
   which still deletes the *Google* credential and signs the user out (API-061;
   `ConnectionStatus.tsx:19` even says "Google Calendar"). OQ-4 records the
   question. Consider stating, as a "must not change" or an interim requirement,
   that this spec does not make it worse.

### Criteria I could not falsify

- AC-7 is a gate, not behaviour. It is satisfied by running the gates and needs
  no test. That is fine, but it should not count as an acceptance criterion of the
  feature.
- AC-3 "at no moment": as written, no single test observation can prove a
  negative across time (see Non-blocking 3). It becomes falsifiable once the
  observation point is named.
- Every other criterion has a test that fails today and passes afterwards. For
  example: AC-1/1b fail because `listEvents` builds only a Google client
  (`handlers_calendar.go:137-143`); AC-3c fails for WebCal because status answers
  `connected: s.WebcalURL != ""` (`handlers_auth.go:108`); AC-4 fails because
  `webcal` falls into the Google `default:` (`freebusy_service.go:128-137`); AC-2b
  fails because the scheme is passed to `http.NewRequestWithContext` unchanged
  (`webcal_client.go:60`); AC-5 fails because the fetch error wraps the URL error
  (`webcal_client.go:66`).

### Citations I checked and found wrong

- "`calendar.ReadPersonalEvents` ... also has no caller outside its package"
  (§2.1). Actually called from `backend/engine/personal_blocker.go:72` (Preview)
  and `:87` (sync), reached from `routes.go:112-113` and the cron sync at
  `backend/main.go:104`, and exercised by `e2e/tests/seam.spec.ts:384-413`.
- "A user-supplied address fetched by the server is a request-forgery surface the
  moment anything calls it" (§2.5), and D-5's "making the feed reader live". The
  reader is already live for personal WebCal calendars (above), with no guard.
- "the connect flow ... on success, stores the credential and flips the provider
  to `outlook`" (§2.5). That is incomplete in a way that changes behaviour: it
  stores the credential on the user resolved from the Microsoft email and signs
  the browser in as that user (`backend/api/handlers_microsoft_auth.go:62-81`).
- All other citations I opened match the code: `handlers_settings.go:32,272-273`;
  `storage/settings.go:195-197,239-273`; `client_factory.go:14-34,20,78-79`;
  `handlers_calendar.go:134-144,161-171`; `cal_iface.go:31-44,90`;
  `server_errors.go:59-75`; `handlers_auth.go:69-73,82-133`;
  `freebusy_service.go:119-138`; `webcal_client.go:19-22,52-70`;
  `outlook_client.go:15,63-65,72-76`; `microsoft_oauth.go:49-89,68`;
  `config/endpoints.go:49-60`; `integration_availability_test.go:31-32`;
  `api-audit.md:260,267,334,486`; `scheduling.yaml:2081-2089`; `PAC-50.md:81`;
  `e2e/tests/calendar.spec.ts:27,58,87,165,221`; `Dashboard.tsx:113-114,435-462`;
  `WorkCalendarConnection.tsx:108-122`.
  Minor: the Google call-site list in §2.2 cites `cal_iface.go:90`,
  `analytics.go:216`, `booking.go:208`, `habits.go:255`, which match current code.
  The issue's older numbers differ, and the spec's are the correct ones.

### Consumers this breaks

- smart-calendar-flow/src/components/ConnectionStatus.tsx:12-14,59 — still starts
  Google and still says "Syncing in real time" for a feed once status is verified
  (Blocking 5).
- smart-calendar-flow/src/pages/Onboarding.tsx:61-62,84-85 — skips the connect
  step whenever `connected` is true and always advances for `webcal`. Once
  `connected` means "read successfully", a WebCal user with an unreachable feed
  passes onboarding and lands on a not-connected dashboard. This is not a crash,
  but the spec should say whether onboarding keeps its "webcal always advances"
  shortcut.
- smart-calendar-flow/src/pages/Dashboard.tsx:269,
  src/components/EventEditForm.tsx:95,122,
  src/components/RescheduleSuggestions.tsx:238 — write actions on events that,
  after AC-1/AC-2, are no longer Google events (Blocking 2; AC-2c covers only
  feed events).
- backend/engine/personal_blocker.go:61,72,87 — shares the feed reader. Any
  guard, redaction or refusal added for AC-5/5b/5c changes personal-calendar
  behaviour too, unless the spec says otherwise (Blocking 4).
- backend/api/gen/paceday.gen.go:2185 (and the generated doc comments at
  `:2296,2353,2469`) — repeat the U-18 description the spec says is untrue. They
  are regenerated from the contract, so the contract stage's rewrite of
  `scheduling.yaml:2081-2089` must flow through `make openapi`. Noted so the
  contract challenger checks for it.

## Revision 1 — 2026-10-26

Answers to the round-1 challenge above. The spec body was edited in place; this
section says what changed and where, so the next challenger can check each
answer against the text. All new citations were re-read on `main` at `7de7044`
(api) and `4ded854` (web). Nothing was compiled or run; this is a text stage.

### Blocking

1. **Outlook connect is a sign-in.** Accepted; the challenger's reading is
   correct (`handlers_microsoft_auth.go:62-81`, `users.go:21-31`,
   `routes.go:35-36`).
   - §2.5 now describes the flow as it is: email-resolved user, credential and
     provider stored on that user, session issued for that user. §2.2 adds the
     same-email / different-email distinction to the sign-in table.
   - §3.3 states what "connect Outlook" does to the signed-in account: it
     **attaches the credential to the signed-in account** and keeps the session.
     Sign-in from the sign-in page with nobody signed in is unchanged (§6). This is
     D-7, assumed pending owner confirmation, chosen because the sign-in
     alternative makes §3.3's way back impossible for a user whose emails differ.
   - AC-3d now requires that it reconnects the chosen provider for every connect
     action. New AC-3e checks who holds the credential (A, not B), that B is
     unchanged and no user is created, and that the browser still holds A's
     session. New AC-3f checks that a completion in another user's browser is
     credited to nobody and replaces no session.

2. **Write actions on Outlook events.** Answered by **refusal** (D-8).
   - §2.7 lists the actions and the server paths they reach.
   - §3.7 now covers Outlook as well as feed events: no move, edit, delete,
     reschedule or conference-link action is offered, and a request is refused with
     a read-only answer, not "not found" and not "not connected".
   - New AC-1d checks that no write for an Outlook-shown event reaches the Google
     or the Microsoft stand-in. AC-1e checks that a user with no Google credential
     gets the read-only refusal, not a 401 that contradicts a "connected" status.
     AC-1f checks that the controls are not offered. AC-2c names the same five
     actions for feed events.

3. **Read side alone hides Paceday's own writes.** Answered with the first of the
   three outcomes: **Paceday stops writing to Google for a user whose chosen
   calendar is not Google** (§3.8, D-9; D-2 revised).
   - §2.7 lists every write path: focus time and its cleanup, smart scheduling,
     habits, compression, personal blockers, auto-decline and booking.
   - New AC-8 checks that no write reaches the Google stand-in for Outlook and
     WebCal users and that existing events are unchanged. AC-8b checks that an
     on-demand start says "unavailable", not success. AC-8c covers booking. AC-8d
     keeps Google users unchanged.
   - Nothing already written is deleted (§3.8, §6). D-9 records the cost so the
     owner can weigh it: those features do nothing for Outlook and WebCal users
     until OQ-1 is answered.

4. **Feed reader already live; SSRF and log leak in production.** Accepted. The
   §2.1 claim was wrong and is corrected, with the callers, routes, cron and e2e
   coverage cited. §2.5 now says the request-forgery surface and the
   address-in-log leak (`personal_blocker.go:61`) exist today. D-5 is corrected.
   - Decision (D-10): **AC-5 and AC-5b to AC-5e cover every server-side feed
     fetch, personal calendars included**, and each is tested for both callers.
     §3.5 and §5 say so.
   - New AC-5f checks that a refused or failed personal fetch removes no existing
     blocker, so the guard loses no user data. §6 keeps public WebCal and Google
     personal syncs working.
   - Because the gap is closed rather than left open, no separate finding with an
     owner is needed.

5. **`ConnectionStatus.tsx` missing.** Accepted.
   - §2.3 now names it (`Dashboard.tsx:579`; `:12-14`, `:19`, `:59`) and
     Onboarding's use of the flag (`Onboarding.tsx:61-62,84-85`). It lists the
     three connect actions and three status lines.
   - §3.3 and AC-3d now cover each connect action: the dashboard prompt, the
     dashboard status card and the Settings panel. §3.9 and new AC-3g cover every
     status line: no "Syncing in real time" for a feed, and no "Google Calendar"
     text or disconnect toast for an Outlook or WebCal user.
   - The OQ-5 resolution names the same surfaces. The challenger's Onboarding
     consumer note is answered in §3.9: onboarding may continue, but it never
     shows a calendar as connected unless the status says so.

### Non-blocking

1. AC-4 now matches §3.4: the user's own busy time comes from the feed, and other
   attendees are reported as unknown.
2. §3.4 is qualified to the free/busy service behind scheduling features.
3. AC-3 names its observation point: after the events request settles, and on a
   second load after both requests settle. OQ-2's options remain for the contract
   stage, but any choice has to pass AC-3 as now written.
4. Redirects and check-then-connect are covered by AC-5d and AC-5e. Plain
   `http://` feed addresses are recorded as OQ-7, with a no-new-breakage interim
   assumption.
5. Added a two-user AC: AC-9.
6. OQ-9 records that PAC-47 is not closed while OQ-3 is open.
7. OQ-8 assigns the Graph stand-in seam to whichever of PAC-47 and PAC-50 reaches
   the contract stage first.
8. §6 now says disconnect is not made worse.

The challenger's note that AC-7 is a gate rather than a behaviour is accepted.
AC-7 is relabelled and kept only for traceability to the ticket.
