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

# The floors are what the suites actually hold today, measured, not the target.
# CLAUDE.md claimed 75–80% for both: mcp does hold it (84.0%), the backend does
# not (38.0% once generated code is excluded, 19.6% with it). A gate set to an
# aspiration fails every run and gets bypassed, which is worse than a gate set
# to the truth and raised deliberately.
#
# These are RATCHETS. Raise one when coverage rises; never lower one to turn a
# red run green. Closing the backend's gap to 75% is tracked as work, not as a
# number in this file.
BACKEND_COVERAGE_MIN ?= 64
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
        openapi openapi-check openapi-check-web lint test coverage e2e \
        test-backend test-mcp test-fake-google test-fileserver test-nginx test-compose \
        coverage-backend coverage-mcp require-docker print-suites \
        verify verify-banner verify-summary

help:
	@echo "Paceday (api repo) — targets"
	@echo "  verify         lint, openapi-check, test, coverage, e2e — fail-fast, prints a PR-pasteable summary"
	@echo "  lint           golangci-lint over backend/ and mcp/"
	@echo "  test           backend suite (throwaway postgres, via docker) + mcp suite"
	@echo "                 one suite: test-backend test-mcp test-fake-google test-fileserver test-nginx test-compose"
	@echo "  coverage       the same suites with profiles, gated at $(BACKEND_COVERAGE_MIN)%/$(MCP_COVERAGE_MIN)% (coverage-backend, coverage-mcp)"
	@echo "  openapi        assemble the contract, regenerate Go code, refresh MIGRATION.md, regenerate web types"
	@echo "  openapi-check  the same, as a gate: fails on any drift (OPENAPI_CROSS_REPO=skip: not the web half)"
	@echo "  openapi-check-web  only the web half: the web repo's generated types match this bundle"
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
# OPENAPI_CROSS_REPO=skip leaves out the web half (the web repo's generated
# types against this bundle) and only that. ci.yml sets it on pushes to main:
# a paired contract change merges api first and web minutes later, so the two
# mains disagree in between by design. The check is not dropped, it moves:
# release.yml runs `openapi-check-web` on the exact pair of main SHAs it would
# publish, and does not publish a pair that fails. PRs keep the full check.
OPENAPI_CROSS_REPO ?= check
openapi-check:
	python3 $(ROOT)/scripts/openapi_assemble.py --check
	python3 $(ROOT)/scripts/openapi_migration_report.py --check
	$(ROOT)/scripts/openapi_gen_go.sh --check
	@if [ "$(OPENAPI_CROSS_REPO)" = "skip" ]; then \
		echo "::notice title=openapi-check::cross-repo half (web generated types vs this bundle) deferred to release.yml (OPENAPI_CROSS_REPO=skip)"; \
	elif [ -d "$(WEB)" ]; then \
		$(SELF) openapi-check-web; \
	else \
		echo "SKIPPED: web generated types (no repo at $(WEB)) — factory §2 gate 4 is only half checked."; \
	fi

## The cross-repo half alone: the web repo's generated types match this bundle.
# Fails, rather than skips, when there is no web repo: release.yml relies on it.
openapi-check-web:
	@test -d "$(WEB)" || { echo "no web repo at $(WEB); pass WEB=..." >&2; exit 1; }
	$(MAKE) --no-print-directory -C "$(WEB)" openapi-check CONTRACT="$(BUNDLE)" OPENAPI_CROSS_REPO=check

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
# One target per suite, so ci.yml can run them as parallel jobs; `test` runs
# them all, in this order, fail-fast, exactly as before. These lists are the
# single source: scripts/ci_suites_check.py (run by ci.yml) fails when the jobs
# in ci.yml stop running exactly these targets. Add a suite here AND in ci.yml.
TEST_SUITES := backend mcp fake-google fileserver nginx compose
COVERAGE_SUITES := backend mcp
# The steps `verify` runs, in its recipe below, in this order.
VERIFY_STEPS := lint openapi-check test coverage e2e

test: $(addprefix test-,$(TEST_SUITES))

# `make -j test` keeps the suites one at a time and in order, as before the
# split (GNU make >= 4.4 serialises just these prerequisites; older makes treat
# any .NOTPARALLEL as the whole makefile). Note the backend suite's postgres is
# published on one fixed port (15432), so test-backend and coverage-backend
# cannot run at once on one host either.
.NOTPARALLEL: test coverage

# For scripts/ci_suites_check.py.
print-suites:
	@echo "TEST_SUITES=$(TEST_SUITES)"
	@echo "COVERAGE_SUITES=$(COVERAGE_SUITES)"
	@echo "VERIFY_STEPS=$(VERIFY_STEPS)"

require-docker:
	@command -v docker >/dev/null 2>&1 || { \
		echo "docker not installed — scripts/test-backend.sh needs it for the throwaway postgres." >&2; \
		echo "  Without docker the integration tests are skipped, and the coverage number is not the official one (CLAUDE.md)." >&2; \
		exit 1; }

# mcp appends to GOFLAGS instead of replacing it: ci.yml sets GOFLAGS=-count=1
# so a restored Go build cache can never replay a test result instead of running it.
test-backend: require-docker
	@echo "-> test backend (full suite, with postgres)"
	$(ROOT)/scripts/test-backend.sh

test-mcp:
	@echo "-> test mcp"
	cd $(ROOT)/mcp && GONOSUMDB='*' GOFLAGS="-mod=mod $${GOFLAGS:-}" go test ./...

test-fake-google:
	@echo "-> test e2e/fake-google (the PAC-43 calendar stand-in and sink)"
	cd $(ROOT)/e2e/fake-google && go vet ./... && go test ./...

test-fileserver:
	@echo "-> test docker/fileserver (PAC-81 Part B: API-base injection without inline script)"
	cd $(ROOT)/docker/fileserver && go vet ./... && go test ./...

test-nginx:
	@echo "-> test nginx (PAC-81 Parts B and C: the shipped edge config against a stub upstream, in docker; ~2.5 min)"
	cd $(ROOT)/nginx/test && go vet ./... && go test -timeout 15m ./...

test-compose:
	@echo "-> test compose (PAC-81 Part C: the shipped production compose files under Docker Compose v2; builds images, ~8 min)"
	cd $(ROOT)/compose-test && go vet ./... && go test -timeout 40m ./...

## The same suites with coverage profiles, gated
coverage: $(addprefix coverage-,$(COVERAGE_SUITES))

coverage-backend:
	@command -v docker >/dev/null 2>&1 || { \
		echo "docker not installed — the official coverage number needs the full suite (CLAUDE.md)." >&2; \
		exit 1; }
	@echo "-> coverage backend"
	@# A coverage-instrumented run is not a substitute for the clean run in
	@# `make test`, so verify pays for both. The profile lands in backend/ because
	@# the suite runs with /app = backend mounted into the container.
	PACEDAY_GO_TEST_FLAGS="-coverprofile=coverage.out -covermode=atomic" $(ROOT)/scripts/test-backend.sh
	$(ROOT)/scripts/coverage-gate.sh $(ROOT)/backend/coverage.out $(BACKEND_COVERAGE_MIN) backend

coverage-mcp:
	@echo "-> coverage mcp"
	cd $(ROOT)/mcp && GONOSUMDB='*' GOFLAGS="-mod=mod $${GOFLAGS:-}" go test ./... -coverprofile=coverage.out
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
	@$(SELF) openapi-check && if [ "$(OPENAPI_CROSS_REPO)" = "skip" ]; then \
		echo "openapi-check  PASS (api half; cross-repo half deferred to release)" >> "$(VERIFY_DIR)/steps.txt"; \
	else echo "openapi-check  PASS" >> "$(VERIFY_DIR)/steps.txt"; fi
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
