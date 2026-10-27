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
