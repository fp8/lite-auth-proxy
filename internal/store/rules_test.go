package store

import (
	"sync"
	"testing"
	"time"
)

func TestMemoryRuleStore_SetRule_AllowUnderLimit(t *testing.T) {
	s := NewMemoryRuleStore()
	defer s.Stop()

	rule := &Rule{
		RuleID:          "test-1",
		TargetHost:      "example.com",
		Action:          "throttle",
		MaxRPM:          10,
		DurationSeconds: 60,
	}
	if err := s.SetRule(rule); err != nil {
		t.Fatalf("SetRule failed: %v", err)
	}

	if !s.ShouldAllow("example.com", "/") {
		t.Error("first request should be allowed")
	}
}

func TestMemoryRuleStore_SetRule_BlockOverLimit(t *testing.T) {
	s := NewMemoryRuleStore()
	defer s.Stop()

	rule := &Rule{
		RuleID:          "test-block",
		TargetHost:      "blocked.com",
		Action:          "block",
		DurationSeconds: 60,
	}
	if err := s.SetRule(rule); err != nil {
		t.Fatalf("SetRule failed: %v", err)
	}

	if s.ShouldAllow("blocked.com", "/") {
		t.Error("blocked host should not be allowed")
	}
}

func TestMemoryRuleStore_RemoveAll(t *testing.T) {
	s := NewMemoryRuleStore()
	defer s.Stop()

	for i := 0; i < 3; i++ {
		_ = s.SetRule(&Rule{
			RuleID:          "rule-" + string(rune('a'+i)),
			TargetHost:      "example.com",
			Action:          "block",
			DurationSeconds: 60,
		})
	}

	count := s.RemoveAll()
	if count != 3 {
		t.Errorf("expected 3 removed, got %d", count)
	}

	if !s.ShouldAllow("example.com", "/") {
		t.Error("should allow after RemoveAll")
	}
}

func TestMemoryRuleStore_ExpiredRule_Allows(t *testing.T) {
	s := NewMemoryRuleStore()
	defer s.Stop()

	rule := &Rule{
		RuleID:          "short-lived",
		TargetHost:      "example.com",
		Action:          "block",
		DurationSeconds: 1,
	}
	if err := s.SetRule(rule); err != nil {
		t.Fatalf("SetRule failed: %v", err)
	}

	if s.ShouldAllow("example.com", "/") {
		t.Error("should block before expiry")
	}

	time.Sleep(1100 * time.Millisecond)

	if !s.ShouldAllow("example.com", "/") {
		t.Error("should allow after expiry")
	}
}

func TestMemoryRuleStore_ConcurrentAccess(t *testing.T) {
	s := NewMemoryRuleStore()
	defer s.Stop()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.SetRule(&Rule{
				RuleID:          "concurrent",
				TargetHost:      "example.com",
				Action:          "throttle",
				MaxRPM:          1000,
				DurationSeconds: 60,
			})
			s.ShouldAllow("example.com", "/")
			s.GetStatus()
		}()
	}
	wg.Wait()
}

func TestMemoryRuleStore_EmptyRuleID(t *testing.T) {
	s := NewMemoryRuleStore()
	defer s.Stop()

	err := s.SetRule(&Rule{
		TargetHost:      "example.com",
		Action:          "block",
		DurationSeconds: 60,
	})
	if err == nil {
		t.Error("expected error for empty ruleId")
	}
}
