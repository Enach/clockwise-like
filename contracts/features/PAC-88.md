# Contract: PAC-88, the public booking page shows times in the host's zone by default, with a visitor zone switcher

- **Linear issue**: https://linear.app/paceday/issue/PAC-88
- **Spec**: `docs/specs/PAC-88.md` (challenged accept-with-changes 2026-11-02 at
  `85b4e25`; owner answers OQ-2 and OQ-4 of 2026-11-02 recorded in the spec by
  this revision; OQ-1 resolved here). This branch is `factory/pac-88-spec`.
- **Owner decisions encoded**: PAC-72 D-9 (host profile zone by default, the
  visitor can change the displayed zone), A-2 (a collective link uses the link
  owner's zone), A-4 (an owner with no zone is UTC, shown as "UTC"), PAC-88 OQ-2
  (the chosen zone is not remembered across visits), PAC-88 OQ-4 (the email
  shows the link's zone with its offset, then UTC; the visitor's zone in the
  email is a follow-up ticket).
- **Stage**: contract-author (2026-11-12), then contract-challenger.
- **Fragment edited**: `contracts/openapi/paths/booking.yaml`
  (`getPublicBookingSlots`, `PublicSlotsResponse`, one sentence in
  `PublicLinkInfo.time_zone`).
- **Generated**: `contracts/openapi/openapi.yaml`, `backend/api/gen/paceday.gen.go`
  (LF docker clone, api-gate pattern). `MIGRATION.md`: note on the
  `getPublicBookingSlots` row (kept handwritten, section 9). Web types are not
  regenerated here; that is the paired web PR (`REGEN=1` web-gate).
- **Base**: clockwise-like `origin/main` `32ecacf`; smart-calendar-flow
  `origin/main` `ae8b0d4` (the spec cited `702a6c2`; the lines this contract
  relies on, `src/api/schedulingLinks.ts` `getPublicLink`/`getPublicSlots`, are
  unchanged).

---

## 1. What changes at the HTTP boundary

| Operation | Change |
|---|---|
| `getPublicBookingSlots` (`GET /api/book/{slug}/slots`) | **Added query parameters** `display_time_zone`, `from`, `to` (all optional). **Added response properties** `time_zone`, `display_time_zone`, `range_from`, `range_to` (always present). Days are days of the *day zone* (`display_time_zone`, else the link's zone). The date-less mode becomes a bounded overview (at most 42 days, default today + 41) instead of the whole-horizon read; a storage failure there is now the 500 instead of an omitted day. Each accepted host's calendar is read at most once per request. Eight new 400 messages. No status added or removed. |
| `getPublicLinkInfo` | One description sentence (the listing states the same zone). No shape change. |
| `createPublicBooking` | **Unchanged** (spec AC-24; owner OQ-4). API-051 is not inherited. |

No `x-uncertain` marker and no open audit finding on these operations, before or
after. Not changed: every authenticated operation, every other public operation,
the rate limits, the 404/410/429/503 responses.

## 2. The listing, pinned

### 2.1 The day zone and how starts are grouped

- The **link's zone** is unchanged (PAC-72 revision 3.1): the link owner's
  profile zone read in this request, UTC when the owner has no settings row or
  an empty, `Local` or unloadable name, the 500 when the read fails. It governs
  the offered set: link days, hours, 15-minute grid, "today", horizon and the
  first-occurrence rule for repeated wall times.
- The **day zone** `Z` is `display_time_zone` when given and non-empty, else the
  link's zone.
- An offered start `s` **falls on** day `D` exactly when the calendar date of
  `s` read in `Z` is `D` (Go: `s.In(Z).Format(time.DateOnly) == D`). No
  "midnight" is computed, so zones whose midnight is skipped on a transition day
  need no special case.
- With `date=D`: `slots` = every offered slot whose start falls on `D`,
  ascending by start. Without `display_time_zone`, `Z` is the link's zone and
  the answer is exactly today's PAC-72 answer for link day `D` (a link window
  lies inside one link day, so every start of link day `D` falls on `D`).
- Without `date` (overview): `available_dates` = the days `D` in
  `range_from..range_to` on which at least one offered start falls, ascending,
  no duplicates.
- The link days a request needs are the link-zone dates of every instant of
  the requested `Z` days: for one `Z` day, two link days (three on a day where
  either zone changes offset); for an overview of `n` days, at most `n + 2`. A
  link day outside the horizon or not among `days_of_week` needs no host read
  (unchanged rule: answered before any host is read).
- **Equivalence (normative, contract test C-1)**: for every link, `Z`, duration
  and range, the answer equals the regrouping, by the rule above, of the union
  of the PAC-72 explicit-date answers (no `display_time_zone`) for the link days
  the request needs, computed at the same `now`. The booking rule is untouched,
  so every listed start books (PAC-72 AC-37 still holds).

### 2.2 The overview range

| Query | Range covered |
|---|---|
| neither `from` nor `to` | `from` = today in `Z` (server clock), `to` = `from` + 41 days |
| both | `from..to` inclusive; `to >= from`; `to - from <= 41` days |
| only one | 400 `from and to must be given together` |
| `date` with `from` or `to` | 400 `date cannot be combined with from or to` |

The bound is **42 days**: a six-week month grid, the most a month view shows
(spec AC-16b: "never ask the server to read more than the days on screen plus
the neighbouring days a displayed day overlaps"). It does not depend on
`booking_horizon_days` (1-365), so a 365-day link costs the same as a 7-day one.
`range_from`/`range_to` echo the resolved range (for `date`, both equal `date`),
so a default range can never be mistaken for the whole horizon.

### 2.3 Cost and the 503

- **One calendar read per host per request** (normative, contract test C-3):
  in both modes and for any `Z`, each accepted host's external calendar is read
  at most once, over one window from the earliest needed link day's window
  start to the latest needed link day's window end. Storage reads (focus blocks,
  bookings on the host's links) may be split as storage needs; they are not
  provider calls, but together they cover every needed link day
  (`storage.ListFocusBlocksForUserWeek` reads 7 days from its start, so a
  44-day window needs a range read or several weeks).
- **The one read is complete** (challenge 2026-11-12): `calendar.ListEvents`
  (`backend/calendar/events.go:14`) reads only the provider's first page
  (Google's default page is 250 events with `singleEvents`). A one-day window
  never reached it; a 44-day window of a busy host does, and the events past
  the first page would silently be free time, breaking C-1 and listing slots
  the booking then refuses as `host_busy`. The read follows every page
  (`nextPageToken`) inside the host's own limit; a read that cannot be
  completed is an unreadable calendar (busy, D-6), never a partial list.
  Pinned by C-10.
- **Robustness residual (D-6, owner-decided)**: one read now covers up to 44
  days, so a host whose provider fails or hits its own 15 s limit makes the
  whole overview offer no day (a 200), where the per-day reads lost only the
  failing days. This is D-6 applied to a larger window, not a new rule. An unreadable calendar (not connected, provider error, own
  15 s limit) makes that host busy for that whole window (D-6: no slot in the
  answer), exactly what the per-day reads gave.
- The PAC-81 budget, per-item limit, 4-way host concurrency and the 503 for a
  host the budget did not reach are unchanged. With one read per host, an
  overview costs what one explicit-date listing costs, so the PAC-72 "long
  horizon 503" (`TestPAC72_LongHorizonDatelessListingIs503`) no longer exists;
  the test is replaced by C-9 (a 365-day link's overview of its last month,
  with a delayed fake calendar, answers 200 inside the 1 s test budget).

### 2.4 Validation order and 400 bodies

Order (the existing order is kept; the PAC-88 checks are appended where the
`date` check was): rate limit (429) → link lookup (404, 410) → `duration` (400)
→ link's zone (500) → `display_time_zone` → `date` with `from`/`to` → `date`
→ `from` → `to` → pairing → order → span. Every 400 is the `BookingError`
envelope `{"error":"<message>"}`, Content-Type `application/json`:

| Check | Message |
|---|---|
| `duration` (unchanged) | `invalid duration` |
| `display_time_zone` fails the pattern `^[A-Za-z][A-Za-z0-9_+-]*(/[A-Za-z0-9_+-]+){0,2}$` or 64 characters, is `Local`, or `time.LoadLocation` fails | `invalid display_time_zone, use an IANA time zone name` |
| `date` with `from` or `to` | `date cannot be combined with from or to` |
| `date` not `YYYY-MM-DD` (unchanged) | `invalid date format, use YYYY-MM-DD` |
| `from` not `YYYY-MM-DD` | `invalid from, use YYYY-MM-DD` |
| `to` not `YYYY-MM-DD` | `invalid to, use YYYY-MM-DD` |
| exactly one of `from`, `to` | `from and to must be given together` |
| `to` before `from` | `to must not be before from` |
| more than 42 days | `range must not exceed 42 days` |

An empty value (`display_time_zone=`) is the same as an absent one (as `date=`
already is). Unknown parameters are ignored. `UTC` and `Etc/*` names are
accepted; tz-database links (`Asia/Calcutta`, `US/Eastern`) load and are echoed
as sent, not canonicalised. Also refused with the same message (challenge
2026-11-12): `localtime` and `posixrules` (on Debian-like hosts
`/usr/share/zoneinfo/localtime` links to `/etc/localtime`, so it would load the
server process's zone, which must have no effect, AC-28) and any name starting
with `right/` (leap-second variants: Go ignores their leap records, so
instants are off by about 27 s). `posix/...` names are plain copies and are
accepted. The backend image ships Alpine `tzdata`
(`backend/Dockerfile:3,12`); the test process uses Go's own zoneinfo, so
contract tests use only long-stable names.

### 2.5 Response (200)

```json
{
  "slots": [],
  "available_dates": ["2027-03-01", "2027-03-02"],
  "time_zone": "Europe/Paris",
  "display_time_zone": "Pacific/Auckland",
  "range_from": "2027-03-01",
  "range_to": "2027-04-11"
}
```

`time_zone` is the link's zone used (AC-31, equal to `PublicLinkInfo.time_zone`
read at the same moment; a host who changes zone changes it on the next request,
AC-26). `display_time_zone` echoes the request, else equals `time_zone`. Slot
instants stay UTC RFC3339 (`PublicBookingSlot`, unchanged).

### 2.6 Rate limit and caching

- **Rate limit unchanged**: 120 requests per client per fixed minute, shared by
  both modes, any `display_time_zone`; an overview counts once whatever its
  range. The web's request budget per visit (section 3.3) stays far below it,
  including for visitors sharing one NAT address.
- **Caching**: no `Cache-Control`, `ETag` or `Last-Modified` (none today, none
  added; adding `no-store` was considered, section 8). Without a validator or
  explicit freshness, browsers do not store the answer heuristically. The web
  may reuse an answer only within the visit, keyed by slug, duration, day zone
  and day/range.

## 3. Web client interface (binding for the smart-calendar-flow PR)

### 3.1 `src/api/schedulingLinks.ts`

- `getPublicLink` keeps `time_zone` (string) and `booking_horizon_days`
  (number) in `PublicLinkInfo` (today the mapper drops both, spec 2.2).
- `getPublicSlots(slug, { date?, from?, to?, duration?, displayTimeZone? })`
  sends `display_time_zone`, `date`, `from`, `to`, `duration` as given and
  returns `{ slots, available_dates, time_zone?, display_time_zone?,
  range_from?, range_to? }` (the four PAC-88 keys optional in the client type,
  because an older server omits them, section 5). The "no date → no request"
  shortcut goes: a request without `date` is the overview and needs `from` and
  `to`. Responses stay loosely parsed (no strict zod at run time).

### 3.2 Requests the page makes

- Every listing request carries `display_time_zone` = the zone being shown,
  **including when it is the link's zone** (uniform legacy detection, section 5).
- Overview: one request per visible calendar grid, `from` = first cell, `to` =
  last cell (at most 42 days); re-issued on month change, duration change and
  zone change. Never a request without `from`/`to`.
- Day: `date` = the selected day in the displayed zone; one request per
  selected day, re-issued on zone or duration change.
- Each answer's `display_time_zone` must equal what was sent and its days are
  rendered only then (else section 5). Each answer's `time_zone` must equal the
  `PublicLinkInfo.time_zone` the page holds; otherwise the page re-reads link
  info, switches the default display to the new zone if the visitor had not
  switched, and re-requests, never rendering the mismatched answer (AC-26).
- A 400 `invalid display_time_zone, use an IANA time zone name` (a zone the
  browser lists but the server's tz database lacks): the page returns to the
  zone shown before the switch, re-requests in it, and shows "Times can't be
  shown in <zone>. Choose another time zone." (`data-testid="booking-tz-unsupported"`).
  It never shows times in the refused zone.
- Failures of the day request or the overview keep the PAC-83 semantics
  (AC-16, AC-16a): one answer per displayed day, so a day is all or nothing by
  construction; an overview failure leaves no day selectable.
- Regression guards against today's page (challenge 2026-11-12, web `ae8b0d4`):
  - `PublicBooking.tsx:459` "`availableDates.size === 0` means every future
    day selectable" goes: an overview answer with no dates means no
    selectable day; while the overview for the current (zone, grid, duration)
    has not answered, no day is selectable.
  - `PublicBooking.tsx:157` `placeholderData: (prev) => prev`: an answer kept
    as placeholder from another zone, day or duration is never rendered as
    the current one (zone, day/range and duration are in the query key, and
    the echo check above applies to placeholders too).
  - `PublicBooking.tsx:457` `date < today` uses the browser's today: it
    becomes today in the displayed zone (AC-12).
  - A 429 on the overview or a day keeps `SLOTS_RATE_LIMITED_MESSAGE`
    (`PublicBooking.tsx:49`, `:497`); any 400 other than the display-zone one
    is a PAC-83 error with retry, never "no time available".

### 3.3 Request budget per visit

Load: link info + 1 overview. Per day picked: 1. Per month moved: 1. Per zone
switch: 2 (overview + selected day). Per duration change: 2. A visitor who looks
at three months, picks five days and switches zone twice makes about 14
listing requests; the limit is 120 per minute.

### 3.4 State and storage (owner OQ-2)

The chosen zone lives in page state only: never written to `localStorage`,
`sessionStorage`, IndexedDB, a cookie or the URL. A new load opens in
`PublicLinkInfo.time_zone` (or UTC when the browser cannot format it, spec F-6).

### 3.5 Test ids (rendered by the web, used by e2e)

`booking-tz-label` (zone name + offset text), `booking-tz-switcher` (the "Time
zone" combobox), `booking-tz-use-mine` ("Use my time zone (...)"),
`booking-tz-back-to-host` (back to the host's zone), `booking-tz-unsupported`
(section 3.2), `booking-tz-announcer` (the polite live region). Day cells and
time buttons keep their existing ids; their accessible names follow spec 3.9.

Accessibility pins (challenge 2026-11-12, spec 3.9, AC-27): the switcher is a
combobox labelled "Time zone" with the current zone as its accessible value
and a listbox of options, following the ARIA combobox pattern (open, type to
filter, arrows, Enter, Escape). The "unsupported" text is announced through
`booking-tz-announcer` and focus stays on the switcher. In link-zone mode
(section 5) the switcher is `aria-disabled="true"`, still focusable, with the
"unavailable right now" text as its `aria-describedby`. Nothing is announced
on load, on a host-zone change the visitor did not cause, or on a re-render.

### 3.6 Demo source (non-production builds only)

The demo `PublicLinkInfo` carries `time_zone: "Europe/Paris"` and
`booking_horizon_days: 60`; demo slot answers carry the four PAC-88 keys and
are grouped by the same rule (AC-33). Production builds contain no demo path
(PAC-83 OQ-1, unchanged).

## 4. The confirmation email (non-HTTP interface, owner OQ-4)

Composed by the engine after the booking is saved (`engine/booking.go`
booking-completion unit), from the link, the booking and the **link's zone read
at compose time** by the PAC-72 rule (owner's settings; no row, empty, `Local`
or unloadable → UTC). Go layouts in brackets; `–` is U+2013 (as today), `−` is
U+2212 MINUS SIGN; offsets are `UTC±hh:mm`, the offset in force at that instant.

Link zone other than UTC (AC-22, Los Angeles example):

```
Subject: Confirmed — <title> on Thursday, January 14          ["Monday, January 2", start in link zone]

Hi <name>,

Your meeting with <hosts> is confirmed.

Date: Thursday, January 14, 2027                              ["Monday, January 2, 2006", start in link zone]
Time: 7:30 PM – 8:00 PM, America/Los_Angeles (UTC−08:00)       ["3:04 PM", start/end in link zone; offset at start]
In UTC: 03:30 – 04:00 UTC on Friday, January 15, 2027          ["15:04", "Monday, January 2, 2006", start in UTC]
```

- If the offset at the end differs from the offset at the start (the meeting
  spans a transition), the Time line is
  `Time: 1:30 AM (UTC−04:00) – 1:15 AM (UTC−05:00), America/New_York`.
- **Link zone UTC** (A-4: owner without a zone), and **zone unreadable** at
  compose time (AC-23): subject date in UTC, and the body has no "In UTC" line:

  ```
  Date: Friday, January 15, 2027
  Time: 03:30 – 04:00 UTC
  ```

  The unreadable case is still sent (never skipped) and logs
  `WARN: booking confirmation email for booking <id>: link zone unreadable, sent in UTC: <redacted error>`
  (redacted with `outbound.RedactAddresses`, as the existing email error log).
- The ICS attachment is unchanged: `DTSTART`/`DTEND` UTC `Z` instants
  (AC-22: `20270115T033000Z`). The MIME structure, From/To and boundary are
  unchanged.
- `ConfirmBooking`'s signature and `createPublicBooking`'s request do not change.

## 5. Compatibility and rollback

| Pair | Behaviour |
|---|---|
| **Older web (main `ae8b0d4`) → new server** | The older page sends `date` + `duration` only and never calls the date-less mode (`schedulingLinks.ts` returns early without a date). Without `display_time_zone` the explicit-date answer is byte-for-byte the old lists plus four added keys, which the loosely parsed client ignores. The date-less changes (bounded default range, 500 instead of an omitted day) reach no web consumer. Its known day mismatch (spec section 1) is unchanged until the web PR ships. |
| **Newer web → old server (api rolled back)** | The old server ignores `display_time_zone`, `from`, `to` and answers without `display_time_zone`. The page detects the missing (or different) `display_time_zone` on the first listing answer and enters **link-zone mode** for the rest of the visit: it displays `PublicLinkInfo.time_zone` (present on the old server since PAC-72), disables the switcher with the text "Time zone switching is unavailable right now", requests days as link-zone `date`s (correct on the old server because display zone = link zone), and uses the old date-less answer's `available_dates` (whole horizon, link-zone days) for the grid if it is a 200; if the old date-less read is a 503 or 500, the calendar shows the PAC-83 error with retry (AC-16a), never "every day selectable". It never renders an answer's days as days of a zone the server did not confirm. |
| **Newer web → server before PAC-72** | Out of scope: PAC-72 is on main; `time_zone` absent from link info means UTC display (A-4 reading). |

Rollback is code-only: no migration, no stored data. Revert the web PR to get
browser-zone display back; revert the api PR to get the UTC email and the
pre-PAC-88 listing (the newer web degrades to link-zone mode, above). Reverting
the contract restores the old fragment; the paired web type regeneration reverts
with it.

## 6. e2e seed (deterministic DST and zone journeys)

Added to `e2e/seed/seed.sql` and `e2e/seed/ids.ts` (`seed.ts` verify derives its
sets from `ids.ts`; `SEEDED_ACCEPTED_HOST_ROWS` becomes `SEEDED_LINK_IDS.length
+ 2`: the D-6 co-host and the Los Angeles co-host). All hosts are connected
through the calendar stand-in (oauth token `e2e-pac88-*-work`, expiry 2099).

Seed rules (challenge 2026-11-12):

- The four hosts join `SEEDED_USER_IDS`, the three links `SEEDED_LINK_IDS`,
  and the four hosts' tokens `ACCEPTED_CREDENTIALS` (`ids.ts:299`). A token
  the stand-in does not accept is an unreadable calendar: D-6 would make
  every PAC-88 link offer nothing and every journey fail.
- Settings rows for New York, Paris and Los Angeles carry
  `calendar_provider = 'google'` (seed check `seed.ts:191`). `seed.sql` deletes
  any settings row of `PAC88_HOSTS.noZone` (a stale row would silently change
  AC-3), and `verify()` asserts the three zones and the absence of that row.
- Calendars: only the booking journeys (AC-9, AC-21) write to the Paris and
  Los Angeles stand-in calendars (a booking creates host events) and add
  bookings that are busy time. They run in one spec file, serially, on
  "booking Mondays" no read-only journey uses (AC-1, AC-2, AC-7 use the AC-10
  Monday or another unbooked Monday), and each picks its start at run time
  as the first offered start on its Monday, so a retry (`E2E_RETRIES`) takes
  the next free start instead of a 409. The `dst` and `noZone` links'
  calendars are written by no journey.
- Other specs: every new row belongs to new users and new `e2e-pac88-*`
  slugs; no existing spec counts users, links or settings outside its own
  fixtures (the exact sets come from `ids.ts`), so no other count moves.

| Fixture | id | Profile zone | Role |
|---|---|---|---|
| `PAC88_HOSTS.newYork` (`e2e.pac88.ny@paceday.test`) | `b4b4b4b4-b4b4-4b4b-8b4b-b4b4b4b4b4b4` | `America/New_York` | owner and only host of `PAC88_LINKS.dst` |
| `PAC88_HOSTS.paris` (`e2e.pac88.paris@paceday.test`) | `b5b5b5b5-b5b5-4b5b-8b5b-b5b5b5b5b5b5` | `Europe/Paris` | owner and host of `PAC88_LINKS.paris` |
| `PAC88_HOSTS.losAngeles` (`e2e.pac88.la@paceday.test`) | `b6b6b6b6-b6b6-4b6b-8b6b-b6b6b6b6b6b6` | `America/Los_Angeles` | accepted co-host of `PAC88_LINKS.paris` (A-2) |
| `PAC88_HOSTS.noZone` (`e2e.pac88.utc@paceday.test`) | `b7b7b7b7-b7b7-4b7b-8b7b-b7b7b7b7b7b7` | no settings row | owner and host of `PAC88_LINKS.noZone` (A-4) |

| Link | id | slug | days | window | horizon | notice / buffers | usage |
|---|---|---|---|---|---|---|---|
| `PAC88_LINKS.dst` | `cbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb` | `e2e-pac88-dst-30min` | `{0,1,2,3,4,5,6}` | 00:00-23:59 | 365 | 0 / 0, 0 | reusable |
| `PAC88_LINKS.paris` | `cacacaca-caca-4aca-8aca-cacacacacaca` | `e2e-pac88-paris-30min` | `{1}` (Monday) | 09:00-17:00 | 365 | 0 / 0, 0 | reusable |
| `PAC88_LINKS.noZone` | `cddddddd-dddd-4ddd-8ddd-dddddddddddd` | `e2e-pac88-utc-30min` | `{1,2,3,4,5}` | 09:00-17:00 | 60 | 0 / 0, 0 | reusable |

(`cccccccc-...`, the first choice, is already `TEAMS.connectedManager`,
`ids.ts:183`; e2e ids stay unique across fixtures.)
All `duration_options = {30}`, `refusal_message` default (generic). The links
are inserted by a separate `INSERT` naming `booking_horizon_days`.

- **AC-19** (`PAC88_LINKS.dst`, browser `timezoneId: 'Europe/Paris'` via
  `test.use`, the global config stays UTC): the test computes, from the tz
  database at run time, the next transition of `America/New_York` and of
  `Europe/Paris` at least two days after now; for each transition day and the
  day either side, in each of the two zones, it reads the server's answer for
  that day **through the API with `display_time_zone`** and also the PAC-72
  explicit-date answers for the overlapping link days, asserts the two agree
  (C-1 end to end), then asserts the page lists exactly those instants with
  their offsets. Never the date-less mode over the horizon. Nobody books on
  this link.
- **AC-10** (`PAC88_LINKS.paris`, display `Pacific/Auckland`): the first
  Monday between 1 December and the end of February at least two days ahead
  (always inside 365 days); booking journeys (AC-9, AC-21) on this link use the
  first Monday at least two days ahead that is not the AC-10 Monday, each a
  distinct start.
- **AC-1, AC-2, AC-7** use `PAC88_LINKS.paris` (collective, co-host in Los
  Angeles); **AC-3** uses `PAC88_LINKS.noZone`.
- `e2e/playwright.config.ts` (`timezoneId: 'UTC'`) and the compose `TZ: UTC`
  stay as they are (AC-28).

## 7. Tests

Contract and unit tests the api PR adds (Go, `backend/api/pac88_*_test.go`,
`backend/engine/*_test.go`):

- **C-1** equivalence (section 2.1) over `Z` in {link zone, UTC,
  `Pacific/Kiritimati` (+14), `Etc/GMT+12` (−12), `Asia/Kolkata` (:30),
  `Asia/Kathmandu` (:45), `America/New_York`, `Pacific/Auckland`}, several
  links (Paris, New York, UTC, a 1-day and a 365-day horizon) and both modes,
  including the fixed DST dates of AC-17, AC-18 and AC-18a.
- **C-2** each 400 message and the validation order (one request per adjacent
  pair of checks, e.g. an invalid zone and an invalid date answer the zone's
  message; an unknown slug with an invalid zone is 404).
- **C-3** one provider read per accepted host per request: a counting fake
  calendar sees exactly `hosts` reads for an overview of 42 days and for a `Z`
  day spanning three link days; zero for a range wholly outside the horizon.
- **C-4** a storage failure (accepted hosts, focus blocks, host bookings) on
  any needed link day makes the overview and the day answer 500
  `{"error":"internal error"}`.
- **C-5** the four keys always present; `display_time_zone` echoes as sent
  (`Asia/Calcutta` stays `Asia/Calcutta`); `time_zone` equals link info; a host
  zone change between two requests changes `time_zone` on the second (AC-26).
- **C-6** no `Cache-Control`, `ETag`, `Last-Modified` header on 200.
- **C-7** rate limit: an overview counts one; 121st request in a minute is the
  429 whatever the parameters.
- **C-8** without `display_time_zone`, `date` answers equal the pre-PAC-88
  lists (the existing PAC-72 suites, unchanged, are the proof).
- **C-9** (replaces `TestPAC72_LongHorizonDatelessListingIs503`) a 365-day link
  with a 20 ms calendar delay under a 1 s budget: the overview of the month of
  the last bookable day answers 200 (AC-16b).
- **C-10** (challenge 2026-11-12) completeness of the one read: a Google stub
  answering the window in two pages (`nextPageToken`), with a busy event only
  on page 2 on day 40 of a 42-day overview, removes that day's covered
  starts; a page-2 failure makes the host busy for the window (D-6, 200 with
  no day); a focus block and a booking on the last needed link day each
  remove their starts (a 7-day focus read fails this test).
- **C-2 additions**: `localtime`, `posixrules` and `right/Europe/Paris` answer
  the display-zone 400.
- **E-1..E-4** email (Go): AC-22 Los Angeles; a UTC owner; the unreadable-zone
  fallback and its log line (AC-23); a meeting spanning a transition.

Existing tests amended by the api PR (each only where it reads the date-less
mode, which no longer reads the whole horizon or omits a day):

- `pac72_offerable_start_test.go` `TestPAC72_ListingDaysAndHorizon`: the 60-day
  horizon is read as two overviews (`from`/`to`), whose union is the 44 dates.
- `pac72_offerable_start_test.go` `TestPAC72_UnreadableBusyTime`: the date-less
  read is now 500 `internal error`, like the explicit date.
- `pac72_offerable_start_test.go` process-zone case (AC-22 of PAC-72, `:835`)
  and `pac72_rev2_test.go` horizon cases (`:125-150`) and case (e) (`:300-320`):
  date-less reads become overviews covering the asserted days (a horizon of `n`
  days is read in `ceil(n/42)` overviews).
- `pac72_rev2_test.go` `TestPAC72_LongHorizonDatelessListingIs503`: replaced
  by C-9 (its premise, a whole-horizon read, no longer exists).
- `BookingFlow` fakes (challenge 2026-11-12): one read per host needs a range
  method on `BookingFlow` (`api/interfaces.go:59`) or a changed
  `CollectiveSlots`; the fakes in `handlers_booking_test.go`,
  `pac81_paths_test.go` (`cutEngine`; `TestPAC81_SlotListingCutIs503`'s
  date-less path is now the 42-day overview, still 503 when cut) and
  `ratelimit_booking_test.go` (`countingBookingFlow`;
  `TestGetSlots_RateLimited` sends date-less requests, still 200) follow it;
  their assertions do not change.

Every other PAC-72, PAC-81 and rate-limit test passes unchanged.

## 8. Alternatives rejected

- **(a) The client requests the overlapping link days and regroups.** Two (or
  three) answers per displayed day must then be assembled, and AC-16 needs
  client code to make the pair atomic; the horizon edges and "today" would be
  recomputed in the browser beside the server's rule; it doubles the requests
  against the 120/min limit shared behind a NAT; and it has no answer for the
  overview except one request per day. (b) makes one displayed day one answer.
- **(c) The date-less listing returns the full offered set.** It is today's
  whole-horizon read with a bigger answer: the PAC-72 503 on long horizons
  stays.
- **Keeping the whole-horizon date-less mode beside the new range.** No
  consumer calls it (the web returns early without a date; e2e and MCP never
  call it; only Go tests do), and it keeps a known 503 and the silent "omitted
  day" the spec forbids (section 3.4). Replacing it by the bounded default is a
  shape-compatible change with no live consumer; keeping it would leave a trap
  documented as "do not use".
- **A separate overview operation** (`/slots/overview`). Cleaner name, but a
  second rate-limit bucket or a shared one across two routes, a second
  generated type, and a second failure surface for the same rule. The modes
  already share one operation and one limit.
- **Range bound = horizon or 62 days.** The horizon bound is the 503 again; 62
  (two months) doubles the cost for no screen the page shows. 42 is the
  six-week grid.
- **Parameter name `tz` or `time_zone`.** `time_zone` already means the link's
  zone in both answers; `tz` is terse but unlike every other name in this
  domain. `display_time_zone` says what it changes.
- **Pass the checked zone into `ConfirmBooking` for the email** (instead of a
  read at compose time). Exact by construction, but it changes an engine
  interface and its fakes, and leaves AC-23 untestable. A read at compose time
  is the same rule read after the host events are created (each up to
  `HostReadLimit()`, 25 s, `engine/booking.go:506`), so seconds later, not a
  fraction of a second; a host who changes zone in that window gets an email
  in the new zone, still labelled, with the UTC line and the ICS instant
  unchanged. The read runs on the completion unit's context (`lifecycle.Go`),
  never the request's, which has ended: a read failing because the drain
  cancelled the unit is the AC-23 path (UTC, still sent). Accepted residual.
- **Email in the visitor's zone.** Owner OQ-4: follow-up ticket (it needs a
  booking-create input and inherits API-051).
- **`Cache-Control: no-store` on the listing.** Correct in spirit, but nothing
  stores the answer today and the spec does not ask for a header; adding one is
  a behaviour change for every client and proxy. Recorded, not done.
- **Canonicalising zone names** (`Asia/Calcutta` → `Asia/Kolkata`). Go has no
  canonical-name API; echoing as sent lets the page compare by string equality.

## 9. MIGRATION.md

`getPublicBookingSlots` stays `handwritten`, with a PAC-88 note: the generated
binder would validate the new query parameters before the handler, answering a
malformed query with its own 400 ahead of the 429, the 404/410 and the
link-zone 500 that this contract orders first. Moving it is a contract change.

## 10. Consumers

| Consumer | Breaks | Detail |
|---|---|---|
| Enach/smart-calendar-flow `PublicBooking.tsx`, `schedulingLinks.ts` | no (older build) / changes (web PR) | Section 5; the web PR implements section 3. |
| `src/api/generated` (web) | regenerated | `PublicSlotsResponse` gains four required keys; the public client does not parse with it. |
| `e2e/tests/public-booking.spec.ts`, `scheduling-links.spec.ts`, `seam.spec.ts` | no | Explicit-date calls without `display_time_zone`: same lists (AC-28). |
| Go tests reading the date-less mode | amended | Section 7. |
| `backend/api/gen` | regenerated | New optional query params in `GetPublicBookingSlotsParams` and response fields; the handler is handwritten (not mounted through the generated wrapper). |
| mcp | no | No MCP tool calls `/api/book` (spec 2.1). |
| Operators | no | No migration, no configuration. |

## 11. Gate results (contract stage)

See the manifest `gates` field for the exact commands and results (LF docker
clone, golang:1.25-bookworm).

## 12. Merge order

The api PR (server, email, seed, API-level e2e checks) merges first; the web PR
follows; the UI e2e journeys (AC-1, AC-2, AC-3, AC-7, AC-9, AC-10, AC-19, AC-21)
land with or after the web PR in the e2e gate of the pair. Either order is safe
for users (section 5).

**PAC-87** (`factory/pac-87-spec`) edits `habits.yaml` and `scheduling.yaml`,
never `booking.yaml`; both PRs regenerate `contracts/openapi/openapi.yaml` and
`backend/api/gen/paceday.gen.go`, and both touch `e2e/TESTIDS-REQUIRED.md`.
Whichever merges second rebases, re-runs `make openapi` in an LF clone,
commits the regenerated bundle and Go types, and records the new sha256 as
its `contractHash`; its fragment text does not change.

**Base**: merged `origin/main` `4f798cb` (CI workflows only; bundle unchanged
by it).
