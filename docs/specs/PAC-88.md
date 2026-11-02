# Spec — PAC-88: Public booking page shows times in the host's time zone by default, with a booker time-zone switcher

- **Linear**: https://linear.app/paceday/issue/PAC-88
- **Status**: draft
- **Author**: spec-author
- **Repos**: `Enach/clockwise-like` (api, at `32ecacf`), `Enach/smart-calendar-flow` (web, at `702a6c2`)
- **Owner decisions this implements**: PAC-72 D-9 (2026-11-01): "display the
  host-profile timezone by default and allow to change the timezone on the
  booking page"; A-2 (a collective link uses the link owner's zone, co-hosts'
  zones ignored); A-4 (an owner with no profile zone → UTC, shown as UTC).
- **Inherited scope**: no `x-uncertain` marker on any `/api/book/{slug}`
  operation (`contracts/openapi/paths/booking.yaml` has none). **Conditional:**
  if the contract stage modifies `POST /api/book/{slug}` (see OQ-1), the open
  audit finding **API-051** on that operation (`docs/factory/api-audit.md:309`,
  cancelling a booking re-opens an exhausted single-use link) becomes inherited
  under the touch-it-fix-it rule, as PAC-67 recorded it
  (`docs/specs/PAC-67.md:83`). API-051 is owned by PAC-40 / epic E5
  (`api-audit.md:657`); the contract stage must either avoid touching that
  operation or carry API-051.

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

## 2. Current behaviour

### 2.1 Server (api, `origin/main` `32ecacf`)

- **The link's zone.** Every public booking operation evaluates the link's
  days, hours, 15-minute grid, "today" and horizon in one zone: the **link
  owner's** profile zone, read at request time (`backend/api/booking_rule.go:22-45`,
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
  answers `{slots, available_dates}` only (`handlers_booking.go:229`, `:249`).
  With `date=YYYY-MM-DD` the date is **a calendar day in the link's zone**
  (`parseCalendarDay`, `booking_rule.go:57-65`; `handlers_booking.go:233-238`)
  and the slots are UTC instants (`booking_rule.go:126-128`). Without `date`,
  `available_dates` are link-zone days within the horizon that offer at least
  one slot (`handlers_booking.go:206-230`; contract `booking.yaml:1900-1910`).
- **DST on the server.** Link hours on a transition day are resolved by
  `wallClock` (`backend/engine/booking_window.go:34-…`), which reports
  non-existent wall times; the grid is built per host-zone day. Display of
  those instants in another zone is not the server's concern today.
- **Booking create** takes `start` (RFC3339 instant), optional `end` /
  `duration` / `duration_minutes`, `name`, `email`, `notes`
  (`handlers_booking.go:273-281`). No zone is sent or needed: the slot is
  compared as an instant (`:368-381`).
- **Confirmation answer** returns `start`/`end` as UTC RFC3339
  (`handlers_booking.go:110-111`; contract `booking.yaml:1990-1998`). No zone.
- **Confirmation email** (`backend/engine/booking.go:553-601`): the subject
  date is `start.Format("Mon Jan 2")` (`:575`) and the body prints
  `Date: … Time: HH:MM – HH:MM UTC` (`:580-583`) — i.e. **UTC**, with a UTC
  label; the subject's day can therefore differ from the booker's and the
  host's day. The attached ICS uses UTC `Z` instants (`:604-615`), which
  calendar clients convert correctly.
- **Host calendar events** are created with RFC3339 instants
  (`booking.go:517-518`); unaffected by display zone.

### 2.2 Web (`smart-calendar-flow`, `origin/main` `702a6c2`, `src/pages/PublicBooking.tsx`)

- The page's zone is **the browser's** (`browserTimezone`, `:55-61`;
  `const tz = useMemo(browserTimezone, [])`, `:112`). `time_zone` from link
  info is **not read anywhere** in `src/` outside generated types and an API
  test (`src/api/schedulingLinks.test.ts:539-546`).
- The zone label in the sidebar is the browser zone name, without offset
  (`:409-412`); the confirmation shows the same label (`:312`).
- The selected date is turned into `YYYY-MM-DD` with **browser-local** getters
  (`dateOnlyStr`, `:26-28`; `:152`) and sent as the `date` the server reads as
  a **host-zone** day (`:153-158`). This is the mismatch in §1.
- Times are formatted with `toLocaleTimeString` and no `timeZone`, i.e. in the
  browser zone (`fmtTime` `:29-32` → `src/lib/timeFormat.ts` `formatTime`;
  `fmtDateLong` `:33-40`). Always 12-hour (PAC-55 §5, `:30`). No offset is
  shown, so the two 1:30 AM times of a fall-back day are indistinguishable.
- The page never calls the date-less listing: `available_dates` comes from the
  per-date answer, where the server always sends `[]` (`handlers_booking.go:249`),
  so the guard at `:456-463` (`if (availableDates.size === 0) return false`)
  makes **every future day selectable**, including days beyond the link's
  horizon and non-link weekdays. "Today" is browser-local midnight
  (`:354-355`).
- Booking sends the slot's `start` string as received (`:178-186`), so the
  booked instant is correct regardless of zone.
- The downloadable ICS (`buildIcs`, `:70-99`) uses UTC `Z` instants: correct.
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

### 3.5 DST correctness

Across the whole horizon, and in both the link's zone and any chosen zone:
no time is shown at a wall-clock time that does not exist on that day
(spring-forward gap); the two occurrences of a repeated wall-clock time
(fall-back) are both shown when both are offered, each with its offset; the
offset shown for each time is the offset in force at that instant, not the
offset of "now".

### 3.6 Booking and confirmation

- The booking sends the same instant whichever zone is displayed. Refusals
  (409, generic or specific reason), 410, 429, 503 and every PAC-83 message
  and retry behave exactly as today.
- The on-page confirmation shows date, start–end times, the zone name and its
  offset at the booked instant, in the zone displayed when the visitor booked.
  "Book another time" keeps that zone.
- The downloadable calendar file keeps absolute (UTC) instants; unchanged.
- The confirmation **email** states the date (subject and body) and times in
  the zone the visitor had displayed when booking, with the zone name and
  offset — not UTC. If the server has no usable zone from the visitor, it uses
  the link's zone, labelled. The email's calendar attachment keeps absolute
  instants. (Factory-assumed, F-4.)

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
  (≥ 44 px).

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
and Tuesday (Auckland) are selectable; Monday lists 9:00 PM – 11:30 PM and
Tuesday lists 12:00 AM – 4:30 AM (Auckland), and no other day lists a time —
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
shows the PAC-83 error and retry, and no time of that day is shown. [unit]

**DST**

AC-17. Given a link in America/New_York offering 01:00–04:00 on the US
spring-forward Sunday, displayed in America/New_York, then no time between
02:00 and 02:59 is shown, and times before and after carry offsets −05:00 and
−04:00 respectively. [unit]

AC-18. Given a link in Europe/London on the October fall-back Sunday offering
00:30–02:30 and a 30-minute duration, displayed in Europe/London, then both
01:00 and both 01:30 instants that the server offers are shown, distinguishable
by offset (+01:00 vs +00:00), and their accessible names differ. [unit]

AC-19. Given a link with a 365-day horizon owned by a user in
America/New_York, a browser in Europe/Paris, and the next DST transition of
either zone (computed by the test at run time; always within 365 days), when
the visitor displays the day of that transition in each of the two zones, then
every listed time and its offset equals the server-offered instant rendered in
that zone at that instant, and the times listed across the transition day and
its neighbours equal the server's offered set for that range. [e2e]

AC-20. Given the horizon spans a date when the displayed zone's offset
differs from today's, then the label for that day shows that day's offset,
not today's. [unit]

**Confirmation**

AC-21. Given a visitor who booked while displaying Asia/Tokyo, then the
on-page confirmation shows the Tokyo date, start–end, "Asia/Tokyo" and
"UTC+09:00"; "Book another time" returns to the flow still in Asia/Tokyo.
[unit] [e2e]

AC-22. Given a booking made while displaying Asia/Tokyo for 2027-01-14 23:30
UTC, then the confirmation email's subject and body give Friday, January 15,
2027, 8:30 AM – 9:00 AM, Asia/Tokyo (UTC+09:00), and the email contains no
"UTC" time line; the attached calendar file's start equals
`20270114T233000Z`. Fails today (`engine/booking.go:575-583`). [unit] (Go)

AC-23. Given a booking whose visitor zone is unavailable to the server, then
the email uses the link's zone with name and offset. [unit] (Go)

AC-24. Given a booking request that carries a visitor zone the server cannot
load, then the booking outcome (status and body) is exactly what it would be
without it — a display zone never causes a refusal. [contract]
(Applies only if the contract carries a visitor zone, OQ-1.)

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
accessible name contains date, time and zone/offset. [unit] (testing-library
roles) + an axe pass on the page in [e2e]

**Unchanged**

AC-28. With browser and host both UTC, the existing `public-booking.spec.ts`
journeys pass unchanged. [e2e]

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

Display-only plus, possibly, an optional extra input on booking create and an
optional zone echo on the slot listing (OQ-1). No migration is expected (F-4
does not require storing the visitor's zone). Reverting the web PR restores
browser-zone display; reverting the api PR restores the UTC email. If the
contract adds only optional fields, either repo can be reverted
independently; a web build that sends a visitor zone to a reverted server
must still book successfully (AC-24's converse: unknown fields are ignored or
the contract makes the field optional — the contract stage must pin which).

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
- **F-4** The confirmation email uses the zone the visitor displayed when
  booking (fallback: link zone), name + offset, no UTC line; ICS unchanged
  (§3.6). Rationale: the issue says "the confirmation shows the chosen zone";
  the email is the confirmation the booker keeps.
- **F-5** Days, "today" and the horizon end are displayed-zone days; a day
  is selectable iff it has an offered start (§3.4).
- **F-6** A server zone the browser cannot render → display UTC, labelled.
- **F-7** A failure listing any part of a displayed day fails the whole day
  (PAC-83 consistency, §3.4).

### Open questions

- **OQ-1 (contract-author, stage 2) — how the page gets displayed-zone days
  and the email gets the visitor's zone.** Today the listing is keyed by
  host-zone day (`booking_rule.go:57-65`), so a displayed day needs either
  (a) the client to request the adjacent host days and regroup, or (b) the
  listing to accept a display zone and group by it, or (c) the date-less
  listing to return the full offered set. AC-16 (no partial day) and AC-26
  (zone consistency, which suggests the listing should state the zone it used
  — the issue's scope says "the slots answers carry the host's IANA time
  zone") constrain the choice. For F-4, booking create needs an optional
  visitor zone; that **modifies `POST /api/book/{slug}` and brings API-051 into
  scope** (see Inherited scope) — the contract stage must weigh carrying
  API-051 against alternatives. Resolve in `contracts/features/PAC-88.md`.
- **OQ-2 (owner, low priority, not blocking)** — should a visitor's chosen
  zone be remembered for later visits (on that browser), overriding the host
  default? The factory assumes not (F-3), as the literal reading of D-9.
- **OQ-3 (spec-challenger)** — is the e2e DST case (AC-19) deterministic
  enough without a server clock seam (`e2e/SEAM-REQUIRED.md:286-300`)? It
  relies on a real upcoming transition within a 365-day horizon and on the
  seeded host having free time that day; if the seed cannot guarantee free
  time 365 days out, the e2e needs either a clock seam (separate issue) or to
  assert on whichever transition-adjacent days do offer times.
