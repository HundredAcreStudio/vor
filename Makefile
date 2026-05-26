.PHONY: build install test test-race lint vet fmt tidy clean run version-info help

GO            ?= go
BIN_DIR       ?= bin
PKG           := github.com/repowise-dev/repowise-go
VERSION       ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT        := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS       := -X $(PKG)/internal/version.Version=$(VERSION) \
                 -X $(PKG)/internal/version.Commit=$(COMMIT) \
                 -X $(PKG)/internal/version.BuildDate=$(BUILD_DATE)

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}; {printf "  %-15s %s\n", $$1, $$2}'

build: ## Build all binaries into ./bin
	@mkdir -p $(BIN_DIR)
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/repowise ./cmd/repowise
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/repowise-augment ./cmd/repowise-augment

install: ## Install binaries to $GOBIN
	$(GO) install -ldflags "$(LDFLAGS)" ./cmd/repowise
	$(GO) install -ldflags "$(LDFLAGS)" ./cmd/repowise-augment

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

run: build ## Build and run repowise
	./$(BIN_DIR)/repowise

version-info: ## Print version metadata that would be embedded
	@echo "Version:    $(VERSION)"
	@echo "Commit:     $(COMMIT)"
	@echo "BuildDate:  $(BUILD_DATE)"

clean: ## Remove build artifacts
	rm -rf $(BIN_DIR) dist coverage.txt
