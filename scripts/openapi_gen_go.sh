#!/usr/bin/env bash
# Generate backend/api/gen from the contract bundle — or prove it is in sync.
#
# `make openapi` and `make openapi-check` both go through this one script so the
# two can never diverge: a drift check that generated code differently from the
# generator would report drift that does not exist, and hide drift that does.
#
#   openapi_gen_go.sh            regenerate in place
#   openapi_gen_go.sh --check    regenerate into a temp dir and diff (exit 1 on drift)
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BUNDLE="$ROOT/contracts/openapi/openapi.yaml"
CONFIG="$ROOT/backend/oapi-codegen.yaml"
TARGET="$ROOT/backend/api/gen/paceday.gen.go"

# Pinned so that everyone's `make openapi` produces the same bytes; a generator
# version bump is a deliberate commit that shows up as a diff in the generated
# file, not a surprise in someone's PR.
OAPI_CODEGEN_VERSION="v2.4.1"
INSTALL_CMD="go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@${OAPI_CODEGEN_VERSION}"

CHECK=0
if [ "${1:-}" = "--check" ]; then
  CHECK=1
elif [ -n "${1:-}" ]; then
  echo "usage: $(basename "$0") [--check]" >&2
  exit 2
fi

if ! command -v oapi-codegen >/dev/null 2>&1; then
  echo "oapi-codegen not installed, install with: ${INSTALL_CMD}" >&2
  echo "  (then make sure \$(go env GOPATH)/bin is on your PATH)" >&2
  exit 1
fi
if ! command -v python3 >/dev/null 2>&1; then
  echo "python3 not installed — it assembles and converts the contract" >&2
  exit 1
fi
if [ ! -f "$BUNDLE" ]; then
  echo "no bundle at $BUNDLE — run: make openapi" >&2
  exit 1
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# oapi-codegen reads OpenAPI 3.0; the contract is 3.1. See the script's docstring.
python3 "$ROOT/scripts/openapi_downconvert.py" "$BUNDLE" "$tmp/openapi-3.0.yaml"

if [ "$CHECK" -eq 1 ]; then
  out="$tmp/paceday.gen.go"
else
  out="$TARGET"
  mkdir -p "$(dirname "$out")"
fi

# cd into backend so relative paths inside the config resolve the same way they
# do when a human runs oapi-codegen by hand from the module root.
(cd "$ROOT/backend" && oapi-codegen -config "$CONFIG" -o "$out" "$tmp/openapi-3.0.yaml")

if [ "$CHECK" -eq 1 ]; then
  if [ ! -f "$TARGET" ]; then
    echo "FAIL: ${TARGET#"$ROOT"/} does not exist; run \`make openapi\` and commit it" >&2
    exit 1
  fi
  if ! diff -u "$TARGET" "$out" > "$tmp/drift.diff" 2>&1; then
    echo "FAIL: backend/api/gen is out of date with the contract." >&2
    echo "      Run \`make openapi\` and commit the result. First 60 diff lines:" >&2
    head -n 60 "$tmp/drift.diff" >&2
    echo "      ($(wc -l < "$tmp/drift.diff" | tr -d ' ') diff lines total)" >&2
    exit 1
  fi
  echo "OK: backend/api/gen matches the contract"
else
  echo "wrote ${out#"$ROOT"/}"
fi
