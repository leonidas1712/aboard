# `make check` runs everything CI runs. A change isn't done until it passes.
#
# Tools are pinned here and installed into .bin/ on first use. Change a version by
# editing it below; the next run installs the new one.

SHELL := /bin/bash
.SHELLFLAGS := -euo pipefail -c
.DEFAULT_GOAL := check

GOLANGCI_LINT_VERSION := v2.14.0
GOVULNCHECK_VERSION   := v1.8.0

BIN           := $(CURDIR)/.bin
GOLANGCI_LINT := $(BIN)/golangci-lint-$(GOLANGCI_LINT_VERSION)
GOVULNCHECK   := $(BIN)/govulncheck-$(GOVULNCHECK_VERSION)

# Go steps are skipped, visibly, until the repo has a go.mod.
REQUIRE_GO = if [ ! -f go.mod ]; then echo "$@: skipped, no go.mod yet"; exit 0; fi

.PHONY: check fmt fmt-check lint vet generate generate-check test e2e vuln tools core-size

## check: format check, lint, vet, generated code, core size, tests, e2e, vulnerabilities
check: fmt-check lint vet generate-check core-size test e2e vuln
	@echo "make check: OK"

## fmt: rewrite Go files with gofumpt and goimports
fmt: $(GOLANGCI_LINT)
	@$(REQUIRE_GO); $(GOLANGCI_LINT) fmt

fmt-check: $(GOLANGCI_LINT)
	@$(REQUIRE_GO); \
	out="$$($(GOLANGCI_LINT) fmt --diff)"; \
	if [ -n "$$out" ]; then echo "$$out"; echo "Run: make fmt"; exit 1; fi

lint: $(GOLANGCI_LINT)
	@$(REQUIRE_GO); $(GOLANGCI_LINT) run ./...

vet:
	@$(REQUIRE_GO); go vet ./...

## generate: regenerate code from spec/openapi.yaml
generate:
	@$(REQUIRE_GO); go generate ./...

# Fails if regenerating changes anything, including creating new files.
generate-check:
	@$(REQUIRE_GO); \
	before="$$(git status --porcelain --untracked-files=all; git diff | shasum)"; \
	go generate ./...; \
	after="$$(git status --porcelain --untracked-files=all; git diff | shasum)"; \
	if [ "$$before" != "$$after" ]; then \
		git status --short; echo "Generated code is out of date. Run: make generate"; exit 1; \
	fi

# The core is the hand-written, non-test Go under server/internal, minus the client
# packages below and test-helper packages (named *test). A new package counts as core
# unless it is added to CLIENT_PKGS. Raising the budget is a recorded decision.
CORE_BUDGET_LINES := 15000
CLIENT_PKGS       := cli delivery deliverytext joinline

## core-size: print the core's size and fail if it is over its budget
core-size:
	@files="$$(find server/internal -name '*.go' ! -name '*_test.go' ! -name '*.gen.go' \
		| grep -v -E '^server/internal/($(subst $(eval) ,|,$(CLIENT_PKGS)))/' \
		| grep -v -E '/[a-z]*test/' || true)"; \
	lines=$$(cat $$files | wc -l | tr -d ' '); \
	tokens=$$(( $$(cat $$files | wc -c) / 4 )); \
	echo "core: $$lines lines of Go, about $$tokens tokens (budget $(CORE_BUDGET_LINES) lines)"; \
	if [ "$$lines" -gt $(CORE_BUDGET_LINES) ]; then \
		echo "The core is over its budget. Move code out of the core, or record a new budget in design/DECISIONS.md."; exit 1; \
	fi

test:
	@$(REQUIRE_GO); go test -race ./...

e2e:
	@$(REQUIRE_GO); \
	if [ -z "$$(go list -tags e2e ./e2e/... 2>/dev/null)" ]; then echo "$@: skipped, no e2e tests yet"; exit 0; fi; \
	go test -race -tags e2e -count=1 ./e2e/...

vuln: $(GOVULNCHECK)
	@$(REQUIRE_GO); $(GOVULNCHECK) ./...

tools: $(GOLANGCI_LINT) $(GOVULNCHECK)

$(GOLANGCI_LINT):
	@mkdir -p $(BIN)/tmp
	GOBIN=$(BIN)/tmp go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	@mv $(BIN)/tmp/golangci-lint $@

$(GOVULNCHECK):
	@mkdir -p $(BIN)/tmp
	GOBIN=$(BIN)/tmp go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
	@mv $(BIN)/tmp/govulncheck $@
