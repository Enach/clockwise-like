#!/usr/bin/env bash
# Enforce the coverage floor from CLAUDE.md on a Go coverage profile.
#
#   coverage-gate.sh <profile> <min-percent> [label]
#
# Generated files are excluded before the total is computed. Code nobody wrote
# is code nobody can be asked to test, and counting it measures how much was
# generated rather than how much is covered: with backend/api/gen's 6363
# uncovered statements in, the backend reads 19.6%; with them out, 38.0%.
#
# Prints the total and exits non-zero below the bar, so `make coverage` fails
# loudly rather than printing a number nobody reads. If COVERAGE_SUMMARY_FILE
# is set, the result line is appended there too — `make verify` collects those
# into the block that gets pasted into the PR body.
set -euo pipefail

PROFILE="${1:?usage: coverage-gate.sh <profile> <min-percent> [label]}"
MIN="${2:?usage: coverage-gate.sh <profile> <min-percent> [label]}"
LABEL="${3:-$(basename "$(dirname "$PROFILE")")}"

# Matched against the profile's file paths as plain substrings. Keep this list
# short and justify every entry: a wrong one hides real code from the gate. It
# is hard-coded rather than caller-supplied so no PR can widen it quietly.
EXCLUDE_FRAGMENTS=(
  "/api/gen/" # oapi-codegen output, carries a DO NOT EDIT header
)

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

# Strip the excluded files from a copy of the profile, keeping its mode line.
# `go tool cover` still computes the total, so the arithmetic stays Go's. The
# copy sits beside the original because cover resolves import paths relative to
# the module.
measured="$base"
excluded=0
if [ "${#EXCLUDE_FRAGMENTS[@]}" -gt 0 ]; then
  kept="$(mktemp)"
  trap 'rm -f "$kept" "$dir/$base.filtered"' EXIT
  tail -n +2 "$PROFILE" > "$kept"
  for fragment in "${EXCLUDE_FRAGMENTS[@]}"; do
    before="$(wc -l < "$kept")"
    grep -v -F -- "$fragment" "$kept" > "$kept.next" || true
    mv "$kept.next" "$kept"
    excluded=$(( excluded + before - $(wc -l < "$kept") ))
  done
  measured="$base.filtered"
  head -n 1 "$PROFILE" > "$dir/$measured"
  cat "$kept" >> "$dir/$measured"
fi

total_line="$(cd "$dir" && go tool cover -func="$measured" | tail -1)"
pct="$(printf '%s\n' "$total_line" | awk '{print $NF}' | tr -d '%')"

if [ -z "$pct" ]; then
  echo "coverage: could not parse a total out of: $total_line" >&2
  exit 1
fi

suffix=""
if [ "$excluded" -gt 0 ]; then
  suffix="$(printf ' [%d generated blocks excluded]' "$excluded")"
fi
line="$(printf '%-8s coverage: %s%% (floor %s%%)%s' "$LABEL" "$pct" "$MIN" "$suffix")"
echo "$line"
if [ -n "${COVERAGE_SUMMARY_FILE:-}" ]; then
  echo "$line" >> "$COVERAGE_SUMMARY_FILE"
fi

# awk rather than bash arithmetic: the total is a decimal.
if awk -v p="$pct" -v m="$MIN" 'BEGIN { exit (p + 0 >= m + 0) ? 0 : 1 }'; then
  exit 0
fi

echo "FAIL: ${LABEL} coverage ${pct}% is below the ${MIN}% floor (CLAUDE.md)." >&2
echo "      The floor is a ratchet: raise it when coverage rises, never lower it." >&2
echo "      Lowest-covered functions first (excluded files are not listed):" >&2
(cd "$dir" && go tool cover -func="$measured" | sort -k3 -n | head -n 10) >&2
exit 1
