# Step 05: Startup Rule Persistence via Environment Variable

## Objective

Ensure throttle rules survive Cloud Run instance replacement.

lite-auth-proxy is deployed on Cloud Run. Cloud Run scales instances up and down at will.
When a new instance starts, the in-memory RuleStore is empty — no throttle rules are active
until ShockGuard's next 5-minute monitor cycle. That gap is up to 5 minutes of unthrottled
traffic on a new instance during an active billing spike.

This step closes that gap: on startup, lite-auth-proxy reads the `PROXY_THROTTLE_RULES`
environment variable and pre-loads it into the RuleStore (and VertexAI bucket) before
serving any traffic.

**ShockGuard's responsibility** (Step 07 update): whenever ShockGuard sets or removes a rule
via the admin API, it also patches the Cloud Run service's env var so that future instances
start with the current rule set. See `docs/steps/step-07-proxy-integration.md §Cloud Run
Rule Sync`.

## Dependencies

- Step 01 (RuleStore, VertexAIBucket exist)
- Step 03 (VertexAIBucket.SetMaxRPM exists)
- Step 04 (VertexAIBucket.SetMaxRPM takes perKey bool; optional — degrade gracefully if
  rateByKey field is absent)

## Deliverables

### New Files

```
internal/startup/rule_loader.go       # Reads PROXY_THROTTLE_RULES and populates RuleStore
internal/startup/rule_loader_test.go
```

### Modified Files

```
cmd/proxy/main.go    # Call RuleLoader.Load() after creating RuleStore, before ListenAndServe
```

---

## Environment Variable Format

**Name:** `PROXY_THROTTLE_RULES`

**Value:** A JSON array of persisted rule objects. ShockGuard writes this; lite-auth-proxy
reads it on startup. Plain JSON (no base64 wrapping).

```json
[
  {
    "ruleId":          "sg-throttle-my-api",
    "targetHost":      "my-api-abc123-uc.a.run.app",
    "action":          "throttle",
    "maxRPM":          50,
    "pathPattern":     null,
    "rateByKey":       false,
    "expiresAt":       "2026-03-30T15:10:00Z"
  },
  {
    "ruleId":          "sg-throttle-vertex",
    "targetHost":      "-aiplatform.googleapis.com",
    "action":          "throttle",
    "maxRPM":          200,
    "pathPattern":     "/v1/projects/",
    "rateByKey":       true,
    "expiresAt":       "2026-03-30T15:10:00Z"
  }
]
```

Key points:
- `expiresAt` is an **absolute** RFC 3339 timestamp (not a duration). ShockGuard writes the
  wall-clock expiry at the time it sets the rule.
- Rules where `expiresAt` is in the past are silently skipped — they have already expired.
- `rateByKey` defaults to `false` if absent (backwards-compatible with Step 03 rules that
  pre-date Step 04).
- An empty array `[]` or a missing env var both result in no rules loaded.
- Cloud Run env var size limit is 32 KB. At ~200 bytes per rule this supports ~160 rules —
  far more than ShockGuard will ever set simultaneously.

---

## Implementation Details

### rule_loader.go

```go
package startup

import (
    "encoding/json"
    "os"
    "time"

    "github.com/fp8/lite-auth-proxy/internal/admin"
    "github.com/fp8/lite-auth-proxy/internal/ratelimit"
)

const EnvThrottleRules = "PROXY_THROTTLE_RULES"

// PersistedRule is the JSON shape stored in PROXY_THROTTLE_RULES.
// It mirrors admin.Rule but uses an absolute ExpiresAt timestamp.
type PersistedRule struct {
    RuleID      string  `json:"ruleId"`
    TargetHost  string  `json:"targetHost"`
    Action      string  `json:"action"`
    MaxRPM      int     `json:"maxRPM,omitempty"`
    PathPattern *string `json:"pathPattern,omitempty"`
    RateByKey   bool    `json:"rateByKey,omitempty"`
    ExpiresAt   string  `json:"expiresAt"` // RFC 3339
}

// RuleLoader loads persisted rules into the RuleStore and VertexAI bucket on startup.
type RuleLoader struct {
    store        *admin.RuleStore
    vertexBucket *ratelimit.VertexAIBucket // may be nil if admin is disabled
}

func NewRuleLoader(store *admin.RuleStore, vertexBucket *ratelimit.VertexAIBucket) *RuleLoader {
    return &RuleLoader{store: store, vertexBucket: vertexBucket}
}

// Load reads PROXY_THROTTLE_RULES and populates the RuleStore.
// It is idempotent and safe to call multiple times (each call replaces previous state).
// Errors in individual rules are skipped with a log warning; a parse failure of the
// entire env var is returned as an error.
//
// Must be called before the HTTP server starts accepting traffic.
func (l *RuleLoader) Load() error {
    raw := os.Getenv(EnvThrottleRules)
    if raw == "" {
        return nil // nothing to load
    }

    var persisted []PersistedRule
    if err := json.Unmarshal([]byte(raw), &persisted); err != nil {
        return fmt.Errorf("PROXY_THROTTLE_RULES: invalid JSON: %w", err)
    }

    now := time.Now()
    loaded := 0

    for _, pr := range persisted {
        expiresAt, err := time.Parse(time.RFC3339, pr.ExpiresAt)
        if err != nil {
            // log warning: skip rule with invalid expiresAt
            continue
        }
        if !expiresAt.After(now) {
            continue // already expired
        }

        remaining := int(expiresAt.Sub(now).Seconds())
        if remaining <= 0 {
            continue
        }

        rule := &admin.Rule{
            RuleID:          pr.RuleID,
            TargetHost:      pr.TargetHost,
            Action:          pr.Action,
            MaxRPM:          pr.MaxRPM,
            PathPattern:     pr.PathPattern,
            RateByKey:       pr.RateByKey,
            DurationSeconds: remaining, // remaining seconds from now
        }

        if err := l.store.SetRule(rule); err != nil {
            // log warning: skip
            continue
        }

        // If this is a Vertex AI rule, also configure the VertexAI bucket.
        if l.vertexBucket != nil && isVertexAIRule(pr) {
            l.vertexBucket.SetMaxRPM(pr.MaxRPM, pr.RateByKey)
        }

        loaded++
    }

    // log info: loaded N rules from PROXY_THROTTLE_RULES
    return nil
}

// isVertexAIRule returns true if the rule targets Vertex AI endpoints.
// Mirrors the detection used in admin/handler.go when setting rules.
func isVertexAIRule(pr PersistedRule) bool {
    if pr.PathPattern == nil {
        return false
    }
    return strings.Contains(*pr.PathPattern, "/v1/projects/") ||
        strings.Contains(pr.TargetHost, "aiplatform.googleapis.com")
}
```

### cmd/proxy/main.go (modification)

After creating the RuleStore (when admin is enabled), and before `server.ListenAndServe()`:

```go
if cfg.Admin.Enabled {
    // ... existing: create ruleStore, adminValidator, register /admin/* routes ...

    loader := startup.NewRuleLoader(ruleStore, vertexBucket)
    if err := loader.Load(); err != nil {
        // Log as warning, not fatal — the proxy still works without pre-loaded rules.
        // ShockGuard will re-apply rules on its next 5-minute cycle.
        logger.Warn("startup rule load failed", "error", err)
    }
}

// existing: server.ListenAndServe()
```

The load failure is non-fatal: the proxy starts normally, ShockGuard's next cycle will
re-apply the rules within 5 minutes. A fatal error here would make the proxy undeployable
if the env var is malformed.

---

## Tests (~5 cases)

### rule_loader_test.go

1. **Empty env var: no rules loaded, no error.**
   - `os.Setenv(EnvThrottleRules, "")`.
   - Call `Load()`. Assert no error.
   - Assert `ruleStore.GetStatus()` returns empty slice.

2. **Valid rules loaded: non-expired rules applied, expired rules skipped.**
   - Set env var with 2 rules: one expiring in 1 hour, one expiring 1 minute ago.
   - Call `Load()`. Assert no error.
   - Assert `ruleStore.GetStatus()` has exactly 1 rule (the non-expired one).

3. **Remaining duration is correct.**
   - Set env var with a rule expiring exactly 300 seconds from now.
   - Call `Load()`.
   - Assert the rule in RuleStore has a `durationSeconds` close to 300 (within ±2s).

4. **Vertex AI rule also configures VertexAI bucket.**
   - Set env var with a Vertex AI rule (`pathPattern: "/v1/projects/"`, `maxRPM: 100`,
     `rateByKey: true`).
   - Call `Load()`.
   - Assert `vertexBucket.GetStatus()` is non-nil with `maxRPM=100` and `mode="per-key"`.

5. **Malformed JSON returns error; proxy does not crash.**
   - Set env var to `"not valid json"`.
   - Call `Load()`. Assert error is returned.
   - Assert `ruleStore.GetStatus()` is still empty (no partial state).

---

## Verification

```bash
go test ./internal/startup/... -count=1
go build ./...

# Manual smoke test: set the env var and start the proxy, verify rules are active
PROXY_THROTTLE_RULES='[{"ruleId":"test","targetHost":"example.run.app","action":"throttle","maxRPM":10,"expiresAt":"2099-01-01T00:00:00Z"}]' \
  go run ./cmd/proxy/... &

curl -s http://localhost:8080/admin/status | jq .rules
# Expected: 1 rule with ruleId="test"
```
