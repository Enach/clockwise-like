# ADR 0001: Tech stack and existing conventions

- Status: accepted
- Date: 2026-05-09
- Project: Paceday

## Context

Paceday backend is an existing Go monorepo with substantial structure already in place: backend/ (HTTP API), mcp/ (MCP server), frontend/ (placeholder), docs/ (Astro doc site), nginx/ (reverse proxy), docker/ (build assets). Coverage gate is documented in CLAUDE.md at 75-80%, which is stricter than the workspace default of 70% — we keep the stricter gate.

## Decision

Lock in the existing conventions:

- **Languages**: Go (backend, mcp). Astro/JS (docs site). Frontend handled in separate repo `Enach/smart-calendar-flow`.
- **Coverage gate**: 75% minimum, 80% target on `backend/` and `mcp/` (per CLAUDE.md).
- **CI**: GitHub Actions on self-hosted runners (already in place: `.github/workflows/sync-and-publish.yml`).
- **Pre-commit**: `.githooks/pre-commit` enforces build + lint; coverage verified manually pre-push.
- **Lint**: `golangci-lint` (config: `.golangci.yml`).
- **Deployment**: Docker Compose (`docker-compose.yml` for dev, `docker-compose.prod.yml` for prod).
- **Frontend source**: cloned at CI run-time from `Enach/smart-calendar-flow`.
- **Git protocol**: SSH (`git@github.com:Enach/clockwise-like.git`).

## Consequences

- Positive: existing investment is preserved; agent team adopts repo conventions instead of imposing.
- Negative: workspace default coverage template (70%) is overridden here — agents must remember the 75-80% bar.
- Neutral: no immediate workflow changes; agent team adds auto-merge + dependabot-automerge wrappers and Anytype linkage.

## Alternatives considered

- Reset to workspace defaults — rejected (would discard substantial existing CI work).
- Lower the gate to 70% — rejected (existing standards are tighter).
