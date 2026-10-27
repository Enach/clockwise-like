# Spec — PAC-41: Room contract mismatch, and free/busy coverage states the server never produces

- **Linear**: https://linear.app/paceday/issue/PAC-41
- **Status**: draft
- **Author**: spec-author
- **Read on**: api `main` at `6d751f9`, web `main` at `ad52160` (2026-10-27)
- **Inherited scope**:
  - **Resolved by this spec**: API-040 (`docs/factory/api-audit.md:298`, room
    search sends a time window the server ignores), API-041 (`:299`, the web room
    type requires fields the server never sends), API-046 (`:304`, the web
    coverage type declares a provider and a third status the server never emits).
    All three are on the room-list and free/busy-query operations.
  - **Already fixed, not inherited**: API-074 (`:332`, Fixed (PAC-23)).
  - **Split out of this spec** (D-4, OQ-3): API-047 (`:305`, the org member list
    answers 200 `[]` on a lookup error), API-075 (`:333`, a personal calendar is
    created disabled when `enabled` is omitted), API-078 (`:336`, room resources
    appear in attendee suggestions). They sit on three other operations, share no
    behaviour with the room/coverage mismatch, and one of them would pull in an
    `x-uncertain` marker this work has no other reason to touch (see D-4).
  - **`x-uncertain`**: none on the room-list or free/busy-query operations, nor on
    the room or free/busy participant schemas (checked in
    `contracts/openapi/openapi.yaml` on `6d751f9`; the nearest markers, at `:6245`,
    `:6433` and `:8448`, sit on `CalendarEvent`, `ConferenceMeetingDetails` and
    `OrgMember`).
  - **Shared with PAC-47** (spec on `factory/pac-47-spec`, not merged): PAC-47
    also lists API-046 as inherited scope on the free/busy-query operation. See
    OQ-2.

## 1. Problem

A user editing an event can switch the location to "Book a room", search, and pick
a conference room. Against the real server, three things go wrong at once:

1. **The room is never booked.** The picker writes the room's name into the
   location and takes the room's address from a field the live room list does not
   carry. The address goes out empty, the server adds no room to the event and
   answers success, and the user sees a room name on an event that holds no room.
   Nobody else sees the room as taken; two meetings can "have" the same room.
2. **Every room is shown as "✓ Available".** The web decides availability from a
   field only demo data carries; when it is missing, the row reads as available.
   The badge is a claim the product never checked.
3. **Each row has an empty detail line** where building, floor and capacity
   would go — fields only demo data carries.

Separately, the scheduling-link drawer's co-host chips are written against a
coverage answer with three states and a provider mark. The server produces two
states and no provider, so the third state ("this person is on Paceday", read
from free/busy) and the Google/Microsoft mark on "Availability synced" can never
appear outside demo mode. That code is dead against the real server, and demo
mode shows visitors a product that does not exist.

The cost is trust: a user who books a room through Paceday and turns up to find
it taken has learnt that Paceday's scheduling cannot be relied on.

## 2. Current behaviour

### 2.1 What the server sends for a room

- The server's room is two fields, `id` and `name`
  (`backend/calendar/rooms_attendees.go:11-14`).
- Rooms are the caller's Google calendar-list entries whose id contains
  `resource.calendar.google.com`, filtered by an optional case-insensitive
  substring of the summary or id (`rooms_attendees.go:25-40`). The `id` is the
  room's resource calendar address, which is also its bookable email.
- The handler reads only `q`; any other query parameter is ignored
  (`backend/api/handlers_events.go:311-328`).
- The contract already says exactly this: `CalendarRoom` is `{id, name}`, both
  required, `additionalProperties: false`
  (`contracts/openapi/paths/calendar.yaml:1577-1590`), and the room-list operation
  declares only `q` (`calendar.yaml:368-405`). The web's generated types carry it
  as `CalendarRoom { id: string; name: string }`
  (`smart-calendar-flow/src/api/generated/types.ts:2727-2732`); no web code uses
  that generated type today.

### 2.2 What the web believes a room is

All web citations are `smart-calendar-flow` at `ad52160`.

- Hand-written `Room` with `email` **required** plus optional `building`, `floor`,
  `capacity`, `available` (`src/api/types.ts:159-167`).
- Demo rooms carry all of them (`src/api/client.ts:136-144`); demo search also
  matches on building and email, computes `available` from demo events, and marks
  two rooms busy "for demo flair" (`client.ts:1416-1446`).
- Room search sends `q`, `start` and `end` (`client.ts:1418`); the server ignores
  the last two (API-040).
- The room picker (`src/components/LocationPicker.tsx`):
  - reports `room.email` as the event's room address on pick (`:74`), so against
    the live server the address is `undefined`;
  - re-finds an existing room by searching the local part of the address and
    matching `r.email` (`:35-36`), which never matches live;
  - renders building/floor/capacity (`:132-134`, `:180-182`) and an
    "✓ Available" / "✗ Busy" badge that disables busy rows (`:172-193`); with no
    `available` field every row is enabled and labelled available;
  - invites search "by name or building" (`:154`); live search matches name or
    address only.
- The event form sends the picker's room address on save
  (`src/components/EventEditForm.tsx:141`). The server adds a room only when that
  field is non-empty and matches `^[^@\s]+@resource\.calendar\.google\.com$`
  (`backend/api/handlers_events.go:48,134-140,189-191`). An absent field adds
  nothing and the save succeeds.

### 2.3 What the server sends for free/busy coverage

- Each participant is `{email, status, busy}`
  (`backend/api/handlers_freebusy.go:23-27,83-87`); there is no provider field.
- `status` is the engine's coverage, which is only ever `"unknown"` or `"known"`
  (`backend/engine/freebusy_service.go:34,93,143-151`).
- The contract already says so: `FreeBusyParticipant.status` is
  `enum: [known, unknown]` with `additionalProperties: false`
  (`calendar.yaml:1522-1543`), and the generated web type matches
  (`src/api/generated/types.ts:3104-3117`).

### 2.4 What the web believes coverage is

- `CoverageStatus = "paceday_user" | "known" | "unknown"` and
  `ParticipantCoverage { email; status; provider?: "google" | "outlook" | "paceday" }`
  (`src/api/types.ts:247-263`).
- Demo free/busy classifies by domain and produces all three statuses and a
  provider (`client.ts:845-866`); the demo coverage summary counts `paceday_user`
  as checked (`client.ts:873-884`).
- The coverage cache copies `provider` through (`src/api/coverageCache.ts:103-119`).
- The co-host chip maps `paceday_user` to "On Paceday" and `known` to
  "Availability synced" with the provider mark
  (`src/components/links/LinkEditDrawer.tsx:193-194`;
  `src/components/links/CoverageBadge.tsx:10-27,72-80`). "On Paceday" is *also*
  reached, independently, from the link's own host record for a co-host who has
  accepted (`LinkEditDrawer.tsx:184-188`) — that path reads no coverage data and
  is real.

### 2.5 What protects this today

- No web test covers the room picker, room search, the coverage cache, the
  coverage badge or the co-host chip mapping (searched `src/**/*.test.*` on
  `ad52160`: no test references `searchRooms`, `LocationPicker`, `CoverageBadge`,
  `getCoverageMap` or `ParticipantCoverage`).
- The e2e suite calls the room list only as a seam check
  (`e2e/tests/seam.spec.ts:348-352`) and the free/busy query in
  `e2e/tests/calendar.spec.ts:195-217` and `seam.spec.ts:163-176`. None drives the
  room picker. The Google stand-in already serves room calendars in the calendar
  list (`e2e/fake-google/calendarlist.go:11-12`, `fixture.go:46`).
- Backend: the room list's failure bodies are covered
  (`backend/api/pac21_calendar_not_connected_test.go`,
  `pac21_error_bodies_test.go`); the free/busy wire keys are covered
  (`backend/api/pac26_wire_keys_test.go`).

## 3. Desired behaviour

The server is the reference; the web is brought to it. No new room data and no
new coverage information are invented (D-1, D-2).

- **A room the user picks is a room the event holds.** Picking a room and saving
  adds that room to the event on the user's calendar, using the room's address as
  the server reports it. When the form already holds a room address and the room
  list contains that room, the picker shows it as selected.
- **The room list claims only what it knows.** Each room shows its name. It shows
  no availability verdict, no building, floor or capacity line, and no empty
  placeholder for them. Every listed room can be picked. The search hint describes
  what search actually matches.
- **Room search asks only for what the server answers.** It sends the search text
  and nothing that implies a time-window filter.
- **Coverage has two states.** A participant's calendar is either readable
  ("Availability synced") or not (unknown). The "synced" chip carries no provider
  mark. "On Paceday" remains, reached only from the scheduling link's own host
  record, as today.
- **Demo mode shows the same product.** Demo rooms have a name and an address and
  nothing else; demo coverage uses the same two states and no provider. Demo mode
  does not display a capability the live product lacks.
- **One definition.** The web's room and coverage shapes are the ones generated
  from the contract; no hand-written copy remains that disagrees with it.
- The server's behaviour does not change.

## 4. Acceptance criteria

AC-1. Given a signed-in Google user whose calendar list contains a room resource
named "Helsinki", when they open an event, choose "Book a room", pick "Helsinki"
and save, then the event stored on their calendar has the Helsinki room address
as a resource attendee. *(Fails today: the save carries no room address and the
stored event has no room.)*  [e2e]

AC-2. Given the room list returns rooms carrying only an identifier and a name,
when the user picks one, then the change the picker reports to the form carries a
room address equal to that room's identifier and location text equal to its name.
*(Fails today: the address is undefined.)*  [unit]

AC-3. Given the room list returns rooms carrying only an identifier and a name,
when the list renders, then no row shows an availability verdict ("Available" or
"Busy"), no row is disabled, and no row renders a secondary detail line.
*(Fails today: every row reads "✓ Available" and has an empty detail line.)*
[unit]

AC-4. Given the form already holds a room address and the room list contains a
room whose identifier equals it, when the picker opens in room mode, then that
room is shown as selected. *(Fails today: the match is made on a field the live
list does not carry.)*  [unit]

AC-5. Given the user types a search in room mode on an event with a start and an
end, when the search request is made, then it carries the search text and no
start or end of a time window. *(Fails today: both are sent.)*  [unit]

AC-6. Given the web code base, when it is type-checked, then the room type every
room consumer accepts is mutually assignable with the contract-generated room
type, so a room with only an identifier and a name is accepted everywhere and a
hand-written shape with an extra required field fails the check. *(Fails today:
the hand-written room requires an address field the generated one lacks.)*  [unit]

AC-7. Given the web code base, when it is type-checked, then the coverage status
type admits exactly the two values the contract declares, and the participant
coverage type has no provider member — both asserted by type-level tests against
the generated participant type. *(Fails today: a third status and a provider are
declared.)*  [unit]

AC-8. Given a free/busy answer in which a co-host's status is "known", when the
scheduling-link drawer renders that co-host (not an accepted host on the saved
link), then the chip reads "Availability synced" and shows no provider mark; given
"unknown", the chip shows the unknown state.  [unit]

AC-9. Given a scheduling link whose saved host record shows a co-host as accepted
with a Paceday account, when the drawer renders that co-host, then the chip reads
"On Paceday" whatever the free/busy answer says. (Holds today; this pins it.)
[unit]

AC-10. Given demo mode, when room search and free/busy are called, then every demo
room carries only an identifier and a name, and every demo coverage status is
"known" or "unknown" with no provider. *(Fails today.)*  [unit]

AC-11. Given the audit register after this work merges, then API-040, API-041 and
API-046 are marked Fixed with a reference to PAC-41, and API-047, API-075 and
API-078 remain open and name the follow-up issue (OQ-3).  [contract]

Gate requirement (from the ticket; not a behaviour): every gate the
implementation touches — the api gate if the contract or audit register is
edited, the web gate, and the e2e gate for AC-1 — prints rc=0 on the final
commits.

## 5. Explicitly out of scope

- **Room metadata (building, floor, capacity) and room availability.** Either
  would be a new feature reading data the server does not fetch today, likely
  needing broader Google directory access. Adding it is an owner decision (OQ-1),
  not a contract fix.
- **A provider mark on coverage, or an "is on Paceday" coverage state.** Same
  reason; OQ-1 and D-2.
- **Showing an event's existing room when the event is loaded from the server.**
  The event the server returns drops room attendees (contract `CalendarEvent`
  attendee description), so the form never holds a room address for a live event
  it did not just set. AC-4 covers the case where the form holds one; making
  loaded events carry their room is a separate change.
- **A newly added co-host who is a Paceday user showing "On Paceday" before
  accepting.** Live behaviour already cannot show this; nothing here makes it worse.
- **API-047, API-075, API-078** (D-4, OQ-3).
- **Outlook and WebCal free/busy.** PAC-47.

## 6. What must not change

- The server's room list and free/busy answers: same fields, same statuses, same
  error bodies. Protected by `backend/api/pac26_wire_keys_test.go`,
  `pac21_calendar_not_connected_test.go`, `pac21_error_bodies_test.go`,
  `handlers_freebusy_test.go`, and the seam and free/busy e2e tests in §2.5.
- Room address validation on event save (`handlers_events.go:48,189-191`),
  covered by the PATCH tests PAC-29 added.
- Free-text location: choosing "Free text" still sends no room address.
  **No test protects this today** — a finding; a test belongs beside AC-2.
- "On Paceday" and "Invite pending" chips from the host record (AC-9). **No test
  protects this today.**
- Busy-window handling used by teams and slot filtering
  (`coverageCache.ts:137-164`, `src/api/teams.ts:11`): only the status type
  narrows; "unknown" is still excluded.

## 7. Rollback

No migration and no server change. Reverting the web PR restores the hand-written
types and the "Available" badge. A contract-side change (descriptions, audit
register) reverts independently. Rooms booked while the change was live stay on
their events after a revert; nothing is deleted.

## 8. Open questions

- **OQ-1 — does the product want room details or room availability?** Owner:
  product owner. Resolve by a yes/no on the Linear issue. Until answered, D-1 to
  D-3 apply. A "yes" is a new feature with its own spec.
- **OQ-2 — PAC-47 also inherits API-046.** Owner: whichever of PAC-41 and PAC-47
  reaches the contract stage second. Resolve by reading the audit register: if
  API-046 is already Fixed, the second must keep it fixed and must not reintroduce
  a third status or a provider field for Outlook or WebCal coverage.
- **OQ-3 — follow-up issue for API-047, API-075, API-078.** Owner: product owner
  (the factory comments and moves status in Linear but does not create issues).
  Resolve by filing one PAC issue in project "API contract hardening" carrying the
  three findings, and linking it here and in the audit register.
- **OQ-4 — can AC-1 be observed in e2e?** The Google stand-in stores events
  (`e2e/fake-google/events.go`); whether it keeps a resource attendee's flag on
  insert or patch was not verified. Owner: e2e-author at stage 5. If it does not,
  the stand-in gains that (stand-in code, not product code).

## Decisions

*None had a recorded owner answer. Each is the most conservative option consistent
with PRODUCT.md and the earlier owner decisions in docs/specs/\*.md, and each is
**assumed by the factory, pending owner confirmation**.*

- **D-1 — the web shrinks to the server's room; the server does not grow.**
  Assumed by the factory, pending owner confirmation. Server and contract already
  agree on `{id, name}`; growing them would add a feature (and possibly broader
  Google access, i.e. more data read) to fix a type mismatch. The room's
  identifier is its bookable address, so nothing the user needs is lost.
- **D-2 — drop the third coverage status and the provider mark rather than
  implement them.** Assumed by the factory, pending owner confirmation. The server
  produces neither. Implementing "on Paceday" from free/busy would tell a caller
  whether an arbitrary address has a Paceday account, which widens what one user
  can learn about others. The real "On Paceday" chip, from the link's own host
  record, stays.
- **D-3 — remove the availability badge rather than show it unverified.** Assumed
  by the factory, pending owner confirmation. A badge that says "Available" for
  every room is worse than none; PRODUCT.md principle 3 asks that what the product
  does be visible and never silent, and it must not claim what it did not check.
- **D-4 — split API-047, API-075 and API-078 out.** Assumed by the factory,
  pending owner confirmation. They sit on the org-member list, personal-calendar
  create and attendee-suggest operations, none of which this spec changes. Under
  touch-it-fix-it, changing the org-member list would also inherit the `OrgMember`
  `x-uncertain` (`openapi.yaml:8448`). Bundling them makes this change harder to
  revert for no shared benefit.
- **D-5 — demo mode follows the live shapes.** Assumed by the factory, pending
  owner confirmation. Demo rooms lose building, floor, capacity and the invented
  busy rooms; demo coverage loses "on Paceday" and the provider. PRODUCT.md calls
  demo mode "the only built-in way to show the product", so it shows the product
  that exists.
