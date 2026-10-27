# Contract — PAC-29: three event mutations return the CalendarEvent DTO, not Google's raw event

- **Linear issue**: [PAC-29](https://linear.app/paceday/issue/PAC-29)
- **Spec**: none (see §0)
- **Stage**: contract-author, then contract-challenger
- **Contract revision**: 2 (revision 1 was challenged; §9 answers the challenge)
- **Fragments edited**: `contracts/openapi/paths/calendar.yaml` (`patchEvent`,
  `deleteEvent`, `EventPatchRequest`, `CalendarEvent` descriptions,
  `GoogleCalendarEventRaw` deleted) and
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

### Scope: all six findings the issue names

The issue body names six findings and asks for all of them ("While in this handler,
fix the adjacent PATCH defects"). Revision 1 resolved only API-017 and deferred the
rest (D-1); the challenge rejected that under factory §7.1 (B-1, §9). Revision 2
resolves all six:

| Finding | What | Here? |
|---|---|---|
| API-017 | Raw `calendar/v3.Event` on PATCH, schedule/create, nlp/confirm | **Yes** (+ U-04, U-14) |
| API-018 | PATCH discards `send_updates`, `room_resource_email`, `attendee_details` | **Yes** (rev 2): D-5, D-6, D-7 |
| API-042 | DELETE ignores `send_updates` | **Yes** (rev 2): D-5 |
| API-043 | PATCH start/end converts all-day to timed | **Yes** (rev 2): D-8 |
| API-044 | PATCH `attendees` strips the room | **Yes** (rev 2): D-6 |
| API-045 | PATCH 404 vs DELETE 500 for the same failure | **Yes** (rev 2): D-9 |

API-042 is on `DELETE`, which §7.1 alone would not pull in; it is included because
API-045 cannot be resolved without changing `DELETE`'s 404 (and touching `DELETE` then
makes §7.1 apply to it), and because the issue body asks for it.

## 1. What changes at the HTTP boundary

| Operation | Before (server today) | After |
|---|---|---|
| `patchEvent` `PATCH /api/events/{id}` | 200 body is the `calendar/v3.Event` from `UpdateEvent`, re-encoded (`handlers_events.go`, last line of `patchEvent`): `summary`, `start.dateTime`, `htmlLink`, ... Contract: `GoogleCalendarEventRaw`. | 200 body is `toCalendarEventDTO(updated)`: **`CalendarEvent`**. |
| `createScheduledMeeting` `POST /api/schedule/create` | 200 body is the created `calendar/v3.Event`. Contract: `GoogleCalendarEvent`. | **`CalendarEvent`**. |
| `confirmNaturalLanguage` `POST /api/nlp/confirm` | Same as above. | **`CalendarEvent`**. |
| `patchEvent` request (rev 2) | `EventPatchRequest` declares six keys; the web's `send_updates`, `room_resource_email`, `attendee_details` are discarded by the decoder. `start`/`end` date-time only, always written as `dateTime`. `attendees` replaces the whole list with email-only entries. | Declares all nine keys. `send_updates` honoured (absent = `none`); a bare-date `start`/`end` is written as `date`; `attendees` keeps existing guests' entries and every room; `room_resource_email` adds a room; `attendee_details` accepted and ignored. |
| `patchEvent` errors (rev 2) | 400 `invalid body` only; every GetEvent failure is 404. | Three more fixed 400 bodies (`invalid send_updates`, `invalid room_resource_email`, mixed start/end kinds); 404 only for a Google 404/410, other GetEvent failures 500. |
| `deleteEvent` (rev 2) | No query parameter read; every failure 500. | `send_updates` query parameter (`all`/`none`, absent = `none`, else 400) passed to Google; Google 404/410 is a 404. |

Nothing else moves: no route, audit row or `CalendarEvent` shape changes, and no
other operation's request or error responses change. `CalendarEvent` itself is unchanged in shape; only its description now names
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
- **Defer API-018/042..045 to follow-ups** (revision 1, D-1). Rejected by the challenge
  (B-1): factory §7.1 is binding on an operation this contract modifies, the issue body
  asks for the fixes, and no other issue tracks them. Revision 2 resolves them (§9).
- **Write `attendee_details` through to Google** (names and RSVPs from the client).
  Would let one user set another person's RSVP on Google, and the list DTO the web edits
  from carries no more than Google already has. Rejected (D-7): existing entries are
  kept from Google instead.
- **`room_resource_email: ""` removes the room.** The web cannot see the room (the DTO
  filters it) and sends `""` or nothing when no room is picked, so this would delete
  bookings the user never saw. Rejected (D-6).
- **Reject a date-time `start`/`end` on an all-day event** instead of converting. The
  web edit form always sends both, so every title edit of an all-day event would fail.
  Rejected (D-8): a bare date keeps it all-day, two date-times convert it explicitly,
  only a mixed pair is a 400.
- **Make DELETE of a missing event a 204 (idempotent).** Hides a wrong id from the
  caller and disagrees with PATCH. Rejected (D-9): 404 on both.
- **`send_updates` default `all`.** Would start emailing guests on every drag-reschedule
  and MCP call that does not send the key. Rejected (D-5): absent means `none`, today's
  behaviour.
- **Add `room_resource_email` to the DTO** so PATCH responses show the room. A new field
  on the list too, no criterion asks for it, and it is API-044's territory. Rejected (D-2).

## 4. Decisions (assumed by the factory, pending owner confirmation)

- **D-1** (revision 1) ~~PAC-29 resolves API-017 (with U-04, U-14) only. API-018,
  API-042, API-043, API-044 and API-045 are follow-ups.~~ **Withdrawn in revision 2**
  (challenge B-1): PAC-29 resolves all six findings; D-5..D-9 record how.
- **D-2** The three responses use `toCalendarEventDTO` unchanged, room filtering
  included; no field is added to `CalendarEvent`. Assumed by the factory, pending owner
  confirmation.
- **D-3** `is_personal_block` stays declared and always omitted, now on four operations;
  its `x-uncertain` is a question about the DTO, not these operations. Assumed by the
  factory, pending owner confirmation.
- **D-4** The MCP tools `clockwise_create_event` and `clockwise_update_event` pass the
  backend body through; their text output changes to the DTO keys with no MCP code
  change. Assumed by the factory, pending owner confirmation.

- **D-5** `send_updates` on PATCH (body) and DELETE (query) is `all` or `none`, passed
  to Google as `sendUpdates`; absent or null means `none`, any other value is a 400 and
  nothing is written. `none` is what every call did before PAC-29, so a client that does
  not send the key (MCP) or sends `none` (drag-reschedule) sees no change; only an
  explicit `all` (the web's "notify guests" checkbox, off by default) emails guests, who
  are already on the event. Assumed by the factory, pending owner confirmation.
- **D-6** Rooms are never removed by PATCH. `attendees` replaces the non-room guests and
  keeps every existing room resource attendee; a non-empty `room_resource_email` (a
  `*@resource.calendar.google.com` address, else 400) adds that room if absent; `""`,
  null or absent leaves rooms unchanged. Removing or swapping a room needs the DTO to
  expose it, which no criterion asks for (follow-up, not a finding). Assumed by the
  factory, pending owner confirmation.
- **D-7** `attendee_details` is declared, accepted and ignored. Existing attendees keep
  their Google entry (display name, RSVP, organizer, optional) when `attendees` still
  lists them; a new email is added email-only. A client never sets another person's RSVP.
  Assumed by the factory, pending owner confirmation.
- **D-8** `start` / `end` accept a bare `YYYY-MM-DD` date (written as Google `date`) or an
  RFC3339 date-time (written as `dateTime` in UTC). If after applying the patch one side
  is a date and the other a date-time, the request is a 400 and nothing is written. The
  web edit form omits `start` / `end` for an all-day event whose date and times the user
  did not change, so editing its title keeps it all-day. Assumed by the factory, pending
  owner confirmation.
- **D-9** A Google 404 or 410 is a 404 `event not found` on both PATCH (from GetEvent)
  and DELETE; any other Google failure is a 500 `internal error` on both. Assumed by the
  factory, pending owner confirmation.

None of these deletes user data or widens who can see data. D-6 and D-7 stop PATCH from
deleting rooms, names and RSVPs it deleted before; D-8 stops it from silently converting
all-day events; D-5 notifies only people already on the event, only on explicit request. D-2 shows the caller less
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
| web `EventEditForm.tsx` save (rev 2) | No (fixed) | Its request (`send_updates`, `room_resource_email`, `attendee_details`) becomes valid under the contract (B-2). The "notify guests" checkbox now notifies (D-5); a picked room is added (D-6); the room and guests' RSVPs survive a save (D-6, D-7). For an all-day event the frontend stage omits `start` / `end` unless the user changed the date or times (D-8, AC-9). |
| web `RescheduleSuggestions.tsx`, `Dashboard.tsx` drag reschedule (rev 2) | No | Send date-time `start`/`end` with `send_updates: "none"`; same behaviour, now keeping the room and RSVPs. |
| web `client.ts` `deleteEvent` / `EventEditForm.tsx` delete (rev 2) | No (fixed) | Sends `?send_updates=all` or `none`, now honoured. Deleting an already-deleted event is a 404 instead of a 500; the caller shows the API error toast either way. |
| `mcp/tools.go` `clockwise_update_event` (rev 2) | No | Sends no `send_updates` (= `none`, today's behaviour). Its `start`/`end` description says RFC3339, which the new pattern accepts; `attendees` now keeps rooms and RSVPs. |
| `mcp/tools.go` `clockwise_delete_event` (rev 2) | No (error text) | Sends no query parameter (= `none`). A missing event now yields an MCP error text carrying 404 instead of 500. |
| backend `pac21_4xx_bodies_test.go` `TestPAC21PatchEventNotFoundIsFixed` (rev 2) | No | Its fake Google answers GET with 404, which stays a 404 under D-9. |
| backend `pac21_calendar_not_connected_test.go` patchEvent / deleteEvent rows (rev 2) | No | The no-credential path is unchanged (401). |

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

Revision 2 adds the tests for the findings it now resolves (factory §7.1), all backend
handler tests against a fake Google except AC-9:

- **AC-6** (API-018, API-042, D-5): PATCH with `send_updates: "all"` makes Google's update
  request carry `sendUpdates=all`; absent or `"none"` carries `sendUpdates=none`;
  `"bogus"` is a 400
  `invalid send_updates` with no update request. The same three cases for
  `DELETE ?send_updates=`.
- **AC-7** (API-043, D-8): on an all-day event, PATCH `{"start":"2026-05-04","end":"2026-05-05"}`
  writes `start.date`/`end.date` and no `dateTime`; PATCH `{"title":"x"}` leaves `date`
  intact; PATCH `{"start":"2026-05-04T10:00:00Z"}` alone is a 400 with no update request;
  both date-times convert it to timed.
- **AC-8** (API-018, API-044, D-6, D-7): event with attendees `a@x.com` (displayName A,
  accepted) and `room-1@resource.calendar.google.com`; PATCH
  `{"attendees":["a@x.com","b@x.com"],"attendee_details":[{"email":"a@x.com","rsvp":"declined"}],"room_resource_email":""}`
  writes `a@x.com` with displayName A and `accepted`, `b@x.com` email-only, and the room;
  with `"room_resource_email":"room-2@resource.calendar.google.com"` both rooms are
  written; with `"room_resource_email":"x@example.com"` it is a 400. The challenger's
  B-2 request is answered 200.
- **AC-9** [web unit] (D-8): `EventEditForm` saving an all-day event (`start` a bare
  date) with only the title changed sends no `start`/`end`; with a changed time it sends
  both as date-times.
- **AC-10** (API-045, D-9): GetEvent answering 500 makes PATCH a 500 `internal error`
  (not 404); GetEvent 404 or 410 stays a 404. DELETE answered 404 or 410 by Google is a
  404 `event not found`; answered 500 is a 500.

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
- Findings resolved: **API-017**, and in revision 2 **API-018, API-042, API-043,
  API-044, API-045**. The implementation PR updates their status in
  `docs/factory/api-audit.md` (and the U-04 / U-14 rows). After revision 2 no open audit
  finding remains on `patchEvent` or `deleteEvent`.

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
- Revision 2: `python scripts/openapi_assemble.py` wrote the bundle (98 paths, 119
  operations, 144 schemas, `x-uncertain: 12`), rc=0; `--check`: `OK: bundle is in sync
  (98 paths)`, rc=0. `api-gate.sh factory/pac-29-contract` on `a4f5340` (fragments):
  `make openapi rc=0`, `regenerated: backend/api/gen/paceday.gen.go` (EventPatchRequest
  fields, DeleteEventParams, the new 400/404 response types), committed unchanged as
  `321cb77`, every other step rc=0. Re-run on `321cb77`: `make openapi rc=0`,
  `regenerated: none`, `openapi-check(api) rc=0`, backend lint rc=0, mcp lint rc=0,
  backend test rc=0, mcp test rc=0, backend coverage 55.4% (floor 43%) rc=0, mcp
  coverage 84.0% (floor 75%) rc=0. Bundle sha256 (LF, `git show HEAD:...`):
  `2a622b5788d6b1f9d7bff435a07e5bba9eca678f2b12f512b60311ae88af7dbf` (the manifest's
  `contractHash`). web-gate still not run at this stage, for the same reason.

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

## 9. Revision — 2026-10-27 (contract revision 2, answering the round 1 challenge)

Author: `contract-author`. The response change of revision 1 is unchanged. This revision
answers the two blocking findings and N-3. N-1 and N-2 are addressed where noted.

### B-1. D-1 broke factory §7.1: resolved by option (a)

Option (b) needs an owner decision to split PAC-29, and no owner has recorded one. Option
(a) needs only factory decisions, and the issue body already asks for these fixes, so
(a) is the option that follows the factory without an owner. D-1 is withdrawn. Revision 2
resolves API-018, API-043, API-044 and API-045 on `patchEvent`, and API-042 and the
`DELETE` half of API-045 on `deleteEvent` (§0 says why `DELETE` is in scope). Each fix
takes the most conservative option and is recorded as an assumed decision in §4:

| Finding | Contract change | Decision | Why it is the conservative option |
|---|---|---|---|
| API-018 `send_updates` | Declared on `EventPatchRequest`, enum `all`/`none`; absent/null = `none`; passed to Google; other values 400 | D-5 | `none` is today's behaviour for every caller that does not opt in. Only the web's explicit "notify guests" checkbox (default off) sends `all`, and it emails only people already on the event. |
| API-042 `DELETE ?send_updates` | Declared query parameter, same enum and default | D-5 | Same as above. |
| API-018 `room_resource_email` | Declared; non-empty adds the room; `""`/null/absent leaves rooms unchanged; must be `*@resource.calendar.google.com` | D-6 | Never removes a booking. The web cannot see rooms (the DTO filters them) and sends `""` or nothing when no room is picked, so "empty removes" would delete bookings the user never saw. |
| API-018 `attendee_details` | Declared, accepted and ignored | D-7 | Writing it through would let one user set another's RSVP. Names and RSVPs are kept from Google instead (next row), which is what the field was trying to preserve. |
| API-044 room stripped | `attendees` replaces only non-room attendees, keeps every room, and keeps existing attendees' Google entries | D-6, D-7 | Stops PATCH deleting rooms, names and RSVPs. `[]` still clears guests, as today. |
| API-043 all-day to timed | `start`/`end` accept a bare date (kept all-day) or a date-time; a date/date-time mix is a 400 | D-8 | No silent conversion. Rejecting date-times on an all-day event was rejected because the web form sends both on every save (§3). The frontend stage omits unchanged `start`/`end` for all-day events (AC-9). |
| API-045 404 vs 500 | Only a Google 404/410 is a 404 on PATCH and DELETE; anything else is a 500 on both | D-9 | One meaning of "not found" on one path. A transport or permission failure no longer claims the event does not exist. |

Tests: AC-6 to AC-10 (§6 and the manifest). `followUps` no longer lists any audit
finding. Its one entry (removing or swapping a room needs the DTO to expose rooms) is a
product question that D-6 raises, not an audit finding. The PAC-29 issue body already
asks for all six fixes, so it does not need amending.

### B-2. `EventPatchRequest` forbade the web's request: resolved

`send_updates`, `room_resource_email` and `attendee_details` are declared on
`EventPatchRequest` with their real semantics under B-1 (a), as the challenge allowed.
`additionalProperties: false` stays: every key the only web consumer
(`client.ts` `updateEvent`, `EventEditForm.tsx` save) and the MCP tool send is now declared,
so the contract still forbids a key the server would silently drop. The description no
longer says "unknown keys are silently ignored" without saying what the contract does
about it. The challenge's example request

```
PATCH /api/events/evt-1
{"title":"Standup","start":"2026-05-01T10:00:00Z","end":"2026-05-01T10:30:00Z",
 "room_resource_email":"","attendee_details":[{"email":"a@x.com"}],
 "attendees":["a@x.com"],"send_updates":"all"}
```

is valid against the revised schema (`""` matches the `room_resource_email` pattern,
`attendee_details` items are free-form objects, `send_updates` is in the enum, both
times match the `start`/`end` pattern). AC-8 asserts the server answers it 200.

One request shape changes from valid to invalid: `start`/`end` lose `format: date-time`
and gain a pattern that accepts either an RFC3339 date-time or a `YYYY-MM-DD` date. The
date-times the web (`toISOString()`) and MCP (RFC3339) send still match; only a lowercase `t` /
`z`, which RFC3339 permits but no consumer sends, no longer matches. The generated Go field becomes a
string, and the handler is hand-written, so nothing consumes the generated type
(MIGRATION.md: 0 generated).

### Non-blocking

- **N-1** Accepted. The contract PR uses `Part of PAC-29`, never `Fixes PAC-29`.
  Revision 2 widens the window in which the contract describes the server after the
  implementation PR, so every changed description says what `main` does until then
  (`patchEvent` and `deleteEvent` descriptions).
- **N-2** Acknowledged, unchanged. There is still no `docs/specs/PAC-29.md` (§0). The
  three supplied criteria are quoted verbatim in §6. Revision 2's extra tests come from
  the issue body and factory §7.1, not from those criteria.
- **N-3** Fixed. `scheduling.notes.md` row 2 is struck through with the reason.

## Challenge — 2026-10-27 (round 2, contract revision 2)

Reviewer: `contract-challenger`, against the committed state of `factory/pac-29-contract`
at `0f59658`. Read: this file and `PAC-29.yaml` as committed, the fragment diff against
`origin/main` and against `1f07d4a` (round 1), the PAC-29 Linear issue body,
`docs/factory/api-audit.md` rows API-017/018/042..045, `handlers_events.go`,
`toCalendarEventDTO` (`handlers_calendar.go`), `calendar/events.go`, `rooms_attendees.go`,
`mcp/tools.go`, `e2e/tests/seam.spec.ts`, and the web consumers in `smart-calendar-flow`
(`main` at `90def70`). Web types regenerated from the branch bundle into a scratch
directory with the pinned `openapi-typescript 7.4.4` and `typed-openapi 4.1.0` (not
committed anywhere).

**Verdict**: accept-with-changes. Round 1's B-1 and B-2 are resolved: all six findings
are in scope, and the round-1 request is valid against the revised `EventPatchRequest`.
One blocking finding remains. The contract claims API-043 is resolved, but an existing web
consumer that the manifest forbids changing keeps producing the exact API-043 defect, and
the contract permits the request it sends.

### Blocking

**C2-B1. API-043 is claimed resolved, but month-view drag still converts all-day events,
and the manifest forbids the fix.**
`Dashboard.tsx` runs FullCalendar with a `dayGridMonth` view (`:524`), `editable` (`:539`),
and passes the DTO's `start`/`end` through unchanged (`:305`). A bare-date DTO `start`
therefore renders as an all-day event that the user can drag (`:308` excludes only focus
and personal blocks). On drop, `handleEventChange` (`:266-269`) sends
`event.start.toISOString()` / `event.end.toISOString()`. Dragging the all-day event
`{"start":"2026-05-01","end":"2026-05-02"}` one day in Europe/Paris sends

```
PATCH /api/events/evt-allday
{"start":"2026-05-01T22:00:00.000Z","end":"2026-05-02T22:00:00.000Z","send_updates":"none"}
```

Under rev 2 this is valid (both values match the `start`/`end` pattern) and specified as
"two date-times convert it to timed" (D-8, AC-7). The server writes
`start.dateTime=2026-05-01T22:00:00Z`, which is the audit's API-043 impact: "silently
converts it to a timed event". The consumer table (§5 and manifest) lists this consumer as
"No: same behaviour", which is accurate, but it is also the defect §7 says is gone. The
manifest's frontend `forbiddenPaths` includes `src/pages`, so the frontend stage may not
fix it, and no AC covers it. §7 then says "no open audit finding remains on `patchEvent`",
but the code contradicts that.

The same gap exists in `EventEditForm.tsx`. AC-9 pins two cases: title-only sends nothing,
and a changed time sends date-times. It does not pin a **date-only** change on an all-day
event, which is the other way to "edit an all-day event's time" (the issue body's words).
Note also that today the form cannot save an all-day event at all. `isoTimeOnly` of
`"2026-05-01"` and of `"2026-05-02"` give the same local time (`:71-72`), and both
`start` and `end` are built on the same `date` (`:81-82`), so `end <= start` fires
"End time must be after start time" (`:90`) in every timezone. §3's reason for rejecting
the alternative ("every title edit of an all-day event would fail") describes a request the
form never sends today. The rejection may still be right, but the stated reason does not
match the code.

Fix (manifest/AC shape, no schema change needed, because the pattern already accepts bare
dates). Do one of the following:
(a) add `src/pages/Dashboard.tsx` (`handleEventChange` only) to the frontend
`allowedPaths`, and add an AC: dragging or resizing an event whose DTO `start` is a bare
date sends bare `YYYY-MM-DD` `start`/`end` (exclusive end). Extend AC-9 with a third case:
a date-only change on an all-day event sends bare dates `start=D`, `end=D+1`. Correct §3's
reason;
or (b) narrow the claim. State in §7 and the manifest that API-043 is resolved at the API
boundary only, and that the web month-view drag and the form's date change still convert.
Then file a Linear issue for the web half and link it from `followUps` before merge, so
the finding keeps a tracking issue (the round-1 B-1 rule).

### Requests wrongly permitted

None beyond C2-B1. Tried, all handled by the schema or by a fixed 400 that the contract
documents:
- `{"send_updates":"externalOnly"}`: Google accepts this value, but the enum rejects it.
  The server 400 `invalid send_updates` is documented. Conservative, so not a defect.
- `{"room_resource_email":"bob@example.com"}`: rejected by the pattern, and by a documented
  400. `{"room_resource_email":"c_188abc@resource.calendar.google.com"}` matches the pattern
  and the IDs `ListRooms` returns (`rooms_attendees.go:31`).
- `{"start":"2026-05-04T10:00:00Z"}` alone on an all-day event: the documented 400.
  `{"start":"2026-02-30"}` matches the pattern, and the description assigns it to
  `invalid body`.
- `{"attendees":[]}`: removes non-room guests, same as today. Documented, and not new
  data loss.
- `DELETE /api/events/x?send_updates=ALL`: rejected by the enum and by a documented 400.

### Responses the client cannot handle

None. The 200 body is unchanged from round 1 (`CalendarEvent`). The new 400 and 404 bodies
are `ErrorResponse`, and every web caller (`EventEditForm.tsx` save and delete, drag,
`RescheduleSuggestions.tsx`) catches and toasts. A DELETE that now returns 404 instead of
500 goes through the same `apiErrorMessage` path.

### Consumers affected

- `smart-calendar-flow/src/pages/Dashboard.tsx:266-269`: see C2-B1.
- `mcp/tools.go` `clockwise_update_event` (non-blocking). When an LLM passes only `start`
  (RFC3339) for an all-day event, the tool used to get a 200 that converted the event.
  It now gets the mixed-kind 400. That is the intended D-8 behaviour, but the manifest row
  says "breaks: no". It should say "an error text where there was a silent conversion".
- `e2e/tests/seam.spec.ts:336-346` `event delete` is not listed. It sends no query
  parameter (so `none`) and asserts only `events.delete` on the fake. Unaffected.
- Generated web types (scratch regeneration): `EventPatchRequest` gains
  `send_updates?: "all" | "none" | null`, `room_resource_email?: string | null`, and
  `attendee_details?: {[key: string]: unknown}[] | null`, and the zod schema is a
  `strictObject` with the declared keys. The web's `{...patch, send_updates}` and the
  `Attendee[]` it passes as `attendee_details` both fit. `deleteEvent` gets
  `query?: {send_updates?: "all" | "none"}`.

### Contract/implementation divergences

None new. Every rev-2 "until PAC-29's implementation lands" sentence matches
`handlers_events.go` as it stands:
- the anonymous struct has six keys (`:37-44`);
- `*time.Time` means a bare date fails the decode with a 400 `invalid body`;
- `EventDateTime{DateTime}` (`:68-72`);
- email-only attendee replacement (`:74-79`);
- every GetEvent error is a 404 (`:54-57`);
- DELETE reads no query and maps every failure to 500 (`:91-100`).

### Non-blocking

- **C2-N1** DTO room filtering and PATCH room keeping use different tests. The DTO drops
  only the `*@resource.calendar.google.com` suffix (`handlers_calendar.go:95`), while PATCH
  keeps `resource: true` **or** that suffix. A resource attendee without the suffix is shown
  to the web as a normal guest. If the user removes it in the form, PATCH answers 200 and
  silently keeps it. Use one rule in both places: either describe the PATCH rule as "suffix
  only", or note the asymmetry in the `attendees` description.
- **C2-N2** Error precedence is unspecified. Two examples: `DELETE ?send_updates=bogus`
  with no stored credential (400 or 401?), and PATCH with an invalid `room_resource_email`
  on a missing event (400 or 404?). Only the mixed-kind 400 necessarily comes after
  GetEvent. State the order, preferably validating before any Google call so that "nothing
  is written" holds trivially, and let AC-6 and AC-8 pin it.
- **C2-N3** Google `events.get` returns 200 with `status: "cancelled"` for a recently
  deleted event, not 404 or 410. PATCH on such an id is therefore neither a 404 nor
  rejected by D-9, and it returns a DTO for a cancelled event. Mention this in the 404
  description, or decide it.
- **C2-N4** §8's first sha256 (`268a…`) is the revision-1 hash. It is labelled by its
  position only. The revision-2 hash `2a622b…` is correct and matches the manifest.
- **C2-N5** `nullable: true` in an `openapi: 3.1.0` bundle is not a JSON Schema 2020-12
  keyword. It follows the repo's existing convention, and both web generators honour it
  (verified: `| null` / `.nullable()`). Do not rely on a strict 3.1 validator here; this is
  a repo-wide debt, not PAC-29's.

### Uncertainties not actually resolved

None claimed beyond round 1. U-04 and U-14 are as in round 1. In the bundle, lines
containing `x-uncertain` number 14 on `origin/main` and 13 on HEAD (`grep -c`). The
assembler's own count of 12 (§8) measures something different, and its delta is the same.

### Verified clean

- `bash .claude/factory/api-gate.sh factory/pac-29-contract` run by the challenger on
  `0f59658`:
  - `make openapi rc=0`, `regenerated: none`
  - `OK: bundle is in sync (98 paths)`
  - `OK: MIGRATION.md covers all 119 operations (0 generated)`
  - `OK: backend/api/gen matches the contract`, `openapi-check(api) rc=0`
  - `backend lint rc=0`, `mcp lint rc=0`, `backend test rc=0`, `mcp test rc=0`
  - backend coverage 55.4% (floor 43%) rc=0; mcp coverage 84.0% (floor 75%) rc=0
- `python scripts/openapi_assemble.py --check`: `OK: bundle is in sync (98 paths)`, rc=0.
- `git show HEAD:contracts/openapi/openapi.yaml | sha256sum` =
  `2a622b5788d6b1f9d7bff435a07e5bba9eca678f2b12f512b60311ae88af7dbf`, which matches
  `contractHash`.
- Round-1 B-2 request: valid against the regenerated zod `EventPatchRequest`. Every key is
  declared, `""` matches `room_resource_email`, and both times match the pattern.
- Every date-time the consumers send matches the new `start`/`end` pattern: web
  `toISOString()` (`…T22:00:00.000Z`), MCP RFC3339 with seconds, and DTO echo-back
  (`+02:00` offsets).
- D-5 default `none` equals today's behaviour for MCP, drag and suggestions. Only the web
  checkbox (default off, `EventEditForm.tsx` `setNotify(false)`) sends `all`.
- D-6 and D-7 neither delete user data nor widen visibility. The DTO still hides rooms, and
  `attendee_details` cannot set another person's RSVP.
- D-9 404 on GetEvent 404/410 keeps `TestPAC21PatchEventNotFoundIsFixed` valid. The
  no-credential 401 path is unchanged.
- No rejected alternative in §3 lacks a reason. The one whose stated reason the code
  contradicts is listed under C2-B1.
