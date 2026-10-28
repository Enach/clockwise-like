# Paceday operations runbook

How to run the production stack (`docker-compose.prod.yml`): settings, deploy
order, rollback, backups and restore. Part of PAC-81 (spec
`NEW-deploy-hardening`, Part D: AC-D3, AC-D4, AC-D6).

Passages marked **added by Part A/B/C** describe PAC-81 work that is not on
`main` yet; until it ships, the "today" instruction next to them applies.

Related documents:

- [`.env.example`](../.env.example) — the reference for every setting.
- [Proxy requirements](./operator/proxy.md) — TLS, HSTS, trusted proxy,
  timeouts, health checks.
- [Security](./security.md).

## Local setup

For development, see the repository [README](../README.md) (`docker compose
up` with `docker-compose.yml`, or `docker-compose.dev.yml`). Everything below
is about production.

## First installation

1. Copy `.env.example` to `.env` next to `docker-compose.prod.yml`, `chmod 600
   .env`, and fill in every `required` entry. Generate `JWT_SECRET` and
   `MCP_AUTH_TOKEN` with `openssl rand -hex 32`.
2. Put a TLS-terminating proxy in front, following
   [proxy requirements](./operator/proxy.md).
3. Set up the daily backup ([Backups](#backups)) **before** the first user
   signs in, and rehearse a restore once ([Restore](#restore)).
4. Deploy with the order below.

## Deploy order

Always in this order. `compose` below means
`docker compose -f docker-compose.prod.yml`.

1. **Back up** — `scripts/ops/backup.sh --out /var/backups/paceday --env-file .env`.
   Do not deploy without a backup taken minutes before: it is the only
   rollback for a migration (see [Rollback](#rollback)).
2. **Pull pinned images** — `compose pull`. Images are pinned to an exact
   version by a required setting — **added by Part C** (AC-C2). Today the
   compose file uses `:latest`, so before pulling, record the running images'
   digests to be able to go back:
   `docker inspect --format '{{.Config.Image}} {{.Image}}' $(compose ps -q) > deployed-$(date -u +%F).txt`.
3. **Start Postgres** — `compose up -d postgres`; wait until
   `compose ps postgres` shows `healthy`.
4. **Start the backend** — `compose up -d backend`. It runs every pending
   migration (up only) **before** it starts listening.
5. **Wait for readiness** — the backend's readiness check and container
   health check are **added by Part A and Part C** (AC-A11, AC-C3; health check
   start period 120 s, because migrations run first). Today, wait for
   `compose logs backend` to print `server listening on :8080`; a
   `failed to open database` / `run migrations` line instead means the
   migration failed: go to [Rollback](#rollback).
6. **Start the rest** — `compose up -d` (frontend, nginx, MCP). Check through
   your proxy: `curl -fsS https://<your host>/api/health`.

## Rollback

- **The backend is not ready after a migration** (it exited, or never
  listens): **restore the backup taken in step 1 and deploy the previous
  version** ([Restore](#restore), with the previous image). Never edit the
  `schema_migrations` table by hand, and **never migrate down in production**:
  several down files destroy data — `006_auth.down.sql` deletes every user
  account — and each says what it destroys on its first line. Down files
  exist for development and for tests, not for rollback.
- **Image-only change** (the release has no new migration): roll back by
  deploying the previous version — the previous pinned version once Part C
  ships, the digests recorded in step 2 today — with `compose up -d`. No
  restore needed.
- A release that added a migration and ran for a while cannot be rolled back
  without losing the data written since the backup. Prefer a fix-forward
  release; if you must go back, restore the pre-deploy backup and accept that
  loss explicitly.

## Expected interruption during a deploy

Paceday runs one backend; a deploy is not zero-downtime (spec §5).

- **Added by Part A** (AC-A6/A7, Part C's 50 s stop grace): on stop the
  backend first reports not-ready for 5 s while still serving, then stops
  accepting connections and lets in-flight requests, background jobs and
  emails finish for up to 30 s; work that cannot finish is cancelled and
  logged by name. Requests arriving during the restart get an error from nginx
  for the few seconds the new backend needs to start (longer if it migrates).
  A booking confirmation email still sending after the drain is logged with
  the booking id and not resent (owner default Q-O4).
- **Today**, the backend has no graceful shutdown: requests in flight when it
  stops are cut, and an email being sent is lost silently. Deploy at a quiet
  time.

## Backups

Owner default (Q-O3): **a daily dump, kept 14 days**. Up to 24 hours of data
can be lost. Point-in-time recovery and off-host storage are operator choices
this runbook does not configure; copy the backup directory off the host
(another machine, object storage) with your own tooling.

`scripts/ops/backup.sh` takes a consistent `pg_dump` (one snapshot) from the
running Postgres **without stopping the backend**, checks the dump is
readable, copies `.env` beside it, writes checksums, and deletes backup sets
older than 14 days:

```bash
scripts/ops/backup.sh --out /var/backups/paceday --env-file /opt/paceday/.env
# options: --compose-file FILE | --container NAME, --keep-days N (default 14)
```

Schedule it daily, e.g. in root's crontab on the Docker host:

```cron
15 3 * * * cd /opt/paceday && scripts/ops/backup.sh --out /var/backups/paceday --env-file .env >> /var/log/paceday-backup.log 2>&1
```

### Backups are secrets

- **A dump contains every user's stored provider credentials in plain
  text**: OAuth access and refresh tokens, Slack/Notion tokens and the AI API
  keys users entered (for example `029_llm_bring_your_own.up.sql`; nothing in
  the backend encrypts them — encryption at rest is a follow-up, spec §10).
  Anyone with a dump can act as your users at those providers.
- **The `.env` copy holds** `JWT_SECRET` (anyone with it can forge a session
  for any user), `POSTGRES_PASSWORD`, every OAuth client secret, `SMTP_PASS`
  and `MCP_AUTH_TOKEN`.
- The script writes every file mode 600 into a mode 700 directory. Store and
  transfer backups as secrets: encrypted at rest off the host, access limited
  to the people who can already read `.env`, deleted on the same 14-day
  schedule everywhere they are copied.
- A dump **without its `.env` does not restore a working stack**: with another
  `JWT_SECRET` every user is logged out, and without the OAuth client secrets
  no stored provider token can be refreshed. Always keep the pair together.

## Restore

On a fresh host (or after the volume was lost):

1. Install the repository at the **same version** that was running (the same
   image version; once Part C ships, the same pinned version setting).
2. Put the `paceday-<T>.env` saved with the dump in place as `.env`
   (`chmod 600`).
3. Start only Postgres on an empty volume: `compose up -d postgres`; wait for
   `healthy`.
4. Restore:
   `scripts/ops/restore.sh --dump /var/backups/paceday/paceday-<T>.dump`.
   It verifies the checksum, refuses a database that already has tables, and
   restores in one transaction.
5. Start the backend and wait for readiness (deploy steps 4–6). The restored
   `schema_migrations` matches the image, so no migration runs.
6. Verify:
   - row counts: `scripts/ops/rowcounts.sh > restored.counts`, and compare with
     the source's counts if the source is still readable (`diff` must print
     nothing for a restore of the latest dump);
   - a session that existed before the backup still works (sign-in is not
     needed; an open browser stays signed in);
   - `curl -fsS https://<your host>/api/health` (readiness once Part A ships).

### Rehearsing a restore

`scripts/ops/rehearse-backup-restore.sh` runs the whole procedure against
throwaway containers (Docker, Go, curl and openssl needed; ports 15492, 15493,
18091–18093 by default): it builds the backend from the checkout, seeds data
and issues a session before the backup, backs up while the backend runs,
restores onto a second Postgres, starts a backend with the saved `.env`, and
checks health, equal row counts for every public table and that the old
session is accepted. Negative controls: the same session against a backend
with another `JWT_SECRET` must get 401; a row added after the backup must make
the counts differ; a 15-day-old backup set must be deleted by retention.
Rehearse once per quarter and after any change to the scripts or to Postgres'
major version, and record it below.

### Rehearsal record

| Date (UTC) | Who | Where | Outcome |
|---|---|---|---|
| 2026-10-28 | factory implementer (PAC-81 Part D) | `rehearse-backup-restore.sh` at `bef4c31` + this branch, Docker 29.8.2 on Windows 11 (Git Bash), `postgres:16-alpine` | **Passed.** 34 public tables, counts equal; restored backend `/api/health` 200; pre-backup session `/api/auth/me` 200; other `JWT_SECRET` 401; post-backup row detected; 15-day-old set removed. Not checked on this host: the mode-600 assertion (Windows has no POSIX modes; the script checks it on Linux). Rehearsed with a binary built from the commit, not a published image (no image pipeline, spec F-C1). |

## Account recovery

Recovering an account whose only sign-in method stopped working is
documented by PAC-71 in `docs/operator/PAC-71-account-recovery.md`. That
document is not on `main` yet (PAC-71 is in review); this section links it
when PAC-71 merges.

## Documentation site

The user documentation (`docs/`, Astro/Starlight) is **published separately**
(`docs/vercel.json`) and is not served by this stack (spec D-16). Today the
production compose file still contains a `docs` service that nothing routes
to; Part C removes it (AC-C8). Publishing the docs is not part of a Paceday
deploy.
