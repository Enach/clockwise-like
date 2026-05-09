# ADR 0002: Frontend lives only in `smart-calendar-flow`

- Status: accepted
- Date: 2026-05-09
- Project: Paceday

## Context

`Enach/clockwise-like` historically had a `frontend/` directory and `Enach/smart-calendar-flow` is a Lovable-generated repo for the same UI. The two had drifted significantly — `clockwise-like/frontend` was missing the entire auth flow (`AuthProvider`, `RequireAuth`, sign-in/sign-up/callback), the `Team`/`Links`/`PublicBooking` pages, and four API modules. The CI workflow `sync-and-publish.yml` does clone `smart-calendar-flow` at run-time for the frontend pipeline, which made the in-repo `frontend/` folder unused but still misleading — it could be edited and the changes would silently not ship.

## Decision

The frontend source-of-truth is **`Enach/smart-calendar-flow`**. The `frontend/` directory in this repo is reduced to a single `README.md` pointer.

Rules:
- No frontend source code is committed here. CI clones from the standalone repo at run-time.
- Lovable's GitHub integration targets `smart-calendar-flow` only.
- Frontend Dependabot config + auto-merge live in `smart-calendar-flow`.
- Backend-side TypeScript types that the frontend consumes are generated and published by the backend; the frontend depends on them, never vice-versa.

## Consequences

- Positive: no more drift. One repo per role.
- Positive: Lovable handoff (Option A in the agent team plan) lands cleanly in `smart-calendar-flow`; `staff-engineer` integration work happens there.
- Negative: cross-repo refactors (e.g., breaking API change) need coordinated PRs. Mitigated by the agent team's `team-orchestrator` skill spawning sessions per repo.
- Negative: developers cloning only `clockwise-like` no longer have a working frontend locally. Mitigated by the README pointer + `docker-compose` clone instructions (TODO).

## Alternatives considered

- Keep `frontend/` in `clockwise-like` and delete `smart-calendar-flow` — rejected because Lovable's integration targets the standalone repo.
- Git submodule — rejected: heavy ergonomics, breaks `gh` CLI and most editors' "open repo" flows.
- Mirror via CI on every push — rejected: still a source-of-truth question, just hidden.
