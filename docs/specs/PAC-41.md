# Spec — PAC-41: Room contract mismatch, and free/busy coverage states the server never produces

- **Linear**: https://linear.app/paceday/issue/PAC-41
- **Status**: challenged (Revision 1 answers the 2026-10-27 challenge)
- **Author**: spec-author
- **Read on**: api `main` at `6d751f9`, web `main` at `ad52160` (2026-10-27;
  both re-checked unchanged for Revision 1)
- **Inherited scope**:
  - **Resolved by this spec**: API-040 (`docs/factory/api-audit.md:298`, room
    search sends a time window the server ignores), API-041 (`:299`, the web room
    type requires fields the server never sends), API-046 (`:304`, the web
    coverage type declares a provider and a third status the server never emits).
    All three are on the room-list and free/busy-query operations, and all three
    are resolved on the web side: the server and the contract already describe
    the right shapes (§2.1, §2.3).
  - **Already fixed, not inherited**: API-074 (`:332`, Fixed (PAC-23)), API-028
    (`:286`, Fixed (PAC-26), mixed key casing in the free/busy-query answer).
  - **Not inherited, owned by PAC-47** (Revision 1): API-077 (`:335`, Reported,
    the free/busy query accepts an end that is not after the start and answers
    200 with empty results). This spec changes neither the free/busy-query
    operation's server behaviour nor its contract; it changes only the web code
    that reads its answer, and API-077 is a server-side validation gap that no web
    change can fix. Fixing it means a new 400 on that operation, i.e. a server and
    contract change, which §3 rules out for this spec. PAC-47 changes that
    operation's answer (WebCal) and already lists API-077 as inherited
    (`factory/pac-47-spec`, `docs/specs/PAC-47.md:31`), so it is owned there, as
    D-4 reasons for the other split findings. If the contract stage for PAC-41
    finds it must edit the free/busy-query operation after all, API-077 becomes
    inherited and the work returns to the spec stage. See OQ-2.
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
    also lists API-046 and API-077 as inherited scope on the free/busy-query
    operation (`PAC-47.md:31`). See OQ-2.

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
- The "Availability synced" tooltip names a provider without a provider field:
  it reads "via Microsoft Outlook" only when the provider is `outlook` and "via
  Google Calendar" otherwise (`CoverageBadge.tsx:83-86`). Against the live server,
  which sends no provider, it therefore says "Google Calendar" for every caller,
  including an Outlook-connected caller whose "known" status came from Microsoft
  (`backend/engine/freebusy_service.go:119-127`). The provider mark itself also
  carries an accessible label and title naming the provider
  (`CoverageBadge.tsx:10-27`).

### 2.4a What the room picker does with an earlier room, and with failures

*(Added in Revision 1.)*

- An event loaded from the server never carries its room: the event list and the
  PATCH answer drop room resource attendees (contract
  `contracts/openapi/paths/calendar.yaml:32-34,238-240`). The edit form starts
  from that event's room address (`EventEditForm.tsx:83,99`), so on reopening an
  event the form holds no room address and the picker starts in "Free text"
  (`LocationPicker.tsx:20`).
- PATCH never removes a room. It adds the given room if absent and keeps every
  room already on the event, including when attendees are rewritten
  (`backend/api/handlers_events.go:96-142`; contract `calendar.yaml:1370-1382`;
  PAC-29 D-6).
- Today none of this matters, because no room is ever booked from Paceday
  (§1 point 1). Once a pick really books a room, two things follow on a later edit
  of the same event: picking a different room adds it and keeps the first, so the
  event holds both; switching to "Free text", pressing "Clear room"
  (`LocationPicker.tsx:60-80`) or editing the location text sends no room address,
  and the first room stays booked while the location text no longer names it.
  Nothing in the form says so.
- Room-list failures are invisible. The search uses `try/finally` with no `catch`
  (`LocationPicker.tsx:46-53`) and the existing-room lookup a `.then` with no
  `catch` (`:35-38`); the list then shows "No rooms matched." or "Start typing to
  search rooms." (`:163-166`). The room list answers 401 `calendar not connected`
  for any user without a stored Google credential, an Outlook-connected user
  included, because it always uses the Google client
  (`handlers_events.go:27-34,312-317`; `engine/cal_iface.go:32-44`), and 500 on any
  other failure (`handlers_events.go:319-323`). Only an unreachable server falls
  back to demo data (`src/api/client.ts:548-561`).

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
- Backend, corrected in Revision 1: **no test pins the room list's success
  body.** Only the 401 `calendar not connected` answer is exercised directly
  (`backend/api/pac21_calendar_not_connected_test.go:87`). The 500 body is
  reached only by the repo-wide static guard against raw error bodies
  (`pac21_error_bodies_test.go`, `TestPAC21NoRawErrorsInResponseBodies`), which
  never calls the room list. The wire-struct tag check
  (`pac26_wire_keys_test.go:70-80`) does not include the room struct, and no test
  reads the room list's 200 body. Revision 0 cited these tests as protecting the
  room list's fields; they do not. Once picking a room books it, booking depends
  on the room's identifier arriving under its current key as the bookable address,
  and a rename of that key would stop every booking with every listed test green.
- Backend: the free/busy wire keys are covered (`pac26_wire_keys_test.go`).

## 3. Desired behaviour

The server is the reference; the web is brought to it. No new room data and no
new coverage information are invented (D-1, D-2).

- **A room the user picks is a room the event holds.** Picking a room and saving
  adds that room to the event on the user's calendar, using the room's address as
  the server reports it. When the form already holds a room address and the room
  list contains that room, the picker shows it as selected.
- **A room booked earlier stays booked, and the user is told before saving**
  (D-6, Revision 1). Paceday adds rooms and never releases them. If the event
  already holds a room, picking another room adds it alongside the first, and
  leaving room booking (switching to free text, clearing the room, or editing the
  location text) leaves the first room booked. Because the event as Paceday loads
  it does not reveal its room, the form cannot know whether an earlier room
  exists, so it says this whenever the user is booking a room and whenever they
  leave room booking in that edit: saving adds the chosen room, does not release a
  room the event already holds, and a held room is released in the calendar
  itself.
- **The room list claims only what it knows.** Each room shows its name, in the
  list and on the selected-room card. Neither shows an availability verdict, a
  building, floor or capacity line, or an empty placeholder for them. Every listed
  room can be picked. The search hint describes what search actually matches.
- **A room list that cannot be read says so** (Revision 1). When the room list
  cannot be read, the picker says the rooms could not be loaded, instead of "no
  rooms matched" or an invitation to type. When the reason is that the user has no
  connected Google calendar (room booking reads Google's room calendars only, so
  this includes Outlook-connected users), it says room booking needs a connected
  Google calendar. The form's room address is not changed by the failure.
- **Room search asks only for what the server answers.** It sends the search text
  and nothing that implies a time-window filter.
- **Coverage has two states.** A participant's calendar is either readable
  ("Availability synced") or not (unknown). The "synced" chip names no calendar
  provider anywhere: not as a mark, not in its tooltip, not in its accessible name
  or description. The server does not say which provider answered, so the chip
  does not guess. "On Paceday" remains, reached only from the scheduling link's
  own host record, as today.
- **Demo mode shows the same product.** Demo rooms have a name and an identifier
  that is a real room address of the form the live product books, and nothing
  else; demo coverage uses the same two states and no provider. Demo mode does
  not display a capability the live product lacks, and a demo room could be
  booked by the live save path unchanged.
- **The room list's answer is pinned.** The server's room-list answer — an
  identifier that is the bookable room address, and a name — is protected by a
  test, since booking now depends on it.
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
"Busy"), no row is disabled, and no row renders a secondary detail line; and when
the user picks one of them, then the selected-room card shows the room's name and
renders no secondary detail line either. *(Fails today: every row reads
"✓ Available" and has an empty detail line, and so does the selected card.)*
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
link) and the user opens the chip's tooltip, then the chip reads "Availability
synced", and neither its visible text, its tooltip text, nor any accessible name,
label, title or description inside the chip names a calendar provider ("Google",
"Microsoft", "Outlook" or any other); given "unknown", the chip shows the unknown
state. *(Fails today: the tooltip says "via Google Calendar" for every caller.)*
[unit]

AC-9. Given a scheduling link whose saved host record shows a co-host as accepted
with a Paceday account, when the drawer renders that co-host, then the chip reads
"On Paceday" whatever the free/busy answer says. (Holds today; this pins it.)
[unit]

AC-10. Given demo mode, when room search and free/busy are called, then every demo
room carries only an identifier and a name, every demo room's identifier is a room
address the live event save accepts (matches
`^[^@\s]+@resource\.calendar\.google\.com$`), and every demo coverage status is
"known" or "unknown" with no provider. *(Fails today: demo rooms carry five more
fields and their identifiers are `r1`…`r7`.)*  [unit]

AC-11. Given the audit register after this work merges, then API-040, API-041 and
API-046 are marked Fixed with a reference to PAC-41; API-077 is unchanged by this
work (still Reported, or as PAC-47 left it); and API-047, API-075 and API-078
remain Reported. If the follow-up issue (OQ-3) exists when the contract stage
runs, each of those three rows names it; if it does not, each carries the note
"split from PAC-41 (D-4); follow-up issue not yet filed" and AC-11 is met by that
note. Either way none of the three is marked Fixed.  [contract]

AC-12. *(Revision 1, D-6.)* Given an event open for editing, when the user is in
room booking — searching for a room or with a room selected — then, before they
save, the form shows text saying that saving adds the chosen room and does not
release a room the event already holds, and that a held room is released in the
calendar itself. *(Fails today: no such text exists.)*  [unit]

AC-13. *(Revision 1, D-6.)* Given the user entered room booking during this edit,
when they switch to free text, or clear the selected room, then, before they save,
the form shows text saying that a room already booked on the event stays booked.
*(Fails today: no such text exists.)*  [unit]

AC-14. *(Revision 1, D-6; pins the outcome D-6 accepts.)* Given an event on a
signed-in Google user's calendar that holds room "Helsinki" booked through
Paceday, (a) when the user reopens it, picks room "Oslo" and saves, then the
stored event holds both Helsinki and Oslo as resource attendees; (b) when the user
reopens it, sets the location to free text "Café" and saves, then the stored
event still holds Helsinki as a resource attendee and its location is "Café".
*(Not reachable today, because no room is ever booked; it becomes reachable with
AC-1. If the owner overturns D-6, this criterion is replaced, not deleted
silently.)*  [e2e]

AC-15. *(Revision 1.)* Given a signed-in Google user whose calendar list contains
a room resource calendar named "Helsinki", when the room list is read, then the
200 answer is a JSON array whose element for Helsinki has exactly the two keys
the contract names for a room, the identifier equal to the room's resource
calendar address and the name "Helsinki"; and a change to the room's wire key
set fails a test. *(Holds today; no test pins it — §2.5. This pins it.)*
[contract]

AC-16. *(Revision 1.)* Given room booking is open, when the room list request
fails because the user has no connected Google calendar, then the picker says
room booking needs a connected Google calendar; when it fails for any other
reason (except an unreachable server, which keeps falling back to demo data),
then the picker says the rooms could not be loaded. In both cases it shows
neither "No rooms matched." nor "Start typing to search rooms.", and the room
address the form holds is unchanged. *(Fails today: the failure is swallowed
and the list reads "No rooms matched." or "Start typing to search rooms.")*
[unit]

Gate requirement (from the ticket; not a behaviour): every gate the
implementation touches — the api gate if the contract or audit register is
edited or a backend test is added (AC-15), the web gate, and the e2e gate for
AC-1 and AC-14 — prints rc=0 on the final commits.

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
  loaded events carry their room is a separate change (OQ-5).
- **Replacing or releasing a room from Paceday** (D-6, OQ-5). Releasing a room
  needs both a server change (PATCH removes nothing today) and the event to carry
  its room when loaded. Until the owner chooses that, the earlier room stays
  booked and the user is told (AC-12 to AC-14).
- **API-077** (the free/busy query's missing window check). Owned by PAC-47; see
  the inherited-scope header and OQ-2.
- **A newly added co-host who is a Paceday user showing "On Paceday" before
  accepting.** Live behaviour already cannot show this; nothing here makes it worse.
- **API-047, API-075, API-078** (D-4, OQ-3).
- **Outlook and WebCal free/busy.** PAC-47.

## 6. What must not change

- The server's free/busy answer: same fields, same statuses, same error bodies.
  Protected by `backend/api/pac26_wire_keys_test.go`, `handlers_freebusy_test.go`,
  and the free/busy e2e tests in §2.5.
- The server's room list: same fields, same error bodies. *(Corrected in
  Revision 1.)* Only its 401 is protected
  (`pac21_calendar_not_connected_test.go:87`); its 500 body only indirectly by the
  static guard in `pac21_error_bodies_test.go`; its 200 body `{id, name}` **by no
  test** — a finding. AC-15 requires the missing test; the e2e seam check
  (`seam.spec.ts:348-352`) calls the operation but does not pin the body's keys.
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
- **OQ-2 — PAC-47 also inherits API-046 and API-077.** Owner: whichever of
  PAC-41 and PAC-47 reaches the contract stage second. Resolve by reading the
  audit register: if API-046 is already Fixed, the second must keep it fixed and
  must not reintroduce a third status or a provider field for Outlook or WebCal
  coverage. API-077 belongs to PAC-47 alone (header; Revision 1): PAC-41 neither
  marks it Fixed nor changes the free/busy-query operation, so whichever order the
  two land in, PAC-47 resolves it. If PAC-47 is abandoned or re-scoped so that it
  no longer changes that operation, API-077 stays Reported and goes to the OQ-3
  follow-up issue, not to PAC-41.
- **OQ-3 — follow-up issue for API-047, API-075, API-078, and confirmation of
  D-4.** Owner: product owner (the factory comments and moves status in Linear but
  does not create issues). The Linear issue lists these three findings as part of
  PAC-41; D-4 narrows that, which is the owner's call. Resolve by confirming D-4
  and filing one PAC issue in project "API contract hardening" carrying the three
  findings, linked here and in the audit register. If unanswered when the
  contract stage runs, AC-11's fallback note applies.
- **OQ-4 — can AC-1 and AC-14 be observed in e2e?** The Google stand-in stores
  the event body it is given (`e2e/fake-google/events.go:45-57`); whether it keeps
  a resource attendee's flag on insert or patch, and returns an earlier room on
  the next read so PATCH can keep it (AC-14), was not verified. Owner: e2e-author
  at stage 5. If it does not, the stand-in gains that (stand-in code, not product
  code).
- **OQ-5 — should Paceday replace and release rooms?** *(Revision 1.)* Owner:
  product owner. D-6 keeps an earlier room booked and tells the user. The
  alternative — a loaded event that carries its room, and a save that can replace
  or release it — is a server, contract and web change, and it releases bookings,
  which this spec does not do without an owner answer. Resolve by a yes/no on the
  Linear issue; a "yes" is its own spec and replaces AC-12 to AC-14.
- **OQ-6 — confirm D-2.** *(Revision 1, from the challenge.)* Owner: product owner.
  Should "On Paceday" ever be decided from free/busy? D-2 says no, for privacy;
  the challenger agrees pending the owner. Resolve by a yes/no on the Linear
  issue.

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
  every room is worse than none: it is a claim the product never checked, and §1
  sets out the cost when a user relies on it. *(Revision 1: Revision 0 attributed
  "must not claim what it did not check" to PRODUCT.md principle 3. Principle 3
  (`smart-calendar-flow/PRODUCT.md:89`) is about automated changes being visible
  and confirmable, never silent; it does not say that. The reasoning above is the
  factory's, not a PRODUCT.md quote. Principle 3 is cited where it applies, in
  D-6.)*
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
  that exists. *(Revision 1:)* demo room identifiers become room addresses of the
  form the live save accepts, so demo mode does not book by an identifier the
  real product would reject (AC-10).
- **D-6 — an earlier room stays booked, and the user is told before saving.**
  *(Revision 1.)* Assumed by the factory, pending owner confirmation (OQ-5).
  Once a pick books a room, a later edit that picks another room, switches to free
  text, clears the room or edits the location text leaves the earlier room booked:
  PATCH never removes a room (PAC-29 D-6), and the loaded event does not reveal it
  (§2.4a). The alternative, releasing rooms, needs a server and contract change
  and removes bookings the user may not mean to remove; with no owner answer the
  factory does not choose an option that removes data. PRODUCT.md principle 3
  (`PRODUCT.md:89`) asks that changes be visible and never silent, so the
  conservative option is not silence: the form says, before saving, that the
  chosen room is added and an earlier room is not released (AC-12, AC-13), and the
  outcome itself is pinned (AC-14). Residual gap, stated plainly: a user who
  reopens an event and edits only the location text, never entering room booking,
  sees no notice, because the form cannot know a room is held. That is the same
  outcome as editing the event's location in any client that does not touch
  attendees, and OQ-5 is how it closes.

## Challenge — 2026-10-27

*spec-challenger, reviewing the committed spec at `7615006` against api `6d751f9`,
web `ad52160` and the PAC-41 Linear issue text. I did not see the author's
reasoning beyond this file.*

**Verdict**: accept-with-changes

### Blocking

1. **Re-picking or clearing a room now silently keeps the old room booked, and the
   spec neither states nor tests it.** Today no room is ever booked, so this cannot
   happen. Once this spec ships, rooms do get booked. A loaded live event never carries
   its room (§5; `calendar.yaml:32-34,238-240`), and PATCH never removes a room
   (`handlers_events.go:100-142`; `calendar.yaml:1376-1379`, PAC-29 D-6). So:
   (a) the user saves with room A, reopens the event, picks room B and saves, and the
   event now holds **A and B**; (b) the user reopens the event, switches to "Free
   text" or presses "Clear room" (`LocationPicker.tsx:60-80`) and saves, and room A
   stays held while the location text no longer names it. Either way the UI tells the
   user one thing and the room calendar shows another. That is the trust failure §1
   describes, in the other direction: rooms are held that the user believes they
   released. It is also a silent change, which PRODUCT.md principle 3 rules out.
   *Resolves when* the spec states the observable outcome for (a) and (b) and pins it
   with an acceptance criterion. One way is to bring room replacement and removal into
   scope. The other is to record, as a Decision pending the owner, that the earlier
   room stays booked, with a criterion that requires the user to be told so before
   saving. "What must not change" alone is not enough. This is a requirement, not a
   choice of mechanism.

2. **§2.5 and §6 say the room list's answer is protected by tests that do not cover
   it.** §6 says the room list keeps "same fields … protected by
   `pac26_wire_keys_test.go`, `pac21_calendar_not_connected_test.go`,
   `pac21_error_bodies_test.go`". In fact:
   - `pac26_wire_keys_test.go` covers the free/busy keys and the wire-struct tag check.
     `calendar.Room` is not in that check's list (`:72-80`), and no test reads
     `GET /api/rooms`' 200 body.
   - `pac21_error_bodies_test.go` never names `/api/rooms` or `listRooms`. Only its
     repo-wide static guard (`TestPAC21NoRawErrorsInResponseBodies`, `:431`) touches
     that handler, and only indirectly.
   - Only the 401 is exercised directly (`pac21_calendar_not_connected_test.go:87`).

   After this spec, booking depends entirely on `id` being the bookable address under
   the JSON key `id`. A rename or retag of that field would silently stop every booking
   while every listed test stays green. *Resolves when* §2.5 and §6 say that no test
   pins the room list's `{id, name}` body (as §6 already does for free text) and the
   spec requires one, or else cite a test that really does pin it.

3. **AC-8 allows the "synced" chip to keep naming a provider.** The chip's tooltip
   reads "We can read this person's calendar via Google Calendar" whenever the
   provider is not `outlook` (`CoverageBadge.tsx:83-86`). Once `provider` is removed,
   it says "Google Calendar" for everyone. That includes Outlook-connected callers,
   whose `known` status comes from Microsoft (`engine/freebusy_service.go:119-127`).
   §3 says the chip "carries no provider mark", and D-2 drops the provider because the
   server does not report it. Yet a test written to AC-8 ("shows no provider mark")
   passes while the tooltip still claims a provider. *Resolves when* AC-8, or a new
   criterion, requires that the "Availability synced" chip names no calendar provider
   anywhere, including its tooltip and accessible text.

4. **The inherited-scope list leaves out an open finding on an operation it names.**
   The header lists every finding on the room-list and free/busy-query operations
   (resolved, already fixed, or split out), but it omits **API-077**
   (`api-audit.md:335`, Reported, `POST /api/freebusy`: no `end_time > start_time`
   check). PAC-47 lists that finding as inherited on the same operation
   (`factory/pac-47-spec`, `PAC-47.md:31`). Under README §7 rule 1, the contract stage
   will have to decide whether this work "modifies" that operation, and the spec gives
   it nothing to decide with. *Resolves when* API-077 is named in the header as one of
   resolved, not inherited because the operation's contract is unchanged (with that
   stated), or split, with the same reasoning D-4 gives. OQ-2 should then cover it the
   way it covers API-046.

### Non-blocking

1. AC-3 covers list rows only. The selected-room card renders the same empty detail
   line (`LocationPicker.tsx:131-135`), and §3 says there is "no empty placeholder".
   AC-3 should cover the selected card too.
2. §3 says "Demo rooms have a name and an address". AC-10 asks only for "an
   identifier and a name", and today's demo ids are `r1`…`r7` (`client.ts:137-143`).
   Since `id` *is* the address (D-1), AC-10 should require each demo room's
   identifier to be a room address the live PATCH would accept
   (`^[^@\s]+@resource\.calendar\.google\.com$`). Otherwise demo mode books rooms by
   an id the real product would reject.
3. Error and unavailable states are not addressed. When the room list fails (401 for
   a user with no Google credential, which includes an Outlook-connected user, because
   the handler uses the Google client: `handlers_events.go:312-317`; or 500), the
   picker swallows the rejection. Search uses `try/finally` with no catch (`:48-53`),
   and the lookup uses `.then` with no catch (`:35`). The picker goes on showing
   "No rooms matched." or "Start typing to search rooms." (`:165`), which is a claim
   it did not check, the thing D-3 objects to. The spec should say what the user sees
   when the room list cannot be read.
4. D-3 attributes "must not claim what it did not check" to PRODUCT.md principle 3.
   Principle 3 (`PRODUCT.md:89`) is about automation changes being visible and
   confirmable, never silent. The conclusion still holds, but the citation is a
   stretch. Blocking 1 is the stronger use of principle 3.
5. AC-11 cannot pass until the owner files the follow-up issue (OQ-3). The spec
   should say what AC-11 requires if OQ-3 is still unanswered when the contract
   stage starts, for example that the rows stay Reported and carry a note "split from
   PAC-41".
6. OWNER: the issue lists API-047, API-075 and API-078 as part of PAC-41's scope. D-4
   splits them out. The split is well argued (other operations, plus the `OrgMember`
   `x-uncertain` at `openapi.yaml:8448`), but narrowing the issue's own scope is the
   owner's call. Confirm D-4 and file the follow-up (OQ-3).
7. OWNER: OQ-1. Does the product want room details (building, floor, capacity) or
   room availability? Either one is a new feature that reads more Google directory
   data.
8. OWNER: D-2. Should "On Paceday" be decided from free/busy at all? D-2's privacy
   argument (it would reveal whether an arbitrary address has a Paceday account) is
   the conservative reading, and I agree it should stand until the owner says
   otherwise.

### Criteria I could not falsify

- None. Every AC from AC-1 to AC-10 has a test that fails on `ad52160` and passes
  after the change. AC-9 is a stated pin, not a change, and is fine as such. AC-1 is
  falsifiable only once OQ-4 is settled. The stand-in stores the event body it is
  given (`e2e/fake-google/events.go:45-57`), but nothing yet asserts that it keeps
  `resource: true`. AC-11 depends on OQ-3 (non-blocking 5).

### Citations I checked and found wrong

- §2.5 / §6: "the room list's failure bodies are covered (…
  `pac21_error_bodies_test.go`)" and "same fields … protected by
  `pac26_wire_keys_test.go`". In fact neither test exercises `/api/rooms`. The 200
  body has no test, and the 500 body is covered only by the repo-wide static guard
  (Blocking 2).
- D-3: PRODUCT.md principle 3 does not say "must not claim what it did not check"
  (`PRODUCT.md:89`; non-blocking 4).
- Everything else checked out: `rooms_attendees.go:11-14,25-40`;
  `handlers_events.go:48,134-140,189-191,311-328`;
  `handlers_freebusy.go:23-27,83-87`; `freebusy_service.go:34,93,143-151`;
  `calendar.yaml:368-405,1522-1543,1577-1590`; generated
  `types.ts:2727-2732,3104-3117` (no non-generated web use of `CalendarRoom`);
  web `types.ts:159-167,247-263`; `client.ts:136-144,845-866,873-884,1416-1446`;
  `LocationPicker.tsx:35-36,74,132-134,154,172-193,180-182`;
  `EventEditForm.tsx:141`; `coverageCache.ts:103-119,137-164`; `teams.ts:11`;
  `LinkEditDrawer.tsx:184-188,193-194`; `CoverageBadge.tsx:10-27,72-80`;
  `seam.spec.ts:348-352,163-176`; `calendar.spec.ts:195-217`;
  `calendarlist.go:11-12`; `fixture.go:46`. api `main` and web `main` have not moved
  since `6d751f9` and `ad52160`.

### Consumers this breaks

- None, as long as the server stays unchanged as specified. Consumers I checked:
  - `clockwise-like/mcp/tools.go:207-215` (`clockwise_search_rooms`) passes only `q`
    and relays the body.
  - In the web: `contract.ts:64,106`, `client.ts:982-986,1416`, `coverageCache.ts`,
    `teams.ts:662` and `LinkEditDrawer.tsx:177`.
  - `contract.test.ts:92-106` already uses `status: "known"` with no provider.
  - `EventEditForm.test.tsx:25` mocks `LocationPicker` away, so it is unaffected.

  The narrowed `searchRooms` signature (AC-5) and the narrowed coverage types (AC-7)
  have no other callers on `ad52160`.

## Revision 1 — 2026-10-27

*spec-author, answering the challenge above. Each item says what changed and
where. Facts re-checked on api `6d751f9` and web `ad52160` (both still `main`).*

### Blocking

1. **Re-picking or clearing a room keeps the old room booked.** Accepted. The
   outcome is now stated (§2.4a, §3) and decided as **D-6**, assumed by the
   factory pending the owner (OQ-5): the earlier room stays booked, because
   releasing it needs a server and contract change and removes bookings without an
   owner answer. The user is told before saving: **AC-12** (while booking a room)
   and **AC-13** (on switching to free text or clearing the room). The outcome for
   both of the challenger's cases is pinned by **AC-14** (a: picking Oslo after
   Helsinki leaves both held; b: free text leaves Helsinki held). The one case the
   form cannot detect — reopening and editing only the location text — is stated
   as a residual gap in D-6 and closes with OQ-5. Room replacement and release
   are listed in §5.
2. **§2.5 and §6 cited tests that do not cover the room list.** Accepted; the
   citation was wrong. §2.5 and §6 now say that only the 401 is exercised
   directly, the 500 body only through the static guard, and the 200 `{id, name}`
   body by no test; the wire-struct tag check does not include the room struct.
   **AC-15** requires a test pinning the 200 body's keys and that the identifier
   is the resource calendar address; §3 states the requirement in prose.
3. **AC-8 allowed the chip to name a provider.** Accepted. §2.4 now records that
   the tooltip says "via Google Calendar" for every live caller, Outlook included.
   §3 and **AC-8** now require that the "Availability synced" chip names no
   calendar provider in its visible text, tooltip, or any accessible name, label,
   title or description. AC-8 now fails today.
4. **API-077 missing from the inherited scope.** Accepted. The header now names
   API-077 as **not inherited, owned by PAC-47**: this spec changes neither the
   free/busy-query operation's server behaviour nor its contract, the fix is a
   server-side 400 that §3 rules out here, and PAC-47 changes that operation and
   already lists API-077. If PAC-41's contract stage must edit that operation after
   all, the work returns to the spec stage. OQ-2 covers API-077 alongside API-046,
   including the case where PAC-47 no longer changes the operation. AC-11 requires
   that this work leaves API-077 unchanged. While checking, API-028 (Fixed,
   PAC-26), on the same operation, was also missing from the header; it is now
   listed under "Already fixed".

### Non-blocking

1. **AC-3 and the selected-room card.** Done: AC-3 now covers the selected card,
   and §3 says it.
2. **AC-10 and demo identifiers.** Done: AC-10 requires every demo identifier to
   match the room-address pattern the live save accepts; §3 and D-5 say why.
3. **Room-list failures.** Done: §2.4a describes today's swallowed 401/500, §3
   states what the user sees, **AC-16** pins it (a no-calendar message for the 401,
   which includes Outlook-connected users; a could-not-load message otherwise; the
   form's room address unchanged; an unreachable server still falls back to demo
   data as today).
4. **D-3 citation.** Done: D-3 no longer attributes its reasoning to PRODUCT.md
   principle 3 and says so; principle 3 is cited in D-6, where it applies.
5. **AC-11 if OQ-3 is unanswered.** Done: AC-11 now says the three rows stay
   Reported and carry "split from PAC-41 (D-4); follow-up issue not yet filed"
   when no follow-up issue exists, and name it when one does.
6. **OWNER: confirm D-4, file the follow-up.** Recorded in OQ-3 (now also asks
   for confirmation of D-4) and raised on the Linear issue.
7. **OWNER: OQ-1.** Unchanged; still open, raised on the Linear issue.
8. **OWNER: D-2.** Recorded as OQ-6 and raised on the Linear issue; D-2 stands
   until answered.
