# Contract: PAC-90 (PAC-81 Part A follow-ups, items 1–4)

- **Linear**: PAC-90, https://linear.app/paceday/issue/PAC-90
- **Spec**: `docs/specs/PAC-90.md`, challenged 2026-11-12 accept-with-changes
  (C-1..C-9 applied in place).
- **Stage**: contract-author, revision 1, 2026-11-12. Awaiting contract-challenger.
- **Manifest**: `contracts/features/PAC-90.yaml`.
- **Scope**: the items 1–4 PR (spec D-6). Item 5 is a follow-up PR that waits
  for PAC-88 (section 6).

Verification (worktree of `factory/pac-90-spec`, Windows, Python 3):
`scripts/openapi_assemble.py --check` rc 0 (`OK: bundle is in sync (106 paths)`),
`scripts/openapi_migration_report.py --check` rc 0 (`OK: MIGRATION.md covers all
128 operations (10 generated)`), `scripts/openapi_gen_go.sh --check` rc 0
(`OK: backend/api/gen matches the contract`). Bundle sha256 on origin/main
4f798cb (`git show origin/main:contracts/openapi/openapi.yaml | sha256sum`):
`c477dbe3b05ddc81ec73698ef566ba511c090550665e8fee3768d4f954259346`, unchanged.

## 1. What changed in the OpenAPI contract

Nothing. No operation, schema, status or header changes. Items 2 and 4 alter
only the access-log line and whether a handler's flush reaches the client, and
no handler flushes today. Item 1 alters what happens to a database connection
after a panic, which is not a documented response. Item 3 is test-only.

## 2. Access-log status field (item 2, binding for the PR)

The fourth field of the PAC-81 AC-A13 line is, in order of precedence:

| Case | Field |
|---|---|
| A panic propagated through the log middleware | `panic` (unchanged) |
| The first `WriteHeader`/`Write`, or a flush, reached the status recorder | that status; a flush before any write is `200` |
| Nothing reached the recorder and the request context had ended when the handler chain returned | `-` (client gone, or the server cancelling at the end of the drain) |
| Nothing reached the recorder and the context is live | `200` (net/http's implicit 200) |

The recorder must see a flush itself: inferring 200 from a live context is not
enough (a client that leaves after a flushed 200 must log `200`, AC-8).

## 3. Flush through the production chain (item 4, binding)

Chain, outer to inner: Sentry's wrapper (has `Flush`) → `statusWriter` →
CORS → body cap → `budget.writer` → [PAC-71: `guardedResponseWriter`] → handler.

1. `budget.writer.Flush` keeps its gate: under the state lock, a no-op once
   the budget answered; otherwise it commits the response (`begin()`) and
   forwards the flush outward through the whole remaining chain, including
   writers that only expose `Unwrap`.
2. `budget.writer` still has no `Unwrap`, and `Hijack` is still refused. A
   handler can reach the outer writers only through the budget's gated methods.
3. `statusWriter` records a flush before any write as 200 and forwards it.
4. Any writer that decides the response on its first write (today: the
   budget; with PAC-71: `guardedResponseWriter`) must implement `Flush` and
   decide before forwarding. Unwrap-only traversal is not allowed for such a
   writer (spec C-3, AC-8b).

## 4. Test seams the PR may add

Package-level function variables, nil in production, in the style of
`beforeBookingConn` (`engine/booking.go:423`):

- `engine`: one seam after `budget.Commit` and before the insert (AC-1a); one
  inside the save step after the insert's `*Row` is returned and before `Scan`
  (AC-1b). Seams may panic; production behaviour with them nil is unchanged.
- `api`: one seam in `finishStepup` between the reservation and
  `budget.Commit` (AC-5b).

No seam may change behaviour when nil, and none is exported.

## 5. Interplay with PAC-71 (#242)

PAC-71 adds `guardedResponseWriter` (Unwrap, no Flush). Whichever of PAC-71 and
PAC-90 merges second gives it a `Flush` that decides first (section 3.4) and
adds `TestPAC90_GuardedFlushStaleSessionIs401` (AC-8b). If PAC-90 merges
second, that one change to `guardedResponseWriter` is in PAC-90's allowed scope
as an amendment recorded here; if PAC-71 merges second, it is PAC-71's. The
step-up fixture of AC-5/5b/6 must satisfy PAC-71's identity re-check if PAC-71
lands first (spec section 9).

## 6. Item 5 (follow-up, depends on PAC-88)

Not in this manifest. PAC-88 (`impl/pac-88-api`) rewrites
`sendConfirmationEmail`'s signature and its call site, which item 5 must
change. After PAC-88 merges, item 5 gets a revision of this manifest with:
spec AC-9..AC-11; the C-6 criterion (an email not started before the hard
cancel is not started); OQ-1's revised default (a context-aware sender beside
the unchanged `outbound.SendMail`, `api/handlers_teams.go` untouched);
allowedPaths `engine/booking.go` (`completeBooking`, `sendConfirmationEmail`,
`sendMail`), `internal/outbound`, engine tests.

## 7. Alternatives rejected

- **Document `Flush` as a no-op instead of forwarding it.** Rejected (spec D-4):
  the first streaming feature would silently buffer, and the test recorder
  hides it.
- **Forward flushes only through `http.ResponseController`'s unwrap chain,
  everywhere.** Rejected: it skips writers that gate or record on first write.
  It would let a stale session's flush commit a 200 with the handler's headers
  past PAC-71's guard, and would leave `statusWriter` unaware of the 200.
- **Give `budget.writer` an `Unwrap` so the standard controller handles flush
  (and deadlines).** Rejected: it hands handlers the raw writer and a way to
  write around the budget (spec section 6).
- **A plain function-scoped `defer conn.Close()` in `ConfirmBooking`** (the
  issue's wording). Rejected: it stalls a pool of one until the budget (C-2),
  and a deferred `Close` alone hangs the handler on a panic inside a query (C-1).
- **Ship all five items in one PR.** Rejected (D-6): item 5 conflicts directly
  with PAC-88's email changes, and items 1–4 do not need to wait.

## 8. Consumers

None outside the backend: smart-calendar-flow and mcp see no response change.
Operators see `200` where the log said `-` for live unwritten responses; the
runbook does not describe the field's values (checked), so it needs no change.

## 9. Stage-4 notes (impl/pac-90-api, 2026-11-12)

No contract mistake found; the bundle hash is unchanged (main still 4f798cb).
Test names are as listed in the manifest, with these readings and additions:

- AC-1b is an engine test, so "a following booking is 201" is read as
  `ConfirmBooking` returning the saved booking with no error (the value the
  route turns into its 201) within 2 s.
- Added, beyond the manifest list: `TestPAC90_BookingReleasesBeforeCompletionOnPoolOfOne`
  (engine; the release still happens before the completion unit on a pool of
  1, the engine-level twin of AC-2) and `TestPAC90_FlushForwardsThroughUnwrapOnlyWriter`
  (budget; section 3.1's "including writers that only expose Unwrap").
- The AC-1b seam wraps the save step's querier only while the seam is set; with
  it nil the insert runs on `storage.BindConn` exactly as before.
- AC-8b: PAC-71 (#242) merged first (main 80f68a4), so PAC-90 is second and
  carries AC-8b (section 5): `guardedResponseWriter.Flush` decides first and
  forwards only when the handler's response stands; test
  `TestPAC90_GuardedFlushStaleSessionIs401`. The 401's header handling is
  otherwise unchanged (a separate PAC-71 fix owns it).
- Amendment (after merging main with PAC-71): AC-5 found that PAC-71's
  step-up check 4b (`stepupIdentityMatches`, `api/handlers_sso_stepup.go`)
  read the binding with a context-less query on the pool, so with the pool
  held the handler goroutine waited past the budget (PAC-81 AC-A9). The
  fixture cannot hold the connection anywhere else (check 4b is the last
  step before the record). The read now runs on `storage.Bind(ctx, db)`;
  `stepupIdentityMatches` takes the request context. allowedPaths widened by
  this one function; no behaviour change while the pool is free (a failed
  read still fails closed).
