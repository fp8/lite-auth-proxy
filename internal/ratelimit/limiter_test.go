package ratelimit

import (
	"testing"
	"time"
)

type fakeClock struct {
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	return c.now
}

func (c *fakeClock) Add(d time.Duration) {
	c.now = c.now.Add(d)
}

func TestRateLimiterAllowsWithinRate(t *testing.T) {
	clock := &fakeClock{now: time.Unix(0, 0)}
	limiter := newRateLimiterWithClock(RateLimiterConfig{
		Name: "test", Enabled: true, RequestsPerMin: 2, BanDuration: 2 * time.Minute,
	}, clock.Now)

	if ok, _ := limiter.Allow("1.2.3.4"); !ok {
		t.Fatal("expected first request to be allowed")
	}
	if ok, _ := limiter.Allow("1.2.3.4"); !ok {
		t.Fatal("expected second request to be allowed")
	}
}

func TestRateLimiterBansOnExceed(t *testing.T) {
	clock := &fakeClock{now: time.Unix(0, 0)}
	limiter := newRateLimiterWithClock(RateLimiterConfig{
		Name: "test", Enabled: true, RequestsPerMin: 1, BanDuration: 5 * time.Minute,
	}, clock.Now)

	if ok, _ := limiter.Allow("1.2.3.4"); !ok {
		t.Fatal("expected first request to be allowed")
	}

	allowed, retryAfter := limiter.Allow("1.2.3.4")
	if allowed {
		t.Fatal("expected second request to be banned")
	}
	if retryAfter == 0 {
		t.Fatal("expected retry-after to be set")
	}
}

func TestRateLimiterBanExpires(t *testing.T) {
	clock := &fakeClock{now: time.Unix(0, 0)}
	limiter := newRateLimiterWithClock(RateLimiterConfig{
		Name: "test", Enabled: true, RequestsPerMin: 1, BanDuration: 1 * time.Minute,
	}, clock.Now)

	limiter.Allow("1.2.3.4")
	limiter.Allow("1.2.3.4")

	clock.Add(61 * time.Second)

	if ok, _ := limiter.Allow("1.2.3.4"); !ok {
		t.Fatal("expected request to be allowed after ban expires")
	}
}

func TestRateLimiterDisabledAllowsAll(t *testing.T) {
	clock := &fakeClock{now: time.Unix(0, 0)}
	limiter := newRateLimiterWithClock(RateLimiterConfig{
		Name: "test", Enabled: false, RequestsPerMin: 1, BanDuration: 1 * time.Minute,
	}, clock.Now)

	if ok, _ := limiter.Allow("1.2.3.4"); !ok {
		t.Fatal("expected request to be allowed when limiter disabled")
	}
	if ok, _ := limiter.Allow("1.2.3.4"); !ok {
		t.Fatal("expected request to be allowed when limiter disabled")
	}
}

func TestRateLimiterThrottleDelay(t *testing.T) {
	limiter := NewRateLimiter(RateLimiterConfig{
		Name: "test", Enabled: true, RequestsPerMin: 1, BanDuration: 1 * time.Minute,
		ThrottleDelay: 100 * time.Millisecond, MaxDelaySlots: 2,
	})

	if limiter.ThrottleDelay() != 100*time.Millisecond {
		t.Fatal("expected throttle delay of 100ms")
	}

	// Acquire two slots (max capacity)
	if !limiter.TryAcquireDelaySlot() {
		t.Fatal("expected first slot to be acquired")
	}
	if !limiter.TryAcquireDelaySlot() {
		t.Fatal("expected second slot to be acquired")
	}
	// Third should fail (semaphore full)
	if limiter.TryAcquireDelaySlot() {
		t.Fatal("expected third slot acquisition to fail")
	}

	// Release one slot and try again
	limiter.ReleaseDelaySlot()
	if !limiter.TryAcquireDelaySlot() {
		t.Fatal("expected slot to be acquired after release")
	}

	// Cleanup
	limiter.ReleaseDelaySlot()
	limiter.ReleaseDelaySlot()
}

func TestRateLimiterNoThrottleDelay(t *testing.T) {
	limiter := NewRateLimiter(RateLimiterConfig{
		Name: "test", Enabled: true, RequestsPerMin: 1, BanDuration: 1 * time.Minute,
	})

	if limiter.ThrottleDelay() != 0 {
		t.Fatal("expected no throttle delay")
	}
	if limiter.TryAcquireDelaySlot() {
		t.Fatal("expected TryAcquireDelaySlot to return false when delay is disabled")
	}
}

func TestRateLimiterAdminMethods(t *testing.T) {
	limiter := NewRateLimiter(RateLimiterConfig{
		Name: "test", Enabled: true, RequestsPerMin: 10, BanDuration: 1 * time.Minute,
	})

	if limiter.Name() != "test" {
		t.Fatalf("expected name 'test', got %q", limiter.Name())
	}

	status := limiter.GetStatus()
	if !status.Enabled || status.RequestsPerMin != 10 {
		t.Fatalf("unexpected status: %+v", status)
	}

	limiter.SetRequestsPerMin(20)
	status = limiter.GetStatus()
	if status.RequestsPerMin != 20 {
		t.Fatalf("expected RPM 20 after SetRequestsPerMin, got %d", status.RequestsPerMin)
	}

	limiter.Disable()
	if ok, _ := limiter.Allow("key"); !ok {
		t.Fatal("expected Allow to pass when disabled")
	}

	limiter.Enable()
	// Should be enabled again — exhaust the limit
	for i := 0; i < 20; i++ {
		limiter.Allow("key2")
	}
	if ok, _ := limiter.Allow("key2"); ok {
		t.Fatal("expected Allow to block after re-enable and exceeding limit")
	}
}
