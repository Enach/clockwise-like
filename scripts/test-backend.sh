#!/usr/bin/env bash
set -euo pipefail

# Git Bash (MSYS) rewrites POSIX-looking arguments for native Windows programs,
# so `-w /app` reaches docker as `C:/Program Files/Git/app` and docker refuses
# it. Docker Desktop accepts the unconverted /c/... mount source. No effect
# elsewhere.
export MSYS_NO_PATHCONV=1

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DB_CONTAINER="paceday-test-postgres-$$"
DB_PORT="${PACEDAY_TEST_DB_PORT:-15432}"

cleanup() {
  docker rm -f "$DB_CONTAINER" >/dev/null 2>&1 || true
}
trap cleanup EXIT

# PAC-148 (contract md §2.2): the suite runs on the production PostgreSQL
# image, read from docker-compose.prod.yml's postgres service, so the
# organisation domain expression (storage.addrDomainSQL, whose lower() follows
# the server's ctype) is tested where it runs.
# TestPAC148_AddrDomainSQLEqualsExtractDomain compares server_version with the
# same tag.
PG_IMAGE="$(sed -n 's/^[[:space:]]*image:[[:space:]]*\(postgres:[0-9][0-9.]*-alpine\)[[:space:]]*$/\1/p' "$ROOT_DIR/docker-compose.prod.yml")"
case "$PG_IMAGE" in
  postgres:*-alpine) ;;
  *)
    echo "docker-compose.prod.yml must name exactly one postgres:<tag>-alpine image (found: '$PG_IMAGE')" >&2
    exit 1
    ;;
esac
case "$PG_IMAGE" in
  *"
"*)
    echo "docker-compose.prod.yml names more than one postgres:<tag>-alpine image" >&2
    exit 1
    ;;
esac

docker run -d --name "$DB_CONTAINER" \
  -e POSTGRES_DB=testdb \
  -e POSTGRES_USER=test \
  -e POSTGRES_PASSWORD=test \
  -p "127.0.0.1:${DB_PORT}:5432" \
  "$PG_IMAGE" >/dev/null

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

# Optional host directory for the container's Go module and build caches
# (ci.yml restores and saves it with actions/cache). Unset, the container starts
# cold as it always has. Set, `-count=1` is added so the build cache can only
# skip compilation, never replay a test result instead of running the test.
CACHE_ARGS=()
if [ -n "${PACEDAY_GO_CACHE_DIR:-}" ]; then
  mkdir -p "$PACEDAY_GO_CACHE_DIR/mod" "$PACEDAY_GO_CACHE_DIR/build"
  CACHE_ARGS=(-v "$PACEDAY_GO_CACHE_DIR:/gocache" -e GOMODCACHE=/gocache/mod -e GOCACHE=/gocache/build)
  GO_TEST_FLAGS="-count=1 $GO_TEST_FLAGS"
fi

# The whole repository is mounted, with backend/ as the working directory, not
# backend/ alone: the PAC-21 contract test (backend/api/pac21_contract_5xx_test.go)
# reads contracts/openapi/openapi.yaml, and the PAC-139 deny-all proxy is built
# from tools/denyproxy. scripts/test-backend-in-container.sh downloads modules
# first, then runs `go test` with HTTP(S)_PROXY pointed at that proxy, and
# fails the run if any test tried to reach a real host through it.
docker run --rm --network host \
  -e "PACEDAY_TEST_DATABASE_URL=postgres://test:test@127.0.0.1:${DB_PORT}/testdb?sslmode=disable" \
  -e "GO_TEST_FLAGS=$GO_TEST_FLAGS" \
  ${CACHE_ARGS[@]+"${CACHE_ARGS[@]}"} \
  -v "$ROOT_DIR:/repo" \
  -w /repo/backend \
  golang:1.26-alpine \
  sh /repo/scripts/test-backend-in-container.sh
