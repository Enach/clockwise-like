#!/usr/bin/env bash
# Self-test for tools/sfguard (PAC-122, silent-failure register §8), end to
# end: the built guard against testdata/fixture, judged by exit code and output.
# Run: bash tools/sfguard/sfguard.test.sh   (make lint runs it before the guard)
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FIX="$HERE/testdata/fixture"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

BIN="$TMP/sfguard"
(cd "$HERE" && go build -o "$BIN" .) || { echo "FAIL could not build the guard"; exit 1; }

fails=0 runs=0
# expect NAME WANT_EXIT BASELINE [GREP_IN_OUTPUT [GREP_NOT_IN_OUTPUT]]
expect() {
  local name=$1 want=$2 baseline=$3 must=${4:-} mustnot=${5:-} out code
  runs=$((runs + 1))
  out=$("$BIN" -root "$FIX" -baseline "$baseline" . 2>&1); code=$?
  if [ "$code" != "$want" ]; then
    fails=$((fails + 1)); echo "FAIL $name: exit $code, want $want"; printf '     %s\n' "$out" | head -20; return
  fi
  if [ -n "$must" ] && ! grep -qE -- "$must" <<<"$out"; then
    fails=$((fails + 1)); echo "FAIL $name: output lacks /$must/"; printf '     %s\n' "$out" | head -20; return
  fi
  if [ -n "$mustnot" ] && grep -qE -- "$mustnot" <<<"$out"; then
    fails=$((fails + 1)); echo "FAIL $name: output has /$mustnot/"; printf '     %s\n' "$out" | head -20; return
  fi
  echo "ok   $name -> exit $code"
}

# The full baseline: every fixture occurrence, each naming an issue.
: > "$TMP/empty"
"$BIN" -root "$FIX" -baseline "$TMP/empty" -update . | sed 's/# TODO/# PAC-122 TODO/' > "$TMP/full"
n=$(grep -c . "$TMP/full")

expect "every occurrence baselined passes" 0 "$TMP/full" "^sfguard OK: $n occurrence"

# A new violation: the fixture minus one baselined line looks, to the guard,
# exactly like a branch that added that call.
grep -v 'auth.LoadToken' "$TMP/full" > "$TMP/minus-auth"
expect "new auth.Load* discard fails" 1 "$TMP/minus-auth" "^NEW .*auth.LoadToken \(auth.Load\*\)" "storage.GetUser"
grep -v 'calendarOps.deleteEvent' "$TMP/full" > "$TMP/minus-calops"
expect "new calendar-ops discard fails" 1 "$TMP/minus-calops" "^NEW .*engine.calendarOps.deleteEvent"
grep -v 'sql.Row.Scan' "$TMP/full" > "$TMP/minus-db"
expect "new h.db...Scan discard fails" 1 "$TMP/minus-db" "^NEW .*sql.Row.Scan"
# The message points at the register and at the exception path.
expect "failure message names the register and the baseline" 1 "$TMP/minus-auth" "silent-failures.md.*SF rows"

# Counts, not just keys: `twice` holds two identical storage.Save discards.
awk '/ twice +storage[.]Save / && !d {d=1; next} {print}' "$TMP/full" > "$TMP/one-of-two"
expect "a second identical discard in one func fails" 1 "$TMP/one-of-two" "^NEW .*twice discards the error from storage.Save"

# The baseline only shrinks: an entry whose occurrence is gone fails.
{ cat "$TMP/full"; echo "api/api.go  checked  storage.GetUser  # PAC-122 fixed long ago"; } > "$TMP/stale"
expect "stale baseline entry fails" 1 "$TMP/stale" "^STALE api/api.go checked storage.GetUser"

# A new entry must name a PAC issue.
awk '!d && sub(/# PAC-122 TODO/, "# TODO") {d=1} {print}' "$TMP/full" > "$TMP/unnamed"
expect "baseline entry without a PAC issue fails" 1 "$TMP/unnamed" "no PAC issue"

# The excluded patterns never appear, even with an empty baseline.
expect "excluded patterns are never reported" 1 "$TMP/empty" "^NEW " \
  "json|Encode|Write|Atoi|ParseToken|Pure|Rollback|NewClient|unrelated|os\.|_test\.go|zz_gen|checked "
got=$("$BIN" -root "$FIX" -baseline "$TMP/empty" . 2>&1 | grep -c '^NEW ')
runs=$((runs + 1))
if [ "$got" = "$n" ]; then echo "ok   empty baseline reports exactly the $n targeted occurrences"
else fails=$((fails + 1)); echo "FAIL empty baseline reports $got occurrences, want $n"; fi

echo "$((runs - fails))/$runs passed"
[ "$fails" -eq 0 ]
