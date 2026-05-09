# Agent Team — Paceday (api)

This repo is bootstrapped and maintained by the Craft Agent team.

- Project: **Paceday**
- Role of this repo: **api**
- Anytype Space: **Paceday**
- Skills index: see workspace `AGENT_TEAM.md`

## Conventions

- Branches: `feat/<task-id>-<slug>`, `fix/...`, `chore/...`
- Every code change has `prompts/<task-id>.md`
- ADRs in `docs/adr/`, PRDs in `docs/prd/`, design briefs in `docs/design/briefs/`
- All git ops via SSH: `git@github.com:Enach/clockwise-like.git`
- CI: self-hosted runners, parallel; coverage gate ≥ 70%; SAST + DAST required
- Deployment target: Docker Compose

## Auto-merge gates

- CI green
- Coverage ≥ 70%
- SAST clean
- DAST baseline clean
- ≥ 1 agent review
- No CODEOWNERS block
