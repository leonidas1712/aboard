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

# Go steps are skipped, visibly, until the repo has a go.mod.
REQUIRE_GO = if [ ! -f go.mod ]; then echo "$@: skipped, no go.mod yet"; exit 0; fi

.PHONY: check fmt fmt-check lint vet generate generate-check test e2e conformance extension-test live live-affected live-smoke launchers launcher-kit harness-table harness-table-check docs-cli docs-check docs-preview docs-links vuln tools core-size web web-check web-e2e install dev release-snapshot release release-check sandbox sandbox-clean

## check: format check, lint, vet, generated code, core size, harness table, docs reference, tests, e2e, extension tests, vulnerabilities
check: fmt-check lint vet generate-check core-size harness-table-check docs-check test e2e extension-test vuln
	@echo "make check: OK"

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

# The core is the hand-written, non-test Go under server/internal, minus the client
# packages below and test-helper packages (named *test). A new package counts as core
# unless it is added to CLIENT_PKGS. Raising the budget is a recorded decision.
CORE_BUDGET_LINES := 15000
CLIENT_PKGS       := cli delivery deliverytext harness joinline launcher

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

## conformance: the harness conformance kit, no model (HARNESS=<name> for one harness)
# The kit's two halves also run in make test and make e2e, so make check runs them. A
# harness with code that runs inside it (adapters/<harness>/*.test.ts) has its tests run
# too, with Bun.
conformance:
	@$(REQUIRE_GO); \
	HARNESS='$(HARNESS)' go test -race -count=1 -run '^TestHarnessConformance$$' ./server/internal/harness/registry/; \
	status=0; out="$$(HARNESS='$(HARNESS)' go test -race -tags e2e -count=1 -run '^TestHarnessConformance$$' -v ./e2e/ 2>&1)" || status=$$?; \
	echo "$$out" | grep -v -E '^ *(=== |--- PASS)' || true; \
	if [ $$status -ne 0 ]; then exit $$status; fi; \
	$(MAKE) --no-print-directory extension-test HARNESS='$(HARNESS)'

# Extensions are TypeScript that the harness's own Bun runs (omp's), so their tests need
# Bun; there is no build step.
BUN ?= $(shell command -v bun 2>/dev/null || ([ -x "$$HOME/.bun/bin/bun" ] && echo "$$HOME/.bun/bin/bun"))

## extension-test: test the code Aboard installs inside harnesses (adapters/*/*.test.ts) with Bun
extension-test:
	@dirs=""; for f in adapters/*/*.test.ts; do \
		h="$$(basename "$$(dirname "$$f")")"; \
		if [ -z "$(HARNESS)" ] || echo ",$(HARNESS)," | grep -q ",$$h,"; then dirs="$$dirs adapters/$$h"; fi; \
	done; \
	if [ -z "$$dirs" ]; then exit 0; fi; \
	if [ -z "$(BUN)" ]; then \
		echo "extension-test: Bun is needed to test the extensions in$$dirs, and isn't installed."; \
		echo "Install it with: curl -fsSL https://bun.sh/install | bash   (or brew install oven-sh/bun/bun)"; \
		exit 1; \
	fi; \
	$(BUN) test $$dirs

## live: drive real harnesses in tmux (spends model turns; HARNESS=<name> for one harness, RUN=TestName for one test)
# Not part of make check. Needs tmux and logged-in harnesses; a missing one is skipped.
# Run it before a release and after any change to delivery, setup or upgrades. Each
# scenario's result is saved in e2e/live/support.json; make harness-table then updates
# the README's table.
#
# Every test runs in a lab of its own, so they all run in parallel, at most LIVE_PARALLEL
# at once. More at once finishes sooner but runs more harnesses side by side: the
# models' rate limits and the machine's memory and CPU set the ceiling (see
# engineering/testing.md). The run ends with a table of how long each test took.
LIVE_PARALLEL ?= 12
live:
	@$(REQUIRE_GO); HARNESS='$(HARNESS)' go test -tags live -count=1 -v -timeout 60m -parallel $(LIVE_PARALLEL) $(if $(RUN),-run '$(RUN)') ./e2e/live

## live-affected: make live for only the harnesses the changes against BASE (default origin/main) touch
# The mapping from changed files to harnesses is the table in e2e/live/affected/main.go.
# go run ./e2e/live/affected prints the decision without running anything.
BASE ?= origin/main
live-affected:
	@$(REQUIRE_GO); plan="$$(go run ./e2e/live/affected -base '$(BASE)')"; \
	case "$$plan" in \
		none) ;; \
		all) $(MAKE) --no-print-directory live ;; \
		*) $(MAKE) --no-print-directory live HARNESS="$$plan" ;; \
	esac

## live-smoke: start each harness once with the model make live runs it with, and check it answers (one model turn per harness)
live-smoke:
	@$(REQUIRE_GO); HARNESS='$(HARNESS)' go test -tags live -count=1 -v -timeout 10m -parallel $(LIVE_PARALLEL) -run 'TestModelSmoke' ./e2e/live

## launchers: build the launchers that live outside aboard into .bin: aboard-launcher-herdr
launchers:
	@$(REQUIRE_GO); go build -o $(BIN)/aboard-launcher-herdr ./launchers/herdr; \
	echo "Built $(BIN)/aboard-launcher-herdr (make install also installs it next to aboard)"

## launcher-kit: run the launcher kit against one launcher (LAUNCHER=tmux, headless, or <name> for aboard-launcher-<name> on the PATH)
# make test already runs it against tmux, headless and the herdr launcher with a stand-in
# for herdr. Against the real herdr it starts a herdr session of its own, in your herdr
# config folder, and removes it at the end.
launcher-kit:
	@$(REQUIRE_GO); LAUNCHER='$(LAUNCHER)' go test -tags launcherkit -count=1 -v -run '^TestLauncherKit$$' ./server/internal/launcher/launchertest/

## harness-table: write the README's harness table from the profiles and the live kit's results
harness-table:
	@$(REQUIRE_GO); go run ./scripts/harnesstable

harness-table-check:
	@$(REQUIRE_GO); go run ./scripts/harnesstable -check

# The docs site (docs/, published with Mintlify; see docs/README-site.md). Its CLI
# reference is written from aboard help --json, and its API reference reads a copy of
# spec/openapi.yaml, since Mintlify reads only files inside docs/. Both need only Go.
DOCS_HELP = go run ./server/cmd/aboard help --json

## docs-cli: write the docs' CLI reference and API spec copy from aboard help and spec/openapi.yaml
docs-cli:
	@$(REQUIRE_GO); $(DOCS_HELP) | go run ./scripts/docscli || exit 1; 	cp spec/openapi.yaml docs/api-reference/openapi.yaml

## docs-check: fail if the docs' CLI reference or API spec copy is out of date
docs-check:
	@$(REQUIRE_GO); $(DOCS_HELP) | go run ./scripts/docscli -check || exit 1; 	if ! cmp -s spec/openapi.yaml docs/api-reference/openapi.yaml; then 		echo "docs/api-reference/openapi.yaml differs from spec/openapi.yaml: run make docs-cli"; exit 1; 	fi

# The Mintlify CLI needs Node 20.17 or later and the network on first use. It runs from
# docs/node_modules (npm ci there), never a global install. Not part of make check.
DOCS_MINT = cd docs && npm ci --no-audit --no-fund --silent && DO_NOT_TRACK=1 npx --no-install mint

## docs-preview: serve the docs site at http://localhost:3000 with the Mintlify CLI
docs-preview:
	$(DOCS_MINT) dev

## docs-links: check the docs site for broken links and build it strictly with the Mintlify CLI
docs-links:
	$(DOCS_MINT) broken-links
	cd docs && DO_NOT_TRACK=1 npx --no-install mint validate

# The web UI needs Node 20 or later; make check doesn't, and a binary built without the
# ui tag serves a page saying how to get the UI.
WEB_ENV := NEXT_TELEMETRY_DISABLED=1

## web: build the web UI into web/out
web:
	cd web && npm ci && $(WEB_ENV) npm run build

## install: build the web UI, then install aboard with the UI embedded
install: web
	go install -tags ui ./server/cmd/aboard
	@gobin="$$(go env GOBIN)"; [ -n "$$gobin" ] || gobin="$$(go env GOPATH)/bin"; \
	for d in launchers/*/; do n=$$(basename $$d); go build -o "$$gobin/aboard-launcher-$$n" ./$$d && echo "Installed $$gobin/aboard-launcher-$$n"; done

# A dev build's version is the source's version with build metadata naming the commit,
# such as 0.1.0+dev.1d0e798ab12c (".dirty" with uncommitted changes). Build metadata
# never orders versions, so a dev build compares with an installed build of the same
# version by commit time, like any other build.
SOURCE_VERSION = $(shell sed -n 's/^var version = "\(.*\)"$$/\1/p' server/internal/cli/build.go)
DEV_VERSION    = $(SOURCE_VERSION)+dev.$(shell git rev-parse --short=12 HEAD)$(if $(shell git status --porcelain),.dirty)

## dev: build this checkout into ./.bin/aboard as a dev build, with the UI if web/out exists
dev:
	@tags=""; if [ -d web/out ]; then tags="-tags ui"; fi; \
	go build $$tags -ldflags "-X github.com/leonidas1712/aboard/server/internal/cli.version=$(DEV_VERSION)" \
		-o $(BIN)/aboard ./server/cmd/aboard; \
	echo "Built $(BIN)/aboard $(DEV_VERSION)$${tags:+ with the web UI}"

## sandbox: open a shell to test this checkout by hand, isolated from your own setup (NAME=<name>)
sandbox: dev
	@scripts/sandbox open "$(NAME)"

## sandbox-clean: stop a sandbox's server and daemon and remove it (NAME=<name>)
sandbox-clean:
	@scripts/sandbox clean "$(NAME)"

## web-check: build and typecheck the web UI, then run its browser smoke test
web-check: web
	cd web && npm run typecheck
	@$(MAKE) --no-print-directory web-e2e

# Builds aboard with the UI (after make web) and drives a board in Chromium.
web-e2e:
	cd web && $(WEB_ENV) npx playwright test

# The release build (.goreleaser.yaml). The release job runs it on a version tag; locally it
# builds every platform's archive into dist/ and publishes nothing. It builds the web UI
# first, so it needs Node, and syft for the SBOMs. By default it skips signing and the
# image (RELEASE_SKIP), which need the release job's identity and Docker.
RELEASE_SKIP ?= sign,docker
## release-snapshot: build every release archive into dist/ without publishing (needs Node and syft)
release-snapshot: $(GORELEASER)
	ABOARD_SNAPSHOT_VERSION='$(DEV_VERSION)' $(GORELEASER) release --snapshot --clean --skip=$(RELEASE_SKIP)

# Only the release job runs this: it publishes, and signs with the job's identity.
release: $(GORELEASER)
	@if [ -z "$$GITHUB_ACTIONS" ]; then echo "make release runs only in the release job; use make release-snapshot"; exit 1; fi
	$(GORELEASER) release --clean --release-notes "$(RELEASE_NOTES)"

## release-check: validate .goreleaser.yaml
release-check: $(GORELEASER)
	$(GORELEASER) check

vuln: $(GOVULNCHECK)
	@$(REQUIRE_GO); $(GOVULNCHECK) ./...

tools: $(GOLANGCI_LINT) $(GOVULNCHECK)

$(GOLANGCI_LINT):
	@mkdir -p $(BIN)/tmp
	GOBIN=$(BIN)/tmp go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	@mv $(BIN)/tmp/golangci-lint $@

$(GORELEASER):
	@mkdir -p $(BIN)/tmp
	# GoReleaser needs a newer Go than the one we build with; auto fetches it (checked
	# against the Go checksum database) for this install only.
	GOTOOLCHAIN=auto GOBIN=$(BIN)/tmp go install github.com/goreleaser/goreleaser/v2@$(GORELEASER_VERSION)
	@mv $(BIN)/tmp/goreleaser $@

$(GOVULNCHECK):
	@mkdir -p $(BIN)/tmp
	GOBIN=$(BIN)/tmp go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
	@mv $(BIN)/tmp/govulncheck $@
