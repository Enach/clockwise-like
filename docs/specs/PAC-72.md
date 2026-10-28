# Spec — PAC-72: Public booking accepts only a start the slot listing would offer

- **Linear**: https://linear.app/paceday/issue/PAC-72/public-booking-never-checks-host-availability-the-links-hours-or-a
- **Status**: challenged (accept-with-changes at `d9be46b`); revision 1 applied, see "Revision — 2026-10-28"
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
    The challenger accepted this deferral (challenge, non-blocking 7). Under
    factory §7 rule 2 the contract keeps describing API-051 as open on this
    operation; this work must not remove that text.
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

All citations are against `origin/main` at `69ffa92` (backend, contract, e2e)
and `smart-calendar-flow` `origin/main` at `aa5c52e` (web).

**What the booking request checks** (`backend/api/handlers_booking.go:216-314`):

1. Rate limit, first (`:219`), then link lookup by slug with `active = true`
   (`:222`, contract `booking.yaml:17`), 404 / 410 for unknown / exhausted
   (`:227-234`).
2. Body shape: name, email, RFC 3339 start (`:244-257`); an end or a duration,
   which must agree (`:258-286`); the duration must be one of the link's
   options, else **400 "invalid duration"** (`:287-290`, `durationAllowed`
   `:124-137`). When an end is sent, the length is
   `int(end.Sub(start).Minutes())` (`:272`), **truncated to whole minutes**:
   an end of start + 30 min 30 s counts as a 30-minute booking, passes the
   duration check, and is stored with the 30 min 30 s end. The start is
   accepted with any seconds and any UTC offset (`time.RFC3339`, `:253`).
3. Minimum notice only: `slotAllowed` is
   `!slot.Start.Before(now + min_notice)` (`:139-141`); failure answers
   **422 "booking is too soon"** (`:295-298`).
4. Overlap with **this link's own** bookings whose status is not `cancelled`
   (`HasOverlappingBooking`, `backend/storage/scheduling_links.go:404-411`);
   a storage failure answers **500 "booking conflict check failed"**, an
   overlap **409 "slot is no longer available"** (`handlers_booking.go:299-305`).
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
  zone — and lists a day if at least one slot passes the notice filter. A day
  whose slot computation returns an error is **silently skipped** (`:181-183`).
- **With `date`**: parses `YYYY-MM-DD` as a UTC midnight (`:195`) and returns
  every slot passing the notice filter (`:200-210`); a slot computation error
  answers **500 "internal error"** (`:200-204`). **There is no upper bound**:
  a date ten years ahead is computed and answered. `available_dates` is always
  `[]` in this mode (`:212`).
- Slots come from `CollectiveSlots` (`engine/booking.go:75-112`): candidate
  starts every 15 minutes from the link's window start while start+duration
  fits in the window end (`:104`), in the location of the `date` passed in
  (`parseTime`, `:180-189`); a candidate is dropped if it overlaps the merged
  busy time of all accepted hosts (`:91-99, :107`). The accepted-host list is
  read first (`:81`); a failure there is the **only** error `CollectiveSlots`
  can return (`:82-84`). With **zero accepted hosts** nothing is busy and
  every candidate in the window is offered; a booking on such a link is written
  to no host's calendar. (The owner is an accepted host only because link
  creation inserts it, `api/handlers_scheduling_links.go:373`.)
- A host's busy time (`hostBusy`, `engine/booking.go:114-169`) is:
  non-transparent calendar events listed for [window start, window end]
  (`:122-136`), those events widened by the link's buffers (`:139-148`;
  buffers apply to calendar events only), the host's own focus blocks
  (`:153-158`, PAC-64), and bookings with status `confirmed` on **any** link
  the host owns or co-hosts, including this one (`:161-166`, query at
  `storage/scheduling_links.go:442-467`). Because events are listed only for
  the window, an event that ends before the window opens is never seen, so its
  buffer-after cannot block the first slots.
- **Every failure inside `hostBusy` is swallowed; it never returns an error**
  (`return busy, nil`, `:168`):
  - calendar client not built, or event list failed: no calendar busy time
    from that host (`:123-125`);
  - focus-block read failed: no focus busy time (`:153-154`, only
    `if err == nil` is handled);
  - bookings read failed: no booking busy time from any link (`:161-162`,
    same pattern).

  The `continue // tolerate per-host errors` branch in `CollectiveSlots`
  (`:93-96`) is therefore unreachable. Today, when the focus-block or bookings
  query fails, **both listing modes offer slots over those focus blocks and
  bookings**, with no error. The link-info read exposes only the calendar case,
  as `coverage.checked < coverage.total` (`handlers_booking.go:87-90`).
- **The link's days of week are ignored.** `SchedulingLink.DaysOfWeek`
  (`storage/scheduling_links.go:20`, 0 = Sunday … 6 = Saturday, validated at
  `api/handlers_scheduling_links.go:225-236`) is read nowhere in the booking or
  slot code (grep for `DaysOfWeek` outside generated code and habits finds only
  the links CRUD). The e2e date helper records this bug
  (`e2e/lib/dates.ts:34-37`, `nextBookableWeekday` comment).

**Time zone.** The link's hours have no zone of their own. The explicit-date
listing evaluates them in UTC (`:195`); the date-less listing in the process's
local zone (`time.Now()`, `:174-179`). The backend image sets no `TZ`
(`backend/Dockerfile:3` installs tzdata only; neither compose file sets `TZ`
for the API), so both are UTC in every deployment today; the e2e stack runs
the backend with `TZ: UTC` (`e2e/docker-compose.e2e.yml:111`). An operator who
set `TZ` would make the two modes disagree.

**Booking statuses.** The column allows `pending`, `confirmed`, `cancelled`
(`storage/migrations/009_scheduling_links.up.sql:33`); rows are inserted with
the default `confirmed` (`storage/scheduling_links.go:395-402`) and no code
updates a booking's status, so every booking is `confirmed` today. The two
overlap reads differ on paper: this link's check counts every non-`cancelled`
booking (`:404-411`), the cross-link busy read only `confirmed` (`:442-467`).

**Concurrency.** Nothing makes check-then-write atomic: there is no exclusion
constraint on bookings (migration 009), so two simultaneous requests for the
same start can both pass the overlap check today.

**The web app** (`smart-calendar-flow/src/pages/PublicBooking.tsx`). The slots
query runs only once a date is picked and always sends that `date`
(`:138-145`, `enabled: !!dateStr && !!duration`); the page **never calls the
date-less listing**. It reads `available_dates` from the explicit-date answer,
which is always `[]` (`handlers_booking.go:212`), so `availableDates.size` is 0
and the date picker disables only past days: **every future date is
selectable** (`PublicBooking.tsx:411-418`). For a picked day with no slots it
shows "No time available on this day — try another date." (or the "for all
hosts" variant, `:455-456`). On a booking answer of 409 it shows the server's
error text, else "This slot was just taken — please pick another time.", clears
the chosen slot and refetches slots (`:186-189`); on 422 it shows the server's
text, else an advance-notice message, and refetches (`:190-192`); on 400 it
shows the server's text. It only sends durations from the link's list, and
starts taken from the listing's answer.

**Contract** (`contracts/openapi/paths/booking.yaml`): the
`createPublicBooking` description states API-094 as open ("Not checked …");
responses list 409 (own-link overlap) and 422 (too soon) (`:50-150`). The
booking rate-limit 429 text lists the statuses a counted request may otherwise
get, including 422 (`:63`). The request's `start` field says "else 422"
(`:1653`). The `getPublicBookingSlots` description says the date-less mode
"probes the next 60 calendar days" and documents no bound for an explicit date
(`:152-230`); the `available_dates` field says "days within the next 60 that
have at least one bookable slot" (`:1627-1628`).

**Tests that pin today's behaviour**: `TestPublicBookingErrorsContract`
(`backend/api/handlers_booking_test.go:180-215`) pins
`422 "booking is too soon"` (`:198`). The unit fixtures create links open
Monday to Friday 09:00–17:00 (`createPublicBookingLink`, `:74-77`) and book at
`futureBookingStart()`, now + 72 h at 10:00 UTC (`:92-95`) — a Saturday or
Sunday on two days in seven. Tests that book at or after that start and assert
201 or 500: `TestPublicBookingCreateContract` (`:151`),
`TestPublicBookingPropagatesEngineFailure` (`:217`),
`TestCreateBooking_RateLimited` (`backend/api/ratelimit_booking_test.go:276-305`,
starts at +0 h … +19 h and +21 h, mostly after 17:00 or on later days),
`TestCreateBooking_ConcurrentBurstCannotOvershoot` (`:456-497`, 60 hourly
starts; counts any non-201, non-429 answer as "other"), and
`TestPAC67_PublicBookingWithARefusedHost`
(`backend/api/pac67_surfaces_test.go:300-355`, books hours after that start).
The handler tests drive the handler through `fakeBookingFlow` /
`countingBookingFlow`, whose slot stubs return no or fixed slots. e2e
`public-booking.spec.ts` pins: full window offered when no host is busy
(`:100`), 400 for an unoffered duration (`:121`), 201 then 409 for the same
slot (`:131-185`), the lost-race message in the UI (`:281`, `:327`). Other e2e
flows book or list on link days within 60 days: `seam.spec.ts:283-296`,
`time-format.spec.ts:113-120`, `scheduling-links.spec.ts:74-94`.

## 3. Desired behaviour

A public booking succeeds only when the interval it would store — from its
start to its end — is **exactly** a slot that the slot listing would offer for
that link, for that duration, at the moment the booking request is handled.
"Would offer" is one rule, used by both listing modes and the booking, made of:

1. the link's duration options;
2. the link's days of the week and daily hours, on the 15-minute grid the
   listing uses, counted from the window's opening time, in whole minutes with
   zero seconds;
3. every accepted host being free for the whole slot: no calendar event that
   marks them busy (widened by the link's buffers), no focus block of theirs,
   and no booking that is not cancelled (that is, pending or confirmed) on any
   link they own or co-host, this link included;
4. the minimum notice;
5. the booking horizon: today and the next 59 days (60 calendar days);
6. the link having at least one accepted host. A link with none offers no slot
   in either listing mode and accepts no booking (D-10).

**One zone.** Rules 2 and 5 are evaluated in UTC, for both listing modes and
the booking, whatever zone the server process runs in (D-9). This is what every
deployment does today; it stops an operator's process zone from making the two
listing modes and the booking disagree.

**Exactness.** A start is compared as an instant: a start sent with another UTC
offset that denotes a grid instant (for example 11:00 at UTC+01:00 for a 10:00
UTC slot) is the same start and is accepted. A start with non-zero seconds or
fractions of a second is not on the grid and is refused with 409. When an end
is sent, end minus start must be exactly one of the link's durations; a length
that is not a whole number of minutes, or not one the link offers, is refused
with 400 like any duration the link does not offer (D-3, D-8) — it is never
rounded to a duration that would be accepted. The stored booking's start and
end are exactly the slot's.

Rules 2, 3 (pending bookings), 5 and 6 and the single zone apply to the listing
as well; the visible ones today:

- The listing stops offering slots on days that are not among the link's days.
  A visitor of a Monday-to-Friday link no longer sees Saturday slots.
- A listing for an explicit date beyond the horizon answers successfully with
  no slots, exactly like a fully booked day — not an error.

Any start that the rule does not offer is refused with **409**, and nothing is
written: no booking row, no calendar event on any host's calendar, no email.
This includes a start inside the minimum notice, which today answers 422.
Every 409 carries the same refusal text, so a refusal does not tell the
visitor which rule failed (for example, that a host has a meeting then).

A duration the link does not offer keeps today's **400**, the same answer the
listing gives for that duration (decision D-3).

**When busy time cannot be gathered.** For each source the rule reads, a
failure is handled the same way by the booking and by the listing, so that
neither offers nor accepts a slot whose freedom was not checked (D-7):

| Source that cannot be read | Booking | Listing for an explicit date | Listing without a date |
|---|---|---|---|
| the link's accepted hosts | server error, nothing written | server error, as today | that day is not listed, as today |
| a host's focus blocks (storage failure) | server error, nothing written | server error (today: slots offered over the blocks) | that day is not listed (today: listed) |
| bookings on any link a host owns or co-hosts, this link included (storage failure) | server error, nothing written | server error (today: slots offered over the bookings) | that day is not listed (today: listed) |
| a host's external calendar (no connection, provider error, refused credential) | host treated as free for calendar events, as today; the booking proceeds if the rest of the rule offers the slot | host treated as free, as today | host treated as free, as today |

The last row keeps today's tolerance in both paths (D-6, OQ-2); the link-info
read keeps reporting it as incomplete coverage. A server error is never a 409:
a visitor is not told a slot is taken when the system could not tell.

**What a visitor using the booking page sees.** For any slot the page showed
them and that they book promptly, nothing changes. Three things do change, and
the web app is deliberately left as it is (§5):

- times on days the link is not open disappear (a picked Saturday on a
  Monday-to-Friday link shows the page's existing "No time available on this
  day" message);
- a picked date after today + 59 days shows the same "No time available on this
  day" message, where today it shows that day's slots — the page's date picker
  keeps every future date selectable, because it never receives the list of
  open dates;
- a start that became unavailable, or fell inside the minimum notice, while the
  page was open is answered with the slot-taken refusal instead of the
  advance-notice one; the page already clears the choice and reloads the times
  for both.

## 4. Acceptance criteria

All criteria use a fixed clock and a controlled calendar state; "the hosts"
means the link's accepted hosts. "Nothing is written" means: no new booking
row for any link, no calendar event created on any host's calendar (asserted
with the PAC-43 calendar fake: at unit level a fake calendar that records every
create call, at e2e level the fake Google server's recorded insert calls), and
no confirmation email attempted. Unless a criterion says otherwise, the server
process runs in UTC.

**Offered slots still book**

AC-1. Given a link whose hosts are free all day and a start the slot listing
returns for the requested date and duration, when that start is booked with
that duration, then the answer is 201 with today's confirmation body, one
booking row exists whose start and end equal the slot's, and each host with a
readable calendar receives one event.  [unit] [contract]

**Host busy → 409, nothing written** (one test per busy source)

AC-2. Given host A has a non-transparent calendar event overlapping the
requested slot, when the slot is booked, then 409 and nothing is written.  [unit]

AC-2b. Given a link with a 15-minute buffer after events and a host event
ending exactly at the requested start (both inside the window), when that start
is booked, then 409 and nothing is written.  [unit]

AC-3. Given host A has a focus block overlapping the requested slot, when the
slot is booked, then 409 and nothing is written.  [unit]

AC-4. Given host A is an accepted co-host of a *different* link that already
has a confirmed booking overlapping the requested slot, when the slot is booked
on this link, then 409 and nothing is written.  [unit]

AC-4b. Given host A hosts a different link that has a **pending** booking
overlapping the requested slot, when the slot is booked on this link, then 409
and nothing is written; and the listing for that date does not offer the
slot.  [unit]

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

AC-13. Given any two different refusal causes from AC-2 to AC-11, AC-21a and
AC-23, when each is booked, then both 409 bodies are identical.  [unit]

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

AC-17. **Busy time that cannot be gathered** — one test per source, each with
the failure injected for that source only and a slot that is otherwise
offered:

- AC-17a. The link's accepted hosts cannot be read: the booking answers 500
  and nothing is written; the explicit-date listing answers 500; the date-less
  listing omits the affected days.
- AC-17b. Host A's focus blocks cannot be read: the booking answers 500 and
  nothing is written; the explicit-date listing for that date answers 500
  (today: 200 with slots); the date-less listing omits that date (today:
  listed).
- AC-17c. Bookings on the links host A owns or co-hosts cannot be read: the
  booking answers 500 and nothing is written; the explicit-date listing answers
  500 (today: 200 with slots); the date-less listing omits that date (today:
  listed).
- AC-17d. Host A's external calendar cannot be read (no connection, or the
  provider fails): the booking of a slot that is otherwise free answers 201 and
  the explicit-date listing offers it — today's tolerance, unchanged (D-6).

In none of AC-17a–c is the answer 409.  [unit]

AC-18. Given a client over the booking rate limit, an unknown slug, an
exhausted link, or a malformed body, when a booking is made for a start the
rule would refuse, then the answer is 429, 404, 410 or 400 respectively, as
today — the 409 check comes after all of them.  [unit]

**Exact interval, offset, zone and hostless links**

AC-21. Given a link open 09:00–17:00 with durations 30 and 60, the hosts free,
and an offered 30-minute slot at 10:00 UTC on a link day:

- AC-21a. A start of 10:00:30 UTC (or 10:00:00.5 UTC) with a duration of 30 is
  refused with 409 and nothing is written.
- AC-21b. A start of 10:00 UTC with an end of 10:30:30 UTC (with or without a
  duration of 30 also sent) is refused with 400 and nothing is written.
- AC-21c. A start of 11:00 at UTC+01:00 with an end of 11:30 at UTC+01:00 is
  accepted (201) and the stored booking's start and end are 10:00 and 10:30
  UTC.  [unit]

AC-22. Given the server process's local zone set to a zone other than UTC
(for example UTC+10), a Monday-to-Friday link open 09:00–17:00, and the clock
at a fixed instant, when the date-less listing, the explicit-date listing and
the booking are exercised, then the dates listed, the slots returned and the
starts accepted are exactly those obtained with the process in UTC.  [unit]

AC-23. Given a link with no accepted host, when the listing is read with and
without a date, then no slot and no date are offered; and when a start inside
the window on a link day is booked, then 409 and nothing is written.  [unit]

**Contract and end-to-end**

AC-19. Given the contract, when the booking operation and the schemas it uses
are read, then: its 409 response lists every refusal cause of §3 (host busy by
calendar, focus block or any non-cancelled booking; outside days or hours or
grid; beyond the horizon; inside the minimum notice; no accepted host) and
states that the stored interval equals the slot; 500 is documented for busy
time that cannot be gathered; it no longer documents 422 anywhere — not in the
responses, not in the booking rate-limit 429 description's list of statuses,
not in the request start field's description; the request end field states
that the length must be exactly an offered duration; its description no longer
states API-094 as open but still states API-051 as open; the listing operation
documents the link-day filter, the horizon for explicit dates, UTC as the zone,
and the server error / omitted day for busy time that cannot be gathered; and
the open-dates field describes days within the horizon that are link days with
at least one offered slot. API-094 is marked fixed in the audit register.
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
  the listing and the booking (D-6, OQ-2). Storage failures on focus blocks,
  bookings or the host list are *not* tolerated (D-7); only the external
  calendar is.
- **A time zone for the link's hours.** Hours are evaluated in UTC (D-9). A
  per-link or per-host zone is a separate feature.
- **A configurable horizon** per link (a new link setting and UI). D-1 fixes
  60 days for everyone.
- **API-051** (exhaustion counting) — PAC-40. The contract keeps describing it.
- **Buffers around focus blocks and bookings.** Buffers continue to widen
  calendar events only, as the listing does today; the rule is shared, not
  redesigned.
- **Buffers across the window edge.** Calendar events are read only for the
  link's daily window, so an event ending before the window opens (or starting
  after it closes) does not block a slot through its buffer, in the listing
  today and in the shared rule. A window-edge variant of AC-2b is inherited
  behaviour, not a defect of this work.
- **Any web change.** The paired web PR only regenerates types. This is a
  stated choice, not a claim that visitors see no difference: the date picker
  keeps every future date selectable, so a non-link day or a date past the
  horizon shows the page's existing "No time available on this day" message
  (§3). Greying those dates out in the picker is a separate web change
  (OQ-5).

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
- The lost-race UI message (e2e `:281`, `:327`).
- e2e flows that book or list on link days within the horizon stay green
  unchanged: `seam.spec.ts:283-296`, `time-format.spec.ts:113-120`,
  `scheduling-links.spec.ts:74-94`. The comment in `e2e/lib/dates.ts:34-37`
  ("ignores link.DaysOfWeek entirely") becomes false and is corrected by the
  implementation; the helper's behaviour (pick a weekday) stays.

**Existing assertions this spec changes.** Each is amended, never deleted or
skipped, and keeps its intent:

- `TestPublicBookingErrorsContract` "booking rejects too soon"
  (`handlers_booking_test.go:198`): 422 "booking is too soon" becomes 409 with
  the common refusal text, by design (AC-11).
- `futureBookingStart()` (`handlers_booking_test.go:92-95`) lands on a weekend
  two days in seven, and the fixture links are Monday to Friday 09:00–17:00
  (`:74-77`). The fixture moves onto a start the rule offers whatever day the
  suite runs (a link day, inside the hours, on the grid, after the notice,
  within the horizon). The tests that rely on it — `TestPublicBookingCreateContract`
  (`:151`), `TestPublicBookingErrorsContract` (`:180`),
  `TestPublicBookingPropagatesEngineFailure` (`:217`) and
  `TestPAC67_PublicBookingWithARefusedHost` (`pac67_surfaces_test.go:300-355`)
  — are amended to book starts the rule offers and keep asserting what they
  assert today (201 and the body; 500 on engine failure; PAC-67's refused-host
  answers).
- `TestCreateBooking_RateLimited` (`ratelimit_booking_test.go:276-305`) books
  hourly starts from 10:00 over 21 hours. It is amended to book 21 distinct,
  non-overlapping starts the rule offers (for example spread over link days in
  the horizon), and must still demonstrate NEW-booking-rate-limit's acceptance
  criteria exactly: the first 20 requests answer **201** (not 409), the 21st
  answers 429 with the expected Retry-After, nothing is written for the refused
  request, and a request after the window answers 201. Accepting 409 as success
  in this test would weaken it and is not allowed.
- `TestCreateBooking_ConcurrentBurstCannotOvershoot` (`:456-497`) keeps passing
  today only because it counts 409 as "other". It is amended to book starts the
  rule offers, so that "at most 20 got past the limiter" is again a statement
  about 201s, and keeps its at-least-40 429s assertion.

## 7. Rollback

No migration and no stored data change. Reverting the backend PR restores
today's acceptance of any start after the notice (and reopens API-094);
reverting the contract PR restores the 422 documentation, and the paired web
type regeneration is reverted with it. The web app handles both 409 and 422,
so either order of revert is safe for visitors. No booking made while the
change was live is altered by a revert. The test amendments of §6 revert with
the backend PR; the fixture move is harmless under the old code.

## 8. Open questions

- **OQ-1 — Owner: product owner.** Confirm D-1 (60-day horizon for both listing
  modes and the booking; the issue says "the link's booking horizon" but no
  per-link setting exists), D-2 (422 becomes 409), D-3 (an unoffered duration
  stays 400, although the issue's draft acceptance lists it among the 409
  cases) and D-4 (the link's days of the week now hide days the listing shows
  today). Resolve by an answer in `.claude/factory/owner-decisions.md`.
- **OQ-2 — Owner: product owner.** Should a booking (and the listing) treat a
  host whose calendar cannot be read as busy rather than free? Today's
  fail-open lets a link with an unconnected co-host book over that co-host's
  real meetings. Changing it hides every slot on such links, so it is not
  assumed here (D-6). Resolve by owner answer; if "busy", a follow-up spec.
- **OQ-3 — Owner: triage.** File a follow-up issue for concurrent double
  booking across links sharing a host (§5). Resolve by a PAC issue linked from
  API-094's fix note.
- **OQ-4 — Owner: contract-author.** Whether the refusal text stays today's
  "slot is no longer available" for all causes or becomes a new uniform text;
  the spec only requires one text for every 409 (AC-13), and the web shows it
  verbatim.
- **OQ-5 — Owner: triage.** A web follow-up so the date picker greys out
  non-link days and dates past the horizon instead of letting the visitor pick
  them and see "No time available". It needs the page to know the open dates
  (it never calls the date-less listing today) and belongs in the web repo
  after this contract lands. Resolve by a PAC issue or an explicit "won't do".
- **OQ-6 — Owner: product owner.** Confirm D-7 (a storage failure while
  gathering busy time makes the explicit-date listing answer a server error and
  the date-less listing omit the day, where today slots are offered over the
  unreadable focus blocks or bookings), D-8 (exact interval), D-9 (UTC), D-10
  (a link with no accepted host offers nothing) and D-11 (pending bookings
  count as busy). Resolve by owner answer.

## Decisions

- **D-1 — Booking horizon: 60 calendar days (today and the next 59), the same
  for the date-less listing, the explicit-date listing and booking. An
  explicit date beyond it lists no slots (200), not an error.** Assumed by the
  factory, pending owner confirmation (2026-10-28). No horizon field exists on
  links; 60 is the only bound in the product today (the date-less listing), and
  applying it everywhere narrows what can be booked without adding a setting.
  Answering "no slots" rather than 400 keeps the listing's shape and errors
  unchanged for existing clients. Visible consequence (challenge blocking 3):
  the web page lets a visitor pick a date past the horizon and shows "No time
  available" there.
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
  Assumed by the factory, pending owner confirmation, because it changes what
  the listing shows today.
- **D-5 — Every 409 carries the same text**, so a refusal does not reveal which
  host constraint failed. Assumed by the factory, pending owner confirmation;
  the most conservative choice about what a refusal discloses.
- **D-6 — An unreadable host calendar is treated as today (free) in both
  paths.** Assumed by the factory, pending owner confirmation; see OQ-2.
- **D-7 — A storage failure while gathering busy time (host list, focus
  blocks, bookings on any of the hosts' links) fails closed in both paths: the
  booking answers 500 and writes nothing; the explicit-date listing answers a
  server error; the date-less listing omits the day.** Assumed by the factory,
  pending owner confirmation (2026-10-28). Neither path may offer or accept a
  slot whose freedom was not checked, and the two paths must agree (AC-14).
  The listing answers chosen are the ones each mode already gives for a
  host-list failure today, so no new error shape is introduced. Only the
  external calendar keeps today's tolerance (D-6), because it fails routinely
  (an unconnected co-host) where a storage failure does not.
- **D-8 — The stored interval equals the slot; a length that is not exactly an
  offered duration (including one with seconds) is 400; a start off the grid
  (including one with seconds) is 409; a start in another offset denoting the
  same instant is accepted.** Assumed by the factory, pending owner
  confirmation (2026-10-28). Today a 30 min 30 s booking is stored and overlaps
  the next slot. The 400 keeps a length the link never offers in the same class
  as D-3; the 409 keeps every off-grid start in the same class as AC-9.
- **D-9 — UTC is the one zone for the link's days, hours and horizon, in both
  listing modes and the booking.** Assumed by the factory, pending owner
  confirmation (2026-10-28). Equal to today in every deployment (no `TZ` set;
  e2e sets `TZ: UTC`), so no visitor sees a change; it removes the
  process-zone dependency of the date-less listing.
- **D-10 — A link with no accepted host offers no slot and accepts no booking
  (409).** Assumed by the factory, pending owner confirmation (2026-10-28).
  Today such a link offers its whole window and a booking lands on no host's
  calendar, which nobody would attend; refusing is the narrowing choice and
  deletes nothing.
- **D-11 — "Busy because of a booking" means any booking that is not
  cancelled (pending or confirmed), on this link or on any other link a host
  owns or co-hosts.** Assumed by the factory, pending owner confirmation
  (2026-10-28). Identical to today in effect (every booking is `confirmed`),
  and the broader set is the conservative one should `pending` ever be written.

## Challenge — 2026-10-28

Reviewed the committed spec at `c412eb1` (branch `factory/pac-72-spec`) against
the code at the same commit (`backend/`, `e2e/`, `contracts/`), the web app at
`smart-calendar-flow` `origin/main` `aa5c52e`, the Linear issue PAC-72 and
`.claude/factory/owner-decisions.md` (no PAC-72 answer recorded there).

**Verdict**: accept-with-changes

### Blocking
1. **AC-17 / §3 "fails closed" are not true of the rule the spec says is shared,
   and §2 omits why.** `hostBusy` never returns an error
   (`backend/engine/booking.go:114-169`, always `return busy, nil` at `:168`):
   a failed focus-block read (`:153-154`, `if err == nil`) and a failed
   cross-link booking read (`:161-162`, `if err == nil`) are silently dropped,
   so the listing today offers a slot over a focus block or another link's
   booking whenever those queries fail, and the "tolerate per-host errors"
   branch at `:95` is unreachable. §2 lists only the calendar-client tolerance.
   As written, a booking that reuses "the one rule" would write over a focus
   block or another booking on a storage failure, which §3 and AC-17 forbid;
   a booking that fails closed while the listing does not breaks AC-14's
   "one rule" equivalence under failure. Resolve: §2 states the swallowed
   storage errors with these citations; §3 says, per busy source (focus blocks,
   bookings on other links, this link's bookings, host list), whether a storage
   failure fails the booking closed (500, nothing written) and what the
   listing does in the same case (both modes: the date-less listing also
   skips a day silently on engine error, `handlers_booking.go:181-183`);
   AC-17 names each source as a separate case.
2. **§6 "the one existing assertion this spec changes" is false; named tests
   break.** Links in the unit fixtures are Mon–Fri 09:00–17:00
   (`backend/api/handlers_booking_test.go:74-77`), and
   `futureBookingStart()` is now+72h at 10:00 UTC (`:92-95`), a Saturday or
   Sunday on two days in seven. Under rules 2 and 3 of §3:
   - `TestCreateBooking_RateLimited` (`backend/api/ratelimit_booking_test.go:276-305`)
     books `start+0h … start+19h` and `start+21h` and asserts 201 for all 20
     and for the post-window retry: from 10:00+8h the starts are past 17:00 or
     on the next day(s), so these become 409.
   - `TestCreateBooking_ConcurrentBurstCannotOvershoot` (`:458-490`) books
     60 hourly starts; it keeps passing only because it counts 409 as "other".
   - `TestPublicBookingCreateContract`, `TestPublicBookingPropagatesEngineFailure`
     (`handlers_booking_test.go:151`, `:217`) and
     `TestPAC67_PublicBookingWithARefusedHost`
     (`backend/api/pac67_surfaces_test.go:300-355`, asserts 201 at 11:00 and
     12:00 on `futureBookingStart()`'s day) become day-of-week dependent.
   - All of the above drive `createBooking` through `fakeBookingFlow` /
     `countingBookingFlow`, whose slot stubs return no or fixed slots.
   Resolve: §6 lists these tests as amended (fixtures moved onto starts the
   rule offers), and states that NEW-booking-rate-limit's acceptance criteria
   (20 accepted then 429, every request counted, Retry-After) must still be
   demonstrated after the change, not weakened to accept 409 as success.
3. **§2's web claim is wrong, so §3's "what a visitor sees" is incomplete.**
   §2 says the date picker "only enables dates the date-less listing returned
   (`:411-417`)". The page never calls the date-less listing: the slots query
   is `enabled: !!dateStr` and always sends `date`
   (`smart-calendar-flow/src/pages/PublicBooking.tsx:138-145`); `available_dates`
   is read from that explicit-date answer, which is always `[]`
   (`handlers_booking.go:212`), so `availableDates.size === 0` and every future
   day is selectable (`PublicBooking.tsx:411-418`). Consequence: under D-1 a
   visitor who picks a date past D+59 now gets "No time available on this day"
   where today they get slots; §3 names only the weekend change. Resolve:
   correct the §2 citation; §3 names the beyond-horizon empty day as a visible
   change, so that §5's "no web change" is a stated choice rather than a claim
   resting on a wrong fact.

### Non-blocking
1. **The booked interval is not required to equal the slot.** With `end`
   supplied, the duration is `int(end.Sub(start).Minutes())`
   (`handlers_booking.go:272`), truncated: `end = start+30m30s` passes as 30
   and the stored booking lasts 30m30s, overlapping the next offered slot. §3
   says "start time and duration are exactly a slot"; it should say the booked
   [start, end) equals the slot, with AC cases for an end with seconds, a start
   with seconds, and a non-UTC offset denoting a grid instant (the last one
   accepted as the same instant).
2. **Two zones, not one.** Rule 5 says "the zone the link's hours are evaluated
   in", but §2 shows the date-less listing uses the process zone and the
   explicit-date listing UTC. Equal today (no `TZ` in `backend/Dockerfile` or
   the compose files; e2e sets `TZ: UTC` at `e2e/docker-compose.e2e.yml:111`),
   but the spec should require listing and booking to use one named zone, so an
   operator-set `TZ` cannot make the two listing modes and the booking disagree.
3. **Link with no accepted host.** `CollectiveSlots` with zero accepted hosts
   offers the whole window (rule 3 is vacuously true); the owner is an
   accepted host only because creation inserts it
   (`handlers_scheduling_links.go:373`). The spec should state what is offered
   and bookable on such a link (today: everything in the window, written to no
   calendar).
4. **Buffers at the window edge.** Events are listed only for
   [window start, window end] (`engine/booking.go:93`, `:124`), so an event
   ending just before the window with a buffer-after may not block the first
   slot. The shared rule inherits this; say so in §5 next to the other buffer
   exclusions, so a window-edge variant of AC-2b is not read as a defect.
5. **AC-19 misses contract text that also documents 422.** Besides the
   `createPublicBooking` responses, `contracts/openapi/paths/booking.yaml:63`
   (429 description lists "409, 410, 422 or 500"), `:1653` (`start`: "else
   422") and `:1628` (`available_dates` wording) need to change too.
6. **Pending vs confirmed.** This link's overlap counts any non-cancelled
   booking (`storage/scheduling_links.go:404-411`); other links count only
   `confirmed` (`:442-467`). Equal today (nothing writes `pending`); rule 3
   should name one status set for both.
7. **API-051 deferral** is acceptable: NEW-booking-rate-limit set the precedent
   (`contracts/features/NEW-booking-rate-limit.md` §6) and PAC-40's spec
   branch owns it (`docs/specs/PAC-40.md` §2.4 on `origin/factory/pac-40-spec`).
   The contract must keep describing it (factory §7 rule 2).
8. **e2e consumers to keep green** (all on link days within the horizon today):
   `e2e/tests/seam.spec.ts:283-296` (Thursday six weeks ahead, 16:00 on the
   seam link; offered only if the stand-in reports no event then),
   `e2e/tests/time-format.spec.ts:113-120`, `e2e/tests/scheduling-links.spec.ts:74-94`.
   The `e2e/lib/dates.ts:34-37` comment ("ignores link.DaysOfWeek entirely")
   becomes stale.
9. OWNER: D-3 keeps 400 for an unoffered duration, but the issue's draft
   acceptance lists "a duration that isn't offered" among the four cases
   refused with a 409. Confirm 400 is acceptable.
10. OWNER: D-1, a fixed 60-day horizon for booking and both listing modes (the
    issue says "the link's booking horizon"; no such setting exists).
11. OWNER: D-2, inside the minimum notice answers 409 instead of 422.
12. OWNER: D-4, applying the link's days hides non-link days that listings show
    today.
13. OWNER: OQ-2 / D-6, a host whose calendar cannot be read stays "free" for
    both listing and booking.

### Criteria I could not falsify
- AC-1, AC-6, AC-12, AC-18 pass on today's code: they are regression guards,
  falsifiable only against a wrong implementation. Acceptable as guards.
- AC-17 as written: today no storage failure in busy-time gathering is
  observable (Blocking 1), so the test's precondition cannot be produced for
  focus blocks or other-link bookings until the spec says those failures
  surface.

### Citations I checked and found wrong
- "Its date picker only enables dates the date-less listing returned
  (`PublicBooking.tsx:411-417`)": the page calls only the explicit-date
  listing, `available_dates` is always empty, and every future date is enabled
  (`PublicBooking.tsx:138-145`, `:411-418`).
- "Per-host failures are tolerated … (`:95` "tolerate per-host errors")":
  `:95` is unreachable (`hostBusy` never returns an error); the real tolerance
  also swallows focus-block and booking read failures
  (`engine/booking.go:153-166`).
- §6 "This is the one existing assertion this spec changes": see Blocking 2.
- Other §2 citations checked and correct: handler `:139-141`, `:163-213`,
  `:216-314`; engine `:75-112`, `:114-169`, `:180-189`, `:216-281`; storage
  `:395-411`, `:442-467`; migration 009 `:33`; `handlers_booking_test.go:198`;
  `backend/Dockerfile:3`.

### Consumers this breaks
- clockwise-like/backend/api/ratelimit_booking_test.go:276-305: out-of-hours and next-day starts asserted 201.
- clockwise-like/backend/api/handlers_booking_test.go:92-95 (`futureBookingStart`): a weekend on 2 days in 7; affects `:151`, `:180`, `:217`.
- clockwise-like/backend/api/pac67_surfaces_test.go:300-355: 201 asserted on `futureBookingStart()`'s day.
- clockwise-like/backend/api/handlers_booking_test.go:198: 422 becomes 409 (already named by the spec).
- smart-calendar-flow/src/pages/PublicBooking.tsx:190-192: the 422 branch becomes dead (harmless; the 409 branch at `:186-189` takes over).

## Revision — 2026-10-28

Answers the challenge above (`d9be46b`, accept-with-changes), item by item.
Every citation added was re-read at `69ffa92` (backend, contract, e2e) and
`smart-calendar-flow` `aa5c52e`. `.claude/factory/owner-decisions.md` has no
PAC-72 answer, so every product choice below is recorded as assumed by the
factory, pending owner confirmation.

### Blocking

1. **Fail-closed vs the shared rule.** Accepted. §2 now states that the
   busy-time computation never returns an error (`engine/booking.go:168`),
   that focus-block and bookings read failures are swallowed (`:153-154`,
   `:161-162`), that the "tolerate per-host errors" branch (`:93-96`) is
   unreachable, and that the date-less listing skips a day on error
   (`handlers_booking.go:181-183`) while the explicit-date listing answers 500
   (`:200-204`). §3 has a per-source table (host list, focus blocks, bookings
   on any of the hosts' links including this one, external calendar) giving the
   booking's answer and both listing modes' answers; storage failures fail
   closed in all three paths (D-7), the external calendar keeps today's
   tolerance in all three (D-6). This-link overlap is covered by the "bookings"
   row because rule 3 now reads one set of bookings across all the hosts'
   links, this one included (D-11). AC-17 is split into AC-17a–d, one per
   source, each asserting the booking and both listing modes, and none
   answering 409. "Criteria I could not falsify" (AC-17) is resolved: AC-17b
   and AC-17c fail today (the listing answers 200 with slots, the booking 201).
2. **§6 named one changed assertion; more change.** Accepted. §2 now lists the
   fixture facts (Mon–Fri 09:00–17:00 links, `futureBookingStart()` a weekend
   two days in seven) and every test that books on or after it. §6 has an
   "Existing assertions this spec changes" list naming
   `TestPublicBookingErrorsContract` (:198, 422 → 409, and :180 via the
   fixture), `TestPublicBookingCreateContract`,
   `TestPublicBookingPropagatesEngineFailure`,
   `TestPAC67_PublicBookingWithARefusedHost`, `TestCreateBooking_RateLimited`
   and `TestCreateBooking_ConcurrentBurstCannotOvershoot`, each amended (never
   deleted or skipped) onto starts the rule offers. It requires that
   NEW-booking-rate-limit's criteria are still demonstrated exactly — 20 × 201,
   then 429 with the expected Retry-After, nothing written for the refused
   request, 201 after the window — and forbids accepting 409 as success there.
3. **Web citation wrong.** Accepted. §2 now says the page only calls the
   explicit-date listing (`PublicBooking.tsx:138-145`), whose open-dates list is
   always empty (`handlers_booking.go:212`), so every future date is selectable
   (`PublicBooking.tsx:411-418`), and quotes the empty-day message (`:455-456`).
   §3 "What a visitor using the booking page sees" now names three visible
   changes: non-link days empty, dates past today + 59 empty (where today they
   show slots), and the advance-notice refusal replaced by the slot-taken one.
   §5 states "no web change" as a choice with its consequence; greying out
   those dates is OQ-5 (triage, web follow-up). D-1 records the consequence.

### Non-blocking

1. **Booked interval equals the slot.** Accepted. §2 cites the truncation
   (`handlers_booking.go:272`) and the free-form start (`:253`). §3
   "Exactness": the stored [start, end) is exactly the slot; a start with
   seconds is off-grid (409); a length that is not exactly an offered duration,
   seconds included, is 400 like D-3 and is never rounded; another offset for
   the same instant is accepted. AC-21a–c cover a start with seconds, an end
   with seconds, and a UTC+01:00 start/end stored as the UTC slot; AC-1 now
   also asserts the stored start and end. D-8.
2. **One zone.** Accepted. §3 "One zone": UTC for days, hours and horizon in
   both listing modes and the booking, whatever the process zone. AC-22 runs
   the three with a non-UTC process zone and requires the UTC answers. §2 cites
   the two zones (`:174-179`, `:195`) and the e2e `TZ: UTC`
   (`e2e/docker-compose.e2e.yml:111`). §5's zone bullet updated. D-9.
3. **Link with no accepted host.** Accepted. §2 states today's behaviour (whole
   window offered, booking written to no calendar; owner inserted as host at
   `handlers_scheduling_links.go:373`). §3 rule 6: such a link offers nothing
   and accepts nothing (409). AC-23; D-10; listed in AC-19's refusal causes and
   AC-13.
4. **Buffers at the window edge.** Accepted. §2 states events are read only for
   the window; §5 has a "Buffers across the window edge" bullet stating the
   shared rule inherits this, so a window-edge AC-2b variant is not a defect.
   AC-2b now says both the event and the start are inside the window.
5. **AC-19 coverage of 422 / open-dates text.** Accepted. §2 cites
   `booking.yaml:63`, `:1653` and `:1627-1628`; AC-19 now requires 422 to be
   gone from the responses, the rate-limit 429 description's list and the start
   field's description, the end field to state the exact-length rule, the
   open-dates field to describe link days within the horizon, 500 for
   unreadable busy time, UTC as the zone, and API-051 still described as open.
6. **One status set.** Accepted. §2 states the two status sets
   (`scheduling_links.go:404-411` non-cancelled, `:442-467` confirmed). Rule 3
   names one set: every non-cancelled (pending or confirmed) booking on any link
   a host owns or co-hosts, this link included. AC-4b covers a pending booking on
   another link for both booking and listing. D-11.
7. **API-051 deferral.** Acknowledged; the inherited-scope note records the
   acceptance and that the contract must keep describing API-051 (factory §7
   rule 2); §5 and AC-19 repeat it.
8. **e2e consumers.** Accepted. §2 and §6 name `seam.spec.ts:283-296`,
   `time-format.spec.ts:113-120`, `scheduling-links.spec.ts:74-94` as flows that
   must stay green unchanged, and `e2e/lib/dates.ts:34-37`'s comment as stale,
   to be corrected by the implementation (helper behaviour unchanged).
9. **OWNER: D-3 (400 for an unoffered duration).** Kept as assumed; OQ-1 now
   names the conflict with the issue's draft acceptance explicitly.
10. **OWNER: D-1 (fixed 60-day horizon).** Kept as assumed; OQ-1 names the
    issue's "the link's booking horizon" wording and the missing setting.
11. **OWNER: D-2 (422 → 409).** Kept as assumed; in OQ-1.
12. **OWNER: D-4 (link days hide days shown today).** Kept as assumed; in OQ-1,
    and the visible effect is now in §3.
13. **OWNER: OQ-2 / D-6 (unreadable calendar is free).** Kept as assumed; §3's
    table and AC-17d make it explicit for both paths. The new assumptions of
    this revision (D-7 to D-11) are collected for the owner in OQ-6.
