# Contract — PAC-41: the room and free/busy participant shapes, stated as the server emits them

- **Linear issue**: [PAC-41](https://linear.app/paceday/issue/PAC-41)
- **Spec**: `docs/specs/PAC-41.md`, accepted at Revision 2.1 (2026-11-13), carried on this
  branch. Revision 1 (`c2d9003`) was the draft this contract was first written against
  (section 0).
- **Stage**: contract-author, then contract-challenger
- **Fragment edited**: `contracts/openapi/paths/calendar.yaml` (plus its companion
  `calendar.notes.md`, items 1, 2 and 6)
- **Bundle**: regenerated with `python scripts/openapi_assemble.py`; generated Go by the
  api gate (`make openapi` in an LF clone); web types on the paired branch
  `factory/pac-41-contract-types`. §7 records what was run and what it printed.
- **Manifest**: `contracts/features/PAC-41.yaml`
- **Audit register**: `docs/factory/api-audit.md` rows API-040, API-041, API-046 (Claimed
  (PAC-41)) and API-047, API-075, API-078 (split note)

---

## 0. The spec is challenged, not yet re-challenged

*(Historical, kept for the record. Since 2026-11-13 the spec is accepted at Revision 2.1;
see the manifest's `specDoc` and `mergeConditions`.)*

The spec's status line reads "challenged (Revision 1 answers the 2026-10-27 challenge)".
The challenge returned **accept-with-changes** with four blocking items, and Revision 1
answers each of them. Nobody has re-run the challenge on Revision 1. The contract-author
definition says to stop when the spec is not accepted. I went ahead because the run that
started this stage asked for the contract, and because the PAC-42 contract set the
precedent: a draft contract against an answered-but-unrechallenged spec, with that fact
recorded in the manifest's `specDoc` and `mergeConditions`. Neither PR may merge before
the spec is accepted. If a later challenge round changes an acceptance criterion, this
contract is revised and challenged again.

## 1. What changes at the HTTP boundary

Nothing on the wire. The spec's premise (§2.1, §2.3) is that the server and the contract
already agree, and the web is the side out of step. I re-checked that on this branch:

| Claim | Server | Contract before this change |
|---|---|---|
| A room is `{id, name}` | `backend/calendar/rooms_attendees.go:11-14` | `CalendarRoom`, both required, `additionalProperties: false` |
| Room search reads only `q` | `backend/api/handlers_events.go` `listRooms` | only `q` declared |
| A participant is `{email, status, busy}` | `handlers_freebusy.go` `freeBusyParticipantDTO` | `FreeBusyParticipant`, `additionalProperties: false` |
| `status` is `known` or `unknown` | `engine/freebusy_service.go` | `enum: [known, unknown]` |

So the contract changes are **descriptions only**, and each one says something a client
author needs and could get wrong:

| Where | Added | Why |
|---|---|---|
| `listRooms` description | `q` is the only parameter read; no time-window or availability filter; `start`/`end` are ignored; each room is exactly `{id, name}`; the list reads the stored Google credential whatever `calendar_provider` is, so a caller with none gets the 401 `calendar not connected` (every Outlook-only caller is one) and an Outlook-selected caller who also holds a Google credential gets that Google calendar's rooms (revision 2) | API-040 happened because a client sent parameters it believed were honoured. AC-16 needs the web to know the 401 is decided by the Google credential, not by the selected provider. |
| `CalendarRoom` | no building/floor/capacity/email/availability field; this is the only room type | API-041 is a hand-written type that grew fields the server never sends (AC-6). |
| `CalendarRoom.id` | send it unchanged as `EventPatchRequest.room_resource_email`; the list does not itself check PATCH's stricter pattern | Booking (AC-1, AC-2) depends on exactly this mapping, which before was implicit in the word "email". |
| `FreeBusyParticipant` | exactly three keys; no provider key; no Paceday-user status | API-046 (AC-7, AC-8); D-2 makes "no Paceday-user status" a privacy property, not an omission. |

The generated Go (`backend/api/gen`) and the web's generated `types.ts` and
`schemas.types.d.ts` change only in doc comments. No type changes in either repo.

## 2. Operations touched, and touch-it-fix-it (factory §7)

- `listRooms` (`GET /api/rooms`): description edited. Open audit findings on it:
  API-040 and API-041, both resolved by this feature (web side). No `x-uncertain`.
- `queryFreeBusy` (`POST /api/freebusy`): **not edited.** Only the
  `FreeBusyParticipant` schema's description changed. That is why API-077 (no
  `end > start` check, a server-side 400) is not pulled in: the spec puts it with
  PAC-47 (header, OQ-2), and changing that operation here would send the work back to
  the spec stage. API-046 is on this operation and is resolved here (web side);
  API-028 is already Fixed (PAC-26). No `x-uncertain` on the operation or the schema.

The `FreeBusyParticipant` description edit does reach the schema PAC-47 will also touch.
OQ-2's rule holds: whichever lands second must not reintroduce a provider key or a third
status. The description now says so in the contract itself, which is where PAC-47's
contract author will read it.

## 3. Alternatives rejected

1. **Grow the server's room to the web's shape** (building, floor, capacity,
   availability). Rejected by spec D-1: that is a new feature reading more Google
   directory data, to fix a type mismatch. OQ-1 is how it comes back if the owner wants
   it, as its own spec.
2. **Declare `start` and `end` on `listRooms` as `deprecated: true` and "ignored".**
   Rejected (C-2). A declared parameter is generated into the web's typed client as
   something you may send; the defect is precisely that a client sends them. The
   description says they are ignored, which is all a reader needs, and nothing invites
   a client to send them.
3. **Add `pattern: '^[^@\s]+@resource\.calendar\.google\.com$'` to `CalendarRoom.id`.**
   Tempting, because it would make the "id is bookable" rule machine-checkable in the
   web's schemas. Rejected (C-1): the list keeps any id that *contains*
   `resource.calendar.google.com`, so the pattern would describe what should be, not
   what is (factory §7 rule 2). Google's room resource ids do have that form; the
   description says both things. If the owner wants the guarantee, it is a one-line
   server filter plus this pattern, as its own change.
4. **Add a `CalendarRoom.email` field equal to `id`.** Would have let the web keep
   reading `email` unchanged. Rejected: it is a server change (spec §3 says the server
   does not change), it duplicates a value under two keys, and the web would still need
   to drop `building`/`floor`/`capacity`/`available`, so it saves nothing.
5. **Mark API-040, API-041, API-046 Fixed now.** Rejected (C-3): the defects are in the
   web, and they are not fixed until the web PR merges. They are `Claimed (PAC-41)`,
   the status API-039 already uses for a claimed web-side fix, and each row says what
   lands it. AC-11's "Fixed after this work merges" is met by the build stage.

## 4. Consumers

Searched both repos (web `origin/main` `ad52160`, api this branch):

- Web: no hand-written code imports `CalendarRoom` or `FreeBusyParticipant`. The
  hand-written `Room`, `CoverageStatus` and `ParticipantCoverage` in `src/api/types.ts`
  are what the build replaces (AC-6, AC-7); they are not consumers of this contract
  today, which is the defect.
- `mcp/tools.go:207-215` (`clockwise_search_rooms`): sends `q` only, relays the body.
  Unaffected.
- `e2e/tests/seam.spec.ts:348-352`: calls the room list, reads no keys. Unaffected.
- `backend/api/gen`: doc comments only; both operations are `handwritten` in
  `MIGRATION.md`.

No breaking change: nothing on the wire changed.

## 5. What the build must add that the contract cannot express

- **AC-15** is the only `[contract]` criterion about the wire, and OpenAPI already says
  `{id, name}` with `additionalProperties: false`. What is missing is a backend test
  that reads the 200 body and fails if the key set changes. That test belongs to the
  backend build (manifest `allowedPaths`).
- **AC-11**'s final state (Fixed) is set when the web PR merges.
- Every other criterion is a web unit test or an e2e scenario.

## 6. Decisions assumed by the factory, pending owner confirmation

- **C-1**: no `pattern` on `CalendarRoom.id` (§3.3).
- **C-2**: no declared `start`/`end` on `listRooms` (§3.2).
- **C-3**: the three resolved findings are `Claimed (PAC-41)` until the web PR merges (§3.5).
- Spec D-2 to D-5 are inherited. D-1 and D-6 are narrowed to interim decisions by the
  owner answers of 2026-10-27 to OQ-1 and OQ-5 (spec D-7: PAC-100, PAC-102); see the
  maintenance section at the end.

Open owner questions carried from the spec: OQ-1 (room details/availability), OQ-3
(confirm the D-4 split and file the follow-up; no follow-up issue existed in Linear on
2026-10-27, so the audit rows carry the fallback note), OQ-5 (replace/release rooms),
OQ-6 (confirm D-2).

## 7. Gates run

See the Linear comment for this stage for the gate output on the final commits of
`factory/pac-41-contract` and `factory/pac-41-contract-types`.

## Challenge — 2026-10-27

**Verdict**: accept-with-changes

Reviewer: contract-challenger. Read: `factory/pac-41-contract` at `afd903d` (committed state
only), `docs/specs/PAC-41.md` revision 1 as carried on that branch, the handlers and
stores cited below, web `origin/main` `ad52160` and the paired web branch
`factory/pac-41-contract-types` `cc63793`. I did not see the author's reasoning beyond
this file and the manifest.

### Requests wrongly permitted

None found. No parameter, request body, required set or enum changed (`git diff
origin/main...afd903d -- contracts/openapi/paths` is descriptions only). Attacked:

1. `GET /api/rooms?q=helsinki&start=2026-10-27T09:00:00Z&end=2026-10-27T10:00:00Z`.
   The contract now says `start`/`end` are ignored; `listRooms`
   (`backend/api/handlers_events.go:312-329`) reads only `r.URL.Query().Get("q")`. Holds.
   C-2 (not declaring them) is consistent with that: the generated web client offers no
   `start`/`end`, so `LocationPicker.tsx:35,49` passing them through `client.ts:1418` is
   the web defect the build removes, not a contract gap.
2. `GET /api/rooms` returning a room with
   `{"id":"x.resource.calendar.google.com@group.calendar.google.com","name":"X"}`, then
   `PATCH /api/events/{id}` with `{"room_resource_email":"x.resource.calendar.google.com@group.calendar.google.com"}`.
   `ListRooms` keeps that id (`backend/calendar/rooms_attendees.go:31` is a
   `strings.Contains`), PATCH rejects it (`^[^@\s]+@resource\.calendar\.google\.com$`,
   `handlers_events.go:48`, 400 `invalid room_resource_email`). The contract permits this
   list-then-book sequence to 400, and `CalendarRoom.id` now says so explicitly. That is
   the server's behaviour described honestly (factory §7 rule 2), so not blocking;
   whether to filter the list is an owner question (C-1).

### Responses the client cannot handle

None introduced: the wire is unchanged. The existing mismatches (web `Room.email`
required, `CoverageStatus` with `paceday_user`, `ParticipantCoverage.provider` in
`smart-calendar-flow/src/api/types.ts:159-167,252-261`) are exactly what the spec's
build stage removes (AC-6, AC-7); the contract now describes the shape the web must
consume.

### Consumers affected

- `smart-calendar-flow/src/api/generated/{types.ts,schemas.types.d.ts}`: doc comments
  only on `factory/pac-41-contract-types` (`git diff ad52160...cc63793`: 8 changed
  lines, all `@description`). No hand-written web code imports `CalendarRoom` or
  `FreeBusyParticipant` (grep of `src/` outside `generated/` on `ad52160`: no hits).
- `clockwise-like/backend/api/gen/paceday.gen.go`: doc comments only.
- `clockwise-like/mcp/tools.go:207-216` (`clockwise_search_rooms`): sends `q` only,
  relays the body. Unaffected.
- `clockwise-like/e2e/tests/seam.spec.ts:350`: `GET /api/rooms` seam check, reads no
  keys. `e2e/tests/calendar.spec.ts:195` and `seam.spec.ts:163-190` call free/busy and
  read no `provider` or `status` value. Unaffected.
- `FreeBusyParticipant` is referenced only from `queryFreeBusy` (`calendar.yaml:1510`);
  `PublicLinkCoverage` (`booking.yaml:1255`) is a separate schema, not touched.

### Contract/implementation divergences

1. **BLOCKING: "an Outlook-connected caller ... gets the 401" is false for a reachable
   user.** The `listRooms` description (`calendar.yaml:382-384`) says "a caller with no
   stored Google credential, an Outlook-connected caller included, gets the 401
   `calendar not connected`", and §1 of this file says the 401 "also covers Outlook
   users". The handler decides on the Google token row alone: `calClient` ->
   `engine.LoadCalendarToken` -> `auth.LoadUserToken`
   (`backend/auth/token_store.go:40-50`, `SELECT ... FROM oauth_tokens WHERE user_id`).
   A Microsoft sign-in does not remove that row: `microsoftCallback`
   (`backend/api/handlers_microsoft_auth.go:69-77`) resolves the user with
   `storage.UpsertUser(..., "microsoft", ...)`, which is `ON CONFLICT (email)`
   (`backend/storage/users.go:21-28`), so a person who once signed in with Google keeps
   the same user id; `auth.SaveMicrosoftToken` (`backend/auth/microsoft_oauth.go:63-81`)
   only sets `settings.microsoft_tokens` and `calendar_provider = 'outlook'`.
   Concretely: `a@corp.com` signs in with Google (row in `oauth_tokens`), later signs in
   with Microsoft (`calendar_provider = 'outlook'`), then `GET /api/rooms` answers
   **200** with the Google room list, not 401.
   Fix (description only): replace the last sentence of the `listRooms` description with
   "The list reads the caller's stored Google credential whatever `calendar_provider`
   is: a caller with no stored Google credential gets the 401 `calendar not connected`
   (every Outlook-only caller is one); an Outlook-selected caller who also holds a
   Google credential (same email signed in with Google earlier) gets that Google
   calendar's rooms." Correct the §1 table row here the same way. The spec's §3 bullet
   ("this includes Outlook-connected users") carries the same overstatement; AC-16 is
   unaffected (it reacts to the 401, whoever gets it).

Everything else in the new descriptions matches the code:
- `CalendarRoom` = `calendar.Room{ID, Name}` with no `omitempty`
  (`rooms_attendees.go:11-14`): both keys always emitted; a nil list becomes `[]`
  (`handlers_events.go:324-326`).
- `FreeBusyParticipant` = `freeBusyParticipantDTO{Email, Status, Busy}`
  (`backend/api/handlers_freebusy.go:23-27,83-87`); `Status` is
  `ParticipantBusy.Coverage`, which `engine/freebusy_service.go:93,143-146` sets only
  to `known` or `unknown`. No `provider` and no `paceday_user` producer anywhere in
  `backend/` (grep: only the unrelated `is_paceday_user` on manager analytics).
- `q` is lowercased, not trimmed; empty means no filter (`handlers_events.go:313`,
  `rooms_attendees.go:34-38`).

### Uncertainties not actually resolved

None claimed: `resolvesUncertain` is `[]`, and no `x-uncertain` sits on `listRooms`,
`queryFreeBusy`, `CalendarRoom` or `FreeBusyParticipant` on this branch. API-040, -041
and -046 are `Claimed (PAC-41)`, not Fixed, which is truthful: no web or backend change
has landed. AC-15's pinning test does not exist yet and is correctly assigned to the
backend build.

### Non-blocking

1. API-040's row (edited by this change) and `calendar.notes.md` item 2 still cite
   `handlers_events.go:106`; the handler is at `:312`. Fix the citation while the row is
   being edited.
2. The manifest's `acceptanceTests` paraphrase criteria and drop qualifiers the spec
   has: AC-8 loses "(not an accepted host on the saved link)" and the named providers
   ("Google", "Microsoft", "Outlook"); AC-12 loses "searching for a room or with a room
   selected"; AC-14 loses "reopens it". A build stage reading only the manifest could
   write a narrower test. Copy them verbatim.
3. No PR is open yet for `factory/pac-41-contract` or `factory/pac-41-contract-types`
   (`gh pr list --head <branch> --state all`: empty in both repos). The manifest puts
   the PR after this challenge; both should open together once item 1 above is fixed.
4. The spec is not yet accepted (manifest `mergeConditions`); this review does not
   change that condition.
5. No follow-up issue for API-047/075/078 was found in Linear (PAC search "API-047"),
   so the fallback "Split from PAC-41 (D-4); follow-up issue not yet filed" note is the
   form AC-11 asks for.

### Verified clean

- `python scripts/openapi_assemble.py --check` on `afd903d`: `OK: bundle is in sync (98
  paths)`, rc=0. `git show HEAD:contracts/openapi/openapi.yaml | sha256sum` =
  `7e0d333c...eabee`, equal to the manifest's `contractHash`.
- `make openapi-check WEB=/nonexistent` in a `golang:1.25-bookworm` LF clone of
  `factory/pac-41-contract` at `afd903d`: bundle in sync; MIGRATION.md covers all 119
  operations; `OK: backend/api/gen matches the contract`; web half SKIPPED there; rc=0.
- Web half: `scripts/openapi-gen.sh --check` in a clone of
  `factory/pac-41-contract-types` at `cc63793` against this branch's bundle:
  `OK: src/api/generated matches the contract`, rc=0.
- Not run: the full `api-gate.sh` (it requires the host checkout on this branch, and
  the host checkout is in use on another branch), `web-gate.sh`, `e2e-gate.sh`. This
  commit adds only this section.
- Attacked and could not break: ignored `start`/`end`; a room's key set
  (`additionalProperties: false`, both required, matches the struct); a third coverage
  status or a `provider` key (no producer); a nil room list (`[]`); a credential-store
  failure (`ErrCredentialStore` -> 500 via `server_errors.go:75-81`, matching the 500
  description); the MCP and e2e consumers above.

## Revision — 2026-10-27 (contract revision 2, answers the challenge above)

Contract revision 2 is `2fe4cee` on `factory/pac-41-contract`; the paired web types are
`df887a4` on `factory/pac-41-contract-types`. The wire is still unchanged: the only
bundle change is the `listRooms` description.

### Blocking

1. **"An Outlook-connected caller gets the 401" is false.** Accepted; I re-read the code
   and it holds as the challenge states: `calClient` -> `engine.LoadCalendarToken` ->
   `auth.LoadUserToken` (`backend/auth/token_store.go:40-50`) reads only `oauth_tokens`;
   `storage.UpsertUser` is `ON CONFLICT (email)` (`backend/storage/users.go:21-28`), so a
   later Microsoft sign-in keeps the user id and its Google row;
   `auth.SaveMicrosoftToken` (`backend/auth/microsoft_oauth.go:63-81`) only sets
   `settings.microsoft_tokens` and `calendar_provider = 'outlook'`. Fixed, description
   only:
   - `calendar.yaml` `listRooms`: the last sentence now reads "The list reads the
     caller's stored Google credential whatever `calendar_provider` is: a caller with no
     stored Google credential gets the 401 `calendar not connected` (every Outlook-only
     caller is one); an Outlook-selected caller who also holds a Google credential (the
     same email signed in with Google earlier) gets that Google calendar's rooms."
     Bundle reassembled.
   - §1 of this file: the `listRooms` row says the same, and its "why" now says the 401
     is decided by the Google credential, not by the selected provider.
   - Manifest `operationsChanged.listRooms.change`: the same.
   - Spec (`docs/specs/PAC-41.md`): the §3 bullet, and the §2.4a sentence that carried
     the same overstatement, now say "every Outlook-only user" and name the
     Outlook-plus-Google case, each marked "corrected at contract revision 2". No
     acceptance criterion changes: AC-16 reacts to the 401 whoever receives it. The
     historical text in the spec's Challenge and Revision 1 sections is left as
     written.

### Non-blocking

1. **Stale `handlers_events.go:106`.** Fixed in the API-040 row of
   `docs/factory/api-audit.md` and in `calendar.notes.md` item 2: both now cite
   `handlers_events.go:312` (`listRooms`). The same stale line number also appears in
   the API-040 work-item body in `docs/factory/api-audit-issues.yaml:467`; that file is a
   record of the proposed issue text and this change does not otherwise touch it, so it
   is left as written.
2. **Manifest acceptance tests paraphrased.** Fixed: `acceptanceTests` AC-1 to AC-16 are
   now the spec's §4 text verbatim (whitespace joined, test level moved to the front),
   so AC-8 keeps "(not an accepted host on the saved link)" and the named providers,
   AC-12 keeps "searching for a room or with a room selected", AC-14 keeps "reopens
   it". A new `acceptanceTestsSource` field says so and that the spec wins on any
   difference. AC-11 carries one addition, marked as a manifest note. DC-1 is unchanged.
3. **No PRs open.** Both PRs are opened together with this revision, one per repo.
4. **Spec not accepted.** Unchanged and still recorded in `mergeConditions`: neither PR
   may merge before `docs/specs/PAC-41.md` is accepted.
5. **OWNER C-1** (filter `GET /api/rooms` to ids PATCH accepts). Not decided here; still
   assumed (no filter, no `pattern`), pending owner confirmation, as §6 records.
6. **OWNER C-2 and C-3.** Still assumed, pending owner confirmation (§6).
7. **OWNER D-1 to D-6, OQ-1, OQ-3, OQ-5, OQ-6.** Still assumed, pending owner
   confirmation (§6, spec Decisions). No follow-up issue for API-047/075/078 exists, so
   the rows keep the fallback note.

### Gates run on revision 2

- `bash api-gate.sh factory/pac-41-contract` on `2fe4cee` (host checkout on the
  branch): `make openapi rc=0`, `regenerated: none`, `OK: bundle is in sync (98
  paths)`, `OK: MIGRATION.md covers all 119 operations (0 generated)`, `OK:
  backend/api/gen matches the contract`, `openapi-check(api) rc=0`, `backend lint
  rc=0`, `mcp lint rc=0`, `backend test rc=0`, `mcp test rc=0`, backend coverage 56.8%
  (floor 43%) `rc=0`, mcp coverage 84.0% (floor 75%) `rc=0`. The generated Go did not
  change: the edited text is an operation description, which `oapi-codegen` does not
  emit for this handwritten operation.
- `REGEN=1 bash web-gate.sh factory/pac-41-contract-types factory/pac-41-contract`:
  regenerated `src/api/generated/types.ts` and `schemas.types.d.ts` (one description
  line each), committed as `df887a4`. Re-run without `REGEN` on `df887a4`: lint,
  typecheck, design-check, openapi-check, test (297 passed), coverage all PASS, `make
  verify rc=0`.
- The e2e gate is not touched by this stage.
- The api gate is re-run on the commit that adds this section; its result is in the
  PR body.

## Maintenance — 2026-11-13 (contract revision 3, main merged)

`origin/main` (33 commits since `6d751f9`, up to `68540f6`) was merged into this branch.
The only conflict was `docs/factory/api-audit.md`: main's API-039 row (its `LLMProvider`
half was fixed by NEW-llm-bring-your-own) was taken, and the API-040/041 rows keep their PAC-41 text.

### The shapes, re-read against main `68540f6`

- `calendar.Room` is still `{ID, Name}` with no `omitempty`
  (`backend/calendar/rooms_attendees.go:11-14`, unchanged since `6d751f9`). `listRooms`
  is still `handlers_events.go:312-330` and reads only `q`.
- `freeBusyParticipantDTO` is still `{Email, Status, Busy}`
  (`backend/api/handlers_freebusy.go:23-27`, file unchanged). `Status` is still
  `ParticipantBusy.Coverage`. `engine/freebusy_service.go` now sets it at `:111`
  (`unknown`) and `:181-183` (`known`/`unknown`), still with no third value and no
  provider. PAC-67 changed only how the requester's credential is renewed and when
  a degraded answer is cached.
- The 401 rule still holds, through new code. `calClient` (`handlers_events.go:27-35`)
  is now `engine.LoadCalendarTokenCtx` -> `auth.LoadUserTokenCtx` (session-generation
  guarded, PAC-71) -> `auth.LoadUserToken` (`backend/auth/token_store.go:111-121`, still
  `SELECT ... FROM oauth_tokens WHERE user_id`). No row still means
  `ErrCalendarNotConnected`, which is the 401. The token source is now
  `auth.UserTokenSource` (PAC-67).
- New since revision 2, and now stated in the `listRooms` description: a stored
  Google credential decides the 401 even when it cannot be used.
  - A credential the provider has permanently refused to renew (a PAC-67 loss): the
    row stays, so `UserTokenSource` returns `auth.LostError`, `ListRooms` fails, and
    the answer is `writeServerError` 500 `internal error`.
  - A credential that cannot be decrypted (PAC-84): `LoadCalendarTokenCtx` wraps it
    as `ErrCredentialStore`, so the answer is the 500.
    `TestPAC84_GoogleUndecryptable` pins this on `GET /api/calendar/events`, which
    uses the same `calClient` path.

  Neither case is the 401. This is a description change only; the 500 response
  already covered "the stored credential could not be read".

Rejected alternative: leaving the description as it was. It is not false, but it
only names the no-row case, and a client reading "no stored Google credential -> 401"
could expect a lost connection to give the 401 as well. AC-16's "could not be loaded"
branch is what a lost connection reaches.

### Owner answers recorded

`.claude/factory/owner-decisions.md` (2026-10-27) answers spec OQ-1 (rooms carry
capacity, and building and floor where available, from Google and Microsoft directory
data, with a graceful fallback and an optional extended-access step) and OQ-5 / D-6
(ask whether to release or keep the earlier room, with a remembered choice in Settings).
Both are now recorded in the spec's Decisions as "owner, 2026-10-27".

They narrow D-1 and D-6 to interim decisions (narrowed / interim, not superseded):
spec Revision 2 (D-7) keeps both as PAC-41's interim behaviour and moves the owner's
answers to their own tickets, PAC-100 (room details) and PAC-102 (release or keep).
They do not make this contract wrong, because the server still emits `{id, name}` and
never releases a room. When those tickets land, `CalendarRoom` gains optional fields
(PAC-100) and AC-12 to AC-14 are replaced (PAC-102). Spec Revision 2.1 carries this and
is accepted; the merge condition it set is met.

### Regeneration

Done in an LF docker clone (`golang:1.25-bookworm`, `make openapi`):
- Only `contracts/openapi/openapi.yaml` changed (the `listRooms` description).
- `backend/api/gen` was already in sync after the merge.
- `openapi-check` (api half): bundle in sync (108 paths), MIGRATION.md covers 130
  operations (12 generated), gen matches.
- `contractHash` `227460b8240b2cf4163ff0c4d1c942b25eab45b4b0b658d9d6f3cdaf50ccfe9c`.

The web types are regenerated against this on `factory/pac-41-contract-types`. The
gate results are in the PR comments.

Not re-challenged: the revision 3 description sentence and the recorded owner
answers have not been through contract-challenger.

## Challenge — 2026-11-13 (revision 3)

Contract-challenger on contract revision 3 (`2396085`), against api `origin/main`
`68540f6` (already merged into this branch) and web `origin/main` `ae72939`. The fixes
below were then made in the contract-author role as **revision 4**, descriptions only.

**Verdict**: accept-with-changes (applied in place as revision 4)

### Requests wrongly permitted
None. `listRooms` declares `q` only and has no body; `queryFreeBusy` is unchanged.

### Responses the client cannot handle
None new. A room's `name` is Google's `summary` and may in principle be `""`
(`backend/calendar/rooms_attendees.go:39`); the schema allows it and the picker shows
an empty name. Not a revision 3 change; noted only.

### Contract/implementation divergences
1. **"(the same email signed in with Google earlier)"**: the revision 2/3 explanation of
   how an Outlook-selected caller holds a Google credential describes matching by
   email, which PAC-71 removed. Today a Google credential lands on an account through
   the signed-in connect step (`callback` connect branch, `UpsertUserToken`,
   `backend/api/handlers_pac71_auth.go:136-146`) or through a Google sign-in that
   `storage.ResolveSignIn` binds to the same account by identity (`:164-176`). An
   Outlook user whose address merely matches a Google account does not get it.
   **Fixed:** "(one who connected Google while signed in, or whose Google sign-in
   reached the same account, PAC-71)". The spec's §2.4a has the same parenthetical; it
   is current-behaviour prose, not a criterion, and is left to the next spec revision.
2. **"TestPAC84_GoogleUndecryptable on the same calendar-client path"** overstates the
   evidence. The test drives `GET /api/calendar/events`, i.e.
   `calendarHandlers.getCalendarClient` (`backend/api/handlers_calendar.go:137-145`), not
   `eventHandlers.calClient` (`handlers_events.go:27-35`), which `listRooms` uses. The two
   are line-for-line twins: same `engine.LoadCalendarTokenCtx`, same
   `auth.UserTokenSource`, same `writeCalendarFailure` mapping. So the claim about
   `/api/rooms` holds by reading, but **no test pins the 401-vs-500 split on
   `/api/rooms`**. **Fixed:** the description now says the PAC-84 test pins the shared
   credential load on `GET /api/calendar/events` and that no test pins it on this
   operation.
3. **One more "stored but unusable" case was missing.** A stored Google credential whose
   access token has expired and which holds no refresh token makes
   `renewingSource.Token` return `&LostError{Err: errNoRefreshToken}`, not recorded
   (`backend/auth/credential_lifecycle.go:147-148`). `ListRooms` then fails and the
   answer is the 500 (`handlers_events.go:320-324`), not the 401. **Fixed:** named
   alongside the PAC-67 loss and the PAC-84 decryption failure.
4. **The 500 response text** said only "the stored credential could not be read". A
   PAC-67 loss is read fine and fails at renewal. **Fixed:** "could not be read,
   decrypted or renewed (PAC-67, PAC-84)".

### The 500-vs-401 sentence, attacked
- `GET /api/rooms`, caller with a stored Google row whose refresh token Google refuses
  with `invalid_grant`: `Token()` re-loads the row, calls the token endpoint, records the
  loss and returns `LostError{Recorded: true}` (`credential_lifecycle.go:155-164`). The
  row is not deleted (`DeleteUserToken` is called only by the disconnect handler,
  `handlers_auth.go:162`, and the operator reset). `CalendarList.List` fails through the
  oauth2 transport, so `writeServerError` answers `500 {"error":"internal error"}`.
  Holds.
- Same, refresh token sealed with a lost key: `scanToken` → `storage.OpenSecret` fails
  (`backend/auth/token_store.go:144-150`) → `LoadCalendarTokenCtx` wraps it as
  `ErrCredentialStore` (`backend/engine/cal_iface.go:45-46`) → `writeCalendarFailure`
  sees no `ErrCalendarNotConnected` → 500 (`backend/api/server_errors.go:75-81`). Holds.
- After the operator's PAC-84 reset deletes the undecryptable row
  (`backend/storage/encryption_ops.go:417-423`), the caller has no stored credential and
  gets the 401 `calendar not connected`. Consistent with "a **stored** Google credential
  decides this".
- Session generation advanced between `requireAuth`'s read and the token load: the
  in-request re-check inside `LoadUserTokenCtx` returns `ErrStaleSession`, which
  `LoadCalendarTokenCtx` wraps as `ErrCredentialStore`, so the handler writes the 500,
  but `guardedResponseWriter` replaces it with
  `401 text/plain {"error":"unauthorized"}\n` (`backend/api/middleware.go:157-215`). So
  a credential-store failure can surface as a 401, but only as the session-ended 401,
  never as `calendar not connected`. A generation that cannot be read at that point
  gives the `SessionGenerationUnavailable` 500. Both are consistent with the sentence
  and with the shared responses.

### The shared 401 on the room list
`listRooms` uses `#/components/responses/CalendarNotConnected`, which has two bodies:
`application/json {"error":"calendar not connected"}` (`CalendarNotConnectedError`, an
enum of one) and `text/plain {"error":"unauthorized"}\n` (`MiddlewareUnauthorized`, which
since PAC-71 also names the in-request re-check). That matches the server. Revision 3's
operation text, though, said only "a caller with no stored Google credential gets the
401 `calendar not connected`". A client author reading only the operation could branch
on the status alone, which is the mistake AC-16 forbids. **Fixed:** the operation now
says the 401 is shared, that a session that has ended answers it with
`{"error":"unauthorized"}`, and that a client tells "calendar not connected" apart by the
body, never by the status. The web can do that: `interpretResponse` JSON-parses a
`text/plain` body (`smart-calendar-flow src/api/client.ts:538-556`), so `e.data.error` is
`"unauthorized"` or `"calendar not connected"`.

### AC-16 rewrite, confirmed
Each branch of AC-16 (Revision 2.1) maps to a contract answer:
- the "calendar not connected" refusal → 401 JSON `CalendarNotConnectedError`;
- session ended → 401 `MiddlewareUnauthorized`, the same status with the `unauthorized`
  answer (from `requireAuth` or from the in-request re-check above);
- any other failure → the 500 JSON (`ApiHttpError` 500: a lost, expired or
  undecryptable credential, a Google error, an unreadable session generation), a
  non-JSON 5xx from the proxy (`ApiGatewayError`), or no answer (`ApiUnreachableError`).
  Since PAC-83 there is no demo fallback (`src/api/demoGate.ts`).

No 401 on this operation carries any other body. The manifest's `acceptanceTests` are
now the Revision 2.1 text (AC-16 rewritten; AC-3, AC-8, AC-10, AC-11 and AC-12 to AC-14
reworded with their interim notes). Nothing is narrowed.

### Consumers affected
- `smart-calendar-flow src/api/generated/types.ts`: the `listRooms` JSDoc changes.
  **PR #128 (`factory/pac-41-contract-types`, `a26cb1b`, generated at revision 3) is
  stale and must be regenerated against revision 4.** No type changes.
- `backend/api/gen`: unchanged (`make openapi` in an LF copy regenerated only the bundle).
- `mcp/tools.go:207-215`: sends `q` only; unaffected. Its tool description says "name
  prefix" while the server matches a substring of the name or the id: an MCP wording
  nit, not a contract issue.
- `e2e/tests/seam.spec.ts:350-352`: reads no keys; unaffected.

### Uncertainties not actually resolved
None claimed (`resolvesUncertain` is empty; no `x-uncertain` on these operations).

### Non-blocking
- The AC-15 backend PR may add, beside the 200 key-set test, one `/api/rooms` case for
  an undecryptable or lost credential (500, not 401), so the sentence in finding 2 is
  pinned on this operation too. Not an acceptance criterion; recommended.
- `ListRooms` reads only the first `CalendarList` page (no `pageToken`,
  `rooms_attendees.go:25`). The 200's "capped only by what Google returns" is true but
  hides that rooms beyond the first page are never listed. Pre-existing; not inherited.

### Verified clean
- `CalendarRoom` is `{ID, Name}` with no `omitempty` (`rooms_attendees.go:11-14`); the
  empty list is `[]` (`handlers_events.go:325-327`).
- `listRooms` reads only `q`, lowercased, not trimmed (`handlers_events.go:314`).
- `FreeBusyParticipant`: three keys (`handlers_freebusy.go:23-27`), `status` only
  `known`/`unknown` (`engine/freebusy_service.go:111,180-183`), `busy` never null
  (`:185-187`).
- Naming and security conventions unchanged: no `security` key on `listRooms`, shared
  responses untouched, `operationId`s unchanged.
- `make openapi-check` (api half, LF copy, `golang:1.25-bookworm`): bundle in sync (108
  paths), MIGRATION.md covers 130 operations (12 generated), gen matches. rc=0.

### Revision 4 (applied)
`contracts/openapi/paths/calendar.yaml`: the `listRooms` description and its 500 text, as
above. Bundle regenerated in an LF copy; `backend/api/gen` unchanged. `contractHash`
`77f0a60841bec8b9399ba8786f6c9ffc8329d4610f803f3f196f2a6354d29f7e`. Manifest:
`contractRevision` 4; `specDoc` and `mergeConditions` for the accepted Revision 2.1;
`acceptanceTests` re-copied; `followUps` OQ-1 → PAC-100 and OQ-5 → PAC-102.
Not run: the full api gate (it needs the host checkout on this branch, which this run
does not change) and the web gate for the regenerated #128.
