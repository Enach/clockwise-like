# Contract — PAC-67: refresh, loss and disconnect for per-user Outlook, Zoom and Google credentials

- **Spec**: `docs/specs/PAC-67.md`, accepted at challenge round 5 (`fc27821` on
  `factory/pac-67-spec`; not yet on `main` when this contract was written).
- **Manifest**: `contracts/features/PAC-67.yaml`.
- **Bundle**: revision 1, sha256 in the manifest.
- **Base**: `origin/main` at `6d751f9`.

Most of this feature is behaviour behind unchanged responses: renewal, storing
the renewed token for its owner, recording a loss, and invalidating degraded
availability results. The HTTP boundary changes in only five ways, and the rest
of this document explains why each one is shaped the way it is.

## 1. What changes at the HTTP boundary

| Change | Where | Spec |
|---|---|---|
| New `POST /api/auth/microsoft/disconnect` (`disconnectMicrosoft`) answering `ProviderDisconnectResult` | auth fragment | AC-13, AC-16, AC-17, D-3, D-5, D-8, OQ-3 |
| `disconnectZoom` 204 (empty) becomes 200 `ProviderDisconnectResult`, with revocation at Zoom | booking fragment | AC-14 to AC-16, D-2, D-9 |
| Two new fixed 400 sentences on both meeting-creation operations: "teams: connection lost — reconnect at /api/auth/microsoft", "zoom: connection lost — reconnect at /api/auth/zoom" | booking fragment | AC-8, AC-10, AC-23 |
| `AuditEntry.action` grows from 7 to 10 kinds: `connection_lost`, `provider_disconnected`, `revocation_unconfirmed` | calendar fragment | AC-22, PAC-23 AC-14 |
| `createConferenceLink` validates its body (API-052), and `ConferenceMeetingDetails` is corrected (U-12) | booking fragment | factory section 7.1 |

Description-only changes say what "connected" means after this feature
(`getAuthStatus`, `listConferenceProviders`, `AuthStatusResponse.connected`,
`ConferenceProviderStatus.connected`/`enabled`), what the callbacks store
(AC-11, AC-12), what `PublicLinkCoverage` actually counts (spec section 6, D-14),
and who can be the subject of an activity entry.

Operations deliberately **not** edited: `POST /api/freebusy`,
`GET /api/teams/{id}/availability`, `GET /api/book/{slug}`,
`GET /api/book/{slug}/slots`, `POST /api/book/{slug}`, and every Google
operation's failure response. The spec pins all of them as unchanged (D-6, D-7,
D-12, D-14). Leaving them alone also keeps API-046, API-077, API-051, API-073 and
API-091 on those operations out of scope (factory section 7.1 applies only to an
operation that is edited). `PublicLinkCoverage` is a schema, not an operation,
and its edit is description-only, which spec section 6 requires.

## 2. Outlook disconnect is its own operation (OQ-3)

**Chosen**: `POST /api/auth/microsoft/disconnect`, protected, no body. It
answers 200 with `ProviderDisconnectResult`, 401 or 500, and never 409.

**Rejected: routing it through `DELETE /api/auth/disconnect`.** That operation
is "disconnect my calendar". For an Outlook or WebCal user PAC-35 made it
answer 409 `CalendarDisconnectUnsupported` and delete nothing, and the web app
already reads that 409 as "not available yet" (PAC-35 AC-17). Changing it would
reopen a settled contract. It would also tie removal of the Microsoft credential
to the calendar provider. A user whose calendar is Google but who holds a
Microsoft credential for Teams (AC-21) could then never remove it. The
Microsoft credential serves two features (Outlook calendar, Teams), so its
removal belongs to the credential and not to the calendar.

**Rejected: `POST /api/conference/teams/disconnect`, mirroring Zoom.** It would
suggest that only Teams is affected, but the same credential is the Outlook
calendar connection.

**Why POST and not DELETE**: it mirrors `disconnectZoom`, the only other
per-provider disconnect, so the web client handles both the same way. The
operation is also not a pure delete: it can change the conferencing provider
(D-8) and writes an activity entry.

**Why no 409**: the operation applies to every caller. A caller with nothing
stored gets `revocation: nothing_stored`. That makes the operation idempotent,
so a double click or a retry after a lost response does no harm.

## 3. Revocation at the provider (OQ-1, OQ-2)

**OQ-1, Microsoft: no single-grant revocation Paceday can call. AC-17 takes
its "none" branch.** Microsoft Graph's
`DELETE /oauth2PermissionGrants/{id}` revokes a delegated grant. Its least
privileged delegated permission is `DelegatedPermissionGrant.ReadWrite.All`.
For work or school accounts the signed-in user must also hold an administrator
role (Application Administrator, Cloud Application Administrator and others).
For personal Microsoft accounts it is "Not supported" (Microsoft Learn, "Delete
oAuth2PermissionGrant", graph-rest-1.0, checked 2026-10-27). Paceday requests
neither that scope nor such a role, and asking every user for an admin-only
directory write scope so they can disconnect would be far worse than the
problem. The remaining per-user call, `revokeSignInSessions`, ends all of the
user's sessions in every application, which D-5 forbids. So nothing is sent,
`revocation` is `not_offered`, and the client tells the user they can remove
the grant from their Microsoft account. The contract names no URL for that,
because the right page differs between personal and work accounts, and copy and
placement belong to the frontend stage (spec section 5).

**OQ-2, Zoom: `POST https://zoom.us/oauth/revoke`, resolved in part.** Zoom's
OAuth documentation (developers.zoom.us, "Using OAuth 2.0", checked 2026-10-27)
documents this endpoint as follows:

- authentication is Basic with the client id and secret;
- the `token` parameter is the access token;
- success is `{"status":"success"}`.

The same page says that refresh tokens rotate on every refresh and expire after
90 days, and that access tokens last one hour. AC-4 and AC-5 are therefore live
risks and not only guards. The page does **not** say whether revoking the
access token also removes the app's authorization for the user. Deauthorization
is described separately, as something the user does. The contract therefore
defines `confirmed` narrowly, as "Zoom answered success to the revocation
request". Whether that removes the grant is a follow-up for the plan, to be
checked against a Zoom development app and never production
(manifest `followUps.zoomDeauthorizationEvidence`). This is recorded in the
manifest as assumed by the factory, pending owner confirmation.

**Rejected: `revocation` as a boolean.** That would merge three different cases
into `false`: "we tried and could not confirm" (the user should check Zoom), "the
provider offers nothing" (the user should go to their account) and "there was
nothing to revoke" (nothing to do). The client must word each one differently.
D-2 forbids silence about a failed revocation, and a boolean would make it easy
to show the same message for all three.

## 4. The reconnect error is a 400 with a fixed sentence

**Chosen**: on a permanent refusal (`invalid_grant`) the meeting-creation
operations answer 400 in the existing `BookingError` envelope, with
"teams: connection lost — reconnect at /api/auth/microsoft" or
"zoom: connection lost — reconnect at /api/auth/zoom". Both sentences use the
same em dash as the three sentences already listed. Every refusal to create a
meeting because of the caller's own connection is already a 400 on these
operations ("not connected — visit …", "stored credential is invalid —
reconnect at …"). A lost connection is the same kind of answer: the caller must
act, and retrying will not help. A temporary failure keeps today's 500
(standalone) or 502 "upstream provider error" (on-event), which never mention
reconnecting (AC-10).

**Rejected: 401.** On these operations 401 means "no session" (or "no Google
calendar" on the on-event path, `CalendarNotConnected`). Clients send people to
sign in on a 401, which is wrong here because the Paceday session is fine.

**Rejected: 409 or 424 with a new error schema carrying `provider` and
`reconnect_url`.** It would be machine-readable, but it would be a second
envelope for one kind of refusal while the three sibling refusals stay 400
sentences. A client would then need two code paths for "your Zoom connection
is unusable". If the frontend ever needs structure, adding it to all four
sentences together is one non-breaking change.

**Google on the on-event path stays a 404** (D-7, D-10). It is misleading, and
the spec states that cost. It is unchanged because changing a Google failure
response is outside this feature. The loss still reaches the user through
Google's status and the activity log (AC-23).

## 5. Status surfaces: no new field, `connected` now means usable

**Chosen**: `getAuthStatus` and `listConferenceProviders` keep their shapes.
`connected` changes meaning from "stored" to "stored and not recorded as lost".
On the `teams` row, `enabled` follows `connected`, so no field of a lost row
says Teams is usable (spec section 4 preamble).

**Rejected: a `state: connected | not_connected | lost` field, or
`reconnect_required: boolean`.** A field would let the UI say "reconnect" rather
than "connect". Against it:

- (a) the spec's acceptance only requires "not connected";
- (b) the activity log already tells the user why, once per loss;
- (c) an added field on two surfaces is a contract the web must start honouring
  in two places at once, while the behaviour it describes is still unbuilt.

Adding it later is non-breaking. Adding it now and having to remove it would
not be. The spec leaves the call to the contract (section 6, "whether fields
are added is the contract's call").

## 6. Disconnect: order, entries and failure

**Order: local removal first, then revocation.** The server loads the
credential, removes it locally (clearing any recorded loss and resetting
conferencing), and only then sends the revocation with the in-memory token. A
500 therefore means nothing changed and nothing was sent, so the client can
retry safely. The reverse order would allow "revoked at Zoom, but still stored
and still shown connected here", which is a state the user cannot reason about.
D-2 also requires that the user's instruction is honoured locally whatever the
provider says.

**Entries, answering round-5 challenge NB-2:**

| Case | `revocation` | Entries |
|---|---|---|
| Zoom, revocation confirmed | `confirmed` | 1 `provider_disconnected` |
| Zoom, revocation not confirmed | `unconfirmed` | 1 `provider_disconnected` and 1 `revocation_unconfirmed` |
| Outlook, credential removed | `not_offered` | 1 `provider_disconnected` |
| Either, nothing stored | `nothing_stored` | none |

AC-16's "one entry recording the disconnect" is the `provider_disconnected`
entry in every row. AC-15's "records the disconnect and the unconfirmed
revocation" is the two entries in the second row. Outlook writes no
`revocation_unconfirmed` because nothing was attempted. A disconnect never
writes `connection_lost`: it ends a loss rather than starting one.

The "nothing stored, no entry" row is a factory assumption pending owner
confirmation (manifest `assumedByTheFactory`). Writing an entry for a no-op
would put an event in the user's log that did not happen to their data.

**Rejected: one entry per disconnect with the outcome in `details` only.** AC-22
requires a separate kind for "revocation not confirmed". A kind can also be
filtered, while text inside `details` cannot.

## 7. Activity-log kinds

The names follow the enum's convention (lower case, underscores, past tense):
`connection_lost`, `provider_disconnected`, `revocation_unconfirmed`. `details`
for each is a JSON object written with `encoding/json`, holding only values the
server chooses:

| Kind | `details` |
|---|---|
| `connection_lost` | `{"provider": "outlook" \| "zoom" \| "google"}` |
| `provider_disconnected` | `{"provider": "outlook" \| "zoom", "revocation": ...}` |
| `revocation_unconfirmed` | `{"provider": "zoom"}` |

These details never contain token material or any text the actor supplied.

The new kinds create the first entries whose actor is not their subject. A
teammate's team-availability request, or an anonymous booking visitor, can
cause a `connection_lost` entry in the credential owner's log (D-12, D-13). The
operation description had said every returnable entry has actor = subject. It
now limits that statement to the seven PAC-23 writers and adds a CONNECTION
ENTRIES paragraph. That paragraph says the entry goes to the owner only, names
no requester, and satisfies PAC-23's rule that a writer whose actor is not the
subject never puts actor text in `details`. The paragraph about the log not
holding configuration events is reworded for the same reason. None of the
seven existing kinds is renamed, removed or redefined (spec section 6, PAC-23
AC-14).

**Rejected: provider-specific kinds (`outlook_connection_lost`, …).** That
would mean seven new kinds instead of three, and the set would grow with every
provider. The provider belongs in `details`.

## 8. Inherited findings and uncertainties (factory section 7.1)

The operations this feature edits, and what that brings into scope:

- **`createConferenceLink`: API-052 resolved.** The body is now validated:
  - `title` must be present and non-empty after trimming;
  - `start` and `end` must be present;
  - `end` must be after `start`.

  Anything else is the existing 400 "invalid body", returned before any
  credential is read. The request schema says the same thing, with `required`
  and `minLength`, so a generated client cannot express the wrong request. No
  consumer breaks: the web app and the MCP server never call this operation,
  and the one Go test that does sends a valid body. The `end`-after-`start`
  rule goes beyond the finding's literal "missing fields". It was taken because
  an inverted window is the same defect, a nonsensical meeting sent to the
  provider, and the challenger may push back on it.
- **`createConferenceLink`: API-091 resolved for this operation.** The
  operation stays. The finding's concern is that nobody checks its shape, and
  that is answered by the PAC-67 Go contract tests that go through it (AC-3,
  AC-5, AC-8).
- **`ConferenceMeetingDetails` (the 200 of `createConferenceLink`): U-12
  resolved.** `provider` is closed to `meet | zoom | teams`, read from the three
  implementations in `backend/conference/`. Reading them also showed that
  `MeetProvider` answers an empty `joinUrl`, so `format: uri` was false for
  every Meet response and is removed (factory section 7.2). The marker is gone.
  The bundle's `x-uncertain` count goes from 12 to 11.
- **`listConferenceProviders`: API-070 resolved on the contract side.** The 1–4
  length and the always-connected `custom` row were already documented, and
  they match `providers` in `handlers_conferencing.go`. They are intended. What
  is left of the finding is a test that indexes by position, which the backend
  stage rewrites (CONTRACT-4).
- **`disconnectZoom`, `addEventConference`, `getAuthStatus`, the three
  callbacks, `disconnectCalendar`, `listAuditEntries`:** no `x-uncertain`, and
  no open finding. API-009, API-011, API-012, API-061, API-064, U-02, API-005 and
  API-074 are all already Fixed. Nothing is inherited from them.

## 9. Consumers

The full list is in the manifest. In summary:

- **Web, Zoom disconnect.** `zoomDisconnect` calls `requestApi<void>`. That
  function returns `undefined` on 204, and on a 200 `application/json` it parses
  the body, which the caller ignores. It keeps working. The frontend stage owes
  the "revocation could not be confirmed" message and an Outlook disconnect
  control.
- **Web, generated types.** The AuditEntry zod enum widens. It is not applied
  to the audit path today, and `Audit.tsx` renders `action` raw, so the new
  kinds show as their identifiers until they get copy.
- **Public booking page.** Unchanged (D-14).
- **MCP.** Only reads `getAuthStatus`, whose shape is unchanged.

## 10. Gates run at this stage

Run on the final commit of `factory/pac-67-contract`:

- `python scripts/openapi_assemble.py` assembled the fragments: 99 paths, 120
  operations, 145 schemas, x-uncertain 11.
- In an LF clone of the branch (`golang:1.25-bookworm`, the same commands as
  step 1 of `api-gate.sh`):
  - `make openapi WEB=/nonexistent` returned rc=0 and regenerated
    `backend/api/gen/paceday.gen.go` and `contracts/openapi/MIGRATION.md`
    (`disconnectMicrosoft` registered as handwritten), both committed;
  - `make openapi-check WEB=/nonexistent` returned rc=0, with "bundle is in sync
    (99 paths)", MIGRATION covering 120 operations, and "backend/api/gen matches
    the contract". The web half reported SKIPPED;
  - `go build ./...` returned rc=0;
  - `golangci-lint` (backend) returned rc=0.
- Not run at this stage:
  - The full `api-gate.sh`. It needs the host checkout on this branch, and the
    host checkout belongs to another stream's branch. Its test and coverage
    steps belong to the implementation PR.
  - The web gate. The web types are regenerated in the paired web PR against
    this branch.
  - The e2e gate. No behaviour has changed yet.

## Challenge — 2026-10-27

Reviewer: contract-challenger, against `factory/pac-67-contract` at `3a38fcc`
(committed state only), spec `docs/specs/PAC-67.md` at `fc27821` on
`factory/pac-67-spec`, web `smart-calendar-flow` at `origin/main` `ad52160`.

**Verdict**: accept-with-changes

### Requests wrongly permitted

1. `POST /api/conference/create` with
   `{"title":"   ","start":"2030-01-07T10:00:00Z","end":"2030-01-07T11:00:00Z"}`
   is permitted by `CreateConferenceRequest` (`title` has `minLength: 1`, and
   three spaces have length 3), but the operation description says the server
   answers 400 "invalid body" for a title that is empty after trimming. §8 of
   this document says the schema change means "a generated client cannot
   express the wrong request". For this request that is false.
   **Fix**: add `pattern: '\S'` to `CreateConferenceRequest.title`, keeping
   `minLength: 1`. The `end`-after-`start` rule cannot be expressed in OpenAPI,
   so the `end` description is the right place for it, and it is already there.

### Responses the client cannot handle / responses the contract cannot honour

2. **`disconnectZoom` and `disconnectMicrosoft` promise activity entries that the
   server cannot guarantee.** The contract makes two promises. A 200 with
   `confirmed` writes ONE `provider_disconnected` entry, `unconfirmed` writes TWO,
   and Outlook writes one. A 500 means that "NOTHING was changed ... no activity
   entry is written".
   - The `provider_disconnected` details carry the `revocation` outcome. On Zoom
     that outcome is only known after the remote call, and the remote call happens
     only after the local removal has committed (step 2). So that entry is
     written after the removal is final.
   - The only writer, `storage.WriteAuditLog` (`backend/storage/focus_blocks.go:80-88`),
     swallows insert failures: it logs and returns nothing.
   - Concretely, on Zoom: the credential is removed, Zoom answers success, and the
     audit INSERT fails. The server can no longer answer 500, because something
     did change. It answers `200 {"provider":"zoom","revocation":"confirmed"}`
     with zero entries, which contradicts the unconditional "writes ONE".
   - The same applies to every `connection_lost` "exactly one per loss" when the
     insert fails.

   **Fix (contract text only)**: in `disconnectZoom`, `disconnectMicrosoft` and
   the CONNECTION ENTRIES paragraph, say that activity entries are written after
   the outcome is known. Say they are best-effort, like every existing kind: a
   failed write is logged, the 200 stands, and no entry may then appear. Or, if
   the owner wants them guaranteed, the contract must name the status returned
   when the entry write fails after removal. Today it names none. Keep "500 =
   nothing changed" for failures before the removal commits.

3. **`disconnectZoom` has no truthful `revocation` value for a stored credential
   that does not decode.** That state is documented and reachable: the
   `createConferenceLink` 400 "zoom: stored credential is invalid — reconnect at
   /api/auth/zoom" comes from `conference.NewProvider`
   (`backend/conference/factory.go:26-29`), where `settings.zoom_tokens` is
   non-blank but is not valid JSON. Suppose such a user calls disconnect. The
   blob is removed (`ClearZoomTokens` blanks it), but no access token can be
   extracted, so no revocation can be sent. None of the four values fits:
   - `confirmed` is false;
   - `unconfirmed` is defined as "a revocation was attempted and ...", and none
     was attempted;
   - `not_offered` is Outlook only;
   - `nothing_stored` says "nothing was removed", which is false.

   The server must answer one of these, and whichever it picks contradicts its
   own definition.
   **Fix**: widen `unconfirmed` to "a revocation was attempted and not confirmed,
   or could not be attempted because the stored credential is unreadable". Say on
   `disconnectZoom` that this case writes `provider_disconnected` plus
   `revocation_unconfirmed`, so the user is still told (D-2).

4. **The observable `revocation` value on `disconnectZoom` is left to the plan.**
   The description says "whether the server first renews an expired access token
   to revoke it is the plan's choice". Zoom access tokens last one hour (this
   document §3). Zoom's OAuth page, which I re-read on 2026-10-27, gives `token`
   as the access token and does not say what happens to an expired one. Take a
   user who connected Zoom yesterday and disconnects today:
   - If the plan sends the stored, expired access token, the likely answer is an
     error. So nearly every real disconnect answers `unconfirmed`, and the grant
     (with a refresh token valid for 90 days) stays live at Zoom.
   - That defeats spec §3 ("the provider is asked to revoke Paceday's grant") and
     makes AC-14's `confirmed` reachable only in the first hour.

   Two implementations that both satisfy the contract would give different
   answers for the same stored state.
   **Fix**: pin the behaviour in the `disconnectZoom` description. An expired
   access token is renewed first with the removed refresh token, and the renewed
   token is held in memory only and never stored. The renewed access token is
   what is revoked. A refused or failed renewal makes `revocation: unconfirmed`
   and writes no `connection_lost` entry.

### Contract/implementation divergences

5. **The Teams row and `getAuthStatus` disagree on a Microsoft credential that
   has no refresh token, and the contract says they agree.**
   - The new `microsoftOAuthCallback` text says: "With no refresh token stored
     before either, the result is a credential with no refresh token, reported
     not connected exactly as today."
   - Today `getAuthStatus` (outlook) does report it not connected
     (`backend/api/handlers_auth.go:128`, `msToken != nil && msToken.RefreshToken != ""`).
   - The `teams` row of `listConferenceProviders` reports
     `{"provider":"teams","connected":true,"enabled":true,"auto_with":"outlook"}`
     for the same credential (`backend/api/handlers_conferencing.go:210`,
     `Connected: microsoftToken != nil, Enabled: microsoftToken != nil`).
   - The new `ConferenceProviderStatus.connected` text ("stored AND not recorded
     as lost") still permits that `true`, even though the credential can never
     be renewed. That breaks spec §3 "connected means usable" on the Teams row.
   - It is also unspecified what `createConferenceLink` and `addEventConference`
     answer once such a token expires. There is no provider refusal, so neither
     "connection lost" nor any listed sentence clearly applies.

   `offline_access` is requested (`backend/auth/microsoft_oauth.go:38`), so this
   is a guard case (spec OQ-6), but the contract's sentence is false today.
   **Fix**:
   - make `ConferenceProviderStatus.connected` and `enabled` for `teams` read
     "stored WITH a refresh token AND not recorded lost", the same predicate as
     `getAuthStatus` outlook (and state the Zoom row's predicate likewise);
   - add to both meeting-creation operations that an expired Teams or Zoom access
     token with no stored refresh token answers the 400 "connection lost"
     sentence for that provider (or name another listed sentence).

### Uncertainties not actually resolved

None. U-12 is settled. `grep -n 'Provider:' backend/conference/*.go` shows
exactly `meet` (meet.go:14), `teams` (teams.go:53) and `zoom` (zoom.go:58).
`conference.Details.JoinURL` has no `omitempty` (`backend/conference/provider.go:11`),
and `MeetProvider` returns `&Details{Provider: "meet"}`, so `joinUrl: ""` is
real and removing `format: uri` is correct. The bundle goes from 12 markers to
11.

### Manifest vs spec

6. Spec AC-10 includes "listing events fails as it does today". The manifest's
   `acceptanceTests` AC-10 entry drops the event-listing row, so it is narrowed
   silently. **Fix**: add "listing events fails as today" to the manifest's
   AC-10 line.

### Consumers affected (checked, none breaks)

- `smart-calendar-flow/src/api/client.ts:1521-1523` `zoomDisconnect` →
  `requestApi<void>`. `requestApi` (`client.ts:442-450`) returns early only on
  204. On a 200 it requires `content-type` to include `application/json`, then
  parses. The new 200 is `application/json`, so it resolves, and
  `ConferencingSection.tsx:98-99` ignores the value and shows "Disconnected Zoom".
  It does not break, but it shows plain success on `unconfirmed`, which the
  frontend stage owes (spec §5).
- `smart-calendar-flow/src/pages/Audit.tsx:97` renders `entry.action` raw. There
  is no exhaustive map over the enum, so the three new kinds render.
- `ConferencingSection.tsx:67-69` finds rows by `provider`, not by index, so it
  is unaffected by the `enabled` change.
- `Dashboard.tsx:114` and `Onboarding.tsx:61,84` read `getAuthStatus.connected`.
  After `disconnectMicrosoft` or a recorded loss, an Outlook user sees
  `calendarNotConnected`. That is intended (D-3) and the shape is unchanged.
- `mcp/tools.go:243` (`clockwise_calendar_status`) passes `/api/auth/status`
  through as JSON, and the shape is unchanged. No MCP tool calls conference,
  disconnect or audit.
- `e2e/tests/audit.spec.ts` asserts scoping and limits only. No e2e calls
  `/api/conference/*` or a disconnect.
- `backend/api/pac21_4xx_bodies_test.go:172` sends a valid
  `{"title":"T","start":...,"end":...}` body, which passes the new validation. No
  backend test asserts the 204 of `disconnectZoom`.

### Verified clean (attacked, could not break)

- `make openapi-check WEB=/nonexistent` in an LF clone of `3a38fcc`
  (golang:1.25-bookworm): "OK: bundle is in sync (99 paths)", "OK: MIGRATION.md
  covers all 120 operations", "OK: backend/api/gen matches the contract", web
  half SKIPPED, rc=0. `go build ./...` rc=0. The bundle's sha256 is
  `886a3e2d…a557c`, which matches the manifest's `contractHash`.
- **Google refusal on `addEventConference` really is the 404.**
  `calClient` (`handlers_events.go:27-34`) builds a lazy `TokenSource`, so the
  refresh runs inside `GetEvent`. Any error there is mapped to 404 "event not
  found" (`handlers_conferencing.go:314-317`), so D-10's "unchanged" holds. The
  loader fails with 401 only when no token row exists.
- **`getAuthStatus` outlook predicate.** The contract says "stored with a refresh
  token", which matches `handlers_auth.go:128`.
- **`disconnectZoom` resets conferencing to `meet` unconditionally (D-9).** One
  UPDATE does it (`storage/conferencing.go:12`), so "nothing changed on 500"
  holds for the local step.
- **`disconnectMicrosoft` is protected.** It inherits the global
  `security: [bearerAuth, cookieAuth]`, unlike the public `/api/auth/microsoft*`
  routes (`routes.go:35-36`), so the plan must register it in a `requireAuth`
  group. A 401 PlainTextError is consistent with the other auth operations.
- **`POST /api/events/{id}/conference` with `{"provider":"zoom"}`** while the
  Zoom row is lost. The 400 sentences are new, fixed, and in the same envelope
  as the three existing ones. The 401 and 409 meanings are untouched.
- **`ProviderDisconnectResult`** is closed (`additionalProperties: false`, both
  fields required), carries no token material, and every documented case maps to
  exactly one value. The exception is finding 3.
- **`createConferenceLink`** with `{"start":"x"}`: `time.Time` decode fails, and
  the answer is the existing 400 "invalid body".
- **OQ-1 / D-5.** No Microsoft call is made, and the contract text forbids
  `revokeSignInSessions`.
- **PAC-35 `disconnectCalendar` 409.** It is untouched; the change is to its
  description only.
- **`AuditEntry.action`.** The seven existing values are unchanged and in place.
  The three new values are appended.

### Not run

- The full `api-gate.sh`: the host checkout is on another stream's branch
  (`impl/pac-25-owner-guards-backend`), and I did not move it.
- The DB-backed Go tests, including `TestPAC21Every5xxInTheContractIsTheJSONEnvelope`,
  which reads the bundle. In the container they failed to start with "connect
  to test database at 127.0.0.1:15432: connection refused", so their result on
  this bundle is unknown.
- The web gate: web types are regenerated in the paired web PR.

### Non-blocking

- OWNER: should a Zoom disconnect whose revocation is `unconfirmed` also give
  the user a way to retry the revocation later? The contract has no retry
  operation, and the token is gone after removal.
- OWNER: is `connection_lost` / `provider_disconnected` as raw identifiers in
  the activity log acceptable until the frontend stage gives them copy?
- OWNER: a disconnect when nothing is stored writes no entry. This is assumed by
  the factory.
- Zoom disconnect while a loss is recorded (the refresh token is already refused)
  will almost surely answer `unconfirmed` and write `revocation_unconfirmed`. The
  grant is probably already gone. That is truthful, but the plan should test it.
- An undecodable `microsoft_tokens` blob. `LoadMicrosoftToken` returns an error
  (`microsoft_oauth.go:92-94`). `disconnectMicrosoft` should still remove it
  (`not_offered`) rather than 500. The contract's 500 says only "settings could
  not be read or written", so the plan should state which applies.
- The `AuditEntry.action` description says "the contract test of this enum
  against the writers covers all ten", and spec §6 calls it "protected today".
  I found no such Go test (`backend/api/handlers_audit_test.go`,
  `backend/storage/audit_log_test.go`), so the plan must add it, not extend it.
- `MIGRATION.md` registers the new `disconnectMicrosoft` as `handwritten`, while
  factory §4 says new endpoints use the generated interface. The plan should say
  which applies.
- `createConferenceLink` with an explicit `"start":"0001-01-01T00:00:00Z"` is
  schema-valid but will be 400 (indistinguishable from absent). This is
  acceptable, but it could be documented.
