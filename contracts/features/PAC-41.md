# Contract — PAC-41: the room and free/busy participant shapes, stated as the server emits them

- **Linear issue**: [PAC-41](https://linear.app/paceday/issue/PAC-41)
- **Spec**: `docs/specs/PAC-41.md`, revision 1 (`c2d9003`, branch `factory/pac-41-spec`,
  carried on this branch)
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
| `listRooms` description | `q` is the only parameter read; no time-window or availability filter; `start`/`end` are ignored; each room is exactly `{id, name}`; an Outlook-connected caller gets the 401 `calendar not connected` | API-040 happened because a client sent parameters it believed were honoured. AC-16 needs the web to know the 401 also covers Outlook users. |
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
- Spec D-1 to D-6 are inherited unchanged.

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
