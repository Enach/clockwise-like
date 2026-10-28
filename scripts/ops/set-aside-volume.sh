#!/usr/bin/env bash
# Set the database volume aside before a same-host restore (PAC-81 Part D;
# docs/runbook.md "Rollback on the same host").
#
# After a failed migration the database is half-migrated (or marked dirty):
# the backend refuses to start against it, and restore.sh refuses to restore
# over it. This script keeps that database for later investigation and frees
# the volume name for a clean restore:
#
#   1. refuses while any container still uses the volume (stop the stack
#      first: docker compose -f <file> down — without -v);
#   2. copies the volume into a new volume <volume>-failed-<UTC timestamp>
#      and checks that both hold the same files and bytes;
#   3. removes the original volume, so the next `docker compose up -d
#      postgres` creates an empty one for restore.sh.
#
# Docker cannot rename a volume, hence copy-then-remove. The copy is never
# deleted by this script; remove it yourself once it is no longer needed
# (docker volume rm <name>) — it holds the same secrets as a dump.
#
# Usage:
#   scripts/ops/set-aside-volume.sh [--compose-file FILE] [--volume KEY] [--copy-name NAME]
#
# Defaults: --compose-file docker-compose.prod.yml, --volume postgres_data
# (the key under `volumes:`). The Docker volume is <project>_<KEY>, where
# <project> is the compose project name (`docker compose -f FILE config`
# prints it as `name:`; by default the directory holding FILE). Prints the
# name of the copy on stdout. The copy's name defaults to
# <volume>-failed-<UTC timestamp>; the script refuses if a volume with that
# name already exists, so it never writes into a volume it did not create.
set -euo pipefail
export MSYS_NO_PATHCONV=1

compose_file="docker-compose.prod.yml" key="postgres_data" copy=""
while [ $# -gt 0 ]; do
  case "$1" in
    --compose-file) compose_file="$2"; shift 2 ;;
    --volume) key="$2"; shift 2 ;;
    --copy-name) copy="$2"; shift 2 ;;
    -h|--help) sed -n '2,31p' "$0"; exit 0 ;;
    *) echo "set-aside: unknown argument $1" >&2; exit 2 ;;
  esac
done

project="$(docker compose -f "$compose_file" config 2>/dev/null | awk '/^name:/ {print $2; exit}')"
[ -n "$project" ] || { echo "set-aside: cannot read the compose project name from $compose_file" >&2; exit 1; }
volume="${project}_${key}"
docker volume inspect "$volume" >/dev/null 2>&1 || { echo "set-aside: volume $volume does not exist" >&2; exit 1; }

users="$(docker ps -a --filter "volume=$volume" --format '{{.Names}}')"
if [ -n "$users" ]; then
  echo "set-aside: volume $volume is still used by: $users" >&2
  echo "set-aside: stop the stack first: docker compose -f $compose_file down   (never with -v)" >&2
  exit 1
fi

[ -n "$copy" ] || copy="${volume}-failed-$(date -u +%Y%m%dT%H%M%SZ)"
if docker volume inspect "$copy" >/dev/null 2>&1; then
  echo "set-aside: volume $copy already exists; refusing to write into it ($volume left untouched)" >&2
  exit 1
fi
image="${SET_ASIDE_IMAGE:-postgres:16-alpine}" # any image with sh, cp, find, md5sum; the stack already has this one
docker volume create --label "paceday.set-aside-from=$volume" "$copy" >/dev/null
docker run --rm -v "$volume:/from:ro" -v "$copy:/to" "$image" sh -c 'cp -a /from/. /to/'
# Same entries, same content: checksum of every file's checksum, plus the
# count of every entry (directories, links included).
summary='cd "$1" && echo "$(find . | wc -l) entries, content $(find . -type f -exec md5sum {} + | sort -k2 | md5sum | cut -c1-12)"'
src_sum="$(docker run --rm -v "$volume:/v:ro" "$image" sh -c "$summary" _ /v)"
dst_sum="$(docker run --rm -v "$copy:/v:ro" "$image" sh -c "$summary" _ /v)"
if [ "$src_sum" != "$dst_sum" ]; then
  echo "set-aside: copy differs ($volume: $src_sum, $copy: $dst_sum); $volume NOT removed" >&2
  exit 1
fi
docker volume rm "$volume" >/dev/null
echo "set-aside: $volume copied to $copy ($dst_sum) and removed" >&2
echo "$copy"
