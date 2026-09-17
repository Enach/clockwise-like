# Spec — PAC-23: the activity log must belong to the person it is about

- **Linear**: https://linear.app/paceday/issue/PAC-23/add-user-id-to-the-audit-log-table-and-scope-get-apiaudit-to-the
- **Status**: draft
- **Author**: spec-author
- **Inherited scope**:
  - **Resolved by this spec**: API-005 (critical), `x-uncertain` U-02, API-074 (low —
    the two different "default limits"; it sits on the one operation this spec
    modifies, so factory §7 puts it in scope).
  - **Discovered while writing this spec, filed rather than fixed**: the `details`
    payload is assembled by string concatenation of untrusted text (§2.6), and the
    table is being asked to be two different things at once (§3.5). Both need their
    own issues — OQ-6, OQ-7.
  - **Inherited, explicitly not resolved**: nothing else is open on this operation.

> **A note on this spec's own evidence.** The sandbox this spec was written in blocks
> the Go module proxy, so nothing here was established by compiling, running or
> querying anything. Every claim in §2 comes from reading source and carries a
> `file:line`. Where a claim is an inference rather than an observation it says so.

---

## 1. Problem

Paceday keeps a log of the things it does to people's calendars — focus blocks it
created, meetings it scheduled or moved, and the natural-language requests people
typed at it. The product shows this to each person as "the last 50 actions Paceday
performed on **your** calendar".

It is not their calendar. It is everybody's.

There is one log for the whole deployment, and every authenticated user reads all of
it with a single request to a documented endpoint. What they read is not a list of
opaque identifiers. It contains the **titles of colleagues' meetings** and the
**verbatim text of what colleagues typed into the scheduling box**. "1:1 re: PIP".
"call with the recruiter at $COMPETITOR". "find 30 minutes with legal before Friday".
Those are the strings, unaltered, sitting behind a GET that any employee can make.

**Who is affected.** Every user of every multi-user deployment, and they are affected
in both directions at once: each of them can read their colleagues, and each of them
is being read. The people worst affected are the ones with the most sensitive
calendars — anyone conducting a performance process, an investigation, a
reorganisation, a job search, or a medical appointment they named honestly.

**What it costs them.** Unlike a leaked credential, this cannot be rotated. Once a
colleague has read that someone has a meeting titled "1:1 re: PIP", nothing the
engineering team does afterwards unreads it. The cost is already incurred for every
row already in the table, and it continues to be incurred for as long as the endpoint
stays open. That is what makes this urgent rather than merely important.

**Why it is not a one-line fix.** The natural remedy — return only the caller's own
entries — cannot be written, because there is nothing in the stored record that says
whose entry it is. The log is not merely unscoped; it is **unscopable**. Every row in
the table today is, permanently and by construction, unattributable.

That has a consequence this spec has to confront rather than skirt: there is no
version of this work in which the existing history both stays visible and becomes
correctly scoped. One of those two has to give.

---

## 2. Current behaviour

Nothing below was executed. All citations are from reading.

### 2.1 The stored record has no notion of who

The table is three columns and an id:

```
audit_log (id SERIAL, action TEXT, details TEXT DEFAULT '', created_at TIMESTAMPTZ)
```

— `backend/storage/migrations/001_initial.up.sql:44-49`. No user column, no org
column, and no later migration adds one: migrations run to `023` and none of them
mention `audit_log` (`backend/storage/migrations/`). The Go struct mirrors it
(`backend/storage/models.go:23-28`, and the read-side copy at
`backend/storage/audit_log.go:8-13`).

This is not an oversight the rest of the schema shares. Migration `006_auth`
retrofitted `user_id` onto the tables that needed it in one pass — `oauth_tokens`,
`settings`, `focus_blocks` (`006_auth.up.sql:14-16`) — and `audit_log` was not among
them.

### 2.2 The read is unfiltered

`ListAuditLog` is the whole of the read path:

```
SELECT id, action, details, created_at FROM audit_log ORDER BY created_at DESC, id DESC LIMIT $1
```

— `backend/storage/audit_log.go:19`. There is no `WHERE`. The handler passes only the
limit (`backend/api/handlers_audit.go:15`) and encodes the slice straight to the
response (`:20-21`). The route sits inside the `requireAuth` group
(`backend/api/routes.go:179`, group opened at `:63-64`), so the only gate is
"authenticated at all".

The caller's identity **is** available to the handler — `requireAuth` puts the user's
UUID in the request context (`backend/api/middleware.go:53`) — and the handler does
not read it.

### 2.3 The write is a fire-and-forget three-argument call

```go
func WriteAuditLog(db *sql.DB, action, details string) {
	_, _ = db.Exec(`INSERT INTO audit_log (action, details, created_at) VALUES ($1, $2, NOW())`, action, details)
}
```

— `backend/storage/focus_blocks.go:61-63`. Note the discarded error: an insert that
fails for any reason is silently lost. Nothing logs it. This matters for §3.4.

### 2.4 There are seven writers, and — contrary to the issue's premise — every one of them has the user in scope

The issue and the audit register (`docs/factory/api-audit.md:509-511`) both anticipate
that some writers will have no user available. I checked all seven call sites and the
transitive callers of each. **None of them lacks one.** This is the single most
consequential finding in this section, because it removes the need for a nullable
column, a sentinel actor, or a "system" pseudo-user in new rows.

| # | Writer | `action` | Is a user in scope? |
|---|---|---|---|
| 1 | `backend/engine/focus_time.go:106` (`RunForUser`) | `focus_created` | **Yes, as a parameter.** `RunForUser(ctx, userID, …)` refuses `uuid.Nil` (`:71-73`) and puts the id in ctx (`:75`). Both entry points reach it: the HTTP path via `Run` (`:59-65`) and the cron path, which calls `RunForUser` explicitly per user (`backend/scheduler/cron.go:52-57`). |
| 2 | `backend/engine/focus_time_cleaner.go:39` (`ClearWeek`) | `focus_cleared` | **Yes, in ctx.** Its only non-test caller is `backend/api/handlers_focus.go:81`, passing `r.Context()` from inside `requireAuth`. Not reachable from cron. |
| 3 | `backend/engine/smart_schedule.go:323` (`CreateMeeting`) | `meeting_scheduled` | **Yes, in ctx.** Callers: `backend/api/handlers_schedule.go:182` and `backend/api/handlers_nlp.go:61`, both authenticated. |
| 4 | `backend/engine/compression.go:178` (`Apply`) | `meeting_moved` | **Yes, in ctx.** Sole caller `backend/api/handlers_schedule.go:96`. |
| 5 | `backend/api/handlers_schedule.go:188` | `meeting_created` | **Yes.** Has `r`; the id is in `r.Context()`. |
| 6 | `backend/api/handlers_nlp.go:67` | `nlp_confirmed` | **Yes.** Same. |
| 7 | `backend/nlp/parser.go:221` (`NLPService.Parse`) | `nlp_parsed` | **Yes, in ctx.** Sole caller `backend/api/handlers_nlp.go:28`. |

**The cron path is not user-less.** This is worth stating separately because it is the
assumption the issue is built on. `FocusCron.Reload` enumerates users with
auto-schedule enabled and registers one cron entry **per user**, each closing over
that user's id and calling `RunForUser(ctx, userID, …)`
(`backend/scheduler/cron.go:36-68`). A focus run initiated by the clock still has a
subject, and the code already refuses to run without one.

**The other cron services write nothing at all.** `AutoDeclineService.RunAll`
(`backend/engine/auto_decline.go:30`), `PersonalBlocker.SyncAllForUser`
(`backend/engine/personal_blocker.go:47`) and `DailyRecapService.RunForUser`
(`backend/engine/daily_recap.go:71`) contain no `WriteAuditLog` call — verified by
grep across `backend/`. They are user-scoped by construction in exactly the same way
(each takes or derives a `userID`), so when somebody does add audit entries to them,
the subject will be available there too.

So the honest statement of today's position is: **there is no system-initiated audit
entry in this codebase, and every writer that exists could name a user today if it
were asked to.** §3.2 still has to decide what the rule is for one, because the
schema outlives this list of writers.

### 2.5 Nothing that exists can tell you whose the old rows are

I looked for a way to attribute the rows already stored, because "backfill by
inference" is the option the reader will reach for first.

- `focus_created` / `focus_cleared` — `details` carries `week_start`, `created`,
  `total_minutes`, `cleared`, `errors` (`focus_time.go:101-105`,
  `focus_time_cleaner.go:33-38`). No identifier of any kind. A *heuristic* is
  conceivable — `focus_blocks` does have `user_id` and `created_at`
  (`006_auth.up.sql:16`, `001_initial.up.sql:35-42`), so one could guess by timestamp
  proximity — and it would be a guess.
- `meeting_scheduled` / `meeting_created` / `meeting_moved` — `details` carries a
  Google event id and a title (`smart_schedule.go:323`, `handlers_schedule.go:188`,
  `compression.go:178`). There is **no table in this schema mapping a Google event id
  to a user**; `focus_blocks` maps only ids that Paceday itself created as focus
  blocks, which these are not. Nothing to join to.
- `nlp_parsed` / `nlp_confirmed` — `details` carries the prompt text and the intent,
  or an event id (`nlp/parser.go:221`, `handlers_nlp.go:67`). Nothing.

So inference is not merely distasteful, it is **infeasible for five of the seven
action types and heuristic for the other two**. §3.3 takes a position on it anyway,
because "we couldn't" is a weaker answer than "we wouldn't".

### 2.6 `details` is built by string concatenation, and the values are not escaped

Found while reading §2.5; not in the register.

```go
storage.WriteAuditLog(e.DB, "meeting_scheduled", `{"event_id":"`+created.Id+`","title":"`+req.Title+`"}`)
```

— `backend/engine/smart_schedule.go:323`, and identically at
`backend/api/handlers_schedule.go:188` and `backend/engine/compression.go:178`. A
meeting title containing a double quote, a backslash or a newline produces a `details`
string that is not valid JSON. (`nlp/parser.go:221` is the exception — it uses `%q`,
and the focus writers use `json.Marshal`.)

The consequence today is cosmetic: the column is a `TEXT` and the frontend renders it
as text without parsing (`smart-calendar-flow/src/api/client.ts:410-419`,
`src/pages/Audit.tsx:103`). It stops being cosmetic the moment anybody parses
`details` — which is what any redaction, filtering or structured rendering of it would
have to do. It is a *reason not to* extend this payload, and it is why OQ-6 exists.

### 2.7 The consumer already promises the behaviour that does not exist

There is exactly one consumer, and it is not neutral on this question. The audit page
is headed "Audit log" and subtitled **"The last 50 actions Paceday performed on your
calendar"** (`smart-calendar-flow/src/pages/Audit.tsx:39-40`), with an empty state
reading "No activity yet" (`:85`). The compact panel in
`src/components/QuickActions.tsx:104-150` shows the ten most recent under a heading
users will read the same way.

So the product's user-facing claim is already per-user. The server is what disagrees.
This is not a restriction being introduced; it is a promise being kept.

Neither the MCP server nor the e2e suite calls this endpoint — no match for `audit` in
`mcp/` at all, and the only `e2e/` hits are two unrelated uses of the English word
(`e2e/auth.ts:29`, `e2e/SEAM-REQUIRED.md:209`).

### 2.8 The two "defaults" (API-074)

The frontend defines `DEFAULT_AUDIT_LIMIT = 50` and always sends it
(`smart-calendar-flow/src/api/client.ts:404`, `:1060-1064`; asserted by
`src/api/audit.test.ts:38-44`). The backend, when the parameter is absent or
unparseable, ends up at **100**: `strconv.Atoi`'s error is discarded so the value
becomes 0 (`handlers_audit.go:14`), and `ListAuditLog` replaces anything `<= 0` **or
`> 500`** with 100 (`audit_log.go:16-18`). The contract records 100 as the default
(`contracts/openapi/paths/calendar.yaml:709-722`).

Two behaviours here are worth separating. The *drift* is that two numbers are both
called "the default". The *surprise* is the clamp: a caller asking for 1000 receives
100 — a number smaller than the documented maximum of 500 — with no error.

### 2.9 What is tested today

- **Nothing at the HTTP boundary.** There is no `handlers_audit_test.go`; no file in
  `backend/api/*_test.go` references the audit handler.
- **Nothing at the storage read path.** There is no `audit_log_test.go`.
- **One smoke test on the write path**: `TestWriteAuditLog`
  (`backend/storage/focus_blocks_test.go:102-107`) calls the function twice and
  asserts only that it does not panic. It does not read anything back.
- **Frontend**: `src/api/audit.test.ts` covers the request shape, the limit parameter,
  the error paths and the normalisation — seven cases. All of them are about transport
  and none about scope, which is correct, because scope is not currently observable
  from the client.

The operation is `handwritten` in `contracts/openapi/MIGRATION.md:75`, and it carries
the unresolved `x-uncertain` U-02 (`contracts/openapi/paths/calendar.yaml:705-707`),
which asks whether the global log is intentional. This spec's answer is that it is
not.

---

## 3. Desired behaviour

### 3.1 The log is the reader's own

Someone who opens their activity log sees the things that were done to their own
calendar and nothing else. A colleague's meeting title, and the text a colleague typed
into the scheduling box, are not reachable from any request they can make — not by
asking for more entries, not by asking for a different page, not by any parameter.
There is no request a normal user can compose that returns another person's row.

A reader with no activity yet sees an empty log, which is the same thing they see
today when the deployment is new, and is distinguishable from an error.

### 3.2 Every new entry names the person it is about, and that is enforced

From the day this ships, every recorded action carries the identity of the person
whose calendar it concerns, and an action that cannot name one is **not silently
recorded against nobody**. Recording an unattributable row is how the current problem
was created, and the system must not be able to create another one, even by accident
and even in code written later.

The person an entry names is the **subject** — whose calendar it happened to — not
necessarily whoever pressed the button. Today those are always the same person (§2.4).
They will not always be: a manager acting on a report's calendar, or a support
operator acting on a customer's, makes them differ. This spec deliberately fixes the
meaning as *subject*, because the log's only consumer is a person asking "what
happened to my calendar" (§2.7), and an entry filed under the actor would be missing
from the log of the person it actually happened to — which is the failure mode that
matters. When actor and subject first diverge, recording the actor as well becomes
necessary; this spec says plainly that it is **not** doing that now and that a log
which cannot distinguish them must not be presented to anyone as evidence of who did
something.

An action initiated by the clock rather than by a person is still about somebody's
calendar, and it appears in that person's log, described as something the system did
rather than something they did. It does not get a placeholder identity. There is no
"system user", because a fabricated identity in a record whose purpose is attribution
is worse than an absence: an absence is visibly unknown, a fabrication is invisibly
wrong.

An action that is genuinely about no individual — something deployment-wide — does not
belong in this log at all, and the right response to wanting one recorded is to build
the thing described in §3.5, not to widen this.

### 3.3 The existing history is kept, and shown to nobody

The entries already stored cannot be attributed (§2.5). Three things could be done
with them, and this spec picks the third.

**Not attributed by inference.** Two of seven action types could be guessed at from
timestamp proximity and five could not, which would produce a log in which some rows
are true, some are guesses, and **no reader can tell which is which**. A record that
might be about you is worse than no record, and a log that silently mixes the two
destroys the credibility of the rows that were right. The rule this spec asserts is
simple and should outlive it: *an attribution is recorded because it was known at the
time, never because it was reconstructed afterwards.*

**Not deleted.** The audit register's own suggestion is to hard-delete pre-migration
rows as "safest" (`docs/factory/api-audit.md:509-510`). It is the safest thing for the
*migration* and the least safe thing for the *data*. Destroying the only record of
what the system did — permanently, as a side effect of a permissions fix, in the one
table whose entire purpose is to be a record — is not a cleanup. It is also
irreversible, which makes the rollback of this work lossy for no reason (§7). If a
deployment later decides the old rows are worthless, deleting them then is one
statement; undeleting them is nothing.

**Kept, and unreadable through the API.** The rows stay where they are, keep their
absent attribution honestly, and are returned to nobody, because they belong to
nobody. They remain available to whoever has direct database access, which is the
correct audience for unattributable history and is a deliberate, auditable act rather
than an HTTP GET.

**This has a visible consequence that must not be discovered in production.** On the
day this ships, every user's activity log appears to empty out, and refills as they
use the product. That is the correct outcome and it will look like a bug to anyone not
told in advance. It needs to be in the release note and, ideally, in the page's empty
state for a period — see OQ-3.

### 3.4 An action that cannot be attributed is a loud failure

Today a failed insert is discarded without a trace (§2.3). Once entries are required
to name a subject, an attempt to record one without a subject becomes possible, and it
must be reported to the people running the deployment rather than swallowed. The
user-visible behaviour of the action itself does not change — recording an activity
entry is not important enough to fail a user's scheduling request over — but the
failure stops being invisible.

### 3.5 What this log is, and what it is not — stated so nobody mistakes one for the other

After this change, the log is a **per-user activity feed**: it tells you what Paceday
did to your calendar. That is what the UI has always claimed (§2.7) and it is a useful
product feature.

It is **not** a security audit trail, and this spec asserts that it must not be
described as one. It contains no authentication events, no configuration changes, no
address or device, no actor distinct from the subject, and it is written on a
best-effort basis. After this change it is also readable *only* by its subject — which
is exactly backwards for an audit trail, where the point is that someone other than
the subject can review it, and where a compromised account whose own log is the only
record is no record at all.

Both things are worth having. Conflating them produces a feature that is trusted for a
job it cannot do. PAC-22's review already ran into the gap from the other side: a
change to an organisation's identity-provider configuration leaves no trace anywhere.
That need is real, it is not this, and it gets its own issue (OQ-7).

### 3.6 One default, described in one place

The number of entries returned when the caller does not ask for a particular number is
a single number, stated once in the contract, and no client defines a second one of
its own. A caller who asks for more than the maximum gets the maximum, rather than
silently getting a number smaller than the maximum (§2.8).

---

## 4. Acceptance criteria

Each is falsifiable today: §2.9 establishes that no test exists over this surface at
the HTTP boundary at all, and each criterion below fails against current behaviour.

**Scope — the core of the finding**

AC-1. Given two users who have each caused activity to be recorded, when one of them
retrieves their activity log, then every returned entry is one caused by their own
activity and none is the other's — including when they ask for the maximum number of
entries the interface permits.  `[contract]`

AC-2. Given a user who has caused no activity to be recorded while other users have,
when that user retrieves their activity log, then the result is an empty log, reported
as an empty result rather than as an error.  `[contract]`

AC-3. Given a stored entry whose `details` contains another user's meeting title and a
stored entry containing another user's typed scheduling prompt, when a user who is not
the subject of those entries retrieves their log with any combination of the
parameters the interface accepts, then neither string appears anywhere in any
response.  `[contract]`

AC-4. Given an authenticated user, when they retrieve their activity log, then the
result is determined by which user they are — two different users making the identical
request at the same moment receive different results.  `[contract]`

**Attribution on write**

AC-5. Given a user who performs each of the recordable actions — creating focus time,
clearing focus time, scheduling a meeting, moving a meeting, submitting a
natural-language request and confirming one — when that user afterwards retrieves
their activity log, then each of those actions appears in it.  `[e2e]`

AC-6. Given a user for whom automatic focus scheduling runs without their involvement,
when the run completes and that user retrieves their activity log, then the run appears
in **their** log, and it does not appear in any other user's log.  `[unit]`

AC-7. Given an attempt to record an activity entry that does not name a subject, when
the attempt is made, then no entry is stored and the failure is reported in the
server's logs; and the user-facing action that triggered the recording still completes
as it otherwise would.  `[unit]`

**The existing history**

AC-8. Given entries stored before this change, when any user retrieves their activity
log, then none of those entries is returned, for any user.  `[contract]`

AC-9. Given entries stored before this change, when the change has been applied, then
those entries still exist in storage with their action, details and timestamp
unaltered, and are distinguishable from entries recorded afterwards by the absence of
a subject.  `[unit]`

**Limits (API-074)**

AC-10. Given a caller who does not state how many entries they want, when they
retrieve their activity log, then they receive the number of entries the published
interface says is the default, and that number is the same one the shipped client uses
when it does not state a preference.  `[contract]`

AC-11. Given a caller who asks for more entries than the published maximum, when they
retrieve their activity log, then they receive at most the published maximum — not a
smaller number chosen silently.  `[contract]`

**Preserved behaviour**

AC-12. Given an unauthenticated caller, when they attempt to retrieve an activity log,
then the attempt is refused exactly as it is today.  `[contract]`

AC-13. Given a user retrieving their activity log, when the response is rendered by
the existing client, then each entry still shows a timestamp, an action name and its
details, in reverse chronological order, with no change to how the page is built.
`[e2e]`

AC-14. Given every action that is recorded today, when this change has been applied,
then that action is still recorded — the set of things the log knows about does not
shrink.  `[unit]`

AC-15. Given a recorded action whose details include free text containing a double
quote, when the entry is stored and later retrieved by its subject, then the entry is
returned and rendered without the response failing to serialise or the client failing
to display it.  `[contract]`
*(Guards §2.6 against being made worse by this work. It does not fix it — see OQ-6.)*

---

## 5. Explicitly out of scope

- **What `details` contains.** Meeting titles and raw prompt text stay as they are.
  Scoping is the critical fix and must not wait for a content decision. My position,
  recorded so it is not mistaken for indifference: a **meeting title** in the
  subject's own feed is defensible and probably necessary — it is their meeting and it
  is how they recognise the row. **Raw natural-language prompt text** is a different
  thing: it is verbatim user input, kept indefinitely, frequently naming third parties
  who are not the subject, stored to populate a feed that an intent summary would
  populate just as well. Scoping limits who reads it; it does not make keeping it
  right. See OQ-6 — and note that the concatenation defect in §2.6 must be fixed before
  anything can safely parse or redact this field.
- **Retention.** Nothing ever deletes from this table. After this change each user's
  log grows forever and is served under a limit, so it is not a correctness problem
  today, but "we keep your typed prompts indefinitely" is a policy that should be
  decided rather than inherited.
- **An organisation-wide or manager-facing view.** Argued in §8 OQ-1 rather than
  assumed away. It cannot be built today because there is no administrator concept in
  the codebase — PAC-22 §2.3 establishes this exhaustively, and the two candidate
  signals are both unusable as grants.
- **A real security audit trail** (§3.5), including recording SSO configuration
  changes. OQ-7.
- **Encrypting or redacting anything at rest.**
- **Moving this operation onto the generated server interface.** Factory §4 says a
  modified endpoint uses the generated interface. No operation in this repo is
  `generated` yet — all rows in `contracts/openapi/MIGRATION.md` are `handwritten` and
  `backend/api/gen/` does not exist — so honouring that rule here means bootstrapping
  the entire generated-server mechanism inside a critical security fix. That makes the
  change larger, the review harder and the revert riskier, which is the opposite of
  what this issue needs. Escalated as OQ-5 rather than decided here.
- **Backfilling by inference**, argued and rejected in §3.3 rather than omitted.

---

## 6. What must not change

- **Every action recorded today is still recorded.** Guarded by AC-14. No test
  protects this now: the only test on the write path asserts that the function does
  not panic (§2.9).
- **The endpoint stays available to every authenticated user, for their own log.**
  This work restricts *what* is returned, never *who* may ask. Guarded by AC-12.
- **The shape of an entry as the client sees it.** `id`, `action`, `details`,
  `created_at` (`contracts/openapi/paths/calendar.yaml:1303-1322`;
  `smart-calendar-flow/src/api/types.ts:219-224`). Guarded by AC-13. Keeping it
  unchanged is what allows the frontend to need no change at all beyond §3.6, and the
  spec would rather have that than a tidier response.
- **The order and the limit semantics**, other than the two corrections AC-10 and
  AC-11 make deliberately.
- **User-facing actions must not start failing because the log failed.** Recording is
  best-effort today (§2.3) and stays best-effort; §3.4 makes the failure visible, not
  fatal. Guarded by AC-7.
- **The existing rows must survive.** Guarded by AC-9. This is stated as a
  must-not-change precisely because the register recommends the opposite (§3.3).

---

## 7. Rollback

There is a migration, and the answer to "does the down migration lose data" is
**yes, and it is worth being exact about which data.**

- **No rows are lost.** Every entry, old and new, keeps its action, details and
  timestamp.
- **Every attribution written since the up migration ran is lost**, irrecoverably, the
  moment the column is dropped. There is no second copy and nothing to reconstruct it
  from (§2.5 applies with equal force to the new rows once the column is gone).
- **The consequence is worse than it first looks.** Down-then-up leaves the whole
  table unattributed, so every user's log is empty and stays empty for everything that
  happened before the second up. A rollback rehearsal that runs down and up to prove
  reversibility would itself destroy the attribution of every row.

Therefore the rollback procedure this spec asks for is **code-only**: revert the
application, leave the column in place. An unused nullable column costs nothing, the
old code neither reads nor writes it, and the attribution accumulated so far survives
for when the code returns. Dropping the column is a separate, deliberate act
appropriate only if the feature is abandoned outright.

Reverting the code restores the leak in full, so a revert here is reopening a critical
finding rather than returning to safety. If a defect is found after release, fix
forward.

One thing does not roll back at all: the pre-existing history becomes invisible to
users the moment the scoped read ships, and reverting makes it visible **to everyone
again**, which is the finding. There is no code state in which the old rows are
visible only to the right people, because there is no right person.

---

## 8. Open questions

**OQ-1 — Should anybody be able to see an activity log that is not their own?**
*Owner: human (product). Should be answered before the contract is written, but does
not block it — the answer "not yet" is already implementable and is what this spec
assumes.*
I am not assuming per-user is right; here is the argument. **For a wider scope:** a
manager investigating why a report's calendar was rearranged, or support diagnosing a
complaint, has a legitimate need, and a log only its subject can read is useless for
exactly the case where the subject is the problem (§3.5). **Against:** there is no
administrator concept to gate it on — PAC-22 §2.3 shows the only two candidates are
unusable, `team_members.role` because team ownership is self-granted and
`user_profiles.is_manager` because it is *detected from calendar shape*, so gating on
it would hand colleagues' typed prompts to anyone whose diary looks managerial. And
the content argues against it independently: an org-wide view of this table is a
record of what colleagues typed into a scheduling box, which is employee monitoring
and a decision with legal weight in several jurisdictions, not an access-control
default. My recommendation: per-user now; if a wider view is ever wanted, it reads a
redacted projection with an explicit grant, and it is a feature with its own spec.
Resolve by a product decision.

**OQ-2 — Should an entry also record who acted, as distinct from whose calendar it
was about?** *Owner: human (product), before the contract is written.* Today they are
always identical (§2.4), so one column is sufficient and a second would be
unfalsifiable — no test could distinguish them. But the first manager-acting-on-report
feature makes them differ, and adding a second column later is a second migration over
the same table. The trade is one cheap migration now against a plausible one later. My
recommendation is one column now, because a column whose values are all equal to
another column's teaches nobody anything, and §3.2 already records the rule that
prevents its absence being misread. I want this decided rather than defaulted.

**OQ-3 — How are users told that their log appears to empty out on release day?**
*Owner: human (product/release).* §3.3 makes this certain, not likely. The cheapest
answer is a line in the release note; a better one is temporary wording in the page's
empty state. It needs a decision because the alternative — saying nothing — produces
support tickets that look like a data-loss incident.

**OQ-4 — Is the shipped client the only caller?** *Owner: whoever operates the
deployments.* I established there is no other caller in either repository, the MCP
server or the e2e suite (§2.7). A script, saved request or dashboard outside the tree
that reads the global log would silently start seeing only its own service account's
entries — which is the correct behaviour, but a surprise. Resolve by asking. The
consequence of being wrong is low: such a caller sees less, never more.

**OQ-5 — Does this operation have to move to the generated server interface?**
*Owner: whoever owns `contracts/openapi/MIGRATION.md`; blocks the plan, not this spec.*
Factory §4 says modified endpoints use the generated interface. No operation is
`generated` today and `backend/api/gen/` does not exist, so the rule as written makes
this critical fix the pilot for the whole mechanism. I think that is the wrong place
to pilot it and have said so in §5, but it is not my call and it is a written rule, so
it needs an explicit exception or an explicit acceptance. Settle it before stage 3
plans the work.

**OQ-6 — Should raw natural-language prompt text be stored at all, and who fixes the
unescaped concatenation?** *Owner: human, as a new issue.* §2.6 and §5. These are one
issue rather than two because the escaping defect is what stops anyone safely parsing
`details` to redact it. Neither is this issue's problem; both stop being invisible if
they are filed.

**OQ-7 — Where do actions that are about no single user get recorded?** *Owner: human
(product), as a new issue.* §3.5. PAC-22's review found that substituting an
organisation's identity provider leaves no trace. That record needs a different
audience and different retention from this feed, and the decision this spec needs is
only the negative one: it is not this table. If somebody decides it *is* this table,
§3.2's rule against placeholder identities is the first thing that has to be revisited,
and this spec would need to be rewritten rather than amended.

---

## Challenge — 2026-09-17

*Reviewed by `spec-challenger`. I did not write this spec and did not ask the author for
their reasoning. Everything below was checked against source in this working tree; the Go
toolchain and the npm registry are blocked here, so nothing was compiled or executed. The
one thing I could run, `python3 scripts/openapi_assemble.py`, I ran.*

**Verdict**: accept-with-changes

The central claim of this spec — the one the Linear issue gets wrong — is **correct**, and
I verified it independently rather than taking it. All seven writers do have a subject
available, the cron path is per-user, and the issue's premise that some writer is
user-less is false. That is the finding the whole design rests on and it holds. §2's
citations are accurate to the line in every case I checked but one. The findings below are
about what §2 leaves out and about two load-bearing claims that are true for a reason the
spec does not state.

### Blocking

1. **§2.4's survey of unrecorded actions is incomplete, and the omission is the one that
   matters.** The spec correctly names `AutoDeclineService`, `PersonalBlocker` and
   `DailyRecapService` as writing nothing (verified: zero `WriteAuditLog` in
   `backend/engine/auto_decline.go`, `personal_blocker.go`, `daily_recap.go`). It does not
   name **public booking**. `POST /api/book/{slug}` is registered *outside* the
   `requireAuth` group (`backend/api/routes.go:26`, under the comment "Public booking
   routes — no JWT required") and reaches `calClient.CreateEvent` at
   `backend/engine/booking.go:267`. So an **unauthenticated third party creates a real
   event on a user's calendar and nothing is recorded anywhere.**
   This is not a completeness nitpick, because §3.2 uses the survey to justify a decision:
   it asserts "Today those are always the same person (§2.4)" and "They will not always be:
   a manager acting on a report's calendar…". Booking is already the actor≠subject case,
   today, in production — it simply writes no row, so the divergence is invisible rather
   than absent. OQ-2's recommendation of a single `subject` column is argued from the
   premise that no divergent case exists yet; the premise is wrong as stated.
   *What resolves it*: §2.4 names the booking path among the writers that record nothing,
   and §3.2 restates its claim as the narrower true one — *every action that is recorded
   has actor = subject, because the one path where they differ records nothing*. OQ-2 then
   has to be decided against that fact rather than against its absence. (I checked the
   other candidate divergence and it is **not** one: `managerHandlers.scheduleMember`,
   `backend/api/handlers_manager.go:443-485`, only returns a `prefill_url` and touches no
   calendar.)

2. **§3.2's enforcement promise is stronger than the mechanism delivers, and the gap is
   `uuid.Nil`, not `NULL`.** §3.2 requires that the system "must not be able to create
   another [unattributable row], even by accident and even in code written later", and
   §5/§7 lean on a database-level guarantee. Six of the seven writers will take the subject
   from the request context, and `auth.UserIDFromContext` **returns `uuid.Nil` rather than
   an error when the value is absent** (`backend/auth/context.go:19-24`). `uuid.Nil` is the
   all-zeroes UUID — it is *not* SQL `NULL`, so a `CHECK (user_id IS NOT NULL)` accepts it.
   The only thing that would stop it reaching the table is a foreign key to `users(id)`,
   and only because no user happens to hold the nil UUID.
   So the spec's asserted invariant is real but is delivered by a different mechanism than
   the one it describes, and the difference is exactly the case AC-7 exists for.
   *What resolves it*: §3.2 states the requirement in terms the enforcement can actually
   meet — *a recorded entry names a subject that exists* — so that both the absent case and
   the all-zeroes case are covered, and AC-7 is stated over "a subject that is absent **or
   is the zero identifier**", so a test can distinguish them.

3. **The invariant that makes six of the seven writers safe is a routing fact the spec
   never states and §6 does not protect.** I traced every non-test path into each writer.
   The claim holds, and here is why it holds: `/api/focus`, `/api/schedule` and `/api/nlp`
   are all inside the single `requireAuth` group opened at `backend/api/routes.go:63-64`,
   and `requireAuth` puts a parsed UUID in the context at `middleware.go:53` or rejects the
   request. The cron path is safe for a second, independent reason — `RunForUser` refuses
   `uuid.Nil` at `focus_time.go:71-73` *before* injecting the id into ctx at `:74`.
   §6 "What must not change" lists six things and this is not one of them. Moving any of
   those three route groups out of the authenticated group — a plausible future edit, e.g.
   a public NLP demo — silently turns six writers back into unattributable ones, and §3.4
   would surface it only as log noise after the fact.
   *What resolves it*: §6 adds the requirement that every route which can reach a recording
   writer stays behind authentication, with an acceptance criterion that fails if one does
   not. (I am naming the requirement, not the mechanism — how it is guarded is the plan's
   business.)

4. **OQ-6 bundles a three-line mechanical defect with an open product question, which
   guarantees the cheap half waits on the expensive half.** OQ-6 is "should raw NLP prompt
   text be stored at all, **and** who fixes the unescaped concatenation?" — and it is
   assigned to "human, as a new issue". Those two have nothing in common except the column
   they touch. The retention question is a product and arguably legal decision with no
   obvious owner and no deadline. The escaping defect is replacing three string
   concatenations with the marshalling the other four writers already use, in three files
   this feature **already opens** — `contracts/features/PAC-23.yaml` lists
   `backend/engine/smart_schedule.go`, `backend/api/handlers_schedule.go` and
   `backend/engine/compression.go` in `allowedPaths` because all three need the
   `WriteAuditLog` signature change anyway.
   The cost of the bundle is concrete and permanent: the contract now ships a standing
   "treat this field as opaque text; do not parse it" warning on `AuditEntry.details`
   (`contracts/openapi/paths/calendar.yaml:1337-1348`) that will outlive everyone's memory
   of why.
   *What resolves it*: split OQ-6 into the escaping defect and the retention question, and
   take a position on the first in §5 rather than deferring it with the second. AC-15 is
   already the right test and currently asserts only that a quoted title "does not break" —
   if the defect comes into scope it becomes "round-trips as valid JSON", which is
   falsifiable today and is not today.

### Non-blocking

1. **§2.6 understates the concatenation defect at one of the three sites, in the direction
   that makes it worse.** The spec describes all three as "a meeting title containing a
   double quote". At `backend/engine/compression.go:178` there is no title — the
   interpolated values are `p.EventID` and an RFC3339 timestamp. The timestamp is safe, but
   `p.EventID` is **taken verbatim from the request body** with no validation
   (`backend/api/handlers_schedule.go:80-93` copies `req.Proposals[].EventID` straight into
   `engine.MoveProposal`). So that site is not "a title might contain a quote" — it is a
   caller writing chosen bytes into the structure of an audit record. It does not widen the
   scoping hole (the row is still the caller's own, so AC-3 is unaffected), which is why
   this is non-blocking, but §2.6 should describe the mechanism it actually has.

2. **§2.7 and §2.9 are right about the consumers and about the test vacuum; one file named
   in the review brief I was given does not exist.** There is no `src/api/audit.ts` in
   `smart-calendar-flow`. The client lives at `src/api/client.ts:1060-1067` and the hook at
   `src/hooks/useAudit.ts:16`. The spec cites both correctly — I am recording this so the
   next reader does not go looking for a file the spec was right not to cite.

3. **The empty-state consequence in §3.3 is real and slightly worse than described.**
   `api.getAudit` wraps the call in `withFallback` and returns `mockState.audit` when the
   backend is unreachable (`src/api/client.ts:1060-1067`); `mockState.audit` is `[]`
   (`:146`). So a genuinely empty scoped log and an unreachable backend render the
   *identical* "No activity yet" panel (`src/pages/Audit.tsx:82-89`). That is harmless
   today because both are empty, but on release day every user is in the state that is
   indistinguishable from an outage, which sharpens OQ-3 from "write a release note" to
   "the empty state cannot currently tell the user which of two things happened".

4. **`AuditEntry.id` is narrower in the database than anywhere else.** `audit_log.id` is
   `SERIAL` — 32-bit — at `backend/storage/migrations/001_initial.up.sql:45`, while the Go
   struct uses `int64` (`backend/storage/audit_log.go:9`). §6 freezes "the shape of an entry
   as the client sees it", which is correct, but the frozen shape over-declares the range
   the table can produce. Worth a line so nobody later treats `int64` as a promise.

5. **§2.8 is accurate and I could not improve on it.** `strconv.Atoi`'s error is discarded
   at `handlers_audit.go:14`, and `audit_log.go:16-18` is exactly
   `if limit <= 0 || limit > 500 { limit = 100 }`. The spec's separation of "the drift" from
   "the surprise" is the right framing, and the clamp really does return 100 for a request
   of 1000 — below the documented maximum of 500.

### Criteria I could not falsify

None — every one of AC-1 through AC-15 describes a test that fails today. §2.9 is correct
that the surface is untested: there is no `backend/api/handlers_audit_test.go`, no
`backend/storage/audit_log_test.go`, and the only write-path test
(`backend/storage/focus_blocks_test.go:102-107`) reads nothing back. Two qualifications
rather than failures:

- **AC-7** is falsifiable only once §3.2's "does not name a subject" is pinned to a
  concrete condition. As written, `uuid.Nil` and an absent context value are the same
  sentence and different code paths (blocking finding 2).
- **AC-9** says pre-change entries stay "distinguishable from entries recorded afterwards
  by the absence of a subject". True as long as absence has exactly one cause. The plan
  proposes `ON DELETE CASCADE`, which preserves that; the alternative it discusses,
  `ON DELETE SET NULL`, would give absence two meanings and make AC-9 unfalsifiable. The
  spec should state the requirement — that the absence of a subject must mean exactly one
  thing — rather than leaving the distinction to a migration keyword.

### Citations I checked and found wrong

- **`backend/engine/compression.go:178` cited as a site where "a meeting title containing a
  double quote" breaks the payload** — there is no title at that call site. The values are
  `p.EventID` and `p.ProposedStart.Format(time.RFC3339)`. The defect is real and the line
  number is right; the mechanism is client-supplied `event_id`, not a title. (Non-blocking
  finding 1.)
- Everything else in §2 that I checked was accurate to the line:
  `001_initial.up.sql:44-49`, `audit_log.go:16-19`, `focus_blocks.go:61-63`,
  `handlers_audit.go:14`, `middleware.go:53`, `routes.go:179` and the group at `:63-64`,
  `scheduler/cron.go:36-68`, `focus_time.go:71-74` and `:106`, `Audit.tsx` subtitle and
  empty state, `QuickActions.tsx:104-150`, and "migrations run to 023 and none mention
  `audit_log`" (verified: `audit_log` appears only in `001_initial.{up,down}.sql`).

### Consumers this breaks

- **`smart-calendar-flow/src/pages/Audit.tsx:22`** and **`src/components/QuickActions.tsx:20`**,
  both through `src/hooks/useAudit.ts:16` — each sees strictly fewer rows. Neither needs a
  code change for the scope half: the page already advertises per-user scope at
  `Audit.tsx:38` and renders the empty case at `:82-89`. Correctly identified by the spec.
- **`smart-calendar-flow/src/api/client.ts:403-404`** — `DEFAULT_AUDIT_LIMIT = 50`, the
  API-074 half. Note it is not only sent as a query parameter: `Audit.tsx:38` interpolates
  the same constant into the visible subtitle, so whatever this becomes is what the page
  tells the user.
- **Nothing else.** I re-ran the search the spec claims: `mcp/` has no reference to
  `audit`, to `WriteAuditLog`, or to any engine type — the module is `client.go`,
  `tools.go`, `main.go` and tests, and none of them mention it. `e2e/` has no audit
  journey. Confirmed, not assumed.
- **`smart-calendar-flow/src/api/audit.test.ts:50`** does not break, but is wrong in a way
  worth knowing: it asserts against `action: "focus.run"`, an action name that exists
  nowhere in `backend/`. See the contract review — it is downstream of a false description
  in the contract, not of this spec.

### On the backfill decision and `NOT VALID`, which I was asked to attack

The choice — keep the rows, leave the subject absent, show them to nobody, enforce new rows
with `CHECK (user_id IS NOT NULL) NOT VALID` — is **the right one**, and the argument in
§3.3 against both alternatives is the strongest reasoning in the spec. I tried to break it
four ways and it survived three.

- **Does `NOT VALID` behave as claimed?** Two of the three claims, yes. Adding a `CHECK …
  NOT VALID` skips the verifying scan, so the retained rows are not checked at `ALTER`
  time; and every subsequent `INSERT` and `UPDATE` **is** checked, which is precisely what
  the mechanism is for. The third claim — that existing rows "are never validated against
  the constraint" and never will be — is a statement about human behaviour, not a property
  of the database. `NOT VALID` is not permanent: anyone can later run
  `ALTER TABLE audit_log VALIDATE CONSTRAINT …`, and that scan will **fail**, because the
  retained rows are by construction the rows that violate it. The failure is loud and
  changes nothing — no data is lost, the constraint simply stays `NOT VALID` — so this is a
  confusing afternoon rather than an incident. But "tidy up the invalid constraints" is a
  routine maintenance habit, and the migration's one job is to make an anomaly permanent on
  purpose. *What resolves it*: the migration carries a comment saying the pre-PAC-23 rows
  are permanent, deliberate violations and the constraint must never be validated, and §3.3
  states that the retained rows' unattributed state is designed to be permanent rather than
  pending. One line each. The plan already flags "whether `NOT VALID` survives this
  deployment's migration tooling" as undetermined (§7 item 2) and proposes the right check;
  it does not flag the validate-later hazard.
- **Does the CHECK actually enforce §3.2?** Only for `NULL`. It does not catch `uuid.Nil`.
  This is blocking finding 2 above and is the one place where the mechanism is weaker than
  the spec's prose.
- **Is a retained NULL row reachable by any other path?** No — and I checked rather than
  assumed. `audit_log` appears in `backend/` exactly four times: the `CREATE`/`DROP` in
  `001_initial`, one `INSERT` (`storage/focus_blocks.go:62`) and one `SELECT`
  (`storage/audit_log.go:19`). No view, no join, no second reader, and no `UPDATE`
  statement anywhere against the table. Once the read gains its predicate the retained rows
  are genuinely returned to nobody. **Verified clean.**
- **Does the down migration lose the column's data, and does the plan say so?** It does,
  and it does — in all three artifacts, unambiguously, which is more than the gate asks
  for. Spec §7 ("every attribution written since the up migration is lost, irrecoverably"),
  plan §2/Migration and §6 ("Do not rehearse this migration by running down and up against
  data you intend to keep"), and `contracts/features/PAC-23.yaml:112`. The observation that
  a rollback *rehearsal* would itself destroy the attribution, and the resulting decision to
  make the supported rollback code-only, is the single best call in this package.
  **Verified clean, no change wanted.**

### On OQ-5, since the spec asks for a view rather than a decision

I agree with the spec and would grant the exception, for a reason §5 does not give:
factory §4's own stated rationale for migrating incrementally is that a big-bang move is "a
rewrite with no test coverage to catch what it broke". By that criterion `listAuditEntries`
is the **worst available pilot**, not a neutral one — §2.9 establishes it has no test at the
HTTP boundary at all. Piloting an unproven generated-server path on the one operation that
cannot detect its own regression inverts the rule's purpose. The author was right to
escalate rather than decide, and right not to bundle.
