# USAGE_GUIDE: lite-auth-proxy

## EXECUTIVE_SUMMARY
High-performance Go-based reverse proxy with JWT/API-key authentication for serverless sidecar deployments. Optimized for Google Cloud Run with <50ms startup, <32MB memory footprint. Zero-trust security model with automatic JWKS discovery, claim-based filtering, rate limiting, and header injection.

## TECH_STACK (Runtime)
**Language**: Go 1.24 (backward compatible to 1.23)
**Runtime**: Standalone HTTP server (port 8888 default), minimal container (~13MB)
**Container**: Multi-stage Docker build with distroless base (gcr.io/distroless/static-debian12:nonroot)
**Configuration**: TOML format with environment variable substitution/overrides
**Logging**: Standard library slog (structured JSON/text)
**External Dependencies**: github.com/BurntSushi/toml v1.6.0 (TOML config parsing only)

## KEY_CAPABILITIES
- **JWT Authentication**: OpenID Connect compliant with automatic JWKS discovery
- **API-Key Authentication**: Independent authentication method with constant-time comparison
- **Rate-Limit-Only Mode**: Both auth methods can be disabled; requests are forwarded without credential checks while rate limiting still applies
- **Claim Filtering**: Exact and regex pattern matching on JWT claims for fine-grained access control
- **Email Allowlist**: Optional `allowed_emails` list on both `auth.jwt` and `admin.jwt`; empty list means no restriction
- **Claim Mapping**: Transform JWT claims to HTTP headers for downstream services
- **Unified Rate Limiting**: Per-IP, per-API-key, and per-JWT rate limiting with configurable request matching, automatic bans, and optional DDoS-safe throttle delay
- **Header Injection**: Inject authentication context as headers to downstream services
- **Header Sanitization**: Prevent header injection attacks by removing incoming auth headers
- **Health Checks**: Local or proxied health endpoints for orchestration compatibility
- **Admin Control Plane**: Runtime throttle/block/allow rules via `/admin/control` with `limiter` field to target specific rate limiters (`ip`, `apikey`, `jwt`)

## CONFIGURATION_SYSTEM

### Config Loading Flow
1. Read TOML file from `-config` flag (default: configs/config.toml)
2. Substitute `{{ENV.VARIABLE_NAME}}` placeholders with env vars
3. Parse TOML into Config struct (github.com/BurntSushi/toml)
4. Apply `PROXY_*` environment variable overrides
5. Set default values for optional fields
6. Validate required fields and constraints
7. Return Config pointer or error

### Config Structure (Go)
```go
type Config struct {
    Server   ServerConfig
    Security SecurityConfig
    Auth     AuthConfig
    Admin    AdminConfig
}

type ServerConfig struct {
    Port                  int         // 8888 default
    TargetURL             string      // Required: downstream URL
    StripPrefix           string      // Optional: URL prefix to strip
    IncludePaths          []string    // ["/*"] default - paths requiring auth
    ExcludePaths          []string    // [] default - paths bypassing auth
    ShutdownTimeoutSecs   int         // 10 default
    HealthCheck           HealthCheck
}

type HealthCheck struct {
    Path   string  // "/healthz" default
    Target string  // Empty = local 200 OK, Set = proxy to downstream
}

type SecurityConfig struct {
    RateLimit       RateLimitConfig
    ApiKeyRateLimit ApiKeyRateLimitConfig
    JwtRateLimit    JwtRateLimitConfig
    MaxBodyBytes    int64           // 1 MiB default
}

type RateLimitConfig struct {
    Enabled         bool  // false default
    RequestsPerMin  int   // 60 default
    BanForMin       int   // 5 default
    ThrottleDelayMs int   // 0 default (disabled)
    MaxDelaySlots   int   // 100 default
}

type ApiKeyRateLimitConfig struct {
    Enabled         bool               // false default
    RequestsPerMin  int                // 60 default
    BanForMin       int                // 5 default
    IncludeIP       bool               // false default — prefix key with client IP
    KeyHeader       string             // "x-goog-api-key" default
    Match           []MatchRule        // Request matching rules (OR between rules, AND within)
    ThrottleDelayMs int                // 0 default
    MaxDelaySlots   int                // 100 default
}

type JwtRateLimitConfig struct {
    Enabled         bool  // false default
    RequestsPerMin  int   // 60 default
    BanForMin       int   // 5 default
    IncludeIP       bool  // false default — prefix key with client IP
    ThrottleDelayMs int   // 0 default
    MaxDelaySlots   int   // 100 default
}

type MatchRule struct {
    Host   string  // exact or /regex/
    Path   string  // exact or /regex/
    Header string  // header name that must be present
}

type AuthConfig struct {
    HeaderPrefix string        // "X-AUTH-" default
    JWT          JWTConfig
    APIKey       APIKeyConfig
}

// JWTConfig is shared by both [auth.jwt] and [admin.jwt].
// AllowedEmails is only enforced when non-empty; an empty slice means no
// email restriction. At least one of AllowedEmails or Filters must be
// non-empty when used for admin.jwt.
type JWTConfig struct {
    Enabled       bool                // false default
    Issuer        string              // Required if enabled
    Audience      string              // Required if enabled
    ToleranceSecs int                 // 30 default
    CacheTTLMins  int                 // 1440 (24h) default
    Filters       map[string]string   // Claim validation rules (exact or /regex/)
    Mappings      map[string]string   // Claim→Header mappings (auth.jwt only)
    AllowedEmails []string            // Optional email allowlist; empty = no restriction
}

type APIKeyConfig struct {
    Enabled bool              // false default
    Name    string            // "X-API-KEY" default
    Value   string            // Required if enabled
    Payload map[string]string // Static headers to inject
}

// AdminConfig controls the admin control-plane API.
// JWT uses the shared JWTConfig structure (same fields as auth.jwt).
// Requires: admin.jwt.issuer, admin.jwt.audience, and at least one of
// admin.jwt.allowed_emails or admin.jwt.filters.
type AdminConfig struct {
    Enabled bool      // false default
    JWT     JWTConfig
}
```

### Environment Variable Substitution
Pattern: `{{ENV.VARIABLE_NAME}}` in TOML file
Regex: `\{\{ENV\.([A-Z_][A-Z0-9_]*)\}\}`
Processing: Pre-parse string replacement before TOML unmarshaling
Missing vars: Placeholder remains unchanged (may cause validation error)

### Environment Variable Overrides
Prefix: `PROXY_`
Format: `PROXY_{SECTION}_{SUBSECTION}_{FIELD}` (uppercase, underscores)
Examples:
- `PROXY_SERVER_PORT=9090` → config.Server.Port
- `PROXY_AUTH_JWT_ENABLED=true` → config.Auth.JWT.Enabled
- `PROXY_AUTH_API_KEY_VALUE=secret` → config.Auth.APIKey.Value

Precedence: Env overrides > TOML (after substitution) > Defaults

### Config Validation Rules
- `auth.jwt.enabled = true` requires `issuer` and `audience`
- `auth.api_key.enabled = true` requires `value`
- Both `auth.jwt` and `auth.api_key` disabled is **valid** — proxy operates in rate-limit-only mode (no credential checks)
- `admin.enabled = true` requires `admin.jwt.issuer`, `admin.jwt.audience`, and at least one of `admin.jwt.allowed_emails` or `admin.jwt.filters`
- `server.port` must be 1–65535

### GOOGLE_CLOUD_PROJECT Auto-Detection
Sources (in order):
1. `GOOGLE_CLOUD_PROJECT` env var
2. GCP Metadata Server: `http://metadata.google.internal/computeMetadata/v1/project/project-id` (Header: `Metadata-Flavor: Google`, 1s timeout)

Used for: JWT issuer/audience string replacement

## AUTHENTICATION_ARCHITECTURE

### Request Processing Pipeline (middleware chain)
```
HTTP Request
  ↓
[Admin mux — registered only when admin.enabled = true]
  ├─ POST /admin/control → AdminAuthMiddleware → ControlHandler
  └─ GET  /admin/status  → AdminAuthMiddleware → StatusHandler
  ↓
1. RequestLogger (log method, path, start time)
  ↓
2. BodyLimiter (reject body > max_body_bytes with 413)
  ↓
3. HeaderSanitizer (remove incoming X-AUTH-* headers)
  ↓
4. PathFilter (check include/exclude patterns, set context flag)
  ↓
5. DynamicRuleCheck (admin throttle/block/allow rules; no-op if admin disabled)
  ↓
6. ApiKeyRateLimit (per-API-key rate limiting with configurable request matching)
  ↓
7. JwtRateLimit (per-JWT sub claim rate limiting)
  ↓
8. IpRateLimit (per-IP sliding window, ban enforcement)
  ↓
9. ServeHTTP (main auth handler)
   ↓
   ├─ Health Check? → handleHealth() → return
   ├─ Auth Required? (from context)
   │   ↓ NO → Forward to proxy
   │   ↓ YES
   │   ├─ Both JWT and API-Key disabled? (rate-limit-only mode)
   │   │   ↓ YES → Forward to proxy (no credential check)
   │   │   ↓ NO
   │   ├─ JWT Auth Enabled + Bearer Token Present?
   │   │   ↓ YES
   │   │   ├─ ValidateToken() → Claims | Error
   │   │   ├─ EvaluateFilters(claims, config.JWT.Filters) → Pass | Fail
   │   │   ├─ AllowedEmails non-empty? → check email claim | skip
   │   │   ├─ limiter.Allow(hash(IP, sub)) → Pass | 429
   │   │   ├─ MapClaims(claims, config.JWT.Mappings) → Headers
   │   │   ├─ Inject headers into request
   │   │   └─ Forward to proxy
   │   │   ↓ NO (JWT not enabled or bearer token absent)
   │   └─ API-Key Auth Enabled?
   │       ↓ YES
   │       ├─ ValidateAPIKey(request) → Headers | Error
   │       ├─ Inject headers into request
   │       └─ Forward to proxy
   │       ↓ NO
   │       └─ 401 Unauthorized
  ↓
9. Reverse Proxy (httputil.ReverseProxy)
   ├─ Rewrite URL (strip prefix if configured)
   ├─ Forward to config.Server.TargetURL
   └─ Return response to client
```

### Admin Auth Middleware (internal/admin/auth.go)
AdminAuthMiddleware(validator, allowedEmails, filters) — applied to `/admin/*` routes when `admin.enabled = true`.

```
Bearer token extracted from Authorization header
  ↓
ValidateToken() → Claims | 401
  ↓
len(filters) > 0? → EvaluateFilters(claims, filters) → Pass | 401
  ↓
len(allowedEmails) > 0? → check email claim in allowedEmails | 401
  ↓
Next handler
```

Access control rules:
- `filters` and `allowedEmails` are evaluated independently
- Both are **optional**: an empty map / nil slice means that check is skipped entirely
- When both are set, **all** conditions must pass (AND logic)
- Config validation requires at least one of `filters` or `allowed_emails` to be non-empty when `admin.enabled = true`

### Admin Control Plane (internal/admin/handler.go)

**POST /admin/control** — manage dynamic rules and rate limiter settings at runtime.

Rule JSON fields:

| Field | Type | Description |
|---|---|---|
| `ruleId` | string | Unique rule identifier (required) |
| `targetHost` | string | Hostname this rule applies to (required) |
| `action` | string | `throttle`, `block`, or `allow` (required) |
| `maxRPM` | int | Max requests per minute — required for `throttle` |
| `limiter` | string | Target rate limiter: `ip`, `apikey`, or `jwt` |
| `throttleDelayMs` | int | Set throttle delay on the targeted limiter (ms); `0` = no change |
| `maxDelaySlots` | int | Set max concurrent throttled responses on the targeted limiter; `0` = no change |
| `pathPattern` | string | Optional path pattern filter for the rule |
| `durationSeconds` | int | Rule active duration in seconds (required) |

When `action = "throttle"` and `limiter` is set:
1. `limiter.SetRequestsPerMin(maxRPM)` — update RPM
2. If `throttleDelayMs > 0`: `limiter.SetThrottleDelay(throttleDelayMs * ms)` — enable/update delay
3. If `maxDelaySlots > 0`: `limiter.SetMaxDelaySlots(maxDelaySlots)` — resize semaphore
4. `limiter.Enable()` — ensure limiter is active

**GET /admin/status** — returns all active rules and rate limiter snapshots:
```json
{
  "rules": [...],
  "rateLimiters": {
    "ip":     {"name": "ip", "enabled": true, "requestsPerMin": 60, "activeEntries": 3},
    "apikey": {"name": "apikey", "enabled": false, "requestsPerMin": 60, "activeEntries": 0},
    "jwt":    {"name": "jwt", "enabled": true, "requestsPerMin": 30, "activeEntries": 12,
               "throttleDelay": "200ms", "maxDelaySlots": 50}
  }
}
```

### JWT Validation Logic (internal/auth/jwt/validator.go)

#### ValidateToken(tokenString) → (Claims, error)
1. Split token by '.' → [header, payload, signature] (3 parts required)
2. Base64 decode header → parse JSON → extract `alg`, `kid`, `typ`
3. Reject symmetric algorithms (HS256, HS384, HS512) - only RS*/ES*/EdDSA allowed
4. Validate `kid` is present
5. Base64 decode payload → parse JSON → Claims map
6. Validate standard claims:
   - `exp` (expiration): now ≤ exp + clockTolerance
   - `nbf` (not before): now ≥ nbf - clockTolerance
   - `iss` (issuer): exact match with config.Issuer
   - `aud` (audience): string exact match OR in array
7. Fetch public key from JWKS cache (kid, issuer)
8. Verify signature:
   - Signing input: `base64url(header).base64url(payload)`
   - Decode signature (base64url)
   - Hash algorithm: SHA256/384/512 based on alg
   - Verify with public key (RSA-PSS, RSA-PKCS1, ECDSA, EdDSA)
9. Return Claims map

Clock tolerance applies to exp/nbf only (default 30s)

#### JWKS Fetching (internal/auth/jwt/jwks.go)
1. Fetch `.well-known/openid-configuration` from issuer
2. Parse JSON → extract `jwks_uri`
3. Fetch JWKS from `jwks_uri`
4. Parse JSON → extract `keys` array
5. For each key: convert JWK to crypto.PublicKey (RSA/ECDSA/EdDSA)
6. Cache keys by `kid` with TTL (default 24h)
7. Automatic expiration and re-fetch on cache miss

KeyCache structure:
- In-memory map[kid]cacheEntry
- cacheEntry: {publicKey, expiresAt}
- Thread-safe with sync.RWMutex
- Cleanup on access (lazy expiration)

#### Claim Filtering (internal/auth/jwt/filter.go)
EvaluateFilters(claims Claims, filters map[string]string) error

For each filter key-value:
1. Extract claim value from claims map
2. If claim missing → return error
3. Match logic:
   - Exact match: filter value == claim value (string comparison)
   - Regex match: filter value starts/ends with '/' → compile regex → test claim
   - Array claims: OR logic - at least one element must match
4. Type coercion: numbers → strings for comparison
5. All filters must pass (AND logic)

Filter syntax:
- `"exact-value"` → exact string match
- `"/regex-pattern/"` → regex match (Go regexp syntax)

Used by both `auth.jwt` (via proxy handler) and `admin.jwt` (via AdminAuthMiddleware).

Example:
```toml
[auth.jwt.filters]
email_verified = "true"          # Exact match
email = "/.*@example\\.com$/"   # Regex match
roles = "admin"                  # Array: passes if "admin" in roles[]

[admin.jwt.filters]
hd = "yourcompany.com"          # Restrict admin to a Google Workspace domain
```

#### Claim Mapping (internal/auth/jwt/mapper.go)
MapClaims(claims Claims, mappings map[string]string, headerPrefix string) map[string]string

For each mapping claimKey→headerSuffix:
1. Extract claim value from claims map
2. If claim missing → skip (no error)
3. Type coercion:
   - String/Number → string value
   - Array → comma-separated string (CSV)
   - Object → JSON string
4. Generate header name: `{headerPrefix}{UPPER(headerSuffix)}`
5. Return map of header name → value

Example:
```toml
[auth]
header_prefix = "X-AUTH-"

[auth.jwt.mappings]
email = "USER-EMAIL"    # X-AUTH-USER-EMAIL: user@example.com
sub = "USER-ID"         # X-AUTH-USER-ID: 1234567890
roles = "USER-ROLES"    # X-AUTH-USER-ROLES: admin,user,viewer
```

### API-Key Validation (internal/auth/apikey/apikey.go)

#### ValidateAPIKey(request, authConfig) → (map[string]string, error)
1. Extract header value: request.Header.Get(config.Name)
2. If header missing → return ErrMissingAPIKey
3. Constant-time comparison: subtle.ConstantTimeCompare(expected, actual)
4. If mismatch → return ErrInvalidAPIKey
5. Generate headers from Payload:
   - For each payloadKey→value in Payload
   - Header name: `{headerPrefix}{UPPER(payloadKey)}`
   - Header value: payload value (static string)
6. Return header map

Security: Uses crypto/subtle.ConstantTimeCompare to prevent timing attacks

Example:
```toml
[auth.api_key]
enabled = true
name = "X-API-KEY"
value = "{{ENV.API_KEY_SECRET}}"

[auth.api_key.payload]
service = "internal"   # X-AUTH-SERVICE: internal
source = "backend"     # X-AUTH-SOURCE: backend
```

## RATE_LIMITING (internal/ratelimit/)

### Architecture: Three Independent Limiters
Rate limiting is handled by three purpose-specific middlewares, each backed by an independent `RateLimiter` instance:

| Middleware | Config section | Key composition | Scope |
|---|---|---|---|
| `IpRateLimit` | `[security.rate_limit]` | `ClientIP(r)` | All requests — DDoS protection layer |
| `ApiKeyRateLimit` | `[security.apikey_rate_limit]` | `"k:" + hash(key)` or `ClientIP(r)` | Only matching requests (configurable rules) |
| `JwtRateLimit` | `[security.jwt_rate_limit]` | `"s:" + sub` | Only requests with a Bearer JWT |

All three run **before** authentication. `JwtRateLimit` extracts the `sub` claim via a non-validating JWT parse so rate limiting happens before the expensive signature verification.

### RateLimiter Core (internal/ratelimit/limiter.go)
Algorithm: **Sliding window** per key with temporary bans.

```go
type RateLimiterConfig struct {
    Name           string
    Enabled        bool
    RequestsPerMin int
    BanDuration    time.Duration
    ThrottleDelay  time.Duration // 0 = no delay (default)
    MaxDelaySlots  int           // semaphore cap; default 100
}
```

**Allow(key string) → (allowed bool, retryAfterSeconds int)**
1. If not enabled or limit ≤ 0 → return true
2. Get/create entry for key
3. If key is currently banned → return false + retry-after
4. If window expired (≥60s since windowStart) → reset count, update windowStart
5. Increment count; if count > limit → set ban, return false + retry-after
6. Return true

Cleanup: periodic (every 60s), removes entries idle for > 2 minutes and no active ban.

### Key Composition

**IpRateLimit:**
- Key: `ClientIP(r)` — raw IP from `RemoteAddr` (no X-Forwarded-For processing)

**ApiKeyRateLimit:**
- Key source (in priority order):
  1. `key_header` header value (default `x-goog-api-key`) → `"k:" + hash(value)`
  2. `key` query parameter → `"k:" + hash(value)`
  3. No key found → `ClientIP(r)` (fallback)
- If `include_ip = true`: prefix key with `ClientIP(r) + ":"`
- Hash function: SHA256(raw) → first 96 bits → 16-char base64url string
- Request matching: only applies to requests where at least one `[[match]]` rule is satisfied (OR between rules, AND within a rule). If no rules configured → middleware is a passthrough.

**JwtRateLimit:**
- Key: `"s:" + sub` where `sub` is the JWT `sub` claim (non-validating parse)
- If no Bearer token or no `sub` claim → passthrough
- If `include_ip = true`: prefix key with `ClientIP(r) + ":"`

### Throttle Delay (DDoS-Safe Backpressure)
When `throttle_delay_ms > 0`, rate-limited responses are delayed before returning 429.
A bounded semaphore (`delaySem chan struct{}`) limits concurrent sleeping goroutines to `max_delay_slots` (default 100). If all slots are occupied (DDoS scenario), the request returns 429 immediately without sleeping.

```go
// TryAcquireDelaySlot returns (acquired, semaphore).
// Caller must pass the returned chan to ReleaseDelaySlot to handle
// concurrent SetMaxDelaySlots calls correctly.
if acquired, sem := limiter.TryAcquireDelaySlot(); acquired {
    defer limiter.ReleaseDelaySlot(sem)
    time.Sleep(limiter.ThrottleDelay())
}
```

### Admin Runtime Methods
All methods are thread-safe:

| Method | Description |
|---|---|
| `SetRequestsPerMin(rpm int)` | Update RPM limit |
| `SetThrottleDelay(d time.Duration)` | Enable/disable/update throttle delay; `0` = disable |
| `SetMaxDelaySlots(n int)` | Resize the delay semaphore (takes effect immediately) |
| `Enable()` / `Disable()` | Toggle rate limiting on/off |
| `GetStatus() *RateLimiterStatus` | Snapshot for admin status endpoint |

```go
type RateLimiterStatus struct {
    Name           string `json:"name"`
    Enabled        bool   `json:"enabled"`
    RequestsPerMin int    `json:"requestsPerMin"`
    ActiveEntries  int    `json:"activeEntries"`
    ThrottleDelay  string `json:"throttleDelay,omitempty"` // e.g. "100ms"
    MaxDelaySlots  int    `json:"maxDelaySlots,omitempty"`
}
```

Response on rate limit:
- HTTP 429 Too Many Requests
- Header: `Retry-After: <seconds>`
- JSON: `{"error":"rate_limited","message":"too many requests","retry_after":123}`

### ApiKeyRateLimit Request Matching (internal/ratelimit/matcher.go)
`RequestMatcher` evaluates `[[security.apikey_rate_limit.match]]` rules.

```go
type RequestMatchRule struct {
    Host   string // exact string or /regex/
    Path   string // exact string or /regex/
    Header string // header name that must be present
}
```

- Multiple rules → **OR** logic (any match = matched)
- Fields within a rule → **AND** logic (all non-empty fields must match)
- Empty matcher (no rules) → `Matches()` returns false (middleware is a no-op)
- Host/Path: `/pattern/` syntax → regex; bare string → exact match

**Example (Vertex AI endpoints):**
```toml
[[security.apikey_rate_limit.match]]
host = "/.*-aiplatform\\.googleapis\\.com/"

[[security.apikey_rate_limit.match]]
path = "/\\/v1\\/projects\\/.*\\/(endpoints|publishers|models)\\//"
```

## HTTP_REVERSE_PROXY (internal/proxy/proxy.go)

### Proxy Configuration
- Uses net/http/httputil.ReverseProxy
- Director function: rewrites request before forwarding
- ErrorHandler: returns 502 Bad Gateway JSON on upstream errors

### URL Rewriting
If config.Server.StripPrefix is set:
1. Check if request.URL.Path starts with prefix
2. Remove prefix: `request.URL.Path = strings.TrimPrefix(path, prefix)`
3. Forward to target with modified path

Example: StripPrefix="/api" → `/api/users` becomes `/users`

### Health Check Handling
Path: config.Server.HealthCheck.Path (default "/healthz")
1. If Target is empty:
   - Return 200 OK
   - JSON: `{"status":"ok"}`
2. If Target is set:
   - Create separate ReverseProxy for health endpoint
   - Forward to Target URL
   - Return downstream response

Always bypasses authentication (in ExcludePaths by default)

## MIDDLEWARE_SYSTEM (internal/proxy/middleware.go)

### Middleware Type
```go
type Middleware func(http.Handler) http.Handler
```

### PathFilter Middleware
1. Parse include/exclude patterns (glob matching with path.Match)
2. For each request:
   - Check exclude patterns first → if match, set authRequired=false
   - Check include patterns → if match, set authRequired=true
   - Store in context: context.WithValue(ctx, authRequiredKey, bool)
3. Call next handler

### HeaderSanitizer Middleware
1. Get header prefix from config (default "X-AUTH-")
2. For each request:
   - Iterate request headers
   - Delete any header starting with prefix (case-insensitive)
   - Purpose: prevent header injection attacks
3. Call next handler

### IpRateLimit Middleware
Applies per-IP rate limiting to **all** requests regardless of auth status. Provides the primary DDoS protection layer.

1. Extract `ClientIP(r)` from `RemoteAddr` (no X-Forwarded-For processing)
2. Call `ipLimiter.Allow(ip)` → if not allowed, call `handleRateLimited` → 429
3. If allowed: call next handler

**Note:** ClientIP extraction uses RemoteAddr only. If deployed behind a load balancer, the proxy must set RemoteAddr via PROXY protocol or similar.

### ApiKeyRateLimit Middleware
Applies per-API-key rate limiting **only to requests matching configured rules** (`[[security.apikey_rate_limit.match]]`). Passthrough if no rules match.

1. Check `matcher.Matches(r)` — if false, passthrough
2. Extract API key from `key_header` header or `key` query param
3. Compute rate-limit key (`"k:" + hash(key)` or `ClientIP(r)` fallback)
4. If `include_ip = true`: prefix with `ClientIP(r) + ":"`
5. Call `apikeyLimiter.Allow(key)` → if not allowed, call `handleRateLimited` → 429

### JwtRateLimit Middleware
Applies per-JWT-identity rate limiting using the `sub` claim. Runs **before** JWT validation to avoid wasting resources on expensive signature checks.

1. Non-validating parse of Bearer token → extract `sub` claim
2. If no Bearer token or no `sub` → passthrough
3. Compute rate-limit key `"s:" + sub`; if `include_ip = true`, prefix with `ClientIP(r) + ":"`
4. Call `jwtLimiter.Allow(key)` → if not allowed, call `handleRateLimited` → 429

### handleRateLimited
```go
func handleRateLimited(w http.ResponseWriter, retryAfter int, limiter *ratelimit.RateLimiter) {
    if acquired, sem := limiter.TryAcquireDelaySlot(); acquired {
        defer limiter.ReleaseDelaySlot(sem)
        time.Sleep(limiter.ThrottleDelay())
    }
    writeRateLimitResponse(w, retryAfter)
}
```
The semaphore channel is returned from `TryAcquireDelaySlot` and passed to `ReleaseDelaySlot` to ensure the release targets the correct channel even if `SetMaxDelaySlots` replaces it at runtime.

### RequestLogger Middleware
Logs every request with:
- method, path, remote_addr
- duration_ms (computed at end)
- status (via ResponseWriter wrapper)
- error (if any)

Uses structured logging (slog) with configurable level

## LOGGING_SYSTEM (internal/logging/logger.go)

### InitLogger(mode, level) → *slog.Logger

Mode (from LOG_MODE env var):
- "development": Text format, colored output, source location
- "production": JSON format, no color, cloud logging compatible

Level (from LOG_LEVEL env var):
- "debug", "info", "warn", "error"
- Default: "info"

### Structured Fields
All logs include contextual fields:
- timestamp (RFC3339)
- severity (level)
- message
- Custom fields: request_id, method, path, status, duration_ms, error

### Google Cloud Logging Compatibility
Production mode outputs JSON with Cloud Logging fields:
- `severity`: Mapped from log level
- `message`: Log message
- Additional fields as JSON properties

## ERROR_HANDLING

### HTTP Error Responses
All errors return JSON with consistent structure:
```json
{
  "error": "error_code",
  "message": "human readable message",
  "retry_after": 123  // Optional: for 429 responses
}
```

Error codes:
- `unauthorized`: 401 (auth failure, invalid token, missing credentials)
- `rate_limited`: 429 (rate limit exceeded)
- `bad_gateway`: 502 (upstream error, JWKS fetch failure)

### Internal Error Handling
- Config loading errors: Log and exit(1)
- JWT validation errors: Return 401 with error message
- JWKS fetch errors: Return 502 (transient) or 401 (invalid config)
- Rate limit exceeded: Return 429 with retry_after
- Proxy errors: Return 502

## PERFORMANCE_CHARACTERISTICS

### Startup Time
- Binary execution: <10ms
- Config loading: ~5ms
- Logger init: ~2ms
- HTTP server start: ~5ms
- **Total: <50ms** (cold start)

### Memory Footprint
- Binary: ~10MB
- Runtime heap: ~15MB
- JWKS cache: ~100KB per issuer
- Rate limiter: ~100 bytes per IP
- **Total: <32MB** typical

### Request Latency
- Path filtering: <0.1ms
- Rate limiting: ~0.2ms
- JWT validation: ~2-5ms (signature verification)
- Header manipulation: <0.1ms
- Proxy overhead: ~1ms
- **Total added latency: 3-6ms** (excluding backend)

### Throughput
- Single core: 10,000+ req/s (with JWT validation)
- CPU-bound: JWT signature verification
- Memory-bound: JWKS cache size
- Network-bound: Backend response time

## SECURITY_CONSIDERATIONS

### Authentication Security
- Only asymmetric JWT algorithms (RS*, ES*, EdDSA)
- Constant-time API key comparison (timing attack prevention)
- Header sanitization (injection attack prevention)
- Clock tolerance for exp/nbf (prevents clock skew issues)

### JWKS Security
- HTTPS-only JWKS fetching
- Cache TTL limits (default 24h, prevents stale keys)
- Key ID validation (kid must be present)
- Signature verification before claim processing

### Rate Limiting Security
- Per-IP tracking (prevents single-source abuse)
- Temporary bans (exponential backoff-like behavior)
- Cleanup mechanism (prevents memory exhaustion)

### Container Security
- Distroless base (minimal attack surface)
- Non-root execution (privilege separation)
- Read-only filesystem support
- No shell or package manager

### Configuration Security
- Environment variable substitution (no secrets in files)
- Secret Manager integration (optional)
- TOML config validation (prevents misconfigurations)

## DEPLOYMENT_PATTERNS

### Standalone Server
- Single binary execution
- TOML config file
- Environment variables for runtime overrides
- Systemd service (optional)

### Docker Container
- Multi-stage build for minimal size
- Environment variable configuration
- Volume-mounted config (optional)
- Health check endpoint for orchestration

### Google Cloud Run
- Sidecar pattern with application container
- Environment variables from Cloud Run
- Secrets from Secret Manager
- Auto-scaling based on request load
- VPC connector for private backends
- Cloud Logging integration (LOG_MODE=production)

### Kubernetes
- Deployment with multiple replicas
- ConfigMap for config.toml
- Secret for sensitive values
- Service for load balancing
- Ingress for external access
- HPA for auto-scaling

## ENVIRONMENT_VARIABLES

### Configuration Overrides (PROXY_* prefix)
All TOML fields overridable with `PROXY_{SECTION}_{FIELD}` format

### Substitution Variables ({{ENV.*}})
- `GOOGLE_CLOUD_PROJECT`: Automatically detected from environment or GCP metadata server
- `API_KEY_SECRET`: API key expected value
- Custom variables: any `[A-Z_][A-Z0-9_]*` pattern

### Logging Configuration
- `LOG_MODE`: development | production
- `LOG_LEVEL`: debug | info | warn | error

### Cloud Run Detection
- `K_SERVICE`: Cloud Run service name (auto-set)
- `K_REVISION`: Cloud Run revision (auto-set)
- `K_CONFIGURATION`: Cloud Run configuration (auto-set)

## OPERATIONAL_METRICS

### Key Metrics to Monitor
1. Request count (total, per endpoint)
2. Request latency (P50, P95, P99)
3. Error rate (4xx, 5xx)
4. JWT validation failures
5. Rate limit hits
6. Upstream errors (502 count)
7. Memory usage
8. CPU usage
9. Startup time
10. JWKS fetch failures

### Health Check
- Endpoint: /healthz (configurable)
- Always bypasses authentication
- Returns 200 OK with `{"status":"ok"}` (local mode)
- Or proxies to downstream health endpoint

### Logging
- Request logging: method, path, status, duration
- Error logging: auth failures, validation errors, upstream errors
- Startup logging: config summary, version, mode
- Structured JSON (production) or text (development)

## API_ENDPOINTS

### Health Check
- `GET /healthz` (default, configurable)
- No authentication
- Response: 200 OK `{"status":"ok"}`

### Proxied Endpoints
- All other paths
- Authentication required (unless in exclude_paths)
- JWT: `Authorization: Bearer <token>`
- API-Key: Custom header (default `X-API-KEY`)
- Forwarded to target_url with optional prefix stripping

## CONFIGURATION_EXAMPLES

### Example 1: Minimal JWT Authentication

```toml
[server]
port = 8888
target_url = "http://localhost:8080"

[auth.jwt]
enabled = true
issuer = "https://accounts.google.com"
audience = "my-app-id"
```

**CLI Equivalent:**
```bash
export PROXY_AUTH_JWT_ENABLED=true
export PROXY_AUTH_JWT_ISSUER=https://accounts.google.com
export PROXY_AUTH_JWT_AUDIENCE=my-app-id
./bin/lite-auth-proxy
```

### Example 2: API-Key Authentication Only

```toml
[server]
port = 8888
target_url = "http://localhost:8080"

[auth.api_key]
enabled = true
name = "X-API-KEY"
value = "{{ENV.API_KEY_SECRET}}"

[auth.api_key.payload]
service = "internal"
source = "backend-job"
```

**CLI Equivalent:**
```bash
export PROXY_AUTH_JWT_ENABLED=false
export PROXY_AUTH_API_KEY_ENABLED=true
export API_KEY_SECRET="your-secret-key"
./bin/lite-auth-proxy
```

### Example 3: Dual Authentication with Rate Limiting

```toml
[server]
port = 8888
target_url = "http://backend:8080"
strip_prefix = "/api"
include_paths = ["/*"]
exclude_paths = ["/healthz", "/metrics"]

[server.health_check]
path = "/healthz"
target = "http://backend:8080/health"

[security.rate_limit]
enabled = true
requests_per_min = 100
ban_for_min = 10

[auth]
header_prefix = "X-AUTH-"

[auth.jwt]
enabled = true
issuer = "https://securetoken.google.com/{{ENV.GOOGLE_CLOUD_PROJECT}}"
audience = "{{ENV.GOOGLE_CLOUD_PROJECT}}"
tolerance_secs = 30
cache_ttl_mins = 1440

[auth.jwt.filters]
email_verified = "true"
email = "/.*@company\\.com$/"

[auth.jwt.mappings]
email = "USER-EMAIL"
sub = "USER-ID"
roles = "USER-ROLES"

[auth.api_key]
enabled = true
name = "X-API-KEY"
value = "{{ENV.API_KEY_SECRET}}"

[auth.api_key.payload]
service = "internal"
```

**CLI Equivalent:**
```bash
# JWT Configuration
export PROXY_AUTH_JWT_ENABLED=true
export PROXY_AUTH_JWT_ISSUER=https://securetoken.google.com/my-project
export PROXY_AUTH_JWT_AUDIENCE=my-project
export PROXY_AUTH_JWT_FILTERS_EMAIL_VERIFIED=true
export PROXY_AUTH_JWT_FILTERS_EMAIL="/.*@company\\\\.com$/"
export PROXY_AUTH_JWT_MAPPINGS_EMAIL=USER-EMAIL
export PROXY_AUTH_JWT_MAPPINGS_SUB=USER-ID
export PROXY_AUTH_JWT_MAPPINGS_ROLES=USER-ROLES

# API-Key Configuration
export PROXY_AUTH_API_KEY_ENABLED=true
export API_KEY_SECRET="your-secret-key"
export PROXY_AUTH_API_KEY_PAYLOAD_SERVICE=internal

# Server Configuration
export PROXY_SERVER_STRIP_PREFIX=/api
export PROXY_SECURITY_RATE_LIMIT_ENABLED=true
export PROXY_SECURITY_RATE_LIMIT_REQUESTS_PER_MIN=100
export PROXY_SECURITY_RATE_LIMIT_BAN_FOR_MIN=10

./bin/lite-auth-proxy
```

### Example 4: Rate-Limit-Only Mode (no authentication)

```toml
[server]
port = 8888
target_url = "http://localhost:8080"

[security.rate_limit]
enabled = true
requests_per_min = 60
ban_for_min = 5

[auth.jwt]
enabled = false

[auth.api_key]
enabled = false
```

All requests on included paths are forwarded without credential checks.
Rate limiting and DDoS hardening remain fully active.

### Example 5: Admin Control Plane (domain-based access)

```toml
[admin]
enabled = true

[admin.jwt]
issuer = "https://accounts.google.com"
audience = "https://my-proxy.run.app"

[admin.jwt.filters]
hd = "yourcompany.com"   # Any user in the Google Workspace domain
```

### Example 6: Admin Control Plane (specific service accounts)

```toml
[admin]
enabled = true

[admin.jwt]
issuer = "https://accounts.google.com"
audience = "https://my-proxy.run.app"
allowed_emails = [
  "deploy-sa@my-project.iam.gserviceaccount.com",
  "ops-sa@my-project.iam.gserviceaccount.com",
]
```

### Example 7: auth.jwt with AllowedEmails

```toml
[auth.jwt]
enabled = true
issuer = "https://accounts.google.com"
audience = "my-app-id"
allowed_emails = ["alice@example.com", "bob@example.com"]
```

Only tokens whose `email` claim exactly matches a listed address are accepted.
`filters` and `allowed_emails` are independent; both can be set simultaneously (AND logic).

## DEPENDENCIES
- **github.com/BurntSushi/toml** v1.6.0: TOML parsing
- **Standard library**: net/http, crypto/*, encoding/*, log/slog, context, sync

No external dependencies for runtime (JWT, HTTP, crypto all stdlib)

---
END_OF_SPECIFICATION
