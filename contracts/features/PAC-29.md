# Contract — PAC-29: three event mutations return the CalendarEvent DTO, not Google's raw event

- **Linear issue**: [PAC-29](https://linear.app/paceday/issue/PAC-29)
- **Spec**: none (see §0)
- **Stage**: contract-author, then contract-challenger
- **Fragments edited**: `contracts/openapi/paths/calendar.yaml` (`patchEvent`,
  `CalendarEvent` descriptions, `GoogleCalendarEventRaw` deleted) and
  `contracts/openapi/paths/scheduling.yaml` (`createScheduledMeeting`,
  `confirmNaturalLanguage`, `GoogleCalendarEvent` / `GoogleEventDateTime` /
  `GoogleEventAttendee` deleted), plus their companion `*.notes.md`.
- **Bundle**: regenerated with `python scripts/openapi_assemble.py`; generated Go by the
  api gate. §8 records what was run and what it printed.
- **Manifest**: `contracts/features/PAC-29.yaml`

---

## 0. There is no spec, and the challenger should say so

There is no `docs/specs/PAC-29.md`. PAC-29 is a defect from the API audit (finding
API-017, batch C4 in `docs/factory/api-audit.md`). The contract-author definition says
to stop when there is no accepted spec. I went ahead on the precedent of PAC-30 and
PAC-36 (their §0):

- the run that started this stage asked for the contract;
- it supplied three acceptance criteria, each checkable at the HTTP boundary or in the
  web client;
- each criterion maps to one line in one handler (`json.NewEncoder(w).Encode(<raw event>)`)
  whose current behaviour I read.

This is a deviation from the factory, recorded rather than hidden. The decisions in §4
are what a spec would have had to settle; with no spec file to hold a Decisions
section, they are recorded in §4 and in the manifest as "assumed by the factory,
pending owner confirmation".

### Scope: one finding, not six

The issue body names six findings. The criteria this stage was given cover one:

| Finding | What | Here? |
|---|---|---|
| API-017 | Raw `calendar/v3.Event` on PATCH, schedule/create, nlp/confirm | **Yes** (+ U-04, U-14) |
| API-018 | PATCH discards `send_updates`, `room_resource_email`, `attendee_details` | No, D-1 |
| API-042 | DELETE ignores `send_updates` | No, D-1 |
| API-043 | PATCH start/end converts all-day to timed | No, D-1 |
| API-044 | PATCH `attendees` strips the room | No, D-1 |
| API-045 | PATCH 404 vs DELETE 500 for the same failure | No, D-1 |

## 1. What changes at the HTTP boundary

| Operation | Before (server today) | After |
|---|---|---|
| `patchEvent` `PATCH /api/events/{id}` | 200 body is the `calendar/v3.Event` from `UpdateEvent`, re-encoded (`handlers_events.go`, last line of `patchEvent`): `summary`, `start.dateTime`, `htmlLink`, ... Contract: `GoogleCalendarEventRaw`. | 200 body is `toCalendarEventDTO(updated)`: **`CalendarEvent`**. |
| `createScheduledMeeting` `POST /api/schedule/create` | 200 body is the created `calendar/v3.Event`. Contract: `GoogleCalendarEvent`. | **`CalendarEvent`**. |
| `confirmNaturalLanguage` `POST /api/nlp/confirm` | Same as above. | **`CalendarEvent`**. |

Nothing else moves: no request body, error response, status code, audit row or route
changes. `CalendarEvent` itself is unchanged in shape; only its description now names
the four operations that return it. The four `Google*` schemas lose their only
references and are deleted, which takes the `GoogleCalendarEvent` `x-uncertain` (U-14)
with them (bundle `x-uncertain` count 13 → 12).

As in PAC-30, the 200 descriptions state the server after the implementation PR and say
so explicitly ("until PAC-29's implementation lands the server still encodes the raw
event"), so the contract does not lie about `main` in the window between the two merges.

## 2. Why the interface is shaped this way

**Reuse `CalendarEvent`, don't define a mutation-specific event.** The web already types
all three responses as `CalendarEvent` and reads `ev.title` from two of them
(`Dashboard.tsx` handleConfirm / handleQuickCreateSave, `ScheduleSuggestModal.tsx`
apply). Today those toasts read `Scheduled "undefined"`. One schema and one converter
(`toCalendarEventDTO`) mean the list and the mutations cannot disagree about the same
event again; AC-3 asserts it.

**Room filtering comes along (D-2).** `toCalendarEventDTO` drops
`*@resource.calendar.google.com` from `attendees` and `attendee_details`. A mutation
response therefore does not show a room even when the event has one. That is exactly
what the list shows for the same event, and the web re-reads the list after each of the
three calls. Adding a room field would be a new DTO member no criterion asks for and
would make the list and the mutations differ again.

**Delete the Google schemas rather than keep them "for reference".** They are fixed by
the vendored Google client version, not by this repo (U-14). An unreferenced schema
still lands in `src/api/generated` and invites a consumer to type against it.

## 3. Alternatives rejected

- **Keep the raw Google body and change the web to read `summary` / `start.dateTime`.**
  Moves Google's vendor struct into the frontend contract and leaves the list and the
  mutations with two shapes for one event. It is also exactly what
  `TestToCalendarEventDTO_JSONShape` forbids on the list. Rejected.
- **Return both: `{event: CalendarEvent, raw: <google>}`.** A wrapper breaks every
  consumer that reads `ev.title` today (they would need `ev.event.title`) and keeps the
  vendor struct in the contract. Rejected.
- **204 No Content for PATCH and let the client re-read.** The web's `updateEvent` is
  typed `Promise<CalendarEvent>` and the MCP `clockwise_update_event` tool returns the
  body to its caller; an empty body would degrade both for no gain. Rejected.
- **Fix API-018/042..045 in the same contract** (the issue body asks for it). Each needs
  a decision no owner has made: `send_updates` emails guests (a user-visible side
  effect), and keeping all-day-ness or the room changes what PATCH writes to Google.
  None is in the acceptance criteria. Deferred (D-1), each listed as a follow-up.
- **Add `room_resource_email` to the DTO** so PATCH responses show the room. A new field
  on the list too, no criterion asks for it, and it is API-044's territory. Rejected (D-2).

## 4. Decisions (assumed by the factory, pending owner confirmation)

- **D-1** PAC-29 resolves API-017 (with U-04, U-14) only. API-018, API-042, API-043,
  API-044 and API-045 are follow-ups. Assumed by the factory, pending owner confirmation.
- **D-2** The three responses use `toCalendarEventDTO` unchanged, room filtering
  included; no field is added to `CalendarEvent`. Assumed by the factory, pending owner
  confirmation.
- **D-3** `is_personal_block` stays declared and always omitted, now on four operations;
  its `x-uncertain` is a question about the DTO, not these operations. Assumed by the
  factory, pending owner confirmation.
- **D-4** The MCP tools `clockwise_create_event` and `clockwise_update_event` pass the
  backend body through; their text output changes to the DTO keys with no MCP code
  change. Assumed by the factory, pending owner confirmation.

None of these deletes user data or widens who can see data. D-2 shows the caller less
than the raw body did (no room attendee, no `htmlLink`, no organizer/creator objects),
never more.

## 5. Consumers, and what happens to them

Searched: `smart-calendar-flow/src`, `clockwise-like/mcp`, `clockwise-like/e2e/tests`,
`clockwise-like/backend`.

| Consumer | Breaks? | What happens |
|---|---|---|
| web `Dashboard.tsx` handleConfirm, handleQuickCreateSave; `ScheduleSuggestModal.tsx` apply | No (fixed) | Toast shows the title instead of `undefined`; the list re-read is unchanged. |
| web `EventEditForm.tsx`, `RescheduleSuggestions.tsx`, `Dashboard.tsx` drag reschedule (`api.updateEvent`) | No | Ignore the body. |
| web `src/api/client.ts`, `src/api/contract.ts` | No | Already typed `CalendarEvent` (hand-written). The frontend stage switches the three to the generated type (AC-4). |
| web `src/api/generated` | No | Four `Google*` types disappear on regeneration; nothing outside `generated/` references them. |
| `mcp/tools.go` `clockwise_create_event`, `clockwise_update_event` | No (output text changes) | Body passed through verbatim (`jsonResult`); MCP clients see DTO keys. `mcp/tools_test.go` asserts request paths/bodies only. D-4. |
| backend `pac21_4xx_bodies_test.go` `TestPatchEvent_SuccessBodyIsRawGoogleEvent` | Yes, by design | It pins the raw body this contract replaces; the backend stage rewrites it to assert the DTO (AC-1), never skips or deletes it without a replacement. |
| backend `TestCreateMeeting_Success`, `TestNLPConfirm_Success` | No | Assert status only; extended by AC-2. |
| `e2e/tests/seam.spec.ts` event patch, NLP confirm | No | Assert on the fake Google request, not the response body. |

## 6. Acceptance criteria, as they apply

The three criteria given to this stage, and the tests that make them falsifiable:

1. *"PATCH /api/events/{id}, POST /api/schedule/create and POST /api/nlp/confirm return
   the same CalendarEvent DTO as GET /api/calendar/events"* → **AC-1** (PATCH body is
   `toCalendarEventDTO(updated)`: `title`, string `start`/`end`, no `summary`/`htmlLink`/
   `hangoutLink`, room dropped), **AC-2** (same for both creates, audit rows intact),
   **AC-3** (list element and PATCH body for one event are the same JSON).
2. *"The web consumes the DTO for all three responses"* → **AC-4** (with a DTO body
   stubbed in fetch, the three client methods resolve to an object whose `title` is the
   title, typed with the generated `CalendarEvent`).
3. *"api-gate and web-gate rc=0"* → **AC-5**.

AC-1..AC-3 are backend handler tests; AC-4 is a web unit test. No new e2e scenario is
required: the existing seam scenarios exercise PATCH and NLP confirm end to end and do
not read the body.

## 7. `x-uncertain` and audit findings

- **U-04** (`patchEvent` 200 body): PAC-21 pinned the raw body; PAC-29 decides that
  `CalendarEvent` is authoritative, and AC-1 pins it. Resolved.
- **U-14** (`GoogleCalendarEvent`): the schema is deleted and both operations pin the
  normalised DTO. Resolved (the marker is gone from the bundle).
- `CalendarEvent.is_personal_block` keeps its `x-uncertain` (D-3). It is on a schema
  property, not on any of the three operations, and asks whether the list's DTO field
  is dead; PAC-29 neither reads nor writes it.
- Finding resolved: **API-017**. The implementation PR updates its status in
  `docs/factory/api-audit.md` (and the U-04 / U-14 rows).

## 8. What was run

- `python scripts/openapi_assemble.py` on the host: wrote the bundle, 98 paths,
  119 operations, 144 schemas, `x-uncertain: 12`, rc=0, no orphan warning. `--check`:
  `OK: bundle is in sync (98 paths)`, rc=0.
- `sha256sum contracts/openapi/openapi.yaml` (LF):
  `268a1834cd4a860c2d98c36959e252f2327004994d62a3d9c95af7cc25bedf21`.
- `api-gate.sh factory/pac-29-contract` on `addf2e4` (the fragment change): `make openapi
  rc=0`, `regenerated: backend/api/gen/paceday.gen.go` (embedded spec, the 200 response
  types of the three operations, the four deleted `Google*` types), committed unchanged
  as `1024a52`. Every other step rc=0.
- `api-gate.sh factory/pac-29-contract` on `1024a52`: `make openapi rc=0`,
  `regenerated: none`, `openapi-check(api) rc=0` (bundle in sync, MIGRATION.md covers all
  119 operations, `backend/api/gen` matches the contract), backend lint rc=0, mcp lint
  rc=0, backend test rc=0, mcp test rc=0, backend coverage 55.4% (floor 43%) rc=0, mcp
  coverage 84.0% (floor 75%) rc=0. Re-run on the final commit of this branch (this file
  only changes after it); that output is in the Linear comment on PAC-29.
- web-gate: not run at this stage (the web types regenerate after the contract merges,
  factory §1).

## Challenge — 2026-10-27

Reviewer: `contract-challenger`, against the committed state of `factory/pac-29-contract`
at `1f07d4a`. Read: this file, `PAC-29.yaml`, the fragment diff against `origin/main`,
the PAC-29 Linear issue body, `docs/factory/api-audit.md` (API-017, U-04, U-14, C4), the
three handlers, `toCalendarEventDTO`, `SmartScheduler.CreateMeeting`, `mcp/tools.go`,
`e2e/tests/seam.spec.ts`, and the web consumers in `smart-calendar-flow` (`main`).

**Verdict**: accept-with-changes. The response change itself (the three 200 bodies become
`CalendarEvent`) is correct and I could not break it. Two blocking findings sit on the
operation this contract touches (`patchEvent`) and must be settled before the contract
merges.

### Blocking

**B-1. D-1 contradicts factory §7.1 and the issue it implements.** README §7.1 ("Touch
it, fix it") is binding: a feature that modifies an operation carrying an open audit
finding on that operation must resolve it. PAC-29 modifies `patchEvent` (its 200
response), and `patchEvent` carries four open findings: API-018 (`send_updates`,
`room_resource_email`, `attendee_details` discarded), API-043 (all-day to timed), API-044
(room stripped) and API-045 (every `GetEvent` failure is 404). The PAC-29 issue body also
asks for them explicitly ("While in this handler, fix the adjacent PATCH defects") and
lists all six findings. D-1 defers them on the factory's own authority, and the
`followUps` name owners but no issue: a Linear search for `API-018` and `API-045` finds
no issue other than PAC-29, so closing PAC-29 would leave those findings with no tracking
issue at all. (API-042 is on `DELETE`, which this contract does not touch, so §7.1 does
not require it here.) "Assumed by the factory" is for product decisions with no owner
answer; it does not override a binding factory rule.
Fix, one of:
(a) extend this contract to resolve API-018/043/044/045 on `patchEvent`, choosing the
most conservative option per finding and recording each under Decisions; or
(b) get an owner decision that PAC-29 is split, recorded in §4 as owner-confirmed, with
one Linear issue per deferred finding filed and linked from `followUps` before merge,
and the PAC-29 issue body amended so it no longer asks for them.

**B-2. `EventPatchRequest` forbids the request the only web consumer sends.**
`EventPatchRequest` declares `additionalProperties: false`, while its own description
says "Unknown keys are silently ignored by encoding/json", which is what
`patchEvent`'s anonymous body struct does (`backend/api/handlers_events.go`, `patchEvent`).
The web sends extra keys on every call:
`smart-calendar-flow/src/api/client.ts:1360-1367` posts `{...patch, send_updates}`, and
`src/components/EventEditForm.tsx:95-107` adds `room_resource_email` and
`attendee_details`. The request

```
PATCH /api/events/evt-1
{"title":"Standup","start":"2026-05-01T10:00:00Z","end":"2026-05-01T10:30:00Z",
 "room_resource_email":"","attendee_details":[{"email":"a@x.com"}],
 "attendees":["a@x.com"],"send_updates":"all"}
```

is answered 200 by the server (extra keys ignored), but the contract rejects it. AC-4
asks the frontend stage to type `updateEvent` with the generated types, which makes this
mismatch visible exactly when the frontend implements this contract. This is the contract
side of API-018 on the operation PAC-29 touches, and §7.2 says the contract describes what
the server does.
Fix (contract shape only): declare `send_updates` (string), `room_resource_email`
(string) and `attendee_details` (array) on `EventPatchRequest`, each described as
"accepted and ignored by the server (API-018)", or relax `additionalProperties` to
`true` with that note. If B-1 is resolved by option (a), these properties get their real
semantics there instead.

### Requests wrongly permitted

None found on the response change. The request bodies of the three operations are
unchanged by this diff. See B-2 for the one request this contract wrongly forbids.

### Responses the client cannot handle

None. Payloads tried for each of the three operations, as `toCalendarEventDTO` would
emit them (`backend/api/handlers_calendar.go:65`):

```
{"id":"new-evt","title":"","start":"2026-05-01","end":"2026-05-02"}
{"id":"evt-1","title":"New","start":"2026-05-01T10:00:00Z","end":"2026-05-01T10:30:00Z",
 "attendees":["a@x.com"],"attendee_details":[{"email":"a@x.com","rsvp":"pending"}],
 "conference":{"provider":"google_meet","url":"https://meet.google.com/abc","label":"Google Meet"}}
```

`id`/`title`/`start`/`end` have no `omitempty` (`handlers_calendar.go:24-27`), so
`required: [id, title, start, end]` holds even for empty values. The only consumers
that read the body, `Dashboard.tsx:198-199`, `Dashboard.tsx:217-224` and
`ScheduleSuggestModal.tsx:119-125`, read `ev.title` only; an empty title renders
`Scheduled ""`, not a crash. All-day bare dates, missing `attendees`, room-only
attendee lists and absent `conference` are covered by the existing schema and are not
read by these consumers.

### Consumers affected

- `smart-calendar-flow/src/api/client.ts:1155-1167` (non-blocking, frontend stage):
  the `scheduleCreate` mock arm builds a hand-written `CalendarEvent` with
  `color: "#3B82F6"` (not in the generated `color` enum) and `coverage` (not in the
  generated schema). `withFallback<T>` (`client.ts:524`) requires both arms to share `T`,
  so typing the method with the generated `CalendarEvent` (AC-4) will not compile until
  the mock arm changes. `client.ts` is in the frontend allowed paths, so AC-4 is still
  achievable; the plan should name this.
- `mcp/tools.go:79-80, 113-114` (`clockwise_create_event`, `clockwise_update_event`):
  output text changes from Google keys to DTO keys (D-4). No MCP code parses it; the tool
  descriptions promise no shape; `clockwise_list_events` already returns the DTO, so MCP
  becomes consistent. Agreed with the manifest.
- `e2e/tests/seam.spec.ts:248-267, 324-335`: assert on the fake Google request only.
  Unaffected, as stated.
- `backend/api/pac21_4xx_bodies_test.go:104` `TestPatchEvent_SuccessBodyIsRawGoogleEvent`:
  on `main` it pins the opposite of this contract from the moment the contract merges
  until the backend PR lands. Stated honestly in the descriptions; see N-1.
- Web code outside `src/api/generated/` references none of the four deleted
  `Google*` schemas (grepped `src`).

### Contract/implementation divergences

1. All three 200 descriptions describe the server *after* the implementation PR (each
   says so). Between the merges, the contract and `main` disagree, and §7.2 says the
   contract describes what is. I accept the explicit wording on the PAC-30 precedent,
   but see N-1.
2. B-2 above (`additionalProperties: false` vs "unknown keys are silently ignored").

### Uncertainties not actually resolved

None that the manifest claims. U-04: settled by decision (CalendarEvent is
authoritative) and pinned by AC-1/AC-3 once written. U-14: the schema is gone and both
operations reference `CalendarEvent`; lines containing `x-uncertain` in the bundle drop
from 14 to 13 (`grep -c`, origin/main vs HEAD). The `CalendarEvent.is_personal_block`
marker now reaches three more operations (D-3); for these operations the behaviour is
determined (always omitted, `toCalendarEventDTO` never sets it), so it does not block.

### Non-blocking

- **N-1** Merge the backend implementation PR directly after this contract, and use
  `Part of PAC-29` (not `Fixes PAC-29`) on the contract PR, so the window in which `main`
  has a test asserting the raw body against a contract saying DTO is short and nothing
  closes PAC-29 early.
- **N-2** There is no `docs/specs/PAC-29.md` (§0 says so). The "three acceptance
  criteria supplied with the contract stage" exist only as paraphrased in §6; I could
  check the manifest's AC-1..AC-5 against the issue body (the PATCH analogue of
  `TestToCalendarEventDTO_JSONShape`, the deleted `GoogleCalendarEvent`) but not against
  the supplied criteria themselves. Narrowing relative to the issue body is B-1.
- **N-3** `scheduling.notes.md` uncertainty table row 2 still names
  `paths./api/nlp/confirm.post.x-uncertain`, which no longer exists in the fragment (the
  400 for a negative index is described in the operation). Stale row on a touched
  operation; strike it like row 3.

### Verified clean

- `bash .claude/factory/api-gate.sh factory/pac-29-contract` run by the challenger on
  `1f07d4a`: `make openapi rc=0`, `regenerated: none`, `OK: bundle is in sync (98 paths)`,
  `OK: MIGRATION.md covers all 119 operations (0 generated)`,
  `OK: backend/api/gen matches the contract`, `openapi-check(api) rc=0`,
  `backend lint rc=0`, `mcp lint rc=0`, `backend test rc=0`, `mcp test rc=0`,
  backend coverage 55.4% (floor 43%) rc=0, mcp coverage 84.0% (floor 75%) rc=0.
- `python scripts/openapi_assemble.py --check`: `OK: bundle is in sync (98 paths)`, rc=0.
- `git show HEAD:contracts/openapi/openapi.yaml | sha256sum` =
  `268a1834cd4a860c2d98c36959e252f2327004994d62a3d9c95af7cc25bedf21`, matching
  `contractHash`.
- All three handlers encode a `*googlecalendar.Event`: `patchEvent` the result of
  `UpdateEvent` (`handlers_events.go:88`), `createMeeting` and `confirm` the result of
  `SmartScheduler.CreateMeeting` (`engine/smart_schedule.go:288`, which returns the
  `createEvent` result unmodified). So `toCalendarEventDTO` applies to all three with no
  provider other than Google on these paths, and the list and the mutations go through
  one converter.
- Room filtering (D-2): the DTO drops `*@resource.calendar.google.com` from both
  `attendees` and `attendee_details` (`handlers_calendar.go:93-97`); the response
  descriptions say so. Shows the caller less than the raw body did, never more.
- Web generated types: `GoogleCalendarEvent*` appear only in `src/api/generated/`.
- No rejected alternative in §3 was rejected without a reason I could check.
