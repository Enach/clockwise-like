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
