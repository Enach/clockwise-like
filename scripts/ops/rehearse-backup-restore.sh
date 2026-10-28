#!/usr/bin/env bash
# Backup/restore rehearsal (PAC-81 Part D, AC-D3) — the integration test for
# backup.sh, restore.sh, rowcounts.sh and set-aside-volume.sh. It needs
# Docker (with Compose v2), Go, curl and openssl, and touches nothing but
# what it creates.
#
# Part 1 — restore on a fresh host:
#   1. A "source host": a compose project with a postgres service on a named
#      volume, as in docker-compose.prod.yml. A "fresh host": a second,
#      empty Postgres container.
#   2. The backend built from this tree, run against the source with a
#      generated .env; users, an organization and a stored provider
#      credential (oauth_tokens) seeded, and a session cookie issued BEFORE
#      the backup, checked to work.
#   3. backup.sh against the running source (the backend keeps running).
#   4. restore.sh onto the fresh host; a backend started with the .env saved
#      beside the dump.
#   5. Asserts: health 200; equal row counts for every public table; the
#      stored credential intact; the pre-backup cookie accepted.
#   6. Negative controls: another JWT_SECRET gets 401; a row added after the
#      backup makes the counts differ; restore.sh refuses a dump without its
#      .sha256 (and a non-empty database); backup.sh refuses a shared
#      directory; retention deletes a 15-day-old set it wrote and nothing else.
#
# Part 2 — rollback on the same host after a failed migration
# (docs/runbook.md "Rollback on the same host"):
#   7. Pre-deploy backup of the source; then a failed migration is simulated
#      (a half-created table, schema_migrations marked dirty at version 32).
#   8. The backend refuses to start on it, and restore.sh refuses it.
#   9. compose down; set-aside-volume.sh copies the volume aside and removes
#      it; compose up -d postgres; restore.sh; the backend (the "previous
#      version": the same build) starts.
#  10. Asserts: health 200; counts equal the pre-deploy backup's; the cookie
#      still works; the set-aside copy still holds the dirty database.
#
# Usage: scripts/ops/rehearse-backup-restore.sh
# Ports (override with env): REHEARSAL_PG_SRC_PORT=15494 REHEARSAL_PG_DST_PORT=15495
#                            REHEARSAL_API_PORT_BASE=18091 (uses +0 … +3)
# Postgres image: REHEARSAL_PG_IMAGE=postgres:16-alpine (the prod compose's).
set -euo pipefail
# Path conversion stays on here (git, go and the backend binary need it); the
# ops scripts it calls turn it off for their docker calls, so paths handed to
# them go through native_path.

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "$here/../.." && pwd)"
src_port="${REHEARSAL_PG_SRC_PORT:-15494}"
dst_port="${REHEARSAL_PG_DST_PORT:-15495}"
api_base="${REHEARSAL_API_PORT_BASE:-18091}"
pg_image="${REHEARSAL_PG_IMAGE:-postgres:16-alpine}"
for p in "$src_port" "$dst_port"; do
  case "$p" in 15432|15433) echo "rehearsal: port $p is reserved for the factory gate" >&2; exit 2 ;; esac
done
native_path() { if command -v cygpath >/dev/null 2>&1; then cygpath -m "$1"; else printf '%s' "$1"; fi; }

run_id="pac81-rehearsal-$$"
project="$run_id"   # compose project of the source host
dst="$run_id-dst"
work="$(mktemp -d)"
compose_file="$(native_path "$work/docker-compose.yml")"
pids=()
cleanup() {
  for pid in "${pids[@]:-}"; do [ -n "$pid" ] && kill "$pid" 2>/dev/null || true; done
  docker compose -f "$compose_file" down -v >/dev/null 2>&1 || true
  docker rm -f "$dst" "$run_id-inspect" >/dev/null 2>&1 || true
  # set-aside copies of this run's volume, including one from a failed run
  docker volume ls -q --filter "label=paceday.set-aside-from=${project}_postgres_data" | while read -r v; do
    docker volume rm "$v" >/dev/null 2>&1 || true
  done
  rm -rf "$work"
}
trap cleanup EXIT

step() { printf '\n== %s\n' "$*"; }
fail() { echo "REHEARSAL FAILED: $*" >&2; exit 1; }
b64url() { openssl base64 -A | tr '+/' '-_' | tr -d '='; }

step "generated .env (stands in for the operator's)"
umask 077
cat > "$work/.env" <<EOF
POSTGRES_PASSWORD=$(openssl rand -hex 16)
JWT_SECRET=$(openssl rand -hex 32)
MCP_AUTH_TOKEN=$(openssl rand -hex 32)
GOOGLE_CLIENT_SECRET=rehearsal-not-a-real-secret
EOF
# shellcheck disable=SC1091
set -a; . "$work/.env"; set +a

step "source host: compose project $project (postgres on volume ${project}_postgres_data, :$src_port); fresh host :$dst_port"
cat > "$work/docker-compose.yml" <<EOF
name: $project
services:
  postgres:
    image: $pg_image
    environment:
      POSTGRES_DB: clockwise
      POSTGRES_USER: clockwise
      POSTGRES_PASSWORD: $POSTGRES_PASSWORD
    ports:
      - "127.0.0.1:$src_port:5432"
    volumes:
      - postgres_data:/var/lib/postgresql/data
volumes:
  postgres_data:
EOF
docker compose -f "$compose_file" up -d postgres >/dev/null 2>&1
docker run -d --name "$dst" -e POSTGRES_DB=clockwise -e POSTGRES_USER=clockwise \
  -e POSTGRES_PASSWORD="$POSTGRES_PASSWORD" -p "127.0.0.1:$dst_port:5432" "$pg_image" >/dev/null
src_container() { docker compose -f "$compose_file" ps -q postgres; }
wait_pg() {
  for _ in $(seq 1 60); do
    docker exec "$1" pg_isready -U clockwise -d clockwise -h 127.0.0.1 >/dev/null 2>&1 && return 0
    sleep 1
  done
  fail "postgres $1 not ready"
}
wait_pg "$(src_container)"; wait_pg "$dst"

step "build the backend from this tree ($(git -C "$root" rev-parse --short HEAD))"
exe="$work/paceday-backend"; case "$(uname -s)" in MINGW*|MSYS*|CYGWIN*) exe="$exe.exe" ;; esac
( cd "$root/backend" && go build -o "$exe" . )

launch_backend() { # name port db_port jwt_secret — starts it, prints nothing
  # A local, unreachable DSN keeps the rehearsal from reporting to any real
  # Sentry project (before Part A an empty DSN falls back to a built-in one).
  env -i PATH="$PATH" SYSTEMROOT="${SYSTEMROOT:-}" TEMP="${TEMP:-/tmp}" \
    PORT="$2" DATABASE_URL="postgres://clockwise:$POSTGRES_PASSWORD@127.0.0.1:$3/clockwise?sslmode=disable" \
    JWT_SECRET="$4" SENTRY_DSN="http://rehearsal@127.0.0.1:9/1" SENTRY_ENVIRONMENT=rehearsal \
    "$exe" > "$work/$1.log" 2>&1 &
  pids+=("$!")
  last_pid="$!"
}
start_backend() { # name port db_port jwt_secret — starts it and waits for health
  launch_backend "$@"
  for _ in $(seq 1 90); do
    [ "$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$2/api/health")" = 200 ] && return 0
    kill -0 "$last_pid" 2>/dev/null || { tail -20 "$work/$1.log" >&2; fail "backend $1 exited"; }
    sleep 1
  done
  tail -20 "$work/$1.log" >&2
  fail "backend $1 never answered /api/health"
}
health() { curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$1/api/health"; }
me_status() { # port token
  curl -s -o /dev/null -w '%{http_code}' --cookie "auth_token=$2" "http://127.0.0.1:$1/api/auth/me"
}
psql_in() { docker exec "$1" psql -U clockwise -d clockwise -v ON_ERROR_STOP=1 -Atc "$2"; }
psql_src() { psql_in "$(src_container)" "$1"; }

# ── Part 1 ────────────────────────────────────────────────────────────────────
step "source backend :$api_base (runs migrations), seed data, issue a session"
start_backend source "$api_base" "$src_port" "$JWT_SECRET"
source_pid="$last_pid"
uid="$(psql_src "INSERT INTO users (email, name) VALUES ('rehearsal-owner@example.com', 'Rehearsal Owner') RETURNING id" | head -1)"
psql_src "INSERT INTO users (email, name) VALUES ('rehearsal-member@example.com', 'Rehearsal Member')" >/dev/null
psql_src "INSERT INTO organizations (name, domain) VALUES ('Rehearsal', 'rehearsal.example.com')" >/dev/null
psql_src "UPDATE users SET org_id = (SELECT id FROM organizations WHERE domain = 'rehearsal.example.com')" >/dev/null
# A stored provider credential, as a connected Google calendar leaves one.
# Expiry far ahead, so no renewal is attempted against Google.
psql_src "INSERT INTO oauth_tokens (user_id, access_token, refresh_token, expiry, calendar_id)
          VALUES ('$uid', 'rehearsal-access-token', 'rehearsal-refresh-token', NOW() + INTERVAL '30 days', 'primary')" >/dev/null
cred_query="SELECT access_token || '/' || refresh_token FROM oauth_tokens WHERE user_id = '$uid'"
now="$(date +%s)"
h="$(printf '%s' '{"alg":"HS256","typ":"JWT"}' | b64url)"
p="$(printf '{"sub":"%s","email":"rehearsal-owner@example.com","iat":%d,"exp":%d}' "$uid" "$now" "$((now + 86400))" | b64url)"
s="$(printf '%s' "$h.$p" | openssl dgst -sha256 -hmac "$JWT_SECRET" -binary | b64url)"
token="$h.$p.$s"
[ "$(me_status "$api_base" "$token")" = 200 ] || fail "the session does not work on the source"
# Touch a user-scoped endpoint so the backend writes rows of its own.
curl -s -o /dev/null --cookie "auth_token=$token" "http://127.0.0.1:$api_base/api/settings" || true
echo "user $uid signed in on the source (cookie issued at $now)"

step "backup.sh while the source backend is running"
backups="$work/backups"
dump="$("$here/backup.sh" --out "$backups" --env-file "$work/.env" --compose-file "$compose_file")"
"$here/rowcounts.sh" --compose-file "$compose_file" > "$work/source.counts"
kill -0 "$source_pid" || fail "the source backend stopped during the backup"
[ "$(health "$api_base")" = 200 ] || fail "source not healthy after backup"
ls -l "$backups"
for f in "$dump" "${dump%.dump}.env" "${dump%.dump}.sha256"; do
  [ -f "$f" ] || fail "missing $f"
  case "$(uname -s)" in MINGW*|MSYS*|CYGWIN*) ;; *) [ "$(stat -c %a "$f")" = 600 ] || fail "$f is not mode 600" ;; esac
done
echo "source row counts ($(wc -l < "$work/source.counts") public tables):"; cat "$work/source.counts"

step "the source host is lost; restore on the fresh host with the saved .env"
kill "$source_pid"; wait "$source_pid" 2>/dev/null || true
"$here/restore.sh" --dump "$dump" --container "$dst"
saved_env="${dump%.dump}.env"
jwt_saved="$(grep '^JWT_SECRET=' "$saved_env" | cut -d= -f2-)"
start_backend restored "$((api_base + 1))" "$dst_port" "$jwt_saved"
restored_pid="$last_pid"

step "assertions (fresh host)"
"$here/rowcounts.sh" --container "$dst" > "$work/restored.counts"
diff -u "$work/source.counts" "$work/restored.counts" || fail "row counts differ"
echo "OK row counts equal for all $(wc -l < "$work/restored.counts") public tables"
[ "$(psql_in "$dst" "$cred_query")" = "rehearsal-access-token/rehearsal-refresh-token" ] || fail "the stored provider credential did not survive the restore"
echo "OK the stored provider credential (oauth_tokens) restored intact"
[ "$(health "$((api_base + 1))")" = 200 ] || fail "restored backend not healthy"
echo "OK restored backend /api/health 200 (readiness check: added by Part A)"
got="$(me_status "$((api_base + 1))" "$token")"
[ "$got" = 200 ] || fail "the pre-backup session got $got on the restored stack"
echo "OK pre-backup session cookie accepted on the restored stack (/api/auth/me 200)"
kill "$restored_pid"; wait "$restored_pid" 2>/dev/null || true

step "negative controls (fresh host)"
start_backend wrong-secret "$((api_base + 2))" "$dst_port" "$(openssl rand -hex 32)"
wrong_pid="$last_pid"
got="$(me_status "$((api_base + 2))" "$token")"
[ "$got" = 401 ] || fail "with another JWT_SECRET the session got $got, want 401"
echo "OK with a different JWT_SECRET the same cookie gets 401 (the .env must travel with the dump)"
kill "$wrong_pid"; wait "$wrong_pid" 2>/dev/null || true

if "$here/restore.sh" --dump "$dump" --container "$dst" 2>/dev/null; then fail "restore.sh accepted a non-empty database"; fi
echo "OK restore.sh refuses a database that already has tables"
mkdir "$work/nosum"; cp "$dump" "$work/nosum/"
nosum_dump="$work/nosum/$(basename "$dump")"
out="$("$here/restore.sh" --dump "$nosum_dump" --container "$dst" 2>&1)" && fail "restore.sh accepted a dump without its .sha256"
echo "$out" | grep -q "REFUSED: .*sha256 is missing" || fail "restore.sh refused without naming the missing checksum: $out"
out="$("$here/restore.sh" --dump "$nosum_dump" --container "$dst" --allow-missing-checksum 2>&1)" && fail "restore.sh restored over a non-empty database"
echo "$out" | grep -q "WARNING: .*NOT verified" || fail "--allow-missing-checksum did not warn: $out"
echo "OK restore.sh refuses a dump without its .sha256, and --allow-missing-checksum proceeds with a warning"

mkdir "$work/shared"; : > "$work/shared/notes.txt"
if "$here/backup.sh" --out "$work/shared" --env-file "$work/.env" --compose-file "$compose_file" >/dev/null 2>&1; then fail "backup.sh wrote into a shared directory"; fi
[ "$(ls -A "$work/shared")" = notes.txt ] || fail "backup.sh changed a directory it refused"
echo "OK backup.sh refuses a directory holding other files"

psql_src "INSERT INTO organizations (name, domain) VALUES ('After backup', 'after.example.com')" >/dev/null
"$here/rowcounts.sh" --compose-file "$compose_file" > "$work/source-later.counts"
if diff -q "$work/source-later.counts" "$work/restored.counts" >/dev/null; then fail "the count comparison missed a row added after the backup"; fi
echo "OK a row added to the source after the backup makes the comparison differ"

old="$backups/paceday-20000101T000000Z"
: > "$old.dump"; : > "$old.env"
( cd "$backups" && sha256sum "$(basename "$old").dump" "$(basename "$old").env" > "$(basename "$old").sha256" )
orphan="$backups/paceday-20000102T000000Z.dump"; : > "$orphan" # no .sha256: not a set the script wrote
for f in "$old.dump" "$old.env" "$old.sha256" "$orphan"; do touch -d '15 days ago' "$f"; done
sleep 1 # a new timestamp for the next backup set
"$here/backup.sh" --out "$backups" --env-file "$work/.env" --compose-file "$compose_file" > /dev/null
[ ! -e "$old.dump" ] && [ ! -e "$old.env" ] && [ ! -e "$old.sha256" ] || fail "retention kept a complete set older than 14 days"
[ -f "$orphan" ] || fail "retention deleted a file that is not part of a set it wrote"
[ -f "$dump" ] || fail "retention deleted the fresh backup"
echo "OK retention removed a 15-day-old set it wrote, kept an old file it did not write, kept today's"

# ── Part 2 ────────────────────────────────────────────────────────────────────
step "same host: pre-deploy backup, then a failed migration"
start_backend source-again "$api_base" "$src_port" "$JWT_SECRET"
pre_pid="$last_pid"
sleep 1
predeploy="$("$here/backup.sh" --out "$backups" --env-file "$work/.env" --compose-file "$compose_file")"
"$here/rowcounts.sh" --compose-file "$compose_file" > "$work/predeploy.counts"
kill "$pre_pid"; wait "$pre_pid" 2>/dev/null || true
# What a migration that failed halfway leaves: part of its schema, and
# golang-migrate's record of the version marked dirty.
psql_src "CREATE TABLE half_applied_032 (id int); UPDATE schema_migrations SET version = 32, dirty = true" >/dev/null
echo "simulated: migration 032 failed halfway (table half_applied_032, schema_migrations 32 dirty)"

launch_backend dirty "$((api_base + 3))" "$src_port" "$JWT_SECRET"
dirty_pid="$last_pid"
for _ in $(seq 1 60); do kill -0 "$dirty_pid" 2>/dev/null || break; sleep 1; done
kill -0 "$dirty_pid" 2>/dev/null && fail "the backend kept running on a dirty database"
wait "$dirty_pid" 2>/dev/null && fail "the backend exited 0 on a dirty database"
grep -qi "dirty" "$work/dirty.log" || fail "the backend's log does not name the dirty database: $(tail -3 "$work/dirty.log")"
echo "OK the backend refuses to start: $(grep -i -m1 dirty "$work/dirty.log")"
if "$here/restore.sh" --dump "$predeploy" --compose-file "$compose_file" 2>/dev/null; then fail "restore.sh restored over the failed database"; fi
echo "OK restore.sh refuses the half-migrated database (the volume must be set aside first)"

step "runbook: stop the stack, set the volume aside, restore, deploy the previous version"
docker compose -f "$compose_file" down >/dev/null 2>&1
set_aside="$("$here/set-aside-volume.sh" --compose-file "$compose_file")"
docker volume inspect "${project}_postgres_data" >/dev/null 2>&1 && fail "the original volume still exists"
docker compose -f "$compose_file" up -d postgres >/dev/null 2>&1
wait_pg "$(src_container)"
"$here/restore.sh" --dump "$predeploy" --compose-file "$compose_file"
start_backend previous-version "$api_base" "$src_port" "$JWT_SECRET"

step "assertions (same host)"
"$here/rowcounts.sh" --compose-file "$compose_file" > "$work/rolledback.counts"
diff -u "$work/predeploy.counts" "$work/rolledback.counts" || fail "row counts differ from the pre-deploy backup"
echo "OK row counts equal the pre-deploy backup's for all $(wc -l < "$work/rolledback.counts") public tables"
[ "$(health "$api_base")" = 200 ] || fail "rolled-back backend not healthy"
echo "OK rolled-back backend /api/health 200"
got="$(me_status "$api_base" "$token")"
[ "$got" = 200 ] || fail "the session got $got after the rollback"
echo "OK the session cookie still works after the rollback"
[ "$(psql_src "SELECT to_regclass('public.half_applied_032') IS NULL")" = t ] || fail "the half-applied table survived the rollback"
docker run -d --name "$run_id-inspect" -v "$set_aside:/var/lib/postgresql/data" "$pg_image" >/dev/null
wait_pg "$run_id-inspect"
[ "$(psql_in "$run_id-inspect" "SELECT version || ' ' || dirty FROM schema_migrations")" = "32 true" ] || fail "the set-aside copy does not hold the failed database"
echo "OK the set-aside volume $set_aside still holds the failed database (schema_migrations 32, dirty)"

printf '\nREHEARSAL PASSED %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
