# Gebunden — Monorepo Build System
# Usage: make [target]
#
# Targets:
#   make          — build all three components
#   make build    — build all three components
#   make core     — build the headless wallet daemon
#   make bridge   — build the permission bridge
#   make pay      — install and build the pay CLI
#   make run      — start bridge + core in the background
#   make stop     — stop background bridge + core processes
#   make test     — run tests for all components
#   make clean    — remove build artifacts
#   make help     — show this help

# --- Variables ---
BIN_DIR := bin

VERSION      ?= $(shell git describe --tags --always 2>/dev/null || echo "dev")
CLEAN_VERSION = $(shell echo $(VERSION) | sed 's/^v//')

CORE_FLAGS := -mod=vendor -ldflags '-X main.version=$(CLEAN_VERSION)'

# --- Targets ---

.PHONY: all build core bridge pay run stop test clean help

all: build

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'

build: core bridge pay ## Build all three components

core: ## Build the headless wallet daemon → bin/gebunden
	@echo "=== core: building gebunden $(CLEAN_VERSION) ==="
	@mkdir -p $(BIN_DIR)
	cd core && go build $(CORE_FLAGS) -o ../$(BIN_DIR)/gebunden .
	@echo "    → $(BIN_DIR)/gebunden"

bridge: ## Build the permission bridge → bin/gebunden-bridge
	@echo "=== bridge: building ==="
	@mkdir -p $(BIN_DIR)
	cd bridge && go build -o ../$(BIN_DIR)/gebunden-bridge .
	@echo "    → $(BIN_DIR)/gebunden-bridge"

pay: ## Install deps and build the pay CLI
	@echo "=== pay: installing dependencies ==="
	cd pay && npm install --silent
	@echo "=== pay: building ==="
	cd pay && npm run build
	@echo "    → pay/dist/index.js"

run: build ## Build then start bridge + core in the background
	@echo "=== Starting bridge ==="
	@$(BIN_DIR)/gebunden-bridge &
	@echo "=== Starting gebunden ==="
	@$(BIN_DIR)/gebunden &
	@echo "Both processes started. Use 'make stop' to shut them down."

stop: ## Stop background bridge + core processes
	@echo "=== Stopping gebunden ==="
	@pkill -f $(BIN_DIR)/gebunden 2>/dev/null || true
	@echo "=== Stopping bridge ==="
	@pkill -f $(BIN_DIR)/gebunden-bridge 2>/dev/null || true
	@echo "Done."

test: ## Run tests for all components
	@echo "=== core: running tests ==="
	cd core && go test -mod=vendor -v -count=1 ./...
	@echo ""
	@echo "=== bridge: running tests ==="
	cd bridge && go test -v -count=1 ./...
	@echo ""
	@echo "=== pay: type check ==="
	cd pay && npx tsc --noEmit
	@echo "All checks passed."

clean: ## Remove build artifacts
	@echo "=== Cleaning ==="
	@rm -f $(BIN_DIR)/gebunden $(BIN_DIR)/gebunden-bridge
	@rm -rf pay/dist
	@echo "Clean complete."
