# Spec — PAC-55: Let users choose 24-hour time (default stays 12-hour)

- **Linear**: https://linear.app/paceday/issue/PAC-55
- **Status**: challenged
- **Author**: spec-author
- **Inherited scope**: no `x-uncertain` on the settings read/write operations
  (the markers in `contracts/openapi/paths/scheduling.yaml:244,374,918,1204` are
  on other operations). Open findings on those operations: API-002, -003, -004,
  -013, -025, -039, -081, -082. **None is resolved by PAC-55; all are
  prerequisites owned elsewhere** (product-owner decision, 2026-10-11):
  - API-002, -003, -004, -013, -081, -082 are claimed by PAC-24, which claims
    eight findings in all (also API-009, -037)
    (`contracts/features/PAC-24.yaml:117-119`).
  - API-039 (`docs/factory/api-audit.md:273`) is grouped with API-013
    (`docs/factory/api-audit-issues.yaml:227`) and belongs with PAC-24.
  - API-025 (`api-audit.md:259`) is in the grouped validation issue
    (`api-audit-issues.yaml:326`).
  These must be resolved, or claimed by those owners in a merged contract, before
  PAC-55's contract changes the settings operations (OQ-4).
  Written from reading source; nothing was run.

## 1. Problem

Every time of day in the web app reads in 12-hour form ("11:00 AM"). People who
read time on a 24-hour clock — most of the world outside the US — have to
translate every meeting, slot and focus block in their head, and there is no
way to change it. The product owner decided on 2026-10-11: the default stays
12-hour, and each signed-in user gets **one** choice, 12-hour or 24-hour, that
applies to every time they see in the app. "Go for simplicity": a single
setting, no per-screen overrides, no detection from the browser.

The issue lists "booking pages the user hosts" among the surfaces covered. This
spec reads that as **the host's own in-app views** of their links and bookings,
which follow the preference. The public booking page a booker opens is not the
host's view; it always shows 12-hour (owner, 2026-10-11).

## 2. Current behaviour

### 2.1 How the web formats times

- Since smart-calendar-flow#86 one constant fixes the formatting locale to
  `en-US` (`smart-calendar-flow/src/lib/locale.ts:8`), and a guard test fails if
  any source formats with the browser locale (`src/lib/locale.test.ts:15-29`).
  The locale fixes both the *words* (weekday/month names) and the clock form.
- Times of day are formatted at ~20 call sites, each with its own options (under
  `src/`): `components/EventDetailView.tsx:41-42`, `links/LinkBookingsDialog.tsx:21`,
  `NLPConfirmModal.tsx:19`, `QuickActions.tsx:140`, `QuickCreatePopover.tsx:41`,
  `RescheduleSuggestions.tsx:116`, `ScheduleSuggestModal.tsx:38`,
  `team/FindATimeTab.tsx:24-25`, `TodayAgenda.tsx:22`, `EventOwnership.tsx:54`
  (composed message text, full date-time with seconds), `pages/Audit.tsx:13`,
  `pages/Dashboard.tsx:237,498`, `pages/Team.tsx:639`, `pages/PublicBooking.tsx:27`
  (public page). They already disagree: some give "02:30 PM", others "2:30 PM".
- Two surfaces do **not** go through the locale constant at all:
  - Protected hours on the Team page use a hand-rolled formatter that appends
    "AM"/"PM" (`src/api/teams.ts:818-823`, used at
    `components/team/ProtectedHoursTab.tsx:194,238,366,409,459`).
  - The calendar grid's hour axis is rendered by the calendar library with no
    locale or label format passed (`pages/Dashboard.tsx:471-484`), so it uses the
    library's own English default ("7am"). Only the event-tile time inside it is
    formatted by the app (`:496-500`). The grid renders even for a user whose
    calendar is not connected (`:442-471`).
- Time **entry** uses native browser time inputs (`pages/Settings.tsx:292,301,
  428,439`, `settings/WorkingHoursEditor.tsx:45,56`, `EventEditForm.tsx:182,193`,
  `links/LinkEditDrawer.tsx:379,383`, `team/ProtectedHoursTab.tsx:536,540`).
  Whether these show AM/PM is decided by the browser and operating system.
- No unit test asserts any formatted time string. The only test that pins a
  clock form is the public booking e2e, which requires slot buttons to match
  `h:mm AM|PM` (`clockwise-like/e2e/tests/public-booking.spec.ts:298`).
- The e2e stack cannot show calendar events: they come only from Google and there
  is no seam to fake them (`e2e/tests/calendar.spec.ts:1-18`, `e2e/SEAM-REQUIRED.md`
  item A). Bookings, by contrast, can be created in e2e.

### 2.2 How settings are stored and read today — a single shared row

- The read selects `FROM settings WHERE id = 1` (`backend/storage/settings.go:227`),
  creating it with `VALUES (1)` (`:262`); the write upserts the same row
  (`:269-349`). The `/api/settings` handlers take no user id
  (`backend/api/handlers_settings.go:31-64`) and `GET`/`PUT /api/settings` sit
  behind auth (`backend/api/routes.go:72-76`), so every signed-in user reads and
  overwrites **one** record (API-002, `docs/factory/api-audit.md:236`).
- `settings.user_id` exists and *is* queried elsewhere — `GetSettingsByUser`
  (`settings.go:418-442`), `ListUsersWithAutoSchedule` (`:379-380`),
  `ListUsersWithAutoDecline` (`:401-402`), called by the engines
  (`engine/focus_time.go:76`, `auto_decline.go:66`, `habits.go:70,96`) — but no
  `/api/settings` handler uses it. Migration 018 attributed the shared row to the
  earliest user only if a user existed when it ran
  (`migrations/018_per_user_settings_and_calendars.up.sql:15-17`).
- **So "per-user preference" is not expressible today**: a value added to the row
  the settings page reads would be global — one user choosing 24-hour would
  switch everyone.

### 2.3 Saving is a whole-document replace

- `PUT` decodes into a zero-valued record and writes every column
  (`handlers_settings.go:41-64`), so any property a writer omits is stored as its
  zero value (API-004, `api-audit.md:238`).
- The web reads with `normalizeSettings` and writes the whole edited draft with
  `settingsRequestBody` through `PUT` (`src/api/client.ts:575-628,872-880`;
  `src/pages/Settings.tsx:200-214`; `src/hooks/useSettings.ts:13-21`). The
  adapter maps every modelled field to camelCase (`client.ts:540-573`) and
  spreads unknown properties through in both directions.
- If the server is unreachable the client silently falls back to in-memory mock
  settings (`client.ts:463-476`).

### 2.4 The contract already describes PAC-24, the server does not

The committed fragment (`contracts/openapi/paths/scheduling.yaml:532-641`) and
the generated types in both repos (`backend/api/gen/paceday.gen.go:2896,5275`,
`src/api/generated/types.ts:1994`) already describe the post-PAC-24 interface —
caller-scoped read, merge `PATCH` as the only write, `PUT` removed, and a write
body that refuses unknown properties (`scheduling.yaml:1984-1995`) — while the
server still serves the singleton `PUT` (§2.2–2.3). PAC-24's spec is `draft`
with four blocking challenge findings (`docs/specs/PAC-24.md:816-874`); its
contract is revision 2, `draft` (`contracts/features/PAC-24.yaml:4-5`). The
divergence is PAC-24's to close; PAC-55's contract stage builds on that fragment.

### 2.5 How this spec relates to PAC-24

PAC-55 adds one display preference to the settings document. It **depends on
PAC-24** and does not re-specify it:

- **Per-user scoping (PAC-24 AC-1, AC-2)** makes the choice per-user and gives a
  user who never saved settings a complete default document. **PAC-55 must not
  ship to users before PAC-24's per-user scoping is live.**
- **Merge-on-write (PAC-24 AC-6, AC-8)** lets Settings save this one choice
  without touching anything else, and makes an omitted preference "unchanged"
  rather than "reset" (under today's replace, an omission reverts to 12-hour).
- **PAC-24's migration** copies a partitioned set of the shared row's values to
  every user (PAC-24 §8 OQ-1). If PAC-55's storage lands after it, everyone
  starts at the default and the question does not arise (OQ-2).
- **Audit prerequisites**: API-025 and API-039 (header) are owned by PAC-24 or
  the grouped audit issue, not by PAC-55.

## 3. Desired behaviour

A signed-in user can choose, in one place in Settings, whether times of day are
shown on a 12-hour clock or a 24-hour clock. Nobody has to choose: a user who
never touches it sees 12-hour time exactly as today.

The choice is the user's own. It is stored with their other settings on the
server, takes effect as soon as it is saved, follows them to another browser or
device, survives clearing the browser, and has no effect on what any other user
sees. Changing it changes nothing else, and saving any other setting leaves it
as it was. Changing it means choosing the other value; there is no "unset".

Once chosen, every time of day the user sees in the signed-in app uses that
clock: calendar event times and the calendar's hour axis, the agenda, event
details, suggestions and proposed slots, the host's own views of their links and
bookings, the activity log, team pages including protected hours, and text the
app composes for the user to send. On a 24-hour clock hours always have two
digits and there is no AM/PM ("09:00", "14:30", "00:15"); where a surface also
shows a date or seconds today, it keeps them and only the clock changes. On a
12-hour clock each surface is exactly as today. Dates, day and month names stay
in English whatever the choice. Time zones are unaffected.

Until the user's choice is known (still loading, or cannot be loaded), times
are shown in the default 12-hour form. The public booking page always shows
12-hour.

## 4. Acceptance criteria

AC-1. Given a signed-in user who has never chosen a time format, when they read
their settings, then the time-format preference is reported and its value is
12-hour.  [contract]

AC-2. Given a signed-in user, when they set the preference to 24-hour and then
read their settings, then it is 24-hour; and every other setting has exactly the
value it had before the change.  [contract]

AC-3. Given two signed-in users where the first sets 24-hour, when the second
reads their settings, then the second's is 12-hour; and when the second sets
24-hour and then 12-hour, the first's is 24-hour after each step.  [contract]

AC-4. Given a user whose preference is 24-hour, when they save a change to any
other setting without mentioning the preference, then the preference is still
24-hour on the next read.  [contract]

AC-5. Given any signed-in user, when they submit a time-format value other than
the two permitted ones (including an empty value or null), then the request is
refused with a client error naming the setting, and nothing is stored.
[contract]

AC-6. For each signed-in surface listed in §3, given fixture data containing the
local times 00:05, 09:00, 13:30 and 23:59: with the 24-hour preference the
surface shows "00:05", "09:00", "13:30", "23:59" with no AM/PM marker (keeping
any date or seconds it shows today); with the 12-hour preference its output is
identical to its output before this change.  [unit]

AC-7. Given the web app's source, when it is scanned, then no code outside one
shared time-of-day formatting path formats a time of day directly — so a new or
unconverted call site, including the hand-rolled protected-hours formatter,
fails the check. This is a guard on structure; behaviour is proven by AC-6,
AC-8 and AC-9.  [unit]

AC-8. Given a signed-in user who has chosen 24-hour, when they open the calendar
week view, then the hour axis reads "07:00" … "20:00" and no "am"/"pm" appears
on it.  [e2e]

AC-9. Given the calendar and the agenda rendered with a fixture event from 14:00
to 15:00 and the 24-hour preference, then the event tile and the agenda show
"14:00" and neither shows "AM" or "PM".  [unit]

AC-10. Given a signed-in user on 12-hour, when they choose 24-hour in Settings
and save, then without reloading the calendar's hour axis is in 24-hour form;
and after the browser's site storage is cleared and they sign in again, Settings
shows 24-hour and the hour axis is still in 24-hour form.  [e2e]

AC-11. Given a host who has chosen 24-hour and has a booking, when they open the
bookings list for that link in the app, then the booking time is in 24-hour
form; and when anyone opens that host's public booking page, the slots are in
12-hour form (the existing check passes unchanged). The second half is a
regression guard: it cannot fail before this feature exists.  [e2e]

## 5. Explicitly out of scope

- **The public booking page.** The booker has no account and no settings; the
  host's preference says nothing about how the booker reads time, and applying it
  would expose a host setting on an unauthenticated page. Always 12-hour
  (owner, 2026-10-11).
- **Native time-entry controls.** Their AM/PM display is set by the browser and
  OS, not the app. Accepted by the owner for now (2026-10-11); replacing them is
  a follow-up, to be filed.
- **Anything formatted outside the web app**: the Slack morning summary and the
  booking confirmation email already use 24-hour times composed by the server;
  calendar entries written to external calendars; the assistant (MCP) tools.
- **The offline fallback.** When the server is unreachable the app's existing
  in-memory fallback applies to this choice as to every setting; it is not
  persisted there. Unchanged by this work.
- **Detecting the preference** from the browser or locale, per-screen overrides,
  date formats, first day of week, and any change to time-zone handling — all
  excluded by the owner's simplicity decision.
- **Everything PAC-24 owns** (per-user records, merge semantics, the provider
  key, the migration of the shared row, its eight claimed findings), and the
  audit findings API-025 and API-039 (header).

## 6. What must not change

- **A user who never chooses sees exactly today's output** on every surface,
  including the existing mix of "02:30 PM" and "2:30 PM" forms. No test pins this
  today except the public booking e2e (`e2e/tests/public-booking.spec.ts:298`);
  AC-6 adds a per-surface guard.
- **English dates and names.** `src/lib/locale.test.ts` must keep passing: no
  surface may fall back to the browser's locale to get 24-hour time, since that
  re-introduces the mixed-language dates #86 removed.
- **The public booking page** (AC-11 and the existing e2e).
- **Every other setting.** No test protects the round trip today (PAC-24 §2.11);
  PAC-24 AC-6 and AC-2/AC-4 here are the guards.

## 7. Rollback

The server adds one stored value defaulting to 12-hour; its down migration drops
it and **loses every user's choice** — acceptable for a display preference;
users return to today's behaviour. Reverting PAC-24 is not required.

**Order matters: revert the web before, or together with, the server.** The
write body the server accepts refuses unknown properties
(`contracts/openapi/paths/scheduling.yaml:1984-1995`), so a reverted server
answers 400 to any settings save from the new web that carries the preference.
Reads are unaffected (an absent preference is treated as 12-hour, §3).
Reverting the web alone is safe: the server keeps the value and nothing reads it.

## 8. Open questions

- **OQ-1 — Public booking pages.** *Closed 2026-10-11 (product owner):* always
  12-hour; the host's in-app views follow the preference (§1, §5, AC-11).
- **OQ-2 — Sequencing against PAC-24.** Owner: plan-author with the PAC-24 owner.
  This spec assumes PAC-55's storage lands after PAC-24's per-user migration. If
  it must land earlier, PAC-24's OQ-1 partition must say whether the shared
  row's value is copied, and PAC-55 would ship a global setting in the interim,
  contradicting the decision. Resolve by fixing the order in the PAC-55 plan.
- **OQ-3 — Native time pickers.** *Closed 2026-10-11 (product owner):*
  acceptable for now; out of scope, follow-up to be filed (owner of filing:
  plan-author).
- **OQ-4 — Who takes API-025 and API-039?** Owner: PAC-24 contract owner (for
  API-039) and the owner of the grouped validation issue (for API-025). The
  owner decided they are not PAC-55's; until one of them claims each finding in
  a merged contract, PAC-55's contract stage cannot start without breaking
  touch-it-fix-it. Resolve by PAC-24 adding API-039 to its claimed findings and
  the grouped issue confirming API-025.
- **OQ-5 — 24-hour form.** *Closed 2026-10-11 (product owner):* "09:00", leading
  zero.

---

## Challenge — 2026-10-11

*spec-challenger. I did not write this spec and did not ask the author for their
reasoning. I re-checked every §2 citation against source in both repos
(`clockwise-like` on `spec/pac-55-time-format`, `smart-calendar-flow` on `main`
at a3a0f76). Nothing was compiled or run. Not posted to Linear (tools
unavailable in this session); this section is the record.*

**Verdict**: accept-with-changes

The web half of §2 is accurate: every time-formatting call site in §2.1 exists at
the cited line with the cited options, I found no time-of-day call site it
missed, and the hand-rolled protected-hours formatter, the unconfigured calendar
hour axis, the native time inputs and the server-composed 24-hour Slack/email
times are all as described. The problem framing matches the issue. The changes
below concern what the ACs can actually prove, the public booking scope, the
inherited-findings list, and a rollback claim the contract contradicts.

### Blocking

1. **AC-8 cannot be run as an e2e test.** The calendar's events come only from
   Google; the e2e stack cannot seed them (`e2e/tests/calendar.spec.ts:1-18`,
   `e2e/SEAM-REQUIRED.md` item A). "An event from 14:00 to 15:00 today" cannot be
   created, so the event-tile and agenda parts of AC-8 can never fail or pass at
   the `[e2e]` level. Only the hour axis renders without events (the grid still
   renders for a not-connected user, `src/pages/Dashboard.tsx:442-471`).
   *Resolves it*: split AC-8 so the hour-axis assertion stays `[e2e]` and the
   event-tile and agenda assertions sit at a level the repos can actually
   exercise with fixture events, or declare SEAM item A a prerequisite of this
   spec. AC-9's "the calendar shows 24-hour times" needs the same clarification
   (axis only).

2. **The public booking page contradicts the issue's stated scope, and AC-11 locks
   in an answer to an open question owned by the product owner.** The issue lists
   "booking pages the user hosts" among the surfaces the preference applies to,
   and separately asks host's-or-booker's as an open question. §1 restates the
   decision without that phrase, §5 excludes public booking pages, and AC-11
   asserts the 12-hour outcome as an acceptance criterion. The §5 reasoning
   (booker has no settings; exposing a host setting unauthenticated) is a
   reasonable argument, but "keep it simple" does not decide it — the host's
   preference is equally a single setting with no per-surface override — so it is
   the author's choice, not the owner's. The spec also does not say whether it
   reads "booking pages the user hosts" as the public page or as the host's own
   view of their links and bookings (Links, `LinkBookingsDialog`), which it does
   cover. This must be settled before stage 2 because it decides whether the
   public link response carries a host field.
   *Resolves it*: quote the issue's phrase in §1, state which reading the spec
   takes, and get OQ-1 answered by the owner before the contract starts. If the
   answer is "default", AC-11 stays; if "host's", §5 and AC-11 change and the
   existing slot check at `e2e/tests/public-booking.spec.ts:298` becomes a
   consumer that breaks.

3. **The inherited scope is understated in one place and over-stated in another.**
   - API-025 (`docs/factory/api-audit.md:259`) is an open finding on
     `GET /api/settings` (capitalised `workingHours.days` keys pass validation
     and are then never matched). It is not in PAC-24's `resolvesFindings`
     (`contracts/features/PAC-24.yaml:117-119`) and not mentioned here. Under
     touch-it-fix-it it is owed by whichever feature next modifies the settings
     operations.
   - AC-10 (API-039, a conferencing/LLM enum mismatch) has nothing to do with
     time format. Pulling it in makes a "keep it simple" display preference also
     carry an unrelated Settings bug. PAC-24 modifies the same operations first,
     and API-039 sits in audit batch D1 with API-013, which PAC-24 already claims.
   *Resolves it*: make "API-025 and API-039 are resolved before PAC-55's contract
   stage" a stated prerequisite alongside per-user scoping — by PAC-24 adding
   them to its claimed findings, or by the grouped validation issue that already
   lists API-025 (`docs/factory/api-audit-issues.yaml:326`) — and drop AC-10. Only
   if neither owner takes them does PAC-55 carry both, and then API-025 needs its
   own AC too. (OQ-4 asks me: my answer is that they belong with PAC-24.)

4. **§7's rollback claim is false against the contract PAC-55 builds on.**
   "Reverting the server under the new web must not break Settings: an absent
   preference is treated as 12-hour" covers reads only. `SettingsUpdate` inherits
   `additionalProperties: false` (`contracts/openapi/paths/scheduling.yaml:1984-1995`),
   so a reverted server rejects with 400 any settings write that carries the
   preference — depending on what the web sends, every Settings save or every
   save that changes the preference.
   *Resolves it*: state the rollback order (web reverted before, or with, the
   server), or state as a requirement which writes may carry the preference, and
   correct §7 accordingly.

### Non-blocking

1. AC-3 says the second user "sets and clears their own", but AC-5 refuses empty
   and null, so "clear" has no defined request. Say "sets 24-hour and then
   12-hour" (or define clear).
2. AC-7 asks a source inspection to decide whether each site "takes its clock form
   from the user's preference". That is not mechanically decidable without
   presupposing one formatting path, and the hour axis is library configuration a
   source scan cannot see. Either accept AC-7 explicitly as a guard that bans
   direct time-of-day formatting outside one sanctioned path, or restate it as
   observable behaviour per surface.
3. AC-6 tests "the app" as if it had one formatter, but "exactly as today" differs
   per surface ("02:30 PM" vs "2:30 PM", §2.1). The 12-hour half is only
   falsifiable per surface; say so.
4. AC-11 cannot fail today (no user can hold 24-hour yet). It is a regression
   guard, which is fine; label it as one.
5. No AC covers the choice taking effect in the same session after saving
   (AC-9 only covers after re-sign-in), nor what happens when the server is
   unreachable and the client falls back to in-memory mock settings
   (`src/api/client.ts:463-476`): a "saved" 24-hour choice would then show
   24-hour until reload and never persist.
6. The composed reschedule message (`src/components/EventOwnership.tsx:54`) uses
   the full date-time form with seconds; AC-6 only defines hour:minute output, so
   the 24-hour form of composed text is unspecified.
7. OQ-3 (native pickers) is correctly routed to the owner; the issue's wording is
   "every time the app shows", so the exclusion needs the owner's explicit
   acceptance, not silence.

### Criteria I could not falsify

- AC-8: event tile and agenda — no e2e test can create the event (Blocking 1).
- AC-3: the "clears" step has no defined request (Non-blocking 1).
- AC-7: partially — "takes its clock form from the preference" is not decidable
  by inspection as written (Non-blocking 2).
- AC-11: cannot fail before the feature exists; a guard, not a criterion.

### Citations I checked and found wrong

- "`settings.user_id` exists … but no query uses it" (§2.2) — actually
  `GetSettingsByUser` (`backend/storage/settings.go:418-442`),
  `ListUsersWithAutoSchedule` (`:379-380`) and `ListUsersWithAutoDecline`
  (`:401-402`) query by it, and the engine calls `GetSettingsByUser`
  (`backend/engine/focus_time.go:76`, `auto_decline.go:66`, `habits.go:70,96`).
  What is true is that no `/api/settings` handler uses it.
- "migration 018 attributed the row to the earliest user" (§2.2) — only if a user
  existed when it ran; the backfill is guarded by `AND EXISTS (SELECT 1 FROM users)`
  (`backend/storage/migrations/018_per_user_settings_and_calendars.up.sql:15-17`).
- "the six audit findings it [PAC-24] claims" (header, §5) — PAC-24 claims eight:
  API-002, -003, -004, -009, -013, -037, -081, -082
  (`contracts/features/PAC-24.yaml:117-119`).
- Inherited scope lists the open findings on the settings operations without
  API-025 (`docs/factory/api-audit.md:259`).
- "Reverting the server under the new web must not break Settings" (§7) —
  `SettingsUpdate` refuses unknown properties
  (`contracts/openapi/paths/scheduling.yaml:1984-1995`).

Checked and correct: `locale.ts:8`; `locale.test.ts:15-29`; all §2.1 formatting
call sites and their options; `teams.ts:818-823` and its five uses; Dashboard
`:471-484` and `:496-500`; every native time input cited; the e2e regex at
`public-booking.spec.ts:298` and the absence of any web unit test asserting a
formatted time; `storage/settings.go:227,262,269`; `handlers_settings.go:31-64`;
`routes.go:72-76` inside the `requireAuth` group; `api-audit.md:236,238`; the web
adapter and save path; `scheduling.yaml:532-641` and the four `x-uncertain`
markers being on other operations; generated-type lines in both repos; PAC-24
spec `draft` with four blocking findings and contract revision 2 `draft`;
server-side 24-hour Slack recap and booking email (`engine/daily_recap.go:349-375`,
`engine/booking.go:312`).

### Consumers this breaks

- clockwise-like/e2e/tests/public-booking.spec.ts:298 — the `h:mm AM|PM` slot
  regex fails if OQ-1 resolves to the host's preference.
- smart-calendar-flow/src/api/client.ts:872-880 (settings save, or its PAC-24
  successor) — 400s against a rolled-back server if it sends the preference
  (Blocking 4).
- clockwise-like/mcp/tools.go:236 (`clockwise_get_settings`) — passes the
  document through; an added property is additive, no break. Listed so it is not
  re-checked.

### Resolution — 2026-10-11 (spec-author)

Product-owner answers of 2026-10-11 applied (OQ-1, OQ-3, OQ-5 closed; AC-10 dropped).

- **Blocking 1 (AC-8 not runnable in e2e)** — split. AC-8 is now hour axis only
  `[e2e]` (renders without events); event tile and agenda moved to AC-9 `[unit]`
  with a fixture event; the persistence criterion (now AC-10) asserts the axis
  only. §2.1 records the missing seam.
- **Blocking 2 (public booking scope)** — §1 quotes "booking pages the user
  hosts" and reads it as the host's in-app views (which follow the preference);
  the public page is always 12-hour by owner decision. OQ-1 closed; AC-11 now
  covers both halves and labels the public half a regression guard.
- **Blocking 3 (inherited scope)** — API-025 added; API-025 and API-039 are
  stated as prerequisites owned by the grouped audit issue / PAC-24, not PAC-55
  (owner decision); AC-10 (API-039) dropped. OQ-4 now asks those owners to claim
  them.
- **Blocking 4 (§7 rollback)** — §7 states the order: web reverted before or with
  the server, because the write body refuses unknown properties.
- **Wrong citations** — §2.2 corrected (`user_id` is queried by storage/engine,
  just not by `/api/settings`; 018's backfill only ran if a user existed); PAC-24
  claims eight findings, listed in the header and §5.
- **Non-blocking** — 1: AC-3 now "sets 24-hour and then 12-hour"; §3 says there is
  no unset. 2: AC-7 restated as a structural guard with behaviour proven by
  AC-6/8/9. 3: AC-6 is per surface. 4: AC-11's public half labelled a guard.
  5: same-session effect added to AC-10; offline fallback recorded in §5. 6: §3
  and AC-6 say composed text keeps its date/seconds and only the clock changes.
  7: native pickers accepted by the owner (§5).
