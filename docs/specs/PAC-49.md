# Spec — PAC-49: automatic focus scheduling must act for exactly the people who asked for it, and the migration that says so must be telling the truth

- **Linear**: https://linear.app/paceday/issue/PAC-49/listuserswithautoschedule-does-not-filter-user-id-so-the-focus-cron-is
- **Status**: v2 (challenged 2026-10-04, accept-with-changes) — **superseded by v3, appended below on 2026-10-21; v3 was challenged on 2026-10-22 (accept-with-changes) and revised to v3.1 (2026-10-22) — v3 challenge answered; awaiting stage 2.** The v2 body, its Challenge and the v3 Challenge are kept verbatim as lineage; where v2 and v3.1 disagree, v3.1 wins.
- **Author**: spec-author
- **Inherited scope**:
  - **Resolved by this spec**: nothing from the defect register. Argued in §0.2 —
    no audit finding and no `x-uncertain` marker sits on an operation this spec
    changes, because the whole of the shippable half of this spec sits below the
    HTTP boundary.
  - **Listed because they sit on the operation that carries this defect's
    HTTP-visible symptom** (`POST /api/focus/run`): **five** items — API-014
    (high, Confirmed), API-015 (high, Reported), API-033 (medium, Reported — it
    is filed against "~15 endpoints (see T5)" at `docs/factory/api-audit.md:267`,
    and theme T5 names `FocusRunResult.createdBlocks`/`skippedDays`/`errors`
    explicitly at `:133-139`, which is this operation's response body),
    API-035 (medium, Reported), and the field-level uncertainty note on the
    focus-run result's skipped-days array
    (`contracts/openapi/openapi.yaml:6228-6231`, register item U-23 at
    `docs/factory/api-audit.md:467`). Factory §7 rule 1 binds a feature that
    *modifies* an operation; this spec does not change that operation's request
    or response, so it does not inherit them today. **If the contract stage
    concludes otherwise** — the most likely trigger is wanting to make the
    failure in §2.7 honest, which OQ-7 now puts on the table — then all **five**
    become inherited and must be resolved before that contract can pass its
    gate. Stated here so the decision is made rather than drifted into, and
    counted exactly, because the count is the thing a contract author acts on.
  - **Discovered while writing this spec, not in the register, filed rather than
    fixed**: nothing re-reads the schedule set after a preference is saved, so
    switching automatic scheduling on takes effect only at the next restart
    (§2.9, OQ-3); the same missing predicate exists latently on the
    personal-calendar fan-out (§2.8, AC-5); migration 018's second comment is
    wrong in a related way to its first (§2.6); and
    `storage.ListPersonalCalendars` (`backend/storage/personal_calendars.go:32`)
    has no caller anywhere and is dead code (OQ-8).
  - **Inherited, explicitly not resolved**: API-002, API-003, API-004 (the shared
    preferences record and the full-replace write). PAC-24 owns all three and
    this spec must not pre-empt it — see §5.
  - **Owned by nobody today, and this spec says so rather than assuming
    otherwise**: the HTTP failure every user on a fresh install gets from "run my
    focus time now" (§2.7, OQ-7). v1 handed it to PAC-24 on the strength of a
    commitment PAC-24 does not make.
  - **Correction owed to an artifact, not to code**: `docs/specs/PAC-24.md:835-847`
    (challenge, Blocking 2) states the same mechanism this spec shows in §2.2 to
    be false. The conclusion it draws — that 018's comment is untrue — survives;
    the mechanism does not. OQ-4.

> **A note on this spec's own evidence.** Nothing here was executed — Docker is
> down on this machine, so no test, no build, no migration and no request was run
> for this spec. Every claim in §2 comes from reading source and carries a
> `file:line`. Citation basis is `origin/main` at `e9d01cc`. Line numbers outside
> the repository are given against `$(go env GOROOT)` on go1.25.5 and against
> `$(go env GOMODCACHE)` at the versions `backend/go.mod` pins. Where a claim is
> an inference from reading rather than an observation it says so, and where a
> claim would be worth more as a real run it is written as an acceptance
> criterion in §4 instead of as a finding here.
>
> The Linear issue and its comments were fetched directly; this spec is not
> working from a relayed summary. `list_comments` returned no comments for me;
> the challenger's attempt returned a 502, so that particular negative is mine
> alone and nothing in this spec rests on it.

> **Revision — v2, 2026-10-04 (after the stage-1 challenge appended below).**
> The challenge's verdict was accept-with-changes and the §2.2 correction that
> the whole spec turns on was re-verified at all three links and stands. What
> changed in this revision, and why:
>
> - **The HTTP symptom is no longer handed to PAC-24.** v1 §2.7 and §5 claimed
>   PAC-24 "owns it, specifies it and has an acceptance criterion for it". It
>   does not: PAC-24 §2.4 names no route and its AC-3 says "a background job"
>   (`docs/specs/PAC-24.md:207-228`, `:491-493`). The only PAC-24 text naming the
>   operation is inside its own unresolved challenge finding (`:819-820`) on a
>   spec still at `Status: draft` (`:4`). §2.7 and §5 now state what PAC-24
>   covers and what it does not, and OQ-7 asks who takes the rest. Declining a
>   symptom on the strength of a commitment that does not exist is the same
>   failure mode as 018's comment, which is this spec's own subject.
> - **A credential gate was missing from §2.8, and it changes the blast radius.**
>   `newCalOps` (`backend/engine/cal_iface.go:53-58`) refuses a user with no
>   stored calendar token, before anything is written. Post-024 the blast radius
>   is "every user who has connected a calendar", not every user. §1, §2.8 and
>   AC-9 are corrected, and AC-9 now requires that its negative clause be proven
>   against users whose calendars Paceday can actually reach — otherwise it
>   passes vacuously.
> - **AC-6 was not buildable and has moved** to its own bucket behind OQ-6, and
>   is restated against an observable that survives `Reload` running once per
>   process. v1 asked for a report "once per pass" in a system that has one pass.
> - **§3.5's principle excluded a live instance of the wrong it names.** The
>   morning summary already fans out to every user off the same unattributed
>   record (`backend/engine/daily_recap.go:121-143`), fired every minute
>   (`backend/main.go:99-110`). §3.5 now states the class and names that instance
>   and its owner; OQ-5 no longer claims ignorance about a cron whose defect is
>   written down in a file this spec quotes.
> - **Citations corrected**: §2.9's grep claim (false — `cron_test.go:75, 89,
>   109, 125, 132` call `Reload`; the conclusion about *production* call sites
>   survives); §2.6's third leg (dropped — `ListPersonalCalendars` is dead code
>   and cannot carry the argument, so it is a set of two); the register
>   undercount (five, not four — API-033); OQ-6's `main.go:21` → `:25` and
>   `cron.go`'s three log sites → five, including `:69`, the one AC-6 most needs;
>   and two off-by-one ranges (`convert.go:307-327`,
>   `openapi.yaml:6228-6231`).
> - **§2.4's causation was imprecise.** 018's `EXISTS` guard is irrelevant on a
>   genuinely fresh install, because the table is empty when migrations run. The
>   conclusion is unchanged; the mechanism is now right, and OQ-2's diagnostic
>   now distinguishes three states and returns a count.
> - **Smaller**: the `[unit]` tags now say they need a live Postgres; AC-7's tag
>   is dropped because no test can hold a claim about prose; OQ-1's compression
>   aside is withdrawn (nothing reads that flag, so it is not a hazard — it is
>   the reason the cell should be split); §4 says whether its guarantees cover one
>   unattributed record or any number; §6 records that no consumer can observe
>   either change and that the migration runner stores no checksum; and §2.7
>   notes a second consumer of the same operation inside this repo
>   (`mcp/tools.go:143-146`).
>
> Nothing in the challenge was rejected. Two of its suggestions were taken in a
> different form than proposed, and both are flagged where they occur (§3.5,
> OQ-7).

---

## 0. Scope

### 0.1 Why this is one spec and not two

The issue contains two things: a defect that exists today, and a hazard that
another in-flight change will create. They have one root in common — a query
that enumerates people without checking that it has a person — and the fix for
the first is a precondition for being able to reason about the second at all. So
they are specified together.

But they cannot *ship* together, and §4 is split into four buckets rather than
two, because v1's two-way split conflated a decision with a deliverable:

| Bucket | Criteria | Gated on |
|---|---|---|
| **Buildable now** | AC-1, AC-2, AC-3, AC-4, AC-5, AC-7 | nothing outside this spec |
| **Blocked on OQ-6** | AC-6 | knowing what an operator can actually see, so the criterion can name an observable instead of a channel |
| **Blocked on OQ-1** | AC-8 | the product decision about which preferences are copied — this is the *only* criterion an answer to OQ-1 unblocks |
| **Blocked on migration 024 existing** | AC-9, AC-10, AC-11 | PAC-24's deliverable. Each begins "when every person is given their own preferences", which is a thing being built, not a thing being decided. AC-10 additionally presupposes one particular answer to OQ-1 |

The distinction in the last two rows matters, because the sequencing argument is
this spec's reason for being one document rather than two. **OQ-1 is on the
critical path and unblocks one criterion; it does not unblock four.** PAC-24 is
already specced, contracted and planned (`docs/specs/PAC-24.md`,
`contracts/features/PAC-24.yaml`, `docs/factory/PAC-24-plan.md`), and its plan
already names the migration file and the tests that will go with it
(`docs/factory/PAC-24-plan.md:54, 72-75, 160, 182`). OQ-1 is the last open
*decision* standing between PAC-24 and stage 4, and answering it after migration
024 is written means rewriting a shipped migration — which is the whole argument
for settling it first.

### 0.2 Why the register is empty for this one

The register (`docs/factory/api-audit.md`) was built by auditing the HTTP
boundary. The shippable half of this defect lives entirely beneath it: a storage
query, a cron registration loop and a comment in a migration. The register
contains the string "cron" zero times, so it has no background-job surface at
all, which is why a deployment-wide dead feature went unrecorded. That gap is
worth noting and is OQ-5 — which, as revised, no longer pretends the gap is
total: one of the five background processes has its defect written down
elsewhere.

---

## 1. Problem

Paceday offers to put focus time on your calendar for you, on a schedule you
choose. You switch it on once and it is supposed to keep happening.

On a freshly installed deployment it never happens, for anybody. Someone
switches it on, sees it saved, and nothing is ever written to their calendar.
There is no error on the page, no email, no banner — the only trace is a line in
the server log that says a scheduled run was attempted for a user whose
identifier is all zeroes, which means nothing to the person who was waiting for
their Tuesday morning to be protected. The feature is not degraded; it is
absent, and it is absent silently.

The cause is that the preferences the deployment is using are not attached to
any person. Nothing in the running product ever attaches them. So when the
scheduler asks "who wants focus time scheduled?", the answer it gets back names
nobody, and a run for nobody is refused one layer down.

That is today's cost. The second cost is scheduled to arrive. PAC-24 fixes the
underlying problem — it gives every person their own preferences — and as
currently specified it does so by copying the shared preferences to everyone,
including the switch that says *yes, write to my calendar on this schedule*.
The moment that lands, every user of the deployment is enrolled in automatic
focus scheduling because one person — the first account, usually the person who
installed it — had switched it on for what was then a single-user product. At
that person's chosen hour, Paceday writes blocks into the real calendars of
every colleague who has connected one, during their real working day, for a
feature they were never offered and never accepted. Those events are in Google
by then. No down migration takes them out again.

The "who has connected one" is a real limit and not a reassuring one: the people
it excludes are the people for whom Paceday does nothing anyway, so it restricts
the damage to exactly the population that uses the product. It is stated because
a criterion that does not state it can pass while proving nothing — see AC-9.

The two halves meet in one place: a query that is written as though it were
enumerating people when in fact it is enumerating preference records, some of
which belong to nobody. And a comment in a shipped migration asserts that the
query does the check it does not do — which is how the decision not to require
an owner on those records came to be made, and how it will be made again by the
next person who reads it.

---

## 2. Current behaviour

### 2.1 The query that enumerates schedulable people does not check that it has a person

`ListUsersWithAutoSchedule` (`backend/storage/settings.go:377-398`) is:

```sql
SELECT user_id, auto_schedule_cron
FROM settings
WHERE auto_schedule_enabled = TRUE
  AND auto_schedule_cron IS NOT NULL
  AND auto_schedule_cron <> ''
```

(`:380-384`). There is no `user_id` predicate. Its sibling two functions below,
`ListUsersWithAutoDecline` (`:400-416`), has one: `WHERE user_id IS NOT NULL AND
auto_decline_outside_working_hours = TRUE` (`:402`). The two were written for the
same purpose at the same layer and disagree.

The destination is a bare value type: `UserScheduleConfig.UserID` is
`uuid.UUID`, not a nullable wrapper (`:372-375`, the field at `:373`), and the
scan is `rows.Scan(&c.UserID, &c.CronExpr)` (`:392-394`).

### 2.2 A NULL owner does **not** fail the scan — the issue's stated mechanism is wrong

The issue asserts that "a NULL `user_id` therefore fails `rows.Scan`, the
function returns an error". It does not. The chain, read end to end:

1. The driver is `pgx/v5` via `database/sql` (`backend/storage/db.go:11, 18`),
   pinned at `v5.10.0` (`backend/go.mod:18`). For a NULL column its `Rows.Next`
   assigns the destination `nil`, not a typed zero and not an error:
   `$(go env GOMODCACHE)/github.com/jackc/pgx/v5@v5.10.0/stdlib/sql.go:877-887`
   — `if rv != nil { … } else { dest[i] = nil }`.
2. `database/sql`'s `convertAssignRows` has a `case nil:` branch
   (`$(go env GOROOT)/src/database/sql/convert.go:307-327`) but it handles only
   `*any`, `*[]byte` and `*RawBytes`. A `*uuid.UUID` is none of those, so it
   falls through to the generic scanner dispatch at `:393-395`:
   `if scanner, ok := dest.(Scanner); ok { return scanner.Scan(src) }`, with
   `src` still `nil`.
3. `uuid.UUID.Scan` (`$(go env GOMODCACHE)/github.com/google/uuid@v1.6.0/sql.go:15-19`,
   the version pinned at `backend/go.mod:17`) begins `switch src := src.(type) { case nil: return nil }`.
   It returns no error and leaves the receiver untouched.

So a NULL owner scans cleanly to `uuid.Nil` — the all-zero UUID — and the
listing **succeeds**, returning that record as though it were a person.

**This tolerance is a property of `uuid.UUID`, not of the driver.** The same
NULL scanned into a bare non-`Scanner` value type *would* fail at step 2's
fall-through — which is why nullable columns elsewhere in this package use
pointers, as `PersonalCalendar.LastSyncedAt` does (`*time.Time`,
`backend/storage/personal_calendars.go:19`). Anyone reading the correction as
"this driver tolerates NULLs" will draw the wrong conclusion about the next
query.

This is the load-bearing correction in this spec. Everything the issue says
about consequences follows from the premise that the listing aborts, and it does
not abort.

### 2.3 What actually happens: one entry for nobody, registered beside everybody else's

`FocusCron.Reload` (`backend/scheduler/cron.go:36-71`) takes the listing, drops
all previously registered entries (`:44-47`), and registers one cron entry per
returned record (`:50-70`), keyed in `fc.entryIDs` by the scanned identifier
(`:68`). With a NULL-owner record present it therefore registers an entry keyed
`uuid.Nil` **in addition to** every genuinely attributed record's entry. The
error branch at `:37-41` — the one the issue says fires — is not reached, and no
*attributed* record is affected.

Registration itself is logged: `log.Printf("cron: focus time scheduled for user
%s: %s", userID, cronExpr)` (`backend/scheduler/cron.go:69`). So the all-zero
UUID is printed at boot, announcing a schedule for nobody as though it were a
schedule for somebody.

When that entry fires, `FocusTimeEngine.RunForUser` rejects the all-zero
identifier on its first statement:
`if userID == uuid.Nil { return nil, fmt.Errorf("FocusTimeEngine.RunForUser: userID is required") }`
(`backend/engine/focus_time.go:70-73`). The caller logs it
(`backend/scheduler/cron.go:57-59`) and returns.

**The entire observable effect of the missing predicate, in isolation, is two
log lines — one at registration, one per firing.** No calendar is written to. No
other user's schedule is disturbed. "The focus cron is dead today" and "no cron
entries at all" are both false as stated in the issue.

One caveat on "no attributed record is affected", because the schema permits a
case the product does not produce. `fc.entryIDs` is
`map[uuid.UUID]cron.EntryID` (`backend/scheduler/cron.go:22`), written at `:68`
and cleared by iterating itself at `:44-47`. Migration 018 adds
`UNIQUE (user_id)` (`018_per_user_settings_and_calendars.up.sql:21-22`) and says
in terms that "PostgreSQL UNIQUE allows multiple NULLs, so this constraint
coexists with the legacy 'no user yet' default row" (`:19-20`). **Two**
unattributed records would therefore both key `uuid.Nil`: the second overwrites
the first in the map, the first's entry is never passed to `fc.cron.Remove`, and
it outlives every later reload — including one after this spec's fix lands —
until the process restarts. I could not reach that state through any product
writer (`insertDefaultSettings` is `id=1 ON CONFLICT (id) DO NOTHING`, and
`settings.user_id` is `REFERENCES users(id)` at
`backend/storage/migrations/006_auth.up.sql:15`, so an all-zero owner cannot be
stored either). **That is an inference from the writer set, not an
observation**, and it is why AC-1 to AC-4 are written for *any number* of
unattributed records rather than one.

The pattern that saves it is deliberate and repeated — `GetSettingsByUser`
(`backend/storage/settings.go:420-423`), `ListPersonalCalendarsByUser`
(`backend/storage/personal_calendars.go:163-166`) and
`PersonalBlocker.SyncAllForUser` (`backend/engine/personal_blocker.go:47-50`)
all reject `uuid.Nil` on entry. That is why a defect of this shape produces
noise rather than damage. It is also why the noise is the only warning, and why
nobody noticed.

### 2.4 Automatic focus scheduling *is* nonetheless dead on a fresh install — for a different reason

The true version of the issue's headline claim is narrower, and does not depend
on the missing predicate at all.

Nothing in the running product ever writes `settings.user_id`:

- `insertDefaultSettings` is `INSERT INTO settings (id) VALUES (1) ON CONFLICT (id) DO NOTHING`
  (`backend/storage/settings.go:261-266`) — no owner.
- `SaveSettings` upserts the same singleton, `VALUES (1, …) ON CONFLICT (id)`
  (`:269-271` and onward) — no owner.
- `GetSettings` reads `… FROM settings WHERE id = 1` (`:227`).
- A grep for `INSERT INTO settings` across the tree returns those two, plus two
  test fixtures (`backend/engine/focus_time_test.go:318`,
  `backend/scheduler/cron_test.go:43`) and the e2e seed
  (`e2e/seed/seed.sql:79-90`). There is no `SaveSettingsByUser` — the audit
  register names it as something that ought to exist
  (`docs/factory/api-audit.md:514-519`) and it does not.

A grep for `UPDATE settings` adds `backend/auth/microsoft_oauth.go:57`,
`backend/storage/conferencing.go:6`, `backend/storage/personal_calendars.go:156`,
`backend/storage/settings.go:505` and migrations 002 and 020 — none of which
touches `user_id`.

The column has been nullable since it was added
(`backend/storage/migrations/006_auth.up.sql:15`). The only thing that has ever
set it is migration 018's best-effort backfill
(`backend/storage/migrations/018_per_user_settings_and_calendars.up.sql:12-17`),
guarded by `AND EXISTS (SELECT 1 FROM users)` (`:17`).

**That guard is a red herring on a genuinely fresh install, and v1 of this spec
got the mechanism wrong by leaning on it.** `001_initial.up.sql:1-24` creates
`settings` empty and inserts nothing, and migrations run inside `storage.Open`
(`backend/storage/db.go:17-28`) before the HTTP server is listening. So 018's
`UPDATE settings` touches **zero rows** whatever the guard evaluates to — there
is no row to attribute. The unattributed row is created later, by the running
product: the first settings read misses, `GetSettings` gets `sql.ErrNoRows`, and
`insertDefaultSettings` creates the ownerless singleton
(`backend/storage/settings.go:249-251, 261-266`).

The conclusion is unchanged and is the thing that matters — NULL forever,
because nothing ever writes it afterwards — but the correct mechanism matters for
OQ-2, because it means a deployment can be in any of **three** states, not two:

| State | How it arises | Automatic focus scheduling |
|---|---|---|
| **No preferences record at all** | installed, migrated, nobody has yet caused a settings read | nothing is registered, and nothing is wrong yet. `TestFocusCron_Reload_NoUsers` is in exactly this state (§2.10) |
| **One unattributed record** | the product created the ownerless singleton after migration — the normal fresh install | the only record the listing can ever return names nobody. **Automatic focus scheduling cannot work for anyone on such a deployment**, and the reason is not a failed listing: no real user has an attributed preferences record at all |
| **One attributed record** | an account existed when 018 ran, so the backfill had a row *and* a user and attributed the singleton to the earliest-created one (`:15`) | that one person's scheduling works. Everybody else is invisible, for the same reason |

The second and third states are reached by different histories of the same
deployment, and only data tells you which one you are in — hence OQ-2.

Two schema defaults decide when any of this becomes visible:
`auto_schedule_enabled BOOLEAN NOT NULL DEFAULT FALSE` and
`auto_schedule_cron TEXT NOT NULL DEFAULT '0 8 * * *'`
(`backend/storage/migrations/001_initial.up.sql:17-18`). So the record qualifies
for the listing the moment somebody switches scheduling on through the shared
settings page — the cron expression is never the thing that disqualifies it,
because it is non-empty by default. Until somebody switches it on, the listing
returns nothing and the symptom does not exist.

### 2.5 The one case where the fix changes nothing, and why it still matters

In the third state of §2.4's table — the attributed singleton — the predicate
makes no difference today: the record has an owner, so it passes either way. The
predicate matters (a) in the second state, where it converts two misleading log
lines into an honest empty set, (b) in none of them does it make scheduling start
working for anyone who is not already served, which is worth saying plainly
because the issue implies otherwise, and (c) as the precondition for being able
to say anything true about what happens after PAC-24 — see §2.8.

### 2.6 Migration 018's comments are untrue, and one of them is the reason the column is nullable

`backend/storage/migrations/018_per_user_settings_and_calendars.up.sql:5-10`:

> We do NOT impose NOT NULL on settings.user_id here because the existing
> insertDefaultSettings path (legacy single-row id=1 helper used by ~16 handler
> call sites) doesn't yet supply a user_id. The cron iteration queries
> explicitly filter `WHERE user_id IS NOT NULL AND auto_schedule_enabled`
> so NULL rows are safely ignored.

The second sentence is false (§2.1). This is the half of the issue that stands
entirely. It matters more than the log line it produced, because it is the stated
justification for leaving the column nullable in a **shipped** migration, and
migrations are immutable once shipped (`docs/factory/PAC-24-plan.md:54`). The
next author to reach for the same reasoning will find it already endorsed.

The same file's second comment (`:24-25`) — "personal_calendars: only backfill,
don't tighten. Per-user query already filters by user_id explicitly, so NULL
rows are silently skipped" — is wrong in a related way, though more narrowly
than v1 of this spec claimed. `ListPersonalCalendarsByUser` does filter and does
guard the all-zero owner (`backend/storage/personal_calendars.go:163-168`). But
`ListUsersWithPersonalCalendars` is
`SELECT DISTINCT user_id FROM personal_calendars WHERE enabled = TRUE` (`:188`)
with no owner predicate, and it is the query the fan-out actually uses (§2.8).
So "the per-user query" is singular about a set of two, one of which does not do
what the comment says.

> v1 made it a set of three by including `ListPersonalCalendars` (`:32`), which
> does return every row unfiltered. That function has **no caller anywhere** — it
> is dead code, and it is not a per-user query, so it cannot bear on a comment
> about what the per-user query filters. Withdrawn as evidence; filed as OQ-8.

### 2.7 The HTTP-visible symptom, and who owns it (nobody, today)

`POST /api/focus/run` reaches the same engine: `runFocus`
(`backend/api/handlers_focus.go:19-45`) calls `h.eng.Run(r.Context(), targetWeek)`
(`:37`), which pulls the caller's identifier from the request context and
delegates (`backend/engine/focus_time.go:59-65`), and `RunForUser` then looks up
that person's preferences and fails if there are none:
`return nil, fmt.Errorf("no settings row for user %s", userID)`
(`:76-82`). The handler writes that through `http.Error` (`:39`), so the user
gets a 500 whose body is a raw Go error string as `text/plain`
(`contracts/openapi/openapi.yaml:1741-1748` documents exactly that).

So on a fresh install, pressing "schedule my focus time now" returns a server
error to every user, and the scheduled version silently does nothing. Those are
the same root cause wearing two faces.

There is a second consumer of that operation inside this repository: the MCP
server's `clockwise_run_focus_engine` tool posts to the same route
(`mcp/tools.go:143-146`), so an agent driving it receives the same raw Go error
string as a tool result. Nothing on that path changes here, but the MCP surface
is in the factory's own ownership table (`docs/factory/README.md` §1) and v1's
claim that the browser was the only consumer was simply wrong.

**Who owns the HTTP face: nobody, and this is the correction that matters most
in this revision.** v1 of this spec asserted that "PAC-24 owns the HTTP face —
its §2.4 describes it and its AC-3 covers it". Checked:

- PAC-24 §2.4 (`docs/specs/PAC-24.md:207-228`) is titled "Nothing ever creates a
  per-user settings row". It names engine call sites only — `focus_time.go:76`,
  `auto_decline.go:66`, `habits.go:70`, `:96`, and `focus_time.go:82-84`. **No
  route, no handler, no status code.**
- PAC-24 AC-3 (`:491-493`) reads "when a **background job** that needs that
  user's settings runs for them". Explicitly the background path.
- The only text in PAC-24 that names this operation is inside **its own
  challenge**, Blocking finding 1 (`:819-820`), whose resolution clause asks
  PAC-24's author to add precisely what v1 of this spec assumed was already
  there. That finding is unresolved and PAC-24 is still `Status: draft` (`:4`).

So PAC-24 covers the background path and does not cover the HTTP one. The 500
that every user on a fresh install gets from "run my focus time now" is, as the
register stands, **owned by no spec at all** — and v1 declined it on the strength
of a commitment that does not exist, which is the same failure mode as 018's
comment and would have been endorsed by the next reader for the same reason. §5
states the deferral conditionally instead, and OQ-7 asks who takes it.

### 2.8 What PAC-24 would do, and the correct attribution of the hazard

PAC-24 §8 OQ-1 (`docs/specs/PAC-24.md:711-731`) proposes a partition of which
preferences migration 024 copies to every user. "Compression and auto-schedule
enablement and schedule" is in the **copied** column (`:724`).

If that is built as proposed, then after migration 024 every user has their own
preferences record carrying `auto_schedule_enabled = TRUE` and the owner's cron
expression. The listing returns every user — correctly, with or without the
predicate, because every record now has a real owner. Each gets a registered
entry. Each entry fires. And then there are **three** gates in `RunForUser`, not
two:

1. The identifier must not be the all-zero UUID (`backend/engine/focus_time.go:70-73`).
   Passed: it is a real account.
2. The person must have a preferences record (`:76-82`). Passed: migration 024
   has just created one for everybody.
3. **The person must have a stored calendar credential.** `RunForUser` calls
   `e.calClient(ctx)` (`:84-87`) before it writes anything, which reaches
   `newCalOps` (`backend/engine/cal_iface.go:53-58`):
   `token, err := auth.LoadUserToken(db, userID); if err != nil || token == nil { return nil, fmt.Errorf("not authenticated") }`.
   A user who has never connected their own calendar fails here, before any day
   is processed.

So the blast radius of migration 024 is **"every user who has connected a
calendar"**, not "every user". v1 of this spec stated the latter, in §1 and here,
and that was wrong.

The hazard survives the correction intact, because the excluded population is
exactly the population for which Paceday does nothing anyway: every user who
actually uses the product gets focus blocks written into their live calendar at
the owner's chosen hour. What does not survive is a criterion that asserts "no
other person's calendar was touched" without saying whose calendar could have
been touched — see AC-9, which v1 got wrong for this reason.

**The missing predicate is not what causes that.** Once every record is
attributed, the predicate is inert. The cause is copying the enablement switch to
people who never set it. The issue conflates the two ("With no `user_id` filter
and every row now attributed, …"); the filter is doing no work in that sentence.
This distinction is not pedantry: it decides where the fix goes. Adding the
predicate does not mitigate the mass write by one calendar event, and anyone who
believes it does will ship migration 024 feeling protected.

PAC-24's own §6 requires only that background jobs must not start *failing*; it
does not require that they not start *acting*, and its §7 rollback analysis does
not mention created calendar events. Its challenge raises this
(`docs/specs/PAC-24.md:848-860`) and leaves it open.

A latent sibling: `ListUsersWithPersonalCalendars`
(`backend/storage/personal_calendars.go:187-202`) has the identical missing
predicate and feeds the 30-minute personal-calendar sync (`backend/main.go:55-79`).
An unattributed record there yields `uuid.Nil`, `SyncAllForUser` rejects it
(`backend/engine/personal_blocker.go:47-50`), and `backend/main.go:69-72` both
logs **and** reports it to Sentry — so that one would emit an error event every
thirty minutes forever. The only writer is `InsertPersonalCalendar`
(`backend/storage/personal_calendars.go:57-64`, the sole
`INSERT INTO personal_calendars` in the tree) and it always supplies an owner;
because `user_id` is `REFERENCES users(id)`
(`backend/storage/migrations/006_auth.up.sql:17`), a caller passing the all-zero
UUID would be rejected by the foreign key rather than stored. So a NULL there can
only come from a row predating 006, or from 018's backfill finding no users.
**That reachability claim is an inference from the writer set, not an
observation**, and it is the reason AC-5 exists rather than a §2 assertion.

### 2.9 Nothing in production re-reads the schedule set when a preference is saved

`Reload`'s own comment says "Safe to call repeatedly (e.g. after a settings PUT)"
(`backend/scheduler/cron.go:34-35`). The only **production** call site is `Start`
(`:73-76`), invoked once from `backend/main.go:50-53`. Nothing calls it after a
settings write.

> v1 of this spec supported that with "a grep for `Reload()` outside `cron.go`
> returns nothing". That is false and contradicted by §2.10 one section later:
> `backend/scheduler/cron_test.go` calls it five times, at `:75, :89, :109, :125,
> :132`. The conclusion is unaffected — tests are not a post-write call site —
> but the stated evidence was wrong and is replaced.

So switching automatic focus scheduling on, or changing its schedule, takes
effect at the next process restart and not before. On a long-lived deployment
that is indefinitely. This is a separate defect from the one the issue reports,
it is not in the register, and it is a plausible reason a user would report
"I turned it on and nothing happened" even on a deployment where the attribution
is correct. OQ-3.

It also has a consequence inside this spec, which v1 missed: **the registration
pass happens once per process.** Any requirement that something be "reported per
pass" is therefore a requirement that it be reported once, at boot. That is what
broke v1's AC-6 (§4).

### 2.10 What tests exist over this today

`backend/scheduler/cron_test.go` is the only coverage of the registration path
and it is reasonable coverage of the happy path:

- `TestFocusCron_Reload_NoUsers` (`:72-79`) — zero settings rows at all, expects
  zero entries. It does **not** cover an unattributed row; the test database has
  no settings rows whatsoever, because `insertDefaultSettings` has not run.
- `TestFocusCron_Reload_PerUserEntries` (`:81-101`) — two enabled users and one
  disabled, expects two entries.
- `TestFocusCron_Reload_InvalidCronSkipped` (`:103-118`).
- `TestFocusCron_Reload_ReplacesOldEntries` (`:120-141`).

Its seed helper always supplies an owner (`:35-48`), so no test in the tree ever
puts an unattributed record in front of this code. There is no test referencing
`ListUsersWithAutoSchedule` directly anywhere
(`backend/storage/settings_test.go` does not mention `user_id`). The e2e seed
leaves `auto_schedule_enabled` at its `FALSE` default
(`e2e/seed/seed.sql:79-90`), so `e2e/tests/focus-time.spec.ts` never exercises
scheduled registration either — only the on-demand run.

**Nothing covers the NULL case, at any layer.** That is why a fresh install can
have a dead feature and a green suite at the same time.

---

## 3. Desired behaviour

Written from the user's point of view, and deliberately naming no file, table
or function — §4 is what gets proven, §2 is where the citations live.

### 3.1 Automatic focus scheduling acts for people, and only for people

When Paceday works out whose calendars it should be writing focus time into, it
considers only preferences that belong to an identifiable person. A set of
preferences that belongs to nobody — which is what a deployment that was
installed before anyone signed up still has — is not a candidate, is never
scheduled, and never produces an attempted run.

### 3.2 A set of preferences that belongs to nobody must not hide the people who do exist

One unattributable record must never cost anybody else their scheduling. The
work of deciding who to schedule for survives encountering one: the people who
are identifiable are scheduled, and the one that is not is left out. This is
already true, by accident, of the current implementation; it becomes a stated
guarantee so that the obvious tightening — insisting on an owner at the point the
record is read — does not quietly turn a skipped record into a
deployment-wide outage.

### 3.3 A deployment whose preferences belong to nobody says so, for as long as it is true

Today the only signals are two log lines about an all-zero identifier — one when
the schedule is registered, one each time it fails — and both read like an
internal glitch rather than "this deployment's automatic scheduling is switched
off for everybody".

After this change, a deployment in that state says so in a form an operator can
act on, and says what the consequence is. Two things about *how* follow from the
system as it is, and both are requirements rather than implementation detail:

- **It must be discoverable while the condition holds, not only at the moment it
  is first noticed.** The decision about whom to schedule for is taken once per
  process lifetime, so a one-off announcement is indistinguishable from the
  glitch it replaces to anyone who was not watching at the time.
- **It must distinguish "nobody has switched this on" — an ordinary, correct
  state — from "this deployment's preferences belong to nobody".** Today neither
  signal does.

It is not a user-facing error. The user has done nothing wrong and can do nothing
about it.

### 3.4 The explanation attached to a shipped change describes what the code does

Where a shipped change justifies a decision by asserting that something
elsewhere performs a check, that assertion is true of the code, or it is removed
and replaced with the real reason. This applies to both assertions of that kind
in the change that introduced ownership of preferences, not only the one the
issue quotes.

### 3.5 Nothing acts on a person's behalf unless that person chose it

This is the half that cannot be built until OQ-1 is answered, and it is stated
as a principle so that whichever answer comes back can be checked against it.

**The class of wrong.** No automatic process may take an action with effects
outside Paceday — writing to a person's calendar, sending them or anyone else a
message — on the strength of a preference that person did not set. The test of
whether a preference belongs to this class is not how important it looks but
whether acting on it wrongly can be undone from inside Paceday. Inheriting
somebody else's working hours is harmless: the worst case is a badly chosen
default the person can correct. Inheriting somebody else's *consent to be acted
for* is not, because by the time they notice, the effect is in a calendar or an
inbox Paceday does not control, and no reversal of the change retracts it.

**This spec's own instance is calendar writes, and it is the one §4 covers.**
v1 drew the principle around calendar writes alone, which had the effect of
blessing a live instance of the same wrong — so the class is stated first and
the known instances named explicitly:

| Instance | Status | Owner |
|---|---|---|
| Automatic focus scheduling writes to the calendars of people who did not enable it | **not live**; arrives with migration 024 if the consent is copied | this spec, AC-8 to AC-11 |
| The morning summary is sent to every user if any one person enables it | **live today** — the fan-out joins every account against the single shared record (`backend/engine/daily_recap.go:121-143`, the query at `:124-128`), fired every minute (`backend/main.go:99-110`) | **PAC-24**, which describes it in full at `docs/specs/PAC-24.md:254-277` and lists it as in scope at `:13-14`. Not this spec — see §5 |

Naming the second one is not scope creep and it does not change this spec's
recommendation. It is the opposite: PAC-24's OQ-1 table already leaves "the
morning-summary destination, channel and enablement" un-copied
(`docs/specs/PAC-24.md:724`), which is exactly the answer this principle gives,
and it is the strongest available argument that the same answer is right for
automatic scheduling. A principle that would have endorsed the recap fan-out
cannot be the yardstick for OQ-1, and §3.4 holds shipped prose to precisely the
standard v1's §3.5 failed.

The consequence for the per-person preferences work is a question with exactly
two defensible answers, and §8 OQ-1 asks for one:

- **Copy the schedule but not the consent.** Each person keeps the schedule the
  deployment was using, so if they later switch automatic scheduling on it
  behaves as it did, but nothing happens until they do. The preference is
  preserved without being acted on.
- **Copy neither.** Everyone starts from the product default on both.

What is not defensible is copying the consent, which is what is specified today.

### 3.6 The person who did switch it on keeps what they had

Whoever the deployment's automatic scheduling was working for before any of this
still has it working afterwards, on the same schedule, with no action required
from them. A fix for "too many people are enrolled" that silently un-enrols the
one person who asked is not a fix.

---

## 4. Acceptance criteria

Each is falsifiable by a test that does not exist today. AC-1, AC-2, AC-3 and
AC-5 fail against `e9d01cc`; AC-4 is buildable now with the qualification noted
against it; AC-7 is checked at review and carries no tag, for the reason given
there. The buckets are §0.1's.

**None of these has been run.** Docker is down on this machine; §4 is a
description of tests to write, not a report of tests observed.

**On the `[unit]` tags.** Every one of them needs a live Postgres. Both queries
take a `*sql.DB` (`backend/storage/settings.go:379`,
`backend/storage/personal_calendars.go:187`) and the only existing harness over
this path builds its fixture through `testdb.Create()` → `sql.Open("pgx", dsn)`
(`backend/scheduler/cron_test.go:13-31`, `backend/internal/testdb/testdb.go:28`).
Under factory §8 these are not tests a spec, contract or plan session can run, so
each is tagged `[unit — needs a live Postgres]` rather than bare `[unit]`, so
that stage 3 is not misled about what the gate costs.

**On the numbering.** AC-6 appears below AC-7 because it moved buckets in this
revision and the numbers were deliberately left alone: the challenge appended
below refers to AC-6, AC-7, AC-9, AC-10 and AC-11 by number, and renumbering
would silently break every one of those references.

**On how many unattributed records.** AC-1 to AC-4 are written for *any number*
of records that belong to nobody, not exactly one. The schema permits more than
one and the consequence of the second differs materially (§2.3), so a fix shaped
for "the NULL row" would satisfy a criterion written in the singular and still
leave a stale schedule alive across reloads.

### Buildable now

AC-1. Given one or more stored sets of focus-scheduling preferences that belong
to no person and have automatic scheduling switched on, when the scheduler works
out whose calendars to schedule, then no schedule is registered at all.
[unit — needs a live Postgres]

> Fails today with one such record: one schedule is registered, keyed by the
> all-zero identifier (§2.3). With two, the stale-entry problem in §2.3 also
> applies, which is why the criterion is written for any number.

AC-2. Given that same unattributed set of preferences stored alongside the
preferences of two identifiable people who have each switched automatic
scheduling on, when the scheduler works out whose calendars to schedule, then
exactly two schedules are registered, one for each of those two people, and
neither is keyed to nobody.  [unit — needs a live Postgres]

> Fails today: three are registered.

AC-3. Given an unattributed set of preferences with automatic scheduling
switched on, when the set of people to schedule for is produced, then the
operation succeeds and the result contains no entry that names nobody.
[unit — needs a live Postgres]

> Fails today on the second clause. The first clause is what distinguishes the
> required fix from the obvious one; see §3.2 and AC-4.

AC-4. Given a store containing any number of unattributed sets of preferences
with automatic scheduling on and one identifiable person's preferences with
automatic scheduling on, when the scheduler works out whose calendars to
schedule, then that person's schedule is registered — that is, the unattributed
records do not prevent the attributed one being served.
[unit — needs a live Postgres]

> Fails today only in the sense that no test asserts it. It is in §4 rather than
> §6 because it is the criterion that an owner-required-at-read-time
> implementation would break, and the one that makes AC-1's fix safe. A reviewer
> should treat AC-1 and AC-4 as a pair.

AC-5. Given an unattributed personal-calendar connection that is switched on,
when the periodic personal-calendar sync works out whose calendars to sync, then
no sync is attempted for an entry that names nobody, and in particular no error
is reported to the error tracker.  [unit — needs a live Postgres]

> Fails today: the all-zero identifier is produced, the sync refuses it, and the
> refusal is reported as an exception every thirty minutes (§2.8). Whether a
> deployment can actually reach this state is an inference I could not confirm
> (§2.8) — which is the argument for the test rather than against it.

AC-7. Given the shipped change that introduced ownership of preferences, when its
stated justification is read, then it asserts no check that is not enforced by
AC-1 and AC-3, for both of the claims it makes.  *(no tag — checked at review)*

> **No test can hold this**, and v1's `[unit]` tag pretended otherwise. It is a
> claim about prose, and stage 3 would have gone looking for a test file. What
> makes it falsifiable is that the corrected prose must assert only predicates
> AC-1 and AC-3 enforce: a reviewer falsifies it by finding a sentence in that
> file claiming a check that no criterion in this list pins. Both of its claims
> are quoted in §2.6, so there is nothing to interpret.

### Blocked on OQ-6 — what an operator can actually see

AC-6. Given a deployment whose focus-scheduling preferences cannot be attributed
to any person, when an operator inspects the running system at any time while
that is the case, then the system reports that automatic focus scheduling is
serving nobody because the preferences have no owner — distinguishably from
"nobody has switched this on", which is a different and unremarkable state.
[unit — needs a live Postgres]

> **Rewritten, and moved out of "buildable now".** v1 required this "once per
> pass in a form an operator sees" and was wrong twice. (a) The registration
> pass happens **once per process** (§2.9), so "once per pass" is "once, at
> boot" — a week-old process shows an operator nothing, and a single startup log
> line is exactly the signal §3.3 rejects. The requirement that survives is that
> the report remain *discoverable for as long as the condition holds*, not that
> it be emitted when the condition is first noticed. (b) "In a form an operator
> sees" cannot be tested until OQ-6 says which channel an operator actually
> reads; today the scheduler writes only to the log and reports nothing to the
> error tracker (`backend/scheduler/cron.go:39, 58, 61, 65, 69` versus
> `backend/main.go:64, 71` in the personal-calendar cron). So this criterion
> names an *observable state* rather than an emission, which is testable either
> way, and it stays blocked on OQ-6 until the channel is chosen. Whatever
> satisfies it must also not regress when OQ-3 is fixed and reloads become
> frequent.

### Blocked on OQ-1 — the decision about which preferences are copied

AC-8. Given a deployment where exactly one person had switched automatic focus
scheduling on, when every person is given their own preferences, then exactly one
person's calendar schedule is registered, and it is that person's.
[unit — needs a live Postgres]

> This is the only criterion an answer to OQ-1 unblocks: it is a statement about
> which rows the migration writes. The three below need the migration itself.

### Blocked on migration 024 existing

AC-9. Given a deployment where exactly one person had switched automatic focus
scheduling on, **and where every other person in the scenario has a working
calendar connection** — so that a write to their calendar would succeed if it
were attempted — when every person is given their own preferences and the chosen
hour then arrives, then focus time is created in that one person's calendar and
no event is created, moved or deleted in anybody else's.  [e2e]

> **The fixture clause is the correction, not decoration.** There is a third
> gate after the identifier and the preferences: a person with no stored calendar
> credential is refused before anything is written (§2.8). v1's wording would
> therefore have passed against a fixture whose other users had never connected a
> calendar — proving only that Paceday cannot write to calendars it cannot reach,
> which was never in doubt. Stated this way the negative clause is proven against
> users whose calendars Paceday *can* reach.
>
> This is the criterion the issue's second half exists for, and the one that most
> needs a real run: it is the difference between an argument about a migration
> and a demonstration of what the migration does. It cannot be run here (§4
> preamble) and it should not be accepted on reading.

AC-10. Given a deployment where the single shared schedule was not the product
default, when every person is given their own preferences, then a person who had
never switched automatic scheduling on finds, on first opening their settings,
the schedule the deployment was using and automatic scheduling switched
off — and switching it on then schedules at that time without further
configuration.  [e2e]

> Presupposes both the migration and OQ-1's first answer. If OQ-1 comes back
> "copy neither", this criterion is withdrawn and replaced by its mirror: the
> person finds the product default on both.

AC-11. Given the person for whom automatic focus scheduling was already working,
when every person is given their own preferences, then it still works, on the
same schedule, with no action from them.  [e2e]

> Presupposes the migration. v1 did not say so and AC-10 did; both do now, so the
> stage-1 gate is not recorded as passed over criteria that cannot yet be
> written.

---

## 5. Explicitly out of scope

- **Making the HTTP "run my focus time now" request succeed for a person who has
  no preferences of their own** — *deferred conditionally, not disowned.* It is
  the same root cause (§2.7) and the fix is the same fix: a per-person
  preferences record, which is PAC-24's entire subject and which this spec must
  not pre-empt. But v1 wrote this bullet as "PAC-24 owns it, specifies it and
  has an acceptance criterion for it", and PAC-24 does not (§2.7). So the
  deferral is stated with its conditions:

  **This spec defers the HTTP failure to PAC-24 if and only if** (a) PAC-24's
  §2.4 states the HTTP surface, not only the engine call sites, and (b) its AC-3
  covers the HTTP path as well as the background one — which is exactly what
  PAC-24's own unresolved challenge finding 1 asks of it
  (`docs/specs/PAC-24.md:819-820`). **If PAC-24 reaches `accepted` without
  both**, the symptom is owned by nobody and this spec is the wrong place to
  discover that, so OQ-7 raises it now with an owner. What this spec will *not*
  do under any answer is change that operation's response shape without the
  five register items in the header being resolved first.

- **Giving anybody their own preferences.** Same reason. This spec makes the
  scheduler honest about who it can serve; it does not change who that is.

- **The morning summary's fan-out to every user** (§3.5). It is a live instance
  of the same class of wrong as the hazard this spec exists to prevent, and it
  needs no migration to be dangerous. It is excluded because PAC-24 already
  describes it in full and has it in scope
  (`docs/specs/PAC-24.md:254-277`, `:13-14`) — an existing owner, unlike the HTTP
  symptom above. Named rather than omitted so that §3.5's principle cannot be
  read as blessing it.

- **Requiring an owner on stored preferences at the schema level.** It is the
  obvious companion to this fix and it cannot be done until nothing writes an
  unattributed record, which again is PAC-24. Argued rather than omitted: doing
  it now would break the save path on every deployment immediately.

- **The shared-preferences defects themselves** — one person's save overwriting
  another's, the provider key being readable, a partial save erasing the rest
  (register items API-002, API-003, API-004). Named in the header as inherited
  and not resolved.

- **Making a preference change take effect without a restart** (§2.9). It is a
  real defect, it is adjacent, and it is a behaviour change to a background
  process that deserves its own acceptance criteria and its own rollback story
  rather than being smuggled in here. OQ-3 files it.

- **Any change to what the on-demand run returns, or to its failure body.** See
  the header: **five** register items sit on that operation and this spec does
  not open them. If OQ-7 is answered by bringing the HTTP failure here, this
  bullet is withdrawn and those five become inherited — which is the cost that
  answer carries, stated so it is weighed rather than discovered.

- **Whether automatic scheduling should exist in its current form at all** —
  whether a person should be able to see, before it runs, what it is about to put
  in their calendar. A fair question raised by §3.5 and not this issue's.

- **Retro-removing focus blocks already created by an unwanted automatic run.**
  If the hazard in §2.8 ever lands, this spec offers no remedy for it, which is
  the whole argument for settling OQ-1 first.

---

## 6. What must not change

- **The person for whom automatic scheduling works today keeps it.** On an
  upgrade deployment that is the earliest-created account. Guarded by AC-4 now
  and AC-11 after PAC-24. No existing test covers it: the only tests over this
  path seed owners explicitly (§2.10), so none of them is in a state where the
  fix could break them.

- **A deployment where nobody has switched automatic scheduling on continues to
  register nothing.** Protected today by `TestFocusCron_Reload_NoUsers`
  (`backend/scheduler/cron_test.go:72-79`), which is the one existing test that
  constrains this fix at all.

- **A record whose schedule expression is unusable is still skipped
  individually, not fatally.** Protected by
  `TestFocusCron_Reload_InvalidCronSkipped` (`:103-118`).

- **Re-deciding who to schedule for remains repeatable.** Protected by
  `TestFocusCron_Reload_ReplacesOldEntries` (`:120-141`).

- **No layer stops refusing to act for an unidentifiable person.** The
  `uuid.Nil` guards at `backend/engine/focus_time.go:70-73`,
  `backend/engine/personal_blocker.go:47-50`,
  `backend/storage/settings.go:420-423` and
  `backend/storage/personal_calendars.go:163-166` are the reason this defect is
  noise and not damage (§2.3). Filtering earlier must not be taken as licence to
  remove them. **No test protects those guards** — that is a finding in itself,
  and AC-1/AC-3/AC-5 only cover the new outer filter, not these.

- **The on-demand run keeps behaving exactly as it does**, including its
  `text/plain` 500. Out of scope per §5; stated here because a reader may expect
  this spec to tidy it.

- **Nothing about the stored shape of a preference changes.** No migration is
  needed for the buildable half of this spec — only a comment correction in an
  existing one (AC-7). That is safe to do despite migrations being immutable
  once shipped (`docs/factory/PAC-24-plan.md:54`) because the runner stores no
  checksum: `runMigrations` uses `iofs` plus the golang-migrate postgres driver
  (`backend/storage/db.go:31-40`), which records a version and a dirty flag and
  nothing about file contents, so a comment edit cannot make an applied
  migration re-run or fail. v1 asserted "prose and does not re-run" without
  showing it; §3.4 demands exactly the opposite standard of such claims.

- **No consumer can observe either change.** `storage.ListUsersWithAutoSchedule`
  has exactly one caller, `backend/scheduler/cron.go:37`;
  `storage.ListUsersWithPersonalCalendars` has exactly one,
  `backend/main.go:61`. No handler, no MCP tool and no generated server
  interface touches either, so nothing crosses the HTTP boundary and
  `smart-calendar-flow` cannot see the difference. This is consistent with §0.2's
  argument that the shippable half sits below the boundary, and it means the
  cross-repo ordering rule in factory §1 does not apply to this work at all. The
  one behaviour a consumer *could* notice is the one AC-4 exists to protect.

---

## 7. Rollback

**The buildable half (AC-1 to AC-5 and AC-7) reverts cleanly and loses nothing.**

There is no migration. The change is a predicate in a query, a tolerance in how
a row is read, a comment correction, and tests — and, if OQ-6 is answered in time
for AC-6 to ship with them, a report. Reverting restores the current behaviour
exactly: a schedule registered for nobody at boot, refused at each firing, logged
both times. No stored data differs before or after, so there is nothing to lose
and no down migration to write, and no consumer can observe the difference in
either direction (§6).

The one asymmetry worth stating: on a fresh install, reverting re-introduces the
misleading log lines but does not re-break anything, because nothing was working
before the change either. On an upgrade install, reverting is a no-op in
observable behaviour (§2.5).

**The blocked half has no rollback, which is the point.**

If migration 024 copies the enablement switch and the scheduled hour passes
before anyone notices, the resulting focus blocks exist in users' real
calendars — in Google, not only in Paceday's tables. Reversing the migration
restores the preference rows; it cannot retract a calendar invitation that has
already been created, and in some setups already notified. There is no down
migration for that and this spec does not propose one, because a cleanup that
deletes events out of people's calendars on the strength of a guess about which
ones it created is a second incident, not a remedy.

That asymmetry — a trivially reversible fix on one side, an irreversible side
effect on the other — is the argument for OQ-1 being answered before PAC-24's
migration is written rather than reviewed after.

---

## 8. Open questions

**OQ-1 — When every person is given their own preferences, is the *consent* to
automatic focus scheduling copied to them, or only the schedule?**
*Owner: human (product). Blocks PAC-24 stage 3 — specifically, it must be
answered before `backend/storage/migrations/024_settings_per_user.up.sql`
(`docs/factory/PAC-24-plan.md:160`) is written. This is the critical path: PAC-24
is otherwise specced, contracted and planned.*

PAC-24 §8 OQ-1 currently puts "Compression and auto-schedule enablement and
schedule" in the copied column (`docs/specs/PAC-24.md:724`). §3.5 argues that
copying the enablement is the one item in that table that cannot be undone, and
that the schedule can be copied safely on its own. Three candidate answers:

| Answer | Consequence |
|---|---|
| Copy the schedule, not the enablement *(this spec's recommendation)* | Nobody's calendar is written to without their say. The deployment's chosen hour is preserved, so the preference is not lost. The one person who had it on must switch it on again — which breaks AC-11 unless the migration treats the record's original owner as a special case, and it should. |
| Copy neither | Simplest migration. Loses a real preference for no benefit over the first option. |
| Copy both *(as currently specified)* | §2.8. Not recoverable. |

The table cell also contains compression, and the cell should still be split —
but for the opposite reason to the one v1 of this spec gave. **Compression is not
a hazard: nothing reads that flag.** Its only occurrences in the backend are
serialisation (`backend/storage/settings.go:167, 216, 237, 277, 310, 338, 431,
451`, plus `backend/storage/settings_test.go:65, 98-99`); there is no compression
cron (`backend/main.go:50-132` wires focus, personal-calendar, auto-decline,
recap and manager, and nothing matching `compress` appears in `backend/main.go`
or `backend/scheduler/`); and the frontend only ever calls compression when a
user asks for it (`smart-calendar-flow/src/api/client.ts:987, 992`). So copying
it to everybody cannot move anybody's meetings. v1 called it "arguably worse"
and invited the decision to be delayed over a non-issue. The cell should be split
because **auto-schedule enablement is the only half of it that any background
process acts on** — which makes OQ-1 a narrower question than it looks, and
cheaper to answer.

Resolve by: a decision, recorded on PAC-24 against its OQ-1, before stage 3
resumes. If the answer is "we cannot tell", the safe reading is the first row.

**OQ-2 — Which of the three states is each live deployment in, how many
unattributed preference records does it hold, and has automatic scheduling ever
been switched on there?**
*Owner: whoever operates the deployment. Does not block the fix; decides its
urgency, whether PAC-24's migration needs a pre-flight check, and how many
people AC-9's hazard would reach.*

§2.4 shows **three** states, not two, and which one a deployment is in depends on
its history — whether anyone had signed up when migration 018 ran *and* whether
the product has ever served a settings read. v1 asked this as a yes/no about
whether the record has an owner, which cannot distinguish "no record yet" from
"a record with no owner", and the two have different consequences.

The diagnostic must therefore report **counts, not a boolean**: how many
preference records exist, how many of those have no owner, and whether automatic
scheduling is switched on for any of them. The count matters because two
unattributed records produce a stale schedule that outlives this spec's fix
until the process restarts (§2.3), and that is a case I could not reach through
any product writer but could not rule out from the schema either.

Three things turn on the answer. If there is an unattributed record *and*
scheduling is on, then automatic scheduling has been silently dead there and
somebody has been waiting for it. That is also the deployment where migration 024
would enrol everybody. And the size of that enrolment is "everyone with a
connected calendar" (§2.8), which only this operator's data can quantify.

**OQ-3 — Should changing an automatic-scheduling preference take effect without a
restart, and is that this issue or another one?**
*Owner: human (triage). Does not block. My proposal: a separate issue.*

§2.9: nothing re-reads the schedule set after a save, despite a comment saying
it is safe to. A user who switches automatic scheduling on gets nothing until the
process restarts. I have kept it out of §4 because it is a behaviour change to a
background process with its own failure modes (what happens when a save races a
firing run?) and it would make this spec two specs. But it is a strong candidate
for the real cause of any "I turned it on and nothing happened" report that is
*not* explained by §2.4, and leaving it unfiled means the fix in this spec could
be shipped and still not satisfy the user who reported it.

**OQ-4 — Who corrects PAC-24's challenge record, and does a wrong mechanism in a
challenge need correcting at all once its conclusion has been accepted?**
*Owner: human (factory process). Does not block this spec.*

`docs/specs/PAC-24.md:835-847` states the mechanism this spec disproves in §2.2,
and PAC-49's own description inherits it from there. The conclusion both drew —
that 018's comment is false — is correct and is why this issue exists. But the
factory's §8 says artifacts must stand alone, and an artifact that explains a
real defect with a mechanism that does not happen will mislead the next reader
exactly as 018's comment did. I am not editing another stage's artifact. My
proposal: an erratum note appended to PAC-24's challenge section, in the style
`docs/factory/README.md` footnote 1 already uses for a superseded decision.

**OQ-5 — Should background jobs be an audited surface?**
*Owner: human (factory process). Does not block.*

`docs/factory/api-audit.md` audited the HTTP boundary and found 85-plus items.
It contains the string "cron" zero times. This defect — a deployment-wide dead
feature and a pending mass write to users' calendars — is in none of those items,
because nothing in it crosses that boundary.

Five cron-driven processes are wired at `backend/main.go:50-132`. What is known
about them, stated precisely, because v1 of this spec overstated its own
ignorance here:

| Process | Status |
|---|---|
| Focus scheduling | this spec, §2.1-§2.4 |
| Personal-calendar sync | same missing predicate, latently (§2.8, AC-5) |
| Daily recap | **already known to fan out to every user** off the shared unattributed record (§3.5), and written down in PAC-24 §2.6 (`docs/specs/PAC-24.md:254-277`) — a file this spec quotes throughout. v1 claimed not to know this |
| Auto-decline | its listing *does* carry the owner predicate (`backend/storage/settings.go:402`), so it does not have this defect. Whether it has others, I did not check |
| Manager weekly | not examined |

So three of five are accounted for and two are not, which is a weaker claim to
ignorance than v1 made and a better argument for the audit: the gap is not that
nobody knows, it is that what is known is scattered across spec documents instead
of a register, which is how PAC-49 came to be filed as a surprise about something
PAC-24 had already written down.

**OQ-6 — What does an operator actually read, and where should AC-6's report
live?**
*Owner: whoever operates the deployment. **Blocks AC-6**, which is why AC-6 is
in its own bucket in §0.1 rather than under "buildable now" as v1 had it.*

Sentry is initialised before the database is opened or any cron starts
(`initSentry()` at `backend/main.go:25`, under the comment at `:21-24` explaining
why the order matters), and the personal-calendar cron reports failures to it
(`:64, 71`). The focus scheduler reports nothing to it. It writes five log lines
and no more (`backend/scheduler/cron.go:39, 58, 61, 65, 69`), of which `:69` is
the one that matters here: it is emitted at **registration** time and today it
prints the all-zero UUID at boot. So both the registration-time and the
firing-time traces exist, and neither distinguishes the two states AC-6 needs
distinguished — v1 said the trace was only at fire time, which was wrong, and
cited three of the five lines.

"In a form an operator sees" therefore has at least two meanings and I do not
know which is reachable in practice: whether this deployment's logs are
aggregated anywhere somebody reads, or whether the error tracker is the only
channel that gets attention. A test cannot tell `log.Printf` from a Sentry event
without that answer, which is why AC-6 names an observable state rather than an
emission and still waits on this.

**OQ-7 — Who owns the HTTP failure that every user on a fresh install gets from
"run my focus time now"?**
*Owner: human (factory process / triage), together with whoever resumes PAC-24.
Blocks nothing in §4, but it must be settled before PAC-24 reaches `accepted`,
or the symptom is orphaned at that moment.*

§2.7 establishes that PAC-24 covers the background path and not the HTTP one,
and that v1 of this spec handed it over on the strength of a commitment PAC-24
does not make. The symptom is live, user-visible, and reachable from two
consumers (the browser and `mcp/tools.go:143-146`). Three ways to settle it, in
my order of preference:

1. **PAC-24 takes it**, by resolving its own challenge finding 1
   (`docs/specs/PAC-24.md:819-820`) — stating the HTTP surface in its §2.4 and
   extending its AC-3 to the HTTP path. This is the cheapest option because
   PAC-24 is already building the fix; it needs only to say that it is.
2. **A separate issue takes it**, scoped to making that failure honest rather
   than making it succeed — which is a change to the operation's response and
   therefore inherits the five register items in the header.
3. **This spec takes it.** I recommend against it: the fix is a per-person
   preferences record, which is PAC-24's subject, and taking it here would mean
   either duplicating that work or specifying a better error message for a
   condition that is about to stop existing.

What is not acceptable is the v1 position, which was option 1 asserted without
anyone having agreed to it.

**OQ-8 — Should `storage.ListPersonalCalendars` be deleted?**
*Owner: human (triage). Does not block. Proposal: a separate issue, not this
one.*

It has no caller anywhere (`backend/storage/personal_calendars.go:32`); the only
other occurrences of the name are the generated HTTP operation in
`backend/api/gen/`, which is a different thing. v1 of this spec cited it as
evidence in §2.6 as though it were live. It is dead code that returns every
user's calendars unfiltered, which is a liability if anyone ever wires it up, and
it is adjacent enough to this work to be noticed and far enough from it not to
belong here.

---

## Challenge — 2026-10-04 (stage-1 challenger, local session, Docker down)

**Verdict**: accept-with-changes

The load-bearing correction holds. I read the whole Scan chain at the pinned
versions and §2.2 is right: a NULL `uuid` column scans to `uuid.Nil` with no
error, the listing succeeds, and the issue's stated mechanism ("fails
`rows.Scan` … registers no cron entries at all") is false. §2 is accurate work —
I re-verified every `file:line` in it and found two wrong, one that does not say
what the spec claims, one undercount, and a handful off by one or two lines. The
changes below are to §2.7/§5 (whose out-of-scope argument rests on a claim about
PAC-24 that PAC-24 does not make), to AC-6 and AC-9 (neither is falsifiable as
written, for different reasons), and to §3.5 (the principle the spec offers as
the yardstick for the decision it says is on the critical path is drawn narrowly
enough to bless a live instance of the same wrong).

**Nothing was executed here either.** Docker is down on this machine, so no
test, build, migration or request was run for this challenge. Every claim below
is from reading the repository at `e9d01cc` (working tree clean apart from this
spec file), the Go distribution at `C:\Program Files\Go` on go1.25.5, and the
module cache at the versions `backend/go.mod` pins. The Linear issue was fetched
directly; `list_comments` returned a 502, so I could not independently confirm
the spec's "there are none", and I have not relied on it.

### Blocking

1. **§2.7 and §5 hand the only user-visible symptom to PAC-24, and PAC-24 does
   not have it.** §2.7 says "**PAC-24 owns the HTTP face** — its §2.4 describes
   it and its AC-3 covers it (`docs/specs/PAC-24.md:207-229`, `:491-494`)", and
   §5's first out-of-scope bullet rests entirely on that: "PAC-24 owns it,
   specifies it and has an acceptance criterion for it." Neither citation says
   that. PAC-24 §2.4 (`docs/specs/PAC-24.md:207-228`) is titled "Nothing ever
   creates a per-user settings row" and names only engine call sites
   (`focus_time.go:76`, `auto_decline.go:66`, `habits.go:70`, `:96`) and
   `focus_time.go:82-84`; it contains no HTTP route, no handler and no status
   code. PAC-24 AC-3 (`:491-493`) reads "when a **background job** that needs
   that user's settings runs for them" — explicitly the background path, not the
   HTTP one. The only text in PAC-24 that names `POST /api/focus/run` is inside
   **its own challenge**, Blocking finding 1 (`:819-820`), whose resolution
   clause asks the author to add exactly what PAC-49 already assumes is there:
   "§2.4 states the HTTP surface … and AC-3 covers the HTTP path as well as the
   background one (it is marked `[unit]` today)." PAC-24 is still
   `Status: draft` (`:4`) and that finding is unresolved. So as the register
   stands, the 500 that every user on a fresh install gets from "schedule my
   focus time now" is owned by nobody — this spec declines it on the strength of
   a commitment that does not exist, and the next reader will find the
   declension already endorsed, which is the same failure mode as 018's comment.
   *Resolves it*: §2.7 and §5 state what PAC-24 actually covers today (the
   background path, AC-3) and what it does not (the HTTP path, raised only in
   its unresolved challenge finding 1), and §5's bullet either takes the HTTP
   symptom or defers it to a named, existing owner — a new open question against
   PAC-24's challenge finding 1 would do, alongside OQ-4, which already proposes
   an erratum to the same section.

2. **AC-6 is not buildable now, and as written it is satisfied by the signal
   §3.3 rejects.** Two independent problems, both visible in the spec's own
   text. (a) `Reload` runs **once per process** — §2.9 establishes this and the
   spec defers it to OQ-3. So "reported once per pass" is "reported once, at
   boot, and never again". An operator who arrives a week after startup sees
   nothing. §3.3 objects to today's state because "the only signal is a log line
   about an all-zero identifier, which reads like an internal glitch"; a single
   startup-time log line about an unattributable record reads much the same, and
   a conforming implementation could emit exactly that. The deferral in §5
   ("Making a preference change take effect without a restart … deserves its own
   acceptance criteria") is therefore load-bearing for a criterion the spec
   keeps, and §5 does not notice. (b) OQ-6 concedes that "in a form an operator
   sees" has at least two meanings and that the spec does not know which is
   reachable. A test cannot distinguish `log.Printf` from a Sentry event without
   that answer, and `Reload` returns nothing and reports nothing to Sentry today
   (`backend/scheduler/cron.go:36-71`; the only Sentry call sites near this are
   `backend/main.go:64, 71`, in the personal-calendar cron). AC-6 is listed
   under "Buildable now" and the §4 preamble asserts it "fails against
   `e9d01cc`"; by the spec's own OQ-6 it cannot yet be written at all.
   *Resolves it*: either move AC-6 to a third bucket blocked on OQ-6, or restate
   it against an observable a test can name, and state its recurrence in a way
   that survives `Reload` being called once — the requirement the spec is
   missing is that the report remain discoverable for as long as the condition
   holds, not that it be emitted at the moment the condition is first noticed.

3. **AC-9 can pass without the hazard being absent, because §2.8 misses a
   gate.** §2.8 enumerates what the post-024 run would pass — "`RunForUser` has
   no objection: the identifier is real and the preferences exist" — and
   concludes "Focus blocks are created in every user's live calendar at the
   owner's chosen hour." There is a further gate after those two. `RunForUser`
   calls `e.calClient(ctx)` (`backend/engine/focus_time.go:84-87`) before it
   writes anything, which reaches `newCalOps`
   (`backend/engine/cal_iface.go:53-65`): `token, err :=
   auth.LoadUserToken(db, userID); if err != nil || token == nil { return nil,
   fmt.Errorf("not authenticated") }`. A user who has never connected their own
   calendar fails there, before `processDay` is reached. The blast radius of
   migration 024 is therefore "every user who has connected a calendar", not
   "every user" — which is still every user who matters, so the hazard survives,
   but the *criterion* does not. AC-9 says "no event is created, moved or
   deleted in any other person's calendar": in a fixture where the other persons
   have no connected calendar, that clause passes for the wrong reason and
   proves nothing, and AC-9 is the one criterion the spec says "most needs a real
   run". §1's "Paceday writes blocks into colleagues' real calendars" has the
   same gap.
   *Resolves it*: §2.8 states the credential gate alongside the other two, and
   AC-9 requires that the non-enrolled persons in the scenario be ones for whom
   a write *would* otherwise succeed — i.e. that the negative clause is proven
   against a user whose calendar Paceday can reach.

4. **§3.5's principle is narrower than the wrong it names, and excludes a live
   instance of it that this spec's own sources already document.** §3.5 is the
   only yardstick the spec offers for OQ-1, the decision it calls the critical
   path: "No background process may create, move or delete events in a person's
   calendar on the strength of a preference that person did not set." The daily
   recap already does the same wrong with a different artefact.
   `DailyRecapService.RunAll` (`backend/engine/daily_recap.go:123-143`) is
   `SELECT u.id, u.name FROM users u INNER JOIN settings st ON st.id = 1 WHERE
   st.recap_enabled = true` — an uncorrelated join of every user against the
   shared, unattributed row, fired every minute from `backend/main.go:99-110`.
   If anyone switches the recap on, every user is DMed a summary at a time and
   to a destination somebody else chose. This needs no migration and no missing
   predicate: it is live today. It is not a discovery of mine — PAC-24 §2.6
   (`docs/specs/PAC-24.md:254-277`) describes it in full, and PAC-24's header
   lists it as in scope (`:13-14`). PAC-49 cites PAC-24 throughout and reads its
   §2.4, §6, §7, §8 and its challenge, yet OQ-5 says only "I cannot tell from
   here how many of the other three have similar gaps". One of the three is
   already written down in a file the spec quotes. The consequence is not that
   the spec is wrong about focus scheduling; it is that §3.5, as drafted, would
   have endorsed the recap fan-out, and §3.4 holds shipped prose to exactly the
   opposite standard.
   *Resolves it*: §3.5 either states the class of wrong it governs so that the
   recap falls inside it, or says in terms that it is confined to calendar
   writes and names the recap as a known instance it deliberately excludes and
   who owns it (PAC-24 §2.6); and OQ-5 stops claiming ignorance about a cron
   whose defect is recorded in a cited artifact. The spec's recommendation on
   OQ-1 is unaffected — PAC-24's OQ-1 table already leaves "the morning-summary
   destination, channel and enablement" un-copied (`docs/specs/PAC-24.md:724`),
   which is the answer §3.5 would give if it reached that far.

### Non-blocking

1. **§2.4's fresh-install causation is imprecise, and OQ-2's diagnostic inherits
   the imprecision.** §2.4 says "Fresh install — migrations run before the first
   account exists. The guard is false. `settings.user_id` is NULL". On a
   genuinely fresh install the guard is irrelevant: `001_initial.up.sql:1-24`
   creates `settings` empty and inserts nothing, and migrations run inside
   `storage.Open` (`backend/storage/db.go:17-28`) before the HTTP server is
   listening, so 018's `UPDATE settings` touches **zero rows** whatever the
   guard says. The unattributed row is created later, by the running product:
   `GetSettings` → `sql.ErrNoRows` → `insertDefaultSettings`
   (`backend/storage/settings.go:249-251, 261-266`). The conclusion — NULL
   forever, because nothing else ever writes it — is right, and I confirmed the
   writer set independently (`INSERT INTO settings` across the tree returns only
   those two helpers, two test fixtures and the e2e seed; `grep -rn "UPDATE
   settings"` adds `microsoft_oauth.go:57`, `conferencing.go:6`,
   `personal_calendars.go:156`, `settings.go:505` and migrations 002 and 020,
   none of which touches `user_id`). But the mechanism matters for OQ-2: its
   query must distinguish three states, not two — no settings row at all, a row
   with no owner, and an attributed row — and §2.4's "the only record the listing
   can ever return is the unattributed one" presupposes a row that a deployment
   which has never served a settings read does not have.
   `TestFocusCron_Reload_NoUsers` is in exactly that third state, as §2.10
   correctly notes.

2. **Every `[unit]` label on AC-1 to AC-6 needs a live Postgres, which this
   environment class does not have.** `ListUsersWithAutoSchedule` and
   `ListUsersWithPersonalCalendars` both take a `*sql.DB`
   (`backend/storage/settings.go:379`,
   `backend/storage/personal_calendars.go:187`), and the only existing harness
   over this path builds its fixture through `testdb.Create()` →
   `sql.Open("pgx", dsn)` (`backend/scheduler/cron_test.go:13-31`,
   `backend/internal/testdb/testdb.go:28`). Under factory §8 these are not tests
   a spec/contract/plan session can run, and calling them `[unit]` will mislead
   stage 3 about what the gate costs. PAC-22's challenge made the same point
   about mislabelled criteria; it is cosmetic in the same way and worth the same
   one line.

3. **The §4 heading "Blocked on OQ-1" is wrong for AC-9 to AC-11.** §0.1 says
   "AC-8 to AC-11 are blocked on a product decision (OQ-1)". They are blocked on
   migration 024 **existing** — AC-9, AC-10 and AC-11 all begin "when every
   person is given their own preferences", which is PAC-24's deliverable, not a
   decision. OQ-1 is one input to that migration, not the whole of what gates
   these four. Worth correcting because the sequencing argument in §0.1 and §7
   is the spec's reason for being one spec, and a reader could conclude that an
   answer to OQ-1 unblocks four criteria when it unblocks one (AC-8, which is
   about which rows the migration writes).

4. **OQ-1's compression aside flags a hazard that does not exist, and invites
   the decision to be delayed over it.** OQ-1 says compression "moves existing
   meetings rather than creating new events, which is arguably worse". Nothing
   in either repo reads `compression_enabled`. The only occurrences in the
   backend are serialisation — `backend/storage/settings.go:167` (the struct
   field), `:216`, `:237`, `:277`, `:310`, `:338`, `:431`, `:451` — plus
   `backend/storage/settings_test.go:65, 98-99`; there is no compression cron
   (`backend/main.go:50-132` wires focus, personal-calendar, auto-decline, recap
   and manager, and a case-insensitive grep for `compress` over
   `backend/main.go` and `backend/scheduler/` returns nothing); and the frontend
   never reads the flag either — its only compression calls are the
   user-initiated `POST /schedule/compress` and `/compress/apply`
   (`smart-calendar-flow/src/api/client.ts:987, 992`). Copying
   `compression_enabled` to every user therefore cannot move anybody's meetings
   without that person asking. The cell still deserves splitting, but for the
   opposite reason: auto-schedule enablement is the only half of it that any
   background process acts on.

5. **§2.3's "no other record is affected" and §4's singular phrasing both assume
   at most one unattributed row, and nothing enforces that.** `fc.entryIDs` is
   `map[uuid.UUID]cron.EntryID` (`backend/scheduler/cron.go:22`), written at
   `:68` and cleared by iterating itself at `:44-47`. 018 adds
   `UNIQUE (user_id)` (`018_per_user_settings_and_calendars.up.sql:21-22`) and
   says in terms that "PostgreSQL UNIQUE allows multiple NULLs, so this
   constraint coexists with the legacy 'no user yet' default row" (`:19-20`).
   Two unattributed rows would both key `uuid.Nil`: the second overwrites the
   first in the map, the first's `cron.EntryID` is never passed to
   `fc.cron.Remove`, and that entry outlives every later `Reload` — including one
   after this spec's fix lands, until the process restarts. I could not reach
   that state through any product writer (`insertDefaultSettings` is `id=1 ON
   CONFLICT (id) DO NOTHING`, and `settings.user_id` is `REFERENCES users(id)`
   (`006_auth.up.sql:15`) so an all-zero UUID cannot be stored either), which is
   the same class of inference §2.8 honestly marks as unconfirmed. The ask is not
   a fix: it is that AC-1 to AC-4 say whether the guarantee is "one unattributed
   record" or "any number of them", since the schema permits the latter and the
   fix's shape differs.

6. **§2.7's "the only one a user can see" misses a second consumer of the same
   operation, in this repo.** The MCP server exposes
   `clockwise_run_focus_engine`, which posts to `/api/focus/run`
   (`mcp/tools.go:143-146`), so an agent driving that tool receives the same raw
   Go error string as a tool result. Nothing breaks — this spec changes nothing
   on that path — but the completeness claim is wrong, and the MCP surface is in
   the factory's own ownership table (README §1).

7. **§6's "prose and does not re-run" is asserted, not established, about a file
   the spec itself calls immutable.** AC-7 requires editing the comments in a
   shipped migration, and §6 justifies it with "only a comment correction in an
   existing one, which is prose and does not re-run", while the spec elsewhere
   cites `docs/factory/PAC-24-plan.md:54` for migrations being immutable once
   shipped. The two are reconcilable only if the migration runner does not verify
   file contents. It does not — `runMigrations` uses `iofs` plus the
   golang-migrate postgres driver (`backend/storage/db.go:31-40`), which records
   version and dirty state and no checksum — but the spec should say so rather
   than leave a reader to assume it, because "editing a shipped migration is fine
   when it is only a comment" is precisely the kind of claim §3.4 demands be true
   of the code.

### Criteria I could not falsify

- **AC-6** — blocking 2. No test can distinguish "a form an operator sees" from
  `log.Printf` until OQ-6 is answered, and "once per pass" is satisfiable by a
  single boot-time emission because `Reload` is called once per process.
- **AC-7** — not falsifiable by any test, which the spec concedes ("the one
  criterion in this list that no test can hold on its own"). The honesty is
  right; the `[unit]` tag is not, and stage 3 will look for a test file. Restate
  it as the requirement that the corrected prose assert only predicates AC-1 and
  AC-3 enforce, checked at review, and drop the tag.
- **AC-9** — blocking 3. Falsifiable in substance, but as written the negative
  clause passes vacuously against a fixture whose other users have no connected
  calendar.
- **AC-10, AC-11** — candidate criteria, not criteria: both presuppose migration
  024 and AC-10 presupposes one particular answer to OQ-1. AC-10 says so itself;
  AC-11 does not. Noted so the stage-1 gate is not recorded as passed over them.

Every other criterion I could write the failing test for, and the two that
matter most I checked in detail. **AC-1**: against a fresh test database (which,
per §2.10, has no `settings` row at all), `INSERT INTO settings
(auto_schedule_enabled, auto_schedule_cron) VALUES (TRUE, '@daily')` leaving
`user_id` NULL, then `Reload`, then assert `len(fc.entryIDs) == 0`; fails today
with 1, keyed `uuid.Nil`, because of exactly the chain §2.2 describes.
**AC-5**: `INSERT INTO personal_calendars (provider, name, url,
credentials_json, enabled) VALUES (…, TRUE)` with `user_id` NULL, then
`ListUsersWithPersonalCalendars`; fails today returning one `uuid.Nil`, and
`backend/main.go:69-72` turns the resulting `SyncAllForUser` refusal
(`backend/engine/personal_blocker.go:47-50`) into a `sentry.CaptureException`
every thirty minutes. I also found the half of §2.8 the spec marked as an
unverified inference to be stronger than it claims: the sole writer is
`InsertPersonalCalendar` (`backend/storage/personal_calendars.go:57-64`, the only
`INSERT INTO personal_calendars` in the tree), and because `user_id` is
`REFERENCES users(id)`, a caller passing `uuid.Nil` would be rejected by the
foreign key rather than storing an all-zero owner — so a NULL there can only come
from a row predating 006 or from 018's backfill finding no users. AC-5 is still
the right call.

### Citations I checked and found wrong

I re-verified every `file:line` in §0, §2, §6, §7 and §8 against the working
tree, and the three out-of-repo citations against `C:\Program Files\Go`
(go1.25.5) and the module cache. Two are wrong, one does not support what it is
cited for, one undercounts, and four are off by one or two lines.

- **§2.9: "a grep for `Reload()` outside `cron.go` returns nothing"** — false.
  `backend/scheduler/cron_test.go` calls it five times, at `:75, :89, :109,
  :125, :132`, and §2.10 of this same spec cites four of those tests by name. The
  conclusion survives intact and is the one that matters — the only
  **production** call site is `Start` (`cron.go:73-76`) from
  `backend/main.go:50-53`, and nothing calls `Reload` after a settings write —
  but the stated evidence is wrong and the spec contradicts itself one section
  later.
- **§2.7: "PAC-24 owns the HTTP face — its §2.4 describes it and its AC-3 covers
  it (`docs/specs/PAC-24.md:207-229`, `:491-494`)"** — neither range says that.
  `:207-228` is "Nothing ever creates a per-user settings row" and names no HTTP
  route; `:491-493` is AC-3, explicitly "when a **background job** … runs for
  them". The HTTP face appears in PAC-24 only at `:819-820`, inside its own
  unresolved challenge finding. Blocking 1.
- **§2.6: "`ListPersonalCalendars` returns every row unfiltered (`:32`)"** — true
  of the code and wrong as evidence. `storage.ListPersonalCalendars` has **no
  caller anywhere** (a grep for `storage.ListPersonalCalendars(` over all `.go`
  files returns nothing; the only other hits for the name are the generated
  `ListPersonalCalendars` HTTP operation in `backend/api/gen/paceday.gen.go`),
  and it is not a per-user query, so it cannot bear on a comment about what "the
  per-user query" filters. §2.6's "singular about a set of three" is really a set
  of two: `ListPersonalCalendarsByUser` does filter and guards `uuid.Nil`
  (`:163-168`), and `ListUsersWithPersonalCalendars` (`:187-202`) is the genuine
  defect. The finding stands on that one; stated as three it is padded, and the
  dead function is worth filing separately rather than citing here.
- **Header: "four register items sit on that operation"** (repeated in §5) —
  five. `API-033` (medium, Reported) is on `~15 endpoints (see T5)`
  (`docs/factory/api-audit.md:267`), and theme T5 names
  `FocusRunResult.createdBlocks`/`skippedDays`/`errors` explicitly (`:133-139`),
  i.e. the `POST /api/focus/run` response body. It belongs in the header's list
  beside API-014, API-015, API-035 and U-23, and in the conditional that says
  "then all four become inherited". The conclusion is unaffected — I verified the
  spec changes neither that operation's request nor its response — but the count
  is the thing a contract author will act on.
- **OQ-6: "Sentry is initialised before anything else (`backend/main.go:21`)"** —
  `:21` is the first line of the explanatory comment; `initSentry()` is at `:25`.
- **OQ-6: "it only calls `log.Printf` (`backend/scheduler/cron.go:39, 58, 65`)"**
  — incomplete, and in a way that costs the spec its best supporting citation.
  There are five: `:39, :58, :61, :65, :69`. `:69` is
  `log.Printf("cron: focus time scheduled for user %s: %s", userID, cronExpr)` —
  emitted at **registration** time, which is where AC-6 wants its report, and it
  is the line that today prints the all-zero UUID at boot. Citing it would make
  §2.3's "emitted at fire time rather than at registration time" claim more
  precise: both happen, and neither distinguishes the two states AC-6 wants
  distinguished.
- **§0 header: "`contracts/openapi/openapi.yaml:6226-6230`"** — the field-level
  uncertainty note is the `description` at `:6228-6231`; `:6226-6227` is the
  `items:` block above it. Trivial.
- **§2.2 step 2: "`convert.go:307-325`"** — the `case nil:` block runs
  `:307-327`; `:325-327` is the tail of the `*RawBytes` branch. Trivial, and the
  branch list the spec gives (`*any`, `*[]byte`, `*RawBytes`) is exactly right.

Everything else checked out, and I am recording the ones that carry the argument
because the spec is being accepted on them. **The §2.2 chain is correct at all
three links**: `pgx/v5@v5.10.0/stdlib/sql.go:877-887` is verbatim `for i, rv :=
range r.rows.RawValues() { if rv != nil { … } else { dest[i] = nil } }`;
`database/sql/convert.go`'s `case nil:` handles only those three pointer kinds
and `*uuid.UUID` is none of them, so control reaches `if scanner, ok :=
dest.(Scanner); ok { return scanner.Scan(src) }` at `:393-395` with `src` still
nil; and `google/uuid@v1.6.0/sql.go:15-19` opens `switch src := src.(type) {
case nil: return nil }`. I also closed the three escape routes the spec does not
mention, because the correction is the whole spec: there is no second driver path
(`sql.Open` is called exactly twice, both with `"pgx"` —
`backend/storage/db.go:18`, `backend/internal/testdb/testdb.go:28`; `lib/pq` is a
direct dependency but is imported only for `pq.Array`/`pq.Int64Array`/
`pq.StringArray` in `habits.go`, `integrations.go` and `scheduling_links.go`,
never as a registered driver); there is no nullable wrapper anywhere on this path
(`UserScheduleConfig.UserID` is `uuid.UUID`, `backend/storage/settings.go:373`,
and `ListUsersWithPersonalCalendars` scans into a bare `uuid.UUID` at `:195`);
and the destination is taken by address, so `*uuid.UUID` satisfies `sql.Scanner`
and the pointer-receiver method is reached. For contrast, the same NULL into a
bare `time.Time` *would* error — `PersonalCalendar.LastSyncedAt` is `*time.Time`
(`backend/storage/personal_calendars.go:19`), which is why
`ListPersonalCalendarsByUser` tolerates a NULL `last_synced_at` — so §2.2's
conclusion is specific to `uuid.UUID` being a `Scanner`, not a general property
of this driver, and it is worth one clause in §2.2 saying so.

Also verified and correct, so nobody re-checks them: all six migration citations
(`001:17-18`, `006:15`, `018:5-10`, `:12-17`, `:17`, `:24-25`) including the
verbatim accuracy of both quoted 018 comments; all five `uuid.Nil` guards in §6
(`focus_time.go:70-73`, `personal_blocker.go:47-50`, `settings.go:420-423`,
`personal_calendars.go:163-166`) and the `Run` guard at `focus_time.go:59-65`;
all four `cron_test.go` ranges and the seed helper at `:35-48`; §2.10's claims
that `backend/storage/settings_test.go` never mentions `user_id` (zero
occurrences) and that `e2e/tests/focus-time.spec.ts` never mentions
`auto_schedule` (zero occurrences) and that the e2e seed leaves it at its FALSE
default (`e2e/seed/seed.sql:79-90` names six columns, none of them
`auto_schedule_enabled`); `focus_time_test.go:318`; `openapi.yaml:1740-1748`'s
`text/plain` 500; `api-audit.md:514-519` naming `SaveSettingsByUser` as something
that ought to exist; U-23 at `api-audit.md:467`; `PAC-24-plan.md:54, 72-75, 160,
182`; `PAC-24.md:711-731, :724, :835-847, :848-861`; and the five crons wired at
`backend/main.go:50-132`. **§0.2's claim is also true and I checked it the hard
way**: `docs/factory/api-audit.md` contains the string "cron" zero times, so the
register genuinely has no background-job surface, and the inherited-scope
reasoning under factory §7 rule 1 holds — this spec modifies neither the request
nor the response of `POST /api/focus/run`, so none of the items sitting on it is
inherited today. Subject to the undercount above, that section is right and the
"stated here so the decision is made rather than drifted into" framing is the
correct way to leave it.

### Consumers this breaks

None, and the spec should say so rather than leave it to be discovered. I
searched for every consumer of the two queries it changes:

- `storage.ListUsersWithAutoSchedule` — exactly one caller,
  `backend/scheduler/cron.go:37`. No handler, no MCP tool and no generated server
  interface touches it.
- `storage.ListUsersWithPersonalCalendars` — exactly one caller,
  `backend/main.go:61`.
- `storage.ListPersonalCalendars` — zero callers (see the third citation
  finding). Dead code, and cited in §2.6 as though it were live.

Nothing crosses the HTTP boundary, so `smart-calendar-flow` cannot observe either
change, which is consistent with the spec's argument that the shippable half sits
below the boundary. The one behaviour a consumer could notice is the one AC-4
exists to protect, and §6 is right that no existing test is in a state where the
fix could break it.

### What I could not settle without executing code

Flagged rather than asserted, and each is a candidate criterion rather than a
finding:

- **Whether a NULL `uuid` column scans without error against a real Postgres
  through this exact stack.** I traced it through three sources and I am
  confident, but the chain has three links and one of them is a standard-library
  type switch whose fall-through I reasoned about rather than observed. The spec
  makes this the premise of everything and it is cheap to pin: AC-1's test
  settles it as a side effect, and it is the single highest-value thing the first
  local session should run.
- **Whether a deployment can hold two unattributed `settings` rows**
  (non-blocking 5). The schema permits it, every writer I found forbids it, and
  only a real deployment's data answers it. OQ-2's query should return a count,
  not a boolean.
- **Whether `ListUsersWithPersonalCalendars` is reachable in the NULL state on
  any real deployment** — §2.8's own open inference, correctly handled as AC-5.
- **Whether the post-024 mass write actually lands**, now that the credential
  gate in blocking 3 is in the picture: that depends on how many users have
  connected calendars, which only OQ-2's operator can answer. It changes the
  blast radius, not the decision.

---
---

# Spec v3 — PAC-49 (2026-10-21): per-user auto-schedule, comprehensive fix

> **Landed from Linear on 2026-10-21** — document [PAC-49 spec v3 (2026-10-21) — per-user auto-schedule, comprehensive fix](https://linear.app/paceday/document/pac-49-spec-v3-2026-10-21-per-user-auto-schedule-comprehensive-fix-746d5dd4c3c2), attached to [PAC-49](https://linear.app/paceday/issue/PAC-49/listuserswithautoschedule-does-not-filter-user-id-so-the-focus-cron-is). This file is now the canonical copy. Linear's `<issue>`/`<pull-request>` markup was rewritten as markdown links; nothing else was edited except as marked **"Landed 2026-10-21"**, **"Corrected 2026-10-21"** or **"Struck 2026-10-21"** inline, every one of which follows the document's own reality-check header: migration numbers (025 is PAC-23's, so this spec takes **026** and PAC-50 takes 027), §3 item 1 / §4.1 "old global columns" / §6 struck as moot, `PUT` → `PATCH`, and OQ-5 answered from the code. The v2 findings the lineage bullet says to carry forward are in the v2 Challenge section above, unchanged.

> ⚠️ **Reality check, 2026-10-21 16:10** — written before discovering that [Enach/clockwise-like#204](https://linear.app/paceday/review/featsettings-every-user-has-their-own-settings-pac-24-89e2813268be) ([PAC-24](https://linear.app/paceday/issue/PAC-24/switch-apisettings-and-the-daily-recap-handlers-to-per-user-settings)) **merged on 2026-10-15**. Migration 024 exists: every user has a row, `user_id` is NOT NULL, nothing that acts on a calendar was copied, Outlook/Zoom credentials are per-user, `PUT /api/settings` is gone (now `PATCH`, merge). Consequences for this spec: §2.1, §4.1 "old global columns", §6 (plan rev 3) are **moot** — the reshape is done. What is still open and what the next revision must be scoped to: (1) `auto_schedule_last_run` + enable-sets-`last_run` (new migration, number TBD — 025 may be taken; **landed as 026**: 025 is PAC-23's, 027 is PAC-50's); (2) the 1-minute non-reentrant ticker replacing `FocusCron.Reload` (enabling still only takes effect at restart); (3) the `auto_schedule_ran` audit action with explicit `userID`; (4) cron/timezone validation on `PATCH`; (5) confirm `ListUsersWithAutoSchedule` and `newCalOps` read the caller's row and token after [Enach/clockwise-like#204](https://linear.app/paceday/review/featsettings-every-user-has-their-own-settings-pac-24-89e2813268be) (AC-6b). §4.4 "PUT remains a full replace" is wrong — it is PATCH. Everything else stands.

* Stage: 1 (spec). Author: cloud session, 2026-10-21; v3.1 revision by `spec-author`, 2026-10-22.
* Status: **v3.1 (2026-10-22) — v3 challenge answered; awaiting stage 2**.
* **Blocked by**: [PAC-23](https://linear.app/paceday/issue/PAC-23/add-user-id-to-the-audit-log-table-and-scope-get-apiaudit-to-the) (backend) — its migration (025) and its explicit-subject audit write land first; PAC-49's migration is 026 and migrations apply in order. AC-5 and AC-6 sit under their own "Blocked on PAC-23" heading in §5 and are implemented after it, never skipped. Everything else in §5 is buildable without PAC-23.
* **Citation basis for v3.1**: `4dc801f` (the last code commit; `50e8ca0` and `a896faa` above it touch only `docs/` and `contracts/features/`). Nothing was executed for this revision — no test, migration or request — so every claim in §1 is a reading of source with a `file:line`, and anything that would be worth more as a run is an acceptance criterion in §5.
* **Lineage**: a v2 of this spec already exists in PR [Enach/clockwise-like#179](https://linear.app/paceday/review/specpac-49-focus-cron-must-act-for-exactly-the-people-who-asked-for-it-a6e5129984b7) (`Enach/clockwise-like`, 2026-10-04: author + challenger, 4 blocking findings resolved). This v3 **supersedes v2's recommendation** ("copy the schedule, not the consent") because of the 2026-10-21 decisions in §2 — zero users, comprehensive fix. Whoever lands this file must keep v2's `## Challenge` section appended and carry forward v2's findings that v3 does not restate: (a) `newCalOps` refuses without an OAuth token (`backend/engine/cal_iface.go:53-58`), so the blast radius of any mistake is "every user with a connected calendar"; (b) `FocusCron.Reload()` has one production call site, inside `Start` (`backend/scheduler/cron.go:73-76`, called from `backend/main.go:69-70`) — enabling auto-schedule today only takes effect at restart (§4.3 removes `Reload` entirely); (c) **corrected 2026-10-22**: v2 recorded the daily recap as joining every user against the shared `id = 1` row; since #204 the recap selects only users whose *own* row has it on (`backend/engine/daily_recap.go:143-148`, `INNER JOIN settings st ON st.user_id = u.id`), so that finding is closed and nothing is owed to any PAC-24 plan revision. v2's correction that `google/uuid` scans NULL to `uuid.Nil` cleanly (the listing never "fails") stands, and is moot since 024 made the column NOT NULL.
* ~~Relationship to PAC-24: implemented in the same migration (024) and the same stage-4 branch. PAC-24's plan (revision 2) was written before the decisions in §2 and must be simplified accordingly — see §6.~~ **Landed 2026-10-21:** [PAC-24](https://linear.app/paceday/issue/PAC-24/switch-apisettings-and-the-daily-recap-handlers-to-per-user-settings) / #204 is merged. This spec ships on its own stage-4 branch with its own migration (026).
* ~~Blocks: PAC-24 migration 024. Sequenced before PAC-48 (which moves the token columns off the same shared row).~~ **Landed 2026-10-21:** moot — 024 has shipped, and Outlook/Zoom credentials are per-user since #204. [PAC-48](https://linear.app/paceday/issue/PAC-48/outlook-and-zoom-credentials-are-written-to-the-singleton-settings-row) keeps only what #204 left (see its issue).

## 1. Problem

> **Rewritten 2026-10-22 (v3 challenge, blocking 1).** v3's §1 described the
> pre-#204 world — one ownerless flag, a run that died on "no settings row", a
> landmine that enrolled everyone. None of the three is true at `4dc801f`, and
> the next reader was being told the landmine was live. This section now states
> what *is* true for a user today, with citations, and what this spec changes.

**Who is affected.** A signed-in person who switches "Auto-schedule focus time"
on in their own settings. Since migration 024 every settings row belongs to one
user and to nobody else, so this is every user of the product who opts in.

**What the issue originally reported is closed.** The issue's mechanism — a
missing `user_id` predicate on the listing, so that an ownerless row was
registered as a schedule for nobody — was closed by
[Enach/clockwise-like#204](https://linear.app/paceday/review/featsettings-every-user-has-their-own-settings-pac-24-89e2813268be)
(PAC-24, merged 2026-10-15): migration 024 deletes any row without an owner and
makes the owner column `NOT NULL`
(`backend/storage/migrations/024_settings_per_user.up.sql:140, 144`), so every
row the listing returns (`backend/storage/settings.go:411-416`) is a person's
own; the run injects that person's id into its context
(`backend/engine/focus_time.go:74`) and reads that person's calendar token
(`backend/engine/cal_iface.go:53-58`, `backend/auth/token_store.go:40-49`), so
no scheduled run writes into anyone else's calendar; and a person with no row
gets one created on first read (`backend/storage/settings.go:237-242`, asserted
by `TestFocusRun_UserWithNoPriorSettings`,
`backend/engine/focus_time_test.go:401-413`), so "no settings row" is no longer
a way for a run to fail. The mass-enrolment landmine never fired: nothing that
acts on a calendar was copied to anyone.

**What is still wrong for that user at `4dc801f`.** Four things, in the order
they are met:

(a) **Switching it on does nothing until the process restarts.** The set of
people to schedule is read once per process: `FocusCron.Reload` has exactly one
production caller, `Start` (`backend/scheduler/cron.go:73-76`), invoked once from
`backend/main.go:69-70`. Saving settings calls nothing on the scheduler. On a
long-lived deployment the page says "saved" and the calendar stays empty
indefinitely. The same is true of changing the schedule or switching it off: the
old schedule keeps firing until a restart.

(b) **When it does run, it runs at the server's local hour, not the user's.**
The scheduler is built with `cron.New()` and no location
(`backend/scheduler/cron.go:27`), so the library evaluates every expression in
`time.Local` (`github.com/robfig/cron/v3@v3.0.1/cron.go:124`). None of
`docker-compose.yml`, `docker-compose.dev.yml` or `docker-compose.prod.yml` sets
`TZ`; only the e2e stack pins `TZ: UTC` (`e2e/docker-compose.e2e.yml:111`). The
user's own `timezone` setting is not consulted when deciding *when* to fire —
only when deciding where in the day to put blocks. A Paris user who writes
`0 7 * * 1-5` on a UTC server has blocks created at 09:00 Paris in summer, after
the working day has started.

(c) **The run is unattributed in the activity log.** A run records one
`focus_created` entry (`backend/engine/focus_time.go:103-108`) through a writer
that takes no user (`backend/storage/focus_blocks.go:74-75`) into a table that
has no user column (`backend/storage/migrations/001_initial.up.sql:44-49`). A
scheduled run and a manual one are indistinguishable, and neither says whose
calendar was written. This half is PAC-23's subject and is the reason for the
"Blocked by" bullet above.

(d) **There is no record of when it last ran.** Nothing stores it. The only
trace is a server log line (`backend/scheduler/cron.go:61-62`). The user cannot
tell from the product whether the feature has ever acted for them; an operator
cannot tell either without the log.

**What this spec changes, observably.** Enabling, editing or disabling takes
effect within a minute and without a restart; a scheduled run happens at the
user's own hour in the user's own timezone; each scheduled run leaves one
attributed entry in the activity log (after PAC-23); the settings page shows
when it last ran; an unusable schedule or timezone is refused at save time with
a machine-readable reason. Nothing about the manual run, the stored defaults, or
what a run does once it starts changes — §7 lists what stays the same.

## 2. Decisions (human, 2026-10-21)

1. **Production has zero users.** Migration 024 has no data to preserve. No backfill, no inheritance, no "copy the preference but don't act on it".
2. **Take the comprehensive fix**: auto-schedule becomes a per-user feature end to end — flag, schedule, execution, audit.
3. Both repos deploy together (docker-compose), so the settings wire-shape change ships in one release with the frontend that reads it. No dual-shape window.
4. **Added 2026-10-22 (orchestrator, from the issue owner)**: the stored schedule default stays `'0 8 * * *'`; migration 026 adds the last-run column and nothing else. The weekday-morning schedule is a preset the user picks, not a default the database imposes.

## 3. Scope

In scope:

1. ~~Per-user `auto_schedule_enabled` and `auto_schedule_cron` columns on the per-user settings row that PAC-24 introduces.~~ **Landed 2026-10-21 (struck per the reality-check header):** both columns already exist on `settings` and every row is per-user since migration 024. What this spec adds to the schema is `auto_schedule_last_run` only (§4.1).
2. The set of people scheduled is exactly the set whose own settings have the flag on. **Already true at `4dc801f`** (§1); AC-2 keeps it as a regression guard because §4.3 replaces the code that currently proves it.
3. A scheduled run acts for one user with that user's settings and **that user's calendar token**. One user's failure must not stop the others.
   **Landed 2026-10-21 (answers OQ-5 and reality-check item 5):** the Google token is *already* per-user — `oauth_tokens` is keyed `(user_id, calendar_id)` since migration 017 and `newCalOps` (`backend/engine/cal_iface.go:53-58`) reads it with `auth.LoadUserToken(db, userID)` for the user id `RunForUser` injects into the context (`backend/engine/focus_time.go:74`). No column move is needed; AC-6b still has to prove it. The struck text below is the pre-#204 reasoning, kept for lineage. ~~**This requires the Google calendar token to be per-user in the same migration.** Today it lives on the shared settings row (`WHERE id = 1`) next to `microsoft_tokens`; [PAC-24](https://linear.app/paceday/issue/PAC-24/switch-apisettings-and-the-daily-recap-handlers-to-per-user-settings)'s reshape of that row into per-user rows carries *every* column, token columns included, so `newCalOps(userID)` reads the caller's own token. Without this, N enabled users would all write focus blocks into the one connected calendar — the §1 landmine relocated, not removed. [PAC-48](https://linear.app/paceday/issue/PAC-48/outlook-and-zoom-credentials-are-written-to-the-singleton-settings-row)'s remaining scope after this is the `calendar_provider` flip semantics and Zoom, not the column move.~~
4. Enabling, disabling, or editing the schedule or timezone takes effect at the next minute, not the next restart (§4.3), with the rules for *when the first run after such a change happens* stated in §4.1 and proven in AC-3, AC-7b, AC-7c, AC-7d.
5. The scheduled run fires at the user's slot in the user's own IANA timezone (§4.3).
6. Settings: the caller reads and writes their own two auto-schedule fields — **already true since PAC-24** (AC-8 is a regression guard) — and additionally reads, but cannot write, when the scheduler last ran for them (§4.4); an unusable schedule or timezone is refused with a machine-readable code (§4.4).
7. Audit: one attributed `auto_schedule_ran` entry per user per scheduled run, in addition to the entry the run already writes (§4.3, item 12). **Blocked on PAC-23.**
8. Frontend: two test ids, schedule presets, and a read-only last-run line (§4.5).

Out of scope:

* Per-user cron *schedules* running at different times. v3 keeps a single process-level tick and lets each user's `cron` expression decide whether this tick is "their" time. See §4.3.
* Changing how tokens are *obtained*, or the `calendar_provider` flip in `SaveMicrosoftToken` ([PAC-48](https://linear.app/paceday/issue/PAC-48/outlook-and-zoom-credentials-are-written-to-the-singleton-settings-row)). **Corrected 2026-10-22**: the token is already per-user (item 3 above); nothing moves, and the v3 sentence that put a column move "in scope via PAC-24's reshape" is withdrawn.
* ~~Making focus blocks per-user in `ListFocusBlocksForWeek` / `FocusMinutesForDay` (they take no user id and act as global busy time). Real defect, separate issue.~~ **Struck 2026-10-22 (v3 challenge, non-blocking 2):** no such defect — both readers take a user id (`backend/storage/focus_blocks.go:30, 64`) and the first name does not exist.
* Changing the stored schedule default. It stays `'0 8 * * *'` (§2 decision 4). A user who never touched the schedule field is run daily at 08:00 in their timezone, Saturday and Sunday included, exactly as the stored default has always said; what a weekend run does is the next bullet.
* What a run does with days already in the past, including a weekend tick that hands the run a week whose working days are over. A tick schedules the week containing the tick instant, exactly as the manual run does today when no week is given; the run's own handling of past days is unchanged by this spec. If that handling should change, it is a change to the run, not to the scheduler, and gets its own spec.
* Clearing or re-planning existing blocks on a scheduled run. A scheduled run only ever adds; it never deletes (§4.3, item 13; AC-14). Whether a morning run *should* re-plan the week is a product question this spec does not open.
* Machine-readable codes for the rest of the settings validation catalogue. Exactly two refusals gain a code here (§4.4); the others keep their messages and nothing else.
* Anything about the manual "run my focus time now" request — its request, its response, or its failure body.
* Reporting a run's outcome to the user by any channel other than the activity log and the last-run time.

## 4. Desired behaviour and requirements

> **Rewritten 2026-10-22 (v3 challenge, non-blocking 1).** v3's §4 named a Go
> signature, a cron library and two of its options, a mutex strategy, an
> interface, a `main.go` seam and a handler helper. The factory forbids that at
> stage 1 (`docs/factory/README.md` stage 1 gate and §5 table). What follows
> states the *requirements* those names were standing in for; the plan chooses
> how to meet them. The names that remain — the migration number, the stored
> column, the wire properties, the audit literal, the two codes — are the
> contract-level decisions the orchestrator fixed for this revision, and stage 2
> writes them down.

### 4.1 Stored state (migration 026 — the last-run watermark only)

> **Landed 2026-10-21:** the table is `settings`, keyed by `user_id` (NOT NULL since 024). `auto_schedule_enabled` and `auto_schedule_cron` already exist (since `001_initial.up.sql`, as `BOOLEAN NOT NULL DEFAULT FALSE` and `TEXT NOT NULL DEFAULT '0 8 * * *'`). **Corrected 2026-10-22 (v3 challenge, blocking 2):** v3 printed an "intended end state" with `DEFAULT '0 7 * * 1-5'` and the prose "weekdays at 07:00". Both are struck; the stored default is `'0 8 * * *'` and 026 does not change it (§2 decision 4). The contract already documents it as such (`contracts/openapi/paths/scheduling.yaml:1752`).

Migration 026 adds exactly one thing: a per-user, nullable, timezone-aware
timestamp — **`auto_schedule_last_run`**, the *watermark*. Nothing else changes
in the schema: no default changes, no column is dropped, no row is rewritten.

What the watermark means, as behaviour:

* **It is null for a user who has never had auto-schedule on.**
* **Enabling sets it to "now".** When a save turns the flag from off to on, the
  watermark becomes the instant of that save. The first scheduled run is
  therefore the user's *next* slot after they enabled, never the next tick:
  a user who enables at 06:30 with a 07:00 schedule runs at 07:00, and a user
  who enables at 09:00 with a 07:00 weekday schedule runs tomorrow at 07:00.
* **Editing the schedule or the timezone while enabled resets it to "now",
  exactly like enabling.** A user whose last run was yesterday 07:00 and who
  changes `0 7 * * 1-5` to `0 5 * * 1-5` at 09:00 is next run tomorrow at
  05:00 — not at the next tick, which is what "next slot after yesterday's
  watermark" would otherwise give. A timezone change, in either direction, is
  treated the same way. An edit never causes an immediate run. *(v3 challenge,
  blocking 6.)*
* **A save that changes neither the flag, the schedule nor the timezone leaves
  the watermark alone**, whatever else it changes.
* **A settings save never carries the watermark.** Only the enable/edit reset
  above and the scheduler's own advance (§4.3, item 4) ever write it. In
  particular, a save built from a settings document read *before* a tick
  advanced the watermark does not rewind it — so a save that races a tick
  cannot cause a second run. *(v3 challenge, non-blocking 8.)* AC-7e proves it.
* **The scheduler advances it to the tick instant before each run** (§4.3,
  item 4).
* **Disabling leaves it as it is.** The value is then history ("last ran at
  …"), and re-enabling resets it to "now" by the first rule, so stale history
  never schedules a run.
* **A null watermark on an enabled user** can only arise from a direct database
  edit. The scheduler treats it as "now": it sets it and does not run the user
  on that tick.

Down migration (026): drop the watermark column. **The down migration loses
it** — see §8.

### 4.2 Who is a candidate

Only a user whose own settings have the flag on. Nobody else is ever
considered, whatever any other user has set. This is already true at `4dc801f`
(§1) and is restated because §4.3 replaces the code that proves it today; AC-2
guards it.

### 4.3 The scheduled loop

The current design — one registration pass at start-up, one library-managed
entry per user, evaluated in the server's local time — is replaced by a loop
with these properties. Each is a requirement; none prescribes a mechanism.

1. **Minute granularity.** While the process runs, the question "is it this
   user's time?" is asked at least once per minute for every candidate. A
   schedule expression's finest unit is the minute, so a coarser check would
   silently skip slots.
2. **No overlapping ticks.** A tick that begins while the previous one is still
   running is dropped, not queued. Together with item 4 this is what makes a
   double run impossible.
3. **Due means: the next slot after the watermark is not later than now, in
   the user's own IANA timezone.** The user's `timezone` setting decides what
   "07:00" means; the server's zone is never consulted.
4. **The watermark is advanced to the tick instant *before* the run, on every
   outcome** — success, the run's own failure (for example, no calendar
   credential), or a run in which every day failed. So a user is never run
   twice for one slot, a user whose credential is dead is retried at their
   *next slot* rather than every minute, and a run that outlives a tick (item
   2) cannot be started again by the next one.
5. **A tick schedules the week containing the tick instant**, exactly as the
   manual run does today when no week is given. The run's handling of days
   already past is unchanged by this spec (§3, out of scope). *(v3 challenge,
   non-blocking 3.)*
6. **One user's failure never stops the loop.** Whatever happens to one user,
   every other due user on the same tick is still attempted.
7. **Per-user time bound: ten minutes per run**, which is the bound each run
   has today. A run that exceeds it is abandoned, treated as a failed run (item
   12's `error`), and the loop moves on; one hung credential refresh cannot
   hold every user behind it. *(v3 challenge, non-blocking 9.)* AC-4e.
8. **A missed slot is caught up once.** If the process was down across a
   user's slot, the first tick after restart runs them (their next slot after
   the watermark is in the past, so they are due), the watermark advances to
   that tick, and they are not run again until their next slot. Never more
   than once, however many slots were missed. *(v3 challenge, blocking 6.)*
   AC-4c.
9. **The loop parses schedules with the same five-field parser the write uses,
   and no other.** A stored expression that parser rejects — a descriptor such
   as `@daily`, which the current registration path accepts and which existing
   test fixtures seed — is skipped with a warning that names the user, writes
   no audit row, and keeps no "already warned" state; the same for a stored
   timezone that cannot be loaded. Both are unreachable through the write path
   after this spec (§4.4) and reachable only by direct database edit or by
   rows that predate it. *(v3 challenge, non-blocking 11.)* AC-4d.
10. **The clock the loop reads can be set by a test**, so that every criterion
    in §5 that says "tick at 07:00" is a unit test and not a wait; and **the
    e2e stack has a way to make the loop tick at a chosen instant**, because
    the e2e suite cannot wait for a real 07:00. The mechanism of both is the
    plan's. *(v3 challenge, non-blocking 6.)* AC-12 requires the second.
11. **A scheduled run calls the same per-user run the manual request calls**,
    with that user's settings and that user's token, and refuses an empty user
    id exactly as it does today.
12. **One attributed audit entry per user per scheduled run — in addition to
    the `focus_created` entry the run itself writes.** The run already records
    `focus_created` (`backend/engine/focus_time.go:103-108`); the loop does
    not replace that, it adds one `auto_schedule_ran` entry for the user, so a
    successful scheduled run leaves two entries and AC-6 counts only the
    second. *(v3 challenge, non-blocking 4.)* Its details are
    `{created: n, errors: [...], error: string|null}` where:
    * `created` is the number of blocks the run created (0 when it found no
      free slot or failed);
    * `errors` is the list of per-day failure strings the run itself reports
      (its own `Errors` list — listing a day's events, creating or saving a
      block), possibly empty;
    * `error` is the run's *own* failure — it did not get as far as the days:
      no calendar credential, settings unreadable, abandoned at the time bound
      — or null.
    So a run that fails every day has `created: 0`, a non-empty `errors` and
    `error: null`; a run refused for want of a credential has `created: 0`,
    `errors: []` and a non-null `error`; a run that found no free slot has
    `created: 0`, `errors: []`, `error: null`. The entry is attributed to the
    user it ran for, which is what PAC-23 provides and why §5's AC-5 and AC-6
    are blocked on it. `cleared` is **not** in the details — see item 13.
13. **A scheduled run never clears blocks.** The run only ever adds blocks
    (`backend/engine/focus_time.go:178-193` creates and saves, nothing else);
    clearing is a separate, manual-only operation
    (`backend/engine/focus_time_cleaner.go:16`, called only from
    `backend/api/handlers_focus.go:81`). This spec keeps that: a block the user
    kept does not vanish every morning. *(v3 challenge, blocking 3.)* AC-14.

### 4.4 Settings interface (the contract delta)

The settings read and write already carry `autoScheduleEnabled` and
`autoScheduleCron` for the caller only (`contracts/openapi/paths/scheduling.yaml:1745-1752`;
PAC-24 contract revision 6). This spec adds to that operation pair, and to one
other, exactly the following:

* **`autoScheduleLastRun`** — read-only, RFC 3339 timestamp or `null`, the
  watermark of §4.1. On write it is **accepted and ignored**, in the same class
  as `llmApiKeySet` and `updatedAt` (`scheduling.yaml:1984`), so a document read
  from GET can be sent back unchanged and PAC-24's AC-10 still holds; it is not
  a `null`-is-400 property (`scheduling.yaml:1987`) because a GET document
  legitimately carries `null` here. *(v3 challenge, non-blocking 7.)* The shipped
  client derives its PATCH key set from the generated write schema minus two
  hard-coded names (`smart-calendar-flow/src/api/client.ts:636-637`), so it
  *will* include this property; accepted-and-ignored is what makes that safe.
* **A `code` property on the write's 400 response**, optional, carrying
  `invalid_cron` when the refusal is the existing
  `autoScheduleCron must be a 5-part cron expression` /
  `autoScheduleCron is invalid: <err>` message, and `invalid_timezone` when it
  is the existing `timezone is not a known IANA location` message. The
  **message stays exactly as it is today**; the shared envelope stays
  `{"error": "<message>"}` and merely gains the optional property; **no other
  message in the catalogue gains a code here**. *(v3 challenge, blocking 4.)*
  The shipped client reads `error` first
  (`smart-calendar-flow/src/api/client.ts:328-329`) and is unaffected.
* **An eighth `action` literal, `auto_schedule_ran`, on the activity-log
  read.** That enum is closed by design — adding a value is a contract change
  (`contracts/openapi/paths/calendar.yaml:1668`;
  `backend/api/gen/paceday.gen.go:534-536`) — and the operation is PAC-23's:
  it carries API-005 (critical, Confirmed, `docs/factory/api-audit.md:239`) and
  API-074 (`:308`), both of which PAC-23 resolves. **PAC-49's contract revision
  is therefore written on top of PAC-23's contract revision 4**
  (`contracts/features/PAC-23.md`, "Revision 4 — 2026-10-04"), never beside
  it, and lands after it. *(v3 challenge, blocking 5.)*
* The settings write continues to refuse an unusable schedule or timezone
  before anything is stored (both refusals exist today; only the code is new).

The MCP server reaches settings through one read-only tool that performs the
GET (`mcp/tools.go:233-238`); it has no write and no direct column read, so it
sees the new property and needs nothing else (OQ-4).

### 4.5 Frontend (smart-calendar-flow, after the contract merges)

The toggle and the schedule field already exist
(`smart-calendar-flow/src/pages/Settings.tsx:393-412`). The whole frontend delta
is: *(v3 challenge, non-blocking 10)*

* two test ids on those existing controls, per `e2e/TESTIDS-REQUIRED.md`:
  `settings-autoschedule-toggle`, `settings-autoschedule-cron`;
* presets that fill the schedule field — at least "weekday mornings"
  (`0 7 * * 1-5`) and "every morning" (`0 8 * * *`, the stored default) — with
  the raw field kept behind "advanced";
* one read-only line showing when the scheduler last ran for this user, from
  `autoScheduleLastRun`, test id `settings-autoschedule-lastrun`, rendered as
  "never" (or hidden) when the value is `null` **or the property is absent** —
  the absence case is what a rollback produces (§8).

Types come from the regenerated contract, never hand-written (factory rule).

## 5. Acceptance criteria

Each is Given/When/Then and names the layer that proves it. "Tick at HH:MM"
means the loop's clock is set to that instant and one tick is forced (§4.3,
item 10). Every `[unit]` here needs a live Postgres, as v2's §4 preamble
explains; none has been run for this revision.

**Regression guards.** AC-1, AC-2 and AC-8 are already true at `4dc801f` and
are kept because §4.3 deletes the code and the tests that currently prove them
(`backend/scheduler/cron_test.go:72-141`, all four `Reload` tests go with
`Reload`). They are stated so that stage 3 writes their replacements, not so
that anyone records them as new behaviour.

### Buildable now

* **AC-1** *(regression guard; the watermark clause is new)* Given a database
  migrated through 026 holding users who have never saved settings, when the
  candidate set is produced, then it is empty; and each such user's settings
  read shows `autoScheduleEnabled: false`, `autoScheduleCron: "0 8 * * *"` and
  `autoScheduleLastRun: null`. [unit]
* **AC-2** *(regression guard)* Given users A and B where only A has enabled
  auto-schedule, when the candidate set is produced, then it is exactly `[A]`.
  [unit]
* **AC-3** Given A with timezone `Europe/Paris` and schedule `0 7 * * 1-5`
  who enables it at 06:30 Paris on a weekday (so the watermark is 06:30), and
  B who has it disabled, when ticks occur at 06:31 and 06:58 Paris, then no
  run is started for A and none for B. [unit]
* **AC-4** Given AC-3's state, when ticks occur at 07:00, 07:01 and 07:07
  Paris, then A is run exactly once, at the 07:00 tick, with the week
  containing that instant as its target; A's watermark afterwards equals the
  07:00 tick instant; B is never run. [unit]
* **AC-4a (timezone)** Given A as in AC-3 but the process clock in UTC, when
  the tick at 05:00 UTC (07:00 Paris, summer) occurs, then A is run; and given
  the same A in winter, the tick at 06:00 UTC runs A and the tick at 05:00 UTC
  does not. [unit]
* **AC-4b (no overlap)** Given A and B both enabled on the same schedule and
  A's run made to block for three minutes, when ticks occur at 07:00, 07:01 and
  07:02, then the 07:01 and 07:02 ticks are dropped, A is run exactly once, and
  B is run exactly once, by the 07:00 tick after A's run returns. [unit]
* **AC-4c (missed slot, caught up once)** Given A on `0 7 * * 1-5` whose
  watermark is Monday 07:00, when the process is down from Tuesday 06:00 to
  Tuesday 09:30 and ticks then occur at 09:30, 09:31 and 09:45, then A is run
  exactly once, at 09:30, A's watermark becomes 09:30, and A is not run again
  before Wednesday 07:00. Given instead the process is down from Monday 08:00
  to Thursday 09:30 (three slots missed), the same ticks run A exactly once.
  [unit]
* **AC-4d (unparseable stored values are skipped, not fatal)** Given A enabled
  with the stored expression `@daily` (accepted by the old registration path,
  rejected by the five-field parser) and B enabled with a valid schedule due
  now, when a tick occurs, then A is not run, one warning naming A is logged,
  no audit entry is written for A, and B is run; and given A instead has a
  stored timezone that cannot be loaded, the same holds. [unit]
* **AC-4e (time bound)** Given A and B both due and A's run made to block
  indefinitely, when the tick occurs, then A's run is abandoned after ten
  minutes, A's watermark was advanced at the tick, and B is run on the same
  tick. [unit]
* **AC-6b (own token only)** Given A and B both enabled and due, each with
  their own calendar credential, and a recording calendar stand-in keyed by
  credential, when the tick runs both, then every event created for A was
  created with A's credential and every event for B with B's; the test fails
  if either run used the other's. [unit]
* **AC-7 (machine-readable refusal)** Given a signed-in user, when they save
  `autoScheduleCron: "every tuesday"`, then the response is 400, JSON, with
  `error` equal to today's message for that refusal and `code` equal to
  `invalid_cron`, and the stored row is unchanged; when they save
  `timezone: "Mars/Olympus"`, the response is 400 with `code` equal to
  `invalid_timezone`; and when they save `focusMinBlockMinutes: -1`, the
  response is 400 with today's message and **no** `code` property. [contract]
* **AC-7b (enable resets the watermark)** Given A with auto-schedule off and
  the write-side clock set to T, when A saves `autoScheduleEnabled: true`,
  then A's `autoScheduleLastRun` reads back as T; and given A already on with
  watermark W, when A saves `autoScheduleEnabled: true` again, or omits it,
  the watermark is still W. [unit]
* **AC-7c (edit while enabled resets the watermark)** Given A enabled on
  `0 7 * * 1-5` with watermark yesterday 07:00, when at 09:00 today A saves
  `autoScheduleCron: "0 5 * * 1-5"`, then A's watermark is 09:00 today, ticks
  at 09:01 and 09:30 do not run A, and the tick at 05:00 tomorrow does; and
  given the same A instead saves `timezone` to a zone six hours west, the
  watermark is likewise 09:00 and no tick before A's next 07:00 in the new
  zone runs A. [unit]
* **AC-7d (an unrelated save leaves the watermark alone)** Given A enabled
  with watermark W, when A saves a change to `focusLabel` only, then A's
  watermark is still W. [unit]
* **AC-7e (a save never carries the watermark)** Given A's settings document
  read at T0 showing `autoScheduleLastRun: W0`, and a tick that then advances
  A's watermark to W1, when that T0 document is sent back as a write with
  `autoScheduleLastRun: W0` and one unrelated property changed, then the
  response is 200, the response body shows `autoScheduleLastRun: W1`, and the
  next tick does not run A. [contract]
* **AC-8** *(regression guard)* Given A and B, when A saves a change to their
  auto-schedule settings, then B's settings read is unchanged. [contract]
* **AC-9** Given (a) an empty database and (b) a database carrying the e2e
  seed's per-user settings rows (`e2e/seed/seed.sql:105-115`, none with
  auto-schedule on — so (b) exercises the migration against real rows but no
  data in the new column, which is what the seed has), when migration 026 is
  applied, reverted and applied again, then each step succeeds and afterwards
  every row's watermark is `null`. [unit]
* ~~**AC-10** No query in `backend/` or `mcp/` references the dropped global columns (grep in `make verify`'s lint step or a unit test that fails on `auto_schedule` outside the new table).~~ **Struck 2026-10-21:** no columns are dropped (§4.1).
* **AC-11** Given the assembled contract, when the contract gate runs, then it
  passes, and the settings fragment documents `autoScheduleLastRun` as
  read-only, nullable and accepted-and-ignored on write; the write's 400
  response documents the optional `code` with exactly the values
  `invalid_cron` and `invalid_timezone`; and the activity-log fragment's
  `action` enum contains `auto_schedule_ran` as its eighth value, on top of
  PAC-23's contract revision 4. [contract]
* **AC-12** Given a seeded user signed in to the e2e stack who turns
  auto-schedule on and picks the "weekday mornings" preset through the
  controls carrying `settings-autoschedule-toggle` and
  `settings-autoschedule-cron`, when the loop is made to tick at that user's
  next slot, then focus blocks appear in the calendar stand-in for that user
  only, no other seeded user's calendar changes, and the Settings page's
  `settings-autoschedule-lastrun` line shows the tick time. The test is not
  `test.fixme`: PAC-43's calendar stand-in exists (`e2e/fake-google/`,
  `e2e/SEAM-REQUIRED.md:3`), and the tick seam is a requirement of this spec
  (§4.3, item 10), not a precondition it waits for. [e2e]
* **AC-13 (last run is visible)** Given A for whom a scheduled run completed
  at tick T, when A reads their settings, then `autoScheduleLastRun` equals T
  and the Settings page shows it under `settings-autoschedule-lastrun`; given
  a user who has never enabled it, the value is `null` and the page shows
  "never" or nothing. [e2e]
* **AC-14 (a scheduled run never clears)** Given A with one existing focus
  block in the current week and auto-schedule due, when the tick runs A, then
  that block still exists in storage and in the calendar stand-in afterwards,
  and no `focus_cleared` entry was written by the run. [unit]

### Blocked on PAC-23

Neither of these can be written until the activity log can carry a subject
(PAC-23's migration 025 and its explicit-subject write). They are implemented
after PAC-23 lands, in the same stage-4 branch or a follow-up on it — never
skipped.

* **AC-5** Given A and B both due, and A's run failing before it reaches any
  day (no calendar credential), when the tick occurs, then the loop goes on to
  run B; A's watermark was advanced; and exactly one `auto_schedule_ran` entry
  exists for A, attributed to A, whose details parse to
  `{created: 0, errors: [], error: <non-null string>}`. [unit]
* **AC-6** Given A due, when A's scheduled run creates two blocks, then
  exactly one `auto_schedule_ran` entry exists for A, attributed to A, whose
  details parse to `{created: 2, errors: [], error: null}` — the run's own
  `focus_created` entry is not counted. Given instead a run that finds no free
  slot, the one entry parses to `{created: 0, errors: [], error: null}`. Given
  instead a run in which listing events fails on every one of the five
  weekdays, the one entry parses to `created: 0`, an `errors` list of five
  strings, and `error: null`. [unit]

## 6. ~~Consequences for PAC-24's plan (revision 2 → 3)~~ — struck 2026-10-21

> **Struck per the reality-check header:** PAC-24 / #204 merged on 2026-10-15 with migration 024 as shipped; there is no PAC-24 plan revision 3 to write. Item 5 (the explicit `userID` on `WriteAuditLog`) is carried by PAC-23's plan, which already specifies it (`docs/factory/PAC-23-plan.md:47`). The list is kept struck for lineage.

~~The plan author should strike, not reinterpret:~~

1. ~~Any inheritance/backfill of the `id = 1` row into per-user rows — gone.~~
2. ~~Any "copy `auto_schedule_enabled` as false regardless" step — superseded by §4.1 (column default false, old column dropped).~~
3. ~~Any cross-repo compatibility window or dual wire shape — gone (§2.3).~~
4. ~~Add: the three columns, the loop rewrite (§4.3), the eighth audit literal, the per-user token columns (§3.3), and ACs 1–12 above to the same branch.~~
5. Confirm [PAC-23](https://linear.app/paceday/issue/PAC-23/add-user-id-to-the-audit-log-table-and-scope-get-apiaudit-to-the)'s `WriteAuditLog` signature takes an explicit `userID`; if [PAC-23](https://linear.app/paceday/issue/PAC-23/add-user-id-to-the-audit-log-table-and-scope-get-apiaudit-to-the)'s plan has it context-derived, change [PAC-23](https://linear.app/paceday/issue/PAC-23/add-user-id-to-the-audit-log-table-and-scope-get-apiaudit-to-the), not this. **Checked 2026-10-22:** it does — `docs/factory/PAC-23-plan.md:47`.

What stays unchanged in [PAC-24](https://linear.app/paceday/issue/PAC-24/switch-apisettings-and-the-daily-recap-handlers-to-per-user-settings): creating a settings row at signup for every new user (this is the real fix for the `no settings row` outage), per-user `GetSettings`/`SaveSettings`, and the `llmApiKey` no longer being a shared cleartext value.

## 7. What must not change

*(Restored 2026-10-22 — v3 challenge, blocking 7. v2 had this section; v3 dropped it.)*

* **The manual "run my focus time now" request and its `focus_created`
  entry.** Its request (optional week), its response, its `text/plain` 500,
  and the one audit entry it writes are untouched. A scheduled run calls the
  same per-user run; it adds an entry beside that one, it does not change it.
* **The settings write's merge semantics and every existing validation.** An
  absent property is unchanged, a present one is set exactly, `null` and
  unknown properties are a 400, `llmApiKeySet` and `updatedAt` are accepted and
  ignored; every message in the validation catalogue is emitted unchanged. The
  only additions are the optional `code` on two of those messages and one more
  accepted-and-ignored property. Protected by PAC-24's tests and AC-7/AC-7e.
* **The run's refusal of an empty user id**, at every layer that refuses it
  today. Filtering candidates earlier is not licence to remove the guards.
* **The per-user token read.** A scheduled run reads only the user's own
  credential, and a user who has none is refused before anything is written.
  AC-6b and AC-5.
* **The stored schedule default `'0 8 * * *'` and the two existing columns.**
  Migration 026 touches neither. AC-1.
* **A user with auto-schedule off is never run**, whatever anyone else set.
  AC-2, AC-3.
* **Nothing a scheduled run does once it starts changes** — which days it
  plans, how it picks slots, what it writes into the calendar, and that it
  never deletes (AC-14).
* **The activity-log read's existing seven `action` values remain**; PAC-23's
  AC-14 (the set never shrinks) holds across this change. The shipped UI treats
  `action` as an opaque string (`smart-calendar-flow/src/api/client.ts:451`)
  and renders an unknown value as `unknown`, which is the contract's own stated
  handling, so the eighth value breaks no consumer.
* **Tests that go, and what replaces them.** The four `Reload` tests
  (`backend/scheduler/cron_test.go:72-141`) test a method §4.3 deletes. Their
  four guarantees survive as AC-1 (nobody on → nothing runs), AC-2 (per-user),
  AC-4d (an unusable stored schedule is skipped, not fatal) and AC-4/AC-7c
  (the set of candidates is re-decided — now every tick rather than on
  reload). Stage 3 names the replacement tests.

## 8. Rollback

*(Restored 2026-10-22 — v3 challenge, blocking 7.)*

**Code revert.** Reverting the release restores the restart-only loop in
server-local time (`backend/scheduler/cron.go:27, 53-56`). Three consequences
for users who enabled under this release: their schedule is evaluated in the
server's zone again, so a Paris user on a UTC server **silently shifts** from
07:00 Paris to 07:00 UTC; any enable or edit they make afterwards takes effect
only at the next restart; and, because the old loop keeps no watermark, a user
whose last run was earlier the same day may be run again at their next
server-local slot. Blocks already created are in the calendar and stay there —
no revert retracts them, which is the same asymmetry v2 §7 recorded.

**Audit entries.** `auto_schedule_ran` entries already written sit outside the
previous contract's closed action set. A strictly validating consumer of the
activity-log read would reject every response that contains one; the contract's
own instruction to consumers is to render the row with a placeholder rather
than discard the response, and the shipped UI does exactly that
(`smart-calendar-flow/src/api/client.ts:451`). The rows are not deleted on
rollback; they remain true history.

**Down migration.** 026's down migration drops the watermark column and
**loses it**. Re-applying the up migration later gives every enabled user a
`null` watermark, which §4.1's last rule turns into "treat as now, skip this
tick" — so a rollback-then-upgrade does not produce a burst of catch-up runs.
What is lost is only the "last ran at" history.

**Wire properties.** After a revert the settings read no longer carries
`autoScheduleLastRun` and the write's 400 no longer carries `code`. The shipped
client reads `error` before anything else and tolerates a missing property; the
last-run line in §4.5 is required to render "never" or nothing when the property
is absent, so a frontend deployed ahead of a backend revert degrades rather than
breaks.

## 9. Open questions

* **OQ-1** ~~Is `robfig/cron/v3` already in `go.mod`?~~ **Closed 2026-10-22 from
  the code:** `github.com/robfig/cron/v3 v3.0.1` is at `backend/go.mod:21` and
  is already imported by the settings handler
  (`backend/api/handlers_settings.go:16`) and the scheduler
  (`backend/scheduler/cron.go:12`). No dependency is added; which parser the
  plan uses is its decision, within §4.3 item 9.
* **OQ-2** Closed in v3: the watermark advances before the run, on every outcome (§4.3, item 4).
* **OQ-3** ~~Is there a second global settings or token read the per-user loop would bypass?~~ **Closed 2026-10-22 from the code:** every engine settings read is per-user — `backend/engine/focus_time.go:76`, `auto_decline.go:66`, `habits.go:70, 96`, `smart_schedule.go:54, 288`, `compression.go:48`, `daily_recap.go:72`, `analytics.go:58`, `freebusy_service.go:75`, `personal_blocker.go:97` all go through the by-user read; the token read is by user (`backend/auth/token_store.go:40-49`); the string `id = 1` survives in non-test Go only inside generated-code comments (`backend/api/gen/paceday.gen.go:2120, 2226, 2274, 2385`) and a comment at `token_store.go:39`.
* **OQ-4** ~~Does the MCP server have an auto-schedule tool or read of these columns?~~ **Closed 2026-10-22 from the code:** one read-only settings tool, `clockwise_get_settings`, which performs the GET (`mcp/tools.go:233-238`); no write, no direct column read; `auto_schedule` does not occur under `mcp/`. The new property reaches it for free.
* **OQ-5** ~~Where exactly is the Google token stored today (§3.3)? Decides whether 024's reshape carries it for free or needs an extra move.~~ **Answered while landing (2026-10-21):** `oauth_tokens (user_id, calendar_id)`, per-user since migration 017 — see §3 item 3. No extra move.
* **OQ-6 (new, 2026-10-22) — daylight-saving edges.** *Owner: plan-author; resolve by a unit test pinned to a real transition, added beside AC-4a.* §4.3 item 3 evaluates slots in the user's zone. Two edge cases are not decided here because I have not read how the chosen parser steps across them: a slot that does not exist on the spring-forward day (`30 2 * * *` in a zone that skips 02:00–03:00) and a slot that exists twice on the fall-back day. The requirement is the §4.3 one — never twice for one slot, never skipped for a whole day — and the plan states which instant the library picks and proves it; if it skips the whole day, that is a finding against the parser choice, not an accepted behaviour.

## 10. Revision log

* v2 (2026-10-04, PR [Enach/clockwise-like#179](https://linear.app/paceday/review/specpac-49-focus-cron-must-act-for-exactly-the-people-who-asked-for-it-a6e5129984b7)): first full spec; recommended copying the cron expression but not `auto_schedule_enabled`.
* v3 draft (2026-10-21 am): rewritten after the "zero users / comprehensive fix" decision.
* v3 (2026-10-21 pm): after an internal challenge (12 findings across [PAC-49](https://linear.app/paceday/issue/PAC-49/listuserswithautoschedule-does-not-filter-user-id-so-the-focus-cron-is)/50) — enable sets `last_run = now` (first-tick bug); per-user token columns pulled into 024 (shared-token landmine); 1-minute non-reentrant ticker with `last_run` written before the run (double-run); `auto_schedule_ran` per-run audit with explicit `userID`; invalid timezone rejected at write; ACs 3–7 rewritten, 4b/6b/7b added.
* v3.1 (2026-10-22): response to the stage-1 challenge of v3 (appended below, after the revision note) — §1 rewritten around what is true post-#204; stored default kept at `'0 8 * * *'`; `cleared` dropped and the audit details defined; `code` on the write's 400 defined; PAC-23 made a stated blocker with AC-5/AC-6 under their own heading; edit-while-enabled and missed-slot rules stated with criteria; "What must not change" and "Rollback" restored; §4 stripped of implementation names; OQ-1/3/4 closed from the code; OQ-6 opened. Every finding is mapped in the `## Revision v3.1` section below.

---

## Revision v3.1 — 2026-10-22 (response to the v3 challenge)

Verdict answered: accept-with-changes. Every finding below was re-verified in
the code at `4dc801f` before the body was edited; nothing was executed. Where a
decision was taken by the orchestrator rather than by me it is marked
*(decision)*. The v2 body, v2's Challenge and the v3 Challenge below are
unchanged by a character.

### Blocking

1. **§1 false post-#204** — §1 rewritten around (a) restart-only, (b)
   server-local hour, (c) unattributed audit, (d) no last-run record, each
   with citations at `4dc801f`; one sentence closes the issue's original
   mechanism via #204; the seven wrong citations are corrected (see "Citations"
   below); the `ListFocusBlocksForWeek` out-of-scope bullet is struck; lineage
   item (c) corrected to the per-user recap join. *(decision)*
2. **Cron default contradiction** — the stored default stays `'0 8 * * *'` and
   026 adds the watermark only (§2 decision 4, §4.1); the `'0 7 * * 1-5'`
   end-state block and "weekdays at 07:00" are struck; weekday mornings is a
   §4.5 preset; the target-week rule is stated (§4.3 item 5) and weekend/past-day
   handling is explicitly out of scope. *(decision)*
3. **`cleared` unreachable** — a scheduled run never clears (§4.3 item 13,
   §3 out of scope, AC-14); `cleared` dropped from the details, which are now
   `{created, errors, error}` with each number's meaning defined (§4.3 item
   12); AC-6 rewritten so every value is one the loop can produce, including
   the all-days-fail case. *(decision)*
4. **`code` undefined** — §4.4: the envelope stays `{"error": msg}` and gains
   an optional `code` on the settings write's 400, `invalid_cron` /
   `invalid_timezone` for exactly those two existing messages, message
   unchanged, no other message gains one; AC-7 asserts the property and its
   absence on a third message. *(decision)*
5. **PAC-23 is a gate** — header bullet "Blocked by: PAC-23 (backend)" with
   the 025-before-026 ordering; §4.4 names the audit-fragment delta (eighth
   literal on PAC-23's operation, carrying API-005/API-074) and that PAC-49's
   contract revision is written on top of PAC-23's revision 4; AC-5 and AC-6
   moved under "Blocked on PAC-23", implemented after it, never skipped.
   *(decision)*
6. **Edits and missed slots** — §4.1: a save changing schedule or timezone
   while enabled resets the watermark to now, like enabling; a save changing
   neither leaves it alone; §4.3 item 8: a missed slot is caught up once at the
   first tick after restart, never more. Criteria AC-7c (edit), AC-7d
   (unrelated save), AC-4c (missed slot, including several missed). *(decision)*
7. **Rollback and must-not-change missing** — restored as §7 and §8, naming
   the manual run and its `focus_created` entry, PATCH merge semantics and the
   validation catalogue, the empty-user-id refusal, the per-user token read; and
   the revert's hour shift, the out-of-enum audit rows, the lossy down
   migration and the absent-property case for the frontend. *(decision)*

### Non-blocking

1. **Implementation names in §4** — §4 rewritten as requirements (minute
   granularity, no overlap, watermark-before-run on every outcome, the user's
   IANA zone, a settable clock, a ten-minute per-user bound); the Go signature,
   library, options, mutex, interface, `main.go` and helper names are gone.
   Contract-level names fixed by the orchestrator (column, wire properties,
   literal, codes, migration number) remain and are flagged as such.
2. **Non-existent `ListFocusBlocksForWeek` defect** — struck in §3 with the
   correction (`focus_blocks.go:30, 64` both take a user id).
3. **Which week a tick schedules** — §4.3 item 5: the week containing the tick
   instant, as the manual run does with no week given; past-day handling
   unchanged and out of scope; AC-4 asserts the target week. *(decision)*
4. **Two audit rows / per-day errors** — §4.3 item 12 says `auto_schedule_ran`
   is *in addition to* `focus_created`, AC-6 counts only the former, and the
   run's per-day failures map to `errors` with `error` reserved for the run's
   own failure. *(decision)*
5. **Lineage (c) stale** — corrected in the lineage bullet to the per-user
   join at `daily_recap.go:143-148`; (a) and (b) kept with their citations.
6. **AC-12's void escape hatch** — `test.fixme` clause removed; §4.3 item 10
   *requires* a way to tick the loop at a chosen instant in the e2e stack,
   mechanism left to the plan; AC-12 rewritten against it and against the
   PAC-43 stand-in that exists.
7. **`autoScheduleLastRun` on write** — §4.4: read-only, accepted and ignored
   like `llmApiKeySet`/`updatedAt`, so PAC-24's AC-10 holds; AC-7e and AC-11
   assert it.
8. **Concurrent write path** — §4.1: a settings save never carries the
   watermark; only enable/edit reset and the scheduler's advance write it;
   stated as behaviour so a save racing a tick cannot rewind it; AC-7e.
9. **No per-user time bound** — §4.3 item 7: ten minutes per run, as today;
   AC-4e.
10. **§4.5 overstated** — §4.5 now says the toggle and field exist and the
    delta is the two test ids, the presets, and one read-only last-run line
    with test id `settings-autoschedule-lastrun`; AC-13 says who sees the
    value.
11. **Parser agreement** — §4.3 item 9: the loop uses the write's five-field
    parser; a stored expression it rejects (e.g. `@daily`) is skipped with a
    warning and no audit row, like an invalid timezone; AC-4d.
12. **OQs answerable from the code** — OQ-1, OQ-3, OQ-4 closed in §9 with the
    challenger's citations re-verified (go.mod:21 and the two imports; the
    eleven per-user engine reads and the by-user token read; the single
    read-only MCP tool).

### Criteria the challenger could not falsify

- **AC-1, AC-2, AC-8** — kept, labelled "regression guard" in §5 with the
  reason (the `Reload` tests that prove them today are deleted with `Reload`);
  AC-1 additionally asserts the new column's `null`.
- **AC-6** — rewritten without `cleared` (blocking 3).
- **AC-7** — rewritten against the defined `code` property (blocking 4).
- **AC-11** — now names the new items: `autoScheduleLastRun`, the `code`
  property and its two values, and the `auto_schedule_ran` literal on top of
  PAC-23's revision 4.
- **AC-12** — now rests on a required tick seam (non-blocking 6).

### Citations the challenger found wrong

All seven corrected in the body: `settings.go:262,271` → the per-user read
creates the row at `:237-242` and the write is `SaveSettingsForUser` at `:279`;
"`RunForUser` dies at `no settings row`" → it calls the by-user read at
`focus_time.go:76` and `focus_time_test.go:401-413` asserts success;
"`WHERE id = 1` everywhere" → no such read remains outside comments;
`ListFocusBlocksForWeek` → struck; "400 `invalid_cron`" as an existing thing →
defined as a new optional property; lineage (c) → `daily_recap.go:143-148`;
`DEFAULT '0 7 * * 1-5'` → `'0 8 * * *'` (`001_initial.up.sql:17`,
`scheduling.yaml:1752`). One refinement to the challenge's own text: "no `TZ`
is set in any `docker-compose*.yml`" is true of the three top-level files; the
e2e stack does pin `TZ: UTC` (`e2e/docker-compose.e2e.yml:111`), which is why
§1(b) names the three.

### Consumers

The challenge's list is adopted: the four `Reload` tests go and §7 names what
replaces them; the activity-log enum gains a literal that the UI renders as
`unknown`; the client's derived PATCH key set will include
`autoScheduleLastRun`, which accepted-and-ignored makes safe (§4.4); the MCP
read-only tool gains a property; AC-9(b) says in terms that the seed exercises
no data in the new column.

---

## Challenge — 2026-10-22 (v3)

**Verdict**: accept-with-changes

Challenged at `a896faa` (worktree clean apart from this file). Every citation
below was re-read in the code; `go build ./... && go vet` of `scheduler`,
`engine` and `storage` pass at that commit. No Docker-backed test was run, by
instruction, so nothing here claims to have executed a migration or a cron
tick. The Linear issue itself was not reachable from this session; the problem
statement I compared against is the one v2 §1 restates, which v2's challenge
checked against the issue.

The decisions in v3 §2 are not under challenge and the shape of §4.3 (a
minute-granular, non-reentrant loop that advances a per-user watermark *before*
running, in the user's own timezone) is sound. What blocks is that v3's §1
describes a world #204 removed, one acceptance criterion asks for a number no
conforming implementation can produce, one names a wire property that does not
exist, and the dependency on PAC-23 is a contract gate rather than the aside the
spec makes of it.

### Blocking

1. **§1 "Problem" is false at `a896faa`, all three bullets, and the live
   problem is not stated.** (i) "exactly one `auto_schedule_enabled` flag ...
   owned by nobody": migration 024 deleted every ownerless row and made
   `user_id` NOT NULL (`backend/storage/migrations/024_settings_per_user.up.sql:140,144`);
   each user's own row carries the flag. (ii) "no production path creates a
   per-user settings row ... `RunForUser` dies at `no settings row`":
   `GetOrCreateSettingsByUser` inserts the row on first read
   (`backend/storage/settings.go:242`), `RunForUser` calls it
   (`backend/engine/focus_time.go:76`), and
   `TestFocusRun_UserWithNoPriorSettings` (`backend/engine/focus_time_test.go:401-413`)
   asserts the opposite of the spec's claim. (iii) "every user whenever anyone
   turned it on": `ListUsersWithAutoSchedule` returns per-user rows
   (`settings.go:411-416`), `RunForUser` injects that user's id into ctx
   (`focus_time.go:74`) and `newCalOps` loads that user's token
   (`backend/engine/cal_iface.go:53-58`). The reality-check header concedes
   §2.1/§4.1/§6 are moot but leaves §1 standing, so the next reader is told the
   landmine is live. What *is* still true for a user today, and what v3 should
   name as its problem: (a) switching it on does nothing until someone restarts
   the process — `Reload()` has one call site, `Start()`
   (`backend/scheduler/cron.go:73-76`, `backend/main.go:69-70`); (b) when it
   does run, it runs at the **server's** local hour, not the user's —
   `cron.New()` at `cron.go:27` takes no `WithLocation`, so robfig uses
   `time.Local` (`robfig/cron/v3@v3.0.1/cron.go:124`), no `TZ` is set in any
   `docker-compose*.yml`, and a Paris user's `0 7 * * 1-5` fires at 09:00 Paris
   in summer; (c) the run is unattributed in the audit log
   (`backend/storage/focus_blocks.go:74-75`); (d) there is no record of when it
   last ran. *Resolves it*: rewrite §1 around (a)–(d), naming the affected user
   and the observable change, and say in one sentence that the issue's stated
   mechanism (a missing `user_id` predicate) is closed by #204's constraint.

2. **§4.1 contradicts itself on the cron default, and the two readings differ
   in user-visible behaviour.** "Migration 026 adds the third column and
   nothing else" cannot coexist with the "intended end state"
   `DEFAULT '0 7 * * 1-5'` and the prose "Default: weekdays at 07:00". The
   stored default is `'0 8 * * *'` (`001_initial.up.sql:17`), the contract
   documents it as such (`contracts/openapi/paths/scheduling.yaml:1752`), and
   nothing in v3 changes it. The difference matters: `'0 8 * * *'` fires on
   Saturday and Sunday, and a tick on those days hands `RunForUser` a week that
   is already over (`startOfWeek`, `focus_time.go:328-334`, used at `:86`;
   the five-day loop at `:94`). *Resolves it*: state one default. If it is the
   weekday one, 026 changes the column default, AC-1 asserts it, and the
   contract's "DB default" text is part of the delta; if not, strike the
   `'0 7 * * 1-5'` line and the "weekdays at 07:00" sentence.

3. **AC-6's `cleared: 1` is unreachable under §4.3, so the criterion cannot
   be written.** The loop calls only `RunForUser`, and `RunForUser` never
   deletes anything — `processDay` creates (`focus_time.go:178`) and saves
   (`:184`), nothing else. Clearing is `ClearWeek`
   (`backend/engine/focus_time_cleaner.go:16`), whose one caller is the HTTP
   handler (`backend/api/handlers_focus.go:81`). Either a scheduled run is
   meant to clear the week's existing blocks and re-plan — a behaviour change
   a user notices (a block they kept vanishes every morning) that must be
   stated as desired behaviour with its own criterion — or `cleared` does not
   belong in the details. *Resolves it*: decide, state it in §3 and §4.3, and
   rewrite AC-6 so that each number in the details is one the loop can produce.

4. **AC-7 names an error `code` that the envelope does not have, so a test
   cannot tell pass from fail.** `writeError` emits `{"error": <msg>}` and
   nothing else (`backend/api/handlers_settings.go:371-375`); the contract's
   `SchedulingErrorResponse` (`scheduling.yaml:875-883`) and the
   `SettingsValidationError` catalogue (`:797-835`) are catalogues of
   *messages*. Both messages AC-7 wants already exist and already return 400:
   `autoScheduleCron ...` at handler `:247-253` / contract `:813-814`, and
   `timezone is not a known IANA location` at handler `:222-224` / contract
   `:831`. The only new thing in AC-7 is the word `invalid_cron`, and the spec
   never says where it appears — a new property on every 400 from this
   operation, a replacement for the message, or a prefix inside it. The one
   shipped client reads `error`, `message` or `detail`
   (`smart-calendar-flow/src/api/client.ts:328-329`). *Resolves it*: state the
   observable — which property carries the code, whether the message stays,
   and whether codes are added for these two messages only or for the whole
   catalogue — and then AC-7 is a one-line test.

5. **The PAC-23 dependency is a contract gate, and the spec treats it as an
   aside.** At `a896faa`: `WriteAuditLog(db, action, details)` has no user
   (`focus_blocks.go:74-75`), `audit_log` has no `user_id`
   (`001_initial.up.sql:44-49`), no migration 025 exists (the directory ends
   at 024), PAC-23's spec is `Status: draft` (`docs/specs/PAC-23.md:4`) and
   only its contract revisions are on main (`d8d346a`), not its code. So AC-5
   and AC-6 cannot be written before PAC-23's stage 4 lands — the spec says as
   much in three scattered places (§3 item 5, §4.3 bullet 3, struck §6 item 5)
   without once writing "blocked by". It is stricter than that: the eighth
   literal `auto_schedule_ran` is a change to the closed `AuditEntryAction`
   enum of `GET /api/audit` (`contracts/openapi/paths/calendar.yaml:1668`;
   `backend/api/gen/paceday.gen.go:534-536`: "adding an eighth action is a
   contract change"), and that operation carries API-005 (critical, Confirmed,
   `docs/factory/api-audit.md:239`) and API-074 (`:308`), both PAC-23's.
   Factory §7 rule 1 and `make contract` gate 3 refuse a feature that touches
   that operation while those are open. §4.4's "the contract delta is
   `autoScheduleLastRun` plus the two error codes" also omits this audit
   fragment delta. *Resolves it*: a header bullet "Blocked by: PAC-23
   (backend)"; §4.4 names the audit-fragment delta; AC-5/AC-6 move under a
   "blocked on PAC-23" heading, or the spec states what a test asserts about
   attribution before PAC-23 lands (nothing can, so the heading is the honest
   option).

6. **Editing the schedule while enabled re-opens the "runs on the next tick"
   case the spec closes for enabling.** §4.1 sets `last_run = now` on enable
   so that "the first run is the user's *next* cron slot, not the next tick";
   AC-7b then requires that a PATCH leaving the flag true "does not touch
   `last_run`". A user whose last run was yesterday 07:00 changes
   `0 7 * * 1-5` to `0 5 * * 1-5` at 09:00: `next` after yesterday 07:00 is
   today 05:00, which is `< now`, so they run at the next tick. A timezone
   change westwards does the same. So does a process that was down across a
   slot: the first tick after restart runs everyone whose slot was missed.
   None of these is wrong on its face, but the spec's own principle covers one
   of them and AC-7b locks the others in without saying so. *Resolves it*:
   state the rule for a cron or timezone edit while enabled and for a missed
   slot (catch up, or wait for the next one), and a criterion for each.

7. **Rollback and "what must not change" are missing.** Factory §2 stage 1
   requires both; v2 had §6 and §7, v3 has only "Down migration (026): drop
   `auto_schedule_last_run`". Rolling the release back brings back
   `cron.go`'s Reload-at-start loop in server-local time (`cron.go:27, :53`):
   every user who enabled under v3 in their own timezone silently shifts hours,
   and rows already written with `auto_schedule_ran` sit outside the old
   contract's closed enum (`gen.go:536`). *Resolves it*: a Rollback section
   naming those two effects, and a short list of what stays the same —
   `POST /api/focus/run` and its `focus_created` row, PATCH merge semantics,
   `RunForUser`'s refusal of `uuid.Nil`.

### Non-blocking

1. **§4 names files, packages and function signatures, which the factory
   forbids at stage 1** (`docs/factory/README.md:66-69`, §5 table). §4.2 is a
   Go signature; §4.3 names `robfig`, `SkipIfStillRunning`, a `TryLock`,
   `engine.Clock` and `main.go`; §4.4 names `writeError`. README §0 says the
   README wins and I say so: stages 2 and 3 must read §4 as illustration, not
   decision. The *requirements* it encodes — minute granularity, no overlap,
   watermark advanced before the run on every outcome, the user's IANA zone, a
   clock a test can set — are good and should be stated as requirements.
2. **§3's third out-of-scope bullet names a defect that does not exist.**
   `ListFocusBlocksForUserWeek(db, userID, …)` (`focus_blocks.go:30`) and
   `FocusMinutesForDay(db, userID, date)` (`:64`) both take a user id; there is
   no `ListFocusBlocksForWeek` in `backend/` or `mcp/`. Strike it.
3. **Which week a tick schedules is unstated.** Today the cron passes
   `time.Now()` (`cron.go:56`), i.e. the current week, and `processDay` has no
   past-day guard, so a Wednesday run re-plans Monday and Tuesday. AC-3/AC-4
   need to know the week to assert anything about created blocks.
4. **Two audit rows per successful cron run, and per-day errors vanish.**
   `RunForUser` writes `focus_created` itself (`focus_time.go:103-108`), so
   the loop's `auto_schedule_ran` is a second row, not a replacement as §4.3's
   wording suggests; say so, since AC-6 counts rows. And `RunForUser` returns
   `nil` with the failures inside `result.Errors` when every day fails to list
   or create (`:98-100`, `:178-180`), so `{error: null}` would be written for a
   run that did nothing. State how `result.Errors` maps into the details.
5. **Lineage item (c) is stale.** `DailyRecapService.recapUsers` joins
   `st.user_id = u.id` (`backend/engine/daily_recap.go:143-148`) since #204;
   there is no PAC-24 plan revision 3 to keep it in. Items (a) and (b) still
   hold (`cal_iface.go:53-58`; `cron.go:73-76` + `main.go:69-70`), and (b) is
   the live half of the problem — see blocking 1.
6. **AC-12's escape hatch is void.** PAC-43 landed (#195, #196;
   `backend/main.go:35-49`; `e2e/fake-google/`; `e2e/SEAM-REQUIRED.md:3`
   "item A is done"), so "may stay `test.fixme` until PAC-43's calendar seam
   lands" permits nothing, and factory §3 rule 4 requires an issue on any
   `fixme` regardless. What AC-12 does still lack is the thing it calls "a
   forced tick (test hook or advanced clock)": the e2e stack cannot wait for
   07:00, so the spec must *require* a way to make the loop tick at a chosen
   instant, as a requirement, and leave the mechanism to the plan.
7. **`autoScheduleLastRun` on write is undecided, and PAC-24's rules make the
   two options differ.** The PATCH contract says `null` is a 400
   (`scheduling.yaml:1987`) and that read-only `llmApiKeySet`/`updatedAt` are
   accepted and ignored (`:1984`) so a GET document can be submitted back
   unchanged. The spec adds a nullable read-only property to the same shared
   anchor without saying which rule it follows. The shipped client diffs
   against the cached previous document (`smart-calendar-flow/src/hooks/useSettings.ts:16`,
   `src/api/client.ts:647-656`), so it would not echo it today, but
   `SETTINGS_PATCH_KEYS` is derived from the generated `SettingsUpdate` minus
   two hard-coded names (`client.ts:636-637`) and will include the new one.
   State "accepted and ignored", or state that PAC-24's AC-10 no longer holds.
8. **Concurrent write path.** `SaveSettingsForUser` upserts the whole row
   (`settings.go:279-372`). If `auto_schedule_last_run` becomes a column that
   write carries, a PATCH that read the row before a tick advanced the
   watermark writes the stale value back, and the user is run again at the
   next tick. AC-7b's "does not touch `last_run`" should be stated to hold
   under a concurrent tick, which is a requirement, not a design.
9. **No per-user time bound.** Today each run has a 10-minute context
   (`cron.go:54`); §4.3 has none, and with a non-reentrant loop one hung token
   refresh holds every user behind it. State the bound AC-4b assumes.
10. **§4.5 overstates the frontend delta.** The toggle and cron field exist
    (`smart-calendar-flow/src/pages/Settings.tsx:393-412`); what is missing is
    test ids (zero `data-testid` in `src/`, `e2e/TESTIDS-REQUIRED.md:7-9`),
    presets, and a decision on whether anyone *sees* `autoScheduleLastRun`.
    The spec puts it on the wire and never says who reads it; if nobody
    renders it, say what it is for.
11. **Parser agreement.** The handler parses with
    `Minute|Hour|Dom|Month|Dow` only (`handlers_settings.go:250`), which
    rejects descriptors; robfig's standard parser used by `AddFunc` accepts
    them, and `cron_test.go:83-84` seeds `@weekly`/`@daily` straight into the
    table. §4.3 says the loop and the write share a parser (good); say what the
    loop does with a stored expression the write-side parser rejects, as it
    already does for timezone (skip, warn, no audit).
12. **Open questions I can answer from the code.** OQ-1: `robfig/cron/v3
    v3.0.1` is in `backend/go.mod:21` and already imported by the handler
    (`handlers_settings.go:16`) and the scheduler; no dependency is added.
    OQ-3: every engine settings read is `GetOrCreateSettingsByUser(userID)`
    (`focus_time.go:76`, `auto_decline.go:66`, `habits.go:70,96`,
    `smart_schedule.go:54,288`, `compression.go:48`, `daily_recap.go:72`,
    `analytics.go:58`, `freebusy_service.go:75`, `personal_blocker.go:97`) and
    the token read is `LoadUserToken(db, userID)` by `user_id`
    (`backend/auth/token_store.go:40-49`); no `id = 1` read remains in
    non-test Go (the string survives only in a comment, `token_store.go:39`).
    OQ-4: the MCP server has one settings tool, `clockwise_get_settings`, a
    GET through HTTP (`mcp/tools.go:233-238`); no write, no direct column
    read — the new property reaches it for free.

### Criteria I could not falsify

- AC-1: already true at `a896faa` — default `FALSE` (`001_initial.up.sql:16`)
  and the query returns nothing on an empty table (`settings.go:411-416`). A
  regression guard; only "after migration 026" is new.
- AC-2: already true — per-user rows since 024, and
  `TestFocusCron_Reload_PerUserEntries` (`cron_test.go:81-101`) proves the
  listing is per-user today. Regression guard.
- AC-6: `cleared: 1` cannot occur (blocking 3).
- AC-7: both 400s exist today (handler `:222-224`, `:247-253`); only `code`
  is new and its location is unstated (blocking 4).
- AC-8: already true — the handler reads and writes the caller's row
  (`handlers_settings.go:72, 137`); PAC-24's tests cover it. Regression guard.
- AC-11: "documents both properties" — `autoScheduleEnabled`/`autoScheduleCron`
  are documented already (`scheduling.yaml:1745-1752`); the new items are
  `autoScheduleLastRun`, the code(s) and the audit literal. Name them.
- AC-12: depends on a tick seam the spec does not require (non-blocking 6).

### Citations I checked and found wrong

- §1 "`INSERT INTO settings` only at `storage/settings.go:262,271`" — now at
  `:242` (`GetOrCreateSettingsByUser`) and `:291` (`SaveSettingsForUser`).
- §1 "`RunForUser` dies at `no settings row for user <id>`
  (`focus_time.go:82-84`)" — `:76` calls `GetOrCreateSettingsByUser`; the
  error string no longer exists in non-test code; `focus_time_test.go:401-413`
  asserts success for a user with no row.
- §1 "the settings singleton (`WHERE id = 1` everywhere)" — no such read
  remains (only the comment at `token_store.go:39`).
- §3 "`ListFocusBlocksForWeek` / `FocusMinutesForDay` (they take no user
  id)" — `focus_blocks.go:30, :64` both take `userID`; the first name does not
  exist.
- §4.4 / AC-7 "400 `invalid_cron`", "the two 400 codes" — the envelope has no
  code property (`handlers_settings.go:371-375`; `scheduling.yaml:875-883`).
- Lineage (c) "`DailyRecapService.RunAll` joins every user against the
  `id = 1` row" — `daily_recap.go:143-148` joins `st.user_id = u.id`.
- §4.1 end-state `DEFAULT '0 7 * * 1-5'` — stored default is `'0 8 * * *'`
  (`001_initial.up.sql:17`), contract agrees (`scheduling.yaml:1752`).
- Verified correct, for the record: `cal_iface.go:53-58` token read;
  `focus_time.go:74` ctx injection and `:71-73` `uuid.Nil` refusal;
  `UserIDFromContext` returns `uuid.Nil`, not an error
  (`backend/auth/context.go:19-24`); `engine.SystemClock` is a package var
  nothing in `main.go` replaces (`backend/engine/clock.go:19`); `oauth_tokens`
  per `(user_id, calendar_id)` since 017; `PUT /api/settings` removed, PATCH is
  a merge (`scheduling.yaml:573-581, 627`); migration numbering 025/026/027
  matches `docs/factory/PAC-23-plan.md:29-32` and `docs/specs/PAC-50.md:9`.

### Consumers this breaks

None outright. Consumers of what changes, so the plan does not discover them:

- `backend/scheduler/cron.go:37` — sole caller of `ListUsersWithAutoSchedule`;
  replaced wholesale. `backend/scheduler/cron_test.go:72-130` tests `Reload`
  and `entryIDs`, both of which §4.3 deletes; the spec should say those tests
  go and what covers the loop instead.
- `GET /api/audit` consumers of the `AuditEntryAction` enum — the generated
  type (`gen.go:48-49`, `smart-calendar-flow/src/api/generated/types.ts`)
  gains a literal; the one UI treats `action` as an opaque string
  (`smart-calendar-flow/src/api/client.ts:451`), so nothing fails to compile.
- `smart-calendar-flow/src/api/client.ts:636-637, 647-656` and
  `src/hooks/useSettings.ts:16` — see non-blocking 7; not broken, but the
  spec's silence on `autoScheduleLastRun`-on-write decides whether it stays
  that way.
- `mcp/tools.go:233-238` — gains a read-only property; no break.
- `e2e/seed/seed.sql:105-115` — nine per-user settings rows, none with
  auto-schedule on; AC-9(b) is realisable but exercises no data in the new
  column, which is fine if the spec says so.
