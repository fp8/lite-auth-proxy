# Rate Limiting Guide

> **Plugin required:** Rate limiting requires the `ratelimit` plugin (`_ "github.com/fp8/lite-auth-proxy/internal/plugins/ratelimit"`). It is included in the full build but not in the lite build. See the [Plugin Guide](PLUGINS.md) for build variants.

lite-auth-proxy provides a unified, multi-layer rate limiting system that protects your backend from abuse. Each layer targets a different identity dimension and can be enabled independently.

## Rate Limiter Layers

| Layer | Config Section | Key Identity | Use Case |
|-------|---------------|-------------|----------|
| **Per-IP** | `[security.rate_limit]` | Client IP address | General DDoS protection |
| **Per-API-Key** | `[security.apikey_rate_limit]` | API key header value | Key-level abuse prevention |
| **Per-JWT** | `[security.jwt_rate_limit]` | JWT `sub` claim | User-level rate limiting |

All three layers share the same configuration knobs and behavior model. They are evaluated in order: API-key -> JWT -> IP. A request rejected by an earlier layer never reaches later ones.

## Configuration Reference

The TOML blocks below reflect the shipped `config/config-flex.toml` defaults. The **Default** column in each table shows the code fallback when a field is omitted entirely from the config file.

### Per-IP Rate Limiting

```toml
[security.rate_limit]
enabled = true                       # First line of defense — on by default
requests_per_min = 60                # Reasonable ceiling for anonymous/unauthenticated traffic
ban_for_min = 5                      # Short enough to not block legitimate users for long
skip_if_jwt_identified = true        # Authenticated (JWT) users bypass the IP limit —
                                     # prevents penalising corporate NAT users who are
                                     # already governed by the JWT rate limiter
# throttle_delay_ms = 0             # Disabled by default to keep response latency predictable
# max_delay_slots = 100             # Only relevant when throttle_delay_ms > 0
```

**Why these defaults:** IP rate limiting is the broadest protection layer and is enabled out of the box. The 60 req/min limit is conservative enough to stop basic abuse while allowing normal browsing. `skip_if_jwt_identified` is `true` because in environments where many authenticated users share a corporate NAT IP, the combined traffic would quickly exhaust the IP bucket — those users are better served by the per-JWT limiter. Throttle delay is off by default because most deployments prefer a clean 429 rejection over holding connections open.

| Field | Type | Default | ENV Variable | Description |
|-------|------|---------|-------------|-------------|
| `enabled` | bool | `false` | `PROXY_SECURITY_RATE_LIMIT_ENABLED` | Enable per-IP rate limiting |
| `requests_per_min` | int | `60` | `PROXY_SECURITY_RATE_LIMIT_REQUESTS_PER_MIN` | Max requests per IP per minute |
| `ban_for_min` | int | `5` | `PROXY_SECURITY_RATE_LIMIT_BAN_FOR_MIN` | Ban duration after limit exceeded (minutes) |
| `skip_if_jwt_identified` | bool | `true` | `PROXY_SECURITY_RATE_LIMIT_SKIP_IF_JWT_IDENTIFIED` | Skip IP rate limit when a JWT `sub` claim is present; see [Corporate NAT scenario](#corporate-nat-and-shared-ip-scenarios) |
| `throttle_delay_ms` | int | `0` | `PROXY_SECURITY_RATE_LIMIT_THROTTLE_DELAY_MS` | Delay before 429 response (ms); `0` = disabled |
| `max_delay_slots` | int | `100` | `PROXY_SECURITY_RATE_LIMIT_MAX_DELAY_SLOTS` | Max concurrent throttled responses (DDoS cap) |

### Per-API-Key Rate Limiting

```toml
[security.apikey_rate_limit]
enabled = false                      # Opt-in — only needed when your backend uses API keys
requests_per_min = 200               # Higher than IP limit because API keys represent known,
                                     # trusted integrations rather than anonymous traffic
ban_for_min = 5                      # Standard ban duration
include_ip = false                   # Keys typically identify a specific integration, not a user;
                                     # a single key from multiple IPs is usually the same client
key_header = "x-goog-api-key"        # Pre-configured for Google Cloud / Vertex AI usage
# throttle_delay_ms = 0             # Disabled by default
# max_delay_slots = 100             # Only relevant when throttle_delay_ms > 0

# Request matching rules — rate limiting only applies to matching requests.
# Multiple [[match]] entries use OR logic; fields within a rule use AND logic.
# Host/Path support exact strings or /regex/ syntax.
#
# Example: Vertex AI endpoints
# [[security.apikey_rate_limit.match]]
# host = "/.*-aiplatform\\.googleapis\\.com/"
#
# [[security.apikey_rate_limit.match]]
# path = "/\\/v1\\/projects\\/.*\\/(endpoints|publishers|models)\\//"
```

**Why these defaults:** API-key rate limiting is disabled by default because not all deployments use API keys. When enabled, the 200 req/min limit is more generous than the IP limit — API keys represent known, provisioned clients that are expected to generate higher traffic than anonymous users. `include_ip = false` because a single API key is typically used by one integration; if you share keys across teams, see the [Shared API Keys](#scenario-4-shared-api-keys-across-teams) scenario. The `key_header` is pre-set to `x-goog-api-key` for Google Cloud / Vertex AI environments, the primary deployment target.

| Field | Type | Default | ENV Variable | Description |
|-------|------|---------|-------------|-------------|
| `enabled` | bool | `false` | `PROXY_SECURITY_APIKEY_RATE_LIMIT_ENABLED` | Enable per-API-key rate limiting |
| `requests_per_min` | int | `60` | `PROXY_SECURITY_APIKEY_RATE_LIMIT_REQUESTS_PER_MIN` | Max requests per key per minute |
| `ban_for_min` | int | `5` | `PROXY_SECURITY_APIKEY_RATE_LIMIT_BAN_FOR_MIN` | Ban duration (minutes) |
| `include_ip` | bool | `false` | `PROXY_SECURITY_APIKEY_RATE_LIMIT_INCLUDE_IP` | Prefix rate-limit key with client IP |
| `key_header` | string | `"x-goog-api-key"` | `PROXY_SECURITY_APIKEY_RATE_LIMIT_KEY_HEADER` | Header to extract API key from |
| `throttle_delay_ms` | int | `0` | `PROXY_SECURITY_APIKEY_RATE_LIMIT_THROTTLE_DELAY_MS` | Delay before 429 response (ms) |
| `max_delay_slots` | int | `100` | `PROXY_SECURITY_APIKEY_RATE_LIMIT_MAX_DELAY_SLOTS` | Max concurrent throttled responses |

#### Request Matching

The `[[security.apikey_rate_limit.match]]` blocks define which requests the API-key limiter applies to. All non-empty fields in a match block must match for the rule to apply. If no match blocks are defined, the limiter applies to all requests carrying the `key_header`.

| Field | Type | Description |
|-------|------|-------------|
| `host` | string | Host pattern -- exact string or `/regex/` |
| `path` | string | Path pattern -- exact string or `/regex/` |
| `header` | string | Header name that must be present |

### Per-JWT Rate Limiting

```toml
[security.jwt_rate_limit]
enabled = false                      # Opt-in — activate when you need per-user rate limiting
requests_per_min = 200               # Generous for authenticated users who have proven identity
ban_for_min = 5                      # Standard ban duration
include_ip = true                    # If a JWT is compromised or shared, this limits the blast
                                     # radius by creating separate buckets per IP+user pair
# throttle_delay_ms = 0             # Disabled by default
# max_delay_slots = 100             # Only relevant when throttle_delay_ms > 0
```

**Why these defaults:** JWT rate limiting is disabled by default because it requires JWT auth to be configured first. The 200 req/min limit is generous because authenticated users have proven their identity and are expected to be legitimate. `include_ip = true` (unlike the API-key limiter) because JWTs represent individual users — if a token is stolen or shared, the IP prefix ensures the attacker's traffic is isolated from the real user's bucket. This also means a legitimate user who switches networks mid-session simply gets a fresh bucket, which is acceptable.

| Field | Type | Default | ENV Variable | Description |
|-------|------|---------|-------------|-------------|
| `enabled` | bool | `false` | `PROXY_SECURITY_JWT_RATE_LIMIT_ENABLED` | Enable per-JWT rate limiting |
| `requests_per_min` | int | `60` | `PROXY_SECURITY_JWT_RATE_LIMIT_REQUESTS_PER_MIN` | Max requests per JWT `sub` per minute |
| `ban_for_min` | int | `5` | `PROXY_SECURITY_JWT_RATE_LIMIT_BAN_FOR_MIN` | Ban duration (minutes) |
| `include_ip` | bool | `false` | `PROXY_SECURITY_JWT_RATE_LIMIT_INCLUDE_IP` | Prefix rate-limit key with client IP |
| `throttle_delay_ms` | int | `0` | `PROXY_SECURITY_JWT_RATE_LIMIT_THROTTLE_DELAY_MS` | Delay before 429 response (ms) |
| `max_delay_slots` | int | `100` | `PROXY_SECURITY_JWT_RATE_LIMIT_MAX_DELAY_SLOTS` | Max concurrent throttled responses |

## How It Works

### Sliding Window

Each limiter uses a per-minute sliding window counter keyed by the identity (IP, API key, or JWT subject). When the counter exceeds `requests_per_min`, the client is **banned** for `ban_for_min` minutes. During a ban, every request from that identity receives an HTTP `429 Too Many Requests` response with a `Retry-After` header.

### Throttle Delay (ShockGuard)

When `throttle_delay_ms > 0`, rate-limited responses are **delayed** instead of being returned immediately. This slows down aggressive callers and smooths traffic spikes.

To prevent the delay mechanism itself from becoming a resource exhaustion vector, `max_delay_slots` caps the number of concurrent throttled goroutines. Once all slots are occupied, additional rate-limited requests receive an immediate 429 with no delay.

```
Request exceeds limit
  |-- throttle_delay_ms = 0?  -> immediate 429
  |-- delay slots available?  -> sleep(throttle_delay_ms), then 429
  |-- no slots available?     -> immediate 429
```

### Identity Prefixing with `include_ip`

For the API-key and JWT limiters, setting `include_ip = true` creates composite keys like `192.168.1.1:api-key-value`. This means the same API key used from two different IPs gets two separate rate limit buckets -- useful when keys are shared across clients.

## Rate-Limit-Only Mode

When both `auth.jwt.enabled` and `auth.api_key.enabled` are `false`, the proxy operates in **rate-limit-only mode**: all requests are forwarded upstream without credential checks, but rate limiting still applies. This is useful when you only need DDoS protection without authentication.

```toml
[auth.jwt]
enabled = false

[auth.api_key]
enabled = false

[security.rate_limit]
enabled = true
requests_per_min = 60
ban_for_min = 5
```

## Corporate NAT and Shared-IP Scenarios

### The Problem

When multiple users within a corporate network route traffic through a shared outbound IP (NAT gateway), all their requests appear to come from the same IP address. With only IP-based rate limiting, the combined traffic from all corporate users can easily exhaust the IP limit, causing legitimate users to be blocked even though no individual is abusing the service.

### The Solution: `skip_if_jwt_identified`

Setting `skip_if_jwt_identified = true` (the default) tells the IP rate limiter to stand down when the request carries a valid JWT `sub` claim. Those requests are instead governed exclusively by the JWT rate limiter, which tracks each user individually.

```
Request with JWT Bearer token
  |-- JwtRateLimit: extract sub -> enforce per-user limit
  |-- IpRateLimit:  JWT found -> skip (user already governed above)

Request without JWT (anonymous traffic)
  |-- JwtRateLimit: no Bearer token -> passthrough
  |-- IpRateLimit:  no JWT flag -> enforce per-IP limit (DDoS protection intact)
```

This means:
- **Corporate users with JWTs** -- each user has their own bucket; the shared IP is irrelevant.
- **Anonymous/unknown traffic** -- still subject to the IP limit; DDoS protection remains fully active.
- **Attacker with a stolen JWT** -- capped by the JWT rate limit, not elevated by the skip behavior.

### Configuration for Corporate + Anonymous Traffic

```toml
[security.rate_limit]
enabled = true
requests_per_min = 30          # Tight limit for anonymous/unknown IPs
skip_if_jwt_identified = true  # JWT users bypass this limit (default)
ban_for_min = 5

[security.jwt_rate_limit]
enabled = true
requests_per_min = 120         # Generous per-user limit for authenticated corporate users
ban_for_min = 5
```

With this setup:
- An unauthenticated IP is capped at 30 req/min.
- An authenticated user can make 120 req/min regardless of how many colleagues share their corporate IP.

### When to Disable `skip_if_jwt_identified`

Set `skip_if_jwt_identified = false` if you need to enforce the IP limit unconditionally -- for example, when you want an absolute ceiling on traffic per IP regardless of authentication, or when operating in rate-limit-only mode (no JWT auth).

```toml
[security.rate_limit]
enabled = true
requests_per_min = 200
skip_if_jwt_identified = false  # Enforce IP ceiling even for authenticated users
```

## Dynamic Rules via Admin API

When the [Admin Control Plane](ADMIN.md) is enabled, you can adjust rate limiting at runtime without redeploying:

```bash
# Throttle a backend to 50 RPM for 10 minutes
curl -X POST https://your-proxy.run.app/admin/control \
  -H "Authorization: Bearer $(gcloud auth print-identity-token)" \
  -H "Content-Type: application/json" \
  -d '{
    "command": "set-rule",
    "rule": {
      "ruleId": "throttle-my-api",
      "targetHost": "my-api.run.app",
      "action": "throttle",
      "maxRPM": 50,
      "durationSeconds": 600
    }
  }'
```

To target a specific limiter layer, add `"limiter"` to the rule (`"ip"`, `"apikey"`, or `"jwt"`). You can also update `throttleDelayMs` and `maxDelaySlots` for the targeted limiter at runtime:

```bash
curl -X POST .../admin/control \
  -H "Authorization: Bearer $(gcloud auth print-identity-token)" \
  -H "Content-Type: application/json" \
  -d '{
    "command": "set-rule",
    "rule": {
      "ruleId": "throttle-apikey",
      "targetHost": "my-api.run.app",
      "action": "throttle",
      "limiter": "apikey",
      "maxRPM": 30,
      "throttleDelayMs": 200,
      "maxDelaySlots": 50,
      "durationSeconds": 300
    }
  }'
```

Supported actions: `throttle` (cap RPM), `block` (drop all), `allow` (bypass per-IP limit).

### Rule Persistence

Active throttle rules can survive Cloud Run instance restarts via the `PROXY_THROTTLE_RULES` environment variable. See the [Admin Control Plane Guide](ADMIN.md#startup-rule-persistence) for details.

## Scenarios

The following scenarios show how to adjust the shipped defaults for common deployment patterns. Each scenario explains the situation, the config changes, and why those values make sense.

### Scenario 1: Basic DDoS Protection (Defaults)

**Situation:** You want basic protection against abusive IPs without any authentication. This is what the shipped `config.toml` gives you out of the box for unauthenticated traffic.

```toml
[security.rate_limit]
enabled = true
requests_per_min = 60
ban_for_min = 5
skip_if_jwt_identified = true
```

**What this does:** Any single IP that sends more than 60 requests in a minute gets banned for 5 minutes. If JWT auth is also enabled, authenticated users bypass this limit entirely (governed by the JWT limiter instead).

### Scenario 2: Enabling ShockGuard for Gradual Backoff

**Situation:** Instead of returning an immediate 429 when a client exceeds the limit, you want to slow them down first. This is useful when clients don't handle 429s well, or you want to smooth traffic spikes rather than create a hard cliff.

**Change from defaults:** Uncomment and set `throttle_delay_ms` and `max_delay_slots`.

```toml
[security.rate_limit]
enabled = true
requests_per_min = 60
ban_for_min = 5
throttle_delay_ms = 500            # Hold rate-limited responses for 500ms before returning 429
max_delay_slots = 50               # Cap at 50 concurrent delayed responses to prevent resource exhaustion
```

**Why 500ms / 50 slots:** A 500ms delay is long enough to meaningfully slow a misbehaving client, but short enough that the connection doesn't time out. 50 slots means at most 50 goroutines are sleeping at any time -- if an attacker floods the proxy, the 51st request gets an immediate 429 so the delay mechanism can't be weaponized as a resource drain.

### Scenario 3: API-Key Rate Limiting for Vertex AI

**Situation:** You're proxying Vertex AI endpoints and want to prevent any single API key from monopolizing your quota. You need to match only Vertex AI traffic, not all requests.

**Change from defaults:** Enable the API-key limiter and add match rules.

```toml
[security.apikey_rate_limit]
enabled = true
requests_per_min = 200
ban_for_min = 5
key_header = "x-goog-api-key"

[[security.apikey_rate_limit.match]]
host = "/.*-aiplatform\\.googleapis\\.com/"

[[security.apikey_rate_limit.match]]
path = "/\\/v1\\/projects\\/.*\\/(endpoints|publishers|models)\\//"
```

**What this does:** Only requests whose host matches the Vertex AI pattern or whose path matches the Vertex AI resource pattern are subject to API-key rate limiting. Other traffic passes through untouched by this limiter.

### Scenario 4: Shared API Keys Across Teams

**Situation:** Multiple teams or clients share the same API key (e.g., a dev key used by several engineers). Without `include_ip`, all of them share one rate limit bucket, so one team's spike blocks the others.

**Change from defaults:** Set `include_ip = true` so each IP+key pair gets its own bucket.

```toml
[security.apikey_rate_limit]
enabled = true
requests_per_min = 200
ban_for_min = 5
include_ip = true                  # Each IP + key pair gets a separate rate limit bucket
key_header = "x-goog-api-key"
```

**What this does:** The rate limit key becomes `<client-ip>:<api-key>` instead of just `<api-key>`. Engineer A and Engineer B using the same key from different machines get independent 200 req/min allowances.

### Scenario 5: Per-User JWT Rate Limiting

**Situation:** You have authenticated users and want to enforce per-user limits so that no single user can monopolize your backend, even if they're technically legitimate.

**Change from defaults:** Enable the JWT limiter alongside the existing IP limiter.

```toml
[security.rate_limit]
enabled = true
requests_per_min = 60
skip_if_jwt_identified = true      # Authenticated users are governed by JWT limiter below

[security.jwt_rate_limit]
enabled = true
requests_per_min = 200             # Authenticated users get a generous per-user limit
ban_for_min = 5
include_ip = true                  # Isolate traffic per user+IP pair (limits stolen token blast radius)
```

**What this does:** Anonymous traffic is capped at 60 req/min per IP. Authenticated users each get 200 req/min, tracked per user+IP pair. If a token is stolen, the attacker's traffic is isolated to their IP and doesn't affect the real user.

### Scenario 6: Corporate Users Behind NAT with Anonymous Traffic

**Situation:** Your users are behind a corporate NAT, so hundreds of employees share one outbound IP. You also receive anonymous traffic from the internet that needs DDoS protection. See the [Corporate NAT section](#corporate-nat-and-shared-ip-scenarios) for a detailed explanation.

**Change from defaults:** Lower the anonymous IP limit, enable a generous JWT limit.

```toml
[security.rate_limit]
enabled = true
requests_per_min = 30              # Tight limit for anonymous/unknown IPs
skip_if_jwt_identified = true      # Corporate users with JWTs bypass this
ban_for_min = 5

[security.jwt_rate_limit]
enabled = true
requests_per_min = 120             # Per-user limit for authenticated corporate users
ban_for_min = 5
```

### Scenario 7: Strict IP Ceiling Regardless of Auth

**Situation:** You want an absolute hard cap on traffic per IP, even for authenticated users. This is useful when your infrastructure has a per-IP capacity limit that must be enforced regardless of who the user is.

**Change from defaults:** Disable `skip_if_jwt_identified`.

```toml
[security.rate_limit]
enabled = true
requests_per_min = 200
skip_if_jwt_identified = false     # Enforce IP ceiling unconditionally
ban_for_min = 5

[security.jwt_rate_limit]
enabled = true
requests_per_min = 100             # Per-user limit still applies on top of the IP limit
ban_for_min = 5
```

**What this does:** Every request hits the IP limiter (200 req/min per IP) AND the JWT limiter (100 req/min per user). A single user can't exceed 100 req/min, and a single IP can't exceed 200 req/min even if multiple authenticated users share it.

### Scenario 8: Multi-Layer Defense (All Limiters)

**Situation:** You want defense in depth -- IP-level DDoS protection, API-key abuse prevention, and per-user fairness limits all at once.

```toml
[security.rate_limit]
enabled = true
requests_per_min = 60
ban_for_min = 5
skip_if_jwt_identified = true

[security.apikey_rate_limit]
enabled = true
requests_per_min = 200
ban_for_min = 5
key_header = "x-goog-api-key"

[[security.apikey_rate_limit.match]]
host = "/.*-aiplatform\\.googleapis\\.com/"

[security.jwt_rate_limit]
enabled = true
requests_per_min = 200
ban_for_min = 5
include_ip = true
```

**Evaluation order:** API-key -> JWT -> IP. A request rejected by the API-key limiter never reaches the JWT or IP limiters. This avoids double-counting and ensures the most specific identity check runs first.

### Scenario 9: Rate-Limit-Only Mode (No Auth)

**Situation:** You don't need authentication at all -- you just want DDoS protection in front of an internal service. See [Rate-Limit-Only Mode](#rate-limit-only-mode) for details.

**Change from defaults:** Disable both auth methods, keep rate limiting on.

```toml
[auth.jwt]
enabled = false

[auth.api_key]
enabled = false

[security.rate_limit]
enabled = true
requests_per_min = 100             # Adjust to your service's capacity
ban_for_min = 10                   # Longer ban to discourage retry floods
skip_if_jwt_identified = false     # No JWT auth, so this flag has no effect — set false for clarity
```

## See Also

- [Configuration Guide](CONFIGURATION.md) -- Full configuration reference
- [Admin Control Plane](ADMIN.md) -- Runtime rule management, serverless caveats
- [API Documentation](API.md) -- HTTP endpoints and error responses
- [Environment Variables Guide](ENVIRONMENT.md) -- All env var overrides
