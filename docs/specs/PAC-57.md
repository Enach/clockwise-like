# Spec — PAC-57: the dashboard should show what Paceday did, not ask the user to run a "focus engine"

- **Linear**: https://linear.app/paceday/issue/PAC-57
- **Status**: draft
- **Author**: spec-author
- **Inherited scope**: none. This spec is a front-end reorganisation of the home
  screen. It changes no HTTP operation (request or response), so factory §7 rule 1
  does not bind it. Findings that sit on operations the screen *reads* and that
  block end-to-end proof of some criteria are listed, not inherited: API-014
  (high, Confirmed) and API-015 (high, Reported) on the focus-blocks list and
  focus-run operations, API-079 (low) on the same list, and API-005 (critical,
  claimed by PAC-23) on the audit log. See §8 OQ-3 and OQ-4. A new, unregistered
  defect found while writing this (focus blocks are not scoped to a user) is
  described in §2.4 and §8 OQ-3.
- Written from reading source in both repos; nothing was run.

## 0. Proposed split

The issue asks three questions. Two have a clearly simpler answer that can be
built today with no contract change; one needs a product decision and backend
work. Writing them as one spec would make it unbuildable, so:

- **PAC-57 (this spec) — "one way, one place".** Remove the "Quick actions" panel,
  keep one date navigator, make the language bar the one primary way to schedule,
  move the focus actions into the focus card with an in-app confirmation, and
  leave the audit log in Settings only. Web repo only.
- **Proposed follow-up (not specified here) — "Paceday protects focus by itself".**
  Focus protection on by default, the dashboard showing what the last automatic
  run did, and a true undo of that run. Needs the owner decision in OQ-1 and
  backend work (per-user focus blocks, OQ-3; the schedule-reload gap PAC-49 already
  records; an undo-of-run capability that no operation offers today).

## 1. Problem

Paceday's promise is "the simplest way to own your day": it protects focus time
for the user. The home screen instead hands the user the machinery. It offers four
overlapping ways to schedule a meeting and two date navigators, and a side panel
called "Quick actions" holds "Run Focus Engine" (writes focus blocks straight into
the user's real calendar, no preview, no undo), "Clear Focus Blocks" (deletes them
after a bare browser dialog) and "Audit Log". Focus protection reads as a chore the
user must remember to run. A design critique scored the screen 19/40 with 5 of 8
cognitive-load checks failing. Both audiences pay: an individual contributor has to
learn which of four paths to use, and a manager sent here from Find a time lands on
the same crowded screen.

## 2. Current behaviour

All web paths are under `smart-calendar-flow/src/`; all backend paths under
`clockwise-like/backend/`.

### 2.1 Four ways to schedule

1. **Language bar** at the top (`pages/Dashboard.tsx:343-348`; placeholder
   "Schedule a meeting… e.g. '30 min with alice@co.com this week'",
   `components/NLPBar.tsx:28`). A meeting request opens a slot-confirmation modal
   (`pages/Dashboard.tsx:168-173,600-607`). A focus request runs the focus engine
   immediately with no confirmation (`:174-180`).
2. **"Schedule Meeting"** button in Quick actions (`components/QuickActions.tsx:65-75`)
   opens a structured form — title, duration, attendees, date range — that finds and
   books slots without the language model (`pages/Dashboard.tsx:595,628-634`;
   `components/ScheduleSuggestModal.tsx:41-60`).
3. **Drag-select on the grid** opens a quick-create popover (`pages/Dashboard.tsx:546-555,617-627`)
   that creates an event directly at the selected time (`:209-231`).
4. The popover's **"More options"** (`components/QuickCreatePopover.tsx:247-253`)
   turns the selection into a sentence and puts it in the language bar
   (`pages/Dashboard.tsx:233-247`).

The language bar depends on a language-model provider: with none usable it returns
"unknown" with the error (`nlp/parser.go:172-181`); the stored default provider is a
local Ollama (`storage/migrations/002_llm_providers.up.sql:13-14`). The structured
form has no such dependency.

### 2.2 Two date navigators

The top bar renders previous / Today / label / next whenever it is given a week
label (`components/Navbar.tsx:75-101`); only the dashboard does so
(`pages/Dashboard.tsx:334-340`). The calendar card has its own previous / next /
Today / title (`pages/Dashboard.tsx:356-393`). Both drive the same calendar
(`:320-322`).

### 2.3 Quick actions

`components/QuickActions.tsx`, rendered in the sidebar below the focus card
(`pages/Dashboard.tsx:588-595`):

- "Run Focus Engine" (`:29-40,76-89`) runs the engine for the visible week. The
  engine writes up to five days of focus blocks into the user's calendar
  (`engine/focus_time.go:70-108`). There is no preview and no undo. The success toast
  reads a snake_case result field (`QuickActions.tsx:33`, type at
  `api/types.ts:171-176`) that the server sends in camelCase
  (`engine/focus_time.go:24-27`, API-015), so against the real backend the success
  path throws and shows an error toast.
- "Clear Focus Blocks" (`:42-54,90-103`) asks with the browser's `confirm()` (`:43`),
  then deletes every focus block of the week from the calendar and the database
  (`engine/focus_time_cleaner.go:12-44`). Any failure shows "Failed to clear focus
  blocks" with no detail (`:49-50`).
- "Audit Log" (`:104-153`) expands the last 10 entries inline with a link to the full
  page. Settings already links to the same page from an "Activity" section
  (`pages/Settings.tsx:251-259`; route `App.tsx:93-99`).
- Neither focus action tells a demo user that changes are local, unlike every other
  dashboard write (`pages/Dashboard.tsx:65-67`, used at `:180,197,221,274`).

The focus card next to it ("Focus this week", `components/FocusStats.tsx:25-58`)
shows minutes against the weekly target. It reads block fields in snake_case
(`:33-35`) that the server sends untagged (`storage/models.go:14-21`, API-014).

### 2.4 Focus blocks are not anyone's in particular (new finding)

Focus blocks are saved without a user (`storage/focus_blocks.go:8-19`), and listing,
counting and deleting them filter by date only (`:21-29,46-58`), although the table
has had a user column since `storage/migrations/006_auth.up.sql:16`. Consequences
today: the focus card counts every user's blocks; the engine's daily-target check
counts other users' minutes (`engine/focus_time.go:112-117`); and "Clear Focus
Blocks" deletes every user's rows for that week while trying to delete their
calendar events through the caller's calendar (`engine/focus_time_cleaner.go:19-32`).
This is not in `docs/factory/api-audit.md`. It shows block times, not titles, so it
does not break the "managers never see titles" rule, but it leaks one user's
calendar shape to every other user and lets one user destroy another's blocks.

### 2.5 Automatic focus scheduling exists but is off by default

A per-user schedule runs the engine when enabled (`scheduler/cron.go:36-71`, started
at `main.go:50-53`); it is off by default (`storage/migrations/001_initial.up.sql:17-18`)
and toggled in Settings with a raw cron expression (`pages/Settings.tsx:392-411`).
The schedule set is read only at start-up (`scheduler/cron.go:73-76`), already
recorded by PAC-49.

### 2.6 Hand-off from Find a time

Find a time navigates to the dashboard with title, attendees and duration
(`components/team/FindATimeTab.tsx:64-70`); the dashboard prefills the language bar
and shows a toast (`pages/Dashboard.tsx:72-83`).

## 3. Desired behaviour

**One primary way to schedule.** The language bar at the top is the way to ask
Paceday for a meeting. Next to it is a quieter "use a form instead" entry that opens
the structured find-a-time form for the visible range, and the same form is offered
whenever the bar cannot understand a request or cannot reach its language model, so
a user without a working model is never stuck. There is no separate "Schedule
Meeting" button. Drag-selecting on the grid stays: it is direct manipulation of a
time the user already chose, not a competing search, and its "More options" keeps
handing the request to the bar.

**One date navigator.** Only the calendar's own navigator remains. The top bar on
the dashboard shows the brand, the page links and the account menu, as on every
other page.

**No "Quick actions" panel.** Its contents move to where they belong:

- **Focus lives in the focus card.** The card is the one place that says how much
  focus Paceday has protected this week. When the shown week is not entirely in the
  past and is below target, the card offers a single action phrased as an outcome
  ("Protect focus this week"), not a tool name. While it runs it cannot be triggered
  again; afterwards the card shows the updated total. No browser dialog appears.
- **Removing focus blocks is a secondary action of the same card**, offered only
  when the shown week has focus blocks and is not entirely in the past. It opens an
  in-app confirmation that says how many blocks will be deleted and that they will be
  removed from the user's calendar. Cancel does nothing. A partial failure says so.
  This is honestly labelled removal, not "undo"; a true undo of what Paceday did
  belongs to the follow-up (§0).
- **The audit log lives in Settings only**, where it is already linked.

Demo users see the same "changes are local" notice after either focus action as
after every other dashboard write.

Nothing here changes what a manager sees about their team, and nothing here reads or
exposes anyone's event titles.

## 4. Acceptance criteria

AC-1. Given a signed-in user on the dashboard, when the page has loaded, then there
is no "Quick actions" region and no control named "Run Focus Engine", "Clear Focus
Blocks", "Audit Log" or "Schedule Meeting".  [e2e]

AC-2. Given a signed-in user on the dashboard, when the page has loaded, then
exactly one control named "Previous" and one named "Next" exist, both inside the
calendar's navigation group.  [e2e]

AC-3. Given the dashboard, when the user activates the "use a form instead" entry
beside the language bar, then the structured find-a-time form opens with its range
equal to the calendar's visible range.  [unit]

AC-4. Given the language bar, when a request comes back not understood or fails,
then the error offers the structured form, and opening it from there leaves the
typed text in the bar.  [unit]

AC-5. Given the focus card for a week that is not entirely past and whose focus
minutes are below the weekly target, when it renders, then it offers exactly one
action to protect focus for that week; and given the week is at or above target, it
offers none.  [unit]

AC-6. Given the protect action, when the user activates it, then one focus run is
requested for the shown week with no browser dialog, the action is disabled until
the request settles, and on success the card's focus data is refetched.  [unit]

AC-7. Given a week entirely in the past, when the focus card renders, then it
offers neither the protect action nor the remove action.  [unit]

AC-8. Given a shown week with N ≥ 1 focus blocks, when the user activates "remove
this week's focus blocks", then an in-app dialog states N and that the blocks will
be deleted from their calendar, and no browser `confirm` is called.  [unit]

AC-9. Given that dialog, when the user cancels or presses Escape, then no removal is
requested; when the user confirms, then exactly one removal is requested for the
shown week and the card's focus data is refetched.  [unit]

AC-10. Given a shown week with zero focus blocks, when the focus card renders, then
the remove action is not offered.  [unit]

AC-11. Given removal fails, when the error returns, then the user sees a message
that the blocks may not all have been removed, and the card's focus data is
refetched rather than left as before.  [unit]

AC-12. Given a demo session, when either focus action succeeds, then the "changes
are local" notice is shown.  [unit]

## 5. Explicitly out of scope

- **Focus protection on by default, a status line saying it is on, and undo of an
  automatic run.** The follow-up in §0; blocked on OQ-1 and OQ-3.
- **The language bar's focus request running without confirmation.** It contradicts
  "changes are confirmed before they are applied" (PRODUCT.md) but changing it means
  adding a preview step the backend does not offer; raised as OQ-5.
- **Fixing API-014 / API-015 / API-079**, the unscoped focus blocks (§2.4) and the
  unscoped audit log (API-005, PAC-23). This spec must not make them worse; it does
  not fix them.
- **The raw cron field in Settings** and the start-up-only schedule reload (PAC-49).
- **Find a time's slot start**, which it sends and the dashboard ignores
  (`FindATimeTab.tsx:68` vs `Dashboard.tsx:74-77`).
- **Time display format** (PAC-55).

## 6. What must not change

- The calendar's navigation and view groups and the "Couldn't load your calendar"
  state — protected by `clockwise-like/e2e/tests/calendar.spec.ts:52-67`.
- The language bar and structured form remain accessible — `components/a11y.test.tsx`
  (renders both).
- The focus card's progress and its no-NaN guard — `components/FocusStats.test.tsx:18,24`.
- The focus API's read/clear behaviour — `clockwise-like/e2e/tests/focus-time.spec.ts`.
- Drag-select quick-create, drag/resize to reschedule, "More options" handing to the
  bar, the Find a time prefill hand-off (§2.6), the d/w/m view shortcuts
  (`Dashboard.tsx:147-161`), the Settings link to the audit log and the full audit
  page. **No test protects any of these** — a finding; the plan should add at least
  the prefill hand-off (the manager's path into the dashboard) and quick-create.
- The top bar on every other page.

## 7. Rollback

Revert the web PR. No migration, no contract change, no stored data touched; the
removed panel and navigator return exactly as before.

## 8. Open questions

- **OQ-1 — Should Paceday protect focus automatically by default?** owner: product
  owner. Simplicity and the positioning say yes: a good default beats a button. But
  Principle 3 says engine changes are "visible and confirmable, never silent", and an
  automatic run writes to the user's real calendar unasked. Options: (a) on by
  default, with "here is what Paceday did, undo" as the confirmation; (b) off by
  default, offered once during onboarding. This decides the follow-up's scope.
- **OQ-2 — Is a working language model guaranteed for every hosted user?** owner:
  product owner. If not, the bar fails for new users (§2.1) and the "form instead"
  entry is in practice the primary path; the owner may then prefer the form as
  primary. This spec keeps the bar primary with a guaranteed fallback.
- **OQ-3 — Focus blocks are not scoped to a user (§2.4).** owner: product owner to
  file and prioritise a defect issue (this agent did not post to Linear). It is a
  hard prerequisite for the follow-up, and the remove action in AC-8/9 should not be
  released to a multi-user deployment before it is fixed, since it deletes other
  users' rows.
- **OQ-4 — API-014 and API-015 are unclaimed.** owner: product owner (prioritise),
  engineering (claim the audit's batch C1). Until fixed, the focus card and the
  protect action's result read fields the server does not send, so AC-5–AC-11 can be
  proven with contract-shaped fixtures but not end to end.
- **OQ-5 — The language bar's focus request applies immediately.** owner: product
  owner. Keep, require confirmation, or route it to the focus card's action?
- **OQ-6 — Is drag-select a "way to schedule" the critique counted?** owner: product
  owner. This spec keeps it as direct manipulation; confirm that removes the
  overlap the critique saw.

## Challenge — 2026-10-11

**Verdict**: accept-with-changes

Checked against `smart-calendar-flow` main (a3a0f76) and `clockwise-like/backend` as on disk. Nothing was run. Proportionality was judged against the owner's "go for simplicity".

### Blocking
1. **The remove action ships a cross-tenant delete that the spec itself says must not ship.** OQ-3 says AC-8/9 "should not be released to a multi-user deployment" until §2.4 is fixed. No AC, scope item or rollback step enforces that, and Paceday is multi-user (teams, managers). Today `ClearWeek` lists blocks by date only (`engine/focus_time_cleaner.go:19`, `storage/focus_blocks.go:21-29`). It deletes every user's rows, including rows whose calendar delete failed (`:26-31`). The count N that AC-8 tells the user to trust would be the count across all users. This also breaks §5's "must not make them worse": the spec gives a cross-tenant delete a purpose-built confirmation instead of a buried button. *Resolves when* one of these holds: (a) the remove action and AC-8–AC-11 move to the follow-up, which already depends on OQ-3 (simplest, and four fewer ACs); or (b) fixing the §2.4 scoping becomes a stated prerequisite of this spec, with an AC that a user's removal leaves another user's blocks untouched.
2. **The protect action is gated on a number that does not exist in production.** AC-5 offers the action only when focus minutes are below target. `GET /api/focus/blocks` returns untagged `StartTime`/`EndTime` (API-014, `storage/models.go:14-21`), so the card's total is `NaN` whenever the week has any block (`components/FocusStats.tsx:33-36`). `NaN < target` is false, so the only dashboard focus control disappears for exactly the weeks where it matters. The spec also does not say what happens when no target is set (`hasTarget` false, `FocusStats.tsx:31`). Removing "Run Focus Engine" without a replacement that actually appears is a regression. *Resolves when* the spec states the card's behaviour when the focus total is unknown or no target is set (an AC in each case), or drops the below-target gate.
3. **The protect action has no failure path.** AC-6 covers success only. The run fails with a 500 when no calendar is connected, or when the token has expired or the calendar is unreachable (`api/handlers_focus.go:37-40`; `clockwise-like/e2e/tests/focus-time.spec.ts` header documents this). The dashboard already knows when the calendar is not connected (`pages/Dashboard.tsx:111`). *Resolves when* there is an AC for a failed run (what the user sees, and the action is re-enabled) and the spec states whether the action is offered at all to a user with no calendar connected.

### Non-blocking
1. The issue's headline question ("something the user does, or something Paceday shows it did, with undo?") is not answered here. It is deferred to a follow-up. With the owner's simplicity steer that split is defensible, but this spec only renames the button ("Protect focus this week"). Stage PRs must use `Part of PAC-57`, never `Fixes PAC-57`, and the follow-up should be filed before this one closes, or the issue will read as done when it is not.
2. Proportionality: AC-3/AC-4 add a new "use a form instead" entry next to the bar. Together with drag-select, that leaves three ways in, not one. The error-path fallback (AC-4) is justified by OQ-2. A permanent second entry beside the bar is not obviously needed to solve the issue, so OQ-2 should decide whether it exists rather than the spec presuming it.
3. API-035 (medium, `POST /api/focus/run` swallows malformed JSON) sits on the same focus-run operation as API-015 but is missing from the "listed, not inherited" set. It is not binding, because no operation changes.
4. OQ-5 leaves the language bar's focus request as a second, unconfirmed way to run focus. That is acceptable for simplicity, but §3's "the card is the one place" is then not literally true. Say so.
5. `nlp/parser.go:173` loads settings with `storage.GetSettings` (not per-user). If the bar stays primary, its provider is whichever settings row that returns. This bears on OQ-2.

### Criteria I could not falsify
- AC-5 (as written): with no target set, or with an unknown total, no test can tell pass from fail. See Blocking 2.
- AC-8: "states N" cannot be checked against a correct N while blocks are unscoped. A fixture-only test passes while production shows the cross-user count. See Blocking 1.

### Citations I checked and found wrong
- §2.4 says listing, counting **and deleting** focus blocks "filter by date only (`:21-29,46-58`)". Actually `DeleteFocusBlock` filters by `google_event_id` with no user predicate (`storage/focus_blocks.go:46-49`). Only list (`:21-29`) and count (`:51-59`) filter by date. The conclusion holds; the citation is imprecise.
- §2.4 says "the focus card counts every user's blocks". Actually it fetches every user's blocks, but cannot total them: with API-014 the card renders `NaN` for any non-empty week (`components/FocusStats.tsx:33-37`). §2.3 cites API-014 but never states this user-visible consequence. It matters for Blocking 2.
- All other §2 citations checked out, within a line or two: Dashboard, Navbar, QuickActions, FocusStats, NLPBar, QuickCreatePopover, ScheduleSuggestModal, Settings, App, types.ts, focus_time.go, focus_time_cleaner.go, models.go, migrations 001/002/006, cron.go, main.go, FindATimeTab.

### Consumers this breaks
- `smart-calendar-flow/src/components/Navbar.tsx:75-101`: the week-navigator props become dead. Its only caller is `pages/Dashboard.tsx:334-340` (no other `weekLabel=` in `src/`).
- `smart-calendar-flow/src/api/client.ts:915,985` and `api/contract.ts:66-68`: `runFocus` / `clearFocusBlocks` lose `QuickActions` as a caller. `runFocus` is still used by `Dashboard.tsx:176`.
- `clockwise-like/e2e/tests/calendar.spec.ts:52-67` asserts the "Calendar navigation" group; it is unaffected as long as that group keeps its name.
- No existing test references "Quick actions", "Run Focus Engine", "Clear Focus Blocks" or "Schedule Meeting" in either repo.
