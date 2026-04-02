# Rate Limiting Guide

lite-auth-proxy provides a unified, multi-layer rate limiting system that protects your backend from abuse. Each layer targets a different identity dimension and can be enabled independently.

## Rate Limiter Layers

| Layer | Config Section | Key Identity | Use Case |
|-------|---------------|-------------|----------|
| **Per-IP** | `[security.rate_limit]` | Client IP address | General DDoS protection |
| **Per-API-Key** | `[security.apikey_rate_limit]` | API key header value | Key-level abuse prevention |
| **Per-JWT** | `[security.jwt_rate_limit]` | JWT `sub` claim | User-level rate limiting |

All three layers share the same configuration knobs and behavior model. They are evaluated in order: API-key → JWT → IP. A request rejected by an earlier layer never reaches later ones.

## Configuration Reference

### Per-IP Rate Limiting

```toml
[security.rate_limit]
enabled = true
requests_per_min = 60
ban_for_min = 5
throttle_delay_ms = 0
max_delay_slots = 100
```

| Field | Type | Default | ENV Variable | Description |
|-------|------|---------|-------------|-------------|
| `enabled` | bool | `false` | `PROXY_SECURITY_RATE_LIMIT_ENABLED` | Enable per-IP rate limiting |
| `requests_per_min` | int | `60` | `PROXY_SECURITY_RATE_LIMIT_REQUESTS_PER_MIN` | Max requests per IP per minute |
| `ban_for_min` | int | `5` | `PROXY_SECURITY_RATE_LIMIT_BAN_FOR_MIN` | Ban duration after limit exceeded (minutes) |
| `throttle_delay_ms` | int | `0` | `PROXY_SECURITY_RATE_LIMIT_THROTTLE_DELAY_MS` | Delay before 429 response (ms); `0` = disabled |
| `max_delay_slots` | int | `100` | `PROXY_SECURITY_RATE_LIMIT_MAX_DELAY_SLOTS` | Max concurrent throttled responses (DDoS cap) |

### Per-API-Key Rate Limiting

```toml
[security.apikey_rate_limit]
enabled = true
requests_per_min = 60
ban_for_min = 5
include_ip = false
key_header = "x-goog-api-key"
throttle_delay_ms = 0
max_delay_slots = 100

[[security.apikey_rate_limit.match]]
host = "my-api.example.com"
path = "/v1/predict"
header = "x-goog-api-key"
```

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
| `host` | string | Host pattern — exact string or `/regex/` |
| `path` | string | Path pattern — exact string or `/regex/` |
| `header` | string | Header name that must be present |

### Per-JWT Rate Limiting

```toml
[security.jwt_rate_limit]
enabled = true
requests_per_min = 60
ban_for_min = 5
include_ip = false
throttle_delay_ms = 0
max_delay_slots = 100
```

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
  └─ throttle_delay_ms = 0?  → immediate 429
  └─ delay slots available?  → sleep(throttle_delay_ms), then 429
  └─ no slots available?     → immediate 429
```

### Identity Prefixing with `include_ip`

For the API-key and JWT limiters, setting `include_ip = true` creates composite keys like `192.168.1.1:api-key-value`. This means the same API key used from two different IPs gets two separate rate limit buckets — useful when keys are shared across clients.

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

## Dynamic Rules via Admin API

When the [Admin Control API](API.md#admin-endpoints) is enabled, you can adjust rate limiting at runtime without redeploying:

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

Active throttle rules can survive Cloud Run instance restarts via the `PROXY_THROTTLE_RULES` environment variable. See the [Configuration Guide](CONFIGURATION.md#startup-rule-persistence) for details.

## Examples

### Basic DDoS Protection

```toml
[security.rate_limit]
enabled = true
requests_per_min = 100
ban_for_min = 10
throttle_delay_ms = 500
max_delay_slots = 50
```

### API-Key Rate Limiting for Vertex AI Proxy

```toml
[security.apikey_rate_limit]
enabled = true
requests_per_min = 200
ban_for_min = 5
key_header = "x-goog-api-key"
throttle_delay_ms = 200
max_delay_slots = 100

[[security.apikey_rate_limit.match]]
host = "/.*-aiplatform\\.googleapis\\.com$/"
path = "/v1/projects/"
header = "x-goog-api-key"
```

### Combined Per-User and Per-IP Limiting

```toml
[security.rate_limit]
enabled = true
requests_per_min = 60
ban_for_min = 5

[security.jwt_rate_limit]
enabled = true
requests_per_min = 120
ban_for_min = 2
include_ip = true
```

## See Also

- [Configuration Guide](CONFIGURATION.md) — Full configuration reference
- [API Documentation](API.md) — Admin endpoints for runtime rule management
- [Environment Variables Guide](ENVIRONMENT.md) — All env var overrides
