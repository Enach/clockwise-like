# PAC-81 Part D — operations: plan

Spec: `docs/specs/NEW-deploy-hardening.md` on `factory/deploy-hardening-spec`
(accepted, `beeedc7`), §2 Part D (F-D1..F-D5), §4 AC-D1..AC-D7, §9 D-13/D-14/D-16,
§11. Owner defaults in force (`.claude/factory/owner-decisions.md`, PAC-81):
Q-O1 MCP loopback only and opt-in; Q-O3 daily dump kept 14 days (up to 24 h of
data loss).

## Why this PR can merge before Parts A, B and C

§11 lets D1, D5 and D6 land at any time; D2, D3, D4 and D7 are written "after
C" because they describe settings and behaviour that Parts A, B and C add. This
PR lands first, so every document describes **what is on `main` today**, and
every statement that depends on another part is marked inline with
"added by Part A", "added by Part B" or "added by Part C" (with the AC). No
document claims a behaviour that `main` does not have. When A, B and C merge,
each of them updates the marked passages; the env completeness test (AC-D1)
fails on any variable they add without documenting it, so that half is
enforced, not hoped for.

Concretely, the marked dependencies are:

| Passage | Depends on | Today on `main` |
|---|---|---|
| Readiness check on the backend and through nginx | A (AC-A11), B (AC-B11) | Only `/api/health` (liveness) exists |
| nginx upstream timeout 130 s | B | nginx default (60 s) |
| nginx trusted-proxy list / real client | B (AC-B5..B7) | nginx sends `X-Real-IP $remote_addr` (the outer proxy) |
| nginx bind address setting | C (AC-C9) | `80:80` on all interfaces |
| HSTS / security headers split | B (AC-B10) | nginx sets no security headers |
| CSP Sentry ingest origin setting | B (AC-B4) | not present |
| "Monitoring is off until `SENTRY_DSN` is set" | A (AC-A14) | empty `SENTRY_DSN` falls back to a DSN compiled into the backend (`backend/sentry.go:16`) |
| Backend receives `APP_URL`, `BASE_URL`, `SMTP_*`, `SENTRY_*`, `PACEDAY_ADMIN_EMAILS` in prod | C (AC-C7) | prod compose does not pass them |
| Pinned image versions, rollback by version | C (AC-C2) | images are `:latest` |
| MCP opt-in, loopback | C (AC-C4/C5) | prod compose always runs MCP on `3001` on all interfaces |
| Unready notice + drain during a deploy | A (AC-A6/A7) | no signal handling; in-flight requests are cut |
| Pool / shutdown settings | A | not present |

## AC → deliverable → test

| AC | Deliverable | Test / evidence |
|---|---|---|
| AC-D1 | `.env.example` gets one machine-readable line per variable: `#: NAME \| required\|optional\|test-only\|deprecated \| default: … \| purpose`. Adds every variable F-D1 lists as missing. `SESSION_SECRET` marked deprecated (no service reads it; prod compose passes it until Part C). `TLS_CERT_DIR` marked deprecated (mounted by prod compose, unused by HTTP-only nginx; Part C removes it). `MCP_AUTH_TOKEN` text corrected (SSE refuses to start without it). | `backend/config/env_reference_test.go` (`[unit]`): parses every non-test `.go` file under `backend/`, `mcp/`, `docker/fileserver/` with `go/parser`; collects `os.Getenv`/`os.LookupEnv`/`getenv` reads (literal or `Env*` constant); any other (dynamic) read fails the test unless it is in a named, reviewed function allowlist whose call-site literals are then counted. Checks: every read variable is documented or in the test-only/retired allowlist; every documented or assigned variable is read by a service, or used by a compose file for something other than passing it through, or marked deprecated; every entry has a valid status, default and purpose. Collector and parser have their own fixture tests (positive and negative). Run first against today's `.env.example` to see it fail on F-D1's lists. |
| AC-D2 | `docs/operator/proxy.md` — the six checklist items. | `[review]`; the PR body maps checklist items to headings. |
| AC-D3 | `scripts/ops/backup.sh` (consistent `pg_dump -Fc` from the running Postgres, `.env` copied beside it, both mode 600, 14-day retention), `scripts/ops/restore.sh`, `scripts/ops/rowcounts.sh` (exact count for every table in `public`), runbook backup/restore sections stating that dumps and `.env` are secrets (dumps contain users' provider credentials in plain columns, F-D5). | `scripts/ops/rehearse-backup-restore.sh` (`[integration]`): two throwaway Postgres containers on ports ≥ 15490, the backend binary built from this tree, a seeded user and a session cookie issued **before** the backup; backup, restore onto the second container, a second backend with the saved secrets; asserts health 200, identical row counts for every public table, and the old cookie accepted (`/api/auth/me` 200). Negative controls: the same cookie against a backend with a different `JWT_SECRET` gets 401, and a row inserted into the source after the backup makes the count comparison fail. Run once; date and outcome recorded in the runbook (`[manual-ops]`). |
| AC-D4 | `docs/runbook.md` — deploy order, rollback rules, image-only rollback, expected interruption, PAC-71 recovery link (doc not on `main` yet: link marked pending), docs site published separately. | `[review]` against the six checklist items. |
| AC-D5 | Down files for 006, 007, 014, 015, 016, each starting with a `-- DESTROYS DATA:` comment. 006's down drops `user_id` from `oauth_tokens`, `settings`, `focus_blocks`, `personal_calendars`, the `users` table and the `pgcrypto` extension 006 created. | `backend/storage/migrations_down_test.go`: `[unit]` every `.up.sql` has a `.down.sql`, and the five named downs start with the comment; `[integration]` empty database → latest → down to 005 → schema equals a fresh 005 → up to latest → schema equals the first latest (tables, columns with types/nullability/defaults, constraints, indexes, extensions). Run first without the new files to see it fail. Any later down file that breaks this is fixed here (AC-D5). |
| AC-D6 | `docs/runbook.md` exists; `docs/contributing.md`'s link resolves. | `backend/config/env_reference_test.go`: `TestContributingLinksResolve` resolves every relative link in `docs/contributing.md`. |
| AC-D7 | `.env.example` Sentry block: `SENTRY_RELEASE` should be the deployed image version; monitoring off until `SENTRY_DSN` is set — **marked as Part A behaviour** (AC-A14), with today's fallback stated. Real DSN value removed from `.env.example` (the backend fallback removal is Part A). | `TestEnvReferenceSentryStatements` greps both statements and that `.env.example` carries no Sentry DSN value. Partially delivered: the unconditional statement becomes true only with Part A. |

## Order of work

1. Plan (this file).
2. Failing tests: env reference, contributing link, migrations down. Run, see
   them fail for the stated reason.
3. `.env.example`, down migrations, runbook, proxy doc, ops scripts.
4. Run: env test, migration up→down→up test against Postgres in Docker on port
   15491, `go build`/`go vet`/`go test` for touched packages, the backup/restore
   rehearsal.

## Not done here (recorded)

- `make verify` is not run in this PR's stage (coordinator instruction: no gate
  scripts); the checks that were run are listed in the PR with their exit codes.
- The env test enumerates explicit reads. Variables read implicitly by the Go
  standard library (`HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY`, `TZ`, …) are
  out of its reach and not listed.
- Restoring onto "the same image version" is rehearsed with a binary built from
  the same commit, because no image pipeline exists (F-C1, out of scope).
- Off-host backup storage, encryption of dumps at rest, PITR: operator choices
  named in the runbook (spec §5).

## Review round 1 (request-changes on `d65a5bd`)

- Blocking: the runbook's rollback assumed an empty volume. Added "Rollback on
  the same host" (stop, set the failed volume aside with
  `scripts/ops/set-aside-volume.sh` or the listed commands, `up -d postgres`,
  `restore.sh`, previous version) and rehearsed it in part 2 of
  `rehearse-backup-restore.sh` (dirty database at 32).
- Env test: any `func(string) string` / `func(string) (string, bool)`
  parameter is a getter, whatever its name (two reviewed non-getters:
  `redact`); every variable a compose file interpolates must be documented
  (the compose-only allowlist is gone).
- `backup.sh` needs a dedicated directory, and retention deletes only complete
  sets it wrote; `restore.sh` refuses a dump without its `.sha256` unless
  `--allow-missing-checksum`.
- Docs: down-file labels worded truthfully; proxy health checks split into
  backend readiness (Part A) and `/nginx-health` (Part B); Part B's
  `NGINX_TRUSTED_PROXIES` named; `.env.example` says plainly that the
  production stack always reports to the built-in Sentry project today.
