#!/usr/bin/env bash
# Backup/restore rehearsal (PAC-81 Part D, AC-D3) — the integration test for
# backup.sh, restore.sh and rowcounts.sh. It needs Docker, Go, curl and
# openssl, and touches nothing but what it creates.
#
#   1. Two throwaway Postgres containers ("source host" and "fresh host").
#   2. The backend built from this tree, run against the source with a
#      generated .env; a user seeded and a session cookie issued BEFORE the
#      backup, checked to work.
#   3. backup.sh against the running source (the backend keeps running).
#   4. restore.sh into the fresh host; a second backend started with the .env
#      saved beside the dump.
#   5. Asserts: health 200; row counts of every public table equal; the
#      pre-backup cookie accepted (/api/auth/me 200).
#   6. Negative controls: the cookie against a backend with another JWT_SECRET
#      gets 401; a row added to the source after the backup makes the count
#      comparison differ; retention deletes a backup set older than 14 days
#      and keeps the new one.
#
# Usage: scripts/ops/rehearse-backup-restore.sh
# Ports (override with env): REHEARSAL_PG_SRC_PORT=15492 REHEARSAL_PG_DST_PORT=15493
#                            REHEARSAL_API_PORT_BASE=18091 (uses +0, +1, +2)
# Postgres image: REHEARSAL_PG_IMAGE=postgres:16-alpine (the prod compose's).
set -euo pipefail
# Path conversion stays on here (git, go and the backend binary need it); the
# ops scripts it calls turn it off for their docker calls.

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "$here/../.." && pwd)"
src_port="${REHEARSAL_PG_SRC_PORT:-15492}"
dst_port="${REHEARSAL_PG_DST_PORT:-15493}"
api_base="${REHEARSAL_API_PORT_BASE:-18091}"
pg_image="${REHEARSAL_PG_IMAGE:-postgres:16-alpine}"
for p in "$src_port" "$dst_port"; do
  case "$p" in 15432|15433) echo "rehearsal: port $p is reserved for the factory gate" >&2; exit 2 ;; esac
done

run_id="pac81-rehearsal-$$"
src="$run_id-src" dst="$run_id-dst"
work="$(mktemp -d)"
pids=()
cleanup() {
  for pid in "${pids[@]:-}"; do [ -n "$pid" ] && kill "$pid" 2>/dev/null || true; done
  docker rm -f "$src" "$dst" >/dev/null 2>&1 || true
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

step "two Postgres containers: source :$src_port, fresh host :$dst_port ($pg_image)"
for spec in "$src:$src_port" "$dst:$dst_port"; do
  docker run -d --name "${spec%%:*}" -e POSTGRES_DB=clockwise -e POSTGRES_USER=clockwise \
    -e POSTGRES_PASSWORD="$POSTGRES_PASSWORD" -p "127.0.0.1:${spec##*:}:5432" "$pg_image" >/dev/null
done
wait_pg() {
  for _ in $(seq 1 60); do
    docker exec "$1" pg_isready -U clockwise -d clockwise -h 127.0.0.1 >/dev/null 2>&1 && return 0
    sleep 1
  done
  fail "postgres $1 not ready"
}
wait_pg "$src"; wait_pg "$dst"

step "build the backend from this tree ($(git -C "$root" rev-parse --short HEAD))"
exe="$work/paceday-backend"; case "$(uname -s)" in MINGW*|MSYS*|CYGWIN*) exe="$exe.exe" ;; esac
( cd "$root/backend" && go build -o "$exe" . )

start_backend() { # name port db_port jwt_secret
  local log="$work/$1.log"
  # A local, unreachable DSN keeps the rehearsal from reporting to any real
  # Sentry project (before Part A an empty DSN falls back to a built-in one).
  env -i PATH="$PATH" SYSTEMROOT="${SYSTEMROOT:-}" TEMP="${TEMP:-/tmp}" \
    PORT="$2" DATABASE_URL="postgres://clockwise:$POSTGRES_PASSWORD@127.0.0.1:$3/clockwise?sslmode=disable" \
    JWT_SECRET="$4" SENTRY_DSN="http://rehearsal@127.0.0.1:9/1" SENTRY_ENVIRONMENT=rehearsal \
    "$exe" > "$log" 2>&1 &
  pids+=("$!")
  for _ in $(seq 1 90); do
    [ "$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$2/api/health")" = 200 ] && return 0
    sleep 1
  done
  tail -20 "$log" >&2
  fail "backend $1 never answered /api/health"
}
me_status() { # port token
  curl -s -o /dev/null -w '%{http_code}' --cookie "auth_token=$2" "http://127.0.0.1:$1/api/auth/me"
}
psql_src() { docker exec "$src" psql -U clockwise -d clockwise -v ON_ERROR_STOP=1 -Atc "$1"; }

step "source backend :$api_base (runs migrations), seed data, issue a session"
start_backend source "$api_base" "$src_port" "$JWT_SECRET"
uid="$(psql_src "INSERT INTO users (email, name) VALUES ('rehearsal-owner@example.com', 'Rehearsal Owner') RETURNING id" | head -1)"
psql_src "INSERT INTO users (email, name) VALUES ('rehearsal-member@example.com', 'Rehearsal Member')" >/dev/null
psql_src "INSERT INTO organizations (name, domain) VALUES ('Rehearsal', 'rehearsal.example.com')" >/dev/null
psql_src "UPDATE users SET org_id = (SELECT id FROM organizations WHERE domain = 'rehearsal.example.com')" >/dev/null
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
dump="$("$here/backup.sh" --out "$work/backups" --env-file "$work/.env" --container "$src")"
"$here/rowcounts.sh" --container "$src" > "$work/source.counts"
kill -0 "${pids[0]}" || fail "the source backend stopped during the backup"
[ "$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$api_base/api/health")" = 200 ] || fail "source not healthy after backup"
ls -l "$work/backups"
for f in "$dump" "${dump%.dump}.env" "${dump%.dump}.sha256"; do
  [ -f "$f" ] || fail "missing $f"
  case "$(uname -s)" in MINGW*|MSYS*|CYGWIN*) ;; *) [ "$(stat -c %a "$f")" = 600 ] || fail "$f is not mode 600" ;; esac
done
echo "source row counts ($(wc -l < "$work/source.counts") public tables):"; cat "$work/source.counts"

step "the source host is lost; restore on the fresh host with the saved .env"
kill "${pids[0]}"; wait "${pids[0]}" 2>/dev/null || true
"$here/restore.sh" --dump "$dump" --container "$dst"
if "$here/restore.sh" --dump "$dump" --container "$dst" 2>/dev/null; then fail "restore.sh accepted a non-empty database"; fi
echo "a second restore into the now non-empty database was refused (expected)"
saved_env="${dump%.dump}.env"
jwt_saved="$(grep '^JWT_SECRET=' "$saved_env" | cut -d= -f2-)"
start_backend restored "$((api_base + 1))" "$dst_port" "$jwt_saved"

step "assertions"
"$here/rowcounts.sh" --container "$dst" > "$work/restored.counts"
diff -u "$work/source.counts" "$work/restored.counts" || fail "row counts differ"
echo "OK row counts equal for all $(wc -l < "$work/restored.counts") public tables"
[ "$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$((api_base + 1))/api/health")" = 200 ] || fail "restored backend not healthy"
echo "OK restored backend /api/health 200 (readiness check: added by Part A)"
got="$(me_status "$((api_base + 1))" "$token")"
[ "$got" = 200 ] || fail "the pre-backup session got $got on the restored stack"
echo "OK pre-backup session cookie accepted on the restored stack (/api/auth/me 200)"

step "negative controls"
start_backend wrong-secret "$((api_base + 2))" "$dst_port" "$(openssl rand -hex 32)"
got="$(me_status "$((api_base + 2))" "$token")"
[ "$got" = 401 ] || fail "with another JWT_SECRET the session got $got, want 401"
echo "OK with a different JWT_SECRET the same cookie gets 401 (the .env must travel with the dump)"
psql_src "INSERT INTO organizations (name, domain) VALUES ('After backup', 'after.example.com')" >/dev/null
"$here/rowcounts.sh" --container "$src" > "$work/source-later.counts"
if diff -q "$work/source-later.counts" "$work/restored.counts" >/dev/null; then fail "the count comparison missed a row added after the backup"; fi
echo "OK a row added to the source after the backup makes the comparison differ"
old="$work/backups/paceday-20000101T000000Z"
for ext in dump env sha256; do : > "$old.$ext"; touch -d '15 days ago' "$old.$ext"; done
sleep 1 # a new timestamp for the second backup set
"$here/backup.sh" --out "$work/backups" --env-file "$work/.env" --container "$src" > /dev/null
[ ! -e "$old.dump" ] || fail "retention kept a backup older than 14 days"
[ -f "$dump" ] || fail "retention deleted the fresh backup"
echo "OK retention removed a 15-day-old set and kept today's"

printf '\nREHEARSAL PASSED %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
