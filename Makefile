# Paceday — the one entrypoint (docs/factory/README.md §3).
#
# Every target is safe to run from any directory: paths are absolute, derived
# from this file's own location, so `make -f /path/to/Makefile verify` behaves
# the same as `make verify` at the repo root.
#
# Recipes run under `bash -euo pipefail` so that a failure inside a pipeline is
# a failure of the target, not a silently swallowed exit code.
SHELL := /usr/bin/env bash
.SHELLFLAGS := -euo pipefail -c

ROOT := $(patsubst %/,%,$(dir $(abspath $(lastword $(MAKEFILE_LIST)))))
BUNDLE := $(ROOT)/contracts/openapi/openapi.yaml
# The `web` repo, as `make clone-frontend` lays it out. Override for a checkout
# somewhere else: `make openapi WEB=~/src/smart-calendar-flow`.
WEB ?= $(ROOT)/../smart-calendar-flow
# Scratch space for the verify summary. Gitignored; safe to delete.
VERIFY_DIR := $(ROOT)/.verify

# CLAUDE.md: backend and mcp hold 75–80%. The floor is the gate; the ceiling is
# a review conversation, not something a script can judge.
BACKEND_COVERAGE_MIN ?= 75
MCP_COVERAGE_MIN ?= 75

# Versions are pinned in one place so an agent that hits a missing tool is told
# exactly what to install, and so two machines generate identical bytes.
GOLANGCI_LINT_VERSION := v1.64.8
GOLANGCI_LINT_INSTALL := go install github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

# Re-entering this makefile by absolute path keeps sub-makes cwd-independent too.
SELF := $(MAKE) --no-print-directory -f $(ROOT)/Makefile

# `make` with no target used to run clone-frontend, which cloned a repo at
# someone who typed `make` by accident. It now prints the target list.
.DEFAULT_GOAL := help

.PHONY: help clone-frontend install-hooks db-shell \
        openapi openapi-check lint test coverage e2e \
        verify verify-banner verify-summary

help:
	@echo "Paceday (api repo) — targets"
	@echo "  verify         lint, openapi-check, test, coverage, e2e — fail-fast, prints a PR-pasteable summary"
	@echo "  lint           golangci-lint over backend/ and mcp/"
	@echo "  test           backend suite (throwaway postgres, via docker) + mcp suite"
	@echo "  coverage       the same suites with profiles, gated at $(BACKEND_COVERAGE_MIN)%/$(MCP_COVERAGE_MIN)%"
	@echo "  openapi        assemble the contract, regenerate Go code, refresh MIGRATION.md, regenerate web types"
	@echo "  openapi-check  the same, as a gate: fails on any drift"
	@echo "  e2e            the Playwright suite in e2e/ (it brings its own compose stack up)"
	@echo "  clone-frontend / install-hooks / db-shell — local setup"

## Clone (or update) Enach/smart-calendar-flow as a sibling for local frontend dev
# Unchanged behaviour, but anchored to $(WEB) rather than to the caller's cwd,
# so it cannot clone a second copy somewhere unexpected.
clone-frontend:
	@if [ -d "$(WEB)" ]; then \
		echo "Updating $(WEB)"; \
		cd "$(WEB)" && git pull --ff-only; \
	else \
		echo "Cloning into $(WEB)"; \
		git clone git@github.com:Enach/smart-calendar-flow.git "$(WEB)"; \
	fi

## Point git at the committed hook scripts
install-hooks:
	git -C $(ROOT) config core.hooksPath .githooks
	@echo "Git hooks installed — pre-commit will run lint + build + openapi-check."

## Open a psql shell into the running postgres container
db-shell:
	cd $(ROOT) && docker compose exec postgres psql -U clockwise -d clockwise

# ── Contract ─────────────────────────────────────────────────────────────────
# The chain is one-way: fragments → bundle → generated code, in both repos.
# Anything downstream of the bundle is a build artifact that happens to be
# committed (factory §4), so `openapi` writes and `openapi-check` only reads.

## Assemble the bundle, regenerate Go code and the register, then the web repo
openapi:
	python3 $(ROOT)/scripts/openapi_assemble.py
	@# Register before codegen: it needs nothing but python, so a missing
	@# oapi-codegen does not also leave MIGRATION.md stale.
	python3 $(ROOT)/scripts/openapi_migration_report.py
	$(ROOT)/scripts/openapi_gen_go.sh
	@if [ -d "$(WEB)" ]; then \
		echo "-> regenerating web artifacts in $(WEB)"; \
		$(MAKE) --no-print-directory -C "$(WEB)" openapi CONTRACT="$(BUNDLE)"; \
	else \
		echo "note: no web repo at $(WEB) — its generated types were NOT refreshed."; \
		echo "      Run \`make clone-frontend\` (or pass WEB=...) before the frontend PR."; \
	fi

## Gate: bundle matches fragments, generated code matches bundle, register is complete
openapi-check:
	python3 $(ROOT)/scripts/openapi_assemble.py --check
	python3 $(ROOT)/scripts/openapi_migration_report.py --check
	$(ROOT)/scripts/openapi_gen_go.sh --check
	@if [ -d "$(WEB)" ]; then \
		$(MAKE) --no-print-directory -C "$(WEB)" openapi-check CONTRACT="$(BUNDLE)"; \
	else \
		echo "SKIPPED: web generated types (no repo at $(WEB)) — factory §2 gate 4 is only half checked."; \
	fi

# ── Static analysis ──────────────────────────────────────────────────────────

## golangci-lint over both Go modules
lint:
	@command -v golangci-lint >/dev/null 2>&1 || { \
		echo "golangci-lint not installed, install with: $(GOLANGCI_LINT_INSTALL)" >&2; \
		echo "  (.golangci.yml is a v1-schema config, so install a v1.x binary)" >&2; \
		exit 1; }
	@# A v2 binary rejects the committed v1-schema config with "unsupported
	@# version of the configuration", which reads like the config is corrupt.
	@# Say what is actually wrong, and leave the choice of fix to a human: both
	@# options change what gets linted, and .githooks/pre-commit shares this config.
	@if golangci-lint version 2>/dev/null | grep -qE 'version v?2\.'; then \
		echo "golangci-lint v2 is installed, but .golangci.yml is a v1-schema config." >&2; \
		echo "  downgrade: $(GOLANGCI_LINT_INSTALL)" >&2; \
		echo "  or migrate the config (separate, reviewable commit — note gosimple is folded into staticcheck in v2):" >&2; \
		echo "             golangci-lint migrate --config $(ROOT)/.golangci.yml" >&2; \
		echo "             https://golangci-lint.run/docs/product/migration-guide" >&2; \
		exit 1; \
	fi
	@echo "-> lint backend"
	cd $(ROOT)/backend && golangci-lint run --config $(ROOT)/.golangci.yml ./...
	@echo "-> lint mcp"
	cd $(ROOT)/mcp && GONOSUMDB='*' GOFLAGS='-mod=mod' golangci-lint run --config $(ROOT)/.golangci.yml ./...
	@echo "lint OK (generated files carry a DO NOT EDIT header and are excluded by golangci-lint)"

# ── Tests ────────────────────────────────────────────────────────────────────
# scripts/test-backend.sh runs the suite inside golang:1.25-alpine against a
# throwaway postgres, which is why the backend needs docker and mcp does not.

## Unit + contract tests for both modules
test:
	@command -v docker >/dev/null 2>&1 || { \
		echo "docker not installed — scripts/test-backend.sh needs it for the throwaway postgres." >&2; \
		echo "  Without docker the integration tests are skipped, and the coverage number is not the official one (CLAUDE.md)." >&2; \
		exit 1; }
	@echo "-> test backend (full suite, with postgres)"
	$(ROOT)/scripts/test-backend.sh
	@echo "-> test mcp"
	cd $(ROOT)/mcp && GONOSUMDB='*' GOFLAGS='-mod=mod' go test ./...

## The same suites with coverage profiles, gated
coverage:
	@command -v docker >/dev/null 2>&1 || { \
		echo "docker not installed — the official coverage number needs the full suite (CLAUDE.md)." >&2; \
		exit 1; }
	@echo "-> coverage backend"
	@# A coverage-instrumented run is not a substitute for the clean run in
	@# `make test`, so verify pays for both. The profile lands in backend/ because
	@# the suite runs with /app = backend mounted into the container.
	PACEDAY_GO_TEST_FLAGS="-coverprofile=coverage.out -covermode=atomic" $(ROOT)/scripts/test-backend.sh
	$(ROOT)/scripts/coverage-gate.sh $(ROOT)/backend/coverage.out $(BACKEND_COVERAGE_MIN) backend
	@echo "-> coverage mcp"
	cd $(ROOT)/mcp && GONOSUMDB='*' GOFLAGS='-mod=mod' go test ./... -coverprofile=coverage.out
	$(ROOT)/scripts/coverage-gate.sh $(ROOT)/mcp/coverage.out $(MCP_COVERAGE_MIN) mcp

## Playwright journeys against the compose stack (suite lives in e2e/)
e2e:
	@if [ ! -d "$(ROOT)/e2e" ]; then \
		echo "SKIPPED: no $(ROOT)/e2e directory yet — the suite is landing separately."; \
		echo "         Say so in the PR body; factory §3 rule 2 forbids silence, not absence."; \
		exit 0; \
	fi; \
	if [ ! -d "$(ROOT)/e2e/node_modules" ]; then \
		echo "e2e dependencies not installed, install with: cd $(ROOT)/e2e && npm install && npx playwright install --with-deps" >&2; \
		exit 1; \
	fi; \
	cd $(ROOT)/e2e; \
	if grep -q '"test:e2e"' package.json; then npm run test:e2e; else npm test; fi
	@# The suite owns its own compose stack (e2e/global-setup.ts), so there is
	@# nothing to start here. Which script name it exposes is its business:
	@# test:e2e if it has one, otherwise the conventional `npm test`.

# ── The gate ─────────────────────────────────────────────────────────────────

## Everything, in factory §3 order, fail-fast, with a pasteable summary
verify:
	@rm -rf "$(VERIFY_DIR)"
	@mkdir -p "$(VERIFY_DIR)"
	@$(SELF) verify-banner | tee "$(VERIFY_DIR)/banner.txt"
	@$(SELF) lint          && echo "lint           PASS" >> "$(VERIFY_DIR)/steps.txt"
	@$(SELF) openapi-check && echo "openapi-check  PASS" >> "$(VERIFY_DIR)/steps.txt"
	@$(SELF) test          && echo "test           PASS" >> "$(VERIFY_DIR)/steps.txt"
	@COVERAGE_SUMMARY_FILE="$(VERIFY_DIR)/coverage.txt" $(SELF) coverage \
		&& echo "coverage       PASS" >> "$(VERIFY_DIR)/steps.txt"
	@$(SELF) e2e
	@if [ -d "$(ROOT)/e2e" ]; then \
		echo "e2e            PASS" >> "$(VERIFY_DIR)/steps.txt"; \
	else \
		echo "e2e            SKIPPED (no e2e/ directory)" >> "$(VERIFY_DIR)/steps.txt"; \
	fi
	@$(SELF) verify-summary

# Printed before anything runs, and again inside the summary: pasted output is
# worthless if you cannot tell which commits produced it.
verify-banner:
	@echo "make verify — Paceday"
	@echo "date : $$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
	@echo "api  : $$(git -C '$(ROOT)' describe --always --dirty 2>/dev/null || echo '<not a git checkout>')" \
	      "on $$(git -C '$(ROOT)' rev-parse --abbrev-ref HEAD 2>/dev/null || echo '-')  [$(ROOT)]"
	@if [ -d "$(WEB)" ]; then \
		echo "web  : $$(git -C '$(WEB)' describe --always --dirty 2>/dev/null || echo '<not a git checkout>')" \
		     "on $$(git -C '$(WEB)' rev-parse --abbrev-ref HEAD 2>/dev/null || echo '-')  [$(WEB)]"; \
	else \
		echo "web  : absent at $(WEB) — cross-repo checks were skipped"; \
	fi

verify-summary:
	@echo ""
	@echo "===== BEGIN make verify summary (paste into the PR body) ====="
	@cat "$(VERIFY_DIR)/banner.txt"
	@echo ""
	@cat "$(VERIFY_DIR)/steps.txt"
	@if [ -s "$(VERIFY_DIR)/coverage.txt" ]; then echo ""; cat "$(VERIFY_DIR)/coverage.txt"; fi
	@echo "===== END make verify summary ====="
