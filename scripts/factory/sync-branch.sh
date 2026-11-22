#!/usr/bin/env bash
# sync-branch.sh <repo-dir> <branch>
# Brings the LOCAL branch <branch> of <repo-dir> up to date with origin/<branch>,
# so a gate that reads local refs (git archive, git clone --branch) tests what is
# actually on GitHub. Added after an e2e gate built a stale local `main`.
#   - no origin/<branch>: nothing to do (local-only branch);
#   - local branch missing: created at origin/<branch>;
#   - local is an ancestor of origin: fast-forwarded (checked-out branch: only if
#     the working tree is clean, via merge --ff-only);
#   - local has commits origin lacks: refuse (exit 3) — never discard local work.
set -uo pipefail
REPO="$1"; BR="$2"
cd "$REPO" || exit 2
git fetch -q origin || { echo "sync: fetch failed in $REPO" >&2; exit 2; }
git rev-parse -q --verify "refs/remotes/origin/$BR" >/dev/null || exit 0
if ! git rev-parse -q --verify "refs/heads/$BR" >/dev/null; then
  git branch -q "$BR" "origin/$BR"; exit 0
fi
[ "$(git rev-parse "$BR")" = "$(git rev-parse "origin/$BR")" ] && exit 0
if ! git merge-base --is-ancestor "$BR" "origin/$BR"; then
  echo "sync: local $BR in $REPO has commits origin/$BR lacks; refusing" >&2; exit 3
fi
if [ "$(git branch --show-current)" = "$BR" ]; then
  if [ -n "$(git status --porcelain --untracked-files=no)" ]; then
    echo "sync: $REPO is on $BR with uncommitted changes; refusing" >&2; exit 3
  fi
  git merge -q --ff-only "origin/$BR"
else
  git branch -q -f "$BR" "origin/$BR"
fi
