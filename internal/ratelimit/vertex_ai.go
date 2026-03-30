package ratelimit

import (
	"net/http"
	"strings"
	"sync/atomic"
)

// VertexAIStatus is the status snapshot returned by VertexAIBucket.GetStatus.
// Included in the GET /admin/status response.
type VertexAIStatus struct {
	Mode       string      `json:"mode"`                 // "global" or "per-key"
	MaxRPM     int         `json:"maxRPM"`
	CurrentRPM *int        `json:"currentRPM,omitempty"` // populated in global mode only
	Keys       []KeyStatus `json:"keys,omitempty"`       // populated in per-key mode only
	Status     string      `json:"status"`
}

// VertexAIBucket manages a global (or per-caller) rate-limit bucket for Vertex AI requests.
// Initially disabled (no limit). Enabled by SetMaxRPM; disabled by Disable.
type VertexAIBucket struct {
	maxRPM     atomic.Int64
	currentRPM atomic.Int64
	enabled    atomic.Bool

	// per-key mode fields (Step 04)
	keyMode   atomic.Bool
	keyBucket *VertexAIKeyBucket
}

// NewVertexAIBucket creates a new, initially disabled bucket.
func NewVertexAIBucket() *VertexAIBucket {
	return &VertexAIBucket{}
}

// IsVertexAIRequest returns true if the request targets a Vertex AI endpoint.
//
// Detection rules:
//  1. Host contains "-aiplatform.googleapis.com"
//  2. Path contains "/v1/projects/" AND one of "/endpoints/", "/publishers/", "/models/"
func IsVertexAIRequest(r *http.Request) bool {
	host := r.Host
	if strings.Contains(host, "-aiplatform.googleapis.com") {
		return true
	}
	p := r.URL.Path
	if strings.Contains(p, "/v1/projects/") {
		return strings.Contains(p, "/endpoints/") ||
			strings.Contains(p, "/publishers/") ||
			strings.Contains(p, "/models/")
	}
	return false
}

// SetMaxRPM enables the bucket with the given limit.
// perKey=false → global counter (all Vertex AI traffic counted together).
// perKey=true  → per-caller-identity counters (Step 04 behaviour).
func (b *VertexAIBucket) SetMaxRPM(maxRPM int, perKey bool) {
	b.maxRPM.Store(int64(maxRPM))
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

// Disable turns off the bucket. All requests are allowed while disabled.
func (b *VertexAIBucket) Disable() {
	b.enabled.Store(false)
	b.keyMode.Store(false)
	b.keyBucket = nil
	b.currentRPM.Store(0)
}

// ShouldAllow returns true if the request should proceed.
// identity is the caller identity string (used in per-key mode); ignored in global mode.
func (b *VertexAIBucket) ShouldAllow(identity string) bool {
	if !b.enabled.Load() {
		return true
	}
	if b.keyMode.Load() && b.keyBucket != nil {
		return b.keyBucket.ShouldAllow(identity)
	}
	// global mode
	return b.currentRPM.Add(1) <= b.maxRPM.Load()
}

// GetStatus returns a status snapshot, or nil if the bucket is disabled.
func (b *VertexAIBucket) GetStatus() *VertexAIStatus {
	if !b.enabled.Load() {
		return nil
	}
	if b.keyMode.Load() && b.keyBucket != nil {
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
