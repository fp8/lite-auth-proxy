.PHONY: build-flex build-lite build-all run-flex run-lite test test-all coverage lint clean help tidy \
	docker-build-flex docker-build-lite docker-build-all \
	docker-run-flex docker-push-flex docker-push-lite docker-push-all

# Default target
.DEFAULT_GOAL := help

# Variables
GO_VERSION?=$(shell grep 'Version = ' cmd/flex/main.go | head -1 | sed 's/.*"\(.*\)".*/\1/')
VERSION=dev
FLEX_BINARY=flex-auth-proxy
LITE_BINARY=lite-auth-proxy
BUILD_DIR=bin
GO=go
GOFLAGS=-v
LDFLAGS=-ldflags "-s -w -X main.Version=$(VERSION)"
# Docker images are published to Docker Hub under the farport/ namespace —
# the exact same images the GitHub Actions release workflow builds (both call
# scripts/docker-build.sh). To target a private Google Artifact Registry
# instead, use: make -f Makefile.gcp ...
DOCKER_NAMESPACE?=farport
FLEX_IMAGE=$(DOCKER_NAMESPACE)/flex-auth-proxy
LITE_IMAGE=$(DOCKER_NAMESPACE)/lite-auth-proxy
IMAGE_TAG?=$(GO_VERSION)
DOCKER_TARGET_URL?=http://localhost:8080

# Build the flex application (all plugins)
build-flex:
	@echo "Building $(FLEX_BINARY) version $(VERSION)..."
	@mkdir -p $(BUILD_DIR)
	$(GO) build $(GOFLAGS) $(LDFLAGS) -o $(BUILD_DIR)/$(FLEX_BINARY) ./cmd/flex

# Build the lite application (no plugins)
build-lite:
	@echo "Building $(LITE_BINARY) version $(VERSION)..."
	@mkdir -p $(BUILD_DIR)
	$(GO) build $(GOFLAGS) $(LDFLAGS) -o $(BUILD_DIR)/$(LITE_BINARY) ./cmd/lite

# Build both variants
build-all: build-flex build-lite

# Run the flex application
run-flex: build-flex
	@echo "Running $(FLEX_BINARY)..."
	./$(BUILD_DIR)/$(FLEX_BINARY) -config config/config-flex.toml

# Run the lite application
run-lite: build-lite
	@echo "Running $(LITE_BINARY)..."
	./$(BUILD_DIR)/$(LITE_BINARY) -config config/config-lite.toml

# Run unit tests only
# Excludes files with //go:build integration tag
# Runs fast unit tests only: ~150 tests
test:
	@echo "Running unit tests..."
	$(GO) test -v -race ./...

# Run all tests (unit + integration)
# Includes files with //go:build integration tag
# Runs ALL tests: ~190 tests
test-all:
	@echo "Running all tests..."
	@bash -c 'set -a; . .env 2>/dev/null; set +a; $(GO) test -v -race -tags=integration ./...'

# Generate coverage report from all tests
coverage:
	@echo "Running all tests with coverage..."
	@bash -c 'set -a; . .env 2>/dev/null; set +a; $(GO) test -v -race -tags=integration -coverprofile=coverage.out ./...'
	@echo "Generating coverage report..."
	@$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

# Run linter (requires golangci-lint)
lint:
	@echo "Running golangci-lint..."
	@which golangci-lint > /dev/null || (echo "golangci-lint not found. Install it from https://golangci-lint.run/usage/install/" && exit 1)
	golangci-lint run ./...

# Clean build artifacts
clean:
	@echo "Cleaning build artifacts..."
	docker compose down --remove-orphans
	rm -rf $(BUILD_DIR)
	rm -f coverage.out coverage.html

# Tidy go modules
tidy:
	@echo "Tidying go modules..."
	$(GO) mod tidy

# Show help (default target)
help:
	@echo "Available targets:"
	@echo "  help              - Show this help message (default)"
	@echo "  build-flex        - Build flex-auth-proxy binary (all plugins)"
	@echo "  build-lite        - Build lite-auth-proxy binary (no plugins)"
	@echo "  build-all         - Build both flex and lite binaries"
	@echo "  run-flex          - Build and run flex-auth-proxy"
	@echo "  run-lite          - Build and run lite-auth-proxy"
	@echo "  test              - Run unit tests only"
	@echo "  test-all          - Run all tests including integration"
	@echo "  coverage          - Run all tests and generate HTML coverage report"
	@echo "  lint              - Run golangci-lint"
	@echo "  tidy              - Tidy go modules"
	@echo "  clean             - Remove build artifacts and coverage files"
	@echo ""
	@echo "Docker targets (Docker Hub: $(DOCKER_NAMESPACE)/*, version from cmd/flex/main.go: $(GO_VERSION)):"
	@echo "  docker-build-flex - Build flex-auth-proxy image ($(FLEX_IMAGE))"
	@echo "  docker-build-lite - Build lite-auth-proxy image ($(LITE_IMAGE))"
	@echo "  docker-build-all  - Build both flex and lite Docker images"
	@echo "  docker-run-flex   - Build + run flex-auth-proxy + echo via Docker Compose"
	@echo "  docker-push-flex  - Build and push flex-auth-proxy image to Docker Hub"
	@echo "  docker-push-lite  - Build and push lite-auth-proxy image to Docker Hub"
	@echo "  docker-push-all   - Build and push both images to Docker Hub"
	@echo ""
	@echo "To build/push to a private Google Artifact Registry instead:"
	@echo "  make -f Makefile.gcp help"

# Docker targets — delegate to scripts/docker-build.sh so the Makefile and the
# GitHub Actions release workflow build identical images (Docker Hub farport/*).
docker-build-flex:
	IMAGE=$(FLEX_IMAGE) ./scripts/docker-build.sh flex

docker-build-lite:
	IMAGE=$(LITE_IMAGE) ./scripts/docker-build.sh lite

docker-build-all: docker-build-flex docker-build-lite

docker-run-flex: docker-build-flex
	@echo "Running flex-auth-proxy + echo in Docker Compose..."
	GOOGLE_CLOUD_PROJECT=fp8devel \
	IMAGE_NAME=$(FLEX_IMAGE) \
	IMAGE_TAG=$(IMAGE_TAG) \
	PROXY_LOG_MODE=development \
	docker compose up --remove-orphans

docker-push-flex:
	IMAGE=$(FLEX_IMAGE) PUSH=true ./scripts/docker-build.sh flex

docker-push-lite:
	IMAGE=$(LITE_IMAGE) PUSH=true ./scripts/docker-build.sh lite

docker-push-all: docker-push-flex docker-push-lite
