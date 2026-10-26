# Spec — PAC-67: Per-user Outlook and Zoom credentials have no refresh or revocation lifecycle

- **Linear**: https://linear.app/paceday/issue/PAC-67
- **Status**: draft
- **Author**: spec-author
- **Origin**: PR Enach/clockwise-like#204 (PAC-24) merge condition 3,
  `calendarCredentialLifecycle`; PAC-24 spec §0 and OQ-6
  (`docs/specs/PAC-24.md:55-61`, `:785-795`). Related: PAC-48 (origin), PAC-23
  (audit `user_id`), PAC-47 (work-calendar provider setting ignored).
- **Inherited scope**:
  - No `x-uncertain` marker sits on any operation this spec is expected to
    change: auth status, calendar disconnect, the Microsoft and Zoom sign-in and
    callback operations, conferencing providers, conference create and Zoom
    disconnect (`contracts/openapi/openapi.yaml:800-830`, `:934-1230`,
    `:1545-1630`) carry none. The only nearby marker is on the
    `ConferenceMeetingDetails` schema (U-12,
    `contracts/openapi/openapi.yaml:6020-6021`). This spec does not change that
    schema; if the contract stage edits it for any reason, U-12 comes into scope
    under factory §7.1.
  - **API-061** (`docs/factory/api-audit.md:295`, calendar disconnect also logs
    the user out) is open on the calendar-disconnect operation. It comes into
    scope **only if** the contract stage changes that operation (for example to
    carry Outlook disconnect). See OQ-3.
  - **API-012** (`docs/factory/api-audit.md:246`, Microsoft callback redirects as
    success after a profile/upsert failure) and **API-011** (`:245`) are open on
    the Microsoft callback. AC-11 changes what that callback stores; if the
    contract stage edits that operation, API-012 and API-011 come into scope. See
    OQ-4.
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
- Outlook availability failures are swallowed (`freebusy_service.go:126`).
  Teams and Zoom failures surface as a generic error on the action
  (`handlers_conferencing.go:152-155`, `:342-345`).

### 2.5 Test seams

The e2e stack has a stand-in for Google only (`e2e/fake-google/`); Microsoft and
Zoom hosts are routed to a refusing sink (`e2e/fake.ts:31-35`,
`e2e/SEAM-REQUIRED.md`). Criteria needing a Microsoft or Zoom exchange are
therefore marked `[unit]` or `[contract]` below, not `[e2e]`.

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
  (revoked, expired refresh credential, consent withdrawn), the user's connection
  status shows that provider as not connected, and the user's own activity log
  gets one entry saying so — not one per failed request. A temporary failure
  (network, provider outage) does not mark the connection dead and is not
  recorded as a loss.
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

**Refresh** (ticket acceptance 1)

AC-1. Given user A has an expired Outlook access token and a valid refresh
token, when an availability lookup that reads A's Outlook calendar runs for A,
then the double receives exactly one refresh request carrying A's refresh token,
the calendar call is made with the new access token, and A's stored credential
afterwards holds the new access token and expiry. Fails today: no refresh
request is sent.  [unit]

AC-2. Given A and B both have Outlook credentials and A's is expired, when the
action in AC-1 runs for A, then B's stored credential is identical before and
after, and no request carrying B's tokens reaches the double.  [unit]

AC-3. Given A has an expired Zoom access token and a valid refresh token, when A
creates a Zoom meeting, then the meeting is created with a refreshed access token
and A's stored Zoom credential holds the new access token. Fails today: the
double's 401 is returned.  [unit]

AC-4. Given the double returns a **new** refresh token on refresh (rotation),
when AC-1 or AC-3 runs, then the stored refresh token is the new one, and a
second action after the new access token also expires succeeds using it.  [unit]

AC-5. Given A's Outlook (separately Zoom) access token is expired and the double
rotates refresh tokens and rejects any refresh token already used, when two
actions for A run concurrently, then both succeed and A's stored credential
afterwards is usable for a third action.  [unit]

AC-6. Given A's Google access token is expired, when a Google calendar action
runs for A, then the refreshed access token and expiry are stored for A only, and
a second action within the new token's lifetime sends no refresh request. Fails
today: every action after expiry refreshes again.  [unit]

AC-7. Given a stored access token that has not expired, when an action runs,
then no refresh request is sent.  [unit]

**Failure surfacing**

AC-8. Given the double answers A's refresh request with a permanent refusal
(OAuth `invalid_grant`), when an Outlook, Zoom or Google action runs for A, then
the action fails with an error telling A to reconnect that provider; A's
connection status reports that provider not connected; and A's audit log holds
exactly one entry, attributed to A, naming the provider and that the connection
was lost. B's status and audit log are unchanged. Fails today: status still
reports connected and no audit entry is written.  [contract]

AC-9. Given the state after AC-8, when three more actions for A hit the same dead
credential, then A's audit log still holds exactly one connection-lost entry for
that provider.  [unit]

AC-10. Given the double answers A's refresh with a temporary failure (timeout or
5xx), when an action runs, then the action fails, A's status still reports
connected, and no connection-lost audit entry is written.  [unit]

AC-11. Given A has a working stored Outlook (separately Zoom) credential, when A
reconnects and the exchange returns an access token but no refresh token, then
A's stored refresh token is unchanged and A remains connected.  [unit]

AC-12. Given A's connection was reported lost (AC-8), when A reconnects
successfully, then A's status reports connected again and actions succeed.  [unit]

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

## Decisions

*None of these has a recorded owner answer. Each is the most conservative option
consistent with PRODUCT.md ("Privacy is not a setting to trade away",
`smart-calendar-flow/PRODUCT.md:90`) and earlier owner decisions (PAC-24
Decisions, `docs/specs/PAC-24.md:986`: credentials belong to one user and are
never shared).*

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
