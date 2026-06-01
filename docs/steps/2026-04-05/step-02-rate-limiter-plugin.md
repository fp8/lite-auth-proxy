# Step 02: Rate Limiter Plugin

## Objective

Extract the three rate-limiting layers (per-IP, per-API-key, per-JWT) into a self-contained plugin. When this plugin is not compiled in, the proxy performs no rate limiting and the `[security.rate_limit]`, `[security.apikey_rate_limit]`, and `[security.jwt_rate_limit]` config sections are rejected at startup.

## Dependencies

- Step 01 (plugin infrastructure, registry, `MiddlewareProvider` interface)

## Context

Rate limiting is currently wired directly into the proxy handler factory. This step moves it behind the plugin interface so that:

1. The lite build has zero rate-limiting code, goroutines, or memory overhead.
2. The rate-limiting middleware is registered via `plugin.Register()` and assembled by the core pipeline.
3. The `deps.RateLimiters` map is populated by the plugin, making it available to the admin plugin for runtime tuning.

---

## Plugin Specification

| Property | Value |
|----------|-------|
| **Name** | `ratelimit` |
| **Priority** | `60` (IP), `70` (JWT), `80` (API-key) — preserves current pipeline order |
| **Implements** | `MiddlewareProvider`, `ConfigValidator`, `Stopper` |

### Middleware contributed

The plugin contributes three middleware functions, in this order:

1. **ApiKeyRateLimit** (priority 60) — per-API-key with request matching
2. **JwtRateLimit** (priority 70) — per-JWT-`sub` with optional IP prefix
3. **IpRateLimit** (priority 80) — per-IP with optional JWT-identified skip

Each middleware is only added if its corresponding config section has `enabled = true`. If all three are disabled but the plugin is compiled in, it contributes no middleware (no-op).

### Rate limiter instances

The plugin creates up to three `RateLimiter` instances and stores them in `deps.RateLimiters`:

```go
deps.RateLimiters = map[string]*ratelimit.RateLimiter{
    "ip":     ipLimiter,
    "apikey": apikeyLimiter,
    "jwt":    jwtLimiter,
}
```

This map is the contract between the rate limiter plugin and the admin plugin. The admin plugin reads it to dynamically tune limiters at runtime. If the rate limiter plugin is not present, `deps.RateLimiters` is `nil` and the admin plugin skips limiter-targeting logic.

### Config validation

The plugin validates:

- `security.apikey_rate_limit.match` entries contain valid regex patterns (when using `/regex/` syntax).
- `security.apikey_rate_limit.key_header` is non-empty when API-key rate limiting is enabled.
- Numeric fields (`requests_per_min`, `ban_for_min`, `max_delay_slots`) are positive when their limiter is enabled.

### Shutdown

The plugin does not currently run background goroutines of its own (rate limiter cleanup is internal to the `RateLimiter` type). The `Stop()` lifecycle hook is reserved for future use (e.g. flushing metrics).

---

## Config Sections Owned

The following config sections are gated behind this plugin:

```toml
[security.rate_limit]
enabled = true
requests_per_min = 60
ban_for_min = 5
skip_if_jwt_identified = true
throttle_delay_ms = 0
max_delay_slots = 100

[security.apikey_rate_limit]
enabled = false
requests_per_min = 200
ban_for_min = 5
include_ip = false
key_header = "x-goog-api-key"
throttle_delay_ms = 0
max_delay_slots = 100

[[security.apikey_rate_limit.match]]
host = "/.*-aiplatform\\.googleapis\\.com/"

[security.jwt_rate_limit]
enabled = false
requests_per_min = 200
ban_for_min = 5
include_ip = true
throttle_delay_ms = 0
max_delay_slots = 100
```

**If the plugin is not compiled in** and any of `security.rate_limit.enabled`, `security.apikey_rate_limit.enabled`, or `security.jwt_rate_limit.enabled` is `true`, the core must fail at startup:

```
FATAL: rate limiting is configured (security.rate_limit.enabled = true) but the
ratelimit plugin is not compiled in. Use the full build image or add the plugin
import to your custom build.
```

**If the plugin is compiled in** but all three `enabled` flags are `false`, the plugin is inert — no middleware, no goroutines, no memory overhead beyond the empty `RateLimiters` map.

---

## Interaction with Other Plugins

### Admin plugin (consumer)

The admin plugin reads `deps.RateLimiters` to:
- Update `requestsPerMin`, `throttleDelay`, and `maxDelaySlots` on a targeted limiter when a `set-rule` with `limiter` field is received.
- Report limiter status in `GET /admin/status`.

If `deps.RateLimiters` is `nil` (rate limiter plugin not present), the admin plugin:
- Ignores the `limiter` field in `set-rule` commands.
- Omits the `rateLimiters` section from the status response.

This ensures the admin plugin functions independently of the rate limiter plugin.

### Storage plugin (no interaction)

The rate limiter plugin has no interaction with the storage plugin. Rate limit counters are always in-memory and per-instance. Cross-instance rate limiting is not a goal — each instance maintains its own counters.

---

## Tests

### Plugin registration

1. When the plugin package is imported, `plugin.Get("ratelimit")` returns non-nil.
2. The plugin implements `MiddlewareProvider`, `ConfigValidator`, and `Stopper`.

### Middleware assembly

3. With all three limiters enabled, `BuildMiddleware()` returns exactly 3 middleware functions.
4. With only IP limiter enabled, returns 1 middleware function.
5. With all disabled, returns an empty slice.

### Config validation

6. Invalid regex in `apikey_rate_limit.match` returns a validation error.
7. `requests_per_min = 0` with `enabled = true` returns a validation error.
8. Disabled limiters with invalid config values do not produce errors (config is not validated when disabled).

### Behavioral parity

9. All existing rate limiter integration tests pass unchanged when running against the full build.

---

## Verification

```bash
# Plugin-specific unit tests
go test ./internal/plugins/ratelimit/... -race -count=1

# Full build: all existing tests pass
go test ./... -race -count=1

# Lite build: rate limit config rejected
./bin/lite-auth-proxy-lite -config config/config.toml
# Expected error: "ratelimit plugin is not compiled in"
```
