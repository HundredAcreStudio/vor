.PHONY: build install test test-race lint vet fmt tidy clean run version-info help ui ui-dev all

GO            ?= go
NPM           ?= npm
UI_DIR        ?= ui
BIN_DIR       ?= bin
GOBIN         ?= $(shell $(GO) env GOBIN)
ifeq ($(GOBIN),)
GOBIN         := $(shell $(GO) env GOPATH)/bin
endif
PKG           := github.com/HundredAcreStudio/vor
VERSION       ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT        := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS       := -X $(PKG)/internal/version.Version=$(VERSION) \
                 -X $(PKG)/internal/version.Commit=$(COMMIT) \
                 -X $(PKG)/internal/version.BuildDate=$(BUILD_DATE)

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}; {printf "  %-15s %s\n", $$1, $$2}'

build: ## Build all binaries into ./bin (embeds the last-built UI in ui/dist)
	@mkdir -p $(BIN_DIR)
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/vor ./cmd/vor
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/vor-augment ./cmd/vor-augment

ui: ## Build the dashboard SPA into ui/dist (embedded by `make build`)
	cd $(UI_DIR) && $(NPM) install && $(NPM) run build

ui-dev: ## Run the Vite dev server (proxies /api to the daemon on :7337)
	cd $(UI_DIR) && $(NPM) install && $(NPM) run dev

all: ui build ## Build the UI then the binaries (full release build)

install: build ## Install built binaries to $GOBIN (fresh inode; avoids macOS codesign-cache SIGKILL)
	@mkdir -p $(GOBIN)
	install -m 0755 $(BIN_DIR)/vor $(GOBIN)/vor
	install -m 0755 $(BIN_DIR)/vor-augment $(GOBIN)/vor-augment

test: ## Run tests
	$(GO) test ./...

test-race: ## Run tests with race detector
	$(GO) test -race ./...

lint: ## Run staticcheck (install with: go install honnef.co/go/tools/cmd/staticcheck@latest)
	staticcheck ./...

vet: ## Run go vet
	$(GO) vet ./...

fmt: ## Format code
	$(GO) fmt ./...

tidy: ## Tidy module deps
	$(GO) mod tidy

run: build ## Build and run vor
	./$(BIN_DIR)/vor

version-info: ## Print version metadata that would be embedded
	@echo "Version:    $(VERSION)"
	@echo "Commit:     $(COMMIT)"
	@echo "BuildDate:  $(BUILD_DATE)"

clean: ## Remove build artifacts
	rm -rf $(BIN_DIR) dist coverage.txt
