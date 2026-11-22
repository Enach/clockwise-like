# Factory scripts

Shared by the Paceday factory in both repositories (`Enach/clockwise-like` and
`Enach/smart-calendar-flow`). The two copies are identical: change both in the
same pair of PRs. The process they serve is described in
`clockwise-like/docs/factory/README.md`, §3 "Paired branches" and §9 "Running
the factory from CodeDen".

| Script | What it does |
|---|---|
| `merge-when-green.sh <pr> "<record>"` | Waits for every check on the PR's current head, re-runs the workflow if the paired branch in the other repo moved, or was merged while a check had failed (PAC-127 race), refuses an empty or non-passing check set, appends a factory merge record to the PR body, then squash-merges with `--match-head-commit`. Run it from a checkout of the PR's repo. |
| `sync-branch.sh <repo-dir> <branch>` | Fast-forwards a local branch to `origin/<branch>`, refusing if local work would be lost. Used by local gates that read local refs. |

Paired changes merge the API half first when the web half only regenerates
types, otherwise as the contract states; `merge-when-green.sh` handles the
re-pairing either way.
