# Configuration Guide

This document provides a comprehensive reference for all configuration options in lite-auth-proxy.

## Configuration File Format

lite-auth-proxy uses TOML format for its configuration files. The default configuration file location is `configs/config.toml`, but you can specify a custom location using the `-config` flag:

```bash
./bin/lite-auth-proxy -config /path/to/custom-config.toml
```

## Configuration Structure

The configuration is organized into four top-level sections:

- **[server]** - HTTP server and proxy settings
- **[security]** - Security features like rate limiting
- **[auth]** - Authentication configuration (JWT and API-Key)
- **[auth.jwt]** - JWT-specific settings
- **[auth.api_key]** - API-Key-specific settings

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

### Rate Limiting

```toml
[security.rate_limit]
enabled = true
requests_per_min = 60
ban_for_min = 5
```

| Field | Type | Default | ENV Variable | Description |
|-------|------|---------|---|-------------|
| `enabled` | boolean | `false` | `PROXY_SECURITY_RATE_LIMIT_ENABLED` | Enable/disable rate limiting |
| `requests_per_min` | integer | `60` | `PROXY_SECURITY_RATE_LIMIT_REQUESTS_PER_MIN` | Maximum requests per IP address per minute |
| `ban_for_min` | integer | `5` | `PROXY_SECURITY_RATE_LIMIT_BAN_FOR_MIN` | Ban duration in minutes when rate limit is exceeded |

**Rate Limiting Behavior:**
- Rate limiting is per-IP address
- When limit is exceeded, IP is banned for specified duration
- Returns HTTP 429 (Too Many Requests) with `retry_after` in response

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

```toml
[auth.jwt]
enabled = true
issuer = "https://securetoken.google.com/{{ENV.GOOGLE_CLOUD_PROJECT}}"
audience = "{{ENV.GOOGLE_CLOUD_PROJECT}}"
tolerance_secs = 30
cache_ttl_mins = 1440
```

| Field | Type | Default | ENV Variable | Description |
|-------|------|---------|---|-------------|
| `enabled` | boolean | `false` | `PROXY_AUTH_JWT_ENABLED` | Enable JWT authentication |
| `issuer` | string | **required if enabled** | `PROXY_AUTH_JWT_ISSUER` | Expected JWT issuer (OpenID Connect issuer URL) |
| `audience` | string | **required if enabled** | `PROXY_AUTH_JWT_AUDIENCE` | Expected JWT audience claim |
| `tolerance_secs` | integer | `30` | `PROXY_AUTH_JWT_TOLERANCE_SECS` | Clock skew tolerance for `exp` and `nbf` validation (seconds) |
| `cache_ttl_mins` | integer | `1440` | `PROXY_AUTH_JWT_CACHE_TTL_MINS` | JWKS public key cache TTL (minutes, default 24 hours) |

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

API-Key authentication is independent of JWT (not a fallback):

```toml
[auth.api_key]
enabled = false
name = "X-API-KEY"
value = "{{ENV.API_KEY_SECRET}}"
```

| Field | Type | Default | ENV Variable | Description |
|-------|------|---------|---|-------------|
| `enabled` | boolean | `false` | `PROXY_AUTH_API_KEY_ENABLED` | Enable API-Key authentication |
| `name` | string | `"X-API-KEY"` | `PROXY_AUTH_API_KEY_NAME` | HTTP header name to check for API key |
| `value` | string | **required if enabled** | `PROXY_AUTH_API_KEY_VALUE` | Expected API key value (use env var substitution) |

**API-Key Validation:**
- Constant-time comparison prevents timing attacks
- Returns 401 Unauthorized if key doesn't match

#### API-Key Payload Injection

Inject static headers when API-key authentication succeeds:

```toml
[auth.api_key.payload]
service = "internal"
source = "backend-job"
team = "platform"
```

| Payload Key | Header Value | Result Header | ENV Variable | Description |
|------------|---|---|---|-------------|
| `service` | `"internal"` | `X-AUTH-SERVICE` | `PROXY_AUTH_API_KEY_PAYLOAD_SERVICE=internal` | Static header injected on auth success |
| `source` | `"backend-job"` | `X-AUTH-SOURCE` | `PROXY_AUTH_API_KEY_PAYLOAD_SOURCE=backend-job` | Static header injected on auth success |
| `team` | `"platform"` | `X-AUTH-TEAM` | `PROXY_AUTH_API_KEY_PAYLOAD_TEAM=platform` | Static header injected on auth success |

**Payload Rules:**
- Header name format: `{header_prefix}{UPPER(payload_key)}`
- Example: `service = "internal"` → `X-AUTH-SERVICE: internal`

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
| `PROXY_SECURITY_RATE_LIMIT_ENABLED` | `security.rate_limit.enabled` | boolean |
| `PROXY_SECURITY_RATE_LIMIT_REQUESTS_PER_MIN` | `security.rate_limit.requests_per_min` | integer |
| `PROXY_SECURITY_RATE_LIMIT_BAN_FOR_MIN` | `security.rate_limit.ban_for_min` | integer |

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

The default configuration file (`configs/config.toml`) comes pre-configured for quick setup. Here's what's enabled by default:

### What's Included by Default

```toml
[server]
port = 8888                          # Listens on port 8888
target_url = "http://localhost:8080" # Proxies to localhost:8080
include_paths = ["/*"]               # All paths require authentication
exclude_paths = ["/healthz"]         # Health check bypasses auth

[security.rate_limit]
enabled = true                       # Rate limiting is ON
requests_per_min = 60                # Max 60 requests per IP per minute
ban_for_min = 5                      # Ban duration is 5 minutes

[auth.jwt]
enabled = true                       # JWT authentication is ON
issuer = "https://securetoken.google.com/{{ENV.GOOGLE_CLOUD_PROJECT}}"
audience = "{{ENV.GOOGLE_CLOUD_PROJECT}}"

[auth.api_key]
enabled = false                      # API-Key auth is OFF by default
```

### What's Disabled by Default

- **API-Key Authentication**: Disabled (`enabled = false`)
- **JWT Filters**: No filters configured (all JWT tokens accepted if valid)
- **JWT Mappings**: Basic mappings only (`sub` → `USER-ID`, `email` → `USER-EMAIL`)

### Enabling API-Key Authentication

The easiest way to enable API-Key authentication is via environment variables:

```bash
# Method 1: Using environment variables (overrides config)
export PROXY_AUTH_API_KEY_ENABLED=true
export API_KEY_SECRET="your-secret-key-value"
./bin/lite-auth-proxy

# Method 2: Using TOML configuration
# Edit configs/config.toml:
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

[auth.jwt.mappings]
email = "USER-EMAIL"
sub = "USER-ID"
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

## Configuration Validation

The proxy validates configuration at startup and exits with an error if:

- Required fields are missing (e.g., `target_url` when no auth enabled)
- Field types are incorrect (e.g., string for integer field)
- URLs are malformed
- Port numbers are out of range (1-65535)
- Both JWT and API-Key are disabled but `include_paths` requires auth

## See Also

- [Environment Variables Guide](ENVIRONMENT.md)
- [API Documentation](API.md)
- [Deployment Guide](DEPLOYMENT.md)
