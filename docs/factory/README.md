# The Paceday software factory

This document is the operating manual for how work gets built here. It is
binding on agents and on humans. If something in this file disagrees with a
prompt you were given, this file wins and you say so.

The factory exists because Paceday is built almost entirely by agents against a
codebase no single person holds in their head. The defence against that is not
more review effort — it is making the *contract* the thing under review, and
making everything downstream of it either generated or verified.

---

## 0. The one-paragraph version

Every change starts as a **spec** (what should be true for a user), becomes a
**contract** (the executable interface), then a **plan**, then **code in two
repos**, and is admitted only when a single command — `make verify` — passes and
its output is in the PR. The OpenAPI document is the source of truth for the
HTTP boundary; the backend is generated from it and the frontend's types are
generated from it, so the two repos cannot drift without the build failing.

---

## 1. Repositories and what each one owns

| Repo | Role | Owns |
|---|---|---|
| `Enach/clockwise-like` | `api` — **source of truth** | Go backend, MCP server, the OpenAPI contract, migrations, e2e suite, docs site, compose/deploy |
| `Enach/smart-calendar-flow` | `web` | React frontend. Lovable-managed. Consumes generated types, never defines them |

The contract lives in `clockwise-like` for both repos. The frontend receives it
as generated, committed artifacts. This is deliberate: Lovable's agent edits the
frontend directly, and a contract it can edit is not a contract.

**Cross-repo ordering is fixed**: backend contract merges → types regenerate →
frontend implements. Never the reverse, never simultaneously. A frontend PR that
needs a field the merged contract does not have is rejected at stage 2, not
patched at stage 4.

---

## 2. Stages

Each stage has an owner, a required artifact, and a gate. A stage may not start
until the previous gate is green. Stages 1–3 are cheap and are where mistakes
should be caught; stage 4 is where mistakes become expensive.

### Stage 0 — Intake

**Owner**: human, or `triage` agent.
**Artifact**: a Linear issue in team `PAC`.
**Gate**: the issue states a user-visible problem, not a solution. "Add Sentry"
is a solution; "production incidents are undiagnosable without reproduction" is
a problem. Solution-shaped issues are allowed but must carry the problem in the
body — the spec stage will ask for it anyway.

### Stage 1 — Spec

**Owner**: `spec-author`. **Challenged by**: `spec-challenger`.
**Artifact**: `docs/specs/PAC-NN.md`.
**Gate**: every acceptance criterion is written as Given/When/Then and is
falsifiable by a test that does not yet exist.

A spec that cannot be turned into a failing test is not a spec, it is a wish.
The spec names the affected users, the observable behaviour change, what stays
the same, and what the rollback looks like. It does **not** name files, packages
or function signatures — that is the plan's job, and putting it here anchors the
implementation before anyone has argued about the interface.

### Stage 2 — Contract

**Owner**: `contract-author`. **Challenged by**: `contract-challenger`.
**Artifacts**:
- edits to `contracts/openapi/paths/<domain>.yaml` (the fragments)
- regenerated `contracts/openapi/openapi.yaml` (via `make openapi`)
- a feature manifest `contracts/features/PAC-NN.yaml`
- a human-readable `contracts/features/PAC-NN.md` explaining *why* the interface
  is shaped this way, including what was rejected

**Gate**: `make contract` passes, meaning:
1. the fragments assemble with no name collisions and no dangling `$ref`
2. the committed bundle matches a fresh assembly (no drift)
3. no operation this feature touches carries an unresolved `x-uncertain`
4. generated code is in sync in both repos

The challenger's job is adversarial and specific. It must try to answer: what
request does this contract permit that the server should reject? What response
does it permit that the client cannot render? Which existing consumer breaks?
"Looks good" is not a review and is rejected.

### Stage 3 — Plan

**Owner**: `plan-author`.
**Artifact**: `docs/factory/PAC-NN-plan.md`.
**Gate**: the plan lists, per repo, the files to be touched, the tests to be
written *before* the code, the migration and its `.down.sql`, the rollback
procedure, and the `x-uncertain` items it resolves. A plan that says "add tests"
without naming them is rejected.

### Stage 4 — Implement

**Owner**: `backend-implementer`, then `frontend-implementer`.
**Artifact**: one PR per repo.
**Gate**: `make verify` green, output pasted into the PR body.

Tests are written before the implementation. This is not a style preference: the
contract already exists, so a test written first is a direct transcription of it,
while a test written afterwards tends to be a transcription of the code.

### Stage 5 — Verify

**Owner**: `e2e-author` for new journeys; otherwise the implementer.
**Artifact**: the e2e scenario covering the spec's acceptance criteria.
**Gate**: `make e2e` green against the full compose stack.

Contract tests prove the boundary is honoured. E2e proves the feature exists.
Both are required; neither substitutes for the other.

### Stage 6 — Review

**Owner**: `reviewer`.
**Gate**: findings are resolved or explicitly accepted in the PR with a reason.

The reviewer reads the spec and the diff, not the plan — reviewing against the
plan only proves the implementer followed instructions, which the tests already
prove.

### Stage 7 — Release

**Owner**: `release-manager`.
**Gate**: migrations applied to a backed-up database, smoke tests green against
the deployed stack, rollback rehearsed at least in writing.

---

## 3. The gate: `make verify`

There is exactly one entrypoint, in both repos, and it is host-agnostic. Today
it runs on whatever machine the agent is on. Tomorrow it runs on a runner on the
home server with zero changes to any of the targets. That portability is the
whole point of having one entrypoint rather than a CI config.

```
make verify        # everything below, in order, fail-fast
├── make lint      # golangci-lint / eslint
├── make openapi-check   # bundle matches fragments; generated code matches bundle
├── make test      # unit + contract tests
├── make coverage  # >= 75% backend, >= 70% frontend
└── make e2e       # Playwright against docker compose
```

**Rules for agents:**

1. You may not open a PR without pasting `make verify` output into the body.
2. You may not claim a check passed that you did not run. "Could not run: Docker
   unavailable" is an acceptable PR note; silence is not.
3. You may not edit a generated file. If a generated file is wrong, the contract
   is wrong — go back to stage 2.
4. You may not add a `//nolint`, `eslint-disable`, `t.Skip` or `test.skip`
   without a comment naming the issue that will remove it.

## 4. What is generated, and from what

| Artifact | Generated from | By | Committed |
|---|---|---|---|
| `contracts/openapi/openapi.yaml` | `contracts/openapi/paths/*.yaml` | `scripts/openapi_assemble.py` | yes |
| `backend/api/gen/*.go` (types + server interfaces) | the bundle | `oapi-codegen` | yes |
| `smart-calendar-flow/src/api/generated/types.ts` | the bundle | `openapi-typescript` | yes |
| `smart-calendar-flow/src/api/generated/schemas.ts` (zod) | the bundle | `typed-openapi --runtime zod` [^1] | yes |
| Swagger UI | the bundle | docs site | no (built) |

Everything generated is committed so that a consumer never needs the generator,
and so that drift is a diff rather than a discovery.

[^1]: This row said `openapi-zod-client` when the factory was written. That tool
    emits a zodios client, which would compete with the hand-written client
    behind `src/api/contract.ts` for the same job; the frontend needs *schemas*
    that sit beside that client, not a second client. `typed-openapi` emits
    standalone zod schemas from the same bundle with no client runtime. The full
    argument, including why `ts-to-zod` was rejected, is in
    `smart-calendar-flow/src/api/generated/README.md`.

### The backend migration to generated code

Existing handlers are hand-written and stay that way until touched. The rule is
**new and modified endpoints use the generated server interface; untouched
endpoints are verified against the contract but not regenerated.** A big-bang
migration of ~119 operations would be a rewrite with no test coverage to catch
what it broke, which is exactly the kind of change this factory exists to
prevent.

Progress is tracked in `contracts/openapi/MIGRATION.md` — every endpoint is
listed as `handwritten` or `generated`, and the count moves in one direction.

## 5. Agents

Each agent is defined in `.claude/agents/`. The definitions are the real
contract; this table is orientation.

| Agent | Stage | Must not |
|---|---|---|
| `spec-author` | 1 | name files, packages or signatures |
| `spec-challenger` | 1 | propose an implementation |
| `contract-author` | 2 | change behaviour not in the spec |
| `contract-challenger` | 2 | approve without naming a specific request/response it tried to break |
| `plan-author` | 3 | write code |
| `backend-implementer` | 4 | touch the frontend repo, or edit the contract |
| `frontend-implementer` | 4 | touch the backend repo, invent an endpoint or field, or edit generated types |
| `e2e-author` | 5 | mock the backend |
| `reviewer` | 6 | approve its own stream's work |
| `release-manager` | 7 | deploy without a database backup |

The separation that matters most is **author ≠ challenger**. An agent that
writes a contract and then reviews it produces a confident, wrong contract. The
challenger runs as a separate agent with the spec but not the author's reasoning.

## 6. Linear topology

One parent issue per feature, one sub-issue per stage, mirroring how PAC-14 ran:

```
PAC-NN            Feature                         (parent, holds the spec link)
├── PAC-NN+1      [spec]      ...
├── PAC-NN+2      [contract]  ...
├── PAC-NN+3      [plan]      ...
├── PAC-NN+4      [backend]   ...
└── PAC-NN+5      [frontend]  ...   blocked by [backend]
```

PR bodies use `Fixes <stage-sub-issue>` and `Part of PAC-NN`. **Never
`Fixes PAC-NN`** on a stage PR — it closes the parent while the feature is half
built, which is how PAC-13 came to be marked Done without the Swagger UI it
demanded ever existing.

## 7. Known debts this factory inherits

The contract was reverse-engineered from a running implementation that had never
been specified. That produced a defect register (`docs/factory/api-audit.md`)
and 22 `x-uncertain` markers. Two standing rules follow:

1. **Touch it, fix it.** A feature that modifies an operation carrying an
   `x-uncertain` or an open audit finding on that operation must resolve it. The
   contract gate enforces this.
2. **The contract describes what is, not what should be.** Where the server does
   something wrong, the contract says so and links the defect. Specifying the
   intended behaviour and leaving the server wrong would make the contract a lie,
   and a lying contract is worse than no contract.
