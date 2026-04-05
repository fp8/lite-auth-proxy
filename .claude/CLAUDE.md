# CLAUDE.md — lite-auth-proxy

Development guide for AI coding assistants working in this repository.

## Project Overview

`lite-auth-proxy` is a lightweight reverse proxy providing JWT and API-Key authentication, per-IP/per-key/per-JWT rate limiting, and a dynamic admin control-plane — designed for serverless sidecar deployments (e.g. Google Cloud Run).

## Key Rules

### 1. Documentation Structure

- **`README.md`** — overview only: key features, quick start, architecture diagram, links to docs. Do NOT add detailed config, API references, or deployment instructions there.
- **`docs/`** — all detail goes here:
  - `docs/PLUGINS.md` — plugin architecture, per-plugin config reference, custom builds
  - `docs/CONFIGURATION.md` — core config reference, cross-plugin scenarios
  - `docs/RATE-LIMITING.md` — rate limiting guide
  - `docs/ENVIRONMENT.md` — env var overrides
  - `docs/API.md` — HTTP endpoints and admin API
  - `docs/ADMIN.md` — Admin control plane, rule lifecycle, serverless caveats
  - `docs/DEPLOYMENT.md` — Docker, Cloud Run, sidecar setup, build variants
  - `docs/DEVELOPMENT.md` — dev setup, testing, debugging

### 2. Version and Release Notes

- **Version string** lives in `cmd/flex/main.go` lines 28–31:
  ```go
  Version = "1.2.0"
  ```
  Update this on every release.

- **`RELEASE.md`** must be updated with every change. Format: `# X.Y.Z [YYYY-MM-DD]` followed by bullet points grouped by feature area. Latest release at the top.

### 3. Admin Interface Parity

Whenever a **configurable feature** is added (especially security and rate-limiting), the same setting must also be exposed through the admin API (`POST /admin/control`) if it makes sense to update at runtime.

Current admin-configurable settings (via `set-rule`):
- `throttleDelayMs` — update throttle delay on a running limiter
- `maxDelaySlots` — update max concurrent delayed responses
- `maxRPM` — max requests per minute for a rule
- `action` — `throttle`, `block`, or `allow`
- `limiter` — target which limiter: `ip`, `apikey`, `jwt`
- `rateByKey`, `pathPattern`, `targetHost`, `durationSeconds`

If you add a new rate-limiting or security knob to `config.go`, check whether it should be settable via `POST /admin/control` and add it to `internal/admin/handler.go` and `internal/admin/types.go` accordingly.

## Code Layout

```
cmd/flex/main.go           # Flex build entry point (all plugins); version constant
cmd/lite/main.go           # Lite build entry point (no plugins)
internal/config/config.go  # Config structs and TOML/env parsing
internal/plugin/           # Plugin registry and interfaces
internal/plugins/          # Plugin implementations:
  ratelimit/               #   Rate limiting plugin (priority 60)
  admin/                   #   Admin control-plane plugin (priority 50)
  apikey/                  #   API-key auth plugin (priority 90)
  storage/firestore/       #   Firestore storage plugin (priority 5)
internal/store/            # RuleStore/KeyValueStore interfaces and in-memory defaults
internal/admin/            # Admin handler, types (uses store.Rule via type alias)
internal/auth/             # JWT and API-key auth
internal/proxy/            # Middleware pipeline and reverse proxy
internal/ratelimit/        # Unified rate limiter (IP, API-key, JWT)
internal/startup/          # Rule loading from PROXY_THROTTLE_RULES
docs/                      # All detailed documentation
config/config-flex.toml    # Default configuration (flex build)
config/config-lite.toml    # Default configuration (lite build)
```

## Config → Env Var Naming Convention

All config fields map to `PROXY_<SECTION>_<FIELD>` env vars (uppercase, `_` as separator). Array/map fields use indexed suffixes. Examples:
- `security.rate_limit.requests_per_min` → `PROXY_SECURITY_RATE_LIMIT_REQUESTS_PER_MIN`
- `auth.jwt.filters.email` → `PROXY_AUTH_JWT_FILTERS_email`

When adding new config fields, add the corresponding env var override in `internal/config/config.go` and document it in `docs/ENVIRONMENT.md`.

## Testing

```bash
make test          # unit tests only (~150 tests, always verbose)
make test-all      # unit + integration tests (~190 tests)
make coverage      # all tests with HTML coverage report
make lint          # golangci-lint
```

Integration tests in `internal/proxy/integration_test.go` run a full proxy stack and are the primary validation. Prefer integration tests over unit tests with mocks.

## Build and Run

```bash
make build-flex    # ./bin/flex-auth-proxy (all plugins)
make build-lite    # ./bin/lite-auth-proxy (no plugins)
make build-all     # both binaries
make run-flex      # run flex-auth-proxy with config/config-flex.toml + .env
make run-lite      # run lite-auth-proxy with config/config-lite.toml + .env
make docker-build-flex  # build flex-auth-proxy Docker image
make docker-build-lite  # build lite-auth-proxy Docker image
make docker-build-all   # both Docker images
```
