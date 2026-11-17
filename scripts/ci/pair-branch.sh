#!/usr/bin/env bash
# Which branch of the other repo does this CI run pair with? (PAC-127)
#
# Paired PRs (factory: the api PR and its web PR land together) carry the
# exact same branch name in both repos. The rule:
#   1. no pac-N in this branch's name                  -> main
#   2. the other repo has a branch with exactly this name (compared
#      case-insensitively); one with an open PR wins   -> that branch
#   3. otherwise                                       -> main
# A different branch that merely shares the pac-N (a sibling batch such as
# factory/pac-122-obs for factory/pac-122-b0) is never used: pairing with it
# checks this branch against the wrong half.
#
# This file is identical in Enach/clockwise-like and Enach/smart-calendar-flow.
# Tests: scripts/ci/pair-branch.test.sh (run by CI right before the pairing).
#
# Usage in CI:  OTHER=owner/repo LABEL=web|api BRANCH=... GH_TOKEN=... \
#               bash scripts/ci/pair-branch.sh
# Writes ref=<branch> to $GITHUB_OUTPUT (when set) and logs the decision.

# pair_decide BRANCH NAMES_OK NAMES HEADS_OK HEADS LABEL
#   NAMES_OK/HEADS_OK: 1 when the list could be read, 0 when not.
#   NAMES: the other repo's branch names; HEADS: head refs of its open PRs
#   (newline-separated). Prints log lines, then "ref=<ref>" and "why=<why>"
#   as the last two lines. Pure: no network, no globals.
pair_decide() {
  local branch=$1 names_ok=$2 names=$3 heads_ok=$4 heads=$5 label=${6:-other}
  local t m o ref=main why
  t=$(grep -oiE -- 'pac-[0-9]+' <<<"$branch" | head -1 | tr '[:upper:]' '[:lower:]' || true)
  if [ -z "$t" ]; then
    why="no pac-N in branch name"
  elif [ "$names_ok" != 1 ]; then
    why="could not list $label branches"
    echo "::warning::could not list $label branches; using main"
  else
    # Exact name, case-insensitive (as the pac-N match always was).
    m=$(printf '%s\n' "$names" | awk -v b="$branch" 'NF && tolower($0) == tolower(b)' || true)
    o=""
    if [ -n "$m" ]; then
      if [ "$heads_ok" = 1 ]; then
        o=$(printf '%s\n' "$m" | grep -xF -f <(printf '%s\n' "$heads") -- || true)
      else
        echo "::warning::could not list open $label PRs; pairing by exact branch name only (no open-PR preference)"
      fi
    fi
    echo "candidates for $branch: $(printf '%s\n' "$m" | paste -sd' ' -) | with an open PR: $(printf '%s\n' "$o" | paste -sd' ' -)"
    if [ -n "$o" ]; then
      ref=$(_pair_pick "$branch" "$o") why="same name as this branch, with an open PR"
    elif [ -n "$m" ]; then
      ref=$(_pair_pick "$branch" "$m") why="same name as this branch, no open PR"
    else
      why="no $label branch named $branch"
    fi
  fi
  echo "ref=$ref"
  echo "why=$why"
}

# Several names equal ignoring case: the exact spelling, else the first sorted.
_pair_pick() {
  if printf '%s\n' "$2" | grep -qxF -- "$1"; then printf '%s\n' "$1"
  else printf '%s\n' "$2" | LC_ALL=C sort | head -1; fi
}

pair_main() {
  : "${OTHER:?OTHER=owner/repo required}" "${BRANCH:?BRANCH required}"
  local label=${LABEL:-$OTHER} names heads names_ok=0 heads_ok=0 out ref why
  if names=$(gh api --paginate "repos/$OTHER/branches?per_page=100" --jq '.[].name'); then names_ok=1; fi
  # head.repo is null for a deleted fork; owner/repo names are case-insensitive.
  if heads=$(gh api --paginate "repos/$OTHER/pulls?state=open&per_page=100" \
      --jq ".[] | select((.head.repo.full_name // \"\" | ascii_downcase) == (\"$OTHER\" | ascii_downcase)) | .head.ref"); then
    heads_ok=1
  fi
  out=$(pair_decide "$BRANCH" "$names_ok" "$names" "$heads_ok" "$heads" "$label")
  printf '%s\n' "$out" | head -n -2
  ref=$(printf '%s\n' "$out" | tail -2 | sed -n 's/^ref=//p')
  why=$(printf '%s\n' "$out" | tail -1 | sed 's/^why=//')
  [ -n "${GITHUB_OUTPUT:-}" ] && echo "ref=$ref" >> "$GITHUB_OUTPUT"
  echo "$label ref: $ref ($why)"
}

if [ "${BASH_SOURCE[0]}" = "$0" ]; then
  set -euo pipefail
  pair_main
fi
