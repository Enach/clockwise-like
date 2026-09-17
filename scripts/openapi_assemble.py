#!/usr/bin/env python3
"""Assemble per-domain OpenAPI fragments into one bundled spec.

Source of truth:  contracts/openapi/paths/<domain>.yaml   (hand-authored / agent-authored)
Build artifact:   contracts/openapi/openapi.yaml          (generated, committed)

The bundled file is what oapi-codegen, openapi-typescript and the Swagger UI
consume. It is committed so that `make verify` can prove it is in sync with the
fragments (drift check), and so consumers never need the assembler.

Fragments are merged, not $ref-linked, because both code generators want a
single self-contained document and $ref-across-files support is uneven.

Exit codes: 0 ok, 1 merge conflict or dangling $ref.
"""

from __future__ import annotations

import argparse
import copy
import pathlib
import re
import sys

import yaml

ROOT = pathlib.Path(__file__).resolve().parent.parent
FRAGMENT_DIR = ROOT / "contracts" / "openapi" / "paths"
BUNDLE = ROOT / "contracts" / "openapi" / "openapi.yaml"

# Component sections that fragments may contribute to. All are merged by name.
COMPONENT_SECTIONS = (
    "schemas",
    "responses",
    "parameters",
    "requestBodies",
    "headers",
    "securitySchemes",
)

# Components that several domains legitimately define. They must be structurally
# identical; only human-facing prose may differ. First writer wins, later
# definitions are checked for structural equality.
SHARED_COMPONENTS = {"ErrorResponse", "Unauthorized", "Forbidden", "NotFound", "InternalError"}

INFO = {
    "title": "Paceday API",
    "version": "0.1.0",
    "description": (
        "The Paceday HTTP API.\n\n"
        "**This document is the contract.** It was reverse-engineered from the "
        "running Go implementation in `backend/api/` and is therefore descriptive "
        "of what the server does today, including behaviour that is known to be "
        "wrong. Operations and properties carrying `x-uncertain` were not fully "
        "traceable to source and must not be treated as settled.\n\n"
        "Going forward the direction reverses: this file is edited first, code "
        "is generated and verified against it, and `make verify` fails on drift."
    ),
}


class MergeError(Exception):
    pass


def structural(node):
    """Strip human-facing prose so two definitions can be compared for meaning."""
    if isinstance(node, dict):
        return {
            k: structural(v)
            for k, v in sorted(node.items())
            if k not in ("description", "summary", "example", "examples")
        }
    if isinstance(node, list):
        return [structural(v) for v in node]
    return node


def load_fragments() -> list[tuple[str, dict]]:
    frags = []
    for path in sorted(FRAGMENT_DIR.glob("*.yaml")):
        with path.open(encoding="utf-8") as fh:
            doc = yaml.safe_load(fh)
        if not isinstance(doc, dict):
            raise MergeError(f"{path.name}: expected a mapping at the top level")
        frags.append((path.name, doc))
    if not frags:
        raise MergeError(f"no fragments found in {FRAGMENT_DIR}")
    return frags


def merge(frags: list[tuple[str, dict]]) -> tuple[dict, list[str]]:
    problems: list[str] = []
    paths: dict = {}
    components: dict[str, dict] = {section: {} for section in COMPONENT_SECTIONS}
    owner_of_path: dict[str, str] = {}
    owner_of_component: dict[tuple[str, str], str] = {}
    owner_of_op: dict[str, str] = {}

    for name, doc in frags:
        for route, item in (doc.get("paths") or {}).items():
            if route in paths:
                # Two fragments describing the same route: merge per-method,
                # conflict only if the same method is defined twice.
                for method, op in item.items():
                    if method in paths[route]:
                        problems.append(
                            f"duplicate operation {method.upper()} {route}: "
                            f"{owner_of_path[route]} and {name}"
                        )
                    else:
                        paths[route][method] = op
                continue
            paths[route] = item
            owner_of_path[route] = name

        # operationId uniqueness across the whole document
        for route, item in (doc.get("paths") or {}).items():
            for method, op in item.items():
                if not isinstance(op, dict):
                    continue
                oid = op.get("operationId")
                if not oid:
                    problems.append(f"{name}: {method.upper()} {route} has no operationId")
                    continue
                if oid in owner_of_op and owner_of_op[oid] != f"{name}:{route}:{method}":
                    problems.append(
                        f"duplicate operationId '{oid}': "
                        f"{owner_of_op[oid]} and {name}:{route}:{method}"
                    )
                owner_of_op[oid] = f"{name}:{route}:{method}"

        frag_components = doc.get("components") or {}
        for section in COMPONENT_SECTIONS:
            for cname, cdef in (frag_components.get(section) or {}).items():
                key = (section, cname)
                if cname not in components[section]:
                    components[section][cname] = cdef
                    owner_of_component[key] = name
                    continue
                if structural(components[section][cname]) == structural(cdef):
                    continue  # identical redefinition, harmless
                if cname in SHARED_COMPONENTS:
                    problems.append(
                        f"shared {section[:-1]} '{cname}' differs structurally between "
                        f"{owner_of_component[key]} and {name}"
                    )
                else:
                    problems.append(
                        f"{section[:-1]} name collision '{cname}': "
                        f"{owner_of_component[key]} and {name} define it differently"
                    )
        unknown = set(frag_components) - set(COMPONENT_SECTIONS)
        if unknown:
            problems.append(f"{name}: unhandled components section(s) {sorted(unknown)}")

    security_schemes = components["securitySchemes"]
    if not security_schemes:
        problems.append("no securitySchemes defined by any fragment")

    spec = {
        "openapi": "3.1.0",
        "info": copy.deepcopy(INFO),
        "servers": [
            {"url": "http://localhost:8080", "description": "backend, direct"},
            {"url": "http://localhost", "description": "through the nginx reverse proxy"},
        ],
        # Default: every operation requires auth. Public operations opt out with
        # `security: []`, which is the safer default to get wrong.
        "security": [{"bearerAuth": []}, {"cookieAuth": []}]
        if "cookieAuth" in security_schemes
        else [{"bearerAuth": []}],
        "paths": dict(sorted(paths.items())),
        "components": {
            section: dict(sorted(components[section].items()))
            for section in COMPONENT_SECTIONS
            if components[section]
        },
    }
    return spec, problems


REF_RE = re.compile(r"^#/components/([A-Za-z]+)/(.+)$")


def check_refs(spec: dict) -> list[str]:
    known = {
        section: set(spec["components"].get(section, {})) for section in COMPONENT_SECTIONS
    }
    dangling: list[str] = []
    used: set[tuple[str, str]] = set()

    def walk(node, trail):
        if isinstance(node, dict):
            ref = node.get("$ref")
            if isinstance(ref, str):
                m = REF_RE.match(ref)
                if not m:
                    dangling.append(f"{trail}: unsupported $ref form '{ref}'")
                elif m.group(1) not in known:
                    dangling.append(f"{trail}: $ref into unknown section '{m.group(1)}'")
                elif m.group(2) not in known[m.group(1)]:
                    dangling.append(
                        f"{trail}: $ref to unknown {m.group(1)[:-1]} '{m.group(2)}'"
                    )
                else:
                    used.add((m.group(1), m.group(2)))
            for k, v in node.items():
                walk(v, f"{trail}/{k}")
        elif isinstance(node, list):
            for i, v in enumerate(node):
                walk(v, f"{trail}[{i}]")

    walk(spec["paths"], "paths")
    walk(spec["components"], "components")

    # A component referenced by nothing is not an error, but it is a smell worth
    # surfacing: usually a leftover from a handler that was deleted or renamed.
    # securitySchemes are referenced by name under `security`, not by $ref.
    for section in COMPONENT_SECTIONS:
        if section == "securitySchemes":
            continue
        for orphan in sorted(known[section] - {n for s, n in used if s == section}):
            dangling.append(f"orphan {section[:-1]} (defined, never referenced): {orphan}")
    return dangling


def count_uncertain(spec: dict) -> list[str]:
    hits: list[str] = []

    def walk(node, trail):
        if isinstance(node, dict):
            if "x-uncertain" in node:
                hits.append(f"{trail}: {node['x-uncertain']}")
            for k, v in node.items():
                walk(v, f"{trail}/{k}")
        elif isinstance(node, list):
            for i, v in enumerate(node):
                walk(v, f"{trail}[{i}]")

    walk(spec, "")
    return hits


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument(
        "--check",
        action="store_true",
        help="fail if the committed bundle differs from a fresh assembly (drift gate)",
    )
    args = ap.parse_args()

    try:
        frags = load_fragments()
    except MergeError as exc:
        print(f"FAIL: {exc}", file=sys.stderr)
        return 1

    spec, problems = merge(frags)
    problems += check_refs(spec)

    if problems:
        print("FAIL: the OpenAPI fragments do not assemble cleanly:", file=sys.stderr)
        for p in problems:
            print(f"  - {p}", file=sys.stderr)
        # Orphan schemas alone are a warning, not a failure.
        hard = [p for p in problems if not p.startswith("orphan ")]
        if hard:
            return 1
        print("  (orphans are warnings only; continuing)", file=sys.stderr)

    rendered = yaml.safe_dump(spec, sort_keys=False, width=100, allow_unicode=True)
    header = (
        "# GENERATED FILE - DO NOT EDIT.\n"
        "# Assembled from contracts/openapi/paths/*.yaml by scripts/openapi_assemble.py.\n"
        "# Edit a fragment, then run `make openapi` (or `make verify`, which checks for drift).\n"
    )
    rendered = header + rendered

    if args.check:
        if not BUNDLE.exists():
            print(f"FAIL: {BUNDLE} does not exist; run `make openapi`", file=sys.stderr)
            return 1
        if BUNDLE.read_text(encoding="utf-8") != rendered:
            print(
                "FAIL: contracts/openapi/openapi.yaml is out of date with its fragments.\n"
                "      Run `make openapi` and commit the result.",
                file=sys.stderr,
            )
            return 1
        print(f"OK: bundle is in sync ({len(spec['paths'])} paths)")
        return 0

    BUNDLE.write_text(rendered, encoding="utf-8", newline="\n")

    ops = sum(
        1
        for item in spec["paths"].values()
        for m, op in item.items()
        if isinstance(op, dict) and m in ("get", "put", "post", "delete", "patch", "head", "options")
    )
    uncertain = count_uncertain(spec)
    public = sum(
        1
        for item in spec["paths"].values()
        for m, op in item.items()
        if isinstance(op, dict) and op.get("security") == []
    )
    print(f"wrote {BUNDLE.relative_to(ROOT)}")
    print(f"  paths      : {len(spec['paths'])}")
    print(f"  operations : {ops}  ({public} public, {ops - public} authenticated)")
    for section in COMPONENT_SECTIONS:
        count = len(spec["components"].get(section, {}))
        if count:
            print(f"  {section:<11}: {count}")
    print(f"  x-uncertain: {len(uncertain)}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
