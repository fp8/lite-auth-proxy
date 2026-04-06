# Configuration Guide

This document provides a comprehensive reference for all configuration options in lite-auth-proxy.

## Configuration File Format

lite-auth-proxy uses TOML format for its configuration files. The default configuration files are `config/config-flex.toml` (flex build) and `config/config-lite.toml` (lite build). You can specify a custom location using the `-config` flag:

```bash
./bin/flex-auth-proxy -config /path/to/custom-config.toml
# or
./bin/lite-auth-proxy -config /path/to/custom-config.toml
```

## Configuration Structure

The configuration is organized into these top-level sections:

- **[server]** — HTTP server and proxy settings (core — always available)
- **[security]** — Rate limiting (requires `ratelimit` plugin)
- **[auth.jwt]** — JWT authentication (core — always available)
- **[auth.api_key]** — API-Key authentication (requires `apikey` plugin)
- **[admin]** — Dynamic control-plane API (requires `admin` plugin)
- **[storage]** — Persistent storage backend (requires a `storage-*` plugin)

Sections marked as requiring a plugin are only available in builds that include the plugin. The flex build (`flex-auth-proxy`) includes all plugins. The lite build (`lite-auth-proxy`) includes only core sections. See the [Plugin Guide](PLUGINS.md) for details.

> **Config validation:** If a plugin-gated section is enabled but the required plugin is not compiled in, the proxy fails at startup with a clear error message naming the missing plugin and the import path to add.

## Server Configuration

### Basic Server Settings

```toml
[server]
port = 8888
target_url = "http://localhost:8080"
strip_prefix = ""
shutdown_timeout_secs = 10
```

| Field | Type | Default | ENV Variable | Description |
|-------|------|---------|---|-------------|
| `port` | integer | `8888` | `PROXY_SERVER_PORT` | HTTP listening port for the proxy server |
| `target_url` | string | **required** | `PROXY_SERVER_TARGET_URL` | Downstream service URL to proxy requests to |
| `strip_prefix` | string | `""` | `PROXY_SERVER_STRIP_PREFIX` | URL prefix to remove from request path before forwarding (e.g., `/api`) |
| `shutdown_timeout_secs` | integer | `10` | `PROXY_SERVER_SHUTDOWN_TIMEOUT_SECS` | Graceful shutdown timeout in seconds |

### Path Filtering

```toml
[server]
include_paths = ["/*"]
exclude_paths = ["/healthz", "/metrics"]
```

| Field | Type | Default | ENV Variable | Description |
|-------|------|---------|---|-------------|
| `include_paths` | array[string] | `["/*"]` | `PROXY_SERVER_INCLUDE_PATHS` | Glob patterns for paths requiring authentication |
| `exclude_paths` | array[string] | `[]` | `PROXY_SERVER_EXCLUDE_PATHS` | Paths that bypass authentication (useful for health checks) |

**Path Matching Rules:**
- Glob patterns are supported: `*` (any characters), `**` (recursive)
- Paths in `exclude_paths` take precedence over `include_paths`
- Exact matches are checked first, then glob patterns

### Health Check Configuration

```toml
[server.health_check]
path = "/healthz"
target = ""
```

| Field | Type | Default | ENV Variable | Description |
|-------|------|---------|---|-------------|
| `path` | string | `"/healthz"` | `PROXY_SERVER_HEALTH_CHECK_PATH` | Health check endpoint path |
| `target` | string | `""` | `PROXY_SERVER_HEALTH_CHECK_TARGET` | Optional: proxy health checks to downstream service |

**Health Check Modes:**
- **Local mode** (target = ""): Returns `{"status":"ok"}` with 200 OK
- **Proxy mode** (target set): Forwards health check to downstream and returns its response

**CLI Health Check:**
- Use `-healthcheck` to run the configured health check and exit.
- When `server.health_check.target` is set (or `PROXY_SERVER_HEALTH_CHECK_TARGET`), the command performs an HTTP GET to the target and returns non-zero if it fails.
- When the target is empty, the command exits successfully.

## Security Configuration

### Body Size Limit

```toml
[security]
max_body_bytes = 1048576             # 1 MiB default; 0 = no limit
```

| Field | Type | Default | ENV Variable | Description |
|-------|------|---------|---|-------------|
| `max_body_bytes` | integer | `1048576` (1 MiB) | `PROXY_SECURITY_MAX_BODY_BYTES` | Max request body size in bytes. Requests exceeding this limit receive a 413 response. `0` disables the limit. |

### Rate Limiting

> **Plugin required:** The `[security.rate_limit]`, `[security.apikey_rate_limit]`, and `[security.jwt_rate_limit]` sections require the `ratelimit` plugin. The lite build does not include this plugin. See the [Rate Limiter Plugin](PLUGINS.md#rate-limiter-plugin) for the full configuration reference.

lite-auth-proxy provides three independent rate limiter layers: per-IP, per-API-key, and per-JWT. For detailed scenarios and tuning, see the [Rate Limiting Guide](RATE-LIMITING.md).

## Authentication Configuration

### Common Authentication Settings

```toml
[auth]
header_prefix = "X-AUTH-"
```

| Field | Type | Default | ENV Variable | Description |
|-------|------|---------|---|-------------|
| `header_prefix` | string | `"X-AUTH-"` | `PROXY_AUTH_HEADER_PREFIX` | Prefix for injected authentication headers |

### JWT Authentication

**Minimum configuration** (`issuer` and `audience` are the only required fields):

```toml
[auth.jwt]
enabled  = true
issuer   = "https://securetoken.google.com/my-project"
audience = "my-project"
```

All available fields:

```toml
[auth.jwt]
enabled        = true
issuer         = "https://securetoken.google.com/{{ENV.GOOGLE_CLOUD_PROJECT}}"
audience       = "{{ENV.GOOGLE_CLOUD_PROJECT}}"
tolerance_secs = 30
cache_ttl_mins = 1440
allowed_emails = []   # optional — empty means no email restriction
```

| Field | Type | Default | ENV Variable | Description |
|-------|------|---------|---|-------------|
| `enabled` | boolean | `false` | `PROXY_AUTH_JWT_ENABLED` | Enable JWT authentication |
| `issuer` | string | **required if enabled** | `PROXY_AUTH_JWT_ISSUER` | Expected JWT issuer (OpenID Connect issuer URL) |
| `audience` | string | **required if enabled** | `PROXY_AUTH_JWT_AUDIENCE` | Expected JWT audience claim |
| `tolerance_secs` | integer | `30` | `PROXY_AUTH_JWT_TOLERANCE_SECS` | Clock skew tolerance for `exp` and `nbf` validation (seconds) |
| `cache_ttl_mins` | integer | `1440` | `PROXY_AUTH_JWT_CACHE_TTL_MINS` | JWKS public key cache TTL (minutes, default 24 hours) |
| `allowed_emails` | array[string] | `[]` | `PROXY_AUTH_JWT_ALLOWED_EMAILS` | Allowlist of email addresses; empty means no restriction (any valid token passes) |

**JWT Validation Process:**
1. Extract JWT from `Authorization: Bearer <token>` header
2. Decode JWT header to get `kid` (key ID)
3. Fetch JWKS from issuer's `.well-known/openid-configuration` endpoint
4. Validate JWT signature using public key
5. Verify `exp`, `nbf`, `iss`, and `aud` claims
6. Apply claim filters (if configured)
7. Map claims to HTTP headers (if configured)

#### JWT Claim Filters

Filters enforce access control based on JWT claims:

```toml
[auth.jwt.filters]
email_verified = "true"
email = "/.*@example\\.com$/"
role = "admin"
```

| Filter Name | Example Value | Filter Type | ENV Variable | Description |
|-------------|---|---|---|-------------|
| `email_verified` | `"true"` | Exact match | `PROXY_AUTH_JWT_FILTERS_EMAIL_VERIFIED=true` | Claim must exactly match the specified value |
| `email` | `"/.*@example\\.com$/"` | Regex match | `PROXY_AUTH_JWT_FILTERS_EMAIL=/.*@example\\.com$/` | Claim must match the regex pattern |
| `role` | `"admin"` or array | Exact/Array | `PROXY_AUTH_JWT_FILTERS_ROLE=admin` | Array claims pass if **any** element matches (OR logic) |

**Filter Behavior:**
- All filters must pass (AND logic between filters)
- For array claims, only one element needs to match (OR logic within array)
- Missing claims cause authentication failure
- Type coercion: numbers converted to strings for comparison

#### Firebase Auth vs Google ID Token

The proxy can be configured to validate two distinct JWT token types, each with a different claim structure. Understanding the difference is critical for correctly setting `issuer`, `audience`, and claim filters.

##### Firebase Authentication Token

Issued by Firebase Auth when a user signs in via email/password, social login, or other Firebase providers. **Does not carry an `hd` (hosted domain) claim** — filter by `email` regex instead.

Example claims:
```json
{
  "iss": "https://securetoken.google.com/my-project",
  "aud": "my-project",
  "sub": "lcbPYEMgbeQcws7Qtl1X225mI0i2",
  "email": "user@example.com",
  "email_verified": false,
  "firebase": {
    "identities": { "email": ["user@example.com"] },
    "sign_in_provider": "password"
  }
}
```

Configuration:
```toml
[auth.jwt]
enabled  = true
issuer   = "https://securetoken.google.com/{{ENV.GOOGLE_CLOUD_PROJECT}}"
audience = "{{ENV.GOOGLE_CLOUD_PROJECT}}"

[auth.jwt.filters]
email_verified = "true"
email          = "/.*@example\\.com$/"   # regex — hd claim does not exist in Firebase tokens
```

##### Google ID Token

Issued by Google OAuth2 when a user authenticates against a Google account. Google Workspace accounts include an `hd` (hosted domain) claim. Use `hd` filtering instead of email regex when all users belong to the same Google Workspace domain.

Example claims:
```json
{
  "iss": "https://accounts.google.com",
  "aud": "32555940559.apps.googleusercontent.com",
  "sub": "114789851119851077143",
  "hd": "example.com",
  "email": "user@example.com",
  "email_verified": true
}
```

Configuration:
```toml
[auth.jwt]
enabled  = true
issuer   = "https://accounts.google.com"
audience = "32555940559.apps.googleusercontent.com"   # OAuth2 client ID

[auth.jwt.filters]
email_verified = "true"
hd             = "example.com"   # hosted domain claim — only present in Google ID tokens
```

> **Key distinction:** Use `hd` filtering only for Google ID tokens. For Firebase tokens, always filter by `email` regex — the `hd` claim will never be present and the request will always fail the filter if `hd` is set.

#### JWT Claim Mappings

Mappings transform JWT claims into HTTP headers forwarded to downstream:

```toml
[auth.jwt.mappings]
email = "USER-EMAIL"
sub = "USER-ID"
roles = "USER-ROLES"
org = "USER-ORG"
```

| Claim Name | Header Suffix | Result Header | ENV Variable | Description |
|------------|---|---|---|-------------|
| `email` | `"USER-EMAIL"` | `X-AUTH-USER-EMAIL` | `PROXY_AUTH_JWT_MAPPINGS_EMAIL=USER-EMAIL` | Mapped claim value `user@example.com` |
| `sub` | `"USER-ID"` | `X-AUTH-USER-ID` | `PROXY_AUTH_JWT_MAPPINGS_SUB=USER-ID` | Mapped claim value (subject ID) |
| `roles` | `"USER-ROLES"` | `X-AUTH-USER-ROLES` | `PROXY_AUTH_JWT_MAPPINGS_ROLES=USER-ROLES` | Comma-separated array values |
| `org` | `"USER-ORG"` | `X-AUTH-USER-ORG` | `PROXY_AUTH_JWT_MAPPINGS_ORG=USER-ORG` | Mapped claim value (organization) |

**Mapping Rules:**
- Header name format: `{header_prefix}{UPPER(mapping_value)}`
- Example: `email = "USER-EMAIL"` → `X-AUTH-USER-EMAIL: user@example.com`

**Type Coercion for Mapped Claims:**
- String/Number → String value
- Array → Comma-separated values (CSV)
- Object → JSON string
- Missing claim → Silently skipped (no error)

### API-Key Authentication

> **Plugin required:** The `[auth.api_key]` section requires the `apikey` plugin. The lite build does not include this plugin. See the [API-Key Plugin](PLUGINS.md#api-key-authentication-plugin) for the full configuration reference.

API-Key authentication is independent of JWT (not a fallback). When the configured header is present, the key is validated via constant-time comparison. On success, static payload headers are injected into the request.

## Environment Variable Substitution

Configuration values can reference environment variables using `{{ENV.VARIABLE_NAME}}` syntax:

```toml
issuer = "https://securetoken.google.com/{{ENV.GOOGLE_CLOUD_PROJECT}}"
audience = "{{ENV.GOOGLE_CLOUD_PROJECT}}"
value = "{{ENV.API_KEY_SECRET}}"
```

**Substitution Behavior:**
- Performed at configuration load time
- If environment variable is not set, the placeholder remains unchanged
- Maximum variable name length: 100 characters
- Variable name must match pattern: `[A-Z_][A-Z0-9_]*`

## Environment Variable Overrides

All configuration values can be overridden using environment variables with the `PROXY_` prefix:

### Server Overrides

| Environment Variable | Config Field | Type |
|---------------------|--------------|------|
| `PROXY_SERVER_PORT` | `server.port` | integer |
| `PROXY_SERVER_TARGET_URL` | `server.target_url` | string |
| `PROXY_SERVER_STRIP_PREFIX` | `server.strip_prefix` | string |
| `PROXY_SERVER_SHUTDOWN_TIMEOUT_SECS` | `server.shutdown_timeout_secs` | integer |
| `PROXY_SERVER_HEALTH_CHECK_PATH` | `server.health_check.path` | string |
| `PROXY_SERVER_HEALTH_CHECK_TARGET` | `server.health_check.target` | string |

### Security Overrides

| Environment Variable | Config Field | Type |
|---------------------|--------------|------|
| `PROXY_SECURITY_MAX_BODY_BYTES` | `security.max_body_bytes` | integer |
| `PROXY_SECURITY_RATE_LIMIT_ENABLED` | `security.rate_limit.enabled` | boolean |
| `PROXY_SECURITY_RATE_LIMIT_REQUESTS_PER_MIN` | `security.rate_limit.requests_per_min` | integer |
| `PROXY_SECURITY_RATE_LIMIT_BAN_FOR_MIN` | `security.rate_limit.ban_for_min` | integer |
| `PROXY_SECURITY_RATE_LIMIT_SKIP_IF_JWT_IDENTIFIED` | `security.rate_limit.skip_if_jwt_identified` | boolean |
| `PROXY_SECURITY_RATE_LIMIT_THROTTLE_DELAY_MS` | `security.rate_limit.throttle_delay_ms` | integer |
| `PROXY_SECURITY_RATE_LIMIT_MAX_DELAY_SLOTS` | `security.rate_limit.max_delay_slots` | integer |
| `PROXY_SECURITY_APIKEY_RATE_LIMIT_ENABLED` | `security.apikey_rate_limit.enabled` | boolean |
| `PROXY_SECURITY_APIKEY_RATE_LIMIT_REQUESTS_PER_MIN` | `security.apikey_rate_limit.requests_per_min` | integer |
| `PROXY_SECURITY_APIKEY_RATE_LIMIT_BAN_FOR_MIN` | `security.apikey_rate_limit.ban_for_min` | integer |
| `PROXY_SECURITY_APIKEY_RATE_LIMIT_INCLUDE_IP` | `security.apikey_rate_limit.include_ip` | boolean |
| `PROXY_SECURITY_APIKEY_RATE_LIMIT_KEY_HEADER` | `security.apikey_rate_limit.key_header` | string |
| `PROXY_SECURITY_APIKEY_RATE_LIMIT_THROTTLE_DELAY_MS` | `security.apikey_rate_limit.throttle_delay_ms` | integer |
| `PROXY_SECURITY_APIKEY_RATE_LIMIT_MAX_DELAY_SLOTS` | `security.apikey_rate_limit.max_delay_slots` | integer |
| `PROXY_SECURITY_JWT_RATE_LIMIT_ENABLED` | `security.jwt_rate_limit.enabled` | boolean |
| `PROXY_SECURITY_JWT_RATE_LIMIT_REQUESTS_PER_MIN` | `security.jwt_rate_limit.requests_per_min` | integer |
| `PROXY_SECURITY_JWT_RATE_LIMIT_BAN_FOR_MIN` | `security.jwt_rate_limit.ban_for_min` | integer |
| `PROXY_SECURITY_JWT_RATE_LIMIT_INCLUDE_IP` | `security.jwt_rate_limit.include_ip` | boolean |
| `PROXY_SECURITY_JWT_RATE_LIMIT_THROTTLE_DELAY_MS` | `security.jwt_rate_limit.throttle_delay_ms` | integer |
| `PROXY_SECURITY_JWT_RATE_LIMIT_MAX_DELAY_SLOTS` | `security.jwt_rate_limit.max_delay_slots` | integer |

### Admin Overrides

| Environment Variable | Config Field | Type |
|---------------------|--------------|------|
| `PROXY_ADMIN_ENABLED` | `admin.enabled` | boolean |
| `PROXY_ADMIN_JWT_ISSUER` | `admin.jwt.issuer` | string |
| `PROXY_ADMIN_JWT_AUDIENCE` | `admin.jwt.audience` | string |
| `PROXY_ADMIN_JWT_ALLOWED_EMAILS` | `admin.jwt.allowed_emails` | comma-separated string |

### Storage Overrides

| Environment Variable | Config Field | Type |
|---------------------|--------------|------|
| `PROXY_STORAGE_ENABLED` | `storage.enabled` | boolean |
| `PROXY_STORAGE_PROJECT_ID` | `storage.project_id` | string |
| `PROXY_STORAGE_DBNAME` | `storage.dbname` | string |
| `PROXY_STORAGE_COLLECTION_PREFIX` | `storage.collection_prefix` | string |

### Authentication Overrides

| Environment Variable | Config Field | Type |
|---------------------|--------------|------|
| `PROXY_AUTH_HEADER_PREFIX` | `auth.header_prefix` | string |
| `PROXY_AUTH_JWT_ENABLED` | `auth.jwt.enabled` | boolean |
| `PROXY_AUTH_JWT_ISSUER` | `auth.jwt.issuer` | string |
| `PROXY_AUTH_JWT_AUDIENCE` | `auth.jwt.audience` | string |
| `PROXY_AUTH_JWT_TOLERANCE_SECS` | `auth.jwt.tolerance_secs` | integer |
| `PROXY_AUTH_JWT_CACHE_TTL_MINS` | `auth.jwt.cache_ttl_mins` | integer |
| `PROXY_AUTH_API_KEY_ENABLED` | `auth.api_key.enabled` | boolean |
| `PROXY_AUTH_API_KEY_NAME` | `auth.api_key.name` | string |
| `PROXY_AUTH_API_KEY_VALUE` | `auth.api_key.value` | string |

## Default Configuration

The default configuration file (`config/config-flex.toml`) comes pre-configured for quick setup. Here's what's enabled by default:

### What's Included by Default

```toml
[server]
port = 8888                          # Listens on port 8888
target_url = "http://localhost:8080" # Proxies to localhost:8080
include_paths = ["/*"]               # All paths require authentication
exclude_paths = ["/healthz"]         # Health check bypasses auth

[security.rate_limit]
enabled = true                       # IP rate limiting is ON
requests_per_min = 60                # Max 60 requests per IP per minute
ban_for_min = 5                      # Ban duration is 5 minutes
skip_if_jwt_identified = true        # Authenticated JWT users bypass IP limiter

[auth.jwt]
enabled = true                       # JWT authentication is ON
issuer = "https://securetoken.google.com/{{ENV.GOOGLE_CLOUD_PROJECT}}"
audience = "{{ENV.GOOGLE_CLOUD_PROJECT}}"

[auth.api_key]
enabled = false                      # API-Key auth is OFF by default
```

### What's Disabled by Default

- **API-Key Authentication**: Disabled (`enabled = false`)
- **Per-API-Key Rate Limiting**: Disabled (`enabled = false`); pre-configured at 200 req/min with `x-goog-api-key` header when enabled
- **Per-JWT Rate Limiting**: Disabled (`enabled = false`); pre-configured at 200 req/min with `include_ip = true` when enabled
- **Throttle Delay (ShockGuard)**: Disabled on all limiters (`throttle_delay_ms = 0`); see [Rate Limiting Guide](RATE-LIMITING.md#scenario-2-enabling-shockguard-for-gradual-backoff) for setup
- **JWT Filters**: No filters configured (all JWT tokens accepted if valid)
- **JWT Mappings**: Basic mappings only (`sub` -> `USER-ID`, `email` -> `USER-EMAIL`)
- **Admin Control-Plane**: Disabled (`enabled = false`)

### Enabling API-Key Authentication

The easiest way to enable API-Key authentication is via environment variables:

```bash
# Method 1: Using environment variables (overrides config)
export PROXY_AUTH_API_KEY_ENABLED=true
export API_KEY_SECRET="your-secret-key-value"
./bin/flex-auth-proxy

# Method 2: Using TOML configuration
# Edit config/config-flex.toml:
[auth.api_key]
enabled = true
name = "X-API-KEY"
value = "your-secret-key-value"

[auth.api_key.payload]
service = "internal"
```

### Environment Variables for Quick Override

You don't need to modify the TOML file for common changes. Just set environment variables:

```bash
# Change proxy port
export PROXY_SERVER_PORT=9090

# Change target backend
export PROXY_SERVER_TARGET_URL=http://my-backend:8000

# Enable API-Key and set secret
export PROXY_AUTH_API_KEY_ENABLED=true
export API_KEY_SECRET=my-secret-key

# Disable rate limiting if not needed
export PROXY_SECURITY_RATE_LIMIT_ENABLED=false

# Add JWT filters
export PROXY_AUTH_JWT_FILTERS_EMAIL_VERIFIED=true
export PROXY_AUTH_JWT_FILTERS_EMAIL="/.*@company\\\\.com$/"

# Add JWT claim mappings
export PROXY_AUTH_JWT_MAPPINGS_ROLES=USER-ROLES
export PROXY_AUTH_JWT_MAPPINGS_ORG=USER-ORG

# Add API-Key payload headers
export PROXY_AUTH_API_KEY_PAYLOAD_SERVICE=internal
export PROXY_AUTH_API_KEY_PAYLOAD_TEAM=platform
```

## Configuration Examples

### Example 1: JWT-Only Authentication

```toml
[server]
port = 8888
target_url = "http://backend:8080"

[auth.jwt]
enabled = true
issuer = "https://accounts.google.com"
audience = "my-app-id"

[auth.jwt.filters]
email_verified = "true"
```

### Example 2: API-Key Authentication

```toml
[server]
port = 8888
target_url = "http://backend:8080"

[auth.api_key]
enabled = true
name = "X-API-KEY"
value = "{{ENV.API_KEY_SECRET}}"

[auth.api_key.payload]
service = "internal"
```

### Example 3: Dual Authentication with Rate Limiting

```toml
[server]
port = 8888
target_url = "http://backend:8080"
exclude_paths = ["/healthz", "/metrics"]

[security.rate_limit]
enabled = true
requests_per_min = 100
ban_for_min = 10

[auth.jwt]
enabled = true
issuer = "https://securetoken.google.com/{{ENV.GOOGLE_CLOUD_PROJECT}}"
audience = "{{ENV.GOOGLE_CLOUD_PROJECT}}"

[auth.api_key]
enabled = true
name = "X-API-KEY"
value = "{{ENV.API_KEY_SECRET}}"
```

---

## Admin Control-Plane

> **Plugin required:** The `[admin]` section requires the `admin` plugin. The lite build does not include this plugin. See the [Admin Plugin](PLUGINS.md#admin-plugin) for the full configuration reference.

The admin API enables runtime traffic control (throttle, block, allow) without redeploying. It is **disabled by default** and has zero overhead when off. For endpoints, rule lifecycle, and serverless caveats see the [Admin Control Plane Guide](ADMIN.md).

> **Rule persistence:** Without a storage plugin, admin rules are held in process memory and lost on restart. Use the [Firestore storage plugin](PLUGINS.md#storage-firestore-plugin) for persistent cross-instance rule sync, or `PROXY_THROTTLE_RULES` as a lightweight alternative.

---

## Storage

> **Plugin required:** The `[storage]` section requires a storage plugin (e.g. `storage-firestore`). See the [Storage Plugin](PLUGINS.md#storage-firestore-plugin) for the full configuration reference and GCP setup.

The storage backend provides persistent `RuleStore` and `KeyValueStore` implementations. When configured, admin rules survive process restarts and are synchronized across Cloud Run instances in real-time.

```toml
[storage]
backend = "firestore"              # "firestore" or "" (no storage)
project_id = ""                    # Defaults to GOOGLE_CLOUD_PROJECT
collection_prefix = "proxy"        # Firestore collection prefix
```

---

## Configuration Validation

The proxy validates configuration at startup and exits with an error if:

- Required fields are missing (e.g., `target_url` when no auth enabled)
- Field types are incorrect (e.g., string for integer field)
- URLs are malformed
- Port numbers are out of range (1-65535)
- A plugin-gated config section is enabled but the required plugin is not compiled in

### Plugin Availability Check

When plugins are registered (full build or custom build), the proxy checks that every enabled config section has its backing plugin. If not, it fails with a message like:

```
rate limiting is configured but the ratelimit plugin is not compiled in —
use the full build image or add the plugin import
```

This prevents silent misconfiguration where a feature appears enabled in the config but has no effect.

## Cross-Plugin Scenarios

### Rate-Limit-Only Mode

When both `auth.jwt.enabled` and `auth.api_key.enabled` are `false`, the proxy operates in **rate-limit-only mode**: all requests are forwarded without credential checks while rate limiting (if the `ratelimit` plugin is present) and admin dynamic rules (if the `admin` plugin is present) remain active. This is useful when only DDoS protection is needed without authentication.

### Admin + Storage for Multi-Instance Deployments

When both the `admin` and `storage-firestore` plugins are compiled in and `[storage]` is configured, admin rules are persisted to Firestore and synchronized across all Cloud Run instances. Without the storage plugin, rules are per-instance only. See the [Deployment Model](PLUGINS.md#deployment-model).

## See Also

- [Plugin Guide](PLUGINS.md) — Plugin-specific configuration reference
- [Admin Control Plane](ADMIN.md)
- [Rate Limiting Guide](RATE-LIMITING.md)
- [Environment Variables Guide](ENVIRONMENT.md)
- [API Documentation](API.md)
- [Deployment Guide](DEPLOYMENT.md)
