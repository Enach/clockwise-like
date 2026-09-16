# Factory agents

These are the agent definitions for the stages in `docs/factory/README.md`. That
document is the spec; these are the implementation. Read it first.

| Stage | Agent | Gate it owns |
|---|---|---|
| 0 Intake | `triage` | the issue states a problem, not a solution |
| 1 Spec | `spec-author` → `spec-challenger` | every acceptance criterion is falsifiable |
| 2 Contract | `contract-author` → `contract-challenger` | `make openapi-check`; no unresolved `x-uncertain` on touched operations |
| 3 Plan | `plan-author` | tests named before code; migration has a rehearsed down |
| 4 Implement | `backend-implementer` → `frontend-implementer` | `make verify` green, output in the PR |
| 5 Verify | `e2e-author` | `make e2e` green against the real stack |
| 6 Review | `reviewer` | findings resolved or explicitly accepted |
| 7 Release | `release-manager` | verified backup, rehearsed migration, smoke tests |

## The two invariants

**Author ≠ challenger.** An agent that writes an artifact and then reviews it
produces a confident, wrong artifact. Each challenger runs as a separate agent
with the upstream artifact but *not* the author's reasoning — if an artifact only
makes sense with a verbal gloss, that is a defect in the artifact.

**Backend before frontend.** The contract merges, types regenerate, then the
frontend implements. Never the reverse, never simultaneously. This is what stops
the two repos drifting, which is the failure the factory exists to prevent.

## Invoking them

Each stage is one agent, run to completion, ending in a Linear comment and an
artifact. The next stage reads the artifact, not the conversation. That is
deliberate: an artifact that cannot carry the work forward on its own has not
finished the stage.

Stages 1–3 are cheap and are where errors should be caught. Stage 4 is where
errors get expensive. If you find yourself wanting to skip ahead to stage 4
because the change is "obvious", note that most of the 92 findings in
`docs/factory/api-audit.md` were introduced by changes that were obvious.

## A note on what these definitions can and cannot do

These are instructions, and instructions are norms rather than enforcement. The
real enforcement is `make verify`, because it is code. When a gate here matters
enough that it must not be bypassable, the right move is to make it a target in
that Makefile rather than a stronger sentence in one of these files.
