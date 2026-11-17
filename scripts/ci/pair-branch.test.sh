#!/usr/bin/env bash
# Tests for scripts/ci/pair-branch.sh (PAC-127). No network: they call the
# pure decision function. Run: bash scripts/ci/pair-branch.test.sh
# This file is identical in Enach/clockwise-like and Enach/smart-calendar-flow.
set -uo pipefail
# shellcheck source-path=SCRIPTDIR source=pair-branch.sh
. "$(dirname "${BASH_SOURCE[0]}")/pair-branch.sh"

fails=0 runs=0
# expect NAME WANT_REF BRANCH NAMES_OK NAMES HEADS_OK HEADS
expect() {
  local name=$1 want=$2 out got; shift 2
  runs=$((runs + 1))
  out=$(pair_decide "$@" other)
  got=$(printf '%s\n' "$out" | sed -n 's/^ref=//p')
  if [ "$got" = "$want" ]; then echo "ok   $name -> $got"
  else fails=$((fails + 1)); echo "FAIL $name: want $want, got $got"; printf '     %s\n' "$out"; fi
}
L() { printf '%s\n' "$@"; }  # newline-separated list

expect "exact match with an open PR" factory/pac-127 \
  factory/pac-127 1 "$(L main factory/pac-127 factory/pac-127-b1)" 1 "$(L factory/pac-127 factory/pac-127-b1)"
expect "exact match without a PR" factory/pac-127 \
  factory/pac-127 1 "$(L main factory/pac-127 factory/pac-127-obs)" 1 "$(L factory/pac-127-obs)"
expect "exact match, open PRs unknown" factory/pac-127 \
  factory/pac-127 1 "$(L main factory/pac-127)" 0 ""
# PAC-122: api factory/pac-122-b0 merged and deleted, factory/pac-122-obs open.
expect "only sibling batch branches exist -> main" main \
  factory/pac-122-b0 1 "$(L main factory/pac-122-obs factory/pac-122-b1)" 1 "$(L factory/pac-122-obs factory/pac-122-b1)"
expect "lone sibling with an open PR -> main" main \
  impl/pac-50 1 "$(L main factory/pac-50)" 1 "$(L factory/pac-50)"
expect "no pac-N in the name -> main" main \
  dependabot/npm/vite-6 1 "$(L main dependabot/npm/vite-6)" 1 "$(L dependabot/npm/vite-6)"
expect "case-insensitive: PAC-127 vs pac-127" factory/pac-127 \
  Factory/PAC-127 1 "$(L main factory/pac-127)" 1 "$(L factory/pac-127)"
expect "case-insensitive: exact spelling preferred" Factory/PAC-127 \
  Factory/PAC-127 1 "$(L factory/pac-127 Factory/PAC-127)" 1 ""
expect "case-insensitive: open PR beats spelling" factory/pac-127 \
  Factory/PAC-127 1 "$(L factory/pac-127 Factory/PAC-127)" 1 "$(L factory/pac-127)"
expect "pac-12 is not pac-127" main \
  factory/pac-12 1 "$(L main factory/pac-127 factory/pac-12-b0)" 1 "$(L factory/pac-127)"
expect "branch list unreadable -> main" main \
  factory/pac-127 0 "" 1 "$(L factory/pac-127)"
expect "regex characters are literal" main \
  factory/pac-127.x 1 "$(L factory/pac-127ax)" 1 ""

# The decision log line is kept.
log=$(pair_decide factory/pac-122-b0 1 "$(L factory/pac-122-obs)" 1 "$(L factory/pac-122-obs)" web)
runs=$((runs + 1))
if grep -q '^candidates for factory/pac-122-b0: ' <<<"$log" && grep -qx 'why=no web branch named factory/pac-122-b0' <<<"$log"; then
  echo "ok   decision log line"
else fails=$((fails + 1)); echo "FAIL decision log line:"; printf '     %s\n' "$log"; fi

echo "$((runs - fails))/$runs passed"
[ "$fails" -eq 0 ]
