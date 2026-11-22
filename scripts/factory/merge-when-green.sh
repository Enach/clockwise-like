#!/bin/bash
# merge-when-green.sh <pr> <record text>: wait for CI on the PR's current head, merge (squash) only if every check passed.
n=$1; rec=$2
for i in $(seq 1 30); do sleep 10; gh pr checks $n --json name -q '.[].name' | grep -q . && break; done
gh pr checks $n --watch --interval 30 >/dev/null 2>&1
# PAC-127 race: the api CI pins the paired web SHA in its "pair web branch" job. If the web branch
# moved after that, re-run the whole workflow (re-run-failed would reuse the stale pairing) and wait again.
REPO=$(gh repo view --json nameWithOwner -q .nameWithOwner)
if [ "$REPO" = "Enach/clockwise-like" ]; then
  for attempt in 1 2; do
    PL=$(gh pr checks $n --json name,link -q '.[]|select(.name=="pair web branch").link')
    [ -z "$PL" ] && break
    PJ=${PL##*/}; RUN=$(echo "$PL" | sed -E 's#.*/runs/([0-9]+)/.*#\1#')
    PAIRED=$(gh api repos/$REPO/actions/jobs/$PJ/logs 2>/dev/null | grep -oE "web: \S+ @ [0-9a-f]{40}" | tail -1)
    WREF=$(echo "$PAIRED" | awk '{print $2}'); WSHA=$(echo "$PAIRED" | awk '{print $4}')
    [ -z "$WREF" ] && break
    CUR=$(gh api "repos/Enach/smart-calendar-flow/commits/$(printf '%s' "$WREF" | sed 's#/#%2F#g')" --jq .sha 2>/dev/null)
    # A deleted branch (paired half already merged) returns error JSON, not a SHA. If the run paired with that
    # branch and it failed, a re-run now pairs with main (which holds the merged half); otherwise nothing to do.
    if ! echo "$CUR" | grep -qE '^[0-9a-f]{40}$'; then
      if [ "$WREF" != "main" ] && gh pr checks $n --json bucket -q '.[].bucket' | grep -q fail; then
        echo "#$n paired web $WREF is gone (merged); failed run re-run against main"
        gh run rerun "$RUN" >/dev/null 2>&1; sleep 45
        gh pr checks $n --watch --interval 30 >/dev/null 2>&1
      fi
      break
    fi
    [ "$CUR" = "$WSHA" ] && break
    echo "#$n paired web $WREF moved ${WSHA:0:7} -> ${CUR:0:7}; full re-run"
    gh run rerun "$RUN" >/dev/null 2>&1; sleep 45
    gh pr checks $n --watch --interval 30 >/dev/null 2>&1
  done
fi
# Same race on the web side: its single "verify" job logs "api  : <short sha> on <ref>".
if [ "$REPO" = "Enach/smart-calendar-flow" ]; then
  for attempt in 1 2; do
    VL=$(gh pr checks $n --json name,link -q '.[]|select(.name=="verify").link')
    [ -z "$VL" ] && break
    VJ=${VL##*/}; RUN=$(echo "$VL" | sed -E 's#.*/runs/([0-9]+)/.*#\1#')
    PAIRED=$(gh api repos/$REPO/actions/jobs/$VJ/logs 2>/dev/null | grep -oE "api +: [0-9a-f]{7,40} on \S+" | tail -1)
    ASHA=$(echo "$PAIRED" | awk '{print $3}'); AREF=$(echo "$PAIRED" | awk '{print $5}')
    [ -z "$AREF" ] && break
    CUR=$(gh api "repos/Enach/clockwise-like/commits/$(printf '%s' "$AREF" | sed 's#/#%2F#g')" --jq .sha 2>/dev/null)
    if ! echo "$CUR" | grep -qE '^[0-9a-f]{40}$'; then
      if [ "$AREF" != "main" ] && gh pr checks $n --json bucket -q '.[].bucket' | grep -q fail; then
        echo "#$n paired api $AREF is gone (merged); failed run re-run against main"
        gh run rerun "$RUN" >/dev/null 2>&1; sleep 45
        gh pr checks $n --watch --interval 30 >/dev/null 2>&1
      fi
      break
    fi
    case "$CUR" in "$ASHA"*) break ;; esac
    echo "#$n paired api $AREF moved ${ASHA:0:7} -> ${CUR:0:7}; full re-run"
    gh run rerun "$RUN" >/dev/null 2>&1; sleep 45
    gh pr checks $n --watch --interval 30 >/dev/null 2>&1
  done
fi
H=$(gh pr view $n --json headRefOid -q .headRefOid)
echo "#$n head=${H:0:7}"; gh pr checks $n
BK=$(gh pr checks $n --json bucket -q '.[].bucket' 2>/dev/null)
if [ -z "$BK" ] || echo "$BK" | grep -qv '^pass$'; then echo "#$n NOT GREEN - not merging"; return 1 2>/dev/null || exit 1; fi
B=$(gh pr view $n --json body -q .body)
gh pr edit $n --body "$B

---
**Factory merge record:** $rec CI verify pass @ ${H:0:7}." >/dev/null
# Success is the PR's state, not gh's exit code: gh fails when a worktree still holds the local branch, after the merge succeeded.
gh pr merge $n --squash --delete-branch --match-head-commit $H 2>&1 | grep -v "delete.*branch"
S=$(gh pr view $n --json state,mergeCommit -q '.state+" "+(.mergeCommit.oid // "")')
echo "#$n $S"
case "$S" in MERGED*) true ;; *) return 1 2>/dev/null || exit 1 ;; esac
