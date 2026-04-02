# CLAUDE.md — lite-auth-proxy

Development guide for AI coding assistants working in this repository.

## Project Overview

`lite-auth-proxy` is a lightweight reverse proxy providing JWT and API-Key authentication, per-IP/per-key/per-JWT rate limiting, and a dynamic admin control-plane — designed for serverless sidecar deployments (e.g. Google Cloud Run).

## Key Rules

### 1. Documentation Structure

- **`README.md`** — overview only: key features, quick start, architecture diagram, links to docs. Do NOT add detailed config, API references, or deployment instructions there.
- **`docs/`** — all detail goes here:
  - `docs/CONFIGURATION.md` — full config reference
  - `docs/RATE-LIMITING.md` — rate limiting guide
  - `docs/ENVIRONMENT.md` — env var overrides
  - `docs/API.md` — HTTP endpoints and admin API
  - `docs/DEPLOYMENT.md` — Docker, Cloud Run, sidecar setup
  - `docs/DEVELOPMENT.md` — dev setup, testing, debugging

### 2. Version and Release Notes

- **Version string** lives in `cmd/proxy/main.go` lines 23–24:
  ```go
  Version = "1.1.1"
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
cmd/proxy/main.go          # Entry point; version constant
internal/config/config.go  # Config structs and TOML/env parsing
internal/admin/            # Admin control-plane (handler, types, rule store)
internal/auth/             # JWT and API-key auth
internal/proxy/            # Middleware pipeline and reverse proxy
internal/ratelimit/        # Unified rate limiter (IP, API-key, JWT)
internal/startup/          # Rule loading from PROXY_THROTTLE_RULES
docs/                      # All detailed documentation
config/config.toml         # Default configuration
```

## Config → Env Var Naming Convention

All config fields map to `PROXY_<SECTION>_<FIELD>` env vars (uppercase, `_` as separator). Array/map fields use indexed suffixes. Examples:
- `security.rate_limit.requests_per_min` → `PROXY_SECURITY_RATE_LIMIT_REQUESTS_PER_MIN`
- `auth.jwt.filters.email` → `PROXY_AUTH_JWT_FILTERS_email`

When adding new config fields, add the corresponding env var override in `internal/config/config.go` and document it in `docs/ENVIRONMENT.md`.

## Testing

```bash
make test          # unit tests only (~92 tests, always verbose)
make test-all      # unit + integration tests (~105 tests)
make coverage      # all tests with HTML coverage report
make lint          # golangci-lint
```

Integration tests in `internal/proxy/integration_test.go` run a full proxy stack and are the primary validation. Prefer integration tests over unit tests with mocks.

## Build and Run

```bash
make build         # ./bin/lite-auth-proxy
make run           # run with config/config.toml + .env
make docker-build  # build Docker image
```
