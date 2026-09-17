# TESTIDS-REQUIRED — frontend hooks the e2e suite needs

**Status**: factory work item for the `web` repo (`Enach/smart-calendar-flow`).
**Raised by**: `e2e-author`. **No frontend file was edited to produce this.**

**Baseline**: `smart-calendar-flow/src` currently contains **zero**
`data-testid` attributes in production code. The only occurrence in the whole
tree is inside `src/components/team/ContactEmailAutocomplete.test.tsx`. Every
selector in `e2e/` is therefore role- or accessible-name-based today, which
works but is brittle in the five places listed below.

**Convention requested**: `data-testid="<area>-<thing>"`, kebab-case, added to
the element that is *semantically* the thing (the button, the row, the input) —
never to a wrapping `<div>` added just to hold the attribute. Test ids are
additive; no existing markup, class or aria attribute should change.

---

## 1. Public booking calendar and slots — `src/pages/PublicBooking.tsx`

Highest priority: this is the suite's most valuable journey.

| Test id | Element | Why |
|---|---|---|
| `booking-day-<YYYY-MM-DD>` | the `<button>` for each day inside the `Calendar` | `e2e/lib/ui.ts pickBookingDate()` currently OR's two selectors — an accessible-name regex built from react-day-picker's default `labelDayButton` format, and the button's bare day-number text — because that format has changed between the library's majors and the app is on v10. A date-stamped id removes the guessing. Pass it through `Calendar`'s `components.DayButton` or via `modifiersClassNames`-adjacent props. |
| `booking-slot-<HH:MM>` | each slot `<button>` | `slotButtons()` matches "any button whose entire label is a clock time". That works, but it depends on U+202F vs U+0020 before AM/PM (modern ICU emits the former) and would break if a "45 min" style chip ever rendered a colon. |
| `booking-slot-list` | the slots container | To assert "no times available" distinctly from "the slots have not loaded yet". |
| `booking-confirmation` | the confirmation card | So the success assertion does not depend on the copy "You are confirmed!". |
| `booking-error` | the `submitError` paragraph | Same, for the 409/410/422 branches. |

## 2. Links page create form — `src/pages/Links.tsx`

Blocking one `test.fixme` in `e2e/tests/scheduling-links.spec.ts`
("a link created through the Links page appears on the public booking page").
The create form's inputs are not associated with `<label for>` and the duration
and day toggles are class-styled `<button>`s with no accessible name beyond
their text, so there is no role-based way to address them unambiguously.

| Test id | Element |
|---|---|
| `link-create-open` | the button that opens the create form/dialog |
| `link-title-input` | the title field |
| `link-duration-<minutes>` | each duration toggle |
| `link-window-start` / `link-window-end` | the window time inputs |
| `link-usage-type-<reusable\|single_use\|recurring>` | the usage-type toggles |
| `link-create-submit` | the submit button |
| `link-row-<slug>` | each row in the list |
| `link-public-url` | the element holding the copyable public URL |

An acceptable alternative to all of the above: give every input a real
`<label htmlFor>` and every toggle an `aria-pressed` plus an `aria-label`. That
is better for users as well, and the suite would then need no ids here.

## 3. Manager roster rows — `src/pages/Team.tsx`

| Test id | Element | Why |
|---|---|---|
| `manager-member-row-<email>` | each `MemberRow` `<li>` | The scoping test asserts on the member's display name, which is fine, but it cannot currently scope a *per-member* assertion (this member's focus minutes) without matching loose text. |
| `manager-member-focus-<email>` | the focus-minutes figure in the row | To assert the exact seeded value (615) end to end, as the API test already does. |
| `team-switcher` | the team popover trigger | `e2e/lib/ui.ts switchTeam()` finds it by filtering buttons whose text is a team name or "No team selected" — it cannot match on the target name, because the trigger shows the *current* team. |
| `team-option-<teamId>` | each team button inside the popover | Same. |

## 4. Dashboard calendar — `src/pages/Dashboard.tsx`

Not blocking anything today (the calendar journey is blocked on the backend
seam, see `SEAM-REQUIRED.md`), but will be needed the moment that lands.

| Test id | Element |
|---|---|
| `calendar-event-<id>` | each FullCalendar event, via `eventContent` |
| `calendar-empty` | the "nothing this week" state |
| `focus-run-button` | whatever triggers `POST /api/focus/run` in `QuickActions` |
| `focus-block-<date>-<HH:MM>` | a rendered focus block, distinguishable from a meeting |

## 5. Demo / mock state markers — `src/components/MockBanner.tsx`, `DemoBanner.tsx`

| Test id | Element | Why |
|---|---|---|
| `mock-banner` | the "Backend not reachable — showing demo data" banner | `e2e/fixtures.ts assertNotDemo` asserts this banner is **absent** on every authenticated page, because `src/api/client.ts` silently substitutes demo fixtures on `ApiUnreachableError`. That guard is the single most important anti-false-pass check in the suite and it currently depends on an exact English sentence. A stable id makes it robust; changing the copy today would silently disarm it. |
| `demo-banner` | the demo-session banner | Same reasoning. |

---

## Contract gaps found while writing this suite

Not test ids — genuine `api`/`web` drift, listed here because they materially
shaped the harness. Each deserves its own Linear issue.

1. **`onboarding_profile_selected` does not exist on the backend.**
   `src/components/RequireAuth.tsx` redirects to `/app/onboarding` unless
   `managerApi.remote.getProfile()` returns it true, and `src/api/manager.ts`
   synthesises it as `Boolean(raw.detected_at) || <localStorage>`. But
   `GET /api/manager/profile` (`backend/api/handlers_manager.go getProfile`)
   returns only `is_manager`, `detected_at` and `team_member_count`. So whether
   a user has finished onboarding is decided by a browser-local value plus a
   field that means something else. `e2e/seed/seed.sql` works around it by
   setting `user_profiles.detected_at`, which is not what that column means.

2. **`src/pages/Team.tsx` reads `is_manager` synchronously from localStorage**
   (`managerApi.getProfile()`), never from `GET /api/manager/profile`. A user
   who enables manager mode on one device sees "Manager mode is off" on
   another, and the server's answer is ignored. `e2e/auth.ts
   onboardedLocalStorage()` reproduces the local value so the roster journey is
   reachable, and says so in a comment.

3. **`storage.ListFocusBlocksForWeek` and `storage.FocusMinutesForDay` take no
   user id.** Focus blocks are global: one user's blocks are busy time for every
   other user's booking availability (`engine/booking.go hostBusy`). The seed
   has to delete all focus blocks to keep the booking slot counts deterministic,
   which is how this was noticed.

4. **`PublicBooking.tsx` marks the booker name and email `required`**, so the
   browser's native validation pre-empts the component's own
   "Please enter your name and email" message. That message is unreachable, and
   its branch is dead code.
