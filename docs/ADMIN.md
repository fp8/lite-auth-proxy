# Admin Control Plane

The admin control plane provides runtime traffic management — throttle, block, or allow requests — without redeploying the proxy. It exposes two HTTP endpoints (`/admin/control` and `/admin/status`) and is **disabled by default**.

> **In-memory only.** All rules created via the admin API are stored in process memory. They are lost when the process exits — including during Cloud Run scale-to-zero, new revision deployments, or any container restart. See [Serverless Caveat](#serverless-caveat-cloud-run-and-ephemeral-instances) for mitigation strategies.

## Table of Contents

- [Overview](#overview)
- [How It Works](#how-it-works)
- [Configuration](#configuration)
- [Authentication](#authentication)
- [Endpoints](#endpoints)
  - [POST /admin/control](#post-admincontrol)
  - [GET /admin/status](#get-adminstatus)
- [Rule Lifecycle](#rule-lifecycle)
- [Targeting Rate Limiters](#targeting-rate-limiters)
- [Startup Rule Persistence](#startup-rule-persistence)
- [Serverless Caveat: Cloud Run and Ephemeral Instances](#serverless-caveat-cloud-run-and-ephemeral-instances)
- [Examples](#examples)

## Overview

The admin API is designed for operational scenarios where you need to react to traffic anomalies quickly — throttle a misbehaving client, block a specific host, or temporarily allow a path that is normally rate-limited — without going through a full deploy cycle.

Key characteristics:

- **Zero overhead when disabled.** Routes are not registered; no goroutines run.
- **In-memory rule store.** Rules are held in a thread-safe Go map with background goroutines for expiry cleanup (every 30s) and RPM counter resets (every 60s).
- **Time-bounded rules.** Every rule requires a `durationSeconds` and auto-expires. There is no way to create a permanent rule via the API.
- **Independent auth pipeline.** Admin endpoints use their own JWT validation (separate issuer/audience/allowlist from the main proxy auth), so admin access can be restricted to a dedicated service account.

## How It Works

```
Operator / Automation
        |
        |  POST /admin/control  { "command":"set-rule", "rule": { ... } }
        v
  +-----------+       +--------------+       +------------------+
  | Admin Auth| ----> | ControlHandler| ----> | RuleStore (map)  |
  | Middleware |       |              |       | in-memory, mutex |
  +-----------+       +--------------+       +------------------+
                                                     |
                                                     v
                                            +------------------+
                                            | ShouldAllow()    |
                                            | called per-request|
                                            | in middleware     |
                                            +------------------+
```

1. An authenticated operator (or automation) sends a `POST /admin/control` request with a rule.
2. The admin auth middleware validates the caller's GCP identity token.
3. The handler validates the rule payload and inserts it into the in-memory `RuleStore`.
4. For every proxied request, the middleware pipeline calls `RuleStore.ShouldAllow(host, path)` which evaluates all active (non-expired) rules against the request's `Host` header and path.
5. Background goroutines clean up expired rules every 30 seconds and reset per-rule RPM counters every 60 seconds.

## Configuration

The admin API is toggled by the `[admin]` section in your TOML config (or the equivalent env vars).

**Minimum configuration:**

```toml
[admin]
enabled = true

[admin.jwt]
issuer         = "https://accounts.google.com"
audience       = "https://your-proxy.run.app"
allowed_emails = ["sg-killswitch@your-project.iam.gserviceaccount.com"]
```

**All available fields:**

| Field | Type | Default | ENV Variable | Description |
|-------|------|---------|---|-------------|
| `admin.enabled` | boolean | `false` | `PROXY_ADMIN_ENABLED` | Register `/admin/control` and `/admin/status` routes |
| `admin.jwt.issuer` | string | `"https://accounts.google.com"` | `PROXY_ADMIN_JWT_ISSUER` | Expected OIDC issuer for admin identity tokens |
| `admin.jwt.audience` | string | — | `PROXY_ADMIN_JWT_AUDIENCE` | Expected audience — set to the proxy's own Cloud Run URL |
| `admin.jwt.allowed_emails` | array[string] | `[]` | `PROXY_ADMIN_JWT_ALLOWED_EMAILS` | Service account emails allowed to call the admin API |
| `admin.jwt.filters` | map[string]string | `{}` | — | Require specific JWT claim values (e.g. `hd = "corp.com"`) |
| `admin.jwt.tolerance_secs` | integer | `30` | `PROXY_ADMIN_JWT_TOLERANCE_SECS` | Clock skew tolerance for admin token validation |
| `admin.jwt.cache_ttl_mins` | integer | `1440` | `PROXY_ADMIN_JWT_CACHE_TTL_MINS` | How long to cache validated admin tokens (minutes) |

**Access control:** At least one of `allowed_emails` or `filters` must be set when `admin.enabled = true`. Both can be used together — the token must satisfy all configured checks.

### Alternative: restrict by claim instead of email

```toml
[admin]
enabled = true

[admin.jwt]
issuer   = "https://accounts.google.com"
audience = "https://your-proxy.run.app"

[admin.jwt.filters]
hd = "your-domain.com"
```

## Authentication

Admin endpoints use a separate JWT validation pipeline from the main proxy auth. This means:

- The admin `issuer` and `audience` can differ from `auth.jwt.issuer` / `auth.jwt.audience`.
- Admin access is typically restricted to GCP service accounts or employees of a specific Google Workspace domain.
- No API key auth is supported for admin endpoints — JWT only.

**How callers authenticate:**

1. Obtain a GCP identity token (ID token) with the proxy's Cloud Run URL as the audience.
2. Send it as `Authorization: Bearer <token>` on every admin request.
3. The proxy validates the token against the issuer's OIDC JWKS endpoint, then checks `email` against `allowed_emails` and/or evaluates `filters`.

```bash
# Example: get an identity token for a service account
TOKEN=$(gcloud auth print-identity-token \
  --audiences="https://your-proxy.run.app" \
  --impersonate-service-account=sg-killswitch@your-project.iam.gserviceaccount.com)

curl -X POST https://your-proxy.run.app/admin/control \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{ ... }'
```

## Endpoints

### POST /admin/control

Manages dynamic rules at runtime. Accepts three commands: `set-rule`, `remove-rule`, `remove-all`.

**Request body:**

```json
{
  "command": "<set-rule | remove-rule | remove-all>",
  "rule": { ... },
  "ruleId": "..."
}
```

#### set-rule

Creates or updates a rule. If a rule with the same `ruleId` already exists, it is replaced and its RPM counter resets to 0.

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `ruleId` | string | yes | Unique rule identifier |
| `targetHost` | string | yes | Exact `Host` header value to match |
| `action` | string | yes | `throttle`, `block`, or `allow` |
| `maxRPM` | integer | when `throttle` | Maximum requests per minute |
| `pathPattern` | string | no | Path prefix or glob to restrict the rule (e.g. `/v1/projects/`) |
| `rateByKey` | boolean | no | `true` = per-caller-identity buckets; `false` (default) = global counter |
| `limiter` | string | no | Target a specific rate limiter: `ip`, `apikey`, or `jwt`. See [Targeting Rate Limiters](#targeting-rate-limiters). |
| `throttleDelayMs` | integer | no | Millisecond delay before returning 429; `0` = no change |
| `maxDelaySlots` | integer | no | Max concurrent throttled (delayed) responses; `0` = no change |
| `durationSeconds` | integer | yes | Rule lifetime in seconds; rule auto-expires afterwards |

**Response (200):**
```json
{"ruleId":"sg-throttle","status":"active","expiresAt":"2026-03-30T15:10:00Z"}
```

#### remove-rule

Deletes a single rule by `ruleId`.

**Request body:**
```json
{"command":"remove-rule","ruleId":"sg-throttle"}
```

**Response (200):**
```json
{"status":"ok","rulesRemoved":1}
```

Returns `404` if the `ruleId` does not exist.

#### remove-all

Deletes all active rules.

**Request body:**
```json
{"command":"remove-all"}
```

**Response (200):**
```json
{"status":"ok","rulesRemoved":3}
```

#### Error responses

| Status | Condition |
|--------|-----------|
| `401` | Missing/invalid identity token, or email not in `allowed_emails` |
| `400` | Invalid JSON, unknown command, or missing required fields |
| `404` | `ruleId` not found (remove-rule only) |

### GET /admin/status

Returns a snapshot of all active rules and rate limiter states. Useful for dashboards, monitoring, or verifying that a rule was applied.

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
  "rateLimiters": {
    "ip": {
      "name": "ip",
      "enabled": true,
      "requestsPerMin": 60,
      "activeEntries": 15,
      "throttleDelay": "100ms",
      "maxDelaySlots": 100
    },
    "apikey": {
      "name": "apikey",
      "enabled": true,
      "requestsPerMin": 200,
      "activeEntries": 3
    },
    "jwt": {
      "name": "jwt",
      "enabled": false,
      "requestsPerMin": 60,
      "activeEntries": 0
    }
  }
}
```

- `rules` is an empty array when no rules are active.
- `rateLimiters` contains a key for each configured limiter (`ip`, `apikey`, `jwt`) with its current state.
- `throttleDelay` and `maxDelaySlots` are omitted when throttle delay is not configured.

## Rule Lifecycle

Every admin rule follows this lifecycle:

```
set-rule (with durationSeconds)
    |
    v
 ACTIVE  ── RPM counter resets every 60s
    |          Evaluated on every proxied request via ShouldAllow()
    |
    |── durationSeconds elapsed ──> EXPIRED (cleaned up within 30s)
    |── remove-rule called ──────> REMOVED immediately
    |── remove-all called ───────> REMOVED immediately
    |── process exits ───────────> LOST (in-memory only)
```

**There is no persistence layer.** The `RuleStore` is a Go `map[string]*Rule` protected by a `sync.RWMutex`. When the process exits, all rules are gone. This is by design — admin rules are intended for short-lived operational interventions, not permanent configuration.

### Rule evaluation order

When a request arrives, `ShouldAllow(host, path)` iterates all non-expired rules:

1. **`block`** — returns `false` (reject) immediately on first match.
2. **`allow`** — returns `true` (permit) immediately on first match.
3. **`throttle`** — increments the rule's RPM counter; returns `false` if the counter exceeds `maxRPM`.

If no rule matches, the request is allowed.

### Path matching

The `pathPattern` field supports two modes:

- **Prefix match** (default): `/v1/projects/` matches any path starting with that prefix.
- **Glob match**: patterns containing `*`, `?`, or `[` are evaluated using Go's `path.Match`.

## Targeting Rate Limiters

When a rule includes `"limiter": "ip"` (or `"apikey"` or `"jwt"`), the `set-rule` command also reconfigures the named rate limiter at runtime:

| Rule field | Limiter effect |
|---|---|
| `maxRPM` | Updates `requestsPerMin` on the targeted limiter |
| `throttleDelayMs` | Updates the limiter's throttle delay (only if > 0) |
| `maxDelaySlots` | Updates the limiter's max concurrent delay slots (only if > 0) |
| `action: "throttle"` | Calls `limiter.Enable()` to activate the limiter if it was disabled |

This means you can use the admin API to dynamically enable and tune a rate limiter without restarting.

```bash
# Enable the API-key limiter at 30 RPM with a 200ms delay
curl -X POST .../admin/control \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "command": "set-rule",
    "rule": {
      "ruleId": "activate-apikey-limiter",
      "targetHost": "my-api.run.app",
      "action": "throttle",
      "limiter": "apikey",
      "maxRPM": 30,
      "throttleDelayMs": 200,
      "maxDelaySlots": 50,
      "durationSeconds": 600
    }
  }'
```

**Note:** When the rule expires or is removed, the limiter settings it changed are **not** automatically reverted. The limiter continues running with the last-set values until the process restarts or another rule changes them.

## Startup Rule Persistence

Because admin rules are in-memory, they would normally be lost whenever a Cloud Run instance restarts. The `PROXY_THROTTLE_RULES` environment variable provides a workaround: it lets you pre-populate the rule store at startup.

When `admin.enabled = true`, the proxy reads `PROXY_THROTTLE_RULES` before serving any traffic and loads the rules into the store. This prevents a gap in throttling when Cloud Run scales up a new instance.

**Format:** JSON array of rule objects with absolute `expiresAt` timestamps (RFC 3339):

```bash
PROXY_THROTTLE_RULES='[
  {
    "ruleId":      "sg-throttle-vertex",
    "targetHost":  "-aiplatform.googleapis.com",
    "action":      "throttle",
    "maxRPM":      200,
    "pathPattern": "/v1/projects/",
    "rateByKey":   true,
    "expiresAt":   "2026-03-30T15:10:00Z"
  }
]'
```

**Behavior:**

- Rules with an `expiresAt` in the past are silently skipped.
- An empty or missing variable results in no rules loaded (not an error).
- A malformed JSON value logs a warning but does **not** prevent the proxy from starting.
- If a rule targets a specific `limiter`, that limiter's RPM is updated and the limiter is enabled.

### Automation pattern

The intended workflow for keeping rules alive across restarts:

1. Your automation (e.g. a ShockGuard script or alert handler) calls `POST /admin/control` to set a rule.
2. The same automation updates the `PROXY_THROTTLE_RULES` env var on the Cloud Run service via `gcloud run services update --set-env-vars`.
3. When Cloud Run spins up new instances (scale-out or cold start), they read `PROXY_THROTTLE_RULES` and start with the rules pre-loaded.
4. When the rule is no longer needed, the automation calls `remove-rule` and clears or updates `PROXY_THROTTLE_RULES`.

## Serverless Caveat: Cloud Run and Ephemeral Instances

**This is a critical consideration for production deployments.**

All admin rules exist only in the memory of the running process. In serverless environments like Cloud Run, Fargate, or Lambda, instances are ephemeral:

- **Scale-to-zero:** When traffic drops and Cloud Run scales the service to zero instances, all in-memory rules vanish. The next request triggers a cold start with an empty rule store.
- **New revisions:** Deploying a new revision replaces all running instances. Rules set on old instances are lost.
- **Multiple instances:** Cloud Run can run multiple instances concurrently. A rule set via the admin API only applies to the instance that received the request. Other instances are unaware of it.
- **Instance recycling:** Cloud Run may terminate and replace instances at any time for maintenance.

### Why this matters

If you set a throttle rule to protect your backend during a traffic spike, and Cloud Run scales up a second instance, that new instance has **no rules** — it will forward traffic unthrottled. Similarly, if your single instance is recycled, the replacement starts clean.

### Mitigation strategies

1. **`PROXY_THROTTLE_RULES` env var (recommended).** After setting a rule via the admin API, also update the `PROXY_THROTTLE_RULES` env var on the Cloud Run service. All new instances will start with those rules pre-loaded. See [Startup Rule Persistence](#startup-rule-persistence).

2. **Set rules on every instance.** If your Cloud Run service runs multiple instances with `min-instances > 0`, your automation needs to account for the fact that each instance has its own rule store. The env var approach above is simpler and covers all instances.

3. **Use static config for permanent rules.** If a rate-limiting rule is always needed (not a temporary response to an incident), configure it in `config.toml` or via env vars in the `[security.*]` sections rather than relying on the admin API. The admin API is designed for short-lived operational interventions.

4. **Monitor with `/admin/status`.** Poll the status endpoint to verify rules are active on the instance handling your request. Be aware that in a multi-instance setup, the load balancer may route your status check to a different instance than the one you set the rule on.

## Examples

### Throttle a backend to 50 RPM for 10 minutes

```bash
TOKEN=$(gcloud auth print-identity-token --audiences="https://your-proxy.run.app")

curl -X POST https://your-proxy.run.app/admin/control \
  -H "Authorization: Bearer $TOKEN" \
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

### Block all traffic to a host for 1 hour

```bash
curl -X POST https://your-proxy.run.app/admin/control \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "command": "set-rule",
    "rule": {
      "ruleId": "block-compromised-host",
      "targetHost": "compromised-service.run.app",
      "action": "block",
      "durationSeconds": 3600
    }
  }'
```

### Allow a specific path to bypass rate limiting

```bash
curl -X POST https://your-proxy.run.app/admin/control \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "command": "set-rule",
    "rule": {
      "ruleId": "allow-health-burst",
      "targetHost": "my-api.run.app",
      "action": "allow",
      "pathPattern": "/healthz",
      "durationSeconds": 300
    }
  }'
```

### Check active rules

```bash
curl -s https://your-proxy.run.app/admin/status \
  -H "Authorization: Bearer $TOKEN" | jq .
```

### Remove a specific rule

```bash
curl -X POST https://your-proxy.run.app/admin/control \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"command":"remove-rule","ruleId":"throttle-my-api"}'
```

### Emergency: remove all rules

```bash
curl -X POST https://your-proxy.run.app/admin/control \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"command":"remove-all"}'
```

## See Also

- [Configuration Guide](CONFIGURATION.md) — Full config reference including admin settings
- [Rate Limiting Guide](RATE-LIMITING.md) — Rate limiter layers, scenarios, and ShockGuard
- [API Documentation](API.md) — All HTTP endpoints and error responses
- [Deployment Guide](DEPLOYMENT.md) — Cloud Run deployment and production setup
