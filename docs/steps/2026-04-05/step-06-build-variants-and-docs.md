# Step 06: Build Variants, Configuration, and Documentation

## Objective

Define the two official Docker images (lite and full), restructure the configuration to support plugin detection, update the Makefile and Dockerfiles, and reorganize documentation to reflect the plugin architecture.

## Dependencies

- Steps 01–05 (all plugin infrastructure and plugin implementations)

## Context

With the plugin system in place, the project needs:
1. Two build entry points and two Dockerfiles.
2. A configuration model where plugin-owned sections are cleanly separated.
3. Updated documentation that explains the plugin model, build variants, and which features are available in which image.

---

## Build Variants

### Entry points

| Path | Image | Plugins | Use case |
|------|-------|---------|----------|
| `cmd/proxy-lite/main.go` | `lite-auth-proxy:X.Y.Z-lite` | none | Minimal JWT proxy |
| `cmd/proxy/main.go` | `lite-auth-proxy:X.Y.Z` | all | Full-featured proxy |

### Binary naming

| Binary | Description |
|--------|-------------|
| `lite-auth-proxy` | Full build (default, backwards-compatible) |
| `lite-auth-proxy-lite` | Lite build |

### Version reporting

Both binaries report the same version (from `ldflags`). The `/healthz` response and startup log include the build variant:

```json
{"status": "ok", "version": "1.2.0", "variant": "lite"}
{"status": "ok", "version": "1.2.0", "variant": "full"}
```

The variant is determined by querying the plugin registry at startup:

```go
func buildVariant() string {
    if len(plugin.All()) == 0 {
        return "lite"
    }
    return "full"
}
```

Custom builds (user-assembled plugins) report `"custom"`.

---

## Dockerfiles

### Dockerfile (full build — default)

```dockerfile
FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags="-s -w -X main.Version=${VERSION}" \
    -o /proxy ./cmd/proxy

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /proxy /proxy
COPY config/config.toml /config/config.toml
ENTRYPOINT ["/proxy"]
CMD ["-config", "/config/config.toml"]
```

### Dockerfile.lite (lite build)

```dockerfile
FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags="-s -w -X main.Version=${VERSION}" \
    -o /proxy ./cmd/proxy-lite

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /proxy /proxy
COPY config/config-lite.toml /config/config.toml
ENTRYPOINT ["/proxy"]
CMD ["-config", "/config/config.toml"]
```

**Key difference**: The lite Dockerfile copies `config/config-lite.toml` (which has only core config sections) as the default config.

---

## Configuration Files

### config/config.toml (full build default)

The existing `config/config.toml` remains the default for the full build. It includes all config sections (server, security, auth, admin). No changes needed.

### config/config-lite.toml (lite build default)

A new minimal config for the lite build:

```toml
[server]
port = 8888
target_url = "http://localhost:8080"
include_paths = ["/*"]
exclude_paths = ["/healthz"]
shutdown_timeout_secs = 10

[server.health_check]
path = "/healthz"

[auth.jwt]
enabled = true
issuer = "https://securetoken.google.com/{{ENV.GOOGLE_CLOUD_PROJECT}}"
audience = "{{ENV.GOOGLE_CLOUD_PROJECT}}"

[auth.jwt.mappings]
sub = "USER-ID"
email = "USER-EMAIL"
```

This config contains only core sections. If a user tries to add `[security.rate_limit]` with `enabled = true` and runs the lite binary, they get a clear error at startup.

---

## Configuration Structure (post-plugin)

The top-level config struct adds a `Storage` section:

```
Config
├── Server        (core — always available)
├── Security      (plugin-gated — requires ratelimit plugin)
├── Auth
│   ├── HeaderPrefix  (core)
│   ├── JWT           (core — always available)
│   └── APIKey        (plugin-gated — requires apikey plugin)
├── Admin         (plugin-gated — requires admin plugin)
└── Storage       (plugin-gated — requires a storage-* plugin)
```

### New storage config section

```toml
[storage]
backend = ""                          # "firestore", "gcs", etc. Empty = no storage.

[storage.firestore]
project_id = ""                       # Defaults to GOOGLE_CLOUD_PROJECT
collection_prefix = "proxy"
```

### Config validation flow

```
1. Parse TOML + apply env overrides + set defaults
2. Core validation (server port, target_url, JWT required fields)
3. Plugin availability check:
   - For each plugin-gated section that is enabled,
     verify the required plugin is registered.
   - Fail with a clear error if not.
4. Plugin-specific validation:
   - Call ValidateConfig() on each registered plugin.
   - Each plugin validates only its own config section.
```

### Error message format

All plugin-availability errors follow a consistent format:

```
FATAL: {feature} is configured ({config_key} = {value}) but the {plugin_name}
plugin is not compiled in. Use the full build image (lite-auth-proxy:X.Y.Z) or
add the plugin import to your custom build:

    import _ "github.com/fp8/lite-auth-proxy/internal/plugins/{path}"
```

This gives the operator the exact import path needed to resolve the issue.

---

## Makefile Updates

```makefile
# Build
build:
	go build -ldflags="-X main.Version=$(VERSION)" -o ./bin/lite-auth-proxy ./cmd/proxy

build-lite:
	go build -ldflags="-X main.Version=$(VERSION)" -o ./bin/lite-auth-proxy-lite ./cmd/proxy-lite

build-all: build build-lite

# Docker
docker-build:
	docker build --build-arg VERSION=$(VERSION) -t lite-auth-proxy:$(VERSION) .

docker-build-lite:
	docker build --build-arg VERSION=$(VERSION) -f Dockerfile.lite \
		-t lite-auth-proxy:$(VERSION)-lite .

docker-build-all: docker-build docker-build-lite

# Test
test:
	go test ./... -v -race -count=1

test-lite:
	go build -o /dev/null ./cmd/proxy-lite && \
	go test ./internal/core/... ./internal/auth/jwt/... -v -race -count=1

test-all: test
	go test ./... -v -race -count=1 -tags=integration

# Cloud Build
cloud-build:
	gcloud builds submit --config=cloudbuild.yaml --project=$(GOOGLE_CLOUD_PROJECT)
```

---

## Cloud Build Updates

The `cloudbuild.yaml` should build and push both images:

```yaml
steps:
  - name: golang:1.23-alpine
    id: test
    entrypoint: go
    args: ['test', './...', '-race', '-count=1']

  - name: gcr.io/cloud-builders/docker
    id: build-full
    args: ['build', '--build-arg', 'VERSION=${_VERSION}',
           '-t', '${_REGISTRY}/${PROJECT_ID}/${_REPO}/lite-auth-proxy:${_VERSION}',
           '.']

  - name: gcr.io/cloud-builders/docker
    id: build-lite
    args: ['build', '--build-arg', 'VERSION=${_VERSION}',
           '-f', 'Dockerfile.lite',
           '-t', '${_REGISTRY}/${PROJECT_ID}/${_REPO}/lite-auth-proxy:${_VERSION}-lite',
           '.']

images:
  - '${_REGISTRY}/${PROJECT_ID}/${_REPO}/lite-auth-proxy:${_VERSION}'
  - '${_REGISTRY}/${PROJECT_ID}/${_REPO}/lite-auth-proxy:${_VERSION}-lite'
```

---

## Documentation Restructuring

### New and updated documentation files

| File | Change | Description |
|------|--------|-------------|
| `docs/PLUGINS.md` | **New** | Plugin architecture overview, available plugins, how to create custom builds |
| `docs/CONFIGURATION.md` | **Updated** | Add storage section, mark plugin-gated sections, add config validation behavior |
| `docs/DEPLOYMENT.md` | **Updated** | Add lite vs. full image guidance, update Cloud Run examples for both variants |
| `docs/ADMIN.md` | **Updated** | Add single-instance warning when storage is absent, add multi-instance guidance with storage |
| `docs/API.md` | **Updated** | Add multi-key API-key management commands (conditional on plugins) |
| `docs/DEVELOPMENT.md` | **Updated** | Add plugin development guide, testing with/without plugins |
| `README.md` | **Updated** | Add build variant overview, link to PLUGINS.md |
| `.claude/CLAUDE.md` | **Updated** | Add plugin layout to code structure, add PLUGINS.md to doc list |

### docs/PLUGINS.md (new)

Table of contents:

```
# Plugin Architecture

## Overview
- What is a plugin
- Why plugins
- Available plugins

## Build Variants
- Lite build (what's included, what's not)
- Full build (everything)
- Custom builds (how to assemble your own)

## Available Plugins
### Rate Limiter
- What it provides
- Config sections it owns
- Interactions with other plugins

### Admin Control Plane
- What it provides
- In-memory vs. persistent mode
- Single-instance vs. multi-instance deployment

### API-Key Authentication
- Single-key vs. multi-key mode
- Dependencies for multi-key

### Storage: Firestore
- What it provides
- GCP setup required
- Future backends

## Creating a Custom Build
- Step-by-step guide
- Example: rate limiting + API key, no admin
- Example: admin + Firestore, no rate limiting

## Plugin Development Guide
- Plugin interfaces
- Registration pattern
- Testing guidelines
```

### Documentation for single-instance vs. multi-instance

This is a critical user-facing concern. The following table must appear in `docs/PLUGINS.md`, `docs/ADMIN.md`, and `docs/DEPLOYMENT.md`:

```
┌─────────────────────────────────────────────────────────────────────────┐
│                    Deployment Model by Plugin Combination               │
├───────────────────┬─────────────────┬───────────────────────────────────┤
│ Admin Plugin      │ Storage Plugin  │ Cloud Run Deployment              │
├───────────────────┼─────────────────┼───────────────────────────────────┤
│ not compiled in   │ n/a             │ Any (no admin state to sync)      │
│ enabled           │ not compiled in │ max-instances=1 recommended       │
│                   │                 │ (rules are per-instance)          │
│ enabled           │ enabled         │ Any (rules synced via storage)    │
└───────────────────┴─────────────────┴───────────────────────────────────┘
```

### CONFIGURATION.md updates

Each plugin-gated config section must include a note:

```markdown
> **Plugin required:** This section requires the `ratelimit` plugin. If this
> section is configured with `enabled = true` but the plugin is not compiled in,
> the proxy will fail at startup. The lite build does not include this plugin.
> Use the full build image or create a custom build.
```

### DEPLOYMENT.md updates

Add a section on choosing the right image:

```markdown
## Choosing a Build Variant

| Need | Image |
|------|-------|
| JWT proxy only, minimal footprint | `lite-auth-proxy:X.Y.Z-lite` (~8 MB) |
| Rate limiting, admin API, API keys | `lite-auth-proxy:X.Y.Z` (~15 MB) |
| Subset of features | Custom build (see [Plugin Guide](PLUGINS.md)) |
```

---

## RELEASE.md Entry

The plugin infrastructure warrants a major version bump (e.g. 2.0.0) since it reorganizes the codebase. The release notes should emphasize:

- **No behavioral changes for existing full-build users.** The default image includes all plugins and behaves identically to the pre-plugin version.
- **New lite image** for minimal deployments.
- **New storage plugin** for persistent, cross-instance admin rules.
- **Multi-key API-key support** when storage is enabled.
- **Config validation** now detects misconfigured plugins at startup.

---

## Migration Guide

For existing users upgrading from the monolithic build:

1. **If you use `lite-auth-proxy:X.Y.Z`**: No changes needed. The full build includes all plugins. Your config works as-is.
2. **If you want to switch to the lite image**: Use `lite-auth-proxy:X.Y.Z-lite`. Remove or comment out `[security.*]`, `[admin]`, and `[auth.api_key]` sections from your config. If these sections are present with `enabled = true`, the lite binary will fail at startup with a clear error.
3. **If you want persistent admin rules**: Add `[storage]` section to your config and ensure the Firestore plugin is in your build (it is included in the full image by default).

---

## Tests

### Build variant tests

1. `cmd/proxy-lite/main.go` compiles without errors.
2. `cmd/proxy/main.go` compiles without errors.
3. Lite binary starts with `config-lite.toml` and serves `/healthz`.
4. Lite binary with `config.toml` (rate limiting enabled) fails at startup with expected error.
5. Full binary starts with `config.toml` and all features work.
6. Full binary starts with `config-lite.toml` (no plugins active) and works as a lite proxy.

### Docker image tests

7. `docker build` (full) succeeds and produces a working image.
8. `docker build -f Dockerfile.lite` succeeds and produces a working image.
9. Full image size is under 25 MB.
10. Lite image size is under 15 MB.

### Config validation tests

11. All existing config tests pass against the full build.
12. Plugin-gated sections with missing plugins produce clear error messages.
13. Plugin-gated sections that are not enabled produce no errors even when plugin is absent.

### Documentation tests

14. All internal doc links resolve (no broken cross-references).
15. `docs/PLUGINS.md` exists and is linked from README.md.

---

## Verification

```bash
# Both builds compile
make build-all

# Both Docker images build
make docker-build-all

# All tests pass
make test-all

# Lite binary rejects full config
./bin/lite-auth-proxy-lite -config config/config.toml 2>&1 | grep "plugin is not compiled in"

# Full binary accepts full config
./bin/lite-auth-proxy -config config/config.toml &
curl -s http://localhost:8888/healthz | jq .variant
# Expected: "full"

# Lite binary accepts lite config
./bin/lite-auth-proxy-lite -config config/config-lite.toml &
curl -s http://localhost:8888/healthz | jq .variant
# Expected: "lite"
```
