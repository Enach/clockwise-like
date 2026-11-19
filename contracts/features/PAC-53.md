# PAC-53 — contract rationale: `GET /api/audit` refuses a `limit` it cannot read

- **Spec**: `docs/specs/PAC-53.md`, revision 1, approved after 2 challenge rounds (`1f88dfc`; status marked at `a0eb854`).
- **Manifest**: `contracts/features/PAC-53.yaml`.
- **Contract revision**: 1. **Base**: clockwise-like `origin/main` `f074b4d`.
- **Operation**: `listAuditEntries` only (`contracts/openapi/paths/calendar.yaml`).
- **Fixed error message**: `limit must be an integer from 1 to 500`.

## 1. What changed in the contract

1. **A `400` is declared** on `listAuditEntries`: `application/json`, the shared
   `ErrorResponse` (`{"error": string}`, exactly one key), and the description pins
   the body to exactly `{"error":"limit must be an integer from 1 to 500"}` for every
   refusal.
2. **The `limit` description** drops PAC-23's two-step "resolution procedure" and
   five-case table, under which every input resolved to some number. It now states
   the spec's rule as C-1 to C-10: C-9 (the raw query as a whole) first, then
   `limit` by its decoded, case-sensitive name. `minimum: 1`, `maximum: 500` and
   `default: 50` keep their values, and are now the whole of what is accepted.
3. **The "WHICH SERVER ANSWERS THIS" paragraph** of the operation description is
   rewritten (spec §3.4, AC-12):
   - It no longer says no generated server code is mounted. That has been false
     since PAC-67.
   - Its falsifying command is scoped to this operation:
     `grep -rn "ListAuditEntries" --include=*.go backend/ | grep -v "^backend/api/gen/" | grep -v "_test\.go:"`.
     It returns nothing at `f074b4d` and on this branch (run, exit 1, no output).
   - It cites the real registration, `backend/api/routes.go:216`, with
     `grep -n '"/api/audit"' backend/api/routes.go` next to it so the citation
     survives line moves.
   - It names the repo's actual migration pattern: hand-parse, then
     `gen.NewStrictHandlerWithOptions(...).<Op>(w, r, params)`, as in
     `getStepupOptions`. Under that pattern the hand-parsing code must carry every
     C-row.
   - It records the three residual differences of `ServerInterfaceWrapper` (spec
     §3.3): a text/plain default `ErrorHandlerFunc`, C-9 not visible to the binder,
     and binding before `HandlerMiddlewares`.
4. **Generated Go**: `ListAuditEntries400JSONResponse` is added, and doc comments
   change. Nothing else in `backend/api/gen` changes.
5. **Register and history**:
   - The `notes` cell of `listAuditEntries` in `MIGRATION.md` is rewritten. Moving
     the row is no longer a contract change for this reason.
   - `docs/specs/PAC-23.md` AC-11 gets a "Superseded by PAC-53" pointer.
   - The `declaredResponses` of `contracts/features/PAC-23.yaml` gets a
     "SUPERSEDED BY PAC-53" prefix. PAC-23's own text is kept as history.

## 2. Why this shape

- **One rule instead of a table.** PAC-23 needed three revisions to describe silent
  substitution, and each revision missed a case. "Absent, or once, as an integer
  from 1 to 500" can be checked by reading it.
- **JSON, not text/plain** (spec D-2). Every other refusal this handler and its
  siblings (`teams/{id}/availability`, `book/{slug}/slots`) produce uses
  `writeError` JSON, and the web error path already reads it. The wrapper's
  text/plain default comes from codegen and is not a product decision, so the
  contract records it as a residual difference instead of adopting it.
- **One fixed message** (OQ-4, AC-3). The input is attacker-controlled text and must
  not be reflected. A caller gains nothing from telling "empty" apart from
  "overflow". The message names the parameter and the accepted range, which is the
  whole of what the caller needs to fix the request. The range in the message is
  the same `minimum`/`maximum` as the schema, so the two are changed together.
- **The 400 uses the shared `ErrorResponse`.** It does not get an operation-specific
  schema. See rejected alternative B.
- **C-9 refuses the whole request** (OQ-2). The server cannot tell whether an
  undecodable pair was `limit` (`%6Cimit` decodes to `limit`). Ignoring the pair is
  exactly the silent substitution this ticket removes. Only hand-built URLs can
  produce such a query. The rule applies to this operation only.
- **Above 500 is refused, not capped** (D-5, OQ-1 not exercised). This removes the
  seam where `9223372036854775807` would cap while `9223372036854775808` must 400.
- **Empty is refused** (OQ-3), in agreement with the generated binder. The siblings'
  "empty = absent" is follow-up F-2.

## 3. Alternatives rejected

- **A. Keep the 200 and document the substitution (ticket option 2).** Rejected by
  the spec (D-1). It keeps a contract that calls silent substitution correct, and it
  leaves migrating to the generated server as a behaviour change hidden in a refactor.
- **B. An inline schema with `error: {const: "limit must be an integer from 1 to 500"}`.**
  This would make the message machine-checked instead of prose. Rejected:
  - It adds an operation-specific schema and a generated type for one string.
  - It departs from every other 400 in this document, all of which reference
    `ErrorResponse` and state their fixed text in the description (e.g.
    `getSsoStepupOptions` in `auth.yaml`).
  - It would put the message into the web's generated types as a literal type that
    no web code reads.

  AC-3 and AC-8 already pin the exact body by test.
- **C. `PlainTextError` for the 400**, matching the generated wrapper's default.
  Rejected per D-2 above.
- **D. A different message per case** (e.g. "limit must not be repeated"). This
  would be more helpful to a hand-writing caller, but it means more strings to keep
  in step across three possible implementations (spec §3.3), and AC-3 requires one
  fixed message.
- **E. Declare `400` through the generated binder by moving the operation to the
  generated server in this change.** Factory §4 says modified endpoints use the
  generated interface. The approved spec scopes the migration out (§5) and chose
  option 1, validation in the hand-written handler. The contract instead makes the
  migration behaviour-neutral by stating which implementation obligations follow.
  This tension with §4 is recorded here rather than settled by the contract author.
  Whether to migrate is a separate ticket.
- **F. Express C-9 or C-8 in the schema.** OpenAPI has no keyword for "the raw query
  decodes" or "this scalar parameter appears at most once". Both remain prose plus
  tests (AC-5, AC-6, AC-11).

## 4. Consumers

| Consumer | Effect |
|---|---|
| smart-calendar-flow `src/api/generated/` (`types.ts`, `schemas.types.d.ts`) | Drift only: a 400 variant and prose. Regenerated in the paired PR. |
| web `getAudit` / `useAudit` (50 from `Audit.tsx`, 10 from `QuickActions.tsx`) | None: only C-6 requests, sent with `URLSearchParams.set`. |
| `backend/api/handlers_audit_test.go`, `e2e/tests/audit.spec.ts` | Pin the superseded behaviour. Rewritten in stage 4 (spec §6), not deleted. |
| MCP | No caller. |

## 5. Paired web PR

`smart-calendar-flow`, branch `factory/pac-53`:
- **Regeneration only** of `src/api/generated/**` against the merged bundle, with
  no hand-written change.
- The api PR merges first. The web PR merges within `PAIR_MISMATCH_GRACE_MINUTES`
  (60).
- Rollback runs in the same order: api first, then web.

## 6. Merge condition

The contract describes the server *after* stage 4. It ships in the same api PR as
the handler change and its tests and must not merge ahead of them (factory §7
rule 2).

## 7. Approval and stage 4

- **Approved**: contract-challenger approved revision 1 at `dff3eb2` (2026-11-19),
  with two non-blocking notes. Stage 4 handles both:
  1. The `listAuditEntries` notes cell in `MIGRATION.md` now opens with "PAC-53:
     parameter and responses modified, kept handwritten (spec §5, contract
     alternative E)."
  2. The stale `limit` bullet in `docs/factory/codebase-notes.md` is corrected.
  Both paths were added to the manifest's `allowedPaths` with a dated note.
- **Stage 4** is recorded in `docs/plans/PAC-53.md`. It makes no fragment, bundle
  or generated-code change, so the `contractHash` is unchanged.
