#!/usr/bin/env python3
"""Maintain contracts/openapi/MIGRATION.md — the handwritten/generated register.

docs/factory/README.md §4 requires every operation in the contract to be listed
as `handwritten` or `generated`, with the count moving in one direction only.
The list of operations is derived from the bundle so it cannot silently fall
behind the contract; the *status* of each row is owned by humans and agents and
is preserved across regenerations, as is the notes column.

    openapi_migration_report.py            refresh the register in place
    openapi_migration_report.py --check    gate: fail on a missing row, a stale
                                           row, an unknown status, formatting
                                           drift, or a regression in the count

Exit codes: 0 ok, 1 the register is wrong (message says how).
"""

from __future__ import annotations

import argparse
import pathlib
import re
import subprocess
import sys

import yaml

ROOT = pathlib.Path(__file__).resolve().parent.parent
BUNDLE = ROOT / "contracts" / "openapi" / "openapi.yaml"
REGISTER = ROOT / "contracts" / "openapi" / "MIGRATION.md"

METHODS = ("get", "put", "post", "delete", "patch", "head", "options")
STATUSES = ("handwritten", "generated")
DEFAULT_STATUS = "handwritten"

HEADER = """# Endpoint migration register

<!-- The rows are generated from contracts/openapi/openapi.yaml by
     scripts/openapi_migration_report.py. The `status` and `notes` columns are
     yours: they are read back and preserved on every regeneration. Run
     `make openapi` after changing the contract, `make openapi-check` to gate. -->

Every operation in the contract is either **handwritten** (a hand-written
handler in `backend/api/`, verified against the contract but not generated from
it) or **generated** (the handler implements the generated
`StrictServerInterface` method from `backend/api/gen/`).

## The one-way rule

`docs/factory/README.md` §4: *new and modified endpoints use the generated
server interface; untouched endpoints are verified against the contract but not
regenerated.* A big-bang migration of every operation would be a rewrite with no
test coverage to catch what it broke.

So a row moves `handwritten` → `generated` and never back. `make openapi-check`
compares this file against its committed version and fails if the number of
`generated` rows went down. If a migration genuinely has to be reverted, say so
in the PR body — the gate is there to make that a decision, not an accident.

## Moving a row

1. Change the handler so it implements the `StrictServerInterface` method for
   that operationId and is mounted through the generated wrapper.
2. Keep the existing handler tests passing — they are the only proof the
   migration preserved behaviour.
3. Flip the row's `status` to `generated` here, and put the issue key in
   `notes`.
4. `make openapi` — it re-renders this table, keeping the status and notes you
   just wrote, and fixes the column alignment your edit changed. Editing the
   cell by hand and skipping this step is reported as drift.
5. `make verify`.

Do not edit the operationId, method, path or tag columns: they are regenerated
from the contract and any edit is reported as drift.
"""


def operations(spec: dict) -> list[dict]:
    ops = []
    for path, item in spec.get("paths", {}).items():
        for method, op in item.items():
            if method not in METHODS or not isinstance(op, dict):
                continue
            ops.append(
                {
                    "operationId": op.get("operationId") or f"<missing:{method}:{path}>",
                    "method": method.upper(),
                    "path": path,
                    "tag": (op.get("tags") or ["-"])[0],
                }
            )
    # Grouped by tag so a reviewer reads a domain at a time; deterministic.
    ops.sort(key=lambda o: (o["tag"], o["path"], o["method"]))
    return ops


ROW_RE = re.compile(r"^\|(?P<cells>.*)\|\s*$")


def parse_existing(text: str) -> dict[str, tuple[str, str]]:
    """Read back {operationId: (status, notes)} from a previous rendering."""
    known: dict[str, tuple[str, str]] = {}
    for line in text.splitlines():
        m = ROW_RE.match(line)
        if not m:
            continue
        cells = [c.strip() for c in m.group("cells").split("|")]
        if len(cells) != 6:
            continue
        oid, method, _path, _tag, status, notes = cells
        if oid in ("operationId", "") or set(method) <= {"-", ":"}:
            continue  # header or separator row
        known[oid] = (status, notes)
    return known


def render(ops: list[dict], known: dict[str, tuple[str, str]]) -> str:
    rows = []
    for op in ops:
        status, notes = known.get(op["operationId"], (DEFAULT_STATUS, ""))
        rows.append([op["operationId"], op["method"], op["path"], op["tag"], status, notes])

    headings = ["operationId", "method", "path", "tag", "status", "notes"]
    widths = [max(len(h), *(len(r[i]) for r in rows)) for i, h in enumerate(headings)]

    def line(cells: list[str]) -> str:
        return "| " + " | ".join(c.ljust(widths[i]) for i, c in enumerate(cells)).rstrip() + " |"

    generated = sum(1 for r in rows if r[4] == "generated")
    body = [
        HEADER,
        "## Progress",
        "",
        f"- operations: **{len(rows)}**",
        f"- generated: **{generated}**",
        f"- handwritten: **{len(rows) - generated}**",
        "",
        "## Register",
        "",
        line(headings),
        "|" + "|".join("-" * (w + 2) for w in widths) + "|",
        *(line(r) for r in rows),
        "",
    ]
    return "\n".join(body)


def committed_generated_count() -> int | None:
    """How many rows were `generated` in the committed version, or None."""
    try:
        out = subprocess.run(
            ["git", "show", f"HEAD:{REGISTER.relative_to(ROOT).as_posix()}"],
            cwd=ROOT,
            capture_output=True,
            text=True,
            encoding="utf-8",
            check=False,
        )
    except OSError:
        return None
    if out.returncode != 0:
        return None  # new file, shallow checkout, or no git — not an error
    return sum(1 for s, _ in parse_existing(out.stdout).values() if s == "generated")


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--check", action="store_true", help="gate instead of writing")
    args = ap.parse_args()

    if not BUNDLE.exists():
        print(f"FAIL: no bundle at {BUNDLE}; run `make openapi`", file=sys.stderr)
        return 1
    spec = yaml.safe_load(BUNDLE.read_text(encoding="utf-8"))
    ops = operations(spec)

    existing_text = REGISTER.read_text(encoding="utf-8") if REGISTER.exists() else ""
    known = parse_existing(existing_text)
    rendered = render(ops, known)

    if not args.check:
        REGISTER.write_text(rendered, encoding="utf-8", newline="\n")
        generated = sum(1 for s, _ in known.values() if s == "generated")
        print(f"wrote {REGISTER.relative_to(ROOT)}")
        print(f"  operations : {len(ops)}  ({generated} generated, {len(ops) - generated} handwritten)")
        return 0

    problems: list[str] = []
    if not existing_text:
        problems.append("contracts/openapi/MIGRATION.md is missing — run `make openapi`")

    listed = set(known)
    in_bundle = {op["operationId"] for op in ops}
    for missing in sorted(in_bundle - listed):
        problems.append(f"operation '{missing}' is in the contract but not in MIGRATION.md")
    for stale in sorted(listed - in_bundle):
        problems.append(f"MIGRATION.md lists '{stale}', which no longer exists in the contract")
    for oid, (status, _notes) in sorted(known.items()):
        if status not in STATUSES:
            problems.append(f"'{oid}' has status '{status}'; expected one of {', '.join(STATUSES)}")

    if existing_text and existing_text != rendered and not problems:
        problems.append(
            "MIGRATION.md is not in the generated shape "
            "(run `make openapi` and commit the result)"
        )

    before = committed_generated_count()
    now = sum(1 for s, _ in known.values() if s == "generated")
    if before is not None and now < before:
        problems.append(
            f"the generated count went down ({before} -> {now}); §4 says it only moves one way"
        )

    if problems:
        print("FAIL: the endpoint migration register is out of date:", file=sys.stderr)
        for p in problems:
            print(f"  - {p}", file=sys.stderr)
        return 1

    print(f"OK: MIGRATION.md covers all {len(ops)} operations ({now} generated)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
