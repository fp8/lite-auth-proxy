# API Documentation: lite-auth-proxy

## Endpoints

### Health Check
- Method: `GET`
- Path: `server.health_check.path` (default: `/healthz`)
- Auth: Always bypassed
- Behavior:
  - Local mode (default): returns `200 OK` with `{"status":"ok"}`
  - Proxy mode: forwards to `server.health_check.target` and returns its status/body

---

## Admin Endpoints

Available only when `admin.enabled = true`. For full documentation on the admin control plane — including configuration, authentication, rule lifecycle, serverless caveats, and examples — see the [Admin Control Plane Guide](ADMIN.md).

**Quick reference:**

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/admin/control` | `POST` | Manage dynamic rules (`set-rule`, `remove-rule`, `remove-all`) |
| `/admin/status` | `GET` | Snapshot of active rules and rate limiter states |

All admin endpoints require `Authorization: Bearer <GCP-identity-token>` from an account listed in `admin.jwt.allowed_emails` or matching `admin.jwt.filters`.

## Authentication

### JWT
- Header: `Authorization: Bearer <token>`
- Validates: signature, `exp`, `nbf`, `iss`, `aud`
- Claim filters: exact match or regex (`/pattern/`)
- Claim mapping: injected headers with prefix

### API Key
- Header: `auth.api_key.name` (default: `X-API-KEY`)
- Constant-time compare against configured value
- Injects `auth.api_key.payload` as headers:
  - Header name format: `{auth.header_prefix}{UPPER(payload_key)}`

---

## Proxy Error Responses

All error responses are JSON with `Content-Type: application/json`.

| Scenario | Status | Response |
|----------|--------|----------|
| Request body too large | 413 | `{"error":"request_too_large","message":"request body exceeds size limit"}` |
| Missing credentials | 401 | `{"error":"unauthorized","message":"missing credentials"}` |
| Invalid JWT format / missing kid | 401 | `{"error":"unauthorized","message":"invalid token format"}` |
| JWT signature failure | 401 | `{"error":"unauthorized","message":"invalid token signature"}` |
| JWT expired | 401 | `{"error":"unauthorized","message":"token expired"}` |
| JWT not yet valid | 401 | `{"error":"unauthorized","message":"token not yet valid"}` |
| JWT issuer/audience mismatch | 401 | `{"error":"unauthorized","message":"invalid token claims"}` |
| Claim filter failure | 401 | `{"error":"unauthorized","message":"access denied"}` |
| API key mismatch | 401 | `{"error":"unauthorized","message":"invalid api key"}` |
| Rate limit exceeded | 429 | `{"error":"rate_limited","message":"too many requests","retry_after":123}` |
| JWKS fetch failure | 502 | `{"error":"bad_gateway","message":"unable to validate token"}` |
| Upstream unavailable | 502 | `{"error":"bad_gateway","message":"upstream unreachable"}` |

## Request Flow

```
HTTP Request
  -> Request Logger         (structured logging)
  -> Body Limiter           (reject oversized requests)
  -> Header Sanitization    (strip X-AUTH-* headers)
  -> Path Filter            (include/exclude patterns)
  -> Dynamic Rule Check     (admin throttle/block/allow — skipped when admin disabled)
  -> API-Key Rate Limit     (per-key limit with optional IP compound key — when match rules apply)
  -> JWT Rate Limit         (per-subject limit with optional IP compound key)
  -> Per-IP Rate Limit      (if security.rate_limit.enabled; skipped when JWT already identified)
  -> Auth                   (JWT or API-Key)
  -> Claim Filter           (JWT only)
  -> Claim Mapping          -> Header Injection
  -> URL Rewriting          (strip prefix)
  -> Forward to Target
```
