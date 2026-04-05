# lite-auth-proxy

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
