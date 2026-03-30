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

Available only when `admin.enabled = true`. All admin endpoints require an `Authorization: Bearer <GCP-identity-token>` header from a service account listed in `admin.jwt.allowed_emails`.

### POST /admin/control

Manages dynamic rate-limit rules at runtime.

**Request body:**

```json
{
  "command": "<set-rule | remove-rule | remove-all>",
  "rule": { ... },   // required for set-rule
  "ruleId": "..."    // required for remove-rule
}
```

**`set-rule` payload:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `ruleId` | string | yes | Unique rule identifier |
| `targetHost` | string | yes | Exact `Host` header value to match |
| `action` | string | yes | `throttle`, `block`, or `allow` |
| `maxRPM` | integer | when `throttle` | Maximum requests per minute (global or per-caller) |
| `pathPattern` | string | no | Path prefix or glob to restrict the rule (e.g. `/v1/projects/`) |
| `rateByKey` | boolean | no | `true` = per-caller-identity buckets; `false` (default) = global counter |
| `durationSeconds` | integer | yes | Rule lifetime in seconds; rule auto-expires afterwards |

**`set-rule` response (200):**
```json
{"ruleId":"sg-throttle","status":"active","expiresAt":"2026-03-30T15:10:00Z"}
```

**`remove-rule` / `remove-all` response (200):**
```json
{"status":"ok","rulesRemoved":1}
```

**Error responses:**

| Status | Condition |
|--------|-----------|
| `401` | Missing/invalid identity token, or email not in `allowed_emails` |
| `400` | Invalid JSON, unknown command, or missing required fields |
| `404` | `ruleId` not found (remove-rule only) |

---

### GET /admin/status

Returns a snapshot of all active rules and the Vertex AI bucket state.

**Response (200):**
```json
{
  "rules": [
    {
      "ruleId": "sg-throttle-my-api",
      "targetHost": "my-api.run.app",
      "action": "throttle",
      "maxRPM": 50,
      "currentRPM": 23,
      "status": "active",
      "expiresAt": "2026-03-30T15:10:00Z"
    }
  ],
  "vertexAI": {
    "mode": "per-key",
    "maxRPM": 200,
    "keys": [
      {"identity": "k:a3f8b2c1d4e5", "currentRPM": 87}
    ],
    "status": "active"
  }
}
```

- `rules` is an empty array when no rules are active.
- `vertexAI` is `null` when the Vertex AI bucket is not enabled.
- In `global` mode, `vertexAI` includes `currentRPM` instead of `keys`.

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
  -> Header Sanitization    (strip X-AUTH-* headers)
  -> Path Filter            (include/exclude patterns)
  -> Dynamic Rule Check     (admin throttle/block/allow — skipped when admin disabled)
  -> Vertex AI Rate Limit   (per-caller or global — skipped when admin disabled)
  -> Per-IP Rate Limit      (if security.rate_limit.enabled)
  -> Auth                   (JWT or API-Key)
  -> Claim Filter           (JWT only)
  -> Claim Mapping          -> Header Injection
  -> URL Rewriting          (strip prefix)
  -> Forward to Target
```
