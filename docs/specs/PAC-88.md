# Spec — PAC-88: Public booking page shows times in the host's time zone by default, with a booker time-zone switcher

- **Linear**: https://linear.app/paceday/issue/PAC-88
- **Status**: draft, challenged 2026-11-02 (accept-with-changes, see Challenge log)
- **Author**: spec-author; body corrected by spec-challenger (see Challenge log)
- **Repos**: `Enach/clockwise-like` (api, at `32ecacf`), `Enach/smart-calendar-flow` (web, at `702a6c2`)
- **Owner decisions this implements**: PAC-72 D-9 (2026-11-01): "display the
  host-profile timezone by default and allow to change the timezone on the
  booking page"; A-2 (a collective link uses the link owner's zone, co-hosts'
  zones ignored); A-4 (an owner with no profile zone → UTC, shown as UTC).
- **Inherited scope**: no `x-uncertain` marker on any `/api/book/{slug}`
  operation (`contracts/openapi/paths/booking.yaml` has none), and no open
  audit finding on `GET /api/book/{slug}` or `GET /api/book/{slug}/slots`.
  **This spec does not modify `POST /api/book/{slug}`** (challenge: F-4 was
  revised so the email needs no new booking-create input). The open finding
  **API-051** on that operation (`docs/factory/api-audit.md:309`, cancelling a
  booking re-opens an exhausted single-use link; owned by epic E5,
  `api-audit.md:657`) is therefore **not** inherited. If the owner overrides
  F-4 (OQ-4) so the email uses the visitor's zone, the contract must add a
  booking-create input, and API-051 then becomes inherited under the
  touch-it-fix-it rule, as PAC-67 recorded it (`docs/specs/PAC-67.md:83`).

## 1. Problem

A visitor opening a Paceday booking link sees the host's free times rendered
in **the visitor's browser zone**, labelled with the browser zone, while the
server decides which times exist **per calendar day in the host's zone**. The
page asks the server for "the host's day D" using the visitor's local date D.
For any visitor whose zone differs from the host's, the day they click and the
times they are shown do not line up: a time listed under "Thursday" may be a
Friday time for the visitor, a visitor's Thursday evening may be missing
because it lives on the host's Friday, and "today" and the last bookable day
are computed in the wrong zone. Nothing on the page tells either party which
zone the host works in.

The owner decided (D-9) that the page shows the host's zone by default and
lets the visitor change it. The cost of not doing this: bookers pick the wrong
time or the wrong day, hosts get meetings at times the booker did not intend,
and the confirmation email states the time in UTC, which almost no booker
reads natively.

**Severity of today's day mismatch (challenge, verified on `702a6c2`/`32ecacf`).**
It never books a wrong instant: each time is rendered correctly in the browser
zone the page labels, booking sends the exact offered instant, and the
details step re-states the slot's own date (`PublicBooking.tsx:545`). The harm
is (1) times listed under the wrong day heading (a Tokyo visitor clicking
"Thursday" for a Paris host with 09:00–17:00 hours in winter sees times from
Thursday 5:00 PM to Friday 12:30 AM Tokyo, all under "Thursday"),
(2) for a visitor whose date is ahead of the host's, the host's remaining
times for the host's today cannot be requested at all (the visitor cannot
select a day before their own today), and (3), for every visitor including
UTC ones, every future day is selectable and most answer "No time available".
None is data loss or a wrong booking; the factory judges **no hotfix**: the
only minimal fix is the day regrouping that is the core of this spec.

## 2. Current behaviour

### 2.1 Server (api, `origin/main` `32ecacf`)

- **The link's zone.** Every public booking operation evaluates the link's
  days, hours, 15-minute grid, "today" and horizon in one zone: the **link
  owner's** profile zone, read at request time (`backend/api/booking_rule.go:23-46`,
  `linkLocation(…, link.OwnerUserID)`). No settings row, an empty name,
  `"Local"` or an unloadable name is UTC (`booking_rule.go:36-44`, owner A-4).
  A failed read is a 500, never UTC (`handlers_booking.go:135-141`, `:198-203`,
  `:346-354`). Co-hosts' zones are never consulted (A-2 is already true).
- **Link info already exposes the zone.** `GET /api/book/{slug}` returns
  `time_zone` (IANA name, never empty) and `booking_horizon_days`
  (`handlers_booking.go:55-57`, `:89-91`; contract
  `contracts/openapi/paths/booking.yaml:1850-1872`, which says the display and
  switcher "are PAC-88").
- **The slot listing does not carry a zone.** `GET /api/book/{slug}/slots`
  answers `{slots, available_dates}` only (`handlers_booking.go:230`, `:248`).
  With `date=YYYY-MM-DD` the date is **a calendar day in the link's zone**
  (`parseCalendarDay`, `booking_rule.go:55-63`; `handlers_booking.go:233-237`)
  and the slots are UTC instants (`booking_rule.go:118`). Without `date`,
  `available_dates` are link-zone days within the horizon that offer at least
  one slot (`handlers_booking.go:206-231`; contract `booking.yaml:1900-1910`).
  The date-less mode reads **every day of the horizon (up to 365) in one
  request**: a budget cut is the 503 (`:217-222`), a non-budget storage error
  silently omits that day (`:223`), and the contract itself tells clients
  needing a long range to use the explicit-date mode per visible day
  (`booking.yaml:310-314`). Both modes share one per-client limit of 120
  requests per minute (`backend/api/ratelimit_booking.go:31-35`).
- **DST on the server.** Link hours on a transition day are resolved by
  `wallClock` (`backend/engine/booking_window.go:30-63`): a skipped wall time
  does not exist, and **a repeated wall time resolves to its first occurrence**
  (`:33`). The grid (`GridSlots`, `:98-125`) therefore never offers the second
  occurrence of a fall-back hour in the link's own zone: two identical
  wall-clock times can appear on the page only when the displayed zone differs
  from the link's zone. Display of those instants in another zone is not the
  server's concern today.
- **Booking create** takes `start` (RFC3339 instant), optional `end` /
  `duration` / `duration_minutes`, `name`, `email`, `notes`
  (`handlers_booking.go:274-282`). No zone is sent or needed: the slot is
  compared as an instant (`:379-385`). The body is decoded without
  `DisallowUnknownFields`, although the contract declares it
  `additionalProperties: false` (`booking.yaml:1913-1920`).
- **Confirmation answer** returns `start`/`end` as UTC RFC3339
  (`handlers_booking.go:110-111`; contract `booking.yaml:1990-1998`). No zone.
- **Confirmation email** (`backend/engine/booking.go:553-601`): the subject
  date is `start.Format("Mon Jan 2")` (`:575`) and the body prints
  `Date: … Time: HH:MM – HH:MM UTC` (`:580-583`) — i.e. **UTC**, with a UTC
  label; the subject's day can therefore differ from the booker's and the
  host's day. The attached ICS uses UTC `Z` instants (`:604-615`), which
  calendar clients convert correctly. The email is sent by the engine after
  the booking is saved (`booking.go:536-545`), from `link` and `booking`
  only; it has no zone today and a failure is logged, never retried.
- **MCP**: no MCP tool reads or creates public bookings or scheduling links
  (`mcp/tools.go` has no `/api/book` or `scheduling-links` call); nothing to
  change there.
- **Host calendar events** are created with RFC3339 instants
  (`booking.go:517-518`); unaffected by display zone.

### 2.2 Web (`smart-calendar-flow`, `origin/main` `702a6c2`, `src/pages/PublicBooking.tsx`)

- The page's zone is **the browser's** (`browserTimezone`, `:55-61`;
  `const tz = useMemo(browserTimezone, [])`, `:112`). `time_zone` from link
  info never reaches the page: the client's `getPublicLink` mapper copies
  only slug, title, durations, hosts, notice, usage type and coverage, and
  **drops `time_zone` and `booking_horizon_days`**
  (`src/api/schedulingLinks.ts:401-414`). The only `time_zone` readers are the
  owner-side link drawer (`src/components/links/LinkEditDrawer.tsx:492`, which
  already tells the owner the hours are in their profile zone) and tests.
- The zone label in the sidebar is the browser zone name, without offset
  (`:409-412`); the confirmation shows the same label (`:313`).
- The selected date is turned into `YYYY-MM-DD` with **browser-local** getters
  (`dateOnlyStr`, `:26-28`; `:152`) and sent as the `date` the server reads as
  a **host-zone** day (`:153-158`). This is the mismatch in §1.
- Times are formatted with `toLocaleTimeString` and no `timeZone`, i.e. in the
  browser zone (`fmtTime` `:29-32` → `src/lib/timeFormat.ts` `formatTime`;
  `fmtDateLong` `:33-40`). Always 12-hour (PAC-55 §5, `:30`). No offset is
  shown, so the two 1:30 AM times of a fall-back day are indistinguishable.
- The page never calls the date-less listing (the client returns an empty
  answer without a request when there is no date,
  `src/api/schedulingLinks.ts:416-419`): `available_dates` comes from the
  per-date answer, where the server always sends `[]` (`handlers_booking.go:248`),
  so the guard at `:456-463` (`if (availableDates.size === 0) return false`)
  makes **every future day selectable**, including days beyond the link's
  horizon and non-link weekdays. "Today" is browser-local midnight
  (`:354-355`).
- Booking sends the slot's `start` string as received (`:178-186`), so the
  booked instant is correct regardless of zone.
- The downloadable ICS (`buildIcs`, `:71-96`) uses UTC `Z` instants: correct.
  Its **file name** carries the browser-local date of the start
  (`downloadIcs`, `:103`).
- "Book another time" clears the date, the time and the typed details
  (`:324-331`).
- The page has a non-production demo source (`demoOrReal`,
  `schedulingLinks.ts:402`, `:420`; PAC-83 OQ-1); it carries no zone today.
- Responses are not strictly parsed at run time: the generated zod schemas
  (`src/api/generated/schemas.ts:380-392`, `strictObject`) are not used by the
  public booking client, so an added response field does not break an older
  web build.
- PAC-83 error handling for the listing (`slotsFailed`, `:166-168`) shows an
  error with retry, never an empty list (PAC-83 AC-11).

### 2.3 Tests that protect today's behaviour

- api: `backend/api/pac72_rev2_test.go` (owner zone, UTC fallback, 500 on
  failed read), `backend/engine/booking_window_test.go` (DST wall-clock),
  `backend/api/pac72_offerable_start_test.go` (listing ≡ booking).
- e2e: `e2e/tests/public-booking.spec.ts` (`time_zone: 'UTC'`, `:79-82`,
  `:512`). The e2e browser is pinned to `timezoneId: 'UTC'`
  (`e2e/playwright.config.ts:49-52`) and the backend to `TZ: UTC`
  (`e2e/docker-compose.e2e.yml:118`) — so **no existing test exercises a
  visitor or host outside UTC**, which is why §1's mismatch is invisible to CI.
- web: `src/pages/PublicBooking.test.tsx`, `src/pages/PublicBooking.pac83.test.tsx`.
- The server's clock cannot be driven from e2e (`e2e/SEAM-REQUIRED.md:286-300`);
  a DST e2e must find a real upcoming transition inside the horizon (AC-19).
  Booking hosts in e2e are connected to the calendar stand-in, whose calendar
  is empty unless a journey writes to it (`e2e/tests/public-booking.spec.ts:23-33`),
  so a host is free on every link day of the horizon.
- Neither repo has an automated accessibility scanner (no axe dependency in
  `e2e/package.json` or the web `package.json`).

## 3. Desired behaviour

### 3.1 Default display zone

When a visitor opens a booking link, every date and time on the page is shown
in **the link's zone**: the link owner's profile zone (for a collective link,
the owner's, never a co-host's — A-2), or UTC when the owner has none, in
which case the page says "UTC" (A-4). The page never falls back to the
browser's zone silently. If the link's zone cannot be determined (the server's
500), the page shows the existing PAC-83 error with retry, not a page in some
other zone.

If the browser cannot render the zone the server names (an IANA name unknown
to that browser), the page displays UTC, labels it "UTC", and offers the
switcher as usual. (Factory-assumed, F-6.)

### 3.2 The zone is always visible

Wherever times are listed or confirmed, the page states the zone in words:
its IANA-derived name (e.g. "Europe/Paris" or a readable city form) **and its
UTC offset for the day being shown** (e.g. "UTC+01:00"). If two times shown
for the same day have different offsets (a DST transition day), each such time
carries its own offset, so two identical wall-clock times are distinguishable.

### 3.3 The switcher

- A control near the zone label lets the visitor choose any IANA zone the
  browser supports. The link's zone and UTC are always in the list, first,
  even if the browser's list omits them.
- The list is searchable: typing part of a zone or city name (case-insensitive,
  space and underscore equivalent, e.g. "new york" finds "America/New_York")
  narrows it. Each option shows its current UTC offset.
- When the browser's zone is known and differs from the zone being shown, a
  one-step shortcut "Use my time zone (<zone>)" switches to it. When it is the
  same, or unknown, the shortcut is not shown.
- A "back to the host's zone" option is always reachable in one step once the
  visitor has switched away.
- Changing the zone changes **only how times are shown**: the set of instants
  offered, the instant booked, every refusal and every error are identical to
  what they would be in the default zone.

### 3.4 Days are days in the displayed zone

The calendar of selectable days, "today", the last bookable day, and the times
listed under a day are all computed in the displayed zone:

- A day is selectable exactly when at least one offered time falls on it in
  the displayed zone. Days before today (displayed zone) and days with no
  offered time are not selectable. (This also fixes §2.2's "every future day
  selectable".)
- The times listed under a selected day are exactly the offered times whose
  start falls on that day in the displayed zone — no more, no fewer — even when
  that day spans two of the host's days. A time therefore moves to another day
  when the visitor switches zone if its start crosses midnight in the new zone.
- On switching zone, the selected day stays selected if it is still a
  selectable day in the new zone; otherwise no day is selected and the page
  says so. A selected time (step 3, booker details) survives a zone switch and
  is re-rendered in the new zone; the booker's typed details are kept.
- If listing any part of the displayed day fails, the whole day shows the
  PAC-83 error with retry — never a day with some of its times missing.
  "Parts" (F-7, made explicit): a displayed day overlaps two of the link's
  days whenever the zones differ by a non-whole-day amount, and up to three on
  a day where one zone changes offset; whatever answers the page needs to list
  one displayed day are one unit — all succeed or the day is an error.
- **Which days are selectable (the day overview)** is a second unit, separate
  from a day's times. If the overview for the days on screen fails, the
  calendar shows the PAC-83 error with retry and no day is offered as
  selectable; it never falls back to "every future day selectable", and a
  day's times already shown stay shown. A link-zone day whose busy time could
  not be gathered must not silently look like a day with no time: if the
  overview cannot say, the page treats it as a failure of the overview.
- The overview works for every horizon from 1 to 365 days (A-3): seeing the
  days of the month on screen never requires one request that reads the whole
  horizon, so a 365-day link does not answer the PAC-72 503 merely because
  its horizon is long (`booking.yaml:310-314`).

### 3.5 DST correctness

Across the whole horizon, and in both the link's zone and any chosen zone:
no time is shown at a wall-clock time that does not exist on that day
(spring-forward gap); the two occurrences of a repeated wall-clock time
(fall-back) are both shown when both are offered, each with its offset; the
offset shown for each time is the offset in force at that instant, not the
offset of "now". The server offers only the first occurrence of a repeated
wall time **in the link's zone** (§2.1); this spec does not change that, so
repeated wall-clock times arise on the page only in a displayed zone other
than the link's.

### 3.6 Booking and confirmation

- The booking sends the same instant whichever zone is displayed. Refusals
  (409, generic or specific reason), 410, 429, 503 and every PAC-83 message
  and retry behave exactly as today.
- The on-page confirmation shows date, start–end times, the zone name and its
  offset at the booked instant, in the zone displayed when the visitor booked.
  "Book another time" keeps that zone.
- The downloadable calendar file keeps absolute (UTC) instants; unchanged.
  Its file name's date is the start's date in the displayed zone.
- The confirmation **email** states the date (subject and body) and times in
  **the link's zone**, with the zone name and the offset in force at the
  booked instant, followed by the same start–end in UTC labelled "UTC" (so a
  booker who switched zone can check it against any clock). The link's zone is
  the one the booking was checked in. If that zone cannot be read when the
  email is composed, the email is still sent, in UTC only, labelled "UTC"
  (never skipped, never another zone unlabelled). The email's calendar
  attachment keeps absolute instants, which the booker's calendar shows in
  their own zone. The booking-create request is unchanged.
  (Factory-assumed, F-4 revised by the challenge; the visitor's zone in the
  email is OQ-4.)
- The page's demo source (non-production only) carries a link zone so the
  demo shows the same labels; production behaviour is unaffected.

### 3.7 Memory of the choice

The visitor's chosen zone holds for the visit: across date and duration
changes, refusals, errors and retries, the confirmation, and "book another
time". A fresh visit to the link (new tab or later) opens in the link's zone
again. (Factory-assumed, F-3: a literal reading of "by default"; the owner may
prefer remembering the choice, OQ-2.)

### 3.8 Host zone changing mid-visit

If the host changes their profile zone while a visitor has the page open, the
page never lists times under the wrong day: the answers it uses to build a day
must all be in one known link zone, and a mismatch makes the page reload the
link's zone (and, if the visitor had not switched, the default display) before
showing times.

### 3.9 Accessibility

- The switcher is a labelled control ("Time zone"), fully keyboard operable
  (open, type to filter, arrow, Enter, Escape), with the current zone as its
  accessible value.
- A zone change is announced politely to assistive technology ("Times are now
  shown in America/New_York, UTC−05:00").
- Each time button's accessible name contains the date, the time and the
  zone/offset (e.g. "Thursday, March 11, 2027, 9:00 AM, America/New_York
  UTC−05:00"), not just "9:00 AM".
- The zone label is text, not only an icon; touch target sizes are unchanged
  (≥ 44 px), including the switcher, its options and the "Use my time zone"
  and "back to the host's zone" actions.
- The announcement is made only on a change the visitor caused, never on page
  load; when a switch deselects the selected day (§3.4), the "pick a date"
  message is announced in the same polite region.
- Offsets are written so a screen reader reads the sign ("UTC−05:00" with a
  real minus, or "UTC minus 5" in the accessible name — the plan picks one and
  the test pins it).
- Each calendar day's accessible name is the displayed-zone date; a
  non-selectable day is exposed as disabled, not merely greyed.

## 4. Acceptance criteria

Levels: `[contract]` Go contract/handler test (api), `[unit]` vitest (web)
or Go unit, `[e2e]` Playwright against compose. "Host zone" means the link
owner's profile zone.

**Default zone**

AC-1. Given a link whose owner's zone is Europe/Paris and a browser in
Asia/Tokyo, when the page loads, then the zone label reads Europe/Paris with
the offset in force on the shown day, and every listed time equals the
offered instant rendered in Europe/Paris. [unit] [e2e]

AC-2. Given a collective link owned by a Europe/Paris user with an accepted
co-host whose zone is America/Los_Angeles, when the page loads, then it shows
Europe/Paris. [e2e]

AC-3. Given a link whose owner has no profile zone, when the page loads, then
the label reads "UTC" (offset +00:00) and times are UTC, whatever the browser
zone. [e2e]

AC-4. Given the link info answer fails with 500, when the page loads, then the
PAC-83 error with retry is shown and no time and no zone label is shown.
[unit]

AC-5. Given the link's zone is a name the browser cannot format, when the page
loads, then times are shown in UTC labelled "UTC" and the switcher is
available. [unit]

**Switcher**

AC-6. Given the page in Europe/Paris, when the visitor opens the switcher and
types "new york", then America/New_York is among the options and each visible
option shows its current UTC offset; the link's zone and UTC are listed first.
[unit]

AC-7. Given a browser in America/New_York and a link in Europe/Paris, then a
"Use my time zone (America/New_York)" action is present, and activating it
shows every time in America/New_York; given a browser already in the link's
zone, the action is absent. [unit] [e2e]

AC-8. Given the visitor switched to Asia/Tokyo, then one action returns to the
link's zone. [unit]

AC-9. Given a visitor who switches zone and books a displayed time, then the
booking request carries exactly the start instant the server offered (same
string as in the listing), and the server's answer (201 or 409 with the same
body, generic or specific) is identical to booking that time from the default
zone. [unit] [e2e]

**Day regrouping**

AC-10. Given a link in Europe/Paris offering 09:00–17:00 Paris on Monday only
and a visitor displaying Pacific/Auckland (UTC+13 in northern winter), when
the visitor views a winter week with a 30-minute duration, then both Monday
and Tuesday (Auckland) are selectable; Monday lists the 12 starts 9:00 PM,
9:15 PM, … 11:45 PM and Tuesday the 19 starts 12:00 AM, 12:15 AM, … 4:30 AM
(Auckland; 15-minute grid, 31 starts in all), and no other day lists a time —
i.e. every offered start s is listed under exactly one day, the Auckland
calendar date of s. [unit] [e2e]

AC-11. Given any link and any displayed zone Z, then the union of times over
all selectable days equals the set of offered starts within the horizon, and
each appears under its Z-calendar date exactly once (no duplicate, no
omission at day boundaries). [unit] (property test over several zones incl.
UTC−12, UTC+14, a :30 and a :45 offset zone)

AC-12. Given the displayed zone's today is the host's yesterday (or tomorrow),
then days before today in the displayed zone are not selectable and the last
selectable day is the last displayed-zone day containing an offered start.
[unit]

AC-13. Given a day with no offered start in the displayed zone (including a
non-link weekday and any day beyond the horizon), then it is not selectable.
Fails today: every future day is selectable (`PublicBooking.tsx:456-463`).
[unit] [e2e]

AC-14. Given a selected day D and a zone switch after which D has no offered
time, then no day is selected and the page says to pick a date; given D still
has times, D stays selected and its times are re-listed in the new zone.
[unit]

AC-15. Given the visitor is on the details step with a time selected and
typed name/email, when the zone changes, then the selected time is re-rendered
in the new zone (date, time, offset) and the typed details are unchanged.
[unit]

AC-16. Given a displayed day whose times come from two host days and the
listing of one of them fails (network, 5xx, 503 budget), then the whole day
shows the PAC-83 error and retry, and no time of that day is shown; after a
successful retry the day lists exactly the AC-11 set for that day. [unit]
(If the contract makes one displayed day one answer, the test fails that one
answer; the assertion is unchanged.)

AC-16a. Given the day overview for the month on screen fails (network, 5xx,
503), then no day is selectable, the PAC-83 error with retry is shown for the
calendar, and no future day is offered as selectable. [unit]

AC-16b. Given a link with a 365-day horizon whose host calendar answers
normally, when the visitor opens the page and moves the calendar to the month
of the last bookable day, then every month on the way shows its selectable
days and no answer is the 503; the page's requests never ask the server to
read more than the days on screen (plus the neighbouring days a displayed day
overlaps). [contract] (server: the overview answer for a bounded range) +
[unit] (web: the requests issued)

**DST**

AC-17. Given a link in America/New_York offering 01:00–04:00 on the US
spring-forward Sunday, displayed in America/New_York, then no time between
02:00 and 02:59 is shown, and times before and after carry offsets −05:00 and
−04:00 respectively. [unit]

AC-18. Given a link whose owner has no zone (UTC) open on Sundays 05:00–07:00
with a 30-minute duration, displayed in America/New_York on the US fall-back
Sunday 2027-11-07, then the day lists seven times in this order: 1:00, 1:15,
1:30 and 1:45 AM at UTC−04:00, then 1:00, 1:15 and 1:30 AM at UTC−05:00; the two
1:00 AM (and the two 1:15, the two 1:30) differ in visible offset and in
accessible name. [unit] (The link's own zone never yields a repeated wall time:
`GridSlots` offers the first occurrence only, §2.1.)

AC-18a. Given a link in Europe/London open 00:00–03:00 on the October
fall-back Sunday, displayed in Europe/London, then every listed time shows the
offset in force at that instant and no wall-clock time appears twice
(regression guard for §3.5's last sentence). [unit]

AC-19. Given a dedicated seeded link owner whose profile zone is
America/New_York (not a shared seed user, so no other journey's zone changes),
a link of that owner with a 365-day horizon, open every day of the week on a
whole-day window, notice 0, hosted by a stand-in-connected host with an empty
calendar, and a browser context in Europe/Paris; given the next DST transition
of each of the two zones that is at least two days after the test's "now"
(computed by the test from the tz database; the gap between transitions is at
most ~238 days, so always inside 365 days), when the visitor displays the
transition day and the day either side, in each of the two zones, then every
listed time and its offset equal the instant the server offers for that range
(read through the API by explicit days in the same run) rendered in that zone,
and the union of listed times equals that set. [e2e] Deterministic without a
server clock: the transitions are absolute dates, the test computes them at
run time, and every assertion compares the page with the server's own answer
for the same days rather than with fixed instants. It must not use the
date-less listing over the full horizon (the PAC-72 503). AC-17, AC-18 and
AC-18a pin fixed dates at [unit] level.

AC-20. Given the horizon spans a date when the displayed zone's offset
differs from today's, then the label for that day shows that day's offset,
not today's. [unit]

**Confirmation**

AC-21. Given a visitor who booked while displaying Asia/Tokyo, then the
on-page confirmation shows the Tokyo date, start–end, "Asia/Tokyo" and
"UTC+09:00"; "Book another time" returns to the flow still in Asia/Tokyo.
[unit] [e2e]

AC-22. Given a link whose owner's zone is America/Los_Angeles and a 30-minute
booking at 2027-01-15 03:30 UTC (made while the visitor displayed any zone),
then the confirmation email's subject gives Thursday, January 14 and the body
gives Thursday, January 14, 2027, 7:30 PM – 8:00 PM, America/Los_Angeles
(UTC−08:00), followed by 03:30 – 04:00 UTC on Friday, January 15; the
attached calendar file's start equals `20270115T033000Z`. Fails today: the
subject says Fri Jan 15 and the only time is UTC (`engine/booking.go:575-583`).
[unit] (Go)

AC-23. Given the link's zone cannot be read when the email is composed, then
the email is still sent, giving the date and times in UTC labelled "UTC", and
the failure is logged with the booking id. [unit] (Go)

AC-24. The booking-create request accepted by the contract is unchanged by
this feature (same properties, same required set), and booking the same
offered instant from any displayed zone produces byte-identical request
bodies apart from the booker's typed fields. [contract] + [unit] (web)

**Memory**

AC-25. Given a visitor who switched to Asia/Tokyo, then changing the duration,
receiving a 409, retrying after an error, and the confirmation all keep
Asia/Tokyo; a new page load of the same link shows the link's zone. [unit]

**Host zone change**

AC-26. Given the page loaded with link zone Europe/Paris and the host then
changes to America/New_York, when the visitor next lists a day, then no time
is shown under a day computed with the stale zone: the page uses the new zone
for day computation and, if the visitor had not switched, displays it.
[contract] + [unit]

**Accessibility**

AC-27. The switcher has the accessible name "Time zone" and is operable with
keyboard alone (open, filter, choose, cancel); a zone change produces a polite
live-region announcement naming the zone and offset; every time button's
accessible name contains date, time and zone/offset; no announcement on page
load; a deselected day after a switch is announced; switcher, options and
the two shortcut actions are at least 44 × 44 px. [unit] (testing-library
roles and live regions). An automated scanner pass is optional: neither repo
has one (§2.3), so adding it is a plan decision with its own dependency, not
an AC.

**Unchanged**

AC-28. With browser and host both UTC, the existing `public-booking.spec.ts`
journeys pass unchanged. [e2e]

**Added by the challenge**

AC-29. Given a link in Europe/Paris and a browser in Asia/Tokyo, at
16:05 Tuesday Paris winter time (00:05 Wednesday in Tokyo), with the link
open 09:00–17:00 Paris on Tuesday, notice 0, and its 16:15 and 16:30 Paris
starts offered, then those two times are reachable
from the page (under Wednesday, Tokyo) — fails today: the visitor cannot
select Tuesday and Paris's Tuesday is never requested. [unit]

AC-30. Given a booking confirmed while displaying Asia/Tokyo for 2027-01-14
23:30 UTC, then the downloaded calendar file's name carries 2027-01-15 and
its DTSTART is `20270114T233000Z`. Pins `buildIcs`, which no test covers
today (§6). [unit]

AC-31. Given the slot listing (either mode) for a link, then the answer
states the link zone it was computed in, equal to `PublicLinkInfo.time_zone`
read at the same moment (the issue's scope: "the … slots answers carry the
host's IANA time zone"). [contract]

AC-32. Given any request the page makes for times or days carrying a display
zone the server does not recognise (if the contract carries one, OQ-1), then
the answer is a 400 naming the parameter, never times computed in another
zone and never a 200 with an empty list. [contract]

AC-33. Given the non-production demo source, then the page shows the demo
link's zone label like a real link; in a production build no demo path
exists (PAC-83 OQ-1, unchanged). [unit]

## 5. Explicitly out of scope

- **A 24-hour clock on the booking page.** The page stays 12-hour (PAC-55 §5);
  only the zone changes.
- **Showing the booker's zone to the host** (in the owner's bookings list,
  host calendar events or host notifications). The host's calendar event is an
  absolute instant and already renders in the host's calendar zone.
- **The owner's bookings list** (`LinkBookingsDialog`) and API-019.
- **Changing which times are offered** — the PAC-72 rule, zone and horizon are
  unchanged; this feature is display only.
- **Co-host zones** (A-2: ignored).
- **Localisation** of date/month names (English only, `src/lib/locale.ts`).
- **Detecting the visitor's zone from IP.**
- **The email in the visitor's chosen zone** (OQ-4; it would change booking
  create and inherit API-051).
- **MCP tools**: none touches public booking (§2.1).
- **Host-side views**: the owner's link drawer already names the profile
  zone (`LinkEditDrawer.tsx:492`); host calendar events are instants. No change.

## 6. What must not change

- The offered set and the booking rule (PAC-72): `pac72_offerable_start_test.go`,
  `pac72_rev2_test.go`, `booking_window_test.go`.
- The link's zone is the owner's, UTC when none, 500 on a failed read:
  `pac72_rev2_test.go`.
- PAC-83 semantics for listing and booking failures, no automatic re-send of
  a booking, demo only in non-production builds:
  `PublicBooking.pac83.test.tsx`, `e2e/tests/public-booking.spec.ts`.
- The calendar file (page and email) keeps UTC instants — no test pins the
  page's `buildIcs` today (finding); AC-22 pins the email's.
- Rate limits (429 first) on both public operations: `ratelimit_booking_test.go`.

## 7. Rollback

Display-only on the web; on the api, the email's wording and, depending on
OQ-1, optional additions to the slot listing (a stated link zone, AC-31; a
display zone and/or a bounded overview range). Booking create is unchanged
(AC-24). No migration. Reverting the web PR restores browser-zone display;
reverting the api PR restores the UTC email and the listing as on `32ecacf`.
Additive response fields do not break an older web build (responses are not
strictly parsed, §2.2). A newer web build against a reverted server must
still list and book correctly: the contract stage must pin that any new
listing parameter is optional and that a server without it answers in a way
the page detects (e.g. the absent stated zone) rather than silently listing
link-zone days as display-zone days.

## 8. Decisions and open questions

### Owner decisions (binding)

- D-9: host profile zone by default, booker can change it.
- A-2: collective → link owner's zone.
- A-4: no zone → UTC, shown as "UTC".

### Factory-assumed (owner may override)

- **F-1** The label shows zone name **and** UTC offset; offsets per time on a
  transition day (§3.2).
- **F-2** Switcher: all browser-supported IANA zones, searchable, link zone
  and UTC first, browser-zone shortcut shown only when it differs (§3.3).
- **F-3** The choice lasts for the visit only; a fresh visit opens in the
  host's zone (§3.7). See OQ-2.
- **F-4** (revised by the challenge) The confirmation email states the date
  and times in the link's zone with name and offset, then the same start–end
  in UTC; UTC only, labelled, if the link zone cannot be read at send time;
  ICS unchanged (§3.6). Rationale: the issue asks that "slot times and the
  confirmation show the chosen zone", which the on-page confirmation does
  (AC-21); the email in the visitor's zone needs a new booking-create input,
  which brings API-051 (a lifecycle/counter defect unrelated to zones) into
  scope, while the attached calendar file already shows the booker their own
  zone. Visitor-zone email is OQ-4.
- **F-5** Days, "today" and the horizon end are displayed-zone days; a day
  is selectable iff it has an offered start (§3.4).
- **F-6** A server zone the browser cannot render → display UTC, labelled.
- **F-7** A failure listing any part of a displayed day fails the whole day
  (PAC-83 consistency, §3.4). The parts are the answers covering the two (on
  an offset-change day, up to three) link-zone days the displayed day
  overlaps; the day overview is a separate unit (AC-16a).

### Open questions

- **OQ-1 (contract-author, stage 2) — how the page gets displayed-zone days.**
  Today the listing is keyed by link-zone day (`booking_rule.go:55-63`). The
  options: (a) the client requests the overlapping link-zone days (2, rarely 3
  per displayed day) and regroups; (b) the listing accepts an optional display
  zone and answers per display-zone day, in both modes, stating the link zone
  it used; (c) the date-less listing returns the full offered set.
  **Challenger's recommended direction: (b)**, with the date-less mode
  answerable for a bounded range. Reasons: one answer per displayed day makes
  F-7/AC-16 atomic by construction (one 200 or one error, no partial
  assembly); "today", the horizon's first and last day and minimum notice are
  computed by the one rule that also checks bookings, so the edges of a
  display-zone day that straddles the horizon cannot drift between client and
  server; it halves the per-day request count against the 120/min/client
  limit (`ratelimit_booking.go:31-35`, shared by visitors behind one NAT); and
  a bounded overview range removes the PAC-72 503 that a 365-day date-less
  read invites (`booking.yaml:310-314`), which (a) cannot fix for the
  calendar overview without one request per day. Its cost: a zone switch
  re-requests (the cache key includes the zone) where (a) could reuse host-day
  answers; acceptable because switches are rare and hosts' calendar reads are
  cached server-side. (c) is rejected: it is today's 503 on long horizons.
  An unknown display zone is a 400 (AC-32). Booking create is not touched.
  Constraints: AC-16, AC-16a, AC-16b, AC-26, AC-31, AC-32 and §7's
  newer-web/older-server rule. Resolve in `contracts/features/PAC-88.md`.
- **OQ-2 (owner, low priority, not blocking)** — should a visitor's chosen
  zone be remembered for later visits (on that browser), overriding the host
  default? The factory assumes not (F-3), as the literal reading of D-9.
- **OQ-3 (resolved by the challenge)** — the e2e DST case (AC-19) is
  deterministic without a clock seam: transitions are absolute dates the test
  computes at run time; a dedicated owner (profile zone America/New_York) and
  link (365 days, every weekday, whole-day window, notice 0) with a
  stand-in-connected host whose calendar is empty guarantee offered times on
  every transition day; assertions compare the page with the server's own
  explicit-day answers for the same days, never with fixed instants; the
  first transition at least two days ahead is used so "today" and notice
  never interfere. Residual risk: tz-database disagreement between the
  backend image, Node and the browser for a future rule change (accepted; it
  would be a real defect worth a red run). Fixed-date DST cases live at unit
  level (AC-17, AC-18, AC-18a).
- **OQ-4 (owner, not blocking)** — should the confirmation email show the
  time in the zone the visitor chose (rather than the host's zone plus UTC,
  F-4)? Yes means a new optional booking-create input and carrying API-051
  (single-use link re-opened by a cancellation) in this ticket, or a
  follow-up ticket after epic E5. The factory recommends the follow-up.

## Challenge log

### Challenge — 2026-11-02

**Verdict**: accept-with-changes (changes applied in this revision; the body
was edited directly at the caller's instruction, which departs from
`.claude/agents/spec-challenger.md`'s "append only" rule — noted, not hidden).
Verified against clockwise-like `origin/main` `32ecacf` and smart-calendar-flow
`origin/main` `702a6c2`.

#### Blocking (resolved in this revision)
1. **AC-18 was untestable as written.** `wallClock` resolves a repeated wall
   time to its first occurrence and `GridSlots` never offers the second
   (`engine/booking_window.go:33`, `:98-125`), so a Europe/London link shown in
   Europe/London never has two 01:00 instants. Resolved: AC-18 now uses a UTC
   link displayed in America/New_York on 2027-11-07 (seven fixed times); AC-18a
   guards the link-zone case; §2.1 and §3.5 state the rule.
2. **AC-10's expected list was wrong.** The grid is 15 minutes, so Monday
   (Auckland) ends at 11:45 PM, not 11:30 PM. Resolved: exact 12 + 19 starts.
3. **F-4 silently enlarged the ticket.** A visitor-zone email needs a new
   booking-create input, which inherits API-051 (an unrelated single-use
   counter defect, likely a migration) against §7's "no migration". The issue
   asks for the chosen zone on "slot times and the confirmation", which the
   on-page confirmation satisfies. Resolved: F-4 revised to link zone + UTC;
   booking create unchanged (AC-24 rewritten); visitor-zone email is OQ-4.
4. **F-5 had no answer for long horizons or overview failures.** "Selectable
   iff it has an offered start" needs a per-day overview; the only server
   mode for that reads the whole horizon (up to 365 days) and ends in the
   PAC-72 503 (`booking.yaml:310-314`), and silently omits days on a storage
   error (`handlers_booking.go:223`). Resolved: §3.4 defines the overview as
   its own unit with an error state (AC-16a) and requires bounded reads
   (AC-16b).
5. **AC-19 was not deterministic as written (OQ-3).** Resolved: dedicated
   owner/link/host seed, run-time transition computation, comparison with
   the server's explicit-day answers, no date-less full-horizon read.

#### Non-blocking (applied)
1. F-7's "parts" were undefined: now 2 (rarely 3) overlapping link-zone days
   per displayed day, plus the overview as a separate unit.
2. Missing case: visitors ahead of the host cannot reach the host's
   remaining "today" times (AC-29, fails today).
3. ICS file name uses the browser-local date (`PublicBooking.tsx:103`); AC-30
   pins it and `buildIcs`, untested today.
4. The issue's scope "slots answers carry the host's IANA time zone" had no
   AC; AC-31 added. Unknown display zone → 400 (AC-32).
5. Email zone read can fail at send time; AC-23 now requires sending in UTC,
   labelled, not skipping.
6. AC-27 required an axe pass; neither repo has a scanner. Replaced by
   role/live-region assertions; scanner is a plan choice.
7. Accessibility gaps: no announcement on load, announce the day deselection,
   minus sign readable, 44 px for the new controls, disabled days exposed.
8. Demo source must carry a zone (AC-33). MCP: no tool touches public
   booking. Host side: drawer already names the zone; out of scope recorded.
9. §7 rollback: a newer web against a reverted server must detect the absence
   of a display-zone answer rather than mislabel link-zone days.

#### Criteria I could not falsify (before this revision)
- AC-18: no server answer can contain the "both 01:00" instants (blocking 1).
- AC-19: outcome depended on the run date and seed (blocking 5).
- AC-24: conditional on a contract choice; now an unconditional check.

#### Citations I checked and found wrong (corrected)
- `booking_rule.go:126-128` "slots are UTC instants" — that is `refusalText`; the UTC conversion is `:118`.
- `booking_rule.go:57-65` parseCalendarDay — actually `:55-63`; `linkLocation` `:23-46` (not `:22-45`).
- `handlers_booking.go:229`, `:249` — the encodes are `:230`, `:248`; body struct `:274-282`; instant match `:379-385`.
- `PublicBooking.tsx:312` zone on confirmation — `:313`; `buildIcs` `:71-96`, not `:70-99`.
- §2.2 "time_zone is not read anywhere" — true for the page, but the cause is the client mapper dropping it (`schedulingLinks.ts:401-414`) and the owner drawer does read it (`LinkEditDrawer.tsx:492`).
- §2.1 DST: omitted the first-occurrence rule that drives blocking 1.
All other file:line claims checked and correct (`browserTimezone` `:55-61`, `:112`, `:409-412`, `dateOnlyStr` `:26-28`, `:152-158`, `:456-463`, `:354-355`, `:178-186`, `:166-168`; `booking.go:575-583`, `:604-615`, `:517-518`; `api-audit.md:309`, `:657`; `PAC-67.md:83`; `SEAM-REQUIRED.md:286-300`; `playwright.config.ts:49-52`; compose `:118`).

#### The live day bug
Confirmed: the page sends the browser-local date (`dateOnlyStr`, `:26-28`,
`:152`) and the server reads it as a link-zone day (`parseCalendarDay`,
`handlers_booking.go:233`). Severity medium-low: no wrong instant is ever
booked and the details step shows the slot's true date; the harm is
misgrouped times, unreachable host-"today" times for visitors ahead of the
host, and every future day selectable for everyone. **No hotfix**: the
minimal correct fix is the regrouping this spec defines; ship PAC-88.

#### Consumers this breaks
- None found. `e2e/tests/public-booking.spec.ts` (UTC/UTC, AC-28) must keep
  passing; the slot-listing API journeys at `:114`, `:134`, `:193`, `:369`
  rely on the explicit-date mode, which OQ-1 (b) keeps as-is when no display
  zone is sent. No MCP consumer.

#### Remaining owner questions
- OQ-2 (remember the chosen zone across visits) — unchanged, not blocking.
- OQ-4 (email in the visitor's zone, pulling in API-051) — new, not blocking.
