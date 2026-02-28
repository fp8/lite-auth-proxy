package ratelimit

import (
	"math"
	"sync"
	"time"
)

// Limiter enforces per-IP rate limits with temporary bans.
type Limiter struct {
	mu              sync.Mutex
	enabled         bool
	requestsPerMin  int
	banDuration     time.Duration
	window          time.Duration
	cleanupInterval time.Duration
	now             func() time.Time
	entries         map[string]*entry
	lastCleanup     time.Time
}

type entry struct {
	windowStart time.Time
	count       int
	bannedUntil time.Time
	lastSeen    time.Time
}

// NewLimiter creates a rate limiter with the provided settings.
func NewLimiter(enabled bool, requestsPerMinute int, banDuration time.Duration) *Limiter {
	return newLimiterWithClock(enabled, requestsPerMinute, banDuration, time.Now)
}

func newLimiterWithClock(enabled bool, requestsPerMinute int, banDuration time.Duration, now func() time.Time) *Limiter {
	return &Limiter{
		enabled:         enabled,
		requestsPerMin:  requestsPerMinute,
		banDuration:     banDuration,
		window:          time.Minute,
		cleanupInterval: 1 * time.Minute,
		now:             now,
		entries:         make(map[string]*entry),
		lastCleanup:     now(),
	}
}

// Allow reports whether the request should be allowed and, if not, the retry-after seconds.
func (l *Limiter) Allow(ip string) (bool, int) {
	if !l.enabled || l.requestsPerMin <= 0 {
		return true, 0
	}

	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	l.cleanupLocked(now)

	rec, ok := l.entries[ip]
	if !ok {
		rec = &entry{windowStart: now}
		l.entries[ip] = rec
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

func (l *Limiter) cleanupLocked(now time.Time) {
	if now.Sub(l.lastCleanup) < l.cleanupInterval {
		return
	}

	for ip, rec := range l.entries {
		if rec.bannedUntil.Before(now) && now.Sub(rec.lastSeen) > 2*l.window {
			delete(l.entries, ip)
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
