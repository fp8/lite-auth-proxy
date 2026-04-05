.PHONY: build-flex build-lite build-all run-flex run-lite test test-all coverage lint clean help tidy \
	docker-build-flex docker-build-lite docker-build-all \
	docker-run-flex docker-push-flex docker-push-lite docker-push-all \
	cloud-build

# Default target
.DEFAULT_GOAL := help

# Variables
GO_VERSION?=$(shell grep 'Version = ' cmd/flex/main.go | head -1 | sed 's/.*"\(.*\)".*/\1/')
VERSION=dev
MAJOR_MINOR=$(shell echo $(VERSION) | cut -d. -f1,2)
FLEX_BINARY=flex-auth-proxy
LITE_BINARY=lite-auth-proxy
BUILD_DIR=bin
GO=go
GOFLAGS=-v
LDFLAGS=-ldflags "-s -w -X main.Version=$(VERSION)"
DOCKER_REGISTRY?=europe-docker.pkg.dev
DOCKER_PROJECT_ID?=$(shell echo $$GOOGLE_CLOUD_PROJECT)
DOCKER_REPO_NAME?=docker
DOCKER_REPO=$(DOCKER_REGISTRY)/$(DOCKER_PROJECT_ID)/$(DOCKER_REPO_NAME)
FLEX_IMAGE=$(DOCKER_REPO)/flex-auth-proxy
LITE_IMAGE=$(DOCKER_REPO)/lite-auth-proxy
IMAGE_TAG=$(VERSION)
GOOGLE_CLOUD_PROJECT?=$(shell echo $$GOOGLE_CLOUD_PROJECT)
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
	@echo "Docker targets (version: $(VERSION), tags: $(VERSION) and $(MAJOR_MINOR)):"
	@echo "  docker-build-flex - Build flex-auth-proxy Docker image (requires GOOGLE_CLOUD_PROJECT)"
	@echo "  docker-build-lite - Build lite-auth-proxy Docker image"
	@echo "  docker-build-all  - Build both flex and lite Docker images"
	@echo "  docker-run-flex   - Run flex-auth-proxy + echo via Docker Compose"
	@echo "  docker-push-flex  - Push flex-auth-proxy image to GCP Artifact Registry"
	@echo "  docker-push-lite  - Push lite-auth-proxy image to GCP Artifact Registry"
	@echo "  docker-push-all   - Push both images to GCP Artifact Registry"
	@echo ""
	@echo "Cloud Build targets:"
	@echo "  cloud-build       - Submit build to Google Cloud Build (requires GOOGLE_CLOUD_PROJECT and gcloud CLI)"
	@echo ""
	@echo "Note: Set GOOGLE_CLOUD_PROJECT environment variable or copy .env.example to .env and source it"

# Docker targets
docker-build-flex:
	@if [ -z "$(DOCKER_PROJECT_ID)" ]; then \
		echo "Error: DOCKER_PROJECT_ID or GOOGLE_CLOUD_PROJECT environment variable must be set"; \
		exit 1; \
	fi
	@echo "Building Docker image: $(FLEX_IMAGE):$(VERSION)"
	docker build \
		--build-arg VERSION=$(GO_VERSION) \
		-t $(FLEX_IMAGE):$(VERSION) \
		-f Dockerfile.flex .
	@echo "Docker image built: $(FLEX_IMAGE):$(VERSION)"

docker-build-lite:
	@if [ -z "$(DOCKER_PROJECT_ID)" ]; then \
		echo "Error: DOCKER_PROJECT_ID or GOOGLE_CLOUD_PROJECT environment variable must be set"; \
		exit 1; \
	fi
	@echo "Building Docker image (lite): $(LITE_IMAGE):$(VERSION)"
	docker build \
		--build-arg VERSION=$(GO_VERSION) \
		-t $(LITE_IMAGE):$(VERSION) \
		-f Dockerfile.lite .
	@echo "Docker image built: $(LITE_IMAGE):$(VERSION)"

docker-build-all: docker-build-flex docker-build-lite

docker-run-flex:
	@if [ -z "$(GOOGLE_CLOUD_PROJECT)" ]; then \
		echo "Error: GOOGLE_CLOUD_PROJECT environment variable must be set"; \
		exit 1; \
	fi
	@echo "Running flex-auth-proxy + echo in Docker Compose..."
	GOOGLE_CLOUD_PROJECT=fp8devel \
	IMAGE_NAME=$(FLEX_IMAGE) \
	IMAGE_TAG=$(IMAGE_TAG) \
	PROXY_LOG_MODE=development \
	docker compose up --remove-orphans

docker-push-flex:
	@if [ -z "$(DOCKER_PROJECT_ID)" ]; then \
		echo "Error: GOOGLE_CLOUD_PROJECT environment variable must be set"; \
		exit 1; \
	fi
	@echo "Pushing flex-auth-proxy images to GCP Artifact Registry..."
	docker push $(FLEX_IMAGE):$(VERSION)
	docker push $(FLEX_IMAGE):$(MAJOR_MINOR)
	@echo "Docker images pushed:"
	@echo "  - $(FLEX_IMAGE):$(VERSION)"
	@echo "  - $(FLEX_IMAGE):$(MAJOR_MINOR)"

docker-push-lite:
	@if [ -z "$(DOCKER_PROJECT_ID)" ]; then \
		echo "Error: GOOGLE_CLOUD_PROJECT environment variable must be set"; \
		exit 1; \
	fi
	@echo "Pushing lite-auth-proxy images to GCP Artifact Registry..."
	docker push $(LITE_IMAGE):$(VERSION)
	docker push $(LITE_IMAGE):$(MAJOR_MINOR)
	@echo "Docker images pushed:"
	@echo "  - $(LITE_IMAGE):$(VERSION)"
	@echo "  - $(LITE_IMAGE):$(MAJOR_MINOR)"

docker-push-all: docker-push-flex docker-push-lite

# Cloud Build target
cloud-build:
	@if [ -z "$(GOOGLE_CLOUD_PROJECT)" ]; then \
		echo "Error: GOOGLE_CLOUD_PROJECT environment variable must be set"; \
		exit 1; \
	fi
	@echo "Submitting build to Google Cloud Build..."
	@if ! command -v gcloud &> /dev/null; then \
		echo "Error: gcloud CLI not found. Install it from https://cloud.google.com/sdk/docs/install"; \
		exit 1; \
	fi
	gcloud builds submit --config cloudbuild.yaml \
		--project=$(GOOGLE_CLOUD_PROJECT)
	@echo "Build submitted to Cloud Build. View progress in GCP Console."
