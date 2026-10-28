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
3. **Start Postgres** — `compose up -d --wait postgres`: it returns once the
   service's `pg_isready` health check passes.
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
  version**. On the same host this means setting the failed database aside
  first: follow [Rollback on the same host](#rollback-on-the-same-host). Never
  edit the `schema_migrations` table by hand, and **never migrate down in
  production**: down files drop tables and columns with their data —
  `006_auth.down.sql` deletes every user account. The five down files added by
  PAC-81 (006, 007, 014, 015, 016) say what they destroy on their first line;
  the older ones do not, so read any down file as destructive. Down files
  exist for development and for tests, not for rollback.
- **Image-only change** (the release has no new migration): roll back by
  deploying the previous version — the previous pinned version once Part C
  ships, the digests recorded in step 2 today — with `compose up -d`. No
  restore needed.
- A release that added a migration and ran for a while cannot be rolled back
  without losing the data written since the backup. Prefer a fix-forward
  release; if you must go back, restore the pre-deploy backup and accept that
  loss explicitly.

### Rollback on the same host

After a failed migration the database is half-migrated, or marked dirty
(`Dirty database version N` in `compose logs backend`). The backend refuses to
start on it, so it restarts in a loop, and `restore.sh` refuses to restore over
it. Do not delete it: set it aside, restore into a fresh volume, deploy the
previous version. Run every command from the directory holding
`docker-compose.prod.yml` and `.env`.

1. **Stop the stack** (keeps the volumes — never add `-v`):

   ```bash
   docker compose -f docker-compose.prod.yml down
   ```

2. **Set the failed database aside.** Docker cannot rename a volume, so it is
   copied to a new volume and the original is removed:

   ```bash
   scripts/ops/set-aside-volume.sh --compose-file docker-compose.prod.yml
   # prints e.g. paceday_postgres_data-failed-20261028T191605Z
   ```

   What the script does, if you need to do it by hand. The volume is
   `<project>_postgres_data`. The project name is the directory holding the
   compose file, unless `-p` or `COMPOSE_PROJECT_NAME` set it;
   `docker compose -f docker-compose.prod.yml config | grep '^name:'` prints it.

   ```bash
   project="$(docker compose -f docker-compose.prod.yml config | awk '/^name:/ {print $2; exit}')"
   vol="${project}_postgres_data"
   copy="${vol}-failed-$(date -u +%Y%m%dT%H%M%SZ)"
   if docker volume inspect "$copy" >/dev/null 2>&1; then
     echo "$copy exists: stop"                  # never write into a volume you did not create
   elif [ -n "$(docker ps -aq --filter "volume=$vol")" ]; then
     echo "$vol is still in use: run step 1 first"
   else
     docker volume create "$copy"
     docker run --rm -v "$vol:/from:ro" -v "$copy:/to" postgres:16-alpine sh -c 'cp -a /from/. /to/'
     # compare: same entry count and content on both sides
     sums="$(for v in "$vol" "$copy"; do
       docker run --rm -v "$v:/v:ro" postgres:16-alpine sh -c \
         'cd /v && echo "$(find . | wc -l) $(find . -type f -exec md5sum {} + | sort -k2 | md5sum)"'
     done | uniq | wc -l)"
     if [ "$sums" -eq 1 ]; then
       docker volume rm "$vol" && echo "set aside as $copy"
     else
       echo "copy differs: $vol NOT removed"
     fi
   fi
   ```

   The script refuses if a volume with the copy's name already exists, so it
   never writes into a volume it did not create (`--copy-name NAME` chooses
   the name). The copy holds the same secrets as a dump; delete it
   (`docker volume rm <copy>`) once the failure is understood.

3. **Restore the pre-deploy backup** into a new, empty volume:

   ```bash
   docker compose -f docker-compose.prod.yml up -d --wait postgres   # creates an empty postgres_data
   scripts/ops/restore.sh --compose-file docker-compose.prod.yml \
     --dump /var/backups/paceday/paceday-<T>.dump               # the step-1 backup
   ```

4. **Deploy the previous version**: set the previous image version (Part C's
   version setting; today, the digests recorded in deploy step 2), then
   `docker compose -f docker-compose.prod.yml up -d backend`, wait for
   readiness, and `up -d` the rest (deploy steps 4–6).
5. **Verify** as in [Restore](#restore) step 6: row counts equal the pre-deploy
   backup's, an existing session still works, health through the proxy.

`scripts/ops/rehearse-backup-restore.sh` rehearses this path (its part 2): a
dirty database at version 32, the backend refusing it, `restore.sh` refusing
it, then the steps above.

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

- **`--out` must be a directory dedicated to these backups.** The script
  creates it if missing, refuses one that holds anything other than its own
  `paceday-<T>.dump/.env/.sha256` files, and makes it mode 700.
- **Retention only touches complete sets the script wrote**: a
  `paceday-<T>.sha256` that lists exactly `paceday-<T>.dump` and
  `paceday-<T>.env`, older than the retention, is deleted with those two
  files; so is a `paceday-<T>.dump.partial` left by an interrupted run once it
  is older than the retention. Any other file is left alone (a stray old dump
  is kept and must be removed by hand).

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

On a fresh host, or after the volume was lost. To roll back on a host whose
failed database still exists, see
[Rollback on the same host](#rollback-on-the-same-host).

1. Install the repository at the **same version** that was running (the same
   image version; once Part C ships, the same pinned version setting).
2. Put the `paceday-<T>.env` saved with the dump in place as `.env`
   (`chmod 600`).
3. Start only Postgres on an empty volume: `compose up -d --wait postgres`.
   Without `--wait` the command returns while Postgres is still initialising
   the new volume. `restore.sh` also waits (up to 120 s, `--wait-seconds`) until
   Postgres accepts TCP connections, which happens only after initialisation:
   the health check alone can pass on the temporary server that initialisation
   runs.
4. Restore:
   `scripts/ops/restore.sh --dump /var/backups/paceday/paceday-<T>.dump`.
   It verifies the checksum from `paceday-<T>.sha256`, waits for Postgres to
   finish starting, refuses a database that
   already has tables, and restores in one transaction. Without the `.sha256`
   it refuses; `--allow-missing-checksum` restores anyway after a warning —
   use it only for a dump whose integrity you have checked another way.
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

`scripts/ops/rehearse-backup-restore.sh` runs both procedures against
throwaway containers (Docker with Compose v2, Go, curl and openssl needed;
ports 15494, 15495 and 18091–18094 by default). It builds the backend from the
checkout, and runs the source database as a compose project on a named volume,
as in production.

- **Fresh host:** seeds users, an organization and a stored provider
  credential (`oauth_tokens`), issues a session before the backup, backs up
  while the backend runs, restores onto a second Postgres, starts a backend
  with the saved `.env`, and checks health, equal row counts for every public
  table, the credential intact and the old session accepted.
- **Same host:** takes a pre-deploy backup, simulates a failed migration
  (half-created table, `schema_migrations` dirty at 32), checks that the
  backend and `restore.sh` both refuse it, then runs
  [Rollback on the same host](#rollback-on-the-same-host) and checks health,
  counts, the session, and that the set-aside volume still holds the failed
  database.
- **Negative controls:** another `JWT_SECRET` gets 401; a row added after the
  backup makes the counts differ; `restore.sh` refuses a dump without its
  `.sha256` (and `--allow-missing-checksum` warns); `backup.sh` refuses a
  shared directory; retention deletes a 15-day-old set it wrote and keeps a
  file it did not write.

Rehearse once per quarter and after any change to the scripts or to Postgres'
major version, and record it below.

### Rehearsal record

| Date (UTC) | Who | Where | Outcome |
|---|---|---|---|
| 2026-10-28 | factory implementer (PAC-81 Part D) | `rehearse-backup-restore.sh` at `bef4c31` + this branch, Docker 29.8.2 on Windows 11 (Git Bash), `postgres:16-alpine` | **Passed.** 34 public tables, counts equal; restored backend `/api/health` 200; pre-backup session `/api/auth/me` 200; other `JWT_SECRET` 401; post-backup row detected; 15-day-old set removed. Not checked on this host: the mode-600 assertion (Windows has no POSIX modes; the script checks it on Linux). Rehearsed with a binary built from the commit, not a published image (no image pipeline, spec F-C1). |
| 2026-10-28 | factory implementer (PAC-81 Part D, review round 1) | `rehearse-backup-restore.sh` on this branch after `d65a5bd`, Docker 29.8.2 / Compose v2 on Windows 11 (Git Bash), `postgres:16-alpine` | **Passed** (19:16 UTC). Fresh host: 34 tables equal, `oauth_tokens` credential intact, health 200, old session 200. Same host: the backend refused the dirty database (`Dirty database version 32`), `restore.sh` refused it, the volume was set aside (1487 entries, content checksums equal), restored, health 200, counts equal to the pre-deploy backup, session 200, the set-aside copy still dirty at 32. Negative controls all as expected. Mode-600 check not run (Windows). |
| 2026-10-28 | factory implementer (PAC-81 Part D, review round 2) | `rehearse-backup-restore.sh` on this branch after `67b8876`, same host setup | **Passed** (19:29 UTC). The rehearsal now runs the runbook's commands only: `up -d --wait postgres` (with the production health check), no extra waits. `restore.sh` onto a Postgres started a moment earlier waited for it to finish initialising, then restored. `set-aside-volume.sh` refused an existing copy name and left both volumes untouched. Retention removed a 15-day-old `.dump.partial`. All earlier assertions passed. |
| 2026-10-28 | factory implementer (PAC-81 Part D, final round) | `rehearse-backup-restore.sh` on this branch merged with `main` at `ec1f096` (Part B shipped), same host setup | **Passed** (19:58 UTC), all 18 checks. The manual set-aside block above was also run verbatim against a throwaway compose project: it refused while the volume was in use, stopped when the copy name existed (the existing volume was left empty), and otherwise set the volume aside. |

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
