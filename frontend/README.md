# Frontend

The Paceday frontend lives in **a separate repo**: https://github.com/Enach/smart-calendar-flow

The CI pipeline (`.github/workflows/sync-and-publish.yml`) clones it at run-time
via the `frontend-source` step. Do not commit frontend source to this repo —
it will diverge.

## Working on the frontend

```bash
git clone git@github.com:Enach/smart-calendar-flow.git
cd smart-calendar-flow
bun install
bun run dev
```

## Why a separate repo?

The frontend is bootstrapped from Lovable. Keeping it in its own repo means:
- Lovable's GitHub integration syncs only there
- Independent dependency / Dependabot lifecycle
- The monorepo CI stays the source of truth for production builds (clones at run-time)

See `docs/adr/0002-frontend-source-of-truth.md` (added in same PR) for the full rationale.
