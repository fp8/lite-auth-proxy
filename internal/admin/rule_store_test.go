package admin

import (
	"sync"
	"testing"
	"time"
)

func TestRuleStore_SetRule_AllowUnderLimit(t *testing.T) {
	s := NewRuleStore()
	defer s.Stop()

	rule := &Rule{
		RuleID:          "r1",
		TargetHost:      "api.example.com",
		Action:          "throttle",
		MaxRPM:          10,
		DurationSeconds: 600,
	}
	if err := s.SetRule(rule); err != nil {
		t.Fatalf("SetRule: %v", err)
	}

	// First 10 requests should be allowed
	for i := 0; i < 10; i++ {
		if !s.ShouldAllow("api.example.com", "/") {
			t.Fatalf("request %d should be allowed (under limit)", i+1)
		}
	}
}

func TestRuleStore_SetRule_BlockOverLimit(t *testing.T) {
	s := NewRuleStore()
	defer s.Stop()

	rule := &Rule{
		RuleID:          "r1",
		TargetHost:      "api.example.com",
		Action:          "throttle",
		MaxRPM:          3,
		DurationSeconds: 600,
	}
	if err := s.SetRule(rule); err != nil {
		t.Fatalf("SetRule: %v", err)
	}

	for i := 0; i < 3; i++ {
		if !s.ShouldAllow("api.example.com", "/") {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}
	if s.ShouldAllow("api.example.com", "/") {
		t.Fatal("4th request should be blocked (over limit)")
	}
}

func TestRuleStore_ExpiredRule_Allows(t *testing.T) {
	s := NewRuleStore()
	defer s.Stop()

	rule := &Rule{
		RuleID:          "r-exp",
		TargetHost:      "api.example.com",
		Action:          "block",
		DurationSeconds: 1,
	}
	if err := s.SetRule(rule); err != nil {
		t.Fatalf("SetRule: %v", err)
	}

	// Should be blocked immediately
	if s.ShouldAllow("api.example.com", "/") {
		t.Fatal("should be blocked before expiry")
	}

	// Wait for rule to expire
	time.Sleep(1100 * time.Millisecond)

	// Should be allowed after expiry
	if !s.ShouldAllow("api.example.com", "/") {
		t.Fatal("should be allowed after rule expiry")
	}
}

func TestRuleStore_RemoveAll(t *testing.T) {
	s := NewRuleStore()
	defer s.Stop()

	for i := 0; i < 3; i++ {
		rule := &Rule{
			RuleID:          "rule" + string(rune('0'+i)),
			TargetHost:      "api.example.com",
			Action:          "block",
			DurationSeconds: 600,
		}
		_ = s.SetRule(rule)
	}

	count := s.RemoveAll()
	if count != 3 {
		t.Fatalf("expected 3 rules removed, got %d", count)
	}

	statuses := s.GetStatus()
	if len(statuses) != 0 {
		t.Fatalf("expected 0 active rules after RemoveAll, got %d", len(statuses))
	}
}

func TestRuleStore_ConcurrentAccess(t *testing.T) {
	s := NewRuleStore()
	defer s.Stop()

	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func(n int) {
			defer wg.Done()
			rule := &Rule{
				RuleID:          "rule",
				TargetHost:      "api.example.com",
				Action:          "throttle",
				MaxRPM:          1000,
				DurationSeconds: 60,
			}
			_ = s.SetRule(rule)
			_ = s.ShouldAllow("api.example.com", "/")
			_ = s.GetStatus()
		}(i)
	}
	wg.Wait()
}
