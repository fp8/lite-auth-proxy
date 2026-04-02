package ratelimit

import (
	"math"
	"sync"
	"time"
)

// RateLimiterConfig holds the settings for creating a RateLimiter.
type RateLimiterConfig struct {
	Name           string
	Enabled        bool
	RequestsPerMin int
	BanDuration    time.Duration
	ThrottleDelay  time.Duration // 0 = no delay (default)
	MaxDelaySlots  int           // semaphore cap; default 100
}

// RateLimiterStatus is the snapshot returned by GetStatus for admin endpoints.
type RateLimiterStatus struct {
	Name           string `json:"name"`
	Enabled        bool   `json:"enabled"`
	RequestsPerMin int    `json:"requestsPerMin"`
	ActiveEntries  int    `json:"activeEntries"`
	ThrottleDelay  string `json:"throttleDelay,omitempty"` // e.g. "100ms"
	MaxDelaySlots  int    `json:"maxDelaySlots,omitempty"` // 0 when throttle is disabled
}

// RateLimiter enforces per-key rate limits with temporary bans and
// an optional DDoS-safe throttle delay.
type RateLimiter struct {
	mu              sync.Mutex
	name            string
	enabled         bool
	requestsPerMin  int
	banDuration     time.Duration
	window          time.Duration
	cleanupInterval time.Duration
	now             func() time.Time
	entries         map[string]*entry
	lastCleanup     time.Time

	throttleDelay time.Duration
	maxDelaySlots int
	delaySem      chan struct{} // bounded semaphore for DDoS-safe delay
}

type entry struct {
	windowStart time.Time
	count       int
	bannedUntil time.Time
	lastSeen    time.Time
}

// NewRateLimiter creates a rate limiter from the provided config.
func NewRateLimiter(cfg RateLimiterConfig) *RateLimiter {
	return newRateLimiterWithClock(cfg, time.Now)
}

func newRateLimiterWithClock(cfg RateLimiterConfig, now func() time.Time) *RateLimiter {
	maxSlots := cfg.MaxDelaySlots
	if maxSlots <= 0 {
		maxSlots = 100
	}

	var sem chan struct{}
	if cfg.ThrottleDelay > 0 {
		sem = make(chan struct{}, maxSlots)
	}

	return &RateLimiter{
		name:            cfg.Name,
		enabled:         cfg.Enabled,
		requestsPerMin:  cfg.RequestsPerMin,
		banDuration:     cfg.BanDuration,
		window:          time.Minute,
		cleanupInterval: 1 * time.Minute,
		now:             now,
		entries:         make(map[string]*entry),
		lastCleanup:     now(),
		throttleDelay:   cfg.ThrottleDelay,
		maxDelaySlots:   maxSlots,
		delaySem:        sem,
	}
}

// Allow reports whether the request should be allowed and, if not, the retry-after seconds.
func (l *RateLimiter) Allow(key string) (bool, int) {
	if !l.enabled || l.requestsPerMin <= 0 {
		return true, 0
	}

	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	l.cleanupLocked(now)

	rec, ok := l.entries[key]
	if !ok {
		rec = &entry{windowStart: now}
		l.entries[key] = rec
	}

	rec.lastSeen = now

	if rec.bannedUntil.After(now) {
		return false, retryAfterSeconds(rec.bannedUntil.Sub(now))
	}

	if now.Sub(rec.windowStart) >= l.window {
		rec.windowStart = now
		rec.count = 0
	}

	rec.count++
	if rec.count > l.requestsPerMin {
		rec.bannedUntil = now.Add(l.banDuration)
		return false, retryAfterSeconds(rec.bannedUntil.Sub(now))
	}

	return true, 0
}

// Name returns the limiter's name (e.g. "ip", "apikey", "jwt").
func (l *RateLimiter) Name() string {
	return l.name
}

// ThrottleDelay returns the configured throttle delay duration.
func (l *RateLimiter) ThrottleDelay() time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.throttleDelay
}

// TryAcquireDelaySlot attempts to acquire a delay slot without blocking.
// Returns (false, nil) if no delay is configured or all slots are occupied (DDoS scenario).
// The caller MUST pass the returned channel to ReleaseDelaySlot to ensure correct
// release even if the semaphore is replaced by SetMaxDelaySlots at runtime.
func (l *RateLimiter) TryAcquireDelaySlot() (bool, chan struct{}) {
	l.mu.Lock()
	sem := l.delaySem
	l.mu.Unlock()

	if sem == nil {
		return false, nil
	}
	select {
	case sem <- struct{}{}:
		return true, sem
	default:
		return false, nil
	}
}

// ReleaseDelaySlot releases a delay slot acquired by TryAcquireDelaySlot.
// The sem argument must be the channel returned by TryAcquireDelaySlot.
func (l *RateLimiter) ReleaseDelaySlot(sem chan struct{}) {
	if sem == nil {
		return
	}
	<-sem
}

// SetRequestsPerMin updates the rate limit at runtime (admin control).
func (l *RateLimiter) SetRequestsPerMin(rpm int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.requestsPerMin = rpm
}

// SetThrottleDelay updates the throttle delay at runtime (admin control).
// Set to 0 to disable throttling. When enabling throttle on a limiter that had
// none, a new semaphore is created using the current MaxDelaySlots value.
func (l *RateLimiter) SetThrottleDelay(delay time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.throttleDelay = delay
	if delay > 0 && l.delaySem == nil {
		slots := l.maxDelaySlots
		if slots <= 0 {
			slots = 100
		}
		l.delaySem = make(chan struct{}, slots)
	} else if delay == 0 {
		l.delaySem = nil
	}
}

// SetMaxDelaySlots updates the maximum number of concurrent throttled responses
// at runtime (admin control). A new semaphore is created immediately if throttle
// is active; in-flight goroutines release via the channel returned by TryAcquireDelaySlot.
func (l *RateLimiter) SetMaxDelaySlots(slots int) {
	if slots <= 0 {
		slots = 100
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.maxDelaySlots = slots
	if l.delaySem != nil {
		l.delaySem = make(chan struct{}, slots)
	}
}

// Enable turns on rate limiting.
func (l *RateLimiter) Enable() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.enabled = true
}

// Disable turns off rate limiting. All requests are allowed while disabled.
func (l *RateLimiter) Disable() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.enabled = false
}

// GetStatus returns a snapshot of the limiter state for admin endpoints.
func (l *RateLimiter) GetStatus() *RateLimiterStatus {
	l.mu.Lock()
	defer l.mu.Unlock()

	status := &RateLimiterStatus{
		Name:           l.name,
		Enabled:        l.enabled,
		RequestsPerMin: l.requestsPerMin,
		ActiveEntries:  len(l.entries),
	}
	if l.throttleDelay > 0 {
		status.ThrottleDelay = l.throttleDelay.String()
		status.MaxDelaySlots = l.maxDelaySlots
	}
	return status
}

func (l *RateLimiter) cleanupLocked(now time.Time) {
	if now.Sub(l.lastCleanup) < l.cleanupInterval {
		return
	}

	for key, rec := range l.entries {
		if rec.bannedUntil.Before(now) && now.Sub(rec.lastSeen) > 2*l.window {
			delete(l.entries, key)
		}
	}

	l.lastCleanup = now
}

func retryAfterSeconds(duration time.Duration) int {
	if duration <= 0 {
		return 0
	}

	return int(math.Ceil(duration.Seconds()))
}
