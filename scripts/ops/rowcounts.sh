#!/usr/bin/env bash
# Exact row count of every table in the public schema, one "table count" per
# line, sorted (PAC-81 Part D, AC-D3). Run it on the source and on the restored
# database and compare: `diff source.counts restored.counts` must print nothing.
#
# Usage:
#   scripts/ops/rowcounts.sh [--compose-file FILE | --container NAME] [--db NAME] [--user NAME]
set -euo pipefail
export MSYS_NO_PATHCONV=1

compose_file="docker-compose.prod.yml" container="" db=clockwise user=clockwise
while [ $# -gt 0 ]; do
  case "$1" in
    --compose-file) compose_file="$2"; shift 2 ;;
    --container) container="$2"; shift 2 ;;
    --db) db="$2"; shift 2 ;;
    --user) user="$2"; shift 2 ;;
    *) echo "rowcounts: unknown argument $1" >&2; exit 2 ;;
  esac
done
if [ -z "$container" ]; then
  container="$(docker compose -f "$compose_file" ps -q postgres)"
  [ -n "$container" ] || { echo "rowcounts: no running postgres service in $compose_file" >&2; exit 1; }
fi

docker exec "$container" psql -U "$user" -d "$db" -v ON_ERROR_STOP=1 -At -F ' ' -c "
SELECT table_name,
       (xpath('/row/c/text()',
              query_to_xml(format('SELECT count(*) AS c FROM %I.%I', table_schema, table_name), false, true, '')))[1]::text::bigint
FROM information_schema.tables
WHERE table_schema = 'public' AND table_type = 'BASE TABLE'
ORDER BY table_name"
