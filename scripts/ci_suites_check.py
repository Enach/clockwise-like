#!/usr/bin/env python3
"""Fail when ci.yml and the Makefile disagree about what the gate runs.

ci.yml runs `make verify` as parallel jobs, one make target per suite, behind
one aggregating job named "verify". This guard keeps the two in step:

  1. The Makefile's `test` and `coverage` prerequisites are exactly
     test-<s> for TEST_SUITES and coverage-<s> for COVERAGE_SUITES, and the
     `verify` recipe runs exactly VERIFY_STEPS, in that order.
  2. The jobs in ci.yml run exactly the targets those steps expand to
     (lint, openapi-check, test-*, coverage-*, e2e), each once: none missing,
     none extra.
  3. Every job that runs one of them is a `needs` of the job "verify", that job
     runs `if: always()`, and no other job is called "verify".

Usage: scripts/ci_suites_check.py [repo-root]   (needs python3-yaml and make)
"""
import os
import re
import subprocess
import sys

import yaml

ROOT = os.path.abspath(sys.argv[1] if len(sys.argv) > 1 else os.path.join(os.path.dirname(__file__), ".."))
MAKEFILE = os.path.join(ROOT, "Makefile")
WORKFLOW = os.path.join(ROOT, ".github", "workflows", "ci.yml")
ENV = dict(os.environ, LC_ALL="C")

errors = []


def make(*args):
    return subprocess.run(["make", "--no-print-directory", "-f", MAKEFILE, *args],
                          cwd=ROOT, env=ENV, capture_output=True, text=True)


# ── 1. The Makefile ──────────────────────────────────────────────────────────
out = make("-s", "print-suites")
if out.returncode != 0:
    sys.exit(f"make print-suites failed:\n{out.stderr}")
lists = {}
for line in out.stdout.splitlines():
    k, _, v = line.partition("=")
    lists[k] = v.split()
test_suites = lists.get("TEST_SUITES", [])
coverage_suites = lists.get("COVERAGE_SUITES", [])
verify_steps = lists.get("VERIFY_STEPS", [])
if not (test_suites and coverage_suites and verify_steps):
    sys.exit(f"empty suite list from make print-suites: {lists}")

# The make database (-p) with nothing run (-q): rules with prerequisites
# expanded, recipes as written.
db = make("-qp").stdout
rules, recipes, cur = {}, {}, None
for line in db.splitlines():
    m = re.match(r"^([A-Za-z0-9_.-]+):(?!=)\s*(.*)$", line)
    if m and not line.startswith("\t"):
        cur = m.group(1)
        # make >= 4.4 shows .NOTPARALLEL as .WAIT between prerequisites
        rules[cur] = [p for p in m.group(2).split() if p != ".WAIT"]
        recipes.setdefault(cur, [])
    elif line.startswith("\t") and cur:
        recipes[cur].append(line)
    elif not line.strip() or line.startswith("#"):
        cur = None if not line.strip() else cur


def expect_prereqs(target, want):
    got = rules.get(target)
    if got is None:
        errors.append(f"Makefile: no rule `{target}`")
    elif got != want:
        errors.append(f"Makefile: `{target}` prerequisites are {got}, expected {want} "
                      f"(from the suite lists; change the list, not the rule)")


expect_prereqs("test", [f"test-{s}" for s in test_suites])
expect_prereqs("coverage", [f"coverage-{s}" for s in coverage_suites])

called = re.findall(r"\$\(SELF\)\s+([a-z][a-z0-9-]*)", "\n".join(recipes.get("verify", [])))
want_called = ["verify-banner", *verify_steps, "verify-summary"]
if called != want_called:
    errors.append(f"Makefile: the `verify` recipe runs {called}, expected {want_called} (VERIFY_STEPS)")

expand = {"test": [f"test-{s}" for s in test_suites],
          "coverage": [f"coverage-{s}" for s in coverage_suites]}
expected = []
for step in verify_steps:
    expected += expand.get(step, [step])
for t in expected:
    if t not in rules:
        errors.append(f"Makefile: verify needs `{t}` but there is no such rule")

# ── 2. ci.yml ────────────────────────────────────────────────────────────────
with open(WORKFLOW, encoding="utf-8") as f:
    wf = yaml.safe_load(f)
jobs = wf.get("jobs", {})
ran = {}  # target -> [job ids]
for jid, job in jobs.items():
    for step in job.get("steps", []):
        run = step.get("run") or ""
        for cmd in re.split(r"[\n;&|]+", run):
            # `make a b`, possibly after VAR=value assignments; not `npm run make`.
            m = re.match(r"^\s*(?:[A-Za-z_][A-Za-z0-9_]*=\S*\s+)*make((?:\s+[a-z][a-z0-9-]*)+)\s*\\?\s*$", cmd)
            if m:
                for t in m.group(1).split():
                    ran.setdefault(t, []).append(jid)

for t in expected:
    if t not in ran:
        errors.append(f"ci.yml: no job runs `make {t}` (part of `make verify`)")
    elif len(ran[t]) > 1:
        errors.append(f"ci.yml: `make {t}` runs in several jobs {ran[t]}")
for t, where in ran.items():
    if t not in expected:
        errors.append(f"ci.yml: job(s) {where} run `make {t}`, which `make verify` does not run")

# ── 3. The aggregating check ─────────────────────────────────────────────────
named_verify = [jid for jid, job in jobs.items() if job.get("name", jid) == "verify"]
if named_verify != ["verify"]:
    errors.append(f"ci.yml: jobs whose check name is `verify`: {named_verify}; expected exactly the job `verify`")
agg = jobs.get("verify", {})
needs = agg.get("needs", [])
needs = [needs] if isinstance(needs, str) else needs
if str(agg.get("if", "")).replace("${{", "").replace("}}", "").strip() != "always()":
    errors.append("ci.yml: job `verify` must run `if: always()`, or a failed job leaves it skipped")
for t, where in ran.items():
    for jid in where:
        if jid not in needs:
            errors.append(f"ci.yml: job `{jid}` runs `make {t}` but is not in verify's needs {needs}")

if errors:
    print("ci_suites_check: ci.yml and the Makefile disagree about the gate:", file=sys.stderr)
    for e in errors:
        print(f"  - {e}", file=sys.stderr)
    sys.exit(1)
print(f"ci_suites_check: OK — ci.yml runs exactly {' '.join(expected)} "
      f"(VERIFY_STEPS={' '.join(verify_steps)}), all behind `verify`")
