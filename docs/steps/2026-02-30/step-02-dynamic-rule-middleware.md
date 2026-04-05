# Step 02: Dynamic Rule Check Middleware

## Objective

Insert a new middleware into lite-auth-proxy's request pipeline that evaluates dynamic rules from the RuleStore (created in Step 01) **before** the existing per-IP rate limiter. This ensures ShockGuard-imposed limits take precedence and are enforced globally regardless of how many unique IPs are sending traffic.

## Dependencies

- Step 01 (admin control API: RuleStore with ShouldAllow method)

## Reference

- ShockGuard spec Appendix D.3.1 (middleware ordering)
- ShockGuard `docs/steps/step-06-proxy-enhancements.md` (Middleware Integration section)

## Deliverables

### New Files

```
internal/proxy/dynamic_rule.go        # Dynamic rule check middleware
internal/proxy/dynamic_rule_test.go
```

### Modified Files

```
internal/proxy/proxy.go    # Insert DynamicRuleCheck into middleware pipeline
```

---

### Middleware Pipeline (Updated)

The existing pipeline is:

```
Request → RequestLogger → HeaderSanitizer → PathFilter → RateLimiter → Auth Handler → Proxy
```

After this step:

```
Request → RequestLogger → HeaderSanitizer → PathFilter → DynamicRuleCheck (NEW) → RateLimiter → Auth Handler → Proxy
```

The DynamicRuleCheck runs BEFORE the per-IP rate limiter so that:
1. ShockGuard's global throttle limits take precedence.
2. Traffic is rejected before reaching per-IP accounting (no wasted rate-limit budget).

---

### Implementation Details

#### dynamic_rule.go

```go
package proxy

// DynamicRuleCheck creates middleware that evaluates the admin RuleStore
// on every request. It requires an interface that matches RuleStore.ShouldAllow.
//
// Behavior:
// 1. Extract the request's Host header.
// 2. Call ruleChecker.ShouldAllow(host, path).
// 3. If allowed: pass to next handler.
// 4. If blocked: return 429 with JSON body:
//    { "error": "rate_limited", "message": "too many requests", "retry_after": 60 }
//
// When ruleChecker is nil (admin disabled), the middleware is a no-op passthrough.
func DynamicRuleCheck(ruleChecker RuleChecker) Middleware {
    // Implementation
}
```

The `RuleChecker` interface decouples the middleware from the admin package:

```go
// RuleChecker is the interface for dynamic rule evaluation.
// Implemented by admin.RuleStore.
type RuleChecker interface {
    ShouldAllow(host string, path string) bool
}
```

#### proxy.go (modifications)

Update the middleware pipeline construction:

```go
pipeline := applyMiddleware(baseHandler,
    RequestLogger(logger, cfg.Server.IncludePaths, cfg.Server.ExcludePaths),
    HeaderSanitizer(cfg.Auth.HeaderPrefix),
    PathFilter(cfg.Server.IncludePaths, cfg.Server.ExcludePaths),
    DynamicRuleCheck(ruleChecker),    // NEW — nil when admin disabled
    RateLimiter(limiter),
)
```

When `cfg.Admin.Enabled` is false, pass `nil` as the ruleChecker — the middleware becomes a no-op.

---

### Tests (~4 cases)

#### dynamic_rule_test.go

1. **No dynamic rules: request passes through to next handler.**
   - Create DynamicRuleCheck with a mock RuleChecker that always returns true.
   - Send a request. Assert next handler was called.

2. **Matching block rule: request returns 429.**
   - Create DynamicRuleCheck with mock returning false.
   - Send request. Assert 429 status. Assert JSON body contains `"error": "rate_limited"`.

3. **Nil ruleChecker (admin disabled): request passes through.**
   - Create DynamicRuleCheck(nil).
   - Send request. Assert next handler was called.

4. **Throttle rule under limit allows, over limit blocks.**
   - Set up a real RuleStore with a throttle rule (maxRPM=2).
   - Send 3 requests through middleware.
   - Assert first 2 return 200, third returns 429.

---

## Verification

```bash
go test ./internal/proxy/... -race -count=1
go build ./...
```
