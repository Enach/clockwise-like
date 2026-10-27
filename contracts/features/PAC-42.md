# PAC-42 contract: one week-start format and one percentage scale on the analytics reads

Spec: `docs/specs/PAC-42.md` on `factory/pac-42-spec` at `93491a2` (revision 1).
Manifest: `contracts/features/PAC-42.yaml`. Fragment: `contracts/openapi/paths/habits.yaml`.
The source was read on `main` at `6d751f9`.
Paired web branch: `factory/pac-42-contract-types` on Enach/smart-calendar-flow
(the three files under `src/api/generated/`, regenerated, never hand-edited).

## 0. Status of the input

The spec has not been accepted. Its status line reads "challenged (round 1 answered
in Revision 1)". Round 1 returned "accept-with-changes" with one blocking item
(rollback under D-3), and revision 1 answers it. Round 2 has not run, and the spec is
not merged to `main`. The factory has drafted contracts against specs in this state
before (PAC-25, PAC-40), and this contract is also a `draft`. The challenger should
check two things:

- Revision 1 still holds.
- If round 2 changes an acceptance criterion, this contract follows the change.

Do not merge this contract before the spec is accepted.

## 1. What the contract says now that it did not say before

| Area | Before | After |
|---|---|---|
| `AnalyticsWeekRow.week_start` (trend read) | `format: date-time`, "Midnight UTC … NOT the "2026-03-02" shape" | `format: date`, `YYYY-MM-DD`, equal to the single-week value for the same week, never the zero date |
| `AnalyticsWeekWithBreakdown.week_start` (single-week read) | `format: date`, "DIFFERENT from the trends endpoint" | `format: date` (unchanged), same wording as the trend row |
| `habit_completion_rate` (both reads) | 0..1 fraction, no bounds | Percentage, `minimum: 0`, `maximum: 100`; storage keeps the unscaled ratio (spec D-3) |
| `AnalyticsWeekRow.focus_score` | No range and no scale stated | Percentage, `minimum: 0`, `maximum: 100` (same as the single-week schema already had) |
| `AnalyticsBreakdownEntry.percentage` | Formula only | Percentage scale stated, plus why it has no maximum |
| `getAnalyticsWeek` 404 | Documented, `x-uncertain` "believed unreachable" | Removed. The 500 description says there is no 404 and that a kept nil guard answers 500 |
| `buffer_minutes` (single-week) | `x-uncertain` | Stated as always 0 for engine-computed weeks, linked to open defect API-057; the trend row says the same |
| Operation and schema descriptions | Both say the two reads differ | Both say the two reads share formats and scales; only `breakdown` differs |

Nothing else changes: no field is added, renamed or removed, no status code other
than the single-week 404 changes, and both operations keep their parameters,
ordering, cache rule and limits.

## 2. The week start: `format: date` on both schemas (D-1, C-2)

The spec decides the value (D-1, the calendar date of the Monday). The contract
decision is how to declare it.

**Chosen:** `type: string, format: date` on both schemas, with a description that
names the shape (`YYYY-MM-DD`), says the two reads are equal strings for the same
week, and says the value is never the zero date. This is the declaration
`ManagerAnalyticsWeek.week_start` (`paths/teams.yaml`) and the single-week `date`
query parameter already use, so all week-bucketed analytics in the contract now read
the same way. The generators follow: Go `openapi_types.Date` on both, zod
`z.iso.date()` on both (it was `z.iso.datetime()` on the trend row).

**Rejected: `format: date-time` on both** (midnight UTC of the Monday). It would
change the single-week read instead of the trend read. A timestamp also states a
time of day and a zone that do not exist: the column is a `DATE`
(`011_analytics.up.sql:4`), and the Monday is computed in the server's zone
(`engine/analytics.go:24-30`), so "midnight UTC" would be a second, wrong claim.
Spec D-1 also rejects it.

**Rejected: adding `pattern: '^\d{4}-\d{2}-\d{2}$'`.** `format: date` already says
this and the zod generator already enforces it (`z.iso.date()`). A pattern on these
two schemas alone would make them differ from every other date in the contract for
no added protection. It cannot express the real defect either: `"0001-01-01"`
matches any date pattern. The zero date is excluded by AC-1 to AC-3 (unit tests),
not by the schema.

## 3. The rate scale: one name, a new scale, declared bounds (D-2, C-4, C-5)

**Chosen:** `habit_completion_rate` keeps its name and its type (`number`,
`double`). Its description now says it is a percentage where 100 means every counted
occurrence was completed, and the schema declares `minimum: 0` and `maximum: 100` on
both reads. The bound is honoured: the engine divides completed occurrences by a
count that includes them (`engine/analytics.go:91-107`), so the ratio is in [0, 1]
and the response value is in [0, 100].

**Rejected: a new field (`habit_completion_percent`) beside or instead of the old
one.** A rename makes a stale client fail loudly instead of misreading 75 as 7500%.
That is the right trade when consumers exist, but none do (spec §2.5: no web, MCP or
e2e code reads either operation, checked again on web `origin/main` `ad52160`). A
rename would also break spec §6 ("every other field keeps its name") and leave the
trend and single-week schemas with a dead field or a migration story for a value no
one has read. If a consumer appears before this merges, revisit this.

**Rejected: a unit extension (`x-unit: percent`) on each value.** No generator in
§4 of the factory README reads it, so it would be a second place to state the scale
that nothing checks. The description and the bounds are what the generated types
carry.

**Bounds where honoured, none where not (C-5).**
- `focus_score` on the trend row gets `minimum: 0, maximum: 100`. That copies what
  the single-week schema has declared since the contract was extracted, for the same
  stored value. The upper bound is enforced by a clamp
  (`engine/analytics.go:182-188`). The lower bound depends on focus blocks not
  ending before they start: `focusBlockMinutes` sums `end - start` with no floor and
  no database constraint forbids a reversed block. That residual exists today on the
  single-week schema. It is not introduced here and is not in PAC-42's scope; it is
  named so the challenger can decide whether it needs an issue.
- `AnalyticsBreakdownEntry.percentage` gets **no** bounds. It can exceed 100: the
  total counts Monday–Friday working windows while meeting and personal minutes count
  every timed event in seven days (spec §2.2, AC-13). A `minimum: 0` would rest on
  the same reversed-interval assumption as above for habit minutes, so it is left
  out too. The description states the scale and why no maximum is declared.

**Wording (AC-13).** No proportion's description in either response schema calls
itself a 0..1 fraction. The rate descriptions say that storage keeps the "unscaled
ratio" and that the multiplication by 100 happens on read. That records D-3 in the
contract without using the phrase AC-13 forbids.

## 4. The single-week 404 (audit U-01, spec OQ-4, C-1)

The audit's instruction is: "Write a test … if it cannot be reached, delete the
branch and the documented 404." The handler answers 404 only when
`ComputeWeek` returns `(nil, nil)` (`handlers_analytics.go:38-41`). That needs one of:

- a cache hit with a nil row: impossible, because the cache path returns only when
  `cached != nil` (`engine/analytics.go:42-44`);
- `compute` returning `(nil, nil)`: it returns the result of `UpsertAnalyticsWeek`,
  whose scanner returns `(nil, nil)` only on `sql.ErrNoRows`
  (`storage/analytics.go:133-135`).

**Observed, 2026-10-27.** A throwaway storage test (not committed) ran on a
disposable `postgres:16-alpine` container against `main` at `6d751f9`. It called
`UpsertAnalyticsWeek` twice for the same user and week, first inserting and then
taking the `ON CONFLICT DO UPDATE` path. Both calls returned a non-nil row with a
nil error. The stored row read back directly held `week_start = 2025-01-06` and the
rate exactly as written (0.5). The same run reproduced the spec's §2.3 zero-date
defect: both the upsert result and the trend list encoded
`"week_start":"0001-01-01T00:00:00Z"`. A `DO UPDATE` with no `WHERE` always returns
the row in PostgreSQL, so neither path yields no row.

**Chosen (C-1):** the 404 is removed from the contract. The build removes the
`result == nil` branch. If the implementer keeps a guard against a future storage
change, it must answer the documented 500 `{"error":"internal error"}`, never 404.
The 500 description says so. A nil row is a server invariant failure, not "your
week does not exist". Every week exists in the sense this read uses: it is computed
on demand.

**Rejected: keep the 404 and drive it with a stubbed storage layer.** The storage
calls are package functions, so a stub needs a seam that exists only to reach a
branch production cannot reach. The test would then document a response no client
can ever receive. AC-9 allows either option, and removing the branch is the one the
audit prescribes.

## 5. `buffer_minutes` (audit U-10, spec D-5)

The marker is removed and the description states the behaviour as fact: always 0 for
engine-computed weeks, open defect API-057. The trend row's `buffer_minutes`, which
had no description, says the same. This is spec D-5. It departs from U-10's
prescribed "wire up or drop" and from factory §7.1, and it is pending owner
confirmation (OQ-8). The contract records what is; it does not decide that question.

## 6. Two schemas, not one (C-3)

The two response schemas now declare the same fields identically, and the trend row
is the single-week response minus `breakdown`.

**Rejected: `AnalyticsWeekWithBreakdown: allOf: [AnalyticsWeekRow, {breakdown}]`.**
Both schemas set `additionalProperties: false`. Under `allOf`, each branch then
rejects the other's properties, so the composed schema accepts no response at all.
Dropping `additionalProperties: false` to make `allOf` work would loosen both
schemas, and the generated zod would no longer be one `strictObject` per schema.
That is a larger, riskier generated-code change than this ticket needs. The
duplication is held in check by AC-5 and AC-13, which inspect both schemas, and by
the descriptions, which each name the other.

## 7. Breaking-change accounting

| Change | Wire effect | Consumers |
|---|---|---|
| Trend `week_start` timestamp → date | Different string shape | None. No web (outside `generated/`), MCP or e2e reference (spec §2.5). Generated Go type changes from `time.Time` to `openapi_types.Date`; no hand-written Go uses `gen.AnalyticsWeekRow` (`go build ./...` passes on this branch) |
| `habit_completion_rate` ×100 on both reads | Same type, values 100× larger | None, as above. Team analytics (`team_availability.go:176`) reads the same stored rows but not this field. The rescale must be applied where the two responses are built, not in the storage layer (manifest `allowedPaths`), so it cannot reach that path |
| Single-week 404 removed | A response that never occurred is no longer documented | None |
| Bounds and descriptions | None | Generated web types gain bounds and comments only |

Before PAC-42, every row these reads returned had the zero date as its week start
(spec §2.3). So no client could have relied on the old timestamp value in any
meaningful way.

## 8. What a challenger should try to break

- A response the contract now permits that the server cannot produce, or the
  reverse. Examples: a `habit_completion_rate` above 100; a negative `focus_score`
  (see §3, residual); a breakdown percentage above 100 (allowed on purpose).
- Whether `format: date` on the trend row is honoured on **every** path. That covers
  the trend list, the upsert's `RETURNING` (cache miss) and the lookup (cache hit).
  The zero-date fix lives in both storage scanners, and AC-1 to AC-4 test all three
  paths.
- Whether AC-13's "none describes itself as a 0..1 fraction" holds literally on the
  bundle (it does: `grep -n "0\.\.1" contracts/openapi/paths/habits.yaml` finds
  nothing).
- Whether removing the 404 hides a reachable path. §4 gives the argument and the
  observation.

## 9. Rollback

Revert the implementation PR and keep this contract revision immutable. Nothing
stored changes (spec D-3, §7): the rate stays an unscaled ratio in storage for every
week, and `week_start` is already stored correctly. So a revert restores today's
behaviour for every row, with no data step and no data deleted. The web change is a
type regeneration that no web code reads, so the two reverts can happen in either
order.
