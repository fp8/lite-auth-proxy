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
- **Rate Limiting**: Per-IP sliding window with configurable limits and bans
- **Header Injection**: Inject authentication context as headers to downstream services
- **Header Sanitization**: Prevent header injection attacks by removing incoming auth headers
- **Health Checks**: Local or proxied health endpoints for orchestration compatibility
- **Admin Control Plane**: Runtime throttle/block/allow rules via `/admin/control` authenticated with GCP identity tokens

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
    RateLimit    RateLimitConfig
    MaxBodyBytes int64           // 1 MiB default
}

type RateLimitConfig struct {
    Enabled        bool  // false default
    RequestsPerMin int   // 60 default
    BanForMin      int   // 5 default
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
6. VertexAIRateLimit (per-caller AI bucket; no-op if admin disabled)
  ↓
7. RateLimiter (per-IP sliding window, ban enforcement)
  ↓
8. ServeHTTP (main auth handler)
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

## RATE_LIMITING (internal/ratelimit/limiter.go)

### Algorithm: Sliding Window with Temporary Bans
Per-key tracking with:
- Window size: 1 minute (fixed)
- Request limit: config.RequestsPerMin
- Ban duration: config.BanForMin
- Key composition: 
  - For authenticated requests (JWT): hashed `IPv4+ ":" + sub_claim` (SHA256)
  - For API-Key authenticated requests: IP address only
  - For non-authenticated requests: IP address only

### Rate Limit Key Selection
For corporate scenarios where multiple users share the same IP:
- **JWT-authenticated requests**: Rate limit key is based on both IP and JWT `sub` (subject) claim
  - Flow: IP + sub → SHA256 hash → 43-char base64url string key
  - Allows: Different JWT users to have independent rate limits
  - Example: Users A and B from same IP (e.g., corporate network) can each make RequestsPerMin requests
  - Requires: JWT validation to succeed; rate limiting applied in handler after validation
- **API-Key authenticated requests**: Rate limit key is IP-only (corporate service assumed to have stable IP)
  - Flow: IP → rate limit check
  - Example: Service at 10.0.0.5 limited to RequestsPerMin requests total
- **Non-authenticated requests** (excluded paths, health checks): Rate limit key is IP-only
  - Applied at middleware level before authentication
  - Example: /healthz endpoint rate-limited by source IP

### Allow(key string) → (allowed bool, retryAfterSeconds int)
1. If not enabled or limit ≤ 0 → return true
2. Get/create entry for key
3. Check if key is currently banned → return false + retry-after
4. If window expired (≥60s since windowStart) → reset count, update windowStart
5. Increment count
6. If count > limit:
   - Set bannedUntil = now + banDuration
   - Return false + retry-after (rounded up to seconds)
7. Return true

Cleanup:
- Periodic (every 60s)
- Remove entries: bannedUntil passed AND lastSeen > 2*window ago
- Prevents memory leak from stale keys

### Memory Efficiency
- JWT sub claim values can be arbitrarily long; hashing prevents unbounded memory growth
- Hash function: SHA256(IP + ":" + sub_claim) → 32-byte output → 43-char base64url string
- Base64url encoding chosen over hex (33% shorter: 43 vs 64 chars)
- Key map size bounded by: (Number of IPs) × (Number of unique users per IP)

Response on rate limit:
- HTTP 429 Too Many Requests
- Header: `Retry-After: <seconds>`
- JSON: `{"error":"rate_limited","message":"too many requests","retry_after":123}`

### Example Rate Limit Scenarios
**Scenario 1: Corporate Network with JWT**
```
Config: rate_limit.requests_per_min = 100

Corporate IP 10.0.0.0/24, 50 users, each with unique JWT sub

User alice@corp.com (sub=alice):
  - Rate limit key: sha256("10.0.0.5:alice")
  - Limit: 100 req/min per alice (across all sessions)

User bob@corp.com (sub=bob):
  - Rate limit key: sha256("10.0.0.5:bob")
  - Limit: 100 req/min per bob (across all sessions)

Result: Both can make 100 req/min simultaneously, 50+ users × 100 req/min = 5000+ req/min total
```

**Scenario 2: Corporate Network with API Key**
```
Config: rate_limit.requests_per_min = 10000

Corporate IP 10.0.0.0/24, single API key for all services:

All requests from corporate network:
  - Rate limit key: "10.0.0.5" (IP only)
  - Limit: 10000 req/min for entire corporate network

Result: Total corporate traffic limited to 10000 req/min (no per-user distinction)
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

### RateLimiter Middleware
Applies rate limiting to non-authenticated requests (excluded paths, health checks).
For authenticated requests, rate limiting is deferred to the handler after auth validation.

For non-authenticated requests:
1. Extract client IP from RemoteAddr
2. Call limiter.Allow(ip) with IP-only key
3. If not allowed:
   - Return 429 JSON with retry_after
   - Don't call next handler
4. If allowed: call next handler

**Note:** lite-auth-proxy is designed for direct exposure without upstream proxies. ClientIP extraction uses RemoteAddr only and does not process X-Forwarded-For headers. If deployed behind a reverse proxy/load balancer, the proxy must be configured to set RemoteAddr appropriately (e.g., using the PROXY protocol or similar mechanism).

### Handler-Based Rate Limiting (for authenticated requests)
Applied in the request handler after JWT/API-Key validation succeeds:

**JWT Authentication:**
- Extract `sub` claim from validated JWT token
- Create hashed key: SHA256(IP + ":" + sub)
- Call limiter.Allow(hashed_key)
- Allows: Different JWT users from same IP to have independent rate limits

**API-Key Authentication:**
- Use IP-only key
- Call limiter.Allow(ip)
- All requests with same API key from same IP share the rate limit

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
