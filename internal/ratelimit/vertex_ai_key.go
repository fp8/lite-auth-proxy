package ratelimit

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// KeyStatus is the per-caller identity status snapshot in VertexAIStatus.
type KeyStatus struct {
	Identity   string `json:"identity"`   // opaque; already hashed or "s:<sub>"
	CurrentRPM int64  `json:"currentRPM"`
}

// keyRecord tracks per-identity RPM within the current window.
type keyRecord struct {
	currentRPM atomic.Int64
	lastSeen   atomic.Int64 // Unix seconds — used for stale-entry cleanup
}

// VertexAIKeyBucket enforces per-caller-identity RPM limits.
// All callers share the same maxRPM ceiling.
type VertexAIKeyBucket struct {
	maxRPM int64

	mu      sync.RWMutex
	records map[string]*keyRecord

	stopCh chan struct{}
}

// NewVertexAIKeyBucket creates a bucket and starts background goroutines.
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

// ShouldAllow returns true if the caller identity is within the rate limit.
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

// GetStatus returns a snapshot of all caller identities and their current RPM.
// Results are sorted by identity for deterministic output.
func (b *VertexAIKeyBucket) GetStatus() []KeyStatus {
	b.mu.RLock()
	defer b.mu.RUnlock()

	result := make([]KeyStatus, 0, len(b.records))
	for identity, rec := range b.records {
		result = append(result, KeyStatus{
			Identity:   identity,
			CurrentRPM: rec.currentRPM.Load(),
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Identity < result[j].Identity
	})
	return result
}

// Stop signals the background goroutines to exit.
func (b *VertexAIKeyBucket) Stop() {
	close(b.stopCh)
}

// resetLoop resets all currentRPM counters to 0 every 60 seconds.
func (b *VertexAIKeyBucket) resetLoop() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			b.mu.RLock()
			for _, rec := range b.records {
				rec.currentRPM.Store(0)
			}
			b.mu.RUnlock()
		case <-b.stopCh:
			return
		}
	}
}

// cleanupLoop removes stale entries (not seen for > 5 minutes) every 5 minutes.
func (b *VertexAIKeyBucket) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			cutoff := time.Now().Unix() - 300
			b.mu.Lock()
			for id, rec := range b.records {
				if rec.lastSeen.Load() < cutoff {
					delete(b.records, id)
				}
			}
			b.mu.Unlock()
		case <-b.stopCh:
			return
		}
	}
}

// ResetAll resets all currentRPM counters (exported for testing).
func (b *VertexAIKeyBucket) ResetAll() {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, rec := range b.records {
		rec.currentRPM.Store(0)
	}
}

// ExtractVertexAICallerIdentity returns a stable, opaque identity string for
// rate-limit bucketing. It does NOT validate any token signature.
//
// Priority:
//  1. x-goog-api-key header
//  2. key query parameter
//  3. sub claim from Bearer JWT payload (unverified parse)
//  4. Client IP fallback
func ExtractVertexAICallerIdentity(r *http.Request) string {
	if key := r.Header.Get("x-goog-api-key"); key != "" {
		return "k:" + hashIdentity(key)
	}
	if key := r.URL.Query().Get("key"); key != "" {
		return "k:" + hashIdentity(key)
	}
	if sub := extractBearerSub(r.Header.Get("Authorization")); sub != "" {
		return "s:" + sub
	}
	return "ip:" + vertexClientIP(r)
}

// hashIdentity returns the first 16 base64url chars of the SHA-256 of raw.
func hashIdentity(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return base64.RawURLEncoding.EncodeToString(h[:12]) // 96 bits → 16 chars
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

// vertexClientIP extracts the client IP, replicating proxy.ClientIP to avoid
// an import cycle between the ratelimit and proxy packages.
func vertexClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	return r.RemoteAddr
}
