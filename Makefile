# clawtool Makefile
#
# All standard project entry points go through here so future work always
# runs the same commands in the same way. CI scripts target `make test`,
# `make e2e`, `make build`.

GO        ?= /usr/local/go/bin/go
BIN_DIR   := bin
DIST_DIR  := dist
INSTALL_DIR ?= $(HOME)/.local/bin
BIN       := $(BIN_DIR)/clawtool

VERSION_PKG := github.com/cogitave/clawtool/internal/version
VERSION     := $(shell git describe --tags --always --dirty 2>/dev/null || echo "0.1.0-dev")
LDFLAGS     := -s -w

.PHONY: all build test e2e install clean fmt vet lint help

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

.PHONY: stub-server
stub-server: ## Build the stub MCP server used as a test fixture.
	$(GO) build -o test/e2e/stub-server/stub-server ./test/e2e/stub-server

.PHONY: portal-integration
portal-integration: ## Drive portal.Ask through real Chrome against an httptest fake portal. Requires Chrome / Chromium on PATH.
	$(GO) test -tags integration -count=1 -v -run TestAsk_RealChrome ./internal/portal/

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
