# Step 03: Vertex AI Endpoint Detection and Rate-Limit Bucket

## Objective

Add Vertex AI endpoint detection with a separate global rate-limit bucket. This enables ShockGuard to throttle Vertex AI traffic independently from per-IP rate limits, since Vertex AI billing is based on total request volume regardless of source IP.

## Dependencies

- Step 01 (admin control API: `/admin/status` returns vertexAI field)
- Step 02 (dynamic rule middleware is in place)

## Reference

- ShockGuard spec Appendix D.3.2 (Vertex AI Endpoint Detection)
- ShockGuard `docs/steps/step-06-proxy-enhancements.md` (vertex_ai.go section)

## Deliverables

### New Files

```
internal/ratelimit/vertex_ai.go        # Vertex AI detection and bucket
internal/ratelimit/vertex_ai_test.go
```

### Modified Files

```
internal/proxy/proxy.go          # Add Vertex AI middleware to pipeline
internal/proxy/middleware.go     # Add VertexAIRateLimit middleware function
internal/admin/handler.go        # Wire VertexAI bucket into /admin/status
```

---

### Implementation Details

#### vertex_ai.go

```go
package ratelimit

// VertexAIBucket manages a separate rate-limit bucket for Vertex AI requests.
//
// This bucket is independent of per-IP rate limits. It provides a global
// (not per-IP) rate limit for all Vertex AI traffic passing through the proxy.
//
// Initially disabled (no limit applied). Enabled when ShockGuard sends a
// throttle command via /admin/control that targets Vertex AI.
type VertexAIBucket struct {
    maxRPM     atomic.Int64
    currentRPM atomic.Int64
    enabled    atomic.Bool
}
```

**IsVertexAIRequest(r *http.Request) bool**

Detection rules:
1. Host contains `-aiplatform.googleapis.com` → true
2. Path contains `/v1/projects/` AND any of `/endpoints/`, `/publishers/`, `/models/` → true
3. Otherwise → false

**ShouldAllow() bool**
- If disabled: return true (no limit).
- Increment currentRPM. If > maxRPM: return false.

**SetMaxRPM(maxRPM int)**
- Enable the bucket and set the limit. Called when ShockGuard sends a throttle command.

**Disable()**
- Disable the bucket. Called when ShockGuard removes the throttle.

**GetStatus() *VertexAIStatus**
- Returns nil if not enabled. Otherwise returns current maxRPM, currentRPM, and status.

RPM counter is reset to 0 every 60 seconds by the same goroutine that resets RuleStore counters (or a dedicated one).

#### Middleware integration

Add after DynamicRuleCheck, before per-IP RateLimiter:

```
Request → ... → PathFilter → DynamicRuleCheck → VertexAIRateLimit (NEW) → RateLimiter → Auth → Proxy
```

The VertexAIRateLimit middleware:
1. Call `IsVertexAIRequest(r)`.
2. If true: call `bucket.ShouldAllow()`.
3. If blocked: return 429 with `{ "error": "rate_limited", "message": "Vertex AI rate limit exceeded", "retry_after": 60 }`.
4. If not a Vertex AI request or allowed: pass to next handler.

#### Admin integration

The `/admin/control` handler gains awareness of Vertex AI:
- When a `set-rule` command has `pathPattern` matching `/v1/projects/` (Vertex AI convention), also update the VertexAIBucket's maxRPM.
- The `/admin/status` endpoint includes the `vertexAI` field from `bucket.GetStatus()`.

---

### Tests (~4 cases)

#### vertex_ai_test.go

1. **Request to Vertex AI endpoint detected correctly.**
   - Host="us-central1-aiplatform.googleapis.com", path="/v1/projects/p/locations/l/endpoints/e:predict".
   - Assert IsVertexAIRequest returns true.

2. **Request to non-Vertex AI endpoint not detected.**
   - Host="myapp-abc123.run.app", path="/api/data".
   - Assert IsVertexAIRequest returns false.

3. **Vertex AI publisher/model endpoint detected.**
   - Path="/v1/projects/p/locations/l/publishers/google/models/gemini-pro:predict".
   - Assert IsVertexAIRequest returns true.

4. **Vertex AI rate limit blocks after exceeding maxRPM.**
   - Set maxRPM=10. Send 11 requests.
   - Assert first 10 allowed, 11th blocked.
   - Verify per-IP rate limiter counter is unaffected.

---

## Verification

```bash
go test ./internal/ratelimit/... -count=1
go test ./internal/proxy/... -race -count=1
go build ./...
```
