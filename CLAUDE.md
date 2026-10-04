# Paceday — Claude Code guidance

## Test coverage requirement

**Target: 75–80% test coverage for `backend/` and `mcp/`. Enforced floors today: backend 38%, mcp 75%.**

The floors in the `Makefile` are what the suites actually hold, measured, not the
target. `mcp` is at 84.0% and clears its floor. The backend is at 38.0% once
generated code is excluded (19.6% with it), so its floor is 38% and the gap to the
target is real work rather than a number nobody meets. Generated files
(`backend/api/gen/**`) are excluded from the measurement by
`scripts/coverage-gate.sh`.

Both floors are **ratchets**: raise one when coverage rises, never lower one to turn
a red run green.

### Rules

1. Every new file that contains business logic must have a corresponding `_test.go` file.
2. Before marking any backend or MCP task as done, run the coverage check and confirm the total is at or above the enforced floor, and that your change moved it toward the 75–80% target rather than away. The **full suite** (including integration tests via testcontainers) must be used for the official number — unit-only runs will show lower figures (~45–55%) because storage and auth integration paths are excluded:
   ```bash
   # Backend — full suite (requires Docker for testcontainers)
   cd backend && go test ./... -coverprofile=coverage.out && go tool cover -func=coverage.out | tail -1

   # Backend — unit tests only (fast, no Docker; expect ~45–55%)
   cd backend && go test -short ./engine/... ./auth/... ./domain/... -coverprofile=coverage.out && go tool cover -func=coverage.out | tail -1

   # MCP
   cd mcp && GONOSUMDB='*' GOFLAGS='-mod=mod' go test ./... -coverprofile=coverage.out && go tool cover -func=coverage.out | tail -1
   ```
3. All tests must pass (`go test ./...` exits 0) before committing.
4. If coverage falls below the enforced floor, add unit tests for the lowest-covered packages before committing. Focus on pure logic functions (algorithms, helpers, transformers) — they are cheapest to test and yield the most coverage gain.
5. Do not inflate coverage with trivial tests (testing struct field assignment, `fmt.Sprintf`, etc.). Tests must verify real behavior.
6. Integration tests that require a running database (testcontainers) count toward coverage but must not be the only tests — fast unit tests are required alongside them.

### What counts as business logic (must be tested)

- Engine packages (`engine/`): scheduling algorithms, slot-finding, ICS generation, categorization
- Storage helpers beyond basic CRUD (query logic, data transformations)
- Auth and domain logic (`auth/`, `domain/`)
- API handlers: at least the request-validation and error paths via `httptest`

### What does not require tests

- Pure DB scan/query boilerplate with no branching logic
- `main.go` wiring
- Migration SQL files

## Commit checklist (backend/mcp changes)

Before every commit touching `backend/` or `mcp/`:
- [ ] `go build ./...` passes
- [ ] `golangci-lint run` passes (zero warnings)
- [ ] `go test ./...` passes
- [ ] Total coverage is at or above the floor in the `Makefile` (check with `make coverage`, which excludes generated files — a bare `go tool cover -func` does not)

## Pre-commit hook

The `.githooks/pre-commit` hook enforces build + lint automatically. Coverage is **not** enforced by the hook (testcontainers are slow), but must be verified manually before pushing.
