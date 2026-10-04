# Spec — PAC-49: automatic focus scheduling must act for exactly the people who asked for it, and the migration that says so must be telling the truth

- **Linear**: https://linear.app/paceday/issue/PAC-49/listuserswithautoschedule-does-not-filter-user-id-so-the-focus-cron-is
- **Status**: challenged, revised (v2)
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
