# lite-auth-proxy

# 1.3.0 [TBD]

## gRPC Transcoding Plugin

* **REST/JSON to gRPC transcoding** — new `grpctranscode` plugin (flex build only, priority 95) transcodes inbound REST/JSON requests to unary gRPC calls on upstream backends and gRPC responses back to JSON. Fully generic: learns services, methods, message schemas, and REST mappings at runtime via gRPC server reflection — no per-service code stubs and no transcoding config files.
* **Three route modes**: `annotation` (reads `google.api.http` method options), `convention` (POST `/<pkg>.<Service>/<Method>`), and `auto` (try annotation, fall back to convention).
* **Minimal config — backend defaults to `server.target_url`**: the only required gRPC setting is `grpc.enabled = true`; the gRPC backend is taken from `server.target_url` (its `host:port`; an `https://` scheme implies TLS). `[[grpc.backends]]` is **optional** — supply it only for multiple backends or `base_url` route namespacing, in which case it replaces the `server.target_url` default and `server.target_url` must resolve to one of the configured backend addresses (otherwise boot fails, preventing a contradictory config). Each backend's services are discovered independently via reflection.
* **Sidecar-safe, health-check-driven readiness**: the proxy boots in milliseconds and never fails or crash-loops because a gRPC backend is slow to start — `Start` only dials (lazily); it does not probe or discover. Readiness is established on demand by the health endpoint, with no background polling. The orchestrator's startup probe gates traffic.
* **`/healthz` translates to a live backend health probe**: new `plugin.ReadinessReporter` interface. On each `/healthz` call the plugin runs a live `grpc.health.v1.Health/Check` against **every** configured backend and, the first time a backend reports `SERVING`, discovers its services via reflection and installs routes (discovery is cached — once, never re-run). Backends are probed **concurrently** (one slow/down backend doesn't serialize the others — `/healthz` latency is the slowest single probe). Readiness is **all-or-nothing**: `/healthz` returns `200` only when every backend is healthy; if any backend fails the body names it. Per-backend results: dest port closed / `NOT_SERVING` → `503 "waiting: …"`; port open but health/reflection absent (`UNIMPLEMENTED`) → `503 "unavailable: …"`; all backends `SERVING`+discovered → `200`. When `grpc.enabled` is set this gRPC health check is **authoritative**: `server.health_check.target` (the HTTP health-proxy target) no longer applies and is not proxied to (a startup warning is logged if both are configured).
* **Discovery also bootstrapped by requests**: discovery isn't solely tied to `/healthz` — the first request to an undiscovered backend triggers the same probe (throttled per backend), so the endpoint works even when no startup probe is wired up.
* **gRPC-only routing (no HTTP fall-through)**: when `grpc.enabled` is set the backend is a gRPC service, so a request matching no discovered gRPC method returns **404** `application/problem+json` (or **503** while discovery is still pending), and a matched route whose backend's last probe was not-ready returns **503** ("service not ready") instead of dialling a dead backend. `server.target_url` remains required core config but is not used for routing in gRPC mode.
* **RFC 9457 error responses**: gRPC errors are returned as `application/problem+json` with standard grpc-gateway status code mapping.
* **`[grpc]` configuration section** with `PROXY_GRPC_*` env var overrides following the project's naming convention. Config env-override guard tests updated.
* **Integration tests** with an in-process gRPC server covering convention mode, base_url routing, gRPC status mapping, multi-method discovery, and the readiness/health-check behaviour.
* **Dedicated implementation docs** — [`docs/GRPC-TRANSCODING.md`](docs/GRPC-TRANSCODING.md) walks through discovery/validation at startup, the three route modes, the per-request transcoding hot path, gRPC→HTTP status mapping, and how to run a gRPC backend locally. Linked from the README and the plugin guide.
* **Local gRPC test backend** — `cmd/grpc-echo`, a tiny self-contained gRPC server (`greeter.v1.Greeter` with `SayHello`/`Echo`) exposing server reflection + health checking. It needs no protoc/code-gen and builds from the existing modules, so the plugin can be exercised end-to-end against a real gRPC service.
* **End-to-end coverage** — the Gherkin/behave e2e suite gained `features/grpc_transcoding.feature` (tagged `@flex-only @grpc`), driving the real Docker image in front of a `grpc-echo` container (`Dockerfile.grpcecho`, new `proxy-grpc` compose service on port 8890). Asserts JSON round-trip transcoding, multi-method routing, `NOT_FOUND`/`INVALID_ARGUMENT` → `404`/`400 problem+json`, and that a path matching no gRPC method returns `404 problem+json` (no HTTP fall-through). Scenarios self-skip on the lite image and against remote targets.
* **Negative testing** — backend-failure handling is covered at both layers. `grpc-echo` gained `-no-health`/`-no-reflection` toggles; integration tests `TestGRPCTranscodeMissingHealth`/`TestGRPCTranscodeMissingReflection` and a black-box `features/grpc_negative.feature` (`@negative`, throwaway `docker-compose.grpc-negative.yml` stack) assert that the proxy **stays up** and its `/healthz` returns `503` naming `health` or `reflection`, rather than crashing against an unusable gRPC backend.

# 1.2.1 [2026-06-01]

## Testing

* **End-to-end test suite (`e2e/`)** — a black-box suite that runs the actual Docker image (or a deployed service) and drives it over HTTP. Scenarios are written in plain-English Gherkin (Cucumber-style) and run with Python + behave, bootstrapped via `uv`. Covers health, JWT auth (using a real Firebase login for the `fp8devel` test user), API-key auth, the locked-down admin control plane, and rate limiting. Works against both the flex and lite images and against remote deployments; scenarios self-skip when prerequisites aren't met. Run with `make e2e-flex`, `make e2e-lite`, or `make e2e-remote URL=...`.

## Build & Packaging

* **Base image updated to `gcr.io/distroless/static-debian13:nonroot`** — both `Dockerfile.flex` and `Dockerfile.lite` now build on the Debian 13 distroless static base (previously Debian 12).
* **Docker images published to Docker Hub** — a GitHub Actions workflow now builds and pushes `farport/flex-auth-proxy` and `farport/lite-auth-proxy` on every published GitHub Release (with manual dispatch for testing). Images are tagged with the full version and the moving major.minor tag (e.g. `1.2.1` and `1.2`) — no `latest` tag is published. The release tag must match the version in `cmd/flex/main.go` exactly (no `v` prefix).
* **Debian build stage** — both Dockerfiles now build on `golang:1.24-trixie` (Debian 13) instead of Alpine, matching the runtime base.
* **Multi-platform images** — published images are now multi-arch manifests covering `linux/amd64` (Intel) and `linux/arm64` (Apple Silicon / ARM Linux); `docker pull` selects the right arch automatically. The Dockerfiles cross-compile using buildx `TARGETOS`/`TARGETARCH`. Local `make docker-build-*` builds the host arch only (a multi-arch manifest can only be pushed to a registry).
* **Unified image builds** — image build/tagging is now centralized in `scripts/docker-build.sh`, shared by the Makefile and the release workflow so local and CI builds are identical. The default `make docker-build-*`/`docker-push-*` targets target Docker Hub (`farport/*`); building/pushing to a private Google Artifact Registry moved to a separate `Makefile.gcp` (`make -f Makefile.gcp ...`).

## Bug Fixes

* **Fixed path matching for multi-segment URLs** — `include_paths` patterns ending with `/*` (e.g. `["/*"]`) now correctly match paths with multiple segments (e.g. `/api/limit-service/portfolio`) and paths with trailing slashes. Previously, Go's `path.Match` was used directly, which does not allow `*` to cross `/` boundaries, causing JWT authentication headers to be silently omitted for any path deeper than one level.

# 1.2.0 [2026-04-05]

## Plugin Architecture

* **Compile-time plugin system** — features are now modular plugins registered via Go `init()` + blank imports (Caddy/CoreDNS pattern). The core proxy is a minimal JWT-validating reverse proxy; rate limiting, admin API, API-key auth, and storage are all optional plugins.
* **Two build variants**: `flex-auth-proxy` (all plugins, full-featured) and `lite-auth-proxy` (no plugins, minimal JWT proxy). Both share the same core and produce separate Docker images.
* **Config validation at startup** — if a config section references a plugin that is not compiled in (e.g. `security.rate_limit.enabled = true` in the lite build), the proxy fails with a clear error message naming the missing plugin and the import path to add.

## Storage Plugin (Firestore)

* **Persistent rule storage via Firestore** — the new `storage-firestore` plugin provides `RuleStore` and `KeyValueStore` implementations backed by Google Cloud Firestore. Admin rules survive process restarts and are synchronized across Cloud Run instances in real-time via Firestore snapshot listeners.
* **Zero-latency hot path** — `ShouldAllow()` reads from an in-memory cache only; Firestore is used for persistence and cross-instance sync, not for per-request lookups.
* **Config**: `[storage]` section with `backend`, `project_id`, and `collection_prefix` fields. Environment variable overrides: `PROXY_STORAGE_BACKEND`, `PROXY_STORAGE_PROJECT_ID`, `PROXY_STORAGE_COLLECTION_PREFIX`.

## Core Refactoring

* **`internal/store` package** — extracted `Rule`, `RuleStatus`, `RuleStore` interface, `MemoryRuleStore`, `KeyValueStore` interface, and `MemoryKeyValueStore` from the admin package into a standalone core package. The admin package now uses type aliases for backwards compatibility.
* **`internal/plugin` package** — plugin registry with `Register`, `Get`, `All`, `OfType[T]`, `StorageBackend`, and `Reset` (test-only). Plugin interfaces: `Plugin`, `MiddlewareProvider`, `RouteProvider`, `AuthProvider`, `StorageBackendProvider`, `ConfigValidator`, `Starter`, `Stopper`.
* **7-phase plugin assembly** in `proxy.NewHandlerWithDeps`: default stores → storage backend → config validation → routes → auth providers → middleware → lifecycle.
* **Dual-path pipeline** — when plugins are registered the plugin pipeline is used; otherwise the legacy direct construction runs unchanged. All 131+ existing tests pass via the legacy path without modification.

## Build & Packaging

* **`Dockerfile.flex`** — builds `flex-auth-proxy` from `cmd/flex` (all plugins).
* **`Dockerfile.lite`** — builds `lite-auth-proxy` from `cmd/lite` (no plugins).
* **`config/config-flex.toml`** / **`config/config-lite.toml`** — default configs for each build.
* **Makefile targets**: `build-flex`, `build-lite`, `build-all`, `docker-build-flex`, `docker-build-lite`, `docker-build-all`.
* **Cloud Build** now builds and pushes both `flex-auth-proxy` and `lite-auth-proxy` images.
* **Startup log** now includes `build` field: `"lite"`, `"flex"`, or `"custom"`.

# 1.1.2 [2026-04-05]

## Rate Limiting

* **`skip_if_jwt_identified` for IP rate limiter** — new `security.rate_limit.skip_if_jwt_identified` flag (default `true`). When enabled, requests carrying a valid JWT `sub` claim bypass the IP rate limiter entirely and are governed solely by the JWT rate limiter. This prevents corporate users sharing a single outbound NAT IP from being incorrectly throttled at the IP level while still protecting against anonymous DDoS traffic.
* New env override: `PROXY_SECURITY_RATE_LIMIT_SKIP_IF_JWT_IDENTIFIED`.

# 1.1.1 [2026-04-01]

## Unified Rate Limiting

* **Single `RateLimiter` core** — all rate limiting logic (sliding window, bans, throttle delay) consolidated into one reusable `RateLimiter` struct, replacing the separate `Limiter`, `VertexAIBucket`, and `VertexAIKeyBucket` implementations.
* **Three rate-limit middlewares**: `IpRateLimit` (per-IP), `ApiKeyRateLimit` (per-API-key with configurable request matching), `JwtRateLimit` (per-JWT `sub` claim). Each has independent configuration (`requests_per_min`, `ban_for_min`, `throttle_delay_ms`).
* **Generic request matching** for API key rate limiting — replaces hardcoded Vertex AI detection. Configure `[[security.apikey_rate_limit.match]]` rules with `host`, `path` (exact or `/regex/`), and `header` fields. Multiple rules use OR logic; fields within a rule use AND logic.
* **Per-JWT rate limiting moved to middleware** — no longer embedded in the handler. Runs before JWT validation for early rejection.
* **DDoS-safe throttle delay** — optional per-limiter delay before returning 429 (bounded by `max_delay_slots` semaphore to prevent goroutine exhaustion under DDoS). Disabled by default (`throttle_delay_ms = 0`).
* **Admin API: `limiter` field** — `set-rule` command now accepts a `"limiter"` field (`"ip"`, `"apikey"`, `"jwt"`) to target a specific rate limiter at runtime.
* **Breaking:** admin status response `vertexAI` field replaced with `rateLimiters` map containing status for all three rate limiters.
* **New config sections:** `[security.apikey_rate_limit]`, `[security.jwt_rate_limit]` with independent settings. Existing `[security.rate_limit]` continues to control the IP rate limiter (backward compatible).
* **New env overrides:** `PROXY_SECURITY_APIKEY_RATE_LIMIT_*`, `PROXY_SECURITY_JWT_RATE_LIMIT_*`, `PROXY_SECURITY_RATE_LIMIT_THROTTLE_DELAY_MS`, `PROXY_SECURITY_RATE_LIMIT_MAX_DELAY_SLOTS`.

## Unified JWT Config

* **Merged `AdminJWTConfig` into `JWTConfig`** — the `[admin.jwt]` section now uses the same configuration structure as `[auth.jwt]`, eliminating the separate `AdminJWTConfig` type. The `admin.jwt` block accepts all standard JWT fields (`issuer`, `audience`, `tolerance_secs`, `cache_ttl_mins`, `filters`, `mappings`, `allowed_emails`).
* **Added `[admin.jwt.filters]`** — admin access can now be restricted by any JWT claim using exact-match or regex rules (e.g. `hd = "yourcompany.com"` to allow an entire Google Workspace domain).
* **Relaxed admin access control:** `allowed_emails` and `filters` are both optional; at least one must be provided when `admin.enabled = true`. When both are configured, all conditions must pass.

## Rate-Limit-Only Mode

* **Both auth methods can now be disabled simultaneously.** Setting `auth.jwt.enabled = false` and `auth.api_key.enabled = false` no longer causes a startup error. The proxy operates in **rate-limit-only mode**: all requests on protected paths are forwarded without credential checks while rate limiting and admin dynamic rules remain active. This is useful when only DDoS protection is needed without authentication.

## Flexible Email Access Control (`allowed_emails`)

* **`allowed_emails` added to `JWTConfig`** — both `[auth.jwt]` and `[admin.jwt]` now support an optional `allowed_emails` list to restrict access to specific email identities in the token's `email` claim.
* **Empty `allowed_emails` means no restriction.** Previously an unconfigured email list caused automatic rejection; it now means any authenticated token is accepted. Explicit lists are only enforced when non-empty.

# 1.1.0 [2026-03-31]

* Added optional `/admin` control-plane API (`POST /admin/control`, `GET /admin/status`) to set, remove, and inspect dynamic rate-limit rules at runtime without redeploying
* Dynamic rules are evaluated before per-IP rate limiting and support `throttle`, `block`, and `allow` actions with automatic expiry
* Added Vertex AI endpoint detection with a dedicated global and per-caller-identity rate-limit bucket, independently throttling AI traffic regardless of source IP
* Added `PROXY_THROTTLE_RULES` env var for persisting active rules across Cloud Run instance restarts
* Added `[admin]` config section with GCP service account identity token authentication (`admin.jwt.issuer`, `admin.jwt.audience`, `admin.jwt.allowed_emails`); disabled by default

## DDoS Hardening

* **Pre-auth rate limiting:** Rate limiter now applies to all requests regardless of authentication status, preventing attackers from sending unlimited requests with invalid credentials
* **Request body size limit:** Added `BodyLimiter` middleware that rejects requests exceeding `max_body_bytes` (default 1 MiB) with 413 status; checks `Content-Length` header upfront and wraps body with `http.MaxBytesReader` for streaming protection
* **Server timeouts:** Added `ReadTimeout` (30s), `WriteTimeout` (60s), and `IdleTimeout` (120s) to mitigate slowloris-style attacks
* **Removed API key rate limit double-counting:** Handler-level IP rate limiting for API key auth removed since middleware already enforces it
* Added `PROXY_SECURITY_MAX_BODY_BYTES` env var and `security.max_body_bytes` TOML config option

# 1.0.4 [2026-03-01]

* Added `-healthcheck` command line option for docker compose
* Merged `newReverseProxy` and `newHealthProxy` to fix `PROXY_SERVER_HEALTH_CHECK_TARGET`
* Renamed `configs` directory to `config`
* Fixed the bug where `auth.jwt.filters` and `auth.jwt.mappings` was not applied the ENV override
* Added ENV overrides: `PROXY_SERVER_INCLUDE_PATHS`, `PROXY_SERVER_EXCLUDE_PATHS`, `PROXY_AUTH_JWT_FILTERS_*`, `PROXY_AUTH_JWT_MAPPINGS_*`, `PROXY_AUTH_JWT_TOLERANCE_SECS`, `PROXY_AUTH_JWT_CACHE_TTL_MINS`, `PROXY_AUTH_API_KEY_PAYLOAD_*`

# 1.0.2 [2026-02-28]

* Initial Release
