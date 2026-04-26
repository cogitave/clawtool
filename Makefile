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

e2e: build ## Run end-to-end MCP integration tests against the built binary.
	@bash test/e2e/run.sh

install: build ## Copy the binary to $(INSTALL_DIR) atomically.
	@mkdir -p $(INSTALL_DIR)
	@# Atomic replace via rename; survives a binary that's currently
	@# being executed (e.g. by an MCP client that has clawtool serving).
	cp $(BIN) $(INSTALL_DIR)/clawtool.new
	mv $(INSTALL_DIR)/clawtool.new $(INSTALL_DIR)/clawtool
	@echo "✓ installed to $(INSTALL_DIR)/clawtool"

fmt: ## gofmt the codebase.
	$(GO) fmt ./...

vet: ## go vet the codebase.
	$(GO) vet ./...

lint: fmt vet ## fmt + vet (no external linter requirement).

clean: ## Remove build outputs.
	rm -rf $(BIN_DIR) $(DIST_DIR)
	@echo "✓ cleaned"

# Cross-compile binaries for release. Run after tagging.
.PHONY: dist
dist: ## Build release binaries for linux/darwin amd64+arm64.
	@mkdir -p $(DIST_DIR)
	GOOS=linux  GOARCH=amd64 $(GO) build -ldflags="$(LDFLAGS)" -o $(DIST_DIR)/clawtool-linux-amd64  ./cmd/clawtool
	GOOS=linux  GOARCH=arm64 $(GO) build -ldflags="$(LDFLAGS)" -o $(DIST_DIR)/clawtool-linux-arm64  ./cmd/clawtool
	GOOS=darwin GOARCH=amd64 $(GO) build -ldflags="$(LDFLAGS)" -o $(DIST_DIR)/clawtool-darwin-amd64 ./cmd/clawtool
	GOOS=darwin GOARCH=arm64 $(GO) build -ldflags="$(LDFLAGS)" -o $(DIST_DIR)/clawtool-darwin-arm64 ./cmd/clawtool
	@echo "✓ dist binaries in $(DIST_DIR)/"
