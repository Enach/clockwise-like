#!/usr/bin/env bash
# Paceday backup (PAC-81 Part D, AC-D3; docs/runbook.md "Backups").
#
# Takes a consistent dump of the running database WITHOUT stopping the backend
# (pg_dump reads one snapshot), saves the .env secrets beside it, and deletes
# backups older than the retention. Run it daily (owner default Q-O3: daily,
# kept 14 days, so up to 24 h of data can be lost).
#
# THE OUTPUT IS SECRET. A dump contains every user's stored provider
# credentials (OAuth tokens, LLM API keys) in plain columns, and the .env copy
# holds JWT_SECRET, POSTGRES_PASSWORD and the OAuth client secrets. Files are
# written mode 600; store and ship them as secrets.
#
# Usage:
#   scripts/ops/backup.sh --out DIR --env-file PATH
#                         [--compose-file FILE | --container NAME]
#                         [--keep-days N] [--db NAME] [--user NAME]
#
# Defaults: --compose-file docker-compose.prod.yml (its postgres service),
# --keep-days 14, --db clockwise, --user clockwise.
#
# DIR must be dedicated to these backups: the script refuses a directory that
# holds anything other than its own paceday-<T>.* files, then makes it mode
# 700. Retention deletes only complete sets this script wrote: a
# paceday-<T>.sha256 listing exactly paceday-<T>.dump and paceday-<T>.env,
# older than --keep-days, together with those two files. Nothing else in DIR
# is ever deleted, except a paceday-<T>.dump.partial left by an interrupted
# run, once it is older than --keep-days.
#
# Writes, for one UTC timestamp T:
#   DIR/paceday-T.dump    pg_dump custom format (restore with restore.sh)
#   DIR/paceday-T.env     the .env file as it was
#   DIR/paceday-T.sha256  checksums of both
# and prints the dump path on stdout.
set -euo pipefail
umask 077
export MSYS_NO_PATHCONV=1 # Git Bash: do not rewrite /paths passed to docker

out="" env_file="" compose_file="docker-compose.prod.yml" container="" keep_days=14 db=clockwise user=clockwise
while [ $# -gt 0 ]; do
  case "$1" in
    --out) out="$2"; shift 2 ;;
    --env-file) env_file="$2"; shift 2 ;;
    --compose-file) compose_file="$2"; shift 2 ;;
    --container) container="$2"; shift 2 ;;
    --keep-days) keep_days="$2"; shift 2 ;;
    --db) db="$2"; shift 2 ;;
    --user) user="$2"; shift 2 ;;
    -h|--help) sed -n '2,27p' "$0"; exit 0 ;;
    *) echo "backup: unknown argument $1" >&2; exit 2 ;;
  esac
done
[ -n "$out" ] || { echo "backup: --out DIR is required" >&2; exit 2; }
[ -n "$env_file" ] || { echo "backup: --env-file PATH is required (a dump without its secrets does not restore a working stack)" >&2; exit 2; }
[ -f "$env_file" ] || { echo "backup: $env_file not found" >&2; exit 2; }
case "$keep_days" in ''|*[!0-9]*) echo "backup: --keep-days must be a positive integer" >&2; exit 2 ;; esac
[ "$keep_days" -ge 1 ] || { echo "backup: --keep-days must be at least 1" >&2; exit 2; }

if [ -z "$container" ]; then
  container="$(docker compose -f "$compose_file" ps -q postgres)"
  [ -n "$container" ] || { echo "backup: no running postgres service in $compose_file" >&2; exit 1; }
fi

own_file='^paceday-[0-9]{8}T[0-9]{6}Z\.(dump|env|sha256|dump\.partial)$'
if [ -e "$out" ]; then
  [ -d "$out" ] || { echo "backup: $out exists and is not a directory" >&2; exit 2; }
  foreign="$(ls -A "$out" | grep -Ev "$own_file" || true)"
  if [ -n "$foreign" ]; then
    echo "backup: $out is not a dedicated backup directory; it holds:" >&2
    printf '%s\n' "$foreign" | sed 's/^/  /' >&2
    echo "backup: use an empty or backup-only directory (the script chmods it 700 and applies retention in it)" >&2
    exit 2
  fi
else
  mkdir -p "$out"
fi
chmod 700 "$out" 2>/dev/null || true
stamp="$(date -u +%Y%m%dT%H%M%SZ)"
base="$out/paceday-$stamp"
[ ! -e "$base.dump" ] || { echo "backup: $base.dump already exists" >&2; exit 1; }

# One snapshot, taken while the backend keeps running.
docker exec "$container" pg_dump -U "$user" -d "$db" --format=custom --no-owner > "$base.dump.partial"
# A dump that pg_restore cannot list is not a backup.
docker exec -i "$container" pg_restore --list < "$base.dump.partial" > /dev/null
mv "$base.dump.partial" "$base.dump"
cp "$env_file" "$base.env"
chmod 600 "$base.dump" "$base.env"
( cd "$out" && sha256sum "paceday-$stamp.dump" "paceday-$stamp.env" > "paceday-$stamp.sha256" )
chmod 600 "$base.sha256"

# Retention: delete complete sets this script wrote (a .sha256 naming exactly
# its own .dump and .env) whose .sha256 is older than keep_days days. The set
# just written is never older than a minute, so it is never deleted.
find "$out" -maxdepth 1 -type f -name 'paceday-*.sha256' -mtime +"$((keep_days - 1))" | while IFS= read -r sums; do
  set_base="${sums%.sha256}"
  name="$(basename "$set_base")"
  [[ "$name" =~ ^paceday-[0-9]{8}T[0-9]{6}Z$ ]] || continue
  listed="$(sed -E 's/^[0-9a-f]{64} [ *]//' "$sums" | sort | tr '\n' ' ')"
  [ "$listed" = "$name.dump $name.env " ] || { echo "backup: retention skipped $sums (not a set this script wrote)" >&2; continue; }
  rm -f -- "$set_base.dump" "$set_base.env" "$sums"
  echo "backup: retention removed set $name" >&2
done

# An interrupted run leaves paceday-<T>.dump.partial; same name rule, same age.
find "$out" -maxdepth 1 -type f -name 'paceday-*.dump.partial' -mtime +"$((keep_days - 1))" | while IFS= read -r partial; do
  [[ "$(basename "$partial")" =~ ^paceday-[0-9]{8}T[0-9]{6}Z\.dump\.partial$ ]] || continue
  rm -f -- "$partial"
  echo "backup: retention removed interrupted dump $(basename "$partial")" >&2
done

echo "$base.dump"
