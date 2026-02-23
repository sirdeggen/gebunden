# Gebunden Core — Headless Wallet Daemon
# Usage: make [target]

# --- Variables ---
APP_NAME   := gebunden
OUTPUT_DIR := ../bin

VERSION      ?= $(shell git describe --tags --always 2>/dev/null || echo "dev")
CLEAN_VERSION = $(shell echo $(VERSION) | sed 's/^v//')

BUILD_TAGS := headless
LDFLAGS    := -X main.version=$(CLEAN_VERSION)

# --- Targets ---

.PHONY: all build run test clean help

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'

all: build ## Build the daemon (default)

build: ## Build the headless daemon binary
	@echo "=== Building $(APP_NAME) $(CLEAN_VERSION) ==="
	@mkdir -p $(OUTPUT_DIR)
	go build -mod=vendor -tags $(BUILD_TAGS) -ldflags '$(LDFLAGS)' -o $(OUTPUT_DIR)/$(APP_NAME) .
	@echo "Binary: $(OUTPUT_DIR)/$(APP_NAME)"

run: build ## Build and run the daemon (bridge must already be running)
	@echo "=== Starting $(APP_NAME) ==="
	$(OUTPUT_DIR)/$(APP_NAME)

test: ## Run Go tests
	@echo "=== Running tests ==="
	go test -mod=vendor -tags $(BUILD_TAGS) -v -count=1 ./...

clean: ## Remove build artifacts
	@echo "=== Cleaning ==="
	@rm -f $(OUTPUT_DIR)/$(APP_NAME)
	@echo "Clean complete"
