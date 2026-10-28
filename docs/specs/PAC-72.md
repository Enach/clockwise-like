# Spec — PAC-72: Public booking accepts only a start the slot listing would offer

- **Linear**: https://linear.app/paceday/issue/PAC-72/public-booking-never-checks-host-availability-the-links-hours-or-a
- **Status**: draft (awaiting spec-challenger)
- **Author**: spec-author
- **Inherited scope**:
  - **API-094** (medium, Confirmed, `docs/factory/api-audit.md:352`), on
    `POST /api/book/{slug}`: no availability, window or date-bound check on a
    public booking. **Resolved by this spec.**
  - **API-051** (medium, Reported, `api-audit.md:309`), also on
    `POST /api/book/{slug}`: `uses_count` is recomputed from live bookings, so
    cancelling a booking re-opens an exhausted single-use link. **Not resolved
    here**: it is owned by PAC-40 (spec branch `factory/pac-40-spec`,
    `docs/specs/PAC-40.md` §2.4), concerns exhaustion rather than which start is
    offerable, and was deferred to PAC-40 on the same grounds by
    NEW-booking-rate-limit (`contracts/features/NEW-booking-rate-limit.md:122`).
    The challenger should confirm this deferral is acceptable under the
    touch-it-fix-it rule.
  - **`x-uncertain`**: none. Checked in the bundle: `getPublicLinkInfo`,
    `createPublicBooking` and `getPublicBookingSlots` carry no `x-` extension.

## 1. Problem

Anyone with a scheduling link can book any time at all, as long as it is after
the link's minimum notice. The booking page only *shows* the times the hosts are
free, but the server does not check that the time it receives is one of them.
A visitor who sends the request directly (or a page left open while the hosts'
calendars change) can therefore book:

- over a host's existing meeting, focus time, or another booking;
- on a day the link is not open, or outside the link's hours (3 a.m., Sunday);
- years ahead.

Each such booking is written to every host's calendar and invites the
booker-chosen address. The hosts lose the guarantee the product promises them:
that a scheduling link only fills the time they made available. The rate limit
(NEW-booking-rate-limit: 20 bookings per client per 10 minutes) only slows a
flood; it does not stop one booking landing on top of a board meeting.

## 2. Current behaviour

All citations are against `origin/main` at `69ffa92`.

**What the booking request checks** (`backend/api/handlers_booking.go:216-314`):

1. Rate limit, first (`:219`), then link lookup by slug with `active = true`
   (`:222`, contract `booking.yaml:17`), 404 / 410 for unknown / exhausted
   (`:227-234`).
2. Body shape: name, email, RFC 3339 start (`:244-257`); an end or a duration,
   which must agree (`:258-286`); the duration must be one of the link's
   options, else **400 "invalid duration"** (`:287-290`, `durationAllowed`
   `:124-137`).
3. Minimum notice only: `slotAllowed` is
   `!slot.Start.Before(now + min_notice)` (`:139-141`); failure answers
   **422 "booking is too soon"** (`:295-298`).
4. Overlap with **this link's own** non-cancelled bookings only
   (`HasOverlappingBooking`, `backend/storage/scheduling_links.go:404-411`);
   failure answers **409 "slot is no longer available"** (`:299-305`).
5. `ConfirmBooking` (`backend/engine/booking.go:216-281`). Its first comment is
   "Re-verify availability." (`:223`), followed only by loading the hosts
   (`:224`). It writes the booking row (`:229-239`), then attempts a calendar
   event on each accepted host's calendar with the booker as a guest
   (`:244-274`), best effort, then sends an email (`:277`).

Nothing checks the hosts' calendars, focus blocks or other links' bookings, the
link's days or hours, the 15-minute slot grid, or any upper date bound.

**What the slot listing offers** (`handlers_booking.go:144-213`):

- Duration: same `durationAllowed` rule, failure **400 "invalid duration"**
  (`:163-173`).
- **Without `date`**: probes `now.AddDate(0,0,i)` for `i` in `0..59`
  (`:176-190`) — today and the next 59 days, in the server process's local
  zone — and lists a day if at least one slot passes the notice filter.
- **With `date`**: parses `YYYY-MM-DD` as a UTC midnight (`:195`) and returns
  every slot passing the notice filter (`:200-210`). **There is no upper bound**:
  a date ten years ahead is computed and answered.
- Slots come from `CollectiveSlots` (`engine/booking.go:75-112`): candidate
  starts every 15 minutes from the link's window start while start+duration
  fits in the window end (`:104`), in the location of the `date` passed in
  (`parseTime`, `:180-189`); a candidate is dropped if it overlaps the merged
  busy time of all accepted hosts (`:91-99, :107`).
- A host's busy time (`hostBusy`, `:114-169`) is: non-transparent calendar
  events (`:122-136`), those events widened by the link's buffers (`:139-148`;
  buffers apply to calendar events only), the host's own focus blocks (`:153-158`,
  PAC-64), and confirmed bookings on **any** link the host owns or co-hosts
  (`:161-166`, query at `storage/scheduling_links.go:442-467`).
- **Per-host failures are tolerated**: a host whose calendar client cannot be
  built or whose event list fails contributes no calendar busy time (`:123-125`,
  `:95` "tolerate per-host errors"). The link-info read exposes this as
  `coverage.checked < coverage.total` (`handlers_booking.go:87-90`).
- **The link's days of week are ignored.** `SchedulingLink.DaysOfWeek`
  (`storage/scheduling_links.go:20`, 0 = Sunday … 6 = Saturday, validated at
  `api/handlers_scheduling_links.go:225-236`) is read nowhere in the booking or
  slot code (grep for `DaysOfWeek` outside generated code and habits finds only
  the links CRUD). The e2e date helper records this bug
  (`e2e/lib/dates.ts`, `nextBookableWeekday` comment).

**Time zone.** The link's hours have no zone of their own. The explicit-date
listing evaluates them in UTC; the date-less listing in the process's local
zone. The backend image sets no `TZ` (`backend/Dockerfile:3` installs tzdata
only; neither compose file sets `TZ` for the API), so both are UTC in every
deployment today; the e2e stack runs the backend with `TZ=UTC`
(`e2e/lib/dates.ts` header).

**Booking statuses.** The column allows `pending`, `confirmed`, `cancelled`
(`storage/migrations/009_scheduling_links.up.sql:33`); rows are inserted with
the default `confirmed` (`storage/scheduling_links.go:395-402`) and no code
updates a booking's status, so every booking is `confirmed` today.

**Concurrency.** Nothing makes check-then-write atomic: there is no exclusion
constraint on bookings (migration 009), so two simultaneous requests for the
same start can both pass the overlap check today.

**The web app** (`smart-calendar-flow/src/pages/PublicBooking.tsx:179-201`):
on 409 it shows the server's error text, else "This slot was just taken —
please pick another time.", clears the chosen slot and refetches slots; on 422
it shows the server's text, else an advance-notice message, and refetches; on
400 it shows the server's text. Its date picker only enables dates the
date-less listing returned (`:411-417`). It only sends durations from the
link's list.

**Contract** (`contracts/openapi/paths/booking.yaml:50-150`): the
`createPublicBooking` description states API-094 as open ("Not checked …");
responses list 409 (own-link overlap) and 422 (too soon). The
`getPublicBookingSlots` description says the date-less mode "probes the next 60
calendar days" and documents no bound for an explicit date (`:152-230`).

**Tests that pin today's behaviour**: `TestPublicBookingErrorsContract`
(`backend/api/handlers_booking_test.go:180-215`) pins
`422 "booking is too soon"` (`:198`) — this case is an intended change and must
be amended, not deleted. `TestPublicBookingPropagatesEngineFailure` (`:217`)
pins 500 on engine failure. e2e `public-booking.spec.ts` pins: full window
offered when no host is busy (`:100`), 400 for an unoffered duration (`:121`),
201 then 409 for the same slot (`:131-185`), the lost-race message in the UI
(`:281`).

## 3. Desired behaviour

A public booking succeeds only when its start time and duration are exactly a
slot that the slot listing would offer for that link, for that duration, at
the moment the booking request is handled. "Would offer" is one rule, used by
both the listing and the booking, made of:

1. the link's duration options;
2. the link's days of the week and daily hours, on the 15-minute grid the
   listing uses;
3. every accepted host being free for the whole slot: no calendar event that
   marks them busy (widened by the link's buffers), no focus block of theirs,
   and no confirmed booking on any link they own or co-host;
4. the minimum notice;
5. the booking horizon: today and the next 59 days (60 calendar days),
   counted in the zone the link's hours are evaluated in.

Rules 2 and 5 are new for the listing as well:

- The listing stops offering slots on days that are not among the link's days.
  A visitor of a Monday-to-Friday link no longer sees Saturday slots.
- A listing for an explicit date beyond the horizon answers successfully with
  no slots, exactly like a fully booked day — not an error.

Any start that the rule does not offer is refused with **409**, and nothing is
written: no booking row, no calendar event on any host's calendar, no email.
This includes a start inside the minimum notice, which today answers 422.
Every 409 carries the same refusal text, so a refusal does not tell the
visitor which rule failed (for example, that a host has a meeting then). The
web app needs no change: its 409 handling already tells the visitor the slot
is gone, clears the choice and reloads the times.

A duration the link does not offer keeps today's **400**, the same answer the
listing gives for that duration (decision D-3).

If the rule itself cannot be computed for the booking (a storage failure while
gathering the hosts or their busy time), the booking is refused with a server
error and nothing is written — it fails closed. A host whose calendar cannot be
read is treated exactly as the listing treats them today (no calendar busy time
from that host): the listing and the booking must agree, and changing that
tolerance is out of scope (§5, OQ-2).

What a visitor using the booking page sees does not change for any slot the
page showed them, except that weekend (non-link-day) times disappear from links
that excluded them.

## 4. Acceptance criteria

All criteria use a fixed clock and a controlled calendar state; "the hosts"
means the link's accepted hosts. "Nothing is written" means: no new booking
row for any link, no calendar event created on any host's calendar (asserted
with the PAC-43 calendar fake: at unit level a fake calendar that records every
create call, at e2e level the fake Google server's recorded insert calls), and
no confirmation email attempted.

**Offered slots still book**

AC-1. Given a link whose hosts are free all day and a start the slot listing
returns for the requested date and duration, when that start is booked with
that duration, then the answer is 201 with today's confirmation body, one
booking row exists, and each host with a readable calendar receives one event.
[unit] [contract]

**Host busy → 409, nothing written** (one test per busy source)

AC-2. Given host A has a non-transparent calendar event overlapping the
requested slot, when the slot is booked, then 409 and nothing is written.  [unit]

AC-2b. Given a link with a 15-minute buffer after events and a host event
ending exactly at the requested start, when that start is booked, then 409 and
nothing is written.  [unit]

AC-3. Given host A has a focus block overlapping the requested slot, when the
slot is booked, then 409 and nothing is written.  [unit]

AC-4. Given host A is an accepted co-host of a *different* link that already
has a confirmed booking overlapping the requested slot, when the slot is booked
on this link, then 409 and nothing is written.  [unit]

AC-5. Given a link with two accepted hosts where only the co-host (not the
owner) is busy, when the slot is booked, then 409 and nothing is written.  [unit]

AC-6. Given a confirmed booking on this same link overlapping the requested
slot, when it is booked again, then 409 (today's behaviour, preserved).  [unit]

**Outside the link's configuration → 409, nothing written** (each a separate test)

AC-7. Given a link open Monday to Friday, when a start on a Saturday that is
inside the daily hours, after the notice and within the horizon is booked, then
409 and nothing is written.  [unit]

AC-8. Given a link open 09:00–17:00, when a start of 08:45, or a start whose
end with the requested duration is after 17:00 (e.g. 16:45 for 30 minutes), is
booked, then 409 and nothing is written (two cases).  [unit]

AC-9. Given a link open 09:00–17:00 with every host free, when a start of
09:05 (inside the hours but off the 15-minute grid) is booked, then 409 and
nothing is written.  [unit]

AC-10. Given the clock at day D, when a start on day D+60 that would otherwise
be offered (link day, inside hours, hosts free) is booked, then 409 and nothing
is written; and a start on day D+59 under the same conditions is 201.  [unit]

AC-11. Given a link with 60 minutes minimum notice and the clock at 10:00, when
a start of 10:45 is booked, then **409** (not 422) with the common refusal
text, and nothing is written; a start of 11:00 that is otherwise offered is
201.  [unit]

AC-12. Given a link whose durations are 30 and 60, when a booking asks for 45
minutes (by duration or by end), then 400 "invalid duration", as today, and
nothing is written.  [unit]

AC-13. Given any two different refusal causes from AC-2 to AC-11, when each is
booked, then both 409 bodies are identical.  [unit]

**One rule for listing and booking**

AC-14. Given a fixed clock, a link with days, hours, buffers, notice and two
hosts, and a calendar state with events, focus blocks and another link's
booking across several days, when every start the listing returns for each
date in the horizon and each duration is booked in a fresh copy of that state,
then every one is accepted; and when every 15-minute-aligned start the listing
does not return for those dates (plus one date past the horizon and one
off-grid start per day) is booked, then every one is refused with 409 (or 400
for an unoffered duration), with nothing written.  [unit]

AC-15. Given a link open Monday to Friday, when the date-less listing is read,
then no Saturday or Sunday appears in the available dates; and when the listing
is read for an explicit Saturday date, then it answers 200 with no slots.  [unit] [contract]

AC-16. Given the clock at day D, when the listing is read for explicit date
D+60 (or any later date), then it answers 200 with no slots; for D+59 it
returns the day's slots; and the date-less listing never lists a date after
D+59.  [unit] [contract]

AC-17. Given the hosts' busy time cannot be gathered because of a storage
failure, when a booking is made, then the answer is 500 and nothing is
written.  [unit]

**Unchanged ordering**

AC-18. Given a client over the booking rate limit, an unknown slug, an
exhausted link, or a malformed body, when a booking is made for a start the
rule would refuse, then the answer is 429, 404, 410 or 400 respectively, as
today — the 409 check comes after all of them.  [unit]

**Contract and end-to-end**

AC-19. Given the contract, when the booking operation is read, then its 409
response lists every refusal cause of §3 (host busy by calendar, focus block or
any booking; outside days or hours or grid; beyond the horizon; inside the
minimum notice), it no longer documents 422, its description no longer states
API-094 as open, and the listing operation documents the link-day filter and
the horizon for explicit dates. API-094 is marked fixed in the audit register.
[contract]

AC-20. Given the full stack with the fake calendar, when an e2e test books a
slot the listing offered, then 201; and when it books an unoffered start on the
same link (a start overlapping an event the fake calendar reports for a host),
then 409, the database has no booking for that start, and the fake calendar
records no event insert for it.  [e2e]

## 5. Explicitly out of scope

- **Concurrent bookings** of the same start (two requests both passing the
  check before either writes). Exists today for same-link overlaps too (§2,
  no exclusion constraint). Closing it needs a storage-level guarantee and its
  own spec; proposed as a follow-up issue (OQ-3).
- **Hosts whose calendar cannot be read** keep being treated as free in both
  the listing and the booking (OQ-2).
- **A time zone for the link's hours.** Hours stay evaluated in the server's
  zone (UTC in every deployment). A per-link or per-host zone is a separate
  feature.
- **A configurable horizon** per link (a new link setting and UI). D-1 fixes
  60 days for everyone.
- **API-051** (exhaustion counting) — PAC-40.
- **Buffers around focus blocks and bookings.** Buffers continue to widen
  calendar events only, as the listing does today; the rule is shared, not
  redesigned.
- **Any web change.** The paired web PR only regenerates types.

## 6. What must not change

- The 201 response body and the best-effort calendar writes for an offered
  slot (AC-1; e2e `public-booking.spec.ts:131-185`, unit
  `TestPublicBookingCreateContract`).
- 400 messages and their precedence over 409 (AC-12, AC-18;
  `TestPublicBookingErrorsContract`, e2e `:121`, `:190`).
- 404 / 410 for unknown and exhausted links (e2e `:74-98`, `:200`).
- Rate-limit ordering: 429 first, every request counted
  (NEW-booking-rate-limit tests).
- 500 on engine failure (`TestPublicBookingPropagatesEngineFailure`).
- The listing's response shape, both keys always present, and its answers on
  link days within the horizon (e2e `:100-117`).
- The lost-race UI message (e2e `:281`).
- `TestPublicBookingErrorsContract`'s "booking rejects too soon" case changes
  from 422 to 409 by design (AC-11). This is the one existing assertion this
  spec changes; it is amended, not removed.

## 7. Rollback

No migration and no stored data change. Reverting the backend PR restores
today's acceptance of any start after the notice (and reopens API-094);
reverting the contract PR restores the 422 documentation, and the paired web
type regeneration is reverted with it. The web app handles both 409 and 422,
so either order of revert is safe for visitors. No booking made while the
change was live is altered by a revert.

## 8. Open questions

- **OQ-1 — Owner: product owner.** Confirm D-1 (60-day horizon for both listing
  modes), D-2 (422 becomes 409) and D-3 (unoffered duration stays 400).
  Resolve by an answer in `.claude/factory/owner-decisions.md`.
- **OQ-2 — Owner: product owner.** Should a booking (and the listing) treat a
  host whose calendar cannot be read as busy rather than free? Today's
  fail-open lets a link with an unconnected co-host book over that co-host's
  real meetings. Changing it hides every slot on such links, so it is not
  assumed here. Resolve by owner answer; if "busy", a follow-up spec.
- **OQ-3 — Owner: triage.** File a follow-up issue for concurrent double
  booking across links sharing a host (§5). Resolve by a PAC issue linked from
  API-094's fix note.
- **OQ-4 — Owner: contract-author.** Whether the refusal text stays today's
  "slot is no longer available" for all causes or becomes a new uniform text;
  the spec only requires one text for every 409 (AC-13), and the web shows it
  verbatim.

## Decisions

- **D-1 — Booking horizon: 60 calendar days (today and the next 59), the same
  for the date-less listing, the explicit-date listing and booking. An
  explicit date beyond it lists no slots (200), not an error.** Assumed by the
  factory, pending owner confirmation (2026-10-28). No horizon field exists on
  links; 60 is the only bound in the product today (the date-less listing), and
  applying it everywhere narrows what can be booked without adding a setting.
  Answering "no slots" rather than 400 keeps the listing's shape and errors
  unchanged for existing clients.
- **D-2 — Inside the minimum notice answers 409, not 422.** Assumed by the
  factory, pending owner confirmation (2026-10-28). The Linear issue, written
  by the product owner, asks for 409 for every start the listing would not
  offer; a single status means one refusal path in clients. The web app already
  handles 409 (clears the slot, reloads times).
- **D-3 — A duration the link does not offer stays 400 "invalid duration".**
  Assumed by the factory, pending owner confirmation (2026-10-28). It is a
  request that can never succeed at any time, it is what the listing answers
  for the same duration, the web app never sends one, and keeping it changes
  nothing for any client. The issue lists "a duration that isn't offered" among
  the refused cases; it is refused, with nothing written, just not with 409.
- **D-4 — The link's days of the week apply to the listing and the booking.**
  Follows from the issue ("the link's days and hours"); narrows what is offered.
  Recorded as assumed by the factory, pending owner confirmation, because it
  changes what the listing shows today.
- **D-5 — Every 409 carries the same text**, so a refusal does not reveal which
  host constraint failed. Assumed by the factory, pending owner confirmation;
  the most conservative choice about what a refusal discloses.
- **D-6 — An unreadable host calendar is treated as today (free) in both
  paths.** Assumed by the factory, pending owner confirmation; see OQ-2.
