# Step 01: Dynamic Control-Plane API

## Objective

Add a dynamic control-plane API to lite-auth-proxy so external systems (e.g., ShockGuard) can command rate-limit changes at runtime. This enables immediate, traffic-level throttling without redeploying or restarting the proxy.

## Context

lite-auth-proxy already provides static per-IP rate limiting configured via TOML. This step adds **dynamic** rate-limit rules that can be created, updated, and removed at runtime via HTTP API. These rules take precedence over the static per-IP limits and enable global (not per-IP) traffic shaping.

## Reference

- ShockGuard spec Appendix D.3.1 (Dynamic Control-Plane API)
- ShockGuard spec Appendix D.3.3 (Thread-Safe Rule Store)
- ShockGuard `docs/steps/step-06-proxy-enhancements.md`

## Deliverables

### New Files

```
internal/admin/
  handler.go           # HTTP handlers for /admin/control and /admin/status
  handler_test.go
  rule_store.go        # Thread-safe in-memory rule storage with auto-expiry
  rule_store_test.go
  auth.go              # Admin JWT authentication middleware (validates GCP service account ID tokens)
  auth_test.go
  types.go             # Shared types for admin package
```

### Modified Files

```
internal/proxy/proxy.go      # Register /admin routes on mux
internal/config/config.go    # Add Admin config section
cmd/proxy/main.go            # Wire admin dependencies
```

---

### Configuration

Add a new `[admin]` section to the TOML config:

```toml
[admin]
enabled = false              # Default: disabled

[admin.jwt]
issuer = "https://accounts.google.com"
audience = "{{ENV.ADMIN_AUDIENCE}}"    # The proxy's own Cloud Run URL
allowed_emails = []                     # Service account emails allowed to call admin API
```

Also support environment variable overrides: `PROXY_ADMIN_JWT_ISSUER`, `PROXY_ADMIN_JWT_AUDIENCE`, `PROXY_ADMIN_JWT_ALLOWED_EMAILS` (comma-separated).

**Authentication model:** The admin API uses **GCP service account identity tokens** (OIDC JWTs) rather than a shared secret. This reuses lite-auth-proxy's existing JWT validation infrastructure and avoids managing a separate secret in Secret Manager.

How it works:
1. ShockGuard's service account (e.g., `sg-killswitch@fp8devel.iam.gserviceaccount.com`) runs in the same GCP project as lite-auth-proxy.
2. When ShockGuard needs to call the admin API, it obtains an **ID token** from the GCP metadata server (or Application Default Credentials), with the `audience` set to the proxy's Cloud Run URL.
3. lite-auth-proxy validates the token against Google's OIDC issuer (`https://accounts.google.com`) using the existing `jwt.Validator` infrastructure and its JWKS key cache.
4. After standard JWT validation (signature, issuer, audience, expiry), the middleware checks the `email` claim against `allowed_emails` to ensure only the ShockGuard service account can access the admin API.

When `admin.enabled = false`, the `/admin/*` routes are not registered. This ensures zero overhead when the control API is not needed.

---

### API Specification

#### POST /admin/control

Authentication: `Authorization: Bearer <service-account-id-token>` — a GCP identity token issued to the ShockGuard service account, with audience set to the proxy's Cloud Run URL.

**Commands:**

1. **set-rule** — Create or update a dynamic rate-limit rule.

   Request:
   ```json
   {
     "command": "set-rule",
     "rule": {
       "ruleId": "sg-throttle-my-api",
       "targetHost": "my-api-abc123-uc.a.run.app",
       "action": "throttle",
       "maxRPM": 50,
       "pathPattern": null,
       "durationSeconds": 600
     }
   }
   ```

   Response (200):
   ```json
   {
     "ruleId": "sg-throttle-my-api",
     "status": "active",
     "expiresAt": "2026-03-28T15:10:00Z"
   }
   ```

   Validation:
   - `ruleId`: non-empty string (required)
   - `targetHost`: non-empty string (required)
   - `action`: one of `"throttle"`, `"block"`, `"allow"` (required)
   - `maxRPM`: > 0 (required when action=throttle)
   - `durationSeconds`: > 0 (required)
   - `pathPattern`: optional string (prefix match or glob)

2. **remove-rule** — Delete a rule by ruleId.

   Request:
   ```json
   {
     "command": "remove-rule",
     "ruleId": "sg-throttle-my-api"
   }
   ```

   Response (200):
   ```json
   { "status": "ok", "rulesRemoved": 1 }
   ```

3. **remove-all** — Clear all dynamic rules.

   Request:
   ```json
   { "command": "remove-all" }
   ```

   Response (200):
   ```json
   { "status": "ok", "rulesRemoved": 3 }
   ```

**Error responses:**
- `401 Unauthorized`: Missing, expired, or invalid identity token; or `email` claim not in `allowed_emails`.
- `400 Bad Request`: Invalid JSON, missing required fields, or unknown command.
- `404 Not Found`: ruleId not found (for remove-rule only).

#### GET /admin/status

Authentication: same service account identity token as Bearer.

Response (200):
```json
{
  "rules": [
    {
      "ruleId": "sg-throttle-my-api",
      "targetHost": "my-api-abc123-uc.a.run.app",
      "action": "throttle",
      "maxRPM": 50,
      "currentRPM": 23,
      "status": "active",
      "expiresAt": "2026-03-28T15:10:00Z"
    }
  ],
  "vertexAI": null
}
```

If no rules are active, `rules` is an empty array.

---

### Implementation Details

#### types.go

```go
package admin

import (
    "sync/atomic"
    "time"
)

// ControlRequest represents the body of POST /admin/control
type ControlRequest struct {
    Command string `json:"command"`
    Rule    *Rule  `json:"rule,omitempty"`
    RuleID  string `json:"ruleId,omitempty"`
}

// Rule represents a dynamic rate-limit rule
type Rule struct {
    RuleID          string     `json:"ruleId"`
    TargetHost      string     `json:"targetHost"`
    Action          string     `json:"action"`
    MaxRPM          int        `json:"maxRPM,omitempty"`
    PathPattern     *string    `json:"pathPattern,omitempty"`
    DurationSeconds int        `json:"durationSeconds"`
    ExpiresAt       time.Time  `json:"-"`
    currentRPM      atomic.Int64
}

type SetRuleResponse struct {
    RuleID    string `json:"ruleId"`
    Status    string `json:"status"`
    ExpiresAt string `json:"expiresAt"`
}

type RemoveResponse struct {
    Status       string `json:"status"`
    RulesRemoved int    `json:"rulesRemoved"`
}

type StatusResponse struct {
    Rules    []RuleStatus    `json:"rules"`
    VertexAI *VertexAIStatus `json:"vertexAI"`
}

type RuleStatus struct {
    RuleID     string `json:"ruleId"`
    TargetHost string `json:"targetHost"`
    Action     string `json:"action"`
    MaxRPM     int    `json:"maxRPM"`
    CurrentRPM int    `json:"currentRPM"`
    Status     string `json:"status"`
    ExpiresAt  string `json:"expiresAt"`
}

type VertexAIStatus struct {
    MaxRPM     int    `json:"maxRPM"`
    CurrentRPM int    `json:"currentRPM"`
    Status     string `json:"status"`
}
```

#### rule_store.go

Thread-safe in-memory store using `sync.RWMutex`.

**Key behaviors:**
- `SetRule(rule)`: Compute `ExpiresAt = now + durationSeconds`. Store/update in map by ruleId. Reset currentRPM to 0 on upsert.
- `RemoveRule(ruleId)`: Delete if exists. Return (found, error).
- `RemoveAll()`: Clear map, return count removed.
- `GetStatus()`: Read-locked snapshot of all active rules with currentRPM values.
- `ShouldAllow(host, path)`: Read-locked lookup. Match by targetHost (exact) and optionally pathPattern (prefix). For `action=block`: return false. For `action=allow`: return true. For `action=throttle`: increment atomic currentRPM counter, return false if > maxRPM.
- Background cleanup goroutine: every 30s, delete expired rules.
- RPM counter reset goroutine: every 60s, reset all currentRPM counters to 0.
- `Stop()`: signal goroutines to exit (called on server shutdown).

**Crash recovery:** Rules are in-memory only. On restart, all rules are lost. This is acceptable — ShockGuard re-applies throttle rules on each 5-minute monitor cycle.

#### auth.go

`AdminAuthMiddleware(validator *jwt.Validator, allowedEmails []string) func(http.Handler) http.Handler`

Reuses the existing `jwt.Validator` from `internal/auth/jwt` with an admin-specific JWT config (admin issuer/audience).

1. Extract `Authorization` header.
2. Verify `Bearer ` prefix, extract the token.
3. Call `validator.ValidateToken(token)` — this performs full JWT validation:
   - Signature verification against Google's JWKS keys (cached).
   - Issuer check (`https://accounts.google.com`).
   - Audience check (proxy's Cloud Run URL).
   - Expiry / not-before checks.
4. Extract the `email` claim from validated claims.
5. Check that the `email` is in the `allowedEmails` list (case-insensitive comparison).
6. If any check fails: return 401 JSON `{ "error": "unauthorized", "message": "Invalid or missing identity token" }`.
7. If valid: call next handler.

This approach means:
- **No shared secret to manage** — no Secret Manager entry needed for the proxy admin API.
- **Same JWKS cache** as the main auth pipeline — no extra HTTP calls for key fetching.
- **Standard GCP service-to-service auth** — the ShockGuard service account obtains an ID token via the metadata server or Application Default Credentials, which is the standard Cloud Run invocation pattern.

#### handler.go

**POST /admin/control handler:**
1. Parse JSON body into ControlRequest.
2. Validate command is one of: `set-rule`, `remove-rule`, `remove-all`.
3. Delegate to RuleStore method.
4. Return appropriate response.

**GET /admin/status handler:**
1. Call RuleStore.GetStatus() for rule snapshot.
2. Call VertexAIBucket.GetStatus() if available (nil in this step — added in step-02).
3. Return StatusResponse.

#### proxy.go (modifications)

In `NewHandler`, when admin is enabled:
1. Create a `jwt.Validator` for the admin JWT config (issuer, audience from `cfg.Admin.JWT`).
2. Create `RuleStore` and start its background goroutines.
3. Register `/admin/control` and `/admin/status` on the mux, wrapped with `AdminAuthMiddleware`.
4. On server shutdown, call `RuleStore.Stop()`.

```go
if cfg.Admin.Enabled {
    adminJWTConfig := &config.JWTConfig{
        Enabled:       true,
        Issuer:        cfg.Admin.JWT.Issuer,
        Audience:      cfg.Admin.JWT.Audience,
        ToleranceSecs: 30,
        CacheTTLMins:  1440,
    }
    adminValidator := jwt.NewValidator(adminJWTConfig)
    ruleStore := admin.NewRuleStore()
    adminAuth := admin.AdminAuthMiddleware(adminValidator, cfg.Admin.JWT.AllowedEmails)
    mux.Handle("POST /admin/control", adminAuth(admin.ControlHandler(ruleStore)))
    mux.Handle("GET /admin/status", adminAuth(admin.StatusHandler(ruleStore, nil)))
}
```

**Note:** The admin JWT validator is separate from the main auth JWT validator. The main validator is for end-user traffic (customer-configured issuer/audience). The admin validator is always configured to validate GCP service account identity tokens from `https://accounts.google.com` with the proxy's own URL as audience.

---

### Tests (~15 Go test cases)

All tests use Go's `testing` package and `net/http/httptest`.

#### handler_test.go (~6 cases)

1. POST /admin/control with valid service account ID token and set-rule returns 200.
2. POST /admin/control without Authorization header returns 401.
3. POST /admin/control with invalid JSON returns 400.
4. POST /admin/control remove-rule for existing rule returns 200.
5. POST /admin/control remove-rule for non-existent rule returns 404.
6. GET /admin/status returns all active rules.

#### rule_store_test.go (~5 cases)

1. SetRule then ShouldAllow with traffic under maxRPM allows request.
2. SetRule then ShouldAllow with traffic over maxRPM blocks request.
3. SetRule with durationSeconds=1, wait 2s, traffic allowed (rule expired).
4. RemoveAll clears all rules.
5. Concurrent SetRule/ShouldAllow operations are thread-safe (run with `-race`).

#### auth_test.go (~4 cases)

1. Valid service account ID token with email in allowed list passes through to next handler.
2. Missing Authorization header returns 401.
3. Valid JWT but email NOT in allowed_emails returns 401.
4. Expired or malformed JWT returns 401.

---

## Verification

```bash
go test ./internal/admin/... -race -count=1    # all tests pass, no race conditions
go build ./...                                  # builds successfully
```
