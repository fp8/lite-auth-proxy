# Step 04: Vertex AI Per-Caller Rate Limiting

## Objective

Replace the coarse global Vertex AI RPM bucket (Step 03) with **per-caller identity** buckets.
One caller exhausting the Vertex AI rate limit no longer starves other callers.

The caller identity is resolved in priority order:
1. `x-goog-api-key` header (GCP API key)
2. `key` query parameter (GCP API key in URL)
3. `sub` claim from `Authorization: Bearer <JWT>` payload (OAuth identity)
4. Client IP fallback (same as existing `ClientIP(r)`)

The identity is extracted in the Vertex AI middleware — **before** the auth handler runs —
so the payload is parsed without re-validating the JWT signature. This is safe for rate
limiting: the token is still fully validated downstream by the auth handler. A forged `sub`
only determines which bucket a request is counted against; it grants no additional access.

## Dependencies

- Step 03 (VertexAIBucket and IsVertexAIRequest exist)

## Reference

- `internal/proxy/proxy.go` — `hashKey`, `extractBearerToken`, `ClientIP`, `writeRateLimitResponse`
- `internal/ratelimit/limiter.go` — per-key map + sliding window pattern to follow
- `internal/auth/jwt/validator.go` — Claims type (`map[string]interface{}`)

---

## Deliverables

### New Files

```
internal/ratelimit/vertex_ai_key.go       # Caller identity extraction + per-key bucket map
internal/ratelimit/vertex_ai_key_test.go
```

### Modified Files

```
internal/ratelimit/vertex_ai.go           # Add key-mode to VertexAIBucket; update ShouldAllow signature
internal/proxy/middleware.go              # Pass identity to bucket.ShouldAllow
internal/admin/types.go                   # Add RateByKey to Rule; expand VertexAIStatus
internal/admin/handler.go                 # Pass RateByKey to bucket.SetMaxRPM
```

---

## Implementation Details

### vertex_ai_key.go

#### Caller identity extraction

```go
package ratelimit

import (
    "crypto/sha256"
    "encoding/base64"
    "encoding/json"
    "net/http"
    "strings"
)

// ExtractVertexAICallerIdentity returns a stable, opaque identity string for
// rate-limit bucketing. It does NOT validate any token signature — the caller
// is only being assigned a rate-limit bucket, not granted access.
//
// Priority:
//  1. x-goog-api-key header
//  2. key query parameter
//  3. sub claim from Bearer JWT payload (unverified parse)
//  4. ClientIP(r) fallback
func ExtractVertexAICallerIdentity(r *http.Request) string {
    if key := r.Header.Get("x-goog-api-key"); key != "" {
        return "k:" + hashIdentity(key)
    }
    if key := r.URL.Query().Get("key"); key != "" {
        return "k:" + hashIdentity(key)
    }
    if sub := extractBearerSub(r.Header.Get("Authorization")); sub != "" {
        return "s:" + sub  // sub is already an opaque identifier; no hashing needed
    }
    return "ip:" + clientIP(r)
}

// hashIdentity returns the first 16 hex chars of the SHA-256 of raw.
// Long enough to avoid accidental collisions; short enough to be a good map key.
func hashIdentity(raw string) string {
    h := sha256.Sum256([]byte(raw))
    return base64.RawURLEncoding.EncodeToString(h[:12]) // 96 bits → 16 base64url chars
}

// extractBearerSub does a non-validating parse of a Bearer JWT to read the sub claim.
// Returns "" if the header is absent, not a JWT, or has no sub claim.
func extractBearerSub(authHeader string) string {
    if !strings.HasPrefix(authHeader, "Bearer ") {
        return ""
    }
    token := strings.TrimPrefix(authHeader, "Bearer ")
    parts := strings.SplitN(token, ".", 3)
    if len(parts) != 3 {
        return ""
    }
    payload, err := base64.RawURLEncoding.DecodeString(parts[1])
    if err != nil {
        return ""
    }
    var claims map[string]interface{}
    if err := json.Unmarshal(payload, &claims); err != nil {
        return ""
    }
    sub, _ := claims["sub"].(string)
    return sub
}
```

`clientIP(r)` is a package-level helper that replicates the logic of `proxy.ClientIP` so
the `ratelimit` package has no import cycle. It calls `net.SplitHostPort(r.RemoteAddr)`.

#### Per-key bucket map

```go
// keyRecord tracks per-identity RPM within the current window.
type keyRecord struct {
    currentRPM atomic.Int64
    lastSeen   atomic.Int64 // Unix seconds — used for stale-entry cleanup
}

// VertexAIKeyBucket enforces per-caller-identity RPM limits.
// All callers share the same maxRPM ceiling; no per-caller customisation.
type VertexAIKeyBucket struct {
    maxRPM int64

    mu      sync.RWMutex
    records map[string]*keyRecord

    stopCh chan struct{}
}

func NewVertexAIKeyBucket(maxRPM int64) *VertexAIKeyBucket {
    b := &VertexAIKeyBucket{
        maxRPM:  maxRPM,
        records: make(map[string]*keyRecord),
        stopCh:  make(chan struct{}),
    }
    go b.resetLoop()
    go b.cleanupLoop()
    return b
}
```

**ShouldAllow(identity string) bool**
- Write-lock on first access per identity (to create the record), then atomic increment.
- Optimise for the common case (existing record) with a read-lock first:

```go
func (b *VertexAIKeyBucket) ShouldAllow(identity string) bool {
    b.mu.RLock()
    rec, ok := b.records[identity]
    b.mu.RUnlock()

    if !ok {
        b.mu.Lock()
        // Re-check after acquiring write lock (avoid double-insert).
        if rec, ok = b.records[identity]; !ok {
            rec = &keyRecord{}
            b.records[identity] = rec
        }
        b.mu.Unlock()
    }

    rec.lastSeen.Store(time.Now().Unix())
    return rec.currentRPM.Add(1) <= b.maxRPM
}
```

**resetLoop** — every 60 s, range over records under read-lock and reset `currentRPM` to 0.
Uses `atomic.Int64.Store(0)`, so no write-lock is needed for the reset itself.

**cleanupLoop** — every 5 min, write-lock and delete records where
`time.Now().Unix() - rec.lastSeen.Load() > 300`.

**Stop()** — closes `stopCh` to signal both goroutines to return.

**GetStatus() []KeyStatus**
```go
type KeyStatus struct {
    Identity   string `json:"identity"`   // already opaque (hashed or "s:<sub>")
    CurrentRPM int64  `json:"currentRPM"`
}
```
Returns a snapshot under read-lock, sorted by identity for deterministic output.

---

### Modifications to vertex_ai.go (Step 03)

#### Extended VertexAIBucket

Add two fields:

```go
type VertexAIBucket struct {
    // --- existing ---
    maxRPM     atomic.Int64
    currentRPM atomic.Int64
    enabled    atomic.Bool

    // --- new in step 04 ---
    keyMode    atomic.Bool
    keyBucket  *VertexAIKeyBucket // non-nil only when keyMode=true
}
```

#### SetMaxRPM signature change

```go
// SetMaxRPM enables the bucket.
// perKey=false → original global counter (step 03 behaviour).
// perKey=true  → per-caller-identity counters (step 04).
func (b *VertexAIBucket) SetMaxRPM(maxRPM int, perKey bool) {
    if perKey {
        b.keyMode.Store(true)
        b.keyBucket = NewVertexAIKeyBucket(int64(maxRPM))
    } else {
        b.keyMode.Store(false)
        b.keyBucket = nil
        b.maxRPM.Store(int64(maxRPM))
        b.currentRPM.Store(0)
    }
    b.enabled.Store(true)
}
```

#### ShouldAllow signature change

The middleware now supplies the caller identity:

```go
// ShouldAllow returns true if the request should proceed.
// identity is ignored in global mode.
func (b *VertexAIBucket) ShouldAllow(identity string) bool {
    if !b.enabled.Load() {
        return true
    }
    if b.keyMode.Load() {
        return b.keyBucket.ShouldAllow(identity)
    }
    // global mode — original step-03 behaviour
    return b.currentRPM.Add(1) <= b.maxRPM.Load()
}
```

#### GetStatus update

```go
func (b *VertexAIBucket) GetStatus() *VertexAIStatus {
    if !b.enabled.Load() {
        return nil
    }
    if b.keyMode.Load() {
        return &VertexAIStatus{
            Mode:   "per-key",
            MaxRPM: int(b.maxRPM.Load()),
            Keys:   b.keyBucket.GetStatus(),
            Status: "active",
        }
    }
    rpm := int(b.currentRPM.Load())
    maxRPM := int(b.maxRPM.Load())
    return &VertexAIStatus{
        Mode:       "global",
        MaxRPM:     maxRPM,
        CurrentRPM: &rpm,
        Status:     "active",
    }
}
```

---

### Modifications to middleware.go

The `VertexAIRateLimit` middleware in Step 03 always calls `bucket.ShouldAllow()`.
Step 04 adds caller-identity extraction:

```go
// VertexAIRateLimit enforces the Vertex AI rate limit bucket.
// In per-key mode the limit applies per caller identity; in global mode it applies
// to all Vertex AI traffic in aggregate.
func VertexAIRateLimit(bucket *ratelimit.VertexAIBucket) Middleware {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            if bucket == nil || !ratelimit.IsVertexAIRequest(r) {
                next.ServeHTTP(w, r)
                return
            }
            identity := ratelimit.ExtractVertexAICallerIdentity(r)
            if !bucket.ShouldAllow(identity) {
                writeRateLimitResponse(w, 60)
                return
            }
            next.ServeHTTP(w, r)
        })
    }
}
```

The identity extraction is lightweight (one header read, or a base64-decode + JSON unmarshal
of a JWT payload that is typically under 512 bytes). No extra I/O or locking beyond the
bucket map lookup.

---

### Modifications to admin/types.go

#### Rule — new field

```go
type Rule struct {
    RuleID          string  `json:"ruleId"`
    TargetHost      string  `json:"targetHost"`
    Action          string  `json:"action"`
    MaxRPM          int     `json:"maxRPM,omitempty"`
    PathPattern     *string `json:"pathPattern,omitempty"`
    RateByKey       bool    `json:"rateByKey,omitempty"` // NEW: per-caller-identity mode
    DurationSeconds int     `json:"durationSeconds"`
    ExpiresAt       time.Time  `json:"-"`
    currentRPM      atomic.Int64
}
```

`rateByKey` is only meaningful when `action=throttle` and `pathPattern` targets a Vertex AI
path. It is silently ignored for non-Vertex AI rules.

#### VertexAIStatus — expanded

```go
type VertexAIStatus struct {
    Mode       string      `json:"mode"`                 // "global" or "per-key"
    MaxRPM     int         `json:"maxRPM"`
    CurrentRPM *int        `json:"currentRPM,omitempty"` // populated in global mode only
    Keys       []KeyStatus `json:"keys,omitempty"`        // populated in per-key mode only
    Status     string      `json:"status"`
}

type KeyStatus struct {
    Identity   string `json:"identity"`   // opaque; safe to log
    CurrentRPM int64  `json:"currentRPM"`
}
```

Example `/admin/status` response with per-key mode active:

```json
{
  "rules": [],
  "vertexAI": {
    "mode": "per-key",
    "maxRPM": 100,
    "keys": [
      { "identity": "k:a3f8b2c1d4e5", "currentRPM": 87 },
      { "identity": "s:11223344556677",  "currentRPM": 12 }
    ],
    "status": "active"
  }
}
```

---

### Modifications to admin/handler.go

The existing Vertex AI detection heuristic (path contains `/v1/projects/`) gains a
`rateByKey` parameter:

```go
// In the set-rule handler, after deciding the rule targets Vertex AI:
if isVertexAIPath(rule.PathPattern) {
    vertexAIBucket.SetMaxRPM(rule.MaxRPM, rule.RateByKey)
}
```

No other changes to the handler. `remove-rule` and `remove-all` call `bucket.Disable()`
exactly as in Step 03.

---

## Tests (~6 cases)

### vertex_ai_key_test.go

1. **API key identity from x-goog-api-key header.**
   - Two requests: one with `x-goog-api-key: key-A`, one with `x-goog-api-key: key-B`.
   - Assert `ExtractVertexAICallerIdentity` returns different `k:...` strings for each.
   - Assert the same key consistently produces the same identity across calls.

2. **API key identity from key query parameter.**
   - Request with `?key=myapikey`, no header.
   - Assert identity starts with `k:`.

3. **Bearer JWT sub used when no API key present.**
   - Craft a fake JWT (unsigned; valid base64 payload with `"sub":"user-123"`).
   - Assert identity is `s:user-123`.

4. **IP fallback when no API key or Bearer token.**
   - Plain request with no relevant headers.
   - Assert identity starts with `ip:`.

5. **Per-key mode: key A exhausting limit does not block key B.**
   - `NewVertexAIKeyBucket(maxRPM=3)`.
   - Send 4 requests as `k:aaa` → first 3 allowed, 4th blocked.
   - Send 1 request as `k:bbb` → allowed (fresh bucket).

6. **RPM reset: counter resets after window.**
   - Use a test-injectable reset tick (or call the reset method directly).
   - Exhaust limit for identity `k:aaa`. Reset counters. Assert next request allowed.

---

## Verification

```bash
go test ./internal/ratelimit/... -race -count=1
go test ./internal/proxy/...    -race -count=1
go test ./internal/admin/...    -race -count=1
go build ./...
```
