# Paceday operations runbook

How to run the production stack (`docker-compose.prod.yml`): settings, deploy
order, rollback, backups and restore. Part of PAC-81 (spec
`NEW-deploy-hardening`, Part D: AC-D3, AC-D4, AC-D6).

All four parts of PAC-81 have shipped: A (backend process), B (nginx edge),
C (compose and images) and D (this runbook).

Related documents:

- [`.env.example`](../.env.example) — the reference for every setting.
- [Proxy requirements](./operator/proxy.md) — TLS, HSTS, trusted proxy,
  timeouts, health checks.
- [Security](./security.md).

## Requirements

- **Docker Engine 25 or later**: the health checks use `start_interval`
  (frequent checks during the start period), which older engines do not
  support.
- **Docker Compose v2** (the `docker compose` plugin; **2.24.0 or later**, or
  the later 5.x line — the compose tests were run with 5.6.1). 2.24.0 is the
  first version that reads the backend's `env_file` entry with
  `required: false` (the encryption key file, [Encryption key](#encryption-key));
  an older 2.2x refuses the compose files before anything runs. The legacy
  `docker-compose` v1 cannot read these files.

## Local setup

For development, see the repository [README](../README.md) (`docker compose
up` with `docker-compose.yml`, or `docker-compose.dev.yml`). Everything below
is about production.

## First installation

1. Copy `.env.example` to `.env` next to `docker-compose.prod.yml`, `chmod 600
   .env`, and fill in every `required` entry, including `PACEDAY_VERSION`
   (the exact image version to run). Generate `JWT_SECRET` with
   `openssl rand -hex 32`. If the TLS proxy runs on this host, set
   `NGINX_BIND_ADDRESS=127.0.0.1` (and `NGINX_HTTP_PORT` if the proxy holds
   port 80).
2. **Encryption key.** Create `encryption.env` beside `.env` (never inside
   it) and store a copy apart from the backups, as
   [Encryption key](#encryption-key) says. Without it the backend refuses to
   start.
3. **MCP (optional).** The MCP server is off by default. To enable it, set
   `MCP_AUTH_TOKEN` (`openssl rand -hex 32`) and add
   `COMPOSE_FILE=docker-compose.prod.yml:docker-compose.mcp.yml` to `.env`
   (Windows hosts: `;` as the separator), so that every `docker compose`
   command below includes it. It is then served at
   `https://<your host>/mcp/sse` behind the token (owner decision Q-O1); see
   the README for the client configuration. Without the token compose
   refuses to start anything, naming `MCP_AUTH_TOKEN`.
4. Put a TLS-terminating proxy in front, following
   [proxy requirements](./operator/proxy.md).
5. Set up the daily backup ([Backups](#backups)) **before** the first user
   signs in, and rehearse a restore once ([Restore](#restore)).
6. Deploy with the order below.

## Deploy order

Always in this order. `compose` below means
`docker compose -f docker-compose.prod.yml`. With MCP enabled through
`COMPOSE_FILE` in `.env`, use plain `docker compose` in that directory
instead: an explicit `-f docker-compose.prod.yml` overrides `COMPOSE_FILE` and
would leave MCP out.

1. **Back up** — `scripts/ops/backup.sh --out /var/backups/paceday --env-file .env`.
   Do not deploy without a backup taken minutes before: it is the only
   rollback for a migration (see [Rollback](#rollback)).
2. **Set the new version and pull** — note the running `PACEDAY_VERSION`
   (the backup's copy of `.env` records it too), set the new one in `.env`,
   then `compose pull`. Every image is an exact version: the Paceday images
   are tagged `PACEDAY_VERSION` (compose refuses to run without it), and the
   nginx and Postgres versions are pinned in the compose file.
3. **Start Postgres** — `compose up -d --wait postgres`: it returns once the
   service's `pg_isready` health check passes.
4. **Start the backend** — `compose up -d backend`. It runs every pending
   migration (up only) **before** it starts listening. No other backend may be
   serving at that moment: see
   [Applying migrations: stop, migrate, start](#applying-migrations-stop-migrate-start).
5. **Wait for readiness** — the backend listens only after its migrations,
   then `GET /api/ready` answers `200 {"status":"ready"}` (PAC-81 Part A,
   AC-A11). Check it through your proxy, or inside the container with
   `compose exec backend /server health` (exit 0 = ready; the binary's
   health mode needs no database and no secret). The container health check
   runs the same command (`compose ps` shows `healthy`); its 120 s start
   period covers the migrations, so `compose up -d --wait backend` returns
   once the backend is ready. A `failed to open database` / `run migrations`
   line in `compose logs backend` instead means the migration failed: go to
   [Rollback](#rollback).
6. **Start the rest** — `compose up -d --wait` (frontend, nginx, and MCP when
   enabled). nginx starts only once the backend and the frontend (and MCP)
   are healthy. Check through your proxy:
   `curl -fsS https://<your host>/api/ready`.

### Applying migrations: stop, migrate, start

A release that adds a migration is applied with **no backend serving**. A
migration changes tables that live requests are reading and writing. If the
two run at the same time, Postgres can abort one of them (`40P01`): a
booking is lost, or the migration fails half-way and the database is left
dirty. From PAC-116's implementation on, the backend and `paceday-operator`
refuse to apply pending migrations while the app is serving. They print what
they saw and change nothing. There is no override.

- **One backend** (the shipped compose file): nothing extra to do. In
  [Deploy order](#deploy-order) step 4, `compose up -d backend` stops the old
  container (graceful drain) before the new one starts and migrates.
- **Several backends** (on one or more hosts):
  1. Stop every backend: `compose stop backend` on every host.
  2. Start **one** backend with the new version and wait until it is ready.
     It applies the migrations.
  3. Start the others.

  From PAC-116's implementation on, a new backend started while an old one
  is still serving refuses to start (`refusing to start: pending migrations
  … were not applied: the app is serving`). Docker restarts it
  (`restart: unless-stopped`) until the old ones are stopped. `compose up -d`
  still returns success: the refusal shows as `Restarting` in
  `compose ps backend` and as `refusing to start` in `compose logs backend`,
  and [Deploy order](#deploy-order) step 5 (`/api/ready`) does not turn
  green. The same happens for up to 90 seconds with one backend when the old
  container was killed rather than stopped (its heartbeat still counts).
- **`paceday-operator` newer than the database** (it would apply
  migrations): either start the new backend first, which migrates, and then
  use `compose exec backend /paceday-operator …` as usual; or stop every
  backend and use `compose run --rm --no-deps backend /paceday-operator …`. A
  refusal exits 6 for `encryption` subcommands (5 if another operator holds
  the maintenance lock) and 1 for `free-address`, `reassign` and
  `advance-all`. `encryption status` never applies migrations and never
  refuses as "serving": it prints the pending migrations and whether the app
  is serving, then its usual report. While a migration is in progress it exits
  5 (`being applied by …`).
- **Reading a refusal.** Each line names what blocked the migration:
  - `live backend heartbeat: instance … on "<host>", seen <n>s ago`: stop
    that backend. A backend that was killed rather than stopped stops
    counting 90 seconds after it was last seen.
  - `other database session: pid …`: something else is connected to the
    database (a backend that has not written its heartbeat yet, an older
    backend, another operator command, `psql`, a monitoring agent). Find it
    with:

    ```sql
    SELECT pid, application_name, usename, client_addr, backend_start, state
    FROM pg_stat_activity
    WHERE datname = current_database() AND pid <> pg_backend_pid()
      AND (backend_type = 'client backend' OR backend_type IS NULL);
    ```

    Run it as the database superuser (or a role with `pg_read_all_stats`):
    other roles see another role's sessions with most columns empty.

    Close it. As a last resort, use `SELECT pg_terminate_backend(<pid>);`.
    Stop monitoring agents that connect to the application database for the
    duration of the migration.
  - `the maintenance lock is held by …` or `being applied by …`: another
    migration or encryption command is running. Run again after it ends.
    "Being applied" with a dirty version is a migration in progress, not a
    failed one: do **not** `force` the version while it runs.
- **A lock holder on a dead host.** The maintenance lock is released as soon
  as its database session ends. But if the host (or its network) running a
  migration died, the server keeps that session until TCP keepalive notices:
  about 2 hours with Linux defaults, unless `tcp_keepalives_idle` is set.
  Until then every start refuses with `being applied by <name> (pid <p>)`.
  Find the holder:

  ```sql
  SELECT a.pid, a.application_name, a.client_addr, a.backend_start, a.state
  FROM pg_locks l JOIN pg_stat_activity a ON a.pid = l.pid
  WHERE l.locktype = 'advisory' AND l.classid = 8400 AND l.objid = 1
    AND l.granted;
  ```

  Once you are certain the process is gone, run
  `SELECT pg_terminate_backend(<pid>);`. If it died inside a migration file,
  that file's changes roll back and the version stays dirty: follow
  [Rollback](#rollback).
- **With a connection pooler** (none ships; for deployments that add one,
  such as pgbouncer):
  - Its idle server connections stay open after every backend has stopped,
    until `server_idle_timeout` (pgbouncer default: 600 s). Each one counts
    as an other database session, so migrations are refused until the
    pooler is stopped or told to reconnect (`RECONNECT`).
  - The backend's start and `paceday-operator` must reach PostgreSQL
    directly or through **session** pooling, never through transaction
    pooling: the migration lock is a session lock.
- **The Postgres health check.** `pg_isready` in the compose file opens a
  short connection to the application database every few seconds. A
  backend start whose check falls in that instant is refused as "serving",
  and Docker retries it a moment later. A single `refusing to start` line
  followed by a normal start is expected and harmless.

### PAC-71 release: sign every session out once (release step)

The PAC-71 release has one extra step, decided by the owner (OQ-6, 2026-11-01:
yes). **After** steps 1-6 above, with both the new backend and the new web app
running, advance every account's session generation once:

```
compose exec backend /paceday-operator advance-all -operator <you> -reason "PAC-71 deploy (OQ-6)"
```

- `advance-all` runs after the new backend has migrated, so it applies
  nothing itself. (From PAC-116's implementation on, a newer operator
  against an older schema refuses while the backend serves; see
  [Applying migrations](#applying-migrations-stop-migrate-start).)
- `advance-all` runs against the running backend (`exec`): it needs no
  backend stop, unlike the encryption subcommands below that use
  `run --rm --no-deps backend`. (`run --rm --no-deps backend /paceday-operator
  advance-all …` also works; it reads the same environment.)
- Every session issued before it gets 401 on its next request, so every user
  signs in once more. No account, credential or setting changes. The run is
  recorded in `operator_audit`; record in the release notes that it ran.
- It locks every account row. A request that is writing account data at that
  moment may be aborted by Postgres's deadlock detection and get a 500 (the
  user retries); if the command itself aborts, run it again.
- Deploy the web app as soon as possible after the backend: in between, the
  old web app's "connect a calendar" buttons start a sign-in.
- Microsoft sign-ins in flight at the backend deploy (started on the old API)
  are refused once, because their token response has no ID token. The user
  signs in again.

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
  setting the previous `PACEDAY_VERSION` in `.env` and `compose up -d --wait`.
  No restore needed.
- A release that added a migration and ran for a while cannot be rolled back
  without losing the data written since the backup. Prefer a fix-forward
  release; if you must go back, restore the pre-deploy backup and accept that
  loss explicitly.
- **Going back to a release before encryption at rest (PAC-84)** is never an
  image swap once the backend has converted the stored credentials: the older
  release would send ciphertext to the providers as tokens, and every
  connection would end up marked lost. Either restore the pre-deploy backup,
  or follow [Encryption key - downgrade](#encryption-key---downgrade).
- **Going back past PAC-87** (organisation AI consent periods, migration
  `036_llm_org_ai_consent_period`): this section applies unchanged — fix
  forward, or restore the pre-deploy backup and start the previous image. An
  image-only rollback does not work: the previous image cannot start on a
  database at 036. There is no operator command for 036's down step; it is
  for development, tests and staging (stop the backend, `migrate ... down 1`
  only while 036 is the newest applied migration, start the previous image).
  Release note for any rollback past PAC-87: every member who uses the
  organisation's AI is asked to confirm again (except followers confirmed at
  exactly the current configuration when 036's down step is used); nothing
  is sent before they confirm. No configuration, key or stored AI choice is
  lost. Once PAC-71's migration is applied above 036, 036 can no longer be
  reverted in place.

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

4. **Deploy the previous version**: set the previous `PACEDAY_VERSION` in
   `.env` (the restored backup's `paceday-<T>.env` holds it), then
   `docker compose -f docker-compose.prod.yml up -d backend`, wait for
   readiness, and `up -d` the rest (deploy steps 4–6).
5. **Verify** as in [Restore](#restore) step 6: row counts equal the pre-deploy
   backup's, an existing session still works, health through the proxy.

`scripts/ops/rehearse-backup-restore.sh` rehearses this path (its part 2): a
dirty database at version 32, the backend refusing it, `restore.sh` refusing
it, then the steps above.

## Expected interruption during a deploy

Paceday runs one backend; a deploy is not zero-downtime (spec §5).

- On SIGTERM or SIGINT (PAC-81 Part A, AC-A6/A7) the backend first answers
  `/api/ready` with 503 for 5 s while still serving, then stops accepting
  connections and lets in-flight requests, background jobs and emails finish
  for up to 30 s. Work that cannot finish is cancelled, given 5 s to return,
  and logged one line per unit (`shutdown: abandoned <kind> <id> …`, never an
  email address). The pool closes after that, and the process exits 0 when
  everything finished in the drain, 1 otherwise. The whole sequence takes at
  most about 43 s. The compose file gives every Paceday service a 50 s stop
  grace period (Docker's default is 10 s), so `compose stop backend` and
  `compose up -d` let it finish; do not pass a shorter `-t`.
- Container logs are rotated: `json-file`, 10 MB per file, 5 files per
  container.
- **With MCP enabled, a connected MCP client makes nginx's stop take the full
  50 s.** nginx stops gracefully: it waits for open connections, and an MCP
  client holds an event stream open on `/mcp/sse` indefinitely. So
  `compose stop`, `down` or an `up` that recreates nginx waits the whole
  50 s grace period while any client is connected, after which Docker ends
  nginx and the client reconnects. This is expected, not a hang. To restart
  quickly, stop MCP first (`compose stop mcp-server`): its streams close and
  nginx then stops at once.
- Requests arriving during the restart get an error from nginx for the few
  seconds the new backend needs to start (longer if it migrates). A booking
  confirmation email still sending after the drain is logged with the booking
  id and not resent (owner default Q-O4).

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

- **Stored provider credentials are encrypted in a dump** (PAC-84): OAuth
  access and refresh tokens, Slack/Notion tokens, the AI API keys users and
  organisations entered, webcal URLs, SSO client secrets. They open only with
  the encryption key, which lives in `encryption.env` and is **never part of
  the backup set** (`backup.sh` refuses an env file that holds it). A dump
  still holds everything else in clear: users' names and emails, calendar
  data the app stores, bookings, settings, team invitation tokens.
- **A dump taken before the conversion finished** (any dump from a release
  before PAC-84, and possibly the first one after the upgrade) still holds
  every credential not yet converted **in plain text**: anyone with it can act
  as your users at those providers. Such dumps age out with the 14-day
  retention; delete off-host copies on the same schedule.
- **Keep the encryption key apart from the dumps**: a dump without the key
  restores a stack whose stored connections cannot be read (see
  [Encryption key](#encryption-key)); the key without a dump is useless. Store
  the two in different places with different access.
- **The `.env` copy holds** `JWT_SECRET` (anyone with it can forge a session
  for any user), `POSTGRES_PASSWORD`, every OAuth client secret, `SMTP_PASS`
  and `MCP_AUTH_TOKEN`.
- The script writes every file mode 600 into a mode 700 directory. Store and
  transfer backups as secrets: encrypted at rest off the host, access limited
  to the people who can already read `.env`, deleted on the same 14-day
  schedule everywhere they are copied.
- A dump **without its `.env` does not restore a working stack**: with another
  `JWT_SECRET` every user is logged out, and without the OAuth client secrets
  no stored provider token can be refreshed. Always keep the pair together —
  and the encryption key separately (above).

## Restore

On a fresh host, or after the volume was lost. To roll back on a host whose
failed database still exists, see
[Rollback on the same host](#rollback-on-the-same-host).

1. Install the repository at the **same version** that was running (the same
   `PACEDAY_VERSION`, which the `paceday-<T>.env` saved with the dump holds).
2. Put the `paceday-<T>.env` saved with the dump in place as `.env`
   (`chmod 600`), and the encryption key, from where you keep it apart from
   the backups, in place as `encryption.env` (`chmod 600`): the key that was
   active when the dump was taken, or a later one with that key still listed
   in `PACEDAY_ENCRYPTION_KEY_PREVIOUS`. With no key or another key the
   backend refuses to start, naming the problem
   ([Encryption key](#encryption-key)).
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
   - `curl -fsS https://<your host>/api/ready`.

### Rehearsing a restore

`scripts/ops/rehearse-backup-restore.sh` runs both procedures against
throwaway containers (Docker with Compose v2, Go, curl and openssl needed;
ports 15494, 15495 and 18091–18094 by default). It builds the backend from the
checkout, and runs the source database as a compose project on a named volume,
as in production.

- **Fresh host:** generates an encryption key into `encryption.env` outside
  the backup directory, seeds users, an organization and stored provider
  credentials (a Google token in `oauth_tokens`, a webcal URL) in plain as an
  older release left them, restarts the backend so its conversion encrypts
  them, issues a session before the backup, backs up while the backend runs
  (and checks that the backup set holds neither the key nor a seeded
  secret), restores onto a second Postgres, starts a backend with the saved
  `.env` and the separately kept key, and checks health, equal row counts for
  every public table, the credentials read back **through the backend** (the
  Google calendar connected, the webcal URL returned unchanged to its owner)
  and the old session accepted.
- **Same host:** takes a pre-deploy backup, simulates a failed migration
  (half-created table, `schema_migrations` dirty at 32), checks that the
  backend and `restore.sh` both refuse it, then runs
  [Rollback on the same host](#rollback-on-the-same-host) and checks health,
  counts, the session, and that the set-aside volume still holds the failed
  database.
- **Negative controls:** another `JWT_SECRET` gets 401; another encryption
  key makes the restored backend refuse to start; a row added after the
  backup makes the counts differ; `restore.sh` refuses a dump without its
  `.sha256` (and `--allow-missing-checksum` warns); `backup.sh` refuses a
  shared directory and an env file holding the encryption key; retention
  deletes a 15-day-old set it wrote and keeps a file it did not write.

Rehearse once per quarter and after any change to the scripts or to Postgres'
major version, and record it below.

### Rehearsal record

| Date (UTC) | Who | Where | Outcome |
|---|---|---|---|
| 2026-10-28 | factory implementer (PAC-81 Part D) | `rehearse-backup-restore.sh` at `bef4c31` + this branch, Docker 29.8.2 on Windows 11 (Git Bash), `postgres:16-alpine` | **Passed.** 34 public tables, counts equal; restored backend `/api/health` 200; pre-backup session `/api/auth/me` 200; other `JWT_SECRET` 401; post-backup row detected; 15-day-old set removed. Not checked on this host: the mode-600 assertion (Windows has no POSIX modes; the script checks it on Linux). Rehearsed with a binary built from the commit, not a published image (no image pipeline, spec F-C1). |
| 2026-10-28 | factory implementer (PAC-81 Part D, review round 1) | `rehearse-backup-restore.sh` on this branch after `d65a5bd`, Docker 29.8.2 / Compose v2 on Windows 11 (Git Bash), `postgres:16-alpine` | **Passed** (19:16 UTC). Fresh host: 34 tables equal, `oauth_tokens` credential intact, health 200, old session 200. Same host: the backend refused the dirty database (`Dirty database version 32`), `restore.sh` refused it, the volume was set aside (1487 entries, content checksums equal), restored, health 200, counts equal to the pre-deploy backup, session 200, the set-aside copy still dirty at 32. Negative controls all as expected. Mode-600 check not run (Windows). |
| 2026-10-28 | factory implementer (PAC-81 Part D, review round 2) | `rehearse-backup-restore.sh` on this branch after `67b8876`, same host setup | **Passed** (19:29 UTC). The rehearsal now runs the runbook's commands only: `up -d --wait postgres` (with the production health check), no extra waits. `restore.sh` onto a Postgres started a moment earlier waited for it to finish initialising, then restored. `set-aside-volume.sh` refused an existing copy name and left both volumes untouched. Retention removed a 15-day-old `.dump.partial`. All earlier assertions passed. |
| 2026-10-28 | factory implementer (PAC-81 Part D, final round) | `rehearse-backup-restore.sh` on this branch merged with `main` at `ec1f096` (Part B shipped), same host setup | **Passed** (19:58 UTC), all 18 checks. The manual set-aside block above was also run verbatim against a throwaway compose project: it refused while the volume was in use, stopped when the copy name existed (the existing volume was left empty), and otherwise set the volume aside. |

## Encryption key

Since PAC-84 every stored secret — Google tokens, the Outlook and Zoom
records, personal-calendar tokens and URLs, the webcal URL, Slack/Notion
tokens, users' and organisations' AI keys, SSO client secrets and SAML
certificates — is stored encrypted (AES-256-GCM). The values are encrypted
with **data keys** kept in the database (`encryption_data_keys`); each data
key is itself encrypted ("wrapped") by **the encryption key** you supply in
`PACEDAY_ENCRYPTION_KEY`. The database alone, or a dump, therefore opens
nothing; the key alone opens nothing either.

**Generate** it once per deployment, in the directory of `.env`:

```sh
echo "PACEDAY_ENCRYPTION_KEY=$(date -u +%Y%m%d):$(openssl rand -base64 32)" > encryption.env && chmod 600 encryption.env
```

The value is `<key id>:<base64 of 32 random bytes>`; the id (here the date)
is what logs, errors and `encryption status` print — the key bytes are never
printed. Standard padded base64 only (what `openssl rand -base64 32` prints).

**Where it lives.**

- `encryption.env`, beside `.env`, mode 600, git-ignored. The backend service
  of `docker-compose.prod.yml` (and `docker-compose.yml`) reads it through
  `env_file` with `required: false` (Docker Compose 2.24.0 or later); neither
  compose file names the key under `environment:`.
- **Never in `.env`**: `.env` travels with every backup, and
  `scripts/ops/backup.sh` refuses (exit 2, nothing written) an env file that
  sets `PACEDAY_ENCRYPTION_KEY` or `PACEDAY_ENCRYPTION_KEY_PREVIOUS`.
- **Back it up separately**: a copy in your password manager or secrets
  vault, with access limited to the people who operate the deployment, not
  next to the dumps. A restore needs it (see [Restore](#restore)).
- Whoever controls Docker on the host can read it (`docker inspect` shows a
  container's environment). That is accepted until the key moves to a KMS
  (owner decision Q-1, a follow-up).

**Start.** The backend refuses to start — before it opens the database, runs
a cron or listens — when the key is missing or malformed, when it does not
open the stored data keys (wrong key), or when a data key is wrapped by a key
id that is neither `PACEDAY_ENCRYPTION_KEY` nor listed in
`PACEDAY_ENCRYPTION_KEY_PREVIOUS`. Every such line starts
`refusing to start:` and ends `(docs/runbook.md, "Encryption key")`. On
success it logs
`encryption: key "<id>" active; <n> data keys (active <m>); <r> re-wrapped from previous keys`.
It also refuses the committed e2e test keys (whatever their id) unless
`PACEDAY_ENCRYPTION_ALLOW_TEST_KEY=true`, which only the e2e stack sets.

**Upgrading to the release with encryption at rest.** Create
`encryption.env` **before** the upgrade (the backend refuses to start
without it) and check Docker Compose is 2.24.0 or later. After the upgrade
the backend serves at once and, once per start, converts the remaining plain
values in the background in small batches (`encryption: conversion done: <n>
values encrypted in <d>`); plain values keep working meanwhile. Check:

```sh
docker compose -f docker-compose.prod.yml exec backend /paceday-operator encryption status --check
```

exits 0 when nothing plain is left (3 while some is).

### Operator commands

`paceday-operator encryption <subcommand>` ships in the backend image as
`/paceday-operator`. It reads `DATABASE_URL` and the same two key variables
as the backend. From PAC-116's implementation on, like the backend, it
applies pending migrations only while no backend is serving
([Applying migrations](#applying-migrations-stop-migrate-start)). Subcommands that need the backend **running or not** use
`exec`:

```sh
docker compose -f docker-compose.prod.yml exec backend /paceday-operator encryption status
```

Subcommands that need the backend **stopped** (`decrypt-all`,
`reset-lost-key`, `downgrade-schema`) run in a one-off container of the
backend service, with Postgres up (`run` gives it the service's `env_file`,
so the key comes from `encryption.env`; compose's own `--env-file` option is
not how the key arrives):

```sh
docker compose -f docker-compose.prod.yml stop backend
docker compose -f docker-compose.prod.yml run --rm --no-deps backend /paceday-operator encryption decrypt-all --operator you@example.com --reason "downgrade to 1.4.2"
```

| Subcommand | Needs | Does |
|---|---|---|
| `status [--check]` | no key | Per secret column: empty, plain and encrypted counts, encrypted per data key; per data key: id, state, wrapping key id, age, values, `sealed_count`, `rotate due` (older than 365 days or past 2^29 seals); live backends. Read-only. |
| `convert --operator --reason [--batch 100]` | active key (the one wrapping the active data key) | Encrypts every plain value. |
| `rotate-data-key --operator --reason --retire <id> [--batch 100] [--wait 5m]` | active key (the one wrapping the active data key) | Makes a new active data key, waits until every running backend uses it, re-encrypts the values under data key `<id>`. |
| `decrypt-all --operator --reason [--empty-undecryptable]` | keys that open every data key; backend stopped | Writes every value back in plain and deletes the data keys (prepare a downgrade). |
| `reset-lost-key --operator --reason --confirm-lost-key` | the **new** key only; backend stopped | The lost-key reset below. |
| `downgrade-schema --operator --reason` | no key; backend stopped | Runs this release's schema down step (see the downgrade below). |

Every mutating subcommand writes one `operator_audit` row (who, why, counts —
never a value), takes the maintenance lock, and is safe to re-run (a second
run reports 0 changes). Exit codes:

| Code | Meaning |
|---|---|
| 0 | done (including "nothing to do") |
| 1 | the run failed or ended incomplete (database error, values that could not be processed, rotation wait timed out); the audit row says `failed`/`partial` and a re-run resumes |
| 2 | usage: unknown subcommand or flag, missing `--operator`/`--reason`/`--retire`/`--confirm-lost-key`, missing `DATABASE_URL` |
| 3 | `status --check` only: at least one plain value remains |
| 4 | key refused: active key missing or malformed, a configured key does not open a data key, a data key wrapped by an unknown key id, `convert`/`rotate-data-key` run with an active key that does not wrap the active data key; for `reset-lost-key`: a configured key **does** open a stored data key (the key is not lost) |
| 5 | maintenance lock held by another process (named, with its pid) |
| 6 | a backend is running (`decrypt-all`, `reset-lost-key`): see below |

### Encryption key - stop the backend first

`decrypt-all` and `reset-lost-key` refuse (exit 6) while any backend has
reported itself in the last 90 seconds: a running backend would re-encrypt
values behind `decrypt-all`, or keep using data keys `reset-lost-key`
removed. Stop **every** backend (`docker compose -f docker-compose.prod.yml
stop backend` on every host), wait 90 seconds, then run the command with
`run --rm --no-deps` as above. A backend that starts while one of them runs
refuses to start (`paceday-operator encryption <subcommand> is running (pid
<p>); start the backend after it ends`).

### Rotating the encryption key

Do this when the key may have been exposed, or on a schedule. No value is
re-encrypted; only the few data keys are re-wrapped.

1. Generate a new key with a new id. In `encryption.env`, make it
   `PACEDAY_ENCRYPTION_KEY` and move the old one to
   `PACEDAY_ENCRYPTION_KEY_PREVIOUS` (comma-separated, at most 8):

   ```sh
   PACEDAY_ENCRYPTION_KEY=20270101:<new base64>
   PACEDAY_ENCRYPTION_KEY_PREVIOUS=20261102:<old base64>
   ```

2. Restart the backend (`compose up -d --wait backend`). At start it re-wraps
   every data key under the new key in one transaction
   (`<r> re-wrapped from previous keys`). With several backends, roll the
   change to **every** one with the same two variables.
3. `encryption status` shows 0 data keys under the old id, and the backend
   logs `previous key "<id>" wraps no data key; remove it once the backup
   retention (14 days) has passed`.
4. **Keep the old key for at least 14 days** (the backup retention): a dump
   taken before the rotation has its data keys wrapped by the old key. Then
   remove it from `_PREVIOUS` and from your separate copy.

### Rotating a data key

Only needed if a data key may have been exposed (or `status` says
`rotate due`):

```sh
docker compose -f docker-compose.prod.yml exec backend /paceday-operator encryption rotate-data-key --retire <data key id> --operator you@example.com --reason "scheduled rotation"
```

It creates a new active data key, waits (up to `--wait`, default 5m) until
every running backend reports it, then re-encrypts the values under the old
one in batches. Afterwards `status` shows 0 values under it; retired data keys
are kept (60 bytes each). A backend also rotates by itself when its active
data key reaches 2^30 encryptions.

### Losing the key

Losing `PACEDAY_ENCRYPTION_KEY` (and every previous key) loses **every
stored connection**: nothing can decrypt them (owner decision Q-2). The
deployment is recovered with a reset, and users reconnect:

1. Stop every backend ([stop the backend first](#encryption-key---stop-the-backend-first)).
2. Generate a new key into `encryption.env` (only the new key; no previous).
3. Run:

   ```sh
   docker compose -f docker-compose.prod.yml run --rm --no-deps backend /paceday-operator encryption reset-lost-key --confirm-lost-key --operator you@example.com --reason "encryption key lost"
   ```

   Every encrypted value is removed as its owner's own disconnect would
   remove it, with no call to any provider: Google and Slack/Notion
   connections deleted, Outlook and Zoom removed (conferencing back to Meet),
   personal calendars holding an encrypted value disabled and emptied, the
   webcal URL, users' and organisations' AI keys and SSO client secrets and
   certificates emptied. Plain values are left alone. Every data key is
   deleted and a new one is created under the new key. It refuses (exit 4) if
   any configured key still opens a stored data key: then the key is not
   lost.
4. Start the backend. Users see those connections as not connected and
   reconnect; organisation admins re-enter the org AI key and the SSO client
   secret (SSO sign-in for that domain fails until then; break-glass still
   works).

### Encryption key - downgrade

To go back to a release from before encryption at rest **without** restoring
a backup (no data lost, but the stored credentials are written back in plain
text — deliberately, and audited):

1. Stop every backend ([stop the backend first](#encryption-key---stop-the-backend-first)).
2. Write every value back in plain and delete the data keys:
   `… run --rm --no-deps backend /paceday-operator encryption decrypt-all --operator … --reason …`.
   Values that cannot be decrypted are listed and left as they are (exit 1);
   `--empty-undecryptable` removes them as the lost-key reset would.
3. Run the schema down step:
   `… run --rm --no-deps backend /paceday-operator encryption downgrade-schema --operator … --reason …`.
   It refuses (exit 1, naming `paceday-operator encryption decrypt-all`)
   while any encrypted value or data key remains, and never leaves
   `schema_migrations` dirty. It drops the data-key and heartbeat tables and
   keeps `operator_audit`.
4. Set the previous `PACEDAY_VERSION` and start it. **Never start the new
   image in between**: its automatic conversion would encrypt everything
   again.

**After `downgrade-schema`, do not run the new image's backend.** This
release's `paceday-operator encryption` commands refuse to migrate a
database below this release's schema: `status` only reports it, and the
other commands exit 1 without changing anything. The backend server
would migrate it up again, and the previous release would then refuse to
start, because it does not know that schema version. Check the result with
plain SQL instead:

```bash
docker compose -f docker-compose.prod.yml exec postgres \
  psql -U clockwise -d clockwise -c 'SELECT version, dirty FROM schema_migrations'
```

The version must be below this release's encryption migration (034) and
`dirty` must be `f`.

`downgrade-schema` runs only on a database exactly at 034. A release that
also carries a later migration (PAC-71's `035_identity_binding`) leaves the
database at 035, and `downgrade-schema` then refuses (exit 1, "the database is at version 35, not 34: roll back the later release first") without changing anything. Going back from such a release
to one before encryption at rest is a restore of the pre-deploy backup
([Rollback](#rollback)); never migrate down in production by hand.

The alternative is restoring the pre-deploy backup (losing what was written
since).

## Booker erasure (PAC-40)

When an owner deletes a scheduling link, the link and its bookings move to
the owner's archive. **90 × 24 hours after the latest meeting end of any of
the link's bookings** (whatever their status), the backend erases each
booker's **name and email address** on that link. Only deleted links are
erased; live and paused links never are. There is no setting that turns this
off or changes the 90 days.

**What it erases and what it keeps.** In the database, each booking's
`booker_name` and `booker_email` become empty and `booker_erased_at` records
the instant; the archive then shows the booking as erased. Everything else is
kept: the link, its deletion time, its hosts, each booking's start, end,
status, notes, creation time and calendar-event references. So uses counts,
busy times and the reserved slug do not change.

**What it cannot reach.** Copies outside the database stay as they are:

- the attendee (the booker's email) and description on each host's calendar
  event;
- the confirmation email and invitation the booker received;
- **database backups taken before the erasure**: they keep the values until
  the backup set is deleted by its retention (14 days by default,
  [Backups](#backups)). **The backup retention therefore bounds how long an
  erased value survives anywhere Paceday controls.** A database page that held
  an erased value is reclaimed by Postgres' routine cleanup, not at once.

**When it runs.** On its own, inside the backend, with no operator action:
once at every start-up, then every 24 hours. A link is erased within 24 hours
of becoming due while the backend runs; one that became due while it was down
is erased by the first run after start. Every run writes exactly one line to
the backend log, also when it erases nothing:

```
booker-erasure: run at=2026-06-01T03:00:00Z links_examined=2 links_erased=2 bookings_erased=5 failures=0
```

The newest such line is the last run. Each erased link also gets one entry
`scheduling_link_bookers_erased` in its **owner's** activity log (title, link
id, bookings erased, last booking date, run time). No log line, activity
entry or error report ever holds a booker's name or email.

**Listing what is due**, without seeing any personal data (read-only, no
`--operator`/`--reason`, never applies a migration, not written to
`operator_audit`; backend running or not):

```sh
docker compose -f docker-compose.prod.yml exec backend /paceday-operator booker-erasure status [--check]
```

It prints one line per deleted link that is due, overdue (more than 24 h past
due) or due within 7 days: link id, owner user id, `deleted_at`,
`last_booking_end_at`, `erasure_due_at`, bookings awaiting erasure, and
`overdue`. Never a name, an email or a title. Exit codes: 0 listed, 1 failure
(database unreachable, schema below migration 037 or dirty, query error),
2 usage, 3 with `--check` only when at least one link is overdue (use that
for monitoring).

**When a run line shows `failures>0`.** Each failed link has its own line
`booker-erasure: link <id> failed: <class>`, where the class is a Postgres
SQLSTATE (`sqlstate 55P03` is a lock wait past the 10 s bound, `sqlstate
40P01` a deadlock) or a fixed word such as `context canceled` (shutdown).
A failed link is rolled back entirely and retried by the next run; the
others in the run are unaffected. Run `booker-erasure status`: a link that
stays `overdue` across two daily runs needs a look. Read the class, check the
database (locks: `pg_stat_activity`, `pg_locks`), and restart the backend to
retry at once (the start-up run). Open an issue if the same link keeps
failing; do not edit the bookings by hand.

**After a restore.** A backup taken before an erasure brings the erased values
back. Start the backend ([Restore](#restore) step 5), which runs the erasure
at start-up, and **check the next `booker-erasure: run` line before anyone
uses the restored data**: the links due are erased again, each with a new
activity entry.

**Erasure cannot be undone**, not by a code revert either: reverting the
release stops the job, and what it erased stays erased. Only a pre-erasure
backup within its retention holds the values, and restoring one to undo an
erasure defeats the owner's decision; after any restore the start-up run
erases them again anyway.

**Rolling back the deleted state (part A, migration 037).** Never migrate
down in production ([Rollback](#rollback)): restore the pre-deploy backup.
If 037's down step is ever run (development, staging), every deleted link
becomes an ordinary **paused** link that its owner can resume, and upgrading
again does not bring the deletion back (`deleted_at` is dropped); those links
then leave the archive and are never erased. Before any rollback that crosses
037, record the deleted link ids
(`SELECT id FROM scheduling_links WHERE deleted_at IS NOT NULL`, or
`booker-erasure status` for those with bookings awaiting erasure).

## Account recovery

Recovering an account whose only sign-in method stopped working, or that
is bound to the wrong identity, is documented in
[`docs/operator/PAC-71-account-recovery.md`](operator/PAC-71-account-recovery.md)
(the `paceday-operator` command ships in the backend image).

## Documentation site

The user documentation (`docs/`, Astro/Starlight) is **published separately**
(`docs/vercel.json`) and is not served by this stack (spec D-16): the
production compose file has no docs service. Publishing the docs is not part
of a Paceday deploy.
