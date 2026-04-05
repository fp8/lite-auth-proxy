# Development Guide

This guide covers development requirements, setup, testing, and contributing to lite-auth-proxy.

## Development Requirements

### Required Tools

| Tool | Minimum Version | Purpose |
|------|----------------|---------|
| Go | 1.23 or later | Primary programming language (uses go 1.24 in go.mod) |
| Make | Any recent version | Build automation |
| Git | 2.x or later | Version control |

### Optional Tools

| Tool | Purpose |
|------|---------|
| [golangci-lint](https://golangci-lint.run/) | Code linting and static analysis |
| Docker | Container building and testing |
| gcloud CLI | Google Cloud deployment |

### System Requirements

- **OS**: macOS, Linux, or Windows (with WSL2)
- **RAM**: 512 MB minimum (< 32 MB runtime footprint)
- **Disk**: 100 MB for source code and dependencies

## Initial Setup

### 1. Clone the Repository

```bash
git clone https://github.com/YOUR_ORG/lite-auth-proxy.git
cd lite-auth-proxy
```

### 2. Install Dependencies

```bash
# Download Go module dependencies
go mod download

# Or use make
make tidy
```

### 3. Configure Environment

Copy the example environment file and configure it:

```bash
cp .env.example .env

# Edit .env with your values
nano .env

# Load environment variables
source .env
```

**Note on environment variables:**

The `.env` file is used for **testing purposes only**. For development/runtime, use `PROXY_*` configuration overrides.

If running the real-world Firebase integration test, set up `.env`:
```bash
cp .env.example .env
# Edit .env with your Facebook testing credentials (see Real-World JWT Tests below)
```

Minimum variables for basic development:
```bash
GOOGLE_CLOUD_PROJECT=your-dev-project
API_KEY_SECRET=dev-secret-key-123
```

### 4. Install Development Tools

**Install golangci-lint (optional but recommended):**

macOS:
```bash
brew install golangci-lint
```

Linux:
```bash
curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(go env GOPATH)/bin
```

## Building the Project

### Build Binary

```bash
# Build using Make (recommended)
make build

# Binary is created at: ./bin/lite-auth-proxy

# Or build manually
go build -v -ldflags "-s -w -X main.Version=1.0.0" -o bin/lite-auth-proxy ./cmd/proxy
```

### Build with Custom Version

```bash
# Version is automatically extracted from cmd/proxy/main.go
make build

# Or specify manually
go build -ldflags "-X main.Version=1.2.3" -o bin/lite-auth-proxy ./cmd/proxy
```

### Clean Build Artifacts

```bash
make clean
```

## Running the Proxy

### Run with Default Config

```bash
# Build and run
make run

# Or run directly
./bin/lite-auth-proxy
```

### Run with Custom Config

```bash
./bin/lite-auth-proxy -config config/config.test.toml
```

### Run with Environment Overrides

```bash
PROXY_SERVER_PORT=9090 \
PROXY_SERVER_TARGET_URL=http://localhost:8080 \
PROXY_AUTH_JWT_ENABLED=true \
./bin/lite-auth-proxy
```

## Testing

The project has comprehensive test coverage across all components.

### Test Structure

Tests are organized into two categories:

1. **Unit Tests** - Fast tests for individual components (no build tags)
2. **Integration Tests** - Full pipeline tests with mock servers (require `//go:build integration` tag)

### Running Tests

#### Unit Tests Only

```bash
# Run all unit tests (excludes integration tests)
make test

# Runs approximately 92 tests
# Output includes race detector
```

#### Integration Tests Only

```bash
# Run integration tests
make test-integration

# Uses build tag: -tags=integration
```

#### All Tests

```bash
# Run both unit and integration tests
make test-all

# Runs approximately 105 total tests
```

#### Specific Package Tests

```bash
# Test a specific package
go test -v ./internal/auth/jwt

# Test with race detector
go test -v -race ./internal/config

# Test specific function
go test -v -run TestValidateToken ./internal/auth/jwt
```

### Test Coverage

#### Generate Coverage Report

```bash
# Run tests and generate HTML coverage report
make coverage

# Opens coverage.html in browser
# Also creates coverage.out file
```

#### View Coverage by Package

```bash
go test -v -tags=integration -coverprofile=coverage.out ./...
go tool cover -func=coverage.out
```

#### Current Coverage Metrics

| Package | Coverage | Key Areas |
|---------|----------|-----------|
| `internal/config` | ~95% | Config loading, env substitution, validation |
| `internal/auth/jwt` | ~90% | JWT validation, JWKS, filters, mappings |
| `internal/auth/apikey` | ~100% | API-key validation, constant-time comparison |
| `internal/proxy` | ~85% | Reverse proxy, middleware, auth flow |
| `internal/ratelimit` | ~90% | Rate limiting, IP banning |
| `internal/logging` | ~80% | Structured logging setup |

### Test Files Organization

```
internal/
├── auth/
│   ├── apikey/
│   │   ├── apikey.go
│   │   └── apikey_test.go          # Unit tests
│   └── jwt/
│       ├── jwt.go
│       ├── jwt_test.go              # Unit tests
│       ├── realworld_test.go        # Real JWKS integration
│       └── testutil.go              # Test helpers
├── config/
│   ├── config.go
│   └── config_test.go               # Unit tests
├── admin/
│   ├── handler.go                   # Admin control-plane API
│   ├── handler_test.go
│   ├── auth.go                      # Admin JWT auth middleware
│   ├── auth_test.go
│   ├── types.go                     # Rule, request/response types
│   ├── rule_store.go                # In-memory rule store
│   └── rule_store_test.go
├── proxy/
│   ├── proxy.go
│   ├── proxy_test.go                # Unit tests
│   ├── middleware.go
│   ├── middleware_test.go
│   ├── dynamic_rule.go              # Admin dynamic rule check middleware
│   ├── dynamic_rule_test.go
│   ├── integration_test.go          # Integration tests (build tag)
│   └── feature_integration_test.go  # Scenario-based integration tests
├── startup/
│   ├── rule_loader.go               # Load persisted rules from PROXY_THROTTLE_RULES
│   └── rule_loader_test.go
└── ratelimit/
    ├── limiter.go
    └── limiter_test.go              # Unit tests
```

### Writing Tests

#### Unit Test Example

```go
package config

import "testing"

func TestLoad(t *testing.T) {
    cfg, err := Load("testdata/valid.toml")
    if err != nil {
        t.Fatalf("Load failed: %v", err)
    }
    
    if cfg.Server.Port != 8888 {
        t.Errorf("expected port 8888, got %d", cfg.Server.Port)
    }
}
```

#### Integration Test Example

```go
//go:build integration

package proxy

import (
    "testing"
    "net/http"
    "net/http/httptest"
)

func TestFullAuthFlow(t *testing.T) {
    // Setup mock backend
    backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // Verify auth headers are present
        if r.Header.Get("X-AUTH-USER-ID") == "" {
            t.Error("missing auth header")
        }
        w.WriteHeader(http.StatusOK)
    }))
    defer backend.Close()
    
    // Test with JWT...
}
```

### Test Environment Variables

#### Unit & Basic Integration Tests

Basic integration tests (mock servers) require minimal setup and run with `make test-all`:

```bash
# Unit tests run without environment variables
make test

# Integration tests (with mocks) run with make test-all
make test-all
```

#### Real-World JWT Tests (Firebase)

The `TestValidateRealWorldFirebaseJWT` test validates JWT tokens against real Google Cloud services.

**Prerequisites:**
- Google Cloud project with Firebase enabled
- Credentials for a Firebase test user (email:password)
- Google Cloud Secret Manager containing:
  - Firebase API Key (Web API key from Firebase Console)
  - Login credentials (email:password format)

**Setup `.env` file:**

```bash
# Copy template
cp .env.example .env

# Edit .env with your Firebase test credentials
nano .env
```

**Required environment variables:**

```bash
# Google Cloud project ID
GOOGLE_CLOUD_PROJECT=your-gcp-project

# Name of the secret in Secret Manager containing Firebase API Key
FIREBASE_API_KEY_SECRET_NAME=your-api-key-secret-name

# Name of the secret in Secret Manager containing Firebase test login (email:password)
FIREBASE_LOGIN_SECRET_NAME=your-login-secret-name
```

**Run the real-world test:**

```bash
# Load .env and run all tests (includes real-world Firebase test)
make test-all

# Or run only the Firebase test
bash -c 'source .env && go test -v -tags=integration -run TestValidateRealWorldFirebaseJWT ./internal/auth/jwt'
```

If environment variables are not set, the real-world test will be skipped (graceful degradation).

**Note:** The test requires GCP credentials to be configured (gcloud CLI authenticated). The proxy will fetch actual secret values from Google Cloud Secret Manager at runtime.

## Code Quality

### Linting

Run linting checks:

```bash
# Run golangci-lint
make lint

# Or manually
golangci-lint run ./...
```

### Code Formatting

```bash
# Format code
go fmt ./...

# Check formatting
gofmt -l .
```

### Vet Analysis

```bash
# Run go vet
go vet ./...
```

## Project Structure

```
lite-auth-proxy/
├── cmd/
│   └── proxy/
│       └── main.go                  # Application entry point
├── internal/
│   ├── auth/                        # Authentication logic
│   │   ├── apikey/                  # API-key authentication
│   │   └── jwt/                     # JWT authentication
│   ├── config/                      # Configuration loading
│   ├── logging/                     # Structured logging
│   ├── proxy/                       # Reverse proxy core
│   └── ratelimit/                   # Rate limiting
├── config/
│   └── config.toml                  # Default configuration
├── docs/                            # Documentation
├── bin/                             # Build output (gitignored)
├── .env.example                     # Environment variable template
├── .gitignore
├── Dockerfile                       # Container image
├── cloudbuild.yaml                  # Google Cloud Build config
├── go.mod                           # Go module dependencies
├── go.sum                           # Dependency checksums
├── Makefile                         # Build automation
└── README.md                        # Project overview
```

## Dependencies

### Core Dependencies

| Package | Version | Purpose |
|---------|---------|---------|
| `github.com/BurntSushi/toml` | v1.6.0 | TOML configuration parsing |

### Indirect Dependencies

- Google Cloud Secret Manager SDK (optional, for secret retrieval)
- Standard library packages for HTTP, crypto, logging

### Adding Dependencies

```bash
# Add a new dependency
go get github.com/package/name@version

# Tidy dependencies
go mod tidy

# Verify dependencies
go mod verify
```

## Debugging

### Enable Debug Logging

```bash
LOG_LEVEL=debug ./bin/lite-auth-proxy
```

### Debug with Delve

```bash
# Install Delve
go install github.com/go-delve/delve/cmd/dlv@latest

# Run with debugger
dlv debug ./cmd/proxy -- -config config/config.toml
```

### VSCode Debug Configuration

`.vscode/launch.json`:
```json
{
  "version": "0.2.0",
  "configurations": [
    {
      "name": "Launch Proxy",
      "type": "go",
      "request": "launch",
      "mode": "debug",
      "program": "${workspaceFolder}/cmd/proxy",
      "args": ["-config", "config/config.toml"],
      "env": {
        "LOG_MODE": "development",
        "LOG_LEVEL": "debug"
      }
    }
  ]
}
```

## Performance Testing

### Benchmarking

```bash
# Run benchmarks
go test -bench=. -benchmem ./...

# Specific benchmark
go test -bench=BenchmarkJWTValidation -benchtime=10s ./internal/auth/jwt
```

### Memory Profiling

```bash
# Generate memory profile
go test -memprofile=mem.out ./...

# Analyze profile
go tool pprof mem.out
```

### CPU Profiling

```bash
# Run with CPU profiling
go test -cpuprofile=cpu.out ./...

# Analyze profile
go tool pprof cpu.out
```

## Docker Development

### Build Docker Image

```bash
# Using Make (requires GOOGLE_CLOUD_PROJECT)
export GOOGLE_CLOUD_PROJECT=my-dev-project
make docker-build

# Or manually
docker build -t lite-auth-proxy:dev .
```

### Run in Docker

The image now defaults to `-config /config/config.toml` via Dockerfile `CMD`.

```bash
# Using Make
make docker-run DOCKER_TARGET_URL=http://host.docker.internal:8080

# Or manually
docker run --rm -p 8888:8888 \
  -e GOOGLE_CLOUD_PROJECT=test-project \
  -e PROXY_SERVER_TARGET_URL=http://host.docker.internal:8080 \
  -e LOG_MODE=development \
  lite-auth-proxy:dev

# Override config path
docker run --rm -p 8888:8888 \
  -e PROXY_SERVER_TARGET_URL=http://host.docker.internal:8080 \
  lite-auth-proxy:dev -config /path/to/custom-config.toml
```

### Test Docker Image

```bash
# Health check
curl http://localhost:8888/healthz

# Test with auth
curl -H "Authorization: Bearer <token>" http://localhost:8888/api/test
```

## Common Development Tasks

### Add a New Configuration Field

1. Add field to struct in `internal/config/config.go`
2. Add TOML tag if needed
3. Add environment variable override in `applyEnvOverrides()`
4. Add default value in `setDefaults()`
5. Add validation in `validate()`
6. Update documentation in `docs/CONFIGURATION.md`
7. Add tests in `internal/config/config_test.go`

### Add a New Middleware

1. Create middleware function in `internal/proxy/middleware.go`
2. Follow signature: `func(http.Handler) http.Handler`
3. Add to middleware pipeline in `NewHandler()`
4. Add tests in `internal/proxy/middleware_test.go`
5. Update documentation

### Add a New Authentication Method

1. Create new package under `internal/auth/`
2. Implement validation logic
3. Add configuration fields in `internal/config/config.go`
4. Integrate in `internal/proxy/proxy.go` handler
5. Add comprehensive tests
6. Update documentation

## Troubleshooting

### Tests Fail with "connection refused"

**Issue:** Integration tests can't connect to mock server.

**Solution:** Check if ports are already in use:
```bash
lsof -i :8888
lsof -i :9999
```

### JWKS Fetch Fails

**Issue:** Can't fetch JWKS from issuer during tests.

**Solution:** Use mock JWKS in tests or check network connectivity:
```bash
curl https://www.googleapis.com/oauth2/v3/certs
```

### Build Fails with "missing go.sum entry"

**Solution:**
```bash
go mod tidy
go mod verify
```

## Contributing Guidelines

1. **Fork** the repository
2. **Create** a feature branch: `git checkout -b feature/my-feature`
3. **Write tests** for new functionality
4. **Run tests** and linting: `make test-all && make lint`
5. **Commit** with clear messages
6. **Push** to your fork
7. **Create** a Pull Request

### Commit Message Format

```
type(scope): short description

Longer description if needed.

Fixes #123
```

Types: `feat`, `fix`, `docs`, `test`, `refactor`, `perf`, `chore`

## See Also

- [Configuration Guide](CONFIGURATION.md)
- [Environment Variables Guide](ENVIRONMENT.md)
- [API Documentation](API.md)
- [Deployment Guide](DEPLOYMENT.md)
