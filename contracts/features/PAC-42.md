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

## Challenge — 2026-10-27

**Verdict**: accept-with-changes

Reviewed on `factory/pac-42-contract` at `e212c05`, committed state only. I read it
against spec revision 1 (`factory/pac-42-spec` at `93491a2`), the code on `main` at
`6d751f9` (the branch's merge base), web `origin/main` `ad52160`, and the paired web
branch `factory/pac-42-contract-types` at `6dace6c`. I did not consult the author.

**What I ran, all on `e212c05`. Each ran in a fresh LF clone in Docker, because the
host api checkout is on another stage's branch.**
- `make openapi-check WEB=/nonexistent`: assembler `OK: bundle is in sync (98
  paths)`, migration report `OK ... 119 operations`, `OK: backend/api/gen matches the
  contract`, `openapi-check(api) rc=0`. The web half was SKIPPED there, so I ran it
  separately (next item).
- Web `make openapi-check CONTRACT=<e212c05 bundle>` on `6dace6c`: `OK:
  src/api/generated matches the contract`, rc=0. `tsc --noEmit -p tsconfig.app.json`
  rc=0. I did **not** run the full web `make verify` / `web-gate.sh`.
- `git show HEAD:contracts/openapi/openapi.yaml | sha256sum` gives `c06388ba…548873`.
  This equals the manifest's `contractHash`.
- `go build ./...` and `go vet ./api/...` (backend) both rc=0.
- `go test ./... -count=1 -p 1` (backend) ran against a disposable `postgres:16-alpine`.
  Every package was `ok`, rc=0. This includes the PAC-21 contract test that parses the
  bundle, so the new 500 wording passes its raw-error rule. I did **not** run
  `api-gate.sh` itself, lint or coverage, because the gate needs the host checkout on
  this branch.
- A throwaway storage test (run inside the container, never committed) called
  `UpsertAnalyticsWeek` twice for the same user and week. The first call inserted and
  the second took `ON CONFLICT DO UPDATE`. Both returned a non-nil row with a nil
  error. The upsert, `GetAnalyticsWeek` and `ListAnalyticsTrends` all read back
  `week_start = 0001-01-01T00:00:00Z`, while `SELECT week_start::text` returned
  `2025-01-06`. That independently reproduces §4 (U-01) and spec §2.3.

### Blocking

1. **`contracts/openapi/paths/habits.notes.md` now contradicts the contract. It is not
   in the diff.** That file is the fragment's companion notes, and it still states, as
   current fact:
   - `:68-73` (item 6): `week_start` has two wire formats, "Modelled as two distinct
     schemas", one of them RFC3339.
   - `:110-111` (item 15): "`habit_completion_rate` is a 0..1 fraction".
   - `:210-213` (x-uncertain list item 1): the `/api/analytics/week` 404 is "Marked
     inline in `habits.yaml`" and "Documented because the code path exists".
   - `:216` (item 3): `buffer_minutes` is listed as an inline `x-uncertain`.

   After `e212c05`, all four are false about the contract. A plan author who reads the
   fragment's notes would be told that the 404 is still documented and that the rate is
   a fraction. PAC-28 handled the same situation in the same file (`:225-227`, "**Resolved
   by PAC-28:** …"). **Fix:** annotate each of the four items with "**Resolved by PAC-42:**"
   plus the new declaration:
   - `week_start` is `format: date` on both schemas.
   - The rate is a percentage on 0..100 on both reads, and storage keeps the ratio.
   - The 404 was removed, with a pointer to PAC-42.md §4.
   - The `x-uncertain` on `buffer_minutes` was removed, and API-057 is still open.

   Item 14 (`:106`, buffer always 0) stays true and needs no change. Items 7 and 8 stay
   true, because they are deferred to PAC-42-B.

### Requests wrongly permitted

None that PAC-42 introduces. Neither request surface changed. I attacked both anyway:
- `GET /api/analytics/week?date=2025-13-45`: the contract forbids it (`format: date`).
  The server answers 200 for today's week, because `parseDateParam` falls back silently
  (`handlers_analytics.go:100-110`).
- `GET /api/analytics/trends?weeks=0` or `?weeks=100`: the contract forbids both
  (`minimum: 1, maximum: 52`). The server answers 200 with up to 8 rows
  (`handlers_analytics.go:52-55`).

In both cases the contract is stricter than the server, and both cases predate PAC-42.
Neither is an `x-uncertain` or an audit finding, so §7.1 does not pull them in.

### Responses the client cannot handle

None. No hand-written web code reads either operation. I grepped
`origin/factory/pac-42-contract-types` `src/` outside `generated/` for
`analytics/(week|trends)`, `week_start`, `habit_completion`, `AnalyticsWeekRow` and
`AnalyticsWeekWithBreakdown`. The only `week_start` hits are `src/api/manager.ts:42,
235, 578-599` and `managerTeam.test.ts`, which belong to `ManagerAnalyticsWeek`, a
different operation. The `weekStart` at `client.ts:1107` is the focus-run mock. The
generated zod on the paired branch carries `z.iso.date()` and `.min(0).max(100)`, and
`tsc` passes.

### Responses permitted that the server cannot produce, or the reverse

1. **Until the backend PR lands, the server emits responses the new contract forbids.**
   Today `GET /api/analytics/trends` returns
   `[{"week_start":"0001-01-01T00:00:00Z","habit_completion_rate":0.75,…}]` (reproduced
   above). That fails `format: date` and the "never the zero date" description, and it
   reports 0.75 where the contract now promises 75. This is expected for a contract that
   leads its implementation, so it is not blocking. It does mean that merging this
   contract opens a window in which the bundle describes the post-build server. See
   non-blocking 2.
2. Attacked `habit_completion_rate > 100` and could not break it. The denominator counts
   `scheduled` and `completed` and the numerator counts only `completed`
   (`engine/analytics.go:97-108`), so the ratio is at most 1. 1.0 × 100 is exactly
   100.0 in float64. The only other writers of `analytics_weeks` are test and seed SQL
   (`backend/api/pac34_invite_analytics_test.go:200`, `e2e/seed/seed.sql:206`), and
   both leave the column at its default of 0.
3. Attacked a negative `focus_score` (§3's residual) and could not break it. The only
   writer of `focus_blocks` is `SaveFocusBlock` from `engine/focus_time.go:194`. It is
   reached only after `blockEnd - blockStart >= FocusMinBlockMinutes` (`:174`), and
   settings validation requires that value to be positive (`handlers_settings.go:233`).
   So `focusMinutes >= 0`, and `focus*100/(focus+meeting)` is within [0, 100]. A
   reversed block needs a write outside the app.

   The e2e seed rows store `focus_score` at its default of 50 next to 615/240 minutes,
   which contradicts the documented formula. That only matters if a future e2e reads
   these rows through the trend. Today they are read only through manager analytics.

### Consumers affected

- `smart-calendar-flow/src/api/generated/{types.ts,schemas.ts,schemas.types.d.ts}`
  change on regeneration. `6dace6c` matches the bundle (openapi-check rc=0), and nothing
  else imports them for these schemas.
- `clockwise-like/backend/api/gen/paceday.gen.go`: `AnalyticsWeekRow.WeekStart` changes
  from `time.Time` to `openapi_types.Date`. No hand-written Go references it, and
  `go build` and `go test` pass.
- `mcp/`: no reference to either path, either schema or the rate (grep).
- `e2e/`: no test calls `/api/analytics/week` or `/trends`. `seam.spec.ts:366` calls
  `/recompute` (untouched, 202), and `focus-time.spec.ts:92` calls `/meetings`
  (untouched). The seed's `analytics_weeks` rows are read by manager analytics
  (`engine/manager.go:468-473`, its own SQL), which is not on the rescaled path.
- `backend/engine/team_availability.go:176` shares the storage lookup and reads neither
  changed field. This is correctly recorded in the manifest.

### Contract/implementation divergences

- None beyond Responses item 1, which is the intended stage-4 delta. The two schemas
  are now identical apart from `breakdown`. I checked this programmatically on the
  bundle: `required` differs only by `breakdown`, both set `additionalProperties:
  false`, and every shared property has the same type, format and bounds. That matches
  the claim in both operation descriptions.

### Uncertainties not actually resolved

- **U-01 (404) is determined but only half pinned.** The determination is sound. I read
  the code (`handlers_analytics.go:38-41`, `engine/analytics.go:42-46`,
  `storage/analytics.go:133-135`) and reproduced both upsert paths above. However, the
  audit's prescribed action is "**Write a test**". The manifest's tests cover the insert
  path (AC-2) and the cache hit (AC-3), but **no** acceptance test drives the `ON
  CONFLICT DO UPDATE` path through the read. That path is a stored week older than 24
  hours, recomputed by `GET /api/analytics/week`. The evidence for that half is an
  uncommitted throwaway test, the author's and mine. This is not blocking, because the
  spec's AC-9 allows removal without a test. See non-blocking 1.
- **U-10 (`buffer_minutes`)**: resolved by certainty and backed by code
  (`engine/analytics.go:190-206` sets no `BufferMinutes`). The departure from U-10's
  prescribed action is recorded as owner-pending (OQ-8). It is honest, and nothing in
  it is hidden.

### Verified clean

- AC-13 holds literally. `grep -n "0\.\.1\|fraction" contracts/openapi/paths/habits.yaml`
  returns nothing. Rate and focus score declare `minimum: 0, maximum: 100` on both
  schemas. Breakdown `percentage` has no bounds.
- AC-5 holds: `format: date` on both, and neither operation description says the week
  start differs. The remaining "breakdown exists only on /week" is about `breakdown`,
  not `week_start`.
- AC-9 and AC-10 hold on the bundle. Both operations and all four involved schemas
  (`AnalyticsWeekRow`, `AnalyticsWeekWithBreakdown`, `AnalyticsBreakdownEntry`,
  `MeetingTitle`) carry no `x-uncertain`. I checked this by walking the parsed bundle.
- The manifest's `acceptanceTests` match spec AC-1 to AC-11, AC-13, AC-14 and DC-1, with
  AC-12 withdrawn. The one narrowing is AC-9, from "either" to "removed". It is
  deliberate and recorded as C-1, and it is one of the spec's own options.
- Rejected alternatives are rejected for real reasons. `allOf` with
  `additionalProperties: false` on both branches would accept nothing. A `pattern` on
  `week_start` cannot exclude `"0001-01-01"`. A rename is unjustified with zero
  consumers, and I re-verified the zero consumers.
- The `contractHash` reproduces. The rollback holds under D-3, because nothing stored
  changes. I confirmed that the stored DATE is correct (`2025-01-06`), so only the read
  changes.
- Naming, security and status codes are unchanged apart from the removed 404. The new
  500 description passes the PAC-21 envelope test.

### Non-blocking

1. Plan: add a test for a stored week computed more than 24 hours ago. When `GET
   /api/analytics/week?date=2025-01-08` is requested, it should return 200,
   `week_start: "2025-01-06"`, the recomputed rate on 0..100, and the same row id. That
   pins the conflict half of U-01 and the date fix on the third storage path (the
   upsert after a stale cache). AC-2 and AC-3 do not reach it.
2. Merge order: the contract will describe `week_start` as never the zero date and the
   rate as 0..100 before the server does (Responses 1). Land the backend PR promptly
   after the contract, or say in the contract PR body that the trend read violates the
   bundle until it does.
3. Process: the spec is not accepted, because round 2 has not run. The manifest and §0
   both say not to merge before it is. If round 2 changes any AC, this contract needs a
   re-challenge. Not a schema defect.
4. Float note for the plan: the rate is `ratio * 100` on read, so non-dyadic ratios are
   not exact. For example, 4/7 stored then ×100 gives `57.14285714285714`, while
   `400/7` gives `57.142857142857146`, and `0.57 * 100` gives `56.99999999999999`. Both
   reads use the same expression, so AC-7 equality holds. Tests should keep to exact
   ratios (3/4, 1/2) or compare with a tolerance.

### OWNER questions (not blocking)

- OWNER: D-1 / OQ-1. `week_start` is a bare `YYYY-MM-DD` on both reads.
- OWNER: D-2 / OQ-2. Every proportion is on 0..100, unrounded, and `habit_completion_rate`
  keeps its name with a new scale (C-4).
- OWNER: D-3 / OQ-7. Storage keeps the 0..1 ratio, and the rescale happens on read.
- OWNER: D-4 / OQ-6. Split API-055's second half, API-056, API-083 and `scheduled_date`
  into PAC-42-B (which does not yet exist, OQ-5).
- OWNER: D-5 / OQ-8. Defer API-057 under factory §7.1, keeping `buffer_minutes` as
  "always 0" instead of U-10's wire-up-or-drop.
- OWNER: OQ-3. Which time zone defines "this week".

## Revision 1 — 2026-10-27 (answers contract challenge round 1)

Author: `contract-author`. The challenge was read as written; its author was not
consulted. The bundle is unchanged by this revision (no fragment edit, so
`contractHash` stays `c06388ba…548873`); the changes are to the fragment's notes
file, the manifest and this document.

### Blocking

1. **Stale `habits.notes.md`.** Done. Each of the four items now carries a
   "**Resolved by PAC-42:**" line in the PAC-28 style, and the original text stays
   above it as the history of what was extracted:
   - item 6 (two `week_start` wire formats): `format: date` (`YYYY-MM-DD`) on both
     `AnalyticsWeekWithBreakdown` and `AnalyticsWeekRow`, equal strings for the same
     week, never the zero date; still two schemas (§6).
   - item 15 (rate as a 0..1 fraction): a percentage on 0..100, unrounded, with
     `minimum: 0` / `maximum: 100`, on both reads; storage keeps the ratio and the
     ×100 is on read only (D-3, §3).
   - `x-uncertain` list item 1 (the single-week 404): removed with its marker; a kept
     guard answers the documented 500, never 404; pointer to §4.
   - `x-uncertain` list item 3 (`buffer_minutes`): the inline marker is removed and
     both schemas state "always 0 for engine-computed weeks"; API-057 stays open
     (D-5, OQ-8), so mismatch item 14 is left as is.

   Items 7, 8 and 14 are untouched because they remain true (deferred to PAC-42-B or
   to API-057), as the challenge noted.

### Non-blocking

1. **The `ON CONFLICT DO UPDATE` half of U-01 has no test.** Accepted. The manifest's
   new `planRequirements` names the test the plan must write: a stored week for
   2025-01-06 whose `computed_at` is more than 24 hours old, then
   `GET /api/analytics/week?date=2025-01-08` answers 200 with `week_start`
   `"2025-01-06"`, the recomputed rate on 0..100 and the same row `id` (the upsert
   updated the row; there is no second row). It is recorded as a plan requirement, not
   a new acceptance criterion: it adds no behaviour to the spec, only coverage of a
   path the spec's AC-2 and AC-9 already govern. The 24-hour rule is
   `engine/analytics.go:41`, and the upsert is `storage/analytics.go:55`.
2. **Merge order.** Accepted. The manifest's new `mergeConditions` states that until
   the backend PR lands, the server violates this contract on both reads (trend
   `week_start` `"0001-01-01T00:00:00Z"` and rate 0.75; single-week `"0001-01-01"` and
   the 0..1 rate). The contract PR body will say the same, and the backend PR is to land
   promptly after it.
3. **The spec is not accepted.** Accepted and unchanged: the spec branch is still at
   `93491a2` (checked 2026-10-27), round 2 has not run, and this contract stays a
   `draft`. `mergeConditions` now also records that a round-2 change to any acceptance
   criterion sends this contract back for revision and a fresh challenge.
4. **Floating-point error in the rescaled rate.** Accepted. `planRequirements` tells the
   plan to assert exact values only for exact ratios (0, 1/2, 3/4, 1) and to compare
   other values with a tolerance. I confirmed the challenger's numbers in IEEE-754
   double arithmetic: `(4/7)*100 = 57.14285714285714`, `400/7 = 57.142857142857146`,
   `0.57*100 = 56.99999999999999`, while `0.75*100 = 75` and `0.5*100 = 50` exactly.
   AC-7's equality between the two reads stays exact because both apply the same
   expression to the same stored value. The AC-6/AC-7/AC-14 fixtures (3 of 4) and AC-8
   (0.5) already use exact ratios.
5. **Requests the contract forbids but the server accepts** (`?date=2025-13-45`,
   `?weeks=0`, `?weeks=100`). Recorded, not fixed: both predate PAC-42, neither is an
   audit finding nor an `x-uncertain`, so factory §7.1 does not pull them in, and
   changing them would change behaviour the spec does not ask for. They are listed in
   the manifest's new `knownServerLeniencyOutOfScope` as needing their own issue.
6. **e2e seed `focus_score` of 50 next to 615/240 minutes.** Recorded in the same list:
   harmless while only manager analytics reads those rows, to be fixed before any e2e
   test reads them through either analytics read. Not changed here (`e2e/` is outside
   this contract's scope and no PAC-42 test reads the seed).
7. **Gates the challenger did not run.** Noted. What this revision ran is listed below.

### OWNER questions

Unchanged and still pending owner confirmation, as recorded in the manifest's
`decisionsAssumed` and `followUps`: D-1/OQ-1 (bare `YYYY-MM-DD` on both reads),
D-2/OQ-2 with C-4 (every proportion unrounded on 0..100; `habit_completion_rate` keeps
its name and changes scale), D-3/OQ-7 (storage keeps the ratio; rescale on read
only), D-4/OQ-6/OQ-5 (API-055's second half, API-056, API-083 and `scheduled_date` to
PAC-42-B, which does not exist yet), D-5/OQ-8 (API-057 deferred, `buffer_minutes`
"always 0" rather than U-10's wire-up-or-drop), and OQ-3 (which time zone defines
"this week"). The factory chose the conservative option for each; none deletes data
or widens who sees it.
