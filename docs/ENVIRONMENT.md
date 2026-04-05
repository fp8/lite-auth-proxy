# Environment Variables Guide

This document provides a comprehensive reference for all environment variables used by lite-auth-proxy.

## Overview

lite-auth-proxy supports environment variables in three ways:

1. **Configuration Overrides** - `PROXY_*` prefix overrides config file values
2. **Substitution in Config Files** - `{{ENV.*}}` syntax for dynamic values
3. **Logging Configuration** - `LOG_MODE` and `LOG_LEVEL` control logging behavior

## Configuration Override Variables

These variables override values from the TOML configuration file at runtime. They use the `PROXY_` prefix followed by the configuration path in uppercase with underscores.

### Server Configuration

| Variable | Type | Default | Description |
|----------|------|---------|-------------|
| `PROXY_SERVER_PORT` | integer | `8888` | HTTP listening port |
| `PROXY_SERVER_TARGET_URL` | string | - | Downstream service URL to proxy to |
| `PROXY_SERVER_STRIP_PREFIX` | string | `""` | URL prefix to strip before forwarding |
| `PROXY_SERVER_SHUTDOWN_TIMEOUT_SECS` | integer | `10` | Graceful shutdown timeout in seconds |
| `PROXY_SERVER_HEALTH_CHECK_PATH` | string | `"/healthz"` | Health check endpoint path |
| `PROXY_SERVER_HEALTH_CHECK_TARGET` | string | `""` | Optional: proxy health check to downstream |

**Example:**
```bash
export PROXY_SERVER_PORT=9090
export PROXY_SERVER_TARGET_URL=http://backend:8080
export PROXY_SERVER_STRIP_PREFIX=/api
```

### Security Configuration

#### Per-IP Rate Limiting

| Variable | Type | Default | Description |
|----------|------|---------|-------------|
| `PROXY_SECURITY_RATE_LIMIT_ENABLED` | boolean | `false` | Enable per-IP rate limiting |
| `PROXY_SECURITY_RATE_LIMIT_REQUESTS_PER_MIN` | integer | `60` | Max requests per IP per minute |
| `PROXY_SECURITY_RATE_LIMIT_BAN_FOR_MIN` | integer | `5` | Ban duration when limit exceeded (minutes) |
| `PROXY_SECURITY_RATE_LIMIT_SKIP_IF_JWT_IDENTIFIED` | boolean | `true` | Skip IP rate limit when a JWT sub claim is present |
| `PROXY_SECURITY_RATE_LIMIT_THROTTLE_DELAY_MS` | integer | `0` | Delay before 429 response (ms); `0` = disabled |
| `PROXY_SECURITY_RATE_LIMIT_MAX_DELAY_SLOTS` | integer | `100` | Max concurrent throttled responses |
| `PROXY_SECURITY_MAX_BODY_BYTES` | integer | `1048576` | Max request body size in bytes (1 MiB default) |

#### Per-API-Key Rate Limiting

| Variable | Type | Default | Description |
|----------|------|---------|-------------|
| `PROXY_SECURITY_APIKEY_RATE_LIMIT_ENABLED` | boolean | `false` | Enable per-API-key rate limiting |
| `PROXY_SECURITY_APIKEY_RATE_LIMIT_REQUESTS_PER_MIN` | integer | `60` | Max requests per key per minute |
| `PROXY_SECURITY_APIKEY_RATE_LIMIT_BAN_FOR_MIN` | integer | `5` | Ban duration (minutes) |
| `PROXY_SECURITY_APIKEY_RATE_LIMIT_INCLUDE_IP` | boolean | `false` | Prefix rate-limit key with client IP |
| `PROXY_SECURITY_APIKEY_RATE_LIMIT_KEY_HEADER` | string | `"x-goog-api-key"` | Header to extract API key from |
| `PROXY_SECURITY_APIKEY_RATE_LIMIT_THROTTLE_DELAY_MS` | integer | `0` | Delay before 429 response (ms) |
| `PROXY_SECURITY_APIKEY_RATE_LIMIT_MAX_DELAY_SLOTS` | integer | `100` | Max concurrent throttled responses |

#### Per-JWT Rate Limiting

| Variable | Type | Default | Description |
|----------|------|---------|-------------|
| `PROXY_SECURITY_JWT_RATE_LIMIT_ENABLED` | boolean | `false` | Enable per-JWT rate limiting |
| `PROXY_SECURITY_JWT_RATE_LIMIT_REQUESTS_PER_MIN` | integer | `60` | Max requests per JWT `sub` per minute |
| `PROXY_SECURITY_JWT_RATE_LIMIT_BAN_FOR_MIN` | integer | `5` | Ban duration (minutes) |
| `PROXY_SECURITY_JWT_RATE_LIMIT_INCLUDE_IP` | boolean | `false` | Prefix rate-limit key with client IP |
| `PROXY_SECURITY_JWT_RATE_LIMIT_THROTTLE_DELAY_MS` | integer | `0` | Delay before 429 response (ms) |
| `PROXY_SECURITY_JWT_RATE_LIMIT_MAX_DELAY_SLOTS` | integer | `100` | Max concurrent throttled responses |

**Example:**
```bash
export PROXY_SECURITY_RATE_LIMIT_ENABLED=true
export PROXY_SECURITY_RATE_LIMIT_REQUESTS_PER_MIN=100
export PROXY_SECURITY_APIKEY_RATE_LIMIT_ENABLED=true
export PROXY_SECURITY_APIKEY_RATE_LIMIT_KEY_HEADER=x-goog-api-key
```

### Authentication Configuration

| Variable | Type | Default | Description |
|----------|------|---------|-------------|
| `PROXY_AUTH_HEADER_PREFIX` | string | `"X-AUTH-"` | Prefix for injected auth headers |

**Example:**
```bash
export PROXY_AUTH_HEADER_PREFIX=X-USER-
```

### JWT Authentication

| Variable | Type | Default | Description |
|----------|------|---------|-------------|
| `PROXY_AUTH_JWT_ENABLED` | boolean | `false` | Enable JWT authentication |
| `PROXY_AUTH_JWT_ISSUER` | string | - | JWT issuer URL (supports `{{ENV.*}}` substitution) |
| `PROXY_AUTH_JWT_AUDIENCE` | string | - | JWT audience claim value |

**Example:**
```bash
export PROXY_AUTH_JWT_ENABLED=true
export PROXY_AUTH_JWT_ISSUER=https://accounts.google.com
export PROXY_AUTH_JWT_AUDIENCE=my-app-client-id
```

### API-Key Authentication

| Variable | Type | Default | Description |
|----------|------|---------|-------------|
| `PROXY_AUTH_API_KEY_ENABLED` | boolean | `false` | Enable API-Key authentication |
| `PROXY_AUTH_API_KEY_NAME` | string | `"X-API-KEY"` | Header name to check for API key |
| `PROXY_AUTH_API_KEY_VALUE` | string | - | Expected API key value |

**Example:**
```bash
export PROXY_AUTH_API_KEY_ENABLED=true
export PROXY_AUTH_API_KEY_NAME=X-API-KEY
export PROXY_AUTH_API_KEY_VALUE=my-secret-key-123
```

### Admin Control-Plane

| Variable | Type | Default | Description |
|----------|------|---------|-------------|
| `PROXY_ADMIN_ENABLED` | boolean | `false` | Register `/admin/control` and `/admin/status` routes |
| `PROXY_ADMIN_JWT_ISSUER` | string | `"https://accounts.google.com"` | OIDC issuer for admin identity tokens |
| `PROXY_ADMIN_JWT_AUDIENCE` | string | - | Expected audience — set to the proxy's own Cloud Run URL |
| `PROXY_ADMIN_JWT_ALLOWED_EMAILS` | string (CSV) | - | Comma-separated service account emails allowed to call the admin API |
| `PROXY_THROTTLE_RULES` | JSON string | - | Persisted throttle rules loaded on startup (see below) |

### Storage Backend

| Variable | Type | Default | Description |
|----------|------|---------|-------------|
| `PROXY_STORAGE_BACKEND` | string | `""` | Storage backend name (`"firestore"` or empty) |
| `PROXY_STORAGE_PROJECT_ID` | string | `GOOGLE_CLOUD_PROJECT` | GCP project ID for storage |
| `PROXY_STORAGE_COLLECTION_PREFIX` | string | `"proxy"` | Firestore collection prefix (`[a-z0-9-]` only) |

**Example:**
```bash
export PROXY_ADMIN_ENABLED=true
export PROXY_ADMIN_JWT_AUDIENCE=https://my-proxy-abc123.run.app
export PROXY_ADMIN_JWT_ALLOWED_EMAILS=sg-killswitch@my-project.iam.gserviceaccount.com
```

#### PROXY_THROTTLE_RULES

A JSON array of rule objects pre-loaded into the rule store before the proxy begins serving traffic. Used to survive Cloud Run instance restarts without waiting for the next ShockGuard cycle.

```bash
export PROXY_THROTTLE_RULES='[
  {
    "ruleId":          "sg-throttle-vertex",
    "targetHost":      "-aiplatform.googleapis.com",
    "action":          "throttle",
    "maxRPM":          200,
    "pathPattern":     "/v1/projects/",
    "rateByKey":       true,
    "expiresAt":       "2026-03-30T15:10:00Z"
  }
]'
```

| Rule Field | Type | Required | Description |
|------------|------|----------|-------------|
| `ruleId` | string | yes | Unique identifier |
| `targetHost` | string | yes | Exact `Host` header value to match |
| `action` | string | yes | `throttle`, `block`, or `allow` |
| `maxRPM` | integer | when throttle | Max requests per minute |
| `pathPattern` | string | no | Path prefix to restrict the rule |
| `rateByKey` | boolean | no | Per-caller-identity mode (default `false` = global) |
| `expiresAt` | string | yes | Absolute RFC 3339 expiry; past values are silently skipped |

## Configuration File Substitution Variables

These variables can be referenced in TOML configuration files using `{{ENV.VARIABLE_NAME}}` syntax. They are substituted at configuration load time.

### Required Variables

| Variable | Description | Example |
|----------|-------------|---------|
| `GOOGLE_CLOUD_PROJECT` | Used by JWT issuer/audience substitution; auto-detected from GCP metadata server | `my-project-id` |
| `API_KEY_SECRET` | Secret API key for authentication | `secure-random-string` |

**Example Configuration:**
```toml
[auth.jwt]
enabled = true
issuer = "https://securetoken.google.com/{{ENV.GOOGLE_CLOUD_PROJECT}}"
audience = "{{ENV.GOOGLE_CLOUD_PROJECT}}"

[auth.api_key]
enabled = true
value = "{{ENV.API_KEY_SECRET}}"
```

### GOOGLE_CLOUD_PROJECT Auto-Detection

If `GOOGLE_CLOUD_PROJECT` is not set, the proxy will attempt to fetch it from:

1. Environment variable `GOOGLE_CLOUD_PROJECT`
2. GCP Metadata Server (when running on GCP)

**Metadata Server Query:**
- URL: `http://metadata.google.internal/computeMetadata/v1/project/project-id`
- Header: `Metadata-Flavor: Google`
- Timeout: 1 second

## Logging Configuration Variables

These variables control logging behavior and are NOT prefixed with `PROXY_`.

| Variable | Type | Default | Description |
|----------|------|---------|-------------|
| `LOG_MODE` | string | `development` | Logging format: `development` (text) or `production` (JSON) |
| `LOG_LEVEL` | string | `info` | Logging level: `debug`, `info`, `warn`, `error` |

**Log Mode Behaviors:**
- `development`: Human-readable text output with colors (for local development)
- `production`: Structured JSON output (Google Cloud Logging compatible)

**Example:**
```bash
export LOG_MODE=production
export LOG_LEVEL=info
```

## Environment Detection

The proxy automatically detects Google Cloud environments and adjusts behavior:

| Detection Variable | Indicates | Behavior |
|-------------------|-----------|----------|
| `K_SERVICE` | Cloud Run | Enables production logging, fetches project ID from metadata |
| `K_REVISION` | Cloud Run | Logs revision information |
| `K_CONFIGURATION` | Cloud Run | Logs configuration name |

## Complete Environment Examples

### Local Development

```bash
# .env file for local development
export GOOGLE_CLOUD_PROJECT=my-dev-project
export API_KEY_SECRET=dev-api-key-123
export LOG_MODE=development
export LOG_LEVEL=debug
export PROXY_SERVER_TARGET_URL=http://localhost:8080
export PROXY_SERVER_PORT=8888
export PROXY_AUTH_JWT_ENABLED=true
```

### Docker Container

```bash
docker run -p 8888:8888 \
  -e GOOGLE_CLOUD_PROJECT=my-project \
  -e API_KEY_SECRET=secure-key \
  -e LOG_MODE=production \
  -e LOG_LEVEL=info \
  -e PROXY_SERVER_TARGET_URL=http://backend:8080 \
  -e PROXY_AUTH_JWT_ENABLED=true \
  flex-auth-proxy:latest
```

### Google Cloud Run

```bash
gcloud run deploy my-service \
  --image=gcr.io/my-project/flex-auth-proxy:latest \
  --set-env-vars GOOGLE_CLOUD_PROJECT=my-project,\
LOG_MODE=production,\
PROXY_SERVER_TARGET_URL=http://backend:8080,\
PROXY_AUTH_JWT_ENABLED=true \
  --set-secrets=API_KEY_SECRET=api-key-secret:latest
```

### Testing Environment

```bash
# For running integration tests
export GOOGLE_CLOUD_PROJECT=test-project
export API_KEY_SECRET=test-key-123
export LOG_MODE=development
export LOG_LEVEL=debug
export PROXY_SERVER_PORT=9999
export PROXY_SERVER_TARGET_URL=http://localhost:8080
```

## Secret Management

### Using Google Secret Manager

Store sensitive values in Google Secret Manager and reference them:

```bash
# Create secret
echo -n "my-secure-api-key" | gcloud secrets create api-key-secret \
  --data-file=- \
  --replication-policy=automatic

# Grant access to service account
gcloud secrets add-iam-policy-binding api-key-secret \
  --member=serviceAccount:my-sa@my-project.iam.gserviceaccount.com \
  --role=roles/secretmanager.secretAccessor

# Use in Cloud Run
gcloud run deploy my-service \
  --set-secrets=API_KEY_SECRET=api-key-secret:latest
```

### Using Environment Variable Files

For local development, use a `.env` file (don't commit to git):

```bash
# .env
GOOGLE_CLOUD_PROJECT=my-project
API_KEY_SECRET=local-dev-key
PROXY_SERVER_TARGET_URL=http://localhost:8080
PROXY_AUTH_JWT_ENABLED=true
```

Load with:
```bash
source .env
./bin/flex-auth-proxy
```

## Boolean Value Formats

Boolean environment variables accept these values (case-insensitive):

| True Values | False Values |
|-------------|--------------|
| `true`, `1`, `t`, `T`, `TRUE`, `yes`, `YES` | `false`, `0`, `f`, `F`, `FALSE`, `no`, `NO` |

## Validation and Error Handling

### Invalid Values

If an environment variable override has an invalid value, the proxy will:

1. Log an error with the variable name and expected type
2. Exit with status code `1`
3. Display the validation error message

**Example Error:**
```
Error: failed to apply env overrides: invalid PROXY_SERVER_PORT: strconv.Atoi: parsing "abc": invalid syntax
```

### Missing Required Variables

If required variables are missing during config substitution:

1. The placeholder `{{ENV.VARIABLE_NAME}}` remains unchanged in config
2. Configuration validation may fail if the field is required
3. Error message indicates which field has invalid value

## Precedence Order

Configuration values are resolved in the following order (highest to lowest precedence):

1. **Environment variable overrides** (`PROXY_*` variables)
2. **Config file with env substitution** (after `{{ENV.*}}` replacement)
3. **Default values** (hardcoded in application)

**Example:**
```toml
# config.toml
[server]
port = 8888  # From config file
```

```bash
export PROXY_SERVER_PORT=9090  # Overrides config file
```

Result: Port `9090` is used.

## Test-Only Environment Variables

These variables are **not used by the proxy itself** but are required for running integration tests with real GCP services (Firebase JWT validation).

They should be defined in the `.env` file (which is gitignored) for local testing only.

| Variable | Description | Example |
|----------|-------------|---------|
| `GOOGLE_CLOUD_PROJECT` | Used for JWT issuer/audience substitution in tests | `my-gcp-project` |
| `FIREBASE_API_KEY_SECRET_NAME` | Name of Secret Manager secret containing Firebase API Key | `firebase-api-key-dev` |
| `FIREBASE_LOGIN_SECRET_NAME` | Name of Secret Manager secret containing test login credentials | `firebase-login-dev` |

**Setup for testing:**

```bash
# Copy template
cp .env.example .env

# Edit with your Firebase test project credentials
nano .env

# Run tests (loads .env automatically)
make test-all
```

See [Development Guide](DEVELOPMENT.md#real-world-jwt-tests-firebase) for detailed setup instructions.

## See Also

- [Configuration Guide](CONFIGURATION.md)
- [Plugin Guide](PLUGINS.md)
- [Development Guide](DEVELOPMENT.md)
- [Deployment Guide](DEPLOYMENT.md)
