# Plan / stage-4 record: PAC-53, GET /api/audit refuses an unreadable `limit` with a fixed JSON 400

- **Spec**: `docs/specs/PAC-53.md` (revision 1, approved after 2 challenge rounds).
- **Contract**: `contracts/features/PAC-53.md` and `.yaml`, revision 1, **approved**
  by contract-challenger at `dff3eb2` (2026-11-19), with two non-blocking notes,
  both folded in here (§2, Docs). `contractHash`
  `45d87ca302897e7ee9ed1a8a64e00b1018e28b020cdec33a38e550d277286b87`.
- **Branch**: `factory/pac-53` (clockwise-like), from `dff3eb2`. `origin/main` was
  still `f074b4d` when this was written, so no merge was needed. Stage 4 does not
  change the fragments, the bundle or `backend/api/gen`, so the hash is unchanged
  (sha256 of the LF bundle recomputed; `openapi_assemble.py --check` and
  `openapi_gen_go.sh --check` pass).
- **Paired web branch**: `factory/pac-53` in smart-calendar-flow. It only
  regenerates `src/api/generated/**` and merges **after** the api PR, within
  `PAIR_MISMATCH_GRACE_MINUTES` (60).
- **Stage**: this file is the stage-4 record, in the house shape of
  `docs/plans/PAC-149.md`. No separate plan-author stage ran. The contract's
  `allowedPaths`, `acceptanceTests` and refusal table, plus spec §6, served as
  the plan.

## 1. Approach

The rule is a small pure function, `auditLimit(rawQuery) (int, bool)`, in
`backend/api/handlers_audit.go`. The handler calls it after the existing
no-user guard and before the storage read. When it refuses, the handler answers
`writeError(w, auditLimitMessage, 400)`.

- **C-9 first, on the raw query.** `url.ParseQuery(r.URL.RawQuery)` returns an
  error for an unencoded `;` or an undecodable `%` sequence anywhere. That error
  is exactly the C-9 condition the contract states. `r.URL.Query()` would drop
  such a pair silently.
- **Then `limit` by its decoded, case-sensitive name** (`q["limit"]`, so
  `%6Cimit` counts as `limit` and `LIMIT` is ignored). The cases:
  - absent → 50;
  - more than one value → refused;
  - otherwise `strconv.ParseInt(v, 10, 64)`. Base 10 refuses empty, spaces,
    decimals, exponents, `0x` and `_`, and accepts one sign and leading zeros
    (C-2, C-3, C-4). Overflow is `ErrRange` and is refused (C-5, C-7);
  - outside 1..500 → refused.
- **Fixed message.** `auditLimitMessage` is a constant, so no refusal can echo
  the input.
- **Handwritten.** The handler refers to no generated symbol (AC-12). It writes
  the 400 with `writeError`, not `gen.ListAuditEntries400JSONResponse`, so the
  contract's falsifying grep still returns nothing.

`storage.ListAuditLog` keeps its defensive clamp unchanged (spec §5). The handler
now passes only 1..500, so the clamp cannot be reached with a value the handler
refuses.

**Why not a shared query helper or middleware:** C-9 applies to this operation
only (spec §5; the manifest forbids a global query-string middleware).

## 2. Files

### Backend (production)

- `backend/api/handlers_audit.go`: `auditLimitMessage`, `auditLimit`, and the
  handler's 400 branch. The deliberately discarded `strconv.Atoi` error is
  gone. It was **not** in `tools/sfguard/baseline.txt`: sfguard targets
  storage/auth/calendar errors, not `strconv`. So no baseline line is removed,
  and sfguard reports "baseline does not grow".

### Tests

- `backend/api/handlers_audit_test.go`: the PAC-23 tests named in spec §6 are
  rewritten, not deleted:
  - `_ForeignTitlesAndPromptsNeverAppear`: the invalid queries now expect 400
    and still assert no foreign text.
  - `_DefaultLimitMatchesContract`: `""` → 50 with 120 rows, and `?limit=10`
    → 10; empty, `abc`, `0`, `-5` and `+50` move to fixed-400 assertions.
  - `_OverMaximumLimitCaps` → `_OverMaximumLimitRefused`: 501, 10000 and
    `99999999999999999999` → fixed 400, with 520 rows stored so a cap would
    show; `?limit=500` → 500.
  - `_NoUserInContextIsRefused` also sends `?limit=abc` and `?limit=%zz`, so
    the handler's own guard comes before the rule.
- `backend/api/pac53_audit_limit_test.go` (new): AC-1..AC-8, AC-11, AC-12 and
  the pure rule test.
- `e2e/tests/audit.spec.ts`: 10000 is dropped from the 200 loop. The activity
  page's request is asserted 200. The PAC-23 AC-10/AC-11 test becomes AC-10
  (no limit → 50; 500 → 500; `10000` and `abc` → 400, `application/json`, the
  exact fixed body). A new test covers the quick-actions preview (AC-9).

### Docs and register

- `contracts/openapi/MIGRATION.md`: the `listAuditEntries` notes cell now opens
  with "PAC-53: parameter and responses modified, kept handwritten (spec §5,
  contract alternative E)." This was challenger note 1. The cell is kept by
  hand and preserved by `scripts/openapi_migration_report.py`, which was re-run
  with `--check`.
- `docs/factory/codebase-notes.md`: the `limit` bullet still described the
  pre-PAC-23 code ("everything out of band becomes 100"). It now states the
  PAC-53 rule. This was challenger note 2.
- `contracts/features/PAC-53.yaml`: status, `gates.contractApproval` (APPROVED
  at `dff3eb2`), `gates.stage4`, and the two `allowedPaths` entries above, each
  dated 2026-11-19. `contracts/features/PAC-53.md`: §7, the approval and
  stage-4 note.

## 3. Acceptance criteria → tests

| AC | Test | File |
|---|---|---|
| AC-1 | `TestPAC53_AC1_AC2_AcceptedCounts` (`""` → 50 of 120); `TestListAuditEntries_DefaultLimitMatchesContract` | `pac53_audit_limit_test.go`; `handlers_audit_test.go` |
| AC-2 | `TestPAC53_AC1_AC2_AcceptedCounts` (1, 10, 500 → 1, 10, 120; `%2B10`, `010` → 10; C-10: `LIMIT=abc` → 50, `%6Cimit=5` → 5, `limit=7&LIMIT=99` → 7) | `pac53_audit_limit_test.go` |
| AC-3 | `TestPAC53_AC3_AC6_RefusedWithTheFixedBody` (empty, bare, abc, 50.5, 1e2, 0x10, 1_0, raw +50, %2050, 50%20; exact body `{"error":"limit must be an integer from 1 to 500"}\n`, `application/json`, strict decode into `gen.ErrorResponse`, no entries even though the user has 12; bodies for `abc` and `99999999999999999999` do not contain them) | `pac53_audit_limit_test.go` |
| AC-4 | same test (0, -0, -5, 501, 10000, 9223372036854775807, 99999999999999999999, -9223372036854775809); `TestListAuditEntries_OverMaximumLimitRefused` | both |
| AC-5 | same test (`10&limit=500`, `10&limit=10`) | `pac53_audit_limit_test.go` |
| AC-6 | same test (`limit=%zz`, `limit=%zz&limit=10`, `x=%zz&limit=10`, `limit=10;x=1`, `x=a;b&limit=10`; and `limit=%zz&limit=10` is not an array) | `pac53_audit_limit_test.go` |
| AC-7 | `TestPAC53_AC7_UnauthenticatedBeforeLimit` (real router via `RegisterRoutes`: unauthenticated `abc`, `%zz`, `10;x=1`, `10&limit=10` → the middleware's text/plain 401; authenticated `abc` → the fixed 400; `5` → 200); `TestListAuditEntries_NoUserInContextIsRefused` (handler guard first) | both |
| AC-8 | `TestPAC53_AC8_ResponsesAreDeclared` (the bundle declares exactly 200/400/401/500; the 400 is `application/json` with `ErrorResponse` = `{error: string}`; every AC-1..AC-6 request gets a declared status; every 400 is `application/json` with exactly one non-empty string key; the count of 400s equals the refused set, so the test is not vacuous) | `pac53_audit_limit_test.go` |
| AC-9 | `AC-1/AC-3/AC-8/AC-13 …` (activity page: `limit=50`, now also status 200, entries rendered); `PAC-53 AC-9: the quick-actions preview asks for 10 and renders them` | `e2e/tests/audit.spec.ts` |
| AC-10 | `AC-10: no limit means 50; PAC-53 AC-10: an unreadable limit is a JSON 400` | `e2e/tests/audit.spec.ts` |
| AC-11 | `TestPAC53_AC11_GeneratedBinderAgrees` (`gen.HandlerWithOptions(gen.Unimplemented{}, …)`: `limit=`, `abc`, `99999999999999999999`, `10&limit=10` → 400; control `10` → 501, the handler's, so the 400s are the binder's) | `pac53_audit_limit_test.go` |
| AC-12 | `TestPAC53_AC12_OnlyTheHandwrittenHandlerServesIt` (walks `backend/`, excluding `api/gen` and `_test.go`: no file names `ListAuditEntries`; `routes.go` has exactly one `"/api/audit"` line, `r.Get("/api/audit", auditHandler(db))`) | `pac53_audit_limit_test.go` |
| rule | `TestPAC53_AuditLimitRule` (`auditLimit` on raw queries, no HTTP: every accepted and refused case above, plus `-1%30`, `+-5`, `--5`, `%2B%2B5`, a zero-width space, a full-width digit, `%6Cimit=5&limit=5` (repeat after decoding), `limit=5;`) | `pac53_audit_limit_test.go` |

### Red first

The new tests were written before the handler change. They were run against the
unchanged `HEAD` handler, with a temporary stub `auditLimit` that reproduced the
old resolution and existed only so the package compiled. The following failed,
each on a 200 where 400 was wanted:

- `_ForeignTitlesAndPromptsNeverAppear`, `_DefaultLimitMatchesContract`,
  `_OverMaximumLimitRefused`;
- `TestPAC53_AuditLimitRule`, `_AC3_AC6_…`, `_AC7_…` (its authenticated `abc`
  got `200 []`).

The regression guards passed, as the spec predicts: AC-1/AC-2, AC-7's 401 half,
AC-11 and AC-12. AC-8 then passed vacuously (every status was 200, which is
declared), so it was hardened afterwards with the 400 count. The stub was
deleted before the handler went in.

## 4. Mutations

Each mutation was applied to `handlers_audit.go` (M12 also to `routes.go`), and
`go test ./api/ -count=1 -run 'PAC53|ListAuditEntries'` was run against a
throwaway `postgres:16.15-alpine`. The files were restored after each run. The
script is not committed. All mutations were killed:

| # | Mutation | Killed by |
|---|---|---|
| M1 | C-9: ignore `ParseQuery`'s error | `AuditLimitRule`, `AC3_AC6`, `AC8` |
| M2 | C-8: accept repeats, first wins | `ForeignTitles…`, `AuditLimitRule`, `AC3_AC6`, `AC8` |
| M3 | C-2: empty value means default | `DefaultLimit…`, `AuditLimitRule`, `AC3_AC6`, `AC8` |
| M4 | C-3: `ParseInt` base 0 (accepts `0x10`, `1_0`) | `AuditLimitRule`, `AC1_AC2`, `AC3_AC6`, `AC8` |
| M4b | C-3: trim spaces before parsing | `DefaultLimit…`, `AuditLimitRule`, `AC3_AC6`, `AC8` |
| M5 | C-5: accept 0 | `DefaultLimit…`, `AuditLimitRule`, `AC3_AC6`, `AC8` |
| M6 | C-7: cap above 500 (overflow too) instead of refusing | `ForeignTitles…`, `OverMaximumLimitRefused`, `AuditLimitRule`, `AC3_AC6`, `AC8` |
| M6b | C-7: refuse only overflow, accept 501..2^40 | same five |
| M7 | C-10: fall back to `LIMIT` | `AuditLimitRule`, `AC1_AC2` |
| M8 | fixed message changed | `DefaultLimit…`, `OverMaximum…`, `AC3_AC6`, `AC7` |
| M9 | echo the raw query in the message | `DefaultLimit…`, `OverMaximum…`, `AC3_AC6`, `AC7` |
| M10 | 400 via `http.Error` (text/plain) | `DefaultLimit…`, `OverMaximum…`, `AC3_AC6`, `AC7`, `AC8` |
| M11 | limit rule before the handler's auth guard | `NoUserInContextIsRefused` |
| M12 | M11 plus the route moved outside the `requireAuth` group | `NoUserInContextIsRefused`, `AC7` |
| M13 | handler references `gen.ListAuditEntries400JSONResponse` | `AC12` |

The e2e changes were not mutated locally (see §6).

## 5. Gates run (commands, results)

The host has no `make`, so the targets' underlying commands were run directly
from the worktree, Git Bash on Windows, with Go 1.26.0 (auto toolchain):

- `cd backend && go build ./... && go vet ./...` → ok.
- `golangci-lint run --config ../.golangci.yml ./...` in `backend/` (v1.64.8
  built with go1.26.0) → exit 0.
- sfguard, as the `sfguard` target does:
  - `cd tools/sfguard && go vet ./... && go test ./...` → ok;
  - `bash tools/sfguard/sfguard.test.sh` → 24/24 passed;
  - `sfguard -root . -baseline tools/sfguard/baseline.txt -base-ref origin/main backend mcp`
    → "baseline does not grow relative to origin/main", "19 occurrence(s), all
    baselined; nothing new, nothing stale".
- openapi checks:
  - `python scripts/openapi_assemble.py --check` → "OK: bundle is in sync (112 paths)";
  - `python scripts/openapi_migration_report.py` (re-render after the notes-cell
    edit), then `--check` → "OK: MIGRATION.md covers all 134 operations (14 generated)";
  - `bash scripts/openapi_gen_go.sh --check` → "OK: backend/api/gen matches the contract";
  - `sha256sum contracts/openapi/openapi.yaml` → `45d87ca3…286b87`, the
    manifest's hash.
- Full backend suite with coverage, on a throwaway `postgres:16.15-alpine`
  (port 15493):
  `PACEDAY_TEST_DB_PORT=15493 PACEDAY_GO_TEST_FLAGS="-coverprofile=coverage.out -covermode=atomic" bash scripts/test-backend.sh`
  → see the PR body for the result and the `scripts/coverage-gate.sh` total.
- e2e typecheck: `cd e2e && npm ci && npm run typecheck` → exit 0.
- e2e run: see the PR body.

## 6. Not run here

- `openapi-check-web` (cross-repo): it needs the paired web branch's
  regeneration, which is that PR's job.
- `golangci-lint` over `mcp/`, `tools/sfguard` and `tools/denyproxy`: none of them
  is changed by this PR. CI runs them.
- `make verify` as one command (no `make` on the host). CI's `verify` job is the
  authority.

## 7. Deviations

- **D-1: `MIGRATION.md` and `docs/factory/codebase-notes.md` are touched in stage 4.**
  The manifest forbids `contracts/openapi/**` "except through a contract
  revision". The MIGRATION.md edit is the hand-kept notes cell only, the
  challenger's note 1, made after approval and without a revision. No fragment,
  bundle or generated file changes, and the hash is unchanged. Both paths were
  added to `allowedPaths` with a dated note.
- **D-2: AC-11 drives the generated wrapper through `gen.HandlerWithOptions`**
  with `gen.Unimplemented{}` and the default error handler. It asserts only
  that the binder answers 400. Content type and body are the wrapper's
  text/plain, which is residual difference (1) of the contract.
- **D-3: AC-12 is a Go test, not the shell grep.** It walks `backend/` with the
  same exclusions as the contract's command (`api/gen`, `_test.go`), so it runs
  in CI without a shell. The shell command itself was also run: no output,
  exit 1.
- The storage clamp is unchanged (spec §5 left the choice to the plan).

## 8. Rollback

Revert the api PR, then the paired web PR within the grace period (spec §7).
There is no migration and no data. Reverting restores PAC-23's silent
resolution and contract text.
