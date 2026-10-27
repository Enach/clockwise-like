# Spec — PAC-42: Analytics week start reported two ways (and wrongly), and two rate units in one response

- **Linear**: https://linear.app/paceday/issue/PAC-42
- **Status**: draft
- **Author**: spec-author
- **Inherited scope**:
  - **API-055** (`docs/factory/api-audit.md:313`, medium, Reported): the week
    start is a plain date on the single-week read and a timestamp on the trend
    read. Resolved here (its `breakdown` half is split out, see §0).
  - **API-058** (`docs/factory/api-audit.md:316`, medium, Reported): habit
    completion rate is a 0..1 fraction beside 0..100 breakdown percentages in
    the same response. Resolved here.
  - **`x-uncertain` on the single-week read's 404**
    (`contracts/openapi/paths/habits.yaml:337`; audit untested-branch row U-01,
    `docs/factory/api-audit.md:469`). The operation is touched, so factory §7.1
    requires it resolved. See AC-9.
  - **`x-uncertain` on the always-zero buffer minutes** of the single-week
    response schema (`contracts/openapi/paths/habits.yaml:1164`; **API-057**,
    `docs/factory/api-audit.md:315`). The schema is touched, so the marker must be
    resolved. See D-5 and AC-10: it is resolved by stating the behaviour as
    certain, not by implementing buffer computation.
  - **New defect found while writing this spec** (§2.3): the week start is the
    zero date (`0001-01-01`) on **every** row returned by both reads. Not in the
    audit. Resolved here, because a unified format around a wrong value would
    satisfy the ticket's letter and fail its purpose.
  - Not inherited: **API-056** and **API-083** (listed in the Linear issue body)
    are on habit and recompute operations this spec does not touch. See §0.

## 0. Scope and split

The Linear issue body bundles five items under audit epic E7
(`docs/factory/api-audit.md:665-668`). They share a domain, not a user problem,
and two of them change status codes on unrelated operations. The factory run that
assigned this ticket narrowed it (title and acceptance: week start format, one
rate unit, paired web regeneration). This spec follows that narrowing and
proposes the remainder as a separate issue (D-4):

| Item | Here | Where |
|---|---|---|
| API-055 week start format, single-week vs trend read | yes | this spec |
| API-058 0..1 vs 0..100 in one response | yes | this spec |
| Week start is the zero date (new, §2.3) | yes | this spec |
| API-055 second half: trend rows carry no breakdown | no | PAC-42-B (proposed) |
| Habit occurrence `scheduled_date` serialised as a timestamp | no | PAC-42-B (proposed) |
| API-056 habit ownership 403 vs 404 | no | PAC-42-B (proposed) |
| API-083 reoptimize 200 vs recompute 202 | no | PAC-42-B (proposed) |
| API-057 buffer minutes never computed | no | own issue (proposed); only its `x-uncertain` is resolved here |

## 1. Problem

Paceday stores a weekly analytics summary per user: how the working week split
into meetings, focus, habits, personal time and free time, and what share of
scheduled habits were completed. Two reads expose it: one for a single week
(computed on demand) and one for the most recent N stored weeks (the trend).

Anyone building on these reads — the web app's future analytics screens, the MCP
server, a third-party client reading the published contract — hits three
problems:

1. **The weeks cannot be told apart.** Every row from either read reports its
   week as starting on 1 January of year 1 (§2.3). A trend chart has no x-axis;
   two trend rows are indistinguishable except by their order.
2. **The same value has two shapes.** Even once the value is right, the single
   week read and the trend read are documented to format it differently (a bare
   date vs a timestamp), so a client needs two parsers and two schemas for one
   concept, and joining a trend row to a single-week response is a string
   comparison that never matches.
3. **Two rates in one response use different scales.** The habit completion rate
   is a fraction (0.75 means 75%) while the breakdown percentages next to it are
   already percentages (75 means 75%). A client that formats both the same way
   shows either "0.75%" or "7500%".

Nobody is harmed today only because nothing renders these reads yet (§2.5). The
cost is paid by the first consumer, which is why it is cheap to fix now: the
contract notes that changes made before publication need no versioning
(`docs/factory/api-audit.md:670-677`, "means regenerate … not version the API").

## 2. Current behaviour

Terms used below: the **single-week read** is `GET /api/analytics/week`
(operation `getAnalyticsWeek`); the **trend read** is `GET /api/analytics/trends`
(operation `listAnalyticsTrends`).

### 2.1 Two serialisations of the week start

- Single-week read: the handler builds a map by hand and formats the week start
  as `"2006-01-02"` (`backend/api/handlers_analytics.go:137`, inside
  `weekWithBreakdown`, `:121-155`).
- Trend read: the handler encodes the storage struct directly
  (`backend/api/handlers_analytics.go:57-66`), whose week start is a `time.Time`
  (`backend/storage/analytics.go:23`), so it marshals as an RFC3339 timestamp.
- The contract models this as two schemas: `AnalyticsWeekWithBreakdown.week_start`
  is `format: date` (`contracts/openapi/paths/habits.yaml`, schema at `:1114`ff,
  property description "DIFFERENT from the trends endpoint"), and
  `AnalyticsWeekRow.week_start` is `format: date-time` ("Midnight UTC of the
  Monday … NOT the "2026-03-02" shape", `:1238-1240`). The two operation
  descriptions repeat the difference (`:307-308`, `:355-357`). Generated Go code
  carries the same split (`backend/api/gen/paceday.gen.go:541` `time.Time` vs
  `:582` `openapi_types.Date`).
- The stored column is a calendar `DATE` (`backend/storage/migrations/011_analytics.up.sql:4`).
- Every other week-bucketed analytics response in the API already uses a bare
  date: manager analytics weeks (`backend/api/handlers_manager.go:540`, `:569`)
  and the single-week read's own `date` query parameter
  (`backend/api/handlers_analytics.go:99-110`).

### 2.2 Two rate units in one response

- The habit completion rate is `completed / (scheduled + completed)` occurrences,
  a 0..1 fraction, 0 when there are none (`backend/engine/analytics.go:91-107`).
  It is stored as-is (`backend/storage/analytics.go:33`, column `FLOAT`,
  `011_analytics.up.sql:14`) and emitted as-is on both reads
  (`handlers_analytics.go:147`; raw struct on the trend read).
- The single-week read's five breakdown entries each carry a percentage computed
  as `minutes / total * 100`, a 0..100 value, unrounded, 0 when the total is 0
  (`handlers_analytics.go:121-134`).
- The focus score is an integer 0..100 (`backend/engine/analytics.go:177-183`),
  documented `minimum: 0, maximum: 100` (`habits.yaml`, `focus_score`).
- So the single-week response carries three proportion-like values: two on a
  0..100 scale and one on 0..1. The trend response carries two: focus score
  (0..100) and habit completion rate (0..1). The contract documents the fraction
  explicitly ("as a 0..1 FRACTION (not a percentage)", `habits.yaml:1178`;
  "0..1 fraction.", `habits.yaml:1262`).

### 2.3 The week start is the zero date on every row (new finding)

Both reads obtain the row through the storage layer's two scanners
(`backend/storage/analytics.go:122-143` and `:151-170`), which scan the `DATE`
column into a Go `string` and then parse it with layout `"2006-01-02"`, discarding
the error (`:139`, `:166`). Under the `pgx` stdlib driver (`backend/storage/db.go:18`)
a `DATE` arrives as a `time.Time`, and `database/sql` converts a `time.Time` into
a string destination as RFC3339 (`"2025-01-06T00:00:00Z"`). The date-only parse
fails, and the week start is left as Go's zero time.

**Observed, not inferred.** On 2026-10-27 the spec author ran a throwaway test
(not committed) against the repo's storage package on a disposable
`postgres:16-alpine` container: upsert a week with week start 2025-01-06, then
read it back through the upsert's own `RETURNING`, the single-week lookup and the
trend list. All three returned `week_start = 0001-01-01 00:00:00 +0000 UTC`,
marshalling to `"0001-01-01T00:00:00Z"`. The upsert path is the one the
single-week read uses on a cache miss (`backend/storage/analytics.go:80-82`) and
the lookup is the one it uses on a hit (`backend/engine/analytics.go:37-44`), so
the single-week read emits `"0001-01-01"` on both paths, and the trend read emits
`"0001-01-01T00:00:00Z"` on every row.

The lookup by week itself works, because the query binds the week start as a
formatted string (`backend/storage/analytics.go:93`); only the value read back is
wrong. Rows are stored with the correct date.

The only existing storage test for these functions
(`backend/storage/analytics_test.go:12-39`, PAC-28) asserts on the meeting-titles
array and never on the week start, which is how this survived.

### 2.4 Inherited `x-uncertain` markers on the touched operation and schema

- Single-week read, 404: emitted when the engine returns a nil week with a nil
  error (`handlers_analytics.go:38-41`); marked believed unreachable
  (`habits.yaml:337`). The audit's U-01 instruction is "write a test; if it cannot
  be reached, delete the branch and the documented 404"
  (`docs/factory/api-audit.md:469`). Reading the code: on a cache miss the result
  is the upsert's `RETURNING` row, and the upsert scanner returns `(nil, nil)` only
  on `sql.ErrNoRows` (`backend/storage/analytics.go:133-135`), which an
  `INSERT … ON CONFLICT DO UPDATE … RETURNING` does not produce. On a cache hit the
  result is non-nil by construction (`engine/analytics.go:42-44`).
- Single-week response, buffer minutes: never assigned by the engine
  (`backend/engine/analytics.go:185-202` builds the stored struct with no buffer
  field), so always 0 for engine-computed weeks (API-057). Marked `x-uncertain`
  (`habits.yaml:1164`) although the behaviour is fully traced.

### 2.5 Who consumes these reads today

- Web app: no call to either read. The habits/analytics notes record this
  (`contracts/openapi/paths/habits.notes.md:20-29`) and a grep of
  `smart-calendar-flow/src` on 2026-10-27 (excluding `src/api/generated/`) finds no
  reference to either path or to the two schemas. The generated files
  (`src/api/generated/types.ts`, `schemas.ts`) do contain both schemas, so they
  change on regeneration.
- MCP server and e2e suite: no reference to either path or to the habit completion
  rate (grep of `mcp/` and `e2e/`, 2026-10-27).
- Both operations are `handwritten` in `contracts/openapi/MIGRATION.md:53-54`.

## 3. Desired behaviour

- Each analytics week reports the Monday it actually starts on, on every path that
  returns it: freshly computed, served from cache, or listed in the trend.
- The week start has **one** representation across the single-week read and the
  trend read: the calendar date of that Monday, with no time of day and no time
  zone (D-1). A client can take a trend row and the single-week response for the
  same week and compare their week starts as equal values.
- Within any one analytics response, every proportion is on the **same scale**:
  a percentage from 0 to 100 (D-2). That includes the habit completion rate, which
  moves from 0..1 to 0..100; the breakdown percentages and the focus score already
  use this scale and do not change. The contract states the scale on every such
  value, so a reader never has to infer it.
- The scale is the same on both reads, so a trend row and the single-week response
  for the same week report the same habit completion rate.
- Weeks computed and stored before this change report on the new scale too. A
  client never sees a mixture of scales across rows of one trend.
- No stored analytics is deleted or lost (D-3).
- The web app's generated types are regenerated against the changed contract in a
  paired web change, so the two repos do not drift.

## 4. Acceptance criteria

Fixtures are those of the backend's existing database-backed tests. "Week W" is
the Monday 2025-01-06 unless stated.

AC-1. Given a stored analytics week for week W, when the user requests the trend,
then that row's week start is the calendar date 2025-01-06 in the single format the
contract documents, and not the zero date. [unit]
*Fails today: the row reports `0001-01-01T00:00:00Z` (§2.3).*

AC-2. Given no stored analytics for week W, when the user requests the single week
containing any date in W (so the week is computed and stored), then the response's
week start is 2025-01-06 in the same documented format as AC-1. [unit]
*Fails today: `"0001-01-01"`.*

AC-3. Given analytics for week W computed less than 24 hours ago, when the user
requests the single week again (served from cache), then the response's week start
is 2025-01-06 in the same documented format. [unit]
*Fails today: `"0001-01-01"`.*

AC-4. Given stored analytics for week W, when the user requests both the single
week for W and the trend, then the week start of the single-week response and of
the trend row for W are byte-for-byte equal strings. [unit]
*Fails today: `"0001-01-01"` vs `"0001-01-01T00:00:00Z"`; and, once AC-1 to AC-3
pass in isolation, would still fail if the two reads kept two formats.*
(This is the ticket's "Go test on both".)

AC-5. Given the contract, when the two read operations' response schemas are
inspected, then the week start is declared with one and the same format on both,
and neither operation's description says the two differ. [contract]
*Fails today: `date` vs `date-time`, and both descriptions call out the
difference.*

AC-6. Given a week in which 3 habit occurrences were completed out of 4 scheduled
or completed, when the user requests that single week, then the habit completion
rate is 75 (on the 0..100 scale) and every breakdown percentage and the focus score
in the same response are also on 0..100. [unit]
*Fails today: the rate is 0.75.*

AC-7. Given the same stored week as AC-6, when the user requests the trend, then
that row's habit completion rate is 75, equal to the single-week value. [unit]
*Fails today: 0.75.*

AC-8. Given a week stored before this change with a habit completion rate stored
as the fraction 0.5, when the user requests the trend or the single week (cache
hit), then the reported rate is 50, and no stored analytics row was deleted. [unit]
*Fails today: 0.5. Guards against a fix that changes only the compute path and
leaves older rows on the old scale.*

AC-9. Given the contract after this change, when the single-week read operation is
inspected, then it carries no `x-uncertain` marker: either its "not found"
response is removed together with the branch that produced it, or a test drives
that response through the real handler and the marker is dropped. [contract]
*Fails today: the marker is present (`habits.yaml:337`).*

AC-10. Given the contract after this change, when the single-week response schema is
inspected, then its buffer minutes property carries no `x-uncertain` marker and its
description states the value is always 0 for engine-computed weeks and links the
open defect API-057. [contract]
*Fails today: the marker is present (`habits.yaml:1164`).*

AC-11. Given a week with no habit occurrences, when the user requests that single
week, then the habit completion rate is 0 (unchanged meaning: "none scheduled"
still reports 0, not an error or a missing value). [unit]
*Protects an edge case the rescale could break; passes today in its 0..1 form.*

AC-12. Given the merged contract change, when the web types are regenerated and the
web gate runs on the paired web branch, then it exits 0 and the regenerated files
are committed; and the backend gate exits 0 on the backend branch. [contract]
*The ticket's third acceptance item; proven by gate output in both PRs, not by a
new test.*

## 5. Explicitly out of scope

- **Trend rows gaining the per-category breakdown** (API-055 second half). It is an
  additive feature, not a consistency fix; proposed for PAC-42-B (D-4).
- **Habit occurrence date serialised as a timestamp.** Same defect family, different
  domain and consumer set; PAC-42-B.
- **Habit ownership 403 vs 404** (API-056) and **acknowledgement 200 vs 202**
  (API-083). Status-code changes on operations this spec does not touch; PAC-42-B.
  API-056 also has a privacy angle (403 confirms an id exists) that deserves its own
  challenge rather than riding along here.
- **Computing buffer minutes** (API-057). Only its `x-uncertain` marker is resolved
  (AC-10, D-5).
- **Which time zone defines "this week"** for the single-week read with no date.
  The default date is the server's current time in the server's zone
  (`handlers_analytics.go:31`, `engine/analytics.go:24-30`); changing that is a
  behaviour change for users near midnight and is not what this ticket asks. Open
  question OQ-3.
- **Rounding.** The breakdown percentages are unrounded today; the rescaled habit
  completion rate is not rounded either. Presentation is the client's job.
- **Any web UI.** The web app renders neither read; the paired web change is a
  type regeneration only.

## 6. What must not change

- The trend read returns stored rows only, never computes, newest first, with the
  `weeks` limit falling back to 8 outside 1..52
  (`handlers_analytics.go:49-66`; contract `habits.yaml:351-371`). **No test
  protects this.**
- The single-week read serves a row computed within the last 24 hours from cache
  and recomputes otherwise (`engine/analytics.go:37-46`). **No test protects this.**
- Breakdown: exactly five entries, fixed order and colours, percentages on 0..100,
  0 when total is 0 (`handlers_analytics.go:121-134`). **No test protects this.**
- Focus score: integer 0..100, 50 when there is neither focus nor meeting time
  (`engine/analytics.go:177-183`). **No test protects this.**
- An empty week's meeting titles encode as `[]` on all three storage paths —
  protected by `TestAnalyticsWeek_NoMeetingTitles_EncodesEmptyArray`
  (`backend/storage/analytics_test.go:12`, PAC-28).
- Lookup of a stored week by its Monday keeps working (it is correct today,
  §2.3).
- Manager and team analytics responses, which already use a bare date for their
  week buckets (`handlers_manager.go:569`), are untouched.
- Every other field of both responses keeps its name, type and meaning.

The absence of handler-level tests on both reads is itself a finding: the plan
should add characterisation tests for the four unprotected behaviours above before
changing the handlers.

## 7. Rollback

Reverting the backend PR restores today's behaviour exactly (including the zero-date
defect). Under D-3 stored data is not rewritten, so there is nothing to undo in the
database and no data is lost. If the plan nonetheless chooses to rescale stored
values with a migration, its down migration must restore the 0..1 values, and the
plan must show the round trip is exact for the values the engine can produce
(ratios of small integers); the challenger should treat a lossy down migration as a
blocker.

The web PR is a type regeneration; reverting it with the backend PR restores the
previous generated types. Since no web code reads these types (§2.5), neither
revert order breaks the web app's build.

## 8. Open questions

- **OQ-1 — Confirm the week-start representation (D-1).** Owner: product owner.
  Resolve by confirming D-1 or naming the alternative before the contract merges.
- **OQ-2 — Confirm the rate scale (D-2).** Owner: product owner. Same resolution.
  The alternative (everything as 0..1 fractions) changes five breakdown values and
  the focus score instead of one value, and contradicts the focus score's documented
  0..100 range.
- **OQ-3 — Which zone defines "this week".** Owner: product owner, via PAC-42-B or
  its own issue. Out of scope here (§5); recorded so it is not lost.
- **OQ-4 — Is the single-week "not found" branch reachable?** Owner: contract
  author / plan author. Resolve per AC-9 by a test; §2.4 argues it is not.
- **OQ-5 — Create PAC-42-B and the API-057 issue.** Owner: triage / product owner.
  This stage cannot create Linear issues; the items are listed in §0.

## Decisions

*None of these has a recorded owner answer. Each is the most conservative option
consistent with PRODUCT.md and earlier owner decisions recorded in `docs/specs/`;
none deletes user data or widens who can see data.*

- **D-1 — The week start is the calendar date of the Monday (`YYYY-MM-DD`), on both
  reads.** It is what is stored (a `DATE` column), what the single-week read is
  already documented to emit, what the single-week read accepts as input, and what
  every other week-bucketed analytics response already emits. Choosing it changes
  only the trend read's documented format. *Assumed by the factory, pending owner
  confirmation.*
- **D-2 — Every proportion in an analytics response is a percentage on 0..100.**
  Two of the three existing values (breakdown percentages, focus score) already use
  it; only the habit completion rate moves. Values are not rounded. *Assumed by the
  factory, pending owner confirmation.*
- **D-3 — Stored analytics are not rewritten or deleted to achieve D-2.** Older
  rows must still report on the new scale (AC-8); how is the plan's choice, but the
  preferred path leaves the database untouched so rollback is a code revert. *Assumed
  by the factory, pending owner confirmation.*
- **D-4 — Split.** PAC-42 covers the week start (format and value) and the rate
  scale. The trend breakdown, the habit occurrence date format, API-056 and API-083
  move to a proposed PAC-42-B; API-057 to its own issue. PAC-42 is not closed until
  PAC-42-B exists in Linear and is linked from this spec. *Assumed by the factory,
  pending owner confirmation.*
- **D-5 — The buffer-minutes `x-uncertain` is resolved by certainty, not by
  implementation.** The behaviour is fully traced (always 0, §2.4), so the contract
  states it as fact and links API-057, per factory §7.2 ("the contract describes what
  is"). *Assumed by the factory, pending owner confirmation.*
