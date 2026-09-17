#!/usr/bin/env bash
# Enforce the coverage floor from CLAUDE.md on a Go coverage profile.
#
#   coverage-gate.sh <profile> <min-percent> [label]
#
# Prints the total and exits non-zero below the bar, so `make coverage` fails
# loudly rather than printing a number nobody reads. If COVERAGE_SUMMARY_FILE
# is set, the result line is appended there too — `make verify` collects those
# into the block that gets pasted into the PR body.
set -euo pipefail

PROFILE="${1:?usage: coverage-gate.sh <profile> <min-percent> [label]}"
MIN="${2:?usage: coverage-gate.sh <profile> <min-percent> [label]}"
LABEL="${3:-$(basename "$(dirname "$PROFILE")")}"

if [ ! -s "$PROFILE" ]; then
  echo "coverage: no profile at $PROFILE (did the test run fail?)" >&2
  exit 1
fi
if ! command -v go >/dev/null 2>&1; then
  echo "go not installed — needed to read the coverage profile" >&2
  exit 1
fi

# `go tool cover` resolves the import paths in the profile against the module in
# the current directory, so it has to run from the module root, not the repo root.
dir="$(cd "$(dirname "$PROFILE")" && pwd)"
base="$(basename "$PROFILE")"

total_line="$(cd "$dir" && go tool cover -func="$base" | tail -1)"
pct="$(printf '%s\n' "$total_line" | awk '{print $NF}' | tr -d '%')"

if [ -z "$pct" ]; then
  echo "coverage: could not parse a total out of: $total_line" >&2
  exit 1
fi

line="$(printf '%-8s coverage: %s%% (floor %s%%)' "$LABEL" "$pct" "$MIN")"
echo "$line"
if [ -n "${COVERAGE_SUMMARY_FILE:-}" ]; then
  echo "$line" >> "$COVERAGE_SUMMARY_FILE"
fi

# awk rather than bash arithmetic: the total is a decimal.
if awk -v p="$pct" -v m="$MIN" 'BEGIN { exit (p + 0 >= m + 0) ? 0 : 1 }'; then
  exit 0
fi

echo "FAIL: ${LABEL} coverage ${pct}% is below the ${MIN}% floor (CLAUDE.md)." >&2
echo "      Lowest-covered packages first:" >&2
(cd "$dir" && go tool cover -func="$base" | sort -k3 -n | head -n 10) >&2
exit 1
