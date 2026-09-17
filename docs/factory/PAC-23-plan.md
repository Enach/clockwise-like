# Implementation plan — PAC-23: the activity log must belong to the person it is about

- **Spec**: `docs/specs/PAC-23.md`
- **Contract**: `contracts/features/PAC-23.yaml` (revision 1, hash
  `2ae421142e7d17eca73c625af3b8f1bb2c397e8bfd92f1fedde4d29377804965`);
  rationale in `contracts/features/PAC-23.md`
- **Resolves**: API-005 (critical), API-074 (low), `x-uncertain` U-02
- **Status of inputs**: the spec is `draft` and the contract has not been through
  `contract-challenger`. Per the plan-author rules this plan should not be started
  until both are accepted; it is written now because stages 1–3 were commissioned
  together, and **anything the challengers change invalidates §2 and §5 first.**

> Nothing here was compiled, tested or run. The module proxy is blocked in this
> sandbox. `python3 scripts/openapi_assemble.py` and
> `scripts/openapi_migration_report.py` were run; nothing else was.

---

## 1. Approach

The change is a schema change with a very small surface above it. `audit_log` gains a
nullable `user_id` that every **new** row is required to populate, the read gains a
`WHERE user_id = $1`, and the seven writers learn to pass the subject. Nothing about
the HTTP shape moves, which is what keeps the frontend change down to one constant.

**The subject is passed explicitly, not derived inside `storage`.** The tempting shape
is `WriteAuditLog(ctx, db, action, details)`, with `storage` calling
`auth.UserIDFromContext` itself — it would keep every call site a one-token diff.
**It is not available**: `backend/auth` already imports `backend/storage`
(`backend/auth/sso_detect.go:14`), so a `storage` → `auth` import is a cycle. The
signature therefore becomes `WriteAuditLog(db, userID, action, details)` and each call
site extracts the id from the context it already holds. Five of the seven files gain a
`backend/auth` import; `backend/engine` already imports it
(`backend/engine/focus_time.go`, `personal_blocker.go`) and `backend/api` already does
(`middleware.go`), so no new dependency edge is created. This is the better shape
anyway: an explicit parameter is what makes the missing-subject case a *compile-time*
question at six of seven sites rather than a runtime one.

**The column is nullable at rest and required on write**, via a `NOT VALID` check
constraint. Pre-migration rows keep a `NULL` subject and are never validated against
the constraint; every insert and update from the migration onward is. This is the only
shape that satisfies spec §3.3 (keep the old rows, unattributed and honest) and §3.2
(the system must be unable to record another unattributable row) at the same time. A
`NOT NULL` column would force a backfill or a delete, both rejected by the spec; a
plain nullable column with no constraint would leave §3.2 enforced only by code review.

**Everything else stays the smallest thing that works.** No response field is added, no
DTO is introduced, the operation stays `handwritten` (§2.5), `details` is not touched,
and the `details` concatenation defect is left alone and filed. The one deliberate
extra is the `limit` correction required by API-074 under factory §7.

---

## 2. Backend — Enach/clockwise-like

### Tests to write first

Storage-level tests use the existing `openTestDB(t)` harness
(`backend/storage/focus_blocks_test.go`, `backend/api/testhelpers_test.go:13-40`),
which is testcontainers-backed and runs the migrations at `storage.Open`.

| Test | File | Proves | Spec AC |
|---|---|---|---|
| `TestWriteAuditLog_StoresSubject` | `backend/storage/audit_log_test.go` *(new)* | A write records the given user id and the row reads back under that user | AC-14 |
| `TestWriteAuditLog_RejectsNilSubject` | `backend/storage/audit_log_test.go` *(new)* | `uuid.Nil` stores nothing — the check constraint or the guard refuses it | AC-7 |
| `TestWriteAuditLog_ReportsInsertFailure` | `backend/storage/audit_log_test.go` *(new)* | A failed insert is written to the server log instead of being discarded, and the function still returns without error to its caller | AC-7 |
| `TestListAuditLog_ScopesByUser` | `backend/storage/audit_log_test.go` *(new)* | Two users' rows; each query returns only its own | AC-1 |
| `TestListAuditLog_ExcludesNullSubjectRows` | `backend/storage/audit_log_test.go` *(new)* | A row inserted with a `NULL` subject (raw SQL, as a pre-migration row) is returned for no user id | AC-8 |
| `TestListAuditLog_PreMigrationRowsSurvive` | `backend/storage/audit_log_test.go` *(new)* | The `NULL`-subject row is still present in the table with action, details and timestamp unchanged | AC-9 |
| `TestListAuditLog_DefaultAndClamping` | `backend/storage/audit_log_test.go` *(new)* | `limit <= 0` → 50; `limit > 500` → 500, not 100 | AC-10, AC-11 |
| `TestListAuditEntries_ReturnsOnlyCallersEntries` | `backend/api/handlers_audit_test.go` *(new)* | The HTTP boundary honours the scope | AC-1 |
| `TestListAuditEntries_EmptyLogIsEmptyArrayNotError` | `backend/api/handlers_audit_test.go` *(new)* | 200 + `[]` for a user with no activity | AC-2 |
| `TestListAuditEntries_ForeignTitlesAndPromptsNeverAppear` | `backend/api/handlers_audit_test.go` *(new)* | Seeds another user's meeting title and NLP prompt; asserts neither string occurs anywhere in the body, across `limit` absent / 1 / 500 / 10000 | AC-3 |
| `TestListAuditEntries_ResultDependsOnCaller` | `backend/api/handlers_audit_test.go` *(new)* | The identical request as two users returns two different bodies | AC-4 |
| `TestListAuditEntries_PreMigrationRowsReturnedToNobody` | `backend/api/handlers_audit_test.go` *(new)* | | AC-8 |
| `TestListAuditEntries_DefaultLimitMatchesContract` | `backend/api/handlers_audit_test.go` *(new)* | No `limit` → 50 rows | AC-10 |
| `TestListAuditEntries_OverMaximumLimitCaps` | `backend/api/handlers_audit_test.go` *(new)* | `limit=10000` → 500, not 100 | AC-11 |
| `TestListAuditEntries_UnauthenticatedIsRefused` | `backend/api/handlers_audit_test.go` *(new)* | 401 with the existing middleware body | AC-12 |
| `TestListAuditEntries_DetailsContainingQuoteRoundTrips` | `backend/api/handlers_audit_test.go` *(new)* | A row whose `details` is malformed JSON still serialises and returns | AC-15 |
| `TestRunForUser_AuditEntrySubjectIsTheRunUser` | `backend/engine/focus_time_test.go` *(exists)* | A cron-shaped `RunForUser` call records against that user and no other | AC-6 |
| `TestClearWeek_AuditEntrySubjectIsCallerFromContext` | `backend/engine/focus_time_test.go` *(exists)* | | AC-5, AC-14 |
| `TestCreateMeeting_AuditEntrySubjectIsCallerFromContext` | `backend/engine/smart_schedule_test.go` *(exists)* | | AC-5, AC-14 |
| `TestApply_AuditEntrySubjectIsCallerFromContext` | `backend/engine/compression_test.go` *(exists)* | | AC-5, AC-14 |
| `TestParse_AuditEntrySubjectIsCallerFromContext` | `backend/nlp/parser_test.go` *(exists)* | | AC-5, AC-14 |
| `TestCreateSchedule_AuditEntrySubjectIsCaller` | `backend/api/handlers_schedule_test.go` *(exists)* | | AC-5, AC-14 |
| `TestNLPConfirm_AuditEntrySubjectIsCaller` | `backend/api/handlers_nlp_test.go` *(exists)* | | AC-5, AC-14 |

### Tests that must change, and why

`TestWriteAuditLog` (`backend/storage/focus_blocks_test.go:102-107`) calls
`WriteAuditLog(db, action, details)` twice and asserts only that it does not panic. It
will not compile after the signature change.

It must change because the signature changed, **not** because behaviour it guarded is
being altered — it guards nothing; it reads nothing back. It should be **deleted from
`focus_blocks_test.go` and replaced** by `TestWriteAuditLog_StoresSubject` in the new
`backend/storage/audit_log_test.go`, which asserts what the old test should have. This
is a test that was wrong, and saying so is the point of this subsection. No other
existing test references `WriteAuditLog` or `ListAuditLog`.

### Files to change

| File | Change | Risk |
|---|---|---|
| `backend/storage/migrations/024_audit_log_user_id.up.sql` *(new)* | Column, index, `NOT VALID` check | The `ALTER TABLE ADD COLUMN` of a nullable column with no default is metadata-only on PG11+; the index build is the only real work. See §2/Migration |
| `backend/storage/migrations/024_audit_log_user_id.down.sql` *(new)* | Reverses it, losing the attribution | See §6 |
| `backend/storage/focus_blocks.go:61-63` | `WriteAuditLog` gains a `userID uuid.UUID` second parameter, refuses `uuid.Nil` before touching the database, and logs the insert error instead of discarding it | Low. The function is 3 lines. Consider moving it to `audit_log.go` where it belongs — it is in `focus_blocks.go` for no reason — but that is a rename in the same package and must not be bundled if it makes the diff harder to read |
| `backend/storage/audit_log.go:15-34` | `ListAuditLog` gains `userID uuid.UUID`, the query gains `WHERE user_id = $1`, the clamp becomes default 50 / cap 500 | Low, but this is the security-critical line. The predicate must be a bound parameter |
| `backend/storage/models.go:23-28` | `AuditLog` gains `UserID` | Trivial; the struct appears unused by the read path |
| `backend/api/handlers_audit.go:12-23` | Reads `auth.UserIDFromContext(r.Context())`, refuses `uuid.Nil` with the middleware-shaped 401, passes it to `ListAuditLog` | Low. The `uuid.Nil` branch is unreachable behind `requireAuth` — see §7 item 3 |
| `backend/api/handlers_schedule.go:188` | Pass `auth.UserIDFromContext(r.Context())`; new `backend/auth` import | Low |
| `backend/api/handlers_nlp.go:67` | Same | Low |
| `backend/engine/focus_time.go:106` | Pass the `userID` already in scope as a parameter (`:70`) | Lowest of the seven — no context read needed |
| `backend/engine/focus_time_cleaner.go:39` | Read from `ctx`; new `backend/auth` import | Low. Only caller is `handlers_focus.go:81` |
| `backend/engine/smart_schedule.go:323` | Read from `ctx`; new import | Low |
| `backend/engine/compression.go:178` | Read from `ctx`; new import. Note the call is **inside the per-proposal loop** — the id is read once outside it | Low |
| `backend/nlp/parser.go:221` | Read from `ctx`; new import. `nlp` already imports `engine`, which imports `auth`; no cycle | Low |
| `contracts/openapi/paths/calendar.yaml` | **Already done at stage 2** — see the contract PR | — |
| `docs/factory/api-audit.md` | Mark API-005 and API-074 closed by PAC-23; mark U-02 resolved in the uncertainty table | Bookkeeping, but the contract gate reads this |

### Migration

- **Up**: `backend/storage/migrations/024_audit_log_user_id.up.sql`
  1. `ALTER TABLE audit_log ADD COLUMN user_id UUID REFERENCES users(id) ON DELETE CASCADE;`
     — nullable, no default. `ON DELETE CASCADE` matches how `006_auth.up.sql:14-16`
     attached `oauth_tokens`, `settings` and `focus_blocks` to `users`, and is correct
     for a per-user activity feed (spec §3.5): deleting a user removes their feed. It
     would be wrong for a security audit trail, which is exactly why the spec insists
     this is not one. Flagged as an assumption in §7.
  2. `CREATE INDEX audit_log_user_id_created_at_id_idx ON audit_log (user_id, created_at DESC, id DESC);`
     — matches the new query exactly (`WHERE user_id = $1 ORDER BY created_at DESC, id DESC LIMIT $2`).
     Without it the scoped read is a full scan plus sort, which is *worse* than today's
     unscoped read.
  3. `ALTER TABLE audit_log ADD CONSTRAINT audit_log_user_id_present CHECK (user_id IS NOT NULL) NOT VALID;`
     — `NOT VALID` means existing rows are not checked and never will be, while every
     insert and update from now on is. This is the mechanism that makes spec §3.2
     enforced by the database rather than by discipline.
- **Down**: `backend/storage/migrations/024_audit_log_user_id.down.sql` — drop the
  constraint, drop the index, drop the column.
  **This loses data.** No rows are lost, but every subject recorded since the up
  migration ran is destroyed irrecoverably, and there is nothing to reconstruct it from
  (spec §2.5 applies to the new rows once the column is gone). A down-then-up cycle —
  including one run purely to rehearse reversibility — leaves the whole table
  unattributed and every user's log permanently empty. **Do not rehearse this
  migration by running down and up against data you intend to keep.**
- **Backfill**: **none, deliberately.** Pre-migration rows keep `user_id IS NULL`, are
  returned to no caller by the new predicate, and are retained in the table. Spec §3.3
  argues this against both alternatives (delete — the audit register's suggestion,
  `docs/factory/api-audit.md:509-510`; infer — infeasible for five of the seven action
  types, spec §2.5).
- **Ordering constraint**: `docs/factory/api-audit-issues.yaml:93` says PAC-23's
  migration must not be released alongside another migration. Batch B3 (per-user
  settings) also touches migrations. Release them in separate deploys.
- Applied to a backed-up database only.

### Generated code

**No row moves from `handwritten` to `generated`**, and this is an escalation rather
than a decision (spec OQ-5, contract §6). Factory §4 requires a modified endpoint to
use the generated server interface, but zero of 119 operations are `generated`,
`backend/api/gen/` contains only a `README.md`, and `oapi-codegen` cannot be fetched in
this environment. Making a critical security fix the pilot for the whole generated-server
mechanism is the opposite of "prefer the smallest change that satisfies the contract".

`make openapi-check` fails only when the generated count *decreases*, so leaving the row
at `handwritten` does not break the gate — it breaks a written rule, which is why it
needs an explicit exception in the PR body rather than silence. **If the exception is
refused, this plan is wrong and §5 must be resequenced before any code is written.**

---

## 3. Frontend — Enach/smart-calendar-flow

The scope change needs no frontend work at all. `src/pages/Audit.tsx:39-40` already
tells the user this is "the last 50 actions Paceday performed on **your** calendar",
and `normalizeAuditEntries` (`src/api/client.ts:422-439`) is indifferent to how many
rows arrive. The only required change is the API-074 half.

### Tests to write first

| Test | File | Proves | Spec AC |
|---|---|---|---|
| `"the default limit comes from the generated contract"` | `src/api/audit.test.ts` *(exists)* | `DEFAULT_AUDIT_LIMIT` equals the `default` on `limit` in the generated contract, so the two cannot drift again | AC-10 |
| `"requests GET /api/audit with the limit query parameter"` | `src/api/audit.test.ts:38-44` *(exists, keep)* | Unchanged behaviour: the client still always sends the parameter | AC-10 |
| `"an empty log renders the empty state, not an error"` | `src/api/audit.test.ts` *(exists at :60-64, extend)* | AC-2 at the client edge | AC-2 |

### Files to change

| File | Change | Risk |
|---|---|---|
| `src/api/generated/types.ts` | **Regenerated**, not edited | Must be regenerated from the merged bundle, never hand-edited (factory §3 rule 3) |
| `src/api/generated/schemas.ts` | **Regenerated** | Same |
| `src/api/client.ts:403-404` | `DEFAULT_AUDIT_LIMIT` derives from the generated contract default instead of being a hand-written `50` | Low. The value does not change, so nothing visible moves; the point is that it can no longer drift |
| `src/pages/Audit.tsx:39-40` | Optional, pending spec OQ-3: empty-state or subtitle wording explaining that activity recorded before this release is not shown | Copy only |

**The frontend PR cannot open until the backend PR merges** (factory §1). The generated
artifacts are produced from the merged bundle; a frontend PR ahead of that is
regenerating from a contract that does not exist yet.

---

## 4. E2E

**New journey**: `e2e/tests/audit.spec.ts`.

What is provable today, and it is the part that matters:

- The suite already seeds two real users with fixed UUIDs — `USERS.primary` and
  `USERS.second` (`e2e/seed/ids.ts:9-22`) — and `e2e/seed/seed.sql` runs after the
  backend has applied migrations. Adding audit rows there gives: several rows for
  `primary`, several for `second` with a recognisable "E2E " meeting title and a
  recognisable prompt string, and **one row with a `NULL` subject** standing in for
  pre-migration history.
- Signed in as `primary`, the page shows only `primary`'s rows; `second`'s title and
  prompt strings appear nowhere in the DOM or in the network response; the `NULL` row
  appears for neither user. That covers AC-1, AC-3, AC-8 and AC-13 end to end.

What is **blocked**, and by what:

- **AC-5 cannot be driven through the product** for the calendar-backed actions.
  `e2e/tests/focus-time.spec.ts:1-27` documents why: every action that would write
  `focus_created`, `focus_cleared`, `meeting_scheduled`, `meeting_created` or
  `meeting_moved` goes through `googlecalendar` against a hardcoded base URL, so the
  journey cannot start without the seam requested in `e2e/SEAM-REQUIRED.md` item A
  (**PAC-43**). `nlp_parsed` / `nlp_confirmed` additionally need an LLM.
  AC-5 is therefore proven at unit level (the six writer tests in §2) until PAC-43
  lands, and `audit.spec.ts` should carry the same kind of header comment
  `focus-time.spec.ts` does, naming what is not asserted and why.
- Test ids: the audit page renders no `data-testid` (`src/pages/Audit.tsx:95-110`), so
  assertions go through text content or the network response. That is acceptable here —
  the assertion "this string does not appear" is stronger against raw text than against
  a test id — but it is the **PAC-44** category and worth noting.

---

## 5. Sequencing

1. **Contract PR** (`Enach/clockwise-like`) — the `calendar.yaml` edit,
   `contracts/features/PAC-23.{yaml,md}`, the regenerated bundle and `MIGRATION.md`,
   the spec, this plan. Goes through `contract-challenger`. **Merge point 1.**
2. **Resolve OQ-5 before writing any backend code.** If `listAuditEntries` must become
   the first generated operation, stop and resequence — that is a different, larger
   piece of work and §2 does not describe it.
3. **Backend PR** (`Enach/clockwise-like`), in this order inside the branch:
   a. the two migration files;
   b. `backend/storage/audit_log_test.go` and the deletion of
      `focus_blocks_test.go:102-107` — **failing**;
   c. `backend/api/handlers_audit_test.go` — **failing**;
   d. the six writer tests in `engine`, `nlp` and `api` — **failing**;
   e. `WriteAuditLog` and `ListAuditLog`;
   f. the seven call sites and the handler;
   g. `docs/factory/api-audit.md` bookkeeping;
   h. `make verify`, output into the PR body.
4. **Merge point 2** — backend merges. Only now does the bundle the frontend generates
   from exist on `main`.
5. **Regenerate** the frontend artifacts from the merged bundle.
6. **Frontend PR** (`Enach/smart-calendar-flow`) — the regenerated artifacts, the
   `DEFAULT_AUDIT_LIMIT` derivation, the test, optional OQ-3 copy. **Merge point 3.**
7. **E2E PR** — `e2e/seed/seed.sql` rows and `e2e/tests/audit.spec.ts`. Can start after
   merge point 2 but only passes against a stack running both merged sides.
8. **Release**: backed-up database, migration applied, smoke test that a signed-in user
   sees their own entries and that the pre-existing rows are still in the table. The
   release note must carry the OQ-3 message — users' logs will look emptied.

PR bodies use `Part of PAC-23` and `Fixes <stage sub-issue>`. **Never `Fixes PAC-23`**
on a stage PR (factory §6).

---

## 6. Rollback

**The supported rollback is code-only. Do not run the down migration.**

```
# backend
git revert <backend merge commit> && git push      # restores the unscoped read
# frontend
git revert <frontend merge commit> && git push
# database: nothing. Leave audit_log.user_id in place.
```

The reverted backend neither reads nor writes `user_id`; the column and every subject
recorded so far sit there unused until the code returns.

**If the column genuinely must go** — the feature is abandoned, not merely paused:

```
psql "$DATABASE_URL" -f backend/storage/migrations/024_audit_log_user_id.down.sql
```

**This is not reversible without loss.** In those words: dropping the column destroys
every attribution written since the up migration, permanently, with no second copy and
nothing to reconstruct it from. Re-applying the up migration afterwards yields a table
in which every row — old and new — has a `NULL` subject, so every user's activity log
is empty and stays empty for everything that happened before that moment.

One consequence of reverting is not a rollback at all: it restores API-005 in full for
every user. Treat a revert as reopening a critical finding and prefer fixing forward.

---

## 7. What I could not determine

1. **Whether `ON DELETE CASCADE` is the intended behaviour for a deleted user's
   activity history.** I chose it for consistency with `006_auth.up.sql:14-16` and
   because spec §3.5 frames this as a per-user feed. The alternative,
   `ON DELETE SET NULL`, would keep the rows but overload `NULL`, which currently means
   exactly "pre-migration and unattributable". Cheapest way to settle it: ask whoever
   owns the deletion/erasure policy; it is one word in one migration file and cannot be
   changed cheaply after rows exist.
2. **Whether `NOT VALID` survives this deployment's migration tooling.** Migrations run
   through golang-migrate inside `storage.Open` (`e2e/seed/seed.sql:4-7` describes the
   ordering). `NOT VALID` on a `CHECK` is plain PostgreSQL DDL and should be
   unremarkable, but I could not execute it here. Cheapest check: run the up migration
   against a scratch database before it goes anywhere near production, insert a row with
   a `NULL` subject, and confirm it is refused while the pre-existing `NULL` rows remain.
3. **Whether the handler's `uuid.Nil` branch is reachable.** `requireAuth`
   (`backend/api/middleware.go:43-53`) rejects before the handler runs, so I believe it
   is not. I still plan the branch, because "unreachable" is the same claim U-02 made
   about the audit log being intentional. Cheapest check: the test in §2 asserts the
   middleware's 401; if the branch cannot be reached from a test, say so in the PR
   rather than deleting the guard.
4. **Row volume, and therefore how long the index build takes.** No production figures
   were available to me. The `ADD COLUMN` is metadata-only; `CREATE INDEX` is not.
   Cheapest check: `SELECT count(*) FROM audit_log` on the target database before
   scheduling the deploy, and use `CREATE INDEX CONCURRENTLY` in a separate migration if
   the count is large enough to matter — note that `CONCURRENTLY` cannot run inside the
   transaction golang-migrate wraps a migration in, which is why it is not in the plan
   as written.
5. **Whether `TestWriteAuditLog`'s removal leaves `backend/storage` under the coverage
   floor.** `CLAUDE.md` requires 75–80% and I cannot run `go test`. The replacement
   tests are strictly more numerous and assert more, so I expect coverage to rise, but I
   am stating the expectation rather than the measurement.
6. **Whether any out-of-tree caller exists** (spec OQ-4). It fails closed — such a
   caller sees only its own entries — so this is a communication risk, not a correctness
   one.
