#!/usr/bin/env bash
# Paceday restore (PAC-81 Part D, AC-D3; docs/runbook.md "Restore").
#
# Restores a dump written by backup.sh into an EMPTY database: a fresh host,
# or a fresh postgres volume. Start only the postgres service first, with the
# .env saved beside the dump (same POSTGRES_PASSWORD), then run this, then
# start the backend at the SAME image version and the rest of the stack.
#
# It refuses to restore over a database that already has tables in the public
# schema: restoring is never a way to "merge" into a live database.
#
# Usage:
#   scripts/ops/restore.sh --dump FILE
#                          [--compose-file FILE | --container NAME]
#                          [--db NAME] [--user NAME] [--wait-seconds N]
#
# Defaults: --compose-file docker-compose.prod.yml (its postgres service),
# --db clockwise, --user clockwise. The checksum in FILE's .sha256 (written by
# backup.sh beside it) is verified first. Without that file the script
# refuses; --allow-missing-checksum restores anyway, after a warning.
#
# A Postgres container on a new volume first initialises it with a temporary
# server that listens only on its Unix socket, then restarts. The script waits
# (default 120 s) until the server accepts TCP connections inside the
# container, which only the final server does, before it touches the
# database.
set -euo pipefail
export MSYS_NO_PATHCONV=1 # Git Bash: do not rewrite /paths passed to docker

dump="" compose_file="docker-compose.prod.yml" container="" db=clockwise user=clockwise allow_missing_checksum=0 wait_seconds=120
while [ $# -gt 0 ]; do
  case "$1" in
    --dump) dump="$2"; shift 2 ;;
    --compose-file) compose_file="$2"; shift 2 ;;
    --container) container="$2"; shift 2 ;;
    --db) db="$2"; shift 2 ;;
    --user) user="$2"; shift 2 ;;
    --allow-missing-checksum) allow_missing_checksum=1; shift ;;
    --wait-seconds) wait_seconds="$2"; shift 2 ;;
    -h|--help) sed -n '2,28p' "$0"; exit 0 ;;
    *) echo "restore: unknown argument $1" >&2; exit 2 ;;
  esac
done
[ -n "$dump" ] && [ -f "$dump" ] || { echo "restore: --dump FILE is required and must exist" >&2; exit 2; }

sums="${dump%.dump}.sha256"
if [ -f "$sums" ]; then
  ( cd "$(dirname "$dump")" && grep -E "[ *]$(basename "$dump")$" "$(basename "$sums")" | sha256sum -c --quiet - ) \
    || { echo "restore: checksum mismatch for $dump" >&2; exit 1; }
elif [ "$allow_missing_checksum" = 1 ]; then
  echo "restore: WARNING: $sums is missing; the dump's integrity is NOT verified (--allow-missing-checksum given)" >&2
else
  echo "restore: REFUSED: $sums is missing, so the dump cannot be verified." >&2
  echo "restore: find the .sha256 written beside it by backup.sh, or rerun with --allow-missing-checksum to restore unverified." >&2
  exit 1
fi

if [ -z "$container" ]; then
  container="$(docker compose -f "$compose_file" ps -q postgres)"
  [ -n "$container" ] || { echo "restore: no running postgres service in $compose_file" >&2; exit 1; }
fi

case "$wait_seconds" in ''|*[!0-9]*) echo "restore: --wait-seconds must be a number" >&2; exit 2 ;; esac
waited=0
until docker exec "$container" pg_isready -q -h 127.0.0.1 -U "$user" -d "$db" >/dev/null 2>&1; do
  if [ "$waited" -ge "$wait_seconds" ]; then
    echo "restore: Postgres in $container did not accept TCP connections within ${wait_seconds}s" >&2
    exit 1
  fi
  [ "$waited" -gt 0 ] || echo "restore: waiting for Postgres to finish starting…" >&2
  sleep 1; waited=$((waited + 1))
done

tables="$(docker exec "$container" psql -U "$user" -d "$db" -Atc "SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public'")"
if [ "$tables" != "0" ]; then
  echo "restore: database $db already has $tables tables in public; restore only into an empty database (fresh volume)" >&2
  exit 1
fi

docker exec -i "$container" pg_restore -U "$user" -d "$db" --no-owner --exit-on-error --single-transaction < "$dump"
echo "restore: $dump restored into $db" >&2
