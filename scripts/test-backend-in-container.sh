#!/bin/sh
# Run by scripts/test-backend.sh inside golang:1.26-alpine, with the repository
# mounted at /repo and backend/ as the working directory. POSIX sh: the image
# has no bash.
#
# PAC-139: the suite runs behind a deny-all proxy (tools/denyproxy). Any test
# whose code calls a real host through the environment's proxy gets a 403, and
# the run fails when the proxy recorded anything, even if every test passed.
set -eu

# Module and toolchain downloads happen here, before any proxy variable is set:
# GOPROXY traffic is not the suite's and must not be denied.
go mod download

workdir="$(mktemp -d)"
# The proxy's own test (403 and a record for plain and CONNECT), then the binary.
(cd /repo/tools/denyproxy && go test ./... && go build -o "$workdir/denyproxy" .)
denied="$workdir/denied.log"
: > "$denied"
"$workdir/denyproxy" -addr-file "$workdir/addr" -log "$denied" &
proxy_pid=$!
trap 'kill "$proxy_pid" 2>/dev/null || true' EXIT

i=0
while [ ! -s "$workdir/addr" ]; do
  i=$((i + 1))
  if [ "$i" -gt 100 ]; then
    echo "denyproxy did not start" >&2
    exit 1
  fi
  sleep 0.1
done
proxy="http://$(cat "$workdir/addr")"
no_proxy_list="localhost,127.0.0.1,::1"

# Positive control: a request through the proxy is refused and recorded, so an
# empty log after the suite means "no outbound call", not "proxy not in use".
if http_proxy="$proxy" wget -q -O /dev/null http://denyproxy-positive-control.invalid/ 2>/dev/null; then
  echo "denyproxy positive control: the request was not refused" >&2
  exit 1
fi
if ! grep -q "denyproxy-positive-control.invalid" "$denied"; then
  echo "denyproxy positive control: the request was not recorded" >&2
  exit 1
fi
: > "$denied"
echo "denyproxy: listening on $proxy; HTTP(S)_PROXY set for go test only, NO_PROXY=$no_proxy_list"

# Scoped to this one command: `go mod download` above ran without them.
status=0
HTTP_PROXY="$proxy" HTTPS_PROXY="$proxy" http_proxy="$proxy" https_proxy="$proxy" \
  NO_PROXY="$no_proxy_list" no_proxy="$no_proxy_list" \
  go test -p 1 ${GO_TEST_FLAGS:-} ./... || status=$?

count=$(wc -l < "$denied" | tr -d ' ')
echo "denyproxy: $count outbound request(s) denied during the suite"
if [ "$count" -ne 0 ]; then
  echo "FAIL: tests tried to reach these hosts through the proxy (PAC-139):" >&2
  sort "$denied" | uniq -c >&2
  [ "$status" -ne 0 ] || status=1
fi
exit "$status"
