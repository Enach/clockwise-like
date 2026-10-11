# Spec — PAC-56: Settings shows operator configuration to every user, contradicting "prefer a good default to a setting"

- **Linear**: https://linear.app/paceday/issue/PAC-56
- **Status**: draft
- **Author**: spec-author
- **Inherited scope**: none for this spec. It changes no API operation: the page
  keeps using the settings read and write operations exactly as PAC-24 leaves
  them. Open findings on those operations (API-002, -003, -004, -013, -025, -037,
  -039, -081, -082) and the `x-uncertain` markers in
  `contracts/openapi/paths/scheduling.yaml` are owned by PAC-24 or the audit
  batches, and two of them are **prerequisites** (§0). The proposed sibling
  issue PAC-56-B (§0) *would* touch the provider-probe operation and the
  provider fields of the settings document, and so would inherit API-003,
  API-037 and API-054 — those belong to its own spec, not this one.

## 0. Scope and split

The issue asks three questions: which settings belong to an end user vs an
operator, whether templates should be the default surface, and which save model.
The first has a half that cannot be done in the web app alone: the language-model
provider is read **from the user's settings record** by the backend
(`backend/nlp/llm_factory.go:13-40`), deliberately — `docker-compose.yml:46-48`
says provider credentials "are configured per-user in the Settings page … no
server-level env vars needed". Removing the form without giving the deployment
another source would silently break the natural-language bar and meeting prep
briefs for everyone. So the work is split:

- **PAC-56 (this spec), web only.** One settings page for ICs and managers:
  no operator controls, templates first with fine-tuning folded away, one save
  model. Every criterion here is buildable in the web repo against the current
  contract.
- **PAC-56-B (to be filed), backend + contract.** "The model provider is
  deployment configuration, not a user setting." Problem statement and a sketch
  are in §5; it needs its own spec because it reshapes PAC-24's provider-key
  criteria (PAC-24 AC-11 to AC-15), which is an owner decision (§8 OQ-1).

**Ordering.** AC-1 (model-provider controls gone) must not reach users before
PAC-56-B is live. Everything else in this spec must not reach users before
PAC-24's per-user scoping and merge-on-write are live (PAC-24 AC-1, AC-2, AC-6,
AC-8): saving on every change against today's shared, full-replace record
(API-002, API-004) would multiply the cross-user overwrites PAC-24 exists to stop.

## 1. Problem

Paceday's position is simplicity — "prefer a good default to a setting" and "one
obvious path" (`smart-calendar-flow/PRODUCT.md`, Positioning and Principle 1).
The settings page is the opposite. Every IC and every manager opens one long
column of 13 equally-weighted sections (~31 visible controls, ~8,700px), and
among their own working hours they find things only an operator could answer: a
cron expression, a free-text calendar identifier, and a form asking for an AI
provider, model name, API key, AWS region/profile or Azure endpoint/deployment/API
version. A user who does not know what those are cannot tell whether they must
fill them in, and a user who guesses can break the product for themselves.

The page also saves in three different ways. Some controls take effect
instantly, some instantly with a toast, and most only when "Save changes" — a
button at the very top of an 8,700px page — is pressed. Nothing warns a user who
leaves with unsaved edits, so changes are lost silently. One message even says a
value was "saved — remember to click Save changes". An Impeccable critique scored
the page 15/40 with 7 of 8 cognitive-load checks failing.

Both primary audiences pay this cost equally: the page is the same for ICs and
managers.

## 2. Current behaviour

All web paths are in `smart-calendar-flow`; backend paths in `clockwise-like`.

**2.1 Structure.** `src/pages/Settings.tsx:234-737` renders, in order: Your
profile (`:250`, component `:746-858`), Activity (`:252-260`), Scheduling
template (`:262-286`), Working Hours incl. timezone and a day-by-day editor
(`:288-330`), Focus Time (`:333-415`), Lunch Break (`:417-493`), Meeting Buffers
(`:496-521`), Meeting Policy (`:523-545`), Meeting Compression (`:547-556`),
Calendar Connection (`:558-574`), Personal Calendars (`:576-581`), Conferencing
(`:583-588`), AI / NLP (`:590-736`). Every section uses the same card
(`:80-90`). The page is identical for ICs and managers; the only role-dependent
content is two team links in the profile card (`:816-835`).

**2.2 Operator-facing controls.**
- *Cron.* When "Auto-schedule focus time" is on (`:393-398`), a free-text "Cron
  schedule" field appears with the hint "e.g. 0 7 * * 1-5" (`:399-413`). The
  server validates it as a 5-field expression (`backend/api/handlers_settings.go:100-109`)
  and registers one job per user with it (`backend/scheduler/cron.go:36-66`). The
  scheduler is created with `cron.New()` (`cron.go:27`), i.e. server-local time,
  not the user's timezone. Default is `0 8 * * *`, auto-schedule off
  (`backend/storage/migrations/001_initial.up.sql:17-18`).
- *Calendar ID.* A free-text "Calendar ID" field defaulting to "primary"
  (`Settings.tsx:564-572`). It is **dead**: the backend settings record has no
  such field (`backend/storage/settings.go:150-205`), the contract has none (no
  `calendarId` in `contracts/openapi/paths/*.yaml`), and the calendar client
  hard-codes `"primary"` (`backend/calendar/client.go:21`). The value only lives
  in the web app's hand-written type (`src/api/types.ts:73`) and offline default
  (`src/api/client.ts:106`).
- *AI / NLP.* Provider select over five providers (`Settings.tsx:58-64,592-604`),
  model (`:606-636`), AWS region/profile (`:639-660`), Azure
  endpoint/deployment/API version (`:663-693`), Ollama base URL (`:696-706`), API
  key (`:709-719`), and "Test connection" (`:166-182,722-735`). The backend builds
  the language-model client from these values on every natural-language request,
  meeting brief and probe (`backend/nlp/llm_factory.go:13`, `nlp/parser.go:238`,
  `api/handlers_meeting_briefs.go:74`, `api/handlers_llm.go:23`). There is no
  deployment-level source (`docker-compose.yml:46-48`).

**2.3 Templates.** Three cards — Balanced, Focus first, Custom
(`src/lib/schedulingPresets.ts:31-69`). A template sets working hours, focus
block min/max/target, lunch and buffers only (`:36-46,52-62`), and forces lunch
protection on (`:99`). The selected card is derived by matching current values
(`:84-94`); any other value makes it "Custom". The section says "Pick a starting
point, then fine-tune below. Nothing is saved until you press Save changes."
(`Settings.tsx:264`). All fine-tune controls are always visible below it.

**2.4 Three save models on one page.**
- *Explicit Save.* "Save changes" exists once, top right (`Settings.tsx:240-247`).
  It validates working hours and lunch (`:201-207`), sends the whole draft
  (`:210`, `src/hooks/useSettings.ts:13-20`) and toasts "Settings saved"
  (`:212`). This covers template, hours, timezone, focus, lunch, buffers, policy,
  compression, cron, calendar ID, AI, and — via the draft — the calendar provider
  choice (`src/components/WorkCalendarConnection.tsx:71`), the WebCal URL (`:77`),
  the default conferencing provider (`src/components/ConferencingSection.tsx:118`)
  and the Teams toggle (`:269`).
- *Instant with toast.* Personal calendars rename/enable/sync/delete/add
  (`src/components/PersonalCalendarsSection.tsx:160,240,251,266,307`), Zoom
  connect/disconnect (`ConferencingSection.tsx:84-103`), calendar disconnect
  (`WorkCalendarConnection.tsx:87-91`), role switch (`Settings.tsx:758-773`).
- *Instant redirect.* Google/Outlook connect leaves the page for OAuth
  (`WorkCalendarConnection.tsx:81`), discarding any unsaved draft.
- *Contradiction.* Saving a WebCal URL toasts "WebCal URL saved — remember to
  click Save changes" (`WorkCalendarConnection.tsx:78`) while the field says
  "Saved with the rest of your settings" (`:188`).
- *No guard.* There is no unsaved-changes warning anywhere in the web app (no
  `beforeunload` or route blocker in `src/`).

**2.5 Role.** The role is read from browser storage and written to it plus a
fire-and-forget server call (`Settings.tsx:748,758-762,769-770`). Manager → IC
asks for confirmation (`:752-755,837-855`).

**2.6 Tests.** No web test renders the settings page (only
`src/api/settingsAdapter.test.ts` and `src/api/errors.test.ts` touch settings
code). No e2e scenario drives it (`e2e/tests/focus-time.spec.ts:81` reads a
setting, never through the page).

## 3. Desired behaviour

**One page, the same for both seats, that a person can finish in a minute.** An
IC and a manager open the same settings page. At the top is who they are: their
role and how they want times shown (the 12-hour/24-hour choice PAC-55 adds lives
here, as a profile preference, defaulting to 12-hour). Then how their day is
shaped: pick a template — Balanced, Focus first — and their timezone. Then what
Paceday is connected to: work calendar, personal calendars, conferencing. Then a
link to the activity log. That is the default surface.

**Fine-tuning is there, folded away.** Everything a template decides, plus the
handful of behaviour switches, sits behind a single "fine-tune" disclosure that
is closed whenever the page opens: day-by-day hours, focus block sizes and daily
target, focus label and colour, auto-scheduling on/off, lunch protection and
times, buffers, out-of-hours allowance, auto-decline, meeting compression.
Changing any template-governed value there makes the template read "Custom", as
today. Choosing a template again overwrites those values, as today.

**No operator configuration on a user's page.** A user is never asked for
anything they would need an operator to explain:
- *Model provider*: no provider, model, key, cloud region/profile, endpoint or
  connection test. The product's AI simply works or reports that it is
  unavailable; where it comes from is the deployment's business (PAC-56-B).
- *Auto-scheduling timing*: one on/off switch. When on, focus time is scheduled
  at the product's default time. A user who already has a custom timing keeps
  it — the page neither shows nor changes it.
- *Calendar identifier*: gone. It does nothing today (§2.2).

Nothing stored is deleted by this work: values that the page no longer shows are
left exactly as they are.

**One save model: a change is saved when it is made.** There is no Save button.
Choosing an option, flipping a switch, picking a template, or finishing an edit
to a typed value (leaving the field) saves that change on its own, and the page
shows the same brief "Saved" confirmation for every kind of change. Connections
behave as today (they leave the page or act immediately) and so already fit.
Nothing on the page tells the user to save later, and there is nothing to lose
by navigating away. This is the simpler answer: it removes the button, the
"remember to save" message, the lost-edit risk, and the need to scroll to save.

- *Invalid input is not saved.* A value the page can tell is invalid (a day that
  ends before it starts, lunch outside working hours) shows an inline message
  next to the control and is not sent; everything else stays saved.
- *Failure is honest.* If saving fails, the user is told that change was not
  saved, and the control shows what is actually stored — never a value that
  looks saved but is not.
- *Order is kept.* Several changes made in quick succession all end up stored;
  a later change never undoes an earlier one.

The manager → IC confirmation stays: it is a consequential role change, not a
setting edit.

## 4. Acceptance criteria

AC-1. Given any signed-in user, IC or manager, when they open Settings and open
fine-tune, then no control on the page asks for a model provider, model name,
API key, cloud region or profile, service endpoint, deployment name, API
version or base URL, and there is no provider connection test.  [e2e]

AC-2. Given any signed-in user, when they open Settings, open fine-tune and turn
auto-scheduling on, then no text field for a schedule expression appears; the
only auto-scheduling control is the on/off switch.  [e2e]

AC-3. Given any signed-in user, when they open Settings and open fine-tune, then
there is no field for a calendar identifier.  [e2e]

AC-4. Given a user whose stored settings carry a non-default auto-scheduling
timing and a non-empty model-provider configuration, when they change their
timezone, pick a template and toggle compression through the page, then reading
their settings afterwards returns the same auto-scheduling timing and the same
provider configuration (key presence included) as before.  [e2e]

AC-5. Given any signed-in user opening Settings, when the page has loaded and
nothing has been clicked, then the role choice, the template choice, the
timezone, the work-calendar connection, personal calendars, conferencing and the
activity-log link are visible, and none of the following is visible: day-by-day
hours, focus block minimum/maximum/daily target, focus label or colour,
auto-scheduling switch, lunch controls, buffer values, out-of-hours allowance,
auto-decline, compression.  [e2e]

AC-6. Given Settings with fine-tune closed, when the user opens fine-tune, then
every control listed as hidden in AC-5 is visible and editable; and when they
change the daily focus target to a value no template uses, then the template
choice shows Custom.  [unit]

AC-7. Given fine-tune was opened, when the user reloads Settings, then fine-tune
is closed.  [unit]

AC-8. Given a user on any template, when they choose "Focus first" and then
reload the page without any other action, then "Focus first" is selected and the
stored working hours, focus targets, lunch and buffers equal that template's
values.  [e2e]

AC-9. Given Settings, when the page is searched for any control that saves
several settings at once or defers saving (a "Save" / "Save changes" button),
then there is none; and no text on the page or in its messages tells the user to
save later.  [unit]

AC-10. Given a user in fine-tune, when they set "buffer before" to 15, leave the
field, see the saved confirmation, and navigate to another page and back, then
"buffer before" reads 15; the same holds for the timezone, the default
conferencing provider, the Teams switch and a WebCal URL, each changed and
checked on its own.  [e2e]

AC-11. Given a stubbed server, when the user changes each of a choice, a switch
and a typed value, then each change produces a save and each shows the same
saved confirmation.  [unit]

AC-12. Given a stubbed server that rejects the next save, when the user changes
"buffer after" from 5 to 20 and leaves the field, then a message says the change
was not saved and the field reads 5.  [unit]

AC-13. Given the day-by-day editor, when the user sets a day to end before it
starts, then an inline message names the problem and no save is sent; when they
correct it, a save is sent.  [unit]

AC-14. Given a stubbed server that answers slowly, when the user changes "buffer
before" and then "buffer after" before the first save has returned, then the
last stored state contains both new values.  [unit]

AC-15. Given a manager, when they choose Individual contributor, then a
confirmation is asked before the role changes; cancelling leaves them a
manager.  [e2e]

AC-16. Given an IC and a manager, when each opens Settings, then both see the
same sections in the same order, the only difference being the manager's links
to their team.  [e2e]

AC-17. Given PAC-55 is live and a user on 12-hour, when they open Settings, then
the 12-hour/24-hour choice is visible in the profile area without opening
fine-tune; and choosing 24-hour is saved on that action alone (reload shows
24-hour).  [e2e]

## 5. Explicitly out of scope

- **PAC-56-B, the deployment source for the model provider.** Problem: if the
  provider is per-user (today, and after PAC-24), every user of a hosted service
  must bring and pay for their own AI key, or the natural-language bar and briefs
  fail for them; this contradicts "hosted SaaS" and "simplicity". Sketch of its
  criteria: a user with no provider configuration gets a working
  natural-language bar when the deployment has one; no settings read or write
  carries provider configuration; a deployment without one reports "AI
  unavailable" rather than an internal error. It needs its own spec and contract
  because it supersedes PAC-24 AC-11 to AC-15 (§8 OQ-1).
- **Changing when auto-scheduling runs** — e.g. in the user's timezone, or
  weekdays only. Today's default runs at 08:00 server time every day (§2.2); this
  spec only hides the expression. §8 OQ-3.
- **Deleting stored values** the page no longer shows (timing expression,
  calendar identifier, provider fields) or dropping their storage. Harmless left
  in place; removal belongs with PAC-56-B or a cleanup.
- **An operator or admin settings screen.** PRODUCT.md records no admin role.
  Operator configuration belongs to the deployment, not to a UI (§8 OQ-2).
- **Everything PAC-24 owns** (per-user records, merge semantics, validation on
  the server, the provider key) and **PAC-55's formatting work**; this spec only
  places PAC-55's control.
- **New templates, changing template values, or the timezone list.**
- **The activity log page, the personal-calendar and connection flows
  themselves**, beyond their placement and confirmation message.
- **Visual design detail** (spacing, typography). The Impeccable critique is a
  review aid, not an acceptance criterion; the design gate applies as usual.

## 6. What must not change

- **Every stored setting keeps its value** unless the user changes that setting
  (AC-4). No test protects this today (§2.6); PAC-24 AC-6 guards the server half.
- **Template semantics**: same templates, same values, lunch forced on, Custom
  derived from values (`schedulingPresets.ts:31-99`). No page-level test today;
  AC-6 and AC-8 add them.
- **Client-side validation of hours and lunch** (`Settings.tsx:194-207`) still
  blocks invalid input (AC-13). Not tested today.
- **Manager → IC confirmation** (`Settings.tsx:752-755,837-855`). Not tested
  today; AC-15 adds it.
- **Connection flows** (OAuth redirect, disconnect, Zoom, personal calendars)
  behave as now. Not tested at page level today.
- **Privacy**: nothing on this page exposes another user's data; managers still
  see only aggregate minutes elsewhere. Unaffected by this work.

## 7. Rollback

Web-only change with no migration and no stored value removed: reverting the web
PR restores today's page, and every value saved meanwhile is readable by it
(today's page sends the whole record, so it keeps values it loaded). If AC-1
shipped with PAC-56-B, rolling back the web PR alone re-exposes a provider form
that no longer drives anything; roll back both or neither.

## 8. Open questions

- **OQ-1 — Is "bring your own AI key" a product feature?** The code and compose
  file say per-user keys are deliberate (`docker-compose.yml:46-48`); PRODUCT.md
  says hosted SaaS and simplicity. Recommended answer: no — the deployment
  provides the model (PAC-56-B), and PAC-24 drops AC-11 to AC-15 in favour of it.
  Owner: product owner. Resolve: decision recorded on PAC-56 and PAC-24 before
  either contract is written.
- **OQ-2 — Where does operator configuration live?** Recommended: deployment
  environment only, no admin UI, consistent with "no admin role" in PRODUCT.md
  and SSO admin being undecided (PAC-50). Owner: product owner.
- **OQ-3 — Default auto-scheduling time.** Today 08:00 server-local, every day
  including weekends (`001_initial.up.sql:18`, `scheduler/cron.go:27`). For a
  user outside the server's timezone that is the wrong hour. Is "early morning in
  the user's own timezone, working days only" the intended default? If yes, a
  follow-up backend issue. Owner: product owner, then backend.
- **OQ-4 — Should "Focus label" and "Focus colour" exist at all?** Under
  "prefer a good default to a setting" they are candidates for removal rather
  than fine-tune. Kept in fine-tune here to avoid removing user-visible
  behaviour without a decision. Owner: product owner.
- **OQ-5 — Ordering against PAC-24.** §0 says this must not ship before PAC-24's
  per-user scoping and merge. If the owner wants the page simplified sooner, the
  save-on-change part (AC-9 to AC-14) can be held back and the rest shipped with
  the existing Save button moved to a persistent position. Owner: product owner.

## Challenge — 2026-10-11

**Verdict**: accept-with-changes

Judged against the Linear issue and the product owner's "go for simplicity"
instruction. Section 2 is accurate almost everywhere: I re-checked §2.1–§2.6 in
`smart-calendar-flow` (main, a3a0f76) and `clockwise-like`, and the
structure, operator controls, template mechanics, the three save models, the
role storage and the absence of page tests all hold. The defects are an AC that
cannot pass web-only, an AC that depends on a product decision nobody has made
yet, and an AC that contradicts §6.

### Blocking
1. **AC-10 cannot pass for the Teams switch or the default conferencing
   provider, and §0's "every criterion is buildable in the web repo against the
   current contract" is false.** Both values are web-only. `teams_enabled` and
   `default_conference_provider` have no field in `storage.Settings`
   (`backend/storage/settings.go:148-205`; the backend key is
   `conferencingProvider`, with a different enum, API-039). They are not in
   `SETTINGS_FIELDS` (`src/api/client.ts:546-573`), so the PUT sends them in
   snake_case, the decoder drops them, and GET never returns them
   (`contracts/openapi/paths/scheduling.notes.md:41`, and PAC-24's challenge
   at `PAC-24.md:946-947`). After navigating away and back, both read their
   defaults whatever the page does. To resolve: take those two items out of
   AC-10 and record their non-persistence in §2.4 and §5 as known, owned
   elsewhere (API-039 / PAC-24). Otherwise, say that this spec takes on that
   contract change, and then it is no longer web-only and the inherited scope
   includes API-039.
2. **AC-1 depends on PAC-56-B, which has not been filed, and on OQ-1, which has
   not been answered, and the spec only covers one answer.** If the owner decides that
   bring-your-own-key is a product feature, AC-1 is wrong. The spec then says
   nothing about where provider configuration goes, and PAC-56 can never close.
   To resolve: get the OQ-1 decision recorded before stage 2/3, and state what
   AC-1 becomes under each answer. Or move AC-1 into PAC-56-B, so that PAC-56
   ships and closes on its own.
3. **AC-8 contradicts §6, which says template semantics stay the same.** For a user whose
   working hours are in day-by-day mode, picking a template only replaces the
   default interval and keeps each day's own hours
   (`src/lib/schedulingPresets.ts:100-105`, `applyDefaultInterval`, by_day
   branch at `:124`). "The stored working hours equal that template's values"
   therefore fails unless template semantics change. The Given clause "a user on any template"
   includes that user. To resolve: limit the Given clause to all-days mode, or state the
   expected day-by-day result.

### Non-blocking
1. **Proportionality ("go for simplicity").** The biggest and riskiest part of the
   spec is the switch to saving on every change (AC-9 to AC-14, plus the
   ordering, rollback and inline-validation rules). It is also the only reason
   the whole spec is gated on PAC-24. The issue asks for *one* save model, not
   for this particular one. OQ-5 already describes a smaller alternative, but
   leaves the spec in two possible shapes. The owner should choose before stage 2
   so that the spec has one shape.
2. With save-on-change, picking an OAuth provider calls the patch and then
   redirects (`src/components/WorkCalendarConnection.tsx:71,81`). Today the
   redirect throws the patch away. Afterwards it may be stored before OAuth
   completes. The spec should say whether a stored calendar provider changes
   when the user abandons the connection. "Connections behave as today" does
   not settle it.
3. AC-15 and AC-16 lock in behaviour that already exists rather than behaviour
   this spec changes. They are useful as regression tests, but they add e2e cost without
   delivering anything new for the issue.
4. AC-17 overlaps with PAC-55's own placement criterion. Name the owner (this
   spec places the control; PAC-55 defines what it does). That way a slip in
   either spec does not leave the control unowned. The placement (profile area,
   12-hour default, 24-hour opt-in) matches the product owner's request.

### Criteria I could not falsify
- None. AC-8 and AC-10 are falsifiable, but as written they fail for reasons
  outside this spec (Blocking 1 and 3).

### Citations I checked and found wrong
- §2.4 says explicit Save "covers … the default conferencing provider
  (`ConferencingSection.tsx:118`) and the Teams toggle (`:269`)". In fact both
  are put in the draft and sent, but the backend ignores them and never
  returns them (`backend/storage/settings.go:148-205`;
  `contracts/openapi/paths/scheduling.notes.md:41`). Save does not save them
  today.
- §0 says "every criterion here is buildable in the web repo against the current
  contract". AC-10 is not (see Blocking 1).
- Minor line drift, with no change of meaning: the toast in
  `PersonalCalendarsSection.tsx` is at 165/243/252/267/308 (cited
  160/240/251/266/307).

### Consumers this breaks
- `smart-calendar-flow/src/components/WorkCalendarConnection.tsx:71,77-81`:
  with save-on-change, the provider patch made before the OAuth redirect becomes
  a persisted write, and the WebCal toast at `:78` must change (AC-9).
- `smart-calendar-flow/src/api/client.ts` `llmTest` (`:1207`) and
  `Settings.tsx:166-182`: once AC-1 lands, no page calls them any more.
- `clockwise-like/backend/nlp/llm_factory.go:13`, `nlp/parser.go:238`,
  `api/handlers_meeting_briefs.go:74`, `api/handlers_llm.go:23`: these read the
  provider only from the user's settings. If AC-1 ships before PAC-56-B, a new
  user's natural-language bar and briefs fail. §0 orders against this, but
  nothing enforces it.
