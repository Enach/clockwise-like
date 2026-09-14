# Implementation Plan — PAC-14: Add Sentry to Go backend and React frontend

- **Linear parent**: [PAC-14](https://linear.app/paceday/issue/PAC-14/add-sentry-to-go-backend-and-react-frontend) (In Progress)
- **Plan sub-issue**: PAC-16 `[plan]` (this stage)
- **Approved contract**: `contracts/features/PAC-14.md` (contract PR #169, merged to `main` of `Enach/clockwise-like`)
- **Stage**: plan-author -> plan-reviewer
- **Streams**: backend `Enach/clockwise-like` (Go) **first**, then frontend `Enach/smart-calendar-flow` (React/Vite/bun) — a real cross-repo boundary.

This plan is documentation only. It derives entirely from the approved contract
and adds no scope. The contract's "Allowed paths" (§6) is the hard boundary; this
plan may not widen it.

---

## 1. Repository boundaries

Two repositories are touched. They are independent codebases with separate CI,
separate dependency managers, and separate Sentry projects.

### 1a. `Enach/clockwise-like` — Go backend (PRIMARY, first)
- **Why**: hosts `backend/main.go` (the single init point), the chi HTTP router in
  `backend/api/**`, and the cron wrappers. This is the source-of-truth repo and
  also carries the merged contract.
- **Touched (from contract §6, allowed only)**:
  - `backend/main.go` — `sentry.Init` + `defer sentry.Flush(2*time.Second)` + cron `CaptureException`.
  - `backend/api/**` — recover/report middleware wrapping the chi router; behavior
    unchanged (panic still becomes `500`).
  - `backend/go.mod`, `backend/go.sum` — add `github.com/getsentry/sentry-go`.
  - `.env.example` — add `SENTRY_DSN`, `SENTRY_ENVIRONMENT`, `SENTRY_RELEASE`.
- **Forbidden (contract §6)**: anything under `backend/storage/migrations/`, any
  domain schema, any `frontend/` source (lives in the other repo), any
  secret/credential file.

### 1b. `Enach/smart-calendar-flow` — React frontend (SECOND, after backend PR merges)
- **Why**: the SPA entrypoint and React tree live here per
  `docs/adr/0002-frontend-source-of-truth.md`. Committing frontend source into
  `clockwise-like` is explicitly banned.
- **Touched (contract §6, allowed only)**:
  - app entrypoint (`src/main.tsx` / `src/index.tsx`) — `Sentry.init` before mount + Sentry error boundary around the root.
  - `package.json` + lockfile — add `@sentry/react`.
  - `.env` example entry — add `VITE_SENTRY_DSN`.
- **Forbidden**: committing any frontend source into `clockwise-like`.

### Boundary rule
The two streams share **no code**. They share only the contract's PII/DSN policy
(§2, §4): PII off by default, DSN read from env with the documented default,
`BeforeSend` scrubber on backend. There is no runtime coupling between them, so
they can ship independently — the only ordering constraint is process discipline
(backend first, see §4).

---

## 2. Migration order

**No database migrations.** Contract §2 states Sentry is an observability sidecar:
no schema change, `backend/storage/migrations/` is untouched, no query paths
change. The migration count for this feature is **zero** in both repos.

The only ordering that matters is **init order inside `main()`**, which is a
runtime invariant, not a DB migration:

1. `sentry.Init(...)` runs **first** in `main()`.
2. `defer sentry.Flush(2 * time.Second)` is registered immediately after.
3. Only then: `DATABASE_URL` read / `storage.Open` / cron start.

Rationale (contract §2): init must precede any DB open or cron start so a panic
during startup is still captured, and a bad/empty DSN degrades to "monitoring off"
without making boot fatal (unlike `DATABASE_URL`, which stays fatal by design).

---

## 3. Test sequence

Gate conditions come from contract §8. Run in this order; a later tier only runs
when the earlier tier is green.

### Backend (`clockwise-like`) — unit -> integration -> vertical
1. **Unit**
   - New test: `BeforeSend` scrubber removes `Authorization`, `Cookie`, and any
     `*token*` / `*secret*` fields from the event (contract §2 PII rule, §5 PII
     failure path). This is the load-bearing test — it proves OAuth tokens
     (`GOOGLE_CLIENT_SECRET`, calendar tokens) never reach Sentry.
   - `go vet ./...` clean.
2. **Integration**
   - Boot-without-DSN smoke: server boots and serves with `SENTRY_DSN=""`
     (init non-fatal, contract §5 failure path).
   - Middleware: a crafted request that panics a handler in `backend/api` still
     returns `500` (client contract unchanged, §3/§5 happy path). Reporting to a
     live Sentry project is manual/observational, not a CI assertion.
3. **Build + lint gates (release gates §8)**
   - `go build ./...` in `backend/`.
   - `golangci-lint run` clean (repo has `.golangci.yml`).
   - `go test ./...` passes (includes the scrubber unit test).
4. **Vertical (cross-boundary, run by vertical-verifier)**
   - Backend panic -> Sentry event + `500` -> `SIGTERM` -> `Flush` delivers within 2s.

### Frontend (`smart-calendar-flow`) — after backend PR merges
1. **Build gate (§8)**: `bun run build` succeeds with `@sentry/react` added.
2. **Behavioral (observational)**: a thrown component is caught by the Sentry error
   boundary, fallback UI shows (no white screen), event reaches the frontend
   Sentry project (§5 frontend happy path).
3. **Failure path**: empty/malformed `VITE_SENTRY_DSN` -> init no-ops, app renders
   normally (§5).
4. **Zod (only if a config object is validated)**: `sentryConfigSchema` with
   `dsn: z.string().url()`, `environment: z.string()`,
   `enabled: z.boolean().default(true)` (§4). No new domain schema otherwise.

### No-secret-leakage gate (§8, both repos)
Grep each diff to confirm no OAuth token/secret is passed to Sentry and DSNs are
read from env with the documented default (not hard-forced).

---

## 4. Branch / PR strategy

Branches follow the pipeline convention: strip the `<user>/` prefix from Linear's
issue `branchName` to get the slug
`pac-14-add-sentry-to-go-backend-and-react-frontend`, prefix by stage.

### This (plan) stage — documentation only
- Repo: `Enach/clockwise-like`.
- Branch: `impl/pac-14-add-sentry-to-go-backend-and-react-frontend-plan` off `main`.
- Adds only `contracts/features/PAC-14-plan.md`.
- PR title: `[PAC-14] plan: Add Sentry to Go backend and React frontend`.
- PR body: `Fixes PAC-16` (closes the plan sub-issue on merge) + `Part of PAC-14`
  (links to parent, does NOT close it). **Never `Fixes PAC-14`.**

### Implementation stages (after this plan is human-approved)
Two implementation streams, each in its own repo, each on its own sub-issue and PR.

1. **Backend FIRST** (`Enach/clockwise-like`)
   - Sub-issue: `[backend] <title>` under PAC-14.
   - Branch: `impl/pac-14-add-sentry-to-go-backend-and-react-frontend-backend` off `main`.
   - PR title: `[PAC-14] backend: Add Sentry to Go backend`.
   - PR body: `Fixes <backend-sub-id>` + `Part of PAC-14`.
2. **Frontend SECOND** (`Enach/smart-calendar-flow`), blocked-by backend
   - Sub-issue: `[frontend] <title>` under PAC-14, `blocked-by` the backend sub-issue.
   - Branch: `impl/pac-14-add-sentry-to-go-backend-and-react-frontend-frontend` off that repo's default branch.
   - PR title: `[PAC-14] frontend: Add Sentry to React frontend`.
   - PR body: `Fixes <frontend-sub-id>` + `Part of PAC-14`.

### Merge order & rationale (backend-first)
Backend merges before frontend is started. Two reasons: (1) the backend carries
the shared PII/DSN policy and the canonical init pattern the frontend mirrors, so
approving it first locks the policy; (2) it matches the pipeline's
`impl/<slug>-backend` merged -> dispatch frontend rule. The frontend has no code
dependency on a running backend, but the pipeline sequences it after the backend
PR merges to keep one reviewable stream in flight at a time.

### Sub-issue / linking discipline
The PARENT PAC-14 is never closed by a stage PR. Each stage PR uses
`Fixes <its-own-sub-id>` + `Part of PAC-14`. The parent goes Done only as a
roll-up after every child is Done.

---

## 5. Runtime implications

- **Feature flag = the DSN env var.** There is no separate flag. Setting
  `SENTRY_DSN` (backend) / `VITE_SENTRY_DSN` (frontend) enables monitoring;
  unsetting it degrades init to "monitoring disabled" (§2, §5). This is also the
  runtime kill switch (§7) — no code deploy needed to turn Sentry off.
- **Backward compatibility**: fully backward compatible. No API, payload, or
  status-code change (§3). Existing clients see identical behavior. A panic still
  becomes a `500`; crons still log via `log.Printf` and continue — Sentry capture
  is additive.
- **Fail-open on init**: a bad/empty DSN never blocks boot or mount (§2, §5). The
  server serves and the SPA renders regardless of Sentry health.
- **Environments**: `SENTRY_ENVIRONMENT` / `import.meta.env.MODE` separate
  dev/staging/prod; `SENTRY_RELEASE` is set by CI (empty locally) for release
  attribution. Backend and frontend point at **separate Sentry projects** (distinct
  DSNs in contract §2 vs §4).
- **PII / data governance**: `SendDefaultPII` / `sendDefaultPii` default `false`;
  `BeforeSend` scrubber (backend) drops `Authorization`, `Cookie`,
  `*token*`/`*secret*`. No request bodies or auth headers attached.
- **Performance**: `TracesSampleRate: 0.0` (errors only for v1) — negligible
  overhead; no tracing spans emitted. `Flush(2*time.Second)` bounds shutdown delay
  to at most 2s.
- **No compatibility window / no phased rollout needed**: because the change is
  additive and env-gated, there is no dual-write or migration window to manage.

---

## 6. Rollback procedure

Mirrors contract §7. Two independent levers.

### Fast (no deploy) — runtime kill switch
1. Unset `SENTRY_DSN` (backend) and/or `VITE_SENTRY_DSN` (frontend) in the target
   environment.
2. Restart the affected process. Init degrades to "monitoring disabled"; app
   behavior is otherwise identical. This turns Sentry off in seconds without
   touching code and is the first response to any Sentry-related incident.

### Full — revert the code
1. `git revert` the **backend** implementation PR in `Enach/clockwise-like`. The
   change is additive and isolated (one init block, one middleware, one dependency
   in `go.mod`/`go.sum`), so the revert removes Sentry cleanly. No data migration
   to reverse (zero migrations).
2. `git revert` the **frontend** implementation PR in `Enach/smart-calendar-flow`
   (init + error boundary + `@sentry/react` dependency). Same additive/isolated
   property.
3. The approved **contract revision stays immutable** (matching PAC-12/PAC-13
   convention). Only the implementation PRs are reverted; the contract and this
   plan remain as the record.
4. Reverts are independent per repo and can be done in either order, since the two
   streams share no code.

### Post-rollback verification
- Backend: `go build ./...` + `go test ./...` green on the reverted tree; grep
  confirms no residual `sentry` import.
- Frontend: `bun run build` green; `@sentry/react` removed from `package.json`/lockfile.
