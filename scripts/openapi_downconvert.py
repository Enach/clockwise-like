#!/usr/bin/env python3
"""Rewrite the OpenAPI 3.1 bundle as an equivalent 3.0.3 document.

Why this exists: `contracts/openapi/openapi.yaml` is OpenAPI 3.1 (the assembler
stamps 3.1.0 and the fragments use 3.1 spellings — `type: [string, 'null']`,
`const:`, schema-level `examples:`). oapi-codegen parses with kin-openapi,
which reads 3.0. Feeding it the 3.1 bundle either fails or silently produces
wrong types, so `make openapi` converts first and hands the converted copy to
the generator.

The output is a **temporary build input**, never committed and never served.
The bundle stays 3.1 because openapi-typescript and the Swagger UI read 3.1
correctly and because downgrading the contract to suit one generator would be
the contract following the code again.

Conversions (each is the standard 3.1 -> 3.0 mapping):
  type: [T, 'null']              -> type: T        + nullable: true
  oneOf: [X, {type: 'null'}]     -> X (allOf-wrapped if X is a $ref) + nullable: true
  const: V                       -> enum: [V]
  examples: [a, b]  (in schemas) -> example: a
Anything else is copied verbatim. When oapi-codegen gains 3.1 support this
script and its call site in scripts/openapi_gen_go.sh both go away.

Usage: openapi_downconvert.py <in.yaml> <out.yaml>
Exit codes: 0 ok, 1 unconvertible construct found (message names it).
"""

from __future__ import annotations

import pathlib
import sys

import yaml

problems: list[str] = []


def convert(node, trail: str = ""):
    if isinstance(node, list):
        return [convert(v, f"{trail}[{i}]") for i, v in enumerate(node)]
    if not isinstance(node, dict):
        return node

    out = {}
    for key, value in node.items():
        sub = f"{trail}/{key}"

        if key == "type" and isinstance(value, list):
            non_null = [t for t in value if t != "null"]
            if len(non_null) != 1:
                # 3.0 has no union types at all; a real union would have to be
                # modelled as oneOf in the fragment, so flag rather than guess.
                problems.append(f"{sub}: cannot express type union {value} in 3.0")
                out[key] = value
                continue
            out["type"] = non_null[0]
            if len(non_null) != len(value):
                out["nullable"] = True
            continue

        if key == "const":
            out["enum"] = [value]
            continue

        if key == "examples" and isinstance(value, list):
            # A list here is the 3.1 schema keyword. The Media Object /
            # Parameter Object `examples` is a mapping and is valid in 3.0, so
            # it is left alone.
            if value:
                out["example"] = convert(value[0], sub)
            continue

        if key == "oneOf" and isinstance(value, list):
            branches = [b for b in value if b != {"type": "null"}]
            nullable = len(branches) != len(value)
            branches = [convert(b, sub) for b in branches]
            if not nullable:
                out[key] = branches
                continue
            if len(branches) == 1 and "$ref" in branches[0]:
                # A $ref's siblings are ignored in 3.0, so nullable has to go on
                # a wrapper schema rather than next to the $ref.
                out["allOf"] = branches
            elif len(branches) == 1:
                out.update(branches[0])
            else:
                out["oneOf"] = branches
            out["nullable"] = True
            continue

        out[key] = convert(value, sub)
    return out


def main() -> int:
    if len(sys.argv) != 3:
        print(__doc__, file=sys.stderr)
        return 1
    src, dst = pathlib.Path(sys.argv[1]), pathlib.Path(sys.argv[2])
    spec = yaml.safe_load(src.read_text(encoding="utf-8"))

    spec = convert(spec)
    spec["openapi"] = "3.0.3"

    if problems:
        print("FAIL: cannot downconvert the bundle to 3.0:", file=sys.stderr)
        for p in problems:
            print(f"  - {p}", file=sys.stderr)
        return 1

    dst.write_text(
        "# GENERATED BUILD INPUT - DO NOT EDIT, DO NOT COMMIT.\n"
        "# 3.0.3 rendering of contracts/openapi/openapi.yaml for oapi-codegen only.\n"
        + yaml.safe_dump(spec, sort_keys=False, width=100, allow_unicode=True),
        encoding="utf-8",
        newline="\n",
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
