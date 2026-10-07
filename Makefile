# `make check` runs everything CI runs. A change isn't done until it passes.
#
# Tools are pinned here and installed into .bin/ on first use. Change a version by
# editing it below; the next run installs the new one.

SHELL := /bin/bash
.SHELLFLAGS := -euo pipefail -c
.DEFAULT_GOAL := check

GOLANGCI_LINT_VERSION := v2.14.0
GOVULNCHECK_VERSION   := v1.8.0
GORELEASER_VERSION    := v2.18.2

BIN           := $(CURDIR)/.bin
GOLANGCI_LINT := $(BIN)/golangci-lint-$(GOLANGCI_LINT_VERSION)
GOVULNCHECK   := $(BIN)/govulncheck-$(GOVULNCHECK_VERSION)
GORELEASER    := $(BIN)/goreleaser-$(GORELEASER_VERSION)

# Every Go step uses the toolchain go.mod pins, so a newer local Go generates and checks
# the same code CI does.
export GOTOOLCHAIN := $(shell awk '/^toolchain /{print $$2}' go.mod 2>/dev/null)

# Go steps are skipped, visibly, until the repo has a go.mod.
REQUIRE_GO = if [ ! -f go.mod ]; then echo "$@: skipped, no go.mod yet"; exit 0; fi

.PHONY: check quick fmt fmt-check lint vet generate generate-check test e2e conformance extension-test live live-affected live-smoke launchers launcher-kit harness-table harness-table-check docs-cli docs-check docs-preview docs-links vuln tools web web-check web-e2e install dev release-snapshot release release-check sandbox sandbox-clean

## check: format check, lint, vet, generated code, core size, harness table, docs reference, tests, e2e, extension tests, vulnerabilities
check: fmt-check lint vet generate-check harness-table-check docs-check test e2e extension-test vuln
	@echo "make check: OK"

## quick: the fast checks to run before asking for review or landing: format, lint, vet, generated code, core size (no tests)
quick: fmt-check lint vet generate-check
	@echo "make quick: OK"

## fmt: rewrite Go files with gofumpt and goimports
fmt: $(GOLANGCI_LINT)
	@$(REQUIRE_GO); $(GOLANGCI_LINT) fmt

fmt-check: $(GOLANGCI_LINT)
	@$(REQUIRE_GO); \
	out="$$($(GOLANGCI_LINT) fmt --diff)" || exit 1; \
	if [ -n "$$out" ]; then echo "$$out"; echo "Run: make fmt"; exit 1; fi

# The live suite builds only with its tag, so it is linted and vetted on its own.
lint: $(GOLANGCI_LINT)
	@$(REQUIRE_GO); $(GOLANGCI_LINT) run ./... && $(GOLANGCI_LINT) run --build-tags live ./e2e/live/...

vet:
	@$(REQUIRE_GO); go vet ./... && go vet -tags live ./e2e/live/...

## generate: regenerate code from spec/openapi.yaml
generate:
	@$(REQUIRE_GO); go generate ./...

# Fails if regenerating changes anything, including creating new files.
generate-check:
	@$(REQUIRE_GO); \
	before="$$(git status --porcelain --untracked-files=all; git diff | shasum)"; \
	go generate ./... || exit 1; \
	after="$$(git status --porcelain --untracked-files=all; git diff | shasum)"; \
	if [ "$$before" != "$$after" ]; then \
		git status --short; echo "Generated code is out of date. Run: make generate"; exit 1; \
	fi

$(GOVULNCHECK):
	@mkdir -p $(BIN)/tmp
	GOBIN=$(BIN)/tmp go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
	@mv $(BIN)/tmp/govulncheck $@
