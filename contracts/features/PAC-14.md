# Contract — PAC-14: Add Sentry to Go backend and React frontend

- **Linear issue**: [PAC-14](https://linear.app/paceday/issue/PAC-14/add-sentry-to-go-backend-and-react-frontend)
- **Supersedes**: PAC-12 (Canceled). PAC-12's prior contract PR
  ([clockwise-like#165](https://github.com/Enach/clockwise-like/pull/165), merged)
  produced only a stub manifest `contracts/features/PAC-12.yaml` with no concrete
  DSNs or init points. This contract replaces it with the concrete backend/frontend
  DSNs and wiring supplied on PAC-14.
- **Stage**: contract-author -> contract-challenger
- **Source of truth repo**: `Enach/clockwise-like` (Go backend + monorepo CI)
- **Cross-repo note**: the React frontend lives in a **separate repo**
  `Enach/smart-calendar-flow` (see `frontend/README.md` and
  `docs/adr/0002-frontend-source-of-truth.md`). Frontend Sentry changes land there;
  this contract governs both streams.

---

## 1. Problem statement

The Paceday backend (Go) and frontend (React) have **no error/exception
monitoring**. When a request panics, a goroutine crashes, or the SPA throws an
unhandled exception, the failure is only visible in local `log.Printf`/console
output — there is no aggregation, alerting, release attribution, or stack-trace
capture in dev/staging. Grep for `sentry`/`SENTRY_DSN` across the repo returns
nothing today, confirming the gap.

Examples of currently-invisible failures:
- A panic in an HTTP handler (`backend/api/*`) -> 500 to the client, nothing captured.
- A background cron failure (`scheduler`, `engine.PersonalBlocker`,
  `engine.AutoDeclineService`) -> a log line that no one is watching.
- A frontend render/effect exception -> white screen, no report.

**Severity**: Medium. No data-loss or security regression, but production/staging
incidents are currently undiagnosable without reproduction.

**Affected paths**:
- Backend: `backend/main.go` (init), HTTP middleware in `backend/api/`, cron
  wrappers in `backend/main.go` / `backend/scheduler` / `backend/engine`.
- Frontend (separate repo `Enach/smart-calendar-flow`): app entrypoint
  (`src/main.tsx` or equivalent) + a React error boundary.

---

## 2. DB / backend rules

- **No schema change.** Sentry is an observability sidecar; it touches no tables,
  no migrations, no query paths. `backend/storage/migrations/` is untouched.
- **Invariant**: `sentry.Init` MUST run **before** any DB open or cron start in
  `main()`, and a `defer sentry.Flush(2 * time.Second)` MUST be registered so
  buffered events are delivered on shutdown. Init failure MUST NOT be fatal — a
  bad/empty DSN degrades to "monitoring off", the server still boots (unlike
  `DATABASE_URL`, which is fatal by design).
- **DSN handling**: the DSN is read from env `SENTRY_DSN` (backend) with the
  PAC-14 value as the documented default; it is **not** a secret in the classic
  sense (Sentry ingest DSNs are client-side identifiers) but MUST still be
  configurable per environment via env, not hard-forced in code, so staging and
  prod can point at different projects. Add `SENTRY_DSN` and
  `SENTRY_ENVIRONMENT` to `.env.example`.
- **PII rule**: `SendDefaultPII` MUST default to `false`. Request bodies and auth
  headers MUST NOT be attached to events. OAuth tokens
  (`GOOGLE_CLIENT_SECRET`, calendar tokens) MUST never reach Sentry — add a
  `BeforeSend` scrubber that drops `Authorization`, `Cookie`, and any
  `*token*`/`*secret*` fields.

Backend init point (canonical, from `backend/main.go`):

```go
import "github.com/getsentry/sentry-go"

func main() {
    if err := sentry.Init(sentry.ClientOptions{
        Dsn:              cmp.Or(os.Getenv("SENTRY_DSN"),
            "https://4383608feea22d8fe1b1bb0c4a922ab1@o4512081160896512.ingest.de.sentry.io/4512083547062352"),
        Environment:      cmp.Or(os.Getenv("SENTRY_ENVIRONMENT"), "development"),
        Release:          os.Getenv("SENTRY_RELEASE"),   // set by CI; empty locally
        SendDefaultPII:   false,
        TracesSampleRate: 0.0,                            // errors only for v1
        BeforeSend:       scrubSensitive,                 // drops auth/cookie/token/secret
    }); err != nil {
        log.Printf("sentry init failed, monitoring disabled: %v", err)
    }
    defer sentry.Flush(2 * time.Second)
    // ... existing DATABASE_URL / storage.Open / crons follow ...
}
```

---

## 3. API contract

- **No new endpoints, no payload changes, no new error codes.** Existing routes,
  request/response shapes, and status codes are unchanged.
- **New behavior only**: an HTTP middleware (Sentry's `sentryhttp` handler, or an
  equivalent recover-and-report wrapper) wraps the chi router in `backend/api` so
  a panic is (a) reported to Sentry and (b) still converted to a `500` exactly as
  today. The client-facing contract does not change.
- **Cron paths**: each background job (`focusCron`, `personalCron`,
  `autoDeclineCron`) reports its error to Sentry via `sentry.CaptureException`
  **in addition to** the existing `log.Printf`, then continues — no behavior
  change to scheduling.

---

## 4. Frontend business rules

Target repo: `Enach/smart-calendar-flow` (React, bun).

- Install `@sentry/react`. Initialize once at the app entrypoint, **before** the
  React tree mounts:

```ts
import * as Sentry from "@sentry/react";

Sentry.init({
  dsn: import.meta.env.VITE_SENTRY_DSN ??
    "https://8141ab6e48ded81c6d47f8f074c1a648@o4512081160896512.ingest.de.sentry.io/4512083551060048",
  environment: import.meta.env.MODE,
  // dataCollection: PII off by default per contract 2
  sendDefaultPii: false,
});
```

- **Zod / state**: no new domain schemas are introduced. If a Sentry-related
  config object is validated, it MUST use a Zod schema `sentryConfigSchema` with
  `dsn: z.string().url()`, `environment: z.string()`,
  `enabled: z.boolean().default(true)`.
- **Allowed state transitions** for monitoring status: `uninitialized -> active`
  (init succeeds) and `uninitialized -> disabled` (no DSN / init throws). No other
  transitions. Once `active`, it stays `active` for the session; the app MUST NOT
  re-init.
- Wrap the root component in a Sentry **error boundary** so a render exception is
  captured and a fallback UI is shown instead of a white screen.
- PII: `sendDefaultPii: false`; do not attach user email/tokens to events.

---

## 5. Vertical scenario

**Happy path (backend)**: server boots -> `sentry.Init` succeeds -> a handler in
`backend/api` panics on a crafted request -> middleware reports the exception to
Sentry (visible in the Sentry project) AND returns `500` to the client -> on
`SIGTERM`, `sentry.Flush` delivers buffered events within 2s.

**Happy path (frontend)**: SPA loads -> `Sentry.init` runs before mount -> a
component throws -> the Sentry error boundary catches it, shows the fallback UI,
and the exception appears in the frontend Sentry project.

**Failure path**: `SENTRY_DSN` is empty/malformed -> backend logs
`sentry init failed, monitoring disabled` and **still boots and serves**;
frontend init no-ops and the app renders normally. No crash, no 500 loop, no
blocked mount.

**PII failure path (must be prevented)**: a handler panics while processing a
request that carried an `Authorization` header -> the event reported to Sentry
MUST NOT contain the header value or any OAuth token (verified by `BeforeSend`
scrubber test).

---

## 6. Allowed paths

Explicit enumeration of what each stream may modify.

**Backend (`Enach/clockwise-like`)** — allowed:
- `backend/main.go` (Sentry init + flush + cron capture)
- `backend/api/**` (recover/report middleware only)
- `backend/go.mod`, `backend/go.sum` (add `github.com/getsentry/sentry-go`)
- `.env.example` (add `SENTRY_DSN`, `SENTRY_ENVIRONMENT`, `SENTRY_RELEASE`)
- `contracts/features/PAC-14.*` (this contract)

**Backend — forbidden**: any file under `backend/storage/migrations/`, any domain
schema, any `frontend/` source (separate repo), any secret/credential file.

**Frontend (`Enach/smart-calendar-flow`)** — allowed:
- app entrypoint (`src/main.tsx` / `src/index.tsx`) — init + error boundary
- `package.json` / lockfile — add `@sentry/react`
- an `.env` example entry `VITE_SENTRY_DSN`

**Frontend — forbidden**: committing frontend source into `Enach/clockwise-like`
(explicitly banned by `frontend/README.md`).

---

## 7. Rollback plan

1. **Revert the implementation PRs** (backend and frontend) — the changes are
   additive and isolated (init block, one middleware, one dependency), so a
   `git revert` of each impl PR fully removes Sentry with no data migration.
2. The approved contract revision stays immutable (matching PAC-12/PAC-13
   convention: "Revert the implementation PRs and keep the approved contract
   revision immutable").
3. **Runtime kill switch (no deploy needed)**: unset `SENTRY_DSN` (backend) /
   `VITE_SENTRY_DSN` (frontend) -> init degrades to "monitoring disabled" on next
   start, so monitoring can be turned off without reverting code.

---

## 8. Release gates

CI checks that MUST pass before implementation is approved / deployed:

- **Backend build**: `go build ./...` in `backend/` succeeds.
- **Backend lint**: `golangci-lint run` clean (repo has `.golangci.yml`).
- **Backend vet/test**: `go vet ./...` and `go test ./...` pass, including a new
  unit test asserting the `BeforeSend` scrubber removes `Authorization`,
  `Cookie`, and `*token*`/`*secret*` fields.
- **Backend boot-without-DSN**: a test/smoke check that the server boots with
  `SENTRY_DSN=""` (init non-fatal).
- **Frontend build**: `bun run build` succeeds in `Enach/smart-calendar-flow`
  with `@sentry/react` added.
- **No secret leakage**: grep the diff to confirm no OAuth token/secret is passed
  to Sentry, and DSNs are read from env with the documented default (not
  hard-forced).
- **Contract link**: the contract PR body contains `Fixes PAC-14` so Linear links
  and auto-transitions on merge.
