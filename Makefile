# clawtool Makefile
#
# All standard project entry points go through here so future work always
# runs the same commands in the same way. CI scripts target `make test`,
# `make e2e`, `make build`.

# GO defaults to whichever `go` is on PATH (covers the GitHub
# Actions setup-go path under /opt/hostedtoolcache/go, brew
# installs, asdf shims, etc.) and falls back to the legacy
# /usr/local/go/bin/go for hosts where Go is direct-installed
# without PATH wiring. scripts/ci.sh has the same fallback chain
# at runtime — the Makefile mirrors it so `make build` works in
# the same environments. Override either with `make GO=/path/to/go`.
GO        ?= $(shell command -v go 2>/dev/null || echo /usr/local/go/bin/go)
BIN_DIR   := bin
DIST_DIR  := dist
INSTALL_DIR ?= $(HOME)/.local/bin
BIN       := $(BIN_DIR)/clawtool

VERSION_PKG := github.com/cogitave/clawtool/internal/version
VERSION     := $(shell git describe --tags --always --dirty 2>/dev/null || echo "0.1.0-dev")
LDFLAGS     := -s -w

.PHONY: all build test e2e install clean fmt vet lint help sync-versions sync-versions-check

all: build

help: ## List targets.
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-12s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## Build the clawtool binary into ./bin.
	@mkdir -p $(BIN_DIR)
	$(GO) build -ldflags="$(LDFLAGS)" -o $(BIN) ./cmd/clawtool
	@echo "✓ built $(BIN) ($(VERSION))"

test: ## Run unit tests (race detector enabled).
	$(GO) test -race -count=1 -timeout=60s ./...

e2e: build stub-server ## Run end-to-end MCP integration tests against the built binary.
	@bash test/e2e/run.sh

.PHONY: integration
integration: build ## Multi-instance soak against real upstream MCP servers (npx required).
	@command -v npx >/dev/null 2>&1 || { echo "npx required (install Node.js 18+)"; exit 1; }
	@bash test/e2e/integration.sh

.PHONY: e2e-onboard
e2e-onboard: ## Run the onboard --yes container e2e (Docker required).
	CLAWTOOL_E2E_DOCKER=1 $(GO) test -count=1 -timeout=300s ./test/e2e/onboard/...

.PHONY: e2e-upgrade
e2e-upgrade: ## Run the binary-swap + daemon-restart container e2e (Docker required).
	CLAWTOOL_E2E_DOCKER=1 $(GO) test -count=1 -timeout=300s ./test/e2e/upgrade/...

.PHONY: e2e-realinstall
e2e-realinstall: ## Run the Alpine + install.sh + GitHub-release e2e (Docker + network required).
	CLAWTOOL_E2E_DOCKER=1 $(GO) test -count=1 -timeout=300s ./test/e2e/realinstall/...

.PHONY: ci ci-fast ci-full
ci: ## Run every CI gate (fmt, vet, build, test, deadcode, stub-e2e). Set CLAWTOOL_E2E_DOCKER=1 for container gates.
	@bash scripts/ci.sh

ci-fast: ## Run quick CI (fmt, vet, build, test, deadcode only — skip e2e + docker).
	@CLAWTOOL_CI_FAST=1 bash scripts/ci.sh

ci-full: ## Run every CI gate including container e2e + docker smoke.
	@CLAWTOOL_E2E_DOCKER=1 bash scripts/ci.sh

.PHONY: stub-server
stub-server: ## Build the stub MCP server used as a test fixture.
	$(GO) build -o test/e2e/stub-server/stub-server ./test/e2e/stub-server

.PHONY: portal-integration
portal-integration: ## Drive portal.Ask through real Chrome against an httptest fake portal. Requires Chrome / Chromium on PATH.
	$(GO) test -tags integration -count=1 -v -run TestAsk_RealChrome ./internal/portal/

.PHONY: docker docker-smoke docker-build-clawtool docker-build-worker docker-build-relay docker-build-all
DOCKER_TAG ?= cogitave/clawtool:dev
DOCKER_WORKER_TAG ?= cogitave/clawtool-worker:dev
DOCKER_RELAY_TAG ?= cogitave/clawtool-relay:dev
DOCKER_UNIFIED ?= Dockerfile.unified

docker: docker-build-clawtool ## Build the cogitave/clawtool Docker image (alias for docker-build-clawtool).

docker-build-clawtool: ## Build the production clawtool image from Dockerfile.unified --target=clawtool (distroless static).
	docker build -f $(DOCKER_UNIFIED) --target clawtool \
		--build-arg VERSION=$(VERSION) \
		--build-arg BUILD_DATE=$(shell date -u +%Y-%m-%dT%H:%M:%SZ) \
		-t $(DOCKER_TAG) .
	@echo "✓ built $(DOCKER_TAG)"

docker-build-worker: ## Build the sandbox-worker image from Dockerfile.unified --target=worker (ubuntu:24.04 + bash/git/python/node).
	docker build -f $(DOCKER_UNIFIED) --target worker \
		--build-arg VERSION=$(VERSION) \
		--build-arg BUILD_DATE=$(shell date -u +%Y-%m-%dT%H:%M:%SZ) \
		-t $(DOCKER_WORKER_TAG) .
	@echo "✓ built $(DOCKER_WORKER_TAG)"

docker-build-relay: ## Build the relay HTTP-gateway image from Dockerfile.unified --target=relay (debian-slim + claude/codex/opencode/gemini CLIs).
	docker build -f $(DOCKER_UNIFIED) --target relay \
		--build-arg VERSION=$(VERSION) \
		--build-arg BUILD_DATE=$(shell date -u +%Y-%m-%dT%H:%M:%SZ) \
		-t $(DOCKER_RELAY_TAG) .
	@echo "✓ built $(DOCKER_RELAY_TAG)"

docker-build-all: docker-build-clawtool docker-build-worker docker-build-relay ## Build clawtool + worker + relay images sequentially (shares the unified builder layer cache).
	@echo "✓ built all three unified-Dockerfile images"

docker-smoke: docker ## Verify the built image responds to MCP `initialize` over stdio.
	@echo "Running MCP initialize handshake against $(DOCKER_TAG)..."
	@printf '%s\n' \
		'{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"docker-smoke","version":"0"}}}' \
		| docker run -i --rm $(DOCKER_TAG) | head -1 \
		| grep -q '"serverInfo"' \
		&& echo "✓ image speaks MCP" \
		|| (echo "✗ image did not return serverInfo on initialize"; exit 1)

install: build ## Copy the binary to $(INSTALL_DIR) atomically + run postinstall cleanup.
	@mkdir -p $(INSTALL_DIR)
	@# Atomic replace via rename; survives a binary that's currently
	@# being executed (e.g. by an MCP client that has clawtool serving).
	cp $(BIN) $(INSTALL_DIR)/clawtool.new
	mv $(INSTALL_DIR)/clawtool.new $(INSTALL_DIR)/clawtool
	@echo "✓ installed to $(INSTALL_DIR)/clawtool"
	@# Postinstall cleanup: drop any leftover manual `claude mcp`
	@# registration (so we don't end up with mcp__clawtool__* AND
	@# mcp__plugin_clawtool_clawtool__* doubled in the model's view),
	@# and hint at the plugin install if the user hasn't done it yet.
	@bash scripts/postinstall.sh

fmt: ## gofmt the codebase.
	$(GO) fmt ./...

vet: ## go vet the codebase.
	$(GO) vet ./...

lint: fmt vet ## fmt + vet (no external linter requirement).

clean: ## Remove build outputs.
	rm -rf $(BIN_DIR) $(DIST_DIR)
	@echo "✓ cleaned"

# Cross-compile binaries for release. Run after tagging.
.PHONY: changelog
changelog: ## Regenerate CHANGELOG.md from git history (git-cliff + cliff.toml).
	@command -v git-cliff >/dev/null 2>&1 || { echo "git-cliff not found; install via 'curl … releases/download/…/git-cliff-…musl.tar.gz' or 'brew install git-cliff'"; exit 1; }
	git-cliff --output CHANGELOG.md
	@echo "✓ CHANGELOG.md regenerated"

.PHONY: dist
dist: ## Build release binaries for linux/darwin amd64+arm64.
	@mkdir -p $(DIST_DIR)
	GOOS=linux  GOARCH=amd64 $(GO) build -ldflags="$(LDFLAGS)" -o $(DIST_DIR)/clawtool-linux-amd64  ./cmd/clawtool
	GOOS=linux  GOARCH=arm64 $(GO) build -ldflags="$(LDFLAGS)" -o $(DIST_DIR)/clawtool-linux-arm64  ./cmd/clawtool
	GOOS=darwin GOARCH=amd64 $(GO) build -ldflags="$(LDFLAGS)" -o $(DIST_DIR)/clawtool-darwin-amd64 ./cmd/clawtool
	GOOS=darwin GOARCH=arm64 $(GO) build -ldflags="$(LDFLAGS)" -o $(DIST_DIR)/clawtool-darwin-arm64 ./cmd/clawtool
	@echo "✓ dist binaries in $(DIST_DIR)/"

.PHONY: release-snapshot
release-snapshot: ## Run GoReleaser in snapshot mode (no publish, useful for local CI parity).
	@command -v goreleaser >/dev/null 2>&1 || { echo "goreleaser not found; install via 'go install github.com/goreleaser/goreleaser/v2@latest'"; exit 1; }
	goreleaser release --clean --snapshot
	@echo "✓ snapshot artifacts in dist/"

# ─── version sync ────────────────────────────────────────────────
# Single source of truth: internal/version.Version is canonical.
# `make sync-versions` regenerates .claude-plugin/plugin.json +
# .claude-plugin/marketplace.json from the const so the three are
# always in lockstep. release-please bumps the const at tag time;
# this target picks up the bump and rewrites the manifests (the
# rewrite is byte-identical except for the version line, so review
# stays trivial).
#
# `make sync-versions-check` is the CI-side gate: same logic but
# refuses to mutate the tree, exiting 1 if a write would change
# anything. Pair with `git diff --exit-code` for belt-and-braces.
sync-versions: ## Regenerate plugin.json + marketplace.json from internal/version.Version.
	$(GO) run ./cmd/version-sync

sync-versions-check: ## Verify plugin.json + marketplace.json match internal/version.Version (CI gate).
	$(GO) run ./cmd/version-sync --check
