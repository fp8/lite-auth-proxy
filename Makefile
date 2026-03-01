.PHONY: build run test test-all coverage lint clean help tidy \
	docker-build docker-run docker-push \
	cloud-build

# Default target
.DEFAULT_GOAL := help

# Variables
GO_VERSION?=$(shell grep 'Version = ' cmd/proxy/main.go | head -1 | sed 's/.*"\(.*\)".*/\1/')
VERSION=dev
MAJOR_MINOR=$(shell echo $(VERSION) | cut -d. -f1,2)
BINARY_NAME=lite-auth-proxy
BUILD_DIR=bin
CONFIG_PATH=configs/config.toml
GO=go
GOFLAGS=-v
LDFLAGS=-ldflags "-s -w -X main.Version=$(VERSION)"
DOCKER_REGISTRY?=europe-docker.pkg.dev
DOCKER_PROJECT_ID?=$(shell echo $$GOOGLE_CLOUD_PROJECT)
DOCKER_REPO_NAME?=docker
DOCKER_REPO=$(DOCKER_REGISTRY)/$(DOCKER_PROJECT_ID)/$(DOCKER_REPO_NAME)
IMAGE_NAME=$(DOCKER_REPO)/lite-auth-proxy
IMAGE_TAG=$(VERSION)
GOOGLE_CLOUD_PROJECT?=$(shell echo $$GOOGLE_CLOUD_PROJECT)
DOCKER_TARGET_URL?=http://localhost:8080

# Build the application
build:
	@echo "Building $(BINARY_NAME) version $(VERSION)..."
	@mkdir -p $(BUILD_DIR)
	$(GO) build $(GOFLAGS) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/proxy

# Run the application
run: build
	@echo "Running $(BINARY_NAME)..."
	./$(BUILD_DIR)/$(BINARY_NAME) -config $(CONFIG_PATH)

# Run unit tests only
# Excludes files with //go:build integration tag
# Runs fast unit tests only: ~92 tests
test:
	@echo "Running unit tests..."
	$(GO) test -v -race ./...

# Run all tests (unit + integration)
# Includes files with //go:build integration tag
# Runs ALL tests: ~105 tests
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
	@echo "  build             - Build the application binary (version from cmd/proxy/main.go)"
	@echo "  run               - Build and run the application"
	@echo "  test              - Run unit tests only (~92 tests)"
	@echo "  test-all          - Run all tests including integration (~105 tests)"
	@echo "  coverage          - Run all tests and generate HTML coverage report"
	@echo "  lint              - Run golangci-lint"
	@echo "  tidy              - Tidy go modules"
	@echo "  clean             - Remove build artifacts and coverage files"
	@echo ""
	@echo "Docker targets (version: $(VERSION), tags: $(VERSION) and $(MAJOR_MINOR)):"
	@echo "  docker-build      - Build Docker images (requires GOOGLE_CLOUD_PROJECT env var)"
	@echo "  docker-run        - Run proxy + echo via Docker Compose"
	@echo "  docker-push       - Push Docker images to GCP Artifact Registry (requires GOOGLE_CLOUD_PROJECT)"
	@echo ""
	@echo "Cloud Build targets:"
	@echo "  cloud-build       - Submit build to Google Cloud Build (requires GOOGLE_CLOUD_PROJECT and gcloud CLI)"
	@echo ""
	@echo "Note: Set GOOGLE_CLOUD_PROJECT environment variable or copy .env.example to .env and source it"

# Docker targets
docker-build:
	@if [ -z "$(DOCKER_PROJECT_ID)" ]; then \
		echo "Error: DOCKER_PROJECT_ID or GOOGLE_CLOUD_PROJECT environment variable must be set"; \
		exit 1; \
	fi
	@echo "Building Docker image: $(IMAGE_NAME):$(VERSION)"
	docker build \
		--build-arg VERSION=$(GO_VERSION) \
		-t $(IMAGE_NAME):$(VERSION) \
		-f Dockerfile .
	@echo "Docker images built successfully:"
	@echo "  - $(IMAGE_NAME):$(VERSION)"

docker-run:
	@if [ -z "$(GOOGLE_CLOUD_PROJECT)" ]; then \
		echo "Error: GOOGLE_CLOUD_PROJECT environment variable must be set"; \
		exit 1; \
	fi
	@echo "Running proxy + echo in Docker Compose..."
	GOOGLE_CLOUD_PROJECT=fp8devel \
	IMAGE_NAME=$(IMAGE_NAME) \
	IMAGE_TAG=$(IMAGE_TAG) \
	PROXY_LOG_MODE=development \
	docker compose up --remove-orphans

docker-push:
	@if [ -z "$(DOCKER_PROJECT_ID)" ]; then \
		echo "Error: GOOGLE_CLOUD_PROJECT environment variable must be set"; \
		exit 1; \
	fi
	@echo "Pushing Docker images to GCP Artifact Registry..."
	docker push $(IMAGE_NAME):$(VERSION)
	docker push $(IMAGE_NAME):$(MAJOR_MINOR)
	@echo "Docker images pushed:"
	@echo "  - $(IMAGE_NAME):$(VERSION)"
	@echo "  - $(IMAGE_NAME):$(MAJOR_MINOR)"

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