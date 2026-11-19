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

# A fixed occurrence must leave the baseline: an entry whose occurrence is gone fails.
{ cat "$TMP/full"; echo "api/api.go  checked  storage.GetUser  # PAC-122 fixed long ago"; } > "$TMP/stale"
expect "stale baseline entry fails" 1 "$TMP/stale" "^STALE api/api.go checked storage.GetUser"

# A new entry must name a PAC issue.
awk '!d && sub(/# PAC-122 TODO/, "# TODO") {d=1} {print}' "$TMP/full" > "$TMP/unnamed"
expect "baseline entry without a PAC issue fails" 1 "$TMP/unnamed" "no PAC issue"

# The excluded patterns never appear, even with an empty baseline.
expect "excluded patterns are never reported" 1 "$TMP/empty" "^NEW " \
  "json|Encode|Write|Atoi|ParseToken|Pure|Rollback|RowsAffected|NewClient|unrelated|os\.|_test\.go|zz_gen|checked "
got=$("$BIN" -root "$FIX" -baseline "$TMP/empty" . 2>&1 | grep -c '^NEW ')
runs=$((runs + 1))
if [ "$got" = "$n" ]; then echo "ok   empty baseline reports exactly the $n targeted occurrences"
else fails=$((fails + 1)); echo "FAIL empty baseline reports $got occurrences, want $n"; fi

# PAC-0 is not an issue.
sed 's/# PAC-122 /# PAC-0 /' "$TMP/full" > "$TMP/pac0"
expect "baseline entry citing PAC-0 fails" 1 "$TMP/pac0" "no PAC issue"

# Seams, generics and the other sql handles are covered; the phrase
# "Code generated ... DO NOT EDIT" after the package clause exempts nothing.
grep -v 'orgAIStore.read' "$TMP/full" > "$TMP/minus-seam"
expect "new discard through a store seam fails" 1 "$TMP/minus-seam" "^NEW .*api.orgAIStore.read \(storage-seam\)"
grep -v 'googleStore.load' "$TMP/full" > "$TMP/minus-concrete"
expect "new discard on a concrete auth store fails" 1 "$TMP/minus-concrete" "^NEW .*auth.googleStore.load \(storage-seam\)"
grep -v 'storage.Find' "$TMP/full" > "$TMP/minus-generic"
expect "new generic storage discard fails" 1 "$TMP/minus-generic" "^NEW .*storage.Find"
grep -v 'notGenerated' "$TMP/full" > "$TMP/minus-midgen"
expect "a mid-file 'Code generated' phrase does not exempt a file" 1 "$TMP/minus-midgen" "^NEW +api/midgen.go"

# PAC-142: a new `if v, err := target(); err == nil` swallow fails, keyed apart
# from a blank discard of the same call; the shapes that are not it
# (testdata/fixture/api/errnil.go, handled) are never reported.
grep -v 'errnil:auth.LoadToken' "$TMP/full" > "$TMP/minus-errnil-auth"
expect "new err == nil swallow (&& chain) fails" 1 "$TMP/minus-errnil-auth" "^NEW .*swallows acts only when auth.LoadToken succeeds.*errnil:auth.LoadToken"
grep -v 'errnil:storage.Store.Get' "$TMP/full" > "$TMP/minus-errnil-get"
expect "new nil == err swallow fails" 1 "$TMP/minus-errnil-get" "^NEW .*errnil:storage.Store.Get"
awk '/ swallows +errnil:storage[.]GetUser / && !d {d=1; next} {print}' "$TMP/full" > "$TMP/minus-errnil-one"
expect "a second err == nil swallow of one call fails" 1 "$TMP/minus-errnil-one" "^NEW .*errnil:storage.GetUser"
{ grep -v 'errnil:storage.Store.Get' "$TMP/full"; echo "api/errnil.go  swallows  storage.Store.Get  # PAC-142 a blank entry"; } > "$TMP/errnil-as-blank"
expect "a blank-shape entry does not accept an err == nil swallow" 1 "$TMP/errnil-as-blank" "^STALE api/errnil.go swallows storage.Store.Get"
expect "err == nil shapes that handle or see the error are never reported" 1 "$TMP/empty" "errnil" "handled|Atoi"

# An interface that wraps storage and is in neither seam list fails the run.
runs=$((runs + 1))
out=$("$BIN" -root "$HERE/testdata/seamfixture" -baseline "$TMP/empty" . 2>&1); code=$?
if [ "$code" = 1 ] && grep -q '^SEAM  api.widgetStore' <<<"$out" && ! grep -q 'pure' <<<"$out"; then
  echo "ok   unlisted seam fails -> exit 1"
else fails=$((fails + 1)); echo "FAIL unlisted seam: exit $code"; printf '     %s\n' "$out" | head; fi
runs=$((runs + 1))
if [ "$code" = 1 ] && grep -q '^SEAM  auth.otherStore implements the listed seam auth.credentialStore' <<<"$out"; then
  echo "ok   unlisted concrete store behind a listed seam fails -> exit 1"
else fails=$((fails + 1)); echo "FAIL unlisted concrete store: exit $code"; printf '     %s\n' "$out" | head; fi

echo "$((runs - fails))/$runs passed"
[ "$fails" -eq 0 ]
