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


## 1a. PAC-88 booking page time zone — `src/pages/PublicBooking.tsx`, `src/components/booking/**`

Contract `contracts/features/PAC-88.md` section 3.5 (exact spellings), used by
`tests/pac88-zones.spec.ts` and `tests/pac88-booking.spec.ts`. Implemented by
the web PR smart-calendar-flow#144 (the journeys need it).

| Test id | Element | Used for |
|---|---|---|
| `booking-tz-label` | zone label, text `<zone> (UTC±hh:mm)` (U+2212 minus); the selected day's offset, or the selected time's | AC-1, AC-2, AC-3, AC-21 |
| `booking-tz-switcher` | `<input role="combobox">` named "Time zone"; value = the current zone; typing filters; options `role="option"` named `<zone> (UTC±hh:mm)` | every zone switch (`lib/zones.ts switchZone`) |
| `booking-tz-use-mine` | "Use my time zone (<browser zone>)", absent when equal | AC-7 |
| `booking-tz-back-to-host` | "Back to the host's time zone (<zone>)", shown after a switch | AC-7 |
| `booking-tz-unsupported` | "Times can't be shown in <zone>. Choose another time zone." | not driven by e2e (a zone the server refuses needs a browser zone the server lacks) |
| `booking-tz-announcer` | `role="status"`, polite, empty on load | AC-7 (after a switch), AC-1 (nothing on load) |

Also relied on (not test ids): each time button's accessible name is
`<Weekday, Month D, YYYY>, <time>, <zone> <offset>` (spec 3.9; asserted with
`toHaveAccessibleName`), its visible text stays the time alone (`slotButtons`);
day cells not listed by the overview are disabled; the month caption reads
`<Month> <YYYY>` (`lib/zones.ts showDay`); the confirmation shows the date,
`start – end · N min` and `<zone> (UTC±hh:mm)`; "Book another time" is a
button with that name.

## 2. Links page create form — `src/pages/Links.tsx`

> **Resolved (2026-10-11) by the "acceptable alternative" below.** Since
> smart-calendar-flow#91 every input has a real `<label htmlFor>` and every toggle
> `aria-pressed`; the test in `tests/scheduling-links.spec.ts` now runs against
> roles and labels, with no test ids. Kept for the record.

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

Needed now (PAC-44): the backend seam landed in PAC-43, and the calendar
journeys run. Until these ids exist, `tests/calendar.spec.ts` (AC-16),
`tests/focus-time.spec.ts` (AC-18) and `tests/seam.spec.ts` (AC-11) find
events by their **title text** — user-visible content, but FullCalendar gives
an event no role — and AC-18 counts focus blocks with
`/^\W*Focus Time$/` because the dashboard prefixes the title with an icon
("🎯 "). A change to that decoration, or a second element repeating a title,
breaks those selectors without breaking the feature.

| Test id | Element |
|---|---|
| `calendar-event-<id>` | each FullCalendar event, via `eventContent` |
| `calendar-empty` | the "nothing this week" state |
| `focus-run-button` | whatever triggers `POST /api/focus/run` in `QuickActions` |
| `focus-block-<date>-<HH:MM>` | a rendered focus block, distinguishable from a meeting |

## 4b. AI features off — `src/components/AINotConfiguredNotice.tsx` (NEW-llm-bring-your-own)

| Test id | Element | Why |
|---|---|---|
| `ai-off-notice` | the "AI features are off" notice shown by the dashboard's scheduling bar and the AI section of Settings | `tests/llm-bring-your-own.spec.ts` (spec AC-21, AC-34) asserts the notice, not an error, appears for a user with no AI configuration. Provided by smart-calendar-flow in the NEW-llm-bring-your-own PR. |

## 4c. Organisation AI — `src/components/settings/OrganizationAISection.tsx`, `src/components/OrgAIConsentDialog.tsx`, `src/components/AINotConfiguredNotice.tsx` (PAC-73 Part 2, PAC-87)

| Test id | Element | Why |
|---|---|---|
| `org-ai-section` | the organisation AI section of Settings (members only) | `tests/llm-org-ai.spec.ts` and `tests/llm-bring-your-own.spec.ts` (P2-AC-30, P2-AC-31) assert what every member is shown, and that the key never is. |
| `org-ai-read` | the read view of that section | Same. |
| `org-ai-form` | the administrator's change form | P2-AC-32: a granted return offers the form. |
| `org-ai-stepup` | the step-up explanation shown before navigating | P2-AC-30. |
| `org-ai-stepup-outcome` | the step-up outcome stated in the organisation AI section | P2-AC-32: an organisation-AI landing is stated there and not in the SSO section. |
| `org-ai-forbid-warning` | the warning before forbidding personal keys on an incomplete configuration | P2-AC-30. |
| `org-ai-consent` / `org-ai-consent-text` / `org-ai-consent-changed` | the one-time notice dialog, its text, and its "changed meanwhile" note | P2-AC-22, P2-AC-26, P2-AC-38 (owner O-13 wording: "access", never read/reads/see). |
| `ai-org-confirm` | the notice's "Review and confirm" action in rows 5 and 10 (PAC-87: row 10 confirms without changing the stored choice) | P2-AC-22; `tests/llm-org-ai.spec.ts` PAC87-AC-16. |
| `ai-use-org` | the notice's "Use my organisation's AI" action in row 7 only (PAC-87: never offered from row 10) | P2-AC-31; PAC87-AC-16 asserts it is absent from row 10. |
| `ai-personal-keys-forbidden` | the Settings note that a member's own choice and key are kept but not used | P2-AC-31; PAC87-AC-16 (rows 9 and 10). |
| `ai-runs-on-org` | the Settings line for a member in row 9: "AI runs on your organisation's AI (‹provider›)" | `tests/llm-org-ai.spec.ts` PAC87-AC-15, PAC87-AC-16. Provided by smart-calendar-flow PAC-87 (impl/pac-87-web, #143). |
| `org-ai-consent-kept` | the consent dialog's sentence that the member's own choice and key are kept (shown when the stored choice is not `organization`) | PAC87-AC-15, PAC87-AC-16. Provided by smart-calendar-flow #143. |
| `org-ai-presave` / `org-ai-presave-<kind>` | the administrator form's pre-save block and each sentence (`provider_changed`, `provider_removed`, `key_or_destination`, `forbid_complete`) | PAC87-AC-17 (unit-tested in the web; available to e2e). Provided by smart-calendar-flow #143. |

## 5. Demo, unreachable and failure markers (PAC-83, contract `contracts/features/PAC-83.md` §5.1)

Binding test ids the web renders since PAC-83. `e2e/fixtures.ts assertNotDemo`
— the single most important anti-false-pass check in the suite — fails on
`demo-label`, `server-unreachable-banner` and `api-error` with
`data-error-kind="lost"` (AC-28, self-tested in `tests/guard-self-test.spec.ts`).

| Test id | Element | Used by |
|---|---|---|
| `demo-entry` | the demo entry control, sign-in initial view, demo builds only | `tests/demo-build.spec.ts` (AC-30: absent from the production image) |
| `demo-label` | the non-dismissible demo label on every signed-in page during a demo; demo builds only | `assertNotDemo` |
| `server-unreachable-banner` | the persistent "can't reach Paceday" banner (Dashboard, Settings, Audit) when the health probe fails | `assertNotDemo` |
| `server-unreachable-retry` | its retry button | — |
| `api-error` | an inline failure message where the action happened; attributes `data-error-kind` (`budget`, `too-large`, `lost`, `http`) and `data-effect` (`read`, `write`, `booking`) | `assertNotDemo` (`lost`); `tests/public-booking.spec.ts` AC-10 |
| `api-error-retry` | the manual retry control inside `api-error`, only when a retry is offered | AC-10 |
| `still-working` | the "still working, may take up to about a minute and a half" notice, on a user-started call after 8 s | AC-5, AC-24 |
| `ai-progress` | the "asking the AI, may take up to a minute" message, from submit until answer | — (unit AC-2) |

Retired: `mock-banner` (never rendered; its banner is gone) and `demo-banner`
(replaced by `demo-label`). The old banner sentence stays in `assertNotDemo`
only while a web from before PAC-83 can still be deployed.

## 6. Framing — `src/pages/PublicBooking.tsx`, `src/components/FrameGuard.tsx` (PAC-91, contract `contracts/features/PAC-91.md` §4)

Binding test ids for the PAC-91 web change (links on the booking page leave
the frame; the app refuses to render framed off the booking page). Rendered
by the PAC-91 web change (Enach/smart-calendar-flow, branch `impl/pac-91-web`).

| Test id | Element | Used by |
|---|---|---|
| `booking-home-link` | the booking page's header link ("paceday"), `href="/"`, `target="_top"` | `tests/framing.spec.ts` AC-4, AC-5; web unit AC-1, AC-3 |
| `booking-error-home-link` | the error-state "Back to Paceday" link (both error states) | web unit AC-2, AC-3 |
| `frame-notice` | the root of the notice the app shows when framed off the booking page | web unit AC-6, AC-7, AC-8, AC-10, AC-11 |
| `frame-notice-open` | the notice's single link, `href="/"`, same target as the booking links | web unit AC-7 |

---

## 7. Sign-in provider choice — `src/components/auth/AuthDialog.tsx`, `src/components/auth/**` (PAC-97)

Detection's new answer `choose` (spec `docs/specs/PAC-97.md` AC-19..AC-22,
contract `contracts/features/PAC-97.md` section 3). Implemented by the web PR
smart-calendar-flow#147; used by `tests/pac97-signin-detect.spec.ts` (AC-27).

| Test id | Element | Used for |
|---|---|---|
| `auth-choose-step` | the choose step's container; heading "Choose how to sign in" (or "...sign up") | AC-27: the step is shown, not the password step |
| `auth-choose-email` | the span showing the typed email | AC-27: the step is for the typed address |
| `auth-choose-google` | button "Continue with Google" / "Sign up with Google" | AC-27: the click requests `/api/auth/google?login_hint=<email>` |
| `auth-choose-microsoft` | button "Continue with Microsoft" / "Sign up with Microsoft" | AC-27: the Microsoft half (shown only when Microsoft is configured) |
| `auth-choose-back` | button "Use a different email" | AC-21 (e2e: the email is kept) |
| `auth-choose-unavailable` | the "sign-in with a provider is not available" text | AC-20 (vitest only) |

The remembered email lives in `sessionStorage` key `paceday:signin-email`
(AC-25, AC-26).

## 8. Deleted links and the owner archive — `src/pages/Links.tsx`, `src/pages/LinksArchive.tsx` (PAC-40 part A)

Contract `contracts/features/PAC-40.md` §2.4 and §6. Implemented by the web PR
smart-calendar-flow#150 (`impl/pac-40-web`); used by
`tests/pac40-lifecycle.spec.ts` (AC-10, AC-25 part A).

| Test id | Element | Used for |
|---|---|---|
| `link-row-<slug>` | each link card on the Links page (its Delete button keeps the accessible name `Delete <title>`) | AC-10, AC-25 |
| `links-archive-link` | the link from the Links page to `/app/links/archive` | AC-25 |
| `link-delete-dialog` | the delete confirmation dialog | AC-10, AC-25 |
| `link-delete-copy` | its copy: people can no longer book; the link and its bookings move to the archive | AC-25 |
| `link-delete-erasure` | the erasure sentence; present only when `booker_erasure.enabled` is true, so absent in part A | AC-25 (asserted absent) |
| `link-delete-confirm` | the confirm button, text "Delete link" | AC-10, AC-25 |
| `link-delete-cancel` | the cancel button | not driven |
| `archive-page` | the archive page | AC-25 |
| `archive-list` / `archive-empty` / `archive-unavailable` | the list, its empty state, and "archive unavailable" (any non-200 or a body failing the schema, K4-7) | not driven in part A |
| `archive-row-<slug>` | one archived link; `data-erasure-state` = `no_bookings` / `awaiting_erasure` / `erased` | AC-25 |
| `archive-erasure` | the erasure date; absent while `enabled` is false | AC-25 (asserted absent) |
| `archive-view-bookings` | the button in a row that opens its detail | AC-25 |
| `archive-detail` | the detail dialog | AC-25 |
| `archive-bookings` / `archive-bookings-empty` / `archive-detail-not-found` / `archive-detail-unavailable` | the bookings list and its other states | not driven in part A |
| `archive-booking-<id>` | one booking; `data-erased` = `true` / `false` | AC-25 |
| `archive-booker-name`, `archive-booker-email` | the booker's name and email (not erased) | AC-25 |
| `archive-booker-erased` | the explicit erased state (part B) | AC-25 (asserted absent) |
| `archive-booking-status` | the booking's status | AC-25 |

Also relied on (not test ids): the archive offers no button or switch named
edit, resume, restore, delete, pause or invite; the public page of a deleted
link shows the "Link not found" heading. Every archive timestamp is UTC with a
`Z` suffix (the web validates with `z.iso.datetime()`); the backend serialises
them that way (`archivedLinkDTO`, `archivedBookingDTO`).

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
