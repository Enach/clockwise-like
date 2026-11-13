# Spec — PAC-41: Room contract mismatch, and free/busy coverage states the server never produces

- **Linear**: https://linear.app/paceday/issue/PAC-41
- **Status**: draft (Revision 2). It replaces Revision 1's scope after the owner answers of 2026-10-27, and is awaiting re-challenge.
- **Author**: spec-author
- **Read on**: api `main` at `68540f6`, web `main` at `ae72939` (2026-11-13). Every
  citation in §2 was re-read on these commits. Revision 1 was read on `6d751f9` /
  `ad52160`, and the web has moved a lot since (PAC-83 moved demo data into
  `src/demo/` and removed the fallback to demo data when the server is unreachable).
- **Scope (Revision 2)**: PAC-41 is a **contract-accuracy ticket**. It covers four things:
  - the room and free/busy-participant shapes as the server emits them today;
  - the web defects API-040, API-041 and API-046, fixed in a web PR;
  - a backend test that pins the room-list answer;
  - the room-list 401-versus-500 distinction already stated in contract revision 3.

  The owner's two product answers become their own tickets, each with its own spec
  (D-7). Room details (OQ-1) are **FOLLOW-UP-ROOM-DETAILS**. Releasing or keeping an
  earlier room (OQ-5) is **FOLLOW-UP-ROOM-RELEASE**. Until those land, PAC-41 ships the
  interim behaviour set out in D-1 and D-6.
- **Inherited scope**:
  - **Resolved by this spec**: API-040 (`docs/factory/api-audit.md:298` on main: room
    search sends a time window the server ignores). API-041 (`:299`: the web room type
    requires fields the server never sends). API-046 (`:304`: the web coverage type
    declares a provider and a third status the server never emits). All three sit on
    the room-list and free/busy-query operations. All three are resolved on the web
    side, because the server and the contract already describe the right shapes (§2.1,
    §2.3).
  - **Already fixed, not inherited**: API-074 (`:332`, Fixed (PAC-23)). API-028
    (`:286`, Fixed (PAC-26)), the mixed key casing in the free/busy-query answer.
  - **Not inherited, owned by PAC-47**: API-077 (`:335`, Reported). The free/busy query
    accepts an end that is not after the start and answers 200 with empty results. This
    spec changes neither the free/busy-query operation's server behaviour nor its
    contract operation. Fixing API-077 means a new 400, which is a server change, and
    PAC-47 (Backlog) changes that operation and already lists API-077. If PAC-41's
    contract must edit that operation after all, API-077 becomes inherited and the work
    returns here. See OQ-2.
  - **Split out of this spec** (D-4, OQ-3): API-047 (`:305`), API-075 (`:333`) and
    API-078 (`:336`). They sit on three other operations and share no behaviour with
    the room/coverage mismatch.
  - **`x-uncertain`**: none on the room-list or free/busy-query operations, and none on
    the room or free/busy-participant schemas. Checked in
    `contracts/openapi/paths/calendar.yaml` on `68540f6`: `/api/rooms` `:368-406`,
    `FreeBusyParticipant` `:1592-1613`, `CalendarRoom` `:1647-1659`.
  - **Shared with PAC-47**: PAC-47 also lists API-046 and API-077 on the free/busy-query
    operation. See OQ-2.

## 1. Problem

A user editing an event can switch the location to "Book a room", search, and pick a
conference room. Against the real server, three things go wrong at once.

1. **The room is never booked.** The picker writes the room's name into the location,
   but it takes the room's address from a field the live room list does not carry. So
   the address goes out empty, the server adds no room to the event, and it answers
   success. The user sees a room name on an event that holds no room. Nobody else sees
   the room as taken, so two meetings can "have" the same room.
2. **Every room is shown as "✓ Available".** The web decides availability from a field
   that only demo data carries. When that field is missing, the row reads as available,
   so the badge is a claim the product never checked.
3. **Each row has an empty detail line** where building, floor and capacity would go.
   Only demo data carries those fields.

Separately, the scheduling-link drawer's co-host chips are written against a coverage
answer with three states and a provider mark. The server produces two states and no
provider. So the third state ("this person is on Paceday", read from free/busy) can
never appear outside demo mode, and neither can the Google/Microsoft mark on
"Availability synced". Meanwhile the chip's tooltip names Google Calendar for every
live caller, including Outlook users.

The cost is trust. A user who books a room through Paceday and arrives to find it taken
has learnt that Paceday's scheduling cannot be relied on.

The owner has since decided where rooms should end up (Decisions, owner answers). Rooms
should show capacity, building and floor from directory data. Paceday should ask whether
to release or keep an earlier room. Both need data or server behaviour that does not
exist today, so they are new features. This ticket makes today's product truthful and
makes room booking actually work, and the two follow-ups build the rest on top of it
(D-7).

## 2. Current behaviour

### 2.1 What the server sends for a room

All api citations are `clockwise-like` at `68540f6`.

- The server's room has two fields, `id` and `name`, with no `omitempty`
  (`backend/calendar/rooms_attendees.go:11-14`).
- Rooms are the caller's Google calendar-list entries whose id contains
  `resource.calendar.google.com`. They can be filtered by an optional lower-cased
  substring of the summary or id (`rooms_attendees.go:24-42`). The `id` is the room's
  resource calendar address, which is also its bookable email.
- The handler reads only `q` and turns a nil list into `[]`
  (`backend/api/handlers_events.go:312-330`).
- The contract says exactly this. `CalendarRoom` is `{id, name}`, both required, with
  `additionalProperties: false` (`contracts/openapi/paths/calendar.yaml:1647-1659`), and
  `/api/rooms` declares only `q` (`calendar.yaml:368-406`). The web's generated types
  carry `CalendarRoom` (`smart-calendar-flow/src/api/generated/types.ts:4365`). No
  hand-written web code uses it.

### 2.2 What the web believes a room is

All web citations are `smart-calendar-flow` at `ae72939`.

- The hand-written `Room` has `email` **required**, plus optional `building`, `floor`,
  `capacity` and `available` (`src/api/types.ts:183-191`).
- Demo rooms carry all of those fields, and their ids are `r1` to `r7`
  (`src/demo/clientMocks.ts:87-95`). Demo search also matches on building and email,
  computes `available` from demo events, and marks two rooms busy "for demo flair"
  (`clientMocks.ts:713-739`). Since PAC-83, demo data is reached only in an explicit
  demo session of a non-production build (`src/api/demoGate.ts:17-24,41-50`).
- Room search sends `q`, `start` and `end` (`src/api/client.ts:1185-1186`;
  `src/api/contract.ts:109`). The server ignores `start` and `end` (API-040).
- The room picker (`src/components/LocationPicker.tsx`):
  - on pick, reports `room.email` as the event's room address (`:74`). Against the live
    server that address is `undefined`;
  - re-finds an existing room by searching the local part of the address and matching
    `r.email` (`:35-36`). This never matches live;
  - renders building, floor and capacity on the selected card (`:131-135`) and in each
    row (`:179-183`). It also renders a "✓ Available" / "✗ Busy" badge and disables busy
    rows (`:172-173,185-193`). With no `available` field, every row is enabled and
    labelled available;
  - invites search "by name or building" (`:154`), but live search matches name or
    address only.
- The event form sends the picker's room address on save
  (`src/components/EventEditForm.tsx:142`). The server adds a room only when that field
  is non-empty and matches `^[^@\s]+@resource\.calendar\.google\.com$`
  (`handlers_events.go:47-49,190-195`). An absent field adds nothing, and the save
  succeeds.

### 2.3 What the server sends for free/busy coverage

- Each participant is `{email, status, busy}`
  (`backend/api/handlers_freebusy.go:23-27,83`). There is no provider field.
- `status` is the engine's coverage, and it is only ever `"unknown"` or `"known"`
  (`backend/engine/freebusy_service.go:34,111,181-183`).
- The contract says the same. `FreeBusyParticipant.status` is `enum: [known, unknown]`
  with `additionalProperties: false` (`calendar.yaml:1592-1613`), and the generated web
  type matches (`src/api/generated/types.ts:4799`).

### 2.4 What the web believes coverage is

- `CoverageStatus = "paceday_user" | "known" | "unknown"`, `CoverageProvider`, and
  `ParticipantCoverage { email; status; provider? }` (`src/api/types.ts:278-287`).
- Demo free/busy classifies addresses by domain and produces all three statuses plus a
  provider (`src/demo/clientMocks.ts:346-367`). The demo coverage summary counts
  `paceday_user` as checked (`clientMocks.ts:374-384`).
- The coverage cache copies `provider` through (`src/api/coverageCache.ts:103-119`).
- The co-host chip maps `paceday_user` to "On Paceday" and `known` to "Availability
  synced" with the provider (`src/components/links/LinkEditDrawer.tsx:228-229`;
  `src/components/links/CoverageBadge.tsx:10-27,72-90`). "On Paceday" is *also* reached
  independently, from the link's own host record for a co-host who has accepted
  (`LinkEditDrawer.tsx:217-224`). That path reads no coverage data and is real.
- The "Availability synced" tooltip reads "via Microsoft Outlook" only when the
  provider is `outlook`, and "via Google Calendar" otherwise (`CoverageBadge.tsx:84-85`).
  The live server sends no provider, so the tooltip says Google Calendar to every
  caller. That includes an Outlook-connected caller whose "known" status came from
  Microsoft. The provider mark also carries an accessible label and title that name the
  provider (`CoverageBadge.tsx:21-22`).

### 2.4a An earlier room, and failures

- An event loaded from the server never carries its room. The event list and the PATCH
  answer drop room resource attendees (`calendar.yaml:32-34,238-240`). The edit form
  starts from the event's room address (`EventEditForm.tsx:84,100`), so when an event
  is reopened the form holds no room address and the picker starts in "Free text"
  (`LocationPicker.tsx:20`).
- PATCH never removes a room. It adds the given room if absent and keeps every room
  already on the event, even when attendees are rewritten
  (`handlers_events.go:94-144`; PAC-29 D-6).
- None of this matters today, because no room is ever booked from Paceday (§1 point 1).
  Once a pick really books a room, two things follow on a later edit of the same
  event. Picking a different room adds it and keeps the first, so the event holds both.
  Switching to "Free text", pressing "Clear room" (`LocationPicker.tsx:60-80`) or
  editing the location text (`:121`) sends no room address, so the first room stays
  booked while the location text no longer names it. Nothing in the form says so.
- Room-list failures are invisible to the user. The search uses `try/finally` with no
  `catch` (`LocationPicker.tsx:46-53`), and the existing-room lookup uses a `.then` with
  no `catch` (`:35-38`). The list then shows "No rooms matched." or "Start typing to
  search rooms." (`:163-166`). The server answers as follows:
  - **401 `{"error":"calendar not connected"}`** when the caller has no stored Google
    credential, whatever calendar provider they selected (`handlers_events.go:27-35,315-318`;
    `server_errors.go:67-82`). Every Outlook-only user is in this group. An
    Outlook-selected user who also holds a Google credential (the same email signed
    in with Google earlier) gets that Google calendar's rooms instead.
  - **500 `internal error`** when a Google credential is stored but cannot be used.
    That covers one the provider has permanently refused to renew (a PAC-67 loss:
    `auth.LostError`, `backend/auth/credential_lifecycle.go:33-47`, surfacing from
    `ListRooms` at `handlers_events.go:320-324`) and one that cannot be decrypted or
    read (`engine.ErrCredentialStore`, `backend/engine/cal_iface.go:23-46`;
    `TestPAC84_GoogleUndecryptable`, `backend/api/pac84_decrypt_failure_test.go:124`, on
    the same `calClient` path). The 500 also covers any Google failure. Contract
    revision 3 states this in the `/api/rooms` description.
  - Since PAC-83, a failed real call never falls back to demo data
    (`src/api/demoGate.ts:37-50`). An unreachable server is now a failure like any
    other.
- The web has no handling for "calendar not connected" on this call. Its generic
  message for a 401 carrying an `error` body is that body's text
  (`src/api/client.ts:429-441`), and the picker never shows it anyway.

### 2.5 What protects this today

- No web test covers the room picker, the coverage cache's provider copy, the coverage
  badge or the co-host chip mapping. I searched `src/**/*.test.*` on `ae72939`.
  `EventEditForm.test.tsx:25` mocks `LocationPicker` away. The only test that names
  room search is `src/api/noInventedData.test.ts:61`, which checks that a failed
  `api.searchRooms` invents no data (PAC-83). It does not check the room shape.
- The e2e suite calls the room list only as a seam check (`e2e/tests/seam.spec.ts:350-352`)
  and never drives the room picker.
- **No backend test pins the room list's success body.** Only the 401 is exercised
  directly (`backend/api/pac21_calendar_not_connected_test.go:87`). The wire-struct tag
  check (`backend/api/pac26_wire_keys_test.go:72-80`) does not include `calendar.Room`.
  Once picking a room books it, booking depends on the room's identifier arriving under
  its current key. A rename of that key would stop every booking while every listed
  test stays green.
- The free/busy wire keys are covered (`pac26_wire_keys_test.go`).

## 3. Desired behaviour

The server is the reference, and the web is brought to it. PAC-41 invents no room data
and no coverage information. The owner's room details and room release are built by
their own tickets (D-7).

- **A room the user picks is a room the event holds.** When the user picks a room and
  saves, that room is added to the event on their calendar, using the room's address as
  the server reports it. When the form already holds a room address and the room list
  contains that room, the picker shows it as selected.
- **Until room release exists, an earlier room stays booked, and the user is told
  before saving** (D-6, interim until FOLLOW-UP-ROOM-RELEASE). Paceday adds rooms and
  never releases them. If the event already holds a room, picking another room adds it
  alongside the first. Leaving room booking (switching to free text or clearing the
  room) also leaves the first room booked. The event as Paceday loads it does not show
  its room, so the form cannot know whether an earlier room exists. It therefore tells
  the user in two situations: whenever they are booking a room, and whenever they leave
  room booking during that edit. The message says that saving adds the chosen room,
  does not release a room the event already holds, and that a held room is released in
  the calendar itself. FOLLOW-UP-ROOM-RELEASE replaces these notices with the owner's
  release-or-keep question.
- **The room list claims only what it knows** (D-1, D-3, interim until
  FOLLOW-UP-ROOM-DETAILS). Each room shows its name, both in the list and on the
  selected-room card. Neither shows an availability verdict, a building, floor or
  capacity line, or an empty placeholder for them. Every listed room can be picked. The
  search hint describes what search actually matches.
- **A room list that cannot be read says so.** When the room list cannot be read
  because the user has no connected Google calendar, the picker says that room booking
  needs a connected Google calendar. Room booking reads Google's room calendars only, so
  this applies to every Outlook-only user. When the list cannot be read for any other
  reason, the picker says the rooms could not be loaded. Other reasons include a Google
  connection that exists but can no longer be used, a server error, or an unreachable
  server. Any session-expiry handling that applies to every request elsewhere still
  applies here. A failure never changes the room address the form holds.
- **Room search asks only for what the server answers.** It sends the search text and
  nothing that implies a time-window filter.
- **Coverage has two states.** A participant's calendar is either readable
  ("Availability synced") or not (unknown). The "synced" chip names no calendar
  provider anywhere: not as a mark, not in its tooltip, and not in its accessible name
  or description. "On Paceday" remains, reached only from the scheduling link's own host
  record, as today.
- **Demo mode shows the same product** (D-5). Demo rooms have a name and an identifier
  that is a real room address of the form the live product books, and nothing else.
  Demo coverage uses the same two states and no provider. When FOLLOW-UP-ROOM-DETAILS
  gives live rooms details, demo rooms gain them in the same change.
- **The room list's answer is pinned.** A test protects the server's room-list answer,
  because booking now depends on it. The answer is an identifier that is the bookable
  room address, and a name.
- **One definition.** The web's room and coverage shapes are the ones generated from
  the contract. No hand-written copy that disagrees with it remains.
- The server's behaviour does not change.

## 4. Acceptance criteria

AC-1. Given a signed-in Google user whose calendar list contains a room resource named
"Helsinki", when they open an event, choose "Book a room", pick "Helsinki" and save,
then the event stored on their calendar has the Helsinki room address as a resource
attendee. *(Fails today: the save carries no room address and the stored event has no
room.)*  [e2e]

AC-2. Given the room list returns rooms carrying only an identifier and a name, when
the user picks one, then the change the picker reports to the form carries a room
address equal to that room's identifier and location text equal to its name. *(Fails
today: the address is undefined.)*  [unit]

AC-3. Given the room list returns rooms carrying only an identifier and a name, when
the list renders, then no row shows an availability verdict ("Available" or "Busy"), no
row is disabled, and no row renders a secondary detail line. And when the user picks one
of them, the selected-room card shows the room's name and renders no secondary detail
line either. *(Fails today: every row reads "✓ Available" and has an empty detail line,
and so does the selected card.)* *(Interim. FOLLOW-UP-ROOM-DETAILS replaces the "no
detail line" half for rooms that carry details.)*  [unit]

AC-4. Given the form already holds a room address and the room list contains a room
whose identifier equals it, when the picker opens in room mode, then that room is shown
as selected. *(Fails today: the match is made on a field the live list does not
carry.)*  [unit]

AC-5. Given the user types a search in room mode on an event with a start and an end,
when the search request is made, then it carries the search text and no start or end of
a time window. *(Fails today: both are sent.)*  [unit]

AC-6. Given the web code base, when it is type-checked, then the room type every room
consumer accepts is mutually assignable with the contract-generated room type. So a
room with only an identifier and a name is accepted everywhere, and a hand-written
shape with an extra required field fails the check. *(Fails today: the hand-written
room requires an address field the generated one lacks.)*  [unit]

AC-7. Given the web code base, when it is type-checked, then the coverage status type
admits exactly the two values the contract declares, and the participant coverage type
has no provider member. Both are asserted by type-level tests against the generated
participant type. *(Fails today: a third status and a provider are declared.)*  [unit]

AC-8. Given a free/busy answer in which a co-host's status is "known", when the
scheduling-link drawer renders that co-host (not an accepted host on the saved link)
and the user opens the chip's tooltip, then the chip reads "Availability synced". In
that case nothing inside the chip names a calendar provider ("Google", "Microsoft",
"Outlook" or any other): not its visible text, its tooltip text, or any accessible
name, label, title or description. Given "unknown", the chip shows the unknown state.
*(Fails today: the tooltip says "via Google Calendar" for every caller.)*  [unit]

AC-9. Given a scheduling link whose saved host record shows a co-host as accepted with
a Paceday account, when the drawer renders that co-host, then the chip reads "On
Paceday" whatever the free/busy answer says. (Holds today; this pins it.)  [unit]

AC-10. Given a demo session, when room search and free/busy are called, then:
- every demo room carries only an identifier and a name;
- every demo room's identifier is a room address the live event save accepts (it
  matches `^[^@\s]+@resource\.calendar\.google\.com$`);
- every demo coverage status is "known" or "unknown", with no provider.

*(Fails today: demo rooms carry five more fields and their identifiers are `r1` to
`r7`.)*  [unit]

AC-11. Given the audit register after this work merges:
- API-040, API-041 and API-046 are marked Fixed with a reference to PAC-41;
- API-077 is unchanged by this work (still Reported, or as PAC-47 left it);
- API-047, API-075 and API-078 remain Reported.

If the OQ-3 follow-up issue exists when the build stage edits the register, each of
those last three rows names it. If it does not, each row carries the note "split from
PAC-41 (D-4); follow-up issue not yet filed", and AC-11 is met by that note. Either way,
none of the three is marked Fixed.  [contract]

AC-12. *(D-6, interim until FOLLOW-UP-ROOM-RELEASE.)* Given an event open for editing,
when the user is in room booking (searching for a room, or with a room selected), then
before they save the form shows text with two points: saving adds the chosen room and
does not release a room the event already holds, and a held room is released in the
calendar itself. *(Fails today: no such text exists.)*  [unit]

AC-13. *(D-6, interim until FOLLOW-UP-ROOM-RELEASE.)* Given the user entered room
booking during this edit, when they switch to free text or clear the selected room,
then before they save the form shows text saying that a room already booked on the event
stays booked. *(Fails today: no such text exists.)*  [unit]

AC-14. *(D-6, interim; pins the outcome D-6 accepts. FOLLOW-UP-ROOM-RELEASE replaces
it and does not delete it silently.)* Given an event on a signed-in Google user's
calendar that holds room "Helsinki" booked through Paceday:
- (a) when the user reopens it, picks room "Oslo" and saves, then the stored event holds
  both Helsinki and Oslo as resource attendees;
- (b) when the user reopens it, sets the location to free text "Café" and saves, then
  the stored event still holds Helsinki as a resource attendee and its location is
  "Café".

*(Not reachable today, because no room is ever booked. It becomes reachable with
AC-1.)*  [e2e]

AC-15. Given a signed-in Google user whose calendar list contains a room resource
calendar named "Helsinki", when the room list is read, then the 200 answer is a JSON
array. Its element for Helsinki has exactly the two keys the contract names for a room,
an identifier equal to the room's resource calendar address, and the name "Helsinki".
A change to the room's wire key set fails a test. *(Holds today, but no test pins it;
see §2.5. This pins it.)*  [contract]

AC-16. *(Revision 2: the exceptions changed.)* Given room booking is open:
- when the room list answers "calendar not connected", the picker says room booking
  needs a connected Google calendar;
- when it fails in any other way (an answer saying the server failed, which includes a
  stored Google connection that can no longer be used, or no answer because the server
  is unreachable), the picker says the rooms could not be loaded.

In every one of these cases the picker shows neither "No rooms matched." nor "Start
typing to search rooms.", and the room address the form holds is unchanged. *(Fails
today: the failure is swallowed, and the list reads "No rooms matched." or "Start
typing to search rooms.")*  [unit]

Gate requirement (from the ticket; not a behaviour): every gate the implementation
touches prints rc=0 on the final commits. That means the api gate (if the audit
register is edited or the AC-15 test is added), the web gate, and the e2e gate for AC-1
and AC-14.

## 5. Explicitly out of scope

- **Room details: capacity, building and floor** (owner OQ-1, D-7). They become
  FOLLOW-UP-ROOM-DETAILS, its own spec. That ticket reads directory data that today
  needs admin-granted permissions, depends on PAC-68's optional "extended access" step,
  and must fall back gracefully. None of that is a contract-accuracy fix.
- **Room availability.** The owner's OQ-1 answer covers details, not availability.
  No ticket is filed for availability. D-3 removes the unverified badge.
- **Releasing or replacing an earlier room, the release-or-keep question, and the
  remembered choice as a user setting** (owner OQ-5, D-7). These become
  FOLLOW-UP-ROOM-RELEASE, its own spec. That ticket needs three things: loaded events
  that carry their room, a server path that releases a room, and a new user setting.
  Until it lands, D-6's interim applies.
- **Rooms for Outlook users.** Room booking reads Google room calendars only. Whether
  the owner's "from Google Workspace and Microsoft 365" means Microsoft 365 users get a
  room list at all is OQ-7, carried to FOLLOW-UP-ROOM-DETAILS.
- **A provider mark on coverage, or an "is on Paceday" coverage state** (D-2, OQ-6).
- **API-077.** Owned by PAC-47 (header, OQ-2).
- **A newly added co-host who is a Paceday user showing "On Paceday" before accepting.**
  Live behaviour already cannot show this, and nothing here makes it worse.
- **API-047, API-075, API-078** (D-4, OQ-3).
- **Outlook and WebCal free/busy.** PAC-47.

## 6. What must not change

- The server's free/busy answer: same fields, same statuses, same error bodies.
  Protected by `backend/api/pac26_wire_keys_test.go` and `handlers_freebusy_test.go`.
- The server's room list: same fields and same error bodies. Only its 401 is protected
  (`pac21_calendar_not_connected_test.go:87`). Its 200 body is protected **by no test**,
  which is a finding; AC-15 adds that test.
- Room address validation on event save (`handlers_events.go:47-49,190-195`), and PATCH
  keeping every room already on the event (`:94-144`). Both are covered by the PATCH
  tests PAC-29 added (`handlers_events_test.go:448-487`).
- Free-text location: choosing "Free text" still sends no room address. **No test
  protects this today**, which is a finding; a test belongs beside AC-2.
- The "On Paceday" and "Invite pending" chips from the host record (AC-9). **No test
  protects this today.**
- Busy-window handling used by teams and slot filtering
  (`coverageCache.ts:137-164`): only the status type narrows, and "unknown" is still
  excluded.
- PAC-83's rule that a failed call never shows invented data
  (`src/api/noInventedData.test.ts`). AC-16's messages are not data, and that test
  must stay green.

## 7. Rollback

There is no migration and no server change. Reverting the web PR restores the
hand-written types and the "Available" badge. The contract PR (descriptions and the
audit register) and the backend test revert independently. Rooms booked while the
change was live stay on their events after a revert, and nothing is deleted.

## 8. Open questions

- **OQ-1, answered (owner, 2026-10-27).** Becomes FOLLOW-UP-ROOM-DETAILS (D-7).
- **OQ-2, PAC-47 also inherits API-046 and API-077.** Owner: whichever of PAC-41 and
  PAC-47 reaches its build stage second. Resolve by reading the audit register:
  - If API-046 is already Fixed, the second ticket must keep it fixed and must not
    reintroduce a third status or a provider field.
  - API-077 belongs to PAC-47 alone. If PAC-47 is abandoned or re-scoped so that it no
    longer changes that operation, API-077 goes to the OQ-3 follow-up, not to PAC-41.
- **OQ-3, follow-up issue for API-047, API-075 and API-078, and confirmation of D-4.**
  Owner: product owner. Still unfiled. AC-11 has a fallback for this case.
- **OQ-4, can AC-1 and AC-14 be observed in e2e?** Owner: e2e-author at stage 5. The
  Google stand-in stores the event body it is given. Nobody has verified whether it
  keeps a resource attendee's flag, or returns an earlier room on the next read. If it
  does not, the stand-in gains that (stand-in code, not product code).
- **OQ-5, answered (owner, 2026-10-27).** Becomes FOLLOW-UP-ROOM-RELEASE (D-7).
- **OQ-6, confirm D-2.** Owner: product owner. Should "On Paceday" ever be decided from
  free/busy? Resolve by a yes/no on the Linear issue.
- **OQ-7, does OQ-1's "Microsoft 365" include a room list for Outlook users?**
  *(Revision 2.)* Owner: product owner, via FOLLOW-UP-ROOM-DETAILS. Today an Outlook-only
  user gets "room booking needs a connected Google calendar" (AC-16). This does not
  block PAC-41; AC-16's message stays true until that ticket changes the room source.
- **OQ-8, confirm D-6's interim and D-7's split.** *(Revision 2.)* Owner: product owner.
  The owner asked for a release-or-keep question. PAC-41 ships a notice instead and
  leaves the question to FOLLOW-UP-ROOM-RELEASE. Resolve by a yes/no on the Linear
  issue. A "no, ship them together" makes PAC-41 wait for FOLLOW-UP-ROOM-RELEASE and
  deletes AC-12 to AC-14.

## Decisions

### Owner answers (owner, 2026-10-27)

Recorded from `.claude/factory/owner-decisions.md` (2026-10-27). They override any
factory decision below.

- **OQ-1, yes.** "Rooms carry capacity (and building/floor where available) from
  Google Workspace and Microsoft 365." The data comes from the providers' directory data
  (Google Admin SDK Directory `resources.calendars`, Microsoft Graph
  `places/microsoft.graph.room`). If the needed admin permission is not granted, it
  falls back gracefully ("no capacity shown, nothing breaks"), and sign-in never starts
  requiring a new admin consent. Per the PAC-68 OQ-2 answer of the same day, an optional
  "extended access" step grants the broader permission, and before that step the app
  explains plainly what is lost without it.
- **OQ-5 / D-6.** "When a user changes or removes a room on an event, ask them whether
  to release the earlier room or keep it booked, with a 'remember my choice' option.
  The remembered choice is a user setting (Settings page) that can be changed or reset
  later." The answer notes that this needs server support for releasing a room.

### Factory decisions

*Each is the most conservative option consistent with PRODUCT.md and the owner answers
above, and each is **assumed by the factory, pending owner confirmation** unless marked
otherwise.*

- **D-1 (revised in Revision 2): PAC-41 does not grow the server's room.** The owner's
  OQ-1 answer sets the product direction: rooms will carry details. That is built by
  FOLLOW-UP-ROOM-DETAILS, not here. In PAC-41 the web shrinks to the `{id, name}` the
  server emits today. The room's identifier is its bookable address, so the user loses
  nothing they have today; every detail shown today is either empty or invented.
- **D-2: drop the third coverage status and the provider mark rather than implement
  them.** The server produces neither. Implementing "on Paceday" from free/busy would
  tell a caller whether an arbitrary address has a Paceday account. The real "On
  Paceday" chip, from the link's own host record, stays. Pending OQ-6.
- **D-3: remove the availability badge rather than show it unverified.** A badge that
  says "Available" for every room is worse than none: it is a claim the product never
  checked, and §1 sets out the cost when a user relies on it. This is the factory's
  reasoning, not a PRODUCT.md quote.
- **D-4: split API-047, API-075 and API-078 out.** They sit on other operations, and
  changing the org-member list would also inherit the `OrgMember` `x-uncertain`.
  Pending OQ-3.
- **D-5 (revised in Revision 2): demo mode follows the live shapes.** PRODUCT.md calls
  demo mode "the only built-in way to show the product" (`smart-calendar-flow/PRODUCT.md:83`),
  so it shows the product that exists. Demo rooms therefore lose building, floor,
  capacity and the invented busy rooms, and their identifiers become real room
  addresses (AC-10). Demo coverage loses "on Paceday" and the provider. The owner's OQ-1
  answer does not change this, because no live room carries details until
  FOLLOW-UP-ROOM-DETAILS lands. That ticket restores demo details in the same change
  that gives them to live rooms.
- **D-6 (revised in Revision 2): interim until FOLLOW-UP-ROOM-RELEASE. An earlier room
  stays booked, and the user is told before saving.** The owner wants the user asked
  whether to release or keep. That needs a server release path and loaded events that
  carry their room, and neither exists. PAC-41 must not start booking rooms silently
  (PRODUCT.md principle 3, `PRODUCT.md:89`: changes are "visible and confirmable, never
  silent"), so it tells the user (AC-12, AC-13) and pins the outcome (AC-14).
  - This is a subset of the owner's answer, not a contradiction of it. "Keep" is the
    only option PAC-41 can offer, and the notice says so.
  - Residual gap: a user who reopens an event and edits only the location text, never
    entering room booking, sees no notice, because the form cannot know a room is held.
    FOLLOW-UP-ROOM-RELEASE closes this.
  - Pending OQ-8.
- **D-7 (new in Revision 2): split the owner's two answers into two new tickets, and
  keep PAC-41 to contract accuracy.**
  - Each answer needs things PAC-41 does not have:
    - room details need directory reads, new permissions, an extended-access step
      (PAC-68) and graceful fallback;
    - release-or-keep needs a server release path, loaded events that carry their room,
      a new user setting and a Settings control.
  - Each also changes the contract's room or event shapes.
  - Bundling them would turn a description-only contract and a web cleanup into three
    features. It would also hold the fix for "the room is never booked" behind work
    that needs new admin consent.
  - Shipping PAC-41 alone leaves a coherent product. Rooms are booked, the user is told
    the truth about earlier rooms, and nothing claims data the product does not have.
  - Pending OQ-8.

## Revision 2 — 2026-11-13

*spec-author. This revision applies the owner answers of 2026-10-27 and re-reads every
citation on api `68540f6` and web `ae72939`.*

- **Scope (D-7, new).** PAC-41 is now a contract-accuracy ticket. The owner's OQ-1
  (room details) and OQ-5 (release or keep, with a remembered setting) each become their
  own ticket and spec: FOLLOW-UP-ROOM-DETAILS and FOLLOW-UP-ROOM-RELEASE. §5 and §8 say
  so, and OQ-8 asks the owner to confirm the split.
- **D-1, D-5, D-6 revised.** D-1 is narrowed to "PAC-41 does not grow the room". D-5
  keeps demo rooms at `{id, name}` until live rooms carry details. D-6 becomes the
  interim until FOLLOW-UP-ROOM-RELEASE. AC-3 and AC-12 to AC-14 are marked as interim,
  and each names the ticket that replaces it.
- **AC-16 rewritten.** PAC-83 removed the fallback to demo data when the server is
  unreachable (`src/api/demoGate.ts:37-50`). Revision 1's exception ("an unreachable
  server keeps falling back to demo data") is therefore false on main, and an
  unreachable server now reaches the "could not be loaded" message. AC-16 also states
  the 401-versus-500 split from contract revision 3: an unusable stored Google
  connection (a PAC-67 loss, or one PAC-84 cannot decrypt) is the 500, not "calendar not
  connected". §2.4a cites the code for both cases.
- **§2 re-cited.** Demo data moved to `src/demo/clientMocks.ts`, and room types, client,
  picker, badge and drawer lines all moved. §2.5 notes PAC-83's
  `noInventedData.test.ts:61`, the only test that names room search. §6 adds that test
  to what must not change.
- **OQ-7 (new).** Does "Microsoft 365" in OQ-1 mean Outlook users get a room list?
  Carried to FOLLOW-UP-ROOM-DETAILS.
- Unchanged: AC-1, AC-2, AC-4 to AC-11 and AC-15 (wording identical apart from AC-10's
  "demo session" and AC-11's "build stage"), D-2, D-3, D-4, OQ-2 to OQ-4, OQ-6.

## History

*The sections below are the record of Revision 1 and its challenge, kept as written. Line citations in them refer to api `6d751f9` and web `ad52160`.*

### Challenge — 2026-10-27

*spec-challenger, reviewing the committed spec at `7615006` against api `6d751f9`,
web `ad52160` and the PAC-41 Linear issue text. I did not see the author's
reasoning beyond this file.*

**Verdict**: accept-with-changes

#### Blocking

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

#### Non-blocking

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

#### Criteria I could not falsify

- None. Every AC from AC-1 to AC-10 has a test that fails on `ad52160` and passes
  after the change. AC-9 is a stated pin, not a change, and is fine as such. AC-1 is
  falsifiable only once OQ-4 is settled. The stand-in stores the event body it is
  given (`e2e/fake-google/events.go:45-57`), but nothing yet asserts that it keeps
  `resource: true`. AC-11 depends on OQ-3 (non-blocking 5).

#### Citations I checked and found wrong

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

#### Consumers this breaks

- None, as long as the server stays unchanged as specified. Consumers I checked:
  - `clockwise-like/mcp/tools.go:207-215` (`clockwise_search_rooms`) passes only `q`
    and relays the body.
  - In the web: `contract.ts:64,106`, `client.ts:982-986,1416`, `coverageCache.ts`,
    `teams.ts:662` and `LinkEditDrawer.tsx:177`.
  - `contract.test.ts:92-106` already uses `status: "known"` with no provider.
  - `EventEditForm.test.tsx:25` mocks `LocationPicker` away, so it is unaffected.

  The narrowed `searchRooms` signature (AC-5) and the narrowed coverage types (AC-7)
  have no other callers on `ad52160`.

### Revision 1 — 2026-10-27

*spec-author, answering the challenge above. Each item says what changed and
where. Facts re-checked on api `6d751f9` and web `ad52160` (both still `main`).*

#### Blocking

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

#### Non-blocking

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
