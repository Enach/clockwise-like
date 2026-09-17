#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MODULE_DIR="$ROOT_DIR/backend"
DB_CONTAINER="paceday-test-postgres-$$"
DB_PORT="${PACEDAY_TEST_DB_PORT:-15432}"

cleanup() {
  docker rm -f "$DB_CONTAINER" >/dev/null 2>&1 || true
}
trap cleanup EXIT

docker run -d --name "$DB_CONTAINER" \
  -e POSTGRES_DB=testdb \
  -e POSTGRES_USER=test \
  -e POSTGRES_PASSWORD=test \
  -p "127.0.0.1:${DB_PORT}:5432" \
  postgres:16-alpine >/dev/null

ready=0
for _ in $(seq 1 30); do
  if docker exec "$DB_CONTAINER" pg_isready -U test -d testdb >/dev/null 2>&1; then
    ready=1
    break
  fi
  sleep 1
done
if [ "$ready" -ne 1 ]; then
  echo "test PostgreSQL did not become ready" >&2
  exit 1
fi

# Extra `go test` flags, e.g. the coverage profile `make coverage` asks for.
# Empty by default, so a bare run of this script behaves exactly as before.
# Word-split deliberately inside the container, hence the unquoted expansion there.
GO_TEST_FLAGS="${PACEDAY_GO_TEST_FLAGS:-}"

docker run --rm --network host \
  -e "PACEDAY_TEST_DATABASE_URL=postgres://test:test@127.0.0.1:${DB_PORT}/testdb?sslmode=disable" \
  -e "GO_TEST_FLAGS=$GO_TEST_FLAGS" \
  -v "$MODULE_DIR:/app" \
  -w /app \
  golang:1.25-alpine \
  sh -c 'go test -p 1 $GO_TEST_FLAGS ./...'
