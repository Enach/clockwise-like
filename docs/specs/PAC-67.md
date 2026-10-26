# Spec — PAC-67: Per-user Outlook and Zoom credentials have no refresh or revocation lifecycle

- **Linear**: https://linear.app/paceday/issue/PAC-67
- **Status**: challenged (round 1 answered in "Revision 1")
- **Author**: spec-author
- **Origin**: PR Enach/clockwise-like#204 (PAC-24) merge condition 3,
  `calendarCredentialLifecycle`; PAC-24 spec §0 and OQ-6
  (`docs/specs/PAC-24.md:55-61`, `:785-795`). Related: PAC-48 (origin), PAC-23
  (audit `user_id`), PAC-47 (work-calendar provider setting ignored).
- **Inherited scope**: every operation this feature touches or may touch, with
  the `x-uncertain` markers and open audit findings on it. "Changed" means this
  spec's criteria require a contract change there; "conditional" means the
  marker or finding comes into scope **only if** the contract stage edits that
  operation, under factory §7.1. Line numbers are in
  `contracts/openapi/openapi.yaml` and `docs/factory/api-audit.md`.
  - **Auth status** (`GET /api/auth/status`, calendar connection status §2.6).
    No `x-uncertain`; API-009 is Fixed (PAC-24). Nothing inherited.
  - **Calendar disconnect** (`DELETE /api/auth/disconnect`, `openapi.yaml:800`). **API-061**
    (`api-audit.md:295`, disconnect also logs the user out) is open:
    conditional. See OQ-3.
  - **Microsoft sign-in and callback** (`GET /api/auth/microsoft`,
    `GET /api/auth/microsoft/callback`). **API-012** (`:246`) and **API-011**
    (`:245`) are open on the callback; **API-064** (`:298`, `login_hint`
    ignored) is open on the sign-in start. All conditional. AC-11 changes what
    the callback stores; see OQ-4.
  - **Zoom connect and callback.** No marker, no finding. Nothing inherited.
  - **Zoom disconnect** (`POST /api/conference/zoom/disconnect`). No marker, no
    finding. AC-15 likely changes its response; nothing inherited either way.
  - **Conferencing provider list** (`GET /api/conference/providers`, §2.6).
    Changed in behaviour by AC-8, AC-12, AC-13 and AC-21. **API-070**
    (`:304`, 1-4 entries depending on configuration, `custom` always
    connected): conditional.
  - **Standalone conference create** (`POST /api/conference/create`,
    `openapi.yaml:1545`). Reached by AC-3, AC-5 and AC-8. **API-052** (`:286`,
    no validation) and **API-091** (`:325`, no frontend consumer): conditional.
  - **Conference on an event** (`POST /api/events/{id}/conference`,
    `openapi.yaml:1717`). Also creates Teams and Zoom meetings from the stored
    credential (`backend/api/handlers_conferencing.go:33-48`, `:337-345`), so
    AC-3, AC-5 and AC-8 reach it. Checked: no `x-uncertain` on it and no audit
    finding names it. Nothing inherited.
  - **Activity log** (`GET /api/audit`, `openapi.yaml:371`). **Changed**: AC-8,
    AC-15 and AC-16 add entry kinds to a closed set (§2.4, AC-22). U-02, API-005
    and API-074 are all resolved; nothing further inherited.
  - **Availability lookup** (`POST /api/freebusy`, `openapi.yaml:1951`). Its
    response does **not** change under this spec (AC-8, D-6). Open findings
    **API-028** (`:262`), **API-046** (`:280`) and **API-077** (`:311`):
    conditional.
  - **Google-backed operations** whose failure response would change if the
    contract stage chose to change it, notably **`GET /api/calendar/freebusy`**
    (`openapi.yaml:1483`), which carries **U-03** (`:1493-1494`,
    `api-audit.md:447`) and **API-091**. This spec requires no change to any
    Google operation's failure response (D-7), so these are conditional.
  - **`ConferenceMeetingDetails`** schema carries **U-12**
    (`openapi.yaml:6020-6021`). Not changed here; conditional.
  - **T8** (provider error text echoed to the client) is a theme, not a finding
    on these operations; the Zoom and Microsoft callbacks echo `err.Error()`
    today (`backend/api/handlers_microsoft_auth.go:56,73`,
    `backend/api/handlers_conferencing.go:109,113`). Not resolved here (§5).

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
- **No disconnect.** No route clears the Outlook credential. Calendar disconnect
  deletes only the Google token row and expires the session cookie
  (`backend/api/handlers_auth.go:135-144`, route `backend/api/routes.go:48`).
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
  `handlers_conferencing.go:186-192`; route `routes.go:127`). Nothing is sent to
  Zoom. Isolation is proven by `TestZoomTokens_ArePerUser`
  (`backend/storage/settings_test.go:154-180`).
- **Status.** Zoom is reported connected whenever the stored string is non-empty
  (`handlers_conferencing.go:176`).

### 2.3 Google credential (the issue's "confirm" item)

- **Per-user.** One row per user in the OAuth token table
  (`backend/auth/token_store.go:16-30`); load and delete refuse a nil user
  (`:40-59`). Disconnect deletes only the caller's row
  (`handlers_auth.go:136-137`). The disconnect test
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
  (`handlers_auth.go:113-131`), whether or not the refresh token still works.
- **No revocation.** Disconnect deletes the row and sends nothing to Google.

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
therefore marked `[unit]` or `[contract]` below, not `[e2e]`.

### 2.6 Status surfaces today

Two surfaces tell a user whether a provider is connected. These are what §3-§4
mean by "status surfaces".

- **Calendar connection status** (`GET /api/auth/status`,
  `backend/api/handlers_auth.go:97-132`). Reports **only** the user's current
  calendar provider: Outlook (connected when a refresh token is stored,
  `:98-105`), WebCal (`:106-111`) or Google (connected when a token row exists,
  `:113-131`). It never mentions Zoom, and it says nothing about a Microsoft
  credential when the calendar provider is not Outlook, nor about a Google
  credential when it is not Google.
- **Conferencing provider list** (`GET /api/conference/providers`,
  `backend/api/handlers_conferencing.go:161-184`). Rows, each present only when
  the provider is configured on the server: `google_meet` connected whenever a
  Google token row exists (`:173`); `zoom` connected whenever the stored Zoom
  string is non-empty (`:176`); `teams` connected **and** enabled whenever a
  Microsoft credential exists (`:179`); `custom` always connected (`:181`).

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

## 3. Desired behaviour

From the user's point of view:

- **Connected means usable.** While a user's Outlook, Zoom or Google connection
  is reported as connected, Paceday can act with it. Access tokens that expire are
  renewed transparently with the user's own refresh credential; nobody has to
  sign in again because an hour passed.
- **Renewal is private to the user.** A renewed credential is stored for the user
  it belongs to and nobody else. One user's credential expiring, renewing or dying
  has no effect on any other user's connection or calendar reads.
- **Renewal survives rotation and concurrency.** Where the provider issues a new
  refresh credential on renewal, the new one is kept. Two simultaneous actions by
  the same user do not leave the stored credential unusable.
- **A dead credential is visible, once.** When renewal is refused by the provider
  (revoked, expired refresh credential, consent withdrawn), every place that
  tells the user whether that provider is connected (§2.6) shows it as not
  connected — including when it is not the user's calendar provider — and the
  user's own activity log gets one entry saying so, not one per failed request. A temporary failure
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
creating a meeting on an existing event and the standalone create; both read
the same stored credential (§2.1, §2.2, inherited scope).

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
creates a Zoom meeting (on an event, and separately standalone), then the meeting is created with a refreshed access token
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
(c) A's audit log holds exactly one new entry, attributed to A, naming the
provider and that the connection was lost;
(d) B's status surfaces and audit log are unchanged.
Fails today: no row satisfies (b) or (c).  [contract]

| Provider | Action (precondition) | Observable result |
|---|---|---|
| Outlook | availability lookup (A's calendar provider is Outlook) | succeeds as today: each attendee that would have been queried has unknown coverage and no busy periods (D-6) |
| Outlook | Teams meeting creation | fails with an error telling A to reconnect Outlook |
| Zoom | Zoom meeting creation | fails with an error telling A to reconnect Zoom |
| Google | availability lookup (A's calendar provider is Google) | succeeds as today, as for Outlook (D-6) |
| Google | listing A's calendar events (A's calendar provider is Google) | fails with the same status and error shape that operation returns for a failed Google call today (D-7) |

AC-9. Given the state after AC-8, when three more actions for A hit the same dead
credential, then A's audit log still holds exactly one connection-lost entry for
that provider.  [unit]

AC-10. Given the double answers A's refresh with a temporary failure (timeout or
5xx), when each action in the AC-8 table runs for A, then: availability lookups
succeed with unknown coverage as today; meeting creation fails with an error
that does **not** tell A to reconnect; listing events fails as it does today;
every status surface for that provider still reports connected; and no
connection-lost audit entry is written.  [unit]

AC-11. Given A has a working stored Outlook (separately Zoom) credential, when A
reconnects and the exchange returns an access token but no refresh token, then
A's stored refresh token is unchanged and A remains connected.  [unit]

AC-12. Given A's connection to a provider was reported lost (AC-8), when A
reconnects that provider successfully, then every status surface that AC-8 set
to not connected reports connected again, and the AC-8 table's actions for that
provider succeed with the new credential (meeting creation creates a meeting;
listing events returns events).  [unit]

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

AC-18. Given A and B have Google credentials, when A disconnects the calendar,
then B's Google credential is still stored. (Already true per §2.3; pins it,
since no test asserts it today.)  [unit]

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
- **Revoking Google on calendar disconnect, and API-061** (calendar disconnect
  logs the user out): changing what calendar disconnect does to the session is a
  product decision outside this issue. Not touched unless OQ-3 routes Outlook
  disconnect through that operation.
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
  controls; the web repo implements against the contract after stage 2.

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
  operation next to Zoom's, or a change to calendar disconnect. The latter pulls
  API-061 into scope.
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
  criterion.

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
