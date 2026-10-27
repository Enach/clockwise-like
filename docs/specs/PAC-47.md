# Spec — PAC-47: Selecting Outlook or WebCal shows "connected", then an empty calendar

- **Linear**: https://linear.app/paceday/issue/PAC-47
- **Status**: challenged — revised after round 4 (see "Revision 1" to "Revision 4")
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
  - **Added in Revision 2.** Operations this spec changes whose open findings
    Revision 1 did not list (touch-it-fix-it):
    - **API-029** (`docs/factory/api-audit.md:287`, untagged slot and event keys)
      on the calendar free/busy operation (§3.4 changes its answer for Outlook and
      WebCal users) and on the personal-calendar preview (§3.5 changes its
      failure answer). Revision 1 listed it as not inherited, which was already
      wrong for the preview.
    - **API-028, API-046, API-077** (`api-audit.md:286,304,335`) on the
      free/busy query operation, whose WebCal answer AC-4 changes.
    - **API-011 and API-012** (`api-audit.md:269-270`) on the Microsoft
      callback, whose behaviour D-7 changes.
    - **API-064** (`api-audit.md:322`) on the Microsoft start route, if the
      contract stage changes that route to tell a connect from a sign-in (§3.3).
    - **API-091 / U-03** (`api-audit.md:349,471`): API-091 lists the calendar
      free/busy operation as having no consumer, while U-03 records that the MCP
      tool `clockwise_find_free_slots` calls it. The contract stage makes the two
      agree when it changes that operation.
    - No `x-uncertain` marker sits on the meeting-suggestion, booking-slot,
      free/busy query, calendar free/busy, Microsoft start or Microsoft callback
      operations (checked in `contracts/openapi/openapi.yaml` on 2026-10-26).
  - **Not inherited**: API-061 (disconnect also signs the user out). It sits
    on an operation this spec does not require changing, and PAC-35 has since
    fixed it (next item).
  - **Revision 4 — findings fixed on `main` since `7de7044`.** PAC-26 fixed
    API-028 and API-029. PAC-35 fixed API-011, API-012, API-061 and API-064
    (`docs/factory/api-audit.md:269-270,286-287,319,322` on `6d751f9`). They are
    no longer open, so they are no longer inherited, and the contract stage must
    keep them fixed. Still open and still inherited: U-18, API-076, API-046,
    API-077 and API-091/U-03. No `x-uncertain` marker sits on any operation this
    spec changes (re-checked in `contracts/openapi/openapi.yaml` on `6d751f9`).

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
- **Revision 4** re-read the code on `main` at `6d751f9` (api) and `ad52160`
  (web), seven api commits after `7de7044`. What changed for this spec:
  - PAC-35: disconnect no longer signs anyone out. For an Outlook or WebCal user
    it answers 409 and deletes nothing (`backend/api/handlers_auth.go:172-195`).
    The Microsoft callback's failures now end on the sign-in failure state, and
    the Microsoft start route forwards `login_hint`. The callback still upserts
    the user by Microsoft email, stores the credential on that user and sets
    their provider to Outlook on every completion
    (`backend/api/handlers_microsoft_auth.go:69,74`;
    `backend/auth/microsoft_oauth.go:79`).
  - PAC-29: event changes still open only a Google client
    (`backend/api/handlers_events.go:27-35`).
  - Citations in §2 were re-anchored where these commits moved lines. Revision 4
    lists them.

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
  directly: `backend/api/handlers_events.go:27-35`, `backend/engine/cal_iface.go:90`,
  `analytics.go:216`, `auto_decline.go:87`, `booking.go:208`, `habits.go:255`,
  `manager.go:196`, `team_availability.go:85`, `freebusy_service.go:133`.

Consequence by sign-in route:

| Signed in with | Picks | Week view today |
|---|---|---|
| Google (credential stored at sign-in, `backend/api/handlers_auth.go:64-74`) | Outlook or WebCal | Shows the **Google** calendar's events, not the chosen one |
| A route that stores no Google credential (e.g. OIDC SSO) | Outlook or WebCal | 401 → the dashboard shows "Couldn't load your calendar" (`smart-calendar-flow/src/pages/Dashboard.tsx:448-455`) while Settings says connected |

The issue's phrase "an empty calendar" is the second row as the user perceives it;
the first row was not described in the issue and is the more serious one.

For Outlook the table needs one more distinction, because "picking Outlook" in
Settings runs the Microsoft *sign-in* (§2.5): a Google-signed-in user whose
Microsoft account has **the same email** ends up with both credentials on one
Paceday account; one whose Microsoft account has **a different email** is signed
in to a different Paceday account (created if absent), and the account they
started from is unchanged and still on Google.

### 2.3 The connection status reports a connection it has not verified

`GET /api/auth/status` (`backend/api/handlers_auth.go:110-161`):

- `webcal` (`:134-139`): `connected` is `true` whenever a feed address is stored.
  Nothing is fetched.
- `outlook` (`:126-133`): `connected` is `true` whenever a Microsoft refresh token is
  stored. Nothing is fetched, and the token is never used to read events (§2.2).
- `google` (`:140-160`): `connected` is `true` whenever a Google token is stored.
  Nothing is fetched (deliberately, PAC-65).

The frontend trusts this flag. The connection panel shows
"<provider> — connected" and "Syncing in real time" from it
(`smart-calendar-flow/src/components/WorkCalendarConnection.tsx:129-136`), and the
dashboard suppresses its "Connect your calendar" prompt whenever it is `true`
(`smart-calendar-flow/src/pages/Dashboard.tsx:113-114,457-475`). When the prompt
does show, its button always starts the **Google** connection
(`Dashboard.tsx:467-470`, `api.authConnectUrl()` with no provider), whatever the
user chose.

The dashboard has a **second** status surface, which the first version of this
spec missed: `smart-calendar-flow/src/components/ConnectionStatus.tsx`, rendered in
the dashboard sidebar (`Dashboard.tsx:592`). Its Connect button always starts the
Google connection (`ConnectionStatus.tsx:17-19`, `api.authConnectUrl()` with no
provider), it shows "Syncing in real time" whenever `connected` is true (`:71`),
and its Disconnect toast says "Disconnected from Google Calendar" whatever the
provider (`:24`).

Onboarding also trusts the flag: it skips the connect step whenever `connected`
is true (`smart-calendar-flow/src/pages/Onboarding.tsx:61-62`) and always advances
for `webcal` without checking anything (`:84-85`). Its connect step is itself a
connect action, which Revision 1 missed: for a user who picked Outlook and is not
connected it sends the browser to `api.authConnectUrl(provider)`
(`Onboarding.tsx:78-87`), which for `outlook` is the Microsoft route
(`src/api/client.ts:1004-1005`). It runs on a signed-in route, in the user's first
session.

So there are **four** connect actions in the product today: the dashboard's
not-connected prompt, the dashboard's status card (`ConnectionStatus`), the
Settings work-calendar panel (`WorkCalendarConnection`) and onboarding's connect
step. The first three are also the product's three status lines.

The same Microsoft route is also the **sign-in** route. The sign-in dialog sends a
user whose email is detected as Microsoft to `/api/auth/microsoft?login_hint=…`
(`src/components/auth/AuthDialog.tsx:102`), and its "Continue with Microsoft"
button to `/api/auth/microsoft` (`:79`). `/signin` renders the landing page
with that dialog already open (`src/pages/SignIn.tsx`, `src/pages/Index.tsx:587`)
whatever the session state, and nothing checks first whether a session is live.
The server cannot tell a connect from a sign-in today: both reach the same handler,
which ignores the session (§2.5).

### 2.4 Availability reads: four paths, and Google answers in most of them

Revision 1 called `FreeBusyService` "the free/busy service behind scheduling
features". That was wrong. Its only caller is `POST /api/freebusy`
(`backend/api/handlers_freebusy.go:70`), and the only web caller of that
operation is the co-host coverage chips of the scheduling-link editor
(`smart-calendar-flow/src/components/links/LinkEditDrawer.tsx:157`,
`src/api/client.ts:982-984`). The scheduling features read availability elsewhere:

| Read path | Served to | What it reads today |
|---|---|---|
| **Free/busy query**: `FreeBusyService` (`backend/engine/freebusy_service.go:119-138`) | `POST /api/freebusy`, used by the scheduling-link editor | `outlook`: the caller's Microsoft credential (`:122-127`). `webcal`: no case, so it falls into `default:` (`:128-137`), which loads the caller's **Google** credential and queries Google. |
| **Meeting suggestions**: `SmartScheduler.Suggest` (`backend/engine/smart_schedule.go:59-71`) | `POST /api/schedule/suggest` (`backend/api/handlers_schedule.go:168`), used by the dashboard's `ScheduleSuggestModal.tsx:97` and the MCP tools `clockwise_create_event` (`mcp/tools.go:54`) and `clockwise_suggest_meeting` (`:265`) | Free/busy from `newCalOps` (`:56`), which loads the **Google** credential whatever the provider (`cal_iface.go:84-95`). With no Google credential the answer is the 401 "calendar not connected" (`handlers_schedule.go:170`), even while the status may say connected. |
| **Public booking slots**: `BookingEngine.hostBusy` (`backend/engine/booking.go:122-135`) | the unauthenticated `GET /api/book/{slug}/slots` (`backend/api/routes.go:26`), used by `PublicBooking.tsx` | Each host's **Google** events, via `calendarClientForUser` (`:202-209`). If no client can be built, or the read fails, the error is dropped (`:123-125`) and the host has **no busy time**: every slot in the link's hours is offered, including slots over the host's real Outlook or feed meetings. |
| **Calendar free/busy**: `handlers_calendar.go:203-209` | `GET /api/calendar/freebusy` (`routes.go:70`), used by the MCP tool `clockwise_find_free_slots` (`mcp/tools.go:129-139`); no web screen uses it | A **Google** client only, or the 401 without a Google credential. |

So an Outlook user has a different calendar queried on their behalf in three of
the four paths, and a WebCal user in all four. A booking host with no Google
credential looks permanently free to guests.

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
  addresses, including any token in them, are written to the server log today. The
same errors also leave the process for the error tracker, which Revision 1 did not
record. The personal-calendar preview answers a failed fetch through
`writeServerError` (`backend/api/handlers_personal.go:168-171`), and sync answers
through `writeCalendarFailure` (`:191-192`, `backend/api/server_errors.go:75-80`).
Both reach `logServerError`, which writes the log line and then calls
`reportServerError`, which calls `hub.CaptureException(err)`
(`server_errors.go:16-22,41-49`). Wherever an error-tracker DSN is configured, the
full feed address, token included, is therefore sent to a third-party service
today. A
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
  (`backend/api/handlers_microsoft_auth.go:17-84`) is mounted without
  `requireAuth` (`backend/api/routes.go:35-36`) and never reads who is signed in.
  On callback it reads the Microsoft account's email (`:64`), resolves a Paceday
  user **by that email** with `storage.UpsertUser(..., "microsoft", ...)` (`:69`;
  `UpsertUser` is `INSERT ... ON CONFLICT (email) DO UPDATE`,
  `backend/storage/users.go:21-31`, so it creates a user when none has that email),
  stores the Microsoft credential **on that user** and flips that user's provider
  to `outlook` (`:74`; `backend/auth/microsoft_oauth.go:60-81`), and **issues a
  session for that user** (`:79`), replacing whatever session the browser held.
  The Settings picker already labels it "Sign in with Microsoft"
  (`smart-calendar-flow/src/components/WorkCalendarConnection.tsx:22`). Consequence:
  the credential lands on the account the browser was signed in to only when both
  accounts share an email address. The only binding between the start and the
  callback is the `ms_oauth_state` cookie (`handlers_microsoft_auth.go:41-46`), so a
  completion in another browser fails with 400 and stores nothing. A completion in
  the same browser after its session changed is credited by Microsoft email, as
  above.
- **The Microsoft sign-in steps go to fixed addresses.** Authorize and token are
  hard-coded at `backend/auth/microsoft_oauth.go:14-17`
  (`login.microsoftonline.com`), and the profile read that names the account is
  hard-coded at `backend/api/handlers_microsoft_auth.go:90` (`graph.microsoft.com`).
  Since PAC-35 the three have in-process test seams: package variables that a Go
  test in the same package can set (`handlers_microsoft_auth.go:86-90,129`,
  `microsoft_oauth.go:23-41`). No deployment setting redirects them, so a
  backend unit test can complete a Microsoft callback, but no e2e run can.

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
  sync log or the error tracker, or which account the Microsoft callback attaches
  its credential to.
- Two existing fixtures serve a WebCal personal feed from a loopback `httptest`
  server without the test-mode declaration
  (`backend/api/pac21_calendar_not_connected_test.go:58-66`;
  `backend/engine/pac21_calendar_credential_test.go:122,134-141`). §3.5 refuses
  loopback addresses outside a declared test deployment, so both must be adapted
  (§6).
- Google meeting suggestions are covered by
  `TestSmartScheduler_Suggest_WithMock` (`backend/engine/smart_schedule_test.go:137`)
  and `TestSmartScheduler_Suggest_StepsThirtyMinutes`
  (`backend/engine/pac21_calendar_credential_test.go:148`). Booking slots are
  covered by `TestPublicBookingSlotsContract` (`backend/api/handlers_booking_test.go:119`)
  and the engine tests in `backend/engine/booking_test.go` and
  `booking_busy_test.go`. None of them uses a non-Google host or user.

### 2.7 Everything that writes to a calendar writes to Google

No write path consults the provider setting. The only readers of it are the
status handler, the free/busy service and the unused factory (§2.1, §2.3, §2.4).

- **Writes the user starts from the week view.** Drag-to-move
  (`smart-calendar-flow/src/pages/Dashboard.tsx:282`), edit and delete
  (`src/components/EventEditForm.tsx:134,160`), reschedule suggestions
  (`src/components/RescheduleSuggestions.tsx:238`) and add/remove conference link
  (`src/api/client.ts:1469,1483`) call `PATCH`/`DELETE /api/events/{id}` and
  `POST`/`DELETE /api/events/{id}/conference` (`backend/api/routes.go:116-129`).
  The server opens only a Google client for them
  (`backend/api/handlers_events.go:27-35`; conference routes via
  `handlers_conferencing.go:309,405`) and answers "event not found" when the id is
  not a Google event id (`handlers_events.go:252-266`), or 401 "calendar not
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
   not-connected prompt, the dashboard's calendar status card, the Settings
   work-calendar panel and onboarding's connect step) takes a WebCal or Outlook
   user to fix *that* connection, never to a Google connection. An Outlook
   connection that the signed-in user starts from one of those four connect
   actions **attaches the Microsoft credential to the account that started it**,
   whatever email the Microsoft account has. It does not sign the browser in as
   anyone else, does not create a Paceday account, and does not change any other
   Paceday account. It is credited only to the account that started it, and only
   while that account is still the one signed in when the Microsoft step
   completes. In any other case it is credited to nobody and changes no session.

   **Only those connect actions attach.** Signing in with Microsoft from the
   sign-in page is always a sign-in, whether or not a session is already live in
   the browser. It signs the browser in as the Paceday user for that Microsoft
   account (created if absent) and replaces any live session, as today. It never
   stores a Microsoft credential on the user whose session was live, and never
   changes that user's work calendar (D-14). The action that started a flow
   decides whether it is a connect or a sign-in. A session being present when the
   flow completes does not make it a connect.

   **A sign-in sets a work calendar only for the account it creates** (D-17).
   This is the one change to signing in. A Microsoft sign-in affects the work
   calendar of the Paceday user it signs in as follows:
   - *A new user*, created by this sign-in: their work calendar is Outlook, as
     today. They have no other calendar to read.
   - *An existing user whose work calendar is the default*: it does not change.
     The product cannot tell the default from a deliberate Google choice, because
     both are stored as Google. This case is therefore handled like the next one.
   - *An existing user who chose Google, WebCal or Outlook*: it does not change,
     and a stored feed address is kept. The Microsoft credential that the sign-in
     obtains is still stored on that user, as today. It is read only if their
     work calendar is Outlook.

   An existing user who wants Outlook chooses it through one of the four connect
   actions. That is a choice, so it makes their work calendar Outlook (AC-3e).
   Today every Microsoft sign-in sets the work calendar to Outlook, whoever the
   user is (§2.5).
4. **Availability is read only from the calendar the user chose, or not at
   all.** No availability read made for an Outlook or WebCal user queries Google,
   even when a Google credential is on file. There is one exception, and it ends
   when PAC-67 is released: until then, the public booking slots of a host whose
   work calendar is Outlook are computed as today (the *interim*, described in
   the booking item below; D-13). For each read path of §2.4:
   - *The free/busy query behind the scheduling-link editor* answers from the
     chosen calendar. A feed describes the user's own time, so the user's own busy
     time comes from the feed. It carries no information about other people, so
     other attendees are "unknown", the same as any other unanswerable attendee
     today. The user's own row comes from their chosen calendar whatever the
     domain of their own address. No answer computed before the user changed
     their work calendar is reused after the change. For Outlook it already reads
     Microsoft, and it keeps doing so.
   - *Meeting suggestions* are **unavailable** for an Outlook or WebCal user. This
     covers the dashboard's suggestion dialog and the agent tools that suggest or
     create a meeting (D-11, under D-9). The answer says so. It is not a list of
     suggestions and not "calendar not connected".
   - *Public booking slots* for a host whose work calendar is Outlook or WebCal
     are computed from that host's chosen calendar (D-13). If that calendar cannot
     be read, no slot that needs that host is offered. A host is never treated as
     free because their calendar could not be read. The guest is then shown no
     free times for that day or period, never an error. Guests still see slot
     times only, never event details. Google hosts are unchanged (§8 OQ-10).
     For an Outlook host this behaviour is released only once Outlook credentials
     are refreshed (PAC-67, D-13). Until then (the *interim*), an Outlook host's
     slots are computed exactly as today. This is the exception named in the
     opening rule of this item. In the interim, for an Outlook host:
     - if the host holds a Google credential, their busy time is read from their
       Google calendar, and slots over those Google events are not offered;
     - if the host holds no Google credential, they have no busy time, and every
       slot in the link's hours that the other hosts allow is offered;
     - in both cases, the host's Microsoft calendar is not read for booking slots,
       and no guest is shown an error because of it.

     The interim keeps today's known defects for those hosts (§2.4): guests can
     book over their Outlook meetings. The read bound below does not yet apply
     to them, so their Google reads stay unbounded, as Google hosts' reads do
     (OQ-11). WebCal hosts are not in the interim.

     **Anonymous guests cannot make the server read a host's calendar
     repeatedly.** A guest's request for a link's slots, whatever period it
     covers, causes at most one read of each WebCal host's calendar and, once
     the interim ends, of each Outlook host's calendar.
     Further slot requests for any link of that host within five minutes of that
     read cause no further read, whether the read succeeded or failed (D-16).
     Changing the work calendar or the feed address discards anything read
     before the change.
   - *The calendar free/busy read used by the agent tools* is **unavailable** for
     an Outlook or WebCal user (D-12), with the same kind of answer as meeting
     suggestions.

   Google users get the same answers as today on all four paths.
5. **A feed address is a secret and an untrusted destination, wherever the server
   fetches one.** This applies to every server-side feed fetch: the work calendar
   *and* personal calendars, which use the same reader today. Private iCal
   addresses embed an access token, so the address leaves the process in no error
   report of any kind: not in a response, not in the server log, and not in the
   error tracker or any other third-party report. The server fetches only from
   addresses on the public internet; in a deployment declared as a test deployment
   it may instead reach only the same non-routable stand-in ranges the calendar
   stand-in already uses, and nothing else. The rule holds for every hop: a
   redirect is followed only to an address that would itself be allowed, and the
   address that was checked is the address connected to. A `webcal://` address is
   read as its HTTPS equivalent. A refused or failed personal-calendar fetch is
   treated like any other failed read: it removes none of the blocks already
   placed from that calendar.
6. **Outlook can be proven against a stand-in, end to end.** Under the same
   test-mode declaration that already governs the Google calendar stand-in, both
   the Microsoft calendar reads and every Microsoft sign-in step can be redirected
   to stand-ins. The sign-in steps are authorize, token exchange, and the profile
   read that names the account. Each redirection is refused at startup without the
   declaration, exactly as the Google redirection is (D-15).
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
   continue to the next step. The product's agent tools are a surface too. No
   tool description or answer calls the user's chosen calendar "Google Calendar".
   The tools that change or delete an event pass on the read-only refusal of §3.7
   as a read-only refusal, not as a generic failure.

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
prompt, the dashboard's calendar status card, the Settings work-calendar panel and
onboarding's connect step), then each leads to reconnecting the provider they
chose (for WebCal, to editing the feed address; for Outlook, to the Microsoft
connection), and none starts a Google connection.  [e2e]

AC-3e. Given user A signed in with Google as `a@x`, a separate existing Paceday
user B whose email is `b@y`, and a Microsoft stand-in account whose email is
`b@y`, when A connects Outlook and completes the Microsoft step, then A holds the
Microsoft credential and A's work calendar is Outlook; B's credentials and
settings are unchanged; no Paceday user is created; and the browser's session is
still A's (the next authenticated request is answered for A). The test runs once
from each of the four connect actions: Settings, onboarding's connect step (with
A a user who has not finished onboarding, who then continues onboarding as A), the
dashboard's not-connected prompt and the dashboard's status card.  [e2e]

AC-3f. Given user A started an Outlook connect from a connect action, and in the
same browser A's session is then replaced by user C's session (or signed out)
before the Microsoft step completes, when the Microsoft step completes in that
browser, then no Microsoft credential is stored on A or C, neither's work calendar
changes, the browser still holds C's session (or none), no Paceday user is
created, and no session is issued.  [unit]

AC-3h. Given user Y signed in with Google (work calendar Google) in a browser, and
a Microsoft stand-in account `x@z`, when sign-in with Microsoft is started from
the sign-in page in that browser and completed as `x@z`, then Y holds no
Microsoft credential and Y's work calendar is still Google, and the browser is
signed in as the Paceday user for `x@z` (created if absent), as it would be with
nobody signed in. Its outcome is today's behaviour, but it can only run once the
Microsoft sign-in stand-ins of §3.6 exist, so it is a regression guard for the new
code rather than a check of today's. It guards against an implementation of D-7
that treats "a session is present" as "this is a connect".  [e2e]

AC-3i. Given user A signed in (work calendar Google) and a Microsoft stand-in
account that has already consented, when a Microsoft flow that was not started
from one of the four connect actions (for example a link on another site that
starts it directly) completes in A's browser, then A holds no Microsoft
credential, A's work calendar is unchanged, and no other Paceday user's
credential or work calendar is changed.  [unit]

AC-3j. Given no Paceday user has the email `new@z`, when sign-in with Microsoft is
started from the sign-in page and completed as the Microsoft stand-in account
`new@z`, then a Paceday user `new@z` exists, holds the Microsoft credential, and
their work calendar is Outlook. This is today's outcome, so AC-3j is a regression
guard for D-17's first case. It cannot run until the §3.6 stand-ins exist.  [e2e]

AC-3k. Given an existing Paceday user `g@z` who signed in with Google, holds a
Google credential and has never saved a work calendar (so it reads as Google, the
default), when they sign in with Microsoft from the sign-in page as the Microsoft
account `g@z`, then their stored work calendar is still Google, the connection
status still names Google, and the Microsoft credential is stored on them. Today
it fails, because the work calendar becomes Outlook.  [unit]

AC-3l. Given an existing Paceday user `w@z` whose work calendar is WebCal with a
stored feed address, when they sign in with Microsoft from the sign-in page as the
Microsoft account `w@z`, then their work calendar is still WebCal, the feed address
is unchanged, and the connection status still names WebCal. The test is repeated
for an existing user who saved Google explicitly in Settings, whose work calendar
stays Google. Today both fail, because the work calendar becomes Outlook.  [unit]

AC-3g. Given a WebCal user whose feed has been read successfully, when the
dashboard and Settings are shown, then neither says "Syncing in real time"; and
given an Outlook or WebCal user, when any status line, connect or disconnect
message is shown, then none names Google Calendar.  [e2e]

**Free/busy**

AC-4. Given a WebCal user who also holds a Google credential, and a feed with one
event this week, when the free/busy query behind the scheduling-link editor is
asked about the user and two other attendees, then no request reaches Google (or
its stand-in), the user's busy time is the feed event, and both other attendees'
coverage is reported as unknown. The test runs twice: once with the user's own
address on a company domain and once on a personal-mail domain (`@gmail.com`);
the user's busy time is the feed event in both. It also runs from a warm cache:
the same query is first answered while the user's work calendar is Google, the
user then switches to WebCal, and the second answer is the feed event, not the
Google answer.  [unit]

AC-4b. Given an Outlook user and, separately, a WebCal user, each holding a Google
credential, when meeting suggestions are requested the way the dashboard's
suggestion dialog and the agent tools request them, then the answer says
suggestions are unavailable for their calendar (not a list of suggestions, not
"calendar not connected"), and no request reaches the Google stand-in. The same
users without a Google credential get the same answer.  [contract]

AC-4c. Given that answer, when the dashboard's suggestion dialog is used, then it
says suggestions are unavailable for the user's calendar. It does not say
"connect your calendar" and does not show an empty list as if there were no free
time.  [unit]

AC-4d. Given a scheduling link whose only host's work calendar is Outlook, a host
who also holds a Google credential, a Microsoft stand-in serving "Board" Tue
10:00–11:00 UTC and a Google stand-in serving "GOOGLE-ONLY" Thu 10:00–11:00 UTC,
both inside the link's hours, when a guest asks for that week's slots, then no
offered slot overlaps Tue 10:00–11:00, slots overlapping Thu 10:00–11:00 are
offered, and no request reaches the Google stand-in. The test is repeated with a
WebCal host whose feed serves "Board".  [unit] The Outlook case is released with
PAC-67 (D-13); its test is written with the rest.

AC-4e. Given a scheduling link whose host's work calendar is Outlook (the
Microsoft stand-in answers 401) or WebCal (the feed does not answer), and a host
who holds no Google credential, when a guest asks for slots, both for one named
day and for the whole bookable period, then no slot that needs that host is
offered. No answer offers that host's slots as if they were free. The named-day
request is answered with no free times, not with an error; the whole-period
request lists no bookable day.  [unit] The Outlook case is released with PAC-67
(D-13); its test is written with the rest.

AC-4g. Given a scheduling link whose only host's work calendar is WebCal, and a
feed stand-in that counts requests and serves "Board" Tue 10:00–11:00 UTC, when a
guest asks for the whole bookable period's slots, then asks for Tuesday's slots,
and a second guest asks for the whole period again, all within five minutes, then
the feed stand-in records exactly one fetch and no Tuesday slot overlaps
10:00–11:00. The test is repeated with an Outlook host against the Microsoft
stand-in: exactly one calendar read is recorded (continuation pages of that one
read count as part of it). Today it fails: the feed is never fetched and the
Microsoft stand-in is never called for a host without a Google credential.  [unit]
The Outlook case is released with PAC-67 (D-13); its test is written with the
rest (see "Release-gated tests" below).

AC-4h. Given the same WebCal link and a feed that does not answer, when guests
make ten slot requests for that link within five minutes, then at most one
connection to the feed address is attempted, and every answer offers no slot that
needs that host. Given the same host's feed address is then changed, when a guest
asks again within those five minutes, then the new address is read.  [unit]

AC-4i. *The interim, until PAC-67 is released.* Given a scheduling link whose only
host's work calendar is Outlook, a Microsoft stand-in serving "Board" Tue
10:00–11:00 UTC, and a Google stand-in serving "GOOGLE-ONLY" Thu 10:00–11:00 UTC,
both inside the link's hours:
(a) when the host holds a Google credential and a guest asks for that week's
slots, then no offered slot overlaps Thu 10:00–11:00, slots overlapping Tue
10:00–11:00 are offered, and no request reaches the Microsoft stand-in;
(b) when the host holds no Google credential and a guest asks for that week's
slots, then slots overlapping both Tue and Thu 10:00–11:00 are offered, no request
reaches the Microsoft stand-in, and both the named-day and the whole-period
requests are answered without an error.
AC-4i pins today's behaviour, so it passes today. It is a regression guard: it
stops the interim from changing by accident, for example through a half-built
Outlook read. It is retired by the change that releases the Outlook half of D-13
(next paragraph).  [unit]

**Release-gated tests.** The Outlook cases of AC-4d, AC-4e and AC-4g are written
with the rest of this spec's tests. They are admitted as skipped tests whose skip
comment names PAC-67 (README §3 rule 4). AC-4i is written as a live test. Until the
release, AC-7's gates therefore run with those three cases skipped and AC-4i live.
The change that releases the Outlook half of D-13, after PAC-67 is released,
un-skips the three cases and retires AC-4i in the same pull request, because AC-4i
then describes behaviour this spec no longer wants. That retirement is a recorded
product change in that pull request, never a way to make a red run green.

AC-4f. Given an Outlook user and, separately, a WebCal user, each holding a Google
credential, when the calendar free/busy read used by the agent tools is
requested, then the answer says it is unavailable for their calendar and no
request reaches the Google stand-in. For a Google user the answer is unchanged.
[contract]

**The feed address**

AC-5 and AC-5b to AC-5e apply to **every server-side feed fetch**: each is
tested once for the work calendar and once for a WebCal personal calendar
(preview, and the sync that the cron also runs).

AC-5. Given a feed address containing a query-string token and a fetch that
fails, when the failure is logged, reported and answered, then the token and the
full address appear in none of these: the server log line, the error-tracker
report (observed where the server hands an error to the tracker), or any response
body.  [unit]

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
work calendar, when a sync's feed fetch is refused, fails, or gets a non-2xx
answer whose body still parses as an iCalendar document (for example a 503 served
as `text/calendar` with no events), then every existing blocker is still in place.
The non-2xx case is the one that fails today.  [unit]

**Stand-in redirection**

AC-6. Given a deployment that redirects Microsoft calendar traffic, or any
Microsoft sign-in step (authorize, token exchange, profile read), to a stand-in
without the test-mode declaration, when the server starts, then it refuses to
start, with the same behaviour the Google calendar redirection has today.  [unit]

AC-6b. Given a declared test deployment with every Microsoft address redirected to
stand-ins, when the Outlook journey of AC-3e runs, then it completes and no
request leaves for a Microsoft address.  [e2e]

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
  convenient. Nothing refreshes the Microsoft access token today. In practice,
  every Outlook user is therefore shown not connected about one access-token
  lifetime (roughly an hour) after each connect, and has to reconnect. This is
  recorded in D-1 so the owner can weigh it. The factory recommends PAC-67 as a
  prerequisite for releasing the Outlook half to users. For the Outlook
  booking-slot behaviour of §3.4 it is a **requirement**, not a recommendation:
  that part is released only after PAC-67 is released (D-13). The WebCal half
  does not depend on it. **Who owns which criterion** (added in Revision 4):
  PAC-67's draft acceptance also asks that the status report not connected for a
  dead Outlook credential. That criterion is owned here (§3.2, AC-3b, AC-3c),
  because this spec makes the status a verified claim. PAC-67 reuses it and owns
  only refresh, revocation and the audit entry.
- **Withdrawing Outlook or WebCal from the picker.** Rejected: PRODUCT.md lists
  both as supported calendars (see Decisions D-1).
- **Personal calendars**, except for the feed-fetch rules. The personal-calendar
  feature's settings and screens do not change. Its WebCal fetches *are* in scope
  for §3.5 (address guard, redirect and lookup rules, redaction), because they use
  the same reader and the gap is live today (§2.5). Its blocker writes are in scope
  for §3.8 only.
- **What "disconnect" does for an Outlook or WebCal user.** Since PAC-35 it
  answers 409 and deletes nothing, and it never signs anyone out
  (`backend/api/handlers_auth.go:172-195`; API-061 fixed). Not changed here;
  recorded as §8 OQ-4.
- **Computing meeting suggestions, or the agent tools' calendar free/busy, from
  Outlook or a feed.** Both are reported unavailable for Outlook and WebCal users
  (§3.4, D-11, D-12), not answered from the chosen calendar. Affected population:
  every Outlook and WebCal user. Until OQ-1 is answered they have no meeting
  suggestions in the dashboard or in the agent tools, and no free-slot search in
  the agent tools.
- **Booking slots for a Google host whose calendar cannot be read.** Such a host
  is still offered as free (§2.4). Changing that changes Google users' booking
  pages; it is recorded as §8 OQ-10.
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
  the Paceday user for that Microsoft email, as today. Its effect on the work
  calendar changes only as D-17 says (AC-3j to AC-3l). No test pins the sign-in
  itself today; the plan stage adds one beside AC-3e.
- **Disconnect is not made worse.** Its behaviour for Outlook and WebCal users
  (409, nothing deleted, session kept; PAC-35) is unchanged by this spec except
  for the wording of §3.9; it must not start deleting anything. Protected by
  `e2e/tests/calendar-disconnect.spec.ts` and `backend/api/pac35_auth_flows_test.go`.
- **Fixtures that make a user Outlook through the credential store keep testing
  what they test.** `backend/api/pac24_scope_test.go:132,156` and
  `backend/api/pac35_auth_flows_test.go:645` store a Microsoft credential in order
  to make a test user an Outlook user. If D-17 is built by moving the provider
  change out of the credential store, those fixtures set the work calendar
  explicitly instead, and their assertions do not change. They are never skipped
  or deleted.
- **Personal-calendar syncs from public WebCal addresses and from Google personal
  calendars keep working.** Protected for Google by `e2e/tests/seam.spec.ts:384-413`;
  nothing protects the WebCal branch today (§2.6).
- **Nothing a feature already wrote to a user's Google calendar is removed** by
  §3.8 taking effect.
- **Google users' meeting suggestions, booking slots and agent tools answer as
  today.** Protected by `TestSmartScheduler_Suggest_WithMock`,
  `TestSmartScheduler_Suggest_StepsThirtyMinutes`, `TestPublicBookingSlotsContract`,
  `backend/engine/booking_test.go`, `booking_busy_test.go` and `mcp/tools_test.go`.
  No test pins the agent tools' calendar free/busy read for a Google user against
  the server; the plan stage adds one beside AC-4f.
- **The existing loopback WebCal fixtures keep testing what they test.**
  `backend/api/pac21_calendar_not_connected_test.go:58-66` and
  `backend/engine/pac21_calendar_credential_test.go:122,134-141` serve a feed from
  loopback without the test-mode declaration, which §3.5 refuses. They are adapted
  (under the declaration, or on an allowed stand-in address). They are never
  skipped or deleted.
- **Signing in with Microsoft from the sign-in page while a session is live**
  still signs in as the Microsoft account's user and replaces the session, as
  today (D-14, AC-3h).

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
  Microsoft credential / feed address? Since PAC-35 it answers 409 and deletes
  nothing for these users (§5). Owner: product. Resolution: PAC-35's OQ-5 (a
  follow-up issue) owns it; this spec does not change disconnect.
- **OQ-5 — feed refresh interval.** The reader's 15-minute cache is ineffective
  today (§2.5). How fresh must a feed be? "Syncing in real time" is not true for a
  feed under any implementation. Owner: product, for the copy; contract-author,
  for the cache. Resolution: no status line on the dashboard (prompt and status
  card) or in Settings says "real time" for a feed (§3.9, AC-3g); the exact
  wording is a frontend-stage choice.
- **OQ-6 — size and time limits on a feed.** A feed is user-chosen and can be
  arbitrarily large or slow. Owner: contract-author. Resolution: state limits in
  the contract; this spec only requires that exceeding them is reported as not
  connected (AC-3) rather than hanging the week view. **The public booking page is
  a consumer of these limits too** (added in Revision 3): a guest's slot request
  reads every Outlook or WebCal host's calendar, so a slow or oversized feed, or a
  slow Microsoft answer, must end that host's read within the same limits and
  count as unreadable (no slot needing that host is offered, §3.4). How often a
  guest can cause such a read is fixed by §3.4 and D-16, not left to this
  question.
- **OQ-11 — bounding Google reads from the public booking page.** A whole-period
  slot request reads each Google host's calendar once per day of the bookable
  period, and nothing bounds repeated requests. The reflector concern of §3.4
  does not apply (the address is Google's, not user-typed), but the volume falls
  on the host's Google quota. Google hosts are unchanged here (§6). Until PAC-67
  is released, the same applies to Outlook hosts, whose slots are computed as
  today in the interim (§3.4; added in Revision 4). Owner: contract-author, with
  product (Nicolas Hochart) for the freshness cost. Resolution: a follow-up issue
  alongside OQ-10.
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
  it. The Graph seam covers the calendar reads and the profile read, which is a
  Graph address. It does not cover the sign-in authorize and token addresses on
  `login.microsoftonline.com`, which AC-6/AC-6b also require (§3.6). PAC-47's
  contract stage owns those, whichever feature builds the Graph seam.
- **OQ-10 — booking slots for a Google host whose calendar cannot be read.** Today
  such a host is offered as free (§2.4). §3.4 makes Outlook and WebCal hosts fail
  closed; Google hosts are unchanged here because the change would alter Google
  users' booking pages. Owner: product (Nicolas Hochart). Resolution: a follow-up
  issue; the factory recommends fail-closed for every host.
- **OQ-12 — users already switched to Outlook by an earlier sign-in.** Before
  D-17, every Microsoft sign-in set the work calendar to Outlook, so some users
  are stored as Outlook without having chosen it. D-17 rewrites no stored value,
  so they stay Outlook. After this spec, Outlook decides their week view, writes
  (D-9) and booking slots (D-13). Should the product tell them, or offer to switch
  back? Owner: product (Nicolas Hochart). Resolution: a decision on a follow-up
  issue. Until then nothing is rewritten, because the stored value cannot be told
  apart from a real choice and changing it unasked is what D-17 forbids. These
  users can change it in Settings.
- **OQ-9 — closing the parent issue.** The issue asks that the status endpoint stop
  reporting any unverified connection; Google stays unverified here (D-4, OQ-3).
  Owner: product. Resolution: PAC-47 is not closed by this feature while OQ-3 is
  open; the stage PRs use `Part of PAC-47`.

## Decisions

- **D-1 — honour the setting rather than withdraw the options.** *Assumed by the
  factory, pending owner confirmation.* PRODUCT.md lists Microsoft 365/Outlook and
  read-only webcal/ICS feeds as calendars Paceday connects to; withdrawing them
  would contradict a recorded product fact, and the ticket's acceptance criteria
  require both to list events. Cost recorded in Revision 2: until PAC-67 adds
  token refresh, an Outlook user is shown not connected about an hour after each
  connect (§5).
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
- **D-11 — meeting suggestions are unavailable for Outlook and WebCal users.**
  *Assumed by the factory, pending owner confirmation.* Reading Google contradicts
  §3.4. D-9 already stops smart scheduling's writes for these users, and a
  suggestion is the first half of that flow: the agent's create-event tool asks for
  suggestions and then creates the event. Offering suggestions whose booking is
  then refused is a dead end. Computing them from Outlook or a feed (attendee
  free/busy on another provider) belongs to the follow-up of OQ-1. Cost: every
  Outlook and WebCal user loses meeting suggestions until then.
- **D-12 — the agent tools' calendar free/busy read is unavailable for Outlook and
  WebCal users.** *Assumed by the factory, pending owner confirmation.* Its only
  consumer is one agent tool. Answering "unavailable" ends the Google read with the
  smallest change. Answering from the chosen calendar would duplicate the
  scheduling-link editor's query (§3.4), and a follow-up can do that. Cost: the
  agent's free-slot search does not work for these users.
- **D-13 — booking slots are computed from each host's chosen calendar, and fail
  closed when it cannot be read.** *Assumed by the factory, pending owner
  confirmation.* Each alternative harms someone. Keeping Google lets guests book
  over the host's real meetings, and a host with no Google credential looks
  permanently free. "Unavailable" makes every booking link of such a host
  unusable. Reading the calendar the host chose tells guests only which slot times
  are free, the same kind of information a booking link exists to publish and
  already publishes from Google. It shows no event detail and widens nothing.
  Failing closed when the calendar cannot be read prevents double-booking; the cost
  is that such a host offers no slots until their calendar is readable again.

  Cost, restated in Revision 3 so the owner can weigh it. Slots are collective:
  one unreadable host empties the whole link. Without token refresh, "readable
  again" would mean a manual reconnect or sign-in roughly every hour, and the
  population is not only users who picked Outlook in Settings. Every Microsoft
  sign-in makes the user an Outlook user. So it would cover every user who has
  ever signed in with Microsoft, and every collective link on which such a user
  is an accepted co-host, including links owned by Google hosts. Their booking
  pages would offer nothing almost all the time, where today they offer every
  slot. That is why the Outlook half of this decision is released only after
  PAC-67 (token refresh) is released. Until then an Outlook host's slots are
  computed exactly as today, and that interim keeps today's known defects for
  those hosts (§2.4). After PAC-67, a host whose Microsoft credential is revoked
  or whose refresh fails still offers no slots until they reconnect; that
  residual cost is accepted. The WebCal half is released with the rest of this
  spec: a feed needs no token, so its population is only hosts who chose WebCal,
  and their pages go empty only while the feed itself is unreadable.

  Revision 4: after D-17, a sign-in makes only a new user an Outlook user. Users
  whom earlier sign-ins set to Outlook stay Outlook (OQ-12), so the population
  above is unchanged at release. The interim for Outlook hosts is specified in
  §3.4 and pinned by AC-4i.
- **D-14 — a Microsoft sign-in from the sign-in page stays a sign-in when a session
  is live.** *Assumed by the factory, pending owner confirmation.* Attaching it
  instead would give the session holder the calendar of whoever completes the
  Microsoft step, which widens who can see data. Refusing to sign in while a
  session is live would be new behaviour that strands a user on a shared machine.
  Today's behaviour (sign in as the Microsoft account's user, replace the session)
  is kept, and it attaches nothing to the user whose session was live. Its
  effect on the signed-in user's work calendar is D-17.
- **D-15 — the Microsoft sign-in steps get test-mode stand-ins, so AC-3e stays
  end to end.** *Assumed by the factory, pending owner confirmation.* AC-3e is the
  only check of D-7's account binding through the browser, the cookie and the
  session. Relabelling it as a unit test would leave exactly that journey
  untested. The stand-ins sit under the PAC-43 declaration and its refusal at
  startup, so production gains no new redirection.
- **D-16 — a host's calendar is read at most once per guest request and at most
  once per five minutes across guest requests.** *Assumed by the factory, pending
  owner confirmation.* An anonymous guest must not be able to turn the booking
  page into a reflector toward a user-typed address, or exhaust a host's
  Microsoft quota and so blank their page. A failed read counts, so an
  unreachable or throttling target is not retried per request. Five minutes is
  shorter than the 15-minute freshness the free/busy query already uses, so it
  adds no staleness the product does not already accept. The cost: a meeting the
  host adds can be offered as free for up to five minutes, and a host whose read
  failed offers no slots for up to five minutes after the cause is fixed. A
  change of work calendar or feed address discards the remembered read, so a
  host who fixes the address sees the effect at once. What is remembered is the
  host's busy times only, used only for that host's slots, and never shown to
  anyone beyond the slot times a guest already sees.
- **D-17 — a Microsoft sign-in sets the work calendar only for the user it
  creates.** *Assumed by the factory, pending owner confirmation.* Added in
  Revision 4. After this spec the work calendar decides the week view, the status
  line, every write (D-9), meeting suggestions (D-11) and booking slots (D-13). A
  sign-in that silently resets it changes all of those without the user asking,
  which contradicts §3.1. This option rewrites the fewest stored choices: it never
  overwrites one, and it keeps today's outcome for a new Microsoft user, who has
  no other calendar. The stored default cannot be told apart from a deliberate
  Google choice (the column is `NOT NULL DEFAULT 'google'`,
  `backend/storage/migrations/003_calendar_providers.up.sql:1`), so both are left
  alone. Rejected options:
  - Keeping the overwrite. One Microsoft sign-in would stop every Paceday write
    for a Google user who shares an email with their Microsoft account, and would
    replace a WebCal user's feed in the week view.
  - Switching only "default" users to Outlook. The default cannot be told apart
    from a choice.
  - Rewriting stored values for users that earlier sign-ins switched (OQ-12).

  Cost, so the owner can weigh it: an existing user who created their account
  with Google or SSO and now signs in only with Microsoft keeps Google as their
  work calendar. If they hold no Google credential, they are shown not connected,
  and the connect actions lead them to Google, their chosen calendar, until they
  pick Outlook in Settings. For an existing Outlook user, a Microsoft sign-in
  still stores the fresh credential, so it still reconnects them (D-13).

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

## Challenge — 2026-10-26 (round 2)

Reviewed by `spec-challenger` against the committed spec at `2acafa5` (Revision 1)
on `factory/pac-47-spec`. The code was read on `main` at `7de7044` (api) and
`4ded854` (web), which are still the tips of both `main` branches. I also read the
Linear issue text. I read no author reasoning beyond this file. I opened and read
every citation below. I compiled and ran nothing, because this is a text-only stage.

Revision 1 answers all five round-1 blocking findings in the text. The round-1
citations I re-checked still hold. The findings below are new, and most of them
are in code paths that Revision 1 brought into scope.

**Verdict**: accept-with-changes

### Blocking

1. **Three scheduling read paths still answer an Outlook or WebCal user from
   Google, and no section accounts for them.** §2.4 calls `FreeBusyService` "the
   free/busy service behind scheduling features". In fact its only caller is
   `POST /api/freebusy` (`backend/api/handlers_freebusy.go:70`), and the only web
   caller of that operation is the scheduling-link drawer
   (`smart-calendar-flow/src/components/links/LinkEditDrawer.tsx:157`,
   `src/api/client.ts:927`). The scheduling features themselves read availability
   in other places:
   - **Meeting suggestions.** `SmartScheduler.Suggest` gets free/busy from
     `newCalOps` (`backend/engine/smart_schedule.go:56,65-71`), which loads the
     **Google** credential (`cal_iface.go:84-95`). That path serves
     `POST /api/schedule/suggest` (`handlers_schedule.go:162`), the dashboard's
     `ScheduleSuggestModal.tsx:97` and the MCP tools `clockwise_create_event` and
     `clockwise_suggest_meeting` (`mcp/tools.go:54`). An Outlook or WebCal user who
     holds a Google credential gets suggestions computed from Google. One without
     a Google credential gets the 401 "calendar not connected" while the status
     reports connected, which is the disagreement §3.2 forbids.
   - **Public booking slots.** `BookingEngine.hostBusy` reads the host's
     **Google** events (`backend/engine/booking.go:122-135`, via
     `calendarClientForUser` `:202-209`). It serves the unauthenticated
     `GET /api/book/{slug}/slots` (`routes.go:26`). For an Outlook or WebCal host,
     guests are offered slots computed from a calendar the host did not choose. A
     host with no Google credential has no busy time at all, so guests can book
     over the host's real Outlook meetings. AC-8c covers only the event that a
     booking creates.
   - **`GET /api/calendar/freebusy`.** §5 excludes it as "used by no frontend
     screen". That is true of the web app, but the MCP tool
     `clockwise_find_free_slots` calls it (`mcp/tools.go:129-139`). The handler
     opens only a Google client (`handlers_calendar.go:203-209`). After AC-1, an
     agent therefore lists the user's Outlook events (`mcp/tools.go:29`) and gets
     free time from their Google calendar.

   §3.4 says such a user's free/busy "is never answered by querying another
   provider on their behalf". AC-4 tests only `FreeBusyService`, and §5's list of
   reads that stay on Google names only analytics, manager views and team
   availability. Under D-9, smart scheduling is "unavailable" for these users, but
   §3.8 and AC-8b are written about writes, so whether suggestions stop is left
   undecided.
   *Resolves when* §2.4 names the real read paths, and the spec states for each
   one (meeting suggestions, booking slots for a non-Google host, and
   `GET /api/calendar/freebusy`) whether it is answered from the chosen calendar,
   reported unavailable under D-9, or kept on Google as an out-of-scope item. An
   out-of-scope item must be listed in §5 with the affected population and a
   Decisions entry. Each path that changes needs an AC proving that no request
   reaches the Google stand-in for an Outlook or WebCal user.

2. **Feed addresses, with any token they contain, are sent to Sentry, and
   §3.5/AC-5 do not forbid it.** §3.5 says the address never appears "in an error
   shown to the user or written to the server log", and AC-5 checks only "the
   server log line" and "any response body". The personal-calendar preview and
   sync handlers answer a failed fetch through `writeServerError`
   (`backend/api/handlers_personal.go:168-171`) and `writeCalendarFailure`
   (`:191-192` → `server_errors.go:75-80`). Both reach `logServerError`, which
   calls `reportServerError` → `hub.CaptureException(err)`
   (`server_errors.go:16-22,41-49`). The wrapped error carries the full URL
   (`webcal_client.go:66`). So a third-party service receives the feed address
   today, and the spec allows that to continue, and to extend to the work-calendar
   reads it creates. §2.5 also does not mention this leak.
   *Resolves when* §2.5 records the error-tracker leak, §3.5 says the address
   leaves the process in no error report of any kind (log, error tracker or
   response), and AC-5 observes the error-tracker report as well. That report is
   already a test seam: `reportServerError` is a variable "so tests can observe
   it".

3. **The spec never says what happens when someone signs in with Microsoft from
   the sign-in page while another user's session is live. Under D-7, that
   attaches a stranger's calendar to the session holder's account.** §3.3 and §6
   define two cases: a connect "while signed in" attaches the credential, and a
   sign-in "with nobody signed in" stays a sign-in. The sign-in dialog opens on
   `/signin` whatever the session state (`smart-calendar-flow/src/pages/SignIn.tsx`,
   `Index.tsx:555`). It sends the browser to the same `/api/auth/microsoft` route
   as the connect actions (`src/components/auth/AuthDialog.tsx:108`;
   `src/api/client.ts:947-948`). Example: on a shared machine, X signs in with
   Microsoft while Y's session is still live. An implementation that tells
   connect from sign-in by "is a session present" satisfies every AC, and it
   attaches X's Microsoft calendar to Y's account and switches Y's provider to
   Outlook. Y can then read X's calendar. That widens who can see data, which the
   factory rules forbid. AC-3f covers only a flow that "user A started" as a
   connection.
   *Resolves when* §3.3 states that only a connect action started by the
   signed-in user attaches a credential, and states what a sign-in started from
   the sign-in page does when a session is live (today it signs in as the
   Microsoft account's user and replaces the session). An AC must check that in
   this case no Microsoft credential is stored on the live-session user and that
   user's provider is unchanged.

4. **Onboarding is a fourth connect action, and the spec says there are three.**
   §2.3 ends "there are three connect actions and three status lines in the
   product today". `Onboarding.tsx:78-87` is another one:
   `api.authConnectUrl(provider)` sends an Outlook choice to
   `/api/auth/microsoft`, on a signed-in route, on the user's first session.
   AC-3d lists three surfaces, and AC-3e connects "from Settings". An
   implementation can make Settings attach (for example, by marking its request
   as a connect) and leave onboarding as a sign-in. Every AC still passes, and a
   new user whose Microsoft email differs from their sign-in email is moved into
   another Paceday account. That is round-1 Blocking 1 again, on the first
   screen a new user sees.
   *Resolves when* §2.3 counts onboarding's connect step. §3.3 and AC-3d must
   include it, and AC-3e (or a sibling AC) must cover an Outlook connect started
   from onboarding.

5. **AC-3e is labelled [e2e], but the spec does not require any of the stand-ins
   it needs.** AC-3e completes a real Microsoft connect: authorize, token
   exchange, then a profile read. Those addresses are fixed in the code: the
   authorize and token URLs at `backend/auth/microsoft_oauth.go:14-17` and the
   profile URL at `backend/api/handlers_microsoft_auth.go:93`. §3.6 and AC-6
   require redirection only for "Microsoft calendar traffic". OQ-8 hands
   PAC-50's Graph-base seam to whichever feature reaches the contract stage
   first, but that seam does not cover the login token endpoint. As written, the
   contract stage can meet §3.6 in full and still leave AC-3e with no way to
   run, so the e2e gate cannot pass.
   *Resolves when* §3.6 and AC-6 also cover the Microsoft sign-in endpoints
   (authorize, token, profile) under the same test-mode declaration and the same
   refuse-at-startup rule. The other option is to relabel AC-3e at a level that
   can run without them and say which part of the journey is then left untested.

### Non-blocking

1. **AC-3f mostly passes today.** A completion in another browser already fails
   at the `ms_oauth_state` cookie check (`handlers_microsoft_auth.go:41-45`):
   400, nothing stored. Today's behaviour breaks AC-3f only when the flow starts
   and finishes in the same browser and the session changes or ends in between.
   Name that setup so the test fails today. Also add to the no-session case that
   no Paceday user is created and no session is issued, which §3.3 requires and
   the AC does not check.
2. **AC-5f mostly passes today.** `syncOne` already returns before deleting
   anything when the read fails (`personal_blocker.go:87-90`). Today a blocker
   is wrongly removed only when a non-2xx answer still parses as iCalendar,
   because the status is not checked (`webcal_client.go:64-70`). Write that case
   into AC-5f so it is a test that fails today.
3. **An Outlook "connected" state will lapse when the access token expires.**
   Nothing refreshes the token. `LoadMicrosoftToken` returns the stored access
   token, and the existing Outlook reader wraps it in `StaticTokenSource`
   (`microsoft_oauth.go:74-89`; `freebusy_service.go:125`). §5 defers refresh to
   PAC-67 and calls the outcome "honest if not convenient". In practice, every
   Outlook user is shown not connected about an access-token lifetime after each
   connect. Say this in D-1 or §5 so the owner weighs it, or name PAC-67 as a
   prerequisite for releasing the Outlook half.
4. **The MCP server is a consumer, and the spec never mentions it.** Its tool
   descriptions say "List Google Calendar events" and "Google Calendar event ID"
   (`mcp/tools.go:22,85,119`). For an Outlook or WebCal user those descriptions
   become false, and §3.9 is written as if the web app were the only surface.
   Update and delete through MCP (`:113,122`) will receive the new read-only
   refusal. §3.9 or §6 should say what the MCP surface owes.
5. **Wrong line number.** §2.5 cites `UpsertUser` at
   `handlers_microsoft_auth.go:67`. It is at `:68`.
6. **Existing tests that AC-5b will break.** Two test fixtures serve the feed
   from a loopback `httptest` server without the test-mode declaration
   (`backend/api/pac21_calendar_not_connected_test.go:58-66`;
   `backend/engine/pac21_calendar_credential_test.go:122,134-141`). Under AC-5b's
   production rule, those fetches are refused. The plan has to adapt the fixtures
   and must not skip them. Naming them here stops that from being found at
   stage 4.

### Criteria I could not falsify

- AC-3f, as written: the cross-browser reading passes today (Non-blocking 1).
- AC-5f, as written: every failure except a parsable non-2xx answer passes today
  (Non-blocking 2).
- AC-7 is a gate and is labelled as one. It needs no test.
- AC-8d and the §6 items are regression guards. They are meant to pass both
  before and after, and that is correct.
- Every other criterion has a test that fails today and passes afterwards. For
  example:
  - AC-1d/1e fail because `handlers_events.go:24-33,55-58` answers "event not
    found" or 401.
  - AC-3e fails because the callback upserts by Microsoft email and issues that
    user's session (`handlers_microsoft_auth.go:68-79`).
  - AC-5b fails because the fetch uses `http.DefaultClient` (`webcal_client.go:64`).
  - AC-8 fails because `newCalOps` loads the Google credential whatever the
    provider (`cal_iface.go:84-95`).
  - AC-8c fails because `ConfirmBooking` creates the host's event in Google
    (`booking.go:252-271`).

### Citations I checked and found wrong

- "the free/busy service behind scheduling features" (§2.4, §3.4, AC-4). In the
  code, `FreeBusyService` is called only by `POST /api/freebusy`
  (`handlers_freebusy.go:70`), which only the scheduling-link drawer uses.
  Meeting suggestions and booking slots read Google without it
  (`smart_schedule.go:65-71`, `booking.go:122-135`).
- "The standalone free/busy operation no screen uses" (§3.4, §5). That is true of
  the web app, but `mcp/tools.go:137` calls it.
- "there are three connect actions" (§2.3). There are four:
  `Onboarding.tsx:78-87` is the fourth.
- "the address never appears in an error shown to the user or written to the
  server log" (§3.5). That rule leaves out the error tracker, which receives the
  address today (`server_errors.go:41-49`; `handlers_personal.go:170,192`).
- `handlers_microsoft_auth.go:67` for `UpsertUser`. It is at `:68`.
- All other new Revision 1 citations I opened match the code:
  - backend: `handlers_microsoft_auth.go:17-81,62,74,79`; `users.go:21-31`;
    `routes.go:35-39,107-113,116-129`; `microsoft_oauth.go:52-69`;
    `personal_reader.go:13-46,15-17`; `personal_blocker.go:61,72,87,92,111,124`;
    `main.go:95-108`; `handlers_events.go:24-33,55-58`;
    `handlers_conferencing.go:309,405`; `focus_time.go:186`;
    `focus_time_cleaner.go:34`; `smart_schedule.go:326`; `habits.go:242`;
    `compression.go:187`; `auto_decline.go:87,105`; `booking.go:202-209,252-255,269`;
    `cal_iface.go:84-95`; `handlers_auth.go:69-73,98-111`; `config/endpoints.go:49-60`.
  - web: `ConnectionStatus.tsx:12-14,19,59`; `Dashboard.tsx:113-114,269,435-462,579`;
    `EventEditForm.tsx:95,122`; `RescheduleSuggestions.tsx:238`;
    `client.ts:1409,1423`; `Onboarding.tsx:61-62,84-85`;
    `WorkCalendarConnection.tsx:16,108-122`.
  - tests: `e2e/tests/seam.spec.ts:384-413`.

### Consumers this breaks

- mcp/tools.go:22,29,85,113,119,122,137,243: tool descriptions name Google
  Calendar. `list_events` and `calendar_status` change meaning,
  `update_event` and `delete_event` receive the new read-only refusal, and
  `find_free_slots` still reads Google (Blocking 1, Non-blocking 4).
- smart-calendar-flow/src/components/auth/AuthDialog.tsx:108: shares the
  Microsoft start route with the connect actions, so the D-7 change affects
  sign-in unless the spec separates them (Blocking 3).
- smart-calendar-flow/src/pages/Onboarding.tsx:80: an Outlook connect that the
  ACs do not name (Blocking 4).
- smart-calendar-flow/src/components/ScheduleSuggestModal.tsx:97 and
  smart-calendar-flow/src/pages/PublicBooking.tsx: their results stay computed
  from Google for Outlook and WebCal users, or become "unavailable", depending
  on how Blocking 1 is resolved.
- backend/api/pac21_calendar_not_connected_test.go:58-66 and
  backend/engine/pac21_calendar_credential_test.go:122,134-141: their loopback
  feeds are refused under AC-5b (Non-blocking 6).

## Revision 2 — 2026-10-26

Answers to the round-2 challenge above. The spec body was edited in place, and
this section says what changed and where. Every new citation was opened on `main`
at `7de7044` (api) and `4ded854` (web), which are still the tips of both `main`
branches. Nothing was compiled or run, because this is a text stage.

### Blocking

1. **Three scheduling read paths still answered from Google.** Accepted; each
   cited line matches the code.
   - §2.4 is rewritten as a table of the four read paths. It corrects Revision 1's
     claim about `FreeBusyService`: its only caller is `POST /api/freebusy`,
     which the scheduling-link editor uses.
   - §3.4 states one outcome for each path.
     - **(a) Free/busy query** (scheduling-link editor): answered from the chosen
       calendar. AC-4 is relabelled to that path.
     - **(b) Meeting suggestions** (dashboard dialog and both MCP tools):
       unavailable under D-9 (D-11). New AC-4b checks the answer and that no
       request reaches the Google stand-in, with and without a Google credential.
       New AC-4c checks the dialog's message.
     - **(c) Public booking slots**: answered from each host's chosen calendar,
       and failing closed when it cannot be read (D-13). New AC-4d checks that the
       Outlook (and WebCal) meeting blocks a slot, that the Google-only event does
       not, and that no request reaches the Google stand-in. New AC-4e checks that
       an unreadable host is never offered as free; it fails today.
     - **(d) Calendar free/busy** (MCP `clockwise_find_free_slots`): unavailable
       (D-12). New AC-4f checks it, and §6 keeps Google users unchanged.
   - §5 drops the "no screen uses it" exclusion and lists what stays unanswered
     (suggestions and agent free/busy from Outlook or a feed), with the affected
     population. It adds Google hosts' fail-open booking slots as OQ-10.
   - Inherited scope now lists the open findings on the operations these paths
     change: API-029, API-028, API-046, API-077 and API-091/U-03. API-011,
     API-012 and API-064 on the Microsoft routes, which Revision 1 already changed
     without listing them, are added too.

2. **Feed addresses reach Sentry.** Accepted. §2.5 records the error-tracker path
   (`handlers_personal.go:168-171,191-192` → `server_errors.go:41-49` →
   `CaptureException`). §3.5 now says the address leaves the process in no error
   report of any kind: response, server log or error tracker. AC-5 now observes
   the error-tracker report at the point where the server hands it the error, and
   it still applies to both callers (work calendar and personal calendars).

3. **Microsoft sign-in with a live session.** Accepted. §2.3 now records that the
   sign-in dialog opens on `/signin` whatever the session state and uses the same
   route as the connect actions. §3.3 now says:
   - only a connect action started by the signed-in user attaches a credential,
     and only while that user is still the one signed in;
   - a sign-in from the sign-in page is always a sign-in, even with a live
     session, and replaces the session as today (D-14);
   - the presence of a session at completion is not evidence of a connect.

   New AC-3h checks that, in that case, no Microsoft credential lands on the
   live-session user and that user's work calendar is unchanged. §6 adds the same
   behaviour as a must-not-change.

4. **Onboarding is a fourth connect action.** Accepted. §2.3 now counts four
   connect actions, citing `Onboarding.tsx:78-87` and `client.ts:947-948`. §3.3 and
   AC-3d name onboarding's connect step. AC-3e now runs once from each of the four
   connect actions, onboarding included, with A a user who has not finished
   onboarding.

5. **AC-3e lacked the Microsoft sign-in stand-ins.** Answered by covering the
   endpoints rather than relabelling (D-15). §2.5 records the fixed authorize,
   token and profile addresses. §3.6 and AC-6 now cover every Microsoft sign-in
   step under the same test-mode declaration and startup refusal. New AC-6b checks
   that the AC-3e journey completes against stand-ins with no request to a
   Microsoft address. OQ-8 now says the Graph seam covers the profile read but
   not the `login.microsoftonline.com` endpoints, and that PAC-47's contract
   stage owns those.

### Non-blocking

1. AC-3f is rewritten for the case that fails today: the flow starts and finishes
   in the same browser, and the session changes or ends in between. It now also
   requires that no user is created and no session is issued. §2.5 records that a
   completion in another browser already fails at the state cookie.
2. AC-5f now names the non-2xx answer that still parses as iCalendar, which is the
   case that fails today.
3. Token expiry: §5 and D-1 now say every Outlook user is shown not connected
   about an hour after each connect, until PAC-67. The factory recommends PAC-67
   as a prerequisite for releasing the Outlook half.
4. MCP surface: §3.9 now says what the agent tools owe (no "Google Calendar"
   wording for the chosen calendar, and the read-only refusal passed on as one).
   §6 protects Google users' tool answers with `mcp/tools_test.go`. The MCP read
   paths are handled in Blocking 1 above.
5. `UpsertUser` is cited at `handlers_microsoft_auth.go:68`.
6. §2.6 and §6 name the two loopback fixtures and require that they be adapted,
   never skipped or deleted.

## Challenge — 2026-10-26 (round 3)

Reviewed by `spec-challenger` against the committed spec at `1948568` (Revision 2)
on `factory/pac-47-spec`. The code was read on `main` at `7de7044` (api) and
`4ded854` (web), which are still the tips of both `main` branches. I also read the
Linear issue text. I read no author reasoning beyond this file. I opened every
citation that Revision 2 added. I compiled and ran nothing, because this is a
text-only stage.

Revision 2 answers all five round-2 blocking findings. Every new citation I
opened matches the code (list below). The two blocking findings are both in the
booking-slot behaviour that Revision 2 introduced (§3.4 bullet 3, D-13, AC-4d/4e).

**Verdict**: accept-with-changes

### Blocking

1. **D-13 understates its cost. Without token refresh, failing closed makes the
   booking links of every Microsoft-signed-in user go empty about an hour after
   each sign-in. Links they co-host go empty too.** D-13 gives the cost as "such a
   host offers no slots until their calendar is readable again". That reads as a
   transient outage. The code makes it close to permanent:
   - Every Microsoft sign-in, not only a Settings connect, makes the user an
     Outlook user. The callback calls `SaveMicrosoftToken`
     (`backend/api/handlers_microsoft_auth.go:74`), which writes
     `calendar_provider = 'outlook'` (`backend/auth/microsoft_oauth.go:68`).
   - Nothing refreshes the stored access token. `LoadMicrosoftToken` returns it
     as stored (`microsoft_oauth.go:74-89`), and §5 records that it lapses after
     about an hour.
   - Slots are collective: a slot is offered only when **all** accepted hosts are
     free (`backend/engine/booking.go:74-99`). "No slot that needs that host is
     offered" therefore means the whole link offers nothing, including a link
     owned by a Google host who added a Microsoft-signed-in co-host.

   Guests open booking pages at times the host does not control. So for these
   hosts the page is empty almost all the time. Today it offers every slot,
   because such a host has no Google credential and `hostBusy` drops the error
   (`booking.go:123`). That is wrong, but the page is usable. This regression
   reaches users who never touched the provider setting, through the guest-facing
   `smart-calendar-flow/src/pages/PublicBooking.tsx`. D-1 and §5 record the
   token-expiry cost only for the status line and the week view. The PAC-67
   recommendation in §5 is a recommendation, not a requirement.
   *Resolves when* either of these is true:
   - The spec makes token refresh (PAC-67) a stated prerequisite for releasing
     the Outlook booking-slot behaviour of §3.4.
   - D-13 records this cost so the owner can weigh it. That means naming the
     affected population (every user who has signed in with Microsoft, and every
     collective link with such a co-host) and saying that "readable again" means
     a manual reconnect or sign-in roughly every hour until PAC-67 lands.

2. **§3.4 makes an unauthenticated route drive an unbounded number of outbound
   reads of a user-chosen address, and nothing in the spec bounds it.**
   `GET /api/book/{slug}/slots` needs no session (`backend/api/routes.go:26`).
   Without a `date`, it calls `CollectiveSlots` for each of the next 60 days
   (`backend/api/handlers_booking.go:171-183`). Each call reads every host's
   calendar again (`booking.go:91-97`). D-13 makes that read a WebCal feed fetch
   or a Microsoft Graph call. The feed reader's cache does nothing when a new
   instance is built per request (§2.5).
   - One anonymous request can then cause about 60 × (hosts) outbound fetches of
     an address that the link's host typed in. §3.5 limits *where* the server may
     fetch. It does not limit *how often* anonymous traffic can make it fetch.
     Anyone can sign up, store a public victim's address as a feed, publish a
     link, and get a 60-fold reflector.
   - For Outlook hosts the same volume goes to Graph. If Graph throttles it,
     D-13's fail-closed rule turns the throttle into an empty booking page. An
     anonymous party can therefore blank a host's booking page.
   - Today the route reads only Google this way, and the read fails open.
   - OQ-6 (feed size and time limits) is scoped to "hanging the week view" and
     does not mention the booking path.

   *Resolves when* the spec states a requirement that bounds the calendar reads a
   public slots request may cause for each host, both within one request and
   across repeated requests in a stated freshness window. That requirement needs
   an AC that fails today. For example: given a WebCal host, when one dateless
   slots request is made, then the feed is fetched at most a stated number of
   times. OQ-6, or a new open question, must name the booking path as a consumer
   of the limits.

### Non-blocking

1. **What a guest sees when a host's calendar cannot be read is not specified.**
   The dated form of the slots request answers a `CollectiveSlots` error with
   500 `internal error` (`handlers_booking.go:195-198`). The dateless form skips
   the day silently (`:176-178`). An implementation that fails closed by
   returning an error satisfies AC-4e. On the dated form, though, the guest would
   see "internal error" (`PublicBooking.tsx:139-142`). AC-4d/4e should name both
   forms of the request and say what answer the guest gets.
2. **A cached Google answer can survive a switch to WebCal or Outlook.** The
   free/busy query caches each answer for 15 minutes. The cache key is
   (requester, email, date), and the provider is not part of it
   (`backend/engine/freebusy_service.go:96-97,140-150`). A user who switches from
   Google keeps getting Google-derived busy time for up to 15 minutes. No new
   request reaches Google, so AC-4 still passes. §3.4 ("answers from the chosen
   calendar") does not hold. Say that a provider change invalidates earlier
   answers, or start AC-4 from a warm cache.
3. **AC-4 does not say how the user's own row is treated when their address is
   on a personal-mail domain.** Addresses on personal-mail domains are answered
   "unknown" before any provider is consulted (`freebusy_service.go:92-94`).
   Should a WebCal user whose own address is `@gmail.com` get their busy time
   from the feed? Under §3.4 they should. Under today's code they would get
   "unknown". Name the domain in AC-4, or state the rule.
4. **The rule that only the four connect actions attach has no AC for a connect
   started from outside the product.** `startMicrosoftOAuth` is a plain GET.
   Under `SameSite=Lax`, the session cookie rides along on a top-level navigation
   from another site. Suppose a victim has a Microsoft session and has already
   given consent. A link on another site could then complete a "connect" with no
   interaction. That switches the victim's work calendar to Outlook, which under
   D-9 stops every Paceday write for them. §3.3's text already forbids this. Add
   an AC that a Microsoft flow not started from one of the four connect actions
   changes neither the signed-in user's credentials nor their work calendar.
5. AC-3h says "This passes today", and it is labelled [e2e]. It can only run once
   the D-15 stand-ins exist, so it is a regression guard for the new code, not a
   check of today's behaviour. That is fine, but say so.

### Criteria I could not falsify

- AC-3h passes today by design. It is a regression guard, and it is labelled as
  one.
- AC-7 is a gate. AC-8d and the §6 items are regression guards.
- Every other criterion has a test that fails today and passes afterwards. For
  the criteria Revision 2 added:
  - AC-4b fails because `Suggest` reads through `newCalOps` → Google
    (`smart_schedule.go:56,65-71`).
  - AC-4d fails because `hostBusy` reads Google (`booking.go:123`,
    `:202-209`).
  - AC-4e fails because a failed read leaves the host free (`booking.go:123`,
    `:95-96`).
  - AC-4f fails because of the Google-only client at
    `handlers_calendar.go:203-209`.
  - AC-5 (error tracker) fails because of `reportServerError` at
    `server_errors.go:48`.
  - AC-6 (sign-in steps) fails because the endpoints are fixed at
    `microsoft_oauth.go:14-17` and `handlers_microsoft_auth.go:93`.

### Citations I checked and found wrong

- None. These Revision 2 citations match `main`:
  - backend: `freebusy_service.go:119-138,122-127,128-137`;
    `smart_schedule.go:56,59-71`; `handlers_schedule.go:162,164`;
    `booking.go:122-135,123-125,202-209`; `routes.go:26,35-39,70`;
    `handlers_calendar.go:203-209`; `handlers_freebusy.go:70`;
    `handlers_personal.go:168-171,191-192`; `server_errors.go:16-22,41-49,75-80`;
    `microsoft_oauth.go:14-17,52-69,74-89`; `handlers_microsoft_auth.go:41-45,62,68,74,79,93`.
  - mcp: `mcp/tools.go:22,54,119,129-139,243,265`; `mcp/tools_test.go` exists.
  - audit: `docs/factory/api-audit.md:269,270,286,287,304,322,334,335,349,471,486`.
  - web: `Onboarding.tsx:61-62,78-87`; `client.ts:927,947-948`;
    `AuthDialog.tsx:108`; `Index.tsx:555`; `ScheduleSuggestModal.tsx:97`;
    `LinkEditDrawer.tsx:157`.
- I confirmed by grep that there are exactly four connect actions
  (`Dashboard.tsx:456`, `ConnectionStatus.tsx:13`,
  `WorkCalendarConnection.tsx:81`, `Onboarding.tsx:80`) and one sign-in caller of
  the Microsoft route (`AuthDialog.tsx:108`).
- §2.7's claim also holds: the only readers of the provider setting are the
  status handler, the free/busy service and the unused factory.

### Consumers this breaks

- smart-calendar-flow/src/pages/PublicBooking.tsx:126-142: empty pages for every
  link with an Outlook host once the token lapses (Blocking 1), and possibly
  "internal error" on the dated form (Non-blocking 1).
- backend/api/handlers_booking.go:171-183: the dateless loop multiplies feed and
  Graph reads per anonymous request (Blocking 2).
- smart-calendar-flow/src/components/links/LinkEditDrawer.tsx:157: co-host
  coverage chips can show Google-derived busy time for up to 15 minutes after a
  provider switch (Non-blocking 2).

## Revision 3 — 2026-10-26

Answers to the round-3 challenge above. The spec body was edited in place, and
this section says what changed and where. The citations were re-opened on `main`
at `7de7044` (api), still the tip of `main`: `handlers_microsoft_auth.go:74`,
`microsoft_oauth.go:68,74-89`, `booking.go:74-99,123`, `routes.go:26` and
`handlers_booking.go:171-183,195-198` match what the challenge says. Nothing was
compiled or run, because this is a text stage.

### Blocking

1. **D-13 understated its cost.** Accepted, and resolved both ways the challenge
   offered.
   - **PAC-67 is a release prerequisite** for the Outlook booking-slot behaviour.
     §3.4 says an Outlook host's slots are computed as today until PAC-67 is
     released. §5 turns the recommendation into a requirement for that part.
     AC-4d, AC-4e and AC-4g note that their Outlook cases are released with PAC-67; the
     tests are still written with the rest.
   - **D-13 records the cost and the population**: every user who has ever signed
     in with Microsoft (the callback makes them an Outlook user), and every
     collective link with such a user as an accepted co-host, including links
     owned by Google hosts. It says that without refresh "readable again" means a
     manual reconnect or sign-in about every hour. It records the residual cost
     after PAC-67 (revoked or unrefreshable credential) as accepted.
   - The WebCal half still ships with this spec. A feed needs no token, so only
     hosts who chose WebCal are affected, and only while their feed is
     unreadable.

2. **An unauthenticated route drove unbounded outbound reads.** Accepted.
   - §3.4 now bounds the reads. One guest request, for one day or the whole
     bookable period, reads each Outlook or WebCal host's calendar at most once.
     Across guest requests, a host's calendar is read at most once per five
     minutes, whether the read succeeded or failed. A change of work calendar or
     feed address discards the earlier read (D-16).
   - **AC-4g** fails today. It checks that a whole-period request, a one-day
     request and a second guest's whole-period request within five minutes cause
     exactly one feed fetch, with the feed's event still blocking its slot. It is
     repeated for Outlook against the Microsoft stand-in. Today the feed and the
     stand-in are never contacted for a host without a Google credential.
   - **AC-4h** checks that ten requests against an unreachable feed cause at most
     one connection attempt, still offer no slot for that host, and that an
     address change is read at once.
   - Because a failed read counts against the bound, anonymous traffic can no
     longer drive Graph into throttling a host and so blank their page.
   - **OQ-6** now names the public booking page as a consumer of the feed size
     and time limits. New **OQ-11** records that Google hosts' booking reads stay
     unbounded here, because Google hosts are unchanged (§6).

### Non-blocking

1. What a guest sees for an unreadable host is now specified. §3.4 says the
   guest is shown no free times, never an error. AC-4e names both the one-day
   and the whole-period request, and requires that the one-day answer is not an
   error.
2. A cached Google answer surviving a switch. §3.4 says no free/busy answer
   computed before a work-calendar change is reused after it. AC-4 now runs from
   a warm cache across the switch.
3. Personal-mail domains. §3.4 says the user's own row comes from their chosen
   calendar whatever their address's domain. AC-4 runs with a company address and
   with a `@gmail.com` address. Other attendees on personal-mail domains are
   still "unknown", as today.
4. A Microsoft flow started from outside the product. New **AC-3i** checks that
   such a flow changes neither the signed-in user's credentials nor their work
   calendar, nor anyone else's. §3.3 already forbade it.
5. AC-3h now says it is a regression guard that can run only once the §3.6
   stand-ins exist, not a check of today's behaviour.

## Challenge — 2026-10-26 (round 4)

Reviewed by `spec-challenger` against the committed spec at `ece0f18` (Revision 3)
on `factory/pac-47-spec`. The code was read on `main` at `7de7044` (api) and
`4ded854` (web), still the tips of both `main` branches. I also read Linear
PAC-67, which the spec now makes a release prerequisite. I read no author
reasoning beyond this file. I compiled and ran nothing, because this is a
text-only stage.

Revision 3 answers both round-3 blocking findings in substance. PAC-67 is now a
release prerequisite for the Outlook booking-slot behaviour, D-13 states the
population, and §3.4/D-16 bound the guest-driven reads, with AC-4g/AC-4h failing
today. The citations I re-opened hold (list below). Two findings block. One is
older than Revision 3 but becomes consequential only because of this spec. The
other is an inconsistency that Revision 3 introduced.

**Verdict**: accept-with-changes

### Blocking

1. **Every Microsoft sign-in overwrites the user's work-calendar choice with
   Outlook. The spec keeps that behaviour ("exactly as today", §3.3, §6 and D-14)
   while making the choice decide what the user sees and what Paceday writes.**
   - The callback calls `SaveMicrosoftToken` on every completion
     (`backend/api/handlers_microsoft_auth.go:74`). That runs
     `UPDATE settings SET microsoft_tokens = $2, calendar_provider = 'outlook'`
     unconditionally (`backend/auth/microsoft_oauth.go:68`). It is the only code
     that writes the provider outside the Settings save. Google sign-in does not
     touch it.
   - The Settings picker saves `calendar_provider: "webcal"` together with the
     feed address (`smart-calendar-flow/src/components/WorkCalendarConnection.tsx:77`).
   - Today the overwrite is nearly harmless, because only the status line and the
     free/busy query read the provider (§2.7). After this spec the provider decides
     the week view (§3.1), the status line (§3.2), every write (§3.8, D-9), meeting
     suggestions (§3.4, D-11) and booking slots (§3.4, D-13).
   - Concrete case: a user who signs in with Microsoft picks WebCal in Settings
     and sees their feed. The next time they "Continue with Microsoft"
     (`AuthDialog.tsx:108,209`), their work calendar silently becomes Outlook
     again. Their week view, status line and booking slots switch calendars
     without their asking. This contradicts §3.1, "the calendar the user chose is
     the calendar the week view shows".
   - The same happens to a Google-signed-in user who shares an email with their
     Microsoft account (§2.2) and signs in once with Microsoft from the sign-in
     page. Their work calendar becomes Outlook, and under D-9 every Paceday write
     to their Google calendar stops. §3.3 says that sign-in behaves "exactly as
     today". D-14 protects only the user whose session was live, not the user
     being signed in.
   - PAC-67 item 4 asks only that the overwrite be scoped to the caller. It does
     not ask whether a sign-in should change an existing choice, so no other issue
     owns this.

   *Resolves when* the spec states what a Microsoft sign-in does to the work
   calendar of a Paceday user in each of three cases: a new user, an existing user
   whose choice is the default, and an existing user who chose WebCal or Google.
   Each case needs an AC that tells the outcomes apart. If the factory keeps the
   overwrite, D-1/D-9 must record it as a cost, with the population named, so the
   owner can weigh it.

2. **The interim behaviour for Outlook booking hosts contradicts the rule that
   opens §3.4 and has no criterion. Revision 3 also claims an AC-4g edit that is
   not in the body.**
   - §3.4 opens with: "No availability read made for an Outlook or WebCal user
     queries Google, even when a Google credential is on file." Its booking bullet
     then says that until PAC-67, an Outlook host's slots are "computed as today".
     Today that means a Google read through `calendarClientForUser`
     (`backend/engine/booking.go:122-125,202-209`), so Google is queried for an
     Outlook user.
   - The bullet calls this "the one exception to the first sentence of this item".
     Read literally, that is the bullet's own first sentence, so the rule that
     opens §3.4 still says the opposite. A contract or plan author is left with
     two conflicting instructions.
   - No AC pins what a guest sees for an Outlook host once this spec merges and
     before PAC-67 ships, with or without a Google credential. The release-gated
     cases of AC-4d, AC-4e and AC-4g cannot pass at merge.
   - The Revision 3 log says "AC-4d, AC-4e and AC-4g note that their Outlook cases
     are released with PAC-67". AC-4d and AC-4e do. AC-4g (lines 611-619) does
     not. Its Outlook case requires exactly one Microsoft read, which the interim
     behaviour never makes.

   *Resolves when* the rule that opens §3.4 names the interim exception, an AC
   states what an Outlook host's guest sees in the interim (with and without a
   Google credential), and AC-4g's Outlook case carries the same PAC-67 release
   note as AC-4d and AC-4e.

### Non-blocking

1. **D-16's read bound does not cover Outlook hosts in the interim.** Before
   PAC-67, an Outlook host is read "as today": one Google read per day of the
   60-day loop (`backend/api/handlers_booking.go:171-183`), with no bound across
   requests. OQ-11 names only Google hosts. Say that OQ-11 also covers Outlook
   hosts until PAC-67 ships, or that D-16 applies to them from the start.
2. **Overlap with PAC-67 on the status line.** PAC-67's draft acceptance also
   requires `auth status` to report `connected: false` for a dead Outlook token,
   and §3.2/AC-3b require the same thing. Name the issue that owns this
   criterion, so that it is neither built twice nor skipped by both.
3. **Release-gated tests and the gate.** "Its test is written with the rest" and
   AC-7 (every gate `rc=0`) together mean that the Outlook cases of AC-4d, AC-4e
   and AC-4g must be admitted in one of two ways: as skipped tests that name
   PAC-67 (README §3 rule 4), or as tests that assert the interim behaviour. Say
   which, so that the plan stage does not have to choose.

### Criteria I could not falsify

- AC-3h is a regression guard by design, and it is labelled as one. AC-7 is a
  gate. AC-8d and the §6 items are regression guards.
- The Outlook cases of AC-4d, AC-4e and AC-4g cannot pass before PAC-67 ships
  (Blocking 2). Until then, no test can tell a correct interim from a broken one.
- Every other criterion, including the WebCal case of AC-4g, AC-4h and AC-3i,
  has a test that fails today and passes afterwards:
  - AC-4g and AC-4h fail because a host with no Google credential is never read
    (`booking.go:123`) and because the dateless loop reads once per day
    (`handlers_booking.go:171-183`).
  - AC-3i fails because the callback ignores the session and credits the
    credential by Microsoft email (`handlers_microsoft_auth.go:68,74,79`).

### Citations I checked and found wrong

- Revision 3 says AC-4g carries the PAC-67 release note. It does not
  (Blocking 2).
- These citations match `main`:
  - `handlers_microsoft_auth.go:68,74,79`
  - `microsoft_oauth.go:52-69,68,74-89`
  - `booking.go:74-99,122-135`
  - `handlers_booking.go:171-183,195-198`
  - `routes.go:26`
  - the 15-minute free/busy TTL that D-16 relies on (`freebusy_service.go:140,150`)

### Consumers this breaks

- smart-calendar-flow/src/components/auth/AuthDialog.tsx:108,209: each Microsoft
  sign-in resets a WebCal or Google choice to Outlook. After this spec, that
  changes the user's week view, writes and booking slots (Blocking 1).
- smart-calendar-flow/src/components/WorkCalendarConnection.tsx:77: a saved WebCal
  choice does not survive the user's next Microsoft sign-in (Blocking 1).
- smart-calendar-flow/src/pages/PublicBooking.tsx: until PAC-67, an Outlook host's
  slots come from Google, and no AC specifies that behaviour (Blocking 2).

## Revision 4 — 2026-10-27

Answers to the round-4 challenge above. The spec body was edited in place, and
this section lists what changed and where. `main` has moved since round 4, so
this revision first merged `origin/main` (`6d751f9`, api) into the spec branch
and re-read every citation the moved files touch. The web app was read at
`ad52160`. Nothing was compiled or run, because this is a text stage.

### Blocking

1. **Every Microsoft sign-in overwrote the work-calendar choice.** Accepted. The
   factory does not keep the overwrite.
   - **New D-17**: a Microsoft sign-in sets the work calendar only for a user it
     creates. Existing users keep their stored choice, whatever it is. §3.3 now
     covers the three cases the challenge named:
     - a new user becomes Outlook, as today (**AC-3j**, a regression guard);
     - an existing user on the default stays Google, because the default cannot
       be told apart from a Google choice (**AC-3k**, fails today);
     - an existing user who chose WebCal or Google keeps it, feed address
       included (**AC-3l**, fails today).
   - Because the overwrite is not kept, D-1 and D-9 record no new cost. D-17
     records its own cost: an existing Google or SSO user who signs in only with
     Microsoft stays on Google until they pick Outlook. D-17 rewrites no stored
     value. Users switched by earlier sign-ins stay Outlook, and new **OQ-12**
     asks the owner whether to tell them. D-13's population is therefore
     unchanged at release, which D-13 now says.
   - D-14 and §6 now point to D-17 for the sign-in's effect on the work calendar.
     §6 adds the three test fixtures that use the credential store to make a user
     Outlook (`pac24_scope_test.go:132,156`, `pac35_auth_flows_test.go:645`).
     They are adapted, never skipped.

2. **The interim for Outlook booking hosts contradicted §3.4 and had no AC.**
   Accepted.
   - The opening rule of §3.4 now names the exception and says that it ends when
     PAC-67 is released.
   - The booking item spells out the interim, with and without a Google
     credential. The host's Microsoft calendar is not read, and no guest sees an
     error.
   - **New AC-4i** pins that interim for both cases. It passes today by design.
     It is a regression guard, retired by the change that releases the Outlook
     half of D-13.
   - AC-4g's Outlook case now carries the PAC-67 release note. Revision 3 claimed
     that note but did not add it.

### Non-blocking

1. D-16's bound and Outlook hosts in the interim: §3.4 and **OQ-11** now say that
   their Google reads stay unbounded until PAC-67 is released, as Google hosts'
   reads do.
2. Overlap with PAC-67 on the status line: §5 now says that this spec owns "not
   connected for a dead Outlook credential" (§3.2, AC-3b, AC-3c). PAC-67 owns only
   refresh, revocation and the audit entry.
3. Release-gated tests and the gate: a new "Release-gated tests" paragraph after
   AC-4i. The Outlook cases of AC-4d, AC-4e and AC-4g are admitted as skipped tests
   whose skip comment names PAC-67. AC-4i runs live. The release change un-skips
   the three cases and retires AC-4i in the same pull request.

### Drift since `7de7044`, and the citations re-anchored

- PAC-26 fixed API-028 and API-029. PAC-35 fixed API-011, API-012, API-061 and
  API-064. Inherited scope now says so, and §5, §6 and OQ-4 describe disconnect
  as it is now: 409 for Outlook and WebCal users, nothing deleted, session kept.
- PAC-35 added in-process test seams for the Microsoft authorize, token and
  profile addresses. §2.5 now says that they exist, and that no deployment setting
  redirects them, so §3.6, D-15 and AC-6/AC-6b still stand.
- Re-anchored citations (old → new):
  - api: `handlers_events.go:26-33`/`24-33` → `27-35`, `:55-58` → `:252-266`.
  - api: `handlers_auth.go:69-73` → `64-74`, `:82-133` → `110-161`,
    `:98-105` → `126-133`, `:106-111` → `134-139`, `:112-132` → `140-160`.
  - api: `handlers_schedule.go:162,164` → `168,170`.
  - api: `handlers_microsoft_auth.go:17-81` → `17-84`, `:41-45` → `41-46`,
    `:62` → `64`, `:68` → `69`, `:93` → `90`.
  - api: `microsoft_oauth.go:52-69` → `60-81`. The provider overwrite is now at
    `:79`, not `:68`.
  - web: `Dashboard.tsx:269` → `282`, `:435-442` → `448-455`, `:445-462` → `457-475`,
    `:455-457` → `467-470`, `:579` → `592`.
  - web: `ConnectionStatus.tsx:12-14` → `17-19`, `:19` → `24`, `:59` → `71`.
  - web: `WorkCalendarConnection.tsx:16` → `22`, `:77` → `83`, `:108-122` → `129-136`.
  - web: `AuthDialog.tsx:108` → `102` (plus the "Continue with Microsoft" button at
    `:79`; the challenge's `:209` no longer exists).
  - web: `Index.tsx:555` → `587`.
  - web: `client.ts:927` → `982-984`, `:947-948` → `1004-1005`,
    `:1409,1423` → `1469,1483`.
  - web: `EventEditForm.tsx:95,122` → `134,160`.
- Earlier challenge and revision sections keep the line numbers they were
  written against (`7de7044`/`4ded854`). They are a record, not live citations.
