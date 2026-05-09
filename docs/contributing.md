# Contributing — Paceday

## Branching

- `main` is protected; merges via squash only.
- Feature branches: `feat/<task-id>-<slug>`.
- Fix: `fix/<task-id>-<slug>`. Chore: `chore/<task-id>-<slug>`.

## PR requirements

- Title: `[<task-id>] <imperative summary>`.
- Body: links the Anytype Task, summary, test plan, screenshots if UI.
- Coauthor: `Co-Authored-By: Craft Agent <agents-noreply@craft.do>` when an agent wrote the code.
- A `prompts/<task-id>.md` file capturing the originating prompt is mandatory for code changes.

## Required checks (set via `gh api`)

`lint`, `typecheck`, `unit`, `coverage` (≥ 70%), `build`, `integration`, `sast`, `dast`.

## Auto-merge

PRs auto-merge once required checks pass and ≥ 1 review approves. Dependabot patch/minor auto-merge on green; majors open a triage issue.

## Local setup

See [runbook](./runbook.md).
