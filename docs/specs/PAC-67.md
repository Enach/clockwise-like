# Spec — PAC-67: Per-user Outlook and Zoom credentials have no refresh or revocation lifecycle

- **Linear**: https://linear.app/paceday/issue/PAC-67
- **Status**: challenged (round 1 answered in "Revision 1", round 2 in "Revision 2",
  round 3 in "Revision 3", round 4 in "Revision 4")
- **Author**: spec-author
- **Origin**: PR Enach/clockwise-like#204 (PAC-24) merge condition 3,
  `calendarCredentialLifecycle`; PAC-24 spec §0 and OQ-6
  (`docs/specs/PAC-24.md:55-61`, `:785-795`). Related: PAC-48 (origin), PAC-23
  (audit `user_id`), PAC-47 (work-calendar provider setting ignored).
- **Inherited scope**: every operation this feature touches or may touch, with
  the `x-uncertain` markers and open audit findings on it, re-derived in
  Revision 4 against `origin/main` at `6d751f9` (rounds 1-4 used `16f3100`;
  PAC-21, PAC-26 and PAC-35 have since fixed several of the findings listed
  then). "Changed" means this spec's criteria require a contract change there;
  "conditional" means the marker or finding comes into scope **only if** the
  contract stage edits that operation, under factory §7.1; "unchanged" means a
  criterion reaches the operation and pins its response as it is. Line numbers
  are in `contracts/openapi/openapi.yaml` and `docs/factory/api-audit.md` at
  `6d751f9`.
  - **Auth status** (`GET /api/auth/status`, `openapi.yaml:1285`, calendar
    connection status §2.6). No `x-uncertain`; API-009 is Fixed (PAC-24).
    Nothing inherited.
  - **Calendar disconnect** (`DELETE /api/auth/disconnect`, `openapi.yaml:864`).
    **API-061** (`api-audit.md:319`) is Fixed (PAC-35): disconnect keeps the
    session, and for a user whose calendar provider is Outlook or WebCal it
    answers 409 and deletes nothing (PAC-35 D-8, §2.1). No `x-uncertain`.
    Nothing inherited. See OQ-3.
  - **Microsoft sign-in and callback** (`GET /api/auth/microsoft`, `:1051`;
    `GET /api/auth/microsoft/callback`, `:1100`). **API-011** (`api-audit.md:269`),
    **API-012** (`:270`) and **API-064** (`:322`) are all Fixed (PAC-35). No
    `x-uncertain`. Nothing inherited. AC-11 changes what the callback stores;
    see OQ-4.
  - **Zoom connect and callback** (`GET /api/auth/zoom`, `:1344`;
    `GET /api/auth/zoom/callback`, `:1376`). No marker, no finding. Nothing
    inherited.
  - **Zoom disconnect** (`POST /api/conference/zoom/disconnect`, `:1789`). No
    marker, no finding. AC-15 likely changes its response; nothing inherited
    either way.
  - **Conferencing provider list** (`GET /api/conference/providers`, `:1759`,
    §2.6). Changed in behaviour by AC-8, AC-12, AC-13 and AC-21. **API-070**
    (`api-audit.md:328`, Reported; 1-4 entries depending on configuration,
    `custom` always connected): conditional.
  - **Standalone conference create** (`POST /api/conference/create`, `:1713`).
    Reached by AC-3, AC-5 and AC-8. **API-052** (`api-audit.md:310`, no
    validation) and **API-091** (`:349`, no frontend consumer), both Reported:
    conditional.
  - **Conference on an event** (`POST /api/events/{id}/conference`, `:1943`).
    Creates Teams and Zoom meetings from the stored credential
    (`backend/api/handlers_conferencing.go:42-56`, `:375-383`), but only after
    reading the event with the **Google** credential (§2.8), so AC-3, AC-5,
    AC-8, AC-10, AC-12 and AC-23 reach it. Checked: no `x-uncertain` on it and
    no audit finding names it. Nothing inherited.
  - **Activity log** (`GET /api/audit`, `:367`; `AuditEntry` at `:5909`).
    **Changed**: AC-8, AC-15 and AC-16 add entry kinds to a closed set (§2.4,
    AC-22). U-02, API-005 and API-074 are all resolved; nothing further
    inherited.
  - **Availability lookup** (`POST /api/freebusy`, `:2194`). Its response does
    **not** change under this spec (AC-8, D-6). **API-028** is Fixed (PAC-26).
    Open findings **API-046** (`api-audit.md:304`) and **API-077** (`:335`):
    conditional.
  - **Team availability** (`GET /api/teams/{id}/availability`, `:5330`). Renews
    every team member's Google credential on a member's request (§2.9).
    Reached by AC-26 and AC-27; its response does **not** change (D-12).
    Checked: no `x-uncertain`, no audit finding. Nothing inherited.
  - **Public link info** (`GET /api/book/{slug}`, `:1432`, operation
    `getPublicLinkInfo`; its `PublicLinkCoverage` schema at `:8706`).
    Unauthenticated. Uses and renews no credential, but tells every visitor how
    many hosts hold a stored Google credential (§2.9). **Unchanged** in
    response: a host whose loss is recorded still counts as checked (D-14,
    AC-28). The contract stage must make the `PublicLinkCoverage` description
    say what is actually counted (§6). Checked: no `x-uncertain`, and no audit
    finding names this operation (API-051 and API-073 are on the `POST` of the
    same path). Nothing inherited.
  - **Public booking slots** (`GET /api/book/{slug}/slots`, `:1532`).
    Unauthenticated; renews each host's Google credential (§2.9). Reached by
    AC-28; response unchanged (D-12). Checked: no `x-uncertain`, no audit
    finding. Nothing inherited.
  - **Public booking creation** (`POST /api/book/{slug}`, path item at `:1432`).
    Unauthenticated; renews each host's Google credential (§2.9). Reached by
    AC-28; response unchanged (D-12). No `x-uncertain`. Open findings
    **API-051** (`api-audit.md:309`) and **API-073** (`:331`): conditional.
  - **Consumers acting for the credential's owner only** (§2.9: scheduled
    jobs, background work the owner started, and every request-driven path
    that loads the caller's own credential). They become subject to loss
    recording (AC-29, D-13); none of their responses changes, so no
    operation's contract is edited and nothing is inherited from them.
  - **Google-backed operations** whose failure response would change if the
    contract stage chose to change it, notably **`GET /api/calendar/freebusy`**
    (`:1651`). Its U-03 marker was resolved as "keep" by PAC-21 and is gone
    from the bundle; **API-091** (`api-audit.md:349`) remains open on it. This
    spec requires no change to any Google operation's failure response (D-7),
    so API-091 is conditional.
  - **`ConferenceMeetingDetails`** schema (`:6412`) carries **U-12** (`:6433`).
    Not changed here; conditional.
  - **T8** (provider error text echoed to the client). Rounds 1-4 cited raw
    echoes in the Zoom and Microsoft callbacks; PAC-21 removed them (the Zoom
    exchange failure now goes through the generic server-error writer,
    `backend/api/handlers_conferencing.go:131-134`, and provider-construction
    refusals are fixed sentences, `backend/conference/factory.go:11-17`,
    `backend/api/handlers_conferencing.go:62-74`). Nothing inherited (§5).

## 0. Scope and split

The issue names four items (refresh, disconnect/revocation, failure surfacing,
the provider flip) for Outlook and Zoom, plus a check of the same properties for
Google. This spec covers all four for the credentials stored on the user's own
settings record (Outlook, Zoom) and for the Google sign-in credential.

Split out, not covered here, proposed as a follow-up issue (OQ-8): credentials
stored on **personal calendars** (secondary Google/Outlook calendars a user
adds), which are also read with a static token and never refreshed
(`backend/calendar/personal_reader.go:18-30`, consumer
`backend/engine/personal_blocker.go:72,87`). Same defect class, different
storage and consumers; bundling it would roughly double the surface.

## 1. Problem

Since PAC-24 every user has their own Outlook and Zoom credential. Nothing looks
after that credential once it is stored:

- **Expiry breaks features silently.** Microsoft and Zoom access tokens are
  short-lived (about an hour). Paceday stores a refresh token for both and never
  uses it. An hour after connecting, a user's Outlook availability lookups and
  their Teams or Zoom meeting creation start failing, while the product keeps
  telling them they are connected. The only recovery is to sign in or connect
  again, which nothing tells them to do.
- **Disconnect does not remove what it should.** There is no way to disconnect
  Outlook. Disconnecting Zoom clears Paceday's copy but leaves the grant live at
  Zoom, so Paceday keeps authority over a Zoom account whose owner believes they
  withdrew it.
- **Dead credentials are invisible.** A revoked or expired credential produces at
  most a generic error on the action that hit it, or nothing at all. The user's
  connection status and activity log say nothing.

Who it costs: every Outlook-signed-in user and every user who connected Zoom.
Google users' tokens refresh, but a dead Google credential is equally invisible
(§2.3).

## 2. Current behaviour

Rounds 1-4 cited the backend at `16f3100`. Revision 4 re-read the paths whose
behaviour changed on `origin/main` since then (PAC-21, PAC-35 and PAC-49 among
others) at `6d751f9` and corrected §2.1, §2.2, §2.3, §2.5, §2.6, §2.8 and §2.9
to match; the corrected citations are at `6d751f9`. Citations elsewhere in §2
whose behaviour did not change were not all re-numbered and may be off by a
few lines at `6d751f9`.

### 2.1 Outlook (Microsoft) credential

- **Storage.** One JSON record per user on their settings row holding access
  token, refresh token and expiry (`backend/auth/microsoft_oauth.go:43-47`,
  `:52-70`). Written only by the Microsoft sign-in callback
  (`backend/api/handlers_microsoft_auth.go:72`), read by `LoadMicrosoftToken`
  (`backend/auth/microsoft_oauth.go:74-89`). The requested scopes include
  `offline_access`, so a refresh token is issued
  (`backend/auth/microsoft_oauth.go:30`).
- **Per-user isolation of the write is already correct.** The write is scoped
  `WHERE user_id = $1` (`backend/auth/microsoft_oauth.go:68`), and
  `TestAuthStatus_DescribesOnlyTheCaller`
  (`backend/api/pac24_scope_test.go:128-164`) proves Bob connecting does not
  replace Alice's credential.
- **No refresh anywhere.** Every consumer wraps the stored token in a static
  token source, which never refreshes:
  - availability lookups: `backend/engine/freebusy_service.go:123-127`
    (`oauth2.StaticTokenSource(token)`), with the error discarded
    (`fetched, _ =`, line 126);
  - Teams meeting creation: `backend/api/handlers_conferencing.go:40-44` and
    `:139-144` pass only the access token;
  - the generic provider factory builds an Outlook client with an **empty**
    access token (`backend/calendar/client_factory.go:20-21`, placeholder at
    `:78-79`). That path belongs to PAC-47 and is not repaired here.
- **Status reports connected after expiry.** Auth status reports Outlook
  connected whenever a refresh token is stored, whether or not it works
  (`backend/api/handlers_auth.go:98-105`). Conferencing providers reports Teams
  connected whenever any Microsoft credential exists
  (`backend/api/handlers_conferencing.go:169,178-179`).
- **No disconnect.** No route clears the Outlook credential. Calendar
  disconnect keeps the session since PAC-35 (API-061 Fixed); for a user whose
  calendar provider is Outlook or WebCal it answers 409 ("disconnecting this
  kind of calendar is not available yet") and deletes nothing, the Google
  credential included (PAC-35 D-8); for anyone else it deletes only the Google
  token row (`backend/api/handlers_auth.go:171-197`, route
  `backend/api/routes.go:48`).
- **Provider flip (issue item 4).** Each Microsoft sign-in sets the *caller's*
  calendar provider to Outlook and nobody else's
  (`backend/auth/microsoft_oauth.go:68`, `WHERE user_id = $1`). The PAC-48 half
  is therefore resolved; no test asserts another user's provider is untouched
  (AC-18 adds one). A side effect remains: because Microsoft is a sign-in flow,
  **every** Microsoft sign-in resets the caller's provider to Outlook,
  overwriting a different choice (for example WebCal) made in settings. See OQ-5.
- **Reconnect can replace a usable refresh token with nothing.** The callback
  stores whatever the exchange returned (`backend/auth/microsoft_oauth.go:56-60`);
  a response without a refresh token would store an empty one, which status then
  reports as disconnected (`handlers_auth.go:100`). Whether Microsoft ever omits
  it with `offline_access` requested is not established (OQ-6).
- **A Microsoft sign-in attaches the credential to the user matched by email,
  not to whoever is signed in.** The callback reads the Microsoft account's
  email, upserts the user by email (`backend/api/handlers_microsoft_auth.go:62-71`;
  `ON CONFLICT (email)`, `backend/storage/users.go:21-25`), stores the credential
  on that user (`:72`) and issues a session for them (`:77`). So "A reconnects
  Outlook" reaches A only while A's Microsoft email equals A's Paceday email.
  See OQ-10.

### 2.2 Zoom credential

- **Storage.** JSON `{access_token, refresh_token}` per user, **no expiry**
  (`backend/storage/personal_calendars.go:157-168`). The exchange response's
  `expires_in` is parsed (`backend/conference/zoom_oauth.go:19`) and dropped
  (`backend/api/handlers_conferencing.go:112`). Connecting also sets the caller's
  conferencing provider to Zoom (`personal_calendars.go:168`).
- **Callback is caller-scoped.** It sits behind `requireAuth`
  (`backend/api/routes.go:39`) and saves against the caller
  (`handlers_conferencing.go:112`).
- **No refresh.** The provider is built from the access token alone
  (`backend/conference/factory.go:17-21`) and sent as a bearer header
  (`backend/conference/zoom.go:36`). No code calls Zoom's token endpoint with a
  refresh grant; the only call is the code exchange
  (`backend/conference/zoom_oauth.go:32-60`).
- **Disconnect clears locally, does not revoke.** `ClearZoomTokens` sets the
  caller's tokens to `''` and conferencing back to `meet`
  (`backend/storage/conferencing.go:11-14`; handler
  `handlers_conferencing.go:217-223`, answering 204; route `routes.go:127`). Nothing is sent to
  Zoom. Isolation is proven by `TestZoomTokens_ArePerUser`
  (`backend/storage/settings_test.go:154-180`).
- **Status.** Zoom is reported connected whenever the stored string is non-empty
  (`handlers_conferencing.go:207`).

### 2.3 Google credential (the issue's "confirm" item)

- **Per-user.** One row per user in the OAuth token table
  (`backend/auth/token_store.go:16-30`); load and delete refuse a nil user
  (`:40-59`). Disconnect deletes only the caller's row, and only when the
  caller's calendar provider is Google (`handlers_auth.go:176-197`, PAC-35
  D-8). The disconnect test
  (`backend/api/handlers_auth_test.go:42-50`) asserts only the status code, so
  that isolation is **not** test-protected today — a finding.
- **Refresh works but is not persisted.** Consumers use the OAuth config's
  refreshing token source (`backend/auth/google_oauth.go:32-33`; for example
  `backend/api/handlers_calendar.go:143`, `backend/engine/freebusy_service.go:132`),
  but the refreshed token is never written back: the only writer is the sign-in
  callback (`backend/api/handlers_auth.go:70`). After expiry every request
  refreshes again. Google normally does not rotate refresh tokens, so this works,
  at the cost of an extra round trip per request.
- **Failure is invisible.** Status reports Google connected whenever a row exists
  (`handlers_auth.go:140-160`), whether or not the refresh token still works.
- **No revocation.** Disconnect deletes the row and sends nothing to Google.
- **Not only read for its owner's actions.** Team availability and the public
  booking routes read and renew *other* users' Google credentials, and public
  link info reports on hosts' credentials to anonymous visitors (§2.9).

### 2.4 Failure surfacing today

- The audit log has a per-user writer, `WriteAuditLog`
  (`backend/storage/focus_blocks.go:80-90`, PAC-23), used today by scheduling
  features only. No credential event is ever recorded.
- The activity log's entry kind is a **closed** set of seven values in the
  contract (`contracts/openapi/paths/calendar.yaml:1669-1672`, enum at
  `:1716-1723`): "adding an eighth action is a contract change", and PAC-23
  AC-14 requires the set never to shrink (`docs/specs/PAC-23.md:433`). Web
  consumers: the generated zod schema validates against exactly these seven
  (`smart-calendar-flow/src/api/generated/schemas.ts:38`, not yet applied to
  the audit path); the hand-written normaliser passes unknown strings through
  (`smart-calendar-flow/src/api/client.ts:478`); the audit page renders the
  value raw (`smart-calendar-flow/src/pages/Audit.tsx:97`).
- Outlook availability failures are swallowed (`freebusy_service.go:126`).
  Teams and Zoom failures surface as a generic error on the action
  (`handlers_conferencing.go:152-155`, `:342-345`).

### 2.5 Test seams

The e2e stack has a stand-in for Google only (`e2e/fake-google/`), reached by
configuration rather than by host name. Every real provider host, Google's
included, is routed to a refusing sink (`e2e/fake.ts:27-35`,
`e2e/SEAM-REQUIRED.md`), so there is no Microsoft or Zoom stand-in. Criteria needing a Microsoft or Zoom exchange are
therefore marked `[unit]` or `[contract]` below, not `[e2e]`. At the unit level
the Microsoft OAuth configuration already accepts an endpoint override for a
local fake provider (`backend/auth/microsoft_oauth.go:23-26`, PAC-35); Zoom's
exchange has no such seam today.

### 2.6 Status surfaces today

Two surfaces tell a user whether a provider is connected. These are what §3-§4
mean by "status surfaces".

- **Calendar connection status** (`GET /api/auth/status`,
  `backend/api/handlers_auth.go:110-161`). Reports **only** the user's current
  calendar provider: Outlook (connected when a refresh token is stored,
  `:126-133`), WebCal (`:134-139`) or Google (connected when a token row exists,
  `:140-160`). It never mentions Zoom, and it says nothing about a Microsoft
  credential when the calendar provider is not Outlook, nor about a Google
  credential when it is not Google.
- **Conferencing provider list** (`GET /api/conference/providers`,
  `backend/api/handlers_conferencing.go:192-214`). Rows, each present only when
  the provider is configured on the server: `google_meet` connected whenever a
  Google token row exists (`:204`); `zoom` connected whenever the stored Zoom
  string is non-empty (`:207`); `teams` connected **and** enabled whenever a
  Microsoft credential exists (`:210`); `custom` always connected (`:212`).

Both derive "connected" from the credential being stored. Because D-1 keeps a
refused credential stored, both would keep reporting connected after a loss
unless they learn about it.

### 2.7 Availability lookups degrade instead of failing

The availability lookup (`FreeBusyService.Query`,
`backend/engine/freebusy_service.go:74-155`) uses the **calendar provider's**
credential only: Outlook's when the provider is Outlook (`:122-128`), Google's
otherwise (`:129-138`). It queries the *attendees'* free/busy with that
credential. Any fetch error is discarded (`:126`, `:136`); every attendee that
was to be queried comes back with coverage `unknown` and no busy periods, and
the call still succeeds. That result is cached per requesting user, attendee
and day for 15 minutes (`:96-100`, `:140-152`), so a later lookup inside that
window is served the `unknown` result without trying the credential.

Two consumers rely on the call succeeding: `POST /api/freebusy`
(`backend/api/handlers_freebusy.go:72-76`, a 200 with per-attendee coverage) and
manager team stats (`backend/engine/manager.go:484-499`, which uses a result
only when there is no error and coverage is `known`).

### 2.8 Meeting creation on an existing event reads the Google credential first

There are two meeting-creation actions, and they do not read the same
credentials.

- **Standalone create** (`createConference`,
  `backend/api/handlers_conferencing.go:146-190`) reads only the user's settings
  and the Teams (Microsoft) or Zoom credential.
- **On an existing event** (`addEventConference`,
  `backend/api/handlers_conferencing.go:293-401`) first builds a calendar client
  from the user's **Google** credential (`:309-313`; `loadEventClient` is
  `eventHandlers.calClient`, `:78-81`, which loads the credential through
  `engine.LoadCalendarToken`, `backend/api/handlers_events.go:27-34`), whatever
  the user's calendar provider is. Without a Google credential it answers the
  401 calendar-not-connected refusal (`writeCalendarFailure`,
  `backend/api/server_errors.go:75-81`, PAC-21). It then reads the event with
  that client; any failure there, including a failed Google refresh, answers
  404 "event not found" (`:314-318`; PAC-21 removed the error text that used to
  follow). Only after that does it read the Teams or Zoom credential
  (`:375-383`), and after the meeting is created it writes the link back to the
  event with the Google client again (`:395-398`).

So on the on-event path the Google credential is a precondition for ever
reaching Teams or Zoom, and a dead Google credential stops the request before
the Teams or Zoom credential is read. Listing events behaves the same way: it
always uses the Google credential, whatever the calendar provider
(`backend/api/handlers_calendar.go:137-145`, used by listing at `:146`).

### 2.9 Credentials used on someone else's behalf

Every Google consumer builds a refreshing token source over the stored
credential, so once AC-6 persists renewals, each of these reads becomes a write
to the credential's owner's row. Three paths use a credential whose owner is
not the one making the request, and a fourth tells the public about it:

- **Team availability** (`GET /api/teams/{id}/availability`, route
  `backend/api/routes.go:236`). A team member's request loops over every member
  and loads and renews *that member's* Google credential
  (`backend/engine/team_availability.go:78-100`, `auth.LoadUserToken(e.DB,
  m.UserID)` at `:82`). Any failure for a member is swallowed: that member
  simply contributes no busy periods (`:82-100`).
- **Public booking slots** (`GET /api/book/{slug}/slots`) and **booking
  creation** (`POST /api/book/{slug}`), both outside authentication
  (`backend/api/routes.go:23-27`). Slots compute each host's busy time with that
  host's Google credential and tolerate a failing host
  (`backend/engine/booking.go:75-95`, `:114-128`, client built at `:202-208`);
  creation writes the event to each host's calendar with that host's credential
  (`:269`) and ignores a failure, so the booking is saved and confirmed anyway.
  No user is acting at all.
- **Public link info** (`GET /api/book/{slug}`, route `backend/api/routes.go:25`,
  no authentication) never uses or renews the credential, but it **reports on
  it to every visitor**. For each accepted host it adds one to
  `coverage.total`, and one to `coverage.checked` when that host's Google
  credential row loads (`backend/api/handlers_booking.go:75-93`, the count at
  `:87-90`). A stored row counts whether its refresh token works or not. The
  contract describes `checked` as the hosts "with a usable OAuth token"
  (`contracts/openapi/paths/booking.yaml:1215-1220`), which is not what the
  server counts. The public booking page shows the result to every visitor
  (`smart-calendar-flow/src/pages/PublicBooking.tsx:378-380`, rendering
  `src/components/CoveragePill.tsx`; the field is typed at
  `src/api/schedulingLinks.ts:62` and read at `:661`): "All participants
  checked" when `checked >= total`, otherwise "Partial availability" with a
  tooltip saying how many participants' availability "could not be verified".
  So today a host whose Google credential is dead still counts as checked,
  while the slots are computed without that host's calendar (bullet above). A
  host with no Google credential at all (for example one on Outlook) counts as
  not checked.

Every other Google consumer acts for the credential's owner, triggered either
by the owner's request or by a job running for the owner. The list below is
what Revision 4 found at `6d751f9`, given so a plan author can check it; it is
**not** the definition of where loss recording applies. D-13 applies it to
every consumer that renews the credential, listed here or not.

- **Scheduled jobs, with no request from the owner:** auto-decline every 15
  minutes (`backend/engine/auto_decline.go:77-87`, `backend/main.go:118-131`);
  the personal-blocker sync every 30 minutes for every user
  (`backend/main.go:92-114`, `backend/engine/personal_blocker.go:92`), which
  renews the owner's work Google credential through the request-context helper
  below; automatic focus-time scheduling, considered every minute per user
  (`backend/scheduler/cron.go:51-53`, wired at `backend/main.go:86`, running
  the focus-time engine); and the weekly manager team detection batch
  (`backend/engine/manager.go:191-195`, `backend/main.go:147-168`).
- **Background work started by the owner's request:** habit re-optimisation
  after habit edits (`backend/engine/habits.go:250-254`,
  `backend/api/handlers_habits.go:139,241,384`), analytics computation and its
  background recompute (`backend/engine/analytics.go:210-216`,
  `backend/api/handlers_analytics.go:33,91`), and manager team detection on the
  manager's request (`backend/api/handlers_manager.go:100,122`).
- **Request-driven paths that load the credential of the user in the request
  context:** the engine helper (`backend/engine/cal_iface.go:84-96`) behind
  focus time (`focus_time.go:54`), compression (`compression.go:43`) and smart
  scheduling (`smart_schedule.go:56`); and the API handlers that build a Google
  client for the caller (listing events and calendar free/busy,
  `backend/api/handlers_calendar.go:137-145`; event operations and meeting
  creation on an event, `backend/api/handlers_events.go:27-34`).

Team analytics reads only stored weekly rows (`team_availability.go:175-176`)
and uses no credential. The Outlook and Zoom credentials are only ever used for
their owner's own actions (§2.1, §2.2, §2.7).

## 3. Desired behaviour

From the user's point of view:

- **Connected means usable.** While a user's Outlook, Zoom or Google connection
  is reported as connected, Paceday can act with it. Access tokens that expire are
  renewed transparently with the user's own refresh credential; nobody has to
  sign in again because an hour passed.
- **Renewal is private to the user.** A renewed credential is stored for the user
  it belongs to and nobody else. One user's credential expiring, renewing or dying
  has no effect on any other user's connection or calendar reads.
- **Everything about a credential belongs to its owner, whoever caused it to be
  used.** Paceday sometimes uses one user's credential for someone else: a
  teammate asking for team availability, an anonymous visitor on a booking page,
  a scheduled job (§2.9). Whoever or whatever triggered the use, the renewed
  credential is stored for its owner, the owner's status surfaces change, and
  the connection-lost entry goes to the owner's activity log. The person or job
  that triggered it gets nothing: no entry in their log, no change to their
  status, and no answer that tells them more about the owner's connection than
  today's answer does. The same holds for what a public booking page says about
  its hosts: a visitor learns nothing about a host's loss that they could not
  learn today.
- **Renewal survives rotation and concurrency.** Where the provider issues a new
  refresh credential on renewal, the new one is kept. Two simultaneous actions by
  the same user do not leave the stored credential unusable.
- **A dead credential is visible, once per loss.** When renewal is refused by the provider
  (revoked, expired refresh credential, consent withdrawn), every place that
  tells the user whether that provider is connected (§2.6) shows it as not
  connected — including when it is not the user's calendar provider — and the
  user's own activity log gets one entry saying so, not one per failed request
  and not one per simultaneous request. This holds on every path that renews
  the credential, including the paths in §2.9 that use it for someone else or
  for a scheduled job. If the user reconnects and the new
  credential is later lost too, that second loss gets its own entry. A temporary failure
  (network, provider outage) does not mark the connection dead and is not
  recorded as a loss.
- **Availability lookups keep degrading, not failing.** A lookup that cannot use
  the credential still answers, with attendees' availability shown as unknown,
  exactly as today; what changes is that a refused credential is now reported
  lost. Meeting creation, which cannot degrade, fails with an error telling the
  user to reconnect. After a successful reconnect, no answer computed while the
  credential was dead is shown again.
- **Disconnect actually disconnects.** A user can disconnect Outlook and Zoom
  individually. Afterwards Paceday holds no usable credential for that provider
  for that user, the provider is asked to revoke Paceday's grant where it offers
  a way to revoke a single grant, and the connection reports not connected.
  Other users are untouched. If revocation cannot be confirmed, the local
  credential is still removed and the user is told.
- **Adding a meeting to an existing event still needs the Google connection.**
  That action reads the event through Google before it reaches Teams or Zoom
  (§2.8). When the Google credential is the one that is dead, the user is told
  through the Google status and activity log, exactly as for any other Google
  action; the Teams or Zoom connection is neither tried nor reported lost.
- **Reconnecting never makes things worse.** A reconnect that returns no new
  refresh credential does not erase a working one.
- **Connecting Outlook changes only the caller's calendar provider** — already
  true, kept true, and pinned by a test.

## 4. Acceptance criteria

"Provider" below means each of Outlook and Zoom unless stated; Google is named
where it applies. "Expired" means the stored access token's expiry is in the
past; for Zoom, whose expiry is not stored today, it means the access token is
past its issued lifetime or the provider rejects it as expired. Provider
responses come from a test double standing in for the provider's token,
revocation and API endpoints.

"Status surfaces" are the two of §2.6. For each provider, the surfaces that must
report it are:

- **Outlook**: the calendar connection status *when the user's calendar provider
  is Outlook*, and the Teams row of the conferencing provider list *always*.
- **Zoom**: the Zoom row of the conferencing provider list. (The calendar
  connection status does not report Zoom and is not required to.)
- **Google**: the calendar connection status *when the user's calendar provider
  is Google*, and the Google Meet row of the conferencing provider list
  *always*.

A row that the server does not show because its provider is not configured is
not required to appear. "Reports not connected" on the Teams row also means it
does not report the provider as usable by any other field of that row.

The actions each criterion applies to are named. "Meeting creation" means both
**standalone** meeting creation and meeting creation **on an existing event**.
They do not read the same credentials: the standalone create reads only the
Teams or Zoom credential, while the on-event action first reads the event with
A's **Google** credential and writes the link back with it, whatever A's
calendar provider is (§2.8).

- **Google precondition.** Every criterion below that runs the on-event variant
  for a Teams or Zoom meeting does so with A holding a working Google
  credential, unless the criterion says otherwise. Where the Google credential
  is the one refused on that path, AC-8's Google on-event row and AC-23 apply.
- **Users without a Google credential** cannot use the on-event variant today
  (401) or after this feature; that variant is out of scope for them (§5) and
  belongs with PAC-47, which owns Outlook as the working calendar for events.
  For them, "meeting creation" in these criteria means the standalone create
  only.

"Loss" means one episode: it starts at the first permanent refusal of a
credential that was usable (just connected or reconnected, or last renewed or
used successfully) and ends when A reconnects that provider successfully or
disconnects it. "Reconnect" for Outlook means a Microsoft sign-in that resolves
to A (§2.1, OQ-10); for Google it means a Google sign-in that resolves to A,
since the Google callback also attaches the credential to the user matched by
email (`backend/api/handlers_auth.go:63-70`, OQ-10).

"Owner" means the user a stored credential belongs to. In AC-1 to AC-25 the
owner is also the acting user A. AC-26 to AC-29 cover paths where the credential
is used for someone else, or for no user (§2.9). On every path, renewal
write-back, status changes and connection-lost entries apply to the owner only
(D-12). "Attributed to A" in any criterion means attributed to A *as the
credential's owner*, never as whoever made the request.

**Refresh** (ticket acceptance 1)

AC-1. Given user A's calendar provider is Outlook and A has an expired Outlook
access token and a valid refresh token, when an availability lookup runs for A
(querying attendees' free/busy with A's credential),
then the double receives exactly one refresh request carrying A's refresh token,
the calendar call is made with the new access token, and A's stored credential
afterwards holds the new access token and expiry. Fails today: no refresh
request is sent.  [unit]

AC-2. Given A and B both have Outlook credentials and A's is expired, when the
action in AC-1 runs for A, then B's stored credential is identical before and
after, and no request carrying B's tokens reaches the double.  [unit]

AC-3. Given A has an expired Zoom access token and a valid refresh token, when A
creates a Zoom meeting (standalone, and separately on an existing event with A
holding a working Google credential), then the meeting is created with a refreshed access token
and A's stored Zoom credential holds the new access token. Fails today: the
double's 401 is returned.  [unit]

AC-4. Given the double returns a **new** refresh token on refresh (rotation),
when AC-1 or AC-3 runs, then the stored refresh token is the new one, and a
second action after the new access token also expires succeeds using it.  [unit]

AC-5. Given A's Outlook (separately Zoom) access token is expired and the double
rotates refresh tokens and rejects any refresh token already used, when two
actions for A run concurrently within one backend process, then both succeed and A's stored credential
afterwards is usable for a third action.  [unit]

AC-6. Given A's Google access token is expired, when a Google calendar action
runs for A, then the refreshed access token and expiry are stored for A only, and
a second action within the new token's lifetime sends no refresh request. Fails
today: every action after expiry refreshes again.  [unit]

AC-7. Given a stored access token that has not expired, when an action runs,
then no refresh request is sent.  [unit]

**Failure surfacing**

AC-8. Given the double answers A's refresh request with a permanent refusal
(OAuth `invalid_grant`), when each action in the table runs for A (one test per
row), then:
(a) the action's observable result is the one in the table;
(b) every status surface named for that provider in the §4 preamble reports it
not connected;
(c) A's audit log holds exactly one new entry, attributed to A as the
credential's owner, naming the provider and that the connection was lost;
(d) B's status surfaces and audit log are unchanged.
Fails today: no row satisfies (b) or (c).  [contract]

| Provider | Action (precondition) | Observable result |
|---|---|---|
| Outlook | availability lookup (A's calendar provider is Outlook) | succeeds as today: each attendee that would have been queried has unknown coverage and no busy periods (D-6) |
| Outlook | Teams meeting creation, standalone and on an existing event (on-event: A holds a working Google credential) | fails with an error telling A to reconnect Outlook |
| Zoom | Zoom meeting creation, standalone and on an existing event (on-event: A holds a working Google credential) | fails with an error telling A to reconnect Zoom |
| Google | availability lookup (A's calendar provider is not Outlook: Google or WebCal; the lookup uses the Google credential for every provider other than Outlook, `freebusy_service.go:122-138`) | succeeds as today, as for Outlook (D-6) |
| Google | listing A's calendar events (any calendar provider: listing always uses the Google credential, §2.8) | fails with the same status and error shape that operation returns for a failed Google call today (D-7) |
| Google | Teams or Zoom meeting creation on an existing event (A's Teams or Zoom credential is valid) | fails with the same status and error shape the on-event action returns today when the event cannot be read through Google (D-7, D-10); the Teams or Zoom credential is not read (AC-23) |

AC-9. Given the state after AC-8, when three more actions for A hit the same dead
credential, then A's audit log still holds exactly one connection-lost entry for
that provider for this loss.  [unit]

AC-10. Given the double answers A's refresh with a temporary failure (timeout or
5xx), when each action in the AC-8 table runs for A, then: availability lookups
succeed with unknown coverage as today; meeting creation fails with an error
that does **not** tell A to reconnect (when the temporary failure is Google's on
the on-event path, it fails as it does today); listing events fails as it does today;
every status surface for that provider still reports connected; and no
connection-lost audit entry is written.  [unit]

AC-11. Given A has a working stored Outlook (separately Zoom) credential, when A
reconnects and the exchange returns an access token but no refresh token, then
A's stored refresh token is unchanged and A remains connected.  [unit]

AC-12. Given A's connection to Outlook, Zoom or Google was reported lost
(AC-8), when A reconnects that provider successfully, then every status surface
that AC-8 set to not connected reports connected again, and the AC-8 table's
actions for that provider succeed with the new credential (meeting creation
creates a meeting, standalone and, with A holding a working Google credential,
on an existing event; listing events returns events; on-event meeting creation
after a Google reconnect creates the meeting with A's valid Teams or Zoom
credential).  [unit]

AC-20. Given an availability lookup for A returned unknown coverage for an
attendee because A's credential was refused (AC-8), when A reconnects
successfully and the same lookup (same attendee, same day) runs again **within
15 minutes**, then the double receives a free/busy request made with the new
credential and the attendee comes back with known coverage. A degraded result
computed from a dead credential never outlives a reconnect. Fails today: the
cached `unknown` result is served without a request (§2.7).  [unit]

AC-21. Given A's calendar provider is Google and A also holds an Outlook
credential, when A's Outlook refresh is permanently refused during Teams meeting
creation, then the Teams row reports not connected, the calendar connection
status still reports Google connected, and one Outlook connection-lost entry is
written. Separately, given A's calendar provider is Outlook and A also holds a
Google credential, when A's Google refresh is permanently refused on a Google
action, then the Google Meet row reports not connected and the calendar
connection status is unchanged.  [unit]

AC-23. Given A requests a Teams (separately Zoom) meeting on an existing event
and the double permanently refuses A's **Google** refresh (`invalid_grant`),
then the response is the one in AC-8's Google on-event row; no request carrying
A's Microsoft or Zoom tokens reaches the double, even when that credential is
also expired or would also be refused; the Google Meet row and, when A's
calendar provider is Google, the calendar connection status report Google not
connected; A's audit log holds exactly one new connection-lost entry, naming
Google; and the Teams or Zoom row is unchanged and no Teams or Zoom
connection-lost entry is written.  [unit]

AC-24. Given A's Outlook (separately Zoom, separately Google) credential was
reported lost (AC-8), and A then reconnects successfully (AC-12), when the
double later permanently refuses the **new** credential's refresh on an action
from the AC-8 table, then A's audit log holds exactly **two** connection-lost
entries for that provider (one per loss), every status surface for that
provider reports not connected again, and further actions on the second dead
credential add no entry (AC-9 applies per loss). Fails against an
implementation that records at most one loss per user and provider ever.  [unit]

AC-25. Given A's Outlook (separately Zoom, separately Google) credential is
expired and the double permanently refuses its refresh, when two or more
actions for A that need that credential run concurrently within one backend
process, then each action's result is the one in the AC-8 table, every status
surface for that provider reports not connected, and A's audit log holds
exactly **one** new connection-lost entry for that provider. Whether this holds
across several backend processes is OQ-9.  [unit]

**Credentials used for someone else** (§2.9, D-12)

AC-26. Given A and B are members of the same team, B's Google access token is
expired and the double permanently refuses B's refresh (`invalid_grant`), and
A's own Google credential is valid, when A requests the team's availability,
then: the response is the one returned today when B's calendar cannot be read
(B contributes no busy periods), with no field that says B's connection was
lost; B's Google Meet row and, when B's calendar provider is Google, B's
calendar connection status report not connected; B's audit log holds exactly
one new connection-lost entry naming Google; and A's audit log, A's status
surfaces and A's stored credential are unchanged. If A and a third member C
request the team's availability concurrently within one backend process and
both reach B's refusal, B still gets exactly one entry and neither A's nor C's
log changes. Separately, when the double answers B's refresh with a temporary
failure (timeout or 5xx) instead, the response is the same, B's status surfaces
still report connected, and no connection-lost entry is written to anyone's
log. Fails against an implementation that writes the entry to the requester's
log, writes none, or records every swallowed error as a loss.  [contract]

AC-27. Given A and B are members of the same team and B's Google access token
is expired with a valid refresh token, when A requests the team's
availability, then the double receives exactly one refresh request carrying B's
refresh token, B's stored credential afterwards holds the new access token and
expiry, A's stored credential is identical before and after, and the response
includes B's busy periods as it does today for a working credential. Fails
today: B's renewal is not stored.  [unit]

AC-28. Given a public scheduling link whose host H has an expired Google access
token and the double permanently refuses H's refresh, when an unauthenticated
visitor requests the link's slots, and separately when a visitor creates a
booking on it, then: each response has the same status and body it has today
when the host's calendar cannot be read or written (slots computed without H's
calendar events; the booking saved and confirmed without an event on H's
calendar); neither response says H's connection was lost; H's status surfaces
report Google not connected; and H's audit log holds exactly one new
connection-lost entry naming Google for this loss, however many slot and
booking requests reach the refusal. No other user's log or status changes.
After H's loss has been recorded, the link's public info response is identical,
status and body, to the one returned for the same link before the loss: in
particular H still counts as checked in its coverage (D-14). Separately, when
the double answers H's refresh with a temporary failure (timeout or 5xx)
instead, the slots, booking and link-info responses are as above, H's status
surfaces still report connected, and no connection-lost entry is written.
Separately, given H's token is expired with a valid refresh token, the renewed
token is stored for H only. Fails today: no entry and no status change for H;
and it fails against an implementation that stops counting a host whose loss
is recorded.  [contract]

AC-29. Given B's Google access token is expired and the double permanently
refuses B's refresh, when a job runs for B with no request from B (the
scheduled auto-decline run, separately the personal-blocker sync, and
separately the weekly manager team detection batch for B), then B's status
surfaces report Google not connected, B's audit log holds exactly one new
connection-lost entry naming Google for this loss, no other user's log or
status changes, and the job's outcome for B is what it is today. When the
double answers with a temporary failure (timeout or 5xx) instead, B's status
surfaces still report connected and no connection-lost entry is written. Fails
today: no entry and no status change.  [unit]

**Disconnect and revocation** (ticket acceptance 2)

AC-13. Given A and B both have Outlook credentials, when A disconnects Outlook,
then A holds no stored Outlook credential, A's status reports Outlook not
connected, Teams is reported not connected for A, and B's stored credential and
status are unchanged. Fails today: there is no way to disconnect Outlook.  [contract]

AC-14. Given A has a Zoom credential, when A disconnects Zoom, then the Zoom
double receives one revocation request for A's grant, A holds no stored Zoom
credential, and A's conferencing provider is Meet. Fails today: no revocation
request is sent.  [unit]

AC-15. Given the Zoom double is unreachable or refuses the revocation, when A
disconnects Zoom, then A's local Zoom credential is still removed, the response
tells A that revocation at Zoom could not be confirmed, and A's audit log records
the disconnect and the unconfirmed revocation.  [contract]

AC-16. Given A disconnects Outlook or Zoom, then A's audit log holds one entry
attributed to A recording the disconnect, and B's audit log is unchanged.  [unit]

AC-17. Given A disconnects Outlook, then the double receives a revocation request
only if OQ-1 establishes that Microsoft offers revocation of a single app grant;
otherwise it receives none, no request revoking A's other Microsoft sessions is
sent, and the response tells A the grant can be removed from their Microsoft
account.  [unit]

AC-18. Given A and B have Google credentials and A's calendar provider is
Google, when A disconnects the calendar, then B's Google credential is still
stored. (Already true per §2.3; pins it. Revision 4: confirm no PAC-35 test
already asserts B's row before adding one; `handlers_auth_test.go` and
`pac35_auth_flows_test.go` assert A's own deletion and session.)  [unit]

**Provider flip (kept)**

AC-19. Given A's and B's calendar providers are Google, when A completes a
Microsoft sign-in, then A's calendar provider is Outlook and B's is still Google.
(Already true per §2.1; pins it.)  [unit]

**Activity-log contract**

AC-22. Given the activity log's documented set of entry kinds, which is closed
(§2.4), when this feature's contract is applied, then the set holds every kind
it held before, unchanged, plus kinds for: connection lost, provider
disconnected, and revocation not confirmed; and a listing that returns entries
of every new kind alongside existing ones conforms to the contract. Fails
today: such a listing is non-conforming.  [contract]

## 5. Explicitly out of scope

- **Personal-calendar credentials** (§0, OQ-8).
- **Outlook as the working calendar for event listing and creation** (the empty
  access token in the generic provider factory): PAC-47.
- **Revoking Google on calendar disconnect.** Calendar disconnect's session
  behaviour was settled by PAC-35 (API-061 Fixed: the session is kept) and its
  409 for Outlook and WebCal users by PAC-35 D-8; neither is changed here, and
  sending a revocation to Google on that disconnect is not added (OQ-3).
- **Proactive background refresh** for users who are not acting. A credential is
  renewed when it is needed; a scheduled job acting for a user gets renewal on
  the same terms when it uses the credential, nothing more.
- **Encrypting stored credentials at rest.** Worth its own issue.
- **Changing how availability lookups fail.** They keep degrading (D-6). Caching
  of a degraded result after a *temporary* failure is unchanged; only a reconnect
  invalidates it (AC-20).
- **Changing Google operations' failure responses** (D-7).
- **Renewal coordination across several backend processes** (OQ-9).
- **Provider error text echoed to the client (T8).**
- **Frontend copy and placement** of the reconnect prompt and disconnect
  controls; the web repo implements against the contract after stage 2. The
  frontend stage **owes** the message that Zoom revocation could not be
  confirmed (D-2, AC-15): today's caller shows a success toast whatever the
  response says (`smart-calendar-flow/src/components/ConferencingSection.tsx:98-99`),
  so the backend PR alone does not satisfy D-2.
- **Meeting creation on an existing event for users without a Google
  credential** (§2.8). It fails with 401 today and keeps doing so; reading
  events from Outlook belongs to PAC-47.
- **Changing what the on-event action answers when Google is refused** (D-10).
- **Telling a requester or booking visitor that a teammate's or host's
  connection was lost.** Team availability and public booking answers stay as
  they are today (D-12), and so does the public link info's host coverage
  (D-14); exposing another user's credential state to them would widen who sees
  data.
- **Making the public coverage count truthful.** Today a host with a dead
  Google credential counts as checked while slots ignore their calendar (§2.9).
  Fixing that for visitors means telling them about the loss, which D-14
  declines. Only the contract's description is corrected (§6).
- **Booking confirmed without an event on a host's calendar** when the host's
  credential is dead (§2.9). Today's behaviour; the host is now told through
  their status and activity log, but making booking creation fail or warn is a
  product decision for a follow-up.
- **Bypassing the availability cache after a disconnect-then-reconnect** inside
  15 minutes when no refusal occurred. AC-20 covers only results computed from a
  refused credential (D-11).

## 6. What must not change

- One user's connect, refresh, failure or disconnect never reads or writes
  another user's credential, provider or audit log. Protected today by
  `TestAuthStatus_DescribesOnlyTheCaller` (`backend/api/pac24_scope_test.go:128`)
  and `TestZoomTokens_ArePerUser` (`backend/storage/settings_test.go:154`).
  Nothing protects B's credential during a refresh of A's (AC-2 adds it), nor
  B's Google row during A's disconnect (AC-18 adds it).
- Zoom disconnect returns conferencing to Meet (`storage/conferencing.go:12`;
  asserted at `settings_test.go:178`).
- Credentials never appear in any API response or audit entry (the settings
  document hides them: `backend/storage/settings.go:200`). New audit entries name
  the provider and event, never token material. No test asserts this for audit
  entries today; the plan should add one.
- Google sign-in and refresh keep working for users who never touch Outlook or
  Zoom (covered by the existing e2e calendar journeys against
  `e2e/fake-google/`).
- Existing status response fields keep their meaning for existing consumers;
  whether fields are added is the contract's call.
- **The activity log's existing entry kinds keep their meaning.** The seven
  values (`focus_created`, `focus_cleared`, `meeting_scheduled`,
  `meeting_moved`, `meeting_created`, `nlp_parsed`, `nlp_confirmed`) are neither
  removed, renamed nor redefined, and their writers are unchanged; the set only
  grows (AC-22, PAC-23 AC-14). Protected today by PAC-23's contract test of the
  enum against the writers.
- **Availability lookups keep answering.** `POST /api/freebusy` keeps returning
  its 200 with per-attendee coverage when a credential fails, and manager team
  stats keep treating unknown coverage as "no data" (§2.7). No test today
  asserts the degraded 200 on a refused refresh; AC-8 adds one.
- **New activity-log kinds do not redefine the existing ones.** The activity
  log entry schema describes every entry as about the calling user's own
  calendar (`contracts/openapi/openapi.yaml:5545`). Connection entries are about
  a credential (Zoom is not a calendar), so the contract stage will reword that
  description; the rewording must widen it to cover the new kinds and must not
  change what any of the seven existing kinds means.
- **Team availability and public booking responses do not change** (D-12,
  D-14). That covers team availability, public booking slots, public booking
  creation, and public link info, whose host coverage keeps counting a host
  with a stored Google credential as checked whether or not the credential
  still works. The link-info consumer is the public booking page's coverage pill
  (`smart-calendar-flow/src/pages/PublicBooking.tsx:378-380`,
  `src/components/CoveragePill.tsx`; field typed at
  `src/api/schedulingLinks.ts:62`). Protected today by
  `TestTeamAvailability_UsesInjectedEngineAndQuery`
  (`backend/api/handlers_teams_test.go`), `TestPublicBookingSlotsContract`
  and `TestPublicBookingCreateContract` (`backend/api/handlers_booking_test.go`)
  and the e2e journey `e2e/tests/public-booking.spec.ts`. None of them runs
  with a refused host or member credential, and no test pins link-info
  coverage for a host with a dead credential; AC-26 and AC-28 add that.
- **The public coverage description says what is counted.** The contract
  describes `checked` as hosts "with a usable OAuth token"
  (`contracts/openapi/paths/booking.yaml:1215-1220`); the server counts hosts
  with a stored Google credential, usable or not (§2.9). Under factory §7.2 the
  contract stage rewrites that description to match the server (and D-14),
  changing no field, type or value; the web types regenerate from it.
- **The existing Zoom disconnect caller keeps working** whatever the new
  response carries (`smart-calendar-flow/src/api/client.ts:1463` treats it as
  no content; `src/components/ConferencingSection.tsx:98` ignores the result).

## 7. Rollback

Reverting the code restores today's behaviour: credentials stop being renewed and
disconnect stops revoking. Credentials renewed in the meantime remain valid and
readable by the old code if the stored shape for Outlook and Google is kept,
which the plan should require. If the plan adds stored information for Zoom
(expiry) or a lost-connection marker, its down migration drops only that derived
information; no credential is lost. A grant already revoked at Zoom by a
disconnect cannot be restored by rollback — the user reconnects, which is what
they asked for by disconnecting.

## 8. Open questions

- **OQ-1 — Does Microsoft offer revocation of a single app grant?** Owner:
  contract-author. Resolve from the Microsoft identity platform documentation. If
  the only server-side option revokes all of the user's sessions, it must not be
  used (D-5), and AC-17's "none" branch applies.
- **OQ-2 — Zoom revocation semantics.** Owner: contract-author. Confirm Zoom's
  revocation revokes this app's grant only, and which token it accepts.
- **OQ-3 — Where does Outlook disconnect live?** Owner: contract-author. A new
  operation next to Zoom's, or a change to calendar disconnect. Since PAC-35,
  calendar disconnect answers 409 and deletes nothing for an Outlook user
  (PAC-35 D-8), and PAC-35 OQ-5 left "disconnect for Outlook/webcal users" to
  its own issue; this spec is where Outlook's half of that lands. Routing it
  through calendar disconnect would change PAC-35's 409 for Outlook users and
  must keep D-3 (calendar provider unchanged) and PAC-35 D-8 for WebCal; a
  separate operation leaves PAC-35's behaviour untouched. The spec's criteria
  hold either way.
- **OQ-4 — Does AC-11 change the Microsoft or Zoom callback's documented
  behaviour?** Owner: contract-author. If yes, API-011 and API-012 come into
  scope for the Microsoft callback (factory §7.1).
- **OQ-5 — Should a Microsoft sign-in keep resetting the user's calendar provider
  to Outlook?** Owner: product owner. Today it overwrites a WebCal choice on every
  sign-in (§2.1). D-4 records what the factory assumed.
- **OQ-6 — Can a Microsoft or Zoom code exchange return no refresh token?**
  Owner: plan-author. AC-11 holds either way; this decides whether it is a live
  risk or a guard.
- **OQ-7 — Microsoft refresh-token inactivity expiry.** Owner: plan-author. A user
  whose refresh token lapsed through inactivity is covered by AC-8 (reported lost,
  reconnect). Confirm nothing else is expected.
- **OQ-8 — Personal-calendar split.** Owner: product owner / triage. File the
  follow-up proposed in §0, or tell the factory to fold it in.
- **OQ-9 — Can more than one backend process act for a user at once?** Owner:
  plan-author. AC-5 is stated for one process. If the deployment runs several
  replicas or a separate scheduler process sharing the database, the plan must
  say whether rotation-safe renewal holds across them, and the spec gains a
  criterion. The same applies to AC-25: one connection-lost entry per loss is
  required within one process; across processes is undecided here.
- **OQ-10 — Outlook reconnect when the Microsoft email no longer matches.**
  Owner: product owner, with contract-author. A Microsoft sign-in attaches the
  credential to the user matched by the Microsoft account's email and signs the
  browser in as that user (§2.1). If A's Microsoft email differs from A's
  Paceday email, the reconnect AC-8 asks for signs the browser into another
  (possibly new) account and leaves A reported lost. The criteria assume a
  sign-in that resolves to A. What A should see otherwise (refusal, a warning,
  linking by Microsoft account identity) is a product decision; this feature
  assumes no change to sign-in matching (in the spirit of D-4, sign-in
  behaviour is not changed for the lifecycle). The same applies to Google: its
  sign-in also resolves the user by email (`backend/api/handlers_auth.go:63-70`),
  and AC-12, AC-20 and AC-24 assume a Google sign-in that resolves to A.

## Decisions

*None of these has a recorded owner answer. Each is the most conservative option
consistent with PRODUCT.md ("Privacy is not a setting to trade away",
`smart-calendar-flow/PRODUCT.md:90`) and earlier owner decisions (PAC-24
Decisions, `docs/specs/PAC-24.md:986-1002`, where the owner chose that
`microsoft_tokens` and `zoom_tokens` are **not copied** between users when
per-user settings rows were created, `:1001-1002`).*

- **D-1 — A refused renewal does not delete the stored credential.** It is
  reported as lost (AC-8) and kept until the user reconnects (which replaces it)
  or disconnects (which removes it). Rationale: no automatic path deletes user
  data, and a misclassified temporary failure stays recoverable. *Assumed by the
  factory, pending owner confirmation.*
- **D-2 — Disconnect removes locally even when revocation fails.** The user's
  instruction to withdraw is honoured locally; a failed revocation is told to the
  user and recorded, never silently ignored, and never blocks local removal.
  *Assumed by the factory, pending owner confirmation.*
- **D-3 — Disconnecting Outlook does not change the user's calendar provider.**
  The user stays on Outlook, shown as not connected, until they reconnect or
  choose another provider. Silently switching to Google could show a different
  account's calendar than the one they chose. *Assumed by the factory, pending
  owner confirmation.*
- **D-4 — The sign-in provider reset (OQ-5) is left as is.** Changing it alters
  sign-in behaviour for every Microsoft user and is not needed for the lifecycle.
  *Assumed by the factory, pending owner confirmation.*
- **D-5 — No account-wide Microsoft session revocation.** If single-grant
  revocation does not exist (OQ-1), Paceday does not fall back to revoking all of
  the user's Microsoft sessions; that would act on far more than the user asked.
  *Assumed by the factory, pending owner confirmation.*
- **D-6 — Availability lookups keep degrading on a dead or failing credential.**
  They answer with unknown coverage, as today, rather than failing; the loss is
  surfaced through the status surfaces and the activity log instead. Rationale:
  two consumers depend on the lookup succeeding (§2.7) and changing that is a
  contract change to a surface this issue does not ask about. *Assumed by the
  factory, pending owner confirmation.*
- **D-7 — Google operations' failure responses do not change.** A refused Google
  renewal is surfaced through status and the activity log; the failing request
  returns what it returns today. Rationale: smallest contract surface, and it
  keeps U-03 and API-091 on `GET /api/calendar/freebusy` out of scope unless the
  contract stage chooses otherwise. *Assumed by the factory, pending owner
  confirmation.*
- **D-8 — Disconnecting Outlook while conferencing is Teams returns conferencing
  to Meet,** mirroring what Zoom disconnect does today
  (`backend/storage/conferencing.go:12`). Leaving Teams selected would make every
  meeting creation fail until the user changed it. No data is deleted. *Assumed
  by the factory, pending owner confirmation.*
- **D-9 — Zoom disconnect keeps setting conferencing to Meet unconditionally,**
  including when the user had chosen Teams (`backend/storage/conferencing.go:12`).
  Changing it is a separate behaviour change not needed for the lifecycle; it is
  recorded here so it is a decision rather than an accident. *Assumed by the
  factory, pending owner confirmation.*
- **D-10 — When Google is refused on the on-event meeting path, the response
  stays what it is today** (404 "event not found", §2.8); the loss
  is surfaced on Google's status surfaces and in the activity log (AC-23), and
  the Teams or Zoom credential is not read, renewed or reported. Rationale: it
  follows D-7 (no Google operation's failure response changes) and keeps a
  Google failure from being blamed on a Teams or Zoom connection that may be
  fine. The response is misleading to a user; changing it is a contract change
  for the contract stage or a follow-up, not assumed here. *Assumed by the
  factory, pending owner confirmation.*
- **D-11 — A reconnect invalidates only availability results computed from a
  refused credential.** A disconnect followed by a reconnect within 15 minutes,
  with no refusal, may still serve a cached result (§2.7) for the rest of that
  window. Rationale: smallest behaviour change that closes the defect the
  challenge found; nothing is deleted and no one sees more data. *Assumed by the
  factory, pending owner confirmation.*
- **D-12 — Renewal, status and loss always belong to the credential's owner,
  and other people's answers do not change.** When a credential is used for
  someone else (team availability, public booking) or for a job, the renewed
  credential is written for its owner, the owner's status surfaces change and
  the connection-lost entry goes to the owner's activity log. The requester's
  log and status are untouched, and team availability and booking responses
  stay exactly as today. Rationale: attributing to the requester would show A
  B's credential state and leave B uninformed; recording nothing would leave
  B's dead credential invisible; changing the responses would widen who sees
  data. *Assumed by the factory, pending owner confirmation.*
- **D-13 — Loss recording applies to every consumer that renews the
  credential,** not only the actions in the AC-8 table, including scheduled
  and background jobs acting for the owner (§2.9). This adds no proactive
  refresh (§5): a credential is still renewed only when something needs it.
  Rationale: §3 promises the loss is visible "every place"; narrowing it to a
  list of actions would leave a dead credential invisible to a user whose only
  use is through a job or a teammate. *Assumed by the factory, pending owner
  confirmation.*
- **D-14 — The public link info's host coverage does not change when a host's
  connection is lost.** A host whose Google loss has been recorded still
  counts as checked, exactly as a host with a dead credential does today
  (§2.9); the response is identical before and after the loss (AC-28). Options
  considered: (a) stop counting a lost host, which matches the contract's word
  "usable" but tells every anonymous visitor with the link that some host's
  connection is dead, on a page the host did not choose to show it on: that
  widens who sees data, against D-12 and §5; (b) keep counting stored
  credentials, chosen. The cost of (b) is stated plainly: the page can keep
  saying "All participants checked" while the slots ignore a lost host's
  calendar, as it does today; the host is told through their own status and
  activity log (AC-28). The contract's description of the count is corrected
  to say what is counted (§6, factory §7.2), so the contract stops claiming
  "usable". *Assumed by the factory, pending owner confirmation.*

## Challenge — 2026-10-26

**Verdict**: accept-with-changes

Reviewed against the committed spec (910145e), the Linear issue text, the
backend at `16f3100`, `contracts/openapi/` and `smart-calendar-flow` at
`d9e0786`. The spec correctly understands the issue's problem, and almost every
§2 citation I checked is accurate. The defects are in what AC-8 leaves undefined
and in an understated inherited scope.

### Blocking

1. **AC-8 and AC-10 say "the action fails" for availability lookups. Those
   lookups degrade by design today instead of failing, and the spec does not say
   which behaviour it wants.** `FreeBusyService.Query`
   (`backend/engine/freebusy_service.go:74-155`) never returns a provider error.
   A failed Outlook or Google fetch becomes `coverage: "unknown"` with empty busy
   slots in a 200, and that result is **cached for 15 minutes** (`:140-152`).
   Two consumers depend on the 200: `POST /api/freebusy`
   (`backend/api/handlers_freebusy.go`) and the manager team-stats path
   (`backend/engine/manager.go:484-487`, which uses the result only when
   `err == nil`). Read literally, AC-8 requires both to start failing, which
   breaks them. Read loosely, a test of Teams creation alone satisfies AC-8, so
   the lookup that AC-1 names is never exercised. The cache causes a further
   problem: after AC-12's reconnect, a lookup can keep returning the cached
   "unknown" for up to 15 minutes, so AC-12's "actions succeed" does not hold
   for this action as written. *Resolves when* AC-8 and AC-10 name the actions
   they apply to and state, per action, the observable result when the
   credential is dead or failing temporarily. That result is either an error the
   user sees, or the existing degraded response plus the status change. AC-12
   must also state whether a result computed from a dead credential may outlive
   a reconnect.

2. **The spec does not define "connection status". Under D-1, two status
   surfaces would keep reporting connected after AC-8 passes.**
   `GET /api/auth/status` reports only the user's current `calendar_provider`
   (`backend/api/handlers_auth.go:97-132`). It says nothing about Zoom. It also
   says nothing about Outlook for a user whose calendar provider is not Outlook
   but whose Microsoft credential still drives Teams. The other surface is
   `GET /api/conference/providers`, which derives `teams` from
   `microsoftToken != nil` and `google_meet` from `googleToken != nil`
   (`backend/api/handlers_conferencing.go:173,179`). D-1 keeps the refused
   credential stored. A test of AC-8 that checks auth status alone therefore
   passes while `teams` and `google_meet` still report `connected: true`. That is
   the "connected but unusable" state §3 forbids. AC-13 already requires Teams to
   report not connected after a disconnect; AC-8 has no equivalent. *Resolves
   when* AC-8 and AC-12 name every status surface that must report not
   connected, for each of Outlook, Zoom and Google. That means auth status plus
   the `zoom`, `teams` and `google_meet` rows of conferencing providers. They
   must also cover the case where the lost credential is not the user's current
   calendar provider.

3. **The new audit entries change the `GET /api/audit` contract, and the spec
   does not list that operation.** AC-8, AC-15 and AC-16 require new audit
   entries. `AuditEntry.action` is a **closed** enum of seven values
   (`contracts/openapi/paths/calendar.yaml:1669-1672`, enum at `:1716-1723`):
   "adding an eighth action is a contract change". The inherited-scope section
   names the auth status, disconnect, sign-in, callback and conferencing
   operations, but not `GET /api/audit`. Three web consumers are affected:
   - `smart-calendar-flow/src/api/generated/schemas.ts:38`: `AuditEntry` uses
     `z.enum` over the seven values. The day this schema is applied, a new value
     throws a validation error and the whole response is lost.
   - `smart-calendar-flow/src/api/client.ts:478`: the normaliser passes unknown
     strings through unchanged.
   - `smart-calendar-flow/src/pages/Audit.tsx:97`: renders `entry.action` raw,
     so a new value shows up as a bare identifier.

   *Resolves when* the spec does three things: lists `GET /api/audit` as an
   operation this feature changes; requires the new action values to be added to
   the enum without removing any (PAC-23's no-shrink rule); and records in §6
   that existing values keep their meaning. I checked that operation's inherited
   items: U-02, API-005 and API-074 are all resolved, so no further scope comes
   with it.

4. **The inherited-scope section is incomplete, so its claim about which
   markers and findings apply does not hold.**
   - `POST /api/events/{id}/conference` (`contracts/openapi/openapi.yaml:1717`)
     also creates Teams and Zoom meetings with the static token, via
     `ProviderForRequest` (`backend/api/handlers_conferencing.go:33-48`,
     `:337-345`). AC-3 and AC-8 reach it ("creates a Zoom meeting", "Zoom
     action"), but it lies outside the cited ranges (`:800-830`, `:934-1230`,
     `:1545-1630`). I found no `x-uncertain` and no audit finding on it, but the
     spec has to say so.
   - The spec omits open findings on operations it does name: **API-052**
     (`POST /api/conference/create`, Reported, `docs/factory/api-audit.md:286`),
     **API-091** (same operation, `:325`) and **API-070**
     (`GET /api/conference/providers`, Reported, `:304`). AC-3 changes conference
     create, and AC-8 and AC-13 change what the providers operation reports.
   - Blocking 1 might be resolved by changing the failure response of
     Google-backed operations. In that case each of those operations comes into
     scope too, including `GET /api/calendar/freebusy`, which carries U-03
     (`contracts/openapi/openapi.yaml:1493-1494`) and API-091.

   *Resolves when* the inherited-scope section lists each of these with the same
   condition used for API-061: it comes into scope if the contract stage edits
   that operation.

### Non-blocking

1. AC-15 requires the Zoom disconnect response to tell A that revocation was not
   confirmed. Today that operation returns 204 and the web caller ignores the
   result (`smart-calendar-flow/src/api/client.ts:1463`,
   `src/components/ConferencingSection.tsx:98`). §5 rightly leaves the copy to
   the web repo. §6 should still note that the existing caller must keep working
   whatever the new response is.
2. The spec does not say what conferencing becomes when a Teams user
   (`conferencing_provider = 'teams'`) disconnects Outlook. Zoom disconnect
   returns conferencing to Meet (§6), and D-3 covers only the calendar provider.
   A sentence settling the Teams case would close the gap.
3. `ClearZoomTokens` sets conferencing to `meet` even when the user had chosen
   Teams (`backend/storage/conferencing.go:12`), and §6 keeps this as "must not
   change". It is worth an explicit decision rather than an inherited accident.
4. AC-5's "run concurrently" does not say whether two backend processes count.
   If the deployment can run more than one replica, the criterion should say
   whether it must hold across replicas.
5. AC-1 says the lookup "reads A's Outlook calendar". It actually queries the
   *attendees'* free/busy using A's credential (`freebusy_service.go:123-127`).
   The test is the same either way; the wording should match the code.
6. The Decisions preamble attributes "credentials belong to one user and are
   never shared" to `docs/specs/PAC-24.md:986`. That section records that
   `microsoft_tokens` and `zoom_tokens` are not copied between users (table at
   `:1001-1002`). That supports the claim but does not say it. Quote what is
   there.
7. §2.5 says the Microsoft and Zoom hosts are routed to the refusing sink. That
   is true, but Google's hosts are in the same list (`e2e/fake.ts:27-35`); the
   Google stand-in is reached another way. This is harmless, but the sentence
   implies Google is not in the sink list.

### Criteria I could not falsify
- AC-8 for availability lookups: no test can tell pass from fail until the spec
  states the expected result of a lookup on a dead credential (Blocking 1).
- AC-12 for availability lookups: the 15-minute cache makes the outcome depend
  on timing (Blocking 1).

Every other criterion has a test that fails today and would pass afterwards:
- AC-1, AC-3, AC-4, AC-6: no refresh request or write-back exists today.
- AC-2, AC-18, AC-19: pin behaviour that is already true, as the spec says.
- AC-5: nothing serialises renewal today.
- AC-7: passes today; it guards against eager refresh.
- AC-9, AC-10: no credential audit entries exist.
- AC-11: `SaveMicrosoftToken` and `SaveZoomTokens` overwrite the stored
  credential with whatever the exchange returned.
- AC-13, AC-17: there is no Outlook disconnect route.
- AC-14, AC-15, AC-16: there is no revocation call and no disconnect audit
  entry.

### Citations I checked and found wrong
- None that matter. Non-blocking 6 and 7 cover two imprecise ones. The
  following are correct:
  - Microsoft: `microsoft_oauth.go:30,43-47,52-70,68,74-89`;
    `handlers_microsoft_auth.go:56,72,73`; `freebusy_service.go:123-127`.
  - Conferencing: `handlers_conferencing.go:40-44,109,112,113,139-144,152-155,169,176,178-179,186-192,342-345`;
    `client_factory.go:20-21,78-79`.
  - Auth and routes: `handlers_auth.go:70,98-105,113-131,135-144`;
    `routes.go:39,48,127`.
  - Zoom: `personal_calendars.go:157-168`; `zoom_oauth.go:19,32-60`;
    `factory.go:17-21`; `zoom.go:36`; `conferencing.go:11-14`.
  - Google: `token_store.go:16-30,40-59`; `google_oauth.go:32-33`;
    `handlers_calendar.go:143`. As §2.3 says, the sign-in callback's
    `UpsertUserToken` is the only writer of Google tokens.
  - Tests: `settings_test.go:154-180`; `pac24_scope_test.go:128-164`;
    `handlers_auth_test.go:42-50` (asserts the status code only).
  - Other: `focus_blocks.go:80-90` (seven callers, none credential-related);
    `settings.go:200`; `personal_reader.go:18-30`; `personal_blocker.go:72,87`;
    `PRODUCT.md:90`; api-audit rows API-011, API-012 and API-061.

### Consumers this breaks
- clockwise-like/backend/engine/manager.go:486: expects `FreeBusyService.Query`
  to succeed. It breaks if AC-8 is read as "availability lookups fail"
  (Blocking 1).
- clockwise-like `POST /api/freebusy` (`backend/api/handlers_freebusy.go`) and
  its web caller: the same risk (Blocking 1).
- smart-calendar-flow/src/api/generated/schemas.ts:38 and src/pages/Audit.tsx:97:
  new audit action values (Blocking 3).
- smart-calendar-flow/src/api/client.ts:1463: Zoom disconnect is typed `void`
  and must keep working if the response gains a body (Non-blocking 1).

## Revision 1 — 2026-10-26

Answers the four blocking findings of the round-1 challenge, and the
non-blocking ones where cheap. Re-checked against the backend and contract at
`2eb07a5`.

### Blocking 1 — availability lookups vs "the action fails"

Accepted. §2.7 now records that the lookup degrades to unknown coverage in a
200, caches that for 15 minutes, and that two consumers rely on it. AC-8 is a
per-action table: availability lookups (Outlook and Google) **keep degrading**
and the loss shows only in status and the activity log (D-6); Teams and Zoom
meeting creation fail with a reconnect error; listing Google events keeps
today's failure response (D-7). AC-10 states the same actions for a temporary
failure. On the cache: **a result computed from a dead credential may not
outlive a reconnect** — new AC-20 tests it inside the 15-minute window. AC-12 now
names what "actions succeed" means per action. Because the lookup's response is
unchanged, `POST /api/freebusy` and manager stats keep working (§6).

### Blocking 2 — "connection status" undefined

Accepted. §2.6 describes both status surfaces as they are today. The §4
preamble names, per provider, which surfaces must report it: Outlook = calendar
connection status when Outlook is the calendar provider, plus the Teams row
always; Zoom = the Zoom row; Google = calendar connection status when Google is
the calendar provider, plus the Google Meet row always. AC-8(b) and AC-12 apply
to all of them. New AC-21 covers a lost credential that is not the current
calendar provider, in both directions. The Teams row's `enabled` flag is covered
by "does not report the provider usable by any other field". §3-§5 refer to the
surfaces by role and §2.6 maps roles to today's operations, so the
no-endpoints-in-§3-5 rule holds while the mapping stays explicit.

### Blocking 3 — `GET /api/audit` contract change

Accepted. Inherited scope lists `GET /api/audit` as **changed**. §2.4 records
the closed enum and its three web consumers. New AC-22 [contract] requires the
set to grow by the three new kinds without losing or changing any existing one.
§6 records that the seven existing values keep their meaning and writers.
U-02, API-005 and API-074 are resolved, as the challenger found.

### Blocking 4 — inherited scope incomplete

Accepted. The inherited-scope section is rewritten as a per-operation list. It
adds `POST /api/events/{id}/conference` (checked: no marker, no finding),
API-052 and API-091 on `POST /api/conference/create`, API-070 on
`GET /api/conference/providers`, and `GET /api/calendar/freebusy` with U-03 and
API-091. Each carries the API-061 condition. Also found and added:
**API-064** (`api-audit.md:298`) on `GET /api/auth/microsoft`, and the open
findings on `POST /api/freebusy` (API-028, API-046, API-077), listed as
conditional since D-6 leaves its response unchanged.

### Non-blocking

1. §6 now requires the existing Zoom disconnect caller to keep working.
2. Teams conferencing on Outlook disconnect: D-8 (returns to Meet).
3. Zoom disconnect resetting a Teams choice: D-9 (kept, recorded as a decision).
4. AC-5 now says "within one backend process"; the multi-process case is OQ-9.
5. AC-1 now says the lookup queries attendees' free/busy with A's credential.
6. The Decisions preamble quotes what PAC-24 actually records.
7. §2.5 now says Google's hosts are in the sink list too.

## Challenge — 2026-10-26 (round 2)

**Verdict**: accept-with-changes

Reviewed the committed spec at `f2337f9` (Revision 1) against the Linear issue
text, the backend and contract at `16f3100` (= `origin/main`, unchanged since
round 1), and `smart-calendar-flow` at `d9e0786`. The four round-1 blocking
findings are resolved: §2.7 and the AC-8 table settle availability lookups,
§2.6 and the §4 preamble define the status surfaces, `GET /api/audit` is listed
as changed with AC-22, and the inherited-scope list is complete. I re-derived
every `x-uncertain` location in `openapi.yaml` by enclosing path or schema and
found none on a touched operation beyond those listed. Two new defects remain.

### Blocking

1. **"Meeting creation on an existing event" also depends on the Google
   credential, which the spec says it does not.** The §4 preamble says both
   meeting-creation actions "read the same stored credential". They do not.
   `POST /api/events/{id}/conference` loads the event through the Google
   client *before* any Teams or Zoom credential is touched
   (`backend/api/handlers_conferencing.go:266-275` calls `loadEventClient`,
   which is `eventHandlers.calClient`, `backend/api/handlers_events.go:24-32`,
   built from `auth.LoadUserToken`), whatever the calendar provider is.
   Consequences for the criteria as written:
   - For a user with no Google credential (the Outlook-signed-in population §1
     names first), the on-event variant fails with 401 "not authenticated" today
     and after this feature, so AC-3 (on-event), AC-8's Teams and Zoom rows and
     AC-12's "meeting creation creates a meeting" cannot pass for that user
     however refresh is built.
   - When A's Google credential is dead, the request fails at `GetEvent` with
     404 "event not found: ..." (`:271-275`) before Teams or Zoom is reached.
     AC-8 and AC-10 do not say which provider is then reported lost or which
     error the user gets, and a test passes or fails depending on whether the
     fixture also gives A a working Google token.

   *Resolves when* the spec (a) corrects the preamble sentence to say the
   on-event action reads the Google credential as well; (b) states the Google
   precondition (A holds a working Google credential) on every criterion that
   runs the on-event variant, or explicitly scopes that variant out for users
   without one and says whether that belongs to PAC-47; and (c) states the
   observable result, the status surfaces and the audit entry when the Google
   credential is the one refused on that path.

2. **"Visible, once" is not scoped to a loss episode, so the criteria permit
   suppressing a genuine second loss.** §3 says the activity log gets "one
   entry" per loss. AC-9 checks only repeated hits on the *same* dead
   credential, and AC-12 (reconnect) asserts nothing about the audit log. An
   implementation that records at most one connection-lost entry per user and
   provider *ever* passes AC-8, AC-9 and AC-12, yet after lose, reconnect, lose
   again, the user's activity log is silent about the second loss, which is the
   invisible-dead-credential problem the issue exists to fix. No criterion
   covers two concurrent actions both meeting the refusal either (AC-5 covers
   concurrent *success* only), where a naive design writes two entries.
   *Resolves when* a criterion states that a loss after a successful reconnect
   produces a new connection-lost entry (lose, reconnect, lose = two entries),
   and that concurrent refusals of the same credential within one backend
   process produce exactly one.

### Non-blocking

1. **"Reconnect Outlook" is not guaranteed to reach A.** The Microsoft callback
   attaches the credential to the user resolved by the Microsoft account's
   `mail` (`backend/api/handlers_microsoft_auth.go:62-76`; `storage.UpsertUser`
   matches `ON CONFLICT (email)`, `backend/storage/users.go:21-25`) and issues
   a session for that user. §3's "changes only the caller's calendar provider"
   and AC-11, AC-12 and AC-20's "A reconnects" assume it is the signed-in user.
   If the Microsoft `mail` no longer equals A's Paceday email, the reconnect
   AC-8 tells A to do signs the browser into another (possibly new) account and
   leaves A reported lost. The spec should define reconnect for Outlook as a
   sign-in that resolves to A, and state what A sees otherwise.
2. AC-12 says "a provider" while the preamble defines provider as Outlook and
   Zoom unless stated; it then mentions listing events, a Google action. Name
   Google explicitly if AC-12 applies to it.
3. The AC-8 table's precondition "listing A's calendar events (A's calendar
   provider is Google)" suggests the operation is provider-dependent. It is not:
   `listEvents` always uses the Google token
   (`backend/api/handlers_calendar.go:133-162`). Harmless, but AC-21's second
   half relies on exactly this, so say so.
4. AC-20 requires the cache to be bypassed after a reconnect only when the
   degraded result came from a *refused* credential. A disconnect followed by a
   reconnect inside 15 minutes still serves a cached `unknown` (§2.7). Decide
   whether "never outlives a reconnect" covers that case.
5. The `AuditEntry` schema describes every entry as "always about the calling
   user's own calendar" (`contracts/openapi/openapi.yaml:5545`). Connection
   entries are about a credential (Zoom is not a calendar). The contract stage
   will have to reword it; a line in §6 would stop that being read as a
   redefinition of the existing kinds.
6. D-2 says an unconfirmed revocation "is told to the user". Until the web
   stage lands, the existing caller shows `toast.success("Disconnected Zoom")`
   whatever the body says
   (`smart-calendar-flow/src/components/ConferencingSection.tsx:98-99`). §5
   should say the frontend stage owes that message, so the backend PR alone is
   not taken to satisfy D-2.

### Criteria I could not falsify
- AC-3, AC-8, AC-10, AC-12 for the on-event meeting-creation variant: the
  outcome depends on an unstated Google-credential precondition (Blocking 1).
- AC-9 cannot distinguish a per-episode dedup from a permanent one (Blocking 2).

Every other criterion has a test that fails today and passes afterwards
(the round-1 list stands). The new ones:
- AC-20: the per-user, per-attendee, per-day cache
  (`backend/engine/freebusy_service.go:96-100`, `:140-150`) serves `unknown`
  without a request today.
- AC-21: both status surfaces derive "connected" from storage alone today
  (`handlers_auth.go:98-131`, `handlers_conferencing.go:173,179`).
- AC-22: the enum has seven values
  (`contracts/openapi/paths/calendar.yaml:1715-1722`), so a listing with a new
  kind is non-conforming today.

### Citations I checked and found wrong
- §4 preamble, "both read the same stored credential" — the on-event action
  also reads the Google credential first, at `handlers_conferencing.go:266-275`
  and `handlers_events.go:24-32` (Blocking 1).
- §3, "Connecting Outlook changes only the caller's calendar provider" — it
  changes the provider of the user matched by the Microsoft `mail`, who then
  becomes the caller, at `handlers_microsoft_auth.go:66-76` and
  `users.go:21-25` (Non-blocking 1).

Checked and correct: §2.6 (`handlers_auth.go:97-132`;
`handlers_conferencing.go:161-184,173,176,179,181`); §2.7
(`freebusy_service.go:74-155,96-100,122-128,126,129-138,136,140-152`;
`handlers_freebusy.go:72-76`; `manager.go:484-499`, which builds a fresh
service per call and so never shares the cache); §2.4
(`calendar.yaml:1669-1672`; `smart-calendar-flow/src/api/generated/schemas.ts:38`,
`src/api/client.ts:478`, `src/pages/Audit.tsx:97`); §6
(`src/api/client.ts:1463`, `src/components/ConferencingSection.tsx:98`);
inherited scope (`openapi.yaml:371,800,1483,1493-1494,1545,1717,1951,6020-6021`;
`api-audit.md:245,246,262,280,286,295,298,304,311,325,447`). Google sign-in
uses `ApprovalForce` (`backend/auth/google_oauth.go:25`), so leaving Google out
of AC-11 is sound.

### Consumers this breaks
- None beyond those §2.4 and §6 already name. The web consumers of
  `connected: false` on calendar status
  (`smart-calendar-flow/src/pages/Dashboard.tsx:114`,
  `src/pages/Onboarding.tsx:61,84`) already treat it as "connect your
  calendar", not as a logged-out session, so AC-8(b) reaching them is the
  intended effect.

## Revision 2 — 2026-10-26

Answers the two blocking findings of the round-2 challenge and its non-blocking
ones. Re-checked against the backend at `16f3100` (= `origin/main`).

### Blocking 1 — the on-event action also reads the Google credential

Accepted; the sentence was wrong. Re-read: `addEventConference` builds its
client through `loadEventClient` (`backend/api/handlers_conferencing.go:52-55`,
`:267-271`), which is `eventHandlers.calClient` over `auth.LoadUserToken`
(`backend/api/handlers_events.go:24-32`); no Google credential gives 401, a
failed Google read gives 404 "event not found" (`:272-275`), and the link is
written back through Google (`:351-355`). Changes:

- (a) The §4 preamble sentence is corrected: the standalone create reads only
  Teams or Zoom; the on-event action reads Google first and writes back with
  it. New §2.8 records this, and the inherited-scope entry for the on-event
  operation says so.
- (b) The §4 preamble states the Google precondition for every criterion that
  runs the on-event variant, and AC-3, AC-8's Teams and Zoom rows and AC-12
  state it inline. For users without a Google credential the on-event variant
  is scoped out (§5) and assigned to PAC-47; for them "meeting creation" means
  the standalone create.
- (c) When Google is the refused credential on that path: AC-8 gains a Google
  on-event row (result: today's failure, D-10), so AC-8(b)-(d) give the status
  surfaces (Google Meet row, and calendar status when the calendar provider is
  Google) and one Google connection-lost entry. New AC-23 pins that the Teams or
  Zoom credential is never read, its row is unchanged and no Teams or Zoom loss
  is written. AC-10 states the temporary-failure case; AC-12 covers reconnecting
  Google and then creating the meeting.

### Blocking 2 — "visible, once" not tied to one loss

Accepted. §3 now says "once per loss" and that a loss after a reconnect gets its
own entry. The §4 preamble defines a loss episode (first permanent refusal of a
usable credential, until a successful reconnect or a disconnect). AC-9 is
scoped to one loss. New AC-24: lose, reconnect, lose writes exactly two
entries, for each of Outlook, Zoom and Google. New AC-25: concurrent refusals
inside one backend process write exactly one entry; the multi-process case is
added to OQ-9.

### Non-blocking

1. Outlook reconnect resolves by email: §2.1 records it; the preamble defines
   reconnect as a sign-in that resolves to A; OQ-10 holds the mismatch case.
2. AC-12 now names Outlook, Zoom and Google.
3. The listing-events row now says listing always uses the Google credential,
   whatever the calendar provider (§2.8), which AC-21's second half relies on.
4. Disconnect-then-reconnect inside the cache window: D-11 (not covered) and §5.
5. §6 now says the activity-log entry description is widened by the contract
   stage without redefining the seven existing kinds.
6. §5 now says the frontend stage owes the unconfirmed-revocation message.

## Challenge — 2026-10-26 (round 3)

**Verdict**: accept-with-changes

I reviewed the committed spec at `05c1ae5` (Revision 2) against the Linear issue
text, the backend and contract at `16f3100` (still `origin/main`), and
`smart-calendar-flow` at `d9e0786`. Both round-2 blocking findings are resolved:

- §2.8 and the §4 preamble now describe the on-event action correctly. I
  re-read `handlers_conferencing.go:52-55,251-358` and
  `handlers_events.go:24-32`: 401 when there is no Google credential (`:267-270`),
  404 "event not found" when the Google read fails (`:272-275`), Teams or Zoom
  read only at `:337`, and the write-back through Google at `:352`.
- AC-24 and AC-25 tie "once" to a single loss episode.

One new defect remains. It comes from an assumption that runs through every
criterion: that the user whose credential is used is the user acting.

### Blocking

1. **Three existing paths use another user's Google credential. The spec does
   not say whose credential is renewed, whose surfaces change or whose activity
   log gets the loss entry on those paths, so it permits writing B's loss into
   A's activity log.**
   Every criterion has A acting with A's own credential. AC-8(c) reads
   "attributed to A", and §3 says "the user's own activity log". The backend
   also renews Google credentials on behalf of someone else:
   - **Team availability** (`GET /api/teams/{id}/availability`,
     `openapi.yaml:4964`). A team member's request loads and renews *every
     member's* Google credential (`backend/engine/team_availability.go:78-88`,
     `auth.LoadUserToken(e.DB, m.UserID)`).
   - **Public booking slots and booking creation** (`GET /api/book/{slug}/slots`,
     `POST /api/book/{slug}`, both unauthenticated, `routes.go:26-27`). These
     use each host's Google credential (`backend/engine/booking.go:114-124,202-208,252`).
     No user is acting at all.

   AC-6 makes the refreshed token persist, and AC-8 makes a refusal produce a
   status change and an audit entry. Together they turn these reads into writes.
   An implementation that attributes the loss to the request's user passes every
   criterion as written. When B's refresh is refused inside A's
   team-availability request, B's "Google connection lost" entry then lands in
   A's activity log. A learns B's credential state, which widens who sees data,
   and B is never told. An implementation that skips the record because no
   caller exists passes too, leaving B's dead credential invisible: the problem
   the issue exists to fix. A public visitor's request has no caller to attribute
   to in the first place. AC-8(d) ("B's surfaces and audit log are unchanged")
   cannot settle this, because there the credential is B's own.

   *Resolves when* the spec states that renewal write-back, the status change and
   the connection-lost entry always belong to **the credential's owner**,
   whoever or whatever triggered the use (another user, an unauthenticated
   booking visitor, a scheduled job), and that the triggering user's activity log
   and status are untouched. It should add a criterion that pins this. Example:
   A requests team availability, B's Google refresh is refused. B's Google Meet
   row reports not connected, B gets one entry, A's log is unchanged, and the
   availability response is as today. The spec must also add a line on the
   public booking path. That line states what the host's surfaces and log show,
   and that the booking responses do not change. If those responses stay
   unchanged, list the three operations in inherited scope as unchanged. I
   checked: none carries an `x-uncertain`. `POST /api/book/{slug}` carries the
   open findings API-051 and API-073 (`api-audit.md:285,307`), which would be
   conditional.

### Non-blocking

1. §3 says a refused renewal is visible "every place", but the AC-8 table names
   only three Google actions. Other Google consumers renew too, and a refusal on
   them is not required to surface:
   - scheduled or engine paths (`backend/engine/auto_decline.go:77-87`,
     `habits.go:250-254`, `analytics.go:212-216`);
   - manager team detection (`manager.go:191-195`).

   State whether loss recording applies to every consumer of the credential or
   only to the listed actions. If it is only the listed actions, narrow §3 to
   match. Blocking 1's resolution may settle this at the same time.
2. Google reconnect resolves the user by email, exactly as Microsoft's does.
   The Google callback upserts by `info.Email` and stores the token on that
   user (`backend/api/handlers_auth.go:63-70`). The preamble defines
   "reconnect" as "resolves to A" only for Outlook, while AC-12, AC-20 and
   AC-24 also reconnect Google. Extend the definition, and OQ-10, to Google.
3. The AC-8 Google availability row says "A's calendar provider is Google".
   The lookup actually uses the Google credential whenever the provider is not
   Outlook, WebCal included (`freebusy_service.go:122-138`). A WebCal user with
   a refused Google credential is covered by no row. One clause would fix this.

### Criteria I could not falsify
- AC-8 and AC-25 for a credential renewed on another user's behalf (team
  availability, public booking): "attributed to A" cannot tell attribution to
  the owner from attribution to the requester (Blocking 1).

Every other criterion has a test that fails today and passes afterwards (the
round-1 and round-2 lists stand). New in Revision 2:
- AC-23: no on-event test today asserts a Google loss entry, or that Teams and
  Zoom go untouched, when Google is refused.
- AC-24: no connection-lost entry exists today, so neither count holds.
- AC-25: no connection-lost entry exists today.

### Citations I checked and found wrong
- None. Revision 2's new citations are correct:
  - `handlers_conferencing.go:52-55,251-358,267-271,268-270,272-275,337-345,351-355`
  - `handlers_events.go:24-32`
  - `handlers_calendar.go:133-144`: actually `134-145` (function begins at
    `:134`). Immaterial.
  - `handlers_microsoft_auth.go:62-77`
  - `users.go:21-25`
- One claim is incomplete rather than wrong: §2.3 and §6 present the Google
  credential as read only for its owner's actions. That is not so on the paths
  in Blocking 1.

### Consumers this breaks
- None beyond those §2.4 and §6 already name, provided Blocking 1 resolves with
  "responses unchanged" for team availability and booking. The web consumers of
  team availability and the public booking page then see no change.

## Revision 3 — 2026-10-26

Answers the one blocking finding of the round-3 challenge and its three
non-blocking ones. Re-checked against the backend and contract at `16f3100`
(still `origin/main`).

### Blocking 1 — credentials used on another user's behalf

Accepted. Re-read and confirmed: team availability loads and renews every
member's Google credential (`backend/engine/team_availability.go:78-88`); public
booking slots and creation, both unauthenticated (`routes.go:23-27`), use each
host's credential (`backend/engine/booking.go:93,114-124,202-208,252`). I also
looked for any other path using a credential for someone other than its owner:
team analytics reads stored rows only (`team_availability.go:175-176`), manager
analytics queries members with the manager's own credential, and `GET
/api/book/{slug}` only checks a credential row exists
(`handlers_booking.go:88-90`). There are no others. Changes:

- (a) New §2.9 records these three paths, and §2.3 no longer implies the
  Google credential is read only for its owner.
- (b) §3 gains the owner rule: renewal write-back, the status change and the
  connection-lost entry always belong to **the credential's owner**, whoever
  or whatever triggered the use (another user, an unauthenticated visitor, a
  scheduled job). The triggering user's log and status are untouched, and no
  answer tells them more about the owner's connection than today. The §4
  preamble defines "owner"; AC-8(c) now reads "attributed to A as the
  credential's owner". D-12 records the decision.
- (c) New AC-26 pins it for team availability: B's refusal inside A's request
  gives B the not-connected status and one entry. A's log, status and
  credential are unchanged, and the response is today's. Concurrent requests
  by A and C still give B one entry. New AC-27 pins the renewal write-back for
  B only on the same path.
- (d) New AC-28 covers the public booking path. Slots and booking creation
  responses are unchanged. The host's surfaces report not connected and the
  host gets one entry per loss. The renewal is stored for the host only.
- (e) Inherited scope lists the three operations as unchanged. None carries an
  `x-uncertain`. `POST /api/book/{slug}` has API-051 and API-073 as
  conditional.

### Non-blocking

1. Loss recording applies to every consumer that renews the credential (D-13),
   so §3's "every place" stands rather than being narrowed. §2.9 lists the
   owner-only consumers. New AC-29 pins a job with no request from the owner
   (scheduled auto-decline, weekly team detection batch). No proactive refresh is
   added (§5).
2. The §4 preamble now defines a Google reconnect as a Google sign-in that
   resolves to A (`handlers_auth.go:63-70`). OQ-10 is extended to Google.
3. The AC-8 Google availability row now reads "calendar provider is not
   Outlook: Google or WebCal" (`freebusy_service.go:122-138`).

## Challenge — 2026-10-26 (round 4)

**Verdict**: accept-with-changes

Reviewed the committed spec at `fa86222` (Revision 3) against the Linear issue
text, the backend and contract at `16f3100` (still `origin/main`) and the local
`smart-calendar-flow` checkout. The round-3 blocking finding is resolved for the
three paths it named: §2.9, the owner rule in §3, D-12 and AC-26 to AC-28 now
state that renewal, status and loss belong to the credential's owner and that
the requester's log, status and answer are unchanged; AC-29 and D-13 settle
round-3 Non-blocking 1. One defect remains, on the fourth public booking
operation, which §2.9 cites and then sets aside.

### Blocking

1. **Public link info tells anonymous visitors how many hosts have a usable
   calendar token, and the spec neither keeps that count as it is nor lets it
   change on purpose; one reading of §3 requires the change D-12 forbids.**
   `GET /api/book/{slug}` (`getPublicLinkInfo`, `openapi.yaml:1259`, route
   `backend/api/routes.go:25`, no authentication) returns `coverage.checked`,
   counted from whether each host's Google credential loads
   (`backend/api/handlers_booking.go:88-90`). The contract defines it as the
   number of hosts "with a usable OAuth token"
   (`contracts/openapi/paths/booking.yaml:1100-1105`), and the public booking
   page shows it to every visitor
   (`smart-calendar-flow/src/pages/PublicBooking.tsx:378-380`, `CoveragePill`).
   §2.9 says this operation "never uses or renews" the credential, which is
   true, and then drops it: it is not in inherited scope, not in §6, and in no
   criterion. Both of these then pass every criterion:
   - An implementation that, following §3 ("connected means usable") and the
     contract's own word "usable", stops counting a host whose loss is recorded.
     Every anonymous visitor with the link then learns H's Google connection is
     dead, from a page H did not ask to show it on: exactly what §5 refuses
     ("telling a ... booking visitor that a ... host's connection was lost") and
     what D-12 calls widening who sees data. D-12 names only "team availability
     and booking responses" as unchanged, and AC-28 and §6 pin only slots and
     booking creation, so nothing forbids it.
   - An implementation that keeps counting stored rows (D-1 keeps them), after
     which the contract's "usable" describes intended rather than actual
     behaviour, which factory §7.2 forbids.

   *Resolves when* the spec states what `coverage.checked` reports for a host
   whose loss has been recorded (D-12 and §5 point to "unchanged: a lost host
   still counts as checked", recorded as a factory assumption pending owner
   confirmation); lists `GET /api/book/{slug}` in inherited scope as unchanged
   in response (checked: no `x-uncertain`, and no audit finding names it); adds
   it to §6's "responses do not change" bullet with `PublicBooking.tsx` as its
   consumer; requires the contract stage to make the `PublicLinkCoverage`
   description match what is actually counted; and extends AC-28 so that after
   H's loss the link-info response is identical to before. If the spec instead
   chooses that visitors see the loss, it must say why that does not widen who
   sees data, against D-12.

### Non-blocking

1. **§2.9's list of owner-only Google consumers is presented as complete and is
   not.** It omits every engine path that reaches the credential through
   `newCalOps` (`backend/engine/cal_iface.go:53-64`, user from the request
   context): focus time (`focus_time.go:52`), compression (`compression.go:43`),
   smart scheduling (`smart_schedule.go:50`), and the personal-blocker sync,
   which runs every 30 minutes for every user with no request from them
   (`backend/main.go:95`, `engine/personal_blocker.go:51,92`) and renews the
   owner's work Google credential. D-13's rule ("every consumer that renews the
   credential") already covers them, so no criterion is wrong; the list and the
   matching inherited-scope bullet should name them or say they are examples,
   so a plan author does not read the list as the set of paths needing loss
   recording.
2. **Temporary failures on someone else's behalf are not pinned.** §3 says a
   temporary failure is never recorded as a loss, and AC-10 tests that only for
   the AC-8 table. No criterion runs AC-26, AC-28 or AC-29 with a timeout or 5xx
   on the owner's refresh, and those are the paths that swallow every error
   today (`team_availability.go:85-99`, `booking.go:93-96`, `:269-272`), where
   an implementation that treats any swallowed error as a loss would go
   unnoticed. One sentence in AC-26 or AC-28 would close it.

### Criteria I could not falsify
- None. AC-28 as written is falsifiable for slots and booking creation; the gap
  in Blocking 1 is the operation it does not cover. New criteria, each with a
  test that fails today and passes afterwards:
  - AC-26: no connection-lost entry exists today, and none is written to either
    log.
  - AC-27: `TokenSource` at `team_availability.go:84` renews but never writes
    back.
  - AC-28: no entry or status change for H today; the host's renewal is not
    stored (`booking.go:202-208`).
  - AC-29: `auto_decline.go:77-87` and `manager.go:191-195` record nothing.

### Citations I checked and found wrong
- None wrong. Revision 3's new citations are correct:
  `team_availability.go:78-88,85-99,175-176`;
  `booking.go:93,114-124,202-208,252,269-272`; `routes.go:23-27,236`;
  `handlers_booking.go:88-90`; `auto_decline.go:77-87`;
  `main.go:119-124,150-162`; `habits.go:250-254`;
  `handlers_habits.go:139,241,384`; `analytics.go:212-216`;
  `handlers_analytics.go:33,91`; `manager.go:191-195`;
  `handlers_manager.go:80,102`; `openapi.yaml:1299,1359,4964`;
  `api-audit.md:285,307`. The claim that Outlook and Zoom are used only for
  their owner holds: every `LoadMicrosoftToken` call takes the caller's or
  requester's id (`handlers_conferencing.go:40,139,169`,
  `freebusy_service.go:123`, `handlers_auth.go:99`), and Zoom is read only from
  the caller's settings (`conference/factory.go:14-21`).
- Incomplete rather than wrong: §2.9's "`GET /api/book/{slug}` ... never uses
  or renews it" omits that the operation reports credential usability to the
  public (Blocking 1); the owner-only list is incomplete (Non-blocking 1).

### Consumers this breaks
- `smart-calendar-flow/src/pages/PublicBooking.tsx:378-380` and
  `src/api/schedulingLinks.ts:61` (`coverage.checked` on the public booking
  page): breaks, or starts revealing a host's loss to visitors, if Blocking 1 is
  resolved by changing what `checked` counts; unaffected if it is resolved as
  unchanged.

## Revision 4 — 2026-10-27

Answers the one blocking finding of the round-4 challenge and its two
non-blocking ones. The branch was brought up to date by merging `origin/main`
(`6d751f9`, eleven commits after `16f3100`), and the spec was re-checked
against it, because PAC-21, PAC-26 and PAC-35 changed several operations this
spec cites.

### Blocking 1 — public link info reports host credential state to visitors

Accepted. Re-read at `6d751f9`: `getLinkInfo` (`backend/api/handlers_booking.go:106-121`)
answers `publicLink`, which counts a host as checked when
`auth.LoadUserToken` returns a stored row (`:87-90`), whether or not the
credential works; the contract says "usable OAuth token"
(`contracts/openapi/paths/booking.yaml:1215-1220`); the public booking page
renders it through `CoveragePill` (`smart-calendar-flow/src/pages/PublicBooking.tsx:378-380`,
checked on the web repo's `origin/main` at `ad52160`). Changes:

- §2.9 now lists public link info as a fourth path that reports on a host's
  credential to anonymous visitors, with what the page shows and the gap
  between the contract's "usable" and what is counted.
- New **D-14**: unchanged. A host whose loss is recorded still counts as
  checked. Option (a), stop counting a lost host, was rejected because it tells
  every visitor that some host's connection is dead, which widens who sees
  data (D-12, §5). The cost of the chosen option is written into D-14.
- Inherited scope lists `GET /api/book/{slug}` as unchanged in response.
  Checked: no `x-uncertain`, and no audit finding names it.
- §6's "responses do not change" bullet now covers link info and names its
  consumer. A new §6 bullet requires the contract stage to rewrite the
  `PublicLinkCoverage` description to say what is counted (factory §7.2),
  changing no field, type or value.
- AC-28 is extended: after H's loss is recorded, the link-info response is
  identical to the one before, and H still counts as checked. It fails against
  an implementation that stops counting a lost host.
- §3's owner rule and §5 now say the public booking page reveals nothing about
  a host's loss, and that making the coverage count truthful is out of scope.

### Non-blocking

1. §2.9's owner-only list is now introduced as examples found at `6d751f9`,
   with D-13 as the rule. It adds the paths that load the credential of the
   user in the request context (`backend/engine/cal_iface.go:84-96`: focus
   time, compression, smart scheduling), the personal-blocker sync every 30
   minutes (`backend/main.go:92-114`, `engine/personal_blocker.go:92`), the
   automatic focus-time scheduler added by PAC-49 (`backend/scheduler/cron.go:51-53`,
   `backend/main.go:86`), and the API handlers that build a Google client for
   the caller. AC-29 now includes the personal-blocker sync. The inherited-scope
   bullet for owner-only consumers points at §2.9 rather than repeating a list.
2. AC-26, AC-28 and AC-29 each gain a temporary-failure case (timeout or 5xx):
   responses as specified, the owner still reported connected, and no
   connection-lost entry. AC-26 now also fails against an implementation that
   records every swallowed error as a loss.

### Changes from re-checking against `origin/main` (`6d751f9`)

These are facts that changed on `main` after round 4 reviewed `16f3100`. None
changes a criterion's intent.

- **Calendar disconnect (PAC-35).** It keeps the session (API-061 Fixed). For
  a user whose calendar provider is Outlook or WebCal it answers 409 and
  deletes nothing (PAC-35 D-8). §2.1, §2.3, §5 and inherited scope are
  updated. OQ-3 now records that PAC-35 OQ-5 left Outlook/WebCal disconnect to
  its own issue, and that routing Outlook disconnect through calendar
  disconnect would change PAC-35's 409. AC-18 gains the precondition that A's
  calendar provider is Google, since otherwise A's disconnect deletes nothing.
- **Fixed findings (PAC-35, PAC-26).** API-011, API-012, API-061 and API-064
  are Fixed (PAC-35), and API-028 is Fixed (PAC-26). Inherited scope no longer
  lists them as conditional. All inherited-scope line numbers are re-derived
  at `6d751f9`.
- **U-03 (PAC-21).** It was resolved as "keep" and is gone from the bundle.
  API-091 stays open on `GET /api/calendar/freebusy`.
- **On-event meeting creation (PAC-21).** With no Google credential it now
  answers the 401 calendar-not-connected refusal. A failed Google read answers
  404 "event not found" with no error text after it. §2.8 and D-10 are
  updated; AC-8's Google on-event row still says "today's response", which
  this now is.
- **T8 (PAC-21).** The raw provider-error echoes earlier revisions cited in
  the Zoom and Microsoft callbacks are gone. The inherited-scope note is
  updated.
- **Test seam (PAC-35).** The Microsoft OAuth configuration accepts an
  endpoint override for a local fake provider (§2.5).
- §2.6 status-surface citations, §2.2's Zoom status and disconnect citations,
  and §2.9's citations are re-numbered at `6d751f9`. The §2 preamble says
  which subsections were re-checked. Citations in §2.1, §2.4 and §2.7 whose
  code did not change were not all re-numbered, and the preamble says so.
