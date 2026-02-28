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

func TestLimiterAllowsWithinRate(t *testing.T) {
	clock := &fakeClock{now: time.Unix(0, 0)}
	limiter := newLimiterWithClock(true, 2, 2*time.Minute, clock.Now)

	if ok, _ := limiter.Allow("1.2.3.4"); !ok {
		t.Fatal("expected first request to be allowed")
	}
	if ok, _ := limiter.Allow("1.2.3.4"); !ok {
		t.Fatal("expected second request to be allowed")
	}
}

func TestLimiterBansOnExceed(t *testing.T) {
	clock := &fakeClock{now: time.Unix(0, 0)}
	limiter := newLimiterWithClock(true, 1, 5*time.Minute, clock.Now)

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

func TestLimiterBanExpires(t *testing.T) {
	clock := &fakeClock{now: time.Unix(0, 0)}
	limiter := newLimiterWithClock(true, 1, 1*time.Minute, clock.Now)

	limiter.Allow("1.2.3.4")
	limiter.Allow("1.2.3.4")

	clock.Add(61 * time.Second)

	if ok, _ := limiter.Allow("1.2.3.4"); !ok {
		t.Fatal("expected request to be allowed after ban expires")
	}
}

func TestLimiterDisabledAllowsAll(t *testing.T) {
	clock := &fakeClock{now: time.Unix(0, 0)}
	limiter := newLimiterWithClock(false, 1, 1*time.Minute, clock.Now)

	if ok, _ := limiter.Allow("1.2.3.4"); !ok {
		t.Fatal("expected request to be allowed when limiter disabled")
	}
	if ok, _ := limiter.Allow("1.2.3.4"); !ok {
		t.Fatal("expected request to be allowed when limiter disabled")
	}
}
