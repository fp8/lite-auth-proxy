# Plugin Architecture

lite-auth-proxy uses a compile-time plugin system. Plugins are Go packages that register themselves via `init()` functions; including or excluding a plugin is a matter of adding or removing a blank import in your `main.go`.

## Build Variants

| Image | Entry point | Plugins | Use case |
|-------|------------|---------|----------|
| `flex-auth-proxy:X.Y.Z` | `cmd/flex` | all | Full-featured proxy with all plugins |
| `lite-auth-proxy:X.Y.Z` | `cmd/lite` | none | Minimal JWT-only proxy |

The **flex** build includes all plugins and behaves identically to pre-plugin versions. The **lite** build is a minimal JWT-validating reverse proxy.

---

## Rate Limiter Plugin

| Property | Value |
|----------|-------|
| **Name** | `ratelimit` |
| **Priority** | `60` |
| **Interfaces** | `MiddlewareProvider` |
| **Import** | `_ "github.com/fp8/lite-auth-proxy/internal/plugins/ratelimit"` |

Provides three independent rate limiter layers: per-IP, per-API-key, and per-JWT. For detailed scenarios, tuning, and the ShockGuard throttle mechanism, see the [Rate Limiting Guide](RATE-LIMITING.md).

### Per-IP Rate Limiting

```toml
[security.rate_limit]
enabled = true
requests_per_min = 60
ban_for_min = 5
skip_if_jwt_identified = true
# throttle_delay_ms = 0
# max_delay_slots = 100
```

| Field | Type | Default | ENV Variable | Description |
|-------|------|---------|---|-------------|
| `enabled` | boolean | `false` | `PROXY_SECURITY_RATE_LIMIT_ENABLED` | Enable per-IP rate limiting |
| `requests_per_min` | integer | `60` | `PROXY_SECURITY_RATE_LIMIT_REQUESTS_PER_MIN` | Max requests per IP per minute |
| `ban_for_min` | integer | `5` | `PROXY_SECURITY_RATE_LIMIT_BAN_FOR_MIN` | Ban duration when limit exceeded (minutes) |
| `skip_if_jwt_identified` | boolean | `true` | `PROXY_SECURITY_RATE_LIMIT_SKIP_IF_JWT_IDENTIFIED` | Skip IP rate limit when a JWT `sub` claim is present |
| `throttle_delay_ms` | integer | `0` | `PROXY_SECURITY_RATE_LIMIT_THROTTLE_DELAY_MS` | Delay before 429 response (ms); `0` = disabled |
| `max_delay_slots` | integer | `100` | `PROXY_SECURITY_RATE_LIMIT_MAX_DELAY_SLOTS` | Max concurrent throttled responses (DDoS cap) |

### Per-API-Key Rate Limiting

```toml
[security.apikey_rate_limit]
enabled = false
requests_per_min = 200
ban_for_min = 5
include_ip = false
key_header = "x-goog-api-key"
# throttle_delay_ms = 0
# max_delay_slots = 100
```

| Field | Type | Default | ENV Variable | Description |
|-------|------|---------|---|-------------|
| `enabled` | boolean | `false` | `PROXY_SECURITY_APIKEY_RATE_LIMIT_ENABLED` | Enable per-API-key rate limiting |
| `requests_per_min` | integer | `60` | `PROXY_SECURITY_APIKEY_RATE_LIMIT_REQUESTS_PER_MIN` | Max requests per key per minute |
| `ban_for_min` | integer | `5` | `PROXY_SECURITY_APIKEY_RATE_LIMIT_BAN_FOR_MIN` | Ban duration (minutes) |
| `include_ip` | boolean | `false` | `PROXY_SECURITY_APIKEY_RATE_LIMIT_INCLUDE_IP` | Prefix rate-limit key with client IP |
| `key_header` | string | `"x-goog-api-key"` | `PROXY_SECURITY_APIKEY_RATE_LIMIT_KEY_HEADER` | Header to extract API key from |
| `throttle_delay_ms` | integer | `0` | `PROXY_SECURITY_APIKEY_RATE_LIMIT_THROTTLE_DELAY_MS` | Delay before 429 response (ms) |
| `max_delay_slots` | integer | `100` | `PROXY_SECURITY_APIKEY_RATE_LIMIT_MAX_DELAY_SLOTS` | Max concurrent throttled responses |

#### API-Key Request Matching

```toml
# Multiple [[match]] entries use OR logic; fields within a rule use AND logic.
# Host/Path support exact strings or /regex/ syntax.
[[security.apikey_rate_limit.match]]
host = "/.*-aiplatform\\.googleapis\\.com/"

[[security.apikey_rate_limit.match]]
path = "/\\/v1\\/projects\\/.*\\/(endpoints|publishers|models)\\//"
```

| Field | Type | Description |
|-------|------|-------------|
| `host` | string | Host pattern — exact string or `/regex/` |
| `path` | string | Path pattern — exact string or `/regex/` |
| `header` | string | Header name that must be present |

### Per-JWT Rate Limiting

```toml
[security.jwt_rate_limit]
enabled = false
requests_per_min = 200
ban_for_min = 5
include_ip = true
# throttle_delay_ms = 0
# max_delay_slots = 100
```

| Field | Type | Default | ENV Variable | Description |
|-------|------|---------|---|-------------|
| `enabled` | boolean | `false` | `PROXY_SECURITY_JWT_RATE_LIMIT_ENABLED` | Enable per-JWT rate limiting |
| `requests_per_min` | integer | `60` | `PROXY_SECURITY_JWT_RATE_LIMIT_REQUESTS_PER_MIN` | Max requests per JWT `sub` per minute |
| `ban_for_min` | integer | `5` | `PROXY_SECURITY_JWT_RATE_LIMIT_BAN_FOR_MIN` | Ban duration (minutes) |
| `include_ip` | boolean | `false` | `PROXY_SECURITY_JWT_RATE_LIMIT_INCLUDE_IP` | Prefix rate-limit key with client IP |
| `throttle_delay_ms` | integer | `0` | `PROXY_SECURITY_JWT_RATE_LIMIT_THROTTLE_DELAY_MS` | Delay before 429 response (ms) |
| `max_delay_slots` | integer | `100` | `PROXY_SECURITY_JWT_RATE_LIMIT_MAX_DELAY_SLOTS` | Max concurrent throttled responses |

---

## Admin Plugin

| Property | Value |
|----------|-------|
| **Name** | `admin` |
| **Priority** | `50` |
| **Interfaces** | `RouteProvider`, `MiddlewareProvider` |
| **Import** | `_ "github.com/fp8/lite-auth-proxy/internal/plugins/admin"` |

Provides the `/admin/control` and `/admin/status` endpoints for runtime traffic management (throttle, block, allow), plus `DynamicRuleCheck` middleware. For endpoints, rule lifecycle, and operational details see the [Admin Control Plane Guide](ADMIN.md).

### Configuration

```toml
[admin]
enabled = true

[admin.jwt]
issuer         = "https://accounts.google.com"
audience       = "https://your-proxy.run.app"
allowed_emails = ["sa@my-project.iam.gserviceaccount.com"]
```

| Field | Type | Default | ENV Variable | Description |
|-------|------|---------|---|-------------|
| `admin.enabled` | boolean | `false` | `PROXY_ADMIN_ENABLED` | Register `/admin/control` and `/admin/status` routes |
| `admin.jwt.issuer` | string | `"https://accounts.google.com"` | `PROXY_ADMIN_JWT_ISSUER` | Expected OIDC issuer for admin identity tokens |
| `admin.jwt.audience` | string | — | `PROXY_ADMIN_JWT_AUDIENCE` | Expected audience — set to the proxy's own Cloud Run URL |
| `admin.jwt.allowed_emails` | array | `[]` | `PROXY_ADMIN_JWT_ALLOWED_EMAILS` | Service account emails allowed to call the admin API |
| `admin.jwt.filters` | map | `{}` | — | Require specific JWT claim values (e.g. `hd = "corp.com"`) |
| `admin.jwt.tolerance_secs` | integer | `30` | `PROXY_ADMIN_JWT_TOLERANCE_SECS` | Clock skew tolerance for admin token validation |
| `admin.jwt.cache_ttl_mins` | integer | `1440` | `PROXY_ADMIN_JWT_CACHE_TTL_MINS` | Admin token cache TTL (minutes) |

**Access control:** At least one of `allowed_emails` or `filters` must be set when `admin.enabled = true`.

### Rule Persistence

Without a storage plugin, all admin rules are held in process memory and lost on restart. In serverless environments, use one of:

1. **`PROXY_THROTTLE_RULES` env var** — pre-loads rules on startup. See [Admin Guide](ADMIN.md#startup-rule-persistence).
2. **Firestore storage plugin** — provides persistent, cross-instance rule sync. See [Storage Plugin](#storage-firestore-plugin) below.

---

## API-Key Authentication Plugin

| Property | Value |
|----------|-------|
| **Name** | `apikey` |
| **Priority** | `90` |
| **Interfaces** | `AuthProvider` |
| **Import** | `_ "github.com/fp8/lite-auth-proxy/internal/plugins/apikey"` |

Adds API-key authentication as an independent method alongside JWT. Requests with the configured header are validated against a static key value; on success, static payload headers are injected.

### Configuration

```toml
[auth.api_key]
enabled = false
name = "X-API-KEY"
value = "{{ENV.API_KEY_SECRET}}"

[auth.api_key.payload]
service = "internal"
source = "backend-job"
```

| Field | Type | Default | ENV Variable | Description |
|-------|------|---------|---|-------------|
| `auth.api_key.enabled` | boolean | `false` | `PROXY_AUTH_API_KEY_ENABLED` | Enable API-Key authentication |
| `auth.api_key.name` | string | `"X-API-KEY"` | `PROXY_AUTH_API_KEY_NAME` | HTTP header name to check for API key |
| `auth.api_key.value` | string | **required if enabled** | `PROXY_AUTH_API_KEY_VALUE` | Expected API key value (use env var substitution) |

#### Payload Injection

When API-key auth succeeds, static headers from `[auth.api_key.payload]` are injected:

| Payload Key | Example Value | Result Header | ENV Variable |
|------------|---|---|---|
| `service` | `"internal"` | `X-AUTH-SERVICE` | `PROXY_AUTH_API_KEY_PAYLOAD_SERVICE=internal` |
| `source` | `"backend-job"` | `X-AUTH-SOURCE` | `PROXY_AUTH_API_KEY_PAYLOAD_SOURCE=backend-job` |

Header name format: `{auth.header_prefix}{UPPER(key)}` (e.g. `service` with prefix `X-AUTH-` becomes `X-AUTH-SERVICE`).

---

## Storage: Firestore Plugin

| Property | Value |
|----------|-------|
| **Name** | `storage-firestore` |
| **Priority** | `5` |
| **Interfaces** | `StorageBackendProvider`, `ConfigValidator`, `Starter`, `Stopper` |
| **Import** | `_ "github.com/fp8/lite-auth-proxy/internal/plugins/storage/firestore"` |

Provides persistent `RuleStore` and `KeyValueStore` implementations backed by Google Cloud Firestore. Enables cross-instance rule synchronization in multi-instance deployments.

### Configuration

```toml
[storage]
enabled = false                      # Enable persistent storage backend (Firestore)
# project_id = ""                    # Defaults to GOOGLE_CLOUD_PROJECT env var
# dbname = ""                        # Firestore database name (defaults to "(default)")
# collection_prefix = "proxy"        # Collections: {prefix}-rules, {prefix}-apikeys
```

| Field | Type | Default | ENV Variable | Description |
|-------|------|---------|---|-------------|
| `storage.enabled` | boolean | `false` | `PROXY_STORAGE_ENABLED` | Enable persistent storage backend (Firestore) |
| `storage.project_id` | string | `GOOGLE_CLOUD_PROJECT` | `PROXY_STORAGE_PROJECT_ID` | GCP project ID |
| `storage.dbname` | string | `"(default)"` | `PROXY_STORAGE_DBNAME` | Firestore database name (e.g. `"flex-auth-proxy"`) |
| `storage.collection_prefix` | string | `"proxy"` | `PROXY_STORAGE_COLLECTION_PREFIX` | Firestore collection prefix (`[a-z0-9-]` only) |

### How It Works

- **Write path**: Rules are written to the in-memory cache immediately, then persisted to Firestore asynchronously. Other instances receive changes via a real-time snapshot listener within 1-2 seconds.
- **Read path**: `ShouldAllow()` reads from the in-memory cache only. Zero Firestore calls on the hot path.
- **Initial load**: On startup, all non-expired rules are loaded from Firestore before the proxy serves traffic.
- **Conflict resolution**: Last-writer-wins. All instances converge via the snapshot listener.

### GCP Requirements

1. **Firestore database** in Native mode:
   ```bash
   gcloud firestore databases create --location=your-region
   ```

2. **IAM role** for the Cloud Run service account:
   ```bash
   gcloud projects add-iam-policy-binding $GOOGLE_CLOUD_PROJECT \
     --member="serviceAccount:$SERVICE_ACCOUNT" \
     --role="roles/datastore.user"
   ```

3. **TTL policy** (optional, recommended) for automatic cleanup of expired rules:
   ```bash
   gcloud firestore fields ttls update expiresAt \
     --collection-group=proxy-rules --enable-ttl
   ```

### Firestore Data Model

| Collection | Document ID | Description |
|------------|-------------|-------------|
| `{prefix}-rules` | `ruleId` | Admin rules with `expiresAt` TTL |
| `{prefix}-apikeys` | `keyId` | API key entries |

---

## Deployment Model

| Admin Plugin | Storage Plugin | Cloud Run Deployment |
|-------------|---------------|---------------------|
| not compiled in | n/a | Any (no admin state to sync) |
| enabled | not compiled in | `max-instances=1` recommended (rules are per-instance) |
| enabled | enabled | Any (rules synced via Firestore) |

---

## Creating a Custom Build

1. Copy `cmd/flex/main.go` to a new entry point (e.g. `cmd/custom/main.go`).
2. Add only the blank imports you need:

```go
import (
    // Pick the plugins you want:
    _ "github.com/fp8/lite-auth-proxy/internal/plugins/ratelimit"
    _ "github.com/fp8/lite-auth-proxy/internal/plugins/admin"
    // _ "github.com/fp8/lite-auth-proxy/internal/plugins/apikey"
    // _ "github.com/fp8/lite-auth-proxy/internal/plugins/storage/firestore"
)
```

3. Build: `go build -o ./bin/custom-auth-proxy ./cmd/custom`

If your config enables a feature whose plugin is not imported, the proxy fails at startup with a clear error naming the missing plugin and the import path to add.

## Plugin Interfaces

All interfaces are defined in `internal/plugin/interfaces.go`.

| Interface | Purpose |
|-----------|---------|
| `Plugin` | Base: `Name()`, `Priority()` |
| `MiddlewareProvider` | Contributes HTTP middleware |
| `RouteProvider` | Registers HTTP routes |
| `AuthProvider` | Adds an authentication method |
| `StorageBackendProvider` | Provides persistent store implementations |
| `ConfigValidator` | Validates plugin-owned config sections |
| `Starter` | Called after initialization, before serving |
| `Stopper` | Called during graceful shutdown |

## Plugin Development

1. Create a package under `internal/plugins/`.
2. Implement `plugin.Plugin` plus any additional interfaces.
3. Register via `init()`:

```go
func init() {
    plugin.Register(&myPlugin{})
}
```

4. Add a blank import in the desired build's `main.go`.

### Priority Guidelines

| Range | Owner |
|-------|-------|
| 0-9 | Storage backends (initialize first) |
| 10-49 | Core middleware |
| 50-89 | Feature plugins (admin, ratelimit) |
| 90+ | Auth plugins (run last in middleware chain) |

### Constraints

- Plugin names must be unique. `Register()` panics on duplicate names.
- Only one `StorageBackendProvider` may exist per binary. `Register()` panics if a second is registered.

## See Also

- [Configuration Guide](CONFIGURATION.md) — Core config reference and cross-plugin scenarios
- [Rate Limiting Guide](RATE-LIMITING.md) — Detailed scenarios and tuning
- [Admin Control Plane](ADMIN.md) — Endpoints, rule lifecycle, serverless caveats
- [Environment Variables](ENVIRONMENT.md) — All env var overrides
